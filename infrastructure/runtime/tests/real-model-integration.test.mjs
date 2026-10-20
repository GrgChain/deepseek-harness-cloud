import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm, copyFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';
import { readPrivate } from '../private-file.mjs';
const exec = promisify(execFile);
const docker = async (...args) => (await exec('docker', args, { timeout: 45000, maxBuffer: 1024 * 1024 })).stdout.trim();
test('real DeepSeek through the pinned hardened DSH executes local file tools with a fixed request budget', { skip: !process.env.HC_REAL_MODEL_KEY_FILE || !process.env.HC_RUNTIME_IMAGE, timeout: 90000 }, async t => {
  const keyPath = resolve(process.env.HC_REAL_MODEL_KEY_FILE);
  await readPrivate(keyPath); // Reject non-private or symlinked input before mounting.
  const root = await mkdtemp(join(tmpdir(), 'hc-model-contract-'));
  const data = join(root, 'data'), run = join(root, 'run'), name = 'hc-model-contract-' + randomUUID();
  await mkdir(data, { mode: 0o700 }); await mkdir(run, { mode: 0o700 });
  t.after(async () => { await docker('rm', '-f', name).catch(() => {}); await rm(root, { recursive: true, force: true }); });
  const config = { schema_version: 1, instance_id: randomUUID(), org_id: randomUUID(), owner_id: randomUUID(), asset_id: randomUUID(), runtime_generation: randomUUID(), configuration_version: 1, public_url: 'https://d0.harness.test', endpoint: 'https://api.deepseek.com/anthropic', model: 'deepseek-flash' };
  for (const [path, value] of [[join(data, '.hc-initialized'), config], [join(run, 'config.json'), config]]) await writeFile(path, JSON.stringify(value), { mode: 0o600 });
  await copyFile(new URL('./fixtures/real-model-probe.mjs', import.meta.url), join(run, 'probe.mjs'));
  const launcher = `import {prepare} from '/opt/harness-runtime/start.mjs'; import {readFile,writeFile} from 'node:fs/promises'; import {spawn} from 'node:child_process'; import {networkInterfaces} from 'node:os';
const p=await prepare('/data','/run/harness','/run/harness/config.json','/run/secrets/model-key');
const patch=JSON.parse(await readFile(p.patchPath,'utf8')); const model=patch.find(row=>row.id==='llm-deepseek'); Object.assign(model.config,{thinking:'disabled',reasoningEffort:'off',maxTokens:512,streamIdleTimeoutMs:15000,retryPolicy:{mode:'normal',maxRetries:0}}); patch.push({insert:[{id:'hc-real-model-contract',name:'/run/harness/probe.mjs'}]}); await writeFile(p.patchPath,JSON.stringify(patch),{mode:0o600});
const address=Object.values(networkInterfaces()).flat().find(row=>row&&!row.internal&&row.family==='IPv4').address;
const child=spawn(process.execPath,['/opt/harness-runtime/node_modules/@deepseek-ai/dsh/lib/bin.js','web','--patch',p.patchPath,'--host',address,'--port','3080','--no-open','--public-url',p.config.public_url,'--trusted-host','d0.harness.test'],{cwd:p.cwd,env:p.environment,stdio:'inherit'}); delete p.environment.HC_DSH_MODEL_KEY; process.on('SIGTERM',()=>child.kill('SIGTERM')); child.on('exit',code=>{process.exitCode=code??1});`;
  await writeFile(join(run, 'launch.mjs'), launcher, { mode: 0o600 });
  await docker('run', '-d', '--name', name, '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--pids-limit', '256', '--memory', '4g', '--cpus', '2', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000', '--mount', `type=bind,src=${data},dst=/data`, '--mount', `type=bind,src=${run},dst=/run/harness`, '--mount', `type=bind,src=${keyPath},dst=/run/secrets/model-key,readonly`, '--entrypoint', '/usr/bin/tini', process.env.HC_RUNTIME_IMAGE, '--', 'node', '/run/harness/launch.mjs');
  let evidence;
  for (let attempt = 0; attempt < 240; attempt++) {
    try { evidence = JSON.parse(await readFile(join(run, 'model-evidence.json'), 'utf8')); break; } catch {}
    if (await docker('inspect', '-f', '{{.State.Running}}', name) !== 'true') break;
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  if (!evidence?.passed && process.env.HC_REAL_MODEL_DIAGNOSTICS_FILE) {
    const result = await exec('docker', ['logs', name], { timeout: 5000, maxBuffer: 1024 * 1024 });
    await writeFile(process.env.HC_REAL_MODEL_DIAGNOSTICS_FILE, result.stdout + result.stderr, { mode: 0o600, flag: 'wx' });
    await writeFile(process.env.HC_REAL_MODEL_DIAGNOSTICS_FILE + '.json', JSON.stringify(evidence ?? {}), { mode: 0o600, flag: 'wx' });
  }
  assert.equal(evidence?.passed, true, 'Real DSH model/tool contract failed; raw provider diagnostics remain private');
  assert.ok(evidence.requests >= 1 && evidence.requests <= 3);
  assert.equal(evidence.max_output_tokens, 512);
  assert.equal(await readFile(join(data, 'workspace/hc-real-probe.txt'), 'utf8'), 'hc-real-model-ok');
  const logs = await docker('logs', name);
  assert.equal(logs.includes((await readPrivate(keyPath)).trim()), false, 'Model credential leaked into runtime output');
});
