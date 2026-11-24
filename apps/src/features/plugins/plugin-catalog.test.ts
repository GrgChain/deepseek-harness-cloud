import { describe, expect, it } from 'vitest';
import {
  bundledMemoryPlugin,
  filterPlugins,
  graphitiPluginPackage,
  isLocalMemoryPackage,
  mem0PluginPackage,
  type CatalogPlugin,
} from './plugin-catalog';

const sampleCatalog: CatalogPlugin[] = [
  {
    id: '1',
    name: 'Mem0 记忆',
    packageName: mem0PluginPackage.name,
    version: mem0PluginPackage.version,
    source: 'community',
    author: 'Harness Cloud',
    category: '会话与记忆',
    summary: 'Mem0 会话同步',
    description: '长期记忆提取',
    keywords: ['mem0', '记忆'],
    repository: '',
    documentation: '',
    compatibility: 'Node 24',
    checkedAt: '2026-11-01',
    sourceNote: '',
    revision: 1,
  },
  {
    id: '2',
    name: '代码检查助手',
    packageName: '@example/linter',
    version: '1.0.0',
    source: 'community',
    author: 'Example',
    category: '开发工具',
    summary: '静态分析',
    description: 'TypeScript 检查',
    keywords: ['lint', 'ts'],
    repository: '',
    documentation: '',
    compatibility: 'Node 24',
    checkedAt: '2026-11-01',
    sourceNote: '',
    revision: 1,
  },
];

describe('plugin-catalog helpers', () => {
  it('identifies bundled local memory packages', () => {
    expect(isLocalMemoryPackage(mem0PluginPackage.name)).toBe(true);
    expect(isLocalMemoryPackage(graphitiPluginPackage.name)).toBe(true);
    expect(isLocalMemoryPackage('@example/linter')).toBe(false);
    expect(bundledMemoryPlugin(sampleCatalog[0])?.filename).toBe(mem0PluginPackage.filename);
    expect(bundledMemoryPlugin({ ...sampleCatalog[0], additionMethod: 'offline' })).toBeUndefined();
  });

  it('filters plugins by category and multi-word case-insensitive query', () => {
    expect(filterPlugins('', '会话与记忆', sampleCatalog)).toHaveLength(1);
    expect(filterPlugins('typescript lint', 'all', sampleCatalog)).toEqual([sampleCatalog[1]]);
    expect(filterPlugins('mem0 开发工具', 'all', sampleCatalog)).toHaveLength(0);
  });
});
