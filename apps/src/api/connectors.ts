import {APIError} from './error';
import type {CurrentUser} from './identity';
export interface MCPTool {name:string;description:string;input_schema:Record<string,unknown>;definition:{annotations?:{readOnlyHint?:boolean;destructiveHint?:boolean}};hash:string;enabled:boolean;change?:string}
export interface Connector {id:string;name:string;description:string;endpoint_id:string;url?:string;catalog_id?:string;protocol_version:string;auth_type:string;version:number;catalog_version:number;enabled:boolean;status:string;server_info:{name?:string;version?:string;removed_tools?:string[];verification_error?:string;capabilities?:Record<string,unknown>};tools:MCPTool[];verified_at:string|null;updated_at:string}
export interface MCPEndpoint {id:string;name:string;url:string;catalog_id?:string}
export interface ConnectorPreset {id:string;name:string;description:string;category:string;mode:string;auth_kind:string;doc_url:string;guide_url:string;setup_hint:string;requirements:string[];color:string}
export interface ConnectorCatalog {version:number;source_url:string;entries:ConnectorPreset[]}
export interface ConnectorList {items:Connector[];endpoints:MCPEndpoint[];catalog:ConnectorCatalog}
export interface MCPCall {id:string;connector_id:string;tool:string;status:string;reason:string;created_at:string;completed_at:string|null;replayed?:boolean;result?:unknown}
export class ConnectorsClient {
 async request<T>(user:CurrentUser,path='',body?:unknown,method=body===undefined?'GET':'POST',signal?:AbortSignal):Promise<T>{
  const timeout=AbortSignal.timeout(127_000);const response=await fetch('/v1/connectors'+path,{method,credentials:'same-origin',cache:'no-store',headers:body===undefined?undefined:{'Content-Type':'application/json','X-CSRF-Token':user.csrf_token},body:body===undefined?undefined:JSON.stringify(body),signal:signal?AbortSignal.any([timeout,signal]):timeout});
  if(!response.ok){const problem=await response.json().catch(()=>({}));const error=new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');if(['CONNECTOR_INVALID','CONNECTOR_UNAVAILABLE','CONNECTOR_BUSY','ENDPOINT_NOT_APPROVED','VERSION_CONFLICT','IDEMPOTENCY_CONFLICT','NOT_FOUND'].includes(problem.code)&&typeof problem.message==='string')error.message=problem.message;throw error;}return response.json();
 }
}
