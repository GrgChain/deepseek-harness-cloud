import {expect,test} from '@playwright/test';
import {strToU8,zipSync} from 'fflate';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync,writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

const id='b3e2b3e2-1111-4111-8111-111111111111';
const user={id:'user-context',memberships:[{org_id:'org',role:'MEMBER',role_id:'member',role_name:'成员',status:'ACTIVE'}],permissions:['knowledge:read','knowledge:manage'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const connection={id,kind:'knowledge',backend:'milvus',endpoint_id:'milvus',name:'测试知识库',version:1,enabled:true,status:'READY',capabilities:{search:true,write:true},verified_at:'2026-10-23T08:00:00Z',updated_at:'2026-10-23T08:00:00Z',integration:'NOT_CONNECTED'};
const zip=(parts:Record<string,string>)=>Buffer.from(zipSync(Object.fromEntries(Object.entries(parts).map(([path,text])=>[path,strToU8(text)]))));
const docx=zip({
 '[Content_Types].xml':'<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
 '_rels/.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
 'word/document.xml':'<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Office document sample</w:t></w:r></w:p><w:sectPr/></w:body></w:document>',
});
const xlsx=zip({
 '[Content_Types].xml':'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>',
 '_rels/.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
 'xl/workbook.xml':'<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet" sheetId="1" r:id="rId1"/></sheets></workbook>',
 'xl/_rels/workbook.xml.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>',
 'xl/worksheets/sheet1.xml':'<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Spreadsheet sample</t></is></c></row></sheetData></worksheet>',
});
const pptx=zip({
 '[Content_Types].xml':'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/></Types>',
 '_rels/.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>',
 'ppt/presentation.xml':'<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>',
 'ppt/_rels/presentation.xml.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>',
 'ppt/slides/slide1.xml':'<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr/><p:grpSpPr/><p:sp><p:nvSpPr/><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Presentation sample</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>',
});
function pdfFixture(){
 const text='BT /F1 24 Tf 40 200 Td (PDF document sample) Tj ET';
 const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R] /Count 1 >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 360 260] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',`<< /Length ${text.length} >>\nstream\n${text}\nendstream`,'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
 let content='%PDF-1.7\n';const offsets=[0];objects.forEach((object,index)=>{offsets.push(Buffer.byteLength(content));content+=`${index+1} 0 obj\n${object}\nendobj\n`;});const start=Buffer.byteLength(content);content+=`xref\n0 ${objects.length+1}\n0000000000 65535 f \n${offsets.slice(1).map(offset=>`${String(offset).padStart(10,'0')} 00000 n \n`).join('')}trailer\n<< /Size ${objects.length+1} /Root 1 0 R >>\nstartxref\n${start}\n%%EOF\n`;return Buffer.from(content);
}
test('one document command accepts text, Office, PDF and image content',async({page},info)=>{
 test.setTimeout(120000);
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',r=>r.fulfill({json:user}));
 await page.route('**/v1/context/knowledge',r=>r.fulfill({json:{items:[connection],endpoints:[],bindings:[],integration:'NOT_CONNECTED'}}));
 await page.route(`**/v1/context/knowledge/${id}/list`,r=>r.fulfill({json:{items:[]}}));
 await page.route('**/v1/context/knowledge/parse',r=>{const request=r.request().postData()??'';if(request.includes('broken.pdf'))return r.fulfill({status:422,json:{code:'CONTEXT_INVALID',message:'文档解析失败，请检查文件格式及内容'}});const content=request.includes('note.txt')?'Plain text sample':request.includes('report.docx')?'Office document sample':request.includes('table.xlsx')?'Spreadsheet sample':request.includes('slides.pptx')?'Presentation sample':request.includes('report.pdf')?'PDF document sample':request.includes('chinese.png')?'项目计划和工作安排':'DOCUMENT TEST 123';return r.fulfill({json:{content}});});
 let previews=0;
 await page.route(`**/v1/context/knowledge/${id}/preview`,r=>{previews++;const input=r.request().postDataJSON();expect(input.content).toContain('Office document sample');return r.fulfill({json:{token:'fixture',items:[{id:'doc',title:input.title,content:input.content,source:input.source,conflict:false}],warnings:[],data_use:'测试服务'}});});
 await page.goto('/app/knowledge');await page.getByRole('button',{name:'管理内容'}).click();
 await expect(page.getByRole('button',{name:'导入文本'})).toHaveCount(0);
 const image=await page.evaluate(()=>{const c=document.createElement('canvas');c.width=700;c.height=160;const x=c.getContext('2d')!;x.fillStyle='white';x.fillRect(0,0,c.width,c.height);x.font='bold 56px Arial';x.fillStyle='black';x.fillText('DOCUMENT TEST 123',25,100);return c.toDataURL('image/png').split(',')[1];});
 const chineseImage=await page.evaluate(()=>{const c=document.createElement('canvas');c.width=700;c.height=160;const x=c.getContext('2d')!;x.fillStyle='white';x.fillRect(0,0,c.width,c.height);x.font='bold 60px "Noto Sans CJK SC"';x.fillStyle='black';x.fillText('项目计划和工作安排',25,100);return c.toDataURL('image/png').split(',')[1];});
 for(const file of [{name:'note.txt',buffer:Buffer.from('Plain text sample'),expected:'Plain text sample'},{name:'report.docx',buffer:docx,expected:'Office document sample'},{name:'table.xlsx',buffer:xlsx,expected:'Spreadsheet sample'},{name:'slides.pptx',buffer:pptx,expected:'Presentation sample'},{name:'report.pdf',buffer:pdfFixture(),expected:'PDF document sample'},{name:'photo.png',buffer:Buffer.from(image,'base64'),expected:'DOCUMENT TEST 123'},{name:'chinese.png',buffer:Buffer.from(chineseImage,'base64'),expected:'项目计划'},{name:'a-very-long-document-file-name-for-small-screen-layout-check-12345678901234567890.txt',buffer:Buffer.from('test'),expected:'DOCUMENT TEST 123'}]){
  await page.getByRole('button',{name:'添加文档',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加文档'});
  await editor.getByText('选择文件',{exact:true}).first().click();
  await expect(editor.getByLabel('选择文档文件')).toBeHidden();await expect(editor.getByLabel('内容',{exact:true})).toHaveCount(0);
  if(file.name==='note.txt')await page.screenshot({path:info.outputPath('document-file-empty.png')});
  await editor.getByLabel('选择文档文件').setInputFiles({name:file.name,mimeType:'application/octet-stream',buffer:file.buffer});
  await expect(editor.getByLabel('内容',{exact:true})).toHaveValue(new RegExp(file.expected),{timeout:90000});
  await expect(editor.getByRole('button',{name:'更换文件'})).toBeVisible();
  await expect(editor.getByRole('button',{name:/^预\s*览$/})).toBeEnabled();
  expect(await editor.locator('.context-document-upload').evaluate(element=>element.scrollWidth<=element.clientWidth)).toBe(true);
  if(file.name==='note.txt')await page.screenshot({path:info.outputPath('document-file-selected.png')});
  expect(previews).toBe(0);
  if(file.name==='report.docx'){await editor.getByRole('button',{name:/^预\s*览$/}).click();await expect(page.getByRole('dialog',{name:'确认保存文档'})).toBeVisible();await page.getByRole('dialog',{name:'确认保存文档'}).getByRole('button',{name:/取\s*消/}).click();expect(previews).toBe(1);previews=0;}
  else await editor.getByRole('button',{name:/^取\s*消$/}).click();
 }
 await page.getByRole('button',{name:'添加文档',exact:true}).click();const editor=page.getByRole('dialog',{name:'添加文档'});
 await editor.getByText('选择文件',{exact:true}).first().click();
 await editor.getByLabel('选择文档文件').setInputFiles({name:'broken.pdf',mimeType:'application/pdf',buffer:Buffer.from('not a pdf')});
 await expect(editor.getByText('文档解析失败，请检查文件格式及内容')).toBeVisible();
 await expect(editor.getByRole('button',{name:/^预\s*览$/})).toBeDisabled();
});

test('Docling parses real document and OCR fixtures',async({page,request})=>{
 test.skip(process.env.HC_TEST_DOCLING_LIVE!=='true','requires local Docling service');
 test.setTimeout(300000);
 const image=await page.evaluate(()=>{const c=document.createElement('canvas');c.width=900;c.height=180;const x=c.getContext('2d')!;x.fillStyle='white';x.fillRect(0,0,c.width,c.height);x.font='bold 64px "Noto Sans CJK SC"';x.fillStyle='black';x.fillText('项目计划和工作安排',25,110);return c.toDataURL('image/png').split(',')[1];});
 const realPptx=execFileSync('python3',['-c','import sys; from pptx import Presentation; p=Presentation(); s=p.slides.add_slide(p.slide_layouts[1]); s.shapes.title.text="Presentation sample"; p.save(sys.stdout.buffer)']);
 const temp=mkdtempSync(join(tmpdir(),'hc-docling-test-'));let legacy:Buffer;
 try{const modern=execFileSync('python3',['-c','import sys; from docx import Document; d=Document(); d.add_paragraph("Legacy document sample"); d.save(sys.stdout.buffer)']);writeFileSync(join(temp,'legacy.docx'),modern);execFileSync('libreoffice',['--headless',`-env:UserInstallation=file://${temp}/profile`,'--convert-to','doc:MS Word 97','--outdir',temp,join(temp,'legacy.docx')]);legacy=readFileSync(join(temp,'legacy.doc'));}finally{rmSync(temp,{recursive:true,force:true});}
 for(const file of [{name:'note.md',buffer:Buffer.from('Plain text sample'),expected:'Plain text sample'},{name:'report.docx',buffer:docx,expected:'Office document sample'},{name:'legacy.doc',buffer:legacy,expected:'Legacy document sample'},{name:'table.xlsx',buffer:xlsx,expected:'Spreadsheet sample'},{name:'slides.pptx',buffer:realPptx,expected:'Presentation sample'},{name:'report.pdf',buffer:pdfFixture(),expected:'PDF document sample'},{name:'chinese.png',buffer:Buffer.from(image,'base64'),expected:'项目计划'}]){
  const response=await request.post('http://127.0.0.1:18582/v1/convert/file',{timeout:240000,multipart:{files:{name:file.name,mimeType:'application/octet-stream',buffer:file.buffer},to_formats:'md',do_ocr:'true',ocr_engine:'rapidocr',ocr_lang:'ch',image_export_mode:'placeholder'}});
  expect(response.status(),file.name).toBe(200);const result=await response.json();expect(result.status,file.name).toBe('success');expect(result.document.md_content.replace(/\s/g,''),file.name).toContain(file.expected.replace(/\s/g,''));
 }
});
