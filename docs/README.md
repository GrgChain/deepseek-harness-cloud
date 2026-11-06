# DSH Cloud 文档

V0.2，面向单组织、可信内部团队的 Docker 部署。启动与开发命令见[根 README](../README.md#本地开发)。

| 任务                             | 文档                                                                                                                                                                                                                               |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 理解功能和业务规则               | [功能需求](requirements.md)                                                                                                                                                                                                        |
| 理解架构、状态和调用流程         | [系统设计](design.md)、[后端](backend.md)、[前端](frontend.md)                                                                                                                                                                     |
| 对接接口或数据结构               | [接口语义](api.md)、[OpenAPI](../api/openapi.yaml)、[数据设计](data-model.md)                                                                                                                                                      |
| 部署、升级、备份和排障           | [部署运维](deployment.md)、[安全设计](security.md)                                                                                                                                                                                 |
| 运行测试、查看覆盖和未完成项     | [测试验收](testing.md)                                                                                                                                                                                                             |
| 配置记忆与知识库服务             | [上下文服务部署](../deploy/context/README.md)                                                                                                                                                                                      |
| 配置 MCP 地址、认证和 OAuth      | [连接器部署](../deploy/connectors/README.md)                                                                                                                                                                                       |
| 安装、配置和维护记忆插件         | [分发总览](../infrastructure/plugins/README.md)、[Mem0](../infrastructure/plugins/mem0/README.md)、[Graphiti](../infrastructure/plugins/graphiti/README.md)、[OpenViking 原包说明](../infrastructure/plugins/openviking/README.md) |
| 验证 Graphiti 原生插件与会话归档 | [Graphiti DSH 测试用例](testing-graphiti-dsh.md)                                                                                                                                                                                   |
| 核对 Workspace 上游来源及许可    | [上游集成边界](workspace-upstream.md)                                                                                                                                                                                              |

需求说明交付要求；架构说明当前实现；测试文档说明验证范围和未完成项。接口字段以 OpenAPI 为准，配置及升级步骤在部署文档维护，插件安装步骤跟随各自 README。历史测试结果不能作为当前服务健康或完整验收的证明。

## 建议阅读顺序

- 初次使用：根 README 的本地开发 → 功能需求的核心流程 → 部署运维的运行环境配置。
- 参与开发：系统设计 → 对应的后端或前端架构 → 接口语义与数据设计 → 测试验收。
- 部署维护：部署运维 → 安全设计 → 按需阅读上下文服务、连接器或插件说明 → 测试验收中的复验命令。

## 专题索引

| 专题                   | 规则与实现                                                                                      | 配置与验证                                                                                      |
| ---------------------- | ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| 工作空间运行与上游边界 | [系统设计](design.md)、[上游集成边界](workspace-upstream.md)                                    | [部署运维](deployment.md)、[运行验证](testing.md#10-内嵌-workspace-验证)                        |
| 记忆、知识库及会话归档 | [功能需求](requirements.md#615-可迁移记忆系统req-21)、[后端实现](backend.md#外接记忆与知识库)   | [服务部署](../deploy/context/README.md)、[记忆复验](testing.md#记忆与知识库复验)                |
| 技能导入与迁移         | [个人技能包](frontend.md#个人技能包)                                                            | [来源出口配置](deployment.md#技能包来源出口)、[技能验证](testing.md#技能包与来源验证)           |
| MCP 与 OAuth           | [连接器接口](api.md#工作台连接器req-23api-100111api-115116)                                     | [连接器部署与复验](../deploy/connectors/README.md)                                              |
| 原生记忆插件及离线分发 | [插件规则](requirements.md#618-插件req-24)、[包与版本维护](../infrastructure/plugins/README.md) | [Mem0 配置](../infrastructure/plugins/mem0/README.md)、[Graphiti 验收](testing-graphiti-dsh.md) |

## 文档维护约定

更新功能时，同时核对需求、接口、实现、部署和测试中涉及的说明。当前行为写在对应主题文档，带日期的执行记录保留当时版本、环境及验证限制。新增专项指南应加入本页，并从所属主文档链接；插件版本和安装约束以分发总览及各插件 README 为入口。

## 当前实现基线

| 项目           | 当前值                                                   | 核对来源                                                                               |
| -------------- | -------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| 平台与运行工具 | V0.2；Go工具链1.27.2、Node.js 24                         | [go.mod](../go.mod)、[控制台依赖](../apps/package.json)                                |
| DSH运行版本    | 0.2.1-alpha.2                                            | [运行依赖](../infrastructure/runtime/package.json)                                     |
| 随附记忆插件   | Mem0 / Graphiti 0.2.0；OpenViking官方原包0.5.31          | [分发总览](../infrastructure/plugins/README.md)                                        |
| 平台数据库     | 迁移至67；既有迁移不可覆写                               | [迁移目录](../db/migrations)、[数据设计](data-model.md#当前插件目录与会话归档迁移6167) |
| 连接器目录     | 38项；19项默认远程地址，其中9项OAuth                     | [内嵌目录](../services/internal/connectors/catalog.json)                               |
| 本地初始资源   | 1 CPU、2 GiB内存、10 GiB存储；保留已有配置               | [初始化实现](../services/internal/devlocal/resources.go)                               |
| IP访问         | 统一HTTPS端口，工作空间路径为`/dsh/<实例UUID>/`          | [部署运维](deployment.md)                                                              |
| 工程检查       | 可分别执行静态检查、单元测试和构建；格式聚合入口配置缺失 | [当前限制与命令](testing.md#工程检查现状)                                              |

旧版本号在迁移说明和带日期的执行记录中保留，以说明当时状态；当前安装、部署及下载步骤以本表及各主题的现行章节为准。
