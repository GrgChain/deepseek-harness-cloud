import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {installExtensions} from './install_extensions.mjs';

function fixture(t){const root=fs.mkdtempSync(path.join(os.tmpdir(),'hc-extensions-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const request={schema_version:1,instance_id:'instance',asset_id:'asset',org_id:'org',owner_id:'owner',extensions:{items:[{kind:'memory',id:'memory',name:'Memory',version:1,scopes:['personal']}]},program:'export function apply() {}'};fs.writeFileSync(path.join(root,'.hc-initialized'),JSON.stringify(request));return {root,request};}
test('native profile initialization is atomic, recoverable and preserves later user edits',t=>{
 const {root,request}=fixture(t);installExtensions(root,request);const profile=path.join(root,'dsh/profiles/web');
 const metadata=JSON.parse(fs.readFileSync(path.join(profile,'package.json'),'utf8'));assert.deepEqual(metadata.dsh.profile.bundles,['@deepseek-ai/dsh-base','@deepseek-ai/dsh-web-app']);
 fs.unlinkSync(path.join(root,'.hc-extensions-initialized'));installExtensions(root,request);
 fs.writeFileSync(path.join(profile,'cordis.patch.yml'),'user changes');installExtensions(root,request);assert.equal(fs.readFileSync(path.join(profile,'cordis.patch.yml'),'utf8'),'user changes');
 assert.throws(()=>installExtensions(root,{...request,extensions:{items:[]}}),/identity mismatch/);
});
test('rejects a different original volume, symlink directories and conflicting profiles',t=>{
 const a=fixture(t);assert.throws(()=>installExtensions(a.root,{...a.request,owner_id:'someone-else'}),/identity mismatch/);
 fs.symlinkSync(os.tmpdir(),path.join(a.root,'dsh'));assert.throws(()=>installExtensions(a.root,a.request),/Unsafe profile directory/);
 const b=fixture(t);fs.mkdirSync(path.join(b.root,'dsh/profiles/web'),{recursive:true});fs.writeFileSync(path.join(b.root,'dsh/profiles/web/package.json'),'user data');assert.throws(()=>installExtensions(b.root,b.request),/Existing profile differs/);assert.equal(fs.readFileSync(path.join(b.root,'dsh/profiles/web/package.json'),'utf8'),'user data');
});
