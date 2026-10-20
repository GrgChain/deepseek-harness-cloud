import { expect, test, type Page } from '@playwright/test';

const admin = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员', status: 'ACTIVE' }], permissions: ['users:read', 'users:manage', 'roles:read'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };
async function navigate(page: Page, label: string) {
  const menu = page.getByRole('button', { name: '打开导航', exact: true });
  if (await menu.isVisible()) await menu.click();
  await page.getByRole('link', { name: label, exact: true }).click();
}

const target = { id: '00000000-0000-4000-8000-000000000045', display_name: '测试成员', email: 'member@example.com', identity_source: 'oidc', status: 'ACTIVE', role_name: '成员', version: 3, last_login_at: null, password_reset: null };

test('user deletion requires a matching name, supports cancellation and removes the user from both default lists', async ({ page }, testInfo) => {
  let deleted = false, commands = 0;
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: admin }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
  await page.route('**/v1/users?*', r => {
    const archived = new URL(r.request().url()).searchParams.get('status') === 'ARCHIVED';
    return r.fulfill({ json: { items: archived ? deleted ? [{ ...target, status: 'ARCHIVED', version: 4 }] : [] : deleted ? [] : [target], next_cursor: null, local_accounts: false } });
  });
  await page.route('**/v1/users/' + target.id + ':quota', r => r.fulfill({ json: { owner_id: target.id, version: 0, configured: false, limits: { max_instances: 2, cpu_millicores: 2000, memory_bytes: 1073741824, storage_bytes: 10737418240 }, usage: { max_instances: 0, cpu_millicores: 0, memory_bytes: 0, storage_bytes: 0 }, organization_limits: { max_instances: 10, cpu_millicores: 10000, memory_bytes: 21474836480, storage_bytes: 107374182400 } } }));
  await page.route('**/v1/users/' + target.id, async r => {
    expect(r.request().method()).toBe('DELETE');
    expect(r.request().postDataJSON()).toEqual({ expected_version: 3, confirmed_name: target.display_name });
    expect(await r.request().headerValue('x-csrf-token')).toBe(admin.csrf_token);
    commands++;
    deleted = true;
    await r.fulfill({ status: 204 });
  });
  await page.goto('/app/quotas');
  await expect(page.getByRole('row').filter({ hasText: target.email })).toBeVisible();
  await navigate(page, '用户管理');
  const row = page.getByRole('row').filter({ hasText: target.email });
  await row.getByRole('button', { name: '更多用户操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '删除', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: '删除用户', exact: true });
  await dialog.getByRole('button', { name: /^取\s*消$/ }).click();
  expect(commands).toBe(0);
  await row.getByRole('button', { name: '更多用户操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '删除', exact: true }).click();
  dialog = page.getByRole('dialog', { name: '删除用户', exact: true });
  await dialog.getByLabel('输入用户名确认删除').fill('其他成员');
  await dialog.getByRole('button', { name: /确认删除$/ }).click();
  await expect(dialog.getByText('用户名不匹配，请输入完整用户名')).toBeVisible();
  expect(commands).toBe(0);
  await dialog.getByLabel('输入用户名确认删除').fill(target.display_name);
  await expect(dialog.getByText('用户名不匹配，请输入完整用户名')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('delete-confirmation.png'), fullPage: true });
  await dialog.getByRole('button', { name: /确认删除$/ }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('用户已删除，登录权限已撤销。')).toBeVisible();
  await expect(row).toHaveCount(0);
  expect(commands).toBe(1);
  await page.getByRole('combobox', { name: '筛选用户状态' }).press('ArrowDown');
  await page.locator('.ant-select-item-option-content').getByText('已删除', { exact: true }).click();
  await expect(row.getByText('已删除', { exact: true })).toBeVisible();
  await row.getByRole('button', { name: '更多用户操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '删除', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await navigate(page, '资源配额');
  await expect(page.getByRole('heading', { name: '用户资源配额', exact: true })).toBeVisible();
  await expect(page.getByRole('row').filter({ hasText: target.email })).toHaveCount(0);
});

test('failed user deletion preserves the row and lets the administrator reread a changed user', async ({ page }) => {
  let commands = 0;
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: admin }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
  await page.route('**/v1/users?*', r => r.fulfill({ json: { items: [target, { ...target, id: admin.id, display_name: '当前管理员', email: 'admin@example.com' }], next_cursor: null } }));
  await page.route('**/v1/users/' + target.id, r => r.fulfill({ status: 409, json: { code: ++commands === 1 ? 'USER_HAS_ASSETS' : 'VERSION_CONFLICT' } }));
  await page.goto('/app/users');
  await page.getByRole('row').filter({ hasText: 'admin@example.com' }).getByRole('button', { name: '更多用户操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '删除', exact: true })).toBeDisabled();
  await page.getByRole('row').filter({ hasText: 'admin@example.com' }).getByRole('button', { name: '更多用户操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '删除', exact: true })).toBeHidden();
  const row = page.getByRole('row').filter({ hasText: target.email });
  await row.getByRole('button', { name: '更多用户操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '删除', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '删除用户', exact: true });
  await dialog.getByLabel('输入用户名确认删除').fill(target.display_name);
  await dialog.getByRole('button', { name: /确认删除$/ }).click();
  await expect(dialog.getByText('用户仍有实例、保留数据或恢复占用，请先禁用并处理资产')).toBeVisible();
  await expect(row).toBeVisible();
  await dialog.getByRole('button', { name: /确认删除$/ }).click();
  await expect(dialog.getByText('用户、角色或配额已变化，请重新读取并确认')).toBeVisible();
  await dialog.getByRole('button', { name: '重新读取', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(row).toBeVisible();
  expect(commands).toBe(2);
});

test('users with read permission have no delete action', async ({ page }) => {
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: { ...admin, permissions: ['users:read', 'roles:read'] } }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: [], next_cursor: null } }));
  await page.route('**/v1/users?*', r => r.fulfill({ json: { items: [target], next_cursor: null } }));
  await page.goto('/app/users');
  await expect(page.getByRole('row').filter({ hasText: target.email })).toBeVisible();
  await expect(page.getByRole('button', { name: '更多用户操作', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '新增用户', exact: true })).toHaveCount(0);
});
