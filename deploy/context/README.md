# 外接记忆与知识库

独立Compose项目 `hc-context`，不修改现有DSH镜像、容器或工作数据。平台仍使用当前Go/Vite部署。测试使用独立卷、回环端口和合成数据；API服务通过私有批准策略访问伴随服务。

```bash
make context-up                    # Mem0、Qdrant、Weaviate、Milvus及配套服务
make context-test                  # 真实读写/召回/隔离/更新/删除及索引故障恢复
```

私有文件生成在Git忽略的 `.local/context/`，目录0700、凭据0600。API配置 `HC_CONTEXT_ENDPOINTS_FILE` 为 `endpoints.local.json` 的绝对路径；`make run-auth`自动识别该文件。新增权限 `memory:read/manage`、`knowledge:read/manage` 内置管理员和成员默认拥有；自定义角色由管理员明确分配。Portal入口 `/app/memory`、`/app/knowledge`。

## 文档解析

知识库文件导入使用自部署的 Docling Serve。`make context-up` 启动知识库配套服务时包含 Docling；也可以单独运行 `make context-docling-up`。默认监听宿主机回环地址 `http://127.0.0.1:18582`，平台 API 可通过 `HC_DOCLING_URL` 配置其他受信任的服务地址。不要将解析接口暴露到公网。

用户选择的文件会发送到平台和 Docling 解析服务，解析得到的 Markdown 可编辑并预览，确认后保存到知识库，原文件不保存到知识库。支持文本、Office、PDF 和图片；扫描件和图片使用 Docling 的 OCR。单文件不超过 16 MiB，提取内容不超过 256 KiB。镜像包含 LibreOffice，用于解析旧版 Office 格式。

知识库文档列表支持按标题搜索和服务端分页，搜索仅覆盖当前连接及所选内容范围，不调用 Embedding 服务。更新平台 API 时须同时更新知识库接入服务。

知识库检索可选最低相关度（余弦相似度，-1 至 1），留空不筛选；语义结果低于阈值时被过滤。混合检索中的关键词命中不受相关度阈值影响。标题下说明检索范围和支持的检索方式；“重置条件”恢复语义检索、返回 5 条和相关度不过滤，清除旧结果并保留检索问题。

## Mem0 本地手动测试

```bash
make context-mem0-up              # 仅启动 Qdrant、Mem0 REST 测试入口和平台接入服务
make context-mem0-test            # 经 REST 验证真实读写、检索、更新、隔离、删除和认证
```

在工作台 `/app/memory` 点击“添加记忆”，选择 Mem0，按下表填写，保存后点击“验证”：

| 字段 | 本机测试值 |
| --- | --- |
| 连接名称 | `Mem0 本地测试` |
| Mem0 服务地址 | `http://mem0:8580` |
| 服务凭据 | 填写 `.local/context/test-key` 文件内容 |

页面填写的是平台接入容器可访问的 Compose 内网地址。宿主机调试地址为 `http://127.0.0.1:18583`，仅绑定回环；`http://127.0.0.1:18580/mem0` 是平台内部签名接口，不能填写到此表单。已有本机策略由 `make context-mem0-up` 补齐 `service_key_file`；平台 API 须加载 `.local/context/endpoints.local.json`（`make run-auth` 自动识别）。

