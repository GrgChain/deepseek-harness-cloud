# DeepSeek Harness Cloud

DSH Cloud 面向企业内部团队，提供通过浏览器配置、创建和使用 DeepSeek Harness 工作空间的平台。当前范围为单组织、Docker 单机初始版本。

## 当前版本

用户管理、角色及功能权限已提供页面、API和数据库授权实现，真实身份供应及完整撤销验收仍待完成。详见[功能需求](docs/requirements.md)和[测试验收](docs/testing.md)。

外部模型由平台统一管理。用户可在“模型提供商”按模型提供商账号管理多个模型，统一添加、编辑密钥和删除整组连接，选择OpenAI、Claude、Gemini、千问、DeepSeek、Moonshot AI或Z.AI，填写个人Key同步官方列表，多选并限制允许使用的模型，再分别校验；也可添加自定义/本地OpenAI或Anthropic兼容服务。平台数据库加密保存真实Key，Go后端直接调用外部模型API，并分配专用受限运行密钥；创建可填写工作空间名称并选择模型提供商账号，初始使用账号默认模型，DSH内可切换该账号下全部可用模型；已有实例通过“更新模型提供商”选择本人的可用账号，应用其默认模型，不提供组织模型选项。个人模型管理已部署到本机，管理员与成员各自保存/同步、跨Owner拒绝、撤销/归档的真实浏览器验证已通过。真实DeepSeek的schema1直连DSH验证已通过；原生Go网关schema2的DSH默认模型文本闭环已验证，真实Kimi/Z.AI对话仍待验证。

文档版本统一为 **V0.1**。已实现 Go API/Worker/工作空间Gateway/原生模型Gateway、OIDC 用户登录、模型配置和版本控制、配额与持久操作、平台内嵌 Workspace/版本化 Terraform 模板/构建、独立数据卷和 React 控制台。正常运行由平台内嵌 Workspace 模块配合 Terraform 管理本机 Docker。

