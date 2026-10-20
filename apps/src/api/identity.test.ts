import { describe, expect, it, vi } from 'vitest';
import { IdentityClient, type CurrentUser } from './identity';

const user: CurrentUser = { id: '00000000-0000-4000-8000-000000000001', memberships: [{ org_id: '00000000-0000-4000-8000-000000000002', role: 'MEMBER', status: 'ACTIVE' , role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员' }], permissions: ["instances:read", "instances:create", "instances:start", "instances:stop", "instances:delete", "instances:configure", "instances:access", "instances:retry", "configuration:read", "audit:read"], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2026-10-10T15:00:00Z' };

describe('IdentityClient', () => {
  it('accepts scoped personal-model and catalog permissions from the authenticated projection', async () => {
    const projected = { ...user, permissions: ['personal-model:read', 'personal-model:write', 'models:catalog:manage'] };
    const client = new IdentityClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(projected))));
    expect(await client.me()).toEqual(projected);
  });
  it('accepts a custom role with explicit effective permissions and authorization version', async () => {
    const custom = { ...user, memberships: [{ ...user.memberships[0], role: 'observer', role_id: '00000000-0000-4000-8000-000000000003', role_name: '只读成员' }], permissions: ['instances:read'], auth_generation: 4 };
    const client = new IdentityClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(custom))));
    expect(await client.me()).toEqual(custom);
  });
  it('reads only the server-authorized identity and treats expiry as anonymous', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(JSON.stringify(user))).mockResolvedValueOnce(new Response('{}', { status: 401 }));
    const client = new IdentityClient(fetcher);
    expect(await client.me()).toEqual(user);
    expect(await client.me()).toBeNull();
  });
  it('does not silently treat service failures as logout', async () => {
    const client = new IdentityClient(vi.fn<typeof fetch>().mockResolvedValue(new Response('{}', { status: 503 })));
    await expect(client.me()).rejects.toThrow('身份服务暂不可用');
  });
  it('sends CSRF on logout and never sends provider tokens', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }));
    await new IdentityClient(fetcher).logout(user);
    expect(fetcher.mock.calls[0][0]).toBe('/auth/logout');
    expect(fetcher.mock.calls[0][1]?.headers).toEqual({ 'X-CSRF-Token': user.csrf_token });
  });
});
