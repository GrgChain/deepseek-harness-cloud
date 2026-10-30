import {expect,test,type Page} from '@playwright/test';
import {zipSync,unzipSync} from 'fflate';
const id='b3e2b3e2-1111-4111-8111-111111111111';
const content='---\nname: meeting-notes\ndescription: 整理会议纪要\n---\n# 会议纪要\n';
const skill={id,name:'meeting-notes',description:'整理会议纪要并提取团队下一步行动，支持中文和附属资源',model_invocable:false,user_invocable:false,version:3,file_count:2,size_bytes:1210,source_url:'https://example.com/meeting-notes.zip',synced_at:'2026-10-22T08:00:00Z',created_at:'2026-10-22T08:00:00Z',updated_at:'2026-10-22T08:00:00Z',has_previous:true,content,files:[{name:'SKILL.md',size_bytes:210},{name:'references/guide.txt',size_bytes:1000}]};
const user={id:'user-skills',memberships:[{org_id:'org',role:'MEMBER',role_id:'member',role_name:'成员',status:'ACTIVE'}],permissions:['skills:read','skills:manage'],auth_generation:1,csrf_token:'x'.repeat(43),session_expires_at:'2099-01-01T00:00:00Z'};
const preview={token:'preview',source_url:skill.source_url,unchanged:false,items:[{name:skill.name,description:skill.description,files:skill.files,conflict:true,local_changed:true,changed_files:['SKILL.md']}]};
async function setup(page:Page,readOnly=false){
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:readOnly?['skills:read']:user.permissions}}));
 await page.route('**/v1/skills?*',r=>r.fulfill({json:{items:[skill],total:1,page:1,limit:10}}));
 await page.route('**/v1/skills/'+id,r=>r.fulfill({json:skill}));
 await page.route('**/v1/skills/'+id+':file?*',r=>r.fulfill({json:{path:new URL(r.request().url()).searchParams.get('path'),content:'资源说明',version:skill.version,editable:true}}));
}
test('Chinese skill creation validates characters and exports the UTF-8 filename',async({page})=>{
 await setup(page);
 const name='财报分析-v2';const chinese={...skill,name,content:content.replace('meeting-notes',name)};let creates=0;
 await page.route('**/v1/skills',async r=>{expect(r.request().method()).toBe('POST');expect(r.request().postDataJSON()).toEqual({name,description:'分析中文财报'});creates++;await r.fulfill({json:chinese});});
 await page.route('**/v1/skills/'+id,r=>r.fulfill({json:chinese}));
 await page.route('**/v1/skills:export',r=>r.fulfill({contentType:'text/markdown',headers:{'Content-Disposition':"attachment; filename*=utf-8''"+encodeURIComponent(name+'.md')},body:chinese.content}));
 await page.goto('/app/skills');await page.getByRole('button',{name:'新建技能',exact:true}).click();
 await page.getByLabel('用途说明',{exact:true}).fill('分析中文财报');
 for(const invalid of ['中文--技能','-中文','中文/技能','中'.repeat(65)]){
  await page.getByLabel('Skill 名称',{exact:true}).fill(invalid);await page.getByRole('button',{name:'创建并编辑',exact:true}).click();await expect(page.getByText('使用 1–64 个中文、小写字母、数字或连字符，连字符不能连续或位于首尾',{exact:true})).toBeVisible();
 }
 expect(creates).toBe(0);await page.getByLabel('Skill 名称',{exact:true}).fill(name);await page.getByRole('button',{name:'创建并编辑',exact:true}).click();await expect(page.getByLabel('SKILL.md',{exact:true})).toHaveValue(chinese.content);expect(creates).toBe(1);
 await page.getByRole('button',{name:'关闭',exact:true}).click();await page.getByRole('button',{name:'下载 meeting-notes',exact:true}).click();const waiting=page.waitForEvent('download');await page.getByRole('button',{name:/^下\s*载$/}).click();expect((await waiting).suggestedFilename()).toBe(name+'.md');
});
test('list, failed save retains text, close confirmation and download',async({page},info)=>{
 await setup(page);
 await page.route('**/v1/skills/'+id,async r=>{if(r.request().method()==='PUT'){expect(r.request().postDataJSON()).toEqual({content:content+'补充要求',expected_version:3});expect(await r.request().headerValue('x-csrf-token')).toBe(user.csrf_token);await r.fulfill({status:409,json:{code:'VERSION_CONFLICT',message:'Skill 已被修改，请刷新后重新操作'}});}else await r.fulfill({json:skill});});
 await page.route('**/v1/skills:export',async r=>{expect(r.request().postDataJSON()).toEqual({ids:[id]});await r.fulfill({contentType:'application/octet-stream',headers:{'Content-Disposition':'attachment; filename=meeting-notes.zip'},body:'test zip'});});
 await page.goto('/app/skills');const row=page.getByRole('article',{name:'Skill meeting-notes',exact:true});await expect(row).toBeVisible();await expect(row.getByRole('switch')).toHaveCount(0);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('skill-library.png'),fullPage:true,animations:'disabled'});
 await row.getByRole('button',{name:'meeting-notes 更多操作',exact:true}).click();await expect(page.getByRole('menuitem',{name:'检查更新',exact:true})).toBeVisible();await page.screenshot({path:info.outputPath('skill-actions.png'),fullPage:true,animations:'disabled'});await page.getByRole('menuitem',{name:'编辑',exact:true}).click();await page.getByLabel('SKILL.md',{exact:true}).fill(content+'补充要求');await page.getByRole('button',{name:/^保\s*存$/}).click();await expect(page.getByText('Skill 已被修改，请刷新后重新操作',{exact:true})).toBeVisible();await expect(page.getByLabel('SKILL.md',{exact:true})).toHaveValue(content+'补充要求');
 await page.getByRole('button',{name:'关闭',exact:true}).click();await expect(page.getByRole('dialog',{name:'放弃未保存的修改？'})).toBeVisible();await page.getByRole('button',{name:'放弃修改',exact:true}).click();await expect(page.getByLabel('SKILL.md',{exact:true})).toHaveCount(0);
 await row.getByRole('button',{name:'下载 meeting-notes',exact:true}).click();await expect(page.getByRole('dialog',{name:'导出 1 项 Skill'})).toBeVisible();const downloading=page.waitForEvent('download');await page.getByRole('button',{name:/^下\s*载$/}).click();expect((await downloading).suggestedFilename()).toBe('meeting-notes.zip');
});
test('unified import dialog switches methods and previews before confirmed replacement',async({page},info)=>{
 await setup(page);let applied=0;
 await page.route('**/v1/skills:source',r=>{expect(r.request().postDataJSON()).toEqual({url:skill.source_url});return r.fulfill({json:preview});});
 await page.route('**/v1/skills:preview',r=>{const body=r.request().postDataJSON();expect(body.filename).toBe('SKILL.md');expect(Buffer.from(body.content_base64,'base64').toString()).toBe(content);return r.fulfill({json:preview});});
 await page.route('**/v1/skills:apply',r=>{expect(r.request().postDataJSON()).toEqual({token:'preview',mode:'replace'});applied++;return r.fulfill({json:{applied:['meeting-notes'],skipped:[]}});});
 await page.goto('/app/skills');await expect(page.getByRole('button',{name:'导入',exact:true})).toHaveCount(1);await expect(page.getByRole('button',{name:'链接导入',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'导入文件',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'导入',exact:true}).click();const dialog=page.getByRole('dialog',{name:'导入技能包',exact:true});await expect(dialog).toBeVisible();await expect(dialog.getByRole('tab',{name:'搜索技能',exact:true})).toHaveAttribute('aria-selected','true');await dialog.getByRole('tab',{name:'链接导入',exact:true}).click();await expect(dialog.getByRole('button',{name:'获取并预览',exact:true})).toBeDisabled();await page.getByLabel('公开链接').fill(skill.source_url);
 await dialog.getByRole('tab',{name:'文件导入',exact:true}).click();await expect(dialog.getByText('Markdown 文件或 ZIP 技能包',{exact:true})).toBeVisible();await expect(dialog.getByRole('button',{name:'获取并预览',exact:true})).toHaveCount(0);await expect(page.getByRole('dialog')).toHaveCount(1);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('unified-skill-import.png'),fullPage:true,animations:'disabled'});
 await dialog.getByRole('tab',{name:'链接导入',exact:true}).click();await expect(page.getByLabel('公开链接')).toHaveValue(skill.source_url);await page.getByRole('button',{name:'获取并预览',exact:true}).click();await expect(page.getByText('此 Skill 有本地修改，替换前会保留上一版。',{exact:true})).toBeVisible();await expect(page.getByRole('radio',{name:'保留现有，跳过同名项'})).toBeChecked();expect(applied).toBe(0);
 await page.getByRole('radio',{name:'替换同名项，并保留上一版供下载'}).check();await page.getByRole('button',{name:/确认导入$/}).click();await expect(page.getByText('已保存 1 项',{exact:true})).toBeVisible();expect(applied).toBe(1);
 await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'文件导入',exact:true}).click();await page.getByLabel('选择 Skill 文件').setInputFiles({name:'SKILL.md',mimeType:'text/markdown',buffer:Buffer.from(content)});await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await page.getByRole('button',{name:/^取\s*消$/}).click();expect(applied).toBe(1);
});
test('skill upload accepts files larger than the previous limit',async({page})=>{
 await setup(page);let requests=0;
 await page.route('**/v1/skills:preview',r=>{requests++;const body=r.request().postDataJSON();expect(Buffer.from(body.content_base64,'base64')).toHaveLength(5*1024*1024+1);return r.fulfill({json:preview});});
 await page.goto('/app/skills');await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'文件导入',exact:true}).click();await expect(page.getByText('保留包内资源目录',{exact:true})).toBeVisible();
 await page.getByLabel('选择 Skill 文件').setInputFiles({name:'large.zip',mimeType:'application/zip',buffer:Buffer.alloc(5*1024*1024+1)});await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();expect(requests).toBe(1);
});

