import {Tag} from 'antd';
import {ConnectorsClient} from '../../api/connectors';
export const client=new ConnectorsClient();
export const errorText=(error:unknown)=>error instanceof Error?error.message:'操作失败，请重试';
export const date=(value:string|null)=>value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'—';
export const states:Record<string,{label:string;color:string}>={UNVERIFIED:{label:'待验证',color:'default'},READY:{label:'可用',color:'green'},FAILED:{label:'不可用',color:'red'},AUTH_REQUIRED:{label:'需授权',color:'orange'},RUNNING:{label:'执行中',color:'blue'},SUCCEEDED:{label:'成功',color:'green'},TOOL_ERROR:{label:'工具执行失败',color:'red'},REJECTED:{label:'未执行',color:'default'},UNKNOWN:{label:'结果待确认',color:'orange'}};
export function StatusTag({status}:{status:string}){const value=states[status]??{label:status,color:'default'};return <Tag color={value.color}>{value.label}</Tag>;}
