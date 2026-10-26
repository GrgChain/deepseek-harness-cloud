# 外接记忆与知识库

独立Compose项目 `hc-context`，不修改现有DSH镜像、容器或工作数据。平台仍使用当前Go/Vite部署。测试使用独立卷、回环端口和合成数据；API服务通过私有批准策略访问伴随服务。

```bash
make context-up                    # Mem0、Qdrant、Weaviate、Milvus及配套服务
make context-test                  # 真实读写/召回/隔离/更新/删除及索引故障恢复
```

私有文件生成在Git忽略的 `.local/context/`，目录0700、凭据0600。API配置 `HC_CONTEXT_ENDPOINTS_FILE` 为 `endpoints.local.json` 的绝对路径；`make run-auth`自动识别该文件。新增权限 `memory:read/manage`、`knowledge:read/manage` 内置管理员和成员默认拥有；自定义角色由管理员明确分配。Portal入口 `/app/memory`、`/app/knowledge`。

OpenViking、Graphiti还需要批准的推理模型。仅在已获授权时，将 `{ "model", "api_base", "api_key" }` 保存到0600的 `.local/context/model.json`，然后执行：

```bash
python3 deploy/context/prepare.py --enable-model-memory
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml --profile openviking --profile graphiti up -d --build
HC_CONTEXT_TEST_BACKENDS=openviking,graphiti python3 deploy/context/test_services.py
```

本机测试已按用户授权复用平台配置的模型；私有快照不会自动跟随平台撤销，轮换或撤销时须同步更新测试配置并重建容器。不要将模型配置、Compose展开结果或容器环境输出到日志。模型提取可能产生调用费用。自动采集保持关闭，正文只在明确预览确认后写入。

| 服务 | 固定版本 | 本机端口 |
| --- | --- | --- |
| Mem0 | 2.2.1 SDK，经认证伴随服务 | 18580/mem0 |
| Qdrant | 1.20.0 | 16333 |
| Weaviate | 1.27.0，vectorizer=none | 18085 / gRPC 15051 |
| Milvus | 2.5.6，独立etcd/MinIO | 19531 / 健康19091 |
| OpenViking | 0.4.16，镜像固定摘要 | 仅Compose内网 |
| Graphiti | 0.31.2 SDK + Neo4j 5.26.0 | 仅Compose内网 |

伴随服务保管外部原文、版本与幂等回执；LlamaIndex分块，FastEmbed `BAAI/bge-small-zh-v1.5` 生成512维中文向量，各向量数据库负责检索。支持UTF-8 TXT/Markdown，单篇256KiB；PDF/Office浏览预览能力不等于可导入知识库。OpenViking用独立租户密钥；Graphiti按Owner/范围/版本分组，迁移包保留完整图谱字段，对无法表达的目标必须确认语义损失。慢操作先显示待确认，使用“核实待确认操作”完成原幂等请求。

这里是本机集成环境：Milvus原生端口依赖回环/主机信任，生产应启用原生鉴权及TLS，隔离网络并备份外部卷。伴随服务强制服务凭据和平台HMAC，不接受浏览器指定namespace/filter。服务容器资源均有限额，网络代理显式关闭。

查看状态或停止：

```bash
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml ps
docker compose --env-file .local/context/compose.env -f deploy/context/compose.yaml stop
```

停止保留数据。不要对其他项目清理卷。完整验证、待完成项和DSH边界见[实施记录](../../docs/context-implementation.md)；本次功能在平台工作台使用，工作空间关联不修改DSH。
