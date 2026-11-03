import { expect, test, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';

const user = {
  id: '00000000-0000-4000-8000-000000000001',
  memberships: [
    {
      org_id: '00000000-0000-4000-8000-000000000002',
      role: 'MEMBER',
      status: 'ACTIVE',
      role_id: '00000000-0000-4000-8000-000000000004',
      role_name: '成员',
    },
  ],
  permissions: ['instances:read', 'connectors:read'],
  auth_generation: 1,
  csrf_token: 'synthetic-csrf-token-32-characters-long',
  session_expires_at: '2099-01-01T00:00:00Z',
};
const customPlugin = {
  id: '00000000-0000-4000-8000-000000000010',
  name: '项目文档助手',
  package_name: '@test/dsh-docs',
  version: '1.2.3',
  source: 'community',
  author: '文档插件维护者',
  category: '集成扩展',
  summary: '整理和查找项目文档。',
  description: '在 DSH 中提供项目文档工具。',
  keywords: ['文档', 'docs'],
  repository: 'https://github.com/example/dsh-docs',
  documentation: 'https://github.com/example/dsh-docs#readme',
  compatibility: 'DSH >=0.2.1',
  checked_at: '2026-11-02',
  custom: true,
  addition_method: 'online',
  revision: 1,
  source_note: '包信息及 DSH bundle 声明',
  filename: '',
  sha256: '',
};
const defaultPlugins = [
  {
    ...customPlugin,
    id: 'default-openviking-memory',
    name: 'OpenViking 记忆',
    package_name: '@openviking/dsh-memory-plugin',
    version: '0.5.29',
    author: 'OpenViking / volcengine',
    category: '会话与记忆',
    summary: '将 OpenViking 的记忆和上下文能力接入 DSH。',
    description: '由 OpenViking 项目提供的 DSH 组合包。',
    keywords: ['记忆', '上下文', 'memory', 'OpenViking'],
    repository: 'https://github.com/volcengine/OpenViking',
    documentation: 'https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin',
    compatibility: '未声明 DSH 版本范围；Node.js 要求 ^22.19.0 或 >=24。',
    checked_at: '2026-10-27',
    custom: false,
  },
  {
    ...customPlugin,
    id: 'default-dsh-ssh',
    name: 'DSH SSH',
    package_name: '@linxin666/dsh-ssh',
    version: '0.4.11',
    author: 'zhu1090093659 / linxin666',
    category: '开发工具',
    summary: '在 DSH Web 中使用 SSH 终端、SFTP 和远程主机工具。',
    description: '为 DSH Web 提供远程 SSH 操作，包括主机配置、终端和文件传输。',
    keywords: ['SSH', 'SFTP', '远程', '终端'],
    repository: 'https://github.com/zhu1090093659/dsh-web',
    documentation: 'https://github.com/zhu1090093659/dsh-web#readme',
    compatibility: '包声明 DSH >=0.2.1-rc.3；Node.js ^22.19.0 或 >=24.0.0。',
    checked_at: '2026-10-27',
    custom: false,
  },
  {
    ...customPlugin,
    id: 'default-dshmarket',
    name: 'DSH Market',
    package_name: 'dshmarket',
    version: '1.71.1',
    author: 'dsh-market',
    category: '集成扩展',
    summary: '社区维护的 DSH 插件发现与浏览工具。',
    description: '该第三方组合包在 DSH 中提供社区目录的浏览和搜索。',
    keywords: ['市场', '目录', 'marketplace', '发现'],
    repository: 'https://github.com/dsh-market/dsh-market',
    documentation: 'https://github.com/dsh-market/dsh-market',
    compatibility: '未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。',
    checked_at: '2026-10-27',
    custom: false,
  },
  {
    ...customPlugin,
    id: 'mem0-memory',
    name: 'Mem0 记忆',
    package_name: '@harness-cloud/dsh-mem0-memory',
    version: '0.1.0',
    author: 'Harness Cloud',
    category: '会话与记忆',
    summary: '连接自建 Mem0，自动召回并按需记录会话记忆。',
    description: 'Harness Cloud 维护的 Mem0 DSH 插件，非 Mem0 官方插件。',
    keywords: ['记忆', 'Mem0', '自动召回', 'memory'],
    repository: '',
    documentation: '',
    compatibility: 'DSH 0.2.1-alpha.2；Node.js >=24。',
    checked_at: '2026-11-03',
    source_note: '本地维护的 DSH 组合包，随平台分发离线包',
    custom: false,
  },
];
// A deterministic npm tarball containing package/package.json and package/plugin.js.
const offlineArchive = Buffer.from(
  'H4sIAAAAAAACA+3TvU7DMBAH8Mw8heUZ0nyUIJWFgYEFMfAEJnZaQ2pb/kBFUd6dS6Mi1IWlVEL8f5J19vmSk6KcE+2bWKuFm2P+GqzJTqwgzXK5j+Q4FtV187Xf58uyqcuMFdkZpBCFp/bZ/zRwI7aKr/hdVCEuZNhc2a7rtVH8kr8rH7Q1dFvnVV5SRqrQeu3inH2aK9n98wNzfVprwzq9i8lPD4sUN9Z/q3oU2kRaytNt/HBT162VqZ+qqTFfDfwlGUln2jkRW0rx+b30X/JxHDM4MXeY/8Nn/oUeP81/UdXH8182N5j/c1A7Z31kUnUi9ZEN4+0FhgIAAAAAAAAAAAAAAAAAAADgD/kEIbsi3QAoAAA=',
  'base64',
);
const offlinePlugin = {
  ...customPlugin,
  id: '00000000-0000-4000-8000-000000000012',
  name: '离线项目助手',
  package_name: '@test/dsh-offline',
  version: '3.2.1',
  author: '离线插件维护者',
  summary: '通过本地插件包提供项目工具。',
  description: '从 npm 压缩包添加的 DSH 插件。',
  repository: '',
  documentation: '',
  addition_method: 'offline',
  filename: 'dsh-offline-3.2.1.tgz',
  sha256: '74142c81bb9d2a746ae768df34389451432156af57394b968ab79961a70e8ed0',
  source_note: '上传包信息及 DSH bundle 声明',
};
async function setup(page: Page, permissions = user.permissions) {
  await page.route('**/livez', (route) =>
    route.fulfill({
      json: { status: 'alive', process: 'api', capabilities: { authentication: true } },
    }),
  );
  await page.route('**/readyz', (route) =>
    route.fulfill({ json: { status: 'ready', process: 'api' } }),
  );
  await page.route('**/v1/me', (route) => route.fulfill({ json: { ...user, permissions } }));
  await page.route('**/v1/plugins', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: { items: defaultPlugins } })
      : route.fallback(),
  );
}
async function select(page: Page, label: string, option: string) {
  await page
    .locator('.ant-select')
    .filter({ has: page.getByRole('combobox', { name: label, exact: true }) })
    .click();
  await page.locator('.ant-select-dropdown:visible').getByTitle(option, { exact: true }).click();
}
function metadata(plugin: typeof customPlugin) {
  const {
    name,
    summary,
    description,
    author,
    version,
    category,
    keywords,
    repository,
    documentation,
    compatibility,
    checked_at,
    source_note,
  } = plugin;
  return {
    name,
    summary,
    description,
    author,
    version,
    category,
    keywords,
    repository,
    documentation,
    compatibility,
    checked_at,
    source_note,
  };
}

