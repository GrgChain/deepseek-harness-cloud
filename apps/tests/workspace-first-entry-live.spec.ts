import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';

test.use({locale:'zh-CN',actionTimeout:10000});
test('a freshly created workspace enters on the first click',async({page,context,baseURL},info)=>{
 test.skip(process.env.HC_FRESH_ENTRY!=='true','Explicit local creation and cleanup acceptance required');
 test.setTimeout(240000);
 const name='startup-probe-'+randomUUID().slice(0,8);
 const entryResponses:string[]=[];
 const catalogResults:unknown[]=[]; const sockets:unknown[]=[];
 context.on('response',async response=>{if(new URL(response.url()).pathname==='/api/session/modelCatalog')try{const r=(await response.json()).result;catalogResults.push({status:response.status(),ok:r?.ok,error_code:r?.error?.code,groups:r?.value?.groups?.length});}catch{catalogResults.push({status:response.status()});}});
 context.on('page',p=>p.on('websocket',ws=>{if(new URL(ws.url()).pathname==='/api/remote.mux'){const opened=Date.now();sockets.push({event:'open'});ws.on('close',()=>sockets.push({event:'close',after_ms:Date.now()-opened}));ws.on('socketerror',()=>sockets.push({event:'error'}));}}));
 context.on('response',response=>{
  const u=new URL(response.url());
  if(u.hostname.startsWith('i-'))entryResponses.push(`${response.status()} ${u.pathname}`);
 });
 let id='',popup:import('@playwright/test').Page|undefined;
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const me=await(await context.request.get('/v1/me')).json();
 const headers={'Origin':new URL(baseURL!).origin,'X-CSRF-Token':me.csrf_token};
 try{
  await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
  await page.getByRole('textbox',{name:'工作空间名称',exact:true}).fill(name);
  for(const label of ['创建时的 CPU','创建时的内存','创建时的存储容量']){
   const select=page.getByRole('combobox',{name:label,exact:true});
   await expect(select).toBeEnabled();await select.focus();await select.press('ArrowDown');await select.press('Home');await select.press('Enter');
  }
  const provider=page.getByRole('combobox',{name:'创建时的模型提供商',exact:true});
  await expect(provider).toBeEnabled();await provider.focus();await provider.press('ArrowDown');await provider.press('Home');await provider.press('Enter');
  try{await expect(async()=>{const retry=page.getByRole('button',{name:'重新检查',exact:true});if(await retry.isVisible())await retry.click();await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled({timeout:1000});}).toPass({timeout:60000,intervals:[2000,5000]});}
  catch(error){await info.attach('create-feedback',{body:await page.locator('.ant-drawer-body').innerText(),contentType:'text/plain'});throw error;}
  const accepted=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/instances'&&r.request().method()==='POST');
  await page.getByRole('button',{name:'创建并运行',exact:true}).click();
  const response=await accepted;expect(response.status()).toBe(202);
  id=(await response.json()).instance_id;
  expect(id).toMatch(/^[a-f0-9-]{36}$/);
  await page.goto('/app/instances/'+id);
  await expect(page.getByRole('button',{name:'进入工作空间',exact:true})).toBeEnabled({timeout:120000});
  let attempts=0;
  if(process.env.HC_ENTRY_INITIAL_CONFLICT==='true')await page.route('**/v1/instances/'+id+':access',route=>++attempts===1?route.fulfill({status:409,json:{code:'STATE_CONFLICT'}}):route.continue());
  const opened=page.waitForEvent('popup');
  const catalogResponse=context.waitForEvent('response',{predicate:r=>new URL(r.url()).pathname==='/api/session/modelCatalog',timeout:30000});void catalogResponse.catch(()=>{});
  const started=Date.now();
  await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
  popup=await opened;
  try{await expect(popup.locator('#root')).not.toBeEmpty({timeout:30000});}
  catch(error){
   const location=new URL(popup.url());
   console.log('First entry failed',location.origin+location.pathname,await popup.locator('body').innerText());
   console.log('Entry responses',entryResponses);
   throw error;
  }
  expect(await popup.evaluate(()=>window.opener===null)).toBe(true);
  await expect(popup.getByText('暂时无法进入',{exact:true})).toHaveCount(0);
  const catalog=(await (await catalogResponse).json()).result;
  expect(catalog.ok).toBe(true);
  expect(catalog.value.routableProviders).toContain(catalog.value.default.provider);
  expect(catalog.value.groups.find((g:{id:string})=>g.id===catalog.value.default.provider)?.models.some((m:{id:string})=>m.id===catalog.value.default.model)).toBe(true);
  const expectedModel=catalog.value.groups.find((g:any)=>g.id===catalog.value.default.provider).models.find((m:any)=>m.id===catalog.value.default.model).name;
  const notice=popup.getByRole('button',{name:/^(继续|Continue)$/});
  try { await expect(async()=>{if(await notice.isVisible())await notice.click();await expect(popup!.getByRole('button',{name:/^选择模型，当前 /})).toBeVisible();}).toPass({timeout:15000}); } catch(error) { await popup.screenshot({path:info.outputPath('first-model-failed.png'),fullPage:true});console.log('Fresh model failure',JSON.stringify({body:await popup.locator('body').innerText(),catalogResults,sockets,entryResponses}));throw error; }
  await popup.getByRole('button',{name:/^选择模型，当前 /}).click();
  await expect(popup.getByRole('menu')).toContainText(expectedModel);
  expect(catalog.value.groups.find((g:any)=>g.id===catalog.value.default.provider).name).toBe('平台模型网关');
  const rendered=Date.now()-started;
  await popup.screenshot({path:info.outputPath('first-model-menu.png'),fullPage:true});
  await popup.keyboard.press('Escape');
  await popup.waitForTimeout(20000);
  console.log('Fresh connection diagnostics',JSON.stringify({catalogResults,sockets}));
  expect(sockets.filter((s:any)=>s.event==='close')).toHaveLength(0);
  expect(catalogResults.every((r:any)=>r.ok===true)).toBe(true);
  console.log('Fresh first entry timing',JSON.stringify({rendered_ms:rendered,initial_conflict:process.env.HC_ENTRY_INITIAL_CONFLICT==='true'}));
  if(process.env.HC_ENTRY_INITIAL_CONFLICT==='true')expect(attempts).toBe(2);
  await popup.getByRole('button',{name:/^(设置|Settings)$/}).click();
  await popup.getByRole('button',{name:/^(模型|Models)$/}).click();
  await expect(popup.getByText('模型由控制台统一管理。',{exact:true})).toBeVisible();
  await expect(popup.getByText('平台模型网关',{exact:true}).first()).toBeVisible();
  await expect(popup.getByText(/加载提供商目录失败|settings are unavailable/)).toHaveCount(0);
  await popup.screenshot({path:info.outputPath('fresh-first-entry.png'),fullPage:true,animations:'disabled'});
  await info.attach('fresh-workspace',{body:JSON.stringify({instance_id:id,first_click:true,rendered_ms:rendered,initial_conflict:process.env.HC_ENTRY_INITIAL_CONFLICT==='true',paid_inference:false}),contentType:'application/json'});
 }finally{
  if(popup)await popup.close();
  if(id){
   // Only clean up this test's uniquely named workspace and empty data asset.
   const current=await(await context.request.get('/v1/instances/'+id)).json();
   expect(current.name).toBe(name);expect(current.owner_id).toBe(me.id);
   await expect.poll(async()=>{
    const value=await(await context.request.get('/v1/instances/'+id)).json();return value.allowed_actions?.includes('delete');
   },{timeout:120000}).toBe(true);
   const removed=await context.request.delete('/v1/instances/'+id,{headers:{...headers,'Idempotency-Key':randomUUID(),'X-Confirm-Instance-Name':encodeURIComponent(name)}});
   expect(removed.status()).toBe(202);
   const assets=await(await context.request.get('/v1/storage-assets?limit=20&page=1&q='+encodeURIComponent(name))).json();
   const asset=assets.items.find((a:{instance_id:string;workspace_name:string})=>a.instance_id===id&&a.workspace_name===name);
   expect(asset).toBeTruthy();
   await expect.poll(async()=>{
    const value=await(await context.request.get('/v1/storage-assets/'+asset.id)).json();return value.workspace_state;
   },{timeout:90000}).toBe('DELETED');
   const released=await context.request.post('/v1/storage-assets/'+asset.id+':release',{headers,data:{confirmed_name:name,confirmed_asset_id:asset.id}});
   expect(released.status()).toBe(202);
   await expect.poll(async()=>{
    const value=await(await context.request.get('/v1/storage-assets/'+asset.id)).json();return value.status;
   },{timeout:90000}).toBe('DELETED');
  }
 }
});
