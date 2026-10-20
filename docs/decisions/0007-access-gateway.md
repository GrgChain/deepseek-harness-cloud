# ADR-07 固定子域与Go受控访问模块

- 版本：V0.1
- 范围：单组织Docker初始版本
- 状态：已选择设计，待实现验证
- 负责人职责：后端与安全开发
- 关联需求：REQ-02、REQ-06、REQ-11

## 1 问题

需要实时平台Owner、原生DSH认证、固定路由和长连接撤销。仅复用Coder App入口不能证明平台权限代次与DSH原生会话同步，开发必须有一个明确入口契约。

## 2 选择

首版实现Go最小访问模块，使用标准反向代理和固定实例子域，浏览器挑战/一次票据→Gateway会话→服务端原生Cookie代理。模块可在同工程运行，不建设通用多协议网关或独立服务平台。内嵌Workspace模块负责构建元数据与真实Owner校验，不开放成员Coder App/端口共享/SSH作为第二入口。

挑战/票据/Origin/Cookie/撤销已在 [安全](../security.md) 固定，路径及payload见 [接口](../api.md)。原生协议见 [运行适配](../backend.md#12-运行适配与外部契约)。每请求查询PG授权，WS每10秒检查，故障关闭，目标撤销≤30秒。

## 3 备选与代价

Coder App代理可减少HTTP转发代码，但仍需补平台授权、票据、原生Cookie和WS撤销；首版使用一个最小Go入口以固定契约。今后若复用现成代理，须通过等价的安全测试后另行决策。直接公开容器端口/任意URI不满足安全边界。

新增Gateway会话表、Cookie jar与连接管理有维护代价；采用标准库且只支持固定DSH后端，避免通用动态路由。依赖DNS/TLS、网络隔离和真实浏览器验证。

## 4 旁路与验证

不提供Coder管理/Agent端口代理，模板不发布coder_app，DSH不发布主机公网端口；若实际部署不能封闭旁路，预检失败，禁止开放试点。不能仅隐藏Portal按钮。

TC-07～11、TC-14、TC-26、TC-28、TC-38验证Cookie/Host/Origin/WS、重放、跨用户/浏览器/实例拒绝、失权及直连/共享端口旁路。当前未执行。

技术依据：[Coder Apps](https://coder.com/docs/admin/templates/extending-templates/web-ides)、[Port forwarding](https://coder.com/docs/admin/networking/port-forwarding)。这些能力与许可差异是评估依据，Go访问模块的选择与撤销保证由本项目设计和验证负责。
