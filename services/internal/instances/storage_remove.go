package instances

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type StorageRemoveCommands interface {
	RemoveStorage(context.Context, identity.Principal, string, string) error
}

func (s *CommandService) RemoveStorage(ctx context.Context, p identity.Principal, id, trace string) error {
	if !idPattern.MatchString(id) {
		return queryNotFound()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:release-storage"); err != nil {
		return err
	}
	if s.maintenance {
		return webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	var status string
	var removed *time.Time
	err = tx.QueryRow(ctx, `SELECT status,owner_removed_at FROM storage_assets WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, id, p.OrganizationID, p.UserID).Scan(&status, &removed)
	if errors.Is(err, pgx.ErrNoRows) {
		return queryNotFound()
	}
	if err != nil {
		return err
	}
	if status != "DELETED" {
		return webapi.Fail(409, "STORAGE_REMOVE_BLOCKED", "仅已回收的数据资产可以删除")
	}
	if removed != nil {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_assets SET owner_removed_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"schema_version": 1, "reason_code": "OWNER_REMOVED_RECLAIMED_ASSET"})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$3,$4,'STORAGE_OWNER_REMOVE','SUCCEEDED',$5,$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, trace, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (h *CommandHandler) removeStorage(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	var err error
	service, ok := h.service.(StorageRemoveCommands)
	if r.URL.RawQuery != "" {
		err = queryNotFound()
	} else if !ok {
		err = webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "删除服务暂不可用")
	} else if err = noBody(r); err == nil {
		err = service.RemoveStorage(r.Context(), p, strings.TrimPrefix(r.URL.Path, "/v1/storage-assets/"), w.Header().Get("X-Request-ID"))
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
