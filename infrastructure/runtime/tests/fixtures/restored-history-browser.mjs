// Explicit offline-recovery acceptance. Reads an existing synthetic task from
// a restored hard-capacity volume; never sends a prompt or edits that history.
import { chromium, expect } from '../../../../apps/node_modules/@playwright/test/index.mjs';
import { readPrivate } from '../../private-file.mjs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, writeFile, readFile, rm, chmod } from 'node:fs/promises';
import { join } from 'node:path';
import https from 'node:https';
import http from 'node:http';

if (process.env.HC_PRODUCT_STORAGE_RESTORE !== 'true') throw new Error('Explicit restored product history acceptance required');
const fixture = JSON.parse(await readPrivate(process.env.HC_RESTORED_HISTORY_FIXTURE_FILE));
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
if (![fixture.instance_id, fixture.asset_id, fixture.org_id, fixture.owner_id].every(id => uuid.test(id)) || !/^session:[A-Za-z0-9_-]{1,128}$/.test(fixture.selected_session) || !/^hc-browser-smoke-[a-f0-9-]{36}\.txt$/.test(fixture.file) || !/^sha256:[a-f0-9]{64}$/.test(fixture.image) || !/^sha256:[a-f0-9]{64}$/.test(fixture.network_image)) throw new Error('Controlled restored identities required');
const exec = promisify(execFile);
const docker = async (...args) => (await exec('docker', ['--host', 'unix:///var/run/docker.sock', ...args], { timeout: 45_000, maxBuffer: 1 << 20 })).stdout.trim();
const id = fixture.instance_id, name = 'hc-restored-history-' + id, network = 'hc-' + id;
const channel = join(fixture.runtime_root, id);
const auth = randomBytes(32).toString('base64url');
const evidence = { schema_version: 1, outcome: 'FAIL', stage: 'initialize', new_browser_model_submissions: 0 };
const status = process.env.HC_RESTORED_HISTORY_FIXTURE_FILE + '.result.json';
let browser, page, server, nativeCookie, ip, sockets = new Set(), networkCreated = false, channelCreated = false, containerCreated = false;
const networkFence = async action => {
 const data = JSON.stringify({ schema_version: 1, instance_id: id });
 const command = execFile('docker', ['run', '--rm', '-i', '--network', 'host', '--cap-drop', 'ALL', '--cap-add', 'NET_ADMIN', fixture.network_image, action], { timeout: 15_000, maxBuffer: 4096 });
 command.stdin.end(data);
 await new Promise((resolve, reject) => { command.on('exit', code => code === 0 ? resolve() : reject(new Error('Controlled recovery network fence failed'))); command.on('error', reject); });
};
const request = path => new Promise((resolve, reject) => {
 const req = http.request({ hostname: ip, port: 3080, path, headers: { Host: fixture.public_authority }, timeout: 5000 }, res => { res.resume(); res.on('end', () => resolve({ status: res.statusCode, headers: res.headers })); });
 req.on('timeout', () => req.destroy(new Error('Private native exchange timeout'))); req.on('error', reject); req.end();
});
try {
 await mkdir(channel, { mode: 0o700 });
 channelCreated = true;
 server = https.createServer({ cert: await readFile(fixture.tls_cert), key: await readFile(fixture.tls_key) });
 await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
 const port = server.address().port;
 if (port <= 10000) throw new Error('Recovery browser port must exceed 10000');
 const origin = 'https://127.0.0.1:' + port;
 fixture.public_authority = '127.0.0.1:' + port;
 const config = { schema_version: 1, instance_id: id, org_id: fixture.org_id, owner_id: fixture.owner_id, asset_id: fixture.asset_id, runtime_generation: randomUUID(), configuration_version: fixture.model_version, public_url: origin, endpoint: 'https://api.deepseek.com/anthropic', model: fixture.model };
 await writeFile(join(channel, 'config.json'), JSON.stringify(config), { mode: 0o600 });
 await writeFile(join(channel, 'model-key'), 'synthetic-restored-history-no-inference', { mode: 0o600 });
 await docker('network', 'create', '--driver', 'bridge', '--opt', 'com.docker.network.bridge.name=hci-' + id.replaceAll('-', '').slice(0, 11), '--opt', 'com.docker.network.bridge.enable_icc=false', '--label', 'hc.instance_id=' + id, network);
 networkCreated = true;
 await networkFence('install');
 containerCreated = true; // docker run may create a container before start fails.
 await docker('run', '-d', '--name', name, '--network', network, '--label', 'hc.instance_id=' + id, '--label', 'hc.asset_id=' + fixture.asset_id, '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--pids-limit', '256', '--memory', '4g', '--memory-swap', '4g', '--cpus', '2', '--stop-timeout', '30', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000', '--mount', 'type=volume,src=hc-' + fixture.asset_id + ',dst=/data,volume-nocopy', '--mount', 'type=bind,src=' + channel + ',dst=/run/harness', '--mount', 'type=bind,src=' + channel + '/model-key,dst=/run/secrets/model-key,readonly', fixture.image);
 evidence.stage = 'native-ready';
 let native;
 for (let attempt = 0; attempt < 160; attempt++) {
  try { native = JSON.parse(await readPrivate(join(channel, 'native.json'))); if (native.runtime_generation === config.runtime_generation) break; native = undefined; } catch {}
  if (await docker('inspect', '--format', '{{.State.Running}}', name) !== 'true') break;
  await new Promise(resolve => setTimeout(resolve, 250));
 }
 if (!native) throw new Error('Restored native runtime readiness missing');
 ip = await docker('inspect', '--format', '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}', name);
 expect((await request('/')).status).toBe(401);
 const exchange = await request('/?token=' + encodeURIComponent(native.token));
 expect(exchange.status).toBe(303);
 expect(exchange.headers['set-cookie']).toHaveLength(1);
 nativeCookie = exchange.headers['set-cookie'][0].split(';')[0];
 // This isolated fixture proxy binds one generated browser capability and
 // keeps native auth server-side. Production Gateway is tested separately.
 const authorized = req => req.headers.host === fixture.public_authority && req.headers.cookie?.split(';').some(value => value.trim() === '__Host-hc-recovery-fixture=' + auth);
 const upstreamHeaders = req => ({ ...req.headers, host: fixture.public_authority, cookie: nativeCookie });
 server.on('request', (req, res) => {
  if (!authorized(req) || req.url.includes('token=')) { res.writeHead(401, { 'Cache-Control': 'no-store' }); res.end(); return; }
  const up = http.request({ hostname: ip, port: 3080, path: req.url, method: req.method, headers: upstreamHeaders(req) }, response => {
   const headers = { ...response.headers }; delete headers['set-cookie'];
   res.writeHead(response.statusCode ?? 502, headers); response.pipe(res);
  });
  up.on('error', () => { res.destroy(); }); req.on('aborted', () => up.destroy()); req.pipe(up);
 });
 server.on('upgrade', (req, socket, head) => {
  if (!authorized(req) || req.headers.origin !== origin) { socket.destroy(); return; }
  const up = http.request({ hostname: ip, port: 3080, path: req.url, method: 'GET', headers: upstreamHeaders(req) });
  up.on('upgrade', (response, nativeSocket, buffered) => {
   sockets.add(socket); sockets.add(nativeSocket);
   let headers = 'HTTP/1.1 101 Switching Protocols\r\n';
   for (const [key, value] of Object.entries(response.headers)) if (key !== 'set-cookie') headers += key + ': ' + value + '\r\n';
   socket.write(headers + '\r\n'); if (buffered.length) socket.write(buffered); if (head.length) nativeSocket.write(head);
   socket.pipe(nativeSocket); nativeSocket.pipe(socket);
   socket.on('close', () => { nativeSocket.destroy(); sockets.delete(socket); }); nativeSocket.on('close', () => { socket.destroy(); sockets.delete(nativeSocket); });
   socket.on('error', () => nativeSocket.destroy()); nativeSocket.on('error', () => socket.destroy());
  });
  up.on('response', response => { response.resume(); socket.destroy(); }); up.on('error', () => socket.destroy()); up.end();
 });
 evidence.stage = 'restored-file-proof';
 const script = "const f=require('fs'),c=require('crypto');const b=f.readFileSync('/data/workspace/'+process.argv[1]);process.stdout.write(JSON.stringify({sha256:c.createHash('sha256').update(b).digest('hex'),bytes:b.length}));";
 const fileProof = JSON.parse(await docker('exec', name, 'node', '-e', script, fixture.file));
 evidence.file_proof = fileProof;
 expect(fileProof).toEqual(fixture.file_proof);
 evidence.stage = 'browser-launch';
 browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, args: ['--no-proxy-server'] });
 const context = await browser.newContext({ ignoreHTTPSErrors: true });
 await context.addCookies([{ name: '__Host-hc-recovery-fixture', value: auth, domain: '127.0.0.1', path: '/', secure: true, httpOnly: true, sameSite: 'Strict' }]);
 page = await context.newPage();
 const response = await page.goto(origin);
 evidence.page_http_status = response?.status();
 evidence.stage = 'browser-introduction';
 const introduction = page.getByRole('button', { name: '继续', exact: true });
 await expect(introduction.or(page.getByRole('button', { name: '设置', exact: true })).first()).toBeVisible({ timeout: 20_000 });
 if (await introduction.isVisible()) { await introduction.click(); await expect(introduction).not.toBeVisible(); }
 const readHistory = async () => {
  const row = page.locator('[role="treeitem"][data-row-key=' + JSON.stringify(fixture.selected_session) + ']').first();
  await expect(row).toBeVisible({ timeout: 30_000 }); await row.click(); await expect(row).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByText('HC_BROWSER_TASK_OK', { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(fixture.file, { exact: false }).first()).toBeVisible();
  if (!await page.locator('[data-tool]').first().isVisible()) await page.getByText(/已完成，用时/).first().click();
  const groups = page.locator('[data-step-process] button[aria-expanded="false"]');
  for (let count = 0; count < 10 && await groups.count(); count++) await groups.first().click();
  await expect(page.locator('[data-tool]').first()).toBeVisible();
  const tools = await page.locator('[data-tool]').evaluateAll(nodes => nodes.map(node => node.getAttribute('data-tool')));
  expect(tools).toEqual(expect.arrayContaining(['write', 'read']));
  return { same_session_id: true, completed_answer_visible: true, original_task_visible: true, tools };
 };
 evidence.stage = 'restored-native-history'; evidence.history = await readHistory();
 await page.reload(); evidence.after_refresh = await readHistory();
 expect(page.url()).toBe(origin + '/');
 evidence.file_proof = fileProof;
 evidence.runtime_image = fixture.image;
 evidence.restored_asset_id = fixture.asset_id;
 evidence.stage = 'complete'; evidence.outcome = 'PASS';
} catch {
 if (page) {
  evidence.visible_contract = await page.evaluate(() => ({ chinese_continue: [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '继续'), english_continue: [...document.querySelectorAll('button')].some(button => button.textContent.trim() === 'Continue'), session_rows: document.querySelectorAll('[role="treeitem"][data-row-key^="session:"]').length, tools: document.querySelectorAll('[data-tool]').length })).catch(() => ({}));
  await page.screenshot({ path: status + '.png' }).then(() => chmod(status + '.png', 0o600)).catch(() => {});
 }
 console.error('Restored history acceptance failed at stage: ' + evidence.stage);
 process.exitCode = 1;
} finally {
 await browser?.close().catch(() => { evidence.cleanup_failed = true; process.exitCode = 1; });
 for (const socket of sockets) socket.destroy();
 if (server) await new Promise(resolve => server.close(resolve));
 if (containerCreated) await docker('rm', '-f', name).catch(async () => {
  const remaining = await docker('ps', '-a', '--filter', 'name=^/' + name + '$', '--format', '{{.ID}}').catch(() => 'unknown');
  if (remaining) { evidence.cleanup_failed = true; process.exitCode = 1; }
 });
 if (networkCreated) {
  await networkFence('remove').catch(() => { evidence.cleanup_failed = true; process.exitCode = 1; });
  await docker('network', 'rm', network).catch(() => { evidence.cleanup_failed = true; process.exitCode = 1; });
 }
 if (channelCreated && !evidence.cleanup_failed) await rm(channel, { recursive: true, force: true });
 await writeFile(status, JSON.stringify(evidence), { mode: 0o600 });
}
