import { describe, expect, it, vi } from 'vitest';
import { ConnectorsClient } from './connectors';
import { ContextClient, type ContextConnection } from './context';
import type { CurrentUser } from './identity';
import { PluginsClient } from './plugins';
import { SkillsClient } from './skills';
import { readWorkspaceExtensions } from './workspace-extensions';

const user: CurrentUser = {
  id: 'u-1',
  csrf_token: 'tok-abc',
  auth_generation: 1,
  session_expires_at: '2026-12-01T00:00:00Z',
  memberships: [],
  permissions: [],
};

describe('workbench API clients', () => {
  it('ConnectorsClient passes through connector error messages and CSRF tokens', async () => {
    const fetcher = vi.fn(
      async () =>
        new Response(JSON.stringify({ code: 'CONNECTOR_BUSY', message: '连接器正在同步' }), {
          status: 409,
        }),
    );
    const client = new ConnectorsClient(fetcher);
    await expect(client.request(user, '/c-1/verify', {})).rejects.toMatchObject({
      status: 409,
      code: 'CONNECTOR_BUSY',
      message: '连接器正在同步',
    });
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/v1/connectors/c-1/verify');
    expect(init.headers).toEqual({
      'Content-Type': 'application/json',
      'X-CSRF-Token': 'tok-abc',
    });
  });

  it('PluginsClient maps snake_case fields to camelCase CatalogPlugin', async () => {
    const fetcher = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            items: [
              {
                id: 'p-1',
                name: 'Mem0',
                package_name: '@harness-cloud/dsh-mem0-memory',
                version: '0.2.0',
                source: 'community',
                author: 'Harness',
                category: 'memory',
                summary: 's',
                description: 'd',
                keywords: ['mem0'],
                repository: '',
                documentation: '',
                compatibility: '>=0.2.0',
                checked_at: '2026-11-01',
                custom: false,
                addition_method: 'online',
                filename: '',
                sha256: '',
                source_note: 'local',
                revision: 2,
              },
            ],
          }),
          { status: 200 },
        ),
    );
    const client = new PluginsClient(fetcher);
    const res = await client.list(user);
    expect(res.items[0]).toMatchObject({
      id: 'p-1',
      packageName: '@harness-cloud/dsh-mem0-memory',
      checkedAt: '2026-11-01',
      additionMethod: 'online',
      sourceNote: 'local',
    });
  });

  it('SkillsClient downloads archive and decodes RFC 5987 filename', async () => {
    const fetcher = vi.fn(
      async () =>
        new Response(new Blob(['zip-bytes']), {
          status: 200,
          headers: {
            'Content-Disposition':
              'attachment; filename="fallback.zip"; filename*=utf-8\'\'%E6%8A%80%E8%83%BD.zip',
          },
        }),
    );
    const client = new SkillsClient(fetcher);
    const result = await client.download(user, ['s-1']);
    expect(result.name).toBe('技能.zip');
    expect(result.blob.size).toBe(9);
  });

  it('ContextClient preserves raw JSON payload on previewArchive', async () => {
    const fetcher = vi.fn(
      async () =>
        new Response(
          JSON.stringify({ token: 't1', items: [], warnings: [], data_use: 'ok', expires_in: 60 }),
          { status: 200 },
        ),
    );
    const client = new ContextClient(fetcher);
    const conn = { id: 'ctx-1', kind: 'memory', version: 3 } as ContextConnection;
    await client.previewArchive(user, conn, '{"big":9007199254740993}');
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/v1/context/memory/ctx-1/preview');
    expect(init.body).toBe('{"expected_version":3,"package":{"big":9007199254740993}}');
  });

  it('readWorkspaceExtensions queries extensions or skills path', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ items: [] }), { status: 200 }));
    await readWorkspaceExtensions('inst/1', undefined, true, fetcher);
    const [url] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/v1/workspace-extensions/inst%2F1/skills');
  });
});
