# OAuth 连接器可用性检查

检查日期：2026-10-26。以下为当日工作区与本机Portal的检查快照，未更改生产实现或厂商账号授权；本次文档整理未重新执行这些测试。当前实现见[实施说明](connectors-implementation.md)，需求及用例状态见[测试验收](testing.md#3-需求追踪)。

## 结论

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

### 企查查文档补充核验

使用当时平台的客户端名称 `DSH Cloud`、授权码及刷新 grant、公开客户端认证，向企查查真实注册接口提交以下回调（只做注册，不执行用户授权）：

| 回调源站（路径均为 `/v1/connectors/oauth/callback`） | 实测结果 |
| --- | --- |
| `http://127.0.0.1:42013` | HTTP 201，返回 client_id |
| `http://localhost:42013` | HTTP 201，返回 client_id |
| `https://127.0.0.1:42013` | HTTP 201，返回 client_id |

因此阻碍明确来自当前自定义域名的回调登记限制，企查查支持本机回环回调。以上成功只代表动态注册接受该地址，并未验证这些地址上存在可完成登录及会话回调的 Portal；当前平台证书、源站、登录会话及相关网关配置须一并匹配，远程浏览器中的 localhost 指向用户自己的电脑，不能直接用来替代云端平台地址。未更改现有部署。

企查查也支持 API Key 以 Bearer 方式访问五类 MCP，可避开 OAuth 回调限制。当前平台已有 Bearer 配置入口，但尚未使用真实企查查 API Key 验证。本项目应登记自己的 `/v1/connectors/oauth/callback`，五类 MCP 聚合和真实账号调用仍须在本项目实现并验收。

## 已执行验证

- `HC_TEST_DB_DSN_FILE=... GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race -count=1 -v ./services/internal/connectors`：通过；包含隔离 PostgreSQL、模拟 OAuth 授权码交换、PKCE、会话/Owner 隔离、状态单次消费、刷新轮换、刷新撤销、工具发现和调用。
- `npm run typecheck`：通过。
- `npx playwright test tests/connectors.spec.ts --workers=2`：桌面及移动端共 22 项通过，使用受控 API 响应。
- `HC_REAL_IDENTITY=true HC_REAL_CONNECTORS=true npx playwright test tests/connectors-live.spec.ts --workers=1`：桌面及移动端共 2 项通过。使用真实 Portal 登录、临时成员、Ardot 真实授权发起和取消回调，以及独立 MCP 合成服务的工具发现/选择/调用；测试数据自动清理。

浏览器测试指定 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome`。默认命令在沙箱内无法启动网页服务，移至允许访问本机服务的执行环境后通过。默认域名健康检查最初连接失败；明确解析到 IPv4 后确认 Portal 可访问，未登录的 `/v1/me` 返回预期 HTTP 401。`/healthz` 在前端端口返回页面 HTML，不能把该响应作为 API 健康检查。

## 仍需验证

四家可发起授权的服务仍需由用户完成真实账号登录/同意，验证回调成功、厂商工具发现，以及有相应权限的实际工具调用。真实厂商令牌刷新尚未验证；滴答清单元数据仅声明 `authorization_code`，不能仅凭平台支持刷新认定其会签发 refresh token。

企查查先解决回调白名单，再执行上述真实账号验收。当前内置企查查只连接企业信息 MCP，风险、知识产权、经营和人员等其它 MCP 服务的聚合待实现。

复验步骤见[部署与复验](../deploy/connectors/README.md#5-复验命令)。本报告中的注册及授权入口探测未提交用户同意，后续厂商策略或平台配置变化须重新验证，不能将当日结果作为持续健康状态。
