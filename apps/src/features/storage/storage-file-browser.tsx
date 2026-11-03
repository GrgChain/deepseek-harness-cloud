import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Space, Spin, Tooltip, Tree } from 'antd';
import {
  ChevronLeft,
  ChevronRight,
  Download,
  Eye,
  FileText,
  Folder,
  RefreshCw,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { APIError } from '../../api/error';
import type { CurrentUser } from '../../api/identity';
import { type StorageAsset, type StorageDirectoryEntry } from '../../api/storage';
import { expireSession } from '../../shared/hooks/use-expired-session';
import { isCancelledRead, readDirectoryForTree } from './directory-read';
import {
  downloadFile,
  filePreviewKind,
  filePreviewLimit,
  FilePreviewModal,
  type PreviewFile,
} from './file-preview';
import { clearResumedPreview, readResumedPreview } from './preview-recovery';

import { client, time } from './storage-shared';

import {
  findStorageTreeNode,
  replaceStorageTreeChildren,
  StorageMorePage,
  StorageRoot,
  storageTreeKey,
  storageTreeLocation,
  StorageTreeNode,
} from './storage-tree';

export function FileBrowser({ user, asset }: { user: CurrentUser; asset: StorageAsset }) {
  const [resumed] = useState(() => readResumedPreview(asset.id));
  const [root, setRoot] = useState<StorageRoot>(resumed?.root ?? 'workspace');
  const [path, setPath] = useState(resumed?.path.split('/').slice(0, -1).join('/') ?? '');
  const [pages, setPages] = useState<string[]>(['']);
  const [page, setPage] = useState(0);
  const [preview, setPreview] = useState<PreviewFile | null>(resumed);
  const [busyFile, setBusyFile] = useState('');
  const [error, setError] = useState('');
  useEffect(() => clearResumedPreview(), []);
  const [treeData, setTreeData] = useState<StorageTreeNode[]>([
    {
      key: 'data:',
      title: (
        <span className="storage-tree-title" title="/data" aria-label="/data">
          /data
        </span>
      ),
      children: [
        {
          key: 'workspace:',
          title: (
            <span
              className="storage-tree-title"
              title="/data/workspace"
              aria-label="/data/workspace"
            >
              workspace
            </span>
          ),
          isLeaf: false,
          icon: <Folder size={16} />,
        },
        {
          key: 'dsh:',
          title: (
            <span className="storage-tree-title" title="/data/dsh" aria-label="/data/dsh">
              dsh
            </span>
          ),
          isLeaf: false,
          icon: <Folder size={16} />,
        },
      ],
      isLeaf: false,
      icon: <Folder size={16} />,
      selectable: false,
    },
  ]);
  const treeDataRef = useRef(treeData);
  const [loadedKeys, setLoadedKeys] = useState<string[]>(['data:']);
  const loadedKeysRef = useRef(new Set<string>(['data:']));
  const [expandedKeys, setExpandedKeys] = useState<string[]>(['data:', 'workspace:']);
  const [selectedKeys, setSelectedKeys] = useState<string[]>(['workspace:']);
  const morePages = useRef(new Map<string, StorageMorePage>());
  const inFlightLoads = useRef(new Map<string, Promise<void>>());
  const [treeError, setTreeError] = useState<{ message: string; retry: () => void } | null>(null);
  const cache = useQueryClient();
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const activeFileRequest = useRef<AbortController | null>(null);
  useEffect(() => () => activeFileRequest.current?.abort(), []);
  const browsable =
    ['READY', 'RETAINED'].includes(asset.status) &&
    ['RUNNING', 'STOPPED', 'DELETED'].includes(asset.workspace_state);
  const after = pages[page] ?? '';
  const directory = useQuery({
    queryKey: ['storage', user.id, 'files', asset.id, root, path, after],
    queryFn: ({ signal }) => client.directory(asset.id, root, path, after, signal),
    retry: false,
    enabled: browsable,
  });
  function setTree(next: StorageTreeNode[]) {
    treeDataRef.current = next;
    setTreeData(next);
  }
  function markLoaded(keys: string[]) {
    loadedKeysRef.current = new Set(keys);
    setLoadedKeys(keys);
  }
  function directoryQuery(rootValue: StorageRoot, pathValue: string, afterValue: string) {
    return cache.fetchQuery({
      queryKey: ['storage', user.id, 'files', asset.id, rootValue, pathValue, afterValue],
      queryFn: ({ signal }) => client.directory(asset.id, rootValue, pathValue, afterValue, signal),
      retry: false,
      staleTime: 15_000,
    });
  }
  function pageNodes(
    result: Awaited<ReturnType<typeof client.directory>>,
    rootValue: StorageRoot,
    pathValue: string,
    existing: StorageTreeNode[] = [],
  ): StorageTreeNode[] {
    const existingByKey = new Map(
      existing.filter((node) => !node.key.startsWith('more:')).map((node) => [node.key, node]),
    );
    const nodes: StorageTreeNode[] = result.entries
      .filter((entry) => entry.type === 'DIRECTORY')
      .map((entry) => {
        const childPath = pathValue ? `${pathValue}/${entry.name}` : entry.name;
        const key = storageTreeKey(rootValue, childPath);
        const fullPath = `${rootValue === 'workspace' ? '/data/workspace' : '/data/dsh'}/${childPath}`;
        return (
          existingByKey.get(key) ?? {
            key,
            title: (
              <span className="storage-tree-title" title={fullPath} aria-label={fullPath}>
                {entry.name}
              </span>
            ),
            isLeaf: false,
            icon: <Folder size={16} />,
          }
        );
      });
    if (result.next_after) {
      const key = `more:${encodeURIComponent(JSON.stringify([rootValue, pathValue, result.next_after]))}`;
      morePages.current.set(key, {
        root: rootValue,
        path: pathValue,
        after: result.next_after,
        parentKey: storageTreeKey(rootValue, pathValue),
      });
      nodes.push({ key, title: '加载更多…', isLeaf: true });
    }
    return nodes;
  }
  async function loadNode(key: string, force = false): Promise<void> {
    if (!force && loadedKeysRef.current.has(key)) return;
    const pending = inFlightLoads.current.get(key);
    if (pending) return pending;
    const location = storageTreeLocation(key);
    if (!location) return;
    const request = (async () => {
      try {
        const result = await readDirectoryForTree(
          () => directoryQuery(location.root, location.path, ''),
          () => mounted.current,
        );
        if (!mounted.current) return;
        setTree(
          replaceStorageTreeChildren(
            treeDataRef.current,
            key,
            pageNodes(result, location.root, location.path),
          ),
        );
        markLoaded([...loadedKeysRef.current, key]);
        setTreeError(null);
      } catch (cause) {
        if (mounted.current && !isCancelledRead(cause)) {
          markLoaded([...loadedKeysRef.current, key]);
          const message = cause instanceof Error ? cause.message : '目录树加载失败';
          setTreeError({
            message,
            retry: () => {
              void loadNode(key, true).catch(() => {});
            },
          });
        }
        throw cause;
      }
    })();
    inFlightLoads.current.set(key, request);
    try {
      await request;
    } finally {
      inFlightLoads.current.delete(key);
    }
  }
  async function loadMore(key: string, more: StorageMorePage) {
    try {
      const result = await directoryQuery(more.root, more.path, more.after);
      const existing = findStorageTreeNode(treeDataRef.current, more.parentKey)?.children ?? [];
      morePages.current.delete(key);
      setTree(
        replaceStorageTreeChildren(treeDataRef.current, more.parentKey, [
          ...existing.filter((node) => !node.key.startsWith('more:')),
          ...pageNodes(result, more.root, more.path, existing),
        ]),
      );
      setTreeError(null);
    } catch (cause) {
      if (mounted.current && !isCancelledRead(cause)) {
        const message = cause instanceof Error ? cause.message : '目录树加载失败';
        setTreeError({
          message,
          retry: () => {
            void loadMore(key, more).catch(() => {});
          },
        });
      }
      throw cause;
    }
  }
  async function revealPath(nextRoot: StorageRoot, nextPath: string) {
    const segments = nextPath ? nextPath.split('/') : [];
    let parentPath = '';
    const expanded = [storageTreeKey(nextRoot, '')];
    try {
      await loadNode(expanded[0]);
    } catch {}
    for (const segment of segments) {
      const parentKey = storageTreeKey(nextRoot, parentPath);
      const targetKey = storageTreeKey(nextRoot, parentPath ? `${parentPath}/${segment}` : segment);
      let child = findStorageTreeNode(treeDataRef.current, parentKey)?.children?.find(
        (node) => node.key === targetKey,
      );
      while (!child) {
        const more = [
          ...(findStorageTreeNode(treeDataRef.current, parentKey)?.children ?? []),
        ].find((node) => node.key.startsWith('more:'));
        if (!more) break;
        const next = morePages.current.get(more.key);
        if (!next) break;
        await loadMore(more.key, next);
        child = findStorageTreeNode(treeDataRef.current, parentKey)?.children?.find(
          (node) => node.key === targetKey,
        );
      }
      if (!child) break;
      parentPath = parentPath ? `${parentPath}/${segment}` : segment;
      expanded.push(storageTreeKey(nextRoot, parentPath));
      try {
        await loadNode(storageTreeKey(nextRoot, parentPath));
      } catch {
        break;
      }
    }
    setExpandedKeys((current) => [...new Set([...current, ...expanded])]);
    setSelectedKeys([storageTreeKey(nextRoot, nextPath)]);
  }
  function navigate(nextRoot: StorageRoot, nextPath: string) {
    setRoot(nextRoot);
    setPath(nextPath);
    setPages(['']);
    setPage(0);
    setError('');
    setSelectedKeys([storageTreeKey(nextRoot, nextPath)]);
    void revealPath(nextRoot, nextPath).catch(() => {});
  }
  function openEntry(entry: StorageDirectoryEntry) {
    if (entry.type === 'DIRECTORY') navigate(root, path ? `${path}/${entry.name}` : entry.name);
  }
  async function useFile(entry: StorageDirectoryEntry, mode: 'PREVIEW' | 'DOWNLOAD') {
    const filePath = path ? `${path}/${entry.name}` : entry.name;
    if (mode === 'PREVIEW') {
      setPreview({ name: entry.name, root, path: filePath });
      return;
    }
    const request = new AbortController();
    activeFileRequest.current = request;
    setBusyFile(`${filePath}:${mode}`);
    setError('');
    try {
      const result = await client.file(asset.id, root, filePath, 'DOWNLOAD', request.signal);
      downloadFile(result.blob, result.name);
    } catch (e) {
      if (!request.signal.aborted) {
        if (e instanceof APIError && e.status === 401) expireSession(cache);
        setError(
          e instanceof APIError
            ? e.message
            : e instanceof Error
              ? e.message
              : '文件读取失败，请重试',
        );
      }
    } finally {
      if (activeFileRequest.current === request) {
        activeFileRequest.current = null;
        setBusyFile('');
      }
    }
  }
  useEffect(() => {
    void revealPath(root, path).catch(() => {});
  }, [user.id, asset.id]);
  const crumbs = path ? path.split('/') : [];
  return (
    <section className="storage-file-browser" aria-label="数据目录">
      {asset.workspace_state === 'RUNNING' && (
        <p className="storage-file-notice">运行中 · 只读浏览，正在修改的文件可能暂时无法读取。</p>
      )}
      {!browsable ? (
        <Alert type="warning" showIcon message="此数据盘当前不可浏览" />
      ) : (
        <div className="storage-file-layout">
          <aside className="storage-file-tree" aria-label="数据目录树">
            <h3>目录</h3>
            <Tree
              blockNode
              selectable
              showIcon
              treeData={treeData}
              loadData={(node) => loadNode(String(node.key))}
              loadedKeys={loadedKeys}
              onLoad={(keys) => markLoaded(keys.map(String))}
              expandedKeys={expandedKeys}
              selectedKeys={selectedKeys}
              onExpand={(keys) => setExpandedKeys(keys.map(String))}
              onSelect={(keys) => {
                const key = String(keys[0] ?? '');
                const more = morePages.current.get(key);
                if (more) {
                  void loadMore(key, more).catch(() => {});
                  return;
                }
                const location = storageTreeLocation(key);
                if (location) navigate(location.root, location.path);
              }}
            />
            {treeError && (
              <Alert
                type="error"
                showIcon
                message={treeError.message}
                action={<Button onClick={treeError.retry}>重试</Button>}
              />
            )}
          </aside>
          <div className="storage-file-main">
            <div className="storage-file-main-toolbar">
              <nav className="storage-file-crumbs" aria-label="当前目录">
                <span className="storage-file-crumb-root">/data</span>
                <span aria-hidden="true">/</span>
                <Button
                  type="link"
                  aria-current={!path ? 'page' : undefined}
                  onClick={() => navigate(root, '')}
                >
                  {root}
                </Button>
                {crumbs.map((part, index) => (
                  <span key={`${index}-${part}`}>
                    <span aria-hidden="true">/</span>
                    <Button
                      type="link"
                      onClick={() => navigate(root, crumbs.slice(0, index + 1).join('/'))}
                    >
                      {part}
                    </Button>
                  </span>
                ))}
              </nav>
              <Tooltip title="刷新当前目录">
                <Button
                  className="storage-file-refresh"
                  aria-label="刷新目录"
                  icon={<RefreshCw size={16} />}
                  loading={directory.isFetching}
                  onClick={() => void directory.refetch()}
                />
              </Tooltip>
            </div>
            {error && (
              <Alert
                type="error"
                showIcon
                message={error}
                action={<Button onClick={() => void directory.refetch()}>重试</Button>}
              />
            )}
            {directory.data?.entries.length ? (
              <div className="storage-file-columns" aria-hidden="true">
                <span>名称</span>
                <span>详情</span>
                <span>操作</span>
              </div>
            ) : null}
            {directory.isPending ? (
              <Spin aria-label="读取数据目录" />
            ) : directory.isError ? (
              <Alert
                type="error"
                showIcon
                message={
                  directory.error instanceof Error ? directory.error.message : '数据目录读取失败'
                }
                action={<Button onClick={() => void directory.refetch()}>重试</Button>}
              />
            ) : !directory.data?.entries.length ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此目录为空" />
            ) : (
              <div className="storage-file-list" role="list">
                {directory.data.entries.map((entry) => {
                  const filePath = path ? `${path}/${entry.name}` : entry.name;
                  return (
                    <div className="storage-file-row" role="listitem" key={entry.name}>
                      {entry.type === 'DIRECTORY' ? (
                        <button
                          type="button"
                          className="storage-file-name storage-directory-link"
                          aria-label={`进入文件夹 ${entry.name}`}
                          onClick={() => openEntry(entry)}
                        >
                          <Folder size={17} />
                          <span title={entry.name}>{entry.name}</span>
                        </button>
                      ) : (
                        <div className="storage-file-name">
                          <FileText size={17} />
                          <span title={entry.name}>{entry.name}</span>
                        </div>
                      )}
                      <span className="storage-file-meta">
                        {entry.type === 'DIRECTORY'
                          ? '文件夹'
                          : entry.type === 'FILE'
                            ? `${fileSize(entry.size_bytes)} · ${time(entry.modified_at)}`
                            : entry.type === 'LINK'
                              ? '符号链接'
                              : '不可预览的项目'}
                      </span>
                      {entry.type === 'FILE' ? (
                        <Space size={4}>
                          {(() => {
                            const kind = filePreviewKind(entry.name);
                            const previewLimit = filePreviewLimit(entry.name);
                            const legacy = /\.(doc|xls|ppt)$/i.test(entry.name);
                            const label = { office: 'Office', pdf: 'PDF', text: '文本' }[kind];
                            return (
                              <Tooltip
                                open={preview ? false : undefined}
                                title={
                                  legacy
                                    ? '旧版 Office 格式请下载查看，或转换为 docx / xlsx / pptx'
                                    : entry.size_bytes > previewLimit
                                      ? `${label}预览上限 ${fileSize(previewLimit)}`
                                      : `预览 ${label}`
                                }
                              >
                                <Button
                                  aria-label={`预览 ${entry.name}`}
                                  icon={<Eye size={16} />}
                                  disabled={
                                    legacy || entry.size_bytes > previewLimit || Boolean(busyFile)
                                  }
                                  onClick={() => void useFile(entry, 'PREVIEW')}
                                />
                              </Tooltip>
                            );
                          })()}
                          <Tooltip
                            title={
                              entry.size_bytes > 64 * 1024 * 1024 ? '下载上限 64 MiB' : '下载文件'
                            }
                          >
                            <Button
                              aria-label={`下载 ${entry.name}`}
                              icon={<Download size={16} />}
                              disabled={entry.size_bytes > 64 * 1024 * 1024 || Boolean(busyFile)}
                              loading={busyFile === `${filePath}:DOWNLOAD`}
                              onClick={() => void useFile(entry, 'DOWNLOAD')}
                            />
                          </Tooltip>
                        </Space>
                      ) : (
                        <span />
                      )}
                    </div>
                  );
                })}
              </div>
            )}
            <div className="storage-file-pagination">
              <Button
                aria-label="上一页"
                icon={<ChevronLeft size={16} />}
                disabled={page === 0 || directory.isFetching}
                onClick={() => setPage((value) => Math.max(0, value - 1))}
              />
              <span>
                第 {page + 1} 页 · {directory.data?.entries.length ?? 0} 项
              </span>
              <Button
                aria-label="下一页"
                icon={<ChevronRight size={16} />}
                disabled={!directory.data?.next_after || directory.isFetching}
                onClick={() => {
                  if (directory.data?.next_after) {
                    setPages((current) => [
                      ...current.slice(0, page + 1),
                      directory.data!.next_after!,
                    ]);
                    setPage((value) => value + 1);
                  }
                }}
              />
            </div>
          </div>
        </div>
      )}
      {preview && (
        <FilePreviewModal
          key={`${preview.root}:${preview.path}`}
          file={preview}
          assetId={asset.id}
          client={client}
          onClose={() => setPreview(null)}
        />
      )}
    </section>
  );
}

export function fileSize(value: number) {
  if (value < 1024) return `${value} B`;
  const units = ['KiB', 'MiB', 'GiB'];
  let size = value / 1024,
    index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index++;
  }
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(size)} ${units[index]}`;
}
