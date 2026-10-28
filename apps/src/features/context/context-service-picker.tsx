import {useState} from 'react';
import {Button,Drawer,Empty,Input} from 'antd';
import {ArrowRight,Brain,Compass,Database,Library,Network,Search} from 'lucide-react';
import type {ContextEndpoint,ContextKind} from '../../api/context';

const services=[
 {id:'mem0',name:'Mem0',kind:'memory',description:'保存偏好与任务经验，按需检索个人记忆。',Icon:Brain},
 {id:'openviking',name:'OpenViking',kind:'memory',description:'组织长期记忆与上下文，支持跨工作空间迁移。',Icon:Compass},
 {id:'graphiti',name:'Graphiti',kind:'memory',description:'以关系和时间线组织记忆，关联人物、事件与事实。',Icon:Network},
 {id:'milvus',name:'Milvus',kind:'knowledge',description:'连接文档向量库，检索私有语料与知识片段。',Icon:Database},
 {id:'weaviate',name:'Weaviate',kind:'knowledge',description:'组织与检索文档知识，定位相关内容。',Icon:Library},
 {id:'qdrant',name:'Qdrant',kind:'knowledge',description:'按语义检索文档，为回答提供知识依据。',Icon:Database},
];
const serviceLogos:Record<string,string>={mem0:'mem0.svg',openviking:'openviking.svg',graphiti:'graphiti.png',milvus:'milvus.svg',weaviate:'weaviate.png',qdrant:'qdrant.png'};
export function ContextServiceIcon({backend,size=22}:{backend:string;size?:number}){
 const logo=serviceLogos[backend];const Icon=services.find(v=>v.id===backend)?.Icon??Brain;
 return logo?<img className="context-service-logo" src={`/context-logos/${logo}`} alt="" aria-hidden="true" width={size} height={size}/>:<Icon size={size} aria-hidden="true"/>;
}
export function ContextServiceSummary({backend}:{backend:string}){
 const service=services.find(v=>v.id===backend);if(!service)return null;
 return <div className="context-service-summary"><span className="context-service-icon" aria-hidden="true"><ContextServiceIcon backend={backend}/></span><div><strong>{service.name}</strong><p>{service.description}</p></div></div>;
}
export function ContextServicePicker({kind,endpoints,onSelect,onClose}:{kind:ContextKind;endpoints:ContextEndpoint[];onSelect:(backend:string)=>void;onClose:()=>void}){
 const [search,setSearch]=useState('');const label=kind==='memory'?'记忆':'知识库';const query=search.trim().toLocaleLowerCase();const items=services.filter(v=>v.kind===kind&&`${v.name} ${v.description}`.toLocaleLowerCase().includes(query));
 return <Drawer open className="context-service-picker" title={`添加${label}连接`} width={800} onClose={onClose}>
  <Input aria-label={`搜索${label}服务`} placeholder="搜索服务名称或用途" prefix={<Search size={17} aria-hidden="true"/>} allowClear value={search} onChange={e=>setSearch(e.target.value)}/>
  <div className="context-service-caption"><h3>选择{label}服务</h3><span role="status">共 {items.length} 个服务</span></div>
  <div className="context-service-grid">{items.length?items.map(({id,name,description})=>{const available=endpoints.some(e=>e.backend===id);return <article key={id} aria-label={`${label}服务 ${name}`}><button className="context-service-select" type="button" aria-label={`${available?'配置连接':'查看配置'} ${name}`} onClick={()=>onSelect(id)}><span className="context-service-card-heading"><span className="context-service-icon" aria-hidden="true"><ContextServiceIcon backend={id}/></span><strong>{name}</strong></span><span className="context-service-description">{description}</span><span className="context-service-card-footer"><span className={available?'is-available':''}>{available?'已配置服务':'待配置服务'}</span><span>{available?'配置连接':'查看配置'}<ArrowRight size={15} aria-hidden="true"/></span></span></button></article>;}):<div className="context-service-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的服务"/><Button onClick={()=>setSearch('')}>清除搜索</Button></div>}</div>
 </Drawer>;
}
