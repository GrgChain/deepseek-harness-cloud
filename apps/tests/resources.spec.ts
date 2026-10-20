import { expect, test } from '@playwright/test';
const user = { id: '00000000-0000-4000-8000-000000000001', memberships: [{ org_id: '00000000-0000-4000-8000-000000000002', role: 'MEMBER', status: 'ACTIVE' , role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员' }], permissions: ["instances:read", "instances:create", "instances:start", "instances:stop", "instances:delete", "instances:configure", "instances:access", "instances:retry", "configuration:read", "audit:read"], auth_generation: 1, csrf_token: 'synthetic-csrf-token-32-characters-long', session_expires_at: '2099-01-01T00:00:00Z' };
const instanceID = '00000000-0000-4000-8000-000000000003';
const health = { status: 'UNKNOWN', checked_at: null, expires_at: null, reason_code: 'HEALTH_REQUIRED' };
const instance = { id: instanceID, owner_id: user.id, name: 'work-one', desired_state: 'STOPPED', observed_state: 'STOPPED', version: 1, ready: false, can_enter: false, allowed_actions: ['delete', 'configure'], active_operation_id: null, access_host: null, storage: null, config_snapshot: { model: 'deepseek-flash', model_configuration_version: 1, spec: { cpu_millicores: 2000, memory_bytes: 4 * 2 ** 30, storage_bytes: 10 * 2 ** 30 } }, health: { application: health, storage: health, configuration: health, access: health, model: health }, created_at: '2026-10-10T00:00:00Z', updated_at: '2026-10-10T00:00:00Z', retention_until: null };
async function session(page: import('@playwright/test').Page) {
 await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true, instances: false } } }));
 await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
 await page.route('**/v1/me', route => route.fulfill({ json: user }));
}
test('authenticated instance queries filter, show details and load owner operation logs', async ({ page }) => {
 await session(page);
 let state = '';
 await page.route('**/v1/instances?*', route => { state = new URL(route.request().url()).searchParams.get('state') ?? ''; return route.fulfill({ json: { items: [instance], next_cursor: null } }); });
 await page.route('**/v1/instances/' + instanceID, route => route.fulfill({ json: instance }));
 await page.route('**/v1/audit-events?*', route => route.fulfill({ json: { items: [{ id: 'operation-log', action: 'MODEL_SAVE', outcome: 'SUCCEEDED', trace_id: 'safe-trace', timestamp: '2026-10-10T00:00:00Z', actor_id: user.id, resource_id: null }], next_cursor: null } }));
 await page.goto('/app/instances');
 await expect(page.getByRole('button', { name: 'work-one', exact: true })).toBeVisible();
 await page.getByText('已停止', { exact: true }).first().click();
 await expect.poll(() => state).toBe('STOPPED');
 await page.getByRole('button', { name: '实例详情', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'work-one' })).toBeVisible();
 await page.getByRole('button', { name: '运行检查', exact: true }).click();
 await expect(page.locator('.instance-health-row').filter({ hasText: '存储' })).toContainText('待检查');
 await page.goto('/app/audit');
 await expect(page.getByRole('cell', { name: '保存模型配置', exact: true })).toBeVisible();
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
test('resource session expiry clears protected data and returns to login', async ({ page }) => {
 await session(page);
 await page.route('**/v1/instances?*', route => route.fulfill({ status: 401, json: { code: 'UNAUTHENTICATED' } }));
 await page.goto('/app/instances');
 await expect(page).toHaveURL(/\/login$/);
 await expect(page.getByRole('button', { name: '登录账户', exact: true })).toBeVisible();
});

test('workspace transport recovery automatically continues in the same isolated window', async ({ page, context, baseURL }) => {
 test.skip(!baseURL?.startsWith('https://portal.'), 'Requires the same-site HTTPS access configuration');
 await context.route('**/livez',route=>route.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await context.route('**/readyz',route=>route.fulfill({json:{status:'ready',process:'api'}}));
 await context.route('**/v1/me',route=>route.fulfill({json:user}));
 const portal = new URL(baseURL!);
 const host = `i-${instanceID.replaceAll('-', '')}.${portal.hostname.slice('portal.'.length)}`;
 const origin = `https://${host}${portal.port ? ':' + portal.port : ''}`;
 const running = { ...instance, desired_state: 'RUNNING', observed_state: 'RUNNING', ready: true, can_enter: true, allowed_actions: ['access'], access_host: host };
 await context.route('**/v1/instances/' + instanceID, route => route.fulfill({ json: running }));
 let challenges = 0;
 let authorizations = 0;
 const challengeID = '00000000-0000-4000-8000-000000000004';
 await context.route(origin + '/__dsh/challenge', route => {
  if (++challenges === 1) return route.abort();
  return route.fulfill({ headers: { 'Access-Control-Allow-Origin': portal.origin, 'Access-Control-Allow-Credentials': 'true' }, json: { challenge_id: challengeID, expires_at: new Date(Date.now() + 60_000).toISOString() } });
 });
 await context.route(origin + '/__dsh/connect', route => route.fulfill({ headers: { 'Content-Security-Policy': "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'", 'X-Frame-Options': 'DENY' }, contentType: 'text/html; charset=utf-8', body: `<meta http-equiv="refresh" content="0;url=${portal.origin}/app/instances/${instanceID}?resume_workspace=1"><h1>正在进入工作空间…</h1>` }));
 await context.route('**/v1/instances/' + instanceID + ':access', route => {
  authorizations++;
  expect(route.request().method()).toBe('POST');
  expect(route.request().postDataJSON()).toEqual({ challenge_id: challengeID });
  return route.fulfill({ json: { challenge_id: challengeID, handoff_url: origin + '/__dsh/handoff', ticket: 't'.repeat(43), expires_at: new Date(Date.now() + 40_000).toISOString() } });
 });
 await context.route(origin + '/__dsh/handoff', route => {
  expect(route.request().method()).toBe('POST');
  expect(new URLSearchParams(route.request().postData()!).get('challenge_id')).toBe(challengeID);
  return route.fulfill({ contentType: 'text/html; charset=utf-8', body: '<h1>合成工作空间</h1>' });
 });
 await page.goto('/app/instances/' + instanceID);
 const popupPromise = page.waitForEvent('popup');
 await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
 const confirmation = await popupPromise;
 await expect(confirmation.getByRole('heading', { name: '合成工作空间' })).toBeVisible();
 expect(new URL(confirmation.url()).search).toBe('');
 expect(await confirmation.evaluate(() => window.opener === null)).toBe(true);
 expect(challenges).toBe(2);expect(authorizations).toBe(1);
 await expect(page.getByText('进入工作空间失败', { exact: true })).toHaveCount(0);

});

test('blocked workspace popup reports the browser setting without issuing a challenge', async ({ page }) => {
 await session(page);
 await page.route('**/v1/instances/' + instanceID, route => route.fulfill({ json: { ...instance, can_enter: true, allowed_actions: ['access'] } }));
 let requests = 0;
 await page.route('**/__dsh/challenge', route => { requests++; return route.abort(); });
 await page.goto('/app/instances/' + instanceID);
 await page.evaluate(() => { window.open = () => null; });
 await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
 await expect(page.getByText('浏览器阻止了新窗口，请允许此网站弹出窗口后点击“进入工作空间”', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeEnabled();
 expect(requests).toBe(0);
});

test('model authentication failure identifies the applied version and provider account recovery', async ({ page }) => {
 await session(page);
 await page.route('**/v1/instances/' + instanceID, route => route.fulfill({ json: { ...instance, health: { ...instance.health, model: { ...health, status: 'UNHEALTHY', reason_code: 'MODEL_AUTH_REJECTED' } } } }));
 await page.goto('/app/instances/' + instanceID);
 await page.getByRole('button', { name: '实例信息', exact: true }).click();
 await expect(page.getByText('模型配置版本', { exact: true })).toBeVisible();
 await expect(page.getByText('v1', { exact: true })).toBeVisible();
 await expect(page.getByText('模型 API 密钥认证失败', { exact: true })).toBeVisible();
 await expect(page.getByText('请检查所用模型提供商账号的密钥，待模型校验通过后再更新实例配置。', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '打开模型提供商', exact: true })).toHaveCount(0);
});

test('connection return URL without a recent click does not authorize automatically',async({page,context})=>{
 await session(page);let challenges=0;let authorizations=0;
 await context.route('**/__dsh/challenge',r=>{challenges++;return r.abort();});
 await page.route('**/v1/instances/'+instanceID,route=>route.fulfill({json:{...instance,can_enter:true,allowed_actions:['access']}}));
 await page.route('**/v1/instances/'+instanceID+':access',route=>{authorizations++;return route.abort();});
 await page.goto('/app/instances/'+instanceID+'?resume_workspace=1');
 await expect(page.getByRole('heading',{name:instance.name})).toBeVisible();await expect(page).toHaveURL(new RegExp('/app/instances/'+instanceID+'$'));
 expect(challenges).toBe(0);expect(authorizations).toBe(0);
});

test('workspace entry presents an accessible transition and keeps failures actionable', async ({ page, context, baseURL }, testInfo) => {
 test.skip(!baseURL?.startsWith('https://portal.'), 'Requires same-site HTTPS');
 await session(page);
 const portal = new URL(baseURL!);
 const host = `i-${instanceID.replaceAll('-', '')}.${portal.hostname.slice('portal.'.length)}`;
 const origin = `https://${host}:${portal.port}`;
 const name = '我的工作空间 <img src=x onerror=alert(1)>';
 await page.route('**/v1/instances/' + instanceID, route => route.fulfill({json:{...instance,name,can_enter:true,allowed_actions:['access'],access_host:host}}));
 let release!: () => void;
 const ready = new Promise<void>(resolve => { release = resolve; });
 await context.route(origin + '/__dsh/challenge', async route => {
  await ready;
  return route.fulfill({headers:{'Access-Control-Allow-Origin':portal.origin,'Access-Control-Allow-Credentials':'true'},json:{challenge_id:'00000000-0000-4000-8000-000000000004',expires_at:new Date(Date.now()+60_000).toISOString()}});
 });
 await page.route('**/v1/instances/' + instanceID + ':access', route => route.fulfill({status:403,json:{code:'FORBIDDEN'}}));
 await page.goto('/app/instances/' + instanceID);
 const opened = page.waitForEvent('popup');
 await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const popup = await opened;
 try {
  await expect(popup.getByRole('heading',{name:'正在进入工作空间',exact:true})).toBeVisible();
  await expect(popup.getByText(name,{exact:true})).toBeVisible();
  await expect(popup.locator('img')).toHaveCount(0);
  expect(await popup.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await popup.emulateMedia({reducedMotion:'reduce'});
  expect(await popup.locator('.indicator').evaluate(element => getComputedStyle(element,'::after').animationName)).toBe('none');
  await popup.screenshot({path:testInfo.outputPath('workspace-entry.png'),fullPage:true,animations:'disabled'});
 } finally { release(); }
 await expect(popup.getByRole('heading',{name:'暂时无法进入',exact:true})).toBeVisible();
 await expect(popup.getByRole('alert')).toContainText('当前账户没有此操作权限');
 await expect(popup.getByRole('link',{name:'返回实例详情重试',exact:true})).toHaveAttribute('href',portal.origin+'/app/instances/'+instanceID);
 expect(popup.isClosed()).toBe(false);
});
