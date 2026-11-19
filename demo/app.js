/* 校零智选 · 交互演示内核 */
'use strict';

var $ = function (s, r) { return (r || document).querySelector(s); };
var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };

var CATS = ['全部', '冰饮', '零食', '速食', '文具', '水果', '日用'];

function seedProducts() {
  return [
    { id: 'p1', name: '脆浪 海盐薯片', spec: '68g / 袋 · 常温', price: 6.5, old: 8, tag: '第二件 3 元', cat: '零食', tone: 't1', stock: 34, safe: 20, shelf: true, desc: '薄切海盐味，宿舍追剧第一选择。买两袋按 3 元一袋结算。', sold: 326, reviews: [['林', '味道很正，薯片没碎'], ['周', '第二次买了，配气泡水刚好']] },
    { id: 'p2', name: '冰屿 白桃气泡水', spec: '480ml / 瓶 · 冰镇 4℃', price: 4.0, old: 0, tag: '冰镇', cat: '冰饮', tone: 't2', stock: 6, safe: 18, shelf: true, promo: { type: 'multi', start: todayISO(), end: addDaysISO(todayISO(), 2), tiers: [{ min: 1, off: 0.95 }, { min: 2, off: 0.8 }] }, desc: '0 糖 0 卡，冰柜直取。整箱下单可以备注送到宿舍门口。', sold: 512, reviews: [['陈', '冰的，很爽'], ['吴', '气很足，晚上写作业刚好']] },
    { id: 'p3', name: '夜猫 红烧牛肉面', spec: '桶装 112g · 附叉子', price: 5.5, old: 0, tag: '送叉子', cat: '速食', tone: 't3', stock: 22, safe: 15, shelf: true, desc: '经典红烧口味，桶装附叉子，热水一泡就能吃。', sold: 288, reviews: [['赵', '汤挺浓，肉块有存在感']] },
    { id: 'p4', name: '一支 中性笔 0.5', spec: '黑色 / 3 支装', price: 2.0, old: 0, tag: '文具区', cat: '文具', tone: 't4', stock: 40, safe: 10, shelf: true, desc: '考试周常备，顺滑不断墨，3 支一组。', sold: 96, reviews: [['孙', '便宜好用']] },
    { id: 'p5', name: '速溶 黑咖啡 3 条', spec: '15g × 3 · 无糖', price: 7.9, old: 9.9, tag: '自习必备', cat: '零食', tone: 't5', stock: 9, safe: 12, shelf: true, desc: '冷热水都可冲，自习室续命装。', sold: 174, reviews: [['钱', '苦得刚刚好']] },
    { id: 'p6', name: '南孚 5 号电池', spec: '2 节装 · 碱性', price: 6.0, old: 0, tag: '日用', cat: '日用', tone: 't6', stock: 25, safe: 10, shelf: true, desc: '台灯、鼠标、耳机都能用，宿舍常备。', sold: 58, reviews: [] },
    { id: 'p7', name: '青柠 苏打饼干', spec: '200g / 袋', price: 5.0, old: 0, tag: '清淡', cat: '零食', tone: 't1', stock: 18, safe: 12, shelf: true, desc: '微咸带青柠香，不腻。', sold: 143, reviews: [] },
    { id: 'p8', name: '鲜丰 砂糖橘', spec: '500g / 袋 · 当季', price: 9.9, old: 0, tag: '当季', cat: '水果', tone: 't5', stock: 12, safe: 10, shelf: true, desc: '当季现摘，皮薄好剥，一袋约 8 个。', sold: 87, reviews: [] }
  ];
}
var PRODUCTS = seedProducts();

function seedOrders() {
  return [
    {
      no: 'A2381', status: '配送中', items: [{ id: 'p1', qty: 2, unit: 6.5 }, { id: 'p2', qty: 1, unit: 4.0 }], addr: '北苑 3 号楼 508',
      time: '20:04', rider: '李海 · 0231', note: '放门口，谢谢', pay: '微信支付', created: '今天 20:04',
      contact: '小满', phone: '13800006621', date: todayISO(),
      logs: [
        { t: '20:04', title: '已下单', desc: '微信支付 · 北苑 3 号楼 508' },
        { t: '20:05', title: '仓库接单', desc: '北苑仓开始备货，共 3 件' },
        { t: '20:09', title: '骑手取货', desc: '李海 · 0231 已取货，配送中' },
        { t: '20:12', title: '配送中', desc: '距你 2.1 km，预计 20:31 送达' }
      ]
    },
    {
      no: 'A2377', status: '已完成', items: [{ id: 'p3', qty: 1, unit: 5.5 }, { id: 'p5', qty: 1, unit: 7.9 }], addr: '北苑 3 号楼 508',
      time: '18:26', rider: '王岩 · 0188', note: '', pay: '微信支付', created: '今天 18:26',
      contact: '小满', phone: '13800006621', date: todayISO(),
      logs: [
        { t: '18:26', title: '已下单', desc: '微信支付 · 北苑 3 号楼 508' },
        { t: '18:27', title: '仓库接单', desc: '北苑仓开始备货，共 2 件' },
        { t: '18:33', title: '骑手取货', desc: '王岩 · 0188 已取货' },
        { t: '18:51', title: '已送达', desc: '放在门口，买家已确认收货' }
      ]
    },
    {
      no: 'A2360', status: '已完成', items: [{ id: 'p4', qty: 1, unit: 2.0 }, { id: 'p7', qty: 2, unit: 5.0 }],
      addr: '学苑 5 号楼 214', time: '12:20', rider: '李海 · 0231', note: '', pay: '微信支付', created: '昨天 12:20',
      contact: '陈可', phone: '13900001111', date: addDaysISO(todayISO(), -1),
      logs: [
        { t: '12:20', title: '已下单', desc: '微信支付 · 学苑 5 号楼 214' },
        { t: '12:22', title: '仓库接单', desc: '北苑仓开始备货' },
        { t: '12:29', title: '已取货', desc: '骑手 李海 已取货' },
        { t: '12:44', title: '已送达', desc: '本人签收' }
      ]
    },
    {
      no: 'A2341', status: '已完成', items: [{ id: 'p2', qty: 2, unit: 4.0 }],
      addr: '北苑 1 号楼 302', time: '19:40', rider: '李海 · 0231', note: '放门口', pay: '货到付款', created: '前天 19:40',
      contact: '周舟', phone: '13700002222', date: addDaysISO(todayISO(), -2),
      logs: [
        { t: '19:40', title: '已下单', desc: '货到付款 · 北苑 1 号楼 302' },
        { t: '19:43', title: '仓库接单', desc: '北苑仓开始备货' },
        { t: '19:50', title: '已取货', desc: '骑手 李海 已取货' },
        { t: '20:05', title: '已送达', desc: '货到付款 ¥8，已收款' }
      ]
    }
  ];
}

