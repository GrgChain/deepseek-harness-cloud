import type { UserQuotaLimits } from '../../api/management';

const GiB = 1024 ** 3;
export const quotaDimensions: { key: keyof UserQuotaLimits; label: string; unit: string; scale: number; step: number }[] = [
  { key: 'max_instances', label: '实例数量', unit: '个', scale: 1, step: 1 },
  { key: 'cpu_millicores', label: 'CPU', unit: 'vCPU', scale: 1000, step: 0.25 },
  { key: 'memory_bytes', label: '内存', unit: 'GiB', scale: GiB, step: 0.25 },
  { key: 'storage_bytes', label: '持久存储', unit: 'GiB', scale: GiB, step: 1 },
];
export const formatQuotaNumber = (value: number) => value.toLocaleString('zh-CN', { maximumFractionDigits: 6 });
