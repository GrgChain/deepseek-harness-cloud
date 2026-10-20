import {PersonalModelsClient,type PersonalModel,type ModelSelection} from './personal-models';
import type {CurrentUser} from './identity';

export interface ProviderAccountModel extends PersonalModel { selection:ModelSelection; in_use:number }
export interface ProviderAccount {id:string;name:string;provider:string;api_base:string;version:number;status:'ACTIVE'|'DELETING'|'DELETED';default_model_id:string|null;credential_configured:boolean;models:ProviderAccountModel[]}
export interface ProviderAccountInput {name:string;provider:string;api_base:string;api_key?:string;models:ModelSelection[];expected_version:number;default_model?:string}

export class ProviderAccountsClient extends PersonalModelsClient {
  providerAccounts(user:CurrentUser,signal?:AbortSignal){return this.request<{items:ProviderAccount[]}>('/v1/my-provider-accounts',user,undefined,undefined,signal);}
  createProviderAccount(user:CurrentUser,input:ProviderAccountInput,key:string){return this.request<ProviderAccount>('/v1/my-provider-accounts',user,input,key);}
  updateProviderAccount(user:CurrentUser,account:ProviderAccount,input:ProviderAccountInput){return this.request<ProviderAccount>('/v1/my-provider-accounts/'+account.id,user,input,undefined,undefined,'PUT');}
  deleteProviderAccount(user:CurrentUser,account:ProviderAccount){return this.request<ProviderAccount>('/v1/my-provider-accounts/'+account.id+':delete',user,{expected_version:account.version});}
  syncProviderAccount(user:CurrentUser,account:ProviderAccount,apiKey?:string){return this.request<{items:{id:string;name:string}[]}>('/v1/my-provider-accounts/'+account.id+':sync',user,{expected_version:account.version,...(apiKey!==undefined?{api_key:apiKey}:{})});}
}
