import { expect, test } from 'vitest';
import type { CurrentUser } from './identity';
import { CommandsClient } from './commands';
import { APIError } from './error';

const user: CurrentUser = { id: '00000000-0000-4000-8000-000000000001', memberships: [{ org_id: '00000000-0000-4000-8000-000000000002', role: 'MEMBER', status: 'ACTIVE' , role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员' }], permissions: ["instances:read", "instances:create", "instances:start", "instances:stop", "instances:delete", "instances:configure", "instances:access", "instances:retry", "configuration:read", "audit:read"], auth_generation: 1, csrf_token: 'private-csrf-value-never-store-1234567890', session_expires_at: '2099-01-01T00:00:00Z' };
const accepted = { instance_id: '00000000-0000-4000-8000-000000000003', operation_id: '00000000-0000-4000-8000-000000000004', state: 'CREATING' };
function memory() { const values = new Map<string,string>(); return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string,value: string) => { values.set(key,value); }, removeItem: (key: string) => { values.delete(key); }, values }; }

test('Chinese deletion confirmation uses ASCII-safe headers and survives an uncertain replay',async()=>{
 const storage=memory();const name='研发工作空间_01';let key='';let calls=0;
 const client=new CommandsClient(async(_url,init)=>{
  const headers=new Headers(init?.headers);
  expect(headers.get('X-Confirm-Instance-Name')).toBe(encodeURIComponent(name));
  expect(init?.body).toBeUndefined();
  if(++calls===1){key=headers.get('Idempotency-Key')!;throw new Error('lost');}
  expect(headers.get('Idempotency-Key')).toBe(key);
  return new Response(JSON.stringify({...accepted,state:'DELETING'}),{status:202});
 },storage);
 await expect(client.delete(user,accepted.instance_id,name)).rejects.toThrow('待确认');
 await expect(client.confirmPending(user,'delete',accepted.instance_id)).resolves.toMatchObject({state:'DELETING'});
 expect(calls).toBe(2);
});

test('failed operation retry keeps its source across a lost response and never sends a body',async()=>{
 const storage=memory();const source='00000000-0000-4000-8000-000000000005';let firstKey:string|undefined;let attempts=0;
 const fetcher:typeof fetch=async(path,init)=>{
  expect(path).toBe(`/v1/operations/${source}:retry`);expect(init?.body).toBeUndefined();expect(init?.method).toBe('POST');
  const key=new Headers(init?.headers).get('Idempotency-Key')!;
  if(++attempts===1){firstKey=key;throw new Error('response lost')}
  expect(key).toBe(firstKey);return new Response(JSON.stringify({...accepted,state:'STARTING'}),{status:202});
 };
 await expect(new CommandsClient(fetcher,storage).retry(user,accepted.instance_id,source)).rejects.toThrow('提交结果待确认');
 const restored=new CommandsClient(fetcher,storage);
 expect(restored.hasPending(user,'retry',accepted.instance_id)).toBe(true);
 await expect(restored.retry(user,accepted.instance_id,accepted.operation_id)).rejects.toThrow('上次提交结果仍待确认');
 expect((await restored.confirmPending(user,'retry',accepted.instance_id)).instance_id).toBe(accepted.instance_id);
 expect(attempts).toBe(2);
});

test('network ambiguity survives reload, reuses the original key and never persists secrets', async () => {
 const storage=memory(); const keys:string[]=[];
 const first = new CommandsClient(async (_url,init) => { keys.push(new Headers(init?.headers).get('Idempotency-Key')!); throw new TypeError('private network diagnostic'); },storage);
 await expect(first.create(user)).rejects.not.toThrow('private network diagnostic');
 expect(first.hasPending(user,'create')).toBe(true);
 expect([...storage.values.values()].join('')).not.toContain(user.csrf_token);
 const reload = new CommandsClient(async (url,init) => { expect(url).toBe('/v1/instances'); expect(init?.body).toBe('{}'); expect(init?.credentials).toBe('same-origin'); expect(init?.cache).toBe('no-store'); expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe(user.csrf_token); keys.push(new Headers(init?.headers).get('Idempotency-Key')!); return new Response(JSON.stringify(accepted),{status:202}); },storage);
 expect(await reload.create(user)).toEqual(accepted); expect(keys[1]).toBe(keys[0]); expect(reload.hasPending(user,'create')).toBe(false); expect(reload.lastAccepted(user,'create')).toEqual(accepted);
 await reload.create(user); expect(keys[2]).not.toBe(keys[1]);
});
test('uncertain configuration cannot silently change version or principal; original submission can be confirmed', async () => {
 const storage=memory(); const input={ expected_instance_version:1,model_configuration_id:accepted.operation_id,model_configuration_version:1 };
 let calls=0;
 const client = new CommandsClient(async () => {calls++; return new Response('{}',{status:503});},storage);
 await expect(client.configure(user,accepted.instance_id,input)).rejects.toBeInstanceOf(APIError);
 await expect(client.configure(user,accepted.instance_id,{...input,expected_instance_version:2})).rejects.toThrow('上次提交');
 expect(calls).toBe(1);
 expect(client.hasPending({...user,id:accepted.operation_id},'configure',accepted.instance_id)).toBe(false);
 const retry = new CommandsClient(async (_url,init) => { expect(JSON.parse(init!.body as string)).toEqual(input); return new Response(JSON.stringify({...accepted,state:'STOPPING'}),{status:202});},storage);
 await retry.confirmPending(user,'configure',accepted.instance_id); expect(retry.hasPending(user,'configure',accepted.instance_id)).toBe(false);
});
test('delete uses exact header confirmation with no body and does not accept malformed acceptance',async () => {
 const storage=memory(); let count=0;
 const client = new CommandsClient(async (url,init) => {
  expect(url).toBe('/v1/instances/'+accepted.instance_id); expect(init?.method).toBe('DELETE'); expect(init?.body).toBeUndefined(); expect(new Headers(init?.headers).get('X-Confirm-Instance-Name')).toBe('work-one'); count++;
  return new Response(JSON.stringify(count===1?{...accepted,operation_id:'foreign/path'}:{...accepted,state:'DELETING'}),{status:202});
 },storage);
 await expect(client.delete(user,accepted.instance_id,'work-one')).rejects.toThrow('结果');
 expect(client.hasPending(user,'delete',accepted.instance_id)).toBe(true);
 await client.confirmPending(user,'delete',accepted.instance_id);
});
test('submission persistence failure, expired ambiguity and duplicate clicks never send another request',async () => {
 let count=0;const client=new CommandsClient(async()=>{count++;return new Response('{}');},{getItem:()=>null,setItem:()=>{throw Error('storage denied')},removeItem:()=>{}});
 await expect(client.create(user)).rejects.toThrow('保存');expect(count).toBe(0);
 const storage=memory(); let finish!:(r:Response)=>void;
 const pending = new CommandsClient(()=>new Promise(resolve=>{count++;finish=resolve}),storage);
 const request=pending.create(user);await expect(pending.create(user)).rejects.toThrow('提交');expect(count).toBe(1);
 finish(new Response('{}',{status:503}));await expect(request).rejects.toBeInstanceOf(APIError);
 const entry=JSON.parse([...storage.values.values()][0]);entry.created_at=Date.now()-7*24*60*60*1000;
 storage.setItem([...storage.values.keys()][0],JSON.stringify(entry));await expect(pending.create(user)).rejects.toThrow('超过');expect(count).toBe(1);
});
test('authentication failure after a lost response keeps the original key across a new login',async () => {
 const storage=memory();const keys:string[]=[];let count=0;
 const client=new CommandsClient(async (_url,init)=>{
  keys.push(new Headers(init?.headers).get('Idempotency-Key')!);count++;
  if(count===1)throw new TypeError('network lost');if(count===2)return new Response(JSON.stringify({code:'UNAUTHENTICATED'}),{status:401});
  return new Response(JSON.stringify(accepted),{status:202});
 },storage);
 await expect(client.create(user)).rejects.toThrow('待确认');await expect(client.create(user)).rejects.toBeInstanceOf(APIError);
 expect(client.hasPending(user,'create')).toBe(true);await client.create({...user,csrf_token:'a-new-private-csrf-token-after-login-1234567'});
 expect(new Set(keys).size).toBe(1);
});

test('uncertain creation keeps resource spec and model version across reload',async()=>{
 const storage=memory();const input={spec_id:accepted.instance_id,model_configuration_id:accepted.operation_id,model_configuration_version:3};let key='';
 const client=new CommandsClient(async(_url,init)=>{key=new Headers(init?.headers).get('Idempotency-Key')!;throw new Error('lost');},storage);
 await expect(client.create(user,input)).rejects.toThrow('待确认');
 await expect(client.create(user,{...input,spec_id:user.id})).rejects.toThrow('上次提交');
 const restored=new CommandsClient(async(_url,init)=>{expect(JSON.parse(init!.body as string)).toEqual(input);expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(key);return new Response(JSON.stringify(accepted),{status:202});},storage);
 await expect(restored.confirmPending(user,'create')).resolves.toEqual(accepted);
});
