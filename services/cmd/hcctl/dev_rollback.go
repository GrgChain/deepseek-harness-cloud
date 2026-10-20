package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func rollbackLocalRuntime(ctx context.Context, previous, target, reason string, apply bool) error {
	o, err := worker.LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
	if err != nil || o.Environment != "local" || o.WorkspaceEngine != "embedded" || o.Host.StorageBudget != 100<<30 {
		return errLocalApproval
	}
	raw, err := config.ReadPrivateFile(os.Getenv("HC_RUNTIME_CONTRACT_FILE"))
	var c runtimeapproval.Contract
	if err != nil || webapi.StrictUnmarshal(raw, &c) != nil || c.TemplateVersion != target || c.OrganizationID != o.WorkspaceOrganizationID || c.Image != o.Host.RuntimeImage {
		return errLocalApproval
	}
	org, err := config.ReadSecret(".local/auth/organization-id")
	a, accessErr := access.LoadOptions(".local/access-config.json")
	if err != nil || accessErr != nil || a.OrganizationID != org || a.WorkspaceOrganizationID != c.OrganizationID || a.ControlUserID != o.ControlUserID {
		return errLocalApproval
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return errLocalApproval
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errLocalApproval
	}
	defer pool.Close()
	var database string
	if err = pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != "hc_dev" {
		return errLocalApproval
	}
	if err = db.Check(ctx, pool); err != nil {
		return errLocalApproval
	}
	var approvedRaw []byte
	var previousImage string
	if err = pool.QueryRow(ctx, `SELECT runtime_manifest FROM approved_templates WHERE id=$1 AND version=$2`, c.TemplateID, target).Scan(&approvedRaw); err != nil {
		return errLocalApproval
	}
	approved, err := runtimeapproval.Parse(approvedRaw)
	if err != nil {
		return errLocalApproval
	}
	if err = pool.QueryRow(ctx, `SELECT image_digest FROM approved_templates WHERE id=$1 AND version=$2`, c.TemplateID, previous).Scan(&previousImage); err != nil {
		return errLocalApproval
	}
	supported := map[string]bool{o.Host.RuntimeImage: true}
	for _, image := range o.Host.PreviousRuntimeImages {
		supported[image] = true
	}
	if !supported[previousImage] {
		return errLocalApproval
	}
	rows, err := pool.Query(ctx, `SELECT config_snapshot->>'image_digest' FROM instances WHERE observed_state<>'DELETED'`)
	if err != nil {
		return errLocalApproval
	}
	for rows.Next() {
		var image string
		if err = rows.Scan(&image); err != nil || !supported[image] {
			rows.Close()
			return errLocalApproval
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errLocalApproval
	}
	client, err := workspaceengine.NewEmbedded(pool, o.WorkspaceOrganizationID, o.ControlUserID)
	if err != nil {
		return errLocalApproval
	}
	host, err := worker.NewLocalHost(o.Host)
	if err != nil {
		return errLocalApproval
	}
	if host.Capacity() != nil || host.Probe(ctx) != nil {
		return errLocalApproval
	}
	sources := map[string][]byte{}
	for _, name := range []string{"main.tf", ".terraform.lock.hcl"} {
		data, err := os.ReadFile(filepath.Join("infrastructure/terraform", name))
		if err != nil || len(data) == 0 || len(data) > 2<<20 {
			return errLocalApproval
		}
		sources[name] = data
	}
	image, err := inspectRuntimeImage(ctx, o.Host.RuntimeImage)
	if err != nil {
		return errLocalApproval
	}
	image.StorageImage, image.NetworkImage = o.Host.StorageImage, o.Host.NetworkImage
	for _, helper := range []struct{ image, path, source, name string }{
		{o.Host.StorageImage, "/opt/harness-storage/helper.py", "infrastructure/storage/helper.py", "storage-helper.py"},
		{o.Host.NetworkImage, "/opt/harness-network/helper.py", "infrastructure/network/helper.py", "network-helper.py"},
	} {
		observed, e := inspectImage(ctx, helper.image, `process.stdout.write(require('node:crypto').createHash('sha256').update(require('node:fs').readFileSync('`+helper.path+`')).digest('hex'))`)
		local, e2 := os.ReadFile(helper.source)
		if e != nil || e2 != nil || string(observed) != runtimeapproval.SHA(local) {
			return errLocalApproval
		}
		sources[helper.name], image.Files[helper.name] = local, string(observed)
	}
	if err = runtimeapproval.VerifyRetained(ctx, client, c, o.ControlUserID, o.Host.RuntimeRoot, previous, sources, image, approved); err != nil {
		return errLocalApproval
	}
	// Preview works before a process restart. Apply also observes the running
	// Worker, preventing an approved default that immediately fails admission.
	if apply {
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(probeCtx, "GET", "http://127.0.0.1:18081/internal/preflight", nil)
		response, err := httpClient.Do(request)
		if err != nil {
			return errLocalApproval
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
		response.Body.Close()
		var facts worker.AdmissionFacts
		if readErr != nil || len(body) > 8192 || response.StatusCode != 200 || webapi.StrictUnmarshal(body, &facts) != nil || facts.SchemaVersion != 1 || facts.RuntimeImage != o.Host.RuntimeImage || !reflect.DeepEqual(facts.PreviousRuntimeImages, o.Host.PreviousRuntimeImages) || facts.StorageImage != o.Host.StorageImage || facts.NetworkImage != o.Host.NetworkImage || facts.Domain != o.Host.Domain || facts.PublicPort != o.Host.PublicPort || !facts.Workspace || !facts.Docker || !facts.Storage {
			return errLocalApproval
		}
	}
	return runtimeapproval.Rollback(ctx, pool, org, c.TemplateID, os.Getenv("HC_OPERATOR_REF"), previous, approved, reason, apply)
}
