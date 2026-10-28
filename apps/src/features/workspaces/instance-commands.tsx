import { useId, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal, Space, Drawer, Form, Select } from 'antd';
import { ArrowRight, Plus } from 'lucide-react';
import { useLocation, useNavigate } from 'react-router-dom';
import { hasPermission, type CurrentUser } from '../../api/identity';
import type { Instance, Operation } from '../../api/resources';
import { CommandsClient, type Acceptance, type CommandAction } from '../../api/commands';
import { isPersonalModelUsable } from '../../api/personal-models';
import { APIError } from '../../api/error';
import { SettingsClient } from '../../api/settings';
import { ProviderAccountsClient } from '../../api/provider-accounts';
import { SkillsClient, type Skill } from '../../api/skills';
import {ProviderLabel,ProviderLogo} from '../../shared/components/provider-logo';
import './instance-model-update.css';
import {CreateExtensions} from './create-extensions';
import type {WorkspaceExtensionsInput} from '../../api/workspace-extensions';
import { ResourceRemaining } from './create-resource-quota';
import { expireSession, loginPath } from '../../shared/hooks/use-expired-session';

const commands = new CommandsClient();
const settings = new SettingsClient();
const providerAccounts=new ProviderAccountsClient();
const skillsClient=new SkillsClient();

