import { Button, Dropdown, Empty, Input, Segmented, Space, Table, Tag } from 'antd';
import { Brain, Download, Ellipsis, FileText, Plus, RefreshCw, Search, Upload } from 'lucide-react';
import type { ContextRecord } from '../../api/context';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { DataPagination, type PagePagination } from '../../shared/components/data-pagination';
import { date, ManagedRecord, memoryCategory, memorySource, status } from './context-shared';

export interface RebuildState {
  record: ContextRecord;
  phase: 'indexing' | 'refreshing' | 'done';
}

export interface ContextRecordsSectionProps {
  user: CurrentUser;
  memory: boolean;
  nativeMemory: boolean;
  remoteNativeMemory: boolean;
  manage: boolean;
  ready: boolean;
  busy: string;
  writeBlocked: boolean;
  listLoading: boolean;
  recordsFetching: boolean;
  listFailed: boolean;
  recordsError: boolean;
  nativeRecordsError: boolean;
  documentSearch: string;
  documentQuery: string;
  memoryFilter: string;
  pagination: PagePagination;
  rows: ManagedRecord[];
  total: number;
  rebuildState: RebuildState | null;
  onOpenSearch: () => void;
  onRefresh: () => void;
  onDocumentSearchChange: (value: string) => void;
  onMemoryFilterChange: (value: string) => void;
  onNewContent: () => void;
  onImportFile: (file: File) => void;
  onExportMemory: () => void;
  onOpenWorkFiles: () => void;
  onOpenRecord: (row: ManagedRecord) => void;
  onEditRecord: (row: ManagedRecord) => void;
  onRebuildRecord: (row: ContextRecord) => void;
  onRemoveRecord: (row: ManagedRecord) => void;
}

export function ContextRecordsSection({
  user,
  memory,
  nativeMemory,
  remoteNativeMemory,
  manage,
  ready,
  busy,
  writeBlocked,
  listLoading,
  recordsFetching,
  listFailed,
  recordsError,
  nativeRecordsError,
  documentSearch,
  documentQuery,
  memoryFilter,
  pagination,
  rows,
  total,
  rebuildState,
  onOpenSearch,
  onRefresh,
  onDocumentSearchChange,
  onMemoryFilterChange,
  onNewContent,
  onImportFile,
  onExportMemory,
  onOpenWorkFiles,
  onOpenRecord,
  onEditRecord,
  onRebuildRecord,
  onRemoveRecord,
}: ContextRecordsSectionProps) {
  return (
    <>
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
                onClick={onOpenSearch}
              >
                记忆检索
              </Button>
              <Button
                aria-label="刷新内容"
                title="刷新内容"
                icon={<RefreshCw size={16} aria-hidden="true" />}
                loading={listLoading}
                disabled={!ready || !!busy}
                onClick={onRefresh}
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
              onChange={(e) => onDocumentSearchChange(e.target.value)}
            />
            <div className="knowledge-document-actions">
              {manage && (
                <Button
                  type="primary"
                  icon={<Plus size={16} aria-hidden="true" />}
                  disabled={!ready || writeBlocked}
                  onClick={onNewContent}
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
                    else if (key === 'export') onExportMemory();
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
              onChange={(value) => onMemoryFilterChange(String(value))}
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
                if (file) onImportFile(file);
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
                onClick={onOpenSearch}
              >
                知识库检索
              </Button>
              <Button
                aria-label="刷新内容"
                title="刷新内容"
                icon={<RefreshCw size={16} aria-hidden="true" />}
                loading={recordsFetching}
                disabled={!ready || !!busy}
                onClick={onRefresh}
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
              onChange={(e) => onDocumentSearchChange(e.target.value)}
            />
            {manage && (
              <div className="knowledge-document-actions">
                {hasPermission(user, 'instances:read') && (
                  <Button
                    icon={<Upload size={16} aria-hidden="true" />}
                    disabled={!ready || !!busy}
                    onClick={onOpenWorkFiles}
                  >
                    从工作数据选择
                  </Button>
                )}
                <Button
                  type="primary"
                  icon={<Plus size={16} aria-hidden="true" />}
                  disabled={!ready || !!busy}
                  onClick={onNewContent}
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
        dataSource={rows}
        pagination={false}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                listFailed || (nativeMemory && (recordsError || nativeRecordsError))
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
                    onDocumentSearchChange('');
                    onMemoryFilterChange('全部');
                  }}
                >
                  {memory ? '清除筛选' : '清除搜索'}
                </Button>
              )}
              {ready && !listFailed && !documentQuery && memoryFilter === '全部' && (
                <p className="knowledge-document-empty-hint">
                  {memory
                    ? nativeMemory
                      ? 'DSH 生成或手动添加记忆后，将在这里展示。'
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
                  <Button type="link" disabled={!!busy} onClick={() => onOpenRecord(row)}>
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
            render: (_, row) => (
              <Space wrap={memory}>
                {row.memory_source === 'automatic' || !manage ? (
                  <Button size="small" disabled={!!busy} onClick={() => onOpenRecord(row)}>
                    查看
                  </Button>
                ) : (
                  <>
                    <Button size="small" disabled={writeBlocked} onClick={() => onEditRecord(row)}>
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
                        loading={
                          rebuildState?.record.id === row.id && rebuildState.phase !== 'done'
                        }
                        disabled={writeBlocked || recordsFetching}
                        onClick={() => onRebuildRecord(row)}
                      >
                        {rebuildState?.record.id === row.id && rebuildState.phase !== 'done'
                          ? '重建中'
                          : '重建'}
                      </Button>
                    )}
                  </>
                )}
                {manage && (row.memory_source !== 'automatic' || !remoteNativeMemory) && (
                  <Button
                    size="small"
                    danger
                    disabled={writeBlocked}
                    onClick={() => onRemoveRecord(row)}
                  >
                    删除
                  </Button>
                )}
              </Space>
            ),
          },
        ]}
      />
      <DataPagination
        label={memory ? '记忆' : '文档'}
        pagination={pagination}
        count={rows.length}
        total={total}
        loading={listLoading || documentSearch.trim() !== documentQuery}
        failed={listFailed}
      />
    </>
  );
}
