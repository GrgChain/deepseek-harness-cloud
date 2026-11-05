import { mountSessionMemory } from './session.mjs';

// Platform mode has the same native bundle lifecycle as standalone mode. Only
// the loopback transport is supplied by the platform, never service credentials.
export async function applyManaged(ctx, config) {
  const endpoint = new URL(config.endpoint);
  if (
    endpoint.protocol !== 'http:' ||
    endpoint.hostname !== '127.0.0.1' ||
    !endpoint.port ||
    !/^\/[a-f0-9]{48}$/.test(endpoint.pathname) ||
    endpoint.username ||
    endpoint.password ||
    endpoint.search ||
    endpoint.hash
  )
    throw Error('Managed Mem0 requires the platform loopback endpoint');

  const post = async (path, body, signal) => {
    const response = await fetch(endpoint.href + path, {
      method: 'POST',
      redirect: 'error',
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(120000)])
        : AbortSignal.timeout(120000),
      headers: { 'content-type': 'application/json' },
      body,
    });
    if (!response.ok) {
      await response.body?.cancel();
      throw Error('Managed Mem0 request failed (HTTP ' + response.status + ')');
    }
    const parts = [];
    let size = 0;
    for await (const part of response.body ?? []) {
      size += part.byteLength;
      if (size > 1048576) throw Error('Managed Mem0 response exceeded the size limit');
      parts.push(part);
    }
    return Buffer.concat(parts).toString('utf8');
  };
  const search = (query, signal) => {
    if (typeof query !== 'string' || !query.trim() || query.length > 4000)
      throw Error('Provide a non-empty memory query up to 4000 characters');
    return post('/search', JSON.stringify({ query }), signal);
  };
  await mountSessionMemory(
    ctx,
    { id: 'managed', session_scope: 'personal' },
    (input, exec) =>
      input.action === 'search'
        ? search(input.query, exec?.signal)
        : post(input.session_request.path, input.session_request.body, exec?.signal),
    { serviceName: 'mem0Memory', sourceKind: 'mem0-memory', archiveSessions: true },
  );
  return {
    search: async (args, signal) => JSON.parse(await search(args.query, signal)),
    status: () => ({
      managed: true,
      autoRecall: true,
      autoCapture: true,
      captureAssistant: true,
      captureToolResults: true,
      captureMode: 'session_archive',
      ...ctx.get('mem0Memory').status(),
    }),
  };
}
