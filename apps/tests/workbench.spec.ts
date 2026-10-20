import { expect, test, type Page } from '@playwright/test';

const member = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'MEMBER', role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员', status: 'ACTIVE' }], permissions: ['instances:read', 'instances:create', 'instances:start', 'configuration:read', 'personal-model:read'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };
const health = { status: 'HEALTHY', checked_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', reason_code: '' };
const defaults = { version: 3, template_id: 'template', template_version: 'template-v3', spec_id: 'spec', model_configuration_id: 'organization-model', model_configuration_version: 2, spec: { id: 'spec', cpu_millicores: 3000, memory_bytes: 8589934592, storage_bytes: 42949672960 }, retention_days: 14, max_user_instances: 5 };
const organizationModel = { id: 'organization-model', version: 2, provider: 'deepseek-official', endpoint: 'https://api.deepseek.com', protocol: 'deepseek-messages', model: 'deepseek-chat', status: 'VALID', credential_configured: true, validation: health };

async function setup(page: Page, permissions = member.permissions) {
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: { ...member, permissions } }));
  await page.route('**/v1/preflight*', r => r.fulfill({ json: { can_create: true, checks: [{ name: 'model', health }] } }));
  await page.route('**/v1/platform-checks', r => r.fulfill({ json: { ready: true, checks: [{ name: 'database', health }] } }));
  await page.route('**/v1/defaults', r => r.fulfill({ json: defaults }));
  await page.route('**/v1/model-configuration', r => r.fulfill({ json: organizationModel }));
  await page.route('**/v1/my-models', r => r.fulfill({ json: { items: [] } }));
  await page.route('**/v1/my-provider-accounts', r => r.fulfill({ json: { items: [] } }));
  await page.route('**/v1/model-providers', r => r.fulfill({ json: { items: [{id:'moonshot',name:'Moonshot AI'},{id:'deepseek',name:'DeepSeek'}] } }));
  await page.route('**/v1/my-model-default', r => r.fulfill({ json: null }));
  await page.route('**/v1/instances?*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
  await page.route('**/v1/model-catalog', r => r.fulfill({ json: { items: [] } }));
}

test('workspace hides the configuration overview and opens model providers', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', e => errors.push(e.message));
  await setup(page);
  await page.goto('/app/instances');
  await expect(page.getByRole('heading', { name: '工作空间', exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: '工作空间规格' })).not.toBeVisible();
  await expect(page.getByText('本地开发环境', { exact: true })).toHaveCount(0);
  await expect(page.getByText('平台 API 已连接', { exact: true })).toHaveCount(0);
  await expect(page.getByText('已登录组织账户', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('region', { name: '常用功能' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '创建工作空间', exact: true })).toBeEnabled();
  await expect(page.getByText('暂无工作空间', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '创建第一个实例', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '创建工作空间', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '取消', exact: true }).click();
  await page.screenshot({ path: testInfo.outputPath('workbench.png'), fullPage: true, animations: 'disabled' });
  await expect(page.getByRole('button', { name: '创建配置', exact: true })).toHaveCount(0);
  await expect(page.getByRole('region', { name: '创建时的默认模型' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: '用户管理', exact: true })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const menu = page.getByRole('button', { name: '打开导航' });
  if (await menu.isVisible()) await menu.click();
  await page.getByRole('link', { name: '模型提供商', exact: true }).filter({ visible: true }).click();
  await expect(page.getByRole('heading', { name: '模型提供商', exact: true })).toBeVisible();
  expect(errors).toEqual([]);
});

test('an unhealthy explicit model preflight blocks creation in the drawer', async ({ page }) => {
  await setup(page);
  await page.route('**/v1/my-model-default', r => r.fulfill({ json: { version: 1, connection_id: 'private', connection_version: 1 } }));
  await page.route('**/v1/my-models', r => r.fulfill({ json: { items: [{ id: 'private', version: 1, name: '我的 Kimi', model: 'kimi-k2.5', provider: 'moonshot', enabled: true, credential_configured: true, sync_status: 'SYNCED', validation: health, gateway_check: { ...health, status: 'UNHEALTHY' } }] } }));
  await page.route('**/v1/preflight*', r => r.fulfill({ json: { can_create: false, checks: [{ name: 'model', health: { ...health, status: 'UNHEALTHY', reason_code: 'VALIDATION_REQUIRED' } }] } }));
  await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[privateAccount('a','办公账号','moonshot',[privateModel('private','kimi-k2.5')])]}}));
  await page.goto('/app/instances');
  await page.getByRole('button', { name: '创建工作空间', exact: true }).click();
  await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
  await expect(page.getByText('所选模型需要校验',{exact:true})).toBeVisible();
  await expect(page.getByRole('button', { name: '创建并运行', exact: true })).toBeDisabled();
});