function seedTodos() {
  return [
    { t: '冰屿 白桃气泡水 补货 48 瓶', due: '已完成', done: true },
    { t: '#A2377 超时赔付款确认', due: '20:40 前', done: false },
    { t: '北苑站 2 名骑手未上线', due: '联系站长', done: false },
    { t: '夜宵套餐 明天是否续档', due: '库存不足', done: false }
  ];
}
var state = {
  client: 'user',
  route: { user: 'home', delivery: 'hall', admin: 'dash' },
  hist: { user: [], delivery: [], admin: [] },
  search: '',
  cat: '全部',
  cart: [{ id: 'p3', qty: 1, checked: true }],
  pd: { qty: 1, spec: '单件' },
  productId: null,
  checkout: { addrIndex: 0, time: '尽快送达（约 20 分钟）', pay: '微信支付', note: '' },
  orders: seedOrders(),
  orderTab: '全部',
  detailNo: null,
  lastNo: null,
  deliver: {
    online: true,
    active: null,
    done: 12,
    income: 86.4,
    last: Date.now(),
    tick: 0,
    poolIdx: 0,
    tab: 'active',
    q: '',
    range: 'all',
    msgs: [{ t: '20:12', text: '订单 A2381，管理员提醒你尽快配送', type: 'admin', read: false }],
    avail: [
      { id: 'T1', no: 'A2384', addr: '学苑 6 号楼 · 302', km: '0.8 km', items: 4, fee: 7.5, min: 12, tag: '刚下单' },
      { id: 'T2', no: 'A2383', addr: '图书馆东门 · 自提柜', km: '1.3 km', items: 2, fee: 6.0, min: 2, tag: '2 分钟前' },
      { id: 'T3', no: 'A2382', addr: '教师公寓 2 栋 · 1102', km: '1.9 km', items: 6, fee: 9.0, min: 4, tag: '4 分钟前' }
    ],
    records: [
      { t: '19:52', no: 'A2376', fee: 7.5 },
      { t: '19:10', no: 'A2371', fee: 6.0 },
      { t: '18:44', no: 'A2368', fee: 8.5 }
    ]
  },
  addrDraft: { building: '北苑 3 号楼', floor: '5', room: '', contact: '小满', phone: '13800006621' },
  pdraft: { id: null, name: '', price: '', spec: '', cat: '零食', tag: '', desc: '', stock: '20', safe: '10' },
  admin: {
    range: 'today',
    filter: '全部',
    q: '',
    listRange: 'all',
    extra: 0,
    todos: seedTodos(),
    riders: [
      { name: '李海', no: '0231', online: true, done: 12, score: 4.98 },
      { name: '王岩', no: '0188', online: true, done: 9, score: 4.91 },
      { name: '周杰', no: '0264', online: false, done: 0, score: 4.87 },
      { name: '陈可', no: '0312', online: false, done: 0, score: 4.95 }
    ],
    feed: [
      { t: '20:10', dot: 'hot', text: '#A2381 骑手已取货，预计 20:31 送达', tag: '北苑' },
      { t: '20:08', dot: 'on', text: '冰屿气泡水 库存低于安全线，剩 6 件', tag: '补货' },
      { t: '20:02', dot: '', text: '#A2377 超时 8 分钟，已触发赔付', tag: '超时' },
      { t: '19:58', dot: '', text: '北苑站 2 名骑手未上线，已通知站长', tag: '调度' }
    ]
  },
  me: {
    addrs: [
      { name: '北苑 3 号楼 508', sub: '小满 · 138****6621', tag: '宿舍', contact: '小满', phone: '13800006621' },
      { name: '图书馆东门 · 自提柜', sub: '小满 · 138****6621', tag: '自提', contact: '小满', phone: '13800006621' }
    ],
    points: 1280,
    coupons: 3
  }
};

var VIEWS = { user: {}, delivery: {}, admin: {} };

/* ---------------- 工具 ---------------- */
function money(n) {
  var v = Math.round(n * 10) / 10;
  return '¥' + (v % 1 === 0 ? v : v.toFixed(1));
}
function byId(id) { for (var i = 0; i < PRODUCTS.length; i++) if (PRODUCTS[i].id === id) return PRODUCTS[i]; return null; }
function pad2(n) { return ('0' + n).slice(-2); }
function todayISO() { var d = new Date(); return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()); }
function addDaysISO(iso, n) {
  var d = new Date(iso + 'T12:00:00');
  d.setDate(d.getDate() + n);
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate());
}
function round1(n) { return Math.round(n * 10) / 10; }
function promoActive(p) {
  if (!p || !p.promo) return false;
  var t = todayISO();
  return (!p.promo.start || p.promo.start <= t) && (!p.promo.end || p.promo.end >= t);
}
function tierFor(p, qty) {
  if (!promoActive(p)) return null;
  var tiers = p.promo.tiers || [];
  var best = null;
  for (var i = 0; i < tiers.length; i++) {
    if (qty >= tiers[i].min && (!best || tiers[i].min > best.min)) best = tiers[i];
  }
  return best;
}
function packBase(p, pack) { return (pack || 1) === 3 ? round1(p.price * 3 - 0.5) : p.price; }
function unitPrice(p, qty, pack) {
  var base = packBase(p, pack || 1);
  var t = tierFor(p, qty || 1);
  return t ? round1(base * t.off) : base;
}
function lineUnit(line) {
  var p = byId(line.id);
  if (!p) return 0;
  return unitPrice(p, line.qty, line.pack || 1);
}
function offLabel(off) {
  var z = Math.round(off * 100) / 10;
  return (z % 1 === 0 ? String(z) : z.toFixed(1)) + '折';
}
function promoBestTier(p) {
  if (!promoActive(p)) return null;
  return p.promo.tiers.reduce(function (a, b) { return !a || b.off < a.off ? b : a; }, null);
}
function promoLowPrice(p) {
  var t = promoBestTier(p);
  return t ? round1(p.price * t.off) : p.price;
}
function promoList() { return PRODUCTS.filter(function (p) { return promoActive(p) && p.shelf !== false; }); }
function now() {
  var d = new Date();
  return ('0' + d.getHours()).slice(-2) + ':' + ('0' + d.getMinutes()).slice(-2);
}
function pushLog(o, title, desc) {
  if (!o.logs) o.logs = [];
  o.logs.push({ t: now(), title: title, desc: desc || '' });
}
function unreadMsg() { return state.deliver.msgs.filter(function (m) { return !m.read; }).length; }
function updateMsgBadge() {
  var b = document.getElementById('msgBadge');
  if (b) { var n = unreadMsg(); b.textContent = n; b.style.display = n ? 'grid' : 'none'; }
}
function pushMsg(text, type) {
  state.deliver.msgs.unshift({ t: now(), text: text, type: type || 'user', read: false });
  if (state.deliver.msgs.length > 20) state.deliver.msgs.pop();
  if (state.client === 'delivery') {
    updateMsgBadge();
    toast(text);
  }
}
function logsHtml(o) {
  var logs = o.logs || [];
  if (!logs.length) return '<p class="small muted">还没有日志</p>';
  return '<div class="logs">' + logs.map(function (l) {
    return '<div class="log"><time>' + l.t + '</time><span class="dot"></span><span class="ln"></span>' +
      '<div><b>' + l.title + '</b><small>' + (l.desc || '') + '</small></div></div>';
  }).join('') + '</div>';
}
function cartCount() { return state.cart.reduce(function (a, b) { return a + b.qty; }, 0); }
function cartTotal() {
  return state.cart.reduce(function (a, c) { return c.checked ? a + lineUnit(c) * c.qty : a; }, 0);
}
function checkedCount() { return state.cart.reduce(function (a, c) { return c.checked ? a + c.qty : a; }, 0); }
function orderTotal(o) { return o.items.reduce(function (a, it) { return a + (it.unit != null ? it.unit : unitPrice(byId(it.id), it.qty, it.pack || 1)) * it.qty; }, 0); }
function statusMeta(s) {
  var m = {
    '备货中': ['wait', '备货中'],
    '待取货': ['wait', '待取货'],
    '配送中': ['run', '配送中'],
    '已完成': ['done', '已完成'],
    '已取消': ['cancel', '已取消'],
    '已退款': ['cancel', '已退款']
  };
  return m[s] || ['wait', s];
}
function statusbar() {
  var d = new Date();
  var hh = ('0' + d.getHours()).slice(-2), mm = ('0' + d.getMinutes()).slice(-2);
  return '<div class="sb"><span>' + hh + ':' + mm + '</span><span class="sb-r">' +
    '<svg width="17" height="11" viewBox="0 0 17 11" fill="currentColor"><rect x="0" y="7" width="3" height="4"/><rect x="4.5" y="5" width="3" height="6"/><rect x="9" y="2.5" width="3" height="8.5"/><rect x="13.5" y="0" width="3" height="11"/></svg>' +
    '<svg width="15" height="11" viewBox="0 0 15 11" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M1 4a9 9 0 0 1 13 0M3.6 6.6a5.6 5.6 0 0 1 7.8 0"/><circle cx="7.5" cy="9.6" r="1" fill="currentColor" stroke="none"/></svg>' +
    '<svg width="24" height="11" viewBox="0 0 24 11" fill="none" stroke="currentColor" stroke-width="1.2"><rect x="0.6" y="0.6" width="19" height="9.8" rx="2.4"/><rect x="2.4" y="2.4" width="13.6" height="6.2" fill="currentColor"/><path d="M21.6 4v3" stroke-width="1.6"/></svg>' +
    '</span></div>';
}
function homebar() { return '<div class="homebar"></div>'; }
function emptyState(title, text, action) {
  return '<div class="empty"><i><svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M4 8h16v12H4z"/><path d="M8 8V6a4 4 0 0 1 8 0v2"/></svg></i><b>' + title + '</b><p>' + text + '</p>' +
    (action ? '<div style="margin-top:16px">' + action + '</div>' : '') + '</div>';
}
function pill(status) {
  var m = statusMeta(status);
  return '<span class="st ' + m[0] + '">' + m[1] + '</span>';
}
function maskPhone(p) {
  var s = String(p || '');
  return s.length >= 7 ? s.slice(0, 3) + '****' + s.slice(-4) : s;
}
function orderMatches(o, q, range) {
  var s = String(q || '').trim().toLowerCase();
  if (range === 'today' && o.date !== todayISO()) return false;
  if (range === '3d') {
    var d = new Date((o.date || todayISO()) + 'T12:00:00').getTime();
    if (Date.now() - d > 3 * 86400000) return false;
  }
  if (!s) return true;
  var hay = [o.no, o.addr, o.contact, o.phone, o.rider, o.status].join(' ').toLowerCase();
  return hay.indexOf(s) >= 0;
}
function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
  });
}
function tile(p) {
  var onSale = promoActive(p);
  var pr = unitPrice(p, 1);
  var tag = onSale ? ('低至 ' + offLabel(promoBestTier(p).off)) : (p.stock > 0 ? p.tag : '售罄');
  return '<div class="tile ' + p.tone + (onSale ? ' sale' : '') + '" data-act="openP" data-val="' + p.id + '">' +
    '<div class="tile-img"><span></span><u>' + tag + '</u></div>' +
    '<b>' + p.name + '</b><small>' + p.spec + '</small>' +
    '<div class="pr"><span>¥</span><b>' + pr.toFixed(1) + '</b>' + (onSale ? '<s>' + p.price.toFixed(1) + '</s>' : '') +
    '<button class="add" data-act="addCart" data-val="' + p.id + '" aria-label="加入购物袋">+</button></div></div>';
}
function tiles(list) { return list.map(tile).join(''); }
function filtered(keyword, cat) {
  var k = (keyword || '').trim();
  return PRODUCTS.filter(function (p) {
    var okCat = !cat || cat === '全部' || p.cat === cat;
    var okKey = !k || p.name.indexOf(k) >= 0 || p.cat.indexOf(k) >= 0 || p.spec.indexOf(k) >= 0;
    return p.shelf !== false && okCat && okKey;
  });
}

