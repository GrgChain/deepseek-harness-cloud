import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mountSessionMemory } from '../session.mjs';

async function fixture(t) {
  const handlers = new Map(),
    effects = [],
    agentEffects = [],
    calls = [],
    raw = [],
    facts = [];
  let runtime,
    failCommit = false,
    onCommit;
  const ctx = {
    on: (name, handler) => handlers.set(name, handler),
    effect: (setup) => effects.push(setup()),
    provide: (_name, value) => {
      runtime = value;
    },
    logger: { warn() {} },
  };
  await mountSessionMemory(
    ctx,
    { id: 'connection', session_scope: 'personal' },
    async (input) => {
      calls.push(input);
      if (input.action === 'search') return JSON.stringify({ items: facts });
      const body = JSON.parse(input.session_request.body);
      if (input.session_request.path === '/sessions/messages') {
        raw.push(...body.messages);
        return JSON.stringify({ accepted: body.messages.length });
      }
      assert.equal(input.session_request.path, '/sessions/commit');
      if (failCommit) throw Error('commit unavailable');
      await onCommit?.(body);
      const committed = body.force && raw.length && !facts.length;
      if (committed) facts.push({ id: 'fact', content: '每周三发布' });
      return JSON.stringify({
        committed: !!committed,
        memories: committed ? 1 : 0,
        pending_messages: committed ? 0 : raw.length,
      });
    },
    { archiveSessions: true },
  );
  t.after(async () => {
    for (const cleanup of effects.reverse()) await cleanup();
  });
  const session = { id: 'session', events: [] };
  const agent = { session, ctx: { effect: (setup) => agentEffects.push(setup()) } };
  const emit = (type, seq, content) => {
    const message = { content: [{ type: 'text', text: content }] };
    const event = { type, seq, data: type === 'user/message' ? message : { message } };
    session.events.push(event);
    handlers.get('session/event')(session, event);
  };
  return {
    handlers,
    runtime,
    calls,
    raw,
    facts,
    session,
    agent,
    agentEffects,
    emit,
    fail: (value) => {
      failCommit = value;
    },
    onCommit: (fn) => {
      onCommit = fn;
    },
  };
}

test('short turns sync raw messages; compaction commits and recalls extracted facts', async (t) => {
  const f = await fixture(t);
  f.emit('user/message', 1, '每周三发布');
  f.emit('assistant/message', 2, '收到');
  f.emit('tool/result', 3, '测试通过');
  f.emit('turn/end', 4);
  await f.runtime.flush(f.session);
  assert.deepEqual(
    f.raw.map((row) => row.role),
    ['user', 'assistant', 'tool'],
  );
  assert.equal(f.facts.length, 0);
  const commits = () => f.calls.filter((call) => call.session_request?.path === '/sessions/commit');
  assert.equal(JSON.parse(commits()[0].session_request.body).force, false);
  f.emit('compaction/start', 5);
  await f.runtime.flush(f.session);
  assert.equal(JSON.parse(commits()[1].session_request.body).force, true);
  assert.equal(f.runtime.status().committed, 1);
  const decision = await f.handlers.get('agent/pre-step')(
    { agent: { session: { id: 'next' } }, turn: 1 },
    async () => ({
      kind: 'enter',
      messages: [{ role: 'user', content: [{ type: 'text', text: '何时发布' }] }],
    }),
  );
  assert.match(JSON.stringify(decision.messages.at(-1)), /每周三发布/);
});

test('agent disposal commits short sessions and failed commits retry at checkpoints', async (t) => {
  const f = await fixture(t);
  await f.handlers.get('agent/created')({ agent: f.agent });
  f.emit('user/message', 1, '每周三发布');
  await f.runtime.flush(f.session);
  f.fail(true);
  await f.agentEffects[0]();
  assert.equal(f.runtime.status().failed, 1);
  assert.equal(f.facts.length, 0);
  f.fail(false);
  await f.runtime.flush(f.session);
  assert.equal(f.facts.length, 1);
  assert.equal(f.runtime.status().pendingBytes, 0);
});

test('a compaction event arriving during a threshold check is not lost', async (t) => {
  const f = await fixture(t);
  let once = true;
  f.onCommit(async (body) => {
    if (once && !body.force) {
      once = false;
      f.emit('compaction/start', 9);
    }
  });
  f.emit('user/message', 1, '每周三发布');
  f.emit('turn/end', 2);
  await f.runtime.flush(f.session);
  assert.equal(f.facts.length, 1);
});
