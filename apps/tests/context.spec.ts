import {expect,test,type Page} from '@playwright/test';
const id='b3e2b3e2-1111-4111-8111-111111111111';
const user={id:'user-context',memberships:[{org_id:'org',role:'MEMBER',role_id:'member',role_name:'成员',status:'ACTIVE'}],permissions:['memory:read','memory:manage','knowledge:read','knowledge:manage'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const connection={id,kind:'memory',backend:'mem0',endpoint_id:'local-mem0',name:'项目记忆',version:1,enabled:true,status:'READY',capabilities:{search:true,write:true},verified_at:'2026-10-23T08:00:00Z',updated_at:'2026-10-23T08:00:00Z',integration:'NOT_CONNECTED'};
const entry={id:'test-memory',title:'发布约定',content:'星河项目每周三发布。',type:'note',source:'本人确认的项目约定',scope:'personal',version:1,status:'READY',created_at:'2026-10-23T08:00:00Z',updated_at:'2026-10-23T08:00:00Z'};
async function setup(page:Page,manage=true){
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:manage?user.permissions:['memory:read']}}));
 await page.route('**/v1/context/memory',r=>r.fulfill({json:{items:[connection],endpoints:[{id:'local-mem0',name:'本机测试 Mem0',backend:'mem0',data_use:'数据仅发送到独立本机测试服务'}],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/memory/${id}/list`,r=>r.fulfill({json:{items:[entry]}}));
 await page.route(`**/v1/context/memory/${id}/get`,r=>r.fulfill({json:entry}));
}
test('memory preview needs confirmation and search citations recheck authorization',async({page},info)=>{
 await setup(page);let writes=0;
 await page.route(`**/v1/context/memory/${id}/preview`,r=>{expect(r.request().postDataJSON()).toMatchObject({scope:'personal',content:'我偏好中文回答',title:'回答偏好'});return r.fulfill({json:{token:'preview-token',items:[{...entry,title:'回答偏好',content:'我偏好中文回答',conflict:false}],warnings:[],data_use:'数据仅发送到独立本机测试服务',expires_in:300}});});
 await page.route(`**/v1/context/memory/${id}/apply`,async r=>{expect(r.request().postDataJSON()).toMatchObject({token:'preview-token',mode:'skip',confirmed:true,expected_version:1});expect(await r.request().headerValue('x-csrf-token')).toBe(user.csrf_token);writes++;return r.fulfill({json:{applied:['test-memory'],skipped:[]}});});
 await page.route(`**/v1/context/memory/${id}/search`,r=>r.fulfill({json:{items:[{...entry,snippet:entry.content,position:'片段 1',score:.9}],status:'HITS'}}));
 await page.goto('/app/memory');await expect(page.getByRole('region',{name:'记忆管理'})).toBeVisible();await expect(page.getByText('未接入工作空间',{exact:true})).toHaveCount(0);
 const memoryLogo=page.getByRole('article',{name:'记忆连接 项目记忆',exact:true}).locator('img');await expect(memoryLogo).toHaveAttribute('src','/context-logos/mem0.svg');await expect.poll(()=>memoryLogo.evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth>0)).toBe(true);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('dialog',{name:connection.name,exact:true}).getByRole('button',{name:'添加记忆',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加记忆',exact:true});
 await editor.getByRole('textbox').nth(0).fill('回答偏好');await editor.getByRole('textbox').nth(1).fill('我偏好中文回答');await editor.getByRole('button',{name:/^预\s*览$/}).click();
 const preview=page.getByRole('dialog',{name:'确认发送到外部服务'});await expect(preview).toBeVisible();expect(writes).toBe(0);await preview.getByRole('button',{name:'确认保存'}).click();await expect(preview).toHaveCount(0);expect(writes).toBe(1);
 await page.getByRole('textbox',{name:'检索问题'}).fill('什么时候发布');await page.getByRole('button',{name:'试检索'}).click();
 await expect(page.getByText('检索结果 · 1')).toBeVisible();await page.screenshot({path:info.outputPath('external-memory.png'),fullPage:true,animations:'disabled'});
 await page.route(`**/v1/context/memory/${id}/get`,r=>r.fulfill({status:404,json:{code:'NOT_FOUND',message:'连接、内容或授权范围不存在'}}));
 await page.locator('.context-results').getByRole('button',{name:'发布约定'}).click();await expect(page.getByText('连接、内容或授权范围不存在',{exact:true})).toBeVisible();await expect(page.getByRole('dialog',{name:'发布约定'})).toHaveCount(0);
});
test('read-only connections hide content management and connection editing',async({page})=>{
 await setup(page,false);await page.goto('/app/memory');await expect(page.getByRole('button',{name:'添加记忆',exact:true})).toHaveCount(0);await page.getByRole('button',{name:'管理内容'}).click();await expect(page.getByRole('button',{name:'添加记忆'})).toHaveCount(0);await expect(page.getByRole('button',{name:'导出',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'删除',exact:true})).toHaveCount(0);
});

test('memory service picker filters services and preserves configuration after save failure',async({page},info)=>{
 await setup(page);let saves=0;
 await page.route('**/v1/context/memory',r=>{
  if(r.request().method()==='POST'){saves++;expect(r.request().postDataJSON()).toMatchObject({name:'我的项目记忆',endpoint_id:'local-mem0',credential:'test-only-key',enabled:true,expected_version:0});return r.fulfill({status:422,json:{code:'CONTEXT_INVALID',message:'请检查服务凭据'}});}
  return r.fulfill({json:{items:[connection],endpoints:[{id:'local-mem0',name:'本机测试 Mem0',backend:'mem0',data_use:'数据仅发送到独立本机测试服务'},{id:'local-qdrant',name:'知识库测试服务',backend:'qdrant',data_use:'知识库专用'}],bindings:[],integration:'NOT_CONNECTED'}});
 });
 await page.goto('/app/memory');await page.getByRole('button',{name:'添加记忆',exact:true}).click();
 const picker=page.getByRole('dialog',{name:'添加记忆连接',exact:true});
 await expect(picker.getByRole('article')).toHaveCount(3);await expect(picker.locator('.context-service-grid')).toHaveCSS('display','grid');
 for(const [name,file] of [['Mem0','mem0.svg'],['OpenViking','openviking.svg'],['Graphiti','graphiti.png']]){const logo=picker.getByRole('article',{name:'记忆服务 '+name,exact:true}).locator('img');await expect(logo).toHaveAttribute('src','/context-logos/'+file);await expect.poll(()=>logo.evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth>0)).toBe(true);}
 await expect(picker.getByText('Qdrant',{exact:true})).toHaveCount(0);
 await picker.getByRole('textbox',{name:'搜索记忆服务'}).fill('无匹配服务');await expect(picker.getByText('没有符合条件的服务')).toBeVisible();await picker.getByRole('button',{name:'清除搜索'}).click();
 await page.screenshot({path:info.outputPath('memory-service-picker.png'),fullPage:true,animations:'disabled'});
 await picker.getByRole('button',{name:'查看配置 Graphiti'}).click();
 let editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(editor.getByText('暂未接入',{exact:true})).toBeVisible();await expect(editor.getByRole('button',{name:/^保\s*存$/})).toBeDisabled();expect(saves).toBe(0);
 await editor.getByRole('button',{name:'返回选择'}).click();await page.getByRole('button',{name:'配置连接 Mem0',exact:true}).click();
 editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('Mem0 记忆');
 await expect(editor.getByText('本机测试 Mem0',{exact:true})).toBeVisible();await editor.getByText('本机测试 Mem0',{exact:true}).click();await expect(page.getByText('知识库测试服务',{exact:true})).toHaveCount(0);await editor.getByLabel('连接名称',{exact:true}).click();
 await editor.getByLabel('连接名称',{exact:true}).fill('我的项目记忆');await editor.getByLabel('服务凭据',{exact:true}).fill('test-only-key');
 await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor.getByText('请检查服务凭据',{exact:true})).toBeVisible();expect(saves).toBe(1);await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('我的项目记忆');await expect(editor.getByLabel('服务凭据',{exact:true})).toHaveValue('test-only-key');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('memory-connection-editor.png'),fullPage:true,animations:'disabled'});
});

test('memory validation brings the first incomplete field into view and focuses it',async({page},info)=>{
 await setup(page);await page.setViewportSize(info.project.name==='mobile'?{width:375,height:667}:{width:844,height:390});await page.emulateMedia({reducedMotion:'reduce'});let writes=0;
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname==='/v1/context/memory')writes++;});
 await page.goto('/app/memory');await page.getByRole('button',{name:'添加记忆',exact:true}).click();await page.getByRole('button',{name:'配置连接 Mem0',exact:true}).click();
 const editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});const name=editor.getByLabel('连接名称',{exact:true});const credential=editor.getByLabel('服务凭据',{exact:true});
 await name.clear();await editor.locator('.ant-modal-body').evaluate(el=>{el.scrollTop=el.scrollHeight;});await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(name).toBeFocused();await expect(name).toBeInViewport();expect(writes).toBe(0);
 await name.fill('我的记忆');await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(credential).toBeFocused();await expect(credential).toBeInViewport();expect(writes).toBe(0);
 expect(await credential.evaluate(el=>{const box=el.getBoundingClientRect();const body=el.closest('.ant-modal-body')!.getBoundingClientRect();return box.top>=body.top&&box.bottom<=body.bottom;})).toBe(true);expect(await editor.getByRole('button',{name:/^保\s*存$/}).evaluate(el=>{const box=el.getBoundingClientRect();return box.top>=0&&box.bottom<=innerHeight;})).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('context-validation-focus.png'),fullPage:true,animations:'disabled'});
});

test('knowledge document list searches and pages through all matching records',async({page})=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'milvus',name:'项目文档'};
 const documents=Array.from({length:1025},(_,i)=>({...entry,id:`doc-${i}`,title:`文档 ${String(i).padStart(4,'0')}`}));const requests:{page:number;page_size:number;query:string}[]=[];
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>{const input=r.request().postDataJSON();requests.push(input);const filtered=documents.filter(item=>item.title.includes(input.query));return r.fulfill({json:{items:filtered.slice((input.page-1)*input.page_size,input.page*input.page_size),total:filtered.length}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();const drawer=page.getByRole('dialog',{name:'项目文档'});const pagination=drawer.getByRole('navigation',{name:'文档分页'});
 await expect(pagination.getByText('1025',{exact:true})).toBeVisible();await expect(drawer.getByRole('button',{name:'文档 0000'})).toBeVisible();
 await pagination.getByRole('button',{name:'下一页'}).click();await expect(drawer.getByRole('button',{name:'文档 0010'})).toBeVisible();
 await drawer.getByRole('textbox',{name:'搜索文档名称'}).fill('1024');await expect(drawer.getByRole('button',{name:'文档 1024'})).toBeVisible();await expect(pagination.getByText('1',{exact:true}).first()).toBeVisible();
 expect(requests.at(-1)).toMatchObject({page:1,page_size:10,query:'1024'});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('knowledge creation uses only its selected backend and blocks edits while saving',async({page},info)=>{
 await setup(page);let saved:Record<string,unknown>|undefined;let finish=()=>{};
 const pending=new Promise<void>(resolve=>{finish=resolve;});
 const qdrant={...connection,kind:'knowledge',backend:'qdrant',endpoint_id:'local-qdrant',name:'个人资料',enabled:false,status:'UNVERIFIED'};
 await page.route('**/v1/context/knowledge',async r=>{
  if(r.request().method()==='POST'){saved=r.request().postDataJSON();await pending;return r.fulfill({json:qdrant});}
  return r.fulfill({json:{items:saved?[qdrant]:[],endpoints:[{id:'local-qdrant',name:'Qdrant 测试服务',backend:'qdrant',custom_service:true,data_use:'所选服务说明'},{id:'local-mem0',name:'Mem0 测试服务',backend:'mem0',data_use:'记忆专用'}],bindings:[],integration:'NOT_CONNECTED'}});
 });
 try{
  await page.goto('/app/knowledge');await page.getByRole('button',{name:'添加知识库',exact:true}).click();const picker=page.getByRole('dialog',{name:'添加知识库连接',exact:true});await expect(picker.getByRole('article')).toHaveCount(3);await expect(picker.getByText('Mem0',{exact:true})).toHaveCount(0);
  await expect(picker.getByRole('article',{name:'知识库服务 Qdrant'}).getByText('可添加连接',{exact:true})).toBeVisible();await expect(picker.getByRole('article',{name:'知识库服务 Milvus'}).getByText('暂未接入',{exact:true})).toBeVisible();
  for(const [name,file] of [['Milvus','milvus.svg'],['Weaviate','weaviate.png'],['Qdrant','qdrant.png']]){const logo=picker.getByRole('article',{name:'知识库服务 '+name,exact:true}).locator('img');await expect(logo).toHaveAttribute('src','/context-logos/'+file);await expect.poll(()=>logo.evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth>0)).toBe(true);}
  await page.screenshot({path:info.outputPath('knowledge-service-picker.png'),fullPage:true,animations:'disabled'});
  await picker.getByRole('button',{name:'配置连接 Qdrant',exact:true}).click();
  const editor=page.getByRole('dialog',{name:'添加知识库连接',exact:true});await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('Qdrant 知识库');await expect(editor.locator('.context-service-logo')).toHaveAttribute('src','/context-logos/qdrant.png');await editor.getByLabel('Qdrant 服务地址',{exact:true}).fill('https://qdrant.example');await editor.getByLabel('连接名称',{exact:true}).fill('个人资料');await editor.getByLabel('服务凭据',{exact:true}).fill('knowledge-test-key');await editor.getByRole('switch',{name:'启用连接',exact:true}).uncheck();
  await editor.getByLabel('Embedding服务地址',{exact:true}).fill('http://192.168.1.10:11434/v1');await editor.getByLabel('Embedding模型名称',{exact:true}).fill('my-local-model');await editor.getByLabel('向量维度',{exact:true}).fill('768');
  await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect.poll(()=>saved).toBeDefined();await expect(editor.getByLabel('连接名称',{exact:true})).toBeDisabled();await expect(editor.getByRole('switch',{name:'启用连接',exact:true})).toBeDisabled();expect(saved).toMatchObject({name:'个人资料',endpoint_id:'local-qdrant',credential:'knowledge-test-key',enabled:false,expected_version:0});finish();await expect(editor).toHaveCount(0);
  const row=page.getByRole('article',{name:'知识库连接 个人资料',exact:true});await expect(row.locator('img')).toHaveAttribute('src','/context-logos/qdrant.png');await expect.poll(()=>row.locator('img').evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth>0)).toBe(true);await expect(row.getByText('已停用',{exact:true})).toBeVisible();await expect(row.getByRole('button',{name:/^验\s*证$/})).toBeDisabled();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('knowledge-created.png'),fullPage:true,animations:'disabled'});
 }finally{finish();}
});

for(const backend of ['weaviate','qdrant'])test(`${backend} accepts a custom service and preserves its address on edit`,async({page})=>{
 await setup(page);let saved:Record<string,unknown>|undefined;let created=false;const name=backend==='weaviate'?'Weaviate':'Qdrant';const url=`https://${backend}.example`;
 const native={...connection,kind:'knowledge',backend,endpoint_id:'local-'+backend,name:'自有 '+name,service_url:url,status:'UNVERIFIED',embedding:{provider:'openai_compatible',api_base:'https://embed.example/v1',model:'model',dimensions:768}};
 await page.route('**/v1/context/knowledge',r=>{if(r.request().method()==='POST'){saved=r.request().postDataJSON();created=true;return r.fulfill({json:native});}return r.fulfill({json:{items:created?[native]:[],endpoints:[{id:'local-'+backend,name:'接入节点',backend,custom_service:true,data_use:'测试'}],bindings:[],integration:'NOT_CONNECTED'}});});
 await page.route('**/v1/context/knowledge/'+id,r=>{expect(r.request().postDataJSON()).toMatchObject({service_url:url,credential:''});return r.fulfill({json:{...native,version:2}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'添加知识库',exact:true}).click();await page.getByRole('button',{name:'配置连接 '+name,exact:true}).click();const editor=page.getByRole('dialog',{name:'添加知识库连接',exact:true});
 await editor.getByLabel('连接名称',{exact:true}).fill(native.name);await editor.getByLabel(name+' 服务地址',{exact:true}).fill(url);await editor.getByLabel('服务凭据',{exact:true}).fill('native-api-key');await editor.getByLabel('Embedding服务地址',{exact:true}).fill(native.embedding.api_base);await editor.getByLabel('Embedding模型名称',{exact:true}).fill(native.embedding.model);await editor.getByLabel('向量维度',{exact:true}).fill('768');await editor.getByRole('button',{name:'保存',exact:true}).click();await expect(editor).toHaveCount(0);expect(saved).toMatchObject({service_url:url,credential:'native-api-key',endpoint_id:'local-'+backend});
 await page.getByRole('button',{name:native.name+' 更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();const settings=page.getByRole('dialog',{name:'连接设置',exact:true});await expect(settings.getByLabel(name+' 服务地址',{exact:true})).toHaveAttribute('readonly','');await expect(settings.getByLabel('服务凭据',{exact:true})).toHaveValue('');await settings.getByRole('button',{name:'保存',exact:true}).click();await expect(settings).toHaveCount(0);
});

test('Milvus creation accepts a custom address and retains settings after a rejected save',async({page},info)=>{
 await setup(page);let saved:Record<string,unknown>|undefined;let attempts=0;
 const native={...connection,kind:'knowledge',backend:'milvus',endpoint_id:'milvus-bridge',name:'自有 Milvus',service_url:'https://my-milvus.example:19530',status:'UNVERIFIED',embedding:{provider:'openai_compatible',api_base:'http://192.168.1.10:11434/v1',model:'my-local-model',dimensions:768}};
 const endpoints=[{id:'milvus-bridge',name:'平台接入节点',backend:'milvus',custom_service:true,data_use:'向量写入你填写的 Milvus 服务。'}];
 await page.route('**/v1/context/knowledge',async r=>{
  if(r.request().method()==='POST'){saved=r.request().postDataJSON();attempts++;return attempts===1?r.fulfill({status:422,json:{code:'CONTEXT_INVALID',message:'请检查 Milvus 地址'}}):r.fulfill({json:native});}
  return r.fulfill({json:{items:attempts>1?[native]:[],endpoints,bindings:[],integration:'NOT_CONNECTED'}});
 });
 await page.route('**/v1/context/knowledge/'+id,async r=>{expect(r.request().postDataJSON()).toMatchObject({service_url:native.service_url,credential:'',expected_version:1});return r.fulfill({json:{...native,version:2}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'添加知识库',exact:true}).click();await page.getByRole('button',{name:'配置连接 Milvus',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加知识库连接',exact:true});
 await expect(editor.getByLabel('Milvus 服务地址',{exact:true})).toHaveValue('');await expect(editor.getByLabel('接入节点',{exact:true})).toBeHidden();await editor.getByLabel('连接名称',{exact:true}).fill(native.name);
 await expect(editor.getByText('平台本地模型',{exact:true})).toHaveCount(0);await editor.getByLabel('Embedding服务地址',{exact:true}).fill('http://192.168.1.10:11434/v1');await editor.getByLabel('Embedding模型名称',{exact:true}).fill('my-local-model');await editor.getByLabel('向量维度',{exact:true}).fill('768');
 await page.screenshot({path:info.outputPath('milvus-connection-editor.png'),fullPage:true,animations:'disabled'});
 await editor.getByLabel('Milvus 服务地址',{exact:true}).fill('https://root:secret@my-milvus.example');await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor.getByText('填写 http:// 或 https:// 地址，不要包含路径、参数或凭据',{exact:true})).toBeVisible();expect(attempts).toBe(0);
 await editor.getByLabel('Milvus 服务地址',{exact:true}).fill(native.service_url);await editor.getByLabel('服务凭据',{exact:true}).fill('root:own-token');await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor.getByText('请检查 Milvus 地址',{exact:true})).toBeVisible();await expect(editor.getByLabel('Milvus 服务地址',{exact:true})).toHaveValue(native.service_url);await expect(editor.getByLabel('服务凭据',{exact:true})).toHaveValue('root:own-token');
 expect(saved).toMatchObject({name:native.name,service_url:native.service_url,credential:'root:own-token',endpoint_id:'milvus-bridge',enabled:true,expected_version:0,embedding:{provider:'openai_compatible',api_base:'http://192.168.1.10:11434/v1',model:'my-local-model',dimensions:768,api_key:''}});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('milvus-custom-service.png'),fullPage:true,animations:'disabled'});
 await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor).toHaveCount(0);await page.getByRole('button',{name:native.name+' 更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();const settings=page.getByRole('dialog',{name:'连接设置',exact:true});await expect(settings.getByLabel('Milvus 服务地址',{exact:true})).toHaveValue(native.service_url);await expect(settings.getByLabel('Milvus 服务地址',{exact:true})).toHaveAttribute('readonly','');await expect(settings.getByLabel('服务凭据',{exact:true})).toHaveValue('');await settings.getByRole('button',{name:/^保\s*存$/}).click();await expect(settings).toHaveCount(0);
});

test('knowledge selects an embedding API, retains failed input and only edits its saved key',async({page},info)=>{
 await setup(page);let created=false;let creates=0;let submitted:Record<string,unknown>={};
 const embedding={provider:'openai_compatible',model:'my-embedding-model',api_base:'https://embed.example/v1',dimensions:768};
 const native={...connection,kind:'knowledge',backend:'milvus',endpoint_id:'milvus-bridge',name:'外部向量模型',service_url:'https://my-milvus.example',embedding,embedding_key_configured:true,status:'UNVERIFIED'};
 const endpoints=[{id:'milvus-bridge',name:'平台接入',backend:'milvus',custom_service:true,data_use:'本机中文模型生成向量，无外部模型调用费用。'}];
 await page.route('**/v1/context/knowledge',r=>{
  if(r.request().method()==='POST'){submitted=r.request().postDataJSON();creates++;if(creates===1)return r.fulfill({status:422,json:{code:'CONTEXT_INVALID',message:'请检查Embedding服务配置'}});created=true;return r.fulfill({json:native});}
  return r.fulfill({json:{items:created?[native]:[],endpoints,bindings:[],integration:'NOT_CONNECTED'}});
 });
 await page.route('**/v1/context/knowledge/'+id,r=>{expect(r.request().postDataJSON()).toMatchObject({embedding:{...embedding,api_key:''},expected_version:1});return r.fulfill({json:{...native,version:2}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'添加知识库',exact:true}).click();await page.getByRole('button',{name:'配置连接 Milvus',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加知识库连接',exact:true});
 await editor.getByLabel('连接名称',{exact:true}).fill(native.name);await editor.getByLabel('Milvus 服务地址',{exact:true}).fill(native.service_url);await expect(editor.getByRole('heading',{name:'Embedding模型',exact:true})).toBeVisible();await expect(editor.getByText('平台本地模型',{exact:true})).toHaveCount(0);
 await editor.getByRole('button',{name:'保存',exact:true}).click();await expect(editor.getByText('请输入Embedding服务地址',{exact:true})).toBeVisible();expect(creates).toBe(0);
 await editor.getByLabel('Embedding服务地址',{exact:true}).fill(embedding.api_base);await editor.getByLabel('Embedding模型名称',{exact:true}).fill(embedding.model);await editor.getByLabel('向量维度',{exact:true}).fill('768');await editor.getByLabel('Embedding服务 API Key',{exact:true}).fill('private-embedding-key');await expect(editor.getByText('文档和检索问题会发送到此 Embedding 服务。',{exact:true})).toBeVisible();await expect(editor.getByText('本机中文模型生成向量，无外部模型调用费用。',{exact:true})).toHaveCount(0);
 await editor.getByRole('button',{name:'保存',exact:true}).click();await expect(editor.getByText('请检查Embedding服务配置',{exact:true})).toBeVisible();await expect(editor.getByLabel('Embedding服务 API Key',{exact:true})).toHaveValue('private-embedding-key');expect(submitted).toMatchObject({embedding:{...embedding,api_key:'private-embedding-key'}});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('embedding-api-selection.png'),fullPage:true,animations:'disabled'});await editor.getByRole('button',{name:'保存',exact:true}).click();await expect(editor).toHaveCount(0);
 await page.getByRole('button',{name:native.name+' 更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();const settings=page.getByRole('dialog',{name:'连接设置',exact:true});await expect(settings.getByLabel('Embedding服务地址',{exact:true})).toHaveAttribute('readonly','');await expect(settings.getByLabel('Embedding模型名称',{exact:true})).toHaveValue(embedding.model);await expect(settings.getByLabel('Embedding模型名称',{exact:true})).toHaveAttribute('readonly','');await expect(settings.getByLabel('向量维度',{exact:true})).toBeDisabled();await expect(settings.getByLabel('Embedding服务 API Key',{exact:true})).toHaveValue('');await expect(settings.getByText('已保存凭据；留空保留，填写新值可更新。',{exact:true})).toBeVisible();await settings.getByRole('button',{name:'保存',exact:true}).click();await expect(settings).toHaveCount(0);
});

test('existing built-in embedding settings stay fixed when editing a legacy connection',async({page})=>{
 await setup(page);const legacy={...connection,kind:'knowledge',backend:'milvus',endpoint_id:'milvus-bridge',name:'旧知识库',service_url:'https://my-milvus.example'};
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[legacy],endpoints:[{id:'milvus-bridge',name:'平台接入',backend:'milvus',custom_service:true,data_use:'测试'}],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route('**/v1/context/knowledge/'+id,r=>{expect(r.request().postDataJSON()).toMatchObject({expected_version:1,embedding:{provider:'local',model:'BAAI/bge-small-zh-v1.5',dimensions:512}});return r.fulfill({json:{...legacy,version:2}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:legacy.name+' 更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();const editor=page.getByRole('dialog',{name:'连接设置',exact:true});
 await expect(editor.getByText('此连接使用旧版本地模型。接入自建或云端 API 请新建连接并重新导入文档。',{exact:true})).toBeVisible();await expect(editor.getByLabel('Embedding服务地址',{exact:true})).toHaveCount(0);await expect(editor.getByLabel('Embedding模型名称',{exact:true})).toHaveValue('BAAI/bge-small-zh-v1.5');await expect(editor.getByLabel('Embedding模型名称',{exact:true})).toHaveAttribute('readonly','');await expect(editor.getByLabel('向量维度',{exact:true})).toBeDisabled();await editor.getByRole('button',{name:'保存',exact:true}).click();await expect(editor).toHaveCount(0);
});

test('knowledge search opens a window with semantic, keyword and hybrid modes',async({page},info)=>{
 if(info.project.name==='mobile')await page.setViewportSize({width:320,height:640});await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};const modes:string[]=[];const limits:number[]=[];
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[entry]}}));
 await page.route(`**/v1/context/knowledge/${id}/search`,r=>{const body=r.request().postDataJSON();modes.push(body.search_mode);limits.push(body.limit);expect(body.scope).toBe('personal');expect(body.query).toBe('星河');if(body.search_mode==='hybrid')return r.fulfill({status:503,json:{code:'CONTEXT_UNAVAILABLE',message:'服务暂不可用'}});return r.fulfill({json:{items:body.search_mode==='keyword'?[]:[{...entry,snippet:entry.content.repeat(100),position:'片段 1',score:.9}],status:'HITS'}});});
 await page.route(`**/v1/context/knowledge/${id}/get`,r=>r.fulfill({json:entry}));
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'知识库检索',exact:true}).click();const modal=page.getByRole('dialog',{name:'知识库检索',exact:true});
 await expect(modal.getByLabel('检索方式')).toBeVisible();await modal.getByLabel('检索问题').fill('星河');await modal.getByRole('button',{name:'检索',exact:true}).click();await expect(modal.getByText('检索结果 · 1')).toHaveCount(0);await expect(modal.getByText(entry.title,{exact:true})).toBeVisible();await expect(modal.getByText('相关度 0.900')).toBeVisible();await expect(modal.getByText(entry.content.repeat(100),{exact:true})).toHaveCount(0);await modal.getByText(entry.title,{exact:true}).click();const content=modal.getByRole('region',{name:entry.title+' 的检索内容'});const resultArea=modal.getByRole('region',{name:'检索结果',exact:true});await expect(content).toBeVisible();await expect.poll(()=>resultArea.evaluate(el=>el.scrollHeight>el.clientHeight)).toBe(true);await expect(content).toHaveCSS('overflow-y','visible');await expect(content.getByRole('button',{name:'查看原文'})).toHaveCount(0);const searchBox=modal.getByLabel('检索问题');const searchY=(await searchBox.boundingBox())!.y;await resultArea.focus();await page.keyboard.press('End');await expect.poll(()=>resultArea.evaluate(el=>el.scrollTop>0)).toBe(true);expect((await searchBox.boundingBox())!.y).toBe(searchY);await page.screenshot({path:info.outputPath('knowledge-search-scroll.png'),animations:'disabled'});await modal.getByRole('button',{name:'查看原文',exact:true}).click();await expect(page.getByRole('dialog',{name:entry.title,exact:true})).toBeVisible();await expect(modal).not.toBeVisible();await page.getByRole('dialog',{name:entry.title,exact:true}).getByRole('button',{name:/^关\s*闭$/}).click();await expect(modal).toBeVisible();await expect(modal.getByText(entry.title,{exact:true})).toBeVisible();
 await modal.locator('.context-search-limit .ant-select-selector').click();await page.getByRole('option',{name:'10 条',exact:true}).click();await modal.getByText('全文检索',{exact:true}).click();await modal.getByRole('button',{name:'检索',exact:true}).click();await expect(modal.getByText('没有命中授权范围内的内容')).toBeVisible();await modal.locator('.context-search-limit .ant-select-selector').click();await page.getByRole('option',{name:'20 条',exact:true}).click();await modal.getByText('混合检索',{exact:true}).click();await modal.getByRole('button',{name:'检索',exact:true}).click();await expect(modal.getByText('服务暂不可用',{exact:true})).toBeVisible();expect(modes).toEqual(['semantic','keyword','hybrid']);expect(limits).toEqual([5,10,20]);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await modal.getByText('语义检索',{exact:true}).click();await modal.getByRole('button',{name:'检索',exact:true}).click();await expect(modal.getByText('检索结果 · 1')).toHaveCount(0);await expect(modal.getByText(entry.title,{exact:true})).toBeVisible();await modal.getByRole('button',{name:'全部展开'}).click();await expect(modal.getByText(entry.content.repeat(100),{exact:true})).toBeVisible();await modal.getByRole('button',{name:'全部收起'}).click();await expect(modal.getByText(entry.content.repeat(100),{exact:true})).not.toBeVisible();await modal.getByRole('button',{name:'检索',exact:true}).hover();await page.screenshot({path:info.outputPath('knowledge-search.png'),animations:'disabled'});
});

test('hybrid search distinguishes keyword matches, vector similarity and fusion scores',async({page})=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));
 await page.route(`**/v1/context/knowledge/${id}/search`,r=>r.fulfill({json:{items:[{...entry,title:'双路结果',snippet:'营业收入',position:'片段 1',section:'财务 / 上半年',score:.8,fusion_score:.0325,match_methods:['keyword','semantic']},{...entry,id:'keyword',title:'关键词结果',snippet:'收入',position:'片段 1',fusion_score:.016,match_methods:['keyword']}]}}));
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'知识库检索',exact:true}).click();const search=page.getByRole('dialog',{name:'知识库检索'});
 await search.getByText('混合检索',{exact:true}).click();await search.getByLabel('检索问题').fill('营业收入');await search.getByRole('button',{name:'检索',exact:true}).click();
 const both=search.locator('.ant-collapse-item').filter({hasText:'双路结果'});await expect(both).toContainText('财务 / 上半年');await expect(both).toContainText('关键词匹配');await expect(both).toContainText('向量相似度 0.800');await expect(both).toContainText('融合分数 0.0325');
 const keyword=search.locator('.ant-collapse-item').filter({hasText:'关键词结果'});await expect(keyword).toContainText('关键词匹配');await expect(keyword).not.toContainText('向量相似度');await expect(keyword).not.toContainText('精确匹配');
 await search.getByRole('button',{name:'最低相关度说明'}).click();await expect(page.getByText('全文检索不使用相关度；混合检索中的关键词结果不受此条件限制。')).toBeVisible();
});

test('ready knowledge documents can be rebuilt with the current version',async({page})=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};let rebuilt=0;
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[entry]}}));
 await page.route(`**/v1/context/knowledge/${id}/rebuild`,r=>{expect(r.request().postDataJSON()).toMatchObject({record_id:entry.id,record_version:entry.version,confirmed:true});rebuilt++;return r.fulfill({json:{rebuilt:true}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:/^重\s*建$/}).click();await expect.poll(()=>rebuilt).toBe(1);
});

