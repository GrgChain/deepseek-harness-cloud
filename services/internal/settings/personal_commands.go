package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type PersonalDefault struct {
	Version           int64   `json:"version"`
	ConnectionID      *string `json:"connection_id"`
	ConnectionVersion *int64  `json:"connection_version"`
}

func (s *Service) GetPersonalDefault(ctx context.Context, p identity.Principal) (*PersonalDefault, error) {
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var value PersonalDefault
	err = tx.QueryRow(ctx, `SELECT version,connection_id::text,connection_version FROM user_model_defaults WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID).Scan(&value.Version, &value.ConnectionID, &value.ConnectionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &value, err
}
func (s *Service) SetPersonalDefault(ctx context.Context, p identity.Principal, id string, version, expected int64, trace string) (*PersonalDefault, error) {
	if !uuid.MatchString(id) || version < 1 || expected < 0 {
		return nil, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "personal-model:"+p.OrganizationID+":"+p.UserID); err != nil {
		return nil, err
	}
	item, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	if item.Version != version {
		return nil, versionConflict()
	}
	if !item.Enabled || item.SyncStatus != "SYNCED" || item.GatewayCheck.Status != "HEALTHY" || item.Validation.Status != "HEALTHY" {
		return nil, invalid()
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT version FROM user_model_defaults WHERE org_id=$1 AND owner_id=$2 FOR UPDATE`, p.OrganizationID, p.UserID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if current != expected {
		return nil, versionConflict()
	}
	value := &PersonalDefault{current + 1, &id, &version}
	_, err = tx.Exec(ctx, `INSERT INTO user_model_defaults(org_id,owner_id,connection_id,connection_version,version) VALUES($1,$2,$3,$4,$5) ON CONFLICT(org_id,owner_id) DO UPDATE SET connection_id=EXCLUDED.connection_id,connection_version=EXCLUDED.connection_version,version=EXCLUDED.version,updated_at=clock_timestamp()`, p.OrganizationID, p.UserID, id, version, value.Version)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE personal_provider_accounts g SET default_model_id=$1,version=version+1,updated_at=clock_timestamp() FROM user_model_connections h WHERE h.id=$1 AND h.provider_account_id=g.id AND g.org_id=$2 AND g.owner_id=$3 AND g.status='ACTIVE'`, id, p.OrganizationID, p.UserID); err != nil {
		return nil, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_DEFAULT_SET", trace); err != nil {
		return nil, err
	}
	return value, tx.Commit(ctx)
}

func (s *Service) RevokePersonalModel(ctx context.Context, p identity.Principal, id string, version int64, trace string) (PersonalModel, error) {
	if !uuid.MatchString(id) || version < 1 {
		return PersonalModel{}, notFound()
	}
	tx, err := s.begin(ctx, p, "personal-model:revoke", false)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	groupID, err := lockPersonalProviderAccount(ctx, tx, p, id)
	if err != nil {
		return PersonalModel{}, err
	}
	item, err := s.revokePersonalModelTx(ctx, tx, p, id, version, trace)
	if err != nil {
		return item, err
	}
	if err = touchPersonalProviderAccount(ctx, tx, groupID, nil); err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}
func (s *Service) revokePersonalModelTx(ctx context.Context, tx pgx.Tx, p identity.Principal, id string, version int64, trace string) (PersonalModel, error) {
	var err error
	var ignored *string
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&ignored); err != nil {
		return PersonalModel{}, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT current_version FROM user_model_connections WHERE org_id=$1 AND owner_id=$2 AND id=$3 FOR UPDATE`, p.OrganizationID, p.UserID, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, notFound()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if version != current {
		return PersonalModel{}, versionConflict()
	}
	statements := []string{
		`UPDATE personal_provider_accounts SET default_model_id=NULL WHERE default_model_id=$1`,
		`UPDATE user_model_connections SET enabled=false,updated_at=clock_timestamp() WHERE id=$1`,
		`UPDATE external_provider_secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE connection_id=$1`,
		`UPDATE secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id IN (SELECT runtime_credential_ref FROM user_model_connection_versions WHERE connection_id=$1)`,
		`UPDATE model_configurations SET status='REVOKED',revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1`,
		`UPDATE model_validations SET status='UNHEALTHY',reason_code='CREDENTIAL_REVOKED',lease_expires_at=NULL,lease_token=lease_token+1 WHERE config_id=$1`,
		`UPDATE instances SET desired_state=CASE WHEN desired_state='DELETED' THEN desired_state ELSE 'STOPPED' END,access_generation=access_generation+1,version=version+1,updated_at=clock_timestamp() WHERE model_config_id=$1 AND deleted_at IS NULL`,
		`UPDATE access_routes SET enabled=false WHERE instance_id IN (SELECT id FROM instances WHERE model_config_id=$1)`,
		`UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND instance_id IN (SELECT id FROM instances WHERE model_config_id=$1)`,
		`UPDATE model_sync_operations SET status='FAILED',reason_code='CREDENTIAL_REVOKED',lease_token=lease_token+1 WHERE connection_id=$1 AND action='UPSERT' AND status IN ('PENDING','UNCERTAIN')`,
		`UPDATE user_model_connection_versions SET sync_status='REVOKING' WHERE connection_id=$1 AND sync_status<>'REVOKED'`,
		`INSERT INTO model_sync_operations(id,org_id,owner_id,connection_id,connection_version,action,remote_model_id) SELECT gen_random_uuid(),org_id,owner_id,connection_id,connection_version,'REVOKE',remote_model_id FROM model_sync_operations WHERE connection_id=$1 AND action='UPSERT' ON CONFLICT(connection_id,connection_version,action) DO NOTHING`,
	}
	for _, sql := range statements {
		if _, err = tx.Exec(ctx, sql, id); err != nil {
			return PersonalModel{}, err
		}
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_REVOKED", trace); err != nil {
		return PersonalModel{}, err
	}
	item, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return item, err
	}
	return item, nil
}

