import { computed, createApp, defineComponent, h, nextTick, ref, onMounted, onUnmounted, watch, type VNodeChild } from 'vue';
import {
 ArrowRight, BrushCleaning, Cherry, CircleUserRound, CookingPot, GraduationCap, Grid2X2, House, IceCreamCone,
 MapPinHouse, MapPinned, Milk, Minus, PackageOpen, Plus, Popcorn, ReceiptText, RotateCw, Search, ShoppingBasket,
 Utensils, WalletCards,
} from '@lucide/vue';
import { apiRequest, ApiError, assertNoProductionMock } from '../shared/api.ts';
import { ProductPhoto } from '../shared/product-photo.ts';
import type { Address, AddressInput, AddressList, AddressUpdate, ServiceBuilding, ServiceBuildingList, Product, ProductList, Order, OrderList, OrderLineInput, Quote, QuoteInput, PaymentMethods, Slot, SlotList, Wallet, OTPChallenge, Me, OrderInput, EmailChallenge, EmailCodeRequest, EmailBindRequest } from '../shared/generated.ts';
import { session, localTestLogin, sessionKey, money, date, today, dayAfter, id, text, btn, field, select, card, empty, badge, stateText, form } from './ui.ts';
import './storefront.css';

const storage='xiaoling.cart';
const customerNavigation = [
 {label: '商品', title: '首页', icon: House},
 {label: '我的订单', title: '订单', icon: ReceiptText},
 {label: '我的账号', title: '我的', icon: CircleUserRound},
] as const;
const categoryRules = [
 {name: '冰淇淋', matches: /冰淇淋|雪糕|冰棍|冰激凌/},
 {name: '冰饮', matches: /饮料|饮品|可乐|雪碧|汽水|矿泉水|红茶|绿茶|奶茶|果汁|咖啡|酸奶|牛奶|椰汁|苏打|乌龙|茉莉|气泡水/},
 {name: '水果', matches: /果切|蜜柚|橙子|哈密瓜|西瓜|苹果|香蕉|草莓|葡萄|芒果|菠萝|柑橘|鲜果/},
 {name: '速食', matches: /方便面|泡面|拌面|米线|自热|火腿肠|烤肠|香肠|螺蛳粉/},
 {name: '零食', matches: /零食|饼干|薯片|糖|辣条|巧克力|坚果|肉脯|面包|蛋糕|海苔|果冻|爆米花|卤蛋/},
];
const drinks = {icon: Milk, tone: 'blue'};
const categoryIcons = new Map([
 ['', {icon: Grid2X2, tone: 'green'}],
 ['冰淇淋', {icon: IceCreamCone, tone: 'blue'}],
 ['冰饮', drinks],
 ['饮品', drinks],
 ['水果', {icon: Cherry, tone: 'rose'}],
 ['速食', {icon: CookingPot, tone: 'clay'}],
 ['零食', {icon: Popcorn, tone: 'amber'}],
 ['餐食', {icon: Utensils, tone: 'clay'}],
 ['日用', {icon: BrushCleaning, tone: 'plum'}],
]);
const otherCategoryIcon = {icon: PackageOpen, tone: 'neutral'};
function productCategory(product: Product) {
 const category = product.category?.trim();
 if (category && category !== 'uncategorized') return category === '饮料' ? '冰饮' : category;
 return categoryRules.find(rule => rule.matches.test(product.name))?.name ?? '其他';
}
function productPhoto(product: Product, large = false) {
 const {icon, tone} = categoryIcons.get(productCategory(product)) ?? otherCategoryIcon;
 return h(ProductPhoto, {url: product.image_url, name: product.name, large}, {
  fallback: () => h('span', {class: ['product-fallback', `icon-tone-${tone}`], 'aria-hidden': 'true'}, [
   h(icon, {size: large ? 48 : 30, strokeWidth: 1.7}),
  ]),
 });
}
const activeOrderStatuses = new Set<Order['status']>(['SCHEDULED','PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP','IN_DELIVERY','RETURNING']);

