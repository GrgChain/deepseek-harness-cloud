# 随平台分发的 DSH 插件包

每个插件仅保留当前使用版本。更新版本时同步修改原生安装器的包名、版本和 SHA-256、Go 嵌入路径、前端下载映射及相关测试，再删除旧归档和对应 `.sha256`。

| 插件                  | 当前版本 | Worker 安装归档                                                                                                        | 插件库下载                                                                                              |
| --------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| OpenViking 官方插件   | 0.5.31   | [openviking/openviking-dsh-memory-plugin-0.5.31.tgz](openviking/openviking-dsh-memory-plugin-0.5.31.tgz)               | 官方 npm 来源                                                                                           |
| Mem0 本地维护插件     | 0.2.0    | [mem0-dist/harness-cloud-dsh-mem0-memory-0.2.0.tgz](mem0-dist/harness-cloud-dsh-mem0-memory-0.2.0.tgz)                 | [public/plugin-packages](../../apps/public/plugin-packages/harness-cloud-dsh-mem0-memory-0.2.0.tgz)     |
| Graphiti 本地维护插件 | 0.2.0    | [graphiti-dist/harness-cloud-dsh-graphiti-memory-0.2.0.tgz](graphiti-dist/harness-cloud-dsh-graphiti-memory-0.2.0.tgz) | [public/plugin-packages](../../apps/public/plugin-packages/harness-cloud-dsh-graphiti-memory-0.2.0.tgz) |

`*-dist` 供 Go 编译嵌入，`apps/public/plugin-packages` 供前端下载；同版本的两份归档必须逐字节一致。`.sha256` 与下载归档一起维护。`apps/dist` 是构建结果，不作为归档源。

Mem0、Graphiti 分别使用 `node infrastructure/plugins/pack-mem0.mjs`、`node infrastructure/plugins/pack-graphiti.mjs` 构建。脚本同时写入嵌入目录、下载目录和下载校验文件。OpenViking 原包及许可证来源见 [说明](openviking/README.md)。

校验下载和 Worker 安装归档：

```bash
go test ./services/internal/plugins -run 'TestBundled(Mem0|Graphiti)ArchiveIsAnInstallableDSHBundle'
```

Graphiti 的连接填写示例、手动验收步骤及自动化命令见 [测试用例](../../docs/testing-graphiti-dsh.md)。
