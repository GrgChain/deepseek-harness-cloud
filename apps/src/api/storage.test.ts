import { describe, expect, it, vi } from 'vitest';
import { StorageClient } from './storage';

const assetId = '8c4f2712-aaf0-4bd4-9b6b-a3376ae15410';

describe('StorageClient file browsing', () => {
  it('encodes a fixed-root directory query and requests private uncached data', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ root: 'workspace', path: 'client notes', entries: [] })));
    const directory = await new StorageClient(fetcher).directory(assetId, 'workspace', 'client notes', '');
    const [input, init] = fetcher.mock.calls[0];
    const query = new URL(String(input), 'https://portal.test').searchParams;
    expect(Object.fromEntries(query)).toEqual({ root: 'workspace', path: 'client notes' });
    expect(directory.entries).toEqual([]);
    expect(init?.credentials).toBe('same-origin');
    expect(init?.cache).toBe('no-store');
  });

  it('omits empty root path and cursor values and uses a directory-specific validation message', async () => {
    let requested = '';
    const fetcher = vi.fn<typeof fetch>(async input => {
      requested = String(input);
      return new Response(JSON.stringify({ root: 'workspace', path: '', entries: [] }));
    });
    const directory = await new StorageClient(fetcher).directory(assetId, 'workspace', '', '');
    expect(new URL(requested, 'https://portal.test').search).toBe('?root=workspace');
    expect(directory.path).toBe('');

    const invalid = new StorageClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ code: 'INVALID_SPEC' }), { status: 422 })));
    await expect(invalid.directory(assetId, 'workspace', '', '')).rejects.toThrow('数据目录请求无效，请刷新目录后重试');
  });

  it('sends a cursor only on a later directory page', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ root: 'dsh', path: 'sessions', entries: [] })));
    await new StorageClient(fetcher).directory(assetId, 'dsh', 'sessions', 'last-entry');
    const query = new URL(String(fetcher.mock.calls[0][0]), 'https://portal.test').searchParams;
    expect(Object.fromEntries(query)).toEqual({ root: 'dsh', path: 'sessions', after: 'last-entry' });
  });

  it('serializes directory and file reads for the same data disk', async () => {
    let active = 0;
    let peak = 0;
    const fetcher = vi.fn<typeof fetch>(async input => {
      active += 1;
      peak = Math.max(peak, active);
      await new Promise(resolve => setTimeout(resolve, 10));
      active -= 1;
      if (String(input).includes('/file?')) return new Response(new Blob(['text']));
      const query = new URL(String(input), 'https://portal.test').searchParams;
      return new Response(JSON.stringify({ root: query.get('root'), path: query.get('path') ?? '', entries: [] }));
    });
    const client = new StorageClient(fetcher);
    await Promise.all([
      client.directory(assetId, 'workspace', '', ''),
      client.directory(assetId, 'dsh', '', ''),
      client.file(assetId, 'workspace', 'notes.txt', 'PREVIEW'),
    ]);
    expect(peak).toBe(1);
  });

  it('explains storage busy responses as competing reads, not workspace operations', async () => {
    const client = new StorageClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ code: 'STORAGE_BUSY' }), { status: 409 })));
    await expect(client.directory(assetId, 'workspace', '', '')).rejects.toThrow('数据盘正在处理其他读取请求，请稍后重试');
  });

  it('reads a bounded file as a Blob and derives its download name from the relative path', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(new Blob(['contents'])));
    const file = await new StorageClient(fetcher).file(assetId, 'dsh', 'sessions/model.json', 'DOWNLOAD');
    expect(file.name).toBe('model.json');
    expect(await file.blob.text()).toBe('contents');
    const query = new URL(String(fetcher.mock.calls[0][0]), 'https://portal.test').searchParams;
    expect(Object.fromEntries(query)).toEqual({ root: 'dsh', path: 'sessions/model.json', mode: 'DOWNLOAD' });
  });

  it('rejects previews that exceed the configured size limit', async () => {
    const oversized = new StorageClient(vi.fn<typeof fetch>().mockResolvedValue(new Response(new Uint8Array(512 * 1024 + 1))));
    await expect(oversized.file(assetId, 'workspace', 'large.txt', 'PREVIEW')).rejects.toThrow('文件超过读取大小限制');

  });
});
