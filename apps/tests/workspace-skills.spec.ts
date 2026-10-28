import { expect, test, type Page } from '@playwright/test';

const user={id:'00000000-0000-4000-8000-000000000001',memberships:[{org_id:'00000000-0000-4000-8000-000000000002',role:'MEMBER',status:'ACTIVE',role_id:'00000000-0000-4000-8000-000000000004',role_name:'成员'}],permissions:['instances:read','instances:create','instances:start','configuration:read','personal-model:read','skills:read'],auth_generation:1,csrf_token:'synthetic-csrf-token-32-characters-long',session_expires_at:'2099-01-01T00:00:00Z'};
const instance='00000000-0000-4000-8000-000000000003';
const operation='00000000-0000-4000-8000-000000000004';
const model={id:'00000000-0000-4000-8000-000000000061',version:1,enabled:true,credential_configured:true,sync_status:'SYNCED',validation:{status:'HEALTHY'},gateway_check:{status:'HEALTHY'}};
const skill=(n:number)=>({id:`00000000-0000-4000-8000-${String(n).padStart(12,'0')}`,name:`skill-${n}`,description:`技能 ${n} 的用途`,version:7,size_bytes:1024,file_count:2});
async function session(page:Page,readSkills=true){
 await page.emulateMedia({reducedMotion:'reduce'});
 await page.route('**/v1/**',r=>r.fulfill({status:404,json:{code:'NOT_FOUND'}}));
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true,instances:false}}}));
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions:readSkills?user.permissions:user.permissions.filter(value=>value!=='skills:read')}}));
 await page.route('**/v1/instances?*',r=>r.fulfill({json:{items:[],next_cursor:null}}));
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[{id:'account',name:'个人账号',provider:'moonshot',status:'ACTIVE',default_model_id:model.id,models:[model]}]}}));
 await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:[{id:'moonshot',name:'Moonshot AI'}]}}));
 await page.route('**/v1/preflight*',r=>r.fulfill({json:{can_create:true,checks:[]}}));
}
async function open(page:Page){
 await page.goto('/app/instances');
 await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await page.getByRole('combobox',{name:'创建时的模型提供商'}).locator('..').locator('..').click();
 await page.locator('.ant-select-dropdown:visible').getByTitle('Moonshot AI · 个人账号',{exact:true}).click();
}

test('workspace skill multi-select includes later pages and replays the same selected versions',async({page})=>{
 await session(page);
 const pages:number[]=[];
 await page.route('**/v1/skills?*',r=>{
  const page=Number(new URL(r.request().url()).searchParams.get('page'));pages.push(page);
  return r.fulfill({json:{items:page===1?Array.from({length:100},(_,n)=>skill(n+100)): [skill(999)],total:101,page,limit:100}});
 });
 const bodies:unknown[]=[];const keys:string[]=[];
 await page.route('**/v1/instances',r=>{
  bodies.push(r.request().postDataJSON());keys.push(r.request().headers()['idempotency-key']);
  return bodies.length===1?r.abort('connectionreset'):r.fulfill({status:202,json:{instance_id:instance,operation_id:operation,state:'CREATING'}});
 });
 await open(page);
 const picker=page.getByRole('combobox',{name:'创建时的技能',exact:true});
 await picker.click();await picker.fill('skill-999');
 await page.locator('.ant-select-dropdown:visible').getByText('skill-999',{exact:true}).click();
 await picker.fill('skill-100');
 await page.locator('.ant-select-dropdown:visible').getByText('skill-100',{exact:true}).click();
 await picker.press('Escape');
 await picker.press('Backspace');
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认创建结果',exact:true})).toBeEnabled();
 expect(bodies[0]).toEqual({model_configuration_id:model.id,model_configuration_version:1,skills:[{id:skill(999).id,version:7}]});
 expect(pages).toEqual([1,2]);
 await page.reload();
 await page.getByRole('button',{name:'查看待确认创建',exact:true}).click();
 await page.getByRole('button',{name:'确认创建结果',exact:true}).click();
 await expect(page).toHaveURL(new RegExp('/app/instances/'+instance+'$'));
 expect(bodies[1]).toEqual(bodies[0]);expect(keys[1]).toBe(keys[0]);
 const saved=await page.evaluate(()=>JSON.stringify({...sessionStorage}));
 expect(saved).not.toContain('技能 999 的用途');expect(saved).not.toContain('SKILL.md');
});

