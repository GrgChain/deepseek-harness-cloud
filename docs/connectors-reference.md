# 连接器地址与实网验证

本文汇总默认地址、认证方式和历史实网测试。实网结果以对应测试日期为准，不代表当前厂商授权或业务工具已验证。当前实现与操作步骤分别见[连接器实施说明](connectors-implementation.md)和[部署与复验](../deploy/connectors/README.md)。

- [默认地址与认证](#连接器默认地址)
- [OAuth 实网检查（2026-10-26）](#oauth-连接器可用性检查)
- [开发类 MCP 实网测试（2026-10-26）](#开发类-mcp-实网测试)

## 连接器默认地址

核对日期：2026-10-27。目录内置下列19种官方远程MCP地址，选择连接器后自动填入。地址仍可修改，部署策略中已有同目录绑定时优先使用该服务。默认地址不代表用户已授权或业务工具已经验证。

| 连接器 | 默认地址 | 个人配置 |
| --- | --- | --- |
| 腾讯文档 | `https://docs.qq.com/openapi/mcp` | Token，原样发送Authorization头 |
| 腾讯会议 | `https://mcp.meeting.tencent.com/mcp/wemeet-open/v1` | Token，使用X-Tencent-Meeting-Token头 |
| 腾讯微云 | `https://www.weiyun.com/api/v3/mcpserver` | Token，使用WyHeader的mcp_token字段 |
| 腾讯乐享 | `https://mcp.lexiang-app.com/mcp` | 企业标识company_from和访问令牌 |
| 滴滴 | `https://mcp.didichuxing.com/mcp-servers` | MCP Key，仅在出站时写入key参数 |
| 腾讯设计Ardot | `https://ardot.tencent.com/mcp` | OAuth授权 |
| Notion | `https://mcp.notion.com/mcp` | OAuth授权 |
| OpenAlex | `https://mcp.openalex.org/mcp` | OAuth授权 |
| 滴答清单 | `https://mcp.dida365.com` | OAuth授权 |
| 企查查 | `https://agent.qcc.com/mcp/company/stream` | OAuth授权或Bearer Key，当前只接入企业信息服务 |
| GitHub | `https://api.githubcopilot.com/mcp/` | 个人访问令牌（PAT），标准Bearer认证 |
| Gitee | `https://api.gitee.com/mcp` | 个人访问令牌，标准Bearer认证 |
| GitLab | `https://gitlab.com/api/v4/mcp` | 具有MCP tool资源Execute权限的细粒度个人访问令牌，或切换OAuth；传统PAT不可用 |
| Codeup | `https://openapi-rdc.aliyuncs.com/ai/mcp` | 云效个人访问令牌，标准Bearer认证；Region站改为本组织域名下的 `/ai/mcp` |
| Context7 | `https://mcp.context7.com/mcp` | 支持匿名；也可使用 Context7 API Key 和标准 Bearer 认证 |
| Linear | `https://mcp.linear.app/mcp` | OAuth授权 |
| Sentry | `https://mcp.sentry.dev/mcp` | OAuth授权，可改为组织/项目专属路径 |
| Vercel | `https://mcp.vercel.com` | OAuth授权，按厂商要求登记客户端（如需） |
| Supabase | `https://mcp.supabase.com/mcp` | OAuth授权或个人访问令牌（PAT）；不使用项目anon/service_role Key |

原生认证只作用于精确匹配的官方URL；自定义网关地址使用标准Bearer认证。乐享的企业标识与令牌一起加密，编辑时均不读回；保持原地址时留空保留，更换其中任意一项须一起重新填写。滴滴Key不进入列表、导出或保存的服务URL。所有连接继续执行原有目标校验、DNS固定及重定向限制。

其余19种服务没有可统一预填的兼容公共MCP地址：16种为网关模式，语雀为本地stdio进程，有道云笔记为旧SSE服务，Dify地址由个人应用或工作流发布生成。页面在地址字段说明获取方式并提供接入文档；用户仍可直接配置自己的兼容HTTPS MCP服务。

GitLab自建实例可改为该实例的 `/api/v4/mcp`，群组或实例须允许访问MCP。

地址与认证来源：

- [腾讯文档官方MCP配置](https://developer.cloud.tencent.com/mcp/server/11803)
- [腾讯云Octop远程连接器配置](https://github.com/TencentCloud/Octop/blob/0d65f2ad4b855141572f4ddeb367db5538c5e92e/src/octop/infra/connectors/builder.py)，核对腾讯会议、微云及远程服务的配置格式
- [腾讯乐享官方MCP Skill](https://github.com/tencent-lexiang/lexiang-mcp-skill)
- [滴滴官方MCP开发者文档](https://mcp.didichuxing.com/api)
- 原有5种OAuth服务沿用官方地址与[可用性检查记录](#oauth-连接器可用性检查)
- [GitHub官方MCP](https://github.com/github/github-mcp-server)：官方远程地址与PAT认证
- [Gitee官方MCP](https://gitee.com/oschina/mcp-gitee/blob/master/README_CN.md)：远程地址与Bearer个人令牌
- [GitLab官方MCP](https://docs.gitlab.com/user/model_context_protocol/mcp_server/)：实例地址、MCP开放设置、细粒度个人令牌及OAuth要求
- [云效官方托管MCP指南](https://github.com/aliyun/alibabacloud-devops-mcp-server/blob/master/docs/hosted-mcp-guide.zh-CN.md)：中心站、Region站与Bearer认证
- [Context7官方接入文档](https://context7.com/docs/resources/all-clients)：远程地址及Bearer API Key
- [Linear官方MCP](https://linear.app/docs/mcp)、[Sentry官方MCP](https://mcp.sentry.dev/)、[Vercel官方MCP](https://vercel.com/docs/agent-resources/vercel-mcp)、[Supabase官方MCP](https://supabase.com/docs/guides/ai-tools/mcp)：远程地址与OAuth接入

验证范围包括默认地址预填、认证字段校验、出站认证编码、加密保存、精确目标隔离及本机合成MCP工具调用。真实厂商工具调用仍须使用用户本人的有效凭据完成验证。

Linear、Sentry、Supabase 通过标准保护资源元数据发现授权服务器；Vercel 显式保留官方资源标识末尾的 `/`。2026-10-26的[开发类实网测试](#开发类-mcp-实网测试)验证了Context7匿名业务调用以及Linear、Sentry、Supabase授权入口；Vercel拒绝当时的本机回调。Context7匿名接入请选择“无需认证”，新建连接自动协商兼容协议，API Key可选。

## OAuth 连接器可用性检查

检查日期：2026-10-26。以下为当日工作区与本机Portal的检查快照，未更改生产实现或厂商账号授权；本次文档整理未重新执行这些测试。当前实现见[实施说明](connectors-implementation.md)，需求及用例状态见[测试验收](testing.md#3-需求追踪)。

### 结论

平台 OAuth 基础流程测试通过。Ardot、Notion、OpenAlex、滴答清单已通过真实厂商元数据发现、动态客户端注册和授权入口检查；这些结果只证明能发起授权，尚不证明用户授权后的厂商工具可调用。企查查在动态注册阶段拒绝当前回调地址，需要调整部署地址或完成厂商回调白名单登记。

| 连接器 | 元数据及 PKCE S256 | 动态注册 | 授权入口结果 |
| --- | --- | --- | --- |
| 腾讯设计 Ardot | 通过 | 通过 | HTTP 302，转向厂商 `/open/oauth/authorize` |
| Notion | 通过 | 通过 | HTTP 302，转向 `api.notion.com/v1/oauth/authorize` |
| OpenAlex | 通过 | 通过 | HTTP 302，转向 `openalex.org/oauth/consent` |
| 滴答清单 | 通过 | 通过 | HTTP 302，转向厂商 `/signin` |
| 企查查 | 通过 | HTTP 400 | 未进入授权页；回调地址不符合厂商限制 |

厂商探测通过临时诊断测试调用现有 `oauthConfiguration`、`oauthHTTP` 和 `pinnedTransport`，使用平台实际的 DNS/IP 校验、TLS、禁用环境代理和禁止 HTTP 重定向策略。注册参数与现有授权实现一致，回调为 `https://portal.harness.localhost:42013/v1/connectors/oauth/callback`。未提交用户授权，未取得用户访问/刷新令牌，未调用真实厂商业务工具。诊断测试已移除；动态注册可能在厂商端留下无用户授权的测试客户端。

企查查返回：`invalid_client_metadata`，说明 `redirect_uri must be an allowed loopback URL, private-use URI scheme, or allowlisted redirect URI`。当前自定义本机域名不被当作允许的回环地址；应按厂商要求登记正式回调地址，或使用厂商接受的本机部署地址，并保持平台源站与回调一致。不能仅替换授权请求里的回调 URL。

#### 企查查文档补充核验

使用当时平台的客户端名称 `DSH Cloud`、授权码及刷新 grant、公开客户端认证，向企查查真实注册接口提交以下回调（只做注册，不执行用户授权）：

| 回调源站（路径均为 `/v1/connectors/oauth/callback`） | 实测结果 |
| --- | --- |
| `http://127.0.0.1:42013` | HTTP 201，返回 client_id |
| `http://localhost:42013` | HTTP 201，返回 client_id |
| `https://127.0.0.1:42013` | HTTP 201，返回 client_id |

因此阻碍明确来自当前自定义域名的回调登记限制，企查查支持本机回环回调。以上成功只代表动态注册接受该地址，并未验证这些地址上存在可完成登录及会话回调的 Portal；当前平台证书、源站、登录会话及相关网关配置须一并匹配，远程浏览器中的 localhost 指向用户自己的电脑，不能直接用来替代云端平台地址。未更改现有部署。

企查查也支持 API Key 以 Bearer 方式访问五类 MCP，可避开 OAuth 回调限制。当前平台已有 Bearer 配置入口，但尚未使用真实企查查 API Key 验证。本项目应登记自己的 `/v1/connectors/oauth/callback`，五类 MCP 聚合和真实账号调用仍须在本项目实现并验收。

### 已执行验证

- `HC_TEST_DB_DSN_FILE=... GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race -count=1 -v ./services/internal/connectors`：通过；包含隔离 PostgreSQL、模拟 OAuth 授权码交换、PKCE、会话/Owner 隔离、状态单次消费、刷新轮换、刷新撤销、工具发现和调用。
- `npm run typecheck`：通过。
- `npx playwright test tests/connectors.spec.ts --workers=2`：桌面及移动端共 22 项通过，使用受控 API 响应。
- `HC_REAL_IDENTITY=true HC_REAL_CONNECTORS=true npx playwright test tests/connectors-live.spec.ts --workers=1`：桌面及移动端共 2 项通过。使用真实 Portal 登录、临时成员、Ardot 真实授权发起和取消回调，以及独立 MCP 合成服务的工具发现/选择/调用；测试数据自动清理。

浏览器测试指定 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome`。默认命令在沙箱内无法启动网页服务，移至允许访问本机服务的执行环境后通过。默认域名健康检查最初连接失败；明确解析到 IPv4 后确认 Portal 可访问，未登录的 `/v1/me` 返回预期 HTTP 401。`/healthz` 在前端端口返回页面 HTML，不能把该响应作为 API 健康检查。

### 仍需验证

四家可发起授权的服务仍需由用户完成真实账号登录/同意，验证回调成功、厂商工具发现，以及有相应权限的实际工具调用。真实厂商令牌刷新尚未验证；滴答清单元数据仅声明 `authorization_code`，不能仅凭平台支持刷新认定其会签发 refresh token。

企查查先解决回调白名单，再执行上述真实账号验收。当前内置企查查只连接企业信息 MCP，风险、知识产权、经营和人员等其它 MCP 服务的聚合待实现。

复验步骤见[部署与复验](../deploy/connectors/README.md#5-复验命令)。本报告中的注册及授权入口探测未提交用户同意，后续厂商策略或平台配置变化须重新验证，不能将当日结果作为持续健康状态。

## 开发类 MCP 实网测试

测试时间：2026-10-26 18:53（Asia/Shanghai）。平台地址：`https://portal.harness.localhost:42013`。

本次直接访问六个官方 MCP 地址，并使用真实平台登录、独立临时成员账号及平台 MCP 客户端验证。没有提供第三方访问令牌，也没有完成厂商账号同意。测试结束已清理临时连接和账号。

| 服务 | 实测结果 | 可用性结论 |
| --- | --- | --- |
| Context7 | 平台握手、发现两个工具、启用工具、真实调用均成功；库检索返回1683字符，文档查询返回3493字符 | 已验证可用，匿名模式、协议 `2025-11-25` |
| GitHub | 官方地址返回401，明确要求 Authorization 头；当前环境与管理员账号均无已配置的个人连接 | 需个人PAT，尚未验证业务工具 |
| Linear | 平台动态注册成功，返回官方 `/authorize` 链接；PKCE S256及当前回调地址校验通过 | 授权入口可用，待本人同意和业务调用 |
| Sentry | 平台动态注册成功，返回官方 `/oauth/authorize` 链接；PKCE S256及当前回调地址校验通过 | 授权入口可用，待本人同意和业务调用 |
| Vercel | 平台授权启动返回422；直接复查官方注册接口返回400 `invalid_redirect_uri`，说明回调URI未获批准 | 当前本机回调配置下不可用，须先解决厂商客户端/回调批准 |
| Supabase | 平台动态注册成功，返回官方 `/v1/oauth/authorize` 链接；PKCE S256及当前回调地址校验通过 | 授权入口可用，待本人同意和业务调用 |

### Context7 使用配置

在“连接器 → 添加连接器 → 开发工具 → Context7”中，保留官方地址 `https://mcp.context7.com/mcp`，将认证方式切换为“无需认证”。当前页面已移除协议选择，新建连接会自动协商兼容协议 `2025-11-25`。保存并验证后，在“工具与记录”启用 `resolve-library-id` 和 `query-docs`。

实测调用先检索React，再查询 `/websites/react_dev` 的 `useState` 示例，两个调用均由平台返回 `SUCCEEDED`。匿名服务存在厂商限流；如需更高额度，可按[官方说明](https://context7.com/docs/resources/troubleshooting)切换Bearer认证并填写本人API Key。

当时验证中，Context7收到初始化版本 `2026-07-28` 时协商返回 `2025-11-25`，平台要求显式匹配。2026-10-27已改为新建连接自动协商，无需用户选择兼容版本；此处保留原始实网验证结果。

### Vercel 阻塞

官方授权服务器公开的注册接口为 `https://api.vercel.com/login/oauth/register`。使用当前平台回调 `https://portal.harness.localhost:42013/v1/connectors/oauth/callback` 注册时，返回：

```json
{"error":"invalid_redirect_uri","error_description":"The provided redirect URIs are not approved for use by this authorization server."}
```

应使用厂商批准的客户端和回调配置，再在私有 `oauth_clients` 中配置相同服务URL的客户端信息；若更换平台公开源站，还须同步平台登录及TLS配置。仅修改MCP地址或绕过平台回调不能解决此限制。

### 复验

```bash
cd apps
HC_REAL_DEVELOPMENT_MCP=true PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors-development-live.spec.ts --project=desktop --workers=1
```

该测试保留对所有OAuth入口成功的断言，本次整体结果为失败，失败项为Vercel；Context7的两项业务调用和其余三项OAuth入口检查已经通过。默认不设置 `HC_REAL_DEVELOPMENT_MCP` 时跳过，不将缺少个人授权视为业务工具通过。

本机证据位于忽略提交的 `.local/qa/development-mcp/`：`initialize.json`保存未认证握手响应，`platform-results.json`保存平台调用及OAuth入口结果，`vercel-registration.json`保存注册拒绝原因。报告不保存令牌、客户端密钥、授权state或完整授权链接。
