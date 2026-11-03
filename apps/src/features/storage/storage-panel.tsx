import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Modal, Select, Space, Spin, Tooltip } from 'antd';
import { ShieldCheck, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { APIError } from '../../api/error';
import type { CurrentUser } from '../../api/identity';
import { type StorageAsset } from '../../api/storage';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { ListSearch } from '../../shared/components/list-search';
import { AssetStatus, assetStatuses } from '../../shared/components/resource-status';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import './storage-asset-removal.css';

import { bytes, client } from './storage-shared';

import { Retention, Usage } from './storage-summary';

import { StorageDetails } from './storage-details';

export function StoragePanel({ user }: { user: CurrentUser }) {
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState({ q: '', status: '' });
  const [target, setTarget] = useState<string | null>(null);
  const navigate = useNavigate();
  const [removing, setRemoving] = useState<StorageAsset | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState('');
  const [removed, setRemoved] = useState(false);
  const cache = useQueryClient();
  useEffect(() => {
    const timer = setTimeout(() => setFilter((current) => ({ ...current, q: search.trim() })), 250);
    return () => clearTimeout(timer);
  }, [search]);
  const pagination = usePagePagination(JSON.stringify([user.id, filter]));
  const assets = useQuery({
    queryKey: ['storage', user.id, 'list', filter, pagination.page, pagination.pageSize],
    queryFn: ({ signal }) =>
      client.assets(user, { ...filter, page: pagination.page, limit: pagination.pageSize }, signal),
    retry: false,
    refetchInterval: 30_000,
  });
  useExpiredSession(assets.error);
  const rows = assets.isError ? [] : (assets.data?.items ?? []);
  async function removeAsset() {
    if (!removing || removeBusy) return;
    setRemoveBusy(true);
    setRemoveError('');
    try {
      await client.remove(user, removing.id);
      setRemoving(null);
      setRemoved(true);
      void cache.invalidateQueries({ queryKey: ['storage', user.id] });
    } catch (e) {
      if (e instanceof APIError && e.status === 401) expireSession(cache);
      setRemoveError(e instanceof Error ? e.message : '删除失败，请重试');
    } finally {
      setRemoveBusy(false);
    }
  }
  return (
    <section className="settings-section storage-panel" aria-label="工作数据">
      <div className="provider-page-heading">
        <div>
          <h2>数据资产</h2>
          <p>查看本人的工作空间数据盘与保留数据。</p>
        </div>
      </div>
      <div className="workspace-toolbar">
        <Select
          aria-label="筛选数据资产状态"
          value={filter.status}
          options={[
            { value: '', label: '全部状态' },
            ...Object.entries(assetStatuses).map(([value, label]) => ({ value, label })),
          ]}
          onChange={(status) => setFilter((current) => ({ ...current, status }))}
        />
        <ListSearch
          label="搜索数据资产"
          placeholder="搜索原工作空间名称"
          value={search}
          onChange={setSearch}
          onSubmit={() => setFilter((current) => ({ ...current, q: search.trim() }))}
          onRefresh={() => void assets.refetch()}
          loading={assets.isFetching}
          refreshLabel="刷新数据资产"
        />
      </div>
      {removed && (
        <Alert
          type="success"
          showIcon
          closable
          message="数据资产已删除"
          onClose={() => setRemoved(false)}
        />
      )}
      {assets.isError && (
        <Alert
          type="error"
          showIcon
          message={assets.error.message}
          action={<Button onClick={() => void assets.refetch()}>重新加载</Button>}
        />
      )}
      {assets.isPending ? (
        <Spin aria-label="加载数据资产" />
      ) : !rows.length ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            assets.isError
              ? '数据暂不可用'
              : filter.q || filter.status
                ? '没有符合条件的数据资产'
                : '暂无数据资产'
          }
        />
      ) : (
        <div className="storage-asset-list">
          {rows.map((asset) => (
            <article
              key={asset.id}
              className="storage-asset-row"
              aria-label={`数据资产 ${asset.workspace_name}`}
            >
              <div className="storage-asset-identity">
                <Button
                  type="link"
                  aria-label={`浏览 ${asset.workspace_name} 数据目录`}
                  onClick={() => navigate(`/app/storage/${asset.id}/browse`)}
                >
                  {asset.workspace_name}
                </Button>
                <code>{asset.id}</code>
                <Space wrap>
                  <AssetStatus status={asset.status} />
                </Space>
              </div>
              <div>
                <span className="storage-field-label">已分配</span>
                <strong>{bytes(asset.capacity_bytes)}</strong>
              </div>
              <div>
                <span className="storage-field-label">实际用量</span>
                <Usage asset={asset} />
              </div>
              <div>
                <span className="storage-field-label">数据保留</span>
                <Retention asset={asset} />
              </div>
              <div className="storage-asset-actions">
                <Button
                  aria-label={`查看${asset.workspace_name}的数据详情`}
                  onClick={() => setTarget(asset.id)}
                >
                  查看详情
                </Button>
                {asset.status === 'DELETED' &&
                  user.permissions.includes('instances:release-storage') && (
                    <Tooltip open={removing ? false : undefined} title="删除已回收资产">
                      <Button
                        danger
                        aria-label={`删除${asset.workspace_name}的数据资产`}
                        icon={<Trash2 size={16} aria-hidden="true" />}
                        onClick={() => {
                          setRemoveError('');
                          setRemoving(asset);
                        }}
                      />
                    </Tooltip>
                  )}
              </div>
            </article>
          ))}
        </div>
      )}
      <DataPagination
        label="数据资产"
        pagination={pagination}
        count={rows.length}
        total={assets.isError ? undefined : assets.data?.total}
        loading={assets.isFetching}
        failed={assets.isError}
      />
      {target && (
        <StorageDetails key={target} user={user} id={target} onClose={() => setTarget(null)} />
      )}
      <Modal
        className="storage-asset-removal"
        width={480}
        centered
        title={
          <span className="storage-asset-removal-title">
            <span className="storage-asset-removal-icon">
              <Trash2 size={20} aria-hidden="true" />
            </span>
            删除数据资产
          </span>
        }
        open={!!removing}
        okText={removeBusy ? '删除中…' : '删除'}
        cancelText="取消"
        okButtonProps={{ danger: true }}
        confirmLoading={removeBusy}
        closable={!removeBusy}
        maskClosable={!removeBusy}
        keyboard={!removeBusy}
        cancelButtonProps={{ disabled: removeBusy }}
        onCancel={() => {
          if (!removeBusy) setRemoving(null);
        }}
        onOk={() => void removeAsset()}
      >
        <p className="storage-asset-removal-description">确定从列表中删除以下已回收资产记录？</p>
        <dl className="storage-asset-removal-details">
          <div>
            <dt>资产名称</dt>
            <dd className="storage-asset-removal-name">{removing?.workspace_name}</dd>
          </div>
          <div>
            <dt>资产编号</dt>
            <dd>
              <code>{removing?.id}</code>
            </dd>
          </div>
        </dl>
        <p className="storage-asset-removal-backup">
          <ShieldCheck size={16} aria-hidden="true" />
          <span>已有备份不受影响</span>
        </p>
        {removeError && <Alert role="alert" type="error" showIcon message={removeError} />}
      </Modal>
    </section>
  );
}

export { StorageBrowsePage } from './storage-browse-page';
