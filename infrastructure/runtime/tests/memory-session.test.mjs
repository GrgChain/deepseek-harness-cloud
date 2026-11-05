import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';

async function fixture() {
  const code = (
    await readFile(new URL('../../plugins/mem0/session.mjs', import.meta.url), 'utf8')
  ).replace(
    /import \{ createUserMessage \} from '[^']+';/,
    "const createUserMessage = (message) => ({role:'user', ...message});",
  );
  const { mountSessionMemory } = await import(
    'data:text/javascript;base64,' + Buffer.from(code).toString('base64')
  );
  const handlers = new Map(),
    calls = [],
    stored = new Map(),
    effects = [];
  let runtime,
    fail = false;
  const ctx = {
    on: (name, handler) => handlers.set(name, handler),
    provide: (_name, value) => {
      runtime = value;
    },
    logger: { warn() {} },
    effect: (setup) => effects.push(setup()),
  };
  await mountSessionMemory(
    ctx,
    { id: 'shared-memory', session_scope: 'personal' },
    async (input) => {
      calls.push(input);
      assert.equal(input.source_id, 'shared-memory');
      assert.equal(input.scope, 'personal');
      if (input.action === 'search') {
        if (fail) throw Error('recall unavailable');
        return JSON.stringify({
          items: [...stored.values()].map((row) => ({
            id: row.id,
            content: row.content,
            source: 'DSH 自动同步',
          })),
        });
      }
      const batch = JSON.parse(input.session_request.body);
      for (const message of batch.messages)
        stored.set(batch.session_id + ':' + message.id, message);
      if (fail) throw Error('unknown outcome after write');
      return JSON.stringify({ accepted: batch.messages.length });
    },
  );
  const session = { id: 'session-1', events: [] };
  const emit = (type, seq, content, source) => {
    const message = {
      role: type === 'assistant/message' ? 'assistant' : 'user',
      content: [{ type: 'text', text: content }],
      ...(source ? { source: { kind: source } } : {}),
    };
    const event = { type, seq, data: type === 'user/message' ? message : { message } };
    session.events.push(event);
    handlers.get('session/event')(session, event);
  };
  return {
    handlers,
    calls,
    stored,
    effects,
    session,
    emit,
    runtime,
    fail: (value) => {
      fail = value;
    },
  };
}

test('full conversation capture preserves all visible roles, skips injected context and deduplicates replay', async () => {
  const f = await fixture();
  f.emit('user/message', 1, '每周三发布', 'user');
  f.emit('assistant/message', 2, '我会在发布前检查', 'model');
  f.emit('tool/result', 3, '测试已通过', 'tool');
  f.emit('user/message', 4, '不应保存的召回上下文', 'cloud-memory');
  f.emit('user/message', 5, '不应保存的系统注入', 'time-context');
  await f.runtime.flush(f.session);
  assert.deepEqual(
    [...f.stored.values()].map((row) => row.role),
    ['user', 'assistant', 'tool'],
  );
  assert.doesNotMatch(JSON.stringify([...f.stored.values()]), /不应保存/);
  await f.handlers.get('agent/created')({ agent: { session: f.session } });
  await f.runtime.flush(f.session);
  assert.equal(f.calls.filter((call) => call.action === 'session').length, 1);
  assert.equal(f.runtime.status().pendingBytes, 0);
  for (const effect of f.effects) await effect();
});

test('failed writes retain stable batches for later retry and persisted events restore them after restart', async () => {
  const f = await fixture();
  f.fail(true);
  f.emit('user/message', 1, '网络超时后的记忆', 'user');
  await f.runtime.flush(f.session);
  assert.ok(f.runtime.status().pendingBytes > 0);
  f.fail(false);
  await f.runtime.flush(f.session);
  assert.deepEqual(f.calls[0].session_request, f.calls[1].session_request);
  assert.equal(f.stored.size, 1);
  assert.equal(f.runtime.status().pendingBytes, 0);
  const restored = await fixture();
  await restored.handlers.get('agent/created')({ agent: { session: f.session } });
  await restored.runtime.flush(f.session);
  assert.equal(restored.calls[0].session_request.body, f.calls[0].session_request.body);
  for (const effect of [...f.effects, ...restored.effects]) await effect();
});

