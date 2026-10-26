import type { CurrentUser } from './identity';
import { APIError } from './error';

export interface Permission { id: string; name: string; description?: string; assignable: boolean; dependencies: string[] }
export interface ManagedRole { id: string; code: string; name: string; description: string; builtin: boolean; version: number; permissions: string[]; user_count: number; created_at: string; updated_at: string }
export type RoleOption = Pick<ManagedRole, 'id' | 'code' | 'name'>;
export interface Provisioning { id: string; status: 'PENDING' | 'SUCCEEDED'; reason_code: string }
export interface ManagedUser { id: string; display_name: string; email: string; login_email: string; identity_source: 'oidc' | 'local'; issuer: string; subject: string; workspace_owner_id: string; status: 'PENDING_IDENTITY' | 'ACTIVE' | 'DISABLED' | 'ARCHIVED'; role_id: string; role_code: string; role_name: string; version: number; created_at: string; updated_at: string; last_login_at: string | null; provisioning: Provisioning | null; password_reset: Provisioning | null }
export interface UserQuotaLimits { max_instances: number; cpu_millicores: number; memory_bytes: number; storage_bytes: number }
export interface UserQuota { owner_id: string; version: number; configured: boolean; limits: UserQuotaLimits; usage: UserQuotaLimits; organization_limits: UserQuotaLimits }
export interface ManagementPage<T> { items: T[]; total?: number; next_cursor: string | null; local_accounts: boolean }
export interface RoleInput { code: string; name: string; description: string; permissions: string[]; expected_version: number }
export interface UserCreateInput { display_name: string; email: string; identity_source: 'oidc' | 'local'; subject?: string; role_id: string; initial_password?: string }
export type UserAction = 'role' | 'disable' | 'enable' | 'archive' | 'retry-identity' | 'reset-password' | 'retry-password-reset';
export interface UserActionInput { expected_version: number; role_id?: string; password?: string }
export interface ListFilter { q?: string; status?: string; role_id?: string; cursor?: string; page?: number; limit?: number }

const messages: Record<string, string> = {
  IDENTITY_INVALID: '用户资料、角色或权限依赖不合法，请检查输入', IDENTITY_CONFLICT: '账号主体或角色编码已存在',
  IDEMPOTENCY_CONFLICT: '请求标识已用于不同参数，请重新打开表单', ROLE_IN_USE: '角色仍有分配用户，请先改派角色',
  BUILTIN_ROLE: '内置角色不能修改或删除', USER_HAS_ASSETS: '用户仍有实例、保留数据或恢复占用，请先禁用并处理资产',
  SELF_CHANGE_FORBIDDEN: '不能禁用、删除自己或修改自己的角色', LAST_ADMIN_REQUIRED: '必须保留至少一名有效管理员',
  LOCAL_IDENTITY_UNAVAILABLE: '未配置本地账号供应，或此账号由组织身份提供方维护',
  IDENTITY_UNAVAILABLE: '身份提供方尚未确认操作，请核实原操作后重试',
  IDENTITY_ALREADY_SUPPLIED: '登录账号已创建，不能修改登录邮箱；请重试身份确认',
  PASSWORD_RESET_PENDING: '已有密码重置待确认，请刷新用户并重试原操作',
  VERSION_CONFLICT: '用户、角色或配额已变化，请重新读取并确认',
  QUOTA_BELOW_USAGE: '额度不能低于当前已预留或已占用资源，请先释放资源',
};
export class ManagementClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  private async request<T>(path: string, user: CurrentUser, method = 'GET', body?: unknown, key?: string, signal?: AbortSignal): Promise<T> {
    const response = await this.fetcher(path, { method, credentials: 'same-origin', cache: 'no-store', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10_000)]) : AbortSignal.timeout(10_000),
      headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json', 'X-CSRF-Token': user.csrf_token, ...(key ? { 'Idempotency-Key': key } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
    if (!response.ok) {
      const result: { code?: unknown; message?: unknown } = await response.json().catch(() => ({}));
      const error = new APIError(response.status, typeof result.code === 'string' ? result.code : 'DEPENDENCY_UNAVAILABLE');
      if (messages[error.code]) error.message = messages[error.code];
      if (error.code === 'IDENTITY_INVALID' && typeof result.message === 'string' && result.message.trim()) error.message = result.message;
      throw error;
    }
    return response.status === 204 ? undefined as T : await response.json() as T;
  }
  private query(filter: ListFilter) { const query = new URLSearchParams(); for (const [key, value] of Object.entries(filter)) if (value !== undefined && value !== '') query.set(key, String(value)); return query.size ? `?${query}` : ''; }
  users(user: CurrentUser, filter: ListFilter = {}, signal?: AbortSignal) { return this.request<ManagementPage<ManagedUser>>('/v1/users' + this.query(filter), user, 'GET', undefined, undefined, signal); }
  roles(user: CurrentUser, filter: ListFilter = {}, signal?: AbortSignal) { return this.request<ManagementPage<ManagedRole>>('/v1/roles' + this.query(filter), user, 'GET', undefined, undefined, signal); }
  userRoleOptions(user: CurrentUser, filter: ListFilter = {}, signal?: AbortSignal) { return this.request<ManagementPage<RoleOption>>('/v1/users/role-options' + this.query(filter), user, 'GET', undefined, undefined, signal); }
  permissions(user: CurrentUser, signal?: AbortSignal) { return this.request<{ items: Permission[] }>('/v1/permissions', user, 'GET', undefined, undefined, signal); }
  userQuota(user: CurrentUser, targetId: string, signal?: AbortSignal) { return this.request<UserQuota>('/v1/users/' + targetId + ':quota', user, 'GET', undefined, undefined, signal); }
  saveUserQuota(user: CurrentUser, targetId: string, limits: UserQuotaLimits, expectedVersion: number) { return this.request<UserQuota>('/v1/users/' + targetId + ':quota', user, 'PUT', { limits, expected_version: expectedVersion }); }
  createUser(user: CurrentUser, input: UserCreateInput, key: string) { return this.request<ManagedUser>('/v1/users', user, 'POST', input, key); }
  editUser(user: CurrentUser, target: ManagedUser, input: { display_name: string; email: string }) { return this.request<ManagedUser>('/v1/users/' + target.id, user, 'PUT', { ...input, expected_version: target.version }); }
  deleteUser(user: CurrentUser, target: ManagedUser, confirmedName: string) { return this.request<void>('/v1/users/' + target.id, user, 'DELETE', { expected_version: target.version, confirmed_name: confirmedName }); }
  userAction(user: CurrentUser, target: ManagedUser, action: UserAction, input: Omit<UserActionInput, 'expected_version'> = {}) { return this.request<ManagedUser>('/v1/users/' + target.id + ':' + action, user, 'POST', { ...input, expected_version: target.version }); }
  saveRole(user: CurrentUser, input: RoleInput, target?: ManagedRole, key?: string) { return this.request<ManagedRole>(target ? '/v1/roles/' + target.id : '/v1/roles', user, target ? 'PUT' : 'POST', input, key); }
  deleteRole(user: CurrentUser, target: ManagedRole) { return this.request<void>('/v1/roles/' + target.id, user, 'DELETE', { expected_version: target.version }); }
}
