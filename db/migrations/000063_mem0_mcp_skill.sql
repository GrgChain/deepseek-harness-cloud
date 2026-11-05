-- Match OpenViking's native skill provider and MCP client integration.
UPDATE plugin_catalog_defaults SET
 package_version='0.1.2', revision=revision+1,
 summary='通过技能检索 Mem0 记忆，自动同步完整会话。',
 description='本项目维护的 DSH 原生记忆插件，内含 mem0-memory 技能，通过 MCP 提供记忆检索和同步状态。创建工作空间选择 Mem0 连接并开启完整会话同步后自动安装、启用，与平台记忆管理共用连接。也支持下载后独立配置 Mem0 OSS REST 服务。',
 keywords=ARRAY['记忆','上下文','memory','Mem0','技能','MCP','自动召回','本地维护','离线']
WHERE package_name='@harness-cloud/dsh-mem0-memory';
