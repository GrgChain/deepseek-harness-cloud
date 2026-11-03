import { Button, Collapse, Empty, Tag } from 'antd';

import { FileText } from 'lucide-react';

import { type ContextRecord } from '../../api/context';

import { KnowledgeContent } from './knowledge-content';

import { ManagedRecord, memoryCategory, memorySource } from './context-shared';

interface ContextSearchResultsProps {
  hits: ManagedRecord[] | null;
  memory: boolean;
  nativeMemory: boolean;
  busy: string;
  searchMode: 'semantic' | 'keyword' | 'hybrid';
  expandedResults: string[];
  onExpandedChange: (keys: string[]) => void;
  onClear: () => void;
  onOpen: (record: ManagedRecord) => Promise<void>;
}

const resultKey = (hit: ContextRecord, i: number) =>
  `${hit.memory_source ?? 'manual'}:${hit.id}:${hit.version}:${hit.position ?? i}`;

export function ContextSearchResults({
  hits,
  memory,
  nativeMemory,
  busy,
  searchMode,
  expandedResults,
  onExpandedChange,
  onClear,
  onOpen,
}: ContextSearchResultsProps) {
  return (
    hits !== null && (
      <section
        className="context-results"
        aria-label="检索结果"
        aria-live="polite"
        tabIndex={memory ? undefined : 0}
      >
        {memory ? (
          <div className="section-top">
            <h3>检索结果 · {hits.length}</h3>
            <Button type="text" onClick={() => onClear()}>
              收起
            </Button>
          </div>
        ) : (
          hits.length > 0 && (
            <div className="context-search-result-actions">
              <Button
                type="text"
                onClick={() =>
                  onExpandedChange(
                    expandedResults.length === hits.length ? [] : hits.map(resultKey),
                  )
                }
              >
                {expandedResults.length === hits.length ? '全部收起' : '全部展开'}
              </Button>
            </div>
          )
        )}
        {hits.length === 0 ? (
          <Empty description="没有命中授权范围内的内容" />
        ) : memory ? (
          hits.map((hit, i) => (
            <article key={resultKey(hit, i)}>
              <Button type="link" disabled={!!busy} onClick={() => void onOpen(hit)}>
                {hit.title}
              </Button>
              <div className="context-memory-result-meta">
                {nativeMemory && (
                  <Tag color={hit.memory_source === 'automatic' ? 'blue' : undefined}>
                    {memorySource(hit)}
                  </Tag>
                )}
                {hit.memory_source !== 'automatic' && <span>v{hit.version}</span>}
                {hit.memory_source === 'automatic' && hit.category && (
                  <span>{memoryCategory(hit.category)}</span>
                )}
                {hit.position && <span>{hit.position}</span>}
                {typeof hit.score === 'number' && Number.isFinite(hit.score) && (
                  <span>相关度 {hit.score.toFixed(3)}</span>
                )}
              </div>
              <p>{hit.snippet}</p>
              <small>来源：{hit.source}</small>
            </article>
          ))
        ) : (
          <Collapse
            className="context-search-results"
            ghost
            activeKey={expandedResults}
            onChange={(keys) => onExpandedChange(Array.isArray(keys) ? keys : [keys])}
            items={hits.map((hit, i) => ({
              key: resultKey(hit, i),
              label: (
                <div className="context-search-result-heading">
                  <strong>{hit.title}</strong>
                  <span>
                    v{hit.version} · {hit.position}
                    {hit.section && <span> · {hit.section}</span>}
                    {hit.match_methods?.includes('keyword') && <span> · 关键词匹配</span>}
                    {typeof hit.score === 'number' && Number.isFinite(hit.score) && (
                      <span title="向量检索返回的相似度">
                        {' '}
                        · {searchMode === 'hybrid' ? '向量相似度' : '相关度'} {hit.score.toFixed(3)}
                      </span>
                    )}
                    {typeof hit.fusion_score === 'number' && Number.isFinite(hit.fusion_score) && (
                      <span title="关键词与向量排名融合后的分数">
                        {' '}
                        · 融合分数 {hit.fusion_score.toFixed(4)}
                      </span>
                    )}
                  </span>
                </div>
              ),
              children: (
                <>
                  <KnowledgeContent
                    text={hit.snippet ?? ''}
                    className="context-search-result-content"
                    label={`${hit.title} 的检索内容`}
                  />
                  <div className="context-search-result-footer">
                    <small>来源：{hit.source}</small>
                    <Button
                      icon={<FileText size={15} aria-hidden="true" />}
                      loading={busy === 'get'}
                      disabled={!!busy}
                      onClick={() => void onOpen(hit)}
                    >
                      查看原文
                    </Button>
                  </div>
                </>
              ),
            }))}
          />
        )}
      </section>
    )
  );
}
