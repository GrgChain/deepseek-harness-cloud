import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';

const image = process.env.HC_RUNTIME_IMAGE;
test(
  'official OpenViking plugin captures complete conversation events and recalls across sessions in the pinned runtime',
  { skip: !image, timeout: 45000 },
  async () => {
    const installer = await readFile(
      new URL('../workspace-assets/install_extensions.mjs', import.meta.url),
      'utf8',
    );
    const sessionProgram = await readFile(
      new URL('../workspace-assets/openviking_session.mjs', import.meta.url),
      'utf8',
    );
    const artifact = await readFile(
      new URL('../../plugins/openviking/openviking-dsh-memory-plugin-0.5.31.tgz', import.meta.url),
    );
    const script = `
 import assert from 'node:assert/strict';import fs from 'node:fs';
 const {installExtensions}=await import('data:text/javascript;base64,${Buffer.from(installer).toString('base64')}');
 const root='/tmp/native-session';fs.mkdirSync(root);
 const identity={schema_version:1,instance_id:'instance',asset_id:'asset',org_id:'org',owner_id:'owner'};
 fs.writeFileSync(root+'/.hc-initialized',JSON.stringify(identity));
 const item={kind:'memory',id:'native',version:1,session_memory:true,session_scope:'personal',scopes:['personal']};
 const input={...identity,extensions:{items:[item]},program:'',session_program:${JSON.stringify(sessionProgram)},session_plugin:${JSON.stringify(artifact.toString('base64'))}};
 installExtensions(root,input);fs.unlinkSync(root+'/.hc-extensions-initialized');installExtensions(root,input);
 const {mountSessionMemory}=await import(root+'/dsh/profiles/web/openviking-session.mjs');
 const handlers=new Map(),effects=[],mounts=[],captured=[],queries=[],commits=[];let runtime,failNext=false;
 const ctx={logger:{debug(){}},on(name,handler){handlers.set(name,handler);},provide(name,value){this[name]=value;if(name==='openvikingMemory')runtime=value;},effect(setup){effects.push(setup());},plugin(plugin,config){mounts.push(config);return Promise.resolve();}};
 const request=async input=>{
  assert.equal(input.scope,'personal');assert.equal(input.source_id,'native');assert.equal(input.action,'session');
  const native=input.session_request;const path=native.path.split('?')[0];const body=native.body?JSON.parse(native.body):null;
  let status=200,result={};
  if(path==='/api/v1/sessions'&&native.method==='POST')result={session_id:body.session_id};
  else if(path.endsWith('/messages')){if(failNext){failNext=false;status=503;}else captured.push({path,body});}
  else if(path.endsWith('/commit'))commits.push(path);
  else if(path==='/api/v1/search/search'){queries.push(body.query);result={rendered:'历史记忆：发布窗口是周三下午。',entries:[{uri:'viking://~/memories/sentinel',content:'发布窗口是周三下午。'}],stats:{}};}
  else if(path==='/api/v1/system/status')result={user:'owner'};
  else if(path.startsWith('/api/v1/fs/'))result=[];
  else if(path==='/api/v1/skills')result={skills:[]};
  else if(path==='/api/v1/content/read')status=404;
  else if(path.startsWith('/api/v1/sessions/'))result={pending_tokens:30000};
  return JSON.stringify({status,headers:{'content-type':'application/json'},body:JSON.stringify({status:status===200?'ok':'error',result})});
 };
 try{
  await mountSessionMemory(ctx,item,request);
  const manifest=JSON.parse(fs.readFileSync(root+'/dsh/profiles/web/package.json','utf8'));
  assert.equal(manifest.dependencies['@openviking/dsh-memory-plugin'],'0.5.31');
  assert.ok(manifest.dsh.profile.bundles.includes('@openviking/dsh-memory-plugin'));
  const patches=JSON.parse(fs.readFileSync(root+'/dsh/profiles/web/cordis.patch.yml','utf8'));
  const entry=patches.find(patch=>patch.id==='openviking-memory-runtime');
  const plugin=await import(root+'/dsh/profiles/web/node_modules/@openviking/dsh-memory-plugin/index.mjs');
  plugin.apply(ctx,{...entry.config,endpoint:ctx.cloudSessionMemory.endpoint});
  assert.ok(mounts.some(c=>c.serverName==='openviking'));assert.ok(mounts.some(c=>c.providerName==='openviking'));
  const session={id:'first',header:{cwd:'/tmp'},events:[]};const agent={session,status:'idle',ctx:{effect(){}},inject(){}};
  await handlers.get('agent/created')({agent,source:'test'});
  const emit=(type,data)=>handlers.get('session/event')(session,{type,data,time:Date.now()});
  emit('user/message',{role:'user',source:{kind:'user'},content:[{type:'text',text:'发布窗口是周三下午。请记住完整对话。'}]});
  emit('assistant/message',{message:{role:'assistant',content:[{type:'text',text:'已理解发布安排，我会在周三检查。'}]}});
  emit('user/message',{role:'user',source:{kind:'time-context'},content:[{type:'text',text:'应跳过的注入上下文'}]});
  emit('tool/call',{callId:'tool-1',name:'read'});
  emit('tool/result',{message:{role:'user',source:{kind:'tool',callId:'tool-1'},content:[{type:'text',text:'发布记录：已完成验收。'}]}});
  emit('turn/end',{});await runtime.flush(session);
  assert.equal(captured.length,3);assert.equal(commits.length,1);
  assert.match(JSON.stringify(captured),/周三下午/);assert.match(JSON.stringify(captured),/已理解发布安排/);assert.match(JSON.stringify(captured),/已完成验收/);assert.doesNotMatch(JSON.stringify(captured),/应跳过/);
  failNext=true;emit('user/message',{role:'user',content:[{type:'text',text:'断线补写验证'}]});await runtime.flush(session);await runtime.replayPendingQueue();
  assert.match(JSON.stringify(captured),/断线补写验证/);
  const nextAgent={...agent,session:{id:'second',header:{cwd:'/tmp'},events:[]}};
  const decision=await handlers.get('agent/pre-step')({agent:nextAgent,signal:new AbortController().signal},async()=>({kind:'enter',messages:[{role:'user',content:[{type:'text',text:'发布窗口是什么时候？'}]},{role:'user',source:{kind:'time-context'},content:[{type:'text',text:'不应作为查询'}]}]}));
  assert.match(JSON.stringify(decision.messages),/历史记忆：发布窗口是周三下午/);assert.deepEqual(queries,['发布窗口是什么时候？']);
  console.log(JSON.stringify({capture:true,tools:true,recall:true,retry:true}));
 }finally{for(const cleanup of effects.reverse())await cleanup?.();}
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
          '--env',
          'HOME=/tmp/dsh',
          '--entrypoint',
          'node',
          image,
          '--input-type=module',
        ],
        { timeout: 40000, maxBuffer: 8192 },
        (error, stdout, stderr) =>
          error ? reject(new Error(stderr || error.message)) : resolve(stdout),
      );
      child.stdin.end(script);
    });
    assert.deepEqual(JSON.parse(output.trim()), {
      capture: true,
      tools: true,
      recall: true,
      retry: true,
    });
  },
);
