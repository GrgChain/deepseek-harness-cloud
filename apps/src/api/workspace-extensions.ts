import {APIError} from './error';
export interface ExtensionSelection {id:string;version:number;catalog_version?:number;scopes?:string[]}
export interface WorkspaceExtensionsInput {connectors?:ExtensionSelection[];memory?:ExtensionSelection[];knowledge?:ExtensionSelection[];plugins?:{id:string;version:string}[]}
export interface WorkspacePlugin {id:string;name:string;package:string;version:string;available:boolean;reason?:string}
export interface WorkspaceExtension {kind:'connector'|'memory'|'knowledge'|'plugin';id:string;name:string;version?:number;package_version?:string;scopes?:string[];tools?:string[];status:string;reason?:string}
export interface WorkspaceSkill {id:string;name:string;version:number}
export async function readWorkspaceExtensions<T>(id:string,signal?:AbortSignal,skills=false):Promise<T>{
 const response=await fetch('/v1/workspace-extensions/'+encodeURIComponent(id)+(skills?'/skills':''),{credentials:'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,AbortSignal.timeout(10000)]):AbortSignal.timeout(10000)});
 if(!response.ok){const problem=await response.json().catch(()=>({}));throw new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');}return response.json();
}
