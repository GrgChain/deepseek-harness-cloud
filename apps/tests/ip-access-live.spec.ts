import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

test.use({
  launchOptions: {
    executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
    args: ['--no-proxy-server', '--host-resolver-rules=MAP *.harness.localhost ~NOTFOUND'],
  },
});

test('IP login, management, workspace frames and logout work without browser domain resolution', async ({
  page,
  context,
  baseURL,
}, info) => {
  test.skip(
    process.env.HC_IP_ACCESS_LIVE !== 'true' || !process.env.HC_ENTRY_INSTANCE_ID,
    'Requires the configured IP entry, local fixture credentials and a ready owner workspace',
  );
  test.setTimeout(90_000);
  const portal = new URL(baseURL!);
  expect(portal.protocol).toBe('https:');
  expect(portal.hostname).toMatch(/^(?:\d+\.\d+\.\d+\.\d+|\[.+\])$/);
  const id = process.env.HC_ENTRY_INSTANCE_ID!;
  const domainRequests: string[] = [];
  const errors: string[] = [];
  context.on('request', (request) => {
    if (new URL(request.url()).hostname.endsWith('.harness.localhost'))
      domainRequests.push(new URL(request.url()).pathname);
  });
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/auth/login?return_to=%2Fapp%2Finstances');
  expect(new URL(page.url()).origin).toBe(portal.origin);
  await page.locator('input[name=login]').fill('admin@harness.local');
  await page
    .locator('input[name=password]')
    .fill(readFileSync('../.local/auth/admin-password', 'utf8').trim());
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(portal.origin + '/app/instances');
  const me = await (await page.request.get('/v1/me')).json();
  for (const route of [
    'instances',
    'storage',
    'skills',
    'connectors',
    'memory',
    'knowledge',
    'plugins',
    'models',
    'users',
    'roles',
    'quotas',
    'audit',
    'settings',
  ]) {
    const response = await page.goto('/app/' + route);
    expect(response?.status()).toBe(200);
    await expect(page.locator('.ant-layout-content')).toBeVisible();
    expect(new URL(page.url()).origin).toBe(portal.origin);
  }
  const missingCSRF = await page.request.post(`/v1/instances/${id}:access-endpoint`, {
    headers: { Origin: portal.origin },
    data: {},
  });
  expect(missingCSRF.status()).toBe(403);
  const endpoint = await page.request.post(`/v1/instances/${id}:access-endpoint`, {
    headers: { Origin: portal.origin, 'X-CSRF-Token': me.csrf_token },
    data: {},
  });
  expect(endpoint.status()).toBe(200);
  const discovered = await endpoint.json();
  const origin = new URL(discovered.ip_origin);
  const workspace = origin.origin + discovered.ip_base_path;
  expect(discovered.ip_base_path).toBe(`/dsh/${id}`);
  expect(origin.hostname).toBe(portal.hostname);
  expect(origin.origin).toBe(portal.origin);
  const foreignOrigin = await page.request.post(`/v1/instances/${id}:access-endpoint`, {
    headers: { Origin: 'https://' + portal.hostname + ':43000', 'X-CSRF-Token': me.csrf_token },
    data: {},
  });
  expect(foreignOrigin.status()).toBe(403);
  await page.goto('/app/instances/' + id);
  const popupPromise = page.waitForEvent('popup');
  await page.getByRole('button', { name: '进入工作空间', exact: true }).click();
  const popup = await popupPromise;
  const escapedRequests: string[] = [];
  popup.on('request', (request) => {
    const url = new URL(request.url());
    if (url.origin === portal.origin && !url.pathname.startsWith(discovered.ip_base_path + '/'))
      escapedRequests.push(url.pathname);
  });
  let frames = 0;
  let closed = false;
  popup.on('websocket', (socket) => {
    expect(new URL(socket.url()).origin).toBe(portal.origin.replace('https:', 'wss:'));
    expect(new URL(socket.url()).pathname).toBe(discovered.ip_base_path + '/api/remote.mux');
    socket.on('framereceived', () => frames++);
    socket.on('close', () => {
      closed = true;
    });
  });
  await popup.waitForURL(workspace + '/', { timeout: 30_000 });
  await expect.poll(() => frames, { timeout: 20_000 }).toBeGreaterThan(0);
  expect(await popup.evaluate(() => window.opener === null)).toBe(true);
  expect(await popup.evaluate(() => document.cookie.includes('__Host-hc_session'))).toBe(false);
  const cookies = await context.cookies(workspace + '/');
  expect(
    cookies.some(
      (cookie) =>
        cookie.name === '__Secure-hc_access_' + id.replaceAll('-', '') &&
        cookie.httpOnly &&
        cookie.secure &&
        cookie.sameSite === 'Strict' &&
        cookie.path === discovered.ip_base_path + '/',
    ),
  ).toBe(true);
  expect(cookies.some((cookie) => cookie.name.startsWith('dsh-auth-'))).toBe(false);
  await popup.screenshot({ path: info.outputPath('ip-workspace.png'), animations: 'disabled' });
  const logout = await page.request.post('/auth/logout', {
    headers: { Origin: portal.origin, 'X-CSRF-Token': me.csrf_token },
  });
  expect(logout.status()).toBe(204);
  expect((await page.request.get('/v1/me')).status()).toBe(401);
  expect(
    (await page.request.get(workspace + '/', { headers: { Accept: 'application/json' } })).status(),
  ).toBe(401);
  await expect.poll(() => closed, { timeout: 15_000 }).toBe(true);
  expect(domainRequests).toEqual([]);
  expect(errors).toEqual([]);
  expect(escapedRequests).toEqual([]);
});
