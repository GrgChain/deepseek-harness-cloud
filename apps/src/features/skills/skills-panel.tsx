import {useEffect,useRef,useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Collapse,Drawer,Dropdown,Empty,Form,Input,Modal,Radio,Select,Space,Spin,Tag,Tabs,Tooltip} from 'antd';
import {BookOpen,Download,Ellipsis,FileArchive,Link2,Plus,Upload} from 'lucide-react';
import {SkillsClient,type Skill,type SkillPreview} from '../../api/skills';
import {hasPermission,type CurrentUser} from '../../api/identity';
import {APIError} from '../../api/error';
import {DataPagination,usePagePagination} from '../../shared/components/data-pagination';
import {ListSearch} from '../../shared/components/list-search';
import {expireSession,useExpiredSession} from '../../shared/hooks/use-expired-session';
import {SkillHubSearch} from './skill-hub-search';

const client=new SkillsClient();
function size(bytes:number){return bytes<1024?`${bytes} B`:`${new Intl.NumberFormat('zh-CN',{maximumFractionDigits:1}).format(bytes/1024)} KiB`;}
function date(value:string|null){return value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'—';}
function download(blob:Blob,name:string){const url=URL.createObjectURL(blob);const link=document.createElement('a');link.href=url;link.download=name;link.click();setTimeout(()=>URL.revokeObjectURL(url),30_000);}
function errorMessage(error:unknown){return error instanceof Error?error.message:'操作失败，请重试';}

function NewSkillFileDialog({user,skill,onClose,onCreated}:{user:CurrentUser;skill:Skill;onClose:()=>void;onCreated:(skill:Skill,path:string)=>Promise<void>}){
 const [path,setPath]=useState('');const [content,setContent]=useState('');const [error,setError]=useState('');const [busy,setBusy]=useState(false);const [discard,setDiscard]=useState(false);const [conflict,setConflict]=useState(false);const cache=useQueryClient();
 function close(){if(busy)return;if(path||content)setDiscard(true);else onClose();}
 async function create(){if(busy||!path.trim())return;setBusy(true);setError('');setConflict(false);try{const result=await client.createFile(user,skill.id,path.trim(),skill.version,content);await onCreated(result,path.trim());}catch(cause){setConflict(cause instanceof APIError&&cause.status===409);if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setBusy(false);}}
 return <><Modal open title="新建文件" width={720} okText="创建文件" cancelText="取消" onCancel={close} onOk={()=>void create()} confirmLoading={busy} okButtonProps={{disabled:!path.trim(),'aria-label':'创建文件','aria-busy':busy}} cancelButtonProps={{disabled:busy}} closable={!busy} maskClosable={!busy} keyboard={!busy}>
  <label className="skill-content-label" htmlFor="skill-new-file-path">文件路径</label><Input id="skill-new-file-path" placeholder="例如 scripts/run.py" maxLength={512} value={path} disabled={busy} onChange={event=>setPath(event.target.value)}/>
  <p className="skill-note">填写技能包内的相对路径，支持 scripts/run.py、scripts/run.sh、references/guide.md 等文件。</p>
  <label className="skill-content-label" htmlFor="skill-new-file-content">文件内容</label><Input.TextArea id="skill-new-file-content" className="skill-markdown" value={content} disabled={busy} onChange={event=>setContent(event.target.value)} autoSize={{minRows:12,maxRows:24}} spellCheck={false}/>
  {error&&<Alert type="error" showIcon message={error} action={conflict?<Button disabled={busy} onClick={()=>void cache.invalidateQueries({queryKey:['skills',user.id,'detail',skill.id]})}>刷新版本</Button>:undefined}/>}
 </Modal><Modal open={discard} title="放弃新建文件？" okText="放弃" cancelText="继续编辑" onCancel={()=>setDiscard(false)} onOk={onClose}><p>尚未创建的文件内容会丢失。</p></Modal></>;
}

