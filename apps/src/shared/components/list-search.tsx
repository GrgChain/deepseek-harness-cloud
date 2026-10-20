import {Button,Input} from 'antd';
import {RefreshCw,Search} from 'lucide-react';

export function ListSearch({label,placeholder,value,onChange,onSubmit,onRefresh,loading,refreshLabel='刷新'}:{
 label:string;placeholder:string;value:string;onChange:(value:string)=>void;onSubmit?:()=>void;
 onRefresh:()=>void;loading:boolean;refreshLabel?:string;
}){
 return <div className="list-search-controls">
  <Input role="searchbox" aria-label={label} placeholder={placeholder} prefix={<Search size={16} aria-hidden="true"/>} value={value} onChange={event=>onChange(event.target.value)} onPressEnter={event=>{if(!event.nativeEvent.isComposing)onSubmit?.();}} allowClear/>
  <Button aria-label={refreshLabel} icon={<RefreshCw size={16} aria-hidden="true"/>} loading={loading} onClick={onRefresh}>刷新</Button>
 </div>;
}
