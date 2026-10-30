# DSH Cloud 接口设计

版本：V0.1。公开契约以 [OpenAPI 3.0.3](../api/openapi.yaml) 为准；本文说明鉴权、事务、错误和开发约束。REQ-01～19 的范围见[功能需求](requirements.md)，数据库字段见[数据设计](data-model.md)。

## 1 公共协议与认证

平台业务接口为同源 `/v1/*` JSON。浏览器使用 `__Host-hc_session` Secure/HttpOnly Cookie，不使用自报用户/组织/Owner头或OIDC Bearer作为管理凭据。`GET /v1/me` 返回固定组织、角色、有效权限、auth_generation、内存CSRF及期限；写操作必须携带精确Portal Origin、X-CSRF-Token和Cookie。响应按需携带X-Request-ID，禁止缓存身份、配置秘密及临时协议材料。

`/auth/login` 建立一次state/nonce/PKCE事务并302到内置OIDC；callback只接受state和code/error二者之一，校验浏览器绑定、一次消费事务、签名与当前授权。return_to仅允许 `/app/` 相对路径白名单。`POST /auth/logout` 需要相同CSRF防护，先撤销Portal/Gateway会话再清Cookie。未配置认证或数据库失效时拒绝认证，不能回退匿名管理。

访问工作空间使用固定实例域和一次交接票据；访问入口仍要求当前Owner及权限，不向浏览器发送DSH原生Token/Cookie。HTTP/WebSocket协议见[安全设计](security.md)，模型Messages协议和Terraform执行是后台适配契约，不作为管理API旁路。

## 2 接口目录

下表由当前OpenAPI操作登记整理；参数、required/enum、请求体、逐状态响应与权限以同一操作定义为准。

