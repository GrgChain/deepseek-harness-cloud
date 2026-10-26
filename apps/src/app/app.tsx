import {ConnectorsPanel} from '../features/connectors/connectors-panel';
import { ContextPanel } from '../features/context/context-panel';
import { SkillsPanel } from '../features/skills/skills-panel';
import { StorageBrowsePage, StoragePanel } from '../features/storage/storage-panel';
import { OperationLogsPanel } from '../features/operation-logs/operation-logs-panel';
import { useEffect, useRef, useState } from 'react';
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Avatar, Button, Card, Drawer, Empty, Layout, Menu, Space, Spin, Tooltip, type MenuProps } from 'antd';
import { Activity, BookOpen, Brain, Library, Box, Check, CircleHelp, Cloud, Database, ExternalLink, FileText, Gauge, LayoutGrid, LockKeyhole, Menu as MenuIcon, RefreshCw, Settings2, ShieldCheck } from 'lucide-react';
import { PlatformClient } from '../api/platform';
import { IdentityClient, hasPermission } from '../api/identity';
import { UsersPanel, RolesPanel } from '../features/management/management-panel';
import { ResourceQuotasPanel } from '../features/resource-quotas/resource-quotas-panel';
import { ModelProvidersPanel } from '../features/models/model-providers-panel';
import { SettingsPanel } from '../features/settings/settings-panel';
import { InstancesPanel, InstanceDetails } from '../features/workspaces/instances-panel';
import { CreateInstanceButton } from '../features/workspaces/instance-commands';
import { expireSession, loginReturnPath } from '../shared/hooks/use-expired-session';

const platform = new PlatformClient();
const identity = new IdentityClient();
const { Sider, Header, Content } = Layout;