function SkillEditor({user,id,onClose,onSaved}:{user:CurrentUser;id:string;onClose:()=>void;onSaved:()=>void}){
 const details=useQuery({queryKey:['skills',user.id,'detail',id],queryFn:({signal})=>client.get(user,id,signal),retry:false,refetchOnWindowFocus:false});useExpiredSession(details.error);
 const [newFile,setNewFile]=useState(false);const [filename,setFilename]=useState('SKILL.md');const [loaded,setLoaded]=useState('');const [content,setContent]=useState('');const [original,setOriginal]=useState('');const [version,setVersion]=useState(0);const [error,setError]=useState('');const [saving,setSaving]=useState(false);const [reloading,setReloading]=useState(false);const [discard,setDiscard]=useState<{intent:'close'|'reload'|'switch'|'create';path?:string}|null>(null);const cache=useQueryClient();
 const file=useQuery({queryKey:['skills',user.id,'file',id,filename],queryFn:({signal})=>client.file(user,id,filename,signal),enabled:!!details.data&&filename!=='SKILL.md',retry:false,refetchOnWindowFocus:false});useExpiredSession(file.error);
 const selected=filename==='SKILL.md'?(details.data?{content:details.data.content??'',version:details.data.version,editable:true}:undefined):file.data;
 const fetching=filename==='SKILL.md'?details.isFetching:file.isFetching;
 const loadError=filename==='SKILL.md'?details.error:file.error;
 useEffect(()=>{if(selected&&!fetching&&loaded!==filename){setContent(selected.content);setOriginal(selected.content);setVersion(selected.version);setLoaded(filename);}},[filename,selected?.content,selected?.version,fetching,loaded]);
 const dirty=loaded===filename&&content!==original;
 function switchFile(path:string){setFilename(path);setLoaded('');setError('');}
 function selectFile(path:string){if(path===filename||saving||reloading)return;if(dirty)setDiscard({intent:'switch',path});else switchFile(path);}
 const close=()=>{if(saving)return;if(dirty)setDiscard({intent:'close'});else onClose();};
 async function reload(){setReloading(true);try{const result=filename==='SKILL.md'?await details.refetch():await file.refetch();if(result.data&&!result.isError){setContent(result.data.content??'');setOriginal(result.data.content??'');setVersion(result.data.version);setLoaded(filename);setError('');}}finally{setReloading(false);}}
 async function save(){if(saving||!details.data||!selected?.editable||loaded!==filename)return;setSaving(true);setError('');try{if(filename==='SKILL.md')await client.save(user,{...details.data,version},content);else await client.saveFile(user,id,filename,version,content);onSaved();}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setSaving(false);}}
 return <><Drawer open title={details.data?.name??'Skill 详情'} width={840} onClose={close} maskClosable={!saving} closable={!saving} keyboard={!saving} className="skill-editor" extra={hasPermission(user,'skills:manage')&&<Button type="primary" aria-label="保存" aria-busy={saving} disabled={!dirty||!!loadError||!selected?.editable||reloading} loading={saving} onClick={()=>void save()}>保存</Button>}>
  {details.isPending?<Spin aria-label="加载 Skill"/>:details.isError?<Alert type="error" showIcon message={details.error.message} action={<Button onClick={()=>void details.refetch()}>重试</Button>}/>:details.data&&<>
   <div className="skill-editor-summary"><p>{details.data.description}</p><Space wrap><Tag>{details.data.file_count} 个文件</Tag><Tag>{size(details.data.size_bytes)}</Tag><span>更新于 {date(details.data.updated_at)}</span></Space></div>
   <div className="skill-file-picker"><label htmlFor="skill-file-picker">文件</label><Select id="skill-file-picker" aria-label="文件" value={filename} virtual={false} showSearch optionFilterProp="label" disabled={saving||reloading} options={(details.data.files??[{name:'SKILL.md',size_bytes:0}]).map(item=>({value:item.name,label:item.name,title:`${item.name} · ${size(item.size_bytes)}`}))} onChange={selectFile}/>{hasPermission(user,'skills:manage')&&<Button icon={<Plus size={16} aria-hidden="true"/>} disabled={saving||reloading} onClick={()=>{if(dirty)setDiscard({intent:'create'});else setNewFile(true);}}>新建文件</Button>}</div>
   {error&&<Alert type="error" showIcon message={error} description="未保存的内容仍保留在编辑器中。版本冲突时，请先复制内容，再重新读取。" action={<Button disabled={saving||reloading} onClick={()=>{if(dirty)setDiscard({intent:'reload'});else void reload();}}>重新读取</Button>}/>}
   {loadError?<Alert type="error" showIcon message={loadError.message} action={<Button loading={reloading} onClick={()=>void reload()}>重试</Button>}/>:loaded!==filename?<Spin aria-label="加载文件"/>:selected?.editable?<>
    <label className="skill-content-label" htmlFor="skill-content">{filename}</label><Input.TextArea id="skill-content" className="skill-markdown" value={content} onChange={event=>setContent(event.target.value)} readOnly={!hasPermission(user,'skills:manage')} autoSize={{minRows:20,maxRows:34}} spellCheck={false}/>
    {filename==='SKILL.md'&&<p className="skill-note">名称须与当前 Skill 一致。</p>}
   </>:<Alert type="info" showIcon message="此文件为二进制文件，请下载技能包后查看。"/>}
   {details.data.source_url&&<Collapse ghost items={[{key:'source',label:'来源与同步',children:<div className="skill-source-detail"><a href={details.data.source_url} target="_blank" rel="noopener noreferrer">{details.data.source_url}</a><span>最近同步：{date(details.data.synced_at)}</span></div>}]}/>}
  </>}
 </Drawer><Modal open={discard!==null} title="放弃未保存的修改？" okText="放弃修改" cancelText="继续编辑" onCancel={()=>setDiscard(null)} onOk={()=>{const pending=discard;setDiscard(null);if(pending?.intent==='reload')void reload();else if(pending?.intent==='switch'&&pending.path)switchFile(pending.path);else if(pending?.intent==='create'){setContent(original);setNewFile(true);}else onClose();}}><p>尚未保存的内容会丢失。</p></Modal>
 {newFile&&details.data&&<NewSkillFileDialog user={user} skill={details.data} onClose={()=>setNewFile(false)} onCreated={async(result,path)=>{await cache.invalidateQueries({queryKey:['skills',user.id,'file',id]});cache.setQueryData(['skills',user.id,'detail',id],{...details.data,...result,content:details.data.content,files:[...(details.data.files??[]),{name:path,size_bytes:0}]});setNewFile(false);switchFile(path);void cache.invalidateQueries({queryKey:['skills',user.id,'list']});void details.refetch();}}/>}
 </>;
}

