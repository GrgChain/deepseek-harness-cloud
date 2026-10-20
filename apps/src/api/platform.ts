export type PlatformStatus = {
  online: boolean;
  dependenciesReady: boolean;
  authenticationReady: boolean;
};

export class PlatformClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}

  async status(signal?: AbortSignal): Promise<PlatformStatus> {
    const timeout = AbortSignal.timeout(5000);
    const options: RequestInit = { credentials: 'same-origin', cache: 'no-store', signal: signal ? AbortSignal.any([signal, timeout]) : timeout };
    const live = await this.fetcher('/livez', options);
    if (!live.ok) throw new Error('STATUS_UNAVAILABLE');
    const body: unknown = await live.json();
    if (!body || typeof body !== 'object' || !('status' in body) || body.status !== 'alive'
      || !('process' in body) || body.process !== 'api') throw new Error('STATUS_INVALID');
    const capabilities = 'capabilities' in body ? body.capabilities : undefined;
    const authenticationReady = !!capabilities && typeof capabilities === 'object'
      && 'authentication' in capabilities && capabilities.authentication === true;
    const ready = await this.fetcher('/readyz', options);
    if (ready.status !== 200 && ready.status !== 503) throw new Error('STATUS_UNAVAILABLE');
    if (ready.ok) {
      const readiness: unknown = await ready.json();
      if (!readiness || typeof readiness !== 'object' || !('status' in readiness)
        || readiness.status !== 'ready' || !('process' in readiness) || readiness.process !== 'api') {
        throw new Error('STATUS_INVALID');
      }
    }
    return { online: true, dependenciesReady: ready.ok, authenticationReady };
  }
}
