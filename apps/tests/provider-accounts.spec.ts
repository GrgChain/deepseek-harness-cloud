import {expect,test,type Page} from '@playwright/test';
const user={id:'00000000-0000-4000-8000-000000000041',memberships:[{org_id:'00000000-0000-4000-8000-000000000040',role:'MEMBER',role_id:'00000000-0000-4000-8000-000000000004',role_name:'成员',status:'ACTIVE'}],permissions:['personal-model:read','personal-model:write','personal-model:validate','personal-model:revoke'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const health={status:'HEALTHY',checked_at:'2026-01-01T00:00:00Z',expires_at:'2099-01-01T00:00:00Z',reason_code:''};
const groupID='00000000-0000-4000-8000-000000000051';
const providers=[{id:'deepseek',name:'DeepSeek',api_bases:['https://api.deepseek.com']},{id:'custom-openai',name:'自定义 · OpenAI',api_bases:['http://127.0.0.1:11434/v1']},{id:'custom-openai-responses',name:'自定义 · OpenAI Responses',api_bases:['http://127.0.0.1:11434/v1']},{id:'custom-anthropic',name:'自定义 · Anthropic',api_bases:['http://127.0.0.1:11434']}];
const makeModel=(model:string)=>({id:groupID.replace('51',model==='current-a'?'61':'62'),name:'我的提供商',provider:'deepseek',model,version:1,catalog_entry_id:'catalog-'+model,enabled:true,credential_configured:true,sync_status:'SYNCED',gateway_check:health,validation:health,in_use:0,selection:{provider:'deepseek',api_base:'https://api.deepseek.com',model,response_models:[model],context_window:32768,max_output_tokens:4096}});
const group=()=>({id:groupID,name:'我的提供商',provider:'deepseek',api_base:'https://api.deepseek.com',version:3,status:'ACTIVE',default_model_id:null as string|null,credential_configured:true,models:[makeModel('current-a'),makeModel('current-b')]});
async function setup(page:Page,items:unknown[]=[]){
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));await page.route('**/v1/me',r=>r.fulfill({json:user}));await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:providers}}));await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items}}));await page.route('**/v1/my-model-default',r=>r.fulfill({json:null}));await page.goto('/app/models');await expect(page.getByRole('heading',{name:'模型提供商',exact:true})).toBeVisible();
}
async function choose(page:Page,name:string){await page.getByLabel('提供商',{exact:true}).locator('..').locator('..').click();if(name==='自定义'){await expect(page.locator('.ant-select-dropdown:visible').getByTitle(name,{exact:true})).toHaveCount(1);await expect(page.locator('.ant-select-dropdown:visible').getByTitle('自定义 · OpenAI',{exact:true})).toHaveCount(0);}await page.locator('.ant-select-dropdown:visible').getByTitle(name,{exact:true}).click();}
async function add(page:Page,name='DeepSeek'){await page.getByRole('button',{name:'添加账号',exact:true}).click();await choose(page,name);await page.getByLabel('名称',{exact:true}).fill('我的提供商');}