function ImportDialog({user,initial,preview:initialPreview,onClose,onApplied}:{user:CurrentUser;initial:'search'|'file'|'source';preview?:SkillPreview;onClose:()=>void;onApplied:(message:string)=>void}){
 const [method,setMethod]=useState(initial);
 const [preview,setPreview]=useState(initialPreview);const [url,setUrl]=useState('');const [mode,setMode]=useState<'skip'|'replace'>('skip');const [busy,setBusy]=useState(false);const [error,setError]=useState('');const cache=useQueryClient();const fileInput=useRef<HTMLInputElement>(null);
 async function inspect(file?:File,source=url.trim()){setBusy(true);setError('');try{setPreview(file?await client.import(user,file):await client.source(user,source));}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setBusy(false);}}
 async function apply(){if(!preview?.token)return;setBusy(true);setError('');try{const result=await client.apply(user,preview,mode);onApplied(`已保存 ${result.applied.length} 项${result.skipped.length?`，保留现有 ${result.skipped.length} 项`:''}`);}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setBusy(false);}}
 const conflicts=preview?.items.some(item=>item.conflict);
 return <Modal open title={preview?'确认导入 Skill':'导入技能包'} width={640} onCancel={onClose} maskClosable={!busy} closable={!busy} keyboard={!busy} footer={!preview&&method==='search'?<Button disabled={busy} onClick={onClose}>取消</Button>:undefined} cancelButtonProps={{disabled:busy}} okText={preview?'确认导入':method==='source'?'获取并预览':'选择文件'} confirmLoading={busy} okButtonProps={{disabled:preview?preview.unchanged||!preview.token:method==='source'&&!url.trim()}} onOk={()=>{if(preview)void apply();else if(method==='source')void inspect();else fileInput.current?.click();}}>
  {error&&<Alert type="error" showIcon message={error}/>}
  {!preview?<><Tabs activeKey={method} onChange={key=>{setMethod(key as 'search'|'file'|'source');setError('');}} items={[
   {key:'search',label:'搜索技能',disabled:busy,children:<SkillHubSearch user={user} busy={busy} onPreview={source=>{setUrl(source);void inspect(undefined,source);}}/>},
   {key:'source',label:'链接导入',disabled:busy,children:<div className="skill-import-form"><label htmlFor="skill-source-url">公开链接</label><Input id="skill-source-url" placeholder="https://github.com/owner/repo" value={url} disabled={busy} onChange={e=>setUrl(e.target.value)} onPressEnter={()=>{if(url.trim()&&!busy)void inspect();}}/><p className="skill-note">支持 GitHub 仓库、技能目录和文件链接，也支持 Markdown、ZIP 直链。先预览，再导入。</p></div>},
   {key:'file',label:'文件导入',disabled:busy,children:<div className="skill-import-form"><div className="skill-upload-target"><FileArchive size={34} aria-hidden="true"/><p>Markdown 文件或 ZIP 技能包</p><Button icon={<Upload size={16} aria-hidden="true"/>} loading={busy} onClick={()=>fileInput.current?.click()}>选择文件</Button><span>保留包内资源目录</span></div></div>},
  ]}/>
   <input ref={fileInput} type="file" accept=".md,.zip" hidden disabled={busy} aria-label="选择 Skill 文件" onChange={event=>{const file=event.target.files?.[0];if(file)void inspect(file);event.target.value='';}}/>
  </>:<div className="skill-import-preview">
   {preview.source_url&&<p className="skill-source-url" title={preview.source_url}><Link2 size={14} aria-hidden="true"/>{preview.source_url}</p>}
   {preview.unchanged&&<Alert type="success" showIcon message="已是最新内容"/>}
   {preview.items.map(item=><article className="skill-preview-item" key={item.name}><div><strong>{item.name}</strong><Tag color={item.conflict?'orange':'blue'}>{item.conflict?'已存在':'新增'}</Tag></div><p>{item.description}</p>{item.local_changed&&<Alert type="warning" showIcon message="此 Skill 有本地修改，替换前会保留上一版。"/>}<Collapse ghost items={[{key:'files',label:`${item.files.length} 个文件 · ${item.changed_files.length} 个差异`,children:<ul className="skill-file-list">{item.files.map(file=><li key={file.name}><code>{file.name}</code><span>{size(file.size_bytes)}</span></li>)}{item.changed_files.filter(name=>!item.files.some(file=>file.name===name)).map(name=><li key={name}><code>{name}</code><span>将移除</span></li>)}</ul>}]}/></article>)}
   {conflicts&&!preview.unchanged&&<fieldset className="skill-conflict-options"><legend>同名 Skill</legend><Radio.Group value={mode} onChange={event=>setMode(event.target.value)}><Space direction="vertical"><Radio value="skip">保留现有，跳过同名项</Radio><Radio value="replace">替换同名项，并保留上一版供下载</Radio></Space></Radio.Group></fieldset>}
  </div>}
 </Modal>;
}

