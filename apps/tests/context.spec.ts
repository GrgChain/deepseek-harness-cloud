import { expect, test, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
const id = 'b3e2b3e2-1111-4111-8111-111111111111';
const user = {
  id: 'user-context',
  memberships: [
    { org_id: 'org', role: 'MEMBER', role_id: 'member', role_name: '成员', status: 'ACTIVE' },
  ],
  permissions: ['memory:read', 'memory:manage', 'knowledge:read', 'knowledge:manage'],
  auth_generation: 1,
  csrf_token: 'x'.repeat(43),
  session_expires_at: '2099-01-01T00:00:00Z',
};
const connection = {
  id,
  kind: 'memory',
  backend: 'mem0',
  endpoint_id: 'local-mem0',
  name: '项目记忆',
  version: 1,
  enabled: true,
  status: 'READY',
  capabilities: { search: true, write: true },
  verified_at: '2026-10-23T08:00:00Z',
  updated_at: '2026-10-23T08:00:00Z',
  integration: 'NOT_CONNECTED',
};
const entry = {
  id: 'test-memory',
  title: '发布约定',
  content: '星河项目每周三发布。',
  type: 'note',
  source: '本人确认的项目约定',
  scope: 'personal',
  version: 1,
  status: 'READY',
  created_at: '2026-10-23T08:00:00Z',
  updated_at: '2026-10-23T08:00:00Z',
};
const portableEntry = {
  id: entry.id,
  title: entry.title,
  content: entry.content,
  type: entry.type,
  source: entry.source,
  scope: entry.scope,
  version: entry.version,
  created_at: entry.created_at,
  updated_at: entry.updated_at,
};
const automaticEntry = {
  uri: 'viking://user/owner/memories/preferences/language.md',
  content: '# 回答偏好\n\n默认使用中文回答。',
  updated_at: entry.updated_at,
};
function canonicalFixture(value: unknown): string {
  return Array.isArray(value)
    ? '[' + value.map(canonicalFixture).join(',') + ']'
    : value !== null && typeof value === 'object'
      ? '{' +
        Object.entries(value)
          .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
          .map(([key, item]) => JSON.stringify(key) + ':' + canonicalFixture(item))
          .join(',') +
        '}'
      : JSON.stringify(value);
}
function memoryPackage(entries = [portableEntry], automatic_entries = [automaticEntry]) {
  return {
    schema: 'hc-memory-v2',
    backend: 'openviking',
    exported_at: entry.updated_at,
    entries,
    automatic_entries,
    sha256: createHash('sha256')
      .update(canonicalFixture({ entries, automatic_entries }))
      .digest('hex'),
  };
}
async function setup(page: Page, manage = true) {
  await page.route('**/livez', (r) =>
    r.fulfill({
      json: { status: 'alive', process: 'api', capabilities: { authentication: true } },
    }),
  );
  await page.route('**/readyz', (r) => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', (r) =>
    r.fulfill({ json: { ...user, permissions: manage ? user.permissions : ['memory:read'] } }),
  );
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: {
        items: [connection],
        endpoints: [
          {
            id: 'local-mem0',
            name: '本机测试 Mem0',
            backend: 'mem0',
            custom_service: true,
            data_use: '数据仅发送到独立本机测试服务',
          },
        ],
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: [entry] } }),
  );
  await page.route(`**/v1/context/memory/${id}/get`, (r) => r.fulfill({ json: entry }));
}
test('OpenViking content management combines automatic and manual memories with their own actions', async ({
  page,
}, info) => {
  await setup(page);
  let nativeReads = 0;
  let manualReads = 0;
  const viking = { ...connection, backend: 'openviking' };
  const memory = {
    id: 'viking://user/owner/memories/preferences/language.md',
    title: 'language',
    category: 'preferences',
    updated_at: entry.updated_at,
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({ json: { items: [viking], endpoints: [], bindings: [] } }),
  );
  await page.route(`**/v1/context/memory/${id}/get`, (r) => {
    manualReads++;
    return r.fulfill({ json: entry });
  });
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) => {
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1 });
    return r.fulfill({ json: { items: [memory, { ...memory }], truncated: false } });
  });
  await page.route(`**/v1/context/memory/${id}/session-get`, (r) => {
    nativeReads++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1, record_id: memory.id });
    return r.fulfill({ json: { ...memory, content: '# 语言偏好\n\n默认使用中文回复。' } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(drawer.getByRole('heading', { name: '记忆管理', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'language', exact: true })).toHaveCount(1);
  await expect(drawer.getByRole('button', { name: 'language', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '添加记忆', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '手动记忆', exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: '返回 DSH 记忆', exact: true })).toHaveCount(0);
  const automatic = drawer
    .getByRole('row')
    .filter({ has: page.getByRole('button', { name: memory.title, exact: true }) });
  const manual = drawer
    .getByRole('row')
    .filter({ has: page.getByRole('button', { name: entry.title, exact: true }) });
  await expect(automatic.getByText('自动生成', { exact: true })).toBeVisible();
  await expect(automatic.getByRole('button', { name: /^编\s*辑$/ })).toHaveCount(0);
  await expect(automatic.getByRole('button', { name: /^删\s*除$/ })).toHaveCount(0);
  await expect(manual.getByText('手动添加', { exact: true })).toBeVisible();
  await expect(manual.getByRole('button', { name: /^编\s*辑$/ })).toBeVisible();
  await expect(manual.getByRole('button', { name: /^删\s*除$/ })).toBeVisible();
  await automatic.getByRole('button', { name: /^查\s*看$/ }).click();
  const detail = page.getByRole('dialog', { name: 'language', exact: true });
  await expect(detail.getByRole('region', { name: '原文内容', exact: true })).toContainText(
    '默认使用中文回复。',
  );
  await detail.getByText('Markdown 预览', { exact: true }).click();
  await expect(detail.getByRole('heading', { name: '语言偏好', exact: true })).toBeVisible();
  expect(nativeReads).toBe(1);
  expect(manualReads).toBe(0);
  await detail.getByRole('button', { name: /^关\s*闭$/, exact: true }).click();
  await manual.getByRole('button', { name: entry.title, exact: true }).click();
  const manualDetail = page.getByRole('dialog', { name: entry.title, exact: true });
  await manualDetail.getByText('原文', { exact: true }).click();
  await expect(manualDetail.getByRole('region', { name: '原文内容', exact: true })).toHaveText(
    entry.content,
  );
  expect(manualReads).toBe(1);
  await manualDetail.getByRole('button', { name: /^关\s*闭$/, exact: true }).click();
  await drawer.getByRole('textbox', { name: '搜索记忆名称' }).fill('不存在');
  await expect(drawer.getByText('没有符合条件的记忆')).toBeVisible();
  await drawer.getByRole('textbox', { name: '搜索记忆名称' }).clear();
  await expect(drawer.getByRole('button', { name: 'language', exact: true })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await drawer.locator('.ant-drawer-body').evaluate((element) => {
    element.scrollTop = 0;
  });
  await drawer.screenshot({
    path: info.outputPath('unified-memories.png'),
    animations: 'disabled',
  });
});

test('OpenViking automatic read failures preserve manual memories and can retry', async ({
  page,
}) => {
  await setup(page);
  let failed = true;
  const memory = {
    id: 'viking://user/owner/memories/preferences/language.md',
    title: 'language',
    category: 'preferences',
    updated_at: entry.updated_at,
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    failed
      ? r.fulfill({
          status: 503,
          json: { code: 'CONTEXT_UNAVAILABLE', message: '外部服务暂不可用' },
        })
      : r.fulfill({ json: { items: [memory], truncated: false } }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(drawer.getByText('自动记忆暂时无法加载', { exact: true })).toBeVisible();
  await expect(drawer.getByText('外部服务暂不可用', { exact: true })).toBeVisible();
  await expect(drawer.getByText('暂无记忆', { exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  failed = false;
  await drawer.getByRole('button', { name: /^重\s*试$/ }).click();
  await expect(drawer.getByText('自动记忆暂时无法加载', { exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: memory.title, exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
});

test('OpenViking manual read failures preserve automatic memories and can retry', async ({
  page,
}) => {
  await setup(page);
  let failed = true;
  const memory = {
    id: 'viking://user/owner/memories/preferences/language.md',
    title: 'language',
    category: 'preferences',
    updated_at: entry.updated_at,
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    failed
      ? r.fulfill({
          status: 503,
          json: { code: 'CONTEXT_UNAVAILABLE', message: '手动记录服务暂不可用' },
        })
      : r.fulfill({ json: { items: [entry] } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [memory], truncated: false } }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(drawer.getByText('手动记忆暂时无法加载', { exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: memory.title, exact: true })).toBeVisible();
  await expect(drawer.getByText('暂无记忆', { exact: true })).toHaveCount(0);
  failed = false;
  await drawer.getByRole('button', { name: /^重\s*试$/ }).click();
  await expect(drawer.getByText('手动记忆暂时无法加载', { exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: memory.title, exact: true })).toBeVisible();
});

test('OpenViking unified memories filter and paginate across both sources without collapsing matching names', async ({
  page,
}) => {
  await setup(page);
  const manual = Array.from({ length: 8 }, (_, i) => ({
    ...entry,
    id: 'manual-' + i,
    title: i === 0 ? '共同约定' : `手动记录 ${i}`,
    updated_at: `2026-10-${String(10 + i).padStart(2, '0')}T08:00:00Z`,
  }));
  const automatic = Array.from({ length: 8 }, (_, i) => ({
    id: `viking://user/owner/memories/preferences/${i}.md`,
    title: i === 0 ? '共同约定' : `自动记录 ${i}`,
    category: 'preferences',
    updated_at: `2026-10-${String(18 + i).padStart(2, '0')}T08:00:00Z`,
  }));
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: manual } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: automatic, truncated: false } }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  const pagination = drawer.getByRole('navigation', { name: '记忆分页' });
  await expect(pagination.getByText('16', { exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '自动记录 7', exact: true })).toBeVisible();
  await pagination.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(drawer.getByRole('button', { name: '手动记录 1', exact: true })).toBeVisible();
  await drawer.getByLabel('搜索记忆名称').fill('共同约定');
  await expect(drawer.getByRole('button', { name: '共同约定', exact: true })).toHaveCount(2);
  await expect(pagination.getByRole('button', { name: '上一页', exact: true })).toBeDisabled();
  await drawer.getByRole('radio', { name: '自动生成', exact: true }).locator('..').click();
  await expect(drawer.getByRole('radio', { name: '自动生成', exact: true })).toBeChecked();
  await expect(drawer.getByRole('button', { name: '共同约定', exact: true })).toHaveCount(1);
  await expect(drawer.getByRole('button', { name: /^编\s*辑$/ })).toHaveCount(0);
  await drawer.getByRole('radio', { name: '手动添加', exact: true }).locator('..').click();
  await expect(drawer.getByRole('radio', { name: '手动添加', exact: true })).toBeChecked();
  await expect(drawer.getByRole('button', { name: '共同约定', exact: true })).toHaveCount(1);
  await expect(drawer.getByRole('button', { name: /^编\s*辑$/ })).toBeVisible();
  await drawer.getByRole('radio', { name: '全部', exact: true }).locator('..').click();
  await expect(drawer.getByRole('radio', { name: '全部', exact: true })).toBeChecked();
  await drawer.getByLabel('搜索记忆名称').fill('偏好');
  await expect(pagination.getByText('8', { exact: true })).toHaveCount(2);
  await expect(drawer.getByRole('button', { name: '共同约定', exact: true })).toHaveCount(1);
  await expect(drawer.getByRole('button', { name: /^编\s*辑$/ })).toHaveCount(0);
});

test('OpenViking read-only users can view both sources without seeing write actions', async ({
  page,
}) => {
  await setup(page, false);
  const memory = {
    id: 'viking://user/owner/memories/preferences/language.md',
    title: 'language',
    category: 'preferences',
    updated_at: entry.updated_at,
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [memory], truncated: false } }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: memory.title, exact: true })).toBeVisible();
  for (const name of ['添加记忆', /^编\s*辑$/, /^删\s*除$/, /^重\s*建$/])
    await expect(drawer.getByRole('button', { name, exact: true })).toHaveCount(0);
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '导出整个记忆', exact: true })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '导入整个记忆', exact: true })).toHaveCount(0);
});

