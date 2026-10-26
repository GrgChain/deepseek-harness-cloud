import { expect, test, type Page } from '@playwright/test';

const user = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'MEMBER', role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员', status: 'ACTIVE' }], permissions: ['instances:read', 'instances:start', 'instances:stop', 'instances:delete', 'instances:configure', 'instances:access', 'instances:retry', 'personal-model:read'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };
const id = '00000000-0000-4000-8000-000000000051';
const operationID = '00000000-0000-4000-8000-000000000052';
const routeAlias = 'hc-5935c6c06c6f48969de6e8c209a5529f-v1';
const health = { status: 'HEALTHY', checked_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', reason_code: '' };
const instance = { id, owner_id: user.id, name: '研发工作空间', observed_state: 'RUNNING', desired_state: 'RUNNING', ready: true, can_enter: true, allowed_actions: ['access', 'stop', 'configure', 'delete'], version: 1, active_operation_id: null, access_host: null, created_at: '2026-10-15T00:00:00Z', updated_at: '2026-10-15T01:00:00Z', retention_until: null, storage: { capacity_bytes: 10 * 2 ** 30, status: 'READY' }, model_display_name: 'deepseek-flash', model_provider_name: 'DeepSeek', config_snapshot: { model: routeAlias, provider: 'platform-models', model_configuration_version: 1, spec: { id: 'spec', cpu_millicores: 1000, memory_bytes: 2 * 2 ** 30, storage_bytes: 10 * 2 ** 30 } }, health: { application: health, storage: health, configuration: health, access: health, model: health } };
async function session(page: Page, data: unknown = instance, permissions = user.permissions) {
 await page.emulateMedia({ reducedMotion: 'reduce' });
 await page.route('**/v1/**', r => r.fulfill({ json: { items: [], next_cursor: null } }));
 await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
 await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
 await page.route('**/v1/me', r => r.fulfill({ json: { ...user, permissions } }));
 await page.route('**/v1/instances/' + id, r => r.fulfill({ json: data }));
}

test('details prioritize resources, real model and actions with diagnostics collapsed', async ({ page }, info) => {
 await session(page);
 await page.goto('/app/instances/' + id);
 await expect(page.getByRole('heading', { name: instance.name, exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeEnabled();
 await expect(page.getByRole('region', { name: '运行资源', exact: true })).toContainText('CPU1核');
 const models = page.getByRole('region', { name: '模型配置', exact: true });
 await expect(models).toContainText('DeepSeek');
 await expect(models).toContainText('deepseek-flash');
 await expect(page.getByText(routeAlias, { exact: true })).toHaveCount(0);
 await expect(page.getByText('实例编号', { exact: true })).toHaveCount(0);
 for (const width of [375, 768, 1024, 1440]) {
  await page.setViewportSize({ width, height: 1000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (width === 375 || width === 1440) await page.screenshot({ path: info.outputPath('instance-detail-' + width + '.png'), fullPage: true, animations: 'disabled' });
 }
 await page.getByRole('button', { name: '运行检查', exact: true }).click();
 await expect(page.locator('.instance-health-row')).toHaveCount(5);
 await page.getByRole('button', { name: '实例信息', exact: true }).click();
 await expect(page.getByText(routeAlias, { exact: true })).toBeVisible();
 await expect(page.getByText(id, { exact: true })).toBeVisible();
 await page.setViewportSize({ width: 375, height: 812 });
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
 await expect(page.getByRole('button', { name: '打开模型提供商', exact: true })).toHaveCount(0);
});

test('a missing display name does not expose a route alias or invent a model', async ({ page }) => {
 await session(page, { ...instance, model_display_name: '', model_provider_name: '' });
 await page.goto('/app/instances/' + id);
 await expect(page.getByRole('region', { name: '模型配置', exact: true })).toContainText('模型信息暂不可用');
 await expect(page.getByText(routeAlias, { exact: true })).toHaveCount(0);
});

test('stopped read-only details show the actual state and cannot enter or change the instance', async ({ page }) => {
 await session(page, { ...instance, observed_state: 'STOPPED', desired_state: 'STOPPED', can_enter: false, ready: false, allowed_actions: [] }, ['instances:read']);
 await page.goto('/app/instances/' + id);
 await expect(page.getByText('已停止', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeDisabled();
 await expect(page.getByRole('button', { name: '停止实例', exact: true })).toHaveCount(0);
 await expect(page.getByRole('button', { name: '打开模型提供商', exact: true })).toHaveCount(0);
 await expect(page.getByText('启动实例后即可进入工作空间。', { exact: true })).toBeVisible();
});

test('pending operation shows translated status and the actual execution stage', async ({ page }) => {
 await session(page, { ...instance, active_operation_id: operationID, observed_state: 'STARTING', ready: false, can_enter: false, allowed_actions: [] });
 await page.route('**/v1/operations/' + operationID, r => r.fulfill({ json: { id: operationID, instance_id: id, type: 'START', status: 'RUNNING', stage: 'BUILDING', error: null, retry_allowed: false, updated_at: instance.updated_at } }));
 await page.goto('/app/instances/' + id);
 const operation = page.getByRole('region', { name: '最近操作', exact: true });
 await expect(operation).toContainText('启动实例');
 await expect(operation).toContainText('处理中');
 await expect(operation).toContainText('构建运行环境');
 await expect(operation).not.toContainText('BUILDING');
 await expect(page.getByRole('button', { name: '进入工作空间', exact: true })).toBeDisabled();
 await expect(page.getByRole('button', { name: '重试失败操作', exact: true })).toHaveCount(0);
});

test('failed operation shows its safe error and preserves the recovery action', async ({ page }) => {
 await session(page, { ...instance, latest_operation_id: operationID, observed_state: 'ERROR', ready: false, can_enter: false, allowed_actions: [] });
 await page.route('**/v1/operations/' + operationID, r => r.fulfill({ json: { id: operationID, instance_id: id, type: 'START', status: 'FAILED', stage: 'COMPLETE', error: { code: 'DEPENDENCY_UNAVAILABLE', message: '依赖暂不可用', request_id: 'safe-trace' }, retry_allowed: true, updated_at: instance.updated_at } }));
 await page.goto('/app/instances/' + id);
 await expect(page.getByRole('region', { name: '最近操作', exact: true })).toContainText('失败');
 await expect(page.getByText('依赖暂不可用', { exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '重试失败操作', exact: true })).toBeEnabled();
});

test('a failed progress read supports retry without claiming completion', async ({ page }) => {
 await session(page, { ...instance, latest_operation_id: operationID });
 await page.route('**/v1/operations/' + operationID, r => r.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }));
 await page.goto('/app/instances/' + id);
 await expect(page.getByText('操作进度暂不可用', { exact: true })).toBeVisible();
 await expect(page.getByRole('region', { name: '最近操作', exact: true })).toHaveCount(0);
 await expect(page.getByRole('button', { name: /^重\s*试$/ })).toBeEnabled();
});

test('an error instance can request deletion with its name and keeps cleanup pending', async ({ page }) => {
 let deleting=false;
 const failed={...instance,observed_state:'ERROR',ready:false,can_enter:false,allowed_actions:['delete'],model_display_name:'',model_provider_name:''};
 await session(page,failed);
 await page.route('**/v1/instances/'+id,async route=>{
  if(route.request().method()==='DELETE'){
   expect(route.request().headers()['x-confirm-instance-name']).toBe(encodeURIComponent(instance.name));
   expect(route.request().headers()['x-csrf-token']).toBe(user.csrf_token);
   expect(route.request().headers()['idempotency-key']).toBeTruthy();
   deleting=true;await route.fulfill({status:202,json:{instance_id:id,operation_id:operationID,state:'DELETING'}});
  }else await route.fulfill({json:deleting?{...failed,observed_state:'DELETING',desired_state:'DELETED',allowed_actions:[],active_operation_id:operationID,latest_operation_id:operationID}:failed});
 });
 await page.route('**/v1/operations/'+operationID,route=>route.fulfill({json:{id:operationID,instance_id:id,type:'DELETE',status:'ACCEPTED',stage:'QUEUED',error:null,retry_allowed:false,updated_at:instance.updated_at}}));
 await page.goto('/app/instances/'+id);
 await expect(page.getByRole('button',{name:'删除实例',exact:true})).toBeEnabled();
 await expect(page.getByRole('button',{name:'启动实例',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'删除实例',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'删除实例',exact:true});
 await expect(dialog.getByRole('button',{name:'确认删除',exact:true})).toBeDisabled();
 await dialog.getByLabel('确认实例名称').fill(instance.name);
 await expect(dialog).toContainText('工作数据按保留策略保存');
 await dialog.getByRole('button',{name:'确认删除',exact:true}).click();
 await expect(dialog).not.toBeVisible();
 await expect(page.getByRole('button',{name:'删除实例',exact:true})).toHaveCount(0);
 await expect(page.getByRole('region',{name:'最近操作',exact:true})).toContainText('已受理');
 expect(deleting).toBe(true);
});
