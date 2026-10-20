import { describe, expect, it, vi } from 'vitest';
import { PlatformClient } from './platform';

describe('PlatformClient', () => {
  it('reports only actual process capabilities and dependency readiness', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'alive', process: 'api', capabilities: { authentication: false, instances: false } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'ready', process: 'api' })));
    const status = await new PlatformClient(fetcher).status();
    expect(status).toEqual({ online: true, dependenciesReady: true, authenticationReady: false });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('keeps authentication closed when the response lacks a declared capability', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response('{}'));
    await expect(new PlatformClient(fetcher).status()).rejects.toThrow('STATUS_INVALID');
  });

  it('reports unavailable dependencies without exposing server error text', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'alive', process: 'api', capabilities: { authentication: false } })))
      .mockResolvedValueOnce(new Response('private connection detail', { status: 503 }));
    expect(await new PlatformClient(fetcher).status()).toEqual({ online: true, dependenciesReady: false, authenticationReady: false });
  });

  it('rejects unexpected readiness errors instead of reporting them as dependency failures', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'alive', process: 'api', capabilities: { authentication: false } })))
      .mockResolvedValueOnce(new Response('not found', { status: 404 }));
    await expect(new PlatformClient(fetcher).status()).rejects.toThrow('STATUS_UNAVAILABLE');
  });
});
