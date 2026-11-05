package identity

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

type Permission struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Assignable   bool     `json:"assignable"`
	Dependencies []string `json:"dependencies"`
}

var permissionCatalog = []Permission{
	{"connectors:read", "查看本人连接器", "查看MCP连接、工具目录及脱敏调用记录。", true, []string{}},
	{"connectors:manage", "管理本人连接器", "配置、授权、验证连接与选择工具；接入工作空间另需工作空间配置和工具调用权限。", true, []string{"connectors:read"}},
	{"connectors:invoke", "调用本人连接器工具", "手动调用已授权MCP工具，或由已接入的工作空间调用。", true, []string{"connectors:read"}},
	{"users:read", "查看用户与资源配额", "查看组织用户及其资源额度和占用。", true, []string{}},
	{"users:manage", "管理用户与资源配额", "管理账号与用户资源额度，可分配包括管理员在内的角色。", true, []string{"users:read"}},
	{"roles:read", "查看角色与权限目录", "查看角色拥有的权限及各权限的用途和依赖。", true, []string{}},
	{"roles:manage", "管理角色与权限", "新增、编辑和删除自定义角色，可配置全部可分配权限；内置角色受保护。", true, []string{"roles:read"}},
	{"instances:read", "查看本人工作空间与数据", "查看工作空间、数据资产和插件目录，浏览、预览及下载本人文件。", true, []string{}},
	{"plugins:manage", "管理本人插件目录", "在线或离线添加插件，编辑默认及本人条目的目录资料；创建时选择插件另需工作空间配置权限。", true, []string{"instances:read"}},
	{"skills:read", "查看本人技能包", "查看技能正文与资源清单，下载单项或导出完整技能包。", true, []string{}},
	{"skills:manage", "管理本人技能包", "创建、编辑、删除、导入及手动同步技能包，保留正文与配套资源。", true, []string{"skills:read"}},
	{"memory:read", "查看本人记忆", "查看连接、自动和手动记忆，检索或导出完整记忆；接入同一连接的工作空间共用内容。", true, []string{}},
	{"memory:manage", "管理本人记忆", "配置连接、维护手动记忆及导入迁移包；接入工作空间另需工作空间配置权限。", true, []string{"memory:read"}},
	{"knowledge:read", "查看本人知识库", "查看连接、文档和索引状态，检索及查看原文引用；接入同一连接的工作空间共用内容。", true, []string{}},
	{"knowledge:manage", "管理本人知识库", "配置连接、导入或删除文档及重建索引；接入工作空间另需工作空间配置权限。", true, []string{"knowledge:read"}},
	{"instances:create", "创建本人工作空间", "按所选规格和模型创建；运行还需启动权限。", true, []string{"instances:read"}},
	{"instances:start", "启动本人工作空间", "启动及维持运行；停止后保留数据。", true, []string{"instances:read"}},
	{"instances:stop", "停止本人工作空间", "释放计算资源，保留数据及存储占用。", true, []string{"instances:read"}},
	{"instances:release-storage", "永久释放本人存储", "仅限已删除工作空间的数据，确认后永久删除，无法恢复。", true, []string{"instances:read"}},
	{"instances:delete", "删除本人工作空间", "删除计算资源，数据按保留策略继续保存。", true, []string{"instances:read"}},
	{"instances:configure", "更新本人工作空间配置", "更新模型或选择连接器、记忆、知识库和插件；使用连接还需对应功能权限。", true, []string{"instances:read"}},
	{"instances:access", "进入本人工作空间", "使用 DSH 文件、会话、插件和 Agent 预设；仅限本人实例。", true, []string{"instances:read"}},
	{"instances:retry", "重试本人工作空间操作", "重试失败操作，仍须具备对应动作权限。", true, []string{"instances:read"}},
	{"configuration:read", "查看平台配置与资源上限", "查看默认资源、系统额度、批准模板及脱敏预检。", true, []string{}},
	{"personal-model:read", "查看本人模型提供商", "查看本人账号、可用模型及账号默认模型。", true, []string{}},
	{"personal-model:write", "管理本人模型提供商", "新增、编辑和启停本人账号与模型，设置账号默认模型。", true, []string{"personal-model:read"}},
	{"personal-model:validate", "校验本人模型", "执行有限真实调用，可能产生模型费用。", true, []string{"personal-model:read"}},
	{"personal-model:revoke", "删除本人模型提供商", "删除本人账号或模型，撤销关联运行凭据。", true, []string{"personal-model:read"}},
	{"models:catalog:manage", "管理批准的模型目录", "维护组织可用的提供商和模型，不授予用户内容访问权。", true, []string{"configuration:read"}},
	{"model:write", "保存组织模型配置", "保存组织共享模型及凭据，不授予明文密钥读取权。", true, []string{"configuration:read"}},
	{"model:validate", "校验组织模型与目录", "刷新组织模型目录并执行有限真实校验，可能产生费用。", true, []string{"configuration:read"}},
	{"model:revoke", "撤销组织模型凭据", "停用关联模型版本及运行授权，不删除工作空间数据。", true, []string{"configuration:read"}},
	{"defaults:write", "管理平台默认配置与资源上限", "修改默认资源、实例上限、数据保留期及系统资源总额。", true, []string{"configuration:read"}},
	{"audit:read", "查看本人操作日志", "查看本人动作及本人资源的脱敏操作记录。", true, []string{}},
	{"audit:organization", "查看组织操作日志", "查看组织范围的脱敏记录，不包含用户文件和工作正文。", true, []string{"audit:read"}},
}

// Has is a presentation hint from the authenticated database projection.
// Application writes must also call RequirePermission inside their transaction.
func (p Principal) Has(permission string) bool { return slices.Contains(p.Permissions, permission) }

func RequirePermission(ctx context.Context, tx pgx.Tx, p Principal, permission string) error {
	if _, err := LockPrincipal(ctx, tx, p); err != nil {
		return err
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,$3)`, p.OrganizationID, p.UserID, permission).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
