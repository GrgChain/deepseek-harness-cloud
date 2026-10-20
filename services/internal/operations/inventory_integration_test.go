package operations

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type inventoryFixtureObserver struct {
	resources []ResourceObservation
	calls     int
	hook      func(int)
}

func (o *inventoryFixtureObserver) Observe(context.Context) ([]ResourceObservation, error) {
	o.calls++
	if o.hook != nil {
		o.hook(o.calls)
	}
	return append([]ResourceObservation(nil), o.resources...), nil
}

func TestRecoveryInventoryOnIsolatedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private test connection missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("test administrator unavailable")
	}
	defer admin.Close(context.Background())
	database := "hc_test_recovery_inventory_" + strings.ReplaceAll(webapi.ID(), "-", "")[:16]
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Error("owned fixture database cleanup failed")
		}
	}()
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(u.String())
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("fixture connection failed")
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	org, owner, secret, model, id, asset, workspace, version, op := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Recovery inventory fixture','DISABLED')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://inventory.test',$1::text,$1::uuid,'ACTIVE')`, owner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, owner)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext,revoked_at) VALUES($1,$2,'fixture',$3,'x',clock_timestamp())`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, model, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status,revoked_at) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'REVOKED',clock_timestamp())`, org, model, secret)
	snapshot := `{"schema_version":1,"defaults_version":1,"template_id":"` + webapi.ID() + `","template_version":"` + version + `","image_digest":"sha256:` + strings.Repeat("a", 64) + `","spec":{"id":"` + webapi.ID() + `","cpu_millicores":2000,"memory_bytes":4294967296,"storage_bytes":10737418240},"model_configuration_id":"` + model + `","model_configuration_version":1,"provider":"deepseek-official","protocol":"deepseek-messages","model":"fixture"}`
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,workspace_id,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,'dsh-fixture',$1,'hc-fixture',$4,'STOPPED','STOPPED',$5,1,$6)`, id, org, owner, workspace, model, snapshot)
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,status,init_version) VALUES($1::uuid,$2,$3,$4,$5,$1::text,10737418240,'READY',1)`, asset, org, owner, id, "hc-"+asset)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, asset)
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}','inventory-fixture')`, op, id, owner)
	must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, owner, id, op)
	must(`INSERT INTO audit_events(id,org_id,action,outcome,trace_id) VALUES($1,$2,'RESTORE_ISOLATED','SUCCEEDED','fixture')`, webapi.ID(), org)
	must(`REVOKE CONNECT ON DATABASE ` + pgx.Identifier{database}.Sanitize() + ` FROM PUBLIC,hc_api,hc_worker,hc_gateway,hc_model_gateway`)
	orphan, orphanAsset := webapi.ID(), webapi.ID()
	observer := &inventoryFixtureObserver{resources: []ResourceObservation{
		{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "WORKSPACE", ExternalID: workspace, InstanceID: id, WorkspaceOwner: owner, TemplateVersion: version, Status: "stopped", IdentityValid: true},
		{Kind: "ASSET", ExternalID: orphanAsset, InstanceID: orphan, AssetID: orphanAsset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
	}}
	service := NewRecoveryInventory(pool, observer, org, "integration-operator")
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='RECOVERY_INVENTORY'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	report, err := service.Run(ctx, database, false)
	if err != nil || report.Applied || report.PromotionAllowed || report.Conservative.Storage != 20<<30 || report.Ledger.Storage != 10<<30 || count() != 0 {
		t.Fatal("preview changed quarantine or undercounted orphan", err, report)
	}
	report, err = service.Run(ctx, database, true)
	if err != nil || !report.Applied || !report.Quarantined || report.PromotionAllowed || count() != 1 {
		t.Fatal("quarantine register not appended atomically", err)
	}
	replay, err := service.Run(ctx, database, true)
	if err != nil || replay.RunID != report.RunID || count() != 1 {
		t.Fatal("exact replay duplicated inventory audit", err)
	}
	must(`UPDATE storage_assets SET filesystem_id=$2 WHERE id=$1`, asset, webapi.ID())
	if r, err := service.Run(ctx, database, false); err != nil || r.Blockers <= report.Blockers || count() != 1 {
		t.Fatal("restored filesystem identity mismatch was hidden", err)
	}
	must(`UPDATE storage_assets SET filesystem_id=id::text WHERE id=$1`, asset)
	must(`UPDATE instances SET desired_state='DELETED' WHERE id=$1`, id)
	if r, err := service.Run(ctx, database, false); err != nil || r.Blockers <= report.Blockers || count() != 1 {
		t.Fatal("restored desired state mismatch was hidden", err)
	}
	must(`UPDATE instances SET desired_state='STOPPED' WHERE id=$1`, id)
	must(`CREATE FUNCTION reject_inventory_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RECOVERY_INVENTORY' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER reject_inventory_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_inventory_audit()`)
	observer.resources = append(observer.resources, ResourceObservation{Kind: "NETWORK", ExternalID: webapi.ID(), InstanceID: orphan, IdentityValid: true})
	if r, err := service.Run(ctx, database, true); err == nil || r.Applied || count() != 1 {
		t.Fatal("failed audit committed a recovery register")
	}
	must(`DROP TRIGGER reject_inventory_audit ON audit_events`)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 2 {
			observer.resources[0].Capacity++
		}
	}
	if _, err := service.Run(ctx, database, true); err == nil || count() != 1 {
		t.Fatal("moving physical inventory accepted")
	}
	observer.resources[0].Capacity--
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 1 {
			must(`UPDATE quota_reservations SET amount=amount+1 WHERE instance_id=$1`, id)
		}
	}
	if _, err := service.Run(ctx, database, true); err == nil || count() != 1 {
		t.Fatal("database changed during scan but inventory committed")
	}
	must(`UPDATE quota_reservations SET amount=10737418240 WHERE instance_id=$1`, id)
	observer.hook = nil
	observer.calls = 0
	if _, err := service.Run(ctx, "hc_recovery_wrong", true); err == nil || observer.calls != 0 {
		t.Fatal("wrong database confirmation reached external host")
	}
	must(`UPDATE organizations SET status='ACTIVE'`)
	if _, err := service.Run(ctx, database, true); err == nil || observer.calls != 0 {
		t.Fatal("active serving authorization reached external host")
	}
	must(`UPDATE organizations SET status='DISABLED'`)
	must(`GRANT CONNECT ON DATABASE ` + pgx.Identifier{database}.Sanitize() + ` TO hc_api`)
	if _, err := service.Run(ctx, database, true); err == nil || observer.calls != 0 {
		t.Fatal("service-accessible restored database accepted")
	}
	var amount int64
	if err := pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status='USED'`).Scan(&amount); err != nil || amount != 10<<30 {
		t.Fatal("inventory changed original quota ledger")
	}
	t.Run("retained resources enter real quota accounting", func(t *testing.T) { exerciseRecoveryLedger(t, ctx, pool, observer, database, org, owner) })
	t.Run("verified checkpoint adopts orphan resources without freeing quota", func(t *testing.T) { exerciseRecoveryAdoption(t, ctx, pool, observer, database, org, owner) })
	t.Run("old source authorization is fenced before recovery activation", func(t *testing.T) { exerciseRecoverySourceFence(t, ctx, pool, observer, database, org, owner) })
}
