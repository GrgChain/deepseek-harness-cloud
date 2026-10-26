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
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'添加记忆',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加记忆',exact:true});
 await editor.getByRole('textbox').nth(0).fill('回答偏好');await editor.getByRole('textbox').nth(1).fill('我偏好中文回答');await editor.getByRole('button',{name:/^预\s*览$/}).click();
 const preview=page.getByRole('dialog',{name:'确认发送到外部服务'});await expect(preview).toBeVisible();expect(writes).toBe(0);await preview.getByRole('button',{name:'确认保存'}).click();await expect(preview).toHaveCount(0);expect(writes).toBe(1);
 await page.getByRole('textbox',{name:'检索问题'}).fill('什么时候发布');await page.getByRole('button',{name:'试检索'}).click();
 await expect(page.getByText('检索结果 · 1')).toBeVisible();await page.screenshot({path:info.outputPath('external-memory.png'),fullPage:true,animations:'disabled'});
 await page.route(`**/v1/context/memory/${id}/get`,r=>r.fulfill({status:404,json:{code:'NOT_FOUND',message:'连接、内容或授权范围不存在'}}));
 await page.locator('.context-results').getByRole('button',{name:'发布约定'}).click();await expect(page.getByText('连接、内容或授权范围不存在',{exact:true})).toBeVisible();await expect(page.getByRole('dialog',{name:'发布约定'})).toHaveCount(0);
});
test('read-only connections hide content management and connection editing',async({page})=>{
 await setup(page,false);await page.goto('/app/memory');await expect(page.getByRole('button',{name:'添加连接'})).toHaveCount(0);await page.getByRole('button',{name:'管理内容'}).click();await expect(page.getByRole('button',{name:'添加记忆'})).toHaveCount(0);await expect(page.getByRole('button',{name:'导出',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'删除',exact:true})).toHaveCount(0);
});

test('memory service picker filters services and preserves configuration after save failure',async({page},info)=>{
 await setup(page);let saves=0;
 await page.route('**/v1/context/memory',r=>{
  if(r.request().method()==='POST'){saves++;expect(r.request().postDataJSON()).toMatchObject({name:'我的项目记忆',endpoint_id:'local-mem0',credential:'test-only-key',enabled:true,expected_version:0});return r.fulfill({status:422,json:{code:'CONTEXT_INVALID',message:'请检查服务凭据'}});}
  return r.fulfill({json:{items:[connection],endpoints:[{id:'local-mem0',name:'本机测试 Mem0',backend:'mem0',data_use:'数据仅发送到独立本机测试服务'},{id:'local-qdrant',name:'知识库测试服务',backend:'qdrant',data_use:'知识库专用'}],bindings:[],integration:'NOT_CONNECTED'}});
 });
 await page.goto('/app/memory');await page.getByRole('button',{name:'添加连接',exact:true}).click();
 const picker=page.getByRole('dialog',{name:'添加记忆连接',exact:true});
 await expect(picker.getByRole('article')).toHaveCount(3);await expect(picker.locator('.context-service-grid')).toHaveCSS('display','grid');
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
 await page.goto('/app/memory');await page.getByRole('button',{name:'添加连接',exact:true}).click();await page.getByRole('button',{name:'配置连接 Mem0',exact:true}).click();
 const editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});const name=editor.getByLabel('连接名称',{exact:true});const credential=editor.getByLabel('服务凭据',{exact:true});
 await name.clear();await editor.locator('.ant-modal-body').evaluate(el=>{el.scrollTop=el.scrollHeight;});await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(name).toBeFocused();await expect(name).toBeInViewport();expect(writes).toBe(0);
 await name.fill('我的记忆');await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(credential).toBeFocused();await expect(credential).toBeInViewport();expect(writes).toBe(0);
 expect(await credential.evaluate(el=>{const box=el.getBoundingClientRect();const body=el.closest('.ant-modal-body')!.getBoundingClientRect();return box.top>=body.top&&box.bottom<=body.bottom;})).toBe(true);expect(await editor.getByRole('button',{name:/^保\s*存$/}).evaluate(el=>{const box=el.getBoundingClientRect();return box.top>=0&&box.bottom<=innerHeight;})).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('context-validation-focus.png'),fullPage:true,animations:'disabled'});
});