function ExportDialog({user,ids,onClose,onDownloaded}:{user:CurrentUser;ids:string[];onClose:()=>void;onDownloaded:()=>void}){
 const details=useQuery({queryKey:['skills',user.id,'export',ids],queryFn:()=>Promise.all(ids.map(id=>client.get(user,id))),retry:false});useExpiredSession(details.error);
 const [busy,setBusy]=useState(false);const [error,setError]=useState('');const cache=useQueryClient();
 async function run(){setBusy(true);setError('');try{const file=await client.download(user,ids);download(file.blob,file.name);onDownloaded();}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setBusy(false);}}
 return <Modal open title={`导出 ${ids.length} 项 Skill`} okText="下载" cancelText="取消" onCancel={onClose} maskClosable={!busy} closable={!busy} keyboard={!busy} cancelButtonProps={{disabled:busy}} okButtonProps={{disabled:!details.data}} confirmLoading={busy} onOk={()=>void run()}>
  <p>单项无附属文件时下载 Markdown，其他情况打包为 ZIP。请确认正文和资源中没有不宜分享的内容。</p>
  {(error||details.isError)&&<Alert type="error" showIcon message={error||details.error?.message}/>}
  {details.isPending?<Spin aria-label="检查导出文件"/>:<Collapse items={details.data?.map(skill=>({key:skill.id,label:`${skill.name} · ${skill.file_count} 个文件`,children:<ul className="skill-file-list">{skill.files?.map(file=><li key={file.name}><code>{file.name}</code><span>{size(file.size_bytes)}</span></li>)}</ul>}))}/>}
 </Modal>;
}

