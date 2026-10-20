package devlocal

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// InitResources fills absent local policies without changing saved limits,
// versions or model settings. Runtime defaults require an approved template.
func InitResources(ctx context.Context, pool *pgxpool.Pool, org, operator string, runtime bool) error {
	if operator == "" {
		return errors.New("resource initialization requires an operator")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='ACTIVE' FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&active); err != nil || !active {
		return errors.New("resource initialization requires an active organization")
	}
	quota, err := tx.Exec(ctx, `INSERT INTO quota_policies(org_id) VALUES($1) ON CONFLICT(org_id) DO NOTHING`, org)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT org_id FROM quota_policies WHERE org_id=$1 FOR UPDATE`, org); err != nil {
		return err
	}
	var defaults, users int64
	if runtime {
		result, err := tx.Exec(ctx, `INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,retention_days)
 SELECT $1,1,t.id,t.version,s.id,7 FROM approved_templates t CROSS JOIN approved_specs s
 WHERE t.enabled AND s.enabled AND s.cpu_millicores=1000 AND s.memory_bytes=2147483648 AND s.storage_bytes=10737418240
 ORDER BY t.id,t.version,s.id LIMIT 1 ON CONFLICT(org_id) DO NOTHING`, org)
		if err != nil {
			return err
		}
		defaults = result.RowsAffected()
		var configured bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_defaults WHERE org_id=$1)`, org).Scan(&configured); err != nil {
			return err
		}
		if !configured {
			return errors.New("default resources require an approved 1 CPU / 2 GiB / 10 GiB specification")
		}
		// Assign initial accounts once, when defaults are first created. Users
		// with existing usage keep their inherited policy; never lower a cap
		// beneath a reservation or retained asset during startup.
		if defaults > 0 {
			result, err = tx.Exec(ctx, `INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes)
 SELECT q.org_id,m.user_id,LEAST(q.max_user_instances,q.max_instances),
 CASE WHEN LEAST(q.max_user_instances,q.max_instances)>q.cpu_millicores/s.cpu_millicores THEN q.cpu_millicores ELSE s.cpu_millicores*LEAST(q.max_user_instances,q.max_instances) END,
 CASE WHEN LEAST(q.max_user_instances,q.max_instances)>q.memory_bytes/s.memory_bytes THEN q.memory_bytes ELSE s.memory_bytes*LEAST(q.max_user_instances,q.max_instances) END,
 CASE WHEN LEAST(q.max_user_instances,q.max_instances)>q.storage_bytes/s.storage_bytes THEN q.storage_bytes ELSE s.storage_bytes*LEAST(q.max_user_instances,q.max_instances) END
 FROM quota_policies q JOIN organization_defaults d ON d.org_id=q.org_id JOIN approved_specs s ON s.id=d.spec_id
 JOIN memberships m ON m.org_id=q.org_id JOIN users u ON u.id=m.user_id
 WHERE q.org_id=$1 AND u.status<>'ARCHIVED'
 AND NOT EXISTS(SELECT 1 FROM quota_commitments c WHERE c.org_id=m.org_id AND c.owner_id=m.user_id)
 ON CONFLICT(org_id,owner_id) DO NOTHING`, org)
			if err != nil {
				return err
			}
			users = result.RowsAffected()
		}
	}
	if quota.RowsAffected()+defaults+users > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details)
 VALUES($1,$2,$2,'RESOURCES_INITIALIZE','SUCCEEDED',$3,jsonb_build_object('operator_ref',$4::text,'organization_quota_initialized',$5::boolean,'defaults_initialized',$6::boolean,'user_quotas_initialized',$7::bigint))`, webapi.ID(), org, webapi.ID(), operator, quota.RowsAffected() > 0, defaults > 0, users); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
