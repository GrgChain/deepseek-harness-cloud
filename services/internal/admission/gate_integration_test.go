package admission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestAdmissionWithActualDependenciesAndRestrictedPostgres(t *testing.T) {
	path, accessPath := os.Getenv("HC_TEST_DB_DSN_FILE"), os.Getenv("HC_ACCESS_CONFIG_FILE")
	if path == "" || accessPath == "" {
		t.Skip("isolated admission PostgreSQL, local Worker/Gateway and controlled access configuration required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := config.ReadPrivateFile(path)
	if err != nil {
		t.Fatal("private test connection unavailable")
	}
	connection, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	connection.Path = "/hc_test_admission"
	if err := db.Apply(ctx, connection.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	apiRaw, err := config.ReadPrivateFile(filepath.Join(filepath.Dir(path), "api-dsn"))
	if err != nil {
		t.Fatal("restricted test connection unavailable")
	}
	apiURL, err := url.Parse(strings.TrimSpace(string(apiRaw)))
	if err != nil {
		t.Fatal("invalid restricted connection")
	}
	apiURL.Path = connection.Path
	api, err := pgxpool.New(ctx, apiURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	// The isolated database owns its embedded Workspace authority; it cannot
	// discover the live platform's initialization through an external service.
	options, err := access.LoadOptions(accessPath)
	if err != nil {
		t.Fatal("controlled access configuration unavailable")
	}
	if _, err = admin.Exec(ctx, `INSERT INTO workspace_authority(singleton,organization_id,control_user_id) VALUES(true,$1,$2) ON CONFLICT(singleton) DO UPDATE SET organization_id=excluded.organization_id,control_user_id=excluded.control_user_id`, options.WorkspaceOrganizationID, options.ControlUserID); err != nil {
		t.Fatal("isolated Workspace authority initialization failed")
	}
	runtime, err := access.NewRuntime(ctx, api, accessPath)
	if err != nil {
		t.Fatal("actual Workspace access contract unavailable")
	}
	defer runtime.Close()
	mappingRaw, err := config.ReadPrivateFile(filepath.Join(filepath.Dir(path), "auth", "identity-mapping.json"))
	if err != nil {
		t.Fatal("controlled identity mapping unavailable")
	}
	var mapping identity.Mapping
	if json.Unmarshal(mappingRaw, &mapping) != nil || len(mapping.Members) != 2 {
		t.Fatal("invalid local identity mapping")
	}
	org := runtime.Options.OrganizationID
	if mapping.OrganizationID != org {
		t.Fatal("test mapping does not match fixed organization")
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'isolated admission fixture','ACTIVE') ON CONFLICT(id) DO NOTHING`, org)
	var user string
	if err := admin.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://admission.test',$1::text,$2,'ACTIVE') ON CONFLICT(workspace_owner_id) DO UPDATE SET status='ACTIVE' RETURNING id::text`, webapi.ID(), mapping.Members[0].WorkspaceOwnerID).Scan(&user); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := admin.QueryRow(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE') ON CONFLICT(org_id,user_id) DO UPDATE SET status='ACTIVE',auth_generation=memberships.auth_generation+1 RETURNING auth_generation`, org, user).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	session := webapi.ID()
	hash := sha256.Sum256([]byte(session))
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,$5,$2,'x','test',$6,clock_timestamp()+interval '1 hour')`, session, hash[:], org, user, generation, bytes.Repeat([]byte{0}, 12))
	p := identity.Principal{OrganizationID: org, UserID: user, SessionID: session, Generation: generation, Role: "MEMBER"}
	must(`UPDATE approved_templates SET enabled=false WHERE enabled`)
	gate, err := NewGate(api, runtime, os.Getenv("HC_ACCESS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	checks, err := gate.Checks(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, check := range checks {
		seen[check.Name] = true
		if check.Name == "capacity" {
			// Available RAM is a live observation. Other test processes or real
			// workloads may correctly close admission while this suite runs.
			if check.Health.Status != "HEALTHY" && (check.Health.Status != "UNHEALTHY" || check.Health.Reason != "CAPACITY_UNAVAILABLE") {
				t.Fatal("capacity did not produce a safe bounded observation")
			}
			continue
		}
		healthy := check.Name != "runtime_manifest"
		if (check.Health.Status == "HEALTHY") != healthy {
			t.Fatalf("actual %s observation: %s %s", check.Name, check.Health.Status, check.Health.Reason)
		}
	}
	if len(seen) != 6 {
		t.Fatal("missing runtime observation")
	}
	if gate.Check(ctx, p) == nil {
		t.Fatal("unapproved runtime opened creation")
	}
	facts, err := gate.http.worker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'incomplete fixture',$3,'{}',true)`, webapi.ID(), webapi.ID(), facts.RuntimeImage)
	if gate.Check(ctx, p) == nil {
		t.Fatal("enabled but incomplete manifest opened creation")
	}
	must(`UPDATE users SET status='DISABLED' WHERE id=$1`, user)
	if _, err := gate.Checks(ctx, p); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("disabled fixture principal reached runtime observations")
	}
	t.Log("real Workspace owner, Worker images/capacity and trusted HTTPS Gateway observed; unapproved/incomplete manifests remain closed; no model inference")
}
