import { useEffect, useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Select, Space, Table, Tag, Tooltip } from 'antd';
import type { CurrentUser } from '../../api/identity';
import { ManagementClient, type ManagedUser } from '../../api/management';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { useTransientNotice } from '../../shared/hooks/use-transient-notice';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { UserQuotaDialog } from './user-quota-dialog';
import { quotaDimensions, formatQuotaNumber } from './quota-format';
import { ListSearch } from '../../shared/components/list-search';

const client = new ManagementClient();
const statuses: Record<string, string> = { ACTIVE: '已启用', DISABLED: '已禁用', PENDING_IDENTITY: '身份待确认', ARCHIVED: '已删除' };

export function ResourceQuotasPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const [filter, setFilter] = useState({ q: '', status: '' });
  const [search, setSearch] = useState('');
  useEffect(() => { const timer = setTimeout(() => { const q = search.trim(); setFilter(current => current.q === q ? current : { ...current, q }); }, 250); return () => clearTimeout(timer); }, [search]);
  const pagination = usePagePagination(JSON.stringify([user.id, filter]));
  const [target, setTarget] = useState<ManagedUser | null>(null);
  const [notice, setNotice] = useTransientNotice();
  const users = useQuery({
    queryKey: ['management', user.id, 'quota-users', filter, pagination.page, pagination.pageSize],
    queryFn: ({ signal }) => client.users(user, { ...filter, page: pagination.page, limit: pagination.pageSize }, signal), retry: false,
  });
  const rows = users.data?.items ?? [];
  // Fetch only this page's users and share each result with the quota editor.
  const queries = useQueries({ queries: rows.map(owner => ({
    queryKey: ['management', user.id, 'user-quota', owner.id],
    queryFn: ({ signal }: { signal: AbortSignal }) => client.userQuota(user, owner.id, signal),
    staleTime: 15_000, retry: false, refetchOnWindowFocus: false,
  })) });
  const quotas = new Map(rows.map((owner, index) => [owner.id, queries[index]]));
  const loading = users.isFetching || queries.some(query => query.isFetching);
  const quotaError = queries.find(query => query.isError)?.error;
  const error = users.error ?? quotaError;
  useExpiredSession(error);
  const refresh = () => {
    void users.refetch();
    void cache.invalidateQueries({ queryKey: ['management', user.id, 'user-quota'] });
  };
  const userQuotaContent = <>
    <div className="workspace-toolbar users-toolbar">
      <div className="users-filters"><Select aria-label="筛选配额用户状态" value={filter.status} options={[{ label: '未删除用户', value: '' }, ...Object.entries(statuses).map(([value, label]) => ({ value, label }))]} onChange={status => setFilter(current => ({ ...current, status }))} /></div>
      <ListSearch label="搜索配额用户" placeholder="搜索显示名或邮箱" value={search} onChange={setSearch} onSubmit={() => setFilter(current => ({ ...current, q: search.trim() }))} onRefresh={refresh} loading={loading} refreshLabel="刷新配额列表" />
    </div>
    {notice && <Alert type="success" showIcon closable onClose={() => setNotice(null)} message={notice} style={{ marginBottom: 16 }} />}
    {users.error && <Alert type="error" showIcon message={users.error.message} style={{ marginBottom: 16 }} />}
    {quotaError && <Alert type="warning" showIcon message="部分用户的配额读取失败，可刷新重试或打开详情重新读取。" style={{ marginBottom: 16 }} />}
    <Table className="data-table users-table" size="middle" rowKey="id" tableLayout="fixed" dataSource={rows} pagination={false} loading={users.isPending} scroll={{ x: 1230 }} locale={{ emptyText: users.isError ? '未能获取用户配额' : filter.q || filter.status ? '没有符合条件的用户' : '暂无用户' }} columns={[
      { title: '用户 / 联系邮箱', key: 'user', width: 280, render: (_, owner: ManagedUser) => <div className="user-profile"><Tooltip title={owner.display_name || owner.id}><Button type="link" onClick={() => { setNotice(null); setTarget(owner); }}>{owner.display_name || owner.id.slice(0, 8)}</Button></Tooltip><span title={owner.email}>{owner.email}</span></div> },
      { title: '状态', key: 'status', width: 110, render: (_, owner: ManagedUser) => <Tag color={owner.status === 'ACTIVE' ? 'green' : 'default'}>{statuses[owner.status]}</Tag> },
      { title: '配额来源', key: 'source', width: 130, render: (_, owner: ManagedUser) => {
        const query = quotas.get(owner.id);
        return query?.isError ? <span className="quota-read-error">读取失败</span> : !query?.data ? <span className="quota-table-muted">正在读取…</span> : <Tag color={query.data.configured ? 'blue' : 'default'}>{query.data.configured ? '单独分配' : '继承组织默认'}</Tag>;
      } },
      ...quotaDimensions.map(dimension => ({ title: `${dimension.label}（${dimension.unit}）`, key: dimension.key, width: 142, render: (_: unknown, owner: ManagedUser) => {
        const query = quotas.get(owner.id);
        if (!query?.data || query.isError) return <span className="quota-table-muted">—</span>;
        const used = query.data.usage[dimension.key] / dimension.scale;
        const limit = query.data.limits[dimension.key] / dimension.scale;
        return <span className={`quota-table-value${used > limit ? ' quota-read-error' : ''}`} aria-label={`${dimension.label}已占用 ${formatQuotaNumber(used)} / 上限 ${formatQuotaNumber(limit)} ${dimension.unit}`}><strong>{formatQuotaNumber(used)}</strong><span> / {formatQuotaNumber(limit)}</span></span>;
      } })),
      { title: '操作', key: 'actions', width: 142, fixed: 'right' as const, render: (_, owner: ManagedUser) => <Space><Button onClick={() => { setNotice(null); setTarget(owner); }}>{user.permissions.includes('users:manage') && owner.status !== 'ARCHIVED' ? '调整配额' : '查看配额'}</Button></Space> },
    ]} />
    <DataPagination label="资源配额" pagination={pagination} count={rows.length} total={users.data?.total} loading={users.isFetching} failed={users.isError} />
    {target && <UserQuotaDialog user={user} target={target} onClose={() => setTarget(null)} onSaved={() => setNotice('资源配额已保存，将用于后续资源申请')} />}
  </>;
  return <section className="settings-section resource-quotas-section" aria-label="资源配额">
    <div className="provider-page-heading"><h2>用户资源配额</h2></div>
    {userQuotaContent}
  </section>;
}
