import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { apply } from '../index.mjs';

test('Graphiti rejects external addresses and identity-bearing endpoints', async () => {
  for (const endpoint of [
    'https://graphiti.example/mcp',
    'http://localhost:8000/' + 'a'.repeat(48),
    'http://127.0.0.1:8000/' + 'a'.repeat(48) + '?group_id=other',
    'http://user:secret@127.0.0.1:8000/' + 'a'.repeat(48),
  ]) {
    await assert.rejects(apply({}, { endpoint }), /platform loopback endpoint/);
  }
});

test(
  'managed plugin stages conversations, archives facts and recalls across sessions and uses only skill and MCP mounts',
  { timeout: 10000 },
  async (t) => {
    const prefix = '/' + 'a'.repeat(48),
      rows = [],
      raw = [],
      requests = [];
    let unavailable = false;
    const server = http.createServer(async (req, res) => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks));
      requests.push({ path: req.url, body });
      res.setHeader('content-type', 'application/json');
      if (unavailable) {
        res.writeHead(503).end('{}');
        return;
      }
      if (req.url === prefix + '/search')
        res.end(
          JSON.stringify({ items: rows.map((row) => ({ id: row.id, snippet: row.content })) }),
        );
      else {
        if (req.url === prefix + '/sessions/commit') {
          const committed = body.force && raw.length && !rows.length;
          if (committed) rows.push({ id: 'fact', content: '发布窗口周三' });
          res.end(
            JSON.stringify({
              committed: !!committed,
              memories: committed ? 1 : 0,
              pending_messages: rows.length ? 0 : raw.length,
            }),
          );
          return;
        }
        assert.equal(req.url, prefix + '/sessions/messages');
        raw.push(...body.messages);
        res.end(JSON.stringify({ accepted: body.messages.length }));
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const handlers = new Map(),
      effects = [],
      mounts = [];
    const ctx = {
      on(event, handler) {
        handlers.set(event, handler);
      },
      get(name) {
        return this[name];
      },
      provide(name, value) {
        this[name] = value;
      },
      effect(setup) {
        effects.push(setup());
      },
      plugin(_plugin, config) {
        mounts.push(config);
        return Promise.resolve();
      },
      tools: {
        register() {
          throw Error('Use MCP tools');
        },
      },
      logger: { warn() {} },
    };
    t.after(async () => {
      unavailable = false;
      for (const cleanup of effects.reverse()) await cleanup();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    await apply(ctx, { endpoint: `http://127.0.0.1:${server.address().port}${prefix}` });
    const skill = mounts.find((config) => config.providerName === 'graphiti');
    assert.equal(skill.includeDefaultRoots, false);
    assert.match(
      await readFile(skill.bundledSkillDir + '/graphiti-memory/SKILL.md', 'utf8'),
      /mcp__graphiti__search/,
    );
    const mcp = mounts.find((config) => config.serverName === 'graphiti');
    assert.equal(mcp.transport, 'stdio');
    assert.deepEqual(Object.keys(mcp.env).sort(), [
      'DSH_GRAPHITI_MCP_ENDPOINT',
      'ELECTRON_RUN_AS_NODE',
    ]);
    const session = { id: 'one', events: [] };
    const emit = (seq, type, source, text) => {
      const message = {
        id: String(seq),
        source: { kind: source },
        content: [{ type: 'text', text }],
      };
      handlers.get('session/event')(session, {
        seq,
        type,
        data: type === 'user/message' ? message : { message },
      });
    };
    emit(1, 'user/message', 'user', '发布窗口周三');
    emit(1, 'user/message', 'user', '发布窗口周三');
    emit(2, 'assistant/message', 'model', '收到');
    emit(3, 'tool/result', 'tool', '检查完成');
    emit(4, 'user/message', 'graphiti-memory', '不要保存注入内容');
    await ctx.graphitiMemory.flush(session);
    assert.deepEqual(
      raw.map((row) => row.role),
      ['user', 'assistant', 'tool'],
    );
    assert.equal(ctx.graphitiMemory.status().saved, 3);
    assert.equal(rows.length, 0);
    emit(5, 'compaction/start', 'system', '');
    await ctx.graphitiMemory.flush(session);
    assert.equal(ctx.graphitiMemory.status().committed, 1);
    const decision = {
      kind: 'enter',
      startsRequestSeries: true,
      messages: [
        { role: 'user', source: { kind: 'user' }, content: [{ type: 'text', text: '何时发布' }] },
      ],
    };
    const next = { agent: { session: { id: 'two' } }, turn: 1 };
    const result = await handlers.get('agent/pre-step')(next, async () => decision);
    assert.equal(result.startsRequestSeries, true);
    assert.equal(result.messages[1].source.kind, 'graphiti-memory');
    assert.match(JSON.stringify(result.messages[1]), /周三/);
    const search = requests.find((row) => row.path.endsWith('/search'));
    assert.deepEqual(search.body, { query: '何时发布' });
    unavailable = true;
    const fallback = await handlers.get('agent/pre-step')(
      { ...next, turn: 2 },
      async () => decision,
    );
    assert.equal(fallback, decision);
    emit(6, 'user/message', 'user', '待同步消息');
    await ctx.graphitiMemory.flush(session);
    assert.equal(ctx.graphitiMemory.status().failed, 1);
    assert.ok(ctx.graphitiMemory.status().pendingBytes > 0);
  },
);
