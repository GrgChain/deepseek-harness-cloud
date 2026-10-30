# 外接记忆与知识库实施记录

## 当前实现

知识库支持 Milvus、Weaviate 和 Qdrant 自有服务，新建连接显式配置 Embedding API。文档可手动输入，或通过 Docling 导入文本、Office、PDF 和图片；解析结果经编辑、预览确认后入库。文档列表按标题搜索并由服务端分页。检索支持语义、全文和混合模式，可设置最低相关度并查看授权原文。格式、大小、数据去向、部署步骤与检索规则统一见[服务部署说明](../deploy/context/README.md)。

知识库使用 `markdown-it-py` 识别章节、段落和表格，超长内容由 LlamaIndex `SentenceSplitter` 分段。向量化时附带文档名及章节；全文索引使用相同片段，检索结果返回章节路径。旧字符串片段仍可读取；已有文档需显式重建才能应用结构化分段，重建采用新版本并在失败时保留旧版。参数和操作步骤见[文档分段与重建](../deploy/context/README.md#文档分段与重建)。

创建工作空间时可选择已授权的记忆和知识库，运行时通过受控通道检索与重新鉴权读取原文，不自动采集会话或写入记忆。当前运行实现见[后端说明](backend.md#创建扩展与受控运行req-25)，独立验证见[扩展专项](testing.md#2026-10-27-创建扩展与受控运行)。

Embedding 可使用自建或云端 OpenAI 兼容 API；本机提供固定版本的 Hugging Face TEI CPU 服务，默认 BGE Small 中文模型（512 维）。已有本地模型连接保留原配置。默认模型的 API 冒烟及平台→伴随服务→TEI→Milvus 文档写入、检索、删除曾验证通过。

以下表格和 2026-10-23 回归为工作台阶段的历史实施记录，完整生产故障与容量验收另行记录。

约束：平台保持原生Go/Vite部署；新增独立Docker项目 `hc-context`。未修改已有DSH镜像、容器、配置组合或持久目录，不使用其他项目的外部服务。真实验证使用独立测试账号、明确授权的权限和合成内容。模型测试按用户授权复用现有批准的 `gpt-6-luna`，凭据仅保存在本机私有配置。

| 交付 | 实现与证据 | 状态 |
| --- | --- | --- |
| 独立服务 | `deploy/context/compose.yaml`固定版本/摘要、独立网络和数据卷、资源上限、内部或回环端口；`prepare.py`生成私有服务密钥、HMAC密钥及批准策略 | 已运行 |
| 控制面 | 迁移47；`services/internal/contextservice`；连接凭据与操作请求/结果加密、Owner/组织隔离、当前权限再检查、版本及幂等、审计、绑定和未知操作核实 | 受限PostgreSQL race集成通过 |
| 密钥轮换 | `operations.Rotator`覆盖技能包/上一版/预览、外接凭据/操作/预览；保留原AAD、版本和状态 | 独立PostgreSQL中移除旧密钥后六类新记录均可解密；审计失败回滚测试通过 |
| Mem0 | 2.2.1 OSS SDK + Qdrant，明确写入 `infer=False`，本机中文Embedding，不自动提取会话 | 真实写入/召回/更新/隔离/删除通过 |
| OpenViking | 0.4.16镜像固定摘要，独立账户及租户密钥，明确记忆文件写入/语义索引、原生find、限定目录删除；内部Embedding兼容float/base64 | 真实写入/召回/更新/隔离/删除通过 |
| Graphiti | 0.31.2 OSS SDK + Neo4j 5.26.0；批准模型提取，Owner/范围/版本分组；原生实体/关系/事件检索，删除由SDK处理 | 真实写入/召回/更新/隔离/删除通过 |
| 知识库 | Qdrant、Weaviate、Milvus原生API；LlamaIndex分块，Embedding API生成向量；服务端限定collection/class与scope，结果复核原文版本 | 三种真实后端曾验证通过 |
| 原文与恢复 | 外部伴随服务SQLite保存原文、版本、预览和操作回执；新版本独立索引，失败时旧版可用并标记STALE；显式重建，删除待确认停止召回 | 故障注入测试通过 |
| 可迁移记忆 | `hc-memory-v1` JSON及SHA-256清单，内容/类型/来源/时间/图谱字段；预览、冲突默认跳过、语义损失确认；不删除源或自动切换绑定 | Graphiti→Mem0/OpenViking真实迁移通过；图谱结构保留另有非空结构测试 |
| Portal | `/app/memory`、`/app/knowledge`；批准连接、验证、内容维护、导入导出、试检索与重新授权的原文查看；本人工作数据显式选文件后预览确认 | 桌面/手机交互、只读权限及六后端逐一使用真实独立账号验证通过 |
| API与文档 | OpenAPI API-095～099、API-120、四个权限、部署/前后端/安全/测试文档、Makefile入口 | 按当前实现维护 |
| 工作台及运行时 | 工作台直接管理与检索；创建时选择的连接可通过受控工具在工作空间中检索和读取授权原文 | 运行边界见 REQ-25 |

## 可重复验证

执行前按[部署说明](../deploy/context/README.md)启动独立服务。真实浏览器需本机Chromium，可通过 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`指定。

```bash
make check
GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race ./...
HC_TEST_DB_DSN_FILE="$PWD/.local/migrator-dsn" GOMODCACHE=/tmp/hc-go-mod GOCACHE=/tmp/hc-go-build go test -race -count=1 ./db ./services/internal/identity ./services/internal/contextservice
cd apps && npm test && npm run build
```

从仓库根目录分别执行：

```bash
HC_CONTEXT_TEST_BACKENDS=qdrant,weaviate,milvus,mem0,openviking,graphiti python3 deploy/context/test_services.py
HC_CONTEXT_TEST_BACKENDS=qdrant,weaviate,milvus python3 deploy/context/test_services.py ExternalServices.test_structure_aware_search_and_rebuild
HC_CONTEXT_TEST_MIGRATION=true python3 deploy/context/test_services.py ExternalServices.test_portable_graph_memory_migrates_without_deleting_source
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile bridge run --rm --no-deps bridge python -m unittest test_document_chunks test_recovery test_embedding_provider test_milvus_service
```

从 `apps` 执行：

```bash
npx playwright test tests/context.spec.ts --grep-invert 'live ' --workers=1
HC_REAL_IDENTITY=true HC_REAL_CONTEXT=true npx playwright test tests/context.spec.ts --grep 'live workbench'
HC_REAL_IDENTITY=true HC_REAL_CONTEXT=true HC_CONTEXT_UI_BACKENDS=mem0,openviking,graphiti,qdrant,weaviate,milvus npx playwright test tests/context.spec.ts --project=desktop --grep 'live platform'
```

后端安全测试覆盖密文不含原始凭据、跨Owner管理员拒绝、过期版本、幂等重放与冲突、外部删除未知后的召回封闭、原操作核实、批准端点撤销及已有会话权限撤销。公网HTTP、链路本地地址、重定向、公开权限策略文件被拒绝。真实服务测试验证中文检索和旧版排除；图谱推理质量不由简单冒烟测试保证。

## 当前限制与后续验收

- 文档解析会提取文字，不保存原始文件；工作数据选择只读取所选文件，不扫描目录或会话。文件和正文大小限制见[部署说明](../deploy/context/README.md#文档解析)。
- Graphiti迁移保留实体/关系/事件来源与有效时间的JSON快照；目标正文检索或重提取不保证保留原图推理语义，必须明确确认损失，不标记完整语义迁移。
- 本机模型配置是批准凭据的私有测试快照，生产模型撤销/费用/密钥轮换尚未接入平台动态治理；撤销时须同步更新测试配置。
- Milvus原生端口只绑定回环，当前依赖主机信任，生产需要原生鉴权、TLS及更强网络隔离。独立备份、灾备、容量/压测和全套竞争故障矩阵尚未验收。
- 知识库文档列表支持服务端分页；旧版不带分页参数的列表仍最多返回1000条，平台连接最多100条。长期回执/历史版本清理尚未实现。源正文及索引历史由独立服务占用，不计工作空间卷配额。
- 迁移48按用户确认将 `memory:read/manage`、`knowledge:read/manage` 赋予内置ADMIN/MEMBER；自定义角色继续显式授权。普通工作空间和模型流程不依赖外部服务健康。

## 2026-10-23 最终回归记录

| 范围 | 结果 |
| --- | --- |
| `make ci` | 格式、vet、TypeScript、Go race、49项前端单测、Python/运行适配测试及Go/Vite生产构建通过；构建保留现有大分包提示 |
| `make test-integration` | 独立PostgreSQL中14个包全部通过，包含身份、技能包、外接服务及新增密文轮换 |
| 完整桌面/手机Playwright | 250通过、56项按真实环境开关跳过、零失败，3.2分钟；跳过项不计为通过 |
| 六后端真实账号及配额 | 六个后端分别创建明确授予权限的独立账号；另验证管理员/成员默认入口，共10项通过，45.4秒（6项手机后端流程条件跳过） |
| 真实登录、技能包、资源与分页套件 | 桌面/手机共26项通过，1分钟；其中20项为真实服务流程、6项为技能包模拟交互 |
| Office/PDF及工作数据浏览 | 专项34项通过，58.4秒，包含预览加载失败与重新加载恢复 |
| 技能链接下载器安全回归 | 14个实际TLS/网络边界用例通过，覆盖逐跳DNS与IP固定、混合内网答案、DNS重绑定、私有重定向、跳数上限、证书与主机名、超限、404、取消与截止时间；另含受限数据库完整技能库集成 |
| 公网技能链接真实浏览器 | 桌面/手机2项通过，13.9秒；独立成员账号完成预览、确认导入、无变化同步及下载正文一致性，随后清理合成数据 |
| 六种原生后端联合读写 | 中文检索、版本、隔离、更新、导出及删除通过，58.563秒 |
| 跨服务真实迁移 | Graphiti→Mem0/OpenViking通过，16.5秒；非空图谱结构的保留另由故障测试覆盖 |

回归中修复了配额输入缺少独立无障碍名称、运行默认存储输入无法保留无效值进行校验及提交按钮加载时名称漂移；同步修正组织额度接口模拟、已移除模型控制的旧断言、分页及移动导航定位、PDF动态模块故障模拟和模型轮询时序。完整回归与真实服务验证均在这些修正后执行。

公网技能来源现已通过可重复的生产下载链测试：本机采用运维显式配置的回环CONNECT代理，下载器先解析并校验全部来源IP，再向代理发送固定公网IP:443，TLS验证原始域名，保持逐跳校验和下载限额。GitHub公开SKILL.md实际下载、预览、保存、手动检查无更新及导出正文一致性已在独立PostgreSQL中通过（3.885秒）；直连出口仍不稳定，未放宽地址或TLS保护。


## 工作台默认入口

管理员和成员均可管理自己的记忆与知识库，自定义角色按需授权。迁移48只调整内置角色，幂等执行、更新角色版本并写入审计，新组织使用同样默认值。桌面侧栏与手机导航使用“记忆”“知识库”；创建时选择的连接按 REQ-25 受控接入工作空间。


本轮工作台验证：默认权限迁移在独立PostgreSQL中验证已有内置角色、幂等审计、自定义角色不变；新组织默认权限与跨Owner拒绝由contextservice集成覆盖。桌面/手机交互8项通过；真实管理员/成员导航与六后端账号流程共10项通过（45.4秒，另6项手机后端流程按开关跳过）；全量页面250项通过、56项条件跳过；公网技能链接真实浏览器2项通过（13.9秒）。最终 `make ci` 全部通过，六后端联合读写58.563秒通过，跨服务迁移使用上表独立验收记录。
