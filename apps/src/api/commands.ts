import type { CurrentUser } from './identity';
import { APIError } from './error';

export type CommandAction = 'create' | 'start' | 'stop' | 'delete' | 'configure' | 'retry';
export interface Acceptance { instance_id: string; operation_id: string; state: string }
export interface ConfigurationInput { expected_instance_version: number; model_configuration_id: string; model_configuration_version: number }
export interface ModelSelection { model_configuration_id: string; model_configuration_version: number }
export type CreateInput = Partial<ModelSelection> & { name?: string; spec_id?: string };
type Submission = { schema_version: 1; action: CommandAction; target: string; key: string; created_at: number; state: 'pending' | 'accepted' | 'rejected'; uncertain?: boolean; response?: Acceptance; body?: ConfigurationInput | CreateInput | Record<string, never>; confirmed_name?: string; source_operation?: string };
type StoragePort = Pick<Storage,'getItem'|'setItem'|'removeItem'>;
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
const inFlight = new Set<string>();

// Only non-secret command facts are stored. A timeout remains unresolved until
// the same key receives an authoritative response; it never becomes a new create.
export class CommandsClient {
 constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args), private readonly storage: StoragePort | undefined = undefined) {}
 private store() { try { return this.storage ?? window.sessionStorage; } catch { throw new Error('无法保存提交状态，请恢复浏览器会话存储后重试'); } }
 private scope(user: CurrentUser, action: CommandAction, target = '') {
  if (!uuid.test(user.id) || !uuid.test(user.memberships[0]?.org_id ?? '') || (action!=='create' && !uuid.test(target))) throw new Error('提交身份或实例编号无效');
  return `hc:command:1:${user.memberships[0].org_id}:${user.id}:${action}:${target}`;
 }
 private read(scope: string, action: CommandAction, target: string): Submission | null {
  let value: unknown;
  try { const raw=this.store().getItem(scope); if(!raw)return null; value=JSON.parse(raw); } catch { throw new Error('无法读取已保存的提交状态，请先检查实例与操作记录'); }
  const s=value as Submission;
  if (!s || s.schema_version!==1 || s.action!==action || s.target!==target || !uuid.test(s.key) || !Number.isFinite(s.created_at) || s.created_at>Date.now() || !['pending','accepted','rejected'].includes(s.state)) throw new Error('已保存的提交状态无效，请先检查实例与操作记录');
  return s;
 }
 hasPending(user:CurrentUser,action:CommandAction,target='') { return this.read(this.scope(user,action,target),action,target)?.state==='pending'; }
 lastAccepted(user:CurrentUser,action:CommandAction,target='') {
  const saved=this.read(this.scope(user,action,target),action,target);
  if(saved?.state!=='accepted' || !saved.response)return null;
  if(!uuid.test(saved.response.instance_id) || !uuid.test(saved.response.operation_id) || (action!=='create' && saved.response.instance_id!==target))throw new Error('已保存的接受记录无效，请刷新实例列表');
  return saved.response;
 }
 private write(scope:string,s:Submission) {try {this.store().setItem(scope,JSON.stringify(s));} catch {throw new Error('无法保存提交状态，请恢复浏览器会话存储后重试');}}
 create(user:CurrentUser, input?: CreateInput) { return this.submit(user,'create','',input ?? {}); }
 start(user:CurrentUser,id:string) { return this.submit(user,'start',id); }
 stop(user:CurrentUser,id:string) { return this.submit(user,'stop',id); }
 delete(user:CurrentUser,id:string,name:string) { return this.submit(user,'delete',id,undefined,name); }
 retry(user:CurrentUser,id:string,operation:string) { if(!uuid.test(operation))throw new Error('操作编号无效');return this.submit(user,'retry',id,undefined,undefined,operation); }
 configure(user:CurrentUser,id:string,input:ConfigurationInput) { return this.submit(user,'configure',id,input); }
 confirmPending(user:CurrentUser,action:CommandAction,target='') {
  const saved=this.read(this.scope(user,action,target),action,target);
  if(!saved || saved.state!=='pending') return Promise.reject(new Error('没有待确认的提交，请刷新实例状态'));
  return this.submit(user,action,target,saved.body,saved.confirmed_name,saved.source_operation);
 }
 private async submit(user:CurrentUser,action:CommandAction,target:string,body?:Submission['body'],name?:string,source?:string):Promise<Acceptance> {
  const scope=this.scope(user,action,target);
  if(inFlight.has(scope))throw new Error('正在提交，请等待当前请求结果');
  inFlight.add(scope);
  try {
   const saved=this.read(scope,action,target);
   if(saved?.state==='pending' && (JSON.stringify(saved.body)!==JSON.stringify(body) || saved.confirmed_name!==name || saved.source_operation!==source))throw new Error('上次提交结果仍待确认，请先确认原请求');
   if(saved?.state==='pending' && Date.now()-saved.created_at>=7*24*60*60*1000)throw new Error('上次提交超过安全重放期限，请先核实实例及操作记录');
   const submission:Submission=saved?.state==='pending'?saved:{schema_version:1,action,target,key:crypto.randomUUID(),created_at:Date.now(),state:'pending',...(body===undefined?{}:{body}),...(name===undefined?{}:{confirmed_name:name}),...(source===undefined?{}:{source_operation:source})};
   this.write(scope,submission);
   const path=action==='retry'?`/v1/operations/${submission.source_operation}:retry`:action==='create'?'/v1/instances':`/v1/instances/${target}${action==='delete'?'':':'+action}`;
   const headers:Record<string,string>={'X-CSRF-Token':user.csrf_token,'Idempotency-Key':submission.key};
   if(body!==undefined)headers['Content-Type']='application/json';if(name!==undefined)headers['X-Confirm-Instance-Name']=encodeURIComponent(name);
   let response:Response;
   try {response=await this.fetcher(path,{method:action==='delete'?'DELETE':'POST',credentials:'same-origin',cache:'no-store',headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(10_000)});} catch {submission.uncertain=true;this.write(scope,submission);throw new Error('提交结果待确认，请使用原请求确认结果或查看实例记录');}
   if(response.status!==202) {
    const problem:{code?:unknown}=await response.json().catch(()=>({}));
    if(!submission.uncertain && response.status>=400 && response.status<500 && ![401,403,429].includes(response.status))submission.state='rejected';else submission.uncertain=true;
    this.write(scope,submission);
    throw new APIError(response.status,typeof problem.code==='string'?problem.code:'DEPENDENCY_UNAVAILABLE');
   }
   const result:Acceptance=await response.json().catch(()=>null);
   if(!result || !uuid.test(result.instance_id) || !uuid.test(result.operation_id) || (action!=='create' && result.instance_id!==target) || !['CREATING','STARTING','RUNNING','STOPPING','STOPPED','DELETING'].includes(result.state)){submission.uncertain=true;this.write(scope,submission);throw new Error('提交结果无效，请使用原请求确认结果');}
   submission.state='accepted';submission.response=result;this.write(scope,submission);return result;
  } finally {inFlight.delete(scope);}
 }
}
