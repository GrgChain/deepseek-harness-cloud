import {expect,test} from '@playwright/test';
import {readFileSync,writeFileSync} from 'node:fs';

test.use({locale:'zh-CN'});
test('measures first model readiness without inference',async({page,context},info)=>{
 test.skip(process.env.HC_MODEL_READY_PROBE!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Requires an existing local owner workspace');
 test.setTimeout(90000);
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 await page.goto('/app/instances/'+process.env.HC_ENTRY_INSTANCE_ID);
 let catalogRequests=0,adapted=false;let modelCatalog:any;context.on('request',r=>{if(new URL(r.url()).pathname==='/api/session/modelCatalog'){catalogRequests++;if(process.env.HC_CAPTURE_MODEL_ONBOARDING==='true')console.log('Catalog wire',r.method(),r.postData());}});context.on('response',async r=>{if(new URL(r.url()).pathname==='/plugins/')try{const code=await r.text();if(code.includes('id: "@deepseek-ai/dsh-client-ui-model-selection"'))writeFileSync(info.outputPath('plugins.js'),code);if(code.includes('this.readManagedCatalog(generation)'))adapted=true;}catch{}});
 context.on('response',async r=>{if(new URL(r.url()).pathname==='/api/session/modelCatalog'){modelCatalog=(await r.json()).result;if(process.env.HC_CAPTURE_MODEL_ONBOARDING==='true')console.log('Catalog ready',modelCatalog?.ok,modelCatalog?.value?.groups?.length);}});
 const responses:string[]=[];context.on('response',r=>{const u=new URL(r.url());if(u.hostname.startsWith('i-'))responses.push(`${r.status()} ${u.pathname} ${r.headers()['content-encoding']??''}`);});context.on('requestfailed',r=>{const u=new URL(r.url());if(u.hostname.startsWith('i-'))responses.push(`failed ${u.pathname} ${r.failure()?.errorText}`);});
 const started=Date.now();const opened=page.waitForEvent('popup');
 await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const workspace=await opened;
 const initiators:unknown[]=[];const cdp=await context.newCDPSession(workspace);await cdp.send('Network.enable');cdp.on('Network.requestWillBeSent',event=>{if(new URL(event.request.url).pathname==='/api/session/modelCatalog')initiators.push(event.initiator);});
 try{await expect(workspace.locator('#root')).not.toBeEmpty({timeout:15000});}catch(error){console.log('Model entry failure',new URL(workspace.url()).pathname,await workspace.locator('body').innerText(),responses);throw error;}
 console.log('Model shell',Date.now()-started);
 if(process.env.HC_CAPTURE_MODEL_ONBOARDING==='true'){await workspace.screenshot({path:info.outputPath('initial-model-state.png'),fullPage:true});console.log('Initial model prompts',await workspace.locator('[role=dialog]').allTextContents());}
 await expect(async()=>{
  const notice=workspace.getByRole('button',{name:/^(继续|Continue)$/});if(await notice.isVisible())await notice.click();
  await expect(workspace.getByRole('button',{name:/^选择模型，当前 /})).toBeEnabled({timeout:1000});
 }).toPass({timeout:20000,intervals:[100,200,500]}).catch(async error=>{await workspace.screenshot({path:info.outputPath('model-failed.png'),fullPage:true});console.log('Model failure diagnostics',JSON.stringify({body:await workspace.locator('body').innerText(),responses,catalog:modelCatalog,sockets:workspace.url()}));throw error;});
 if(process.env.HC_EXPECT_MODEL_CATALOG_READY==='true'){
  await expect.poll(()=>modelCatalog?.ok).toBe(true);
  const catalog=modelCatalog.value;
  expect(catalog.routableProviders).toContain(catalog.default.provider);
  expect(catalog.groups.find((g:any)=>g.id===catalog.default.provider)?.models.some((m:any)=>m.id===catalog.default.model)).toBe(true);
 }
 const timing={model_ready_ms:Date.now()-started,catalog_requests:catalogRequests,adapted,resources:await workspace.evaluate(()=>performance.getEntriesByType('resource').map(e=>{const r=e as PerformanceResourceTiming;return {path:new URL(r.name).pathname,duration:r.duration,bytes:r.transferSize,encoded:r.encodedBodySize,decoded:r.decodedBodySize};}))};
 writeFileSync(info.outputPath('initiators.json'),JSON.stringify(initiators,null,2));
 console.log('Model requests',catalogRequests,'adapted',adapted);
 if(process.env.HC_EXPECT_OPTIMIZED_MODEL_INIT==='true'){expect(catalogRequests).toBe(1);expect(adapted).toBe(true);expect(timing.resources.some(r=>r.path==='/plugins/'&&r.encoded<r.decoded)).toBe(true);}
 console.log('Model ready',JSON.stringify(timing));
 writeFileSync(info.outputPath('timing.json'),JSON.stringify(timing,null,2));
 await workspace.screenshot({path:info.outputPath('model-ready.png'),fullPage:true});
 await workspace.close();
});
