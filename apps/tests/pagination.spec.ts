import { expect, test, type Page } from '@playwright/test';

const user = {
  id: '00000000-0000-4000-8000-000000000041',
  memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员', status: 'ACTIVE' }],
  permissions: ['instances:read', 'users:read', 'roles:read', 'audit:read'],
  auth_generation: 1, csrf_token: 'synthetic-pagination-csrf-token-32-characters', session_expires_at: '2099-01-01T00:00:00Z',
};
const health = { status: 'UNKNOWN', checked_at: null, expires_at: null, reason_code: 'HEALTH_REQUIRED' };
const entries = Array.from({ length: 43 }, (_, index) => {
  const name = `记录-${String(index + 1).padStart(2, '0')}`;
  return {
    id: `record-${index}`, name, display_name: name, email: `member${index}@example.com`,
    code: `role_${index}`, role_name: '成员', status: 'ACTIVE', identity_source: 'oidc',
    last_login_at: null, builtin: true, permissions: [], user_count: 0,
    action: name, outcome: 'SUCCEEDED', trace_id: `trace-${index}`, timestamp: '2026-10-14T00:00:00Z',
    owner_id: user.id, observed_state: 'STOPPED', ready: false, can_enter: false, allowed_actions: [],
    updated_at: '2026-10-14T00:00:00Z', config_snapshot: { model: 'deepseek', spec: { cpu_millicores: 2000, memory_bytes: 4 * 2 ** 30, storage_bytes: 10 * 2 ** 30 } },
    health: { application: health, storage: health, configuration: health, access: health, model: health },
  };
});

async function session(page: Page) {
  await page.route('**/v1/**', route => route.fulfill({ json: { items: [] } }));
  await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', route => route.fulfill({ json: user }));
}

for (const table of [
  { path: 'instances', endpoint: 'instances', label: '实例', search: '搜索实例' },
  { path: 'users', endpoint: 'users', label: '用户', search: '搜索用户' },
  { path: 'roles', endpoint: 'roles', label: '角色', search: '搜索角色' },
  { path: 'quotas', endpoint: 'users', label: '资源配额', search: '搜索配额用户' },
  { path: 'audit', endpoint: 'audit-events', label: '操作日志', search: null },
]) {
  test(`${table.label} pagination replaces rows, changes size and resets filters`, async ({ page }, testInfo) => {
    await session(page);
    if (table.path === 'quotas') {
      const limits = { max_instances: 2, cpu_millicores: 2000, memory_bytes: 4 * 2 ** 30, storage_bytes: 10 * 2 ** 30 };
      await page.route('**/v1/users/*:quota', route => route.fulfill({ json: { owner_id: '', version: 0, configured: false, limits, usage: { ...limits, max_instances: 0 }, organization_limits: limits } }));
    }
    const requests: URLSearchParams[] = [];
    await page.route(`**/v1/${table.endpoint}?*`, route => {
      const params = new URL(route.request().url()).searchParams;
      requests.push(params);
      const limit = Number(params.get('limit'));
      const start = (Number(params.get('page') ?? 1) - 1) * limit;
      return route.fulfill({ json: { items: entries.slice(start, start + limit), total: entries.length, next_cursor: start + limit < entries.length ? `opaque:${start + limit}` : null, local_accounts: false } });
    });
    await page.goto('/app/' + table.path);
    const pagination = page.getByRole('navigation', { name: `${table.label}分页`, exact: true });
    const pageNumber = pagination.locator('[aria-current="page"]');
    await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
    expect(requests.at(-1)?.get('limit')).toBe('10');
    await expect(pagination.getByRole('button', { name: '上一页', exact: true })).toBeDisabled();
    await pagination.getByRole('button', { name: '跳到尾页', exact: true }).click();
    await expect(pageNumber).toHaveText('第 5 / 5 页');
    expect(requests).toHaveLength(2);
    expect(requests.at(-1)?.get('page')).toBe('5');
    await pagination.getByRole('button', { name: '上一页', exact: true }).click();
    await expect(pageNumber).toHaveText('第 4 / 5 页');
    await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
    await expect(page.getByText('记录-31', { exact: true })).toBeVisible();
    await expect(page.getByText('记录-01', { exact: true })).toHaveCount(0);
    expect(requests.at(-1)?.get('page')).toBe('4');
    expect(requests.at(-1)?.has('cursor')).toBe(false);
    await pagination.getByRole('button', { name: '跳到尾页', exact: true }).click();
    expect(requests.at(-1)?.get('page')).toBe('5');
    await expect(pageNumber).toHaveText('第 5 / 5 页');
    await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 3 条');
    await expect(pagination.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
    await pagination.getByRole('button', { name: '上一页', exact: true }).click();
    await expect(page.getByText('记录-31', { exact: true })).toBeVisible();
    await pagination.getByRole('button', { name: '返回首页', exact: true }).focus();
    await pagination.getByRole('button', { name: '返回首页', exact: true }).press('Enter');
    await expect(pageNumber).toHaveText('第 1 / 5 页');
    await expect(page.getByText('记录-01', { exact: true })).toBeVisible();
    await pagination.locator('.ant-select').click();
    await page.getByRole('option', { name: '50 条 / 页', exact: true }).click();
    await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 43 条');
    expect(requests.at(-1)?.get('limit')).toBe('50');
    expect(requests.at(-1)?.has('cursor')).toBe(false);
    await pagination.locator('.ant-select').click();
    await page.getByRole('option', { name: '10 条 / 页', exact: true }).click();
    await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
    await pagination.getByRole('button', { name: '下一页', exact: true }).click();
    await expect(pageNumber).toHaveText('第 2 / 5 页');
    if (table.search) {
      const search = page.getByRole('searchbox', { name: table.search, exact: true });
      await search.fill('记录');
      await search.press('Enter');
      await expect(pageNumber).toHaveText('第 1 / 5 页');
      await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
      expect(requests.at(-1)?.get('q')).toBe('记录');
      expect(requests.at(-1)?.has('cursor')).toBe(false);
    }
    if (table.path === 'instances') {
      await pagination.getByRole('button', { name: '下一页', exact: true }).click();
      await page.getByText('已停止', { exact: true }).first().click();
      await expect(pageNumber).toHaveText('第 1 / 5 页');
      await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
      expect(requests.at(-1)?.get('state')).toBe('STOPPED');
      expect(requests.at(-1)?.has('cursor')).toBe(false);
    }
    await pagination.getByRole('button', { name: '跳到尾页', exact: true }).click();
    await expect(pageNumber).toHaveText('第 5 / 5 页');
    await expect(pagination.getByRole('button', { name: '跳到尾页', exact: true })).toBeDisabled();
    expect(requests.at(-1)?.get('page')).toBe('5');
    await pagination.scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`${table.path}-pagination.png`), animations: 'disabled' });
  });
}

