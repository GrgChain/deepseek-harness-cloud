import {expect,test,type Page} from '@playwright/test';
import {readFileSync} from 'node:fs';
import {unzipSync} from 'fflate';
import {randomBytes,randomUUID} from 'node:crypto';

test('live public skill link previews imports checks updates and downloads unchanged content',async({page,browser,baseURL})=>{
 const source=process.env.HC_REAL_SKILL_SOURCE_URL;
 test.skip(process.env.HC_REAL_SKILLS!=='true'||!source,'requires an accessible public source and the local platform');test.setTimeout(90_000);
 const adminContext=await browser.newContext({baseURL,ignoreHTTPSErrors:true});const admin=await adminContext.newPage();
 let account:{id:string}|undefined;let adminHeaders:Record<string,string>={};let memberHeaders:Record<string,string>={};
 async function login(target:Page,email:string,password:string){await target.goto('/auth/login?return_to=%2Fapp%2Fskills');await target.getByLabel('邮箱',{exact:true}).fill(email);await target.getByLabel('密码',{exact:true}).fill(password);await target.getByRole('button',{name:'登录',exact:true}).click();await expect(target).toHaveURL(/\/app\/skills$/);return (await target.request.get('/v1/me')).json();}
 try{
  const a=await login(admin,'admin@harness.local',readFileSync('../.local/auth/admin-password','utf8').trim());adminHeaders={Origin:new URL(admin.url()).origin,'X-CSRF-Token':a.csrf_token};
  const roles=await(await admin.request.get('/v1/roles?limit=100')).json();const role=roles.items.find((r:{code:string})=>r.code==='MEMBER');expect(role).toBeTruthy();
  const suffix=randomUUID().slice(0,8),password=randomBytes(24).toString('base64url'),email='source-'+suffix+'@harness.local';
  const created=await admin.request.post('/v1/users',{headers:{...adminHeaders,'Idempotency-Key':randomUUID()},data:{display_name:'链接导入验证 '+suffix,email,identity_source:'local',role_id:role.id,initial_password:password}});expect(created.status()).toBe(201);account=await created.json();
  const me=await login(page,email,password);memberHeaders={Origin:new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
  await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'链接导入',exact:true}).click();await page.getByLabel('公开链接').fill(source!);
  const previewed=page.waitForResponse(r=>r.url().endsWith('/v1/skills:source')&&r.request().method()==='POST');await page.getByRole('button',{name:'获取并预览',exact:true}).click();const previewResponse=await previewed;expect(previewResponse.status()).toBe(200);const preview=await previewResponse.json();expect(preview.items.length).toBeGreaterThan(0);const name=preview.items[0].name;
  await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();expect((await(await page.request.get('/v1/skills')).json()).items).toHaveLength(0);
  await page.getByRole('button',{name:/确认导入$/}).click();const row=page.getByRole('article',{name:'Skill '+name,exact:true});await expect(row).toBeVisible();await expect(row.getByRole('switch')).toHaveCount(0);
  const list=await(await page.request.get('/v1/skills?limit=100')).json();expect(list.total).toBe(preview.items.length);const imported=list.items.find((item:{name:string})=>item.name===name);expect(imported).toBeTruthy();const saved=await(await page.request.get('/v1/skills/'+imported.id)).json();expect(saved.source_url).toBeTruthy();expect(saved.user_invocable).toBe(false);
  await row.getByRole('button',{name:name+' 更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'检查更新'}).click();await expect(page.getByText('此 Skill 已是最新内容',{exact:true})).toBeVisible({timeout:20_000});
  await row.getByRole('button',{name:'下载 '+name,exact:true}).click();const downloading=page.waitForEvent('download');await page.getByRole('button',{name:/^下\s*载$/}).click();const downloaded=await downloading;const bytes=readFileSync((await downloaded.path())!);if(downloaded.suggestedFilename().endsWith('.zip')){const files=unzipSync(bytes);expect(Object.keys(files)).toHaveLength(saved.file_count);expect(new TextDecoder().decode(files[name+'/SKILL.md'])).toBe(saved.content);}else expect(bytes.toString('utf8')).toBe(saved.content);
 }finally{
  if(memberHeaders['X-CSRF-Token']){const listing=await page.request.get('/v1/skills?limit=100');if(listing.ok())for(const skill of (await listing.json()).items){expect((await page.request.delete('/v1/skills/'+skill.id,{headers:memberHeaders,data:{expected_version:skill.version,confirmed_name:skill.name}})).status()).toBe(200);}}
  if(account){const fresh=await admin.request.get('/v1/users/'+account.id);if(fresh.ok()){const current=await fresh.json();expect((await admin.request.delete('/v1/users/'+account.id,{headers:adminHeaders,data:{expected_version:current.version,confirmed_name:current.display_name}})).status()).toBe(204);}}
  await adminContext.close();
 }
});