test('automatic recall spans sessions, runs once per turn and never blocks a conversation on failure', async () => {
  const f = await fixture();
  f.emit('user/message', 1, '每周三发布', 'user');
  await f.runtime.flush(f.session);
  const input = { agent: { session: { id: 'session-2' } }, turn: 1 };
  const decision = {
    kind: 'enter',
    messages: [
      {
        role: 'user',
        source: { kind: 'user' },
        content: [{ type: 'text', text: '什么时候发布？' }],
      },
    ],
  };
  const recall = f.handlers.get('agent/pre-step');
  const recalled = await recall(input, async () => decision);
  assert.equal(recalled.messages.length, 2);
  assert.match(recalled.messages[1].content[0].text, /不可信参考资料.*\n.*每周三发布/s);
  assert.equal(recalled.messages[1].source.kind, 'cloud-memory');
  assert.equal(await recall(input, async () => decision), decision);
  assert.equal(f.calls.filter((call) => call.action === 'search').length, 1);
  f.fail(true);
  assert.equal(await recall({ ...input, turn: 2 }, async () => decision), decision);
  for (const effect of f.effects) await effect();
});

test(
  'managed Mem0 and Graphiti profiles load in the pinned DSH runtime without an OpenViking bundle',
  { skip: !process.env.HC_RUNTIME_IMAGE, timeout: 45000 },
  async () => {
    const installer = await readFile(
      new URL('../workspace-assets/install_extensions.mjs', import.meta.url),
      'utf8',
    );
    const transport = await readFile(
      new URL('../workspace-assets/openviking_session.mjs', import.meta.url),
      'utf8',
    );
    const artifact = await readFile(
      new URL('../../plugins/mem0-dist/harness-cloud-dsh-mem0-memory-0.2.0.tgz', import.meta.url),
    );
    const graphitiArtifact = await readFile(
      new URL(
        '../../plugins/graphiti-dist/harness-cloud-dsh-graphiti-memory-0.2.0.tgz',
        import.meta.url,
      ),
    );
    const script = `
    import assert from 'node:assert/strict'; import fs from 'node:fs';
    import {loadProfile, composeEntries} from '/opt/harness-runtime/node_modules/@deepseek-ai/dsh-app-boot/lib/index.js';
    const {installExtensions}=await import('data:text/javascript;base64,${Buffer.from(installer).toString('base64')}');
    for (const backend of ['mem0','graphiti']) {
      const root='/tmp/managed-'+backend; fs.mkdirSync(root);
      const identity={schema_version:1,instance_id:'instance',asset_id:'asset',org_id:'org',owner_id:'owner'};
      fs.writeFileSync(root+'/.hc-initialized',JSON.stringify(identity));
      installExtensions(root,{...identity,extensions:{items:[{kind:'memory',id:'shared',backend,session_memory:true,session_scope:'personal'}]},program:'',session_program:${JSON.stringify(transport)},session_plugin:backend==='mem0'?${JSON.stringify(artifact.toString('base64'))}:${JSON.stringify(graphitiArtifact.toString('base64'))}});
      const manifest=JSON.parse(fs.readFileSync(root+'/dsh/profiles/web/package.json','utf8'));
      assert.ok(!manifest.dsh.profile.bundles.includes('@openviking/dsh-memory-plugin'));
      const handlers=new Map(),effects=[],rows=[],raw=[]; let runtime;
      const ctx={on:(name,handler)=>handlers.set(name,handler),provide(name,value){this[name]=value;if(name===(backend==='mem0'?'mem0Memory':'graphitiMemory'))runtime=value;},get(name){return this[name];},tools:{register(){throw Error('No direct tool registration');}},plugin(){return Promise.resolve();},effect:setup=>effects.push(setup()),logger:{warn(){}}};
      const request=async input=>{
        if(input.action==='search')return JSON.stringify({items:rows.map(row=>({...row,snippet:row.content}))});
        if(input.session_request.path==='/sessions/commit'){const commit=JSON.parse(input.session_request.body);const committed=commit.force&&raw.length&&!rows.length;if(committed)rows.push({id:'fact',content:'周三发布'});return JSON.stringify({committed:!!committed,memories:committed?1:0,pending_messages:rows.length?0:raw.length});}
        const batch=JSON.parse(input.session_request.body); raw.push(...batch.messages); return JSON.stringify({accepted:batch.messages.length});
      };
      const packageName='@harness-cloud/dsh-'+backend+'-memory';
      const version='0.2.0';
      assert.equal(manifest.dependencies[packageName],'file:./harness-cloud-dsh-'+backend+'-memory-'+version+'.tgz');
      const loaded=loadProfile('dsh','web','/opt/harness-runtime/node_modules/@deepseek-ai/dsh/package.json',root+'/dsh');
      assert.deepEqual(loaded.skippedBundles,[]);
      const entries=composeEntries([...loaded.layers.map(layer=>layer.patches), loaded.patches]);
      const entry=entries.find(row=>row.id===backend+'-memory-runtime');assert.equal(entry.disabled,false);
      assert.equal(entry.config.apiKey,undefined);assert.equal(entry.config.userId,undefined);
      assert.equal(fs.existsSync(root+'/dsh/profiles/web/memory-session.mjs'),false);
      const {mountSessionMemory}=await import(root+'/dsh/profiles/web/openviking-session.mjs');
      await mountSessionMemory(ctx,{id:'shared',backend,session_scope:'personal'},request);
      const plugin=await import(root+'/dsh/profiles/web/node_modules/'+packageName+'/index.mjs');
      await plugin.apply(ctx,{...(backend==='mem0'?{managed:true}:{}),endpoint:ctx.cloudSessionMemory.endpoint});
      assert.ok(runtime);
      for(const path of ['/memories','/sessions/other','/search?user_id=foreign'])assert.equal((await fetch(ctx.cloudSessionMemory.endpoint+path,{method:'POST',body:'{}'})).status,404);
      const session={id:'first',events:[]};
      for(const [seq,type,role,content,source] of [[1,'user/message','user','周三发布','user'],[2,'assistant/message','assistant','收到','model'],[3,'tool/result','user','测试通过','tool']]){
        const message={role,source:{kind:source},content:[{type:'text',text:content}]};
        handlers.get('session/event')(session,{type,seq,data:type==='user/message'?message:{message}});
      }
      await runtime.flush(session); assert.deepEqual(raw.map(row=>row.role),['user','assistant','tool']);assert.equal(rows.length,0);
      handlers.get('session/event')(session,{type:'compaction/start',seq:4});await runtime.flush(session);assert.equal(rows.length,1);assert.equal(runtime.status().committed,1);
      const decision=await handlers.get('agent/pre-step')({agent:{session:{id:'second'}},turn:1},async()=>({kind:'enter',messages:[{role:'user',source:{kind:'user'},content:[{type:'text',text:'何时发布'}]}]}));
      assert.equal(decision.messages.length,2); assert.match(JSON.stringify(decision.messages[1]),/周三发布/);
      for(const cleanup of effects.reverse())await cleanup();
    }
    console.log('managed-memory-runtime-passed');
  `;
    const output = await new Promise((resolve, reject) => {
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
          '--security-opt',
          'no-new-privileges:true',
          '--memory',
          '512m',
          '--pids-limit',
          '64',
          '--tmpfs',
          '/tmp:rw,nosuid,nodev,size=64m,uid=1000,gid=1000',
          '--entrypoint',
          'node',
          process.env.HC_RUNTIME_IMAGE,
          '--input-type=module',
        ],
        { timeout: 40000, maxBuffer: 8192 },
        (error, stdout, stderr) =>
          error ? reject(new Error(stderr || error.message)) : resolve(stdout),
      );
      child.stdin.end(script);
    });
    assert.equal(output.trim(), 'managed-memory-runtime-passed');
  },
);
