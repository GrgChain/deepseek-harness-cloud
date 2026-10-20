import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

test.use({locale:'zh-CN',actionTimeout:5000});

test('new workspace directories start on the writable persistent volume', async ({page},testInfo)=>{
 test.setTimeout(60000);
 test.skip(process.env.HC_REAL_IDENTITY!=='true'||!process.env.HC_ENTRY_INSTANCE_ID,'Requires an existing local owner workspace');
 const id=process.env.HC_ENTRY_INSTANCE_ID!;
 if(!/^[a-f0-9-]{36}$/.test(id))throw new Error('Invalid test workspace');
 const name='hc-directory-probe-'+randomUUID();
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 await page.goto('/app/instances/'+id);
 const opened=page.waitForEvent('popup',{timeout:10000});
 await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const workspace=await opened;
 try{
  await expect(workspace.locator('#root')).not.toBeEmpty({timeout:30000});
  await expect(async()=>{
   const notice=workspace.getByRole('button',{name:/^(继续|Continue)$/});
   if(await notice.isVisible())await notice.click();
   if(!await workspace.getByRole('heading',{name:'选择工作区目录',exact:true}).isVisible()){
    await workspace.getByRole('button',{name:'添加工作区',exact:true}).click({timeout:1500});
   }
   await expect(workspace.getByRole('heading',{name:'选择工作区目录',exact:true})).toBeVisible({timeout:1500});
   await expect(workspace.getByRole('button',{name:'新建文件夹',exact:true})).toBeEnabled({timeout:1500});
   const editor=workspace.getByRole('textbox',{name:'编辑路径',exact:true});
   if(!await editor.isVisible())await workspace.getByRole('button',{name:'编辑路径',exact:true}).click({timeout:1500});
   await expect(editor).toHaveValue('/data/workspace/',{timeout:1500});
  }).toPass({timeout:20000,intervals:[500,1000]});
  for(const path of ['/home/node','/data/workspace/../dsh','/data/workspace-other']){
   const editor=workspace.getByRole('textbox',{name:'编辑路径',exact:true});
   await editor.fill(path);await editor.press('Enter');
   await expect(workspace.getByRole('alert')).toContainText('请在 workspace 目录内新建或选择工作区。');
  }
  await workspace.getByRole('textbox',{name:'编辑路径',exact:true}).fill('/data/workspace/');
  await workspace.getByRole('textbox',{name:'编辑路径',exact:true}).press('Enter');
  await expect(workspace.getByRole('textbox',{name:'编辑路径',exact:true})).toHaveCount(0);
  await expect(workspace.getByRole('navigation').getByRole('button',{name:'data',exact:true})).toHaveCount(0);
  await expect(workspace.getByRole('navigation').getByRole('button',{name:'/',exact:true})).toHaveCount(0);
  await workspace.getByRole('button',{name:'新建文件夹',exact:true}).click();
  await workspace.getByRole('textbox',{name:'文件夹名称',exact:true}).fill(name);
  await workspace.getByRole('button',{name:'创建',exact:true}).click();
  await expect(workspace.getByRole('textbox',{name:'文件夹名称',exact:true})).toHaveCount(0);
  await expect(workspace.getByText(name,{exact:true}).first()).toBeVisible();
  await expect(workspace.getByText(/EROFS|cannot create/)).toHaveCount(0);
  execFileSync('docker',['exec','hc-'+id,'node','-e','const f=require("fs");if(!f.statSync(process.argv[1]).isDirectory())process.exit(1)','/data/workspace/'+name]);
  await workspace.screenshot({path:testInfo.outputPath('directory-created.png'),fullPage:true,animations:'disabled'});
 }catch(error){
  await testInfo.attach('workspace-state',{body:await workspace.locator('body').innerText(),contentType:'text/plain'});
  throw error;
 }finally{
  // Only remove our unique empty probe; never recursively remove user data.
  execFileSync('docker',['exec','hc-'+id,'node','-e','try{require("fs").rmdirSync(process.argv[1])}catch(e){if(e.code!=="ENOENT")throw e}','/data/workspace/'+name]);
  await workspace.close();
 }
});
