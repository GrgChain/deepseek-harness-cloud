import { expect, test, type Page } from '@playwright/test';
const user={id:'00000000-0000-4000-8000-000000000001',memberships:[{org_id:'00000000-0000-4000-8000-000000000002',role:'MEMBER',status:'ACTIVE', role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员' }], permissions: ["instances:read", "instances:create", "instances:start", "instances:stop", "instances:delete", "instances:configure", "instances:access", "instances:retry", "configuration:read", "audit:read"], auth_generation: 1,csrf_token:'synthetic-csrf-token-32-characters-long',session_expires_at:'2099-01-01T00:00:00Z'};
const id='00000000-0000-4000-8000-000000000003';const operation='00000000-0000-4000-8000-000000000004';
const health={status:'UNKNOWN',checked_at:null,expires_at:null,reason_code:'HEALTH_REQUIRED'};
const instance={id,owner_id:user.id,name:'work-one',desired_state:'RUNNING',observed_state:'RUNNING',version:1,ready:false,can_enter:false,allowed_actions:['stop','delete','configure'],active_operation_id:null,access_host:null,storage:{capacity_bytes:10*2**30,status:'READY'},config_snapshot:{model:'deepseek-flash',spec:{cpu_millicores:2000,memory_bytes:4*2**30,storage_bytes:10*2**30}},health:{application:health,storage:health,configuration:health,access:health,model:health},created_at:'2026-10-10T00:00:00Z',updated_at:'2026-10-10T00:00:00Z',retention_until:null};
async function session(page:Page){
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.route('**/v1/**',route=>route.fulfill({status:404,json:{code:'NOT_FOUND'}}));
 await page.route('**/livez',route=>route.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true,instances:false}}}));
 await page.route('**/readyz',route=>route.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',route=>route.fulfill({json:user}));
 await page.route('**/v1/instances?*',route=>route.fulfill({json:{items:[],next_cursor:null}}));
 await page.route('**/v1/instances/'+id,route=>route.fulfill({json:instance}));
}

test('create and update provider use only checked account defaults and never fall back after selection is lost',async({page})=>{
 test.setTimeout(45_000);
 await session(page);
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'personal-model:read']}}));
 const approved={id:'00000000-0000-4000-8000-000000000011',name:'已核对个人模型',version:1,provider:'moonshot',model:'kimi-k2.5',enabled:true,credential_configured:true,sync_status:'SYNCED',validation:{status:'HEALTHY'},gateway_check:{status:'HEALTHY'}};
 const invalid=[{...approved,id:'00000000-0000-4000-8000-000000000012',name:'漂移个人模型',gateway_check:{status:'UNHEALTHY'}},{...approved,id:'00000000-0000-4000-8000-000000000013',name:'过期个人模型',gateway_check:{status:'UNKNOWN'}}];
 let items=[approved,...invalid];let writes=0;let organizationReads=0;const configurations:unknown[]=[];
 await page.route('**/v1/my-models',r=>r.fulfill({json:{items}}));
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[{id:'provider-account',name:'我的账号',provider:'moonshot',status:'ACTIVE',default_model_id:approved.id,models:items}]}}));
 await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:[{id:'moonshot',name:'Moonshot AI'}]}}));
 await page.route('**/v1/preflight*',r=>r.fulfill({json:{can_create:true,checks:[]}}));
 await page.route('**/v1/model-configuration',r=>{organizationReads++;return r.fulfill({status:403,json:{code:'FORBIDDEN'}});});
 await page.route('**/v1/instances/'+id+':configure',r=>{configurations.push(r.request().postDataJSON());return r.fulfill({status:202,json:{instance_id:id,operation_id:operation,state:'STARTING'}});});
 await page.route('**/v1/instances',r=>{if(r.request().method()==='POST'){writes++;return r.fulfill({status:409,json:{code:'CONFIGURATION_INVALID'}});}return r.fallback();});
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled({timeout:8000});
 await page.getByRole('combobox',{name:'创建时的模型提供商',exact:true}).locator('..').locator('..').click();
 await page.locator('.ant-select-dropdown:visible').getByTitle('Moonshot AI · 我的账号',{exact:true}).click();
 await expect(page.getByRole('textbox',{name:'创建时的默认模型'})).toHaveCount(0);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 items=invalid;
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled({timeout:8000});
 items=[approved,...invalid];
 await page.goto('/app/instances/'+id);
 await page.getByRole('button',{name:'更新模型提供商',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'更新模型提供商',exact:true});
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeDisabled();
 await dialog.getByRole('combobox',{name:'应用到实例的模型提供商',exact:true}).locator('..').locator('..').click();
 await expect(page.getByTitle('组织模型',{exact:true})).toHaveCount(0);
 await page.locator('.ant-select-dropdown:visible').getByTitle('Moonshot AI · 我的账号',{exact:true}).click();
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeEnabled();
 items=invalid;
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeDisabled({timeout:8000});
 expect(writes).toBe(0);expect(configurations).toHaveLength(0);expect(organizationReads).toBe(0);
 items=[approved,...invalid];
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeEnabled({timeout:8000});
 await dialog.getByRole('button',{name:'应用配置',exact:true}).click();
 await expect(dialog).not.toBeVisible();
 expect(configurations).toEqual([{expected_instance_version:instance.version,model_configuration_id:approved.id,model_configuration_version:approved.version}]);
 expect(organizationReads).toBe(0);
});

