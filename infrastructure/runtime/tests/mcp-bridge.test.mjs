import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const run = promisify(execFile);

test(
  'official MCP adapter preserves schemas and results and follows server lifecycle',
  {
    skip: !process.env.HC_RUNTIME_IMAGE,
    timeout: 30000,
  },
  async () => {
    const program = `
    import assert from 'node:assert/strict';
    import fs from 'node:fs/promises';
    import { Context } from '@deepseek-ai/cordis';
    import SystemPrompt from '@deepseek-ai/dsh-system-prompt';
    import ToolRuntime from '@deepseek-ai/dsh-tools';
    import { installExtensions } from '/qa/install_extensions.mjs';
    import { publicToolName } from '/qa/cloud_mcp.mjs';
    const root = await fs.mkdtemp('/tmp/mcp-test-');
    const channel = '/run/harness/';
    const id = 'aabbccdd-1111-4111-8111-111111111111';
    const schema = { type:'object', properties:{ query:{ type:'object', properties:{ text:{type:'string'} } } }, required:['query'] };
    const tool = { name:'echo', description:'Echo', input_schema:schema, definition:{ annotations:{readOnlyHint:true,destructiveHint:false} }, hash:'one', enabled:true };
    let tools = [tool], servers, requests = [], approval = 'allowed-once';
    const identity = {schema_version:1,instance_id:'instance',asset_id:'asset',org_id:'org',owner_id:'owner'};
    await fs.writeFile(root+'/.hc-initialized',JSON.stringify(identity));
    installExtensions(root,{
      ...identity,
      extensions:{items:[{kind:'connector',id}]},
      program:await fs.readFile('/qa/cloud_extensions.mjs','utf8'),
      mcp_program:await fs.readFile('/qa/cloud_mcp.mjs','utf8'),
    });
    await fs.writeFile(channel+'runtime.json',JSON.stringify({instance_id:'instance',runtime_generation:'generation'}));
    const ctx = new Context();
    ctx.provide('approval',{request:async () => approval});
    const fibers = [];
    fibers.push(ctx.plugin(SystemPrompt,{})); await fibers.at(-1);
    fibers.push(ctx.plugin(ToolRuntime,{mode:'native'})); await fibers.at(-1);
    const registered = {
      get(name) { return ctx.tools.get(name); },
      get size() { return ctx.tools.schemas().length; },
    };
    const publish = async (expires = Date.now()+15000, generation='generation') => {
      servers = [{id,name:'Test MCP',version:7,catalog_version:3,tools}];
      await fs.writeFile(channel+'view.tmp', JSON.stringify({ generation, expires_at:expires, servers }));
      await fs.rename(channel+'view.tmp',channel+'.hc-mcp-tools.json');
    };
    const waitFor = async (condition) => {
      for(let i=0;i<100;i++) { if(condition()) return; await new Promise(r=>setTimeout(r,50)); }
      throw Error('Catalog did not reconcile');
    };
    await publish();
    // Exercise the installed extension's actual private file channel.
    let busy=false;
    const responder=setInterval(async () => {
      if(busy) return;
      busy=true;
      try {
        const body=JSON.parse(await fs.readFile(channel+'.hc-extension-request.json','utf8'));
        await fs.unlink(channel+'.hc-extension-request.json');
        requests.push(body);
        await fs.writeFile(channel+'result.tmp',JSON.stringify({id:body.id,result:{status:'SUCCEEDED',result:{content:[{type:'text',text:'中文回显'}],structuredContent:{ok:true}}}}),{mode:0o600});
        await fs.rename(channel+'result.tmp',channel+'.hc-extension-response.json');
      } catch(error) { if(error.code!=='ENOENT') throw error; }
      finally { busy=false; }
    },10);
    try {
    const extension=await import(root+'/dsh/profiles/web/cloud-extensions.mjs');
    fibers.push(ctx.plugin({name:'installed-mcp-test',inject:['tools'],apply:extension.apply}));
    await fibers.at(-1);
    const name = publicToolName(id,'echo');
    assert.equal(ctx.tools.schemas().length,1);
    assert.deepEqual(ctx.tools.schemas()[0].parameters,schema);
    assert.match(name,/^mcp__[a-f0-9]{32}__echo$/);
    assert.match(registered.get(name).description,/Test MCP/);
    assert.deepEqual(registered.get(name).parameters,schema);
    const args={query:{text:'中文输入'}};
    const result=await registered.get(name).execute(args,{agent:{},callId:'one'});
    assert.deepEqual(result.content,[{type:'text',text:'中文回显'}]);
    assert.deepEqual(result.structuredContent,{ok:true});
    assert.deepEqual(requests[0].arguments,args);
    assert.equal(requests[0].tool,'echo');
    assert.equal(requests[0].expected_version,7);
    assert.equal(requests[0].catalog_version,3);
    tools=[tool,{...tool,name:'write',definition:{},hash:'two'}];
    await publish();
    await waitFor(()=>registered.size===2);
    approval='denied';
    await assert.rejects(registered.get(publicToolName(id,'write')).execute({}, {agent:{},callId:'two'}),/未确认/);
    assert.equal(requests.length,1);
    // Removing/disabled servers and stale or foreign runtime generations withdraw tools.
    tools=[]; await publish(); await waitFor(()=>registered.size===0);
    tools=[tool]; await publish(); await waitFor(()=>registered.size===1);
    await publish(0); await waitFor(()=>registered.size===0);
    await publish(Date.now()+15000,'other-generation');
    await new Promise(r=>setTimeout(r,1100)); assert.equal(registered.size,0);
    assert.notEqual(publicToolName(id,'a.b'),publicToolName(id,'a_b'));
    assert.equal(publicToolName(id,'x'.repeat(200)).length,64);
    await fibers.pop().dispose();
    assert.equal(registered.size,0);
    console.log('Native MCP schema, Chinese result, live discovery, confirmation, withdrawal and naming passed');
    } finally {
      clearInterval(responder);
      for(const fiber of fibers.reverse()) await fiber.dispose();
    }
  `;
    const { stdout } = await run(
      'docker',
      [
        'run',
        '--rm',
        '--network',
        'none',
        '--read-only',
        '--tmpfs',
        '/tmp',
        '--tmpfs',
        '/run/harness:rw,nosuid,nodev,uid=1000,gid=1000',
        '--user',
        '1000:1000',
        '--cap-drop',
        'ALL',
        '--mount',
        `type=bind,src=${fileURLToPath(new URL('../workspace-assets/', import.meta.url))},dst=/qa,readonly`,
        '--entrypoint',
        'node',
        process.env.HC_RUNTIME_IMAGE,
        '--input-type=module',
        '-e',
        program,
      ],
      { maxBuffer: 1024 * 1024 },
    );
    assert.match(stdout, /Native MCP schema/);
  },
);
