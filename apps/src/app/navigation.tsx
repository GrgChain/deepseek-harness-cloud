import { Link } from 'react-router-dom';
import { Button, type MenuProps } from 'antd';
import {
  Activity,
  BookOpen,
  Box,
  Brain,
  Check,
  Database,
  ExternalLink,
  FileText,
  Gauge,
  LayoutGrid,
  Library,
  LockKeyhole,
  Puzzle,
  RefreshCw,
  Settings2,
  ShieldCheck,
} from 'lucide-react';
import { hasPermission, type CurrentUser } from '../api/identity';

const routeTitles: Record<string, string> = {
  '/app/instances': '工作空间',
  '/app/storage': '工作数据',
  '/app/skills': '技能包',
  '/app/connectors': '连接器',
  '/app/plugins': '插件',
  '/app/memory': '记忆',
  '/app/knowledge': '知识库',
  '/app/models': '模型提供商',
  '/app/users': '用户管理',
  '/app/quotas': '资源配额',
  '/app/roles': '角色与权限',
  '/app/settings': '平台配置',
  '/app/audit': '操作日志',
  '/login': '账户',
};

export function pageTitle(pathname: string, detail: boolean, storageBrowse: boolean): string {
  if (detail) return '实例详情';
  if (storageBrowse) return '数据浏览';
  return routeTitles[pathname] ?? '工作空间';
}

export function buildMenuItems(user: CurrentUser | null | undefined): MenuProps['items'] {
  const workspaceItems: MenuProps['items'] = [
    ...(!user || hasPermission(user, 'instances:read')
      ? [
          {
            key: '/app/instances',
            icon: <LayoutGrid size={18} />,
            label: <Link to="/app/instances">工作空间</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'instances:read')
      ? [
          {
            key: '/app/storage',
            icon: <Database size={18} />,
            label: <Link to="/app/storage">工作数据</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'skills:read')
      ? [
          {
            key: '/app/skills',
            icon: <BookOpen size={18} />,
            label: <Link to="/app/skills">技能包</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'connectors:read')
      ? [
          {
            key: '/app/connectors',
            icon: <ExternalLink size={18} />,
            label: <Link to="/app/connectors">连接器</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'memory:read')
      ? [
          {
            key: '/app/memory',
            icon: <Brain size={18} />,
            label: <Link to="/app/memory">记忆</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'knowledge:read')
      ? [
          {
            key: '/app/knowledge',
            icon: <Library size={18} />,
            label: <Link to="/app/knowledge">知识库</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'instances:read')
      ? [
          {
            key: '/app/plugins',
            icon: <Puzzle size={18} />,
            label: <Link to="/app/plugins">插件</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'personal-model:read')
      ? [
          {
            key: '/app/models',
            icon: <Box size={18} />,
            label: <Link to="/app/models">模型提供商</Link>,
          },
        ]
      : []),
  ];

  const administrationItems: MenuProps['items'] = [
    ...(hasPermission(user, 'users:read')
      ? [
          {
            key: '/app/users',
            icon: <ShieldCheck size={18} />,
            label: <Link to="/app/users">用户管理</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'roles:read')
      ? [
          {
            key: '/app/roles',
            icon: <LockKeyhole size={18} />,
            label: <Link to="/app/roles">角色与权限</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'users:read')
      ? [
          {
            key: '/app/quotas',
            icon: <Gauge size={18} />,
            label: <Link to="/app/quotas">资源配额</Link>,
          },
        ]
      : []),
    ...(hasPermission(user, 'audit:read')
      ? [
          {
            key: '/app/audit',
            icon: <FileText size={18} />,
            label: <Link to="/app/audit">操作日志</Link>,
          },
        ]
      : []),
    ...(!user || hasPermission(user, 'configuration:read')
      ? [
          {
            key: '/app/settings',
            icon: <Settings2 size={18} />,
            label: <Link to="/app/settings">平台配置</Link>,
          },
        ]
      : []),
  ];

  return [
    ...(workspaceItems.length
      ? [{ type: 'group' as const, label: '工作台', children: workspaceItems }]
      : []),
    ...(administrationItems.length
      ? [{ type: 'group' as const, label: '管理', children: administrationItems }]
      : []),
  ];
}

export function DiagnosticsSection({
  online,
  ready,
  auth,
  pending,
  fetching,
  onRefresh,
}: {
  online: boolean;
  ready: boolean;
  auth: boolean;
  pending: boolean;
  fetching: boolean;
  onRefresh: () => void;
}) {
  return (
    <section className="settings-section diagnostics-section" aria-label="平台连接状态">
      <div className="section-top">
        <h2>平台连接状态</h2>
        <Button icon={<RefreshCw size={15} />} onClick={onRefresh} loading={fetching}>
          刷新状态
        </Button>
      </div>
      <div className="diagnostics-grid">
        {[
          { icon: Activity, title: '平台 API', ok: online, label: online ? '已连接' : '未连接' },
          {
            icon: Database,
            title: '数据库与缓存',
            ok: ready,
            label: online ? (ready ? '已就绪' : '未就绪') : '待检查',
          },
          { icon: ShieldCheck, title: '组织身份认证', ok: auth, label: auth ? '已就绪' : '未就绪' },
        ].map(({ icon: Icon, title: checkTitle, ok, label }) => (
          <div className="diagnostic-item" key={checkTitle}>
            <Icon size={18} aria-hidden="true" />
            <div>
              <strong>{checkTitle}</strong>
              <span className={`diagnostic-state ${ok ? 'ok' : pending ? 'pending' : 'error'}`}>
                {ok && <Check size={13} aria-hidden="true" />}
                {pending ? '检查中' : label}
              </span>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

export { HelpDrawer } from './help-drawer';
