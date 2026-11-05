-- Graphiti uses a native DSH bundle with a skill provider and MCP tools.
INSERT INTO plugin_catalog_defaults(id,package_name,name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,sort_order) VALUES
 ('6e2bccdc-1ae1-4f04-8ddf-6bab763d2635','@harness-cloud/dsh-graphiti-memory','Graphiti 记忆','0.1.0',
  'Harness Cloud','会话与记忆',
  '通过技能检索 Graphiti 记忆，自动同步完整会话。',
  '本项目维护的 DSH 原生记忆插件，内含 graphiti-memory 技能，通过 MCP 提供记忆检索和同步状态。创建工作空间选择 Graphiti 连接并开启完整会话同步后自动安装、启用，与平台记忆管理共用连接；凭据、图谱分组和提取模型由平台接入服务管理。此版本仅支持平台连接。',
  ARRAY['记忆','图谱','关系','Graphiti','技能','MCP','自动召回','本地维护','离线'],
  '','',
  '适配 DSH 0.2.1-alpha.2；Node.js 24。需要平台 Graphiti 记忆连接。',
  '2026-11-03','本项目维护的离线组合包；源码和 README 位于 infrastructure/plugins/graphiti。非 Graphiti 官方插件，未发布到 npm。',4)
ON CONFLICT (package_name) DO NOTHING;