test('knowledge creation uses only its selected backend and blocks edits while saving',async({page},info)=>{
 await setup(page);let saved:Record<string,unknown>|undefined;let finish=()=>{};
 const pending=new Promise<void>(resolve=>{finish=resolve;});
 const qdrant={...connection,kind:'knowledge',backend:'qdrant',endpoint_id:'local-qdrant',name:'个人资料',enabled:false,status:'UNVERIFIED'};
 await page.route('**/v1/context/knowledge',async r=>{
  if(r.request().method()==='POST'){saved=r.request().postDataJSON();await pending;return r.fulfill({json:qdrant});}
  return r.fulfill({json:{items:saved?[qdrant]:[],endpoints:[{id:'local-qdrant',name:'Qdrant 测试服务',backend:'qdrant',data_use:'所选服务说明'},{id:'local-mem0',name:'Mem0 测试服务',backend:'mem0',data_use:'记忆专用'}],bindings:[],integration:'NOT_CONNECTED'}});
 });
 try{
  await page.goto('/app/knowledge');await page.getByRole('button',{name:'添加连接',exact:true}).click();const picker=page.getByRole('dialog',{name:'添加知识库连接',exact:true});await expect(picker.getByRole('article')).toHaveCount(3);await expect(picker.getByText('Mem0',{exact:true})).toHaveCount(0);await picker.getByRole('button',{name:'配置连接 Qdrant',exact:true}).click();
  const editor=page.getByRole('dialog',{name:'添加知识库连接',exact:true});await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('Qdrant 知识库');await editor.getByText('Qdrant 测试服务',{exact:true}).click();await expect(page.getByRole('option',{name:'Mem0 测试服务',exact:true})).toHaveCount(0);await editor.getByLabel('连接名称',{exact:true}).fill('个人资料');await editor.getByLabel('服务凭据',{exact:true}).fill('knowledge-test-key');await editor.getByRole('switch',{name:'启用连接',exact:true}).uncheck();
  await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect.poll(()=>saved).toBeDefined();await expect(editor.getByLabel('连接名称',{exact:true})).toBeDisabled();await expect(editor.getByRole('switch',{name:'启用连接',exact:true})).toBeDisabled();expect(saved).toMatchObject({name:'个人资料',endpoint_id:'local-qdrant',credential:'knowledge-test-key',enabled:false,expected_version:0});finish();await expect(editor).toHaveCount(0);
  const row=page.getByRole('article',{name:'知识库连接 个人资料',exact:true});await expect(row.getByText('已停用',{exact:true})).toBeVisible();await expect(row.getByRole('button',{name:/^验\s*证$/})).toBeDisabled();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('knowledge-created.png'),fullPage:true,animations:'disabled'});
 }finally{finish();}
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

