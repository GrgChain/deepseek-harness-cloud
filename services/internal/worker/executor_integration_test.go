package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// The real Workspace write succeeds, then its response is deliberately lost. The
// recovered lease must find the durable successor instead of sending it again.
type lostStartResponse struct {
	*workspaceengine.Client
	armed  bool
	writes int
}

func (c *lostStartResponse) CreateBuild(ctx context.Context, workspace, transition, version string, parameters []workspaceengine.Parameter) (workspaceengine.Build, error) {
	if c.armed && transition == "start" {
		c.writes++
		build, err := c.Client.CreateBuild(ctx, workspace, transition, version, parameters)
		if err != nil {
			return build, err
		}
		c.armed = false
		return workspaceengine.Build{}, workspaceengine.ErrUncertain
	}
	if transition == "start" {
		c.writes++
	}
	return c.Client.CreateBuild(ctx, workspace, transition, version, parameters)
}

// This real-runtime fixture scans only its own instance; unrelated synthetic
// lease fixtures in the same isolated test database have no external resources.
type ownedInstanceScanner struct {
	store *SafetyStore
	id    string
}

func (s ownedInstanceScanner) Scan(ctx context.Context, _ string, _ int) (string, error) {
	return "", s.store.reconcile(ctx, s.id)
}

type actualHostAdmission struct{ host *LocalHost }

