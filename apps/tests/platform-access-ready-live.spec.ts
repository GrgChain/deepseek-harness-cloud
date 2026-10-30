import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';

test('platform access check is ready',async({page})=>{
 test.skip(process.env.HC_PLATFORM_ACCESS_READY!=='true','Requires a local platform');
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/platform-checks');
 await page.goto('/app/settings');
 const checks=await response;
 expect(checks.status()).toBe(200);
 const access=(await checks.json()).checks.find((item:{name:string})=>item.name==='access');
 expect(access.health.status).toBe('HEALTHY');
 const row=page.locator('.check-row').filter({hasText:'工作台入口'});
 await expect(row).toContainText('检查通过');
 await expect(row).toContainText('就绪');
});
