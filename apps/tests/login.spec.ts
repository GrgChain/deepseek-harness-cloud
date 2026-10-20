import { expect,test } from '@playwright/test';
import { readFileSync } from 'node:fs';

test.describe('real local OIDC',()=>{
  test.skip(process.env.HC_REAL_IDENTITY!=='true','requires embedded platform OIDC, private test credentials and API');

  for(const role of ['admin','member'] as const){
    test(`${role} can log in, restore session and log out`,async({page,context},testInfo)=>{
      await page.goto('/login');
      const login=page.getByRole('button',{name:'登录账户',exact:true});
      await expect(login).toBeEnabled();await login.click();
      await expect(page.getByRole('heading',{name:'登录账户'})).toBeVisible();
      await page.locator('input[name=login]').fill(`${role}@harness.local`);
      await page.locator('input[name=password]').fill(readFileSync(`../.local/auth/${role}-password`,'utf8').trim());
      await page.getByRole('button',{name:'登录',exact:true}).click();
      await expect(page).toHaveURL(/\/app\/instances$/);
      await expect(page.getByRole('button',{name:role==='admin'?'管理员账户':'成员账户',exact:true})).toBeVisible();
      const cookies=await context.cookies();const session=cookies.find(c=>c.name==='__Host-hc_session');
      expect(session?.secure).toBe(true);expect(session?.httpOnly).toBe(true);expect(session?.sameSite).toBe('Lax');
      const me=await page.request.get('/v1/me');expect(me.status()).toBe(200);const user=await me.json();expect(user.memberships[0].role).toBe(role==='admin'?'ADMIN':'MEMBER');
      await page.reload();await expect(page.getByRole('button',{name:role==='admin'?'管理员账户':'成员账户',exact:true})).toBeVisible();
      await expect(page.getByRole('heading',{name:'工作空间',exact:true})).toBeVisible();
      await expect(page.getByRole('button',{name:'创建配置',exact:true})).toHaveCount(0);
      await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
      await expect(page.getByRole('region',{name:'运行资源'})).toBeVisible();
      await expect(page.getByRole('region',{name:'使用模型'})).toBeVisible();
      await page.getByRole('button',{name:'取消',exact:true}).click();
      await expect(page.getByText('本地开发环境',{exact:true})).toHaveCount(0);
      await expect(page.getByText('平台 API 已连接',{exact:true})).toHaveCount(0);
      expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
      await page.screenshot({path:testInfo.outputPath(`${role}-workbench.png`),fullPage:true,animations:'disabled'});
      for(const [path,title] of [['models','模型提供商'],...(role==='admin'?[['users','用户管理'],['roles','角色与权限']]:[])]){
        const loaded=page.waitForResponse(response=>response.request().method()==='GET'&&new URL(response.url()).pathname===(path==='models'?'/v1/my-provider-accounts':'/v1/'+path));
        await page.goto('/app/'+path);
        const response=await loaded;
        const count=(await response.json()).items.length;
        await expect(page.locator(path==='models'?'.provider-account':'.ant-table-tbody .ant-table-row')).toHaveCount(Math.min(count,10));
        await expect(page.getByRole('heading',{name:title,exact:true,level:1})).toBeVisible();
        await expect(page.locator('.ant-spin-spinning')).toHaveCount(0);
        expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
        if(path==='users')expect(await page.locator('.user-profile .ant-btn-link').evaluateAll(buttons=>buttons.length>0&&buttons.every(button=>button.getBoundingClientRect().right<=button.parentElement!.getBoundingClientRect().right))).toBe(true);
        await page.screenshot({path:testInfo.outputPath(`${role}-${path}.png`),fullPage:true,animations:'disabled'});
      }
      const instances=await page.request.get('/v1/instances?limit=20');expect(instances.status()).toBe(200);const visible=(await instances.json()).items;expect(Array.isArray(visible)).toBe(true);expect(visible.every((item:{owner_id:string})=>item.owner_id===user.id)).toBe(true);
      const operationLogs=await page.request.get('/v1/audit-events?limit=20');expect(operationLogs.status()).toBe(200);expect(Array.isArray((await operationLogs.json()).items)).toBe(true);
      if(role==='member'){
        await page.goto('/app/settings');
        await expect(page.getByRole('heading',{name:'默认运行配置',exact:true})).toBeVisible();
        await expect(page.getByRole('button',{name:/^(修改配置|初始化配置)$/})).toHaveCount(0);
        await expect(page.getByRole('link',{name:'用户管理',exact:true})).toHaveCount(0);
      }
      const denied=await page.request.post('/auth/logout',{headers:{Origin:'https://sibling.example','X-CSRF-Token':user.csrf_token}});expect(denied.status()).toBe(403);
      await page.goto('/login');await expect(page.getByText('账户已登录',{exact:true})).toBeVisible();
      await page.screenshot({path:testInfo.outputPath(`${role}-signed-in.png`),fullPage:true});
      await page.getByRole('button',{name:'退出登录',exact:true}).click();
      await expect(page.getByRole('button',{name:'登录账户',exact:true})).toBeEnabled();
      expect((await page.request.get('/v1/me')).status()).toBe(401);
      expect((await context.cookies()).some(c=>c.name==='__Host-hc_session')).toBe(false);
    });
  }

  test('wrong password and unmapped identity cannot access platform',async({page})=>{
    await page.goto('/auth/login');
    await page.locator('input[name=login]').fill('admin@harness.local');await page.locator('input[name=password]').fill('intentionally-wrong');
    await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/oidc\//);
    expect((await page.request.get('/v1/me')).status()).toBe(401);
    await page.goto('/auth/login');
    await page.locator('input[name=login]').fill('unmapped@harness.local');await page.locator('input[name=password]').fill(readFileSync('../.local/auth/unmapped-password','utf8').trim());
    await page.getByRole('button',{name:'登录',exact:true}).click();
    await expect(page.getByText(/此账户未获平台授权/)).toBeVisible();
    expect((await page.request.get('/v1/me')).status()).toBe(401);
  });
});
