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

知识库检索可选最低相关度（余弦相似度，-1 至 1），留空不筛选；语义结果低于阈值时被过滤。混合检索中的关键词命中不受相关度阈值影响。

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

OpenViking、Graphiti还需要批准的推理模型。仅在已获授权时，将 `{ "model", "api_base", "api_key" }` 保存到0600的 `.local/context/model.json`，然后执行：

```bash
python3 deploy/context/prepare.py --enable-model-memory
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile openviking --profile graphiti up -d --build
HC_CONTEXT_TEST_BACKENDS=openviking,graphiti python3 deploy/context/test_services.py
```

本机测试已按用户授权复用平台配置的模型；私有快照不会自动跟随平台撤销，轮换或撤销时须同步更新测试配置并重建容器。不要将模型配置、Compose展开结果或容器环境输出到日志。模型提取可能产生调用费用。自动采集保持关闭，正文只在明确预览确认后写入。

| 服务 | 固定版本 | 本机端口 |
| --- | --- | --- |
| Embedding | Hugging Face TEI cpu-1.9，镜像固定摘要 | 18581 / 内网8581 |
| 文档解析 | Docling Serve CPU v1.36.0，包含 LibreOffice | 18582 / 内网5001 |
| Mem0 | 2.2.1 SDK，经认证伴随服务 | 18580/mem0 |
| Qdrant | 1.20.0 | 16333 |
| Weaviate | 1.27.0，vectorizer=none | 18085 / gRPC 15051 |
| Milvus | 2.5.6，独立etcd/MinIO | 19531 / 健康19091 |
| OpenViking | 0.4.16，镜像固定摘要 | 仅Compose内网 |
| Graphiti | 0.31.2 SDK + Neo4j 5.26.0 | 仅Compose内网 |

知识库连接的“Embedding模型”统一通过 API 接入，适用于 Milvus、Weaviate、Qdrant。本机或私网部署的模型也需提供平台接入服务可访问的 OpenAI 兼容 `/embeddings` 接口，填写 API Base URL、模型名、实际向量维度与可选 API Key；本机服务使用局域网地址，勿填写浏览器所在电脑的 `localhost`。新建连接必须显式配置 API，不再提供平台内置本地模型选项；已有本地模型连接保留原配置，更换模型需新建连接并重新导入文档。接口不要求支持可选的 `dimensions` 请求参数，平台检查实际返回维度与所填值一致。文档写入及问题检索始终使用该连接的配置，接口错误或维度不符会失败，不回退到本地模型。公网服务使用 HTTPS；自建私网 HTTP 服务遵循接入节点的 HTTP 许可及 DNS/目标校验。

Embedding配置和 API Key 与原连接凭据一起加密，列表和设置只返回非敏感配置及是否已保存密钥。编辑留空 API Key 保留原值，填写新值可更新并重新验证。创建后固定来源、模型、地址和维度；更换模型须新建知识库并重新导入文档，不在已有 collection 上混写其他向量。原有未配置Embedding选项的知识库继续使用本地默认模型，已有记忆服务的Embedding配置保持原部署方式。导入预览按实际来源说明文档和问题的数据去向及潜在外部调用费用。

伴随服务保管外部原文、版本与幂等回执；LlamaIndex分块，所选Embedding模型生成向量，各向量数据库负责检索。导入内容单篇最多256KiB。OpenViking用独立租户密钥；Graphiti按Owner/范围/版本分组，迁移包保留完整图谱字段，对无法表达的目标必须确认语义损失。慢操作先显示待确认，使用“核实待确认操作”完成原幂等请求。

这里是本机集成环境：Milvus原生端口依赖回环/主机信任，生产应启用原生鉴权及TLS，隔离网络并备份外部卷。伴随服务强制服务凭据和平台HMAC，不接受浏览器指定namespace/filter。服务容器资源均有限额，网络代理显式关闭。

查看状态或停止：

```bash
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml ps
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml stop
```

停止保留数据。不要对其他项目清理卷。完整验证和待完成项见[实施记录](../../docs/context-implementation.md)；创建工作空间时可选择已授权的连接，运行时边界见 [REQ-25](../../docs/requirements.md#619-创建工作空间扩展配置req-25)。
