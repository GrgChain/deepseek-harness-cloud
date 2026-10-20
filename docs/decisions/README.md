# DSH Cloud 初始版本架构决策

版本：V0.1。范围：单组织Docker可信内部用户。以下是已选择的开发设计；身份、配置、独立存储和持久任务已有模块及真实运行证据，Gateway 与真实浏览器交接已有证据，本机一键运行闭环已验证，完整恢复切换与产品验收待完成。设计选择不等于完整安全测试或产品验收通过。

## 1 合理性与选择

| 决策 | 结论 | 选择与代价 | 验证重点 |
| --- | --- | --- | --- |
| [ADR-01 Coder集成](0001-coder-integration.md) | 有条件合理 | 源码抽取内嵌 Go、平台PG保存模板/构建；Terraform效果仍需对账 | 稳定名称/Build/迟到请求 |
| [ADR-03 独立存储](0003-independent-storage.md) | 必须保留 | 硬容量独立资产与原卷标记；增加受信主机管理 | Destroy保卷、缺卷拒绝、恢复 |
| [ADR-04 凭据边界](0004-credential-boundary.md) | 合理并有明确限制 | AES-GCM密文PG、主密钥外置；共享Key仍可能被本人Shell复制 | 无平台秘密泄漏、上游吊销影响 |
| [ADR-05 原生认证](0005-native-auth.md) | 必须保留 | 服务端Token/Cookie交换，原生Cookie不进浏览器 | 结构化凭据通道、Host/Origin/WS |
| [ADR-06 持久操作](0006-outbox-reconciliation.md) | 必须保留 | PG Operation/Outbox、租约/原子配额/对账 | 未知占槽、失租、重复投递 |
| [ADR-07 受控访问](0007-access-gateway.md) | 已选择最小实现 | Go Gateway+挑战/票据；内嵌Workspace负责构建，封闭其他成员入口 | 跨浏览器票据/CSRF/持续撤销/旁路 |
| [ADR-08 后端栈](0008-backend-stack.md) | 用户已确定 | Go + Redis + PostgreSQL；Redis不能成为唯一业务事实 | 故障不扩大授权/额度 |
| [ADR-09 对象与TDD](0009-object-oriented-tdd.md) | 用户已确定 | Go组合/小接口、React函数组件/API用例对象、测试优先 | 不变量/事务/真实边界，不做继承框架 |


身份方案采用平台Go内置OIDC Provider与BFF，账号散列及协议状态由平台PostgreSQL持久化，签名私钥外置；不再部署独立身份服务。选择支持固定平台客户端的授权码+PKCE，避免扩大到身份联邦、动态客户端和SSO生命周期。理由、限制与验证见[后端架构](../backend.md#内置身份模块)、[安全设计](../security.md#内置-oidc-provider)与[测试](../testing.md)。

## 2 真实验证优先级

D0必须证明DSH原生凭据通道与默认工作目录/模型初始化、Workspace稳定名与构建中断接管、Destroy不删原卷、旁路封闭。未证明不得开放真实入口；后续纯领域/API/前端可以按固定契约开发。

共享Key的复制/上游吊销边界属于首版已声明限制；不引入超出范围的计费、复杂凭据代理或多租户隔离承诺。具体运行边界见[系统设计](../design.md#10-变更与开发检查)，验收状态见[测试文档](../testing.md)。

## 3 设计与验证职责

对象/依赖见 [对象设计](../design.md#8-面向对象详细设计)，身份与访问见 [安全](../security.md)，数值见 [配置](../deployment.md#8-配置与业务规则基线)，运行协议见 [运行适配](../backend.md#12-运行适配与外部契约)，证据要求见 [测试](../testing.md)。

各决策由相应模块的开发、安全或运维负责人维护。新增决策使用 [模板](0000-template.md)，编号不复用，范围必须关联REQ。只有实际测试证据才能更新验证结果。

模型接入采用本平台Go原生实现：以固定官方API根和Chat Completions/Messages/generateContent适配控制必要范围；提供商密文、版本、执行路由和运行密钥摘要共用平台PostgreSQL，不额外依赖模型代理镜像、Python运行环境或独立数据库。代价是平台负责协议适配及能力验收；新增模型、思考签名或多模态必须单独设计和验证。