test('provider pagination searches all accounts, resets filters and recovers after deletion',async({page},info)=>{
 const accounts=Array.from({length:12},(_,i)=>({...group(),id:`00000000-0000-4000-8000-${String(i+100).padStart(12,'0')}`,name:`模型提供商账号 ${String(i+1).padStart(2,'0')}`}));
 await setup(page,accounts);
 const pager=page.getByRole('navigation',{name:'模型提供商分页',exact:true});
 await expect(page.getByRole('article')).toHaveCount(10);
 await expect(pager).toContainText('共 12 条');
 await pager.getByRole('button',{name:'下一页',exact:true}).click();
 await expect(page.getByRole('article')).toHaveCount(2);
 await expect(page.getByRole('article',{name:'模型提供商账号 11',exact:true})).toBeVisible();
 await page.getByRole('button',{name:/^刷\s*新$/}).click();
 await expect(pager).toContainText('第 2 / 2 页');
 await page.getByLabel('搜索提供商或模型').fill('账号 01');
 await expect(page.getByRole('article')).toHaveCount(1);
 await expect(pager).toContainText('第 1 / 1 页');
 await page.getByLabel('搜索提供商或模型').fill('');
 await pager.getByRole('combobox',{name:'每页条数',exact:true}).locator('..').locator('..').click();
 await page.locator('.ant-select-dropdown:visible').getByTitle('20 条 / 页',{exact:true}).click();
 await expect(page.getByRole('article')).toHaveCount(12);
 await pager.getByRole('combobox',{name:'每页条数',exact:true}).locator('..').locator('..').click();
 await page.locator('.ant-select-dropdown:visible').getByTitle('10 条 / 页',{exact:true}).click();
 await pager.getByRole('button',{name:'下一页',exact:true}).click();
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:accounts.slice(0,10)}}));
 await page.getByRole('button',{name:/^刷\s*新$/}).click();
 await expect(page.getByRole('article')).toHaveCount(10);
 await expect(pager).toContainText('第 1 / 1 页');
 await pager.scrollIntoViewIfNeeded();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('provider-pagination.png'),animations:'disabled'});
});

test('provider list expands models, searches and keeps technical details out of the header',async({page},info)=>{
 await setup(page,[group()]);const row=page.getByRole('article',{name:'我的提供商'});await expect(row.getByText('2 个模型',{exact:true})).toBeVisible();await expect(row.getByText('current-a',{exact:true})).toHaveCount(0);await row.getByRole('button',{name:'展开 我的提供商'}).click();await expect(row.getByText('current-a',{exact:true})).toBeVisible();await expect(row.getByRole('button',{name:'重新校验'})).toHaveCount(2);await page.getByLabel('搜索提供商或模型').fill('no-match');await expect(page.getByText('没有匹配的提供商')).toBeVisible();await page.getByLabel('搜索提供商或模型').fill('current-a');await expect(row).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('provider-list.png'),animations:'disabled'});
});

test('add synchronizes official list, selects and excludes models in one provider request',async({page},info)=>{
 await setup(page);let syncs=0,writes=0;
 await page.route('**/v1/model-providers/deepseek:sync',r=>{syncs++;expect(r.request().postDataJSON()).toEqual({api_base:'https://api.deepseek.com',api_key:'synthetic-key'});return r.fulfill(syncs===1?{status:502,json:{code:'MODEL_DISCOVERY_FAILED'}}:{json:{items:['current-a','current-b','excluded'].map(id=>({id,name:id}))}});});
 await page.route('**/v1/my-provider-accounts',r=>{if(r.request().method()==='GET')return r.fulfill({json:{items:[]}});writes++;const data=r.request().postDataJSON();expect(data.name).toBe('我的提供商');expect(data.api_key).toBe('synthetic-key');expect(data.models.map((m:{model:string})=>m.model)).toEqual(['current-a','current-b']);expect(r.request().headers()['x-csrf-token']).toBe(user.csrf_token);expect(r.request().headers()['idempotency-key']).toBeTruthy();return r.fulfill({status:202,json:group()});});
 await add(page);const save=page.getByRole('dialog').getByRole('button',{name:'添加账号',exact:true});await page.getByLabel('API Key',{exact:true}).fill('synthetic-key');await expect(save).toBeDisabled();await page.getByRole('button',{name:'同步官方列表'}).click();await expect(page.getByRole('alert')).toContainText('同步模型列表失败');await page.getByRole('button',{name:/^重\s*试$/}).click();await expect(page.getByRole('dialog').getByRole('status')).toContainText('已同步 3 个模型');await page.getByRole('button',{name:'选择当前列表'}).click();await page.getByRole('checkbox',{name:'excluded',exact:true}).uncheck();await expect(save).toBeEnabled();expect(writes).toBe(0);await page.screenshot({path:info.outputPath('provider-add.png'),animations:'disabled'});await save.click();await expect(page.getByRole('dialog')).toHaveCount(0);expect(writes).toBe(1);expect(await page.evaluate(()=>JSON.stringify({localStorage,sessionStorage}))).not.toContain('synthetic-key');
});