func (g actualHostAdmission) Check(context.Context, identity.Principal) error {
	return g.host.Capacity()
}
func TestDurableExecutorRunsEmbeddedLifecycleAndRetainsData(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated worker PostgreSQL database required")
	}
	options, contract, client, workspaceOwner := realHostContract(t)
	ingress := httptest.NewUnstartedServer(nil)
	defer ingress.Close()
	options.Domain = "harness.localhost"
	options.PublicPort = ingress.Listener.Addr().(*net.TCPAddr).Port
	host, err := NewLocalHost(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	connection, _ := url.Parse(strings.TrimSpace(string(raw)))
	connection.Path = "/hc_test_worker"
	if err = db.Apply(ctx, connection.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantGateway(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	connectRole := func(role string) *pgxpool.Pool {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), role+"-dsn"))
		if err != nil {
			t.Fatal("restricted role connection unavailable")
		}
		parsed, _ := url.Parse(strings.TrimSpace(string(data)))
		parsed.Path = connection.Path
		pool, err := pgxpool.New(ctx, parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		return pool
	}
	apiPool, workerPool := connectRole("api"), connectRole("worker")
	gatewayPool := connectRole("gateway")
	defer gatewayPool.Close()
	defer apiPool.Close()
	defer workerPool.Close()
	must(`UPDATE outbox_events SET status='DONE' WHERE status='PENDING'`)
	must(`UPDATE operations SET status='FAILED',lease_owner=NULL,lease_expires_at=NULL WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN')`)
	org := "00000000-0000-4000-8000-000000000061"
	user, session, model, secret, spec := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Executor fixture','ACTIVE') ON CONFLICT(id) DO NOTHING`, org)
	if err := admin.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://executor.test',$2,$3,'ACTIVE') ON CONFLICT(workspace_owner_id) DO UPDATE SET status='ACTIVE' RETURNING id::text`, user, user, workspaceOwner).Scan(&user); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE') ON CONFLICT(org_id,user_id) DO UPDATE SET status='ACTIVE'`, org, user)
	var generation int64
	if err := admin.QueryRow(ctx, `SELECT auth_generation FROM memberships WHERE org_id=$1 AND user_id=$2`, org, user).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(session))
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,$5,$2,'x','fixture',$6,clock_timestamp()+interval '1 hour')`, session, hash[:], org, user, generation, bytes.Repeat([]byte{0}, 12))
	principal := identity.Principal{UserID: user, OrganizationID: org, SessionID: session, Role: "MEMBER", Generation: generation}
	cipher, _ := security.NewSecretCipher("test", bytes.Repeat([]byte{9}, 32))
	sealed, err := cipher.Encrypt([]byte("synthetic-executor-model-key-no-inference"), security.Context{Environment: "test", OrganizationID: org, RecordID: secret, Purpose: "model-key"})
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, secret, org, sealed.KeyID, sealed.Nonce, sealed.Ciphertext)
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, model, org)
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, org).Scan(&model); err != nil {
		t.Fatal(err)
	}
	var modelVersion int64
	if err := admin.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM model_configurations WHERE id=$1`, model).Scan(&modelVersion); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','deepseek-flash',$4,'VALID')`, org, model, modelVersion, secret)
	must(`INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,$2,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, model, modelVersion)
	must(`UPDATE model_configuration_heads SET current_version=$2 WHERE id=$1`, model, modelVersion)
	must(`UPDATE approved_templates SET enabled=false WHERE enabled`)
	must(`UPDATE approved_specs SET enabled=false WHERE enabled`)
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'real-executor',$3,'{"schema_version":1}',true) ON CONFLICT(id,version) DO UPDATE SET enabled=true`, contract.TemplateID, contract.TemplateVersion, contract.ImageDigest)
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,2000,4294967296,10737418240,true)`, spec)
	must(`INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,model_config_id,model_config_version) VALUES($1,1,$2,$3,$4,$5,$6) ON CONFLICT(org_id) DO UPDATE SET version=organization_defaults.version+1,template_id=EXCLUDED.template_id,template_version=EXCLUDED.template_version,spec_id=EXCLUDED.spec_id,model_config_id=EXCLUDED.model_config_id,model_config_version=EXCLUDED.model_config_version`, org, contract.TemplateID, contract.TemplateVersion, spec, model, modelVersion)
	must(`INSERT INTO quota_policies(org_id,max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes,max_concurrent_creates) VALUES($1,10000,2,20000000,42949672960000,107374182400000,10000) ON CONFLICT(org_id) DO UPDATE SET max_instances=10000,max_user_instances=2,cpu_millicores=20000000,memory_bytes=42949672960000,storage_bytes=107374182400000,max_concurrent_creates=10000`, org)
	commands := instances.NewCommandService(apiPool, actualHostAdmission{host}, false)
	control, err := client.User(ctx, "me")
	if err != nil || len(control.OrganizationIDs) != 1 {
		t.Fatal("controlled Workspace identity unavailable")
	}
	leases := NewLeaseStore(workerPool)
	executor, err := NewExecutor(NewExecutionStore(workerPool, cipher, "test"), leases, client, host, control.OrganizationIDs[0], control.ID)
	if err != nil {
		t.Fatal(err)
	}
	codec, _ := pagination.NewCodec(bytes.Repeat([]byte{9}, 32), "test")
	queries := instances.NewQueryService(apiPool, codec)
	accepted, err := commands.Create(ctx, principal, webapi.ID(), instances.CreateInput{}, "real-executor")
	if err != nil {
		t.Fatal(err)
	}
	instance := accepted.InstanceID
	var plan ExecutionPlan
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		workspace, _, findErr := executor.lookup(cleanup, plan, true)
		if findErr != nil && !errors.Is(findErr, workspaceengine.ErrNotFound) {
			t.Error("owned executor fixture Workspace absence remains uncertain; retaining data")
			return
		}
		if findErr == nil && workspace.LatestBuild.Transition != "delete" {
			build, err := client.CreateBuild(cleanup, workspace.ID, "delete", contract.TemplateVersion, nil)
			if err != nil {
				t.Error("owned executor fixture cleanup rejected")
				return
			}
			if _, err = executor.waitBuild(cleanup, plan, workspace, build.ID); err != nil {
				t.Error("owned executor fixture delete did not settle")
				return
			}
		}
		if plan.AssetID == "" {
			return
		}
		if _, err := host.ConfirmAbsent(cleanup, plan, true); err != nil {
			t.Error("owned executor fixture compute remains; retaining data")
			return
		}
		_, assetErr := os.Lstat(filepath.Join(options.AssetRoot, plan.AssetID))
		if assetErr != nil && !os.IsNotExist(assetErr) {
			t.Error("owned fixture asset absence uncertain")
			return
		}
		if !os.IsNotExist(assetErr) {
			volume, err := dockerCommand(cleanup, nil, "volume", "ls", "--filter", "name=^"+plan.ResourceName+"$", "--format", "{{.Name}}")
			if err != nil {
				t.Error("owned fixture volume absence uncertain")
				return
			}
			if strings.TrimSpace(string(volume)) != "" {
				if _, err := dockerCommand(cleanup, nil, "volume", "rm", plan.ResourceName); err != nil {
					t.Error("owned executor fixture volume remains; retaining data")
					return
				}
			}
			script := "import os,subprocess,shutil; p='/host-dev/hc-cloud/'+os.environ['ASSET']\nif os.path.islink(p):\n d=os.readlink(p); subprocess.run(['losetup','-d',d],check=True); os.unlink(p)\na='/assets/'+os.environ['ASSET']\nif os.path.isdir(a): shutil.rmtree(a)"
			if _, err := dockerCommand(cleanup, nil, "run", "--rm", "--network", "none", "--privileged", "--env", "ASSET="+plan.AssetID, "--mount", "type=bind,src="+options.AssetRoot+",dst=/assets", "--mount", "type=bind,src=/dev,dst=/host-dev", "--entrypoint", "python3", options.StorageImage, "-c", script); err != nil {
				t.Error("owned executor fixture asset cleanup failed")
				return
			}
		}
		os.RemoveAll(filepath.Join(options.RuntimeRoot, instance))
		os.Remove(filepath.Join(options.RuntimeRoot, ".locks", instance))
		// Only this isolated fixture is released after confirmed external removal.
		tx, err := admin.Begin(cleanup)
		if err != nil {
			t.Error("fixture teardown ledger unavailable")
			return
		}
		defer tx.Rollback(context.Background())
		for _, sql := range []string{`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=COALESCE(deleted_at,clock_timestamp()) WHERE id=$1`, `UPDATE storage_assets SET status='DELETED' WHERE instance_id=$1`, `UPDATE quota_reservations SET status='RELEASED',updated_at=clock_timestamp() WHERE instance_id=$1`, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE instance_id=$1 AND revoked_at IS NULL`, `DELETE FROM native_sessions WHERE instance_id=$1`} {
			if _, err = tx.Exec(cleanup, sql, instance); err != nil {
				t.Error("fixture teardown ledger failed")
				return
			}
		}
		if err = tx.Commit(cleanup); err != nil {
			t.Error("fixture teardown ledger commit failed")
		}
	})
	execute := func(accepted instances.Accepted) instances.InstanceView {
		t.Helper()
		claimed, err := leases.Claim(ctx, "real-executor", 1, false)
		if err != nil || len(claimed) != 1 || claimed[0].ID != accepted.OperationID {
			t.Fatalf("durable claim: %v %v", claimed, err)
		}
		before, err := executor.store.CleanupPlan(ctx, claimed[0])
		if err != nil {
			t.Fatal(err)
		}
		plan = before
		if err = executor.Execute(ctx, claimed[0]); err != nil {
			t.Fatal(err)
		}
		operation, err := queries.Operation(ctx, principal, accepted.OperationID)
		if err != nil || operation.Status != "SUCCEEDED" {
			t.Fatalf("operation did not succeed: status=%s stage=%s error=%+v query=%v", operation.Status, operation.Stage, operation.Error, err)
		}
		view, err := queries.Get(ctx, principal, instance)
		if err != nil {
			t.Fatal(err)
		}
		if view.WorkspaceID != nil {
			plan.WorkspaceID = view.WorkspaceID
		}
		return view
	}
	view := execute(accepted)
	if !view.Ready || !view.CanEnter || view.Observed != "RUNNING" || view.Storage == nil {
		t.Fatal("actual runtime was not published ready")
	}
	healthStore := NewHealthStore(workerPool, cipher, "test", options.PublicPort)
	healthChecker := NewRuntimeHealthChecker(executor)
	probeHealth := func(renew bool) {
		t.Helper()
		if renew {
			must(`UPDATE native_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE instance_id=$1`, instance)
		}
		healthPlan, err := healthStore.Load(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		defer healthPlan.Destroy()
		result, err := healthChecker.Check(ctx, healthPlan)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Destroy()
		for dimension, value := range result.Checks {
			if value.Status != instances.Healthy {
				t.Fatalf("actual %s health failed: %s", dimension, value.Reason)
			}
		}
		if renew && len(result.cookie) == 0 {
			t.Fatal("expired private native cookie was not renewed")
		}
		if saved, err := healthStore.Publish(ctx, healthPlan, result); err != nil || !saved {
			t.Fatalf("actual health result not published: %v", err)
		}
	}
	probeHealth(false)
	probeHealth(true)
	verifyRealGateway(t, ctx, ingress, admin, apiPool, gatewayPool, cipher, client, principal, options, instance, control.OrganizationIDs[0], control.ID)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- NewMonitor(healthStore, healthChecker, ownedInstanceScanner{NewSafetyStore(workerPool), instance}).Run(monitorCtx)
	}()
	monitoringStart := time.Now()
	for time.Since(monitoringStart) < 62*time.Second {
		select {
		case err := <-monitorDone:
			stopMonitor()
			t.Fatalf("live health monitor stopped early: %v", err)
		case <-ctx.Done():
			stopMonitor()
			<-monitorDone
			t.Fatal("live health monitoring deadline")
		case <-time.After(500 * time.Millisecond):
		}
	}
	stopMonitor()
	if err := <-monitorDone; err != nil {
		t.Fatal(err)
	}
	continuouslyReady, err := queries.Get(ctx, principal, instance)
	if err != nil || !continuouslyReady.Ready || !continuouslyReady.CanEnter || continuouslyReady.Version != view.Version || continuouslyReady.Health[instances.Application].CheckedAt == nil || !continuouslyReady.Health[instances.Application].CheckedAt.After(monitoringStart.Add(28*time.Second)) {
		t.Fatal("periodic real health did not preserve readiness beyond the initial 60-second TTL")
	}
	cleanProbe, err := dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "process.stdout.write(String(require('fs').readdirSync('/data/.hc-health').length))")
	if err != nil || string(cleanProbe) != "0" {
		t.Fatal("storage health probe left files in its private probe directory")
	}
	fileProof := "retained-durable-worker-file"
	if _, err = dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "require('fs').writeFileSync('/data/workspace/executor-proof.txt',process.argv[1],{mode:0o600})", fileProof); err != nil {
		t.Fatal(err)
	}
	stop, err := commands.Command(ctx, principal, instance, instances.Stop, webapi.ID(), instances.CommandInput{}, "real-stop")
	if err != nil {
		t.Fatal(err)
	}
	view = execute(stop)
	if view.Observed != "STOPPED" || view.Ready || view.CanEnter {
		t.Fatal("confirmed stop retained a ready route")
	}
	failedStart, err := commands.Command(ctx, principal, instance, instances.Start, webapi.ID(), instances.CommandInput{}, "real-retry-controlled-failure")
	if err != nil {
		t.Fatal(err)
	}
	failedLease, err := leases.Claim(ctx, "controlled-failure-worker", 1, false)
	if err != nil || len(failedLease) != 1 || failedLease[0].ID != failedStart.OperationID {
		t.Fatal("controlled failed-start lease missing", err)
	}
	// Stop above proved compute absent and the original filesystem ready. Fail
	// before any new Workspace write to exercise a determinate Worker interruption.
	var originalFilesystem string
	if err = admin.QueryRow(ctx, `SELECT filesystem_id FROM storage_assets WHERE instance_id=$1`, instance).Scan(&originalFilesystem); err != nil {
		t.Fatal(err)
	}
	if err = executor.store.Fail(ctx, failedLease[0], "DEPENDENCY_UNAVAILABLE", Completion{ComputeAbsent: true, AssetReady: true, FilesystemID: originalFilesystem}); err != nil {
		t.Fatal(err)
	}
	failedView, err := queries.Operation(ctx, principal, failedStart.OperationID)
	if err != nil || failedView.Status != "FAILED" || !failedView.RetryAllowed {
		t.Fatal("reconciled real failure was not retryable", err)
	}
	retried, err := commands.Retry(ctx, principal, failedStart.OperationID, webapi.ID(), "real-retry", func(context.Context, string, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	originalWorkspace, originalAsset := *view.WorkspaceID, *view.AssetID
	view = execute(retried)
	if !view.Ready || view.WorkspaceID == nil || *view.WorkspaceID != originalWorkspace || view.AssetID == nil || *view.AssetID != originalAsset {
		t.Fatal("real retry replaced Workspace or original asset")
	}
	if output, err := dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "process.stdout.write(require('fs').readFileSync('/data/workspace/executor-proof.txt','utf8'))"); err != nil || string(output) != fileProof {
		t.Fatal("real retry lost retained file", err)
	}
	restop, err := commands.Command(ctx, principal, instance, instances.Stop, webapi.ID(), instances.CommandInput{}, "real-retry-stop")
	if err != nil {
		t.Fatal(err)
	}
	view = execute(restop)
	start, err := commands.Command(ctx, principal, instance, instances.Start, webapi.ID(), instances.CommandInput{}, "real-start")
	if err != nil {
		t.Fatal(err)
	}
	fault := &lostStartResponse{Client: client, armed: true}
	executor.workspaces = fault
	first, err := leases.Claim(ctx, "response-lost-worker", 1, false)
	if err != nil || len(first) != 1 || first[0].ID != start.OperationID {
		t.Fatal("first restart lease missing")
	}
	if err = executor.Execute(ctx, first[0]); err != nil {
		t.Fatal(err)
	}
	uncertain, err := queries.Operation(ctx, principal, start.OperationID)
	if err != nil || uncertain.Status != "UNCERTAIN" {
		t.Fatal("lost Workspace response did not retain an uncertain operation")
	}
	var held int
	var enabled bool
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM quota_reservations WHERE operation_id=$1 AND status='HELD' AND resource_type IN ('CPU','MEMORY','RUNNING','CONCURRENT')`, start.OperationID).Scan(&held); err != nil || held != 4 {
		t.Fatal("unknown external outcome released reserved capacity")
	}
	if err = admin.QueryRow(ctx, `SELECT enabled FROM access_routes WHERE instance_id=$1`, instance).Scan(&enabled); err != nil || enabled {
		t.Fatal("unknown external outcome enabled the route")
	}
	// Advance only this isolated fixture's backoff; production uses database time.
	must(`UPDATE outbox_events SET retry_at=clock_timestamp() WHERE operation_id=$1`, start.OperationID)
	recoveryCtx, stopRecovery := context.WithCancel(ctx)
	recovered := make(chan error, 1)
	go func() { recovered <- NewRunner(leases, executor).Run(recoveryCtx) }()
	recoveryDeadline := time.Now().Add(45 * time.Second)
	for {
		operation, queryErr := queries.Operation(ctx, principal, start.OperationID)
		if queryErr != nil {
			stopRecovery()
			<-recovered
			t.Fatal(queryErr)
		}
		if operation.Status == "SUCCEEDED" {
			break
		}
		if operation.Status == "FAILED" || time.Now().After(recoveryDeadline) {
			stopRecovery()
			<-recovered
			t.Fatal("recovered durable runner did not settle the original operation")
		}
		time.Sleep(100 * time.Millisecond)
	}
	stopRecovery()
	if err = <-recovered; err != nil {
		t.Fatal(err)
	}
	if fault.writes != 1 {
		t.Fatal("recovery blindly repeated the successful Workspace write")
	}
	var token int64
	if err = admin.QueryRow(ctx, `SELECT lease_token FROM operations WHERE id=$1`, start.OperationID).Scan(&token); err != nil || token != 2 {
		t.Fatal("recovery did not use a new fenced lease")
	}
	executor.workspaces = client
	view, err = queries.Get(ctx, principal, instance)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Ready || view.Storage.AssetID != plan.AssetID {
		t.Fatal("original storage was not restarted")
	}
	actual, err := dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "process.stdout.write(require('fs').readFileSync('/data/workspace/executor-proof.txt'))")
	if err != nil || string(actual) != fileProof {
		t.Fatal("original data changed across durable stop/start")
	}
	nextVersion := modelVersion + 1
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','deepseek-flash',$4,'VALID')`, org, model, nextVersion, secret)
	must(`INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,$2,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, model, nextVersion)
	configured, err := commands.Command(ctx, principal, instance, instances.Configure, webapi.ID(), instances.CommandInput{ExpectedVersion: &view.Version, ModelID: &model, ModelVersion: &nextVersion}, "real-configure")
	if err != nil {
		t.Fatal(err)
	}
	view = execute(configured)
	if !view.Ready || view.ModelVersion != nextVersion {
		t.Fatal("explicit configuration restart did not apply the target model version")
	}
	// A real platform revocation is compensated through the independent safety
	// lane, without requiring the disabled Owner's Portal session or model key.
	must(`UPDATE memberships SET status='DISABLED',auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, org, user)
	if err = NewSafetyStore(workerPool).reconcile(ctx, instance); err != nil {
		t.Fatal(err)
	}
	safetyLeases, err := leases.Claim(ctx, "real-safety-worker", 1, true)
	if err != nil || len(safetyLeases) != 1 || safetyLeases[0].InstanceID != instance {
		t.Fatal("revocation compensation was not claimed in the safety lane")
	}
	if err = executor.Execute(ctx, safetyLeases[0]); err != nil {
		t.Fatal(err)
	}
	var stoppedState string
	if err = admin.QueryRow(ctx, `SELECT observed_state FROM instances WHERE id=$1`, instance).Scan(&stoppedState); err != nil || stoppedState != "STOPPED" {
		t.Fatal("disabled Owner's actual compute was not safely stopped")
	}
	if _, err = host.ConfirmAbsent(ctx, plan, false); err != nil {
		t.Fatal("revocation reported a stop without actual physical absence")
	}
	must(`UPDATE memberships SET status='ACTIVE' WHERE org_id=$1 AND user_id=$2`, org, user)
	if err = admin.QueryRow(ctx, `SELECT auth_generation FROM memberships WHERE org_id=$1 AND user_id=$2`, org, user).Scan(&principal.Generation); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE portal_sessions SET auth_generation=$2 WHERE id=$1`, session, principal.Generation)
	view, err = queries.Get(ctx, principal, instance)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := view.Name
	deleted, err := commands.Command(ctx, principal, instance, instances.Delete, webapi.ID(), instances.CommandInput{ConfirmedName: &confirmed}, "real-delete")
	if err != nil {
		t.Fatal(err)
	}
	view = execute(deleted)
	if view.Observed != "DELETED" || view.CanEnter || view.Storage == nil || view.Storage.Status != "RETAINED" || view.Storage.RetentionUntil == nil {
		t.Fatal("soft delete did not retain original data and revoke access")
	}
	var storage, running int64
	if err := admin.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='RUNNING'),0) FROM quota_reservations WHERE instance_id=$1 AND status IN ('HELD','USED')`, instance).Scan(&storage, &running); err != nil || storage != 10<<30 || running != 0 {
		t.Fatal("retained asset or released compute quota mismatched")
	}
	// No actual model request is made by this fixture. The real paid model
	// contract is separately opt-in and uses its own bounded request budget.
}
