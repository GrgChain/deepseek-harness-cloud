import {useState} from 'react';
import {useMutation,useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Empty,Modal,Space,Spin,Tag} from 'antd';
import {ChevronDown,ChevronRight,Plug,Plus,Trash2} from 'lucide-react';
import {isPersonalModelUsable} from '../../api/personal-models';
import {ProviderAccountsClient,type ProviderAccount,type ProviderAccountModel} from '../../api/provider-accounts';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {ProviderAccountEditor} from './provider-account-editor';
import {ProviderLogo} from '../../shared/components/provider-logo';
import {DataPagination,usePagePagination} from '../../shared/components/data-pagination';
import {useTransientNotice} from '../../shared/hooks/use-transient-notice';
import {ListSearch} from '../../shared/components/list-search';

const client=new ProviderAccountsClient();
function modelStatus(model:ProviderAccountModel){
 if(!model.enabled)return '已撤销';
 if(['PENDING_SYNC','SYNCING'].includes(model.sync_status))return '正在同步';
 if(model.sync_status==='REVOKING')return '正在撤销';
 if(model.sync_status==='FAILED'||model.gateway_check?.status==='UNHEALTHY')return '连接异常';
 if(model.validation?.status==='CHECKING')return '正在校验';
 if(model.validation?.status==='UNHEALTHY')return '校验失败';
 if(isPersonalModelUsable(model))return '可用';
 if(model.sync_status==='UNCERTAIN')return '等待同步确认';
 return model.gateway_check?.status==='HEALTHY'?(model.validation?.checked_at?'待校验':'等待自动校验'):'等待网关核对';
}
function validationFailure(model:ProviderAccountModel){
 if(model.validation?.status!=='UNHEALTHY')return undefined;
 const reasons:Record<string,string>={MODEL_AUTH_REJECTED:'密钥无效或无模型访问权限',MODEL_REQUEST_REJECTED:'模型服务拒绝请求，请检查模型名称',MODEL_TOOL_UNAVAILABLE:'模型未通过工具调用校验',MODEL_PROTOCOL_INVALID:'模型响应不符合所需协议',MODEL_TRANSPORT_UNAVAILABLE:'无法连接模型服务',MODEL_UPSTREAM_UNAVAILABLE:'模型服务暂不可用',MODEL_RATE_LIMITED:'模型服务请求限流',PROBE_BUDGET_EXCEEDED:'校验预算已用尽，请稍后重试'};
 return reasons[model.validation.reason_code]??'模型校验未通过，请检查账号配置后重试';
}
export function ModelProvidersPanel({user}:{user:CurrentUser}){
 const cache=useQueryClient();
 const [query,setQuery]=useState('');const [expanded,setExpanded]=useState<string[]>([]);
 const [editor,setEditor]=useState<{account?:ProviderAccount}>();
 const [deleting,setDeleting]=useState<ProviderAccount>();
 const [notice,showNotice]=useTransientNotice();
 const pagination=usePagePagination(JSON.stringify([user.id,query]));
 const list=useQuery({queryKey:['provider-accounts',user.id],queryFn:({signal})=>client.providerAccounts(user,signal),refetchInterval:5000,retry:false});
 const providers=useQuery({queryKey:['model-providers',user.id],queryFn:({signal})=>client.providers(user,signal),retry:false});
 const personalDefault=useQuery({queryKey:['personal-model-default',user.id],queryFn:({signal})=>client.default(user,signal),refetchInterval:5000,retry:false});
 const invalidate=()=>{for(const key of ['provider-accounts','personal-models','personal-model-default','model-catalog','settings'])void cache.invalidateQueries({queryKey:[key,user.id]});};
 const command=useMutation({mutationFn:async({model,action}:{model:ProviderAccountModel;action:'validate'|'default'})=>{if(action==='default')await client.setDefault(user,model,personalDefault.data?.version??0);else await client.action(user,model,'validate');},onSuccess:(_,vars)=>{showNotice(vars.action==='default'?'已设置提供商默认模型。':'连接校验已完成。');invalidate();}});
 const remove=useMutation({mutationFn:(account:ProviderAccount)=>client.deleteProviderAccount(user,account),onSuccess:()=>{setDeleting(undefined);showNotice('模型提供商账号已停用，后台正在清理连接。');invalidate();}});
 const items=(list.data?.items??[]).filter(item=>(item.name+' '+item.provider+' '+item.api_base+' '+item.models.map(m=>m.model).join(' ')).toLowerCase().includes(query.toLowerCase()));
 const pageItems=items.slice((pagination.page-1)*pagination.pageSize,pagination.page*pagination.pageSize);
 const providerName=(id:string)=>id==='custom-openai'||id==='custom-openai-responses'||id==='custom-anthropic'?'自定义':providers.data?.items.find(p=>p.id===id)?.name??id;
 const busy=command.isPending||remove.isPending;
 return <section className="settings-section provider-accounts-section">
  <div className="provider-page-heading"><h2>模型提供商账号</h2>{hasPermission(user,'personal-model:write')&&<Button type="primary" icon={<Plus size={16} aria-hidden="true"/>} onClick={()=>{showNotice('');setEditor({});}}>添加账号</Button>}</div>
  {notice&&<Alert className="provider-feedback" type="success" showIcon closable message={notice} onClose={()=>showNotice(null)}/>}
  {[list.error,command.error].filter(Boolean).map((error,index)=><Alert className="provider-feedback" key={index} type="error" showIcon message={error!.message} action={error===list.error?<Button onClick={()=>void list.refetch()}>重试</Button>:undefined}/>)}
  <div className="provider-list-toolbar"><ListSearch label="搜索提供商或模型" placeholder="搜索提供商或模型" value={query} onChange={setQuery} loading={list.isFetching} onRefresh={()=>void list.refetch()}/></div>
  <Spin spinning={list.isPending}><div className="provider-accounts-list">
   {!list.isPending&&!list.isError&&items.length===0&&<div className="provider-list-empty"><Plug size={32} aria-hidden="true"/><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={query?'没有匹配的提供商':'尚未添加模型提供商'}/>{!query&&hasPermission(user,'personal-model:write')&&<Button onClick={()=>setEditor({})}>添加第一个账号</Button>}</div>}
   {pageItems.map(item=>{const open=expanded.includes(item.id);const ready=item.models.filter(isPersonalModelUsable).length;return <article className="provider-account" key={item.id} aria-label={item.name}>
    <div className="provider-account-header"><Button type="text" className="provider-expand" aria-label={(open?'收起 ':'展开 ')+item.name} aria-expanded={open} onClick={()=>setExpanded(ids=>open?ids.filter(id=>id!==item.id):[...ids,item.id])} icon={open?<ChevronDown size={18} aria-hidden="true"/>:<ChevronRight size={18} aria-hidden="true"/>}/><span className="provider-symbol"><ProviderLogo provider={item.provider} size={28}/></span><div className="provider-account-title"><h3>{item.name}</h3><p>{providerName(item.provider)}<span>·</span>{item.api_base}</p></div><div className="provider-account-summary">{item.status==='DELETING'?<Tag color="orange">正在删除</Tag>:<><span>{item.models.length} 个模型</span>{ready>0&&<Tag color="green">{ready} 个可用</Tag>}</>}</div><Space className="provider-account-actions">{hasPermission(user,'personal-model:write')&&<Button disabled={busy||item.status!=='ACTIVE'} onClick={()=>{showNotice('');setEditor({account:item});}}>编辑</Button>}{hasPermission(user,'personal-model:revoke')&&<Button danger type="text" aria-label={'删除 '+item.name} icon={<Trash2 size={16} aria-hidden="true"/>} disabled={busy||item.status!=='ACTIVE'} onClick={()=>{remove.reset();setDeleting(item);}}/>}</Space></div>
    {open&&<div className="provider-account-models">{item.status==='DELETING'?<p role="status">正在撤销模型路由和访问密钥，完成后自动移除。</p>:item.models.map(model=>{const usable=isPersonalModelUsable(model);const currentDefault=item.default_model_id===model.id;return <div className="provider-connected-model" key={model.id}><div className="provider-connected-model-name"><strong>{model.model}</strong>{validationFailure(model)&&<small role="status">{validationFailure(model)}</small>}{model.in_use>0&&<small>{model.in_use} 个工作空间使用</small>}</div><Tag color={usable?'green':['连接异常','校验失败'].includes(modelStatus(model))?'red':undefined}>{modelStatus(model)}</Tag>{currentDefault&&<Tag color="blue">默认模型</Tag>}<Space className="provider-model-actions">{hasPermission(user,'personal-model:validate')&&model.validation?.status!=='CHECKING'&&!!model.validation?.checked_at&&<Button loading={command.isPending&&command.variables?.model.id===model.id} disabled={busy||!model.enabled||model.sync_status!=='SYNCED'||model.gateway_check?.status!=='HEALTHY'} onClick={()=>command.mutate({model,action:'validate'})}>{usable?'重新校验':'重试校验'}</Button>}{hasPermission(user,'personal-model:write')&&!currentDefault&&<Button disabled={busy||!usable||personalDefault.isPending||personalDefault.isError} onClick={()=>command.mutate({model,action:'default'})}>设为默认</Button>}</Space></div>})}</div>}
   </article>})}
  </div></Spin>
  <DataPagination label="模型提供商" pagination={pagination} count={pageItems.length} total={items.length} loading={list.isPending} failed={list.isError}/>
  {editor&&<ProviderAccountEditor user={user} account={editor.account} onClose={()=>setEditor(undefined)} onSaved={()=>{showNotice(editor.account?'模型提供商账号已更新。':'模型提供商账号已添加，后台将自动同步并校验模型。');setEditor(undefined);invalidate();}}/>}
  <Modal title="删除账号" open={!!deleting} onCancel={()=>{if(!remove.isPending)setDeleting(undefined);}} closable={!remove.isPending} maskClosable={!remove.isPending} keyboard={!remove.isPending} footer={<Space><Button disabled={remove.isPending} onClick={()=>setDeleting(undefined)}>取消</Button><Button danger type="primary" loading={remove.isPending} disabled={!!deleting?.models.some(m=>m.in_use>0)} onClick={()=>{if(deleting)remove.mutate(deleting);}}>删除账号</Button></Space>}>
   <p>删除“{deleting?.name}”及其 {deleting?.models.length} 个模型？访问密钥将撤销，该账号的默认模型将一并停用。</p>
   {deleting?.models.some(m=>m.in_use>0)&&<Alert type="warning" showIcon message="此提供商的模型正在被工作空间使用" description="请先更换工作空间的模型或删除相关工作空间。"/>}
   {remove.isError&&<Alert type="error" showIcon message={remove.error.message}/>}
  </Modal>
 </section>;
}