func (s *Service) ArchivePersonalModel(ctx context.Context, p identity.Principal, id string, version int64, trace string) (PersonalModel, error) {
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	if !uuid.MatchString(id) {
		return PersonalModel{}, notFound()
	}
	groupID, err := lockPersonalProviderAccount(ctx, tx, p, id)
	if err != nil {
		return PersonalModel{}, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT current_version FROM user_model_connections WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, id, p.OrganizationID, p.UserID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, notFound()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if current != version {
		return PersonalModel{}, versionConflict()
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT NOT h.enabled AND NOT EXISTS(SELECT 1 FROM user_model_connection_versions WHERE connection_id=h.id AND sync_status<>'REVOKED') AND NOT EXISTS(SELECT 1 FROM instances WHERE model_config_id=h.id AND deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM user_model_defaults WHERE connection_id=h.id) FROM user_model_connections h WHERE id=$1`, id).Scan(&allowed)
	if err != nil {
		return PersonalModel{}, err
	}
	if !allowed {
		return PersonalModel{}, webapi.Fail(409, "STATE_CONFLICT", "请先撤销连接，并解除实例和个人默认引用")
	}
	if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET archived_at=COALESCE(archived_at,clock_timestamp()) WHERE id=$1`, id); err != nil {
		return PersonalModel{}, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_ARCHIVED", trace); err != nil {
		return PersonalModel{}, err
	}
	item, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return item, err
	}
	if err = touchPersonalProviderAccount(ctx, tx, groupID, nil); err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}

func (s *Service) ClearPersonalDefault(ctx context.Context, p identity.Principal, expected int64, trace string) (*PersonalDefault, error) {
	if expected < 1 {
		return nil, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var value PersonalDefault
	var id *string
	if err = tx.QueryRow(ctx, `SELECT version,connection_id::text FROM user_model_defaults WHERE org_id=$1 AND owner_id=$2 FOR UPDATE`, p.OrganizationID, p.UserID).Scan(&value.Version, &id); err != nil {
		return nil, versionConflict()
	}
	if value.Version != expected {
		return nil, versionConflict()
	}
	value.Version++
	if _, err = tx.Exec(ctx, `UPDATE user_model_defaults SET version=$3,connection_id=NULL,connection_version=NULL,updated_at=clock_timestamp() WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID, value.Version); err != nil {
		return nil, err
	}
	resource := p.UserID
	if id != nil {
		resource = *id
	}
	if err = s.audit(ctx, tx, p, resource, "PERSONAL_MODEL_DEFAULT_CLEARED", trace); err != nil {
		return nil, err
	}
	return &value, tx.Commit(ctx)
}
