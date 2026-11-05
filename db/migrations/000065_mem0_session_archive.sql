-- Raw sessions are separate from extracted memories, matching OpenViking.
UPDATE plugin_catalog_defaults SET
 package_version='0.2.0', revision=revision+1,
 summary='通过技能检索 Mem0 记忆，同步会话并在归档后提取记忆。',
 description='本项目维护的 DSH 原生记忆插件，内含 mem0-memory 技能和 MCP 工具。创建工作空间选择 Mem0 连接并开启完整会话同步后自动安装、启用。原始会话单独保存，在达到归档阈值、开始压缩或会话释放时通过 Mem0 提取长期记忆，与平台手动记忆共同检索。也支持独立配置 Mem0 OSS REST 服务。'
WHERE package_name='@harness-cloud/dsh-mem0-memory';
