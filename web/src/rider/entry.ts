import { createApp, h, ref, onMounted, onUnmounted, watch } from 'vue';
import { ApiError, assertNoProductionMock } from '../shared/api.ts';
import type { LobbyTask, LobbyTaskList, RiderTask, Earning, EarningList, Message, MessageList, Availability, DeliverInput, Me } from '../shared/generated.ts';
import { session, localTestLogin, sessionKey, shell, money, date, today, id, btn, field, check, card, empty, badge } from '../user/ui.ts';

export const RiderApp={setup(){
 const tab=ref('站点大厅'),tasks=ref<LobbyTask[]>([]),history=ref<LobbyTask[]>([]),earnings=ref<Earning[]>([]),messages=ref<Message[]>([]),current=ref<RiderTask>(),available=ref<boolean>(),cash=ref(false),reason=ref('CUSTOMER_REFUSED'),incomeDate=ref(today()),lastUpdated=ref(''),polling=ref(false);
 let timer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|undefined,generation=0,failures=0,disposed=false;
 const s=session(['RIDER'],refresh);
 const testLogin=localTestLogin('rider',s);
 watch(()=>sessionKey(s.me.value),(value,prior)=>{if(value!==prior){invalidate();tasks.value=[];history.value=[];earnings.value=[];messages.value=[];current.value=undefined;available.value=undefined;cash.value=false;lastUpdated.value='';}},{flush:'sync'});
 function invalidate(){generation++;controller?.abort();controller=undefined;clearTimeout(timer);polling.value=false;}
 function schedule(){clearTimeout(timer);if(disposed||document.hidden||!s.me.value)return;timer=setTimeout(()=>{void poll();},Math.min(60000,10000*2**Math.min(failures,3)));}
 async function snapshot(signal:AbortSignal){
  const identity=await s.get<Me>('/me',signal);if(!identity.roles.includes('RIDER'))throw new ApiError(403,'FORBIDDEN','骑手权限已撤销');
  const [hall,past,income,mail]=await Promise.all([s.list<LobbyTaskList>('/rider/tasks?limit=100',signal),s.list<LobbyTaskList>('/rider/history?limit=100',signal),s.list<EarningList>(`/rider/earnings?date=${incomeDate.value}&limit=100`,signal),s.list<MessageList>('/rider/messages?limit=100',signal)]);
  const active=hall.items.find(t=>['AWAITING_PICKUP','IN_DELIVERY','RETURNING'].includes(t.order_status));
  const detail=active?await s.get<RiderTask>(`/rider/tasks/${id(active.id)}`,signal):undefined;
  return {identity,hall,past,income,mail,detail};
 }
 async function poll(force=false){if(polling.value||disposed||document.hidden||(!force&&s.busy.value)||!s.me.value){schedule();return;}const token=++generation;const actor=sessionKey(s.me.value);controller=new AbortController();polling.value=true;
  try{const result=await snapshot(controller.signal);if(token!==generation||disposed||document.hidden||sessionKey(s.me.value)!==actor){if(force)throw new DOMException('刷新已失效','AbortError');return;}s.me.value=result.identity;tasks.value=result.hall.items;history.value=result.past.items;earnings.value=result.income.items;messages.value=result.mail.items;
   if(!s.pending.value){const same=current.value?.id===result.detail?.id&&current.value?.order_status===result.detail?.order_status;current.value=result.detail;if(!same)cash.value=false;}lastUpdated.value=new Date().toISOString();failures=0;
  }catch(e){if(token===generation&&!(e instanceof DOMException&&e.name==='AbortError')){failures++;if(e instanceof ApiError&&(e.status===401||e.status===403)){current.value=undefined;tasks.value=[];history.value=[];earnings.value=[];messages.value=[];s.me.value=undefined;s.ready.value=false;s.failure(e);}}if(force)throw e;}
  finally{if(token===generation){polling.value=false;controller=undefined;schedule();}}
 }
 async function refresh(){if(document.hidden)throw new Error('页面已隐藏，请回到当前页面后核对服务器记录。');invalidate();await poll(true);}
 async function command(action:'claim'|'pickup'|'deliver'|'return',taskId:string,body?:DeliverInput|{reason_code:string}){
  invalidate();cash.value=false;await s.write<RiderTask>(`/rider/tasks/${id(taskId)}/${action}`,body,async v=>{current.value=v;tab.value='当前任务';s.notice.value=`服务器已确认：${action==='claim'?'接单':action==='pickup'?'取货':action==='deliver'?'送达':'拒付退回'}。`;await refresh();});
  if(s.error.value&&!s.pending.value){try{await refresh();}catch(e){s.failure(e);}}schedule();
 }
 async function setAvailability(value:boolean){invalidate();await s.write<Availability>('/rider/availability',{available:value},async v=>{available.value=v.available;s.notice.value=v.available?'服务器已确认上线':'服务器已确认下线';await refresh();},'PUT');schedule();}
 function hallTask(t:LobbyTask){return card(t.building_name,[h('p',{class:'muted'},`任务 ${t.id} · ${t.item_count} 件商品`),badge(t.order_status),h('p',`服务窗口 ${date(t.slot.starts_at)} — ${date(t.slot.ends_at)}`),h('p',`配送费 ${money(t.fee_fen)} · 完成后以收入记录为准`),h('div',{class:'actions'},[t.order_status==='READY_FOR_CLAIM'?btn('接单',()=>command('claim',t.id),s.busy.value||!!s.pending.value||available.value===false,`claim-${t.id}`,'primary'):btn('查看当前任务',()=>s.run(async()=>{invalidate();current.value=await s.get<RiderTask>(`/rider/tasks/${id(t.id)}`);cash.value=false;tab.value='当前任务';schedule();}),s.busy.value)])]);}
 function detail(){const t=current.value;if(!t)return empty('当前没有进行中的任务，请到站点大厅刷新。');return card('当前任务详情',[h('p',`任务 ${t.id}`),badge(t.order_status),h('p',`${t.building_name} · ${t.item_count} 件 · 配送费 ${money(t.fee_fen)}`),h('p',`服务窗口 ${date(t.slot.starts_at)} — ${date(t.slot.ends_at)}`),t.address?h('div',{'data-sensitive':true,class:'note'},[h('b',`${t.address.building_name} ${t.address.room}`),h('p',t.address.contact_name),h('a',{href:`tel:${t.address.contact_phone}`},t.address.contact_phone)]):h('p','此状态下服务器未授权查看详细地址或电话。'),h('p',{class:'muted'},'任务契约未返回付款方式和应收金额。请核对站点交接凭据，不按配送费收取货款。'),
 t.order_status==='IN_DELIVERY'?check('已全额收到货到付款',cash.value,v=>cash.value=v):null,
 ['IN_DELIVERY','RETURNING'].includes(t.order_status)?field('拒付 / 退回原因码',reason.value,v=>reason.value=v):null,
 h('div',{class:'actions'},[
 btn('确认取货',()=>command('pickup',t.id),s.busy.value||t.order_status!=='AWAITING_PICKUP'||!!s.pending.value,'pickup','primary'),
 btn('确认货到付款送达',()=>command('deliver',t.id,{cod_received:true}),s.busy.value||t.order_status!=='IN_DELIVERY'||!cash.value||!!s.pending.value,'deliver','primary'),
 btn('线上已付款订单送达',()=>command('deliver',t.id,{}),s.busy.value||t.order_status!=='IN_DELIVERY'||!!s.pending.value,'deliver-prepaid'),
 btn('拒付并退回',()=>command('return',t.id,{reason_code:reason.value}),s.busy.value||t.order_status!=='IN_DELIVERY'||!reason.value||!!s.pending.value,'return','red')]),t.order_status==='RETURNING'?h('p','请将商品交回站点，由运营验退后结束任务。'):null],{'data-testid':'current-task'});}
 async function markRead(ids:string[]){const remaining=ids.filter(k=>messages.value.some(m=>m.id===k&&!m.read_at));const chunk=remaining.slice(0,100);if(!chunk.length){s.notice.value='全部消息已由服务器标记为已读。';return;}let accepted=false;invalidate();await s.write('/rider/messages/read',{ids:chunk},async()=>{accepted=true;for(const m of messages.value)if(chunk.includes(m.id))m.read_at=new Date().toISOString();if(remaining.length>100)s.notice.value=`已确认 ${chunk.length} 条，继续处理其余 ${remaining.length-chunk.length} 条。`;else s.notice.value='消息已由服务器标记为已读。';});if(accepted&&remaining.length>100&&!s.pending.value)await markRead(remaining.slice(100));else schedule();}
 function messagesView(){return card('站内消息',[...messages.value.map(m=>h('article',{class:'item'},[h('b',m.kind==='ORDER_URGE'?'用户催单':m.kind==='ORDER_OVERDUE'?'订单逾期提醒':m.kind),h('p',`任务 ${m.task_id} · ${date(m.created_at)}`),h('p',m.read_at?`已读 ${date(m.read_at)}`:'未读'),!m.read_at?btn('标为已读',()=>markRead([m.id]),s.busy.value,`read-${m.id}`):null])),!messages.value.length?empty('暂无站内消息'):null,btn('全部标为已读',()=>markRead(messages.value.filter(m=>!m.read_at).map(m=>m.id)),s.busy.value||!!s.pending.value||!messages.value.some(m=>!m.read_at),'read-all')]);}
 function pastView(){return h('div',{class:'split'},[card('本人配送历史',[...history.value.map(t=>h('article',{class:'item'},[h('b',t.building_name),h('p',`任务 ${t.id} · ${t.item_count} 件`),badge(t.order_status),h('p',date(t.slot.starts_at))])),!history.value.length?empty('暂无配送历史'):null]),card('实际收入记录',[field('收入日期',incomeDate.value,v=>{incomeDate.value=v;void s.run(refresh);},'date'),h('p',{class:'price'},money(earnings.value.reduce((a,e)=>a+e.amount_fen,0))),h('p',{class:'muted'},'仅汇总服务器返回的本日收入记录，不含预计收益。'),...earnings.value.map(e=>h('article',{class:'item'},[h('b',money(e.amount_fen)),h('p',`任务 ${e.task_id} · ${date(e.recorded_at)}`)])),!earnings.value.length?empty('本日暂无收入记录'):null])]);}
 const visibility=()=>{if(document.hidden)invalidate();else void s.run(refresh);};
 onMounted(async()=>{document.addEventListener('visibilitychange',visibility);await s.recover();schedule();});
 onUnmounted(()=>{disposed=true;invalidate();document.removeEventListener('visibilitychange',visibility);});
 return ()=>shell('rider','把每一程，稳稳送好',h('div',{class:'actions'},['站点大厅','当前任务','消息','历史与收入'].map(t=>btn(t,()=>tab.value=t,false,undefined,tab.value===t?'active':''))),[
 testLogin(),!s.me.value?card('员工登录与权限恢复',[h('p','请通过当前骑手入口的员工登录恢复会话。失效或停用的会话不能继续领取或履约。'),h('a',{href:'/api/v1/auth/staff/start'},'前往员工登录')]):null,
 s.me.value?h('div',{class:'note'},[h('h2',available.value===undefined?'接单状态待确认':available.value?'已上线，准备出发':'已下线'),h('p',available.value===undefined?'契约没有当前在线状态读取接口，可向服务器确认上线或下线。':'状态来自最近一次服务器确认。'),h('div',{class:'actions'},[btn('上线接单',()=>setAvailability(true),s.busy.value,'go-online','primary'),btn('下线休息',()=>setAvailability(false),s.busy.value,'go-offline'),btn('刷新任务',()=>s.run(refresh),s.busy.value||polling.value,'refresh-tasks')]),h('p',{class:'muted'},lastUpdated.value?`最近刷新 ${date(lastUpdated.value)} · 页面隐藏时暂停轮询`:'等待服务器数据'),h('p',{class:'muted'},'工作窗口以每个任务的服务器时段显示。独立排班接口尚未提供。'),btn('设置工作时段',()=>{},true)]):null,
 s.me.value?(tab.value==='站点大厅'?(tasks.value.length?h('div',{class:'grid'},tasks.value.map(hallTask)):empty('大厅暂无可接任务')):tab.value==='当前任务'?detail():tab.value==='消息'?messagesView():pastView()):null
 ],s.tools(),s.status());
}};
export function mount(root:HTMLElement){assertNoProductionMock(import.meta.env.PROD,import.meta.env.VITE_ENABLE_MOCK);return createApp(RiderApp).mount(root);}
if(document.querySelector<HTMLElement>('#app')?.dataset.entry==='rider')mount(document.querySelector<HTMLElement>('#app')!);
export default RiderApp;
