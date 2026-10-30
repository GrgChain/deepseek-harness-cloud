const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const vm=require('node:vm');
const flush=()=>new Promise(setImmediate);
function fixture(){
 const source=fs.readFileSync(process.env.HC_PATCHED_MODELS_FILE,'utf8');const start=source.indexOf('var ModelsSettingsStore = class');assert.ok(start>=0);const code=source.slice(start,source.indexOf('//#endregion',start));
 const requests=[],timers=new Map(),events=new Map();let id=0;
 const ctx={on:(n,f)=>events.set(n,f),remote:{$host:{isLoopback:false},session:{modelCatalog:()=>new Promise(resolve=>requests.push(resolve))}}};
 const sandbox={setTimeout:fn=>{timers.set(++id,fn);return id;},clearTimeout:id=>timers.delete(id),_deepseek_ai_dsh_client_store:{createSnapshotStore:state=>({getSnapshot:()=>state,update:f=>{state={...state};f(state);}})}};
 const Store=vm.runInNewContext(code+';ModelsSettingsStore',sandbox);
 return {store:new Store(ctx,{},{}),requests,timers,events,advance:async()=>{const[id,fn]=[...timers.entries()][0];timers.delete(id);fn();await flush();}};
}
const healthy={ok:true,value:{default:{provider:'platform-models',model:'approved'},routableProviders:['platform-models'],groups:[{id:'platform-models',models:[{id:'approved'}]}]}};
const failure={ok:false,error:{code:'gateway/internal',message:'transport failure for /api/session/modelCatalog: HTTP 502'}};
test('settings recovers a failed initial read automatically',async()=>{const f=fixture(),pending=f.store.load();f.requests[0](failure);await flush();assert.equal(f.store.store.getSnapshot().status,'loading');await f.advance();f.requests[1](healthy);await pending;assert.equal(f.store.store.getSnapshot().status,'ready');assert.equal(f.store.store.getSnapshot().managedCatalog.default.model,'approved');});
test('exhausted transport retries become an error with manual recovery',async()=>{const f=fixture(),pending=f.store.load();for(let i=0;i<5;i++){f.requests[i](failure);await flush();if(i<4)await f.advance();}await pending;assert.equal(f.store.store.getSnapshot().status,'error');const retry=f.store.load();f.requests[5](healthy);await retry;assert.equal(f.store.store.getSnapshot().status,'ready');});
test('superseding refresh and disposal release timers and pending callers',async()=>{for(const dispose of [false,true]){const f=fixture(),old=f.store.load();f.requests[0](failure);await flush();assert.equal(f.timers.size,1);if(dispose){f.events.get('dispose')();await old;assert.equal(f.requests.length,1);}else{const next=f.store.load();f.requests[1](healthy);await next;await old;}assert.equal(f.timers.size,0);}});
test('authorization failures do not retry',async()=>{const f=fixture(),pending=f.store.load();f.requests[0]({ok:false,error:{code:'gateway/forbidden',message:'permission denied'}});await pending;assert.equal(f.store.store.getSnapshot().status,'error');assert.equal(f.timers.size,0);});
