const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const flush=()=>new Promise(setImmediate);

function fixture(){
 const source=fs.readFileSync(process.env.HC_PATCHED_MODEL_SELECTION_FILE,'utf8');
 const extract=name=>{const start=source.indexOf('var '+name+' = class');assert.ok(start>=0);return source.slice(start,source.indexOf('//#endregion',start));};
 const events=new Map(),remoteEvents=new Map(),requests=[],timers=new Map();let timerId=0;
 const ctx={on:(name,handler)=>events.set(name,handler),remote:{$on:(name,handler)=>remoteEvents.set(name,handler),session:{modelCatalog:()=>new Promise(resolve=>requests.push(resolve))}}};
 const sandbox={setTimeout:fn=>{const id=++timerId;timers.set(id,fn);return id;},clearTimeout:id=>timers.delete(id),_deepseek_ai_dsh_client_store:{createSnapshotStore:initial=>{let state=initial;return {getSnapshot:()=>state,set:value=>{state=value;},update:fn=>{state={...state};fn(state);}};}},_deepseek_ai_cordis:{Service:class{constructor(ctx){this.ctx=ctx;}}},WeakMapWithValues:class{values=[];}};
 const Resolver=vm.runInNewContext(extract('ModelCatalogDirectory')+extract('ModelDirectoryResolver')+';ModelDirectoryResolver',sandbox);
 return {resolver:new Resolver(ctx),events,remoteEvents,requests,timers};
}
const value=n=>({groups:[{id:'platform-models',models:[{id:'approved-'+n}]}],default:{provider:'platform-models',model:'approved-'+n}});
test('first connection makes one shared catalog request; reconnect clears old values',async()=>{
 const {resolver,events,requests}=fixture();assert.equal(requests.length,0);
 events.get('connection/reset')();assert.equal(requests.length,1);
 const first=resolver.catalog.load();assert.equal(resolver.catalog.load(),first);assert.equal(requests.length,1);
 requests[0]({ok:true,value:value(1)});await first;
 assert.equal(resolver.catalog.store.getSnapshot().value.default.model,'approved-1');
 events.get('connection/reset')();assert.equal(requests.length,2);assert.equal(resolver.catalog.store.getSnapshot().value,null);
 const next=resolver.catalog.load();requests[1]({ok:true,value:value(2)});await next;
 assert.equal(resolver.catalog.store.getSnapshot().value.default.model,'approved-2');
});
test('stale response cannot overwrite a new generation; settings and credential changes still reload',async()=>{
 const {resolver,events,remoteEvents,requests}=fixture();events.get('connection/reset')();const old=resolver.catalog.load();
 remoteEvents.get('settings/document-updated')();const current=resolver.catalog.load();assert.equal(requests.length,2);
 requests[1]({ok:true,value:value(2)});await current;requests[0]({ok:true,value:value(1)});await assert.rejects(old,/generation changed/);
 assert.equal(resolver.catalog.store.getSnapshot().value.default.model,'approved-2');
 remoteEvents.get('credentials/reference-updated')();const changed=resolver.catalog.load();assert.equal(requests.length,3);
 requests[2]({ok:true,value:value(3)});await changed;assert.equal(resolver.catalog.store.getSnapshot().value.default.model,'approved-3');
});

test('empty managed directory retries automatically and becomes usable without a reconnect',async()=>{
 const {resolver,events,requests,timers}=fixture();events.get('connection/reset')();const first=resolver.catalog.load();
 requests[0]({ok:true,value:{...value(1),groups:[]}});await flush();
 assert.equal(resolver.catalog.store.getSnapshot().status,'loading');assert.match(resolver.catalog.store.getSnapshot().error,/自动检查/);
 assert.equal(timers.size,1);const [id,run]=[...timers.entries()][0];timers.delete(id);run();await flush();
 assert.equal(requests.length,2);requests[1]({ok:true,value:value(1)});await first;
 assert.equal(resolver.catalog.store.getSnapshot().status,'ready');assert.equal(resolver.catalog.store.getSnapshot().value.groups[0].models[0].id,'approved-1');
});
test('empty catalog reaches a recoverable error and a manual retry can succeed',async()=>{
 const {resolver,events,requests,timers}=fixture();events.get('connection/reset')();const first=resolver.catalog.load();const rejected=assert.rejects(first,/所选模型暂不可用/);
 for(let i=0;i<7;i++){
  requests[i]({ok:true,value:{...value(1),groups:[]}});await flush();
  if(i<6){const [id,run]=[...timers.entries()][0];timers.delete(id);run();await flush();}
 }
 await rejected;assert.equal(resolver.catalog.store.getSnapshot().status,'error');
 const retry=resolver.catalog.load();requests[7]({ok:true,value:value(2)});await retry;assert.equal(resolver.catalog.store.getSnapshot().status,'ready');
});
test('reconnection or disposal cancels pending catalog retry without stale requests',async()=>{
 for(const event of ['connection/reset','dispose']){
 const {resolver,events,requests,timers}=fixture();events.get('connection/reset')();const old=resolver.catalog.load();const rejected=assert.rejects(old,/generation changed/);
 requests[0]({ok:true,value:{...value(1),groups:[]}});await flush();assert.equal(timers.size,1);
 events.get(event)();await rejected;assert.equal(timers.size,0);assert.equal(requests.length,event==='dispose'?1:2);
 if(event!=='dispose'){requests[1]({ok:true,value:value(2)});await resolver.catalog.load();}
 }
});

test('temporary HTTP failures retry, but permission and application errors do not',async()=>{
 for(const message of ['client api: session/modelCatalog failed: transport failure for /api/session/modelCatalog: HTTP 502','client api: session/modelCatalog failed: Failed to fetch']){
  const {resolver,events,requests,timers}=fixture();events.get('connection/reset')();const pending=resolver.catalog.load();
  requests[0]({ok:false,error:{code:'gateway/internal',message}});await flush();assert.equal(timers.size,1);assert.equal(resolver.catalog.store.getSnapshot().status,'loading');
  const [id,run]=[...timers.entries()][0];timers.delete(id);run();await flush();requests[1]({ok:true,value:value(1)});await pending;assert.equal(resolver.catalog.store.getSnapshot().status,'ready');
 }
 for(const [code,message] of [['gateway/forbidden','permission denied'],['gateway/internal','unexpected application error'],['gateway/internal','transport failure for /api/session/modelCatalog: HTTP 401']]){
  const {resolver,events,requests,timers}=fixture();events.get('connection/reset')();const pending=resolver.catalog.load();const rejected=assert.rejects(pending);
  requests[0]({ok:false,error:{code,message}});await rejected;assert.equal(timers.size,0);assert.equal(requests.length,1);assert.equal(resolver.catalog.store.getSnapshot().status,'error');
 }
});