for(const provider of providers.slice(1))test('custom provider supports no-auth, synchronization and manual models: '+provider.id,async({page},info)=>{
 await setup(page);await page.route('**/v1/model-providers/'+provider.id+':sync',r=>{expect(r.request().postDataJSON()).toEqual({api_base:provider.api_bases[0],api_key:''});return r.fulfill({json:{items:[{id:'local-a',name:'A'},{id:'local-b',name:'B'}]}});});let writes=0;await page.route('**/v1/my-provider-accounts',r=>{if(r.request().method()==='GET')return r.fulfill({json:{items:[]}});writes++;const body=r.request().postDataJSON();expect(body.models.map((m:{model:string})=>m.model)).toEqual(['local-a','manual-model']);expect(body.api_key).toBe('');expect(body.models[0].provider).toBe(provider.id);return r.fulfill({status:202,json:group()});});await add(page,'自定义');await page.getByLabel('接口协议',{exact:true}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle(provider.id==='custom-openai'?'OpenAI Chat Completions':provider.id==='custom-openai-responses'?'OpenAI Responses':'Anthropic Messages',{exact:true}).click();await expect(page.getByLabel('此服务无需 API Key')).toBeChecked();await expect(page.getByLabel('服务地址',{exact:true})).toHaveValue('');await expect(page.getByRole('button',{name:'同步服务模型'})).toBeDisabled();await page.getByLabel('服务地址',{exact:true}).fill(provider.api_bases[0]);await page.getByText('新模型的能力限制').click();await expect(page.getByLabel('上下文上限')).toHaveValue('131072');await expect(page.getByLabel('输出上限')).toHaveValue('16384');await page.getByRole('button',{name:'同步服务模型'}).click();await page.getByRole('checkbox',{name:'local-a',exact:true}).check();await page.getByLabel('自定义模型 ID').fill('manual-model');await page.getByRole('button',{name:'添加模型',exact:true}).click();await expect(page.getByRole('checkbox',{name:'manual-model',exact:true})).toBeChecked();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath(provider.id+'.png'),animations:'disabled'});await page.getByRole('dialog').getByRole('button',{name:'添加账号',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);expect(writes).toBe(1);
});

