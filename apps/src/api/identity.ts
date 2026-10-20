export interface CurrentUser {
  id: string;
  memberships: { org_id: string; role: string; role_id: string; role_name: string; status: 'ACTIVE' | 'DISABLED' }[];
  permissions: string[];
  auth_generation: number;
  csrf_token: string;
  session_expires_at: string;
}

export class IdentityClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}

  async me(signal?: AbortSignal): Promise<CurrentUser | null> {
    const response = await this.fetcher('/v1/me', { credentials: 'same-origin', cache: 'no-store', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(5000)]) : AbortSignal.timeout(5000) });
    if (response.status === 401) return null;
    if (!response.ok) throw new Error('身份服务暂不可用，请稍后重试');
    const user: CurrentUser = await response.json();
    const membership = user.memberships?.[0];
    if (typeof user.id !== 'string' || !Array.isArray(user.memberships) || user.memberships.length !== 1 || !membership || !/^(ADMIN|MEMBER|[a-z][a-z0-9_]{2,31})$/.test(membership.role) || typeof membership.role_id !== 'string' || typeof membership.role_name !== 'string' || membership.status !== 'ACTIVE' || !Array.isArray(user.permissions) || user.permissions.length > 64 || !user.permissions.every(value => typeof value === 'string' && /^[a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*){1,2}$/.test(value)) || new Set(user.permissions).size !== user.permissions.length || !Number.isSafeInteger(user.auth_generation) || user.auth_generation < 1 || typeof user.csrf_token !== 'string' || user.csrf_token.length < 32 || !Number.isFinite(Date.parse(user.session_expires_at))) {
      throw new Error('用户会话数据无效，请重新登录');
    }
    return user;
  }

  async logout(user: CurrentUser): Promise<void> {
    const response = await this.fetcher('/auth/logout', { method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { 'X-CSRF-Token': user.csrf_token }, signal: AbortSignal.timeout(5000) });
    if (response.status !== 204 && response.status !== 401) throw new Error('退出登录失败，请重试');
  }
}

export function hasPermission(user: CurrentUser | null | undefined, permission: string): boolean {
  return user?.permissions.includes(permission) === true;
}
