package operations

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

// This opt-in test reads only the explicitly identified synthetic product task.
// It backs up its real stopped source through StorageService, restores into an
// independently allocated owned test asset and reads history with the unchanged
// approved runtime. It never changes a product instance or uses a model key.
func TestRealProductNativeHistoryFromEncryptedIndependentAssetRestore(t *testing.T) {
	if os.Getenv("HC_PRODUCT_STORAGE_RESTORE") != "true" {
		t.Skip("explicit stopped product file/history proof and trusted host required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	defer cancel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	readJSON := func(path string, out any) {
		t.Helper()
		raw, err := config.ReadPrivateFile(path)
		if err != nil || json.Unmarshal(raw, out) != nil {
			t.Fatal("controlled proof missing or invalid")
		}
	}
	var proof struct {
		Outcome    string `json:"outcome"`
		InstanceID string `json:"instance_id"`
		File       string `json:"file"`
		FileProof  struct {
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"file_proof"`
	}
	readJSON(os.Getenv("HC_PRODUCT_FILE_PROOF_FILE"), &proof)
	var history struct {
		Outcome    string `json:"outcome"`
		InstanceID string `json:"instance_id"`
		Completed  struct {
			Selected  string   `json:"selected_session"`
			ToolNames []string `json:"tool_names"`
		} `json:"completed_history"`
	}
	readJSON(os.Getenv("HC_PRODUCT_HISTORY_PROOF_FILE"), &history)
	if proof.Outcome != "PASS" || history.Outcome != "PASS" || proof.InstanceID != history.InstanceID || !uuid.MatchString(proof.InstanceID) || !strings.HasPrefix(proof.File, "hc-browser-smoke-") || !strings.HasSuffix(proof.File, ".txt") || !uuid.MatchString(strings.TrimSuffix(strings.TrimPrefix(proof.File, "hc-browser-smoke-"), ".txt")) || !strings.HasPrefix(history.Completed.Selected, "session:session-") || proof.FileProof.Bytes != 19 || proof.FileProof.SHA256 != "6adcb300a84035b1d29128d356b7c91270e5a7e2a79e12ff022ad94e4855a19b" {
		t.Fatal("completed synthetic file/history contract required")
	}
	o, err := worker.LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	host, err := worker.NewLocalHost(o.Host)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_TEST_DB_DSN_FILE"))
	if err != nil {
		t.Fatal("controlled source database missing")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Path != "/hc_dev" {
		t.Fatal("this acceptance reads only the explicit local hc_dev product")
	}
	sourcePool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("source database unavailable")
	}
	defer sourcePool.Close()
	var org, asset string
	if err = sourcePool.QueryRow(ctx, `SELECT org_id::text,storage_asset_id::text FROM instances WHERE id=$1 AND observed_state='STOPPED' AND desired_state='STOPPED'`, proof.InstanceID).Scan(&org, &asset); err != nil {
		t.Fatal("explicit product must be stopped")
	}
	sourceService := NewStorageService(sourcePool, host, org, "native-recovery-acceptance")
	source, err := sourceService.read(ctx, sourcePool, asset)
	if err != nil || CheckBackup(source) != nil {
		t.Fatal("original product asset is not backup eligible")
	}
	beforeState := func() string {
		t.Helper()
		var data []byte
		if err := sourcePool.QueryRow(ctx, `SELECT jsonb_build_object('instance',jsonb_build_array(i.version,i.desired_state,i.observed_state,i.config_snapshot,i.storage_asset_id,i.workspace_id),'asset',jsonb_build_array(a.status,a.filesystem_id,a.capacity_bytes),'quota',(SELECT jsonb_agg(jsonb_build_array(resource_type,amount,status) ORDER BY id) FROM quota_reservations WHERE instance_id=i.id)) FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id WHERE i.id=$1`, proof.InstanceID).Scan(&data); err != nil {
			t.Fatal("source-state query failed")
		}
		return string(data)
	}
	before := beforeState()
	directory := t.TempDir()
	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	archive := filepath.Join(directory, "source.hcbk")
	archiveProof, err := sourceService.Backup(ctx, source.ID, archive, key)
	if err != nil {
		t.Fatal("real product encrypted backup failed", err)
	}
	// Only the generated fixture DB receives synthetic authorization. Original
	// model/Portal/native secrets never enter this database or browser fixture.
	database := "hc_test_native_restore_" + strings.ReplaceAll(webapi.ID(), "-", "")[:16]
	if _, err = sourcePool.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := sourcePool.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Error("owned recovery fixture database cleanup failed")
		}
	}()
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("fixture database unavailable")
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Native recovery fixture','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://native-recovery.test',$1::text,$2::uuid,'ACTIVE')`, source.OwnerID, source.Plan.WorkspaceOwner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, source.OwnerID)
	secret := webapi.ID()
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'synthetic',$3,'synthetic-never-decrypted')`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, source.Plan.Snapshot.ModelID, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages',$4,$5,'VALID')`, org, source.Plan.Snapshot.ModelID, source.Plan.Snapshot.ModelVersion, source.Plan.Snapshot.Model, secret)
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, org)
	target := source.Plan
	target.InstanceID, target.AssetID, target.ResourceName, target.FilesystemID = webapi.ID(), webapi.ID(), "", ""
	target.ResourceName = "hc-" + target.AssetID
	target.WorkspaceID = nil
	target.AssetStatus = "PREPARING"
	target.Action = "CREATE"
	var originalSnapshot []byte
	if err = sourcePool.QueryRow(ctx, `SELECT config_snapshot FROM instances WHERE id=$1`, proof.InstanceID).Scan(&originalSnapshot); err != nil {
		t.Fatal(err)
	}
	for _, p := range []worker.ExecutionPlan{source.Plan, target} {
		op := webapi.ID()
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,'STOPPED','STOPPED',$5,$6,$7)`, p.InstanceID, org, source.OwnerID, "dsh-"+p.InstanceID[:12], source.Plan.Snapshot.ModelID, source.Plan.Snapshot.ModelVersion, originalSnapshot)
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}','native-recovery-fixture')`, op, p.InstanceID, source.OwnerID)
		fsid := p.AssetID
		status := "READY"
		if p.InstanceID == target.InstanceID {
			fsid = ""
			status = "PREPARING"
		}
		must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,status,init_version) VALUES($1,$2,$3,$4,$5,$6,10737418240,$7,1)`, p.AssetID, org, source.OwnerID, p.InstanceID, p.ResourceName, fsid, status)
		must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, p.InstanceID, p.AssetID)
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, source.OwnerID, p.InstanceID, op)
	}
	prepared := false
	defer func() {
		if prepared {
			bounded, stop := context.WithTimeout(context.Background(), 90*time.Second)
			defer stop()
			unlock, err := host.Acquire(bounded, target.InstanceID)
			if err != nil {
				t.Error("target cleanup guard unavailable")
				return
			}
			defer unlock()
			if err = host.ReclaimAsset(bounded, target); err != nil {
				t.Error("owned target remains protected; cleanup failed")
			}
		}
	}()
	unlock, err := host.Acquire(ctx, target.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := host.Prepare(ctx, target)
	unlock()
	if err != nil {
		t.Fatal("independent target disk preparation failed", err)
	}
	prepared = true
	target.FilesystemID = physical.FilesystemID
	must(`UPDATE storage_assets SET status='READY',filesystem_id=id::text WHERE id=$1`, target.AssetID)
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "1000:1000", "--mount", "type=volume,src="+target.ResourceName+",dst=/data,volume-nocopy", "--entrypoint", "node", o.Host.RuntimeImage, "-e", `const f=require('fs');f.mkdirSync('/data/workspace',{recursive:true});f.writeFileSync('/data/workspace/target-before-restore','independent original',{mode:0o600})`)
	command.Stderr = nil
	if command.Run() != nil {
		t.Fatal("controlled original target proof write failed")
	}
	service := NewStorageService(pool, host, org, "native-recovery-acceptance")
	preimage := filepath.Join(directory, "target-original.hcbk")
	restored, err := service.Restore(ctx, archive, target.AssetID, preimage, target.AssetID, key, true)
	if err != nil || !restored.Applied || restored.Proof != archiveProof || restored.Preimage.Bytes == 0 {
		t.Fatal("actual encrypted alternate-asset restore failed", err)
	}
	var storage int64
	if err = pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status IN ('HELD','USED')`).Scan(&storage); err != nil || storage != 20<<30 {
		t.Fatal("alternate recovery skipped target storage accounting")
	}
	fixture := filepath.Join(directory, "restored-browser.json")
	raw, _ := json.Marshal(map[string]any{"instance_id": target.InstanceID, "asset_id": target.AssetID, "org_id": org, "owner_id": source.OwnerID, "runtime_root": o.Host.RuntimeRoot, "image": source.Plan.Snapshot.ImageDigest, "network_image": o.Host.NetworkImage, "model": source.Plan.Snapshot.Model, "model_version": source.Plan.Snapshot.ModelVersion, "tls_cert": filepath.Join(root, ".local/auth/cert.pem"), "tls_key": filepath.Join(root, ".local/auth/key.pem"), "file": proof.File, "file_proof": proof.FileProof, "selected_session": history.Completed.Selected})
	if err = os.WriteFile(fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	node := os.Getenv("HC_NODE_EXECUTABLE")
	if node == "" {
		node = "node"
	}
	browser := exec.CommandContext(ctx, node, filepath.Join(root, "infrastructure/runtime/tests/fixtures/restored-history-browser.mjs"))
	browser.Env = append(os.Environ(), "HC_RESTORED_HISTORY_FIXTURE_FILE="+fixture)
	browser.Stdout = nil
	browser.Stderr = nil
	if err = browser.Run(); err != nil {
		var failed struct {
			Stage string `json:"stage"`
		}
		if raw, e := os.ReadFile(fixture + ".result.json"); e == nil {
			_ = json.Unmarshal(raw, &failed)
			if output := os.Getenv("HC_NATIVE_RESTORE_EVIDENCE_FILE"); filepath.IsAbs(output) && privateDirectory(filepath.Dir(output)) == nil {
				_ = os.WriteFile(output+".failed", raw, 0600)
				if image, e := os.ReadFile(fixture + ".result.json.png"); e == nil {
					_ = os.WriteFile(output+".failed.png", image, 0600)
				}
			}
		}
		t.Fatalf("restored native browser proof failed at %q; secrets withheld", failed.Stage)
	}
	var result map[string]any
	readJSON(fixture+".result.json", &result)
	if result["outcome"] != "PASS" {
		t.Fatal("restored browser result missing")
	}
	// Compare the source's file-system identity and product state after the
	// browser reads only the target, without logging private contents.
	if beforeState() != before {
		t.Fatal("source product state changed during independent restore")
	}
	if err = host.InspectAsset(ctx, source.Plan); err != nil {
		t.Fatal("original source volume identity changed")
	}
	sum := sha256.Sum256([]byte(before))
	result["source_product_state_sha256"] = hex.EncodeToString(sum[:])
	result["source_state_unchanged"] = true
	result["independent_asset"] = true
	result["storage_ledger_bytes"] = storage
	result["backup_sha256"] = archiveProof.SHA256
	result["target_preimage_sha256"] = restored.Preimage.SHA256
	result["recorded_at"] = time.Now().UTC().Format(time.RFC3339)
	if output := os.Getenv("HC_NATIVE_RESTORE_EVIDENCE_FILE"); output != "" {
		data, _ := json.MarshalIndent(result, "", "  ")
		if !filepath.IsAbs(output) || privateDirectory(filepath.Dir(output)) != nil || os.WriteFile(output, append(data, '\n'), 0600) != nil {
			t.Fatal("private recovery evidence publication failed")
		}
	}
}
