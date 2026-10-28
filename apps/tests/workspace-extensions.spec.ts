import {expect,test,type Page} from '@playwright/test';
const id=(n:number)=>`00000000-0000-4000-8000-${String(n).padStart(12,'0')}`;
const user={id:id(1),memberships:[{org_id:id(2),role:'MEMBER',status:'ACTIVE',role_id:id(4),role_name:'成员'}],permissions:['instances:read','instances:create','instances:start','instances:configure','configuration:read','personal-model:read','connectors:read','connectors:manage','connectors:invoke','memory:read','memory:manage','knowledge:read','knowledge:manage'],auth_generation:1,csrf_token:'synthetic-csrf-token-32-characters-long',session_expires_at:'2099-01-01T00:00:00Z'};
const model={id:id(61),version:1,enabled:true,credential_configured:true,sync_status:'SYNCED',validation:{status:'HEALTHY'},gateway_check:{status:'HEALTHY'}};
const connector={id:id(10),name:'研发工具',description:'读取开发信息',catalog_id:'github',version:3,catalog_version:4,enabled:true,status:'READY',tools:[{name:'read_issue',enabled:true}]};
const source=(kind:string)=>({id:id(kind==='memory'?11:12),kind,backend:kind==='memory'?'mem0':'qdrant',name:kind==='memory'?'项目记忆':'项目知识库',version:2,enabled:true,status:'READY'});
async function setup(page:Page,permissions=user.permissions){
 await page.route('**/v1/**',r=>r.fulfill({status:404,json:{code:'NOT_FOUND'}}));
 await page.route('**/livez',r=>r.fulfill({json:{status:'alive',process:'api',capabilities:{authentication:true,instances:false}}}));
 await page.route('**/v1/me',r=>r.fulfill({json:{...user,permissions}}));
 await page.route('**/v1/instances?*',r=>r.fulfill({json:{items:[],next_cursor:null}}));
 await page.route('**/v1/my-provider-accounts',r=>r.fulfill({json:{items:[{id:'account',name:'账号',provider:'moonshot',status:'ACTIVE',default_model_id:model.id,models:[model]}]}}));
 await page.route('**/v1/model-providers',r=>r.fulfill({json:{items:[{id:'moonshot',name:'Moonshot AI'}]}}));
 await page.route('**/v1/preflight*',r=>r.fulfill({json:{can_create:true,checks:[]}}));
 const spec={id:id(70),cpu_millicores:1000,memory_bytes:2*2**30,storage_bytes:10*2**30};
 await page.route('**/v1/defaults',r=>r.fulfill({json:{template_id:id(71),template_version:id(72),spec}}));
 await page.route('**/v1/templates',r=>r.fulfill({json:{items:[{id:id(71),template_version:id(72),spec}]}}));
 await page.route('**/v1/connectors',r=>r.fulfill({json:{items:[connector]}}));
 for(const kind of ['memory','knowledge'])await page.route('**/v1/context/'+kind,r=>r.fulfill({json:{items:[source(kind)],endpoints:[],bindings:[]}}));
 await page.route('**/v1/workspace-extensions/plugins',r=>r.fulfill({json:{items:[{id:'session-search',name:'会话搜索',version:'0.2.1-alpha.2',package:'@deepseek-ai/dsh-experimental-session-search',available:true},{id:'dsh-ssh',name:'DSH SSH',version:'0.4.11',package:'@linxin666/dsh-ssh',available:false,reason:'与当前 DSH 版本不兼容'}]}}));
}
async function choose(page:Page,label:string,name:string){const picker=page.getByRole('combobox',{name:label,exact:true});await picker.click();if(label==='创建时的模型提供商'){await page.locator('.ant-select-dropdown:visible').getByTitle(name,{exact:true}).click();return;}await picker.fill(name);await page.locator('.ant-select-dropdown:visible').getByText(name,{exact:true}).click();await picker.press('Escape');}
async function open(page:Page){await page.goto('/app/instances');await page.getByRole('button',{name:'创建工作空间',exact:true}).click();await choose(page,'创建时的模型提供商','Moonshot AI · 账号');}

