import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';

test('reclaimed assets expose their removal action',async({page})=>{
 test.skip(process.env.HC_STORAGE_REMOVE_LIVE!=='true','Requires local owner credentials');
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/storage-assets');
 await page.goto('/app/storage');
 const result=await response;expect(result.status()).toBe(200);
 const items=(await result.json()).items;
 expect(items.some((item:{status:string})=>item.status==='DELETED')).toBe(true);
 for(const item of items){const row=page.getByRole('article',{name:`数据资产 ${item.workspace_name}`,exact:true}).filter({hasText:item.id});await expect(row.getByRole('button',{name:`删除${item.workspace_name}的数据资产`})).toHaveCount(item.status==='DELETED'?1:0);}
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