test('hub search previews the selected result and supports provider errors and empty results',async({page},info)=>{
 await setup(page);let applied=0,searches=0;
 const url='https://skills.sh/team/repo/browser';
 await page.route('**/v1/skills:search',async r=>{
  searches++;const input=r.request().postDataJSON();expect(await r.request().headerValue('x-csrf-token')).toBe(user.csrf_token);
  if(input.provider==='clawhub')return r.fulfill({json:{provider:'clawhub',items:[]}});
  if(input.query==='failed')return r.fulfill({status:422,json:{code:'SKILL_INVALID',message:'技能市场搜索暂不可用，请稍后重试或切换来源'}});
  expect(input).toEqual({provider:'skills.sh',query:'browser'});
  await new Promise(resolve=>setTimeout(resolve,200));return r.fulfill({json:{provider:'skills.sh',items:[{name:'browser',description:'自动浏览网页并保留脚本资源',publisher:'team/repo',url,downloads:1200}]}});
 });
 await page.route('**/v1/skills:source',r=>{expect(r.request().postDataJSON()).toEqual({url});return r.fulfill({json:{...preview,source_url:url}});});
 await page.route('**/v1/skills:apply',r=>{applied++;return r.fulfill({json:{applied:['meeting-notes'],skipped:[]}});});
 await page.goto('/app/skills');await page.getByRole('button',{name:'导入',exact:true}).click();const dialog=page.getByRole('dialog',{name:'导入技能包',exact:true});
 await expect(dialog.getByRole('tab',{name:'搜索技能',exact:true})).toHaveAttribute('aria-selected','true');await expect(dialog.getByRole('button',{name:'搜索技能',exact:true})).toBeDisabled();await page.getByLabel('搜索关键词',{exact:true}).fill('browser');await dialog.getByRole('button',{name:'搜索技能',exact:true}).click();
 const result=page.getByRole('article',{name:'搜索结果 browser',exact:true});await expect(result).toBeVisible();await expect(result.getByRole('link',{name:'查看来源'})).toHaveAttribute('href',url);expect(applied).toBe(0);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('skill-hub-search.png'),fullPage:true,animations:'disabled'});
 await result.getByRole('button',{name:'预览并导入',exact:true}).click();await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();expect(applied).toBe(0);await page.getByRole('radio',{name:'替换同名项，并保留上一版供下载'}).check();await page.getByRole('button',{name:/确认导入$/}).click();await expect(page.getByText('已保存 1 项',{exact:true})).toBeVisible();expect(applied).toBe(1);
 await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByLabel('搜索关键词',{exact:true}).fill('failed');await page.getByRole('button',{name:'搜索技能',exact:true}).click();await expect(page.getByText('技能市场搜索暂不可用，请稍后重试或切换来源',{exact:true})).toBeVisible();
  await page.locator('.skill-hub-search-form .ant-select-selector').click();await page.getByRole('option',{name:'ClawHub',exact:true}).click();await page.getByRole('button',{name:'搜索技能',exact:true}).click();await expect(page.getByText('没有找到匹配的技能，试试其他关键词或切换来源',{exact:true})).toBeVisible();expect(searches).toBe(3);
});

