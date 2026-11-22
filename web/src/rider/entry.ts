import { createApp, computed, h, nextTick, onMounted, onUnmounted, ref, watch, type Component, type VNodeChild } from 'vue';
import {
  ArrowLeft, Banknote, BriefcaseBusiness, ChevronDown, ChevronRight, ClipboardList, MapPin, MessageCircle,
  Package, RefreshCw, Search, Trophy, User,
} from '@lucide/vue';
import { ApiError, assertNoProductionMock } from '../shared/api.ts';
import type {
  Availability, DeliverInput, Earning, EarningList, LobbyTask, LobbyTaskList, Me, Message, MessageList,
  OrderBatchResult, RiderCapacity, RiderLeaderboard, RiderTask, Slot,
} from '../shared/generated.ts';
import { moneyAmount } from '../shared/money.ts';
import { date, field, localTestLogin, session, sessionKey, stateText, today, id } from '../user/ui.ts';
import './app.css';

// 任务大厅、待取货、配送中三段共用一次 /rider/tasks 读取结果。
const activeStates: LobbyTask['order_status'][] = ['AWAITING_PICKUP', 'IN_DELIVERY', 'RETURNING'];
const deliveringStates: LobbyTask['order_status'][] = ['IN_DELIVERY', 'RETURNING'];
// 货到付款必须逐单确认收款，只有线上已付款的配送中任务能批量送达。
const prepaidInDelivery = (t: LobbyTask) => t.payment_method === 'BALANCE' && t.order_status === 'IN_DELIVERY';
const hm = (value: string) => new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
const slotSpan = (slot: Slot) => `${hm(slot.starts_at)}–${hm(slot.ends_at)}`;
const yuan = (fen: number) => `¥${moneyAmount(fen)}`;
const same = (list: string[], key: string) => list.includes(key);
const icon = (component: Component, size = 18, strokeWidth = 1.9) => h(component, { size, strokeWidth, 'aria-hidden': 'true' });

type Tab = 'orders' | 'messages' | 'me';
type Segment = 'hall' | 'pickup' | 'delivering';
type SubPage = '' | 'wallet' | 'history' | 'ranking';
type Sort = 'slot' | 'fee';

