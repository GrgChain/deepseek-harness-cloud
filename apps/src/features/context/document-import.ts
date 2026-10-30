import type {CurrentUser} from '../../api/identity';
import {APIError} from '../../api/error';

export const documentAccept='.txt,.text,.md,.markdown,.csv,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.odt,.ods,.odp,.pdf,.png,.jpg,.jpeg,.webp,.bmp,.tif,.tiff';
export const documentMaxBytes=16*1024*1024;
export function supportedDocument(name:string){return /\.(txt|text|md|markdown|csv|docx?|xlsx?|pptx?|odt|ods|odp|pdf|png|jpe?g|webp|bmp|tiff?)$/i.test(name);}

export async function extractDocumentText(user:CurrentUser,name:string,blob:Blob):Promise<string>{
 if(!supportedDocument(name))throw new Error('不支持此文件格式');
 if(!blob.size)throw new Error('文件为空');
 if(blob.size>documentMaxBytes)throw new Error('文件不能超过 16 MiB');
 const data=new FormData();data.append('file',blob,name);
 const response=await fetch('/v1/context/knowledge/parse',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'X-CSRF-Token':user.csrf_token},body:data,signal:AbortSignal.timeout(305_000)});
 if(!response.ok){const problem=await response.json().catch(()=>({}));if((problem.code==='CONTEXT_INVALID'||problem.code==='PAYLOAD_TOO_LARGE')&&typeof problem.message==='string')throw new Error(problem.message);throw new APIError(response.status,problem.code??'DEPENDENCY_UNAVAILABLE');}
 const result=await response.json() as {content:string};return result.content;
}