| 编号 | 方法与路径 | 契约职责 |
|---|---|---|
| API-001 | `GET /v1/me` | Get current user and organization memberships |
| API-003 | `GET /v1/templates` | Get the approved fixed template and version |
| API-004 | `GET /v1/instances` | List authorized instances |
| API-005 | `POST /v1/instances` | Create an instance asynchronously and idempotently |
| API-006 | `GET /v1/instances/{id}` | Get instance state, resources and access health |
| API-007 | `POST /v1/instances/{id}:start` | Start an instance using its original verified storage |
| API-008 | `POST /v1/instances/{id}:stop` | Stop compute while preserving storage |
| API-009 | `DELETE /v1/instances/{id}` | Soft-delete instance and revoke compute access |
| API-010 | `GET /v1/operations/{id}` | Get operation status and error |
| API-011 | `POST /v1/instances/{id}:access` | Authorize a controlled native-auth session handoff |
| API-012 | `GET /v1/audit-events` | Query audit events within authorized organization scope |
| API-013 | `GET /v1/preflight` | Check initial release dependencies and defaults |
| API-014 | `GET /v1/model-configuration` | Get redacted organization model configuration |
| API-015 | `PUT /v1/model-configuration` | Save a versioned model configuration |
| API-016 | `POST /v1/model-configuration:validate` | Validate a model using a bounded real request |
| API-017 | `POST /v1/instances/{id}:configure` | Apply an explicitly selected validated model configuration |
| API-018 | `POST /v1/operations/{id}:retry` | Retry only a reconciled, safely retryable failed operation |
| API-019 | `GET /v1/defaults` | Get approved fixed organization defaults |
| API-020 | `PUT /v1/defaults` | Update defaults for future instances |
| API-021 | `POST /v1/model-configuration:revoke` | Revoke use of a model configuration version |
| API-022 | `GET /auth/login` | login |
| API-023 | `GET /auth/callback` | Require state and exactly one of code or error. Validate browser binding before handling provider denial. Never log code/state; denial returns a safe au… |
| API-024 | `POST /auth/logout` | logout |
| API-025 | `GET /__dsh/challenge` | createBrowserChallenge |
| API-026 | `POST /__dsh/handoff` | consumeHandoff |
| API-027 | `GET /__dsh/connect` | confirmWorkspaceConnection |
| API-028 | `GET /v1/users` | 查询组织用户 |
| API-029 | `POST /v1/users` | 创建本地账号或绑定已有组织身份 |
| API-030 | `GET /v1/users/{id}` | 查询用户及身份供应结果 |
| API-031 | `PUT /v1/users/{id}` | 编辑联系资料 |
| API-077 | `DELETE /v1/users/{id}` | 确认用户名后软删除用户并撤销登录 |
| API-032 | `POST /v1/users/{id}:role` | 用户治理动作：role |
| API-033 | `POST /v1/users/{id}:disable` | 用户治理动作：disable |
| API-034 | `POST /v1/users/{id}:enable` | 用户治理动作：enable |
| API-035 | `POST /v1/users/{id}:archive` | 用户治理动作：archive |
| API-036 | `POST /v1/users/{id}:retry-identity` | 用户治理动作：retry-identity |
| API-037 | `POST /v1/users/{id}:reset-password` | 用户治理动作：reset-password |
| API-038 | `GET /v1/roles` | 查询内置及自定义角色 |
| API-039 | `POST /v1/roles` | 创建自定义角色 |
| API-040 | `GET /v1/roles/{id}` | 查询角色权限与分配数 |
| API-041 | `PUT /v1/roles/{id}` | 修改自定义角色与权限 |
| API-042 | `DELETE /v1/roles/{id}` | 删除未分配的自定义角色 |
| API-117 | `GET /v1/users/role-options` | 以users:read读取本组织精简角色选项，支持搜索与分页 |
| API-118 | `GET /v1/workspace-extensions/plugins` | 创建时可选的固定版本插件及禁选原因 |
| API-119 | `GET /v1/workspace-extensions/{id}` | 本人已选扩展、冻结版本/范围及当前运行代次状态 |
| - | `GET /v1/workspace-extensions/{id}/skills` | 本人创建时选择的技能名称与版本，不包含技能内容 |
| API-043 | `GET /v1/permissions` | 读取固定权限目录、中文名称、用途说明、可分配性及依赖 |
| API-044 | `GET /v1/model-directory` | 读取已保存配置版本的批准目录 |
| API-045 | `POST /v1/model-directory:refresh` | 刷新已保存配置版本的批准目录 |
| API-046 | `GET /v1/model-providers` | 读取启用的提供商 |
| API-047 | `GET /v1/model-catalog` | 读取组织批准模型目录 |
| API-048 | `GET /v1/my-models` | 列出本人未归档连接 |
| API-049 | `POST /v1/my-models` | 添加本人外部模型并排队同步 |
| API-050 | `GET /v1/my-models/{id}` | 读取本人连接及同步结果 |
| API-051 | `POST /v1/my-models/{id}:validate` | 本人模型动作：validate |
| API-052 | `POST /v1/my-models/{id}:revoke` | 本人模型动作：revoke |
| API-053 | `POST /v1/my-models/{id}:archive` | 本人模型动作：archive |
| API-054 | `GET /v1/my-model-default` | 读取个人默认及版本 |
| API-055 | `POST /v1/my-model-default` | 设置通过校验的个人默认 |
| API-056 | `POST /v1/my-model-default:clear` | 清除个人默认并保留版本防止旧请求覆盖 |
| API-057 | `PUT /v1/my-models/{id}` | 更新本人连接并冻结新的模型与密钥版本 |
| API-058 | `GET /v1/model-catalog/manage` | 管理员读取组织全部模型目录（含停用） |
| API-059 | `POST /v1/model-catalog/manage` | 管理员添加组织批准模型与区域 |
| API-060 | `PUT /v1/model-catalog/manage/{id}` | 管理员按版本编辑或停用模型目录 |
| API-061 | `POST /v1/users/{id}:retry-password-reset` | 继续原密码重置，无需重新输入密码 |
| API-062 | `GET /oidc/.well-known/openid-configuration` | Discovery for the embedded first-party authorization-code provider. |
| API-063 | `GET /oidc/jwks` | Public signing key only. The RSA private key is never exposed. |
| API-064 | `GET /oidc/authorize` | Only the configured client and exact HC_PUBLIC_URL/auth/callback are accepted. Duplicate or unsupported parameters are rejected. |
| API-065 | `POST /oidc/login` | A bounded urlencoded form authenticates the account and consumes its browser-bound authorization challenge. No password or protocol tokens are returned … |
| API-066 | `POST /oidc/token` | Client secret basic only; code is consumed once and bound to redirect, PKCE, credential version and authority generation. No refresh/password/client-cre… |
| API-067 | `GET /oidc/userinfo` | Requires the opaque provider access token, not a Portal cookie. Rechecks enabled account, organization, member generation and credential version. |
| API-068 | `POST /v1/model-providers/{provider}:sync` | 读取官方/自定义服务实时模型列表 |
| API-069 | `POST /v1/my-models:batch` | 同一提供商多个模型原子保存及完整批次幂等 |
| API-070 | `GET /v1/users/{id}:quota` | 查询用户有效额度、占用与组织总上限 |
| API-071 | `PUT /v1/users/{id}:quota` | 按预期版本分配完整四项资源额度 |

## 3 请求、分页与并发

登录恢复：浏览器HTML导航访问`/auth/callback`时，失效或无效的授权303返回固定`/auth/login`重新登录，不显示整页认证JSON；JSON客户端继续收到401。`/oidc/authorize`和密码提交也检查BFF事务及浏览器绑定，旧表单失效后重新建立登录，不自动重交密码。仅匹配当前浏览器的旧事务可保留白名单内的`return_to`。

实例、用户、角色与审计列表支持 `page`（从1开始）和 `limit`（默认20、最大100），返回按当前筛选与实际权限统计的 `total`，可用 `ceil(total/limit)` 计算总页数并直接查询尾页；空列表界面显示第1/1页。按页查询使用稳定时间/ID排序，数据变化后总数会更新，不是冻结快照。保留有签名游标兼容原客户端，不透明next_cursor为空表示结束；page与cursor不能同时传入，非法页码返回422。游标绑定当前组织、过滤、排序和访问权限上下文，不能伪造跨用户翻页。查询过滤和排序取OpenAPI枚举；不返回其他Owner的记录。未知/重复JSON字段与不合法ID、状态、版本、邮箱、提供商、模型或URL由服务端拒绝。