test('administrator reaches management through the navigation', async ({ page }) => {
  await setup(page, [...member.permissions, 'users:read', 'roles:read', 'audit:read']);
  await page.route('**/v1/users*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
  await page.route('**/v1/roles*', r => r.fulfill({ json: { items: [] } }));
  await page.goto('/app/instances');
  const menu = page.getByRole('button', { name: '打开导航' });
  if (await menu.isVisible()) await menu.click();
  await expect(page.getByRole('link', { name: '用户管理', exact: true }).filter({ visible: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '角色与权限', exact: true }).filter({ visible: true })).toBeVisible();
  await page.getByRole('link', { name: '用户管理', exact: true }).filter({ visible: true }).click();
  await expect(page.getByRole('heading', { name: '用户管理', exact: true })).toBeVisible();
});

test('failed default lookup reports failure instead of claiming unconfigured or ready', async ({ page }) => {
  await setup(page);
  await page.route('**/v1/my-model-default', r => r.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }));
  await page.route('**/v1/defaults', r => r.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }));
  await page.goto('/app/instances');
  await page.getByRole('button', { name: '创建工作空间', exact: true }).click();
  await expect(page.getByText('资源规格加载失败', { exact: true })).toBeVisible();
  await expect(page.getByRole('combobox', { name: '创建时的 CPU', exact: true })).toBeDisabled();
});

const privateModel=(id:string,model:string,version=1)=>({id,model,version,name:'个人账号',provider:'moonshot',enabled:true,credential_configured:true,sync_status:'SYNCED',gateway_check:health,validation:health});
const privateAccount=(id:string,name:string,provider:string,models:ReturnType<typeof privateModel>[])=>({id,name,provider,api_base:'https://api.example.test',version:1,status:'ACTIVE',credential_configured:true,default_model_id:models[0]?.id??null,models});
async function selectCreation(page:Page,label:string,value:string){
 await page.getByRole('combobox',{name:label,exact:true}).locator('..').locator('..').click();
 await page.locator('.ant-select-dropdown:visible').getByTitle(value,{exact:true}).click();
}

test('creation automatically uses each provider account default and submits its exact version',async({page},info)=>{
 await setup(page);
 const first=privateModel('00000000-0000-4000-8000-000000000061','kimi-k2.5',3);
 const second=privateModel('00000000-0000-4000-8000-000000000062','kimi-k2',2);
 let accounts=[privateAccount('a','办公账号','moonshot',[second,first]),privateAccount('b','研发账号','moonshot',[second]),privateAccount('c','DeepSeek账号','deepseek',[{...second,id:'other',model:'deepseek-chat'}])];
 accounts[0].default_model_id=first.id;
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:accounts}}));
 const checks:string[]=[];
 await page.route('**/v1/preflight*',r=>{checks.push(r.request().url());return r.fulfill({json:{can_create:true,checks:[]}});});
 let body:unknown;
 await page.route('**/v1/instances',r=>{body=r.request().postDataJSON();return r.fulfill({status:409,json:{code:'CONFIGURATION_INVALID'}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();await expect.poll(()=>checks.length).toBe(1);
 await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);
 await expect(page.getByRole('combobox',{name:'创建时的模型配置'})).toHaveCount(0);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 expect(checks.some(url=>url.includes('model_configuration_id='+first.id)&&url.includes('model_configuration_version=3'))).toBe(true);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('creation-provider-default.png'),animations:'disabled'});
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect.poll(()=>body).toEqual({model_configuration_id:first.id,model_configuration_version:3});
 await selectCreation(page,'创建时的模型提供商','Moonshot AI · 研发账号');
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);
 await selectCreation(page,'创建时的模型提供商','DeepSeek · DeepSeek账号');
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 accounts=accounts.filter(a=>a.provider!=='deepseek');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
});

