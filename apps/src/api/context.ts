import {APIError} from './error';
import type {CurrentUser} from './identity';

export type ContextKind = 'memory'|'knowledge';
export interface ContextConnection {id:string;kind:ContextKind;backend:string;endpoint_id:string;name:string;version:number;enabled:boolean;status:'UNVERIFIED'|'READY'|'FAILED';capabilities:Record<string,unknown>;verified_at:string|null;updated_at:string;integration:'NOT_CONNECTED'}
export interface ContextEndpoint {id:string;name:string;backend:string;data_use:string}
export interface ContextBinding {instance_id:string;connection_id:string;version:number;enabled:boolean;integration:string}
export interface ContextList {items:ContextConnection[];endpoints:ContextEndpoint[];bindings:ContextBinding[];integration:string}
export interface ContextRecord {id:string;title:string;content?:string;type:string;source:string;scope:string;version:number;status:string;created_at:string;updated_at:string;snippet?:string;position?:string;score?:number;conflict?:boolean}
export interface ContextPreview {token:string;items:ContextRecord[];warnings:string[];data_use:string;expires_in:number}
export class ContextClient {
 constructor(private readonly fetcher:typeof fetch=(...args)=>fetch(...args)){}
 async request<T>(user:CurrentUser,kind:ContextKind,path='',body?:unknown,method=body===undefined?'GET':'POST',signal?:AbortSignal):Promise<T>{
  const timeout=AbortSignal.timeout(22_000);
  const response=await this.fetcher(`/v1/context/${kind}${path}`,{method,credentials:'same-origin',cache:'no-store',headers:body===undefined?undefined:{'Content-Type':'application/json','X-CSRF-Token':user.csrf_token},body:body===undefined?undefined:JSON.stringify(body),signal:signal?AbortSignal.any([signal,timeout]):timeout});
  if(!response.ok){const problem=await response.json().catch(()=>({}));const error=new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');if(['CONTEXT_INVALID','CONTEXT_UNAVAILABLE','CONTEXT_DISABLED','CONTEXT_UNSUPPORTED','CONTEXT_PENDING','ENDPOINT_NOT_APPROVED','NOT_FOUND','VERSION_CONFLICT'].includes(problem.code)&&typeof problem.message==='string')error.message=problem.message;throw error;}
  return response.json();
 }
 list(user:CurrentUser,kind:ContextKind,signal?:AbortSignal){return this.request<ContextList>(user,kind,'',undefined,undefined,signal);}
 call<T>(user:CurrentUser,connection:ContextConnection,action:string,body:Record<string,unknown>={}){return this.request<T>(user,connection.kind,`/${connection.id}/${action}`,{expected_version:connection.version,...body});}
}
