import {useEffect} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Form,Select} from 'antd';
import {Puzzle} from 'lucide-react';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {ConnectorsClient,type Connector} from '../../api/connectors';
import {ContextClient,type ContextConnection} from '../../api/context';
import {readWorkspaceExtensions,type WorkspaceExtensionsInput,type WorkspacePlugin} from '../../api/workspace-extensions';
import {pluginIcons} from '../plugins/plugins-panel';
import {ConnectorIcon} from '../connectors/connector-logo';
import {ContextServiceIcon} from '../context/context-service-picker';

const connectors=new ConnectorsClient();const context=new ContextClient();
type Kind='connectors'|'memory'|'knowledge'|'plugins';
const labels:Record<Kind,string>={connectors:'连接器',memory:'记忆',knowledge:'知识库',plugins:'插件'};
type Option={id:string;name:string;version:number|string;description:string;available:boolean;catalog_version?:number;logo?:string};
export function CreateExtensions({user,open,disabled,value,onChange,onValidity}:{user:CurrentUser;open:boolean;disabled:boolean;value:WorkspaceExtensionsInput;onChange:(value:WorkspaceExtensionsInput)=>void;onValidity:(valid:boolean)=>void}){
 const canConfigure=hasPermission(user,'instances:configure');
 const can=(kind:Kind)=>canConfigure&&(kind==='plugins'||hasPermission(user,kind+':read')&&hasPermission(user,kind+':manage')&&(kind!=='connectors'||hasPermission(user,'connectors:invoke')));
 const connectionQuery=useQuery({queryKey:['connectors',user.id,'workspace-options'],enabled:open&&can('connectors'),queryFn:({signal})=>connectors.request<{items:Connector[]}>(user,'',undefined,undefined,signal),retry:false,staleTime:0});
 const memoryQuery=useQuery({queryKey:['context',user.id,'memory','workspace-options'],enabled:open&&can('memory'),queryFn:({signal})=>context.list(user,'memory',signal),retry:false,staleTime:0});
 const knowledgeQuery=useQuery({queryKey:['context',user.id,'knowledge','workspace-options'],enabled:open&&can('knowledge'),queryFn:({signal})=>context.list(user,'knowledge',signal),retry:false,staleTime:0});
 const pluginQuery=useQuery({queryKey:['workspace-plugin-options',user.id],enabled:open&&can('plugins'),queryFn:({signal})=>readWorkspaceExtensions<{items:WorkspacePlugin[]}>('plugins',signal),retry:false,staleTime:0});
 const contextOption=(item:ContextConnection):Option=>({id:item.id,name:item.name,version:item.version,logo:item.backend,available:item.enabled&&item.status==='READY',description:`${item.backend} · ${!item.enabled?'已停用':item.status==='READY'?'已就绪':'请先验证连接'}`});
 const groups=[
  {kind:'connectors' as Kind,query:connectionQuery,options:(connectionQuery.data?.items??[]).map((item):Option=>({id:item.id,name:item.name,version:item.version,catalog_version:item.catalog_version,logo:item.catalog_id,available:item.enabled&&item.status==='READY'&&item.tools.some(tool=>tool.enabled),description:!item.enabled?'已停用':item.status!=='READY'?'请先验证连接':`${item.tools.filter(tool=>tool.enabled).length} 个已授权工具${item.tools.some(tool=>tool.enabled)?'':'，请先启用工具'}`}))},
  {kind:'memory' as Kind,query:memoryQuery,options:(memoryQuery.data?.items??[]).map(contextOption)},
  {kind:'knowledge' as Kind,query:knowledgeQuery,options:(knowledgeQuery.data?.items??[]).map(contextOption)},
  {kind:'plugins' as Kind,query:pluginQuery,options:(pluginQuery.data?.items??[]).map((item):Option=>({id:item.id,name:item.name,version:item.version,available:item.available,description:item.reason??`${item.package}@${item.version}`}))},
 ];
 const usable=(group:typeof groups[number])=>!(value[group.kind]?.length)||can(group.kind)&&!group.query.isError&&!group.query.isPending&&value[group.kind]!.every(selected=>group.options.some(item=>item.available&&item.id===selected.id&&item.version===selected.version&&(!('catalog_version' in selected)||item.catalog_version===selected.catalog_version)));
 const valid=groups.every(usable);useEffect(()=>onValidity(valid),[valid,onValidity]);
 return <>{groups.filter(group=>can(group.kind)).map(group=>{
  const {kind,query,options}=group;const label=labels[kind];const selected=value[kind]??[];const update=(ids:string[])=>{
   const items=ids.map(id=>selected.find(item=>item.id===id)??(()=>{const option=options.find(item=>item.id===id)!;return kind==='plugins'?{id,version:String(option.version)}:{id,version:Number(option.version),...(kind==='connectors'?{catalog_version:option.catalog_version}:{scopes:selected[0]&&'scopes' in selected[0]?selected[0].scopes:['personal','workspace']})};})());
   onChange({...value,[kind]:items});
  };
  const icon=(item:Option|undefined)=>kind==='plugins'?(()=>{const Icon=pluginIcons[item?.id??'']??Puzzle;return <Icon size={18} aria-hidden="true"/>;})():kind==='connectors'?<ConnectorIcon catalogID={item?.logo}/>:<ContextServiceIcon backend={item?.logo??''} size={20}/>;
  return <section className={`create-extension-field create-extension-${kind}`} aria-label={`选择${label}`} key={kind}><h4><label htmlFor={`create-${kind}`}>选择{label}</label>{selected.length>0&&<span className="create-extension-count">已选 {selected.length} 项</span>}</h4>
   <Form.Item extra={kind==='plugins'?'仅可选择当前版本兼容的插件。':kind==='connectors'?'仅使用已授权的工具。':kind==='memory'?'检索所选连接的授权记忆。':'检索所选连接的授权文档。'} validateStatus={usable(group)?undefined:'error'} help={usable(group)?undefined:`所选${label}已更新或不可用，请重新加载并重新选择`}>
    <Select id={`create-${kind}`} aria-label={`创建时的${label}`} mode="multiple" allowClear showSearch optionFilterProp="search" maxCount={kind==='plugins'?12:50} maxTagCount="responsive" placeholder={`搜索并选择${label}`} value={selected.map(item=>item.id)} loading={query.isFetching} disabled={disabled||query.isPending||query.isError} options={options.map(item=>({value:item.id,label:item.name,disabled:!item.available,search:item.name+' '+item.description,description:item.description}))} labelRender={option=>{const item=options.find(item=>item.id===option.value);const frozen=selected.find(item=>item.id===option.value);return <span className="create-extension-tag" title={`${item?.name??option.label} · v${frozen?.version??''}${frozen&&'catalog_version' in frozen?` · 工具目录 v${frozen.catalog_version}`:''}`}>{icon(item)}<span>{item?.name??option.label}</span><small>v{frozen?.version}</small></span>;}} optionRender={option=><div className="create-extension-option">{icon(options.find(item=>item.id===option.value))}<div><div>{option.label}</div><div className="create-skill-description">{option.data.description}</div></div></div>} onChange={update} notFoundContent={`暂无可选${label}`}/>
   </Form.Item>

   {query.isError&&<Alert type="error" showIcon message={`${label}加载失败，未选择时可继续创建`} action={<Button type="link" size="small" onClick={()=>void query.refetch()}>重试</Button>}/>}
   {selected.length>0&&(!usable(group)||query.isError)&&<Button type="link" onClick={()=>{onChange({...value,[kind]:[]});void query.refetch();}}>清空并重新加载{label}</Button>}
   {(kind==='memory'||kind==='knowledge')&&selected.length>0&&<div className="create-extension-scope"><span>检索范围</span><Checkbox.Group disabled={disabled} aria-label={`${label}检索范围`} options={[{label:'个人',value:'personal'},{label:'本工作空间',value:'workspace'}]} value={'scopes' in selected[0]?selected[0].scopes:[]} onChange={scopes=>{if(scopes.length)onChange({...value,[kind]:selected.map(item=>({...item,scopes:scopes.map(String)}))});}}/></div>}
  </section>;
 })}</>;
}
