import {expect,test} from '@playwright/test';
import {readFileSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
import {zipSync,strToU8} from 'fflate';

test('imports a large skill through the real portal and removes the fixture',async({page})=>{
 test.skip(process.env.HC_LARGE_SKILL_LIVE!=='true','Requires local owner credentials');
 test.setTimeout(120000);
 const name='import-check-'+randomBytes(6).toString('hex');
 const files:Record<string,Uint8Array>={'SKILL.md':strToU8(`---\nname: ${name}\ndescription: Temporary import verification\n---\nTest fixture\n`),'assets/large.bin':new Uint8Array(13*1024*1024)};
 for(let i=0;i<150;i++)files[`assets/file-${i}.txt`]=strToU8('fixture');
 const archive=zipSync(files,{level:0});
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 const me=await (await page.request.get('/v1/me')).json();
 const headers={'Origin':new URL(page.url()).origin,'X-CSRF-Token':me.csrf_token};
 let id='';let version=0;
 try {
  const response=await page.request.post('/v1/skills:preview',{headers,data:{filename:'fixture.zip',content_base64:Buffer.from(archive).toString('base64')},timeout:90000});
  expect(response.status(),await response.text()).toBe(200);
  const preview=await response.json();expect(preview.items[0].files).toHaveLength(152);
  const applied=await page.request.post('/v1/skills:apply',{headers,data:{token:preview.token,mode:'skip'},timeout:90000});
  expect(applied.status(),await applied.text()).toBe(200);
  const list=await page.request.get('/v1/skills?'+new URLSearchParams({q:name,page:'1',limit:'10'}));
  const items=(await list.json()).items;expect(items).toHaveLength(1);id=items[0].id;version=items[0].version;
  expect(items[0].file_count).toBe(152);expect(items[0].size_bytes).toBeGreaterThan(13*1024*1024);
 } finally {
  if(!id){const list=await page.request.get('/v1/skills?'+new URLSearchParams({q:name,page:'1',limit:'10'}));if(list.ok()){const found=(await list.json()).items.find((item:{name:string})=>item.name===name);if(found){id=found.id;version=found.version;}}}
  if(id){const deleted=await page.request.delete('/v1/skills/'+id,{headers,data:{confirmed_name:name,expected_version:version}});expect(deleted.status()).toBe(200);}
 }
});
