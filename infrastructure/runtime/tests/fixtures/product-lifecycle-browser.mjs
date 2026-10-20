import { chromium, expect } from '../../../../apps/node_modules/@playwright/test/index.mjs';
import { readPrivate } from '../../private-file.mjs';
import { writeFile,chmod } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const exec = promisify(execFile);
if (process.env.HC_PRODUCT_INTEGRATION !== 'true') throw new Error('Explicit local product acceptance required');
const portal = 'https://portal.harness.localhost:42013';
const fixture = JSON.parse(await readPrivate(process.env.HC_PRODUCT_INSTANCE_FILE ?? '.local/product-instance.json'));
if (!/^[a-f0-9-]{36}$/.test(fixture.instance_id)) throw new Error('Controlled instance fixture required');
let browser,context,page,nativePage,stage = 'login';
let verifiedDefault;
const evidenceFile=process.env.HC_PRODUCT_LIFECYCLE_EVIDENCE_FILE ?? '.local/product-lifecycle-evidence.json';
const diagnostics=[];
const historyDiagnostics=[];
await writeFile(evidenceFile,JSON.stringify({outcome:'IN_PROGRESS',instance_id:fixture.instance_id}),{mode:0o600});
async function completedHistory(popup, file, selectedSession) {
 const rows=popup.locator('[role="treeitem"][data-row-key^="session:"]');
 await expect(rows.first()).toBeVisible({timeout:30_000});
 const keys=selectedSession?[selectedSession]:[...new Set(await rows.evaluateAll(nodes=>nodes.map(node=>node.getAttribute('data-row-key'))))];
 if(keys.length>20||keys.some(key=>!/^session:[A-Za-z0-9_-]{1,128}$/.test(key)))throw new Error('Controlled session history census exceeded');
 for(const key of keys){
  const row=popup.locator('[role="treeitem"][data-row-key='+JSON.stringify(key)+']').first();
  if(await row.count()===0)continue;
  // Welcome readiness resolves after the shell renders. A visible Settings
  // button alone does not prove the late notice has finished deciding.
  await expect(async()=>{
   const notice=popup.getByRole('button',{name:'继续',exact:true});
   if(await notice.isVisible())await notice.click();
   await row.click({timeout:2000});
  }).toPass({timeout:20_000,intervals:[500,1000]});
  await expect(row).toHaveAttribute('aria-selected','true');
  try {
   await expect(popup.getByText('HC_BROWSER_TASK_OK',{exact:true})).toBeVisible({timeout:2500});
   await expect(popup.getByText(file,{exact:false}).first()).toBeVisible();
   if(!await popup.locator('[data-tool]').first().isVisible())await popup.getByText(/已完成，用时/).first().click();
   const steps=popup.locator('[data-step-process] button[aria-expanded="false"]');
   for(let remaining=0;remaining<10&&await steps.count()>0;remaining++)await steps.first().click();
   await expect(popup.locator('[data-tool]').first()).toBeVisible();
   const tools=await popup.locator('[data-tool]').evaluateAll(nodes=>nodes.map(node=>node.getAttribute('data-tool')));
   expect(tools).toEqual(expect.arrayContaining(['write','read']));
   return {completed_answer_visible:true,original_file_task_visible:true,tool_history_visible:true,tool_names:tools,selected_session:key};
  } catch {
   historyDiagnostics.push(await popup.evaluate(({file,key})=>({key,expected_file_in_dom:document.body.innerText.includes(file),answer_marker_in_dom:document.body.innerText.includes('HC_BROWSER_TASK_OK'),tool_rows:document.querySelectorAll('[data-tool]').length,selected:document.querySelector('[data-row-key^="session:"][aria-selected="true"]')?.getAttribute('data-row-key')}),{file,key}));
  }
 }
 await popup.screenshot({path:'.local/product-history-failure.png'});await chmod('.local/product-history-failure.png',0o600);
 throw new Error('Completed synthetic conversation was not readable');
}
try {
 browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, args: ['--no-proxy-server'] });
 context = await browser.newContext({ ignoreHTTPSErrors: true });
 page = await context.newPage();
 page.setDefaultTimeout(30_000);
 page.setDefaultNavigationTimeout(45_000);
 page.on('requestfailed',request=>{
  const path=new URL(request.url()).pathname;
  if(path.endsWith(':access')||path==='/__dsh/challenge')diagnostics.push({endpoint:path.endsWith(':access')?'access':'challenge',network_error:request.failure()?.errorText});
 });
 page.on('response',async response=>{
  const path=new URL(response.url()).pathname;
  if(response.status()>=400 && (path.endsWith(':access')||path.endsWith(':start')||path==='/__dsh/challenge')){
   const body=await response.json().catch(()=>({}));
   diagnostics.push({endpoint:path.endsWith(':access')?'access':path.endsWith(':start')?'start':'challenge',status:response.status(),code:typeof body.code==='string'?body.code:undefined});
  }
 });
 await page.goto(portal + '/auth/login');
 await page.locator('input[name=login]').fill('member@harness.local');
 await page.locator('input[name=password]').fill((await readPrivate('.local/auth/member-password')).trim());
 await page.getByRole('button', { name: 'Login', exact: true }).click();
 await page.waitForURL(portal + '/app/instances',{timeout:45_000});
 if(process.env.HC_EXPECTED_DEFAULT_TEMPLATE_VERSION){
  const expected=process.env.HC_EXPECTED_DEFAULT_TEMPLATE_VERSION;
  if(!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(expected))throw new Error('Controlled expected default version required');
  const response=await context.request.get(portal+'/v1/defaults');
  expect(response.status()).toBe(200);
  const defaults=await response.json();
  expect(defaults.template_version).toBe(expected);
  verifiedDefault=expected;
 }
 await page.goto(portal + '/app/instances/' + fixture.instance_id);
 const initial = await (await context.request.get(portal + '/v1/instances/' + fixture.instance_id)).json();
 if (initial.observed_state === 'STOPPED') {
  stage = 'member-start-frozen-runtime';
  await page.getByRole('button', { name: '启动实例', exact: true }).click();
 }
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeEnabled({ timeout: 120_000 });
 stage='native-browser-handoff';
 const popupEvent = page.waitForEvent('popup');
 await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
 const popup = await popupEvent;
 nativePage=popup;
 await popup.waitForURL(url => url.hostname.startsWith('i-') && url.pathname === '/');
 const introduction=popup.getByRole('button',{name:'继续',exact:true});
 await expect(introduction.or(popup.getByRole('button',{name:'设置',exact:true})).first()).toBeVisible({timeout:20_000});
 if(await introduction.isVisible()){await introduction.click();await expect(introduction).not.toBeVisible();}
 await expect(popup.getByRole('button', { name: '设置', exact: true })).toBeVisible();
 stage = 'member-stop';
 const detailsResponse = await context.request.get(portal + '/v1/instances/' + fixture.instance_id);
 expect(detailsResponse.status()).toBe(200);
 const before = await detailsResponse.json();
 let fileProof,historyProof;
 if(process.env.HC_PRODUCT_FILE_PROOF_FILE){
  const original=JSON.parse(await readPrivate(process.env.HC_PRODUCT_FILE_PROOF_FILE));
  if(original.outcome!=='PASS'||original.instance_id!==fixture.instance_id||!/^hc-browser-smoke-[a-f0-9-]{36}\.txt$/.test(original.file))throw new Error('Controlled generated file proof required');
  const script="const f=require('fs'),c=require('crypto');const b=f.readFileSync('/data/workspace/'+process.argv[1]);process.stdout.write(JSON.stringify({sha256:c.createHash('sha256').update(b).digest('hex'),bytes:b.length}));";
  fileProof=JSON.parse((await exec('docker',['exec','hc-'+fixture.instance_id,'node','-e',script,original.file],{timeout:5000})).stdout);
  expect(fileProof).toEqual(original.file_proof);
  if(process.env.HC_PRODUCT_HISTORY_PROOF==='true'){
   stage='completed-native-history-after-restart';
   const previous=JSON.parse(await readPrivate(process.env.HC_PRODUCT_HISTORY_PROOF_FILE??'.local/product-history-evidence.json'));
   const selectedSession=previous.completed_history?.selected_session;
   if(previous.outcome!=='PASS'||previous.instance_id!==fixture.instance_id||!/^session:[A-Za-z0-9_-]{1,128}$/.test(selectedSession??''))throw new Error('Controlled completed-session proof required');
   historyProof=await completedHistory(popup,original.file,selectedSession);
   await popup.reload();
   historyProof.after_refresh=await completedHistory(popup,original.file,selectedSession);
  }
 }
 await page.getByRole('button', { name: '停止实例', exact: true }).click();
 await expect(page.getByRole('button', { name: '启动实例', exact: true })).toBeVisible({ timeout: 90_000 });
 const stoppedResponse = await context.request.get(portal + '/v1/instances/' + fixture.instance_id);
 const stopped = await stoppedResponse.json();
 expect(stopped.observed_state).toBe('STOPPED');
 expect(stopped.config_snapshot).toEqual(initial.config_snapshot);
 expect(before.config_snapshot.image_digest).toBe(initial.config_snapshot.image_digest);
 expect(stopped.can_enter).toBe(false);
 expect(stopped.storage.capacity_bytes).toBe(before.storage.capacity_bytes);
 const me = await (await context.request.get(portal + '/v1/me')).json();
 await context.request.post(portal + '/auth/logout', { headers: { Origin: portal, 'X-CSRF-Token': me.csrf_token } });
 await writeFile(evidenceFile, JSON.stringify({ outcome: 'PASS', instance_id: fixture.instance_id, real_member_stop: true, original_runtime_preserved: true, started_from_stopped: initial.observed_state === 'STOPPED', gateway_entry_revoked: true, retained_capacity_bytes: stopped.storage.capacity_bytes, paid_model_task_requests: 0, file_retained_after_restart:!!fileProof, file_proof:fileProof,completed_history:historyProof,verified_default_template_version:verifiedDefault }), { mode: 0o600 });
 await context.close();
 context=undefined;
} catch (error) {
 if(page)await page.screenshot({path:'.local/rollback-instance-failure.png'}).then(()=>chmod('.local/rollback-instance-failure.png',0o600)).catch(()=>{});
 if(nativePage&&stage==='completed-native-history-after-restart'){
  await nativePage.screenshot({path:'.local/rollback-native-history-failure.png'}).then(()=>chmod('.local/rollback-native-history-failure.png',0o600)).catch(()=>{});
 }
 await writeFile('.local/product-task-diagnostic.txt', String(error?.stack ?? error), { mode: 0o600 });
 await writeFile(evidenceFile,JSON.stringify({outcome:'FAIL',stage,instance_id:fixture.instance_id,diagnostics,history_diagnostics:historyDiagnostics}),{mode:0o600});
 console.error('Native browser acceptance failed at stage: ' + stage); process.exitCode = 1;
} finally {
 if(context&&page){
  try {
   await page.goto(portal+'/app/instances/'+fixture.instance_id);
   const response=await context.request.get(portal+'/v1/instances/'+fixture.instance_id);
   if(response.ok()){
    const current=await response.json();
    if(current.observed_state!=='STOPPED'&&current.observed_state!=='DELETED'){
     const stop=page.getByRole('button',{name:'停止实例',exact:true});
     await expect(stop).toBeEnabled({timeout:10_000});await stop.click();
     await expect(page.getByRole('button',{name:'启动实例',exact:true})).toBeVisible({timeout:90_000});
    }
    const me=await(await context.request.get(portal+'/v1/me')).json();
    await context.request.post(portal+'/auth/logout',{headers:{Origin:portal,'X-CSRF-Token':me.csrf_token}});
   }
  } catch {console.error('Explicit product fixture cleanup needs review');process.exitCode=1;}
 }
 await browser?.close();
}
