import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';

const exec = promisify(execFile);
test('hardened runtime converts Chinese Office documents with bundled fonts', { skip: !process.env.HC_RUNTIME_IMAGE, timeout: 90000 }, async () => {
  // A fresh container must work without user fonts or an external Office service.
  const script = `
    import { zipSync, strToU8 } from 'fflate';
    import { writeFile, readFile } from 'node:fs/promises';
    import { createConverter } from '@deepseek-ai/libreoffice-kit';
    const xml = {
      '[Content_Types].xml': '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
      '_rels/.rels': '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
      'word/document.xml': '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:rPr><w:rFonts w:ascii="Liberation Sans" w:eastAsia="WenQuanYi Zen Hei"/></w:rPr><w:t>中文文档预览 English 123</w:t></w:r></w:p><w:sectPr/></w:body></w:document>',
    };
    await writeFile('/tmp/source.docx', zipSync(Object.fromEntries(Object.entries(xml).map(([name, content]) => [name, strToU8(content)]))));
    const converter = await createConverter({ timeoutMs: 60000 });
    try {
      const result = await converter.render({ inputPath: '/tmp/source.docx', outputPath: '/tmp/preview.pdf' });
      const pdf = await readFile('/tmp/preview.pdf');
      console.log(JSON.stringify({ signature: pdf.subarray(0, 5).toString(), embeddedFonts: pdf.includes(Buffer.from('/FontFile2')), unicodeMap: pdf.includes(Buffer.from('/ToUnicode')), missingFonts: result.missingFonts }));
    } finally { await converter.dispose(); }
  `;
  const name = 'hc-office-contract-' + randomUUID();
  try {
    const { stdout } = await exec('docker', [
      'run', '--rm', '--name', name, '--read-only', '--network', 'none', '--cap-drop', 'ALL',
      '--security-opt', 'no-new-privileges', '--pids-limit', '256', '--memory', '2g', '--cpus', '1',
      '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000',
      '--entrypoint', 'node', process.env.HC_RUNTIME_IMAGE, '--input-type=module', '-e', script,
    ], { timeout: 80000, maxBuffer: 1024 * 1024 });
    assert.deepEqual(JSON.parse(stdout.trim()), { signature: '%PDF-', embeddedFonts: true, unicodeMap: true, missingFonts: [] });
  } finally {
    await exec('docker', ['rm', '-f', name], { timeout: 5000 }).catch(() => {});
  }
});
