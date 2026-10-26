import { expect,test,type Page } from '@playwright/test';

const current={id:'00000000-0000-4000-8000-000000000041',memberships:[{org_id:'00000000-0000-4000-8000-000000000040',role:'ADMIN',status:'ACTIVE',role_id:'00000000-0000-4000-8000-000000000004',role_name:'管理员'}],permissions:['instances:read','configuration:read','model:write','model:validate','model:revoke','defaults:write','models:catalog:manage'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const defaults={version:3,template_id:'template',template_version:'version',spec_id:'spec',model_configuration_id:'model',model_configuration_version:99,spec:{id:'spec',cpu_millicores:2000,memory_bytes:4*2**30,storage_bytes:10*2**30},retention_days:7,max_user_instances:2};
const health={status:'HEALTHY',checked_at:'2026-01-01T00:00:00Z',expires_at:'2099-01-01T00:00:00Z',reason_code:''};
const templates=[{id:'template',template_version:'version',name:'DSH',backend_type:'docker',image_digest:'sha256:test',spec:defaults.spec},{id:'template',template_version:'version',name:'DSH',backend_type:'docker',image_digest:'sha256:test',spec:{id:'small',cpu_millicores:1000,memory_bytes:2*2**30,storage_bytes:10*2**30}},{id:'template',template_version:'version',name:'DSH',backend_type:'docker',image_digest:'sha256:test',spec:{id:'medium',cpu_millicores:1000,memory_bytes:4*2**30,storage_bytes:10*2**30}}];
async function setup(page:Page){
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.route('**/v1/**',r=>r.fulfill({status:404,json:{code:'NOT_FOUND'}}));
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',r=>r.fulfill({json:current}));
 await page.route('**/v1/defaults',r=>r.fulfill({json:defaults}));
 await page.route('**/v1/templates',r=>r.fulfill({json:{items:templates}}));
 await page.route('**/v1/organization-quota',r=>r.fulfill({json:{version:1,limits:{max_instances:20,cpu_millicores:40000,memory_bytes:80*2**30,storage_bytes:200*2**30},usage:{max_instances:0,cpu_millicores:0,memory_bytes:0,storage_bytes:0},default_user_instances:2}}));
 await page.route('**/v1/platform-checks',r=>r.fulfill({json:{ready:true,checks:[{name:'database',health},{name:'spec',health},{name:'model',health},{name:'model_adapter',health}]}}));
}

test('platform configuration displays runtime settings without model controls or model API requests',async({page},info)=>{
 await setup(page);const modelRequests:string[]=[];
 page.on('request',r=>{if(/\/v1\/(model-|my-)/.test(new URL(r.url()).pathname))modelRequests.push(r.url());});
 await page.goto('/app/settings');
 const runtime=page.getByRole('region',{name:'默认用户资源配置',exact:true});
 await expect(runtime.getByText('2 核',{exact:true})).toBeVisible();await expect(runtime.getByText('4 GiB',{exact:true})).toBeVisible();await expect(runtime.getByText('10 GiB',{exact:true})).toBeVisible();
 await expect(page.locator('input[type=password]')).toHaveCount(0);
 for(const name of ['保存模型配置','校验模型连接','撤销此凭据','将当前模型设为默认配置','刷新模型目录'])await expect(page.getByRole('button',{name,exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'刷新检查结果',exact:true}).click();
 await expect(page.getByText('数据库',{exact:true})).toBeVisible();await expect(page.getByText('运行规格',{exact:true})).toBeVisible();
 await expect(page.getByText('默认模型',{exact:true})).toHaveCount(0);await expect(page.getByText('外部模型运行适配',{exact:true})).toHaveCount(0);
 expect(modelRequests).toEqual([]);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('platform-runtime-only.png'),fullPage:true,animations:'disabled'});
});

test('runtime configuration read failure can be retried without writing or requesting model settings',async({page})=>{
 await setup(page);let failed=true;const methods:string[]=[];
 await page.route('**/v1/defaults',r=>{methods.push(r.request().method());return r.fulfill(failed?{status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}:{json:defaults});});
 await page.goto('/app/settings');await expect(page.getByRole('alert').filter({hasText:'依赖服务暂不可用'})).toBeVisible();
 failed=false;await page.getByRole('button',{name:'重新加载',exact:true}).click();await expect(page.getByText('2 核',{exact:true})).toBeVisible();expect(methods.every(method=>method==='GET')).toBe(true);
});

test('missing runtime defaults shows initialization state',async({page})=>{
 await setup(page);await page.route('**/v1/defaults',r=>r.fulfill({status:404,json:{code:'DEFAULTS_NOT_CONFIGURED'}}));
 await page.goto('/app/settings');await expect(page.getByText('尚未初始化默认用户资源配置',{exact:true})).toBeVisible();await expect(page.getByText('模型连接',{exact:true})).toHaveCount(0);
});

test('administrator initializes runtime defaults without loading or submitting a model',async({page})=>{
 await setup(page);let saved:typeof defaults|null=null;const writes:unknown[]=[];const models:string[]=[];
 page.on('request',r=>{if(/\/v1\/(model-|my-)/.test(new URL(r.url()).pathname))models.push(r.url());});
 await page.route('**/v1/defaults',r=>{
  if(r.request().method()==='PUT'){const body=r.request().postDataJSON();writes.push(body);saved={...defaults,...body,version:1,model_configuration_id:'',model_configuration_version:0};return r.fulfill({json:saved});}
  return r.fulfill(saved?{json:saved}:{status:404,json:{code:'DEFAULTS_NOT_CONFIGURED'}});
 });
 await page.goto('/app/settings');await page.getByRole('button',{name:'初始化配置',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'初始化默认用户资源配置',exact:true});
 await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeEnabled();
 await dialog.getByRole('spinbutton',{name:'每用户默认实例上限',exact:true}).fill('3');
 await dialog.getByRole('spinbutton',{name:'数据保留期限',exact:true}).fill('14');
 await dialog.getByRole('button',{name:'保存配置',exact:true}).click();
 await expect(dialog).not.toBeVisible();await expect(page.getByText('14 天',{exact:true})).toBeVisible();
 expect(writes).toEqual([{version:0,template_id:'template',template_version:'version',spec_id:'spec',max_user_instances:3,retention_days:14}]);expect(models).toEqual([]);
 await expect(page.getByText('默认用户资源配置已保存',{exact:true})).toBeVisible();
 await expect(page.getByText('默认用户资源配置已保存',{exact:true})).not.toBeVisible({timeout:6000});
});

test('administrator edits approved resources and retention while cancellation writes nothing',async({page},info)=>{
 await setup(page);const writes:unknown[]=[];
 await page.route('**/v1/defaults',r=>{if(r.request().method()==='PUT'){const body=r.request().postDataJSON();writes.push(body);return r.fulfill({json:{...defaults,...body,version:4,spec:templates.find(x=>x.spec.id===body.spec_id)!.spec}});}return r.fulfill({json:defaults});});
 await page.goto('/app/settings');await page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true}).click();
 let dialog=page.getByRole('dialog',{name:'修改默认用户资源配置',exact:true});
 await dialog.getByRole('button',{name:/^取\s*消$/}).click();expect(writes).toEqual([]);
 await page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true}).click();dialog=page.getByRole('dialog',{name:'修改默认用户资源配置',exact:true});
 await dialog.getByRole('combobox',{name:'默认 CPU',exact:true}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle('1 核',{exact:true}).click();
 await dialog.getByRole('combobox',{name:'默认内存',exact:true}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle('2 GiB',{exact:true}).click();
 await dialog.getByRole('spinbutton',{name:'数据保留期限',exact:true}).fill('30');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('runtime-defaults-editor.png'),fullPage:true});
 await dialog.getByRole('button',{name:'保存配置',exact:true}).click();await expect(dialog).not.toBeVisible();
 expect(writes).toEqual([{version:3,template_id:'template',template_version:'version',spec_id:'small',max_user_instances:2,retention_days:30}]);
});

