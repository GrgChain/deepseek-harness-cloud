export const pluginCategories = ['会话与记忆', '开发工具', '集成扩展'] as const;
export type PluginCategory = (typeof pluginCategories)[number];
export interface CatalogPlugin {
  id: string;
  name: string;
  packageName: string;
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
  checkedAt: string;
  sourceNote: string;
  revision: number;
  custom?: boolean;
  additionMethod?: 'online' | 'offline';
  filename?: string;
  sha256?: string;
}

export const mem0PluginPackage = {
  name: '@harness-cloud/dsh-mem0-memory',
  version: '0.1.0',
  filename: 'harness-cloud-dsh-mem0-memory-0.1.0.tgz',
  path: '/plugin-packages/harness-cloud-dsh-mem0-memory-0.1.0.tgz',
};

export function isBundledMem0Plugin(
  plugin: Pick<CatalogPlugin, 'packageName' | 'version' | 'additionMethod'>,
) {
  return (
    plugin.packageName === mem0PluginPackage.name &&
    plugin.version === mem0PluginPackage.version &&
    plugin.additionMethod !== 'offline'
  );
}

export function filterPlugins(search: string, category: string, catalog: CatalogPlugin[]) {
  const words = search.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  return catalog.filter(
    (plugin) =>
      (category === 'all' || plugin.category === category) &&
      words.every((word) =>
        [
          plugin.name,
          plugin.packageName,
          plugin.summary,
          plugin.description,
          plugin.author,
          plugin.category,
          ...plugin.keywords,
        ]
          .join(' ')
          .toLocaleLowerCase()
          .includes(word),
      ),
  );
}