function storefront(
 navigation: VNodeChild,
 body: VNodeChild,
 location: VNodeChild,
 search: VNodeChild,
 checkoutBar: VNodeChild,
 status: VNodeChild,
 tools: VNodeChild,
) {
 return h('main', {class: ['app user', {'has-checkout-bar': !!checkoutBar}], 'data-entry': 'user'}, [
  h('header', {class: 'masthead'}, [
   h('div', {class: ['masthead-inner', {'with-search': !!search}]}, [
    h('div', {class: 'masthead-row'}, [
     h('div', {class: 'brand'}, [
      h('span', {class: 'logo', 'aria-hidden': 'true'}, [h(GraduationCap, {size: 23, strokeWidth: 1.85})]),
      h('h1', '校零智选'),
     ]),
     location,
    ]),
    search,
   ]),
  ]),
  h('nav', {class: 'tabs', 'aria-label': '主要功能'}, [navigation]),
  h('div', {class: 'content'}, [status, body]),
  h('footer', [tools]),
  checkoutBar,
 ]);
}
export function readCart(key=storage):OrderLineInput[]{try{const value:unknown=JSON.parse(localStorage.getItem(key)||'[]');return Array.isArray(value)?value.filter((x):x is OrderLineInput=>x&&typeof x.sku_id==='string'&&Number.isSafeInteger(x.qty)&&x.qty>0&&x.qty<=100).map(x=>({sku_id:x.sku_id,qty:x.qty})):[];}catch{return [];}}
export const UserApp = defineComponent({
 setup() {
 const tab=ref('商品'),products=ref<Product[]>([]),search=ref(''),product=ref<Product>(),cart=ref<OrderLineInput[]>(readCart()),addresses=ref<Address[]>([]),address=ref(''),slots=ref<Slot[]>([]),mode=ref<'ASAP'|'SCHEDULED'>('ASAP'),slot=ref(''),payment=ref<PaymentMethods>(),wallet=ref<Wallet>(),quote=ref<Quote>(),orders=ref<Order[]>([]),detail=ref<Order>(),note=ref(''),reason=ref('CUSTOMER_REQUEST');
 const balanceEnabled = computed(() => payment.value?.methods.some(option => option.code === 'BALANCE' && option.enabled) === true);
 const catalogLoaded = ref(false);
 const cartCount = computed(() => cart.value.reduce((sum, line) => sum + line.qty, 0));
 const deliveryAddress = computed(() => addresses.value.find(item => item.id === address.value));
 const orderFilter = ref<'all' | 'active'>('all');
 const quoteSummary = ref<HTMLElement>();
 const checkout = ref<HTMLElement>();
 const checkoutOpen = ref(false);
 watch(tab, () => window.scrollTo(0, 0));
 watch(quote, value => {
  if (!value || tab.value !== '商品' || !checkoutOpen.value || !quoteSummary.value) return;
  quoteSummary.value.focus({preventScroll: true});
  quoteSummary.value.scrollIntoView({block: 'end'});
 }, {flush: 'post'});
 const submittedSearch = ref('');
 const selectedCategory = ref('');
 const categorizedProducts = computed(() => products.value.map(item => ({item, category: productCategory(item)})));
 const categories = computed(() => {
  const present = new Set(categorizedProducts.value.map(({category}) => category));
  const standard = categoryRules.map(({name}) => name);
  return [...standard.filter(name => present.has(name)), ...[...present].filter(name => !standard.includes(name))];
 });
 watch(categories, value => {if (!value.includes(selectedCategory.value)) selectedCategory.value = '';});
 const visibleProducts = computed(() => {
  const query = submittedSearch.value.toLowerCase();
  return categorizedProducts.value.filter(({item, category}) =>
   (!selectedCategory.value || category === selectedCategory.value) &&
   (!query || item.name.toLowerCase().includes(query) || item.description.toLowerCase().includes(query)),
  ).map(({item}) => item);
 });
 const phone=ref(''),code=ref(''),challenge=ref<OTPChallenge>(),editing=ref<Address>();
 const authMode=ref<'EMAIL_LOGIN'|'EMAIL_REGISTER'|'PHONE_LOGIN'>('EMAIL_LOGIN');
 const email=ref(''),emailCode=ref(''),emailChallenge=ref<EmailChallenge>(),bindEmail=ref(''),bindCode=ref(''),bindChallenge=ref<EmailChallenge>();
 const emailNow=ref(Date.now()),cooldowns=new Map<string,number>();
 let resendTimer=0;
 const addressForm=ref<AddressInput>({building_id:'',room:'',contact_name:'',contact_phone:''});
 const buildings = ref<ServiceBuilding[]>([]);
 const buildingStatus = ref<'loading' | 'ready' | 'error'>('loading');
 const buildingRetry = ref(0);
 const reservationDate=ref(today());let catalogGeneration=0,quoteGeneration=0,slotGeneration=0;
 const s = session(['CUSTOMER'], refresh, error => {
  switch (error.code) {
   case 'MIXED_MERCHANTS':
    return '这单含有不同店铺的商品，请按店铺分开下单。';
   case 'QUOTE_CHANGED':
    return '本次报价已失效，请重新获取并核对金额';
   case 'SLOT_EXPIRED':
    if (quote.value) return '本次报价的配送时段已失效，请重新获取报价。';
    return mode.value === 'ASAP'
     ? '暂时没有可用的即时配送时段。可切换到预约配送查看开放时段。'
     : '所选配送时段已失效，请刷新后选择其他时段。';
   case 'SLOT_FULL':
    return '配送时段已满，请刷新后选择其他时段。';
   case 'OUT_OF_STOCK':
    return '所选商品库存不足，请减少数量或移除后再试。';
  }
 });
 const testLogin=localTestLogin('user',s);
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){addresses.value=[];address.value='';orders.value=[];detail.value=undefined;wallet.value=undefined;payment.value=undefined;invalidate();editing.value=undefined;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};bindEmail.value='';bindCode.value='';bindChallenge.value=undefined;}},{flush:'sync'});
 watch([tab, () => sessionKey(s.me.value), buildingRetry], ([active, actor], _, onCleanup) => {
  buildings.value = [];
  buildingStatus.value = 'loading';
  if (active !== '地址簿' || !actor) return;
  const request = new AbortController();
  onCleanup(() => request.abort());
  void loadBuildings(request.signal);
 }, {flush: 'sync'});

 async function loadBuildings(signal: AbortSignal) {
  try {
   const result = await s.list<ServiceBuildingList>('/me/service-buildings?limit=100', signal);
   if (signal.aborted) return;
   buildings.value = result.items;
   buildingStatus.value = 'ready';
  } catch {
   if (!signal.aborted) buildingStatus.value = 'error';
  }
 }

 function hasSelectedBuilding() {
  return buildings.value.some(building => building.id === addressForm.value.building_id);
 }

 function buildingField() {
  const status = buildingStatus.value;
  const placeholder = status === 'loading' ? '正在加载楼栋…' : status === 'error' ? '楼栋加载失败' : buildings.value.length ? '请选择楼栋' : '本站点暂无可配送楼栋';
  return h('div', [
   select('楼栋', hasSelectedBuilding() ? addressForm.value.building_id : '', [
    ['', placeholder, true],
    ...buildings.value.map((building): [string, string] => [building.id, building.name]),
   ], value => addressForm.value.building_id = value, {required: true, disabled: !buildings.value.length}),
   status === 'error' ? btn('重新加载楼栋', () => {s.error.value = ''; buildingRetry.value++;}) : null,
   status === 'ready' && editing.value && addressForm.value.building_id && !hasSelectedBuilding()
    ? h('p', {class: 'muted'}, '原楼栋已停止配送，请选择可配送的楼栋。') : null,
  ]);
 }
 async function catalog() {
  const generation = ++catalogGeneration;
  const result = await s.list<ProductList>('/catalog/products?limit=100');
  if (generation !== catalogGeneration) return;
  products.value = result.items;
  catalogLoaded.value = true;
 }
 async function refresh(){await catalog();if(!s.me.value)return;await Promise.all([
  s.list<AddressList>('/me/addresses?limit=100').then(v=>{addresses.value=v.items;if(!address.value&&v.items.length)address.value=v.items[0].id;}),
  loadSlots(),
  s.get<PaymentMethods>('/payment-methods').then(v=>payment.value=v),
  s.list<OrderList>('/orders?limit=100').then(v=>orders.value=v.items),
  s.get<Wallet>('/me/wallet').then(v=>wallet.value=v),
  detail.value?s.get<Order>(`/orders/${id(detail.value.id)}`).then(v=>detail.value=v):Promise.resolve()
 ]);}
 async function loadSlots(){const token=++slotGeneration;const requested=reservationDate.value;const result=await s.list<SlotList>(`/delivery/slots?date=${requested}&limit=100`);if(token===slotGeneration&&requested===reservationDate.value)slots.value=result.items;}
 function chooseDate(value:string){reservationDate.value=value;slot.value='';slots.value=[];invalidate();if(value<today()||value>dayAfter(3)){s.error.value='预约日期须在今天至三天后之间；可选窗口由服务器决定。';return;}void s.run(loadSlots);}
 function invalidate(){quoteGeneration++;quote.value=undefined;}
 async function openCheckout() {
  tab.value = '商品';
  checkoutOpen.value = true;
  await nextTick();
  checkout.value?.focus({preventScroll: true});
  checkout.value?.scrollIntoView({block: 'start'});
 }
 function save(){try{localStorage.setItem(storage,JSON.stringify(cart.value.map(({sku_id,qty})=>({sku_id,qty}))));}catch{s.notice.value='浏览器未允许保存所选商品，当前选择仍可使用。';}invalidate();}
 function add(sku_id:string){const existing=cart.value.find(l=>l.sku_id===sku_id);if(existing){if(existing.qty>=100)return;existing.qty++;}else cart.value.push({sku_id,qty:1});save();}
 function changeQty(sku_id:string,qty:number){cart.value=cart.value.map(l=>l.sku_id===sku_id?{sku_id,qty}:l).filter(l=>l.qty>0);save();}
 const quantities = computed(() => new Map(cart.value.map(line => [line.sku_id, line.qty])));
 const catalogSKUs = computed(() => new Map(products.value.flatMap(product =>
  product.skus.map(sku => [sku.id, {name: product.name, sku}] as const)
 )));
 const hasUnavailableItems = computed(() => cart.value.some(line => !catalogSKUs.value.has(line.sku_id)));
 const estimatedSubtotal = computed(() => hasUnavailableItems.value ? undefined : cart.value.reduce(
  (sum, line) => sum + line.qty * (catalogSKUs.value.get(line.sku_id)?.sku.unit_price_fen ?? 0), 0
 ));
 watch(balanceEnabled, enabled => {if (!enabled) invalidate();});
 async function getQuote(){if(!balanceEnabled.value||!catalogLoaded.value||hasUnavailableItems.value)return;invalidate();const token=quoteGeneration;const input:QuoteInput={lines:cart.value.map(l=>({...l})),address_id:address.value,delivery_mode:mode.value,payment_method:'BALANCE',...(mode.value==='SCHEDULED'?{slot_id:slot.value}:{})};await s.write<Quote>('/checkout/quote',input,v=>{if(token!==quoteGeneration){s.notice.value='配送信息或所选商品已改变，旧报价已失效，请重新获取报价。';return;}if(v.payment_method!=='BALANCE'){s.error.value='报价的支付方式与余额支付不一致，请重新获取报价。';return;}quote.value=v;s.notice.value='报价已更新，请核对金额并再次确认下单。';});}
 async function confirm() {
  const currentQuote = quote.value;
  if (!currentQuote || !balanceEnabled.value || currentQuote.payment_method !== 'BALANCE') return;
  if (Date.parse(currentQuote.valid_until) <= Date.now()) {
   invalidate();
   s.error.value = '报价已过期，请刷新报价后重新确认。';
   return;
  }
  const input: OrderInput = {quote_id: currentQuote.quote_id, ...(note.value ? {note: note.value} : {})};
  await s.write<Order>('/orders', input, async order => {
   detail.value = order;
   tab.value = '我的订单';
   cart.value = [];
   checkoutOpen.value = false;
   save();
   await refresh();
   s.notice.value = '服务器已确认订单。';
  });
  if (!s.error.value || s.pending.value) return;
  invalidate();
  s.error.value += '。请刷新信息和报价后重新确认，支付方式与所选时段已保留。';
 }
 async function rebuy(o:Order){await s.run(async()=>{await catalog();if(reservationDate.value<today()||reservationDate.value>dayAfter(3))reservationDate.value=today();await loadSlots();cart.value=o.lines.map(l=>({sku_id:l.sku_id,qty:l.qty}));save();slot.value='';await openCheckout();s.notice.value='已重新获取当前商品和服务窗口，请核对规格、库存、支付方式和配送时段，再请求新报价。';});}
 function showOrder(o:Order){void s.run(async()=>{detail.value=await s.get<Order>(`/orders/${id(o.id)}`);tab.value='我的订单';window.scrollTo(0,0);});}
 function priceBreakdown(amount: Pick<Order, 'lines' | 'subtotal_fen' | 'delivery_fee_fen' | 'total_fen'>) {
  const subtotal = amount.subtotal_fen ?? amount.lines.reduce((sum, line) => sum + line.line_total_fen, 0);
  const amounts = [
   ['商品小计', subtotal],
   ['配送费', amount.delivery_fee_fen ?? amount.total_fen - subtotal],
   ['合计', amount.total_fen],
  ] as const;
  return h('div', {class: 'receipt-amounts'}, amounts.map(([label, amountFen]) => h('p', {class: ['receipt-row', {'receipt-total': label === '合计'}]}, [
   h('span', `${label} `), h('strong', money(amountFen)),
  ])));
 }
 function orderCard(o:Order,full=false){
  if (!full) {
   return h('article', {class: 'card order-card', key: o.id}, [
    h('div', {class: 'order-heading'}, [
     h('span', {class: 'order-icon', 'aria-hidden': 'true'}, [h(ReceiptText, {size: 20, strokeWidth: 1.8})]),
     h('div', [h('h3', '校零智选'), h('span', {class: 'muted'}, date(o.created_at))]),
     h('span', {class: ['order-status', {current: activeOrderStatuses.has(o.status)}]}, stateText(o.status)),
    ]),
    h('ul', {class: 'order-preview'}, o.lines.slice(0, 3).map(line => h('li', [
     h('span', `${line.name} · ${line.spec}`), h('span', {class: 'muted'}, `× ${line.qty}`),
    ]))),
    h('div', {class: 'order-total'}, [
     h('span', {class: 'muted'}, `共 ${o.lines.reduce((sum, line) => sum + line.qty, 0)} 件`),
     h('strong', {class: 'price'}, money(o.total_fen)),
    ]),
    h('div', {class: 'actions order-actions'}, [
     btn('查看详情', () => showOrder(o), s.busy.value, `order-${o.id}`, activeOrderStatuses.has(o.status) ? 'primary' : ''),
     btn('再买一次', () => rebuy(o), s.busy.value, undefined, o.status === 'DELIVERED' ? 'primary' : ''),
    ]),
   ]);
  }
  return card('订单详情',[h('p',{class:'muted'},`${date(o.created_at)} · ${o.delivery_mode==='ASAP'?'即时配送':'预约配送'}`),h('p',{class:'muted'},`订单号 ${o.id}`),h('div',[badge(o.status),badge(o.stock_status),badge(o.settlement_status)]),priceBreakdown(o),h('p',o.payment_method==='COD'&&o.settlement_status==='COD_UNCOLLECTED'?'送达后付款':o.payment_method==='BALANCE'&&o.settlement_status==='BALANCE_PAID'?'余额已扣':stateText(o.settlement_status)),h('p',`配送窗口 ${date(o.slot.starts_at)} — ${date(o.slot.ends_at)}`),...o.lines.map(l=>h('p',{class:'item'},`${l.name} · ${l.spec} × ${l.qty} · ${money(l.line_total_fen)}`)),h('ol',{class:'timeline','aria-label':'订单时间轴'},[
 h('li',`已下单 · ${date(o.created_at)}`),h('li',{class:'current'},`当前履约：${stateText(o.status)}`),h('li',`备货 / 库存：${stateText(o.stock_status)}`),h('li',`资金：${stateText(o.settlement_status)}`)]),
 field('操作原因码',reason.value,v=>reason.value=v),h('div',{class:'actions'},[btn('返回订单列表',()=>detail.value=undefined),btn('再买一次',()=>rebuy(o),s.busy.value),btn('取消订单',()=>s.write<Order>(`/orders/${id(o.id)}/cancel`,{reason_code:reason.value},async v=>{detail.value=v;await refresh();}),s.busy.value||!['SCHEDULED','PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP'].includes(o.status),'cancel-order'),btn('催单',()=>s.write(`/orders/${id(o.id)}/urge`,undefined,()=>s.notice.value='服务器已接受催单。'),s.busy.value||['CANCELED','DELIVERED','RETURNED'].includes(o.status),'urge-order')])],{'data-testid':'order-detail'});
 }
 function authTabs(){return h('div',{class:'actions','data-testid':'auth-modes'},[['EMAIL_LOGIN','邮箱登录'],['EMAIL_REGISTER','邮箱注册'],['PHONE_LOGIN','手机号登录']].map(([value,label])=>btn(label,()=>{if(authMode.value===value)return;authMode.value=value as typeof authMode.value;emailChallenge.value=undefined;emailCode.value='';challenge.value=undefined;code.value='';s.error.value='';s.notice.value='';},s.busy.value,'auth-'+value,authMode.value===value?'active':'')));}
 function phoneLogin(){return card('手机号登录',[h('p',{class:'muted'},'沿用原手机号验证码入口。若服务尚未接入，页面会显示真实拒绝原因。'),form([field('手机号',phone.value,v=>phone.value=v,'tel',{autocomplete:'tel',required:true,'data-sensitive':true}),h('button',{type:'submit',class:'btn',disabled:s.busy.value},'获取验证码')],()=>s.run(async()=>{challenge.value=await apiRequest<OTPChallenge>('/auth/otp/request',{method:'POST',body:{phone:phone.value}});s.notice.value='验证码请求已发送，请输入收到的验证码。';})),challenge.value?form([field('验证码',code.value,v=>code.value=v,'text',{inputmode:'numeric',autocomplete:'one-time-code',required:true}),h('button',{type:'submit',class:'btn primary',disabled:s.busy.value},'验证并登录')],()=>s.run(async()=>{await apiRequest<Me>('/auth/otp/verify',{method:'POST',body:{challenge_id:challenge.value!.challenge_id,code:code.value}});phone.value='';code.value='';challenge.value=undefined;await s.recover();})):null],{'data-testid':'phone-login'});}
 const cooldownKey=(purpose:string,address:string)=>`${purpose}:${address.trim().toLowerCase()}`;
 const cooldown=(key:string)=>Math.max(0,Math.ceil(((cooldowns.get(key)??0)-emailNow.value)/1000));
 function startCooldown(key:string,result:EmailChallenge){emailNow.value=Date.now();cooldowns.set(key,emailNow.value+result.resend_after_seconds*1000);}
 const expired=(challenge:EmailChallenge)=>Date.parse(challenge.expires_at)<=emailNow.value;
 const digits=(value:string)=>/^[0-9]{6}$/.test(value);
 const codeProps={inputmode:'numeric',autocomplete:'one-time-code',required:true,maxlength:6,pattern:'[0-9]{6}'};
 function clearEmailFeedback(){s.error.value='';s.notice.value='';}
 function emailLogin(){
  const purpose=authMode.value==='EMAIL_REGISTER'?'REGISTER' as const:'LOGIN' as const;
  const key=cooldownKey(purpose,email.value),left=cooldown(key),current=emailChallenge.value;
  const available=current&&!expired(current);
  return card(purpose==='REGISTER'?'邮箱注册':'邮箱登录',[
   form([field('邮箱地址',email.value,v=>{email.value=v;emailChallenge.value=undefined;emailCode.value='';clearEmailFeedback();},'email',{autocomplete:'email',required:true,readonly:s.busy.value,'data-sensitive':true}),
    h('button',{type:'submit',class:'btn',disabled:s.busy.value||left>0,'data-testid':'request-email-code'},left>0?`${left} 秒后可重发`:current?'重发验证码':'获取验证码')],()=>s.run(async()=>{
     if(cooldown(key)>0)return;
     const body:EmailCodeRequest={email:email.value,purpose};
     emailChallenge.value=await apiRequest<EmailChallenge>('/auth/email/challenges',{method:'POST',body});emailCode.value='';startCooldown(key,emailChallenge.value);
   })),
   current?h('p',{role:'status',class:'muted'},available?'验证码请求已受理。若该邮箱符合条件，请查看收件箱。':'验证码已过期，请重新获取。'):null,
   available?form([field('邮箱验证码',emailCode.value,v=>emailCode.value=v,'text',{...codeProps,'data-testid':'email-code'}),
    h('button',{type:'submit',class:'btn primary',disabled:s.busy.value||!digits(emailCode.value),'data-testid':'verify-email-code'},purpose==='REGISTER'?'验证并创建账号':'验证并登录')],()=>s.run(async()=>{
     if(Date.parse(current.expires_at)<=Date.now()||!digits(emailCode.value))return;
     try{await apiRequest<Me>('/auth/email/challenges/'+id(current.challenge_id)+'/verify',{method:'POST',body:{code:emailCode.value}});}
     catch(e){if(e instanceof ApiError&&e.status===401){s.error.value='验证码无效、已过期或错误次数已达上限。请检查验证码或重新获取。';return;}if(e instanceof ApiError&&e.status===403&&purpose==='REGISTER'){s.error.value='当前未开放邮箱注册。已有账号可以切换到登录。';return;}throw e;}
     email.value='';emailCode.value='';emailChallenge.value=undefined;await s.recover();
     if(s.me.value&&!s.error.value)s.notice.value='邮箱验证通过，已登录。';
     else if(!s.error.value)s.error.value='验证码已验证，但未能恢复会话，请重新登录。';
   })):null
  ],{'data-testid':'email-login'});
 }
 function login(){return h('div',[authTabs(),authMode.value==='PHONE_LOGIN'?phoneLogin():emailLogin()]);}
 function accountView(){const me=s.me.value;if(!me)return empty('请登录后查看账号');
  const key=cooldownKey('BIND',bindEmail.value),left=cooldown(key),current=bindChallenge.value,available=current&&!expired(current);
  return h('div',{class:'split'},[card('我的账号',[h('p',{class:'muted'},'站点 '+me.station_id+' · 角色 '+me.roles.join(' / ')),h('p',{'data-testid':'bound-email'},me.email?'已验证邮箱 '+me.email:'尚未绑定邮箱')]),me.email?null:card('绑定邮箱',[
   form([field('要绑定的邮箱',bindEmail.value,v=>{bindEmail.value=v;bindChallenge.value=undefined;bindCode.value='';clearEmailFeedback();},'email',{autocomplete:'email',required:true,readonly:s.busy.value||!!s.pending.value,'data-sensitive':true}),h('button',{type:'submit',class:'btn',disabled:s.busy.value||!!s.pending.value||left>0,'data-testid':'request-bind-code'},left>0?`${left} 秒后可重发`:current?'重发验证码':'获取绑定验证码')],()=>{
    if(cooldown(key)>0)return;
    const body:EmailBindRequest={email:bindEmail.value};return s.write<EmailChallenge>('/me/email/challenges',body,c=>{bindChallenge.value=c;bindCode.value='';startCooldown(key,c);});
   }),
   current?h('p',{role:'status',class:'muted'},available?'验证码请求已受理。若该邮箱可绑定，请查看收件箱。':'验证码已过期，请重新获取。'):null,
   available?form([field('邮箱验证码',bindCode.value,v=>bindCode.value=v,'text',{...codeProps,'data-testid':'bind-code'}),h('button',{type:'submit',class:'btn primary',disabled:s.busy.value||!digits(bindCode.value),'data-testid':'verify-bind-code'},'确认绑定')],()=>{
    if(Date.parse(current.expires_at)<=Date.now()||!digits(bindCode.value))return;
    return s.write<Me>('/me/email/challenges/'+id(current.challenge_id)+'/verify',{code:bindCode.value},v=>{s.me.value=v;bindChallenge.value=undefined;bindCode.value='';bindEmail.value='';s.notice.value='邮箱已绑定到当前账号。';});
   }):null
  ])]);}
 function addressView(){return h('div',{class:'split'},[card('我的地址',[...addresses.value.map(a=>h('article',{class:'item','data-sensitive':true},[h('b',`${a.building_name} ${a.room}`),h('p',`${a.contact_name} · ${a.contact_phone}`),h('div',{class:'actions'},[btn('编辑地址',()=>{editing.value=a;addressForm.value={building_id:a.building_id,room:a.room,contact_name:a.contact_name,contact_phone:a.contact_phone};},s.busy.value,`edit-address-${a.id}`),btn('删除地址',()=>deleteAddress(a),s.busy.value,`delete-address-${a.id}`)])])),!addresses.value.length?empty('请添加配送地址'):null]),card(editing.value?'编辑地址':'新增地址',[form([
 buildingField(),field('房间号',addressForm.value.room,v=>addressForm.value.room=v,'text',{required:true,'data-sensitive':true}),field('联系人',addressForm.value.contact_name,v=>addressForm.value.contact_name=v,'text',{required:true,'data-sensitive':true}),field('联系电话',addressForm.value.contact_phone,v=>addressForm.value.contact_phone=v,'tel',{required:true,pattern:'(?:1[0-9]{10}|\\+[1-9][0-9]{6,14})',title:'请输入 11 位手机号，或带国家区号的电话号码','data-sensitive':true}),h('div',{class:'actions'},[h('button',{type:'submit',class:'btn primary',disabled:s.busy.value||!hasSelectedBuilding()},'保存地址'),editing.value?btn('放弃编辑',()=>{editing.value=undefined;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};}):null])
 ],async()=>{if(!hasSelectedBuilding())return;if(editing.value){const input:AddressUpdate={...addressForm.value,expected_version:editing.value.version};await s.write<Address>(`/me/addresses/${id(editing.value.id)}`,input,addressSaved,'PATCH');}else await s.write<Address>('/me/addresses',{...addressForm.value},addressSaved);})])]);}
 async function addressSaved(a:Address){editing.value=undefined;address.value=a.id;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};invalidate();await refresh();s.notice.value='地址已由服务器保存。';}
 async function deleteAddress(a:Address){await s.run(async()=>{await apiRequest(`/me/addresses/${id(a.id)}`,{method:'DELETE',csrf:s.me.value?.csrf_token,fetcher:(url,options)=>{const headers=new Headers(options?.headers);headers.set('If-Match',`"${a.version}"`);return fetch(url,{...options,headers});}});if(address.value===a.id)address.value='';invalidate();await refresh();s.notice.value='地址已删除。';});}
 function catalogSearch() {
  return h('div', {class: 'catalog-search'}, [form([
   h(Search, {size: 20, strokeWidth: 2.2, 'aria-hidden': 'true'}),
   h('input', {type: 'search', value: search.value, 'aria-label': '搜索商品', placeholder: '搜索商品', maxlength: 80,
    onInput: (event: Event) => {search.value = text(event); if (!search.value) submittedSearch.value = '';}}),
   h('button', {class: 'btn primary', type: 'submit', disabled: s.busy.value}, '搜索'),
  ], () => s.run(async () => {
   const query = search.value.trim();
   await catalog();
   selectedCategory.value = '';
   submittedSearch.value = query;
   product.value = undefined;
  }))]);
 }
 function openProduct(item: Product) {
  return s.run(async () => {
   product.value = await s.get<Product>(`/catalog/products/${id(item.id)}`);
   window.scrollTo(0, 0);
  });
 }
 function catalogView() {
  const selected = product.value;
  return h('section', {class: 'catalog-view'}, [
   h('nav', {class: 'category-nav', 'aria-label': '商品分类'}, ['', ...categories.value].map(name => {
    const {icon, tone} = categoryIcons.get(name) ?? otherCategoryIcon;
    return h('button', {
     key: name, class: 'category-button', type: 'button', 'aria-pressed': name === selectedCategory.value,
     onClick: () => {selectedCategory.value = name; product.value = undefined;},
    }, [
     h('span', {class: ['category-symbol', `icon-tone-${tone}`], 'aria-hidden': 'true'}, [h(icon, {size: 18, strokeWidth: 1.85})]),
     name || '全部',
    ]);
   })),
   h('div', {class: 'catalog-items'}, [
   h('div', {class: 'section-heading catalog-heading'}, [
    h('div', [
     h('h2', submittedSearch.value ? '搜索结果' : selectedCategory.value || '全部商品'),
     catalogLoaded.value ? h('span', {class: 'muted'}, `${visibleProducts.value.length} 款商品`) : null,
    ]),
    h('button', {type: 'button', class: 'btn icon-button', title: '刷新商品', 'aria-label': '刷新商品',
     disabled: s.busy.value, onClick: () => s.run(catalog)}, [h(RotateCw, {size: 18, strokeWidth: 2, 'aria-hidden': 'true'})]),
   ]),
   selected ? card(selected.name, [
    productPhoto(selected, true),
    h('p', selected.description),
    ...selected.skus.map(sku => h('p', `${sku.label} · 每包 ${sku.units_per_pack} 件 · ${money(sku.unit_price_fen)}`)),
    btn('关闭详情', () => product.value = undefined),
   ], {class: 'card detail'}) : null,
   visibleProducts.value.length ? h('div', {class: 'catalog-list'}, visibleProducts.value.map(p =>
    h('article', {class: 'product-card', key: p.id}, [
     h('button', {type: 'button', class: 'product-media', 'aria-label': `商品详情：${p.name}`, disabled: s.busy.value,
     onClick: () => openProduct(p)}, [productPhoto(p)]),
     h('div', {class: 'product-body'}, [
      h('h3', p.name),
      p.description ? h('p', {class: 'product-description muted'}, p.description) : null,
      ...p.skus.map(sku => {
       const qty = quantities.value.get(sku.id) ?? 0;
       const label = `${p.name}（${sku.label}）`;
       return h('div', {class: 'sku-row', key: sku.id}, [
        h('div', {class: 'sku-price'}, [h('span', {class: 'sku-label'}, sku.label), h('strong', {class: 'price'}, money(sku.unit_price_fen))]),
        h('div', {class: 'catalog-quantity'}, [
         qty ? h('button', {type: 'button', class: 'step-button', 'data-testid': `subtract-${sku.id}`,
          title: `减少 ${label}`, 'aria-label': `减少 ${label}`, onClick: () => changeQty(sku.id, qty - 1),
         }, [h(Minus, {size: 18, strokeWidth: 2.4, 'aria-hidden': 'true'})]) : null,
         qty ? h('span', {class: 'selected-quantity'}, String(qty)) : null,
         h('button', {type: 'button', class: 'step-button add-button', 'data-testid': `add-${sku.id}`,
          title: `增加 ${label}`, 'aria-label': `增加 ${label}`, disabled: qty >= 100, onClick: () => add(sku.id),
         }, [h(Plus, {size: 18, strokeWidth: 2.4, 'aria-hidden': 'true'})]),
        ]),
       ]);
      }),
     ]),
    ])
   )) : empty(catalogLoaded.value ? '没有找到商品' : s.error.value ? '商品暂时无法加载' : '正在加载商品…'),
   ]),
  ]);
 }
 function checkoutBar() {
  const subtotal = estimatedSubtotal.value;
  return h('div', {class: 'checkout-bar'}, [
   h(ShoppingBasket, {size: 22, strokeWidth: 1.9, 'aria-hidden': 'true'}),
   h('div', {class: 'checkout-totals', 'aria-live': 'polite'}, [
    h('strong', cartCount.value ? `已选 ${cartCount.value} 件${subtotal === undefined ? '' : ` · ${money(subtotal)}`}` : '未选商品'),
    h('span', '配送费以报价为准'),
   ]),
   h('button', {type: 'button', class: 'checkout-cta', 'data-testid': 'start-checkout',
    'aria-expanded': checkoutOpen.value, 'aria-controls': checkoutOpen.value ? 'inline-checkout' : undefined,
    disabled: !cartCount.value, onClick: openCheckout,
   }, '结算'),
  ]);
 }
 function quoteView(current: Quote) {
  const window = slots.value.find(item => item.id === current.slot_id);
  return card('请核对本次报价', [
   h('ul', {class: 'receipt-lines'}, current.lines.map(line => h('li', {class: 'receipt-row', key: line.sku_id}, [
    h('div', [h('strong', line.name), h('span', {class: 'muted'}, `${line.spec} × ${line.qty}`)]),
    h('span', money(line.line_total_fen)),
   ]))),
   priceBreakdown(current),
   h('div', {class: 'receipt-meta'}, [
    h('p', `报价有效至 ${date(current.valid_until)}`),
    h('p', `服务窗口 ${window ? date(window.starts_at) : current.slot_id}`),
    h('p', '余额付款以服务器扣款结果为准'),
   ]),
   btn('确认下单', confirm, !balanceEnabled.value || s.busy.value || !!s.pending.value, 'confirm-order', 'primary'),
  ], {class: 'quote-summary', ref: quoteSummary, tabindex: -1, 'aria-label': '本次报价'});
 }
 function closeCheckout() {
  checkoutOpen.value = false;
  document.querySelector<HTMLButtonElement>('[data-testid="start-checkout"]')?.focus({preventScroll: true});
  window.scrollTo(0, 0);
 }
 function checkoutView(){return h('section',{
  id:'inline-checkout', class:'checkout-layout', ref:checkout, tabindex:-1, 'aria-label':'配送与支付',
 },[
 h('div',{class:'section-heading'},[h('h2','配送与支付'),btn('继续选购',closeCheckout)]),
 h('ul',{class:'checkout-lines','aria-label':'本单商品'},cart.value.map(line=>{
  const item=catalogSKUs.value.get(line.sku_id);
  return h('li',{class:'checkout-line',key:line.sku_id},[
   h('span',item?`${item.name} · ${item.sku.label}`:catalogLoaded.value?'商品规格已失效':'正在核对商品'),
   h('span',{class:'muted'},`× ${line.qty}`),
   btn('移除',()=>changeQty(line.sku_id,0),!!s.pending.value,undefined,'quiet'),
  ]);
 })),
 !cart.value.length ? empty('请先选择商品') : null,
 select('配送地址',address.value,[['','请选择地址'],...addresses.value.map(a=>[a.id,`${a.building_name} ${a.room}`] as [string,string])],v=>{address.value=v;invalidate();}),btn('管理地址',()=>tab.value='地址簿'),
 select('配送方式',mode.value,[['ASAP','即时配送'],['SCHEDULED','预约配送']],v=>{mode.value=v as 'ASAP'|'SCHEDULED';invalidate();}),
 mode.value==='SCHEDULED'?h('div',[field('预约日期',reservationDate.value,chooseDate,'date',{min:today(),max:dayAfter(3),'aria-label':'预约日期'}),select('服务窗口',slot.value,[['','请选择窗口'],...slots.value.map(w=>[w.id,`${date(w.starts_at)} — ${date(w.ends_at)}${!w.accepting?'（停止接单）':''}`,!w.accepting] as [string,string,boolean])],v=>{slot.value=v;invalidate();})]):h('p',{class:'muted'},'即时配送窗口由服务器报价确定。'),
 h('fieldset', [h('legend', '支付方式'),
  h('label', {class: 'check'}, [h('input', {type: 'radio', name: 'payment', value: 'BALANCE', checked: balanceEnabled.value, disabled: !balanceEnabled.value, 'aria-label': '余额'}), `余额支付${wallet.value ? `（${money(wallet.value.balance_fen)}）` : ''}`]),
  h('label', {class: 'check'}, [h('input', {type: 'radio', name: 'payment', disabled: true, 'aria-label': '微信待接入'}), '微信待接入']),
  !balanceEnabled.value ? h('p', {class: 'payment-hint'}, payment.value ? '本站暂未开放余额支付，当前无法下单。' : s.me.value ? '暂未获取支付方式，请刷新配送与支付信息。' : '请登录后查看可用支付方式。') : null,
 ]),
 field('订单备注',note.value,v=>{note.value=v;invalidate();},'text',{maxlength:200,'data-sensitive':true}),h('div',{class:'actions'},[btn('获取报价',getQuote,s.busy.value||!s.me.value||!balanceEnabled.value||!catalogLoaded.value||hasUnavailableItems.value||!cart.value.length||!address.value||(mode.value==='SCHEDULED'&&!slot.value)||!!s.pending.value,'quote', 'primary'),btn('刷新配送与支付信息',()=>s.run(async()=>{invalidate();await refresh();s.notice.value='信息已刷新，请重新报价。';}),s.busy.value)]),
 quote.value ? quoteView(quote.value) : null
 ]);}
 onMounted(async()=>{resendTimer=window.setInterval(()=>{emailNow.value=Date.now();for(const [key,until] of cooldowns)if(until<=emailNow.value)cooldowns.delete(key);},1000);await s.recover();if(!s.me.value)try{await catalog();}catch{}});
 onUnmounted(()=>{window.clearInterval(resendTimer);});
 function ordersView() {
  const list = orders.value.filter(o => orderFilter.value === 'all' || activeOrderStatuses.has(o.status));
  return h('section', [
   h('div', {class: 'section-heading'}, [h('h2', '我的订单'), btn('刷新订单', () => s.run(refresh), s.busy.value)]),
   h('div', {class: 'segmented', 'aria-label': '订单状态'}, ([
    ['all', '全部订单'], ['active', '进行中'],
   ] as const).map(([filter, label]) => h('button', {type: 'button', 'aria-pressed': orderFilter.value === filter,
    onClick: () => {orderFilter.value = filter; detail.value = undefined;}}, label))),
   detail.value ? orderCard(detail.value, true) : null,
   list.length ? h('div', {class: 'order-grid'}, list.filter(o => o.id !== detail.value?.id).map(o => orderCard(o)))
    : empty(!s.me.value ? '登录后查看订单' : orderFilter.value === 'active' ? '暂无进行中的订单' : '还没有订单'),
  ]);
 }
 function profileView() {
  return h('section', [
   h('div', {class: 'section-heading'}, [h('h2', '我的')]),
   h('div', {class: 'profile-overview'}, [
    wallet.value ? h('div', {class: 'profile-wallet'}, [
     h(WalletCards, {size: 22, strokeWidth: 1.8, 'aria-hidden': 'true'}), h('span', '账户余额'), h('strong', money(wallet.value.balance_fen)),
    ]) : null,
    h('button', {type: 'button', class: 'profile-link', onClick: () => tab.value = '地址簿'}, [
     h(MapPinned, {size: 20, strokeWidth: 1.8, 'aria-hidden': 'true'}), h('span', '我的地址'), h(ArrowRight, {size: 16, 'aria-hidden': 'true'}),
    ]),
    h('button', {type: 'button', class: 'profile-link', onClick: () => tab.value = '我的订单'}, [
     h(ReceiptText, {size: 20, strokeWidth: 1.8, 'aria-hidden': 'true'}), h('span', '查看订单'), h(ArrowRight, {size: 16, 'aria-hidden': 'true'}),
    ]),
   ]),
   accountView(),
  ]);
 }
 function navigation() {
  return customerNavigation.map(({label, title, icon}) => h('button', {
   key: label, type: 'button', class: ['nav-link', {active: tab.value === label}],
   'aria-current': tab.value === label ? 'page' : undefined,
   'aria-label': label,
   onClick: () => {
    if (tab.value === label) return;
    s.notice.value = '';
    tab.value = label;
   },
  }, [
   h('span', {class: 'nav-icon', 'aria-hidden': 'true'}, [h(icon, {size: 20, strokeWidth: 1.9})]),
   title,
  ]));
 }
 return () => storefront(navigation(), [
  tab.value === '商品' ? catalogView() : null,
  tab.value === '商品' && checkoutOpen.value ? checkoutView() : null,
  tab.value === '我的订单' ? ordersView() : null,
  tab.value === '地址簿' ? (s.me.value ? addressView() : empty('请登录后管理地址')) : null,
  tab.value === '我的账号' && s.me.value ? profileView() : null,
  !s.me.value ? h('section', {class: 'signin-section', 'aria-label': '登录'}, [testLogin(), login()]) : null,
 ], h('button', {
  type: 'button', class: 'delivery-location', 'aria-label': '地址簿', onClick: () => tab.value = '地址簿',
 }, [
  h(MapPinHouse, {size: 18, strokeWidth: 1.9, 'aria-hidden': 'true'}),
  h('span', {'data-sensitive': deliveryAddress.value ? true : undefined},
   deliveryAddress.value ? `${deliveryAddress.value.building_name} ${deliveryAddress.value.room}` : '选择配送地址'),
  h(ArrowRight, {size: 16, 'aria-hidden': 'true'}),
 ]), tab.value === '商品' ? catalogSearch() : null, tab.value === '商品' ? checkoutBar() : null, s.status(), s.tools());
}});
export function mount(root:HTMLElement){assertNoProductionMock(import.meta.env.PROD,import.meta.env.VITE_ENABLE_MOCK);return createApp(UserApp).mount(root);}
if(document.querySelector<HTMLElement>('#app')?.dataset.entry==='user')mount(document.querySelector<HTMLElement>('#app')!);
export default UserApp;
