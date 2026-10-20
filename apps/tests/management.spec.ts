import { expect, test, type Page, type Locator } from '@playwright/test';
import { readFileSync } from 'node:fs';

const admin = { id: '00000000-0000-4000-8000-000000000041', memberships: [{ org_id: '00000000-0000-4000-8000-000000000040', role: 'ADMIN', role_id: '00000000-0000-4000-8000-000000000004', role_name: '管理员', status: 'ACTIVE' }], permissions: ['users:read','users:manage','roles:read','roles:manage'], auth_generation: 1, csrf_token: 'x'.repeat(43), session_expires_at: '2099-01-01T00:00:00Z' };

async function userAction(page: Page, record: Locator, name: string | RegExp) {
  await record.getByRole('button', { name: '更多用户操作', exact: true }).click();
  await page.getByRole('menuitem', { name, exact: true }).click();
}

test('user table keeps actions accessible and role assignment keeps its version check', async ({ page }, info) => {
  const roles = [{ id: 'member-role', name: '成员', code: 'MEMBER' }, { id: 'reviewer-role', name: '审核员', code: 'reviewer' }];
  const target = { id: 'layout-user', display_name: '产品研发团队的工作空间管理员与模型接入负责人', email: 'workspace-administrator-and-model-provider-owner@example.com', role_id: 'member-role', role_name: '成员', status: 'ACTIVE', identity_source: 'local', version: 7, last_login_at: null, password_reset: null };
  let roleID = target.role_id;
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: admin }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: roles, next_cursor: null } }));
  await page.route('**/v1/users?*', r => r.fulfill({ json: { items: [{ ...target, role_id: roleID, role_name: roles.find(role => role.id === roleID)!.name }], total: 1, next_cursor: null } }));
  await page.route('**/v1/users/layout-user:role', async r => {
    expect(r.request().postDataJSON()).toEqual({ role_id: 'reviewer-role', expected_version: 7 });
    expect(await r.request().headerValue('x-csrf-token')).toBe(admin.csrf_token);
    roleID = 'reviewer-role';
    await r.fulfill({ json: { ...target, role_id: roleID, role_name: '审核员', version: 8 } });
  });
  await page.goto('/app/users');
  const record = page.getByRole('row').filter({ hasText: target.email });
  await expect(record.getByRole('button', { name: target.display_name, exact: true })).toBeVisible();
  await expect(record.getByText('尚未登录', { exact: true })).toBeVisible();
  await expect(page.getByRole('searchbox', { name: '搜索用户', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await expect(page.getByRole('columnheader', { name: '用户 / 联系邮箱', exact: true })).toBeVisible();
  await expect(record.getByRole('button', { name: '更多用户操作', exact: true })).toBeInViewport();
  await page.screenshot({ path: info.outputPath('users-table.png'), fullPage: true, animations: 'disabled' });
  await userAction(page, record, '分配角色');
  const dialog = page.getByRole('dialog', { name: '分配角色', exact: true });
  await dialog.getByRole('combobox', { name: /用户角色$/ }).press('ArrowDown');
  await page.locator('.ant-select-item-option-content').filter({ hasText: '审核员 (reviewer)' }).click();
  await dialog.getByRole('button', { name: /^保\s*存$/ }).click();
  await expect(dialog).toHaveCount(0);
  await expect(record.getByText('审核员', { exact: true })).toBeVisible();
});

for (const readOnly of [false, true]) {
  test(`role table keeps protected actions and ${readOnly ? 'read access' : 'versioned edits and deletion'}`, async ({ page }, info) => {
    let roles = [
      { id: 'builtin-role', name: '管理员', code: 'ADMIN', builtin: true, user_count: 1, version: 1, permissions: ['instances:read'] },
      { id: 'used-role', name: '项目成员', code: 'project_member', builtin: false, user_count: 2, version: 3, permissions: ['instances:read'] },
      { id: 'custom-role', name: '研发团队的自定义工作空间审核角色', code: 'workspace_reviewer', builtin: false, user_count: 0, version: 7, permissions: ['instances:read'] },
    ];
    await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
    await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
    await page.route('**/v1/me', r => r.fulfill({ json: { ...admin, permissions: readOnly ? ['roles:read'] : admin.permissions } }));
    await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: roles, total: roles.length, next_cursor: null } }));
    await page.route('**/v1/permissions', r => r.fulfill({ json: { items: [{ id: 'instances:read', name: '查看本人实例', assignable: true, dependencies: [] }] } }));
    await page.route('**/v1/roles/custom-role', async r => {
      const target = roles.find(role => role.id === 'custom-role')!;
      expect(await r.request().headerValue('x-csrf-token')).toBe(admin.csrf_token);
      if (r.request().method() === 'PUT') {
        expect(r.request().postDataJSON()).toEqual({ name: '新版审核角色', code: 'workspace_reviewer', description: '', permissions: ['instances:read'], expected_version: 7 });
        Object.assign(target, { name: '新版审核角色', version: 8 });
        await r.fulfill({ json: target });
      } else {
        expect(r.request().method()).toBe('DELETE');
        expect(r.request().postDataJSON()).toEqual({ expected_version: 8 });
        roles = roles.filter(role => role.id !== target.id);
        await r.fulfill({ status: 204 });
      }
    });
    await page.goto('/app/roles');
    const builtin = page.getByRole('row').filter({ hasText: 'ADMIN' });
    await expect(page.getByRole('searchbox', { name: '搜索角色', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '刷新角色列表', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: info.outputPath(`roles-table-${readOnly ? 'read' : 'manage'}.png`), fullPage: true, animations: 'disabled' });
    await builtin.getByRole('button', { name: '管理员', exact: true }).click();
    const details = page.getByRole('dialog', { name: '角色权限详情', exact: true });
    await expect(details.getByRole('checkbox')).toHaveCount(0);
    await expect(details.getByRole('region', { name: '工作空间', exact: true }).getByText('查看本人实例', { exact: true })).toBeVisible();
    await expect(details.getByText('1 项权限 · 1 名用户', { exact: true })).toBeVisible();
    await details.getByRole('button', { name: /^关\s*闭$/ }).click();
    if (readOnly) {
      await expect(page.getByRole('button', { name: '新增角色', exact: true })).toHaveCount(0);
      await expect(page.getByRole('button', { name: '更多角色操作', exact: true })).toHaveCount(0);
      return;
    }
    await expect(builtin.getByRole('button', { name: /^编\s*辑$/ })).toBeDisabled();
    for (const code of ['ADMIN', 'project_member']) {
      const record = page.getByRole('row').filter({ hasText: code });
      await record.getByRole('button', { name: '更多角色操作', exact: true }).click();
      await expect(page.getByRole('menuitem', { name: '删除角色', exact: true })).toBeDisabled();
      await record.getByRole('button', { name: '更多角色操作', exact: true }).click();
      await expect(page.getByRole('menuitem', { name: '删除角色', exact: true })).toBeHidden();
    }
    const custom = page.getByRole('row').filter({ hasText: 'workspace_reviewer' });
    await custom.getByRole('button', { name: /^编\s*辑$/ }).click();
    const editor = page.getByRole('dialog', { name: '编辑角色权限', exact: true });
    await editor.getByLabel('角色名称', { exact: true }).fill('新版审核角色');
    await editor.getByRole('button', { name: /^确\s*认$/ }).click();
    await expect(editor).toHaveCount(0);
    await expect(custom.getByRole('button', { name: '新版审核角色', exact: true })).toBeVisible();
    await custom.getByRole('button', { name: '更多角色操作', exact: true }).click();
    await page.getByRole('menuitem', { name: '删除角色', exact: true }).click();
    await page.getByRole('dialog', { name: '删除角色', exact: true }).getByRole('button', { name: /^确\s*认$/ }).click();
    await expect(custom).toHaveCount(0);
  });
}

