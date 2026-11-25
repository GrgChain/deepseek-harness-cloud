import { Alert, Button, Input, InputNumber, Modal, Popover, Segmented, Select } from 'antd';
import { CircleHelp, Search } from 'lucide-react';
import type { ReactNode } from 'react';

export interface ContextSearchModalProps {
  open: boolean;
  memory: boolean;
  nativeMemory: boolean;
  ready: boolean;
  busy: string;
  query: string;
  searchMode: 'semantic' | 'keyword' | 'hybrid';
  searchLimit: number;
  minSimilarity: number | null;
  searchSource: 'all' | 'automatic' | 'manual';
  similarityHelpOpen: boolean;
  searchError: string;
  results: ReactNode;
  onClose: () => void;
  onReset: () => void;
  onSearchSourceChange: (value: 'all' | 'automatic' | 'manual') => void;
  onSearchLimitChange: (value: number) => void;
  onMinSimilarityChange: (value: number | null) => void;
  onSimilarityHelpOpenChange: (open: boolean) => void;
  onSearchModeChange: (value: 'semantic' | 'keyword' | 'hybrid') => void;
  onQueryChange: (value: string) => void;
  onSearch: () => void;
}

export function ContextSearchModal({
  open,
  memory,
  nativeMemory,
  ready,
  busy,
  query,
  searchMode,
  searchLimit,
  minSimilarity,
  searchSource,
  similarityHelpOpen,
  searchError,
  results,
  onClose,
  onReset,
  onSearchSourceChange,
  onSearchLimitChange,
  onMinSimilarityChange,
  onSimilarityHelpOpenChange,
  onSearchModeChange,
  onQueryChange,
  onSearch,
}: ContextSearchModalProps) {
  return (
    <Modal
      className={`context-search-modal${memory ? ' context-memory-search-modal' : ''}`}
      width={760}
      open={open}
      title={memory ? '记忆检索' : '知识库检索'}
      footer={null}
      closable={!busy}
      maskClosable={!busy}
      onCancel={onClose}
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
          onClick={onReset}
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
              onChange={onSearchSourceChange}
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
            onChange={onSearchLimitChange}
          />
        </div>
        <div>
          <div className="context-similarity-label">
            <label htmlFor="context-min-similarity">最低相关度</label>
            <Popover
              trigger="click"
              placement="bottomRight"
              open={similarityHelpOpen}
              onOpenChange={onSimilarityHelpOpenChange}
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
            onChange={onMinSimilarityChange}
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
          onChange={(value) => onSearchModeChange(value as 'semantic' | 'keyword' | 'hybrid')}
        />
      )}
      <div className="context-search">
        <Input
          aria-label="检索问题"
          value={query}
          maxLength={4000}
          disabled={!!busy}
          placeholder="输入检索内容"
          onChange={(e) => onQueryChange(e.target.value)}
          onPressEnter={onSearch}
        />
        <Button
          type="primary"
          icon={<Search size={15} />}
          loading={busy === 'search'}
          disabled={!ready || !query.trim() || !!busy}
          onClick={onSearch}
        >
          检索
        </Button>
      </div>
      {searchError && <Alert type="error" showIcon message={searchError} />} {results}
    </Modal>
  );
}
