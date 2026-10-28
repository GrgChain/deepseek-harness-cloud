import { expect, test, type Page } from '@playwright/test';

const user = {
 id: '00000000-0000-4000-8000-000000000001',
 memberships: [{ org_id: '00000000-0000-4000-8000-000000000002', role: 'MEMBER', status: 'ACTIVE', role_id: '00000000-0000-4000-8000-000000000004', role_name: '成员' }],
 permissions: ['instances:read', 'connectors:read'], auth_generation: 1,
 csrf_token: 'synthetic-csrf-token-32-characters-long', session_expires_at: '2099-01-01T00:00:00Z',
};
async function setup(page: Page, permissions = user.permissions) {
 await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
 await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
 await page.route('**/v1/me', route => route.fulfill({ json: { ...user, permissions } }));
}
async function select(page: Page, label: string, option: string) {
 await page.locator('.ant-select').filter({ has: page.getByRole('combobox', { name: label, exact: true }) }).click();
 await page.locator('.ant-select-dropdown:visible').getByTitle(option, { exact: true }).click();
}

test('plugin square navigation and responsive catalog work without querying or changing a workspace', async ({ page }, info) => {
 await setup(page, [...user.permissions, 'memory:read', 'knowledge:read', 'personal-model:read']); const workspaceRequests: string[] = [], writes: string[] = [], errors: string[] = [];
 page.on('request', request => {
  if (request.url().includes('/v1/instances') || request.url().includes('/__dsh/') || request.url().includes('/api/plugin')) workspaceRequests.push(request.url());
  if (!['GET', 'HEAD'].includes(request.method())) writes.push(request.url());
 });
 page.on('pageerror', error => errors.push(error.message));
 await page.goto('/app/plugins');
 if (info.project.name === 'mobile') await page.getByRole('button', { name: '打开导航', exact: true }).click();
 const nav = page.getByRole('navigation', { name: '主导航', exact: true }).filter({ visible: true });
 await expect(nav.getByRole('link', { name: '插件', exact: true })).toBeVisible();
 const labels = await nav.getByRole('link').allTextContents();
 expect(labels.indexOf('插件')).toBe(labels.indexOf('知识库') + 1);
 expect(labels.indexOf('模型提供商')).toBe(labels.indexOf('插件') + 1);
 await nav.getByRole('link', { name: '插件', exact: true }).click();
 await expect(page.getByRole('heading', { name: '插件', exact: true })).toBeVisible();
 await expect(page.getByRole('article')).toHaveCount(10);
 await expect(page.getByRole('button', { name: '打开插件管理', exact: true })).toHaveCount(0);
 await expect(page.getByRole('radio')).toHaveCount(0);
 await expect(page.getByRole('navigation', { name: '插件分页', exact: true })).toContainText('12');
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
 await page.screenshot({ path: info.outputPath('plugin-square.png'), fullPage: true, animations: 'disabled' });
 await page.getByRole('article', { name: 'OpenViking 记忆', exact: true }).getByRole('button', { name: '查看详情', exact: true }).click();
 await expect(page.getByRole('dialog', { name: '插件详情', exact: true })).toBeVisible();
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
 await page.screenshot({ path: info.outputPath('plugin-details.png'), animations: 'disabled' });
 expect(workspaceRequests).toEqual([]); expect(writes).toEqual([]); expect(errors).toEqual([]);
});

test('search covers packages, authors and capabilities; source and category filters combine over the full catalog', async ({ page }) => {
 await setup(page); await page.goto('/app/plugins');
 await page.getByRole('button', { name: '下一页', exact: true }).click();
 await expect(page.getByRole('article')).toHaveCount(2);
 const search = page.getByRole('searchbox', { name: '搜索插件', exact: true });
 await search.fill('会话');
 await expect(page.getByRole('article', { name: '会话搜索', exact: true })).toBeVisible();
 await expect(page.getByRole('navigation', { name: '插件分页', exact: true })).toContainText('第 1 / 1 页');
 await search.fill('@openviking/dsh-memory-plugin');
 await expect(page.getByRole('article')).toHaveCount(1);
 await expect(page.getByRole('article', { name: 'OpenViking 记忆', exact: true })).toBeVisible();
 await search.fill('zhu1090093659');
 await expect(page.getByRole('article', { name: 'DSH SSH', exact: true })).toBeVisible();
 await search.fill(''); await select(page, '插件来源', '第三方');
 await expect(page.getByRole('article')).toHaveCount(3);
 await select(page, '插件分类', '会话与记忆');
 await expect(page.getByRole('article')).toHaveCount(1);
 await expect(page.getByRole('article', { name: 'OpenViking 记忆', exact: true })).toBeVisible();
 await select(page, '插件来源', '官方');
 await expect(page.getByRole('article')).toHaveCount(2);
 await expect(page.getByRole('article', { name: '会话标题', exact: true })).toBeVisible();
});

