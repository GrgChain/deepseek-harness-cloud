import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Avatar,
  Button,
  Card,
  Drawer,
  Empty,
  Layout,
  Menu,
  Space,
  Spin,
  Tooltip,
} from 'antd';
import { Box, CircleHelp, ExternalLink, LockKeyhole, Menu as MenuIcon } from 'lucide-react';
import { PlatformClient } from '../api/platform';
import { IdentityClient, hasPermission } from '../api/identity';
import { expireSession, loginReturnPath } from '../shared/hooks/use-expired-session';
import { BrandLogo } from './brand-logo';
import { buildMenuItems, DiagnosticsSection, HelpDrawer, pageTitle } from './navigation';

const ConnectorsPanel = lazy(() =>
  import('../features/connectors/connectors-panel').then((m) => ({ default: m.ConnectorsPanel })),
);
const ContextPanel = lazy(() =>
  import('../features/context/context-panel').then((m) => ({ default: m.ContextPanel })),
);
const SkillsPanel = lazy(() =>
  import('../features/skills/skills-panel').then((m) => ({ default: m.SkillsPanel })),
);
const PluginsPanel = lazy(() =>
  import('../features/plugins/plugins-panel').then((m) => ({ default: m.PluginsPanel })),
);
const StoragePanel = lazy(() =>
  import('../features/storage/storage-panel').then((m) => ({ default: m.StoragePanel })),
);
const StorageBrowsePage = lazy(() =>
  import('../features/storage/storage-panel').then((m) => ({ default: m.StorageBrowsePage })),
);
const OperationLogsPanel = lazy(() =>
  import('../features/operation-logs/operation-logs-panel').then((m) => ({
    default: m.OperationLogsPanel,
  })),
);
const UsersPanel = lazy(() =>
  import('../features/management/management-panel').then((m) => ({ default: m.UsersPanel })),
);
const RolesPanel = lazy(() =>
  import('../features/management/management-panel').then((m) => ({ default: m.RolesPanel })),
);
const ResourceQuotasPanel = lazy(() =>
  import('../features/resource-quotas/resource-quotas-panel').then((m) => ({
    default: m.ResourceQuotasPanel,
  })),
);
const ModelProvidersPanel = lazy(() =>
  import('../features/models/model-providers-panel').then((m) => ({
    default: m.ModelProvidersPanel,
  })),
);
const SettingsPanel = lazy(() =>
  import('../features/settings/settings-panel').then((m) => ({ default: m.SettingsPanel })),
);
const InstancesPanel = lazy(() =>
  import('../features/workspaces/instances-panel').then((m) => ({ default: m.InstancesPanel })),
);
const InstanceDetails = lazy(() =>
  import('../features/workspaces/instances-panel').then((m) => ({ default: m.InstanceDetails })),
);
const CreateInstanceButton = lazy(() =>
  import('../features/workspaces/instance-commands').then((m) => ({
    default: m.CreateInstanceButton,
  })),
);

const platform = new PlatformClient();
const identity = new IdentityClient();
const { Sider, Header, Content } = Layout;

function RouteFallback() {
  return (
    <div style={{ display: 'flex', justifyContent: 'center', padding: '48px 0' }} role="status">
      <Spin aria-label="加载页面" />
    </div>
  );
}

