import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Drawer,Empty,Form,Input,InputNumber,Modal,Space,Switch,Table,Tabs,Tag} from 'antd';
import type {Connector,MCPTool,MCPCall} from '../../api/connectors';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {APIError} from '../../api/error';
import {expireSession,useExpiredSession} from '../../shared/hooks/use-expired-session';
import {DataPagination,usePagePagination} from '../../shared/components/data-pagination';
import {client,date,errorText,StatusTag} from './connector-shared';

function isRisky(tool:MCPTool){return tool.definition.annotations?.readOnlyHint!==true||tool.definition.annotations?.destructiveHint!==false;}
export function ConnectorDetails({user,connection,onClose,onRefresh}:{user:CurrentUser;connection:Connector;onClose:()=>void;onRefresh:()=>Promise<void>}){
 const cache=useQueryClient();const manage=hasPermission(user,'connectors:manage');const invoke=hasPermission(user,'connectors:invoke');const [tab,setTab]=useState('tools');const [busy,setBusy]=useState('');const [error,setError]=useState('');const [search,setSearch]=useState('');const [tool,setTool]=useState<MCPTool|null>(null);const [result,setResult]=useState<MCPCall|null>(null);const toolsPage=usePagePagination(search);const callPage=usePagePagination(connection.id);
 const ready=connection.enabled&&connection.status==='READY';const tools=connection.tools.filter(t=>(t.name+' '+t.description).toLocaleLowerCase().includes(search.toLocaleLowerCase()));const visible=tools.slice((toolsPage.page-1)*toolsPage.pageSize,toolsPage.page*toolsPage.pageSize);
 const calls=useQuery({queryKey:['connector-calls',user.id,connection.id,callPage.page,callPage.pageSize],queryFn:({signal})=>client.request<{items:MCPCall[];total:number}>(user,`/${connection.id}/calls?page=${callPage.page}&limit=${callPage.pageSize}`,undefined,'GET',signal),retry:false,refetchInterval:query=>busy==='invoke'||query.state.data?.items.some(v=>v.status==='RUNNING')?1000:false});useExpiredSession(calls.error);
 async function refresh(){await onRefresh();await calls.refetch();}
 async function run(action:string,task:()=>Promise<void>){setBusy(action);setError('');try{await task();await refresh();}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorText(cause));}finally{setBusy('');}}
 return <Drawer open title={connection.name} width={960} onClose={onClose} className="connector-details">
  <Space wrap>{connection.enabled?<StatusTag status={connection.status}/>:<Tag>已停用</Tag>}<Tag>{connection.protocol_version}</Tag><span>{connection.server_info.name} {connection.server_info.version}</span></Space>
  {error&&<Alert type="error" role="alert" showIcon message={error}/>} {!ready&&<Alert type="warning" message={connection.server_info.verification_error||"连接尚未就绪，请启用并重新验证。"}/>}
  <Tabs activeKey={tab} onChange={setTab} items={[
   {key:'tools',label:'工具',children:<>
    <div className="connector-toolbar"><Input aria-label="搜索工具" placeholder="搜索工具" value={search} onChange={e=>setSearch(e.target.value)}/>{manage&&<Button loading={busy==='sync'} disabled={!connection.enabled||!!busy} onClick={()=>void run('sync',async()=>{await client.request(user,`/${connection.id}/sync`,{expected_version:connection.version});})}>同步工具目录</Button>}</div>
    {(connection.server_info.removed_tools?.length??0)>0&&<Alert type="info" message={'已移除工具：'+connection.server_info.removed_tools?.join('、')}/>}
    <p className="connector-note">仅勾选的工具可调用。定义变化后需重新启用，发现工具不会执行操作。</p>
    <Table<MCPTool> rowKey="name" dataSource={visible} pagination={false} scroll={{x:560}} locale={{emptyText:<Empty description="暂无工具，请先验证连接"/>}} columns={[
     {title:'工具',render:(_,t)=><div><strong>{t.name}</strong><p className="connector-description">{t.description}</p>{t.change&&<Tag color="orange">{t.change==='added'?'新工具':'定义已变更'}</Tag>}</div>},
     {title:'已启用',render:(_,t)=><Switch aria-label={'启用工具 '+t.name} checked={t.enabled} disabled={!manage||!ready||!!busy} onChange={enabled=>void run('select',async()=>{const selected=connection.tools.filter(v=>v.name===t.name?enabled:v.enabled).map(v=>v.name);await client.request(user,`/${connection.id}/tools`,{expected_version:connection.version,enabled_tools:selected});})}/>},
     {title:'操作',render:(_,t)=><Button disabled={!invoke||!ready||!t.enabled||!!busy} onClick={()=>{setTool(t);setResult(null);}}>试调用</Button>},
    ]}/><DataPagination label="工具" pagination={toolsPage} count={visible.length} total={tools.length} loading={!!busy} failed={false}/>
   </>},
   {key:'settings',label:'连接信息',children:<dl className="connector-info"><dt>服务地址</dt><dd>{connection.url||'—'}</dd><dt>认证方式</dt><dd>{connection.auth_type==='oauth2'?'OAuth 2.0 授权':connection.auth_type==='bearer'?'Bearer Token':'无需认证'}</dd><dt>说明</dt><dd>{connection.description||'—'}</dd><dt>最近验证</dt><dd>{date(connection.verified_at)}</dd><dt>服务能力</dt><dd><pre className="connector-json">{JSON.stringify(connection.server_info.capabilities??{},null,2)}</pre></dd><dt>目录版本</dt><dd>{connection.catalog_version}</dd><dt>当前配置版本</dt><dd>{connection.version}</dd></dl>},
   {key:'calls',label:'调用记录',children:<>
    <div className="connector-toolbar"><p className="connector-note">仅保存状态。参数和结果正文不持久保存，断连后请核对外部执行结果。</p><Button loading={calls.isFetching} onClick={()=>void calls.refetch()}>刷新记录</Button></div>
    {calls.isError&&<Alert role="alert" type="error" message={errorText(calls.error)}/>}
    <Table<MCPCall> rowKey="id" loading={calls.isPending} dataSource={calls.isError?[]:calls.data?.items??[]} pagination={false} scroll={{x:600}} columns={[
     {title:'工具',dataIndex:'tool'},{title:'状态',render:(_,v)=><><StatusTag status={v.status}/>{v.reason&&<p className="connector-note">{v.reason}</p>}</>},{title:'发起时间',render:(_,v)=>date(v.created_at)},
     {title:'调用标识',render:(_,v)=><code className="connector-call-id">{v.id}</code>},
     {title:'操作',render:(_,v)=>v.status==='RUNNING'&&invoke?<Button danger disabled={!!busy&&busy!=='invoke'} onClick={()=>void run('cancel',async()=>{await client.request(user,`/calls/${v.id}/cancel`,{});})}>申请取消</Button>:'—'},
    ]}/><DataPagination label="调用记录" pagination={callPage} count={calls.data?.items.length??0} total={calls.data?.total??0} loading={calls.isFetching} failed={calls.isError}/>
   </>},
  ]}/>
  {tool&&<ToolCall key={tool.name+connection.version} user={user} connection={connection} tool={tool} onClose={()=>{setTool(null);setBusy('');}} onStarted={()=>{setBusy('invoke');void calls.refetch();}} onBackground={()=>{setTool(null);setTab('calls');}} onResult={async value=>{setBusy('');setResult(value);setTool(null);await refresh();}}/>}
  <Modal className="connector-modal" open={!!result} title="工具调用结果" width={760} footer={<Button onClick={()=>setResult(null)}>关闭</Button>} onCancel={()=>setResult(null)}>
   {result&&<><StatusTag status={result.status}/><p>{result.reason}</p><p className="connector-note">调用标识：{result.id}</p>{result.result?<pre className="connector-json">{JSON.stringify(result.result,null,2)}</pre>:<p>未保留结果正文。{result.replayed?'此提交已受理，没有重复调用。':''}</p>}</>}
  </Modal>
 </Drawer>;
}

