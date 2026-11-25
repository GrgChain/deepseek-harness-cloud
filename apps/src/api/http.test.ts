import { describe, expect, it, vi } from 'vitest';
import { APIError } from './error';
import { apiFetch } from './http';

describe('apiFetch', () => {
  it('sends same-origin GET without CSRF header when no body is provided', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    const res = await apiFetch('/v1/test', { fetcher, timeoutMs: 5_000 });
    expect(await res.json()).toEqual({ ok: true });
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/v1/test');
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('same-origin');
    expect(init.cache).toBe('no-store');
    expect(init.headers).toBeUndefined();
    expect(init.body).toBeUndefined();
    expect(init.signal).toBeInstanceOf(AbortSignal);
  });

  it('sends JSON body and CSRF token for write requests and preserves rawBody', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ saved: 1 }), { status: 200 }));
    await apiFetch('/v1/items', {
      fetcher,
      user: { csrf_token: 'csrf-123' },
      body: { a: 1 },
      rawBody: '{"raw":true}',
      method: 'PUT',
    });
    const [, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('PUT');
    expect(init.headers).toEqual({
      'Content-Type': 'application/json',
      'X-CSRF-Token': 'csrf-123',
    });
    expect(init.body).toBe('{"raw":true}');
  });

  it('attaches X-CSRF-Token on bodiless mutating requests and merges custom headers', async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 204 }));
    await apiFetch('/v1/storage-assets/asset-1', {
      fetcher,
      method: 'DELETE',
      user: { csrf_token: 'csrf-del' },
      headers: { 'Idempotency-Key': 'idem-1' },
    });
    const [, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('DELETE');
    expect(init.body).toBeUndefined();
    expect(init.headers).toEqual({
      'X-CSRF-Token': 'csrf-del',
      'Idempotency-Key': 'idem-1',
    });
  });

  it('maps error responses to APIError and applies passthroughError selectively', async () => {
    const fetcher = vi.fn(
      async () =>
        new Response(JSON.stringify({ code: 'PLUGIN_INVALID', message: '插件清单缺少版本' }), {
          status: 422,
        }),
    );
    await expect(
      apiFetch('/v1/plugins', {
        fetcher,
        passthroughError: (code) => code.startsWith('PLUGIN_'),
      }),
    ).rejects.toMatchObject({
      status: 422,
      code: 'PLUGIN_INVALID',
      message: '插件清单缺少版本',
    });

    await expect(apiFetch('/v1/plugins', { fetcher })).rejects.toBeInstanceOf(APIError);
    await expect(apiFetch('/v1/plugins', { fetcher })).rejects.not.toMatchObject({
      message: '插件清单缺少版本',
    });
  });

  it('falls back to DEPENDENCY_UNAVAILABLE on non-JSON error responses', async () => {
    const fetcher = vi.fn(async () => new Response('gateway error', { status: 502 }));
    await expect(apiFetch('/v1/down', { fetcher })).rejects.toMatchObject({
      status: 502,
      code: 'DEPENDENCY_UNAVAILABLE',
    });
  });
});
