package settings

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

// These checks use the restricted Worker/API roles and never send a paid
// inference request, recreate a route or rotate a virtual credential.
func exerciseGatewayChecks(t *testing.T, ctx context.Context, admin *pgxpool.Pool, api *Service, syncer *ModelSync, owner identity.Principal, id string, drift func(bool), counters func() (int, int, int)) {
	t.Helper()
	initialRoutes, initialKeys, initialPaid := counters()
	assertAvailable := func(want bool) {
		t.Helper()
		var actual bool
		if err := admin.QueryRow(ctx, `SELECT public.hc_model_available($1,$2,$3,1)`, owner.OrganizationID, owner.UserID, id).Scan(&actual); err != nil || actual != want {
			t.Fatal("gateway availability gate disagrees", err, actual, want)
		}
	}
	due := func() {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE model_gateway_checks SET next_check_at=clock_timestamp() WHERE connection_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	assertBlocked := func() {
		t.Helper()
		assertAvailable(false)
		if _, err := api.ValidatePersonalModel(ctx, owner, id, 1, ""); problemCode(err) != "CONFIGURATION_INVALID" {
			t.Fatal("unchecked gateway permitted paid validation", err)
		}
		if _, err := api.SetPersonalDefault(ctx, owner, id, 1, 1, ""); problemCode(err) != "CONFIGURATION_INVALID" {
			t.Fatal("unchecked gateway permitted default selection", err)
		}
	}
	assertAvailable(true)
	// Updating a connection leaves its previous default version in use until
	// the user explicitly changes that default. The old route still needs checks.
	current, err := api.GetPersonalModel(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := api.UpdatePersonalModel(ctx, owner, id, PersonalModelUpdate{Name: current.Name, CatalogEntryID: current.CatalogEntryID, ExpectedVersion: 1}, "")
	if err != nil || updated.Version != 2 {
		t.Fatal("could not prepare retained version check", err)
	}
	cleanupNewVersion := func() {
		t.Helper()
		// Remove only this unexecuted test update so the enclosing fixture can
		// continue testing revocation/rotation from its original version.
		tx, err := admin.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		for _, query := range []string{
			`UPDATE user_model_connections SET current_version=1 WHERE id=$1`,
			`DELETE FROM model_sync_operations WHERE connection_id=$1 AND connection_version=2`,
			`DELETE FROM user_model_connection_versions WHERE connection_id=$1 AND version=2`,
		} {
			if _, err = tx.Exec(context.Background(), query, id); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	defer cleanupNewVersion()
	due()
	if ran, err := syncer.CheckOnce(ctx); !ran || err != nil {
		t.Fatal("retained default version stopped receiving checks", err)
	}
	assertAvailable(true)
	cleanupNewVersion()
	if _, err := admin.Exec(ctx, `UPDATE model_gateway_checks SET checked_at=statement_timestamp()-interval '61 seconds',expires_at=statement_timestamp()-interval '1 second' WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	item, err := api.GetPersonalModel(ctx, owner, id)
	if err != nil || item.GatewayCheck.Status != "UNKNOWN" || item.GatewayCheck.Reason != "MODEL_GATEWAY_CHECK_EXPIRED" || item.Validation.Status != "HEALTHY" {
		t.Fatal("expired gateway check hidden or inference evidence overwritten", err)
	}
	due()
	if ran, err := syncer.CheckOnce(ctx); !ran || err != nil {
		t.Fatal("periodic readback failed", err)
	}
	assertAvailable(true)
	drift(true)
	due()
	if ran, err := syncer.CheckOnce(ctx); !ran || err == nil {
		t.Fatal("drifted gateway remained healthy")
	}
	assertBlocked()
	item, err = api.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "SYNCED" || item.GatewayCheck.Status != "UNHEALTHY" || item.GatewayCheck.Reason != "MODEL_ROUTE_DRIFT" || item.Validation.Status != "HEALTHY" {
		t.Fatal("independent gateway drift status missing", err)
	}
	drift(false)
	due()
	if ran, err := syncer.CheckOnce(ctx); !ran || err != nil {
		t.Fatal("restored gateway did not recover", err)
	}
	assertAvailable(true)
	// A response belonging to an expired/replaced lease cannot publish health.
	due()
	lease, claimed, err := syncer.claimCheck(ctx)
	if err != nil || !claimed {
		t.Fatal("could not acquire gateway check lease", err)
	}
	if _, duplicated, err := syncer.claimCheck(ctx); err != nil || duplicated {
		t.Fatal("active gateway lease was claimed twice", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE model_gateway_checks SET lease_token=lease_token+1,lease_expires_at=clock_timestamp()-interval '1 second',status='UNKNOWN',reason_code='MODEL_GATEWAY_UNVERIFIED' WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := syncer.publishCheck(ctx, lease, ""); err != nil {
		t.Fatal("obsolete check completion must be harmless", err)
	}
	assertBlocked()
	due()
	if ran, err := syncer.CheckOnce(ctx); !ran || err != nil {
		t.Fatal("abandoned check lease did not recover", err)
	}
	assertAvailable(true)
	// Health and status-change audit commit together. A failed audit preserves
	// the original lease so the same readback can be published after recovery.
	due()
	lease, claimed, err = syncer.claimCheck(ctx)
	if err != nil || !claimed {
		t.Fatal("could not prepare audit rollback", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE model_gateway_checks SET status='UNKNOWN',reason_code='MODEL_GATEWAY_UNVERIFIED' WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE OR REPLACE FUNCTION hc_test_reject_gateway_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='PERSONAL_MODEL_GATEWAY_CHECK' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER hc_test_reject_gateway_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_gateway_audit()`); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_reject_gateway_audit ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_gateway_audit()`)
	if err := syncer.publishCheck(ctx, lease, ""); err == nil {
		t.Fatal("health was published without its audit")
	}
	assertBlocked()
	if _, err := admin.Exec(ctx, `DROP TRIGGER hc_test_reject_gateway_audit ON audit_events; DROP FUNCTION hc_test_reject_gateway_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := syncer.publishCheck(ctx, lease, ""); err != nil {
		t.Fatal("same leased check could not recover after audit rollback", err)
	}
	assertAvailable(true)
	if routes, keys, paid := counters(); routes != initialRoutes || keys != initialKeys || paid != initialPaid {
		t.Fatal("periodic check mutated configuration or issued inference", routes, keys, paid)
	}
	if _, err := api.pool.Exec(ctx, `UPDATE model_gateway_checks SET status='HEALTHY' WHERE connection_id=$1`, id); err == nil {
		t.Fatal("API acquired gateway-check write permission")
	}
}
