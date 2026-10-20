import {test,expect} from '@playwright/test';
import {readFileSync} from 'node:fs';

test.use({locale:'zh-CN',actionTimeout:10000,navigationTimeout:15000});
test('managed workspace can toggle a bundled plugin and open third-party installation',async({page},info)=>{
 test.skip(process.env.HC_PLUGIN_MANAGER_ACCEPTANCE!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Explicit local plugin acceptance required');
 test.setTimeout(90000);
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 await page.goto('/app/instances/'+process.env.HC_ENTRY_INSTANCE_ID);
 await expect(page.getByRole('button',{name:'进入工作空间',exact:true})).toBeEnabled({timeout:30000});
 const opened=page.waitForEvent('popup');await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const workspace=await opened;
 const responses:unknown[]=[];
 workspace.on('response',async r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/plugin'))try{const result=(await r.json()).result;responses.push({path,status:r.status(),ok:result?.ok,management:result?.value?.managementAvailable});}catch{responses.push({path,status:r.status()});}});
 try{
  const preview=workspace.getByRole('button',{name:/^(继续|Continue)$/});
  await expect(preview).toBeVisible({timeout:20000});await preview.click();
  await workspace.getByRole('button',{name:'插件',exact:true}).click();
  await expect(workspace.getByRole('button',{name:'添加插件',exact:true})).toBeEnabled({timeout:20000});
  await expect(workspace.getByText(/本部署没有可管理的 profile/)).toHaveCount(0);
  // Restore the original selection even when a later assertion fails.
  const toggle=workspace.getByRole('switch',{name:'启用 会话搜索',exact:true});
  await expect(toggle).toBeEnabled();
  const original=await toggle.isChecked();
  try{
   await toggle.click();await expect(toggle).toBeChecked({checked:!original});
   await expect(toggle).toBeEnabled();
   await workspace.screenshot({path:info.outputPath('plugins-active.png'),fullPage:true});
  }finally{
   await expect(toggle).toBeEnabled();
   if(await toggle.isChecked()!==original)await toggle.click();
   await expect(toggle).toBeChecked({checked:original});await expect(toggle).toBeEnabled();
  }
  await workspace.getByRole('button',{name:'添加插件',exact:true}).click();
  await expect(workspace.getByRole('textbox',{name:'包名或地址',exact:true})).toBeVisible();
  await workspace.screenshot({path:info.outputPath('third-party-install.png'),fullPage:true});
 }catch(error){
  await workspace.screenshot({path:info.outputPath('plugin-failure.png'),fullPage:true});
  await info.attach('plugin-page',{body:await workspace.locator('body').innerText(),contentType:'text/plain'});
  await info.attach('plugin-responses',{body:JSON.stringify(responses),contentType:'application/json'});throw error;
 }finally{await workspace.close();}
});
