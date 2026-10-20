import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

test.describe('real workspace entry', () => {
 test.skip(process.env.HC_REAL_IDENTITY !== 'true' || !process.env.HC_ENTRY_INSTANCE_ID, 'Requires local identity and an existing owner workspace');
 for (const mode of ['direct', 'network', 'readiness']) {
  test(mode === 'network' ? 'recovers a failed connection and enters the real workspace' : mode === 'readiness' ? 'waits through an initial state conflict and enters the real workspace' : 'enters the real workspace directly', async ({ page, context, baseURL },info) => {
   await page.goto('/auth/login');
   await page.locator('input[name=login]').fill('admin@harness.local');
   await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password', 'utf8').trim());
   await page.getByRole('button', { name: '登录', exact: true }).click();
   await expect(page).toHaveURL(/\/app\/instances$/);
   const id = process.env.HC_ENTRY_INSTANCE_ID!;
   const portal = new URL(baseURL!);
   const origin = `https://i-${id.replaceAll('-', '')}.${portal.hostname.slice('portal.'.length)}:${portal.port}`;
   const entryResponses: string[] = [];
   context.on('response', response => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith('/__dsh/') || path === '/v1/me' || path.endsWith(':access')) entryResponses.push(`${response.status()} ${path}`);
   });
   let accessAttempts=0;
   if(mode==='readiness'){
    await page.route('**/v1/instances/'+id+':access',route=>{
     if(++accessAttempts===1)return route.fulfill({status:409,json:{code:'STATE_CONFLICT',message:'Synthetic startup health transition'}});
     return route.continue();
    });
   }
   if (mode==='network') {
    let failed = false;
    await context.route(origin + '/__dsh/challenge', route => {
     if (!failed) { failed = true; return route.abort(); }
     return route.continue();
    });
   }
   await page.goto('/app/instances/' + id);
   const popupPromise = page.waitForEvent('popup');
   const handoff = context.waitForEvent('response', { predicate: response => response.url() === origin + '/__dsh/handoff', timeout: 30_000 });
   const started=Date.now();
   await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
   const popup = await popupPromise;
   let response;
   try { response = await handoff; }
   catch (error) {
    console.log('Entry return state', new URL(popup.url()).pathname, popup.isClosed());
    console.log('Entry responses', entryResponses);
    console.log('Entry error', await page.locator('.ant-alert').allTextContents());
    throw error;
   }
   expect(response.status()).toBe(303);
   const authorized=Date.now();
   await expect(popup).toHaveURL(origin + '/');
   await expect(popup.locator('body')).not.toBeEmpty();
   await expect(popup.locator('#root')).not.toBeEmpty({ timeout: 30_000 });
   const timing={mode,authorization_ms:authorized-started,rendered_ms:Date.now()-started,navigation:await popup.evaluate(()=>performance.getEntriesByType('navigation').map(entry=>entry.toJSON()))};
   console.log('Entry timing',JSON.stringify(timing));
   await info.attach('entry-timing',{body:JSON.stringify(timing),contentType:'application/json'});
   expect(await popup.evaluate(() => window.opener === null)).toBe(true);
   if(mode==='readiness')expect(accessAttempts).toBe(2);
   await popup.close();
   await page.goto('/login');
   await page.getByRole('button', { name: '退出登录', exact: true }).click();
  });
 }
});
