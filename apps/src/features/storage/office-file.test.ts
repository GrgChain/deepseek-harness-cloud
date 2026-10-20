import { describe, expect, it } from 'vitest';
import { inspectOfficeFile, officePreviewError } from './office-file';

describe('Office file content checks', () => {
 it('shows the reported UTF-8 placeholder as text with an explicit format warning', () => {
  const text = '此文件使用占位文本创建，稍后将由生成工具填充。';
  const result = inspectOfficeFile('AI-Agent技术研究报告_2026-10.docx', new TextEncoder().encode(text));
  expect(result).toMatchObject({ kind: 'text', text });
  expect(result?.kind === 'text' && result.notice).toContain('与 .docx 扩展名不符');
 });
 it('leaves ZIP documents and encrypted compound documents to the Office parser', () => {
  for (const header of [[0x50, 0x4b, 3, 4], [0x50, 0x4b, 5, 6], [0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]])
   expect(inspectOfficeFile('report.docx', new Uint8Array(header))).toBeNull();
 });
 it('rejects empty, invalid UTF-8 and binary files rather than showing meaningless text', () => {
  expect(() => inspectOfficeFile('report.docx', new Uint8Array())).toThrow('文件为空');
  for (const bytes of [new Uint8Array([0xff, 0xfe, 0xfd]), new TextEncoder().encode('binary\0data'), new Uint8Array([0x50, 0x4b, 0, 0])])
   expect(() => inspectOfficeFile('report.docx', bytes)).toThrow('Office 格式不符');
 });
 it('does not decode large non-Office input as text', () => {
  expect(() => inspectOfficeFile('report.docx', new Uint8Array(512 * 1024 + 1).fill(65))).toThrow('Office 格式不符');
 });
 it('treats markup as literal text, without generating HTML', () => {
  expect(inspectOfficeFile('report.docx', new TextEncoder().encode('<script>alert(1)</script>'))).toMatchObject({ kind: 'text', text: '<script>alert(1)</script>' });
 });
 it('distinguishes encryption, damaged archives, mismatched formats and processing limits', () => {
  const message = (code: string) => officePreviewError({ officeIssue: { code } });
  expect(message('PASSWORD_REQUIRED')).toContain('已加密');
  expect(message('ZIP_TRUNCATED')).toContain('内容不完整');
  expect(message('REQUIRED_PART_MISSING')).toContain('缺少必需文件');
  expect(message('BUFFER_TYPE_MISMATCH')).toContain('扩展名不符');
  expect(message('XML_ELEMENT_LIMIT_EXCEEDED')).toContain('处理上限');
  expect(officePreviewError(new Error('private-parser-details'))).not.toContain('private-parser-details');
 });
});
