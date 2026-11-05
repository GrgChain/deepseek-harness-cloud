-- The shipped native bundle supports platform-managed full conversation sync.
UPDATE plugin_catalog_defaults SET
 package_version='0.1.1', revision=revision+1,
 summary='接入 Mem0 记忆，自动同步完整会话并召回相关内容。',
 description='本项目维护的 DSH 原生记忆插件。创建工作空间选择 Mem0 记忆连接并启用完整会话同步后自动安装、启用，无需在 DSH 重填服务地址和凭据；同步用户消息、助手回复和工具结果，与平台记忆管理共用连接。也支持下载后独立配置 Mem0 OSS REST 服务。',
 source_note='本项目维护的离线组合包；源码和 README 位于 infrastructure/plugins/mem0。非 Mem0 官方插件，未发布到 npm。'
WHERE package_name='@harness-cloud/dsh-mem0-memory';