test('knowledge search sends optional similarity threshold only for vector results',async({page},info)=>{
 if(info.project.name==='mobile')await page.setViewportSize({width:320,height:640});await setup(page);
 const knowledge={...connection,kind:'knowledge',backend:'milvus',name:'项目文档'};const calls:Record<string,unknown>[]=[];
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[],total:0}}));
 await page.route(`**/v1/context/knowledge/${id}/search`,r=>{calls.push(r.request().postDataJSON());return r.fulfill({json:{items:[],status:'NO_HITS'}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容'}).click();await page.getByRole('button',{name:'知识库检索'}).click();const modal=page.getByRole('dialog',{name:'知识库检索'});
 const threshold=modal.getByRole('spinbutton',{name:'最低相关度',exact:true});await expect(threshold).toHaveValue('');const thresholdBounds=await threshold.boundingBox();const limitBounds=await modal.locator('.context-search-limit').boundingBox();expect(thresholdBounds!.x).toBeGreaterThan(limitBounds!.x);await modal.getByLabel('检索问题').fill('发布');await modal.getByRole('button',{name:'检索',exact:true}).click();await expect.poll(()=>calls.length).toBe(1);expect(calls[0]).not.toHaveProperty('min_similarity');
 await threshold.fill('0.75');await threshold.blur();await modal.getByRole('button',{name:'检索',exact:true}).click();await expect.poll(()=>calls.length).toBe(2);expect(calls[1]).toMatchObject({search_mode:'semantic',min_similarity:0.75});
 await modal.getByText('全文检索',{exact:true}).click();await expect(threshold).toBeDisabled();await modal.getByRole('button',{name:'检索',exact:true}).click();await expect.poll(()=>calls.length).toBe(3);expect(calls[2]).not.toHaveProperty('min_similarity');
 await modal.getByText('混合检索',{exact:true}).click();await expect(threshold).toBeEnabled();const help=modal.getByRole('button',{name:'最低相关度说明'});await expect(page.getByText('取值范围 -1 至 1，留空不过滤。')).not.toBeVisible();await help.click();await expect(help).toHaveAttribute('aria-expanded','true');await expect(page.getByText('取值范围 -1 至 1，留空不过滤。')).toBeVisible();await expect(page.getByText('全文检索不使用相关度；混合检索中的关键词结果不受此条件限制。')).toBeVisible();await help.click();await expect(help).toHaveAttribute('aria-expanded','false');await modal.getByRole('button',{name:'检索',exact:true}).click();await expect.poll(()=>calls.length).toBe(4);expect(calls[3]).toMatchObject({search_mode:'hybrid',min_similarity:0.75});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('original document remains readable and scrollable on small screens',async({page},info)=>{
 if(info.project.name==='mobile')await page.setViewportSize({width:320,height:640});await setup(page);
 const knowledge={...connection,kind:'knowledge',backend:'milvus',name:'项目文档'};const longTitle='项目文档'.repeat(25);const longSource='工作数据/'.repeat(35);const original={...entry,title:longTitle,source:longSource,content:'发布安排：每周三。\n'.repeat(200)};
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[original],total:1}}));await page.route(`**/v1/context/knowledge/${id}/get`,r=>r.fulfill({json:original}));
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容'}).click();await page.getByRole('button',{name:longTitle,exact:true}).click();const modal=page.getByRole('dialog',{name:longTitle});const content=modal.getByRole('region',{name:'原文内容'});
 await expect(content).toContainText('发布安排：每周三。');await expect(modal.getByText(longSource,{exact:false})).toBeVisible();await expect(modal.getByText('更新于',{exact:true})).toBeVisible();await expect.poll(()=>content.evaluate(el=>el.scrollHeight>el.clientHeight)).toBe(true);await content.focus();await page.keyboard.press('End');await expect.poll(()=>content.evaluate(el=>el.scrollTop>0)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);const close=modal.getByRole('button',{name:/^关\s*闭$/});const closeBounds=await close.boundingBox();expect(closeBounds!.y+closeBounds!.height).toBeLessThanOrEqual(page.viewportSize()!.height);await page.screenshot({path:info.outputPath('original-document.png'),animations:'disabled'});await close.click();await expect(modal).toHaveCount(0);
});

