import { describe, expect, it, vi } from 'vitest';
import { APIError } from './error';
import { SettingsClient } from './settings';
import type { CurrentUser } from './identity';
const user: CurrentUser = { id: 'test-user', memberships: [], permissions: [], auth_generation: 1, csrf_token: 'csrf-opaque', session_expires_at: '2099-01-01T00:00:00Z' };
describe('settings client', () => {
  it('only treats documented absence as uninitialized', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response('{"code":"NOT_FOUND"}', { status: 404 })).mockResolvedValueOnce(new Response('{"code":"FORBIDDEN"}', { status: 403 }));
    const client = new SettingsClient(fetcher);
    expect(await client.model(user)).toBeNull();
    await expect(client.model(user)).rejects.toBeInstanceOf(APIError);
  });
  it('keeps credentials in the explicit write body, with CSRF and no cache', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response('{}'));
    await new SettingsClient(fetcher).saveModel(user, { provider: 'deepseek-official', protocol: 'deepseek-messages', endpoint: 'https://api.deepseek.com/anthropic', model: 'test', api_key: 'synthetic-key', expected_version: 0 });
    const [path, options] = fetcher.mock.calls[0];
    expect(path).toBe('/v1/model-configuration');
    expect(options).toMatchObject({ method: 'PUT', cache: 'no-store', credentials: 'same-origin', headers: { 'X-CSRF-Token': 'csrf-opaque' } });
    expect(options?.body).toContain('synthetic-key');
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});
