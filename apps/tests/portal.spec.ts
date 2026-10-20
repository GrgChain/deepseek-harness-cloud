import { expect, test } from '@playwright/test';

test('unavailable API keeps creation closed and preserves navigation', async ({ page }) => {
  await page.route('**/livez', route => route.fulfill({ status: 503, body: '{}' }));
  await page.goto('/app/instances');
  await expect(page.getByText('平台尚未连接', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '创建工作空间' })).toBeDisabled();
  await page.getByRole('button', { name: '查看平台配置', exact: true }).click();
  await page.getByRole('button', { name: '诊断信息', exact: true }).click();
  await expect(page.getByRole('heading', { name: '平台预检' })).toBeVisible();
  await expect(page.getByRole('button', { name: '配置模型连接' })).toHaveCount(0);
  await expect(page.getByText('登录后查看运行配置。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '尚未登录', exact: true }).click();
  await expect(page.getByRole('button', { name: '登录账户' })).toBeDisabled();
});

test('healthy dependencies do not imply authentication or instance readiness', async ({ page }) => {
  await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api' } }));
  await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.goto('/app/settings');
  await expect(page.getByText('身份服务尚未就绪', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '诊断信息', exact: true }).click();
  await expect(page.getByText('已就绪', { exact: true })).toHaveCount(1);
  await expect(page.getByRole('button', { name: '配置模型连接' })).toHaveCount(0);
  await expect(page.getByText('登录后查看运行配置。', { exact: true })).toBeVisible();
});

test('desktop and mobile render without overflow or runtime errors', async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/livez', route => route.fulfill({ status: 503, body: '{}' }));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/app/instances');
  await expect(page.getByText('平台尚未连接', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('desktop.png'), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath('mobile.png'), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole('button', { name: '打开导航' }).click();
  await page.getByRole('link', { name: '平台配置' }).filter({ visible: true }).click();
  await page.getByRole('button', { name: '诊断信息', exact: true }).click();
  await expect(page.getByRole('heading', { name: '平台预检' })).toBeVisible();
  await expect(page.getByRole('dialog')).not.toBeVisible();
  expect(errors).toEqual([]);
});