test('plugin square navigation and responsive catalog work without querying or changing a workspace', async ({
  page,
}, info) => {
  await setup(page, [...user.permissions, 'memory:read', 'knowledge:read', 'personal-model:read']);
  const workspaceRequests: string[] = [],
    writes: string[] = [],
    errors: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      path.startsWith('/v1/instances') ||
      path.startsWith('/__dsh/') ||
      path.startsWith('/api/plugin')
    )
      workspaceRequests.push(request.url());
    if (!['GET', 'HEAD'].includes(request.method())) writes.push(request.url());
  });
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/app/plugins');
  if (info.project.name === 'mobile')
    await page.getByRole('button', { name: '打开导航', exact: true }).click();
  const nav = page
    .getByRole('navigation', { name: '主导航', exact: true })
    .filter({ visible: true });
  await expect(nav.getByRole('link', { name: '插件', exact: true })).toBeVisible();
  const labels = await nav.getByRole('link').allTextContents();
  expect(labels.indexOf('插件')).toBe(labels.indexOf('知识库') + 1);
  expect(labels.indexOf('模型提供商')).toBe(labels.indexOf('插件') + 1);
  await nav.getByRole('link', { name: '插件', exact: true }).click();
  await expect(page.getByRole('heading', { name: '插件', exact: true })).toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(4);
  for (const name of ['OpenViking 记忆', 'DSH SSH', 'DSH Market', 'Mem0 记忆'])
    await expect(page.getByRole('article', { name, exact: true })).toBeVisible();
  await expect(page.getByText('官方', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('combobox', { name: '插件来源', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '添加插件', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '打开插件管理', exact: true })).toHaveCount(0);
  await expect(page.getByRole('radio')).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('plugin-square.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await page
    .getByRole('article', { name: 'OpenViking 记忆', exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  await expect(page.getByRole('dialog', { name: '插件详情', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '编辑信息', exact: true })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: info.outputPath('plugin-details.png'), animations: 'disabled' });
  await page.keyboard.press('Escape');
  let catalogState: 'empty' | 'failed' | 'loaded' = 'empty';
  await page.route('**/v1/plugins', (route) =>
    route.fulfill(
      catalogState === 'failed'
        ? { status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }
        : { json: { items: catalogState === 'loaded' ? defaultPlugins : [] } },
    ),
  );
  await page.reload();
  await expect(page.getByRole('article')).toHaveCount(0);
  await expect(page.getByText('没有符合条件的插件', { exact: true })).toBeVisible();
  catalogState = 'failed';
  await page.reload();
  await expect(page.getByRole('alert').filter({ hasText: '依赖服务暂不可用' })).toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(0);
  catalogState = 'loaded';
  await page.getByRole('button', { name: /^重\s*试$/ }).click();
  await expect(page.getByRole('article')).toHaveCount(4);
  expect(workspaceRequests).toEqual([]);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});

test('search and categories cover third-party and local packages, authors and capabilities', async ({
  page,
}) => {
  await setup(page);
  await page.goto('/app/plugins');
  await expect(page.getByRole('article')).toHaveCount(4);
  const search = page.getByRole('searchbox', { name: '搜索插件', exact: true });
  await search.fill('@openviking/dsh-memory-plugin');
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(page.getByRole('article', { name: 'OpenViking 记忆', exact: true })).toBeVisible();
  await search.fill('zhu1090093659');
  await expect(page.getByRole('article', { name: 'DSH SSH', exact: true })).toBeVisible();
  await search.fill('SFTP');
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(page.getByRole('article', { name: 'DSH SSH', exact: true })).toBeVisible();
  for (const removed of ['DeepSeek', '@deepseek-ai/dsh-experimental-session-search']) {
    await search.fill(removed);
    await expect(page.getByRole('article')).toHaveCount(0);
    await expect(page.getByText('没有符合条件的插件', { exact: true })).toBeVisible();
  }
  await page.getByRole('button', { name: '清除筛选', exact: true }).click();
  await expect(search).toHaveValue('');
  await expect(page.getByRole('article')).toHaveCount(4);
  await page
    .locator('.ant-select')
    .filter({ has: page.getByRole('combobox', { name: '插件分类', exact: true }) })
    .click();
  await expect(
    page.locator('.ant-select-dropdown:visible .ant-select-item-option-content'),
  ).toHaveText(['全部分类', '会话与记忆', '开发工具', '集成扩展']);
  await page
    .locator('.ant-select-dropdown:visible')
    .getByTitle('会话与记忆', { exact: true })
    .click();
  await expect(page.getByRole('article')).toHaveCount(2);
  for (const name of ['OpenViking 记忆', 'Mem0 记忆'])
    await expect(page.getByRole('article', { name, exact: true })).toBeVisible();
  await search.fill('SSH');
  await expect(page.getByRole('article')).toHaveCount(0);
  await search.fill('');
  await select(page, '插件分类', '开发工具');
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(page.getByRole('article', { name: 'DSH SSH', exact: true })).toBeVisible();
  await select(page, '插件分类', '集成扩展');
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(page.getByRole('article', { name: 'DSH Market', exact: true })).toBeVisible();
  await select(page, '插件分类', '全部分类');
  await expect(page.getByRole('article')).toHaveCount(4);
});

test('details expose pinned version, provenance, compatibility and manual install guidance; copying does not install', async ({
  page,
  context,
}) => {
  await setup(page);
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const writes: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST') writes.push(request.url());
  });
  await page.goto('/app/plugins');
  await page
    .getByRole('article', { name: 'DSH SSH', exact: true })
    .getByRole('heading')
    .getByRole('button')
    .click();
  const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(drawer).toContainText('0.4.11');
  await expect(drawer).toContainText('>=0.2.1-rc.3');
  await expect(drawer).toContainText('来源核对');
  await expect(drawer).toContainText('安装与启用由你在 DSH 中完成');
  await expect(drawer.getByRole('link', { name: '源代码', exact: true })).toHaveAttribute(
    'href',
    'https://github.com/zhu1090093659/dsh-web',
  );
  await expect(drawer.getByRole('link', { name: '源代码', exact: true })).toHaveAttribute(
    'rel',
    'noopener noreferrer',
  );
  await expect(drawer.getByRole('link', { name: 'npm 包', exact: true })).toHaveAttribute(
    'href',
    'https://www.npmjs.com/package/@linxin666/dsh-ssh/v/0.4.11',
  );
  await drawer.getByRole('button', { name: '复制包名', exact: true }).click();
  await expect(drawer.getByRole('status')).toHaveText('包名与版本已复制');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    '@linxin666/dsh-ssh@0.4.11',
  );
  expect(writes).toEqual([]);
  await page.keyboard.press('Escape');
  await expect(drawer).not.toBeVisible();
  await page
    .getByRole('article', { name: 'DSH Market', exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  await expect(page.getByRole('dialog')).toContainText('dshmarket@1.71.1');
  await expect(page.getByRole('button', { name: '复制包名', exact: true })).toBeVisible();
});

