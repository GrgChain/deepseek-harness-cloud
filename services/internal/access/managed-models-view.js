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
   @media(max-width:600px){
    [data-shortcut-modal="settings"]:has([data-hc-managed-models]){flex-direction:column}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav{width:100%;padding:18px 16px 0;gap:12px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:first-child{padding:0 40px 0 0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav>div:last-child{flex-direction:row;overflow-x:auto;flex-shrink:0;padding-bottom:4px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>nav button{flex-shrink:0;min-height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div{min-height:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child{height:16px;padding:0}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:first-child>button{position:absolute;top:12px;right:10px;width:44px;height:44px}
    [data-shortcut-modal="settings"]:has([data-hc-managed-models])>div>div:last-child{padding:0 16px 20px}
   }`}),
  jsx('h2',{className:classes.title,children:t('title')}),
  jsx('p',{className:classes.intro,children:copy.managed}),
  catalog.groups.length ? jsx('ul',{className:classes.rows,children:catalog.groups.map(group=>jsxs('li',{
   className:classes.rowCard, children:[
    jsx('h3',{className:classes.rowName,children:group.name}),
    jsx('ul',{className:classes.rows,children:group.models.map(model=>jsxs('li',{children:[
     jsx('span',{children:model.name || model.id}),
     catalog.default?.provider === group.id && catalog.default?.model === model.id ? jsx('span',{className:classes.rowTag,style:{marginInlineStart:8},children:copy.current}) : null
    ]},model.id))})
   ]},group.id))}) : jsx('p',{className:classes.notice,children:copy.empty}),
  catalog.failures.length ? jsx('p',{role:'status',className:classes.notice,children:copy.failed}) : null,
  jsx('p',{className:classes.intro,children:copy.hint}),
  jsxs('div',{style:{display:'flex',flexWrap:'wrap',gap:8},children:[
   jsx('a',{className:classes.primaryButton,style:{textDecoration:'none'},href:HC_MODELS_URL,target:'_blank',rel:'noopener noreferrer',children:copy.manage}),
   jsx('a',{className:classes.secondaryButton,style:{textDecoration:'none'},href:HC_INSTANCE_URL,target:'_blank',rel:'noopener noreferrer',children:copy.apply}),
   jsx('button',{className:classes.secondaryButton,type:'button',onClick:()=>controller.load(),children:copy.refresh})
  ]})
 ]});
}
