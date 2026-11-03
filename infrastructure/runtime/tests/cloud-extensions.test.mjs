import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rename, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

async function fixture(
  t,
  outcome = 'allowed-once',
  warnings = [],
  applyError = '',
  permissions = ['read', 'write', 'delete'],
  kind = 'knowledge',
) {
  const root = await mkdtemp(join(tmpdir(), 'hc-context-tool-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const program = (
    await readFile(new URL('../workspace-assets/cloud_extensions.mjs', import.meta.url), 'utf8')
  )
    .replace(
      /import\s*\{\s*defineTool\s*\}\s*from\s*['"]\/opt\/harness-runtime\/node_modules\/@deepseek-ai\/dsh-tools\/lib\/index\.js['"];/,
      'const defineTool=value=>value;',
    )
    .replace(
      /const channel\s*=\s*['"]\/run\/harness\/['"];/,
      `const channel=${JSON.stringify(root + '/')};`,
    );
  await writeFile(join(root, 'extension.mjs'), program);
  await writeFile(
    join(root, 'cloud-extensions.json'),
    JSON.stringify({
      items: [{ kind, id: 'knowledge-1', name: '项目知识', scopes: ['personal'], permissions }],
    }),
  );
  await writeFile(
    join(root, 'runtime.json'),
    JSON.stringify({ instance_id: 'workspace', runtime_generation: 'generation' }),
  );
  const tools = [];
  const approvals = [];
  const requests = [];
  let responseReady = false;
  const timer = setInterval(async () => {
    if (responseReady) return;
    try {
      const file = join(root, '.hc-extension-request.json');
      const request = JSON.parse(await readFile(file, 'utf8'));
      responseReady = true;
      await rm(file);
      requests.push(request);
      const result =
        request.action === 'preview'
          ? {
              token: 'preview-token',
              items: [
                {
                  title: request.title,
                  content: request.content,
                  source: request.source,
                  conflict: false,
                },
              ],
              warnings,
              data_use: '发送到测试 Embedding 服务',
            }
          : request.action === 'delete_preview'
            ? { id: request.record_id, title: '发布约定', content: '每周三发布。', version: 3 }
            : { applied: ['document-1'], skipped: [] };
      const response =
        request.action === 'apply' && applyError
          ? { id: request.id, error: applyError }
          : { id: request.id, result };
      await writeFile(join(root, 'result.tmp'), JSON.stringify(response));
      await rename(join(root, 'result.tmp'), join(root, '.hc-extension-response.json'));
      responseReady = false;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }, 10);
  t.after(() => clearInterval(timer));
  const ctx = {
    tools: { register: (tool) => tools.push(tool) },
    pluginManager: { listBundles: async () => [] },
    get: (name) =>
      name === 'approval'
        ? {
            request: async (input) => {
              approvals.push(input);
              return outcome;
            },
          }
        : undefined,
  };
  await (await import(pathToFileURL(join(root, 'extension.mjs')).href)).apply(ctx);
  return { tools, approvals, requests, ctx };
}
const args = { title: '发布约定', content: '每周三发布。', scope: 'workspace:untrusted' };
const exec = { callId: 'call-1', agent: { session: { id: 'session-1' } } };
test('knowledge submission previews exact content and applies only after approval', async (t) => {
  const f = await fixture(t);
  const tool = f.tools.find((tool) => tool.name.startsWith('cloud_knowledge_submit_'));
  assert.ok(tool);
  for (const registered of f.tools) assert.equal(registered.parameters.scope, undefined);
  const result = JSON.parse(await tool.execute(args, exec));
  assert.deepEqual(result.applied, ['document-1']);
  assert.deepEqual(
    f.requests.map((r) => r.action),
    ['preview', 'apply'],
  );
  assert.notEqual(f.requests[0].id, f.requests[1].id);
  assert.ok(f.requests.every((request) => request.scope === 'personal'));
  assert.doesNotMatch(f.approvals[0].reason, /个人|本工作空间/);
  assert.equal(f.requests[1].confirmed, true);
  assert.equal(f.requests[1].token, 'preview-token');
  assert.match(f.approvals[0].reason, /发布约定/);
  assert.match(f.approvals[0].reason, /每周三发布。/);
  assert.match(f.approvals[0].reason, /Embedding/);
});
test('denied submission does not apply and missing approval does not send content', async (t) => {
  const f = await fixture(t, 'denied');
  const tool = f.tools.find((tool) => tool.name.startsWith('cloud_knowledge_submit_'));
  await assert.rejects(tool.execute(args, exec), /用户未确认/);
  assert.deepEqual(
    f.requests.map((r) => r.action),
    ['preview'],
  );
  f.ctx.get = () => undefined;
  await assert.rejects(tool.execute(args, { ...exec, callId: 'call-2' }), /不支持写入确认/);
  assert.equal(f.requests.length, 1);
});
test('preview warnings prevent approval and submission', async (t) => {
  const f = await fixture(t, 'allowed-once', ['requires review']);
  const tool = f.tools.find((tool) => tool.name.startsWith('cloud_knowledge_submit_'));
  await assert.rejects(tool.execute(args, exec), /冲突或警告/);
  assert.equal(f.approvals.length, 0);
  assert.deepEqual(
    f.requests.map((r) => r.action),
    ['preview'],
  );
});
test('failed apply is not retried or reported as saved', async (t) => {
  const f = await fixture(t, 'allowed-once', [], '服务超时');
  const tool = f.tools.find((tool) => tool.name.startsWith('cloud_knowledge_submit_'));
  await assert.rejects(tool.execute(args, exec), /提交结果未确认.*不要重复新增/);
  assert.deepEqual(
    f.requests.map((r) => r.action),
    ['preview', 'apply'],
  );
});
test('knowledge tools match independently granted permissions', async (t) => {
  for (const [permission, prefix] of [
    ['read', 'cloud_knowledge_knowledge'],
    ['write', 'cloud_knowledge_submit_'],
    ['delete', 'cloud_knowledge_delete_'],
  ]) {
    const f = await fixture(t, 'allowed-once', [], '', [permission]);
    assert.equal(f.tools.length, 1);
    assert.ok(f.tools[0].name.startsWith(prefix));
  }
});
test('delete previews the exact version and requires single approval', async (t) => {
  const f = await fixture(t);
  const tool = f.tools.find((tool) => tool.name.startsWith('cloud_knowledge_delete_'));
  await tool.execute({ record_id: 'document-1' }, exec);
  assert.deepEqual(
    f.requests.map((r) => r.action),
    ['delete_preview', 'delete'],
  );
  assert.equal(f.requests[1].confirmed, true);
  assert.equal(f.requests[1].record_version, 3);
  assert.match(f.approvals[0].reason, /无法恢复/);
  assert.match(f.approvals[0].reason, /发布约定/);
  const denied = await fixture(t, 'denied');
  await assert.rejects(
    denied.tools
      .find((tool) => tool.name.startsWith('cloud_knowledge_delete_'))
      .execute({ record_id: 'document-1' }, exec),
    /未删除/,
  );
  assert.deepEqual(
    denied.requests.map((r) => r.action),
    ['delete_preview'],
  );
});

test('memory retrieval uses the shared connection corpus and ignores tool-selected scopes', async (t) => {
  const f = await fixture(t, 'allowed-once', [], '', ['read'], 'memory');
  assert.equal(f.tools.length, 1);
  const tool = f.tools[0];
  assert.match(tool.name, /^cloud_memory_/);
  assert.equal(tool.parameters.scope, undefined);
  await tool.execute({ query: 'shared memory', scope: 'workspace:untrusted' }, exec);
  assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].scope, 'personal');
  assert.equal(f.requests[0].kind, 'memory');
  assert.equal(f.requests[0].action, 'search');
});