test('GitHub repository directory and file links preview multiple skills before saving',async({page})=>{
 await setup(page);let submitted='',applied=0;
 await page.route('**/v1/skills:source',r=>{expect(r.request().postDataJSON()).toEqual({url:submitted});return r.fulfill({json:{...preview,source_url:submitted,items:[preview.items[0],{...preview.items[0],name:'another-skill',description:'仓库中的另一个技能',conflict:false,local_changed:false}]}});});
 await page.route('**/v1/skills:apply',r=>{applied++;return r.fulfill({json:{applied:[],skipped:[]}});});
 for(const link of ['https://github.com/team/repo','https://github.com/team/repo/tree/main/skills','https://github.com/team/repo/blob/main/skills/example/SKILL.md']){
  submitted=link;await page.goto('/app/skills');await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'链接导入',exact:true}).click();await page.getByLabel('公开链接').fill(link);await expect(page.getByText('支持 GitHub 仓库、技能目录和文件链接，也支持 Markdown、ZIP 直链。先预览，再导入。',{exact:true})).toBeVisible();await page.getByRole('button',{name:'获取并预览',exact:true}).click();await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await expect(page.getByText('another-skill',{exact:true})).toBeVisible();expect(applied).toBe(0);await page.getByRole('button',{name:/^取\s*消$/}).click();
 }
});

