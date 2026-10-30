# DSH Cloud 文档总览

版本：V0.1。范围：单组织、可信内部用户、Docker 单机部署。需求、当前实现和验收结果分别维护，完整产品验收进度见[测试验收](testing.md#11-当前验证范围与剩余工作)。

## 1 阅读顺序

| 顺序 | 文档 | 用途 |
| --- | --- | --- |
| 1 | [功能需求](requirements.md) | REQ-01～20初始范围与REQ-21～25扩展范围、业务规则、页面流程和验收条件 |
| 2 | [系统设计](design.md) | 架构、对象、状态、身份映射、事务边界和关键时序 |
| 3 | [后端](backend.md)、[前端](frontend.md) | 当前模块、技术栈、运行适配及页面交互 |
| 4 | [数据设计](data-model.md)、[SQL](../db/schema.sql)、[接口说明](api.md)、[OpenAPI](../api/openapi.yaml) | 表、锁、迁移、输入输出及错误契约 |
| 5 | [部署运维](deployment.md)、[安全设计](security.md) | 环境配置、受控出口、凭据、身份、备份与恢复 |
| 6 | [测试验收](testing.md)、[架构决策](decisions/README.md) | REQ→模块/API/数据/TC追踪、执行证据、未完成项与决策理由 |

本地启动和常用命令见[根README](../README.md#本地开发)。

## 2 文档职责

| 内容 | 维护位置 | 更新规则 |
| --- | --- | --- |
| 功能与验收要求 | 功能需求 | “必须”表示交付条件，不表示已经实现或通过 |
| 架构与当前行为 | 系统设计、后端、前端、数据设计 | 对照现有代码，区分实现与计划 |
| 公开API结构 | OpenAPI | 接口说明补充使用语义，不定义另一套DTO |
| 配置数值与运行步骤 | [配置基线](deployment.md#8-配置与业务规则基线)、部署说明 | 与实际环境变量、命令及合法值一致 |
| 已通过与待验收项 | 测试验收及其链接的专项报告 | 写清环境、日期、执行范围、跳过和剩余限制；历史记录不代表当前重新执行 |
| 模块实施说明 | 专项实施文档 | 说明当前入口、模块和能力边界，测试明细链接到验收文档 |
| 历史选择与代价 | ADR | 保留历史背景，当前状态链接到实现和测试 |

需求、对象、数据/API、页面和测试随同一功能变更同步。精确依赖以 `go.mod/go.sum`、`apps/package-lock.json`、Provider lock及批准镜像摘要为准；数据库已应用迁移保持校验和，不通过整理文档改写迁移。

## 3 专项资料

| 主题 | 需求与规则 | 实现、部署与验证 |
| --- | --- | --- |
| 用户、角色与权限 | REQ-02、REQ-13～15 | [后端身份与授权](backend.md)、[TC-51～60及撤销验证](testing.md) |
| 个人模型与平台模型网关 | REQ-16～17、[模型规则](requirements.md#61-默认配置与凭据) | [后端](backend.md)、[部署运维](deployment.md)、[TC-61～76](testing.md) |
| 用户资源配额与默认值 | REQ-18、[默认配置](requirements.md#61-默认配置与凭据) | [配置基线](deployment.md#8-配置与业务规则基线)、[TC-84～89](testing.md) |
| 工作数据与保留恢复 | [REQ-19](requirements.md#612-工作空间存储数据管理req-19) | [数据设计](data-model.md)、[部署运维](deployment.md)、[TC-91～98](testing.md) |
| 工作空间插件与Agent预设 | [REQ-07](requirements.md#613-工作空间插件与-agent-预设req-07) | [运行契约](backend.md#129-插件管理与预设设置)、[部署](deployment.md#工作空间插件与-agent-预设)、[专项验收](testing.md#工作空间插件与-agent-预设验收) |
| 插件 | [REQ-24](requirements.md#618-插件req-24) | [实施与维护](plugins-marketplace.md)、[前端](frontend.md#插件req-24)、[TC-110](testing.md#2-用例目录)；平台目录，支持官方和第三方，不改DSH内部 |
| 个人技能包 | [REQ-20](requirements.md#614-个人技能包与迁移req-20) | [前端技能流程](frontend.md#个人技能包)、[来源出口](deployment.md#技能包来源出口)、[TC-99及专项验证](testing.md) |
| 创建工作空间扩展配置 | [REQ-25：五类选择与生效规则](requirements.md#619-创建工作空间扩展配置req-25) | [TC-111～118](testing.md#2-用例目录)、[当前状态](testing.md#11-当前验证范围与剩余工作)；五类创建选择、受控调用/检索及兼容官方插件已实现，完整矩阵分项验收 |
| 记忆与知识库 | [REQ-21](requirements.md#615-可迁移记忆系统req-21)、[REQ-22](requirements.md#616-外接知识库req-22) | [实施记录](context-implementation.md)、[独立服务部署](../deploy/context/README.md)、[TC-100～101](testing.md) |
| 连接器与OAuth | [REQ-23](requirements.md#617-连接器mcpreq-23)、[厂商验收](requirements.md#6171-oauth部署与厂商验收)、[企查查完整接入](requirements.md#6172-企查查完整接入扩展交付) | [实施说明](connectors-implementation.md)、[地址与实网验证](connectors-reference.md)、[部署与复验](../deploy/connectors/README.md)、[TC-102～109](testing.md) |

连接器通用OAuth已经实现；企查查五类聚合、专用认证切换及远端解绑仍须开发，真实厂商账号授权和业务工具调用尚未完整验收。注册成功或进入授权页不等于工具可用，状态以TC-106～109为准。

## 4 术语与实现边界

全称 DeepSeek Harness Cloud，简称 DSH Cloud；DSH是工作空间中运行的应用。平台后端为Go、PostgreSQL、Redis，前端为React/TypeScript。平台内嵌Workspace/TemplateVersion/Build编排模块，受信Worker调用Terraform创建Docker资源；身份、权限、配额、模型和审计由平台管理。

`RUNNING`表示计算运行，`ready`要求五维健康有效，`can_enter`还须满足当前身份和权限。模型异常时可按授权进入诊断，不能据此宣称Agent任务可执行。平台与Workspace组织标识的映射见[系统设计](design.md#91-组织和身份标识)。

创建工作空间可选技能、连接器、记忆、知识库及插件；技能复制完整包，外部能力仅按选择及授权范围受控使用，兼容官方插件固定版本启用。平台不自动收集会话或写入记忆；未验证兼容性、缺依赖或需额外配置的插件禁止选择，仍可在DSH原生界面按既有规则管理。工作空间原生插件与预设使用DSH自己的界面和RPC，插件广场仍为独立只读目录。工作盘保留、独立备份和故障恢复分别验收，本机演练不替代独立故障域验证。
