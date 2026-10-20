package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func exerciseRuntimeDefaults(t *testing.T, ctx context.Context, root *pgxpool.Pool, service *Service, admin, member identity.Principal) {
	t.Helper()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := root.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	org := admin.OrganizationID
	template, version, small, large := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`DELETE FROM organization_defaults WHERE org_id=$1`, org)
	must(`UPDATE approved_templates SET enabled=false WHERE enabled`)
	must(`UPDATE approved_specs SET enabled=false WHERE enabled`)
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'runtime-defaults-fixture',$3,'{"storage_adapter":"bounded-ext4-v1"}',true)`, template, version, "sha256:"+strings.Repeat("a", 64))
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,1000,2147483648,10737418240,true),($2,2000,4294967296,21474836480,true)`, small, large)
	must(`INSERT INTO quota_policies(org_id,max_instances,max_user_instances) VALUES($1,10,2) ON CONFLICT(org_id) DO UPDATE SET max_instances=10,max_user_instances=2`, org)
	defer must(`DELETE FROM organization_defaults WHERE org_id=$1`, org)
	current := int64(0)
	retention, limit := 14, 3
	input := DefaultsInput{Version: &current, TemplateID: template, TemplateVersion: version, SpecID: large, RetentionDays: &retention, MaxUserInstances: &limit}
	if _, err := service.SaveDefaults(ctx, member, input, "runtime-member-denied"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member changed runtime defaults: %v", err)
	}
	must(`UPDATE approved_templates SET runtime_manifest='{}' WHERE id=$1 AND version=$2`, template, version)
	if _, err := service.SaveDefaults(ctx, admin, input, "legacy-storage-adapter"); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("legacy adapter approved configurable storage", err)
	}
	must(`UPDATE approved_templates SET runtime_manifest='{"storage_adapter":"bounded-ext4-v1"}' WHERE id=$1 AND version=$2`, template, version)
	saved, err := service.SaveDefaults(ctx, admin, input, "runtime-initialize")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.ModelID != "" || saved.ModelVersion != 0 || saved.RetentionDays != 14 || saved.MaxUserInstances != 3 || saved.Spec.CPU != 2000 || saved.Spec.Storage != 20<<30 {
		t.Fatalf("incorrect initialized defaults: %+v", saved)
	}
	var nullModel bool
	if err = root.QueryRow(ctx, `SELECT model_config_id IS NULL AND model_config_version IS NULL FROM organization_defaults WHERE org_id=$1`, org).Scan(&nullModel); err != nil || !nullModel {
		t.Fatal("initialization required a model", err)
	}
	current = saved.Version
	retention = 30
	limit = 2
	input.SpecID = small
	saved, err = service.SaveDefaults(ctx, admin, input, "runtime-update")
	if err != nil || saved.Version != 2 || saved.Spec.CPU != 1000 || saved.RetentionDays != 30 || saved.MaxUserInstances != 2 {
		t.Fatalf("runtime update failed: %+v %v", saved, err)
	}
	if _, err = service.SaveDefaults(ctx, admin, input, "runtime-stale"); problemCode(err) != "VERSION_CONFLICT" {
		t.Fatal("stale version overwritten", err)
	}
	current = saved.Version
	retention = 366
	if _, err = service.SaveDefaults(ctx, admin, input, "runtime-invalid-retention"); problemCode(err) != "INVALID_SPEC" {
		t.Fatal("invalid retention accepted", err)
	}
	retention = 30
	limit = 11
	if _, err = service.SaveDefaults(ctx, admin, input, "runtime-invalid-quota"); problemCode(err) != "INVALID_SPEC" {
		t.Fatal("quota above organization accepted", err)
	}
	limit = 2
	input.SpecID = webapi.ID()
	if _, err = service.SaveDefaults(ctx, admin, input, "runtime-unapproved-spec"); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("unapproved spec accepted", err)
	}
	input.SpecID = small
	results := make(chan error, 2)
	for range 2 {
		go func() { _, e := service.SaveDefaults(ctx, admin, input, "runtime-concurrent"); results <- e }()
	}
	wins, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			wins++
		} else if problemCode(e) == "VERSION_CONFLICT" {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("concurrent runtime saves overwrote a version")
	}
	must(`CREATE OR REPLACE FUNCTION hc_test_defaults_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='DEFAULTS_SAVE' AND NEW.trace_id='runtime-audit-failure' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER hc_test_defaults_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_defaults_audit()`)
	defer must(`DROP TRIGGER hc_test_defaults_audit ON audit_events; DROP FUNCTION hc_test_defaults_audit()`)
	current = 3
	retention = 60
	limit = 4
	if _, err = service.SaveDefaults(ctx, admin, input, "runtime-audit-failure"); err == nil {
		t.Fatal("audit failure accepted")
	}
	after, err := service.GetDefaults(ctx, admin)
	if err != nil || after.Version != 3 || after.RetentionDays != 30 || after.MaxUserInstances != 2 {
		t.Fatalf("late failure did not roll back configuration and quota: %+v %v", after, err)
	}
	expanded := webapi.ID()
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,16000,68719476736,21474836480,true)`, expanded)
	current = after.Version
	retention, limit = 30, 2
	input.SpecID = expanded
	saved, err = service.SaveDefaults(ctx, admin, input, "runtime-expanded-resources")
	if err != nil || saved.Spec.CPU != 16000 || saved.Spec.Memory != 64<<30 {
		t.Fatalf("expanded defaults rejected: %+v %v", saved, err)
	}
}