test('OpenViking memory search includes both sources independently of list filters and preserves results after reading', async ({
  page,
}, info) => {
  await setup(page);
  let searches = 0;
  let nativeReads = 0;
  let manualReads = 0;
  const manual = {
    ...entry,
    title: '共同偏好',
    content: '本人确认的完整手动原文。',
    snippet: '手动添加：请用中文并保留代码示例。',
    position: '片段 1',
    score: 0.81,
    memory_source: 'manual',
  };
  const automatic = {
    ...entry,
    id: automaticEntry.uri,
    title: manual.title,
    type: 'memory',
    source: 'DSH 会话',
    version: 0,
    memory_source: 'automatic',
    category: 'preferences',
    snippet: '会话自动生成：优先使用中文解释。',
    score: 0.97,
  };
  const listed = Array.from({ length: 12 }, (_, i) => ({
    ...entry,
    id: `filtered-list-${i}`,
    title: `手动列表 ${i}`,
  }));
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: listed } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({
      json: {
        items: [
          {
            id: automatic.id,
            title: '列表自动记忆',
            category: 'preferences',
            updated_at: entry.updated_at,
          },
        ],
        truncated: false,
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/search`, (r) => {
    searches++;
    expect(r.request().postDataJSON()).toEqual({
      expected_version: 1,
      query: '中文回答',
      limit: 5,
    });
    return r.fulfill({ json: { items: [automatic, manual], status: 'HITS' } });
  });
  await page.route(`**/v1/context/memory/${id}/session-get`, (r) => {
    nativeReads++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1, record_id: automatic.id });
    return r.fulfill({
      json: { ...automatic, content: '# 自动偏好\n\n' + automaticEntry.content },
    });
  });
  await page.route(`**/v1/context/memory/${id}/get`, (r) => {
    manualReads++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1, record_id: manual.id });
    return r.fulfill({ json: manual });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('radio', { name: '手动添加', exact: true }).locator('..').click();
  await drawer.getByLabel('搜索记忆名称').fill('手动列表');
  const pagination = drawer.getByRole('navigation', { name: '记忆分页' });
  await expect(pagination.getByText('12', { exact: true })).toBeVisible();
  await pagination.getByRole('button', { name: '下一页', exact: true }).click();
  await drawer.getByRole('button', { name: '记忆检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
  await expect(
    search.getByText('检索自动生成和手动添加的全部记忆。', { exact: true }),
  ).toBeVisible();
  await search.getByLabel('检索问题').fill('  中文回答  ');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  const results = search.getByRole('region', { name: '检索结果', exact: true });
  await expect(results.getByRole('heading', { name: '检索结果 · 2', exact: true })).toBeVisible();
  await expect(results.getByRole('button', { name: manual.title, exact: true })).toHaveCount(2);
  const nativeHit = results
    .locator('article')
    .filter({ has: page.getByText('自动生成', { exact: true }) });
  const manualHit = results
    .locator('article')
    .filter({ has: page.getByText('手动添加', { exact: true }) });
  await expect(nativeHit).toContainText(automatic.snippet);
  await expect(nativeHit).toContainText('偏好');
  await expect(nativeHit).toContainText('相关度 0.970');
  await expect(nativeHit).not.toContainText('v0');
  await expect(manualHit).toContainText(manual.snippet);
  await expect(manualHit).toContainText('相关度 0.810');
  await expect(manualHit).toContainText('v1');
  expect(searches).toBe(1);
  await search.locator('.ant-modal-body').evaluate((element) => {
    element.scrollTop = 0;
  });
  expect(await search.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await search.screenshot({
    path: info.outputPath('unified-memory-search.png'),
    animations: 'disabled',
  });
  await nativeHit.getByRole('button', { name: manual.title, exact: true }).click();
  const detail = page.getByRole('dialog', { name: manual.title, exact: true });
  await expect(detail.getByRole('region', { name: '原文内容', exact: true })).toContainText(
    automaticEntry.content,
  );
  expect(nativeReads).toBe(1);
  expect(manualReads).toBe(0);
  await detail.getByRole('button', { name: /^关\s*闭$/, exact: true }).click();
  await expect(search).toBeVisible();
  await expect(search.getByLabel('检索问题')).toHaveValue('  中文回答  ');
  await expect(results.getByRole('button', { name: manual.title, exact: true })).toHaveCount(2);
  await manualHit.getByRole('button', { name: manual.title, exact: true }).click();
  await expect(detail.getByRole('region', { name: '原文内容', exact: true })).toHaveText(
    manual.content,
  );
  expect(manualReads).toBe(1);
  await detail.getByRole('button', { name: /^关\s*闭$/, exact: true }).click();
  await expect(results.getByRole('button', { name: manual.title, exact: true })).toHaveCount(2);
  expect(searches).toBe(1);
  await search.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(drawer.getByRole('radio', { name: '手动添加', exact: true })).toBeChecked();
  await expect(drawer.getByLabel('搜索记忆名称')).toHaveValue('手动列表');
  await expect(pagination.getByRole('button', { name: '上一页', exact: true })).toBeEnabled();
});

test('OpenViking memory search submits source, limit and similarity conditions and can reset them', async ({
  page,
}, info) => {
  await setup(page, false);
  const requests: Record<string, unknown>[] = [];
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/search`, (r) => {
    const body = r.request().postDataJSON();
    requests.push(body);
    return r.fulfill({
      json: {
        items: [
          {
            ...entry,
            title: '命中的记忆',
            memory_source: body.memory_source ?? 'automatic',
            score: 0.85,
            snippet: entry.content,
          },
        ],
      },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  const searchButton = drawer.getByRole('button', { name: '记忆检索', exact: true });
  await expect
    .poll(() =>
      searchButton.evaluate(
        (button) =>
          button.nextElementSibling?.getAttribute('aria-label') === '刷新内容' &&
          button.getBoundingClientRect().right <=
            button.nextElementSibling.getBoundingClientRect().left,
      ),
    )
    .toBe(true);
  await searchButton.click();
  const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
  const source = search.getByRole('combobox', { name: '记忆来源', exact: true });
  const limit = search.getByRole('combobox', { name: '返回条数', exact: true });
  const similarity = search.getByRole('spinbutton', { name: '最低相关度', exact: true });
  const reset = search.getByRole('button', { name: '重置条件', exact: true });
  await expect(reset).toBeDisabled();
  await source.press('ArrowDown');
  await page.getByRole('option', { name: '自动生成', exact: true }).click();
  await limit.press('ArrowDown');
  await page.getByRole('option', { name: '10 条', exact: true }).click();
  await similarity.fill('0.75');
  await search.getByLabel('检索问题', { exact: true }).fill('  回答偏好  ');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByRole('heading', { name: '检索结果 · 1', exact: true })).toBeVisible();
  expect(requests[0]).toEqual({
    expected_version: 1,
    query: '回答偏好',
    limit: 10,
    memory_source: 'automatic',
    min_similarity: 0.75,
  });
  await source.press('ArrowDown');
  await page.getByRole('option', { name: '手动添加', exact: true }).click();
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toHaveCount(0);
  await expect(search.getByLabel('检索问题', { exact: true })).toHaveValue('  回答偏好  ');
  await limit.press('ArrowDown');
  await page.getByRole('option', { name: '20 条', exact: true }).click();
  await similarity.fill('0');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByRole('heading', { name: '检索结果 · 1', exact: true })).toBeVisible();
  expect(requests[1]).toEqual({
    expected_version: 1,
    query: '回答偏好',
    limit: 20,
    memory_source: 'manual',
    min_similarity: 0,
  });
  await search.getByRole('button', { name: '最低相关度说明', exact: true }).click();
  await expect(
    page.getByText('仅返回相关度达到此值的记忆；数值越高，匹配越严格。', { exact: true }),
  ).toBeVisible();
  await search.getByRole('button', { name: '最低相关度说明', exact: true }).click();
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 375, height: 667 });
  expect(await search.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await search.screenshot({
    path: info.outputPath('memory-search-conditions.png'),
    animations: 'disabled',
  });
  if (info.project.name === 'mobile') {
    await page.setViewportSize({ width: 844, height: 390 });
    await search.getByRole('button', { name: /^检\s*索$/, exact: true }).scrollIntoViewIfNeeded();
    await expect(search.getByRole('button', { name: /^检\s*索$/, exact: true })).toBeInViewport();
    await search.screenshot({
      path: info.outputPath('memory-search-landscape.png'),
      animations: 'disabled',
    });
  }
  await reset.click();
  await expect(reset).toBeDisabled();
  await expect(similarity).toHaveValue('');
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toHaveCount(0);
  await expect(search.getByLabel('检索问题', { exact: true })).toHaveValue('  回答偏好  ');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByRole('heading', { name: '检索结果 · 1', exact: true })).toBeVisible();
  expect(requests[2]).toEqual({ expected_version: 1, query: '回答偏好', limit: 5 });
});

test('OpenViking memory search clears prior hits on external failures and recovers without partial results', async ({
  page,
}) => {
  await setup(page);
  let response = 'hits';
  let searches = 0;
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/search`, (r) => {
    searches++;
    expect(r.request().postDataJSON()).toEqual({
      expected_version: 1,
      query: '检索中文',
      limit: 5,
    });
    return response === 'failed'
      ? r.fulfill({
          status: 503,
          json: {
            code: 'CONTEXT_UNAVAILABLE',
            message: '自动记忆检索暂不可用',
            items: [
              {
                ...entry,
                title: '不能展示的部分结果',
                snippet: '部分结果',
                memory_source: 'manual',
              },
            ],
          },
        })
      : r.fulfill({
          json: {
            items:
              response === 'hits'
                ? [
                    { ...entry, snippet: entry.content, memory_source: 'manual' },
                    {
                      ...entry,
                      id: automaticEntry.uri,
                      title: '自动检索结果',
                      version: 0,
                      snippet: '会话生成的已匹配内容',
                      source: 'DSH 会话',
                      memory_source: 'automatic',
                    },
                  ]
                : [],
          },
        });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('button', { name: '记忆检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
  await search.getByLabel('检索问题').fill('检索中文');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByRole('heading', { name: '检索结果 · 2', exact: true })).toBeVisible();
  response = 'failed';
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByText('自动记忆检索暂不可用', { exact: true })).toBeVisible();
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toHaveCount(0);
  await expect(search.getByText('不能展示的部分结果', { exact: true })).toHaveCount(0);
  await expect(search.getByRole('button', { name: '自动检索结果', exact: true })).toHaveCount(0);
  await expect(search.getByLabel('检索问题')).toHaveValue('检索中文');
  response = 'empty';
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByText('没有命中授权范围内的内容', { exact: true })).toBeVisible();
  await expect(search.getByText('自动记忆检索暂不可用', { exact: true })).toHaveCount(0);
  expect(searches).toBe(3);
});

test('OpenViking read-only users can search both memory sources without write actions', async ({
  page,
}) => {
  await setup(page, false);
  let writes = 0;
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/search`, (r) => {
    expect(r.request().postDataJSON()).toEqual({
      expected_version: 1,
      query: '只读检索',
      limit: 5,
    });
    return r.fulfill({
      json: {
        items: [
          { ...entry, memory_source: 'manual', snippet: entry.content },
          {
            ...entry,
            id: automaticEntry.uri,
            title: '只读会话偏好',
            memory_source: 'automatic',
            source: 'DSH 会话',
            version: 0,
            snippet: '自动生成的只读记忆。',
          },
        ],
      },
    });
  });
  for (const action of ['preview', 'apply', 'delete', 'rebuild'])
    await page.route(`**/v1/context/memory/${id}/${action}`, (r) => {
      writes++;
      return r.fulfill({ status: 403, json: { code: 'FORBIDDEN' } });
    });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(drawer.getByRole('button', { name: '添加记忆', exact: true })).toHaveCount(0);
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '导入整个记忆', exact: true })).toHaveCount(0);
  await drawer.getByRole('heading', { name: '记忆管理', exact: true }).click();
  await drawer.getByRole('button', { name: '记忆检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
  await search.getByLabel('检索问题').fill('只读检索');
  await search.getByRole('button', { name: /^检\s*索$/, exact: true }).click();
  await expect(search.getByText('自动生成', { exact: true })).toBeVisible();
  await expect(search.getByText('手动添加', { exact: true })).toBeVisible();
  await expect(search.getByRole('button', { name: '只读会话偏好', exact: true })).toBeVisible();
  await expect(search.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  for (const name of ['添加记忆', /^编\s*辑$/, /^删\s*除$/, /^重\s*建$/])
    await expect(search.getByRole('button', { name, exact: true })).toHaveCount(0);
  expect(writes).toBe(0);
});

for (const [backend, name] of [
  ['mem0', 'Mem0'],
  ['graphiti', 'Graphiti'],
] as const)
  test(`${name} memory search submits limit and similarity, resets conditions and reads records without native sources`, async ({
    page,
  }, info) => {
    await setup(page, false);
    let reads = 0;
    let nativeReads = 0;
    const requests: Record<string, unknown>[] = [];
    await page.route('**/v1/context/memory', (r) =>
      r.fulfill({ json: { items: [{ ...connection, backend }], endpoints: [], bindings: [] } }),
    );
    await page.route(`**/v1/context/memory/${id}/search`, (r) => {
      requests.push(r.request().postDataJSON());
      return r.fulfill({
        json: { items: [{ ...entry, snippet: entry.content, position: '片段 1', score: 0.9 }] },
      });
    });
    await page.route(`**/v1/context/memory/${id}/get`, (r) => {
      reads++;
      expect(r.request().postDataJSON()).toEqual({ expected_version: 1, record_id: entry.id });
      return r.fulfill({ json: entry });
    });
    for (const action of ['session-list', 'session-get'])
      await page.route(`**/v1/context/memory/${id}/${action}`, (r) => {
        nativeReads++;
        return r.fulfill({ status: 400, json: { code: 'CONTEXT_UNSUPPORTED' } });
      });
    await page.goto('/app/memory');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
    await expect(drawer.getByRole('radiogroup', { name: '记忆来源筛选', exact: true })).toHaveCount(
      0,
    );
    await drawer.getByRole('button', { name: '记忆检索', exact: true }).click();
    const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
    const query = search.getByLabel('检索问题');
    const limit = search.getByRole('combobox', { name: '返回条数', exact: true });
    const similarity = search.getByRole('spinbutton', { name: '最低相关度', exact: true });
    const reset = search.getByRole('button', { name: '重置条件', exact: true });
    const submit = search.getByRole('button', { name: /^检\s*索$/, exact: true });
    await expect(search.getByRole('combobox', { name: '记忆来源', exact: true })).toHaveCount(0);
    await expect(reset).toBeDisabled();
    await expect(similarity).toHaveValue('');
    await query.fill('  发布约定  ');
    await submit.click();
    await expect(search.getByText('相关度 0.900', { exact: true })).toBeVisible();
    expect(requests[0]).toEqual({ expected_version: 1, query: '发布约定', limit: 5 });
    await limit.press('ArrowDown');
    await page.getByRole('option', { name: '20 条', exact: true }).click();
    await similarity.fill('0.75');
    await submit.click();
    await expect.poll(() => requests.length).toBe(2);
    expect(requests[1]).toEqual({
      expected_version: 1,
      query: '发布约定',
      limit: 20,
      min_similarity: 0.75,
    });
    await similarity.fill('0');
    await submit.click();
    await expect.poll(() => requests.length).toBe(3);
    expect(requests[2]).toEqual({
      expected_version: 1,
      query: '发布约定',
      limit: 20,
      min_similarity: 0,
    });
    for (const label of ['自动生成', '手动添加'])
      await expect(search.getByText(label, { exact: true })).toHaveCount(0);
    await search.getByRole('button', { name: '最低相关度说明', exact: true }).click();
    await expect(
      page.getByText('仅返回相关度达到此值的记忆；数值越高，匹配越严格。', { exact: true }),
    ).toBeVisible();
    await search.getByRole('button', { name: '最低相关度说明', exact: true }).click();
    expect(await search.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(
      true,
    );
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await search.screenshot({
      path: info.outputPath(`${backend}-search-conditions.png`),
      animations: 'disabled',
    });
    await reset.click();
    await expect(reset).toBeDisabled();
    await expect(similarity).toHaveValue('');
    await expect(query).toHaveValue('  发布约定  ');
    await expect(search.getByRole('region', { name: '检索结果', exact: true })).toHaveCount(0);
    await submit.click();
    await expect(search.getByText(entry.content, { exact: true })).toBeVisible();
    expect(requests[3]).toEqual({ expected_version: 1, query: '发布约定', limit: 5 });
    await search.getByRole('button', { name: entry.title, exact: true }).click();
    const detail = page.getByRole('dialog', { name: entry.title, exact: true });
    await expect(detail.getByRole('region', { name: '原文内容', exact: true })).toHaveText(
      entry.content,
    );
    await detail.getByRole('button', { name: /^关\s*闭$/, exact: true }).click();
    await expect(search.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
    expect(reads).toBe(1);
    expect(nativeReads).toBe(0);
  });

test('OpenViking entire memory export includes both sources outside the active filter and page', async ({
  page,
}) => {
  await setup(page);
  const manual = Array.from({ length: 12 }, (_, i) => ({
    ...portableEntry,
    id: `export-manual-${i}`,
    title: `手动导出 ${i}`,
    content: `手动原文 ${i}，保留换行。\n第二行。`,
  }));
  const automatic = Array.from({ length: 12 }, (_, i) => ({
    ...automaticEntry,
    uri: `viking://user/owner/memories/preferences/export-${i}.md`,
    content: `# 自动原文 ${i}\n\n保留完整会话提取内容。`,
  }));
  const exported = memoryPackage(manual, automatic);
  let exports = 0;
  let writes = 0;
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: manual.map((item) => ({ ...item, status: 'READY' })) } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({
      json: {
        items: automatic.map((item, i) => ({
          id: item.uri,
          title: `自动导出 ${i}`,
          category: 'preferences',
          updated_at: item.updated_at,
        })),
        truncated: false,
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/export`, (r) => {
    exports++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1 });
    return r.fulfill({ json: exported });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    writes++;
    return r.fulfill({ status: 500, json: { code: 'CONTEXT_UNAVAILABLE' } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('radio', { name: '手动添加', exact: true }).locator('..').click();
  await expect(drawer.getByRole('radio', { name: '手动添加', exact: true })).toBeChecked();
  await drawer
    .getByRole('navigation', { name: '记忆分页' })
    .getByRole('button', { name: '下一页', exact: true })
    .click();
  await expect(drawer.getByRole('button', { name: '手动导出 0', exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: '自动导出 0', exact: true })).toHaveCount(0);
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  const pendingDownload = page.waitForEvent('download');
  await page.getByRole('menuitem', { name: '导出整个记忆', exact: true }).click();
  const download = await pendingDownload;
  expect(await download.failure()).toBeNull();
  const saved = JSON.parse(await readFile((await download.path())!, 'utf8'));
  expect(saved).toEqual(exported);
  expect(saved.entries).toHaveLength(12);
  expect(saved.automatic_entries).toHaveLength(12);
  expect(saved.entries.find((item: { id: string }) => item.id === manual[0].id)?.content).toBe(
    manual[0].content,
  );
  expect(
    saved.automatic_entries.find((item: { uri: string }) => item.uri === automatic[0].uri)?.content,
  ).toBe(automatic[0].content);
  expect(exports).toBe(1);
  expect(writes).toBe(0);
  await expect(drawer.getByRole('radio', { name: '手动添加', exact: true })).toBeChecked();
  await expect(
    drawer
      .getByRole('navigation', { name: '记忆分页' })
      .getByRole('button', { name: '上一页', exact: true }),
  ).toBeEnabled();
});

test('OpenViking entire memory export reports service failures without downloading an empty package', async ({
  page,
}) => {
  await setup(page);
  let response = 'unavailable';
  let exports = 0;
  const downloads: string[] = [];
  page.on('download', (download) => downloads.push(download.suggestedFilename()));
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/export`, (r) => {
    exports++;
    return response === 'unavailable'
      ? r.fulfill({
          status: 503,
          json: { code: 'CONTEXT_UNAVAILABLE', message: '自动记忆读取失败，整包未导出' },
        })
      : r.fulfill({
          json:
            response === 'incomplete'
              ? {
                  schema: 'hc-memory-v2',
                  backend: 'openviking',
                  entries: [],
                  sha256: '0'.repeat(64),
                }
              : memoryPackage(),
        });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '导出整个记忆', exact: true }).click();
  await expect(drawer.getByText('自动记忆读取失败，整包未导出', { exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '记忆更多操作', exact: true })).toBeEnabled();
  expect(downloads).toHaveLength(0);
  response = 'incomplete';
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '导出整个记忆', exact: true }).click();
  await expect(
    drawer.getByText('记忆包不完整，未生成导出文件，请重试', { exact: true }),
  ).toBeVisible();
  expect(downloads).toHaveLength(0);
  response = 'complete';
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  const pendingDownload = page.waitForEvent('download');
  await page.getByRole('menuitem', { name: '导出整个记忆', exact: true }).click();
  const download = await pendingDownload;
  expect(JSON.parse(await readFile((await download.path())!, 'utf8'))).toEqual(memoryPackage());
  expect(exports).toBe(3);
  expect(downloads).toHaveLength(1);
  await expect(
    drawer.getByText('记忆包不完整，未生成导出文件，请重试', { exact: true }),
  ).toHaveCount(0);
});

test('OpenViking entire memory archives preserve exact numeric metadata when downloaded and reimported', async ({
  page,
}) => {
  await setup(page);
  let previews = 0;
  let writes = 0;
  // Python archives distinguish 1.0 from 1 and retain integers beyond JS precision.
  const graph = '{"opaque_id":9007199254740993,"weight":1.0}';
  const entries = [{ ...portableEntry, graph: '__raw_numeric_graph__' }];
  const automatic_entries = [automaticEntry];
  const payload = canonicalFixture({ entries, automatic_entries }).replace(
    '"__raw_numeric_graph__"',
    graph,
  );
  const archive = canonicalFixture({
    schema: 'hc-memory-v2',
    backend: 'openviking',
    exported_at: entry.updated_at,
    entries,
    automatic_entries,
    sha256: createHash('sha256').update(payload).digest('hex'),
  }).replace('"__raw_numeric_graph__"', graph);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/export`, (r) =>
    r.fulfill({ contentType: 'application/json', body: archive }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    previews++;
    const raw = r.request().postData()!;
    expect(raw).toContain(archive);
    expect(raw).toContain('"opaque_id":9007199254740993');
    expect(raw).toContain('"weight":1.0');
    expect(r.request().postDataJSON()).toMatchObject({
      expected_version: 1,
      package: { schema: 'hc-memory-v2', backend: 'openviking' },
    });
    return r.fulfill({
      json: {
        token: 'numeric-metadata-preview',
        items: [{ ...entry, memory_source: 'manual', conflict: false }],
        warnings: [],
        data_use: '原始数字与校验和验证通过。',
        expires_in: 300,
        source_counts: { manual: 1, automatic: 1 },
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    writes++;
    expect(r.request().postDataJSON()).toMatchObject({
      token: 'numeric-metadata-preview',
      mode: 'skip',
      confirmed: true,
    });
    return r.fulfill({ json: { applied: [entry.id, automaticEntry.uri], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  const pendingDownload = page.waitForEvent('download');
  await page.getByRole('menuitem', { name: '导出整个记忆', exact: true }).click();
  const download = await pendingDownload;
  const downloaded = await readFile((await download.path())!);
  expect(downloaded.toString('utf8')).toBe(archive);
  await drawer.locator('#context-import').setInputFiles({
    name: 'numeric-metadata.json',
    mimeType: 'application/json',
    buffer: downloaded,
  });
  const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
  await expect(preview.getByText('原始数字与校验和验证通过。', { exact: true })).toBeVisible();
  expect(previews).toBe(1);
  expect(writes).toBe(0);
  await preview.getByRole('button', { name: '确认导入', exact: true }).click();
  await expect(preview).not.toBeVisible();
  expect(writes).toBe(1);
});

test('OpenViking entire memory import previews both sources before replacing conflicts', async ({
  page,
}, info) => {
  await setup(page);
  const imported = memoryPackage();
  let previews = 0;
  let writes = 0;
  let saved = false;
  const automatic = {
    ...entry,
    id: automaticEntry.uri,
    title: 'language',
    content: automaticEntry.content,
    type: 'memory',
    source: 'DSH 会话',
    memory_source: 'automatic',
    conflict: true,
  };
  const manual = { ...entry, memory_source: 'manual', conflict: false };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: saved ? [entry] : [] } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({
      json: {
        items: saved
          ? [
              {
                id: automaticEntry.uri,
                title: 'language',
                category: 'preferences',
                updated_at: automaticEntry.updated_at,
              },
            ]
          : [],
        truncated: false,
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    previews++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1, package: imported });
    return r.fulfill({
      json: {
        token: 'entire-import-preview',
        items: [manual, automatic],
        warnings: [],
        data_use: '将自动记忆与手动记忆写入此连接。',
        expires_in: 300,
        source_counts: { manual: 1, automatic: 1 },
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, async (r) => {
    const body = r.request().postDataJSON();
    expect(body).toMatchObject({
      expected_version: 1,
      token: 'entire-import-preview',
      mode: 'replace',
      confirmed: true,
      accept_loss: false,
    });
    expect(body).not.toHaveProperty('scope');
    expect(body.idempotency_key).toEqual(expect.any(String));
    expect(await r.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    writes++;
    saved = true;
    return r.fulfill({ json: { applied: [entry.id, automaticEntry.uri], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  const fileChooser = page.waitForEvent('filechooser');
  await page.getByRole('menuitem', { name: '导入整个记忆', exact: true }).click();
  await (
    await fileChooser
  ).setFiles({
    name: 'entire-memory.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(imported)),
  });
  const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
  await expect(
    preview.getByText('自动生成 1 条 · 手动添加 1 条 · 确认后导入全部内容', { exact: true }),
  ).toBeVisible();
  await expect(preview.getByText('自动生成', { exact: true })).toBeVisible();
  await expect(preview.getByText('手动添加', { exact: true })).toBeVisible();
  await expect(preview.getByText(entry.content, { exact: true })).toBeVisible();
  await expect(preview.locator('.context-preview').filter({ hasText: 'language' })).toContainText(
    automaticEntry.content,
  );
  await expect(preview.getByText('已存在', { exact: true })).toBeVisible();
  expect(previews).toBe(1);
  expect(writes).toBe(0);
  await preview.getByRole('radio', { name: '替换现有内容', exact: true }).check();
  await preview.locator('.ant-modal-body').evaluate((element) => {
    element.scrollTop = 0;
  });
  expect(await preview.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(
    true,
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await preview.screenshot({
    path: info.outputPath('entire-memory-import-preview.png'),
    animations: 'disabled',
  });
  await preview.getByRole('button', { name: '确认导入', exact: true }).click();
  await expect(preview).not.toBeVisible();
  await expect(drawer.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'language', exact: true })).toBeVisible();
  expect(writes).toBe(1);
});

for (const [backend, name] of [
  ['openviking', 'OpenViking'],
  ['mem0', 'Mem0'],
  ['graphiti', 'Graphiti'],
] as const)
  test(`${name} import restores deleted manual memories as new while skipping live conflicts`, async ({
    page,
  }) => {
    await setup(page);
    const deletedArchive = {
      ...portableEntry,
      id: 'deleted-product',
      title: '产品',
      content: '归档里的产品资料。',
    };
    const existingArchive = {
      ...portableEntry,
      id: 'current-memory',
      title: '现有约定',
      content: '包中的旧版本约定。',
    };
    const current = {
      ...entry,
      ...existingArchive,
      content: '当前约定应当保留，不被旧包覆盖。',
      version: 3,
    };
    const restored = { ...entry, ...deletedArchive, version: 3 };
    const entries = [deletedArchive, existingArchive];
    const imported =
      backend === 'openviking'
        ? memoryPackage(entries, [])
        : {
            schema: 'hc-memory-v1',
            backend,
            exported_at: entry.updated_at,
            entries,
            sha256: createHash('sha256').update(canonicalFixture(entries)).digest('hex'),
          };
    let saved = false;
    let previews = 0;
    let writes = 0;
    await page.route('**/v1/context/memory', (r) =>
      r.fulfill({ json: { items: [{ ...connection, backend }], endpoints: [], bindings: [] } }),
    );
    await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
      r.fulfill({ json: { items: [], truncated: false } }),
    );
    await page.route(`**/v1/context/memory/${id}/list`, (r) =>
      r.fulfill({ json: { items: saved ? [current, restored] : [current] } }),
    );
    await page.route(`**/v1/context/memory/${id}/get`, (r) =>
      r.fulfill({ json: r.request().postDataJSON().record_id === current.id ? current : restored }),
    );
    await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
      previews++;
      expect(r.request().postDataJSON()).toEqual({ expected_version: 1, package: imported });
      return r.fulfill({
        json: {
          token: 'restore-deleted-preview',
          items: [
            { ...restored, memory_source: 'manual', conflict: false },
            {
              ...current,
              content: existingArchive.content,
              memory_source: 'manual',
              conflict: true,
            },
          ],
          warnings: [],
          data_use: '预览手动记忆恢复及现有冲突。',
          expires_in: 300,
          ...(backend === 'openviking' ? { source_counts: { manual: 2, automatic: 0 } } : {}),
        },
      });
    });
    await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
      writes++;
      expect(r.request().postDataJSON()).toEqual({
        expected_version: 1,
        token: 'restore-deleted-preview',
        mode: 'skip',
        idempotency_key: expect.any(String),
        confirmed: true,
        accept_loss: false,
      });
      saved = true;
      return r.fulfill({ json: { applied: [restored.id], skipped: [current.id] } });
    });
    await page.goto('/app/memory');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
    await expect(drawer.getByRole('button', { name: current.title, exact: true })).toBeVisible();
    await expect(drawer.getByRole('button', { name: restored.title, exact: true })).toHaveCount(0);
    await drawer.locator('#context-import').setInputFiles({
      name: 'before-deletion.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(imported)),
    });
    const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
    const deletedItem = preview.locator('.context-preview').filter({ hasText: restored.title });
    await expect(deletedItem.getByText('新增', { exact: true })).toBeVisible();
    await expect(deletedItem.getByText('已存在', { exact: true })).toHaveCount(0);
    await expect(
      preview
        .locator('.context-preview')
        .filter({ hasText: current.title })
        .getByText('已存在', { exact: true }),
    ).toBeVisible();
    await expect(
      preview.getByRole('radio', { name: '保留现有，跳过冲突', exact: true }),
    ).toBeChecked();
    expect(writes).toBe(0);
    await preview.getByRole('button', { name: '确认导入', exact: true }).click();
    await expect(preview).not.toBeVisible();
    await expect(drawer.getByRole('button', { name: restored.title, exact: true })).toBeVisible();
    await expect(drawer.getByText('导入完成：保存 1 条，保留 1 条', { exact: true })).toBeVisible();
    expect(previews).toBe(1);
    expect(writes).toBe(1);
    await drawer.getByRole('button', { name: current.title, exact: true }).click();
    const detail = page.getByRole('dialog', { name: current.title, exact: true });
    await expect(detail.getByText(current.content, { exact: true })).toBeVisible();
    await expect(detail.getByText(existingArchive.content, { exact: true })).toHaveCount(0);
  });

test('OpenViking entire memory import accepts legacy packages and rejects damaged packages before any save', async ({
  page,
}) => {
  await setup(page);
  let previews = 0;
  let writes = 0;
  const legacy = {
    schema: 'hc-memory-v1',
    backend: 'openviking',
    exported_at: entry.updated_at,
    entries: [portableEntry],
    sha256: createHash('sha256')
      .update(canonicalFixture([portableEntry]))
      .digest('hex'),
  };
  const damaged = { ...memoryPackage(), sha256: '0'.repeat(64) };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    previews++;
    const body = r.request().postDataJSON();
    if (previews === 1) {
      expect(body.package).toEqual(damaged);
      return r.fulfill({
        status: 422,
        json: { code: 'CONTEXT_INVALID', message: '记忆包校验失败，请重新导出' },
      });
    }
    expect(body.package).toEqual(legacy);
    return r.fulfill({
      json: {
        token: 'legacy-import-preview',
        items: [{ ...entry, memory_source: 'manual', conflict: false }],
        warnings: [],
        data_use: '导入兼容的手动记忆。',
        expires_in: 300,
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    writes++;
    expect(r.request().postDataJSON()).toMatchObject({
      token: 'legacy-import-preview',
      mode: 'skip',
      confirmed: true,
    });
    return r.fulfill({ json: { applied: [entry.id], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.locator('#context-import').setInputFiles({
    name: 'damaged.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(damaged)),
  });
  await expect(drawer.getByText('记忆包校验失败，请重新导出', { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: '确认导入记忆', exact: true })).toHaveCount(0);
  expect(writes).toBe(0);
  await drawer.locator('#context-import').setInputFiles({
    name: 'legacy.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(legacy)),
  });
  const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
  await expect(preview.getByText(entry.content, { exact: true })).toBeVisible();
  await expect(preview.getByText('手动添加', { exact: true })).toBeVisible();
  expect(writes).toBe(0);
  await preview.getByRole('button', { name: '确认导入', exact: true }).click();
  await expect(preview).not.toBeVisible();
  expect(previews).toBe(2);
  expect(writes).toBe(1);
});

test('OpenViking entire memory import accepts packages above 2 MiB and blocks files above 16 MiB', async ({
  page,
}) => {
  await setup(page);
  let previews = 0;
  let writes = 0;
  const automatic = Array.from({ length: 12 }, (_, i) => ({
    ...automaticEntry,
    uri: `viking://user/owner/memories/preferences/large-${i}.md`,
    content: `# 记忆 ${i}\n` + '保留原始内容。'.repeat(10000),
  }));
  const imported = memoryPackage([portableEntry], automatic);
  const file = Buffer.from(JSON.stringify(imported));
  expect(file.byteLength).toBeGreaterThan(2 * 1024 * 1024);
  expect(file.byteLength).toBeLessThan(16 * 1024 * 1024);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    previews++;
    expect(r.request().postDataJSON()).toEqual({ expected_version: 1, package: imported });
    return r.fulfill({
      json: {
        token: 'large-import-preview',
        items: [
          { ...entry, memory_source: 'manual', conflict: false },
          ...automatic.map((item, i) => ({
            ...entry,
            id: item.uri,
            title: `大包记忆 ${i}`,
            content: item.content,
            memory_source: 'automatic',
            conflict: false,
          })),
        ],
        warnings: [],
        data_use: '完整记忆包已解析。',
        expires_in: 300,
        source_counts: { manual: 1, automatic: 12 },
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    writes++;
    return r.fulfill({ json: { applied: [], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.locator('#context-import').setInputFiles({
    name: 'too-large.json',
    mimeType: 'application/json',
    buffer: Buffer.alloc(16 * 1024 * 1024 + 1, 32),
  });
  await expect(drawer.getByText('文件不能超过 16 MiB', { exact: true })).toBeVisible();
  expect(previews).toBe(0);
  expect(writes).toBe(0);
  await drawer.locator('#context-import').setInputFiles({
    name: 'large-entire-memory.json',
    mimeType: 'application/json',
    buffer: file,
  });
  const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
  await expect(preview.getByText('完整记忆包已解析。', { exact: true })).toBeVisible();
  const pagination = preview.getByRole('navigation', { name: '导入预览分页' });
  await expect(pagination.getByText('13', { exact: true })).toBeVisible();
  await expect(preview.getByText(entry.title, { exact: true })).toBeVisible();
  await pagination.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(preview.getByText('大包记忆 11', { exact: true })).toBeVisible();
  await expect(preview.getByText(entry.title, { exact: true })).toHaveCount(0);
  expect(previews).toBe(1);
  expect(writes).toBe(0);
  await preview.getByRole('button', { name: /^取\s*消$/ }).click();
  await expect(preview).not.toBeVisible();
  expect(writes).toBe(0);
});

test('OpenViking entire memory import prevents submitting an empty preview', async ({ page }) => {
  await setup(page);
  let writes = 0;
  const empty = memoryPackage([], []);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({ json: { items: [], truncated: false } }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) =>
    r.fulfill({
      json: {
        token: 'empty-import-preview',
        items: [],
        warnings: [],
        data_use: '记忆包中没有内容。',
        expires_in: 300,
        source_counts: { manual: 0, automatic: 0 },
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    writes++;
    return r.fulfill({ json: { applied: [], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.locator('#context-import').setInputFiles({
    name: 'empty-memory.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(empty)),
  });
  const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
  await expect(preview.getByText('没有可导入的记忆', { exact: true })).toBeVisible();
  await expect(preview.getByRole('button', { name: '确认导入', exact: true })).toBeDisabled();
  expect(writes).toBe(0);
  await preview.getByRole('button', { name: /^取\s*消$/ }).click();
  await expect(preview).not.toBeVisible();
  expect(writes).toBe(0);
});

for (const [backend, name] of [
  ['mem0', 'Mem0'],
  ['graphiti', 'Graphiti'],
] as const)
  test(`${name} v1 memory archives round-trip above 2 MiB and reject incomplete exports and imports above 16 MiB`, async ({
    page,
  }) => {
    await setup(page);
    let ready = false;
    let exports = 0;
    let previews = 0;
    let writes = 0;
    const downloads: string[] = [];
    page.on('download', (download) => downloads.push(download.suggestedFilename()));
    const entries = Array.from({ length: 12 }, (_, i) => ({
      ...portableEntry,
      id: `large-manual-${i}`,
      title: `完整记忆 ${i}`,
      content: `# 记忆 ${i}\n` + '完整原文。'.repeat(15000),
    }));
    const archive = {
      schema: 'hc-memory-v1',
      backend,
      exported_at: entry.updated_at,
      entries,
      sha256: createHash('sha256').update(canonicalFixture(entries)).digest('hex'),
    };
    const file = Buffer.from(JSON.stringify(archive));
    expect(file.byteLength).toBeGreaterThan(2 * 1024 * 1024);
    expect(file.byteLength).toBeLessThan(16 * 1024 * 1024);
    await page.route('**/v1/context/memory', (r) =>
      r.fulfill({ json: { items: [{ ...connection, backend }], endpoints: [], bindings: [] } }),
    );
    await page.route(`**/v1/context/memory/${id}/list`, (r) =>
      r.fulfill({
        json: { items: entries.map(({ content, ...row }) => ({ ...row, status: 'READY' })) },
      }),
    );
    await page.route(`**/v1/context/memory/${id}/export`, (r) => {
      exports++;
      expect(r.request().postDataJSON()).toEqual({ expected_version: 1 });
      return ready
        ? r.fulfill({ contentType: 'application/json', body: file })
        : r.fulfill({
            status: 409,
            json: {
              code: 'VERSION_CONFLICT',
              message: '部分记忆尚未就绪，暂时无法完整导出，请稍后重试',
            },
          });
    });
    await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
      previews++;
      expect(r.request().postDataJSON()).toEqual({ expected_version: 1, package: archive });
      return r.fulfill({
        json: {
          token: 'large-v1-preview',
          items: entries.map((row) => ({ ...row, status: 'READY', conflict: false })),
          warnings: [],
          data_use: '完整 v1 记忆包已解析。',
          expires_in: 300,
        },
      });
    });
    await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
      writes++;
      expect(r.request().postDataJSON()).toEqual({
        expected_version: 1,
        token: 'large-v1-preview',
        mode: 'skip',
        idempotency_key: expect.any(String),
        confirmed: true,
        accept_loss: false,
      });
      return r.fulfill({ json: { applied: entries.map((row) => row.id), skipped: [] } });
    });
    await page.goto('/app/memory');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
    await drawer.getByLabel('搜索记忆名称').fill('完整记忆 0');
    await expect(drawer.getByRole('button', { name: entries[0].title, exact: true })).toBeVisible();
    await expect(drawer.getByRole('button', { name: entries[11].title, exact: true })).toHaveCount(
      0,
    );
    await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
    await page.getByRole('menuitem', { name: /^导出(整个记忆)?$/, exact: true }).click();
    await expect(
      drawer.getByText('部分记忆尚未就绪，暂时无法完整导出，请稍后重试', { exact: true }),
    ).toBeVisible();
    expect(downloads).toHaveLength(0);
    ready = true;
    await drawer.getByRole('button', { name: '记忆更多操作', exact: true }).click();
    const downloading = page.waitForEvent('download');
    await page.getByRole('menuitem', { name: /^导出(整个记忆)?$/, exact: true }).click();
    const download = await downloading;
    expect(await download.failure()).toBeNull();
    const downloaded = await readFile((await download.path())!);
    expect(downloaded.equals(file)).toBe(true);
    const exported = JSON.parse(downloaded.toString('utf8'));
    expect(exported.schema).toBe('hc-memory-v1');
    expect(exported.backend).toBe(backend);
    expect(exported.entries).toHaveLength(12);
    expect(exported).not.toHaveProperty('automatic_entries');
    expect(exports).toBe(2);
    expect(downloads).toHaveLength(1);
    await drawer.locator('#context-import').setInputFiles({
      name: 'too-large-v1.json',
      mimeType: 'application/json',
      buffer: Buffer.alloc(16 * 1024 * 1024 + 1, 32),
    });
    await expect(drawer.getByText('文件不能超过 16 MiB', { exact: true })).toBeVisible();
    expect(previews).toBe(0);
    expect(writes).toBe(0);
    await expect(page.getByRole('dialog', { name: '确认导入记忆', exact: true })).toHaveCount(0);
    await drawer
      .locator('#context-import')
      .setInputFiles({ name: 'large-v1.json', mimeType: 'application/json', buffer: downloaded });
    const preview = page.getByRole('dialog', { name: '确认导入记忆', exact: true });
    await expect(preview.getByText('完整 v1 记忆包已解析。', { exact: true })).toBeVisible();
    await expect(
      preview.getByText('共 12 条记忆 · 确认后导入全部内容', { exact: true }),
    ).toBeVisible();
    await expect(preview.getByText('自动生成', { exact: true })).toHaveCount(0);
    await expect(preview.getByText('手动添加', { exact: true })).toHaveCount(0);
    await expect(preview.getByText(entries[0].title, { exact: true })).toBeVisible();
    await preview
      .getByRole('navigation', { name: '导入预览分页', exact: true })
      .getByRole('button', { name: '下一页', exact: true })
      .click();
    await expect(preview.getByText(entries[11].title, { exact: true })).toBeVisible();
    expect(previews).toBe(1);
    expect(writes).toBe(0);
    await preview.getByRole('button', { name: '确认导入', exact: true }).click();
    await expect(preview).not.toBeVisible();
    await expect(
      drawer.getByText('导入完成：保存 12 条，保留 0 条', { exact: true }),
    ).toBeVisible();
    expect(writes).toBe(1);
  });

test('custom memory connections submit their service address and API key', async ({ page }) => {
  await setup(page);
  let saved = false;
  await page.route('**/v1/context/memory', (r) => {
    if (r.request().method() === 'POST') {
      expect(r.request().postDataJSON()).toMatchObject({
        endpoint_id: 'viking',
        service_url: 'https://viking.example',
        credential: 'admin-key',
      });
      saved = true;
      return r.fulfill({ json: {} });
    }
    return r.fulfill({
      json: {
        items: [],
        endpoints: [
          {
            id: 'viking',
            name: '接入节点',
            backend: 'openviking',
            custom_service: true,
            data_use: '测试',
          },
        ],
        bindings: [],
      },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 OpenViking' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await editor.getByLabel('OpenViking 服务地址').fill('https://viking.example');
  await editor.getByLabel('服务凭据').fill('admin-key');
  await editor.getByRole('button', { name: '保存' }).click();
  await expect.poll(() => saved).toBe(true);
});
test('OpenViking configuration is visible without a custom endpoint', async ({ page }) => {
  await setup(page);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: {
        items: [],
        endpoints: [
          { id: 'legacy', name: '旧接入节点', backend: 'openviking', data_use: '旧服务' },
        ],
        bindings: [],
      },
    }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '查看配置 OpenViking' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await expect(editor.getByLabel('OpenViking 服务地址')).toBeVisible();
  await expect(editor.getByLabel('服务凭据')).toBeVisible();
  await expect(editor.getByText('暂未接入')).toHaveCount(0);
  await expect(editor.getByRole('button', { name: '保存' })).toBeDisabled();
});
test('Graphiti connection accepts a Neo4j address and credentials', async ({ page }) => {
  await setup(page);
  let saved = false;
  await page.route('**/v1/context/memory', (r) => {
    if (r.request().method() === 'POST') {
      expect(r.request().postDataJSON()).toMatchObject({
        endpoint_id: 'graphiti',
        service_url: 'bolt+s://neo4j.example:7687',
        credential: 'neo4j:secret',
      });
      saved = true;
      return r.fulfill({ json: {} });
    }
    return r.fulfill({
      json: {
        items: [],
        endpoints: [
          {
            id: 'graphiti',
            name: '接入节点',
            backend: 'graphiti',
            custom_service: true,
            data_use: '测试',
          },
        ],
        bindings: [],
      },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 Graphiti' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await editor.getByLabel('Neo4j Bolt 地址').fill('bolt+s://neo4j.example:7687');
  await editor.getByLabel('服务凭据').fill('neo4j:secret');
  await editor.getByRole('button', { name: '保存' }).click();
  await expect.poll(() => saved).toBe(true);
});
test('Mem0 connection accepts an external service address and API key', async ({ page }) => {
  await setup(page);
  let saved = false;
  await page.route('**/v1/context/memory', (r) => {
    if (r.request().method() === 'POST') {
      expect(r.request().postDataJSON()).toMatchObject({
        endpoint_id: 'local-mem0',
        service_url: 'https://mem0.example',
        credential: 'mem0-key',
      });
      saved = true;
      return r.fulfill({ json: {} });
    }
    return r.fulfill({
      json: {
        items: [],
        endpoints: [
          {
            id: 'local-mem0',
            name: '接入节点',
            backend: 'mem0',
            custom_service: true,
            data_use: '本机测试',
          },
        ],
        bindings: [],
      },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 Mem0' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await expect(editor.getByText('本机测试', { exact: true })).toHaveCount(0);
  await editor.getByLabel('Mem0 服务地址').fill('https://mem0.example');
  await editor.getByLabel('服务凭据').fill('mem0-key');
  await editor.getByRole('button', { name: '保存' }).click();
  await expect.poll(() => saved).toBe(true);
});
test('Mem0 shows its address form without a custom endpoint and preserves legacy settings', async ({
  page,
}) => {
  await setup(page);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: {
        items: [connection],
        endpoints: [{ id: 'local-mem0', name: '旧 Mem0', backend: 'mem0', data_use: '旧连接' }],
        bindings: [],
      },
    }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '查看配置 Mem0' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await expect(editor.getByLabel('Mem0 服务地址')).toBeVisible();
  await expect(editor.getByRole('button', { name: '保存' })).toBeDisabled();
  await expect(
    editor.getByText('请联系管理员配置支持自定义地址的 Mem0 接入节点后保存。'),
  ).toBeVisible();
  await editor.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: '项目记忆 更多操作' }).click();
  await page.getByRole('menuitem', { name: '设置' }).click();
  const settings = page.getByRole('dialog', { name: '连接设置' });
  await expect(settings.getByLabel('服务', { exact: true })).toBeVisible();
  await expect(settings.getByLabel('Mem0 服务地址')).toHaveCount(0);
  await expect(settings.getByRole('button', { name: '保存' })).toBeEnabled();
});
test('Graphiti configuration remains visible when no custom endpoint is available', async ({
  page,
}) => {
  await setup(page);
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: {
        items: [],
        endpoints: [
          { id: 'old-graphiti', name: '旧接入节点', backend: 'graphiti', data_use: '旧服务' },
        ],
        bindings: [],
      },
    }),
  );
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  const picker = page.getByRole('dialog', { name: '添加记忆连接' });
  await expect(
    picker.getByRole('article', { name: '记忆服务 Graphiti' }).getByText('暂未接入'),
  ).toBeVisible();
  await picker.getByRole('button', { name: '查看配置 Graphiti' }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接' });
  await expect(editor.getByLabel('Neo4j Bolt 地址')).toBeVisible();
  await expect(editor.getByLabel('服务凭据')).toBeVisible();
  await expect(
    editor.getByText('请联系管理员配置支持自定义地址的 Graphiti 接入节点后保存。'),
  ).toBeVisible();
  await expect(editor.getByRole('button', { name: '保存' })).toBeDisabled();
});
test('memory preview needs confirmation and search citations recheck authorization', async ({
  page,
}, info) => {
  await setup(page);
  let writes = 0;
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      content: '我偏好中文回答',
      title: '回答偏好',
    });
    expect(r.request().postDataJSON()).not.toHaveProperty('scope');
    return r.fulfill({
      json: {
        token: 'preview-token',
        items: [{ ...entry, title: '回答偏好', content: '我偏好中文回答', conflict: false }],
        warnings: [],
        data_use: '数据仅发送到独立本机测试服务',
        expires_in: 300,
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, async (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      token: 'preview-token',
      mode: 'skip',
      confirmed: true,
      expected_version: 1,
    });
    expect(await r.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
    writes++;
    return r.fulfill({ json: { applied: ['test-memory'], skipped: [] } });
  });
  await page.route(`**/v1/context/memory/${id}/search`, (r) =>
    r.fulfill({
      json: {
        items: [{ ...entry, snippet: entry.content, position: '片段 1', score: 0.9 }],
        status: 'HITS',
      },
    }),
  );
  await page.goto('/app/memory');
  await expect(page.getByRole('region', { name: '记忆管理' })).toBeVisible();
  await expect(page.getByText('未接入工作空间', { exact: true })).toHaveCount(0);
  const memoryLogo = page
    .getByRole('article', { name: '记忆连接 项目记忆', exact: true })
    .locator('img');
  await expect(memoryLogo).toHaveAttribute('src', '/context-logos/mem0.svg');
  await expect
    .poll(() =>
      memoryLogo.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0),
    )
    .toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page
    .getByRole('dialog', { name: connection.name, exact: true })
    .getByRole('button', { name: '添加记忆', exact: true })
    .click();
  const editor = page.getByRole('dialog', { name: '添加记忆', exact: true });
  await editor.getByRole('textbox').nth(0).fill('回答偏好');
  await editor.getByRole('textbox').nth(1).fill('我偏好中文回答');
  await editor.getByRole('button', { name: /^预\s*览$/ }).click();
  const preview = page.getByRole('dialog', { name: '确认发送到外部服务' });
  await expect(preview).toBeVisible();
  expect(writes).toBe(0);
  await preview.getByRole('button', { name: '确认保存' }).click();
  await expect(preview).toHaveCount(0);
  expect(writes).toBe(1);
  await page.getByRole('button', { name: '记忆检索' }).click();
  await page.getByRole('textbox', { name: '检索问题' }).fill('什么时候发布');
  await page
    .getByRole('dialog', { name: '记忆检索' })
    .getByRole('button', { name: '检索', exact: true })
    .click();
  await expect(page.getByText('检索结果 · 1')).toBeVisible();
  await page.screenshot({
    path: info.outputPath('external-memory.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await page.route(`**/v1/context/memory/${id}/get`, (r) =>
    r.fulfill({ status: 404, json: { code: 'NOT_FOUND', message: '连接、内容或授权范围不存在' } }),
  );
  await page.locator('.context-results').getByRole('button', { name: '发布约定' }).click();
  await expect(page.getByText('连接、内容或授权范围不存在', { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: '发布约定' })).toHaveCount(0);
});
test('manual memory keeps pending content open and confirms the original save before showing success', async ({
  page,
}, info) => {
  await setup(page);
  const viking = { ...connection, backend: 'openviking' };
  const added = {
    ...entry,
    id: 'zzzz-new-memory',
    title: '新增回答偏好',
    content: '请默认使用中文回复。',
  };
  const previous = Array.from({ length: 12 }, (_, i) => ({
    ...entry,
    id: `old-${i}`,
    title: `旧记忆 ${i}`,
    updated_at: '2025-01-01T00:00:00Z',
  }));
  let saved = false;
  let receiptReady = false;
  let reconciliations = 0;
  let attempts = 0;
  let previews = 0;
  const keys = new Set<string>();
  const pending = {
    code: 'CONTEXT_PENDING',
    message: '外部服务正在处理，请稍后点击“核实待确认操作”',
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({ json: { items: [viking], endpoints: [], bindings: [] } }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    r.fulfill({
      json: {
        items: previous.map((row) => ({
          id: `viking://user/owner/memories/preferences/${row.id}.md`,
          title: row.title,
          category: 'preferences',
          updated_at: row.updated_at,
        })),
        truncated: false,
      },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) =>
    r.fulfill({ json: { items: saved ? [...previous, added] : previous } }),
  );
  await page.route(`**/v1/context/memory/${id}/preview`, (r) => {
    previews++;
    return r.fulfill({
      json: {
        token: 'pending-save-preview',
        items: [added],
        warnings: [],
        data_use: '测试记忆服务',
        expires_in: 300,
      },
    });
  });
  await page.route(`**/v1/context/memory/${id}/apply`, (r) => {
    attempts++;
    const input = r.request().postDataJSON();
    keys.add(input.idempotency_key);
    expect(input).toMatchObject({ token: 'pending-save-preview', confirmed: true });
    if (!receiptReady) return r.fulfill({ status: 503, json: pending });
    saved = true;
    return r.fulfill({ json: { applied: [added.id], skipped: [] } });
  });
  await page.route(`**/v1/context/memory/${id}/reconcile`, (r) => {
    reconciliations++;
    if (reconciliations === 1) return r.fulfill({ json: { deleted: true } });
    if (reconciliations === 2) return r.fulfill({ status: 503, json: pending });
    receiptReady = true;
    return r.fulfill({ json: { applied: [added.id], skipped: [] } });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
  await drawer.getByRole('radio', { name: '自动生成', exact: true }).locator('..').click();
  await expect(drawer.getByRole('radio', { name: '自动生成', exact: true })).toBeChecked();
  await drawer.getByLabel('搜索记忆名称').fill('旧记忆');
  const pagination = drawer.getByRole('navigation', { name: '记忆分页' });
  await expect(pagination.getByText('12', { exact: true })).toBeVisible();
  await pagination.getByRole('button', { name: '下一页', exact: true }).click();
  await drawer.getByRole('button', { name: '添加记忆', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆', exact: true });
  await editor.getByLabel('标题', { exact: true }).fill(added.title);
  await editor.getByLabel('内容', { exact: true }).fill(added.content);
  await editor.getByRole('button', { name: /^预\s*览$/ }).click();
  const preview = page.getByRole('dialog', { name: '确认发送到外部服务', exact: true });
  await preview.getByRole('button', { name: '确认保存', exact: true }).click();
  await expect(preview.getByText('正在确认保存结果', { exact: true })).toBeVisible();
  await expect(preview.getByText(added.content, { exact: true })).toBeVisible();
  await expect(preview.getByRole('button', { name: /^取\s*消$/ })).toBeDisabled();
  await expect.poll(() => attempts, { timeout: 12_000 }).toBe(2);
  await expect(preview).toBeVisible();
  expect(saved).toBe(false);
  await page.screenshot({
    path: info.outputPath('memory-save-pending.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await expect(preview).toHaveCount(0, { timeout: 20_000 });
  await expect(drawer.getByRole('button', { name: added.title, exact: true })).toBeVisible();
  await expect(drawer.getByLabel('搜索记忆名称')).toHaveValue('');
  await expect(drawer.getByRole('radio', { name: '全部', exact: true })).toBeChecked();
  await expect(pagination.getByRole('button', { name: '上一页', exact: true })).toBeDisabled();
  await expect(drawer.getByRole('heading', { name: '记忆管理', exact: true })).toBeVisible();
  expect(previews).toBe(1);
  expect(attempts).toBe(3);
  expect(reconciliations).toBe(3);
  expect(keys.size).toBe(1);
  await expect(drawer.getByText('记忆已保存', { exact: true })).toBeVisible();
  await expect(drawer.getByText('记忆已保存', { exact: true })).toHaveCount(0, { timeout: 5000 });
});

for (const [backend, name] of [
  ['openviking', 'OpenViking'],
  ['mem0', 'Mem0'],
  ['graphiti', 'Graphiti'],
] as const)
  test(`${name} memory deletion completes pending work automatically with the original receipt`, async ({
    page,
  }, info) => {
    await setup(page);
    const target = { ...entry, id: 'product-memory', title: '产品', version: 2 };
    const survivor = { ...entry, id: 'other-memory', title: '保留的记忆' };
    let deleted = false;
    let receiptReady = false;
    let attempts = 0;
    let reconciliations = 0;
    let lists = 0;
    let finish = () => {};
    const pendingReceipt = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const bodies: Record<string, unknown>[] = [];
    const keys = new Set<string>();
    const pending = {
      code: 'CONTEXT_PENDING',
      message: '外部服务正在处理，请稍后点击“核实待确认操作”',
    };
    await page.route('**/v1/context/memory', (r) =>
      r.fulfill({ json: { items: [{ ...connection, backend }], endpoints: [], bindings: [] } }),
    );
    await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
      r.fulfill({ json: { items: [], truncated: false } }),
    );
    await page.route(`**/v1/context/memory/${id}/list`, (r) => {
      lists++;
      return r.fulfill({ json: { items: deleted ? [survivor] : [target, survivor] } });
    });
    await page.route(`**/v1/context/memory/${id}/delete`, (r) => {
      attempts++;
      const body = r.request().postDataJSON();
      bodies.push(body);
      keys.add(body.idempotency_key);
      expect(body).toMatchObject({
        expected_version: connection.version,
        record_id: target.id,
        record_version: target.version,
        confirmed: true,
      });
      expect(body).not.toHaveProperty('scope');
      expect(body.idempotency_key).toEqual(expect.any(String));
      if (!receiptReady) return r.fulfill({ status: 503, json: pending });
      deleted = true;
      return r.fulfill({ json: { deleted: true, rebuilt: false } });
    });
    await page.route(`**/v1/context/memory/${id}/reconcile`, async (r) => {
      reconciliations++;
      if (reconciliations === 1) return r.abort('failed');
      if (reconciliations === 2)
        return r.fulfill({ json: { applied: ['unrelated-save'], skipped: [] } });
      await pendingReceipt;
      receiptReady = true;
      return r.fulfill({ json: { deleted: true, rebuilt: false } });
    });
    try {
      await page.goto('/app/memory');
      await page.getByRole('button', { name: '管理内容', exact: true }).click();
      const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
      await drawer
        .getByRole('row')
        .filter({ has: page.getByRole('button', { name: target.title, exact: true }) })
        .getByRole('button', { name: /^删\s*除$/ })
        .click();
      const modal = page.getByRole('dialog', { name: '永久删除此记忆？', exact: true });
      const checkbox = modal.getByRole('checkbox', { name: '我确认永久删除' });
      const submit = modal.getByRole('button', { name: /(永久删除|正在删除|删除中)$/ });
      await expect(
        modal.getByText(`删除「${target.title}」的外部内容及索引。操作不可恢复。`, { exact: true }),
      ).toBeVisible();
      await expect(submit).toBeDisabled();
      await checkbox.check();
      await submit.click();
      await expect(modal.getByRole('status')).toContainText(/正在删除|删除中/);
      await expect(submit).toBeDisabled();
      await expect(submit.locator('.ant-btn-loading-icon')).toBeVisible();
      await expect(checkbox).toBeDisabled();
      await expect(modal.getByRole('button', { name: /^取\s*消$/ })).toBeDisabled();
      await expect.poll(() => attempts, { timeout: 12_000 }).toBe(2);
      await expect(modal).toBeVisible();
      expect(deleted).toBe(false);
      await expect(drawer.getByRole('button', { name: target.title, exact: true })).toBeVisible();
      await expect.poll(() => reconciliations, { timeout: 12_000 }).toBe(3);
      await expect(page.getByRole('button', { name: '核实待确认操作', exact: true })).toHaveCount(
        0,
      );
      await expect(page.getByText(pending.message, { exact: true })).toHaveCount(0);
      await page.keyboard.press('Escape');
      await expect(modal).toBeVisible();
      await page.screenshot({
        path: info.outputPath('memory-delete-pending.png'),
        fullPage: true,
        animations: 'disabled',
      });
      finish();
      await expect(modal).toHaveCount(0);
      await expect(drawer.getByRole('button', { name: target.title, exact: true })).toHaveCount(0);
      await expect(drawer.getByRole('button', { name: survivor.title, exact: true })).toBeVisible();
      await expect(drawer.getByText('记忆已删除', { exact: true })).toBeVisible();
      expect(attempts).toBe(3);
      expect(reconciliations).toBe(3);
      expect(keys.size).toBe(1);
      expect(bodies[1]).toEqual(bodies[0]);
      expect(bodies[2]).toEqual(bodies[0]);
      expect(lists).toBeGreaterThanOrEqual(2);
    } finally {
      finish();
    }
  });

test('OpenViking pending deletion resumes automatically when reopening content management', async ({
  page,
}) => {
  await setup(page);
  const survivor = { ...entry, id: 'remaining-memory', title: '保留的记忆' };
  let recovered = false;
  let lists = 0;
  let reconciliations = 0;
  let deletes = 0;
  let finish = () => {};
  const receipt = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const pending = {
    code: 'CONTEXT_PENDING',
    message: '外部服务正在处理，请稍后点击“核实待确认操作”',
  };
  await page.route('**/v1/context/memory', (r) =>
    r.fulfill({
      json: { items: [{ ...connection, backend: 'openviking' }], endpoints: [], bindings: [] },
    }),
  );
  await page.route(`**/v1/context/memory/${id}/session-list`, (r) =>
    recovered
      ? r.fulfill({ json: { items: [], truncated: false } })
      : r.fulfill({ status: 503, json: pending }),
  );
  await page.route(`**/v1/context/memory/${id}/list`, (r) => {
    lists++;
    return recovered
      ? r.fulfill({ json: { items: [survivor] } })
      : r.fulfill({ status: 503, json: pending });
  });
  await page.route(`**/v1/context/memory/${id}/reconcile`, async (r) => {
    reconciliations++;
    await receipt;
    recovered = true;
    return r.fulfill({ json: { deleted: true } });
  });
  await page.route(`**/v1/context/memory/${id}/delete`, (r) => {
    deletes++;
    return r.fulfill({ status: 500, json: { code: 'UNEXPECTED_DELETE' } });
  });
  try {
    await page.goto('/app/memory');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: connection.name, exact: true });
    await expect.poll(() => reconciliations, { timeout: 12_000 }).toBe(1);
    await expect(drawer.getByRole('status').filter({ hasText: '正在完成操作' })).toBeVisible();
    await expect(page.getByRole('button', { name: '核实待确认操作', exact: true })).toHaveCount(0);
    await expect(page.getByText(pending.message, { exact: true })).toHaveCount(0);
    await expect(page.getByText(/记忆已保存|文档已保存|保存成功/)).toHaveCount(0);
    expect(deletes).toBe(0);
    finish();
    await expect(drawer.getByRole('button', { name: survivor.title, exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '核实待确认操作', exact: true })).toHaveCount(0);
    await expect(page.getByText(/记忆已保存|文档已保存|保存成功/)).toHaveCount(0);
    expect(lists).toBeGreaterThanOrEqual(2);
    expect(reconciliations).toBe(1);
    expect(deletes).toBe(0);
  } finally {
    finish();
  }
});

test('memory list filters titles and keeps search separate', async ({ page }, info) => {
  await setup(page);
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容' }).click();
  const detail = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(detail.getByRole('heading', { name: '记忆管理', exact: true })).toBeVisible();
  await expect(detail.getByRole('button', { name: '发布约定' })).toBeVisible();
  await page.screenshot({
    path: info.outputPath('manual-memory-layout.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await detail.getByLabel('搜索记忆名称').fill('不存在');
  await expect(detail.getByText('没有符合条件的记忆')).toBeVisible();
  await detail.getByLabel('搜索记忆名称').fill('发布');
  await expect(detail.getByRole('button', { name: '发布约定' })).toBeVisible();
  await detail.getByRole('button', { name: '记忆检索' }).click();
  await expect(page.getByRole('dialog', { name: '记忆检索' }).getByLabel('检索问题')).toBeVisible();
});
test('read-only connections hide content management and connection editing', async ({ page }) => {
  await setup(page, false);
  await page.goto('/app/memory');
  await expect(page.getByRole('button', { name: '添加记忆', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: '管理内容' }).click();
  await expect(page.getByRole('button', { name: '添加记忆' })).toHaveCount(0);
  await page.getByRole('button', { name: '记忆更多操作', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: '导出整个记忆', exact: true })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '导入迁移包', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '删除', exact: true })).toHaveCount(0);
});

test('memory service picker filters services and preserves configuration after save failure', async ({
  page,
}, info) => {
  await setup(page);
  let saves = 0;
  await page.route('**/v1/context/memory', (r) => {
    if (r.request().method() === 'POST') {
      saves++;
      expect(r.request().postDataJSON()).toMatchObject({
        name: '我的项目记忆',
        endpoint_id: 'local-mem0',
        service_url: 'https://mem0.example',
        credential: 'test-only-key',
        enabled: true,
        expected_version: 0,
      });
      return r.fulfill({
        status: 422,
        json: { code: 'CONTEXT_INVALID', message: '请检查服务凭据' },
      });
    }
    return r.fulfill({
      json: {
        items: [connection],
        endpoints: [
          {
            id: 'local-mem0',
            name: '本机测试 Mem0',
            backend: 'mem0',
            custom_service: true,
            data_use: '数据仅发送到独立本机测试服务',
          },
          { id: 'local-qdrant', name: '知识库测试服务', backend: 'qdrant', data_use: '知识库专用' },
        ],
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  const picker = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
  await expect(picker.getByRole('article')).toHaveCount(3);
  await expect(picker.locator('.context-service-grid')).toHaveCSS('display', 'grid');
  for (const [name, file] of [
    ['Mem0', 'mem0.svg'],
    ['OpenViking', 'openviking.svg'],
    ['Graphiti', 'graphiti.png'],
  ]) {
    const logo = picker
      .getByRole('article', { name: '记忆服务 ' + name, exact: true })
      .locator('img');
    await expect(logo).toHaveAttribute('src', '/context-logos/' + file);
    await expect
      .poll(() => logo.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0))
      .toBe(true);
  }
  await expect(picker.getByText('Qdrant', { exact: true })).toHaveCount(0);
  await picker.getByRole('textbox', { name: '搜索记忆服务' }).fill('无匹配服务');
  await expect(picker.getByText('没有符合条件的服务')).toBeVisible();
  await picker.getByRole('button', { name: '清除搜索' }).click();
  await page.screenshot({
    path: info.outputPath('memory-service-picker.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await picker.getByRole('button', { name: '查看配置 Graphiti' }).click();
  let editor = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
  await expect(editor.getByText('暂未接入', { exact: true })).toBeVisible();
  await expect(editor.getByRole('button', { name: /^保\s*存$/ })).toBeDisabled();
  expect(saves).toBe(0);
  await editor.getByRole('button', { name: '返回选择' }).click();
  await page.getByRole('button', { name: '配置连接 Mem0', exact: true }).click();
  editor = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
  await expect(editor.getByLabel('连接名称', { exact: true })).toHaveValue('Mem0 记忆');
  await expect(editor.getByLabel('接入节点', { exact: true })).toBeHidden();
  await editor.getByLabel('Mem0 服务地址').fill('https://mem0.example');
  await expect(page.getByText('知识库测试服务', { exact: true })).toHaveCount(0);
  await editor.getByLabel('连接名称', { exact: true }).fill('我的项目记忆');
  await editor.getByLabel('服务凭据', { exact: true }).fill('test-only-key');
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(editor.getByText('请检查服务凭据', { exact: true })).toBeVisible();
  expect(saves).toBe(1);
  await expect(editor.getByLabel('连接名称', { exact: true })).toHaveValue('我的项目记忆');
  await expect(editor.getByLabel('服务凭据', { exact: true })).toHaveValue('test-only-key');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('memory-connection-editor.png'),
    fullPage: true,
    animations: 'disabled',
  });
});

test('memory validation brings the first incomplete field into view and focuses it', async ({
  page,
}, info) => {
  await setup(page);
  await page.setViewportSize(
    info.project.name === 'mobile' ? { width: 375, height: 667 } : { width: 844, height: 390 },
  );
  await page.emulateMedia({ reducedMotion: 'reduce' });
  let writes = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/v1/context/memory') writes++;
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '添加记忆', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 Mem0', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
  const name = editor.getByLabel('连接名称', { exact: true });
  const credential = editor.getByLabel('服务凭据', { exact: true });
  await name.clear();
  await editor.locator('.ant-modal-body').evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(name).toBeFocused();
  await expect(name).toBeInViewport();
  expect(writes).toBe(0);
  await name.fill('我的记忆');
  await editor.getByLabel('Mem0 服务地址').fill('https://mem0.example');
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(credential).toBeFocused();
  await expect(credential).toBeInViewport();
  expect(writes).toBe(0);
  expect(
    await credential.evaluate((el) => {
      const box = el.getBoundingClientRect();
      const body = el.closest('.ant-modal-body')!.getBoundingClientRect();
      return box.top >= body.top && box.bottom <= body.bottom;
    }),
  ).toBe(true);
  expect(
    await editor.getByRole('button', { name: /^保\s*存$/ }).evaluate((el) => {
      const box = el.getBoundingClientRect();
      return box.top >= 0 && box.bottom <= innerHeight;
    }),
  ).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('context-validation-focus.png'),
    fullPage: true,
    animations: 'disabled',
  });
});

test('knowledge document list searches and pages through all matching records', async ({
  page,
}) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'milvus', name: '项目文档' };
  const documents = Array.from({ length: 1025 }, (_, i) => ({
    ...entry,
    id: `doc-${i}`,
    title: `文档 ${String(i).padStart(4, '0')}`,
  }));
  const requests: { page: number; page_size: number; query: string }[] = [];
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => {
    const input = r.request().postDataJSON();
    requests.push(input);
    const filtered = documents.filter((item) => item.title.includes(input.query));
    return r.fulfill({
      json: {
        items: filtered.slice((input.page - 1) * input.page_size, input.page * input.page_size),
        total: filtered.length,
      },
    });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: '项目文档' });
  const pagination = drawer.getByRole('navigation', { name: '文档分页' });
  await expect(pagination.getByText('1025', { exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '文档 0000' })).toBeVisible();
  await pagination.getByRole('button', { name: '下一页' }).click();
  await expect(drawer.getByRole('button', { name: '文档 0010' })).toBeVisible();
  await drawer.getByRole('textbox', { name: '搜索文档名称' }).fill('1024');
  await expect(drawer.getByRole('button', { name: '文档 1024' })).toBeVisible();
  await expect(pagination.getByText('1', { exact: true }).first()).toBeVisible();
  expect(requests.at(-1)).toMatchObject({ page: 1, page_size: 10, query: '1024' });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('knowledge creation uses only its selected backend and blocks edits while saving', async ({
  page,
}, info) => {
  await setup(page);
  let saved: Record<string, unknown> | undefined;
  let finish = () => {};
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const qdrant = {
    ...connection,
    kind: 'knowledge',
    backend: 'qdrant',
    endpoint_id: 'local-qdrant',
    name: '个人资料',
    enabled: false,
    status: 'UNVERIFIED',
  };
  await page.route('**/v1/context/knowledge', async (r) => {
    if (r.request().method() === 'POST') {
      saved = r.request().postDataJSON();
      await pending;
      return r.fulfill({ json: qdrant });
    }
    return r.fulfill({
      json: {
        items: saved ? [qdrant] : [],
        endpoints: [
          {
            id: 'local-qdrant',
            name: 'Qdrant 测试服务',
            backend: 'qdrant',
            custom_service: true,
            data_use: '所选服务说明',
          },
          { id: 'local-mem0', name: 'Mem0 测试服务', backend: 'mem0', data_use: '记忆专用' },
        ],
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    });
  });
  try {
    await page.goto('/app/knowledge');
    await page.getByRole('button', { name: '添加知识库', exact: true }).click();
    const picker = page.getByRole('dialog', { name: '添加知识库连接', exact: true });
    await expect(picker.getByRole('article')).toHaveCount(3);
    await expect(picker.getByText('Mem0', { exact: true })).toHaveCount(0);
    await expect(
      picker
        .getByRole('article', { name: '知识库服务 Qdrant' })
        .getByText('可添加连接', { exact: true }),
    ).toBeVisible();
    await expect(
      picker
        .getByRole('article', { name: '知识库服务 Milvus' })
        .getByText('暂未接入', { exact: true }),
    ).toBeVisible();
    for (const [name, file] of [
      ['Milvus', 'milvus.svg'],
      ['Weaviate', 'weaviate.png'],
      ['Qdrant', 'qdrant.png'],
    ]) {
      const logo = picker
        .getByRole('article', { name: '知识库服务 ' + name, exact: true })
        .locator('img');
      await expect(logo).toHaveAttribute('src', '/context-logos/' + file);
      await expect
        .poll(() => logo.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0))
        .toBe(true);
    }
    await page.screenshot({
      path: info.outputPath('knowledge-service-picker.png'),
      fullPage: true,
      animations: 'disabled',
    });
    await picker.getByRole('button', { name: '配置连接 Qdrant', exact: true }).click();
    const editor = page.getByRole('dialog', { name: '添加知识库连接', exact: true });
    await expect(
      editor.getByText('同一知识库的内容由接入它的工作空间共用。', { exact: true }),
    ).toBeVisible();
    await expect(editor.getByLabel('内容范围')).toHaveCount(0);
    await expect(editor.getByText('个人语料', { exact: true })).toHaveCount(0);
    await expect(editor.getByLabel('连接名称', { exact: true })).toHaveValue('Qdrant 知识库');
    await expect(editor.locator('.context-service-logo')).toHaveAttribute(
      'src',
      '/context-logos/qdrant.png',
    );
    await editor.getByLabel('Qdrant 服务地址', { exact: true }).fill('https://qdrant.example');
    await editor.getByLabel('连接名称', { exact: true }).fill('个人资料');
    await editor.getByLabel('服务凭据', { exact: true }).fill('knowledge-test-key');
    await editor.getByRole('switch', { name: '启用连接', exact: true }).uncheck();
    await editor
      .getByLabel('Embedding服务地址', { exact: true })
      .fill('http://192.168.1.10:11434/v1');
    await editor.getByLabel('Embedding模型名称', { exact: true }).fill('my-local-model');
    await editor.getByLabel('向量维度', { exact: true }).fill('768');
    await editor.getByRole('button', { name: /^保\s*存$/ }).click();
    await expect.poll(() => saved).toBeDefined();
    await expect(editor.getByLabel('连接名称', { exact: true })).toBeDisabled();
    await expect(editor.getByRole('switch', { name: '启用连接', exact: true })).toBeDisabled();
    expect(saved).toMatchObject({
      name: '个人资料',
      endpoint_id: 'local-qdrant',
      credential: 'knowledge-test-key',
      enabled: false,
      expected_version: 0,
    });
    finish();
    await expect(editor).toHaveCount(0);
    const row = page.getByRole('article', { name: '知识库连接 个人资料', exact: true });
    await expect(row.locator('img')).toHaveAttribute('src', '/context-logos/qdrant.png');
    await expect
      .poll(() =>
        row
          .locator('img')
          .evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0),
      )
      .toBe(true);
    await expect(row.getByText('已停用', { exact: true })).toBeVisible();
    await expect(row.getByRole('button', { name: /^验\s*证$/ })).toBeDisabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: info.outputPath('knowledge-created.png'),
      fullPage: true,
      animations: 'disabled',
    });
  } finally {
    finish();
  }
});

for (const backend of ['weaviate', 'qdrant'])
  test(`${backend} accepts a custom service and preserves its address on edit`, async ({
    page,
  }) => {
    await setup(page);
    let saved: Record<string, unknown> | undefined;
    let created = false;
    const name = backend === 'weaviate' ? 'Weaviate' : 'Qdrant';
    const url = `https://${backend}.example`;
    const native = {
      ...connection,
      kind: 'knowledge',
      backend,
      endpoint_id: 'local-' + backend,
      name: '自有 ' + name,
      service_url: url,
      status: 'UNVERIFIED',
      embedding: {
        provider: 'openai_compatible',
        api_base: 'https://embed.example/v1',
        model: 'model',
        dimensions: 768,
      },
    };
    await page.route('**/v1/context/knowledge', (r) => {
      if (r.request().method() === 'POST') {
        saved = r.request().postDataJSON();
        created = true;
        return r.fulfill({ json: native });
      }
      return r.fulfill({
        json: {
          items: created ? [native] : [],
          endpoints: [
            {
              id: 'local-' + backend,
              name: '接入节点',
              backend,
              custom_service: true,
              data_use: '测试',
            },
          ],
          bindings: [],
          integration: 'NOT_CONNECTED',
        },
      });
    });
    await page.route('**/v1/context/knowledge/' + id, (r) => {
      expect(r.request().postDataJSON()).toMatchObject({ service_url: url, credential: '' });
      return r.fulfill({ json: { ...native, version: 2 } });
    });
    await page.goto('/app/knowledge');
    await page.getByRole('button', { name: '添加知识库', exact: true }).click();
    await page.getByRole('button', { name: '配置连接 ' + name, exact: true }).click();
    const editor = page.getByRole('dialog', { name: '添加知识库连接', exact: true });
    await editor.getByLabel('连接名称', { exact: true }).fill(native.name);
    await editor.getByLabel(name + ' 服务地址', { exact: true }).fill(url);
    await editor.getByLabel('服务凭据', { exact: true }).fill('native-api-key');
    await editor.getByLabel('Embedding服务地址', { exact: true }).fill(native.embedding.api_base);
    await editor.getByLabel('Embedding模型名称', { exact: true }).fill(native.embedding.model);
    await editor.getByLabel('向量维度', { exact: true }).fill('768');
    await editor.getByRole('button', { name: '保存', exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(saved).toMatchObject({
      service_url: url,
      credential: 'native-api-key',
      endpoint_id: 'local-' + backend,
    });
    await page.getByRole('button', { name: native.name + ' 更多操作', exact: true }).click();
    await page.getByRole('menuitem', { name: '设置', exact: true }).click();
    const settings = page.getByRole('dialog', { name: '连接设置', exact: true });
    await expect(settings.getByLabel(name + ' 服务地址', { exact: true })).toHaveAttribute(
      'readonly',
      '',
    );
    await expect(settings.getByLabel('服务凭据', { exact: true })).toHaveValue('');
    await settings.getByRole('button', { name: '保存', exact: true }).click();
    await expect(settings).toHaveCount(0);
  });

test('Milvus creation accepts a custom address and retains settings after a rejected save', async ({
  page,
}, info) => {
  await setup(page);
  let saved: Record<string, unknown> | undefined;
  let attempts = 0;
  const native = {
    ...connection,
    kind: 'knowledge',
    backend: 'milvus',
    endpoint_id: 'milvus-bridge',
    name: '自有 Milvus',
    service_url: 'https://my-milvus.example:19530',
    status: 'UNVERIFIED',
    embedding: {
      provider: 'openai_compatible',
      api_base: 'http://192.168.1.10:11434/v1',
      model: 'my-local-model',
      dimensions: 768,
    },
  };
  const endpoints = [
    {
      id: 'milvus-bridge',
      name: '平台接入节点',
      backend: 'milvus',
      custom_service: true,
      data_use: '向量写入你填写的 Milvus 服务。',
    },
  ];
  await page.route('**/v1/context/knowledge', async (r) => {
    if (r.request().method() === 'POST') {
      saved = r.request().postDataJSON();
      attempts++;
      return attempts === 1
        ? r.fulfill({
            status: 422,
            json: { code: 'CONTEXT_INVALID', message: '请检查 Milvus 地址' },
          })
        : r.fulfill({ json: native });
    }
    return r.fulfill({
      json: {
        items: attempts > 1 ? [native] : [],
        endpoints,
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    });
  });
  await page.route('**/v1/context/knowledge/' + id, async (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      service_url: native.service_url,
      credential: '',
      expected_version: 1,
    });
    return r.fulfill({ json: { ...native, version: 2 } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '添加知识库', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 Milvus', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '添加知识库连接', exact: true });
  await expect(editor.getByLabel('Milvus 服务地址', { exact: true })).toHaveValue('');
  await expect(editor.getByLabel('接入节点', { exact: true })).toBeHidden();
  await editor.getByLabel('连接名称', { exact: true }).fill(native.name);
  await expect(editor.getByText('平台本地模型', { exact: true })).toHaveCount(0);
  await editor
    .getByLabel('Embedding服务地址', { exact: true })
    .fill('http://192.168.1.10:11434/v1');
  await editor.getByLabel('Embedding模型名称', { exact: true }).fill('my-local-model');
  await editor.getByLabel('向量维度', { exact: true }).fill('768');
  await page.screenshot({
    path: info.outputPath('milvus-connection-editor.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await editor
    .getByLabel('Milvus 服务地址', { exact: true })
    .fill('https://root:secret@my-milvus.example');
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(
    editor.getByText('填写 http:// 或 https:// 地址，不要包含路径、参数或凭据', { exact: true }),
  ).toBeVisible();
  expect(attempts).toBe(0);
  await editor.getByLabel('Milvus 服务地址', { exact: true }).fill(native.service_url);
  await editor.getByLabel('服务凭据', { exact: true }).fill('root:own-token');
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(editor.getByText('请检查 Milvus 地址', { exact: true })).toBeVisible();
  await expect(editor.getByLabel('Milvus 服务地址', { exact: true })).toHaveValue(
    native.service_url,
  );
  await expect(editor.getByLabel('服务凭据', { exact: true })).toHaveValue('root:own-token');
  expect(saved).toMatchObject({
    name: native.name,
    service_url: native.service_url,
    credential: 'root:own-token',
    endpoint_id: 'milvus-bridge',
    enabled: true,
    expected_version: 0,
    embedding: {
      provider: 'openai_compatible',
      api_base: 'http://192.168.1.10:11434/v1',
      model: 'my-local-model',
      dimensions: 768,
      api_key: '',
    },
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('milvus-custom-service.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await editor.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(editor).toHaveCount(0);
  await page.getByRole('button', { name: native.name + ' 更多操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '设置', exact: true }).click();
  const settings = page.getByRole('dialog', { name: '连接设置', exact: true });
  await expect(settings.getByLabel('Milvus 服务地址', { exact: true })).toHaveValue(
    native.service_url,
  );
  await expect(settings.getByLabel('Milvus 服务地址', { exact: true })).toHaveAttribute(
    'readonly',
    '',
  );
  await expect(settings.getByLabel('服务凭据', { exact: true })).toHaveValue('');
  await settings.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(settings).toHaveCount(0);
});

test('knowledge selects an embedding API, retains failed input and only edits its saved key', async ({
  page,
}, info) => {
  await setup(page);
  let created = false;
  let creates = 0;
  let submitted: Record<string, unknown> = {};
  const embedding = {
    provider: 'openai_compatible',
    model: 'my-embedding-model',
    api_base: 'https://embed.example/v1',
    dimensions: 768,
  };
  const native = {
    ...connection,
    kind: 'knowledge',
    backend: 'milvus',
    endpoint_id: 'milvus-bridge',
    name: '外部向量模型',
    service_url: 'https://my-milvus.example',
    embedding,
    embedding_key_configured: true,
    status: 'UNVERIFIED',
  };
  const endpoints = [
    {
      id: 'milvus-bridge',
      name: '平台接入',
      backend: 'milvus',
      custom_service: true,
      data_use: '本机中文模型生成向量，无外部模型调用费用。',
    },
  ];
  await page.route('**/v1/context/knowledge', (r) => {
    if (r.request().method() === 'POST') {
      submitted = r.request().postDataJSON();
      creates++;
      if (creates === 1)
        return r.fulfill({
          status: 422,
          json: { code: 'CONTEXT_INVALID', message: '请检查Embedding服务配置' },
        });
      created = true;
      return r.fulfill({ json: native });
    }
    return r.fulfill({
      json: {
        items: created ? [native] : [],
        endpoints,
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    });
  });
  await page.route('**/v1/context/knowledge/' + id, (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      embedding: { ...embedding, api_key: '' },
      expected_version: 1,
    });
    return r.fulfill({ json: { ...native, version: 2 } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '添加知识库', exact: true }).click();
  await page.getByRole('button', { name: '配置连接 Milvus', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '添加知识库连接', exact: true });
  await editor.getByLabel('连接名称', { exact: true }).fill(native.name);
  await editor.getByLabel('Milvus 服务地址', { exact: true }).fill(native.service_url);
  await expect(editor.getByRole('heading', { name: 'Embedding模型', exact: true })).toBeVisible();
  await expect(editor.getByText('平台本地模型', { exact: true })).toHaveCount(0);
  await editor.getByRole('button', { name: '保存', exact: true }).click();
  await expect(editor.getByText('请输入Embedding服务地址', { exact: true })).toBeVisible();
  expect(creates).toBe(0);
  await editor.getByLabel('Embedding服务地址', { exact: true }).fill(embedding.api_base);
  await editor.getByLabel('Embedding模型名称', { exact: true }).fill(embedding.model);
  await editor.getByLabel('向量维度', { exact: true }).fill('768');
  await editor.getByLabel('Embedding服务 API Key', { exact: true }).fill('private-embedding-key');
  await expect(
    editor.getByText('文档和检索问题会发送到此 Embedding 服务。', { exact: true }),
  ).toBeVisible();
  await expect(
    editor.getByText('本机中文模型生成向量，无外部模型调用费用。', { exact: true }),
  ).toHaveCount(0);
  await editor.getByRole('button', { name: '保存', exact: true }).click();
  await expect(editor.getByText('请检查Embedding服务配置', { exact: true })).toBeVisible();
  await expect(editor.getByLabel('Embedding服务 API Key', { exact: true })).toHaveValue(
    'private-embedding-key',
  );
  expect(submitted).toMatchObject({
    embedding: { ...embedding, api_key: 'private-embedding-key' },
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('embedding-api-selection.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await editor.getByRole('button', { name: '保存', exact: true }).click();
  await expect(editor).toHaveCount(0);
  await page.getByRole('button', { name: native.name + ' 更多操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '设置', exact: true }).click();
  const settings = page.getByRole('dialog', { name: '连接设置', exact: true });
  await expect(settings.getByLabel('Embedding服务地址', { exact: true })).toHaveAttribute(
    'readonly',
    '',
  );
  await expect(settings.getByLabel('Embedding模型名称', { exact: true })).toHaveValue(
    embedding.model,
  );
  await expect(settings.getByLabel('Embedding模型名称', { exact: true })).toHaveAttribute(
    'readonly',
    '',
  );
  await expect(settings.getByLabel('向量维度', { exact: true })).toBeDisabled();
  await expect(settings.getByLabel('Embedding服务 API Key', { exact: true })).toHaveValue('');
  await expect(
    settings.getByText('已保存凭据；留空保留，填写新值可更新。', { exact: true }),
  ).toBeVisible();
  await settings.getByRole('button', { name: '保存', exact: true }).click();
  await expect(settings).toHaveCount(0);
});

test('existing built-in embedding settings stay fixed when editing a legacy connection', async ({
  page,
}) => {
  await setup(page);
  const legacy = {
    ...connection,
    kind: 'knowledge',
    backend: 'milvus',
    endpoint_id: 'milvus-bridge',
    name: '旧知识库',
    service_url: 'https://my-milvus.example',
  };
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: {
        items: [legacy],
        endpoints: [
          {
            id: 'milvus-bridge',
            name: '平台接入',
            backend: 'milvus',
            custom_service: true,
            data_use: '测试',
          },
        ],
        bindings: [],
        integration: 'NOT_CONNECTED',
      },
    }),
  );
  await page.route('**/v1/context/knowledge/' + id, (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      expected_version: 1,
      embedding: { provider: 'local', model: 'BAAI/bge-small-zh-v1.5', dimensions: 512 },
    });
    return r.fulfill({ json: { ...legacy, version: 2 } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: legacy.name + ' 更多操作', exact: true }).click();
  await page.getByRole('menuitem', { name: '设置', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '连接设置', exact: true });
  await expect(
    editor.getByText('此连接使用旧版本地模型。接入自建或云端 API 请新建连接并重新导入文档。', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(editor.getByLabel('Embedding服务地址', { exact: true })).toHaveCount(0);
  await expect(editor.getByLabel('Embedding模型名称', { exact: true })).toHaveValue(
    'BAAI/bge-small-zh-v1.5',
  );
  await expect(editor.getByLabel('Embedding模型名称', { exact: true })).toHaveAttribute(
    'readonly',
    '',
  );
  await expect(editor.getByLabel('向量维度', { exact: true })).toBeDisabled();
  await editor.getByRole('button', { name: '保存', exact: true }).click();
  await expect(editor).toHaveCount(0);
});

test('knowledge search opens a window with semantic, keyword and hybrid modes', async ({
  page,
}, info) => {
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 640 });
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  const modes: string[] = [];
  const limits: number[] = [];
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [entry] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/search`, (r) => {
    const body = r.request().postDataJSON();
    modes.push(body.search_mode);
    limits.push(body.limit);
    expect(body.scope).toBeUndefined();
    expect(body.query).toBe('星河');
    if (body.search_mode === 'hybrid')
      return r.fulfill({
        status: 503,
        json: { code: 'CONTEXT_UNAVAILABLE', message: '服务暂不可用' },
      });
    return r.fulfill({
      json: {
        items:
          body.search_mode === 'keyword'
            ? []
            : [{ ...entry, snippet: entry.content.repeat(100), position: '片段 1', score: 0.9 }],
        status: 'HITS',
      },
    });
  });
  await page.route(`**/v1/context/knowledge/${id}/get`, (r) => r.fulfill({ json: entry }));
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '知识库检索', exact: true }).click();
  const modal = page.getByRole('dialog', { name: '知识库检索', exact: true });
  await expect(modal.getByLabel('检索方式')).toBeVisible();
  await modal.getByLabel('检索问题').fill('星河');
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect(modal.getByText('检索结果 · 1')).toHaveCount(0);
  await expect(modal.getByText(entry.title, { exact: true })).toBeVisible();
  await expect(modal.getByText('相关度 0.900')).toBeVisible();
  await expect(modal.getByText(entry.content.repeat(100), { exact: true })).toHaveCount(0);
  await modal.getByText(entry.title, { exact: true }).click();
  const content = modal.getByRole('region', { name: entry.title + ' 的检索内容' });
  const resultArea = modal.getByRole('region', { name: '检索结果', exact: true });
  await expect(content).toBeVisible();
  await expect
    .poll(() => resultArea.evaluate((el) => el.scrollHeight > el.clientHeight))
    .toBe(true);
  await expect(content).toHaveCSS('overflow-y', 'visible');
  await expect(content.getByRole('button', { name: '查看原文' })).toHaveCount(0);
  const searchBox = modal.getByLabel('检索问题');
  const searchY = (await searchBox.boundingBox())!.y;
  await resultArea.focus();
  await page.keyboard.press('End');
  await expect.poll(() => resultArea.evaluate((el) => el.scrollTop > 0)).toBe(true);
  expect((await searchBox.boundingBox())!.y).toBe(searchY);
  await page.screenshot({
    path: info.outputPath('knowledge-search-scroll.png'),
    animations: 'disabled',
  });
  await modal.getByRole('button', { name: '查看原文', exact: true }).click();
  await expect(page.getByRole('dialog', { name: entry.title, exact: true })).toBeVisible();
  await expect(modal).not.toBeVisible();
  await page
    .getByRole('dialog', { name: entry.title, exact: true })
    .getByRole('button', { name: /^关\s*闭$/ })
    .click();
  await expect(modal).toBeVisible();
  await expect(modal.getByText(entry.title, { exact: true })).toBeVisible();
  await modal.locator('.context-search-limit .ant-select-selector').click();
  await page.getByRole('option', { name: '10 条', exact: true }).click();
  await modal.getByText('全文检索', { exact: true }).click();
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect(modal.getByText('没有命中授权范围内的内容')).toBeVisible();
  await modal.locator('.context-search-limit .ant-select-selector').click();
  await page.getByRole('option', { name: '20 条', exact: true }).click();
  await modal.getByText('混合检索', { exact: true }).click();
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect(modal.getByText('服务暂不可用', { exact: true })).toBeVisible();
  expect(modes).toEqual(['semantic', 'keyword', 'hybrid']);
  expect(limits).toEqual([5, 10, 20]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await modal.getByText('语义检索', { exact: true }).click();
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect(modal.getByText('检索结果 · 1')).toHaveCount(0);
  await expect(modal.getByText(entry.title, { exact: true })).toBeVisible();
  await modal.getByRole('button', { name: '全部展开' }).click();
  await expect(modal.getByText(entry.content.repeat(100), { exact: true })).toBeVisible();
  await modal.getByRole('button', { name: '全部收起' }).click();
  await expect(modal.getByText(entry.content.repeat(100), { exact: true })).not.toBeVisible();
  await modal.getByRole('button', { name: '检索', exact: true }).hover();
  await page.screenshot({ path: info.outputPath('knowledge-search.png'), animations: 'disabled' });
});

test('hybrid search distinguishes keyword matches, vector similarity and fusion scores', async ({
  page,
}) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => r.fulfill({ json: { items: [] } }));
  await page.route(`**/v1/context/knowledge/${id}/search`, (r) =>
    r.fulfill({
      json: {
        items: [
          {
            ...entry,
            title: '双路结果',
            snippet: '营业收入',
            position: '片段 1',
            section: '财务 / 上半年',
            score: 0.8,
            fusion_score: 0.0325,
            match_methods: ['keyword', 'semantic'],
          },
          {
            ...entry,
            id: 'keyword',
            title: '关键词结果',
            snippet: '收入',
            position: '片段 1',
            fusion_score: 0.016,
            match_methods: ['keyword'],
          },
        ],
      },
    }),
  );
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '知识库检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '知识库检索' });
  await search.getByText('混合检索', { exact: true }).click();
  await search.getByLabel('检索问题').fill('营业收入');
  await search.getByRole('button', { name: '检索', exact: true }).click();
  const both = search.locator('.ant-collapse-item').filter({ hasText: '双路结果' });
  await expect(both).toContainText('财务 / 上半年');
  await expect(both).toContainText('关键词匹配');
  await expect(both).toContainText('向量相似度 0.800');
  await expect(both).toContainText('融合分数 0.0325');
  const keyword = search.locator('.ant-collapse-item').filter({ hasText: '关键词结果' });
  await expect(keyword).toContainText('关键词匹配');
  await expect(keyword).not.toContainText('向量相似度');
  await expect(keyword).not.toContainText('精确匹配');
  await search.getByRole('button', { name: '最低相关度说明' }).click();
  await expect(
    page.getByText('全文检索不使用相关度；混合检索中的关键词结果不受此条件限制。'),
  ).toBeVisible();
});

test('ready knowledge documents can be rebuilt with the current version', async ({ page }) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  let rebuilt = 0;
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [entry] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/rebuild`, (r) => {
    expect(r.request().postDataJSON()).toMatchObject({
      record_id: entry.id,
      record_version: entry.version,
      confirmed: true,
    });
    rebuilt++;
    return r.fulfill({ json: { rebuilt: true } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: /^重\s*建$/ }).click();
  await expect.poll(() => rebuilt).toBe(1);
});

test('knowledge search sends optional similarity threshold only for vector results', async ({
  page,
}, info) => {
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 640 });
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'milvus', name: '项目文档' };
  const calls: Record<string, unknown>[] = [];
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [], total: 0 } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/search`, (r) => {
    calls.push(r.request().postDataJSON());
    return r.fulfill({ json: { items: [], status: 'NO_HITS' } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容' }).click();
  await page.getByRole('button', { name: '知识库检索' }).click();
  const modal = page.getByRole('dialog', { name: '知识库检索' });
  const threshold = modal.getByRole('spinbutton', { name: '最低相关度', exact: true });
  await expect(threshold).toHaveValue('');
  await expect
    .poll(() =>
      modal.evaluate(
        (el) =>
          el.querySelector('#context-min-similarity')!.getBoundingClientRect().x -
          el.querySelector('.context-search-limit')!.getBoundingClientRect().x,
      ),
    )
    .toBeGreaterThan(0);
  await modal.getByLabel('检索问题').fill('发布');
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0]).not.toHaveProperty('min_similarity');
  await threshold.fill('0.75');
  await threshold.blur();
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect.poll(() => calls.length).toBe(2);
  expect(calls[1]).toMatchObject({ search_mode: 'semantic', min_similarity: 0.75 });
  await modal.getByText('全文检索', { exact: true }).click();
  await expect(threshold).toBeDisabled();
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect.poll(() => calls.length).toBe(3);
  expect(calls[2]).not.toHaveProperty('min_similarity');
  await modal.getByText('混合检索', { exact: true }).click();
  await expect(threshold).toBeEnabled();
  const help = modal.getByRole('button', { name: '最低相关度说明' });
  await expect(page.getByText('取值范围 -1 至 1，留空不过滤。')).not.toBeVisible();
  await help.click();
  await expect(help).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByText('取值范围 -1 至 1，留空不过滤。')).toBeVisible();
  await expect(
    page.getByText('全文检索不使用相关度；混合检索中的关键词结果不受此条件限制。'),
  ).toBeVisible();
  await help.click();
  await expect(help).toHaveAttribute('aria-expanded', 'false');
  await modal.getByRole('button', { name: '检索', exact: true }).click();
  await expect.poll(() => calls.length).toBe(4);
  expect(calls[3]).toMatchObject({ search_mode: 'hybrid', min_similarity: 0.75 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('knowledge search resets conditions while keeping the query', async ({ page }) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  const requests: Record<string, unknown>[] = [];
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({ json: { items: [knowledge], endpoints: [], bindings: [] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [entry] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/search`, (r) => {
    requests.push(r.request().postDataJSON());
    return r.fulfill({ json: { items: [{ ...entry, snippet: entry.content, score: 0.9 }] } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '知识库检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '知识库检索', exact: true });
  const reset = search.getByRole('button', { name: '重置条件', exact: true });
  const similarity = search.getByRole('spinbutton', { name: '最低相关度', exact: true });
  await expect(
    search.getByText('检索此知识库的全部文档，支持语义、全文和混合检索。', { exact: true }),
  ).toBeVisible();
  await expect(reset).toBeDisabled();
  await search.getByLabel('检索问题', { exact: true }).fill('项目约定');
  await search.getByText('混合检索', { exact: true }).click();
  await search.getByRole('combobox', { name: '返回条数', exact: true }).press('ArrowDown');
  await page.getByRole('option', { name: '20 条', exact: true }).click();
  await similarity.fill('0.75');
  await search.getByRole('button', { name: '检索', exact: true }).click();
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toBeVisible();
  expect(requests[0]).toEqual({
    expected_version: 1,
    query: '项目约定',
    limit: 20,
    search_mode: 'hybrid',
    min_similarity: 0.75,
  });
  await reset.click();
  await expect(reset).toBeDisabled();
  await expect(similarity).toHaveValue('');
  await expect(search.getByLabel('检索问题', { exact: true })).toHaveValue('项目约定');
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toHaveCount(0);
  await search.getByRole('button', { name: '检索', exact: true }).click();
  await expect(search.getByRole('region', { name: '检索结果', exact: true })).toBeVisible();
  expect(requests[1]).toEqual({
    expected_version: 1,
    query: '项目约定',
    limit: 5,
    search_mode: 'semantic',
  });
});

test('original document remains readable and scrollable on small screens', async ({
  page,
}, info) => {
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 640 });
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'milvus', name: '项目文档' };
  const longTitle = '项目文档'.repeat(25);
  const longSource = '工作数据/'.repeat(35);
  const original = {
    ...entry,
    title: longTitle,
    source: longSource,
    content: '发布安排：每周三。\n'.repeat(200),
  };
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [original], total: 1 } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/get`, (r) => r.fulfill({ json: original }));
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容' }).click();
  await page.getByRole('button', { name: longTitle, exact: true }).click();
  const modal = page.getByRole('dialog', { name: longTitle });
  const content = modal.getByRole('region', { name: '原文内容' });
  await expect(content).toContainText('发布安排：每周三。');
  await expect(modal.getByText(longSource, { exact: false })).toBeVisible();
  await expect(modal.getByText('更新于', { exact: true })).toBeVisible();
  await expect.poll(() => content.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
  await content.focus();
  await page.keyboard.press('End');
  await expect.poll(() => content.evaluate((el) => el.scrollTop > 0)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const close = modal.getByRole('button', { name: /^关\s*闭$/ });
  const closeBounds = await close.boundingBox();
  expect(closeBounds!.y + closeBounds!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  await page.screenshot({ path: info.outputPath('original-document.png'), animations: 'disabled' });
  await close.click();
  await expect(modal).toHaveCount(0);
});

test('workbench navigation opens memory and knowledge in the portal', async ({ page }, info) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  let popups = 0;
  page.on('popup', () => popups++);
  await page.goto('/app/memory');
  await expect(page.getByRole('region', { name: '记忆管理' })).toBeVisible();
  if (info.project.name === 'mobile') await page.getByRole('button', { name: '打开导航' }).click();
  await page.getByRole('link', { name: '知识库', exact: true }).filter({ visible: true }).click();
  await expect(page).toHaveURL(/\/app\/knowledge$/);
  await expect(page.getByRole('region', { name: '知识库管理' })).toBeVisible();
  await expect(page.getByText('未接入工作空间', { exact: true })).toHaveCount(0);
  expect(popups).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('knowledge management has no manual workspace association control', async ({ page }) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'milvus', name: 'Milvus知识库' };
  await page.route('**/v1/me', (r) =>
    r.fulfill({
      json: {
        ...user,
        permissions: [...user.permissions, 'instances:read', 'instances:configure'],
      },
    }),
  );
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: {
        items: [knowledge],
        endpoints: [],
        bindings: [{ instance_id: 'workspace-id', connection_id: id, enabled: true, version: 1 }],
        integration: 'NOT_CONNECTED',
      },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => r.fulfill({ json: { items: [] } }));
  await page.route('**/v1/instances?**', (r) =>
    r.fulfill({
      json: {
        items: [{ id: 'workspace-id', name: '测试工作空间', observed_state: 'RUNNING' }],
        total: 1,
      },
    }),
  );
  let bindings = 0;
  page.on('request', (r) => {
    if (new URL(r.url()).pathname.endsWith('/binding')) bindings++;
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const detail = page.getByRole('dialog', { name: 'Milvus知识库' });
  await expect(detail.getByText('关联工作空间', { exact: true })).toHaveCount(0);
  await expect(detail.getByRole('switch', { name: /关联/ })).toHaveCount(0);
  await expect(detail.getByLabel('内容范围')).toHaveCount(0);
  await expect(detail.getByRole('button', { name: '添加文档' })).toBeVisible();
  await expect(detail.getByText('个人语料', { exact: true })).toHaveCount(0);
  await detail.getByRole('button', { name: '知识库检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '知识库检索', exact: true });
  await expect(search.getByLabel('检索范围')).toHaveCount(0);
  await expect(search.getByRole('combobox', { name: '返回条数' })).toBeVisible();
  expect(bindings).toBe(0);
});

test('memory management shares one corpus without range or workspace controls', async ({
  page,
}, info) => {
  await setup(page);
  let instanceReads = 0;
  const actions: string[] = [];
  await page.route('**/v1/me', (r) =>
    r.fulfill({
      json: {
        ...user,
        permissions: [...user.permissions, 'instances:read', 'instances:configure'],
      },
    }),
  );
  await page.route('**/v1/instances?**', (r) => {
    instanceReads++;
    return r.fulfill({ json: { items: [], total: 0 } });
  });
  await page.route(`**/v1/context/memory/${id}/*`, (r) => {
    const action = new URL(r.request().url()).pathname.split('/').at(-1)!;
    actions.push(action);
    expect(r.request().postDataJSON()).not.toHaveProperty('scope');
    return r.fulfill({
      json: action === 'search' ? { items: [], status: 'HITS' } : { items: [entry] },
    });
  });
  await page.goto('/app/memory');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const detail = page.getByRole('dialog', { name: connection.name, exact: true });
  await expect(detail.getByText('接入的工作空间共用此连接的记忆。')).toBeVisible();
  await expect(detail.getByLabel('内容范围')).toHaveCount(0);
  await expect(detail.getByText('关联工作空间', { exact: true })).toHaveCount(0);
  await expect(detail.getByRole('button', { name: entry.title, exact: true })).toBeVisible();
  await detail.getByRole('button', { name: '记忆检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '记忆检索', exact: true });
  await expect(search.getByLabel('检索范围')).toHaveCount(0);
  await expect(search.getByText('个人记忆', { exact: true })).toHaveCount(0);
  await search.getByLabel('检索问题').fill('共享记忆');
  await search.getByRole('button', { name: '检索', exact: true }).click();
  await expect(search.getByText('没有命中授权范围内的内容')).toBeVisible();
  expect(instanceReads).toBe(0);
  expect(actions).toContain('search');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('shared-memory-search.png'),
    animations: 'disabled',
  });
});

for (const account of ['admin', 'member'])
  test(`live workbench gives ${account} personal memory and knowledge navigation`, async ({
    page,
  }, info) => {
    test.skip(
      process.env.HC_REAL_CONTEXT !== 'true',
      'requires the local platform and built-in accounts',
    );
    const { readFileSync } = await import('node:fs');
    await page.goto('/auth/login?return_to=%2Fapp%2Fmemory');
    await page.getByLabel('邮箱', { exact: true }).fill(account + '@harness.local');
    await page
      .getByLabel('密码', { exact: true })
      .fill(readFileSync('../.local/auth/' + account + '-password', 'utf8').trim());
    await page.getByRole('button', { name: '登录', exact: true }).click();
    await expect(page).toHaveURL(/\/app\/memory$/);
    await expect(page.getByRole('region', { name: '记忆管理' })).toBeVisible();
    const me = await (await page.request.get('/v1/me')).json();
    for (const p of ['memory:read', 'memory:manage', 'knowledge:read', 'knowledge:manage'])
      expect(me.permissions).toContain(p);
    await expect(page.getByRole('button', { name: '添加记忆', exact: true })).toBeVisible();
    await page.getByRole('button', { name: '添加记忆', exact: true }).click();
    const picker = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
    await expect(picker.getByRole('article')).toHaveCount(3);
    await picker.getByRole('button', { name: '配置连接 Mem0', exact: true }).click();
    const editor = page.getByRole('dialog', { name: '添加记忆连接', exact: true });
    await expect(editor.getByLabel('连接名称', { exact: true })).toHaveValue('Mem0 记忆');
    await expect(editor.getByLabel('服务凭据', { exact: true })).toBeVisible();
    await expect(editor).toHaveCSS('transform', 'none');
    await expect(editor).toHaveCSS('opacity', '1');
    await page.screenshot({
      path: info.outputPath(account + '-memory-editor.png'),
      fullPage: true,
      animations: 'disabled',
    });
    await editor.getByRole('button', { name: /^取\s*消$/ }).click();
    if (info.project.name === 'mobile')
      await page.getByRole('button', { name: '打开导航' }).click();
    await page.getByRole('link', { name: '知识库', exact: true }).filter({ visible: true }).click();
    await expect(page.getByRole('region', { name: '知识库管理' })).toBeVisible();
    await expect(page.getByRole('button', { name: '添加知识库', exact: true })).toBeVisible();
    await page.screenshot({
      path: info.outputPath(account + '-workbench-context.png'),
      fullPage: true,
      animations: 'disabled',
    });
  });

for (const backend of (process.env.HC_CONTEXT_UI_BACKENDS ?? 'mem0,qdrant').split(','))
  test(`live platform uses ${backend} with an explicitly granted test role`, async ({
    page,
    browser,
    baseURL,
  }, info) => {
    test.skip(
      process.env.HC_REAL_CONTEXT !== 'true' || info.project.name !== 'desktop',
      'requires the local external Docker services',
    );
    test.setTimeout(180_000);
    page.setDefaultTimeout(15_000);
    test.skip(
      ['milvus', 'weaviate', 'qdrant'].includes(backend) &&
        !process.env.HC_CONTEXT_TEST_EMBEDDING_FILE,
      'knowledge requires an explicitly configured Embedding API',
    );
    const { readFileSync } = await import('node:fs');
    const { randomUUID, randomBytes } = await import('node:crypto');
    const suffix = randomUUID().slice(0, 8);
    const password = randomBytes(24).toString('base64url');
    const adminContext = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: baseURL });
    const admin = await adminContext.newPage();
    admin.setDefaultTimeout(15_000);
    let member: { id: string; display_name: string; version: number } | null = null;
    let role: { id: string; version: number } | null = null;
    const created: { id: string; kind: string; name: string; version: number }[] = [];
    let headers: Record<string, string> = {};
    let adminHeaders: Record<string, string> = {};
    async function login(target: Page, email: string, secret: string) {
      await target.goto('/auth/login?return_to=%2Fapp%2Fmemory');
      await target.locator('input[name=login]').fill(email);
      await target.locator('input[name=password]').fill(secret);
      await target.getByRole('button', { name: '登录', exact: true }).click();
      await expect(target).toHaveURL(/\/app\/memory$/);
      return (await target.request.get('/v1/me')).json();
    }
    try {
      const a = await login(
        admin,
        'admin@harness.local',
        readFileSync('../.local/auth/admin-password', 'utf8').trim(),
      );
      adminHeaders = { Origin: new URL(admin.url()).origin, 'X-CSRF-Token': a.csrf_token };
      let response = await admin.request.post('/v1/roles', {
        headers: { ...adminHeaders, 'Idempotency-Key': randomUUID() },
        data: {
          code: 'ctx_' + suffix,
          name: '外接服务验证 ' + suffix,
          description: '独立测试账号专用',
          permissions: ['memory:read', 'memory:manage', 'knowledge:read', 'knowledge:manage'],
          expected_version: 0,
        },
      });
      expect(response.status()).toBe(201);
      role = await response.json();
      response = await admin.request.post('/v1/users', {
        headers: { ...adminHeaders, 'Idempotency-Key': randomUUID() },
        data: {
          display_name: '外接测试 ' + suffix,
          email: 'ctx-' + suffix + '@harness.local',
          identity_source: 'local',
          role_id: role!.id,
          initial_password: password,
        },
      });
      expect(response.status()).toBe(201);
      member = await response.json();
      const me = await login(page, 'ctx-' + suffix + '@harness.local', password);
      headers = { Origin: new URL(page.url()).origin, 'X-CSRF-Token': me.csrf_token };
      console.log('Real external backend:', backend);
      const kind = ['mem0', 'openviking', 'graphiti'].includes(backend) ? 'memory' : 'knowledge';
      const names: Record<string, string> = {
        mem0: 'Mem0',
        openviking: 'OpenViking',
        graphiti: 'Graphiti',
        milvus: 'Milvus',
        weaviate: 'Weaviate',
        qdrant: 'Qdrant',
      };
      await page.goto('/app/' + kind);
      await page
        .getByRole('button', { name: kind === 'memory' ? '添加记忆' : '添加知识库', exact: true })
        .click();
      await page.getByRole('button', { name: '配置连接 ' + names[backend], exact: true }).click();
      const editor = page.getByRole('dialog', {
        name: kind === 'memory' ? '添加记忆连接' : '添加知识库连接',
        exact: true,
      });
      await editor
        .getByLabel('连接名称', { exact: true })
        .fill('真实测试 ' + backend + ' ' + suffix);
      if (backend === 'milvus') {
        await editor
          .getByLabel('Milvus 服务地址', { exact: true })
          .fill(process.env.HC_CONTEXT_TEST_MILVUS_URL ?? 'http://hc-context-milvus-1:19530');
      } else if (['weaviate', 'qdrant'].includes(backend)) {
        await editor
          .getByLabel(names[backend] + ' 服务地址', { exact: true })
          .fill('http://' + backend + ':' + (backend === 'weaviate' ? '8080' : '6333'));
        await editor
          .getByLabel('服务凭据', { exact: true })
          .fill(readFileSync('../.local/context/test-key', 'utf8').trim());
      } else
        await editor
          .getByLabel('服务凭据', { exact: true })
          .fill(readFileSync('../.local/context/test-key', 'utf8').trim());
      if (backend === 'mem0')
        await editor
          .getByLabel('Mem0 服务地址', { exact: true })
          .fill(process.env.HC_CONTEXT_TEST_MEM0_URL ?? 'http://mem0:8580');
      if (kind === 'knowledge' && process.env.HC_CONTEXT_TEST_EMBEDDING_FILE) {
        const config = JSON.parse(readFileSync(process.env.HC_CONTEXT_TEST_EMBEDDING_FILE, 'utf8'));
        await editor.getByLabel('Embedding服务地址', { exact: true }).fill(config.api_base);
        await editor.getByLabel('Embedding模型名称', { exact: true }).fill(config.model);
        await editor.getByLabel('向量维度', { exact: true }).fill(String(config.dimensions));
        await editor
          .getByLabel('Embedding服务 API Key', { exact: true })
          .fill(config.api_key ?? '');
      }
      const saving = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === '/v1/context/' + kind && r.request().method() === 'POST',
      );
      await editor.getByRole('button', { name: /^保\s*存$/ }).click();
      const saveResponse = await saving;
      expect(saveResponse.status()).toBe(200);
      const c = await saveResponse.json();
      created.push(c);
      await expect(editor).toHaveCount(0);
      expect(c.endpoint_id).toBe('local-' + backend);
      const call = async (action: string, data: Record<string, unknown> = {}) => {
        const result = await page.request.post(`/v1/context/${kind}/${c.id}/${action}`, {
          headers,
          data: { expected_version: c.version, ...data },
        });
        expect(result.status()).toBe(200);
        return result.json();
      };
      const mutate = async (action: string, data: Record<string, unknown>) => {
        const r = await page.request.post(`/v1/context/${kind}/${c.id}/${action}`, {
          headers,
          data: { expected_version: c.version, ...data },
        });
        if (r.ok()) return r.json();
        expect((await r.json()).code).toBe('CONTEXT_PENDING');
        for (let i = 0; i < 12; i++) {
          await new Promise((resolve) => setTimeout(resolve, 3000));
          const check = await page.request.post(`/v1/context/${kind}/${c.id}/reconcile`, {
            headers,
            data: {},
          });
          if (check.ok()) return check.json();
          expect((await check.json()).code).toBe('CONTEXT_PENDING');
        }
        throw new Error('external operation did not finish');
      };
      await call('verify');
      const staged = await call('preview', {
        title: '发布约定',
        content: '合成项目每周三发布，由测试机器人负责。',
        source: '真实集成的合成数据',
      });
      if (process.env.HC_CONTEXT_TEST_EMBEDDING_FILE) {
        expect(staged.data_use).toContain('配置的 Embedding 服务');
        expect(staged.data_use).not.toContain('无外部');
        expect(c.embedding.provider).toBe('openai_compatible');
        expect(c.embedding.api_key).toBeUndefined();
        expect(c.embedding_key_configured).toBe(true);
      }
      await mutate('apply', {
        token: staged.token,
        mode: 'skip',
        idempotency_key: randomUUID(),
        confirmed: true,
      });
      const hits = await call('search', { query: '合成项目什么时候发布？', limit: 5 });
      expect(hits.items.some((h: { snippet: string }) => h.snippet.includes('每周三'))).toBe(true);
      if (kind === 'knowledge') {
        for (const [search_mode, limit] of [
          ['semantic', 5],
          ['exact', 10],
          ['hybrid', 20],
        ] as const) {
          const found = await call('search', { query: '每周三', limit, search_mode });
          const hit = found.items.find((h: { snippet: string }) => h.snippet.includes('每周三'));
          expect(hit).toBeDefined();
          if (search_mode === 'exact') expect(hit.score).toBeUndefined();
          else expect(Number.isFinite(hit.score)).toBe(true);
        }
        const rejected = await page.request.post(`/v1/context/${kind}/${c.id}/search`, {
          headers,
          data: { expected_version: c.version, query: '每周三', search_mode: 'invalid' },
        });
        expect(rejected.status()).toBe(422);
      }
      await page.goto('/app/' + kind);
      await page.getByRole('button', { name: '管理内容', exact: true }).click();
      await expect(page.getByRole('button', { name: '发布约定', exact: true })).toBeVisible();
      await page.getByRole('button', { name: '发布约定', exact: true }).click();
      await expect(page.locator('.context-content')).toContainText('每周三');
      await page
        .getByRole('dialog', { name: '发布约定', exact: true })
        .getByRole('button', { name: 'Close', exact: true })
        .click();
      const identity = staged.items[0].id;
      await mutate('delete', {
        record_id: identity,
        record_version: 1,
        idempotency_key: randomUUID(),
        confirmed: true,
      });
      expect((await call('search', { query: '合成项目', limit: 5 })).items).toHaveLength(0);
      response = await page.request.delete(`/v1/context/${kind}/${c.id}`, {
        headers,
        data: { expected_version: c.version, confirmed_name: c.name },
      });
      expect(response.status()).toBe(200);
      created.pop();
    } finally {
      for (const c of created)
        await page.request.delete(`/v1/context/${c.kind}/${c.id}`, {
          headers,
          data: { expected_version: c.version, confirmed_name: c.name },
        });
      if (member) {
        const fresh = await admin.request.get('/v1/users/' + member.id);
        if (fresh.ok()) {
          const current = await fresh.json();
          await admin.request.delete('/v1/users/' + member.id, {
            headers: adminHeaders,
            data: { expected_version: current.version, confirmed_name: current.display_name },
          });
        }
      }
      if (role)
        await admin.request.delete('/v1/roles/' + role.id, {
          headers: adminHeaders,
          data: { expected_version: role.version },
        });
      await adminContext.close();
    }
  });

test('document deletion requires confirmation and keeps the target visible', async ({
  page,
}, info) => {
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 640 });
  await setup(page);
  const filename = '国轩高科分析报告_2026-10-28_' + 'long-document-name-'.repeat(6) + '.md';
  const record = { ...entry, title: filename };
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  let deletes = 0;
  let finish = () => {};
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [record] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/delete`, async (r) => {
    deletes++;
    expect(r.request().postDataJSON()).toMatchObject({
      record_id: record.id,
      record_version: record.version,
      confirmed: true,
    });
    expect(r.request().postDataJSON()).not.toHaveProperty('scope');
    await pending;
    return r.fulfill({ json: { deleted: true } });
  });
  try {
    await page.goto('/app/knowledge');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    await page.getByRole('button', { name: /^删\s*除$/ }).click();
    const modal = page.getByRole('dialog', { name: '删除文档', exact: true });
    await expect(modal.getByText(filename, { exact: true })).toBeVisible();
    await expect(
      modal.getByText('删除后无法恢复，相关索引将一并移除。', { exact: true }),
    ).toBeVisible();
    const submit = modal.getByRole('button', { name: '永久删除', exact: true });
    await expect(submit).toBeDisabled();
    expect(deletes).toBe(0);
    await modal.getByRole('checkbox', { name: '我确认永久删除' }).check();
    await expect(submit).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await submit.hover();
    await page.screenshot({ path: info.outputPath('delete-document.png'), animations: 'disabled' });
    await submit.click();
    await expect.poll(() => deletes).toBe(1);
    await expect(modal.getByRole('checkbox', { name: '我确认永久删除' })).toBeDisabled();
    finish();
    await expect(modal).toHaveCount(0);
  } finally {
    finish();
  }
});

test('work-data picker navigates file pages and folders without losing the asset', async ({
  page,
}, info) => {
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 640 });
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  const asset = {
    id: '11111111-1111-4111-8111-111111111111',
    workspace_name: '测试项目',
    owner_id: user.id,
    capacity_bytes: 10737418240,
    usage: {},
    maintenance: { backup_blockers: [], restore_blockers: [], reclaim_blockers: [] },
    status: 'ACTIVE',
  };
  const row = (name: string, type = 'FILE') => ({
    name,
    type,
    size_bytes: 32,
    modified_at: '2026-10-23T08:00:00Z',
  });
  await page.route('**/v1/me', (r) =>
    r.fulfill({ json: { ...user, permissions: [...user.permissions, 'instances:read'] } }),
  );
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => r.fulfill({ json: { items: [] } }));
  await page.route('**/v1/instances?**', (r) =>
    r.fulfill({ json: { items: [], total: 0, page: 1, limit: 20 } }),
  );
  await page.route('**/v1/storage-assets?**', (r) => {
    const pageNumber = Number(new URL(r.request().url()).searchParams.get('page'));
    return r.fulfill({
      json: {
        items:
          pageNumber === 1
            ? [asset]
            : [
                {
                  ...asset,
                  id: '22222222-2222-4222-8222-222222222222',
                  workspace_name: '另一个项目',
                },
              ],
        total: 21,
        page: pageNumber,
        limit: 20,
      },
    });
  });
  await page.route('**/v1/storage-assets/*/files?**', (r) => {
    const params = new URL(r.request().url()).searchParams;
    const path = params.get('path') ?? '';
    const after = params.get('after');
    return r.fulfill({
      json: {
        root: params.get('root'),
        path,
        entries: path
          ? [row('nested.md')]
          : after
            ? [row('last.md')]
            : [row('资料', 'DIRECTORY'), row('first.md')],
        ...(!path && !after ? { next_after: 'first.md' } : {}),
      },
    });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '从工作数据选择', exact: true }).click();
  const picker = page.getByRole('dialog', { name: '选择工作数据文件' });
  await expect(picker.getByText('请选择数据资产', { exact: true })).toBeVisible();
  await picker.getByRole('combobox', { name: '工作数据资产' }).click();
  await page.getByText('测试项目', { exact: true }).click();
  await expect(picker.getByRole('button', { name: 'first.md', exact: true })).toBeVisible();
  await picker.getByRole('button', { name: '下一页资产' }).click();
  await expect(picker.getByText('测试项目', { exact: true })).toBeVisible();
  await picker.getByRole('button', { name: '上一页资产' }).click();
  await picker.getByRole('button', { name: '下一页文件' }).click();
  await expect(picker.getByRole('button', { name: 'last.md', exact: true })).toBeVisible();
  await picker.getByRole('button', { name: '上一页文件' }).click();
  await expect(picker.getByRole('button', { name: 'first.md', exact: true })).toBeVisible();
  await picker.getByRole('button', { name: '资料', exact: true }).click();
  await expect(picker.getByRole('button', { name: 'nested.md', exact: true })).toBeVisible();
  await picker.getByRole('button', { name: '/data/workspace', exact: true }).click();
  await expect(picker.getByRole('button', { name: 'first.md', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await picker.getByRole('button', { name: 'first.md', exact: true }).hover();
  await page.screenshot({ path: info.outputPath('work-data-picker.png'), animations: 'disabled' });
});

test('knowledge work-data import reads only the explicitly chosen UTF-8 file before preview', async ({
  page,
}, info) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  await page.route('**/v1/me', (r) =>
    r.fulfill({ json: { ...user, permissions: [...user.permissions, 'instances:read'] } }),
  );
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => r.fulfill({ json: { items: [] } }));
  await page.route('**/v1/instances?**', (r) =>
    r.fulfill({ json: { items: [], total: 0, page: 1, limit: 20 } }),
  );
  let reads = 0;
  let previews = 0;
  await page.route('**/v1/storage-assets?**', (r) =>
    r.fulfill({
      json: {
        items: [
          {
            id: '11111111-1111-4111-8111-111111111111',
            workspace_name: '测试项目',
            owner_id: user.id,
            capacity_bytes: 10737418240,
            usage: {},
            maintenance: { backup_blockers: [], restore_blockers: [], reclaim_blockers: [] },
            status: 'ACTIVE',
          },
        ],
        total: 1,
        page: 1,
        limit: 20,
      },
    }),
  );
  await page.route('**/v1/storage-assets/*/files?**', (r) =>
    r.fulfill({
      json: {
        root: 'workspace',
        path: '',
        entries: [
          { name: 'README.md', type: 'FILE', size_bytes: 32, modified_at: '2026-10-23T08:00:00Z' },
          {
            name: 'secrets.env',
            type: 'FILE',
            size_bytes: 20,
            modified_at: '2026-10-23T08:00:00Z',
          },
        ],
      },
    }),
  );
  await page.route('**/v1/storage-assets/*/file?**', (r) => {
    reads++;
    return r.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'text/plain; charset=utf-8',
        'Content-Disposition': 'inline; filename="README.md"',
      },
      body: '发布安排：每周三。',
    });
  });
  await page.route('**/v1/context/knowledge/parse', (r) => {
    expect(r.request().postData()).toContain('README.md');
    return r.fulfill({ json: { content: '发布安排：每周三。' } });
  });
  await page.route(`**/v1/context/knowledge/${id}/preview`, (r) => {
    previews++;
    expect(r.request().postDataJSON()).toMatchObject({
      content: '发布安排：每周三。',
      title: 'README.md',
      source: '工作数据：/data/workspace/README.md',
    });
    return r.fulfill({
      json: {
        token: 'selected-file',
        items: [
          {
            id: 'sample',
            title: 'README.md',
            content: '发布安排：每周三。',
            source: '工作数据：/data/workspace/README.md',
            conflict: false,
          },
        ],
        warnings: [],
        data_use:
          '原文保存在知识库接入服务，向量写入所选数据库。文档片段和检索问题会发送到配置的 Embedding 服务。',
        expires_in: 300,
      },
    });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '从工作数据选择', exact: true }).click();
  const picker = page.getByRole('dialog', { name: '选择工作数据文件' });
  expect(reads).toBe(0);
  expect(previews).toBe(0);
  await picker.getByRole('combobox', { name: '工作数据资产' }).click();
  await page.getByText('测试项目', { exact: true }).click();
  await expect(picker.getByRole('button', { name: 'secrets.env' })).toBeDisabled();
  expect(reads).toBe(0);
  await picker.getByRole('button', { name: 'README.md' }).click();
  const editor = page.getByRole('dialog', { name: '添加文档' });
  await expect(editor.getByLabel('内容', { exact: true })).toHaveValue('发布安排：每周三。');
  expect(reads).toBe(1);
  expect(previews).toBe(0);
  await editor.getByRole('button', { name: /^预\s*览$/ }).click();
  const preview = page.getByRole('dialog', { name: '确认保存文档' });
  await expect(preview).toBeInViewport();
  await preview.getByRole('button', { name: '确认保存' }).hover();
  expect(previews).toBe(1);
  await page.screenshot({ path: info.outputPath('knowledge-preview.png'), animations: 'disabled' });
});

test('knowledge content switches between source and Markdown without changing saved text', async ({
  page,
}, info) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  const markdown =
    '# 项目报告\n\n**重点内容**\n\n| 项目 | 状态 |\n| --- | --- |\n| 发布 | 完成 |\n\n[安全链接](https://example.com)\n\n[不安全链接](javascript:alert(1))';
  const record = { ...entry, title: '报告.md', content: markdown };
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: { items: [knowledge], endpoints: [], bindings: [], integration: 'NOT_CONNECTED' },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({ json: { items: [record] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/get`, (r) => r.fulfill({ json: record }));
  await page.route(`**/v1/context/knowledge/${id}/search`, (r) =>
    r.fulfill({
      json: { items: [{ ...record, snippet: markdown, position: '片段 1', score: 0.9 }] },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/preview`, (r) => {
    expect(r.request().postDataJSON()).toMatchObject({ title: '报告.md', content: markdown });
    return r.fulfill({
      json: {
        token: 'test',
        items: [{ ...record, conflict: false }],
        warnings: [],
        data_use: '测试',
        expires_in: 300,
      },
    });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  await page.getByRole('button', { name: '报告.md', exact: true }).click();
  const detail = page.getByRole('dialog', { name: '报告.md', exact: true });
  await expect(detail.getByRole('region', { name: '原文内容' })).toHaveText(markdown);
  await detail.getByText('Markdown 预览', { exact: true }).click();
  await expect(detail.getByRole('heading', { name: '项目报告' })).toBeVisible();
  await expect(detail.getByRole('table')).toBeVisible();
  await expect(detail.getByRole('link', { name: '不安全链接' })).not.toHaveAttribute(
    'href',
    /^javascript:/,
  );
  await page.screenshot({
    path: info.outputPath('knowledge-markdown.png'),
    animations: 'disabled',
  });
  await detail.getByText('原文', { exact: true }).click();
  await expect(detail.getByRole('region', { name: '原文内容' })).toHaveText(markdown);
  await detail.getByRole('button', { name: /^关\s*闭$/ }).click();
  await page.getByRole('button', { name: '知识库检索', exact: true }).click();
  const search = page.getByRole('dialog', { name: '知识库检索' });
  await search.getByLabel('检索问题').fill('项目');
  await search.getByRole('button', { name: '检索', exact: true }).click();
  await search.getByText('报告.md', { exact: true }).click();
  await search.getByText('Markdown 预览', { exact: true }).click();
  await expect(search.getByRole('heading', { name: '项目报告' })).toBeVisible();
  await search.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: '添加文档', exact: true }).click();
  const editor = page.getByRole('dialog', { name: '添加文档' });
  await editor.getByLabel('标题').fill('报告.md');
  await editor.getByLabel('内容', { exact: true }).fill(markdown);
  await editor.getByText('Markdown 预览', { exact: true }).click();
  await expect(editor.getByRole('heading', { name: '项目报告' })).toBeVisible();
  await editor.getByText('原文', { exact: true }).click();
  await expect(editor.getByLabel('内容', { exact: true })).toHaveValue(markdown);
  await editor.getByRole('button', { name: /^预\s*览$/ }).click();
  const preview = page.getByRole('dialog', { name: '确认保存文档' });
  await preview.getByText('Markdown 预览', { exact: true }).click();
  await expect(preview.getByRole('heading', { name: '项目报告' })).toBeVisible();
  await preview.getByText('原文', { exact: true }).click();
  await expect(preview.getByRole('region', { name: '原文内容' })).toHaveText(markdown);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('knowledge management clears filters and keeps document actions organized', async ({
  page,
}, info) => {
  await setup(page);
  await page.route('**/v1/me', (r) =>
    r.fulfill({ json: { ...user, permissions: [...user.permissions, 'instances:read'] } }),
  );
  const knowledge = {
    ...connection,
    kind: 'knowledge',
    backend: 'qdrant',
    name: '产品与研发知识库',
    service_url: 'https://qdrant.example.com',
    embedding: { model: 'bge-m3', dimensions: 1024 },
  };
  const documents = [
    { ...entry, id: 'handbook', title: '产品使用手册', source: '产品文档 / 使用指南', version: 3 },
    { ...entry, id: 'release', title: '版本发布流程', source: '研发规范', status: 'INDEXING' },
    { ...entry, id: 'guide', title: '新成员入门指南', source: '团队协作文档', status: 'ERROR' },
  ];
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({
      json: {
        items: [
          knowledge,
          {
            ...knowledge,
            id: 'disabled-connection',
            name: '历史项目归档',
            backend: 'milvus',
            enabled: false,
          },
        ],
        endpoints: [],
        bindings: [],
      },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) => {
    const { query } = r.request().postDataJSON();
    const items = documents.filter((v) => v.title.includes(query));
    return r.fulfill({ json: { items, total: items.length } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('searchbox', { name: '搜索知识库连接' }).fill('不存在的知识库');
  await expect(page.getByText('没有符合条件的知识库连接')).toBeVisible();
  await page.getByRole('button', { name: '清除筛选' }).click();
  await expect(
    page.getByRole('article', { name: '知识库连接 产品与研发知识库', exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('knowledge-management.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await page
    .getByRole('article', { name: '知识库连接 产品与研发知识库', exact: true })
    .getByRole('button', { name: '管理内容', exact: true })
    .click();
  const drawer = page.getByRole('dialog', { name: knowledge.name, exact: true });
  await expect(drawer.getByText('接入的工作空间共用此知识库的文档。')).toBeVisible();
  await expect(drawer.getByRole('button', { name: '产品使用手册', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '从工作数据选择', exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '添加文档', exact: true })).toBeEnabled();
  await expect(drawer.getByLabel('内容范围')).toHaveCount(0);
  await drawer.getByRole('textbox', { name: '搜索文档名称' }).fill('没有的文档');
  await expect(drawer.getByText('没有符合条件的文档')).toBeVisible();
  await drawer.getByRole('button', { name: '清除搜索', exact: true }).click();
  await expect(drawer.getByRole('button', { name: '产品使用手册', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: info.outputPath('knowledge-documents.png'),
    animations: 'disabled',
  });
});

test('knowledge rebuild shows the active document, refresh phase and completion', async ({
  page,
}, info) => {
  await setup(page);
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 320, height: 740 });
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  let rebuilt = false;
  let writes = 0;
  let finishRebuild = () => {};
  let finishRefresh = () => {};
  const rebuilding = new Promise<void>((resolve) => {
    finishRebuild = resolve;
  });
  const refreshing = new Promise<void>((resolve) => {
    finishRefresh = resolve;
  });
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({ json: { items: [knowledge], endpoints: [], bindings: [] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, async (r) => {
    if (rebuilt) await refreshing;
    return r.fulfill({
      json: {
        items: [
          { ...entry, version: rebuilt ? 2 : 1 },
          { ...entry, id: 'another-document', title: '另一份文档' },
        ],
      },
    });
  });
  await page.route(`**/v1/context/knowledge/${id}/rebuild`, async (r) => {
    writes++;
    expect(r.request().postDataJSON()).toMatchObject({
      record_id: entry.id,
      record_version: 1,
      confirmed: true,
    });
    await rebuilding;
    rebuilt = true;
    return r.fulfill({ json: { rebuilt: true } });
  });
  try {
    await page.goto('/app/knowledge');
    await page.getByRole('button', { name: '管理内容', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: '项目文档', exact: true });
    const row = drawer
      .getByRole('row')
      .filter({ has: page.getByRole('button', { name: entry.title, exact: true }) });
    await row.getByRole('button', { name: /^重\s*建$/ }).click();
    await expect.poll(() => writes).toBe(1);
    await expect(row.getByRole('button', { name: '重建中', exact: true })).toBeDisabled();
    await expect(row.getByRole('button', { name: '重建中', exact: true })).toHaveClass(
      /ant-btn-loading/,
    );
    await expect(row.getByText('重建中', { exact: true })).toHaveCount(2);
    await expect(drawer.getByRole('status').filter({ hasText: '正在重建索引' })).toContainText(
      entry.title,
    );
    await expect(drawer.getByRole('button', { name: '添加文档', exact: true })).toBeDisabled();
    await expect(
      drawer
        .getByRole('row')
        .filter({ hasText: '另一份文档' })
        .getByRole('button', { name: /^重\s*建$/ }),
    ).toBeDisabled();
    const feedback = drawer.getByRole('status').filter({ hasText: '正在重建索引' });
    await feedback.scrollIntoViewIfNeeded();
    await expect(feedback).toBeInViewport();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: info.outputPath('knowledge-rebuild-progress.png'),
      animations: 'disabled',
    });
    finishRebuild();
    await expect(drawer.getByRole('status').filter({ hasText: '正在更新文档列表' })).toContainText(
      entry.title,
    );
    await expect(row.getByRole('button', { name: '重建中', exact: true })).toBeDisabled();
    finishRefresh();
    await expect(drawer.getByRole('status').filter({ hasText: '重建完成' })).toContainText(
      entry.title,
    );
    await expect(row.getByText('v2', { exact: true })).toBeVisible();
    await expect(row.getByRole('button', { name: /^重\s*建$/ })).toBeEnabled();
    await expect(row.getByText('重建中', { exact: true })).toHaveCount(0);
    await expect(drawer.getByRole('status').filter({ hasText: '重建完成' })).toHaveCount(0, {
      timeout: 5000,
    });
  } finally {
    finishRebuild();
    finishRefresh();
  }
});

test('knowledge rebuild failure clears progress and permits retry with the refreshed version', async ({
  page,
}) => {
  await setup(page);
  const knowledge = { ...connection, kind: 'knowledge', backend: 'qdrant', name: '项目文档' };
  let writes = 0;
  await page.route('**/v1/context/knowledge', (r) =>
    r.fulfill({ json: { items: [knowledge], endpoints: [], bindings: [] } }),
  );
  await page.route(`**/v1/context/knowledge/${id}/list`, (r) =>
    r.fulfill({
      json: {
        items: [{ ...entry, version: writes ? 2 : 1, status: writes === 1 ? 'ERROR' : 'READY' }],
      },
    }),
  );
  await page.route(`**/v1/context/knowledge/${id}/rebuild`, (r) => {
    writes++;
    if (writes === 1)
      return r.fulfill({
        status: 503,
        json: { code: 'CONTEXT_UNAVAILABLE', message: 'Embedding 服务暂时不可用' },
      });
    expect(r.request().postDataJSON()).toMatchObject({ record_version: 2 });
    return r.fulfill({ json: { rebuilt: true } });
  });
  await page.goto('/app/knowledge');
  await page.getByRole('button', { name: '管理内容', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: '项目文档', exact: true });
  await drawer.getByRole('button', { name: /^重\s*建$/ }).click();
  await expect(
    drawer.getByText('重建失败：Embedding 服务暂时不可用', { exact: true }),
  ).toBeVisible();
  await expect(drawer.getByText('索引失败', { exact: true })).toBeVisible();
  await expect(drawer.getByRole('button', { name: '重建中', exact: true })).toHaveCount(0);
  await expect(drawer.getByRole('status').filter({ hasText: '重建完成' })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: /^重\s*建$/ })).toBeEnabled();
  await drawer.getByRole('button', { name: /^重\s*建$/ }).click();
  await expect(drawer.getByRole('status').filter({ hasText: '重建完成' })).toContainText(
    entry.title,
  );
  expect(writes).toBe(2);
  await expect(drawer.getByText('重建失败：Embedding 服务暂时不可用', { exact: true })).toHaveCount(
    0,
  );
});
