import { request } from 'node:http';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Socket } from 'node:net';
import type { Plugin, ViteDevServer } from 'vite';

// HTTP/1 framing belongs to the upstream connection. It must not reach the
// HTTP/2 HTTPS listener; Set-Cookie remains an array of separate headers.
export function proxyResponseHeaders(headers: IncomingMessage['headers']): IncomingMessage['headers'] {
 const blocked = new Set(['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade']);
 for (const token of String(headers.connection ?? '').split(',')) blocked.add(token.trim().toLowerCase());
 return Object.fromEntries(Object.entries(headers).filter(([name]) => !blocked.has(name.toLowerCase())));
}

// The local development HTTPS listener routes only fixed instance authorities
// to the loopback Gateway. Gateway performs all authorization and upstream
// selection; the Vite process never reads native cookies or model credentials.
export function localAccess(): Plugin {
 const authority = (req: IncomingMessage) => String(req.headers.host ?? req.headers[':authority'] ?? '');
 const forward = (req: IncomingMessage) => {
  const headers = { ...req.headers, host: authority(req) };
  for (const name of Object.keys(headers)) if (name.startsWith(':')) delete headers[name as keyof typeof headers];
  return request({ hostname: '127.0.0.1', port: 18082, path: req.url, method: req.method, headers });
 };
 const fail = (res: ServerResponse) => { if (!res.headersSent) { res.writeHead(503, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify({ code: 'DEPENDENCY_UNAVAILABLE', message: '工作空间访问暂不可用' })); } else res.destroy(); };
 const install = (server: Pick<ViteDevServer, 'middlewares' | 'httpServer'>, port: number) => {
  const instance = new RegExp('^i-[a-f0-9]{32}\\.harness\\.localhost:' + port + '$');
  const portal = new Set(['portal.harness.localhost:' + port, '127.0.0.1:' + port, 'localhost:' + port]);
  server.middlewares.use((req, res, next) => {
   const host = authority(req);
   if (portal.has(host)) { next(); return; }
   if (!instance.test(host)) { res.writeHead(404); res.end(); return; }
   const upstream = forward(req);
   upstream.on('response', response => { try { res.writeHead(response.statusCode ?? 502, proxyResponseHeaders(response.headers)); response.pipe(res); response.on('error', () => res.destroy()); } catch { response.destroy(); fail(res); } });
   upstream.on('error', () => fail(res)); req.on('aborted', () => upstream.destroy()); res.on('close', () => upstream.destroy()); req.pipe(upstream);
  });
  server.httpServer?.prependListener('upgrade', (req: IncomingMessage, socket: Socket, head: Buffer) => {
   const host = authority(req);
   if (portal.has(host)) return;
   if (!instance.test(host)) { socket.destroy(); return; }
   const upstream = forward(req);
   upstream.on('upgrade', (response, native, buffered) => {
    let headers = `HTTP/1.1 ${response.statusCode} ${response.statusMessage}\r\n`;
    for (let i = 0; i < response.rawHeaders.length; i += 2) headers += `${response.rawHeaders[i]}: ${response.rawHeaders[i + 1]}\r\n`;
    socket.write(headers + '\r\n'); if (buffered.length) socket.write(buffered); if (head.length) native.write(head);
    socket.pipe(native); native.pipe(socket); socket.on('close', () => native.destroy()); native.on('close', () => socket.destroy());
    socket.on('error', () => native.destroy()); native.on('error', () => socket.destroy());
   });
   upstream.on('response', response => { response.resume(); socket.end(`HTTP/1.1 ${response.statusCode ?? 503} Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n`); });
   upstream.on('error', () => socket.destroy()); socket.on('close', () => upstream.destroy()); upstream.end();
  });
 };
 return { name: 'local-access', configureServer: server => install(server, server.config.server.port ?? 42013), configurePreviewServer: server => install(server, server.config.preview.port ?? 42013) };
}
