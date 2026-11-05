import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mountMem0Mcp } from '../mcp.mjs';

async function client(t, api) {
  let config;
  const effects = [];
  await mountMem0Mcp(
    {
      plugin(_plugin, value) {
        config = value;
        return Promise.resolve();
      },
      effect(setup) {
        effects.push(setup());
      },
    },
    api,
  );
  const child = spawn(config.command, config.args, {
    env: { ...process.env, ...config.env },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  const lines = createInterface({ input: child.stdout });
  const pending = new Map();
  let sequence = 0;
  lines.on('line', (line) => {
    const message = JSON.parse(line);
    pending.get(message.id)?.(message);
    pending.delete(message.id);
  });
  t.after(async () => {
    child.kill();
    lines.close();
    for (const cleanup of effects.reverse()) await cleanup();
  });
  const send = (message) =>
    child.stdin.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\n');
  const request = (method, params = {}) => {
    const id = ++sequence;
    return {
      id,
      response: new Promise((resolve) => {
        pending.set(id, resolve);
        send({ id, method, params });
      }),
    };
  };
  return { request, send };
}

test(
  'stdio MCP preserves fixed scope, advertises managed capabilities and cancels in-flight retrieval',
  { timeout: 10000 },
  async (t) => {
    let started;
    const ready = new Promise((resolve) => {
      started = resolve;
    });
    let aborted = false;
    const calls = [];
    const c = await client(t, {
      status: () => ({ managed: true, saved: 2 }),
      search: async (args, signal) => {
        calls.push(args);
        if (args.query === 'slow') {
          started();
          await new Promise((resolve, reject) =>
            signal.addEventListener(
              'abort',
              () => {
                aborted = true;
                reject(signal.reason);
              },
              { once: true },
            ),
          );
        }
        return { items: [{ text: 'Release Wednesday' }] };
      },
    });
    const initialized = await c.request('initialize', { protocolVersion: '2025-06-18' }).response;
    assert.equal(initialized.result.serverInfo.name, 'mem0');
    assert.match(initialized.result.instructions, /mem0-memory/);
    c.send({ method: 'notifications/initialized' });
    const listed = await c.request('tools/list').response;
    assert.deepEqual(
      listed.result.tools.map((tool) => tool.name),
      ['search', 'status'],
    );
    const found = await c.request('tools/call', { name: 'search', arguments: { query: 'release' } })
      .response;
    assert.match(found.result.content[0].text, /Wednesday/);
    for (const args of [
      { query: 'q', user_id: 'other' },
      { query: 'q', limit: 10 },
      { query: ' ' },
    ]) {
      const rejected = await c.request('tools/call', { name: 'search', arguments: args }).response;
      assert.equal(rejected.result.isError, true);
    }
    const unavailable = await c.request('tools/call', {
      name: 'remember',
      arguments: { content: 'q' },
    }).response;
    assert.equal(unavailable.result.isError, true);
    assert.deepEqual(calls, [{ query: 'release' }]);
    const state = await c.request('tools/call', { name: 'status' }).response;
    assert.deepEqual(JSON.parse(state.result.content[0].text), { managed: true, saved: 2 });
    const slow = c.request('tools/call', { name: 'search', arguments: { query: 'slow' } });
    await ready;
    c.send({
      method: 'notifications/cancelled',
      params: { requestId: slow.id, reason: 'user stopped' },
    });
    assert.ok((await slow.response).error);
    // The transport cancellation must reach the parent, not just abandon its response.
    for (let attempt = 0; !aborted && attempt < 50; attempt++)
      await new Promise((resolve) => setTimeout(resolve, 10));
    assert.equal(aborted, true);
  },
);
