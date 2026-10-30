// Uses the existing, immutable runtime image and the native profile format.
import fs from 'node:fs';
import path from 'node:path';
import {createHash,randomUUID} from 'node:crypto';

export function installExtensions(root,request){
 const marker=JSON.parse(fs.readFileSync(path.join(root,'.hc-initialized'),'utf8'));
 if(marker.schema_version!==1||['instance_id','asset_id','org_id','owner_id'].some(key=>marker[key]!==request[key]))throw Error('Storage identity mismatch');
 const digest=createHash('sha256').update(JSON.stringify(request.extensions)).digest('hex');
 const exists=value=>{try{return fs.lstatSync(value);}catch(error){if(error.code==='ENOENT')return null;throw error;}};
 const initialized=path.join(root,'.hc-extensions-initialized');
 if(exists(initialized)){if(!exists(initialized).isFile()||fs.readFileSync(initialized,'utf8')!==digest)throw Error('Extension identity mismatch');return;}
 const directory=value=>{const info=exists(value);if(info&&!info.isDirectory())throw Error('Unsafe profile directory');if(!info)fs.mkdirSync(value,{mode:0o700});};
 const home=path.join(root,'dsh');directory(home);const profiles=path.join(home,'profiles');directory(profiles);
 const staging=fs.mkdtempSync(path.join(profiles,'.hc-extensions-'));const target=path.join(profiles,'web');
 try{
  const bundles=['@deepseek-ai/dsh-base','@deepseek-ai/dsh-web-app'];
  for(const item of request.extensions.items){if(item.kind==='plugin'){
   if(!/^@deepseek-ai\/dsh-experimental-[a-z-]+$/.test(item.package)||item.package_version!=='0.2.1-alpha.2')throw Error('Unapproved plugin');
   const metadata=JSON.parse(fs.readFileSync('/opt/harness-runtime/node_modules/'+item.package+'/package.json','utf8'));
   if(metadata.version!==item.package_version||!metadata.dsh?.bundle)throw Error('Incompatible plugin');bundles.push(item.package);
  }}
  const files={
   'package.json':JSON.stringify({name:'dsh-profile-web',private:true,dependencies:{},dsh:{profile:{bundles}}}),
   'cordis.patch.yml':JSON.stringify([{insert:[{id:'harness-cloud-extensions',name:'/data/dsh/profiles/web/cloud-extensions.mjs'}]}]),
   'cloud-extensions.mjs':request.program,
   'cloud-extensions.json':JSON.stringify(request.extensions),
   'pnpm-workspace.yaml':'packages:\n  - .\n\nnodeLinker: hoisted\nautoInstallPeers: false\n',
  };
  for(const [name,content]of Object.entries(files)){const fd=fs.openSync(path.join(staging,name),'wx',0o600);try{fs.writeFileSync(fd,content);fs.fsyncSync(fd);}finally{fs.closeSync(fd);}}
  if(exists(target)){
   if(!exists(target).isDirectory())throw Error('Unsafe existing profile');
   const actual=fs.readdirSync(target).sort();if(JSON.stringify(actual)!==JSON.stringify(Object.keys(files).sort()))throw Error('Existing profile differs');
   for(const [name,content]of Object.entries(files)){const file=path.join(target,name);if(!exists(file)?.isFile()||fs.readFileSync(file,'utf8')!==content)throw Error('Existing profile differs');}
  }else fs.renameSync(staging,target);
  const temporary=path.join(root,'.hc-extensions-'+randomUUID());const fd=fs.openSync(temporary,'wx',0o600);try{fs.writeFileSync(fd,digest);fs.fsyncSync(fd);}finally{fs.closeSync(fd);}fs.renameSync(temporary,initialized);
  for(const folder of [target,profiles,root]){const fd=fs.openSync(folder,'r');try{fs.fsyncSync(fd);}finally{fs.closeSync(fd);}}
 }finally{fs.rmSync(staging,{recursive:true,force:true});}
}
if(process.argv[1]==='--install'){
 try{const raw=fs.readFileSync(0);if(raw.length>2**20)throw Error('Oversized request');installExtensions('/data',JSON.parse(raw));process.stdout.write(JSON.stringify({installed:true}));}
 catch{process.stderr.write('Workspace extension initialization failed\n');process.exitCode=1;}
}
