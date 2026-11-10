import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Dropdown, Empty, Segmented, Spin, Switch, Tag } from 'antd';
import { Ellipsis, ExternalLink, Plus } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { type Connector, type ConnectorList, type ConnectorPreset } from '../../api/connectors';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { DeleteConfirmation } from '../../shared/components/delete-confirmation';
import { ListSearch } from '../../shared/components/list-search';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { ConnectorCatalogPicker } from './connector-catalog';
import { ConnectorDetails } from './connector-details';
import { ConnectorIcon } from './connector-logo';
import { authorizeConnector } from './connector-oauth';
import { client, date, errorText, StatusTag } from './connector-shared';
import './connectors.css';

import { ConnectorEditor } from './connector-editor';

export function ConnectorsPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const scope = ['connectors', user.id];
  const list = useQuery({
    queryKey: scope,
    queryFn: ({ signal }) => client.request<ConnectorList>(user, '', undefined, 'GET', signal),
    retry: false,
    refetchInterval: 5000,
  });
  useExpiredSession(list.error);
  const manage = hasPermission(user, 'connectors:manage');
  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [selected, setSelected] = useState('');
  const [editor, setEditor] = useState<Connector | 'new' | null>(null);
  const [duplicate, setDuplicate] = useState<Connector | null>(null);
  const [deleting, setDeleting] = useState<Connector | null>(null);
  const [confirmedName, setConfirmedName] = useState('');
  const [catalogOpen, setCatalogOpen] = useState(false);
  const [preset, setPreset] = useState<ConnectorPreset | null>(null);
  const [filter, setFilter] = useState('全部');
  const page = usePagePagination(JSON.stringify([search, filter]));
  const items = (list.isError ? [] : (list.data?.items ?? [])).filter(
    (v) =>
      (filter === '全部' || v.enabled === (filter === '已启用')) &&
      (v.name + ' ' + v.description)
        .toLocaleLowerCase()
        .includes(search.trim().toLocaleLowerCase()),
  );
  const visible = items.slice((page.page - 1) * page.pageSize, page.page * page.pageSize);
  const connection = list.data?.items.find((v) => v.id === selected);
  function edit(
    value: Connector | 'new',
    copy: Connector | null = null,
    template: ConnectorPreset | null = null,
  ) {
    const connection = value === 'new' ? copy : value;
    const catalog =
      connection?.catalog_id ??
      list.data?.endpoints.find((e) => e.id === connection?.endpoint_id)?.catalog_id;
    setPreset(template ?? list.data?.catalog?.entries.find((v) => v.id === catalog) ?? null);
    setDuplicate(copy);
    setEditor(value);
  }
  async function refresh() {
    await cache.invalidateQueries({ queryKey: scope });
  }
  async function run(id: string, task: () => Promise<void>) {
    setBusy(id);
    setError('');
    try {
      await task();
      await refresh();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorText(cause));
    } finally {
      setBusy('');
    }
  }
  return (
    <section className="instances-section connectors-panel" aria-label="连接器管理">
      <div className="provider-page-heading workspace-actions">
        <h2>连接器</h2>
        {manage && (
          <Button
            type="primary"
            icon={<Plus size={16} aria-hidden="true" />}
            disabled={!list.data || list.isError}
            onClick={() => setCatalogOpen(true)}
          >
            添加连接器
          </Button>
        )}
      </div>
      <OAuthFeedback />
      <div className="workspace-toolbar">
        <Segmented
          aria-label="连接器状态筛选"
          options={['全部', '已启用', '已停用']}
          value={filter}
          onChange={(value) => setFilter(String(value))}
        />
        <ListSearch
          value={search}
          onChange={setSearch}
          onRefresh={() => void list.refetch()}
          loading={list.isFetching}
          placeholder="搜索名称或说明"
          label="搜索连接器"
          refreshLabel="刷新连接器列表"
        />
      </div>
      {(error || list.isError) && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={error || errorText(list.error)}
          action={<Button onClick={() => void list.refetch()}>重试</Button>}
        />
      )}
      <Spin spinning={list.isPending}>
        <div className="workspace-list" aria-label="连接器列表">
          {visible.length ? (
            visible.map((v) => (
              <article className="workspace-record" key={v.id} aria-label={`连接器 ${v.name}`}>
                <ConnectorIcon
                  catalogID={
                    v.catalog_id ||
                    list.data?.endpoints.find((e) => e.id === v.endpoint_id)?.catalog_id
                  }
                />
                <div className="workspace-record-content">
                  <div className="workspace-record-heading">
                    <h3>
                      <Button type="link" onClick={() => setSelected(v.id)}>
                        {v.name}
                      </Button>
                    </h3>
                    {v.enabled ? <StatusTag status={v.status} /> : <Tag>已停用</Tag>}
                  </div>
                  {v.description && <p className="workbench-record-description">{v.description}</p>}
                  <div className="workspace-record-meta">
                    <span>
                      {v.endpoint_id === 'personal'
                        ? v.url
                        : (list.data?.endpoints.find((e) => e.id === v.endpoint_id)?.name ??
                          '服务配置已失效')}
                    </span>
                    <span>
                      工具已启用 {v.tools.filter((t) => t.enabled).length} / {v.tools.length}
                    </span>
                    <span>最近验证：{date(v.verified_at)}</span>
                  </div>
                  {v.enabled && v.server_info.verification_error && (
                    <p className="connector-note">{v.server_info.verification_error}</p>
                  )}
                </div>
                <div className="workbench-record-actions">
                  <Button className="workspace-details" onClick={() => setSelected(v.id)}>
                    工具与记录
                  </Button>
                  {manage && (
                    <>
                      <Switch
                        aria-label={`启用服务 ${v.name}`}
                        checked={v.enabled}
                        loading={busy === v.id}
                        disabled={!!busy}
                        onChange={(enabled) =>
                          void run(v.id, async () => {
                            await client.request(user, `/${v.id}/enabled`, {
                              expected_version: v.version,
                              enabled,
                            });
                          })
                        }
                      />
                      <Button
                        disabled={!v.enabled || !!busy}
                        loading={busy === v.id}
                        onClick={() =>
                          void run(v.id, async () => {
                            if (v.auth_type === 'oauth2' && v.status === 'AUTH_REQUIRED') {
                              await authorizeConnector(user, v);
                            } else {
                              await client.request(user, `/${v.id}/sync`, {
                                expected_version: v.version,
                              });
                            }
                          })
                        }
                      >
                        {v.auth_type === 'oauth2' && v.status === 'AUTH_REQUIRED'
                          ? '授权连接'
                          : '验证'}
                      </Button>
                      <Dropdown
                        trigger={['click']}
                        menu={{
                          items: [
                            ...(v.auth_type === 'oauth2'
                              ? [{ key: 'oauth', label: '重新授权', disabled: !v.enabled }]
                              : []),
                            { key: 'settings', label: '设置' },
                            { key: 'copy', label: '复制' },
                            { key: 'delete', label: '删除', danger: true },
                          ],
                          onClick: ({ key }) => {
                            if (key === 'oauth') {
                              void run(v.id, () => authorizeConnector(user, v));
                            } else if (key === 'settings') {
                              edit(v);
                            } else if (key === 'copy') {
                              edit('new', v);
                            } else if (key === 'delete') {
                              setConfirmedName('');
                              setDeleting(v);
                            }
                          },
                        }}
                      >
                        <Button
                          aria-label={`${v.name} 更多操作`}
                          disabled={!!busy}
                          icon={<Ellipsis size={17} aria-hidden="true" />}
                        />
                      </Dropdown>
                    </>
                  )}
                </div>
              </article>
            ))
          ) : (
            <div className="workspace-list-empty">
              <ExternalLink size={28} aria-hidden="true" />
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  list.isError
                    ? '未能获取连接器'
                    : search || filter !== '全部'
                      ? '没有符合条件的连接器'
                      : '暂无连接器'
                }
              />
            </div>
          )}
        </div>
      </Spin>
      <DataPagination
        label="连接器"
        pagination={page}
        count={visible.length}
        total={items.length}
        loading={list.isFetching}
        failed={list.isError}
      />
      {catalogOpen && list.data && (
        <ConnectorCatalogPicker
          catalog={
            list.data.catalog ?? {
              version: 1,
              source_url: 'https://github.com/TencentCloud/Octop/blob/main/docs/connectors.md',
              entries: [],
            }
          }
          onClose={() => setCatalogOpen(false)}
          onSelect={(template) => {
            setCatalogOpen(false);
            edit('new', null, template);
          }}
        />
      )}
      {editor && (
        <ConnectorEditor
          key={(editor === 'new' ? 'new' : editor.id) + ':' + (preset?.id ?? 'custom')}
          user={user}
          value={editor}
          duplicate={duplicate}
          preset={preset}
          endpoints={list.data?.endpoints ?? []}
          onBack={
            editor === 'new'
              ? () => {
                  setEditor(null);
                  setCatalogOpen(true);
                }
              : undefined
          }
          onClose={() => {
            setEditor(null);
            void refresh();
          }}
          onSaved={async () => {
            setEditor(null);
            await refresh();
          }}
        />
      )}
      {connection && (
        <ConnectorDetails
          user={user}
          connection={connection}
          onClose={() => setSelected('')}
          onRefresh={refresh}
        />
      )}
      <DeleteConfirmation
        open={!!deleting}
        title="删除连接器？"
        name={deleting?.name ?? ''}
        label="确认连接器名称"
        value={confirmedName}
        onChange={setConfirmedName}
        confirmText="删除连接器"
        busy={!!busy}
        error={error}
        onCancel={() => setDeleting(null)}
        onConfirm={() =>
          deleting &&
          void run('delete', async () => {
            await client.request(
              user,
              `/${deleting.id}`,
              { expected_version: deleting.version, confirmed_name: confirmedName },
              'DELETE',
            );
            setDeleting(null);
          })
        }
      >
        <p>
          移除此连接及工具授权，<strong>外部系统的数据将保留</strong>。
        </p>
        <p>在途调用可能已经执行。</p>
      </DeleteConfirmation>
    </section>
  );
}

function OAuthFeedback() {
  const [params, setParams] = useSearchParams();
  const [outcome, setOutcome] = useState('');
  useEffect(() => {
    const value = params.get('connector_oauth');
    if (value) {
      setOutcome(value);
      const next = new URLSearchParams(params);
      next.delete('connector_oauth');
      setParams(next, { replace: true });
    }
  }, [params, setParams]);
  if (!outcome) return null;
  const messages: Record<string, string> = {
    ready: '授权成功，连接已就绪。请选择需要启用的工具。',
    verify_failed: '授权已保存，连接验证未通过，请查看连接状态并重试验证。',
    cancelled: '已取消授权，可随时重新连接。',
    failed: '授权未完成或请求已失效，请重新授权。',
  };
  return (
    <Alert
      role="status"
      showIcon
      closable
      onClose={() => setOutcome('')}
      type={outcome === 'ready' ? 'success' : outcome === 'cancelled' ? 'info' : 'warning'}
      message={messages[outcome] ?? messages.failed}
    />
  );
}
