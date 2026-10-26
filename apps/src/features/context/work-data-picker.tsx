import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Breadcrumb,Button,Empty,Modal,Select,Space,Spin,Table} from 'antd';
import {FileText,Folder} from 'lucide-react';
import {StorageClient,type StorageDirectoryEntry} from '../../api/storage';
import type {CurrentUser} from '../../api/identity';
import {useExpiredSession} from '../../shared/hooks/use-expired-session';

const storage=new StorageClient();
export function WorkDataPicker({user,onClose,onSelected}:{user:CurrentUser;onClose:()=>void;onSelected:(content:string,title:string,source:string)=>Promise<void>}){
 const [asset,setAsset]=useState('');const [root,setRoot]=useState<'workspace'|'dsh'>('workspace');const [path,setPath]=useState('');const [after,setAfter]=useState('');const [page,setPage]=useState(1);const [busy,setBusy]=useState(false);const [error,setError]=useState('');
 const assets=useQuery({queryKey:['context-assets',user.id,page],queryFn:({signal})=>storage.assets(user,{page,limit:20,status:'',q:''},signal),retry:false});useExpiredSession(assets.error);
 const directory=useQuery({queryKey:['context-directory',user.id,asset,root,path,after],queryFn:({signal})=>storage.directory(asset,root,path,after,signal),enabled:!!asset,retry:false,refetchOnWindowFocus:false});useExpiredSession(directory.error);
 function navigate(value:string){setPath(value);setAfter('');}
 async function choose(row:StorageDirectoryEntry){
  const relative=[path,row.name].filter(Boolean).join('/');if(row.type==='DIRECTORY'){navigate(relative);return;}
  setError('');setBusy(true);try{const file=await storage.file(asset,root,relative,'PREVIEW');if(file.blob.size>256*1024)throw new Error('文本不能超过 256 KiB');const content=new TextDecoder('utf-8',{fatal:true}).decode(await file.blob.arrayBuffer());await onSelected(content,row.name,`工作数据：/data/${root}/${relative}`);onClose();}catch(cause){setError(cause instanceof Error?cause.message:'读取失败，请重试');}finally{setBusy(false);}
 }
 const supported=(row:StorageDirectoryEntry)=>row.type==='DIRECTORY'||row.type==='FILE'&&/\.(txt|md|markdown)$/i.test(row.name)&&row.size_bytes<=256*1024;
 return <Modal open title="选择工作数据文件" footer={<Button disabled={busy} onClick={onClose}>取消</Button>} onCancel={()=>{if(!busy)onClose();}} width={760}>
  <p className="context-note">只读取所选文件，预览确认后才发送到外部知识库。目前支持 UTF-8 Markdown 和文本，单文件不超过 256 KiB。</p>
  {(error||assets.isError||directory.isError)&&<Alert type="error" showIcon message={error||assets.error?.message||directory.error?.message}/>}
  <Space wrap style={{marginBottom:16}}><Select aria-label="工作数据资产" placeholder="选择本人数据资产" value={asset||undefined} style={{minWidth:220}} loading={assets.isFetching} options={assets.data?.items.filter(v=>!['RECLAIMED','DESTROYED'].includes(v.status)).map(v=>({value:v.id,label:v.workspace_name}))} onChange={value=>{setAsset(value);navigate('');}}/><Button size="small" disabled={page===1||busy} onClick={()=>setPage(v=>v-1)}>上一页资产</Button><Button size="small" disabled={busy||page*20>=(assets.data?.total??0)} onClick={()=>setPage(v=>v+1)}>下一页资产</Button><Select aria-label="数据根目录" value={root} options={[{value:'workspace',label:'/data/workspace'},{value:'dsh',label:'/data/dsh'}]} onChange={value=>{setRoot(value);navigate('');}}/></Space>
  <Breadcrumb items={[{title:<Button type="link" disabled={busy} onClick={()=>navigate('')}>/data/{root}</Button>},...path.split('/').filter(Boolean).map((part,index,all)=>({title:<Button type="link" disabled={busy} onClick={()=>navigate(all.slice(0,index+1).join('/'))}>{part}</Button>}))]}/>
  <Spin spinning={busy||directory.isFetching}><Table<StorageDirectoryEntry> rowKey="name" size="small" pagination={false} dataSource={directory.isError?[]:directory.data?.entries??[]} locale={{emptyText:<Empty description={asset?'目录为空':'请选择数据资产'}/>}} columns={[{title:'名称',render:(_,row)=><Button type="link" disabled={busy||!supported(row)} onClick={()=>void choose(row)} icon={row.type==='DIRECTORY'?<Folder size={16}/>:<FileText size={16}/>}>{row.name}</Button>},{title:'大小',render:(_,row)=>row.type==='FILE'?`${Math.ceil(row.size_bytes/1024)} KiB`:'—'}]}/></Spin>
  {directory.data?.next_after&&<Button disabled={busy} onClick={()=>setAfter(directory.data!.next_after!)}>下一页文件</Button>}
 </Modal>;
}