export function RetryOperationButton({user,instance,operation}:{user:CurrentUser;instance:Instance;operation:Operation|undefined}) {
 const result=useCommandResult(user);
 const unresolved=pending(user,'retry',instance.id);
 const retry=useMutation({mutationFn:()=>unresolved?commands.confirmPending(user,'retry',instance.id):commands.retry(user,instance.id,operation!.id),...result});
 if(!hasPermission(user,'instances:retry') || (!unresolved && !operation?.retry_allowed))return null;
 return <Space direction="vertical" style={{marginBottom:16}}><Button loading={retry.isPending} onClick={()=>retry.mutate()}>{unresolved?'确认上次重试结果':'重试失败操作'}</Button>{retry.isError && <Alert type="error" showIcon message={retry.error.message}/>}</Space>;
}
function useCommandResult(user:CurrentUser) {
 const cache=useQueryClient();const navigate=useNavigate();const {pathname}=useLocation();
 return {
  onSuccess:(result:Acceptance)=>{void cache.invalidateQueries({queryKey:['resources',user.id]});void cache.invalidateQueries({queryKey:['settings',user.id]});navigate('/app/instances/'+result.instance_id);},
  onError:(error:Error)=>{if(error instanceof APIError && error.status===401){expireSession(cache);navigate(loginPath(pathname),{replace:true});}},
 };
}
function pending(user:CurrentUser,action:CommandAction,id='') {try{return commands.hasPending(user,action,id);}catch{return false;}}
export function CreateInstanceButton({user,label,variant='primary'}:{user:CurrentUser|null|undefined;label?:string;variant?:'primary'|'default'}) {
 const formID=useId();
 const extensionCache=useQueryClient();
 const navigate=useNavigate();
 const result=useCommandResult(user!);
 const [open,setOpen]=useState(false);
 const [workspaceName,setWorkspaceName]=useState('');
 const cleanName=workspaceName.trim();
 const nameValid=!cleanName||/^[\p{L}\p{N}_-]{1,32}$/u.test(cleanName);
 const [selectedSpecID,setSelectedSpecID]=useState<string>();
 const readConfiguration=!!user&&hasPermission(user,'configuration:read');
 const defaults=useQuery({queryKey:['settings',user?.id,'defaults'],enabled:open&&readConfiguration,queryFn:({signal})=>settings.defaults(user!,signal),refetchInterval:30_000,retry:false});
 const templates=useQuery({queryKey:['settings',user?.id,'templates'],enabled:open&&readConfiguration,queryFn:({signal})=>settings.templates(user!,signal),refetchInterval:30_000,retry:false});
 const specs=(templates.data?.items??[]).filter(item=>item.id===defaults.data?.template_id&&item.template_version===defaults.data.template_version).map(item=>item.spec);
 const currentSpec=selectedSpecID?specs.find(spec=>spec.id===selectedSpecID):defaults.data?.spec;
 const specUsable=!selectedSpecID||!!currentSpec&&!templates.isError&&!defaults.isError;
 const resourceLoading=defaults.isPending||templates.isPending;
 const resourceError=defaults.isError||templates.isError;
 const cpuOptions=Array.from(new Set(specs.filter(spec=>spec.memory_bytes===currentSpec?.memory_bytes&&spec.storage_bytes===currentSpec.storage_bytes).map(spec=>spec.cpu_millicores))).sort((a,b)=>a-b).map(value=>({value,label:`${value/1000} 核`}));
 const memoryOptions=Array.from(new Set(specs.filter(spec=>spec.cpu_millicores===currentSpec?.cpu_millicores&&spec.storage_bytes===currentSpec.storage_bytes).map(spec=>spec.memory_bytes))).sort((a,b)=>a-b).map(value=>({value,label:`${value/2**30} GiB`}));
 const storageOptions=Array.from(new Set(specs.filter(spec=>spec.cpu_millicores===currentSpec?.cpu_millicores&&spec.memory_bytes===currentSpec.memory_bytes).map(spec=>spec.storage_bytes))).sort((a,b)=>a-b).map(value=>({value,label:`${value/2**30} GiB`}));
 const selectResources=(cpu:number,memory:number,storage:number)=>{const spec=specs.find(item=>item.cpu_millicores===cpu&&item.memory_bytes===memory&&item.storage_bytes===storage);if(spec){setSelectedSpecID(spec.id);create.reset();}};
 const [selectedProvider,setSelectedProvider]=useState('');
 const [extensions,setExtensions]=useState<WorkspaceExtensionsInput>({});
 const [extensionsValid,setExtensionsValid]=useState(true);
 const [selectedSkills,setSelectedSkills]=useState<Skill[]>([]);
 const canReadSkills=!!user&&hasPermission(user,'skills:read');
 const skills=useQuery({queryKey:['skills',user?.id,'workspace-options'],enabled:open&&canReadSkills,queryFn:async({signal})=>{
  const first=await skillsClient.list(user!,'',1,100,signal);
  const items=[...first.items];
  for(let page=2;(page-1)*100<first.total;page++)items.push(...(await skillsClient.list(user!,'',page,100,signal)).items);
  return items;
 },retry:false,refetchOnWindowFocus:true,staleTime:0});
 const skillsUsable=selectedSkills.length===0||canReadSkills&&!skills.isError&&!skills.isPending&&selectedSkills.every(selected=>skills.data?.some(item=>item.id===selected.id&&item.version===selected.version));
 const canReadModels=!!user&&hasPermission(user,'personal-model:read');
 const accounts=useQuery({queryKey:['provider-accounts',user?.id],enabled:open&&canReadModels,queryFn:({signal})=>providerAccounts.providerAccounts(user!,signal),refetchInterval:5000,retry:false});
 const providers=useQuery({queryKey:['model-providers',user?.id],enabled:open&&canReadModels,queryFn:({signal})=>providerAccounts.providers(user!,signal),retry:false});
 const activeAccounts=(accounts.data?.items??[]).filter(account=>account.status==='ACTIVE');
 const account=activeAccounts.find(item=>item.id===selectedProvider);
 const choice=account?.models.find(item=>item.id===account.default_model_id);
 const selectedUsable=canReadModels&&!!selectedProvider&&!accounts.isError&&isPersonalModelUsable(choice);
 const providerName=(id:string)=>id.startsWith('custom-')?'自定义':providers.data?.items.find(provider=>provider.id===id)?.name??id;
 const providerOptions=activeAccounts.map(account=>({value:account.id,label:`${providerName(account.provider)} · ${account.name}`}));
 const check=useQuery({queryKey:['settings',user?.id,'preflight',selectedProvider,choice?.id,choice?.version,selectedSpecID],enabled:open&&!!user&&hasPermission(user,'configuration:read')&&specUsable&&(!selectedProvider||canReadModels&&!!choice&&!accounts.isError),queryFn:({signal})=>settings.preflight(user!,signal,choice?{id:choice.id,version:choice.version}:undefined,selectedSpecID),refetchInterval:30_000,retry:false});
 const create=useMutation({mutationFn:()=>{if(user&&pending(user,'create'))return commands.confirmPending(user,'create');if(!nameValid||!selectedUsable||!specUsable||!skillsUsable||!extensionsValid||!check.data?.can_create||check.isError)throw new APIError(409,'CONFIGURATION_INVALID');return commands.create(user!,{...extensions,...(cleanName?{name:cleanName}:{}),model_configuration_id:choice!.id,model_configuration_version:choice!.version,...(selectedSpecID?{spec_id:selectedSpecID}:{}),...(selectedSkills.length?{skills:selectedSkills.map(({id,version})=>({id,version}))}:{})});},...result,onError:(error:Error)=>{result.onError(error);if(error instanceof APIError&&['EXTENSION_INVALID','VERSION_CONFLICT','NOT_FOUND'].includes(error.code)){void extensionCache.invalidateQueries({queryKey:['connectors',user?.id]});void extensionCache.invalidateQueries({queryKey:['context',user?.id]});void extensionCache.invalidateQueries({queryKey:['workspace-plugin-options',user?.id]});}if(selectedSkills.length&&error instanceof APIError&&['VERSION_CONFLICT','NOT_FOUND'].includes(error.code))void skills.refetch();},onSuccess:accepted=>{setOpen(false);result.onSuccess(accepted);}});
 const unresolved=!!user && pending(user,'create');
 const resourceBlocked=check.isError?undefined:check.data?.checks.find(item=>item.health.status!=='HEALTHY'&&['CAPACITY_UNAVAILABLE','QUOTA_EXCEEDED'].includes(item.health.reason_code));
 const blocked=resourceBlocked??check.data?.checks.find(item=>item.health.status!=='HEALTHY');
 const resourceReason=resourceBlocked?.health.reason_code==='QUOTA_EXCEEDED'?(check.data?.quota&&check.data.quota.available.storage_bytes<check.data.quota.required.storage_bytes?'存储额度不足，请联系管理员处理保留数据或调整配额':'资源额度不足，请降低资源规格或联系管理员调整配额'):resourceBlocked?'当前空闲 CPU 或内存不足，请停止不再使用的工作空间后重新检查，或联系管理员扩容':null;
 const blockedReason=!specUsable?'所选资源规格已不可用，请重新加载后选择':!selectedProvider?null:selectedProvider&&accounts.isError?'模型提供商账号加载失败，请重试':selectedProvider&&!choice?'此账号尚未设置默认模型，请到模型提供商中设置':resourceReason?resourceReason:!selectedUsable?'所选模型暂不可用，请等待校验完成或重新选择':check.isError?'暂时无法检查创建条件，请重新检查':blocked?({MAINTENANCE:'平台正在维护，请稍后重试',RUNTIME_NOT_APPROVED:'运行模板暂不可用，请联系管理员更新',VALIDATION_REQUIRED:'所选模型需要校验'} as Record<string,string>)[blocked.health.reason_code]??null:!check.data?'正在检查创建条件':!check.data.can_create?'创建条件尚未满足，请重新检查':null;
 let previous:Acceptance|null=null;try{if(user)previous=commands.lastAccepted(user,'create');}catch{ /* A corrupt record is handled by submit before any network request. */ }
 const canOpen=!!user&&hasPermission(user,'instances:create')&&hasPermission(user,'instances:start');
 const canSubmit=canOpen&&(unresolved||nameValid&&selectedUsable&&specUsable&&skillsUsable&&extensionsValid&&!!check.data?.can_create&&!check.isError);
 const quota=check.isError?undefined:check.data?.quota;
 const extensionCount=(['connectors','memory','knowledge','plugins'] as const).reduce((total,kind)=>total+(extensions[kind]?.length??0),selectedSkills.length);
 const close=()=>{if(!create.isPending)setOpen(false);};
 const show=()=>{if(!unresolved){setWorkspaceName('');setSelectedSpecID(undefined);setSelectedProvider('');setSelectedSkills([]);setExtensions({});setExtensionsValid(true);create.reset();}setOpen(true);};
 return <div className="create-command">
  <Button type={variant} icon={variant==='primary'?<Plus size={17} aria-hidden="true"/>:undefined} disabled={!canOpen||create.isPending} onClick={show}>{label??(unresolved?'查看待确认创建':'创建工作空间')}</Button>
  <Drawer rootClassName="create-instance-drawer" className="create-instance-editor" title="创建工作空间" width={760} open={open} onClose={close} closable={!create.isPending} maskClosable={!create.isPending} keyboard={!create.isPending} destroyOnHidden footer={<div className="create-instance-footer">{!unresolved&&<div className="create-instance-summary" aria-live="polite"><strong>{!skillsUsable||!extensionsValid?'请检查所选扩展':account?`${providerName(account.provider)} · ${account.name}`:'请选择模型提供商'}</strong><span>{currentSpec?`${currentSpec.cpu_millicores/1000} 核 · ${currentSpec.memory_bytes/2**30} GiB 内存 · ${currentSpec.storage_bytes/2**30} GiB 存储`:'使用默认资源'}{extensionCount>0?` · ${extensionCount} 项扩展`:''}</span></div>}<div className="create-instance-actions"><Button aria-label="取消" disabled={create.isPending} onClick={close}>取消</Button><Button aria-label={unresolved?'确认创建结果':'创建并运行'} type="primary" htmlType="submit" form={formID} disabled={!canSubmit} loading={create.isPending}>{unresolved?'确认创建结果':'创建并运行'}</Button></div></div>}>
   <Form id={formID} layout="vertical" disabled={create.isPending} onFinish={()=>{if(canSubmit&&!create.isPending)create.mutate();}}>
    {unresolved?<Alert type="info" showIcon message="上次创建结果待确认" description="将使用上次提交的名称、资源、提供商及所选扩展配置确认结果。"/>:<>
     <section className="create-instance-section create-basics" aria-label="基础配置"><h3>基础配置</h3><div className="create-basic-grid">
     <Form.Item label="工作空间名称" htmlFor="create-workspace-name" validateStatus={nameValid?undefined:'error'} help={nameValid?undefined:'使用 1–32 个字符，支持中文、字母、数字、下划线和短横线'}><Input id="create-workspace-name" aria-label="工作空间名称" placeholder="例如：研发工作空间，留空自动生成" maxLength={64} value={workspaceName} onChange={event=>{setWorkspaceName(event.target.value);create.reset();}}/></Form.Item>
     {canReadModels&&<section className="create-model-field" aria-label="使用模型">
      <Form.Item label="模型提供商" htmlFor="create-provider" required><Select id="create-provider" aria-label="创建时的模型提供商" placeholder="选择模型提供商" notFoundContent="暂无模型提供商账号" value={selectedProvider||undefined} loading={accounts.isPending} disabled={create.isPending||accounts.isError} options={providerOptions} optionRender={option=><ProviderLabel provider={activeAccounts.find(item=>item.id===option.value)?.provider??'custom'}>{option.label}</ProviderLabel>} labelRender={option=><ProviderLabel provider={activeAccounts.find(item=>item.id===option.value)?.provider??'custom'}>{option.label}</ProviderLabel>} onChange={value=>{setSelectedProvider(value);create.reset();}}/></Form.Item>
      {accounts.isError&&<Alert type="error" showIcon message="模型提供商账号加载失败" action={<Button type="link" size="small" onClick={()=>void accounts.refetch()}>重试</Button>}/>}
     </section>}
     </div></section>
     {readConfiguration&&<section className="create-instance-section" aria-label="运行资源"><h3 className="create-resource-heading">运行资源<ResourceRemaining quota={quota} resource="max_instances"/></h3>
      <div className="create-resource-grid">
       <Form.Item label={<span className="create-resource-label">CPU<ResourceRemaining quota={quota} resource="cpu_millicores"/></span>} htmlFor="create-cpu"><Select id="create-cpu" aria-label="创建时的 CPU" placeholder="默认规格" value={currentSpec?.cpu_millicores} options={cpuOptions} loading={resourceLoading} disabled={create.isPending||resourceError||!currentSpec||!specs.length} onChange={value=>selectResources(value,currentSpec!.memory_bytes,currentSpec!.storage_bytes)}/></Form.Item>
       <Form.Item label={<span className="create-resource-label">内存<ResourceRemaining quota={quota} resource="memory_bytes"/></span>} htmlFor="create-memory"><Select id="create-memory" aria-label="创建时的内存" placeholder="默认规格" value={currentSpec?.memory_bytes} options={memoryOptions} loading={resourceLoading} disabled={create.isPending||resourceError||!currentSpec||!specs.length} onChange={value=>selectResources(currentSpec!.cpu_millicores,value,currentSpec!.storage_bytes)}/></Form.Item>
      <Form.Item className="create-storage-field" label={<span className="create-resource-label">存储<ResourceRemaining quota={quota} resource="storage_bytes"/></span>} htmlFor="create-storage"><Select id="create-storage" aria-label="创建时的存储容量" placeholder="默认规格" value={currentSpec?.storage_bytes} options={storageOptions} loading={resourceLoading} disabled={create.isPending||resourceError||!currentSpec||!specs.length} onChange={value=>selectResources(currentSpec!.cpu_millicores,currentSpec!.memory_bytes,value)}/></Form.Item>
      </div>
      {resourceError&&<Alert type="error" showIcon message="资源规格加载失败" action={<Button type="link" size="small" onClick={()=>{void templates.refetch();void defaults.refetch();}}>重新加载</Button>}/>}
      {selectedSpecID&&!specUsable&&<Button type="link" onClick={()=>{setSelectedSpecID(undefined);create.reset();}}>恢复默认规格</Button>}
      {quota?.concurrent_creates_available===0&&<p className="create-quota-insufficient" role="status">当前创建并发名额已满，请等待现有操作完成后重新检查。</p>}
     </section>}

     {(canReadSkills||!!user&&hasPermission(user,'instances:configure'))&&<section className="create-instance-section create-extensions" aria-label="扩展能力"><div className="create-extensions-heading"><h3>扩展能力 <span className="create-optional">可选</span></h3><span>{extensionCount?`已选 ${extensionCount} 项`:'按需添加'}</span></div><div className="create-extension-grid">
     {canReadSkills&&<section className="create-extension-field" aria-label="选择技能"><h4><label htmlFor="create-skills">选择技能</label>{selectedSkills.length>0&&<span className="create-extension-count">已选 {selectedSkills.length} 项</span>}</h4>
      <Form.Item extra="创建时复制到工作空间并启用。" validateStatus={!skillsUsable?'error':undefined} help={!skillsUsable?'所选技能已更新或不可用，请重新加载并重新选择':undefined}>
       <Select id="create-skills" aria-label="创建时的技能" mode="multiple" showSearch allowClear maxTagCount="responsive" optionFilterProp="label" placeholder="搜索并选择技能" value={selectedSkills.map(item=>item.id)} loading={skills.isFetching} disabled={create.isPending||skills.isPending||skills.isError} notFoundContent="暂无可选技能" options={(skills.data??[]).map(item=>({value:item.id,label:item.name,description:item.description}))} optionRender={option=><div><div>{option.label}</div><div className="create-skill-description">{option.data.description}</div></div>} onChange={(ids:string[])=>{setSelectedSkills(ids.map(id=>selectedSkills.find(item=>item.id===id)??skills.data!.find(item=>item.id===id)!));create.reset();}}/>
      </Form.Item>
      {skills.isError&&<Alert type="error" showIcon message="技能加载失败，可重试或不选择技能继续创建" action={<Button type="link" size="small" onClick={()=>void skills.refetch()}>重试</Button>}/>}
      {selectedSkills.length>0&&(!skillsUsable||skills.isError)&&<Button type="link" onClick={()=>{setSelectedSkills([]);void skills.refetch();create.reset();}}>清空并重新加载技能</Button>}
     </section>}
     <CreateExtensions user={user!} open={open} disabled={create.isPending} value={extensions} onChange={value=>{setExtensions(value);create.reset();}} onValidity={setExtensionsValid}/>
     </div></section>}
     {blockedReason&&<div role="status" className="create-instance-feedback"><Alert type="warning" showIcon message={blockedReason} action={!!choice&&specUsable?<Button type="link" size="small" loading={check.isFetching} onClick={()=>void check.refetch()}>重新检查</Button>:undefined}/></div>}
    </>}
    {previous&&<Button type="link" onClick={()=>navigate('/app/instances/'+previous!.instance_id)}>查看上次创建</Button>}
    {create.isError&&<Alert className="create-instance-feedback" type="error" showIcon message={create.error.message}/>}
   </Form>
  </Drawer>
 </div>;
}
export function InstanceCommands({user,instance}:{user:CurrentUser;instance:Instance}) {
 const [selectedProvider,setSelectedProvider]=useState<string>();
 const result=useCommandResult(user);const [dialog,setDialog]=useState<'delete'|'configure'|null>(null);const [name,setName]=useState('');const [version,setVersion]=useState(instance.version);
 const canReadModels=hasPermission(user,'personal-model:read');
 const accounts=useQuery({queryKey:['provider-accounts',user.id],enabled:dialog==='configure'&&canReadModels,queryFn:({signal})=>providerAccounts.providerAccounts(user,signal),refetchInterval:5000,retry:false});
 const providers=useQuery({queryKey:['model-providers',user.id],enabled:dialog==='configure'&&canReadModels,queryFn:({signal})=>providerAccounts.providers(user,signal),retry:false});
 const activeAccounts=(accounts.data?.items??[]).filter(account=>account.status==='ACTIVE');
 const account=activeAccounts.find(item=>item.id===selectedProvider);
 const choice=account?.models.find(item=>item.id===account.default_model_id);
 const sameModel=!!choice && choice.id===instance.config_snapshot.model_configuration_id && choice.version===instance.config_snapshot.model_configuration_version;
 const usable=canReadModels&&!accounts.isError&&isPersonalModelUsable(choice);
 const providerName=(id:string)=>id.startsWith('custom-')?'自定义':providers.data?.items.find(provider=>provider.id===id)?.name??id;
 const providerOptions=activeAccounts.filter(account=>isPersonalModelUsable(account.models.find(item=>item.id===account.default_model_id))).map(account=>({value:account.id,label:`${providerName(account.provider)} · ${account.name}`}));
 const action=useMutation({mutationFn:(value:CommandAction)=>{
  switch(value){
   case 'start':return commands.start(user,instance.id);
   case 'stop':return commands.stop(user,instance.id);
   case 'delete':return commands.delete(user,instance.id,name);
   case 'configure':if(!usable||!choice)throw new APIError(409,'CONFIGURATION_INVALID');return commands.configure(user,instance.id,{expected_instance_version:version,model_configuration_id:choice.id,model_configuration_version:choice.version});
   default:throw new Error('操作不可用');
  }
 },...result,onSuccess:(accepted:Acceptance)=>{setDialog(null);setName('');result.onSuccess(accepted);}});
 const resume=useMutation({mutationFn:(value:CommandAction)=>commands.confirmPending(user,value,instance.id),...result});
 const busy=action.isPending || resume.isPending;const error=action.error ?? resume.error;
 const actions=instance.allowed_actions.filter(value=>['start','stop','delete','configure'].includes(value));
 const unresolved=(['start','stop','delete','configure'] as CommandAction[]).filter(value=>pending(user,value,instance.id));
 const open=(value:'delete'|'configure')=>{setVersion(instance.version);setSelectedProvider(undefined);setName('');action.reset();resume.reset();setDialog(value);};
 return <>
  <Space wrap style={{marginBottom:16}}>
   {actions.includes('start') && <Button disabled={busy || unresolved.length>0} onClick={()=>action.mutate('start')}>启动实例</Button>}
   {actions.includes('stop') && <Button disabled={busy || unresolved.length>0} onClick={()=>action.mutate('stop')}>停止实例</Button>}
   {actions.includes('configure') && <Button disabled={busy || unresolved.length>0} onClick={()=>open('configure')}>更新模型提供商</Button>}
   {actions.includes('delete') && <Button danger disabled={busy || unresolved.length>0} onClick={()=>open('delete')}>删除实例</Button>}
   {unresolved.map(value=><Button key={value} loading={resume.isPending} disabled={busy && !resume.isPending} onClick={()=>resume.mutate(value)}>确认上次{({start:'启动',stop:'停止',delete:'删除',configure:'模型提供商更新'} as Record<string,string>)[value]}结果</Button>)}
  </Space>
  {error && <Alert style={{marginBottom:16}} type="error" showIcon message={error.message}/>}
  <Modal className={dialog==='configure'?'instance-model-update':undefined} title={dialog==='delete'?'删除实例':'更新模型提供商'} open={dialog!==null} onCancel={()=>setDialog(null)} closable={!busy} maskClosable={!busy} cancelButtonProps={{disabled:busy}} confirmLoading={busy} okText={dialog==='delete'?'确认删除':'应用配置'} okButtonProps={{danger:dialog==='delete',disabled:busy || (dialog==='delete'?name!==instance.name:!usable)}} onOk={()=>action.mutate(dialog!)}>
   {dialog==='delete'?<><p>删除后释放计算资源。工作数据按保留策略保存，仍占用存储额度。</p><p>输入「<strong>{instance.name}</strong>」以确认删除</p><Input aria-label="确认实例名称" autoComplete="off" value={name} onChange={event=>setName(event.target.value)}/></>:<>
    <p className="instance-model-update-intro">为「{instance.name}」选择模型提供商，应用该账号的默认模型。</p>
    <div className="instance-model-comparison" aria-label="模型更新预览">
     <div className="instance-model-comparison-item"><span>当前模型</span><strong>{instance.model_display_name || '模型信息暂不可用'}</strong></div>
     <ArrowRight className="instance-model-comparison-arrow" size={18} aria-hidden="true"/>
     <div className="instance-model-comparison-item" role="status"><span>应用后的模型{choice&&sameModel&&<small>与当前相同</small>}</span>{choice&&usable?<strong>{choice.model}</strong>:<span className="instance-model-placeholder">选择提供商后显示</span>}</div>
    </div>
    <Form layout="vertical"><Form.Item label="模型提供商" htmlFor="instance-update-provider" required extra="仅显示已完成校验的账号，使用其默认模型。"><Select id="instance-update-provider" aria-label="应用到实例的模型提供商" showSearch optionFilterProp="label" style={{width:'100%'}} placeholder="搜索并选择模型提供商" notFoundContent="暂无可用的模型提供商" value={account?selectedProvider:undefined} loading={canReadModels&&accounts.isPending} disabled={busy||!canReadModels||accounts.isError} options={providerOptions} optionRender={option=>{const item=activeAccounts.find(item=>item.id===option.value);return <div className="instance-provider-option"><ProviderLogo provider={item?.provider??'custom'} size={28}/><span><strong>{option.label}</strong><small>默认模型 · {item?.models.find(model=>model.id===item.default_model_id)?.model}</small></span></div>;}} labelRender={option=><ProviderLabel provider={activeAccounts.find(item=>item.id===option.value)?.provider??'custom'}>{option.label}</ProviderLabel>} onChange={value=>{setSelectedProvider(value);action.reset();}}/></Form.Item></Form>
    <Alert className="instance-model-restart-notice" type="info" showIcon message={instance.observed_state==='RUNNING'?'应用后实例将重启，工作数据保留。':'应用后将在下次启动时使用新模型，工作数据保留。'}/>
    {!canReadModels?<Alert type="warning" showIcon message="没有模型提供商读取权限"/>:accounts.isError?<Alert type="error" showIcon message="模型提供商账号加载失败" action={<Button onClick={()=>void accounts.refetch()}>重新加载</Button>}/>:!accounts.isPending&&!providerOptions.length?<Alert type="warning" showIcon message="暂无可用的模型提供商，请先配置并完成校验"/>:selectedProvider&&!usable&&<Alert type="warning" showIcon message="所选提供商暂不可用，请重新选择或等待校验完成"/>}
   </>}
   {action.isError && <Alert style={{marginTop:16}} type="error" message={action.error.message}/>}
  </Modal>
 </>;
}