test('role details group granted permissions, preserve unknown permissions and show an empty role', async ({ page }, info) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const catalog = [
    { id: 'instances:read', name: '查看本人实例' },
    { id: 'instances:create', name: '创建本人实例' },
    { id: 'instances:delete', name: '删除本人实例计算' },
    { id: 'personal-model:read', name: '查看本人模型连接' },
    { id: 'personal-model:write', name: '管理本人模型与个人默认' },
    { id: 'audit:read', name: '查看本人操作日志' },
  ];
  const granted = catalog.filter(permission => permission.id !== 'instances:delete').map(permission => permission.id).concat('extension:read');
  await page.route('**/livez', r => r.fulfill({ json: { status: 'alive', process: 'api', capabilities: { authentication: true } } }));
  await page.route('**/readyz', r => r.fulfill({ json: { status: 'ready', process: 'api' } }));
  await page.route('**/v1/me', r => r.fulfill({ json: { ...admin, permissions: ['roles:read'] } }));
  await page.route('**/v1/permissions', r => r.fulfill({ json: { items: catalog } }));
  await page.route('**/v1/roles?*', r => r.fulfill({ json: { items: [
    { id: 'role', name: '项目研发成员', code: 'project_member', description: '使用本人模型创建工作空间，查看相关操作记录。', builtin: false, permissions: granted, user_count: 12 },
    { id: 'empty', name: '待配置角色', code: 'unconfigured', builtin: false, permissions: [], user_count: 0 },
  ], total: 2, next_cursor: null } }));
  await page.goto('/app/roles');
  await page.getByRole('button', { name: '项目研发成员', exact: true }).click();
  const details = page.getByRole('dialog', { name: '角色权限详情', exact: true });
  await expect(details.getByRole('region', { name: '工作空间', exact: true }).getByRole('listitem')).toHaveCount(2);
  await expect(details.getByRole('region', { name: '模型提供商', exact: true }).getByRole('listitem')).toHaveCount(2);
  await expect(details.getByRole('region', { name: '操作日志', exact: true }).getByText('查看本人操作日志')).toBeVisible();
  await expect(details.getByRole('region', { name: '其他权限', exact: true }).getByText('extension:read')).toBeVisible();
  await expect(details.getByText('删除本人实例计算', { exact: true })).toHaveCount(0);
  await expect(details.getByText('6 项权限 · 12 名用户', { exact: true })).toBeVisible();
  await expect(details.getByRole('checkbox')).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.mouse.move(0, 0);
  await details.getByRole('heading', { name: '已拥有权限', exact: true }).click();
  await expect(page.getByRole('tooltip')).toBeHidden();
  await expect.poll(() => details.evaluate(element => getComputedStyle(element).transform)).toBe('none');
  const closeButton = details.getByRole('button', { name: /^关\s*闭$/ });
  await expect(closeButton).toBeInViewport();
  await page.screenshot({ path: info.outputPath('role-permissions.png'), fullPage: true, animations: 'disabled' });
  await closeButton.click();
  await page.getByRole('button', { name: '待配置角色', exact: true }).click();
  await expect(details.getByText('该角色尚未分配权限', { exact: true })).toBeVisible();
  await expect(details.getByRole('region')).toHaveCount(0);
});