test('workbench navigation opens memory and knowledge in the portal',async({page},info)=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 let popups=0;page.on('popup',()=>popups++);
 await page.goto('/app/memory');await expect(page.getByRole('region',{name:'记忆管理'})).toBeVisible();
 if(info.project.name==='mobile')await page.getByRole('button',{name:'打开导航'}).click();
 await page.getByRole('link',{name:'知识库',exact:true}).filter({visible:true}).click();
 await expect(page).toHaveURL(/\/app\/knowledge$/);await expect(page.getByRole('region',{name:'知识库管理'})).toBeVisible();
 await expect(page.getByText('未接入工作空间',{exact:true})).toHaveCount(0);expect(popups).toBe(0);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('knowledge management has no manual workspace association control',async({page})=>{
 await setup(page);
 const knowledge={...connection,kind:'knowledge',backend:'milvus',name:'Milvus知识库'};
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'instances:read','instances:configure']}}));
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[{instance_id:'workspace-id',connection_id:id,enabled:true,version:1}],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));
 await page.route('**/v1/instances?**',r=>r.fulfill({json:{items:[{id:'workspace-id',name:'测试工作空间',observed_state:'RUNNING'}],total:1}}));
 let bindings=0;page.on('request',r=>{if(new URL(r.url()).pathname.endsWith('/binding'))bindings++;});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();
 const detail=page.getByRole('dialog',{name:'Milvus知识库'});
 await expect(detail.getByText('关联工作空间',{exact:true})).toHaveCount(0);
 await expect(detail.getByRole('switch',{name:/关联/})).toHaveCount(0);
 await expect(detail.getByLabel('内容范围')).toBeVisible();await expect(detail.getByRole('button',{name:'添加文档'})).toBeVisible();
 expect(bindings).toBe(0);
});

