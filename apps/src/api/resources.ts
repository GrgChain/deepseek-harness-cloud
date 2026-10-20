import type { CurrentUser } from './identity';
import { APIError } from './error';
import { type Health, type ResourceSpec } from './settings';

export interface Instance {
 id: string; owner_id: string; name: string; observed_state: string; desired_state: string; version: number;
 config_snapshot: { spec: ResourceSpec; model: string; model_configuration_version: number; provider?: string }; health: Record<'application' | 'storage' | 'configuration' | 'access' | 'model', Health>;
 model_display_name?: string; model_provider_name?: string;
 ready: boolean; can_enter: boolean; allowed_actions: string[]; active_operation_id: string | null; latest_operation_id?: string | null; access_host: string | null;
 created_at: string; updated_at: string; retention_until: string | null;
 storage: { capacity_bytes: number; status: string } | null;
}
export interface Operation { id: string; instance_id: string; type: string; status: string; stage: string; error: { code: string; message: string; request_id: string } | null; retry_allowed: boolean; updated_at: string }
export interface OperationLog { id: string; actor_id: string | null; actor_name: string; owner_id: string | null; action: string; outcome: string; timestamp: string; resource_id: string | null; resource_name: string; resource_type: string; trace_id: string; details: Record<string, unknown> }
export interface OperationLogFilters { action?: string; outcome?: string; resource_id?: string; from?: string; to?: string }
export interface Page<T> { items: T[]; total?: number; next_cursor: string | null }
export interface InstanceFilters { state?: string; q?: string; limit?: number; page?: number }

export class ResourcesClient {
 constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
 private async read<T>(path: string, signal?: AbortSignal): Promise<T> {
  const timeout = AbortSignal.timeout(10_000);
  const response = await this.fetcher(path, { credentials: 'same-origin', cache: 'no-store', signal: signal ? AbortSignal.any([signal, timeout]) : timeout });
  if (!response.ok) { const problem: { code?: unknown } = await response.json().catch(() => ({})); throw new APIError(response.status, typeof problem.code === 'string' ? problem.code : 'DEPENDENCY_UNAVAILABLE'); }
  return response.json() as Promise<T>;
 }
 private instance(value: Instance, user: CurrentUser) {
  if (!value || value.owner_id !== user.id || typeof value.id !== 'string' || typeof value.name !== 'string' || typeof value.ready !== 'boolean' || typeof value.can_enter !== 'boolean' || !value.config_snapshot?.spec || !value.health || !['application','storage','configuration','access','model'].every(dimension => dimension in value.health) || !Array.isArray(value.allowed_actions)) throw new Error('实例数据无效，请重新加载');
  return value;
 }
 private page<T>(value: Page<T>): Page<T> {
  if (!value || !Array.isArray(value.items) || (value.next_cursor !== null && typeof value.next_cursor !== 'string')) throw new Error('分页数据无效，请重新加载');
  return value;
 }
 async instances(user: CurrentUser, filters: InstanceFilters = {}, cursor?: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: String(filters.limit ?? 20) });
  if (filters.state) query.set('state', filters.state);
  if (filters.q) query.set('q', filters.q);
  if (cursor) query.set('cursor', cursor);
  if (filters.page !== undefined) query.set('page', String(filters.page));
  const result = this.page(await this.read<Page<Instance>>('/v1/instances?' + query, signal));
  result.items.forEach(item => this.instance(item, user)); return result;
 }
 async details(user: CurrentUser, id: string, signal?: AbortSignal) { return this.instance(await this.read<Instance>('/v1/instances/' + encodeURIComponent(id), signal), user); }
 operation(id: string, signal?: AbortSignal) { return this.read<Operation>('/v1/operations/' + encodeURIComponent(id), signal); }
 async operationLogs(cursor?: string, signal?: AbortSignal, limit = 20, page?: number, filters: OperationLogFilters = {}) { const query = new URLSearchParams({ limit: String(limit) }); if (cursor) query.set('cursor', cursor); if (page !== undefined) query.set('page', String(page)); for (const [key, value] of Object.entries(filters)) if (value) query.set(key, value); return this.page(await this.read<Page<OperationLog>>('/v1/audit-events?' + query, signal)); }
}
