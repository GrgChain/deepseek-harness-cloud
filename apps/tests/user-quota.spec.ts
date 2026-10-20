import { expect, test, type Page } from '@playwright/test';

const GiB = 1024 ** 3;
const admin = {
  id: '00000000-0000-4000-8000-000000000021',
  memberships: [{ org_id: '00000000-0000-4000-8000-000000000020', role: 'ADMIN', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员', status: 'ACTIVE' }],
  permissions: ['users:read', 'users:manage', 'roles:read', 'instances:read'],
  csrf_token: 'synthetic-quota-csrf-token-32-characters', auth_generation: 1, session_expires_at: '2099-01-01T00:00:00Z',
};
const target = { id: '00000000-0000-4000-8000-000000000022', display_name: '研发成员', email: 'developer@example.com', status: 'ACTIVE', identity_source: 'oidc', role_name: '成员', version: 1, last_login_at: null };
const endpoint = '/v1/users/' + target.id + ':quota';
const defaults = { max_instances: 2, cpu_millicores: 10000, memory_bytes: 20 * GiB, storage_bytes: 100 * GiB };
const quota = { owner_id: target.id, version: 0, configured: false, limits: defaults, usage: { max_instances: 1, cpu_millicores: 2000, memory_bytes: 4 * GiB, storage_bytes: 10 * GiB }, organization_limits: { ...defaults, max_instances: 10 } };

async function session(page: Page, permissions = admin.permissions) {
  // All API traffic is isolated: no live account or quota mutations.
  await page.route('**/v1/**', r => r.fulfill({ json: { items: [] } }));
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: { ...admin, permissions } }));
  await page.route('**/v1/users?*', r => r.fulfill({ json: { items: [target], next_cursor: null, local_accounts: false } }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
}
async function open(page: Page) {
  await page.goto('/app/quotas');
  await page.getByRole('button', { name: /^(调整配额|查看配额)$/ }).click();
  return page.getByRole('dialog', { name: '资源配额', exact: true });
}

test('quota editor displays commitments and saves all native units with expected version', async ({ page }, info) => {
  await page.clock.install();
  await session(page);
  let saved = false;
  await page.route('**' + endpoint, async r => {
    if (r.request().method() === 'GET') return r.fulfill({ json: quota });
    const input = r.request().postDataJSON();
    expect(await r.request().headerValue('x-csrf-token')).toBe(admin.csrf_token);
    expect(input).toEqual({ expected_version: 0, limits: { max_instances: 3, cpu_millicores: 4000, memory_bytes: 8 * GiB, storage_bytes: 30 * GiB } });
    saved = true;
    return r.fulfill({ json: { ...quota, configured: true, version: 1, limits: input.limits } });
  });
  const dialog = await open(page);
  await expect(dialog.getByLabel('同时运行数量上限（个）', { exact: true })).toHaveCount(0);
  await expect(dialog.getByRole('spinbutton')).toHaveCount(4);
  await expect(dialog.getByText('继承组织默认', { exact: true })).toBeVisible();
  await expect(dialog.getByText('已占用 10 / 100 GiB', { exact: true })).toBeVisible();
  for (const [label, value] of [['实例数量上限（个）', '3'], ['CPU上限（vCPU）', '4'], ['内存上限（GiB）', '8'], ['持久存储上限（GiB）', '30']]) await dialog.getByLabel(label, { exact: true }).fill(value);
  await expect(dialog).toBeInViewport();
  await dialog.locator('.ant-modal-body').evaluate(element => { element.scrollTop = 0; });
  await page.screenshot({ path: info.outputPath('user-quota.png'), fullPage: true, animations: 'disabled' });
  await dialog.getByRole('button', { name: '保存配额', exact: true }).click();
  await expect(page.getByText('资源配额已保存，将用于后续资源申请', { exact: true })).toBeVisible();
  expect(saved).toBe(true);
  const row = page.getByRole('row').filter({ hasText: target.email });
  await expect(row.getByText('单独分配', { exact: true })).toBeVisible();
  await expect(row.getByLabel('CPU已占用 2 / 上限 4 vCPU', { exact: true })).toBeVisible();
  await page.clock.fastForward(4500);
  await expect(page.getByText('资源配额已保存，将用于后续资源申请', { exact: true })).toHaveCount(0);
});

test('quota validation blocks below usage and stale version requires explicit reread', async ({ page }) => {
  await session(page);
  let reads = 0, writes = 0;
  await page.route('**' + endpoint, r => {
    if (r.request().method() === 'GET') { reads++; return r.fulfill({ json: { ...quota, version: reads > 1 ? 2 : 1, configured: true } }); }
    writes++;
    return r.fulfill({ status: 409, json: { code: 'VERSION_CONFLICT' } });
  });
  const dialog = await open(page);
  await dialog.getByLabel('持久存储上限（GiB）', { exact: true }).fill('9');
  await dialog.getByRole('button', { name: '保存配额', exact: true }).click();
  await expect(dialog.getByText('不能低于当前占用 10 GiB', { exact: true })).toBeVisible();
  expect(writes).toBe(0);
  await dialog.getByLabel('持久存储上限（GiB）', { exact: true }).fill('30');
  await dialog.getByRole('button', { name: '保存配额', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('已变化');
  await expect(dialog.getByRole('button', { name: '保存配额', exact: true })).toBeDisabled();
  await dialog.getByRole('button', { name: '重新读取', exact: true }).click();
  await expect(dialog.getByRole('button', { name: '保存配额', exact: true })).toBeEnabled();
  expect(reads).toBe(2); expect(writes).toBe(1);
});

test('failed reads cannot save and read-only users cannot change quotas', async ({ page }) => {
  await session(page, ['users:read', 'roles:read']);
  let fail = true;
  await page.route('**' + endpoint, r => r.fulfill(fail ? { status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } } : { json: quota }));
  const dialog = await open(page);
  await expect(dialog.getByRole('alert').filter({ hasText: '重新读取' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: '保存配额', exact: true })).toBeDisabled();
  fail = false;
  await dialog.getByRole('button', { name: '重新读取', exact: true }).click();
  await expect(dialog.getByText('继承组织默认', { exact: true })).toBeVisible();
  await expect(dialog.getByLabel('CPU上限（vCPU）', { exact: true })).toBeDisabled();
  await expect(dialog.getByRole('button', { name: '保存配额', exact: true })).toBeDisabled();
});


test('resource quotas has its own navigation entry and user management has no quota editor', async ({ page }, info) => {
  await session(page);
  await page.route('**' + endpoint, r => r.fulfill({ json: quota }));
  await page.goto('/app/users');
  await expect(page.getByRole('button', { name: '资源配额', exact: true })).toHaveCount(0);
  if (info.project.name === 'mobile') await page.getByRole('button', { name: '打开导航', exact: true }).click();
  await page.getByRole('link', { name: '资源配额', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/quotas$/);
  await expect(page.getByRole('heading', { name: '资源配额', exact: true })).toBeVisible();
  const row = page.getByRole('row').filter({ hasText: target.email });
  await expect(row.getByLabel('CPU已占用 2 / 上限 10 vCPU', { exact: true })).toBeVisible();
  await page.screenshot({ path: info.outputPath('resource-quotas-page.png'), animations: 'disabled' });
});

test('quota search and pagination only show the selected users and their allocations', async ({ page }) => {
  await session(page);
  const other = { ...target, id: '00000000-0000-4000-8000-000000000023', display_name: '测试成员', email: 'qa@example.com' };
  await page.route('**/v1/users?*', r => {
    const params = new URL(r.request().url()).searchParams;
    const next = params.get('page') === '2' || params.get('q') === 'qa';
    return r.fulfill({ json: { items: [next ? other : target], total: params.get('q') === 'qa' ? 1 : 21, next_cursor: next ? null : 'next-quota-page', local_accounts: false } });
  });
  await page.route('**' + endpoint, r => r.fulfill({ json: quota }));
  await page.route('**/v1/users/' + other.id + ':quota', r => r.fulfill({ json: { ...quota, owner_id: other.id, configured: true, version: 1, limits: { ...quota.limits, cpu_millicores: 4000 } } }));
  await page.goto('/app/quotas');
  const next = page.getByRole('button', { name: '下一页', exact: true });
  await expect(page.getByRole('row').filter({ hasText: target.email }).getByLabel('CPU已占用 2 / 上限 10 vCPU', { exact: true })).toBeVisible();
  await next.click();
  await expect(page.getByRole('row').filter({ hasText: target.email })).toHaveCount(0);
  await expect(page.getByRole('row').filter({ hasText: other.email }).getByLabel('CPU已占用 2 / 上限 4 vCPU', { exact: true })).toBeVisible();
  await page.getByRole('searchbox', { name: '搜索配额用户', exact: true }).fill('qa');
  await page.getByRole('searchbox', { name: '搜索配额用户', exact: true }).press('Enter');
  await expect(page.getByRole('navigation', { name: '资源配额分页', exact: true }).getByText('第', { exact: false })).toContainText('1');
  await expect(next).toBeDisabled();
});

test('members cannot open the quota page or discover its navigation entry', async ({ page }) => {
  await session(page, ['instances:read']);
  let reads = 0;
  await page.route('**/v1/users?*', r => { reads++; return r.fulfill({ json: { items: [] } }); });
  await page.goto('/app/quotas');
  await expect(page.getByText('没有资源配额读取权限', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '资源配额', exact: true })).toHaveCount(0);
  expect(reads).toBe(0);
});
