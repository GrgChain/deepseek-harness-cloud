# DSH Cloud 文档

V0.2，面向单组织、可信内部团队的 Docker 部署。启动与开发命令见[根 README](../README.md#本地开发)。

| 任务 | 文档 |
| --- | --- |
| 理解功能和业务规则 | [功能需求](requirements.md) |
| 理解架构、状态和调用流程 | [系统设计](design.md)、[后端](backend.md)、[前端](frontend.md) |
| 对接接口或数据结构 | [接口语义](api.md)、[OpenAPI](../api/openapi.yaml)、[数据设计](data-model.md) |
| 部署、升级、备份和排障 | [部署运维](deployment.md)、[安全设计](security.md) |
| 运行测试、查看覆盖和未完成项 | [测试验收](testing.md) |
| 配置记忆与知识库服务 | [上下文服务部署](../deploy/context/README.md) |
| 配置 MCP 地址、认证和 OAuth | [连接器部署](../deploy/connectors/README.md) |
| 安装和配置独立记忆插件 | [Mem0](../infrastructure/plugins/mem0/README.md)、[OpenViking 原包说明](../infrastructure/plugins/openviking/README.md) |
| 核对 Workspace 上游来源及许可 | [上游集成边界](workspace-upstream.md) |

需求说明交付要求；架构说明当前实现；测试文档说明验证范围和未完成项。接口字段以 OpenAPI 为准，配置及升级步骤在部署文档维护，插件安装步骤跟随各自 README。历史测试结果不能作为当前服务健康或完整验收的证明。
