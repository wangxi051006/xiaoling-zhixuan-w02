import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,copyFileSync,readFileSync,writeFileSync,appendFileSync,existsSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {test} from 'node:test';

const repoRoot=resolve(import.meta.dirname,'..','..');
const cli=join(repoRoot,'deploy/ci/release-manifest.mjs');
const lockPath=join(repoRoot,'deploy/ci/migration-lock.json');
const lock=JSON.parse(readFileSync(lockPath,'utf8'));
const sourceRoot=join(repoRoot,'backend/migrations');
const sha256=path=>createHash('sha256').update(readFileSync(path)).digest('hex');

assert.ok(Array.isArray(lock)&&lock.length>0,'migration lock must be a non-empty array');
for(const item of lock)assert.deepEqual(Object.keys(item).sort(),['name','sha256'],'lock entries carry only name and sha256');

function fixture(){
  const base=mkdtempSync(join(tmpdir(),'release-manifest-'));
  const repo=join(base,'repo');
  const out=join(base,'out');
  mkdirSync(join(repo,'deploy','ci'),{recursive:true});
  mkdirSync(join(repo,'backend','migrations'),{recursive:true});
  mkdirSync(join(out,'migrations'),{recursive:true});
  for(const name of ['release-manifest.mjs','migration-lock.json','toolchain-lock.json']){
    copyFileSync(join(repoRoot,'deploy','ci',name),join(repo,'deploy','ci',name));
  }
  for(const {name} of lock){
    copyFileSync(join(sourceRoot,name),join(repo,'backend','migrations',name));
    copyFileSync(join(sourceRoot,name),join(out,'migrations',name));
  }
  return {base,repo,out};
}

function run({repo,out}){
  const result=spawnSync(process.execPath,['deploy/ci/release-manifest.mjs','metadata',out],{
    cwd:repo,encoding:'utf8',
    env:{...process.env,CANDIDATE_ID:'LOCAL-REHEARSAL-MIGRATION-TEST',SOURCE_DATE_EPOCH:'0',PREPARATION_ENVIRONMENT:'staging-preparation'},
  });
  return {status:result.status,stdout:result.stdout??'',stderr:result.stderr??''};
}

const expected=lock.map(({name,sha256:digest})=>{const bytes=readFileSync(join(sourceRoot,name));return {name,bytes:bytes.length,sha256:digest};});

function assertNoMetadata(out){
  assert.equal(existsSync(join(out,'VERSION.json')),false,'VERSION.json must not be written');
  assert.equal(existsSync(join(out,'migrations.json')),false,'migrations.json must not be written');
}

test('approved eight-migration set produces metadata',()=>{
  const fx=fixture();
  try{
    const result=run(fx);
    assert.equal(result.status,0,`expected exit 0, got ${result.status}: ${result.stderr}`);
    assert.deepEqual(JSON.parse(readFileSync(join(fx.out,'migrations.json'),'utf8')),expected);
    const version=JSON.parse(readFileSync(join(fx.out,'VERSION.json'),'utf8'));
    assert.equal(version.candidate,'LOCAL-REHEARSAL-MIGRATION-TEST');
    assert.equal(version.level,'LOCAL-REHEARSAL');
    assert.equal(version.preparationEnvironment,'staging-preparation');
    assert.equal(version.sourceDateEpoch,0);
    assert.equal(version.productionRelease,'NO-GO');
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects missing 060 migration',()=>{
  const fx=fixture();
  try{
    rmSync(join(fx.repo,'backend','migrations','060_001_account_identity_email.sql'));
    rmSync(join(fx.out,'migrations','060_001_account_identity_email.sql'));
    const result=run(fx);
    assert.notEqual(result.status,0,'missing 060 must fail');
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects unapproved extra migration',()=>{
  const fx=fixture();
  try{
    writeFileSync(join(fx.repo,'backend','migrations','070_001_unapproved.sql'),'-- unapproved\n');
    writeFileSync(join(fx.out,'migrations','070_001_unapproved.sql'),'-- unapproved\n');
    const result=run(fx);
    assert.notEqual(result.status,0,'unapproved 070 must fail');
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects unnumbered SQL present only in source migrations',()=>{
  const fx=fixture();
  try{
    writeFileSync(join(fx.repo,'backend','migrations','notes.sql'),'-- unapproved\n');
    const result=run(fx);
    assert.equal(result.status,1,'source-only notes.sql must be rejected');
    assert.match(result.stderr,/invalid migration filename: notes\.sql/);
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects extra non-SQL entries in the packaged migration directory',()=>{
  const fx=fixture();
  try{
    writeFileSync(join(fx.out,'migrations','notes.txt'),'not a migration\n');
    const result=run(fx);
    assert.equal(result.status,1);
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects a directory in place of a packaged migration file',()=>{
  const fx=fixture();
  try{
    const file=join(fx.out,'migrations','060_001_account_identity_email.sql');
    rmSync(file);
    mkdirSync(file);
    const result=run(fx);
    assert.equal(result.status,1);
    assert.match(result.stderr,/migration must be a regular file/);
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects changed historical migration bytes',()=>{
  const fx=fixture();
  try{
    appendFileSync(join(fx.repo,'backend','migrations','040_001_settlement.sql'),'\n-- tampered\n');
    appendFileSync(join(fx.out,'migrations','040_001_settlement.sql'),'\n-- tampered\n');
    const result=run(fx);
    assert.notEqual(result.status,0,'changed 040 bytes must fail');
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});

test('rejects packaged copy that differs from approved bytes',()=>{
  const fx=fixture();
  try{
    appendFileSync(join(fx.out,'migrations','050_001_fulfillment.sql'),'\n-- package only\n');
    const result=run(fx);
    assert.notEqual(result.status,0,'packaged copy drift must fail');
    assertNoMetadata(fx.out);
  } finally { rmSync(fx.base,{recursive:true,force:true}); }
});
