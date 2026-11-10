# MCP连接器部署与复验

本文提供现有连接器的地址、认证配置和复验步骤。功能与模块见[后端说明](../../docs/backend.md#工作台连接器mcp)及[工作台交互](../../docs/frontend.md#工作台连接器mcp)，交付要求见[REQ-23](../../docs/requirements.md#617-连接器mcpreq-23)，验收状态见[测试文档](../../docs/testing.md)。

## 1 部署前提

工作台入口为 `/app/connectors`。平台API须具备PostgreSQL、Redis、登录会话、数据加密密钥及公网HTTPS出口；用户需要本人连接的读取/管理/调用权限。工作台由平台作为远程MCP客户端，不修改DSH；创建时的受控接入另见[REQ-25](../../docs/requirements.md#619-创建工作空间扩展配置req-25)。支持Streamable HTTP、无认证/Bearer/OAuth 2.0 + PKCE；新建连接优先使用 `2026-07-28`，自动协商兼容 `2025-11-25`，已有兼容版配置保留。

执行 `make migrate` 应用当前全部迁移并更新受限角色授权；连接器表来自迁移49～51，当前平台基线为迁移68。更新平台API与Worker进程，Worker内嵌新空间的安装脚本和逐工具适配器；两者须使用同一份 `HC_CONNECTOR_ENDPOINTS_FILE` 出口策略。本机 `make run-auth` 包含迁移及初始化步骤。API依赖就绪后即可执行登录测试，无启动冷却；DSH源码、运行镜像及已初始化的空间Profile保持原样。

## 2 平台服务与出口策略

API通过 `HC_CONNECTOR_ENDPOINTS_FILE` 读取0600私有JSON文件；本机 `make run-auth` 自动识别 `.local/connectors/endpoints.json`。个人公网HTTPS地址无需预先加入文件；策略用于固定平台服务及私网/HTTP精确例外。

```json
{
  "endpoints": [
    {
      "id": "approved-example",
      "name": "业务工具服务",
      "url": "https://mcp.example.com/mcp",
      "allow_private": false,
      "allow_http": false
    }
  ]
}
```

`allow_private`放行明确批准的私网目标；仅本机/私网HTTP可同时设置 `allow_http`，公网仍须HTTPS。完整URL精确匹配，不批准整个域名或网段；ID不可为保留值 `personal`。普通服务Token不存此文件，用户在本人的连接中配置。

文件按请求重新读取，未知目录ID或不合法配置会拒绝加载。DNS全部答案校验后固定拨号IP，TLS验证原域名，不跟随重定向或环境代理；更换目标地址须重新认证。

### 绑定内置目录

38种目录条目均支持用户自行填写服务地址和认证信息，19种官方远程服务默认有官方地址（其中9种OAuth）。管理员可通过 `catalog_id` 绑定兼容服务；同目录已有绑定时保留管理员服务，不添加默认绑定。例如：

```json
{
  "endpoints": [
    {
      "id": "approved-notion",
      "name": "Notion MCP 服务",
      "url": "https://mcp.notion.com/mcp",
      "catalog_id": "notion",
      "allow_private": false,
      "allow_http": false
    }
  ]
}
```

此示例用于说明绑定格式；默认OAuth服务通常无需重复登记。除已支持的腾讯文档、腾讯会议、微云、乐享及滴滴原生认证外，其他需CLI、stdio或旧SSE的条目，先独立部署兼容HTTP MCP适配服务，再由用户填写该服务地址；平台策略用于保留旧平台服务及批准私网/HTTP地址，表单不再提供推荐服务选择。目录本身不会部署这些服务。用户可在任意目录条目直接填写个人地址，“自定义MCP”用于不关联目录的服务。

### 默认地址与认证

以下为2026-10-27核对的19种官方远程地址；名称链接指向来源文档。默认地址可修改，表单按内置目录预填官方地址，自定义MCP地址默认留空，不展示推荐服务选择；预填地址不代表用户已授权或服务持续可用。

| 连接器 | 默认地址 | 个人认证 |
| --- | --- | --- |
| [腾讯文档](https://developer.cloud.tencent.com/mcp/server/11803) | `https://docs.qq.com/openapi/mcp` | Token，原样发送 `Authorization` |
| 腾讯会议 | `https://mcp.meeting.tencent.com/mcp/wemeet-open/v1` | Token，发送 `X-Tencent-Meeting-Token` 及 `X-Skill-Version: v1.0.1` |
| 腾讯微云 | `https://www.weiyun.com/api/v3/mcpserver` | Token，发送 `WyHeader: mcp_token=令牌` |
| [腾讯乐享](https://github.com/tencent-lexiang/lexiang-mcp-skill) | `https://mcp.lexiang-app.com/mcp` | 企业标识 `company_from` 与访问令牌，格式见下文 |
| [滴滴](https://mcp.didichuxing.com/api) | `https://mcp.didichuxing.com/mcp-servers` | MCP Key，仅出站时写入 `key` 查询参数 |
| [腾讯设计 Ardot](https://docs.ardot.tencent.com/ardot-mcp/introduction.html) | `https://ardot.tencent.com/mcp` | OAuth |
| [Notion](https://developers.notion.com/docs/mcp) | `https://mcp.notion.com/mcp` | OAuth |
| [OpenAlex](https://help.openalex.org/access/connector/) | `https://mcp.openalex.org/mcp` | OAuth |
| [滴答清单](https://help.dida365.com/articles/7438132116019216384) | `https://mcp.dida365.com` | OAuth |
| [企查查](https://agent.qcc.com/guide) | `https://agent.qcc.com/mcp/company/stream` | OAuth或Bearer Key；当前仅接入企业信息服务 |
| [GitHub](https://github.com/github/github-mcp-server) | `https://api.githubcopilot.com/mcp/` | 个人访问令牌（PAT），标准Bearer |
| [Gitee](https://gitee.com/oschina/mcp-gitee/blob/master/README_CN.md) | `https://api.gitee.com/mcp` | 个人访问令牌，标准Bearer |
| [GitLab](https://docs.gitlab.com/user/model_context_protocol/mcp_server/) | `https://gitlab.com/api/v4/mcp` | 具有MCP tool资源Execute权限的细粒度PAT，或OAuth；传统PAT不可用 |
| [Codeup](https://github.com/aliyun/alibabacloud-devops-mcp-server/blob/master/docs/hosted-mcp-guide.zh-CN.md) | `https://openapi-rdc.aliyuncs.com/ai/mcp` | 云效个人访问令牌，标准Bearer；Region站改为本组织域名下的 `/ai/mcp` |
| [Context7](https://context7.com/docs/resources/all-clients) | `https://mcp.context7.com/mcp` | 可选“无需认证”，或以标准Bearer发送本人API Key |
| [Linear](https://linear.app/docs/mcp) | `https://mcp.linear.app/mcp` | OAuth |
| [Sentry](https://mcp.sentry.dev/) | `https://mcp.sentry.dev/mcp` | OAuth；可按官方说明改为组织或项目专属路径 |
| [Vercel](https://vercel.com/docs/agent-resources/vercel-mcp) | `https://mcp.vercel.com` | OAuth，必要时登记厂商批准的客户端与回调 |
| [Supabase](https://supabase.com/docs/guides/ai-tools/mcp) | `https://mcp.supabase.com/mcp` | OAuth或PAT；不能使用项目 `anon` / `service_role` Key |

腾讯会议、微云等原生格式的核对来源另见[腾讯云 Octop 配置](https://github.com/TencentCloud/Octop/blob/0d65f2ad4b855141572f4ddeb367db5538c5e92e/src/octop/infra/connectors/builder.py)。原生认证仅适用于精确匹配的官方URL，自定义网关继续使用标准Bearer，不因目录ID相同而发送厂商专用头。

乐享页面分别填写企业标识和令牌；API的 `credential` 为包含二者的JSON字符串，如 `{"token":"本人令牌","company_from":"企业标识"}`。服务端将 `company_from` 编码为查询参数，并发送 `Authorization: Bearer 令牌`。二者一起加密且均不读回；原地址编辑时均留空可保留，更换任意一项须一起重填。滴滴Key只在出站时注入，不保存到连接URL、列表或导出中。

GitLab自建实例使用该实例的 `/api/v4/mcp`，并须在群组或实例允许MCP。Context7匿名接入保存后自动连接和发现，可在“工具与记录”查看 `resolve-library-id` 和 `query-docs`；厂商可能限制匿名额度，需要时按[官方说明](https://context7.com/docs/resources/troubleshooting)改用Bearer API Key。

其余目录没有统一的兼容公共地址：CLI或stdio服务须独立部署HTTP MCP网关，有道云笔记的旧SSE须适配，Dify地址由个人应用或工作流发布生成。填写个人兼容HTTPS地址仍沿用平台出口校验，目录本身不部署服务。

## 3 OAuth配置

默认目录中9种服务使用OAuth，地址见[上表](#默认地址与认证)。每个用户用自己的厂商账号授权；企查查五类聚合和专用解绑仍属待实现范围。

回调固定为 `HC_PUBLIC_URL/v1/connectors/oauth/callback`。正式部署使用可访问的HTTPS源站，并按厂商要求登记完整回调；本机须使浏览器信任证书。平台源站、证书、登录会话与网关应一致，不单独改授权请求里的回调地址。

自定义MCP须提供RFC 9728保护资源元数据、授权服务器元数据和PKCE S256。支持动态注册时由平台注册客户端；无法动态注册时在同一0600策略文件中配置 `oauth_clients`（仅格式示例）：

```json
{
  "endpoints": [],
  "oauth_clients": [{
    "url": "https://mcp.example.com/mcp",
    "issuer": "https://auth.example.com",
    "client_id": "registered-platform-client",
    "client_secret": "replace-in-private-file-only",
    "auth_method": "client_secret_basic",
    "resource": "https://mcp.example.com/mcp",
    "scopes": "tools:read"
  }]
}
```

客户端配置按精确服务URL匹配，不通过连接器列表读回。`auth_method`支持 `none`、`client_secret_basic`、`client_secret_post`，公开客户端无需 `client_secret`。所有元数据、注册与Token端点均执行独立出口检查；测试HTTP端点须逐个精确批准，不能仅批准MCP地址就放行所有授权端点。

### 资源标识与厂商回调

自定义服务通过RFC 9728路径级或根级保护资源元数据发现授权服务器；Linear、Sentry、Supabase使用此流程。`resource` 必须与厂商声明的标识一致，不按服务URL自行拼接或去掉末尾 `/`：滴答清单为 `https://mcp.dida365.com/`，Vercel为 `https://mcp.vercel.com/`。scope与grant按厂商元数据和本人授权范围申请；2026-10-26滴答清单元数据仅声明 `authorization_code`，平台支持刷新不代表厂商一定签发refresh token。

Vercel的注册接口为 `https://api.vercel.com/login/oauth/register`。2026-10-26检查中，当前本机 `portal.harness.localhost` 回调被以 `invalid_redirect_uri` 拒绝；须先取得厂商批准的客户端及回调，再按精确服务URL配置 `oauth_clients`。仅修改MCP地址或授权请求回调不能解决登记限制。

### 企查查回调限制

2026-10-26检查时，自定义本机域名 `portal.harness.localhost` 被企查查以 `invalid_client_metadata` 拒绝；回调路径为 `/v1/connectors/oauth/callback` 时，`http://localhost:42013`、`http://127.0.0.1:42013` 和 `https://127.0.0.1:42013` 的注册被接受。该结果仅证明当日注册接受地址，不证明登录、会话和回调已完整运行。

本机回调要求浏览器与服务处于同一机器，并满足平台的TLS/会话配置；厂商接受某个HTTP回调不代表当前平台登录可直接迁移到HTTP。云端平台须登记正式HTTPS回调，不能用用户电脑的localhost替代。企查查API Key可避免OAuth回调限制，但当前仅有通用Bearer入口，尚未完成专用双模式及真实Key验收。

### 实网验证边界

2026-10-26，Ardot、Notion、OpenAlex、滴答清单、Linear、Sentry和Supabase通过平台真实出站策略的元数据、动态注册及授权入口检查；未完成厂商账号同意、成功回调、业务调用或真实令牌刷新。Context7匿名模式已通过平台握手、工具发现及两项显式业务调用，协商协议为 `2025-11-25`；GitHub未提供本人PAT，未验收业务工具。Vercel和企查查的回调限制见上文。这些是当日结果，不代表持续健康，厂商策略或平台配置变化后须重新验证。

平台模拟OAuth、受控页面和合成MCP服务验证不能替代逐厂商真实账号验收。厂商调用与刷新按[TC-106](../../docs/testing.md#2-用例目录)继续验收；企查查专用双模式、五类聚合及远端撤销按[第6.17.2节](../../docs/requirements.md#6172-企查查完整接入扩展交付)交付。

## 4 独立测试服务

Compose项目 `hc-mcp` 提供合成工具：`echo`中文回显、`synthetic_write`内存计数及延迟返回、`effect_count`读取计数。它不接入厂商业务、不挂载工作空间，重启计数清零。

准备至少32字符的随机测试密钥 `.local/connectors/test-key`（0600），并将 `http://127.0.0.1:18781/mcp` 以 `allow_private=true`、`allow_http=true`登记到策略。随后在仓库根目录执行：

```bash
mkdir -p /tmp/hc-mcp-fixture-build
CGO_ENABLED=0 GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go build -o /tmp/hc-mcp-fixture-build/mcp-fixture ./services/cmd/mcp-fixture
docker build -t hc-mcp-fixture:0.1.0 -f deploy/connectors/Dockerfile /tmp/hc-mcp-fixture-build
HC_MCP_UID=$(id -u) HC_MCP_GID=$(id -g) docker compose -f deploy/connectors/compose.yaml up -d
curl --fail http://127.0.0.1:18781/healthz
make migrate
make run-auth
```

在另一终端运行前端 `make portal`。容器使用只读根文件系统、无额外capability、CPU/内存/PID限制和只读密钥挂载，仅监听回环端口。停止：

```bash
docker compose -f deploy/connectors/compose.yaml stop
```

## 5 复验命令

后端测试使用独立PostgreSQL测试库，不把跳过数据库用例当作通过。在仓库根目录执行：

```bash
HC_TEST_DB_DSN_FILE="$PWD/.local/migrator-dsn" GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race -count=1 ./services/internal/connectors ./db ./services/internal/operations ./services/internal/security ./services/internal/httpserver
HC_TEST_DB_DSN_FILE="$PWD/.local/migrator-dsn" GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race -count=1 ./services/internal/worker -run '^TestWorkspaceRuntimeMCPFollowsBoundServersAndStableReceipts$'
HC_RUNTIME_IMAGE='<已批准的运行镜像或摘要>' node --test infrastructure/runtime/tests/mcp-bridge.test.mjs
```

Worker用例验证真实HTTP MCP、受限Worker身份、动态目录、服务启停和旧确认拒绝；原生适配用例在独立容器内运行安装脚本、DSH工具注册器及私有调用通道，不修改已有空间。上述命令不调用对话模型。

页面测试从 `apps` 目录执行；真实测试需平台、登录账号及独立MCP测试服务就绪：

```bash
cd apps
npm run typecheck
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors.spec.ts --workers=2
HC_REAL_IDENTITY=true HC_REAL_CONNECTORS=true PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors-live.spec.ts --workers=1
HC_REAL_DEVELOPMENT_MCP=true PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors-development-live.spec.ts --project=desktop --workers=1
```

受控页面使用模拟API；真实页面创建独立成员账号，验证平台配置/工具流程并清理数据，桌面另覆盖Ardot真实注册、授权URL及取消回调。合成工具调用成功不代表真实厂商工具通过；厂商账号同意、成功回调及业务调用须按TC-106～109另行验收。

开发类实网用例显式启用后验证Context7调用及OAuth入口；它保留全部OAuth入口成功的断言，未解决Vercel回调批准时整项会失败，不能将其余入口通过写成整项通过。未启用真实环境开关时跳过，不计作验收成功。

## 6 排查入口

| 现象 | 检查 |
| --- | --- |
| 平台页面可打开，API不可用 | 检查API `/livez`、`/readyz`及反向代理；前端 `/healthz` 可能返回HTML，不能作为API健康证明 |
| 未登录请求 `/v1/me` 返回401 | 符合未登录行为；完成平台登录后再调用本人连接接口 |
| 回调未到平台 | 核对源站、TLS、会话、厂商登记的完整回调及浏览器访问位置 |
| 注册拒绝或需管理员配置 | 检查厂商回调限制、动态注册能力和私有 `oauth_clients` |
| 元数据或服务不可访问 | 检查DNS、证书、出站网络及逐端点例外；平台不使用环境代理或自动重定向 |
| 授权成功但工具不可调用 | 新发现工具会自动启用；若曾手动停用，请重新启用，并检查账号权限、连接/目录版本及令牌有效性 |
| 工作台试调用成功，DSH中没有该工具 | 确认新建空间时已选择服务、扩展状态为可用；旧空间Profile不会自动替换，需新建并选择连接器 |
| 确认后提示服务或工具定义已更新 | 旧确认已失效；等待目录刷新后重新发起调用，核对新参数与目标 |
| 工具调用超时或取消 | 保留调用ID并核对外部结果，不自动重试可能已执行的操作 |

平台连接器调用账本仅保存状态、版本及摘要，不保存参数、结果正文或秘密；工作台结果仅在本次响应展示。DSH对话调用的参数和返回内容属于会话记录，会沿用DSH的会话持久化及已选择的记忆同步规则。当前删除连接只清除本地凭据；正式企查查远端解绑流程尚待实现。

## 在 DSH 对话中调用

1. 添加自定义MCP，填写可访问的Streamable HTTP地址和认证信息，保持服务启用。保存后自动发现，新工具默认启用；OAuth服务先完成授权。
2. 创建工作空间，在扩展的“连接器”中选择该服务。启动后在详情“扩展配置”确认连接器状态为可用，再进入DSH。模型接收每个工具的原始参数Schema，结果进入对话。
3. 对测试服务发送：`调用“我的测试 MCP”的 echo 工具一次，text 填写“你好 MCP”，收到工具结果后只返回原文。` 将服务名替换为本人配置的名称；`echo`仅为本项目合成服务的示例，其他服务使用各自工具及参数。只读工具沿用DSH权限流程，可能修改外部数据或行为未知的工具额外展示参数并请求单次确认。
4. 运行中空间自动跟随该服务的目录，约30秒发现新增/移除工具。手动停用单个工具会保留偏好；服务开关停用后立即禁止新调用，并随目录同步撤回模型工具，重新启用后恢复。身份或权限撤销同样阻止调用。

DSH调用携带当前展示的服务和目录版本；等待确认期间发生配置变化，会拒绝旧确认，请重新发起调用。

此流程通过平台适配插件复用DSH官方MCP适配器，不修改DSH本体；凭据保留在平台。旧空间内已安装的通用适配器不会被覆盖，需创建选择该服务的新空间来使用逐工具适配器。平台页面提供远程HTTP连接；本地stdio程序仍使用DSH原生配置，或先部署HTTP网关。

新空间验收时，核对DSH中的工具调用、返回内容、模型最终回复，以及工作台“调用记录”的工具名、成功状态和调用ID。2026-11-10已通过真实模型调用合成 `echo` 的完整链路，结果与最终回复一致；环境及未验收范围见[新空间真实对话记录](../../docs/testing.md#新空间真实mcp对话2026-11-10)。
