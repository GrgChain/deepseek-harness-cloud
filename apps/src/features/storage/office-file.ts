export type OfficePreviewResult = { kind: 'office'; html: string } | { kind: 'text'; text: string; notice: string };
const TEXT_FALLBACK_MAX_BYTES = 512 * 1024;

export function inspectOfficeFile(name: string, bytes: Uint8Array): OfficePreviewResult | null {
 const zip = bytes[0] === 0x50 && bytes[1] === 0x4b &&
  (bytes[2] === 3 && bytes[3] === 4 || bytes[2] === 5 && bytes[3] === 6 || bytes[2] === 7 && bytes[3] === 8);
 const compound = [0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1].every((byte, index) => bytes[index] === byte);
 if (zip || compound) return null;
 if (!bytes.length) throw new Error('文件为空，尚无可预览的文档内容');
 if (bytes.length <= TEXT_FALLBACK_MAX_BYTES) {
  let text: string | undefined;
  try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); } catch { /* Binary files must never be shown as text. */ }
  if (text !== undefined && !/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(text)) {
   const extension = name.split('.').at(-1)?.toLowerCase() ?? 'Office';
   return { kind: 'text', text, notice: `文件内容是纯文本，与 .${extension} 扩展名不符。以下按文本显示；正式文档可能尚未生成。` };
  }
 }
 throw new Error('文件内容与 Office 格式不符，请确认文档已生成完成且扩展名正确');
}

export function officePreviewError(error: unknown): string {
 const code = (error as { officeIssue?: { code?: string } } | null)?.officeIssue?.code;
 if (code === 'PASSWORD_REQUIRED' || code === 'PASSWORD_INCORRECT' || code === 'DOCUMENT_DECRYPTION_FAILED')
  return '此 Office 文件已加密或无法解密，请下载后使用办公软件打开';
 if (code === 'ZIP_TRUNCATED' || code === 'ZIP_NO_ENTRIES_FOUND' || code === 'REQUIRED_PART_MISSING')
  return '文档内容不完整或缺少必需文件，请重新生成或保存文档后再试';
 if (code === 'FILE_TYPE_DETECTION_FAILED' || code === 'BUFFER_TYPE_MISMATCH')
  return '文件内容与扩展名不符，请确认文件格式或重新保存文档';
 if (code?.includes('LIMIT') || code === 'OUTPUT_TOO_LARGE')
  return '文档内容过多，超过预览处理上限，请下载查看';
 return 'Office 文档解析失败，请下载检查文件，或重新保存后再试';
}