export function App() {
  const [navOpen, setNavOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  useEffect(() => {
    contentRef.current?.focus({ preventScroll: true });
  }, [location.pathname]);
  const home = location.pathname === '/app/instances';
  const settings = location.pathname === '/app/settings';
  const login = location.pathname === '/login';
  const detail = location.pathname.startsWith('/app/instances/');
  const storageBrowse = /^\/app\/storage\/[^/]+\/browse$/.test(location.pathname);
  const title = pageTitle(location.pathname, detail, storageBrowse);
  const status = useQuery({
    queryKey: ['platform-status'],
    queryFn: ({ signal }) => platform.status(signal),
    refetchInterval: (query) => (query.state.status === 'error' ? false : 30_000),
  });
  const online = status.data?.online === true && !status.isError;
  const ready = online && status.data?.dependenciesReady;
  const auth = online && status.data?.authenticationReady;
  const cache = useQueryClient();
  const session = useQuery({
    queryKey: ['identity'],
    queryFn: ({ signal }) => identity.me(signal),
    enabled: !!auth,
    refetchInterval: 60_000,
    staleTime: 15_000,
    retry: false,
  });
  const user = session.isError ? null : session.data;
  const returnTo = loginReturnPath(
    login ? new URLSearchParams(location.search).get('return_to') : location.pathname,
  );
  const anonymous = !!auth && session.isSuccess && session.data === null;
  const redirecting = useRef(false);
  useEffect(() => {
    if (!anonymous || redirecting.current) return;
    redirecting.current = true;
    window.location.replace('/auth/login?return_to=' + encodeURIComponent(returnTo));
  }, [anonymous, returnTo]);
  const logout = useMutation({
    mutationFn: () => (user ? identity.logout(user) : Promise.resolve()),
    onSuccess: () => {
      expireSession(cache);
      navigate('/login');
    },
  });
  const refresh = () => {
    void status.refetch();
    if (auth) void session.refetch();
  };
  const roleName = user?.memberships[0]?.role_name ?? '组织用户';
  const menuItems = buildMenuItems(user);
  const nav = (
    <div className="sidebar-content">
      <Link className="brand" to="/app/instances" onClick={() => setNavOpen(false)}>
        <span className="brand-mark">
          <BrandLogo size={36} />
        </span>
        <span className="brand-copy">
          <span className="brand-name">DSH Cloud</span>
          <span className="brand-tagline">AI Workspace</span>
        </span>
      </Link>
      <nav aria-label="主导航">
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[
            detail ? '/app/instances' : storageBrowse ? '/app/storage' : location.pathname,
          ]}
          items={menuItems}
          onClick={() => setNavOpen(false)}
        />
      </nav>
    </div>
  );
  const connectionMessage = status.isPending
    ? null
    : !online
      ? '平台尚未连接'
      : !auth
        ? '身份服务尚未就绪'
        : session.isError
          ? '无法验证用户会话'
          : null;
  const diagnostics = (
    <DiagnosticsSection
      online={online}
      ready={!!ready}
      auth={!!auth}
      pending={status.isPending}
      fetching={status.isFetching}
      onRefresh={refresh}
    />
  );

  if (anonymous || status.isPending || (auth && session.isPending))
    return (
      <main className="auth-transition" aria-busy="true">
        <span className="brand-mark">
          <BrandLogo size={52} />
        </span>
        <h1>DSH Cloud</h1>
        <div role="status">
          <Spin size="small" />
          {anonymous ? '正在前往登录' : '正在验证账户'}
        </div>
      </main>
    );

  return (
    <>
      <a className="skip-link" href="#main-content">
        跳至主要内容
      </a>
      <Layout className="app-shell">
        <Sider width={240} theme="dark" className="sidebar">
          {nav}
        </Sider>
        <Drawer
          className="navigation-drawer"
          open={navOpen}
          onClose={() => setNavOpen(false)}
          placement="left"
          width={264}
          title="导航"
        >
          <div className="mobile-nav">{nav}</div>
        </Drawer>
        <Layout className="main-shell">
          <Header className="topbar">
            <div className="topbar-title">
              <Button
                className="menu-button"
                type="text"
                aria-label="打开导航"
                icon={<MenuIcon size={20} />}
                onClick={() => setNavOpen(true)}
              />
              <h1>{title}</h1>
            </div>
            <Space size={8}>
              <Tooltip title="使用帮助">
                <Button
                  className="topbar-icon-button"
                  type="text"
                  aria-label="使用帮助"
                  icon={<CircleHelp size={18} />}
                  onClick={() => setHelpOpen(true)}
                />
              </Tooltip>
              <Button
                className="account-button"
                type="text"
                aria-label={user ? `${roleName}账户` : '尚未登录'}
                onClick={() => {
                  navigate('/login');
                  setNavOpen(false);
                }}
              >
                <Avatar size={28} icon={<LockKeyhole size={14} />} />
                {user ? roleName : '尚未登录'}
              </Button>
            </Space>
          </Header>
          <Content
            ref={contentRef}
            id="main-content"
            role="main"
            aria-label={title}
            tabIndex={-1}
            className={`page-content ${settings ? 'settings-page' : login ? 'login-page' : ''}`}
          >
            {connectionMessage && (
              <Alert
                className="connection-alert"
                type="warning"
                showIcon
                message={connectionMessage}
                description={
                  !online
                    ? '请稍后重试。'
                    : !auth
                      ? '暂时无法登录，请稍后重试。'
                      : '请重新检查或稍后登录。'
                }
                action={
                  <Button size="small" onClick={refresh} loading={status.isFetching}>
                    重新检查
                  </Button>
                }
              />
            )}
            {home && !user && (
              <div className="page-actions">
                <Suspense fallback={null}>
                  <CreateInstanceButton user={user} />
                </Suspense>
              </div>
            )}
            <Suspense fallback={<RouteFallback />}>
              <Routes>
                <Route
                  path="/app/instances"
                  element={
                    <>
                      {user && hasPermission(user, 'instances:read') ? (
                        <InstancesPanel
                          user={user}
                          actions={<CreateInstanceButton user={user} />}
                        />
                      ) : user ? (
                        <Alert
                          type="error"
                          showIcon
                          message="没有实例读取权限"
                          description="请联系管理员分配所需权限。"
                        />
                      ) : (
                        <Card className="welcome-card">
                          {status.isPending || (auth && session.isPending) ? (
                            <Spin aria-label="加载账户" />
                          ) : (
                            <Empty
                              image={Empty.PRESENTED_IMAGE_SIMPLE}
                              description="登录后查看工作空间"
                            >
                              <Space wrap>
                                <Button
                                  type="primary"
                                  disabled={!auth}
                                  onClick={() => navigate('/login')}
                                >
                                  登录
                                </Button>
                                <Button onClick={() => navigate('/app/settings')}>
                                  查看平台配置
                                </Button>
                              </Space>
                            </Empty>
                          )}
                        </Card>
                      )}
                    </>
                  }
                />
                <Route
                  path="/app/settings"
                  element={
                    user && !hasPermission(user, 'configuration:read') ? (
                      <Alert
                        type="error"
                        showIcon
                        message="没有配置读取权限"
                        description="请联系管理员分配所需权限。"
                      />
                    ) : (
                      <>
                        {user && hasPermission(user, 'configuration:read') ? (
                          <SettingsPanel user={user} diagnostics={diagnostics} />
                        ) : (
                          <>
                            <Card className="configuration-placeholder">
                              <Box size={28} />
                              <h2>请管理员登录</h2>
                              <p>登录后查看运行配置。</p>
                            </Card>
                            {diagnostics}
                          </>
                        )}
                      </>
                    )
                  }
                />
                <Route
                  path="/app/storage"
                  element={
                    user && hasPermission(user, 'instances:read') ? (
                      <StoragePanel key={user.id} user={user} />
                    ) : (
                      <Alert
                        type={user ? 'error' : 'info'}
                        message={user ? '没有数据资产读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/app/storage/:assetId/browse"
                  element={
                    user && hasPermission(user, 'instances:read') ? (
                      <StorageBrowsePage user={user} />
                    ) : (
                      <Alert
                        type={user ? 'error' : 'info'}
                        message={user ? '没有数据资产读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/app/skills"
                  element={
                    user && hasPermission(user, 'skills:read') ? (
                      <SkillsPanel key={user.id} user={user} />
                    ) : (
                      <Alert
                        type={user ? 'error' : 'info'}
                        message={user ? '没有 Skill 读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/app/connectors"
                  element={
                    user && hasPermission(user, 'connectors:read') ? (
                      <ConnectorsPanel key={user.id} user={user} />
                    ) : (
                      <Alert type="error" message="没有连接器读取权限" />
                    )
                  }
                />
                <Route
                  path="/app/plugins"
                  element={
                    user && hasPermission(user, 'instances:read') ? (
                      <PluginsPanel key={user.id} user={user} />
                    ) : (
                      <Alert
                        type={user ? 'error' : 'info'}
                        showIcon
                        message={user ? '没有实例读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/app/memory"
                  element={
                    user && hasPermission(user, 'memory:read') ? (
                      <ContextPanel key={user.id + 'memory'} user={user} kind="memory" />
                    ) : (
                      <Alert type="error" message="没有记忆读取权限" />
                    )
                  }
                />
                <Route
                  path="/app/knowledge"
                  element={
                    user && hasPermission(user, 'knowledge:read') ? (
                      <ContextPanel key={user.id + 'knowledge'} user={user} kind="knowledge" />
                    ) : (
                      <Alert type="error" message="没有知识库读取权限" />
                    )
                  }
                />
                <Route
                  path="/app/models"
                  element={
                    user && hasPermission(user, 'personal-model:read') ? (
                      <ModelProvidersPanel key={user.id} user={user} />
                    ) : (
                      <Alert
                        type="error"
                        showIcon
                        message={user ? '没有个人模型读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/app/users"
                  element={
                    user && hasPermission(user, 'users:read') ? (
                      <UsersPanel user={user} />
                    ) : (
                      <Alert type="error" showIcon message="没有用户管理权限" />
                    )
                  }
                />
                <Route
                  path="/app/quotas"
                  element={
                    user && hasPermission(user, 'users:read') ? (
                      <ResourceQuotasPanel key={user.id} user={user} />
                    ) : (
                      <Alert type="error" showIcon message="没有资源配额读取权限" />
                    )
                  }
                />
                <Route
                  path="/app/roles"
                  element={
                    user && hasPermission(user, 'roles:read') ? (
                      <RolesPanel user={user} />
                    ) : (
                      <Alert type="error" showIcon message="没有角色管理权限" />
                    )
                  }
                />
                <Route
                  path="/app/audit"
                  element={
                    user && hasPermission(user, 'audit:read') ? (
                      <OperationLogsPanel user={user} />
                    ) : (
                      <Alert
                        type={user ? 'error' : 'info'}
                        message={user ? '没有操作日志读取权限' : '请先登录组织账户'}
                      />
                    )
                  }
                />
                <Route
                  path="/login"
                  element={
                    <Card className="login-section">
                      <span className="login-icon">
                        <BrandLogo size={56} />
                      </span>
                      <h2>{user ? '账户已登录' : '登录 DSH Cloud'}</h2>
                      <p>{user ? roleName : '登录账户'}</p>
                      {user ? (
                        <Space wrap>
                          <Button type="primary" onClick={() => navigate('/app/instances')}>
                            进入工作台
                          </Button>
                          <Button loading={logout.isPending} onClick={() => logout.mutate()}>
                            退出登录
                          </Button>
                        </Space>
                      ) : (
                        <Button
                          type="primary"
                          disabled={!auth || session.isPending}
                          icon={<ExternalLink size={16} />}
                          onClick={() =>
                            window.location.assign('/auth/login?return_to=/app/instances')
                          }
                        >
                          登录账户
                        </Button>
                      )}
                      {logout.isError && (
                        <Alert type="error" showIcon message={logout.error.message} />
                      )}
                    </Card>
                  }
                />
                <Route
                  path="/app/instances/:id"
                  element={
                    user && hasPermission(user, 'instances:read') ? (
                      <InstanceDetails user={user} />
                    ) : user ? (
                      <Alert type="error" showIcon message="没有实例读取权限" />
                    ) : (
                      <Card>
                        <Empty description="登录后查看实例">
                          <Button onClick={() => navigate('/login')}>登录</Button>
                        </Empty>
                      </Card>
                    )
                  }
                />
                <Route path="*" element={<Navigate to="/app/instances" replace />} />
              </Routes>
            </Suspense>
          </Content>
        </Layout>
        <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
      </Layout>
    </>
  );
}
