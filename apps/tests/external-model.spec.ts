import { expect, test } from '@playwright/test';

const current = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', status: 'ACTIVE', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员' }], permissions: ['configuration:read', 'model:write', 'model:validate'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };
const health = { status: 'HEALTHY', checked_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', reason_code: '' };

// Organization gateway models remain an operator API. Platform configuration
// must not restore their removed editor or silently request private credentials.
for (const alias of ['dsh-kimi-v1', 'dsh-zai-v1']) {
  test(`runtime settings keep legacy organization model ${alias} out of the page`, async ({ page }) => {
    const modelRequests: string[] = [];
    await page.route('**/v1/**', route => route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } }));
    await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
    await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
    await page.route('**/v1/me', route => route.fulfill({ json: current }));
    await page.route('**/v1/defaults', route => route.fulfill({ json: { version: 1, template_id: 'template', template_version: 'v1', spec_id: 'spec', model_configuration_id: alias, model_configuration_version: 1, spec: { id: 'spec', cpu_millicores: 2000, memory_bytes: 4 * 2 ** 30, storage_bytes: 10 * 2 ** 30 }, retention_days: 7, max_user_instances: 2 } }));
    await page.route('**/v1/platform-checks', route => route.fulfill({ json: { ready: true, checks: [{ name: 'database', health }, { name: 'model', health }, { name: 'model_adapter', health }] } }));
    await page.route('**/v1/organization-quota', route => route.fulfill({ json: { version: 1, limits: { max_instances: 20, cpu_millicores: 40000, memory_bytes: 80 * 2 ** 30, storage_bytes: 200 * 2 ** 30 }, usage: { max_instances: 0, cpu_millicores: 0, memory_bytes: 0, storage_bytes: 0 }, default_user_instances: 2 } }));
    page.on('request', request => { if (/\/v1\/(model-|my-)/.test(new URL(request.url()).pathname)) modelRequests.push(request.url()); });
    await page.goto('/app/settings');
    await expect(page.getByRole('region', { name: '默认用户资源配置', exact: true })).toContainText('2 核');
    await expect(page.getByRole('region', { name: '检查结果', exact: true })).toContainText('数据库');
    await expect(page.getByText(alias, { exact: true })).toHaveCount(0);
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await expect(page.getByRole('button', { name: '保存模型配置', exact: true })).toHaveCount(0);
    await expect(page.getByText('默认模型', { exact: true })).toHaveCount(0);
    expect(modelRequests).toEqual([]);
  });
}