test('pending and failed pages cannot advance and can return to the first page', async ({ page }) => {
  await session(page);
  let release: () => void = () => {};
  await page.route('**/v1/audit-events?*', async route => {
    if (new URL(route.request().url()).searchParams.get('page') === '2') {
      await new Promise<void>(resolve => { release = resolve; });
      return route.fulfill({ status: 400, json: { code: 'INVALID_CURSOR' } });
    }
    return route.fulfill({ json: { items: entries.slice(0, 10), total: entries.length, next_cursor: 'expired:10' } });
  });
  await page.goto('/app/audit');
  const pagination = page.getByRole('navigation', { name: '操作日志分页', exact: true });
  await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
  await pagination.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(pagination.getByRole('status')).toHaveText('正在加载数据…');
  await expect(pagination.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
  await expect(pagination.getByRole('button', { name: '跳到尾页', exact: true })).toBeDisabled();
  await expect(pagination.getByRole('combobox', { name: '每页条数' })).toBeDisabled();
  release();
  await expect(pagination.getByRole('status')).toHaveText('数据暂不可用');
  await expect(pagination.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
  await pagination.getByRole('button', { name: '返回首页', exact: true }).click();
  await expect(pagination.getByRole('status')).toHaveText('共 43 条 · 本页 10 条');
  await expect(page.getByText('操作日志查询失败', { exact: true })).toHaveCount(0);
});

test('a reduced result count returns an out-of-range page to the new last page', async ({ page }) => {
  await session(page);
  let total = 43;
  const requested: number[] = [];
  await page.route('**/v1/audit-events?*', route => {
    const number = Number(new URL(route.request().url()).searchParams.get('page'));
    requested.push(number);
    return route.fulfill({ json: { items: entries.slice((number - 1) * 10, Math.min(number * 10, total)), total, next_cursor: null } });
  });
  await page.goto('/app/audit');
  const pagination = page.getByRole('navigation', { name: '操作日志分页', exact: true });
  await expect(pagination.locator('[aria-current="page"]')).toHaveText('第 1 / 5 页');
  await pagination.getByRole('button', { name: '跳到尾页', exact: true }).click();
  await expect(pagination.locator('[aria-current="page"]')).toHaveText('第 5 / 5 页');
  total = 21;
  await page.getByRole('button', { name: /^刷\s*新$/ }).click();
  await expect(pagination.locator('[aria-current="page"]')).toHaveText('第 3 / 3 页');
  await expect(pagination.getByRole('status')).toHaveText('共 21 条 · 本页 1 条');
  expect(requested.slice(-2)).toEqual([5, 3]);
});

test('an empty result keeps pagination visible with no forward navigation', async ({ page }) => {
  await session(page);
  await page.route('**/v1/audit-events?*', route => route.fulfill({ json: { items: [], total: 0, next_cursor: null } }));
  await page.goto('/app/audit');
  const pagination = page.getByRole('navigation', { name: '操作日志分页', exact: true });
  await expect(pagination.getByRole('status')).toHaveText('共 0 条 · 本页 0 条 · 暂无数据');
  await expect(pagination.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
  await expect(pagination.getByRole('button', { name: '上一页', exact: true })).toBeDisabled();
});
