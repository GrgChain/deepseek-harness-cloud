package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrDatabaseBackup = errors.New("database backup or isolated restore failed; inspect controlled database/tool availability")
var ErrRecoveryTarget = errors.New("restore requires an empty isolated hc_recovery_ or hc_test_recovery_ database and matching confirmation")

func privateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInput
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrInput
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return ErrInput
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return ErrInput
	}
	return nil
}
func postgresCommand(ctx context.Context, tool, dsn string, args ...string) (*exec.Cmd, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return nil, ErrInput
	}
	password, _ := u.User.Password()
	u.User = url.User(u.User.Username())
	q := u.Query()
	if q.Has("password") {
		return nil, ErrInput
	}
	if tool == "pg_dump" || tool == "psql" {
		args = append(args, "--dbname="+u.String())
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PGDATABASE=" + u.String(), "PGPASSWORD=" + password, "PGCONNECT_TIMEOUT=5"}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

// BackupDatabase uses pg_dump's consistent snapshot. Publication is exclusive,
// durable and encrypted; the independent backup key is never stored in the DB.
func BackupDatabase(ctx context.Context, pool *pgxpool.Pool, dsn, output, operator string, key []byte) (ArchiveProof, error) {
	if !operatorName.MatchString(operator) || len(key) != 32 || !filepath.IsAbs(output) || privateDirectory(filepath.Dir(output)) != nil {
		return ArchiveProof{}, ErrInput
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return ArchiveProof{}, ErrInput
	}
	if err := db.CheckBackup(ctx, pool); err != nil {
		return ArchiveProof{}, err
	}
	var org, database string
	if err := pool.QueryRow(ctx, `SELECT id::text,current_database() FROM organizations`).Scan(&org, &database); err != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".hc-backup-*")
	if err != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	command, err := postgresCommand(ctx, "pg_dump", dsn, "--format=custom", "--no-owner", "--no-acl")
	if err != nil {
		return ArchiveProof{}, err
	}
	pipe, err := command.StdoutPipe()
	if err != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	if err = command.Start(); err != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	proof, sealErr := SealArchive(tmp, pipe, key, BackupManifest{SchemaVersion: 1, Kind: "postgres", OrganizationID: org, CreatedAt: time.Now().UTC(), Database: database})
	if sealErr != nil {
		_ = command.Process.Kill()
	}
	if err = command.Wait(); err != nil || sealErr != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	if tmp.Sync() != nil || tmp.Close() != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	if err = os.Link(tmp.Name(), output); err != nil {
		return ArchiveProof{}, ErrDatabaseBackup
	}
	if err = syncDirectory(filepath.Dir(output)); err != nil {
		return ArchiveProof{}, err
	}
	details, _ := json.Marshal(map[string]any{"schema_version": 1, "operator_ref": operator, "backup_sha256": proof.SHA256, "plain_sha256": proof.PlainSHA256, "bytes": proof.Bytes})
	if _, err = pool.Exec(ctx, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2::uuid,'BACKUP_CREATE','SUCCEEDED',$1::text,$3)`, webapi.ID(), org, details); err != nil {
		return proof, errors.New("encrypted backup published but audit append failed")
	}
	return proof, nil
}
func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type RestoreReport struct {
	Manifest    BackupManifest `json:"manifest"`
	Proof       ArchiveProof   `json:"proof"`
	Applied     bool           `json:"applied"`
	Quarantined bool           `json:"quarantined"`
}

// RestoreDatabase never targets a serving database. Complete integrity is
// verified before SQL generation. Restore and quarantine share psql's single
// transaction: a crash cannot commit an active restored authorization state.
func RestoreDatabase(ctx context.Context, dsn, input, confirmed, operator string, key []byte, apply bool) (RestoreReport, error) {
	if !operatorName.MatchString(operator) || len(key) != 32 || !filepath.IsAbs(input) || privateDirectory(filepath.Dir(input)) != nil {
		return RestoreReport{}, ErrInput
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return RestoreReport{}, ErrInput
	}
	archive, err := os.Open(input)
	if err != nil {
		return RestoreReport{}, ErrInput
	}
	defer archive.Close()
	opened, err := archive.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return RestoreReport{}, ErrInput
	}
	stage, err := os.CreateTemp(filepath.Dir(input), ".hc-recovery-*")
	if err != nil {
		return RestoreReport{}, ErrDatabaseBackup
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	manifest, proof, err := OpenArchive(stage, archive, key)
	if err != nil {
		return RestoreReport{}, err
	}
	if manifest.Kind != "postgres" {
		return RestoreReport{}, ErrInput
	}
	if stage.Sync() != nil {
		return RestoreReport{}, ErrDatabaseBackup
	}
	report := RestoreReport{Manifest: manifest, Proof: proof}
	target, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return report, ErrDatabaseBackup
	}
	defer target.Close(context.Background())
	if _, err = target.Exec(ctx, `SELECT pg_advisory_lock(731804526)`); err != nil {
		return report, ErrDatabaseBackup
	}
	var database string
	var tables, otherConnections int
	if err = target.QueryRow(ctx, `SELECT current_database(),(SELECT count(*) FROM information_schema.tables WHERE table_schema='public'),(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&database, &tables, &otherConnections); err != nil {
		return report, ErrDatabaseBackup
	}
	if !databaseName.MatchString(database) || (!strings.HasPrefix(database, "hc_recovery_") && !strings.HasPrefix(database, "hc_test_recovery_")) || database != confirmed || tables != 0 || otherConnections != 0 {
		return report, ErrRecoveryTarget
	}
	if !apply {
		return report, nil
	}
	sql, err := os.CreateTemp(filepath.Dir(input), ".hc-recovery-sql-*")
	if err != nil {
		return report, ErrDatabaseBackup
	}
	defer os.Remove(sql.Name())
	defer sql.Close()
	command, err := postgresCommand(ctx, "pg_restore", dsn, "--no-owner", "--no-acl", "--file=-", stage.Name())
	if err != nil {
		return report, err
	}
	command.Stdout = sql
	if command.Run() != nil {
		return report, ErrDatabaseBackup
	}
	// Fixed values are validated identifiers; no raw input is interpolated SQL.
	quarantine := `
UPDATE public.organizations SET status='DISABLED';
UPDATE public.memberships SET auth_generation=auth_generation+1;
UPDATE public.portal_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE public.access_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE public.access_tickets SET consumed_at=coalesce(consumed_at,clock_timestamp());
UPDATE public.access_challenges SET consumed_at=coalesce(consumed_at,clock_timestamp());
DO $$ BEGIN
 IF to_regclass('public.model_gateway_keys') IS NOT NULL THEN
  UPDATE public.model_gateway_keys SET revoked_at=coalesce(revoked_at,clock_timestamp());
  UPDATE public.model_gateway_routes SET enabled=false;
 END IF;
 IF to_regclass('public.identity_authorizations') IS NOT NULL THEN
  UPDATE public.identity_authorizations SET consumed_at=coalesce(consumed_at,clock_timestamp());
  UPDATE public.identity_codes SET consumed_at=coalesce(consumed_at,clock_timestamp());
  UPDATE public.identity_access_tokens SET revoked_at=coalesce(revoked_at,clock_timestamp());
 END IF;
END $$;
UPDATE public.oidc_transactions SET consumed_at=coalesce(consumed_at,clock_timestamp()),expires_at=least(expires_at,clock_timestamp());
UPDATE public.secrets SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE public.model_configurations SET status='REVOKED',revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE public.model_validations SET status='UNKNOWN',checked_at=NULL,expires_at=NULL,reason_code='VALIDATION_REQUIRED',lease_token=lease_token+1,lease_expires_at=NULL;
INSERT INTO public.probe_budgets(org_id,day,attempts) SELECT id,(clock_timestamp() AT TIME ZONE 'UTC')::date,1000 FROM public.organizations ON CONFLICT(org_id,day) DO UPDATE SET attempts=1000;
UPDATE public.instance_health SET status='UNKNOWN',checked_at=NULL,expires_at=NULL,reason_code='HEALTH_EXPIRED';
UPDATE public.operations SET lease_token=lease_token+1,lease_owner=NULL,lease_expires_at=NULL WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN');
DELETE FROM public.native_sessions;
UPDATE public.access_routes SET enabled=false;
UPDATE public.instances SET access_generation=access_generation+1,version=version+1,desired_state=CASE WHEN desired_state='DELETED' THEN 'DELETED' ELSE 'STOPPED' END;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE CONNECT ON DATABASE ` + pgx.Identifier{database}.Sanitize() + ` FROM PUBLIC,hc_api,hc_worker,hc_gateway,hc_model_gateway;
INSERT INTO public.audit_events(id,org_id,action,outcome,trace_id,details) VALUES('` + webapi.ID() + `','` + manifest.OrganizationID + `','RESTORE_ISOLATED','SUCCEEDED','isolated-restore',jsonb_build_object('schema_version',1,'operator_ref','` + operator + `','backup_sha256','` + proof.SHA256 + `'));
`
	if _, err = io.WriteString(sql, quarantine); err != nil {
		return report, ErrDatabaseBackup
	}
	if sql.Sync() != nil || sql.Close() != nil {
		return report, ErrDatabaseBackup
	}
	command, err = postgresCommand(ctx, "psql", dsn, "--no-psqlrc", "--single-transaction", "--set=ON_ERROR_STOP=on", "--file="+sql.Name())
	if err != nil {
		return report, err
	}
	command.Stdout = io.Discard
	if command.Run() != nil {
		return report, ErrDatabaseBackup
	}
	if err = db.CheckBackup(ctx, target); err != nil {
		return report, err
	}
	report.Applied = true
	report.Quarantined = true
	return report, nil
}
