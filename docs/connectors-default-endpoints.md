# 连接器默认地址

核对日期：2026-10-26。目录内置下列16种官方远程MCP地址，选择连接器后自动填入。地址仍可修改，部署策略中已有同目录绑定时优先使用该服务。默认地址不代表用户已授权或业务工具已经验证。

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
| Context7 | `https://mcp.context7.com/mcp` | Context7 API Key，标准Bearer认证 |
| Linear | `https://mcp.linear.app/mcp` | OAuth授权 |
| Sentry | `https://mcp.sentry.dev/mcp` | OAuth授权，可改为组织/项目专属路径 |
| Vercel | `https://mcp.vercel.com` | OAuth授权，按厂商要求登记客户端（如需） |
| Supabase | `https://mcp.supabase.com/mcp` | OAuth授权或个人访问令牌（PAT）；不使用项目anon/service_role Key |

原生认证只作用于精确匹配的官方URL；自定义网关地址使用标准Bearer认证。乐享的企业标识与令牌一起加密，编辑时均不读回；保持原地址时留空保留，更换其中任意一项须一起重新填写。滴滴Key不进入列表、导出或保存的服务URL。所有连接继续执行原有目标校验、DNS固定及重定向限制。

其余19种服务没有可统一预填的兼容公共MCP地址：16种为网关模式，语雀为本地stdio进程，有道云笔记为旧SSE服务，Dify地址由个人应用或工作流发布生成。页面在地址字段说明获取方式并提供接入文档；用户仍可直接配置自己的兼容HTTPS MCP服务。

地址与认证来源：

- [腾讯文档官方MCP配置](https://developer.cloud.tencent.com/mcp/server/11803)
- [腾讯云Octop远程连接器配置](https://github.com/TencentCloud/Octop/blob/0d65f2ad4b855141572f4ddeb367db5538c5e92e/src/octop/infra/connectors/builder.py)，核对腾讯会议、微云及远程服务的配置格式
- [腾讯乐享官方MCP Skill](https://github.com/tencent-lexiang/lexiang-mcp-skill)
- [滴滴官方MCP开发者文档](https://mcp.didichuxing.com/api)
- 原有5种OAuth服务沿用官方地址与[可用性检查记录](connectors-oauth-availability-2026-10-26.md)
- [GitHub官方MCP](https://github.com/github/github-mcp-server)：官方远程地址与PAT认证
- [Context7官方接入文档](https://context7.com/docs/resources/all-clients)：远程地址及Bearer API Key
- [Linear官方MCP](https://linear.app/docs/mcp)、[Sentry官方MCP](https://mcp.sentry.dev/)、[Vercel官方MCP](https://vercel.com/docs/agent-resources/vercel-mcp)、[Supabase官方MCP](https://supabase.com/docs/guides/ai-tools/mcp)：远程地址与OAuth接入

验证范围包括默认地址预填、认证字段校验、出站认证编码、加密保存、精确目标隔离及本机合成MCP工具调用。真实厂商工具调用仍须使用用户本人的有效凭据完成验证。

新增六项位于“开发工具”分类。Linear、Sentry、Supabase 通过标准保护资源元数据发现授权服务器；Vercel 显式保留官方资源标识末尾的 `/`，服务地址仍使用文档推荐值。2026-10-26已核对官方文档及上述四项公开OAuth元数据；账号授权、令牌交换及真实业务工具调用需要本人凭据另行验证。

随后进行的[开发类实网测试](connectors-development-availability-2026-10-26.md)已验证Context7匿名业务调用，以及Linear、Sentry、Supabase授权入口；Vercel拒绝当前本机回调，尚不可用。Context7匿名接入请使用“无需认证”和兼容协议 `2025-11-25`，API Key为可选的提额方式。
