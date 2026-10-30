import { createServer, connect } from 'node:http2';
import { EventEmitter, once } from 'node:events';
import { expect, test } from 'vitest';
import { PassThrough } from 'node:stream';
import { localAccess, proxyResponseHeaders } from './local-access';

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

test('browser reset before an upgrade completes does not escape the local HTTPS proxy', () => {
 const httpServer=new EventEmitter();
 const plugin=localAccess();
 const server={middlewares:{use:()=>{}},httpServer,config:{server:{port:42013}}};
 (plugin.configureServer as Function)(server);
 const socket=new PassThrough();
 const request={headers:{host:'i-796c8262e76d452dbddcb87a83e2ee53.harness.localhost:42013'},url:'/api/remote.mux',method:'GET'};
 httpServer.emit('upgrade',request,socket,Buffer.alloc(0));
 expect(socket.listenerCount('error')).toBeGreaterThan(0);
 expect(()=>socket.emit('error',Object.assign(new Error('reset'),{code:'ECONNRESET'}))).not.toThrow();
 socket.destroy();
});
