import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';

test('live plugin library offers the reviewed Mem0 bundle and configuration guide', async ({
  page,
}, info) => {
  test.skip(
    process.env.HC_REAL_MEM0_PLUGIN !== 'true',
    'requires the local catalog and OIDC services',
  );
  test.setTimeout(60_000);
  const password = (await readFile('../.local/auth/admin-password', 'utf8')).trim();
  await page.goto('/auth/login?return_to=%2Fapp%2Fplugins');
  await page.locator('input[name=login]').fill('admin@harness.local');
  await page.locator('input[name=password]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/plugins$/);
  const response = await page.request.get('/v1/plugins');
  expect(response.status()).toBe(200);
  const { items } = await response.json();
  expect(items).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ package_name: '@harness-cloud/dsh-mem0-memory', version: '0.1.0' }),
    ]),
  );
  await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('Mem0');
  const card = page.getByRole('article', { name: 'Mem0 记忆', exact: true });
  await expect(card.getByText('本地维护', { exact: true })).toBeVisible();
  await card.getByRole('button', { name: '查看详情', exact: true }).click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(details.getByRole('link', { name: 'npm 包', exact: true })).toHaveCount(0);
  await expect(details.getByRole('region', { name: 'Mem0 配置说明', exact: true })).toContainText(
    '默认关闭',
  );
  const waiting = page.waitForEvent('download');
  await details.getByRole('button', { name: '下载插件包', exact: true }).click();
  const download = await waiting;
  expect(download.suggestedFilename()).toBe('harness-cloud-dsh-mem0-memory-0.1.0.tgz');
  const file = await download.path();
  expect(file).toBeTruthy();
  expect(await readFile(file!)).toEqual(
    await readFile('public/plugin-packages/harness-cloud-dsh-mem0-memory-0.1.0.tgz'),
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await details.screenshot({
    path: info.outputPath('mem0-live-details.png'),
    animations: 'disabled',
  });
});