工作空间内支持启停内置可选插件、安装和卸载第三方 DSH 组合包，以及在“设置 → Agent 预设”保存新任务默认模式。插件、依赖和预设设置随数据盘保留；模型提供商仍由控制台统一管理。当前运行基线、已有实例升级与常见错误见[部署说明](docs/deployment.md#工作空间插件与-agent-预设)，实际验证范围见[专项验收](docs/testing.md#工作空间插件与-agent-预设验收)。

本机已验证管理员配置→成员一键创建→安全进入 DSH→真实模型/工具文件操作→启停保留；加密备份、另盘恢复、审计归档和部分恢复资源管理已有证据。完整恢复授权切换、正式安装/控制面回退以及安全和性能矩阵仍待完成，具体状态统一见[测试验收](docs/testing.md#11-当前验证范围与剩余工作)。

初始版本闭环：管理员准备运行资源 → 用户配置本人模型提供商账号和默认模型 → 成员一键创建 → 平台自动配置启动 → 安全进入 DSH → 真实模型对话与工具文件操作 → 停止后恢复原数据。详细范围以 [功能需求](docs/requirements.md) 为准。

## 文档入口

从 [文档总览](docs/README.md) 查看阅读顺序、文档职责和一致性规则。

| 文档 | 用途 |
| --- | --- |
| [功能需求](docs/requirements.md) | 19 项必需功能、业务规则、验收与实施顺序 |
| [系统设计](docs/design.md) | 架构、状态、异步编排、领域对象和用例接口 |
| [后端技术架构](docs/backend.md) | 技术栈、Go 模块、事务、Worker、运行适配与外部契约 |
| [前端技术架构](docs/frontend.md) | 技术栈、工程分层、页面、缓存、轮询和安全交互 |
| [数据设计](docs/data-model.md) | 配置、实例、操作、配额、持久卷和审计 |
| [接口说明](docs/api.md) / [OpenAPI](api/openapi.yaml) | Cookie/CSRF、请求/响应、分页/错误和访问交接 |
| [安全设计](docs/security.md) | 认证、Owner、凭据、原生访问与容器边界 |
| [测试验收](docs/testing.md) | 97项用例、TDD、需求追踪和真实证据要求 |
| [部署运维](docs/deployment.md) | Docker 部署、配置基线、输入校验、默认数值与备份恢复 |
| [架构决策](docs/decisions/README.md) | 合理性评估、备选方案、约束与验证条件 |

## 实施边界

初始版本：一个组织、可信内部用户、管理员/成员及 Owner、固定 DSH 模板/规格、组织默认及用户个人模型连接、安全原生入口、独立持久卷、启停/软删除、基础配额与审计、运维备份恢复。

不在范围：多组织、集群后端、高可用、自动轮换/休眠、精细计量/计费、共享/批量和Git/MCP等第三方集成。Go原生外部模型接入属于初始版本需求。

## 工程结构

当前工程如下，同一 Go 模块内分进程运行；正式单机安装仍待实现：

```text
apps/                            React / TypeScript 浏览器控制台
services/cmd/api/                Go API / BFF 入口
services/cmd/worker/             Go 异步执行与对账入口
services/cmd/model-gateway/      Go 原生模型入口
services/cmd/gateway/            Go 实例HTTP/WS访问入口
services/cmd/hcctl/              迁移、运行批准/兼容升级及受信备份恢复
services/internal/               领域模块、存储、访问与外部集成
infrastructure/runtime/          锁定 DSH 包、启动适配、私有凭据通道及契约测试
infrastructure/terraform/        锁定 Provider 的 Terraform 模板与本机执行工具
infrastructure/storage/          独立硬容量文件系统执行器及真实契约测试
infrastructure/network/          实例网络围栏执行器及真实契约测试
deploy/local/                    本地 PostgreSQL/Redis Compose
db/schema.sql                    PostgreSQL 物理结构设计基线
db/migrations/                   不可变初始迁移及校验和执行器
apps/tests/                      控制台浏览器测试
```

后端Go + Redis + PostgreSQL；前端React + TypeScript + Vite，配套React Router、TanStack Query和Ant Design。采用面向对象模块设计与TDD；API/受信Worker分进程，Go Gateway提供唯一受控工作台入口。身份由平台Go内置OIDC Provider+BFF实现，原生Cookie由服务端持有。

## 本地开发

需要 Go 1.27.2、Node 24.21.0、npm、Docker Engine 和 Docker Compose。所有主机监听端口大于 10000，默认仅绑定回环地址。

```bash
npm ci --prefix apps
make dev-init
make auth-init
make infra-up
make migrate
make auth-apply
make run-auth
```

资源紧张的本地验收可先执行 `make build`，再用 `make portal-preview` 提供已经构建的控制台；同样保留HTTPS/API/Gateway路由并使用42013端口，避免长期保留开发编译缓存。开发调试继续使用 `make portal`。

在另一终端执行 `make portal`，打开 [控制台登录](https://127.0.0.1:42013/login)。本地自动生成的证书只用于开发；浏览器首次访问 Portal 可能要求确认该证书。API 为 `http://127.0.0.1:18080`；`GET /livez` 检查进程存活，`GET /readyz` 检查迁移版本和实际 PostgreSQL/Redis 连接。创建前检查按真实配置返回，进程就绪不代表实例可用。

本地账户为 `admin@harness.local` 和 `member@harness.local`，随机密码分别保存在 `.local/auth/admin-password` 和 `.local/auth/member-password`，请在本机查看。账号密码由平台Go内置OIDC服务验证；OIDC Token不交给浏览器，Portal使用Secure/HttpOnly Cookie。初始角色来自受控账号导入，后续由管理员管理；组织共享模型只保留受控后端管理接口，不作为控制台创建或更新实例的选项；具有个人模型权限的成员可在“模型提供商”管理自己的外部模型。Portal会话最长8小时，空闲30分钟过期，退出会撤销关联访问会话。

用户进入“模型提供商”添加账号、同步并选择允许使用的模型；保存后后台自动校验。管理员在“平台配置”初始化或修改默认 CPU、内存、存储、每用户实例上限及数据保留期限；存储使用数字输入 1–100 GiB 的整数，默认 10 GiB，运行模板由后台选择，普通成员仅查看。运行默认值不要求先配置组织模型。凭据不会读回，校验可能产生模型服务费用，每次最多 2 个请求、每请求最多 128 输出 tokens，结果 5 分钟有效；首次启动及 Redis 重连后限流保护 120 秒。没有批准模板/规格时不能设置默认项或创建实例。

使用 `make run-worker` 启动已配置的常驻 Worker，监听 18081 并执行持久任务、运行健康探测和安全停止补偿。`make run-gateway` 读取私有访问配置，监听 18082 并转发已授权工作台。PostgreSQL/Redis 主机端口分别为 55432/56379，容器内部端口不对外开放。

个人外部模型还需原生Go模型入口：`make model-gateway-init`生成本机受控配置，执行迁移/最小授权并将生成的入口策略及控制Key文件引用配置到Worker后，在独立终端运行`make run-model-gateway`。入口使用私有网络TLS端口14000，直接接入官方模型API；完整顺序及配置见[原生模型入口部署](docs/deployment.md#88-平台原生模型入口)。

`make dev-init`、`make auth-init`生成随机密码、独立进程DSN、证书和加密主密钥，写入忽略提交的`.local/`私有文件；重复执行拒绝覆盖。数据库迁移只由独立迁移账号执行，应用账号按模块授予受限权限；用户/角色治理由API检查当前管理员资格后执行，Worker、访问Gateway及模型Gateway不能修改身份角色，模板批准使用独立受控命令。`make infra-stop`停止PostgreSQL/Redis并保留数据卷；初始化失败时先诊断，不能删除数据卷作为默认修复方式。

运行契约环境按顺序执行：

```bash
make workspace-init
make runtime-build storage-build network-build
# 将 runtime_image（本机不可变镜像ID）和 runtime_root（0700私有目录）
# 写入 .local/workspace/template-variables.json（0600），再发布固定模板版本。
make workspace-template
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_embedded
make runtime-test storage-test network-test workspace-test
```

这些命令安装固定 Terraform CLI/Provider、初始化平台内嵌 Workspace 模块并发布模板。重复发布相同模板不会创建新版本；发布不等于运行批准。契约测试只清理自己生成的临时资产；测试用 10 GiB 卷与正式 100 GiB 主机容量承诺分开。本地已为 Worker 配置 `/data` 上的独立私有资产目录，通过 100 GiB 承诺及 20% 余量检查；根分区不足的目录仍会被拒绝。本机已完成运行批准和真实依赖检查；资源规格从批准目录选择，运行限额按当前可用内存收紧为1个。

准备当前用户所有、权限 0700 的独立资产目录后，初始化并启动常驻 Worker：

```bash
HC_STORAGE_ROOT=/absolute/private/assets HC_INSTANCE_DOMAIN=harness.test make worker-init
make run-worker
HC_TEST_DB_DSN_FILE="$PWD/.local/migrator-dsn" make worker-runtime-test
```

将资产路径替换为实际目录；初始化检查容量、固定镜像、平台工作空间服务身份和私有密钥文件，生成 `.local/worker-config.json`（0600），重复执行拒绝覆盖。`harness.test` 是本地契约域；正式入口须使用受控 DNS/TLS。Worker 在 `127.0.0.1:18081` 提供健康检查，执行已接受任务不依赖 Redis。真实 Worker 测试使用隔离 `hc_test_worker` 数据库及独有 10 GiB 资产，包含创建、停止、启动、配置重启、删除保卷、响应丢失恢复、超过初始 60 秒有效期的健康刷新、原生 Cookie 续期及失权后的真实 SYSTEM STOP；普通 CI 未提供真实配置时明确跳过该组测试。

运行中的实例约每 30 秒检查应用、原卷、实际配置及原生 HTTP/WS 认证，最多 2 项并发，四维共享 5 秒预算；结果从探测开始起 60 秒失效。刷新按实例版本和运行代次丢弃过期结果，不能重新开启已关闭的路由。每 10 秒分批扫描禁用主体或已撤销凭据，关闭访问并通过独立安全执行槽停止计算；角色变更后仍有本人权限的实例不会因此停止。个人模型新增或轮换密钥后，当前版本在路由同步完成后自动首检；已完成校验且仍被默认配置或未删除实例引用的模型版本每4分钟自动复核，同版本共用45秒租约，每组织每日最多1000个上游请求；每轮最多2次、每次128输出tokens。自动探测可能产生模型服务费用，已启用且校验成功的当前个人模型即使未被引用也自动续检，未引用的失败版本可手动重试；组织共享配置仍需显式首检，四维实例检查本身不调用模型。

工作空间模块位于 `services/internal/workspace`，在 Go 后端内管理 Workspace、模板版本和构建记录，元数据保存在平台 PostgreSQL。源码来源及 AGPL 许可见 [第三方说明](third_party/coder/README.md) 和 [COPYING](COPYING)。既有实例迁移步骤见 [ADR-01](docs/decisions/0001-coder-integration.md)。平台业务库、API 和配置使用 `workspace_*`，计算容器使用 `hc.workspace_owner_id` / `hc.workspace_id`；旧字段不提供兼容别名。schema 10 及模板标签迁移见[部署说明](docs/deployment.md#工作空间业务命名迁移)。

## 验证

Worker 初始化完成且尚无活跃平台实例时，用 `make access-init` 设置同站 HTTPS 入口，再重启 `make run-auth`、`make run-worker`、`make run-gateway` 和 Portal。入口为 `https://portal.harness.localhost:42013`，实例使用独立的 `i-<实例UUID去连字符>.harness.localhost:42013`。`.localhost` 由浏览器解析到回环地址，无需修改系统 hosts。

初始化生成独立站点证书和 `.local/access-config.json`，复用既有身份与私有凭据引用；拒绝覆盖或在活跃实例存在时改变域名。浏览器需信任 `.local/auth/cert.pem` 的本地开发 CA；自动化测试只在隔离环境忽略证书错误。Workspace 授权直接查询平台 PostgreSQL，身份沿用平台 OIDC 映射。

浏览器对控制台的单站证书确认不覆盖新实例域名。若“进入工作空间”遇到站点连接失败，新窗口会保留并打开公开连接确认页；在本机开发环境确认该实例站点的证书后，返回控制台重新进入。信任上述本地CA可避免每个新实例重复确认。确认页不会签发访问授权，入口仍核验原挑战、本人身份和实例状态。API/Gateway首次启动或Redis真实重连后保留120秒恢复保护；限流连接跨正常空闲保持，不因默认30分钟客户端空闲回收额外触发等待。

工作空间的模型由平台注入。远程网页的“设置 → 模型”展示工作空间可用的提供商和模型，只读并提供控制台管理入口；通过当前固定DSH版本的网关兼容适配读取原生会话模型目录，不依赖本地设置。请在[模型提供商](https://portal.harness.localhost:42013/app/models)保存账号并等待自动校验。更改账号默认模型只影响后续创建或显式更新；已有实例需在详情页点击“更新模型提供商”，选择本人可用账号并应用其当前默认模型，运行中会短暂重启并保留原盘。未选择、账号加载失败或默认模型失效时禁止提交，不自动回退。详情展示真实模型名称，实际配置版本可在“实例信息”中查看；保存新Key不会自动切换旧实例。

```bash
make ci
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_d1
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_bootstrap
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_workspace_bootstrap
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_workspace_names
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_identity
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_settings
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_instances
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_worker
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_monitor
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_probe
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_access
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_admission
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_approval
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_operations
docker exec hc-local-postgres-1 createdb -U hc_migrator hc_test_storage_ops
HC_TEST_DB_DSN_FILE=.local/migrator-dsn make test-integration
cd apps
npx playwright install chromium
HC_REAL_IDENTITY=true npm run test:e2e -- --workers=1
```

测试数据库名必须以 `hc_test_` 开头，与 `hc_dev` 分离；仅首次创建测试库，重复测试直接执行集成命令。`make ci` 包含格式、静态检查、Go 竞态测试、前端和运行适配单元测试及构建。浏览器测试默认使用合成 fixture；设置 `HC_REAL_IDENTITY=true` 后额外执行真实内置 OIDC 登录用例，需要 API 已运行。模型协议测试使用合成 HTTP 响应，不能证明真实模型可调用。CI 工作流已加入上述检查，远端运行结果尚未验证。

Portal 开发服务或静态preview已运行时，可用 `HC_TEST_DB_DSN_FILE=.local/migrator-dsn PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/path/to/chromium make gateway-browser-test` 验证实际交接。该测试使用隔离 PG、受限进程角色、内嵌 Workspace/Docker/DSH 和预建 BFF 会话，检查页面交接、原生 WS、复制票据拒绝、Host/Origin/Cookie 隔离及退出后 30 秒内断开，同时验证 Worker 生命周期。真实 OIDC 登录单独测试；此访问测试不调用模型，不代表完整一键创建或模型对话验收。

运行预检用 `HC_TEST_DB_DSN_FILE=.local/migrator-dsn HC_ACCESS_CONFIG_FILE="$PWD/.local/access-config.json" HC_ACCESS_CA_FILE="$PWD/.local/auth/cert.pem" go test -race -count=1 ./services/internal/admission` 验证。它使用隔离 `hc_test_admission` 数据库及受限 API 角色，核验平台 Workspace Owner、Worker 镜像/磁盘容量和实例域 HTTPS；未批准或缺失字段的模板保持拒绝，不调用模型。

真实模型测试必须显式提供本机私有 Key 文件路径（0600），普通 CI 不调用付费模型：

```bash
HC_REAL_MODEL_KEY_FILE=/absolute/private/model-key make runtime-model-test
```

该测试使用真实 DSH Web 默认预设和工具，最多 3 次模型请求、每请求最多 512 输出 tokens，关闭自动重试；验收临时文件写入、工具读回和最终回答，不输出凭据或原始模型响应。测试不是 Portal/Gateway 浏览器闭环验收。

本地已验证 PG17 的迁移/身份会话/模型版本与受限角色、Redis 并发限流与重连保护，以及桌面/手机浏览器34项回归通过，其中6项真实OIDC验证，覆盖登录、访问、配置、操作恢复及移动端交互。DSH 私有通道、自动目录/模型、原生 HTTP/WS 认证与重启、10 GiB 硬容量/删除保卷、网络私网拒绝/公网 TLS，以及平台 Workspace Owner/同名唯一性/启停/删除已验证；真实模型连接通过受限工具调用探测，DSH Web 默认预设也已通过真实模型写文件、工具读回和最终回答测试。实例创建、启停、配置更新和删除接受事务已在隔离PG验证幂等、本人权限、版本冲突、原资产检查、配额竞争和迟写失败回滚；Worker租约及终态事务已验证到期换代、旧写者拒绝、UNCERTAIN保留、原生Cookie加密、停止释放计算/删除保留存储，以及撤销后的已确认安全收尾。常驻 Go Worker 已通过内嵌 Workspace/Docker/PG 的完整生命周期及构建响应丢失恢复：仅提交一次构建，由第二代租约接管，未知期间关闭入口并保留额度。Worker 已验证配置/身份拒绝、Redis 不可用时就绪和正常退出。持续四维探测已验证超过初始60秒有效期后仍保持就绪、原生Cookie加密续期、旧结果丢弃及失权后的真实SYSTEM STOP；PG验证安全补偿幂等/回滚、未核实存储HELD保留、探测预算等待到期拒绝和最后名额竞争。自动模型调度已在隔离PG验证同版本合并、同版本并发租约、重启后间隔、预算、允许列表及撤销/到期/关闭时的结果隔离。Gateway 已通过隔离 PG/Workspace/Docker 中的真实 Portal 页面交接、原生 HTTP/WS、异浏览器票据拒绝、Cookie 隔离与退出限时撤销；Go Worker 完整生命周期测试通过。API 已接入创建/启停/配置/删除和已对账失败重试，运行 manifest 已在本机受控批准。真实 OIDC 管理员默认配置、成员一键创建、固定规格、容量/配额检查、Gateway HTTP/WS 进入及页面停止已通过；普通浏览器内真实模型文件任务、文件重启保留、加密备份/隔离恢复/回收已通过；恢复保留计额已实现；可信检查点接纳/保留转换已通过；未知资源处置、授权切换及完整故障/性能验收仍待完成；当前本机日备份/定时器已演练，状态见 [测试验收](docs/testing.md)。

## 许可证

原有代码许可见 [LICENSE](LICENSE)；含 Coder 衍生代码的组合程序适用 AGPL-3.0，范围和来源见 [COPYING](COPYING) 与[第三方说明](third_party/coder/README.md)。发布网络服务时应提供对应源码及构建安装材料。

本地 `make run-auth`、`make run-worker`、`make run-gateway`、`make run-model-gateway` 启动前会自动执行 `make local-bootstrap`，迁移数据库并补齐缺失的组织配额。组织不存在时导入本地初始账号；已有账号、密码和配置保留。已安装 Worker 的环境还会恢复工作空间身份与模板，按原有流程核验源码、固定镜像和契约证据后批准运行资源，再初始化默认用户资源配置为 1 核、2 GiB 内存、10 GiB 持久存储、每用户 2 个实例、保留 7 天。首次初始化默认配置时，无已有资源占用的账号按默认规格乘以实例上限分配额度。重复启动不覆盖自定义配额、默认配置或已有用户分配。清空数据库但保留 `.local` 后，重新启动服务即可恢复这些基础配置；也可单独执行 `make local-bootstrap`。仅配置登录的环境先初始化组织配额，安装并验证运行环境后再自动补齐运行资源。

运行批准也可单独使用 `make runtime-approve`：先核验受控平台模板三文件源码、Terraform/Provider 锁、镜像中的实际适配文件和已完成契约证据，再在一个事务写入固定模板、规格、收紧的配额和审计。资源批准不要求提供模型 Key；模型真实能力另按用户所选账号校验。默认资源可在“平台配置”修改；版本冲突须重新读取并确认。重复执行相同批准不改额度；存在不同批准、默认项或保留资产时拒绝覆盖。成员页面可以一键创建并进入 DSH。

显式产品验收脚本位于 `infrastructure/runtime/tests/fixtures/`：`product-browser.mjs` 验证一键创建/进入，`product-model-browser.mjs` 验证正常原生页面的模型/文件任务，`product-lifecycle-browser.mjs` 验证启停保留，要求 `HC_PRODUCT_INTEGRATION=true`；首次模型校验另需 `HC_REAL_MODEL_VALIDATION=true`，最多2次请求、每次128输出tokens。脚本仅写本机私有状态证据；创建实例是实际10GiB资产，停止后按正常产品规则保留，不能当作测试垃圾删除。

普通浏览器模型任务需另设`HC_PRODUCT_MODEL_TASK=true`，运行profile默认4096输出tokens、每步最多1次重试；一次浏览器提交可包含多次模型/工具步骤，此默认值不等于任务总费用预算。测试只生成独有的验收文件，不改运行profile，结束后停止指定实例。`HC_PRODUCT_FILE_PROOF_FILE="$PWD/.local/product-model-evidence.json"`传给生命周期脚本可确认该文件重启后校验和保持。兼容运行镜像升级和备份恢复命令见[部署运维](docs/deployment.md#7-运维命令接口)。

生命周期会话回读验收在`HC_PRODUCT_FILE_PROOF_FILE`指定原合成任务证据的基础上设`HC_PRODUCT_HISTORY_PROOF=true`，按原会话ID读取并展开完成的write/read记录，不提交新模型任务；`HC_PRODUCT_LIFECYCLE_EVIDENCE_FILE`指定私有结果文件。

`make test-product-restore`另行验证原完成会话的加密恢复。须显式指定`HC_TEST_DB_DSN_FILE`、`HC_WORKER_CONFIG_FILE`、`HC_PRODUCT_FILE_PROOF_FILE`和`HC_PRODUCT_HISTORY_PROOF_FILE`；源合成任务必须处于STOPPED。测试新建独有10GiB目标盘，保留目标原数据的加密副本，以相同运行镜像和合成模型凭据回读原会话、工具记录及文件，结束只清理生成的测试资产。`HC_NATIVE_RESTORE_EVIDENCE_FILE`可指定0600结果文件，`HC_NODE_EXECUTABLE`与`PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`指定受控Node/Chromium。此验证已在本机通过，不提交新模型任务。

审计归档和法律保留使用`hcctl audit-archive`与`hcctl audit-hold`，默认预览，实际变更要求独立维护账户、维护状态和精确组织确认。90天保留、加密校验、损坏拒绝、末尾审计失败时原行及trigger回滚均已通过真实PG；命令与界限见[部署运维](docs/deployment.md#7-运维命令接口)。当前备份按本机演练推进；本机数据盘和备份目录属于同一故障域。

本机日备份命令为`hcctl backup-daily --directory /absolute/private/repository`，默认预览。全部实例已停止/软删除且无活动任务时，在维护状态下加`--apply`生成并校验加密数据库、所有原盘和备份组索引，保留最近7个成功日期；同日重放复用已有组，失败/损坏/法律保留保护旧组。`make local-backup-timer`安装用户systemd服务和每日北京时间11:00的定时器；本机已启用linger，首个真实备份和服务重放通过。七份保留已通过模拟跨日测试，真实连续7日及RPO/RTO仍须实测；不会自动停止用户实例或改动API/Worker维护配置。

本机兼容回滚命令为`hcctl dev-runtime-rollback --from-template-version CURRENT_VERSION --to-template-version RETAINED_VERSION --reason-code COMPATIBILITY_REHEARSAL`，默认预览；执行要求受控Worker/模板契约文件和协调维护。实际平台模板源码/镜像摘要、运行Worker镜像列表、停止状态及原子审计均受检查。已演练旧版回退与新版恢复、两个方向精确重放、运行Worker不一致拒绝；旧实例成员页面启停/进入通过，新实例在默认值恢复后通过成员API启动、原文件/原完成会话/刷新回读和页面停止。两个原盘、20GiB占用及凭据状态保持；流程仅切换未来创建的运行默认值，见[部署运维](docs/deployment.md#6-维护升级与回滚)。

恢复核对后的保留计额使用`hcctl recovery-retain`，引用精确的隔离库/核对run_id和原因码，默认预览；`--apply`只增加已核实归属资源的额度并原子审计。保留计入普通API的组织/本人配额，原预留账本不改，恢复组织和凭据继续隔离。已在旧备份隔离库把真实占用补记为2实例/20GiB，并验证精确重放、审计失败回滚和配额拒绝；`make test-recovery-ledger`运行相关PG回归。可信检查点接纳/保留转换已实现，未知资源处置及授权切换仍待完成，见[部署运维](docs/deployment.md#7-运维命令接口)。

可信检查点资源接纳已实现：`hcctl recovery-adopt`从另一已认证备份隔离库读取原快照/资产/终态操作/原预留，重新核验内嵌Workspace/镜像/原盘和Owner，再原子转换保留额度；来源和目标继续隔离，原默认项/现有撤销凭据不覆盖。本机已验证两实例/20GiB正常账本、保留0、差异0、精确重放单审计，原服务环境不变。命令及必需输入见[部署运维](docs/deployment.md#7-运维命令接口)；相关PG验证纳入`make test-recovery-ledger`。未知元数据资源的受控处置与独立授权切换仍待完成。

来源授权隔离已实现：`hcctl recovery-fence-source`默认预览，实际变更要求维护窗口和已对账的隔离目标；原子撤销旧来源授权后关闭旧服务连接，中断可按同一意图继续，目标保持隔离。独立PG已验证真实API连接关闭/重连拒绝、事务回滚、清理审计失败后继续和原数据/请求预算保护；本机服务库仅做只读预览。`HC_TEST_DB_DSN_FILE=.local/migrator-dsn HC_TEST_API_DSN_FILE=.local/api-dsn make test-recovery-source-fence`运行该组测试。目标重新授权及服务连接切换仍待完成；当前服务授权保留。另已修复软删除保留盘误计INSTANCE额度，保留盘继续正常计STORAGE。

## 平台内置身份服务

登录和账号密码由平台Go后端与PostgreSQL管理，使用同源OIDC授权码流程；无需独立身份容器。管理员在“用户管理”创建账号、分配角色或重置密码，新账号可直接登录。登录名是创建时的邮箱，编辑联系邮箱不会改变登录名。

本机实例入口开启后，控制台为 [Harness Cloud](https://portal.harness.localhost:42013/)，issuer为该入口的`/oidc`。部署须配置 `HC_OIDC_SIGNING_KEY_FILE` 独立RSA私钥；初始化、账号迁移、协议和验证说明见[部署](docs/deployment.md#内置-oidc-安装与升级)、[安全](docs/security.md#内置-oidc-provider)和[接口](docs/api.md#内置-oidc-协议)。

## 工作空间与模型提供商

在“模型提供商”添加本人账号，同步并选择允许模型、设置账号默认模型；保存后等待后台同步与自动校验。模型提供商账号和真实密钥由平台数据库管理，运行时使用 Go 原生模型网关。

| 操作 | 页面行为 |
| --- | --- |
| 创建工作空间 | 抽屉填写可选空间名称，支持中文；选择 CPU（1/2 核）、内存（2/4 GiB）和本人模型提供商账号，数据盘固定 10 GiB。自动采用账号默认模型，不单独选择具体模型。 |
| 查看工作空间 | 列表显示名称、实际状态、CPU/内存/存储及“提供商 · 模型”，支持搜索、状态筛选、刷新和分页。未筛选的空列表提供“创建第一个实例”描边按钮。 |
| 查看实例详情 | 集中展示进入、启停、更新提供商和删除操作，以及运行资源和真实模型名。“运行检查”“实例信息”按需展开，内部路由标识只在实例信息中显示；不展示“模型配置说明”或“打开模型提供商”按钮。 |
| 更新模型提供商 | 在详情页选择本人可用模型提供商账号，应用其当前默认模型的精确 ID/版本。无有效选择或读取失败时不能提交，不读取组织模型，也不自动回退其他配置。运行中更新会受控重启并保留原盘。 |

预检与创建接受事务绑定所选规格和默认模型版本，并校验用户及组织额度。更改模型提供商账号的默认模型不自动修改已有实例；进入 DSH 后可切换所选账号下全部可用模型。实例列表和详情显示冻结配置对应的真实模型名，不把内部路由别名作为模型名称。

资源配额统一为实例数量、CPU、内存和持久存储四项；实例数量同时决定可运行数量。创建、启动和重试仍校验组织资源池、用户额度及宿主机容量。

操作日志支持可读操作者、对象、结果筛选及变更详情，敏感字段不返回。详细规则见[功能需求](docs/requirements.md#68-用户私有模型与平台统一管理req-17)和[前端架构](docs/frontend.md#实例详情布局)。
