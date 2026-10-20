import { mkdir, unlink } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { networkInterfaces } from 'node:os';
import { validateConfiguration, profilePatch } from './contract.mjs';
import { readPrivate, writePrivate, privateDirectory } from './private-file.mjs';

export async function prepare(dataDirectory, runDirectory, configPath, keyPath) {
  await privateDirectory(runDirectory);
  const config = validateConfiguration(JSON.parse(await readPrivate(configPath)));
  const marker = JSON.parse(await readPrivate(join(dataDirectory, '.hc-initialized')));
  if (marker.schema_version !== 1 || ['asset_id', 'instance_id', 'org_id', 'owner_id'].some(key => marker[key] !== config[key])) throw new Error('Original storage identity is missing or mismatched');
  const key = (await readPrivate(keyPath)).trim();
  if (!/^[\x21-\x7e]{1,4096}$/.test(key)) throw new Error('Model key is invalid');
  await mkdir(join(dataDirectory, 'workspace'), { recursive: true, mode: 0o700 });
  await mkdir(join(dataDirectory, 'dsh'), { recursive: true, mode: 0o700 });
  const runtimeConfig = join(runDirectory, 'runtime.json');
  await writePrivate(runtimeConfig, config);
  await unlink(join(runDirectory, 'native.json')).catch(error => { if (error.code !== 'ENOENT') throw error; });
  const patchPath = join(runDirectory, 'profile.patch.json');
  await writePrivate(patchPath, profilePatch(config, fileURLToPath(new URL('./bootstrap.mjs', import.meta.url))));
  // Load the packaged native module directly. Its optional cache otherwise
  // copies executable code into /tmp, which is deliberately mounted noexec.
  const dshHome = join(dataDirectory, 'dsh');
  return { config, patchPath, cwd: join(dataDirectory, 'workspace'), environment: { ...process.env, NARB_DISABLE_NATIVE_CACHE: '1', DSH_HOME: dshHome, XDG_CACHE_HOME: join(dshHome, '.cache'), XDG_DATA_HOME: join(dshHome, '.local/share'), XDG_STATE_HOME: join(dshHome, '.local/state'), XDG_CONFIG_HOME: join(dshHome, '.config'), npm_config_cache: join(dshHome, '.npm'), HC_RUNTIME_CONFIG: runtimeConfig, HC_DSH_MODEL_KEY: key } };
}
export async function main() {
  const prepared = await prepare('/data', '/run/harness', '/run/harness/config.json', '/run/secrets/model-key');
  const addresses = Object.values(networkInterfaces()).flat().filter(address => address && !address.internal && address.family === 'IPv4');
  if (addresses.length !== 1) throw new Error('Runtime requires exactly one controlled IPv4 interface');
  const cli = fileURLToPath(new URL('./node_modules/@deepseek-ai/dsh/lib/bin.js', import.meta.url));
  const child = spawn(process.execPath, [cli, 'web', '--patch', prepared.patchPath, '--host', addresses[0].address, '--port', '3080', '--no-open', '--public-url', prepared.config.public_url, '--trusted-host', new URL(prepared.config.public_url).host], { cwd: prepared.cwd, env: prepared.environment, stdio: 'inherit' });
  delete prepared.environment.HC_DSH_MODEL_KEY;
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => child.kill(signal));
  child.on('exit', (code, signal) => { process.exitCode = code ?? (signal ? 1 : 0); });
  child.on('error', () => { console.error('harness-cloud: runtime launch failed'); process.exitCode = 1; });
}
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main().catch(() => { console.error('harness-cloud: runtime preparation failed'); process.exitCode = 1; });
