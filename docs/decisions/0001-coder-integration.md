# ADR-01 在平台 Go 后端内嵌 Coder 工作空间代码

- 版本：V0.1
- 范围：单组织 Docker 初始版本
- 状态：已选择；实现与验证范围见下文
- 关联需求：REQ-01、REQ-02、REQ-04、REQ-08、REQ-11

## 1 决策

从固定 Coder v2.37.4 源码抽取并改编 Workspace、版本化 Terraform 模板、构建执行记录及状态转换代码，放入平台 `internal/workspace` 模块。运行时不启动独立 Coder 服务，不使用 Coder 官方镜像，也不将完整 Coder Server 改名包装后运行。

API、Worker 和 Gateway 通过 Go 方法查询同一平台 PostgreSQL 中的 Workspace/Build；Terraform 执行只在受信 Worker 内。模板版本与源码不可覆盖，实例明确绑定版本；每个 Workspace 的构建序号从 1 连续递增，仅允许一个活动构建。创建操作和构建记录都有稳定 ID，未知结果继续按已有操作/构建对账。

## 2 复用和自有边界

| 能力 | 实现与职责 |
| --- | --- |
| Workspace/TemplateVersion/Build、状态转换 | 抽取 Coder SDK 子集并适配 Go 内部模块 |
| 工作空间与构建持久化 | 改编 Coder 数据实体/查询规则，使用平台 pgx 仓储 |
| Terraform 执行 | 改编 Coder init/plan/apply/state 与 Provider 元数据协议，由 Worker 调用受控 CLI |
| 用户登录、组织、授权、配额、Outbox | 平台实现，平台 membership 为唯一授权事实 |
| DSH 镜像、配置、数据盘、网络、原生访问 | 项目独立构建及受信 Worker/Gateway 实现 |

本决策的源码版本见第1节，复用和修改范围如上表；不引入 `enterprise/` 下的代码。来源清单、文件摘要及许可证原文属于对应版本的发布材料，不在本页另列已移除的本地目录入口。

## 3 数据与执行约束

数据库文件独立放在 `db/`。新增 `workspace_authority`、`workspace_templates`、`workspace_template_versions`、`workspace_workspaces` 和 `workspace_builds`，与业务操作共用备份。业务身份使用 `workspace_owner_id`、`workspace_name`、`workspace_id`，计算容器使用 `hc.workspace_owner_id`、`hc.workspace_id`；已有身份 ID 保持不变。

运维身份初始化编排身份、发布模板版本及执行受控备份恢复；Worker 可写 Workspace/Build 和读取私有 Terraform state；API/Gateway 只读归属、版本与参数，不能读取 provisioner_state 或修改模板。Terraform 使用固定 CLI 摘要、Provider lock、本机 Provider 镜像目录、0700 工作目录及环境白名单，不继承平台数据库、OIDC 或模型密钥。Terraform 只创建停止的计算容器及网络；Worker 核验数据盘与网络围栏后才启动。

## 4 初始化与数据维护

`NewEmbedded` 直接访问平台 PostgreSQL；Workspace 客户端不提供外部 HTTP 后端或旧服务导入入口。首次部署执行数据库迁移与角色授权，再通过 `workspace-init` 初始化编排身份、`workspace-template` 发布不可变模板。模板经过独立运行批准后才能用于创建实例。

维护或恢复前暂停生命周期写入，等待活动操作及构建终态，保存加密数据库备份、受控配置及工作盘备份。Workspace 元数据与平台事实成套恢复，保持 Owner、Workspace、TemplateVersion、Build、Job ID 和 Terraform state 的对应关系；恢复后先核验资源与归属，再开放路由及生命周期操作。业务命名迁移使用第 7 节的受控命令，已应用数据库迁移保持校验和。

## 5 验证

内嵌模块使用隔离 PostgreSQL 测试不可变版本、稳定名称冲突、连续构建号、并发排他、固定参数、禁用 Owner 和历史保留。真实 Terraform 验证 init/plan/apply/state/delete；真实 DSH 验证创建、停止、重新启动、删除保盘与原生访问。已有故障/配额/租约/恢复测试继续适用，旧独立 Coder 验证结果不能替代新模块验证。

## 6 许可

Coder 开源部分采用 AGPL-3.0。保留原许可证、版权与修改声明，并在网络服务正式发布时提供相应源码、来源清单、许可证原文与构建安装脚本。

## 7 业务命名与上游标识

业务架构、服务接口和错误说明使用 Workspace/TemplateVersion/Build。源码复用不要求删除全部 Coder 字样：

| 引用 | 处理规则 |
| --- | --- |
| 独立服务地址、管理登录、用户创建和构建 HTTP 访问 | 不支持；Workspace 客户端直接访问平台 PostgreSQL |
| Go 业务接口 | 使用 WorkspaceControl、WorkspaceReader、WorkspaceOwnership；包路径为 internal/workspace，跨包引用使用 workspaceengine，内部变量使用 Workspace 语义 |
| 数据库/API/配置字段、错误码、容器标签 | 使用 workspace_owner_id、workspace_name、workspace_id、workspace_engine、workspace_organization_id、workspace_source_version、workspace_provider_version、WORKSPACE_UNAVAILABLE 及 hc.workspace_owner_id / hc.workspace_id；不提供旧字段别名 |
| Terraform Provider 与数据源 | 保留 coder/coder、coder_workspace、coder_parameter、CODER_* 元数据协议和 Provider lock；随意改名会破坏固定模板执行 |
| 上游来源及许可 | 保留 Coder 名称、版本、URL、版权、AGPL 许可证和文件摘要 |
| 数据与资源迁移 | 已应用迁移 SQL 保持校验和；转换代码和拒绝旧字段的测试需要识别旧名称，不作为当前业务别名 |
| 浏览器安全拦截 | 保留拦截 coder_session_token / Coder-Session-Token 的规则，防止旧服务凭据进入实例访问链路 |

配置中 workspace_engine 仅允许 embedded；删除服务 Token 与外部 HTTP 开关。schema 10 在原库改名并保持实体 ID、外键和权限；已执行的旧迁移文件保留校验和。容器标签迁移要求全部实例停止、操作及构建终态、路由关闭、计算额度释放。受控命令 workspace-names-migrate 发布新不可变模板版本，原子迁移实例和组织默认绑定、增加实例版本及访问代次并审计；原构建、模板字节和操作日志保持完整。重复执行不重复生成版本。随后使用新模板启动计算容器并复用原数据盘。

Worker 读取新旧已批准版本，只允许最后一次成功停止构建跨越仅修改标签的版本迁移。核验同一 Workspace、Owner、组织、模板、构建发起者、固定参数和最后构建 ID；模板锁、变量及运行清单必须完全一致。新构建仍必须使用当前冻结版本，其他变更、运行中构建和身份冲突均拒绝。安全收尾也使用新模板生成停止构建，不把历史停止记录改写为新版本。
