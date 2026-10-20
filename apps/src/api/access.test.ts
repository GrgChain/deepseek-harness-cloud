import { afterEach, expect, test, vi } from 'vitest';
import { AccessClient, accessOrigin, enterWorkspace, resumeWorkspace } from './access';
import type { Instance } from './resources';
import type { CurrentUser } from './identity';

const id = '00000000-0000-4000-8000-000000000001';
const host = 'i-00000000000040008000000000000001.harness.localhost';
const user = { id: 'owner', csrf_token: 'synthetic-csrf' } as CurrentUser;
const instance = { id, owner_id: user.id, access_host: host, can_enter: true, observed_state:'RUNNING', desired_state:'RUNNING', active_operation_id:null, allowed_actions:['access'] } as Instance;
const portal = 'https://portal.harness.localhost:42013';
const challenge = { challenge_id: '00000000-0000-4000-8000-000000000002', expires_at: new Date(Date.now() + 60_000).toISOString() };
afterEach(() => {vi.useRealTimers();vi.unstubAllGlobals();});

test('startup state conflict waits for fresh readiness and uses a new challenge',async()=>{
 vi.useFakeTimers();let grants=0,checks=0,requests=0;
 const waiting=vi.fn();
 const client=new AccessClient(async(input)=>{
  const path=String(input);requests++;
  if(path.endsWith('/__dsh/challenge'))return new Response(JSON.stringify({...challenge,expires_at:new Date(Date.now()+60_000).toISOString()}));
  if(path.endsWith(':access')){
   if(++grants===1)return new Response(JSON.stringify({code:'STATE_CONFLICT'}),{status:409});
   return new Response(JSON.stringify({...challenge,ticket:'t'.repeat(43),handoff_url:`https://${host}:42013/__dsh/handoff`,expires_at:new Date(Date.now()+40_000).toISOString()}));
  }
  checks++;return new Response(JSON.stringify({...instance,can_enter:checks>1}));
 });
 const result=client.prepareReady(user,instance,portal,()=>false,waiting);
 await vi.advanceTimersByTimeAsync(250);
 expect((await result).ticket).toBe('t'.repeat(43));expect(grants).toBe(2);expect(checks).toBe(2);expect(requests).toBe(6);expect(waiting).toHaveBeenCalled();
});

test('readiness wait ends on stop, owner change, denied authorization or cancellation',async()=>{
 vi.useFakeTimers();
 for(const refreshed of [{...instance,observed_state:'STOPPED',desired_state:'STOPPED'},{...instance,owner_id:'foreign'},{...instance,allowed_actions:[]}]){
  let requests=0;
  const client=new AccessClient(async(input)=>{requests++;return String(input).endsWith('/'+id)?new Response(JSON.stringify(refreshed)):new Response(JSON.stringify({code:'STATE_CONFLICT'}),{status:409});});
  const result=client.prepareReady(user,instance,portal,()=>false,()=>{});
  const rejected=expect(result).rejects.toThrow();
  await vi.advanceTimersByTimeAsync(2000);await rejected;expect(requests).toBe(2);
 }
 let cancelled=false;const fetcher=vi.fn(async(input:RequestInfo|URL)=>String(input).endsWith('/'+id)?new Response(JSON.stringify({...instance,can_enter:false})):new Response(JSON.stringify({code:'STATE_CONFLICT'}),{status:409}));
 const result=new AccessClient(fetcher).prepareReady(user,instance,portal,()=>cancelled,()=>{});
 const rejected=expect(result).rejects.toThrow('已取消');
 await vi.advanceTimersByTimeAsync(0);cancelled=true;await vi.advanceTimersByTimeAsync(250);await rejected;expect(fetcher).toHaveBeenCalledTimes(2);
 const denied=vi.fn(async()=>new Response(JSON.stringify({code:'FORBIDDEN'}),{status:403}));
 await expect(new AccessClient(denied).prepareReady(user,instance,portal,()=>false,()=>{})).rejects.toThrow('没有此操作权限');expect(denied).toHaveBeenCalledTimes(1);
});

test('unhealthy workspace wait has a fixed deadline and never issues a handoff',async()=>{
 vi.useFakeTimers();let requests=0;
 const client=new AccessClient(async()=>{requests++;return new Response(JSON.stringify({...instance,can_enter:false}));});
 const result=client.prepareReady(user,{...instance,can_enter:false},portal,()=>false,()=>{});
 const rejected=expect(result).rejects.toThrow('仍未就绪');
 await vi.advanceTimersByTimeAsync(60000);await rejected;expect(requests).toBeGreaterThan(0);expect(requests).toBeLessThanOrEqual(35);
});

function popupFixture() {
 const values = new Map<string,string>();
 const sessionStorage = {getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);},removeItem:(key:string)=>{values.delete(key);}};
 const elements = new Map<string, {textContent: string; href?: string}>();
 const popup = { sessionStorage, opener: {}, closed: false, document: { open: vi.fn(), write: vi.fn(), close: vi.fn(), getElementById: (id: string) => { if (!elements.has(id)) elements.set(id, {textContent:''}); return elements.get(id)!; } }, close: vi.fn(), location: { replace: vi.fn() } };
 vi.stubGlobal('window', { location: { origin: portal }, open: vi.fn(() => popup) });
 return popup;
}

test('instance transport failure starts credential-free automatic continuation in the isolated window', async () => {
 const popup = popupFixture();
 const fetcher = vi.fn().mockRejectedValue(new TypeError('Failed to fetch private-upstream-detail'));
 vi.stubGlobal('fetch', fetcher);
 await enterWorkspace(user, instance);
 expect(popup.close).not.toHaveBeenCalled();
 expect(popup.opener).toBe(null);
 expect(popup.location.replace).toHaveBeenCalledWith(`https://${host}:42013/__dsh/connect`);
 expect(fetcher).toHaveBeenCalledTimes(1);
 const intent=popup.sessionStorage.getItem('hc:workspace-connect:1')!;
 expect(intent).not.toContain('csrf');expect(intent).not.toContain('ticket');
 expect(JSON.parse(intent)).toMatchObject({user:user.id,instance:id,origin:portal});
});