test('pending password reset resumes its original operation without another password',async({page})=>{
  await page.clock.install();
  let target={id:'00000000-0000-4000-8000-000000000045',display_name:'待确认用户',email:'pending@example.com',identity_source:'local',status:'ACTIVE',role_name:'成员',version:3,last_login_at:null,password_reset:{id:'00000000-0000-4000-8000-000000000046',status:'PENDING',reason_code:'IDENTITY_UNAVAILABLE'}};
  await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true}}}));
  await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',process:'api'}}));
  await page.route('**/v1/me',r=>r.fulfill({json:admin}));
  await page.route('**/v1/users?*',r=>r.fulfill({json:{items:[target],next_cursor:null,local_accounts:true}}));
  await page.route('**/v1/roles?*',r=>r.fulfill({json:{items:[],next_cursor:null}}));
  await page.route('**/v1/users/'+target.id+':retry-password-reset',async r=>{
    expect(r.request().postDataJSON()).toEqual({expected_version:3});
    expect(await r.request().headerValue('x-csrf-token')).toBe(admin.csrf_token);
    target={...target,version:4,password_reset:{...target.password_reset,status:'SUCCEEDED',reason_code:''}};
    await r.fulfill({json:target});
  });
  await page.goto('/app/users');await expect(page.getByText('密码重置待确认',{exact:true})).toBeVisible();
  const row=page.getByRole('row').filter({hasText:'待确认用户'});
  await row.getByRole('button',{name:'更多用户操作',exact:true}).click();
  await expect(page.getByRole('menuitem',{name:'删除',exact:true})).toBeDisabled();
  await expect(page.getByRole('menuitem',{name:'重置密码',exact:true})).toHaveCount(0);
  await page.getByRole('menuitem',{name:'重试密码重置',exact:true}).click();
  const dialog=page.getByRole('dialog',{name:'重试密码重置',exact:true});
  await expect(dialog.locator('input[type=password]')).toHaveCount(0);
  await dialog.getByRole('button',{name:/确\s*认/}).click();
  await expect(page.getByText('操作已完成',{exact:true})).toBeVisible();
  await row.getByRole('button',{name:'更多用户操作',exact:true}).click();
  await expect(page.getByRole('menuitem',{name:'重置密码',exact:true})).toBeVisible();
  await page.keyboard.press('Escape');
  await page.clock.fastForward(4500);
  await expect(page.getByText('操作已完成',{exact:true})).toHaveCount(0);
});

