import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Popover,
  Radio,
  Segmented,
  Select,
  Space,
  Spin,
  Table,
  Tag,
} from 'antd';
import {
  Brain,
  CircleHelp,
  Download,
  Ellipsis,
  FileText,
  Plus,
  RefreshCw,
  Search,
  Upload,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import {
  type ContextConnection,
  type ContextPreview,
  type ContextRecord,
  type MemoryArchive,
} from '../../api/context';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage as message } from '../../shared/utils/error-message';
import { ContextDeleteDialog } from './context-delete-dialog';
import { ContextRecordPreview } from './context-record-preview';
import { ContextSearchResults } from './context-search-results';
import { ContextServiceIcon } from './context-service-picker';
import { documentAccept, extractDocumentText } from './document-import';
import {
  ContentViewSwitcher,
  KnowledgeContent,
  MarkdownContent,
  type ContentView,
} from './knowledge-content';
import { WorkDataPicker } from './work-data-picker';

import {
  backendNames,
  client,
  date,
  ManagedRecord,
  memoryCategory,
  memorySource,
  SessionMemory,
  status,
} from './context-shared';

export const key = () => crypto.randomUUID();

export function ContextDetails({
  user,
  connection,
  onClose,
}: {
  user: CurrentUser;
  connection: ContextConnection;
  onClose: () => void;
}) {
  const [workFiles, setWorkFiles] = useState(false);
  const [operationPending, setOperationPending] = useState(false);
  const [savedNotice, setSavedNotice] = useState('');
  const previewOperationKey = useRef('');
  const deleteOperationKey = useRef('');
  const operationController = useRef<AbortController | null>(null);
  const retryOperation = useRef<(() => Promise<void>) | null>(null);
  useEffect(() => () => operationController.current?.abort(), []);
  useEffect(() => {
    if (!savedNotice) return;
    const timer = window.setTimeout(() => setSavedNotice(''), 3000);
    return () => window.clearTimeout(timer);
  }, [savedNotice]);
  const [rebuildState, setRebuildState] = useState<{
    record: ContextRecord;
    phase: 'indexing' | 'refreshing' | 'done';
  } | null>(null);
  useEffect(() => {
    if (rebuildState?.phase !== 'done') return;
    const timer = window.setTimeout(() => setRebuildState(null), 3000);
    return () => window.clearTimeout(timer);
  }, [rebuildState]);
  const memory = connection.kind === 'memory';
  const manage = hasPermission(user, connection.kind + ':manage');
  const cache = useQueryClient();
  const nativeMemory = memory && connection.backend === 'openviking';
  const [previewImport, setPreviewImport] = useState(false);
  const scope = 'personal';
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<ManagedRecord[] | null>(null);
  const [detail, setDetail] = useState<ManagedRecord | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState('');
  const [title, setTitle] = useState('');
  const [source, setSource] = useState('');
  const [target, setTarget] = useState<ContextRecord | null>(null);
  const [preview, setPreview] = useState<ContextPreview | null>(null);
  const [mode, setMode] = useState('skip');
  const [acceptLoss, setAcceptLoss] = useState(false);
  const [removing, setRemoving] = useState<ContextRecord | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchMode, setSearchMode] = useState<'semantic' | 'keyword' | 'hybrid'>('semantic');
  const [searchLimit, setSearchLimit] = useState(5);
  const [expandedResults, setExpandedResults] = useState<string[]>([]);
  const [searchError, setSearchError] = useState('');
  const [minSimilarity, setMinSimilarity] = useState<number | null>(null);
  const [searchSource, setSearchSource] = useState<'all' | 'automatic' | 'manual'>('all');
  const [similarityHelpOpen, setSimilarityHelpOpen] = useState(false);
  const [documentMode, setDocumentMode] = useState<'text' | 'file'>('text');
  const [documentName, setDocumentName] = useState('');
  const [editorView, setEditorView] = useState<ContentView>('source');
  const [documentSearch, setDocumentSearch] = useState('');
  const [documentQuery, setDocumentQuery] = useState('');
  const [memoryFilter, setMemoryFilter] = useState('全部');
  const documentPagination = usePagePagination(
    JSON.stringify([connection.id, scope, documentQuery, memoryFilter]),
  );
  const previewPagination = usePagePagination(preview?.token ?? '');
  const previewItems = previewImport
    ? preview?.items.slice(
        (previewPagination.page - 1) * previewPagination.pageSize,
        previewPagination.page * previewPagination.pageSize,
      )
    : preview?.items;
  const documentInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const timer = window.setTimeout(() => setDocumentQuery(documentSearch.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [documentSearch]);
  useEffect(() => {
    setHits(null);
    setDetail(null);
    setPreview(null);
  }, [connection.version, connection.enabled]);
  const ready = connection.enabled && connection.status === 'READY';
  const writeBlocked = !!busy || operationPending;
  const records = useQuery({
    queryKey: [
      'context-records',
      user.id,
      connection.id,
      connection.version,
      scope,
      ...(memory ? [] : [documentQuery, documentPagination.page, documentPagination.pageSize]),
    ],
    queryFn: () =>
      client.call<{ items: ContextRecord[]; total?: number }>(user, connection, 'list', {
        scope,
        ...(!memory
          ? {
              query: documentQuery,
              page: documentPagination.page,
              page_size: documentPagination.pageSize,
            }
          : {}),
      }),
    enabled: ready,
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(records.error);
  const nativeRecords = useQuery({
    queryKey: ['context-session-memories', user.id, connection.id, connection.version],
    queryFn: () =>
      client.call<{ items: SessionMemory[]; truncated: boolean }>(user, connection, 'session-list'),
    enabled: ready && nativeMemory,
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(nativeRecords.error);
  const listLoading = records.isFetching || (nativeMemory && nativeRecords.isFetching);
  const listFailed = records.isError && (!nativeMemory || nativeRecords.isError);
  async function run(action: string, fn: () => Promise<void>) {
    const inSearch = searchOpen && (action === 'search' || action === 'get');
    setBusy(action);
    if (action !== 'rebuild') setRebuildState(null);
    if (inSearch) setSearchError('');
    else setError('');
    try {
      await fn();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      if (cause instanceof APIError && cause.code === 'CONTEXT_PENDING') setOperationPending(true);
      if (inSearch) setSearchError(message(cause));
      else setError(action === 'rebuild' ? `重建失败：${message(cause)}` : message(cause));
      if (action === 'rebuild') void records.refetch();
    } finally {
      setBusy('');
    }
  }
  async function save(body: Record<string, unknown>) {
    const controller = new AbortController();
    operationController.current = controller;
    retryOperation.current = () => save(body);
    try {
      const result = await client.callAndConfirm<{
        applied?: number | string[];
        skipped?: number | string[];
      }>(user, connection, 'apply', body, () => setOperationPending(true), controller.signal);
      const applied = Array.isArray(result.applied) ? result.applied.length : result.applied;
      const skipped = Array.isArray(result.skipped) ? result.skipped.length : result.skipped;
      retryOperation.current = null;
      setOperationPending(false);
      setPreview(null);
      setSavedNotice(
        previewImport
          ? typeof applied === 'number' && typeof skipped === 'number'
            ? `导入完成：保存 ${applied} 条，保留 ${skipped} 条`
            : '记忆已导入'
          : memory
            ? '记忆已保存'
            : '文档已保存',
      );
      if (memory) {
        setDocumentSearch('');
        setDocumentQuery('');
        setMemoryFilter('全部');
        documentPagination.go(1);
      }
      await refresh();
    } catch (cause) {
      if (
        cause instanceof APIError &&
        cause.status >= 400 &&
        cause.status < 500 &&
        cause.code !== 'CONTEXT_PENDING'
      ) {
        retryOperation.current = null;
        setOperationPending(false);
        previewOperationKey.current = key();
      }
      throw cause;
    } finally {
      if (operationController.current === controller) operationController.current = null;
    }
  }
  async function callUntilComplete<T>(action: string, body: Record<string, unknown>): Promise<T> {
    const controller = new AbortController();
    operationController.current = controller;
    try {
      for (;;) {
        try {
          return await client.callAndConfirm<T>(
            user,
            connection,
            action,
            body,
            () => setOperationPending(true),
            controller.signal,
          );
        } catch (cause) {
          if (
            !controller.signal.aborted &&
            (!(cause instanceof APIError) || cause.code === 'CONTEXT_PENDING')
          )
            continue;
          throw cause;
        }
      }
    } finally {
      if (operationController.current === controller) operationController.current = null;
    }
  }
  async function remove(body: Record<string, unknown>) {
    try {
      const result = await callUntilComplete<{ deleted: boolean }>('delete', body);
      if (result.deleted !== true) throw new Error('删除尚未完成，请重试');
      setOperationPending(false);
      setRemoving(null);
      setConfirmed(false);
      setSavedNotice(memory ? '记忆已删除' : '文档已删除');
      await refresh();
    } catch (cause) {
      if (
        cause instanceof APIError &&
        cause.status >= 400 &&
        cause.status < 500 &&
        cause.code !== 'CONTEXT_PENDING'
      )
        setOperationPending(false);
      throw cause;
    }
  }
  function reconcilePending() {
    if (busy) return;
    void run('reconcile', async () => {
      if (retryOperation.current) {
        await retryOperation.current();
        return;
      }
      try {
        await client.request(user, connection.kind, `/${connection.id}/reconcile`, {});
      } catch (cause) {
        if (!(cause instanceof APIError && cause.code === 'NOT_FOUND')) throw cause;
      }
      setOperationPending(false);
      setRemoving(null);
      await refresh();
    });
  }
  async function refresh() {
    setHits(null);
    setDetail(null);
    await Promise.all([records.refetch(), ...(nativeMemory ? [nativeRecords.refetch()] : [])]);
    await cache.invalidateQueries({ queryKey: ['context', user.id, connection.kind] });
  }
  useEffect(() => {
    if (
      !ready ||
      !manage ||
      busy ||
      operationPending ||
      ![records.error, nativeRecords.error].some(
        (cause) => cause instanceof APIError && cause.code === 'CONTEXT_PENDING',
      )
    )
      return;
    setOperationPending(true);
    void run('reconcile', async () => {
      await callUntilComplete('list', { scope });
      setOperationPending(false);
      await refresh();
    });
  }, [ready, manage, busy, operationPending, records.error, nativeRecords.error]);
  function rebuild(row: ContextRecord) {
    if (busy || records.isFetching) return;
    setRebuildState({ record: row, phase: 'indexing' });
    void run('rebuild', async () => {
      try {
        await client.call(user, connection, 'rebuild', {
          scope,
          record_id: row.id,
          record_version: row.version,
          idempotency_key: key(),
          confirmed: true,
        });
        setRebuildState({ record: row, phase: 'refreshing' });
        await refresh();
        setRebuildState({ record: row, phase: 'done' });
      } catch (cause) {
        setRebuildState(null);
        throw cause;
      }
    });
  }
  async function open(row: ManagedRecord) {
    setDetail(null);
    await run('get', async () => {
      if (row.memory_source === 'automatic') {
        const result = await client.call<SessionMemory>(user, connection, 'session-get', {
          record_id: row.id,
        });
        setDetail({ ...row, ...result });
      } else
        setDetail({
          ...row,
          ...(await client.call<ContextRecord>(user, connection, 'get', {
            scope,
            record_id: row.id,
          })),
        });
    });
  }
  function newContent(row?: ContextRecord) {
    if (operationPending) return;
    setTarget(row ?? null);
    setContent(row?.content ?? '');
    setTitle(row?.title ?? '');
    setSource(row?.source ?? '');
    setDocumentMode('text');
    setDocumentName('');
    setEditorView('source');
    setEditing(true);
    setError('');
    setSavedNotice('');
  }
  function showPreview(value: ContextPreview, imported = false) {
    previewOperationKey.current = key();
    setPreview(value);
    setPreviewImport(imported);
    setAcceptLoss(false);
    setMode('skip');
  }
  function search() {
    if (ready && query.trim() && !busy)
      void run('search', async () => {
        setHits(null);
        setExpandedResults([]);
        const result = await client.call<{ items: ContextRecord[] }>(user, connection, 'search', {
          scope,
          query: query.trim(),
          limit: searchLimit,
          ...(!memory
            ? {
                search_mode: searchMode,
                ...(searchMode !== 'keyword' && minSimilarity !== null
                  ? { min_similarity: minSimilarity }
                  : {}),
              }
            : {
                ...(nativeMemory && searchSource !== 'all' ? { memory_source: searchSource } : {}),
                ...(minSimilarity !== null ? { min_similarity: minSimilarity } : {}),
              }),
        });
        setHits(result.items);
      });
  }

  const results = (
    <ContextSearchResults
      hits={hits}
      memory={memory}
      nativeMemory={nativeMemory}
      busy={busy}
      searchMode={searchMode}
      expandedResults={expandedResults}
      onExpandedChange={setExpandedResults}
      onClear={() => setHits(null)}
      onOpen={open}
    />
  );
  async function importFile(file: File) {
    const maxMiB = 16;
    if (file.size > maxMiB * 1024 * 1024) throw new Error(`文件不能超过 ${maxMiB} MiB`);
    let data: unknown;
    let text: string;
    try {
      text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer());
      data = JSON.parse(text);
    } catch {
      throw new Error('文件不是有效的 UTF-8 JSON 记忆包');
    }
    if (!data || typeof data !== 'object' || Array.isArray(data))
      throw new Error('请选择有效的记忆包');
    showPreview(await client.previewArchive(user, connection, text), true);
  }
  async function selectDocument(file: File) {
    const text = await extractDocumentText(user, file.name, file);
    setDocumentName(file.name);
    setTitle(file.name.slice(0, 200));
    setSource('文件导入：' + file.name);
    setContent(text);
  }
  function exportMemory() {
    void run('export', async () => {
      const text = await client.exportArchive(user, connection);
      const result: MemoryArchive = JSON.parse(text);
      if (
        !result ||
        !Array.isArray(result.entries) ||
        typeof result.sha256 !== 'string' ||
        (nativeMemory
          ? result.schema !== 'hc-memory-v2' || !Array.isArray(result.automatic_entries)
          : result.schema !== 'hc-memory-v1')
      )
        throw new Error('记忆包不完整，未生成导出文件，请重试');
      // Keep the downloaded file within the same byte limit accepted on import.
      const blob = new Blob([text], { type: 'application/json' });
      if (blob.size > 16 * 1024 * 1024) throw new Error('记忆包超过 16 MiB，无法完整导出');
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'memory-export.json';
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    });
  }
  const memoryRows: ManagedRecord[] = [
    ...(records.isError ? [] : (records.data?.items ?? [])).map((row) => ({
      ...row,
      memory_source: 'manual' as const,
    })),
    ...(nativeMemory && !nativeRecords.isError ? (nativeRecords.data?.items ?? []) : []).map(
      (row) => ({
        ...row,
        type: 'memory',
        source: 'DSH 会话',
        scope,
        version: 0,
        status: 'READY',
        created_at: row.updated_at ?? '',
        updated_at: row.updated_at ?? '',
        memory_source: 'automatic' as const,
      }),
    ),
  ];
  const memoryItems = [
    ...new Map(memoryRows.map((row) => [`${row.memory_source}:${row.id}`, row])).values(),
  ]
    .filter(
      (row) =>
        (memoryFilter === '全部' || memorySource(row) === memoryFilter) &&
        (row.title + ' ' + memoryCategory(row.category))
          .toLocaleLowerCase()
          .includes(documentQuery.toLocaleLowerCase()),
    )
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at) || a.id.localeCompare(b.id));
  const pendingFeedback = operationPending && !removing && (
    <Alert
      role="status"
      aria-live="polite"
      type="info"
      showIcon
      icon={busy ? <Spin size="small" /> : undefined}
      message={
        busy ? (retryOperation.current ? '正在确认保存结果' : '正在完成操作') : '操作结果待确认'
      }
      description={
        busy
          ? retryOperation.current
            ? '内容已保留，完成后将自动更新列表。'
            : '完成后将自动更新列表。'
          : '请重试以完成处理。'
      }
      action={!busy ? <Button onClick={reconcilePending}>重试</Button> : undefined}
    />
  );
  return (
    <Drawer
      open
      title={connection.name}
      width={960}
      onClose={() => {
        if (!busy) onClose();
      }}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      destroyOnClose
      className={`context-details knowledge-details${memory ? ' memory-details' : ''}`}
    >
      <div className="knowledge-detail-summary">
        <div className="knowledge-detail-identity">
          <span className="context-service-icon">
            <ContextServiceIcon backend={connection.backend} />
          </span>
          <div>
            <strong>{backendNames[connection.backend] ?? connection.backend}</strong>
            <span>
              {memory ? '接入的工作空间共用此连接的记忆。' : '接入的工作空间共用此知识库的文档。'}
            </span>
          </div>
          <Tag color={connection.enabled ? status[connection.status].color : 'default'}>
            {connection.enabled ? status[connection.status].label : '已停用'}
          </Tag>
        </div>
        {!memory && connection.embedding && (
          <p>
            Embedding · {connection.embedding.model}
            <span>{connection.embedding.dimensions} 维</span>
          </p>
        )}
      </div>
      {!ready && <Alert type="warning" showIcon message="连接未就绪，请在列表中启用并验证服务。" />}
      {pendingFeedback}
      {!operationPending && (error || (!nativeMemory && records.isError)) && (
        <Alert
          type="error"
          showIcon
          message={error || message(records.error)}
          action={
            <Button disabled={!!busy} onClick={() => void refresh()}>
              刷新
            </Button>
          }
        />
      )}
      {nativeMemory && records.isError && !operationPending && (
        <Alert
          type="error"
          showIcon
          message="手动记忆暂时无法加载"
          description={message(records.error)}
          action={
            <Button disabled={!!busy} onClick={() => void refresh()}>
              重试
            </Button>
          }
        />
      )}
      {nativeMemory &&
        nativeRecords.isError &&
        !(
          operationPending &&
          nativeRecords.error instanceof APIError &&
          nativeRecords.error.code === 'CONTEXT_PENDING'
        ) && (
          <Alert
            type="error"
            showIcon
            message="自动记忆暂时无法加载"
            description={message(nativeRecords.error)}
            action={
              <Button disabled={!!busy} onClick={() => void refresh()}>
                重试
              </Button>
            }
          />
        )}
      {nativeMemory && nativeRecords.data?.truncated && (
        <Alert
          type="info"
          showIcon
          message="当前仅展示部分自动记忆，服务返回的目录已达到读取上限。"
        />
      )}
      {manage && !operationPending && !removing && (error || records.isError) && (
        <Button loading={busy === 'reconcile'} onClick={reconcilePending}>
          核实待确认操作
        </Button>
      )}
      {savedNotice && (
        <Alert
          role="status"
          aria-live="polite"
          type="success"
          showIcon
          message={savedNotice}
          closable
          onClose={() => setSavedNotice('')}
        />
      )}
      {(busy === 'export' || busy === 'import') && (
        <Alert
          role="status"
          aria-live="polite"
          type="info"
          showIcon
          icon={<Spin size="small" />}
          message={busy === 'export' ? '正在导出整个记忆…' : '正在校验记忆包…'}
          description={
            busy === 'export' ? '正在读取记忆原文，请稍候。' : '完成后可预览内容并确认导入。'
          }
        />
      )}
      {rebuildState && (
        <Alert
          className="context-rebuild-feedback"
          role="status"
          aria-live="polite"
          aria-atomic="true"
          type={rebuildState.phase === 'done' ? 'success' : 'info'}
          showIcon
          icon={rebuildState.phase === 'done' ? undefined : <Spin size="small" />}
          message={`${rebuildState.phase === 'done' ? '重建完成' : rebuildState.phase === 'refreshing' ? '正在更新文档列表' : '正在重建索引'}：${rebuildState.record.title}`}
          description={
            rebuildState.phase === 'indexing'
              ? '正在重新生成索引，耗时取决于文档长度和模型服务。'
              : undefined
          }
          closable={rebuildState.phase === 'done'}
          onClose={() => setRebuildState(null)}
        />
      )}
      {memory ? (
        <section className="knowledge-document-header" aria-label="记忆内容管理">
          <div className="knowledge-document-heading">
            <div>
              <h3>记忆管理</h3>
              <p>
                {nativeMemory
                  ? '统一查看会话生成和手动添加的偏好、事实与经验。'
                  : '维护偏好与约定，按需检索或迁移。'}
              </p>
            </div>
            <div className="knowledge-document-utilities">
              <Button
                icon={<Search size={16} aria-hidden="true" />}
                disabled={!ready || !!busy}
                onClick={() => {
                  setSearchError('');
                  setSearchOpen(true);
                }}
              >
                记忆检索
              </Button>
              <Button
                aria-label="刷新内容"
                title="刷新内容"
                icon={<RefreshCw size={16} aria-hidden="true" />}
                loading={listLoading}
                disabled={!ready || !!busy}
                onClick={() => void refresh()}
              />
            </div>
          </div>
          <div className="knowledge-document-toolbar">
            <Input
              aria-label="搜索记忆名称"
              placeholder={nativeMemory ? '搜索名称或分类' : '搜索记忆名称'}
              prefix={<Search size={16} aria-hidden="true" />}
              allowClear
              disabled={!ready}
              value={documentSearch}
              onChange={(e) => setDocumentSearch(e.target.value)}
            />
            <div className="knowledge-document-actions">
              {manage && (
                <Button
                  type="primary"
                  icon={<Plus size={16} aria-hidden="true" />}
                  disabled={!ready || writeBlocked}
                  onClick={() => newContent()}
                >
                  添加记忆
                </Button>
              )}
              <Dropdown
                trigger={['click']}
                menu={{
                  items: [
                    ...(manage
                      ? [
                          {
                            key: 'import',
                            label: '导入整个记忆',
                            icon: <Upload size={15} aria-hidden="true" />,
                          },
                        ]
                      : []),
                    {
                      key: 'export',
                      label: '导出整个记忆',
                      icon: <Download size={15} aria-hidden="true" />,
                    },
                  ],
                  onClick: ({ key }) => {
                    if (key === 'import') document.getElementById('context-import')?.click();
                    else if (key === 'export') exportMemory();
                  },
                }}
              >
                <Button
                  aria-label="记忆更多操作"
                  title="更多操作"
                  loading={busy === 'export' || busy === 'import'}
                  disabled={!ready || writeBlocked}
                  icon={<Ellipsis size={17} aria-hidden="true" />}
                />
              </Dropdown>
            </div>
          </div>
          {nativeMemory && (
            <Segmented
              className="memory-source-filter"
              aria-label="记忆来源筛选"
              options={['全部', '自动生成', '手动添加']}
              value={memoryFilter}
              disabled={!ready}
              onChange={(value) => setMemoryFilter(String(value))}
            />
          )}
          {manage && (
            <input
              id="context-import"
              aria-label="选择记忆文件"
              type="file"
              hidden
              accept=".json,application/json"
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.target.value = '';
                if (file) void run('import', () => importFile(file));
              }}
            />
          )}
        </section>
      ) : (
        <section className="knowledge-document-header" aria-label="文档管理">
          <div className="knowledge-document-heading">
            <div>
              <h3>文档列表</h3>
              <p>添加和维护文档，检索正文与相关片段。</p>
            </div>
            <div className="knowledge-document-utilities">
              <Button
                icon={<Search size={16} aria-hidden="true" />}
                disabled={!ready || !!busy}
                onClick={() => {
                  setSearchError('');
                  setSearchOpen(true);
                }}
              >
                知识库检索
              </Button>
              <Button
                aria-label="刷新内容"
                title="刷新内容"
                icon={<RefreshCw size={16} aria-hidden="true" />}
                loading={records.isFetching}
                disabled={!ready || !!busy}
                onClick={() => void refresh()}
              />
            </div>
          </div>
          <div className="knowledge-document-toolbar">
            <Input
              aria-label="搜索文档名称"
              placeholder="搜索文档名称"
              prefix={<Search size={16} aria-hidden="true" />}
              allowClear
              value={documentSearch}
              disabled={!ready}
              onChange={(e) => setDocumentSearch(e.target.value)}
            />
            {manage && (
              <div className="knowledge-document-actions">
                {hasPermission(user, 'instances:read') && (
                  <Button
                    icon={<Upload size={16} aria-hidden="true" />}
                    disabled={!ready || !!busy}
                    onClick={() => setWorkFiles(true)}
                  >
                    从工作数据选择
                  </Button>
                )}
                <Button
                  type="primary"
                  icon={<Plus size={16} aria-hidden="true" />}
                  disabled={!ready || !!busy}
                  onClick={() => newContent()}
                >
                  添加文档
                </Button>
              </div>
            )}
          </div>
        </section>
      )}
      <Table<ManagedRecord>
        className="knowledge-document-table"
        tableLayout={memory ? 'fixed' : undefined}
        size="small"
        rowKey={(row) => `${row.memory_source ?? 'document'}:${row.id}`}
        loading={listLoading}
        dataSource={
          ready
            ? memory
              ? memoryItems.slice(
                  (documentPagination.page - 1) * documentPagination.pageSize,
                  documentPagination.page * documentPagination.pageSize,
                )
              : records.isError
                ? []
                : (records.data?.items ?? [])
            : []
        }
        pagination={false}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                listFailed || (nativeMemory && (records.isError || nativeRecords.isError))
                  ? memory
                    ? '记忆暂时无法加载'
                    : '文档暂时无法加载'
                  : !ready
                    ? '连接就绪后可查看内容'
                    : documentQuery || memoryFilter !== '全部'
                      ? memory
                        ? '没有符合条件的记忆'
                        : '没有符合条件的文档'
                      : memory
                        ? '暂无记忆'
                        : '暂无文档'
              }
            >
              {ready && !listFailed && (documentQuery || memoryFilter !== '全部') && (
                <Button
                  onClick={() => {
                    setDocumentSearch('');
                    setMemoryFilter('全部');
                  }}
                >
                  {memory ? '清除筛选' : '清除搜索'}
                </Button>
              )}
              {ready && !listFailed && !documentQuery && memoryFilter === '全部' && (
                <p className="knowledge-document-empty-hint">
                  {memory
                    ? nativeMemory
                      ? '添加记忆，或在 DSH 会话归档并完成提取后查看。'
                      : manage
                        ? '点击「添加记忆」保存偏好与约定。'
                        : '此连接还没有记忆。'
                    : manage
                      ? '点击「添加文档」录入内容或导入文件。'
                      : '此知识库还没有文档。'}
                </p>
              )}
            </Empty>
          ),
        }}
        scroll={memory ? undefined : { x: 560 }}
        columns={[
          {
            title: memory ? '记忆' : '文档',
            render: (_, row) => (
              <div className="knowledge-document-name">
                {memory ? (
                  <Brain size={19} aria-hidden="true" />
                ) : (
                  <FileText size={19} aria-hidden="true" />
                )}
                <div>
                  <Button type="link" disabled={!!busy} onClick={() => void open(row)}>
                    {row.title}
                  </Button>
                  <div className="knowledge-document-meta">
                    {row.memory_source === 'automatic' ? (
                      <span>{memoryCategory(row.category)}</span>
                    ) : (
                      <>
                        <span>v{row.version}</span>
                        {row.source && <span title={row.source}>{row.source}</span>}
                      </>
                    )}
                  </div>
                  {memory && (
                    <div
                      className={nativeMemory ? 'memory-record-provenance' : 'memory-record-status'}
                    >
                      {nativeMemory && (
                        <Tag color={row.memory_source === 'automatic' ? 'blue' : undefined}>
                          {memorySource(row)}
                        </Tag>
                      )}
                      {row.memory_source !== 'automatic' &&
                        (rebuildState?.record.id === row.id && rebuildState.phase !== 'done' ? (
                          <Tag color="blue">重建中</Tag>
                        ) : (
                          <Tag color={status[row.status]?.color ?? 'default'}>
                            {status[row.status]?.label ?? row.status}
                          </Tag>
                        ))}
                    </div>
                  )}
                </div>
              </div>
            ),
          },
          {
            title: '状态',
            width: memory ? 90 : undefined,
            responsive: memory ? ['md'] : undefined,
            hidden: nativeMemory,
            render: (_, row) =>
              rebuildState?.record.id === row.id && rebuildState.phase !== 'done' ? (
                <Tag color="blue">重建中</Tag>
              ) : (
                <Tag color={status[row.status]?.color ?? 'default'}>
                  {status[row.status]?.label ?? row.status}
                </Tag>
              ),
          },
          {
            title: '更新于',
            width: 160,
            responsive: ['lg'],
            render: (_, row) =>
              row.updated_at && !Number.isNaN(Date.parse(row.updated_at))
                ? date(row.updated_at)
                : '—',
          },
          {
            title: '操作',
            width: memory ? 128 : 190,
            hidden: !manage && !nativeMemory,
            render: (_, row) =>
              row.memory_source === 'automatic' ? (
                <Button size="small" disabled={!!busy} onClick={() => void open(row)}>
                  查看
                </Button>
              ) : manage ? (
                <Space wrap={memory}>
                  <Button
                    size="small"
                    disabled={writeBlocked}
                    onClick={() =>
                      void run('edit', async () =>
                        newContent(
                          await client.call(user, connection, 'get', { scope, record_id: row.id }),
                        ),
                      )
                    }
                  >
                    编辑
                  </Button>
                  {(!memory || row.status !== 'READY') && (
                    <Button
                      size="small"
                      aria-label={
                        rebuildState?.record.id === row.id && rebuildState.phase !== 'done'
                          ? '重建中'
                          : '重建'
                      }
                      aria-busy={
                        rebuildState?.record.id === row.id && rebuildState.phase !== 'done'
                      }
                      loading={rebuildState?.record.id === row.id && rebuildState.phase !== 'done'}
                      disabled={writeBlocked || records.isFetching}
                      onClick={() => rebuild(row)}
                    >
                      {rebuildState?.record.id === row.id && rebuildState.phase !== 'done'
                        ? '重建中'
                        : '重建'}
                    </Button>
                  )}
                  <Button
                    size="small"
                    danger
                    disabled={writeBlocked}
                    onClick={() => {
                      deleteOperationKey.current = key();
                      setError('');
                      setRemoving(row);
                      setConfirmed(false);
                    }}
                  >
                    删除
                  </Button>
                </Space>
              ) : (
                <Button size="small" disabled={!!busy} onClick={() => void open(row)}>
                  查看
                </Button>
              ),
          },
        ]}
      />
      <DataPagination
        label={memory ? '记忆' : '文档'}
        pagination={documentPagination}
        count={
          ready
            ? memory
              ? memoryItems.slice(
                  (documentPagination.page - 1) * documentPagination.pageSize,
                  documentPagination.page * documentPagination.pageSize,
                ).length
              : records.isError
                ? 0
                : (records.data?.items.length ?? 0)
            : 0
        }
        total={
          ready
            ? memory
              ? memoryItems.length
              : (records.data?.total ?? records.data?.items.length ?? 0)
            : 0
        }
        loading={listLoading || documentSearch.trim() !== documentQuery}
        failed={listFailed}
      />
      {workFiles && (
        <WorkDataPicker
          user={user}
          onClose={() => setWorkFiles(false)}
          onSelected={async (content, title, source) => {
            setTarget(null);
            setTitle(title);
            setSource(source);
            setContent(content);
            setDocumentName(title);
            setDocumentMode('file');
            setEditorView('source');
            setError('');
            setEditing(true);
          }}
        />
      )}
      <Modal
        className={`context-search-modal${memory ? ' context-memory-search-modal' : ''}`}
        width={760}
        open={searchOpen && !detail}
        title={memory ? '记忆检索' : '知识库检索'}
        footer={null}
        closable={!busy}
        maskClosable={!busy}
        onCancel={() => {
          if (!busy) {
            setSearchOpen(false);
            setSimilarityHelpOpen(false);
          }
        }}
      >
        <div className="context-search-condition-heading">
          <p className="context-note">
            {!memory
              ? '检索此知识库的全部文档，支持语义、全文和混合检索。'
              : !nativeMemory
                ? '检索此连接的全部记忆。'
                : searchSource === 'all'
                  ? '检索自动生成和手动添加的全部记忆。'
                  : searchSource === 'automatic'
                    ? '仅检索会话自动生成的记忆。'
                    : '仅检索手动添加的记忆。'}
          </p>
          <Button
            type="text"
            disabled={
              !!busy ||
              (searchSource === 'all' &&
                searchMode === 'semantic' &&
                searchLimit === 5 &&
                minSimilarity === null)
            }
            onClick={() => {
              setSearchSource('all');
              setSearchMode('semantic');
              setSearchLimit(5);
              setMinSimilarity(null);
              setHits(null);
              setExpandedResults([]);
              setSearchError('');
              setSimilarityHelpOpen(false);
            }}
          >
            重置条件
          </Button>
        </div>
        <div
          className={`context-search-options${memory ? ' context-memory-search-options' : ''}${nativeMemory ? '' : ' context-search-basic-options'}`}
        >
          {nativeMemory && (
            <div className="context-search-source">
              <label htmlFor="context-memory-source">记忆来源</label>
              <Select
                id="context-memory-source"
                aria-label="记忆来源"
                value={searchSource}
                disabled={!!busy}
                virtual={false}
                options={[
                  { value: 'all', label: '全部来源' },
                  { value: 'automatic', label: '自动生成' },
                  { value: 'manual', label: '手动添加' },
                ]}
                onChange={(value) => {
                  setSearchSource(value);
                  setHits(null);
                  setSearchError('');
                }}
              />
            </div>
          )}
          <div>
            <label htmlFor="context-search-limit">返回条数</label>
            <Select
              className="context-search-limit"
              id="context-search-limit"
              aria-label="返回条数"
              value={searchLimit}
              disabled={!!busy}
              virtual={false}
              options={[5, 10, 20].map((value) => ({ value, label: `${value} 条` }))}
              onChange={(value) => {
                setSearchLimit(value);
                setHits(null);
                setSearchError('');
              }}
            />
          </div>
          <div>
            <div className="context-similarity-label">
              <label htmlFor="context-min-similarity">最低相关度</label>
              <Popover
                trigger="click"
                placement="bottomRight"
                open={similarityHelpOpen}
                onOpenChange={setSimilarityHelpOpen}
                content={
                  <div className="context-similarity-help">
                    <p>取值范围 -1 至 1，留空不过滤。</p>
                    <p>
                      {memory
                        ? '仅返回相关度达到此值的记忆；数值越高，匹配越严格。'
                        : '全文检索不使用相关度；混合检索中的关键词结果不受此条件限制。'}
                    </p>
                  </div>
                }
              >
                <button
                  type="button"
                  className="context-similarity-help-button"
                  aria-label="最低相关度说明"
                  aria-expanded={similarityHelpOpen}
                >
                  <CircleHelp size={15} aria-hidden="true" />
                </button>
              </Popover>
            </div>
            <InputNumber
              id="context-min-similarity"
              aria-label="最低相关度"
              min={-1}
              max={1}
              step={0.05}
              precision={2}
              placeholder="不过滤"
              value={minSimilarity}
              disabled={!!busy || (!memory && searchMode === 'keyword')}
              onChange={(value) => {
                setMinSimilarity(value);
                setHits(null);
                setSearchError('');
              }}
            />
          </div>
        </div>
        {!memory && (
          <Segmented
            aria-label="检索方式"
            block
            value={searchMode}
            disabled={!!busy}
            options={[
              { label: '语义检索', value: 'semantic' },
              { label: '全文检索', value: 'keyword' },
              { label: '混合检索', value: 'hybrid' },
            ]}
            onChange={(value) => {
              setSearchMode(value as typeof searchMode);
              setHits(null);
              setSearchError('');
            }}
          />
        )}
        <div className="context-search">
          <Input
            aria-label="检索问题"
            value={query}
            maxLength={4000}
            disabled={!!busy}
            placeholder="输入检索内容"
            onChange={(e) => setQuery(e.target.value)}
            onPressEnter={search}
          />
          <Button
            type="primary"
            icon={<Search size={15} />}
            loading={busy === 'search'}
            disabled={!ready || !query.trim() || !!busy}
            onClick={search}
          >
            检索
          </Button>
        </div>
        {searchError && <Alert type="error" showIcon message={searchError} />} {results}
      </Modal>
      <Modal
        className="context-content-editor"
        width={640}
        open={editing}
        title={target ? '编辑内容' : memory ? '添加记忆' : '添加文档'}
        okText="预览"
        cancelText="取消"
        confirmLoading={busy === 'preview'}
        cancelButtonProps={{ disabled: !!busy }}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
        onCancel={() => {
          if (!busy) setEditing(false);
        }}
        okButtonProps={{
          disabled:
            !!busy ||
            operationPending ||
            !title.trim() ||
            !content.trim() ||
            (!memory && !target && documentMode === 'file' && !documentName),
        }}
        onOk={() => {
          if (!busy)
            void run('preview', async () => {
              showPreview(
                await client.call(user, connection, 'preview', {
                  scope,
                  title,
                  content,
                  source: source || '手动保存',
                  type: memory ? 'note' : 'document',
                  ...(target ? { record_id: target.id, record_version: target.version } : {}),
                }),
              );
              setEditing(false);
            });
        }}
      >
        {pendingFeedback}
        {error && !operationPending && <Alert type="error" showIcon message={error} />}
        <Form layout="vertical">
          {!memory && !target && (
            <>
              <Form.Item className="context-document-mode" label="添加方式">
                <Segmented
                  block
                  aria-label="添加方式"
                  value={documentMode}
                  disabled={!!busy}
                  options={[
                    { label: '输入文字', value: 'text' },
                    { label: '选择文件', value: 'file' },
                  ]}
                  onChange={(value) => {
                    setDocumentMode(value as 'text' | 'file');
                    setError('');
                  }}
                />
              </Form.Item>
              {documentMode === 'file' && (
                <div className="context-document-upload">
                  <div className="context-document-upload-main">
                    <span className="context-document-upload-icon">
                      <FileText size={20} aria-hidden="true" />
                    </span>
                    <div className="context-document-upload-copy" aria-live="polite">
                      <strong>{documentName || '选择文档文件'}</strong>
                      <span>
                        {busy === 'document'
                          ? '正在解析文件...'
                          : documentName
                            ? '解析完成，可检查和编辑内容'
                            : 'Word、Excel、PowerPoint、PDF、图片及文本'}
                      </span>
                    </div>
                    <Button
                      icon={<Upload size={15} aria-hidden="true" />}
                      loading={busy === 'document'}
                      disabled={!!busy}
                      onClick={() => documentInput.current?.click()}
                    >
                      {documentName ? '更换文件' : '选择文件'}
                    </Button>
                  </div>
                  <input
                    ref={documentInput}
                    className="context-document-file-input"
                    aria-label="选择文档文件"
                    type="file"
                    accept={documentAccept}
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      event.target.value = '';
                      if (file) {
                        setDocumentName('');
                        setContent('');
                        void run('document', () => selectDocument(file));
                      }
                    }}
                  />
                  <p className="context-document-upload-note">
                    文件最大 16 MiB，提取文字最多 256 KiB。文件发送至平台解析，仅保存提取的文字。
                  </p>
                </div>
              )}
            </>
          )}
          {(memory || !!target || documentMode === 'text' || !!documentName) && (
            <>
              <Form.Item label="标题" htmlFor="context-content-title" required>
                <Input
                  id="context-content-title"
                  placeholder={memory ? '例如：回答偏好、项目约定' : '文档标题'}
                  value={title}
                  maxLength={200}
                  disabled={!!busy}
                  onChange={(e) => setTitle(e.target.value)}
                />
              </Form.Item>
              <Form.Item
                label="内容"
                htmlFor={memory || editorView === 'source' ? 'context-content-body' : undefined}
                required
              >
                {!memory && <ContentViewSwitcher value={editorView} onChange={setEditorView} />}
                {memory || editorView === 'source' ? (
                  <Input.TextArea
                    id="context-content-body"
                    placeholder={
                      memory ? '写下希望保留的偏好、经验或事实' : '输入需要保存的文档内容'
                    }
                    autoSize={{ minRows: 6, maxRows: 12 }}
                    value={content}
                    maxLength={262144}
                    disabled={!!busy}
                    onChange={(e) => setContent(e.target.value)}
                  />
                ) : (
                  <div
                    className="context-editor-markdown"
                    role="region"
                    aria-label="Markdown 预览内容"
                    tabIndex={0}
                  >
                    <MarkdownContent text={content} />
                  </div>
                )}
              </Form.Item>
              <Form.Item label="来源" htmlFor="context-content-source">
                <Input
                  id="context-content-source"
                  placeholder="选填，例如会议纪要或本人确认"
                  value={source}
                  maxLength={1000}
                  disabled={!!busy}
                  onChange={(e) => setSource(e.target.value)}
                />
              </Form.Item>
            </>
          )}
        </Form>
      </Modal>
      <Modal
        className="context-preview-modal"
        width={640}
        open={!!preview}
        title={previewImport ? '确认导入记忆' : memory ? '确认发送到外部服务' : '确认保存文档'}
        okText={
          busy
            ? '正在保存'
            : operationPending
              ? '继续核实'
              : previewImport
                ? '确认导入'
                : '确认保存'
        }
        cancelText="取消"
        confirmLoading={busy === 'apply' || busy === 'reconcile'}
        okButtonProps={{
          disabled:
            !!busy ||
            (previewImport && !preview?.items.length) ||
            (!!preview?.warnings.length && !acceptLoss),
        }}
        cancelButtonProps={{ disabled: !!busy }}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
        onCancel={() => {
          if (!busy) setPreview(null);
        }}
        onOk={() => {
          if (!preview || busy) return;
          if (operationPending && retryOperation.current) {
            reconcilePending();
            return;
          }
          void run('apply', () =>
            save({
              scope,
              token: preview.token,
              mode,
              idempotency_key: previewOperationKey.current,
              confirmed: true,
              accept_loss: acceptLoss,
            }),
          );
        }}
      >
        {pendingFeedback}
        {error && !operationPending && <Alert type="error" message={error} />}
        <Alert type="info" showIcon message={preview?.data_use} />
        {previewImport && (
          <p className="context-import-summary">
            {preview?.source_counts
              ? `自动生成 ${preview.source_counts.automatic} 条 · 手动添加 ${preview.source_counts.manual} 条`
              : `共 ${preview?.items.length ?? 0} 条记忆`}{' '}
            · 确认后导入全部内容
          </p>
        )}
        {previewImport && !preview?.items.length && (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有可导入的记忆" />
        )}
        {previewItems?.map((item) => (
          <article className="context-preview" key={`${item.memory_source ?? 'manual'}:${item.id}`}>
            <div className="context-preview-heading">
              <div className="context-preview-identity">
                <strong>{item.title}</strong>
                {nativeMemory && previewImport && (
                  <Tag color={item.memory_source === 'automatic' ? 'blue' : undefined}>
                    {memorySource(item)}
                  </Tag>
                )}
              </div>
              <Tag color={item.conflict ? 'orange' : 'blue'}>
                {item.conflict ? '已存在' : '新增'}
              </Tag>
            </div>
            {memory ? (
              <p>{item.content?.slice(0, 1200)}</p>
            ) : (
              <KnowledgeContent
                text={item.content?.slice(0, 1200) ?? ''}
                className="context-preview-content"
                label="原文内容"
              />
            )}
            <small>来源：{item.source}</small>
          </article>
        ))}
        {previewImport && !!preview?.items.length && (
          <DataPagination
            label="导入预览"
            pagination={previewPagination}
            count={previewItems?.length ?? 0}
            total={preview.items.length}
            loading={!!busy}
            failed={false}
          />
        )}
        {preview?.items.some((v) => v.conflict) && (
          <Radio.Group
            disabled={!!busy || operationPending}
            value={mode}
            onChange={(e) => setMode(e.target.value)}
          >
            <Radio value="skip">保留现有，跳过冲突</Radio>
            <Radio value="replace">替换现有内容</Radio>
          </Radio.Group>
        )}
        {!!preview?.warnings.length && (
          <>
            <Alert
              type="warning"
              showIcon
              message="迁移有语义损失"
              description={preview.warnings.join(' ')}
            />
            <Checkbox
              disabled={!!busy || operationPending}
              checked={acceptLoss}
              onChange={(e) => setAcceptLoss(e.target.checked)}
            >
              已了解并接受上述语义损失
            </Checkbox>
          </>
        )}
      </Modal>
      <ContextRecordPreview
        detail={detail}
        nativeMemory={nativeMemory}
        onClose={() => setDetail(null)}
      />
      <ContextDeleteDialog
        memory={memory}
        removing={removing}
        busy={busy}
        operationPending={operationPending}
        confirmed={confirmed}
        error={error}
        onConfirmedChange={setConfirmed}
        onCancel={() => {
          if (!busy) setRemoving(null);
        }}
        onConfirm={() =>
          removing &&
          void run('delete', () =>
            remove({
              scope,
              record_id: removing.id,
              record_version: removing.version,
              idempotency_key: deleteOperationKey.current,
              confirmed: true,
            }),
          )
        }
      />
    </Drawer>
  );
}
