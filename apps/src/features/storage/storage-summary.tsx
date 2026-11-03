import { Collapse, Descriptions, Tag } from 'antd';
import { type StorageAsset, type StorageBackup } from '../../api/storage';

import { backupStatuses, blockers, bytes, time } from './storage-shared';

export function Usage({ asset, detail = false }: { asset: StorageAsset; detail?: boolean }) {
  const usage = asset.usage;
  const known =
    ['CURRENT', 'STALE'].includes(usage.status) &&
    usage.used_bytes !== null &&
    usage.available_bytes !== null;
  return (
    <div className="storage-usage">
      <strong>
        {known
          ? `${bytes(usage.used_bytes!)} 已用`
          : ({ UNSAMPLED: '未采集', UNAVAILABLE: '暂不可用' }[usage.status] ?? '待核对')}
      </strong>
      {known && (
        <span>
          {bytes(usage.available_bytes!)} 可用{usage.status === 'STALE' ? ' · 待刷新' : ''}
        </span>
      )}
      {detail && usage.sampled_at && (
        <span>
          更新于 {time(usage.sampled_at)}
          {usage.status === 'UNAVAILABLE' ? '（最近记录）' : ''}
        </span>
      )}
    </div>
  );
}

export function BackupSummary({ backup }: { backup: StorageBackup | null }) {
  return backup ? (
    <div className="storage-backup-summary">
      <span>{backupStatuses[backup.status] ?? '待核对'}</span>
      <small>{time(backup.completed_at ?? backup.created_at)}</small>
    </div>
  ) : (
    <span className="storage-muted">暂无备份记录</span>
  );
}

export function Retention({ asset }: { asset: StorageAsset }) {
  return (
    <div className="storage-retention">
      {asset.legal_hold && <Tag>法律保留中</Tag>}
      {asset.status === 'DELETED' ? (
        <span>已完成回收</span>
      ) : asset.retention_expired ? (
        <span>保留已到期，尚未回收</span>
      ) : asset.retention_until ? (
        <span>保留至 {time(asset.retention_until)}</span>
      ) : (
        <span>{asset.deleted_at ? '保留期限待核对' : '随工作空间保留'}</span>
      )}
    </div>
  );
}

export function MaintenanceGuide({ asset }: { asset: StorageAsset }) {
  return (
    <Collapse
      ghost
      items={[
        {
          key: 'maintenance',
          label: '维护条件',
          children: (
            <>
              <Descriptions
                column={1}
                size="small"
                items={(['backup', 'restore', 'reclaim'] as const).map((action) => ({
                  key: action,
                  label: { backup: '备份', restore: '恢复', reclaim: '到期回收' }[action],
                  children: asset.maintenance[`${action}_blockers`].length
                    ? asset.maintenance[`${action}_blockers`]
                        .map((code) => blockers[code] ?? '请联系管理员核对')
                        .join('；')
                    : '可联系管理员处理',
                }))}
              />
              <p className="storage-muted">
                恢复须使用本人另一工作空间的同容量备份；先保留目标原数据，恢复后不自动启动。
              </p>
            </>
          ),
        },
      ]}
    />
  );
}