export const RiderApp = { setup() {
 const tab = ref<Tab>('orders'), page = ref<SubPage>(''), segment = ref<Segment>('hall');
 const tasks = ref<LobbyTask[]>([]), history = ref<LobbyTask[]>([]), earnings = ref<Earning[]>([]), messages = ref<Message[]>([]);
 const current = ref<RiderTask>(), available = ref<boolean>(), cash = ref(false), reason = ref('CUSTOMER_REFUSED');
 const incomeDate = ref(today()), lastUpdated = ref(''), polling = ref(false);
 const capacity = ref<RiderCapacity>(), leaderboard = ref<RiderLeaderboard>();
 const paymentFilter = ref('ALL'), buildingFilter = ref('ALL'), sortBy = ref<Sort>('slot'), query = ref('');
 const claimSelection = ref<string[]>([]), pickupSelection = ref<string[]>([]), deliverySelection = ref<string[]>([]);
 const statusOpen = ref(false);
 let timer: ReturnType<typeof setTimeout> | undefined, controller: AbortController | undefined, generation = 0, failures = 0, disposed = false;
 const s = session(['RIDER'], refresh, error => {
  if (error.code === 'ORDER_LIMIT_REACHED') return '所选订单超过剩余接单名额，请处理手上的订单或减少选择。';
  if (error.code === 'COD_CLAIM_EXCLUSIVE') return '货到付款订单需单独配送，请先完成手上的订单。';
  if (error.code === 'COD_BATCH_DELIVERY_FORBIDDEN') return '货到付款订单需逐单确认收款后送达。';
 });
 const testLogin = localTestLogin('rider', s);
 const busy = () => s.busy.value || !!s.pending.value;
 const remaining = () => capacity.value?.remaining ?? 0;
 const unread = computed(() => messages.value.filter(m => !m.read_at).length);
 const todayOrders = computed(() => earnings.value.length);
 const todayIncome = computed(() => earnings.value.reduce((sum, item) => sum + item.amount_fen, 0));
 const myRank = computed(() => leaderboard.value?.entries?.find(entry => entry.me)?.rank);

 watch(() => sessionKey(s.me.value), (value, prior) => {
  if (value === prior) return;
  invalidate();
  tasks.value = []; history.value = []; earnings.value = []; messages.value = []; current.value = undefined;
  available.value = undefined; cash.value = false; lastUpdated.value = ''; capacity.value = undefined; leaderboard.value = undefined;
  claimSelection.value = []; pickupSelection.value = []; deliverySelection.value = []; paymentFilter.value = 'ALL';
  buildingFilter.value = 'ALL'; query.value = ''; page.value = ''; segment.value = 'hall';
 }, { flush: 'sync' });

 function invalidate() { generation++; controller?.abort(); controller = undefined; clearTimeout(timer); polling.value = false; }
 function schedule() { clearTimeout(timer); if (disposed || document.hidden || !s.me.value) return; timer = setTimeout(() => { void poll(); }, Math.min(60000, 10000 * 2 ** Math.min(failures, 3))); }
 async function snapshot(signal: AbortSignal) {
  const identity = await s.get<Me>('/me', signal);
  if (!identity.roles.includes('RIDER')) throw new ApiError(403, 'FORBIDDEN', '骑手权限已撤销');
  const [hall, past, income, mail, slots, avail] = await Promise.all([
   s.list<LobbyTaskList>('/rider/tasks?limit=100', signal), s.list<LobbyTaskList>('/rider/history?limit=100', signal),
   s.list<EarningList>(`/rider/earnings?date=${incomeDate.value}&limit=100`, signal), s.list<MessageList>('/rider/messages?limit=100', signal),
   s.get<RiderCapacity>('/rider/capacity', signal), s.get<Availability>('/rider/availability', signal),
  ]);
  const active = hall.items.filter(t => activeStates.includes(t.order_status));
  const focus = active.find(t => t.id === current.value?.id)
   ?? active.filter(t => t.order_status !== 'RETURNING').sort((a, b) => Date.parse(a.slot.ends_at) - Date.parse(b.slot.ends_at))[0]
   ?? active[0];
  const detail = focus ? await s.get<RiderTask>(`/rider/tasks/${id(focus.id)}`, signal) : undefined;
  return { identity, hall, past, income, mail, detail, slots, avail };
 }
 async function poll(force = false) {
  if (polling.value || disposed || document.hidden || (!force && s.busy.value) || !s.me.value) { schedule(); return; }
  const token = ++generation, actor = sessionKey(s.me.value);
  controller = new AbortController(); polling.value = true;
  try {
   const result = await snapshot(controller.signal);
   if (token !== generation || disposed || document.hidden || sessionKey(s.me.value) !== actor) {
    if (force) throw new DOMException('刷新已失效', 'AbortError');
    return;
   }
   s.me.value = result.identity;
   tasks.value = result.hall.items; history.value = result.past.items; earnings.value = result.income.items; messages.value = result.mail.items;
   capacity.value = result.slots;
   available.value = result.avail?.available;
   claimSelection.value = claimSelection.value.filter(key => tasks.value.some(t => t.id === key && t.order_status === 'READY_FOR_CLAIM'));
   pickupSelection.value = pickupSelection.value.filter(key => tasks.value.some(t => t.id === key && t.order_status === 'AWAITING_PICKUP'));
   deliverySelection.value = deliverySelection.value.filter(key => tasks.value.some(t => t.id === key && prepaidInDelivery(t)));
   if (!s.pending.value) {
    const unchanged = current.value?.id === result.detail?.id && current.value?.order_status === result.detail?.order_status;
    current.value = result.detail;
    if (!unchanged) cash.value = false;
   }
   lastUpdated.value = new Date().toISOString(); failures = 0;
  } catch (e) {
   if (token === generation && !(e instanceof DOMException && e.name === 'AbortError')) {
    failures++;
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
     current.value = undefined; tasks.value = []; history.value = []; earnings.value = []; messages.value = [];
     s.me.value = undefined; s.ready.value = false; s.failure(e);
    }
    if (force) throw e;
   }
  } finally {
   if (token === generation) { polling.value = false; controller = undefined; schedule(); }
  }
 }
 async function refresh() { if (document.hidden) throw new Error('页面已隐藏，请回到当前页面后核对服务器记录。'); invalidate(); await poll(true); }
 async function loadLeaderboard() {
  await s.run(async () => { leaderboard.value = await s.get<RiderLeaderboard>('/rider/leaderboard'); });
 }
 async function command(action: 'claim' | 'pickup' | 'deliver' | 'return', taskId: string, body?: DeliverInput | { reason_code: string }) {
  invalidate(); cash.value = false;
  await s.write<RiderTask>(`/rider/tasks/${id(taskId)}/${action}`, body, async v => {
   s.notice.value = `服务器已确认：${action === 'claim' ? '接单' : action === 'pickup' ? '取货' : action === 'deliver' ? '送达' : '拒付退回'}。`;
   if (action === 'deliver') {
    await advanceAfterDelivery([taskId]);
   } else {
    current.value = v;
    if (action === 'claim') segment.value = 'pickup';
    if (action === 'pickup') segment.value = 'delivering';
    await refresh();
   }
  });
  if (s.error.value && !s.pending.value) { try { await refresh(); } catch (e) { s.failure(e); } }
  schedule();
 }
 async function focusTaskDetail() { await nextTick(); document.getElementById('rider-task-detail')?.focus(); }
 async function advanceAfterDelivery(completedIds: string[]) {
  current.value = undefined;
  cash.value = false;
  tasks.value = tasks.value.filter(t => !completedIds.includes(t.id));
  deliverySelection.value = deliverySelection.value.filter(key => !completedIds.includes(key));
  pickupSelection.value = pickupSelection.value.filter(key => !completedIds.includes(key));
  if (segment.value !== 'delivering') segment.value = 'delivering';
  if (paymentFilter.value !== 'ALL') paymentFilter.value = 'ALL';
  await refresh();
  await focusTaskDetail();
  void loadLeaderboard();
 }
 async function runTaskBatch(action: 'claim' | 'deliver') {
  const selection = action === 'claim' ? claimSelection : deliverySelection;
  const ids = [...selection.value];
  invalidate();
  await s.write<OrderBatchResult>(`/rider/task-batches/${action}`, { ids }, async result => {
   selection.value = [];
   if (action === 'deliver') {
    await advanceAfterDelivery(ids);
   } else {
    segment.value = 'pickup';
    await refresh();
   }
   s.notice.value = `已${action === 'claim' ? '接单' : '送达'} ${result.count} 单。`;
  });
  if (s.error.value && !s.pending.value) { try { await refresh(); } catch (error) { s.failure(error); } }
  schedule();
 }
 // 契约里没有批量取货命令，这里按顺序逐单调用 /pickup，服务器逐单确认。
 async function runPickupBatch() {
  const ids = [...pickupSelection.value];
  if (!ids.length || busy()) return;
  invalidate();
  let confirmed = 0;
  for (const key of ids) {
   if (s.pending.value || s.error.value) break;
   await s.write<RiderTask>(`/rider/tasks/${id(key)}/pickup`, undefined, async v => {
    confirmed++;
    current.value = v;
    pickupSelection.value = pickupSelection.value.filter(item => item !== key);
   });
  }
  if (confirmed) {
   segment.value = 'delivering';
   s.notice.value = `已确认取货 ${confirmed} 单。`;
  }
  try { await refresh(); } catch (error) { s.failure(error); }
  schedule();
 }
 async function openTask(task: LobbyTask) {
  await s.run(async () => {
   invalidate();
   try {
    current.value = await s.get<RiderTask>(`/rider/tasks/${id(task.id)}`);
    cash.value = false;
    await focusTaskDetail();
   } finally { schedule(); }
  });
 }
 async function setAvailability(value: boolean) {
  invalidate();
  await s.write<Availability>('/rider/availability', { available: value }, async v => {
   available.value = v.available;
   statusOpen.value = false;
   s.notice.value = v.available ? '服务器已确认上线' : '服务器已确认下线';
   await refresh();
  }, 'PUT');
  schedule();
 }
 async function markRead(keys: string[]) {
  const pending = keys.filter(key => messages.value.some(m => m.id === key && !m.read_at));
  const chunk = pending.slice(0, 100);
  if (!chunk.length) { s.notice.value = '全部消息已由服务器标记为已读。'; return; }
  let accepted = false;
  invalidate();
  await s.write('/rider/messages/read', { ids: chunk }, async () => {
   accepted = true;
   for (const m of messages.value) if (chunk.includes(m.id)) m.read_at = new Date().toISOString();
   if (pending.length > 100) s.notice.value = `已确认 ${chunk.length} 条，继续处理其余 ${pending.length - chunk.length} 条。`;
   else s.notice.value = '消息已由服务器标记为已读。';
  });
  if (accepted && pending.length > 100 && !s.pending.value) await markRead(pending.slice(100));
  else schedule();
 }
 function navigate(next: Tab) { tab.value = next; closePage(); if (next === 'me' && !leaderboard.value) void loadLeaderboard(); }
 function openPage(next: Exclude<SubPage, ''>) { page.value = next; if (next === 'ranking') void loadLeaderboard(); }
 // 钱包可以回看历史日期，离开时恢复成今日，避免「今日单量」跟着旧日期变化。
 function closePage() {
  page.value = '';
  if (incomeDate.value !== today()) { incomeDate.value = today(); void s.run(refresh); }
 }
 function switchSegment(next: Segment) { segment.value = next; statusOpen.value = false; }
 function clearSelections() { claimSelection.value = []; pickupSelection.value = []; deliverySelection.value = []; }
 function logout() { void s.write('/auth/logout', undefined, () => { s.me.value = undefined; s.ready.value = false; s.notice.value = '已退出登录'; }); }

 const selectionOf = (t: LobbyTask) => t.order_status === 'READY_FOR_CLAIM' ? claimSelection : t.order_status === 'AWAITING_PICKUP' ? pickupSelection : deliverySelection;
 const segmentList = (which: Segment) => {
  const source = which === 'hall' ? tasks.value.filter(t => t.order_status === 'READY_FOR_CLAIM')
   : which === 'pickup' ? tasks.value.filter(t => t.order_status === 'AWAITING_PICKUP')
   : tasks.value.filter(t => deliveringStates.includes(t.order_status));
  const needle = query.value.trim().toLowerCase();
  const list = source
   .filter(t => paymentFilter.value === 'ALL' || t.payment_method === paymentFilter.value)
   .filter(t => buildingFilter.value === 'ALL' || t.building_name === buildingFilter.value)
   .filter(t => !needle || t.building_name.toLowerCase().includes(needle) || t.id.toLowerCase().includes(needle));
  return list.sort((a, b) => sortBy.value === 'fee' ? b.fee_fen - a.fee_fen : Date.parse(a.slot.ends_at) - Date.parse(b.slot.ends_at));
 };
 const segmentCount = (which: Segment) => which === 'hall' ? tasks.value.filter(t => t.order_status === 'READY_FOR_CLAIM').length
  : which === 'pickup' ? tasks.value.filter(t => t.order_status === 'AWAITING_PICKUP').length
  : tasks.value.filter(t => deliveringStates.includes(t.order_status)).length;
 const buildings = () => ['ALL', ...new Set(tasks.value.map(t => t.building_name))];
 const workStateText = () => available.value === true ? '工作中' : available.value === false ? '休息中' : '状态待确认';
 const canClaim = (t: LobbyTask) => !busy() && available.value !== false && remaining() > 0 && (t.payment_method !== 'COD' || (capacity.value?.active ?? 0) === 0);
 const selectable = (t: LobbyTask) => t.order_status === 'READY_FOR_CLAIM' ? t.payment_method !== 'COD' : t.order_status === 'AWAITING_PICKUP' ? true : prepaidInDelivery(t);

 /* ---------- 通用控件 ---------- */
 function chip(kind: 'cod' | 'prepaid' | 'muted', label: string) { return h('span', { class: ['r-chip', kind] }, label); }
 function checkbox(label: string, checked: boolean, change: (value: boolean) => void, disabled = false) {
  return h('label', { class: ['r-check', disabled ? 'disabled' : ''] }, [
   h('input', { type: 'checkbox', checked, disabled, onChange: (event: Event) => change((event.target as HTMLInputElement).checked) }),
   label,
  ]);
 }
 function compactSelect(label: string, value: string, options: Array<[string, string]>, change: (value: string) => void) {
  return h('label', { class: 'r-select' }, [
   h('select', {
    value, 'aria-label': label,
    onChange: (event: Event) => change((event.target as HTMLSelectElement).value),
   }, options.map(([key, text]) => h('option', { value: key }, text))),
   h(ChevronDown, { size: 12, strokeWidth: 2.4, 'aria-hidden': 'true' }),
  ]);
 }
 function iconButton(component: Component, label: string, onClick: () => unknown, testid?: string, disabled = false) {
  return h('button', { type: 'button', class: 'r-icon-btn', 'aria-label': label, 'data-testid': testid, disabled, onClick }, [icon(component, 19)]);
 }
 function emptyState(title: string, hint: string, action?: VNodeChild) {
  return h('div', { class: 'r-empty' }, [
   h('div', { class: 'r-empty-art', 'aria-hidden': 'true' }, [h('div', { innerHTML: emptyArt })]),
   h('p', { class: 'r-empty-title' }, title),
   hint ? h('p', { class: 'r-empty-sub' }, hint) : null,
   action ?? null,
  ]);
 }
 // 参考图的空态插画：暖色沙丘 + 橙色票据。
 const emptyArt = `<svg viewBox="0 0 170 120" width="170" height="120" fill="none" xmlns="http://www.w3.org/2000/svg">
  <path d="M14 92c14-12 34-19 71-19s57 7 71 19c-14 11-34 17-71 17s-57-6-71-17z" fill="#fdf2e2"/>
  <ellipse cx="85" cy="88" rx="46" ry="12" fill="#fbe6c8"/>
  <rect x="10" y="70" width="4" height="16" rx="2" fill="#f2d9b4"/>
  <circle cx="12" cy="68" r="7" fill="#f5e2c4"/>
  <rect x="146" y="66" width="4" height="20" rx="2" fill="#f2d9b4"/>
  <circle cx="148" cy="63" r="8" fill="#f7e7cf"/>
  <path d="M63 44c0-5 4-9 9-9h34c5 0 9 4 9 9v9a5 5 0 0 0 0 10v9c0 5-4 9-9 9H72c-5 0-9-4-9-9V63a5 5 0 0 0 0-10v-9z" fill="#f5a623"/>
  <path d="M63 44c0-5 4-9 9-9h34c5 0 9 4 9 9v9a5 5 0 0 0 0 10v9c0 5-4 9-9 9H72c-5 0-9-4-9-9V63a5 5 0 0 0 0-10v-9z" fill="url(#g)" fill-opacity=".35"/>
  <path d="M112 57a8 8 0 0 1 0 14v-14z" fill="#fdf2e2"/>
  <path d="M76 47h26M76 54h18M76 70h26M76 77h14" stroke="#fff8ec" stroke-width="3" stroke-linecap="round"/>
  <path d="M124 30l7-6M130 40l9-2M118 25l2-8" stroke="#f0703c" stroke-width="3" stroke-linecap="round"/>
  <defs><linearGradient id="g" x1="63" y1="35" x2="115" y2="90" gradientUnits="userSpaceOnUse"><stop stop-color="#fff"/><stop offset="1" stop-color="#f5a623" stop-opacity="0"/></linearGradient></defs>
 </svg>`;

 /* ---------- 订单页 ---------- */
 function topBar() {
  return h('div', { class: 'r-top' }, [
   h('div', { class: 'r-top-row' }, [
    h('div', { class: 'r-place' }, [
     h(MapPin, { size: 18, strokeWidth: 2.1, 'aria-hidden': 'true' }),
     h('span', { class: 'r-place-name' }, '校园配送站'),
    ]),
    h('div', { class: 'r-top-actions' }, [
     capacity.value ? h('span', { class: 'r-capacity', 'data-testid': 'rider-capacity' }, `手上 ${capacity.value.active}/${capacity.value.limit}`) : null,
     h('button', { type: 'button', class: 'r-icon-btn', 'aria-label': workStateText(), onClick: () => navigate('me') }, [icon(User, 19)]),
    ]),
   ]),
   h('div', { class: 'r-seg', role: 'group', 'aria-label': '订单分段' }, (['hall', 'pickup', 'delivering'] as Segment[]).map(key => {
    const labels = { hall: '任务大厅', pickup: '待取货', delivering: '配送中' } as const;
    const count = segmentCount(key);
    return h('button', {
     type: 'button', 'aria-pressed': segment.value === key, 'aria-label': labels[key],
     class: ['r-seg-btn', segment.value === key ? 'active' : ''], 'data-testid': `seg-${key}`,
     onClick: () => switchSegment(key),
    }, [labels[key], count ? h('span', { class: 'r-seg-count', 'aria-hidden': 'true' }, count) : null]);
   })),
  ]);
 }
 function batchAction() {
  if (segment.value === 'hall') {
   return { label: '批量抢单', testid: 'rider-batch-claim', disabled: busy() || available.value === false || !claimSelection.value.length || claimSelection.value.length > remaining(), run: () => runTaskBatch('claim') };
  }
  if (segment.value === 'pickup') {
   return { label: '批量取货', testid: 'rider-batch-pickup', disabled: busy() || !pickupSelection.value.length, run: () => runPickupBatch() };
  }
  return { label: '批量送达', testid: 'rider-batch-deliver', disabled: busy() || !deliverySelection.value.length, run: () => runTaskBatch('deliver') };
 }
 function filterRow() {
  const batch = batchAction();
  return h('div', { class: 'r-filters' }, [
   compactSelect('楼宇', buildingFilter.value, buildings().map(name => [name, name === 'ALL' ? '全部楼宇' : name]), value => { buildingFilter.value = value; }),
   compactSelect('付款方式', paymentFilter.value, [['ALL', '全部付款'], ['COD', '货到付款'], ['BALANCE', '用户已付款']], value => { paymentFilter.value = value; clearSelections(); }),
   compactSelect('排序', sortBy.value, [['slot', '时段优先'], ['fee', '配送费高']], value => { sortBy.value = value as Sort; }),
   h('span', { class: 'r-filter-spacer' }),
   h('button', { type: 'button', class: 'r-filter-btn', 'data-testid': batch.testid, disabled: batch.disabled, onClick: batch.run }, batch.label),
  ]);
 }
 function searchRow() {
  return h('div', { class: 'r-search' }, [
   h('span', { class: 'r-search-scope' }, '楼宇'),
   h('span', { class: 'r-search-div' }),
   h(Search, { size: 16, strokeWidth: 1.9, 'aria-hidden': 'true' }),
   h('input', {
    type: 'search', value: query.value, 'aria-label': '搜索楼宇或任务号', placeholder: '请输入楼宇或任务号',
    onInput: (event: Event) => { query.value = (event.target as HTMLInputElement).value; },
   }),
  ]);
 }
 function selectionHint() {
  const selected = segment.value === 'hall' ? claimSelection.value.length : segment.value === 'pickup' ? pickupSelection.value.length : deliverySelection.value.length;
  const notes: string[] = [];
  if (selected) notes.push(`已选 ${selected} 单`);
  if (segment.value === 'hall') notes.push(capacity.value?.cod_active ? '有货到付款订单时需先完成手上的配送' : '货到付款订单需单独接单');
  if (!notes.length) return null;
  return h('p', { class: 'r-note-line', 'aria-live': 'polite' }, notes.join(' · '));
 }
 function taskCard(t: LobbyTask) {
  const selection = selectionOf(t);
  const selected = same(selection.value, t.id);
  const inHall = t.order_status === 'READY_FOR_CLAIM';
  const selectableTask = selectable(t);
  const claimable = canClaim(t);
  const disabledSelect = busy() || (!selected && inHall && (!claimable || selection.value.length >= remaining()));
  const action = inHall
   ? h('button', { type: 'button', class: ['r-btn', 'primary'], 'data-testid': `claim-${t.id}`, disabled: !claimable, onClick: () => command('claim', t.id) }, '接单')
   : h('button', {
    type: 'button', class: ['r-btn', t.id === current.value?.id ? 'primary' : 'ghost'], 'data-testid': `task-${t.id}`,
    disabled: busy(), onClick: () => openTask(t),
   }, t.order_status === 'AWAITING_PICKUP' ? '去取货' : t.order_status === 'RETURNING' ? '查看退回' : '去送达');
  return h('article', { class: ['r-task', t.payment_method === 'COD' ? 'cod' : ''], 'data-testid': `rider-task-${t.id}` }, [
   h('div', { class: 'r-task-head' }, [
    h('span', { class: 'r-task-title' }, t.building_name),
    chip(t.payment_method === 'COD' ? 'cod' : 'prepaid', t.payment_method === 'COD' ? '货到付款' : '用户已付款'),
   ]),
   h('div', { class: 'r-task-meta' }, [
    h('span', {}, `任务 ${t.id}`),
    h('span', {}, `${t.item_count} 件`),
    h('span', {}, slotSpan(t.slot)),
    h('span', {}, stateText(t.order_status)),
   ]),
   h('div', { class: 'r-task-foot' }, [
    h('span', { class: 'r-fee' }, ['配送费 ', h('b', {}, yuan(t.fee_fen))]),
    selectableTask ? checkbox(inHall ? '选择接单' : t.order_status === 'AWAITING_PICKUP' ? '选择取货' : '选择送达', selected, value => {
     const next = value ? [...selection.value, t.id] : selection.value.filter(key => key !== t.id);
     selection.value = next;
    }, disabledSelect) : null,
    action,
   ]),
  ]);
 }
 function detailCard() {
  const t = current.value;
  if (!t) return null;
  const fits = segment.value === 'pickup' ? t.order_status === 'AWAITING_PICKUP' : segment.value === 'delivering' && deliveringStates.includes(t.order_status);
  if (!fits) return null;
  return h('section', {
   class: 'r-card r-detail', id: 'rider-task-detail', tabindex: -1, 'data-testid': 'current-task',
  }, [
   h('h2', {}, '当前任务'),
   h('div', { class: 'r-task-head' }, [h('span', { class: 'r-task-title' }, `任务 ${t.id}`), chip(t.payment_method === 'COD' ? 'cod' : 'prepaid', t.payment_method === 'COD' ? '货到付款' : '用户已付款')]),
   h('p', {}, `${stateText(t.order_status)} · ${t.building_name} · ${t.item_count} 件 · 配送费 ${yuan(t.fee_fen)}`),
   h('p', {}, `服务窗口 ${slotSpan(t.slot)}（${date(t.slot.starts_at)} 起）`),
   t.address ? h('div', { class: 'r-address', 'data-sensitive': true }, [
    h('b', {}, `${t.address.building_name} ${t.address.room}`),
    h('p', {}, [`${t.address.contact_name} · `, h('a', { href: `tel:${t.address.contact_phone}` }, t.address.contact_phone)]),
   ]) : h('p', { class: 'r-hint' }, '此状态下服务器未授权查看详细地址或电话。'),
   t.cod_due_fen !== undefined ? h('p', { class: 'r-address' }, ['货到付款应收 ', h('b', {}, yuan(t.cod_due_fen))]) : null,
   t.order_status === 'IN_DELIVERY' && t.cod_due_fen !== undefined ? checkbox('已全额收到货到付款', cash.value, value => { cash.value = value; }) : null,
   ['IN_DELIVERY', 'RETURNING'].includes(t.order_status) ? field('拒付 / 退回原因码', reason.value, value => { reason.value = value; }) : null,
   h('div', { class: 'r-detail-actions' }, [
    t.order_status === 'AWAITING_PICKUP' ? h('button', { type: 'button', class: 'r-btn primary', 'data-testid': 'pickup', disabled: busy(), onClick: () => command('pickup', t.id) }, '确认取货') : null,
    t.order_status === 'IN_DELIVERY' && t.cod_due_fen !== undefined ? h('button', { type: 'button', class: 'r-btn primary', 'data-testid': 'deliver', disabled: busy() || !cash.value || !!s.pending.value, onClick: () => command('deliver', t.id, { cod_received: true }) }, '确认货到付款送达') : null,
    t.order_status === 'IN_DELIVERY' && t.payment_method === 'BALANCE' ? h('button', { type: 'button', class: 'r-btn primary', 'data-testid': 'deliver-prepaid', disabled: busy() || !!s.pending.value, onClick: () => command('deliver', t.id, {}) }, '线上已付款订单送达') : null,
    t.order_status === 'IN_DELIVERY' ? h('button', { type: 'button', class: 'r-btn danger', 'data-testid': 'return', disabled: busy() || !reason.value || !!s.pending.value, onClick: () => command('return', t.id, { reason_code: reason.value }) }, '拒付并退回') : null,
   ]),
   t.order_status === 'RETURNING' ? h('p', { class: 'r-hint' }, '请将商品交回站点，由运营验退后结束任务。') : null,
  ]);
 }
 function ordersView(): { head: VNodeChild; body: VNodeChild } {
  const list = segmentList(segment.value);
  const labels = { hall: '任务大厅', pickup: '待取货', delivering: '配送中' } as const;
  return {
   head: [h('h1', { class: 'sr-only' }, `订单 · ${labels[segment.value]}`), topBar()],
   body: h('div', {}, [
    filterRow(),
    searchRow(),
    selectionHint(),
    detailCard(),
    list.length
     ? h('div', { class: 'r-list' }, list.map(taskCard))
     : emptyState(
      segment.value === 'hall' ? '暂时没有发现订单哦～' : segment.value === 'pickup' ? '暂时没有待取货任务～' : '暂时没有配送中任务～',
      segment.value === 'hall' ? '换个楼宇或付款方式再试试' : '手上的任务都在任务大厅排好了队',
      h('button', { type: 'button', class: 'r-btn primary', onClick: () => s.run(refresh), disabled: busy() }, '刷新列表'),
     ),
    h('p', { class: 'r-foot-note' }, lastUpdated.value ? `最近刷新 ${date(lastUpdated.value)} · 页面隐藏时暂停轮询` : '正在与服务器同步任务'),
   ]),
  };
 }

 /* ---------- 消息 ---------- */
 function messagesView(): { head: VNodeChild; body: VNodeChild } {
  return {
   head: h('div', { class: 'r-sub-head' }, [
    h('h1', { class: 'r-sub-title' }, '消息'),
    unread.value ? h('span', { class: 'r-capacity' }, `${unread.value} 条未读`) : null,
    h('button', { type: 'button', class: 'r-btn sm', 'data-testid': 'read-all', disabled: s.busy.value || !!s.pending.value || !unread.value, onClick: () => markRead(messages.value.filter(m => !m.read_at).map(m => m.id)) }, '全部已读'),
   ]),
   body: h('div', {}, [
    messages.value.length ? h('div', {}, messages.value.map(m => h('article', { class: ['r-msg', m.read_at ? '' : 'unread'] }, [
     h('div', { class: 'r-msg-top' }, [
      h('span', { class: 'r-msg-kind' }, m.kind === 'ORDER_URGE' ? '用户催单' : m.kind === 'ORDER_OVERDUE' ? '订单逾期提醒' : m.kind),
      h('span', { class: 'r-msg-meta' }, date(m.created_at)),
     ]),
     h('p', { class: 'r-msg-meta' }, `任务 ${m.task_id} · ${m.read_at ? `已读 ${date(m.read_at)}` : '未读'}`),
     m.read_at ? null : h('button', { type: 'button', class: 'r-btn sm ghost', 'data-testid': `read-${m.id}`, disabled: s.busy.value, onClick: () => markRead([m.id]) }, '标为已读'),
    ]))) : emptyState('暂时没有站内消息', '催单和逾期提醒会出现在这里'),
   ]),
  };
 }

 /* ---------- 我的 ---------- */
 function meRow(options: { icon: Component; label: string; value?: VNodeChild; onClick?: () => unknown; testid?: string }) {
  return h('button', { type: 'button', class: 'r-row', 'data-testid': options.testid, onClick: options.onClick }, [
   h('span', { class: 'r-tile' }, [icon(options.icon, 20)]),
   h('span', { class: 'r-row-label' }, options.label),
   options.value ? h('span', { class: 'r-row-value' }, [options.value]) : null,
   h(ChevronRight, { size: 17, strokeWidth: 2, class: 'r-row-chev', 'aria-hidden': 'true' }),
  ]);
 }
 function stateLabel() {
  const on = available.value === true;
  return h('span', { class: ['r-state', on ? 'on' : ''] }, [h('span', { class: 'r-state-dot', 'aria-hidden': 'true' }), workStateText()]);
 }
 function meView(): { head: VNodeChild; body: VNodeChild } {
  return {
   head: h('div', { class: 'r-me-head' }, [
    h('div', { class: 'r-me-top' }, [
     h('div', {}, [h('h1', { class: 'r-me-title' }, '我的'), h('p', { class: 'r-me-sub' }, available.value ? '在线接单中，注意按时送达' : available.value === false ? '休息中，随时可以重新上线' : '正在确认接单状态')]),
     h('div', { class: 'r-avatar', 'aria-hidden': 'true' }, [icon(User, 30, 1.7)]),
    ]),
   ]),
   body: h('div', { class: 'r-sheet' }, [
    h('div', { class: 'r-stats' }, [
     h('div', { class: 'r-stat' }, [h('div', { class: 'r-stat-value' }, todayOrders.value), h('div', { class: 'r-stat-label' }, '今日单量')]),
     h('div', { class: 'r-stat' }, [h('div', { class: 'r-stat-value' }, yuan(todayIncome.value)), h('div', { class: 'r-stat-label' }, '今日收入')]),
    ]),
    h('div', { class: 'r-menu' }, [
     meRow({ icon: BriefcaseBusiness, label: '工作状态', value: stateLabel(), testid: 'me-work-status', onClick: () => { statusOpen.value = !statusOpen.value; } }),
     statusOpen.value ? h('div', { class: 'r-menu-detail' }, [
      h('p', { class: 'r-hint' }, available.value ? '上线后服务器会即时派单；下线后不再接收新的任务。' : '当前处于休息状态，上线后才能接单。'),
      h('div', { class: 'r-state-actions' }, [
       h('button', { type: 'button', class: 'r-btn primary sm', 'data-testid': 'go-online', disabled: s.busy.value, onClick: () => setAvailability(true) }, '上线接单'),
       h('button', { type: 'button', class: 'r-btn sm', 'data-testid': 'go-offline', disabled: s.busy.value, onClick: () => setAvailability(false) }, '下线休息'),
      ]),
     ]) : null,
     meRow({ icon: Banknote, label: '钱包', value: `今日 ${yuan(todayIncome.value)}`, testid: 'me-wallet', onClick: () => openPage('wallet') }),
     meRow({ icon: ClipboardList, label: '订单', value: `进行中 ${tasks.value.filter(t => activeStates.includes(t.order_status)).length}`, testid: 'me-orders', onClick: () => openPage('history') }),
     meRow({ icon: Trophy, label: '排行榜', value: myRank.value ? `第 ${myRank.value} 名` : '骑手单量榜', testid: 'me-ranking', onClick: () => openPage('ranking') }),
    ]),
    h('div', { class: 'r-account' }, [
     h('p', { class: 'r-account-meta' }, `站点 ${s.me.value?.station_id ?? ''} · ${s.me.value?.roles.join(' / ') ?? ''}`),
     h('div', { class: 'r-account-actions' }, [
      h('button', { type: 'button', class: 'r-link', 'data-testid': 'recover-session', disabled: s.busy.value, onClick: () => s.recover(true) }, '恢复会话'),
      h('button', { type: 'button', class: 'r-link', disabled: s.busy.value, onClick: logout }, '退出登录'),
      h('a', { class: 'r-link', href: '/api/v1/auth/staff/start' }, '员工登录 / MFA'),
     ]),
    ]),
    h('p', { class: 'r-foot-note' }, '校零智选 · 时间按 Asia/Shanghai 显示 · 订单状态以服务器为准'),
   ]),
  };
 }

 /* ---------- 排行榜 ---------- */
 function rankingView(): { head: VNodeChild; body: VNodeChild } {
  const entries = Array.isArray(leaderboard.value?.entries) ? leaderboard.value!.entries : [];
  const mine = entries.find(entry => entry.me);
  return {
   head: subHead('排行榜', iconButton(RefreshCw, '刷新排行榜', () => s.run(loadLeaderboard), 'ranking-refresh', s.busy.value)),
   body: h('div', {}, [
    h('div', { class: 'r-podium' }, [
     h('div', { class: 'r-podium-label' }, '我的今日排名'),
     h('div', { class: 'r-podium-rank' }, mine ? `第 ${mine.rank} 名` : entries.length ? '未上榜' : '待上榜'),
     h('div', { class: 'r-podium-meta' }, [
      h('span', {}, `今日单量 ${todayOrders.value}`),
      h('span', {}, `榜单人数 ${entries.length}`),
      h('span', {}, leaderboard.value?.local_date ?? today()),
     ]),
    ]),
    entries.length
     ? h('div', { class: 'r-rank-list' }, entries.map(entry => h('div', { class: ['r-rank-row', entry.me ? 'me' : ''] }, [
      h('span', { class: ['r-rank-no', entry.rank === 1 ? 'gold' : entry.rank === 2 ? 'silver' : entry.rank === 3 ? 'bronze' : ''] }, entry.rank),
      h('span', { class: 'r-rank-name' }, [h('span', {}, entry.display_name), entry.me ? h('span', { class: 'r-rank-me' }, '我') : null]),
      h('span', { class: 'r-rank-orders' }, [entry.orders, h('small', {}, '单')]),
     ])))
     : emptyState('今天还没有骑手完成配送', '完成第一单就能出现在榜单上', h('button', { type: 'button', class: 'r-btn primary', disabled: s.busy.value, onClick: () => s.run(loadLeaderboard) }, '刷新榜单')),
    h('p', { class: 'r-hint' }, '按今日服务器确认的送达单量排名，同一站点内可见；收入与提现以钱包记录为准。'),
   ]),
  };
 }

 /* ---------- 钱包 ---------- */
 function walletView(): { head: VNodeChild; body: VNodeChild } {
  return {
   head: subHead('钱包', null),
   body: h('div', {}, [
    h('div', { class: 'r-wallet-top' }, [
     h('div', { class: 'r-wallet-label' }, `${incomeDate.value} 收入`),
     h('div', { class: 'r-wallet-value' }, yuan(todayIncome.value)),
     h('div', { class: 'r-wallet-meta' }, [h('span', {}, `单量 ${todayOrders.value}`), h('span', {}, '含货到付款提成')]),
    ]),
    h('div', { class: 'r-date-row' }, [
     h('label', { for: 'income-date' }, '收入日期'),
     h('input', { id: 'income-date', type: 'date', value: incomeDate.value, 'aria-label': '收入日期', onChange: (event: Event) => { incomeDate.value = (event.target as HTMLInputElement).value; void s.run(refresh); } }),
    ]),
    earnings.value.length
     ? h('div', { class: 'r-rank-list' }, earnings.value.map(item => h('div', { class: 'r-earn' }, [
      h('span', { class: 'r-tile' }, [icon(Package, 19)]),
      h('span', { class: 'r-earn-main' }, [
       h('span', { class: 'r-earn-title' }, `任务 ${item.task_id}`),
       h('span', { class: 'r-earn-sub' }, `${date(item.recorded_at)}${item.commission_fen !== undefined ? ` · 含货款提成 ${yuan(item.commission_fen)}` : ''}`),
      ]),
      h('span', { class: 'r-earn-amount' }, yuan(item.amount_fen)),
     ])))
     : emptyState('这天还没有收入记录', '收入以服务器确认的送达记录为准'),
    h('p', { class: 'r-hint' }, '钱包只展示服务器返回的骑手收入记录，不包含预计收益；提现由平台结算流程处理。'),
   ]),
  };
 }

 /* ---------- 我的订单 ---------- */
 function historyView(): { head: VNodeChild; body: VNodeChild } {
  const list = history.value.filter(t => paymentFilter.value === 'ALL' || t.payment_method === paymentFilter.value);
  return {
   head: subHead('我的订单', null),
   body: h('div', {}, [
    h('div', { class: 'r-filters' }, [
     compactSelect('付款方式', paymentFilter.value, [['ALL', '全部付款'], ['COD', '货到付款'], ['BALANCE', '用户已付款']], value => { paymentFilter.value = value; }),
     h('span', { class: 'r-filter-spacer' }),
     h('span', { class: 'r-row-value' }, `共 ${list.length} 单`),
    ]),
    list.length
     ? h('div', { class: 'r-list' }, list.map(t => h('article', { class: 'r-task' }, [
      h('div', { class: 'r-task-head' }, [
       h('span', { class: 'r-task-title' }, t.building_name),
       chip(t.payment_method === 'COD' ? 'cod' : 'prepaid', t.payment_method === 'COD' ? '货到付款' : '用户已付款'),
      ]),
      h('div', { class: 'r-task-meta' }, [h('span', {}, `任务 ${t.id}`), h('span', {}, `${t.item_count} 件`), h('span', {}, date(t.slot.starts_at)), h('span', {}, stateText(t.order_status))]),
      h('div', { class: 'r-task-foot' }, [h('span', { class: 'r-fee' }, ['配送费 ', h('b', {}, yuan(t.fee_fen))]), h('span', { class: 'r-row-value' }, date(t.slot.ends_at))]),
     ])))
     : emptyState('还没有配送记录', '完成配送后订单会出现在这里'),
   ]),
  };
 }

 /* ---------- 子页头 ---------- */
 function subHead(title: string, extra: VNodeChild) {
  return h('div', { class: 'r-sub-head' }, [
   h('button', { type: 'button', class: 'r-icon-btn', 'aria-label': '返回', onClick: closePage }, [icon(ArrowLeft, 21, 2)]),
   h('h1', { class: 'r-sub-title' }, title),
   extra,
  ]);
 }

 /* ---------- 外壳 ---------- */
 function tabbar() {
  const tabs: Array<[Tab, string, Component, number]> = [['orders', '订单', ClipboardList, 0], ['messages', '消息', MessageCircle, unread.value], ['me', '我的', User, 0]];
  return h('nav', { class: 'r-tabbar', 'aria-label': '底部导航' }, tabs.map(([key, label, component, badge]) => h('button', {
   type: 'button', class: ['r-tab', tab.value === key && !page.value ? 'active' : ''], 'aria-label': label, 'data-testid': `nav-${key}`,
   'aria-current': tab.value === key && !page.value ? 'page' : undefined,
   onClick: () => navigate(key),
  }, [
   h('span', { class: 'r-tab-icon' }, [icon(component, 21, 1.9), badge ? h('span', { class: 'r-tab-dot', 'aria-hidden': 'true' }, badge > 99 ? '99+' : badge) : null]),
   label,
  ])));
 }
 function toast() {
  if (available.value !== false) return null;
  return h('div', { class: 'r-toast', role: 'status' }, ['您已经休息啦～', h('button', { type: 'button', onClick: () => setAvailability(true) }, '去上线')]);
 }
 function fab() {
  return h('button', { type: 'button', class: 'r-fab', 'aria-label': '刷新列表', 'data-testid': 'refresh-tasks', disabled: busy(), onClick: () => s.run(refresh) }, [icon(RefreshCw, 22, 2)]);
 }
 function loginView() {
  return h('main', { 'data-entry': 'rider', class: ['r-app', 'r-login'] }, [
   h('div', { class: 'r-login-hero' }, [h('span', { class: 'r-login-logo' }, '零'), h('h1', {}, '骑手端'), h('p', {}, '把每一程，稳稳送好')]),
   h('div', { class: 'r-login-sheet' }, [
    testLogin(),
    s.status(),
    h('div', { class: 'card' }, [
     h('h2', {}, '员工登录与权限恢复'),
     h('p', {}, '请通过当前骑手入口的员工登录恢复会话。失效或停用的会话不能继续领取或履约。'),
     h('a', { href: '/api/v1/auth/staff/start' }, '前往员工登录 / MFA'),
    ]),
    h('p', { class: 'r-foot-note' }, '校零智选 · 校园配送'),
   ]),
  ]);
 }

 const visibility = () => { if (document.hidden) invalidate(); else void s.run(refresh); };
 onMounted(async () => {
  document.addEventListener('visibilitychange', visibility);
  await s.recover();
  schedule();
  if (s.me.value) void loadLeaderboard();
 });
 onUnmounted(() => { disposed = true; invalidate(); document.removeEventListener('visibilitychange', visibility); });

 return () => {
  if (!s.me.value) return loginView();
  const view = page.value === 'wallet' ? walletView() : page.value === 'ranking' ? rankingView() : page.value === 'history' ? historyView()
   : tab.value === 'messages' ? messagesView() : tab.value === 'me' ? meView() : ordersView();
  return h('main', { 'data-entry': 'rider', class: 'r-app' }, [
   h('span', { class: 'sr-only', 'data-testid': 'session-ready' }, `站点 ${s.me.value.station_id} · ${s.me.value.roles.join(' / ')}`),
   view.head,
   s.status(),
   view.body,
   tabbar(),
   toast(),
   page.value ? null : fab(),
  ]);
 };
}};
export function mount(root: HTMLElement) { assertNoProductionMock(import.meta.env.PROD, import.meta.env.VITE_ENABLE_MOCK); return createApp(RiderApp).mount(root); }
if (document.querySelector<HTMLElement>('#app')?.dataset.entry === 'rider') mount(document.querySelector<HTMLElement>('#app')!);
export default RiderApp;
