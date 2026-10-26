import { expect, test } from '@playwright/test';

test('reports live service readiness and keeps unimplemented login closed', async ({ page }) => {
  await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: false, instances: false } } }));
  await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '工作空间', exact: true })).toBeVisible();
  await expect(page.getByText('身份服务尚未就绪', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '创建工作空间' })).toBeDisabled();
  await expect(page.getByText('暂时无法登录，请稍后重试。', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: `test-results/entry-${test.info().project.name}.png`, fullPage: true });
});

test('reports connection errors and recovers through the refresh control', async ({ page }) => {
  let online = false;
  await page.route('**/livez', route => online ? route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: false } } }) : route.abort());
  await page.route('**/readyz', route => route.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }));
  await page.goto('/app/instances');
  await expect(page.getByText('平台尚未连接', { exact: true })).toBeVisible();
  online = true;
  await page.getByRole('button', { name: '重新检查' }).click();
  await expect(page.getByText('暂时无法登录，请稍后重试。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '查看平台配置', exact: true }).click();
  await expect(page.getByRole('region', { name: '平台连接状态' }).getByText('未就绪', { exact: true })).toHaveCount(2);
  await page.getByRole('button', { name: '尚未登录', exact: true }).click();
  await expect(page.getByRole('button', { name: '登录账户' })).toBeDisabled();
});
