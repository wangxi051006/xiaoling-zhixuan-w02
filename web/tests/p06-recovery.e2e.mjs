import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createServer as createVite } from 'vite';
import { chromium } from 'playwright-core';

const slot={id:'slot-1',starts_at:'2026-11-19T12:00:00+08:00',ends_at:'2026-11-19T12:30:00+08:00',dispatch_at:'2026-11-19T11:50:00+08:00',accepting:true};
const product={id:'p1',name:'恢复测试商品',description:'用于故障分支',skus:[{id:'s1',label:'单件',units_per_pack:1,unit_price_fen:500}]};
const quote={quote_id:'q1',valid_until:'2099-01-01T00:00:00Z',slot_id:slot.id,lines:[{sku_id:'s1',qty:1,name:product.name,spec:'单件',unit_price_fen:500,line_total_fen:500}],total_fen:500,payment_method:'BALANCE'};
const order={id:'o1',delivery_mode:'SCHEDULED',slot,status:'SCHEDULED',stock_status:'UNSORTED',settlement_status:'BALANCE_PAID',payment_method:'BALANCE',total_fen:500,lines:quote.lines,created_at:'2026-11-19T03:00:00Z'};
const address={id:'a1',building_id:'b1',building_name:'合成楼栋',room:'101',contact_name:'合成用户',contact_phone:'+15550000001',version:1};
const task={id:'t1',building_name:'合成楼栋',item_count:1,fee_fen:100,slot,order_status:'READY_FOR_CLAIM'};
const pageOf=items=>({items,next_cursor:null});
async function withRole(role,fn,roles){
 const vite=await createVite({configFile:false,server:{middlewareMode:true,hmr:false,ws:false},appType:'custom'});
 const server=createServer((req,res)=>req.url==='/'?res.setHeader('Content-Type','text/html').end(`<html lang="zh-CN"><div id="app" data-entry="${role}"></div><script type="module" src="/src/${role}/entry.ts"></script></html>`):vite.middlewares(req,res));
 await new Promise(r=>server.listen(0,'127.0.0.1',r));let browser;
 try{
  browser=await chromium.launch({executablePath:process.env.BROWSER_EXECUTABLE||'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true});
  const page=await browser.newPage({viewport:{width:390,height:900}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/@vite/client',route=>route.fulfill({contentType:'text/javascript',body:'export function updateStyle(id,css){let el=document.getElementById(id);if(!el){el=document.createElement("style");el.id=id;document.head.append(el);}el.textContent=css;}export function removeStyle(id){document.getElementById(id)?.remove();}export function createHotContext(){return {accept(){},prune(){}};}export function injectQuery(url){return url;}'}));
  await page.clock.install({time:new Date('2026-11-19T03:00:00Z')});
  const write=[];let handler;
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),path=new URL(req.url()).pathname.replace('/api/v1','');
   if(req.method()!=='GET')write.push({path,method:req.method(),body:req.postData(),key:req.headers()['idempotency-key'],csrf:req.headers()['x-csrf-token']});
   const respond=(body,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
   if(handler&&await handler({route,req,path,respond}))return;
   const defaults={
    '/me':{id:'actor',station_id:'station',roles:roles||[{user:'CUSTOMER',rider:'RIDER',admin:'FINANCE'}[role]],csrf_token:'csrf-test'},
    '/catalog/products':pageOf([product]),'/catalog/products/p1':product,'/me/addresses':pageOf([address]),'/delivery/slots':pageOf([slot]),'/me/wallet':{balance_fen:10000,currency:'CNY'},
    '/payment-methods':{methods:[{code:'COD',enabled:true,label:'货到付款'},{code:'BALANCE',enabled:true,label:'余额'},{code:'WECHAT',enabled:false,label:'微信待接入'}]},
    '/checkout/quote':quote,'/orders':pageOf([]),'/orders/o1':order,'/rider/tasks':pageOf([task]),'/rider/history':pageOf([]),'/rider/earnings':pageOf([]),'/rider/messages':pageOf([]),
    '/admin/dashboard':{local_date:'2026-11-19',order_count:1,delivered_count:1,net_collected_fen:500,overdue_count:0},'/admin/refunds':pageOf([]),'/admin/cod/receipts':pageOf([])
   };
   await respond(defaults[path]||pageOf([]));
  });
  const start=async()=>{await page.goto(`http://127.0.0.1:${server.address().port}`);await page.getByTestId('session-ready').waitFor();};
  await fn({page,start,write,handle:fn=>handler=fn,errors});assert.deepEqual(errors,[]);
 }finally{await browser?.close();await vite.close();await new Promise(r=>server.close(r));}
}

test('MOCK user conflict keeps selected payment/window and requires fresh quotation',async()=>withRole('user',async({page,start,handle})=>{
 let code='QUOTE_CHANGED';handle(async({path,req,respond})=>{if(path==='/orders'&&req.method()==='POST'){await respond({code,message:'服务器拒绝本次交易',request_id:'synthetic'},409);return true;}});
 await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByLabel('配送方式',{exact:true}).selectOption('SCHEDULED');await page.getByLabel('服务窗口',{exact:true}).selectOption(slot.id);await page.getByRole('radio',{name:'余额',exact:true}).check();
 for(const value of ['QUOTE_CHANGED','INSUFFICIENT_BALANCE','SLOT_FULL','OUT_OF_STOCK','SLOT_EXPIRED']){
  code=value;await page.getByTestId('quote').click();await page.getByTestId('confirm-order').click();await page.getByRole('alert').filter({hasText:value}).waitFor();
  assert.equal(await page.getByRole('radio',{name:'余额',exact:true}).isChecked(),true);assert.equal(await page.getByLabel('服务窗口',{exact:true}).inputValue(),slot.id);assert.equal(await page.getByTestId('confirm-order').count(),0);assert.equal(await page.getByTestId('order-detail').count(),0);
 }
}));

test('MOCK user uncertain committed order requires check then same operation body and key retry',async()=>withRole('user',async({page,start,handle,write})=>{
 let committed=false,first=true;
 handle(async({path,req,respond,route})=>{
  if(path==='/orders'&&req.method()==='POST'){committed=true;if(first){first=false;await route.abort('failed');}else await respond(order,201);return true;}
  if(path==='/orders'){await respond(pageOf(committed?[order]:[]));return true;}
 });
 await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByRole('radio',{name:'余额',exact:true}).check();await page.getByTestId('quote').click();await page.getByTestId('confirm-order').click();await page.getByTestId('check-uncertain').waitFor();assert.equal(await page.getByTestId('retry-uncertain').isDisabled(),true);assert.equal(await page.getByTestId('order-detail').count(),0);
 await page.getByTestId('check-uncertain').click();await page.getByTestId('retry-uncertain').click();await page.getByTestId('order-detail').waitFor();assert.ok(await page.getByText('余额已扣',{exact:true}).count());const attempts=write.filter(w=>w.path==='/orders');assert.equal(attempts.length,2);assert.deepEqual(attempts[0],attempts[1]);
}));

test('MOCK hanging order write expires into uncertain state and requires server check',async()=>withRole('user',async({page,start,handle})=>{
 let release;handle(async({path,req,route})=>{if(path==='/orders'&&req.method()==='POST'){await new Promise(r=>release=r);await route.abort('failed').catch(()=>{});return true;}});
 try{await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByTestId('quote').click();const sent=page.waitForRequest(r=>r.url().endsWith('/orders')&&r.method()==='POST');await page.getByTestId('confirm-order').click();await sent;await page.clock.runFor(16000);assert.equal(await page.getByTestId('check-uncertain').count(),1,'a hanging write must become reviewable after its timeout');assert.equal(await page.getByTestId('retry-uncertain').isDisabled(),true);}finally{release?.();}
}));

for(const status of [201,500])test(`MOCK ambiguous HTTP ${status} preserves original order operation for checked retry`,async()=>withRole('user',async({page,start,handle,write})=>{
 let first=true;handle(async({path,req,route,respond})=>{if(path==='/orders'&&req.method()==='POST'){if(first){first=false;await route.fulfill({status,contentType:'application/json',body:status===201?'{"id":':JSON.stringify({code:'INTERNAL_ERROR',message:'结果无法确认',request_id:'synthetic'})});}else await respond(order,201);return true;}});
 await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByTestId('quote').click();await page.getByTestId('confirm-order').click();await page.getByRole('alert').waitFor();assert.equal(await page.getByTestId('check-uncertain').count(),1,'ambiguous success/server error must retain the operation');await page.getByTestId('check-uncertain').click();await page.getByTestId('retry-uncertain').click();await page.getByTestId('order-detail').waitFor();const attempts=write.filter(w=>w.path==='/orders');assert.equal(attempts.length,2);assert.deepEqual(attempts[0],attempts[1]);
}));

test('MOCK paginated catalog includes subsequent server pages',async()=>withRole('user',async({page,start,handle})=>{
 handle(async({path,req,respond})=>{if(path==='/catalog/products'){const cursor=new URL(req.url()).searchParams.get('cursor');await respond(cursor?pageOf([{...product,id:'p2',name:'第二页商品',skus:[{...product.skus[0],id:'s2'}]}]):{items:[product],next_cursor:'p1'});return true;}});
 await start();await page.getByTestId('add-s1').waitFor();assert.equal(await page.getByTestId('add-s2').count(),1,'catalog must consume the next_cursor instead of silently omitting products');
}));

test('MOCK changing checkout selection invalidates a quotation already in flight',async()=>withRole('user',async({page,start,handle})=>{
 let release;handle(async({path,req,respond})=>{
  if(path==='/me/addresses'){await respond(pageOf([address,{...address,id:'a2',room:'202'}]));return true;}
  if(path==='/checkout/quote'){const input=req.postDataJSON();await new Promise(r=>release=r);await respond({...quote,payment_method:input.payment_method},201);return true;}
 });
 try{await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();const sent=page.waitForRequest(r=>r.url().endsWith('/checkout/quote'));await page.getByTestId('quote').click();await sent;await page.getByLabel('配送地址',{exact:true}).selectOption('a2');await page.getByRole('radio',{name:'余额',exact:true}).check();await page.getByLabel('配送方式',{exact:true}).selectOption('SCHEDULED');await page.getByLabel('服务窗口',{exact:true}).selectOption(slot.id);release();await page.waitForFunction(()=>document.querySelector('[data-testid="quote"]')?.disabled===false);assert.equal(await page.getByTestId('confirm-order').count(),0,'late quotation must not restore confirmation for old delivery/payment inputs');}finally{release?.();}
}));

test('MOCK admin logout and changed account clear private order detail including late reads',async()=>withRole('admin',async({page,start,handle})=>{
 let actor='actor',delay=false,release;const adminOrder={...order,address,overdue:false};
 const adminProduct={...product,on_sale:true,version:1,inventory:{id:'i1',on_hand:100,reserved:1,reorder_point:5,version:1},promotion:null};
 handle(async({path,respond})=>{
  if(path==='/me'){await respond({id:actor,station_id:actor==='actor'?'station':'new-station',roles:['OPERATOR'],csrf_token:'csrf-test'});return true;}
  if(path==='/admin/orders'){await respond(pageOf(actor==='actor'?[adminOrder]:[]));return true;}
  if(path==='/admin/products'){await respond(pageOf([adminProduct]));return true;}
  if(path==='/admin/orders/o1'){if(delay){delay=false;await new Promise(r=>release=r);await respond(adminOrder);}else await respond(actor==='actor'?adminOrder:{code:'NOT_FOUND',message:'无权读取旧订单',request_id:'synthetic'},actor==='actor'?200:404);return true;}
 });
 try{
  await start();await page.getByRole('button',{name:'订单履约',exact:true}).click();await page.getByTestId('order-o1').click();await page.getByTestId('admin-order-detail').waitFor();
  delay=true;const held=page.waitForRequest(r=>r.url().endsWith('/admin/orders/o1'));await page.getByTestId('recover-session').click();await held;
  await page.getByRole('button',{name:'退出登录',exact:true}).click();await page.getByTestId('session-ready').waitFor({state:'hidden'});
  actor='actor2';const stale=page.waitForResponse(r=>r.url().endsWith('/admin/orders/o1'));release();await(await stale).finished();
  await page.getByTestId('recover-session').click();await page.getByTestId('session-ready').getByText(/new-station/).waitFor();
  await page.getByRole('button',{name:'订单履约',exact:true}).click();await page.getByText('没有符合条件的订单').waitFor();
  assert.equal(await page.getByTestId('admin-order-detail').count(),0,'new actor must not retain prior private order cache');assert.equal(await page.getByText('+15550000001',{exact:false}).count(),0);
 }finally{release?.();}
},['OPERATOR']));

test('MOCK old failed read cannot revoke a newly recovered account',async()=>withRole('admin',async({page,start,handle})=>{
 let actor='actor',hold=false,release;const adminOrder={...order,address,overdue:false};handle(async({path,respond})=>{
  if(path==='/me'){await respond({id:actor,station_id:'station',roles:['OPERATOR'],csrf_token:'csrf-test'});return true;}
  if(path==='/admin/orders'){if(hold){hold=false;await new Promise(r=>release=r);await respond({code:'UNAUTHENTICATED',message:'旧会话失效',request_id:'synthetic'},401);}else await respond(pageOf([]));return true;}
 });
 try{
  await start();await page.getByRole('button',{name:'订单履约',exact:true}).click();await page.getByText('没有符合条件的订单').waitFor();
  hold=true;const held=page.waitForRequest(r=>r.url().includes('/admin/orders?'));await page.getByTestId('recover-session').click();await held;
  actor='new-actor';const fresh=page.waitForResponse(r=>r.url().includes('/admin/dashboard?')&&r.status()===200);
  await page.getByTestId('recover-session').click();await(await fresh).finished();await page.getByText('净实收',{exact:true}).waitFor();
  const stale=page.waitForResponse(r=>r.url().includes('/admin/orders?')&&r.status()===401);release();await(await stale).finished();await page.evaluate(()=>new Promise(requestAnimationFrame));
  assert.equal(await page.getByTestId('session-ready').count(),1,'late failure of old account must not clear new account');assert.equal(await page.getByRole('alert').filter({hasText:'旧会话失效'}).count(),0);
 }finally{release?.();}
},['OPERATOR']));

test('MOCK same account fresh CSRF recovery ignores old unauthorized read',async()=>withRole('admin',async({page,start,handle})=>{
 let token='csrf-old',hold=false,release;handle(async({path,respond})=>{
  if(path==='/me'){await respond({id:'actor',station_id:'station',roles:['OPERATOR'],csrf_token:token});return true;}
  if(path==='/admin/orders'){if(hold){hold=false;await new Promise(r=>release=r);await respond({code:'UNAUTHENTICATED',message:'旧会话失效',request_id:'synthetic'},401);}else await respond(pageOf([]));return true;}
 });
 try{await start();await page.getByRole('button',{name:'订单履约',exact:true}).click();await page.getByText('没有符合条件的订单').waitFor();hold=true;const held=page.waitForRequest(r=>r.url().includes('/admin/orders?'));await page.getByTestId('recover-session').click();await held;token='csrf-new';const fresh=page.waitForResponse(r=>r.url().includes('/admin/orders?')&&r.status()===200);await page.getByTestId('recover-session').click();await fresh;const stale=page.waitForResponse(r=>r.url().includes('/admin/orders?')&&r.status()===401);release();await(await stale).finished();await page.evaluate(()=>new Promise(requestAnimationFrame));assert.equal(await page.getByTestId('session-ready').count(),1,'same actor recovery must ignore prior session error');assert.equal(await page.getByRole('alert').filter({hasText:'旧会话失效'}).count(),0);}finally{release?.();}
},['OPERATOR']));

test('MOCK same account renewal retains unknown in-flight write with original key',async()=>withRole('user',async({page,start,handle,write})=>{
 let token='csrf-old',holdMe=false,releaseMe,releaseOrder,first=true;
 handle(async({path,req,respond})=>{
  if(path==='/me'){if(holdMe){holdMe=false;await new Promise(r=>releaseMe=r);}await respond({id:'actor',station_id:'station',roles:['CUSTOMER'],csrf_token:token});return true;}
  if(path==='/orders'&&req.method()==='POST'){if(first){first=false;await new Promise(r=>releaseOrder=r);await respond({code:'INTERNAL_ERROR',message:'结果未知',request_id:'synthetic'},500);}else await respond(order,201);return true;}
 });
 try{await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByTestId('quote').click();await page.getByTestId('confirm-order').waitFor();holdMe=true;const heldMe=page.waitForRequest(r=>r.url().endsWith('/me'));await page.getByTestId('recover-session').click();await heldMe;const heldOrder=page.waitForRequest(r=>r.url().endsWith('/orders')&&r.method()==='POST');await page.getByTestId('confirm-order').click();await heldOrder;token='csrf-new';const renewed=page.waitForResponse(r=>r.url().endsWith('/me'));releaseMe();await renewed;const failed=page.waitForResponse(r=>r.url().endsWith('/orders')&&r.status()===500);releaseOrder();await(await failed).finished();await page.evaluate(()=>new Promise(requestAnimationFrame));assert.equal(await page.getByTestId('check-uncertain').count(),1,'credential rotation must preserve unknown write for same business identity');assert.equal(await page.getByTestId('session-ready').count(),1);await page.getByTestId('check-uncertain').click();await page.getByTestId('retry-uncertain').click();await page.getByTestId('order-detail').waitFor();const attempts=write.filter(w=>w.path==='/orders');assert.equal(attempts.length,2);assert.equal(attempts[0].key,attempts[1].key);assert.equal(attempts[0].body,attempts[1].body);assert.equal(attempts[1].csrf,'csrf-new');}finally{releaseMe?.();releaseOrder?.();}
}));

test('MOCK scheduled date switches server day and clears old window and quotation',async()=>withRole('user',async({page,start,handle})=>{
 const dates=[];handle(async({path,req,respond})=>{if(path==='/delivery/slots'){const d=new URL(req.url()).searchParams.get('date');dates.push(d);await respond(pageOf([{...slot,id:d==='2026-11-22'?'day-3':slot.id}]));return true;}});
 await start();await page.getByTestId('add-s1').click();await page.getByRole('button',{name:/^购物袋/}).click();await page.getByLabel('配送方式',{exact:true}).selectOption('SCHEDULED');assert.equal(await page.getByLabel('预约日期',{exact:true}).count(),1,'scheduled checkout needs a server-day selector');await page.getByLabel('服务窗口',{exact:true}).selectOption(slot.id);await page.getByTestId('quote').click();await page.getByTestId('confirm-order').waitFor();await page.getByLabel('预约日期',{exact:true}).fill('2026-11-22');await page.getByLabel('服务窗口',{exact:true}).locator('option[value="day-3"]').waitFor({state:'attached'});assert.equal(await page.getByLabel('服务窗口',{exact:true}).inputValue(),'');assert.equal(await page.getByTestId('confirm-order').count(),0);assert.equal(await page.getByRole('radio',{name:'货到付款',exact:true}).isChecked(),true);assert.equal(dates.at(-1),'2026-11-22');
}));

test('MOCK rider all unread messages are written in independently keyed batches of 100',async()=>withRole('rider',async({page,start,handle,write})=>{
 const read=new Set();const mail=Array.from({length:205},(_,i)=>({id:`m${i}`,kind:'ORDER_URGE',task_id:'t1',created_at:'2026-11-19T03:00:00Z'}));handle(async({path,req,respond})=>{
  if(path==='/rider/messages'){await respond(pageOf(mail.map(m=>({...m,...(read.has(m.id)?{read_at:'2026-11-19T03:00:00Z'}:{})}))));return true;}
  if(path==='/rider/messages/read'){for(const id of req.postDataJSON().ids)read.add(id);await respond({accepted:true});return true;}
 });
 await start();await page.getByRole('button',{name:'消息',exact:true}).click();await page.getByTestId('read-all').click();await page.waitForFunction(()=>document.querySelector('[data-testid="read-all"]')?.disabled===true);const batches=write.filter(w=>w.path==='/rider/messages/read');assert.deepEqual(batches.map(w=>JSON.parse(w.body).ids.length),[100,100,5],'mark-read contract only accepts up to 100 ids per command');assert.equal(new Set(batches.map(w=>w.key)).size,3);
}));

test('MOCK rider failed explicit server check does not enable uncertain retry',async()=>withRole('rider',async({page,start,handle})=>{
 let offline=false;handle(async({path,route})=>{if(path==='/rider/tasks/t1/claim'){offline=true;await route.abort('failed');return true;}if(offline&&path==='/rider/tasks'){await route.abort('failed');return true;}});
 await start();await page.getByTestId('claim-t1').click();await page.getByTestId('check-uncertain').waitFor();await page.getByTestId('check-uncertain').click();await page.waitForFunction(()=>!Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent.includes('正在与服务器核对')));assert.equal(await page.getByTestId('retry-uncertain').isDisabled(),true,'failed server query must leave retry locked');
}));

test('MOCK rider periodic refresh preserves explicit COD confirmation for same task',async()=>withRole('rider',async({page,start,handle})=>{
 handle(async({path,respond})=>{if(path==='/rider/tasks'){await respond(pageOf([{...task,order_status:'IN_DELIVERY'}]));return true;}if(path==='/rider/tasks/t1'){await respond({...task,order_status:'IN_DELIVERY',address});return true;}});
 await start();await page.getByRole('button',{name:'当前任务',exact:true}).click();await page.getByLabel('已全额收到货到付款',{exact:true}).check();await page.clock.runFor(10020);
 const refreshed=page.waitForResponse(r=>r.url().endsWith('/rider/tasks/t1'));await page.getByTestId('refresh-tasks').click();await refreshed;
 assert.equal(await page.getByLabel('已全额收到货到付款',{exact:true}).isChecked(),true,'same active task should preserve the rider confirmation');
}));

test('MOCK rider claim conflict refreshes hall; hidden page pauses polling',async()=>withRole('rider',async({page,start,handle})=>{
 let claimed=false,reads=0;handle(async({path,respond})=>{if(path==='/rider/tasks'){reads++;await respond(pageOf(claimed?[]:[task]));return true;}if(path==='/rider/tasks/t1/claim'){claimed=true;await respond({code:'TASK_ALREADY_CLAIMED',message:'任务已被领取',request_id:'synthetic'},409);return true;}});
 await start();await page.getByTestId('claim-t1').click();await page.getByRole('alert').filter({hasText:'TASK_ALREADY_CLAIMED'}).waitFor();await page.getByText('大厅暂无可接任务').waitFor();assert.equal(await page.getByTestId('claim-t1').count(),0);
 await page.evaluate(()=>{Object.defineProperty(document,'hidden',{value:true,configurable:true});document.dispatchEvent(new Event('visibilitychange'));});const before=reads;await page.clock.runFor(120000);assert.equal(reads,before);
 await page.evaluate(()=>{Object.defineProperty(document,'hidden',{value:false,configurable:true});document.dispatchEvent(new Event('visibilitychange'));});await page.waitForResponse(r=>r.url().includes('/rider/tasks?'));assert.equal(reads,before+1);
}));

test('MOCK rider refresh holds one polling group and backs off after failed reads',async()=>withRole('rider',async({page,start,handle})=>{
 let reads=0,fail=false;handle(async({path,respond})=>{if(path==='/rider/tasks'){reads++;if(fail)await respond({code:'INTERNAL_ERROR',message:'暂不可用',request_id:'synthetic'},500);else await respond(pageOf([]));return true;}});
 await start();await page.getByText(/最近刷新/).waitFor();fail=true;const next=page.waitForResponse(r=>r.url().includes('/rider/tasks?'));await page.clock.runFor(10010);await next;const before=reads;await page.clock.runFor(10000);assert.equal(reads,before,'after failure the next 10s must not start another request');const retried=page.waitForResponse(r=>r.url().includes('/rider/tasks?'));await page.clock.runFor(10020);await retried;assert.equal(reads,before+1);
}));

test('MOCK finance permissions, MFA failure, and separate refund approval/payout requests',async()=>withRole('admin',async({page,start,handle,write})=>{
 let forbidden=true,paid=false;const refund={id:'f1',order_id:'o1',amount_fen:500,payment_method:'COD',status:'REFUND_PENDING',requested_at:'2026-11-19T03:00:00Z'};
 handle(async({path,respond})=>{
  if(path==='/admin/refunds'){await respond(pageOf([{...refund,status:paid?'REFUNDED':'REFUND_PENDING'}]));return true;}
  if(path==='/admin/refunds/f1/approve'){await respond(forbidden?{code:'FORBIDDEN',message:'需要近期 MFA',request_id:'synthetic'}:refund,forbidden?403:200);return true;}
  if(path==='/admin/refunds/f1/record-cod-payout'){paid=true;await respond({...refund,status:'REFUNDED'});return true;}
 });
 await start();assert.equal(await page.getByRole('button',{name:'订单履约',exact:true}).count(),0);assert.equal(await page.getByRole('button',{name:'COD 财务',exact:true}).count(),1);await page.getByRole('button',{name:'退款',exact:true}).click();await page.getByTestId('approve-refund-f1').click();await page.getByRole('alert').filter({hasText:'MFA'}).waitFor();assert.equal(write.filter(w=>w.path.endsWith('record-cod-payout')).length,0);
 forbidden=false;await page.getByTestId('approve-refund-f1').click();await page.getByRole('status').filter({hasText:'审批已由服务器记录'}).waitFor();assert.equal(await page.getByTestId('payout-refund-f1').isDisabled(),true);await page.getByLabel('实际 COD 兑付凭证号',{exact:true}).fill('SYNTHETIC_RECEIPT');await page.getByTestId('payout-refund-f1').click();await page.getByText('已退款',{exact:true}).waitFor();
 const approve=write.filter(w=>w.path.endsWith('/approve')).at(-1),payout=write.find(w=>w.path.endsWith('/record-cod-payout'));assert.deepEqual(JSON.parse(approve.body),{reason_code:'OPERATOR_REQUEST'});assert.deepEqual(JSON.parse(payout.body),{receipt_reference:'SYNTHETIC_RECEIPT'});assert.notEqual(approve.key,payout.key);
}));
