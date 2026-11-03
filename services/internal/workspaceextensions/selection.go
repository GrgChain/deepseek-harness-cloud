// Package workspaceextensions owns the immutable, non-secret expansion of a
// workspace creation request. External content and credentials stay at source.
package workspaceextensions

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

type Selection struct {
	ID             string   `json:"id"`
	Version        int64    `json:"version"`
	CatalogVersion int64    `json:"catalog_version,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	Permissions    []string `json:"permissions,omitempty"`
	SessionMemory  bool     `json:"session_memory,omitempty"`
	SessionScope   string   `json:"session_scope,omitempty"`
}
type PluginSelection struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type Input struct {
	Connectors []Selection       `json:"connectors,omitempty"`
	Memory     []Selection       `json:"memory,omitempty"`
	Knowledge  []Selection       `json:"knowledge,omitempty"`
	Plugins    []PluginSelection `json:"plugins,omitempty"`
}
type Item struct {
	Kind           string   `json:"kind"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        int64    `json:"version,omitempty"`
	CatalogVersion int64    `json:"catalog_version,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	Permissions    []string `json:"permissions,omitempty"`
	SessionMemory  bool     `json:"session_memory,omitempty"`
	SessionScope   string   `json:"session_scope,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Package        string   `json:"package,omitempty"`
	PackageVersion string   `json:"package_version,omitempty"`
}
type Snapshot struct {
	Items []Item `json:"items"`
}
type Plugin struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

var Catalog = []Plugin{
	{"session-search", "会话搜索", "@deepseek-ai/dsh-experimental-session-search", "0.2.1-alpha.2", true, ""},
	{"terminal-bundle", "持久终端", "@deepseek-ai/dsh-experimental-terminal-bundle", "0.2.1-alpha.2", true, ""},
	{"agent-team-profile", "智能体团队", "@deepseek-ai/dsh-experimental-agent-team-profile", "0.2.1-alpha.2", true, ""},
	{"voice-input-bundle", "语音输入", "@deepseek-ai/dsh-experimental-voice-input-bundle", "0.2.1-alpha.2", false, "需要额外语音服务和模型，请在 DSH 中配置"},
	{"session-titles-bundle", "会话标题", "@deepseek-ai/dsh-experimental-session-titles-bundle", "0.2.1-alpha.2", true, ""},
	{"tool-worktree", "Git Worktree", "@deepseek-ai/dsh-experimental-tool-worktree", "0.2.1-alpha.2", true, ""},
	{"ralph-bundle", "Ralph 循环任务", "@deepseek-ai/dsh-experimental-ralph-bundle", "0.2.1-alpha.2", true, ""},
	{"inspector-profile", "会话检查器", "@deepseek-ai/dsh-experimental-inspector-profile", "0.2.1-alpha.2", true, ""},
	{"badge-skill-bundle", "DSH 徽章技能", "@deepseek-ai/dsh-experimental-badge-skill-bundle", "0.2.1-alpha.2", true, ""},
	{"openviking-memory", "OpenViking 记忆", "@openviking/dsh-memory-plugin", "0.5.31", false, "选择 OpenViking 记忆连接后开启完整会话同步，无需单独添加插件"},
	{"dsh-ssh", "DSH SSH", "@linxin666/dsh-ssh", "0.4.11", false, "要求 DSH >=0.2.1-rc.3，与当前版本不兼容"},
	{"dshmarket", "DSH Market", "dshmarket", "1.71.1", false, "尚未验证当前运行版本兼容性"},
}

