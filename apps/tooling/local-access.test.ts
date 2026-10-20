import { createServer, connect } from 'node:http2';
import { once } from 'node:events';
import { expect, test } from 'vitest';
import { proxyResponseHeaders } from './local-access';

test('HTTP/1 chunked responses cross the HTTP/2 listener without crashing or losing cookies', async () => {
 const server = createServer();
 const headers = { 'transfer-encoding': 'chunked', connection: 'keep-alive, x-internal', 'keep-alive': 'timeout=5', 'x-internal': 'private', 'content-type': 'text/html', 'set-cookie': ['__Host-test=x; Secure; HttpOnly; Path=/'] };
 let failed: (error: Error) => void = () => {};
 server.on('request', (_req, res) => {
  try { res.writeHead(200, proxyResponseHeaders(headers)); res.end('native-root'); }
  catch (error) { failed(error as Error); res.destroy(); }
 });
 server.listen(0, '127.0.0.1'); await once(server, 'listening');
 const address = server.address(); if (!address || typeof address === 'string') throw new Error('test listener required');
 const session = connect('http://127.0.0.1:' + address.port);
 try {
  const result = await new Promise<{ body: string; cookies: unknown }>((resolve, reject) => {
   failed = reject;
   const stream = session.request({ ':path': '/' }); let body = ''; let cookies: unknown;
   stream.on('response', response => { cookies = response['set-cookie']; expect(response['transfer-encoding']).toBeUndefined(); expect(response['x-internal']).toBeUndefined(); });
   stream.on('data', data => { body += String(data); }); stream.on('error', reject);
   stream.on('end', () => resolve({ body, cookies })); stream.end();
  });
  expect(result.body).toBe('native-root'); expect(result.cookies).toEqual(headers['set-cookie']);
 } finally { session.destroy(); server.close(); }
 expect(headers['transfer-encoding']).toBe('chunked');
});