新增的 `mem0` 服务是基于真实 Mem0 2.2.1 SDK 的精简本机测试入口，实现平台所需的四个 [OSS REST 路由](https://github.com/mem0ai/mem0/blob/v2.2.1/server/main.py)：`GET/POST /memories`、`POST /search`、`DELETE /memories/{id}`，不是上游完整服务器或 Dashboard。所有数据接口校验 `X-API-Key`，写入固定 `infer=False`，使用本机 `BAAI/bge-small-zh-v1.5` 512 维向量，不调用外部推理模型。Qdrant collection 为 `hc_workbench_memories`，历史记录保存在独立 `mem0-data` 卷，与原 SDK 接入的 collection 和历史库分开。

验证就绪后，在“管理内容”添加记忆“星河项目每周三下午 15:00 发布，李明负责发布”，预览并确认保存。检索“星河项目什么时候发布？”，应命中这条记忆并能查看原文；测试结束后可删除记录。平台 Mem0 连接通过已接入工作空间手动检索记忆，不提供完整会话自动同步。

需要 DSH 自动召回、确认保存或可选的对话采集时，可在工作台“插件”搜索“Mem0 记忆”，下载本项目维护的 `@harness-cloud/dsh-mem0-memory@0.1.0`，在目标工作空间安装并配置后启用，详见[插件 README](../../infrastructure/plugins/mem0/README.md)。此插件直接连接 Mem0 OSS REST，单独配置 `endpoint`、`apiKey`、`userId`；自动采集默认关闭，用户显式开启后才保存消息。本机服务使用上表 API Key，`infer` 保持 `false`；`http://mem0:8580` 只在目标工作空间能访问 Compose 网络时可用。插件使用独立用户命名空间，其内容不会自动出现在平台记忆连接的管理列表。

工作台真实集成测试会创建并清理专用账号、角色、连接和合成记录；使用本机已安装的 Chromium 时，可用 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` 指定可执行文件路径：

```bash
cd apps
HC_REAL_IDENTITY=true HC_REAL_CONTEXT=true HC_CONTEXT_UI_BACKENDS=mem0 npx playwright test tests/context.spec.ts --project=desktop --grep 'live platform'
```

本机已通过上述 REST 读写与认证测试，以及工作台真实账号创建连接、验证、写入、检索、原文查看和删除测试。

## Docker Embedding 服务

直接使用 [Hugging Face Text Embeddings Inference 官方 CPU 镜像](https://huggingface.co/docs/text-embeddings-inference/en/supported_models)，版本 `cpu-1.9` 并锁定镜像摘要；不维护自写推理服务或 Embedding Dockerfile。默认模型为 `BAAI/bge-small-zh-v1.5`，输出 512 维向量，提供原生 OpenAI 兼容 `/v1/embeddings` 接口。

```bash
make context-embedding-up          # 仅启动 Embedding 服务；首次下载模型
make context-embedding-test        # 真实中文向量、认证、批量和编码校验
```

平台知识库填写：

| 字段 | 配置 |
| --- | --- |
| Embedding 服务地址 | `http://embeddings:8581/v1`（平台接入容器与服务位于同一 Compose 网络） |
| 模型名称 | `BAAI/bge-small-zh-v1.5` |
| 向量维度 | `512` |
| API Key | `.local/context/test-key` 中的私有凭据，不要提交到 Git 或日志 |

宿主机调试地址为 `http://127.0.0.1:18581/v1`，健康检查为 `/health`，模型元信息为 `/info`。宿主端口仅绑定回环，平台接入容器使用上表的内网地址。模型文件保存在独立 `embedding-api-data` 卷，重启复用；首次启动需访问 Hugging Face 下载模型。可用 `HC_CONTEXT_EMBEDDING_MODEL` 指定其他 TEI 支持的模型，并在新建知识库时填写匹配的模型名及维度。

### 手动测试模板

当前本机 Compose 环境可按下表新增知识库，保存后点击“验证”。

| 字段 | 示例值 |
| --- | --- |
| 连接名称 | `Milvus 手动测试` |
| Milvus 服务地址 | `http://milvus:19530` |
| 服务凭据 | 留空（本机测试 Milvus 未启用原生认证） |
| Embedding 服务地址 | `http://embeddings:8581/v1` |
| Embedding 模型名称 | `BAAI/bge-small-zh-v1.5` |
| 向量维度 | `512` |
| Embedding 服务 API Key | 填写 `.local/context/test-key` 文件内容 |

打开“管理内容”，点击“添加文档”，标题填写“星河项目发布流程”，输入以下合成文本，预览并确认保存：

> 星河项目每周三下午 15:00 发布，李明负责发布，王芳确认验收。出现严重故障时停止发布，李明在 15 分钟内回滚到上一个稳定版本。发布记录保存在项目文档的“发布日志”目录。

检索窗口支持语义检索、全文检索和混合检索。全文检索对中文分词后按关键词匹配并排序，不调用 Embedding 服务；混合检索结合全文索引和向量检索，按片段去重并通过 RRF 融合排序。接入服务首次升级时自动为已有可用文档建立全文索引，后续文档更新和删除同步维护。所有方式均只返回当前连接和所选范围内可访问的内容。旧 API 的 `exact` 模式仍按完整文字精确匹配。

检索结果支持选择返回 5、10 或 20 条，并可逐条折叠或全部展开。语义命中的向量相似度来自向量数据库；混合检索的融合分数只用于排序，不等同于向量相似度。仅关键词命中的结果不显示向量相似度。

新保存的文档按章节和段落切分，检索结果显示章节路径。已有文档需在文档列表点击“重建”以应用新规则；具体规则和重建流程见[文档分段与重建](#文档分段与重建)。

保存后检索以下问题，确认结果片段及原文与预期一致：

| 检索问题 | 预期片段中的事实 |
| --- | --- |
| 星河项目什么时候发布？ | 每周三下午 15:00 |
| 谁负责发布和验收？ | 李明负责发布，王芳确认验收 |
| 严重故障后如何处理？ | 停止发布，李明在 15 分钟内回滚到上一个稳定版本 |
| 发布记录放在哪里？ | 项目文档的“发布日志”目录 |

### 文档分段与重建

保存文档时，接入服务使用 `markdown-it-py` 解析 Markdown 标题、段落和表格，使用 LlamaIndex `SentenceSplitter` 处理超长内容。Milvus、Weaviate 和 Qdrant 共用该规则；未识别到 Markdown 结构的文本仍按内容块和长度切分。

- 按标题层级记录章节路径，在同一章节内合并较短的内容块；切换章节时开始新片段。
- 表格优先按行拆分，并在各段保留表头。表头或单行过长时会进一步按长度切分，不能保证每个子片段都保留完整表头。
- 向量化输入包含文档名、章节路径和正文；全文检索索引文档名、章节及正文。检索返回正文片段和章节路径，原文仍保持完整。
- 目标为每段连同标题上下文不超过 384 个接入服务 token。过长内容按句子等边界拆分，重叠最多 48 token；实际重叠可能更少。计数使用 LlamaIndex 默认 `tiktoken` 分词器，与所选 Embedding 模型的分词器和输入长度限制不一定一致。
- 章节路径最多保留 120 个字符；向量化时的标题与章节上下文会进一步截短，以控制片段长度。检索结果展示的正文片段最多 2000 个字符。

升级接入服务后，新保存或编辑的文档使用新规则；已有文档继续使用原有片段和索引，不会自动改写。要更新已有文档：

1. 打开知识库的“管理内容”，在文档列表中找到需要更新的文档。
2. 点击“重建”，等待索引完成，再用全文或混合检索确认章节和片段。

重建会重新调用连接配置的 Embedding 服务并生成新的文档版本；原文不变。失败时最后可用版本仍可检索，状态会显示为旧版可用或索引失败，可重试重建。新版完成前旧版本继续使用旧分段；不要仅修改数据库里的片段或向量，否则片段编号会与索引不一致。

新增 Milvus 连接时填写自己的 REST API 服务地址和原生 Token（或 `用户名:密码`），未启用认证可留空。地址和凭据按连接一起加密保存；编辑时显示地址，凭据不回显，留空保留。地址创建后固定，更换数据库须新建连接，以免将原文、回执及工作空间绑定关联到其他数据库。HTTPS 校验证书；HTTP 仅用于明确允许的私有网络，平台接入容器须能解析并访问该地址，容器内的 `localhost` 不是宿主机。用户地址不覆盖其他连接或全局环境。

Weaviate 和 Qdrant 同样支持填写各自的服务地址和 API Key，未启用认证可留空。每个连接独立保存地址及加密凭据，编辑留空保留凭据；更换地址须新建连接。接入节点需要配置 `service_key_file`，由平台使用该密钥认证伴随服务，原生 API Key 只发送到所选数据库。已有未指定服务地址的连接继续使用原有配置。

三个向量数据库的批准策略指向部署的伴随服务，并配置私有 `service_key_file` 供平台向其认证；`prepare.py` 为本机接入节点补充 `.local/context/test-key`。浏览器只获知 `custom_service` 能力标记，不接触接入密钥或文件路径。每次调用由平台签名携带该连接的原生地址和凭据；伴随服务校验全部 DNS 结果后固定目标 IP，保留 Host 与 TLS SNI，拒绝跳转、回环、链路本地及元数据地址。已有未指定地址的连接继续使用原模式；Milvus 部署默认地址可通过 `HC_CONTEXT_DEFAULT_MILVUS_URL` 设置。

OpenViking 接入节点配置 `service_key_file` 后，也可在添加连接时填写平台接入服务可访问的 OpenViking 地址及其管理员 API Key。接入服务使用管理员凭据创建隔离账户，并使用该账户访问记忆；服务地址变更需要新建连接。已有连接继续使用原有配置。

Mem0 接入节点配置 `service_key_file` 后，可填写自建 Mem0 OSS REST 服务地址及 API Key。该模式使用 Mem0 的 `/memories` 和 `/search` 接口，不适用于 Mem0 云平台 API。

Graphiti 接入节点配置 `service_key_file` 后，可填写 Neo4j Bolt 地址和 `用户名:密码`，由接入节点的 Graphiti SDK 同步处理记忆。地址支持 `bolt+s://`，管理员允许明文时也支持 `bolt://` 私网地址；接入节点固定并校验 DNS 解析结果，禁止回环和链路本地地址。图谱提取继续使用接入节点已批准的模型配置，正文会发送到该模型；更换数据库地址需新建连接。此模式连接 Neo4j，并非 Graphiti 官方参考 REST 服务。

OpenViking、Graphiti还需要批准的推理模型。仅在已获授权时，将 `{ "model", "api_base", "api_key" }` 保存到0600的 `.local/context/model.json`，然后执行：

```bash
python3 deploy/context/prepare.py --enable-model-memory
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile openviking --profile graphiti up -d --build
HC_CONTEXT_TEST_BACKENDS=openviking,graphiti python3 deploy/context/test_services.py
```

本机测试已按用户授权复用平台配置的模型；私有快照不会自动跟随平台撤销，轮换或撤销时须同步更新测试配置并重建容器。不要将模型配置、Compose展开结果或容器环境输出到日志。模型提取可能产生调用费用。手动记忆正文在预览确认后写入；DSH 完整会话同步在创建时选择 OpenViking 连接后默认开启，可手动关闭。

| 服务 | 固定版本 | 本机端口 |
| --- | --- | --- |
| Embedding | Hugging Face TEI cpu-1.9，镜像固定摘要 | 18581 / 内网8581 |
| 文档解析 | Docling Serve CPU v1.36.0，包含 LibreOffice | 18582 / 内网5001 |
| Mem0 | 2.2.1 SDK，经认证伴随服务 | 18580/mem0 |
| Mem0 REST 测试入口 | 2.2.1 SDK，固定 infer=False | 18583 / 内网 mem0:8580 |
| Qdrant | 1.20.0 | 16333 |
| Weaviate | 1.27.0，vectorizer=none | 18085 / gRPC 15051 |
| Milvus | 2.5.6，独立etcd/MinIO | 19531 / 健康19091 |
| OpenViking | 0.4.16，镜像固定摘要 | 仅Compose内网 |
| Graphiti | 0.31.2 SDK + Neo4j 5.26.0 | 仅Compose内网 |

知识库连接的“Embedding模型”统一通过 API 接入，适用于 Milvus、Weaviate、Qdrant。本机或私网部署的模型也需提供平台接入服务可访问的 OpenAI 兼容 `/embeddings` 接口，填写 API Base URL、模型名、实际向量维度与可选 API Key；本机服务使用局域网地址，勿填写浏览器所在电脑的 `localhost`。新建连接必须显式配置 API，不再提供平台内置本地模型选项；已有本地模型连接保留原配置，更换模型需新建连接并重新导入文档。接口不要求支持可选的 `dimensions` 请求参数，平台检查实际返回维度与所填值一致。文档写入及问题检索始终使用该连接的配置，接口错误或维度不符会失败，不回退到本地模型。公网服务使用 HTTPS；自建私网 HTTP 服务遵循接入节点的 HTTP 许可及 DNS/目标校验。

Embedding配置和 API Key 与原连接凭据一起加密，列表和设置只返回非敏感配置及是否已保存密钥。编辑留空 API Key 保留原值，填写新值可更新并重新验证。创建后固定来源、模型、地址和维度；更换模型须新建知识库并重新导入文档，不在已有 collection 上混写其他向量。原有未配置Embedding选项的知识库继续使用本地默认模型，已有记忆服务的Embedding配置保持原部署方式。导入预览按实际来源说明文档和问题的数据去向及潜在外部调用费用。

伴随服务保管外部原文、版本与幂等回执；LlamaIndex分块，所选Embedding模型生成向量，各向量数据库负责检索。导入内容单篇最多256KiB。OpenViking用独立租户密钥；Graphiti按Owner/范围/版本分组，迁移包保留完整图谱字段，对无法表达的目标必须确认语义损失。页面删除内容时，确认一次后自动等待原幂等请求的删除回执，完成后关闭弹窗并刷新列表，无需手动核实；重新打开内容页时，管理用户也会自动继续处理服务端已确认的待完成操作。

这里是本机集成环境：Milvus原生端口依赖回环/主机信任，生产应启用原生鉴权及TLS，隔离网络并备份外部卷。伴随服务强制服务凭据和平台HMAC，不接受浏览器指定namespace/filter。服务容器资源均有限额，网络代理显式关闭。

当前每人每类最多100个连接；旧版不分页列表最多返回1000项。操作回执与历史版本尚未实现长期清理。外部原文、索引及其历史占用外部服务存储，不计入工作空间数据盘额度，应单独制定备份和保留策略。

查看状态或停止：

```bash
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml ps
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml stop
```

停止保留数据。不要对其他项目清理卷。完整验证和待完成项见[测试验收](../../docs/testing.md#记忆与知识库复验)；创建工作空间时可选择已授权的连接，运行时边界见 [REQ-25](../../docs/requirements.md#619-创建工作空间扩展配置req-25)。

### 在 DSH 对话中使用知识库

创建工作空间时选择知识库，并为所有已选知识库统一勾选一组 DSH 的“读取、写入、删除”权限，默认仅读取。权限冻结在工作空间配置中，工具注册和每次 Worker 调用都检查对应权限；旧配置未指定权限时只允许读取。记忆和知识库均不再选择个人/本工作空间检索范围；每个连接使用一份共享内容，由服务端固定历史默认存储键。

“读取”注册检索和原文读取工具；“写入”注册 `cloud_knowledge_submit_*`，可在对话中要求“将以下内容保存到知识库，标题为……”。工具先生成预览，再通过 DSH 确认窗口展示标题、完整正文、来源、范围和数据去向，确认后新增；取消、缺少确认组件、无写权限或预览有警告时不保存，不覆盖已有文档。“删除”注册 `cloud_knowledge_delete_*`，按准确文档 id 获取目标版本，展示标题、正文、范围及不可恢复提示，单次确认后删除正文和索引；文档更新后不能删除旧预览以外的新版本。写入/删除复用幂等回执，失败或超时不能当作成功，也不自动重试新增。

部署需执行 `hcctl dev-grants`（本地可通过 `make run-worker` 完成）并更新 Worker，使其具备知识库幂等回执写权限和新版扩展脚本。扩展脚本在创建时写入工作盘，已有工作空间不会因刷新页面或普通重启自动更新；新建并选择知识库的工作空间会加载新工具。当前不自动覆写已有工作空间的 profile 或用户文件。

### DSH 完整会话与 OpenViking 集成

创建工作空间，选择已验证的 OpenViking 记忆连接，默认开启“完整会话同步”（可手动关闭），同一用户接入同一连接的多个工作空间可共享会话记忆并跨空间召回，无需选择保存范围。每个工作空间最多接入一个记忆连接，未勾选和旧配置均不自动采集。Mem0、Graphiti 保持现有手动检索方式。

初始化时离线安装未修改的官方 `@openviking/dsh-memory-plugin@0.5.31` npm 包，校验 SHA-256，并登记到工作空间 profile 的 `dependencies` 和 `dsh.profile.bundles`，复用当前固定 DSH 版本的依赖；无需升级运行镜像或在工作空间运行安装命令。官方 `openviking-memory-runtime` 条目由 DSH loader 加载并管理生命周期，可在 DSH 插件列表查看和启用、停用。插件提供原生 MCP 工具及 `openviking-memory`、`openviking-skills`、`ov-experience-memory` 三个内置技能。

官方插件从 DSH 事件流捕获用户消息、助手回复和工具结果，过滤系统/插件注入上下文，在新输入到达时自动召回。消息发送到 OpenViking 原生 `dsh-<session-id>` 会话；达到 20000 token、开始压缩或会话销毁时归档并触发记忆提取，失败写入由官方插件在工作盘排队补写。短会话可要求助手用 `mcp__openviking__remember` 立即保存明确偏好，或结束会话等待归档后再验证召回；打开另一个聊天本身不保证前一个会话已归档。

服务密钥保留在平台服务端。平台 `cloudSessionMemory` 仅为官方插件提供容器回环端点和私有 Worker 转发通道，不自行实现捕获、召回和归档；每次转发检查创建快照、连接版本、当前用户权限、绑定与范围。伴随服务以组织/用户/连接隔离原生租户，同一连接跨已接入工作空间共享，禁止转发管理 API、任意地址和身份头。MCP 工具仅开放 search、find、read、list、tree、grep、glob 和 remember；原生删除、编辑、资源导入及技能写入不开放，内置技能也受此工具边界约束。取消绑定、停用连接或撤销权限后，后续同步和召回被拒绝。

OpenViking 连接的“管理内容”统一展示“自动生成”和“手动添加”的记忆，可按来源过滤，共用名称、分类搜索和分页，并支持查看原文及 Markdown 预览。自动生成的记忆日常管理只读；手动添加的记忆保留新增、编辑、删除功能。“记忆检索”同时语义检索此连接下全部自动和手动记忆，按相关度合并结果，标明来源并支持查看对应原文，不受列表的名称、来源筛选或分页限制。页面合并保留两类记录原有的数据和存储租户，不自动迁移或改写内容，也不改变官方插件的召回范围。

OpenViking、Mem0 和 Graphiti 的记忆检索均可选择返回 5／10／20 条，以及最低相关度（-1 至 1，留空不过滤）。默认检索此连接的全部记忆并返回最多 5 条。“重置条件”恢复默认条件、清除旧结果并保留检索问题。OpenViking 另可选择“全部来源／自动生成／手动添加”，来源条件决定实际查询的记忆集合；最低相关度同时作用于选中来源的候选结果，后端过滤、按相关度合并后再截取返回条数。Mem0、Graphiti 检索通过本平台维护的记忆，不显示会话自动生成来源；相关度按各后端返回分数判断，Graphiti 当前使用原生召回排名分数，不宜跨服务比较。

更新检索参数后，须同步重新构建并重启 API 和 bridge；仅更新前端无法更新正在运行的接口字段定义。部署后应通过真实登录接口验证“手动添加”来源及最低相关度，避免旧 API 将 `memory_source` 拒绝为“请求字段或格式不正确”，或旧 bridge 拒绝新增字段。

“导出整个记忆”读取此连接下全部自动和手动记忆的完整正文，不受当前筛选、列表分页或目录展示上限影响，生成带 SHA-256 清单的 `hc-memory-v2` JSON。导入支持该完整包及旧 `hc-memory-v1` 迁移包，先预览来源和内容，再确认保存；冲突默认保留现有，也可明确选择替换。已删除的手动记忆在导入预览中显示为“新增”，默认跳过冲突时也会正常导入，并沿用原记录的下一版本。自动记忆保留原生路径并写回官方插件使用的租户，手动记忆保留原有管理接口。操作复用幂等流水与待确认核实，源连接及工作空间绑定不会被改变。

三种记忆后端的导入导出均上限 16 MiB、共 10000 条。Mem0、Graphiti 继续使用兼容已有迁移流程的 `hc-memory-v1`，导出全部当前记录并保留图谱字段；OpenViking 的 `hc-memory-v2` 另包含会话自动生成的原生记忆文件。记录仍在处理中、正文读取失败或超出上限时，完整导出失败，不返回截断文件。预览分页仅影响显示，确认导入处理全部条目；三种后端均把已删除记录视为新增，默认跳过冲突也可恢复。该包不包含原始聊天会话、附件、历史修订、外部服务凭据或向量；导入按目标服务重建索引。

自动生成的记忆列表和详情通过专用只读接口访问插件使用的同一租户，服务端固定组织、用户、连接和历史默认范围，仅接受当前用户及其 peer 的记忆文件，不开放任意原生 API。每棵目录读取最多 1000 个节点、8 层深度，达到节点上限时页面提示仅展示部分自动记忆；一类记忆加载失败不隐藏另一类已加载的记录。空列表不代表会话消息未同步。

捕获遵循官方插件的格式和长度限制，本集成设置单条文本上限 100000 字符，并受 512 KiB 原生请求及 1 MiB 私有通道约束；不等同于附件原文件备份。原生记忆提取使用 OpenViking 服务配置的模型及 Embedding，可能产生调用费用。

仅新建并开启此选项的工作空间加载集成；普通重启不会改写既有 profile。验证：

```bash
HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 node --test infrastructure/runtime/tests/openviking-session-integration.test.mjs
HC_RUNTIME_IMAGE=hc-dsh-runtime:0.1.0 HC_RUNTIME_SESSION_MEMORY=true node --test infrastructure/runtime/tests/docker-integration.test.mjs
python3 -m unittest discover -s deploy/context/bridge -p 'test_viking*.py'
```
