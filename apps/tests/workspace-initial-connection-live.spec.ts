import {test,expect} from '@playwright/test';
import {readFileSync,writeFileSync} from 'node:fs';

test.use({locale:'zh-CN'});
test('initial connection and temporarily empty model catalog recover without manual actions',async({page,context},info)=>{
 test.skip(process.env.HC_INITIAL_CONNECTION_PROBE!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Requires a local owner workspace');
 test.setTimeout(90000);
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/instances$/);
 await page.goto('/app/instances/'+process.env.HC_ENTRY_INSTANCE_ID);
 await context.addInitScript(()=>{
  const labels:string[]=[];(window as any).__hcConnectionLabels=labels;
  const observe=()=>{new MutationObserver(()=>{for(const text of ['正在连接工作空间…','重新连接中','连接成功'])if(document.body?.innerText.includes(text)&&labels.at(-1)!==text)labels.push(text);}).observe(document.documentElement,{childList:true,subtree:true,characterData:true});};
  if(document.documentElement)observe();else document.addEventListener('DOMContentLoaded',observe,{once:true});
 });
 let firstSocketFailed=false;
 if(process.env.HC_FAIL_FIRST_NATIVE_SOCKET==='true')await context.routeWebSocket('**/api/remote.mux',async socket=>{
  if(!firstSocketFailed){firstSocketFailed=true;await socket.close({code:1013,reason:'Test temporary unavailability'});}else socket.connectToServer();
 });
 if(process.env.HC_DELAY_NATIVE_SOCKET==='true')await context.routeWebSocket('**/api/remote.mux',async socket=>{await new Promise(resolve=>setTimeout(resolve,1200));socket.connectToServer();});
 let injected=false,requests=0,modelName='',shellAdapted=false;
 context.on('response',async response=>{if(new URL(response.url()).pathname==='/plugins/')try{shellAdapted ||= (await response.text()).includes('connectingLabel: t(hasConnected.current ?');}catch{}});
 await context.route('**/api/session/modelCatalog',async route=>{
  requests++;if(process.env.HC_CATALOG_TRANSPORT_FAILURE==='true' && requests<=2){await route.fulfill({status:502,json:{code:'UPSTREAM_UNAVAILABLE',message:'Test transient transport failure'}});return;}const response=await route.fetch();const body=await response.json();const catalog=body.result?.value;
  if(!injected&&body.result?.ok&&catalog.groups.some((g:any)=>g.models.length>0)){
   modelName=catalog.groups.find((g:any)=>g.id===catalog.default.provider)?.models.find((m:any)=>m.id===catalog.default.model)?.name??'';
   injected=true;await route.fulfill({response,json:{...body,result:{...body.result,value:{...catalog,groups:[],routableProviders:[]}}}});
  }else await route.fulfill({response});
 });
 const opened=page.waitForEvent('popup');await page.getByRole('button',{name:'进入工作空间',exact:true}).click();const workspace=await opened;
 try{
  await expect(workspace.locator('#root')).not.toBeEmpty({timeout:20000});
  const notice=workspace.getByRole('button',{name:/^(继续|Continue)$/});if(await notice.isVisible())await notice.click();
  await expect.poll(()=>injected,{timeout:60000}).toBe(true);
  await expect.poll(()=>modelName).not.toBe('');
  await expect(workspace.getByRole('button',{name:new RegExp('^选择模型，当前 '+modelName)})).toBeVisible({timeout:60000});
  const labels=await workspace.evaluate(()=>(window as any).__hcConnectionLabels as string[]);
  if(process.env.HC_FAIL_FIRST_NATIVE_SOCKET==='true')expect(firstSocketFailed).toBe(true);
  expect(shellAdapted).toBe(true);expect(labels).not.toContain('重新连接中');expect(labels).not.toContain('连接成功');expect(requests).toBeGreaterThanOrEqual(2);
  await expect(async()=>{if(await notice.isVisible())await notice.click();await workspace.getByRole('button',{name:new RegExp('^选择模型，当前 '+modelName)}).click({timeout:1000});}).toPass({timeout:10000,intervals:[100,300]});await expect(workspace.getByRole('menu')).toContainText(modelName);
  await workspace.screenshot({path:info.outputPath('model-restored.png'),fullPage:true});
  writeFileSync(info.outputPath('verification.json'),JSON.stringify({labels,shell_adapted:shellAdapted,catalog_requests:requests,transport_failures_injected:process.env.HC_CATALOG_TRANSPORT_FAILURE==='true'?2:0,initial_socket_failure_injected:firstSocketFailed,empty_catalog_injected:injected,visible_model:modelName,manual_reconnect:false,inference:false},null,2));
 }finally{await context.unrouteAll({behavior:'wait'});await workspace.close();}
});
