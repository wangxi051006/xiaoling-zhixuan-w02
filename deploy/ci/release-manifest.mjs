import assert from 'node:assert/strict';
import {readFile,writeFile,readdir,stat} from 'node:fs/promises';
import {resolve,relative} from 'node:path';
import {createHash} from 'node:crypto';
const [mode,folder]=process.argv.slice(2);assert(folder,'release directory required');const root=resolve(folder);
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const canonical=value=>JSON.stringify(value,null,2)+'\n';
async function walk(dir){const paths=[];for(const e of (await readdir(dir,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name,'en'))){const p=resolve(dir,e.name);assert(!e.isSymbolicLink(),'release symlink forbidden');if(e.isDirectory())paths.push(...await walk(p));else paths.push(p);}return paths;}
const migrationPattern=/^[0-9]{3}_[0-9]{3}_[a-z][a-z0-9_]*\.sql$/;
async function readFileSet(dir,{sqlOnly}){
 const entries=await readdir(dir,{withFileTypes:true});
 const rows=[];
 for(const entry of entries.sort((a,b)=>a.name.localeCompare(b.name,'en'))){
  // Enumerate like go:embed *.sql and the packaging glob, then validate names.
  if(sqlOnly&&!entry.name.endsWith('.sql'))continue;
  assert(entry.isFile(),`migration must be a regular file: ${entry.name}`);
  assert(migrationPattern.test(entry.name),`invalid migration filename: ${entry.name}`);
  const bytes=await readFile(resolve(dir,entry.name));
  rows.push({name:entry.name,bytes:bytes.length,sha256:hash(bytes)});
 }
 return rows;
}
if(mode==='metadata'){
 assert(process.env.CANDIDATE_ID&&/^(?:[a-f0-9]{40}|LOCAL-REHEARSAL-[A-Za-z0-9._-]+)$/.test(process.env.CANDIDATE_ID),'reviewable candidate ID required');assert(/^\d+$/.test(process.env.SOURCE_DATE_EPOCH??''),'SOURCE_DATE_EPOCH required');
 const lock=JSON.parse(await readFile(new URL('./migration-lock.json',import.meta.url),'utf8'));
 assert(Array.isArray(lock)&&lock.length>0,'approved migration lock required');
 for(const item of lock)assert.deepEqual(Object.keys(item).sort(),['name','sha256'],'migration lock entries must be {name,sha256}');
 const approved=lock.map(({name,sha256})=>({name,sha256}));
 const source=await readFileSet('backend/migrations',{sqlOnly:true});
 assert.equal(source.length,approved.length,'source migration count differs from approved lock');
 assert.deepEqual(source.map(({name,sha256})=>({name,sha256})),approved,'source migrations differ from approved lock');
 const packaged=await readFileSet(resolve(root,'migrations'),{sqlOnly:false});
 assert.equal(packaged.length,approved.length,'packaged migration count differs from approved lock');
 assert.deepEqual(packaged.map(({name,sha256})=>({name,sha256})),approved,'packaged migrations differ from approved lock');
 await writeFile(resolve(root,'migrations.json'),canonical(source));
 await writeFile(resolve(root,'VERSION.json'),canonical({candidate:process.env.CANDIDATE_ID,level:process.env.CANDIDATE_ID.startsWith('LOCAL-')?'LOCAL-REHEARSAL':'CI-CANDIDATE-PREPARATION',preparationEnvironment:process.env.PREPARATION_ENVIRONMENT??'LOCAL-REHEARSAL',sourceDateEpoch:Number(process.env.SOURCE_DATE_EPOCH),productionRelease:'NO-GO',toolchain:JSON.parse(await readFile('deploy/ci/toolchain-lock.json','utf8'))}));
}else if(mode==='inventory'){
 const files=[];for(const path of await walk(root)){
  const name=relative(root,path).replaceAll('\\','/');if(name==='SHA256SUMS'||name==='artifact-manifest.json')continue;
  assert(!/(^|\/)(?:demo|mock|mocks|tests?|fixtures?|node_modules|\.git)(\/|$)|\.(?:pfx|pem|key|env)$|p06[_-]server|certificate\.ps1/i.test(name),`forbidden release file: ${name}`);
  const bytes=await readFile(path);files.push({path:name,bytes:bytes.length,sha256:hash(bytes)});
 }
 assert(files.some(f=>f.path==='bin/campus-api')&&files.some(f=>f.path==='sbom.spdx.json'),'release binary or SBOM absent');
 for(const role of ['user','rider','admin'])assert(files.some(f=>f.path===`static/${role}/${role}.html`),'missing separated entry '+role);
 await writeFile(resolve(root,'artifact-manifest.json'),canonical(files));
 const manifest=await readFile(resolve(root,'artifact-manifest.json'));files.push({path:'artifact-manifest.json',sha256:hash(manifest)});
 await writeFile(resolve(root,'SHA256SUMS'),files.map(f=>`${f.sha256}  ${f.path}`).sort().join('\n')+'\n');
}else throw Error('metadata or inventory required');
