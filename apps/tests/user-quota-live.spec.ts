import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

test('live administrator reads quota editor and member cannot read administrator quotas', async ({ browser }, info) => {
  test.setTimeout(60_000);
  test.skip(process.env.HC_REAL_QUOTA !== 'true', 'requires deployed embedded OIDC; only quota reads are performed');
  const adminContext = await browser.newContext({ ...info.project.use, baseURL: info.project.use.baseURL, ignoreHTTPSErrors: true });
  const memberContext = await browser.newContext({ ...info.project.use, baseURL: info.project.use.baseURL, ignoreHTTPSErrors: true });
  const login = async (context: typeof adminContext, role: 'admin' | 'member') => {
    const page = await context.newPage();
    page.setDefaultTimeout(10_000);
    const loginResponse = await page.goto('/auth/login');
    expect(loginResponse?.status()).toBe(200);
    await page.locator('input[name=login]').fill(role + '@harness.local');
    await page.locator('input[name=password]').fill(readFileSync('../.local/auth/' + role + '-password', 'utf8').trim());
    await page.getByRole('button', { name: '登录', exact: true }).click();
    await expect(page).toHaveURL(/\/app\/instances$/);
    const response = await page.request.get('/v1/me');
    expect(response.status()).toBe(200);
    return { page, user: await response.json() };
  };
  try {
    const { page, user } = await login(adminContext, 'admin');
    const response = await page.request.get('/v1/users/' + user.id + ':quota');
    expect(response.status()).toBe(200);
    const quota = await response.json();
    expect(quota.owner_id).toBe(user.id);
    expect(Object.keys(quota.limits).sort()).toEqual(['cpu_millicores', 'max_instances', 'memory_bytes', 'storage_bytes']);
    await page.goto('/app/quotas');
    await page.getByRole('searchbox', { name: '搜索配额用户', exact: true }).fill('admin@harness.local');
    await page.getByRole('searchbox', { name: '搜索配额用户', exact: true }).press('Enter');
    await page.getByRole('row').filter({ hasText: 'admin@harness.local' }).getByRole('button', { name: '调整配额', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '资源配额', exact: true });
    await expect(dialog.getByLabel('同时运行数量上限（个）', { exact: true })).toHaveCount(0);
    await expect(dialog.getByRole('spinbutton')).toHaveCount(4);
    await expect(dialog.getByLabel('CPU上限（vCPU）', { exact: true })).toHaveAttribute('aria-valuenow',String(quota.limits.cpu_millicores / 1000));
    await expect(dialog.getByRole('button', { name: '保存配额', exact: true })).toBeVisible();
    await expect(dialog).not.toHaveClass(/ant-zoom/);
    await page.screenshot({ path: info.outputPath('live-user-quota.png'), fullPage: false, animations: 'disabled' });
    await dialog.getByLabel('持久存储上限（GiB）', { exact: true }).scrollIntoViewIfNeeded();
    await expect(dialog.getByLabel('持久存储上限（GiB）', { exact: true })).toBeInViewport();
    await page.screenshot({ path: info.outputPath('live-user-quota-storage.png'), fullPage: false, animations: 'disabled' });
    const member = await login(memberContext, 'member');
    await member.page.goto('/app/quotas');
    await expect(member.page.getByText('没有资源配额读取权限', { exact: true })).toBeVisible();
    expect((await member.page.request.get('/v1/users/' + user.id + ':quota')).status()).toBe(403);
    for (const current of [{ page, user }, member]) {
      expect((await current.page.request.post('/auth/logout', { headers: { Origin: new URL(current.page.url()).origin, 'X-CSRF-Token': current.user.csrf_token } })).status()).toBe(204);
    }
  } finally {
    await memberContext.close();
    await adminContext.close();
  }
});
