// Only transport lives here. DSH loads the selected memory bundle itself.
import http from 'node:http';
import { randomBytes } from 'node:crypto';

export async function mountSessionMemory(ctx, item, request) {
  const prefix = '/' + randomBytes(24).toString('hex');
  const server = http.createServer(async (req, res) => {
    try {
      if (!req.url?.startsWith(prefix + '/')) {
        res.writeHead(404).end();
        return;
      }
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 512 * 1024) {
          res.writeHead(413).end();
          return;
        }
        chunks.push(chunk);
      }
      const headers = {};
      for (const key of ['accept', 'mcp-session-id', 'mcp-protocol-version'])
        if (typeof req.headers[key] === 'string') headers[key] = req.headers[key];
      const path = req.url.slice(prefix.length);
      const body = Buffer.concat(chunks).toString('utf8');
      if (['mem0', 'graphiti'].includes(item.backend)) {
        const paths = ['/search', '/sessions/messages', '/sessions/commit', '/sessions/status'];
        if (req.method !== 'POST' || !paths.includes(path)) {
          res.writeHead(404).end();
          return;
        }
        const input = path === '/search' ? JSON.parse(body) : null;
        if (
          input &&
          (Object.keys(input).length !== 1 ||
            typeof input.query !== 'string' ||
            !input.query.trim() ||
            input.query.length > 4000)
        ) {
          res.writeHead(400).end();
          return;
        }
        const result = await request(
          {
            kind: 'memory',
            source_id: item.id,
            scope: item.session_scope,
            ...(path === '/search'
              ? { action: 'search', query: input?.query }
              : {
                  action: 'session',
                  session_request: { method: 'POST', path, body },
                }),
          },
          {},
        );
        res.writeHead(200, { 'content-type': 'application/json' }).end(result);
        return;
      }
      const result = JSON.parse(
        await request(
          {
            kind: 'memory',
            source_id: item.id,
            action: 'session',
            scope: item.session_scope,
            session_request: {
              method: req.method,
              path,
              body,
              headers,
            },
          },
          {},
        ),
      );
      res.writeHead(result.status, result.headers).end(result.body);
    } catch {
      if (!res.headersSent) res.writeHead(503, { 'content-type': 'application/json' });
      res.end('{"status":"error","message":"会话记忆连接暂不可用"}');
    }
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  ctx.effect(
    () => () => new Promise((resolve) => server.close(resolve)),
    'cloudSessionMemory.close()',
  );
  // Credentials are deliberately empty. The scoped companion authenticates
  // upstream, and never accepts identity or service headers from this process.
  ctx.provide('cloudSessionMemory', {
    endpoint: `http://127.0.0.1:${server.address().port}${prefix}`,
  });
}
