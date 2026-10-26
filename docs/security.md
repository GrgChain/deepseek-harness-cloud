# DSH Cloud 安全设计

版本：V0.1。身份与访问设计基线；OIDC/BFF、配置加密、Owner 与 Worker 隔离已有模块证据，Gateway 交接与 HTTP/WS 已有真实浏览器证据，完整安全验收尚未完成。适用单组织可信内部用户。数值以 [配置规则](deployment.md#8-配置与业务规则基线) 为准。

## 1 身份与权限

首版固定 OIDC Authorization Code + PKCE，由同一 Go API 内的 AuthorizationServer 签发、BFF 交换并用标准验证库验证 ID Token。校验 issuer、audience、签名算法/JWKS、exp、nonce、state；拒绝隐式流、任意身份头和浏览器自传 Owner。固定回调 `/auth/callback`；登录返回路径仅允许服务端白名单内 `/app/` 相对路径，禁止外域/双斜杠/编码绕过。

首次管理员以受控初始化命令登记 issuer+subject、固定组织、角色和对应 Workspace Owner UUID（字段 workspace_owner_id）；成员不自助注册，不按可变 email 自动合并账户。管理员页面/API管理用户、自定义角色和功能权限，规则见[功能需求](requirements.md#64-用户管理规则req-13)。身份供应未确认、无映射、disabled或Workspace Owner不合法时禁止创建/进入；身份密码由平台数据库的 bcrypt 散列管理，平台不得回显或记录明文密码。

平台密码重置先原子提交任务、授权代次推进、旧会话撤销和接受审计，再由 Accounts 更新数据库中的 bcrypt 散列及 credential_version。任务仅保存 AES-GCM 保护的 cost=12 散列，以环境、组织、用户、任务与 identity-password-hash 用途绑定，原始密码不进入数据库、摘要或审计。凭据写入使用独立事务，固定5秒调用期限；15秒租约未到期拒绝并发领取，过期只重试原任务和同一散列。写入应答未知时保留 PENDING，禁止新密码覆盖和归档，登录、授权码交换与 UserInfo 同时拒绝待确认账号。确认及审计原子提交并清除任务密文；完成时重新检查当前管理员资格。重置自身撤销原会话，需要重新登录。

内置ADMIN默认持有全部治理权限，自定义角色也可显式获授固定目录中的用户、角色和模型目录管理权限；授权以当前有效权限及依赖为准。ADMIN/MEMBER的系统权限不可网页改写，最后一个ACTIVE管理员不得禁用、归档或降级。用户状态、角色分配及角色权限变更须推进受影响用户auth_generation，原子撤销旧会话/票据并追加审计；写入事务内按当前权限重查，审计失败全部回滚。完整目录及依赖见[权限规则](requirements.md#66-权限目录与生效规则req-15)。

Portal 随机 256-bit 会话 Cookie 只保存摘要到 PostgreSQL；浏览器不持有 OIDC Token 或控制面凭据。会话绝对/空闲期限、CSRF 和 Cookie 属性见配置。登录 state 与浏览器临时 Cookie 绑定，PKCE verifier 加密存储，一次消费。退出先撤销 Portal 与关联 Gateway 会话再清 Cookie，活动 WS 随下一次检查关闭。每次登录要求重新验证密码；内置身份服务不保留独立的浏览器 SSO 会话。

| 动作 | 内置成员本人 | 内置管理员本人 | 内置管理员对他人 |
| --- | --- | --- | --- |
| 查看默认项/模板/脱敏模型摘要 | 允许 | 允许 | 相同组织摘要 |
| 列表/详情/操作/启停/删除/重试/应用配置 | 允许 | 允许 | 不允许；越权资源返回 404 |
| 进入工作台/文件与会话内容 | 允许 | 允许 | 不允许 |
| 数据资产、目录浏览、文件预览与下载 | instances:read及本人归属 | instances:read及本人归属 | 不允许 |
| 永久释放已删除工作空间的数据 | instances:release-storage及本人确认 | instances:release-storage及本人确认 | 不允许 |
| 保存/校验/撤销模型、更新默认项 | 拒绝 403 | 允许 | 组织配置，不授予内容权限 |
| 预检 | 安全摘要 | 含脱敏技术诊断 | 不含用户文件 |
| 审计查询 | 本人动作与本人实例事件 | 组织审计元数据 | 不含工作正文 |
| 用户查询/新增/编辑/角色分配/启停用/归档 | 拒绝403 | 治理界面禁止自改角色、自禁用及自归档 | 允许；最后管理员、身份和资产保护仍生效 |
| 自定义角色与权限配置 | 拒绝403 | 允许；内置/在用角色保护 | 不授予他人工作空间内容权限 |

上述治理功能已实现，完整产品验收状态见[测试文档](testing.md)。自定义角色逐项检查有效权限，不按ADMIN/MEMBER字符串推定；已有实例动作、配置、审计与访问均需纳入该检查。任何角色仍受组织、Owner和资源状态限制，工作空间内容不因拥有用户/角色/组织审计管理能力而可读。

首版不提供临时维护授权 API。主机/Workspace/密钥系统特权运维属于受信边界，维护必须另行记录原因、操作者和影响，不能承诺抵抗其技术访问能力。

## 2 Portal 会话与请求防护

全部 /v1 和 /auth/logout 使用 Cookie 身份；`GET /v1/me` 返回当前会话 CSRF token 和有效期，Cache-Control: no-store。csrf 原值仅在前端内存；服务端保存摘要与加密副本，GET解密返回同一会话值，写入恒时比较摘要。所有写接口要求 `X-CSRF-Token`、精确 Portal Origin，JSON 接口 Content-Type=application/json；禁止通配 CORS。无身份 401，CSRF/Origin错误 403，未授权对象 404。

Portal Cookie 为 `__Host-hc_session`，无 Domain。Portal 与实例必须是同一受控站点下的不同 Host，如 portal.cloud.example 与 i-<UUID无连字符>.cloud.example；不允许任意外部实例域名。CSP 限制脚本自身/构建 hash、form-action 为批准实例 Host；frame-ancestors 'none'、X-Content-Type-Options: nosniff、Referrer-Policy: no-referrer。绝不共享父域 Cookie。

## 3 浏览器访问交接契约

访问模块为 Go 最小 Gateway，固定实例子域，原生 DSH Cookie 在服务端加密保存，浏览器只持 Gateway 会话。全部路径、状态和 payload 已在 OpenAPI 定义；真实上游交换见 [运行适配](backend.md#123-原生认证与访问路径)。

1. Owner 从实例详情取得固定 access_host。前端向该 Host 的 `GET /__dsh/challenge` 发起 credentials=include 的 fetch，Origin 必须等于 Portal。Gateway 生成 challenge_id 与随机秘密，写 PostgreSQL，并设置仅该 Host 的挑战 Cookie，响应 challenge_id/expires_at。CORS 仅允许精确 Portal Origin、GET、credentials，Vary: Origin。
2. 前端用 Portal 会话、CSRF 向 `POST /v1/instances/{id}:access` 提交 challenge_id。API 实时检查 Owner、身份映射/Workspace Owner、实例状态、五维中必要健康、路由与限流；模型失败可诊断进入。绑定挑战、主体/Portal session、Host、auth_generation、access_generation，签发一次性随机票据。
3. 响应 200 `{handoff_url,ticket,challenge_id,expires_at}`，handoff_url 只能是该实例 `https://<access_host>/__dsh/handoff`。响应禁止缓存/上报。前端立即以新窗口 HTML form POST 隐藏字段 ticket/challenge_id；票据不在 URL、存储或页面可见正文。弹窗被拦截时显示明确打开命令，复用挑战流程重新签发，禁止把票据改放链接。
4. Gateway 要求 Portal Origin、form 类型、≤8KiB、挑战 Cookie摘要匹配；网络调用在事务外完成；随后按[统一锁序](data-model.md#3-创建和幂等事务)锁授权主体、配置、实例、挑战和票据并重新检查 Portal会话、当前角色/Owner、代次、实例/路由/健康和过期，原子消费两者，生成 Gateway会话。重复/过期拒绝 403，无副作用。
5. Gateway 在事务外验证 Worker 为当前运行代次建立并加密保存的原生会话。成功设置 `__Host-hc_access`，清挑战 Cookie，303 到实例根 `/`；原生失败返回 502/503 并撤销本次 Gateway 会话，已消费票据不能复用。
6. 后续 HTTP 每请求从 PostgreSQL 检查组织/用户/membership状态、Portal与Gateway期限/撤销、Owner、配置及credential未撤销、两种代次和启用路由；WebSocket 握手校验 Origin 精确等于实例 HTTPS根，建立后每 10秒重新检查，DB不可用或失权关闭。模型故障不解除其他授权条件。

票据与挑战分别至少 256 bits，摘要存储，短期清理；挑战 ID 不能替代随机 Cookie秘密。无需在实例 Host 发送 Portal Cookie。不同浏览器窃取票据因无挑战 Cookie不能消费；并发消费只成功一次。平台 CSRF token 不下发实例域。

## 4 路由、原生会话与撤销

固定路由从可信 Worker 登记的实例/IP/端口/进程代次读取；Host必须精确匹配且路由存在。代理拒绝任意 upstream URL、重绑定 DNS、伪造 Forwarded/X-Forwarded-* 与未知 Host。管理网络与成员网络隔离，DSH端口不发布主机公网。

原生 Cookie 不透传浏览器，Cookie jar按实例/进程代次隔离；浏览器提供的 Cookie/Authorization不能改变上游身份。代理保留已校验 Host/Origin，HTTP/WS使用同一授权。禁止启动 Token进入浏览器URL、日志、trace。服务端原生根交换的内部 Token query 必须从 HTTP访问日志/追踪中整体排除。

已实现的后台健康检查只访问已核验容器的固定私有IP/端口和原生路径，禁用代理、重定向及任意URL；核验匿名HTTP/WS拒绝、精确Origin和带服务端Cookie的认证成功。原生会话到期续期必须使用同一实例/运行代次的私有凭据通道，所有必需检查通过后才加密发布。过期探测、配置/实例版本变化、失权或路由关闭均丢弃发布，健康任务不得启用路由。原卷读写仅在固定私有探测目录中操作自己新建的排他文件，不读取用户内容或创建替代卷。

停止/删除接受事务立即增加 access_generation、禁用 route、撤销相关会话/票据；Worker清理前先确认撤销。失权推进 auth_generation。Gateway查当前PG事实，并在每HTTP请求/WS握手及10秒WS检查中核对内嵌Workspace Owner/主体状态；授权查询总超时2秒，不可用关闭，禁止缓存旧许可。目标撤销≤30秒，重启不会让旧会话绕过代次检查。

不提供 Coder 管理、Agent、App、端口共享或转发入口，模板不发布第二工作台入口。外网只允许 Portal与Gateway；旁路未封闭则预检失败。

## 5 密钥与加密

初始实现使用 Go crypto/aes + cipher.GCM，AES-256-GCM、每条记录随机96-bit nonce，AAD包含环境、org、secret ID和用途，密文及key_id存PG，32-byte主密钥以只读0600 Secret文件独立注入；主密钥不进数据库或同一备份。轮换以key_id读取旧密钥、新写用新密钥，完成重加密/恢复演练后才移除旧密钥。API无解密返回接口。

模型保存首次必须提供Key；省略Key只复用未撤销凭据。撤销配置使该版本禁止新用、健康失效，并在同一配置事务撤销所有引用同一credential_ref的版本；新授权直接检查该事实即刻拒绝，实例路由关闭/代次推进/系统停止按[数据事务规则](data-model.md#3-创建和幂等事务)分批执行并扫描补偿；已有活跃操作设置阻断标记，在下一检查点进入撤销处理，禁止绕过单活跃约束插第二操作。复制的直接注入Key只能由模型上游吊销；上游管理动作由受信管理员执行并记录结果，本地不声称已吊销。共享Key影响范围按credential_ref计算。

平台DB/Redis/OIDC管理秘密仅可信控制面持有，不进DSH。直接注入的模型Key、DSH原生签名秘密可能被本人Shell读取；首版只对可信内部用户开放，不提供绝对Key隐匿或不可信代码隔离承诺。密钥请求体、错误、备份均必须脱敏/加密，不用通用对象dump日志。

个人模型真实Key在平台PostgreSQL以AES-GCM加密，AAD绑定environment/org/owner/connection/secret/version及provider-key用途。Go模型入口通过独立hc_model_gateway账号受控读取，按请求解密；不保存额外数据库副本。运行密钥使用独立model-key用途加密，执行表只存摘要及绑定路由。DSH仅获得本人连接版本运行密钥，可用路径只有/v1/messages与/v1/models；管理Key与推理Key互斥。本地撤销、停用Owner或目录立即阻止新请求，已接受的流最多120秒结束。

模型目录与真实校验只由服务端访问网关，权限沿用configuration:read、model:validate及独立写入/默认项权限；不把网关管理API暴露为通用代理。平台、网关和诊断默认关闭提示/回复/工具正文及认证头持久日志，仅保留必要脱敏事件与关联ID；原生模型进程的日志保留、平台备份和第三方模型数据外发须纳入部署控制。详细规则见[模型需求](requirements.md#67-平台模型网关外部模型接入规则req-16)。

## 6 容器、工具与数据

用户容器非root、no-new-privileges、cap-drop ALL、seccomp、PID/CPU/内存硬限制；禁止privileged、hostNetwork、Docker Socket、平台DB、其他卷/主机目录。可写目录限/data和受限tmpfs。工作空间Owner可在实例内启停内置插件、安装或卸载第三方DSH组合包；插件在该实例的DSH进程内运行，共享该实例的数据与权限，不获得平台宿主机或其他实例权限。安装前沿用上游信任提示、版本兼容检查和构建脚本批准，不自动批准脚本或版本豁免；不因此默认开放MCP。

平台启动覆盖层关闭非必要遥测、自动会话日志上传和默认额外外发扩展；模型调用只发送任务所需内容。Owner主动安装的插件可能增加实例内行为；其网络访问仍受原出口策略约束。插件管理不修改平台批准模型、原生认证或容器权限；上游默认额外外发能力仍须显式关闭并由出口测试验证。

config-editor与settings用于持久化预设等原生设置，写入沿用revision冲突检查。平台启动覆盖项优先；通过原生设置API覆盖平台默认模型须被服务端拒绝，不能只靠页面隐藏入口。此限制针对配置写入，不隔离同进程插件；第三方代码可接触DSH进程可用的数据和凭据，当前容器仍仅适用于可信内部用户与可信代码。

DeepSeek直连仅允许批准HTTPS公网出口。平台模型网关支持显式批准的专用私网网关，精确绑定HTTPS主机名/端口、固定IPv4目标和受控CA；API探测与DSH网络围栏共同限制。禁止回环/链路本地/元数据地址、重定向及DNS重绑定，批准不得覆盖DB/Redis/OIDC或宿主控制面。用户只能选择平台目录，不能提交任意地址、认证头、路径或参数。Docker共享内核适用于可信代码。

数据卷初始化/硬容量/缺卷保护见运行适配；软删除保留、备份/恢复见部署。Redis使用内网ACL，不存秘密、完整会话或授权许可。

## 7 安全测试

TC-01、TC-07～11、TC-14、TC-26、TC-28、TC-35～39及新增TC-51～68必须覆盖正常与拒绝路径。源码/生成前端/日志扫描Key、Token、Cookie；真实浏览器验证挑战绑定、CSRF、跨实例Cookie、Host/Origin、WS失权和旁路。用户治理还须验证身份供应未知、目录外权限拒绝、最后管理员竞争、授权事务重查和审计回滚；模型网关须验证受限Key、未授权别名、真实吊销、私网/TLS旁路及正文不留日志。已有模块及隔离环境验证不覆盖新增待开发功能，完整产品安全验收未完成；结果见测试文档。

## 8 认证细节与同站请求隔离

OIDC临时浏览器Cookie为`__Host-hc_oidc`，随机256 bits、HttpOnly/Secure/SameSite=Lax、Path=/、无Domain、Max-Age=300；只存浏览器绑定随机量，不存state/code/PKCE。每次登录建立新的state/nonce/PKCE S256事务，首版同浏览器仅一个待完成登录；重开登录先按旧Cookie摘要使未消费事务失效，再写新事务和Cookie，不能只替换浏览器Cookie而保留旧绑定可用。callback验证state和Cookie后原子消费事务，再在事务外交换code并验证ID Token；提供方错误同样消费并清Cookie。交换失败重新登录，禁止重用已消费code/state。成功登录替换旧Portal会话，清临时Cookie；无需持久化提供方access/refresh Token。

PKCE verifier用AES-GCM加密，oidc_transactions独立保存pkce_nonce/key_id；使用cipher.NewGCM和显式随机12-byte nonce，不能同时用自带nonce前缀的另一封装重复拼接。各密文AAD按版本化数组`["hc-aad-v1",environment,org_id,record_id,purpose]`结构化JSON序列化；purpose区分model-key、portal-csrf、oidc-pkce、native-cookie。OIDC用部署固定组织UUID，nonce每次加密重新随机生成；错误AAD/nonce/密文拒绝解密，不返回密文内容。nonce/key_id跟随记录保存，不能从ID或时间推导。

Portal与实例同站但不同Origin，SameSite不能独立防御被攻陷的另一个实例Host。除平台challenge/handoff外，Gateway对所有代理的POST/PUT/PATCH/DELETE要求Origin精确等于该实例HTTPS Origin；缺失或兄弟Host/Portal Origin均403。WS也要求同一精确Origin。Gateway移除代理响应中的上游Access-Control-*头，除challenge外不开放跨Origin读；浏览器query中的token参数拒绝，原生交换只能由可信服务内部发起。GET/HEAD只允许固定上游验证为只读的页面/资源，不能承载业务写入；其余方法（包括CONNECT/TRACE）拒绝。受控网络不能替代这项校验；D0需验证真实DSH请求矩阵兼容。

票据期限取签发时刻+45秒、挑战期限和Portal绝对期限的最小值；挑战剩余不足5秒则403并要求重新challenge，不能返回已无效票据。每次只允许一个挑战Cookie；同浏览器同实例并发进入由 enterWorkspace 串行，新流程替换旧挑战，旧票据失效。

Portal空闲期限在每个已授权Portal请求以及DSH实际HTTP/WS业务消息活动时刷新，最多每60秒写一次last_seen_at；过期检查先于刷新，服务端ping/pong及纯保活不延长空闲期限。Gateway会话30分钟固定不滑动，到期重新走Portal交接；WS按期关闭，HTML GET导航回固定Portal实例详情，其余请求401。恢复登录只接受白名单实例详情路径，不让上游Location控制回跳。

设计依据：[OIDC授权码验证](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation)、[Go AEAD nonce契约](https://pkg.go.dev/crypto/cipher#AEAD)。以上浏览器策略由真实同站跨Origin测试验证。

私有网关可部署在受控Host的专用私有桥地址。schema2网络围栏在INPUT及DOCKER-USER中只放行批准的IPv4目标和TCP端口；其他Host新连接、同网关其他端口、未批准私网及公网目标均阻止。私有网关容器采用固定镜像、独立PG账号与数据库，TLS进程只读挂载证书材料，不挂载提供商Key、Master Key或数据库env。Docker默认跨桥直连保护不应通过放宽全局规则关闭，采用固定Host TLS入口。

个人模型网关核对属于Worker控制权限：只读取冻结路由及平台虚拟Key摘要查询的范围，不读取真实Key内容或发送提供商请求。网关可用状态最多60秒，外部变化可能在下一次核对前尚未被发现，不能宣传即时不变证明。API无网关核对写权限，失败/过期不允许收费校验或工作空间新用；虚拟Key范围被扩展后仍可由平台撤销。原始管理响应、认证头、Key、明文提供商错误均不得进入日志、审计或浏览器。

## 内置 OIDC Provider

`AuthorizationServer`、`Accounts` 与 BFF 均位于平台 Go API 中。OIDC issuer 为精确的 `HC_PUBLIC_URL/oidc`，客户端 ID/秘密为部署受控输入；只接受该客户端与 `HC_PUBLIC_URL/auth/callback`，拒绝通配、额外查询/片段、未知客户端、重复参数及未支持的协议参数。仅授权码、query 响应模式、PKCE S256 与 openid/profile/email scope；state 和 nonce 必填。发现文档只声明已实现能力。

`identity_authorizations` 保存浏览器/CSRF摘要、固定客户端/回调和协议上下文，5分钟有效、最多10次密码失败。`__Host-hc_identity` 是随机256-bit、Secure/HttpOnly/SameSite=Lax、Path=/、无Domain Cookie，成功登录清除。密码 POST 必须同源 Origin 与表单 CSRF；页面不嵌入 state、nonce、PKCE 或密码，不包含第三方资源；登录HTML的CSP仅允许同源样式与脚本，禁止内联脚本、跨源表单、frame 和 base URI；协议JSON响应继续禁止脚本。交互脚本不存储凭据或调用额外接口，仍由原生表单同源提交；密码失败只回显转义后的邮箱。登录HTML使用Referrer-Policy=strict-origin以保留Chrome表单的同源Origin，仅发送origin、不发送授权查询；其余协议响应no-referrer。未知邮箱和错误密码使用一致反馈，未知账号仍执行 cost=12 的散列比较；导入的旧散列cost可为10～14，不承诺跨所有迁移账号的完全恒时。

Redis 对每分钟来源授权请求90次、密码请求120次、Token请求120次及每账号密码请求15次分别计数，只使用连接来源、不信任任意 Forwarded 头。共享代理来源会共享来源额度；故障或冷启动保护期间认证暂不可用，不能绕过限流。请求正文上限4096字节；认证查询、密码、Cookie、授权码和Token不得写普通访问日志。

授权码随机256-bit，仅存 SHA-256 摘要，60秒有效；绑定客户端、精确回调、nonce、PKCE challenge、账号凭据版本与权限代次。密码验证后重新锁定当前账号/组织/成员资格，事务内消费挑战并签发授权码。Token端点仅接受 client_secret_basic；验证客户端后持久消费授权码，再检查 PKCE、期限、启用状态、凭据版本及授权代次。重放已用授权码会撤销其已签发 Access Token。

ID Token 使用成熟 JOSE 库 RS256 签名，包含 iss/sub/aud/iat/exp/auth_time/nonce/at_hash；内部授权代次和凭据版本用于防止重置前 Token 在 BFF 回调重新建立会话。RSA PKCS8 私钥2048～4096位、0600，独立于TLS和数据加密密钥，kid 为公钥 SHA-256 指纹；JWKS只公开公钥，重启不重新生成私钥。ID Token/Access Token均5分钟有效；Access Token为随机256-bit，数据库只存摘要，UserInfo逐次检查账号/组织/成员及版本。profile/email按请求scope返回，email_verified=false，不以邮箱证明身份。

BFF在进程内调用同一 Token 签发逻辑并用 go-oidc StaticKeySet 验证签名，避免启动时回调本机 Discovery；公开HTTPS Discovery/JWKS/Token端点仍供协议验证使用。浏览器只有平台会话 Cookie，不能将 ID Token 或 UserInfo Bearer 当作平台业务 API 凭据。退出撤销当前Portal/Gateway会话；没有独立SSO会话或 refresh_token。

原生浏览器加载、刷新登录页及提交密码前，还检查对应BFF事务的有效期和浏览器Cookie绑定，避免旧登录页生成无法兑换的授权码。失效时303重新建立登录；仅在旧事务与浏览器仍匹配时保留受控业务返回路径，否则使用默认工作台。浏览器回调认证失败也303到固定`/auth/login`，清理握手Cookie，不创建会话、不携带旧授权参数、不自动重交密码。JSON调用仍返回401；依赖故障不按失效登录循环重试。最终BFF回调的一次性state、nonce、PKCE及当前账号资格校验继续生效。

签名私钥须纳入受控备份且与数据库密码散列备份分开保护。首版更换签名私钥需维护窗口，撤销全部未完成登录/授权码及Access Token，再更新只读文件和重启API；不提供在线重叠JWKS轮换。恢复库关闭组织并撤销全部身份协议状态，重新授权后才开放。

协议依据：[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html)、[Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html)、[PKCE](https://www.rfc-editor.org/rfc/rfc7636.html)、[OAuth安全规范](https://www.rfc-editor.org/rfc/rfc9700.html)。本实现是平台单客户端授权码配置，未取得 OpenID Provider 认证。

自定义模型根是显式运维授权能力：API的模型allowlist及原生模型网关配置均须包含相同`custom_api_bases`精确根。用户输入不能扩大出站范围。HTTP仅用于固定私网/回环IP或localhost，且端口须在1024～65535范围内（包括常用8000/8080）；公网服务要求TLS验证通过。每次请求重新检查根与冻结路由，DNS解析后验证全部地址并直接拨号，禁止重定向、环境代理及上游URL覆盖。发现接口只接收只写密钥并执行Owner权限及限流，返回安全错误，不泄露上游响应、Key或凭据密文。私有目录不能通过管理员共享目录接口修改或通过其他用户的catalog_entry_id引用。

## 模型提供商账号治理边界

聚合读写始终限制org+owner，管理员无隐式跨Owner私钥权限。目录同步用hc_personal_provider_account_credential受控函数，校验Session ID/鉴权代数/当前数据库权限，只返回当前聚合的一份有效密文信封；函数不可PUBLIC执行，Worker/Gateway不得执行，API无外部密文列的直接读权限。返回DTO不得含Key、密文或Secret引用。全组密钥替换、模型增减和审计为原子事务，所选模型独立运行密钥与路由仍受冻结版本核对。删除先按同一组织模型锁检查所有非删除工作空间引用，再清空默认/撤销资格/排队清理；后台确认后才归档，页面不允许物理删除记录。

个人模型保存后的自动首检遵守与手动校验相同的网关批准、Owner可用性、未撤销凭据、45秒版本租约及每日预算。后台执行不依赖浏览器会话，但每次请求前及发布时重查当前授权、版本和可用性。当前未校验版本可无引用首检；当前启用且已通过的个人版本可无引用续检，维持有时效的能力证据，过期仍拒绝新用而不伪造成功。完成后未引用的失败版本不自动反复调用；系统审计MODEL_PROBE保留版本和安全原因码，不记录提供商原始响应或密钥。

## DSH内模型切换的授权边界

账号模型范围以运行密钥的数据库锚定路由推导，拒绝客户端指定Owner或账号作为授权来源。范围扩展只限锚定账号，使用同一主机TLS/IP出口；上游提供商Key只由网关解密并短暂使用。默认单模型探测和控制管理密钥权限保持，DSH显式账号范围才支持其他模型。每次推理核对目标的当前资格与能力，不信任DSH缓存目录；停用、归档、撤销、能力/网关证据过期均拒绝目标，其他账号和用户不受影响。限流按原密钥共用，不能通过模型切换绕过。锚定凭据失效则整个账号范围不可用，须显式重新应用可用配置。


## 外接记忆与知识库

新增 `memory:read/manage`、`knowledge:read/manage`；内置管理员和成员默认拥有本人内容权限，迁移48同步已有内置角色；自定义角色仍显式授权。每个请求再次检查当前角色、组织和Owner，工作空间范围还检查实例归属与权限。批准端点来自私有策略文件，HTTP仅显式允许私网，DNS固定、不跟随重定向、不使用环境代理。凭据和待确认操作请求使用AES-GCM及环境/组织/记录/用途AAD加密；Owner由事务内查询和复合外键校验，正文不写审计。技能包及上一版包、导入预览、外接凭据和操作回执均纳入受信密钥轮换，重加密不改变版本或待确认状态。外部请求另加时间戳HMAC，浏览器不能自行指定tenant、filter或namespace。未知删除暂停召回，未知索引保留上一可用版本，核实只能重放原始幂等请求。独立测试模型配置是私有凭据快照，平台模型撤销后应同步轮换/删除该测试配置；它不代表生产模型治理闭环。详见[实施记录](context-implementation.md)。


技能来源可由运维通过私有 `HC_SKILL_SOURCE_PROXY_FILE` 显式选择本机CONNECT代理；环境代理仍不采用。来源域名先由平台解析，所有答案均通过公网校验后，CONNECT仅传递固定公网IP:443；浏览器不能选择代理。TLS仍校验原始来源域名，代理不能豁免证书、重定向或文件限额。


## 工作台连接器（MCP）

MCP按组织和Owner隔离，管理员无隐式跨Owner读取。Bearer凭据独立加密且不读回，仅发送给本人配置的精确URL；公网 HTTPS 可由用户添加，私网/HTTP 仍须精确平台例外，更换地址不可复用旧凭据；不复用平台Cookie或其他服务密钥。检查全部DNS答案并固定拨号IP，拒绝环境代理、重定向、元数据地址及未批准私网，TLS正常校验证书。JSON/Schema、分页、响应大小、调用时间和并发均有界；拒绝远程Schema加载、递归引用及导入stdio命令。工具目录和结果不可信，仅文本呈现。调用先记录意图再出站，未知结果不重试，审计不保存参数、结果或凭据。 细节见[实施记录](connectors-implementation.md)。

外部连接器OAuth与平台登录OIDC分别管理。授权请求使用PKCE S256与有效10分钟的一次性state，绑定组织、Owner、当前Portal会话、连接版本及精确目标；回调从配置的公共源站生成，不信任请求Host。state只存摘要，verifier和动态客户端信息使用`connector-oauth`上下文加密；访问/刷新令牌使用`connector-credential`上下文加密，均参与密钥轮换。回调先消费state再交换授权码，不重试授权码，授权后不自动启用工具。

OAuth元数据、注册与Token端点分别执行出口校验、响应上限及超时限制，MCP目标的私网例外不扩大到整个授权域名。列表、导出、工作台返回URL和调用记录不包含密钥或令牌。当前连接删除只清除本地凭据及待授权事务；企查查远端撤销、待撤销状态及五类资源共享凭据属于[待实现要求](requirements.md#6172-企查查完整接入扩展交付)，不能将本地删除宣称为远端授权已吊销。
