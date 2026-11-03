import { ContextClient, type ContextRecord } from '../../api/context';

export const client = new ContextClient();

export const status: Record<string, { label: string; color: string }> = {
  READY: { label: '已就绪', color: 'green' },
  STALE: { label: '旧版可用', color: 'orange' },
  UNVERIFIED: { label: '待验证', color: 'default' },
  FAILED: { label: '验证失败', color: 'red' },
  INDEXING: { label: '索引中', color: 'blue' },
  ERROR: { label: '索引失败', color: 'red' },
  PENDING_DELETE: { label: '删除待确认', color: 'orange' },
};

export const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });

export const backendNames: Record<string, string> = {
  mem0: 'Mem0',
  openviking: 'OpenViking',
  graphiti: 'Graphiti',
  milvus: 'Milvus',
  weaviate: 'Weaviate',
  qdrant: 'Qdrant',
};

export const memoryCustomBackends = ['graphiti', 'mem0', 'openviking'];

export interface SessionMemory {
  id: string;
  title: string;
  category: string;
  updated_at?: string;
  content?: string;
}

export type ManagedRecord = ContextRecord & {
  memory_source?: 'automatic' | 'manual';
  category?: string;
};

export const memoryCategories: Record<string, string> = {
  preferences: '偏好',
  entities: '事实与实体',
  events: '事件',
  experiences: '经验',
  profile: '用户画像',
  patterns: '模式',
  cases: '案例',
};

export const memoryCategory = (value?: string) =>
  value ? (memoryCategories[value] ?? value) : '记忆';

export const memorySource = (row: ManagedRecord) =>
  row.memory_source === 'automatic' ? '自动生成' : '手动添加';
