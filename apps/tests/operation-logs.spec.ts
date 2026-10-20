import { expect, test, type Page } from '@playwright/test';
const user={id:'00000000-0000-4000-8000-000000000041',memberships:[{org_id:'00000000-0000-4000-8000-000000000040',role:'MEMBER',role_id:'member-role',role_name:'成员',status:'ACTIVE'}],permissions:['audit:read'],csrf_token:'x'.repeat(43),auth_generation:1,session_expires_at:'2099-01-01T00:00:00Z'};
const resource='00000000-0000-4000-8000-000000000042';
const event={id:'00000000-0000-4000-8000-000000000043',actor_id:user.id,actor_name:'管理员林',owner_id:resource,resource_id:resource,resource_name:'测试成员',resource_type:'user',action:'USER_QUOTA_UPDATE',outcome:'SUCCEEDED',trace_id:'operation-log-request-example',timestamp:new Date().toISOString(),details:{before:{max_instances:1,cpu_millicores:1000,memory_bytes:2*2**30,storage_bytes:10*2**30},after:{max_instances:2,cpu_millicores:2000,memory_bytes:4*2**30,storage_bytes:20*2**30}}};
async function setup(page:Page){
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));await page.route('**/v1/me',r=>r.fulfill({json:user}));
 const queries:URL[]=[];
 await page.route('**/v1/audit-events?*',r=>{queries.push(new URL(r.request().url()));return r.fulfill({json:{items:[event],next_cursor:null,total:1}});});
 await page.goto('/app/audit');return queries;
}
test('operation logs shows readable operator object outcome and quota changes in a details drawer',async({page},info)=>{
 await setup(page);const row=page.getByRole('row').filter({hasText:'管理员林'});
 await expect(row).toContainText('调整资源配额');await expect(row).toContainText('测试成员');await expect(row).toContainText('成功');
 await row.getByRole('button',{name:/^详\s*情$/}).click();const drawer=page.getByRole('dialog',{name:'操作日志详情'});
 await expect(drawer.getByRole('row').filter({hasText:'CPU'})).toContainText('1 vCPU');await expect(drawer.getByRole('row').filter({hasText:'CPU'})).toContainText('2 vCPU');
 await expect(drawer.getByRole('row').filter({hasText:'内存'})).toContainText('4 GiB');
 await drawer.getByText('关联信息',{exact:true}).click();await expect(drawer.getByText('operation-log-request-example',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:info.outputPath('operation-log-details.png'),animations:'disabled'});
 await drawer.getByRole('button',{name:'查看此对象记录'}).click();await expect(drawer).not.toBeVisible();await expect(page.getByPlaceholder('操作对象编号')).toHaveValue(resource);
});
test('operation logs filters send action outcome date and object without changing authorization scope',async({page})=>{
 const queries=await setup(page);
 async function choose(label:string,title:string){await page.getByRole('combobox',{name:label}).locator('..').locator('..').click();const dropdown=page.locator('.ant-select-dropdown:visible');const input=page.getByRole('combobox',{name:label});if(label==='日志操作类型')await input.fill(title);await dropdown.getByTitle(title,{exact:true}).click();}
 await choose('日志操作类型','创建工作空间');await expect.poll(()=>queries.at(-1)?.searchParams.get('action')).toBe('INSTANCE_CREATE');
 await choose('日志操作结果','失败');await expect.poll(()=>queries.at(-1)?.searchParams.get('outcome')).toBe('FAILED');
 await choose('操作日志时间范围','最近7天');await expect.poll(()=>queries.at(-1)?.searchParams.has('from')).toBe(true);
 await page.getByPlaceholder('操作对象编号').fill(resource);await page.getByPlaceholder('操作对象编号').press('Enter');await expect.poll(()=>queries.at(-1)?.searchParams.get('resource_id')).toBe(resource);
 expect(queries.at(-1)?.searchParams.has('owner_id')).toBe(false);
});
