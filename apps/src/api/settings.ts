import { APIError } from './error';
import type { CurrentUser } from './identity';

export interface Health { status: 'UNKNOWN' | 'CHECKING' | 'HEALTHY' | 'UNHEALTHY'; checked_at: string | null; expires_at: string | null; reason_code: string }
export interface ModelManifest { id: string; response_models: string[]; revision: string; digest: string; context_window: number; max_output_tokens: number; streaming: boolean; tool_use: boolean }
export interface ModelDirectory { version: number; items: ModelManifest[]; health: Health }
export interface ApprovedConnection { provider: 'platform-models'; protocol: 'anthropic-messages'; endpoint: string; models: ModelManifest[] }
export interface ModelConfiguration { id: string; version: number; provider: 'deepseek-official' | 'platform-models'; endpoint: string; protocol: 'deepseek-messages' | 'anthropic-messages'; model: string; status: 'UNVALIDATED' | 'VALID' | 'INVALID' | 'REVOKED'; credential_configured: boolean; validation: Health; manifest?: ModelManifest }
export interface ModelInput { provider: 'deepseek-official' | 'platform-models'; endpoint: string; protocol: 'deepseek-messages' | 'anthropic-messages'; model: string; api_key?: string; expected_version: number }
export interface ResourceSpec { id: string; cpu_millicores: number; memory_bytes: number; storage_bytes: number }
export interface ApprovedTemplate { id: string; template_version: string; name: string; backend_type: 'docker'; image_digest: string; spec: ResourceSpec }
export interface Defaults { version: number; template_id: string; template_version: string; spec_id: string; model_configuration_id: string; model_configuration_version: number; spec: ResourceSpec; retention_days: number; max_user_instances: number }
export interface RuntimeDefaultsInput { version:number; template_id:string; template_version:string; spec_id:string; retention_days:number; max_user_instances:number }
export interface OrganizationQuotaLimits { max_instances:number; cpu_millicores:number; memory_bytes:number; storage_bytes:number }
export interface OrganizationQuota { version:number; limits:OrganizationQuotaLimits; usage:OrganizationQuotaLimits; default_user_instances:number }
export interface ResourceQuota { available: OrganizationQuotaLimits; required: OrganizationQuotaLimits; concurrent_creates_available: number }
export interface Preflight { quota?: ResourceQuota; can_create: boolean; checks: { name: string; health: Health }[]; approved_models?: string[]; approved_endpoints?: string[]; approved_connections?: ApprovedConnection[] }
export interface PlatformChecks { ready: boolean; checks: { name: string; health: Health }[] }
export class SettingsClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  private async request<T>(path: string, user: CurrentUser, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
    const timeout = AbortSignal.timeout(path.endsWith(':validate') ? 36_000 : path.endsWith(':refresh') || method === 'PUT' ? 15_000 : 10_000);
    const response = await this.fetcher(path, { method, credentials: 'same-origin', cache: 'no-store', signal: signal ? AbortSignal.any([signal, timeout]) : timeout, headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json', 'X-CSRF-Token': user.csrf_token }, body: body === undefined ? undefined : JSON.stringify(body) });
    if (!response.ok) { const error: {code?: unknown} = await response.json().catch(() => ({})); throw new APIError(response.status, typeof error.code === 'string' ? error.code : 'DEPENDENCY_UNAVAILABLE'); }
    return response.json() as Promise<T>;
  }
  platformChecks(user: CurrentUser, signal?: AbortSignal) { return this.request<PlatformChecks>('/v1/platform-checks', user, 'GET', undefined, signal); }
  preflight(user: CurrentUser, signal?: AbortSignal, model?: {id: string; version: number}, specID?: string) {
    const query = new URLSearchParams();
    if (model) { query.set('model_configuration_id', model.id); query.set('model_configuration_version', String(model.version)); }
    if (specID) query.set('spec_id', specID);
    return this.request<Preflight>('/v1/preflight' + (query.size ? '?' + query : ''), user, 'GET', undefined, signal);
  }
  templates(user: CurrentUser, signal?: AbortSignal) { return this.request<{ items: ApprovedTemplate[] }>('/v1/templates', user, 'GET', undefined, signal); }
  async model(user: CurrentUser, signal?: AbortSignal) { try { return await this.request<ModelConfiguration>('/v1/model-configuration', user, 'GET', undefined, signal); } catch (err) { if (err instanceof APIError && err.status === 404 && err.code === 'NOT_FOUND') return null; throw err; } }
  async defaults(user: CurrentUser, signal?: AbortSignal) { try { return await this.request<Defaults>('/v1/defaults', user, 'GET', undefined, signal); } catch (err) { if (err instanceof APIError && err.status === 404 && err.code === 'DEFAULTS_NOT_CONFIGURED') return null; throw err; } }
  organizationQuota(user: CurrentUser, signal?: AbortSignal) { return this.request<OrganizationQuota>('/v1/organization-quota', user, 'GET', undefined, signal); }
  saveOrganizationQuota(user: CurrentUser, quota: OrganizationQuotaLimits, expectedVersion: number) { return this.request<OrganizationQuota>('/v1/organization-quota', user, 'PUT', { limits: quota, expected_version: expectedVersion }); }
  saveModel(user: CurrentUser, input: ModelInput) { return this.request<ModelConfiguration>('/v1/model-configuration', user, 'PUT', input); }
  directory(user: CurrentUser, version: number, signal?: AbortSignal) { return this.request<ModelDirectory>('/v1/model-directory?version=' + version, user, 'GET', undefined, signal); }
  refreshDirectory(user: CurrentUser, version: number) { return this.request<ModelDirectory>('/v1/model-directory:refresh', user, 'POST', { version }); }
  validate(user: CurrentUser, version: number) { return this.request<ModelConfiguration>('/v1/model-configuration:validate', user, 'POST', { version }); }
  revoke(user: CurrentUser, version: number) { return this.request<ModelConfiguration>('/v1/model-configuration:revoke', user, 'POST', { version }); }
  saveRuntimeDefaults(user:CurrentUser,input:RuntimeDefaultsInput) { return this.request<Defaults>('/v1/defaults',user,'PUT',input); }
  saveDefaults(user: CurrentUser, template: ApprovedTemplate, model: ModelConfiguration, version: number) { return this.request<Defaults>('/v1/defaults', user, 'PUT', { version, template_id: template.id, template_version: template.template_version, spec_id: template.spec.id, model_configuration_id: model.id, model_configuration_version: model.version }); }
}