test('memory management retains workspace association',async({page})=>{
 await setup(page);await page.route('**/v1/instances?**',r=>r.fulfill({json:{items:[],total:0}}));
 await page.goto('/app/memory');await page.getByRole('button',{name:'管理内容',exact:true}).click();
 await expect(page.getByText('关联工作空间',{exact:true})).toBeVisible();
});

for(const account of ['admin','member'])test(`live workbench gives ${account} personal memory and knowledge navigation`,async({page},info)=>{
 test.skip(process.env.HC_REAL_CONTEXT!=='true','requires the local platform and built-in accounts');
 const {readFileSync}=await import('node:fs');
 await page.goto('/auth/login?return_to=%2Fapp%2Fmemory');await page.getByLabel('邮箱',{exact:true}).fill(account+'@harness.local');await page.getByLabel('密码',{exact:true}).fill(readFileSync('../.local/auth/'+account+'-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/memory$/);await expect(page.getByRole('region',{name:'记忆管理'})).toBeVisible();
 const me=await(await page.request.get('/v1/me')).json();for(const p of ['memory:read','memory:manage','knowledge:read','knowledge:manage'])expect(me.permissions).toContain(p);
 await expect(page.getByRole('button',{name:'添加记忆',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'添加记忆',exact:true}).click();const picker=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(picker.getByRole('article')).toHaveCount(3);await picker.getByRole('button',{name:'配置连接 Mem0',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('Mem0 记忆');await expect(editor.getByLabel('服务凭据',{exact:true})).toBeVisible();await expect(editor).toHaveCSS('transform','none');await expect(editor).toHaveCSS('opacity','1');await page.screenshot({path:info.outputPath(account+'-memory-editor.png'),fullPage:true,animations:'disabled'});await editor.getByRole('button',{name:/^取\s*消$/}).click();
 if(info.project.name==='mobile')await page.getByRole('button',{name:'打开导航'}).click();
 await page.getByRole('link',{name:'知识库',exact:true}).filter({visible:true}).click();
 await expect(page.getByRole('region',{name:'知识库管理'})).toBeVisible();await expect(page.getByRole('button',{name:'添加知识库',exact:true})).toBeVisible();
 await page.screenshot({path:info.outputPath(account+'-workbench-context.png'),fullPage:true,animations:'disabled'});
});

for(const backend of (process.env.HC_CONTEXT_UI_BACKENDS??'mem0,qdrant').split(','))test(`live platform uses ${backend} with an explicitly granted test role`,async({page,browser,baseURL},info)=>{
 test.skip(process.env.HC_REAL_CONTEXT!=='true'||info.project.name!=='desktop','requires the local external Docker services');test.setTimeout(180_000);page.setDefaultTimeout(15_000);
 test.skip(['milvus','weaviate','qdrant'].includes(backend)&&!process.env.HC_CONTEXT_TEST_EMBEDDING_FILE,'knowledge requires an explicitly configured Embedding API');
 const {readFileSync}=await import('node:fs');const {randomUUID,randomBytes}=await import('node:crypto');const suffix=randomUUID().slice(0,8);const password=randomBytes(24).toString('base64url');
 const adminContext=await browser.newContext({ignoreHTTPSErrors:true,baseURL:baseURL});
 const admin=await adminContext.newPage();admin.setDefaultTimeout(15_000);let member:{id:string;display_name:string;version:number}|null=null;let role:{id:string;version:number}|null=null;
 const created:{id:string;kind:string;name:string;version:number}[]=[];let headers:Record<string,string>={};let adminHeaders:Record<string,string>={};
 async function login(target:Page,email:string,secret:string){await target.goto('/auth/login?return_to=%2Fapp%2Fmemory');await target.locator('input[name=login]').fill(email);await target.locator('input[name=password]').fill(secret);await target.getByRole('button',{name:'登录',exact:true}).click();await expect(target).toHaveURL(/\/app\/memory$/);return (await target.request.get('/v1/me')).json();}
 try{
  const a=await login(admin,'admin@harness.local',readFileSync('../.local/auth/admin-password','utf8').trim());adminHeaders={Origin:new URL(admin.url()).origin,'X-CSRF-Token':a.csrf_token};
  let response=await admin.request.post('/v1/roles',{headers:{...adminHeaders,'Idempotency-Key':randomUUID()},data:{code:'ctx_'+suffix,name:'外接服务验证 '+suffix,description:'独立测试账号专用',permissions:['memory:read','memory:manage','knowledge:read','knowledge:manage'],expected_version:0}});expect(response.status()).toBe(201);role=await response.json();
  response=await admin.request.post('/v1/users',{headers:{...adminHeaders,'Idempotency-Key':randomUUID()},data:{display_name:'外接测试 '+suffix,email:'ctx-'+suffix+'@harness.local',identity_source:'local',role_id:role!.id,initial_password:password}});expect(response.status()).toBe(201);member=await response.json();
  const me=await login(page,'ctx-'+suffix+'@harness.local',password);headers={Origin:new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
  console.log('Real external backend:',backend);
   const kind=['mem0','openviking','graphiti'].includes(backend)?'memory':'knowledge';const names:Record<string,string>={mem0:'Mem0',openviking:'OpenViking',graphiti:'Graphiti',milvus:'Milvus',weaviate:'Weaviate',qdrant:'Qdrant'};
   await page.goto('/app/'+kind);await page.getByRole('button',{name:kind==='memory'?'添加记忆':'添加知识库',exact:true}).click();await page.getByRole('button',{name:'配置连接 '+names[backend],exact:true}).click();const editor=page.getByRole('dialog',{name:kind==='memory'?'添加记忆连接':'添加知识库连接',exact:true});await editor.getByLabel('连接名称',{exact:true}).fill('真实测试 '+backend+' '+suffix);if(backend==='milvus'){await editor.getByLabel('Milvus 服务地址',{exact:true}).fill(process.env.HC_CONTEXT_TEST_MILVUS_URL??'http://hc-context-milvus-1:19530');}else if(['weaviate','qdrant'].includes(backend)){await editor.getByLabel(names[backend]+' 服务地址',{exact:true}).fill('http://'+backend+':'+(backend==='weaviate'?'8080':'6333'));await editor.getByLabel('服务凭据',{exact:true}).fill(readFileSync('../.local/context/test-key','utf8').trim());}else await editor.getByLabel('服务凭据',{exact:true}).fill(readFileSync('../.local/context/test-key','utf8').trim());
   if(kind==='knowledge'&&process.env.HC_CONTEXT_TEST_EMBEDDING_FILE){const config=JSON.parse(readFileSync(process.env.HC_CONTEXT_TEST_EMBEDDING_FILE,'utf8'));await editor.getByLabel('Embedding服务地址',{exact:true}).fill(config.api_base);await editor.getByLabel('Embedding模型名称',{exact:true}).fill(config.model);await editor.getByLabel('向量维度',{exact:true}).fill(String(config.dimensions));await editor.getByLabel('Embedding服务 API Key',{exact:true}).fill(config.api_key??'');}
   const saving=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/context/'+kind&&r.request().method()==='POST');await editor.getByRole('button',{name:/^保\s*存$/}).click();const saveResponse=await saving;expect(saveResponse.status()).toBe(200);const c=await saveResponse.json();created.push(c);await expect(editor).toHaveCount(0);expect(c.endpoint_id).toBe('local-'+backend);
   const call=async(action:string,data:Record<string,unknown>={})=>{const result=await page.request.post(`/v1/context/${kind}/${c.id}/${action}`,{headers,data:{expected_version:c.version,...data}});expect(result.status()).toBe(200);return result.json();};
   const mutate=async(action:string,data:Record<string,unknown>)=>{const r=await page.request.post(`/v1/context/${kind}/${c.id}/${action}`,{headers,data:{expected_version:c.version,...data}});if(r.ok())return r.json();expect((await r.json()).code).toBe('CONTEXT_PENDING');for(let i=0;i<12;i++){await new Promise(resolve=>setTimeout(resolve,3000));const check=await page.request.post(`/v1/context/${kind}/${c.id}/reconcile`,{headers,data:{}});if(check.ok())return check.json();expect((await check.json()).code).toBe('CONTEXT_PENDING');}throw new Error('external operation did not finish');};
   await call('verify');const staged=await call('preview',{title:'发布约定',content:'合成项目每周三发布，由测试机器人负责。',source:'真实集成的合成数据'});if(process.env.HC_CONTEXT_TEST_EMBEDDING_FILE){expect(staged.data_use).toContain('配置的 Embedding 服务');expect(staged.data_use).not.toContain('无外部');expect(c.embedding.provider).toBe('openai_compatible');expect(c.embedding.api_key).toBeUndefined();expect(c.embedding_key_configured).toBe(true);}await mutate('apply',{token:staged.token,mode:'skip',idempotency_key:randomUUID(),confirmed:true});
   const hits=await call('search',{query:'合成项目什么时候发布？',limit:5});expect(hits.items.some((h:{snippet:string})=>h.snippet.includes('每周三'))).toBe(true);
   if(kind==='knowledge'){for(const [search_mode,limit] of [['semantic',5],['exact',10],['hybrid',20]] as const){const found=await call('search',{query:'每周三',limit,search_mode});const hit=found.items.find((h:{snippet:string})=>h.snippet.includes('每周三'));expect(hit).toBeDefined();if(search_mode==='exact')expect(hit.score).toBeUndefined();else expect(Number.isFinite(hit.score)).toBe(true);}const rejected=await page.request.post(`/v1/context/${kind}/${c.id}/search`,{headers,data:{expected_version:c.version,query:'每周三',search_mode:'invalid'}});expect(rejected.status()).toBe(422);}
   await page.goto('/app/'+kind);await page.getByRole('button',{name:'管理内容',exact:true}).click();await expect(page.getByRole('button',{name:'发布约定',exact:true})).toBeVisible();await page.getByRole('button',{name:'发布约定',exact:true}).click();await expect(page.locator('.context-content')).toContainText('每周三');
   await page.getByRole('dialog',{name:'发布约定',exact:true}).getByRole('button',{name:'Close',exact:true}).click();
   const identity=staged.items[0].id;await mutate('delete',{record_id:identity,record_version:1,idempotency_key:randomUUID(),confirmed:true});expect((await call('search',{query:'合成项目',limit:5})).items).toHaveLength(0);
   response=await page.request.delete(`/v1/context/${kind}/${c.id}`,{headers,data:{expected_version:c.version,confirmed_name:c.name}});expect(response.status()).toBe(200);created.pop();
 }finally{
  for(const c of created)await page.request.delete(`/v1/context/${c.kind}/${c.id}`,{headers,data:{expected_version:c.version,confirmed_name:c.name}});
  if(member){const fresh=await admin.request.get('/v1/users/'+member.id);if(fresh.ok()){const current=await fresh.json();await admin.request.delete('/v1/users/'+member.id,{headers:adminHeaders,data:{expected_version:current.version,confirmed_name:current.display_name}});}}
  if(role)await admin.request.delete('/v1/roles/'+role.id,{headers:adminHeaders,data:{expected_version:role.version}});
  await adminContext.close();
 }
});

test('document deletion requires confirmation and keeps the target visible',async({page},info)=>{
 if(info.project.name==='mobile')await page.setViewportSize({width:320,height:640});await setup(page);const filename='国轩高科分析报告_2026-10-28_'+('long-document-name-'.repeat(6))+'.md';const record={...entry,title:filename};const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};let deletes=0;let finish=()=>{};const pending=new Promise<void>(resolve=>{finish=resolve;});
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[record]}}));
 await page.route(`**/v1/context/knowledge/${id}/delete`,async r=>{deletes++;expect(r.request().postDataJSON()).toMatchObject({record_id:record.id,record_version:record.version,confirmed:true,scope:'personal'});await pending;return r.fulfill({json:{deleted:true}});});
 try{await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:/^删\s*除$/}).click();const modal=page.getByRole('dialog',{name:'删除文档',exact:true});await expect(modal.getByText(filename,{exact:true})).toBeVisible();await expect(modal.getByText('删除后无法恢复，相关索引将一并移除。',{exact:true})).toBeVisible();const submit=modal.getByRole('button',{name:'永久删除',exact:true});await expect(submit).toBeDisabled();expect(deletes).toBe(0);await modal.getByRole('checkbox',{name:'我确认永久删除'}).check();await expect(submit).toBeEnabled();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await submit.hover();await page.screenshot({path:info.outputPath('delete-document.png'),animations:'disabled'});await submit.click();await expect.poll(()=>deletes).toBe(1);await expect(modal.getByRole('checkbox',{name:'我确认永久删除'})).toBeDisabled();finish();await expect(modal).toHaveCount(0);}finally{finish();}
});