/* ---------------- 渲染 ---------------- */
function render() {
  var c = state.client, r = state.route[c];
  var fn = VIEWS[c] && VIEWS[c][r];
  var screen = $('#screen');
  screen.className = 'screen app-' + c;
  screen.innerHTML = fn ? fn() : '<div class="empty"><b>页面不存在</b></div>';
  var sc = $('.scroll', screen);
  if (sc) sc.scrollTop = 0;
  syncChrome();
  requestAnimationFrame(function () { screen.classList.add('enter'); });
}
function syncChrome() {
  $$('#switcher .sw').forEach(function (b) { b.classList.toggle('on', b.dataset.client === state.client); });
  var hints = {
    user: '试试：搜索商品 → 加购 → 购物车结算 → 提交订单',
    delivery: '试试：抢一单 → 推进取货 / 送达，收入会累加',
    admin: '试试：新增商品 / 改库存 / 下架 → 订单详情看日志时间轴'
  };
  $('#hint').textContent = hints[state.client];
  var tips = {
    user: ['搜索框输入「薯片」「冰饮」会实时过滤', '点商品卡进详情：选规格、改数量', '「+」加入购物袋，购物袋角标会变', '购物车里勾选、加减、删除，金额实时算', '结算页可换地址、选送达时间、选支付方式', '提交后生成订单，可在订单里取消 / 再来一单'],
    delivery: ['顶部「接单中」可切换在线 / 离线', '待抢单点「接单」进入当前任务', '任务页按步骤推进：取货 → 送达', '送达后今日收入累加，可在收入页提现'],
    admin: ['今日 / 本周切换，指标与图表同步变化', '订单可按状态筛选，点开看详情与时间轴', '详情里可派单（推到配送端）/ 退款', '商品页可新增、编辑、改库存、上下架', '骑手页可切换上线状态']
  };
  $('#tips').innerHTML = tips[state.client].map(function (t) { return '<li>' + t + '</li>'; }).join('');
}
function paint(id, html) { var el = document.getElementById(id); if (el) el.innerHTML = html; }
function updateBadge() {
  var b = document.getElementById('bagBadge');
  if (b) { b.textContent = cartCount(); b.style.display = cartCount() ? 'grid' : 'none'; }
}
function toast(msg) {
  var t = $('#toast');
  t.textContent = msg;
  t.classList.add('on');
  clearTimeout(toast._t);
  toast._t = setTimeout(function () { t.classList.remove('on'); }, 1800);
}
function openSheet(html) {
  var s = $('#sheet');
  s.innerHTML = '<div class="grip"></div><button class="sheet-x" data-act="closeSheet" aria-label="关闭">×</button>' + html;
  s.classList.add('on');
  $('#veil').classList.add('on');
  s.scrollTop = 0;
}
function closeSheet() {
  $('#sheet').classList.remove('on');
  $('#veil').classList.remove('on');
}

/* ---------------- 路由 ---------------- */
function go(route) {
  var c = state.client;
  if (state.route[c] !== route) state.hist[c].push(state.route[c]);
  state.route[c] = route;
  render();
}
function tab(route) {
  state.hist[state.client] = [];
  state.route[state.client] = route;
  render();
}
function back() {
  var c = state.client;
  var p = state.hist[c].pop();
  state.route[c] = p || (c === 'user' ? 'home' : c === 'delivery' ? 'hall' : 'dash');
  render();
}
function switchClient(c) {
  state.client = c;
  if (history && history.replaceState) history.replaceState(null, '', '#' + c);
  render();
}

/* ---------------- 行为 ---------------- */
var ACTIONS = {};

