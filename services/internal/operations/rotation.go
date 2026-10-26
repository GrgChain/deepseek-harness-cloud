// Package operations contains trusted operator workflows, separate from member APIs.
package operations

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrInput = errors.New("invalid controlled operations input")
var operatorName = regexp.MustCompile(`^[A-Za-z0-9_.:@/-]{1,128}$`)
var keyName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

type Rotator struct {
	pool                                *pgxpool.Pool
	cipher                              *security.SecretCipher
	environment, organization, operator string
}

func NewRotator(pool *pgxpool.Pool, cipher *security.SecretCipher, environment, organization, operator string) *Rotator {
	return &Rotator{pool, cipher, environment, organization, operator}
}

// Identifiers are fixed program constants, never operator-supplied SQL.
type encryptedTable struct{ table, id, org, record, key, nonce, cipher, purpose string }

var encryptedTables = []encryptedTable{
	{"mcp_oauth_transactions", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "connector-oauth"},
	{"mcp_connectors", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "connector-credential"},
	{"secrets", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "model-key"},
	{"external_provider_secrets", "id", "org_id::text", "owner_id::text||'/'||connection_id::text||'/'||id::text||'/'||bound_version::text", "key_id", "nonce", "ciphertext", "provider-key"},
	{"identity_password_resets", "id", "org_id::text", "user_id::text||'/'||id::text", "key_id", "nonce", "ciphertext", "identity-password-hash"},
	{"portal_sessions", "id", "org_id::text", "id::text", "csrf_key_id", "csrf_nonce", "csrf_cipher", "portal-csrf"},
	{"oidc_transactions", "id", "$3::text", "id::text", "key_id", "pkce_nonce", "pkce_cipher", "oidc-pkce"},
	{"native_sessions", "instance_id", "(SELECT org_id::text FROM public.instances WHERE id=t.instance_id)", "instance_id::text||':'||runtime_generation::text", "key_id", "nonce", "cookie_ciphertext", "native-cookie"},
	{"personal_skills", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "skill-bundle"},
	{"skill_import_previews", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "skill-import"},
	{"skill_replacements", "skill_id", "(SELECT org_id::text FROM public.personal_skills WHERE id=t.skill_id)", "skill_id::text", "key_id", "nonce", "ciphertext", "skill-bundle"},
	{"context_connections", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "context-credential"},
	{"context_operations", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "context-operation"},
	{"context_previews", "id", "org_id::text", "id::text", "key_id", "nonce", "ciphertext", "context-preview"},
}

func (r *Rotator) valid(from string) bool {
	return r.pool != nil && r.cipher != nil && keyName.MatchString(from) && from != r.cipher.CurrentKeyID() && uuid.MatchString(r.organization) && operatorName.MatchString(r.operator) && r.environment != ""
}
func (r *Rotator) Count(ctx context.Context, from string) (int64, error) {
	if !r.valid(from) {
		return 0, ErrInput
	}
	var total int64
	for _, t := range encryptedTables {
		var n int64
		if err := r.pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM public.%s WHERE %s=$1`, t.table, t.key), from).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
func (r *Rotator) Batch(ctx context.Context, from string, limit int) (int, error) {
	if !r.valid(from) || limit < 1 || limit > 500 {
		return 0, ErrInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	var actualOrg string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations`).Scan(&actualOrg); err != nil {
		return 0, err
	}
	if actualOrg != r.organization {
		return 0, ErrInput
	}
	total := 0
	for _, t := range encryptedTables {
		if total == limit {
			break
		}
		// $3 is used only for the singleton organization on OIDC records.
		query := fmt.Sprintf(`SELECT t.%s::text,%s,%s,t.%s,t.%s,t.%s FROM public.%s t WHERE t.%s=$1 AND $3::text<>'' ORDER BY t.%s LIMIT $2 FOR UPDATE OF t SKIP LOCKED`, t.id, t.org, t.record, t.key, t.nonce, t.cipher, t.table, t.key, t.id)
		rows, err := tx.Query(ctx, query, from, limit-total, r.organization)
		if err != nil {
			return 0, err
		}
		type row struct {
			id, org, record string
			e               security.Envelope
		}
		var batch []row
		for rows.Next() {
			var value row
			if err = rows.Scan(&value.id, &value.org, &value.record, &value.e.KeyID, &value.e.Nonce, &value.e.Ciphertext); err != nil {
				rows.Close()
				return 0, err
			}
			batch = append(batch, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		for _, v := range batch {
			if v.org != r.organization {
				return 0, ErrInput
			}
			aad := security.Context{Environment: r.environment, OrganizationID: v.org, RecordID: v.record, Purpose: t.purpose}
			plain, err := r.cipher.Decrypt(v.e, aad)
			if err != nil {
				return 0, security.ErrDecryption
			}
			e, err := r.cipher.Encrypt(plain, aad)
			clear(plain)
			if err != nil {
				return 0, err
			}
			tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE public.%s SET %s=$2,%s=$3,%s=$4 WHERE %s=$1 AND %s=$5`, t.table, t.key, t.nonce, t.cipher, t.id, t.key), v.id, e.KeyID, e.Nonce, e.Ciphertext, from)
			if err != nil || tag.RowsAffected() != 1 {
				if err != nil {
					return 0, err
				}
				return 0, errors.New("rotation row changed")
			}
			total++
		}
	}
	if total > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2::uuid,'SECRET_REENCRYPT','SUCCEEDED',$1::text,jsonb_build_object('schema_version',1,'operator_ref',$3::text,'key_id',$4::text,'previous_key_id',$5::text,'records',$6::integer))`, webapi.ID(), r.organization, r.operator, r.cipher.CurrentKeyID(), from, total)
		if err != nil {
			return 0, err
		}
	}
	return total, tx.Commit(ctx)
}
