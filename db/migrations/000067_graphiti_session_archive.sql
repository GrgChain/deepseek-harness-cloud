-- Match OpenViking: raw conversations are staged before long-term extraction.
UPDATE plugin_catalog_defaults SET
 package_version='0.2.0', revision=revision+1,
 summary='通过技能检索 Graphiti 记忆，归档会话后提取长期记忆。',
 description='本项目维护的 DSH 原生记忆插件，内含 graphiti-memory 技能和 MCP 工具。创建工作空间选择 Graphiti 连接并开启完整会话同步后自动安装、启用。原始会话单独保存，在达到归档阈值、开始压缩或会话释放时通过 Graphiti 提取长期记忆，与平台手动记忆共同检索；凭据、图谱分组和模型由平台管理。此版本仅支持平台连接。'
WHERE package_name='@harness-cloud/dsh-graphiti-memory';
