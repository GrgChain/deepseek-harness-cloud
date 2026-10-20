import type { OfficePreviewResult } from './office-file';

export const OFFICE_PREVIEW_MAX_BYTES = 16 * 1024 * 1024;
const OFFICE_PREVIEW_TIMEOUT_MS = 30_000;

export function isOfficePreviewable(name: string) {
 return /\.(docx|xlsx|pptx|odt|ods|odp)$/i.test(name);
}

export function previewOfficeFile(name: string, blob: Blob, signal: AbortSignal): Promise<OfficePreviewResult> {
 if (blob.size > OFFICE_PREVIEW_MAX_BYTES) return Promise.reject(new Error('Office 文件预览上限为 16 MiB'));
 return new Promise((resolve, reject) => {
  if (signal.aborted) return reject(new DOMException('预览已取消', 'AbortError'));
  const worker = new Worker(new URL('./office-preview.worker.ts', import.meta.url), { type: 'module' });
  const timeout = window.setTimeout(() => finish(new Error('文件解析超时，请稍后重试')), OFFICE_PREVIEW_TIMEOUT_MS);
  const cleanup = () => {
   window.clearTimeout(timeout);
   signal.removeEventListener('abort', abort);
   worker.terminate();
  };
  const finish = (error?: Error, result?: OfficePreviewResult) => {
   cleanup();
   if (error) reject(error);
   else if (result) resolve(result);
   else reject(new Error('Office 预览响应无效，请重新加载'));
  };
  const abort = () => finish(new DOMException('预览已取消', 'AbortError'));
  worker.onmessage = event => {
   const result = event.data as OfficePreviewResult | { error: string };
   if ('error' in result) finish(new Error(result.error));
   else finish(undefined, result);
  };
  worker.onerror = () => finish(new Error('Office 文件暂时无法预览'));
  signal.addEventListener('abort', abort, { once: true });
  worker.postMessage({ name, blob });
 });
}

export function officePreviewDocument(html: string) {
 const parsed = new DOMParser().parseFromString(html, 'text/html');
 // The generated workbook normally hides inactive sheets and switches them with JavaScript.
 // Expose every sheet as a named section, keeping the preview completely script-free.
 const tabs = new Map(Array.from(parsed.querySelectorAll('.spreadsheet-tab')).map(tab => [tab.getAttribute('href')?.slice(1), tab.textContent]));
 parsed.querySelectorAll('.spreadsheet-sheet').forEach((sheet, index) => {
  const heading = parsed.createElement('h2');
  heading.className = 'storage-sheet-heading';
  heading.textContent = tabs.get(sheet.id) || `工作表 ${index + 1}`;
  sheet.prepend(heading);
 });
 parsed.querySelectorAll('script,iframe,object,embed,form,base,meta,link').forEach(node => node.remove());
 parsed.querySelectorAll('*').forEach(node => {
  for (const attribute of Array.from(node.attributes)) {
   if (/^on/i.test(attribute.name) || attribute.name === 'srcdoc') node.removeAttribute(attribute.name);
  }
 });
 parsed.querySelectorAll('a').forEach(link => {
  if (!link.getAttribute('href')?.startsWith('#')) link.removeAttribute('href');
  link.removeAttribute('target');
 });
 return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; script-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"><style>
*{box-sizing:border-box}html{background:#fff;color:#20242b;font:14px/1.65 system-ui,-apple-system,"Segoe UI",sans-serif}body{margin:0;padding:24px;overflow:auto;overflow-wrap:anywhere}img,svg{max-width:100%;height:auto}table{max-width:100%;border-collapse:collapse}td,th{border:1px solid #d5d9e0;padding:5px 8px;vertical-align:top}pre{max-width:100%;overflow:auto;white-space:pre-wrap}a{color:#245bdb}
</style>${parsed.head.innerHTML}</head><body>${parsed.body.innerHTML}<style>
body{margin:0;padding:16px}.document-container{padding:8px;max-width:900px;margin:auto}.spreadsheet-container{height:auto;overflow:visible}.spreadsheet-sheet,.spreadsheet-sheet.active{display:block;height:auto;flex:none;overflow:auto;margin:0 0 24px;scroll-margin-top:12px}.spreadsheet-tabs{flex-wrap:wrap;flex-shrink:0;height:auto}.storage-sheet-heading{font:600 16px/1.5 system-ui;margin:12px 0}.resize-handle,.column-resize-handle,.row-resize-handle{display:none!important}.spreadsheet-table{min-width:0}.slide{max-width:100%;overflow:auto}
@media(max-width:600px){body{padding:8px}.document-container{padding:4px}td,th{padding:4px 6px}}
</style></body></html>`;
}
