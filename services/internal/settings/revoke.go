package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Service) Revoke(ctx context.Context, p identity.Principal, version int64, trace string) (Model, error) {
	tx, err := s.begin(ctx, p, "model:revoke", false)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback(context.Background())
	if version < 1 {
		return Model{}, invalid()
	}
	var id, credential string
	err = tx.QueryRow(ctx, `SELECT h.id::text,c.credential_ref::text FROM model_configuration_heads h JOIN model_configurations c ON c.id=h.id WHERE h.org_id=$1 AND c.version=$2 FOR UPDATE OF h`, p.OrganizationID, version).Scan(&id, &credential)
	if errors.Is(err, pgx.ErrNoRows) {
		return Model{}, notFound()
	}
	if err != nil {
		return Model{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL`, credential)
	if err != nil {
		return Model{}, err
	}
	if tag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx, `UPDATE model_configurations SET status='REVOKED',revoked_at=clock_timestamp() WHERE credential_ref=$1 AND revoked_at IS NULL`, credential); err != nil {
			return Model{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE model_validations SET status='UNHEALTHY',reason_code='CREDENTIAL_REVOKED',lease_expires_at=NULL,lease_token=lease_token+1 WHERE config_id=$1 AND config_version IN (SELECT version FROM model_configurations WHERE credential_ref=$2)`, id, credential); err != nil {
			return Model{}, err
		}
		rows, err := tx.Query(ctx, `SELECT i.id::text,i.version,i.observed_state FROM instances i JOIN model_configurations c ON c.id=i.model_config_id AND c.version=i.model_config_version WHERE i.org_id=$1 AND c.credential_ref=$2 AND i.deleted_at IS NULL ORDER BY i.id FOR UPDATE OF i`, p.OrganizationID, credential)
		if err != nil {
			return Model{}, err
		}
		type affected struct {
			id      string
			version int64
			state   string
		}
		affectedInstances := []affected{}
		for rows.Next() {
			var i affected
			if err := rows.Scan(&i.id, &i.version, &i.state); err != nil {
				rows.Close()
				return Model{}, err
			}
			affectedInstances = append(affectedInstances, i)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Model{}, err
		}
		for _, i := range affectedInstances {
			if _, err := tx.Exec(ctx, `UPDATE instances SET access_generation=access_generation+1,version=version+1,desired_state=CASE WHEN desired_state='DELETED' THEN 'DELETED' ELSE 'STOPPED' END,updated_at=clock_timestamp() WHERE id=$1`, i.id); err != nil {
				return Model{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, i.id); err != nil {
				return Model{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE instance_id=$1 AND revoked_at IS NULL`, i.id); err != nil {
				return Model{}, err
			}
			if i.state != "STOPPED" && i.state != "DELETED" {
				intent, _ := json.Marshal(map[string]any{"schema_version": 1, "expected_instance_version": i.version + 1, "target_desired_state": "STOPPED", "reason": "CREDENTIAL_REVOKED", "request_id": trace})
				op := webapi.ID()
				tag, err := tx.Exec(ctx, `INSERT INTO operations(id,instance_id,actor_kind,type,status,stage,intent,trace_id) SELECT $1,$2,'SYSTEM','STOP','ACCEPTED','QUEUED',$3,$4 WHERE NOT EXISTS(SELECT 1 FROM operations WHERE instance_id=$2 AND status IN ('ACCEPTED','RUNNING','UNCERTAIN'))`, op, i.id, intent, trace)
				if err != nil {
					return Model{}, err
				}
				if tag.RowsAffected() > 0 {
					if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), op); err != nil {
						return Model{}, err
					}
				}
			}
		}
		if err := s.audit(ctx, tx, p, id, "MODEL_REVOKE", trace); err != nil {
			return Model{}, err
		}
	}
	m, err := readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return Model{}, err
	}
	return m, tx.Commit(ctx)
}
