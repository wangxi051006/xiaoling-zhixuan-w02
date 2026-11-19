import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {createServer as httpsServer} from 'node:https';
import {request as httpRequest} from 'node:http';
import {readFile,mkdtemp,rm,mkdir,writeFile} from 'node:fs/promises';
import {resolve,sep} from 'node:path';
import {tmpdir} from 'node:os';
import {randomBytes} from 'node:crypto';
import {chromium} from 'playwright-core';

// This harness never mocks business responses. All /api/v1 calls proxy to Go.
// Browser/session setup is test-only and lives outside the production graph.
export async function startHarness(){
 assert.ok(process.env.P06_FIXTURE_EXECUTABLE,'P06_FIXTURE_EXECUTABLE is required');
 assert.ok(process.env.TEST_DATABASE_URL,'TEST_DATABASE_URL is required');
 assert.ok(process.env.BROWSER_EXECUTABLE,'BROWSER_EXECUTABLE is required');
 const evidence=resolve(process.env.P06_EVIDENCE_DIR??'p06-evidence');await mkdir(evidence,{recursive:true});
 const temp=await mkdtemp(resolve(tmpdir(),'xiaoling-p06-'));const cert=resolve(temp,'fixture.pfx');
 const ps=spawnSync('pwsh',['-NoProfile','-File',resolve('tests/fixtures/certificate.ps1'),'-OutputPath',cert],{encoding:'utf8',windowsHide:true});assert.equal(ps.status,0,'test TLS certificate creation failed');
 const pfx=await readFile(cert);await rm(cert);const servers=[],origins={},httpLog=[];let api;let dropOrderResponse=false;
 const types={html:'text/html; charset=utf-8',js:'text/javascript',css:'text/css',svg:'image/svg+xml'};
 for(const role of ['user','rider','admin']){
  const host=role+'.p06.test';
  const server=httpsServer({pfx,passphrase:''},async(req,res)=>{
   if(req.headers.host!==new URL(origins[role]).host){res.writeHead(421).end();return;}
   if(req.url.startsWith('/api/v1/')){
    if(!api){res.writeHead(503).end();return;}
    const target=new URL(api);const start=Date.now();
    const proxied=httpRequest({hostname:target.hostname,port:target.port,method:req.method,path:req.url,headers:req.headers},up=>{
     httpLog.push({role,method:req.method,path:req.url.split('?')[0],status:up.statusCode,durationMs:Date.now()-start});
     if(dropOrderResponse&&role==='user'&&req.method==='POST'&&req.url==='/api/v1/orders'){dropOrderResponse=false;res.writeHead(up.statusCode,up.headers);res.flushHeaders();up.resume();up.on('end',()=>res.destroy());return;}
     res.writeHead(up.statusCode,up.headers);up.pipe(res);
    });proxied.on('error',()=>{res.writeHead(502).end()});req.pipe(proxied);return;
   }
   const raw=req.url.split('?')[0],file=raw==='/'?role+'.html':raw.startsWith('/assets/')?decodeURIComponent(raw.slice(1)):null;
   try{const dist=resolve('dist'),path=file&&resolve(dist,file);if(!path?.startsWith(dist+sep))throw Error('not found');const body=await readFile(path);res.writeHead(200,{'Content-Type':types[file.split('.').at(-1)]??'application/octet-stream','Cache-Control':'no-store'}).end(body);}catch{res.writeHead(404).end();}
  });await new Promise((r,j)=>server.once('error',j).listen(0,'127.0.0.1',r));servers.push(server);origins[role]=`https://${host}:${server.address().port}`;
 }
 const secret=randomBytes(32).toString('hex');const child=spawn(process.env.P06_FIXTURE_EXECUTABLE,[],{env:{...process.env,P06_USER_ORIGIN:origins.user,P06_RIDER_ORIGIN:origins.rider,P06_ADMIN_ORIGIN:origins.admin,P06_CONTROL_SECRET:secret},windowsHide:true,stdio:['pipe','pipe','pipe']});
 let diagnostics='';child.stderr.on('data',b=>diagnostics+=b);
 let ready;
 try {ready=await new Promise((r,j)=>{let text='';const timeout=setTimeout(()=>j(Error('fixture startup timeout')),30000);child.stdout.on('data',b=>{text+=b;if(text.includes('\n')){clearTimeout(timeout);try{r(JSON.parse(text.split('\n')[0]))}catch(e){j(e)}}});child.on('exit',code=>{clearTimeout(timeout);j(Error(`fixture exited ${code}: ${diagnostics}`))});child.on('error',j)});}catch(e){child.kill();for(const s of servers)s.close();await rm(temp,{recursive:true,force:true});throw e}
 api=ready.api;
 const control=async(command,params={})=>{const r=await fetch(ready.control,{method:'POST',headers:{Authorization:'Bearer '+secret,'Content-Type':'application/json'},body:JSON.stringify({Command:command,...params})});assert.equal(r.status,200,'fixture control '+command);const result=await r.json();if(command==='advance')for(const p of pages)await p.clock.setFixedTime(new Date(result.now));return result};
 const browser=await chromium.launch({executablePath:process.env.BROWSER_EXECUTABLE,headless:true,args:['--no-proxy-server','--host-resolver-rules=MAP *.p06.test 127.0.0.1']});
 const pages=[],contexts=[];const failures=[];
 async function actor(id,role){const credential=await control('session',{Actor:id});const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:1000},timezoneId:'Asia/Shanghai'});contexts.push(context);await context.addCookies([{name:'__Host-xiaoling-session',value:credential.token,url:origins[role],secure:true,httpOnly:true,sameSite:'Lax'}]);const page=await context.newPage();await page.clock.install({time:new Date((await control('info')).now)});page.on('pageerror',e=>failures.push(e.message));pages.push(page);await page.goto(origins[role]);await page.locator(`main[data-entry="${role}"]`).waitFor();return{page,context,id,role,async me(){return request(this,'GET','/me')},async refreshSession(){const s=await control('session',{Actor:id});await context.addCookies([{name:'__Host-xiaoling-session',value:s.token,url:origins[role],secure:true,httpOnly:true,sameSite:'Lax'}]);await page.reload()}};}
 async function request(actor,method,path,body,key=randomBytes(12).toString('hex')){
  return actor.page.evaluate(async({method,path,body,key})=>{const me=await (await fetch('/api/v1/me',{cache:'no-store'})).json();const headers={'Accept':'application/json','Origin':location.origin};if(method!=='GET'){headers['X-CSRF-Token']=me.csrf_token;headers['Idempotency-Key']=key;}if(body!==undefined)headers['Content-Type']='application/json';const r=await fetch('/api/v1'+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),cache:'no-store'});return{status:r.status,body:await r.json().catch(()=>null)}},{method,path,body,key});
 }
 async function shot(actor,name,width=1440){await actor.page.setViewportSize({width,height:1000});const mask=await actor.page.addStyleTag({content:'[data-sensitive]{visibility:hidden!important}input[type="tel"],input[autocomplete="tel"]{color:transparent!important;text-shadow:none!important}'});try{assert.equal(await actor.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'no horizontal overflow '+name);await actor.page.screenshot({path:resolve(evidence,name+'-'+width+'.png'),fullPage:true});}finally{await mask.evaluate(el=>el.remove());}}
 async function close(){await writeFile(resolve(evidence,'http.json'),JSON.stringify(httpLog,null,2));await writeFile(resolve(evidence,'browser-errors.json'),JSON.stringify(failures));for(const c of contexts)await c.close();await browser.close();await control('shutdown');await new Promise(r=>child.exitCode!==null?r():child.once('exit',r));for(const s of servers)await new Promise(r=>s.close(r));await rm(temp,{recursive:true,force:true});if(diagnostics)await writeFile(resolve(evidence,'fixture-diagnostics.log'),diagnostics);assert.deepEqual(failures,[],'unhandled page errors');}
 return{origins,control,actor,request,shot,close,evidence,info:await control('info'),failures,dropNextOrderResponse(){dropOrderResponse=true;}};
}