test('provider update blocks a failed account read and recovers without organization defaults',async({page})=>{
 await session(page);let failed=true;let organizationReads=0;
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'personal-model:read']}}));
 const model={id:'private-model',version:3,model:'deepseek-flash',enabled:true,credential_configured:true,sync_status:'SYNCED',validation:{status:'HEALTHY'},gateway_check:{status:'HEALTHY'}};
 const account={id:'private-account',name:'研发账号',provider:'deepseek',status:'ACTIVE',default_model_id:model.id,models:[model]};
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill(failed?{status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}:{json:{items:[account,{...account,id:'disabled-account',provider:'moonshot',status:'DELETED'}]}}));
 await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:[{id:'deepseek',name:'DeepSeek'},{id:'moonshot',name:'Moonshot AI'}]}}));
 await page.route('**/v1/model-configuration',r=>{organizationReads++;return r.fulfill({status:403,json:{code:'FORBIDDEN'}});});
 await page.goto('/app/instances/'+id);await page.getByRole('button',{name:'更新模型提供商',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'更新模型提供商',exact:true});
 await expect(dialog.getByText('模型提供商账号加载失败',{exact:true})).toBeVisible();
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeDisabled();
 failed=false;await dialog.getByRole('button',{name:'重新加载',exact:true}).click();
 await dialog.getByRole('combobox',{name:'应用到实例的模型提供商',exact:true}).locator('..').locator('..').click();
 await expect(page.getByTitle('组织模型',{exact:true})).toHaveCount(0);
 await expect(page.getByTitle('Moonshot AI',{exact:true})).toHaveCount(0);
 await page.locator('.ant-select-dropdown:visible').getByTitle('DeepSeek · 研发账号',{exact:true}).click();
 await expect(dialog.getByRole('button',{name:'应用配置',exact:true})).toBeEnabled();
 expect(organizationReads).toBe(0);
});

