import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const image = process.env.HC_RUNTIME_IMAGE;
test(
  'packed Mem0 bundle loads in pinned DSH and saves, recalls and isolates real OSS memories',
  { skip: !image || process.env.HC_MEM0_PLUGIN_LIVE !== 'true', timeout: 45000 },
  async () => {
    const archive = fileURLToPath(
      new URL(
        '../../../apps/public/plugin-packages/harness-cloud-dsh-mem0-memory-0.2.0.tgz',
        import.meta.url,
      ),
    );
    const key = (
      await readFile(new URL('../../../.local/context/test-key', import.meta.url), 'utf8')
    ).trim();
    const script = `
   import assert from 'node:assert/strict';
   import fs from 'node:fs';
   import {spawn,spawnSync} from 'node:child_process';
   import {createInterface} from 'node:readline';
   import {randomUUID} from 'node:crypto';
   import {loadProfile, composeEntries} from '@deepseek-ai/dsh-app-boot';
   import {createUserMessage} from '@deepseek-ai/dsh-llm';
   const home='/tmp/mem0-plugin-test', profile=home+'/profiles/verification';
   const packageName='@harness-cloud/dsh-mem0-memory';
   const pluginDir=profile+'/node_modules/'+packageName;
   fs.mkdirSync(pluginDir,{recursive:true});
   const extracted=spawnSync('tar',['-xzf','/run/mem0-plugin.tgz','--strip-components=1','-C',pluginDir]);
   assert.equal(extracted.status,0);
   fs.symlinkSync('/opt/harness-runtime/node_modules',pluginDir+'/node_modules');
   fs.writeFileSync(profile+'/package.json',JSON.stringify({name:'verification',private:true,dependencies:{[packageName]:'file:/run/mem0-plugin.tgz'},dsh:{profile:{bundles:[packageName]}}}));
   const loaded=loadProfile('dsh','verification','/opt/harness-runtime/node_modules/@deepseek-ai/dsh/package.json',home);
   assert.deepEqual(loaded.skippedBundles,[]);
   assert.equal(loaded.layers.length,1);
   const entries=composeEntries(loaded.layers.map(layer=>layer.patches));
   assert.equal(entries[0].id,'mem0-memory-runtime');assert.equal(entries[0].disabled,true);
   const plugin=await import(pluginDir+'/index.mjs');
   const owner='plugin-test-'+randomUUID(), other='plugin-test-'+randomUUID();
   const apiKey=${JSON.stringify(key)}, endpoint='http://mem0:8580';
   const native=async(path,body,method='POST')=>{
    const response=await fetch(endpoint+path,{method,headers:{'X-API-Key':apiKey,'Content-Type':'application/json'},...(body?{body:JSON.stringify(body)}:{})});
    assert.equal(response.status,200);return response.json();
   };
   const handlers=new Map(),effects=[],mounts=[];
   const ctx={logger:{warn(){}},on(event,handler){handlers.set(event,handler);},provide(key,value){this[key]=value;},get(key){return this[key];},effect(setup){effects.push(setup());},plugin(_plugin,config){mounts.push(config);return Promise.resolve();},tools:{register(){throw Error('Direct tools must not be registered');}}};
   await plugin.apply(ctx,{endpoint,apiKey,userId:owner,autoCapture:true});
   const mcp=mounts.find(config=>config.serverName==='mem0');
   assert.ok(mounts.some(config=>config.providerName==='mem0'));
   const child=spawn(mcp.command,mcp.args,{env:{...process.env,...mcp.env},stdio:['pipe','pipe','pipe']});
   let sequence=0;const pending=new Map();
   const lines=createInterface({input:child.stdout});
   lines.on('line',line=>{const message=JSON.parse(line);pending.get(message.id)?.(message);pending.delete(message.id);});
   const rpc=(method,params={})=>new Promise(resolve=>{const id=++sequence;pending.set(id,resolve);child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\\n');});
   try{
    const initialized=await rpc('initialize',{protocolVersion:'2025-06-18',capabilities:{},clientInfo:{name:'mem0-test',version:'1'}});
    assert.equal(initialized.result.serverInfo.name,'mem0');
    const listed=await rpc('tools/list');
    assert.deepEqual(listed.result.tools.map(tool=>tool.name),['search','status','remember']);
    const session={id:'first-'+randomUUID(),header:{},events:[]};
    const content='星河项目发布窗口固定在周三下午15点。';
    const message=createUserMessage({content:[{type:'text',text:content}],source:{kind:'user'}});
    const emit=(type,data)=>handlers.get('session/event')(session,{type,data});
    emit('user/message',message);emit('user/message',message);
    emit('user/message',createUserMessage({content:[{type:'text',text:'不可保存的插件上下文'}],source:{kind:'time-context'}}));
    await handlers.get('session/flush')(session);
    const records=await native('/memories?user_id='+owner,null,'GET');
    assert.equal(records.results.length,1);assert.equal(records.results[0].memory,content);
    assert.equal(ctx.mem0Memory.status().saved,1);
    const foreign=await native('/search',{query:'星河项目发布时间',filters:{user_id:other,scope:'personal'},top_k:5});
    assert.equal(foreign.results.length,0);
    const next={id:'second-'+randomUUID(),header:{},events:[]};
    const input=createUserMessage({content:[{type:'text',text:'星河项目什么时候发布？'}],source:{kind:'user'}});
    const decision=await handlers.get('agent/pre-step')({agent:{session:next},turn:1,step:1,signal:new AbortController().signal},async()=>({kind:'enter',messages:[input],startsRequestSeries:true}));
    assert.equal(decision.startsRequestSeries,true);assert.equal(decision.messages.length,2);
    assert.equal(decision.messages[1].source.kind,'mem0-memory');
    assert.match(JSON.stringify(decision.messages[1]),/周三下午15点/);
    const searched=await rpc('tools/call',{name:'search',arguments:{query:'星河项目发布时间'}});
    assert.match(searched.result.content[0].text,/周三下午15点/);
    const denied=await rpc('tools/call',{name:'search',arguments:{query:'星河',user_id:other}});
    assert.equal(denied.result.isError,true);
    const guard=handlers.get('tools/pre-execute');
    const execution={name:'mcp__mem0__remember',arguments:{content:'发布负责人为李明。'},agent:{session:next},callId:randomUUID()};
    assert.equal((await guard({...execution,agent:null},async()=>({kind:'allow'}))).kind,'deny');
    const ask=await guard(execution,async()=>({kind:'allow'}));
    assert.equal(ask.kind,'ask');assert.match(ask.reason,/发布负责人为李明/);
    assert.equal((await native('/memories?user_id='+owner,null,'GET')).results.length,1);
    // Simulate the native runtime's allowed-once decision before executing MCP.
    const saved=await rpc('tools/call',{name:'remember',arguments:execution.arguments});
    const receipt=JSON.parse(saved.result.content[0].text);
    assert.equal(receipt.saved,true);assert.equal(receipt.ids.length,1);
    console.log(JSON.stringify({bundle:true,capture:true,recall:true,isolation:true,confirmed_save:true}));
   }finally{
    child.kill();lines.close();
    for(const cleanup of effects.reverse())await cleanup?.();
    const records=await native('/memories?user_id='+owner,null,'GET');
    for(const row of records.results)await native('/memories/'+row.id,null,'DELETE');
    assert.equal((await native('/memories?user_id='+owner,null,'GET')).results.length,0);
   }
  `;
    const output = await new Promise((resolve, reject) => {
      const child = execFile(
        'docker',
        [
          'run',
          '--rm',
          '-i',
          '--network',
          'hc-context_default',
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
          '--mount',
          'type=bind,src=' + archive + ',dst=/run/mem0-plugin.tgz,readonly',
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
      bundle: true,
      capture: true,
      recall: true,
      isolation: true,
      confirmed_save: true,
    });
  },
);
