import {useEffect,useState} from 'react';
import {useSearchParams} from 'react-router-dom';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Collapse,Dropdown,Empty,Form,Input,Modal,Segmented,Select,Spin,Switch,Tag} from 'antd';
import {ArrowLeft,Ellipsis,ExternalLink,Plus} from 'lucide-react';
import {type Connector,type ConnectorList,type MCPEndpoint,type ConnectorPreset} from '../../api/connectors';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {APIError} from '../../api/error';
import {expireSession,useExpiredSession} from '../../shared/hooks/use-expired-session';
import {ListSearch} from '../../shared/components/list-search';
import {DataPagination,usePagePagination} from '../../shared/components/data-pagination';
import {ConnectorDetails} from './connector-details';
import {ConnectorCatalogPicker,ConnectorPresetSetup} from './connector-catalog';
import './connectors.css';
import {client,date,errorText,StatusTag} from './connector-shared';
import {authorizeConnector} from './connector-oauth';

export function ConnectorsPanel({user}:{user:CurrentUser}){
 const cache=useQueryClient();const scope=['connectors',user.id];const list=useQuery({queryKey:scope,queryFn:({signal})=>client.request<ConnectorList>(user,'',undefined,'GET',signal),retry:false});useExpiredSession(list.error);
 const manage=hasPermission(user,'connectors:manage');const [search,setSearch]=useState('');const [busy,setBusy]=useState('');const [error,setError]=useState('');const [selected,setSelected]=useState('');const [editor,setEditor]=useState<Connector|'new'|null>(null);const [duplicate,setDuplicate]=useState<Connector|null>(null);const [deleting,setDeleting]=useState<Connector|null>(null);const [confirmedName,setConfirmedName]=useState('');
 const [catalogOpen,setCatalogOpen]=useState(false);const [preset,setPreset]=useState<ConnectorPreset|null>(null);
 const [filter,setFilter]=useState('全部');const page=usePagePagination(JSON.stringify([search,filter]));const items=(list.isError?[]:list.data?.items??[]).filter(v=>(filter==='全部'||v.enabled===(filter==='已启用'))&&(v.name+' '+v.description).toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));const visible=items.slice((page.page-1)*page.pageSize,page.page*page.pageSize);const connection=list.data?.items.find(v=>v.id===selected);
 function edit(value:Connector|'new',copy:Connector|null=null,template:ConnectorPreset|null=null){const connection=value==='new'?copy:value;const catalog=connection?.catalog_id??list.data?.endpoints.find(e=>e.id===connection?.endpoint_id)?.catalog_id;setPreset(template??list.data?.catalog?.entries.find(v=>v.id===catalog)??null);setDuplicate(copy);setEditor(value);}
 async function refresh(){await cache.invalidateQueries({queryKey:scope});}
 async function run(id:string,task:()=>Promise<void>){setBusy(id);setError('');try{await task();await refresh();}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorText(cause));}finally{setBusy('');}}
 return <section className="instances-section connectors-panel" aria-label="连接器管理">
  <div className="provider-page-heading workspace-actions"><h2>连接器</h2>{manage&&<Button type="primary" icon={<Plus size={16} aria-hidden="true"/>} disabled={!list.data||list.isError} onClick={()=>setCatalogOpen(true)}>添加连接器</Button>}</div>
  <OAuthFeedback/>
  <div className="workspace-toolbar"><Segmented aria-label="连接器状态筛选" options={['全部','已启用','已停用']} value={filter} onChange={value=>setFilter(String(value))}/><ListSearch value={search} onChange={setSearch} onRefresh={()=>void list.refetch()} loading={list.isFetching} placeholder="搜索名称或说明" label="搜索连接器" refreshLabel="刷新连接器列表"/></div>
  {(error||list.isError)&&<Alert type="error" showIcon role="alert" message={error||errorText(list.error)} action={<Button onClick={()=>void list.refetch()}>重试</Button>}/>}
  <Spin spinning={list.isPending}><div className="workspace-list" aria-label="连接器列表">{visible.length?visible.map(v=><article className="workspace-record" key={v.id} aria-label={`连接器 ${v.name}`}>
   <span className="workspace-symbol" aria-hidden="true"><ExternalLink size={20}/></span>
   <div className="workspace-record-content"><div className="workspace-record-heading"><h3><Button type="link" onClick={()=>setSelected(v.id)}>{v.name}</Button></h3>{v.enabled?<StatusTag status={v.status}/>:<Tag>已停用</Tag>}</div>{v.description&&<p className="workbench-record-description">{v.description}</p>}<div className="workspace-record-meta"><span>{v.endpoint_id==='personal'?v.url:list.data?.endpoints.find(e=>e.id===v.endpoint_id)?.name??'服务配置已失效'}</span><span>工具已启用 {v.tools.filter(t=>t.enabled).length} / {v.tools.length}</span><span>最近验证：{date(v.verified_at)}</span></div>{v.enabled&&v.server_info.verification_error&&<p className="connector-note">{v.server_info.verification_error}</p>}</div>
   <div className="workbench-record-actions"><Button className="workspace-details" onClick={()=>setSelected(v.id)}>工具与记录</Button>{manage&&<><Button disabled={!v.enabled||!!busy} loading={busy===v.id} onClick={()=>void run(v.id,async()=>{if(v.auth_type==='oauth2'&&v.status==='AUTH_REQUIRED'){await authorizeConnector(user,v);}else{await client.request(user,`/${v.id}/sync`,{expected_version:v.version});}})}>{v.auth_type==='oauth2'&&v.status==='AUTH_REQUIRED'?'授权连接':'验证'}</Button><Dropdown trigger={['click']} menu={{items:[...(v.auth_type==='oauth2'?[{key:'oauth',label:'重新授权',disabled:!v.enabled}]:[]),{key:'settings',label:'设置'},{key:'copy',label:'复制'},{key:'delete',label:'删除',danger:true}],onClick:({key})=>{if(key==='oauth'){void run(v.id,()=>authorizeConnector(user,v));}else if(key==='settings'){edit(v);}else if(key==='copy'){edit('new',v);}else if(key==='delete'){setConfirmedName('');setDeleting(v);}}}}><Button aria-label={`${v.name} 更多操作`} disabled={!!busy} icon={<Ellipsis size={17} aria-hidden="true"/>}/></Dropdown></>}</div>
  </article>):<div className="workspace-list-empty"><ExternalLink size={28} aria-hidden="true"/><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={list.isError?'未能获取连接器':search||filter!=='全部'?'没有符合条件的连接器':'暂无连接器'}/></div>}</div></Spin>
  <DataPagination label="连接器" pagination={page} count={visible.length} total={items.length} loading={list.isFetching} failed={list.isError}/>
  {catalogOpen&&list.data&&<ConnectorCatalogPicker catalog={list.data.catalog??{version:1,source_url:'https://github.com/TencentCloud/Octop/blob/main/docs/connectors.md',entries:[]}} endpoints={list.data.endpoints} onClose={()=>setCatalogOpen(false)} onSelect={template=>{setCatalogOpen(false);edit('new',null,template);}}/>}
  {editor&&<ConnectorEditor key={(editor==='new'?'new':editor.id)+':'+(preset?.id??'custom')} user={user} value={editor} duplicate={duplicate} preset={preset} endpoints={list.data?.endpoints??[]} onCustom={()=>edit('new')} onBack={editor==='new'?()=>{setEditor(null);setCatalogOpen(true);}:undefined} onClose={()=>{setEditor(null);void refresh();}} onSaved={async()=>{setEditor(null);await refresh();}}/>}
  {connection&&<ConnectorDetails user={user} connection={connection} onClose={()=>setSelected('')} onRefresh={refresh}/>}
  <Modal className="connector-modal" open={!!deleting} title="删除连接器？" okText="删除连接器" cancelText="取消" confirmLoading={busy==='delete'} okButtonProps={{danger:true,disabled:confirmedName!==deleting?.name}} onCancel={()=>{if(busy!=='delete')setDeleting(null);}} onOk={()=>deleting&&void run('delete',async()=>{await client.request(user,`/${deleting.id}`,{expected_version:deleting.version,confirmed_name:confirmedName},'DELETE');setDeleting(null);})}>
   <p>移除此连接及工具授权，外部系统的数据将保留。在途调用可能已经执行。</p><label htmlFor="connector-delete-name">输入连接器名称「{deleting?.name}」确认</label><Input id="connector-delete-name" value={confirmedName} onChange={e=>setConfirmedName(e.target.value)}/>{error&&<Alert role="alert" type="error" message={error}/>}
  </Modal>
 </section>;
}

