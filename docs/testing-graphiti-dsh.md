# Graphiti DSH 测试用例

<!-- 文档目录：随二级标题更新 -->
<details>
<summary>本页目录</summary>

- [连接填写示例](#连接填写示例)
- [手动验收](#手动验收)
- [自动化命令与覆盖](#自动化命令与覆盖)
- [执行记录](#执行记录)

</details>

适用版本：`@harness-cloud/dsh-graphiti-memory@0.2.0`，DSH `0.2.1-alpha.2`。插件使用平台 Graphiti 连接，由原生 Profile 加载技能和 MCP 工具。

## 连接填写示例

以下示例用于已经启动 Graphiti 配套 Neo4j、并配置好提取模型的本机 Compose 环境。平台接入节点须允许该私网地址，并与 Neo4j 在同一 Docker 网络中。

已有本机接入服务时，先启动配套数据库并等待健康检查通过：

```bash
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile graphiti up -d --no-deps --wait --wait-timeout 60 neo4j
```

若接入服务日志出现 `Name or service not known`，检查 Neo4j 是否启动及共享 Docker 网络；数据库未启动时 `bolt://neo4j:7687` 无法解析。

| 字段            | 测试填写                                                                      |
| --------------- | ----------------------------------------------------------------------------- |
| 名称            | Graphiti 本地测试                                                             |
| 类型            | Graphiti                                                                      |
| Neo4j Bolt 地址 | `bolt://neo4j:7687`                                                           |
| 服务凭据        | `neo4j:<Neo4j 密码>`；本仓库 Compose 的密码来自私有 `.local/context/test-key` |
| 启用连接        | 开启                                                                          |

地址从平台接入服务访问，不使用浏览器或工作空间容器的 `localhost`。服务凭据字段填写实际用户名与密码，不填写文件路径；不将凭据放到聊天、截图或仓库中。远程服务使用其实际 `bolt+s://` 地址。部署前提见 [外接服务部署](../deploy/context/README.md)。

创建测试工作空间时选择此连接，保持“完整会话同步”开启。已有旧 Profile 不会自动迁移，使用新建空间验证。

## 手动验收

准备同一用户的空间 A、B 接入同一连接 G1，空间 C 接入另一连接 G2；另一用户创建连接至同一 Neo4j，供隔离测试。使用合成内容并给每次测试加独立编号，例如 `GRA-20261103-01`。

| 编号   | 操作                                                                           | 预期结果                                                                                                                                               |
| ------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| GRA-01 | 保存并验证 Graphiti 连接；另用错误密码验证一条测试连接                         | 正确连接可用；错误凭据明确失败，不泄露密码                                                                                                             |
| GRA-02 | 创建 A，查看 DSH 插件、技能和工具                                              | 原生插件版本 0.2.0 已启用；模型可用技能 `graphiti-memory`；工具包含 `mcp__graphiti__search` 和 `mcp__graphiti__status`；无重复的 `cloud_memory_*` 工具 |
| GRA-03 | 在 A 发送“项目 GRA-20261103-01 的负责人是林青，每周三 15 点发布。”并让助手回答 | 完成一轮后原文单独暂存，管理页尚无逐条会话记忆；开始压缩或会话释放后提取长期事实，管理页只出现提取结果                                                 |
| GRA-04 | 让 DSH 执行一次简单工具操作，然后完成该轮                                      | 工具的可见结果进入原文暂存，归档时仅提取长期信息；思考、系统提示、技能正文和插件召回不作为用户会话保存                                                 |
| GRA-05 | 在 A 新建会话，检索“GRA-20261103-01 负责人和发布时间”                          | 召回林青、周三 15 点，来源属于 G1；无命中时如实说明                                                                                                    |
| GRA-06 | 在 B 检索同一编号                                                              | 可召回 A 保存的内容，无需复制记忆或配置新用户标识                                                                                                      |
| GRA-07 | 在 C 及另一用户空间检索同一编号                                                | 不返回 G1 的内容；工具不能通过传入用户或图谱分组切换身份                                                                                               |
| GRA-08 | 在 DSH 停用 Graphiti 插件，再重新启用                                          | 停用后技能、MCP 工具及采集钩子移除；启用后恢复；已经保存的内容保留                                                                                     |
| GRA-09 | 已同步后重启空间；模拟保存结果未知后恢复连接并再次同步                         | 插件仍加载；稳定消息标识避免重复暂存；未完成归档可对账恢复，索引失败重用提取回执                                                                       |
| GRA-10 | 在平台解除 A 的记忆绑定，再尝试同步与检索                                      | A 后续请求被拒绝，B 的有效绑定继续可用                                                                                                                 |
| GRA-11 | 在独立测试服务上模拟断开，再恢复连接                                           | 检索失败不阻断普通对话；同步状态显示失败或待同步，不能声称保存成功；恢复后后续检查点可对账                                                             |
| GRA-12 | 从平台记忆管理导出，预览导入到独立测试连接；删除一条已同步记录后重放原消息     | 导出包含当前授权记录和图谱字段；预览说明冲突或语义损失；已删除记录不因同步重放复活                                                                     |

GRA-03～07、GRA-09、GRA-12 的真实图谱提取和语义召回需要已配置的 Graphiti 服务与模型，会使用该模型。自动化的受控存储测试不替代这部分人工验收。

## 自动化命令与覆盖

以下命令从仓库根目录运行。

| 覆盖                                                                | 命令                                                                                                                                                                           | 测试入口                                                                           |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------- |
| 地址校验、会话角色、去重、跨会话召回、失败保留、原生技能与 MCP 挂载 | `make plugin-graphiti-test`                                                                                                                                                    | [插件单元测试](../infrastructure/plugins/graphiti/test/plugin.test.mjs)            |
| MCP 协议、检索、状态、身份覆盖拒绝及取消                            | 同上                                                                                                                                                                           | [MCP 子进程测试](../infrastructure/plugins/graphiti/test/mcp.test.mjs)             |
| 保存结果未知后的稳定重试、事件恢复、每轮召回及受控 Profile 加载     | `HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/runtime/tests/memory-session.test.mjs`                                                                       | [会话与 Profile 测试](../infrastructure/runtime/tests/memory-session.test.mjs)     |
| 实际 DSH 安装、技能可见、MCP 工具启停、安装其他插件及重启持久化     | `HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 HC_RUNTIME_SESSION_MEMORY=true HC_RUNTIME_MEMORY_BACKEND=graphiti node --test infrastructure/runtime/tests/docker-integration.test.mjs` | [DSH Docker 集成测试](../infrastructure/runtime/tests/docker-integration.test.mjs) |
| 下载包与 Worker 嵌入字节一致、离线包可解析                          | `go test ./services/internal/plugins -run TestBundledGraphitiArchiveIsAnInstallableDSHBundle`                                                                                  | [归档测试](../services/internal/plugins/graphiti_bundle_test.go)                   |
| 数据库权限、跨工作空间共享与解绑撤销                                | 配置 `HC_TEST_DB_DSN_FILE` 后执行 `go test -race -count=1 ./services/internal/worker -run 'TestWorkspaceNativeSessionMemoryOptInAndRevocation/graphiti'`                       | [Worker 权限测试](../services/internal/worker/extensions_integration_test.go)      |
| 桌面与手机上的准确版本下载、未知版本保护                            | 在 `apps` 执行 `npx playwright test tests/plugins.spec.ts --grep Graphiti --workers=1`                                                                                         | [页面用例](../apps/tests/plugins.spec.ts)                                          |
| 实际目录、登录、桌面与手机下载字节                                  | 在 `apps` 执行 `HC_REAL_MEMORY_PLUGINS=true npx playwright test tests/plugins-mem0-live.spec.ts --grep Graphiti --workers=1`                                                   | [真实目录用例](../apps/tests/plugins-mem0-live.spec.ts)                            |

Docker 测试使用固定运行镜像和受控代理，不调用对话或图谱模型。数据库测试必须使用独立测试数据库；浏览器测试需本地门户，若未安装 Playwright 自带 Chromium，可设置 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`。

## 执行记录

2026-11-03：插件单元、MCP 子进程、实际 DSH 加载/启停/重启、隔离数据库权限、归档字节一致性及桌面/手机下载验证已通过。会话内容测试使用受控事件与存储；本轮未执行付费模型的自主对话和真实 Graphiti 图谱提取，手动用例中的相关步骤仍需在目标服务验收。

本机手动连接复验：启动缺失的 Neo4j 后，接入服务解析、数据库认证和查询通过；平台已有“Graphiti 记忆”连接重新验证返回 HTTP 200、`ready: true`，状态更新为 `READY`。此验证未调用图谱提取模型。

2026-11-04：修复 Graphiti 导出包包含 `invalid_at: null`、`expired_at: null` 等合法图谱字段时，导入预览返回“请求字段或格式不正确”的问题。仅记忆预览的 `package` 对象允许内部空值，原始 JSON 数值和校验和保持不变；外层空值、重复键、未知身份字段、过深嵌套和非对象包仍拒绝。

Go contextservice/webapi 全部测试及隔离 PostgreSQL 集成测试通过；bridge 139项测试中138项通过、1项按环境跳过，新增图谱空值、长整数和浮点值的导出／预览／确认导入／再导出回归。桌面及手机4项 Graphiti 导入、冲突恢复和大小限制用例通过。更新运行 API 后，真实连接的合成空值图谱包预览从 HTTP 422 恢复为200，图谱字段完整保留；既有空包导出再预览也通过。真实连接仅执行预览，未导入合成记录或调用图谱提取模型。

Graphiti 记忆管理与 Mem0 共用表格、说明、来源标签、筛选、检索及原文展示。桌面与手机8项页面用例通过，确认自动内容只读、手动内容可维护，查看仍通过当前连接 get 接口；截图核对无横向溢出。bridge 140项测试中139项通过、1项按环境跳过，新增三种会话角色与手动记忆的来源识别和筛选回归；隔离 PostgreSQL 集成测试验证 Graphiti 的来源条件正确转发。前端构建通过，运行 API 和 bridge 已更新；真实页面的表头、来源筛选和检索面板检查通过，自动／手动来源请求均返回200。

2026-11-04：0.2.0 的归档提取、回执重试与原文隔离通过 bridge 单测和原生插件测试；固定 DSH 镜像验证加载、启停与重启，桌面和手机验证目录及准确版本下载。真实 Neo4j 与配置的提取模型在独立随机命名空间中归档三种角色消息，生成并召回2条长期记忆，覆盖单实体发布时间和语言偏好；原文重放和重复归档没有增加记录，远端合成记录已清理。
