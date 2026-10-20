import { inspectOfficeFile, officePreviewError } from './office-file';

type PreviewRequest = { name: string; blob: Blob };

self.addEventListener('message', async event => {
 const { name, blob } = (event as MessageEvent<PreviewRequest>).data;
 try {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let fallback;
  try { fallback = inspectOfficeFile(name, bytes); }
  catch (error) { self.postMessage({ error: (error as Error).message }); return; }
  if (fallback) { self.postMessage(fallback); return; }
  Object.defineProperty(globalThis, 'window', { value: globalThis, configurable: true });
  const { OfficeParser } = await import('officeparser/slim');
  const document = await OfficeParser.parseOffice(new File([blob], name), {
   extractAttachments: true,
   decompressionLimits: {
    maxUncompressedBytes: 64 * 1024 * 1024,
    maxZipEntries: 2000,
    maxTableCells: 100_000,
    maxXmlElements: 250_000,
    maxRepeatedContent: 4 * 1024 * 1024,
    maxRawContentLength: 0,
   },
  });
  const { value } = await document.to('html', {
   includeFormatting: true,
   htmlConfig: { standalone: { document: false, styles: 'full', scripts: false, headInjections: false, bodyInjections: false } },
  });
  if (typeof value !== 'string' || value.length > 6 * 1024 * 1024) {
   self.postMessage({ error: 'Office 文件内容过多，暂不能预览' });
   return;
  }
  self.postMessage({ kind: 'office', html: value });
 } catch (error) {
  self.postMessage({ error: officePreviewError(error) });
 }
});
