package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrAuditArchive = errors.New("audit retention failed; originals remain protected unless the archive transaction committed")
var ErrAuditHold = errors.New("legal hold protects this organization's complete audit history")
var auditReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type AuditArchiveReport struct {
	Selected int          `json:"selected"`
	Applied  bool         `json:"applied"`
	Held     bool         `json:"held"`
	Cutoff   time.Time    `json:"cutoff"`
	BatchID  string       `json:"batch_id,omitempty"`
	Proof    ArchiveProof `json:"proof"`
}

type AuditHoldReport struct {
	Active    bool `json:"active"`
	Requested bool `json:"requested"`
	Applied   bool `json:"applied"`
}

// AuditArchiver runs only with the independent table-owner maintenance account.
// Table DDL and deletion share one transaction. The exclusive lock prevents any
// writer from observing the temporary trigger change. Publication and full
// ciphertext verification precede deletion; rollback preserves the old rows.
type AuditArchiver struct {
	pool          *pgxpool.Pool
	org, operator string
	seal          func(io.Writer, io.Reader, []byte, BackupManifest) (ArchiveProof, error)
}

func NewAuditArchiver(pool *pgxpool.Pool, org, operator string) *AuditArchiver {
	return &AuditArchiver{pool: pool, org: org, operator: operator, seal: SealArchive}
}

func (s *AuditArchiver) begin(ctx context.Context, confirmed string, apply bool) (pgx.Tx, error) {
	if s == nil || s.pool == nil || !uuid.MatchString(s.org) || !operatorName.MatchString(s.operator) || (apply && confirmed != s.org) {
		return nil, ErrInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, ErrAuditArchive
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = db.Check(ctx, tx); err != nil {
		return nil, ErrAuditArchive
	}
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='4s'; SET LOCAL idle_in_transaction_session_timeout='10s'`); err != nil {
		return nil, ErrAuditArchive
	}
	var owner bool
	if err = tx.QueryRow(ctx, `SELECT current_user NOT IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway') AND pg_has_role(current_user,relowner,'USAGE') AND current_setting('session_replication_role')='origin' FROM pg_class WHERE oid='audit_events'::regclass`).Scan(&owner); err != nil || !owner {
		return nil, ErrAuditArchive
	}
	var org string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id=$1 FOR UPDATE`, s.org).Scan(&org); err != nil {
		return nil, ErrAuditArchive
	}
	// All trusted hold changes acquire this organization row first. SHARE also
	// protects against unrelated direct maintenance while we verify the file.
	if _, err = tx.Exec(ctx, `LOCK TABLE audit_retention_holds,storage_assets IN SHARE MODE`); err != nil {
		return nil, ErrAuditArchive
	}
	ok = true
	return tx, nil
}

func (s *AuditArchiver) held(ctx context.Context, tx pgx.Tx) (bool, error) {
	var held bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_retention_holds WHERE org_id=$1 AND active) OR EXISTS(SELECT 1 FROM storage_assets WHERE org_id=$1 AND legal_hold)`, s.org).Scan(&held)
	return held, err
}

func (s *AuditArchiver) append(ctx context.Context, tx pgx.Tx, id, action string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2::uuid,$3,'SUCCEEDED',$1::text,$4)`, id, s.org, action, raw)
	return err
}

func (s *AuditArchiver) Hold(ctx context.Context, active bool, reason, confirmed string, apply bool) (AuditHoldReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !auditReason.MatchString(reason) {
		return AuditHoldReport{}, ErrInput
	}
	tx, err := s.begin(ctx, confirmed, apply)
	if err != nil {
		return AuditHoldReport{}, err
	}
	defer tx.Rollback(context.Background())
	var previous bool
	if err = tx.QueryRow(ctx, `SELECT coalesce((SELECT active FROM audit_retention_holds WHERE org_id=$1),false)`, s.org).Scan(&previous); err != nil {
		return AuditHoldReport{}, ErrAuditArchive
	}
	report := AuditHoldReport{Active: previous, Requested: active}
	if !apply || previous == active {
		return report, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_retention_holds(org_id,active,reason_code,operator_ref) VALUES($1,$2,$3,$4) ON CONFLICT(org_id) DO UPDATE SET active=excluded.active,reason_code=excluded.reason_code,operator_ref=excluded.operator_ref,changed_at=clock_timestamp()`, s.org, active, reason, s.operator); err != nil {
		return report, ErrAuditArchive
	}
	if err = s.append(ctx, tx, webapi.ID(), "AUDIT_HOLD_CHANGE", map[string]any{"schema_version": 1, "operator_ref": s.operator, "reason_code": reason, "previous": previous, "active": active}); err != nil {
		return report, ErrAuditArchive
	}
	if err = tx.Commit(ctx); err != nil {
		return report, ErrAuditArchive
	}
	report.Active, report.Applied = active, true
	return report, nil
}

