import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';

test('opens a live data directory without cancellation alerts',async({page})=>{
 const id=process.env.HC_STORAGE_ENTRY_ASSET_ID;
 test.skip(!id,'Requires an existing owner data asset');
 test.setTimeout(90000);
 await page.addInitScript(()=>{const events:string[]=[];(window as typeof window&{storageAlerts:string[]}).storageAlerts=events;new MutationObserver(records=>{for(const record of records)for(const node of record.addedNodes){if(node instanceof Element){if(node.matches('[role=alert]'))events.push(node.textContent??'');node.querySelectorAll('[role=alert]').forEach(el=>events.push(el.textContent??''));}}}).observe(document,{subtree:true,childList:true});});
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const response=page.waitForResponse(r=>new URL(r.url()).pathname===`/v1/storage-assets/${id}/files`&&r.status()===200,{timeout:70000});
 await page.goto('/app/storage/'+id+'/browse');
 await response;
 await expect(page.locator('.storage-file-tree').getByRole('treeitem',{name:/\/data\/workspace$/})).toBeVisible();
 await expect(page.getByLabel('读取数据目录')).toHaveCount(0,{timeout:70000});
 await expect(page.locator('.storage-file-tree').getByRole('alert')).toHaveCount(0);
 const alerts=await page.evaluate(()=>(window as typeof window&{storageAlerts:string[]}).storageAlerts);
 expect(alerts.filter(text=>/cancel(?:led)?error|aborterror/i.test(text))).toEqual([]);
});
