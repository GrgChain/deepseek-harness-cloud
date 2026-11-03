import { StorageClient } from '../../api/storage';

export const client = new StorageClient();

export const backupStatuses: Record<string, string> = {
  RUNNING: '进行中',
  SUCCEEDED: '备份成功',
  FAILED: '备份失败',
  UNCERTAIN: '结果待确认',
  PRUNE_PENDING: '清理待确认',
  PRUNED: '已清理',
  EXPIRED: '已过期',
};

export const blockers: Record<string, string> = {
  OPERATION_PENDING: '有未完成或待确认的工作空间操作',
  BINDING_UNCONFIRMED: '原数据盘绑定尚未确认',
  QUOTA_UNCONFIRMED: '存储额度账本待核对',
  COMPUTE_NOT_STOPPED: '须先停止工作空间并确认完成',
  BACKUP_PENDING: '须先核对上次备份结果',
  TARGET_NOT_STOPPED: '目标须为已停止且可用的工作空间',
  LEGAL_HOLD: '受法律保留保护',
  NOT_SOFT_DELETED: '须先软删除工作空间并确认完成',
  RETENTION_ACTIVE: '尚未到保留截止时间',
  MINIMUM_7_DAYS: '软删除后须至少保留 7 天',
  ALREADY_RECLAIMED: '数据盘已回收',
  RUNTIME_BINDING_UNAVAILABLE: '目标运行绑定或凭据不可用',
};

export const time = (value: string | null | undefined) =>
  value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';

export const bytes = (value: number) =>
  `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value / 2 ** 30)} GiB`;