test('Tencent and SkillsMP results keep their source links and preview the right package',async({page},info)=>{
 await setup(page);let previewURL='';
 await page.route('**/v1/skills:search',r=>{const {provider,query}=r.request().postDataJSON();expect(query).toBe('browser');const mp=provider==='skillsmp';expect(['tencent','skillsmp']).toContain(provider);return r.fulfill({json:{provider,items:[{name:'browser',description:'浏览器自动化技能',publisher:'team',url:mp?'https://skillsmp.com/creators/team/repo/browser':'https://skillhub.cn/skills/browser',downloads:1200,...(mp?{metric:'stars',source_url:'https://github.com/team/repo/tree/main/skills/browser'}:{})}]}});});
 await page.route('**/v1/skills:source',r=>{expect(r.request().postDataJSON()).toEqual({url:previewURL});return r.fulfill({json:{...preview,source_url:previewURL}});});
 await page.goto('/app/skills');
 for(const provider of ['SkillHub','SkillsMP']){
  await page.getByRole('button',{name:'导入',exact:true}).click();await page.locator('.skill-hub-search-form .ant-select-selector').click();await page.getByRole('option',{name:provider,exact:true}).click();await page.getByLabel('搜索关键词',{exact:true}).fill('browser');await page.getByRole('button',{name:'搜索技能',exact:true}).click();
  const result=page.getByRole('article',{name:'搜索结果 browser',exact:true});await expect(result).toBeVisible();await expect(result.getByText(provider,{exact:true})).toBeVisible();await expect(result.getByText(provider==='SkillsMP'?'1200 个星标':'1200 次下载',{exact:true})).toBeVisible();await expect(result.getByRole('link',{name:'查看来源'})).toHaveAttribute('href',provider==='SkillsMP'?'https://skillsmp.com/creators/team/repo/browser':'https://skillhub.cn/skills/browser');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath(provider+'-search.png'),fullPage:true,animations:'disabled'});
  previewURL=provider==='SkillsMP'?'https://github.com/team/repo/tree/main/skills/browser':'https://skillhub.cn/skills/browser';await result.getByRole('button',{name:'预览并导入',exact:true}).click();await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await page.getByRole('button',{name:/^取\s*消$/}).click();
 }
});
test('read-only library permits view and download without management',async({page})=>{
 await setup(page,true);await page.goto('/app/skills');await expect(page.getByRole('button',{name:'meeting-notes',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'新建技能',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'导入',exact:true})).toHaveCount(0);await expect(page.getByRole('switch')).toHaveCount(0);await page.getByRole('button',{name:'meeting-notes',exact:true}).click();await expect(page.getByLabel('SKILL.md',{exact:true})).toHaveAttribute('readonly','');await expect(page.getByRole('button',{name:'来源与同步'})).toBeVisible();await expect(page.getByRole('button',{name:/^保\s*存$/})).toHaveCount(0);await expect(page.getByRole('button',{name:'新建文件',exact:true})).toHaveCount(0);
 await page.locator('.skill-file-picker .ant-select-selector').click();await page.getByRole('option',{name:'references/guide.txt',exact:true}).click();await expect(page.getByLabel('references/guide.txt',{exact:true})).toHaveAttribute('readonly','');
});

