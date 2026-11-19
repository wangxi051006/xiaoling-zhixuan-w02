import { h, ref, watch, type VNodeChild } from 'vue';
import { apiRequest, ApiError } from '../shared/api.ts';
import type { Me, Role } from '../shared/generated.ts';
import './style.css';

export { money } from '../shared/money.ts';
export const date=(value:string)=>new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date(value));
export const today=()=>new Intl.DateTimeFormat('sv-SE',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
export const dayAfter=(days:number)=>{const d=new Date(`${today()}T12:00:00+08:00`);d.setUTCDate(d.getUTCDate()+days);return new Intl.DateTimeFormat('sv-SE',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(d);};
export const id=(value:string)=>encodeURIComponent(value);
export const sessionKey=(me:Me|undefined)=>me?`${me.id}|${me.station_id}|${[...me.roles].sort().join(',')}`:'';
export const text=(event:Event)=>(event.target as HTMLInputElement).value;
export const checked=(event:Event)=>(event.target as HTMLInputElement).checked;
export const labels:Record<string,string>={SCHEDULED:'预约待备货',PREPARING:'备货中',READY_FOR_CLAIM:'待骑手接单',AWAITING_PICKUP:'待取货',IN_DELIVERY:'配送中',DELIVERED:'已送达',CANCELED:'已取消',RETURNING:'退回中',RETURNED:'已退回',UNSORTED:'未分拣',SORTED:'已分拣',RESTOCK_PENDING:'待归架',RESTOCKED:'已归架',COD_UNCOLLECTED:'货到待收',COD_COLLECTED:'货到已收',BALANCE_PAID:'余额已扣',VOID:'已作废',REFUND_PENDING:'退款待处理',REFUNDED:'已退款',NOT_REMITTED:'未上缴',IN_BATCH:'批次待复点',VERIFIED:'已复点',DISCREPANCY:'复点有差异'};
export const stateText=(s:string)=>labels[s]||s;
export const btn=(label:string,fn:()=>unknown,disabled=false,testid?:string,kind='')=>h('button',{type:'button',onClick:fn,disabled,'data-testid':testid,class:`btn ${kind}`},label);
export const field=(label:string,value:string|number,onChange:(v:string)=>void,type='text',props:Record<string,unknown>={})=>h('label',{class:'field'},[h('span',label),h('input',{type,value,onInput:(e:Event)=>onChange(text(e)),...props})]);
export const select=(label:string,value:string,options:Array<[string,string,boolean?]>,change:(v:string)=>void)=>h('label',{class:'field','data-sensitive':label==='配送地址'?true:undefined},[h('span',label),h('select',{value,'aria-label':label,onChange:(e:Event)=>change(text(e))},options.map(([v,t,disabled])=>h('option',{value:v,disabled},t)))]);
export const check=(label:string,value:boolean,change:(v:boolean)=>void)=>h('label',{class:'check'},[h('input',{type:'checkbox',checked:value,onChange:(e:Event)=>change(checked(e))}),label]);
export const card=(title:string,children:VNodeChild[],props:Record<string,unknown>={})=>h('section',{class:'card',...props},[h('h2',title),...children]);
export const empty=(label='暂无记录')=>h('p',{class:'empty'},label);
export const badge=(s:string)=>h('span',{class:'badge'},stateText(s));
export const form=(children:VNodeChild[],submit:()=>unknown)=>h('form',{onSubmit:(e:Event)=>{e.preventDefault();void submit();}},children);

type Pending={path:string;method:'POST'|'PUT'|'PATCH'|'DELETE';body:unknown;key:string;actor:string;done:(result:any)=>unknown;checked:boolean};
export function session(roles:Role[],refresh:()=>Promise<unknown>){
 const me=ref<Me>();const error=ref('');const notice=ref('');const busy=ref(false);const pending=ref<Pending>();const ready=ref(false);let epoch=0;
 watch(()=>sessionKey(me.value),(value,prior)=>{if(value!==prior){epoch++;pending.value=undefined;}},{flush:'sync'});
 watch(()=>me.value?.csrf_token,(value,prior)=>{if(value&&prior&&value!==prior)epoch++;},{flush:'sync'});
 function failure(e:unknown){
   if(e instanceof DOMException&&e.name==='AbortError')return;
   if(e instanceof ApiError){error.value=`${e.code}：${e.message}${e.requestId?`（请求 ${e.requestId}）`:''}`;if(e.status===401){me.value=undefined;ready.value=false;pending.value=undefined;error.value+='。会话已失效，请重新登录。';}if(e.status===403)error.value+='。操作被拒绝，请检查角色或通过员工登录完成近期 MFA。';}
   else error.value='网络连接中断或结果未知，请核对服务器状态后重试。';
 }
 async function get<T>(path:string,signal?:AbortSignal):Promise<T>{const token=epoch,controller=new AbortController();const timeout=setTimeout(()=>controller.abort(new DOMException('请求超时，结果未知','TimeoutError')),15000);try{const value=await apiRequest<T>(path,{signal:signal?AbortSignal.any([signal,controller.signal]):controller.signal});if(token!==epoch)throw new DOMException('会话已改变，忽略旧响应','AbortError');return value;}catch(e){if(token!==epoch)throw new DOMException('会话已改变，忽略旧错误','AbortError');if(!(e instanceof DOMException&&e.name==='AbortError'))failure(e);throw e;}finally{clearTimeout(timeout);}}
 async function list<T extends {items:unknown[];next_cursor:string|null}>(path:string,signal?:AbortSignal):Promise<T>{const first=await get<T>(path,signal);const items=[...first.items];const seen=new Set<string>();let cursor=first.next_cursor;while(cursor){if(seen.has(cursor))throw new ApiError(500,'INTERNAL_ERROR','列表游标重复，请刷新重试');seen.add(cursor);const next=new URL(path,'https://local.invalid');next.searchParams.set('cursor',cursor);const page=await get<T>(next.pathname+next.search,signal);items.push(...page.items);cursor=page.next_cursor;}return {...first,items,next_cursor:null} as T;}
 async function recover(showAuthError=false){error.value='';try{const result=await get<Me>('/me');if(!result.roles.some(r=>roles.includes(r))){me.value=undefined;error.value='FORBIDDEN：此账号没有当前入口所需权限。';return;}me.value=result;ready.value=true;await refresh();}catch(e){if(!showAuthError&&e instanceof ApiError&&e.status===401)error.value='';}}
 async function run(fn:()=>Promise<unknown>){if(busy.value)return;const token=epoch;busy.value=true;error.value='';notice.value='';try{await fn();}catch(e){if(token===epoch)failure(e);}finally{busy.value=false;}}
 async function execute(p:Pending){let result:unknown;const token=epoch,controller=new AbortController();const timeout=setTimeout(()=>controller.abort(new DOMException('请求超时，结果未知','TimeoutError')),15000);try{result=await apiRequest(p.path,{method:p.method,body:p.body,csrf:me.value?.csrf_token,idempotencyKey:p.key,signal:controller.signal});if(sessionKey(me.value)!==p.actor)throw new DOMException('会话已改变，忽略旧响应','AbortError');}catch(e){if(sessionKey(me.value)!==p.actor)throw new DOMException('会话已改变，忽略旧错误','AbortError');const ambiguous=!(e instanceof ApiError)||e.status>=500||(e.status>=200&&e.status<300);if(ambiguous&&!(e instanceof DOMException&&e.name==='AbortError')){pending.value=p;p.checked=false;}else if(token===epoch)pending.value=undefined;if(token!==epoch)throw new DOMException('凭据已更新，保留待核对操作并忽略旧凭据错误','AbortError');throw e;}finally{clearTimeout(timeout);}pending.value=undefined;await p.done(result);}
 async function write<T>(path:string,body:unknown,done:(result:T)=>unknown,method:Pending['method']='POST'){
  if(pending.value){error.value='请先核对并处理上一笔结果未知的操作。';return;}
  if(!me.value){error.value='请先恢复会话或登录。';return;}
  const actor=sessionKey(me.value);await run(()=>execute({path,body:body===undefined?undefined:JSON.parse(JSON.stringify(body)),method,key:crypto.randomUUID(),actor,done,checked:false}));
 }
 function status(){return h('div',{class:'feedback'},[
  error.value?h('p',{role:'alert',class:'error'},error.value):null,notice.value?h('p',{role:'status'},notice.value):null,
  busy.value?h('p',{role:'status'},'正在与服务器核对…'):null,
  pending.value?card('操作结果待核对',[h('p','请求可能已经完成。先查看服务器记录，再决定是否重试；重试保留原请求和幂等键。'),btn('核对服务器记录',()=>run(async()=>{await refresh();if(pending.value)pending.value.checked=true;notice.value='已刷新服务器记录，请核对当前状态。';}),busy.value,'check-uncertain'),btn('重试原操作',()=>run(()=>execute(pending.value!)),busy.value||!pending.value.checked,'retry-uncertain')]):null
 ]);}
 function tools(){return h('div',{class:'session'},[me.value?h('span',{'data-testid':'session-ready'},`站点 ${me.value.station_id} · ${me.value.roles.join(' / ')}`):h('span','尚未登录'),btn('恢复会话',()=>recover(true),busy.value,'recover-session'),me.value?btn('退出登录',()=>write('/auth/logout',undefined,()=>{me.value=undefined;ready.value=false;notice.value='已退出登录';}),busy.value):null,h('a',{href:'/api/v1/auth/staff/start'},'员工登录 / MFA')]);}
 return {me,error,notice,busy,pending,ready,get,list,recover,run,write,failure,status,tools};
}
export function shell(role:string,title:string,tabs:VNodeChild,body:VNodeChild,tools:VNodeChild,status:VNodeChild){return h('main',{'data-entry':role,class:`app ${role}`},[h('header',{class:'masthead'},[h('div',{class:'brand'},[h('span',{class:'logo','aria-hidden':'true'},'零'),h('div',[h('small','校零智选 · 校园生活手账'),h('h1',title)])]),tools]),h('nav',{'aria-label':'主要功能',class:'tabs'},[tabs]),status,h('div',{class:'content'},[body]),h('footer','校零智选 · 时间按 Asia/Shanghai 显示 · 订单状态以服务器为准')]);}
