import {APIError} from './error';
import type {CurrentUser} from './identity';

export interface SkillFile {name:string;size_bytes:number}
export interface SkillFileContent {path:string;content:string;version:number;editable:boolean}
export interface Skill {id:string;name:string;description:string;model_invocable:boolean;user_invocable:boolean;version:number;file_count:number;size_bytes:number;source_url:string;synced_at:string|null;created_at:string;updated_at:string;has_previous:boolean;content?:string;files?:SkillFile[]}
export interface SkillList {items:Skill[];total:number;page:number;limit:number}
export interface SkillPreview {token:string;source_url:string;unchanged:boolean;items:{name:string;description:string;files:SkillFile[];conflict:boolean;local_changed:boolean;changed_files:string[]}[]}
export interface HubSkill {name:string;description:string;publisher:string;url:string;downloads:number;metric?:'stars';source_url?:string}
export interface HubSearch {provider:'skills.sh'|'clawhub'|'tencent'|'skillsmp';items:HubSkill[]}

export class SkillsClient {
 constructor(private readonly fetcher:typeof fetch=(...args)=>fetch(...args)){}
 private async response(user:CurrentUser,path:string,body?:unknown,method=body===undefined?'GET':'POST',signal?:AbortSignal){
  const timeout=AbortSignal.timeout(path==='/v1/skills:source'||path.endsWith(':check-update')?127_000:22_000);
  const response=await this.fetcher(path,{method,credentials:'same-origin',cache:'no-store',headers:body===undefined?undefined:{'Content-Type':'application/json','X-CSRF-Token':user.csrf_token},body:body===undefined?undefined:JSON.stringify(body),signal:signal?AbortSignal.any([timeout,signal]):timeout});
  if(!response.ok){const problem=await response.json().catch(()=>({}));const error=new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');if(['SKILL_INVALID','SKILL_EXISTS','SKILL_LIMIT','VERSION_CONFLICT','NOT_FOUND','PAYLOAD_TOO_LARGE'].includes(problem.code)&&typeof problem.message==='string')error.message=problem.message;throw error;}
  return response;
 }
 private async request<T>(user:CurrentUser,path:string,body?:unknown,method?:string,signal?:AbortSignal):Promise<T>{return (await this.response(user,path,body,method,signal)).json();}
 list(user:CurrentUser,q:string,page:number,limit:number,signal?:AbortSignal){return this.request<SkillList>(user,'/v1/skills?'+new URLSearchParams({q,page:String(page),limit:String(limit)}),undefined,undefined,signal);}
 get(user:CurrentUser,id:string,signal?:AbortSignal){return this.request<Skill>(user,'/v1/skills/'+encodeURIComponent(id),undefined,undefined,signal);}
 file(user:CurrentUser,id:string,path:string,signal?:AbortSignal){return this.request<SkillFileContent>(user,`/v1/skills/${encodeURIComponent(id)}:file?`+new URLSearchParams({path}),undefined,undefined,signal);}
 create(user:CurrentUser,name:string,description:string){return this.request<Skill>(user,'/v1/skills',{name,description});}
 save(user:CurrentUser,skill:Skill,content:string){return this.request<Skill>(user,'/v1/skills/'+skill.id,{content,expected_version:skill.version},'PUT');}
 saveFile(user:CurrentUser,id:string,path:string,version:number,content:string){return this.request<Skill>(user,`/v1/skills/${encodeURIComponent(id)}:file`,{path,content,expected_version:version},'PUT');}
 createFile(user:CurrentUser,id:string,path:string,version:number,content:string){return this.request<Skill>(user,`/v1/skills/${encodeURIComponent(id)}:file`,{path,content,expected_version:version});}
 remove(user:CurrentUser,skill:Skill,name:string){return this.request(user,'/v1/skills/'+skill.id,{confirmed_name:name,expected_version:skill.version},'DELETE');}
 async import(user:CurrentUser,file:File){
  if(file.size>5*1024*1024||file.size===0)throw new Error('请选择不超过 5 MiB 的非空 Markdown 或 ZIP 文件');
  const data=await new Promise<string>((resolve,reject)=>{const reader=new FileReader();reader.onerror=()=>reject(new Error('文件无法读取，请重新选择'));reader.onload=()=>resolve(String(reader.result).split(',')[1]);reader.readAsDataURL(file);});
  return this.request<SkillPreview>(user,'/v1/skills:preview',{filename:file.name,content_base64:data});
 }
 source(user:CurrentUser,url:string){return this.request<SkillPreview>(user,'/v1/skills:source',{url});}
 searchHub(user:CurrentUser,provider:HubSearch['provider'],query:string,signal?:AbortSignal){return this.request<HubSearch>(user,'/v1/skills:search',{provider,query},undefined,signal);}
 check(user:CurrentUser,skill:Skill){return this.request<SkillPreview>(user,`/v1/skills/${skill.id}:check-update`,{expected_version:skill.version});}
 apply(user:CurrentUser,preview:SkillPreview,mode:'skip'|'replace'){return this.request<{applied:string[];skipped:string[]}>(user,'/v1/skills:apply',{token:preview.token,mode});}
 async download(user:CurrentUser,ids:string[],previous=false){
  const response=await this.response(user,previous?`/v1/skills/${ids[0]}:previous`:'/v1/skills:export',previous?{}:{ids});
  const blob=await response.blob();if(blob.size>5*1024*1024)throw new Error('导出文件超过 5 MiB');
  const disposition=response.headers.get('Content-Disposition')??'';const name=disposition.match(/filename="?([a-z0-9.-]+)"?/i)?.[1]??'skills-export.zip';
  return {blob,name};
 }
}
