import { apiFetch } from './http';
import type { CurrentUser } from './identity';
import type { CatalogPlugin, PluginCategory } from '../features/plugins/plugin-catalog';

interface PluginResponse {
  id: string;
  name: string;
  package_name: string;
  version: string;
  source: 'community';
  author: string;
  category: PluginCategory;
  summary: string;
  description: string;
  keywords: string[];
  repository: string;
  documentation: string;
  compatibility: string;
  checked_at: string;
  custom: boolean;
  addition_method: 'online' | 'offline';
  filename: string;
  sha256: string;
  source_note: string;
  revision: number;
}
const plugin = (item: PluginResponse): CatalogPlugin => ({
  ...item,
  packageName: item.package_name,
  checkedAt: item.checked_at,
  custom: item.custom,
  additionMethod: item.addition_method,
  sourceNote: item.source_note,
});
export interface PluginArchive {
  filename: string;
  content_base64: string;
}
export type PluginMetadata = Pick<
  CatalogPlugin,
  | 'name'
  | 'summary'
  | 'description'
  | 'author'
  | 'version'
  | 'category'
  | 'keywords'
  | 'repository'
  | 'documentation'
  | 'compatibility'
  | 'checkedAt'
  | 'sourceNote'
>;

const pluginPassthrough = (code: string) => code.startsWith('PLUGIN_');

export class PluginsClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  private async request<T>(
    user: CurrentUser,
    path = '',
    body?: unknown,
    signal?: AbortSignal,
    method?: 'PUT',
  ): Promise<T> {
    const response = await apiFetch('/v1/plugins' + path, {
      method: method ?? (body === undefined ? 'GET' : 'POST'),
      user,
      body,
      timeoutMs: 22_000,
      signal,
      fetcher: this.fetcher,
      passthroughError: pluginPassthrough,
    });
    return response.json();
  }
  async list(user: CurrentUser, signal?: AbortSignal) {
    const result = await this.request<{ items: PluginResponse[] }>(user, '', undefined, signal);
    return { items: result.items.map(plugin) };
  }
  async preview(user: CurrentUser, packageName: string) {
    return plugin(
      await this.request<PluginResponse>(user, ':preview', { package_name: packageName }),
    );
  }
  async add(user: CurrentUser, item: CatalogPlugin, category: PluginCategory) {
    return plugin(
      await this.request<PluginResponse>(user, '', {
        package_name: item.packageName,
        version: item.version,
        category,
      }),
    );
  }
  async previewArchive(user: CurrentUser, archive: PluginArchive) {
    return plugin(await this.request<PluginResponse>(user, ':preview', archive));
  }
  async addArchive(user: CurrentUser, archive: PluginArchive, category: PluginCategory) {
    return plugin(await this.request<PluginResponse>(user, '', { ...archive, category }));
  }
  async update(user: CurrentUser, item: CatalogPlugin, metadata: PluginMetadata) {
    const { checkedAt, sourceNote, ...fields } = metadata;
    return plugin(
      await this.request<PluginResponse>(
        user,
        '/' + encodeURIComponent(item.id),
        {
          ...fields,
          checked_at: checkedAt,
          source_note: sourceNote,
          expected_revision: item.revision,
        },
        undefined,
        'PUT',
      ),
    );
  }
  async download(id: string) {
    const response = await apiFetch('/v1/plugins/' + encodeURIComponent(id) + '/archive', {
      timeoutMs: 22_000,
      fetcher: this.fetcher,
      passthroughError: pluginPassthrough,
    });
    return response.blob();
  }
}
