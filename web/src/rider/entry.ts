import { createApp, h, nextTick, ref, onMounted, onUnmounted, watch } from 'vue';
import { ApiError, assertNoProductionMock } from '../shared/api.ts';
import type { LobbyTask, LobbyTaskList, RiderTask, Earning, EarningList, Message, MessageList, Availability, DeliverInput, Me } from '../shared/generated.ts';
import type { OrderBatchResult, RiderCapacity } from '../shared/generated.ts';
import { session, localTestLogin, sessionKey, shell, money, date, today, id, btn, field, check, card, empty, badge, select } from '../user/ui.ts';
import './orders.css';

const activeStates=['AWAITING_PICKUP','IN_DELIVERY','RETURNING'];
const prepaidInDelivery=(task:LobbyTask)=>task.payment_method==='BALANCE'&&task.order_status==='IN_DELIVERY';
const paymentClass=(task:LobbyTask)=>task.payment_method==='COD'?'payment-cod':'payment-prepaid';
const paymentLabel=(task:LobbyTask)=>h('strong',{class:'payment-label'},task.payment_method==='COD'?'货到付款':'用户已付款');

export const RiderApp={setup(){
 const tab=ref('站点大厅'),tasks=ref<LobbyTask[]>([]),history=ref<LobbyTask[]>([]),earnings=ref<Earning[]>([]),messages=ref<Message[]>([]),current=ref<RiderTask>(),available=ref<boolean>(),cash=ref(false),reason=ref('CUSTOMER_REFUSED'),incomeDate=ref(today()),lastUpdated=ref(''),polling=ref(false);
 const capacity=ref<RiderCapacity>();
 const paymentFilter=ref('ALL');
 const claimSelection=ref<string[]>([]);
 const deliverySelection=ref<string[]>([]);
 let timer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|undefined,generation=0,failures=0,disposed=false;
 const s=session(['RIDER'],refresh,error=>{
  if(error.code==='ORDER_LIMIT_REACHED')return '所选订单超过剩余接单名额，请处理手上的订单或减少选择。';
  if(error.code==='COD_CLAIM_EXCLUSIVE')return '货到付款订单需单独配送，请先完成手上的订单。';
  if(error.code==='COD_BATCH_DELIVERY_FORBIDDEN')return '货到付款订单需逐单确认收款后送达。';
 });
 const testLogin=localTestLogin('rider',s);
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){invalidate();tasks.value=[];history.value=[];earnings.value=[];messages.value=[];current.value=undefined;available.value=undefined;cash.value=false;lastUpdated.value='';}},{flush:'sync'});
 watch(()=>sessionKey(s.me.value),(value,prior)=>{
  if(value===prior)return;
  capacity.value=undefined;
  paymentFilter.value='ALL';
  claimSelection.value=[];
  deliverySelection.value=[];
 },{flush:'sync'});
 function invalidate(){generation++;controller?.abort();controller=undefined;clearTimeout(timer);polling.value=false;}
 function schedule(){clearTimeout(timer);if(disposed||document.hidden||!s.me.value)return;timer=setTimeout(()=>{void poll();},Math.min(60000,10000*2**Math.min(failures,3)));}
 async function snapshot(signal:AbortSignal){
  const identity=await s.get<Me>('/me',signal);if(!identity.roles.includes('RIDER'))throw new ApiError(403,'FORBIDDEN','骑手权限已撤销');
  const [hall,past,income,mail,slots]=await Promise.all([
   s.list<LobbyTaskList>('/rider/tasks?limit=100',signal),s.list<LobbyTaskList>('/rider/history?limit=100',signal),
   s.list<EarningList>(`/rider/earnings?date=${incomeDate.value}&limit=100`,signal),s.list<MessageList>('/rider/messages?limit=100',signal),
   s.get<RiderCapacity>('/rider/capacity',signal),
  ]);
  const active=hall.items.filter(t=>activeStates.includes(t.order_status));
  const focus=active.find(t=>t.id===current.value?.id)
   ??active.filter(t=>t.order_status!=='RETURNING')
    .sort((a,b)=>Date.parse(a.slot.ends_at)-Date.parse(b.slot.ends_at))[0]
   ??active[0];
  const detail=focus?await s.get<RiderTask>(`/rider/tasks/${id(focus.id)}`,signal):undefined;
  return {identity,hall,past,income,mail,detail,slots};
 }
 async function poll(force=false){if(polling.value||disposed||document.hidden||(!force&&s.busy.value)||!s.me.value){schedule();return;}const token=++generation;const actor=sessionKey(s.me.value);controller=new AbortController();polling.value=true;
  try{const result=await snapshot(controller.signal);if(token!==generation||disposed||document.hidden||sessionKey(s.me.value)!==actor){if(force)throw new DOMException('刷新已失效','AbortError');return;}s.me.value=result.identity;tasks.value=result.hall.items;history.value=result.past.items;earnings.value=result.income.items;messages.value=result.mail.items;
   capacity.value=result.slots;
   claimSelection.value=claimSelection.value.filter(key=>tasks.value.some(t=>t.id===key&&t.order_status==='READY_FOR_CLAIM'));
   deliverySelection.value=deliverySelection.value.filter(key=>tasks.value.some(t=>t.id===key&&prepaidInDelivery(t)));
   if(!s.pending.value){const same=current.value?.id===result.detail?.id&&current.value?.order_status===result.detail?.order_status;current.value=result.detail;if(!same)cash.value=false;}lastUpdated.value=new Date().toISOString();failures=0;
  }catch(e){if(token===generation&&!(e instanceof DOMException&&e.name==='AbortError')){failures++;if(e instanceof ApiError&&(e.status===401||e.status===403)){current.value=undefined;tasks.value=[];history.value=[];earnings.value=[];messages.value=[];s.me.value=undefined;s.ready.value=false;s.failure(e);}}if(force)throw e;}
  finally{if(token===generation){polling.value=false;controller=undefined;schedule();}}
 }
 async function refresh(){if(document.hidden)throw new Error('页面已隐藏，请回到当前页面后核对服务器记录。');invalidate();await poll(true);}
 async function command(action:'claim'|'pickup'|'deliver'|'return',taskId:string,body?:DeliverInput|{reason_code:string}){
  invalidate();cash.value=false;
  await s.write<RiderTask>(`/rider/tasks/${id(taskId)}/${action}`,body,async v=>{
   s.notice.value=`服务器已确认：${action==='claim'?'接单':action==='pickup'?'取货':action==='deliver'?'送达':'拒付退回'}。`;
   if(action==='deliver'){
    await advanceAfterDelivery([taskId]);
   }else{
    current.value=v;
    if(action==='claim')navigate('当前任务');
    await refresh();
   }
  });
  if(s.error.value&&!s.pending.value){try{await refresh();}catch(e){s.failure(e);}}schedule();
 }
 const isBusy=()=>s.busy.value||!!s.pending.value;
 const remaining=()=>capacity.value?.remaining??0;
 function navigate(next:string){
  if(next===tab.value)return;
  tab.value=next;
  changePaymentFilter('ALL');
 }
 function changePaymentFilter(value:string){
  paymentFilter.value=value;
  claimSelection.value=[];
  deliverySelection.value=[];
 }
 async function focusTaskDetail(){
  await nextTick();
  document.getElementById('rider-task-detail')?.focus();
 }
 async function advanceAfterDelivery(completedIds:string[]){
  current.value=undefined;
  cash.value=false;
  tasks.value=tasks.value.filter(t=>!completedIds.includes(t.id));
  deliverySelection.value=deliverySelection.value.filter(key=>!completedIds.includes(key));
  navigate('当前任务');
  if(paymentFilter.value!=='ALL')changePaymentFilter('ALL');
  await refresh();
  await focusTaskDetail();
 }
 function matchesPayment(task:LobbyTask){
  return paymentFilter.value==='ALL'||task.payment_method===paymentFilter.value;
 }
 function paymentSelect(){
  return select('付款方式',paymentFilter.value,[['ALL','全部付款方式'],['COD','货到付款'],['BALANCE','用户已付款']],changePaymentFilter,{disabled:isBusy()});
 }
 async function runTaskBatch(action:'claim'|'deliver'){
  const selection=action==='claim'?claimSelection:deliverySelection;
  const ids=[...selection.value];
  invalidate();
  await s.write<OrderBatchResult>(`/rider/task-batches/${action}`,{ids},async result=>{
   selection.value=[];
   if(action==='deliver'){
    await advanceAfterDelivery(ids);
   }else{
    navigate('当前任务');
    await refresh();
   }
   s.notice.value=`已${action==='claim'?'接单':'送达'} ${result.count} 单。`;
  });
  if(s.error.value&&!s.pending.value){try{await refresh();}catch(error){s.failure(error);}}
  schedule();
 }
 async function openTask(task:LobbyTask){
  await s.run(async()=>{
   invalidate();
   try{
    const result=await s.get<RiderTask>(`/rider/tasks/${id(task.id)}`);
    current.value=result;
    cash.value=false;
    await focusTaskDetail();
   }finally{schedule();}
  });
 }
 async function setAvailability(value:boolean){invalidate();await s.write<Availability>('/rider/availability',{available:value},async v=>{available.value=v.available;s.notice.value=v.available?'服务器已确认上线':'服务器已确认下线';await refresh();},'PUT');schedule();}
 function taskCard(t:LobbyTask, inHall:boolean){
  const selection=inHall?claimSelection:deliverySelection;
  const selected=selection.value.includes(t.id);
  const selectable=inHall?t.payment_method!=='COD':prepaidInDelivery(t);
  const busyWithOrders=(capacity.value?.active??0)>0;
  const canClaim=!isBusy()&&available.value!==false&&remaining()>0&&(t.payment_method!=='COD'||!busyWithOrders);
  return card(t.building_name,[
   paymentLabel(t),
   h('p',{class:'muted'},`任务 ${t.id} · ${t.item_count} 件商品`),
   badge(t.order_status),
   h('p',`服务窗口 ${date(t.slot.starts_at)} — ${date(t.slot.ends_at)}`),
   h('p',`配送费 ${money(t.fee_fen)}`),
   selectable?check(inHall?'选择接单':'选择送达',selected,v=>{
    selection.value=v?[...selection.value,t.id]:selection.value.filter(key=>key!==t.id);
   },isBusy()||(!selected&&inHall&&(!canClaim||selection.value.length>=remaining()))):null,
   inHall
    ?btn('接单',()=>command('claim',t.id),!canClaim,`claim-${t.id}`,'primary')
    :btn('处理订单',()=>openTask(t),isBusy(),`task-${t.id}`,t.id===current.value?.id?'primary':''),
  ],{key:t.id,class:`card payment-task ${paymentClass(t)}`,'data-testid':`rider-task-${t.id}`});
 }
 function tasksView(inHall:boolean){
  const list=tasks.value.filter(t=>matchesPayment(t)&&(inHall?t.order_status==='READY_FOR_CLAIM':activeStates.includes(t.order_status)));
  const selection=inHall?claimSelection.value:deliverySelection.value;
  return h('div',[
   paymentSelect(),
   inHall?h('p',{'aria-live':'polite'},capacity.value?.cod_active
    ?'当前有货到付款订单，处理完成后可继续接单。'
    :'货到付款订单需要空手接单，每次配送一单。'):null,
   h('div',{class:'actions'},[
    h('span',{class:'muted','aria-live':'polite'},`已选 ${selection.length} 单`),
    inHall?btn('批量接单',()=>runTaskBatch('claim'),isBusy()||available.value===false||!selection.length||selection.length>remaining(),'rider-batch-claim','primary')
     :btn('批量送达',()=>runTaskBatch('deliver'),isBusy()||!selection.length,'rider-batch-deliver','primary'),
    btn('清除选择',()=>{if(inHall)claimSelection.value=[];else deliverySelection.value=[];},isBusy()||!selection.length),
   ]),
   list.length?h('div',{class:'grid rider-task-grid'},list.map(t=>taskCard(t,inHall))):empty(
    paymentFilter.value==='ALL'?(inHall?'大厅暂无可接任务':'手上暂无进行中的任务'):'暂无符合条件的任务'),
   !inHall&&current.value&&matchesPayment(current.value)?detail():null,
  ]);
 }

 function detail(){const t=current.value;if(!t)return empty('当前没有进行中的任务，请到站点大厅刷新。');return card('当前任务详情',[paymentLabel(t),h('p',`任务 ${t.id}`),badge(t.order_status),h('p',`${t.building_name} · ${t.item_count} 件 · 配送费 ${money(t.fee_fen)}`),h('p',`服务窗口 ${date(t.slot.starts_at)} — ${date(t.slot.ends_at)}`),t.address?h('div',{'data-sensitive':true,class:'note'},[h('b',`${t.address.building_name} ${t.address.room}`),h('p',t.address.contact_name),h('a',{href:`tel:${t.address.contact_phone}`},t.address.contact_phone)]):h('p','此状态下服务器未授权查看详细地址或电话。'),t.cod_due_fen !== undefined ? h('p',{class:'price'},`货到付款应收 ${money(t.cod_due_fen)}`) : null,
 t.order_status==='IN_DELIVERY' && t.cod_due_fen !== undefined ? check('已全额收到货到付款',cash.value,v=>cash.value=v) : null,
 ['IN_DELIVERY','RETURNING'].includes(t.order_status)?field('拒付 / 退回原因码',reason.value,v=>reason.value=v):null,
 h('div',{class:'actions'},[
 btn('确认取货',()=>command('pickup',t.id),s.busy.value||t.order_status!=='AWAITING_PICKUP'||!!s.pending.value,'pickup','primary'),
 t.cod_due_fen !== undefined ? btn('确认货到付款送达',()=>command('deliver',t.id,{cod_received:true}),s.busy.value||t.order_status!=='IN_DELIVERY'||!cash.value||!!s.pending.value,'deliver','primary') : null,
 t.payment_method==='BALANCE' ? btn('线上已付款订单送达',()=>command('deliver',t.id,{}),s.busy.value||t.order_status!=='IN_DELIVERY'||!!s.pending.value,'deliver-prepaid') : null,
 btn('拒付并退回',()=>command('return',t.id,{reason_code:reason.value}),s.busy.value||t.order_status!=='IN_DELIVERY'||!reason.value||!!s.pending.value,'return','red')]),t.order_status==='RETURNING'?h('p','请将商品交回站点，由运营验退后结束任务。'):null],{id:'rider-task-detail',tabindex:-1,'data-testid':'current-task',class:'card payment-task current-task '+paymentClass(t)});}
 async function markRead(ids:string[]){const remaining=ids.filter(k=>messages.value.some(m=>m.id===k&&!m.read_at));const chunk=remaining.slice(0,100);if(!chunk.length){s.notice.value='全部消息已由服务器标记为已读。';return;}let accepted=false;invalidate();await s.write('/rider/messages/read',{ids:chunk},async()=>{accepted=true;for(const m of messages.value)if(chunk.includes(m.id))m.read_at=new Date().toISOString();if(remaining.length>100)s.notice.value=`已确认 ${chunk.length} 条，继续处理其余 ${remaining.length-chunk.length} 条。`;else s.notice.value='消息已由服务器标记为已读。';});if(accepted&&remaining.length>100&&!s.pending.value)await markRead(remaining.slice(100));else schedule();}
 function messagesView(){return card('站内消息',[...messages.value.map(m=>h('article',{class:'item'},[h('b',m.kind==='ORDER_URGE'?'用户催单':m.kind==='ORDER_OVERDUE'?'订单逾期提醒':m.kind),h('p',`任务 ${m.task_id} · ${date(m.created_at)}`),h('p',m.read_at?`已读 ${date(m.read_at)}`:'未读'),!m.read_at?btn('标为已读',()=>markRead([m.id]),s.busy.value,`read-${m.id}`):null])),!messages.value.length?empty('暂无站内消息'):null,btn('全部标为已读',()=>markRead(messages.value.filter(m=>!m.read_at).map(m=>m.id)),s.busy.value||!!s.pending.value||!messages.value.some(m=>!m.read_at),'read-all')]);}
 function pastView(){
  const list=history.value.filter(matchesPayment);
  return h('div',{class:'split'},[
   card('本人配送历史',[
    paymentSelect(),
    ...list.map(t=>card(t.building_name,[
     paymentLabel(t),h('p',`任务 ${t.id} · ${t.item_count} 件`),badge(t.order_status),h('p',date(t.slot.starts_at)),
    ],{key:t.id,class:`card payment-task ${paymentClass(t)}`})),
    !list.length?empty('暂无符合条件的配送历史'):null,
   ]),
   card('实际收入记录',[
    field('收入日期',incomeDate.value,v=>{incomeDate.value=v;void s.run(refresh);},'date'),
    h('p',{class:'price'},money(earnings.value.reduce((a,e)=>a+e.amount_fen,0))),
    h('p',{class:'muted'},'仅汇总服务器返回的本日收入记录，不含预计收益。'),
    ...earnings.value.map(e=>h('article',{class:'item'},[
     h('b',`配送费收入 ${money(e.amount_fen-(e.commission_fen??0))}`),
     e.commission_fen!==undefined?h('p',`货到付款提成 ${money(e.commission_fen)} · 合计 ${money(e.amount_fen)}`):null,
     h('p',`任务 ${e.task_id} · ${date(e.recorded_at)}`),
    ])),
    !earnings.value.length?empty('本日暂无收入记录'):null,
   ]),
  ]);
 }
 const visibility=()=>{if(document.hidden)invalidate();else void s.run(refresh);};
 onMounted(async()=>{document.addEventListener('visibilitychange',visibility);await s.recover();schedule();});
 onUnmounted(()=>{disposed=true;invalidate();document.removeEventListener('visibilitychange',visibility);});
 return ()=>shell('rider','把每一程，稳稳送好',h('div',{class:'actions'},['站点大厅','当前任务','消息','历史与收入'].map(t=>btn(t,()=>navigate(t),false,undefined,tab.value===t?'active':''))),[
 testLogin(),!s.me.value?card('员工登录与权限恢复',[h('p','请通过当前骑手入口的员工登录恢复会话。失效或停用的会话不能继续领取或履约。'),h('a',{href:'/api/v1/auth/staff/start'},'前往员工登录')]):null,
 s.me.value?h('div',{class:'note'},[h('h2',available.value===undefined?'接单状态待确认':available.value?'已上线，准备出发':'已下线'),h('p',available.value===undefined?'契约没有当前在线状态读取接口，可向服务器确认上线或下线。':'状态来自最近一次服务器确认。'),h('div',{class:'actions'},[btn('上线接单',()=>setAvailability(true),s.busy.value,'go-online','primary'),btn('下线休息',()=>setAvailability(false),s.busy.value,'go-offline'),btn('刷新任务',()=>s.run(refresh),s.busy.value||polling.value,'refresh-tasks')]),h('p',{class:'muted'},lastUpdated.value?`最近刷新 ${date(lastUpdated.value)} · 页面隐藏时暂停轮询`:'等待服务器数据'),h('p',{class:'muted'},'工作窗口以每个任务的服务器时段显示。独立排班接口尚未提供。'),btn('设置工作时段',()=>{},true),capacity.value?h('p',{'aria-live':'polite','data-testid':'rider-capacity'},`手上 ${capacity.value.active} / ${capacity.value.limit} 单 · 还可接 ${capacity.value.remaining} 单`):null]):null,
 s.me.value?(tab.value==='站点大厅'?tasksView(true):tab.value==='当前任务'?tasksView(false):tab.value==='消息'?messagesView():pastView()):null
 ],s.tools(),s.status());
}};
export function mount(root:HTMLElement){assertNoProductionMock(import.meta.env.PROD,import.meta.env.VITE_ENABLE_MOCK);return createApp(RiderApp).mount(root);}
if(document.querySelector<HTMLElement>('#app')?.dataset.entry==='rider')mount(document.querySelector<HTMLElement>('#app')!);
export default RiderApp;
