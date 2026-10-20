import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import http from 'node:http';
import { prepare } from '../start.mjs';

test('pinned real DSH initializes defaults and exchanges native auth without leaking credentials', { skip: !process.env.HC_DSH_CONTRACT_CLI, timeout: 60000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'hc-dsh-contract-'));
  const data = join(root, 'data'), run = join(root, 'run');
  await mkdir(data, { mode: 0o700 }); await mkdir(run, { mode: 0o700 });
  const config = { schema_version: 1, instance_id: randomUUID(), org_id: randomUUID(), owner_id: randomUUID(), asset_id: randomUUID(), runtime_generation: randomUUID(), configuration_version: 1, public_url: 'https://d0.harness.test', endpoint: 'https://api.deepseek.com/anthropic', model: 'deepseek-flash' };
  const key = 'synthetic-contract-key-no-inference';
  await writeFile(join(data, '.hc-initialized'), JSON.stringify(config), { mode: 0o600 });
  await writeFile(join(run, 'config.json'), JSON.stringify(config), { mode: 0o600 });
  await writeFile(join(run, 'model-key'), key, { mode: 0o600 });
  const prepared = await prepare(data, run, join(run, 'config.json'), join(run, 'model-key'));
  const child = spawn(process.execPath, [process.env.HC_DSH_CONTRACT_CLI, 'web', '--patch', prepared.patchPath, '--host', '127.0.0.1', '--port', '13080', '--no-open', '--public-url', config.public_url, '--trusted-host', 'd0.harness.test'], { cwd: prepared.cwd, env: { ...prepared.environment, HTTP_PROXY: '', HTTPS_PROXY: '', ALL_PROXY: '', http_proxy: '', https_proxy: '', all_proxy: '' }, stdio: ['ignore', 'pipe', 'pipe'] });
  let logs = ''; child.stdout.on('data', bytes => { logs += bytes; }); child.stderr.on('data', bytes => { logs += bytes; });
  t.after(async () => {
    if (child.exitCode === null && child.signalCode === null) {
      await new Promise(resolve => { const timer = setTimeout(() => { child.kill('SIGKILL'); resolve(); }, 5000); child.once('exit', () => { clearTimeout(timer); resolve(); }); child.kill('SIGTERM'); });
    }
    await rm(root, { recursive: true, force: true });
  });
  let native;
  for (let attempt = 0; attempt < 160; attempt++) {
    try { native = JSON.parse(await readFile(join(run, 'native.json'), 'utf8')); break; } catch {}
    if (child.exitCode !== null) break;
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  assert.ok(native, 'Runtime did not publish its private readiness contract');
  assert.equal(native.workspace_path, prepared.cwd); assert.equal(native.model, config.model); assert.equal(native.configuration_version, 1);
  const request = (path, headers = {}) => new Promise((resolve, reject) => {
    const req = http.request({ hostname: '127.0.0.1', port: 13080, path, headers: { Host: 'd0.harness.test', ...headers } }, response => { response.resume(); response.on('end', () => resolve({ status: response.statusCode, headers: response.headers })); });
    req.on('error', reject); req.end();
  });
  assert.equal((await request('/')).status, 401);
  const exchanged = await request('/?token=' + encodeURIComponent(native.token));
  assert.equal(exchanged.status, 303); assert.equal(exchanged.headers.location, './');
  const cookie = exchanged.headers['set-cookie'][0].split(';')[0];
  assert.equal((await request('/', { Cookie: cookie })).status, 200);
  assert.equal((await request('/', { Host: 'wrong.harness.test', Cookie: cookie })).status, 401);
  assert.equal((await request('/api', { Cookie: cookie, Origin: 'https://wrong.harness.test' })).status, 403);
  assert.equal(logs.includes(native.token), false, 'Native token appeared in process output');
  assert.equal(logs.includes(key), false, 'Model key appeared in process output');
});