func invalid(message string) error { return webapi.Fail(422, "EXTENSION_INVALID", message) }
func (in Input) Empty() bool {
	return len(in.Connectors)+len(in.Memory)+len(in.Knowledge)+len(in.Plugins) == 0
}
func (in Input) Valid() bool {
	if len(in.Memory) > 1 {
		return false
	}
	for _, group := range [][]Selection{in.Connectors, in.Memory, in.Knowledge} {
		if len(group) > 50 {
			return false
		}
		seen := map[string]bool{}
		for _, v := range group {
			if !uuid.MatchString(v.ID) || v.Version < 1 || v.CatalogVersion < 0 || seen[v.ID] || len(v.Scopes) > 2 {
				return false
			}
			seen[v.ID] = true
			scopes := map[string]bool{}
			for _, scope := range v.Scopes {
				if (scope != "personal" && scope != "workspace") || scopes[scope] {
					return false
				}
				scopes[scope] = true
			}
		}
	}
	if len(in.Plugins) > len(Catalog) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range in.Plugins {
		if v.ID == "" || v.Version == "" || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	for _, v := range in.Connectors {
		if v.CatalogVersion < 1 || len(v.Scopes) > 0 || v.Permissions != nil || v.SessionMemory || v.SessionScope != "" {
			return false
		}
	}
	for _, group := range [][]Selection{in.Memory, in.Knowledge} {
		for _, v := range group {
			if v.CatalogVersion != 0 {
				return false
			}
		}
	}
	sessionCount := 0
	for _, v := range in.Memory {
		if v.Permissions != nil {
			return false
		}
		if v.SessionMemory {
			sessionCount++
			if v.SessionScope != "" && v.SessionScope != "personal" && v.SessionScope != "workspace" {
				return false
			}
			if v.SessionScope != "" && len(v.Scopes) > 0 {
				found := false
				for _, scope := range v.Scopes {
					found = found || scope == v.SessionScope
				}
				if !found {
					return false
				}
			}
		} else if v.SessionScope != "" {
			return false
		}
	}
	if sessionCount > 1 {
		return false
	}
	for _, v := range in.Knowledge {
		if v.SessionMemory || v.SessionScope != "" || len(v.Permissions) > 3 || v.Permissions != nil && len(v.Permissions) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, permission := range v.Permissions {
			if permission != "read" && permission != "write" && permission != "delete" || seen[permission] {
				return false
			}
			seen[permission] = true
		}
	}
	return true
}

// KnowledgeAllowed keeps existing snapshots read-only unless explicitly granted.
func (item Item) KnowledgeAllowed(permission string) bool {
	if item.Kind != "knowledge" {
		return false
	}
	if len(item.Permissions) == 0 {
		return permission == "read"
	}
	for _, allowed := range item.Permissions {
		if allowed == permission {
			return true
		}
	}
	return false
}

func Save(ctx context.Context, tx pgx.Tx, p identity.Principal, instance string, in Input) error {
	if in.Empty() {
		return nil
	}
	if !in.Valid() {
		return invalid("扩展配置格式不正确")
	}
	if err := identity.RequirePermission(ctx, tx, p, "instances:configure"); err != nil {
		return err
	}
	snapshot := Snapshot{Items: []Item{}}
	for _, kind := range []string{"connector", "memory", "knowledge"} {
		selected := in.Connectors
		permission := "connectors"
		if kind == "memory" {
			selected = in.Memory
			permission = kind
		}
		if kind == "knowledge" {
			selected = in.Knowledge
			permission = kind
		}
		if len(selected) == 0 {
			continue
		}
		for _, action := range []string{"read", "manage"} {
			if err := identity.RequirePermission(ctx, tx, p, permission+":"+action); err != nil {
				return err
			}
		}
		if kind == "connector" {
			if err := identity.RequirePermission(ctx, tx, p, "connectors:invoke"); err != nil {
				return err
			}
		}
		lock := "context:"
		if kind == "connector" {
			lock = "connector:"
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock+p.OrganizationID+":"+p.UserID); err != nil {
			return err
		}
		for _, selection := range selected {
			item := Item{Kind: kind, ID: selection.ID, Version: selection.Version, CatalogVersion: selection.CatalogVersion}
			var version int64
			var enabled bool
			var status string
			var err error
			if kind == "connector" {
				var catalog int64
				var tools []byte
				err = tx.QueryRow(ctx, `SELECT name,version,catalog_version,enabled,status,tools FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND deleted_at IS NULL FOR SHARE`, p.OrganizationID, p.UserID, selection.ID).Scan(&item.Name, &version, &catalog, &enabled, &status, &tools)
				if err == nil && catalog != selection.CatalogVersion {
					return webapi.Fail(409, "VERSION_CONFLICT", "连接器工具目录已更新，请重新选择")
				}
				var entries []struct {
					Name    string `json:"name"`
					Enabled bool   `json:"enabled"`
				}
				if err == nil {
					err = json.Unmarshal(tools, &entries)
				}
				for _, tool := range entries {
					if tool.Enabled {
						item.Tools = append(item.Tools, tool.Name)
					}
				}
				if err == nil && len(item.Tools) == 0 {
					return invalid("连接器没有已启用的工具")
				}
			} else {
				err = tx.QueryRow(ctx, `SELECT name,version,enabled,status FROM context_connections WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND kind=$4 FOR SHARE`, p.OrganizationID, p.UserID, selection.ID, kind).Scan(&item.Name, &version, &enabled, &status)
				if err == nil && selection.SessionMemory {
					var backend string
					if err = tx.QueryRow(ctx, `SELECT backend FROM context_connections WHERE id=$1`, selection.ID).Scan(&backend); err != nil {
						return err
					}
					if backend != "openviking" {
						return invalid("会话同步需要选择 OpenViking 连接")
					}
					item.SessionMemory, item.SessionScope = true, "personal"
				}
				// Both memory and knowledge have one shared corpus per connection.
				item.Scopes = []string{"personal"}
				if kind == "knowledge" {
					item.Permissions = selection.Permissions
					if len(item.Permissions) == 0 {
						item.Permissions = []string{"read"}
					}
				}
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return webapi.Fail(404, "NOT_FOUND", "所选扩展不存在或无权使用")
			}
			if err != nil {
				return err
			}
			if version != selection.Version {
				return webapi.Fail(409, "VERSION_CONFLICT", "所选扩展已更新，请重新选择")
			}
			if !enabled || status != "READY" {
				return invalid("所选扩展尚未就绪，请重新验证后选择")
			}
			snapshot.Items = append(snapshot.Items, item)
			if kind != "connector" {
				scopes, _ := json.Marshal(item.Scopes)
				if _, err = tx.Exec(ctx, `INSERT INTO context_bindings(org_id,owner_id,instance_id,connection_id,scopes) VALUES($1,$2,$3,$4,$5)`, p.OrganizationID, p.UserID, instance, item.ID, scopes); err != nil {
					return err
				}
			}
		}
	}
	for _, selection := range in.Plugins {
		var found *Plugin
		for i := range Catalog {
			if Catalog[i].ID == selection.ID {
				found = &Catalog[i]
				break
			}
		}
		if found == nil || !found.Available || found.Version != selection.Version {
			return invalid("所选插件版本不可用或不兼容，请重新选择")
		}
		snapshot.Items = append(snapshot.Items, Item{Kind: "plugin", ID: found.ID, Name: found.Name, Package: found.Package, PackageVersion: found.Version})
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,$4,$5)`, instance, p.OrganizationID, p.UserID, p.Generation, raw)
	return err
}

func Load(ctx context.Context, tx pgx.Tx, org, owner, instance string) (Snapshot, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT configuration FROM workspace_extensions WHERE instance_id=$1 AND org_id=$2 AND owner_id=$3`, instance, org, owner).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{Items: []Item{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var out Snapshot
	err = webapi.StrictUnmarshal(raw, &out)
	return out, err
}
