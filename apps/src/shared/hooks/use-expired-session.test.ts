import { describe, expect, it } from 'vitest';
import { loginReturnPath } from './use-expired-session';

describe('loginReturnPath', () => {
  it('allows valid internal app paths and rejects open redirects', () => {
    expect(loginReturnPath('/app/instances')).toBe('/app/instances');
    expect(loginReturnPath('/app/storage/abc/browse')).toBe('/app/storage/abc/browse');
    expect(loginReturnPath('https://evil.example/phish')).toBe('/app/instances');
    expect(loginReturnPath('//evil.example/phish')).toBe('/app/instances');
    expect(loginReturnPath('/app/instances?foo=bar')).toBe('/app/instances');
    expect(loginReturnPath('/login')).toBe('/app/instances');
    expect(loginReturnPath(null)).toBe('/app/instances');
  });
});