function ConnectorEditor({user,value,duplicate,preset,endpoints:allEndpoints,onCustom,onBack,onClose,onSaved}:{user:CurrentUser;value:Connector|'new';duplicate:Connector|null;preset:ConnectorPreset|null;endpoints:MCPEndpoint[];onCustom:()=>void;onBack?:()=>void;onClose:()=>void;onSaved:()=>Promise<void>}){
 const endpoints=preset?allEndpoints.filter(e=>e.catalog_id===preset.id):allEndpoints;
 const cache=useQueryClient();const [form]=Form.useForm();const authType=Form.useWatch('auth_type',form);const enabled=Form.useWatch('enabled',form);const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [saved,setSaved]=useState<Connector|null>(null);const creating=value==='new';const previous=creating?duplicate:value;const address=previous?.url??allEndpoints.find(e=>e.id===previous?.endpoint_id)?.url??'';
 const platformEndpoint=previous?endpoints.find(e=>e.id===previous.endpoint_id||previous.endpoint_id==='personal'&&!!preset&&e.url===address):undefined;
 const platform=previous?previous.endpoint_id!=='personal'||!!platformEndpoint:!!preset;const unavailable=platform&&endpoints.length===0;
 const target=platform?{endpoint_id:previous?platformEndpoint?.id??previous.endpoint_id:endpoints.length===1?endpoints[0].id:undefined}:{url:address};
 const initial={...(previous?{name:previous.name+(creating?' 副本':''),description:previous.description,enabled:previous.enabled,auth_type:previous.auth_type,protocol_version:previous.protocol_version}:{enabled:true,auth_type:preset?.auth_kind==='oauth2'&&endpoints.some(e=>e.id.startsWith('builtin-'))?'oauth2':'none',protocol_version:preset?.auth_kind==='oauth2'&&endpoints.some(e=>e.id.startsWith('builtin-'))?'2025-11-25':'2026-07-28',...(preset?{name:preset.name,description:preset.description}:{})}),...target,credential:''};
 async function save(){try{const fields=await form.validateFields();setBusy(true);setError('');const existing=creating?saved:value;const connection=await client.request<Connector>(user,existing?`/${existing.id}`:'',{...fields,credential:fields.auth_type==='bearer'?fields.credential??'':'',catalog_id:preset?.id??'',expected_version:existing?.version??0},existing?'PUT':'POST');setSaved(connection);if(fields.auth_type==='oauth2'&&fields.enabled){await authorizeConnector(user,connection);}else{await onSaved();}}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);if(cause instanceof Error)setError(errorText(cause));}finally{setBusy(false);}}
 return <Modal className="connector-modal connector-editor" width={600} open title={creating?'添加连接器':'连接器设置'} okText={authType==='oauth2'&&enabled?'授权连接':'保存'} cancelText="取消" confirmLoading={busy} okButtonProps={{disabled:unavailable}} onCancel={()=>{if(!busy)onClose();}} onOk={()=>void save()}>
  {onBack&&<Button className="connector-editor-back" type="text" icon={<ArrowLeft size={15} aria-hidden="true"/>} disabled={busy} onClick={onBack}>返回选择</Button>}
  {error&&<Alert type="error" role="alert" showIcon message={error}/>}{preset&&<ConnectorPresetSetup preset={preset} configured={!unavailable}/>} {unavailable?<>{!preset&&<Alert type="info" showIcon message="平台服务暂不可用" description="请联系管理员检查配置。"/>}{creating&&<Button onClick={onCustom}>自定义 MCP</Button>}</>:<><Form form={form} layout="vertical" initialValues={initial} disabled={busy}>
   <Form.Item name="name" label="连接器名称" rules={[{required:true,whitespace:true,max:100,message:'请输入1～100字符名称'}]}><Input maxLength={100}/></Form.Item>
   {platform?<><Form.Item name="endpoint_id" label="平台服务" rules={[{required:true,message:'请选择平台服务'},{validator:(_,id)=>endpoints.some(e=>e.id===id)?Promise.resolve():Promise.reject(new Error('所选平台服务已失效，请重新选择'))}]}><Select virtual={false} placeholder="选择平台服务" options={endpoints.map(e=>({value:e.id,label:e.name}))}/></Form.Item><Form.Item noStyle shouldUpdate>{()=>{const endpoint=endpoints.find(e=>e.id===form.getFieldValue('endpoint_id'));return <Form.Item label="服务地址" htmlFor="connector-platform-url" extra="由平台统一配置"><Input id="connector-platform-url" readOnly value={endpoint?.url??''} placeholder="选择服务后显示"/></Form.Item>;}}</Form.Item></>:<Form.Item name="url" label="服务地址" extra="填写自定义 MCP 服务的 HTTPS 地址。" rules={[{required:true,whitespace:true,message:'请输入 MCP 服务地址'},{type:'url',message:'请输入完整的服务地址'}]}><Input maxLength={1000} placeholder="https://mcp.example.com/mcp"/></Form.Item>}
   <Form.Item name="auth_type" label="认证方式"><Select virtual={false} options={[{value:'none',label:'无需认证'},{value:'bearer',label:'Bearer Token'},{value:'oauth2',label:'OAuth 2.0 授权'}]}/></Form.Item>
   <Form.Item noStyle shouldUpdate>{()=>{const currentAddress=platform?endpoints.find(e=>e.id===form.getFieldValue('endpoint_id'))?.url:form.getFieldValue('url');return form.getFieldValue('auth_type')==='bearer'?<Form.Item name="credential" label="服务凭据" extra={creating?'凭据加密保存，仅本人可使用，不会读回。':'服务不变时留空保留原凭据；更换服务须重新填写。'} rules={creating||currentAddress!==address?[{required:true,message:'请输入此服务的 Bearer 凭据'}]:[]}><Input.Password autoComplete="new-password" maxLength={4096}/></Form.Item>:null;}}</Form.Item>
   <Collapse className="connector-editor-advanced" ghost items={[{key:'advanced',label:'更多设置',forceRender:true,children:<><Form.Item name="protocol_version" label="协议版本" rules={[{required:true}]}><Select virtual={false} options={['2026-07-28','2025-11-25'].map(v=>({value:v,label:v+(v==='2025-11-25'?'（兼容）':'')}))}/></Form.Item><Form.Item name="description" label="说明"><Input.TextArea rows={2} maxLength={1000}/></Form.Item></>}]}/>
   <div className="connector-editor-enabled"><label htmlFor="connector-enabled">启用连接器</label><Form.Item name="enabled" valuePropName="checked"><Switch id="connector-enabled"/></Form.Item></div>
  </Form><p className="connector-editor-note">{authType==='oauth2'?'仅本人可见。授权后自动验证连接，工具需自行选择启用。':'仅本人可见。保存后验证连接，再选择需要的工具。'}</p></>}
 </Modal>;
}

function OAuthFeedback(){
 const [params,setParams]=useSearchParams();const [outcome,setOutcome]=useState('');
 useEffect(()=>{const value=params.get('connector_oauth');if(value){setOutcome(value);const next=new URLSearchParams(params);next.delete('connector_oauth');setParams(next,{replace:true});}},[params,setParams]);
 if(!outcome)return null;
 const messages:Record<string,string>={ready:'授权成功，连接已就绪。请选择需要启用的工具。',verify_failed:'授权已保存，连接验证未通过，请查看连接状态并重试验证。',cancelled:'已取消授权，可随时重新连接。',failed:'授权未完成或请求已失效，请重新授权。'};
 return <Alert role="status" showIcon closable onClose={()=>setOutcome('')} type={outcome==='ready'?'success':outcome==='cancelled'?'info':'warning'} message={messages[outcome]??messages.failed}/>;
}
