import {test,expect} from '@playwright/test';
import {readFileSync} from 'node:fs';

test.use({locale:'zh-CN',actionTimeout:10000,navigationTimeout:15000});
test('Agent preset settings load with the native settings service',async({page},info)=>{
 test.skip(process.env.HC_PRESET_SETTINGS_ACCEPTANCE!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Explicit local settings acceptance required');
 test.setTimeout(60000);
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 await page.goto('/app/instances/'+process.env.HC_ENTRY_INSTANCE_ID);
 await expect(page.getByRole('button',{name:'进入工作空间',exact:true})).toBeEnabled({timeout:15000});
 const opened=page.waitForEvent('popup',{timeout:15000});
 await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const workspace=await opened;
 const settingsReplies:unknown[]=[];
 workspace.on('response',async r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))try{const result=(await r.json()).result;settingsReplies.push({path,status:r.status(),ok:result?.ok,writable:result?.value?.writable,error:result?.error?.code});}catch{}});
 try{
  const preview=workspace.getByRole('button',{name:/^(继续|Continue)$/});
  await expect(preview).toBeVisible({timeout:20000});await preview.click();
  await workspace.getByRole('button',{name:'设置',exact:true}).click();
  await workspace.getByRole('button',{name:'Agent 预设',exact:true}).click();
  await expect(workspace.locator('[data-agent-preset-id="standard"]')).toContainText('标准模式',{timeout:20000});
  await expect(workspace.locator('[data-agent-preset-id="cordis"]')).toContainText('创造模式');
  await expect(workspace.getByRole('button',{name:/^新任务默认:/})).toBeVisible();
  await expect(workspace.getByText(/settings service is absent|settings are unavailable|加载失败/)).toHaveCount(0);
  const settings=await workspace.evaluate(async()=>{
   const method='settings/describe';
   const response=await fetch('/api/'+method,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({type:'client-request',rpcId:'preset-settings-acceptance',method,payload:{args:{}}})});
   const result=(await response.json()).result;
   return {status:response.status,ok:result?.ok,writable:result?.value?.writable};
  });
  expect(settings).toEqual({status:200,ok:true,writable:true});
  await workspace.screenshot({path:info.outputPath('agent-presets.png'),fullPage:true});
  await workspace.getByRole('button',{name:'模型',exact:true}).click();
  await expect(workspace.getByText('模型由控制台统一管理。',{exact:true})).toBeVisible();
  await expect(workspace.getByText('平台模型网关',{exact:true}).first()).toBeVisible();
 }catch(error){
  await workspace.screenshot({path:info.outputPath('settings-failure.png'),fullPage:true});
  await info.attach('settings-page',{body:await workspace.locator('body').innerText(),contentType:'text/plain'});
  await info.attach('settings-responses',{body:JSON.stringify(settingsReplies),contentType:'application/json'});throw error;
 }finally{await workspace.close();}
});
