import { defineConfig, devices } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
const access = existsSync('../.local/access-config.json')
  ? JSON.parse(readFileSync('../.local/access-config.json', 'utf8'))
  : undefined;
const ipAddress = access?.ip_access?.address;
const baseURL =
  process.env.HC_TEST_PORTAL_URL ??
  (access?.ip_only
    ? `https://${ipAddress.includes(':') ? `[${ipAddress}]` : ipAddress}:${access.ip_access.portal_port}`
    : access
      ? 'https://portal.harness.localhost:42013'
      : existsSync('../.local/auth/cert.pem')
        ? 'https://127.0.0.1:42013'
        : 'http://127.0.0.1:42013');

export default defineConfig({
  testDir: './tests',
  workers: process.env.HC_REAL_IDENTITY === 'true' ? 1 : undefined,
  use: {
    baseURL,
    ignoreHTTPSErrors: true,
    browserName: 'chromium',
    launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH },
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['iPhone 13'], defaultBrowserType: 'chromium' } },
  ],
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port ' + (new URL(baseURL).port || '42013'),
    url: baseURL,
    ignoreHTTPSErrors: true,
    reuseExistingServer: !process.env.CI,
  },
});
