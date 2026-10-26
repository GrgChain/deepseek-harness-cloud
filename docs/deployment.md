# DSH Cloud 部署与运维设计

版本：V0.1。已有本地 Docker 初始化、迁移、登录和常驻 Worker 命令；本机加密备份/隔离恢复已演练，正式安装包及完整恢复切换尚未完成。环境输入与数值见 [配置基线](deployment.md#8-配置与业务规则基线)，工程依赖见[实施顺序](requirements.md#9-实施顺序)，当前未完成范围见[测试验收](testing.md)。

## 1 拓扑与权限

单台受控Linux主机部署HTTPS入口、静态Portal、Go API/BFF、Go Worker/访问Gateway/模型入口、PostgreSQL和Redis；DSH计算及独立固定容量资产在Docker。API、受信Worker、访问Gateway与模型入口为四个独立Go进程，同一代码工程分别装配DB角色/网络/Secret挂载；无高可用承诺。

只有HTTPS Portal和实例域名向可信试点网络开放443；PG/Redis/DSH内网端口禁止外部与成员容器访问。不提供 Coder Agent/App/端口共享/SSH 入口，其他旁路必须封闭；模板不发布coder_app。Gateway路由可信Worker登记，公网入口严格Host白名单。

控制面预留至少4vCPU/8GiB；默认系统额度为20个实例、40vCPU/80GiB、200GiB持久存储。宿主机按实际同时运行的实例配置容量；创建预检按所选CPU/内存判断，不以旧的2CPU/4GiB构建并发单位阻断1CPU/2GiB规格。数据可用量至少覆盖承诺存储+20%余量，另有控制面/镜像/日志容量。主机不足允许收紧组织限额，不能超卖内存/存储或默默改变实例冻结规格。



## 2 配置接口

下面是完整业务实现必须支持的配置接口。API 已读取数据库/Redis、OIDC/BFF、加密主密钥、模型允许列表及维护配置；Worker 通过 HC_WORKER_CONFIG_FILE 读取已实现的执行配置。Gateway/API 通过 HC_ACCESS_CONFIG_FILE 读取受控访问配置；本地密钥环/备份配置与运维CLI已实现，本机日备份与定时器已实现，独立故障域部署待完成；实例命令接口已接入；完整运行 manifest 未批准时创建和启动被拒绝。Secret引用首版固定为只读文件路径，不能把Secret值放普通.env或前端构建变量；非秘密可通过受控环境文件提供。

| 配置 | 必填/默认 | 校验与消费者 |
| --- | --- | --- |
| HC_HTTP_ADDR | API 127.0.0.1:18080、Worker :18081、Gateway :18082 | 基础进程监听host:port；本地主机端口均大于10000，Portal 42013，PG 55432，Redis 56379 |
| HC_PUBLIC_URL | 必填 | HTTPS Portal根、无query；API/Portal |
| HC_INSTANCE_DOMAIN | 必填 | 受控实例域后缀，Wildcard DNS/TLS；Gateway/Worker |
| HC_ORGANIZATION_ID | 必填 | 固定UUID v4；所有控制面 |
| HC_AUTH_MODE | oidc | 首版只允许oidc；API |
| HC_OIDC_ISSUER / HC_OIDC_CLIENT_ID | 必填 | issuer精确等于HC_PUBLIC_URL/oidc、固定平台受众；API |
| HC_OIDC_CLIENT_SECRET_FILE | 必填 | 0600客户端秘密文件；API |
| HC_OIDC_SIGNING_KEY_FILE | 必填 | 0600 RSA PKCS8私钥，2048～4096位；API，独立于TLS/数据Key |
| HC_IDENTITY_ACCOUNTS_FILE | 账号导入时必填 | 0600初始化账号散列及可信旧主体映射；hcctl identity-import |
| HC_IDENTITY_MAPPING_FILE | 手工受控映射时 | issuer/subject/role/Workspace Owner UUID（键 workspace_owner_id）；初始化命令 |
| HC_ACCESS_CA_FILE | 可选 | API 验证实例 HTTPS 入口的额外信任 CA 文件；本地使用开发 CA，禁止跳过证书校验 |
| HC_ACCESS_CONFIG_FILE | Gateway 必填、API 可选 | 0600/0400 私有 JSON；同站 HTTPS Portal、实例域及内嵌Workspace/组织/密钥引用，API 配置必须与 BFF 一致 |
| HC_WORKER_CONFIG_FILE | Worker 必填 | 0600/0400 私有 JSON，固定 Workspace 控制身份、镜像及主机资产配置；缺失或不一致拒绝启动 |
| workspace_engine | Worker/Access 固定配置 | 必须为 embedded；不包含外部服务地址、Token 或 HTTP 开关 |
| HC_DB_DSN_FILE | 必填 | 各进程独立受限DB身份；API/Worker/工作空间Gateway/模型Gateway |
| HC_REDIS_ADDR / HC_REDIS_USERNAME / HC_REDIS_PASSWORD_FILE | 必填 | 内网ACL；API/限流 |
| HC_REDIS_TLS / HC_REDIS_KEY_PREFIX | true / hc:<env>:<org>: | TLS不能静默降级；可信同主机内网可显式false |
| HC_DATA_KEY_FILE / HC_DATA_KEY_ID | 必填 | 32-byte当前主密钥文件及版本，不在DB备份；必要可信组件 |
| HC_DATA_KEYRING_FILE | 轮换时必填 | schema_version=1，key_id→受控只读文件路径，支持读取旧密钥；当前项须与HC_DATA_KEY_FILE一致 |
| HC_RUNTIME_MANIFEST_FILE | 目标配置 | 完整 commit/npm完整性/三个镜像digest/模板/Provider/profile/适配文件/持久目录；本地 `make runtime-approve` 核对真实归档与镜像后生成0600文件并原子批准，已有契约不可静默覆盖 |
| HC_RUNTIME_CONTRACT_FILE | 本机回滚 | 0600目标Workspace模板契约文件；必须与显式目标版本、已批准manifest和Worker主镜像一致，不接受仅凭镜像标签回退 |
| HC_MODEL_ALLOWLIST_FILE | 必填 | 批准endpoint/model，不含Key；API/Worker |
| HC_STORAGE_ROOT / HC_BACKUP_TARGET | 必填 | 独立数据资产根及不同故障域备份目标；受信运维 |
| HC_DEFAULT_BACKEND | docker | 其他值启动失败 |
| HC_MAINTENANCE | false | 按接口第9节限制新命令；查询/退出/撤销/停止/删除继续 |

Worker JSON 使用 `schema_version=1`，字段为 `workspace_engine`、`workspace_organization_id`、`control_user_id`、`environment`、`data_key_file`、`data_key_id`、`model_allowlist_file`、`host`。`host` 使用 `RuntimeRoot`、`AssetRoot`、`RuntimeImage`、`StorageImage`、`NetworkImage`、`Domain`、`StorageBudget`、`PublicPort`及可选`PreviousRuntimeImages`（最多4个不同的旧镜像digest，不能含当前镜像/标签）；根目录必须当前用户所有且 0700，镜像必须为本机固定 sha256 ID。启动核验内嵌工作空间服务身份、Terraform二进制摘要/版本、固定Provider及三个当前镜像和明确保留的旧镜像，readyz同样核验；已接受任务和Worker就绪不依赖Redis。额外必填 `terraform`，包含绝对路径 `binary`、`root`、`cli_config` 和 `binary_sha256`；使用 `make workspace-init` 生成受控本机配置。`make worker-init` 从已导入本地模板及既有私有文件生成配置，不批准模板、不调整配额、不覆盖既有文件。PublicPort 未设置/0 为 HTTPS 默认 443，本地同站入口使用 42013；原生 Host、Origin、Cookie authority 与 public_url 都包含一致的非默认端口。

个人 Skill 库由 Portal 与平台 API 提供，执行迁移 46 并更新 API 角色对 `personal_skills`、`skill_import_previews`、`skill_replacements` 的权限。技能内容使用平台数据密钥加密；DB 备份及恢复须保留对应 keyring。链接导入需要 API 主机能够访问公开 HTTPS 文件来源，下载器自行校验 DNS/IP 与重定向，不使用代理；无需新增 Worker 配置、DSH 插件、镜像或容器挂载。更新平台服务时保持数据库迁移版本一致。

公开技能搜索包含 skills.sh、ClawHub、SkillHub 和 SkillsMP。SkillHub 与 SkillsMP 默认可使用匿名额度；需要密钥时，将 API 进程环境变量 `HC_TENCENT_SKILLHUB_API_KEY_FILE`、`HC_SKILLSMP_API_KEY_FILE` 分别指向模式为 0600 或 0400 的密钥文件。密钥仅由服务端读取，SkillHub 密钥只发送给 `api.skillhub.cn`，SkillsMP 密钥只发送给 `skillsmp.com`，下载跳转不携带市场密钥。SkillsMP 匿名查询存在每日及每分钟限额；前端缓存同一关键词 60 秒且失败不自动重试。网络策略须允许 `api.skillhub.cn`、腾讯 COS 技能包存储及 `skillsmp.com`；来源流量继续复用公开 IP 校验和显式配置的 Skill 来源代理。


常驻 Worker 同时运行任务执行、四维健康检查、安全扫描和自动模型探测，退出时统一取消并等待有界调用结束。扫描每10秒处理最多10个未停止实例，保留稳定分页游标，单项失败下轮循环重试。运行健康结果均使用数据库探测开始时间，过期读为UNKNOWN；路由关闭后健康刷新不能重新开放。原生Cookie的服务端续期只更新加密会话且绑定原运行代次。自动模型调度每30秒串行检查最多10个到期版本，处理同步完成的个人模型当前未校验版本，以及已完成校验且仍有引用的配置；持久attempted_at限制自动间隔4分钟，手动校验也更新该时间。模型允许列表以model_allowlist_file引用私有文件，缺失/非法拒绝启动；既有Worker配置须补入该绝对路径。模型探测可能产生费用，个人模型新增和轮换密钥后即使尚无引用也会进行首检，完成后的未引用版本不持续探测。

配额/超时/TTL采用配置基线默认值，配置文件只允许已定义字段和正数范围，启动时校验资源关系。端口/数据库角色/证书挂载由部署清单固定。master key生成/更新需受控Secret工具，禁止示例真实Key。镜像与模板一律固定digest/版本，不启动时下载。

Access JSON 使用 `schema_version=1`，含 `organization_id`、`workspace_organization_id`、`control_user_id`、`workspace_engine`、`environment`、`data_key_file`、`data_key_id`、`portal_origin`、`instance_domain`。Gateway 只读取 Worker 发布的加密原生会话；所有请求在平台PG中核验 Workspace/Build/参数归属和当前平台授权。`make access-init` 在无活跃实例时生成独立站点证书与配置，本地 Vite 开发服务或构建后的静态preview提供同站 HTTPS 和固定回环 Gateway 转发；正式入口部署清单及恢复演练仍待完成。

Portal 增量构建保留 `apps/dist` 中原有的带摘要静态资源，供已打开页面继续加载 PDF、Office 等按需模块；发布时也须保留这些资源，不能只替换 HTML 后立即清空旧资源。PDF 模块加载失败时检查当前页面与最新 HTML 的入口版本，版本变化自动刷新一次并恢复原文件预览；连续失败不循环刷新，仍提供原文件下载与手动刷新入口。

本机浏览器应信任`.local/auth/cert.pem`开发CA；只为控制台接受一次证书警告时，新实例Host的跨站fetch仍可能被浏览器拒绝。入口连接失败后保留原无opener窗口，导航到该实例`/__dsh/connect`供确认站点安全连接，然后返回控制台重新执行完整交接。该公开静态页无Cookie/挑战/票据/原生会话副作用，拒绝查询参数和非GET，严格CSP及禁止嵌入；不得把票据、密钥或工作正文带入确认URL。正式部署使用浏览器信任的DNS/TLS证书。

Worker 的 `GET /internal/preflight` 只接受回环来源、精确内部 Host 和无 query 的 GET，返回非秘密的固定域名/端口/镜像及 Workspace、Docker、容量状态。容量不足只阻断新运行接受，不阻断已接受任务的安全停止。API 只探测固定回环 Worker 18081/Gateway 18082，并用系统 CA 或 HC_ACCESS_CA_FILE 验证实例域 HTTPS；API 不挂 Docker socket、不读原卷。创建前检查另显示模板运行校验、计算容量和剩余额度，空或不完整 manifest 不能开放创建。计算按当前 MemAvailable 扣除8GiB控制面预留并以4GiB基线规格保守计槽，CPU扣除4核后按每实例2核计槽；初始批准取较小槽数，最多5，仅限制创建并发，不压低系统 CPU/内存额度。新组织使用数据库默认额度，已有保存额度保持不变。预检与Worker启动另按所选规格检查实际空闲CPU/内存，保留相同控制面余量。

## 3 安装与初始化规格

1. 校验主机、DNS/TLS、时间同步、网络隔离、磁盘与备份目标。
2. 安装锁定PG/Redis和本机Terraform/Provider，平台PG保存业务与workspace_*表；最小运行角色、Redis ACL/noeviction与内存限制。
3. 运行版本化数据库迁移，加载单组织、身份映射、批准规格/模板和运行manifest；运行账户不拥有迁移DDL权限。
4. 启动API/Worker/工作空间Gateway/模型Gateway及HTTPS入口，验证/livez、/readyz及只读预检。readyz检查PG、必要Secret及配置，创建预检另外检查Redis/Workspace/Terraform/原生适配/卷/模型。
5. 受控批准运行镜像、模板、规格和组织额度后，管理员登录“平台配置”初始化默认 CPU、内存、数字存储容量、每用户实例上限与保留期限（运行模板由后台选择）；此阶段不要求组织模型或提供商 Key。用户在“模型提供商”保存账号、设置默认模型并等待自动校验后，再执行一键创建及真实模型/文件/启停/访问验证。
6. 执行故障、配额、删除保留与独立恢复测试；满足需求验收后才开放可信试点。

已实现的本地初始化、迁移、登录和 Worker 命令见 README；本机备份/恢复/回收CLI已实现并完成隔离演练；本机日备份/定时器已完成演练，正式安装、独立故障域备份和恢复授权切换仍待完成。DSH原生适配未通过D0时预检明确NATIVE_ADAPTER_UNVERIFIED，阻止创建/进入，不允许关闭原生认证代替。

本地服务的 `make run-auth`、`make run-worker`、`make run-gateway` 和 `make run-model-gateway` 会先执行 `make local-bootstrap`。它使用本地运维身份执行迁移，只补缺失的账号组织、组织配额、工作空间身份和模板；运行环境存在时仍须通过已有源码、固定镜像和契约证据核验，随后补齐默认用户资源配置（1 核、2 GiB、10 GiB、每用户 2 个实例、保留 7 天）。首次创建默认配置时，为无已有占用的初始用户分配默认规格乘以实例数的额度。并发启动串行初始化，重复执行保留已保存的配额、默认值、账号密码和模型配置。清空本地数据库后保留 `.local`，再启动服务即可恢复基础配置；单独修复可执行 `make local-bootstrap`。直接运行服务二进制仍使用受限身份，不执行迁移或运行批准。

## 4 观测与故障处置

/livez仅进程存活；/readyz控制面依赖；/metrics仅内网。日志JSON传播request_id/trace_id/operation_id/instance_id，记录阶段/安全错误/耗时；禁Key/Token/Cookie/工作正文，HTTP服务不记录完整query/body。指标使用有限标签：操作类型/状态、依赖名/错误分类，无实例ID标签。

| 告警 | 默认触发 | 首次动作与停止条件 |
| --- | --- | --- |
| Outbox积压/操作未知 | 最老待执行>60秒；UNCERTAIN>10分钟 | 查Worker租约与Workspace Owner/name/build，不新建第二Workspace |
| PG或授权不可用 | 连续30秒 | 拒绝新写/交接，WS立即失败关闭；不沿用缓存许可 |
| Redis故障 | 命令失败或内存>80% | 停止新建/探测/交接；查询有界回源；撤销/清理继续 |
| 数据盘空间 | 可用<20%或卷达到90% | 禁止新分配并告警，保护原数据，不自动清空 |
| 原卷异常 | 单次发现 | 禁止启动、记录asset/文件系统标识，隔离恢复，不补空卷 |
| 模型失败/探测预算 | 最近探测失败或每日预算达80% | ready=false，修复配置/减少探测，禁止无限重试 |
| 撤销延迟 | WS超过30秒仍存活 | 关闭相应路由/连接、保留证据，不开放旧会话 |
| 备份过期 | 最近成功>24小时 | 立即检查备份目标/任务，恢复证据不完整不得声称RPO达标 |

每个Runbook需记录安全可执行命令、预期结果、残留资源及回退条件；运维命令以部署版本提供的hcctl为接口，规格见第7节。不能通过人工UPDATE operation=SUCCEEDED或删除Outbox伪装修复。

## 5 备份恢复与保留

| 对象 | 策略 | 验证 |
| --- | --- | --- |
| 平台PG | 每日一致性逻辑备份，7个日备份，独立故障域加密 | 空库恢复、约束/Owner/配置头/账本一致 |
| 工作卷 | 每日停止/冻结后快照，7个日备份 | 原资产标识、文件SHA-256、已刷盘会话可读 |
| 主密钥 | 独立受控Secret备份/恢复演练 | DB/卷备份无权取得主密钥 |
| 审计/日志 | 90/14天，法律保留优先 | 不含秘密/正文，受控归档不可由普通服务改写 |
| 软删除卷 | 至少7天，法律保留优先 | 到期不自动删除；独立运维核验后回收 |
| Redis/短期会话 | Redis可重建；恢复后会话全撤销 | 不恢复旧授权，不以Redis备份代替业务备份 |

DB与已刷盘工作数据目标RPO≤24h/RTO≤4h。保留原卷不等于备份。备份一致性需要停止DSH、等待30秒正常退出/刷盘后复制，期间显示维护/暂停，不能承诺热备零影响。恢复时先隔离网络，将备份解包到同Owner、正常创建并停止的另一目标实例资产，校验备份来源/Owner/版本/校验和及模型凭据状态；目标和原保留资产分别占用真实额度，额外容量不足先收紧新分配并由受信运维扩容，不通过覆盖原卷绕开额度。恢复命令仅在两实例无活跃操作/无挂载计算且目标非原asset时执行，保留目标原有数据的隔离副本，经确认后写入恢复内容并以恢复授权重写目标asset初始化标记；记录source/target/备份校验和，重新注入目标配置并撤销目标平台/原生会话，通过检查后启用目标路由。原实例保留且禁止自动改绑定。

PG恢复到旧时间点后须全量对账Workspace/卷，按真实存在资源保守计账；不自动释放旧快照漏记的额度、不重新创建同名Workspace。外部孤儿资源先隔离登记并审计。

## 6 维护、升级与回滚

维护模式禁止新创建/启动/configure、模型保存/validate与defaults更新；retry仅允许STOP/DELETE，允许查询、登录/退出、撤销、停止/删除。暂停非安全新任务领取，正在执行的外部动作对账到可控状态。升级前备份，锁镜像/模板/Provider/DSH，迁移expand→migrate→contract，旧实例不隐式换模板/配置。

回滚先验证数据格式/迁移/凭据兼容，只回退兼容镜像；破坏性数据变更前向修复或从验证备份隔离恢复。撤销凭据不会因回滚恢复使用。DNS/TLS/认证变更先隔离实例实测。

## 7 运维命令接口

已实现命令使用独立运维数据库账户，普通服务不拥有备份、重加密、DDL或物理回收权限。`restore`、`storage-restore`、`storage-reclaim`、`secrets-reencrypt`、`recovery-inventory`、`audit-archive`、`audit-hold` 默认只预览；实际执行需要 `--apply`，并声明 `HC_MAINTENANCE=true`。生产资产变更前须协调 API/Worker 维护状态；隔离数据库恢复不连接生产进程。

| 命令 | 必需输入 | 行为 |
| --- | --- | --- |
| hcctl backup | --output、新独立备份密钥、操作者 | 一致性逻辑备份，密文文件排他创建，输出SHA-256并追加审计；支持已知旧schema的升级前备份 |
| hcctl backup-daily | --directory 专用0700本机仓库 | 默认预览；--apply须维护状态，全部实例STOPPED/DELETED且无活动任务，完整加密DB/原卷备份组校验后登记完成；保留最近7个完成日，法律保留/失败阻止清理 |
| hcctl recovery-retain | --target-dsn-file、--confirm-database、--inventory-run、--reason-code | 隔离恢复库的已审查核对记录，默认预览；--apply须维护，仅增加真实保留额度并原子审计，不解除隔离/不更改外部资源 |
| hcctl recovery-adopt | --target-dsn-file、--confirm-database、--metadata-dsn-file、--confirm-metadata-database、--instance、--inventory-run、--reason-code | 从独立隔离检查点接纳已停止的原资源；默认预览，--apply须维护；原实例/操作/预留入库与移除对应保留同事务，总计额不变，授权继续隔离 |
| hcctl recovery-fence-source | --source-dsn-file、--confirm-source-database、--target-dsn-file、--confirm-database、--inventory-run、--reason-code | 已对账目标保持隔离；默认预览，--apply须维护；原子撤销旧来源授权后关闭旧服务连接，同一意图可继续连接清理 |
| hcctl dev-runtime-rollback | --from-template-version、--to-template-version、--reason-code | 本机hc_dev兼容运行版本切换，默认预览；--apply须协调维护及运行Worker配置一致，只选择已批准保留版本，原快照/卷/凭据不改 |
| hcctl restore | --input、--target-dsn-file、--confirm-database | 只允许空的hc_recovery_或hc_test_recovery_库；完整验证后单事务恢复和隔离，拒绝覆盖现有库 |
| hcctl recovery-inventory | --target-dsn-file、--confirm-database | 只核对已隔离恢复库；默认只读预览，--apply原子登记完整差异与保守容量，不修改外部资源或开放组织 |
| hcctl storage-backup | --asset、--output、受信Worker配置 | 只备份已停止/软删除保留的原盘；核对卷/计算/挂载/Owner，独立加密保存 |
| hcctl storage-restore | --input、--asset目标、--preimage、--confirm-asset | 同Owner的另一已停止资产；先备份目标原数据，持久RESTORING，再校验/重绑目标标记/原子替换；失败可按同一摘要继续 |
| hcctl storage-reclaim | --asset、--reason-code、--confirm-asset | 核对软删除至少7天、保留到期、无法律保留/活动操作/挂载/孤立loop；物理确认后释放存储额度 |
| hcctl secrets-reencrypt | --from-key、当前/旧密钥环 | 每批1～500条，最多10000批/次；原AAD、条件更新和审计同事务，失败可继续，不复活撤销记录 |
| hcctl cleanup | --batch 1～500 | 默认回滚事务预览；--apply清理到期临时记录，不删实例/操作/账本/审计 |
| hcctl audit-archive | --batch 1～500、--output、--confirm-org | 超过90天且无法律保留；加密回读校验并持久发布后，在同一锁表事务删除原行、恢复trigger和追加维护审计 |
| hcctl audit-hold | --hold-state active/released、--reason-code、--confirm-org | 组织审计法律保留；默认预览，变更和审计同事务，不解除资产保留 |
| hcctl identity-apply | 受控映射文件/操作者 | 推进身份代次和审计，禁用触发安全停止 |

加密备份格式为HCBK0001：独立32-byte密钥、随机256-bit salt，经HKDF-SHA256派生每份归档的AES-256-GCM密钥；序号、长度、类型和元数据均认证，必须有最终长度/SHA-256，拒绝截断、换序、拼接和损坏。单份明文流上限128GiB，按64KiB分帧；数据库custom dump和压缩完整工作盘都走同一格式。备份密钥不得复用当前或旧数据主密钥；所有输入密钥/连接文件0600，备份和临时恢复目录0700，文件0600，成功/失败均清除本次私有临时明文。工作盘恢复需要与目标盘容量相同的额外临时空间，仍保留控制面和存储余量。

本地命令示例，先在仓库根目录构建CLI并设置受控文件引用：

```bash
go build -o .local/hcctl ./services/cmd/hcctl
export HC_DB_DSN_FILE="$PWD/.local/migrator-dsn"
export HC_WORKER_CONFIG_FILE="$PWD/.local/worker-config.json"
export HC_DATA_KEY_FILE="$PWD/.local/auth/data-key"
export HC_BACKUP_KEY_FILE="$PWD/.local/auth/backup-key"
export HC_ORGANIZATION_ID="$(cat .local/auth/organization-id)"
export HC_OPERATOR_REF=local-operator
.local/hcctl backup --output "$PWD/.local/backups/platform-new.hcbk"
.local/hcctl cleanup --batch 500
.local/hcctl storage-backup --asset SOURCE_ASSET_UUID --output "$PWD/.local/backups/source-new.hcbk"
.local/hcctl storage-restore --input "$PWD/.local/backups/source-new.hcbk" --asset TARGET_ASSET_UUID --preimage "$PWD/.local/backups/target-original-new.hcbk"
.local/hcctl storage-reclaim --asset EXPIRED_ASSET_UUID --reason-code RETENTION_EXPIRED
```

UUID占位符必须替换为已核实资产。默认预览没有写入恢复/回收；实际执行加精确 `--confirm-asset` 和 `--apply`。每次备份使用新文件名，禁止覆盖或把密钥放进同一备份目标。源码不提供真实备份密钥；需以密码学随机源生成并独立保管。本机 `.local/backups` 的单次归档及 `.local/daily-backups` 的日备份组均用于本机演练，不构成不同故障域。

`backup-daily`使用专用0700目录，初次只绑定空仓库到精确组织/数据库；未知文件、符号链接、错误身份或损坏旧归档均拒绝，不能把普通备份目录自动接管为清理目标。完整组含数据库custom dump、所有仍计账的原盘密文及加密认证索引；备份前后核对实例/资产/额度/模型/默认配置/身份版本摘要，变化或跨UTC日期不发布完成组。各归档全量回读认证、文件/目录fsync和完成审计成功后才把pending目录发布为当日完成目录。同日重放复用该组，未完成pending密文保持供诊断，不计入成功日；当前不自动清理失败组。

保留最近7个不同UTC日期的成功组，不补造缺失日期。清理前核验所有完成组、至少保留7份，并在PG锁下重新检查组织/资产法律保留；记录清理接受，重命名为pruning标记后只删除认证索引列出的文件，最后写结果审计和移除标记。中断可以继续，无法验证的组阻断删除，其他文件不删除。法律保留期间允许新增备份但不清理，显式解除后才按规则继续。

本机`make local-backup-timer`生成只含路径/安全元数据的0600环境文件及用户systemd服务，拒绝覆盖不同配置；每日03:00 UTC（北京时间11:00）尝试执行，Persistent补跑漏过的调度。用户manager须持续运行，需核对`loginctl show-user "$USER" -p Linger`；本机已启用linger。`systemctl --user start hc-local-backup.service`可手动演练，`systemctl --user show hc-local-backup.service -p Result -p ExecMainStatus`和timer下一执行时间用于检查。服务不自动停止用户实例、不改变API/Worker维护配置；运行/活跃任务时失败并保留旧备份，受信维护人员须另行协调可用窗口。首个真实DB/两个盘日备份及同日定时服务重放已通过，七份保留/法律保护/损坏与失败分支通过模拟跨日及PG测试；真实连续7日、RPO≤24h/RTO≤4h和独立故障域尚未证明。

数据库恢复在同一提交中禁用组织、撤销全部Portal/OIDC/原生访问、禁用路由、清除健康并围栏旧租约，撤销所有恢复的模型凭据，防止旧快照复活其后发生的撤销；恢复当日探测预算保守占满。还会撤销PUBLIC函数执行/Schema访问，以及API/Worker/工作空间Gateway/模型Gateway的目标库CONNECT。随后在隔离库迁移至当前schema，全量对账内嵌Workspace/数据盘/孤立资源，保守计账、核实原密钥与Owner、重新保存/校验模型和显式应用配置、重新授予最小角色权限，完成验证后再恢复服务。外部资源只读核对与隔离登记已实现；核对后的保留计额已实现；可信检查点接纳与保留额度转换已实现；未知元数据资源处置、生产授权/切换和控制面版本回退仍待实现/演练；本机日备份、定时器和七份保留保护已实现；审计归档与失败回滚已实现并通过真实PG验证，禁止直接修改组织状态或单个operation伪装恢复成功。

`hcctl recovery-inventory --target-dsn-file /private/recovery-dsn --confirm-database hc_recovery_example` 必须指定精确隔离库，拒绝服务库、启用组织、仍有有效会话/凭据/路由或运行角色可连接的目标。核验内嵌Workspace组织与控制主体，读取平台库内全部未删除Workspace，结合主机原盘manifest/文件系统UUID、Docker卷/容器/网络的归属元数据。最多1000个实例、5000条资源观察；两次外部扫描及提交前数据库快照必须一致，资源变化/观察不完整/重复标识均拒绝登记。扫描只读，不挂载工作盘、不读工作正文或秘密通道、不发送模型请求；它不构成文件内容完整性或已删除Workspace历史的证明。

默认仅预览；加`HC_MAINTENANCE=true`及`--apply`时，把完整差异、原账本与保守容量和操作者作为一条追加审计原子保存；相同摘要重放返回原记录，审计失败不留下登记。逻辑盘与已核验同身份Docker卷去重，不同盘分别计量；缺失资源保留旧额度，Owner/标识异常不能推断可回收，残留计算保留相应额度。`conservative`是隔离恢复核对表中的占用下限，原quota_reservations不变；不能把该表当作已完成账本迁移。即使blockers=0，promotion_allowed也始终为false；随后须通过recovery-retain补记保留额度，再在真实维护窗口完成差异处置、资源正式接纳/解除保留、凭据重新配置和独立授权切换。该命令不会停止或删除其他服务环境的资源。已在旧备份隔离库验证实际新增实例/工作盘/Workspace被识别，保守容量从10GiB核对为20GiB，正在使用的环境保持不变。

核对后保留计额使用`hcctl recovery-retain --target-dsn-file /private/recovery-dsn --confirm-database EXACT_DATABASE --inventory-run REVIEWED_RUN_UUID --reason-code RECOVERY_RETAIN`，默认预览；协调维护、保存隔离库加密备份后加`--apply`。仅接受单组织DISABLED、RESTORE_ISOLATED成功记录、模型/平台/原生会话均撤销及服务CONNECT已撤销的hc_recovery_/hc_test_recovery_库。必须引用已持久登记的精确核对记录，重新扫描内嵌Workspace/主机两次且稳定，核验数据库Owner映射；未核实/归属冲突/错误组织/变动资源/活动数据库操作拒绝，不能指定另一个Owner绕过归属检查。

受信短事务锁定身份、实例、资产、配额及恢复保留表，重新检查隔离及捕获状态；按真实占用只增加缺额，保留原实例UUID但不伪造instances/operations，不修改原预留账本、不停止/删除/挂载外部资源。保留额度与RECOVERY_LEDGER_RETAIN同事务提交，迟写审计失败全撤回；相同核对ID/操作者/原因及状态摘要重放复用已有记录。统一quota_commitments视图使预检和创建/启动都计入保留资源，包括本人实例上限；超过上限也继续记录真实占用，不能通过增大配额隐藏债务。

本机旧备份已实际演练原账本1实例/10GiB及核对发现的新增资源，经保留计额后统一账本2实例/20GiB；原实例/卷/旧预留/凭据及正常服务环境不变，重复执行无重复计额。组织持续DISABLED、模型持续REVOKED、服务CONNECT持续撤销，promotion_allowed仍为false。保留计额只登记容量债务；可信检查点接纳可转入正常账本，未知元数据资源处置、物理缺额释放和独立授权切换仍待实现/验收。存在未处置恢复保留额度时backup-daily拒绝发布完整日备份，避免遗漏尚无平台备份来源的孤儿盘；独立数据库加密备份仍可保存恢复进度。

具有可信原始元数据的资源使用`recovery-adopt`接纳。先用`restore`把覆盖该实例的已认证加密数据库备份恢复到另一空隔离库，并迁移至当前schema；两库都必须保持DISABLED、会话/凭据撤销及服务CONNECT撤销，名称和连接配置明确不同。不得以正在服务的数据库作为元数据来源。缺少覆盖该实例的可验证检查点时，继续保留计额，不人工拼接快照或伪造成功创建记录。

命令示例：`hcctl recovery-adopt --target-dsn-file /private/recovery-dsn --confirm-database hc_recovery_target --metadata-dsn-file /private/checkpoint-dsn --confirm-metadata-database hc_recovery_checkpoint --instance ORIGINAL_INSTANCE_UUID --inventory-run REVIEWED_RUN_UUID --reason-code RECOVERY_ADOPT`。精确核对记录须反映已补记的保留额度。核验原Owner的OIDC issuer/subject/Workspace Owner映射、原UUID/稳定名称/Workspace、固定配置/模型版本/规格、原盘及原CREATE成功记录和预留ID；从实际不可变镜像/helper和平台模板源码重新验证批准。两次扫描必须稳定，目标实例尚未登记，仅有同Owner的INSTANCE=1/STORAGE=10GiB保留；实际资源恰为同身份ASSET/VOLUME/已停止Workspace，无计算/网络或额外盘。

默认预览；保存目标加密备份并协调维护后加`--apply`。短事务锁定两库并重检元数据/归属，来源只读；目标导入原实例/原资产/终态操作及原预留。实例访问代次和导入操作租约代次推进；新模板/规格只登记为禁用，原默认项和批准开关不改。新增模型/秘密仅保留REVOKED状态，已存在撤销密文即使曾重加密也逐字节保留；模型头只单调推进，避免后续保存撞版本。

原USED预留插入、对应恢复保留移除与RECOVERY_RESOURCE_ADOPT审计同事务提交，晚写失败全撤回；统一计额前后相等，不能把转换视为释放物理容量。精确重放须操作者/原因/检查点/原核对ID及数据库/外部摘要一致，复用原记录；后续真实变更使旧意图失效。接纳后重新核对，原资源应MATCHED；即使零差异，promotion_allowed仍为false，不授予CONNECT、不启用组织/路由、不更改原服务环境。本机已验证两实例/20GiB正常账本、恢复保留0、差异0及精确重放单审计。未知元数据资源的受控处置、物理缺额释放和独立授权切换仍待后续流程。

恢复切换前的来源授权隔离使用`recovery-fence-source`。先核验目标已完成资源接纳、恢复保留为0、核对差异为0且正常账本等于真实保守占用；目标仍须满足完整恢复隔离。来源和目标必须为两个精确指定的不同数据库，原实例/资产/冻结运行配置/额度及Owner的OIDC/Workspace Owner身份一致，全部实例STOPPED/DELETED，无活跃操作、计算预留、启用路由或存储维护。两次内嵌Workspace/Docker/原盘扫描及两库提交前状态都须稳定，引用目标最新已持久登记且摘要一致的核对记录。来源维护账号须具有数据库/业务表撤销权限及`pg_signal_backend`能力或superuser权限；四个服务角色不得通过继承授权保留来源CONNECT。

预览示例：`hcctl recovery-fence-source --source-dsn-file /private/source-dsn --confirm-source-database EXACT_SOURCE_DATABASE --target-dsn-file /private/recovery-dsn --confirm-database hc_recovery_target --inventory-run REVIEWED_RUN_UUID --reason-code RECOVERY_SWITCH`。连接配置文件0600，受信Worker配置与操作者按本节加载。预览不撤销授权、不关闭连接、不写审计；返回`applied=false`。实际执行须另行协调完整切换窗口，保存两库加密备份，并设置`HC_MAINTENANCE=true`及`--apply`。

来源短事务一次性禁用组织、推进成员/实例访问代次、撤销Portal/OIDC/交接/原生会话和全部模型/秘密、清除健康和探测租约、撤销PUBLIC及API/Worker/Gateway的库CONNECT，并追加RECOVERY_SOURCE_FENCE。审计失败全部回滚，原连接不提前关闭；原UUID/冻结快照/工作盘/额度/加密密文和每日模型请求预算保持。事务提交后才关闭四个服务角色在来源库的实际连接，确认连接清零后追加RECOVERY_SOURCE_DRAIN，成功报告同时含`applied=true`、`drained=true`及两条审计ID。目标始终只读隔离，不启用组织/模型、不给CONNECT、不创建新入口。

若命令在来源提交后中断或连接清理/其审计失败，来源撤销已经生效；错误退出不能推定全程回滚。核对来源的RECOVERY_SOURCE_FENCE/RECOVERY_SOURCE_DRAIN，使用相同操作者、原因码、来源/目标及核对ID重试，完成未结束的连接清理；精确重放不再次推进代次或重复审计，改变意图/业务状态须重新评估。禁止单独恢复来源CONNECT或组织状态作为失败补偿。

来源隔离已通过独立PG17测试库的真实API登录连接关闭/重新连接拒绝、提交失败全回滚、提交后中断及清理审计失败后继续；实际本机服务库仅完成只读预览，授权和服务保持。目标的重新授权、模型重新保存/校验与显式配置应用、最小角色授权和三进程连接切换尚未实现，当前不对正在服务的库执行`--apply`。这些后续步骤具备独立验证后，才能安排完整本机恢复切换。

软删除实例的原盘/卷继续计入STORAGE；已释放INSTANCE时，核对不会仅因保留盘重新增加实例额度。未知归属盘及残留Workspace/计算/网络仍按保守资源规则阻断并计额，物理容量释放继续走受保护回收。

兼容运行镜像升级使用本地受信命令 `hcctl dev-runtime-upgrade --from-template-version PREVIOUS_VERSION`，默认仅预览。先停止新变更、等待已接受操作终态，所有实例必须确认STOPPED/DELETED且计算额度已释放；备份数据库和原受控配置，构建并验证新不可变镜像、发布新的不可变平台模板版本，Worker配置保留旧digest于PreviousRuntimeImages，完成运行/模型/存储/网络/Workspace/Gateway真实契约。为新manifest指定新的HC_RUNTIME_MANIFEST_FILE；预览通过后在协调维护状态下加`--apply`。命令核验hc_dev、精确旧模板版本、旧镜像保留、相同存储/原生/配置格式，无活跃操作或存储维护，单事务禁用旧批准、插入新批准、递增默认版本并审计；失败整批回滚，精确重放无副作用。已有实例快照、原卷、凭据引用和旧批准均保留，只有未来创建使用新版本；旧实例启停继续使用旧digest。此命令不支持跨DSH包/文件格式的不兼容升级，也不自动迁移旧实例。

兼容运行回滚使用`hcctl dev-runtime-rollback --from-template-version CURRENT_VERSION --to-template-version RETAINED_VERSION --reason-code COMPATIBILITY_REHEARSAL`，默认预览；`HC_WORKER_CONFIG_FILE`指定目标Worker配置，`HC_RUNTIME_CONTRACT_FILE`指定保留的目标模板契约。操作只接受hc_dev和已批准版本，核验内嵌Workspace Owner、组织、版本构建及三文件源码、两个非秘密变量、Provider锁、镜像内六文件摘要/npm完整性和两个helper；旧镜像按其批准manifest验证，不要求旧适配文件等于当前源文件。平台模板活动版本可保持当前版或目标版，平台始终显式固定构建版本。

执行前保存加密数据库备份和当前受控配置，协调API/Worker维护并停止实例；把Worker主镜像改为目标digest，将原当前版及其他已有实例的digest保留在PreviousRuntimeImages，重启Worker后再加`--apply`。命令同时核验运行Worker返回的主镜像、保留镜像列表、helper、域名/端口及Workspace/Terraform/Docker/存储检查，防止仅编辑配置文件而未实际部署；全部非删除实例的固定镜像必须仍受支持。该元数据切换不分配计算资源，可用计算不足仍由创建/启动预检拒绝。数据库短事务拒绝状态不一致、未停止实例、ACCEPTED/RUNNING/UNCERTAIN操作、未释放计算和存储维护；同事务切换两个批准标志、递增defaults版本并追加RUNTIME_ROLLBACK，审计失败全部撤回。精确重放要求已存在相同来源/目标/操作者/原因的成功审计，不增加默认版本或重复审计。

回滚后完成登录、预检和两代实例启停/进入/原数据检查，再退出维护；API和Gateway重启或限流连接恢复有120秒保护期，就绪HTTP成功不足以证明昂贵操作已经可接受。演练返回已保留的新版同样走显式from/to和原因码的受控命令。该流程只切换未来创建的运行默认值；控制面二进制、数据库降级、破坏性格式变更和独立故障域恢复仍须各自部署/恢复验收。

运行profile为普通浏览器任务设置默认输出上限4096 tokens、关闭默认思考、每步最多1次上游重试及30秒流空闲超时；模型catalog和provider采用相同默认上限。该值是单次模型调用的默认值，不是任务总费用预算。模型连接校验仍使用独立的每请求128 tokens和每轮最多2次限制。测试插件只用于显式隔离契约，禁止注入正式profile。

密钥轮换先生成独立新主密钥，0600密钥环格式为 `{"schema_version":1,"keys":{"old-id":"/private/old-key","new-id":"/private/new-key"}}`。API使用HC_DATA_KEYRING_FILE；Worker/Gateway在私有JSON中使用可选data_keyring_file。全部角色先部署可读旧/新且新写使用new-id的配置，再在维护窗口执行 `secrets-reencrypt --from-key old-id` 预览和 `--apply`，核对remaining=0并完成旧备份恢复演练后才退休旧密钥；旧备份仍可能需要独立保存的旧密钥。正常服务不得读取备份密钥。

## 8 配置与业务规则基线

以下为开发默认值和校验规则，可通过受控部署配置收紧。放宽容量必须通过预检；修改规则须同步需求、OpenAPI 和测试。数值是设计目标，尚未实测。

### 8.1 标识与输入

所有平台实体 ID 使用小写 UUID v4，无业务前缀；Workspace/模板/Build ID 单独保存，字段名为 workspace_owner_id、workspace_name、workspace_id。示例 UUID 为 `11111111-1111-4111-8111-111111111111`。时间使用 UTC、RFC3339，数据库 timestamptz；配额 CPU 为整数 millicores，内存/存储为整数 bytes，展示换算 GiB=2^30 bytes。

实例显示名称使用1–32个字符，支持中文、字母、数字、下划线和短横线，组织内活动名称唯一，首次自动生成 `dsh-` 加实例 UUID 去连字符后的前 12 位，冲突返回明确冲突、不改变外部标识。名称初始版本不可修改。Workspace 名称为 `hc-` 加 UUID 完整128位的小写无填充Base32（29字符，满足32字符上限），外部 create key 为实例 UUID。模板/规格引用必须等于批准值，拒绝未知字段、空字符串、null 与任意 Owner/镜像/挂载。

HTTP 管理请求体上限 64KiB；模型 api_key 只接受可见ASCII、长度 1～4096 bytes，仅专用写入接受。endpoint 不含用户信息、query、fragment，必须匹配批准 HTTPS 根；当前 DeepSeek Messages 根为 `https://api.deepseek.com/anthropic`，provider=`deepseek-official`，protocol=`deepseek-messages`。REQ-16使用provider=platform-models、protocol=anthropic-messages，endpoint为批准网关根，由固定适配器追加/v1/messages，api_key为受限虚拟密钥。模型ID/网关别名由管理员在部署批准列表中选择，不由成员任意输入；切换连接类型必须提供匹配用途的新凭据，不能隐式复用直连Key。

### 8.2 配额与容量

| 项目 | 默认值 | 计量规则 |
| --- | --- | --- |
| 创建资源规格 | CPU 1000/2000/4000/8000/16000 millicores × 内存 2/4/8/16/32/64 GiB；默认 2000 millicores、4 GiB；数据盘默认 10 GiB、管理员可设 1–100 GiB 整数，PID 256 | 选择批准组合，按实际金额计额；Worker 启动前核对 cgroup 限制；每个实例冻结数据盘硬容量，修改默认值不扩缩原盘 |
| 组织活动实例 | 20 | 包括创建/错误/停止；确认软删除才释放 |
| 每用户活动实例 | 2 | 同上；不随停止释放 |
| 组织运行/预留计算 | 40000 millicores、80GiB RAM | 创建/启动原子预留；停止确认才释放 |
| 组织存储 | 200GiB | 已分配/预留/保留卷均占用，真实回收才释放 |
| 同时创建/启动 | 2 | ACCEPTED/RUNNING/UNCERTAIN 均占槽 |
| 主机余量 | 控制面至少 4 vCPU/8GiB；数据盘可用量超过承诺存储 20% | 不足禁止新建，已有数据保护优先 |

卷采用独立固定容量块设备或文件系统硬配额，见 [运行适配](backend.md#124-存储与模板契约)。普通无限容量 Docker 卷不满足存储规格。到达盘容量返回可识别的存储错误，禁止自动删除用户文件。

### 8.3 超时、租约与重试

| 项目 | 默认值 | 规则 |
| --- | --- | --- |
| API 普通请求 | 10 秒；模型校验 35 秒 | API 超时不取消已经提交的持久命令 |
| Workspace 元数据调用 | 3/15 秒 | PostgreSQL提交结果未知时按稳定标识重查；Terraform执行上限120秒 |
| 操作租约/续租 | 60/15 秒 | 过期执行器不得回写；领取后先检查外部资源 |
| Worker 领取/对账 | 每 2 秒最多 10 项；对账每 10 秒 | 外部调用在事务外；禁止无界并发 |
| 外部读重试 | 最多 3 次，1/2/4 秒全抖动上限 | 仅无副作用读，遵守总context预算；写操作通过对账恢复 |
| PG短事务 | 总计5秒、statement 5秒、lock 1秒、idle in transaction 5秒 | 失租/超时回滚；可恢复事务最多重试2次，见后端13.2 |
| 模型探测租约 | 45秒 | 与30秒模型总预算分离，发布检查token/期限/未撤销 |
| 执行并发 | 普通2项、安全STOP 1项、健康检查2项 | 独立有界执行槽；只为已开始执行任务领取租约 |
| 进程退出 | API35秒、Worker30秒、管理器45秒 | 停新请求/领取、关闭WS；未决任务重启先对账 |
| Gateway Workspace授权检查 | 每HTTP请求/WS握手及10秒周期检查，总超时2秒 | 核对实际Owner/主体状态；不使用普通15秒编排超时或缓存旧许可 |
| 操作等待预算 | 10 分钟 | 已确认外部失败才 FAILED；仍未知保持 UNCERTAIN 并告警 |
| 幂等 Key | 16～128 个 ASCII 字母/数字/下划线/连字符 | 同作用域唯一；客户端推荐 UUID v4 |
| 幂等期限 | 终态后至少 7 天；活跃操作不清理 | 首次响应/快照稳定；过期键再次使用视为新命令，客户端禁止复用 |
| 控制台轮询 | 2 秒；传输错误退避至 10 秒 | GET 最多 2 次重试；写入不自动重试 |

幂等作用域 `(org_id, actor_id, action, target_id, key)`；CREATE 的 target_id 固定为组织 ID。请求先严格解码、拒绝重复 JSON 键/未知字段，按字段名递归排序、紧凑 UTF-8 JSON、整数无浮点、字符串不裁剪再 SHA-256；包括确认名称和显式版本，省略字段保持省略。首次解析默认项另存快照，不加入哈希；因此默认项变更后的 `{}` 重放仍返回原操作。

### 8.4 健康、模型和限流

| 项目 | 默认值 | 持久与失败行为 |
| --- | --- | --- |
| 应用/存储/配置/访问健康 | 每30秒加0～4秒稳定抖动，四项共享5秒总预算，开始时间+60秒失效 | 元数据写 PostgreSQL；原卷读写仅受控探测目录 |
| 模型校验 | 总计 30 秒、最多 2 次请求、每次输出上限 128 tokens | 仅合成文本与无副作用工具 schema；非瞬态错误不重试 |
| 模型校验有效期 | 5 分钟 | 版本绑定；到期 UNKNOWN，可授权诊断 |
| 自动模型探测 | 按配置版本合并，每 4 分钟最多一次 | 每组织每日最多 1000 次上游请求，PostgreSQL 按配置头→validation→预算锁序预留次数；拿锁后复核lease，失败不退款 |
| 创建/启动请求流量 | 每用户 10 次/分钟 | Redis 原子固定窗口，含重放；业务配额独立 |
| 模型校验流量 | 每管理员 5 次/分钟 | 同时一项/配置版本；持久每日次数仍约束 |
| 访问票据流量 | 每用户 30 次/分钟 | 无法确认限流时拒绝新交接 |
| 脱敏元数据缓存 | 15 秒；单值 ≤64KiB；合计最多 10000 项 | 不缓存授权决定、秘密或含 can_enter 的跨用户响应 |
| Redis | 256MiB、noeviction；命令超时 200ms、无命令自动重试 | Lua 原子计数与首次设置 TTL，窗口 60 秒、键 TTL 120 秒 |

模型健康在配置版本上共享探测结果，但实例还必须确认实际注入版本；创建时仅复用有效校验不重复计费。持续上游错误立即使相应模型健康 UNHEALTHY。预算用尽停止自动探测并告警，不阻止用户自有已运行的对话，也不宣称控制全部 DSH 使用费用。这里限制的是平台探测，不增加计费产品。

Redis首次启动、run_id变化或连接重建后的限流状态不可确认时，新建/启动/计费探测/票据入口冷却120秒；持久预算仍检查。停机/删除/撤销与已接受任务不受冷却阻止。多个API副本各自保守冷却，不能用Redis重启清空计数后立即放宽流量。

每个进程的限流器保留单条Redis连接，禁用客户端正常空闲回收；安静窗口不应人为启动新的恢复等待。真正断连仍触发原120秒保护，依赖不可用继续拒绝高成本入口。进程readyz成功与限流恢复窗口结束分别核验。

匿名浏览器挑战创建同样限流：受信入口解析的来源IP每分钟30次、组织每分钟300次，Redis不可用/冷却时拒绝，挑战仅针对已登记运行路由。短期身份/访问数据每5分钟、每批最多500条按[数据清理规则](data-model.md#9-保留清理与数据库权限)处理，禁止无限累积。来源IP只信任固定反向代理链，客户端转发头不参与限流键。

### 8.5 会话与数据保留

| 项目 | 默认值 | 约束 |
| --- | --- | --- |
| Portal 会话 | 8 小时绝对期限、30 分钟空闲 | HttpOnly/Secure/SameSite=Lax、host-only、Path=/ |
| OIDC state/nonce/PKCE | 5 分钟、一次使用 | 登录回调精确 URL；重放拒绝 |
| Gateway 挑战/票据 | 60 秒 / 45 秒 | 随机 256 bits；绑定实例/Host/浏览器挑战/主体代次 |
| Gateway 会话 | 30 分钟；不得超过 Portal 会话到期 | host-only、HttpOnly/Secure/SameSite=Strict、Path=/ |
| HTTP/WS 撤销检查 | 每请求查 PostgreSQL；WS 每 10 秒 | DB 查询 2 秒超时立即关闭；撤销目标 ≤30 秒 |
| 软删除卷 | 默认 7 天，可配置 1–365 天 | 永久回收另须软删除至少 7 天、期限到达且无法律保留，不自动清空 |
| 审计/普通日志 | 90 天 / 14 天 | 不含秘密或工作正文；法律保留优先 |
| DB 与工作卷备份 | 每日、保留 7 个日备份 | 独立故障域、加密、校验；统一 RPO≤24h/RTO≤4h 目标 |

Cookie 名称 Portal=`__Host-hc_session`、Gateway=`__Host-hc_access`、挑战=`__Host-hc_challenge`。不设置 Domain，禁止父域 Cookie。原生 DSH Cookie 仅保存在可信 Gateway 的加密记录中，不传浏览器。

### 8.6 部署输入

必须提供 HTTPS Portal URL、实例域名后缀/通配 TLS、固定组织 UUID、OIDC issuer/client/secret/允许主体列表、主体→Workspace Owner UUID 映射、内嵌Workspace控制主体、固定Terraform二进制/摘要/Provider镜像目录、PostgreSQL DSN、Redis ACL、32-byte 数据加密主密钥文件、镜像 digest/上游 commit/模板版本/Provider 锁、模型允许列表、独立数据盘和备份目标。

模型允许列表最多100个ID、16个批准endpoint；组织保留一个默认连接，个人最多20个未归档外部模型连接；个人默认或显式选择只影响本人的DSH。支持DeepSeek直连与平台模型网关 Messages网关。网关目录查询10秒、单请求、256KiB/100项上限，模型探测继续30秒/最多2次上游请求/单次128输出tokens/结果5分钟，必须将网关重试纳入预算；受控校验路由关闭额外重试/回退。Portal/Gateway HTTP代理连接池、DB池和任务并发必须有界：API/Gateway各最多10个PG连接，Worker最多5个；Gateway每实例最多20个活动WS，超额429；HTTP上传上限10MiB，WS采用有界流式转发；更大附件不属于初始版承诺。

这些值是环境输入，不是等待开发人员自行选择的协议。缺失或格式错误启动失败；密钥文件只向必要可信进程只读挂载。禁止用默认管理密码、浮动 latest 或开发免认证模式开放网络。

### 8.7 配置变更与校验边界

域名/组织/身份映射/密钥ID/manifest/允许列表等输入不由成员API修改；启动严格解码受控文件，未知字段或schema_version、重复主体/UUID、非法URL/CIDR、缺失文件/证书、nonce密钥不匹配均拒绝启动。HC_DATA_KEY_ID必须在部署密钥集合中可用，旧key_id解密所需文件保留到重加密/恢复演练完成。域名必须同站不同Host，TLS通配范围覆盖全部实例，绝不能把Portal Host用作实例域。

配置收紧需与已实际占用量对比：可以阻止新分配，不能自动停止/删卷以满足更低数字；首次上线默认承诺100GiB数据还须20%物理余量。模板/spec各只启用一套用于新创建；已有实例按其冻结manifest启动，停用新创建选项不等于隐式更新旧实例。身份Workspace Owner映射在存在任何活动或保留资产时不得换绑到另一Workspace Owner主体，需先受控处置资产并审计；角色/status变化立即推进代次。

非秘密运行配置和维护标志由受控部署统一更新并重启相关进程生效，首版不承诺任意热加载。DSH每个进程启动分配新runtime_generation，路由就绪前关闭；旧Cookie jar/健康结果不跨代次复用。启动清单、配置摘要及依赖版本写入脱敏诊断，Secret值不输出。

当前初始化身份映射文件结构为`schema_version=1,organization_id,members[]`，每成员只含issuer/subject/role/workspace_owner_id；组织须等于部署UUID，issuer须等于平台内置issuer，subject非空、role仅ADMIN/MEMBER。首次至少一个ADMIN，重复(issuer,subject)/Workspace Owner UUID拒绝；apply为显式upsert，文件未列出的已登记成员不自动删除/禁用。identity-apply仅用于受控映射修复，内置账号初始化使用identity-import，不提供浏览器治理参数；日常用户启停、角色分配和权限治理使用平台用户/角色页面及API-028～043。本地账号供应通过平台内置账号服务执行，完整真实供应与撤销验收见测试文档，不能把编辑映射文件视为页面验收已完成。首次bootstrap以受信运维operator_ref审计，不能伪造尚不存在的用户会话。允许列表文件结构为`schema_version=1,endpoints[],models[]`且数量受8.6节约束；Secret keyring只放路径，不能内嵌Key值。

本机已验证原卷正常启停及另盘加密恢复后的完成会话回读：同一会话ID、完成回答、write/read记录及刷新保持，19-byte验收文件SHA一致；源实例/原资产状态不变，目标原数据先加密保存，两个资产继续按20GiB计账。另盘运行使用同一批准镜像、合成模型凭据及隔离TLS代理读取已有会话；本次没有提交模型任务，生产Gateway另有独立证据。这证明本机恢复后的数据可读，不证明不同故障域、定时备份或RPO/RTO。

审计维护先使用`hcctl audit-archive --batch 500`预览。需归档时配置独立`HC_BACKUP_KEY_FILE`与0700目标目录，在协调维护窗口指定新文件名、`--confirm-org "$HC_ORGANIZATION_ID" --apply`；成功返回删除行数和密文/明文SHA-256，原审计保护立即恢复，近期记录保留。法律保留用`hcctl audit-hold --hold-state active --reason-code LEGAL_REVIEW`预览，实际变更同样需组织确认、维护状态和`--apply`；释放必须显式选择released并给出原因，资产legal_hold仍独立生效。归档采用认证的audit manifest和JSONL，不能作为数据库或工作盘备份恢复；错误密钥、截断、损坏、输出冲突或审计晚写失败均禁止不受保护地删除原事件。

### 8.8 平台原生模型入口

模型入口使用本仓库Go源码直接调用官方API，配置、目录、用户密文和执行元数据都在平台PostgreSQL。部署不需要第三方模型代理镜像、Python环境、nginx模型入口或独立模型数据库。Docker仍用于工作空间、PostgreSQL和Redis。

配置示例：[原生入口配置](../deploy/model-gateway/config.example.json)、[平台入口允许列表](../deploy/model-gateway/model-policy.example.json)。所有受控配置及秘密文件0600、目录0700。`HC_DB_DSN_FILE`指向独立`hc_model_gateway`身份，`HC_MODEL_GATEWAY_CONFIG_FILE`指向schema_version=1配置；签名/数据密钥及控制凭据只引用私有文件，不能写入仓库。

| 输入 | 规则 |
| --- | --- |
| address | 明确私有或回环IP，禁止任意地址监听；本机10.51.0.1:14000，端口大于10000 |
| certificate_file / certificate_key_file | 该入口有效TLS证书及私钥，API/Worker与DSH通过受控CA验证 |
| control_key_file | 至少32bytes；仅Worker与原生模型入口读取，API/Portal/DSH不配置 |
| data_key_file / data_keyring_file / data_key_id | 与平台提供商密文一致的数据密钥及可选轮换读集合；不使用控制Key替代 |
| environment | 必须与平台密文AAD环境一致 |
| HC_MAINTENANCE | true时模型管理与推理全部503；恢复/切换时同其他进程一起停止并围栏 |

本机初始化与运行：

```sh
make model-gateway-init
make migrate
make run-model-gateway
```

初始化脚本创建受限数据库账号、TLS和原生入口配置；专用Docker网桥只为DSH访问宿主入口提供稳定地址，不拉取或运行模型代理镜像。新建环境在`make dev-init auth-init infra-up migrate auth-apply`后初始化模型入口。配置API的`HC_MODEL_ALLOWLIST_FILE`与Worker的`model_allowlist_file`为相同入口/修订/IP/CA，并将Worker `model_control_key_file`指向原生入口控制凭据。生成的`.local/model-gateway/gateway-policy.json`须合并进允许列表；models可为空，提供商目录与个人模型从数据库派生。

模型入口代码支持OpenAI、Claude、Gemini、通义千问、DeepSeek、Kimi及Z.AI。批准根来自`internal/modelgateway/transport.go`及数据库提供商目录的交集；修改地区或模型生成新连接版本。DSH使用schema2、provider=platform-models、protocol=anthropic-messages，运行适配器为platform-messages-v1。变更DSH源文件必须重新构建、验证并发布模板，不能覆盖已批准镜像或在缺少真实模型证据时标记运行批准。

用户操作：模型提供商 → 选择批准提供商/模型 → 填写个人Key → 等待注册确认 → 主动流式工具校验 → 设置个人默认/创建时选用。注册及目录不收费；真实校验可能产生提供商费用。已有实例只在显式应用后变更模型。

升级至schema24前先保存加密平台备份及受控旧网关备份，确认所有实例停止且无活动编排/重置/同步任务，停止API、Worker、访问Gateway及模型入口。执行迁移及四角色授权；`adapter_prefix`取代旧适配器列，活动模型provider为platform-models，新建model_gateway_routes/model_gateway_keys。旧个人SYNCED版本重新注册并清除能力校验期限，不将旧适配的证据当作新适配通过。更新控制凭据/允许列表引用，部署原生入口及三进程、Portal。核验双用户登录、模型注册/目录/撤销，再移除旧代理容器和活动配置。不能直接drop平台个人模型或删除用户密文；共享连接与已批准运行模板须逐项验证。

离线协议与隔离数据库验证不调用真实提供商：

```sh
go test -race ./services/internal/modelgateway
HC_TEST_DB_DSN_FILE="$PWD/.local/migrator-dsn" go test -race -count=1 ./db ./services/internal/settings
```

真实Kimi/Z.AI及至少一个非DeepSeek的DSH文件工具闭环仍需有效账户和真实能力证据；七种提供商注册通过仅证明配置与权限流程。

## 内嵌工作空间编排

工作空间编排内嵌于 `internal/workspace`，由 Worker 执行受控 Terraform。Provider 中保留 coder_workspace/coder_parameter 数据源，元数据由 Worker 私有环境传入。初始部署执行 `make workspace-init`、配置 `.local/workspace/template-variables.json`、`make workspace-template`，身份沿用 OIDC 平台映射。模板发布不等于运行批准。

Workspace 客户端通过 `NewEmbedded` 直接访问平台 PostgreSQL；配置只支持 `workspace_engine=embedded`。数据库及 Workspace 元数据按 [ADR-01](decisions/0001-coder-integration.md) 成套备份恢复，恢复时保持身份、模板、构建和 Terraform state 对应关系。

### 工作空间业务命名迁移

从 schema 9 升级时，先保存加密数据库备份与受控配置，使用正常停止命令停止全部实例，确认操作及构建终态、路由关闭及计算额度释放，再停止 API、Worker、Gateway。执行 `hcctl migrate` 和 `hcctl dev-grants` 升至 schema 10。私有 Worker/Access 配置改用 workspace_engine 与 workspace_organization_id，删除 coder_token_file、allow_local_coder_http；身份映射改用 workspace_owner_id，运行清单改用 workspace_source_version，模板契约改用 workspace_source_version/workspace_provider_version。旧字段不再被接受。

以维护账号设置 HC_MAINTENANCE=true、HC_OPERATOR_REF、HC_DB_DSN_FILE、HC_RUNTIME_MANIFEST_FILE，执行 `hcctl workspace-names-migrate`。该事务发布只修改两项容器标签的新模板，归档旧版本、迁移实例和默认模板绑定、增加版本及访问代次并追加审计；原模型版本、镜像、Owner、Workspace、数据盘和历史构建不变。命令可重试并刷新当前模板契约和运行清单。Worker 使用已批准版本的最小只读列权限核验最后成功停止构建及只修改标签的模板变更，禁止将其他模板差异视为迁移。重启三个进程，通过 readyz 和预检后正常启动原实例，核验容器仅使用 hc.workspace_owner_id / hc.workspace_id、原盘复用、模型配置及原生 HTTP/WebSocket 入口。

回退须在全部生命周期服务停止并核验实际资源后成套恢复原数据库、二进制、配置及对应模板状态；禁止只回退二进制，或启动独立 Coder 进行双写。旧迁移 SQL 和历史不可变模板/构建保留供完整性核验，当前业务库、接口和运行资源没有旧字段别名。

个人网关持续核对升级至schema22：先完成独立加密数据库备份，确认实例停止且无活动编排、同步和密码重置任务，停止API/Worker/Gateway，使用新hcctl执行migrate和dev-grants，再部署三进程和Portal。旧SYNCED版本由迁移初始化UNKNOWN，Worker只读核对后才恢复可用；禁止人工初始化HEALTHY绕过检查。当前版本使用60秒结果期限、30秒复查间隔/租约、15秒远端期限/5秒完成事务，每5秒最多10项，网关变慢或积压时宁可过期拒绝新用。组织共享连接暂未使用本个人核对表，仍须独立完成真实Key统一管理与相关验收。

## 内置 OIDC 安装与升级

身份Provider与BFF在Go API内运行，不增加身份容器、独立数据库或管理端口。入口只需Portal/Gateway HTTPS与现有API回环监听；本机issuer为 `https://portal.harness.localhost:42013/oidc`（未启用实例入口时为 `https://127.0.0.1:42013/oidc`）。Vite开发/preview及生产反向代理必须将 `/oidc/` 与 `/auth/`、`/v1/` 转发到API，保留Cookie与同源Origin；授权码查询和密码正文禁止进入访问日志。

新环境执行 `make dev-init auth-init infra-up migrate auth-apply`，然后分别启动 `make run-auth` 与 `make portal`/`portal-preview`。`auth-init`创建随机密码、TLS、客户端秘密、数据Key、3072位签名私钥和0600 `bootstrap-accounts.json`；`auth-apply`用独立迁移账号运行 `hcctl identity-import`，原子创建组织、管理员/成员、账号散列及Workspace Owner。重复初始化拒绝覆盖；重复导入核对原账号，不更改现有密码、状态、角色和资源归属。日常新增/重置使用管理员页面。

已有环境先完成加密平台数据库与原身份存储备份，确认无待确认密码重置，停止API/Worker/Gateway及模型入口以避免迁移期间使用旧授权。运行schema23迁移及最小授权，生成独立签名私钥，使用受控0600账号导入文件运行 `identity-import`，再更新issuer和启动三进程/HTTPS入口。导入按可信旧issuer+subject定位用户，保留用户ID、角色、Owner、实例、原卷及模型；缺少可信映射或Owner冲突使整批回滚，不能仅按邮箱认领账号。切换主体推进授权代次并撤销旧会话，需要重新登录。确认管理员/成员登录、密码重置及工作空间访问后停止并移除旧身份进程与配置；原存储只保留为受控加密备份。

账号导入结构为 `schema_version=1, organization_id, previous_issuer, accounts[]`，每项含 `id,email,name,password_hash,previous_subject,role,workspace_owner_id`；初始受控未映射测试账号的后三项为空。首次至少一名ADMIN，已有匹配账号保持当前角色；邮箱不得重复，散列必须为60-byte bcrypt、cost 10～14。散列与导入文件都属于秘密，不进仓库/普通日志。`HC_OPERATOR_REF`必填，导入写审计。

生产签名私钥示例：`umask 077; openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out /受控目录/signing-key.pem`。启动检查私钥文件权限、RSA类型/长度、issuer同源及配置完整性；不自动创建/覆盖生产私钥。签名私钥更换和恢复要求见[安全设计](security.md#内置-oidc-provider)。验证 `GET /oidc/.well-known/openid-configuration`、JWKS和真实登录；进程ready不能替代真实认证验收。

本机内置OIDC切换已完成：schema24、原账号/Owner/资源保持、旧会话撤销、四个Go进程与HTTPS入口就绪，旧身份进程和活动配置已移除；管理员/成员及新增账号真实登录、权限与密码重置通过。原身份停止前和SQLite停止检查点后的存储均已加密保存，不能删除备份密钥。验证范围见[测试记录](testing.md#内置-oidc-验证req-02req-1315)。

自定义/本地模型配置：在`.local/auth/model-allowlist.json`和`.local/model-gateway/config.json`同时增加`"custom_api_bases":["http://127.0.0.1:11434/v1","http://127.0.0.1:11434","http://127.0.0.1:12345/v1","http://127.0.0.1:12345"]`，保留其他配置和0600权限，重启API、Worker与原生模型网关。地址指平台后端主机能访问的上游，localhost不代表用户浏览器主机；平台不负责启动或下载本地模型。容器部署须使用后端可达的固定地址。自定义Anthropic根可为服务根或以`/v1`结尾；OpenAI根通常以`/v1`结尾，不填写具体`/chat/completions`、`/responses`或`/messages`路径。运维添加其他端口或内网IP时须在两处配置相同精确根。schema36增加OpenAI Responses协议适配器；升级前加密备份、协调停止服务、执行迁移及grants、重编译重启全部Go服务并重建Portal。

官方目录来源按当前账户API实时读取：[OpenAI Models](https://developers.openai.com/api/reference/resources/models/methods/list)、[Anthropic List Models](https://platform.claude.com/docs/en/api/models/list)、[Gemini models.list](https://ai.google.dev/api/models#method:-models.list)、[DeepSeek Lists Models](https://api-docs.deepseek.com/api/list-models/)。Kimi、Qwen、Z.AI使用其OpenAI兼容根的`/models`，若对应区域或套餐未提供目录接口会明确返回同步失败；自定义可手动填写模型ID，并可选择OpenAI Chat Completions、OpenAI Responses或Anthropic Messages协议。协议适配不代表任意模型都支持工具调用；须通过模型能力校验后使用。

## 用户资源配额升级（schema26）

升级前使用当前版本CLI生成新的加密数据库备份并保留当前服务二进制。协调停止API/Worker/Gateway/原生模型网关，使用新版本CLI执行`migrate`及`dev-grants`（受控生产角色应用等价最小授权），再启动重编译的全部Go服务并构建Portal。所有服务的schema检查须匹配26；不得只更新前端或只重启API。

新表`user_quota_policies`初始为空，已有用户继承旧额度。API需要该表SELECT/INSERT和version、四项额度、updated_at列UPDATE；Worker及两类Gateway无需新增配额表写权限。管理员在独立“资源配额”页（`/app/quotas`）逐人配置。额度不预留宿主机资源，用户上限与组织池同时限制；降低额度前须处理占用，保留卷须受控回收。配额配置由完整数据库备份保存，日备份前后状态指纹会检测配额变更。


提供商页面升级到schema28：先完成加密数据库备份，再协调停止四个Go服务，使用新hcctl执行migrate与dev-grants并重编译重启全部Go服务。重建前端并重启Portal预览进程，确认HTML引用当前资源摘要。新权限包括API对personal_provider_accounts的读/写与受控本人凭据函数执行，以及Worker的提供商状态/模型归档更新；其他进程不需要这组管理权限。原用户/模型/工作空间ID保持，升级不启动模型推理，不自动更换现有工作空间的模型。

资源选择升级包含迁移 29（多组批准规格）与迁移 30（身份锁兼容外键引用）。升级前执行独立密钥加密数据库备份；短暂停止 API、Worker、Gateway 与模型网关，使用新 hcctl migrate 顺序迁移并同步更新四个服务二进制，再恢复服务。迁移保留现有实例快照、默认规格和全部额度。无需重新批准 Terraform 模板或替换实例镜像，Worker 启动前应用冻结资源限制。

## 实例数量统一上限升级（schema31）

先使用独立密钥生成升级前数据库备份，再停止四个 Go 服务，用迁移身份执行 `hcctl migrate`，更新 API、Worker、Gateway 和模型网关二进制并重启，最后构建并更新 Portal。schema31 删除组织及用户 `max_running` 列；原先更小的运行数量限制不再生效，保留实例数、CPU、内存、存储及占用。用户配额请求/响应改为四项，不再接受旧运行数量字段；已有独立配额版本自动递增，页面须重新读取。应用身份权限列表同步移除该列，既有其他授权不变。运行批准工具依据宿主机核算 CPU/内存和构建并发保护，不再写独立运行数量。

## 模型提供商账号默认模型升级（schema32）

先完成独立密钥加密备份，再协调停止四个Go服务，使用新hcctl执行migrate，部署包含schema32的API/Worker/Gateway/模型网关并重启，最后构建Portal。迁移回填账号默认并递增账号版本，旧编辑器须重新读取，原模型和实例绑定保持。若当前模板缺少platform-messages-v1适配器，须验证原生运行镜像、存储、网络、Workspace和真实模型契约，通过受控dev-runtime-upgrade切换新创建使用的模板；保留旧镜像以继续支持旧实例快照，不直接修改批准记录。

## DSH账号模型切换运行环境

部署新增模型网关账号目录/授权、API显式应用批准适配器逻辑及Portal名称表单；执行hcctl dev-grants更新模型网关对personal_provider_accounts、model_validations、model_gateway_checks的只读授权，无新增数据库schema。构建并验证账号切换适配器镜像、schema2、存储、网络、Workspace及真实模型契约，通过受控workspace-template与dev-runtime-upgrade批准新模板。维护前停止运行实例并保留工作盘，部署后普通启动仍使用冻结旧适配器；需要切换功能的旧实例由Owner显式更新模型配置后启动。不得覆写旧镜像、manifest、实例快照或明文运行Key。

已有运行实例显式应用新版时，Worker须同时部署兼容模板前驱与target_snapshot核对逻辑；仅更新API会使旧Worker拒绝新模板并保留UNCERTAIN。部署修复Worker后原受理操作可自动对账完成，不能修改操作状态跳过校验。

## 默认用户资源配置部署与维护

部署 schema34 前备份数据库，执行 migrate 和 dev-grants，更新 API/Worker/Portal。迁移允许没有组织模型时保存运行默认值，并为既有默认值补充 7 天保留期限；不自动批准未知镜像或模板。资源批准仍须核对固定源码、镜像摘要、实际运行/存储/网络/Workspace 契约与 Gateway 证据，真实模型能力按用户账号单独验收，不作为初始化资源默认值的前置条件。

本地首次完成受控 runtime-approve 后，在 /app/settings 点击“初始化配置”；后续点击“修改配置”。无批准模板/规格时禁止保存，不能伪造批准或健康记录。平台配置不保存模型账号或 Key。运行资源修改只作用于后续创建，默认实例上限作用于继承用户，保留期限作用于后续确认软删除，原有更长期限与法律保留继续生效。

数字存储容量使用受控 `bounded-ext4-v1` 适配器。先构建并核验新存储镜像，完成容量/持久化、Workspace/Worker/Gateway 契约；协调维护并发布新的版本化模板及 manifest，通过 dev-runtime-upgrade 事务批准整数 GiB 组合。不能仅修改数据库批准开关或覆写旧 manifest；旧运行模板只接受原 10 GiB 容量。升级可保持相同 DSH 镜像，改变存储 helper 和模板版本；组织和用户额度仍独立检查。已有资产继续按原 manifest、原容量、原 UUID 检查，默认值不执行磁盘扩缩容。备份恢复目标必须同 Owner 且容量与来源相等；容量不匹配拒绝恢复，未知物理资产按最大支持容量保守计额。

## 默认用户资源配额升级（schema39）

升级前完成数据库备份；协调停止 API、Worker、Gateway 和原生模型网关，使用新版本 `hcctl migrate` 与 `dev-grants`，更新并重启全部 Go 服务，再重建 Portal。运行默认配置已初始化时，此后通过用户管理创建的账号会在同一事务中获得默认规格乘以每用户默认实例数的个人额度，CPU、内存和存储上限受组织配额约束。迁移不改写已有用户配额。

## 个人模型工作空间执行升级（schema40）

执行 `hcctl migrate` 与 `dev-grants`，更新并重启 API、Worker、Gateway 和模型网关。Worker、操作重试和存储维护允许组织未配置共享模型，仍按既定顺序锁定成员关系及已存在的组织模型，并检查个人模型权限、凭据和可用性。此前因缺少组织模型记录而停留在 `UNCERTAIN / RECONCILING` 的创建任务由 Worker 自动继续，无须重复创建或手动修改操作状态。

平台配置页的系统检查使用 `/v1/platform-checks`，检查公共服务、运行资源默认值、批准模板与规格，以及组织配额是否初始化。个人模型、Workspace Owner 映射和剩余额度不参与系统健康判断；创建工作空间继续通过 `/v1/preflight` 检查这些条件，系统和用户额度在受理事务中再次核验。

## 系统资源默认上限升级（schema41）

执行迁移并更新全部 Go 服务后，新初始化组织默认使用20个实例、40vCPU、80GiB内存和200GiB持久存储；默认单用户实例上限仍为2。迁移只修改列默认值，保留已有系统额度、用户额度和占用。运行首次批准仅按宿主机容量限制创建并发，创建/启动仍检查宿主机实际余量。已有组织需要扩大额度时，在“系统资源上限”中保存新值。

### 资源规格扩展迁移

迁移42仅为已有启用的存储容量补齐5档CPU与6档内存的批准组合；不启用原本停用的存储容量，不修改默认资源、现有配额或实例快照。迁移41只调整新建组织的数据库默认系统额度。升级前按备份流程保存数据库，并协调更新API、Worker、Gateway与模型网关；内部Worker预检增加主机资源事实，须使用同一版本。

### 本机存储管理部署与维护（schema43）

迁移43须在API、Worker、Gateway、模型网关协调停止后执行migrate及受限角色授权更新，再使用当前构建重启四进程。升级前创建独立密钥加密数据库备份。资产、已有期限、配额与冻结规格保持不变；历史备份没有安全记录时页面显示无记录。

用量基线固定为60秒间隔、300秒TTL、单次5秒预算、10秒调度、每批最多2项，仅探测已运行且核实原绑定的工作空间。停止资产保留最近观察和时间，不能临时启动采集。备份RUNNING超过30分钟在查询中显示待确认，不能自动判失败后重建。

受信CLI使用原有HC_DB_DSN_FILE、HC_WORKER_CONFIG_FILE、HC_ORGANIZATION_ID、HC_OPERATOR_REF及独立HC_BACKUP_KEY_FILE（32字节，区别于数据加密密钥），输出目录须私有。`storage-backup --asset <UUID> --output <绝对私有路径>` 会记录并验证归档；新归档密文包含冻结模板和镜像版本，旧版无这两个字段的归档继续兼容。中断后使用 `storage-backup-reconcile --backup-id <原任务UUID> --apply` 核对原归档；无文件确认失败，损坏保持待核对。`storage-hold --asset <UUID> --hold-state active|released --reason-code <安全原因>` 默认只预览，执行另传 `--confirm-asset <同一UUID> --apply`。reconcile及hold写操作要求HC_MAINTENANCE=true。恢复与永久回收继续使用既有精确确认及保护规则。

本次范围完成本机功能；与主机分离的备份目标、独立故障域、RPO/RTO和灾备演练后续单独建设与验收。本机加密备份不作为灾备达标证据。

### 本人手动释放升级（schema44）

四进程协调更新，升级前加密备份，再执行迁移44和受限角色授权。新增权限默认赋予内置ADMIN/MEMBER，自定义角色需显式授予instances:release-storage。无需增加主机凭据或新的服务。Worker每5秒处理一个请求，45秒执行预算；失败保留意图与存储额度，60秒后对账，维护模式停止调度。此入口只释放用户在网页上精确确认的已删除资产，不自动提前删除现有保留数据。

## 工作空间插件与 Agent 预设

### 当前运行基线

以下为2026-10-20本机已批准并验证的版本，其他部署须以自身批准manifest及Worker实际配置为准。此运行更新不新增数据库迁移。

| 项目 | 当前值 |
| --- | --- |
| 批准模板 | `9098df09-7e95-4661-92a9-c70b8642dff4` |
| 运行镜像 | `sha256:969b673d4c83e2a842dd4c9378c1d7288ae453cc6e1096322cf0d116ba5631d0` |
| DSH / pnpm | `0.2.1-alpha.2` / `11.28.5`，均由锁文件固定 |
| 必需服务 | 原生PluginManager、configEditor和settings；HMR启用，bootstrap等待必需服务挂载 |
| 持久目录 | `/data/workspace`存放工作文件；`/data/dsh`存放profile、依赖、锁文件、设置与缓存 |
| 平台覆盖 | 模型路由、默认模型覆盖和原生认证；非必要遥测与默认会话日志上传关闭 |
| Office 预览 | 镜像内置 DejaVu、Liberation 和文泉驿正黑字体，供原生 Office 转换服务使用；新工作盘无需另行安装字体 |

Owner可在“插件”页管理内置可选插件和第三方DSH组合包，在“设置 → Agent 预设”保存新任务默认模式。更改作用于同实例全部会话并随原数据盘保留；停止、重启不清空配置。保留原生信任提示、兼容性检查、脚本批准及失败回滚，普通npm库不能作为DSH组合包安装。配置写入边界与第三方代码权限见[安全设计](security.md#6-容器工具与数据)。

### 发布与已有实例升级

1. 按[运维命令接口](#7-运维命令接口)中的受控运行批准流程，备份数据库和私有配置，验证新不可变镜像与manifest，保留已有实例依赖的旧镜像及批准记录。
2. 发布新的批准版本后，未来创建使用新默认版本；已有实例继续使用各自冻结版本，不会因默认值变化自动升级。
3. 已有实例通过正常停止、配置升级（configure）、启动（start）应用新批准镜像，核对原资产绑定并保留原数据盘；禁止直接覆写冻结快照、修改运行容器文件或删除`/data/dsh`作为修复手段。
4. 就绪后从控制台重新进入，确认插件页可管理、Agent预设可加载、平台模型目录正常；复验范围与命令见[专项验收](testing.md#工作空间插件与-agent-预设验收)。API/Gateway重启或限流连接恢复后的120秒保护期沿用原规则。

前一插件管理版本（模板`9031d3f9-7cc7-48cb-871e-abea3e640400`、镜像`sha256:4762b2fa97ac3c5b29c090e429aa1b78014e24686ee5bc8924fd747fccf9f6d7`）仅作为保留的历史批准，未包含本次完整设置服务修复。

Office 字体修复沿用完整设置服务版本（模板`46ca7eb2-9576-4766-8b19-d8d8fc4c4b92`、镜像`sha256:e038baf6af8c4aef93cc11631281a5d9cbb8e9aa23824a427d7fdbafae6b5f1d`）的 DSH、profile 与存储格式。该历史镜像继续保留，已有实例仍可按原快照启停；默认版本切换只作用于后续创建。

### 常见故障

| 现象 | 检查与处理 |
| --- | --- |
| “本部署没有可管理的 profile” | 核对实例冻结镜像是否支持持久profile、PluginManager及HMR，以及锁定pnpm是否可执行；旧实例按批准流程升级 |
| `settings service is absent` | 核对config-editor与settings均启用，bootstrap必需服务包含configEditor/settings；仅启用插件管理不足以修复此问题 |
| 安装失败、非组合包或版本不兼容 | 查看原生失败原因，使用兼容的DSH组合包；依赖下载成功不代表实际激活，不自动豁免版本或批准构建脚本 |
| `settings/conflict` | 重新读取设置和revision，再确认提交，避免覆盖并发修改 |
| `settings/rejected`，提示平台模型项被覆盖 | 模型提供商与默认模型通过控制台管理；原生预设设置不能覆盖平台模型配置 |
| Office 预览不可用 | `office-to-pdf` 已挂载也可能因缺少字体而返回 `unavailable`。运行镜像须包含 `fonts-dejavu-core`、`fonts-liberation2`、`fonts-wqy-zenhei`，仅安装系统 LibreOffice 无效。已有实例可在持久目录 `/data/dsh/.local/share/fonts` 安装受信字体及许可文件，再重新打开预览；无需停止工作空间。 |

运行镜像的 Office 字体验证：`HC_RUNTIME_IMAGE=hc-dsh-runtime:office-preview node --test infrastructure/runtime/tests/office-integration.test.mjs`。该测试在无网络、只读根文件系统及 noexec 临时目录的全新容器中转换含中英文的 DOCX，检查 PDF 嵌入字体及 Unicode 映射；不依赖已有工作盘字体，也不调用模型。

实例入口对 `POST /api/officeToPdf/render` 使用独立的 90 秒请求时限，覆盖首次引擎及字体加载和转换。其他浏览器请求仍为 15 秒响应头时限，原生健康探测仍为 2 秒；Office 请求继续使用相同的身份、Host、Origin 和请求体限制。发布字体镜像时需同步更新 Gateway，避免首次预览被入口提前中断。

历史首次进入认证间歇失败与新建性能不能仅凭本次插件/设置验收判定已全部解决，验证边界以测试文档为准。


## 外接记忆与知识库

本机外接服务使用独立Compose项目 `hc-context`，部署步骤见[外部服务说明](../deploy/context/README.md)。API通过 `HC_CONTEXT_ENDPOINTS_FILE` 加载0600私有批准策略；`make run-auth` 会识别本机策略文件。所有数据使用独立卷，未修改任何既有DSH容器或数据。OpenViking/Graphiti测试复用用户批准的平台模型配置，单独私有保存；生产须使用专用最小权限服务账号与备份策略。


### 技能包来源出口

来源下载默认直连，忽略环境中的HTTP代理。主机直连公网不稳定时，运维可用0600私有JSON文件配置明确的本机CONNECT代理，设置 `HC_SKILL_SOURCE_PROXY_FILE` 指向该文件；`make run-auth` 自动识别 `.local/skill-source-proxy.json`。

```json
{"url":"http://127.0.0.1:20172"}
```

仅接受带端口的HTTP回环IP，不接受代理域名、远端代理、凭据或附加路径。下载器仍自行解析并验证来源的全部DNS答案，代理只收到固定公网IP的443端口；目标TLS证书按原始来源域名验证，逐跳重定向、12秒总时限和4MiB上限不变。浏览器不能设置代理或任意内网目标。本机测试代理须保持运行；移除此配置即可恢复直连。


## 工作台连接器（MCP）

API读取私有0600文件 `HC_CONNECTOR_ENDPOINTS_FILE`；本机 `make run-auth` 自动识别 `.local/connectors/endpoints.json`。未配置策略文件时，5种OAuth服务仍有默认固定地址，用户也可填写个人公网HTTPS；其他目录服务须绑定兼容服务或从自定义入口添加。私网/HTTP例外只对精确批准URL生效。

执行迁移49～51及API角色授权，再更新平台API原生进程；不修改DSH。OAuth回调固定为 `HC_PUBLIC_URL/v1/connectors/oauth/callback`，须与源站、TLS和登录会话一致，并满足厂商回调登记要求。无法动态注册时使用策略文件中的私有 `oauth_clients`；企查查当前本机自定义域名的注册限制见[实网报告](connectors-oauth-availability-2026-10-26.md)。

固定服务、OAuth、独立Docker合成服务、故障排查与复验命令统一见[部署与复验](../deploy/connectors/README.md)。首次启动API沿用既有限流器120秒安全恢复窗口，恢复后再执行真实登录；不要通过重启DSH处理。功能及待交付边界见[实施说明](connectors-implementation.md)。
