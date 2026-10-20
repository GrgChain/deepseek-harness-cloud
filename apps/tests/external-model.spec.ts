import { expect, test } from '@playwright/test';

const current = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', status: 'ACTIVE', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员' }], permissions: ['configuration:read', 'model:write', 'model:validate'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };
const endpoint = 'https://models.enterprise.example:14000';
const models = [
  { id: 'dsh-kimi-v1', response_models: ['kimi-k2.5'] },
  { id: 'dsh-zai-v1', response_models: ['glm-4.7'] },
].map(model => ({ ...model, context_window: 32768, max_output_tokens: 4096, streaming: true, tool_use: true, revision: 'routes-v1', digest: 'sha256:' + 'a'.repeat(64) }));

for (const alias of models.map(model => model.id)) {
  test(`gateway-only deployment selects ${alias}, refreshes its saved version and clears the virtual key`, async ({ page }) => {
    let saved: Record<string, unknown> | undefined;
    let writes = 0;
    let refreshes = 0;
    await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
    await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
    await page.route('**/v1/me', route => route.fulfill({ json: current }));
    await page.route('**/v1/preflight', async route => {
      // Model absence is delivered first; the approved gateway arrives later.
      await new Promise(resolve => setTimeout(resolve, 250));
      await route.fulfill({ json: { can_create: false, checks: [], approved_connections: [{ provider: 'platform-models', protocol: 'anthropic-messages', endpoint, models }] } });
    });
    await page.route('**/v1/templates', route => route.fulfill({ json: { items: [] } }));
    await page.route('**/v1/defaults', route => route.fulfill({ status: 404, json: { code: 'DEFAULTS_NOT_CONFIGURED' } }));
    await page.route('**/v1/model-configuration', async route => {
      if (route.request().method() === 'GET') { await route.fulfill(saved ? { json: saved } : { status: 404, json: { code: 'NOT_FOUND' } }); return; }
      writes++;
      expect(route.request().postDataJSON()).toEqual({ provider: 'platform-models', protocol: 'anthropic-messages', endpoint, model: alias, expected_version: 0, api_key: 'synthetic-virtual-key' });
      saved = { id: '00000000-0000-4000-8000-000000000043', version: 1, provider: 'platform-models', protocol: 'anthropic-messages', endpoint, model: alias, credential_configured: true, manifest: models.find(model => model.id === alias), status: 'UNVALIDATED', validation: { status: 'UNKNOWN', checked_at: null, expires_at: null, reason_code: 'VALIDATION_REQUIRED' } };
      await route.fulfill({ json: saved });
    });
    await page.route('**/v1/model-directory?version=1', route => route.fulfill({ json: { version: 1, items: [], health: { status: 'UNKNOWN', checked_at: null, expires_at: null, reason_code: 'DIRECTORY_REFRESH_REQUIRED' } } }));
    await page.route('**/v1/model-directory:refresh', async route => {
      refreshes++;
      expect(route.request().postDataJSON()).toEqual({ version: 1 });
      expect(await route.request().headerValue('x-csrf-token')).toBe(current.csrf_token);
      await route.fulfill({ json: { version: 1, items: models, health: { status: 'HEALTHY', checked_at: '2026-10-13T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', reason_code: '' } } });
    });
    await page.goto('/app/settings');
    await expect(page.getByText(endpoint, { exact: true })).toBeVisible();
    await page.getByLabel('模型名称', { exact: true }).click();
    await page.getByTitle(alias, { exact: true }).click();
    await page.getByLabel('虚拟密钥', { exact: true }).fill('synthetic-virtual-key');
    await page.getByRole('button', { name: '保存模型配置', exact: true }).click();
    await expect(page.getByText('配置已保存，请校验新版本', { exact: true })).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveValue('');
    await page.getByRole('button', { name: '刷新模型目录', exact: true }).click();
    await expect(page.locator('.ant-tag').filter({ hasText: /^dsh-kimi-v1$/ })).toBeVisible();
    await expect(page.locator('.ant-tag').filter({ hasText: /^dsh-zai-v1$/ })).toBeVisible();
    expect(writes).toBe(1);
    expect(refreshes).toBe(1);
    expect(await page.evaluate(() => JSON.stringify({ localStorage, sessionStorage }))).not.toContain('synthetic-virtual-key');
  });
}