ACTIONS.reset = function () {
  state.search = ''; state.cat = '全部';
  state.cart = [{ id: 'p3', qty: 1, checked: true }];
  state.orders = seedOrders();
  state.orderTab = '全部'; state.detailNo = null; state.lastNo = null;
  state.deliver.active = null; state.deliver.online = true; state.deliver.done = 12; state.deliver.income = 86.4;
  state.deliver.last = Date.now(); state.deliver.tick = 0; state.deliver.poolIdx = 0;
  state.deliver.msgs = [{ t: '20:12', text: '订单 A2381，管理员提醒你尽快配送', type: 'admin', read: false }];
  state.deliver.avail = [
    { id: 'T1', no: 'A2384', addr: '学苑 6 号楼 · 302', km: '0.8 km', items: 4, fee: 7.5, min: 12, tag: '刚下单' },
    { id: 'T2', no: 'A2383', addr: '图书馆东门 · 自提柜', km: '1.3 km', items: 2, fee: 6.0, min: 2, tag: '2 分钟前' },
    { id: 'T3', no: 'A2382', addr: '教师公寓 2 栋 · 1102', km: '1.9 km', items: 6, fee: 9.0, min: 4, tag: '4 分钟前' }
  ];
  state.admin.extra = 0; state.admin.range = 'today'; state.admin.filter = '全部';
  state.admin.todos = seedTodos();
  PRODUCTS = seedProducts();
  state.me.addrs = [
    { name: '北苑 3 号楼 508', sub: '小满 · 138****6621', tag: '宿舍', contact: '小满', phone: '13800006621' },
    { name: '图书馆东门 · 自提柜', sub: '小满 · 138****6621', tag: '自提', contact: '小满', phone: '13800006621' }
  ];
  state.checkout = { addrIndex: 0, time: '尽快送达（约 20 分钟）', pay: '微信支付', note: '' };
  state.addrDraft = { building: '北苑 3 号楼', floor: '5', room: '', contact: '小满', phone: '13800006621' };
  state.admin.promoDraft = { id: null, type: 'multi', start: todayISO(), end: addDaysISO(todayISO(), 2), tiers: [{ min: '2', z: '8' }] };
  state.pdraft = { id: null, name: '', price: '', spec: '', cat: '零食', tag: '', desc: '', stock: '20', safe: '10' };
  state.hist = { user: [], delivery: [], admin: [] };
  state.route = { user: 'home', delivery: 'hall', admin: 'dash' };
  closeSheet();
  render();
  toast('演示已重置');
};
ACTIONS.tab = function (r) {
  if (state.client === 'delivery' && r === 'hall') state.deliver.last = Date.now();
  tab(r);
};
ACTIONS.go = function (r) { go(r); };
ACTIONS.back = function () { back(); };
ACTIONS.closeSheet = function () { closeSheet(); };
ACTIONS.soon = function (t) { toast(t || '演示版：这个入口先留个位置'); };

ACTIONS.addCart = function (id) {
  var p = byId(id);
  if (!p) return;
  if (p.shelf === false) { toast('这个商品已下架'); return; }
  var line = null;
  for (var i = 0; i < state.cart.length; i++) if (state.cart[i].id === id && (state.cart[i].pack || 1) === 1) line = state.cart[i];
  var have = line ? line.qty : 0;
  if (have + 1 > p.stock) { toast(p.stock ? '库存只剩 ' + p.stock + ' 件' : '这件已经卖完了'); return; }
  if (line) line.qty += 1; else state.cart.push({ id: id, qty: 1, pack: 1, checked: true });
  updateBadge();
  toast(p.name.split(' ')[0] + ' 已加入购物袋');
};
ACTIONS.openP = function (id) {
  state.productId = id;
  state.pd = { qty: 1, spec: '单件' };
  go('product');
};
ACTIONS.pdQty = function (d) {
  state.pd.qty = Math.max(1, Math.min(99, state.pd.qty + Number(d)));
  render();
};
ACTIONS.pdSpec = function (s) { state.pd.spec = s; render(); };
ACTIONS.cat = function (c) { state.cat = c; render(); };
ACTIONS.openCat = function (c) { state.cat = c; go('category'); };
ACTIONS.pdAdd = function () {
  var id = state.productId;
  var p = byId(id);
  if (!p || p.shelf === false) { toast('这个商品已下架'); return; }
  var pack = state.pd.spec === '三件装' ? 3 : 1;
  var line = null;
  for (var j = 0; j < state.cart.length; j++) if (state.cart[j].id === id && (state.cart[j].pack || 1) === pack) line = state.cart[j];
  var want = (line ? line.qty : 0) + state.pd.qty;
  if (want > p.stock) { toast('库存只剩 ' + p.stock + ' 件'); return; }
  if (line) line.qty += state.pd.qty; else state.cart.push({ id: id, qty: state.pd.qty, pack: pack, checked: true });
  updateBadge();
  var t = tierFor(p, want);
  toast('已加入 ' + state.pd.qty + ' 件 · ' + state.pd.spec + (t ? ' · 享' + offLabel(t.off) : ''));
};
ACTIONS.buyNow = function () {
  var id = state.productId;
  state.cart = [{ id: id, qty: state.pd.qty, pack: state.pd.spec === '三件装' ? 3 : 1, checked: true }];
  go('checkout');
};
ACTIONS.addrSheet = function () {
  var list = state.me.addrs.map(function (a, i) {
    return '<div class="row" data-act="pickAddr" data-val="' + i + '" style="cursor:pointer">' +
      '<div class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M12 3c3.6 0 6.4 2.8 6.4 6.3 0 4.4-6.4 10.2-6.4 10.2S5.6 13.7 5.6 9.3C5.6 5.8 8.4 3 12 3z"/><circle cx="12" cy="9.3" r="2.1"/></svg></div>' +
      '<div><b>' + a.name + '</b><small>' + a.sub + ' · ' + a.tag + '</small></div>' +
      '<span class="rt">' + (state.checkout.addrIndex === i ? '当前' : '选择 →') + '</span></div>';
  }).join('');
  openSheet('<h3>选择收货地址</h3><p class="sub">校园内 30 分钟送达</p>' + list +
    '<button class="btn primary block" style="margin-top:14px" data-act="addrNew">＋ 新增地址（楼栋 / 楼层 / 宿舍号）</button>');
};
ACTIONS.pickAddr = function (i) { state.checkout.addrIndex = Number(i); closeSheet(); render(); toast('已切换收货地址'); };
ACTIONS.cartQty = function (v) {
  var p = v.split(':'), i = Number(p[0]), d = Number(p[1]);
  var line = state.cart[i];
  if (!line) return;
  if (d < 0 && line.qty <= 1) {
    var nm = byId(line.id).name;
    state.cart.splice(i, 1);
    render();
    toast('已移出：' + nm);
    return;
  }
  line.qty = Math.max(1, line.qty + d);
  render();
};
ACTIONS.cartCheck = function (i) { state.cart[Number(i)].checked = !state.cart[Number(i)].checked; render(); };
ACTIONS.cartAll = function () {
  var all = state.cart.every(function (c) { return c.checked; });
  state.cart.forEach(function (c) { c.checked = !all; });
  render();
};
ACTIONS.cartRemove = function (i) {
  var name = byId(state.cart[Number(i)].id).name;
  state.cart.splice(Number(i), 1);
  render();
  toast('已移出：' + name);
};
ACTIONS.checkout = function () {
  if (!checkedCount()) { toast('先勾选要结算的商品'); return; }
  go('checkout');
};
ACTIONS.setTime = function (t) { state.checkout.time = t; render(); };
ACTIONS.setPay = function (p) { state.checkout.pay = p; render(); };
ACTIONS.submit = function () {
  var items = state.cart.filter(function (c) { return c.checked; }).map(function (c) { return { id: c.id, qty: c.qty, pack: c.pack || 1, unit: lineUnit(c) }; });
  if (!items.length) { toast('购物袋是空的'); return; }
  var total = state.cart.reduce(function (a, c) { return c.checked ? a + byId(c.id).price * c.qty : a; }, 0);
  var no = 'A' + (2382 + state.orders.length);
  var addrObj = state.me.addrs[state.checkout.addrIndex];
  var addrName = addrObj.name;
  var order = {
    no: no, status: '备货中', items: items, addr: addrName,
    contact: addrObj.contact || '小满', phone: addrObj.phone || '13800006621', date: todayISO(),
    time: '刚刚', rider: '待分配', note: state.checkout.note, pay: state.checkout.pay,
    created: '今天 ' + now(),
    logs: [{ t: now(), title: '已下单', desc: state.checkout.pay + ' · ' + addrName }]
  };
  state.orders.unshift(order);
  state.cart = state.cart.filter(function (c) { return !c.checked; });
  state.lastNo = no;
  state.admin.extra += 1;
  state.checkout.note = '';
  go('success');
  toast('下单成功 · ' + money(total));
};
ACTIONS.orderTab = function (t) { state.orderTab = t; render(); };
ACTIONS.openOrder = function (no) { state.detailNo = no; go('order'); };
ACTIONS.cancelOrder = function (no) {
  openSheet('<h3>取消这一单？</h3><p class="sub">备货中取消会立即退款，预计 1 分钟内到账。</p>' +
    '<div style="display:flex;gap:9px;margin-top:16px">' +
    '<button class="btn block" data-act="closeSheet">再想想</button>' +
    '<button class="btn red block" data-act="doCancel" data-val="' + no + '">确认取消</button></div>');
};
ACTIONS.doCancel = function (no) {
  state.orders.forEach(function (o) {
    if (o.no === no) {
      o.status = '已取消';
      pushLog(o, '已取消', o.pay === '货到付款' ? '用户取消，订单关闭' : '用户取消，退款原路退回');
    }
  });
  closeSheet();
  render();
  toast('订单已取消，退款已提交');
};
ACTIONS.reorder = function (no) {
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) return;
  o.items.forEach(function (it) {
    var pack = it.pack || 1;
    var line = state.cart.filter(function (c) { return c.id === it.id && (c.pack || 1) === pack; })[0];
    if (line) line.qty += it.qty; else state.cart.push({ id: it.id, qty: it.qty, pack: pack, checked: true });
  });
  updateBadge();
  tab('cart');
  toast('已把 ' + o.items.length + ' 种商品放回购物袋');
};
ACTIONS.contact = function () { toast('演示版：正在呼叫骑手 0231'); };
ACTIONS.urge = function (no) {
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) return;
  if (o.status === '已完成' || o.status === '已取消' || o.status === '已退款') { toast('这一单已经结束了'); return; }
  pushLog(o, '用户催单', '用户点击催单，已通知骑手');
  pushMsg('订单 ' + no + '，发起催单', 'user');
  toast('已催单 · 配送端收到提醒');
  render();
};

