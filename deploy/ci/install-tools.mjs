import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile,appendFile,chmod} from 'node:fs/promises';
import {resolve} from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
const lock=JSON.parse(await readFile(new URL('./toolchain-lock.json',import.meta.url),'utf8'));
const destination=resolve(process.env.RUNNER_TEMP??process.env.TEMP??'/tmp','xiaoling-p07-tools');await mkdir(destination,{recursive:true});
for(const name of process.argv.slice(2)){
 const item=lock.downloads[name];assert(item,'unknown locked tool '+name);
 const folder=resolve(destination,name);await mkdir(folder,{recursive:true});
 const archive=resolve(folder,item.url.endsWith('.zip')?'download.zip':'download.tar.gz');
 const response=await fetch(item.url);assert(response.ok,`${name} download HTTP ${response.status}`);const bytes=Buffer.from(await response.arrayBuffer());
 assert.equal(createHash('sha256').update(bytes).digest('hex'),item.sha256,`${name} checksum mismatch`);await writeFile(archive,bytes);
 if(item.url.endsWith('.zip')){
  if(process.platform==='win32')execFileSync('tar',['-xf',archive,'-C',folder],{stdio:'inherit',windowsHide:true});
  else execFileSync('unzip',['-q','-o',archive,'-d',folder],{stdio:'inherit'});
 }else execFileSync('tar',['-xzf',archive,'-C',folder],{stdio:'inherit'});
 const executable=resolve(folder,item.executable);if(process.platform!=='win32')await chmod(executable,0o755);
 if(name.startsWith('browser')){if(process.env.GITHUB_ENV)await appendFile(process.env.GITHUB_ENV,`BROWSER_EXECUTABLE=${executable}\n`);}
 else if(process.env.GITHUB_PATH)await appendFile(process.env.GITHUB_PATH,folder+'\n');
 console.log(JSON.stringify({name,sha256:item.sha256,executable}));
}
