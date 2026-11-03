import { type ReactNode } from 'react';

export type StorageRoot = 'workspace' | 'dsh';

export type StorageTreeNode = {
  key: string;
  title: ReactNode;
  children?: StorageTreeNode[];
  isLeaf?: boolean;
  icon?: ReactNode;
  selectable?: boolean;
};

export type StorageTreeLocation = { root: StorageRoot; path: string };

export type StorageMorePage = { root: StorageRoot; path: string; after: string; parentKey: string };

export const storageTreeKey = (root: StorageRoot, path: string) => `${root}:${path}`;

export function storageTreeLocation(key: string): StorageTreeLocation | null {
  const index = key.indexOf(':');
  if (index < 0) return null;
  const root = key.slice(0, index);
  if (root !== 'workspace' && root !== 'dsh') return null;
  return { root, path: key.slice(index + 1) };
}

export function findStorageTreeNode(
  nodes: StorageTreeNode[],
  key: string,
): StorageTreeNode | undefined {
  for (const node of nodes) {
    if (node.key === key) return node;
    const found = node.children && findStorageTreeNode(node.children, key);
    if (found) return found;
  }
  return undefined;
}

export function replaceStorageTreeChildren(
  nodes: StorageTreeNode[],
  key: string,
  children: StorageTreeNode[],
): StorageTreeNode[] {
  return nodes.map((node) =>
    node.key === key
      ? { ...node, children, isLeaf: children.length === 0 }
      : node.children
        ? { ...node, children: replaceStorageTreeChildren(node.children, key, children) }
        : node,
  );
}