var TASK_POOL = [
  { no: 'A2391', addr: '北苑 5 号楼 · 416', km: '1.2 km', items: 3, fee: 7.0 },
  { no: 'A2392', addr: '学苑 6 号楼 · 208', km: '0.6 km', items: 2, fee: 5.5 },
  { no: 'A2393', addr: '图书馆东门 · 自提柜', km: '1.4 km', items: 5, fee: 8.0 },
  { no: 'A2394', addr: '教师公寓 1 栋 · 803', km: '2.0 km', items: 4, fee: 9.5 }
];

ACTIONS.refresh = function () { ACTIONS.refreshTasks(false); };
ACTIONS.refreshTasks = function (auto) {
  var d = state.deliver;
  d.last = Date.now();
  d.tick += 1;
  var added = 0;
  if (d.avail.length < 4 && (!auto || d.tick % 2 === 0)) {
    var src = TASK_POOL[d.poolIdx % TASK_POOL.length];
    d.poolIdx += 1;
    d.avail.unshift({ id: 'T' + Date.now(), no: src.no, addr: src.addr, km: src.km, items: src.items, fee: src.fee, tag: '刚刚' });
    added = 1;
  }
  if (state.client === 'delivery' && state.route.delivery === 'hall') {
    var sc = document.querySelector('#screen .scroll');
    var top = sc ? sc.scrollTop : 0;
    render();
    var sc2 = document.querySelector('#screen .scroll');
    if (sc2) sc2.scrollTop = top;
    var btn = document.querySelector('[data-act="refresh"]');
    if (btn) {
      btn.classList.add('spinning');
      var h = btn.querySelector('#refreshHint');
      if (h) h.textContent = '刷新中';
      setTimeout(function () { btn.classList.remove('spinning'); }, 850);
    }
  }
  if (!auto) toast(added ? '已刷新 · 新进 1 单' : '已刷新，暂无新单');
  else if (added) toast('自动刷新 · 新进 1 单');
};

ACTIONS.dTab = function (t) { state.deliver.tab = t; render(); };
ACTIONS.dRange = function (r) { state.deliver.range = r; render(); };
ACTIONS.dOrder = function (no) {
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) return;
  var items = o.items.map(function (it) {
    var p = byId(it.id);
    var unit = it.unit != null ? it.unit : unitPrice(p, it.qty, it.pack || 1);
    return '<div class="co-line"><span>' + p.name + ' × ' + it.qty + '</span><b>' + money(unit * it.qty) + '</b></div>';
  }).join('');
  openSheet('<h3>' + o.no + '</h3><p class="sub">' + o.created + ' · ' + o.addr + ' · ' + pill(o.status) + '</p>' +
    items +
    '<div class="co-line"><span>收货人</span><b>' + o.contact + ' · ' + maskPhone(o.phone) + '</b></div>' +
    '<div class="co-line"><span>支付方式</span><b>' + o.pay + '</b></div>' +
    '<div class="co-line total"><span>合计</span><b>' + money(orderTotal(o)) + '</b></div>' +
    '<div class="divider"><span>订单日志</span><i></i></div>' + logsHtml(o) +
    statusActions(o));
};
function statusActions(o) {
  var mine = o.rider && o.rider.indexOf('李海') >= 0;
  var open = o.status === '待取货' || o.status === '配送中';
  if (mine && open) {
    var label = o.status === '待取货' ? '确认已取货' : '确认已送达';
    var act = o.status === '待取货' ? 'pick' : 'done';
    return '<div style="display:flex;gap:8px;margin-top:14px">' +
      '<button class="btn primary block" data-act="dOrderStep" data-val="' + o.no + '|' + act + '">' + label + '</button></div>' +
      '<p class="small muted" style="margin-top:8px;text-align:center">更新后会同步到用户端和管理端的订单日志</p>';
  }
  var hint = o.status === '备货中' ? '仓库还没派单，接单后才能更新状态'
    : open ? '这一单由 ' + o.rider + ' 配送，你只能查看'
    : '这一单已结束，只能查看';
  return '<p class="small muted" style="margin-top:12px">' + hint + '</p>';
}
ACTIONS.dOrderStep = function (v) {
  var p = String(v).split('|'), no = p[0], act = p[1];
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) return;
  if (!o.rider || o.rider.indexOf('李海') < 0) { toast('这一单不是你的，不能改状态'); return; }
  if (act === 'pick') {
    if (o.status !== '待取货') { toast('当前状态不能确认取货'); return; }
    o.status = '配送中';
    pushLog(o, '已取货', '骑手已从北苑仓取货，配送中');
    if (state.deliver.active && state.deliver.active.no === no) state.deliver.active.stage = 1;
    closeSheet();
    render();
    toast('已更新为：已取货');
    return;
  }
  if (act === 'done') {
    if (o.status !== '配送中') { toast('当前状态不能确认送达'); return; }
    o.status = '已完成';
    pushLog(o, '已送达', '已送达并确认收货');
    var active = state.deliver.active && state.deliver.active.no === no ? state.deliver.active : null;
    var fee = active ? active.fee : 7.5;
    state.deliver.done += 1;
    state.deliver.income += fee;
    state.deliver.records.unshift({ t: '刚刚', no: no, fee: fee });
    if (active) state.deliver.active = null;
    closeSheet();
    render();
    toast('已送达 · 本单 +' + money(fee));
  }
};
ACTIONS.aListRange = function (r) { state.admin.listRange = r; render(); };

ACTIONS.dMsg = function () {
  var msgs = state.deliver.msgs;
  var list = msgs.map(function (m) {
    return '<div class="msg ' + (m.read ? '' : 'unread') + '"><span class="mdot"></span>' +
      '<div><b>' + m.text + '</b><small>' + m.t + ' · ' + (m.type === 'admin' ? '管理端提醒' : '用户催单') + '</small></div></div>';
  }).join('');
  openSheet('<h3>消息提醒</h3><p class="sub">' + unreadMsg() + ' 条未读 · 用户催单与管理端提醒都在这里</p>' +
    (list || '<p class="small muted">暂时没有新消息</p>') +
    '<button class="btn block" style="margin-top:14px" data-act="markRead">全部标为已读</button>');
};
ACTIONS.markRead = function () {
  state.deliver.msgs.forEach(function (m) { m.read = true; });
  closeSheet();
  render();
  toast('消息已全部标为已读');
};

