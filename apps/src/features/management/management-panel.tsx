import { useEffect, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Dropdown, Empty, Form, Input, Modal, Select, Space, Spin, Table, Tag, Tooltip } from 'antd';
import { Check, ChevronDown, Info, Plus } from 'lucide-react';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { ManagementClient, type ManagedRole, type ManagedUser, type UserAction } from '../../api/management';
import { APIError } from '../../api/error';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { useTransientNotice } from '../../shared/hooks/use-transient-notice';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { ListSearch } from '../../shared/components/list-search';
import { includeDependencies, permissionGroup, permissionGroups, RolePermissionPicker } from './role-permission-picker';

const client = new ManagementClient();
const userStatuses: Record<string, string> = { ACTIVE: '已启用', DISABLED: '已禁用', PENDING_IDENTITY: '身份待确认', ARCHIVED: '已删除' };
const date = (value: string | null) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未登录';
const profileRules = { name: [{ required: true, whitespace: true, max: 100, message: '请输入1～100字符显示名' }], email: [{ required: true, type: 'email' as const, max: 254, message: '请输入有效邮箱' }] };
const passwordRules = [{ required: true, message: '请输入初始密码' }, { validator: (_: unknown, value: string) => !value || new TextEncoder().encode(value).length >= 12 && new TextEncoder().encode(value).length <= 72 && !/[\x00-\x1f\x7f]/.test(value) ? Promise.resolve() : Promise.reject(new Error('密码须为12～72字节，不能包含控制字符')) }];

