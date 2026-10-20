import {useEffect,useRef,useState} from 'react';
import {useMutation,useQuery} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Collapse,Drawer,Empty,Form,Input,InputNumber,Select,Space,Spin,Tag} from 'antd';
import {Plus,RefreshCw,Search,X} from 'lucide-react';
import type {ModelSelection} from '../../api/personal-models';
import {ProviderAccountsClient,type ProviderAccount,type ProviderAccountInput} from '../../api/provider-accounts';
import type {CurrentUser} from '../../api/identity';
import {ProviderLabel} from '../../shared/components/provider-logo';

const client=new ProviderAccountsClient();
const validModel=/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const customProviderIds=['custom-openai','custom-openai-responses','custom-anthropic'];
const protocolName=(id:string)=>id==='custom-openai'?'OpenAI Chat Completions':id==='custom-openai-responses'?'OpenAI Responses':'Anthropic Messages';
export function ProviderAccountEditor({user,account,onClose,onSaved}:{user:CurrentUser;account?:ProviderAccount;onClose:()=>void;onSaved:()=>void}){
 const [name,setName]=useState(account?.name??'');
 const [provider,setProvider]=useState(account?.provider);
 const [base,setBase]=useState(account?.api_base??'');
 const [key,setKey]=useState('');
 const keyRef=useRef('');keyRef.current=key;
 useEffect(()=>()=>{keyRef.current='';},[]);
 const [noAuth,setNoAuth]=useState(false);
 const [selected,setSelected]=useState<string[]>(account?.models.map(m=>m.model)??[]);
 const [preferred,setPreferred]=useState(account?.models.find(m=>m.id===account.default_model_id)?.model);
 const defaultModel=preferred&&selected.includes(preferred)?preferred:selected[0];
 const [directory,setDirectory]=useState<{id:string;name:string}[]>([]);
 const [search,setSearch]=useState('');
 const [selectedOnly,setSelectedOnly]=useState(false);
 const [manual,setManual]=useState('');
 const [manualError,setManualError]=useState('');
 const [contextWindow,setContextWindow]=useState(131072);
 const [outputLimit,setOutputLimit]=useState(16384);
 const receipt=useRef(crypto.randomUUID());const previousDigest=useRef('');
 const providers=useQuery({queryKey:['model-providers',user.id],queryFn:({signal})=>client.providers(user,signal),retry:false});
 const custom=customProviderIds.includes(provider??'');
 const customProviders=providers.data?.items.filter(p=>customProviderIds.includes(p.id))??[];
 const providerOptions=(providers.data?.items.filter(p=>!customProviderIds.includes(p.id)).map(p=>({value:p.id,label:p.name,title:p.name}))??[]).concat(customProviders.length?[{value:'custom',label:'自定义',title:'自定义'}]:[]);
 const responsesProviderAvailable=providers.data?.items.some(p=>p.id==='custom-openai-responses')??false;
 const providerInfo=providers.data?.items.find(p=>p.id===provider);
 const effectiveKey=custom&&noAuth?'':keyRef.current;
 const sync=useMutation({mutationFn:()=>account?client.syncProviderAccount(user,account,(custom&&noAuth)||keyRef.current?effectiveKey:undefined):client.syncModels(user,provider!,{api_base:base,api_key:effectiveKey}),onSuccess:data=>setDirectory(data.items)});
 const existing=new Map(account?.models.map(m=>[m.model,m])??[]);
 const choices=new Map<string,{id:string;name:string}>();
 for(const item of directory)choices.set(item.id,item);
 for(const model of account?.models??[])if(!choices.has(model.model))choices.set(model.model,{id:model.model,name:model.model});
 for(const id of selected)if(!choices.has(id))choices.set(id,{id,name:id});
 const visible=Array.from(choices.values()).filter(m=>(!selectedOnly||selected.includes(m.id))&&(m.id+' '+m.name).toLowerCase().includes(search.toLowerCase()));
 const removed=account?.models.filter(m=>!selected.includes(m.model))??[];
 const blocked=removed.filter(m=>m.in_use>0);
 const rotation=!!account&&(!!key||custom&&noAuth);
 const keyReady=custom?(noAuth||!!key||!!account?.credential_configured):!!key||!!account?.credential_configured;
 const canSave=!!name.trim()&&!!providerInfo&&!!base&&keyReady&&selected.length>0&&selected.length<=20&&selected.every(m=>validModel.test(m))&&blocked.length===0&&outputLimit<=contextWindow&&(!!account||custom||sync.isSuccess);
 const save=useMutation({mutationFn:async()=>{
  const models:ModelSelection[]=selected.map(model=>existing.get(model)?.selection??{provider:provider!,api_base:base,model,...(custom?{context_window:contextWindow,max_output_tokens:outputLimit}:{})});
  const input:ProviderAccountInput={name:name.trim(),provider:provider!,api_base:base,models,default_model:defaultModel,expected_version:account?.version??0,...(!account||key||custom&&noAuth?{api_key:effectiveKey}:{})};
  if(account)return client.updateProviderAccount(user,account,input);
  const bytes=new TextEncoder().encode(JSON.stringify(input));const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),b=>b.toString(16).padStart(2,'0')).join('');bytes.fill(0);
  if(previousDigest.current&&previousDigest.current!==digest)receipt.current=crypto.randomUUID();previousDigest.current=digest;
  return client.createProviderAccount(user,input,receipt.current);
 },onSuccess:onSaved,onSettled:()=>{setKey('');keyRef.current='';}});
 const busy=save.isPending||sync.isPending;
 const changeProvider=(value:string)=>{const id=value==='custom'?(customProviders.find(p=>p.id==='custom-openai')?.id??customProviders[0]?.id):value;setProvider(id);const info=providers.data?.items.find(p=>p.id===id);if(!name)setName(id?.startsWith('custom-')?'自定义':info?.name??'');setBase(id?.startsWith('custom-')?'':info?.api_bases[0]??'');setSelected([]);setDirectory([]);setSearch('');setSelectedOnly(false);setKey('');keyRef.current='';setNoAuth(!!id?.startsWith('custom-'));sync.reset();save.reset();};
 const toggle=(id:string,checked:boolean)=>setSelected(values=>checked?values.includes(id)?values:[...values,id]:values.filter(value=>value!==id));
 const addManual=()=>{const id=manual.trim();if(!validModel.test(id)){setManualError('请输入有效的模型 ID，仅支持英文、数字和 . _ : / -');return;}if(selected.length>=20&&!selected.includes(id)){setManualError('最多选择20个模型');return;}toggle(id,true);setManual('');setManualError('');};
 return <Drawer className="provider-account-editor" title={account?'编辑账号':'添加账号'} width={640} open onClose={()=>{if(!busy){keyRef.current='';onClose();}}} closable={!busy} maskClosable={!busy} keyboard={!busy} destroyOnHidden footer={<div className="provider-account-editor-footer"><span>{selected.length} 个模型</span><Space><Button disabled={busy} onClick={onClose}>取消</Button><Button type="primary" htmlType="submit" form="provider-account-editor-form" loading={save.isPending} disabled={!canSave||sync.isPending}>{account?'保存修改':'添加账号'}</Button></Space></div>}>
  <Form id="provider-account-editor-form" layout="vertical" onFinish={()=>{if(canSave&&!busy)save.mutate();}} disabled={busy}>
   <div className="provider-account-editor-section">
    <Form.Item label="名称" htmlFor="provider-name" required><Input id="provider-name" value={name} maxLength={100} placeholder="例如：我的 DeepSeek" onChange={e=>setName(e.target.value)}/></Form.Item>
    {providers.isError&&<Alert type="error" message="提供商加载失败" action={<Button onClick={()=>void providers.refetch()}>重试</Button>}/>}
    <Form.Item label="提供商" htmlFor="provider-choice" required><Select id="provider-choice" showSearch optionFilterProp="label" value={custom?'custom':provider} disabled={busy||!!account||providers.isPending||providers.isError} loading={providers.isPending} placeholder="选择提供商" options={providerOptions} optionRender={option=><ProviderLabel provider={String(option.value)}>{option.label}</ProviderLabel>} labelRender={option=><ProviderLabel provider={String(option.value)}>{option.label}</ProviderLabel>} onChange={changeProvider}/></Form.Item>
    {custom&&<Form.Item label="接口协议" htmlFor="provider-protocol" required><Select id="provider-protocol" value={provider} disabled={busy||!!account} options={customProviders.map(p=>({value:p.id,label:protocolName(p.id),title:protocolName(p.id)}))} onChange={changeProvider}/></Form.Item>}
    {!providers.isPending&&!providers.isError&&customProviders.some(p=>p.id==='custom-openai')&&!responsesProviderAvailable&&<Alert type="warning" showIcon message="OpenAI Responses 尚未在当前服务端启用" description="请部署最新服务端并应用数据库迁移 36 后重试。"/>}
    {provider&&<Form.Item label="服务地址" htmlFor="provider-base" required>{custom?<Input id="provider-base" value={base} disabled={busy||!!account} placeholder="http://127.0.0.1:11434/v1" onChange={e=>{setBase(e.target.value);setDirectory([]);setSelected([]);sync.reset();setKey('');keyRef.current='';}}/>:<Select id="provider-base" value={base||undefined} disabled={busy||!!account} options={providerInfo?.api_bases.map(value=>({value,label:value}))} onChange={value=>{setBase(value);setSelected([]);setDirectory([]);sync.reset();setKey('');keyRef.current='';}}/>}</Form.Item>}
    {provider&&<Form.Item label="API Key" htmlFor="provider-key" required={!custom&&!account?.credential_configured} extra={account?'留空保留已保存的密钥；更换后自动重新校验所选模型。':custom?'地址须可被平台后端访问。':'密钥加密保存，仅本人可使用。'}><Input.Password id="provider-key" value={key} maxLength={4096} autoComplete="new-password" disabled={busy||custom&&noAuth} placeholder={account?'已保存，留空保留':'填写提供商密钥'} onChange={e=>setKey(e.target.value)}/>{custom&&<Checkbox checked={noAuth} disabled={busy} onChange={e=>{setNoAuth(e.target.checked);setKey('');keyRef.current='';}}>此服务无需 API Key</Checkbox>}</Form.Item>}
   </div>
   <div className="provider-account-editor-section"><div className="provider-model-picker-heading"><h3>可用模型 <span>{selected.length}/20</span></h3><Button icon={<RefreshCw size={15} aria-hidden="true"/>} loading={sync.isPending} disabled={!provider||!base||!keyReady||save.isPending} onClick={()=>sync.mutate()}>{custom?'同步服务模型':'同步官方列表'}</Button></div>
    {sync.isError&&<Alert type="error" showIcon message={sync.error.message} action={<Button onClick={()=>sync.mutate()}>重试</Button>}/>}
    {sync.isSuccess&&<p className="provider-sync-result" role="status">已同步 {directory.length} 个模型，勾选允许使用的模型。</p>}
    {custom&&<div className="provider-manual-model"><Input aria-label="自定义模型 ID" placeholder="输入模型 ID" value={manual} disabled={busy||!base} onChange={e=>{setManual(e.target.value);setManualError('');}} onPressEnter={e=>{e.preventDefault();addManual();}}/><Button icon={<Plus size={15} aria-hidden="true"/>} disabled={busy||!base||!manual.trim()} onClick={addManual}>添加模型</Button></div>}
    {manualError&&<Alert type="error" message={manualError}/>}
    {choices.size>0?<><div className="provider-model-picker-toolbar"><Input aria-label="搜索模型" prefix={<Search size={16} aria-hidden="true"/>} placeholder="搜索模型" value={search} allowClear onChange={e=>setSearch(e.target.value)}/><Checkbox checked={selectedOnly} onChange={e=>setSelectedOnly(e.target.checked)}>仅已选</Checkbox></div><div className="provider-model-picker-actions"><Button type="link" disabled={busy||!visible.length} onClick={()=>setSelected(values=>Array.from(new Set([...values,...visible.map(m=>m.id)])).slice(0,20))}>选择当前列表</Button><Button type="link" disabled={busy||!selected.length} onClick={()=>setSelected([])}>清空选择</Button></div><div className="provider-model-picker" aria-label="模型列表"><Spin spinning={sync.isPending}>{visible.length?visible.map(item=>{const used=existing.get(item.id)?.in_use??0;return <div className="provider-model-option" key={item.id}><Checkbox checked={selected.includes(item.id)} disabled={busy||(!selected.includes(item.id)&&selected.length>=20)} onChange={e=>toggle(item.id,e.target.checked)}><span>{item.id}</span></Checkbox>{used>0&&<small>{used} 个工作空间使用</small>}</div>}):<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有匹配的模型"/>}</Spin></div></>:<div className="provider-picker-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={provider?'同步模型列表后选择模型'+(custom?'，或手动添加':''):'请先选择提供商'}/></div>}
    {selected.length>0&&<Form.Item label="默认模型" htmlFor="provider-default-model" required style={{marginTop:20}}><Select id="provider-default-model" value={defaultModel} options={selected.map(model=>({value:model,label:model}))} onChange={setPreferred}/></Form.Item>}
    {removed.length>0&&<div className="provider-removed-models"><p>本次移除</p><Space wrap>{removed.map(m=><Tag key={m.id} color={m.in_use>0?'red':undefined}>{m.model}<Button type="text" size="small" aria-label={'保留 '+m.model} icon={<X size={12} aria-hidden="true"/>} onClick={()=>toggle(m.model,true)}/></Tag>)}</Space></div>}
    {blocked.length>0&&<Alert type="error" showIcon message="部分模型正在被工作空间使用，请先更换工作空间模型后再移除。"/>}
    {rotation&&<Alert type="info" showIcon message="所选模型将使用新密钥，后台自动重新同步和校验。"/>}
    {custom&&<Collapse ghost className="provider-capabilities" items={[{key:'limits',label:'新模型的能力限制',children:<div className="provider-capability-fields"><Form.Item label="上下文上限"><InputNumber aria-label="上下文上限" min={1024} max={10000000} value={contextWindow} onChange={v=>setContextWindow(v??131072)}/></Form.Item><Form.Item label="输出上限"><InputNumber aria-label="输出上限" min={128} max={contextWindow} value={outputLimit} onChange={v=>setOutputLimit(v??16384)}/></Form.Item></div>} ]}/>}
   </div>
   {(!account||rotation||selected.some(id=>!existing.has(id)))&&<p className="provider-sync-result">保存后自动校验新增或变更的模型，已通过的模型会定期复检，可能产生少量模型用量。</p>}
   {save.isError&&<Alert className="provider-save-error" type="error" showIcon message={save.error.message}/>}
  </Form>
 </Drawer>;
}