ACTIONS.dOnline = function () {
  state.deliver.online = !state.deliver.online;
  render();
  toast(state.deliver.online ? '已上线，开始接单' : '已收工，暂停派单');
};
ACTIONS.accept = function (id) {
  if (!state.deliver.online) { toast('先上线再接单'); return; }
  if (state.deliver.active) { toast('手上还有一单，先送达'); return; }
  var t = state.deliver.avail.filter(function (x) { return x.id === id; })[0];
  if (!t) return;
  state.deliver.avail = state.deliver.avail.filter(function (x) { return x.id !== id; });
  state.deliver.active = { no: t.no, addr: t.addr, km: t.km, items: t.items, fee: t.fee, stage: 0, at: '刚刚' };
  var order = state.orders.filter(function (o) { return o.no === t.no; })[0];
  if (!order) {
    order = {
      no: t.no, status: '备货中', items: [{ id: 'p1', qty: t.items, unit: 6.5 }], addr: t.addr,
      time: now(), rider: '待分配', note: '', pay: '微信支付', created: '今天 ' + now(),
      contact: '林同学', phone: '13600003333', date: todayISO(),
      logs: [{ t: now(), title: '已下单', desc: '配送任务 · ' + t.addr }]
    };
    state.orders.unshift(order);
  }
  order.status = '待取货';
  order.rider = '李海 · 0231';
  pushLog(order, '骑手接单', '李海 · 0231 已接单，前往北苑仓取货');
  tab('task');
  toast('接单成功 · 去北苑仓 A-12 取货');
};
ACTIONS.openTask = function () { go('task'); };
ACTIONS.dStep = function () {
  var a = state.deliver.active;
  if (!a) return;
  if (a.stage === 0) {
    a.stage = 1;
    state.orders.forEach(function (o) { if (o.no === a.no) { o.status = '配送中'; pushLog(o, '已取货', '骑手已从北苑仓取货，配送中'); } });
    render();
    toast('已取货，开始配送');
    return;
  }
  if (a.stage === 1) {
    a.stage = 2;
    state.deliver.done += 1;
    state.deliver.income += a.fee;
    state.deliver.records.unshift({ t: '刚刚', no: a.no, fee: a.fee });
    state.orders.forEach(function (o) { if (o.no === a.no) { o.status = '已完成'; pushLog(o, '已送达', '放在门口，已拍照确认'); } });
    render();
    toast('已送达 · 本单 +' + money(a.fee));
  }
};
ACTIONS.finishTask = function () { state.deliver.active = null; tab('hall'); };
ACTIONS.withdraw = function () {
  openSheet('<h3>提现到微信</h3><p class="sub">今日可提 ' + money(state.deliver.income) + '，预计 2 小时内到账。</p>' +
    '<div style="display:flex;gap:9px;margin-top:16px"><button class="btn block" data-act="closeSheet">取消</button>' +
    '<button class="btn primary block" data-act="doWithdraw">确认提现</button></div>');
};
ACTIONS.doWithdraw = function () {
  closeSheet();
  toast('已提交提现申请 ' + money(state.deliver.income));
  state.deliver.income = 0;
  render();
};

ACTIONS.aRange = function (r) { state.admin.range = r; render(); };
ACTIONS.aFilter = function (f) { state.admin.filter = f; render(); };
ACTIONS.aOpen = function (no) {
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) { toast('这一单在演示数据里不存在'); return; }
  var items = o.items.map(function (it) {
    var p = byId(it.id);
    var unit = it.unit != null ? it.unit : unitPrice(p, it.qty, it.pack || 1);
    return '<div class="co-line"><span>' + p.name + ' × ' + it.qty + '</span><b>' + money(unit * it.qty) + '</b></div>';
  }).join('');
  openSheet('<h3>' + o.no + '</h3><p class="sub">' + o.created + ' · ' + o.addr + ' · ' + pill(o.status) + '</p>' +
    items + '<div class="co-line"><span>支付方式</span><b>' + o.pay + '</b></div>' +
    '<div class="co-line total"><span>合计</span><b>' + money(orderTotal(o)) + '</b></div>' +
    '<div class="divider"><span>订单日志</span><i></i></div>' + logsHtml(o) +
    '<div style="display:flex;gap:8px;margin-top:16px">' +
    '<button class="btn block" data-act="aAct" data-val="' + o.no + '|remind">催单</button>' +
    '<button class="btn block" data-act="aAct" data-val="' + o.no + '|dispatch">派单</button>' +
    '<button class="btn red block" data-act="aAct" data-val="' + o.no + '|refund">退款</button></div>');
};
ACTIONS.aAct = function (v) {
  var p = v.split('|'), no = p[0], act = p[1];
  var o = state.orders.filter(function (x) { return x.no === no; })[0];
  if (!o) return;
  if (act === 'dispatch') {
    o.status = '待取货';
    if (!state.deliver.active && !state.deliver.avail.some(function (x) { return x.no === no; })) {
      state.deliver.avail.unshift({ id: 'T' + Date.now(), no: no, addr: o.addr, km: '1.1 km', items: o.items.reduce(function (a, i) { return a + i.qty; }, 0), fee: 7.0, min: 0, tag: '刚派单' });
    }
    pushLog(o, '已派单', '北苑仓已派单，等待骑手接单');
    toast('已派单，配送端可见');
  } else if (act === 'refund') {
    o.status = '已退款';
    pushLog(o, '已退款', '管理员发起退款 ' + money(orderTotal(o)));
    toast('已退款 ' + money(orderTotal(o)));
  } else if (act === 'remind') {
    pushLog(o, '管理员催单', '管理员提醒骑手尽快配送');
    pushMsg('订单 ' + no + '，管理员提醒你尽快配送', 'admin');
    toast('已催单，配送端收到提醒');
  }
  closeSheet();
  render();
};
ACTIONS.aTodo = function (i) {
  state.admin.todos[Number(i)].done = !state.admin.todos[Number(i)].done;
  render();
};
var BUILDINGS = ['北苑 1 号楼', '北苑 2 号楼', '北苑 3 号楼', '北苑 4 号楼', '学苑 5 号楼', '学苑 6 号楼', '教师公寓 2 栋'];
var FLOORS = ['1', '2', '3', '4', '5', '6'];

function productForm() {
  var d = state.pdraft;
  var cats = CATS.filter(function (c) { return c !== '全部'; });
  return '<h3>' + (d.id ? '编辑商品' : '新增商品') + '</h3>' +
    '<p class="sub">' + (d.id ? '保存后立即同步到用户端' : '创建后立即上架，用户端马上能买到') + '</p>' +
    '<div class="field"><label>商品名称</label><input class="inp" data-input="pf" data-field="name" value="' + esc(d.name) + '" placeholder="如：脆浪 海盐薯片"></div>' +
    '<div class="field-row"><div class="field"><label>售价（元）</label><input class="inp" data-input="pf" data-field="price" value="' + esc(d.price) + '" inputmode="decimal" placeholder="6.5"></div>' +
    '<div class="field"><label>库存（件）</label><input class="inp" data-input="pf" data-field="stock" value="' + esc(d.stock) + '" inputmode="numeric" placeholder="20"></div></div>' +
    '<div class="field"><label>规格</label><input class="inp" data-input="pf" data-field="spec" value="' + esc(d.spec) + '" placeholder="如：68g / 袋"></div>' +
    '<div class="field"><label>分类</label><div class="chips">' + cats.map(function (c) {
      return '<button class="chip ' + (d.cat === c ? 'on' : '') + '" data-act="pDraft" data-val="cat|' + c + '">' + c + '</button>';
    }).join('') + '</div></div>' +
    '<div class="field-row"><div class="field"><label>角标文案</label><input class="inp" data-input="pf" data-field="tag" value="' + esc(d.tag) + '" placeholder="如：新品"></div>' +
    '<div class="field"><label>安全库存</label><input class="inp" data-input="pf" data-field="safe" value="' + esc(d.safe) + '" inputmode="numeric"></div></div>' +
    '<div class="field"><label>商品说明</label><textarea class="inp" data-input="pf" data-field="desc" placeholder="一句话卖点">' + esc(d.desc) + '</textarea></div>' +
    '<button class="btn primary block" data-act="pSave">' + (d.id ? '保存修改' : '创建并上架') + '</button>';
}

