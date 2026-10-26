import { expect, test } from '@playwright/test';

async function platform(page: import('@playwright/test').Page) {
  await page.route('**/livez', route => route.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', route => route.fulfill({ json: { status: 'ready', process: 'api' } }));
}

for (const path of ['/app/instances', '/app/storage/00000000-0000-4000-8000-000000000003/browse', '/app/skills']) {
  test(`anonymous ${path} goes directly to login and preserves destination`, async ({ page }) => {
    await platform(page);
    await page.route('**/v1/me', route => route.fulfill({ status: 401, json: { code: 'UNAUTHENTICATED' } }));
    let redirects = 0;
    await page.route('**/auth/login?*', route => {
      redirects++;
      expect(new URL(route.request().url()).searchParams.get('return_to')).toBe(path);
      return route.fulfill({ contentType: 'text/html; charset=utf-8', body: '<h1>登录账户</h1>' });
    });
    await page.goto(path);
    await expect(page.getByRole('heading', { name: '登录账户' })).toBeVisible();
    expect(redirects).toBe(1);
    await expect(page.getByRole('navigation')).toHaveCount(0);
  });
}

test('authentication dependency failure offers recovery without redirecting', async ({ page }) => {
  await platform(page);
  let unavailable = true;
  await page.route('**/v1/me', route => unavailable ? route.fulfill({ status: 503, json: { code: 'DEPENDENCY_UNAVAILABLE' } }) : route.fulfill({ status: 401, json: { code: 'UNAUTHENTICATED' } }));
  await page.route('**/auth/login?*', route => route.fulfill({ contentType: 'text/html; charset=utf-8', body: '<h1>登录账户</h1>' }));
  await page.goto('/app/skills');
  await expect(page.getByText('无法验证用户会话', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/app\/skills$/);
  unavailable = false;
  await page.getByRole('button', { name: '重新检查', exact: true }).click();
  await expect(page.getByRole('heading', { name: '登录账户' })).toBeVisible();
});

test('login rejects an external return address and does not replay query actions', async ({ page }) => {
  await platform(page);
  await page.route('**/v1/me', route => route.fulfill({ status: 401, json: {} }));
  await page.route('**/auth/login?*', route => {
    expect(new URL(route.request().url()).searchParams.get('return_to')).toBe('/app/instances');
    return route.fulfill({ contentType: 'text/html; charset=utf-8', body: '<h1>登录账户</h1>' });
  });
  await page.goto('/login?return_to=https%3A%2F%2Fexample.com');
  await expect(page.getByRole('heading', { name: '登录账户' })).toBeVisible();
  await page.goto('/app/instances?resume_workspace=1');
  await expect(page.getByRole('heading', { name: '登录账户' })).toBeVisible();
});