test('resource editor protects unsaved text, rejects binary edits and saves only the selected file',async({page})=>{
 await setup(page);let writes=0;
 await page.route('**/v1/skills/'+id,r=>r.fulfill({json:{...skill,file_count:3,files:[...skill.files,{name:'assets/chart.png',size_bytes:50}]}}));
 await page.route('**/v1/skills/'+id+':file?*',r=>{const path=new URL(r.request().url()).searchParams.get('path');return r.fulfill({json:{path,content:path==='assets/chart.png'?'':'附属说明',version:7,editable:path!=='assets/chart.png'}});});
 await page.route('**/v1/skills/'+id+':file',r=>{expect(r.request().method()).toBe('PUT');expect(r.request().postDataJSON()).toEqual({path:'references/guide.txt',content:'修改后的附属说明',expected_version:7});writes++;return writes===1?r.fulfill({status:409,json:{code:'VERSION_CONFLICT',message:'文件已被修改，请刷新后重新操作'}}):r.fulfill({json:{...skill,version:8}});});
 await page.goto('/app/skills');await page.getByRole('button',{name:'meeting-notes',exact:true}).click();await expect(page.getByLabel('SKILL.md',{exact:true})).toHaveValue(content);
 async function selectFile(path:string){await page.locator('.skill-file-picker .ant-select-selector').click();await page.getByRole('option',{name:path,exact:true}).click();}
 await selectFile('assets/chart.png');await expect(page.getByText('此文件为二进制文件，请下载技能包后查看。',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:/^保\s*存$/})).toBeDisabled();
 await selectFile('references/guide.txt');const editor=page.getByLabel('references/guide.txt',{exact:true});await expect(editor).toHaveValue('附属说明');await editor.fill('修改后的附属说明');await selectFile('SKILL.md');await expect(page.getByRole('dialog',{name:'放弃未保存的修改？'})).toBeVisible();await page.getByRole('button',{name:'继续编辑',exact:true}).click();await expect(editor).toHaveValue('修改后的附属说明');
 await page.getByRole('button',{name:/^保\s*存$/}).click();await expect(page.getByText('文件已被修改，请刷新后重新操作',{exact:true})).toBeVisible();await expect(editor).toHaveValue('修改后的附属说明');await page.getByRole('button',{name:/^保\s*存$/}).click();await expect(page.getByText('Skill 已保存',{exact:true})).toBeVisible();expect(writes).toBe(2);
});

