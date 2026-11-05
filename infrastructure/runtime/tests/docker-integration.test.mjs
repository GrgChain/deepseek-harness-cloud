import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID, randomBytes } from 'node:crypto';
import http from 'node:http';

const exec = promisify(execFile);
const docker = async (...args) =>
  (await exec('docker', args, { timeout: 45000, maxBuffer: 1024 * 1024 })).stdout.trim();
test(
  'real hardened Docker runtime preserves files and fences HTTP and WebSocket authentication',
  { skip: !process.env.HC_RUNTIME_IMAGE, timeout: 150000 },
  async (t) => {
    const root = await mkdtemp(join(tmpdir(), 'hc-dsh-docker-'));
    const data = join(root, 'data'),
      run = join(root, 'run'),
      name = 'hc-contract-' + randomUUID();
    await mkdir(data, { mode: 0o700 });
    await mkdir(run, { mode: 0o700 });
    t.after(async () => {
      await docker('rm', '-f', name).catch(() => {});
      await rm(root, { recursive: true, force: true });
    });
    const config = {
      schema_version: 1,
      instance_id: randomUUID(),
      org_id: randomUUID(),
      owner_id: randomUUID(),
      asset_id: randomUUID(),
      runtime_generation: randomUUID(),
      configuration_version: 1,
      public_url: 'https://d0.harness.test',
      endpoint: 'https://api.deepseek.com/anthropic',
      model: 'deepseek-flash',
    };
    if (process.env.HC_RUNTIME_EXTERNAL_CONTRACT === 'true')
      Object.assign(config, {
        schema_version: 2,
        provider: 'platform-models',
        protocol: 'anthropic-messages',
        route_revision: 'test-v1',
        approval_digest: 'sha256:' + 'a'.repeat(64),
        context_window: 128000,
        max_output_tokens: 4096,
        response_models: ['actual-test-model'],
        approved_ips: ['10.50.0.10'],
        approved_networks: ['10.50.0.10/32'],
        endpoint: 'https://model.enterprise.example:14000',
        model: 'enterprise-chat-v1',
      });
    const key = 'synthetic-contract-key-no-inference';
    const sessionMemory = process.env.HC_RUNTIME_SESSION_MEMORY === 'true';
    const memoryBackend = process.env.HC_RUNTIME_MEMORY_BACKEND ?? 'openviking';
    const localMemory = memoryBackend !== 'openviking';
    const memoryRuntime = {
      mem0: {
        package: '@harness-cloud/dsh-mem0-memory',
        version: '0.2.0',
        archive: '../../plugins/mem0-dist/harness-cloud-dsh-mem0-memory-0.2.0.tgz',
        skills: ['mem0-memory'],
      },
      graphiti: {
        package: '@harness-cloud/dsh-graphiti-memory',
        version: '0.2.0',
        archive: '../../plugins/graphiti-dist/harness-cloud-dsh-graphiti-memory-0.2.0.tgz',
        skills: ['graphiti-memory'],
      },
      openviking: {
        package: '@openviking/dsh-memory-plugin',
        version: '0.5.31',
        archive: '../../plugins/openviking/openviking-dsh-memory-plugin-0.5.31.tgz',
        skills: ['openviking-memory', 'openviking-skills', 'ov-experience-memory'],
      },
    }[memoryBackend];
    for (const [path, value] of [
      [join(data, '.hc-initialized'), JSON.stringify({ ...config, schema_version: 1 })],
      [join(run, 'config.json'), JSON.stringify(config)],
      [join(run, 'model-key'), key],
    ])
      await writeFile(path, value, { mode: 0o600 });
    if (sessionMemory) {
      const installer = await readFile(
        new URL('../workspace-assets/install_extensions.mjs', import.meta.url),
        'utf8',
      );
      const input = {
        ...config,
        extensions: {
          items: [
            {
              kind: 'memory',
              backend: memoryBackend,
              id: randomUUID(),
              name: 'Native memory',
              version: 1,
              scopes: ['personal'],
              session_memory: true,
              session_scope: 'personal',
            },
          ],
        },
        program: await readFile(
          new URL('../workspace-assets/cloud_extensions.mjs', import.meta.url),
          'utf8',
        ),
        session_program: await readFile(
          new URL('../workspace-assets/openviking_session.mjs', import.meta.url),
          'utf8',
        ),
        session_plugin: (await readFile(new URL(memoryRuntime.archive, import.meta.url))).toString(
          'base64',
        ),
      };
      await new Promise((resolve, reject) => {
        const child = execFile(
          'docker',
          [
            'run',
            '--rm',
            '-i',
            '--network',
            'none',
            '--read-only',
            '--cap-drop',
            'ALL',
            '--mount',
            `type=bind,src=${data},dst=/data`,
            '--entrypoint',
            'node',
            process.env.HC_RUNTIME_IMAGE,
            '--input-type=module',
            '-e',
            installer,
            '--',
            '--install',
          ],
          { timeout: 15000 },
          (error) => (error ? reject(error) : resolve()),
        );
        child.stdin.end(JSON.stringify(input));
      });
      await writeFile(join(run, 'runtime.json'), JSON.stringify(config), { mode: 0o600 });
      let busy = false;
      const responder = setInterval(async () => {
        if (busy) return;
        busy = true;
        try {
          const request = JSON.parse(
            await readFile(join(run, '.hc-extension-request.json'), 'utf8'),
          );
          await rm(join(run, '.hc-extension-request.json'));
          if (localMemory) {
            assert.ok(['session', 'search'].includes(request.action));
            const result =
              request.action === 'search'
                ? { items: [] }
                : request.session_request.path === '/sessions/commit'
                  ? { committed: false, memories: 0, pending_messages: 0 }
                  : { accepted: JSON.parse(request.session_request.body).messages.length };
            await writeFile(
              join(run, '.hc-extension-response.json'),
              JSON.stringify({ id: request.id, result }),
              { mode: 0o600 },
            );
            return;
          }
          assert.equal(request.action, 'session');
          const native = request.session_request;
          const payload = native.body ? JSON.parse(native.body) : {};
          let result = {};
          if (native.path === '/mcp')
            result =
              payload.method === 'initialize'
                ? {
                    jsonrpc: '2.0',
                    id: payload.id,
                    result: {
                      protocolVersion: '2025-06-18',
                      capabilities: { tools: {} },
                      serverInfo: { name: 'fixture', version: '1' },
                    },
                  }
                : payload.method === 'tools/list'
                  ? {
                      jsonrpc: '2.0',
                      id: payload.id,
                      result: {
                        tools: [
                          {
                            name: 'find',
                            description: 'Find fixture memories',
                            inputSchema: {
                              type: 'object',
                              properties: { query: { type: 'string' } },
                              required: ['query'],
                            },
                          },
                          {
                            name: 'remember',
                            description: 'Remember fixture content',
                            inputSchema: {
                              type: 'object',
                              properties: { content: { type: 'string' } },
                              required: ['content'],
                            },
                          },
                        ],
                      },
                    }
                  : { jsonrpc: '2.0', id: payload.id, result: {} };
          else if (native.path.includes('/fs/')) result = [];
          else if (native.path.includes('/system/status')) result = { user: 'owner' };
          else if (native.path.includes('/skills')) result = { skills: [] };
          await writeFile(
            join(run, '.hc-extension-response.json'),
            JSON.stringify({
              id: request.id,
              result: {
                status: 200,
                headers: { 'content-type': 'application/json' },
                body: JSON.stringify(native.path === '/mcp' ? result : { status: 'ok', result }),
              },
            }),
            { mode: 0o600 },
          );
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        } finally {
          busy = false;
        }
      }, 10);
      t.after(() => clearInterval(responder));
    }
    await docker(
      'run',
      '-d',
      '--name',
      name,
      '--read-only',
      '--cap-drop',
      'ALL',
      '--security-opt',
      'no-new-privileges',
      '--pids-limit',
      '256',
      '--memory',
      '4g',
      '--cpus',
      '2',
      '--tmpfs',
      '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000',
      '--mount',
      `type=bind,src=${data},dst=/data`,
      '--mount',
      `type=bind,src=${run},dst=/run/harness`,
      '--mount',
      `type=bind,src=${run}/model-key,dst=/run/secrets/model-key,readonly`,
      process.env.HC_RUNTIME_IMAGE,
    );
    const ready = async () => {
      for (let attempt = 0; attempt < 160; attempt++) {
        try {
          const value = JSON.parse(await readFile(join(run, 'native.json'), 'utf8'));
          if (value.runtime_generation === config.runtime_generation) return value;
        } catch {}
        if ((await docker('inspect', '-f', '{{.State.Running}}', name)) !== 'true') break;
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
      assert.fail('Runtime did not publish its private readiness contract');
    };
    const native = await ready();
    assert.equal(
      await docker('exec', name, 'node', '-e', "process.stdout.write(require('os').homedir())"),
      '/data/dsh',
    );
    await docker(
      'exec',
      name,
      'node',
      '-e',
      "const fs=require('fs');fs.mkdirSync('/data/dsh/.local/bin',{recursive:true});fs.writeFileSync('/data/dsh/.local/bin/hc-install-proof','#!/bin/sh\\necho persistent-user-tool\\n',{mode:0o755})",
    );
    assert.equal(
      await docker('exec', name, 'bash', '-c', 'hc-install-proof'),
      'persistent-user-tool',
      'Tools installed in the persistent user home must be callable without manual PATH changes',
    );
    assert.equal(native.provider, config.provider ?? 'deepseek-official');
    assert.equal(native.workspace_path, '/data/workspace');
    assert.equal(native.model, config.model);
    assert.equal((await stat(join(run, 'native.json'))).mode & 0o777, 0o600);
    const ip = await docker(
      'inspect',
      '-f',
      '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',
      name,
    );
    const request = (path, headers = {}, upgrade = false) =>
      new Promise((resolve, reject) => {
        const req = http.request(
          {
            hostname: ip,
            port: 3080,
            path,
            headers: { Host: 'd0.harness.test', ...headers },
            timeout: 5000,
          },
          (response) => {
            response.resume();
            response.on('end', () =>
              resolve({ status: response.statusCode, headers: response.headers }),
            );
          },
        );
        if (upgrade)
          req.on('upgrade', (response, socket) => {
            socket.destroy();
            resolve({ status: response.statusCode, headers: response.headers });
          });
        req.on('timeout', () =>
          req.destroy(new Error('Runtime request timed out: ' + path.split('?')[0])),
        );
        req.on('error', reject);
        req.end();
      });
    assert.equal((await request('/')).status, 401);
    const exchanged = await request('/?token=' + encodeURIComponent(native.token));
    assert.equal(exchanged.status, 303);
    assert.equal(exchanged.headers.location, './');
    let cookie = exchanged.headers['set-cookie'][0].split(';')[0];
    assert.equal((await request('/', { Cookie: cookie })).status, 200);
    const rpc = (method, args = {}) =>
      new Promise((resolve, reject) => {
        const body = JSON.stringify({
          type: 'client-request',
          rpcId: 'creator-readiness',
          method,
          payload: { args },
        });
        const req = http.request(
          {
            hostname: ip,
            port: 3080,
            path: '/api/' + method,
            method: 'POST',
            headers: {
              Host: 'd0.harness.test',
              Cookie: cookie,
              Origin: config.public_url,
              'Content-Type': 'application/json',
            },
            timeout: 60000,
          },
          (response) => {
            let data = '';
            response.on('data', (bytes) => {
              data += bytes;
            });
            response.on('end', () => {
              try {
                assert.equal(response.statusCode, 200);
                resolve(JSON.parse(data).result);
              } catch (error) {
                reject(error);
              }
            });
          },
        );
        req.on('timeout', () => req.destroy(new Error('Preset readiness timed out')));
        req.on('error', reject);
        req.end(body);
      });
    const presets = await rpc('agentPresets/list');
    assert.equal(presets.ok, true);
    const creator = presets.value.presets.find((row) => row.id === 'cordis');
    assert.ok(creator);
    assert.equal(
      creator.broken,
      undefined,
      'Creator must load before runtime readiness is published',
    );
    const initial = await rpc('session/list', { _request: {} });
    assert.equal(initial.ok, true);
    assert.ok(
      initial.value.items.some(
        (row) =>
          row.sessionId === 'harness-initial-' + config.instance_id &&
          row.cwd === '/data/workspace' &&
          row.agentAvailable,
      ),
      'The first usable session must exist before native readiness',
    );
    const session = await rpc('session/create', {
      request: { agentPreset: 'standard', workspaceId: native.workspace_id },
    });
    assert.equal(session.ok, true, 'A first session must initialize before any reconnect');
    assert.ok(session.value.sessionId);
    const settings = await rpc('settings/describe');
    assert.equal(settings.ok, true, 'Settings must be available before readiness');
    assert.equal(settings.value.writable, true);
    const modelSettings = settings.value.namespaces.find((row) => row.ns === 'agent-default-model');
    assert.ok(modelSettings);
    const overriddenModel = await rpc('settings/update', {
      ns: modelSettings.ns,
      patch: { model: 'unapproved-test-model' },
      expectedRevision: modelSettings.revision,
    });
    assert.equal(
      overriddenModel.ok,
      false,
      'Platform model configuration must remain controlled by the launch overlay',
    );
    assert.equal(overriddenModel.error.code, 'settings/rejected');
    const presetSettings = settings.value.namespaces.find(
      (row) => row.ns === 'agent-preset-registry',
    );
    assert.ok(presetSettings);
    const savedPreset = await rpc('settings/update', {
      ns: presetSettings.ns,
      patch: { selectedDefault: 'cordis' },
      expectedRevision: presetSettings.revision,
    });
    assert.equal(savedPreset.ok, true, JSON.stringify(savedPreset));
    assert.equal(savedPreset.value.value.selectedDefault, 'cordis');
    const stalePreset = await rpc('settings/update', {
      ns: presetSettings.ns,
      patch: { selectedDefault: 'standard' },
      expectedRevision: presetSettings.revision,
    });
    assert.equal(stalePreset.ok, false, 'Stale settings writes must be refused');
    assert.equal(stalePreset.error.code, 'settings/conflict');
    const inventory = await rpc('pluginInventory/list');
    assert.equal(inventory.ok, true);
    assert.equal(inventory.value.managementAvailable, true);
    const bundles = await rpc('pluginManager/listBundles');
    assert.equal(bundles.ok, true, 'The persistent profile manager must be available');
    const memoryBundle = memoryRuntime.package;
    const memorySkills = memoryRuntime.skills;
    if (sessionMemory) {
      const bundle = bundles.value.find((row) => row.name === memoryBundle);
      assert.ok(bundle, 'The memory bundle must be managed by the native profile');
      assert.equal(bundle.version, memoryRuntime.version);
      assert.equal(bundle.installed, true);
      assert.equal(bundle.enabled, true);
      assert.equal(bundle.error, undefined);
      const plugins = await rpc('pluginManager/listPlugins');
      assert.equal(plugins.ok, true);
      assert.ok(
        plugins.value.some(
          (row) => row.entryId === 'include:' + memoryBackend + '-memory-runtime' && row.enabled,
        ),
        'The native memory plugin entry must activate',
      );
      const skills = await rpc('skills/list', { request: { sessionId: session.value.sessionId } });
      assert.equal(skills.ok, true, JSON.stringify(skills));
      for (const skill of memorySkills)
        assert.ok(
          skills.value.skills.some((row) => row.name === skill && row.modelInvocable),
          `The model must see bundled memory skill ${skill}`,
        );
    }
    const builtin = '@deepseek-ai/dsh-experimental-session-search';
    assert.ok(bundles.value.some((row) => row.name === builtin));
    const toggle = async (name, enabled) => {
      const result = await rpc('pluginManager/setBundleEnabled', { name, enabled });
      assert.equal(result.ok, true);
      assert.equal(result.value.application, 'applied', JSON.stringify(result));
      const rows = await rpc('pluginManager/listBundles');
      assert.equal(rows.value.find((row) => row.name === name)?.enabled, enabled);
    };
    await toggle(builtin, true);
    await toggle(builtin, false);
    const registry = await rpc('pluginManager/inspect', { spec: 'is-number@7.0.0' });
    assert.equal(registry.ok, true);
    assert.equal(registry.value.status, 'refused', JSON.stringify(registry));
    assert.equal(
      registry.value.problem,
      'not-a-bundle',
      'Registry lookup must identify a plain library and refuse to install it as a plugin',
    );
    // A genuine out-of-installation bundle, installed by pnpm without a registry
    // dependency or inference. It must activate, survive restart and uninstall.
    const addon = join(data, 'workspace', 'test-addon');
    await mkdir(addon);
    await writeFile(
      join(addon, 'package.json'),
      JSON.stringify({
        name: 'hc-test-addon',
        version: '1.0.0',
        type: 'module',
        dsh: { bundle: { patch: 'cordis.patch.json' } },
      }),
    );
    await writeFile(
      join(addon, 'cordis.patch.json'),
      JSON.stringify([{ insert: [{ id: 'hc-test-addon', name: './plugin.mjs' }] }]),
    );
    await writeFile(
      join(addon, 'plugin.mjs'),
      sessionMemory
        ? `import fs from 'node:fs';
export const inject = ['tools', 'agents'];
export function apply(ctx) {
  ctx.provide('hcTestAddon', true);
  const inspect = () => {
    const target = '/run/harness/openviking-tools-test.json';
    fs.writeFileSync(target + '.tmp', JSON.stringify(ctx.tools.schemas(ctx.agents.get(${JSON.stringify(session.value.sessionId)})).map(tool => tool.name)), {mode: 0o600});
    fs.renameSync(target + '.tmp', target);
  };
  ctx.on('tools/change', inspect);
  inspect();
}\n`
        : 'export function apply(ctx) { ctx.provide("hcTestAddon", true); }\n',
    );
    const installed = await rpc('pluginManager/installBundle', {
      spec: 'file:/data/workspace/test-addon',
      options: { enabled: true },
    });
    assert.equal(installed.ok, true);
    assert.equal(installed.value.application, 'applied', JSON.stringify(installed));
    const addonRows = await rpc('pluginManager/listPlugins');
    const addonRow = addonRows.value.find(
      (row) => row.moduleName?.includes('hc-test-addon') || row.entryId === 'include:hc-test-addon',
    );
    assert.ok(addonRow?.enabled, 'Third-party bundle must actually activate');
    if (sessionMemory) {
      const memoryTools = localMemory
        ? [`mcp__${memoryBackend}__search`, `mcp__${memoryBackend}__status`]
        : ['mcp__openviking__find', 'mcp__openviking__remember'];
      const expectTools = async (enabled) => {
        let tools = [];
        for (let attempt = 0; attempt < 80; attempt++) {
          tools = JSON.parse(await readFile(join(run, 'openviking-tools-test.json'), 'utf8'));
          assert.equal(
            tools.some((name) => name.startsWith('cloud_memory_')),
            false,
            'Native memory must use its MCP tools rather than duplicate built-in tools',
          );
          if (memoryTools.every((tool) => tools.includes(tool) === enabled)) return;
          await new Promise((resolve) => setTimeout(resolve, 100));
        }
        assert.equal(
          memoryTools.every((tool) => tools.includes(tool) === enabled),
          true,
          `Memory tool activation must match bundle enabled=${enabled}`,
        );
      };
      await expectTools(true);
      await toggle(memoryBundle, false);
      await expectTools(false);
      const disabled = await rpc('skills/list', {
        request: { sessionId: session.value.sessionId },
      });
      assert.equal(disabled.ok, true);
      assert.equal(
        disabled.value.skills.some((row) => memorySkills.includes(row.name)),
        false,
        'Disabling the native bundle must remove its skill provider',
      );
      await toggle(memoryBundle, true);
      await expectTools(true);
      const enabled = await rpc('skills/list', { request: { sessionId: session.value.sessionId } });
      assert.equal(enabled.ok, true);
      for (const skill of memorySkills)
        assert.ok(
          enabled.value.skills.some((row) => row.name === skill),
          `Re-enabling the bundle must restore ${skill}`,
        );
    }
    assert.equal((await request('/', { Host: 'wrong.harness.test', Cookie: cookie })).status, 401);
    assert.equal(
      (await request('/api', { Cookie: cookie, Origin: 'https://wrong.harness.test' })).status,
      403,
    );
    const ws = {
      Connection: 'Upgrade',
      Upgrade: 'websocket',
      'Sec-WebSocket-Version': '13',
      'Sec-WebSocket-Key': randomBytes(16).toString('base64'),
      Origin: config.public_url,
    };
    assert.equal((await request('/api/remote.mux', ws, true)).status, 401);
    assert.equal(
      (
        await request(
          '/api/remote.mux',
          { ...ws, Cookie: cookie, Origin: 'https://wrong.harness.test' },
          true,
        )
      ).status,
      403,
    );
    assert.equal((await request('/api/remote.mux', { ...ws, Cookie: cookie }, true)).status, 101);
    await docker(
      'exec',
      name,
      'node',
      '-e',
      "require('fs').writeFileSync('/data/workspace/retained-proof.txt','retained-original-data',{mode:0o600})",
    );
    config.runtime_generation = randomUUID();
    await writeFile(join(run, 'config.json'), JSON.stringify(config), { mode: 0o600 });
    await docker('restart', name);
    const restarted = await ready();
    assert.notEqual(restarted.token, native.token);
    const renewed = await request('/?token=' + encodeURIComponent(restarted.token));
    cookie = renewed.headers['set-cookie'][0].split(';')[0];
    const retained = await rpc('session/list', { _request: {} });
    assert.equal(retained.ok, true);
    assert.equal(
      retained.value.items.filter(
        (row) => row.sessionId === 'harness-initial-' + config.instance_id,
      ).length,
      1,
      'Restart must preserve the initial session without duplicates',
    );
    assert.ok(
      retained.value.items.some((row) => row.sessionId === session.value.sessionId),
      'Restart must preserve later sessions',
    );
    const afterRestart = await rpc('pluginManager/listBundles');
    if (sessionMemory) {
      const bundle = afterRestart.value.find((row) => row.name === memoryBundle);
      assert.ok(bundle?.enabled);
      assert.equal(bundle.version, memoryRuntime.version);
      assert.equal(bundle.error, undefined);
    }
    const retainedSettings = await rpc('settings/describe');
    assert.equal(retainedSettings.ok, true);
    assert.equal(
      retainedSettings.value.namespaces.find((row) => row.ns === 'agent-preset-registry')?.value
        .selectedDefault,
      'cordis',
      'Preset default must survive restart',
    );
    assert.equal(
      afterRestart.value.find((row) => row.name === 'hc-test-addon')?.enabled,
      true,
      'Installed plugins must survive restart',
    );
    await toggle('hc-test-addon', false);
    await toggle('hc-test-addon', true);
    const removed = await rpc('pluginManager/removeBundle', { name: 'hc-test-addon' });
    assert.equal(removed.ok, true);
    assert.equal(removed.value.application, 'applied', JSON.stringify(removed));
    assert.equal(
      (await rpc('pluginManager/listBundles')).value.some((row) => row.name === 'hc-test-addon'),
      false,
    );
    assert.equal(
      await readFile(join(data, 'workspace', 'retained-proof.txt'), 'utf8'),
      'retained-original-data',
    );
    const logs = await docker('logs', name);
    assert.equal(logs.includes(native.token), false, 'Native token appeared in container output');
    assert.equal(logs.includes(key), false, 'Model key appeared in container output');
    const ports = await docker('inspect', '-f', '{{json .HostConfig.PortBindings}}', name);
    assert.ok(ports === '{}' || ports === 'null');
  },
);
