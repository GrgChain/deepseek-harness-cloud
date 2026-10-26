import {expect,test,type Page} from '@playwright/test';
import {strToU8,zipSync} from 'fflate';
const owner='00000000-0000-4000-8000-000000000041';
const user={id:owner,memberships:[{org_id:'00000000-0000-4000-8000-000000000040',role:'MEMBER',role_id:'00000000-0000-4000-8000-000000000004',role_name:'成员',status:'ACTIVE'}],permissions:['instances:read'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const goodBackup={id:'backup-good',status:'SUCCEEDED',created_at:'2026-10-15T01:00:00Z',completed_at:'2026-10-15T01:01:00Z',verified_at:'2026-10-15T01:01:00Z',expires_at:null,retention_policy:'MANUAL'};
const base={id:'00000000-0000-4000-8000-000000000081',owner_id:owner,instance_id:'00000000-0000-4000-8000-000000000061',workspace_name:'测试',workspace_state:'DELETED',capacity_bytes:10*2**30,status:'RETAINED',created_at:'2026-10-01T00:00:00Z',deleted_at:'2026-10-02T00:00:00Z',retention_until:'2026-10-09T00:00:00Z',legal_hold:true,retention_expired:true,usage:{status:'UNSAMPLED',used_bytes:null,available_bytes:null,sampled_at:null,attempted_at:null},latest_backup:{...goodBackup,id:'backup-failed',status:'FAILED',verified_at:null},latest_verified_backup:goodBackup,maintenance:{backup_blockers:[],restore_blockers:['TARGET_NOT_STOPPED','LEGAL_HOLD'],reclaim_blockers:['LEGAL_HOLD']}};
const second={...base,id:'00000000-0000-4000-8000-000000000082',workspace_name:'运行数据',workspace_state:'RUNNING',status:'READY',legal_hold:false,deleted_at:null,retention_until:null,retention_expired:false,latest_backup:null,latest_verified_backup:null,usage:{status:'STALE',used_bytes:0,available_bytes:9*2**30,sampled_at:'2026-10-15T00:00:00Z',attempted_at:'2026-10-15T00:00:00Z'}};
const officeZip=(parts:Record<string,string>)=>zipSync(Object.fromEntries(Object.entries(parts).map(([path,content])=>[path,strToU8(content)])),{level:0});
const docxPreviewFixture=officeZip({
 '[Content_Types].xml':'<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
 '_rels/.rels':'<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
 'word/document.xml':'<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Office Preview Sample</w:t></w:r></w:p><w:sectPr/></w:body></w:document>',
});
const xlsxPreviewFixture=officeZip({
 '[Content_Types].xml':'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>',
 '_rels/.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
 'xl/workbook.xml':'<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Preview" sheetId="1" r:id="rId1"/><sheet name="Second sheet" sheetId="2" r:id="rId2"/></sheets></workbook>',
 'xl/_rels/workbook.xml.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/></Relationships>',
 'xl/worksheets/sheet1.xml':'<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Spreadsheet Preview Sample</t></is></c></row></sheetData></worksheet>',
 'xl/worksheets/sheet2.xml':'<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Second Sheet Content</t></is></c></row></sheetData></worksheet>',
});
const pptxPreviewFixture=officeZip({
 '[Content_Types].xml':'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/></Types>',
 '_rels/.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>',
 'ppt/presentation.xml':'<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>',
 'ppt/_rels/presentation.xml.rels':'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>',
 'ppt/slides/slide1.xml':'<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr/><p:grpSpPr/><p:sp><p:nvSpPr/><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>Presentation Preview Sample</a:t></a:r><a:endParaRPr/></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>',
});
function pdfPreviewFixture() {
 const stream=(text:string)=>{const content=`BT /F1 24 Tf 40 200 Td (${text}) Tj ET`;return `<< /Length ${content.length} >>\nstream\n${content}\nendstream`;};
 const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>',
  '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 360 260] /Resources << /Font << /F1 7 0 R >> >> /Contents 4 0 R >>',stream('PDF First Page'),
  '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 360 260] /Resources << /Font << /F1 7 0 R >> >> /Contents 6 0 R >>',stream('PDF Second Page'),
  '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
 let content='%PDF-1.7\n';const offsets=[0];
 objects.forEach((object,index)=>{offsets.push(Buffer.byteLength(content));content+=`${index+1} 0 obj\n${object}\nendobj\n`;});
 const start=Buffer.byteLength(content);content+=`xref\n0 ${objects.length+1}\n0000000000 65535 f \n${offsets.slice(1).map(offset=>`${String(offset).padStart(10,'0')} 00000 n \n`).join('')}trailer\n<< /Size ${objects.length+1} /Root 1 0 R >>\nstartxref\n${start}\n%%EOF\n`;
 return Buffer.from(content);
}
function openDocumentFixture(kind:'text'|'spreadsheet'|'presentation',body:string) {
 return officeZip({mimetype:`application/vnd.oasis.opendocument.${kind}`,
  'META-INF/manifest.xml':`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"><manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.${kind}"/><manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/></manifest:manifest>`,
  'content.xml':`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" office:version="1.2"><office:body><office:${kind}>${body}</office:${kind}></office:body></office:document-content>`});
}
async function setup(page:Page,permissions=user.permissions){
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions}}));
 await page.route('**/v1/storage-assets?*',r=>r.fulfill({json:{items:[base,second],total:2,next_cursor:null}}));
 await page.route('**/v1/storage-assets/'+base.id,r=>r.fulfill({json:base}));
 await page.route('**/v1/storage-assets/'+base.id+'/backups?*',r=>r.fulfill({json:{items:[base.latest_backup,goodBackup],total:2,next_cursor:null}}));
}
test('storage shows retained assets and actual use separately with responsive detail and maintenance guidance',async({page},info)=>{
 await setup(page);const writes:string[]=[];page.on('request',r=>{if(r.url().includes('/v1/storage-assets')&&r.method()!=='GET')writes.push(r.method());});
 await page.goto('/app/storage');await expect(page.getByRole('heading',{name:'数据资产',exact:true})).toBeVisible();
 const retained=page.getByRole('article',{name:'数据资产 测试',exact:true});
 await expect(retained).toContainText('10 GiB');await expect(retained).toContainText('未采集');await expect(retained).toContainText('保留已到期，尚未回收');await expect(retained).not.toContainText('备份失败');await expect(retained).not.toContainText('最近备份');
 const running=page.getByRole('article',{name:'数据资产 运行数据',exact:true});await expect(running).toContainText('0 GiB 已用');await expect(running).toContainText('9 GiB 可用 · 待刷新');
 await retained.getByRole('button',{name:'查看测试的数据详情'}).click();
 const drawer=page.getByRole('dialog',{name:'数据资产详情'});await expect(drawer).toBeVisible();await expect(drawer).toContainText('最近有效备份：');await expect(drawer.getByText('资产标识',{exact:true})).toHaveCount(0);await drawer.getByRole('button',{name:'更多信息'}).click();await drawer.getByRole('button',{name:'维护条件'}).click();await expect(drawer).toContainText('受法律保留保护');await expect(drawer).toContainText('恢复后不自动启动');
 await expect(drawer.getByRole('article',{name:'备份记录'})).toHaveCount(2);
 await expect(drawer.getByText(base.id,{exact:true})).toBeVisible();await expect(drawer.getByText('未采集',{exact:true})).toBeVisible();
 await expect(drawer.getByRole('button',{name:'永久回收',exact:true})).toHaveCount(0);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:info.outputPath('storage-details.png'),fullPage:true,animations:'disabled'});
 await drawer.getByRole('button',{name:'关闭',exact:true}).click();
 await page.screenshot({path:info.outputPath('storage-list.png'),fullPage:true,animations:'disabled'});
 expect(writes).toEqual([]);
});
test('asset name opens the directory tree and browsing stays read-only',async({page},info)=>{
 await setup(page);
 const longDirectory='documents-with-a-very-long-name-that-must-stay-on-one-line';
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>{
  const query=new URL(route.request().url()).searchParams;const root=query.get('root') as 'workspace'|'dsh';const path=query.get('path')??'';
  const entries=path===''?[{name:'docs',type:'DIRECTORY',size_bytes:0,modified_at:'2026-10-15T00:00:00Z'},{name:longDirectory,type:'DIRECTORY',size_bytes:0,modified_at:'2026-10-15T00:00:00Z'}]:path==='docs'?[{name:'readme.md',type:'FILE',size_bytes:24,modified_at:'2026-10-15T00:00:00Z'}]:[];
  return route.fulfill({json:{root,path,entries}});
 });
 const writes:string[]=[];page.on('request',request=>{if(request.url().includes('/v1/storage-assets')&&request.method()!=='GET')writes.push(request.method());});
 await page.goto('/app/storage');
 await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();
 await expect(page).toHaveURL(`/app/storage/${base.id}/browse`);
 await expect(page.getByRole('main',{name:'数据浏览'})).toBeVisible();
 await expect(page.getByRole('heading',{name:'测试',exact:true})).toBeVisible();
 await expect(page.locator('.storage-browser-meta')).toContainText('已分配');await expect(page.locator('.storage-browser-meta')).toContainText('10 GiB');await expect(page.locator('.storage-browser-meta')).toContainText('未采集');
 const tree=page.locator('.storage-file-tree');
 await expect(tree.getByRole('heading',{name:'目录',exact:true})).toBeVisible();
 const treeItems=tree.getByRole('treeitem');
 await expect(treeItems).toHaveCount(5);
 const dataRoot=treeItems.nth(0);
 await expect(dataRoot).toBeVisible();await expect(dataRoot).toHaveAttribute('aria-expanded','true');
 await expect(dataRoot).toHaveAccessibleName(/\/data$/);
 const workspaceRoot=treeItems.nth(1);
 await expect(workspaceRoot).toBeVisible();
 await expect(workspaceRoot).toHaveAccessibleName(/\/data\/workspace$/);
 const workspaceTitle=workspaceRoot.locator('.storage-tree-title');
 await expect(workspaceTitle).toHaveCSS('white-space','nowrap');
 await expect(workspaceRoot.locator('.ant-tree-node-content-wrapper')).toHaveCSS('min-height',await page.evaluate(()=>innerWidth<=767?'36px':'30px'));
 await expect(workspaceRoot.locator('.ant-tree-indent-unit')).toHaveCSS('width','18px');
 const arrowIconGap=await workspaceRoot.evaluate(node=>{const arrow=node.querySelector('.ant-tree-switcher')?.getBoundingClientRect();const icon=node.querySelector('.ant-tree-iconEle')?.getBoundingClientRect();return arrow&&icon?icon.left-arrow.right:Infinity;});
 expect(arrowIconGap).toBeLessThanOrEqual(1);
 const iconAndTitleAlign=await workspaceRoot.evaluate(node=>{const icon=node.querySelector('.ant-tree-iconEle')?.getBoundingClientRect();const title=node.querySelector('.storage-tree-title')?.getBoundingClientRect();return Boolean(icon&&title&&Math.abs(icon.top+icon.height/2-title.top-title.height/2)<2&&title.height<=36);});
 expect(iconAndTitleAlign).toBe(true);
 const longTitle=tree.locator('.storage-tree-title').filter({hasText:longDirectory});
 await expect(longTitle).toHaveCSS('white-space','nowrap');await expect(longTitle).toHaveCSS('text-overflow','clip');
 const treeScroll=await tree.evaluate(element=>{element.scrollLeft=element.scrollWidth;const title=element.querySelector('.storage-tree-title[title*="documents-with-a-very-long-name"]')?.getBoundingClientRect();const heading=element.querySelector('h3')?.getBoundingClientRect();const bounds=element.getBoundingClientRect();return {scrollWidth:element.scrollWidth,clientWidth:element.clientWidth,scrollLeft:element.scrollLeft,titleRight:title?.right,headingLeft:heading?.left,visibleLeft:bounds.left+element.clientLeft,visibleRight:bounds.right-element.clientLeft};});
 expect(treeScroll.scrollWidth).toBeGreaterThan(treeScroll.clientWidth);expect(treeScroll.scrollLeft).toBeGreaterThan(0);expect(treeScroll.titleRight).toBeLessThanOrEqual(treeScroll.visibleRight+1);expect(treeScroll.headingLeft).toBeGreaterThanOrEqual(treeScroll.visibleLeft-1);
 await tree.evaluate(element=>element.scrollLeft=0);
 const longLabelStaysBesideIcon=await longTitle.evaluate(title=>{const node=title.closest('[role="treeitem"]');const icon=node?.querySelector('.ant-tree-iconEle')?.getBoundingClientRect();const text=title.getBoundingClientRect();return Boolean(icon&&Math.abs(icon.top+icon.height/2-text.top-text.height/2)<2&&text.height<=36);});
 expect(longLabelStaysBesideIcon).toBe(true);
 await page.screenshot({path:info.outputPath('storage-directory-tree.png'),fullPage:true,animations:'disabled'});
 await expect(page.locator('.storage-file-columns')).toContainText('名称');await expect(page.locator('.storage-file-columns')).toContainText('详情');
 await expect(page.getByRole('button',{name:'打开',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'进入文件夹 docs',exact:true}).click();await expect(page.getByText('readme.md',{exact:true})).toBeVisible();
 await expect(page.getByRole('navigation',{name:'当前目录'})).toContainText('/data/workspace/docs');
 await page.getByRole('navigation',{name:'当前目录'}).getByRole('button',{name:'workspace',exact:true}).click();await expect(page.getByRole('button',{name:'进入文件夹 docs',exact:true})).toBeVisible();
 const dshRoot=treeItems.nth(4);
 await expect(dshRoot).toBeVisible();await expect(dshRoot).toHaveAccessibleName(/\/data\/dsh$/);
 await tree.getByText('docs',{exact:true}).click();
 await expect(page.getByText('readme.md',{exact:true})).toBeVisible();
 const breadcrumbs=page.getByRole('navigation',{name:'当前目录'});
 await expect(breadcrumbs).toContainText('/data/workspace/docs');
 const workspaceCrumb=breadcrumbs.getByRole('button',{name:'workspace',exact:true});
 await expect(workspaceCrumb).toBeVisible();
 const breadcrumbColors=await breadcrumbs.evaluate(nav=>[getComputedStyle(nav.querySelector('.storage-file-crumb-root')!).color,getComputedStyle(nav.querySelector('.ant-btn')!).color]);
 expect(breadcrumbColors[0]).toBe(breadcrumbColors[1]);
 await expect(breadcrumbs.getByRole('button',{name:'docs',exact:true})).toBeVisible();
 await dshRoot.click();
 await expect(breadcrumbs).toContainText('/data/dsh');
 await expect(breadcrumbs.getByRole('button',{name:'dsh',exact:true})).toHaveAttribute('aria-current','page');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 expect(writes).toEqual([]);
 await page.getByRole('button',{name:'返回工作数据'}).click();
 await expect(page).toHaveURL('/app/storage');
});
test('directory tree has no horizontal overflow when names fit',async({page})=>{
 await setup(page);
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:[{name:'docs',type:'DIRECTORY',size_bytes:0,modified_at:'2026-10-15T00:00:00Z'},{name:'assets',type:'DIRECTORY',size_bytes:0,modified_at:'2026-10-15T00:00:00Z'}]}}));
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();
 const tree=page.locator('.storage-file-tree');await expect(tree.getByRole('treeitem')).toHaveCount(5);
 const dimensions=await tree.evaluate(element=>({scrollWidth:element.scrollWidth,clientWidth:element.clientWidth}));
 expect(await tree.evaluate(element=>getComputedStyle(element).overflowX)).toBe('auto');expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth);
});
test('Word, spreadsheet, and presentation files render in a sandboxed preview',async({page},info)=>{
 await setup(page);const requestedModes:string[]=[];const fixtures=[
  {name:'report.docx',blob:docxPreviewFixture,text:'Office Preview Sample'},
  {name:'report.xlsx',blob:xlsxPreviewFixture,text:'Spreadsheet Preview Sample'},
  {name:'report.pptx',blob:pptxPreviewFixture,text:'Presentation Preview Sample'},
  {name:'report.odt',blob:openDocumentFixture('text','<text:p>OpenDocument Text Preview</text:p>'),text:'OpenDocument Text Preview'},
  {name:'report.ods',blob:openDocumentFixture('spreadsheet','<table:table table:name="Data"><table:table-row><table:table-cell office:value-type="string"><text:p>OpenDocument Sheet Preview</text:p></table:table-cell></table:table-row></table:table>'),text:'OpenDocument Sheet Preview'},
  {name:'report.odp',blob:openDocumentFixture('presentation','<draw:page draw:name="Slide 1"><draw:frame><draw:text-box><text:p>OpenDocument Slide Preview</text:p></draw:text-box></draw:frame></draw:page>'),text:'OpenDocument Slide Preview'},
 ];
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:fixtures.map(file=>({name:file.name,type:'FILE',size_bytes:file.blob.length,modified_at:'2026-10-15T00:00:00Z'}))}}));
 await page.route('**/v1/storage-assets/'+base.id+'/file?*',route=>{const query=new URL(route.request().url()).searchParams;requestedModes.push(query.get('mode')??'');const fixture=fixtures.find(file=>file.name===query.get('path'));return fixture?route.fulfill({contentType:'application/octet-stream',body:Buffer.from(fixture.blob)}):route.fulfill({status:404});});
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();
 for(const file of fixtures){
  const previewButton=page.getByRole('button',{name:`预览 ${file.name}`});await expect(previewButton).toBeEnabled();await previewButton.click();
  const iframe=page.locator(`iframe[title="${file.name} 预览"]`);const frame=page.frameLocator(`iframe[title="${file.name} 预览"]`);
  await expect(frame.getByText(file.text)).toBeVisible({timeout:30_000});
  await expect(iframe).toHaveAttribute('sandbox','');await expect(frame.locator('script')).toHaveCount(0);
  if(file.name.endsWith('.xlsx')){await expect(frame.getByText('Second Sheet Content')).toBeVisible();await expect(frame.getByRole('heading',{name:'Second sheet'})).toBeVisible();}
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:info.outputPath(`${file.name}-preview.png`),animations:'disabled'});
  await page.locator('.ant-modal-close').click();await expect(page.getByRole('dialog',{name:file.name})).toBeHidden();
 }
 expect(requestedModes).toEqual(fixtures.map(()=>'DOWNLOAD'));
});
test('PDF preview renders selectable pages, zooms and downloads the original',async({page},info)=>{
 await setup(page);const pdf=pdfPreviewFixture();const modes:string[]=[];const failures:string[]=[];
 page.on('pageerror',error=>failures.push(error.message));
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:[{name:'report.pdf',type:'FILE',size_bytes:pdf.length,modified_at:'2026-10-15T00:00:00Z'}]}}));
 await page.route('**/v1/storage-assets/'+base.id+'/file?*',route=>{modes.push(new URL(route.request().url()).searchParams.get('mode')!);return route.fulfill({contentType:'application/octet-stream',body:pdf});});
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();await page.getByRole('button',{name:'预览 report.pdf'}).click();
 const dialog=page.getByRole('dialog',{name:'report.pdf'});
 await expect(dialog.locator('.storage-pdf-text').getByText('PDF First Page')).toBeVisible({timeout:30_000});
 await expect(dialog.getByRole('button',{name:'PDF 上一页'})).toBeDisabled();await expect(dialog.locator('.storage-pdf-viewport')).toHaveAttribute('aria-busy','false');
 const size=await dialog.locator('canvas').evaluate(canvas=>({width:(canvas as HTMLCanvasElement).width,height:(canvas as HTMLCanvasElement).height}));expect(size.width).toBeGreaterThan(100);expect(size.height).toBeGreaterThan(100);
 await dialog.getByRole('button',{name:'PDF 下一页'}).click();await expect(dialog.locator('.storage-pdf-text').getByText('PDF Second Page')).toBeVisible();await expect(dialog.getByRole('button',{name:'PDF 下一页'})).toBeDisabled();
 await dialog.getByRole('combobox',{name:'PDF 缩放'}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle('150%',{exact:true}).click();
 await expect(dialog.locator('.storage-pdf-viewport')).toHaveAttribute('aria-busy','false');await expect(dialog.locator('.storage-pdf-page')).toHaveCSS('width','540px');
 const downloaded=page.waitForEvent('download');await dialog.getByRole('button',{name:'下载原文件'}).click();expect((await downloaded).suggestedFilename()).toBe('report.pdf');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('pdf-preview.png'),animations:'disabled'});
 await page.locator('.ant-modal-close').click();await page.getByRole('button',{name:'预览 report.pdf'}).click();await expect(page.locator('.storage-pdf-text').getByText('PDF First Page')).toBeVisible();
 expect(modes).toEqual(['DOWNLOAD','DOWNLOAD']);expect(failures).toEqual([]);
});
test('a DOCX placeholder shows its actual text and explains the extension mismatch',async({page})=>{
 await setup(page);
 const name='AI-Agent技术研究报告_2026-10.docx';
 const text='此文件使用占位文本创建，稍后将由生成工具填充。';
 const invalid=Buffer.from([0x50,0x4b,3,4,0,0]);
 const files=[{name,blob:Buffer.from(text)},{name:'incomplete.docx',blob:invalid},{name:'markup.docx',blob:Buffer.from('<script>window.untrustedPreview=true</script>')}];
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:files.map(file=>({name:file.name,type:'FILE',size_bytes:file.blob.length,modified_at:'2026-10-15T00:00:00Z'}))}}));
 await page.route('**/v1/storage-assets/'+base.id+'/file?*',route=>{const file=files.find(file=>file.name===new URL(route.request().url()).searchParams.get('path'));return route.fulfill({contentType:'application/octet-stream',body:file!.blob});});
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();
 await page.getByRole('button',{name:`预览 ${name}`}).click();const dialog=page.getByRole('dialog',{name});
 await expect(dialog.getByRole('alert')).toContainText('与 .docx 扩展名不符');await expect(dialog.locator('.storage-file-preview')).toHaveText(text);await expect(dialog.locator('iframe')).toHaveCount(0);
 const download=page.waitForEvent('download');await dialog.getByRole('button',{name:'下载原文件'}).click();expect((await download).suggestedFilename()).toBe(name);
 await page.locator('.ant-modal-close').click();await page.getByRole('button',{name:'预览 incomplete.docx'}).click();await expect(page.getByRole('alert')).toContainText('文档内容不完整或缺少必需文件');
 await page.locator('.ant-modal-close').click();await page.getByRole('button',{name:'预览 markup.docx'}).click();await expect(page.locator('.storage-file-preview')).toHaveText('<script>window.untrustedPreview=true</script>');expect(await page.evaluate(()=>Reflect.get(window,'untrustedPreview'))).toBeUndefined();
});
for(const scenario of ['refresh','updated','unavailable'])test(`PDF viewer handles missing modules: ${scenario}`,async({page})=>{
 const staleBuild=scenario!=='refresh';const persistent=scenario==='unavailable';
 await setup(page);let moduleRequests=0;let navigations=0;let versionChecks=0;
 const pdf=pdfPreviewFixture();
 page.on('framenavigated',frame=>{if(frame===page.mainFrame())navigations++;});
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:[{name:'report.pdf',type:'FILE',size_bytes:pdf.length,modified_at:'2026-10-15T00:00:00Z'}]}}));
 await page.route('**/v1/storage-assets/'+base.id+'/file?*',route=>route.fulfill({contentType:'application/octet-stream',body:pdf}));
 // Exercise the same lazy import in both Vite development and built assets.
 await page.route(/\/(?:assets\/pdf-preview-[^/]+\.js|src\/features\/storage\/pdf-preview\.tsx)(?:\?.*)?$/,async route=>{moduleRequests++;if(moduleRequests===1||persistent)return route.fulfill({status:404,contentType:'text/html',body:'<!doctype html><html>Resource removed by a newer build</html>'});return route.continue();});
 await page.route(`**/app/storage/${base.id}/browse`,async route=>{
  if(route.request().isNavigationRequest())return route.continue();
  versionChecks++;const response=await route.fetch();const html=await response.text();
  return route.fulfill({response,body:staleBuild?html.replace(/(<script\b(?=[^>]*type="module")[^>]*src=")([^"]+)(")/,'$1$2?new-build$3'):html});
 });
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();const before=navigations;await page.getByRole('button',{name:'预览 report.pdf'}).click();
 if(!staleBuild){
  await expect(page.getByRole('alert')).toContainText('查看器资源加载失败');await expect(page.getByRole('button',{name:'下载原文件'})).toBeEnabled();
  expect(navigations).toBe(before);await page.getByRole('button',{name:'刷新页面',exact:true}).click();
 }
 if(persistent){
  await expect(page.getByRole('alert')).toContainText('查看器资源加载失败');await expect(page.getByRole('button',{name:'下载原文件'})).toBeEnabled();
  expect(versionChecks).toBe(2);
 }else{
  await expect(page.getByRole('dialog',{name:'report.pdf'}).locator('.storage-pdf-text').getByText('PDF First Page')).toBeVisible({timeout:30_000});
  expect(versionChecks).toBe(1);await expect(page.getByRole('alert')).toHaveCount(0);
 }
 expect(navigations).toBe(before+1);expect(moduleRequests).toBe(2);
});
test('preview opens while loading, can be cancelled and retries the actual file after errors',async({page})=>{
 await setup(page);let state:'pending'|'broken'|'valid'='pending';let requests=0;let release:(()=>void)|undefined;
 const pending=new Promise<void>(resolve=>{release=resolve;});
 const files=[{name:'report.pdf',size_bytes:100},{name:'large.pdf',size_bytes:33*1024*1024},{name:'legacy.doc',size_bytes:100}];
 await page.route('**/v1/storage-assets/'+base.id+'/files?*',route=>route.fulfill({json:{root:'workspace',path:'',entries:files.map(file=>({...file,type:'FILE',modified_at:'2026-10-15T00:00:00Z'}))}}));
 await page.route('**/v1/storage-assets/'+base.id+'/file?*',async route=>{requests++;const current=state;if(current==='pending')await pending;await route.fulfill({contentType:'application/octet-stream',body:current==='valid'?pdfPreviewFixture():Buffer.from('broken PDF')}).catch(()=>{});});
 await page.goto('/app/storage');await page.getByRole('button',{name:'浏览 测试 数据目录'}).click();
 await expect(page.getByRole('button',{name:'预览 large.pdf'})).toBeDisabled();await expect(page.getByRole('button',{name:'下载 large.pdf'})).toBeEnabled();await expect(page.getByRole('button',{name:'预览 legacy.doc'})).toBeDisabled();
 await page.getByRole('button',{name:'预览 report.pdf'}).click();await expect(page.getByRole('dialog',{name:'report.pdf'}).getByRole('status')).toContainText('正在加载预览');
 await expect.poll(()=>requests).toBe(1);await page.locator('.ant-modal-close').click();state='broken';release!();
 await page.getByRole('button',{name:'预览 report.pdf'}).click();await expect(page.getByRole('alert')).toContainText('PDF 已损坏或无法读取');await expect(page.getByRole('button',{name:'下载原文件'})).toBeEnabled();
 state='valid';await page.getByRole('button',{name:'重新加载',exact:true}).click();await expect(page.locator('.storage-pdf-text').getByText('PDF First Page')).toBeVisible();expect(requests).toBe(3);
});
test('storage paginates filters and refreshes metadata without collecting data in the browser',async({page})=>{
 await setup(page);let last=new URLSearchParams();let reads=0;
 await page.route('**/v1/storage-assets?*',r=>{reads++;last=new URL(r.request().url()).searchParams;return r.fulfill({json:{items:last.get('status')||last.get('q')?[]:[base],total:last.get('status')||last.get('q')?0:21,next_cursor:null}});});
 await page.goto('/app/storage');await expect(page.getByRole('article')).toHaveCount(1);
 await page.getByRole('button',{name:'下一页',exact:true}).click();await expect.poll(()=>last.get('page')).toBe('2');
 await page.getByRole('combobox',{name:'筛选数据资产状态'}).locator('..').locator('..').click();await page.locator('.ant-select-dropdown:visible').getByTitle('已回收',{exact:true}).click();await expect.poll(()=>last.get('status')).toBe('DELETED');expect(last.get('page')).toBe('1');
 await expect(page.getByText('没有符合条件的数据资产',{exact:true})).toBeVisible();
 await page.getByRole('searchbox',{name:'搜索数据资产'}).fill('测试');await expect.poll(()=>last.get('q')).toBe('测试');const before=reads;
 await page.getByRole('button',{name:'刷新数据资产'}).click();await expect.poll(()=>reads).toBeGreaterThan(before);
});
test('storage distinguishes unavailable use and hides stale details after refresh failure',async({page})=>{
 await setup(page);let failed=false;
 await page.route('**/v1/storage-assets/'+base.id,r=>r.fulfill(failed?{status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}:{json:{...base,usage:{...base.usage,status:'UNAVAILABLE'}}}));
 await page.goto('/app/storage');await page.getByRole('button',{name:'查看测试的数据详情'}).click();const drawer=page.getByRole('dialog',{name:'数据资产详情'});
 await expect(drawer.getByText('暂不可用',{exact:true})).toBeVisible();failed=true;await drawer.getByRole('button',{name:'刷新详情'}).click();await expect(drawer.getByRole('alert')).toBeVisible();await expect(drawer.getByText(base.id,{exact:true})).toHaveCount(0);
});
test('storage list failure retries and empty data stays distinct from failures',async({page})=>{
 await setup(page);let failed=true;
 await page.route('**/v1/storage-assets?*',r=>r.fulfill(failed?{status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}:{json:{items:[],total:0,next_cursor:null}}));
 await page.goto('/app/storage');await expect(page.getByText('数据暂不可用',{exact:true}).first()).toBeVisible();failed=false;await page.getByRole('button',{name:'重新加载',exact:true}).click();await expect(page.getByText('暂无数据资产',{exact:true})).toBeVisible();
});
test('storage rechecks sessions and rejects owner mismatches',async({page})=>{
 await setup(page);
 await page.route('**/auth/login?*',route=>{
  expect(new URL(route.request().url()).searchParams.get('return_to')).toBe('/app/storage');
  return route.fulfill({contentType:'text/html; charset=utf-8',body:'<h1>登录账户</h1>'});
 });
 await page.route('**/v1/storage-assets?*',r=>r.fulfill({json:{items:[{...base,owner_id:'another-owner'}],total:1,next_cursor:null}}));
 await page.goto('/app/storage');await expect(page.getByRole('alert')).toContainText('存储资产数据无效');await expect(page.getByRole('article')).toHaveCount(0);
 await page.route('**/v1/me',r=>r.fulfill({status:401,json:{code:'UNAUTHENTICATED'}}));await page.route('**/v1/storage-assets?*',r=>r.fulfill({status:401,json:{code:'UNAUTHENTICATED'}}));await page.getByRole('button',{name:'重新加载',exact:true}).click();await expect(page.getByRole('heading',{name:'登录账户',exact:true})).toBeVisible();
});
test('organization audit authority does not grant storage access',async({page})=>{
 await setup(page,['audit:read','audit:organization']);let reads=0;await page.route('**/v1/storage-assets**',r=>{reads++;return r.fulfill({json:{items:[],total:0,next_cursor:null}});});
 await page.goto('/app/storage');await expect(page.getByText('没有数据资产读取权限',{exact:true})).toBeVisible();expect(reads).toBe(0);
});

test('owner can confirm early storage release and waits for physical completion',async({page},info)=>{
 await setup(page,['instances:read','instances:release-storage']);
 const releasable={...base,legal_hold:false,retention_expired:false,retention_until:'2099-01-01T00:00:00Z',maintenance:{...base.maintenance,reclaim_blockers:['RETENTION_ACTIVE','MINIMUM_7_DAYS']}};
 await page.route('**/v1/storage-assets/'+base.id,r=>r.fulfill({json:releasable}));
 await page.route('**/v1/storage-assets/'+base.id+'/backups?*',r=>r.fulfill({json:{items:[],total:0,next_cursor:null}}));
 let calls=0;let submitted:any;
 await page.route('**/v1/storage-assets/'+base.id+':release',async r=>{calls++;submitted=r.request().postDataJSON();expect(r.request().headers()['x-csrf-token']).toBe(user.csrf_token);await r.fulfill({status:202,json:{request_id:'release-1',asset_id:base.id,status:'ACCEPTED'}});});
 await page.goto('/app/storage');await page.getByRole('button',{name:'查看测试的数据详情'}).click();const drawer=page.getByRole('dialog',{name:'数据资产详情'});
 await expect(drawer.getByRole('navigation',{name:'备份记录分页'})).toHaveCount(0);await expect(drawer.getByText('资产标识',{exact:true})).toHaveCount(0);
 await page.screenshot({path:info.outputPath('storage-concise-details.png'),fullPage:true,animations:'disabled'});
 await drawer.getByRole('button',{name:'释放存储',exact:true}).click();let dialog=page.getByRole('dialog',{name:'释放存储',exact:true});
 await expect(dialog).toContainText('10 GiB');await expect(dialog).toContainText('无法恢复');await expect(dialog.getByRole('button',{name:'永久释放'})).toBeDisabled();
 await dialog.getByLabel(/^输入「.*」以确认永久删除$/).fill('错误名称');await expect(dialog.getByRole('button',{name:'永久释放'})).toBeDisabled();
 await dialog.getByRole('button',{name:/^取\s*消$/}).click();expect(calls).toBe(0);
 await drawer.getByRole('button',{name:'释放存储',exact:true}).click();dialog=page.getByRole('dialog',{name:'释放存储',exact:true});await dialog.getByLabel(/^输入「.*」以确认永久删除$/).fill(base.workspace_name);await dialog.getByRole('button',{name:'永久释放'}).click();
 await expect(drawer).toContainText('释放中，完成后返还存储额度');expect(calls).toBe(1);expect(submitted).toEqual({confirmed_name:base.workspace_name,confirmed_asset_id:base.id});
 await expect(drawer.getByRole('button',{name:'释放存储',exact:true})).toHaveCount(0);
});
test('legal hold blocks owner release and server rejection stays visible',async({page})=>{
 await setup(page,['instances:read','instances:release-storage']);await page.goto('/app/storage');await page.getByRole('button',{name:'查看测试的数据详情'}).click();const drawer=page.getByRole('dialog',{name:'数据资产详情'});
 await expect(drawer.getByRole('button',{name:'释放存储',exact:true})).toBeDisabled();
 await page.route('**/v1/storage-assets/'+base.id,r=>r.fulfill({json:{...base,legal_hold:false,maintenance:{...base.maintenance,reclaim_blockers:[]}}}));await drawer.getByRole('button',{name:'刷新详情'}).click();await expect(drawer.getByRole('button',{name:'释放存储',exact:true})).toBeEnabled();
 await page.route('**/v1/storage-assets/'+base.id+':release',r=>r.fulfill({status:409,json:{code:'STORAGE_PROTECTED'}}));
 await drawer.getByRole('button',{name:'释放存储',exact:true}).click();const dialog=page.getByRole('dialog',{name:'释放存储',exact:true});await dialog.getByLabel(/^输入「.*」以确认永久删除$/).fill(base.workspace_name);await dialog.getByRole('button',{name:'永久释放'}).click();await expect(dialog.getByRole('alert')).toContainText('法律保留');
});
