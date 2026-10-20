package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestAuditArchiveRetentionIntegrityAndRollbackOnIsolatedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	dsn, err := config.ReadSecret(path)
	if err != nil {
		t.Fatal("private test connection required")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("fixture administrator unavailable")
	}
	defer admin.Close(context.Background())
	database := "hc_test_audit_" + strings.ReplaceAll(webapi.ID(), "-", "")[:16]
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Error("owned audit fixture cleanup failed")
		}
	}()
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("fixture pool unavailable")
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	org := webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Audit retention fixture','ACTIVE')`, org)
	for _, days := range []int{95, 94, 93, 89, 0} {
		must(`INSERT INTO audit_events(id,org_id,action,outcome,trace_id,timestamp,details) VALUES($1,$2,'CONTROLLED_FIXTURE','SUCCEEDED','synthetic',clock_timestamp()-make_interval(days=>$3),'{"schema_version":1,"reason_code":"SYNTHETIC"}')`, webapi.ID(), org, days)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='CONTROLLED_FIXTURE'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	protected := func() {
		t.Helper()
		var enabled string
		if err := pool.QueryRow(ctx, `SELECT tgenabled::text FROM pg_trigger WHERE tgrelid='audit_events'::regclass AND tgname='audit_append_only'`).Scan(&enabled); err != nil || enabled != "O" {
			t.Fatal("append-only trigger was not restored")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE action='CONTROLLED_FIXTURE'`); err == nil {
			t.Fatal("ordinary audit deletion became possible")
		}
	}
	directory := t.TempDir()
	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{29}, 32)
	service := NewAuditArchiver(pool, org, "controlled-audit-operator")
	output := filepath.Join(directory, "first.hcbk")
	preview, err := service.Batch(ctx, output, key, org, 2, false)
	if err != nil || preview.Selected != 2 || preview.Applied || count() != 5 {
		t.Fatal("bounded audit preview changed original rows", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("preview published an archive")
	}
	if _, err := service.Batch(ctx, output, key, webapi.ID(), 2, true); err == nil {
		t.Fatal("wrong organization confirmation accepted")
	}
	if _, err := service.Batch(ctx, output, key, org, 501, true); err == nil {
		t.Fatal("unbounded retention accepted")
	}
	if hold, err := service.Hold(ctx, true, "LEGAL_REVIEW", org, true); err != nil || !hold.Active || !hold.Applied {
		t.Fatal("legal hold failed", err)
	}
	if _, err := service.Batch(ctx, output, key, org, 2, true); err == nil || count() != 5 {
		t.Fatal("legal hold did not block archive/pruning")
	}
	if hold, err := service.Hold(ctx, false, "LEGAL_REVIEW_ENDED", org, false); err != nil || hold.Applied || !hold.Active {
		t.Fatal("hold release preview changed protection", err)
	}
	if _, err := service.Hold(ctx, false, "LEGAL_REVIEW_ENDED", org, true); err != nil {
		t.Fatal(err)
	}
	// Corrupt the newly sealed ciphertext before integrity verification. No
	// archive is published and no old row or trigger state may change.
	seal := service.seal
	service.seal = func(w io.Writer, r io.Reader, key []byte, m BackupManifest) (ArchiveProof, error) {
		proof, err := SealArchive(w, r, key, m)
		if err == nil {
			_, err = w.(*os.File).WriteAt([]byte{0}, 0)
		}
		return proof, err
	}
	if _, err := service.Batch(ctx, output, key, org, 2, true); err == nil || count() != 5 {
		t.Fatal("integrity failure pruned audit rows")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("corrupt archive was published")
	}
	service.seal = seal
	protected()
	must(`CREATE FUNCTION reject_hold_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='AUDIT_HOLD_CHANGE' THEN RAISE EXCEPTION 'controlled hold failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER reject_hold_fixture BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_hold_fixture()`)
	if _, err := service.Hold(ctx, true, "LEGAL_REVIEW", org, true); err == nil {
		t.Fatal("failed hold audit committed the change")
	}
	holdPreview, err := service.Hold(ctx, true, "LEGAL_REVIEW", org, false)
	if err != nil || holdPreview.Active {
		t.Fatal("hold audit rollback did not preserve prior state")
	}
	must(`DROP TRIGGER reject_hold_fixture ON audit_events`)
	must(`DROP FUNCTION reject_hold_fixture()`)
	// A failure after DELETE must roll back deletion and DDL together. The
	// verified encrypted archive remains as evidence, never a missing preimage.
	must(`CREATE FUNCTION reject_retention_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='AUDIT_ARCHIVE' THEN RAISE EXCEPTION 'controlled late failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER reject_retention_fixture BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_retention_fixture()`)
	if _, err := service.Batch(ctx, output, key, org, 2, true); err == nil || count() != 5 {
		t.Fatal("late audit failure lost original events")
	}
	protected()
	must(`DROP TRIGGER reject_retention_fixture ON audit_events`)
	must(`DROP FUNCTION reject_retention_fixture()`)
	if _, err := service.Batch(ctx, output, key, org, 2, true); err == nil {
		t.Fatal("existing archive could be overwritten")
	}
	output = filepath.Join(directory, "success.hcbk")
	report, err := service.Batch(ctx, output, key, org, 2, true)
	if err != nil || !report.Applied || report.Selected != 2 || count() != 3 {
		t.Fatal("bounded audit pruning failed", err)
	}
	protected()
	encrypted, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var plain bytes.Buffer
	manifest, proof, err := OpenArchive(&plain, bytes.NewReader(encrypted), key)
	if err != nil || manifest.Kind != "audit" || manifest.OrganizationID != org || manifest.AuditCount != 2 || proof != report.Proof || bytes.Contains(encrypted, []byte("CONTROLLED_FIXTURE")) {
		t.Fatal("audit archive lacked authenticated original events", err)
	}
	lines := bytes.Split(bytes.TrimSpace(plain.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatal("archive row count mismatch")
	}
	for _, line := range lines {
		var event struct {
			ID        string    `json:"id"`
			Action    string    `json:"action"`
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(line, &event) != nil || !uuid.MatchString(event.ID) || event.Action != "CONTROLLED_FIXTURE" || !event.Timestamp.Before(manifest.AuditCutoff) {
			t.Fatal("archive contains an ineligible audit event")
		}
	}
	last, err := service.Batch(ctx, filepath.Join(directory, "last.hcbk"), key, org, 500, true)
	if err != nil || last.Selected != 1 || count() != 2 {
		t.Fatal("retention continuation touched recent events", err)
	}
	for _, role := range []string{"hc_api", "hc_worker", "hc_gateway"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize())
		if err == nil {
			_, err = tx.Exec(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_append_only`)
		}
		_ = tx.Rollback(ctx)
		if err == nil {
			t.Fatal("runtime role can disable audit protection", role)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".hcbk") {
			t.Fatal("temporary or unencrypted audit export remained")
		}
	}
	if evidence := os.Getenv("HC_AUDIT_ARCHIVE_EVIDENCE_FILE"); evidence != "" {
		if !filepath.IsAbs(evidence) || privateDirectory(filepath.Dir(evidence)) != nil {
			t.Fatal("private audit evidence required")
		}
		raw, _ := json.MarshalIndent(map[string]any{"outcome": "PASS", "retention_days": 90, "bounded_archive_rows": 2, "recent_events_preserved": true, "legal_hold_blocks_pruning": true, "integrity_failure_preserves_original": true, "late_audit_failure_rolls_back_deletion_and_ddl": true, "runtime_roles_cannot_disable_trigger": true, "archive_sha256": report.Proof.SHA256}, "", "  ")
		if err := os.WriteFile(evidence, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
