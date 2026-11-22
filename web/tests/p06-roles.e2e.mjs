import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createVite } from 'vite';
import { chromium } from 'playwright-core';

const me = role => ({ id: `${role}-actor`, station_id:'station', roles:[{user:'CUSTOMER',rider:'RIDER',admin:'OPERATOR'}[role]], csrf_token:'test-csrf' });
const slot = {id:'slot-1',starts_at:'2026-11-19T20:00:00+08:00',ends_at:'2026-11-19T20:30:00+08:00',dispatch_at:'2026-11-19T19:50:00+08:00',accepting:true};
const product = {id:'p1',name:'测试饮品',description:'真实接口商品描述',skus:[{id:'sku-1',label:'单件',units_per_pack:1,unit_price_fen:350}]};
const order = {id:'order-1',delivery_mode:'ASAP',slot,status:'PREPARING',stock_status:'UNSORTED',settlement_status:'COD_UNCOLLECTED',payment_method:'COD',total_fen:350,lines:[{sku_id:'sku-1',qty:1,name:product.name,spec:'单件',unit_price_fen:350,line_total_fen:350}],created_at:'2026-11-19T19:00:00+08:00'};
const paidOrder = {...order, payment_method:'BALANCE', settlement_status:'BALANCE_PAID'};
for (const role of ['user','rider','admin']) test(`MOCK ${role}: accessible entry and contract actions`, async () => {
  assert.equal(existsSync(`src/${role}/entry.ts`), true, `${role} must provide mountable entry`);
  const vite = await createVite({configFile:false,server:{middlewareMode:true,hmr:false,ws:false},appType:'custom'});
  const server = createServer((req,res) => {
    if(req.url === '/') res.setHeader('Content-Type','text/html').end(`<html lang="zh-CN"><div id="app" data-entry="${role}"></div><script type="module" src="/src/${role}/entry.ts"></script></html>`);
    else vite.middlewares(req,res);
  });
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const browser = await chromium.launch({executablePath:process.env.BROWSER_EXECUTABLE || 'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true});
  try {
    const page=await browser.newPage({viewport:{width:390,height:900}});const writes=[];let created=false,taskStage='READY_FOR_CLAIM',prepared=false,delayAddress=false,releaseAddress;
    await page.route('**/api/v1/**',async route=>{
      const req=route.request(),url=new URL(req.url()),path=url.pathname.replace('/api/v1','');
      if(req.method()!=='GET'){writes.push({path,body:req.postDataJSON(),headers:req.headers()});}
      let value={items:[],next_cursor:null};
      if(path==='/me')value=me(role);
      else if(path==='/catalog/products')value={items:[product],next_cursor:null};
      else if(path==='/catalog/products/p1')value=product;
      else if(path==='/me/addresses'){if(delayAddress)await new Promise(resolve=>releaseAddress=resolve);value={items:[{id:'address-1',building_id:'b1',building_name:'一号楼',room:'101',contact_name:'合成人',contact_phone:'13800000000',version:1}],next_cursor:null};}
      else if(path==='/delivery/slots')value={items:[slot],next_cursor:null};
      else if(path==='/payment-methods')value={methods:[{code:'COD',enabled:true,label:'货到付款'},{code:'BALANCE',enabled:true,label:'余额'},{code:'WECHAT',enabled:false,label:'微信待接入'}]};
      else if(path==='/me/wallet')value={balance_fen:1000,currency:'CNY'};
      else if(path==='/checkout/quote')value={quote_id:'quote-1',valid_until:'2099-01-01T00:00:00Z',slot_id:slot.id,lines:order.lines,total_fen:350,payment_method:'BALANCE'};
      else if(path==='/orders'&&req.method()==='POST'){created=true;value=paidOrder;}
      else if(path==='/orders')value={items:created?[paidOrder]:[],next_cursor:null};
      else if(path==='/orders/order-1')value=paidOrder;
      else if(path==='/rider/tasks')value={items:taskStage==='DELIVERED'?[]:[{id:'task-1',building_name:'一号楼',item_count:1,fee_fen:100,slot,order_status:taskStage,payment_method:'COD'}],next_cursor:null};
      else if(path.startsWith('/rider/tasks/task-1')){if(path.endsWith('/claim'))taskStage='AWAITING_PICKUP';if(path.endsWith('/pickup'))taskStage='IN_DELIVERY';if(path.endsWith('/deliver'))taskStage='DELIVERED';value={id:'task-1',building_name:'一号楼',item_count:1,fee_fen:100,slot,order_status:taskStage,payment_method:'COD',...(taskStage==='IN_DELIVERY'?{cod_due_fen:500}:{}),address:{building_name:'一号楼',building_id:'b1',room:'101',contact_name:'合成人',contact_phone:'13800000000'}};}
      else if(path==='/rider/capacity')value={active:0,limit:8,remaining:8};
      else if(path==='/rider/availability')value={available:true};
      else if(path==='/rider/leaderboard')value={local_date:'2026-11-19',entries:[]};
      else if(path==='/admin/order-limits')value={merchant_batch_limit:15,rider_active_limit:8,version:1};
      else if(path==='/rider/availability')value={available:true};
      else if(path==='/admin/dashboard')value={local_date:'2026-11-19',order_count:1,delivered_count:0,net_collected_fen:0,overdue_count:0};
      else if(path==='/admin/orders')value={items:[{...order,address:{building_name:'一号楼',building_id:'b1',room:'101',contact_name:'合成人',contact_phone:'13800000000'},overdue:false}],next_cursor:null};
      else if(path.startsWith('/admin/orders/order-1')){if(path.endsWith('/prepare'))prepared=true;value={...order,address:{building_name:'一号楼',room:'101'},stock_status:prepared?'SORTED':'UNSORTED',overdue:false};}
      await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(value)});
    });
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.locator(`main[data-entry="${role}"]`).waitFor();
    await page.getByTestId('session-ready').waitFor();
    assert.equal(await page.getByRole('heading',{level:1}).count(),1);
    if(role==='user'){
      await page.getByTestId('add-sku-1').click();await page.getByTestId('start-checkout').click();
      assert.equal(await page.getByLabel('微信待接入').isDisabled(),true);
      await page.getByTestId('quote').click();await page.getByTestId('confirm-order').click();
      await page.getByTestId('order-detail').locator('p').getByText('余额已扣',{exact:true}).waitFor();
      const saved=await page.evaluate(()=>JSON.parse(localStorage.getItem('xiaoling.cart')||'[]'));
      for(const line of saved)assert.deepEqual(Object.keys(line).sort(),['qty','sku_id']);
      await page.getByRole('status').filter({hasText:'服务器已确认订单'}).waitFor();delayAddress=true;
      const requested=page.waitForRequest(r=>r.url().includes('/me/addresses'));
      await page.getByTestId('recover-session').click();await requested;
      await page.getByRole('button',{name:'退出登录',exact:true}).click();
      releaseAddress();
      await page.getByRole('button',{name:'商品',exact:true}).click();
      await page.getByTestId('add-sku-1').click();await page.getByTestId('start-checkout').click();
      await page.getByTestId('auth-modes').waitFor();
      assert.equal(await page.getByTestId('session-ready').count(),0);
      assert.equal((await page.getByLabel('配送地址',{exact:true}).locator('option').allTextContents()).some(t=>t.includes('101')),false,'logout must clear address data and discard late responses');
    } else if(role==='rider'){
      assert.equal(await page.getByText('13800000000',{exact:false}).count(),0);
      await page.getByTestId('claim-task-1').click();await page.getByTestId('pickup').click();
      assert.equal(await page.getByTestId('deliver').isDisabled(),true);
      await page.getByLabel('已全额收到货到付款').check();await page.getByTestId('deliver').click();
      assert.deepEqual(writes.find(x=>x.path.endsWith('/deliver')).body,{cod_received:true});
    } else {
      await page.getByRole('button',{name:'订单履约',exact:true}).click();
      await page.getByTestId('order-order-1').click();await page.getByTestId('prepare').click();
      assert.deepEqual(writes.find(x=>x.path.endsWith('/prepare')).body,{sorted:true});
      assert.equal(await page.getByRole('button',{name:'COD 财务',exact:true}).count(),0);
    }
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    for(const w of writes.filter(x=>!x.path.startsWith('/auth/')))assert.equal(w.headers['x-csrf-token'],'test-csrf');
  } finally {await browser.close();await vite.close();await new Promise(r=>server.close(r));}
});
