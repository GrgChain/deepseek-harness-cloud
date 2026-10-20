import {expect,test,type BrowserContext,type Page} from '@playwright/test';
import {readFile} from 'node:fs/promises';
import {createServer,type Server} from 'node:http';
import type {ProviderAccount} from '../src/api/provider-accounts';

// Opt-in local acceptance: real Portal/API/PostgreSQL/gateway, no paid inference.
test.skip(process.env.HC_REAL_PROVIDER_CRUD!=='true','requires the running local platform and approved 127.0.0.1:12345/v1 test root');
test.describe.configure({mode:'serial'});
test('provider CRUD, saved-key discovery and owner isolation on the running platform',async({page,browser,baseURL},info)=>{
 test.setTimeout(180_000);
 const base='http://127.0.0.1:12345/v1';
 const name='provider-qa-'+crypto.randomUUID().slice(0,8);
 let directoryReads=0,inferenceCalls=0,server:Server|undefined,other:BrowserContext|undefined,connection:ProviderAccount|undefined;
 const modelCalls=new Map<string,number>();
 const login=async(p:Page,account:string,passwordFile:string)=>{
  await p.goto('/auth/login');
  await expect(p.locator('input[name=login]')).toBeVisible({timeout:10_000});
  await p.locator('input[name=login]').fill(account);
  await p.locator('input[name=password]').fill((await readFile('../.local/auth/'+passwordFile,'utf8')).trim());
  await p.getByRole('button',{name:'登录',exact:true}).click();await p.waitForURL('**/app/instances');
  const response=await p.request.get('/v1/me');expect(response.status()).toBe(200);return response.json();
 };
 await login(page,'admin@harness.local','admin-password');
 const user=await (await page.request.get('/v1/me')).json();
 const headers={'X-CSRF-Token':user.csrf_token,Origin:baseURL!};
 const own=async()=>{const response=await page.request.get('/v1/my-provider-accounts');expect(response.status()).toBe(200);return (await response.json()).items as ProviderAccount[];};
 try{
  server=createServer((request,response)=>{
   if(request.method==='GET'&&request.url==='/v1/models'){directoryReads++;expect(request.headers.authorization).toBeUndefined();response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify({data:['qa-one','qa-two','qa-three'].map(id=>({id}))}));}
   else if(request.method==='POST'&&request.url==='/v1/chat/completions'){
    let body='';request.setEncoding('utf8');request.on('data',chunk=>{body+=chunk;});request.on('end',()=>{
     const input=JSON.parse(body);inferenceCalls++;modelCalls.set(input.model,(modelCalls.get(input.model)??0)+1);
     const second=input.messages.some((m:{role:string})=>m.role==='tool');
     const delta=second?{content:'ok'}:{tool_calls:[{index:0,id:'qa-call',type:'function',function:{name:input.tools[0].function.name,arguments:JSON.stringify({value:'ok'})}}]};
     response.writeHead(200,{'Content-Type':'text/event-stream'});
     response.end('data: '+JSON.stringify({id:'qa-response',model:input.model,choices:[{index:0,delta,finish_reason:second?'stop':'tool_calls'}]})+'\n\ndata: [DONE]\n\n');
    });
   }else{response.writeHead(405);response.end();}
  });
  await new Promise<void>((resolve,reject)=>{server!.once('error',reject);server!.listen(12345,'127.0.0.1',resolve);});
  await page.goto('/app/models');await page.getByRole('button',{name:'添加账号',exact:true}).click();
  await page.getByLabel('提供商',{exact:true}).locator('..').locator('..').click();
  await page.locator('.ant-select-dropdown:visible').getByTitle('自定义',{exact:true}).click();
  await page.getByLabel('名称',{exact:true}).fill(name);await page.getByLabel('服务地址',{exact:true}).fill(base);
  await page.getByRole('button',{name:'同步服务模型'}).click();await expect(page.getByRole('status')).toContainText('已同步 3 个模型');
  await page.getByRole('checkbox',{name:'qa-one',exact:true}).check();await page.getByRole('checkbox',{name:'qa-two',exact:true}).check();
  await page.screenshot({path:info.outputPath('live-provider-add.png'),animations:'disabled'});
  await page.getByRole('dialog').getByRole('button',{name:'添加账号',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);
  connection=(await own()).find(item=>item.name===name);expect(connection?.models).toHaveLength(2);
  // Wait for actual asynchronous route registration before editing its model set.
  await expect.poll(async()=>{connection=(await own()).find(item=>item.name===name);return connection?.models.every(m=>m.sync_status==='SYNCED');},{timeout:40_000}).toBe(true);
  // No validate request is sent by the browser: the durable Worker performs it.
  await expect.poll(async()=>{connection=(await own()).find(item=>item.name===name);return connection?.models.every(m=>m.validation?.status==='HEALTHY');},{timeout:60_000}).toBe(true);
  expect(inferenceCalls).toBe(4);
  other=await browser.newContext({baseURL,ignoreHTTPSErrors:true});const otherPage=await other.newPage();
  const member=await login(otherPage,'member@harness.local','member-password');
  const foreignHeaders={'X-CSRF-Token':member.csrf_token,Origin:baseURL!};
  expect((await (await otherPage.request.get('/v1/my-provider-accounts')).json()).items.some((item:ProviderAccount)=>item.id===connection!.id)).toBe(false);
  for(const suffix of [':sync',':delete'])expect((await otherPage.request.post('/v1/my-provider-accounts/'+connection!.id+suffix,{headers:foreignHeaders,data:{expected_version:connection!.version}})).status()).toBe(404);
  const row=page.getByRole('article',{name,exact:true});await row.getByRole('button',{name:/^编\s*辑$/}).click();
  await expect(page.getByLabel('API Key',{exact:true})).toHaveValue('');
  await page.getByRole('button',{name:'同步服务模型'}).click();await expect(page.getByRole('status')).toContainText('已同步 3 个模型');
  await page.getByRole('checkbox',{name:'qa-one',exact:true}).uncheck();await page.getByRole('checkbox',{name:'qa-three',exact:true}).check();
  await page.getByLabel('名称',{exact:true}).fill(name+'-edited');await page.getByRole('button',{name:'保存修改'}).click();await expect(page.getByRole('dialog')).toHaveCount(0);
  connection=(await own()).find(item=>item.id===connection!.id);expect(connection!.models.map(m=>m.model).sort()).toEqual(['qa-three','qa-two']);expect(connection!.models.every(m=>m.version===1)).toBe(true);
  await expect.poll(async()=>{connection=(await own()).find(item=>item.id===connection!.id);return connection?.models.every(m=>m.validation?.status==='HEALTHY');},{timeout:60_000}).toBe(true);
  await page.getByRole('button',{name:'展开 '+name+'-edited',exact:true}).click();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:info.outputPath('live-provider-list.png'),animations:'disabled'});
  await page.getByRole('button',{name:'删除 '+name+'-edited',exact:true}).click();
  await page.getByRole('dialog').getByRole('button',{name:'删除账号',exact:true}).click();await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect.poll(async()=>(await own()).some(item=>item.id===connection!.id),{timeout:40_000}).toBe(false);
  expect(directoryReads).toBe(2);expect(inferenceCalls).toBe(6);
  expect(Array.from(modelCalls.entries()).sort()).toEqual([['qa-one',2],['qa-three',2],['qa-two',2]]);
 }finally{
  // Clean up even when a UI assertion fails; never touch unrelated connections.
  const remaining=(await own()).filter(item=>item.name===name||item.name===name+'-edited');
  for(const item of remaining)if(item.status==='ACTIVE')await page.request.post('/v1/my-provider-accounts/'+item.id+':delete',{headers,data:{expected_version:item.version}});
  if(other)await other.close();
  if(server)await new Promise<void>(resolve=>server!.close(()=>resolve()));
  await page.request.post('/auth/logout',{headers});
 }
});