test('runtime defaults version conflict requires explicit reload before saving the new version',async({page})=>{
 await setup(page);let version=3;const writes:number[]=[];
 await page.route('**/v1/defaults',r=>{if(r.request().method()==='PUT'){const body=r.request().postDataJSON();writes.push(body.version);if(body.version===3){version=4;return r.fulfill({status:409,json:{code:'VERSION_CONFLICT'}});}return r.fulfill({json:{...defaults,...body,version:5}});}return r.fulfill({json:{...defaults,version}});});
 await page.goto('/app/settings');await page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'修改默认用户资源配置',exact:true});await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeEnabled();
 await dialog.getByRole('button',{name:'保存配置',exact:true}).click();await expect(dialog.getByText('配置已被更新，请重新加载后保存',{exact:true})).toBeVisible();
 await dialog.getByRole('button',{name:'重新读取配置',exact:true}).click();await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeEnabled();
 await dialog.getByRole('button',{name:'保存配置',exact:true}).click();await expect(dialog).not.toBeVisible();expect(writes).toEqual([3,4]);
});

test('read-only user cannot initialize or edit runtime defaults',async({page})=>{
 await setup(page);await page.route('**/v1/me',r=>r.fulfill({json:{...current,permissions:['instances:read','configuration:read']}}));
 await page.goto('/app/settings');await expect(page.getByText('2 核',{exact:true})).toBeVisible();await expect(page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true})).toHaveCount(0);
});

