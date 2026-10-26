# 开发类 MCP 实网测试

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

## Context7 使用配置

在“连接器 → 添加连接器 → 开发工具 → Context7”中，保留官方地址 `https://mcp.context7.com/mcp`，将认证方式切换为“无需认证”，在“更多设置”中使用兼容协议 `2025-11-25`。保存并验证后，在“工具与记录”启用 `resolve-library-id` 和 `query-docs`。

实测调用先检索React，再查询 `/websites/react_dev` 的 `useState` 示例，两个调用均由平台返回 `SUCCEEDED`。匿名服务存在厂商限流；如需更高额度，可按[官方说明](https://context7.com/docs/resources/troubleshooting)切换Bearer认证并填写本人API Key。

Context7收到初始化版本 `2026-07-28` 时协商返回 `2025-11-25`；平台采用显式协议匹配，因此此地址应使用已验证的兼容版本。

## Vercel 阻塞

官方授权服务器公开的注册接口为 `https://api.vercel.com/login/oauth/register`。使用当前平台回调 `https://portal.harness.localhost:42013/v1/connectors/oauth/callback` 注册时，返回：

```json
{"error":"invalid_redirect_uri","error_description":"The provided redirect URIs are not approved for use by this authorization server."}
```

应使用厂商批准的客户端和回调配置，再在私有 `oauth_clients` 中配置相同服务URL的客户端信息；若更换平台公开源站，还须同步平台登录及TLS配置。仅修改MCP地址或绕过平台回调不能解决此限制。

## 复验

```bash
cd apps
HC_REAL_DEVELOPMENT_MCP=true PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors-development-live.spec.ts --project=desktop --workers=1
```

该测试保留对所有OAuth入口成功的断言，本次整体结果为失败，失败项为Vercel；Context7的两项业务调用和其余三项OAuth入口检查已经通过。默认不设置 `HC_REAL_DEVELOPMENT_MCP` 时跳过，不将缺少个人授权视为业务工具通过。

本机证据位于忽略提交的 `.local/qa/development-mcp/`：`initialize.json`保存未认证握手响应，`platform-results.json`保存平台调用及OAuth入口结果，`vercel-registration.json`保存注册拒绝原因。报告不保存令牌、客户端密钥、授权state或完整授权链接。