export function UsersPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const [filter, setFilter] = useState({ q: '', status: '', role_id: '' });
  const [search, setSearch] = useState('');
  useEffect(() => { const timer = setTimeout(() => { const q = search.trim(); setFilter(current => current.q === q ? current : { ...current, q }); }, 250); return () => clearTimeout(timer); }, [search]);
  const canManage = hasPermission(user, 'users:manage');
  const pagination = usePagePagination(JSON.stringify([user.id, filter]));
  const [roleSearch, setRoleSearch] = useState('');
  const [dialog, setDialog] = useState<{ action: 'create' | 'edit' | 'view' | 'delete' | 'repair-identity' | UserAction; target?: ManagedUser } | null>(null);
  const [requestKey, setRequestKey] = useState('');
  const [notice, setNotice] = useTransientNotice();
  const [form] = Form.useForm();
  const source = Form.useWatch('identity_source', form);
  const users = useQuery({ queryKey: ['management', user.id, 'users', filter, pagination.page, pagination.pageSize], queryFn: ({ signal }) => client.users(user, { ...filter, page: pagination.page, limit: pagination.pageSize }, signal), retry: false });
  const roles = useInfiniteQuery({ queryKey: ['management', user.id, 'role-options', roleSearch], initialPageParam: '', queryFn: ({ pageParam, signal }) => client.userRoleOptions(user, { q: roleSearch, cursor: pageParam, limit: 100 }, signal), getNextPageParam: page => page.next_cursor ?? undefined, retry: false });
  const roleOptions = roles.data?.pages.flatMap(page => page.items.map(role => ({ label: `${role.name} (${role.code})`, value: role.id }))) ?? [];
  if (dialog?.target && !roleOptions.some(option => option.value === dialog.target!.role_id)) roleOptions.unshift({ label: dialog.target.role_name, value: dialog.target.role_id });
  const refresh = () => { void cache.invalidateQueries({ queryKey: ['management', user.id] }); };
  const mutation = useMutation({ mutationFn: async () => {
    if (!dialog) throw new Error('请重新打开操作表单');
    const values = form.getFieldsValue();
    // Mutation variables contain no credentials. Passwords live only in the
    // form and this request's local variables, then are cleared on settlement.
    if (dialog.action === 'create') return client.createUser(user, { display_name: values.display_name.trim(), email: values.email.trim(), role_id: values.role_id, identity_source: values.identity_source, ...(values.identity_source === 'local' ? { initial_password: values.password } : { subject: values.subject.trim() }) }, requestKey);
    const target = dialog.target!;
    if (dialog.action === 'edit') return client.editUser(user, target, { display_name: values.display_name.trim(), email: values.email.trim() });
    if (dialog.action === 'repair-identity') {
      const updated = await client.editUser(user, target, { display_name: values.display_name.trim(), email: values.email.trim() });
      setDialog(current => current ? { ...current, target: updated } : current);
      return client.userAction(user, updated, 'retry-identity', { password: values.password });
    }
    if (dialog.action === 'view') return target;
    if (dialog.action === 'delete') { await client.deleteUser(user, target, values.confirmed_name); return { ...target, status: 'ARCHIVED' as const }; }
    return client.userAction(user, target, dialog.action, dialog.action === 'role' ? { role_id: values.role_id } : dialog.action === 'reset-password' || dialog.action === 'retry-identity' ? { password: values.password } : {});
  }, onSuccess: result => {
    const pendingIdentity = result.status === 'PENDING_IDENTITY';
    const message = dialog?.action === 'delete' ? '用户已删除，登录权限已撤销。'
      : result.password_reset?.status === 'PENDING' ? '密码重置尚未确认，请稍后重试原操作，无需重新输入密码。'
      : ['retry-identity', 'repair-identity'].includes(dialog?.action ?? '') && result.status === 'ACTIVE' ? '身份已确认，用户已启用。'
      : pendingIdentity && result.provisioning?.reason_code === 'LOGIN_EMAIL_IN_USE' ? '登录邮箱已被其他账号占用。请改用未被占用的邮箱，再次修复并启用。'
      : pendingIdentity ? '身份供应仍未完成，账号不能登录。请核对登录邮箱和初始密码后重试。'
      : '操作已完成';
    setNotice(message, result.password_reset?.status === 'PENDING' || pendingIdentity);
    setDialog(null); form.resetFields(); refresh();
  }, onSettled: () => form.resetFields(['password']) });
  useExpiredSession(users.error ?? roles.error ?? mutation.error);
  const open = (action: NonNullable<typeof dialog>['action'], target?: ManagedUser) => {
    mutation.reset(); setNotice(null); form.resetFields(); setRoleSearch(''); setRequestKey(crypto.randomUUID()); setDialog({ action, target });
    form.setFieldsValue(target ? { display_name: target.display_name, email: target.email, role_id: target.role_id } : { identity_source: users.data?.local_accounts ? 'local' : 'oidc', role_id: roles.data?.pages.flatMap(page => page.items).find(role => role.code === 'MEMBER')?.id });
  };
  const roleSelect = <Select showSearch filterOption={false} onSearch={setRoleSearch} options={roleOptions} loading={roles.isFetching} disabled={roles.isError} onPopupScroll={event => { const target = event.currentTarget; if (target.scrollHeight - target.scrollTop - target.clientHeight < 24 && roles.hasNextPage && !roles.isFetchingNextPage) void roles.fetchNextPage(); }} placeholder="选择角色" />;
  const changingPassword = dialog?.action === 'reset-password' || dialog?.action === 'retry-identity' || dialog?.action === 'repair-identity';
  const editing = dialog?.action === 'edit' || dialog?.action === 'create' || dialog?.action === 'repair-identity';
  const title = dialog ? ({ create: '新增用户', edit: '编辑用户', view: '用户详情', role: '分配角色', disable: '禁用用户', enable: '启用用户', archive: '归档用户', delete: '删除用户', 'retry-identity': '重试并启用用户', 'repair-identity': '修复并启用用户', 'reset-password': '重置密码', 'retry-password-reset':'重试密码重置' })[dialog.action] : '';
  const filterBy = (patch: Partial<typeof filter>) => setFilter(current => ({ ...current, ...patch }));
  return <section className="settings-section users-section">
    <div className="provider-page-heading workspace-actions"><h2>用户列表</h2>{canManage && <Button type="primary" icon={<Plus size={16} aria-hidden="true" />} onClick={() => open('create')}>新增用户</Button>}</div>
    <div className="workspace-toolbar users-toolbar">
      <div className="users-filters"><Select aria-label="筛选用户状态" value={filter.status} options={[{ label: '未删除用户', value: '' }, ...Object.entries(userStatuses).map(([value, label]) => ({ value, label }))]} onChange={status => filterBy({ status })} /><Select aria-label="筛选用户角色" allowClear showSearch filterOption={false} onSearch={setRoleSearch} options={roleOptions} placeholder="全部角色" value={filter.role_id || undefined} loading={roles.isFetching} onPopupScroll={event => { const target = event.currentTarget; if (target.scrollHeight - target.scrollTop - target.clientHeight < 24 && roles.hasNextPage && !roles.isFetchingNextPage) void roles.fetchNextPage(); }} onChange={role_id => filterBy({ role_id: role_id ?? '' })} /></div>
      <ListSearch label="搜索用户" placeholder="搜索显示名或邮箱" value={search} onChange={setSearch} onSubmit={() => filterBy({ q: search.trim() })} onRefresh={refresh} loading={users.isFetching} refreshLabel="刷新用户列表" />
    </div>
    {notice && <Alert showIcon closable onClose={() => setNotice(null)} type={notice.includes('未完成') || notice.includes('占用') ? 'warning' : 'success'} message={notice} style={{ marginBottom: 16 }} />}
    {roles.error && <Alert type="error" showIcon message="角色选项加载失败" action={<Button onClick={() => void roles.refetch()}>重试</Button>} style={{ marginBottom: 16 }} />}
    {users.error && <Alert showIcon type="error" message={users.error.message} style={{ marginBottom: 16 }} />}
    <Table<ManagedUser> className="data-table users-table" size="middle" rowKey="id" tableLayout="fixed" dataSource={users.data?.items ?? []} loading={users.isPending} pagination={false} scroll={{ x: 900 }} locale={{ emptyText: users.isError ? '未能获取用户' : filter.q || filter.status || filter.role_id ? '没有符合条件的用户' : '暂无用户' }} columns={[
      { title: '用户 / 联系邮箱', key: 'user', width: 280, render: (_, target) => <div className="user-profile"><Tooltip title={target.display_name || target.id}><Button type="link" onClick={() => open('view', target)}>{target.display_name || target.id.slice(0, 8)}</Button></Tooltip>{target.email && <span title={target.email}>{target.email}</span>}</div> },
      { title: '角色', dataIndex: 'role_name', width: 100 },
      { title: '状态', key: 'status', width: 180, render: (_, target) => <Space size={4} wrap><Tag color={target.status === 'ACTIVE' ? 'green' : 'default'}>{userStatuses[target.status]}</Tag>{target.provisioning?.reason_code === 'LOGIN_EMAIL_IN_USE' && <Tooltip title="登录邮箱被其他账号占用；修改邮箱后重试身份供应。"><Tag color="red">登录邮箱冲突</Tag></Tooltip>}{target.password_reset?.status === 'PENDING' && <Tag color="orange">密码重置待确认</Tag>}</Space> },
      { title: '最后登录', key: 'last-login', width: 180, render: (_, target) => <span className="table-date">{date(target.last_login_at)}</span> },
      ...(canManage ? [{ title: '操作', key: 'actions', width: 200, fixed: 'right' as const, render: (_: unknown, target: ManagedUser) => {
        const actions = [
          { key: 'role', label: '分配角色', disabled: target.id === user.id },
          ...(target.status === 'ACTIVE' ? [{ key: 'disable', label: '禁用', danger: true, disabled: target.id === user.id }] : []),
          ...(target.status === 'DISABLED' ? [{ key: 'enable', label: '启用' }] : []),
          ...(target.status === 'PENDING_IDENTITY' ? [{ key: 'edit', label: '编辑用户' }] : []),
          ...(target.identity_source === 'local' && ['ACTIVE', 'DISABLED'].includes(target.status) ? [{ key: target.password_reset?.status === 'PENDING' ? 'retry-password-reset' : 'reset-password', label: target.password_reset?.status === 'PENDING' ? '重试密码重置' : '重置密码' }] : []),
          ...(['ACTIVE', 'DISABLED'].includes(target.status) ? [{ key: 'delete', label: <Tooltip title={target.id === user.id ? '不能删除当前登录账号' : target.password_reset?.status === 'PENDING' ? '请先完成待确认的密码重置' : undefined}>删除</Tooltip>, danger: true, disabled: target.id === user.id || target.password_reset?.status === 'PENDING' }] : []),
        ];
        return <div className="management-table-actions">{target.status !== 'ARCHIVED' && <Button type={target.status === 'PENDING_IDENTITY' ? 'primary' : undefined} onClick={() => open(target.status === 'PENDING_IDENTITY' ? 'repair-identity' : 'edit', target)}>{target.status === 'PENDING_IDENTITY' ? '修复并启用' : '编辑'}</Button>}<Dropdown trigger={['click']} menu={{ items: actions, onClick: ({ key }) => open(key as UserAction | 'delete', target) }}><Button aria-label="更多用户操作">更多<ChevronDown size={14} aria-hidden="true" /></Button></Dropdown></div>;
      } }] : []),
    ]} />
    <DataPagination label="用户" pagination={pagination} count={users.data?.items.length ?? 0} total={users.data?.total} loading={users.isFetching} failed={users.isError} />
    <Modal open={dialog !== null} title={title} className={dialog?.action === 'role' ? 'role-assignment-dialog' : undefined} width={dialog?.action === 'role' ? 480 : undefined} centered={dialog?.action === 'role'} confirmLoading={mutation.isPending} okText={dialog?.action === 'view' ? '关闭' : dialog?.action === 'delete' ? '确认删除' : dialog?.action === 'role' ? '保存' : dialog?.action === 'retry-identity' || dialog?.action === 'repair-identity' ? '修复并启用' : '确认'} okButtonProps={{ danger: dialog?.action === 'delete', disabled: (dialog?.action === 'create' || dialog?.action === 'role') && (roles.isPending || roles.isError) }} cancelText="取消" cancelButtonProps={{ disabled: mutation.isPending }} onCancel={() => { if (!mutation.isPending) { setDialog(null); form.resetFields(); mutation.reset(); } }} onOk={() => dialog?.action === 'view' ? setDialog(null) : form.submit()} destroyOnHidden>
      {dialog?.target && (dialog.action === 'role' ? <div className="role-assignment-user"><span>分配对象</span><strong>{dialog.target.display_name}</strong>{dialog.target.email && <span>{dialog.target.email}</span>}</div> : <p>{dialog.target.display_name} · 版本 {dialog.target.version}</p>)}
      {dialog?.action === 'view' ? <Descriptions column={1} size="small" items={Object.entries({ 用户ID: dialog.target!.id, 身份来源: dialog.target!.identity_source, 登录邮箱: dialog.target!.login_email || '由组织身份提供方维护', Issuer: dialog.target!.issuer, Subject: dialog.target!.subject, 'Workspace Owner': dialog.target!.workspace_owner_id, 创建时间: date(dialog.target!.created_at), 更新时间: date(dialog.target!.updated_at), 身份供应: dialog.target!.provisioning ? `${dialog.target!.provisioning.status} · ${dialog.target!.provisioning.reason_code || '已确认'}` : '已有组织账号' }).map(([label, children]) => ({ label, children }))} /> : <Form form={form} layout="vertical" disabled={mutation.isPending} onFinish={() => mutation.mutate()}>
        {editing && <><Form.Item label="显示名" name="display_name" rules={profileRules.name}><Input maxLength={100} /></Form.Item><Form.Item label={dialog?.action === 'repair-identity' ? '初始登录邮箱' : '联系邮箱'} name="email" rules={profileRules.email} extra={dialog?.action === 'create' && source === 'local' ? '联系邮箱作为初次登录名，账号确认前可修改；账号启用后再编辑不会改变登录名。' : dialog?.action === 'repair-identity' ? '选择一个未被其他平台账号占用的邮箱；保存后会立即用下方初始密码重试启用。' : dialog?.action === 'edit' && dialog.target?.identity_source === 'local' && dialog.target.status === 'PENDING_IDENTITY' ? '账号尚未创建成功；修改此邮箱会同步更新初始登录名。' : dialog?.action === 'edit' && dialog.target?.identity_source === 'local' ? '修改联系邮箱不会改变登录名。' : undefined}><Input maxLength={254} /></Form.Item></>}
        {dialog?.action === 'create' && <><Form.Item label="身份来源" name="identity_source" rules={[{ required: true }]}><Select options={users.data?.local_accounts ? [{ label: '平台登录账号', value: 'local' }] : [{ label: '绑定已有组织账号', value: 'oidc' }]} /></Form.Item>{source === 'oidc' && <Form.Item label="可信 OIDC Subject" name="subject" rules={[{ required: true, max: 1024 }]} extra="账号须已在当前组织身份提供方创建；邮箱不能代替不可变Subject。"><Input maxLength={1024} /></Form.Item>}{source === 'local' && <Form.Item label="初始密码" name="password" rules={passwordRules} extra="密码提交后不读回。"><Input.Password autoComplete="new-password" /></Form.Item>}</>}
        {(dialog?.action === 'create' || dialog?.action === 'role') && <Form.Item label={dialog.action === 'role' ? '用户角色' : '角色'} name="role_id" rules={[{ required: true, message: '请选择角色' }]}>{roleSelect}</Form.Item>}
        {(dialog?.action === 'create' || dialog?.action === 'role') && roles.isError && <Alert type="error" showIcon message="角色选项加载失败，请重试后再保存" action={<Button onClick={() => void roles.refetch()}>重试</Button>} />}
        {(dialog?.action === 'retry-identity' || dialog?.action === 'repair-identity') && <Alert type="info" showIcon message="身份账号确认成功后，用户会自动启用。" description="密码不会读回；请输入创建时的初始密码。" style={{ marginBottom: 16 }} />}
        {changingPassword && <Form.Item label={dialog?.action === 'retry-identity' || dialog?.action === 'repair-identity' ? '初始密码' : '新密码'} name="password" rules={passwordRules} extra={dialog?.action === 'retry-identity' || dialog?.action === 'repair-identity' ? '系统会先保存登录邮箱，再立即重试身份确认。' : '供应结果未知时核对原账号，已存在账号不会被重复创建或自动改密。'}><Input.Password autoComplete="new-password" /></Form.Item>}
        {dialog?.action==='retry-password-reset'&&<p>继续原密码重置，无需重新输入密码；完成后原登录会话失效。</p>}
        {dialog?.action === 'disable' && <Alert type="warning" showIcon message="将撤销登录和工作空间会话，并安全停止计算；保留数据。" />}
        {dialog?.action === 'delete' && <><Alert type="warning" showIcon message="删除后禁止登录，并从用户与资源配额的默认列表移除；保留历史和操作日志。" description="请先处理该用户的实例、保留盘和恢复占用，删除后无法重新启用。" /><Form.Item label="输入用户名确认删除" name="confirmed_name" style={{ marginTop: 16 }} extra={`请输入：${dialog.target!.display_name || dialog.target!.id}`} rules={[{ required: true, message: '请输入完整用户名' }, { validator: (_, value) => value === (dialog.target!.display_name || dialog.target!.id) ? Promise.resolve() : Promise.reject(new Error('用户名不匹配，请输入完整用户名')) }]}><Input autoComplete="off" /></Form.Item></>}
        {dialog?.action === 'enable' && <p>启用后用户必须重新登录，旧会话不会恢复。</p>}
        {dialog?.action === 'role' && <div className="role-assignment-note"><Info size={16} aria-hidden="true" /><div><p>保存后，用户需重新登录。</p><p>若撤销运行权限，工作空间将安全停止，数据保留。</p></div></div>}
      </Form>}
      {mutation.error && <Alert type="error" showIcon message={mutation.error.message} action={mutation.error instanceof APIError && mutation.error.code === 'VERSION_CONFLICT' ? <Button onClick={() => { refresh(); setDialog(null); }}>重新读取</Button> : undefined} style={{ marginTop: 16 }} />}
    </Modal>
  </section>;
}

