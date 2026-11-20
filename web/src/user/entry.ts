import { createApp, h, ref, onMounted, onUnmounted, watch } from 'vue';
import { apiRequest, ApiError, assertNoProductionMock } from '../shared/api.ts';
import type { Address, AddressInput, AddressList, AddressUpdate, Product, ProductList, Order, OrderList, OrderLineInput, Quote, QuoteInput, PaymentMethods, PaymentMethod, Slot, SlotList, Wallet, OTPChallenge, Me, OrderInput, EmailChallenge, EmailCodeRequest, EmailBindRequest } from '../shared/generated.ts';
import { session, localTestLogin, sessionKey, shell, money, date, today, dayAfter, id, text, btn, field, select, card, empty, badge, stateText, form } from './ui.ts';

const storage='xiaoling.cart';
export function readCart():OrderLineInput[]{try{const value:unknown=JSON.parse(localStorage.getItem(storage)||'[]');return Array.isArray(value)?value.filter((x):x is OrderLineInput=>x&&typeof x.sku_id==='string'&&Number.isSafeInteger(x.qty)&&x.qty>0&&x.qty<=100).map(x=>({sku_id:x.sku_id,qty:x.qty})):[];}catch{return [];}}
export const UserApp={setup(){
 const tab=ref('商品'),products=ref<Product[]>([]),search=ref(''),product=ref<Product>(),cart=ref<OrderLineInput[]>(readCart()),addresses=ref<Address[]>([]),address=ref(''),slots=ref<Slot[]>([]),mode=ref<'ASAP'|'SCHEDULED'>('ASAP'),slot=ref(''),method=ref<PaymentMethod>('COD'),payment=ref<PaymentMethods>(),wallet=ref<Wallet>(),quote=ref<Quote>(),orders=ref<Order[]>([]),detail=ref<Order>(),note=ref(''),reason=ref('CUSTOMER_REQUEST');
 const phone=ref(''),code=ref(''),challenge=ref<OTPChallenge>(),editing=ref<Address>();
 const authMode=ref<'EMAIL_LOGIN'|'EMAIL_REGISTER'|'PHONE_LOGIN'>('EMAIL_LOGIN');
 const email=ref(''),emailCode=ref(''),emailChallenge=ref<EmailChallenge>(),bindEmail=ref(''),bindCode=ref(''),bindChallenge=ref<EmailChallenge>();
 const emailNow=ref(Date.now()),cooldowns=new Map<string,number>();
 let resendTimer=0;
 const addressForm=ref<AddressInput>({building_id:'',room:'',contact_name:'',contact_phone:''});
 const reservationDate=ref(today());let catalogGeneration=0,quoteGeneration=0,slotGeneration=0;
 const s=session(['CUSTOMER'],refresh);
 const testLogin=localTestLogin('user',s);
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){addresses.value=[];address.value='';orders.value=[];detail.value=undefined;wallet.value=undefined;payment.value=undefined;invalidate();editing.value=undefined;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};bindEmail.value='';bindCode.value='';bindChallenge.value=undefined;}},{flush:'sync'});
 async function catalog(){const g=++catalogGeneration;const v=await s.list<ProductList>(`/catalog/products?limit=100${search.value?`&q=${id(search.value)}`:''}`);if(g===catalogGeneration)products.value=v.items;}
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
 function save(){try{localStorage.setItem(storage,JSON.stringify(cart.value.map(({sku_id,qty})=>({sku_id,qty}))));}catch{s.notice.value='浏览器未允许保存购物袋，当前选择仍可使用。';}invalidate();}
 function add(sku_id:string){const existing=cart.value.find(l=>l.sku_id===sku_id);if(existing){if(existing.qty<100)existing.qty++;}else cart.value.push({sku_id,qty:1});save();s.notice.value='已加入购物袋';}
 function changeQty(sku_id:string,qty:number){cart.value=cart.value.map(l=>l.sku_id===sku_id?{sku_id,qty}:l).filter(l=>l.qty>0);save();}
 const known=(sku_id:string)=>{for(const p of products.value){const sku=p.skus.find(s=>s.id===sku_id);if(sku)return {name:p.name,sku};}return undefined;};
 async function getQuote(){invalidate();const token=quoteGeneration;const input:QuoteInput={lines:cart.value.map(l=>({...l})),address_id:address.value,delivery_mode:mode.value,payment_method:method.value,...(mode.value==='SCHEDULED'?{slot_id:slot.value}:{})};await s.write<Quote>('/checkout/quote',input,v=>{if(token!==quoteGeneration){s.notice.value='配送信息或购物袋已改变，旧报价已失效，请重新获取报价。';return;}quote.value=v;s.notice.value='报价已更新，请核对金额并再次确认下单。';});}
 async function confirm(){const q=quote.value;if(!q)return;if(Date.parse(q.valid_until)<=Date.now()){invalidate();s.error.value='报价已过期，请刷新报价后重新确认。';return;}
  const input:OrderInput={quote_id:q.quote_id,...(note.value?{note:note.value}:{})};await s.write<Order>('/orders',input,async v=>{detail.value=v;quote.value=undefined;tab.value='我的订单';cart.value=[];save();await refresh();s.notice.value='服务器已确认订单。';});if(s.error.value&&!s.pending.value){invalidate();s.error.value+='。请刷新信息和报价后重新确认，支付方式与所选时段已保留。';}}
 async function rebuy(o:Order){await s.run(async()=>{await catalog();if(reservationDate.value<today()||reservationDate.value>dayAfter(3))reservationDate.value=today();await loadSlots();cart.value=o.lines.map(l=>({sku_id:l.sku_id,qty:l.qty}));save();slot.value='';tab.value='购物袋';s.notice.value='已重新获取当前商品和服务窗口，请核对规格、库存、支付方式和配送时段，再请求新报价。';});}
 function showOrder(o:Order){void s.run(async()=>{detail.value=await s.get<Order>(`/orders/${id(o.id)}`);tab.value='我的订单';});}
 function orderCard(o:Order,full=false){return card(full?'订单详情':`订单 ${o.id}`,[h('p',{class:'muted'},`${date(o.created_at)} · ${o.delivery_mode==='ASAP'?'即时配送':'预约配送'}`),h('div',[badge(o.status),badge(o.stock_status),badge(o.settlement_status)]),h('p',{class:'price'},money(o.total_fen)),h('p',o.payment_method==='COD'&&o.settlement_status==='COD_UNCOLLECTED'?'送达后付款':o.payment_method==='BALANCE'&&o.settlement_status==='BALANCE_PAID'?'余额已扣':stateText(o.settlement_status)),h('p',`配送窗口 ${date(o.slot.starts_at)} — ${date(o.slot.ends_at)}`),...o.lines.map(l=>h('p',{class:'item'},`${l.name} · ${l.spec} × ${l.qty} · ${money(l.line_total_fen)}`)),full?h('ol',{class:'timeline','aria-label':'订单时间轴'},[
 h('li',`已下单 · ${date(o.created_at)}`),h('li',{class:'current'},`当前履约：${stateText(o.status)}`),h('li',`备货 / 库存：${stateText(o.stock_status)}`),h('li',`资金：${stateText(o.settlement_status)}`),h('li','详细流转时间尚未由接口提供；不显示推算到达时间。')]):null,
 full?field('操作原因码',reason.value,v=>reason.value=v):null,h('div',{class:'actions'},[full?null:btn('查看详情',()=>showOrder(o),s.busy.value,`order-${o.id}`),btn('再买一次',()=>rebuy(o),s.busy.value),full?btn('取消订单',()=>s.write<Order>(`/orders/${id(o.id)}/cancel`,{reason_code:reason.value},async v=>{detail.value=v;await refresh();}),s.busy.value||!['SCHEDULED','PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP'].includes(o.status),'cancel-order'):null,full?btn('催单',()=>s.write(`/orders/${id(o.id)}/urge`,undefined,()=>s.notice.value='服务器已接受催单。'),s.busy.value||['CANCELED','DELIVERED','RETURNED'].includes(o.status),'urge-order'):null])],{'data-testid':full?'order-detail':undefined});}
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
 function addressView(){return h('div',{class:'split'},[card('我的地址',[...addresses.value.map(a=>h('article',{class:'item','data-sensitive':true},[h('b',`${a.building_name} ${a.room}`),h('p',`${a.contact_name} · ${a.contact_phone}`),h('div',{class:'actions'},[btn('编辑地址',()=>{editing.value=a;addressForm.value={building_id:a.building_id,room:a.room,contact_name:a.contact_name,contact_phone:a.contact_phone};},s.busy.value,`edit-address-${a.id}`),btn('删除地址',()=>deleteAddress(a),s.busy.value,`delete-address-${a.id}`)])])),!addresses.value.length?empty('请添加配送地址'):null]),card(editing.value?'编辑地址':'新增地址',[h('p',{class:'muted'},'楼栋目录接口尚未提供；请填写站点公布的楼栋 ID。'),form([
 field('楼栋 ID',addressForm.value.building_id,v=>addressForm.value.building_id=v,'text',{required:true}),field('房间号',addressForm.value.room,v=>addressForm.value.room=v,'text',{required:true,'data-sensitive':true}),field('联系人',addressForm.value.contact_name,v=>addressForm.value.contact_name=v,'text',{required:true,'data-sensitive':true}),field('联系电话',addressForm.value.contact_phone,v=>addressForm.value.contact_phone=v,'tel',{required:true,'data-sensitive':true}),h('div',{class:'actions'},[h('button',{type:'submit',class:'btn primary',disabled:s.busy.value},'保存地址'),editing.value?btn('放弃编辑',()=>{editing.value=undefined;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};}):null])
 ],async()=>{if(editing.value){const input:AddressUpdate={...addressForm.value,expected_version:editing.value.version};await s.write<Address>(`/me/addresses/${id(editing.value.id)}`,input,addressSaved,'PATCH');}else await s.write<Address>('/me/addresses',{...addressForm.value},addressSaved);})])]);}
 async function addressSaved(a:Address){editing.value=undefined;address.value=a.id;addressForm.value={building_id:'',room:'',contact_name:'',contact_phone:''};invalidate();await refresh();s.notice.value='地址已由服务器保存。';}
 async function deleteAddress(a:Address){await s.run(async()=>{await apiRequest(`/me/addresses/${id(a.id)}`,{method:'DELETE',csrf:s.me.value?.csrf_token,fetcher:(url,options)=>{const headers=new Headers(options?.headers);headers.set('If-Match',`"${a.version}"`);return fetch(url,{...options,headers});}});if(address.value===a.id)address.value='';invalidate();await refresh();s.notice.value='地址已删除。';});}
 function catalogView(){return h('div',[h('div',{class:'note'},[h('h2','把校园日常，装进购物袋'),h('p','选择喜欢的商品，按服务窗口配送。价格、库存与付款状态由站点实时确认。')]),form([field('搜索商品',search.value,v=>search.value=v,'search'),h('button',{class:'btn',type:'submit',disabled:s.busy.value},'搜索')],()=>s.run(catalog)),h('div',{class:'actions'},[btn('刷新商品',()=>s.run(catalog),s.busy.value)]),products.value.length?h('div',{class:'grid'},products.value.map(p=>h('article',{class:'card'},[
 h('div',{class:'art','aria-hidden':'true'},[h('svg',{viewBox:'0 0 80 80'},[h('path',{d:'M22 25h36l-3 42H25z M29 25v-8q11-10 22 0v8 M32 40h16 M32 47h16 M33 54h14'})])]),h('h3',p.name),h('p',{class:'muted'},p.description),...p.skus.map(sku=>h('div',{class:'item'},[h('p',sku.label),h('strong',{class:'price'},money(sku.unit_price_fen)),h('div',{class:'actions'},[btn('加入购物袋',()=>add(sku.id),false,`add-${sku.id}`)])])),btn('商品详情',()=>s.run(async()=>product.value=await s.get<Product>(`/catalog/products/${id(p.id)}`)),s.busy.value)
 ]))):empty('没有找到商品'),product.value?card(product.value.name,[h('p',product.value.description),...product.value.skus.map(sku=>h('p',`${sku.label} · 每包 ${sku.units_per_pack} 件 · ${money(sku.unit_price_fen)}`)),btn('关闭详情',()=>product.value=undefined)],{class:'card detail'}):null]);}
 function cartView(){return h('div',{class:'split'},[card('购物袋',[...cart.value.map(l=>{const k=known(l.sku_id);return h('div',{class:'item'},[h('b',k?`${k.name} · ${k.sku.label}`:`商品规格 ${l.sku_id}`),h('p',k?`${money(k.sku.unit_price_fen)} / 份 · 当前商品价，最终以报价为准`:'商品可能已下架，请刷新商品核对。'),h('div',{class:'actions'},[btn('−',()=>changeQty(l.sku_id,l.qty-1)),h('span',String(l.qty)),btn('+',()=>changeQty(l.sku_id,Math.min(100,l.qty+1))),btn('移除',()=>changeQty(l.sku_id,0))])]);}),!cart.value.length?empty('购物袋还是空的'):null,btn('继续选购',()=>tab.value='商品')]),card('配送与结算',[
 select('配送地址',address.value,[['','请选择地址'],...addresses.value.map(a=>[a.id,`${a.building_name} ${a.room}`] as [string,string])],v=>{address.value=v;invalidate();}),btn('管理地址',()=>tab.value='地址簿'),
 select('配送方式',mode.value,[['ASAP','即时配送'],['SCHEDULED','预约配送']],v=>{mode.value=v as 'ASAP'|'SCHEDULED';invalidate();}),
 mode.value==='SCHEDULED'?h('div',[field('预约日期',reservationDate.value,chooseDate,'date',{min:today(),max:dayAfter(3),'aria-label':'预约日期'}),select('服务窗口',slot.value,[['','请选择窗口'],...slots.value.map(w=>[w.id,`${date(w.starts_at)} — ${date(w.ends_at)}${!w.accepting?'（停止接单）':''}`,!w.accepting] as [string,string,boolean])],v=>{slot.value=v;invalidate();})]):h('p',{class:'muted'},'即时配送窗口由服务器报价确定。'),
 h('fieldset',[h('legend','支付方式'),...(['COD','BALANCE','WECHAT'] as PaymentMethod[]).map(m=>{const option=payment.value?.methods.find(v=>v.code===m);const label=m==='WECHAT'?'微信待接入':m==='BALANCE'?`余额${wallet.value?`（${money(wallet.value.balance_fen)}）`:''}${!option?.enabled?' · 未开放':''}`:'货到付款';return h('label',{class:'check'},[h('input',{type:'radio',name:'payment',value:m,checked:method.value===m,disabled:m==='WECHAT'||!option?.enabled,onChange:()=>{method.value=m;invalidate();},'aria-label':m==='WECHAT'?'微信待接入':m==='BALANCE'?'余额':'货到付款'}),label]);})]),
 field('订单备注',note.value,v=>{note.value=v;invalidate();},'text',{maxlength:200,'data-sensitive':true}),h('div',{class:'actions'},[btn('获取报价',getQuote,s.busy.value||!s.me.value||!cart.value.length||!address.value||(mode.value==='SCHEDULED'&&!slot.value)||!!s.pending.value,'quote', 'primary'),btn('刷新配送与支付信息',()=>s.run(async()=>{invalidate();await refresh();s.notice.value='信息已刷新，原支付和时段选择保留，请重新报价。';}),s.busy.value)]),
 quote.value?card('请核对本次报价',[...quote.value.lines.map(l=>h('p',`${l.name} ${l.spec} × ${l.qty} · ${money(l.line_total_fen)}`)),h('p',{class:'price'},`合计 ${money(quote.value.total_fen)}`),h('p',`报价有效至 ${date(quote.value.valid_until)}`),h('p',`服务窗口 ${slots.value.find(w=>w.id===quote.value!.slot_id)?date(slots.value.find(w=>w.id===quote.value!.slot_id)!.starts_at):quote.value.slot_id}`),h('p',quote.value.payment_method==='COD'?'确认后送达后付款':'余额付款以服务器扣款结果为准'),btn('确认下单',confirm,s.busy.value||!!s.pending.value,'confirm-order','red')]):null
 ])]);}
 onMounted(async()=>{resendTimer=window.setInterval(()=>{emailNow.value=Date.now();for(const [key,until] of cooldowns)if(until<=emailNow.value)cooldowns.delete(key);},1000);await s.recover();if(!s.me.value)try{await catalog();}catch{}});
 onUnmounted(()=>{window.clearInterval(resendTimer);});
 return ()=>shell('user','把日常，送到身边',h('div',{class:'actions'},['商品','购物袋','我的订单','地址簿','我的账号'].map(t=>btn(t==='购物袋'?`购物袋 (${cart.value.reduce((n,l)=>n+l.qty,0)})`:t,()=>{tab.value=t;},false,undefined,tab.value===t?'active':''))),[
 testLogin(),!s.me.value?login():null,tab.value==='商品'?catalogView():null,tab.value==='购物袋'?cartView():null,tab.value==='我的订单'?h('div',[h('div',{class:'actions'},[btn('刷新订单',()=>s.run(refresh),s.busy.value)]),detail.value?orderCard(detail.value,true):null,...orders.value.filter(o=>o.id!==detail.value?.id).map(o=>orderCard(o)),!orders.value.length?empty('还没有订单'):null]):null,tab.value==='地址簿'?(s.me.value?addressView():empty('请登录后管理地址')):null,tab.value==='我的账号'?(s.me.value?accountView():empty('请登录后查看账号')):null
 ],s.tools(),s.status());
}};
export function mount(root:HTMLElement){assertNoProductionMock(import.meta.env.PROD,import.meta.env.VITE_ENABLE_MOCK);return createApp(UserApp).mount(root);}
if(document.querySelector<HTMLElement>('#app')?.dataset.entry==='user')mount(document.querySelector<HTMLElement>('#app')!);
export default UserApp;
