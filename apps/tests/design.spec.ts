import { expect, test, type Page } from '@playwright/test';

const user = {
  id: '00000000-0000-4000-8000-000000000041',
  memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员', status: 'ACTIVE' }],
  permissions: ['instances:read', 'instances:create', 'instances:start', 'configuration:read', 'personal-model:read', 'personal-model:write', 'users:read', 'roles:read'],
  auth_generation: 1, csrf_token: 'synthetic-csrf-token-32-characters-long', session_expires_at: '2099-01-01T00:00:00Z',
};
const health = { status: 'HEALTHY', checked_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', reason_code: '' };
const model = { id: 'model-one', name: '团队开发模型', provider: 'moonshot', model: 'kimi-k2.5', version: 1, catalog_entry_id: 'catalog', enabled: true, credential_configured: true, sync_status: 'SYNCED', reason_code: '', validation: health, gateway_check: health };
const instances = ['开发工作空间', '数据分析环境', 'long-workspace-name-without-spaces-for-responsive-layout'].map((name, index) => ({
  id: 'instance-' + index, owner_id: user.id, name, observed_state: index === 1 ? 'STOPPED' : 'RUNNING', desired_state: 'RUNNING', ready: index !== 1, can_enter: false, allowed_actions: [],
  config_snapshot: { model: 'kimi-k2.5', model_configuration_version: 1, spec: { cpu_millicores: 2000, memory_bytes: 4 * 2 ** 30, storage_bytes: 10 * 2 ** 30 } },
  health: { application: health, storage: health, configuration: health, access: health, model: health }, updated_at: '2026-10-14T01:00:00Z',
}));

async function setup(page: Page) {
  await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', route => route.fulfill({ json: user }));
  await page.route('**/v1/preflight*', route => route.fulfill({ json: { can_create: true, checks: [] } }));
  await page.route('**/v1/platform-checks', route => route.fulfill({ json: { ready: true, checks: [] } }));
  await page.route('**/v1/my-models', route => route.fulfill({ json: { items: [model] } }));
  await page.route('**/v1/my-provider-accounts', route => route.fulfill({json:{items:[]}}));
  await page.route('**/v1/my-model-default', route => route.fulfill({ json: null }));
  await page.route('**/v1/model-providers', route => route.fulfill({ json: { items: [{ id: 'moonshot', name: 'Kimi', api_bases: ['https://api.moonshot.ai/v1'], version: 1 }] } }));
  await page.route('**/v1/instances?*', route => {
    const params = new URL(route.request().url()).searchParams;
    const items = instances.filter(item => (!params.get('state') || item.observed_state === params.get('state')) && (!params.get('q') || item.name.startsWith(params.get('q')!)));
    return route.fulfill({ json: { items, next_cursor: null } });
  });
}

test('responsive workspace layout keeps long names and filtering operable', async ({ page }, testInfo) => {
  await setup(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/app/instances');
  for (const width of [375, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(page.getByRole('button', { name: '实例详情', exact: true })).toHaveCount(3);
    await expect(page.getByRole('button', { name: '开发工作空间', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    if (width === 375 || width === 1440) await page.screenshot({ path: testInfo.outputPath(`workspace-${width}.png`), fullPage: true });
  }
  await page.getByText('已停止', { exact: true }).first().click();
  await expect(page.getByRole('button', { name: '实例详情', exact: true })).toHaveCount(1);
  await expect(page.getByRole('button', { name: '数据分析环境', exact: true })).toBeVisible();
  const skip = page.getByRole('link', { name: '跳至主要内容', exact: true });
  await skip.focus();
  await expect(skip).toBeVisible();
  await skip.press('Enter');
  await expect(page.getByRole('main')).toBeFocused();
});

test('model drawer clears cancelled credentials and never submits on open or close', async ({ page }, testInfo) => {
  await setup(page);
  let writes = 0;
  await page.route('**/v1/my-provider-accounts', route => {
    if (route.request().method() !== 'GET') writes++;
    return route.fulfill({ json: { items: [] } });
  });
  await page.goto('/app/models');
  await expect(page.getByLabel('API Key', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: '添加账号', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '添加账号', exact: true });
  await expect(dialog).toBeVisible();
  await page.getByLabel('名称', { exact: true }).fill('取消的草稿');
  await page.getByLabel('提供商',{exact:true}).locator('..').locator('..').click();
  await page.locator('.ant-select-dropdown:visible').getByTitle('Moonshot AI',{exact:true}).click();
  await page.getByLabel('API Key', { exact: true }).fill('synthetic-cancelled-key');
  await dialog.getByRole('button', { name: /^取\s*消$/ }).click();
  await expect(dialog).toHaveCount(0);
  await page.getByRole('button', { name: '添加账号', exact: true }).click();
  await expect(page.getByLabel('名称', { exact: true })).toHaveValue('');
  await page.getByLabel('提供商',{exact:true}).locator('..').locator('..').click();
  await page.locator('.ant-select-dropdown:visible').getByTitle('Moonshot AI',{exact:true}).click();
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('API Key', { exact: true })).toBeInViewport();
  await expect(dialog.getByRole('button', { name: /^取\s*消$/ })).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath('model-drawer.png'), animations: 'disabled' });
  expect(writes).toBe(0);
  expect(await page.evaluate(() => JSON.stringify({ localStorage, sessionStorage }))).not.toContain('synthetic-cancelled-key');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
