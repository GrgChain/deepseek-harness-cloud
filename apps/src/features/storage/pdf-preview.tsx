import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Button, InputNumber, Select, Spin, Tooltip } from 'antd';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { getDocument, GlobalWorkerOptions, TextLayer, version, type PDFDocumentProxy, type RenderTask } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

GlobalWorkerOptions.workerSrc = workerUrl;

export default function PDFPreview({ blob, onError }: { blob: Blob; onError: (message: string) => void }) {
 const [document, setDocument] = useState<PDFDocumentProxy | null>(null);
 const [pageNumber, setPageNumber] = useState(1);
 const [zoom, setZoom] = useState<number | 'fit'>('fit');
 const [width, setWidth] = useState(0);
 const [busy, setBusy] = useState(true);
 const [pageSize, setPageSize] = useState({ width: 0, height: 0, scale: 1 });
 const viewer = useRef<HTMLDivElement>(null);
 const canvas = useRef<HTMLCanvasElement>(null);
 const text = useRef<HTMLDivElement>(null);
 useEffect(() => {
  let cancelled = false;
  let task: ReturnType<typeof getDocument> | undefined;
  const timeout = window.setTimeout(() => onError('PDF 解析超时，请重新加载或下载查看'), 30_000);
  const assets = new URL(`${import.meta.env.BASE_URL}pdfjs/${version}/`, window.location.origin).href;
  void blob.arrayBuffer().then(data => {
   if (cancelled) return;
   task = getDocument({ data: new Uint8Array(data), cMapUrl: `${assets}cmaps/`, cMapPacked: true,
    standardFontDataUrl: `${assets}standard_fonts/`, wasmUrl: `${assets}wasm/`, iccUrl: `${assets}iccs/`,
    enableXfa: false, maxImageSize: 16_777_216, canvasMaxAreaInBytes: 64 * 1024 * 1024 });
   return task.promise;
  }).then(value => { window.clearTimeout(timeout); if (value && !cancelled) setDocument(value); }).catch(cause => {
   window.clearTimeout(timeout);
   if (!cancelled) onError(cause?.name === 'PasswordException' ? '此 PDF 已加密，请下载后输入密码查看' : 'PDF 已损坏或无法读取，请下载查看');
  });
  return () => { cancelled = true; window.clearTimeout(timeout); void task?.destroy(); };
 }, [blob, onError]);
 useEffect(() => {
  const element = viewer.current;
  if (!element) return;
  const observer = new ResizeObserver(entries => setWidth(Math.floor(entries[0].contentRect.width)));
  observer.observe(element);
  return () => observer.disconnect();
 }, []);
 useEffect(() => {
  if (!document || !width) return;
  let cancelled = false;
  let renderTask: RenderTask | undefined;
  let layer: TextLayer | undefined;
  const timeout = window.setTimeout(() => onError('此页显示超时，请重新加载或下载查看'), 30_000);
  setBusy(true);
  // A new canvas for each render prevents overlapping tasks on rapid page/zoom changes.
  const target = canvas.current;
  const container = text.current;
  if (!target || !container) { window.clearTimeout(timeout); return; }
  container.replaceChildren();
  void (async () => {
   const page = await document.getPage(pageNumber);
   if (cancelled) return;
   const natural = page.getViewport({ scale: 1 });
   const scale = zoom === 'fit' ? Math.min(2, Math.max(0.1, (width - 24) / natural.width)) : zoom;
   const viewport = page.getViewport({ scale });
   const ratio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(16_777_216 / (viewport.width * viewport.height)));
   target.width = Math.floor(viewport.width * ratio);
   target.height = Math.floor(viewport.height * ratio);
   target.style.width = `${viewport.width}px`; target.style.height = `${viewport.height}px`;
   setPageSize({ width: viewport.width, height: viewport.height, scale });
   renderTask = page.render({ canvas: target, viewport, transform: [ratio, 0, 0, ratio, 0, 0] });
   await renderTask.promise;
   if (cancelled) return;
   layer = new TextLayer({ textContentSource: page.streamTextContent(), container, viewport });
   await layer.render();
   if (!cancelled) setBusy(false);
  })().catch(cause => { if (!cancelled && cause?.name !== 'RenderingCancelledException') onError('此页无法显示，请重新加载或下载查看'); }).finally(() => window.clearTimeout(timeout));
  return () => { cancelled = true; window.clearTimeout(timeout); renderTask?.cancel(); layer?.cancel(); };
 }, [document, pageNumber, zoom, width, onError]);
 return <section className="storage-pdf-preview" aria-label="PDF 文档">
  <div className="storage-pdf-toolbar">
   <Tooltip title="上一页"><Button aria-label="PDF 上一页" icon={<ChevronLeft size={16}/>} disabled={!document || pageNumber <= 1} onClick={() => setPageNumber(pageNumber - 1)}/></Tooltip>
   <label className="storage-pdf-pages">第 <InputNumber aria-label="PDF 页码" min={1} max={document?.numPages ?? 1} value={pageNumber} controls={false} disabled={!document} onChange={value => { if (value) setPageNumber(Math.max(1, Math.min(document?.numPages ?? 1, value))); }}/> / {document?.numPages ?? '—'} 页</label>
   <Tooltip title="下一页"><Button aria-label="PDF 下一页" icon={<ChevronRight size={16}/>} disabled={!document || pageNumber >= document.numPages} onClick={() => setPageNumber(pageNumber + 1)}/></Tooltip>
   <Select aria-label="PDF 缩放" value={zoom} onChange={setZoom} options={[{ value: 'fit', label: '适合宽度' }, ...[0.5, 0.75, 1, 1.25, 1.5, 2].map(value => ({ value, label: `${value * 100}%` }))]}/>
  </div>
  <div className="storage-pdf-viewport" ref={viewer} aria-busy={busy}>
   {busy && <div className="storage-pdf-loading" role="status"><Spin/><span>正在显示页面…</span></div>}
   <div className="storage-pdf-page" style={{ width: pageSize.width || undefined, height: pageSize.height || undefined, '--total-scale-factor': pageSize.scale } as CSSProperties}>
    <canvas key={`${pageNumber}:${zoom}:${width}`} ref={canvas} aria-label={`PDF 第 ${pageNumber} 页`}/><div ref={text} className="storage-pdf-text"/>
   </div>
  </div>
 </section>;
}