test('skill loading can retry and clearing the selection allows creation without skills',async({page})=>{
 await session(page);let failing=true;const bodies:Record<string,unknown>[]=[];
 await page.route('**/v1/skills?*',r=>failing?r.fulfill({status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}):r.fulfill({json:{items:[skill(999)],total:1,page:1,limit:100}}));
 await page.route('**/v1/instances',r=>{bodies.push(r.request().postDataJSON());return r.fulfill({status:202,json:{instance_id:instance,operation_id:operation,state:'CREATING'}});});
 await open(page);
 await expect(page.getByText('技能加载失败，可重试或不选择技能继续创建')).toBeVisible();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 failing=false;await page.getByRole('button',{name:'重试',exact:true}).click();
 const picker=page.getByRole('combobox',{name:'创建时的技能',exact:true});
 await expect(picker).toBeEnabled();await picker.click();await picker.fill('skill-999');
 await page.locator('.ant-select-dropdown:visible').getByText('skill-999',{exact:true}).click();
 await picker.press('Escape');
 await picker.press('Backspace');
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page).toHaveURL(new RegExp('/app/instances/'+instance+'$'));
 expect(bodies[0]).not.toHaveProperty('skills');
});

test('workspace creation does not fetch personal skills without permission',async({page})=>{
 await session(page,false);let reads=0;
 await page.route('**/v1/skills?*',r=>{reads++;return r.fulfill({status:403,json:{code:'FORBIDDEN'}});});
 await open(page);
 await expect(page.getByRole('combobox',{name:'创建时的技能'})).toHaveCount(0);
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 expect(reads).toBe(0);
});

test('a stale selected version must be cleared and selected again after rejection',async({page})=>{
 await session(page);let version=7;const bodies:{skills:{id:string;version:number}[]}[]=[];
 await page.route('**/v1/skills?*',r=>r.fulfill({json:{items:[{...skill(999),version}],total:1,page:1,limit:100}}));
 await page.route('**/v1/instances',r=>{
  bodies.push(r.request().postDataJSON());
  if(bodies.length===1){version=8;return r.fulfill({status:409,json:{code:'VERSION_CONFLICT',message:'Skill 已被修改，请刷新后重新操作'}});}
  return r.fulfill({status:202,json:{instance_id:instance,operation_id:operation,state:'CREATING'}});
 });
 await open(page);
 const picker=page.getByRole('combobox',{name:'创建时的技能',exact:true});
 const choose=async()=>{await picker.click();await picker.fill('skill-999');await page.locator('.ant-select-dropdown:visible').getByText('skill-999',{exact:true}).click();await picker.press('Escape');};
 await choose();await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByText('所选技能已更新或不可用，请重新加载并重新选择')).toBeVisible();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 expect(bodies[0].skills).toEqual([{id:skill(999).id,version:7}]);
 await page.getByRole('button',{name:'清空并重新加载技能',exact:true}).click();
 await choose();await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page).toHaveURL(new RegExp('/app/instances/'+instance+'$'));
 expect(bodies[1].skills).toEqual([{id:skill(999).id,version:8}]);
});


test('creation submits more than 32 skills with over 12 MiB combined',async({page})=>{
 await session(page);
 const items=Array.from({length:33},(_,n)=>({...skill(n+100),size_bytes:512*2**10}));
 await page.route('**/v1/skills?*',r=>r.fulfill({json:{items,total:items.length,page:1,limit:100}}));
 let body:unknown;
 await page.route('**/v1/instances',r=>{
  body=r.request().postDataJSON();
  return r.fulfill({status:202,json:{instance_id:instance,operation_id:operation,state:'CREATING'}});
 });
 await open(page);
 const picker=page.getByRole('combobox',{name:'创建时的技能',exact:true});
 await picker.click();
 for(const item of items){
  await picker.fill(item.name);
  await page.locator('.ant-select-dropdown:visible').getByText(item.name,{exact:true}).click();
 }
 await picker.press('Escape');
 await expect(page.getByRole('region',{name:'选择技能',exact:true}).getByText('已选 33 项',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page).toHaveURL(new RegExp('/app/instances/'+instance+'$'));
 expect(body).toEqual({model_configuration_id:model.id,model_configuration_version:1,skills:items.map(({id,version})=>({id,version}))});
});
