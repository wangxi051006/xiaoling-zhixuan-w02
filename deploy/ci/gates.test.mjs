import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {checkWiring,checkTap,checkGo,checkAudit} from './gates.mjs';
test('release gate rejects placeholder and crossed entry, accepts each owned entry',async()=>{
 const root=await mkdtemp(join(tmpdir(),'p07-gate-')); await mkdir(join(root,'web'));
 try {for(const role of ['user','rider','admin'])await writeFile(join(root,'web',role+'.html'),`<script type="module" src="/src/shared/entry.ts"></script>`);
 await assert.rejects(checkWiring(root),/BLOCKED/);
 for(const role of ['user','rider','admin'])await writeFile(join(root,'web',role+'.html'),`<script type="module" src="/src/${role}/entry.ts"></script>`);
 await checkWiring(root);await writeFile(join(root,'web','admin.html'),'<script type="module" src="/src/user/entry.ts"></script>');await assert.rejects(checkWiring(root),/BLOCKED/);
 } finally {await rm(root,{recursive:true,force:true});}
});
test('required TAP fails zero, skip, todo, malformed counts and failures',()=>{
 const good='TAP version 13\n# tests 3\n# pass 3\n# fail 0\n# cancelled 0\n# skipped 0\n# todo 0\n'; assert.equal(checkTap(good,3).pass,3);
 assert.equal(checkTap(good.replaceAll('# ','ℹ '),3).pass,3);
 for(const bad of [good.replace('# tests 3','# tests 0'),good.replace('# skipped 0','# skipped 1'),good.replace('# todo 0','# todo 1'),good.replace('# fail 0','# fail 1'),''])assert.throws(()=>checkTap(bad,3));
});
test('Go JSON requires executed tests and rejects skip/fail',()=>{
 const good=JSON.stringify({Action:'pass',Test:'TestMigration',Package:'p'})+'\n';assert.equal(checkGo(good,1,['TestMigration']).pass,1);
 assert.equal(checkGo(good+JSON.stringify({Action:'skip',Package:'migrations'}),1).pass,1);
 assert.throws(()=>checkGo(good,1,['Missing']));assert.throws(()=>checkGo(''));assert.throws(()=>checkGo(good+JSON.stringify({Action:'skip',Test:'Skipped'})));
});
test('all-dependency npm audit rejects high, critical, error and missing metadata',()=>{
 assert.equal(checkAudit({metadata:{vulnerabilities:{low:0,moderate:1,high:0,critical:0}},vulnerabilities:{picomatch:{severity:'moderate'}}}).picomatch.severity,'moderate');
 for(const input of [{},{error:{code:'EFAIL'}},{metadata:{vulnerabilities:{high:1,critical:0}}},{metadata:{vulnerabilities:{high:0,critical:1}}}])assert.throws(()=>checkAudit(input));
});
