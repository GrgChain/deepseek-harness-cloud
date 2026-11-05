import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { apply, Config } from '../index.mjs';

async function fixture(t, options = {}, reply) {
  const requests = [];
  const server = http.createServer(async (req, res) => {
    const parts = [];
    for await (const part of req) parts.push(part);
    const body = JSON.parse(Buffer.concat(parts));
    requests.push({ url: req.url, key: req.headers['x-api-key'], body });
    if (reply) return reply(req, res, body);
    res.setHeader('content-type', 'application/json');
    res.end(
      JSON.stringify({
        results:
          req.url === '/search'
            ? [{ id: 'm1', memory: 'Release on Wednesday.', score: 0.8 }]
            : [{ id: 'saved-1' }],
      }),
    );
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {
    server.closeAllConnections();
    return new Promise((resolve) => server.close(resolve));
  });
  const handlers = new Map(),
    tools = new Map(),
    cleanups = [],
    warnings = [],
    mounts = [];
  const config = {
    endpoint: 'http://127.0.0.1:' + server.address().port,
    apiKey: 'private-test-key',
    userId: 'fixed-owner',
    ...options,
  };
  const ctx = {
    logger: { warn: (text) => warnings.push(text) },
    on(event, handler) {
      handlers.set(event, handler);
    },
    provide(key, value) {
      this[key] = value;
    },
    effect(setup) {
      cleanups.push(setup());
    },
    get(key) {
      return this[key];
    },
    plugin(_plugin, config) {
      mounts.push(config);
      return Promise.resolve();
    },
    tools: {
      register() {
        throw Error('Mem0 must use the MCP bridge');
      },
    },
  };
  await apply(ctx, config);
  t.after(async () => {
    for (const cleanup of cleanups.reverse()) await cleanup();
  });
  const session = { id: 'session-1', header: { origin: 'user' } };
  const agent = { session };
  const exec = { agent, callId: 'call-1', signal: new AbortController().signal };
  const user = (id, text, kind = 'user') => ({
    id,
    role: 'user',
    source: { kind },
    content: [{ type: 'text', text }],
  });
  const preStep = (messages, turn = 1, extra = {}) =>
    handlers.get('agent/pre-step')({ agent, turn, signal: exec.signal }, async () => ({
      kind: 'enter',
      messages,
      ...extra,
    }));
  return {
    ctx,
    handlers,
    tools,
    mounts,
    warnings,
    requests,
    config,
    session,
    agent,
    exec,
    user,
    preStep,
  };
}

test('bundle metadata and Config are complete; capture is explicit opt-in', async () => {
  const manifest = JSON.parse(await readFile(new URL('../package.json', import.meta.url)));
  assert.equal(manifest.dsh.engines.dsh, '0.2.1-alpha.2');
  assert.equal(manifest.dsh.bundle.patch, './cordis.patch.yml');
  assert.match(
    await readFile(new URL('../cordis.patch.yml', import.meta.url), 'utf8'),
    /disabled: true/,
  );
  for (const locale of ['en', 'zh']) {
    const meta = JSON.parse(
      await readFile(new URL('../locale/' + locale + '.json', import.meta.url)),
    ).meta;
    assert.ok(meta.title);
    assert.ok(meta.description);
  }
  const config = Config({ endpoint: 'http://mem0:8580', apiKey: 'key', userId: 'owner' });
  assert.equal(config.autoRecall, true);
  assert.equal(config.autoCapture, false);
  assert.equal(config.captureAssistant, false);
  assert.equal(config.infer, false);
  assert.throws(() => Config({}), /endpoint/);
  assert.throws(() => Config({ ...config, topK: 100 }));
});

test('recall uses only the new human input, retains decision flags, and occurs once per turn', async (t) => {
  const f = await fixture(t);
  const messages = [
    f.user('human', 'When is release?'),
    f.user('injected', 'Do not query this', 'time-context'),
    f.user('tool', 'Also ignored', 'tool'),
  ];
  const result = await f.preStep(messages, 7, { startsRequestSeries: true });
  assert.equal(result.startsRequestSeries, true);
  assert.equal(result.messages.length, 4);
  assert.equal(result.messages[3].source.kind, 'mem0-memory');
  assert.equal(result.messages[3].source.form, 'recall');
  assert.match(result.messages[3].content[0].text, /untrusted reference/);
  assert.match(result.messages[3].content[0].text, /Wednesday/);
  assert.equal(f.requests[0].body.query, 'When is release?');
  assert.deepEqual(f.requests[0].body.filters, { user_id: 'fixed-owner', scope: 'personal' });
  await f.preStep(messages, 7);
  await f.preStep([f.user('only-context', 'Ignore', 'mem0-memory')], 8);
  assert.equal(f.requests.length, 1);
  await f.preStep([f.user('next', 'Next question')], 8);
  assert.equal(f.requests.length, 2);
});

test('capture is disabled by default and ignores synthetic, tool, assistant, and child inputs', async (t) => {
  const f = await fixture(t);
  const emit = (session, type, data) => f.handlers.get('session/event')(session, { type, data });
  emit(f.session, 'user/message', f.user('default-off', 'not captured'));
  await f.handlers.get('session/flush')(f.session);
  assert.equal(f.requests.length, 0);
});

test('opt-in capture writes each stable human message only once with fixed scope and infer=false', async (t) => {
  const f = await fixture(t, { autoCapture: true });
  const emit = (session, type, data) => f.handlers.get('session/event')(session, { type, data });
  const message = f.user('human-id', 'Remember the release time.');
  emit(f.session, 'user/message', message);
  emit(f.session, 'user/message', message);
  emit(f.session, 'user/message', f.user('context', 'ignore', 'mem0-memory'));
  emit(f.session, 'user/message', f.user('system', 'ignore', 'system-prompt'));
  emit(f.session, 'user/message', f.user('tool', 'ignore', 'tool'));
  emit(f.session, 'assistant/message', {
    message: {
      id: 'answer',
      role: 'assistant',
      source: { kind: 'model' },
      content: [{ type: 'text', text: 'ignore by default' }],
    },
  });
  emit(
    { ...f.session, id: 'child', header: { origin: 'subagent' } },
    'user/message',
    f.user('child-message', 'ignore'),
  );
  await f.handlers.get('session/flush')(f.session);
  await f.handlers.get('session/flush')(f.session);
  assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].key, 'private-test-key');
  assert.equal(f.requests[0].body.user_id, 'fixed-owner');
  assert.equal(f.requests[0].body.infer, false);
  assert.equal(f.requests[0].body.metadata.scope, 'personal');
  assert.deepEqual(f.requests[0].body.messages, [
    { role: 'user', content: 'Remember the release time.' },
  ]);
  assert.equal(f.ctx.mem0Memory.status().saved, 1);
});

