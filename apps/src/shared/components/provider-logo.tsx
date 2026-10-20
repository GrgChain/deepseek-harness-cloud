import {Plug} from 'lucide-react';
import type {ReactNode} from 'react';

const providerLogos:Record<string,string>={
 openai:'openai',anthropic:'anthropic',gemini:'gemini',qwen:'qwen',
 deepseek:'deepseek',moonshot:'moonshot',zai:'zai',
 'custom-openai':'openai','custom-openai-responses':'openai','custom-anthropic':'anthropic',
};

export function ProviderLogo({provider,size=24}:{provider:string;size?:number}){
 const logo=providerLogos[provider];
 return <span className="provider-logo" aria-hidden="true" style={{width:size,height:size}}>{logo?<img src={`/provider-logos/${logo}.svg`} alt="" width={size} height={size}/>:<Plug size={size}/>}</span>;
}

export function ProviderLabel({provider,children}:{provider:string;children:ReactNode}){
 return <span className="provider-label"><ProviderLogo provider={provider} size={20}/><span>{children}</span></span>;
}
