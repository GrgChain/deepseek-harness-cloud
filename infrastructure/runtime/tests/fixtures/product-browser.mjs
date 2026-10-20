// Explicit local product acceptance: real OIDC, approved template, persisted
// model configuration, asynchronous Worker and browser-to-DSH Gateway.
import { chromium, expect } from '../../../../apps/node_modules/@playwright/test/index.mjs';
import { readPrivate } from '../../private-file.mjs';
import { writeFile } from 'node:fs/promises';

if (process.env.HC_PRODUCT_INTEGRATION !== 'true') throw new Error('Explicit local product acceptance required');
const portal = 'https://portal.harness.localhost:42013';
const previous = process.env.HC_PRODUCT_RESUME === 'true' ? JSON.parse(await readPrivate('.local/product-instance.json')) : null;
const evidence = { schema_version: 1, outcome: 'FAIL', stage: 'launch', manual_model_probe: { max_requests: 2, max_output_tokens: 128 }, checks: [] };
const record = () => writeFile('.local/product-validation-evidence.json', JSON.stringify(evidence, null, 2), { mode: 0o600 });
let browser;
async function login(role) {
 const context = await browser.newContext({ ignoreHTTPSErrors: true });
 const page = await context.newPage();
 await page.goto(portal + '/login');
 await page.getByRole('button', { name: '使用组织账户登录', exact: true }).click();
 await page.locator('input[name=login]').fill(role + '@harness.local');
 await page.locator('input[name=password]').fill((await readPrivate('.local/auth/' + role + '-password')).trim());
 await page.getByRole('button', { name: 'Login', exact: true }).click();
 await page.waitForURL(portal + '/app/instances');
 const me = await context.request.get(portal + '/v1/me');
 expect(me.status()).toBe(200);
 return { context, page, user: await me.json() };
}
try {
 browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, args: ['--no-proxy-server'] });
 evidence.stage = 'administrator-login';
 const admin = await login('admin');
 expect(admin.user.memberships[0].role).toBe('ADMIN');
 evidence.checks.push('real_oidc_administrator');
 await admin.page.goto(portal + '/app/settings');
 await expect(admin.page.getByText(/批准模板：hc-dsh-initial/)).toBeVisible();
 const currentResponse = await admin.context.request.get(portal + '/v1/model-configuration');
 expect(currentResponse.status()).toBe(200);
 const current = await currentResponse.json();
 expect(current.credential_configured).toBe(true);
 expect('api_key' in current).toBe(false);
 if (current.validation.status !== 'HEALTHY' || Date.parse(current.validation.expires_at ?? '') <= Date.now()) {
  if (process.env.HC_REAL_MODEL_VALIDATION !== 'true') throw new Error('Explicit paid model validation required');
  evidence.stage = 'bounded-model-validation';
  await admin.page.getByRole('button', { name: '校验模型连接', exact: true }).click();
  const validated = admin.page.waitForResponse(r => new URL(r.url()).pathname === '/v1/model-configuration:validate' && r.request().method() === 'POST', { timeout: 40_000 });
  await admin.page.getByRole('button', { name: '开始校验', exact: true }).click();
  const response = await validated;
  expect(response.status()).toBe(200);
  const model = await response.json();
  expect(model.validation.status).toBe('HEALTHY');
  evidence.model_configuration_id = model.id;
  evidence.model_configuration_version = model.version;
  evidence.manual_model_probe.executed = true;
 } else { evidence.manual_model_probe.executed = false; }
 evidence.checks.push('encrypted_model_and_bounded_validation');
 evidence.stage = 'administrator-defaults';
 const defaults = await admin.context.request.get(portal + '/v1/defaults');
 if (defaults.status() === 404) {
  const saved = admin.page.waitForResponse(r => new URL(r.url()).pathname === '/v1/defaults' && r.request().method() === 'PUT');
  await expect(admin.page.getByRole('button', { name: '将当前模型设为默认配置', exact: true })).toBeEnabled();
  await admin.page.getByRole('button', { name: '将当前模型设为默认配置', exact: true }).click();
  expect((await saved).status()).toBe(200);
 } else { expect(defaults.status()).toBe(200); }
 await expect.poll(async () => {
  const response = await admin.context.request.get(portal + '/v1/preflight');
  expect(response.status()).toBe(200);
  const result = await response.json();
  evidence.preflight_checks = result.checks.map(check => ({ name: check.name, status: check.health.status, reason_code: check.health.reason_code }));
  return result.can_create;
 }, { timeout: 150_000, intervals: [3000, 5000] }).toBe(!previous);
 await admin.page.getByRole('button', { name: '刷新检查结果', exact: true }).click();
 await expect(admin.page.getByText(previous ? '创建条件尚未满足' : '已满足创建条件', { exact: true })).toBeVisible({ timeout: 10_000 });
 evidence.checks.push('administrator_defaults_and_real_preflight');
 await admin.context.request.post(portal + '/auth/logout', { headers: { Origin: portal, 'X-CSRF-Token': admin.user.csrf_token } });
 await admin.context.close();
 evidence.stage = 'member-login';
 const member = await login('member');
 expect(member.user.memberships[0].role).toBe('MEMBER');
 evidence.checks.push('real_oidc_member');
 evidence.stage = 'one-click-create';
 const before = await member.context.request.get(portal + '/v1/instances');
 expect(before.status()).toBe(200);
 const items = (await before.json()).items;
 let result;
 if (previous?.instance_id && items.some(i => i.id === previous.instance_id)) {
  result = { instance_id: previous.instance_id, operation_id: previous.operation_id };
  evidence.resumed_existing_product_fixture = true;
  await member.page.goto(portal + '/app/instances/' + result.instance_id);
 } else {
  expect(items.every(item => item.observed_state === 'STOPPED')).toBe(true);
  evidence.existing_stopped_instances = items.length;
  await expect(member.page.getByRole('button', { name: '创建并运行', exact: true })).toBeEnabled({ timeout: 90_000 });
  const accepted = member.page.waitForResponse(r => new URL(r.url()).pathname === '/v1/instances' && r.request().method() === 'POST');
  await member.page.getByRole('button', { name: '创建并运行', exact: true }).click();
  const response = await accepted;
  expect(response.status()).toBe(202);
  result = await response.json();
 }
 evidence.instance_id = result.instance_id;
 evidence.operation_id = result.operation_id;
 await writeFile('.local/product-instance.json', JSON.stringify(result), { mode: 0o600 });
 await record();
 await expect(member.page).toHaveURL(portal + '/app/instances/' + result.instance_id);
 evidence.stage = 'worker-ready';
 await expect(member.page.getByRole('button', { name: '进入工作空间', exact: true })).toBeEnabled({ timeout: 120_000 });
 const detailsResponse = await member.context.request.get(portal + '/v1/instances/' + result.instance_id);
 expect(detailsResponse.status()).toBe(200);
 const details = await detailsResponse.json();
 expect(details.ready).toBe(true);
 expect(details.config_snapshot.spec.cpu_millicores).toBe(2000);
 expect(details.config_snapshot.spec.memory_bytes).toBe(4 * 2 ** 30);
 expect(details.storage.capacity_bytes).toBe(10 * 2 ** 30);
 evidence.checks.push('one_click_real_worker_create_fixed_spec_and_ready');
 const remaining = await member.context.request.get(portal + '/v1/preflight');
 expect(remaining.status()).toBe(200);
 const checks = await remaining.json();
 expect(checks.can_create).toBe(false);
 expect(checks.checks.some(c => c.name === 'quota' && c.health.reason_code === 'QUOTA_EXCEEDED')).toBe(true);
 evidence.checks.push('running_quota_reflected_in_preflight');
 evidence.stage = 'browser-gateway-entry';
 const popupEvent = member.page.waitForEvent('popup');
 await member.page.getByRole('button', { name: '进入工作空间', exact: true }).click();
 const popup = await popupEvent;
	 popup.on('requestfailed',request=>{const url=new URL(request.url());evidence.failed_request={origin:url.origin,path:url.pathname,error:request.failure()?.errorText};});
	 popup.on('response',response=>{if(response.request().isNavigationRequest()){const url=new URL(response.url());evidence.navigation={origin:url.origin,path:url.pathname,status:response.status(),location:response.headers().location?.split('?')[0]};}});
 let frames = 0;
 popup.on('websocket', socket => socket.on('framereceived', () => frames++));
 evidence.stage = 'handoff-root';
 await popup.waitForURL(url => url.hostname === 'i-' + result.instance_id.replaceAll('-', '') + '.harness.localhost' && url.pathname === '/', { timeout: 20_000 });
 evidence.stage = 'native-frames';
 await expect.poll(() => frames, { timeout: 15_000 }).toBeGreaterThan(0);
 expect(await popup.evaluate(() => window.opener === null)).toBe(true);
 expect(popup.url()).not.toContain('token');
 evidence.checks.push('real_browser_gateway_http_websocket_no_opener');
 await writeFile('.local/product-native-ui.json', JSON.stringify(await popup.evaluate(() => ({ text: document.body.innerText, assets: [...document.querySelectorAll('script[src]')].map(e => e.src), buttons: [...document.querySelectorAll('button')].map(e => ({ text: e.innerText, title: e.title, aria: e.getAttribute('aria-label') })) }))), { mode: 0o600 });
 evidence.outcome = 'PASS';
 evidence.stage = 'create-and-entry-complete';
 evidence.not_covered = ['browser_model_file_task', 'retry', 'backup_restore_reclamation', 'full_security_fault_performance_matrix'];
 await member.context.request.post(portal + '/auth/logout', { headers: { Origin: portal, 'X-CSRF-Token': member.user.csrf_token } });
 await member.context.close();
} catch (error) {
 await writeFile('.local/product-validation-diagnostic.txt', String(error?.stack ?? error), { mode: 0o600 });
 console.error('Local product acceptance failed at stage: ' + evidence.stage);
 process.exitCode = 1;
} finally { await record(); await browser?.close(); }