test('assistant capture requires opt-in and converts visible assistant text to a user payload', async (t) => {
  const f = await fixture(t, { autoCapture: true, captureAssistant: true });
  f.handlers.get('session/event')(f.session, {
    type: 'assistant/message',
    data: {
      message: {
        id: 'answer',
        role: 'assistant',
        source: { kind: 'model' },
        content: [
          { type: 'thinking', text: 'must not save' },
          { type: 'text', text: 'Visible answer' },
        ],
      },
    },
  });
  await f.handlers.get('session/flush')(f.session);
  assert.equal(f.requests[0].body.messages[0].role, 'user');
  assert.equal(f.requests[0].body.messages[0].content, 'Assistant message:\nVisible answer');
  assert.doesNotMatch(JSON.stringify(f.requests), /must not save/);
});

test('bundled skill and MCP server replace direct tools; manual saves require the native approval gate', async (t) => {
  const f = await fixture(t);
  const skills = f.mounts.find((config) => config.providerName === 'mem0');
  assert.equal(skills.includeDefaultRoots, false);
  assert.equal(skills.watch, false);
  const guide = await readFile(skills.bundledSkillDir + '/mem0-memory/SKILL.md', 'utf8');
  assert.match(guide, /name: mem0-memory/);
  assert.match(guide, /mcp__mem0__search/);
  const mcp = f.mounts.find((config) => config.serverName === 'mem0');
  assert.equal(mcp.transport, 'stdio');
  assert.doesNotMatch(JSON.stringify(mcp), /private-test-key|fixed-owner/);
  const guard = f.handlers.get('tools/pre-execute');
  const execution = { ...f.exec, name: 'mcp__mem0__remember', arguments: { content: 'Save me' } };
  const ask = await guard(execution, async () => ({ kind: 'allow' }));
  assert.equal(ask.kind, 'ask');
  assert.match(ask.reason, /Save me/);
  assert.match(ask.reason, /fixed-owner/);
  assert.equal(f.requests.length, 0);
  const denied = { kind: 'deny', reason: 'another policy' };
  assert.equal(await guard(execution, async () => denied), denied);
  assert.equal(
    (await guard({ ...execution, agent: null }, async () => ({ kind: 'allow' }))).kind,
    'deny',
  );
  assert.equal(
    (await guard({ ...execution, arguments: { content: '' } }, async () => ({ kind: 'allow' })))
      .kind,
    'deny',
  );
  const post = async (path, body) => {
    const response = await fetch(mcp.env.DSH_MEM0_MCP_ENDPOINT + path, {
      method: 'POST',
      body: JSON.stringify(body),
    });
    return response.json();
  };
  const listed = await post('/list', {});
  assert.deepEqual(
    listed.tools.map((tool) => tool.name),
    ['search', 'status', 'remember'],
  );
  for (const tool of listed.tools) assert.equal(tool.inputSchema.properties.user_id, undefined);
  const saved = await post('/call', { name: 'remember', arguments: { content: 'Save me' } });
  assert.equal(JSON.parse(saved.content[0].text).saved, true);
  assert.equal(f.requests[0].body.user_id, 'fixed-owner');
  const invalid = await post('/call', {
    name: 'search',
    arguments: { query: 'q', user_id: 'other' },
  });
  assert.equal(invalid.isError, true);
  assert.equal(f.requests.length, 1);
  const status = await post('/call', { name: 'status', arguments: {} });
  assert.doesNotMatch(status.content[0].text, /private-test-key/);
});