test('latest reconciled failure retries with no body and resumes the same request after reload',async({page})=>{
 await session(page);const keys:string[]=[];let attempts=0;
 await page.route('**/v1/instances/'+id,route=>route.fulfill({json:{...instance,observed_state:'ERROR',allowed_actions:[],latest_operation_id:operation}}));
 await page.route('**/v1/operations/'+operation,route=>route.fulfill({json:{id:operation,instance_id:id,type:'START',status:'FAILED',stage:'COMPLETE',error:{code:'DEPENDENCY_UNAVAILABLE',message:'依赖暂不可用',request_id:'failed-trace'},retry_allowed:true,updated_at:instance.updated_at}}));
 await page.route('**/v1/operations/'+operation+':retry',route=>{
  expect(route.request().postData()).toBeNull();keys.push(route.request().headers()['idempotency-key']);attempts++;
  if(attempts===1)return route.abort('failed');
  return route.fulfill({status:202,json:{instance_id:id,operation_id:'00000000-0000-4000-8000-000000000005',state:'STARTING'}});
 });
 await page.goto('/app/instances/'+id);
 await expect(page.getByRole('region',{name:'最近操作',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'重试失败操作',exact:true}).click();
 await expect(page.getByText(/提交结果待确认/)).toBeVisible();
 await page.reload();await page.getByRole('button',{name:'确认上次重试结果',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认上次重试结果',exact:true})).not.toBeVisible();
 expect(keys).toHaveLength(2);expect(keys[1]).toBe(keys[0]);
});
test('lost create response is restored after reload and reuses the original submission',async({page})=>{
 await session(page);let count=0;const keys:string[]=[];const bodies:unknown[]=[];let ready=true;
 const selectedModel={id:'00000000-0000-4000-8000-000000000061',version:1,model:'kimi-k2.5',enabled:true,credential_configured:true,sync_status:'SYNCED',gateway_check:{status:'HEALTHY'},validation:{status:'HEALTHY'}};
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:[...user.permissions,'personal-model:read']}}));
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[{id:'account',name:'我的账号',provider:'moonshot',status:'ACTIVE',default_model_id:selectedModel.id,models:[selectedModel]}]}}));
 await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:[{id:'moonshot',name:'Moonshot AI'}]}}));
 await page.route('**/v1/preflight*',route=>route.fulfill({json:{can_create:ready,checks:[]}}));
 await page.route('**/v1/instances',route=>{
  if(route.request().method()!=='POST')return route.fallback();
  expect(route.request().postDataJSON()).toEqual({name:'研发工作空间',model_configuration_id:selectedModel.id,model_configuration_version:1});expect(route.request().headers()['x-csrf-token']).toBe(user.csrf_token);keys.push(route.request().headers()['idempotency-key']);count++;
  bodies.push(route.request().postDataJSON());if(count===1){ready=false;return route.abort('connectionreset');}
  return route.fulfill({status:202,json:{instance_id:id,operation_id:operation,state:'CREATING'}});
 });
 await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 for(const [label,title] of [['创建时的模型提供商','Moonshot AI · 我的账号']]){await page.getByRole('combobox',{name:label,exact:true}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle(title,{exact:true}).click();}
 await page.getByRole('textbox',{name:'工作空间名称',exact:true}).fill('研'.repeat(33));
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await page.getByRole('textbox',{name:'工作空间名称',exact:true}).fill('work/invalid');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await page.getByRole('textbox',{name:'工作空间名称',exact:true}).fill('研发工作空间');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认创建结果',exact:true})).toBeEnabled();
 await page.reload();await page.getByRole('button',{name:'查看待确认创建',exact:true}).click();await page.getByRole('button',{name:'确认创建结果',exact:true}).click();
 await expect(page).toHaveURL(new RegExp('/app/instances/'+id+'$'));expect(keys).toHaveLength(2);expect(keys[1]).toBe(keys[0]);expect(bodies).toHaveLength(2);expect(bodies[0]).toMatchObject({name:'研发工作空间'});expect(bodies[1]).toEqual(bodies[0]);
 const stored=await page.evaluate(()=>JSON.stringify({...sessionStorage}));expect(stored).not.toContain(user.csrf_token);
});
test('stop tracks its operation, then deletion requires exact name and no body',async({page})=>{
 await session(page);const namedInstance={...instance,name:'研发工作空间_01'};let state: Omit<typeof instance,'active_operation_id'> & { active_operation_id: string|null }={...namedInstance};const received:string[]=[];
 await page.route('**/v1/preflight',route=>route.fulfill({json:{can_create:false,checks:[]}}));
 await page.route('**/v1/instances/'+id,route=>{
  if(route.request().method()==='DELETE'){
   expect(route.request().postData()).toBeNull();expect(route.request().headers()['x-confirm-instance-name']).toBe(encodeURIComponent(namedInstance.name));received.push('delete');
   return route.fulfill({status:202,json:{instance_id:id,operation_id:operation,state:'DELETING'}});
  }
  return route.fulfill({json:state});
 });
 await page.route('**/v1/instances/'+id+':stop',route=>{
  expect(route.request().postData()).toBeNull();received.push('stop');state={...namedInstance,active_operation_id:operation,observed_state:'STOPPING',allowed_actions:[]};
  return route.fulfill({status:202,json:{instance_id:id,operation_id:operation,state:'STOPPING'}});
 });
 await page.route('**/v1/operations/'+operation,route=>route.fulfill({json:{id:operation,instance_id:id,type:'STOP',status:'ACCEPTED',stage:'QUEUED',error:null,retry_allowed:false,updated_at:instance.updated_at}}));
 await page.goto('/app/instances/'+id);await page.getByRole('button',{name:'停止实例',exact:true}).click();await expect(page.getByRole('region',{name:'最近操作',exact:true})).toBeVisible();
 state={...namedInstance,observed_state:'STOPPED',desired_state:'STOPPED',allowed_actions:['delete','configure']};await page.getByRole('button',{name:'刷新',exact:true}).click();
 await page.getByRole('button',{name:'删除实例',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认删除',exact:true})).toBeDisabled();await page.getByRole('textbox',{name:'确认实例名称'}).fill('wrong-name');await expect(page.getByRole('button',{name:'确认删除',exact:true})).toBeDisabled();
 await page.getByRole('textbox',{name:'确认实例名称'}).fill(namedInstance.name);await page.getByRole('button',{name:'确认删除',exact:true}).click();await expect(page.getByRole('dialog')).not.toBeVisible();
 expect(received).toEqual(['stop','delete']);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
