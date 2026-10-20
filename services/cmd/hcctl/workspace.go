package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var errWorkspace = errors.New("workspace operation requires controlled private configuration, current schema, operator identity and settled lifecycle writers")

func writeWorkspaceFile(path string, raw []byte) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errWorkspace
	}
	f, err := os.CreateTemp(dir, ".workspace-")
	if err != nil {
		return errWorkspace
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errWorkspace
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return errWorkspace
	}
	return nil
}

type workspaceControl struct {
	Organization string `json:"organization_id"`
	Control      string `json:"control_user_id"`
}

func workspacePool(ctx context.Context) (*pgxpool.Pool, error) {
	if os.Getenv("HC_OPERATOR_REF") == "" {
		return nil, errWorkspace
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return nil, errWorkspace
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, errWorkspace
	}
	var role string
	if err = pool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role == "hc_api" || role == "hc_worker" || role == "hc_gateway" {
		pool.Close()
		return nil, errWorkspace
	}
	if err = db.Check(ctx, pool); err != nil {
		pool.Close()
		return nil, errWorkspace
	}
	return pool, nil
}
func runWorkspace(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := workspacePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	root, err := filepath.Abs(".local/workspace")
	if err != nil {
		return errWorkspace
	}
	if err = os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return errWorkspace
	}
	if name == "workspace-names-migrate" {
		return runWorkspaceNames(ctx, pool, root)
	}
	var authority workspaceControl
	if name == "workspace-init" {
		org, err := config.ReadSecret(".local/auth/organization-id")
		if err != nil {
			return errWorkspace
		}
		control := webapi.ID()
		if raw, e := config.ReadPrivateFile(filepath.Join(root, "control.json")); e == nil {
			var previous workspaceControl
			if webapi.StrictUnmarshal(raw, &previous) != nil || previous.Organization != org {
				return errWorkspace
			}
			control = previous.Control
		} else if _, statErr := os.Lstat(filepath.Join(root, "control.json")); !errors.Is(statErr, os.ErrNotExist) {
			return errWorkspace
		}
		if _, err = pool.Exec(ctx, `INSERT INTO workspace_authority(singleton,organization_id,control_user_id) VALUES(true,$1,$2) ON CONFLICT(singleton) DO NOTHING`, org, control); err != nil {
			return errWorkspace
		}
	}
	if err = pool.QueryRow(ctx, `SELECT organization_id::text,control_user_id::text FROM workspace_authority WHERE singleton`).Scan(&authority.Organization, &authority.Control); err != nil {
		return errWorkspace
	}
	if name == "workspace-init" {
		raw, _ := json.MarshalIndent(authority, "", "  ")
		return writeWorkspaceFile(filepath.Join(root, "control.json"), raw)
	}
	if name != "workspace-template" {
		return errWorkspace
	}
	client, err := workspaceengine.NewEmbedded(pool, authority.Organization, authority.Control)
	if err != nil {
		return errWorkspace
	}
	raw, err := config.ReadPrivateFile(filepath.Join(root, "template-variables.json"))
	if err != nil {
		return errWorkspace
	}
	var variables struct {
		Image string `json:"runtime_image"`
		Root  string `json:"runtime_root"`
	}
	if webapi.StrictUnmarshal(raw, &variables) != nil || len(variables.Image) != 71 || !filepath.IsAbs(variables.Root) || filepath.Clean(variables.Root) != variables.Root {
		return errWorkspace
	}
	image, _ := json.Marshal(variables.Image)
	runtimeRoot, _ := json.Marshal(variables.Root)
	canonical := []byte(fmt.Sprintf(`{"runtime_image": %s, "runtime_root": %s}`, image, runtimeRoot))
	source := map[string][]byte{"variables.json": canonical}
	for _, k := range []string{"main.tf", ".terraform.lock.hcl"} {
		source[k], err = os.ReadFile(filepath.Join("infrastructure/terraform", k))
		if err != nil {
			return errWorkspace
		}
	}
	content := append(append(append([]byte{}, source["main.tf"]...), source[".terraform.lock.hcl"]...), canonical...)
	sha := runtimeapproval.SHA(content)
	template := webapi.ID()
	version, file := webapi.ID(), webapi.ID()
	// A database reset keeps private local configuration and its frozen manifest.
	// Reuse matching identifiers so restoring the template can verify that manifest.
	if raw, e := config.ReadPrivateFile(filepath.Join(root, "template-contract.json")); e == nil {
		var previous runtimeapproval.Contract
		if webapi.StrictUnmarshal(raw, &previous) == nil && previous.OrganizationID == authority.Organization && previous.SourceSHA == sha && previous.Image == variables.Image {
			template, version = previous.TemplateID, previous.TemplateVersion
		}
	}
	if err = pool.QueryRow(ctx, `SELECT id::text FROM workspace_templates WHERE organization_id=$1 AND name='hc-dsh-initial'`, authority.Organization).Scan(&template); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errWorkspace
	}
	if err = pool.QueryRow(ctx, `SELECT id::text,file_id::text FROM workspace_template_versions WHERE template_id=$1 AND name=$2`, template, "v0-"+sha[:20]).Scan(&version, &file); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errWorkspace
	}
	if err = client.PublishTemplate(ctx, template, version, file, "v0-"+sha[:20], source); err != nil {
		return errWorkspace
	}
	contract := runtimeapproval.Contract{TemplateID: template, TemplateVersion: version, OrganizationID: authority.Organization, WorkspaceSourceVersion: workspaceengine.Version, TerraformVersion: runtimeapproval.TerraformVersion, WorkspaceProviderVersion: "2.19.0", DockerProvider: "4.6.0", SourceSHA: sha, Image: variables.Image}
	raw, _ = json.MarshalIndent(contract, "", "  ")
	return writeWorkspaceFile(filepath.Join(root, "template-contract.json"), raw)
}