ACTIONS.pNew = function () {
  state.pdraft = { id: null, name: '', price: '', spec: '', cat: '零食', tag: '', desc: '', stock: '20', safe: '10' };
  openSheet(productForm());
};
ACTIONS.pEdit = function (id) {
  var p = byId(id);
  if (!p) return;
  state.pdraft = { id: id, name: p.name, price: String(p.price), spec: p.spec, cat: p.cat, tag: p.tag, desc: p.desc, stock: String(p.stock), safe: String(p.safe) };
  openSheet(productForm());
};
ACTIONS.pDraft = function (v) {
  var p = v.split('|');
  state.pdraft[p[0]] = p[1];
  openSheet(productForm());
};
ACTIONS.pSave = function () {
  var d = state.pdraft;
  var name = String(d.name || '').trim();
  var price = parseFloat(d.price);
  var stock = parseInt(d.stock, 10);
  var safe = parseInt(d.safe, 10);
  if (!name) { toast('先填商品名称'); return; }
  if (isNaN(price) || price <= 0) { toast('售价要大于 0'); return; }
  if (isNaN(stock) || stock < 0) { toast('库存填个数字'); return; }
  if (isNaN(safe)) safe = 10;
  if (d.id) {
    var p = byId(d.id);
    p.name = name; p.price = price; p.stock = stock; p.safe = safe; p.cat = d.cat;
    p.spec = String(d.spec || '').trim() || p.spec;
    p.tag = String(d.tag || '').trim() || p.tag;
    p.desc = String(d.desc || '').trim() || p.desc;
    toast('已保存 · ' + name);
  } else {
    var tones = ['t1', 't2', 't3', 't4', 't5', 't6'];
    PRODUCTS.unshift({
      id: 'p' + Date.now(), name: name, spec: String(d.spec || '').trim() || '校园现货',
      price: price, old: 0, tag: String(d.tag || '').trim() || '新品', cat: d.cat || '零食',
      tone: tones[PRODUCTS.length % tones.length], stock: stock, safe: safe, shelf: true,
      desc: String(d.desc || '').trim() || '新上架商品，北苑仓现货。', sold: 0, reviews: []
    });
    toast('已新增并上架：' + name);
  }
  closeSheet();
  render();
};
function newPromoDraft(id) {
  return { id: id, type: 'multi', start: todayISO(), end: addDaysISO(todayISO(), 2), tiers: [{ min: '2', z: '8' }] };
}
function promoForm() {
  var d = state.admin.promoDraft;
  var p = byId(d.id);
  if (!p) return '';
  var single = d.type === 'single';
  var rows = single
    ? '<div class="te-row"><span class="te-fix">任意件数</span>' +
      '<input class="inp te-inp" data-input="promo" data-field="tier-0-z" value="' + esc(d.tiers[0] ? d.tiers[0].z : '9') + '" inputmode="decimal">' +
      '<span>折</span></div>'
    : d.tiers.map(function (t, i) {
        return '<div class="te-row"><span>满</span>' +
          '<input class="inp te-inp" data-input="promo" data-field="tier-' + i + '-min" value="' + esc(t.min) + '" inputmode="numeric">' +
          '<span>件</span>' +
          '<input class="inp te-inp" data-input="promo" data-field="tier-' + i + '-z" value="' + esc(t.z) + '" inputmode="decimal">' +
          '<span>折</span>' +
          '<button class="te-del" data-act="promoDelTier" data-val="' + i + '" aria-label="删除">×</button></div>';
      }).join('');
  return '<h3>设置促销</h3><p class="sub">' + p.name + ' · 原价 ' + money(p.price) + ' · 可设置多档折扣</p>' +
    '<div class="field"><label>促销类型</label><div class="chips">' +
      '<button class="chip ' + (single ? 'on' : '') + '" data-act="promoType" data-val="single">单件打折</button>' +
      '<button class="chip ' + (!single ? 'on' : '') + '" data-act="promoType" data-val="multi">多件折扣</button>' +
    '</div><div class="hint">' + (single ? '买几件都按这个折扣结算' : '按购买件数取最高一档，例如满 1 件 95 折、满 2 件 8 折') + '</div></div>' +
    '<div class="field"><label>' + (single ? '折扣' : '折扣阶梯') + '</label>' + rows +
      (single ? '' : '<button class="te-add" data-act="promoAddTier">＋ 添加一档</button>') + '</div>' +
    '<div class="field"><label>活动时间</label><div class="field-row">' +
      '<div class="field" style="margin-bottom:0"><input class="inp" type="date" data-input="promo" data-field="start" value="' + d.start + '"></div>' +
      '<div style="flex:0 0 18px;display:flex;align-items:center;justify-content:center;font-size:10px;color:#A29888">至</div>' +
      '<div class="field" style="margin-bottom:0"><input class="inp" type="date" data-input="promo" data-field="end" value="' + d.end + '"></div>' +
    '</div></div>' +
    '<button class="btn primary block" data-act="promoSave">' + (p.promo ? '保存促销' : '开始促销') + '</button>' +
    (p.promo ? '<button class="btn ghost block" style="margin-top:8px" data-act="promoOff">结束当前促销</button>' : '');
}
ACTIONS.pPromo = function (id) {
  var p = byId(id);
  if (!p) return;
  if (p.promo) {
    state.admin.promoDraft = {
      id: id,
      type: p.promo.type || 'multi',
      start: p.promo.start,
      end: p.promo.end,
      tiers: p.promo.tiers.map(function (t) { return { min: String(t.min), z: String(Math.round(t.off * 100) / 10) }; })
    };
  } else {
    state.admin.promoDraft = newPromoDraft(id);
  }
  openSheet(promoForm());
};
ACTIONS.promoType = function (v) {
  var d = state.admin.promoDraft;
  d.type = v;
  if (v === 'single') d.tiers = [{ min: '1', z: d.tiers[0] ? d.tiers[0].z : '9' }];
  else if (!d.tiers.length) d.tiers = [{ min: '2', z: '8' }];
  openSheet(promoForm());
};
ACTIONS.promoAddTier = function () {
  var d = state.admin.promoDraft;
  var last = d.tiers[d.tiers.length - 1];
  var next = last ? Math.max(2, (parseInt(last.min, 10) || 1) + 1) : 2;
  d.tiers.push({ min: String(next), z: '7' });
  openSheet(promoForm());
};
ACTIONS.promoDelTier = function (i) {
  var d = state.admin.promoDraft;
  if (d.tiers.length <= 1) { toast('至少保留一档'); return; }
  d.tiers.splice(Number(i), 1);
  openSheet(promoForm());
};
ACTIONS.promoSave = function () {
  var d = state.admin.promoDraft;
  var p = byId(d.id);
  if (!p) return;
  if (!d.start || !d.end) { toast('选一下活动日期'); return; }
  if (d.start > d.end) { toast('结束日期要晚于开始日期'); return; }
  var tiers = d.tiers.map(function (t) {
    return { min: Math.max(1, parseInt(t.min, 10) || 1), off: Math.round((parseFloat(t.z) || 0) * 10) / 100 };
  }).filter(function (t) { return t.off > 0 && t.off < 1; });
  if (!tiers.length) { toast('折扣填 1–10 折之间'); return; }
  if (d.type === 'single') tiers = [{ min: 1, off: tiers[0].off }];
  tiers.sort(function (a, b) { return a.min - b.min; });
  p.promo = { type: d.type, start: d.start, end: d.end, tiers: tiers };
  closeSheet();
  render();
  var top = tiers[tiers.length - 1];
  toast('已开始促销 · ' + p.name.split(' ')[0] + ' ' + (d.type === 'single' ? offLabel(top.off) : '满' + top.min + '件' + offLabel(top.off)));
};
ACTIONS.promoOff = function () {
  var p = byId(state.admin.promoDraft.id);
  if (!p) return;
  delete p.promo;
  closeSheet();
  render();
  toast('已结束促销 · ' + p.name.split(' ')[0]);
};
ACTIONS.stock = function (v) {
  var p = v.split('|');
  var prod = byId(p[0]);
  if (!prod) return;
  prod.stock = Math.max(0, prod.stock + Number(p[1]));
  render();
};
ACTIONS.shelf = function (id) {
  var p = byId(id);
  if (!p) return;
  p.shelf = p.shelf === false;
  render();
  toast(p.shelf ? '已上架 · ' + p.name.split(' ')[0] : '已下架 · ' + p.name.split(' ')[0]);
};