修改用户/角色/模型目录/连接与实例的请求携带对应expected_version，更新以数据库条件和锁保护，冲突409，客户端重新读取和确认。稳定创建/命令请求携带Idempotency-Key；同键同安全参数重放返回原资源/操作，同键不同参数拒绝。密码、真实Key不进入请求摘要或审计。服务端生成组织、Owner、内部状态、权限代次和凭据引用，客户端不得覆盖。

长任务接受后返回202与持久操作标识；202只代表接受，不能显示成功运行。刷新和失联恢复查询同一操作；UNCERTAIN先核对外部效果，再继续原任务，不创建重复Workspace或模型路由。只有终态与必要健康证据满足时才显示可进入/已校验。

## 4 资源与配置语义

实例列表、详情和操作仅返回本人可访问数据；allowed_actions与can_enter按当前权限、Owner、状态及必要健康计算。创建/启动/停止/应用配置/删除在接受事务内重查资格、版本、配额和原资产，记录操作及审计；Worker执行资源效果。删除保留原卷，永久回收为受控CLI，无用户永久删除API。停止/删除立即撤销访问代次与入口。

模型配置是不可变版本，更新生成新版本；默认项变更只影响新创建。已有实例显式应用新配置。个人模型只允许本人读写，真实提供商Key加密保存、不读回；同提供商及地区允许省略Key复用，空值/切换提供商或地区不得借用旧Key。平台目录与受控网关决定可选模型/endpoint，用户不能提交任意网络地址或协议参数。同步未知、周期核对过期或真实校验失败不能被目录读取成功替代。

校验模型可能收费，用户显式发起，并受超时/输出/调用次数预算限制。管理CRUD/协议合成测试不能证明真实模型可调用。撤销需完成所有版本的后台虚拟Key吊销和在用引用检查；详情中的状态/原因反映实际确认结果。

## 5 用户、角色与权限

用户与角色治理按users/roles有效权限及依赖授权，自定义角色可显式获授；服务端入口与数据库事务内同时校验。平台创建账号使用identity_source=local、显示名、邮箱、角色和12～72 UTF-8字节初始密码；subject与独立Workspace Owner由服务器生成。登录邮箱固定为创建时邮箱，联系邮箱后续可编辑。内置Provider部署不接受仅绑定外部subject的新账号创建。

PENDING_IDENTITY表示凭据供应尚未确认，不能登录/创建资源；重复供应核对稳定账号且不自动覆盖初始密码。禁用/归档/角色与权限修改推进auth_generation并撤销旧会话/票据；禁用保留资源并按既有规则安全停止。禁止修改自身角色/禁用/归档自身，保护最后有效管理员。内置角色不能修改或删除；在用自定义角色不能删除；归档用户必须先处理原资产及待确认任务。

角色权限来自固定目录，显式包含依赖权限，不接收任意字符串；返回有效权限供页面菜单和按钮使用，前端隐藏不替代后端授权。Owner隔离仍叠加于功能权限，管理员不能借角色读取其他用户的私有模型Key。

## 6 本地密码重置语义

`POST /v1/users/{id}:reset-password` 提交expected_version和password。意图、旧会话撤销、授权代次推进与接受审计先原子提交，再由Accounts更新账号bcrypt散列及credential_version。确认及完成审计提交返回200；结果未知返回202和ManagedUser.password_reset=PENDING。新密码不能覆盖PENDING任务，返回409 PASSWORD_RESET_PENDING；用户归档同时拒绝。

`:retry-password-reset`只接受expected_version，按原任务/同一加密散列恢复；活跃15秒租约返回409 STATE_CONFLICT。PENDING期间账号登录、授权码交换及UserInfo暂不可用。完成后SUCCEEDED、旧会话再次撤销、版本递增、任务密文清除；重置自身须重新登录，不能以原会话401推定重置失败。所有响应和审计不包含密码或散列。

## 7 业务错误与日志

业务错误为 `{code,message,request_id}`；401表示未认证/会话失效，403为无权限或CSRF/Owner拒绝，404隐藏不可访问资源，409表示版本、幂等、引用、状态或额度冲突，422为输入语义不合法，429为并发容量已满或上游限流，503为依赖不可用。具体code以OpenAPI及服务端Problem为准，页面提供安全可操作的提示，不回显上游原始错误。

不得记录Cookie、Authorization、密码、Key、ID/Access Token、登录查询中的code/state/nonce/PKCE、签名私钥、模型请求正文或用户文件。认证页面和协议响应no-store，协议响应Referrer-Policy=no-referrer，登录HTML为strict-origin（只发送origin，不携带路径/query）；响应中的路径和ID不能被视为授权。

## 内置 OIDC 协议

| 编号 | 入口 | 认证/结果 |
|---|---|---|
| API-062 | GET /oidc/.well-known/openid-configuration | 公开Discovery，只声明当前授权码/RS256/S256/固定客户端能力 |
| API-063 | GET /oidc/jwks | 公开RSA公钥及kid，不含私钥 |
| API-064 | GET /oidc/authorize | 验证客户端、精确redirect_uri、state、nonce、scope、response_type=code、S256；200返回安全登录HTML与临时Cookie |
| API-065 | POST /oidc/login | 同源Origin、挑战Cookie和表单CSRF；303固定回调+code/state，错误密码401、安全错误403、依赖503 |
| API-066 | POST /oidc/token | client_secret_basic及urlencoded授权码/PKCE；200 Access/ID Token，invalid_client=401，invalid_grant/unsupported_grant_type=400 |
| API-067 | GET /oidc/userinfo | Authorization: Bearer随机Access Token；200按scope返回sub/name/email/email_verified=false；失效/撤销/资格变更401 |