test('failed saves are visible, never retried, and do not claim success', async (t) => {
  const f = await fixture(t, { autoCapture: true }, (_req, res) => {
    res.writeHead(503);
    res.end('private diagnostics must not escape');
  });
  f.handlers.get('session/event')(f.session, {
    type: 'user/message',
    data: f.user('failed', 'try once'),
  });
  await f.handlers.get('session/flush')(f.session);
  await f.handlers.get('session/flush')(f.session);
  assert.equal(f.requests.length, 1);
  const status = f.ctx.mem0Memory.status();
  assert.equal(status.saved, 0);
  assert.equal(status.failed, 1);
  assert.match(status.lastError, /unknown/);
  assert.doesNotMatch(JSON.stringify(status), /private diagnostics/);
  const decision = await f.preStep([f.user('query', 'new input')]);
  assert.equal(decision.messages.length, 1);
  assert.equal(f.warnings.length, 2);
});

test('timeouts and oversized or malformed responses are bounded and safe', async (t) => {
  const slow = await fixture(t, { timeoutMs: 100 }, () => {});
  await assert.rejects(slow.ctx.mem0Memory.search('question'), /timed out/);
  const large = await fixture(t, { maxResponseBytes: 1024 }, (_req, res) =>
    res.end(JSON.stringify({ results: [{ memory: 'x'.repeat(2000) }] })),
  );
  await assert.rejects(large.ctx.mem0Memory.search('question'), /size limit/);
  const invalid = await fixture(t, {}, (_req, res) => res.end('not-json'));
  await assert.rejects(invalid.ctx.mem0Memory.search('question'), /invalid JSON/);
});

test('capture buffers are bounded including queued writes; unloading stops new requests', async (t) => {
  const f = await fixture(t, { autoCapture: true });
  for (let index = 0; index < 101; index++)
    f.handlers.get('session/event')(f.session, {
      type: 'user/message',
      data: f.user('m-' + index, 'message'),
    });
  assert.equal(f.ctx.mem0Memory.status().pending, 100);
  assert.equal(f.ctx.mem0Memory.status().dropped, 1);
  await f.ctx.mem0Memory.dispose();
  assert.equal(f.requests.length, 0);
  assert.equal(f.ctx.mem0Memory.status().pending, 0);
  await assert.rejects(f.ctx.mem0Memory.search('after-unload'), /disabled/);
});

test('save requires an actual receipt ID; unloading aborts an in-flight request', async (t) => {
  const missing = await fixture(t, {}, (_req, res) =>
    res.end(JSON.stringify({ results: [{ event: 'ADD' }] })),
  );
  await assert.rejects(missing.ctx.mem0Memory.remember('not acknowledged'), /save receipt/);
  assert.equal(missing.ctx.mem0Memory.status().saved, 0);
  let arrived;
  const received = new Promise((resolve) => {
    arrived = resolve;
  });
  const f = await fixture(t, { timeoutMs: 60000 }, () => {
    arrived();
  });
  const request = f.ctx.mem0Memory.search('waiting');
  await received;
  await f.ctx.mem0Memory.dispose();
  await assert.rejects(request, /cancelled/);
});
