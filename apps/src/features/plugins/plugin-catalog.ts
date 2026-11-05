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
  version: '0.2.0',
  filename: 'harness-cloud-dsh-mem0-memory-0.2.0.tgz',
  path: '/plugin-packages/harness-cloud-dsh-mem0-memory-0.2.0.tgz',
};

export const graphitiPluginPackage = {
  name: '@harness-cloud/dsh-graphiti-memory',
  version: '0.2.0',
  filename: 'harness-cloud-dsh-graphiti-memory-0.2.0.tgz',
  path: '/plugin-packages/harness-cloud-dsh-graphiti-memory-0.2.0.tgz',
};

const localMemoryPackages = [mem0PluginPackage, graphitiPluginPackage];
export function isLocalMemoryPackage(packageName: string) {
  return localMemoryPackages.some((item) => item.name === packageName);
}

export function bundledMemoryPlugin(
  plugin: Pick<CatalogPlugin, 'packageName' | 'version' | 'additionMethod'>,
) {
  return localMemoryPackages.find(
    (item) =>
      plugin.packageName === item.name &&
      plugin.version === item.version &&
      plugin.additionMethod !== 'offline',
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