test('clipboard failure keeps a selectable package and detail close preserves filters', async ({
  page,
}) => {
  await setup(page);
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async () => {
          throw new Error('denied');
        },
      },
    });
  });
  await page.goto('/app/plugins');
  await select(page, '插件分类', '会话与记忆');
  await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('OpenViking');
  await page.getByRole('article').getByRole('button', { name: '查看详情', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
  await drawer.getByRole('button', { name: '复制包名', exact: true }).click();
  await expect(drawer).toContainText('复制失败，请手动选择并复制上方包名');
  await expect(drawer.getByLabel('插件安装包名', { exact: true })).toHaveText(
    '@openviking/dsh-memory-plugin@0.5.29',
  );
  await page.keyboard.press('Escape');
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(page.getByRole('searchbox', { name: '搜索插件', exact: true })).toHaveValue(
    'OpenViking',
  );
  await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('不存在的插件');
  await expect(page.getByText('没有符合条件的插件', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '清除筛选', exact: true }).click();
  await expect(page.getByRole('article')).toHaveCount(4);
  await expect(page.getByRole('searchbox', { name: '搜索插件', exact: true })).toHaveValue('');
});

test('the locally maintained Mem0 plugin explains explicit capture and downloads its pinned bundle without installing', async ({
  page,
}, info) => {
  await setup(page);
  const requests: string[] = [],
    writes: string[] = [];
  let downloads = 0;
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      path.startsWith('/v1/instances') ||
      path.startsWith('/__dsh/') ||
      path.startsWith('/api/plugin') ||
      path.endsWith('/archive')
    )
      requests.push(path);
    if (!['GET', 'HEAD'].includes(request.method())) writes.push(request.url());
  });
  const filename = 'harness-cloud-dsh-mem0-memory-0.1.0.tgz';
  await page.route('**/plugin-packages/' + filename, (route) => {
    downloads++;
    expect(route.request().method()).toBe('GET');
    if (downloads === 1) return route.fulfill({ status: 503, body: 'unavailable' });
    if (downloads === 2)
      return route.fulfill({
        contentType: 'text/html',
        body: '<!doctype html><h1>Missing asset</h1>',
      });
    return route.fulfill({ contentType: 'application/gzip', body: offlineArchive });
  });
  await page.goto('/app/plugins');
  await page.getByRole('searchbox', { name: '搜索插件', exact: true }).fill('Mem0');
  const card = page.getByRole('article', { name: 'Mem0 记忆', exact: true });
  await expect(page.getByRole('article')).toHaveCount(1);
  await expect(card.getByText('本地维护', { exact: true })).toBeVisible();
  await card.getByRole('button', { name: '查看详情', exact: true }).click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(details).toContainText('非 Mem0 官方插件');
  await expect(details.getByRole('link', { name: 'npm 包', exact: true })).toHaveCount(0);
  await expect(details).toContainText('插件包的绝对路径');
  await expect(details).toContainText('包内 README');
  const config = details.getByRole('region', { name: 'Mem0 配置说明', exact: true });
  for (const field of ['endpoint', 'apiKey', 'userId', 'autoCapture'])
    await expect(config).toContainText(field);
  await expect(config).toContainText('默认关闭，需显式开启');
  await expect(config).toContainText('默认只保存用户消息');
  await expect(config).toContainText('infer 默认为 false');
  await expect(config).toContainText('自动召回');
  await expect(config).toContainText('与平台记忆连接分别配置');
  await expect(details.getByRole('button', { name: '编辑信息', exact: true })).toHaveCount(0);
  const downloadButton = details.getByRole('button', { name: '下载插件包', exact: true }),
    browserDownloads: string[] = [];
  page.on('download', (download) => browserDownloads.push(download.suggestedFilename()));
  for (let attempt = 0; attempt < 2; attempt++) {
    await downloadButton.click();
    await expect(details.getByRole('alert')).toContainText('插件包暂不可用，请稍后重试');
    await expect(downloadButton).toBeEnabled();
  }
  expect(browserDownloads).toEqual([]);
  const event = page.waitForEvent('download');
  await downloadButton.click();
  const download = await event;
  expect(download.suggestedFilename()).toBe(filename);
  expect(await readFile((await download.path())!)).toEqual(offlineArchive);
  await expect(details.getByRole('alert')).toHaveCount(0);
  await expect(downloadButton).toBeEnabled();
  expect(downloads).toBe(3);
  expect(browserDownloads).toEqual([filename]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await details.screenshot({
    path: info.outputPath('mem0-plugin-details.png'),
    animations: 'disabled',
  });
  expect(requests).toEqual([]);
  expect(writes).toEqual([]);
});