function addrForm() {
  var d = state.addrDraft;
  return '<h3>新增收货地址</h3><p class="sub">校园内 30 分钟送达 · 楼栋 / 楼层快捷选择</p>' +
    '<div class="field"><label>楼栋</label><div class="chips">' + BUILDINGS.map(function (b) {
      return '<button class="chip ' + (d.building === b ? 'on' : '') + '" data-act="addrPick" data-val="building|' + b + '">' + b + '</button>';
    }).join('') + '</div></div>' +
    '<div class="field"><label>楼层</label><div class="chips">' + FLOORS.map(function (f) {
      return '<button class="chip ' + (d.floor === f ? 'on' : '') + '" data-act="addrPick" data-val="floor|' + f + '">' + f + 'F</button>';
    }).join('') + '</div></div>' +
    '<div class="field"><label>宿舍号</label><input class="inp" data-input="addr" data-field="room" value="' + esc(d.room) + '" inputmode="numeric" maxlength="2" placeholder="两位数字，如 08">' +
    '<div class="hint" id="addrPreview">将生成：' + d.building + ' ' + d.floor + (d.room || '__') + '</div></div>' +
    '<div class="field-row"><div class="field"><label>联系人</label><input class="inp" data-input="addr" data-field="contact" value="' + esc(d.contact) + '"></div>' +
    '<div class="field"><label>手机号</label><input class="inp" data-input="addr" data-field="phone" value="' + esc(d.phone) + '" inputmode="numeric" placeholder="用于联系"></div></div>' +
    '<button class="btn primary block" style="margin-top:4px" data-act="addrSave">保存并使用</button>';
}
ACTIONS.addrNew = function () {
  state.addrDraft = { building: '北苑 3 号楼', floor: '5', room: '', contact: '小满', phone: '' };
  openSheet(addrForm());
};
ACTIONS.addrPick = function (v) {
  var p = v.split('|');
  state.addrDraft[p[0]] = p[1];
  openSheet(addrForm());
};
ACTIONS.addrSave = function () {
  var d = state.addrDraft;
  var room = String(d.room || '').replace(/\D/g, '');
  if (!d.building) { toast('先选楼栋'); return; }
  if (!d.floor) { toast('先选楼层'); return; }
  if (room.length < 2) { toast('宿舍号填两位，如 08'); return; }
  var name = d.building + ' ' + d.floor + room;
  var contact = String(d.contact || '').trim() || '小满';
  var phone = String(d.phone || '').trim();
  state.me.addrs.push({ name: name, sub: contact + (phone ? ' · ' + phone : ''), tag: '宿舍', contact: contact, phone: phone });
  state.checkout.addrIndex = state.me.addrs.length - 1;
  closeSheet();
  render();
  toast('已新增地址：' + name);
};
ACTIONS.aRider = function (i) {
  var r = state.admin.riders[Number(i)];
  r.online = !r.online;
  render();
  toast(r.name + (r.online ? ' 已上线' : ' 已下线'));
};

/* 搜索等输入 */
var INPUTS = {};
INPUTS.search = function (v) {
  state.search = v;
  var list = filtered(v, state.cat);
  paint('goodsGrid', list.length ? tiles(list) : '');
  paint('secTitle', (v.trim() ? '搜索「' + esc(v.trim()) + '」' : '常买清单') + '<small>共 ' + list.length + ' 件</small>');
};
INPUTS.note = function (v) { state.checkout.note = v; };
INPUTS.dorder = function (v) {
  state.deliver.q = v;
  if (window.DLIST) { var r = window.DLIST(); paint('dOrderList', r.html); paint('dOrderCount', r.count + ' 单'); }
};
INPUTS.asearch = function (v) {
  state.admin.q = v;
  if (window.ALIST) { var r = window.ALIST(); paint('aOrderList', r.html); paint('aOrderCount', r.count + ' 单'); }
};
INPUTS.pf = function (v, el) { state.pdraft[el.dataset.field] = v; };
INPUTS.promo = function (v, el) {
  var d = state.admin.promoDraft;
  var f = el.dataset.field;
  if (f === 'start' || f === 'end') { d[f] = v; return; }
  var m = f.match(/^tier-(\d+)-(min|z)$/);
  if (m) d.tiers[Number(m[1])][m[2]] = v;
};
INPUTS.addr = function (v, el) {
  var f = el.dataset.field;
  if (f === 'room') {
    var digits = v.replace(/\D/g, '').slice(0, 2);
    state.addrDraft.room = digits;
    el.value = digits;
  } else {
    state.addrDraft[f] = v;
  }
  var prev = document.getElementById('addrPreview');
  if (prev) prev.textContent = '将生成：' + state.addrDraft.building + ' ' + state.addrDraft.floor + (state.addrDraft.room || '__');
};

/* 事件 */
document.addEventListener('click', function (e) {
  var sw = e.target.closest('[data-client]');
  if (sw) { switchClient(sw.dataset.client); return; }
  var el = e.target.closest('[data-act]');
  if (!el) return;
  var fn = ACTIONS[el.dataset.act];
  if (!fn) return;
  e.preventDefault();
  e.stopPropagation();
  fn(el.dataset.val, el);
});
document.addEventListener('input', function (e) {
  var el = e.target.closest('[data-input]');
  if (!el) return;
  var fn = INPUTS[el.dataset.input];
  if (fn) fn(el.value, el);
});
$('#veil').addEventListener('click', closeSheet);

var initial = (location.hash || '').replace('#', '');
if (['user', 'delivery', 'admin'].indexOf(initial) >= 0) state.client = initial;
render();
window.addEventListener('hashchange', function () {
  var h = (location.hash || '').replace('#', '');
  if (['user', 'delivery', 'admin'].indexOf(h) >= 0 && h !== state.client) { state.client = h; render(); }
});

/* 配送端大厅：10 秒静默刷新 */
setInterval(function () {
  if (state.client !== 'delivery' || state.route.delivery !== 'hall') return;
  var left = 10 - Math.floor((Date.now() - state.deliver.last) / 1000);
  var el = document.getElementById('refreshHint');
  if (el) el.textContent = left > 0 ? left + 's' : '刷新中';
  if (left <= 0) ACTIONS.refreshTasks(true);
}, 1000);

/* 促销倒计时 */
setInterval(function () {
  var els = document.querySelectorAll('[data-end]');
  for (var i = 0; i < els.length; i++) {
    var end = new Date(els[i].getAttribute('data-end')).getTime();
    var left = end - Date.now();
    if (left <= 0) { els[i].textContent = '已结束'; continue; }
    var dd = Math.floor(left / 86400000);
    var hh = Math.floor(left % 86400000 / 3600000);
    var mm = Math.floor(left % 3600000 / 60000);
    var ss = Math.floor(left % 60000 / 1000);
    els[i].textContent = (dd > 0 ? dd + '天 ' : '') + pad2(hh) + ':' + pad2(mm) + ':' + pad2(ss);
  }
}, 1000);