test('creation rechecks a rotated version and waits for the new preflight result',async({page})=>{
 await page.clock.install();await setup(page);
 let selected=privateModel('00000000-0000-4000-8000-000000000061','kimi-k2.5',1);
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[privateAccount('a','办公账号','moonshot',[selected])]}}));
 let checkedVersion=0;
 await page.route('**/v1/preflight*',r=>{const version=Number(new URL(r.request().url()).searchParams.get('model_configuration_version'));checkedVersion=version;return r.fulfill({json:{can_create:version!==2,checks:[]}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 selected={...selected,version:2};await page.clock.fastForward(5100);
 await expect.poll(()=>checkedVersion).toBe(2);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
});

test('provider loading failure blocks creation and retry requires selecting an account',async({page})=>{
 await setup(page);let failed=true;
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill(failed?{status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}:{json:{items:[privateAccount('a','办公账号','moonshot',[privateModel('model-one','kimi-k2.5')])]}}));
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await expect(page.getByRole('alert').filter({hasText:'模型提供商账号加载失败'})).toBeVisible();
 await expect(page.getByRole('combobox',{name:'创建时的模型提供商'})).toBeDisabled();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 failed=false;await page.getByRole('button',{name:/^重\s*试$/}).click();
 await expect(page.getByRole('combobox',{name:'创建时的模型提供商'})).toBeEnabled();
 await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
});

const resourceModel=privateModel('00000000-0000-4000-8000-000000000061','kimi-k2.5');
async function chooseResourceModel(page:Page){await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');}
const resourceSpecs=[1000,2000].flatMap((cpu,index)=>[2,4].map((memory,memoryIndex)=>({id:`00000000-0000-4000-8000-00000000008${index*2+memoryIndex}`,cpu_millicores:cpu,memory_bytes:memory*2**30,storage_bytes:10*2**30})));
async function setupResources(page:Page){
 await setup(page);
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[privateAccount('a','办公账号','moonshot',[resourceModel])]}}));
 await page.route('**/v1/defaults',r=>r.fulfill({json:{...defaults,spec_id:resourceSpecs[3].id,spec:resourceSpecs[3]}}));
 await page.route('**/v1/templates',r=>r.fulfill({json:{items:resourceSpecs.map(spec=>({id:defaults.template_id,template_version:defaults.template_version,name:'approved',backend_type:'docker',image_digest:'sha256:fixture',spec}))}}));
}

test('creation binds CPU memory and selected provider model to preflight and exact request',async({page},info)=>{
 await setupResources(page);
 const model=privateModel('00000000-0000-4000-8000-000000000061','kimi-k2.5',3);
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[privateAccount('a','办公账号','moonshot',[model])]}}));
 let inspected='';let body:unknown;
 await page.route('**/v1/preflight*',r=>{inspected=r.request().url();return r.fulfill({json:{can_create:true,checks:[]}});});
 await page.route('**/v1/instances',r=>{body=r.request().postDataJSON();return r.fulfill({status:409,json:{code:'CONFIGURATION_INVALID'}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await selectCreation(page,'创建时的 CPU','1 核');await selectCreation(page,'创建时的内存','2 GiB');
 await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 await expect.poll(()=>new URL(inspected).searchParams.get('spec_id')).toBe(resourceSpecs[0].id);
 expect(new URL(inspected).searchParams.get('model_configuration_id')).toBe(model.id);
 await expect(page.getByRole('combobox',{name:'创建时的存储容量'}).locator('..').locator('..')).toContainText('10 GiB');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('creation-resources-model.png'),fullPage:true,animations:'disabled'});
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect.poll(()=>body).toEqual({model_configuration_id:model.id,model_configuration_version:3,spec_id:resourceSpecs[0].id});
});

