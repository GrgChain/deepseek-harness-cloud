import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Modal, Spin } from 'antd';
import { Download } from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { APIError } from '../../api/error';
import type { StorageClient } from '../../api/storage';
import { expireSession } from '../../shared/hooks/use-expired-session';
import { isOfficePreviewable, OFFICE_PREVIEW_MAX_BYTES, officePreviewDocument, previewOfficeFile } from './office-preview';
import { recoverOutdatedPreview, reloadFilePreview } from './preview-recovery';

class PreviewViewerLoadError extends Error {}

export type PreviewFile = { name: string; root: 'workspace' | 'dsh'; path: string };
export function filePreviewKind(name: string) {
 return /\.pdf$/i.test(name) ? 'pdf' : isOfficePreviewable(name) ? 'office' : 'text';
}
export function filePreviewLimit(name: string) {
 return { pdf: 32 * 1024 * 1024, office: OFFICE_PREVIEW_MAX_BYTES, text: 512 * 1024 }[filePreviewKind(name)];
}
export function downloadFile(blob: Blob, name: string) {
 const url = URL.createObjectURL(blob);
 const anchor = document.createElement('a');
 anchor.href = url;
 anchor.download = name;
 anchor.click();
 window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
function LoadingPreview() {
 return <div className="storage-preview-loading" role="status"><Spin/><span>正在加载预览…</span></div>;
}
export function FilePreviewModal({ file, assetId, client, onClose }: {
 file: PreviewFile; assetId: string; client: StorageClient; onClose: () => void;
}) {
 const cache = useQueryClient();
 const kind = filePreviewKind(file.name);
 const [attempt, setAttempt] = useState(0);
 const [result, setResult] = useState<{ blob: Blob; kind: 'office' | 'pdf' | 'text'; content: string; notice?: string; PDFPreview?: typeof import('./pdf-preview').default } | null>(null);
 const [source, setSource] = useState<Blob | null>(null);
 const [error, setError] = useState('');
 const [viewerFailed, setViewerFailed] = useState(false);
 const fail = useCallback((message: string) => setError(message), []);
 useEffect(() => {
  const request = new AbortController();
  setResult(null); setSource(null); setError(''); setViewerFailed(false);
  void (async () => {
   try {
    const [{ blob }, pdf] = await Promise.all([
     client.file(assetId, file.root, file.path, kind === 'text' ? 'PREVIEW' : 'DOWNLOAD', request.signal).then(value => {
      if (value.blob.size > filePreviewLimit(file.name)) throw new Error('文件超过预览大小上限，请下载查看');
      if (!request.signal.aborted) setSource(value.blob);
      return value;
     }),
     kind === 'pdf' ? import('./pdf-preview').catch(async () => {
      if (request.signal.aborted) return null;
      if (await recoverOutdatedPreview(assetId, file, request.signal)) return null;
      throw new PreviewViewerLoadError('查看器资源加载失败，请刷新页面后重试；也可下载原文件查看');
     }) : Promise.resolve(null),
    ]);
    if (blob.size > filePreviewLimit(file.name)) throw new Error('文件超过预览大小上限，请下载查看');
    if (request.signal.aborted) return;
    if (kind === 'pdf' && !pdf) return;
    setSource(blob);
    if (kind === 'office') {
     const office = await previewOfficeFile(file.name, blob, request.signal);
     if (!request.signal.aborted) setResult(office.kind === 'office' ? { blob, kind: 'office', content: officePreviewDocument(office.html) } : { blob, kind: 'text', content: office.text, notice: office.notice });
    } else {
     const content = kind === 'text' ? await blob.text() : '';
     if (!request.signal.aborted) setResult({ blob, kind, content, PDFPreview: pdf?.default });
    }
   } catch (cause) {
    if (request.signal.aborted) return;
    if (cause instanceof APIError && cause.status === 401) expireSession(cache);
    setViewerFailed(cause instanceof PreviewViewerLoadError);
    setError(cause instanceof Error ? cause.message : '文件读取失败，请重试');
   }
  })();
  return () => request.abort();
 }, [assetId, file.root, file.path, file.name, kind, client, cache, attempt]);
 return <Modal className="storage-preview-modal" title={file.name} open footer={null} destroyOnHidden onCancel={onClose} width={kind === 'text' ? 800 : 1120}>
  <div className="storage-preview-toolbar"><span>{(result?.kind ?? kind) === 'office' ? '内容预览 · 复杂版式或图表请下载查看' : kind === 'pdf' ? 'PDF 预览' : '文本预览'}</span>
   <Button icon={<Download size={15}/>} disabled={!source} onClick={() => source && downloadFile(source, file.name)}>下载原文件</Button>
  </div>
  {result?.notice && <Alert className="storage-preview-notice" type="warning" showIcon message={result.notice}/>}
  {error ? <Alert type="error" showIcon message="无法预览此文件" description={error} action={<Button onClick={() => viewerFailed ? reloadFilePreview(assetId, file) : setAttempt(value => value + 1)}>{viewerFailed ? '刷新页面' : '重新加载'}</Button>}/> : !result ? <LoadingPreview/> : result.kind === 'office' ?
   <iframe className="storage-office-preview" title={`${file.name} 预览`} sandbox="" referrerPolicy="no-referrer" srcDoc={result.content}/> : result.PDFPreview ?
    <result.PDFPreview key={attempt} blob={result.blob} onError={fail}/> :
    <pre className="storage-file-preview">{result.content || '（空文件）'}</pre>}
 </Modal>;
}
