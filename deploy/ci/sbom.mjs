import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {pathToFileURL} from 'node:url';
// Deterministic SPDX for linked Go modules + locked production npm dependency graph.
// Source/dev dependency SBOM is independently produced by Syft in SEC-04.
export function createSBOM({candidate,epoch,build,lock,binaryHash}){
 assert(candidate&&/^\d+$/.test(String(epoch))&&build.GoVersion&&Array.isArray(build.Deps),'complete build provenance required');
 const id=text=>'SPDXRef-'+createHash('sha256').update(text).digest('hex');
 const packages=[{SPDXID:'SPDXRef-Application',name:'xiaoling',versionInfo:candidate,downloadLocation:'NOASSERTION',filesAnalyzed:false,licenseConcluded:'NOASSERTION',licenseDeclared:'NOASSERTION',copyrightText:'NOASSERTION'}];
 const relationships=[{spdxElementId:'SPDXRef-DOCUMENT',relationshipType:'DESCRIBES',relatedSpdxElement:'SPDXRef-Application'}];
 function add(type,name,version){const purl=`pkg:${type}/${name}@${version}`,SPDXID=id(purl);packages.push({SPDXID,name,versionInfo:version,downloadLocation:'NOASSERTION',filesAnalyzed:false,licenseConcluded:'NOASSERTION',licenseDeclared:'NOASSERTION',copyrightText:'NOASSERTION',externalRefs:[{referenceCategory:'PACKAGE-MANAGER',referenceType:'purl',referenceLocator:purl}]});relationships.push({spdxElementId:'SPDXRef-Application',relationshipType:'DEPENDS_ON',relatedSpdxElement:SPDXID});}
 const stdlibVersion=build.GoVersion.replace(/^go/,'');assert(/^\d+\.\d+\.\d+$/.test(stdlibVersion),'release Go version must be a stable semantic version');
 add('golang','stdlib',stdlibVersion);
 for(const dep of build.Deps){assert(!dep.Replace,'unreviewed Go replace in release');add('golang',dep.Path,dep.Version);}
 for(const [path,pkg] of Object.entries(lock.packages??{}).sort(([a],[b])=>a.localeCompare(b,'en'))){if(!path||pkg.dev||pkg.devOptional)continue;assert(pkg.version&&pkg.integrity,'unlocked npm production package');const name=pkg.name??path.slice(path.lastIndexOf('node_modules/')+'node_modules/'.length);add('npm',name.startsWith('@')?'%40'+name.slice(1):name,pkg.version);}
 return {spdxVersion:'SPDX-2.3',dataLicense:'CC0-1.0',SPDXID:'SPDXRef-DOCUMENT',name:'xiaoling-candidate',documentNamespace:`https://xiaoling.invalid/spdx/${candidate}`,creationInfo:{created:new Date(Number(epoch)*1000).toISOString().replace('.000Z','Z'),creators:['Tool: xiaoling-p07-sbom-1']},packages:packages.sort((a,b)=>a.SPDXID.localeCompare(b.SPDXID,'en')),files:[{SPDXID:'SPDXRef-Binary',fileName:'./bin/campus-api',checksums:[{algorithm:'SHA256',checksumValue:binaryHash}],licenseConcluded:'NOASSERTION',copyrightText:'NOASSERTION'}],relationships:[...relationships,{spdxElementId:'SPDXRef-Application',relationshipType:'CONTAINS',relatedSpdxElement:'SPDXRef-Binary'}].sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b),'en'))};
}
if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href){
 const folder=resolve(process.argv[2]);const binary=resolve(folder,'bin/campus-api');const build=JSON.parse(execFileSync('go',['version','-m','-json',binary],{encoding:'utf8'}));
 const doc=createSBOM({candidate:process.env.CANDIDATE_ID,epoch:process.env.SOURCE_DATE_EPOCH,build,lock:JSON.parse(await readFile('web/package-lock.json','utf8')),binaryHash:createHash('sha256').update(await readFile(binary)).digest('hex')});
 await writeFile(resolve(folder,'sbom.spdx.json'),JSON.stringify(doc,null,2)+'\n');
}
