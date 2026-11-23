import { computed, createApp, h, onMounted, ref, watch, type Component, type VNodeChild } from 'vue';
import {
  ArrowLeft, Banknote, BarChart3, Boxes, ChevronDown, ChevronRight, ClipboardList, Clock3, House,
  Layers3, LogOut, Package, Plus, Printer, ReceiptText, RefreshCw, ScanLine, Search, Settings2,
  ShieldCheck, ShoppingBag, Smile, Tags, Undo2, X,
} from '@lucide/vue';
import { assertNoProductionMock } from '../shared/api.ts';
import type {
  AdminProduct, AdminProductList, Inventory, InventoryAdjustment, MerchantOrder, MerchantOrderList,
  MerchantSettlement, MerchantSettlementList, OrderBatchResult, ProductInput, ProductUpdate,
} from '../shared/generated.ts';
import { moneyAmount } from '../shared/money.ts';
import { native } from '../shared/native.ts';
import { btn, date, field, form, localTestLogin, session, sessionKey, today } from '../user/ui.ts';
import './app.css';

assertNoProductionMock(import.meta.env.PROD, import.meta.env.VITE_ENABLE_MOCK);

type Tab = 'orders' | 'store' | 'me';
type OrderSegment = 'pending' | 'history';
type PendingFilter = 'active' | 'accept' | 'prepare' | 'pickup';
type HistoryFilter = 'all' | 'done' | 'canceled' | 'after';
type StorePage = 'home' | 'categories' | 'products' | 'stock' | 'combos' | 'aftersales' | 'finance' | 'business';
type SearchScope = 'day' | 'id';

const activeStatuses = new Set<MerchantOrder['status']>(['SCHEDULED', 'PREPARING', 'READY_FOR_CLAIM', 'AWAITING_PICKUP', 'IN_DELIVERY', 'RETURNING']);
const terminalStatuses = new Set<MerchantOrder['status']>(['DELIVERED', 'CANCELED', 'RETURNED']);
const paymentLabels: Record<MerchantOrder['payment_method'], string> = { COD: '货到付款', BALANCE: '余额支付', WECHAT: '微信支付' };
const payoutLabels: Record<MerchantSettlement['status'], string> = {
  WAITING_REMITTANCE: '待核销',
  PAYABLE: '待打款',
  PAID: '已打款',
  REFUND: '退款处理',
  NO_PAYOUT: '无应结款',
};
const payoutTones: Record<MerchantSettlement['status'], string> = {
  WAITING_REMITTANCE: 'warn',
  PAYABLE: 'blue',
  PAID: 'green',
  REFUND: 'red',
  NO_PAYOUT: 'muted',
};
const categoryLabels: Record<string, string> = { drink: '饮料', snack: '零食', daily: '日用', fresh: '生鲜', stationery: '文具' };

const EMPTY_ART = `
<svg viewBox="0 0 220 150" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
  <ellipse cx="110" cy="128" rx="72" ry="14" fill="#f5e3c4" opacity=".7"/>
  <path d="M40 124c14-15 36-22 70-22s56 7 70 22c-20 10-44 15-70 15s-50-5-70-15Z" fill="#fdf1dc"/>
  <path d="M52 120c11-10 28-16 58-16s47 6 58 16c-16 8-37 12-58 12s-42-4-58-12Z" fill="#fbe7c8" opacity=".9"/>
  <g transform="rotate(-13 111 84)">
    <path d="M84 48h42l10 8-5 48c-.4 5-4 8-9 8H91c-5 0-9-3-9-8L78 56c-.4-4 2-8 6-8Z" fill="url(#receipt-grad)"/>
    <path d="M126 48l10 8h-14c-3 0-5-2-5-5l1-3h8Z" fill="#e88a17"/>
    <path d="M94 67h26M93 77h28M92 87h19" stroke="#ffe0a5" stroke-width="4" stroke-linecap="round"/>
    <circle cx="112" cy="102" r="3" fill="#fff0cd"/>
  </g>
  <g stroke="#f06a4d" stroke-width="3" stroke-linecap="round">
    <path d="M135 42l7-8M147 49l10-3M129 32l2-9"/>
  </g>
  <g>
    <path d="M62 106v12" stroke="#e6c98f" stroke-width="2"/>
    <circle cx="62" cy="102" r="7" fill="#ffe1a8"/>
    <path d="M158 108v10" stroke="#e6c98f" stroke-width="2"/>
    <circle cx="158" cy="104" r="6" fill="#ffe1a8"/>
    <path d="M113 126v8" stroke="#e6c98f" stroke-width="2"/>
    <circle cx="113" cy="123" r="4" fill="#ffd793"/>
  </g>
  <defs>
    <linearGradient id="receipt-grad" x1="78" y1="48" x2="131" y2="112" gradientUnits="userSpaceOnUse">
      <stop stop-color="#ffb53f"/>
      <stop offset="1" stop-color="#ff8a1f"/>
    </linearGradient>
  </defs>
</svg>`;