test('new file dialog preserves failed scripts and opens the saved script',async({page},info)=>{
 await setup(page);let count=0;
 await page.route('**/v1/skills/'+id,r=>r.fulfill({json:{...skill,source_url:'',synced_at:null}}));
 await page.route('**/v1/skills/'+id+':file',r=>{expect(r.request().method()).toBe('POST');expect(r.request().postDataJSON()).toEqual({path:'scripts/run.py',content:"print('hello')\n",expected_version:3});count++;return count===1?r.fulfill({status:422,json:{code:'SKILL_INVALID',message:'已存在同名文件，请使用其他路径'}}):r.fulfill({json:{...skill,version:4,file_count:3}});});
 await page.route('**/v1/skills/'+id+':file?*',r=>r.fulfill({json:{path:'scripts/run.py',content:"print('hello')\n",version:4,editable:true}}));
 await page.goto('/app/skills');await page.getByRole('button',{name:'meeting-notes',exact:true}).click();await expect(page.getByLabel('SKILL.md',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'来源与同步'})).toHaveCount(0);await expect(page.getByText('本地创建或文件导入',{exact:true})).toHaveCount(0);await page.getByRole('button',{name:'新建文件',exact:true}).click();await page.getByLabel('文件路径',{exact:true}).fill('scripts/run.py');await page.getByLabel('文件内容',{exact:true}).fill("print('hello')\n");await expect(page.getByRole('dialog',{name:'新建文件',exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('new-script.png'),fullPage:true,animations:'disabled'});await page.getByRole('button',{name:/^取\s*消$/}).click();await expect(page.getByRole('dialog',{name:'放弃新建文件？'})).toBeVisible();await page.getByRole('button',{name:'继续编辑',exact:true}).click();await expect(page.getByLabel('文件内容',{exact:true})).toHaveValue("print('hello')\n");
 await page.getByRole('button',{name:'创建文件',exact:true}).click();await expect(page.getByText('已存在同名文件，请使用其他路径',{exact:true})).toBeVisible();await expect(page.getByLabel('文件内容',{exact:true})).toHaveValue("print('hello')\n");await page.getByRole('button',{name:'创建文件',exact:true}).click();await expect(page.getByLabel('scripts/run.py',{exact:true})).toHaveValue("print('hello')\n");expect(count).toBe(2);
});

test('live personal library creates, edits, imports, exports and deletes without workspace operations',async({page})=>{
 test.skip(process.env.HC_REAL_SKILLS!=='true','requires local platform OIDC and migration 46');test.setTimeout(60_000);
 const {readFileSync}=await import('node:fs');const name='中文技能-'+Date.now().toString(36)+'-'+Math.random().toString(36).slice(2,6);
 await page.goto('/auth/login?return_to=/app/skills');await page.locator('input[name=login]').fill('member@harness.local');await page.locator('input[name=password]').fill(readFileSync('../.local/auth/member-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/skills$/);
 const me=await page.request.get('/v1/me');expect(me.status()).toBe(200);const identity=await me.json();const headers={Origin:new URL(page.url()).origin,'X-CSRF-Token':identity.csrf_token};
 try{
  await page.getByRole('button',{name:'新建技能',exact:true}).click();await page.getByLabel('Skill 名称',{exact:true}).fill(name);await page.getByLabel('用途说明',{exact:true}).fill('个人技能库真实流程测试');await page.getByRole('button',{name:'创建并编辑',exact:true}).click();
  const editor=page.getByLabel('SKILL.md',{exact:true});await expect(editor).toBeVisible();const body=await editor.inputValue();await editor.fill(body+'\n# 验证\n仅平台个人库测试，不修改 DSH。\n');await page.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor).toHaveCount(0);
  const list=await (await page.request.get('/v1/skills?q='+name)).json();expect(list.items).toHaveLength(1);const current=await (await page.request.get('/v1/skills/'+list.items[0].id)).json();expect(current.content).toContain('仅平台个人库测试');
  const exported=await page.request.post('/v1/skills:export',{headers,data:{ids:[current.id]}});expect(exported.status()).toBe(200);expect(await exported.text()).toBe(current.content);
  // Browser file upload goes through real parse/preview/apply transactions.
  await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'文件导入',exact:true}).click();await page.getByLabel('选择 Skill 文件').setInputFiles({name:'SKILL.md',mimeType:'text/markdown',buffer:Buffer.from(current.content+'\n导入新内容\n')});await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await page.getByRole('radio',{name:'替换同名项，并保留上一版供下载'}).check();await page.getByRole('button',{name:/确认导入$/}).click();await expect(page.getByText('已保存 1 项',{exact:true})).toBeVisible();
  const replaced=await (await page.request.get('/v1/skills/'+current.id)).json();expect(replaced.content).toContain('导入新内容');expect(replaced.model_invocable).toBe(false);expect(replaced.user_invocable).toBe(false);expect(replaced.has_previous).toBe(true);
  const previous=await page.request.post('/v1/skills/'+current.id+':previous',{headers,data:{}});expect(previous.status()).toBe(200);expect(await previous.text()).toBe(current.content);
  const missingCSRF=await page.request.post('/v1/skills',{headers:{Origin:headers.Origin},data:{name:'rejected',description:'test'}});expect(missingCSRF.status()).toBe(403);
 }finally{
  const lookup=await page.request.get('/v1/skills?q='+name);if(lookup.ok()){for(const entry of (await lookup.json()).items.filter((item:{name:string})=>item.name===name)){const removed=await page.request.delete('/v1/skills/'+entry.id,{headers,data:{confirmed_name:name,expected_version:entry.version}});expect(removed.status()).toBe(200);}}
  expect((await page.request.post('/auth/logout',{headers})).status()).toBe(204);
 }
});

