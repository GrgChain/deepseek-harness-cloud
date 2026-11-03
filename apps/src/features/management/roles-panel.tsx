import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Spin,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { Check, ChevronDown, Plus } from 'lucide-react';
import { useEffect, useState } from 'react';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { type ManagedRole } from '../../api/management';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { ListSearch } from '../../shared/components/list-search';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import {
  includeDependencies,
  permissionGroup,
  permissionGroups,
  RolePermissionPicker,
} from './role-permission-picker';

import { client } from './management-client';

export function RolesPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const [form] = Form.useForm();
  const [filter, setFilter] = useState({ q: '' });
  const [search, setSearch] = useState('');
  useEffect(() => {
    const timer = setTimeout(() => {
      const q = search.trim();
      setFilter((current) => (current.q === q ? current : { q }));
    }, 250);
    return () => clearTimeout(timer);
  }, [search]);
  const canManage = hasPermission(user, 'roles:manage');
  const pagination = usePagePagination(JSON.stringify([user.id, filter]));
  const [dialog, setDialog] = useState<{
    action: 'create' | 'edit' | 'view' | 'delete';
    target?: ManagedRole;
  } | null>(null);
  const [requestKey, setRequestKey] = useState('');
  const selected: string[] = Form.useWatch('permissions', form) ?? [];
  const roles = useQuery({
    queryKey: ['management', user.id, 'roles', filter, pagination.page, pagination.pageSize],
    queryFn: ({ signal }) =>
      client.roles(user, { ...filter, page: pagination.page, limit: pagination.pageSize }, signal),
    retry: false,
  });
  const permissions = useQuery({
    queryKey: ['management', user.id, 'permissions'],
    queryFn: ({ signal }) => client.permissions(user, signal),
    retry: false,
  });
  const labels = new Map(
    permissions.data?.items.map((permission) => [permission.id, permission.name]),
  );
  const descriptions = new Map(
    permissions.data?.items.map((permission) => [permission.id, permission.description]),
  );
  const missing =
    permissions.data?.items
      .filter((permission) => selected.includes(permission.id))
      .flatMap((permission) =>
        permission.dependencies
          .filter((dependency) => !selected.includes(dependency))
          .map((dependency) => `${permission.name}需要${labels.get(dependency) ?? dependency}`),
      ) ?? [];
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ['management', user.id] });
  };
  const mutation = useMutation({
    mutationFn: async () => {
      if (!dialog) throw new Error('请重新打开角色表单');
      if (dialog.action === 'delete') return client.deleteRole(user, dialog.target!);
      if (missing.length) throw new Error('请先选择完整的依赖权限');
      const values = form.getFieldsValue();
      return client.saveRole(
        user,
        {
          code: values.code,
          name: values.name.trim(),
          description: values.description ?? '',
          permissions: values.permissions ?? [],
          expected_version: dialog.target?.version ?? 0,
        },
        dialog.target,
        dialog.action === 'create' ? requestKey : undefined,
      );
    },
    onSuccess: () => {
      setDialog(null);
      refresh();
    },
  });
  useExpiredSession(roles.error ?? permissions.error ?? mutation.error);
  const open = (action: NonNullable<typeof dialog>['action'], target?: ManagedRole) => {
    mutation.reset();
    form.resetFields();
    setRequestKey(crypto.randomUUID());
    setDialog({ action, target });
    form.setFieldsValue(target ?? { permissions: [], description: '' });
  };
  const readOnly = dialog?.action === 'view';
  const grantedGroups = permissionGroups
    .map((group) => ({
      name: group.name,
      items: (dialog?.target?.permissions ?? []).filter((id) => permissionGroup(id) === group),
    }))
    .filter((group) => group.items.length);
  return (
    <section className="settings-section roles-section">
      <div className="provider-page-heading workspace-actions">
        <h2>角色列表</h2>
        {canManage && (
          <Button
            type="primary"
            icon={<Plus size={16} aria-hidden="true" />}
            onClick={() => open('create')}
          >
            新增角色
          </Button>
        )}
      </div>
      <div className="workspace-toolbar roles-toolbar">
        <ListSearch
          label="搜索角色"
          placeholder="搜索角色名称或编码"
          value={search}
          onChange={setSearch}
          onSubmit={() => setFilter({ q: search.trim() })}
          onRefresh={refresh}
          loading={roles.isFetching}
          refreshLabel="刷新角色列表"
        />
      </div>
      {(roles.error ?? permissions.error) && (
        <Alert
          type="error"
          showIcon
          message={(roles.error ?? permissions.error)!.message}
          style={{ marginBottom: 16 }}
        />
      )}
      <Table<ManagedRole>
        className="data-table roles-table"
        size="middle"
        rowKey="id"
        tableLayout="fixed"
        dataSource={roles.data?.items ?? []}
        loading={roles.isPending}
        pagination={false}
        scroll={{ x: 820 }}
        locale={{
          emptyText: roles.isError ? '未能获取角色' : filter.q ? '没有符合条件的角色' : '暂无角色',
        }}
        columns={[
          {
            title: '角色 / 编码',
            key: 'role',
            width: 300,
            render: (_, role) => (
              <div className="user-profile">
                <Tooltip title={role.name}>
                  <Button type="link" onClick={() => open('view', role)}>
                    {role.name}
                  </Button>
                </Tooltip>
                <span title={role.code}>{role.code}</span>
              </div>
            ),
          },
          {
            title: '类型',
            key: 'builtin',
            width: 110,
            render: (_, role) => <Tag>{role.builtin ? '内置' : '自定义'}</Tag>,
          },
          {
            title: '权限数',
            key: 'permissions',
            width: 110,
            render: (_, role) => role.permissions.length,
          },
          { title: '分配用户', dataIndex: 'user_count', width: 120 },
          ...(canManage
            ? [
                {
                  title: '操作',
                  key: 'actions',
                  width: 180,
                  fixed: 'right' as const,
                  render: (_: unknown, role: ManagedRole) => (
                    <div className="management-table-actions">
                      <Tooltip title={role.builtin ? '内置角色不可编辑' : undefined}>
                        <Button disabled={role.builtin} onClick={() => open('edit', role)}>
                          编辑
                        </Button>
                      </Tooltip>
                      <Dropdown
                        trigger={['click']}
                        menu={{
                          items: [
                            {
                              key: 'delete',
                              label: (
                                <Tooltip
                                  title={
                                    role.builtin
                                      ? '内置角色不可删除'
                                      : role.user_count > 0
                                        ? '请先将已分配用户改派到其他角色'
                                        : undefined
                                  }
                                >
                                  删除角色
                                </Tooltip>
                              ),
                              danger: true,
                              disabled: role.builtin || role.user_count > 0,
                            },
                          ],
                          onClick: () => open('delete', role),
                        }}
                      >
                        <Button aria-label="更多角色操作">
                          更多
                          <ChevronDown size={14} aria-hidden="true" />
                        </Button>
                      </Dropdown>
                    </div>
                  ),
                },
              ]
            : []),
        ]}
      />
      <DataPagination
        label="角色"
        pagination={pagination}
        count={roles.data?.items.length ?? 0}
        total={roles.data?.total}
        loading={roles.isFetching}
        failed={roles.isError}
      />
      <Modal
        open={dialog !== null}
        className={
          readOnly
            ? 'role-permissions-dialog'
            : dialog?.action === 'create' || dialog?.action === 'edit'
              ? 'role-editor-dialog'
              : undefined
        }
        centered={readOnly || dialog?.action === 'create' || dialog?.action === 'edit'}
        width={readOnly ? 680 : dialog?.action === 'delete' ? 520 : 760}
        title={
          dialog?.action === 'create'
            ? '新增角色'
            : dialog?.action === 'delete'
              ? '删除角色'
              : readOnly
                ? '角色权限详情'
                : '编辑角色权限'
        }
        okText={readOnly ? '关闭' : '确认'}
        confirmLoading={mutation.isPending}
        okButtonProps={{
          danger: dialog?.action === 'delete',
          disabled:
            !readOnly &&
            dialog?.action !== 'delete' &&
            (missing.length > 0 || permissions.isPending || permissions.isError),
        }}
        cancelText="取消"
        cancelButtonProps={readOnly ? { style: { display: 'none' } } : undefined}
        onCancel={() => {
          if (!mutation.isPending) setDialog(null);
        }}
        onOk={() =>
          readOnly
            ? setDialog(null)
            : dialog?.action === 'delete'
              ? mutation.mutate()
              : form.submit()
        }
        destroyOnHidden
      >
        {readOnly && dialog?.target ? (
          <>
            <div className="role-permissions-summary">
              <div>
                <h3>{dialog.target.name}</h3>
                <span>{dialog.target.code}</span>
              </div>
              <Tag>{dialog.target.builtin ? '内置角色' : '自定义角色'}</Tag>
            </div>
            {dialog.target.description && (
              <p className="role-permissions-description">{dialog.target.description}</p>
            )}
            <div className="role-permissions-heading">
              <h3>已拥有权限</h3>
              <span>
                {dialog.target.permissions.length} 项权限 · {dialog.target.user_count} 名用户
              </span>
            </div>
            {permissions.isError && (
              <Alert
                type="warning"
                showIcon
                message="权限名称加载失败，暂时显示权限编码"
                action={<Button onClick={() => void permissions.refetch()}>重试</Button>}
              />
            )}
            <Spin spinning={permissions.isPending}>
              <div className="role-permissions-groups">
                {grantedGroups.map((group) => (
                  <section
                    className="role-permission-group"
                    key={group.name}
                    aria-label={group.name}
                  >
                    <div className="role-permission-group-heading">
                      <h4>{group.name}</h4>
                      <span>{group.items.length} 项</span>
                    </div>
                    <ul>
                      {group.items.map((id) => (
                        <li key={id}>
                          <Check size={16} aria-hidden="true" />
                          <span className="role-permission-copy">
                            <span>{labels.get(id) ?? id}</span>
                            {descriptions.get(id) && <small>{descriptions.get(id)}</small>}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
                {!grantedGroups.length && (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该角色尚未分配权限" />
                )}
              </div>
            </Spin>
          </>
        ) : dialog?.action === 'delete' ? (
          <Alert
            type="warning"
            showIcon
            message={`确认删除“${dialog.target!.name}”？仍有分配用户时会拒绝。`}
          />
        ) : (
          <Form
            form={form}
            layout="vertical"
            onFinish={() => mutation.mutate()}
            disabled={mutation.isPending}
          >
            <div className="role-basic-fields">
              <Form.Item
                label="角色编码"
                name="code"
                rules={[
                  {
                    required: true,
                    pattern: /^[a-z][a-z0-9_]{2,31}$/,
                    message: '3～32字符，小写字母开头，字母/数字/下划线',
                  },
                ]}
              >
                <Input disabled={!!dialog?.target} maxLength={32} />
              </Form.Item>
              <Form.Item
                label="角色名称"
                name="name"
                rules={[{ required: true, whitespace: true, max: 64 }]}
              >
                <Input maxLength={64} />
              </Form.Item>
              <Form.Item
                className="role-description-field"
                label="说明"
                name="description"
                rules={[{ max: 500 }]}
              >
                <Input.TextArea maxLength={500} rows={2} />
              </Form.Item>
            </div>
            {dialog?.target && !readOnly && (
              <Alert
                type="warning"
                showIcon
                message={`当前分配 ${dialog.target.user_count} 名用户；保存会撤销这些用户的旧会话。`}
                description={`移除：${
                  dialog.target.permissions
                    .filter((id) => !selected.includes(id))
                    .map((id) => labels.get(id) ?? id)
                    .join('、') || '无'
                }；新增：${
                  selected
                    .filter((id) => !dialog.target!.permissions.includes(id))
                    .map((id) => labels.get(id) ?? id)
                    .join('、') || '无'
                }`}
                style={{ marginBottom: 16 }}
              />
            )}
            {permissions.isPending ? (
              <div className="role-permissions-loading">
                <Spin aria-label="加载功能权限" />
              </div>
            ) : permissions.isError ? (
              <Alert type="error" showIcon message="功能权限加载失败，请刷新角色列表后重试" />
            ) : (
              <Form.Item
                className="role-permission-form-item"
                label={
                  <span className="role-permission-label">
                    <strong>功能权限</strong>
                    <span>
                      已选 {selected.length} / {permissions.data?.items.length ?? 0} 项
                    </span>
                  </span>
                }
                name="permissions"
              >
                <RolePermissionPicker
                  catalog={permissions.data?.items ?? []}
                  disabled={mutation.isPending}
                />
              </Form.Item>
            )}
            {missing.length > 0 && (
              <Alert
                type="error"
                showIcon
                message="请显式选择依赖权限"
                description={missing.join('；')}
                action={
                  <Button
                    disabled={mutation.isPending}
                    onClick={() =>
                      form.setFieldValue(
                        'permissions',
                        includeDependencies(selected, permissions.data?.items ?? []),
                      )
                    }
                  >
                    补选依赖权限
                  </Button>
                }
              />
            )}
          </Form>
        )}
        {mutation.error && (
          <Alert
            type="error"
            showIcon
            message={mutation.error.message}
            action={
              mutation.error instanceof APIError && mutation.error.code === 'VERSION_CONFLICT' ? (
                <Button
                  onClick={() => {
                    refresh();
                    setDialog(null);
                  }}
                >
                  重新读取
                </Button>
              ) : undefined
            }
            style={{ marginTop: 16 }}
          />
        )}
      </Modal>
    </section>
  );
}
