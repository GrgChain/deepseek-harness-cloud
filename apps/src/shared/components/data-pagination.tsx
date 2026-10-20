import { useEffect, useState } from 'react';
import { Button, Select } from 'antd';
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from 'lucide-react';

export function usePagePagination(scope: string) {
  const initial = { scope, page: 1, pageSize: 10 };
  const [saved, setSaved] = useState(initial);
  const state = saved.scope === scope ? saved : { ...initial, pageSize: saved.pageSize };
  if (saved.scope !== scope) setSaved(state);
  return {
    page: state.page, pageSize: state.pageSize,
    go: (page: number) => setSaved({ ...state, page: Math.max(1, page) }),
    resize: (pageSize: number) => setSaved({ ...state, page: 1, pageSize }),
  };
}

export function DataPagination({ label, pagination, count, total, loading, failed }: {
  label: string;
  pagination: ReturnType<typeof usePagePagination>;
  count: number;
  total?: number;
  loading: boolean;
  failed: boolean;
}) {
  const { page, pageSize, go } = pagination;
  const records = total ?? count;
  const pages = Math.max(1, Math.ceil(records / pageSize));
  // Deleting rows or refreshing filters can reduce the number of pages.
  useEffect(() => { if (!loading && !failed && page > pages) go(pages); }, [loading, failed, page, pages, go]);
  const unavailable = loading || failed;
  return <nav className="data-pagination" aria-label={`${label}分页`} aria-busy={loading}>
    <div className="data-pagination-summary" role="status" aria-live="polite">
      {loading ? '正在加载数据…' : failed ? '数据暂不可用' : <>共 <strong>{records}</strong> 条 · 本页 <strong>{count}</strong> 条{records === 0 ? ' · 暂无数据' : ''}</>}
    </div>
    <div className="data-pagination-controls">
      <Select aria-label="每页条数" value={pageSize} disabled={loading} virtual={false}
        options={[10, 20, 50].map(value => ({ value, label: `${value} 条 / 页` }))} onChange={pagination.resize} />
      <div className="data-pagination-pages">
        <Button aria-label="返回首页" title="返回首页" disabled={page === 1 || loading} icon={<ChevronsLeft size={16} aria-hidden="true" />} onClick={() => go(1)} />
        <Button aria-label="上一页" disabled={page === 1 || loading} icon={<ChevronLeft size={16} aria-hidden="true" />} onClick={() => go(page - 1)}>上一页</Button>
        <span className="data-pagination-current" aria-current="page">第 <strong>{page}</strong> / {unavailable ? '—' : pages} 页</span>
        <Button aria-label="下一页" disabled={page >= pages || unavailable} onClick={() => go(page + 1)}>下一页<ChevronRight size={16} aria-hidden="true" /></Button>
        <Button aria-label="跳到尾页" title="跳到尾页" disabled={page >= pages || unavailable} icon={<ChevronsRight size={16} aria-hidden="true" />} onClick={() => go(pages)} />
      </div>
    </div>
  </nav>;
}
