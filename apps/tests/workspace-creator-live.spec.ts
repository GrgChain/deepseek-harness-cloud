import {test,expect} from '@playwright/test';
import {readFileSync,writeFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
test.use({locale:'zh-CN'});
test('managed Creator mode loads and can be selected',async({page,context,baseURL},info)=>{
 test.skip(process.env.HC_CREATOR_PROBE!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Explicit local Creator acceptance required');test.setTimeout(180000);
 await page.goto('/auth/login');await page.locator('input[name=login]').fill('admin@harness.local');await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/instances$/);
 const id=process.env.HC_ENTRY_INSTANCE_ID!;const me=await(await context.request.get('/v1/me')).json();const headers={'Origin':new URL(baseURL!).origin,'X-CSRF-Token':me.csrf_token,'Idempotency-Key':randomUUID()};
 const detail=async()=>{const r=await context.request.get('/v1/instances/'+id);expect(r.status()).toBe(200);return r.json();};
 const original=await detail();expect(original.owner_id).toBe(me.id);
 const action=process.env.HC_CREATOR_MAINTENANCE_ACTION;
 if(action==='stop'){
  expect(original.observed_state).toBe('RUNNING');const response=await context.request.post('/v1/instances/'+id+':stop',{headers});expect(response.status()).toBe(202);
  await expect.poll(async()=>(await detail()).observed_state,{timeout:120000}).toBe('STOPPED');return;
 }
 if(action==='validate'){
  const response=await context.request.post('/v1/my-models/'+original.model_configuration_id+':validate',{headers,data:{version:original.model_configuration_version},timeout:40000});
  expect(response.status(),await response.text()).toBe(200);const result=await response.json();
  console.log('Model health check',JSON.stringify({validation:result.validation?.status,gateway:result.gateway_check?.status,reason:result.validation?.reason_code??result.gateway_check?.reason_code}));
  expect(result.validation?.status).toBe('HEALTHY');return;
 }
 if(action==='resume'){
  expect(original.observed_state).toBe('STOPPED');
  const response=await context.request.post('/v1/instances/'+id+':configure',{headers,data:{expected_instance_version:original.version,model_configuration_id:original.model_configuration_id,model_configuration_version:original.model_configuration_version}});
  expect(response.status(),await response.text()).toBe(202);
  await expect.poll(async()=>(await detail()).active_operation_id,{timeout:90000}).toBeFalsy();
  const start=await context.request.post('/v1/instances/'+id+':start',{headers:{...headers,'Idempotency-Key':randomUUID()}});expect(start.status(),await start.text()).toBe(202);
  await expect.poll(async()=>(await detail()).can_enter,{timeout:120000}).toBe(true);return;
 }
 let rows:any[]=[];
 context.on('response',async response=>{if(new URL(response.url()).pathname==='/api/agentPresets/list')try{rows=(await response.json()).result.value.presets;}catch{}});
 await page.goto('/app/instances/'+id);const opened=page.waitForEvent('popup');await page.getByRole('button',{name:'进入工作空间',exact:true}).click();const workspace=await opened;
 try{
  await expect(workspace.locator('#root')).not.toBeEmpty({timeout:30000});
  const preview=workspace.getByRole('button',{name:/^(继续|Continue)$/});await expect(preview).toBeVisible({timeout:15000});await preview.click();
  await expect.poll(()=>rows.some(row=>row.id==='cordis')).toBe(true);expect(rows.find(row=>row.id==='cordis').broken).toBeUndefined();
  const chooseWorkspace=workspace.getByRole('button',{name:'选择工作区',exact:true});
  if(await chooseWorkspace.isVisible()){await chooseWorkspace.click();await workspace.getByRole('menuitem',{name:/workspace/}).click();}
  await expect(workspace.getByRole('button',{name:/^选择模型，当前 /})).toBeVisible({timeout:20000});
  await workspace.getByRole('button',{name:/^(标准模式|创造模式)$/}).click();
  await workspace.getByRole('menuitem',{name:/创造模式/}).click();
  await expect(workspace.getByRole('button',{name:'创造模式',exact:true})).toBeVisible();
  await expect(workspace.getByRole('button',{name:/^选择模型，当前 /})).toBeVisible();
  await workspace.screenshot({path:info.outputPath('creator-selected.png'),fullPage:true});
  writeFileSync(info.outputPath('verification.json'),JSON.stringify({preset:'cordis',broken:false,selected:true,inference:false},null,2));
 }finally{await workspace.close();}
});
