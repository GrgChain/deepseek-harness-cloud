import z from '@deepseek-ai/schemastery';
import { mountSessionMemory } from './session.mjs';
import { mountGraphitiMcp } from './mcp.mjs';
import { mountGraphitiSkills } from './skills.mjs';

export const name = 'graphiti-memory';
export const inject = ['agents', 'sessions', 'tools'];
export const Config = z.object({
  endpoint: z.string().required().description('Platform-provided Graphiti loopback endpoint.'),
});

export async function apply(ctx, input = {}) {
  const config = Config(input);
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
    throw Error('Graphiti requires the platform loopback endpoint');

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
      throw Error('Managed Graphiti request failed (HTTP ' + response.status + ')');
    }
    const parts = [];
    let size = 0;
    for await (const part of response.body ?? []) {
      size += part.byteLength;
      if (size > 1048576) throw Error('Managed Graphiti response exceeded the size limit');
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
    { serviceName: 'graphitiMemory', sourceKind: 'graphiti-memory', archiveSessions: true },
  );
  await mountGraphitiMcp(ctx, {
    search: async (args, signal) => JSON.parse(await search(args.query, signal)),
    status: () => ({
      managed: true,
      autoRecall: true,
      autoCapture: true,
      captureAssistant: true,
      captureToolResults: true,
      captureMode: 'session_archive',
      ...ctx.get('graphitiMemory').status(),
    }),
  });
  mountGraphitiSkills(ctx);
}
