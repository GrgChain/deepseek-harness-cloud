import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Collapse, Descriptions, Drawer, Spin } from 'antd';
import { Link } from 'react-router-dom';
import type { CurrentUser } from '../../api/identity';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { assetProcessingStages, AssetStatus } from '../../shared/components/resource-status';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

import { bytes, client, time } from './storage-shared';

import { BackupSummary, MaintenanceGuide, Retention, Usage } from './storage-summary';

import { ReleaseStorage } from './release-storage';

export function StorageDetails({
  user,
  id,
  onClose,
}: {
  user: CurrentUser;
  id: string;
  onClose: () => void;
}) {
  const pagination = usePagePagination(id);
  const details = useQuery({
    queryKey: ['storage', user.id, 'asset', id],
    queryFn: ({ signal }) => client.details(user, id, signal),
    retry: false,
    refetchInterval: 30_000,
  });
  const backups = useQuery({
    queryKey: ['storage', user.id, 'backups', id, pagination.page, pagination.pageSize],
    queryFn: ({ signal }) => client.backups(id, pagination.page, pagination.pageSize, signal),
    retry: false,
    refetchInterval: 30_000,
  });
  useExpiredSession(details.error ?? backups.error);
  const asset = details.isError ? undefined : details.data;
  return (
    <Drawer
      title="数据资产详情"
      width={640}
      open
      onClose={onClose}
      className="storage-details"
      extra={
        <Button
          loading={details.isFetching || backups.isFetching}
          onClick={() => {
            void details.refetch();
            void backups.refetch();
          }}
        >
          刷新详情
        </Button>
      }
    >
      {details.isPending ? (
        <Spin aria-label="加载数据资产" />
      ) : details.isError ? (
        <Alert type="error" showIcon message={details.error.message} />
      ) : (
        asset && (
          <>
            <h2>{asset.workspace_name}</h2>
            <AssetStatus status={asset.status} />
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: 'workspace',
                  label: '原工作空间',
                  children: (
                    <Link to={'/app/instances/' + asset.instance_id}>{asset.workspace_name}</Link>
                  ),
                },
                { key: 'capacity', label: '已分配容量', children: bytes(asset.capacity_bytes) },
                { key: 'usage', label: '实际数据用量', children: <Usage asset={asset} detail /> },
                { key: 'retention', label: '数据保留', children: <Retention asset={asset} /> },
              ]}
            />
            {asset.usage.status === 'CURRENT' && asset.usage.available_bytes === 0 && (
              <Alert type="warning" showIcon message="工作空间数据盘空间不足，请清理本人文件" />
            )}
            <Collapse
              ghost
              items={[
                {
                  key: 'metadata',
                  label: '更多信息',
                  children: (
                    <Descriptions
                      column={1}
                      size="small"
                      items={[
                        ...(Object.hasOwn(assetProcessingStages, asset.status)
                          ? [
                              {
                                key: 'stage',
                                label: '处理阶段',
                                children: assetProcessingStages[asset.status],
                              },
                            ]
                          : []),
                        { key: 'asset', label: '资产标识', children: <code>{asset.id}</code> },
                        {
                          key: 'instance',
                          label: '工作空间标识',
                          children: <code>{asset.instance_id}</code>,
                        },
                        { key: 'deleted', label: '删除时间', children: time(asset.deleted_at) },
                      ]}
                    />
                  ),
                },
              ]}
            />
            <ReleaseStorage asset={asset} user={user} />
            <section className="storage-detail-section">
              <h3>备份记录</h3>
              {asset.latest_backup?.status !== 'SUCCEEDED' && asset.latest_verified_backup && (
                <p>最近有效备份：{time(asset.latest_verified_backup.completed_at)} · 已校验</p>
              )}
              {backups.isError ? (
                <Alert type="error" showIcon message={backups.error.message} />
              ) : backups.isPending ? (
                <Spin aria-label="加载备份记录" />
              ) : !backups.data?.items.length ? (
                <p className="storage-muted">暂无备份记录</p>
              ) : (
                <div className="storage-backup-history">
                  {backups.data.items.map((backup) => (
                    <article key={backup.id} aria-label="备份记录">
                      <BackupSummary backup={backup} />
                      <code>{backup.id}</code>
                      <span>
                        校验：
                        {backup.verified_at ? `通过 · ${time(backup.verified_at)}` : '尚未确认'}
                      </span>
                      <span>
                        有效期限：
                        {backup.expires_at
                          ? time(backup.expires_at)
                          : backup.retention_policy === 'LAST_7_SUCCESSFUL_DATES'
                            ? '按最近 7 个成功日期保留'
                            : '由管理员保管'}
                      </span>
                    </article>
                  ))}
                </div>
              )}
              {!backups.isError &&
                ((backups.data?.total ?? 0) > pagination.pageSize || pagination.page > 1) && (
                  <DataPagination
                    label="备份记录"
                    pagination={pagination}
                    count={backups.data?.items.length ?? 0}
                    total={backups.data?.total}
                    loading={backups.isFetching}
                    failed={backups.isError}
                  />
                )}
            </section>
            <MaintenanceGuide asset={asset} />
          </>
        )
      )}
    </Drawer>
  );
}