这些是OIDC协议入口，错误形状为 `{error: "协议错误码"}`，与业务 `{code,message,request_id}` 区分。只支持固定平台客户端和固定回调；无动态注册、刷新令牌、密码grant、隐式流或外部身份联邦。`/oidc/login.css`和`/oidc/login.js`分别提供登录页本地样式与表单交互；不引用第三方资源。密码登录401返回HTML错误表单，保留邮箱但不回显密码。协议完整参数与响应见[OpenAPI](../api/openapi.yaml)，期限/散列/密钥规则见[安全设计](security.md#内置-oidc-provider)。

平台用户创建 `identity_source=local`，必须提交初始密码、不得指定subject；subject与Workspace Owner由服务器生成，已有主体不会按邮箱自动合并。历史受控映射数据的oidc来源只保留为数据识别，不作为内置账号创建入口。账号导入属于受控CLI，无公共HTTP导入接口。

## 平台原生模型接口

模型入口为独立Go进程，公开路径使用受控HTTPS。它不属于Portal BFF的Cookie接口，不接受用户任意代理请求。提供商真Key只供服务端解密，DSH运行密钥只能访问绑定连接版本。

| 接口 | 凭据与行为 |
| --- | --- |
| GET /v1/models | 运行密钥；只返回一个绑定别名，不触发推理 |
| POST /v1/messages | 运行密钥；文本/工具流，model必须为绑定别名，max_tokens不超过冻结上限，未知字段/多模态拒绝 |
| GET /v2/model/info | Worker控制凭据；modelId稳定UUID，返回冻结provider_params，不含api_key |
| POST /model/new、/model/delete | Worker控制凭据；仅注册数据库已批准的连接版本，原标识重复操作安全；撤销同时关闭关联运行密钥 |
| GET /key/info | Worker控制凭据；只以SHA-256摘要查询，不把明文Key放URL |
| POST /key/generate、/key/delete | Worker控制凭据；注册必须匹配平台已签发密钥，绑定唯一别名和两个使用路径；已撤销Key不得重开 |
| GET /readyz | 不含秘密的进程/数据库就绪；不证明提供商真实推理能力 |

管理凭据访问推理和目录返回403；运行密钥访问管理返回403；无效/撤销/失权运行密钥401，跨模型/不支持请求400，并发容量已满429，传输/协议故障502。上游错误正文不透传，流开始后失败发送安全error事件而不宣告成功结束。稳定参数、费用及超时见[模型需求](requirements.md#67-平台模型网关外部模型接入规则req-16)。

提供商实时目录与批量模型：`POST /v1/model-providers/{provider}:sync`（API-068）接收`api_base`和只写`api_key`，返回`items[{id,name}]`。要求`personal-model:write`、Origin/CSRF。它只读目录，无模型推理；失败不含上游原文。`POST /v1/my-models:batch`（API-069）接收`items`，每项沿用个人模型创建DTO，可在`catalog_entry_id`和`selection`中二选一；`selection={provider,api_base,model,response_models?,context_window?,max_output_tokens?}`。1–20项须同一提供商/API根，整体事务及完整请求幂等，202返回全部个人模型。私有目录限2000个版本快照，公共目录100项，读取目录最多100公共+20本人当前绑定项。能力数值为保守配置，只有显式推理校验成功后可使用。

## 用户资源配额（REQ-18）

用户配额查看和调整分别需要 `users:read`、`users:manage`；写入接受 `expected_version` 和 `limits`，四项均须提供非负安全整数，允许 0。字段为 `max_instances`、`cpu_millicores`、`memory_bytes`、`storage_bytes`。运行默认配置已初始化时，新建用户会按默认规格乘以每用户默认实例数生成独立额度，资源维度不超过组织上限；此前已存在或尚无运行默认配置的未分配用户，实例上限继承组织统一用户默认，其他维度继承组织上限。返回 `configured`、`limits`、`usage` 和 `organization_limits`，占用包含保留数据和恢复账本。

调整低于已占用返回 `409 QUOTA_BELOW_USAGE`，预期版本不符返回 `409 VERSION_CONFLICT`，高于组织上限或缺字段返回 `422 IDENTITY_INVALID`。跨组织目标返回404，归档目标409，普通成员403；审计失败整体回滚。创建、启动和运行重试同时检查组织及用户配额，超额429且不写入资源/操作；停止、删除和原请求幂等重放不重复扣额。实例数量同时决定可运行数量，不接受独立运行数量字段；启动已有实例不重复扣减实例数。CPU、内存等资源仍按组织和用户上限校验。额度是共享池的使用上限，不表示宿主机容量承诺。


## 模型提供商账号（REQ-17）

| API编号 | 方法与路径 | 模型提供商账号操作 |
|---|---|---|
| API-072 | GET /v1/my-provider-accounts | 本人连接及已选模型/工作空间使用数量 |
| API-073 | POST /v1/my-provider-accounts | 整组原子创建，Idempotency-Key，expected_version=0 |
| API-074 | PUT /v1/my-provider-accounts/{id} | 整组修改名称/Key/选择集合，固定提供商及API根、预期版本 |
| API-075 | POST /v1/my-provider-accounts/{id}:delete | 检查实例引用、撤销整组及清空默认；异步DELETING→DELETED |
| API-076 | POST /v1/my-provider-accounts/{id}:sync | 以已保存本人Key或本次临时替换Key读取官方/自定义列表 |

输入{name,provider,api_base,api_key?,models:ModelSelection[],expected_version}；新增要求api_key，编辑省略保留本人原密钥，1～20个模型须同一提供商/API根。返回ProviderAccount包含独立id、version、status、credential_configured与models；每模型含既有PersonalModel、冻结selection与in_use。Key无回显。写操作均要求Origin/CSRF。读需personal-model:read，新增/修改/目录需write，移除模型和整组删除另需revoke。引用冲突MODEL_IN_USE为409，版本冲突VERSION_CONFLICT为409；失败整批回滚。目录同步共用每Owner 10次/分钟，不调用推理。

模型提供商账号创建或更新返回202只代表配置已保存。后台同步成功后，新增及密钥变更的个人模型当前版本自动进行真实能力校验；查询账号内模型的validation状态跟踪结果，浏览器无需额外发起validate。仅改名或保留模型不重复校验，失败可使用原单模型validate接口手动重试。自动与手动共用版本租约和探测预算。

### 用户删除

`DELETE /v1/users/{id}` 提交 `expected_version` 和 `confirmed_name`（当前完整显示名，显示名为空时为完整用户ID），成功返回204，无响应体。要求users:manage及读取依赖，可由内置ADMIN或获授自定义角色执行，沿用现有 `USER_ARCHIVE` 原子事务及审计：撤销会话和票据、推进授权代次、状态转为 `ARCHIVED`；保留不可变身份、配额配置及历史，不调用外部身份提供方删除账号。删除后不能重新启用或用原身份换绑新的Owner。

当前账号、有未删除实例/未完成操作/保留盘/恢复占用、待确认密码重置及不符合状态的目标不能删除；版本过期409，确认名错误422，跨组织404。审计失败整体回滚。未传status时用户列表（及资源配额页）默认排除 `ARCHIVED`，可用 `status=ARCHIVED` 查看历史。兼容保留原 `POST /v1/users/{id}:archive`。

创建资源选择：API-013 GET /v1/preflight 支持独立可选的 spec_id（UUID），可与 model_configuration_id、model_configuration_version 成对参数组合。规格必须启用且在支持范围，配额按其金额计算；不改变组织默认。响应可含 quota：available/required 分别给出实例数、CPU millicores、内存/存储 bytes，concurrent_creates_available 单独给出剩余创建并发名额。available 取个人与组织剩余承诺额度的非负较小值，包含预留与保留存储，不返回其他用户明细。默认或额度未初始化时省略 quota，查询失败不能当零额度。只读预览不代替提交时原子校验。API-005 POST /v1/instances 的 spec_id 可选批准规格，省略沿用默认；模板与模板版本仍须默认批准值。未知、停用或不支持规格返回 422 INVALID_SPEC，额度不足返回 429 QUOTA_EXCEEDED；同一幂等键改规格返回 409 IDEMPOTENCY_CONFLICT。

## 提供商默认模型与审计扩展

ProviderAccount返回default_model_id（可空稳定模型ID）。新增/编辑输入default_model为models中一个模型名，拒绝未选模型；省略时新增取首项，编辑保留原默认或取首个保留项。POST /v1/my-model-default仍保留个人默认接口兼容性，同时更新目标模型所属账号的默认及账号版本，不清除其他账号默认。POST /v1/instances继续明确提交model_configuration_id/version，Portal由所选账号默认自动解析；不修改幂等重放规则。

GET /v1/audit-events新增outcome精确过滤（ACCEPTED/SUCCEEDED/FAILED/DENIED），与action、resource_id、from/to及分页组合，非法值返回INVALID_SPEC。OperationLog新增actor_name、owner_id、resource_type、resource_name、details；详情仅输出允许的配额、状态、角色、权限、原因代码、版本和关联ID。姓名为当前名称，无法解析时为空，不承诺事件时刻快照。筛选纳入游标作用域，沿用Owner和组织权限控制。详见OpenAPI。

## DSH账号模型目录与推理

模型执行网关GET /v1/models?scope=provider-account用运行Bearer密钥返回{object: list, data: [...]}；每项包含id（内部版本别名）、object、name（模型名）、context_window、max_output_tokens、response_models。只返回所选账号有效模型，最多20个当前模型及一个仍有效的冻结默认版本；无有效项返回空列表，管理Key不能执行此接口。

POST /v1/messages?scope=provider-account的model必须是该运行密钥所属账号中可用的模型别名；请求和响应受该目标模型能力约束。越账号/Owner、失效或未批准模型返回403 MODEL_NOT_ALLOWED；超出所选模型能力返回400 INVALID_REQUEST；模型切换与推理均不设置平台每分钟请求限制。未传scope的同步探测接口保留单模型契约，GET /key/info的管理核对仍描述原始锚定绑定。

POST /v1/instances已有可选name字段，Portal创建抽屉现提交用户填写名称；留空省略字段自动生成。name、资源规格与默认模型版本均纳入原始幂等摘要，不能改名后重放同键。

工作空间显示名称可选，留空自动生成；填写时使用1–32个字符，支持中文、字母、数字、下划线和短横线。删除确认头 `X-Confirm-Instance-Name` 使用 UTF-8 百分号编码（前端 `encodeURIComponent`），后端只解码一次后精确匹配；原 ASCII 名称继续兼容，畸形编码被拒绝。显示名称不用于容器、Terraform 或域名，内部标识继续由 UUID 生成。

实例列表及详情增加只读model_display_name和model_provider_name。个人模型按实例绑定的org_id/owner_id、model_configuration_id和精确版本读取不可变manifest.catalog.model；提供商按该版本的provider解析显示名，组织模型沿用原模型名。名称缺失返回空字符串。读取不追随个人模型头版本或目录新版本，不修改config_snapshot.model和执行路由，也不返回凭据。

## 默认用户资源配置初始化与修改（API-019、API-020）

`GET /v1/defaults` 需要 configuration:read，未初始化返回 404 DEFAULTS_NOT_CONFIGURED。`PUT /v1/defaults` 需要 defaults:write、同源/CSRF 校验，必填 version、template_id、template_version、spec_id；初始化 version=0，更新须精确匹配当前版本，成功递增，冲突返回 409 VERSION_CONFLICT。模板和规格须已批准且启用；平台页不展示模板选择，按当前批准模板及数字资源解析 spec_id。存储支持 1–100 GiB 整数，协议仍传批准 spec_id，不允许未批准资源金额。

可提交 retention_days（整数 1–365，初始化默认 7）和 max_user_instances（正整数，省略保留组织额度默认值）。上限不得超过组织 max_instances 或低于继承用户的现有实例占用；单独分配的用户额度不变。配置、默认额度版本与 DEFAULTS_SAVE 审计原子提交。数据期限用于后续确认软删除，已有更长资产期限不缩短；受控永久回收仍须满足独立 7 天安全门槛。资源修改不替换已有实例快照。

模型字段可省略：新建运行默认值不需要组织模型，返回 model_configuration_id=""、model_configuration_version=0。兼容调用若显式提交组织模型，须同时提供合法 ID/版本并通过现有组织归属、凭据及能力校验；省略不清除已有模型绑定。平台页面不发送这两个字段，创建时独立验证所选本人模型提供商账号。

API-043 返回当前34项有效权限目录（包含技能包、连接器、记忆及知识库权限），新增能力沿用既有授权：数据资产、文件浏览/预览/下载使用instances:read，手动释放使用instances:release-storage，用户资源配额使用users:read/users:manage，系统上限使用configuration:read/defaults:write，DSH原生插件与预设使用instances:access及本人工作空间边界。目录可附description说明，角色保存仍仅提交权限ID；目录展示不修改既有角色授权。API-117仅返回角色的id/code/name，需users:read；不返回权限集合、说明、版本或用户数。完整角色、权限目录读取仍需roles:read，用户写入与角色分配仍需users:manage；搜索及page/cursor规则与列表一致。

## 存储资产查询（REQ-19）

| 编号 | 方法与路径 | 契约职责 |
| --- | --- | --- |
| API-077 | GET /v1/storage-assets | 本人资产分页、状态筛选及名称前缀查询 |
| API-078 | GET /v1/storage-assets/{id} | 本人资产容量、用量观察、保留与维护阻断原因 |
| API-079 | GET /v1/storage-assets/{id}/backups | 本人资产的安全备份历史分页 |
| API-081 | GET /v1/storage-assets/{id}/files | 浏览本人运行、停止或软删除数据盘中的固定目录 `/data/workspace` 或 `/data/dsh`，每页最多100项 |
| API-082 | GET /v1/storage-assets/{id}/file | 预览最多512 KiB纯文本或下载最多64 MiB的普通文件 |
| - | DELETE /v1/storage-assets/{id} | 删除本人已回收的数据资产列表记录，保留后台关联与备份 |

`DELETE /v1/storage-assets/{id}` 要求当前 `instances:release-storage` 权限、同源和 CSRF，且目标属于本人。仅 `DELETED` 可删除；其他状态返回 409 `STORAGE_REMOVE_BLOCKED`，不存在或不属于本人返回 404。成功返回 `{"deleted":true}`；重复请求返回成功。操作只隐藏本人列表、详情和备份历史入口，保留数据库资产、备份及审计记录，不触发物理回收。删除与 `STORAGE_OWNER_REMOVE` 审计同事务提交。

资产读取接口要求当前有效会话、组织成员身份及 `instances:read`；显式限制组织和Owner，管理员无隐式跨Owner访问。跨Owner对象404，失权403，失效会话401。page从1开始，limit默认20、最大100；筛选及重复/未知参数严格校验。资产用量状态为UNSAMPLED/CURRENT/STALE/UNAVAILABLE，未知不回填0；备份返回任务标识、状态与时间，不返回归档路径、密钥、文件或私有摘要。维护阻断原因是说明，实际主机条件由受信CLI重新核验。

API-077的`status=PROCESSING`合并筛选PREPARING、RESTORING、RECLAIM_PENDING，先在服务端筛选再计数和分页，并保持组织、Owner和名称前缀约束；兼容原有精确状态筛选。返回资产的`status`保留原始状态，PROCESSING仅为查询分组，不写入资产状态。页面统一显示四种状态，具体处理阶段在详情中查看。

API-080 `POST /v1/storage-assets/{id}:release` 要求当前 `instances:release-storage`、Owner、Origin与CSRF，提交 `{confirmed_name,confirmed_asset_id}`。精确确认后返回202 `{request_id,asset_id,status}`；每资产请求唯一，重复调用核对原申请。仅限已删除的本人保留数据，可主动提前结束保留期；法律保留、未完成备份/生命周期任务、绑定或账本不明仍409，错误确认422，跨Owner404。GET资产状态确认回收进度；202不释放配额。后台核实物理结果与末尾审计一起提交后才完成。

API-081接收`root=workspace|dsh`、可选相对`path`和目录分页`after`，只返回名称、类型、字节数和更新时间。API-082接收同一根目录与相对文件路径，以及`mode=PREVIEW|DOWNLOAD`；预览仅返回UTF-8纯文本，下载以附件返回。两者在运行、停止或软删除工作空间的本人权限有效且没有冲突维护任务时执行；Worker持有实例锁并只读访问原数据盘。拒绝绝对路径、`..`、符号链接及非普通文件。文件读取不递归扫描、不写入、不由API直接挂载主机路径；一次性密钥保护暂存正文，数据库不保存明文，且内容不进入审计或长期缓存。

## 个人 Skill 库（REQ-20）

API-083～094 为 Portal 个人技能库接口：列表/创建、正文读取/保存/删除、调用元数据兼容接口、文件预览、公开链接获取、检查来源更新、确认应用、所选技能导出与上一版下载。另有 API-112～114（`GET/PUT/POST /v1/skills/{id}:file`）用于读取、编辑与新建文本文件；GET 须提供唯一 `path` 查询参数，PUT 提交 `path`、`content`、`expected_version`，保存使用整包版本；POST 使用相同请求字段新建脚本或其他文本文件，拒绝重名及路径冲突。二进制内容不作为文本返回或接受覆盖，其他文件不变。完整请求字段见 [OpenAPI](../api/openapi.yaml) 和[后端说明](backend.md#个人-skill-库)。

`skills:read` 允许查看和导出本人技能，`skills:manage` 允许维护、导入与手动同步，并依赖读取权限；内置角色默认具有两项。来源支持公开 HTTPS Markdown/ZIP 直链及 GitHub 仓库、`tree` 技能目录、`blob` 文件链接；仓库读取默认分支，目录限制扫描范围。拒绝查询参数、凭据、私网和保留地址。GitHub 归档流式写入私有临时文件并在处理结束后删除；多技能来源的更新只暂存当前技能。先预览再应用，预览有效 5 分钟且绑定用户、原记录 ID 和版本。同名默认跳过；明确替换时保留上一版，同步更新保留调用设置；无自动更新或向 DSH 容器写入。

技能包导入不再设置文件数量、上传或解压大小的固定限额，实际规模受浏览器、服务内存及数据库和磁盘容量约束；技能包其他 JSON 写请求仍有 7 MiB 上限，文本文件编辑仍限 1 MiB。远程来源保留下载超时、重定向和公开网络校验；批量导出仍最多选择 50 项。写操作不设置请求频率限制。`SKILL_INVALID` 返回 422，`SKILL_EXISTS` / `VERSION_CONFLICT` 返回 409，过期预览及他人技能返回 404。源获取失败保留当前包；导出返回 `no-store` 附件。


## 外接记忆与知识库（API-095～099、API-120）

`/v1/context/{memory|knowledge}`提供本人连接列表/创建，`/{id}`更新/移除；`/{id}/{action}`提供验证、list/search/get/export、预览/保存、内容删除/重建、绑定和核实。POST即使只读仍校验Origin及CSRF。`apply/delete/rebuild`使用请求内幂等键；绑定同时检查连接与绑定版本；`reconcile`只接受空对象，重放服务器加密保存的原始操作。移除连接保留外部数据，永久内容删除独立确认。公开字段以[OpenAPI](../api/openapi.yaml)为准，支持程度见[实施记录](context-implementation.md)。

`POST /v1/context/knowledge/parse` 接收一个 multipart 文件并转交配置的 Docling，返回可编辑的 Markdown，不直接写入知识库。知识库 `list` 可提交 `page`、`page_size` 和标题 `query`；`search` 支持 `semantic`、`keyword`、`hybrid`，保留旧客户端的 `exact` 模式。`min_similarity` 可选，适用于语义和混合检索的向量结果；关键词命中不受阈值限制。未提交阈值时保持原有行为。


## 工作台连接器（REQ-23，API-100～111、API-115/116）

| 方法与路径 | 权限及用途 |
| --- | --- |
| `GET /v1/connectors` | `connectors:read`；本人连接、工具快照、批准服务及29种内置连接器目录 |
| `POST /v1/connectors`、`PUT /v1/connectors/{id}` | `connectors:manage`；创建及版本化保存；凭据只写 |
| `DELETE /v1/connectors/{id}` | 管理权限；`expected_version`、`confirmed_name`确认删除 |
| `POST /v1/connectors/{id}/sync` | 管理权限；`expected_version`验证并完整同步目录 |
| `POST /v1/connectors/{id}/tools` | 管理权限；`expected_version`、`enabled_tools`选择工具 |
| `POST /v1/connectors/{id}/invoke` | `connectors:invoke`；手动确认后调用 |
| `GET /v1/connectors/{id}/calls`、`GET /v1/connectors/calls/{id}` | 读取权限；分页或按ID读取脱敏状态 |
| `POST /v1/connectors/calls/{id}/cancel` | 调用权限；尽力取消，不能证明外部操作撤销 |
| `POST /v1/connectors/export`、`POST /v1/connectors/import` | 管理权限；无秘密JSON配置迁移，导入先预览再确认 |
| `POST /v1/connectors/{id}/oauth`（API-115） | 管理权限、CSRF及`expected_version`；返回`authorization_url`供当前浏览器跳转，不返回Token或客户端密钥 |
| `GET /v1/connectors/oauth/callback`（API-116） | 当前组织、Owner及登录会话；单次消费state并交换授权码，保存密文后验证工具；303返回工作台 |

所有写请求须同源及CSRF。配置及工具选择使用当前版本；调用另提交目录版本、UUID `idempotency_key`、JSON对象参数、`confirmed`及必要的`risk_confirmed`。同键同请求仅返回原状态，不再次执行；同键不同请求409。调用结果仅在本次响应返回，查询和重放均无正文。发现和调用的网络/业务失败分别通过连接状态及调用状态反馈；参数422、版本/幂等/并发冲突409、失权403、跨Owner404、超限429。完整DTO及限额见[OpenAPI](../api/openapi.yaml)与[实施记录](connectors-implementation.md)。

`GET /v1/connectors` 新增 `catalog: {version, source_url, entries}`。每个条目提供 `id/name/description/category/mode/auth_kind/default_url/doc_url/guide_url/setup_hint/requirements/color`；可选 `default_url` 为10种官方远程服务提供预填地址，其余服务按个人部署或应用发布结果填写。`mode`、`auth_kind`描述上游接入要求，不代表平台已运行适配器。批准服务新增可选 `catalog_id`，仅支持已知目录ID；不设置时继续作为自定义MCP服务。API继续支持平台服务 `endpoint_id`；工作台中每个目录条目及“自定义 MCP”均提交本人填写的 `url`、可选 `catalog_id` 及独立凭据，两种目标只能选一。推荐服务可预填地址，允许用户修改；旧平台连接编辑后保存为个人地址。公网 HTTPS 无需管理员预先绑定；私网或 HTTP 须精确匹配平台例外。地址不接受内嵌账号、查询参数和片段。更换地址须重新填写 Bearer 凭据。导入同样校验个人地址，导出不含凭据和工具授权；保存不发起外部请求。

`auth_type`支持`none`、`bearer`、`oauth2`。`bearer`在自定义网关使用标准Bearer；精确匹配腾讯文档、腾讯会议、微云、乐享或滴滴官方地址时使用厂商原生认证。乐享的`credential`为包含`token`和`company_from`的JSON字符串，二者一起加密且不读回；滴滴Key只在出站时写入查询参数，不存入连接URL。OAuth凭据只能经授权流程获取，配置请求不得手填OAuth Token。OAuth连接须先保存并启用，再发起授权。回调state有效10分钟，绑定连接版本及完整目标；取消、过期、重放、跨会话和配置变化均不能发布新凭据。回调消费在Token交换之前完成，交换中断须重新发起授权。

回调处理后返回 `/app/connectors`，查询参数`connector_oauth`为`ready`、`verify_failed`、`cancelled`或`failed`之一，页面展示后清除该参数。返回URL不含授权码、Token或厂商错误正文；登录失效时返回工作台登录流程。授权成功只同步工具，不自动启用或调用。厂商账号完整验收与企查查专用聚合/解绑是独立待完成项，当前没有对应聚合或远端撤销API契约。

## 创建扩展配置（REQ-25；API-005、118～119）

API-005支持可空的`skills/connectors/memory/knowledge/plugins`。新四类只提交资源ID、准确版本、连接器目录版本或`personal/workspace`范围，与模型/资源一起原子接受及幂等重放；不接收密钥、任意npm地址或外部命名空间。非空四类需要`instances:configure`，连接器另需read/manage/invoke，记忆和知识库分别需read/manage。新增`GET /v1/workspace-extensions/plugins`（API-118）返回兼容性及禁选原因；`GET /v1/workspace-extensions/{id}`（API-119）返回本人配置及当前代次逐项状态，不返回无当前类型读取权限的名称或引用。原生DSH工具使用私有Worker通道，无新增公网工具调用接口。字段、数量和状态枚举以[OpenAPI](../api/openapi.yaml)为准。