test('editing uses saved credential for discovery, retains selected models, removes one and saves atomically',async({page},info)=>{
 const current=group();await setup(page,[current]);let writes=0;await page.route('**/v1/my-provider-accounts/'+groupID+':sync',r=>{expect(r.request().postDataJSON()).toEqual({expected_version:3});return r.fulfill({json:{items:['current-a','current-b','new-model'].map(id=>({id,name:id}))}});});await page.route('**/v1/my-provider-accounts/'+groupID,r=>{writes++;const body=r.request().postDataJSON();expect(r.request().method()).toBe('PUT');expect(body.expected_version).toBe(3);expect(body.api_key).toBeUndefined();expect(body.models.map((m:{model:string})=>m.model)).toEqual(['current-b','new-model']);expect(body.default_model).toBe('new-model');return r.fulfill({status:202,json:{...current,version:4}});});await page.getByRole('article').getByRole('button',{name:/^编\s*辑$/}).click();await expect(page.getByLabel('API Key',{exact:true})).toHaveValue('');await expect(page.getByLabel('提供商',{exact:true})).toBeDisabled();await page.getByRole('button',{name:'同步官方列表'}).click();await page.getByRole('checkbox',{name:'current-a',exact:true}).uncheck();await page.getByRole('checkbox',{name:'new-model',exact:true}).check();await expect(page.getByText('本次移除')).toBeVisible();await page.getByLabel('默认模型',{exact:true}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle('new-model',{exact:true}).click();await page.screenshot({path:info.outputPath('provider-edit.png'),animations:'disabled'});expect(writes).toBe(0);await page.getByRole('button',{name:'保存修改'}).click();await expect(page.getByRole('dialog')).toHaveCount(0);expect(writes).toBe(1);
});

test('in-use models block removal and deletion with an actionable message',async({page})=>{
 const current=group();current.models[0].in_use=1;await setup(page,[current]);await page.getByRole('article').getByRole('button',{name:/^编\s*辑$/}).click();await page.getByRole('checkbox',{name:'current-a',exact:true}).uncheck();await expect(page.getByRole('alert')).toContainText('部分模型正在被工作空间使用');await expect(page.getByRole('button',{name:'保存修改'})).toBeDisabled();await page.getByRole('dialog').getByRole('button',{name:/^取\s*消$/}).click();await page.getByRole('button',{name:'删除 我的提供商',exact:true}).click();await expect(page.getByText('此提供商的模型正在被工作空间使用')).toBeVisible();await expect(page.getByRole('dialog').getByRole('button',{name:'删除账号',exact:true})).toBeDisabled();
});

test('deletion confirmation expires its success notice while cleanup status stays visible',async({page},info)=>{
 await page.clock.install();
 await setup(page,[group()]);let writes=0;
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[writes?{...group(),status:'DELETING',version:4,models:[]}:group()]}}));
 await page.route('**/v1/my-provider-accounts/'+groupID+':delete',r=>{writes++;expect(r.request().postDataJSON()).toEqual({expected_version:3});return r.fulfill({status:202,json:{...group(),status:'DELETING',version:4}});});
 await page.getByRole('button',{name:'删除 我的提供商',exact:true}).click();expect(writes).toBe(0);await expect(page.getByRole('dialog')).toContainText('及其 2 个模型');await page.getByRole('dialog').getByRole('button',{name:'删除账号',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);expect(writes).toBe(1);await expect(page.getByRole('alert')).toContainText('后台正在清理');
 await expect(page.getByText('正在删除',{exact:true})).toBeVisible();
 await page.screenshot({path:info.outputPath('provider-notice-light.png'),animations:'disabled'});
 await page.clock.fastForward(4500);
 await expect(page.getByRole('alert')).toHaveCount(0);
 await expect(page.getByText('正在删除',{exact:true})).toBeVisible();
});

for(const [status,reason] of [['UNKNOWN','MODEL_GATEWAY_CHECK_EXPIRED'],['UNHEALTHY','MODEL_ROUTE_DRIFT']])test('gateway check blocks validation/default: '+reason,async({page})=>{
 const current=group();current.models[0].gateway_check={...health,status,reason_code:reason};await setup(page,[current]);await page.getByRole('button',{name:'展开 我的提供商'}).click();const row=page.locator('.provider-connected-model').filter({hasText:'current-a'});await expect(row.getByRole('button',{name:'重试校验'})).toBeDisabled();await expect(row.getByRole('button',{name:'设为默认'})).toBeDisabled();
});

test('validation and setting defaults are model operations inside the provider',async({page})=>{
 const current=group();current.models[0].validation={...health,status:'UNKNOWN'};await setup(page,[current]);let validates=0,defaults=0;await page.route('**/v1/my-models/'+current.models[0].id+':validate',r=>{validates++;return r.fulfill({json:makeModel('current-a')});});await page.route('**/v1/my-model-default',r=>{if(r.request().method()==='GET')return r.fulfill({json:null});defaults++;expect(r.request().postDataJSON().connection_id).toBe(current.models[1].id);return r.fulfill({json:{version:1,connection_id:current.models[1].id,connection_version:1}});});await page.getByRole('button',{name:'展开 我的提供商'}).click();await page.locator('.provider-connected-model').filter({hasText:'current-a'}).getByRole('button',{name:'重试校验'}).click();await expect.poll(()=>validates).toBe(1);await page.locator('.provider-connected-model').filter({hasText:'current-b'}).getByRole('button',{name:'设为默认'}).click();await expect.poll(()=>defaults).toBe(1);
});

