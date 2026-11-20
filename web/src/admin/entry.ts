import { createApp, h, ref, onMounted, onUnmounted, watch } from 'vue';
import { ApiError, apiRequest, assertNoProductionMock } from '../shared/api.ts';
import { ProductPhoto } from '../shared/product-photo.ts';
import { moneyAmount, moneyPattern, parseMoney } from '../shared/money.ts';
import type { AdminOrder, AdminOrderList, Dashboard, DeliveryFee, DeliveryFeeUpdate, AdminProduct, AdminProductList, ProductImage, ProductInput, ProductUpdate, SKUInput, InventoryAdjustment, Promotion, PromotionUpdate, AdminSlot, AdminSlotList, SlotBatch, SlotUpdate, Rider, RiderList, RiderUpdate, CODReceipt, CODReceiptList, CODBatch, CODBatchInput, CODVerify, Refund, RefundList, RestockInput, ReturnInspect, Reason, PayoutInput, RiderProvision } from '../shared/generated.ts';
import type { MerchantSettlement, MerchantSettlementList, OrderBatchResult, OrderLimits, OrderLimitsUpdate } from '../shared/generated.ts';
import { session, localTestLogin, sessionKey, shell, money, date, today, id, btn, field, select, check, card, empty, badge, form, stateText } from '../user/ui.ts';
// difference_fen is the stored ledger direction (expected - actual); the label is
// shared with the tests so the page can never subtract the two sides itself.
import { codDifference } from '../shared/cod.ts';

type StockRow={inventory_id:string;units:number};
type ProductForm = Omit<ProductInput, 'skus'> & {
 skus: (Omit<SKUInput, 'unit_price_fen'> & {price: string})[];
};
const amountField = (label: string, value: string, update: (value: string) => void, disabled = false) =>
 field(label, value, update, 'text', {inputmode: 'decimal', pattern: moneyPattern, title: '请输入非负金额，最多两位小数', required: true, disabled});
