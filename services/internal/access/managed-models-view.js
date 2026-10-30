if (state.managedCatalog) {
 const jsx = react_jsx_runtime.jsx, jsxs = react_jsx_runtime.jsxs;
 const classes = ModelsSection_module_css_default;
 const english = t('title') === 'Models';
 const copy = english ? {
  managed:'Models are managed in the console.', search:'Search models',
  hint:'After saving a provider, apply it from workspace details.', empty:'No models are available. Check your provider in the console.',
  noMatches:'No matching models.', current:'Default', count:'models', failed:'Some providers are unavailable. Try reopening model settings.'
 } : {
  managed:'模型由控制台统一管理。', search:'搜索模型',
  hint:'保存提供商后，请到实例详情应用更新。', empty:'暂无可用模型，请在控制台检查模型提供商。',
  noMatches:'没有匹配的模型。', current:'当前默认', count:'个模型', failed:'部分提供商暂不可用，请重新打开模型设置重试。'
 };
 const catalog = state.managedCatalog;
 const query = managedModelQuery.trim().toLocaleLowerCase();
 const groups = catalog.groups.map(group=>({...group,models:group.models.filter(model=>`${group.name} ${group.id} ${model.name || ''} ${model.id}`.toLocaleLowerCase().includes(query))})).filter(group=>group.models.length);
 return jsxs('section', {className:classes.section, 'data-hc-managed-models':'', 'aria-label':t('title'), children:[
  jsx('style',{children:`
   [data-hc-managed-models]{width:100%;max-width:760px;gap:14px;container:hc-model-directory/inline-size;color:var(--dsw-alias-label-primary)}
   [data-hc-managed-models] h2{margin:0;font-size:18px;font-weight:600;line-height:26px}
   [data-hc-managed-models] [data-hc-managed-models-lead]{margin:0;color:var(--dsw-alias-label-tertiary);font-size:13px;line-height:20px}
   [data-hc-managed-models-search]{position:relative;display:flex;align-items:center;color:var(--dsw-alias-label-tertiary)}
   [data-hc-managed-models-search]>svg{position:absolute;left:12px;pointer-events:none}
   [data-hc-managed-models-search]>input{box-sizing:border-box;width:100%;height:36px;min-width:0;padding:0 34px 0 36px;border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-layer-1);color:var(--dsw-alias-label-primary);font:inherit;font-size:13px;outline:none}
   [data-hc-managed-models-search]>input::placeholder{color:var(--dsw-alias-label-tertiary)}
   [data-hc-managed-models-search]>input:focus-visible{border-color:var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));box-shadow:0 0 0 2px color-mix(in srgb,var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary)) 18%,transparent)}
   [data-hc-managed-models] [data-hc-managed-models-directory]{display:flex;flex-direction:column;gap:14px;margin:0;padding:0;list-style:none}
   [data-hc-managed-models-provider]{display:flex;flex-direction:column;gap:10px;min-width:0}
   [data-hc-managed-models-provider]+[data-hc-managed-models-provider]{border-top:.5px solid var(--dsw-alias-border-l2);padding-top:14px}
   [data-hc-managed-models-provider-heading]{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:36px}
   [data-hc-managed-models-provider-name]{margin:0;font-size:14px;font-weight:400;line-height:22px;overflow-wrap:anywhere}
   [data-hc-managed-models-count]{flex:none;color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:18px;font-variant-numeric:tabular-nums}
   [data-hc-managed-models] [data-hc-managed-models-models]{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin:0;padding:0;list-style:none}
   [data-hc-managed-models-entry]{box-sizing:border-box;display:flex;flex-direction:column;gap:6px;min-width:0;min-height:60px;padding:12px 14px;border:.5px solid var(--dsw-alias-settings-card-stroke);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-settings-card-fill)}
   [data-hc-managed-models-model-heading]{display:flex;align-items:center;justify-content:space-between;gap:8px;min-width:0}
   [data-hc-managed-models-model-name]{min-width:0;overflow-wrap:anywhere;font-size:14px;font-weight:500;line-height:20px}
   [data-hc-managed-models-current]{flex:none;border:0;border-radius:999px;background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary);padding:2px 8px;font-size:11px;line-height:16px}
   [data-hc-managed-models] [data-hc-managed-models-hint],[data-hc-managed-models] [data-hc-managed-models-empty]{margin:0;color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:18px}
   @container hc-model-directory (width<=520px){[data-hc-managed-models] [data-hc-managed-models-models]{grid-template-columns:minmax(0,1fr)}}
   @media(max-width:600px){
    [data-shortcut-modal="settings"]:has([data-hc-managed-models]){height:min(720px,calc(100dvh - 32px));flex-direction:column}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav{width:100%;padding:18px 16px 0;gap:12px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:first-child{padding:0 40px 0 0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:last-child{flex-direction:row;overflow-x:auto;flex-shrink:0;padding-bottom:4px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav button{flex-shrink:0;min-height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div{min-height:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child{height:16px;padding:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child>button{position:absolute;top:12px;right:10px;width:44px;height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:last-child{padding:0 16px 20px}
    [data-hc-managed-models-search]>input{height:44px}
   }`}),
  jsx('h2',{className:classes.title,children:t('title')}),
  jsx('p',{'data-hc-managed-models-lead':'',children:copy.managed}),
  jsxs('label',{'data-hc-managed-models-search':'',children:[
   jsx('svg',{width:16,height:16,viewBox:'0 0 24 24',fill:'none',stroke:'currentColor',strokeWidth:1.5,'aria-hidden':'true',children:[jsx('circle',{cx:10.5,cy:10.5,r:6.5},'circle'),jsx('path',{d:'m16 16 4.5 4.5'},'handle')]}),
   jsx('input',{type:'search','aria-label':copy.search,placeholder:copy.search,value:managedModelQuery,onChange:event=>setManagedModelQuery(event.currentTarget.value)})
  ]}),
  groups.length ? jsx('ul',{'data-hc-managed-models-directory':'',children:groups.map(group=>jsxs('li',{
   'data-hc-managed-models-provider':'', children:[
    jsxs('div',{'data-hc-managed-models-provider-heading':'',children:[
     jsx('h3',{'data-hc-managed-models-provider-name':'',children:group.name}),
     jsx('span',{'data-hc-managed-models-count':'',children:`${group.models.length} ${copy.count}`})
    ]}),
    jsx('ul',{'data-hc-managed-models-models':'',children:group.models.map(model=>jsxs('li',{
     'data-hc-managed-models-entry':'',children:[
      jsxs('div',{'data-hc-managed-models-model-heading':'',children:[
       jsx('span',{'data-hc-managed-models-model-name':'',children:model.name || model.id}),
       catalog.default?.provider === group.id && catalog.default?.model === model.id ? jsx('span',{'data-hc-managed-models-current':'',children:copy.current}) : null
      ]})
     ]},model.id))})
   ]},group.id))}) : jsx('p',{'data-hc-managed-models-empty':'',role:'status',children:catalog.groups.length?copy.noMatches:copy.empty}),
  catalog.failures.length ? jsx('p',{role:'status',className:classes.notice,children:copy.failed}) : null,
  jsx('p',{'data-hc-managed-models-hint':'',children:copy.hint})
 ]});
}
