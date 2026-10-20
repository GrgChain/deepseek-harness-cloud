import { readFileSync } from 'node:fs';
import { expect,test } from '@playwright/test';

test('live member selects approved CPU memory and sees the model provider selector',async({page},info)=>{
 test.skip(process.env.HC_REAL_RESOURCE_SELECTION!=='true','requires deployed local OIDC and schema30; only reads are performed');
 test.setTimeout(60_000);
 const login=await page.goto('/auth/login');expect(login?.status()).toBe(200);
 await page.locator('input[name=login]').fill('member@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/member-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/instances$/);
 const identity=await page.request.get('/v1/me');expect(identity.status()).toBe(200);const user=await identity.json();
 try {
  await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
  const defaults=await (await page.request.get('/v1/defaults')).json();
  const catalog=await page.request.get('/v1/templates');expect(catalog.status()).toBe(200);
  const items=(await catalog.json()).items.filter((item:{id:string;template_version:string})=>item.id===defaults.template_id&&item.template_version===defaults.template_version);
  const selected=items.find((item:{spec:{cpu_millicores:number;memory_bytes:number}})=>item.spec.cpu_millicores===1000&&item.spec.memory_bytes===2*2**30);expect(selected).toBeTruthy();
  for(const [name,label] of [['创建时的 CPU','1 核'],['创建时的内存','2 GiB']]){
   await page.getByRole('combobox',{name,exact:true}).locator('..').locator('..').click();
   await page.locator('.ant-select-dropdown:visible').getByTitle(label,{exact:true}).click();
  }
  await expect(page.getByRole('combobox',{name:'创建时的模型提供商',exact:true})).toBeVisible();
  await expect(page.locator('.create-storage-value')).toHaveValue('10 GiB');
  const check=await page.request.get('/v1/preflight?spec_id='+selected.spec.id);expect(check.status()).toBe(200);const result=await check.json();
  expect(result.checks.find((check:{name:string})=>check.name==='spec').health.status).toBe('HEALTHY');
  expect(result.checks.find((check:{name:string})=>check.name==='quota')).toBeTruthy();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:info.outputPath('live-create-resources.png'),fullPage:true,animations:'disabled'});
  await page.getByRole('dialog',{name:'创建工作空间',exact:true}).getByRole('button',{name:'取消',exact:true}).click();
  await page.goto('/app/settings');
  await expect(page.getByRole('heading',{name:'默认运行配置',exact:true})).toBeVisible();
  await expect(page.getByText('10 GiB',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'配置模型连接',exact:true})).toHaveCount(0);
  await expect(page.getByRole('heading',{name:/模型/})).toHaveCount(0);
  await page.screenshot({path:info.outputPath('live-platform-runtime.png'),fullPage:true,animations:'disabled'});
 } finally {
  expect((await page.request.post('/auth/logout',{headers:{Origin:new URL(page.url()).origin,'X-CSRF-Token':user.csrf_token}})).status()).toBe(204);
 }
});
