import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, Descriptions, Drawer, Empty, Input, Modal, Select, Space, Spin, Tag, Tooltip, Tree } from 'antd';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, ChevronLeft, ChevronRight, Download, Eye, FileText, Folder, RefreshCw, Trash2 } from 'lucide-react';
import type { CurrentUser } from '../../api/identity';
import { APIError } from '../../api/error';
import { StorageClient, type StorageAsset, type StorageBackup, type StorageDirectoryEntry } from '../../api/storage';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { ListSearch } from '../../shared/components/list-search';
import { AssetStatus, assetStatuses, assetProcessingStages } from '../../shared/components/resource-status';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { downloadFile, FilePreviewModal, filePreviewKind, filePreviewLimit, type PreviewFile } from './file-preview';
import { clearResumedPreview, readResumedPreview } from './preview-recovery';

const client=new StorageClient();
const backupStatuses:Record<string,string>={RUNNING:'进行中',SUCCEEDED:'备份成功',FAILED:'备份失败',UNCERTAIN:'结果待确认',PRUNE_PENDING:'清理待确认',PRUNED:'已清理',EXPIRED:'已过期'};
const blockers:Record<string,string>={OPERATION_PENDING:'有未完成或待确认的工作空间操作',BINDING_UNCONFIRMED:'原数据盘绑定尚未确认',QUOTA_UNCONFIRMED:'存储额度账本待核对',COMPUTE_NOT_STOPPED:'须先停止工作空间并确认完成',BACKUP_PENDING:'须先核对上次备份结果',TARGET_NOT_STOPPED:'目标须为已停止且可用的工作空间',LEGAL_HOLD:'受法律保留保护',NOT_SOFT_DELETED:'须先软删除工作空间并确认完成',RETENTION_ACTIVE:'尚未到保留截止时间',MINIMUM_7_DAYS:'软删除后须至少保留 7 天',ALREADY_RECLAIMED:'数据盘已回收',RUNTIME_BINDING_UNAVAILABLE:'目标运行绑定或凭据不可用'};
const time=(value:string|null|undefined)=>value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'—';
const bytes=(value:number)=>`${new Intl.NumberFormat('zh-CN',{maximumFractionDigits:2}).format(value/2**30)} GiB`;
function Usage({asset,detail=false}:{asset:StorageAsset;detail?:boolean}){
 const usage=asset.usage;
 const known=['CURRENT','STALE'].includes(usage.status)&&usage.used_bytes!==null&&usage.available_bytes!==null;
 return <div className="storage-usage"><strong>{known?`${bytes(usage.used_bytes!)} 已用`:{UNSAMPLED:'未采集',UNAVAILABLE:'暂不可用'}[usage.status]??'待核对'}</strong>
  {known&&<span>{bytes(usage.available_bytes!)} 可用{usage.status==='STALE'?' · 待刷新':''}</span>}
  {detail&&usage.sampled_at&&<span>更新于 {time(usage.sampled_at)}{usage.status==='UNAVAILABLE'?'（最近记录）':''}</span>}
 </div>;
}
function BackupSummary({backup}:{backup:StorageBackup|null}){return backup?<div className="storage-backup-summary"><span>{backupStatuses[backup.status]??'待核对'}</span><small>{time(backup.completed_at??backup.created_at)}</small></div>:<span className="storage-muted">暂无备份记录</span>;}
function Retention({asset}:{asset:StorageAsset}){return <div className="storage-retention">{asset.legal_hold&&<Tag>法律保留中</Tag>}{asset.status==='DELETED'?<span>已完成回收</span>:asset.retention_expired?<span>保留已到期，尚未回收</span>:asset.retention_until?<span>保留至 {time(asset.retention_until)}</span>:<span>{asset.deleted_at?'保留期限待核对':'随工作空间保留'}</span>}</div>;}
function MaintenanceGuide({asset}:{asset:StorageAsset}){return <Collapse ghost items={[{key:'maintenance',label:'维护条件',children:<>
 <Descriptions column={1} size="small" items={(['backup','restore','reclaim'] as const).map(action=>({key:action,label:{backup:'备份',restore:'恢复',reclaim:'到期回收'}[action],children:asset.maintenance[`${action}_blockers`].length?asset.maintenance[`${action}_blockers`].map(code=>blockers[code]??'请联系管理员核对').join('；'):'可联系管理员处理'}))}/>
 <p className="storage-muted">恢复须使用本人另一工作空间的同容量备份；先保留目标原数据，恢复后不自动启动。</p>
 </>}]}/>;}
