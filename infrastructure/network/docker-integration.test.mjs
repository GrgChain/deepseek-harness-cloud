import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';
import http from 'node:http';
const exec = promisify(execFile);
const docker = async (...args) => (await exec('docker', args, { timeout: 20000, maxBuffer: 1024 * 1024 })).stdout.trim();
test('real bridge fences block new host/control-plane connections and preserve public TLS', { skip: !process.env.HC_NETWORK_IMAGE || !process.env.HC_RUNTIME_IMAGE, timeout: 90000 }, async t => {
  const instance = randomUUID(), suffix = instance.replaceAll('-', '').slice(0, 11), network = 'hc-network-test-' + instance, bridge = 'hci-' + suffix, name = network;
  const config = JSON.stringify({ schema_version: 1, instance_id: instance });
  const helper = action => new Promise((resolve, reject) => {
    const child = spawn('docker', ['run', '--rm', '-i', '--network', 'host', '--cap-drop', 'ALL', '--cap-add', 'NET_ADMIN', '--read-only', process.env.HC_NETWORK_IMAGE, action], { stdio: ['pipe', 'pipe', 'pipe'] });
    let out = ''; child.stdout.on('data', b => { out += b; }); child.stderr.resume(); child.on('error', reject); child.on('exit', code => code === 0 ? resolve(JSON.parse(out)) : reject(new Error('Network helper rejected fence'))); child.stdin.end(config);
  });
  await docker('network', 'create', '--driver', 'bridge', '--opt', 'com.docker.network.bridge.name=' + bridge, '--opt', 'com.docker.network.bridge.enable_icc=false', network);
  const gateway = JSON.parse(await docker('network', 'inspect', network))[0].IPAM.Config[0].Gateway;
  const server = http.createServer((_, response) => response.end('private-control-plane'));
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, gateway, resolve); });
  t.after(async () => { await docker('rm', '-f', name).catch(() => {}); await helper('remove'); await new Promise(resolve => server.close(resolve)); await docker('network', 'rm', network); });
  const port = server.address().port; assert.ok(port > 10000);
  await docker('run', '-d', '--name', name, '--network', network, '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--entrypoint', 'node', process.env.HC_RUNTIME_IMAGE, '-e', 'setInterval(()=>{},10000)');
  const fetchPrivate = `fetch('http://${gateway}:${port}',{signal:AbortSignal.timeout(2000)}).then(r=>{console.log(r.status)}).catch(()=>{console.log('blocked')})`;
  assert.equal(await docker('exec', name, 'node', '-e', fetchPrivate), '200', 'Fixture must prove the private host endpoint was reachable before fencing');
  await assert.rejects(helper('inspect'), /rejected/);
  assert.equal((await helper('install')).isolated, true);
  assert.deepEqual(await helper('inspect'), await helper('install'));
  assert.equal(await docker('exec', name, 'node', '-e', fetchPrivate), 'blocked');
  const publicTLS = "const tls=require('tls');const s=tls.connect({host:'api.deepseek.com',port:443,servername:'api.deepseek.com',family:4},()=>{console.log('public-tls');s.end()});s.setTimeout(8000,()=>{s.destroy();process.exitCode=1});s.on('error',()=>{process.exitCode=1})";
  assert.equal(await docker('exec', name, 'node', '-e', publicTLS), 'public-tls');
});

