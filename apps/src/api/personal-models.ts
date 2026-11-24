import type { CurrentUser } from './identity';
import { apiFetch } from './http';
import { type Health } from './settings';

export interface PersonalModel {
  id: string;
  name: string;
  version: number;
  provider: string;
  model: string;
  catalog_entry_id: string;
  enabled: boolean;
  credential_configured: boolean;
  sync_status:
    'PENDING_SYNC' | 'SYNCING' | 'UNCERTAIN' | 'SYNCED' | 'FAILED' | 'REVOKING' | 'REVOKED';
  operation_id: string;
  operation_status: string;
  reason_code: string;
  validation?: Health;
  gateway_check: Health;
}
export interface ModelSelection {
  provider: string;
  api_base: string;
  model: string;
  response_models?: string[];
  context_window?: number;
  max_output_tokens?: number;
}
export interface CatalogEntry {
  id: string;
  name: string;
  provider: string;
  model: string;
  api_base: string;
  version: number;
  response_models?: string[];
  context_window?: number;
  max_output_tokens?: number;
}
export interface PersonalDefault {
  version: number;
  connection_id: string | null;
  connection_version: number | null;
}
export function isPersonalModelUsable(model: PersonalModel | undefined): boolean {
  return (
    !!model?.enabled &&
    model.credential_configured &&
    model.sync_status === 'SYNCED' &&
    model.gateway_check?.status === 'HEALTHY' &&
    model.validation?.status === 'HEALTHY'
  );
}
export class PersonalModelsClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  protected async request<T>(
    path: string,
    user: CurrentUser,
    body?: unknown,
    key?: string,
    signal?: AbortSignal,
    method?: string,
  ): Promise<T> {
    const timeoutMs = path.endsWith(':validate')
      ? 36_000
      : path.endsWith(':sync')
        ? 30_000
        : 10_000;
    const response = await apiFetch(path, {
      method,
      user,
      body,
      headers: key ? { 'Idempotency-Key': key } : undefined,
      timeoutMs,
      signal,
      fetcher: this.fetcher,
    });
    return response.json() as Promise<T>;
  }
  list(user: CurrentUser, signal?: AbortSignal) {
    return this.request<{ items: PersonalModel[] }>(
      '/v1/my-models',
      user,
      undefined,
      undefined,
      signal,
    );
  }
  async providers(user: CurrentUser, signal?: AbortSignal) {
    const result = await this.request<{
      items: { id: string; name: string; api_bases: string[]; version: number }[];
    }>('/v1/model-providers', user, undefined, undefined, signal);
    const displayNames: Record<string, string> = { moonshot: 'Moonshot AI', zai: 'Z.AI' };
    return {
      ...result,
      items: result.items.map((provider) =>
        displayNames[provider.id] ? { ...provider, name: displayNames[provider.id] } : provider,
      ),
    };
  }
  syncModels(user: CurrentUser, provider: string, input: { api_base: string; api_key: string }) {
    return this.request<{ items: { id: string; name: string }[] }>(
      `/v1/model-providers/${encodeURIComponent(provider)}:sync`,
      user,
      input,
    );
  }
  catalog(user: CurrentUser, signal?: AbortSignal) {
    return this.request<{ items: CatalogEntry[] }>(
      '/v1/model-catalog',
      user,
      undefined,
      undefined,
      signal,
    );
  }
  createMany(
    user: CurrentUser,
    items: {
      name: string;
      catalog_entry_id?: string;
      selection?: ModelSelection;
      api_key: string;
    }[],
    key: string,
  ) {
    return this.request<{ items: PersonalModel[] }>('/v1/my-models:batch', user, { items }, key);
  }
  create(
    user: CurrentUser,
    input: { name: string; catalog_entry_id?: string; selection?: ModelSelection; api_key: string },
    key: string,
  ) {
    return this.request<PersonalModel>('/v1/my-models', user, input, key);
  }
  update(
    user: CurrentUser,
    id: string,
    input: {
      name: string;
      catalog_entry_id?: string;
      selection?: ModelSelection;
      api_key?: string;
      expected_version: number;
    },
  ) {
    return this.request<PersonalModel>(
      '/v1/my-models/' + id,
      user,
      input,
      undefined,
      undefined,
      'PUT',
    );
  }
  action(user: CurrentUser, model: PersonalModel, action: 'validate' | 'revoke' | 'archive') {
    return this.request<PersonalModel>(`/v1/my-models/${model.id}:${action}`, user, {
      version: model.version,
    });
  }
  default(user: CurrentUser, signal?: AbortSignal) {
    return this.request<PersonalDefault | null>(
      '/v1/my-model-default',
      user,
      undefined,
      undefined,
      signal,
    );
  }
  setDefault(user: CurrentUser, model: PersonalModel, expected: number) {
    return this.request<PersonalDefault>('/v1/my-model-default', user, {
      connection_id: model.id,
      connection_version: model.version,
      expected_version: expected,
    });
  }
  clearDefault(user: CurrentUser, expected: number) {
    return this.request<PersonalDefault>('/v1/my-model-default:clear', user, {
      expected_version: expected,
    });
  }
}
