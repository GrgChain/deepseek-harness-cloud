import {Alert,Form,Input,InputNumber,Tag} from 'antd';
import type {ContextConnection,EmbeddingConfig} from '../../api/context';

export const legacyEmbedding:EmbeddingConfig={provider:'local',model:'BAAI/bge-small-zh-v1.5',dimensions:512};
export const externalEmbedding:Partial<EmbeddingConfig>={provider:'openai_compatible',model:'',api_base:'',api_key:''};

export function EmbeddingFields({connection}:{connection:ContextConnection|'new'}){
 const existing=connection!=='new';const legacy=existing&&(!connection.embedding||connection.embedding.provider==='local');
 return <div className="context-embedding-fields">
  <div className="context-embedding-heading"><h3>Embedding模型</h3><Tag>{legacy?'旧版配置':'API 接入'}</Tag></div>
  <Form.Item name={['embedding','provider']} hidden><Input/></Form.Item>
  {legacy?<>
   <Form.Item name={['embedding','model']} label="Embedding模型名称"><Input readOnly/></Form.Item>
   <Form.Item name={['embedding','dimensions']} label="向量维度"><InputNumber disabled style={{width:'100%'}}/></Form.Item>
   <Alert type="info" showIcon message="此连接使用旧版本地模型。接入自建或云端 API 请新建连接并重新导入文档。"/>
  </>:<>
   <Form.Item name={['embedding','api_base']} label="Embedding服务地址" tooltip="填写平台可访问的 API Base URL，支持自建或云端 OpenAI 兼容的 /embeddings 接口。" rules={[{required:true,whitespace:true,message:'请输入Embedding服务地址'},{validator:(_,value:string)=>{if(!value)return Promise.resolve();try{const url=new URL(value);if(['http:','https:'].includes(url.protocol)&&!url.username&&!url.password&&!url.search&&!url.hash)return Promise.resolve();}catch{}return Promise.reject(new Error('填写有效的 HTTP(S) 地址，不要包含参数或凭据'));}}]}><Input placeholder="http://192.168.1.10:11434/v1" maxLength={1000} readOnly={existing} autoComplete="url"/></Form.Item>
   <Form.Item name={['embedding','model']} label="Embedding模型名称" rules={[{required:true,whitespace:true,max:256,message:'请输入Embedding服务提供的模型名称'}]}><Input placeholder="填写服务实际支持的模型名称" maxLength={256} readOnly={existing}/></Form.Item>
   <Form.Item name={['embedding','dimensions']} label="向量维度" rules={[{required:true,type:'number',min:1,max:32768,message:'请输入 1–32768 的整数'},{validator:(_,value:number)=>value===undefined||Number.isInteger(value)?Promise.resolve():Promise.reject(new Error('向量维度须为整数'))}]}><InputNumber placeholder="填写模型输出维度" min={1} max={32768} precision={0} disabled={existing} style={{width:'100%'}}/></Form.Item>
   <Form.Item name={['embedding','api_key']} label="Embedding服务 API Key" extra={existing?(connection.embedding_key_configured?'已保存凭据；留空保留，填写新值可更新。':'未保存 API Key；如服务需要认证请填写。'):undefined}><Input.Password placeholder="选填，按服务认证要求填写" maxLength={4096} autoComplete="new-password"/></Form.Item>
   <Alert type="info" showIcon message="文档和检索问题会发送到此 Embedding 服务。"/>
  </>}
 </div>;
}