test('resource quota preflight blocks an oversized choice then allows a smaller one',async({page})=>{
 await setupResources(page);
 await page.route('**/v1/preflight*',r=>{const small=new URL(r.request().url()).searchParams.get('spec_id')===resourceSpecs[0].id;return r.fulfill({json:{can_create:small,checks:small?[]:[{name:'quota',health:{...health,status:'UNHEALTHY',reason_code:'QUOTA_EXCEEDED'}}]}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await chooseResourceModel(page);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await expect(page.getByRole('status').filter({hasText:'资源额度不足，请降低资源规格或联系管理员调整配额'})).toBeVisible();
 await selectCreation(page,'创建时的 CPU','1 核');await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await selectCreation(page,'创建时的内存','2 GiB');await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
});

test('creation shows available quota before model selection and checks expanded CPU and memory choices',async({page},info)=>{
 await setupResources(page);
 const expandedSpecs=[1000,2000,4000,8000,16000].flatMap((cpu,i)=>[2,4,8,16,32,64].map((gib,j)=>({id:`00000000-0000-4000-8000-${String(100+i*6+j).padStart(12,'0')}`,cpu_millicores:cpu,memory_bytes:gib*2**30,storage_bytes:10*2**30})));
 const defaultSpec=expandedSpecs.find(spec=>spec.cpu_millicores===2000&&spec.memory_bytes===4*2**30)!;
 await page.route('**/v1/defaults',r=>r.fulfill({json:{...defaults,spec_id:defaultSpec.id,spec:defaultSpec}}));
 await page.route('**/v1/templates',r=>r.fulfill({json:{items:expandedSpecs.map(spec=>({id:defaults.template_id,template_version:defaults.template_version,name:'approved',backend_type:'docker',image_digest:'sha256:fixture',spec}))}}));
 const available={max_instances:1,cpu_millicores:8000,memory_bytes:32*2**30,storage_bytes:20*2**30};
 await page.route('**/v1/preflight*',r=>{
  const spec=expandedSpecs.find(spec=>spec.id===new URL(r.request().url()).searchParams.get('spec_id'))??defaultSpec;
  const enough=spec.cpu_millicores<=available.cpu_millicores&&spec.memory_bytes<=available.memory_bytes;
  return r.fulfill({json:{can_create:enough,checks:enough?[]:[{name:'quota',health:{...health,status:'UNHEALTHY',reason_code:'QUOTA_EXCEEDED'}}],quota:{available,required:{max_instances:1,cpu_millicores:spec.cpu_millicores,memory_bytes:spec.memory_bytes,storage_bytes:spec.storage_bytes},concurrent_creates_available:1}}});
 });
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 const cpuRemaining=page.getByLabel('CPU剩余额度',{exact:true});
 await expect(cpuRemaining).toHaveText('剩余 8 核');
 await expect(page.getByRole('table',{name:'剩余额度与本次创建所需资源'})).toHaveCount(0);
 await expect(page.getByText('可用额度取个人与系统剩余额度的较小值，包含已预留资源和保留数据。以提交时检查结果为准。',{exact:true})).toHaveCount(0);
 await expect(page.getByLabel('实例剩余额度',{exact:true})).toHaveText('剩余 1 个实例');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await selectCreation(page,'创建时的 CPU','16 核');await selectCreation(page,'创建时的内存','64 GiB');await chooseResourceModel(page);
 await expect(cpuRemaining).toHaveText('剩余 8 核（不足）');
 await expect(page.getByLabel('内存剩余额度',{exact:true})).toHaveText('剩余 32 GiB（不足）');
 await expect(page.getByLabel('存储剩余额度',{exact:true})).toHaveText('剩余 20 GiB');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('available-quota-expanded-specs.png'),fullPage:true,animations:'disabled'});
 await selectCreation(page,'创建时的 CPU','4 核');await selectCreation(page,'创建时的内存','8 GiB');
 await expect(cpuRemaining).toHaveText('剩余 8 核');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
});

test('creation keeps zero quota distinct from unavailable data and explains full concurrency',async({page})=>{
 await setupResources(page);let failed=false;
 await page.route('**/v1/preflight*',r=>failed?r.fulfill({status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}):r.fulfill({json:{can_create:false,checks:[{name:'quota',health:{...health,status:'UNHEALTHY',reason_code:'QUOTA_EXCEEDED'}}],quota:{available:{max_instances:0,cpu_millicores:0,memory_bytes:0,storage_bytes:0},required:{max_instances:1,cpu_millicores:2000,memory_bytes:4*2**30,storage_bytes:10*2**30},concurrent_creates_available:0}}}));
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await chooseResourceModel(page);
 const cpuRemaining=page.getByLabel('CPU剩余额度',{exact:true});
 await expect(cpuRemaining).toHaveText('剩余 0 核（不足）');
 await expect(page.getByLabel('存储剩余额度',{exact:true})).toHaveText('剩余 0 GiB（不足）');
 await expect(page.getByText('当前创建并发名额已满，请等待现有操作完成后重新检查。',{exact:true})).toBeVisible();
 failed=true;await page.getByRole('button',{name:'重新检查',exact:true}).click();
 await expect(cpuRemaining).not.toBeVisible();await expect(page.getByText('暂时无法检查创建条件，请重新检查',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
});

for(const reason of ['QUOTA_EXCEEDED','CAPACITY_UNAVAILABLE']) {
 test(`creation shows ${reason} even while the selected model awaits validation`,async({page})=>{
  await setupResources(page);
  const model={...resourceModel,validation:{...health,status:'CHECKING'}};
  await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[privateAccount('a','办公账号','moonshot',[model])]}}));
  let available=false;
  await page.route('**/v1/preflight*',r=>r.fulfill({json:{can_create:available,checks:[
   {name:'model',health:{...health,status:'UNHEALTHY',reason_code:'VALIDATION_REQUIRED'}},
   {name:reason==='QUOTA_EXCEEDED'?'quota':'capacity',health:{...health,status:available?'HEALTHY':'UNHEALTHY',reason_code:available?'':reason}},
  ]}}));
  await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await chooseResourceModel(page);
  const message=reason==='QUOTA_EXCEEDED'?'资源额度不足，请降低资源规格或联系管理员调整配额':'当前空闲 CPU 或内存不足，请停止不再使用的工作空间后重新检查，或联系管理员扩容';
  await expect(page.getByRole('status').filter({hasText:message})).toBeVisible();
  await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
  available=true;await page.getByRole('button',{name:'重新检查',exact:true}).click();
  await expect(page.getByText('所选模型暂不可用，请等待校验完成或重新选择',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 });
}

test('lost creation response freezes selected resources until the original request is confirmed',async({page})=>{
 await setupResources(page);const bodies:unknown[]=[];const keys:(string|undefined)[]=[];
 await page.route('**/v1/instances',r=>{bodies.push(r.request().postDataJSON());keys.push(r.request().headers()['idempotency-key']);return bodies.length===1?r.abort('failed'):r.fulfill({status:409,json:{code:'CONFIGURATION_INVALID'}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await selectCreation(page,'创建时的 CPU','1 核');await selectCreation(page,'创建时的内存','2 GiB');await chooseResourceModel(page);
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认创建结果',exact:true})).toBeEnabled();
 await expect(page.getByText('上次创建结果待确认',{exact:true})).toBeVisible();await expect(page.getByRole('combobox',{name:'创建时的 CPU'})).toHaveCount(0);
 await page.reload();await page.getByRole('button',{name:'查看待确认创建',exact:true}).click();await page.getByRole('button',{name:'确认创建结果',exact:true}).click();
 await expect.poll(()=>bodies.length).toBe(2);expect(bodies).toEqual(Array(2).fill({model_configuration_id:resourceModel.id,model_configuration_version:1,spec_id:resourceSpecs[0].id}));expect(keys[1]).toBe(keys[0]);
});

test('creation opens a drawer and cancellation discards edits without creating a workspace',async({page},info)=>{
 await setupResources(page);let writes=0;
 await page.route('**/v1/instances',r=>{writes++;return r.fulfill({status:409,json:{code:'CONFIGURATION_INVALID'}});});
 await page.goto('/app/instances');
 await expect(page.getByRole('combobox',{name:'创建时的 CPU'})).toHaveCount(0);
 await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 const drawer=page.getByRole('dialog',{name:'创建工作空间',exact:true});await expect(drawer).toBeVisible();
 await selectCreation(page,'创建时的 CPU','1 核');await selectCreation(page,'创建时的内存','2 GiB');
 const footer=drawer.getByRole('button',{name:'创建并运行',exact:true});const bounds=await footer.boundingBox();expect(bounds&&bounds.y>=0&&bounds.y+bounds.height<=page.viewportSize()!.height).toBe(true);
 await page.screenshot({path:info.outputPath('creation-drawer.png'),fullPage:true,animations:'disabled'});
 await drawer.getByRole('button',{name:'取消',exact:true}).click();await expect(drawer).not.toBeVisible();expect(writes).toBe(0);
 await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await expect(drawer.getByRole('combobox',{name:'创建时的 CPU'}).locator('..').locator('..')).toContainText('2 核');
 await expect(drawer.getByRole('combobox',{name:'创建时的内存'}).locator('..').locator('..')).toContainText('4 GiB');
 await page.keyboard.press('Escape');await expect(drawer).not.toBeVisible();expect(writes).toBe(0);
});

test('pending creation cannot be dismissed or double submitted and closes after acceptance',async({page})=>{
 await setupResources(page);let release!:()=>void;const held=new Promise<void>(resolve=>{release=resolve;});let writes=0;
 const id='00000000-0000-4000-8000-000000000091';
 await page.route('**/v1/instances/'+id,r=>r.fulfill({status:404,json:{code:'NOT_FOUND'}}));
 await page.route('**/v1/instances',async r=>{writes++;await held;await r.fulfill({status:202,json:{instance_id:id,operation_id:'00000000-0000-4000-8000-000000000092',state:'CREATING'}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await chooseResourceModel(page);
 const drawer=page.getByRole('dialog',{name:'创建工作空间',exact:true});
 await expect(drawer.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();await drawer.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect.poll(()=>writes).toBe(1);await expect(drawer.getByRole('button',{name:'取消',exact:true})).toBeDisabled();
 await page.keyboard.press('Escape');await expect(drawer).toBeVisible();await page.keyboard.press('Enter');expect(writes).toBe(1);
 release();await expect(page).toHaveURL(new RegExp('/app/instances/'+id+'$'));await expect(drawer).not.toBeVisible();expect(writes).toBe(1);
});


test('workspace list keeps filtering refresh and detail navigation on desktop and mobile',async({page},info)=>{
 await setup(page);
 const instance={id:'workspace-ui-test',owner_id:member.id,name:'研发工作空间',model_provider_name:'Moonshot AI',model_display_name:'kimi-k2.5',observed_state:'RUNNING',desired_state:'RUNNING',ready:false,can_enter:false,allowed_actions:[],version:1,active_operation_id:null,created_at:'2026-10-15T00:00:00Z',updated_at:'2026-10-15T00:00:00Z',retention_until:null,storage:null,config_snapshot:{model:'kimi-k2.5',model_configuration_version:3,spec:{id:'small',cpu_millicores:1000,memory_bytes:2*2**30,storage_bytes:10*2**30}},health:{application:health,storage:health,configuration:health,access:health,model:health}};
 const reads:URLSearchParams[]=[];
 await page.route('**/v1/instances?*',r=>{const params=new URL(r.request().url()).searchParams;reads.push(params);const items=params.get('state')==='STOPPED'?[]:[instance];return r.fulfill({json:{items,total:items.length,next_cursor:null}});});
 await page.route('**/v1/instances/workspace-ui-test',r=>r.fulfill({json:instance}));
 await page.goto('/app/instances');
 const record=page.getByRole('article');
 await expect(record.getByRole('button',{name:'研发工作空间',exact:true})).toBeVisible();
 await expect(record.getByText('健康待确认',{exact:true})).toBeVisible();
 await expect(record.getByText('1 核 CPU · 2 GiB 内存 · 10 GiB 存储',{exact:true})).toBeVisible();
 await expect(record.getByText('Moonshot AI · kimi-k2.5',{exact:true})).toBeVisible();
 const count=reads.length;await page.getByRole('button',{name:'刷新实例列表',exact:true}).click();await expect.poll(()=>reads.length).toBe(count+1);
 await page.getByRole('radiogroup',{name:'实例状态筛选'}).getByText('已停止',{exact:true}).click();await expect(page.getByText('没有符合条件的实例',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'创建第一个实例',exact:true})).toHaveCount(0);expect(reads.at(-1)?.get('state')).toBe('STOPPED');
 await page.getByRole('radiogroup',{name:'实例状态筛选'}).getByText('全部',{exact:true}).click();await expect(record).toBeVisible();
 const search=page.getByRole('searchbox',{name:'搜索实例',exact:true});await search.fill('研发');await expect.poll(()=>reads.at(-1)?.get('q')).toBe('研发');
 await page.locator('.list-search-controls .ant-input-clear-icon').click();await expect(search).toHaveValue('');await expect.poll(()=>reads.at(-1)?.get('q')??'').toBe('');
 await search.fill('研发');await search.press('Enter');await expect.poll(()=>reads.at(-1)?.get('q')).toBe('研发');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('workspace-list.png'),fullPage:true,animations:'disabled'});
 await record.getByRole('button',{name:'实例详情',exact:true}).click();await expect(page).toHaveURL(/\/app\/instances\/workspace-ui-test$/);await expect(page.getByRole('heading',{name:'研发工作空间',exact:true})).toBeVisible();
});

test('an unavailable account default cannot fall back to another healthy model',async({page})=>{
 await page.clock.install();await setup(page);
 const unhealthy={...privateModel('default-unhealthy','kimi-k2.5'),validation:{...health,status:'UNHEALTHY'}};
 const fallback=privateModel('other-healthy','kimi-k2');
 const account={...privateAccount('a','办公账号','moonshot',[unhealthy,fallback]),default_model_id:unhealthy.id as string|null};
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[account]}}));let checks=0;
 await page.route('**/v1/preflight*',r=>{checks++;return r.fulfill({json:{can_create:true,checks:[]}});});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await selectCreation(page,'创建时的模型提供商','Moonshot AI · 办公账号');
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();await expect.poll(()=>checks).toBe(2);
 await expect(page.getByText('所选模型暂不可用，请等待校验完成或重新选择',{exact:true})).toBeVisible();
 account.default_model_id=null;await page.clock.fastForward(5100);
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();expect(checks).toBe(2);
});