export function RolesPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient(); const [form] = Form.useForm();
  const [filter, setFilter] = useState({ q: '' });
  const [search, setSearch] = useState('');
  useEffect(() => { const timer = setTimeout(() => { const q = search.trim(); setFilter(current => current.q === q ? current : { q }); }, 250); return () => clearTimeout(timer); }, [search]);
  const canManage = hasPermission(user, 'roles:manage');
  const pagination = usePagePagination(JSON.stringify([user.id, filter]));
  const [dialog, setDialog] = useState<{ action: 'create' | 'edit' | 'view' | 'delete'; target?: ManagedRole } | null>(null);
  const [requestKey, setRequestKey] = useState('');
  const selected: string[] = Form.useWatch('permissions', form) ?? [];
  const roles = useQuery({ queryKey: ['management', user.id, 'roles', filter, pagination.page, pagination.pageSize], queryFn: ({ signal }) => client.roles(user, { ...filter, page: pagination.page, limit: pagination.pageSize }, signal), retry: false });
  const permissions = useQuery({ queryKey: ['management', user.id, 'permissions'], queryFn: ({ signal }) => client.permissions(user, signal), retry: false });
  const labels = new Map(permissions.data?.items.map(permission => [permission.id, permission.name]));
  const descriptions = new Map(permissions.data?.items.map(permission => [permission.id, permission.description]));
  const missing = permissions.data?.items.filter(permission => selected.includes(permission.id)).flatMap(permission => permission.dependencies.filter(dependency => !selected.includes(dependency)).map(dependency => `${permission.name}需要${labels.get(dependency) ?? dependency}`)) ?? [];
  const refresh = () => { void cache.invalidateQueries({ queryKey: ['management', user.id] }); };
  const mutation = useMutation({ mutationFn: async () => {
    if (!dialog) throw new Error('请重新打开角色表单');
    if (dialog.action === 'delete') return client.deleteRole(user, dialog.target!);
    if (missing.length) throw new Error('请先选择完整的依赖权限');
    const values = form.getFieldsValue();
    return client.saveRole(user, { code: values.code, name: values.name.trim(), description: values.description ?? '', permissions: values.permissions ?? [], expected_version: dialog.target?.version ?? 0 }, dialog.target, dialog.action === 'create' ? requestKey : undefined);
  }, onSuccess: () => { setDialog(null); refresh(); } });
  useExpiredSession(roles.error ?? permissions.error ?? mutation.error);
  const open = (action: NonNullable<typeof dialog>['action'], target?: ManagedRole) => { mutation.reset(); form.resetFields(); setRequestKey(crypto.randomUUID()); setDialog({ action, target }); form.setFieldsValue(target ?? { permissions: [], description: '' }); };
  const readOnly = dialog?.action === 'view';
  const grantedGroups = permissionGroups.map(group => ({ name: group.name, items: (dialog?.target?.permissions ?? []).filter(id => permissionGroup(id) === group) })).filter(group => group.items.length);
  return <section className="settings-section roles-section">
    <div className="provider-page-heading workspace-actions"><h2>角色列表</h2>{canManage && <Button type="primary" icon={<Plus size={16} aria-hidden="true" />} onClick={() => open('create')}>新增角色</Button>}</div>
    <div className="workspace-toolbar roles-toolbar"><ListSearch label="搜索角色" placeholder="搜索角色名称或编码" value={search} onChange={setSearch} onSubmit={() => setFilter({ q: search.trim() })} onRefresh={refresh} loading={roles.isFetching} refreshLabel="刷新角色列表" /></div>
    {(roles.error ?? permissions.error) && <Alert type="error" showIcon message={(roles.error ?? permissions.error)!.message} style={{ marginBottom: 16 }} />}
    <Table<ManagedRole> className="data-table roles-table" size="middle" rowKey="id" tableLayout="fixed" dataSource={roles.data?.items ?? []} loading={roles.isPending} pagination={false} scroll={{ x: 820 }} locale={{ emptyText: roles.isError ? '未能获取角色' : filter.q ? '没有符合条件的角色' : '暂无角色' }} columns={[
      { title: '角色 / 编码', key: 'role', width: 300, render: (_, role) => <div className="user-profile"><Tooltip title={role.name}><Button type="link" onClick={() => open('view', role)}>{role.name}</Button></Tooltip><span title={role.code}>{role.code}</span></div> },
      { title: '类型', key: 'builtin', width: 110, render: (_, role) => <Tag>{role.builtin ? '内置' : '自定义'}</Tag> },
      { title: '权限数', key: 'permissions', width: 110, render: (_, role) => role.permissions.length },
      { title: '分配用户', dataIndex: 'user_count', width: 120 },
      ...(canManage ? [{ title: '操作', key: 'actions', width: 180, fixed: 'right' as const, render: (_: unknown, role: ManagedRole) => <div className="management-table-actions"><Tooltip title={role.builtin ? '内置角色不可编辑' : undefined}><Button disabled={role.builtin} onClick={() => open('edit', role)}>编辑</Button></Tooltip><Dropdown trigger={['click']} menu={{ items: [{ key: 'delete', label: <Tooltip title={role.builtin ? '内置角色不可删除' : role.user_count > 0 ? '请先将已分配用户改派到其他角色' : undefined}>删除角色</Tooltip>, danger: true, disabled: role.builtin || role.user_count > 0 }], onClick: () => open('delete', role) }}><Button aria-label="更多角色操作">更多<ChevronDown size={14} aria-hidden="true" /></Button></Dropdown></div> }] : []),
    ]} />
    <DataPagination label="角色" pagination={pagination} count={roles.data?.items.length ?? 0} total={roles.data?.total} loading={roles.isFetching} failed={roles.isError} />
    <Modal open={dialog !== null} className={readOnly ? 'role-permissions-dialog' : dialog?.action === 'create' || dialog?.action === 'edit' ? 'role-editor-dialog' : undefined} centered={readOnly || dialog?.action === 'create' || dialog?.action === 'edit'} width={readOnly ? 680 : dialog?.action === 'delete' ? 520 : 760} title={dialog?.action === 'create' ? '新增角色' : dialog?.action === 'delete' ? '删除角色' : readOnly ? '角色权限详情' : '编辑角色权限'} okText={readOnly ? '关闭' : '确认'} confirmLoading={mutation.isPending} okButtonProps={{ danger: dialog?.action === 'delete', disabled: !readOnly && dialog?.action !== 'delete' && (missing.length > 0 || permissions.isPending || permissions.isError) }} cancelText="取消" cancelButtonProps={readOnly ? { style: { display: 'none' } } : undefined} onCancel={() => { if (!mutation.isPending) setDialog(null); }} onOk={() => readOnly ? setDialog(null) : dialog?.action === 'delete' ? mutation.mutate() : form.submit()} destroyOnHidden>
      {readOnly && dialog?.target ? <>
        <div className="role-permissions-summary"><div><h3>{dialog.target.name}</h3><span>{dialog.target.code}</span></div><Tag>{dialog.target.builtin ? '内置角色' : '自定义角色'}</Tag></div>
        {dialog.target.description && <p className="role-permissions-description">{dialog.target.description}</p>}
        <div className="role-permissions-heading"><h3>已拥有权限</h3><span>{dialog.target.permissions.length} 项权限 · {dialog.target.user_count} 名用户</span></div>
        {permissions.isError && <Alert type="warning" showIcon message="权限名称加载失败，暂时显示权限编码" action={<Button onClick={() => void permissions.refetch()}>重试</Button>} />}
        <Spin spinning={permissions.isPending}><div className="role-permissions-groups">
          {grantedGroups.map(group => <section className="role-permission-group" key={group.name} aria-label={group.name}><div className="role-permission-group-heading"><h4>{group.name}</h4><span>{group.items.length} 项</span></div><ul>{group.items.map(id => <li key={id}><Check size={16} aria-hidden="true" /><span className="role-permission-copy"><span>{labels.get(id) ?? id}</span>{descriptions.get(id) && <small>{descriptions.get(id)}</small>}</span></li>)}</ul></section>)}
          {!grantedGroups.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该角色尚未分配权限" />}
        </div></Spin>
      </> : dialog?.action === 'delete' ? <Alert type="warning" showIcon message={`确认删除“${dialog.target!.name}”？仍有分配用户时会拒绝。`} /> : <Form form={form} layout="vertical" onFinish={() => mutation.mutate()} disabled={mutation.isPending}>
        <div className="role-basic-fields">
          <Form.Item label="角色编码" name="code" rules={[{ required: true, pattern: /^[a-z][a-z0-9_]{2,31}$/, message: '3～32字符，小写字母开头，字母/数字/下划线' }]}><Input disabled={!!dialog?.target} maxLength={32} /></Form.Item>
          <Form.Item label="角色名称" name="name" rules={[{ required: true, whitespace: true, max: 64 }]}><Input maxLength={64} /></Form.Item>
          <Form.Item className="role-description-field" label="说明" name="description" rules={[{ max: 500 }]}><Input.TextArea maxLength={500} rows={2} /></Form.Item>
        </div>
        {dialog?.target && !readOnly && <Alert type="warning" showIcon message={`当前分配 ${dialog.target.user_count} 名用户；保存会撤销这些用户的旧会话。`} description={`移除：${dialog.target.permissions.filter(id => !selected.includes(id)).map(id => labels.get(id) ?? id).join('、') || '无'}；新增：${selected.filter(id => !dialog.target!.permissions.includes(id)).map(id => labels.get(id) ?? id).join('、') || '无'}`} style={{ marginBottom: 16 }} />}
        {permissions.isPending ? <div className="role-permissions-loading"><Spin aria-label="加载功能权限" /></div> : permissions.isError ? <Alert type="error" showIcon message="功能权限加载失败，请刷新角色列表后重试" /> : <Form.Item className="role-permission-form-item" label={<span className="role-permission-label"><strong>功能权限</strong><span>已选 {selected.length} / {permissions.data?.items.length ?? 0} 项</span></span>} name="permissions"><RolePermissionPicker catalog={permissions.data?.items ?? []} disabled={mutation.isPending} /></Form.Item>}
        {missing.length > 0 && <Alert type="error" showIcon message="请显式选择依赖权限" description={missing.join('；')} action={<Button disabled={mutation.isPending} onClick={() => form.setFieldValue('permissions', includeDependencies(selected, permissions.data?.items ?? []))}>补选依赖权限</Button>} />}
      </Form>}
      {mutation.error && <Alert type="error" showIcon message={mutation.error.message} action={mutation.error instanceof APIError && mutation.error.code === 'VERSION_CONFLICT' ? <Button onClick={() => { refresh(); setDialog(null); }}>重新读取</Button> : undefined} style={{ marginTop: 16 }} />}
    </Modal>
  </section>;
}