test('work-data picker navigates file pages and folders without losing the asset',async({page},info)=>{
 if(info.project.name==='mobile')await page.setViewportSize({width:320,height:640});await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};const asset={id:'11111111-1111-4111-8111-111111111111',workspace_name:'测试项目',owner_id:user.id,capacity_bytes:10737418240,usage:{},maintenance:{backup_blockers:[],restore_blockers:[],reclaim_blockers:[]},status:'ACTIVE'};const row=(name:string,type='FILE')=>({name,type,size_bytes:32,modified_at:'2026-10-23T08:00:00Z'});
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'instances:read']}}));await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));await page.route('**/v1/instances?**',r=>r.fulfill({json:{items:[],total:0,page:1,limit:20}}));
 await page.route('**/v1/storage-assets?**',r=>{const pageNumber=Number(new URL(r.request().url()).searchParams.get('page'));return r.fulfill({json:{items:pageNumber===1?[asset]:[{...asset,id:'22222222-2222-4222-8222-222222222222',workspace_name:'另一个项目'}],total:21,page:pageNumber,limit:20}});});
 await page.route('**/v1/storage-assets/*/files?**',r=>{const params=new URL(r.request().url()).searchParams;const path=params.get('path')??'';const after=params.get('after');return r.fulfill({json:{root:params.get('root'),path,entries:path?[row('nested.md')]:after?[row('last.md')]:[row('资料','DIRECTORY'),row('first.md')],...(!path&&!after?{next_after:'first.md'}:{})}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'从工作数据选择',exact:true}).click();const picker=page.getByRole('dialog',{name:'选择工作数据文件'});await expect(picker.getByText('请选择数据资产',{exact:true})).toBeVisible();await picker.getByRole('combobox',{name:'工作数据资产'}).click();await page.getByText('测试项目',{exact:true}).click();await expect(picker.getByRole('button',{name:'first.md',exact:true})).toBeVisible();await picker.getByRole('button',{name:'下一页资产'}).click();await expect(picker.getByText('测试项目',{exact:true})).toBeVisible();await picker.getByRole('button',{name:'上一页资产'}).click();await picker.getByRole('button',{name:'下一页文件'}).click();await expect(picker.getByRole('button',{name:'last.md',exact:true})).toBeVisible();await picker.getByRole('button',{name:'上一页文件'}).click();await expect(picker.getByRole('button',{name:'first.md',exact:true})).toBeVisible();await picker.getByRole('button',{name:'资料',exact:true}).click();await expect(picker.getByRole('button',{name:'nested.md',exact:true})).toBeVisible();await picker.getByRole('button',{name:'/data/workspace',exact:true}).click();await expect(picker.getByRole('button',{name:'first.md',exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await picker.getByRole('button',{name:'first.md',exact:true}).hover();await page.screenshot({path:info.outputPath('work-data-picker.png'),animations:'disabled'});
});

test('knowledge work-data import reads only the explicitly chosen UTF-8 file before preview',async({page},info)=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'instances:read']}}));
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));
 await page.route('**/v1/instances?**',r=>r.fulfill({json:{items:[],total:0,page:1,limit:20}}));
 let reads=0;let previews=0;
 await page.route('**/v1/storage-assets?**',r=>r.fulfill({json:{items:[{id:'11111111-1111-4111-8111-111111111111',workspace_name:'测试项目',owner_id:user.id,capacity_bytes:10737418240,usage:{},maintenance:{backup_blockers:[],restore_blockers:[],reclaim_blockers:[]},status:'ACTIVE'}],total:1,page:1,limit:20}}));
 await page.route('**/v1/storage-assets/*/files?**',r=>r.fulfill({json:{root:'workspace',path:'',entries:[{name:'README.md',type:'FILE',size_bytes:32,modified_at:'2026-10-23T08:00:00Z'},{name:'secrets.env',type:'FILE',size_bytes:20,modified_at:'2026-10-23T08:00:00Z'}]}}));
 await page.route('**/v1/storage-assets/*/file?**',r=>{reads++;return r.fulfill({status:200,headers:{'Content-Type':'text/plain; charset=utf-8','Content-Disposition':'inline; filename="README.md"'},body:'发布安排：每周三。'});});
 await page.route('**/v1/context/knowledge/parse',r=>{expect(r.request().postData()).toContain('README.md');return r.fulfill({json:{content:'发布安排：每周三。'}});});
 await page.route(`**/v1/context/knowledge/${id}/preview`,r=>{previews++;expect(r.request().postDataJSON()).toMatchObject({content:'发布安排：每周三。',title:'README.md',source:'工作数据：/data/workspace/README.md'});return r.fulfill({json:{token:'selected-file',items:[{id:'sample',title:'README.md',content:'发布安排：每周三。',source:'工作数据：/data/workspace/README.md',conflict:false}],warnings:[],data_use:'原文保存在知识库接入服务，向量写入所选数据库。文档片段和检索问题会发送到配置的 Embedding 服务。',expires_in:300}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'从工作数据选择',exact:true}).click();
 const picker=page.getByRole('dialog',{name:'选择工作数据文件'});expect(reads).toBe(0);expect(previews).toBe(0);
 await picker.getByRole('combobox',{name:'工作数据资产'}).click();await page.getByText('测试项目',{exact:true}).click();
 await expect(picker.getByRole('button',{name:'secrets.env'})).toBeDisabled();expect(reads).toBe(0);
 await picker.getByRole('button',{name:'README.md'}).click();const editor=page.getByRole('dialog',{name:'添加文档'});await expect(editor.getByLabel('内容',{exact:true})).toHaveValue('发布安排：每周三。');expect(reads).toBe(1);expect(previews).toBe(0);await editor.getByRole('button',{name:/^预\s*览$/}).click();const preview=page.getByRole('dialog',{name:'确认保存文档'});await expect(preview).toBeInViewport();await preview.getByRole('button',{name:'确认保存'}).hover();expect(previews).toBe(1);await page.screenshot({path:info.outputPath('knowledge-preview.png'),animations:'disabled'});
});