export function App() {
  const [navOpen, setNavOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  useEffect(() => { contentRef.current?.focus({ preventScroll: true }); }, [location.pathname]);
  const home = location.pathname === '/app/instances';
  const settings = location.pathname === '/app/settings';
  const login = location.pathname === '/login';
  const detail = location.pathname.startsWith('/app/instances/');
  const storageBrowse = /^\/app\/storage\/[^/]+\/browse$/.test(location.pathname);
  const titles: Record<string, string> = { '/app/instances': '工作空间', '/app/storage': '工作数据', '/app/skills': '技能包', '/app/connectors': '连接器', '/app/memory': '记忆', '/app/knowledge': '知识库', '/app/models': '模型提供商', '/app/users': '用户管理', '/app/quotas': '资源配额', '/app/roles': '角色与权限', '/app/settings': '平台配置', '/app/audit': '操作日志', '/login': '账户' };
  const title = detail ? '实例详情' : storageBrowse ? '数据浏览' : titles[location.pathname] ?? '工作空间';
  const status = useQuery({ queryKey: ['platform-status'], queryFn: ({ signal }) => platform.status(signal), refetchInterval: query => query.state.status === 'error' ? false : 30_000 });
  const online = status.data?.online === true && !status.isError;
  const ready = online && status.data?.dependenciesReady;
  const auth = online && status.data?.authenticationReady;
  const cache = useQueryClient();
  const session = useQuery({ queryKey: ['identity'], queryFn: ({ signal }) => identity.me(signal), enabled: !!auth, refetchInterval: 60_000, staleTime: 15_000, retry: false });
  const user = session.isError ? null : session.data;
  const returnTo = loginReturnPath(login ? new URLSearchParams(location.search).get('return_to') : location.pathname);
  const anonymous = !!auth && session.isSuccess && session.data === null;
  const redirecting = useRef(false);
  useEffect(() => {
    if (!anonymous || redirecting.current) return;
    redirecting.current = true;
    window.location.replace('/auth/login?return_to=' + encodeURIComponent(returnTo));
  }, [anonymous, returnTo]);
  const logout = useMutation({ mutationFn: () => user ? identity.logout(user) : Promise.resolve(), onSuccess: () => { expireSession(cache); navigate('/login'); } });
  const refresh = () => { void status.refetch(); if (auth) void session.refetch(); };
  const roleName = user?.memberships[0]?.role_name ?? '组织用户';
  const workspaceItems: MenuProps['items'] = [
    ...(!user || hasPermission(user, 'instances:read') ? [{ key: '/app/instances', icon: <LayoutGrid size={18} />, label: <Link to="/app/instances">工作空间</Link> }] : []),
    ...(hasPermission(user, 'instances:read') ? [{ key: '/app/storage', icon: <Database size={18} />, label: <Link to="/app/storage">工作数据</Link> }] : []),
    ...(hasPermission(user, 'skills:read') ? [{ key: '/app/skills', icon: <BookOpen size={18} />, label: <Link to="/app/skills">技能包</Link> }] : []),
    ...(hasPermission(user, 'connectors:read') ? [{key:'/app/connectors',icon:<ExternalLink size={18}/>,label:<Link to="/app/connectors">连接器</Link>}] : []),
    ...(hasPermission(user, 'memory:read') ? [{ key: '/app/memory', icon: <Brain size={18} />, label: <Link to="/app/memory">记忆</Link> }] : []),
    ...(hasPermission(user, 'knowledge:read') ? [{ key: '/app/knowledge', icon: <Library size={18} />, label: <Link to="/app/knowledge">知识库</Link> }] : []),
    ...(hasPermission(user, 'personal-model:read') ? [{ key: '/app/models', icon: <Box size={18} />, label: <Link to="/app/models">模型提供商</Link> }] : []),
  ];
  const administrationItems: MenuProps['items'] = [
    ...(hasPermission(user, 'users:read') ? [{ key: '/app/users', icon: <ShieldCheck size={18} />, label: <Link to="/app/users">用户管理</Link> }] : []),
    ...(hasPermission(user, 'roles:read') ? [{ key: '/app/roles', icon: <LockKeyhole size={18} />, label: <Link to="/app/roles">角色与权限</Link> }] : []),
    ...(hasPermission(user, 'users:read') ? [{ key: '/app/quotas', icon: <Gauge size={18} />, label: <Link to="/app/quotas">资源配额</Link> }] : []),
    ...(hasPermission(user, 'audit:read') ? [{ key: '/app/audit', icon: <FileText size={18} />, label: <Link to="/app/audit">操作日志</Link> }] : []),
    ...(!user || hasPermission(user, 'configuration:read') ? [{ key: '/app/settings', icon: <Settings2 size={18} />, label: <Link to="/app/settings">平台配置</Link> }] : []),
  ];
  const menuItems: MenuProps['items'] = [
    ...(workspaceItems.length ? [{ type: 'group' as const, label: '工作台', children: workspaceItems }] : []),
    ...(administrationItems.length ? [{ type: 'group' as const, label: '管理', children: administrationItems }] : []),
  ];
  const nav = <div className="sidebar-content"><Link className="brand" to="/app/instances" onClick={() => setNavOpen(false)}><span className="brand-mark"><Cloud size={22} aria-hidden="true" /></span><span>DSH Cloud</span></Link><nav aria-label="主导航"><Menu theme="dark" mode="inline" selectedKeys={[detail ? '/app/instances' : storageBrowse ? '/app/storage' : location.pathname]} items={menuItems} onClick={() => setNavOpen(false)} /></nav></div>;
  const connectionMessage = status.isPending ? null : !online ? '平台尚未连接' : !auth ? '身份服务尚未就绪' : session.isError ? '无法验证用户会话' : null;
  const diagnostics = <section className="settings-section diagnostics-section" aria-label="平台连接状态"><div className="section-top"><h2>平台连接状态</h2><Button icon={<RefreshCw size={15} />} onClick={refresh} loading={status.isFetching}>刷新状态</Button></div><div className="diagnostics-grid">{[{ icon: Activity, title: '平台 API', ok: online, label: online ? '已连接' : '未连接' }, { icon: Database, title: '数据库与缓存', ok: ready, label: online ? ready ? '已就绪' : '未就绪' : '待检查' }, { icon: ShieldCheck, title: '组织身份认证', ok: auth, label: auth ? '已就绪' : '未就绪' }].map(({ icon: Icon, title: checkTitle, ok, label }) => <div className="diagnostic-item" key={checkTitle}><Icon size={18} aria-hidden="true" /><div><strong>{checkTitle}</strong><span className={`diagnostic-state ${ok ? 'ok' : status.isPending ? 'pending' : 'error'}`}>{ok && <Check size={13} aria-hidden="true" />}{status.isPending ? '检查中' : label}</span></div></div>)}</div></section>;

  if (anonymous || status.isPending || auth && session.isPending) return <main className="auth-transition" aria-busy="true"><span className="brand-mark"><Cloud size={24} aria-hidden="true" /></span><h1>DSH Cloud</h1><div role="status"><Spin size="small" />{anonymous ? '正在前往登录' : '正在验证账户'}</div></main>;

  return <><a className="skip-link" href="#main-content">跳至主要内容</a><Layout className="app-shell">
    <Sider width={240} theme="dark" className="sidebar">{nav}</Sider>
    <Drawer className="navigation-drawer" open={navOpen} onClose={() => setNavOpen(false)} placement="left" width={264} title="导航"><div className="mobile-nav">{nav}</div></Drawer>
    <Layout className="main-shell">
      <Header className="topbar"><div className="topbar-title"><Button className="menu-button" type="text" aria-label="打开导航" icon={<MenuIcon size={20} />} onClick={() => setNavOpen(true)} /><h1>{title}</h1></div><Space><Tooltip title="使用帮助"><Button type="text" aria-label="使用帮助" icon={<CircleHelp size={18} />} onClick={() => setHelpOpen(true)} /></Tooltip><Button className="account-button" type="text" aria-label={user ? `${roleName}账户` : '尚未登录'} onClick={() => { navigate('/login'); setNavOpen(false); }}><Avatar size={28} icon={<LockKeyhole size={14} />} />{user ? roleName : '尚未登录'}</Button></Space></Header>
      <Content ref={contentRef} id="main-content" role="main" aria-label={title} tabIndex={-1} className={`page-content ${settings ? 'settings-page' : login ? 'login-page' : ''}`}>
        {connectionMessage && <Alert className="connection-alert" type="warning" showIcon message={connectionMessage} description={!online ? '请稍后重试。' : !auth ? '暂时无法登录，请稍后重试。' : '请重新检查或稍后登录。'} action={<Button size="small" onClick={refresh} loading={status.isFetching}>重新检查</Button>} />}
        {home && !user && <div className="page-actions"><CreateInstanceButton user={user} /></div>}
        <Routes>
          <Route path="/app/instances" element={<>
            {user && hasPermission(user, 'instances:read') ? <InstancesPanel user={user} actions={<CreateInstanceButton user={user} />} /> : user ? <Alert type="error" showIcon message="没有实例读取权限" description="请联系管理员分配所需权限。" /> : <Card className="welcome-card">{status.isPending || auth && session.isPending ? <Spin aria-label="加载账户" /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="登录后查看工作空间"><Space wrap><Button type="primary" disabled={!auth} onClick={() => navigate('/login')}>登录</Button><Button onClick={() => navigate('/app/settings')}>查看平台配置</Button></Space></Empty>}</Card>}
          </>} />
          <Route path="/app/settings" element={user && !hasPermission(user, 'configuration:read') ? <Alert type="error" showIcon message="没有配置读取权限" description="请联系管理员分配所需权限。" /> : <>
            {user && hasPermission(user, 'configuration:read') ? <SettingsPanel user={user} /> : <Card className="configuration-placeholder"><Box size={28} /><h2>请管理员登录</h2><p>登录后查看运行配置。</p></Card>}
            {diagnostics}
          </>} />
          <Route path="/app/storage" element={user && hasPermission(user, 'instances:read') ? <StoragePanel key={user.id} user={user}/> : <Alert type={user?'error':'info'} message={user?'没有数据资产读取权限':'请先登录组织账户'}/>} />
          <Route path="/app/storage/:assetId/browse" element={user && hasPermission(user, 'instances:read') ? <StorageBrowsePage user={user}/> : <Alert type={user?'error':'info'} message={user?'没有数据资产读取权限':'请先登录组织账户'}/>} />
          <Route path="/app/skills" element={user && hasPermission(user, 'skills:read') ? <SkillsPanel key={user.id} user={user}/> : <Alert type={user?'error':'info'} message={user?'没有 Skill 读取权限':'请先登录组织账户'}/>} />
          <Route path="/app/connectors" element={user && hasPermission(user, 'connectors:read') ? <ConnectorsPanel key={user.id} user={user}/> : <Alert type="error" message="没有连接器读取权限"/>} />
          <Route path="/app/memory" element={user && hasPermission(user, 'memory:read') ? <ContextPanel key={user.id+'memory'} user={user} kind="memory"/> : <Alert type="error" message="没有记忆读取权限"/>} />
          <Route path="/app/knowledge" element={user && hasPermission(user, 'knowledge:read') ? <ContextPanel key={user.id+'knowledge'} user={user} kind="knowledge"/> : <Alert type="error" message="没有知识库读取权限"/>} />
          <Route path="/app/models" element={user && hasPermission(user, 'personal-model:read') ? <ModelProvidersPanel key={user.id} user={user} /> : <Alert type="error" showIcon message={user ? '没有个人模型读取权限' : '请先登录组织账户'} />} />
          <Route path="/app/users" element={user && hasPermission(user, 'users:read') ? <UsersPanel user={user} /> : <Alert type="error" showIcon message="没有用户管理权限" />} />
          <Route path="/app/quotas" element={user && hasPermission(user, 'users:read') ? <ResourceQuotasPanel key={user.id} user={user} /> : <Alert type="error" showIcon message="没有资源配额读取权限" />} />
          <Route path="/app/roles" element={user && hasPermission(user, 'roles:read') ? <RolesPanel user={user} /> : <Alert type="error" showIcon message="没有角色管理权限" />} />
          <Route path="/app/audit" element={user && hasPermission(user, 'audit:read') ? <OperationLogsPanel user={user} /> : <Alert type={user ? 'error' : 'info'} message={user ? '没有操作日志读取权限' : '请先登录组织账户'} />} />
          <Route path="/login" element={<Card className="login-section"><span className="login-icon"><ShieldCheck size={28} /></span><h2>{user ? '账户已登录' : '登录 DSH Cloud'}</h2><p>{user ? roleName : '登录账户'}</p>{user ? <Space wrap><Button type="primary" onClick={() => navigate('/app/instances')}>进入工作台</Button><Button loading={logout.isPending} onClick={() => logout.mutate()}>退出登录</Button></Space> : <Button type="primary" disabled={!auth || session.isPending} icon={<ExternalLink size={16} />} onClick={() => window.location.assign('/auth/login?return_to=/app/instances')}>登录账户</Button>}{logout.isError && <Alert type="error" showIcon message={logout.error.message} />}</Card>} />
          <Route path="/app/instances/:id" element={user && hasPermission(user, 'instances:read') ? <InstanceDetails user={user} /> : user ? <Alert type="error" showIcon message="没有实例读取权限" /> : <Card><Empty description="登录后查看实例"><Button onClick={() => navigate('/login')}>登录</Button></Empty></Card>} />
          <Route path="*" element={<Navigate to="/app/instances" replace />} />
        </Routes>
      </Content>
    </Layout>
    <Drawer title="使用帮助" open={helpOpen} onClose={() => setHelpOpen(false)} width={380}><div className="help-content"><h3>模型</h3><p>在“模型提供商”添加连接，校验后设置为个人默认或在创建时选择。</p><h3>工作空间</h3><p>创建后在实例详情进入。停止保留工作数据，删除前需确认实例名称。</p><h3>用户与权限</h3><p>管理员通过用户管理分配角色，在“资源配额”查看占用和分配额度。功能入口按账户权限显示。</p></div></Drawer>
  </Layout></>;
}
