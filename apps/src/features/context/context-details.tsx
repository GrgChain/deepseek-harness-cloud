import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Drawer, Spin, Tag } from 'antd';
import { useEffect, useRef, useState } from 'react';
import {
  type ContextConnection,
  type ContextPreview,
  type ContextRecord,
  type MemoryArchive,
} from '../../api/context';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { usePagePagination } from '../../shared/components/data-pagination';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage as message } from '../../shared/utils/error-message';
import { ContextDeleteDialog } from './context-delete-dialog';
import { ContextEditorModal } from './context-editor-modal';
import { ContextPreviewModal } from './context-preview-modal';
import { ContextRecordPreview } from './context-record-preview';
import { ContextRecordsSection, type RebuildState } from './context-records-table';
import { ContextSearchModal } from './context-search-modal';
import { ContextSearchResults } from './context-search-results';
import { ContextServiceIcon } from './context-service-picker';
import {
  backendNames,
  client,
  ManagedRecord,
  memoryCategory,
  memorySource,
  SessionMemory,
  status,
} from './context-shared';
import { extractDocumentText } from './document-import';
import { type ContentView } from './knowledge-content';
import { WorkDataPicker } from './work-data-picker';

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
  const pendingSaveBody = useRef<Record<string, unknown> | null>(null);
  useEffect(() => () => operationController.current?.abort(), []);
  useEffect(() => {
    if (!savedNotice) return;
    const timer = window.setTimeout(() => setSavedNotice(''), 3000);
    return () => window.clearTimeout(timer);
  }, [savedNotice]);
  const [rebuildState, setRebuildState] = useState<RebuildState | null>(null);
  useEffect(() => {
    if (rebuildState?.phase !== 'done') return;
    const timer = window.setTimeout(() => setRebuildState(null), 3000);
    return () => window.clearTimeout(timer);
  }, [rebuildState]);
  const memory = connection.kind === 'memory';
  const manage = hasPermission(user, connection.kind + ':manage');
  const cache = useQueryClient();
  const remoteNativeMemory = memory && connection.backend === 'openviking';
  const nativeMemory = memory && ['openviking', 'mem0', 'graphiti'].includes(connection.backend);
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
    queryFn: ({ signal }) =>
      client.call<{ items: ContextRecord[]; total?: number }>(
        user,
        connection,
        'list',
        {
          scope,
          ...(!memory
            ? {
                query: documentQuery,
                page: documentPagination.page,
                page_size: documentPagination.pageSize,
              }
            : {}),
        },
        signal,
      ),
    enabled: ready,
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(records.error);
  const nativeRecords = useQuery({
    queryKey: ['context-session-memories', user.id, connection.id, connection.version],
    queryFn: ({ signal }) =>
      client.call<{ items: SessionMemory[]; truncated: boolean }>(
        user,
        connection,
        'session-list',
        {},
        signal,
      ),
    enabled: ready && remoteNativeMemory,
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(nativeRecords.error);
  const listLoading = records.isFetching || (remoteNativeMemory && nativeRecords.isFetching);
  const listFailed = records.isError && (!remoteNativeMemory || nativeRecords.isError);

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
    pendingSaveBody.current = body;
    try {
      const result = await client.callAndConfirm<{
        applied?: number | string[];
        skipped?: number | string[];
      }>(user, connection, 'apply', body, () => setOperationPending(true), controller.signal);
      const applied = Array.isArray(result.applied) ? result.applied.length : result.applied;
      const skipped = Array.isArray(result.skipped) ? result.skipped.length : result.skipped;
      pendingSaveBody.current = null;
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
        pendingSaveBody.current = null;
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
      return await client.callAndConfirm<T>(
        user,
        connection,
        action,
        body,
        () => setOperationPending(true),
        controller.signal,
      );
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
      if (pendingSaveBody.current) {
        await save(pendingSaveBody.current);
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
    await Promise.all([
      records.refetch(),
      ...(remoteNativeMemory ? [nativeRecords.refetch()] : []),
    ]);
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
      if (row.memory_source === 'automatic' && remoteNativeMemory) {
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
        (remoteNativeMemory
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
      memory_source: row.memory_source ?? ('manual' as const),
    })),
    ...(remoteNativeMemory && !nativeRecords.isError ? (nativeRecords.data?.items ?? []) : []).map(
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

  const visibleRows = ready
    ? memory
      ? memoryItems.slice(
          (documentPagination.page - 1) * documentPagination.pageSize,
          documentPagination.page * documentPagination.pageSize,
        )
      : records.isError
        ? []
        : (records.data?.items ?? [])
    : [];
  const totalRecords = ready
    ? memory
      ? memoryItems.length
      : (records.data?.total ?? records.data?.items.length ?? 0)
    : 0;

  const pendingFeedback = operationPending && !removing && (
    <Alert
      role="status"
      aria-live="polite"
      type="info"
      showIcon
      icon={busy ? <Spin size="small" /> : undefined}
      message={
        busy ? (pendingSaveBody.current ? '正在确认保存结果' : '正在完成操作') : '操作结果待确认'
      }
      description={
        busy
          ? pendingSaveBody.current
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
      {!operationPending && (error || (!remoteNativeMemory && records.isError)) && (
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
      {remoteNativeMemory && records.isError && !operationPending && (
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
      {remoteNativeMemory &&
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
      {remoteNativeMemory && nativeRecords.data?.truncated && (
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
      <ContextRecordsSection
        user={user}
        memory={memory}
        nativeMemory={nativeMemory}
        remoteNativeMemory={remoteNativeMemory}
        manage={manage}
        ready={ready}
        busy={busy}
        writeBlocked={writeBlocked}
        listLoading={listLoading}
        recordsFetching={records.isFetching}
        listFailed={listFailed}
        recordsError={records.isError}
        nativeRecordsError={nativeRecords.isError}
        documentSearch={documentSearch}
        documentQuery={documentQuery}
        memoryFilter={memoryFilter}
        pagination={documentPagination}
        rows={visibleRows}
        total={totalRecords}
        rebuildState={rebuildState}
        onOpenSearch={() => {
          setSearchError('');
          setSearchOpen(true);
        }}
        onRefresh={() => void refresh()}
        onDocumentSearchChange={setDocumentSearch}
        onMemoryFilterChange={setMemoryFilter}
        onNewContent={() => newContent()}
        onImportFile={(file) => void run('import', () => importFile(file))}
        onExportMemory={exportMemory}
        onOpenWorkFiles={() => setWorkFiles(true)}
        onOpenRecord={(row) => void open(row)}
        onEditRecord={(row) =>
          void run('edit', async () =>
            newContent(
              await client.call(user, connection, 'get', {
                scope,
                record_id: row.id,
              }),
            ),
          )
        }
        onRebuildRecord={rebuild}
        onRemoveRecord={(row) => {
          deleteOperationKey.current = key();
          setError('');
          setRemoving(row);
          setConfirmed(false);
        }}
      />
      {workFiles && (
        <WorkDataPicker
          user={user}
          onClose={() => setWorkFiles(false)}
          onSelected={async (pickedContent, pickedTitle, pickedSource) => {
            setTarget(null);
            setTitle(pickedTitle);
            setSource(pickedSource);
            setContent(pickedContent);
            setDocumentName(pickedTitle);
            setDocumentMode('file');
            setEditorView('source');
            setError('');
            setEditing(true);
          }}
        />
      )}
      <ContextSearchModal
        open={searchOpen && !detail}
        memory={memory}
        nativeMemory={nativeMemory}
        ready={ready}
        busy={busy}
        query={query}
        searchMode={searchMode}
        searchLimit={searchLimit}
        minSimilarity={minSimilarity}
        searchSource={searchSource}
        similarityHelpOpen={similarityHelpOpen}
        searchError={searchError}
        results={
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
        }
        onClose={() => {
          if (!busy) {
            setSearchOpen(false);
            setSimilarityHelpOpen(false);
          }
        }}
        onReset={() => {
          setSearchSource('all');
          setSearchMode('semantic');
          setSearchLimit(5);
          setMinSimilarity(null);
          setHits(null);
          setExpandedResults([]);
          setSearchError('');
          setSimilarityHelpOpen(false);
        }}
        onSearchSourceChange={(value) => {
          setSearchSource(value);
          setHits(null);
          setSearchError('');
        }}
        onSearchLimitChange={(value) => {
          setSearchLimit(value);
          setHits(null);
          setSearchError('');
        }}
        onMinSimilarityChange={(value) => {
          setMinSimilarity(value);
          setHits(null);
          setSearchError('');
        }}
        onSimilarityHelpOpenChange={setSimilarityHelpOpen}
        onSearchModeChange={(value) => {
          setSearchMode(value);
          setHits(null);
          setSearchError('');
        }}
        onQueryChange={setQuery}
        onSearch={search}
      />
      <ContextEditorModal
        open={editing}
        memory={memory}
        target={target}
        busy={busy}
        operationPending={operationPending}
        error={error}
        pendingFeedback={pendingFeedback}
        documentMode={documentMode}
        documentName={documentName}
        editorView={editorView}
        title={title}
        content={content}
        source={source}
        onCancel={() => {
          if (!busy) setEditing(false);
        }}
        onSubmit={() => {
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
        onDocumentModeChange={(value) => {
          setDocumentMode(value);
          setError('');
        }}
        onSelectDocument={(file) => {
          setDocumentName('');
          setContent('');
          void run('document', () => selectDocument(file));
        }}
        onTitleChange={setTitle}
        onContentChange={setContent}
        onSourceChange={setSource}
        onEditorViewChange={setEditorView}
      />
      <ContextPreviewModal
        preview={preview}
        previewImport={previewImport}
        memory={memory}
        nativeMemory={nativeMemory}
        busy={busy}
        operationPending={operationPending}
        error={error}
        pendingFeedback={pendingFeedback}
        mode={mode}
        acceptLoss={acceptLoss}
        onModeChange={setMode}
        onAcceptLossChange={setAcceptLoss}
        onCancel={() => {
          if (!busy) setPreview(null);
        }}
        onConfirm={() => {
          if (!preview || busy) return;
          if (operationPending && pendingSaveBody.current) {
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
      />
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
