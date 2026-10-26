import {useState} from 'react';
import {Collapse,Drawer,Empty,Input,Button} from 'antd';
import {ArrowRight,BookOpen,BriefcaseBusiness,Compass,Code2,ExternalLink,FileText,Headphones,Mail,MapPin,Music2,Newspaper,NotebookPen,Plug,Plus,Scale,Search,Server,Video} from 'lucide-react';
import type {ConnectorCatalog,ConnectorPreset,MCPEndpoint} from '../../api/connectors';

const categories=[['office','办公协作'],['knowledge','知识与笔记'],['productivity','效率工具'],['development','开发工具'],['media','资讯娱乐'],['travel','出行旅行'],['professional','专业服务'],['self_hosted','自托管']];
const categoryIcons={office:BriefcaseBusiness,knowledge:BookOpen,productivity:Plug,development:Code2,media:Newspaper,travel:Compass,professional:Scale,self_hosted:Server};
const serviceIcons={'tencent-docs':FileText,'tencent-meeting':Video,'qq-mail':Mail,'agently-cli':Mail,'qq-music':Music2,'wechat-reading':Headphones,'baidu-map':MapPin,'didi':MapPin,'youdao-note':NotebookPen,'obsidian-cli':NotebookPen};
export function categoryName(id:string){return categories.find(([key])=>key===id)?.[1]??id;}
export function PresetIcon({preset}:{preset:ConnectorPreset}){
 const Icon=serviceIcons[preset.id as keyof typeof serviceIcons]??categoryIcons[preset.category as keyof typeof categoryIcons]??Plug;
 return <span className="connector-preset-icon" style={{color:preset.color,backgroundColor:preset.color+'14'}} aria-hidden="true"><Icon size={20}/></span>;
}

export function ConnectorCatalogPicker({catalog,endpoints,onSelect,onClose}:{catalog:ConnectorCatalog;endpoints:MCPEndpoint[];onSelect:(preset:ConnectorPreset|null)=>void;onClose:()=>void}){
 const [search,setSearch]=useState('');const [category,setCategory]=useState('all');
 const query=search.trim().toLocaleLowerCase();const entries=catalog.entries.filter(v=>(category==='all'||v.category===category)&&`${v.name} ${v.id} ${v.description} ${categoryName(v.category)}`.toLocaleLowerCase().includes(query));
 return <Drawer className="connector-catalog" open title="添加连接器" width={960} onClose={onClose} extra={<Button icon={<Plus size={16} aria-hidden="true"/>} onClick={()=>onSelect(null)}>自定义 MCP</Button>}>
  <div className="connector-catalog-controls">
   <Input aria-label="搜索内置连接器" placeholder="搜索服务名称或用途" prefix={<Search size={17} aria-hidden="true"/>} allowClear value={search} onChange={e=>setSearch(e.target.value)}/>
   <nav className="connector-catalog-categories" aria-label="连接器分类">{[['all','全部'],...categories].map(([id,label])=><button type="button" key={id} aria-label={label} aria-pressed={category===id} className={category===id?'is-selected':''} onClick={()=>setCategory(id)}>{label}<span aria-hidden="true">{id==='all'?catalog.entries.length:catalog.entries.filter(v=>v.category===id).length}</span></button>)}</nav>
  </div>
  <div className="connector-catalog-caption"><h3>{category==='all'?'全部服务':categoryName(category)}</h3><span role="status">共 {entries.length} 个连接器</span></div>
  <div className="connector-catalog-list" aria-label="内置连接器目录">{entries.length?entries.map(preset=>{
   const configured=!!preset.default_url||endpoints.some(e=>e.catalog_id===preset.id);const oauth=configured&&preset.auth_kind==='oauth2';const action=oauth?'授权连接':'配置连接';
   return <article className="connector-preset" key={preset.id} aria-label={`内置连接器 ${preset.name}`}>
    <button type="button" className="connector-preset-select" aria-label={`${action} ${preset.name}`} onClick={()=>onSelect(preset)}>
     <span className="connector-preset-heading"><PresetIcon preset={preset}/><span className="connector-preset-title"><strong>{preset.name}</strong><span>{categoryName(preset.category)}</span></span></span>
     <span className="connector-preset-description">{preset.description}</span>
     <span className="connector-preset-footer"><span className={`connector-preset-status${configured?' is-configured':''}`}>{oauth?'OAuth 授权':preset.default_url?'官方默认地址':configured?'有推荐地址':preset.mode==='gateway'||preset.mode==='stdio'?'需部署网关':'使用个人服务地址'}</span><span className="connector-preset-action">{action}<ArrowRight size={15} aria-hidden="true"/></span></span>
    </button>
   </article>;
  }):<div className="connector-catalog-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的连接器"/><Button onClick={()=>{setSearch('');setCategory('all');}}>清除筛选</Button></div>}</div>
 </Drawer>;
}

export function ConnectorPresetSetup({preset}:{preset:ConnectorPreset}){
 return <div className="connector-preset-setup">
  <div className="connector-preset-summary"><PresetIcon preset={preset}/><div><strong>{preset.name}</strong><p>{preset.description}</p></div></div>
  <Collapse ghost size="small" items={[{key:'requirements',label:'接入准备',children:<><p className="connector-note">{preset.setup_hint}</p><ul>{preset.requirements.map(item=><li key={item}>{item}</li>)}</ul><a href={preset.guide_url} target="_blank" rel="noopener noreferrer">查看接入文档 <ExternalLink size={12} aria-hidden="true"/></a></>}]}/>
 </div>;
}
