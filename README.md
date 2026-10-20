# DeepSeek Harness Cloud

通过浏览器创建和使用 DeepSeek Harness 工作空间，统一管理模型、资源和持久数据。

当前版本 **V0.1**，面向单组织、可信内部团队，使用 Docker 单机部署。验收进度见[测试验收](docs/testing.md#11-当前验证范围与剩余工作)。

[功能概览](#功能概览) · [本地开发](#本地开发) · [工程结构](#工程结构) · [文档入口](#文档入口)

## 功能概览

- **工作空间**：选择 CPU、内存、存储和模型提供商，支持创建、进入、启停及删除。
- **模型管理**：管理个人提供商账号、密钥、允许使用的模型及默认模型，通过 Go 模型网关接入官方或兼容服务。
- **数据管理**：保留工作目录与 DSH 数据，浏览 `/data/workspace` 和 `/data/dsh`，支持文件预览、Office / PDF 预览及下载；已删除工作空间的数据可单独永久释放。
- **原生扩展**：启停内置插件、安装第三方 DSH 组合包、保存 Agent 预设。
- **平台管理**：内置登录与账号管理、角色权限、实例 / CPU / 内存 / 存储四类额度及操作日志。

停止释放计算资源，停止或软删除仍保留数据并计入存储额度。备份、恢复和回收规则见[存储数据管理](docs/requirements.md#612-工作空间存储数据管理req-19)。

## 本地开发

### 环境准备

需要 Go **1.27.2**、Node.js **24.x**、npm、Docker Engine 和 Docker Compose。

在仓库根目录执行，初始化本地依赖并启动 API 与内置身份服务：

```bash
npm ci --prefix apps
make dev-init
make auth-init
make infra-up
make migrate
make auth-apply
make run-auth
```

在另一终端启动控制台：

```bash
make portal
```

打开 [控制台登录](https://127.0.0.1:42013/login)。API 默认监听 `http://127.0.0.1:18080`，健康检查为 `/livez` 和 `/readyz`。

| 本地账号 | 密码文件 |
| --- | --- |
| `admin@harness.local` | `.local/auth/admin-password` |
| `member@harness.local` | `.local/auth/member-password` |

随机密码、密钥和本机配置保存在忽略提交的 `.local/`。初始化命令拒绝覆盖已有文件；`make infra-stop` 停止数据库与 Redis 并保留数据卷。

### 启用工作空间

以上步骤启动控制台与身份服务。创建和进入工作空间，还需配置运行镜像、Terraform 模板、持久存储、Worker、访问入口和模型网关，完整步骤见[部署运维](docs/deployment.md)。

启用实例访问入口后，控制台地址为 [portal.harness.localhost:42013](https://portal.harness.localhost:42013/)。本地开发需信任 `.local/auth/cert.pem`，以访问新建实例的独立站点。

配置完成后，管理员在“平台配置”设置默认资源和系统上限，成员在“模型提供商”添加账号并完成校验，即可创建工作空间。

### 常用命令

| 命令 | 用途 |
| --- | --- |
| `make portal` | 启动前端开发服务 |
| `make check` | 格式、静态检查与 TypeScript 类型检查 |
| `make test` | 运行单元测试与 Go 竞态检查 |
| `make build` | 构建后端与控制台 |
| `make ci` | 依次执行检查、测试与构建 |
| `make portal-preview` | 预览已构建的控制台，需先执行 `make build` |

数据库集成测试、浏览器测试和真实运行验收见[测试验收](docs/testing.md)。

## 工程结构

后端采用 Go、PostgreSQL、Redis；前端采用 React、TypeScript、Vite 和 Ant Design。

```text
apps/             浏览器控制台与前端测试
services/         API、Worker、访问网关、模型网关及 hcctl 运维工具
infrastructure/   DSH 运行适配、Terraform 模板、存储与网络执行器
db/               数据结构与版本化迁移
deploy/           本地依赖部署配置
docs/             需求、架构、部署与验收文档
```

## 文档入口

[文档总览](docs/README.md)提供阅读顺序和各文档的维护范围。

| 主题 | 文档 |
| --- | --- |
| 产品与流程 | [功能需求](docs/requirements.md) |
| 架构与实现 | [系统设计](docs/design.md) · [后端](docs/backend.md) · [前端](docs/frontend.md) |
| 接口与数据 | [接口说明](docs/api.md) · [OpenAPI](api/openapi.yaml) · [数据设计](docs/data-model.md) |
| 部署与安全 | [部署运维](docs/deployment.md) · [安全设计](docs/security.md) |
| 验证与决策 | [测试验收](docs/testing.md) · [架构决策](docs/decisions/README.md) |

## 许可证

原有代码许可见 [LICENSE](LICENSE)。含 Coder 衍生代码的组合程序适用 **AGPL-3.0-only**，范围与来源见 [COPYING](COPYING) 和[第三方说明](third_party/coder/README.md)。发布网络服务时应提供对应源码及构建安装材料。