test('an edited Mem0 version cannot download a different bundled version or offer an unpublished npm link', async ({
  page,
}) => {
  await setup(page);
  const mem0 = defaultPlugins[3];
  await page.route('**/v1/plugins', (route) =>
    route.fulfill({ json: { items: [{ ...mem0, version: '0.2.0' }] } }),
  );
  await page.goto('/app/plugins');
  await page
    .getByRole('article', { name: mem0.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(details).toContainText('此版本没有随平台分发的插件包');
  await expect(details.getByRole('button', { name: '下载插件包', exact: true })).toHaveCount(0);
  await expect(details.getByRole('link', { name: 'npm 包', exact: true })).toHaveCount(0);
});

test('a personally uploaded Mem0 archive retains its original download', async ({ page }) => {
  await setup(page);
  const mem0 = {
    ...offlinePlugin,
    package_name: '@harness-cloud/dsh-mem0-memory',
    version: '0.1.0',
  };
  let downloads = 0;
  await page.route('**/v1/plugins', (route) => route.fulfill({ json: { items: [mem0] } }));
  await page.route(`**/v1/plugins/${mem0.id}/archive`, (route) => {
    downloads++;
    return route.fulfill({ contentType: 'application/gzip', body: offlineArchive });
  });
  await page.route('**/plugin-packages/**', () => {
    throw new Error('Must download the personally uploaded archive');
  });
  await page.goto('/app/plugins');
  await page
    .getByRole('article', { name: mem0.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(details).toContainText(mem0.sha256);
  const event = page.waitForEvent('download');
  await details.getByRole('button', { name: '下载插件包', exact: true }).click();
  const download = await event;
  expect(download.suggestedFilename()).toBe(mem0.filename);
  expect(await readFile((await download.path())!)).toEqual(offlineArchive);
  expect(downloads).toBe(1);
});

test('adding an npm plugin reads its pinned metadata and persists it across reload without installing', async ({
  page,
}, info) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  let saved = false,
    previews = 0,
    saves = 0,
    lists = 0;
  const workspaceRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      path.startsWith('/v1/instances') ||
      path.startsWith('/__dsh/') ||
      path.startsWith('/api/plugin')
    )
      workspaceRequests.push(request.url());
  });
  await page.route('**/v1/plugins:preview', (route) => {
    previews++;
    expect(route.request().postDataJSON()).toEqual({ package_name: '@test/dsh-docs@1.2.3' });
    return route.fulfill({ json: customPlugin });
  });
  await page.route('**/v1/plugins', async (route) => {
    if (route.request().method() === 'GET') {
      lists++;
      return route.fulfill({
        json: { items: [...defaultPlugins, ...(saved ? [customPlugin] : [])] },
      });
    }
    saves++;
    expect(route.request().postDataJSON()).toEqual({
      package_name: customPlugin.package_name,
      version: customPlugin.version,
      category: customPlugin.category,
    });
    expect(await route.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    saved = true;
    return route.fulfill({ json: customPlugin });
  });
  await page.goto('/app/plugins');
  await expect(page.getByRole('article')).toHaveCount(4);
  const search = page.getByRole('searchbox', { name: '搜索插件', exact: true });
  await search.fill('当前筛选不包含新插件');
  await select(page, '插件分类', '会话与记忆');
  await page.getByRole('button', { name: '添加插件', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '添加插件', exact: true });
  const input = modal.getByRole('textbox', { name: 'npm 包名', exact: true }),
    submit = modal.getByRole('button', { name: /^添\s*加$/ });
  await expect(modal.getByText('集成扩展', { exact: true })).toBeVisible();
  await expect(submit).toBeDisabled();
  await expect(modal.getByRole('checkbox')).toHaveCount(0);
  await input.fill('@test/dsh-docs@1.2.3');
  await expect(submit).toBeDisabled();
  await modal.getByRole('button', { name: '读取信息', exact: true }).click();
  await expect(modal).toContainText(customPlugin.package_name);
  await expect(modal).toContainText(customPlugin.version);
  await expect(modal).toContainText(customPlugin.author);
  await expect(modal).toContainText(customPlugin.summary);
  await expect(submit).toBeEnabled();
  expect(saves).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('plugin-add-preview.png'),
    animations: 'disabled',
  });
  await submit.click();
  await expect(modal).not.toBeVisible();
  await expect(search).toHaveValue('');
  await expect(page.getByRole('article')).toHaveCount(5);
  await expect(page.getByRole('article', { name: customPlugin.name, exact: true })).toBeVisible();
  await expect(
    page
      .locator('.ant-select')
      .filter({ has: page.getByRole('combobox', { name: '插件分类', exact: true }) }),
  ).toContainText('全部分类');
  await page.reload();
  await expect(page.getByRole('article', { name: customPlugin.name, exact: true })).toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(5);
  expect(previews).toBe(1);
  expect(saves).toBe(1);
  expect(lists).toBeGreaterThanOrEqual(2);
  expect(workspaceRequests).toEqual([]);
  await page.screenshot({
    path: info.outputPath('plugin-added.png'),
    fullPage: true,
    animations: 'disabled',
  });
});

test('npm plugin errors preserve input and changing the package invalidates its preview before saving', async ({
  page,
}) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  const replacement = {
    ...customPlugin,
    id: '00000000-0000-4000-8000-000000000011',
    package_name: '@test/dsh-shell',
    name: '项目 Shell 工具',
    version: '2.0.0',
    summary: '提供项目 Shell 工具。',
  };
  let previews = 0,
    saves = 0,
    saved = false;
  await page.route('**/v1/plugins:preview', (route) => {
    previews++;
    const packageName = route.request().postDataJSON().package_name;
    if (previews === 1) {
      expect(packageName).toBe('@test/missing-plugin');
      return route.fulfill({
        status: 503,
        json: { code: 'DEPENDENCY_UNAVAILABLE', message: 'npm 服务暂时不可用' },
      });
    }
    expect(packageName).toBe(previews === 2 ? '@test/dsh-docs@1.2.3' : '@test/dsh-shell@2.0.0');
    return route.fulfill({ json: previews === 2 ? customPlugin : replacement });
  });
  await page.route('**/v1/plugins', (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({
        json: { items: [...defaultPlugins, ...(saved ? [replacement] : [])] },
      });
    saves++;
    expect(route.request().postDataJSON()).toEqual({
      package_name: replacement.package_name,
      version: replacement.version,
      category: replacement.category,
    });
    if (saves === 1)
      return route.fulfill({
        status: 503,
        json: { code: 'DEPENDENCY_UNAVAILABLE', message: '暂时无法保存插件' },
      });
    saved = true;
    return route.fulfill({ json: replacement });
  });
  await page.goto('/app/plugins');
  await page.getByRole('button', { name: '添加插件', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '添加插件', exact: true });
  const input = modal.getByRole('textbox', { name: 'npm 包名', exact: true }),
    read = modal.getByRole('button', { name: '读取信息', exact: true }),
    submit = modal.getByRole('button', { name: /^添\s*加$/ });
  await input.fill('@test/missing-plugin');
  await read.click();
  await expect(modal.getByRole('alert')).toBeVisible();
  await expect(input).toHaveValue('@test/missing-plugin');
  await expect(submit).toBeDisabled();
  expect(saves).toBe(0);
  await input.fill('@test/dsh-docs@1.2.3');
  await read.click();
  await expect(modal).toContainText(customPlugin.summary);
  await expect(submit).toBeEnabled();
  await input.fill('@test/dsh-shell@2.0.0');
  await expect(submit).toBeDisabled();
  await expect(modal).not.toContainText(customPlugin.summary);
  expect(saves).toBe(0);
  await read.click();
  await expect(modal).toContainText(replacement.summary);
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(modal.getByRole('alert')).toBeVisible();
  await expect(input).toHaveValue('@test/dsh-shell@2.0.0');
  await expect(submit).toBeEnabled();
  await expect(modal).toBeVisible();
  await submit.click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole('article', { name: replacement.name, exact: true })).toBeVisible();
  expect(previews).toBe(3);
  expect(saves).toBe(2);
});