test('four extension groups submit exact references and retain them during uncertain replay',async({page},info)=>{
 await setup(page);const bodies:Record<string,unknown>[]=[];const keys:string[]=[];
 await page.route('**/v1/instances',r=>{bodies.push(r.request().postDataJSON());keys.push(r.request().headers()['idempotency-key']);return bodies.length===1?r.abort('connectionreset'):r.fulfill({status:202,json:{instance_id:id(31),operation_id:id(32),state:'CREATING'}});});
 await open(page);
 await choose(page,'创建时的连接器','研发工具');await choose(page,'创建时的记忆','项目记忆');await choose(page,'创建时的知识库','项目知识库');await choose(page,'创建时的插件','会话搜索');
 await page.locator('section[aria-label="选择知识库"]').getByLabel('个人',{exact:true}).uncheck();
 await page.getByRole('heading',{name:'扩展能力',exact:false}).click();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();
 await page.screenshot({path:info.outputPath('selected-extensions.png'),fullPage:true});
 await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByRole('button',{name:'确认创建结果',exact:true})).toBeEnabled();
 expect(bodies[0]).toMatchObject({connectors:[{id:id(10),version:3,catalog_version:4}],memory:[{id:id(11),version:2,scopes:['personal','workspace']}],knowledge:[{id:id(12),version:2,scopes:['workspace']}],plugins:[{id:'session-search',version:'0.2.1-alpha.2'}]});
 await page.reload();await page.getByRole('button',{name:'查看待确认创建',exact:true}).click();await page.getByRole('button',{name:'确认创建结果',exact:true}).click();await expect(page).toHaveURL(new RegExp('/app/instances/'+id(31)+'$'));
 expect(bodies[1]).toEqual(bodies[0]);expect(keys[1]).toBe(keys[0]);
 const saved=await page.evaluate(()=>JSON.stringify({...sessionStorage}));expect(saved).not.toContain('项目记忆');expect(saved).not.toContain('credential');
});

test('stale extensions block a new request until explicitly reselected',async({page})=>{
 await setup(page);let version=3;let attempts=0;
 await page.route('**/v1/connectors',r=>r.fulfill({json:{items:[{...connector,version}]}}));
 await page.route('**/v1/instances',r=>{attempts++;version=4;return r.fulfill({status:409,json:{code:'VERSION_CONFLICT',message:'连接器已更新，请重新选择'}});});
 await open(page);await choose(page,'创建时的连接器','研发工具');await page.getByRole('button',{name:'创建并运行',exact:true}).click();
 await expect(page.getByText('所选连接器已更新或不可用，请重新加载并重新选择')).toBeVisible();await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
 await page.getByRole('button',{name:'清空并重新加载连接器',exact:true}).click();await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();expect(attempts).toBe(1);
});

test('unselected service outages do not prevent base creation and incompatible plugins cannot be selected',async({page})=>{
 await setup(page);await page.route('**/v1/context/memory',r=>r.fulfill({status:503,json:{code:'DEPENDENCY_UNAVAILABLE'}}));await open(page);
 await expect(page.getByText('记忆加载失败，未选择时可继续创建')).toBeVisible();await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeEnabled();
 const picker=page.getByRole('combobox',{name:'创建时的插件',exact:true});await picker.click();await picker.fill('DSH SSH');await expect(page.locator('.ant-select-item-option-disabled')).toContainText('与当前 DSH 版本不兼容');await picker.press('Escape');
});

test('no configuration permission hides all extension groups without fetching private resources',async({page})=>{
 await setup(page,user.permissions.filter(permission=>permission!=='instances:configure'));let reads=0;await page.route('**/v1/context/*',r=>{reads++;return r.fulfill({json:{items:[]}});});await open(page);
 for(const label of ['连接器','记忆','知识库','插件'])await expect(page.getByRole('combobox',{name:'创建时的'+label,exact:true})).toHaveCount(0);expect(reads).toBe(0);
});

test('creation groups stay compact and reopening starts with fresh extension choices',async({page},info)=>{
 await setup(page);await open(page);
 const connectorGroup=page.getByRole('region',{name:'选择连接器',exact:true});
 const memoryGroup=page.getByRole('region',{name:'选择记忆',exact:true});
 const connectorBox=await connectorGroup.boundingBox();const memoryBox=await memoryGroup.boundingBox();
 expect(connectorBox&&memoryBox).toBeTruthy();
 if(info.project.name==='desktop')expect(memoryBox!.x).toBeGreaterThan(connectorBox!.x);else expect(Math.abs(memoryBox!.x-connectorBox!.x)).toBeLessThan(2);
 await expect(page.getByRole('dialog').getByRole('link',{name:/管理/})).toHaveCount(0);
 await expect(connectorGroup.locator('label').filter({hasText:/^连接器$/})).toHaveCount(0);
 await choose(page,'创建时的连接器','研发工具');await choose(page,'创建时的记忆','项目记忆');
 await page.getByRole('heading',{name:'选择记忆',exact:false}).click();
 await expect(page.locator('.create-instance-summary')).toContainText('2 项扩展');
 await page.getByRole('button',{name:'取消',exact:true}).click();
 await page.getByRole('button',{name:'创建工作空间',exact:true}).click();
 await expect(connectorGroup.locator('.ant-select-selection-item')).toHaveCount(0);
 await expect(memoryGroup.locator('.ant-select-selection-item')).toHaveCount(0);
 await expect(page.locator('.create-instance-summary')).toContainText('请选择模型提供商');
 await expect(page.getByRole('button',{name:'创建并运行',exact:true})).toBeDisabled();
});
