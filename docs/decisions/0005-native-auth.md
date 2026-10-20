# ADR-05 保留DSH原生认证并由服务端持有会话

- 版本：V0.1
- 范围：单组织Docker初始版本
- 状态：已选择设计，待D0固定版本验证
- 负责人职责：集成与安全开发
- 关联需求：REQ-02、REQ-06、REQ-11

## 1 问题与依据

平台Owner授权不能替代DSH原生认证。官方连接文档明确原生GET根路径Token交换、签名Cookie和Host/Origin/WS校验，不支持用Authorization Token代替。

原生Token取得、默认工作空间初始化及服务端代理兼容性尚无真实原型证据，必须先做D0；不能因文档存在宣称一键入口已经可运行。

## 2 选择

启动适配包使用固定Web profile，禁止打印Token URL，通过受限结构化通道向可信控制面提供必要凭据。Gateway服务端完成原生根交换和Cookie校验；原生Cookie按实例/进程代次加密存PG，不返回浏览器。公开浏览器只持平台Gateway会话。

平台协议已在 [安全](../security.md) 和 [OpenAPI](../../api/openapi.yaml) 固定，原生细节由 [Worker 运行适配](../backend.md#12-运行适配与外部契约) 隔离。停止/注销/失权由Gateway撤销与WS周期检查，不依赖DSH单用户logout。

## 3 备选与代价

浏览器持原生Cookie会增加直接访问绕过控制面的风险，首版使用服务端Cookie jar。人工输入Token和关闭原生认证都不满足功能/安全需求。通用日志抓取启动URL会泄漏秘密，不采用。

维护最小启动插件/适配包和Cookie jar增加开发工作，但边界集中且可写契约测试。主机/本人Shell可能读取原生签名秘密属于可信内部用户边界；平台必须封闭外部直连旁路。

## 4 验证

D0锁commit/digest并验证结构化凭据通道，无需修改核心认证；若上游缺受支持访问点，先完成限于启动适配层的最小原型并重新验证，不降低认证要求。TC-07～11、TC-14、TC-38、TC-41验证真实浏览器/原生会话、Host/Origin/WS、重放、进程重启和撤销，当前均未执行。

技术依据：[DSH原生连接认证](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/client/connection/README.md)、[Web部署](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/user/guide/public-deployments.md)。平台Cookie代理方案是本项目设计，仍需真实兼容性验证。
