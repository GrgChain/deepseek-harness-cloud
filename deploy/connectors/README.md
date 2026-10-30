# MCP连接器部署与复验

本文提供现有连接器的配置和复验步骤。功能与模块见[实施说明](../../docs/connectors-implementation.md)，交付要求见[REQ-23](../../docs/requirements.md#617-连接器mcpreq-23)，历史结果见[测试验收](../../docs/testing.md)。文中的命令是复验入口，不代表本次整理重新执行了测试或改变部署。

## 1 部署前提

工作台入口为 `/app/connectors`。平台API须具备PostgreSQL、Redis、登录会话、数据加密密钥及公网HTTPS出口；用户需要本人连接的读取/管理/调用权限。平台作为远程MCP客户端，不修改DSH。支持Streamable HTTP、无认证/Bearer/OAuth 2.0 + PKCE，协议选择为 `2026-07-28` 或显式 `2025-11-25`。

执行 `make migrate` 应用迁移49～51并更新API角色授权，再更新平台API原生进程；本机 `make run-auth` 包含迁移及初始化步骤。API依赖就绪后即可执行登录测试，无请求限流或启动冷却。无需通过重启DSH处理平台认证问题。

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

38种目录条目均支持用户自行填写服务地址和认证信息，19种官方远程服务默认有推荐地址（其中9种OAuth）。管理员可通过 `catalog_id` 绑定兼容服务；同目录已有绑定时保留管理员服务，不添加默认绑定。例如：

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

此示例用于说明绑定格式；默认OAuth服务通常无需重复登记。除已支持的腾讯文档、腾讯会议、微云、乐享及滴滴原生认证外，其他需CLI、stdio或旧SSE的条目，先独立部署兼容HTTP MCP适配服务，再由用户填写该服务地址；可选通过平台策略提供推荐地址。目录本身不会部署这些服务。用户可在任意目录条目直接填写个人地址，也可选择推荐地址后修改；“自定义MCP”用于不关联目录的服务。

官方地址及认证格式见[默认地址说明](../../docs/connectors-reference.md#连接器默认地址)。默认地址均可修改，平台已有同目录服务时优先使用平台服务；公网地址无需预先登记。

## 3 OAuth配置

内置Ardot、Notion、OpenAlex、滴答清单、企查查企业信息MCP的推荐地址，用户可以修改。每个用户用自己的厂商账号授权；企查查五类聚合和专用解绑仍属待实现范围。

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

### 企查查回调限制

2026-10-26检查时，自定义本机域名 `portal.harness.localhost` 被企查查注册接口拒绝；`localhost`、`127.0.0.1`的回环回调注册被接受。结果见[实网报告](../../docs/connectors-reference.md#oauth-连接器可用性检查)。

本机回调要求浏览器与服务处于同一机器，并满足平台的TLS/会话配置；厂商接受某个HTTP回调不代表当前平台登录可直接迁移到HTTP。云端平台须登记正式HTTPS回调，不能用用户电脑的localhost替代。企查查API Key可避免OAuth回调限制，但当前仅有通用Bearer入口，尚未完成专用双模式及真实Key验收。

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
```

页面测试从 `apps` 目录执行；真实测试需平台、登录账号及独立MCP测试服务就绪：

```bash
cd apps
npm run typecheck
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors.spec.ts --workers=2
HC_REAL_IDENTITY=true HC_REAL_CONNECTORS=true PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/google/chrome/chrome npx playwright test tests/connectors-live.spec.ts --workers=1
```

受控页面使用模拟API；真实页面创建独立成员账号，验证平台配置/工具流程并清理数据，桌面另覆盖Ardot真实注册、授权URL及取消回调。合成工具调用成功不代表真实厂商工具通过；厂商账号同意、成功回调及业务调用须按TC-106～109另行验收。

## 6 排查入口

| 现象 | 检查 |
| --- | --- |
| 平台页面可打开，API不可用 | 检查API `/livez`、`/readyz`及反向代理；前端 `/healthz` 可能返回HTML，不能作为API健康证明 |
| 未登录请求 `/v1/me` 返回401 | 符合未登录行为；完成平台登录后再调用本人连接接口 |
| 回调未到平台 | 核对源站、TLS、会话、厂商登记的完整回调及浏览器访问位置 |
| 注册拒绝或需管理员配置 | 检查厂商回调限制、动态注册能力和私有 `oauth_clients` |
| 元数据或服务不可访问 | 检查DNS、证书、出站网络及逐端点例外；平台不使用环境代理或自动重定向 |
| 授权成功但工具不可调用 | 先验证完整工具目录并手动启用；检查账号权限、连接/目录版本及令牌有效性 |
| 工具调用超时或取消 | 保留调用ID并核对外部结果，不自动重试可能已执行的操作 |

所有结果正文只在本次响应展示，持久记录不含参数、结果或秘密。当前删除连接只清除本地凭据；正式企查查远端解绑流程尚待实现。