test('an offline npm archive persists and downloads safely across transient errors and an expired session', async ({
  page,
}, info) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  let saved = false,
    previews = 0,
    saves = 0,
    downloads = 0;
  let expired = false;
  await page.route('**/v1/me', (route) =>
    route.fulfill(
      expired
        ? { status: 401, json: { code: 'UNAUTHENTICATED' } }
        : { json: { ...user, permissions: [...user.permissions, 'plugins:manage'] } },
    ),
  );
  let releaseRead!: () => void, releaseSave!: () => void, releaseDownload!: () => void;
  const readGate = new Promise<void>((resolve) => {
      releaseRead = resolve;
    }),
    saveGate = new Promise<void>((resolve) => {
      releaseSave = resolve;
    }),
    downloadGate = new Promise<void>((resolve) => {
      releaseDownload = resolve;
    });
  const workspaceRequests: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      path.startsWith('/v1/instances') ||
      path.startsWith('/__dsh/') ||
      path.startsWith('/api/plugin')
    )
      workspaceRequests.push(request.url());
  });
  await page.route('**/v1/plugins:preview', async (route) => {
    previews++;
    expect(route.request().postDataJSON()).toEqual({
      filename: offlinePlugin.filename,
      content_base64: offlineArchive.toString('base64'),
    });
    expect(await route.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    await readGate;
    return route.fulfill({ json: offlinePlugin });
  });
  await page.route('**/v1/plugins', async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({
        json: {
          items: [
            ...defaultPlugins,
            ...(saved ? [{ ...offlinePlugin, category: '开发工具' }] : []),
          ],
        },
      });
    saves++;
    expect(route.request().postDataJSON()).toEqual({
      filename: offlinePlugin.filename,
      content_base64: offlineArchive.toString('base64'),
      category: '开发工具',
    });
    expect(await route.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    await saveGate;
    saved = true;
    return route.fulfill({ json: { ...offlinePlugin, category: '开发工具' } });
  });
  await page.route(`**/v1/plugins/${offlinePlugin.id}/archive`, async (route) => {
    downloads++;
    expect(route.request().method()).toBe('GET');
    if (downloads === 1)
      return route.fulfill({
        status: 503,
        json: { code: 'PLUGIN_UNAVAILABLE', message: '插件包暂不可用，请稍后重试' },
      });
    if (downloads === 3) {
      expired = true;
      return route.fulfill({ status: 401, json: { code: 'UNAUTHENTICATED' } });
    }
    await downloadGate;
    return route.fulfill({
      contentType: 'application/gzip',
      headers: { 'content-disposition': `attachment; filename="${offlinePlugin.filename}"` },
      body: offlineArchive,
    });
  });
  await page.route('**/auth/login?*', (route) => {
    expect(new URL(route.request().url()).searchParams.get('return_to')).toBe('/app/plugins');
    return route.fulfill({ contentType: 'text/html; charset=utf-8', body: '<h1>登录账户</h1>' });
  });
  await page.goto('/app/plugins');
  await page.getByRole('button', { name: '添加插件', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '添加插件', exact: true }),
    read = modal.getByRole('button', { name: '读取信息', exact: true }),
    submit = modal.getByRole('button', { name: /^添\s*加$/ });
  await expect(modal.getByRole('radio', { name: '在线', exact: true })).toBeChecked();
  await modal.getByText('离线', { exact: true }).click();
  await expect(modal.getByRole('radio', { name: '离线', exact: true })).toBeChecked();
  const upload = modal.getByLabel('插件压缩包', { exact: true });
  await expect(modal.getByRole('button', { name: '选择插件包', exact: true })).toBeVisible();
  await expect(read).toBeDisabled();
  await upload.setInputFiles({
    name: offlinePlugin.filename,
    mimeType: 'application/gzip',
    buffer: offlineArchive,
  });
  await expect(modal).toContainText(offlinePlugin.filename);
  await expect(submit).toBeDisabled();
  expect(previews).toBe(0);
  expect(saves).toBe(0);
  await modal.getByText('集成扩展', { exact: true }).click();
  await page
    .locator('.ant-select-dropdown:visible')
    .getByTitle('开发工具', { exact: true })
    .click();
  await read.click();
  await expect(read).toHaveAttribute('aria-busy', 'true');
  for (const mode of ['在线', '离线'])
    await expect(modal.getByRole('radio', { name: mode, exact: true })).toBeDisabled();
  await expect(upload).toBeDisabled();
  await expect(read).toBeDisabled();
  await expect(submit).toBeDisabled();
  await expect(modal.getByRole('button', { name: /^取\s*消$/ })).toBeDisabled();
  await expect(modal.locator('.ant-modal-close')).not.toBeVisible();
  await page.keyboard.press('Escape');
  await expect(modal).toBeVisible();
  expect(previews).toBe(1);
  expect(saves).toBe(0);
  releaseRead();
  const preview = modal.getByRole('region', { name: '插件信息', exact: true });
  await expect(preview).toContainText(offlinePlugin.package_name);
  await expect(preview).toContainText(offlinePlugin.version);
  await expect(preview).toContainText(offlinePlugin.summary);
  await expect(submit).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('plugin-offline-preview.png'),
    animations: 'disabled',
  });
  await submit.click();
  await expect(submit).toHaveAttribute('aria-busy', 'true');
  await expect(submit).toBeDisabled();
  await expect(read).toBeDisabled();
  await expect(upload).toBeDisabled();
  for (const mode of ['在线', '离线'])
    await expect(modal.getByRole('radio', { name: mode, exact: true })).toBeDisabled();
  await expect(modal.getByRole('button', { name: /^取\s*消$/ })).toBeDisabled();
  await expect(modal.locator('.ant-modal-close')).not.toBeVisible();
  await page.keyboard.press('Escape');
  await expect(modal).toBeVisible();
  expect(saves).toBe(1);
  releaseSave();
  await expect(modal).not.toBeVisible();
  const card = page.getByRole('article', { name: offlinePlugin.name, exact: true });
  await expect(card).toBeVisible();
  await expect(card.getByText('离线', { exact: true })).toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(5);
  await page.reload();
  await expect(card).toBeVisible();
  await expect(card.getByText('离线', { exact: true })).toBeVisible();
  await card.getByRole('button', { name: '查看详情', exact: true }).click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(details).toContainText(offlinePlugin.filename);
  await expect(details).toContainText(offlinePlugin.sha256);
  const browserDownloads: string[] = [];
  page.on('download', (download) => browserDownloads.push(download.suggestedFilename()));
  const downloadButton = details.getByRole('button', { name: '下载插件包', exact: true });
  await downloadButton.click();
  await expect(
    details.getByRole('alert').filter({ hasText: '插件包暂不可用，请稍后重试' }),
  ).toBeVisible();
  await expect(downloadButton).toBeEnabled();
  expect(downloads).toBe(1);
  expect(browserDownloads).toEqual([]);
  const downloadEvent = page.waitForEvent('download');
  await downloadButton.click();
  await expect(downloadButton).toBeDisabled();
  await expect(
    details.getByRole('alert').filter({ hasText: '插件包暂不可用，请稍后重试' }),
  ).toHaveCount(0);
  await expect.poll(() => downloads).toBe(2);
  releaseDownload();
  const download = await downloadEvent;
  expect(download.suggestedFilename()).toBe(offlinePlugin.filename);
  expect(await readFile((await download.path())!)).toEqual(offlineArchive);
  await expect(downloadButton).toBeEnabled();
  await downloadButton.click();
  await expect(page).toHaveURL(/\/auth\/login\?/);
  await expect(page.getByRole('heading', { name: '登录账户', exact: true })).toBeVisible();
  expect(browserDownloads).toEqual([offlinePlugin.filename]);
  expect(previews).toBe(1);
  expect(saves).toBe(1);
  expect(downloads).toBe(3);
  expect(workspaceRequests).toEqual([]);
});

