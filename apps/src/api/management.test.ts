import { describe, expect, it, vi } from 'vitest';
import { APIError } from './error';
import type { CurrentUser } from './identity';
import { ManagementClient } from './management';

const mockUser: CurrentUser = {
  id: '00000000-0000-4000-8000-000000000001',
  csrf_token: 'csrf-token-management',
  auth_generation: 1,
  session_expires_at: '2026-12-31T00:00:00Z',
  memberships: [],
  permissions: [],
};

describe('ManagementClient', () => {
  it('encodes path identifiers and attaches CSRF and idempotency headers on mutations', async () => {
    const fetcher = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ id: 'role-1' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    const client = new ManagementClient(fetcher);
    await client.userQuota(mockUser, 'user/with:special');
    expect(fetcher).toHaveBeenCalledWith(
      '/v1/users/user%2Fwith%3Aspecial:quota',
      expect.objectContaining({ method: 'GET' }),
    );

    await client.createUser(
      mockUser,
      {
        display_name: 'Alice',
        email: 'alice@example.com',
        identity_source: 'local',
        role_id: 'role-1',
        initial_password: 'password123456',
      },
      'idem-key-1',
    );
    const [, init] = fetcher.mock.calls[1] as [string, RequestInit];
    expect(init.headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-CSRF-Token': 'csrf-token-management',
      'Idempotency-Key': 'idem-key-1',
    });
  });

  it('preserves server IDENTITY_INVALID detail messages and maps domain error codes', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ code: 'IDENTITY_INVALID', message: '自定义权限校验失败' }), {
          status: 422,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ code: 'LAST_ADMIN_REQUIRED' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    const client = new ManagementClient(fetcher);
    await expect(client.permissions(mockUser)).rejects.toMatchObject({
      status: 422,
      code: 'IDENTITY_INVALID',
      message: '自定义权限校验失败',
    } satisfies Partial<APIError>);
    await expect(client.permissions(mockUser)).rejects.toMatchObject({
      status: 409,
      code: 'LAST_ADMIN_REQUIRED',
      message: '必须保留至少一名有效管理员',
    } satisfies Partial<APIError>);
  });
});
