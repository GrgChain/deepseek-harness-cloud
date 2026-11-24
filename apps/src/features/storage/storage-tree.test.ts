import { describe, expect, it } from 'vitest';
import {
  findStorageTreeNode,
  replaceStorageTreeChildren,
  storageTreeKey,
  storageTreeLocation,
  type StorageTreeNode,
} from './storage-tree';

describe('storage-tree helpers', () => {
  it('builds and parses storage tree keys safely', () => {
    expect(storageTreeKey('workspace', 'src/index.ts')).toBe('workspace:src/index.ts');
    expect(storageTreeLocation('workspace:src/a:b.ts')).toEqual({
      root: 'workspace',
      path: 'src/a:b.ts',
    });
    expect(storageTreeLocation('dsh:')).toEqual({ root: 'dsh', path: '' });
    expect(storageTreeLocation('invalid:path')).toBeNull();
    expect(storageTreeLocation('nocolon')).toBeNull();
  });

  it('finds and replaces nested tree children immutably', () => {
    const tree: StorageTreeNode[] = [
      {
        key: 'workspace:',
        title: 'workspace',
        children: [{ key: 'workspace:src', title: 'src' }],
      },
    ];
    expect(findStorageTreeNode(tree, 'workspace:src')?.title).toBe('src');
    expect(findStorageTreeNode(tree, 'workspace:missing')).toBeUndefined();

    const updated = replaceStorageTreeChildren(tree, 'workspace:src', [
      { key: 'workspace:src/app.tsx', title: 'app.tsx', isLeaf: true },
    ]);
    expect(findStorageTreeNode(updated, 'workspace:src')?.children).toHaveLength(1);
    expect(findStorageTreeNode(updated, 'workspace:src')?.isLeaf).toBe(false);

    const emptied = replaceStorageTreeChildren(updated, 'workspace:src', []);
    expect(findStorageTreeNode(emptied, 'workspace:src')?.isLeaf).toBe(true);
  });
});