test('switching plugin addition methods and replacing an archive cannot reuse stale metadata or errors', async ({
  page,
}) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  const replacement = {
    ...offlinePlugin,
    id: '00000000-0000-4000-8000-000000000013',
    name: '另一离线项目助手',
    filename: 'another-plugin.tar.gz',
    version: '4.0.0',
    summary: '替换文件后的插件信息。',
  };
  const previewBodies: unknown[] = [],
    saveBodies: unknown[] = [];
  let onlineReads = 0;
  await page.route('**/v1/plugins:preview', (route) => {
    const body = route.request().postDataJSON();
    previewBodies.push(body);
    if (body.package_name) {
      onlineReads++;
      return route.fulfill(
        onlineReads === 1
          ? { json: customPlugin }
          : { status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } },
      );
    }
    return route.fulfill({
      json: body.filename === replacement.filename ? replacement : offlinePlugin,
    });
  });
  await page.route('**/v1/plugins', (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { items: defaultPlugins } });
    saveBodies.push(route.request().postDataJSON());
    return route.fulfill({ json: replacement });
  });
  await page.goto('/app/plugins');
  await page.getByRole('button', { name: '添加插件', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '添加插件', exact: true }),
    read = modal.getByRole('button', { name: '读取信息', exact: true }),
    submit = modal.getByRole('button', { name: /^添\s*加$/ });
  await modal.getByRole('textbox', { name: 'npm 包名', exact: true }).fill('@test/dsh-docs@1.2.3');
  await read.click();
  await expect(modal).toContainText(customPlugin.summary);
  await modal.getByText('离线', { exact: true }).click();
  await expect(modal.getByRole('region', { name: '插件信息', exact: true })).toHaveCount(0);
  await expect(submit).toBeDisabled();
  const upload = modal.getByLabel('插件压缩包', { exact: true });
  await upload.setInputFiles({
    name: offlinePlugin.filename,
    mimeType: 'application/gzip',
    buffer: offlineArchive,
  });
  await read.click();
  await expect(modal).toContainText(offlinePlugin.summary);
  await modal.getByText('在线', { exact: true }).click();
  await expect(modal.getByRole('region', { name: '插件信息', exact: true })).toHaveCount(0);
  await expect(submit).toBeDisabled();
  await modal.getByRole('textbox', { name: 'npm 包名', exact: true }).fill('@test/missing-plugin');
  await read.click();
  await expect(modal.getByRole('alert')).toBeVisible();
  await modal.getByText('离线', { exact: true }).click();
  await expect(modal.getByRole('alert')).toHaveCount(0);
  await expect(submit).toBeDisabled();
  await upload.setInputFiles({
    name: offlinePlugin.filename,
    mimeType: 'application/gzip',
    buffer: offlineArchive,
  });
  await read.click();
  await expect(modal).toContainText(offlinePlugin.summary);
  await upload.setInputFiles({
    name: replacement.filename,
    mimeType: 'application/gzip',
    buffer: offlineArchive,
  });
  await expect(modal).toContainText(replacement.filename);
  await expect(modal.getByRole('region', { name: '插件信息', exact: true })).toHaveCount(0);
  await expect(submit).toBeDisabled();
  expect(saveBodies).toEqual([]);
  await read.click();
  await expect(modal).toContainText(replacement.summary);
  await expect(modal).not.toContainText(offlinePlugin.summary);
  await submit.click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole('article', { name: replacement.name, exact: true })).toBeVisible();
  expect(previewBodies).toEqual([
    { package_name: '@test/dsh-docs@1.2.3' },
    { filename: offlinePlugin.filename, content_base64: offlineArchive.toString('base64') },
    { package_name: '@test/missing-plugin' },
    { filename: offlinePlugin.filename, content_base64: offlineArchive.toString('base64') },
    { filename: replacement.filename, content_base64: offlineArchive.toString('base64') },
  ]);
  expect(saveBodies).toEqual([
    {
      filename: replacement.filename,
      content_base64: offlineArchive.toString('base64'),
      category: replacement.category,
    },
  ]);
});

test('invalid offline archives stay local and preview or save failures preserve the selected file for retry', async ({
  page,
}) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  let previews = 0,
    saves = 0;
  await page.route('**/v1/plugins:preview', (route) => {
    previews++;
    expect(route.request().postDataJSON()).toEqual({
      filename: offlinePlugin.filename,
      content_base64: offlineArchive.toString('base64'),
    });
    return route.fulfill(
      previews === 1
        ? { status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }
        : { json: offlinePlugin },
    );
  });
  await page.route('**/v1/plugins', (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { items: defaultPlugins } });
    saves++;
    expect(route.request().postDataJSON()).toEqual({
      filename: offlinePlugin.filename,
      content_base64: offlineArchive.toString('base64'),
      category: offlinePlugin.category,
    });
    return route.fulfill(
      saves === 1
        ? { status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }
        : { json: offlinePlugin },
    );
  });
  await page.goto('/app/plugins');
  await page.getByRole('button', { name: '添加插件', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '添加插件', exact: true }),
    read = modal.getByRole('button', { name: '读取信息', exact: true }),
    submit = modal.getByRole('button', { name: /^添\s*加$/ });
  await modal.getByText('离线', { exact: true }).click();
  const upload = modal.getByLabel('插件压缩包', { exact: true });
  await upload.setInputFiles({
    name: 'plugin.zip',
    mimeType: 'application/zip',
    buffer: offlineArchive,
  });
  await expect(modal.getByRole('alert')).toContainText(/\.tgz|\.tar\.gz/);
  await expect(read).toBeDisabled();
  await expect(submit).toBeDisabled();
  await upload.setInputFiles({
    name: 'too-large.tar.gz',
    mimeType: 'application/gzip',
    buffer: Buffer.alloc(5 * 1024 * 1024 + 1),
  });
  await expect(modal.getByRole('alert')).toContainText(/5\s*(MiB|MB)/);
  await expect(read).toBeDisabled();
  await expect(submit).toBeDisabled();
  expect(previews).toBe(0);
  expect(saves).toBe(0);
  await upload.setInputFiles({
    name: offlinePlugin.filename,
    mimeType: 'application/gzip',
    buffer: offlineArchive,
  });
  await expect(modal.getByRole('alert')).toHaveCount(0);
  await expect(read).toBeEnabled();
  await read.click();
  await expect(modal.getByRole('alert')).toBeVisible();
  await expect(modal).toContainText(offlinePlugin.filename);
  await expect(submit).toBeDisabled();
  await expect(read).toBeEnabled();
  await read.click();
  await expect(modal.getByRole('alert')).toHaveCount(0);
  await expect(modal).toContainText(offlinePlugin.summary);
  await submit.click();
  await expect(modal.getByRole('alert')).toBeVisible();
  await expect(modal).toBeVisible();
  await expect(modal).toContainText(offlinePlugin.filename);
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole('article', { name: offlinePlugin.name, exact: true })).toBeVisible();
  expect(previews).toBe(2);
  expect(saves).toBe(2);
});

