package workspaceextensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/skills"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Handler struct {
	Pool        *pgxpool.Pool
	Cipher      *security.SecretCipher
	Environment string
}
type StatusItem struct {
	Item
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(r.Context(), tx, p, "instances:read"); err != nil {
		identity.WriteError(w, r, err)
		return
	}
	if r.Method != "GET" || r.URL.RawQuery != "" {
		webapi.WriteError(w, r, webapi.Fail(404, "NOT_FOUND", "配置不存在"))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/workspace-extensions/")
	if id == "plugins" {
		webapi.JSON(w, 200, map[string]any{"items": Catalog})
		return
	}
	skillList := strings.HasSuffix(id, "/skills")
	if skillList {
		id = strings.TrimSuffix(id, "/skills")
		if err = identity.RequirePermission(r.Context(), tx, p, "skills:read"); err != nil {
			identity.WriteError(w, r, err)
			return
		}
	}
	if !uuid.MatchString(id) {
		webapi.WriteError(w, r, webapi.Fail(404, "NOT_FOUND", "配置不存在"))
		return
	}
	var state string
	var overdue bool
	err = tx.QueryRow(r.Context(), `SELECT observed_state,updated_at<clock_timestamp()-interval '2 minutes' FROM instances WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID).Scan(&state, &overdue)
	if errors.Is(err, pgx.ErrNoRows) {
		err = webapi.Fail(404, "NOT_FOUND", "配置不存在")
	}
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	if skillList {
		if h.Cipher == nil {
			webapi.WriteError(w, r, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "技能配置暂不可用"))
			return
		}
		selected, e := skills.LoadWorkspaceSkills(r.Context(), tx, h.Cipher, h.Environment, p.OrganizationID, p.UserID, id)
		if e != nil {
			webapi.WriteError(w, r, e)
			return
		}
		defer skills.DestroyWorkspaceSkills(selected)
		items := make([]map[string]any, 0, len(selected))
		for _, item := range selected {
			items = append(items, map[string]any{"id": item.ID, "name": item.Name, "version": item.Version})
		}
		webapi.JSON(w, 200, map[string]any{"items": items})
		return
	}
	snapshot, err := Load(r.Context(), tx, p.OrganizationID, p.UserID, id)
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	var applied bool
	var generation int64
	if len(snapshot.Items) > 0 {
		err = tx.QueryRow(r.Context(), `SELECT COALESCE(applied_at IS NOT NULL AND applied_generation=(SELECT runtime_generation FROM access_routes WHERE instance_id=$1 AND enabled),false),auth_generation FROM workspace_extensions WHERE instance_id=$1`, id).Scan(&applied, &generation)
		if err != nil {
			webapi.WriteError(w, r, err)
			return
		}
	}
	out := []StatusItem{}
	for _, item := range snapshot.Items {
		permission := item.Kind
		if permission == "connector" {
			permission = "connectors"
		}
		if item.Kind != "plugin" && !p.Has(permission+":read") {
			continue
		}
		row := StatusItem{Item: item, Status: "CONFIGURING"}
		if applied {
			row.Status = "AVAILABLE"
		}
		if state == "RUNNING" && !applied && overdue {
			row.Status = "FAILED"
			row.Reason = "运行时尚未确认扩展加载，请进入工作空间检查插件状态，或停止后重新启动"
		}
		if state == "STOPPED" {
			row.Status = "STOPPED"
		}
		if state == "ERROR" {
			row.Status = "FAILED"
			row.Reason = "工作空间配置失败，请查看最近操作并重试"
		}
		if state == "DELETED" || state == "DELETING" {
			row.Status = "UNAVAILABLE"
		}
		if item.Kind != "plugin" {
			valid, e := Current(r.Context(), tx, p, id, generation, item)
			if e != nil {
				webapi.WriteError(w, r, e)
				return
			}
			if !valid {
				row.Status = "AUTH_REQUIRED"
				row.Reason = "连接、版本、范围或授权已变更，请在管理页面检查后重新创建绑定"
			}
		}
		out = append(out, row)
	}
	webapi.JSON(w, 200, map[string]any{"items": out})
}

// Current is used for both display and before every runtime invocation.
func Current(ctx context.Context, tx pgx.Tx, p identity.Principal, instance string, generation int64, item Item) (bool, error) {
	if generation != p.Generation {
		return false, nil
	}
	var valid bool
	permission := item.Kind
	if permission == "connector" {
		permission = "connectors"
	}
	err := tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,$3)`, p.OrganizationID, p.UserID, permission+":read").Scan(&valid)
	if err != nil || !valid {
		return false, err
	}
	if item.Kind == "connector" {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_connectors WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND version=$4 AND catalog_version=$5 AND enabled AND status='READY' AND deleted_at IS NULL) AND public.hc_has_permission($2,$3,'connectors:invoke')`, item.ID, p.OrganizationID, p.UserID, item.Version, item.CatalogVersion).Scan(&valid)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_connections c JOIN context_bindings b ON b.connection_id=c.id WHERE c.id=$1 AND c.org_id=$2 AND c.owner_id=$3 AND c.kind=$4 AND c.version=$5 AND c.enabled AND c.status='READY' AND b.instance_id=$6 AND b.enabled AND b.scopes=$7::jsonb)`, item.ID, p.OrganizationID, p.UserID, item.Kind, item.Version, instance, mustJSON(item.Scopes)).Scan(&valid)
	}
	return valid, err
}

func mustJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
