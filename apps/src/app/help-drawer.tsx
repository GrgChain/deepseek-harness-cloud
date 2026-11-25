import { Link } from 'react-router-dom';
import { Collapse, Drawer, Steps, Tag } from 'antd';
import {
  BookOpen,
  Box,
  Brain,
  Database,
  ExternalLink,
  HelpCircle,
  LayoutGrid,
  Puzzle,
  ShieldCheck,
} from 'lucide-react';

export function HelpDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const featureItems = [
    {
      key: 'workspaces',
      label: (
        <span className="help-section-title">
          <LayoutGrid size={15} aria-hidden="true" />
          工作空间
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            按需选择
            CPU、内存与存储规格，支持创建、进入、启停和删除工作空间。创建完成后在实例详情页进入 DSH
            工作空间。
          </p>
          <p>
            停止工作空间会释放 CPU 与内存资源，但保留工作目录与 DSH 数据；删除前需确认实例名称。
          </p>
          <Link to="/app/instances" onClick={onClose} className="help-quick-link">
            前往工作空间 →
          </Link>
        </div>
      ),
    },
    {
      key: 'storage',
      label: (
        <span className="help-section-title">
          <Database size={15} aria-hidden="true" />
          工作数据
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            浏览持久保留的 <code>/data/workspace</code> 与 <code>/data/dsh</code>{' '}
            目录，支持常见文本、Office 文档和 PDF 在线预览及下载。
          </p>
          <p>已软删除工作空间的保留数据仍计入存储额度，可在此页面永久释放以回收存储空间。</p>
          <Link to="/app/storage" onClick={onClose} className="help-quick-link">
            前往工作数据 →
          </Link>
        </div>
      ),
    },
    {
      key: 'models',
      label: (
        <span className="help-section-title">
          <Box size={15} aria-hidden="true" />
          模型提供商
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            添加官方或兼容服务提供商账号与密钥，校验可用模型后设为个人默认，或在创建工作空间时指定。
          </p>
          <p>工作空间内通过平台模型网关统一接入，无需在容器内明文暴露密钥。</p>
          <Link to="/app/models" onClick={onClose} className="help-quick-link">
            前往模型提供商 →
          </Link>
        </div>
      ),
    },
    {
      key: 'skills',
      label: (
        <span className="help-section-title">
          <BookOpen size={15} aria-hidden="true" />
          技能包
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            支持从公开来源（ClawHub、SkillHub、SkillsMP）搜索技能，也支持通过公开链接或本地 Markdown
            / ZIP 文件导入、编辑和导出。
          </p>
          <p>在创建工作空间时勾选技能，启动后会自动装配至工作空间目录。</p>
          <Link to="/app/skills" onClick={onClose} className="help-quick-link">
            前往技能包 →
          </Link>
        </div>
      ),
    },
    {
      key: 'context',
      label: (
        <span className="help-section-title">
          <Brain size={15} aria-hidden="true" />
          记忆与知识库
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            在工作台维护、检索和迁移个人记忆与知识库内容，支持接入 OpenViking、Mem0、Graphiti 与
            Milvus、Weaviate、Qdrant 等后端。
          </p>
          <p>创建工作空间时可按需绑定，运行期按当前账户权限验证访问。</p>
          <div className="help-quick-links">
            <Link to="/app/memory" onClick={onClose} className="help-quick-link">
              前往记忆 →
            </Link>
            <Link to="/app/knowledge" onClick={onClose} className="help-quick-link">
              前往知识库 →
            </Link>
          </div>
        </div>
      ),
    },
    {
      key: 'connectors',
      label: (
        <span className="help-section-title">
          <ExternalLink size={15} aria-hidden="true" />
          连接器 (MCP)
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>配置自定义 MCP 服务或 OAuth 连接器，自动发现工具并支持在工作台手动试调用。</p>
          <p>新建工作空间时选择已启用的连接器，即可在 DSH 对话中直接调用对应工具。</p>
          <Link to="/app/connectors" onClick={onClose} className="help-quick-link">
            前往连接器 →
          </Link>
        </div>
      ),
    },
    {
      key: 'plugins',
      label: (
        <span className="help-section-title">
          <Puzzle size={15} aria-hidden="true" />
          插件
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>浏览默认及个人插件目录，支持在线或离线添加插件包、维护 Agent 预设与内置扩展组合。</p>
          <Link to="/app/plugins" onClick={onClose} className="help-quick-link">
            前往插件 →
          </Link>
        </div>
      ),
    },
    {
      key: 'admin',
      label: (
        <span className="help-section-title">
          <ShieldCheck size={15} aria-hidden="true" />
          团队与平台管理
          <Tag color="default" style={{ marginInlineStart: 4 }}>
            管理员
          </Tag>
        </span>
      ),
      children: (
        <div className="help-section-body">
          <p>
            管理员可在侧栏管理入口维护用户账号、角色权限、个人资源配额、默认资源配置、系统资源上限及操作审计日志。侧栏菜单按当前账户权限动态显示。
          </p>
        </div>
      ),
    },
  ];

  const faqItems = [
    {
      key: 'faq-capacity',
      label: '提示「当前空闲 CPU 或内存不足」怎么办？',
      children: (
        <p>
          平台会为控制面保留固定的 CPU
          与内存余量。请先停止暂不使用的工作空间，或在创建弹窗中改选更小的规格，再点击「重新检查」。
        </p>
      ),
    },
    {
      key: 'faq-storage',
      label: '提示「存储额度不足」怎么办？',
      children: (
        <p>
          已停止或已软删除的工作空间仍会保留数据并计入存储额度。可前往「工作数据」永久释放不再需要的保留数据，或联系管理员调整存储配额。
        </p>
      ),
    },
    {
      key: 'faq-cert',
      label: '浏览器提示 HTTPS 证书不受信任？',
      children: (
        <p>
          本地或内网环境使用自签开发 CA 证书。将部署主机上的开发 CA
          证书导入当前设备的受信任根证书库后刷新即可。
        </p>
      ),
    },
  ];

  return (
    <Drawer title="使用帮助" open={open} onClose={onClose} width={440}>
      <div className="help-content">
        <section className="help-block">
          <h3 className="help-heading">快速上手</h3>
          <Steps
            direction="vertical"
            size="small"
            current={-1}
            items={[
              {
                title: '配置模型提供商',
                description: '在「模型提供商」添加账号与密钥，校验通过后设置默认模型。',
              },
              {
                title: '创建工作空间',
                description: '在「工作空间」选择资源规格、模型及可选的技能、连接器、记忆或知识库。',
              },
              {
                title: '进入与管理',
                description: '就绪后在实例详情进入 DSH；不用时可停止释放算力并保留数据。',
              },
            ]}
          />
        </section>

        <section className="help-block">
          <h3 className="help-heading">功能说明</h3>
          <Collapse
            ghost
            size="small"
            defaultActiveKey={['workspaces']}
            items={featureItems}
            className="help-collapse"
          />
        </section>

        <section className="help-block">
          <h3 className="help-heading">
            <HelpCircle size={15} aria-hidden="true" />
            常见问题
          </h3>
          <Collapse ghost size="small" items={faqItems} className="help-collapse" />
        </section>
      </div>
    </Drawer>
  );
}