function ToolCall({user,connection,tool,onClose,onStarted,onBackground,onResult}:{user:CurrentUser;connection:Connector;tool:MCPTool;onClose:()=>void;onStarted:()=>void;onBackground:()=>void;onResult:(v:MCPCall)=>Promise<void>}){
 const cache=useQueryClient();const [text,setText]=useState('{}');const [timeout,setTimeout]=useState<number>(60);const [preview,setPreview]=useState<Record<string,unknown>|null>(null);const [confirmed,setConfirmed]=useState(false);const [riskConfirmed,setRiskConfirmed]=useState(false);const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [key]=useState(()=>crypto.randomUUID());const [uncertain,setUncertain]=useState(false);const risky=isRisky(tool);
 function check(){try{const value:unknown=JSON.parse(text);if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('参数须为JSON对象');if(new TextEncoder().encode(text).length>900_000)throw new Error('参数过大，请减少内容');setPreview(value as Record<string,unknown>);setConfirmed(false);setRiskConfirmed(false);setError('');}catch(cause){setError(errorText(cause));}}
 async function submit(){if(!preview)return;setBusy(true);setError('');onStarted();try{const value=await client.request<MCPCall>(user,`/${connection.id}/invoke`,{expected_version:connection.version,catalog_version:connection.catalog_version,tool:tool.name,arguments:preview,idempotency_key:key,confirmed,risk_confirmed:riskConfirmed,timeout_seconds:timeout});await onResult(value);}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);if(!(cause instanceof APIError)||cause.status>=500){setUncertain(true);setError('请求结果未确认，请查看调用记录并核对外部系统。不会自动重试。');}else setError(errorText(cause));}finally{setBusy(false);}}
 return <Modal className="connector-modal" open title={`试调用 · ${tool.name}`} width={720} closable={!busy} maskClosable={false} onCancel={()=>{if(!busy)onClose();}} footer={<Space>{busy&&<Button onClick={onBackground}>查看记录与取消</Button>}<Button disabled={busy} onClick={onClose}>关闭</Button>{!preview?<Button type="primary" onClick={check}>检查参数</Button>:<><Button disabled={busy||uncertain} onClick={()=>setPreview(null)}>修改参数</Button><Button type="primary" danger={risky} loading={busy} disabled={!confirmed||risky&&!riskConfirmed||uncertain} onClick={()=>void submit()}>确认调用</Button></>}</Space>}>
  {error&&<Alert role="alert" type="error" showIcon message={error}/>}
  <p>服务：{connection.name} · 工具：{tool.name}</p><p>{tool.description}</p>
  {!preview?<><details><summary>查看参数JSON Schema</summary><pre className="connector-json">{JSON.stringify(tool.input_schema,null,2)}</pre></details><Form layout="vertical"><Form.Item label="调用参数（JSON）" htmlFor="connector-arguments"><Input.TextArea id="connector-arguments" rows={10} value={text} onChange={e=>setText(e.target.value)}/></Form.Item><Form.Item label="超时（秒）" htmlFor="connector-timeout"><InputNumber id="connector-timeout" min={1} max={120} value={timeout} onChange={v=>setTimeout(v??60)}/></Form.Item></Form></>:<><pre className="connector-json">{JSON.stringify(preview,null,2)}</pre><div className="connector-confirmations"><Checkbox disabled={busy||uncertain} checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}>确认将以上参数发送到此服务</Checkbox>{risky&&<Checkbox disabled={busy||uncertain} checked={riskConfirmed} onChange={e=>setRiskConfirmed(e.target.checked)}>此工具可能修改或发送外部数据，我确认执行</Checkbox>}</div></>}
  {busy&&<p role="status">正在执行，超时不代表外部操作已撤销。</p>}
 </Modal>;
}
