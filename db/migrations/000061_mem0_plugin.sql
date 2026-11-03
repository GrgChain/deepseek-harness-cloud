-- Locally maintained, downloadable OSS REST bundle; no npm publication implied.
INSERT INTO plugin_catalog_defaults(id,package_name,name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,sort_order) VALUES
 ('6e2bccdc-1ae1-4f04-8ddf-6bab763d2634','@harness-cloud/dsh-mem0-memory','Mem0 记忆','0.1.0',
  'Harness Cloud','会话与记忆',
  '通过 Mem0 保存明确记忆、自动召回，并可开启对话消息保存。',
  '本项目维护的 DSH 插件，通过 Mem0 OSS REST 服务保存和检索记忆。提供记忆检索、确认后保存和状态工具；自动保存对话需显式开启，默认仅处理用户消息。使用前在 DSH 中配置服务地址、API Key 和独立用户标识。',
  ARRAY['记忆','上下文','memory','Mem0','自动召回','本地维护','离线'],
  '','',
  '适配 DSH 0.2.1-alpha.2；Node.js >=24。需要可访问的 Mem0 OSS REST 服务。',
  '2026-11-03','本项目维护的离线组合包；源码和 README 位于 infrastructure/plugins/mem0。非 Mem0 官方插件，未发布到 npm。',3)
ON CONFLICT (package_name) DO NOTHING;
