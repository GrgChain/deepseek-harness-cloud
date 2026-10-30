import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';

test('shows the selected skills and extension categories on a live instance',async({page},info)=>{
 const id=process.env.HC_EXTENSION_DETAIL_INSTANCE_ID;
 test.skip(!id,'Requires an existing owner workspace');
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const response=page.waitForResponse(r=>new URL(r.url()).pathname===`/v1/workspace-extensions/${id}/skills`);
 const extensionsResponse=page.waitForResponse(r=>new URL(r.url()).pathname===`/v1/workspace-extensions/${id}`);
 await page.goto('/app/instances/'+id);
 await page.getByRole('button',{name:'扩展配置',exact:true}).click();
 const result=await response;
 expect(result.status()).toBe(200);
 const skills=await result.json();
 const extensions=await (await extensionsResponse).json();
 expect(skills.items.length).toBeGreaterThan(0);
 const panel=page.getByRole('region',{name:'扩展配置'});
 for(const item of skills.items)await expect(panel.getByText(item.name,{exact:true})).toBeVisible();
 for(const [kind,category] of [['connector','连接器'],['memory','记忆'],['knowledge','知识库'],['plugin','插件']])await expect(panel.getByRole('heading',{name:category,exact:true})).toHaveCount(extensions.items.some((item:{kind:string})=>item.kind===kind)?1:0);
 await page.getByRole('button',{name:'运行检查',exact:true}).click();
 await expect(page.locator('.instance-health-row')).toHaveCount(5);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('workspace-extensions.png'),fullPage:true,animations:'disabled'});
});
