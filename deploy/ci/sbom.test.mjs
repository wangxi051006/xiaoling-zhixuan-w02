import test from 'node:test';
import assert from 'node:assert/strict';
import {createSBOM} from './sbom.mjs';
test('release SPDX is reproducible, includes linked Go/production npm, excludes dev-only packages',()=>{
 const input={candidate:'a'.repeat(40),epoch:1795000000,build:{GoVersion:'go1.27.4',Deps:[{Path:'example.org/runtime',Version:'v1.0.0'}]},lock:{packages:{'':{},'node_modules/vue':{version:'3.5.45',integrity:'sha512-locked'},'node_modules/vite':{version:'7.3.8',dev:true}}},binaryHash:'b'.repeat(64)};
 const a=createSBOM(input),b=createSBOM(input);assert.deepEqual(a,b);assert.equal(a.spdxVersion,'SPDX-2.3');assert(a.packages.some(p=>p.name==='vue'));assert(a.packages.some(p=>p.name==='example.org/runtime'));assert(!a.packages.some(p=>p.name==='vite'));assert(a.relationships.every(r=>r.relatedSpdxElement==='SPDXRef-Binary'||a.packages.some(p=>p.SPDXID===r.relatedSpdxElement)));
 const stdlib=a.packages.find(p=>p.name==='stdlib');assert.equal(stdlib.versionInfo,'1.27.4');assert.equal(stdlib.externalRefs[0].referenceLocator,'pkg:golang/stdlib@1.27.4');
 assert.throws(()=>createSBOM({...input,build:{...input.build,Deps:[{Path:'p',Version:'v1',Replace:{}}]}}));
});
