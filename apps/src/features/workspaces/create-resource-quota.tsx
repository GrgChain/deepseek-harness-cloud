import type { ResourceQuota } from '../../api/settings';

const resources = {
  max_instances: { name: '实例', unit: '个实例', scale: 1 },
  cpu_millicores: { name: 'CPU', unit: '核', scale: 1000 },
  memory_bytes: { name: '内存', unit: 'GiB', scale: 2 ** 30 },
  storage_bytes: { name: '存储', unit: 'GiB', scale: 2 ** 30 },
} as const;
const format = (value: number, scale: number) => new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value / scale);

export function ResourceRemaining({ quota, resource }: { quota?: ResourceQuota; resource: keyof typeof resources }) {
  if (!quota) return null;
  const { name, unit, scale } = resources[resource];
  const insufficient = quota.available[resource] < quota.required[resource];
  return <span className={`create-resource-remaining${insufficient ? ' create-quota-insufficient' : ''}`} aria-label={`${name}剩余额度`}>
    剩余 {format(quota.available[resource], scale)} {unit}{insufficient ? '（不足）' : ''}
  </span>;
}