const localTime=(iso:string)=>{const p=new Intl.DateTimeFormat('sv-SE',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date(iso));return p.replace(' ','T');};
const toISO=(local:string)=>`${local}:00+08:00`;
export const AdminApp={setup(){
 const tab=ref('经营看板'),day=ref(today()),dashboard=ref<Dashboard>(),orders=ref<AdminOrder[]>([]),order=ref<AdminOrder>(),filter=ref('ALL'),orderQuery=ref(''),products=ref<AdminProduct[]>([]),editing=ref<AdminProduct>(),slots=ref<AdminSlot[]>([]),riders=ref<Rider[]>([]),receipts=ref<CODReceipt[]>([]),refunds=ref<Refund[]>([]),batch=ref<CODBatch>(),batchId=ref(''),receiptSelection=ref<string[]>([]),batchRider=ref(''),actualAmount=ref('0'),reason=ref('OPERATOR_REQUEST'),reference=ref(''),stock=ref<StockRow[]>([]);
 const productForm=ref<ProductForm>({name:'',description:'',skus:[{label:'单件',units_per_pack:1,price:'0'}],on_sale:false,reorder_point:0});
 const imageError=ref('');
 const deliveryFee = ref<DeliveryFee>();
 const deliveryFeeAmount = ref('');
 const commissionPercent = ref('');
 const merchantSettlements = ref<MerchantSettlement[]>([]);
 const payoutReferences = ref<Record<string,string>>({});
 const orderLimits = ref<OrderLimits>();
 const merchantLimit = ref('');
 const riderLimit = ref('');
 const orderSelection = ref<string[]>([]);
 const promoStart=ref(''),promoEnd=ref(''),promoTiers=ref([{min_qty:1,discount_bps:0}]);
 const inventoryDelta=ref(0),batchForm=ref<SlotBatch>({local_date:today(),opens_at:'09:00',closes_at:'22:00',lead_minutes:10,capacity:10});
 const capacities=ref<Record<string,number>>({}),accepting=ref<Record<string,boolean>>({}),makers=new Map<string,string>();
 const provisionIssuer=ref(''),provisionSubject=ref(''),provisionName=ref('');
 const s=session(['OPERATOR','FINANCE','SECURITY_ADMIN'],refresh,error=>
  error.code==='ORDER_LIMIT_REACHED'?'所选订单超过当前单次接单上限，请减少选择后重试。':undefined);
 const testLogin=localTestLogin('admin',s);
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){dashboard.value=undefined;orders.value=[];order.value=undefined;products.value=[];editing.value=undefined;imageError.value='';slots.value=[];riders.value=[];receipts.value=[];refunds.value=[];batch.value=undefined;batchId.value='';receiptSelection.value=[];batchRider.value='';actualAmount.value='0';reason.value='OPERATOR_REQUEST';reference.value='';stock.value=[];orderQuery.value='';filter.value='ALL';productForm.value={name:'',description:'',skus:[{label:'单件',units_per_pack:1,price:'0'}],on_sale:false,reorder_point:0};promoStart.value='';promoEnd.value='';promoTiers.value=[{min_qty:1,discount_bps:0}];inventoryDelta.value=0;capacities.value={};accepting.value={};makers.clear();}},{flush:'sync'});
 const operator=()=>!!s.me.value?.roles.includes('OPERATOR'),finance=()=>!!s.me.value?.roles.includes('FINANCE'),security=()=>!!s.me.value?.roles.includes('SECURITY_ADMIN');
 const business=()=>operator()||finance();
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){tab.value=security()&&!business()?'骑手管理':'经营看板';provisionIssuer.value='';provisionSubject.value='';provisionName.value='';deliveryFee.value=undefined;deliveryFeeAmount.value='';commissionPercent.value='';merchantSettlements.value=[];payoutReferences.value={};}},{flush:'sync'});
 watch(()=>sessionKey(s.me.value),(value,prior)=>{
  if(value===prior)return;
  orderLimits.value=undefined;
  merchantLimit.value='';
  riderLimit.value='';
  orderSelection.value=[];
 },{flush:'sync'});
 const isBusy=()=>s.busy.value||!!s.pending.value;
 let orderRefreshTimer: ReturnType<typeof setTimeout> | undefined;
 let orderRefreshRequest: AbortController | undefined;
 const canRefreshOrders=()=>tab.value==='订单履约'&&operator()&&!s.busy.value&&!document.hidden;
 function stopOrderRefresh(){
  clearTimeout(orderRefreshTimer);
  orderRefreshRequest?.abort();
  orderRefreshRequest=undefined;
 }
 function scheduleOrderRefresh(){
  if(canRefreshOrders())orderRefreshTimer=setTimeout(()=>void pollOrders(),10_000);
 }
 async function refreshOrderList(signal?:AbortSignal){
  const {items}=await s.list<AdminOrderList>('/admin/orders?limit=100',signal);
  if(!signal?.aborted){
   orders.value=items;
   orderSelection.value=orderSelection.value.filter(key=>items.some(o=>o.id===key&&o.status==='PREPARING'));
  }
  return items;
 }
 async function pollOrders(){
  if(!canRefreshOrders()||orderRefreshRequest)return;
  const request=new AbortController();
  orderRefreshRequest=request;
  try{
   const items=await refreshOrderList(request.signal);
   if(request.signal.aborted)return;
   const detail=items.find(o=>o.id===order.value?.id);
   if(detail)order.value=detail;
  }catch{
   // The session reports request failures. Resume polling when the page is active.
  }finally{
   if(orderRefreshRequest===request){
    orderRefreshRequest=undefined;
    scheduleOrderRefresh();
   }
  }
 }
 function onOrderVisibilityChange(){
  stopOrderRefresh();
  if(!document.hidden)void pollOrders();
 }
 watch([tab,s.busy,s.me],()=>{
  stopOrderRefresh();
  scheduleOrderRefresh();
 },{flush:'sync'});
 async function refresh(){if(!s.me.value)return;const jobs:Promise<unknown>[]=[];
  if(tab.value==='经营看板'&&business())jobs.push(s.get<Dashboard>(`/admin/dashboard?date=${day.value}`).then(v=>dashboard.value=v));
  if(tab.value==='订单履约'&&operator()){
   jobs.push(refreshOrderList());
   jobs.push(s.get<OrderLimits>('/admin/order-limits').then(v=>orderLimits.value=v));
   if(order.value)jobs.push(s.get<AdminOrder>(`/admin/orders/${id(order.value.id)}`).then(v=>order.value=v));
  }
  if(tab.value==='接单设置'&&operator())jobs.push(s.get<OrderLimits>('/admin/order-limits').then(showOrderLimits));
  if(tab.value==='商品与库存'&&operator())jobs.push(loadProducts());
  if(tab.value==='配送费'&&operator())jobs.push(s.get<DeliveryFee>('/admin/delivery-fee').then(showDeliveryFee));
  if(tab.value==='商家结算'&&finance())jobs.push(loadMerchantSettlements());
  if(tab.value==='服务时段'&&operator())jobs.push(s.list<AdminSlotList>(`/admin/slots?date=${day.value}&limit=100`).then(v=>{slots.value=v.items;for(const w of v.items){capacities.value[w.id]=w.capacity;accepting.value[w.id]=w.accepting;}}));
  if(tab.value==='骑手管理'&&operator())jobs.push(s.list<RiderList>('/admin/riders?limit=100').then(v=>riders.value=v.items));
  if(tab.value==='COD 财务'&&finance())jobs.push(s.list<CODReceiptList>(`/admin/cod/receipts?date=${day.value}&limit=100`).then(v=>{receipts.value=v.items;receiptSelection.value=receiptSelection.value.filter(k=>v.items.some(r=>r.id===k&&r.remittance_status==='NOT_REMITTED'));}));
  if(tab.value==='退款'&&business())jobs.push(s.list<RefundList>('/admin/refunds?limit=100').then(v=>refunds.value=v.items));
  await Promise.all(jobs);
 }
 async function loadProducts(){products.value=(await s.list<AdminProductList>('/admin/products?limit=100')).items;}
 function go(t:string){tab.value=t;void s.run(refresh);}
 async function openOrder(o:AdminOrder){await s.run(async()=>{order.value=await s.get<AdminOrder>(`/admin/orders/${id(o.id)}`);stock.value=[];if(!products.value.length)await loadProducts();for(const p of products.value){if(o.lines.some(l=>p.skus.some(k=>k.id===l.sku_id)))stock.value.push({inventory_id:p.inventory.id,units:0});}});}
 async function orderAction(action:string,body?:unknown){
  if(!order.value)return;
  await s.write<AdminOrder|Refund>(`/admin/orders/${id(order.value.id)}/${action}`,body,async v=>{
   if('stock_status' in v)order.value=v;
   await refresh();
   s.notice.value=`服务器已确认${({prepare:'备货',dispatch:'放单',reclaim:'回收',cancel:'取消',restock:'归架','return-inspect':'验退',refund:'退款申请'} as Record<string,string>)[action]||action}。`;
  },'POST',async error=>{
   if(action==='dispatch'&&error.code==='INVALID_STATE')await refresh();
  });
 }
 async function runOrderBatch(action:'accept'|'ready', ids:string[]){
  await s.write<OrderBatchResult>(`/admin/order-batches/${action}`,{ids},async result=>{
   orderSelection.value=orderSelection.value.filter(key=>!ids.includes(key));
   await refresh();
   s.notice.value=`已${action==='accept'?'接单':'出餐'} ${result.count} 单。`;
  });
  if(s.error.value&&!s.pending.value&&s.me.value){try{await refresh();}catch(error){s.failure(error);}}
 }
 function showOrderLimits(value:OrderLimits){
  orderLimits.value=value;
  merchantLimit.value=String(value.merchant_batch_limit);
  riderLimit.value=String(value.rider_active_limit);
 }
 function orderLimitsView(){
  const saved=orderLimits.value;
  const maxLimit=2147483647;
  const valid=(value:string)=>/^[1-9][0-9]*$/.test(value)&&Number(value)<=maxLimit;
  return card('接单上限',saved?[form([
   field('商家每次最多接单',merchantLimit.value,v=>merchantLimit.value=v,'number',{min:1,max:maxLimit,step:1,required:true}),
   field('每位骑手手上最多订单',riderLimit.value,v=>riderLimit.value=v,'number',{min:1,max:maxLimit,step:1,required:true}),
   h('p',{class:'muted'},'骑手的待取货、配送中和退回中订单都占用名额。下调上限后，骑手先处理手上的订单，有空余名额后即可接新单。'),
   h('button',{type:'submit',class:'btn primary',disabled:isBusy()||!valid(merchantLimit.value)||!valid(riderLimit.value),'data-testid':'save-order-limits'},'保存接单上限'),
  ],async()=>{
   if(!valid(merchantLimit.value)||!valid(riderLimit.value))return;
   const body:OrderLimitsUpdate={merchant_batch_limit:Number(merchantLimit.value),rider_active_limit:Number(riderLimit.value),expected_version:saved.version};
   await s.write<OrderLimits>('/admin/order-limits',body,v=>{showOrderLimits(v);s.notice.value='接单上限已更新。';},'PUT');
   if(s.error.value&&!s.pending.value&&s.me.value){
    try{orderLimits.value=await s.get<OrderLimits>('/admin/order-limits');}catch(error){s.failure(error);}
   }
  })]:[empty('正在读取接单设置')]);
 }
 const merchantOrderStatus=(o:AdminOrder)=>o.status==='PREPARING'&&!o.merchant_accepted?'待商家接单':o.status;
 function orderDetail(){const o=order.value;if(!o)return null;return card('订单作业详情',[
 h('p',`订单 ${o.id} · ${date(o.created_at)}`),h('div',[badge(merchantOrderStatus(o)),badge(o.stock_status),badge(o.settlement_status),o.overdue?h('span',{class:'badge'},'已逾期'):null]),h('p',{class:'price'},money(o.total_fen)),h('p',`配送费 ${money(o.delivery_fee_fen ?? o.total_fen - o.lines.reduce((sum, line) => sum + line.line_total_fen, 0))}`),h('p',`${o.delivery_mode==='SCHEDULED'?'预约配送':'即时配送'} · ${date(o.slot.starts_at)} — ${date(o.slot.ends_at)}`),h('p',`放单时刻 ${date(o.slot.dispatch_at)}`),h('div',{'data-sensitive':true},[h('p',`${o.address.building_name} ${o.address.room}`),h('p',`${o.address.contact_name} · ${o.address.contact_phone}`)]),...o.lines.map(l=>h('p',{class:'item'},`${l.name} · ${l.spec} × ${l.qty} · ${money(l.line_total_fen)}`)),field('操作原因码',reason.value,v=>reason.value=v,'text',{pattern:'[A-Z0-9_]{1,64}'}),h('div',{class:'actions'},[
 o.status==='PREPARING'&&!o.merchant_accepted?btn('接单',()=>runOrderBatch('accept',[o.id]),isBusy()||!orderLimits.value,'merchant-accept','primary'):null,
 btn('备货完成',()=>orderAction('prepare',{sorted:true}),isBusy()||o.status!=='PREPARING'||o.stock_status!=='UNSORTED'||o.merchant_accepted===false,'prepare','primary'),
 btn('放单到大厅',()=>orderAction('dispatch'),isBusy()||o.status!=='PREPARING'||o.stock_status!=='SORTED','dispatch','primary'),
 btn('回收骑手任务',()=>orderAction('reclaim',{reason_code:reason.value}),isBusy()||o.status!=='AWAITING_PICKUP'||!reason.value,'reclaim'),
 btn('取消订单',()=>orderAction('cancel',{reason_code:reason.value}),isBusy()||!['SCHEDULED','PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP'].includes(o.status)||!reason.value,'cancel','red'),
 btn('申请全额退款',()=>orderAction('refund',{reason_code:reason.value}),isBusy()||o.status!=='DELIVERED'||!reason.value,'refund')]),
 o.status==='SCHEDULED'?h('p',{class:'note'},'预约尚未由服务器唤醒。到放单时刻后由 worker 推进，当前不能提前备货或放单。'):null,
 (o.stock_status==='RESTOCK_PENDING'||o.status==='RETURNING')?h('div',[h('h3',o.status==='RETURNING'?'退回验收':'取消订单归架'),h('p','逐库存填写实际数量。取消归架填写损坏数量，验退填写可售数量；未填或零的库存行不提交。'),...stock.value.map(row=>field(`${o.status==='RETURNING'?'可售':'损坏'}件数 · ${products.value.find(p=>p.inventory.id===row.inventory_id)?.name||row.inventory_id}`,row.units,v=>row.units=Number(v),'number',{min:0,max:99999,step:1})),h('div',{class:'actions'},[o.status==='RETURNING'?btn('确认退回验收',()=>{const input:ReturnInspect={reason_code:reason.value,sellable:stock.value.filter(r=>r.units>0).map(r=>({inventory_id:r.inventory_id,sellable_units:r.units}))};return orderAction('return-inspect',input);},isBusy()||!reason.value,'return-inspect','primary'):btn('确认分拣归架',()=>{const input:RestockInput={reason_code:reason.value,damaged:stock.value.filter(r=>r.units>0).map(r=>({inventory_id:r.inventory_id,damaged_units:r.units}))};return orderAction('restock',input);},isBusy()||!reason.value,'restock','primary')])]):null,
 btn('关闭订单详情',()=>order.value=undefined)
 ],{'data-testid':'admin-order-detail',class:'card detail'});}
 function ordersView(){
  const list=orders.value.filter(o=>{
   const matches=filter.value==='ALL'||(filter.value==='OVERDUE'&&o.overdue)||
    (filter.value==='UNACCEPTED'&&merchantOrderStatus(o)==='待商家接单')||
    (filter.value===o.status&&(o.status!=='PREPARING'||o.merchant_accepted));
   return matches&&(!orderQuery.value||o.id.includes(orderQuery.value)||o.lines.some(l=>l.name.includes(orderQuery.value)));
  });
  const selection=list.filter(o=>orderSelection.value.includes(o.id));
  const stage=selection[0]?.merchant_accepted?'ready':'accept';
  const limit=orderLimits.value?.merchant_batch_limit;
  const ids=selection.map(o=>o.id);
  const sameStage=selection.every(o=>!!o.merchant_accepted===(stage==='ready'));
  const options:Array<[string,string]>=[
   ['ALL','全部订单'],['UNACCEPTED','待商家接单'],['SCHEDULED','预约待备货'],['OVERDUE','逾期'],
   ...['PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP','IN_DELIVERY','RETURNING','RETURNED','DELIVERED','CANCELED'].map(v=>[v,stateText(v)] as [string,string]),
  ];
  return h('div',[
   h('div',{class:'split'},[
    field('搜索订单或商品',orderQuery.value,v=>{orderQuery.value=v;orderSelection.value=[];},'search'),
    select('订单筛选',filter.value,options,v=>{filter.value=v;orderSelection.value=[];}),
   ]),
   h('div',{class:'actions'},[
    h('span',{class:'muted','aria-live':'polite'},`已选 ${ids.length} 单${limit===undefined?'':` · 单次最多接 ${limit} 单`}`),
    btn('批量接单',()=>runOrderBatch('accept',ids),isBusy()||!ids.length||!sameStage||stage!=='accept'||limit===undefined||ids.length>limit,'merchant-batch-accept','primary'),
    btn('批量出餐',()=>runOrderBatch('ready',ids),isBusy()||!ids.length||!sameStage||stage!=='ready','merchant-batch-ready','primary'),
    btn('清除选择',()=>orderSelection.value=[],isBusy()||!ids.length),
   ]),
   orderDetail(),
   list.length?h('div',{class:'grid order-batch-grid'},list.map(o=>{
    const selected=ids.includes(o.id);
    const canSelect=!ids.length||!!o.merchant_accepted===(stage==='ready');
    const belowLimit=o.merchant_accepted||ids.length<(limit??0);
    return card(`订单 ${o.id}`,[
     h('p',`${date(o.created_at)} · ${o.delivery_mode==='SCHEDULED'?'预约':'即时'}`),
     h('div',[badge(merchantOrderStatus(o)),badge(o.stock_status),badge(o.settlement_status)]),
     h('p',{class:'price'},money(o.total_fen)),
     o.overdue?h('p',{class:'error'},'服务器标记逾期'):null,
     o.status==='PREPARING'?check('选择订单',selected,v=>{
      orderSelection.value=v?[...ids,o.id]:ids.filter(key=>key!==o.id);
     },isBusy()||(!selected&&(!canSelect||!belowLimit))):null,
     btn('查看并处理',()=>openOrder(o),s.busy.value,`order-${o.id}`),
    ],{key:o.id,'data-testid':`merchant-order-${o.id}`});
   })):empty('没有符合条件的订单'),
  ]);
 }
 function editProduct(p?:AdminProduct){editing.value=p;imageError.value='';productForm.value=p?{name:p.name,description:p.description,image_url:p.image_url,skus:p.skus.map(({label,units_per_pack,unit_price_fen})=>({label,units_per_pack,price:moneyAmount(unit_price_fen)})),on_sale:p.on_sale,reorder_point:p.inventory.reorder_point}:{name:'',description:'',skus:[{label:'单件',units_per_pack:1,price:'0'}],on_sale:false,reorder_point:0};inventoryDelta.value=0;promoStart.value=p?.promotion?localTime(p.promotion.starts_at):'';promoEnd.value=p?.promotion?localTime(p.promotion.ends_at):'';promoTiers.value=p?.promotion?p.promotion.tiers.map(t=>({...t})):[{min_qty:1,discount_bps:0}];}
 async function productSaved(p:AdminProduct){await loadProducts();editProduct(p);s.notice.value='商品已保存，版本已刷新。';}
 async function chooseProductPhoto(event: Event) {
  const input = event.currentTarget as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file || isBusy() || !s.me.value) return;
  imageError.value = '';
  if (file.type !== 'image/jpeg' && file.type !== 'image/png') {
   imageError.value = '请选择 JPEG 或 PNG 图片。';
   return;
  }
  if (file.size === 0 || file.size > 5 * 1024 * 1024) {
   imageError.value = '请选择大小在 5 MB 以内的图片。';
   return;
  }
  const actor = sessionKey(s.me.value);
  const csrf = s.me.value.csrf_token;
  await s.run(async () => {
   try {
    const result = await apiRequest<ProductImage>('/admin/product-images', {
     method: 'POST', body: file, csrf, signal: AbortSignal.timeout(15000),
    });
    if (sessionKey(s.me.value) === actor) productForm.value.image_url = result.image_url;
   } catch (error) {
    if (sessionKey(s.me.value) !== actor) return;
    if (error instanceof ApiError && error.status === 400) {
     imageError.value = '无法读取这张图片，请选择有效且长宽均不超过 4096 像素的 JPEG 或 PNG 图片。';
     return;
    }
    if (error instanceof ApiError && error.status === 413) {
     imageError.value = '图片超过 5 MB，请缩小图片后重试。';
     return;
    }
    throw error;
   }
  });
 }
 function productPhotoField() {
  const url = productForm.value.image_url;
  return h('div', {class: 'product-photo-field'}, [
   url ? h(ProductPhoto, {url, name: productForm.value.name, large: true}) : null,
   h('label', {class: 'field'}, [
    h('span', '商品主图'),
    h('input', {type: 'file', accept: 'image/jpeg,image/png', 'aria-describedby': 'product-photo-help', onChange: chooseProductPhoto, disabled: isBusy()}),
   ]),
   h('p', {class: 'muted', id: 'product-photo-help'}, '支持 JPEG 和 PNG，单张最多 5 MB，长宽均不超过 4096 像素。保存商品后生效。'),
   url ? btn('移除主图', () => { productForm.value.image_url = undefined; imageError.value = ''; }, isBusy()) : null,
   imageError.value ? h('p', {class: 'error', role: 'alert'}, imageError.value) : null,
  ]);
 }
 async function saveProduct() {
  const { skus: drafts, ...fields } = productForm.value;
  const skus: SKUInput[] = [];
  for (const { price, ...sku } of drafts) {
   const fen = parseMoney(price);
   if (fen === undefined || fen < 1) {
    s.error.value = '规格价格须至少为 0.01 块，最多两位小数。';
    return;
   }
   skus.push({ ...sku, unit_price_fen: fen });
  }
  const value = editing.value;
  if (value) {
   const body: ProductUpdate = {
    ...fields,
    image_url: fields.image_url === value.image_url ? undefined : fields.image_url ?? '',
    expected_version: value.version,
    skus: skus.map((sku, i) => ({ ...sku, id: value.skus[i].id })),
   };
   await s.write<AdminProduct>(`/admin/products/${id(value.id)}`, body, productSaved, 'PATCH');
  } else {
   await s.write<AdminProduct>('/admin/products', { ...fields, skus }, productSaved);
  }
 }
 function productsView(){return h('div',{class:'split'},[card('站点商品与库存',[
 btn('新建商品',()=>editProduct(),s.busy.value,'new-product'),...products.value.map(p=>h('article',{class:'item'},[h('h3',p.name),h('p',p.on_sale?'在售':'已下架'),h('p',`实存 ${p.inventory.on_hand} · 预占 ${p.inventory.reserved} · 补货阈值 ${p.inventory.reorder_point}`),h('p',{class:'muted'},`商品 v${p.version} / 库存 v${p.inventory.version}`),...p.skus.map(k=>h('p',`${k.label} · ${k.units_per_pack} 件/包 · ${money(k.unit_price_fen)}`)),p.promotion?h('p',`促销 ${date(p.promotion.starts_at)} — ${date(p.promotion.ends_at)}`):h('p',{class:'muted'},'当前无促销'),btn('编辑商品 / 库存 / 促销',()=>editProduct(p),s.busy.value,`edit-product-${p.id}`)])),!products.value.length?empty('暂无商品，请新建商品后调整入库'):null]),h('div',[
 card(editing.value?'编辑商品和规格':'新建商品',[form([
 field('商品名称',productForm.value.name,v=>productForm.value.name=v,'text',{required:true,maxlength:80}),field('商品描述',productForm.value.description,v=>productForm.value.description=v,'text',{required:true,maxlength:1000}),field('补货阈值',productForm.value.reorder_point,v=>productForm.value.reorder_point=Number(v),'number',{required:true,min:0,step:1}),check('上架销售',productForm.value.on_sale,v=>productForm.value.on_sale=v),
  productPhotoField(),
 ...productForm.value.skus.map((k,index)=>h('fieldset',[h('legend',`规格 ${index+1}`),field('规格名称',k.label,v=>k.label=v,'text',{required:true}),select('每包件数',String(k.units_per_pack),[['1','单件'],['3','三件装']],v=>k.units_per_pack=Number(v) as 1|3),amountField('规格价格（块）',k.price,v=>k.price=v),!editing.value&&productForm.value.skus.length>1?btn('移除规格',()=>productForm.value.skus.splice(index,1)):null])),
 !editing.value?btn('新增规格',()=>productForm.value.skus.push({label:'三件装',units_per_pack:3,price:'0'})):null,
 h('div',{class:'actions'},[h('button',{type:'submit',class:'btn primary',disabled:isBusy(),'data-testid':'save-product'},'保存商品')])],saveProduct)]),
 editing.value?card('库存调整',[h('p',`库存 ${editing.value.inventory.id} · 当前版本 ${editing.value.inventory.version}`),form([field('调整件数（正数入库，负数出库）',inventoryDelta.value,v=>inventoryDelta.value=Number(v),'number',{required:true,step:1}),field('库存调整原因码',reason.value,v=>reason.value=v,'text',{required:true,pattern:'[A-Z0-9_]{1,64}'}),h('button',{type:'submit',class:'btn primary',disabled:isBusy()||inventoryDelta.value===0,'data-testid':'adjust-inventory'},'确认调整库存')],async()=>{const p=editing.value!;const body:InventoryAdjustment={inventory_id:p.inventory.id,delta:inventoryDelta.value,expected_version:p.inventory.version,reason_code:reason.value};await s.write('/admin/inventory/adjustments',body,async()=>{await loadProducts();const updated=products.value.find(x=>x.id===p.id);if(updated)editProduct(updated);s.notice.value='库存调整已由服务器确认。';});})]):null,
 editing.value?card('阶梯促销',[h('p','减价基点：1000 表示减价 10%，即九折。金额由服务器按整数分计算。'),form([
 field('促销开始（上海时间）',promoStart.value,v=>promoStart.value=v,'datetime-local',{required:true}),field('促销结束（上海时间）',promoEnd.value,v=>promoEnd.value=v,'datetime-local',{required:true}),...promoTiers.value.map((tier,i)=>h('fieldset',[h('legend',`阶梯 ${i+1}`),field('起购数量',tier.min_qty,v=>tier.min_qty=Number(v),'number',{required:true,min:1,max:999,step:1}),field('减价基点',tier.discount_bps,v=>tier.discount_bps=Number(v),'number',{required:true,min:0,max:10000,step:1}),promoTiers.value.length>1?btn('移除阶梯',()=>promoTiers.value.splice(i,1)):null])),btn('增加阶梯',()=>promoTiers.value.push({min_qty:1,discount_bps:0})),h('div',{class:'actions'},[h('button',{type:'submit',class:'btn primary',disabled:isBusy(),'data-testid':'save-promotion'},'保存促销'),btn('取消促销',()=>savePromotion(null),isBusy(),'clear-promotion')])],()=>savePromotion({starts_at:toISO(promoStart.value),ends_at:toISO(promoEnd.value),tiers:promoTiers.value.map(t=>({...t}))}))]):null
 ])]);}
 async function savePromotion(promotion:Promotion|null){const p=editing.value!;const body:PromotionUpdate={expected_version:p.version,promotion};await s.write<AdminProduct>(`/admin/products/${id(p.id)}/promotion`,body,productSaved,'PUT');}
 function slotsView(){return h('div',{class:'split'},[card('服务窗口与容量',[field('窗口日期',day.value,v=>{day.value=v;void s.run(refresh);},'date'),...slots.value.map(w=>h('article',{class:'item'},[h('b',`${date(w.starts_at)} — ${date(w.ends_at)}`),h('p',`放单 ${date(w.dispatch_at)} · 已使用 ${w.used} / ${w.capacity} · v${w.version}`),field('容量',capacities.value[w.id],v=>capacities.value[w.id]=Number(v),'number',{min:1,max:9999,step:1}),check('接受新订单',accepting.value[w.id],v=>accepting.value[w.id]=v),btn('更新窗口',()=>{const body:SlotUpdate={expected_version:w.version,capacity:capacities.value[w.id],accepting:accepting.value[w.id]};return s.write<AdminSlot>(`/admin/slots/${id(w.id)}`,body,async()=>{await refresh();s.notice.value='服务窗口已更新。';},'PUT');},isBusy(),`save-slot-${w.id}`)])),!slots.value.length?empty('本日没有服务窗口'):null]),card('批量生成窗口',[h('p','按上海本地日期和营业时间生成，操作使用同一幂等键安全重试。'),form([
 field('营业日期',batchForm.value.local_date,v=>batchForm.value.local_date=v,'date',{required:true}),field('营业开始',batchForm.value.opens_at,v=>batchForm.value.opens_at=v,'time',{required:true}),field('营业结束',batchForm.value.closes_at,v=>batchForm.value.closes_at=v,'time',{required:true}),field('提前备货分钟数',batchForm.value.lead_minutes,v=>batchForm.value.lead_minutes=Number(v),'number',{required:true,min:0,step:1}),field('每窗口容量',batchForm.value.capacity,v=>batchForm.value.capacity=Number(v),'number',{required:true,min:1,max:9999,step:1}),h('button',{type:'submit',class:'btn primary',disabled:isBusy(),'data-testid':'create-slot-batch'},'生成服务窗口')],()=>s.write<AdminSlotList>('/admin/slots/batches',{...batchForm.value},async v=>{slots.value=v.items;day.value=batchForm.value.local_date;await refresh();s.notice.value='服务窗口批次已生成。';}))])]);}
 function provisionCard(){return card('开通骑手账号（安全管理员）',[h('p',{class:'muted'},'只在管理入口由近期 MFA 的安全管理员开户。必须填写员工 IdP 已核对的 issuer 与 subject；系统只授予 RIDER，新骑手初始不可接单，也没有自助注册入口。'),form([field('IdP issuer',provisionIssuer.value,v=>provisionIssuer.value=v,'text',{required:true,maxlength:255}),field('IdP subject',provisionSubject.value,v=>provisionSubject.value=v,'text',{required:true,maxlength:255}),field('骑手显示名',provisionName.value,v=>provisionName.value=v,'text',{required:true,maxlength:60}),field('开户原因码',reason.value,v=>reason.value=v,'text',{pattern:'[A-Z0-9_]{1,64}'}),h('button',{type:'submit',class:'btn primary',disabled:isBusy()||!provisionIssuer.value||!provisionSubject.value||!provisionName.value||!reason.value,'data-testid':'provision-rider'},'开通骑手账号')],()=>{const body:RiderProvision={issuer:provisionIssuer.value,subject:provisionSubject.value,display_name:provisionName.value,reason_code:reason.value};return s.write<Rider>('/admin/riders/provision',body,async r=>{await refresh();provisionSubject.value='';provisionName.value='';s.notice.value='骑手账号已开通（'+r.display_name+'）。仍需运营启用并通过员工登录与 MFA 才能履约。';});}),h('p',{class:'muted'},'停用会立即失效骑手会话；未绑定受信 issuer/subject 的账号不可启用。')],{'data-testid':'rider-provision'});}
 function riderView(){return h('div',{class:operator()&&security()?'split':undefined},[operator()?card('骑手管理',[h('p','启停需要近期 MFA。角色绑定由安全管理员负责，本页只管理已有骑手。'),field('骑手管理原因码',reason.value,v=>reason.value=v,'text',{pattern:'[A-Z0-9_]{1,64}'}),...riders.value.map(r=>h('article',{class:'item'},[h('h3',r.display_name),h('p',`${r.enabled?'已启用':'已停用'} · ${r.available?'在线':'离线'} · v${r.version}`),btn(r.enabled?'停用骑手':'启用骑手',()=>{const body:RiderUpdate={enabled:!r.enabled,expected_version:r.version,reason_code:reason.value};return s.write<Rider>(`/admin/riders/${id(r.id)}`,body,async()=>{await refresh();s.notice.value='骑手状态已更新。';},'PATCH');},isBusy()||!reason.value,`rider-${r.id}`)])),!riders.value.length?empty('暂无骑手记录'):null]):null,security()?provisionCard():null]);}
 function financeView(){return h('div',{class:'split'},[card('COD 实收与上缴制单',[field('收款日期',day.value,v=>{day.value=v;receiptSelection.value=[];void s.run(refresh);},'date'),h('p','请选择同一骑手、同一日期且尚未上缴的实收凭证。上缴制单与复点由不同财务账号执行，均要求近期 MFA。'),select('上缴骑手',batchRider.value,[['','请选择骑手'],...[...new Set(receipts.value.map(r=>r.rider_id))].map(r=>[r,r] as [string,string])],v=>{batchRider.value=v;receiptSelection.value=[];}),...receipts.value.map(r=>h('article',{class:'item'},[check(`凭证 ${r.id}`,receiptSelection.value.includes(r.id),v=>{if(v)receiptSelection.value.push(r.id);else receiptSelection.value=receiptSelection.value.filter(k=>k!==r.id);}),h('p',`订单 ${r.order_id} · 骑手 ${r.rider_id}`),h('p',`${money(r.amount_fen)} · ${date(r.collected_at)}`),badge(r.remittance_status),r.remittance_status!=='NOT_REMITTED'||r.rider_id!==batchRider.value?h('p',{class:'muted'},'此凭证不符合本次制单选择，请选择正确骑手及未上缴凭证。'):null])),!receipts.value.length?empty('本日暂无 COD 实收凭证'):null,h('p',{class:'price'},`所选实收 ${money(receipts.value.filter(r=>receiptSelection.value.includes(r.id)).reduce((a,r)=>a+r.amount_fen,0))}`),btn('创建上缴批次',()=>{const body:CODBatchInput={rider_id:batchRider.value,local_date:day.value,receipt_ids:[...receiptSelection.value]};return s.write<CODBatch>('/admin/cod/batches',body,async v=>{batch.value=v;batchId.value=v.id;makers.set(v.id,s.me.value!.id);receiptSelection.value=[];await refresh();s.notice.value='上缴批次已创建，请将批次号交独立财务复点。';});},isBusy()||!batchRider.value||!receiptSelection.value.length||receiptSelection.value.some(k=>{const r=receipts.value.find(r=>r.id===k);return !r||r.rider_id!==batchRider.value||r.remittance_status!=='NOT_REMITTED';}),'create-cod-batch','primary')]),
 card('独立财务复点',[h('p','复点只记录实际清点结果。制单人不能复点自己的批次，服务器执行最终身份校验。'),form([
 field('批次号',batchId.value,v=>batchId.value=v,'text',{required:true}),amountField('实际清点金额（块）',actualAmount.value,v=>actualAmount.value=v),field('复点原因码',reason.value,v=>reason.value=v,'text',{required:true,pattern:'[A-Z0-9_]{1,64}'}),h('button',{type:'submit',class:'btn primary',disabled:isBusy()||!batchId.value||makers.get(batchId.value)===s.me.value?.id,'data-testid':'verify-cod-batch'},'确认财务复点')],()=>{const actual_fen=parseMoney(actualAmount.value);if(actual_fen===undefined){s.error.value='请输入有效的清点金额，最多两位小数。';return;}const body:CODVerify={actual_fen,reason_code:reason.value};return s.write<CODBatch>(`/admin/cod/batches/${id(batchId.value)}/verify`,body,async v=>{batch.value=v;await refresh();s.notice.value='复点结果已由服务器记录。';});}),makers.get(batchId.value)===s.me.value?.id?h('p',{class:'error'},'当前账号为该批次制单人，请由独立财务账号复点。'):null,
 batch.value?card('服务器批次结果',[h('p',`批次 ${batch.value.id}`),h('p',`骑手 ${batch.value.rider_id} · 日期 ${batch.value.local_date}`),h('p',`应上缴 ${money(batch.value.expected_fen)}`),h('p',batch.value.actual_fen===null?'尚未复点':`实际清点 ${money(batch.value.actual_fen)}`),h('p',{'data-testid':'cod-batch-difference'},codDifference(batch.value)),badge(batch.value.status)],{'data-testid':'cod-batch-result'}):null,
 h('p',{class:'muted'},'冻结接口没有批次列表/详情查询。可输入已知批次号复点；未知结果需核对凭证状态，再使用同一请求重试。')])]);}
 function refundView(){return card('退款审批与 COD 实际兑付',[h('p','退款申请由运营发起，审批由财务完成；COD 实际兑付凭证单独登记，不将审批视为已付款。'),field('退款审批原因码',reason.value,v=>reason.value=v,'text',{pattern:'[A-Z0-9_]{1,64}'}),field('实际 COD 兑付凭证号',reference.value,v=>reference.value=v),...refunds.value.map(r=>h('article',{class:'item'},[h('h3',`退款 ${r.id}`),h('p',`订单 ${r.order_id} · ${date(r.requested_at)}`),h('p',{class:'price'},money(r.amount_fen)),h('p',r.payment_method==='COD'?'货到付款退款':'余额退款'),badge(r.status),finance()?h('div',{class:'actions'},[
 btn('批准退款',()=>{const body:Reason={reason_code:reason.value};return s.write<Refund>(`/admin/refunds/${id(r.id)}/approve`,body,async()=>{await refresh();s.notice.value='退款审批已由服务器记录；COD 实际兑付仍需单独登记。';});},isBusy()||r.status!=='REFUND_PENDING'||!reason.value,`approve-refund-${r.id}`,'primary'),
 btn('登记 COD 实际兑付',()=>{const body:PayoutInput={receipt_reference:reference.value};return s.write<Refund>(`/admin/refunds/${id(r.id)}/record-cod-payout`,body,async()=>{await refresh();reference.value='';s.notice.value='COD 实际兑付已由服务器登记。';});},isBusy()||r.payment_method!=='COD'||r.status!=='REFUND_PENDING'||!reference.value,`payout-refund-${r.id}`)
 ]):h('p',{class:'muted'},'当前运营账号可查看进度；审批和兑付登记需要财务权限。')])),!refunds.value.length?empty('暂无退款记录'):null]);}
 function showDeliveryFee(value: DeliveryFee) {
  deliveryFee.value = value;
  deliveryFeeAmount.value = moneyAmount(value.delivery_fee_fen);
  commissionPercent.value = String((value.cod_commission_bps ?? 1000) / 100);
 }
 function commissionInBPS() {
  const value = commissionPercent.value.trim();
  if (!/^\d{1,3}(?:\.\d{1,2})?$/.test(value)) return undefined;
  const bps = Math.round(Number(value)*100);
  return bps <= 10000 ? bps : undefined;
 }
 function feeSettingsChanged(fee:DeliveryFee, amount:number|undefined, bps:number|undefined){
  return amount!==undefined&&bps!==undefined&&(amount!==fee.delivery_fee_fen||bps!==(fee.cod_commission_bps??1000));
 }
 function saveDeliveryFee() {
  const fee = deliveryFee.value;
  const amount = parseMoney(deliveryFeeAmount.value);
  const bps = commissionInBPS();
  if (!fee || amount === undefined || bps === undefined || !feeSettingsChanged(fee, amount, bps) || isBusy()) return;
  const body: DeliveryFeeUpdate = {delivery_fee_fen: amount, expected_version: fee.version};
  if(bps !== (fee.cod_commission_bps ?? 1000)) body.cod_commission_bps = bps;
  return s.write<DeliveryFee>('/admin/delivery-fee', body, updated => {
   showDeliveryFee(updated);
   s.notice.value = '配送费和货到付款提成设置已保存。';
  }, 'PUT');
 }
 function deliveryFeeView() {
  const fee = deliveryFee.value;
  const amount = parseMoney(deliveryFeeAmount.value);
  const bps = commissionInBPS();
  return card('配送费与货到付款提成', [
   h('p', '新订单会自动收取此费用。填写 0 块可免配送费。骑手送达后，按该订单的配送费计入收入。'),
   fee ? form([
    h('p', {class: 'muted'}, `当前配送费 ${money(fee.delivery_fee_fen)} / 单`),
    amountField('每单配送费（块）', deliveryFeeAmount.value, v => deliveryFeeAmount.value = v, isBusy()),
    amount === undefined ? h('p', {class: 'error'}, '请输入有效金额，最多保留两位小数。') : null,
    field('货到付款提成（%）', commissionPercent.value, v => commissionPercent.value = v, 'number', {
     min: 0, max: 100, step: '0.01', required: true, disabled: isBusy(),
    }),
    bps === undefined ? h('p', {class: 'error'}, '请输入 0 到 100 之间的比例，最多保留两位小数。') : null,
    h('p', {class: 'muted'}, '新订单按优惠后商品实付金额计算提成，配送费另计。'),
    h('button', {class: 'btn primary', type: 'submit', disabled: isBusy() || !feeSettingsChanged(fee, amount, bps)}, '保存配送费和提成'),
   ], saveDeliveryFee) : empty('等待服务器返回配送费'),
  ]);
 }
 async function loadMerchantSettlements() {
  const actor = sessionKey(s.me.value);
  const requestedDay = day.value;
  const result = await s.list<MerchantSettlementList>(`/admin/cod/merchant-settlements?date=${requestedDay}&limit=100`);
  if(actor===sessionKey(s.me.value)&&requestedDay===day.value) merchantSettlements.value=result.items;
 }
 function merchantSettlementsView(){
  const status={WAITING_REMITTANCE:'待上缴复点对平',PAYABLE:'待打款',PAID:'已登记打款',REFUND:'退款处理中或已退款',NO_PAYOUT:'无应付金额'};
  return h('section',[
   field('收款日期',day.value,v=>{day.value=v;merchantSettlements.value=[];payoutReferences.value={};void s.run(refresh);},'date',{disabled:isBusy()}),
   ...merchantSettlements.value.map(m=>{
    const orderID=m.order_id;
    const receipt=payoutReferences.value[orderID]?.trim()??'';
    return h('article',{class:'item',key:orderID},[
     h('h3',`订单 ${orderID}`),
     h('p',`实收 ${money(m.collected_fen)} · 配送费 ${money(m.delivery_fee_fen)} · 骑手提成 ${money(m.commission_fen)}`),
     h('p',`商家应结 ${money(m.amount_fen)} · ${status[m.status]}`),
     m.receipt_reference?h('p',`打款凭据 ${m.receipt_reference}`):null,
     m.status==='PAYABLE'?form([
      field('实际打款凭据',payoutReferences.value[orderID]??'',v=>payoutReferences.value[orderID]=v,'text',{maxlength:128,required:true,disabled:isBusy()}),
      h('button',{type:'submit',class:'btn primary',disabled:isBusy()||!receipt},'登记商家打款'),
     ],()=>s.write<MerchantSettlement>(`/admin/cod/merchant-settlements/${id(orderID)}/record-payout`,{receipt_reference:receipt},updated=>{
      merchantSettlements.value=merchantSettlements.value.map(item=>item.order_id===orderID?updated:item);
      delete payoutReferences.value[orderID];
      s.notice.value='商家打款凭据已登记。';
     })):null,
    ]);
   }),
   !merchantSettlements.value.length?empty('本日暂无商家结算记录'):null,
  ]);
 }
 function dashboardView(){const d=dashboard.value;return h('div',[h('div',{class:'note'},[h('h2','今天的站点手账'),h('p','订单、送达和实际净收款均来自服务器。'),field('看板日期',day.value,v=>{day.value=v;void s.run(refresh);},'date')]),d?h('div',{class:'metrics'},[['订单',String(d.order_count)],['已送达',String(d.delivered_count)],['净实收',money(d.net_collected_fen)],['逾期',String(d.overdue_count)]].map(([label,value])=>h('div',{class:'metric'},[h('span',label),h('b',value)]))):empty('等待经营数据'),card('对账与异常处理',[h('p','通过服务器经营看板、COD 实收凭证、上缴复点和退款结果逐笔核对。净实收不等于商品销售预测。'),h('div',{class:'actions'},[operator()?btn('查看订单履约',()=>go('订单履约')):null,finance()?btn('查看 COD 凭证与复点',()=>go('COD 财务')):null,btn('查看退款进度',()=>go('退款')),btn('导出完整账本对账',()=>{},true)]),h('p',{class:'muted'},'冻结契约没有独立账本对账、差异明细及导出接口，完整账本对账暂不可用。')])]);}
 onMounted(()=>{
  document.addEventListener('visibilitychange',onOrderVisibilityChange);
  void s.recover();
 });
 onUnmounted(()=>{
  document.removeEventListener('visibilitychange',onOrderVisibilityChange);
  stopOrderRefresh();
 });
 return ()=>{const tabs=[...(business()?['经营看板']:[]),...(operator()?['订单履约','商品与库存','服务时段','配送费','接单设置']:[]),...(operator()||security()?['骑手管理']:[]),...(finance()?['COD 财务','商家结算']:[]),...(business()?['退款']:[])];return shell('admin','站点经营手账',h('div',{class:'actions'},tabs.map(t=>btn(t,()=>go(t),s.busy.value,undefined,tab.value===t?'active':''))),[
 testLogin(),!s.me.value?card('员工登录与权限',[h('p','请使用当前管理入口的员工登录。菜单由服务器 /me 的运营与财务角色决定；敏感动作需要近期 MFA。'),h('a',{href:'/api/v1/auth/staff/start'},'前往员工登录 / MFA')]):h('div',[
 h('div',{class:'actions'},[btn('刷新服务器数据',()=>s.run(refresh),s.busy.value,'refresh-admin')]),
 tab.value==='经营看板'&&business()?dashboardView():tab.value==='订单履约'&&operator()?ordersView():tab.value==='商品与库存'&&operator()?productsView():tab.value==='服务时段'&&operator()?slotsView():tab.value==='配送费'&&operator()?deliveryFeeView():tab.value==='接单设置'&&operator()?orderLimitsView():tab.value==='骑手管理'&&(operator()||security())?riderView():tab.value==='COD 财务'&&finance()?financeView():tab.value==='商家结算'&&finance()?merchantSettlementsView():tab.value==='退款'&&business()?refundView():empty('当前角色没有此页面权限')
 ])],s.tools(),s.status());};
}};
export function mount(root:HTMLElement){assertNoProductionMock(import.meta.env.PROD,import.meta.env.VITE_ENABLE_MOCK);return createApp(AdminApp).mount(root);}
if(document.querySelector<HTMLElement>('#app')?.dataset.entry==='admin')mount(document.querySelector<HTMLElement>('#app')!);
export default AdminApp;
