import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

test('live lists show authorized totals and navigate directly to the last page', async ({ browser }, info) => {
  test.skip(process.env.HC_REAL_PAGINATION !== 'true', 'requires the deployed local platform; reads lists without changing business data');
  test.setTimeout(60_000);
  const context = await browser.newContext({ ...info.project.use, baseURL: info.project.use.baseURL, ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.setDefaultTimeout(10_000);
  let csrf = '';
  try {
    await page.goto('/auth/login');
    await page.locator('input[name=login]').fill('admin@harness.local');
    await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password', 'utf8').trim());
    await page.getByRole('button', { name: '登录', exact: true }).click();
    await expect(page).toHaveURL(/\/app\/instances$/);
    const me = await page.request.get('/v1/me');
    expect(me.status()).toBe(200);
    csrf = (await me.json()).csrf_token;
    for (const table of [
      { path: 'instances', endpoint: 'instances', label: '实例' },
      { path: 'users', endpoint: 'users', label: '用户' },
      { path: 'roles', endpoint: 'roles', label: '角色' },
      { path: 'quotas', endpoint: 'users', label: '资源配额' },
      { path: 'audit', endpoint: 'audit-events', label: '操作日志' },
    ]) {
      const firstResponse = page.waitForResponse(response => {
        const url = new URL(response.url());
        return url.pathname === '/v1/' + table.endpoint && url.searchParams.get('page') === '1' && url.searchParams.get('limit') === '10';
      });
      await page.goto('/app/' + table.path);
      const first = await firstResponse;
      expect(first.status()).toBe(200);
      const data = await first.json();
      expect(Number.isSafeInteger(data.total)).toBe(true);
      expect(data.total).toBeGreaterThanOrEqual(data.items.length);
      const pages = Math.max(1, Math.ceil(data.total / 10));
      const navigation = page.getByRole('navigation', { name: table.label + '分页', exact: true });
      await expect(navigation.getByRole('status')).toContainText('共 ' + data.total + ' 条');
      await expect(navigation.locator('[aria-current="page"]')).toHaveText('第 1 / ' + pages + ' 页');
      if (pages > 1) {
        const lastResponse = page.waitForResponse(response => {
          const url = new URL(response.url());
          return url.pathname === '/v1/' + table.endpoint && url.searchParams.get('page') === String(pages);
        });
        await navigation.getByRole('button', { name: '跳到尾页', exact: true }).click();
        const last = await lastResponse;
        expect(last.status()).toBe(200);
        const tail = await last.json();
        expect(tail.next_cursor).toBeNull();
        expect(tail.items.length).toBeGreaterThan(0);
        expect(tail.items.length).toBeLessThanOrEqual(10);
        await expect(navigation.locator('[aria-current="page"]')).toHaveText('第 ' + pages + ' / ' + pages + ' 页');
        await expect(navigation.getByRole('status')).toHaveText('共 ' + tail.total + ' 条 · 本页 ' + tail.items.length + ' 条');
      }
      await expect(navigation.getByRole('button', { name: '跳到尾页', exact: true })).toBeDisabled();
      await navigation.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: info.outputPath(table.path + '-pagination-live.png'), animations: 'disabled' });
    }
  } finally {
    if (csrf) expect((await page.request.post('/auth/logout', { headers: { Origin: new URL(page.url()).origin, 'X-CSRF-Token': csrf } })).status()).toBe(204);
    await context.close();
  }
});