test('knowledge content switches between source and Markdown without changing saved text',async({page},info)=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};
 const markdown='# 项目报告\n\n**重点内容**\n\n| 项目 | 状态 |\n| --- | --- |\n| 发布 | 完成 |\n\n[安全链接](https://example.com)\n\n[不安全链接](javascript:alert(1))';
 const record={...entry,title:'报告.md',content:markdown};
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[record]}}));
 await page.route(`**/v1/context/knowledge/${id}/get`,r=>r.fulfill({json:record}));
 await page.route(`**/v1/context/knowledge/${id}/search`,r=>r.fulfill({json:{items:[{...record,snippet:markdown,position:'片段 1',score:.9}]}}));
 await page.route(`**/v1/context/knowledge/${id}/preview`,r=>{expect(r.request().postDataJSON()).toMatchObject({title:'报告.md',content:markdown});return r.fulfill({json:{token:'test',items:[{...record,conflict:false}],warnings:[],data_use:'测试',expires_in:300}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();
 await page.getByRole('button',{name:'报告.md',exact:true}).click();const detail=page.getByRole('dialog',{name:'报告.md',exact:true});
 await expect(detail.getByRole('region',{name:'原文内容'})).toHaveText(markdown);await detail.getByText('Markdown 预览',{exact:true}).click();
 await expect(detail.getByRole('heading',{name:'项目报告'})).toBeVisible();await expect(detail.getByRole('table')).toBeVisible();await expect(detail.getByRole('link',{name:'不安全链接'})).not.toHaveAttribute('href',/^javascript:/);await page.screenshot({path:info.outputPath('knowledge-markdown.png'),animations:'disabled'});
 await detail.getByText('原文',{exact:true}).click();await expect(detail.getByRole('region',{name:'原文内容'})).toHaveText(markdown);await detail.getByRole('button',{name:/^关\s*闭$/}).click();
 await page.getByRole('button',{name:'知识库检索',exact:true}).click();const search=page.getByRole('dialog',{name:'知识库检索'});await search.getByLabel('检索问题').fill('项目');await search.getByRole('button',{name:'检索',exact:true}).click();await search.getByText('报告.md',{exact:true}).click();await search.getByText('Markdown 预览',{exact:true}).click();await expect(search.getByRole('heading',{name:'项目报告'})).toBeVisible();await search.getByRole('button',{name:'Close',exact:true}).click();
 await page.getByRole('button',{name:'添加文档',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加文档'});await editor.getByLabel('标题').fill('报告.md');await editor.getByLabel('内容',{exact:true}).fill(markdown);await editor.getByText('Markdown 预览',{exact:true}).click();await expect(editor.getByRole('heading',{name:'项目报告'})).toBeVisible();await editor.getByText('原文',{exact:true}).click();await expect(editor.getByLabel('内容',{exact:true})).toHaveValue(markdown);await editor.getByRole('button',{name:/^预\s*览$/}).click();
 const preview=page.getByRole('dialog',{name:'确认保存文档'});await preview.getByText('Markdown 预览',{exact:true}).click();await expect(preview.getByRole('heading',{name:'项目报告'})).toBeVisible();await preview.getByText('原文',{exact:true}).click();await expect(preview.getByRole('region',{name:'原文内容'})).toHaveText(markdown);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
