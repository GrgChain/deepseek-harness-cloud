if (state.managedCatalog) {
 const jsx = react_jsx_runtime.jsx, jsxs = react_jsx_runtime.jsxs;
 const classes = ModelsSection_module_css_default;
 const english = t('title') === 'Models';
 const copy = english ? {
  managed:'Models are managed in the console.', manage:'Manage model providers', apply:'Open workspace details',
  hint:'After saving a provider, apply it from workspace details.', empty:'No models are available. Check your provider in the console.',
  refresh:'Refresh models', current:'Default', failed:'Some providers are unavailable. Try refreshing.'
 } : {
  managed:'模型由控制台统一管理。', manage:'管理模型提供商', apply:'打开实例详情',
  hint:'保存提供商后，请到实例详情应用更新。', empty:'暂无可用模型，请在控制台检查模型提供商。',
  refresh:'刷新目录', current:'当前默认', failed:'部分提供商暂不可用，请刷新重试。'
 };
 const catalog = state.managedCatalog;
 return jsxs('section', {className:classes.section, 'data-hc-managed-models':'', 'aria-label':t('title'), children:[
  jsx('style',{children:`
   [data-hc-managed-models] a,[data-hc-managed-models] button{min-height:44px;height:auto;white-space:normal;overflow-wrap:anywhere;padding:10px 16px;box-sizing:border-box}
   [data-hc-managed-models] li{min-width:0;overflow-wrap:anywhere}
   [data-hc-managed-models]{gap:10px}
   [data-shortcut-modal="settings"]:has([data-hc-managed-models]){height:min(560px,calc(100vh - 2 * max(24px,var(--dsh-frame-overlay-top,24px))))}
   [data-hc-managed-models] [data-hc-managed-models-lead]{max-width:58ch}
   [data-hc-managed-models] [data-hc-managed-models-directory]{margin:2px 0 0;gap:8px}
   [data-hc-managed-models] [data-hc-managed-models-provider]{gap:8px;padding:12px 16px;border-radius:var(--dsw-radius-md)}
   [data-hc-managed-models] [data-hc-managed-models-provider-name]{font-size:13px;font-weight:500;line-height:20px}
   [data-hc-managed-models] [data-hc-managed-models-models]{flex-direction:row;flex-wrap:wrap;align-items:center;gap:8px 20px;margin:0}
   [data-hc-managed-models] [data-hc-managed-models-entry]{display:inline-flex;align-items:center;gap:8px}
   [data-hc-managed-models] [data-hc-managed-models-current]{border:0;border-radius:999px;background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary);padding:2px 8px;font-size:11px;line-height:16px}
   [data-hc-managed-models] [data-hc-managed-models-hint]{margin-top:2px}
   [data-hc-managed-models-actions]{display:grid;grid-template-columns:max-content max-content max-content;align-items:center;gap:8px;margin-top:4px}
   [data-hc-managed-models-actions]>:first-child{grid-column:auto}
   [data-hc-managed-models-actions]>a,[data-hc-managed-models-actions]>button{margin:0;justify-self:start;text-align:center}
   @media(max-width:600px){
    [data-shortcut-modal="settings"]:has([data-hc-managed-models]){height:min(520px,calc(100dvh - 32px));flex-direction:column}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav{width:100%;padding:18px 16px 0;gap:12px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:first-child{padding:0 40px 0 0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:last-child{flex-direction:row;overflow-x:auto;flex-shrink:0;padding-bottom:4px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav button{flex-shrink:0;min-height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div{min-height:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child{height:16px;padding:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child>button{position:absolute;top:12px;right:10px;width:44px;height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:last-child{padding:0 16px 20px}
    [data-hc-managed-models-actions]{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}
    [data-hc-managed-models-actions]>:first-child{grid-column:1/-1}
    [data-hc-managed-models-actions]>a,[data-hc-managed-models-actions]>button{width:100%;justify-self:stretch}
   }`}),
  jsx('h2',{className:classes.title,children:t('title')}),
  jsx('p',{className:classes.intro,'data-hc-managed-models-lead':'',children:copy.managed}),
  catalog.groups.length ? jsx('ul',{className:classes.rows,'data-hc-managed-models-directory':'',children:catalog.groups.map(group=>jsxs('li',{
   className:classes.rowCard,'data-hc-managed-models-provider':'', children:[
    jsx('h3',{className:classes.rowName,'data-hc-managed-models-provider-name':'',children:group.name}),
    jsx('ul',{className:classes.rows,'data-hc-managed-models-models':'',children:group.models.map(model=>jsxs('li',{
     'data-hc-managed-models-entry':'',children:[
     jsx('span',{children:model.name || model.id}),
     catalog.default?.provider === group.id && catalog.default?.model === model.id ? jsx('span',{className:classes.rowTag,'data-hc-managed-models-current':'',children:copy.current}) : null
    ]},model.id))})
   ]},group.id))}) : jsx('p',{className:classes.notice,children:copy.empty}),
  catalog.failures.length ? jsx('p',{role:'status',className:classes.notice,children:copy.failed}) : null,
  jsx('p',{className:classes.intro,'data-hc-managed-models-hint':'',children:copy.hint}),
  jsxs('div',{'data-hc-managed-models-actions':'',children:[
   jsx('a',{className:classes.primaryButton,style:{textDecoration:'none'},href:HC_MODELS_URL,target:'_blank',rel:'noopener noreferrer',children:copy.manage}),
   jsx('a',{className:classes.secondaryButton,style:{textDecoration:'none'},href:HC_INSTANCE_URL,target:'_blank',rel:'noopener noreferrer',children:copy.apply}),
   jsx('button',{className:classes.secondaryButton,type:'button',onClick:()=>controller.load(),children:copy.refresh})
  ]})
 ]});
}
