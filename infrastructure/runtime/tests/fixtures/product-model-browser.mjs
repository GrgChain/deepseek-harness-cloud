// Explicit paid product acceptance. Uses the normal browser composer and the
// approved runtime, without adding plugins or changing the runtime profile.
import { chromium, expect } from '../../../../apps/node_modules/@playwright/test/index.mjs';
import { readPrivate } from '../../private-file.mjs';
import { writeFile } from 'node:fs/promises';
import { randomUUID, createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

if (process.env.HC_PRODUCT_INTEGRATION !== 'true' || process.env.HC_PRODUCT_MODEL_TASK !== 'true') throw new Error('Explicit paid local product acceptance required');
const exec = promisify(execFile);
const portal = 'https://portal.harness.localhost:42013';
const fixture = JSON.parse(await readPrivate('.local/product-instance.json'));
if (!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(fixture.instance_id)) throw new Error('Controlled product fixture required');
const file = 'hc-browser-smoke-' + randomUUID() + '.txt';
const contents = 'hc-browser-model-ok';
const evidence = { schema_version: 1, date: new Date().toISOString(), outcome: 'FAIL', stage: 'login', instance_id: fixture.instance_id, runtime_modified: false, browser_submissions: 0, file };
const record = () => writeFile('.local/product-model-evidence.json', JSON.stringify(evidence, null, 2), { mode: 0o600 });
let browser, context, page, popup;
try {
 browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, args: ['--no-proxy-server'] });
 context = await browser.newContext({ ignoreHTTPSErrors: true });
 page = await context.newPage();
 await page.goto(portal + '/auth/login');
 await page.locator('input[name=login]').fill('member@harness.local');
 await page.locator('input[name=password]').fill((await readPrivate('.local/auth/member-password')).trim());
 await page.getByRole('button', { name: 'Login', exact: true }).click();
 await page.waitForURL(portal + '/app/instances');
 await page.goto(portal + '/app/instances/' + fixture.instance_id);
 const initial = await (await context.request.get(portal + '/v1/instances/' + fixture.instance_id)).json();
 if (initial.observed_state === 'STOPPED') {
  evidence.stage = 'member-start';
  const accepted = page.waitForResponse(response => new URL(response.url()).pathname === '/v1/instances/' + fixture.instance_id + ':start' && response.request().method() === 'POST');
  await page.getByRole('button', { name: '启动实例', exact: true }).click();
  const response = await accepted;
  evidence.start_http_status = response.status();
  if (response.status() !== 202) evidence.start_error_code = (await response.json()).code;
  await record();
  expect(response.status()).toBe(202);
 }
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeEnabled({ timeout: 120_000 });
 const popupEvent = page.waitForEvent('popup');
 await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
 popup = await popupEvent;
 evidence.stage = 'native-onboarding';
 await popup.waitForURL(url => url.hostname === 'i-' + fixture.instance_id.replaceAll('-', '') + '.harness.localhost' && url.pathname === '/', { timeout: 20_000 });
 const introduction = popup.getByRole('button', { name: '继续', exact: true });
 const workspacePicker = popup.getByRole('button', { name: '选择工作区', exact: true });
 await expect(introduction).toBeVisible({ timeout: 20_000 });
 await introduction.click();
 await expect(introduction).not.toBeVisible();
 await expect(workspacePicker).toBeVisible({ timeout: 15_000 });
 evidence.stage = 'normal-runtime-profile';
 const profileScript = `const f=require('fs');const p=JSON.parse(f.readFileSync('/run/harness/profile.patch.json'));const c=p.find(x=>x.id==='llm-deepseek').config;process.stdout.write(JSON.stringify({default_max_tokens:c.maxTokens,model_max_tokens:c.models[0].maxTokens,retries:c.retryPolicy.maxRetries,plugins:p.filter(x=>x.insert).flatMap(x=>x.insert.map(i=>i.id))}));`;
 const profile = JSON.parse((await exec('docker', ['exec', 'hc-' + fixture.instance_id, 'node', '-e', profileScript], { timeout: 5000 })).stdout);
 expect(profile.default_max_tokens).toBe(4096);
 expect(profile.model_max_tokens).toBe(4096);
 expect(profile.retries).toBe(1);
 expect(profile.plugins).toEqual(['harness-cloud-bootstrap']);
 evidence.profile = profile;
 evidence.stage = 'workspace-session';
 await popup.getByRole('button', { name: '新建会话', exact: true }).last().click();
 const composer = popup.locator('[data-composer-input][contenteditable="true"]');
 await expect(composer).toBeVisible({ timeout: 15_000 });
 await expect(workspacePicker).toHaveText('workspace');
 const prompt = `仅执行这个简短的本地文件验收任务：在当前工作区新建文件 ${file}，内容精确为 ${contents}（无换行）；使用本地文件工具实际写入并读取确认。不要访问网络，不要改动其他文件。成功后最后单独回复 HC_BROWSER_TASK_OK。`;
 await composer.fill(prompt);
 evidence.stage = 'model-file-task';
 await popup.getByRole('button', { name: '发送消息', exact: true }).click();
 evidence.browser_submissions = 1;
 await record();
 await expect(popup.getByText('HC_BROWSER_TASK_OK', { exact: true })).toBeVisible({ timeout: 90_000 });
 evidence.stage = 'verify-generated-file';
 const verifyScript = `const f=require('fs'),c=require('crypto');const p='/data/workspace/'+process.argv[1];const x=f.readFileSync(p);process.stdout.write(JSON.stringify({sha256:c.createHash('sha256').update(x).digest('hex'),bytes:x.length}));`;
 const fileProof = JSON.parse((await exec('docker', ['exec', 'hc-' + fixture.instance_id, 'node', '-e', verifyScript, file], { timeout: 5000 })).stdout);
 expect(fileProof.sha256).toBe(createHash('sha256').update(contents).digest('hex'));
 expect(fileProof.bytes).toBe(Buffer.byteLength(contents));
 evidence.file_proof = fileProof;
 evidence.outcome = 'PASS';
 evidence.stage = 'model-file-task-complete';
} catch (error) {
 await writeFile('.local/product-model-diagnostic.txt', String(error?.stack ?? error), { mode: 0o600 });
 if (popup) await writeFile('.local/product-model-ui.json', JSON.stringify(await popup.evaluate(() => ({ text: document.body.innerText, buttons: [...document.querySelectorAll('button')].map(e => ({ text: e.innerText, aria: e.getAttribute('aria-label') })) })).catch(() => ({}))), { mode: 0o600 });
 console.error('Product browser model acceptance failed at stage: ' + evidence.stage);
 process.exitCode = 1;
} finally {
 // Stop only the explicit product fixture even when the paid task fails.
 if (page) {
  try {
   await page.goto(portal + '/app/instances/' + fixture.instance_id);
   const current = await (await context.request.get(portal + '/v1/instances/' + fixture.instance_id)).json();
   const stop = page.getByRole('button', { name: '停止实例', exact: true });
   if (current.observed_state !== 'STOPPED') {
    await expect(stop).toBeEnabled({ timeout: 10_000 });
    await stop.click();
   }
   await expect(page.getByRole('button', { name: '启动实例', exact: true })).toBeVisible({ timeout: 90_000 });
   const details = await (await context.request.get(portal + '/v1/instances/' + fixture.instance_id)).json();
   evidence.stopped = details.observed_state === 'STOPPED' && details.can_enter === false;
  } catch { evidence.stopped = false; process.exitCode = 1; }
 }
 if (context) {
  try {
   const me = await (await context.request.get(portal + '/v1/me')).json();
   await context.request.post(portal + '/auth/logout', { headers: { Origin: portal, 'X-CSRF-Token': me.csrf_token } });
  } catch {}
 }
 await record();
 await browser?.close();
}