createApp({
  setup() {
    const tab = ref<Tab>('orders');
    const orderSegment = ref<OrderSegment>('pending');
    const pendingFilter = ref<PendingFilter>('active');
    const historyFilter = ref<HistoryFilter>('all');
    const search = ref('');
    const searchScope = ref<SearchScope>('day');
    const storePage = ref<StorePage>('home');
    const categoryFilter = ref('');

    const orders = ref<MerchantOrder[]>([]);
    const products = ref<AdminProduct[]>([]);
    const payouts = ref<MerchantSettlement[]>([]);

    // 原生容器：扫码回调 + 小票打印。浏览器里退回原来的 window.print()。
    if (native.available) {
      window.addEventListener('native:scan', event => {
        const code = (event as CustomEvent<{ code?: string }>).detail?.code ?? '';
        auth.notice.value = code ? '扫到 ' + code + '，核对后请点出餐' : '扫码已取消';
      });
    }

    function printSlip() {
      if (!native.available) { window.print(); return; }
      const stamp = new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
      }).format(new Date());
      const pending = orders.value.filter(order => order.status === 'SCHEDULED' || order.status === 'PREPARING').length;
      const active = orders.value.filter(order => order.status !== 'CANCELED' && order.status !== 'RETURNED').length;
      const text = ['校零智选', stamp, '待处理 ' + pending + ' 单 · 在途 ' + active + ' 单',
        '打印机 ' + (native.printerName() || '未选择（请在系统设置配对打印机）')].join('\n');
      const printed = native.printReceipt(text);
      auth.notice.value = printed ? '已发送到打印机' : '打印机不可用，小票已进入补打队列';
    }

    const auth = session(['MERCHANT'], refresh);
    const testLogin = localTestLogin('merchant', auth);

    const creating = ref(false);
    const draft = ref({ name: '', description: '', price: '5', label: '单份', reorder: '5', onSale: true });
    const editing = ref<AdminProduct | null>(null);
    const editForm = ref({ name: '', description: '', price: '0', reorder: '0', onSale: true });
    const adjusting = ref<AdminProduct | null>(null);
    const adjust = ref({ delta: '0', reason: 'MERCHANT_RESTOCK' });

    watch(() => sessionKey(auth.me.value), () => {
      orders.value = [];
      products.value = [];
      payouts.value = [];
      tab.value = 'orders';
      orderSegment.value = 'pending';
      pendingFilter.value = 'active';
      historyFilter.value = 'all';
      search.value = '';
      storePage.value = 'home';
      categoryFilter.value = '';
      creating.value = false;
      editing.value = null;
      adjusting.value = null;
    }, { flush: 'sync' });

    onMounted(() => auth.recover());

    async function loadOrders() {
      orders.value = (await auth.list<MerchantOrderList>('/merchant/orders?limit=100')).items;
    }

    async function loadProducts() {
      products.value = (await auth.list<AdminProductList>('/merchant/products?limit=100')).items;
    }

    async function loadPayouts() {
      payouts.value = (await auth.list<MerchantSettlementList>('/merchant/settlements?limit=100')).items;
    }

    async function refresh() {
      if (!auth.me.value) return;
      if (tab.value === 'orders') {
        await loadOrders();
        return;
      }
      if (tab.value !== 'store') return;
      switch (storePage.value) {
        case 'home':
          await loadOrders();
          break;
        case 'categories':
        case 'products':
        case 'stock':
          await loadProducts();
          break;
        case 'finance':
          await loadPayouts();
          break;
        case 'aftersales':
          await loadOrders();
          break;
        case 'business':
          await Promise.all([loadOrders(), loadPayouts()]);
          break;
      }
    }

    function switchTab(next: Tab) {
      if (tab.value === next) {
        if (next === 'store' && storePage.value !== 'home') {
          storePage.value = 'home';
          categoryFilter.value = '';
          resetProductEditors();
          window.scrollTo({ top: 0 });
          auth.run(refresh);
          return;
        }
        window.scrollTo({ top: 0, behavior: 'smooth' });
        return;
      }
      tab.value = next;
      resetProductEditors();
      if (next === 'store') {
        storePage.value = 'home';
        categoryFilter.value = '';
      }
      window.scrollTo({ top: 0 });
      auth.run(refresh);
    }

    function resetProductEditors() {
      creating.value = false;
      editing.value = null;
      adjusting.value = null;
    }

    function openOrders(segment: OrderSegment = 'pending', filter: PendingFilter = 'active') {
      tab.value = 'orders';
      orderSegment.value = segment;
      if (segment === 'pending') pendingFilter.value = filter;
      else historyFilter.value = 'all';
      window.scrollTo({ top: 0 });
      auth.run(refresh);
    }

    function openStore(page: StorePage, category = '') {
      tab.value = 'store';
      storePage.value = page;
      categoryFilter.value = category;
      resetProductEditors();
      window.scrollTo({ top: 0 });
      auth.run(refresh);
    }

    function backStore() {
      storePage.value = storePage.value === 'products' && categoryFilter.value ? 'categories' : 'home';
      categoryFilter.value = '';
      resetProductEditors();
      window.scrollTo({ top: 0 });
      auth.run(refresh);
    }

    function chooseSegment(segment: OrderSegment) {
      orderSegment.value = segment;
      if (segment === 'pending') pendingFilter.value = 'active';
      else historyFilter.value = 'all';
      window.scrollTo({ top: 0 });
    }

    async function acceptOrder(order: MerchantOrder) {
      await auth.write<OrderBatchResult>('/merchant/order-batches/accept', { ids: [order.id] }, async () => {
        auth.notice.value = '已接单，开始备货';
        if (orderSegment.value === 'pending') pendingFilter.value = 'prepare';
        await loadOrders();
      });
    }

    async function readyOrder(order: MerchantOrder) {
      await auth.write<OrderBatchResult>('/merchant/order-batches/ready', { ids: [order.id] }, async () => {
        auth.notice.value = '备货完成，已通知骑手';
        if (orderSegment.value === 'pending') pendingFilter.value = 'active';
        await loadOrders();
      });
    }

    async function createProduct() {
      const body: ProductInput = {
        name: draft.value.name.trim(),
        description: draft.value.description.trim(),
        on_sale: draft.value.onSale,
        reorder_point: Number(draft.value.reorder),
        skus: [{ label: draft.value.label.trim(), units_per_pack: 1, unit_price_fen: Math.round(Number(draft.value.price) * 100) }],
      };
      await auth.write<AdminProduct>('/merchant/products', body, async created => {
        auth.notice.value = `已上架「${created.name}」`;
        draft.value = { name: '', description: '', price: '5', label: '单份', reorder: '5', onSale: true };
        creating.value = false;
        await loadProducts();
      });
    }

    function startEdit(product: AdminProduct) {
      adjusting.value = null;
      editing.value = product;
      editForm.value = {
        name: product.name,
        description: product.description,
        price: String(product.skus[0] ? product.skus[0].unit_price_fen / 100 : 0),
        reorder: String(product.inventory.reorder_point),
        onSale: product.on_sale,
      };
    }

    async function saveEdit() {
      const product = editing.value;
      if (!product) return;
      const price = Math.round(Number(editForm.value.price) * 100);
      const body: ProductUpdate = {
        expected_version: product.version,
        name: editForm.value.name.trim(),
        description: editForm.value.description.trim(),
        on_sale: editForm.value.onSale,
        reorder_point: Number(editForm.value.reorder),
        skus: product.skus.map((sku, index) => (index === 0 ? { ...sku, unit_price_fen: price } : sku)),
      };
      await auth.write<AdminProduct>(`/merchant/products/${product.id}`, body, async updated => {
        auth.notice.value = `已保存「${updated.name}」`;
        editing.value = null;
        await loadProducts();
      }, 'PATCH');
    }

    async function toggleSale(product: AdminProduct) {
      const body: ProductUpdate = { expected_version: product.version, on_sale: !product.on_sale };
      await auth.write<AdminProduct>(`/merchant/products/${product.id}`, body, async updated => {
        auth.notice.value = updated.on_sale ? `「${updated.name}」已上架` : `「${updated.name}」已下架`;
        await loadProducts();
      }, 'PATCH');
    }

    async function adjustStock() {
      const product = adjusting.value;
      if (!product) return;
      const body: InventoryAdjustment = {
        inventory_id: product.inventory.id,
        delta: Number(adjust.value.delta),
        expected_version: product.inventory.version,
        reason_code: adjust.value.reason.trim(),
      };
      await auth.write<Inventory>('/merchant/inventory/adjustments', body, async inventory => {
        auth.notice.value = `库存已更新，可售 ${inventory.on_hand - inventory.reserved}`;
        adjusting.value = null;
        adjust.value = { delta: '0', reason: 'MERCHANT_RESTOCK' };
        await loadProducts();
      });
    }

    function logout() {
      auth.write<unknown>('/auth/logout', undefined, () => {
        auth.me.value = undefined;
        auth.ready.value = false;
        tab.value = 'orders';
        orderSegment.value = 'pending';
        pendingFilter.value = 'active';
        storePage.value = 'home';
        auth.notice.value = '已退出登录';
      });
    }

    function categoryOf(product: AdminProduct) {
      const raw = product.category?.trim();
      return raw && raw !== 'uncategorized' ? raw : 'uncategorized';
    }

    function categoryLabel(key: string) {
      return categoryLabels[key] ?? (key === 'uncategorized' ? '未分类' : key);
    }

    const categoryGroups = computed(() => {
      const groups = new Map<string, { key: string; label: string; items: AdminProduct[] }>();
      for (const product of products.value) {
        const key = categoryOf(product);
        const group = groups.get(key) ?? { key, label: categoryLabel(key), items: [] };
        group.items.push(product);
        groups.set(key, group);
      }
      return [...groups.values()].sort((a, b) => b.items.length - a.items.length || a.label.localeCompare(b.label, 'zh-CN'));
    });

    const visibleProducts = computed(() => categoryFilter.value
      ? products.value.filter(product => categoryOf(product) === categoryFilter.value)
      : products.value);

    const acceptCount = computed(() => orders.value.filter(order => order.status === 'PREPARING' && !order.merchant_accepted).length);

    const filteredOrders = computed(() => {
      let list: MerchantOrder[];
      if (orderSegment.value === 'pending') {
        list = orders.value.filter(order => activeStatuses.has(order.status));
        if (pendingFilter.value === 'accept') list = list.filter(order => order.status === 'PREPARING' && !order.merchant_accepted);
        if (pendingFilter.value === 'prepare') list = list.filter(order => order.status === 'PREPARING' && order.merchant_accepted);
        if (pendingFilter.value === 'pickup') list = [];
      } else {
        list = orders.value.filter(order => terminalStatuses.has(order.status));
        if (historyFilter.value === 'done') list = list.filter(order => order.status === 'DELIVERED');
        if (historyFilter.value === 'canceled') list = list.filter(order => order.status === 'CANCELED');
        if (historyFilter.value === 'after') {
          list = orders.value.filter(order => order.status === 'RETURNING' || order.status === 'RETURNED'
            || order.settlement_status === 'REFUND_PENDING' || order.settlement_status === 'REFUNDED');
        }
      }
      const keyword = search.value.trim().toUpperCase();
      if (keyword) {
        list = list.filter(order => searchScope.value === 'day'
          ? dayNo(order).includes(keyword)
          : shortId(order).includes(keyword) || order.id.toUpperCase().includes(keyword));
      }
      const direction = orderSegment.value === 'pending' ? 1 : -1;
      return [...list].sort((a, b) => direction * a.created_at.localeCompare(b.created_at));
    });

    const todayOrders = computed(() => orders.value.filter(order => order.status !== 'CANCELED' && dayKey(order.created_at) === today()));
    const todayRevenue = computed(() => todayOrders.value.reduce((sum, order) => sum + (order.subtotal_fen ?? order.total_fen), 0));
    const todayCount = computed(() => todayOrders.value.length);

    const pendingPayout = computed(() => payouts.value
      .filter(payout => payout.status === 'WAITING_REMITTANCE' || payout.status === 'PAYABLE')
      .reduce((sum, payout) => sum + payout.amount_fen, 0));
    const paidPayout = computed(() => payouts.value
      .filter(payout => payout.status === 'PAID')
      .reduce((sum, payout) => sum + payout.amount_fen, 0));

    const businessStats = computed(() => {
      const month = today().slice(0, 7);
      let todayAmount = 0;
      let todayOrderCount = 0;
      let monthAmount = 0;
      for (const order of orders.value) {
        if (order.status === 'CANCELED') continue;
        const key = dayKey(order.created_at);
        const amount = order.subtotal_fen ?? order.total_fen;
        if (key === today()) {
          todayAmount += amount;
          todayOrderCount += 1;
        }
        if (key.startsWith(month) && order.status === 'DELIVERED') monthAmount += amount;
      }
      const days: Array<{ key: string; label: string; amount: number; orders: number }> = [];
      for (let index = 6; index >= 0; index -= 1) {
        const day = new Date(Date.now() - index * 86_400_000);
        days.push({
          key: dayKeyAt(day),
          label: new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'narrow' }).format(day),
          amount: 0,
          orders: 0,
        });
      }
      const dayIndex = new Map(days.map(day => [day.key, day]));
      for (const order of orders.value) {
        if (order.status === 'CANCELED') continue;
        const bucket = dayIndex.get(dayKey(order.created_at));
        if (bucket) {
          bucket.amount += order.subtotal_fen ?? order.total_fen;
          bucket.orders += 1;
        }
      }
      const top = new Map<string, { name: string; qty: number; amount: number }>();
      for (const order of orders.value) {
        if (order.status === 'CANCELED') continue;
        for (const line of order.lines) {
          const item = top.get(line.sku_id) ?? { name: line.name, qty: 0, amount: 0 };
          item.qty += line.qty;
          item.amount += line.line_total_fen;
          top.set(line.sku_id, item);
        }
      }
      return {
        todayAmount,
        todayOrderCount,
        monthAmount,
        pending: pendingPayout.value,
        last7: days,
        maxDay: Math.max(...days.map(day => day.amount), 1),
        top: [...top.values()].sort((a, b) => b.amount - a.amount || b.qty - a.qty).slice(0, 5),
      };
    });

    const afterSales = computed(() => orders.value
      .filter(order => order.status === 'RETURNING' || order.status === 'RETURNED'
        || order.settlement_status === 'REFUND_PENDING' || order.settlement_status === 'REFUNDED')
      .sort((a, b) => b.created_at.localeCompare(a.created_at)));

    function dayKey(value: string) {
      return dayKeyAt(new Date(value));
    }

    function dayKeyAt(value: Date) {
      return new Intl.DateTimeFormat('sv-SE', {
        timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
      }).format(value);
    }

    function clock(value: string) {
      return new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false,
      }).format(new Date(value));
    }

    function shortId(order: MerchantOrder) {
      return order.id.replace(/-/g, '').slice(-8).toUpperCase();
    }

    function dayNo(order: MerchantOrder) {
      const digits = String(parseInt(order.id.replace(/-/g, '').slice(-6), 16) % 10_000).padStart(4, '0');
      return `${dayKey(order.created_at).slice(5).replace('-', '')}${digits}`;
    }

    function yuan(fen: number | undefined) {
      return fen === undefined ? '—' : `¥${moneyAmount(fen)}`;
    }

    function statusMeta(order: MerchantOrder): { label: string; tone: string } {
      if (order.status === 'PREPARING' && !order.merchant_accepted) return { label: '待接单', tone: 'warn' };
      switch (order.status) {
        case 'SCHEDULED': return { label: '待备货', tone: 'muted' };
        case 'PREPARING': return { label: '备货中', tone: 'blue' };
        case 'READY_FOR_CLAIM': return { label: '待骑手接单', tone: 'teal' };
        case 'AWAITING_PICKUP': return { label: '待取货', tone: 'indigo' };
        case 'IN_DELIVERY': return { label: '配送中', tone: 'blue' };
        case 'DELIVERED': return { label: '已送达', tone: 'green' };
        case 'CANCELED': return { label: '已取消', tone: 'muted' };
        case 'RETURNING': return { label: '退回中', tone: 'warn' };
        case 'RETURNED': return { label: '已退回', tone: 'muted' };
      }
    }

    function feedback() {
      return h('div', { class: 'm-feedback' }, auth.status());
    }

    function iconButton(icon: Component, label: string, onClick: () => unknown) {
      return h('button', { type: 'button', class: 'm-icon-btn', 'aria-label': label, title: label, onClick }, [
        h(icon, { size: 19, strokeWidth: 1.9 }),
      ]);
    }

    function tile(icon: Component, tone: string, size = 22) {
      return h('span', { class: ['m-tile', `tone-${tone}`], 'aria-hidden': 'true' }, [
        h(icon, { size, strokeWidth: 1.9 }),
      ]);
    }

    function panel(title: string, children: VNodeChild, extra?: VNodeChild) {
      return h('section', { class: 'm-panel' }, [
        h('header', { class: 'm-panel-head' }, [h('h2', { class: 'm-panel-title' }, title), extra ?? null]),
        children,
      ]);
    }

    function pageHead(title: string, onBack: () => unknown, action?: VNodeChild) {
      return h('header', { class: 'm-page-head' }, [
        h('button', { type: 'button', class: 'm-back', 'aria-label': '返回', 'data-testid': 'page-back', onClick: onBack }, [
          h(ArrowLeft, { size: 22, strokeWidth: 2 }),
        ]),
        h('h1', { class: 'm-page-title' }, title),
        action ?? h('span', { class: 'm-page-spacer' }),
      ]);
    }

    function storeEntry(key: string, label: string, icon: Component, tone: string, onClick: () => unknown) {
      return h('button', {
        type: 'button', class: 'm-entry', 'data-testid': `store-entry-${key}`, onClick,
      }, [tile(icon, tone, 24), h('span', { class: 'm-entry-label' }, label)]);
    }

    function emptyState(title: string, hint: string, action?: VNodeChild) {
      return h('div', { class: 'm-empty' }, [
        h('div', { class: 'm-empty-art', innerHTML: EMPTY_ART }),
        h('p', { class: 'm-empty-title' }, title),
        hint ? h('p', { class: 'm-empty-hint' }, hint) : null,
        action ?? null,
      ]);
    }

    function refreshButton(label = '刷新列表', testid = 'orders-refresh') {
      return h('button', {
        type: 'button', class: 'm-btn primary', 'data-testid': testid, disabled: auth.busy.value,
        onClick: () => auth.run(refresh),
      }, label);
    }

    function orderAction(order: MerchantOrder): VNodeChild {
      if (order.status === 'PREPARING' && !order.merchant_accepted) {
        return h('button', {
          type: 'button', class: 'm-btn primary small', disabled: auth.busy.value,
          onClick: () => acceptOrder(order),
        }, '接单');
      }
      if (order.status === 'PREPARING' && order.merchant_accepted) {
        return h('button', {
          type: 'button', class: 'm-btn primary small', disabled: auth.busy.value,
          onClick: () => readyOrder(order),
        }, '备货完成');
      }
      if (order.status === 'SCHEDULED') return h('span', { class: 'm-hint' }, '等待备货时间');
      if (order.status === 'READY_FOR_CLAIM') return h('span', { class: 'm-hint' }, '已通知骑手接单');
      if (order.status === 'AWAITING_PICKUP' || order.status === 'IN_DELIVERY') return h('span', { class: 'm-hint' }, '骑手配送中');
      return null;
    }

    function orderCard(order: MerchantOrder, options: { actions?: boolean } = {}) {
      const meta = statusMeta(order);
      const count = order.lines.reduce((sum, line) => sum + line.qty, 0);
      return h('article', { class: 'm-order', 'data-order-id': order.id }, [
        h('div', { class: 'm-order-head' }, [
          h('div', { class: 'm-order-title' }, [
            h('span', { class: 'm-order-day' }, `日单号 ${dayNo(order)}`),
            h('span', { class: 'm-order-time' }, date(order.created_at)),
          ]),
          h('span', { class: ['m-status', `tone-${meta.tone}`] }, meta.label),
        ]),
        h('div', { class: 'm-order-slot' }, [
          h(Clock3, { size: 14, strokeWidth: 2 }),
          h('span', `${order.delivery_mode === 'SCHEDULED' ? '时段配送' : '尽快配送'} · ${date(order.slot.starts_at)}–${clock(order.slot.ends_at)}`),
        ]),
        h('ul', { class: 'm-order-lines' }, order.lines.map((line, index) => h('li', { key: `${line.sku_id}-${index}` }, [
          h('span', { class: 'm-line-name' }, `${line.name} · ${line.spec}`),
          h('span', { class: 'm-line-qty' }, `× ${line.qty}`),
          h('span', { class: 'm-line-amount' }, yuan(line.line_total_fen)),
        ]))),
        h('div', { class: 'm-order-foot' }, [
          h('div', { class: 'm-order-sum' }, [
            h('span', { class: 'm-muted' }, `${count} 件 · ${paymentLabels[order.payment_method]}`),
            h('strong', yuan(order.subtotal_fen ?? order.total_fen)),
          ]),
          options.actions === false ? null : orderAction(order),
        ]),
      ]);
    }

    function searchRow() {
      const placeholder = searchScope.value === 'day' ? '请输入日单号' : '请输入订单号';
      return h('div', { class: 'm-search-row' }, [
        h('label', { class: 'm-scope' }, [
          h('select', {
            value: searchScope.value, 'aria-label': '单号类型',
            onChange: (event: Event) => { searchScope.value = (event.target as HTMLSelectElement).value as SearchScope; },
          }, [h('option', { value: 'day' }, '日单号'), h('option', { value: 'id' }, '订单号')]),
          h(ChevronDown, { size: 14, strokeWidth: 2, 'aria-hidden': 'true' }),
        ]),
        h('label', { class: 'm-search-box' }, [
          h(Search, { size: 17, strokeWidth: 1.9, 'aria-hidden': 'true' }),
          h('input', {
            type: 'search', value: search.value, placeholder, 'aria-label': placeholder, 'data-testid': 'orders-search',
            onInput: (event: Event) => { search.value = (event.target as HTMLInputElement).value; },
          }),
          search.value ? h('button', {
            type: 'button', class: 'm-search-clear', 'aria-label': '清空搜索',
            onClick: () => { search.value = ''; },
          }, [h(X, { size: 14, strokeWidth: 2.2 })]) : null,
        ]),
      ]);
    }

    function orderEmpty() {
      if (orderSegment.value === 'pending' && pendingFilter.value === 'pickup') {
        return emptyState('店内/自取订单暂未开通', '当前店铺只支持配送；自取单会集中显示在这里。', refreshButton());
      }
      if (search.value.trim()) {
        return emptyState('没有找到相关订单', `没有匹配「${search.value.trim()}」的${searchScope.value === 'day' ? '日单号' : '订单号'}。`,
          h('button', { type: 'button', class: 'm-btn', onClick: () => { search.value = ''; } }, '清空搜索'));
      }
      return emptyState('一个订单都木有哦～',
        orderSegment.value === 'pending' ? '新订单会自动出现在这里，也可以手动刷新。' : '还没有已完成或已取消的订单。',
        refreshButton());
    }

    function ordersPage() {
      const pendingTabs: Array<[PendingFilter, string, string]> = [
        ['active', '进行中', 'orders-filter-active'],
        ['accept', '待接单', 'orders-filter-accept'],
        ['prepare', '待出餐', 'orders-filter-prepare'],
        ['pickup', '店内/自取', 'orders-filter-pickup'],
      ];
      const historyTabs: Array<[HistoryFilter, string, string]> = [
        ['all', '全部', 'history-filter-all'],
        ['done', '已完成', 'history-filter-done'],
        ['canceled', '已取消', 'history-filter-canceled'],
        ['after', '退款售后', 'history-filter-after'],
      ];
      const list = filteredOrders.value;
      return h('div', { class: 'm-page m-orders-page' }, [
        h('div', { class: 'm-orders-top' }, [
          h('div', { class: 'm-top-row' }, [
            h('div', { class: 'm-seg', role: 'tablist', 'aria-label': '订单范围' }, [
              h('button', {
                type: 'button', role: 'tab', class: ['m-seg-btn', { active: orderSegment.value === 'pending' }],
                'aria-selected': orderSegment.value === 'pending', 'data-testid': 'orders-pending',
                onClick: () => chooseSegment('pending'),
              }, '待处理'),
              h('button', {
                type: 'button', role: 'tab', class: ['m-seg-btn', { active: orderSegment.value === 'history' }],
                'aria-selected': orderSegment.value === 'history', 'data-testid': 'orders-history',
                onClick: () => chooseSegment('history'),
              }, '历史订单'),
            ]),
            h('div', { class: 'm-top-actions' }, [
              iconButton(ScanLine, '扫码', () => { if (native.available) native.scan('order'); else auth.notice.value = '当前环境不支持扫码，请在收银端核销。'; }),
              iconButton(Printer, '打印小票', () => printSlip()),
              iconButton(Settings2, '设置', () => switchTab('me')),
            ]),
          ]),
          h('div', { class: 'm-filter-tabs', role: 'tablist', 'aria-label': '订单筛选' }, (
            orderSegment.value === 'pending' ? pendingTabs : historyTabs
          ).map(([key, label, testid]) => h('button', {
            key, type: 'button', role: 'tab',
            class: ['m-filter-tab', { active: (orderSegment.value === 'pending' ? pendingFilter.value : historyFilter.value) === key }],
            'aria-selected': (orderSegment.value === 'pending' ? pendingFilter.value : historyFilter.value) === key,
            'data-testid': testid,
            onClick: () => {
              if (orderSegment.value === 'pending') pendingFilter.value = key as PendingFilter;
              else historyFilter.value = key as HistoryFilter;
              window.scrollTo({ top: 0 });
            },
          }, [label, key === 'accept' && acceptCount.value ? h('span', { class: 'm-count' }, String(acceptCount.value)) : null]))),
          searchRow(),
        ]),
        feedback(),
        list.length ? h('div', { class: 'm-list' }, list.map(order => orderCard(order))) : orderEmpty(),
      ]);
    }

    function storeMetric(label: string, value: string, onClick: () => unknown, withRefresh: boolean) {
      return h('div', { class: 'm-metric' }, [
        h('div', { class: 'm-metric-top' }, [
          h('span', { class: 'm-metric-label' }, label),
          withRefresh ? h('button', {
            type: 'button', class: 'm-link', onClick: () => auth.run(refresh),
          }, '刷新') : null,
        ]),
        h('button', { type: 'button', class: 'm-metric-value', onClick }, [
          h('span', value), h(ChevronRight, { size: 18, strokeWidth: 2 }),
        ]),
      ]);
    }

    function storeHome() {
      const me = auth.me.value;
      return h('div', { class: 'm-page m-store-page' }, [
        h('section', { class: 'm-store-hero' }, [
          h('div', { class: 'm-store-row' }, [
            h('span', { class: 'm-store-avatar', 'aria-hidden': 'true' }, '零'),
            h('div', { class: 'm-store-id' }, [
              h('h1', { class: 'm-store-name' }, '校零智选商家'),
              h('p', { class: 'm-store-status' }, [
                h('span', { class: 'm-store-pill' }, [
                  h('i', { class: 'm-dot', 'aria-hidden': 'true' }),
                  '接单中',
                ]),
                me ? h('span', { class: 'm-store-station' }, `站点 ${me.station_id}`) : null,
              ]),
            ]),
            h('button', {
              type: 'button', class: 'm-store-switch', 'aria-label': '刷新店铺数据', title: '刷新店铺数据',
              onClick: () => auth.run(refresh),
            }, [h(RefreshCw, { size: 18, strokeWidth: 2 }) ]),
          ]),
          h('div', { class: 'm-store-metrics' }, [
            storeMetric('今日预计收入/元', yuan(todayRevenue.value), () => openStore('business'), true),
            storeMetric('有效订单/单', String(todayCount.value), () => openOrders('pending', 'active'), false),
          ]),
        ]),
        feedback(),
        h('div', { class: 'm-store-panels' }, [
          panel('商品管理', h('div', { class: 'm-entry-grid' }, [
            storeEntry('categories', '分类', Tags, 'orange', () => openStore('categories')),
            storeEntry('products', '商品', ShoppingBag, 'amber', () => openStore('products')),
            storeEntry('stock', '规格库存', Boxes, 'orange-2', () => openStore('stock')),
            storeEntry('combos', '套餐', Layers3, 'green', () => openStore('combos')),
          ])),
          panel('门店经营', h('div', { class: 'm-entry-grid' }, [
            storeEntry('orders', '配送订单', ClipboardList, 'blue', () => openOrders('pending', 'active')),
            storeEntry('aftersales', '售后维权', Undo2, 'red', () => openStore('aftersales')),
          ])),
          panel('财务数据', h('div', { class: 'm-entry-grid' }, [
            storeEntry('finance', '财务', Banknote, 'gold', () => openStore('finance')),
            storeEntry('business', '营业数据', BarChart3, 'violet', () => openStore('business')),
          ])),
        ]),
        h('p', { class: 'm-foot-note' }, '营业数据按本店最近 100 条订单统计 · 状态以服务器为准'),
      ]);
    }

    function categoriesPage() {
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('分类', backStore),
        feedback(),
        h('p', { class: 'm-note m-note-pad' }, '分类由平台后台维护，这里按分类汇总本店商品。'),
        categoryGroups.value.length ? h('div', { class: 'm-list' }, categoryGroups.value.map(group => h('button', {
          key: group.key, type: 'button', class: 'm-category-row',
          onClick: () => openStore('products', group.key),
        }, [
          tile(Tags, 'orange', 22),
          h('span', { class: 'm-category-main' }, [
            h('strong', group.label),
            h('small', `${group.items.length} 件商品`),
          ]),
          h(ChevronRight, { size: 18, class: 'm-chev' }),
        ]))) : emptyState('暂无商品分类', '先在「商品」里添加商品，分类会在这里汇总。', refreshButton('刷新分类', 'categories-refresh')),
      ]);
    }

    function newProductPanel() {
      return panel('新增商品', form([
        field('商品名称', draft.value.name, value => { draft.value.name = value; }, 'text', { required: true, maxlength: 200 }),
        field('描述', draft.value.description, value => { draft.value.description = value; }, 'text', { maxlength: 2000 }),
        field('售价（元）', draft.value.price, value => { draft.value.price = value; }, 'number', { required: true, min: 0, step: '0.01' }),
        field('规格标签', draft.value.label, value => { draft.value.label = value; }, 'text', { required: true, maxlength: 60 }),
        field('补货提醒值', draft.value.reorder, value => { draft.value.reorder = value; }, 'number', { required: true, min: 0, step: 1 }),
        h('label', { class: 'check' }, [
          h('input', {
            type: 'checkbox', checked: draft.value.onSale,
            onChange: (event: Event) => { draft.value.onSale = (event.target as HTMLInputElement).checked; },
          }),
          '创建后立即上架',
        ]),
        h('div', { class: 'actions' }, [
          h('button', {
            type: 'submit', class: 'btn primary', disabled: auth.busy.value, 'data-testid': 'merchant-create-product',
          }, '保存商品'),
          btn('取消', () => { creating.value = false; }, auth.busy.value),
        ]),
      ], createProduct));
    }

    function editBlock(product: AdminProduct) {
      return form([
        field('商品名称', editForm.value.name, value => { editForm.value.name = value; }, 'text', { required: true, maxlength: 200 }),
        field('描述', editForm.value.description, value => { editForm.value.description = value; }, 'text', { maxlength: 2000 }),
        field('首个规格售价（元）', editForm.value.price, value => { editForm.value.price = value; }, 'number', { required: true, min: 0, step: '0.01' }),
        field('补货提醒值', editForm.value.reorder, value => { editForm.value.reorder = value; }, 'number', { required: true, min: 0, step: 1 }),
        h('label', { class: 'check' }, [
          h('input', {
            type: 'checkbox', checked: editForm.value.onSale,
            onChange: (event: Event) => { editForm.value.onSale = (event.target as HTMLInputElement).checked; },
          }),
          '在售（取消勾选即下架）',
        ]),
        h('div', { class: 'actions' }, [
          h('button', {
            type: 'submit', class: 'btn primary', disabled: auth.busy.value, 'data-testid': 'merchant-save-product',
          }, '保存修改'),
          btn('取消', () => { editing.value = null; }, auth.busy.value),
        ]),
      ], saveEdit);
    }

    function adjustBlock(product: AdminProduct) {
      return form([
        field('调整件数（正数入库，负数出库）', adjust.value.delta, value => { adjust.value.delta = value; }, 'number', { required: true, step: 1 }),
        field('原因码', adjust.value.reason, value => { adjust.value.reason = value; }, 'text', { required: true, pattern: '[A-Z0-9_]{1,64}' }),
        h('div', { class: 'actions' }, [
          h('button', {
            type: 'submit', class: 'btn primary', disabled: auth.busy.value || Number(adjust.value.delta) === 0,
            'data-testid': 'merchant-adjust-stock',
          }, '提交调整'),
          btn('取消', () => { adjusting.value = null; }, auth.busy.value),
        ]),
      ], adjustStock);
    }

    function productCard(product: AdminProduct) {
      const editingOpen = editing.value?.id === product.id;
      const adjustingOpen = adjusting.value?.id === product.id;
      const available = product.inventory.on_hand - product.inventory.reserved;
      return h('article', { class: 'm-product' }, [
        h('div', { class: 'm-product-head' }, [
          h('span', { class: 'm-thumb', 'aria-hidden': 'true' }, [h(Package, { size: 22, strokeWidth: 1.8 })]),
          h('div', { class: 'm-product-main' }, [
            h('div', { class: 'm-product-name' }, product.name),
            h('div', { class: 'm-product-meta' }, [
              h('span', { class: 'm-price' }, yuan(product.skus[0]?.unit_price_fen)),
              h('span', { class: 'm-dot-sep' }, '·'),
              h('span', { class: available <= product.inventory.reorder_point ? 'm-stock low' : 'm-stock' }, `可售 ${available}`),
            ]),
          ]),
          h('span', { class: ['m-status', product.on_sale ? 'tone-green' : 'tone-muted'] }, product.on_sale ? '销售中' : '已下架'),
        ]),
        h('div', { class: 'm-product-actions' }, [
          h('button', {
            type: 'button', class: 'm-chip-btn', 'data-testid': 'merchant-edit-product',
            onClick: () => {
              if (editingOpen) { editing.value = null; return; }
              startEdit(product);
            },
          }, editingOpen ? '收起编辑' : '编辑'),
          h('button', {
            type: 'button', class: 'm-chip-btn', 'data-testid': 'merchant-toggle-sale',
            onClick: () => toggleSale(product),
          }, product.on_sale ? '下架' : '上架'),
          h('button', {
            type: 'button', class: 'm-chip-btn',
            onClick: () => {
              editing.value = null;
              adjusting.value = adjustingOpen ? null : product;
              adjust.value = { delta: '0', reason: 'MERCHANT_RESTOCK' };
            },
          }, adjustingOpen ? '收起' : '调库存'),
        ]),
        editingOpen ? editBlock(product) : null,
        adjustingOpen ? adjustBlock(product) : null,
      ]);
    }

    function productsPage() {
      const title = categoryFilter.value ? categoryLabel(categoryFilter.value) : '商品管理';
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead(title, backStore, h('button', {
          type: 'button', class: 'm-head-action',
          onClick: () => {
            creating.value = true;
            editing.value = null;
            adjusting.value = null;
            window.scrollTo({ top: 0 });
          },
        }, [h(Plus, { size: 16, strokeWidth: 2.2 }), '新增'])),
        feedback(),
        categoryFilter.value ? h('div', { class: 'm-filter-chip' }, [
          h('span', `分类：${categoryLabel(categoryFilter.value)}`),
          h('button', { type: 'button', 'aria-label': '清除分类筛选', onClick: () => openStore('products') }, [
            h(X, { size: 14, strokeWidth: 2.2 }),
          ]),
        ]) : null,
        h('div', { class: 'm-subbar' }, [
          h('span', `${visibleProducts.value.length} 件商品`),
          h('button', { type: 'button', class: 'm-link', onClick: () => auth.run(refresh) }, '刷新'),
        ]),
        creating.value ? newProductPanel() : null,
        visibleProducts.value.length ? visibleProducts.value.map(productCard) : emptyState('暂无商品', '上架的商品只属于本店铺，其他店铺看不到也改不了。',
          h('button', { type: 'button', class: 'm-btn primary', onClick: () => { creating.value = true; } }, '新增商品')),
      ]);
    }

    function stockPage() {
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('规格库存', backStore),
        feedback(),
        h('p', { class: 'm-note m-note-pad' }, '库存调整会立即写入服务器；可售 = 在库 − 已占用。'),
        products.value.length ? h('div', { class: 'm-list' }, products.value.map(product => {
          const adjustingOpen = adjusting.value?.id === product.id;
          const available = product.inventory.on_hand - product.inventory.reserved;
          return h('article', { class: 'm-stock-card', key: product.id }, [
            h('div', { class: 'm-product-head' }, [
              h('span', { class: 'm-thumb stock', 'aria-hidden': 'true' }, [h(Boxes, { size: 22, strokeWidth: 1.8 })]),
              h('div', { class: 'm-product-main' }, [
                h('div', { class: 'm-product-name' }, product.name),
                h('div', { class: 'm-product-meta' }, product.skus.map(sku => `${sku.label} ${yuan(sku.unit_price_fen)}`).join(' / ')),
              ]),
              h('div', { class: 'm-stock-num' }, [h('strong', String(available)), h('small', '可售')]),
            ]),
            h('div', { class: 'm-stock-meta' }, [
              h('span', `在库 ${product.inventory.on_hand}`),
              h('span', `占用 ${product.inventory.reserved}`),
              h('span', `补货点 ${product.inventory.reorder_point}`),
              available <= product.inventory.reorder_point ? h('span', { class: 'm-stock low' }, '需补货') : null,
            ]),
            h('div', { class: 'm-product-actions' }, [
              h('button', {
                type: 'button', class: 'm-chip-btn',
                onClick: () => {
                  editing.value = null;
                  adjusting.value = adjustingOpen ? null : product;
                  adjust.value = { delta: '0', reason: 'MERCHANT_RESTOCK' };
                },
              }, adjustingOpen ? '收起' : '调库存'),
            ]),
            adjustingOpen ? adjustBlock(product) : null,
          ]);
        })) : emptyState('暂无库存记录', '添加商品后即可在这里调整库存。', refreshButton('刷新库存', 'stock-refresh')),
      ]);
    }

    function combosPage() {
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('套餐', backStore),
        feedback(),
        emptyState('套餐功能暂未开通', '先用「商品」维护单品；套餐上线后可在这里组合售卖。',
          h('button', { type: 'button', class: 'm-btn', onClick: () => openStore('products') }, '去商品管理')),
      ]);
    }

    function payoutCard(payout: MerchantSettlement) {
      return h('article', { class: 'm-settlement', key: payout.order_id }, [
        h('div', { class: 'm-settlement-head' }, [
          h('div', { class: 'm-settlement-id' }, [
            h('strong', `订单 ${shortValue(payout.order_id)}`),
            h('small', '结算单'),
          ]),
          h('span', { class: ['m-status', `tone-${payoutTones[payout.status]}`] }, payoutLabels[payout.status]),
        ]),
        h('div', { class: 'm-money-row' }, [h('span', '商家应结'), h('strong', yuan(payout.amount_fen))]),
        h('p', { class: 'm-split-row' }, `实收 ${yuan(payout.collected_fen)} · 配送费 ${yuan(payout.delivery_fee_fen)} · 佣金 ${yuan(payout.commission_fen)}`),
        payout.receipt_reference ? h('p', { class: 'm-note' }, `流水号 ${payout.receipt_reference}`) : null,
      ]);
    }

    function shortValue(value: string) {
      return value.replace(/-/g, '').slice(-8).toUpperCase();
    }

    function financePage() {
      const waitingCount = payouts.value.filter(payout => payout.status === 'WAITING_REMITTANCE' || payout.status === 'PAYABLE').length;
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('财务', backStore),
        feedback(),
        h('section', { class: 'm-summary' }, [
          h('div', { class: 'm-summary-main' }, [
            h('span', '待结算金额'),
            h('strong', yuan(pendingPayout.value)),
            h('small', `${waitingCount} 笔待核销 / 待打款`),
          ]),
          h('div', { class: 'm-summary-side' }, [
            h('span', '已打款'),
            h('strong', yuan(paidPayout.value)),
          ]),
        ]),
        h('p', { class: 'm-note m-note-pad' }, '打款与核销由平台财务处理；这里只读展示结算单。'),
        h('div', { class: 'm-subbar' }, [
          h('span', `${payouts.value.length} 笔结算单`),
          h('button', { type: 'button', class: 'm-link', onClick: () => auth.run(refresh) }, '刷新'),
        ]),
        payouts.value.length ? h('div', { class: 'm-list' }, payouts.value.map(payoutCard))
          : emptyState('暂无结算记录', '订单完成且货到付款交款后，结算单会出现在这里。', refreshButton('刷新财务', 'finance-refresh')),
      ]);
    }

    function businessPage() {
      const stats = businessStats.value;
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('营业数据', backStore),
        feedback(),
        h('div', { class: 'm-stat-grid' }, [
          statCard('今日营业额', yuan(stats.todayAmount), `共 ${stats.todayOrderCount} 单`),
          statCard('今日订单', `${stats.todayOrderCount} 单`, '不含已取消'),
          statCard('本月营业额', yuan(stats.monthAmount), '仅统计已送达'),
          statCard('待结算', yuan(stats.pending), '由平台财务处理'),
        ]),
        panel('最近 7 天', h('div', { class: 'm-chart' }, stats.last7.map(day => h('div', { class: 'm-chart-col', key: day.key }, [
          h('span', { class: 'm-chart-value' }, day.amount ? yuan(day.amount) : '—'),
          h('span', { class: 'm-chart-track' }, [
            h('span', {
              class: 'm-chart-bar',
              style: { height: day.amount ? `${Math.max(4, Math.round((day.amount / stats.maxDay) * 100))}%` : '0' },
            }),
          ]),
          h('span', { class: 'm-chart-label' }, day.label),
        ])))),
        panel('商品销量 TOP 5', stats.top.length ? h('div', { class: 'm-rank' }, stats.top.map((item, index) => h('div', { class: 'm-rank-row', key: item.name }, [
          h('span', { class: 'm-rank-index' }, String(index + 1)),
          h('span', { class: 'm-rank-main' }, [
            h('strong', item.name),
            h('small', `销量 ${item.qty}`),
          ]),
          h('span', { class: 'm-rank-amount' }, yuan(item.amount)),
        ]))) : h('p', { class: 'm-note' }, '还没有可统计的商品销量。')),
        h('p', { class: 'm-foot-note' }, '按最近 100 条订单统计 · 时间按 Asia/Shanghai'),
      ]);
    }

    function statCard(label: string, value: string, hint: string) {
      return h('div', { class: 'm-stat' }, [
        h('span', { class: 'm-stat-label' }, label),
        h('strong', { class: 'm-stat-value' }, value),
        h('small', { class: 'm-stat-hint' }, hint),
      ]);
    }

    function afterSalesPage() {
      return h('div', { class: 'm-page m-subpage' }, [
        pageHead('售后维权', backStore),
        feedback(),
        h('p', { class: 'm-note m-note-pad' }, '退款与理赔由平台财务处理；商家端可查看退回中、已退回或退款中的订单。'),
        afterSales.value.length ? h('div', { class: 'm-list' }, afterSales.value.map(order => orderCard(order, { actions: false })))
          : emptyState('暂无售后订单', '有退款或退回时，会在这里显示。', refreshButton('刷新售后', 'aftersales-refresh')),
      ]);
    }

    function storeDetail() {
      switch (storePage.value) {
        case 'categories': return categoriesPage();
        case 'products': return productsPage();
        case 'stock': return stockPage();
        case 'combos': return combosPage();
        case 'finance': return financePage();
        case 'business': return businessPage();
        case 'aftersales': return afterSalesPage();
        default: return storeHome();
      }
    }

    function menuRow(icon: Component, label: string, tone: string, onClick: () => unknown, testid?: string) {
      return h('button', { type: 'button', class: 'm-menu-row', 'data-testid': testid, onClick }, [
        h('span', { class: ['m-menu-icon', `tone-${tone}`], 'aria-hidden': 'true' }, [h(icon, { size: 18, strokeWidth: 1.9 })]),
        h('span', { class: 'm-menu-label' }, label),
        h(ChevronRight, { size: 17, class: 'm-chev' }),
      ]);
    }

    function infoRow(label: string, value: string) {
      return h('div', { class: 'm-info-row' }, [
        h('span', label),
        h('strong', { title: value }, value),
      ]);
    }

    function mePage() {
      const me = auth.me.value;
      if (!me) return null;
      return h('div', { class: 'm-page m-me-page' }, [
        h('section', { class: 'm-me-hero' }, [
          h('span', { class: 'm-me-avatar', 'aria-hidden': 'true' }, '零'),
          h('div', { class: 'm-me-id' }, [
            h('h1', '商家账号'),
            h('p', me.email ?? `账号 ${me.id}`),
            h('div', { class: 'm-me-chips' }, [
              h('span', { class: 'm-role-chip', 'data-testid': 'session-ready' }, me.roles.join(' / ')),
              h('span', { class: 'm-role-chip ghost' }, `站点 ${me.station_id}`),
            ]),
          ]),
        ]),
        feedback(),
        h('div', { class: 'm-menu' }, [
          menuRow(ShieldCheck, '员工登录 / MFA', 'blue', () => window.location.assign('/api/v1/auth/staff/start')),
          menuRow(RefreshCw, '恢复会话', 'green', () => auth.recover(true), 'recover-session'),
          menuRow(Printer, '打印小票', 'violet', () => printSlip()),
          menuRow(LogOut, '退出登录', 'red', logout, 'me-logout'),
        ]),
        h('div', { class: 'm-panel m-info' }, [
          h('h2', { class: 'm-panel-title' }, '店铺信息'),
          infoRow('站点', me.station_id),
          infoRow('入口', 'merchant'),
          infoRow('角色', me.roles.join('、')),
          infoRow('账号', me.id),
        ]),
        h('div', { class: 'm-panel m-info' }, [
          h('h2', { class: 'm-panel-title' }, '说明'),
          h('p', { class: 'm-note' }, '订单、库存与结算状态以服务器为准。营业数据按本店最近 100 条订单统计。'),
          h('p', { class: 'm-note' }, '时间统一按 Asia/Shanghai 显示；退款与理赔由平台财务处理。'),
        ]),
        h('p', { class: 'm-foot-note' }, '校零智选商家 · 校园配送工作台'),
      ]);
    }

    function loginPage() {
      const test = testLogin();
      return h('main', { class: 'm-app m-login', 'data-entry': 'merchant' }, [
        h('section', { class: 'm-login-hero' }, [
          h('span', { class: 'm-login-logo', 'aria-hidden': 'true' }, '零'),
          h('h1', '校零智选商家'),
          h('p', '校园配送 · 店铺经营工作台'),
        ]),
        h('div', { class: 'm-login-sheet' }, [
          feedback(),
          test ?? panel('员工登录', [
            h('p', { class: 'm-note' }, '使用员工账号完成 MFA 后进入商家端。'),
            h('a', { class: 'm-btn primary block', href: '/api/v1/auth/staff/start' }, '员工登录 / MFA'),
          ]),
          h('button', {
            type: 'button', class: 'm-btn block', disabled: auth.busy.value,
            onClick: () => auth.recover(true),
          }, '恢复会话'),
          test ? h('a', { class: 'm-login-alt', href: '/api/v1/auth/staff/start' }, '员工登录 / MFA') : null,
          h('p', { class: 'm-login-foot' }, '校零智选 · 时间按 Asia/Shanghai 显示'),
        ]),
      ]);
    }

    function bottomNav() {
      const items: Array<{ key: Tab; label: string; icon: Component }> = [
        { key: 'orders', label: '订单', icon: ReceiptText },
        { key: 'store', label: '店铺', icon: House },
        { key: 'me', label: '我的', icon: Smile },
      ];
      return h('nav', { class: 'm-tabbar', 'aria-label': '主要功能' }, items.map(item => h('button', {
        key: item.key, type: 'button', class: ['m-tab', { active: tab.value === item.key }],
        'aria-current': tab.value === item.key ? 'page' : undefined,
        'data-testid': `nav-${item.key}`,
        onClick: () => switchTab(item.key),
      }, [
        h('span', { class: 'm-tab-icon', 'aria-hidden': 'true' }, [h(item.icon, { size: 22, strokeWidth: 1.9 })]),
        h('span', item.label),
      ])));
    }

    return () => {
      if (!auth.me.value) return loginPage();
      return h('main', { class: 'm-app', 'data-entry': 'merchant' }, [
        tab.value === 'orders'
          ? ordersPage()
          : tab.value === 'store'
            ? (storePage.value === 'home' ? storeHome() : storeDetail())
            : mePage(),
        bottomNav(),
      ]);
    };
  },
}).mount('#app');