test('authorization rejection keeps a safe error and return action in the popup', async () => {
 const popup = popupFixture();
 vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'FORBIDDEN', message: 'private detail' }), { status: 403 })));
 await expect(enterWorkspace(user, instance)).rejects.toThrow('没有此操作权限');
 expect(popup.close).not.toHaveBeenCalled();
 expect(popup.document.getElementById('entry-description').textContent).toContain('没有此操作权限');
 expect(popup.document.getElementById('entry-return').href).toBe(portal + '/app/instances/' + id);
 expect(popup.location.replace).not.toHaveBeenCalled();
});

test('challenge timeout uses bounded connection recovery, without blaming certificates', async () => {
 const popup=popupFixture();vi.stubGlobal('fetch',vi.fn().mockRejectedValue(new DOMException('private timeout detail','TimeoutError')));
 await enterWorkspace(user,instance);
 expect(popup.close).not.toHaveBeenCalled();expect(popup.location.replace).toHaveBeenCalledOnce();
});
test('Portal transport failure does not trigger an instance trust page',async()=>{
 const popup=popupFixture();let calls=0;
 vi.stubGlobal('fetch',vi.fn(async()=>{if(++calls===1)return new Response(JSON.stringify(challenge));throw new TypeError('private-network-detail');}));
 await expect(enterWorkspace(user,instance)).rejects.toThrow('检查网络或平台状态');expect(popup.close).not.toHaveBeenCalled();expect(popup.location.replace).not.toHaveBeenCalled();
});
test('continuation rejects missing, expired, wrong-user or wrong-instance intents and consumes once',async()=>{
 const popup=popupFixture();const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);
 vi.stubGlobal('window',{location:{origin:portal},sessionStorage:popup.sessionStorage});
 for(const change of [null,{user:'other'},{instance:host},{origin:'https://foreign.test'},{expires:Date.now()-1},{expires:Date.now()+180_000}]){
  if(change)popup.sessionStorage.setItem('hc:workspace-connect:1',JSON.stringify({user:user.id,instance:id,origin:portal,expires:Date.now()+120_000,...change}));
  expect(await resumeWorkspace(user,instance)).toBe(false);expect(popup.sessionStorage.getItem('hc:workspace-connect:1')).toBe(null);
 }
 expect(fetcher).not.toHaveBeenCalled();
 popup.sessionStorage.setItem('hc:workspace-connect:1',JSON.stringify({user:user.id,instance:id,origin:portal,expires:Date.now()+120_000}));
 vi.stubGlobal('fetch',vi.fn(async()=>{throw new TypeError('private');}));
 await expect(resumeWorkspace(user,instance)).rejects.toThrow('检查网络');expect(await resumeWorkspace(user,instance)).toBe(false);
});

test('access origin is derived from the exact instance and Portal site', () => {
 expect(accessOrigin(instance, portal)).toBe(`https://${host}:42013`);
 for (const value of [`${host}.evil.test`, 'foreign.test', host.replace('0001', '0002'), host + ':18082']) expect(() => accessOrigin({ ...instance, access_host: value }, portal)).toThrow();
 expect(() => accessOrigin(instance, 'http://portal.harness.localhost:42013')).toThrow();
});
test('browser challenge uses host cookies and ticket is issued only in private JSON', async () => {
 const paths: string[] = [];
 const client = new AccessClient(async (input, init) => {
  paths.push(String(input)); expect(init?.cache).toBe('no-store'); expect(init?.redirect).toBe('error');
  if (paths.length === 1) { expect(init?.credentials).toBe('include'); expect(init?.headers).toBeUndefined(); return new Response(JSON.stringify(challenge)); }
  expect(init?.credentials).toBe('same-origin'); expect(init?.method).toBe('POST'); expect(init?.headers).toEqual({ 'Content-Type': 'application/json', 'X-CSRF-Token': user.csrf_token }); expect(JSON.parse(String(init?.body))).toEqual({ challenge_id: challenge.challenge_id });
  return new Response(JSON.stringify({ ...challenge, ticket: 't'.repeat(43), handoff_url: `https://${host}:42013/__dsh/handoff`, expires_at: new Date(Date.now() + 40_000).toISOString() }));
 });
 expect((await client.prepare(user, instance, portal)).ticket).toBe('t'.repeat(43));
 expect(paths).toEqual([`https://${host}:42013/__dsh/challenge`, `/v1/instances/${id}:access`]);
});
test('foreign handoff URL, changed challenge and raw errors are rejected', async () => {
 for (const change of [{ handoff_url: 'https://foreign.test/?token=secret' }, { challenge_id: id }, { expires_at: new Date(Date.now() - 1000).toISOString() }, { ticket: 'raw-secret' }]) {
  let calls = 0;
  const client = new AccessClient(async () => new Response(JSON.stringify(++calls === 1 ? challenge : { ...challenge, ticket: 't'.repeat(43), handoff_url: `https://${host}:42013/__dsh/handoff`, ...change })));
  await expect(client.prepare(user, instance, portal)).rejects.toThrow('访问交接响应无效');
 }
 const client = new AccessClient(async () => new Response(JSON.stringify({ code: 'FORBIDDEN', message: 'private raw upstream details' }), { status: 403 }));
 await expect(client.prepare(user, instance, portal)).rejects.not.toThrow('private raw');
});