test('background validation progresses without a browser command and hides duplicate retry',async({page})=>{
 await page.clock.install();
 const current=group();current.models[0].validation={...health,status:'UNKNOWN',checked_at:null as unknown as string};
 await setup(page,[current]);let validates=0;
 await page.route('**/v1/my-models/*:validate',r=>{validates++;return r.fulfill({json:makeModel('current-a')});});
 await page.getByRole('button',{name:'展开 我的提供商'}).click();
 const row=page.locator('.provider-connected-model').filter({hasText:'current-a'});
 await expect(row.getByText('等待自动校验',{exact:true})).toBeVisible();
 await expect(row.getByRole('button',{name:/校验/})).toHaveCount(0);
 current.models[0].validation={...health,status:'CHECKING'};
 await page.clock.fastForward(5100);
 await expect(row.getByText('正在校验',{exact:true})).toBeVisible();
 await expect(row.getByRole('button',{name:/校验/})).toHaveCount(0);
 current.models[0].validation=health;
 await page.clock.fastForward(5100);
 await expect(row.getByText('可用',{exact:true})).toBeVisible();
 await expect(row.getByRole('button',{name:'设为默认'})).toBeEnabled();
 expect(validates).toBe(0);
});

test('failed automatic validation displays a safe reason and supports retry',async({page})=>{
 const current=group();current.models[0].validation={...health,status:'UNHEALTHY',reason_code:'MODEL_AUTH_REJECTED'};
 await setup(page,[current]);let validates=0;
 await page.route('**/v1/my-models/'+current.models[0].id+':validate',r=>{validates++;current.models[0].validation=health;return r.fulfill({json:current.models[0]});});
 await page.getByRole('button',{name:'展开 我的提供商'}).click();
 const row=page.locator('.provider-connected-model').filter({hasText:'current-a'});
 await expect(row.getByText('校验失败',{exact:true})).toBeVisible();
 await expect(row.getByRole('status')).toHaveText('密钥无效或无模型访问权限');
 await expect(row.getByRole('button',{name:'设为默认'})).toBeDisabled();
 await row.getByRole('button',{name:'重试校验'}).click();
 await expect(row.getByText('可用',{exact:true})).toBeVisible();expect(validates).toBe(1);
});

test('lost response preserves receipt for the same re-entered key and cancelled editor clears drafts',async({page})=>{
 await setup(page);await page.route('**/v1/model-providers/deepseek:sync',r=>r.fulfill({json:{items:[{id:'current-a',name:'A'}]}}));const keys:string[]=[];await page.route('**/v1/my-provider-accounts',r=>{if(r.request().method()==='GET')return r.fulfill({json:{items:[]}});keys.push(r.request().headers()['idempotency-key']);return keys.length===1?r.abort('failed'):r.fulfill({status:202,json:group()});});await add(page);await page.getByLabel('API Key',{exact:true}).fill('synthetic-retry-key');await page.getByRole('button',{name:'同步官方列表'}).click();await page.getByRole('checkbox',{name:'current-a',exact:true}).check();const save=page.getByRole('dialog').getByRole('button',{name:'添加账号',exact:true});await save.click();await expect(page.getByLabel('API Key',{exact:true})).toHaveValue('');await page.getByLabel('API Key',{exact:true}).fill('synthetic-retry-key');await save.click();await expect(page.getByRole('dialog')).toHaveCount(0);expect(keys).toHaveLength(2);expect(keys[0]).toBe(keys[1]);await add(page);await page.getByLabel('API Key',{exact:true}).fill('cancel-key');await page.getByRole('dialog').getByRole('button',{name:/^取\s*消$/}).click();await add(page);await expect(page.getByLabel('API Key',{exact:true})).toHaveValue('');
});