test('editing a default plugin replaces its server identity and persists every configurable metadata field', async ({
  page,
}, info) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  const original = defaultPlugins[0];
  let current = original,
    writes = 0;
  const changed = {
    name: '团队 OpenViking',
    summary: '按团队习惯配置的记忆目录。',
    description: '团队的记忆插件说明。',
    author: '记忆维护组',
    version: '0.6.0',
    category: '开发工具',
    keywords: ['企业记忆', '团队', '检索'],
    repository: 'https://github.com/example/team-memory',
    documentation: 'https://docs.example.com/memory',
    compatibility: 'DSH >=0.3.0',
    checked_at: '2026-11-03',
    source_note: '团队已核对包声明',
  };
  let releaseSave!: () => void;
  const saveGate = new Promise<void>((resolve) => {
    releaseSave = resolve;
  });
  await page.route('**/v1/plugins', (route) =>
    route.fulfill({ json: { items: [current, ...defaultPlugins.slice(1)] } }),
  );
  await page.route(`**/v1/plugins/${original.id}`, async (route) => {
    expect(route.request().method()).toBe('PUT');
    expect(await route.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    writes++;
    expect(route.request().postDataJSON()).toEqual({
      ...changed,
      expected_revision: original.revision,
    });
    await saveGate;
    current = {
      ...original,
      ...changed,
      id: '00000000-0000-4000-8000-000000000020',
      custom: true,
      revision: 2,
    };
    return route.fulfill({ json: current });
  });
  await page.goto('/app/plugins');
  await page
    .getByRole('article', { name: original.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await details.getByRole('button', { name: '编辑信息', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '编辑插件信息', exact: true }),
    save = modal.getByRole('button', { name: /^保\s*存$/ });
  await expect(modal).toContainText(original.package_name);
  await expect(modal.getByRole('textbox', { name: '包名', exact: true })).toHaveCount(0);
  for (const [label, value] of [
    ['插件名称', changed.name],
    ['简介', changed.summary],
    ['详细介绍', changed.description],
    ['发布方', changed.author],
    ['收录版本', changed.version],
    ['关键词', '企业记忆, 团队, 检索'],
    ['文档链接', changed.documentation],
    ['源码链接', changed.repository],
    ['版本要求', changed.compatibility],
    ['来源说明', changed.source_note],
  ])
    await modal.getByRole('textbox', { name: label, exact: true }).fill(value);
  await modal.getByLabel('核对日期', { exact: true }).fill(changed.checked_at);
  await modal.locator('.ant-select-selector').click();
  await page
    .locator('.ant-select-dropdown:visible')
    .getByTitle(changed.category, { exact: true })
    .click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await save.scrollIntoViewIfNeeded();
  await expect(save).toBeInViewport();
  await page.screenshot({
    path: info.outputPath('plugin-metadata-editor.png'),
    animations: 'disabled',
  });
  await save.click();
  await expect(save).toBeDisabled();
  await expect(modal.getByRole('button', { name: /^取\s*消$/ })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(modal).toBeVisible();
  await expect.poll(() => writes).toBe(1);
  releaseSave();
  await expect(modal).not.toBeVisible();
  await expect(details).toContainText(changed.name);
  await expect(details).toContainText(changed.description);
  await expect(details).toContainText(changed.source_note);
  await expect(details).toContainText(changed.author);
  await expect(details).toContainText(changed.version);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('article')).toHaveCount(4);
  await expect(page.getByRole('article', { name: original.name, exact: true })).toHaveCount(0);
  await expect(page.getByRole('article', { name: changed.name, exact: true })).toContainText(
    changed.summary,
  );
  await page.reload();
  await expect(page.getByRole('article')).toHaveCount(4);
  await page
    .getByRole('article', { name: changed.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  await details.getByRole('button', { name: '编辑信息', exact: true }).click();
  await expect(modal.getByRole('textbox', { name: '插件名称', exact: true })).toHaveValue(
    changed.name,
  );
  await expect(modal.getByRole('textbox', { name: '简介', exact: true })).toHaveValue(
    changed.summary,
  );
  await expect(modal.getByRole('textbox', { name: '来源说明', exact: true })).toHaveValue(
    changed.source_note,
  );
  await expect(modal.getByLabel('核对日期', { exact: true })).toHaveValue(changed.checked_at);
  await modal.getByRole('button', { name: /^取\s*消$/ }).click();
  expect(writes).toBe(1);
});

for (const existing of [customPlugin, offlinePlugin])
  test(`editing an existing ${existing.addition_method} plugin retains its package and archive identity`, async ({
    page,
  }) => {
    await setup(page, [...user.permissions, 'plugins:manage']);
    let current = existing,
      writes = 0,
      downloads = 0;
    const changed = {
      ...metadata(existing),
      name: `已编辑的${existing.name}`,
      summary: '此简介来自已保存的个人配置。',
      source_note: '',
    };
    await page.route('**/v1/plugins', (route) =>
      route.fulfill({ json: { items: [...defaultPlugins, current] } }),
    );
    await page.route(`**/v1/plugins/${existing.id}`, (route) => {
      expect(route.request().method()).toBe('PUT');
      expect(route.request().postDataJSON()).toEqual({
        ...changed,
        expected_revision: existing.revision,
      });
      writes++;
      current = { ...existing, ...changed, revision: 2 };
      return route.fulfill({ json: current });
    });
    await page.route(`**/v1/plugins/${existing.id}/archive`, (route) => {
      downloads++;
      expect(route.request().method()).toBe('GET');
      return route.fulfill({ contentType: 'application/gzip', body: offlineArchive });
    });
    await page.goto('/app/plugins');
    await page
      .getByRole('article', { name: existing.name, exact: true })
      .getByRole('button', { name: '查看详情', exact: true })
      .click();
    const details = page.getByRole('dialog', { name: '插件详情', exact: true });
    await details.getByRole('button', { name: '编辑信息', exact: true }).click();
    const modal = page.getByRole('dialog', { name: '编辑插件信息', exact: true });
    await modal.getByRole('textbox', { name: '插件名称', exact: true }).fill(changed.name);
    await modal.getByRole('textbox', { name: '简介', exact: true }).fill(changed.summary);
    await modal.getByRole('textbox', { name: '来源说明', exact: true }).fill('');
    await modal.getByRole('button', { name: /^保\s*存$/ }).click();
    await expect(modal).not.toBeVisible();
    await expect(details).toContainText(changed.name);
    await expect(details).not.toContainText(existing.source_note);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('article')).toHaveCount(5);
    await page.reload();
    await page
      .getByRole('article', { name: changed.name, exact: true })
      .getByRole('button', { name: '查看详情', exact: true })
      .click();
    await expect(details.getByLabel('插件安装包名', { exact: true })).toHaveText(
      `${existing.package_name}@${existing.version}`,
    );
    await expect(details).not.toContainText(existing.source_note);
    if (existing.addition_method === 'offline') {
      await expect(details).toContainText(offlinePlugin.filename);
      await expect(details).toContainText(offlinePlugin.sha256);
      const downloadEvent = page.waitForEvent('download');
      await details.getByRole('button', { name: '下载插件包', exact: true }).click();
      const download = await downloadEvent;
      expect(download.suggestedFilename()).toBe(offlinePlugin.filename);
      expect(await readFile((await download.path())!)).toEqual(offlineArchive);
      expect(downloads).toBe(1);
    } else {
      await expect(details.getByRole('button', { name: '下载插件包', exact: true })).toHaveCount(0);
      expect(downloads).toBe(0);
    }
    expect(writes).toBe(1);
  });

test('metadata conflicts require explicit reload while service and permission errors preserve an editable draft', async ({
  page,
}) => {
  await setup(page, [...user.permissions, 'plugins:manage']);
  let current = customPlugin,
    writes = 0,
    lists = 0;
  const firstDraft = '尚未保存的个人名称',
    secondDraft = '服务失败后保留的名称',
    finalName = '重新载入后保存的名称';
  await page.route('**/v1/plugins', (route) => {
    lists++;
    return route.fulfill({ json: { items: [...defaultPlugins, current] } });
  });
  await page.route(`**/v1/plugins/${customPlugin.id}`, (route) => {
    const body = route.request().postDataJSON();
    expect(route.request().method()).toBe('PUT');
    writes++;
    if (writes === 1) {
      expect(body).toEqual({ ...metadata(customPlugin), name: firstDraft, expected_revision: 1 });
      current = {
        ...customPlugin,
        name: '其他会话更新的名称',
        summary: '来自服务器的最新简介。',
        revision: 2,
      };
      return route.fulfill({
        status: 409,
        json: { code: 'PLUGIN_VERSION_CONFLICT', message: '插件信息已更新，请重新载入后保存' },
      });
    }
    expect(body).toEqual({
      ...metadata(current),
      name: writes === 4 ? finalName : secondDraft,
      expected_revision: 2,
    });
    if (writes === 2)
      return route.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } });
    if (writes === 3) return route.fulfill({ status: 403, json: { code: 'FORBIDDEN' } });
    current = { ...current, name: finalName, revision: 3 };
    return route.fulfill({ json: current });
  });
  await page.goto('/app/plugins');
  await page
    .getByRole('article', { name: customPlugin.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  const details = page.getByRole('dialog', { name: '插件详情', exact: true });
  await details.getByRole('button', { name: '编辑信息', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '编辑插件信息', exact: true }),
    name = modal.getByRole('textbox', { name: '插件名称', exact: true }),
    save = modal.getByRole('button', { name: /^保\s*存$/ });
  await name.fill(firstDraft);
  await save.click();
  await expect(modal.getByRole('alert')).toContainText('重新载入');
  await expect(name).toHaveValue(firstDraft);
  await expect(save).toBeDisabled();
  expect(writes).toBe(1);
  expect(lists).toBeGreaterThanOrEqual(1);
  const beforeReload = lists;
  await modal.getByRole('button', { name: '重新载入', exact: true }).click();
  await expect(name).toHaveValue(current.name);
  await expect(modal.getByRole('textbox', { name: '简介', exact: true })).toHaveValue(
    current.summary,
  );
  await expect(modal.getByRole('alert')).toHaveCount(0);
  await expect(save).toBeEnabled();
  expect(writes).toBe(1);
  expect(lists).toBeGreaterThan(beforeReload);
  await name.fill(secondDraft);
  await save.click();
  await expect(modal.getByRole('alert')).toContainText('依赖服务暂不可用');
  await expect(name).toHaveValue(secondDraft);
  await expect(save).toBeEnabled();
  await save.click();
  await expect(modal.getByRole('alert')).toContainText('当前账户没有此操作权限');
  await expect(name).toHaveValue(secondDraft);
  await modal.getByRole('button', { name: /^取\s*消$/ }).click();
  await expect(modal).not.toBeVisible();
  expect(writes).toBe(3);
  await page.reload();
  await page
    .getByRole('article', { name: current.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  await details.getByRole('button', { name: '编辑信息', exact: true }).click();
  await expect(name).toHaveValue(current.name);
  await name.fill(finalName);
  await save.click();
  await expect(modal).not.toBeVisible();
  await expect(details).toContainText(finalName);
  expect(writes).toBe(4);
});

test('direct access and navigation follow the existing read permission', async ({ page }) => {
  await setup(page, []);
  await page.goto('/app/plugins');
  await expect(page.getByText('没有实例读取权限', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '插件', exact: true })).toHaveCount(0);
  await expect(page.getByRole('article')).toHaveCount(0);
  await setup(page, ['plugins:manage']);
  await page.reload();
  await expect(page.getByText('没有实例读取权限', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '添加插件', exact: true })).toHaveCount(0);
  await setup(page, ['instances:read']);
  await page.route('**/v1/plugins', (route) =>
    route.fulfill({ json: { items: [...defaultPlugins, offlinePlugin] } }),
  );
  await page.reload();
  await page
    .getByRole('article', { name: offlinePlugin.name, exact: true })
    .getByRole('button', { name: '查看详情', exact: true })
    .click();
  await expect(page.getByRole('dialog', { name: '插件详情', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '编辑信息', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '添加插件', exact: true })).toHaveCount(0);
});

test('the third-party catalog is keyboard accessible and uses safe public source links', async ({
  page,
}) => {
  await setup(page);
  await page.goto('/app/plugins');
  const titleButton = page
    .getByRole('article', { name: 'DSH Market', exact: true })
    .getByRole('heading')
    .getByRole('button');
  await titleButton.focus();
  await page.keyboard.press('Enter');
  const drawer = page.getByRole('dialog', { name: '插件详情', exact: true });
  await expect(drawer).toBeVisible();
  for (const link of await drawer.getByRole('link').all()) {
    const url = new URL((await link.getAttribute('href'))!);
    expect(url.protocol).toBe('https:');
    expect(url.username).toBe('');
    expect(url.password).toBe('');
  }
  await page.keyboard.press('Escape');
  await expect(drawer).not.toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(4);
});
