# Mem0 记忆插件

`@harness-cloud/dsh-mem0-memory@0.1.0` 是 Harness Cloud 编写的独立 DSH 组合包，支持自部署 Mem0 OSS REST 服务。适配并固定测试 DSH `0.2.1-alpha.2`、Node.js 24；不是 Mem0 官方插件，也不支持 `api.mem0.ai` 托管平台 API。

安装后默认停用。在 DSH 插件管理中安装本地 npm `.tgz` 组合包，配置 `mem0-memory-runtime` 后再启用。插件使用原生 Bundle、Config、工具注册和会话事件接口，无安装脚本，不修改 DSH。

## 配置

在工作空间的用户配置层 `cordis.patch.yml` 中加入以下配置。DSH Loader 支持 `!!js` 环境引用；不要把实际密钥写入聊天、Git 或公开配置。

```yaml
- id: mem0-memory-runtime
  disabled: false
  config:
    endpoint: http://mem0:8580
    apiKey: !!js process.env.MEM0_API_KEY
    userId: !!js process.env.MEM0_USER_ID
    autoRecall: true
    autoCapture: false
    captureAssistant: false
    infer: false
    topK: 5
```

`endpoint`、`apiKey`、`userId` 必填。地址必须能从 DSH 容器访问；宿主机的 `localhost` 不是工作空间。项目本机服务使用 `http://mem0:8580`，工作空间还需接入可访问它的网络；宿主机调试地址 `http://127.0.0.1:18583` 仅供宿主机调用。不要将平台内部签名接口 `18580/mem0` 填到此处。自建远程服务应使用 HTTPS。

`userId` 是此独立插件固定使用的 Mem0 命名空间。检索和写入始终携带它，工具不能改写；只向同一位用户的受信任工作空间配置同一 ID 和凭据。此命名空间**不会自动与平台记忆连接中的命名空间共享**，插件直写的记忆也不会自动出现在平台的连接管理列表。凭据能访问哪些数据最终由服务端决定，切勿向不同用户分发同一管理员密钥。插件不持久化密钥或会话队列。

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `autoRecall` | `true` | 在新的用户轮次检索一次并注入相关记忆 |
| `autoCapture` | `false` | 用户显式开启后，自动发送人类输入的可见文本 |
| `captureAssistant` | `false` | 开启自动采集后，可额外保存助手可见回复；不含思考或工具调用 |
| `infer` | `false` | 原文直存；开启后服务端可能调用其配置的模型并产生费用 |
| `topK` | `5` | 检索条数，1–20 |
| `timeoutMs` | `10000` | 每次请求超时，100–60000 毫秒 |
| `maxContentChars` | `12000` | 单次写入和单条返回记忆长度上限，100–262144 |
| `maxQueryChars` | `4000` | 检索问题长度上限，1–4000 |
| `maxContextChars` | `6000` | 自动召回上下文总长度上限，100–32000 |
| `maxResponseBytes` | `262144` | HTTP 响应上限，1024–1048576 字节 |

本项目的本地 REST 测试服务只接受 `infer: false`。自动采集默认关闭；启用意味着之后的用户文本会发送到所配置 Mem0 服务，开启 `captureAssistant` 还会发送助手回复。它不只识别“请记住”语句。关闭或卸载插件不会删除已保存的数据。

## 能力与边界

- `mem0_search`：按问题检索固定用户、`personal` scope 的记忆，可指定 1–20 条；不返回密钥。
- `mem0_remember`：保存用户明确要求记住的文本，展示完整内容、目标服务和用户标识，并要求单次人工确认。没有确认服务、拒绝或取消时不写入。
- `mem0_status`：只读查看开关、待保存数、成功/失败/丢弃数和通用错误；不含密钥、记忆正文或服务端原始错误。
- 自动召回只读取当轮 `source.kind=user` 的输入，每轮至多一次；只注入带来源标记的不可信参考内容，保留 DSH 的原始决策字段。
- 自动采集读取后续提交的 `session/event`，不扫描或重新保存旧 `session.events`。只采集人类输入；助手回复需额外开启。系统、工具、其他插件的注入、附件、思考和子代理会话均不采集。
- 在 `turn/end` 和 `session/flush` 保存排队文本，每条折叠为一条 Mem0 用户消息，附带会话/消息来源元数据。使用稳定消息 ID 去重，同一进程中同一条消息不会重复发送。
- 存储服务断开或返回错误时，不阻断普通自动召回；自动写入失败写入状态并记录不含正文的通用警告。手动保存明确返回失败，HTTP 成功但缺少回执也不报告保存成功。
- 不自动重试非幂等写入。超时可能已在远端完成，请先检查服务再重新保存。队列只在内存中，进程崩溃或停用插件时未发送内容可能丢失；停用会取消在途请求，不保证跨重启恰好写入一次。
- 为控制内存，最多跟踪 100 个主会话，每个会话跟踪 2000 个消息 ID，全部会话合计最多缓冲 100 条、1 MiB 文本（含在途请求）；超限或单条超长时跳过，并在状态中增加 `dropped`，不会悄悄截断保存正文。

## 本地开发与打包

从仓库安装好的固定 DSH 依赖执行测试，不安装或下载第三方包：

```bash
cd infrastructure/plugins/mem0
npm test
npm run check
npm pack --ignore-scripts
```

测试使用原生 DSH `Config`、`createUserMessage`、`defineTool` 和本机 HTTP fixture，覆盖会话过滤、每轮去重、单次确认、固定身份、超时、限额、错误与不重试。真实 Mem0 SDK 和 Qdrant 的验证由本仓库本地集成环境执行。

在平台插件库离线添加生成的 `.tgz` 包即可保存并下载该原包。添加到插件库与在 DSH 安装/启用是不同步骤；此包没有发布到公共 npm。
