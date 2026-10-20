import type { CurrentUser } from './identity';
import { APIError } from './error';
import type { Page } from './resources';

export interface StorageBackup {
 id:string; status:string; created_at:string; completed_at:string|null; verified_at:string|null; expires_at:string|null; retention_policy:'MANUAL'|'LAST_7_SUCCESSFUL_DATES';
}
export interface StorageAsset {
 id:string; owner_id:string; instance_id:string; workspace_name:string; workspace_state:string; capacity_bytes:number; status:string; created_at:string;
 deleted_at:string|null; retention_until:string|null; legal_hold:boolean; retention_expired:boolean;
 usage:{status:string;used_bytes:number|null;available_bytes:number|null;sampled_at:string|null;attempted_at:string|null};
 latest_backup:StorageBackup|null; latest_verified_backup:StorageBackup|null;
 maintenance:{backup_blockers:string[];restore_blockers:string[];reclaim_blockers:string[]};
}
export interface StorageDirectoryEntry {name:string;type:'DIRECTORY'|'FILE'|'LINK'|'OTHER';size_bytes:number;modified_at:string}
export interface StorageDirectory {root:'workspace'|'dsh';path:string;entries:StorageDirectoryEntry[];next_after?:string}
export interface StorageDownloadedFile {blob:Blob;name:string}
export class StorageClient {
 private browseTails=new Map<string,Promise<void>>();
 constructor(private readonly fetcher:typeof fetch=(...args)=>fetch(...args)){}
 private queueBrowse<T>(id:string,operation:()=>Promise<T>):Promise<T>{
  const previous=this.browseTails.get(id)??Promise.resolve();const current=previous.then(operation,operation);const tail=current.then(()=>undefined,()=>undefined);this.browseTails.set(id,tail);void tail.then(()=>{if(this.browseTails.get(id)===tail)this.browseTails.delete(id);});return current;
 }
 private browseError(status:number,code:string):Error{
  if(code==='INVALID_SPEC')return new Error('数据目录请求无效，请刷新目录后重试');
  if(code==='STORAGE_BUSY')return new Error('数据盘正在处理其他读取请求，请稍后重试');
  return new APIError(status,code);
 }
 private async read<T>(path:string,signal?:AbortSignal,timeoutMs=10_000):Promise<T>{
  const timeout=AbortSignal.timeout(timeoutMs);
  const response=await this.fetcher(path,{credentials:'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,timeout]):timeout});
  if(!response.ok){const problem=await response.json().catch(()=>({}));throw path.includes('/files?')?this.browseError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE'):new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');}
  return response.json();
 }
 private asset(value:StorageAsset,user:CurrentUser){
  if(!value||value.owner_id!==user.id||typeof value.id!=='string'||typeof value.workspace_name!=='string'||!Number.isSafeInteger(value.capacity_bytes)||value.capacity_bytes<=0||!value.usage||!value.maintenance||!['backup_blockers','restore_blockers','reclaim_blockers'].every(key=>Array.isArray(value.maintenance[key as keyof StorageAsset['maintenance']])))throw new Error('存储资产数据无效，请重新加载');
  return value;
 }
 async release(user:CurrentUser,id:string,name:string){
  let response:Response;
  try{response=await this.fetcher(`/v1/storage-assets/${encodeURIComponent(id)}:release`,{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':user.csrf_token},body:JSON.stringify({confirmed_name:name,confirmed_asset_id:id}),signal:AbortSignal.timeout(10_000)});}catch{throw new Error('释放结果待确认，请刷新查看；重复确认会核对同一申请');}
  if(!response.ok){const problem=await response.json().catch(()=>({}));const messages:Record<string,string>={STORAGE_PROTECTED:'数据受法律保留保护，暂不能释放',STORAGE_RELEASE_BLOCKED:'当前数据暂不能释放，请刷新查看',CONFIRMATION_MISMATCH:'工作空间名称不匹配'};if(messages[problem.code])throw new Error(messages[problem.code]);throw new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');}
  const result=await response.json();if(response.status!==202||result.asset_id!==id||typeof result.request_id!=='string'||!['ACCEPTED','RUNNING','UNCERTAIN','SUCCEEDED'].includes(result.status))throw new Error('释放结果待确认，请刷新查看');return result;
 }
 async assets(user:CurrentUser,filters:{page:number;limit:number;status:string;q:string},signal?:AbortSignal){
  const query=new URLSearchParams({page:String(filters.page),limit:String(filters.limit)});if(filters.status)query.set('status',filters.status);if(filters.q)query.set('q',filters.q);
  const result=await this.read<Page<StorageAsset>>('/v1/storage-assets?'+query,signal);
  if(!Array.isArray(result.items)||!Number.isSafeInteger(result.total)||(result.total??-1)<0)throw new Error('存储资产列表无效，请重新加载');
  result.items.forEach(item=>this.asset(item,user));return result;
 }
 async details(user:CurrentUser,id:string,signal?:AbortSignal){return this.asset(await this.read<StorageAsset>('/v1/storage-assets/'+encodeURIComponent(id),signal),user);}
 async backups(id:string,page:number,limit:number,signal?:AbortSignal){
  const result=await this.read<Page<StorageBackup>>(`/v1/storage-assets/${encodeURIComponent(id)}/backups?page=${page}&limit=${limit}`,signal);
  if(!Array.isArray(result.items)||!Number.isSafeInteger(result.total)||(result.total??-1)<0)throw new Error('备份记录无效，请重新加载');return result;
 }
 async directory(id:string,root:'workspace'|'dsh',path:string,after:string,signal?:AbortSignal){
  return this.queueBrowse(id,async()=>{
   const query=new URLSearchParams({root});if(path)query.set('path',path);if(after)query.set('after',after);
   const result=await this.read<StorageDirectory>(`/v1/storage-assets/${encodeURIComponent(id)}/files?${query}`,signal,70_000);
   if(result.root!==root||result.path!==path||!Array.isArray(result.entries)||result.entries.length>100||result.entries.some(entry=>typeof entry.name!=='string'||!entry.name||entry.name==='.'||entry.name==='..'||new TextEncoder().encode(entry.name).length>255||/[\\/\u0000]/.test(entry.name)||!['DIRECTORY','FILE','LINK','OTHER'].includes(entry.type)||!Number.isSafeInteger(entry.size_bytes)||entry.size_bytes<0||typeof entry.modified_at!=='string'||!Number.isFinite(Date.parse(entry.modified_at)))||result.next_after!==undefined&&(typeof result.next_after!=='string'||!result.next_after||new TextEncoder().encode(result.next_after).length>255||result.entries.at(-1)?.name!==result.next_after))throw new Error('数据目录响应无效，请重新加载');
   return result;
  });
 }
 async file(id:string,root:'workspace'|'dsh',path:string,mode:'PREVIEW'|'DOWNLOAD',signal?:AbortSignal):Promise<StorageDownloadedFile>{
  return this.queueBrowse(id,async()=>{
   const query=new URLSearchParams({root,path,mode});
   const timeout=AbortSignal.timeout(70_000);
   const response=await this.fetcher(`/v1/storage-assets/${encodeURIComponent(id)}/file?${query}`,{credentials:'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,timeout]):timeout});
   if(!response.ok){const problem=await response.json().catch(()=>({}));throw this.browseError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');}
   const blob=await response.blob();
   if(blob.size>(mode==='PREVIEW'?512*1024:64*1024*1024))throw new Error('文件超过读取大小限制');
   return {blob,name:path.split('/').at(-1)??'file'};
  });
 }
}