test('runtime settings with an expired session return to login',async({page})=>{
 await setup(page);let expired=false;
 await page.route('**/auth/login?*',r=>{expect(new URL(r.request().url()).searchParams.get('return_to')).toBe('/app/settings');return r.fulfill({contentType:'text/html; charset=utf-8',body:'<h1>登录账户</h1>'});});
 await page.route('**/v1/me',r=>r.fulfill(expired?{status:401,json:{code:'UNAUTHENTICATED'}}:{json:current}));
 await page.route('**/v1/defaults',r=>{expired=true;return r.fulfill({status:401,json:{code:'UNAUTHENTICATED'}});});
 await page.goto('/app/settings');await expect(page).toHaveURL(/\/auth\/login\?/);await expect(page.getByRole('heading',{name:'登录账户',exact:true})).toBeVisible();
});

test('runtime configuration stays protected by configuration read permission',async({page})=>{
 await setup(page);await page.route('**/v1/me',r=>r.fulfill({json:{...current,permissions:['instances:read']}}));
 let reads=0;await page.route('**/v1/defaults',r=>{reads++;return r.fulfill({json:defaults});});
 await page.goto('/app/settings');await expect(page.getByText('没有配置读取权限',{exact:true})).toBeVisible();expect(reads).toBe(0);
});


test('runtime storage is numeric and maps approved capacities without a template selector',async({page})=>{
 await setup(page); const large={...templates[0],spec:{...defaults.spec,id:'storage-20',storage_bytes:20*2**30}};const writes:unknown[]=[];let saved=defaults;
 await page.route('**/v1/templates',r=>r.fulfill({json:{items:[...templates,large]}}));
 await page.route('**/v1/defaults',r=>{if(r.request().method()==='PUT'){const body=r.request().postDataJSON();writes.push(body);saved={...defaults,...body,version:4,spec:large.spec};return r.fulfill({json:saved});}return r.fulfill({json:saved});});
 await page.goto('/app/settings');await page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'修改默认用户资源配置',exact:true});
 await expect(dialog.getByText('运行模板',{exact:true})).toHaveCount(0);await expect(dialog.getByRole('combobox',{name:'默认运行模板'})).toHaveCount(0);
 const storage=dialog.getByRole('spinbutton',{name:'默认存储',exact:true});await expect(storage).toHaveValue('10');
 await storage.fill('20');await storage.blur();await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeEnabled();await dialog.getByRole('button',{name:'保存配置',exact:true}).click();
 await expect(dialog).not.toBeVisible();await expect(page.getByText('20 GiB',{exact:true})).toBeVisible();expect(writes).toEqual([{version:3,template_id:'template',template_version:'version',spec_id:'storage-20',max_user_instances:2,retention_days:7}]);
});

test('runtime storage rejects fractional empty and unapproved values without writes',async({page})=>{
 await setup(page);let writes=0;await page.route('**/v1/defaults',r=>{if(r.request().method()==='PUT')writes++;return r.fulfill({json:defaults});});
 await page.goto('/app/settings');await page.getByRole('region',{name:'默认用户资源配置',exact:true}).getByRole('button',{name:'修改配置',exact:true}).click();const dialog=page.getByRole('dialog',{name:'修改默认用户资源配置',exact:true});const storage=dialog.getByRole('spinbutton',{name:'默认存储',exact:true});
 for(const [value,message] of [['','请输入存储容量'],['10.5','请输入 1–100 的整数'],['20','此存储容量尚未批准，请选择已批准容量']]){await storage.fill(value);await storage.blur();await expect(dialog.getByText(message,{exact:true})).toBeVisible();await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeDisabled();}
 await storage.fill('10');await storage.blur();await expect(dialog.getByRole('button',{name:'保存配置',exact:true})).toBeEnabled();expect(writes).toBe(0);
});
