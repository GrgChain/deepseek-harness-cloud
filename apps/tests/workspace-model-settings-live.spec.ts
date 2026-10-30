import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';

test.use({locale:'zh-CN'});

test('remote workspace model settings shows its managed directory without local settings', async ({page,context},testInfo) => {
 test.skip(process.env.HC_REAL_IDENTITY !== 'true' || !process.env.HC_ENTRY_INSTANCE_ID, 'Requires an existing local owner workspace');
 await page.goto('/auth/login');
 await page.locator('input[name=login]').fill('admin@harness.local');
 await page.locator('input[name=password]').fill(readFileSync('../.local/auth/admin-password','utf8').trim());
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await expect(page).toHaveURL(/\/app\/instances$/);
 let adapted = false, failures = 0;
 if(process.env.HC_CATALOG_TRANSPORT_FAILURE==='true')await context.route('**/api/session/modelCatalog',async route=>{
  if(failures<2){failures++;await route.fulfill({status:502,json:{code:'UPSTREAM_UNAVAILABLE',message:'Test transient transport failure'}});}else await route.continue();
 });
 context.on('response',async response=>{if(response.url().includes('ui-settings-models'))adapted=(await response.text()).includes('state.managedCatalog = catalog.value');});
 await page.goto('/app/instances/'+process.env.HC_ENTRY_INSTANCE_ID);
 const opened=page.waitForEvent('popup');
 await page.getByRole('button',{name:'进入工作空间',exact:true}).click();
 const workspace=await opened;
 await expect(workspace.locator('#root')).not.toBeEmpty();
 await expect.poll(()=>adapted).toBe(true);
 const preview=workspace.getByRole('button',{name:/^(继续|Continue)$/});
 await expect(preview).toBeVisible({timeout:15000});await preview.click();
 await expect(async()=>{
  const notice=workspace.getByRole('button',{name:/^(继续|Continue)$/});
  if(await notice.isVisible())await notice.click();
  if(await workspace.getByText('模型由控制台统一管理。',{exact:true}).isVisible())return;
  const models=workspace.getByRole('button',{name:/^(模型|Models)$/});
  if(!await models.isVisible())await workspace.getByRole('button',{name:/^(设置|Settings)$/}).click({timeout:1500});
  await models.click({timeout:1500});
 }).toPass({timeout:20000,intervals:[500,1000]});
 await expect(workspace.getByText('模型由控制台统一管理。',{exact:true})).toBeVisible({timeout:20000});
 await expect(workspace.getByText(/加载提供商目录失败|settings are unavailable/)).toHaveCount(0);
 await expect(workspace.getByText('平台模型网关',{exact:true}).first()).toBeVisible();
 if(process.env.HC_CATALOG_TRANSPORT_FAILURE==='true')expect(failures).toBe(2);
 for(const name of ['管理模型提供商','打开实例详情'])await expect(workspace.getByRole('link',{name,exact:true})).toHaveCount(0);
 await expect(workspace.getByRole('button',{name:'刷新目录',exact:true})).toHaveCount(0);
 expect(await workspace.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 const section=workspace.locator('[data-hc-managed-models]');
 expect((await section.boundingBox())!.width).toBeGreaterThan(250);
 await expect(section.locator('a,button')).toHaveCount(0);
 const entries=section.locator('[data-hc-managed-models-entry]');const count=await entries.count();expect(count).toBeGreaterThan(0);
 const search=section.getByRole('searchbox',{name:'搜索模型',exact:true});const firstName=await entries.first().locator('[data-hc-managed-models-model-name]').innerText();
 await search.fill(firstName);await expect(entries).toHaveCount(1);
 await search.fill('不存在的模型-qa');await expect(section.getByRole('status')).toHaveText('没有匹配的模型。');await expect(entries).toHaveCount(0);
 await search.clear();await expect(entries).toHaveCount(count);
 for(const entry of await entries.all())expect(await entry.evaluate(element=>element.scrollWidth<=element.clientWidth)).toBe(true);
 const cards=section.locator('[data-hc-managed-models-models]').first();const columns=await cards.evaluate(element=>getComputedStyle(element).gridTemplateColumns.split(' ').length);expect(columns).toBe(testInfo.project.name==='mobile'?1:2);
 await workspace.screenshot({path:testInfo.outputPath('model-settings.png'),fullPage:true,animations:'disabled'});
 await context.unrouteAll({behavior:'wait'});await workspace.close();await page.goto('/login');await page.getByRole('button',{name:'退出登录',exact:true}).click();
});
