import {useState,type ReactNode} from 'react';
import {Segmented} from 'antd';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export type ContentView='source'|'markdown';

export function ContentViewSwitcher({value,onChange}:{value:ContentView;onChange:(value:ContentView)=>void}){
 return <Segmented className="context-content-switcher" aria-label="内容显示方式" value={value} options={[{label:'原文',value:'source'},{label:'Markdown 预览',value:'markdown'}]} onChange={value=>onChange(value as ContentView)}/>;
}

export function MarkdownContent({text}:{text:string}){
 return <div className="context-markdown"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{a:({node,...props})=><a {...props} target="_blank" rel="noopener noreferrer"/>}}>{text}</ReactMarkdown></div>;
}

export function KnowledgeContent({text,className,label,children}:{text:string;className:string;label:string;children?:ReactNode}){
 const [view,setView]=useState<ContentView>('source');
 return <><ContentViewSwitcher value={view} onChange={setView}/><div className={className} role="region" aria-label={view==='source'?label:'Markdown 预览内容'} tabIndex={0}>{view==='source'?<p className="context-source-text">{text}</p>:<MarkdownContent text={text}/>} {children}</div></>;
}
