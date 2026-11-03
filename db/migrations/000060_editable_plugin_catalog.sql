CREATE TABLE plugin_catalog_defaults (
 id uuid PRIMARY KEY,
 package_name text NOT NULL UNIQUE CHECK(length(package_name) BETWEEN 1 AND 214),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 214),
 package_version text NOT NULL CHECK(length(package_version) BETWEEN 1 AND 128),
 author text NOT NULL,
 category text NOT NULL CHECK(category IN ('会话与记忆','开发工具','集成扩展')),
 summary text NOT NULL,
 description text NOT NULL,
 keywords text[] NOT NULL DEFAULT '{}',
 repository text NOT NULL DEFAULT '',
 documentation text NOT NULL DEFAULT '',
 compatibility text NOT NULL,
 checked_at date NOT NULL,
 source_note text NOT NULL DEFAULT '',
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 sort_order integer NOT NULL CHECK(sort_order>=0)
);
REVOKE ALL ON plugin_catalog_defaults FROM PUBLIC;

-- Initial catalog metadata is preserved from the previous built-in directory.
-- These notes describe its sources; they do not claim runtime verification.
INSERT INTO plugin_catalog_defaults(id,package_name,name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,sort_order) VALUES
 ('6e2bccdc-1ae1-4f04-8ddf-6bab763d2631','@openviking/dsh-memory-plugin','OpenViking 记忆','0.5.29',
  'OpenViking / volcengine','会话与记忆',
  '将 OpenViking 的记忆和上下文能力接入 DSH。',
  '由 OpenViking 项目提供的 DSH 组合包。使用前需按项目文档准备 OpenViking 服务及相关配置；具体连接能力和授权范围以该版本文档为准。',
  ARRAY['记忆','上下文','memory','OpenViking'],
  'https://github.com/volcengine/OpenViking',
  'https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin',
  '未声明 DSH 版本范围；Node.js 要求 ^22.19.0 或 >=24。',
  '2026-10-27','包信息及 DSH bundle 声明',0),
 ('6e2bccdc-1ae1-4f04-8ddf-6bab763d2632','@linxin666/dsh-ssh','DSH SSH','0.4.11',
  'zhu1090093659 / linxin666','开发工具',
  '在 DSH Web 中使用 SSH 终端、SFTP 和远程主机工具。',
  '为 DSH Web 提供远程 SSH 操作，包括主机配置、持久连接、终端、文件传输和端口转发。使用时需要自行配置主机与认证，并满足运行环境的网络策略。',
  ARRAY['SSH','SFTP','远程','终端'],
  'https://github.com/zhu1090093659/dsh-web','https://github.com/zhu1090093659/dsh-web#readme',
  '包声明 DSH >=0.2.1-rc.3；Node.js ^22.19.0 或 >=24.0.0。',
  '2026-10-27','包信息及 DSH bundle 声明',1),
 ('6e2bccdc-1ae1-4f04-8ddf-6bab763d2633','dshmarket','DSH Market','1.71.1',
  'dsh-market','集成扩展',
  '社区维护的 DSH 插件发现与浏览工具。',
  '该第三方组合包在 DSH 中提供社区目录的浏览和搜索。它由独立社区维护，目录收录规则及所需的 DSH 版本请查看项目文档。',
  ARRAY['市场','目录','marketplace','发现'],
  'https://github.com/dsh-market/dsh-market','https://github.com/dsh-market/dsh-market#readme',
  '未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。',
  '2026-10-27','包信息及 DSH bundle 声明',2);

ALTER TABLE personal_plugins
 ADD COLUMN source_note text NOT NULL DEFAULT '',
 ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 ADD COLUMN default_id uuid REFERENCES plugin_catalog_defaults(id);
CREATE UNIQUE INDEX personal_plugins_default ON personal_plugins(org_id,owner_id,default_id) WHERE default_id IS NOT NULL;