test('gateway fence permits public downloads while blocking private hosts and unapproved ports', {skip: !process.env.HC_NETWORK_GATEWAY_POLICY_FILE || !process.env.HC_NETWORK_IMAGE || !process.env.HC_RUNTIME_IMAGE,timeout:60000},async t=>{
  const {readFile}=await import('node:fs/promises');
  const policy=JSON.parse(await readFile(process.env.HC_NETWORK_GATEWAY_POLICY_FILE,'utf8'));
  const endpoint=new URL(policy.endpoint),instance=randomUUID(),network='hc-network-gateway-test-'+instance,name=network,bridge='hci-'+instance.replaceAll('-','').slice(0,11);
  const config=JSON.stringify({schema_version:2,instance_id:instance,gateway:{addresses:policy.addresses,port:Number(endpoint.port)}});
  const helper=action=>new Promise((resolve,reject)=>{
    const child=spawn('docker',['run','--rm','-i','--network','host','--cap-drop','ALL','--cap-add','NET_ADMIN','--read-only',process.env.HC_NETWORK_IMAGE,action],{stdio:['pipe','pipe','pipe']});
    let out='';child.stdout.on('data',b=>{out+=b});child.stderr.resume();child.on('error',reject);child.on('exit',code=>code===0?resolve(JSON.parse(out)):reject(new Error('Gateway fence rejected')));child.stdin.end(config);
  });
  await docker('network','create','--driver','bridge','--opt','com.docker.network.bridge.name='+bridge,'--opt','com.docker.network.bridge.enable_icc=false',network);
  const gateway=JSON.parse(await docker('network','inspect',network))[0].IPAM.Config[0].Gateway;
  const privateServer=http.createServer((_,response)=>response.end('private-control-plane'));
  const wrongPortServer=http.createServer((_,response)=>response.end('unapproved-port'));
  await new Promise((resolve,reject)=>{privateServer.once('error',reject);privateServer.listen(0,gateway,resolve)});
  await new Promise((resolve,reject)=>{wrongPortServer.once('error',reject);wrongPortServer.listen(0,endpoint.hostname,resolve)});
  t.after(async()=>{
    await docker('rm','-f',name).catch(()=>{});
    try {await helper('remove')} finally {
      await Promise.all([new Promise(resolve=>privateServer.close(resolve)),new Promise(resolve=>wrongPortServer.close(resolve))]);
      await docker('network','rm',network);
    }
  });
  assert.ok(privateServer.address().port>10000 && wrongPortServer.address().port>10000);
  await docker('run','-d','--name',name,'--network',network,'--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--mount','type=bind,src='+policy.ca_file+',dst=/gateway-ca.pem,readonly','--entrypoint','node',process.env.HC_RUNTIME_IMAGE,'-e','setInterval(()=>{},10000)');
  const probeTLS=`const https=require('https'),fs=require('fs');const r=https.get(${JSON.stringify(policy.endpoint+'/readyz')},{ca:fs.readFileSync('/gateway-ca.pem'),timeout:4000},response=>{console.log(response.statusCode);response.resume()});r.on('timeout',()=>r.destroy());r.on('error',error=>console.log('blocked:'+error.code))`;
  const probePrivate=`fetch('http://${gateway}:${privateServer.address().port}',{signal:AbortSignal.timeout(2000)}).then(r=>console.log(r.status)).catch(()=>console.log('blocked'))`;
  const wrongPort=`fetch(${JSON.stringify('http://'+endpoint.hostname+':'+wrongPortServer.address().port)},{signal:AbortSignal.timeout(2000)}).then(r=>console.log(r.status)).catch(()=>console.log('blocked'))`;
  const publicDownload=`fetch('https://stockbuddy.qq.com/release/clawhub/cli/v0.0.2/SHA256.txt',{signal:AbortSignal.timeout(8000)}).then(async r=>console.log(r.ok && (await r.text()).includes('westock-linux-amd64')?'public-download':'download-failed')).catch(()=>console.log('blocked'))`;
  assert.equal(await docker('exec',name,'node','-e',probeTLS),'200','The private host TLS endpoint must be reachable before applying the fence');
  assert.equal(await docker('exec',name,'node','-e',probePrivate),'200');
  assert.equal(await docker('exec',name,'node','-e',wrongPort),'200');
  assert.equal(await docker('exec',name,'node','-e',publicDownload),'public-download','The official WeStock download must be reachable before fencing');
  assert.equal((await helper('install')).isolated,true);
  assert.deepEqual(await helper('inspect'),await helper('install'));
  assert.equal(await docker('exec',name,'node','-e',probeTLS),'200');
  assert.equal(await docker('exec',name,'node','-e',probePrivate),'blocked');
  assert.equal(await docker('exec',name,'node','-e',wrongPort),'blocked');
  assert.equal(await docker('exec',name,'node','-e',publicDownload),'public-download','A model gateway configuration must not disable public HTTPS downloads');
});