func (s *AuditArchiver) Batch(ctx context.Context, output string, key []byte, confirmed string, batch int, apply bool) (AuditArchiveReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if batch < 1 || batch > 500 || (apply && (len(key) != 32 || !filepath.IsAbs(output) || filepath.Clean(output) != output || privateDirectory(filepath.Dir(output)) != nil)) {
		return AuditArchiveReport{}, ErrInput
	}
	if apply {
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			return AuditArchiveReport{}, ErrInput
		}
	}
	tx, err := s.begin(ctx, confirmed, apply)
	if err != nil {
		return AuditArchiveReport{}, err
	}
	defer tx.Rollback(context.Background())
	held, err := s.held(ctx, tx)
	if err != nil {
		return AuditArchiveReport{}, ErrAuditArchive
	}
	report := AuditArchiveReport{Held: held}
	if held {
		if apply {
			return report, ErrAuditHold
		}
		return report, nil
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE`); err != nil {
		return report, ErrAuditArchive
	}
	var enabled string
	if err = tx.QueryRow(ctx, `SELECT tgenabled::text FROM pg_trigger WHERE tgrelid='audit_events'::regclass AND tgname='audit_append_only' AND NOT tgisinternal`).Scan(&enabled); err != nil || enabled != "O" {
		return report, ErrAuditArchive
	}
	var created time.Time
	var database string
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp(),current_database()`).Scan(&created, &database); err != nil {
		return report, ErrAuditArchive
	}
	created = created.UTC()
	report.Cutoff = created.Add(-90 * 24 * time.Hour)
	if !apply {
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM audit_events WHERE org_id=$1 AND timestamp<$2 ORDER BY timestamp,id LIMIT $3) a`, s.org, report.Cutoff, batch).Scan(&report.Selected); err != nil {
			return report, ErrAuditArchive
		}
		return report, nil
	}
	// Keep the bounded JSONL only in memory. A process crash must not leave an
	// unencrypted audit export on the backup filesystem.
	var plain bytes.Buffer
	defer func() { clear(plain.Bytes()) }()
	rows, err := tx.Query(ctx, `SELECT id::text,row_to_json(audit_events)::text FROM audit_events WHERE org_id=$1 AND timestamp<$2 ORDER BY timestamp,id LIMIT $3`, s.org, report.Cutoff, batch)
	if err != nil {
		return report, ErrAuditArchive
	}
	var ids []string
	var total int
	for rows.Next() {
		var id, body string
		if err = rows.Scan(&id, &body); err != nil {
			rows.Close()
			return report, ErrAuditArchive
		}
		total += len(body) + 1
		if len(body) > 2<<20 || total > 16<<20 {
			rows.Close()
			return report, ErrAuditArchive
		}
		if _, err = io.WriteString(&plain, body+"\n"); err != nil {
			rows.Close()
			return report, ErrAuditArchive
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, ErrAuditArchive
	}
	report.Selected = len(ids)
	if len(ids) == 0 {
		return report, nil
	}
	report.BatchID = webapi.ID()
	manifest := BackupManifest{SchemaVersion: 1, Kind: "audit", OrganizationID: s.org, CreatedAt: created, Database: database, AuditBatchID: report.BatchID, AuditCount: len(ids), AuditCutoff: report.Cutoff}
	sealed, err := os.CreateTemp(filepath.Dir(output), ".hc-audit-sealed-*")
	if err != nil {
		return report, ErrAuditArchive
	}
	defer sealed.Close()
	defer os.Remove(sealed.Name())
	proof, err := s.seal(sealed, bytes.NewReader(plain.Bytes()), key, manifest)
	if err != nil || sealed.Sync() != nil {
		return report, ErrAuditArchive
	}
	if _, err = sealed.Seek(0, io.SeekStart); err != nil {
		return report, ErrAuditArchive
	}
	verified, verifiedProof, err := OpenArchive(io.Discard, sealed, key)
	if err != nil || verified != manifest || verifiedProof != proof {
		return report, ErrAuditArchive
	}
	if err = sealed.Close(); err != nil {
		return report, ErrAuditArchive
	}
	if err = os.Link(sealed.Name(), output); err != nil {
		return report, ErrAuditArchive
	}
	if err = syncDirectory(filepath.Dir(output)); err != nil {
		return report, ErrAuditArchive
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_append_only`); err != nil {
		return report, ErrAuditArchive
	}
	deleted, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE org_id=$1 AND id=ANY($2::uuid[]) AND timestamp<$3`, s.org, ids, report.Cutoff)
	if err != nil || deleted.RowsAffected() != int64(len(ids)) {
		return report, ErrAuditArchive
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_append_only`); err != nil {
		return report, ErrAuditArchive
	}
	if err = s.append(ctx, tx, report.BatchID, "AUDIT_ARCHIVE", map[string]any{"schema_version": 1, "operator_ref": s.operator, "batch_id": report.BatchID, "archived_count": len(ids), "cutoff": report.Cutoff, "backup_sha256": proof.SHA256, "plain_sha256": proof.PlainSHA256, "bytes": proof.Bytes}); err != nil {
		return report, ErrAuditArchive
	}
	if err = db.Check(ctx, tx); err != nil {
		return report, ErrAuditArchive
	}
	if err = tx.Commit(ctx); err != nil {
		return report, ErrAuditArchive
	}
	report.Applied, report.Proof = true, proof
	return report, nil
}