async function selectPermission(page:Page,id:string){
  const checkbox=page.getByRole('checkbox',{name:new RegExp(id)});
  await expect(async()=>{await checkbox.setChecked(true);await expect(checkbox).toBeChecked();}).toPass({timeout:5000});
}

async function login(page:Page,email:string,password:string){
  await page.goto('/auth/login');await page.locator('input[name=login]').fill(email);await page.locator('input[name=password]').fill(password);
  const submitted=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/oidc/login');
  await page.getByRole('button',{name:'登录',exact:true}).click();return(await submitted).status();
}

test('real administrator creates a local user, manages permissions and resets the login password',async({browser},testInfo)=>{
  test.skip(process.env.HC_REAL_MANAGEMENT!=='true','requires deployed platform and embedded platform OIDC');
  test.setTimeout(180_000);
  const contexts=[];const context=await browser.newContext({baseURL:testInfo.project.use.baseURL,ignoreHTTPSErrors:true});contexts.push(context);
  const page=await context.newPage();page.setDefaultTimeout(10_000);const suffix=crypto.randomUUID().slice(0,8),roleName='验收角色 '+suffix,roleCode='review_'+suffix,userName='用户治理验收 '+suffix,email='qa_'+suffix+'@harness.local';
  const originalPassword='Qa!'+crypto.randomUUID(),newPassword='Qa!'+crypto.randomUUID();
  let createdRole:{id:string;version:number}|undefined,createdUser:{id:string;version:number}|undefined;
  let csrf='',memberRole='';
  const headers=()=>({Origin:new URL(page.url()).origin,'X-CSRF-Token':csrf});
  const command=async(suffix:string,data:unknown)=>{
    const response=await page.request.post('/v1/users/'+createdUser!.id+suffix,{headers:headers(),data});expect(response.status(), 'user action '+suffix+' status').toBe(200);createdUser=await response.json();return createdUser!;
  };
  const confirm=async(path:string,method='POST')=>{
    const accepted=page.waitForResponse(r=>new URL(r.url()).pathname===path&&r.request().method()===method);
    await page.getByRole('dialog').getByRole('button',{name:/确\s*认/}).click();const response=await accepted;expect(response.status()).toBe(200);
    await expect(page.getByRole('dialog')).toHaveCount(0);return response.json();
  };
  const userRow=()=>page.getByRole('row').filter({hasText:userName});
  const freshLogin=async(password:string,invalid=false)=>{
    const c=await browser.newContext({baseURL:testInfo.project.use.baseURL,ignoreHTTPSErrors:true});contexts.push(c);const p=await c.newPage();const status=await login(p,email,password);if(invalid)expect(status).toBe(401);return p;
  };
  try{
    await login(page,'admin@harness.local',readFileSync('../.local/auth/admin-password','utf8').trim());await expect(page).toHaveURL(/\/app\/instances$/);
    const me=await page.request.get('/v1/me');expect(me.status()).toBe(200);csrf=(await me.json()).csrf_token;
    const builtin=await page.request.get('/v1/roles?limit=100');expect(builtin.status()).toBe(200);memberRole=(await builtin.json()).items.find((r:{code:string})=>r.code==='MEMBER').id;
    await page.goto('/app/roles');await page.getByRole('button',{name:'新增角色',exact:true}).click();
    await page.getByLabel('角色编码',{exact:true}).fill(roleCode);await page.getByLabel('角色名称',{exact:true}).fill(roleName);await selectPermission(page,'instances:read');
    const roleResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/roles'&&r.request().method()==='POST');
    await page.getByRole('dialog').getByRole('button',{name:/确\s*认/}).click();const roleAccepted=await roleResponse;expect(roleAccepted.status()).toBe(201);createdRole=await roleAccepted.json();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await page.goto('/app/users');await page.getByRole('button',{name:'新增用户',exact:true}).click();
    await page.getByLabel('显示名',{exact:true}).fill(userName);await page.getByLabel('联系邮箱',{exact:true}).fill(email);
    await expect(page.getByLabel('初始密码',{exact:true})).toBeVisible();
    await page.getByRole('dialog').getByRole('combobox',{name:/角色$/}).press('ArrowDown');await page.locator('.ant-select-item-option-content').filter({hasText:roleName+' ('+roleCode+')'}).click();
    await page.getByLabel('初始密码',{exact:true}).fill(originalPassword);
    const userResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/v1/users'&&r.request().method()==='POST');
    await page.getByRole('dialog').getByRole('button',{name:/确\s*认/}).click();const userAccepted=await userResponse;expect(userAccepted.status()).toBe(201);createdUser=await userAccepted.json();
    await expect(page.getByRole('dialog')).toHaveCount(0);await expect(userRow().getByText('已启用',{exact:true})).toBeVisible();
    let member=await freshLogin(originalPassword);await expect(member).toHaveURL(/\/app\/instances$/);
    let identity=await member.request.get('/v1/me');let projection=await identity.json();expect(projection.id).toBe(createdUser!.id);expect(projection.permissions).toEqual(['instances:read']);
    expect((await member.request.get('/v1/users')).status()).toBe(403);expect((await member.request.get('/v1/my-models')).status()).toBe(403);
    await expect(member.getByRole('link',{name:'用户管理',exact:true})).toHaveCount(0);await expect(member.getByRole('link',{name:'模型提供商',exact:true})).toHaveCount(0);

    await page.goto('/app/roles');const roleRow=page.getByRole('row').filter({hasText:roleCode});await roleRow.getByRole('button',{name:/^编\s*辑$/}).click();
    await selectPermission(page,'personal-model:read');await selectPermission(page,'personal-model:write');
    createdRole=await confirm('/v1/roles/'+createdRole!.id,'PUT');expect((await member.request.get('/v1/me')).status()).toBe(401);
    member=await freshLogin(originalPassword);await expect(member).toHaveURL(/\/app\/instances$/);identity=await member.request.get('/v1/me');projection=await identity.json();
    expect(projection.permissions).toContain('personal-model:write');expect((await member.request.get('/v1/model-catalog')).status()).toBe(200);expect((await member.request.get('/v1/users')).status()).toBe(403);

    await page.goto('/app/users');await page.getByRole('searchbox',{name:'搜索用户',exact:true}).fill(userName);await page.getByRole('searchbox',{name:'搜索用户',exact:true}).press('Enter');
    await userAction(page,userRow(),'重置密码');await page.getByLabel('新密码',{exact:true}).fill(newPassword);
    createdUser=await confirm('/v1/users/'+createdUser!.id+':reset-password');expect((createdUser as {password_reset?:{status:string}}).password_reset?.status).toBe('SUCCEEDED');expect((await member.request.get('/v1/me')).status()).toBe(401);
    const old=await freshLogin(originalPassword,true);await expect(old).toHaveURL(/\/oidc\//);expect((await old.request.get('/v1/me')).status()).toBe(401);
    member=await freshLogin(newPassword);await expect(member).toHaveURL(/\/app\/instances$/);
    await userAction(page,userRow(),'禁用');createdUser=await confirm('/v1/users/'+createdUser!.id+':disable');expect((await member.request.get('/v1/me')).status()).toBe(401);
    const disabled=await freshLogin(newPassword);await expect(disabled.getByRole('alert')).toBeVisible();
    await userAction(page,userRow(),'启用');createdUser=await confirm('/v1/users/'+createdUser!.id+':enable');
    member=await freshLogin(newPassword);await expect(member).toHaveURL(/\/app\/instances$/);
    expect(await page.evaluate(()=>JSON.stringify({localStorage,sessionStorage}))).not.toContain(newPassword);
  }finally{
    testInfo.setTimeout(testInfo.timeout+45_000);
    if(createdUser&&csrf){
      const current=await page.request.get('/v1/users/'+createdUser.id);expect(current.status()).toBe(200);createdUser=await current.json();
      const user=await command(':role',{expected_version:createdUser!.version,role_id:memberRole});
      let version=user.version;
      if((user as {status?:string}).status==='ACTIVE')version=(await command(':disable',{expected_version:version})).version;
      await command(':archive',{expected_version:version});
    }
    if(createdRole&&csrf){
      const role=await page.request.get('/v1/roles/'+createdRole.id);expect(role.status()).toBe(200);const current=await role.json();
      expect((await page.request.delete('/v1/roles/'+createdRole.id,{headers:headers(),data:{expected_version:current.version}})).status()).toBe(204);
    }
    for(const c of contexts)await c.close();
  }
});
