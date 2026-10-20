package operations

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func exerciseRecoverySourceFence(t *testing.T, ctx context.Context, target *pgxpool.Pool, observer *inventoryFixtureObserver, targetDB, org, owner string) {
	t.Helper()
	apiPath := os.Getenv("HC_TEST_API_DSN_FILE")
	if apiPath == "" {
		t.Skip("controlled API login DSN required for real connection fencing")
	}
	raw, err := os.ReadFile(os.Getenv("HC_TEST_DB_DSN_FILE"))
	if err != nil {
		t.Fatal("test administrator missing")
	}
	admin, err := pgx.Connect(ctx, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("test administrator unavailable")
	}
	defer admin.Close(context.Background())
	sourceDB := "hc_test_recovery_source_fence_" + strings.ReplaceAll(webapi.ID(), "-", "")[:16]
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{sourceDB}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := admin.Exec(context.Background(), `DROP DATABASE `+pgx.Identifier{sourceDB}.Sanitize()+` WITH (FORCE)`)
		if err != nil {
			t.Error("owned source fixture cleanup failed")
		}
	}()
	u, _ := url.Parse(strings.TrimSpace(string(raw)))
	u.Path = "/" + sourceDB
	key := bytes.Repeat([]byte{17}, 32)
	backup := filepath.Join(t.TempDir(), "metadata.hcbk")
	if err = os.Chmod(filepath.Dir(backup), 0700); err != nil {
		t.Fatal(err)
	}
	// Repair the deliberately missing INSTANCE reservation in this fixture's
	// initial data. The serving-source fixture must have a complete normal ledger.
	if _, err = target.Exec(ctx, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) SELECT $1,r.org_id,r.owner_id,r.instance_id,r.operation_id,'INSTANCE',1,'USED' FROM quota_reservations r WHERE r.resource_type='STORAGE' AND EXISTS(SELECT 1 FROM recovery_quota_holds h WHERE h.instance_id=r.instance_id AND h.resource_type='INSTANCE')`, webapi.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, `DELETE FROM recovery_quota_holds`); err != nil {
		t.Fatal(err)
	}
	if _, err = BackupDatabase(ctx, target, target.Config().ConnString(), backup, "source-fence-fixture", key); err != nil {
		t.Fatal("controlled source fixture backup", err)
	}
	if _, err = RestoreDatabase(ctx, u.String(), backup, sourceDB, "source-fence-fixture", key, true); err != nil {
		t.Fatal("controlled source fixture restore", err)
	}
	cfg, _ := pgxpool.ParseConfig(u.String())
	cfg.MaxConns = 1
	source, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("owned source fixture unavailable")
	}
	defer source.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := source.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic source data models an already serving primary. No live product
	// organization, model credential, session or database privilege is modified.
	must(`UPDATE organizations SET status='ACTIVE';UPDATE secrets SET revoked_at=NULL;UPDATE model_configurations SET status='VALID',revoked_at=NULL;UPDATE probe_budgets SET attempts=3`)
	must(`GRANT CONNECT ON DATABASE ` + pgx.Identifier{sourceDB}.Sanitize() + ` TO hc_api,hc_worker,hc_gateway,hc_model_gateway`)
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) SELECT $1,$2,org_id,user_id,auth_generation,$2,'fixture','fixture',$3,clock_timestamp()+interval '1 hour' FROM memberships WHERE org_id=$4 AND user_id=$5`, webapi.ID(), bytes.Repeat([]byte{9}, 32), bytes.Repeat([]byte{8}, 12), org, owner)
	apiRaw, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal("controlled API connection missing")
	}
	apiURL, err := url.Parse(strings.TrimSpace(string(apiRaw)))
	if err != nil {
		t.Fatal("controlled API connection invalid")
	}
	apiURL.Path = "/" + sourceDB
	connection, err := pgx.Connect(ctx, apiURL.String())
	if err != nil {
		t.Fatal("controlled API source connection unavailable")
	}
	defer connection.Close(context.Background())
	core := func() string {
		t.Helper()
		var raw []byte
		if err := source.QueryRow(ctx, `SELECT jsonb_build_object('instances',(SELECT jsonb_agg(jsonb_build_array(id,org_id,owner_id,storage_asset_id,workspace_id,config_snapshot,desired_state,observed_state) ORDER BY id) FROM instances),'assets',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM storage_assets a),'quota',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM quota_reservations r),'cipher',(SELECT jsonb_agg(jsonb_build_array(id,key_id,nonce,ciphertext) ORDER BY id) FROM secrets))`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return inventoryHash(raw)
	}
	originalCore := core()
	var originalGeneration int64
	if err = source.QueryRow(ctx, `SELECT auth_generation FROM memberships WHERE org_id=$1 AND user_id=$2`, org, owner).Scan(&originalGeneration); err != nil {
		t.Fatal(err)
	}
	inventory := NewRecoveryInventory(target, observer, org, "source-fence-fixture")
	reviewed, err := inventory.Run(ctx, targetDB, true)
	if err != nil || reviewed.Blockers != 0 {
		t.Fatal("target fixture not completely reconciled", err)
	}
	service := NewRecoverySourceFence(source, target, observer, org, "source-fence-fixture")
	run := func(reason string, apply bool) (RecoverySourceFenceReport, error) {
		return service.Fence(ctx, sourceDB, targetDB, reviewed.RunID, reason, apply)
	}
	check := func(active bool, fences, drains int) {
		t.Helper()
		var isActive, credentialsLive, connect bool
		var gotFences, gotDrains, attempts int
		err := source.QueryRow(ctx, `SELECT (SELECT status='ACTIVE' FROM organizations),EXISTS(SELECT 1 FROM secrets WHERE revoked_at IS NULL),has_database_privilege('hc_api',current_database(),'CONNECT'),(SELECT count(*) FROM audit_events WHERE action='RECOVERY_SOURCE_FENCE'),(SELECT count(*) FROM audit_events WHERE action='RECOVERY_SOURCE_DRAIN'),(SELECT attempts FROM probe_budgets WHERE day=(clock_timestamp() AT TIME ZONE 'UTC')::date)`).Scan(&isActive, &credentialsLive, &connect, &gotFences, &gotDrains, &attempts)
		if err != nil || isActive != active || credentialsLive != active || connect != active || gotFences != fences || gotDrains != drains || attempts != 3 {
			t.Fatal("partial source fence or changed budget", err, isActive, credentialsLive, connect, gotFences, gotDrains, attempts)
		}
	}
	preview, err := run("RECOVERY_SWITCH", false)
	if err != nil || preview.Applied || preview.Drained {
		t.Fatal("source fence preview mutated authorization", err)
	}
	check(true, 0, 0)
	var originalOperation string
	if err = source.QueryRow(ctx, `SELECT id::text FROM operations WHERE type='CREATE' AND status='SUCCEEDED' ORDER BY id LIMIT 1`).Scan(&originalOperation); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE operations SET status='RUNNING' WHERE id=$1`, originalOperation)
	if _, err = run("RECOVERY_SWITCH", true); err == nil {
		t.Fatal("source with an active operation was fenced")
	}
	must(`UPDATE operations SET status='SUCCEEDED' WHERE id=$1`, originalOperation)
	check(true, 0, 0)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 1 {
			if _, err := target.Exec(ctx, `UPDATE instances SET version=version+1`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = run("RECOVERY_SWITCH", true); err == nil {
		t.Fatal("target policy changed during inventory but source was fenced")
	}
	if _, err = target.Exec(ctx, `UPDATE instances SET version=version-1`); err != nil {
		t.Fatal(err)
	}
	observer.hook = nil
	check(true, 0, 0)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 1 {
			must(`UPDATE instances SET version=version+1`)
		}
	}
	if _, err = run("RECOVERY_SWITCH", true); err == nil {
		t.Fatal("source policy changed during inventory but source was fenced")
	}
	must(`UPDATE instances SET version=version-1`)
	observer.hook = nil
	check(true, 0, 0)
	if _, err = target.Exec(ctx, `GRANT CONNECT ON DATABASE `+pgx.Identifier{targetDB}.Sanitize()+` TO hc_gateway`); err != nil {
		t.Fatal(err)
	}
	observer.calls = 0
	if _, err = run("RECOVERY_SWITCH", true); err == nil || observer.calls != 0 {
		t.Fatal("service-connectable target reached source fence")
	}
	if _, err = target.Exec(ctx, `REVOKE CONNECT ON DATABASE `+pgx.Identifier{targetDB}.Sanitize()+` FROM hc_gateway`); err != nil {
		t.Fatal(err)
	}
	check(true, 0, 0)
	must(`CREATE FUNCTION reject_source_fence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RECOVERY_SOURCE_FENCE' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_source_fence BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_source_fence()`)
	if r, err := run("RECOVERY_SWITCH", true); err == nil || r.Applied {
		t.Fatal("source late audit failure did not roll back")
	}
	check(true, 0, 0)
	if connection.Ping(ctx) != nil {
		t.Fatal("source connection killed before fence committed")
	}
	must(`DROP TRIGGER reject_source_fence ON audit_events`)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 2 {
			observer.resources[len(observer.resources)-1].Status = "running"
		}
	}
	if _, err = run("RECOVERY_SWITCH", true); err == nil {
		t.Fatal("changing actual resources fenced")
	}
	observer.resources[len(observer.resources)-1].Status = "stopped"
	observer.hook = nil
	check(true, 0, 0)
	if _, err = service.Fence(ctx, sourceDB, targetDB, webapi.ID(), "RECOVERY_SWITCH", true); err == nil {
		t.Fatal("unreviewed recovery target fenced source")
	}
	drain := service.drain
	service.drain = func(context.Context, *pgxpool.Pool) error {
		return errors.New("injected interruption after source commit")
	}
	partial, err := run("RECOVERY_SWITCH", true)
	if err == nil || !partial.Applied || partial.Drained {
		t.Fatal("post-commit interruption hid persisted source fence")
	}
	check(false, 1, 0)
	service.drain = drain
	must(`CREATE FUNCTION reject_source_drain() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RECOVERY_SOURCE_DRAIN' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_source_drain BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_source_drain()`)
	if r, err := run("RECOVERY_SWITCH", true); err == nil || !r.Applied || r.Drained {
		t.Fatal("drain-audit failure hid the committed source fence")
	}
	check(false, 1, 0)
	if connection.Ping(ctx) == nil {
		t.Fatal("drain audit failure revived a killed source connection")
	}
	must(`DROP TRIGGER reject_source_drain ON audit_events`)
	complete, err := run("RECOVERY_SWITCH", true)
	if err != nil || !complete.Applied || !complete.Drained || complete.RunID != partial.RunID {
		t.Fatal("committed source fence could not finish draining", err)
	}
	check(false, 1, 1)
	if connection.Ping(ctx) == nil {
		t.Fatal("original API source connection survived")
	}
	if reopened, err := pgx.Connect(ctx, apiURL.String()); err == nil {
		reopened.Close(context.Background())
		t.Fatal("original API role reconnected to fenced source")
	}
	replay, err := run("RECOVERY_SWITCH", true)
	if err != nil || replay.RunID != complete.RunID || replay.DrainRunID != complete.DrainRunID {
		t.Fatal("source fence replay duplicated mutation", err)
	}
	check(false, 1, 1)
	if _, err = run("OTHER_REASON", true); err == nil {
		t.Fatal("changed source-fence intent replayed")
	}
	if core() != originalCore {
		t.Fatal("source fence changed original snapshots, assets, reservations or encrypted material")
	}
	var revoked bool
	if err = source.QueryRow(ctx, `SELECT (SELECT bool_and(auth_generation=$3) FROM memberships WHERE org_id=$1 AND user_id=$2) AND NOT EXISTS(SELECT 1 FROM portal_sessions WHERE revoked_at IS NULL) AND NOT EXISTS(SELECT 1 FROM model_configurations WHERE status<>'REVOKED' OR revoked_at IS NULL)`, org, owner, originalGeneration+1).Scan(&revoked); err != nil || !revoked {
		t.Fatal("old source generations or model/session authorization survived", err)
	}
	post, err := inventory.Run(ctx, targetDB, false)
	if err != nil || post.SnapshotSHA256 != reviewed.SnapshotSHA256 || post.PromotionAllowed {
		t.Fatal("source fence changed or authorized target", err)
	}
}
