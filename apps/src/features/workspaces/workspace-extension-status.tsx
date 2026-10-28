import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Spin,Tag} from 'antd';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {readWorkspaceExtensions,type WorkspaceExtension,type WorkspaceSkill} from '../../api/workspace-extensions';

const groups=[{kind:'connector',label:'连接器',permission:'connectors:read'},{kind:'memory',label:'记忆',permission:'memory:read'},{kind:'knowledge',label:'知识库',permission:'knowledge:read'},{kind:'plugin',label:'插件'}] as const;
const statuses:Record<string,string>={CONFIGURING:'待配置',AVAILABLE:'可用',STOPPED:'随工作空间停止',FAILED:'配置失败',AUTH_REQUIRED:'需重新授权',UNAVAILABLE:'不可用'};
export function WorkspaceExtensionStatus({id,user}:{id:string;user:CurrentUser}){
 const query=useQuery({queryKey:['workspace-extensions',user.id,id],queryFn:({signal})=>readWorkspaceExtensions<{items:WorkspaceExtension[]}>(id,signal),retry:false,refetchInterval:5000});
 const canReadSkills=hasPermission(user,'skills:read');
 const skills=useQuery({queryKey:['workspace-skills',user.id,id],queryFn:({signal})=>readWorkspaceExtensions<{items:WorkspaceSkill[]}>(id,signal,true),enabled:canReadSkills,retry:false});
 const selectedSkills=skills.data?.items??[];
 const selectedExtensions=query.data?.items??[];
 const hasRestricted=!canReadSkills||groups.some(group=>'permission' in group&&!hasPermission(user,group.permission));
 const settled=(!canReadSkills||skills.isSuccess)&&query.isSuccess;
 return <section aria-label="扩展配置">
  {canReadSkills&&skills.isPending&&<Spin size="small" aria-label="加载技能配置"/>}
  {canReadSkills&&skills.isError&&<Alert type="warning" showIcon message="技能配置暂不可用" action={<Button type="link" size="small" onClick={()=>void skills.refetch()}>重试</Button>}/>}
  {query.isPending&&<Spin size="small" aria-label="加载扩展配置"/>}
  {query.isError&&<Alert type="warning" showIcon message="扩展配置状态暂不可用" action={<Button type="link" size="small" onClick={()=>void query.refetch()}>重试</Button>}/>}
  {selectedSkills.length>0&&<div className="instance-extension-group"><h4>技能</h4><ul className="instance-extension-list">{selectedSkills.map(item=><li key={item.id}><div className="instance-extension-main"><strong>{item.name}</strong><span>v{item.version}</span></div></li>)}</ul></div>}
  {groups.map(group=>{
   const items=selectedExtensions.filter(item=>item.kind===group.kind);
   if(!items.length)return null;
   return <div className="instance-extension-group" key={group.kind}><h4>{group.label}</h4><ul className="instance-extension-list">{items.map(item=><li key={item.id}><div><div className="instance-extension-main"><strong>{item.name}</strong><span>{item.package_version?`v${item.package_version}`:item.version?`v${item.version}`:''}{item.scopes?.length?' · '+item.scopes.map(scope=>scope==='personal'?'个人':'本工作空间').join('、'):''}</span></div>{item.reason&&<p>{item.reason}</p>}</div><Tag color={item.status==='AVAILABLE'?'green':item.status==='FAILED'||item.status==='AUTH_REQUIRED'?'red':'default'}>{statuses[item.status]??'待确认'}</Tag></li>)}</ul></div>;
  })}
  {settled&&!selectedSkills.length&&!selectedExtensions.length&&<p className="instance-extension-empty">{hasRestricted?'暂无可查看的扩展配置':'暂无扩展配置'}</p>}
  {hasRestricted&&<p className="instance-extension-empty">部分配置无查看权限</p>}
 </section>;
}
