import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Empty,Input,Select,Spin,Tag} from 'antd';
import {ExternalLink,Search} from 'lucide-react';
import {SkillsClient,type HubSearch} from '../../api/skills';
import type {CurrentUser} from '../../api/identity';
import {useExpiredSession} from '../../shared/hooks/use-expired-session';

const client=new SkillsClient();
const providers=[{value:'skills.sh',label:'skills.sh'},{value:'clawhub',label:'ClawHub'},{value:'tencent',label:'SkillHub'},{value:'skillsmp',label:'SkillsMP'}];
function providerName(value:HubSearch['provider']){return providers.find(item=>item.value===value)?.label??value;}
export function SkillHubSearch({user,busy,onPreview}:{user:CurrentUser;busy:boolean;onPreview:(url:string)=>void}){
 const [provider,setProvider]=useState<HubSearch['provider']>('skills.sh');const [query,setQuery]=useState('');const [submitted,setSubmitted]=useState('');const [previewing,setPreviewing]=useState('');
 const results=useQuery({queryKey:['skill-hub-search',user.id,provider,submitted],queryFn:({signal})=>client.searchHub(user,provider,submitted,signal),enabled:!!submitted,retry:false,staleTime:60_000,refetchOnWindowFocus:false});useExpiredSession(results.error);
 const loading=!!submitted&&results.isFetching;
 return <div className="skill-hub-search">
  <form className="skill-hub-search-form" onSubmit={event=>{event.preventDefault();if(query.trim().length>=2&&!busy){if(submitted===query.trim())void results.refetch();else setSubmitted(query.trim());}}}>
   <div><label htmlFor="skill-hub-provider">技能来源</label><Select id="skill-hub-provider" value={provider} disabled={busy||loading} virtual={false} options={providers} onChange={value=>{setProvider(value);setSubmitted('');}}/></div>
   <div><label htmlFor="skill-hub-query">搜索关键词</label><Input id="skill-hub-query" placeholder="例如 browser、React、数据分析" value={query} maxLength={100} disabled={busy} onChange={event=>{setQuery(event.target.value);setSubmitted('');}}/></div>
   <Button htmlType="submit" type="primary" aria-label="搜索技能" icon={<Search size={16} aria-hidden="true"/>} loading={loading} disabled={busy||query.trim().length<2}>搜索</Button>
  </form>
  <p className="skill-note">输入至少 2 个字符搜索公开技能，选择结果后可预览文件并确认导入。{provider==='skillsmp'&&'SkillsMP 搜索有次数限制，达到限额时可切换来源。'}</p>
  <div className="skill-hub-results" aria-label="技能搜索结果" aria-live="polite" aria-busy={loading}>
   {loading?<div className="skill-hub-status"><Spin/><p>正在搜索 {providerName(provider)}…</p></div>:results.isError&&submitted?<Alert type="error" showIcon message={results.error.message} action={<Button onClick={()=>void results.refetch()}>重试</Button>}/>:submitted&&results.data?results.data.items.length?<>
    <p className="skill-note">找到 {results.data.items.length} 项技能</p>
    {results.data.items.map(item=><article key={item.url} className="skill-hub-result" aria-label={`搜索结果 ${item.name}`}>
     <div className="skill-hub-result-heading"><strong>{item.name}</strong><Tag>{providerName(results.data.provider)}</Tag></div>
     {item.description&&<p>{item.description}</p>}
     <div className="skill-hub-result-meta"><span>{item.publisher}</span><span>{new Intl.NumberFormat('zh-CN',{notation:'compact'}).format(item.downloads)}{item.metric==='stars'?' 个星标':results.data.provider==='skills.sh'?' 次安装':' 次下载'}</span></div>
     <div className="skill-hub-result-actions"><a href={item.url} target="_blank" rel="noopener noreferrer">查看来源<ExternalLink size={13} aria-hidden="true"/></a><Button disabled={busy} loading={busy&&previewing===item.url} onClick={()=>{setPreviewing(item.url);onPreview(item.source_url??item.url);}}>预览并导入</Button></div>
    </article>)}
   </>:<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有找到匹配的技能，试试其他关键词或切换来源"/>:<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择技能来源并搜索，发现所需技能"/>}
  </div>
 </div>;
}
