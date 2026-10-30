import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';
import http from 'node:http';
import https from 'node:https';
import { chromium } from '../../../apps/node_modules/playwright-core/index.mjs';

const exec = promisify(execFile);
const docker = async (...args) => (await exec('docker', args, { timeout: 45000 })).stdout.trim();

for (const platform of [false, true]) test(`real runtime synchronizes cold sessions after ${platform ? 'platform provider' : 'built-in model'} replacement`, { skip: !process.env.HC_RUNTIME_IMAGE || !process.env.HC_WORKER_SYNC_TEST_BINARY, timeout: 150000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'hc-model-sync-'));
  const data = join(root, 'data'), run = join(root, 'run'), name = 'hc-model-sync-' + randomUUID();
  await mkdir(data, { mode: 0o700 }); await mkdir(run, { mode: 0o700 });
  t.after(async () => { await docker('rm', '-f', name).catch(() => {}); await rm(root, { recursive: true, force: true }); });
  const oldModel=platform?'hc-old-provider':'deepseek-flash', newModel=platform?'hc-new-provider':'deepseek-chat', provider=platform?'platform-models':'deepseek-official';
  const config = { schema_version: 1, instance_id: randomUUID(), org_id: randomUUID(), owner_id: randomUUID(), asset_id: randomUUID(), runtime_generation: randomUUID(), configuration_version: 1, public_url: 'https://d0.harness.test', endpoint: 'https://127.0.0.1:65530/anthropic', model: oldModel };
  if(platform){
    const gatewayIP=await docker('network','inspect','bridge','--format','{{(index .IPAM.Config 0).Gateway}}');
    const key=join(root,'gateway-key.pem'), cert=join(root,'gateway-cert.pem');
    await exec('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',key,'-out',cert,'-days','1','-subj','/CN=synthetic-gateway','-addext',`subjectAltName=IP:${gatewayIP}`]);
    const gateway=https.createServer({key:await readFile(key),cert:await readFile(cert)},(req,res)=>{
      if(req.method==='GET' && req.url==='/v1/models?scope=provider-account'){
        res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({object:'list',data:[{id:config.model,name:config.model,context_window:32768,max_output_tokens:1024,response_models:[config.model]}]}));
      }else{res.writeHead(503);res.end();}
    });
    await new Promise(resolve=>gateway.listen(0,'0.0.0.0',resolve));
    t.after(()=>{gateway.closeAllConnections();gateway.close();});
    Object.assign(config,{schema_version:2,provider,protocol:'anthropic-messages',route_revision:'test-v1',approval_digest:'sha256:'+'a'.repeat(64),context_window:32768,max_output_tokens:1024,response_models:[oldModel],approved_ips:[gatewayIP],approved_networks:[`${gatewayIP}/32`],ca_pem:await readFile(cert,'utf8'),endpoint:`https://${gatewayIP}:${gateway.address().port}`});
  }
  for (const [path, value] of [[join(data, '.hc-initialized'), JSON.stringify({...config,schema_version:1})], [join(run, 'config.json'), JSON.stringify(config)], [join(run, 'model-key'), 'synthetic-no-inference']]) await writeFile(path, value, { mode: 0o600 });
  await docker('run', '-d', '--name', name, '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--pids-limit', '256', '--memory', '2g', '--cpus', '1', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000', '--mount', `type=bind,src=${data},dst=/data`, '--mount', `type=bind,src=${run},dst=/run/harness`, '--mount', `type=bind,src=${run}/model-key,dst=/run/secrets/model-key,readonly`, process.env.HC_RUNTIME_IMAGE);
  const ready = async () => {
    for (let i=0;i<160;i++) {
      try { const value=JSON.parse(await readFile(join(run,'native.json'),'utf8')); if(value.runtime_generation===config.runtime_generation)return value; } catch {}
      await new Promise(resolve=>setTimeout(resolve,250));
    }
    assert.fail('runtime did not become ready');
  };
  let native=await ready();
  const ip=await docker('inspect','-f','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',name);
  const request=(path,method='GET',headers={},body) => new Promise((resolve,reject)=>{
    const req=http.request({hostname:ip,port:3080,path,method,headers:{Host:'d0.harness.test',...headers},timeout:15000},res=>{let raw='';res.on('data',chunk=>raw+=chunk);res.on('end',()=>resolve({status:res.statusCode,headers:res.headers,body:raw}));});
    req.on('timeout',()=>req.destroy(new Error('runtime timeout')));req.on('error',reject);req.end(body);
  });
  const exchange=async()=>{const r=await request('/?token='+encodeURIComponent(native.token));assert.equal(r.status,303);return r.headers['set-cookie'][0].split(';')[0];};
  let cookie=await exchange();
  const rpc=async(method,args)=>{const response=await request('/api/'+method,'POST',{Cookie:cookie,Origin:config.public_url,'Content-Type':'application/json'},JSON.stringify({type:'client-request',rpcId:randomUUID(),method,payload:{args}}));assert.equal(response.status,200);const result=JSON.parse(response.body).result;assert.equal(result.ok,true,JSON.stringify(result.error));return result.value;};
  const sessionId='harness-initial-'+config.instance_id;
  const waitForSelection=async expected=>{
    let last;
    for(let i=0;i<60;i++){
      const result=await rpc('session/list',{_request:{}});
      const item=result.items.find(row=>row.sessionId===sessionId);
      last=item && { modelSelection:item.projections?.values?.modelSelection, title:item.projections?.values?.title, formatStatus:item.formatStatus, agentAvailable:item.agentAvailable };
      if(item?.projections?.values?.modelSelection?.next?.model===expected)return result;
      await new Promise(resolve=>setTimeout(resolve,100));
    }
    assert.fail('session selection was not projected: '+JSON.stringify(last));
  };
  await rpc('session/rename',{request:{sessionId,title:'Synthetic retained session'}});
  await rpc('session/selectModel',{request:{sessionId,provider,model:oldModel}});
  await rpc('session/prompt',{request:{sessionId,requestId:randomUUID(),mode:'queue',content:[{type:'text',text:'Synthetic test message'}]}});
  let sessions=await waitForSelection(oldModel);
  assert.equal(sessions.items.find(item=>item.sessionId===sessionId).projections.values.modelSelection.next.model,oldModel);
  // The live projection is immediate; wait for the synthetic event to reach
  // the persistent session log before simulating a later provider update.
  const logDir=join(data,'dsh','sessions','--data-workspace--',sessionId);
  for(let i=0;i<60;i++){
    let persisted=false;
    try { const {stdout}=await exec('zstd',['-dc',join(logDir,'session.v4.jsonl.zstd')]); persisted=stdout.includes('model/selection') && stdout.includes(oldModel); } catch {}
    if(persisted)break;
    if(i===59)assert.fail('synthetic selection was not persisted');
    await new Promise(resolve=>setTimeout(resolve,100));
  }
  config.runtime_generation=randomUUID();config.configuration_version=2;config.model=newModel;if(platform)config.response_models=[newModel];
  await writeFile(join(run,'config.json'),JSON.stringify(config),{mode:0o600});
  await docker('restart',name);native=await ready();cookie=await exchange();
  sessions=await rpc('session/list',{_request:{}});
  assert.equal(sessions.items.find(item=>item.sessionId===sessionId).agentAvailable,false);
  const cold=await rpc('session/projections',{request:{sessionId}});
  assert.equal(cold.values.modelSelection.next.model,oldModel);
  const fixture=join(root,'fixture.json');
  await writeFile(fixture,JSON.stringify({Endpoint:`http://${ip}:3080`,Host:'d0.harness.test',Cookie:cookie,Provider:provider,Model:newModel}),{mode:0o600});
  await exec(process.env.HC_WORKER_SYNC_TEST_BINARY,['-test.run','^TestNativeSessionSyncFixture$','-test.v'],{env:{...process.env,HC_NATIVE_SYNC_FIXTURE:fixture},timeout:30000});
  sessions=await waitForSelection(newModel);
  const updated=sessions.items.find(item=>item.sessionId===sessionId);
  assert.equal(updated.projections.values.modelSelection.next.model,newModel);
  assert.equal(updated.cwd,'/data/workspace');
  const catalog=await rpc('session/modelCatalog',{});
  assert.equal(catalog.default.model,newModel);
  assert.ok(catalog.groups.some(group=>group.models.some(model=>model.id===newModel)));
  const proxy=http.createServer((incoming,outgoing)=>{
    const headers={...incoming.headers,host:'d0.harness.test',cookie,origin:config.public_url};
    const upstream=http.request({hostname:ip,port:3080,path:incoming.url,method:incoming.method,headers},response=>{outgoing.writeHead(response.statusCode,response.headers);response.pipe(outgoing);});
    upstream.on('error',()=>{if(!outgoing.headersSent)outgoing.writeHead(502);outgoing.end();});incoming.pipe(upstream);
  });
  const sockets=new Set();
  proxy.on('connection',socket=>{sockets.add(socket);socket.on('close',()=>sockets.delete(socket));});
  proxy.on('upgrade',(incoming,socket,head)=>{
    const headers={...incoming.headers,host:'d0.harness.test',cookie,origin:config.public_url};
    const upstream=http.request({hostname:ip,port:3080,path:incoming.url,method:incoming.method,headers});
    upstream.on('upgrade',(response,native,buffer)=>{socket.write(`HTTP/1.1 ${response.statusCode} ${response.statusMessage}\r\n`+response.rawHeaders.reduce((text,value,i,all)=>i%2?text+value+'\r\n':text+value+': ','')+'\r\n');if(head.length)native.write(head);if(buffer.length)socket.write(buffer);socket.pipe(native);native.pipe(socket);socket.on('error',()=>native.destroy());native.on('error',()=>socket.destroy());});
    upstream.on('error',()=>socket.destroy());upstream.end();
  });
  await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH});
  t.after(async()=>{await browser.close();proxy.closeAllConnections();for(const socket of sockets)socket.destroy();proxy.close();});
  for(const viewport of [{width:1440,height:900},{width:390,height:844}]){
    const page=await browser.newPage({locale:'zh-CN',viewport});
    await page.goto(`http://127.0.0.1:${proxy.address().port}/`,{waitUntil:'domcontentloaded'});
    const notice=page.getByRole('button',{name:/^(继续|Continue)$/});
    const savedSession=page.getByText('Synthetic retained session',{exact:true}).first();
    const sidebar=page.getByRole('button',{name:'打开侧边栏'});
    await Promise.any([notice.waitFor({state:'visible',timeout:15000}),savedSession.waitFor({state:'visible',timeout:15000}),sidebar.waitFor({state:'visible',timeout:15000})]);
    if(await notice.isVisible())await notice.click();
    if(await sidebar.isVisible())await sidebar.click();
    try { await page.getByText('Synthetic retained session',{exact:true}).first().click({timeout:15000}); }
    catch (error) { console.log('synthetic page:',await page.locator('body').innerText());throw error; }
    const selector=page.getByRole('button',{name:new RegExp('^选择模型，当前 '+newModel)});
    await selector.waitFor({state:'visible',timeout:20000});
    assert.ok((await selector.getAttribute('aria-label')).includes(newModel));
    await page.close();
  }
  // The next ordinary restart must retain the synchronized model.
  for(let i=0;i<60;i++){
    let persisted=false;
    try { const {stdout}=await exec('zstd',['-dc',join(logDir,'session.v4.jsonl.zstd')]);persisted=stdout.includes('model/selection') && stdout.includes(newModel); } catch {}
    if(persisted)break;
    if(i===59)assert.fail('new selection was not persisted');
    await new Promise(resolve=>setTimeout(resolve,100));
  }
  config.runtime_generation=randomUUID();
  await writeFile(join(run,'config.json'),JSON.stringify(config),{mode:0o600});
  await docker('restart',name);native=await ready();cookie=await exchange();
  const retained=await rpc('session/projections',{request:{sessionId}});
  assert.equal(retained.values.modelSelection.next.model,newModel);
});