test('details expose pinned version, provenance, compatibility and manual install guidance; copying does not install', async ({ page, context }) => {
 await setup(page);
 await context.grantPermissions(['clipboard-read', 'clipboard-write']);
 const writes: string[] = []; page.on('request', request => { if (request.method() === 'POST') writes.push(request.url()); });
 await page.goto('/app/plugins');
 await page.getByRole('article', { name: 'DSH SSH', exact: true }).getByRole('heading').getByRole('button').click();
 const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
 await expect(drawer).toContainText('0.4.11'); await expect(drawer).toContainText('>=0.2.1-rc.3');
 await expect(drawer).toContainText('来源核对'); await expect(drawer).toContainText('收录不代表已通过运行验证');
 await expect(drawer).toContainText('安装与启用由你在 DSH 中完成');
 await expect(drawer.getByRole('link', { name: '源代码', exact: true })).toHaveAttribute('href', 'https://github.com/zhu1090093659/dsh-web');
 await expect(drawer.getByRole('link', { name: '源代码', exact: true })).toHaveAttribute('rel', 'noopener noreferrer');
 await expect(drawer.getByRole('link', { name: 'npm 包', exact: true })).toHaveAttribute('href', 'https://www.npmjs.com/package/@linxin666/dsh-ssh/v/0.4.11');
 await drawer.getByRole('button', { name: '复制包名', exact: true }).click();
 await expect(drawer.getByRole('status')).toHaveText('包名与版本已复制');
 expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('@linxin666/dsh-ssh@0.4.11');
 expect(writes).toEqual([]);
 await page.keyboard.press('Escape'); await expect(drawer).not.toBeVisible();
 await page.getByRole('article', { name: '会话搜索', exact: true }).getByRole('button', { name: '查看详情', exact: true }).click();
 await expect(page.getByRole('dialog')).toContainText('此官方组合包随对应版本的 DSH 提供');
 await expect(page.getByRole('button', { name: '复制包名', exact: true })).toBeVisible();
});

test('clipboard failure keeps a selectable package and detail close preserves filters', async ({ page }) => {
 await setup(page);
 await page.addInitScript(() => { Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw new Error('denied'); } } }); });
 await page.goto('/app/plugins'); await select(page, '插件来源', '第三方');
 await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('OpenViking');
 await page.getByRole('article').getByRole('button', { name: '查看详情', exact: true }).click();
 const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
 await drawer.getByRole('button', { name: '复制包名', exact: true }).click();
 await expect(drawer).toContainText('复制失败，请手动选择并复制上方包名');
 await expect(drawer.getByLabel('插件安装包名', { exact: true })).toHaveText('@openviking/dsh-memory-plugin@0.5.29');
 await page.keyboard.press('Escape');
 await expect(page.getByRole('article')).toHaveCount(1);
 await expect(page.getByRole('searchbox', { name: '搜索插件', exact: true })).toHaveValue('OpenViking');
 await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('不存在的插件');
 await expect(page.getByText('没有符合条件的插件', { exact: true })).toBeVisible();
 await page.getByRole('button', { name: '清除筛选', exact: true }).click();
 await expect(page.getByRole('article')).toHaveCount(10);
 await expect(page.getByRole('searchbox', { name: '搜索插件', exact: true })).toHaveValue('');
});

test('direct access and navigation follow the existing read permission', async ({ page }) => {
 await setup(page, []); await page.goto('/app/plugins');
 await expect(page.getByText('没有实例读取权限', { exact: true })).toBeVisible();
 await expect(page.getByRole('link', { name: '插件', exact: true })).toHaveCount(0);
 await expect(page.getByRole('article')).toHaveCount(0);
});

test('the full catalog is keyboard accessible and uses safe public source links', async ({ page }) => {
 await setup(page); await page.goto('/app/plugins');
 const titleButton = page.getByRole('article', { name: '会话搜索', exact: true }).getByRole('heading').getByRole('button');
 await titleButton.focus(); await page.keyboard.press('Enter');
 const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
 await expect(drawer).toBeVisible();
 for (const link of await drawer.getByRole('link').all()) {
  const url = new URL((await link.getAttribute('href'))!);
  expect(url.protocol).toBe('https:'); expect(url.username).toBe(''); expect(url.password).toBe('');
 }
 await page.keyboard.press('Escape'); await expect(drawer).not.toBeVisible();
 await page.getByRole('button', { name: '跳到尾页', exact: true }).click();
 await expect(page.getByRole('article', { name: 'DSH 徽章技能', exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
 await page.getByRole('button', { name: '返回首页', exact: true }).click();
 await expect(page.getByRole('article')).toHaveCount(10);
});