function ReleaseStorage({asset,user}:{asset:StorageAsset;user:CurrentUser}){
 const cache=useQueryClient();const [open,setOpen]=useState(false);const [name,setName]=useState('');const [busy,setBusy]=useState(false);const [submitted,setSubmitted]=useState(false);const [error,setError]=useState('');
 const allowed=user.permissions.includes('instances:release-storage');
 const blocked=asset.maintenance.reclaim_blockers.filter(code=>!['RETENTION_ACTIVE','MINIMUM_7_DAYS'].includes(code));
 const pending=asset.status==='RECLAIM_PENDING'||(submitted&&asset.status!=='DELETED');
 async function release(){setBusy(true);setError('');try{await client.release(user,asset.id,name);setSubmitted(true);setOpen(false);void cache.invalidateQueries({queryKey:['storage']});void cache.invalidateQueries({queryKey:['preflight']});}catch(e){if(e instanceof APIError&&e.status===401)expireSession(cache);setError(e instanceof Error?e.message:'释放结果待确认，请刷新查看');}finally{setBusy(false);}}
 if(asset.status==='DELETED')return <p className="storage-muted">存储已释放</p>;
 if(pending)return <Alert type="info" showIcon message="释放中，完成后返还存储额度"/>;
 if(!allowed||asset.workspace_state!=='DELETED')return null;
 return <><Button danger disabled={blocked.length>0} onClick={()=>{setName('');setError('');setOpen(true);}}>释放存储</Button>
 {blocked.length>0&&<p className="storage-muted">{blocked.map(code=>blockers[code]??'条件待核对').join('；')}</p>}
 <Modal title="释放存储" open={open} onCancel={()=>{if(!busy)setOpen(false);}} closable={!busy} maskClosable={!busy} keyboard={!busy} cancelButtonProps={{disabled:busy}} okText="永久释放" cancelText="取消" okButtonProps={{danger:true,disabled:name!==asset.workspace_name}} confirmLoading={busy} onOk={()=>void release()}>
 <p>工作数据将永久删除，无法恢复。回收完成后释放 {bytes(asset.capacity_bytes)} 存储额度。</p>
 <label htmlFor="release-workspace-name">输入「<strong>{asset.workspace_name}</strong>」以确认永久删除</label><Input id="release-workspace-name" value={name} disabled={busy} maxLength={32} autoComplete="off" onChange={e=>setName(e.target.value)}/>
 {error&&<Alert type="error" showIcon message={error}/>}
 </Modal></>;
}
type StorageRoot='workspace'|'dsh';
type StorageTreeNode={key:string;title:ReactNode;children?:StorageTreeNode[];isLeaf?:boolean;icon?:ReactNode;selectable?:boolean};
type StorageTreeLocation={root:StorageRoot;path:string};
type StorageMorePage={root:StorageRoot;path:string;after:string;parentKey:string};
const storageTreeKey=(root:StorageRoot,path:string)=>`${root}:${path}`;
function storageTreeLocation(key:string):StorageTreeLocation|null{
 const index=key.indexOf(':');if(index<0)return null;const root=key.slice(0,index);if(root!=='workspace'&&root!=='dsh')return null;return {root,path:key.slice(index+1)};
}
function findStorageTreeNode(nodes:StorageTreeNode[],key:string):StorageTreeNode|undefined{
 for(const node of nodes){if(node.key===key)return node;const found=node.children&&findStorageTreeNode(node.children,key);if(found)return found;}return undefined;
}
function replaceStorageTreeChildren(nodes:StorageTreeNode[],key:string,children:StorageTreeNode[]):StorageTreeNode[]{
 return nodes.map(node=>node.key===key?{...node,children,isLeaf:children.length===0}:node.children?{...node,children:replaceStorageTreeChildren(node.children,key,children)}:node);
}
function FileBrowser({user,asset}:{user:CurrentUser;asset:StorageAsset}){
 const [resumed]=useState(()=>readResumedPreview(asset.id));
 const [root,setRoot]=useState<StorageRoot>(resumed?.root??'workspace');const [path,setPath]=useState(resumed?.path.split('/').slice(0,-1).join('/')??'');const [pages,setPages]=useState<string[]>(['']);const [page,setPage]=useState(0);const [preview,setPreview]=useState<PreviewFile|null>(resumed);const [busyFile,setBusyFile]=useState('');const [error,setError]=useState('');
 useEffect(()=>clearResumedPreview(),[]);
 const [treeData,setTreeData]=useState<StorageTreeNode[]>([{key:'data:',title:<span className="storage-tree-title" title="/data" aria-label="/data">/data</span>,children:[{key:'workspace:',title:<span className="storage-tree-title" title="/data/workspace" aria-label="/data/workspace">workspace</span>,isLeaf:false,icon:<Folder size={16}/>},{key:'dsh:',title:<span className="storage-tree-title" title="/data/dsh" aria-label="/data/dsh">dsh</span>,isLeaf:false,icon:<Folder size={16}/>}],isLeaf:false,icon:<Folder size={16}/>,selectable:false}]);
 const treeDataRef=useRef(treeData);const [loadedKeys,setLoadedKeys]=useState<string[]>(['data:']);const loadedKeysRef=useRef(new Set<string>(['data:']));const [expandedKeys,setExpandedKeys]=useState<string[]>(['data:','workspace:']);const [selectedKeys,setSelectedKeys]=useState<string[]>(['workspace:']);
 const morePages=useRef(new Map<string,StorageMorePage>());const inFlightLoads=useRef(new Map<string,Promise<void>>());const [treeError,setTreeError]=useState<{message:string;retry:()=>void}|null>(null);
 const cache=useQueryClient();
 const activeFileRequest=useRef<AbortController|null>(null);
 useEffect(()=>()=>activeFileRequest.current?.abort(),[]);
 const browsable=['READY','RETAINED'].includes(asset.status)&&['RUNNING','STOPPED','DELETED'].includes(asset.workspace_state);
 const after=pages[page]??'';
 const directory=useQuery({queryKey:['storage',user.id,'files',asset.id,root,path,after],queryFn:({signal})=>client.directory(asset.id,root,path,after,signal),retry:false,enabled:browsable});
 function setTree(next:StorageTreeNode[]){treeDataRef.current=next;setTreeData(next);}
 function markLoaded(keys:string[]){loadedKeysRef.current=new Set(keys);setLoadedKeys(keys);}
 function directoryQuery(rootValue:StorageRoot,pathValue:string,afterValue:string){return cache.fetchQuery({queryKey:['storage',user.id,'files',asset.id,rootValue,pathValue,afterValue],queryFn:({signal})=>client.directory(asset.id,rootValue,pathValue,afterValue,signal),retry:false,staleTime:15_000});}
 function pageNodes(result:Awaited<ReturnType<typeof client.directory>>,rootValue:StorageRoot,pathValue:string,existing:StorageTreeNode[]=[]):StorageTreeNode[]{
  const existingByKey=new Map(existing.filter(node=>!node.key.startsWith('more:')).map(node=>[node.key,node]));
  const nodes:StorageTreeNode[]=result.entries.filter(entry=>entry.type==='DIRECTORY').map(entry=>{const childPath=pathValue?`${pathValue}/${entry.name}`:entry.name;const key=storageTreeKey(rootValue,childPath);const fullPath=`${rootValue==='workspace'?'/data/workspace':'/data/dsh'}/${childPath}`;return existingByKey.get(key)??{key,title:<span className="storage-tree-title" title={fullPath} aria-label={fullPath}>{entry.name}</span>,isLeaf:false,icon:<Folder size={16}/>};});
  if(result.next_after){const key=`more:${encodeURIComponent(JSON.stringify([rootValue,pathValue,result.next_after]))}`;morePages.current.set(key,{root:rootValue,path:pathValue,after:result.next_after,parentKey:storageTreeKey(rootValue,pathValue)});nodes.push({key,title:'加载更多…',isLeaf:true});}
  return nodes;
 }
 async function loadNode(key:string):Promise<void>{
  if(loadedKeysRef.current.has(key))return;const pending=inFlightLoads.current.get(key);if(pending)return pending;const location=storageTreeLocation(key);if(!location)return;
  const request=(async()=>{try{const result=await directoryQuery(location.root,location.path,'');setTree(replaceStorageTreeChildren(treeDataRef.current,key,pageNodes(result,location.root,location.path)));markLoaded([...loadedKeysRef.current,key]);setTreeError(null);}catch(cause){const message=cause instanceof Error?cause.message:'目录树加载失败';setTreeError({message,retry:()=>{void loadNode(key).catch(()=>{});}});throw cause;}})();
  inFlightLoads.current.set(key,request);try{await request;}finally{inFlightLoads.current.delete(key);}
 }
 async function loadMore(key:string,more:StorageMorePage){
  try{const result=await directoryQuery(more.root,more.path,more.after);const existing=findStorageTreeNode(treeDataRef.current,more.parentKey)?.children??[];morePages.current.delete(key);setTree(replaceStorageTreeChildren(treeDataRef.current,more.parentKey,[...existing.filter(node=>!node.key.startsWith('more:')),...pageNodes(result,more.root,more.path,existing)]));setTreeError(null);}
  catch(cause){const message=cause instanceof Error?cause.message:'目录树加载失败';setTreeError({message,retry:()=>{void loadMore(key,more).catch(()=>{});}});throw cause;}
 }
 async function revealPath(nextRoot:StorageRoot,nextPath:string){
  const segments=nextPath?nextPath.split('/'):[];let parentPath='';const expanded=[storageTreeKey(nextRoot,'')];
  try{await loadNode(expanded[0]);}catch{}
  for(const segment of segments){
   const parentKey=storageTreeKey(nextRoot,parentPath);const targetKey=storageTreeKey(nextRoot,parentPath?`${parentPath}/${segment}`:segment);
   let child=findStorageTreeNode(treeDataRef.current,parentKey)?.children?.find(node=>node.key===targetKey);
   while(!child){const more=[...(findStorageTreeNode(treeDataRef.current,parentKey)?.children??[])].find(node=>node.key.startsWith('more:'));if(!more)break;const next=morePages.current.get(more.key);if(!next)break;await loadMore(more.key,next);child=findStorageTreeNode(treeDataRef.current,parentKey)?.children?.find(node=>node.key===targetKey);}
   if(!child)break;parentPath=parentPath?`${parentPath}/${segment}`:segment;expanded.push(storageTreeKey(nextRoot,parentPath));
   try{await loadNode(storageTreeKey(nextRoot,parentPath));}catch{break;}
  }
  setExpandedKeys(current=>[...new Set([...current,...expanded])]);setSelectedKeys([storageTreeKey(nextRoot,nextPath)]);
 }
 function navigate(nextRoot:StorageRoot,nextPath:string){setRoot(nextRoot);setPath(nextPath);setPages(['']);setPage(0);setError('');setSelectedKeys([storageTreeKey(nextRoot,nextPath)]);void revealPath(nextRoot,nextPath).catch(()=>{});}
 function openEntry(entry:StorageDirectoryEntry){if(entry.type==='DIRECTORY')navigate(root,path?`${path}/${entry.name}`:entry.name);}
 async function useFile(entry:StorageDirectoryEntry,mode:'PREVIEW'|'DOWNLOAD'){
  const filePath=path?`${path}/${entry.name}`:entry.name;
  if(mode==='PREVIEW'){setPreview({name:entry.name,root,path:filePath});return;}
  const request=new AbortController();activeFileRequest.current=request;setBusyFile(`${filePath}:${mode}`);setError('');
  try{const result=await client.file(asset.id,root,filePath,'DOWNLOAD',request.signal);downloadFile(result.blob,result.name);}
  catch(e){if(!request.signal.aborted){if(e instanceof APIError&&e.status===401)expireSession(cache);setError(e instanceof APIError?e.message:e instanceof Error?e.message:'文件读取失败，请重试');}}
  finally{if(activeFileRequest.current===request){activeFileRequest.current=null;setBusyFile('');}}
 }
 useEffect(()=>{void revealPath(root,path).catch(()=>{});},[user.id,asset.id]);
 const crumbs=path?path.split('/'):[];
 return <section className="storage-file-browser" aria-label="数据目录">
  {asset.workspace_state==='RUNNING'&&<p className="storage-file-notice">运行中 · 只读浏览，正在修改的文件可能暂时无法读取。</p>}
  {!browsable?<Alert type="warning" showIcon message="此数据盘当前不可浏览"/>:<div className="storage-file-layout">
   <aside className="storage-file-tree" aria-label="数据目录树"><h3>目录</h3><Tree blockNode selectable showIcon treeData={treeData} loadData={node=>loadNode(String(node.key))} loadedKeys={loadedKeys} onLoad={keys=>markLoaded(keys.map(String))} expandedKeys={expandedKeys} selectedKeys={selectedKeys} onExpand={keys=>setExpandedKeys(keys.map(String))} onSelect={keys=>{const key=String(keys[0]??'');const more=morePages.current.get(key);if(more){void loadMore(key,more).catch(()=>{});return;}const location=storageTreeLocation(key);if(location)navigate(location.root,location.path);}}/>
    {treeError&&<Alert type="error" showIcon message={treeError.message} action={<Button onClick={treeError.retry}>重试</Button>}/>}
   </aside>
   <div className="storage-file-main">
    <div className="storage-file-main-toolbar"><nav className="storage-file-crumbs" aria-label="当前目录"><span className="storage-file-crumb-root">/data</span><span aria-hidden="true">/</span><Button type="link" aria-current={!path?'page':undefined} onClick={()=>navigate(root,'')}>{root}</Button>{crumbs.map((part,index)=><span key={`${index}-${part}`}><span aria-hidden="true">/</span><Button type="link" onClick={()=>navigate(root,crumbs.slice(0,index+1).join('/'))}>{part}</Button></span>)}</nav><Tooltip title="刷新当前目录"><Button className="storage-file-refresh" aria-label="刷新目录" icon={<RefreshCw size={16}/>} loading={directory.isFetching} onClick={()=>void directory.refetch()}/></Tooltip></div>
    {error&&<Alert type="error" showIcon message={error} action={<Button onClick={()=>void directory.refetch()}>重试</Button>}/>}
    {directory.data?.entries.length? <div className="storage-file-columns" aria-hidden="true"><span>名称</span><span>详情</span><span>操作</span></div>:null}
    {directory.isPending?<Spin aria-label="读取数据目录"/>:directory.isError?<Alert type="error" showIcon message={directory.error instanceof Error?directory.error.message:'数据目录读取失败'} action={<Button onClick={()=>void directory.refetch()}>重试</Button>}/>:!directory.data?.entries.length?<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此目录为空"/>:<div className="storage-file-list" role="list">{directory.data.entries.map(entry=>{const filePath=path?`${path}/${entry.name}`:entry.name;return <div className="storage-file-row" role="listitem" key={entry.name}>
      {entry.type==='DIRECTORY'?<button type="button" className="storage-file-name storage-directory-link" aria-label={`进入文件夹 ${entry.name}`} onClick={()=>openEntry(entry)}><Folder size={17}/><span title={entry.name}>{entry.name}</span></button>:<div className="storage-file-name"><FileText size={17}/><span title={entry.name}>{entry.name}</span></div>}<span className="storage-file-meta">{entry.type==='DIRECTORY'?'文件夹':entry.type==='FILE'?`${fileSize(entry.size_bytes)} · ${time(entry.modified_at)}`:entry.type==='LINK'?'符号链接':'不可预览的项目'}</span>
      {entry.type==='FILE'?<Space size={4}>{(()=>{const kind=filePreviewKind(entry.name);const previewLimit=filePreviewLimit(entry.name);const legacy=/\.(doc|xls|ppt)$/i.test(entry.name);const label={office:'Office',pdf:'PDF',text:'文本'}[kind];return <Tooltip open={preview?false:undefined} title={legacy?'旧版 Office 格式请下载查看，或转换为 docx / xlsx / pptx':entry.size_bytes>previewLimit?`${label}预览上限 ${fileSize(previewLimit)}`:`预览 ${label}`}><Button aria-label={`预览 ${entry.name}`} icon={<Eye size={16}/>} disabled={legacy||entry.size_bytes>previewLimit||Boolean(busyFile)} onClick={()=>void useFile(entry,'PREVIEW')}/></Tooltip>;})()}<Tooltip title={entry.size_bytes>64*1024*1024?'下载上限 64 MiB':'下载文件'}><Button aria-label={`下载 ${entry.name}`} icon={<Download size={16}/>} disabled={entry.size_bytes>64*1024*1024||Boolean(busyFile)} loading={busyFile===`${filePath}:DOWNLOAD`} onClick={()=>void useFile(entry,'DOWNLOAD')}/></Tooltip></Space>:<span/>}
     </div>})}</div>}
    <div className="storage-file-pagination"><Button aria-label="上一页" icon={<ChevronLeft size={16}/>} disabled={page===0||directory.isFetching} onClick={()=>setPage(value=>Math.max(0,value-1))}/><span>第 {page+1} 页 · {directory.data?.entries.length??0} 项</span><Button aria-label="下一页" icon={<ChevronRight size={16}/>} disabled={!directory.data?.next_after||directory.isFetching} onClick={()=>{if(directory.data?.next_after){setPages(current=>[...current.slice(0,page+1),directory.data!.next_after!]);setPage(value=>value+1);}}}/></div>
   </div>
  </div>}
  {preview&&<FilePreviewModal key={`${preview.root}:${preview.path}`} file={preview} assetId={asset.id} client={client} onClose={()=>setPreview(null)}/>}
 </section>;
}
function fileSize(value:number){if(value<1024)return `${value} B`;const units=['KiB','MiB','GiB'];let size=value/1024,index=0;while(size>=1024&&index<units.length-1){size/=1024;index++;}return `${new Intl.NumberFormat('zh-CN',{maximumFractionDigits:1}).format(size)} ${units[index]}`;}
function StorageDetails({user,id,onClose}:{user:CurrentUser;id:string;onClose:()=>void}){
 const pagination=usePagePagination(id);
 const details=useQuery({queryKey:['storage',user.id,'asset',id],queryFn:({signal})=>client.details(user,id,signal),retry:false,refetchInterval:30_000});
 const backups=useQuery({queryKey:['storage',user.id,'backups',id,pagination.page,pagination.pageSize],queryFn:({signal})=>client.backups(id,pagination.page,pagination.pageSize,signal),retry:false,refetchInterval:30_000});
 useExpiredSession(details.error??backups.error);
 const asset=details.isError?undefined:details.data;
 return <Drawer title="数据资产详情" width={640} open onClose={onClose} className="storage-details" extra={<Button loading={details.isFetching||backups.isFetching} onClick={()=>{void details.refetch();void backups.refetch();}}>刷新详情</Button>}>
 {details.isPending?<Spin aria-label="加载数据资产"/>:details.isError?<Alert type="error" showIcon message={details.error.message}/>:asset&&<>
  <h2>{asset.workspace_name}</h2><AssetStatus status={asset.status}/>
  <Descriptions column={1} size="small" items={[
   {key:'workspace',label:'原工作空间',children:<Link to={'/app/instances/'+asset.instance_id}>{asset.workspace_name}</Link>},
   {key:'capacity',label:'已分配容量',children:bytes(asset.capacity_bytes)},{key:'usage',label:'实际数据用量',children:<Usage asset={asset} detail/>},
   {key:'retention',label:'数据保留',children:<Retention asset={asset}/>},
  ]}/>
  {asset.usage.status==='CURRENT'&&asset.usage.available_bytes===0&&<Alert type="warning" showIcon message="工作空间数据盘空间不足，请清理本人文件"/>}
  <Collapse ghost items={[{key:'metadata',label:'更多信息',children:<Descriptions column={1} size="small" items={[
   ...(Object.hasOwn(assetProcessingStages,asset.status)?[{key:'stage',label:'处理阶段',children:assetProcessingStages[asset.status]}]:[]),
   {key:'asset',label:'资产标识',children:<code>{asset.id}</code>},{key:'instance',label:'工作空间标识',children:<code>{asset.instance_id}</code>},{key:'deleted',label:'删除时间',children:time(asset.deleted_at)},
  ]}/>}]}/>
  <ReleaseStorage asset={asset} user={user}/>
  <section className="storage-detail-section"><h3>备份记录</h3>
   {asset.latest_backup?.status!=='SUCCEEDED'&&asset.latest_verified_backup&&<p>最近有效备份：{time(asset.latest_verified_backup.completed_at)} · 已校验</p>}
   {backups.isError?<Alert type="error" showIcon message={backups.error.message}/>:backups.isPending?<Spin aria-label="加载备份记录"/>:!backups.data?.items.length?<p className="storage-muted">暂无备份记录</p>:<div className="storage-backup-history">{backups.data.items.map(backup=><article key={backup.id} aria-label="备份记录"><BackupSummary backup={backup}/><code>{backup.id}</code><span>校验：{backup.verified_at?`通过 · ${time(backup.verified_at)}`:'尚未确认'}</span><span>有效期限：{backup.expires_at?time(backup.expires_at):backup.retention_policy==='LAST_7_SUCCESSFUL_DATES'?'按最近 7 个成功日期保留':'由管理员保管'}</span></article>)}</div>}
   {!backups.isError&&((backups.data?.total??0)>pagination.pageSize||pagination.page>1)&&<DataPagination label="备份记录" pagination={pagination} count={backups.data?.items.length??0} total={backups.data?.total} loading={backups.isFetching} failed={backups.isError}/> }
  </section>
  <MaintenanceGuide asset={asset}/>
 </>}
 </Drawer>;
}
export function StoragePanel({user}:{user:CurrentUser}){
 const [search,setSearch]=useState('');const [filter,setFilter]=useState({q:'',status:''});const [target,setTarget]=useState<string|null>(null);const navigate=useNavigate();
 const [removing,setRemoving]=useState<StorageAsset|null>(null);const [removeBusy,setRemoveBusy]=useState(false);const [removeError,setRemoveError]=useState('');const [removed,setRemoved]=useState(false);const cache=useQueryClient();
 useEffect(()=>{const timer=setTimeout(()=>setFilter(current=>({...current,q:search.trim()})),250);return()=>clearTimeout(timer);},[search]);
 const pagination=usePagePagination(JSON.stringify([user.id,filter]));
 const assets=useQuery({queryKey:['storage',user.id,'list',filter,pagination.page,pagination.pageSize],queryFn:({signal})=>client.assets(user,{...filter,page:pagination.page,limit:pagination.pageSize},signal),retry:false,refetchInterval:30_000});
 useExpiredSession(assets.error);
 const rows=assets.isError?[]:assets.data?.items??[];
 async function removeAsset(){if(!removing)return;setRemoveBusy(true);setRemoveError('');try{await client.remove(user,removing.id);setRemoving(null);setRemoved(true);void cache.invalidateQueries({queryKey:['storage',user.id]});}catch(e){if(e instanceof APIError&&e.status===401)expireSession(cache);setRemoveError(e instanceof Error?e.message:'删除失败，请重试');}finally{setRemoveBusy(false);}}
 return <section className="settings-section storage-panel" aria-label="工作数据"><div className="provider-page-heading"><div><h2>数据资产</h2><p>查看本人的工作空间数据盘与保留数据。</p></div></div>
  <div className="workspace-toolbar"><Select aria-label="筛选数据资产状态" value={filter.status} options={[{value:'',label:'全部状态'},...Object.entries(assetStatuses).map(([value,label])=>({value,label}))]} onChange={status=>setFilter(current=>({...current,status}))}/><ListSearch label="搜索数据资产" placeholder="搜索原工作空间名称" value={search} onChange={setSearch} onSubmit={()=>setFilter(current=>({...current,q:search.trim()}))} onRefresh={()=>void assets.refetch()} loading={assets.isFetching} refreshLabel="刷新数据资产"/></div>
  {removed&&<Alert type="success" showIcon closable message="数据资产已删除" onClose={()=>setRemoved(false)}/>}
  {assets.isError&&<Alert type="error" showIcon message={assets.error.message} action={<Button onClick={()=>void assets.refetch()}>重新加载</Button>}/>}
  {assets.isPending?<Spin aria-label="加载数据资产"/>:!rows.length?<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={assets.isError?'数据暂不可用':filter.q||filter.status?'没有符合条件的数据资产':'暂无数据资产'}/>:<div className="storage-asset-list">{rows.map(asset=><article key={asset.id} className="storage-asset-row" aria-label={`数据资产 ${asset.workspace_name}`}>
   <div className="storage-asset-identity"><Button type="link" aria-label={`浏览 ${asset.workspace_name} 数据目录`} onClick={()=>navigate(`/app/storage/${asset.id}/browse`)}>{asset.workspace_name}</Button><code>{asset.id}</code><Space wrap><AssetStatus status={asset.status}/></Space></div>
   <div><span className="storage-field-label">已分配</span><strong>{bytes(asset.capacity_bytes)}</strong></div>
   <div><span className="storage-field-label">实际用量</span><Usage asset={asset}/></div>
   <div><span className="storage-field-label">数据保留</span><Retention asset={asset}/></div>
   <div className="storage-asset-actions"><Button aria-label={`查看${asset.workspace_name}的数据详情`} onClick={()=>setTarget(asset.id)}>查看详情</Button>{asset.status==='DELETED'&&user.permissions.includes('instances:release-storage')&&<Tooltip title="删除已回收资产"><Button danger aria-label={`删除${asset.workspace_name}的数据资产`} icon={<Trash2 size={16} aria-hidden="true"/>} onClick={()=>{setRemoveError('');setRemoving(asset);}}/></Tooltip>}</div>
  </article>)}</div>}
  <DataPagination label="数据资产" pagination={pagination} count={rows.length} total={assets.isError?undefined:assets.data?.total} loading={assets.isFetching} failed={assets.isError}/>
  {target&&<StorageDetails key={target} user={user} id={target} onClose={()=>setTarget(null)}/>}
  <Modal title="删除数据资产" open={!!removing} okText="删除" cancelText="取消" okButtonProps={{danger:true}} confirmLoading={removeBusy} closable={!removeBusy} maskClosable={!removeBusy} keyboard={!removeBusy} cancelButtonProps={{disabled:removeBusy}} onCancel={()=>setRemoving(null)} onOk={()=>void removeAsset()}><p>从列表中删除“{removing?.workspace_name}”的已回收资产记录。已有备份不受影响。</p><p>资产编号：<code>{removing?.id}</code></p>{removeError&&<Alert type="error" showIcon message={removeError}/>}</Modal>
 </section>;
}