for(const account of ['admin','member'])test(`live workbench gives ${account} personal memory and knowledge navigation`,async({page},info)=>{
 test.skip(process.env.HC_REAL_CONTEXT!=='true','requires the local platform and built-in accounts');
 const {readFileSync}=await import('node:fs');
 await page.goto('/auth/login?return_to=%2Fapp%2Fmemory');await page.getByLabel('邮箱',{exact:true}).fill(account+'@harness.local');await page.getByLabel('密码',{exact:true}).fill(readFileSync('../.local/auth/'+account+'-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/memory$/);await expect(page.getByRole('region',{name:'记忆管理'})).toBeVisible();
 const me=await(await page.request.get('/v1/me')).json();for(const p of ['memory:read','memory:manage','knowledge:read','knowledge:manage'])expect(me.permissions).toContain(p);
 await expect(page.getByRole('button',{name:'添加连接',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'添加连接',exact:true}).click();const picker=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(picker.getByRole('article')).toHaveCount(3);await picker.getByRole('button',{name:'配置连接 Mem0',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加记忆连接',exact:true});await expect(editor.getByLabel('连接名称',{exact:true})).toHaveValue('Mem0 记忆');await expect(editor.getByLabel('服务凭据',{exact:true})).toBeVisible();await expect(editor).toHaveCSS('transform','none');await expect(editor).toHaveCSS('opacity','1');await page.screenshot({path:info.outputPath(account+'-memory-editor.png'),fullPage:true,animations:'disabled'});await editor.getByRole('button',{name:/^取\s*消$/}).click();
 if(info.project.name==='mobile')await page.getByRole('button',{name:'打开导航'}).click();
 await page.getByRole('link',{name:'知识库',exact:true}).filter({visible:true}).click();
 await expect(page.getByRole('region',{name:'知识库管理'})).toBeVisible();await expect(page.getByRole('button',{name:'添加连接',exact:true})).toBeVisible();
 await page.screenshot({path:info.outputPath(account+'-workbench-context.png'),fullPage:true,animations:'disabled'});
});

for(const backend of (process.env.HC_CONTEXT_UI_BACKENDS??'mem0,qdrant').split(','))test(`live platform uses ${backend} with an explicitly granted test role`,async({page,browser,baseURL},info)=>{
 test.skip(process.env.HC_REAL_CONTEXT!=='true'||info.project.name!=='desktop','requires the local external Docker services');test.setTimeout(180_000);page.setDefaultTimeout(15_000);
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
   await page.goto('/app/'+kind);await page.getByRole('button',{name:'添加连接',exact:true}).click();await page.getByRole('button',{name:'配置连接 '+names[backend],exact:true}).click();const editor=page.getByRole('dialog',{name:kind==='memory'?'添加记忆连接':'添加知识库连接',exact:true});await editor.getByLabel('连接名称',{exact:true}).fill('真实测试 '+backend+' '+suffix);await editor.getByLabel('服务凭据',{exact:true}).fill(readFileSync('../.local/context/test-key','utf8').trim());
   const saving=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/context/'+kind&&r.request().method()==='POST');await editor.getByRole('button',{name:/^保\s*存$/}).click();const saveResponse=await saving;expect(saveResponse.status()).toBe(200);const c=await saveResponse.json();created.push(c);await expect(editor).toHaveCount(0);expect(c.endpoint_id).toBe('local-'+backend);
   const call=async(action:string,data:Record<string,unknown>={})=>{const result=await page.request.post(`/v1/context/${kind}/${c.id}/${action}`,{headers,data:{expected_version:c.version,...data}});expect(result.status()).toBe(200);return result.json();};
   const mutate=async(action:string,data:Record<string,unknown>)=>{const r=await page.request.post(`/v1/context/${kind}/${c.id}/${action}`,{headers,data:{expected_version:c.version,...data}});if(r.ok())return r.json();expect((await r.json()).code).toBe('CONTEXT_PENDING');for(let i=0;i<12;i++){await new Promise(resolve=>setTimeout(resolve,3000));const check=await page.request.post(`/v1/context/${kind}/${c.id}/reconcile`,{headers,data:{}});if(check.ok())return check.json();expect((await check.json()).code).toBe('CONTEXT_PENDING');}throw new Error('external operation did not finish');};
   await call('verify');const staged=await call('preview',{title:'发布约定',content:'合成项目每周三发布，由测试机器人负责。',source:'真实集成的合成数据'});await mutate('apply',{token:staged.token,mode:'skip',idempotency_key:randomUUID(),confirmed:true});
   const hits=await call('search',{query:'合成项目什么时候发布？',limit:5});expect(hits.items.some((h:{snippet:string})=>h.snippet.includes('每周三'))).toBe(true);
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

test('knowledge work-data import reads only the explicitly chosen UTF-8 file before preview',async({page})=>{
 await setup(page);const knowledge={...connection,kind:'knowledge',backend:'qdrant',name:'项目文档'};
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'instances:read']}}));
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[knowledge],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));
 await page.route('**/v1/instances?**',r=>r.fulfill({json:{items:[],total:0,page:1,limit:20}}));
 let reads=0;let previews=0;
 await page.route('**/v1/storage-assets?**',r=>r.fulfill({json:{items:[{id:'11111111-1111-4111-8111-111111111111',workspace_name:'测试项目',owner_id:user.id,capacity_bytes:10737418240,usage:{},maintenance:{backup_blockers:[],restore_blockers:[],reclaim_blockers:[]},status:'ACTIVE'}],total:1,page:1,limit:20}}));
 await page.route('**/v1/storage-assets/*/files?**',r=>r.fulfill({json:{root:'workspace',path:'',entries:[{name:'README.md',type:'FILE',size_bytes:32,modified_at:'2026-10-23T08:00:00Z'},{name:'secrets.env',type:'FILE',size_bytes:20,modified_at:'2026-10-23T08:00:00Z'}]}}));
 await page.route('**/v1/storage-assets/*/file?**',r=>{reads++;return r.fulfill({status:200,headers:{'Content-Type':'text/plain; charset=utf-8','Content-Disposition':'inline; filename="README.md"'},body:'发布安排：每周三。'});});
 await page.route(`**/v1/context/knowledge/${id}/preview`,r=>{previews++;expect(r.request().postDataJSON()).toMatchObject({content:'发布安排：每周三。',title:'README.md',source:'工作数据：/data/workspace/README.md'});return r.fulfill({json:{token:'selected-file',items:[],warnings:[],data_use:'独立测试服务',expires_in:300}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容',exact:true}).click();await page.getByRole('button',{name:'从工作数据选择',exact:true}).click();
 const picker=page.getByRole('dialog',{name:'选择工作数据文件'});expect(reads).toBe(0);expect(previews).toBe(0);
 await picker.getByRole('combobox',{name:'工作数据资产'}).click();await page.getByText('测试项目',{exact:true}).click();
 await expect(picker.getByRole('button',{name:'secrets.env'})).toBeDisabled();expect(reads).toBe(0);
 await picker.getByRole('button',{name:'README.md'}).click();await expect(page.getByRole('dialog',{name:'确认发送到外部服务'})).toBeVisible();expect(reads).toBe(1);expect(previews).toBe(1);
});