export function SkillsPanel({user}:{user:CurrentUser}){
 const [search,setSearch]=useState('');const [filter,setFilter]=useState('');const [selected,setSelected]=useState<string[]>([]);const [editor,setEditor]=useState<string|null>(null);const [create,setCreate]=useState(false);const [form]=Form.useForm<{name:string;description:string}>();
 const [importDialog,setImportDialog]=useState<{initial:'search'|'file'|'source';preview?:SkillPreview}|null>(null);const [exports,setExports]=useState<string[]|null>(null);const [deleting,setDeleting]=useState<Skill|null>(null);const [confirmation,setConfirmation]=useState('');const [error,setError]=useState('');const [notice,setNotice]=useState('');const [busy,setBusy]=useState('');const cache=useQueryClient();
 const manage=hasPermission(user,'skills:manage');
 useEffect(()=>{const timer=setTimeout(()=>setFilter(search.trim()),250);return()=>clearTimeout(timer);},[search]);
 const pagination=usePagePagination([user.id,filter].join(':'));
 const list=useQuery({queryKey:['skills',user.id,'list',filter,pagination.page,pagination.pageSize],queryFn:({signal})=>client.list(user,filter,pagination.page,pagination.pageSize,signal),retry:false});useExpiredSession(list.error);
 useEffect(()=>{setSelected([]);},[filter,pagination.page,pagination.pageSize]);
 useEffect(()=>{if(notice){const timer=setTimeout(()=>setNotice(''),5000);return()=>clearTimeout(timer);}},[notice]);
 const items=list.isError?[]:list.data?.items??[];
 async function refresh(){await cache.invalidateQueries({queryKey:['skills',user.id]});}
 async function action(key:string,run:()=>Promise<void>){setBusy(key);setError('');try{await run();}catch(cause){if(cause instanceof APIError&&cause.status===401)expireSession(cache);setError(errorMessage(cause));}finally{setBusy('');}}
 function select(id:string,checked:boolean){setSelected(current=>checked?[...current,id]:current.filter(value=>value!==id));}
 const all=items.length>0&&items.every(item=>selected.includes(item.id));
 return <section className="instances-section skills-panel" aria-label="技能包">
  <div className="provider-page-heading workspace-actions"><h2>技能包</h2>{manage&&<Space wrap><Button aria-label="导入" icon={<Upload size={16} aria-hidden="true"/>} onClick={()=>{setError('');setImportDialog({initial:'search'});}}>导入</Button><Button type="primary" icon={<Plus size={16} aria-hidden="true"/>} onClick={()=>{setError('');form.resetFields();setCreate(true);}}>新建技能</Button></Space>}</div>
  <div className="workspace-toolbar"><Space className="skill-selection" wrap><Checkbox aria-label="选择本页全部 Skills" checked={all} disabled={!items.length} indeterminate={selected.length>0&&!all} onChange={event=>setSelected(event.target.checked?items.map(item=>item.id):[])}>选择本页</Checkbox><Button icon={<Download size={16} aria-hidden="true"/>} disabled={!selected.length||selected.length>50} onClick={()=>setExports(selected)}>导出{selected.length?` (${selected.length})`:''}</Button></Space><ListSearch label="搜索 Skills" placeholder="搜索名称或说明" value={search} onChange={setSearch} onSubmit={()=>setFilter(search.trim())} onRefresh={()=>void list.refetch()} loading={list.isFetching} refreshLabel="刷新 Skills"/></div>
  {notice&&<Alert type="success" showIcon message={notice} closable onClose={()=>setNotice('')}/>}
  {(error||list.isError)&&<Alert type="error" showIcon message={error||list.error?.message} closable={!!error} onClose={()=>setError('')} action={list.isError?<Button onClick={()=>void list.refetch()}>重试</Button>:undefined}/>}
  <Spin spinning={list.isPending}><div className="workspace-list" aria-label="技能包列表">{items.length?items.map(skill=><article className="workspace-record skill-row" key={skill.id} aria-label={`Skill ${skill.name}`}>
    <Checkbox aria-label={`选择 ${skill.name}`} checked={selected.includes(skill.id)} onChange={event=>select(skill.id,event.target.checked)}/>
    <span className="workspace-symbol" aria-hidden="true"><BookOpen size={20}/></span>
    <div className="workspace-record-content skill-identity"><div className="workspace-record-heading"><h3><Button type="link" onClick={()=>setEditor(skill.id)}>{skill.name}</Button></h3></div><p title={skill.description}>{skill.description}</p><div className="workspace-record-meta"><span>{skill.file_count} 个文件 · {size(skill.size_bytes)}</span><span>{skill.source_url?<Tooltip title={skill.source_url}><span>外部链接</span></Tooltip>:'个人技能'}</span><time dateTime={skill.updated_at}>更新于 {date(skill.updated_at)}</time></div></div>
    <div className="skill-row-actions"><Tooltip title="下载 Skill"><Button aria-label={`下载 ${skill.name}`} icon={<Download size={16}/>} onClick={()=>setExports([skill.id])}/></Tooltip><Dropdown trigger={['click']} menu={{items:[{key:'edit',label:manage?'编辑':'查看'},...(manage&&skill.source_url?[{key:'sync',label:'检查更新'}]:[]),...(skill.has_previous?[{key:'previous',label:'下载替换前版本'}]:[]),...(manage?[{key:'delete',label:'删除',danger:true}]:[])],onClick:({key})=>{if(key==='edit')setEditor(skill.id);else if(key==='delete'){setError('');setDeleting(skill);setConfirmation('');}else if(key==='sync')void action(skill.id,async()=>{const preview=await client.check(user,skill);if(preview.unchanged)setNotice('此 Skill 已是最新内容');else setImportDialog({initial:'source',preview});});else if(key==='previous')void action(skill.id,async()=>{const file=await client.download(user,[skill.id],true);download(file.blob,file.name);});}}}><Button aria-label={`${skill.name} 更多操作`} disabled={!!busy} icon={<Ellipsis size={17}/>}/></Dropdown></div>
   </article>):<div className="workspace-list-empty"><BookOpen size={28} aria-hidden="true"/><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={list.isError?'技能库暂不可用':filter?'没有匹配的 Skill':'暂无技能包'}/></div>}</div></Spin>
  <DataPagination label="Skills" pagination={pagination} count={list.isError?0:items.length} total={list.isError?undefined:list.data?.total} loading={list.isFetching} failed={list.isError}/>
  {editor&&<SkillEditor key={editor} user={user} id={editor} onClose={()=>setEditor(null)} onSaved={()=>{setEditor(null);setNotice('Skill 已保存');void refresh();}}/>}
  <Modal open={create} title="新建技能" okText="创建并编辑" confirmLoading={busy==='create'} cancelButtonProps={{disabled:!!busy}} maskClosable={!busy} closable={!busy} keyboard={!busy} onCancel={()=>setCreate(false)} onOk={()=>form.submit()}><Form form={form} layout="vertical" onFinish={values=>void action('create',async()=>{const skill=await client.create(user,values.name,values.description);setCreate(false);setEditor(skill.id);await refresh();})}><Form.Item name="name" label="Skill 名称" rules={[{required:true,message:'请输入名称'},{pattern:/^[a-z0-9]+(?:-[a-z0-9]+)*$/,max:64,message:'使用 1–64 个小写字母、数字和单个连字符'}]}><Input placeholder="例如 meeting-notes" maxLength={64}/></Form.Item><Form.Item name="description" label="用途说明" rules={[{required:true,whitespace:true,message:'请填写用途说明'},{max:2000}]}><Input.TextArea placeholder="这个 Skill 用来做什么，适合何时使用" autoSize={{minRows:3,maxRows:6}} maxLength={2000}/></Form.Item>{error&&busy!=='create'&&<Alert type="error" showIcon message={error}/>}</Form></Modal>
  {importDialog&&<ImportDialog user={user} {...importDialog} onClose={()=>setImportDialog(null)} onApplied={message=>{setImportDialog(null);setNotice(message);void refresh();}}/>}
  {exports&&<ExportDialog user={user} ids={exports} onClose={()=>setExports(null)} onDownloaded={()=>{setExports(null);setSelected([]);}}/>}
  <Modal open={!!deleting} title="删除 Skill？" okText="永久删除" okButtonProps={{danger:true,disabled:!deleting||confirmation!==deleting.name}} confirmLoading={busy==='delete'} cancelButtonProps={{disabled:!!busy}} maskClosable={!busy} closable={!busy} keyboard={!busy} onCancel={()=>setDeleting(null)} onOk={()=>{if(deleting)void action('delete',async()=>{await client.remove(user,deleting,confirmation);setDeleting(null);setSelected(current=>current.filter(id=>id!==deleting.id));setNotice('Skill 已删除');await refresh();});}}><p>将删除「{deleting?.name}」及其资源文件和替换前版本，无法恢复。</p><label htmlFor="skill-delete-name">输入 Skill 名称确认</label><Input id="skill-delete-name" value={confirmation} onChange={event=>setConfirmation(event.target.value)} autoComplete="off"/>{error&&<Alert type="error" showIcon message={error}/>}</Modal>
 </section>;
}
