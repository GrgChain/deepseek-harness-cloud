import {expect,test,type Page} from '@playwright/test';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {randomUUID,randomBytes} from 'node:crypto';
import type {Connector,ConnectorList} from '../src/api/connectors';

test('official development MCP services discover tools call documentation and start OAuth',async({page,browser,baseURL},info)=>{
 test.skip(process.env.HC_REAL_DEVELOPMENT_MCP!=='true','requires public development MCP endpoints');test.setTimeout(180_000);page.setDefaultTimeout(20_000);
 const adminContext=await browser.newContext({baseURL,ignoreHTTPSErrors:true});const admin=await adminContext.newPage();let account:{id:string}|undefined;let adminHeaders:Record<string,string>={};let memberHeaders:Record<string,string>={};
 const report:Record<string,unknown>={checked_at:new Date().toISOString(),credentials_supplied:false,services:{}};const services=report.services as Record<string,unknown>;
 async function login(target:Page,email:string,password:string){await target.goto('/auth/login?return_to=%2Fapp%2Fconnectors');await target.getByLabel('邮箱',{exact:true}).fill(email);await target.getByLabel('密码',{exact:true}).fill(password);await target.getByRole('button',{name:'登录',exact:true}).click();await expect(target).toHaveURL(/\/app\/connectors$/);return(await target.request.get('/v1/me')).json();}
 try{
  const a=await login(admin,'admin@harness.local',readFileSync('../.local/auth/admin-password','utf8').trim());adminHeaders={Origin:new URL(admin.url()).origin,'X-CSRF-Token':a.csrf_token};
  const owned:ConnectorList=await(await admin.request.get('/v1/connectors')).json();report.existing_admin_connections=owned.items.filter(c=>['github','context7','linear','sentry','vercel','supabase'].includes(c.catalog_id??'')).map(c=>({name:c.name,catalog_id:c.catalog_id,status:c.status,auth_type:c.auth_type}));
  const roles=await(await admin.request.get('/v1/roles?limit=100')).json();const role=roles.items.find((r:{code:string})=>r.code==='MEMBER');const suffix=randomUUID().slice(0,8),password=randomBytes(24).toString('base64url'),email='dev-mcp-'+suffix+'@harness.local';
  const created=await admin.request.post('/v1/users',{headers:{...adminHeaders,'Idempotency-Key':randomUUID()},data:{display_name:'开发MCP验证 '+suffix,email,identity_source:'local',role_id:role.id,initial_password:password}});expect(created.status()).toBe(201);account=await created.json();
  const me=await login(page,email,password);memberHeaders={Origin:new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
  const initial:ConnectorList=await(await page.request.get('/v1/connectors')).json();
  // Run every authorization preflight before asserting so one vendor cannot hide another's result.
  for(const preset of initial.catalog.entries.filter(e=>e.category==='development'&&e.auth_kind==='oauth2')){
   const saved=await page.request.post('/v1/connectors',{headers:memberHeaders,data:{name:'开发实网 '+preset.name,url:preset.default_url,catalog_id:preset.id,protocol_version:'2025-11-25',auth_type:'oauth2',enabled:true}});expect(saved.status()).toBe(201);const c:Connector=await saved.json();
   const started=await page.request.post(`/v1/connectors/${c.id}/oauth`,{headers:memberHeaders,data:{expected_version:c.version}});const payload=await started.json();
   if(!started.ok()){services[preset.id]={stage:'oauth_start',status:started.status(),error:payload};continue;}
   const authorization=new URL(payload.authorization_url);services[preset.id]={stage:'oauth_authorization_url',status:started.status(),authorization_origin:authorization.origin,authorization_path:authorization.pathname,redirect_uri:authorization.searchParams.get('redirect_uri'),pkce:authorization.searchParams.get('code_challenge_method'),resource:authorization.searchParams.get('resource'),scope:authorization.searchParams.get('scope'),account_consent:'not_completed'};
   expect(authorization.searchParams.get('code_challenge_method')).toBe('S256');expect(authorization.searchParams.get('redirect_uri')).toBe(new URL('/v1/connectors/oauth/callback',baseURL).href);
   const cancelled=new URL('/v1/connectors/oauth/callback',baseURL);cancelled.searchParams.set('state',authorization.searchParams.get('state')!);cancelled.searchParams.set('error','access_denied');await page.goto(cancelled.href);await expect(page.getByRole('status').filter({hasText:'已取消授权'})).toBeVisible();
  }
  await page.getByRole('button',{name:'添加连接器',exact:true}).click();await page.getByRole('button',{name:'开发工具',exact:true}).click();await page.getByRole('button',{name:'配置连接 Context7',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加连接器',exact:true});
  await editor.getByLabel('认证方式').press('ArrowDown');await page.getByRole('option',{name:'无需认证',exact:true}).click();await editor.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor).toHaveCount(0);
  const row=page.getByRole('article',{name:'连接器 Context7',exact:true});await row.getByRole('button',{name:/^验\s*证$/}).click();await expect(row.getByText('可用',{exact:true})).toBeVisible();
  let connection:Connector=(await(await page.request.get('/v1/connectors')).json()).items.find((c:Connector)=>c.catalog_id==='context7');services.context7={stage:'tools_discovered',status:connection.status,protocol:connection.protocol_version,auth:connection.auth_type,tools:connection.tools.map(t=>t.name)};
  const toolNames=['resolve-library-id','query-docs'];expect(connection.tools.map(t=>t.name)).toEqual(expect.arrayContaining(toolNames));
  const enabled=await page.request.post(`/v1/connectors/${connection.id}/tools`,{headers:memberHeaders,data:{expected_version:connection.version,enabled_tools:toolNames}});expect(enabled.status()).toBe(200);connection=await enabled.json();
  const results=[];
  for(const [tool,args] of [['resolve-library-id',{libraryName:'React',query:'How does React useState update a counter?'}],['query-docs',{libraryId:'/websites/react_dev',query:'React useState counter example',language:'TypeScript'}]] as const){
   const response=await page.request.post(`/v1/connectors/${connection.id}/invoke`,{headers:memberHeaders,data:{expected_version:connection.version,catalog_version:connection.catalog_version,tool,arguments:args,idempotency_key:randomUUID(),confirmed:true,risk_confirmed:false,timeout_seconds:60}});const body=await response.json();
   const text=body.result?.content?.filter((c:{type:string})=>c.type==='text').map((c:{text:string})=>c.text).join('\n')??'';results.push({tool,http_status:response.status(),status:body.status,reason:body.reason,is_error:body.result?.isError,response_characters:text.length,sample:text.slice(0,800)});
  }
  services.context7={...(services.context7 as object),stage:'tools_called',results};await page.reload();await page.getByRole('article',{name:'连接器 Context7',exact:true}).getByRole('button',{name:'工具与记录'}).click();await page.getByRole('tab',{name:'调用记录'}).click();await page.screenshot({path:info.outputPath('context7-real-documentation.png'),fullPage:true,animations:'disabled'});
  for(const result of results){expect(result.http_status).toBe(200);expect(result.status).toBe('SUCCEEDED');expect(result.is_error).not.toBe(true);expect(result.response_characters).toBeGreaterThan(100);}
  for(const preset of initial.catalog.entries.filter(e=>e.category==='development'&&e.auth_kind==='oauth2'))expect((services[preset.id] as {status:number}).status,JSON.stringify(services[preset.id])).toBe(200);
 }finally{
  if(memberHeaders['X-CSRF-Token']){const list=await page.request.get('/v1/connectors');if(list.ok())for(const c of(await list.json()).items)expect((await page.request.delete('/v1/connectors/'+c.id,{headers:memberHeaders,data:{expected_version:c.version,confirmed_name:c.name}})).status()).toBe(200);}
  if(account){const fresh=await admin.request.get('/v1/users/'+account.id);if(fresh.ok()){const current=await fresh.json();expect((await admin.request.delete('/v1/users/'+account.id,{headers:adminHeaders,data:{expected_version:current.version,confirmed_name:current.display_name}})).status()).toBe(204);}}
  mkdirSync('../.local/qa/development-mcp',{recursive:true});writeFileSync('../.local/qa/development-mcp/platform-results.json',JSON.stringify(report,null,2)+'\n');await adminContext.close();
 }
});
