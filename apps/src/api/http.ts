import { APIError } from './error';
import type { CurrentUser } from './identity';

export interface APIFetchOptions {
  method?: string;
  user?: Pick<CurrentUser, 'csrf_token'>;
  body?: unknown;
  rawBody?: string;
  headers?: Record<string, string>;
  timeoutMs?: number;
  signal?: AbortSignal;
  fetcher?: typeof fetch;
  errorMessages?: Record<string, string>;
  passthroughError?: (code: string) => boolean;
}

export function combineAbortSignals(
  signal?: AbortSignal,
  timeoutMs?: number,
): AbortSignal | undefined {
  const timeout = timeoutMs !== undefined ? AbortSignal.timeout(timeoutMs) : undefined;
  if (signal && timeout) return AbortSignal.any([signal, timeout]);
  return signal ?? timeout;
}

export async function assertAPIResponse(
  response: Response,
  passthroughError?: (code: string) => boolean,
  errorMessages?: Record<string, string>,
): Promise<Response> {
  if (!response.ok) {
    const problem = (await response.json().catch(() => ({}))) as {
      code?: unknown;
      message?: unknown;
    };
    const code = typeof problem.code === 'string' ? problem.code : 'DEPENDENCY_UNAVAILABLE';
    const error = new APIError(response.status, code);
    if (errorMessages?.[code]) {
      error.message = errorMessages[code];
    }
    if (passthroughError?.(code) && typeof problem.message === 'string' && problem.message.trim()) {
      error.message = problem.message;
    }
    throw error;
  }
  return response;
}

export async function apiFetch(path: string, options: APIFetchOptions = {}): Promise<Response> {
  const hasBody = options.body !== undefined || options.rawBody !== undefined;
  const method = (options.method ?? (hasBody ? 'POST' : 'GET')).toUpperCase();
  const isMutating = !['GET', 'HEAD', 'OPTIONS'].includes(method);
  const headerEntries: Record<string, string> = {};
  if (hasBody) headerEntries['Content-Type'] = 'application/json';
  if (options.user && (isMutating || hasBody))
    headerEntries['X-CSRF-Token'] = options.user.csrf_token;
  if (options.headers) Object.assign(headerEntries, options.headers);
  const headers = Object.keys(headerEntries).length > 0 ? headerEntries : undefined;
  const fetcher = options.fetcher ?? ((...args: Parameters<typeof fetch>) => fetch(...args));
  const response = await fetcher(path, {
    method,
    credentials: 'same-origin',
    cache: 'no-store',
    headers,
    body: hasBody ? (options.rawBody ?? JSON.stringify(options.body)) : undefined,
    signal: combineAbortSignals(options.signal, options.timeoutMs),
  });
  return assertAPIResponse(response, options.passthroughError, options.errorMessages);
}
