import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {stripVTControlCharacters} from 'node:util';
export async function checkWiring(root='.') {
 for(const role of ['user','rider','admin']){
  const html=await readFile(resolve(root,'web',role+'.html'),'utf8');
  const sources=[...html.matchAll(/<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>/gi)].map(m=>m[1]);
  assert.deepEqual(sources,[`/src/${role}/entry.ts`],`BLOCKED: controller must integrate ${role}.html with its owned entry before release; P07 never rewrites HTML`);
 }
 return {status:'PASS',entries:['user','rider','admin']};
}
export function checkTap(text,min=1){
 text=stripVTControlCharacters(text);
 // npm's exact frozen scripts can select Node 26's spec reporter even in pipes.
 // Require the same complete summary for TAP or spec; never infer success from check marks.
 const n=key=>{const m=[...text.matchAll(new RegExp(`^(?:# |ℹ )${key} (\\d+)\\r?$`,'gm'))].at(-1);assert(m,`missing test summary ${key}`);return Number(m[1]);};
 const result=Object.fromEntries(['tests','pass','fail','cancelled','skipped','todo'].map(k=>[k,n(k)]));
 assert(result.tests>=min&&result.pass>=min,'zero or too few matched tests');assert.equal(result.tests,result.pass,'not every test passed');
 for(const k of ['fail','cancelled','skipped','todo'])assert.equal(result[k],0,`required tests ${k}`);
 return result;
}
export function checkGo(text,min=1,required=[]){
 const events=text.split(/\r?\n/).filter(Boolean).map(line=>JSON.parse(line));
 assert(!events.some(e=>e.Action==='fail'||(e.Action==='skip'&&e.Test)), 'required Go test failed or skipped');
 const passed=events.filter(e=>e.Action==='pass'&&e.Test);assert(passed.length>=min,'zero or too few matched Go tests');
 for(const name of required)assert(passed.some(e=>e.Test===name),`required test did not pass: ${name}`);
 return {pass:passed.length,fail:0,skip:0};
}
export function checkAudit(doc){
 assert(!doc.error,'npm audit service error');const v=doc.metadata?.vulnerabilities;assert(v&&Number.isInteger(v.high)&&Number.isInteger(v.critical),'incomplete dependency audit');
 assert.equal(v.high+v.critical,0,'unclosed HIGH/CRITICAL npm dependency vulnerabilities');
 return {status:'PASS',vulnerabilities:v,picomatch:doc.vulnerabilities?.picomatch??{status:'not reported by this audit'}};
}
if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href){
 const [mode,file,...args]=process.argv.slice(2);let result;
 if(mode==='wiring')result=await checkWiring(file??'.');
 else if(mode==='tap')result=checkTap(await readFile(file,'utf8'),Number(args[0]??1));
 else if(mode==='go')result=checkGo(await readFile(file,'utf8'),Number(args[0]??1),args.slice(1));
 else if(mode==='audit')result=checkAudit(JSON.parse(await readFile(file,'utf8')));
 else throw Error('usage: gates.mjs wiring [root] | tap report min | go report min [required tests] | audit report');
 console.log(JSON.stringify(result));
}
