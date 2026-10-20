const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const source=fs.readFileSync(__dirname+'/managed-directory.js','utf8');
test('picker scopes browsing, breadcrumbs and creation to workspace',async()=>{
 const calls=[];
 const signal=new AbortController().signal;
 const ctx={uiWorkspace:{
  listDirectory:async(path,receivedSignal)=>{
   calls.push(['list',path]);assert.equal(receivedSignal,signal);
   return {path,home:'/home/node',crumbs:[{path:'/'},{path:'/data'},{path:'/data/workspace'},{path:'/data/workspace/project'}],
    entries:[{path:'/data/workspace/project'},{path:'/data/dsh'},{path:'/data/workspace-other'}],truncated:true};
  },
  createDirectory:async(path,name)=>{calls.push(['create',path,name]);return path+'/'+name;}
 },locale:{bind:()=>((...args)=>args)}};
 const api=new Function('ctx','LOCALE_NS',source+'\nreturn injected();')(ctx,'directory-browser');
 const listing=await api.listDirectory(undefined,signal);
 assert.equal(listing.path,'/data/workspace');assert.equal(listing.home,'/data/workspace');
 assert.deepEqual(listing.crumbs.map(row=>row.path),['/data/workspace','/data/workspace/project']);
 assert.deepEqual(listing.entries.map(row=>row.path),['/data/workspace/project']);assert.equal(listing.truncated,true);
 await api.listDirectory('/data/workspace/a/../project/',signal);
 assert.deepEqual(calls.at(-1),['list','/data/workspace/project']);
 const count=calls.length;
 for(const path of ['/','/data','/home/node','/data/workspace-other','/data/workspace/../dsh','/data/workspace/a/../../../etc','relative','/data/workspace/\\..',null]){
  await assert.rejects(api.listDirectory(path,signal),/workspace/);
  await assert.rejects(api.createDirectory(path,'probe'),/workspace/);
 }
 assert.equal(calls.length,count,'rejected paths must not reach native RPC');
 for(const name of ['..','.','a/b','a\\b','', ' ','a\0b'])await assert.rejects(api.createDirectory('/data/workspace',name));
 assert.equal(calls.length,count);
 assert.equal(await api.createDirectory('/data/workspace/project/','测试'),'/data/workspace/project/测试');
 assert.equal(api.t('browser.home'),'workspace');
 assert.deepEqual(api.t('browser.createIn',{name:'project'}),['browser.createIn',{name:'project'}]);
});
