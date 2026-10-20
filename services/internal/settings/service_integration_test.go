package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type blockingProbe struct {
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingProbe) Check(ctx context.Context, _ Model, key string) (string, bool) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return "", false
	case <-ctx.Done():
		return "MODEL_TRANSPORT_UNAVAILABLE", false
	}
}
func problemCode(err error) string {
	var p *webapi.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}
func TestSettingsWithRestrictedPostgresRole(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL settings database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("missing test connection file")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_settings"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	adminDB, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	tx, err := adminDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	appRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "api-dsn"))
	if err != nil {
		t.Fatal("restricted test identity missing")
	}
	appURL, err := url.Parse(strings.TrimSpace(string(appRaw)))
	if err != nil {
		t.Fatal("invalid application connection")
	}
	appURL.Path = u.Path
	appDB, err := pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer appDB.Close()
	org := "00000000-0000-4000-8000-000000000040"
	if _, err := adminDB.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Settings test','ACTIVE') ON CONFLICT(id) DO NOTHING`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := adminDB.Exec(ctx, `DELETE FROM probe_budgets WHERE org_id=$1`, org); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{9}, 32))
	principal := func(user, role string) identity.Principal {
		t.Helper()
		_, err := adminDB.Exec(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://test.example',$2,$1,'ACTIVE') ON CONFLICT(id) DO NOTHING`, user, user)
		if err != nil {
			t.Fatal(err)
		}
		_, err = adminDB.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE') ON CONFLICT(org_id,user_id) DO UPDATE SET role=EXCLUDED.role,status='ACTIVE'`, org, user, role)
		if err != nil {
			t.Fatal(err)
		}
		p := identity.Principal{OrganizationID: org, UserID: user, Role: role, SessionID: webapi.ID()}
		if err := adminDB.QueryRow(ctx, `SELECT auth_generation FROM memberships WHERE org_id=$1 AND user_id=$2`, org, user).Scan(&p.Generation); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(webapi.ID()))
		csrf := sha256.Sum256([]byte(webapi.ID()))
		envelope, _ := cipher.Encrypt([]byte("synthetic-csrf"), security.Context{Environment: "test", OrganizationID: org, RecordID: p.SessionID, Purpose: "portal-csrf"})
		_, err = adminDB.Exec(ctx, `INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '1 hour')`, p.SessionID, hash[:], org, user, p.Generation, csrf[:], envelope.Ciphertext, envelope.KeyID, envelope.Nonce)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	admin := principal("00000000-0000-4000-8000-000000000041", "ADMIN")
	member := principal("00000000-0000-4000-8000-000000000042", "MEMBER")
	policy, _ := ParsePolicy([]byte(`{"schema_version":1,"endpoints":["https://api.deepseek.com/anthropic"],"models":["test-model"]}`))
	service := NewService(appDB, cipher, policy, "test", false)
	t.Run("runtime defaults initialize and update without models", func(t *testing.T) { exerciseRuntimeDefaults(t, ctx, adminDB, service, admin, member) })
	current := int64(0)
	if m, err := service.GetModel(ctx, admin, 0); err == nil {
		current = m.Version
	} else if problemCode(err) != "NOT_FOUND" {
		t.Fatal(err)
	}
	key := "synthetic-key-" + webapi.ID()
	input := ModelInput{Provider: "deepseek-official", Protocol: "deepseek-messages", Endpoint: policy.Endpoints[0], Model: "test-model", APIKey: &key, ExpectedVersion: &current}
	member.Role = "ADMIN"
	if _, err := service.SaveModel(ctx, member, input, "test"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("forged admin role accepted: %v", err)
	}
	first, err := service.SaveModel(ctx, admin, input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "UNVALIDATED" || first.Validation.Status != "UNKNOWN" {
		t.Fatal("save fabricated valid status")
	}
	t.Run("native Go gateway registers seven providers and isolates credentials", func(t *testing.T) { exerciseNativeGateway(t, ctx, adminDB, appDB, cipher, admin, member) })
	t.Run("provider aggregate CRUD and saved credentials", func(t *testing.T) { exerciseProviderAccounts(t, ctx, adminDB, appDB, cipher, member, admin) })
	t.Run("custom protocols and discovered models are owner isolated", func(t *testing.T) { exerciseCustomSelections(t, ctx, adminDB, appDB, cipher, member, admin) })
	t.Run("personal provider keys are encrypted and connections are owner scoped", func(t *testing.T) {
		if _, e := adminDB.Exec(ctx, `DELETE FROM user_model_defaults WHERE org_id=$1`, org); e != nil {
			t.Fatal(e)
		}
		privatePolicy, e := ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"v1","addresses":["10.50.0.10/32"],"models":[{"id":"fixture","response_models":["fixture"],"context_window":32768,"max_output_tokens":4096,"streaming":true,"tool_use":true}],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
		if e != nil {
			t.Fatal(e)
		}
		handler := NewHandler(NewService(appDB, cipher, privatePolicy, "test", false), nil, nil)
		var catalogID string
		if e := adminDB.QueryRow(ctx, `SELECT id::text FROM model_catalog_entries WHERE org_id=$1 AND provider_id='moonshot' AND enabled ORDER BY created_at LIMIT 1`, org).Scan(&catalogID); e != nil {
			t.Fatal(e)
		}
		key := "synthetic-private-provider-" + webapi.ID()
		receipt := "personal-create-key-" + webapi.ID()
		body, _ := json.Marshal(map[string]any{"name": "Personal Kimi", "catalog_entry_id": catalogID, "api_key": key})
		request := func(method, path string, body []byte, principal identity.Principal) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", receipt)
			w := httptest.NewRecorder()
			handler.ServeAuthenticated(w, r, principal)
			return w
		}
		created := request("POST", "/v1/my-models", body, member)
		if created.Code != 202 {
			t.Fatalf("personal create status=%d body=%s", created.Code, created.Body.String())
		}
		var connection struct {
			ID         string `json:"id"`
			Version    int64  `json:"version"`
			SyncStatus string `json:"sync_status"`
		}
		if json.Unmarshal(created.Body.Bytes(), &connection) != nil || connection.ID == "" || connection.Version != 1 || connection.SyncStatus != "PENDING_SYNC" {
			t.Fatal("create fabricated ready state")
		}
		for _, response := range []*httptest.ResponseRecorder{created, request("GET", "/v1/my-models/"+connection.ID, nil, member)} {
			if strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), "ciphertext") || strings.Contains(response.Body.String(), "provider_secret_id") {
				t.Fatal("provider secret leaked")
			}
		}
		if other := request("GET", "/v1/my-models/"+connection.ID, nil, admin); other.Code != 404 {
			t.Fatal("administrator implicitly read another user's model", other.Code)
		}
		if repeat := request("POST", "/v1/my-models", body, member); repeat.Code != 202 || !strings.Contains(repeat.Body.String(), connection.ID) {
			t.Fatal("personal create replay duplicated connection")
		}
		var ciphertext []byte
		if e := adminDB.QueryRow(ctx, `SELECT ciphertext FROM external_provider_secrets WHERE connection_id=$1`, connection.ID).Scan(&ciphertext); e != nil || bytes.Contains(ciphertext, []byte(key)) {
			t.Fatal("provider key not encrypted")
		}
		exercisePersonalSync(t, ctx, adminDB, appDB, cipher, privatePolicy, member, admin, connection.ID, key)
		exercisePersonalKeyReuse(t, ctx, adminDB, appDB, cipher, privatePolicy, member, admin)
		if _, e := adminDB.Exec(ctx, `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, connection.ID); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("preflight checks selected resources against the real quota", func(t *testing.T) {
		must := func(sql string, args ...any) {
			t.Helper()
			if _, e := adminDB.Exec(ctx, sql, args...); e != nil {
				t.Fatal(e)
			}
		}
		template, version, large, small := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
		must(`UPDATE approved_templates SET enabled=false WHERE enabled`)
		must(`UPDATE approved_specs SET enabled=false WHERE enabled`)
		must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'resource-fixture',$3,'{}',true)`, template, version, "sha256:"+strings.Repeat("a", 64))
		must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,2000,4294967296,10737418240,true),($2,1000,2147483648,10737418240,true)`, large, small)
		must(`INSERT INTO quota_policies(org_id,cpu_millicores,memory_bytes) VALUES($1,1000,2147483648) ON CONFLICT(org_id) DO UPDATE SET cpu_millicores=1000,memory_bytes=2147483648`, org)
		must(`INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,model_config_id,model_config_version) VALUES($1,1,$2,$3,$4,$5,$6) ON CONFLICT(org_id) DO UPDATE SET template_id=$2,template_version=$3,spec_id=$4,model_config_id=$5,model_config_version=$6`, org, template, version, large, first.ID, first.Version)
		defer must(`DELETE FROM organization_defaults WHERE org_id=$1`, org)
		probe := func(context.Context) error { return nil }
		for _, test := range []struct{ id, name, status, reason string }{{"", "quota", "UNHEALTHY", "QUOTA_EXCEEDED"}, {small, "quota", "HEALTHY", ""}, {webapi.ID(), "spec", "UNHEALTHY", "SPEC_NOT_CONFIGURED"}} {
			result, e := service.PreflightForModel(ctx, admin, probe, "", 0, test.id)
			if e != nil {
				t.Fatal(e)
			}
			if result.Quota == nil || result.Quota.Available.CPU != 1000 || result.Quota.Available.Memory != 2<<30 {
				t.Fatalf("preflight omitted effective remaining quota: %+v", result.Quota)
			}
			found := false
			for _, check := range result.Checks {
				if check.Name == test.name {
					found = true
					if check.Health.Status != test.status || check.Health.Reason != test.reason {
						t.Fatalf("selected spec check differs: %+v", check)
					}
				}
			}
			if !found {
				t.Fatal("missing resource check")
			}
		}
		must(`UPDATE approved_specs SET enabled=false WHERE id=$1`, small)
		result, e := service.PreflightForModel(ctx, admin, probe, "", 0, small)
		if e != nil {
			t.Fatal(e)
		}
		for _, check := range result.Checks {
			if check.Name == "spec" && check.Health.Status != "UNHEALTHY" {
				t.Fatal("disabled resource choice accepted")
			}
		}
		handler := NewHandler(service, probe, nil)
		requester := admin
		requester.Permissions = []string{"configuration:read"}
		for _, query := range []string{"spec_id=bad", "spec_id=" + small + "&spec_id=" + large, "spec_id=" + small + "&unknown=1", "spec_id=" + small + "&model_configuration_version=1"} {
			response := httptest.NewRecorder()
			handler.ServeAuthenticated(response, httptest.NewRequest("GET", "/v1/preflight?"+query, nil), requester)
			if response.Code != 422 {
				t.Fatal("malformed spec query accepted", response.Code)
			}
		}
	})

	t.Run("quota preview includes held and retained resources", func(t *testing.T) {
		must := func(sql string, args ...any) {
			t.Helper()
			if _, e := adminDB.Exec(ctx, sql, args...); e != nil {
				t.Fatal(e)
			}
		}
		must(`INSERT INTO quota_policies(org_id,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,2000,4294967296,107374182400) ON CONFLICT(org_id) DO UPDATE SET cpu_millicores=2000,memory_bytes=4294967296,storage_bytes=107374182400`, org)
		spec := Spec{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}
		check := func(want string) {
			t.Helper()
			_, reason, e := service.quotaPreview(ctx, admin, spec)
			if e != nil || reason != want {
				t.Fatalf("quota preview=%s want=%s err=%v", reason, want, e)
			}
		}
		check("")
		must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,0,4294967296,10737418240)`, org, admin.UserID)
		check("QUOTA_EXCEEDED")
		must(`UPDATE user_quota_policies SET cpu_millicores=2000 WHERE org_id=$1 AND owner_id=$2`, org, admin.UserID)
		check("")
		preview, _, e := service.quotaPreview(ctx, admin, spec)
		if e != nil || preview == nil || preview.Available != (userquota.Limits{Instances: 2, CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}) || preview.Required != (userquota.Limits{Instances: 1, CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}) {
			t.Fatalf("personal remaining quota differs: %+v %v", preview, e)
		}
		// Another owner in the same shared pool retains their own defaults.
		_, reason, e := service.quotaPreview(ctx, member, spec)
		if e != nil || reason != "" {
			t.Fatal("personal preflight affected another owner", reason, e)
		}
		must(`DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, admin.UserID)
		instance, operation, running, storage := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,'RUNNING','CREATING',$5,$6,'{}')`, instance, org, admin.UserID, "quota-"+instance[:8], first.ID, first.Version)
		defer func() {
			must(`DELETE FROM quota_reservations WHERE instance_id=$1`, instance)
			must(`DELETE FROM operations WHERE id=$1`, operation)
			must(`DELETE FROM instances WHERE id=$1`, instance)
		}()
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','ACCEPTED','QUEUED','{}',$4)`, operation, instance, admin.UserID, operation)
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'RUNNING',1,'HELD'),($6,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, running, org, admin.UserID, instance, operation, storage)
		// An existing running reservation has no independent count cap.
		check("")
		cpu := webapi.ID()
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'CPU',2000,'HELD')`, cpu, org, admin.UserID, instance, operation)
		check("QUOTA_EXCEEDED")
		preview, _, e = service.quotaPreview(ctx, member, spec)
		if e != nil || preview == nil || preview.Available.CPU != 0 {
			t.Fatalf("other owner's held compute did not reduce system availability: %+v %v", preview, e)
		}
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE id=$1`, running)
		check("QUOTA_EXCEEDED")
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE id=$1`, cpu)
		check("")
		must(`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp() WHERE id=$1`, instance)
		must(`UPDATE quota_reservations SET amount=107374182400 WHERE id=$1`, storage)
		check("QUOTA_EXCEEDED")
		preview, _, e = service.quotaPreview(ctx, admin, spec)
		if e != nil || preview == nil || preview.Available.Storage != 0 {
			t.Fatalf("retained storage was presented as available: %+v %v", preview, e)
		}
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE id=$1`, storage)
		orphan := webapi.ID()
		defer adminDB.Exec(context.Background(), `DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, org, orphan)
		must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,'STORAGE',107374182400,$4,$5,'TEST_RETAIN','fixture')`, org, admin.UserID, orphan, webapi.ID(), strings.Repeat("a", 64))
		check("QUOTA_EXCEEDED")
		must(`DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, org, orphan)
		check("")
	})
	var id string
	var envelope security.Envelope
	if err := adminDB.QueryRow(ctx, `SELECT s.id::text,s.key_id,s.nonce,s.ciphertext FROM secrets s JOIN model_configurations c ON c.credential_ref=s.id WHERE c.id=$1 AND c.version=$2`, first.ID, first.Version).Scan(&id, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope.Ciphertext, []byte(key)) {
		t.Fatal("plaintext stored")
	}
	plaintext, err := cipher.Decrypt(envelope, service.aad(org, id))
	if err != nil || string(plaintext) != key {
		t.Fatal("credential encryption is unusable")
	}
	input.ExpectedVersion = &first.Version
	input.APIKey = nil
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.SaveModel(ctx, admin, input, "test"); results <- err }()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if problemCode(err) == "VERSION_CONFLICT" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("optimistic concurrency failed")
	}
	probe := &blockingProbe{entered: make(chan struct{}), release: make(chan struct{})}
	service.probe = probe
	validated := make(chan error, 1)
	go func() {
		m, err := service.Validate(ctx, admin, first.Version, "test")
		if err == nil && (m.Status != "VALID" || m.Validation.Status != "HEALTHY") {
			err = errors.New("valid response missing")
		}
		validated <- err
	}()
	<-probe.entered
	if _, err := service.Validate(ctx, admin, first.Version, "test"); problemCode(err) != "STATE_CONFLICT" {
		t.Fatalf("concurrent validation accepted: %v", err)
	}
	close(probe.release)
	if err := <-validated; err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		status, checked, expires, lease, expected string
	}{
		{"CHECKING", "clock_timestamp()-interval '1 minute'", "clock_timestamp()+interval '4 minutes'", "clock_timestamp()-interval '1 second'", "UNKNOWN"},
		{"CHECKING", "NULL", "NULL", "clock_timestamp()+interval '40 seconds'", "CHECKING"},
		{"HEALTHY", "clock_timestamp()+interval '1 minute'", "clock_timestamp()+interval '6 minutes'", "NULL", "UNKNOWN"},
		{"UNHEALTHY", "clock_timestamp()-interval '6 minutes'", "clock_timestamp()-interval '1 minute'", "NULL", "UNKNOWN"},
	} {
		// Expressions are fixed test cases, never input from an API or credential.
		if _, err := adminDB.Exec(ctx, `UPDATE model_validations SET status=$3,checked_at=`+state.checked+`,expires_at=`+state.expires+`,lease_expires_at=`+state.lease+` WHERE config_id=$1 AND config_version=$2`, first.ID, first.Version, state.status); err != nil {
			t.Fatal(err)
		}
		model, err := service.GetModel(ctx, member, first.Version)
		if err != nil || model.Validation.Status != state.expected {
			t.Fatalf("model validation %s expected %s: %+v %v", state.status, state.expected, model.Validation, err)
		}
		if state.expected == "CHECKING" && (model.Validation.CheckedAt == nil || model.Validation.ExpiresAt == nil || model.Validation.ExpiresAt.Sub(*model.Validation.CheckedAt) != 45*time.Second) {
			t.Fatal("active validation did not expose its own bounded lease")
		}
	}
	// A budget row can be contended long enough for the lease to expire. A
	// waiter must recheck the database deadline after obtaining the budget lock.
	lease, err := service.acquire(ctx, admin, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	budgetLock, err := adminDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer budgetLock.Rollback(context.Background())
	var before int
	if err = budgetLock.QueryRow(ctx, `SELECT attempts FROM probe_budgets WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date FOR UPDATE`, org).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	if err = adminDB.QueryRow(ctx, `UPDATE model_validations SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE config_id=$1 AND config_version=$2 RETURNING lease_expires_at`, first.ID, first.Version).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	reserved := make(chan error, 1)
	go func() { reserved <- service.reserveProbe(ctx, org, lease) }()
	blocked := false
	for range 80 {
		if err = adminDB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%probe_budgets%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("budget reservation did not reach the held database lock")
	}
	if remaining := time.Until(deadline) + 20*time.Millisecond; remaining > 0 {
		time.Sleep(remaining)
	}
	if err = budgetLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-reserved; problemCode(err) != "PROBE_BUDGET_EXCEEDED" {
		t.Fatalf("expired waiter reserved a paid request: %v", err)
	}
	var after int
	if err = adminDB.QueryRow(ctx, `SELECT attempts FROM probe_budgets WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date`, org).Scan(&after); err != nil || after != before {
		t.Fatal("expired reservation consumed a request budget")
	}
	lease, err = service.acquire(ctx, admin, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adminDB.Exec(ctx, `UPDATE probe_budgets SET attempts=999 WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date`, org); err != nil {
		t.Fatal(err)
	}
	lastSlot := make(chan error, 2)
	for range 2 {
		go func() { lastSlot <- service.reserveProbe(ctx, org, lease) }()
	}
	wins, conflicts = 0, 0
	for range 2 {
		err = <-lastSlot
		if err == nil {
			wins++
		} else if problemCode(err) == "PROBE_BUDGET_EXCEEDED" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if err = adminDB.QueryRow(ctx, `SELECT attempts FROM probe_budgets WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date`, org).Scan(&after); err != nil || after != 1000 || wins != 1 || conflicts != 1 {
		t.Fatal("concurrent reservations exceeded the final daily request slot")
	}
	if _, err = adminDB.Exec(ctx, `UPDATE model_validations SET status='HEALTHY',lease_expires_at=NULL WHERE config_id=$1 AND config_version=$2`, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := adminDB.Exec(ctx, `UPDATE model_validations SET checked_at=clock_timestamp()-interval '6 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE config_id=$1 AND config_version=$2`, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	expired, err := service.GetModel(ctx, member, first.Version)
	if err != nil || expired.Validation.Status != "UNKNOWN" {
		t.Fatal("expired validation trusted")
	}
	revoked, err := service.Revoke(ctx, admin, first.Version, "test")
	if err != nil || revoked.Status != "REVOKED" || revoked.CredentialConfigured {
		t.Fatalf("revoke failed: %v", err)
	}
	head, err := service.GetModel(ctx, admin, 0)
	if err != nil || head.Status != "REVOKED" {
		t.Fatal("shared credential remained valid")
	}
	input.ExpectedVersion = &head.Version
	if _, err := service.SaveModel(ctx, admin, input, "test"); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("revoked credential reused")
	}
	if _, err := service.Revoke(ctx, admin, first.Version, "test"); err != nil {
		t.Fatal("revoke not idempotent")
	}
	if _, err := appDB.Exec(ctx, `UPDATE approved_specs SET enabled=true`); err == nil {
		t.Fatal("API role can approve runtime resources")
	}
	if _, err := appDB.Exec(ctx, `UPDATE memberships SET role='ADMIN' WHERE org_id=$1`, org); err == nil {
		t.Fatal("API role can elevate identities")
	}
}
