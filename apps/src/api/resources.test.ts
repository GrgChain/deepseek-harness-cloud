import { expect, test } from 'vitest';
import { ResourcesClient } from './resources';
import type { CurrentUser } from './identity';
import { APIError } from './error';
const user = { id: 'owner' } as CurrentUser;

test('resource reads use private cookies, preserve opaque cursors and redact upstream errors', async () => {
 let requested = '';
 const client = new ResourcesClient(async (input, init) => {
  requested = String(input);
  expect(init?.credentials).toBe('same-origin'); expect(init?.cache).toBe('no-store'); expect(init?.body).toBeUndefined();
  return new Response(JSON.stringify({ items: [], next_cursor: null }));
 });
 expect((await client.instances(user, { q: 'work-', state: 'STOPPED' }, 'opaque.cursor')).items).toEqual([]);
 expect(new URL(requested, 'https://portal.test').searchParams.get('cursor')).toBe('opaque.cursor');
 const denied = new ResourcesClient(async () => new Response(JSON.stringify({ code: 'UNAUTHENTICATED', message: 'private raw diagnostics' }), { status: 401 }));
 await expect(denied.operationLogs()).rejects.toBeInstanceOf(APIError);
 await expect(denied.operationLogs()).rejects.not.toThrow('private raw diagnostics');
});
test('a resource response for another owner is rejected before rendering', async () => {
 const client = new ResourcesClient(async () => new Response(JSON.stringify({ id: 'foreign', owner_id: 'someone-else' })));
 await expect(client.details(user, 'foreign')).rejects.toThrow('实例数据无效');
});
test('operation log filters preserve exact authorized object action outcome and date bounds',async()=>{
 let requested='';const client=new ResourcesClient(async input=>{requested=String(input);return new Response(JSON.stringify({items:[],next_cursor:null}));});
 await client.operationLogs(undefined,undefined,10,2,{action:'USER_QUOTA_UPDATE',outcome:'FAILED',resource_id:'object-id',from:'2026-10-14T00:00:00Z'});
 const query=new URL(requested,'https://portal.test').searchParams;
 expect(Object.fromEntries(query)).toEqual({limit:'10',page:'2',action:'USER_QUOTA_UPDATE',outcome:'FAILED',resource_id:'object-id',from:'2026-10-14T00:00:00Z'});
});