test('live skill resource text can be edited and exported while other files stay intact',async({page})=>{
 test.skip(process.env.HC_REAL_SKILLS!=='true','requires local platform OIDC and skill file API');test.setTimeout(60_000);
 const {readFileSync}=await import('node:fs');const name='test-files-'+Date.now().toString(36)+'-'+Math.random().toString(36).slice(2,6);
 await page.goto('/auth/login?return_to=/app/skills');await page.locator('input[name=login]').fill('member@harness.local');await page.locator('input[name=password]').fill(readFileSync('../.local/auth/member-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/skills$/);
 const me=await(await page.request.get('/v1/me')).json();const headers={Origin:new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
 const root='---\nname: '+name+'\ndescription: 测试附属文件编辑\n---\n# 正文保留\n';const image=Uint8Array.from([137,80,78,71,13,10,26,10,0]);
 try{
  const data=zipSync({'SKILL.md':new TextEncoder().encode(root),'references/中文.txt':new TextEncoder().encode('原始资源说明'),'scripts/example.py':new TextEncoder().encode("print('example')\n"),'assets/chart.png':image});
  await page.getByRole('button',{name:'导入',exact:true}).click();await page.getByRole('tab',{name:'文件导入',exact:true}).click();await page.getByLabel('选择 Skill 文件').setInputFiles({name:'resources.zip',mimeType:'application/zip',buffer:Buffer.from(data)});await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await expect(page.getByText(/默认关闭自动和手动调用/)).toHaveCount(0);await page.getByRole('button',{name:/确认导入$/}).click();
  const row=page.getByRole('article',{name:'Skill '+name,exact:true});await expect(row).toBeVisible();await expect(row.getByRole('switch')).toHaveCount(0);await row.getByRole('button',{name,exact:true}).click();const originalRoot=await page.getByLabel('SKILL.md',{exact:true}).inputValue();
  async function selectFile(path:string){await page.locator('.skill-file-picker .ant-select-selector').click();await page.getByRole('option',{name:path,exact:true}).click();}
  await selectFile('assets/chart.png');await expect(page.getByText('此文件为二进制文件，请下载技能包后查看。',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:/^保\s*存$/})).toBeDisabled();
  await selectFile('references/中文.txt');const editor=page.getByLabel('references/中文.txt',{exact:true});await expect(editor).toHaveValue('原始资源说明');await editor.fill('更新后的资源说明\n');await selectFile('SKILL.md');await page.getByRole('button',{name:'继续编辑',exact:true}).click();await expect(editor).toHaveValue('更新后的资源说明\n');await page.getByRole('button',{name:/^保\s*存$/}).click();await expect(editor).toHaveCount(0);
  const list=await(await page.request.get('/v1/skills?q='+name)).json();expect(list.items).toHaveLength(1);const skill=list.items[0];const file=await(await page.request.get('/v1/skills/'+skill.id+':file?'+new URLSearchParams({path:'references/中文.txt'}))).json();expect(file.content).toBe('更新后的资源说明\n');expect(file.version).toBe(skill.version);
  const exported=await page.request.post('/v1/skills:export',{headers,data:{ids:[skill.id]}});expect(exported.status()).toBe(200);const files=unzipSync(await exported.body());expect(Object.keys(files)).toHaveLength(4);expect(new TextDecoder().decode(files[name+'/SKILL.md'])).toBe(originalRoot);expect(new TextDecoder().decode(files[name+'/references/中文.txt'])).toBe('更新后的资源说明\n');expect(new TextDecoder().decode(files[name+'/scripts/example.py'])).toBe("print('example')\n");expect(files[name+'/assets/chart.png']).toEqual(image);
  const missingCSRF=await page.request.put('/v1/skills/'+skill.id+':file',{headers:{Origin:headers.Origin},data:{path:'references/中文.txt',content:'rejected',expected_version:skill.version}});expect(missingCSRF.status()).toBe(403);
 }finally{
  const lookup=await page.request.get('/v1/skills?q='+name);if(lookup.ok())for(const entry of (await lookup.json()).items.filter((item:{name:string})=>item.name===name))expect((await page.request.delete('/v1/skills/'+entry.id,{headers,data:{confirmed_name:name,expected_version:entry.version}})).status()).toBe(200);
  expect((await page.request.post('/auth/logout',{headers})).status()).toBe(204);
 }
});


test('live newly created skill accepts a script and exports it intact',async({page})=>{
 test.skip(process.env.HC_REAL_SKILLS!=='true','requires local skill file creation API');test.setTimeout(60_000);
 const {readFileSync}=await import('node:fs');const name='test-script-'+Date.now().toString(36)+'-'+Math.random().toString(36).slice(2,6);
 await page.goto('/auth/login?return_to=/app/skills');await page.locator('input[name=login]').fill('member@harness.local');await page.locator('input[name=password]').fill(readFileSync('../.local/auth/member-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/skills$/);
 const me=await(await page.request.get('/v1/me')).json();const headers={Origin:new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
 try{
  await page.getByRole('button',{name:'新建技能',exact:true}).click();await page.getByLabel('Skill 名称',{exact:true}).fill(name);await page.getByLabel('用途说明',{exact:true}).fill('验证新技能添加脚本');await page.getByRole('button',{name:'创建并编辑',exact:true}).click();const root=await page.getByLabel('SKILL.md',{exact:true}).inputValue();
  await page.getByRole('button',{name:'新建文件',exact:true}).click();await page.getByLabel('文件路径',{exact:true}).fill('scripts/run.py');const script="#!/usr/bin/env python3\nprint('新建技能脚本')\n";await page.getByLabel('文件内容',{exact:true}).fill(script);await page.getByRole('button',{name:'创建文件',exact:true}).click();await expect(page.getByLabel('scripts/run.py',{exact:true})).toHaveValue(script);
  await page.getByRole('button',{name:'关闭',exact:true}).click();const listing=await(await page.request.get('/v1/skills?q='+name)).json();const item=listing.items[0];expect(item.file_count).toBe(2);const exported=await page.request.post('/v1/skills:export',{headers,data:{ids:[item.id]}});expect(exported.status()).toBe(200);const files=unzipSync(await exported.body());expect(Object.keys(files)).toHaveLength(2);expect(new TextDecoder().decode(files[name+'/SKILL.md'])).toBe(root);expect(new TextDecoder().decode(files[name+'/scripts/run.py'])).toBe(script);
  const csrf=await page.request.post('/v1/skills/'+item.id+':file',{headers:{Origin:headers.Origin},data:{path:'scripts/denied.py',content:'',expected_version:item.version}});expect(csrf.status()).toBe(403);
 }finally{
  const listing=await page.request.get('/v1/skills?q='+name);if(listing.ok())for(const item of (await listing.json()).items.filter((item:{name:string})=>item.name===name))expect((await page.request.delete('/v1/skills/'+item.id,{headers,data:{confirmed_name:name,expected_version:item.version}})).status()).toBe(200);
  expect((await page.request.post('/auth/logout',{headers})).status()).toBe(204);
 }
});