export function StorageBrowsePage({user}:{user:CurrentUser}){
 const {assetId=''}=useParams();const navigate=useNavigate();
 const details=useQuery({queryKey:['storage',user.id,'asset',assetId],queryFn:({signal})=>client.details(user,assetId,signal),retry:false});
 useExpiredSession(details.error);
 if(details.isPending)return <div className="storage-browser-loading"><Spin aria-label="加载数据资产"/></div>;
 if(details.isError)return <Alert type="error" showIcon message="无法打开数据资产" description={details.error.message} action={<Button onClick={()=>navigate('/app/storage')}>返回工作数据</Button>}/>;
 const asset=details.data;
 return <section className="storage-browser-page" aria-label="数据浏览">
  <header className="storage-browser-heading">
   <Button className="storage-browser-back" type="text" icon={<ArrowLeft size={16} aria-hidden="true"/>} onClick={()=>navigate('/app/storage')}>返回工作数据</Button>
   <div className="storage-browser-identity"><div className="storage-browser-title"><h2>{asset.workspace_name}</h2><AssetStatus status={asset.status}/></div><div className="storage-browser-meta"><span className="storage-browser-capacity"><small>已分配</small><strong>{bytes(asset.capacity_bytes)}</strong></span><span className="storage-browser-meta-divider" aria-hidden="true"/><span className="storage-browser-usage"><small>数据用量</small><Usage asset={asset}/></span></div></div>
  </header>
  <FileBrowser user={user} asset={asset}/>
 </section>;
}
