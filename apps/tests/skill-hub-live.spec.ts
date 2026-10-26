import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';

test('public Tencent SkillHub detail route renders the reported skill',async({page})=>{
 test.skip(process.env.HC_REAL_SKILL_HUB!=='true','requires public SkillHub');
 await page.goto('https://skillhub.cn/indiv-weishui/ws-bond-plan');
 await expect(page.getByText('你访问的页面已不存在或已下线～',{exact:true})).toBeVisible();
 await page.goto('https://skillhub.cn/skills/ws-bond-plan');
 await expect(page.getByText('专项债实施方案',{exact:true}).first()).toBeVisible({timeout:20_000});
 await expect(page.locator('body')).not.toContainText('page not found');
 await expect(page.locator('body')).not.toContainText('你访问的页面已不存在或已下线');
});

test('live hubs search and preview selected public packages without applying them',async({page},info)=>{
 test.skip(process.env.HC_REAL_SKILL_HUB!=='true','requires local platform and public skill registries');test.setTimeout(160_000);
 await page.goto('/auth/login?return_to=/app/skills');await expect(page.getByLabel('邮箱',{exact:true})).toBeVisible({timeout:5000});await page.getByLabel('邮箱',{exact:true}).fill('member@harness.local');await page.getByLabel('密码',{exact:true}).fill(readFileSync('../.local/auth/member-password','utf8').trim());await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/app\/skills$/);
 const me=await(await page.request.get('/v1/me')).json();const origin=new URL(page.url()).origin;
 expect((await page.request.post('/v1/skills:search',{headers:{Origin:origin},data:{provider:'skills.sh',query:'browser'}})).status()).toBe(403);
 try{
  const sources=[{provider:'skills.sh',label:'skills.sh',query:'browser',publisher:'vercel-labs/agent-browser'},{provider:'clawhub',label:'ClawHub',query:'browser',publisher:'rez0'},{provider:'tencent',label:'SkillHub',query:'chrome-devtools-mcp',publisher:''},{provider:'skillsmp',label:'SkillsMP',query:'chrome-devtools-mcp',publisher:'ChromeDevTools'}];
  for(const {provider,label,query,publisher} of sources.filter(source=>!process.env.HC_SKILL_HUB_PROVIDERS||process.env.HC_SKILL_HUB_PROVIDERS.split(',').includes(source.provider))){
   await page.getByRole('button',{name:'导入',exact:true}).click();
   if(provider!=='skills.sh'){await page.locator('.skill-hub-search-form .ant-select-selector').click();await page.getByRole('option',{name:label,exact:true}).click();}
   await page.getByLabel('搜索关键词',{exact:true}).fill(query);const searching=page.waitForResponse(r=>r.url().endsWith('/v1/skills:search')&&r.request().method()==='POST');await page.getByRole('button',{name:'搜索技能',exact:true}).click();const response=await searching;expect(response.status(),await response.text()).toBe(200);const results=await response.json();expect(results.items.length).toBeGreaterThan(0);
   if(provider==='tencent'){
    for(const item of results.items)expect(item.url).toMatch(/^https:\/\/skillhub\.cn\/skills\//);
    const regression=await page.request.post('/v1/skills:search',{headers:{Origin:origin,'X-CSRF-Token':me.csrf_token},data:{provider:'tencent',query:'ws-bond-plan'}});expect(regression.status()).toBe(200);expect((await regression.json()).items.some((item:{url:string})=>item.url==='https://skillhub.cn/skills/ws-bond-plan')).toBe(true);
   }
   const result=page.locator('.skill-hub-result').filter({hasText:publisher}).first();await expect(result).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath(provider+'-live-search.png'),fullPage:true,animations:'disabled'});
   if(provider==='tencent'){
    const [source]=await Promise.all([page.waitForEvent('popup'),result.getByRole('link',{name:'查看来源'}).click()]);await expect(source.getByText(results.items[0].name,{exact:true}).first()).toBeVisible({timeout:20_000});await expect(source.locator('body')).not.toContainText('page not found');await source.close();
   }
   const previewing=page.waitForResponse(r=>r.url().endsWith('/v1/skills:source')&&r.request().method()==='POST');await result.getByRole('button',{name:'预览并导入',exact:true}).click();const preview=await previewing;expect(preview.status(),await preview.text()).toBe(200);expect((await preview.json()).items).toHaveLength(1);await expect(page.getByRole('dialog',{name:'确认导入 Skill'})).toBeVisible();await page.getByRole('button',{name:/^取\s*消$/}).click();
  }
 }finally{expect((await page.request.post('/auth/logout',{headers:{Origin:origin,'X-CSRF-Token':me.csrf_token}})).status()).toBe(204);}
});
