import { APIError } from './error';
import type { CurrentUser } from './identity';

export type ContextKind = 'memory' | 'knowledge';
export interface EmbeddingConfig {
  provider: 'local' | 'openai_compatible';
  model: string;
  dimensions: number;
  api_base?: string;
  api_key?: string;
}
export interface ContextConnection {
  id: string;
  kind: ContextKind;
  backend: string;
  endpoint_id: string;
  service_url?: string;
  embedding?: EmbeddingConfig;
  embedding_key_configured?: boolean;
  name: string;
  version: number;
  enabled: boolean;
  status: 'UNVERIFIED' | 'READY' | 'FAILED';
  capabilities: Record<string, unknown>;
  verified_at: string | null;
  updated_at: string;
  integration: 'NOT_CONNECTED';
}
export interface ContextEndpoint {
  id: string;
  name: string;
  backend: string;
  data_use: string;
  custom_service?: boolean;
}
export interface ContextBinding {
  instance_id: string;
  connection_id: string;
  version: number;
  enabled: boolean;
  integration: string;
}
export interface ContextList {
  items: ContextConnection[];
  endpoints: ContextEndpoint[];
  bindings: ContextBinding[];
  integration: string;
}
export interface ContextRecord {
  id: string;
  title: string;
  content?: string;
  type: string;
  source: string;
  scope: string;
  version: number;
  status: string;
  created_at: string;
  updated_at: string;
  snippet?: string;
  position?: string;
  section?: string;
  score?: number;
  fusion_score?: number;
  match_methods?: ('keyword' | 'semantic')[];
  conflict?: boolean;
  memory_source?: 'automatic' | 'manual';
}
export interface ContextPreview {
  token: string;
  items: ContextRecord[];
  warnings: string[];
  data_use: string;
  expires_in: number;
  source_counts?: { manual: number; automatic: number };
}
export interface MemoryArchive {
  schema: 'hc-memory-v1' | 'hc-memory-v2';
  backend: string;
  exported_at: string;
  entries: unknown[];
  automatic_entries?: { uri: string; content: string; updated_at?: string }[];
  sha256: string;
}
export class ContextClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  async request<T>(
    user: CurrentUser,
    kind: ContextKind,
    path = '',
    body?: unknown,
    method = body === undefined ? 'GET' : 'POST',
    signal?: AbortSignal,
    wire?: { body?: string; response?: 'text' },
  ): Promise<T> {
    const timeout = AbortSignal.timeout(
      kind === 'memory' && /\/(export|preview)$/.test(path) ? 95_000 : 22_000,
    );
    const response = await this.fetcher(`/v1/context/${kind}${path}`, {
      method,
      credentials: 'same-origin',
      cache: 'no-store',
      headers:
        body === undefined
          ? undefined
          : { 'Content-Type': 'application/json', 'X-CSRF-Token': user.csrf_token },
      body: body === undefined ? undefined : (wire?.body ?? JSON.stringify(body)),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
    if (!response.ok) {
      const problem = await response.json().catch(() => ({}));
      const error = new APIError(response.status, problem.code ?? 'DEPENDENCY_UNAVAILABLE');
      if (
        [
          'CONTEXT_INVALID',
          'CONTEXT_UNAVAILABLE',
          'CONTEXT_DISABLED',
          'CONTEXT_UNSUPPORTED',
          'CONTEXT_PENDING',
          'ENDPOINT_NOT_APPROVED',
          'NOT_FOUND',
          'VERSION_CONFLICT',
          'PAYLOAD_TOO_LARGE',
        ].includes(problem.code) &&
        typeof problem.message === 'string'
      )
        error.message = problem.message;
      throw error;
    }
    return wire?.response === 'text' ? ((await response.text()) as T) : response.json();
  }
  list(user: CurrentUser, kind: ContextKind, signal?: AbortSignal) {
    return this.request<ContextList>(user, kind, '', undefined, undefined, signal);
  }
  call<T>(
    user: CurrentUser,
    connection: ContextConnection,
    action: string,
    body: Record<string, unknown> = {},
  ) {
    return this.request<T>(user, connection.kind, `/${connection.id}/${action}`, {
      expected_version: connection.version,
      ...body,
      scope: undefined,
    });
  }
  exportArchive(user: CurrentUser, connection: ContextConnection) {
    return this.request<string>(
      user,
      connection.kind,
      `/${connection.id}/export`,
      { expected_version: connection.version },
      'POST',
      undefined,
      { response: 'text' },
    );
  }
  previewArchive(user: CurrentUser, connection: ContextConnection, json: string) {
    // Preserve JSON numeric literals in graph metadata. Parsing and serializing
    // through JavaScript can change large integers and the package checksum.
    return this.request<ContextPreview>(
      user,
      connection.kind,
      `/${connection.id}/preview`,
      {},
      'POST',
      undefined,
      { body: `{"expected_version":${JSON.stringify(connection.version)},"package":${json}}` },
    );
  }
  async callAndConfirm<T>(
    user: CurrentUser,
    connection: ContextConnection,
    action: string,
    body: Record<string, unknown>,
    onPending: () => void,
    signal: AbortSignal,
  ): Promise<T> {
    const attempt = () =>
      this.request<T>(
        user,
        connection.kind,
        `/${connection.id}/${action}`,
        { expected_version: connection.version, ...body, scope: undefined },
        'POST',
        signal,
      );
    const uncertain = (cause: unknown) =>
      cause instanceof APIError ? cause.code === 'CONTEXT_PENDING' : !signal.aborted;
    let pending: unknown;
    try {
      return await attempt();
    } catch (cause) {
      if (!uncertain(cause)) throw cause;
      pending = cause;
    }
    onPending();
    const deadline = Date.now() + 90_000;
    while (Date.now() < deadline) {
      await new Promise<void>((resolve, reject) => {
        if (signal.aborted) {
          reject(signal.reason);
          return;
        }
        const abort = () => {
          window.clearTimeout(timer);
          reject(signal.reason);
        };
        const timer = window.setTimeout(() => {
          signal.removeEventListener('abort', abort);
          resolve();
        }, 3000);
        signal.addEventListener('abort', abort, { once: true });
      });
      try {
        await this.request(
          user,
          connection.kind,
          `/${connection.id}/reconcile`,
          {},
          'POST',
          signal,
        );
      } catch (cause) {
        if (uncertain(cause)) {
          pending = cause;
          continue;
        }
        if (!(cause instanceof APIError && cause.code === 'NOT_FOUND')) throw cause;
      }
      // Reconciliation may have completed an earlier operation. Only a receipt
      // for this exact request proves that the requested operation completed.
      try {
        return await attempt();
      } catch (cause) {
        if (!uncertain(cause)) throw cause;
        pending = cause;
      }
    }
    throw pending;
  }
}
