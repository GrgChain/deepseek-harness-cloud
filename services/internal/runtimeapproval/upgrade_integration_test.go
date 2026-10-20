package runtimeapproval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRuntimeUpgradeIsAtomicQuiescentAndPreservesFrozenInstances(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated approval PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private test connection missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_approval"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`TRUNCATE organizations,approved_templates,approved_specs,users CASCADE`)
	org, template, user, model, secret, instance, op := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	old := fixtureManifest()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Upgrade fixture','ACTIVE')`, org)
	if err = Approve(ctx, pool, org, template, "upgrade-fixture", old, 1); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://upgrade.test',$1::text,$1::uuid,'ACTIVE')`, user)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, user)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'fixture',$3,'x')`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, model, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID')`, org, model, secret)
	must(`INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,model_config_id,model_config_version) SELECT $1,1,$2,$3,id,$4,1 FROM approved_specs WHERE enabled AND cpu_millicores=2000 AND memory_bytes=4294967296`, org, template, old.TemplateVersion, model)
	snapshot, _ := json.Marshal(map[string]any{"schema_version": 1, "template_version": old.TemplateVersion, "image_digest": old.Image, "sentinel": "immutable-instance-snapshot"})
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,'upgrade-instance',$1,'upgrade-instance','STOPPED','STOPPED',$4,1,$5)`, instance, org, user, model, snapshot)
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'STOP','SUCCEEDED','COMPLETE','{}','upgrade-fixture')`, op, instance, user)
	must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, user, instance, op)
	next := old
	next.TemplateVersion = webapi.ID()
	next.Image = "sha256:" + strings.Repeat("b", 64)
	next.ProfileDigest = next.Image
	next.ToolDigest = next.Image
	next.AdapterDigest = next.Image
	next.ExternalModels = ExternalModelAdapter
	next.NetworkImage = "sha256:" + strings.Repeat("c", 64)
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, false); err != nil {
		t.Fatal("preview failed", err)
	}
	var version int
	var active string
	check := func(expected int, image string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT (SELECT version FROM organization_defaults WHERE org_id=$1),(SELECT image_digest FROM approved_templates WHERE enabled)`, org).Scan(&version, &active); err != nil || version != expected || active != image {
			t.Fatal("partial upgrade/default change", err)
		}
	}
	check(1, old.Image)
	for _, state := range []string{"RUNNING", "ERROR"} {
		must(`UPDATE instances SET observed_state=$2 WHERE id=$1`, instance, state)
		if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err == nil {
			t.Fatal("unconfirmed compute accepted", state)
		}
	}
	must(`UPDATE instances SET observed_state='STOPPED' WHERE id=$1`, instance)
	must(`UPDATE operations SET status='UNCERTAIN' WHERE id=$1`, op)
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err == nil {
		t.Fatal("active operation accepted")
	}
	must(`UPDATE operations SET status='SUCCEEDED' WHERE id=$1`, op)
	must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'RUNNING',1,'HELD')`, webapi.ID(), org, user, instance, op)
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err == nil {
		t.Fatal("unreleased compute accepted")
	}
	must(`UPDATE quota_reservations SET status='RELEASED' WHERE resource_type='RUNNING'`)
	must(`CREATE FUNCTION hc_test_reject_upgrade() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RUNTIME_UPGRADE' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER hc_test_reject_upgrade BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_upgrade()`)
	defer pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_reject_upgrade ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_upgrade()`)
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err == nil {
		t.Fatal("late audit failure accepted")
	}
	check(1, old.Image)
	must(`DROP TRIGGER hc_test_reject_upgrade ON audit_events`)
	if err = Upgrade(ctx, pool, org, template, "operator", webapi.ID(), next, true); err == nil {
		t.Fatal("stale previous version accepted")
	}
	bad := next
	bad.StorageImage = next.Image
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, bad, true); err == nil {
		t.Fatal("incompatible storage upgrade accepted")
	}
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err != nil {
		t.Fatal(err)
	}
	if err = Upgrade(ctx, pool, org, template, "operator", old.TemplateVersion, next, true); err != nil {
		t.Fatal("exact upgrade replay failed", err)
	}
	check(2, next.Image)
	var frozen, retained bool
	var audits int
	if err = pool.QueryRow(ctx, `SELECT (SELECT config_snapshot=$2::jsonb FROM instances WHERE id=$1),(SELECT count(*)=2 AND count(*) FILTER(WHERE enabled)=1 FROM approved_templates),(SELECT count(*) FROM audit_events WHERE action='RUNTIME_UPGRADE')`, instance, snapshot).Scan(&frozen, &retained, &audits); err != nil || !frozen || !retained || audits != 1 {
		t.Fatal("upgrade mutated frozen instance or removed prior contract", err)
	}
	t.Run("compatible rollback", func(t *testing.T) { exerciseRollback(t, ctx, pool, org, template, instance, op, old, next, snapshot) })
}
