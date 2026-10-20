package instances

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type StorageReleaseInput struct {
	ConfirmedName    string `json:"confirmed_name"`
	ConfirmedAssetID string `json:"confirmed_asset_id"`
}
type StorageReleaseAccepted struct {
	RequestID string `json:"request_id"`
	AssetID   string `json:"asset_id"`
	Status    string `json:"status"`
}
type StorageReleaseCommands interface {
	ReleaseStorage(context.Context, identity.Principal, string, StorageReleaseInput, string) (StorageReleaseAccepted, error)
}

func (s *CommandService) ReleaseStorage(ctx context.Context, p identity.Principal, id string, input StorageReleaseInput, trace string) (StorageReleaseAccepted, error) {
	out := StorageReleaseAccepted{}
	if !idPattern.MatchString(id) || input.ConfirmedAssetID != id || !namePattern.MatchString(input.ConfirmedName) {
		return out, invalidQuery()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:release-storage"); err != nil {
		return out, err
	}
	if s.maintenance {
		return out, webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	var instance, name, status, desired, observed string
	var held, busy, binding, reserved bool
	// Same authority -> quota -> instance -> asset lock order as lifecycle commands.
	var org string
	if err = tx.QueryRow(ctx, `SELECT org_id::text FROM quota_policies WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&org); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT i.id::text,i.name,a.status,i.desired_state,i.observed_state,a.legal_hold,
 EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM storage_backups b WHERE b.asset_id=a.id AND b.status IN ('RUNNING','UNCERTAIN')),
 coalesce(a.filesystem_id=a.id::text AND a.init_version>=1,false),
 (SELECT coalesce(sum(q.amount),0)=a.capacity_bytes FROM quota_commitments q WHERE q.org_id=a.org_id AND q.owner_id=a.owner_id AND q.instance_id=i.id AND q.resource_type='STORAGE')
 FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id AND a.instance_id=i.id AND a.org_id=i.org_id AND a.owner_id=i.owner_id
 WHERE a.id=$1 AND a.org_id=$2 AND a.owner_id=$3 FOR UPDATE OF i,a`, id, p.OrganizationID, p.UserID).Scan(&instance, &name, &status, &desired, &observed, &held, &busy, &binding, &reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, queryNotFound()
	}
	if err != nil {
		return out, err
	}
	if name != input.ConfirmedName {
		return out, webapi.Fail(422, "CONFIRMATION_MISMATCH", "工作空间名称不匹配")
	}
	out.AssetID = id
	err = tx.QueryRow(ctx, `SELECT id::text,status FROM storage_release_requests WHERE asset_id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID).Scan(&out.RequestID, &out.Status)
	if err == nil {
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if held {
		return out, webapi.Fail(409, "STORAGE_PROTECTED", "数据受法律保留保护，暂不能释放")
	}
	if status != "RETAINED" || desired != "DELETED" || observed != "DELETED" || busy || !binding || !reserved {
		return out, webapi.Fail(409, "STORAGE_RELEASE_BLOCKED", "仅可释放已删除且无待确认任务的保留数据")
	}
	out.RequestID = webapi.ID()
	out.Status = "ACCEPTED"
	intent, _ := json.Marshal(map[string]any{"schema_version": 1, "action": "OWNER_RELEASE", "request_id": out.RequestID})
	if _, err = tx.Exec(ctx, `INSERT INTO storage_release_requests(id,org_id,owner_id,instance_id,asset_id,confirmed_name,status) VALUES($1,$2,$3,$4,$5,$6,'ACCEPTED')`, out.RequestID, p.OrganizationID, p.UserID, instance, id, name); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='RECLAIM_PENDING',maintenance_intent=$2 WHERE id=$1`, id, intent); err != nil {
		return out, err
	}
	details, _ := json.Marshal(map[string]any{"schema_version": 1, "request_id": out.RequestID, "source_asset_id": id, "reason_code": "OWNER_CONFIRMED_RELEASE"})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,'STORAGE_OWNER_RELEASE','ACCEPTED',$5,$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, trace, details); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (h *CommandHandler) releaseStorage(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	var out StorageReleaseAccepted
	var err error
	service, ok := h.service.(StorageReleaseCommands)
	if r.Method != "POST" || r.URL.RawQuery != "" {
		err = queryNotFound()
	} else if !ok {
		err = webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "释放服务暂不可用")
	} else {
		var input StorageReleaseInput
		err = webapi.Decode(w, r, &input)
		if err == nil {
			out, err = service.ReleaseStorage(r.Context(), p, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/storage-assets/"), ":release"), input, w.Header().Get("X-Request-ID"))
		}
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, http.StatusAccepted, out)
}
