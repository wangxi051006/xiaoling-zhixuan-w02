// 骑手端界面回归：用固定数据的 API 替身驱动已构建的 web/dist/rider.html，
// 覆盖任务大厅 → 待取货 → 配送中 的批量与逐单操作，并输出证据截图。
// 用法：先在仓库根目录 `cd web && npm run build`，再
//   cd apps/rider-app && node tools/ui-check.mjs
import pw from 'file:///D:/校零/web/node_modules/playwright-core/index.js';
import { createServer } from 'node:http';
import { readFile, mkdir, readdir, unlink } from 'node:fs/promises';
import { resolve, sep } from 'node:path';
import assert from 'node:assert/strict';

const { chromium } = pw;
const dist = resolve('../../web/dist');
const out = resolve('evidence/ui-redesign');
const types = { html: 'text/html; charset=utf-8', js: 'text/javascript', css: 'text/css', svg: 'image/svg+xml' };

await mkdir(out, { recursive: true });
for (const name of await readdir(out)) if (name.endsWith('.png')) await unlink(resolve(out, name));

/* ---------- 固定数据 ---------- */
const slot = (minutes) => ({
  id: `slot-${minutes}`,
  starts_at: `2026-11-22T${String(11 + Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}:00+08:00`,
  ends_at: `2026-11-22T${String(11 + Math.floor((minutes + 30) / 60)).padStart(2, '0')}:${String((minutes + 30) % 60).padStart(2, '0')}:00+08:00`,
  dispatch_at: '2026-11-22T10:30:00+08:00',
  accepting: true,
});
const address = { building_id: 'b-1', room: 'A317', contact_name: '王同学', contact_phone: '13800001234', building_name: '信息工程学院 3 号楼' };
const tasks = [
  { id: 'T1122001', building_name: '信息工程学院 3 号楼', item_count: 2, fee_fen: 450, slot: slot(60), order_status: 'READY_FOR_CLAIM', payment_method: 'BALANCE' },
  { id: 'T1122002', building_name: '图书馆北门', item_count: 1, fee_fen: 350, slot: slot(75), order_status: 'READY_FOR_CLAIM', payment_method: 'COD' },
  { id: 'T1122003', building_name: '学生公寓 7 栋', item_count: 3, fee_fen: 600, slot: slot(45), order_status: 'READY_FOR_CLAIM', payment_method: 'BALANCE' },
  { id: 'T1122004', building_name: '学生公寓 7 栋', item_count: 2, fee_fen: 500, slot: slot(20), order_status: 'AWAITING_PICKUP', payment_method: 'BALANCE' },
  { id: 'T1122005', building_name: '信息工程学院 3 号楼', item_count: 1, fee_fen: 400, slot: slot(10), order_status: 'IN_DELIVERY', payment_method: 'BALANCE' },
  { id: 'T1122006', building_name: '图书馆北门', item_count: 2, fee_fen: 550, slot: slot(5), order_status: 'IN_DELIVERY', payment_method: 'COD' },
];
const history = [
  { id: 'T1121901', building_name: '学生公寓 7 栋', item_count: 1, fee_fen: 400, slot: slot(30), order_status: 'DELIVERED', payment_method: 'BALANCE' },
  { id: 'T1121902', building_name: '图书馆北门', item_count: 2, fee_fen: 500, slot: slot(40), order_status: 'DELIVERED', payment_method: 'COD' },
];
const earnings = [
  { id: 'e-1', task_id: 'T1121901', amount_fen: 400, recorded_at: '2026-11-22T09:20:00+08:00' },
  { id: 'e-2', task_id: 'T1121902', amount_fen: 500, recorded_at: '2026-11-22T10:05:00+08:00', commission_fen: 120 },
  { id: 'e-3', task_id: 'T1121903', amount_fen: 450, recorded_at: '2026-11-22T10:40:00+08:00' },
];
let messages = [
  { id: 'm-1', kind: 'ORDER_URGE', task_id: 'T1122005', created_at: '2026-11-22T10:42:00+08:00' },
  { id: 'm-2', kind: 'ORDER_OVERDUE', task_id: 'T1122004', created_at: '2026-11-22T10:20:00+08:00', read_at: '2026-11-22T10:30:00+08:00' },
];
const leaderboard = {
  local_date: '2026-11-22',
  entries: [
    { rank: 1, rider_id: 'r-zhang', display_name: '张鹏', orders: 12, me: false },
    { rank: 2, rider_id: 'r-li', display_name: '李伟', orders: 9, me: false },
    { rank: 3, rider_id: 'r-me', display_name: '我（陈晨）', orders: 7, me: true },
    { rank: 4, rider_id: 'r-zhao', display_name: '赵宇', orders: 5, me: false },
    { rank: 5, rider_id: 'r-sun', display_name: '孙婷', orders: 3, me: false },
  ],
};
let available = true;

const pageOf = (items) => ({ items, next_cursor: null });
const taskView = (task) => ({
  ...task,
  address,
  ...(task.payment_method === 'COD' && task.order_status === 'IN_DELIVERY' ? { cod_due_fen: 3200 } : {}),
});

const server = createServer(async (req, res) => {
  const raw = (req.url ?? '/').split('?')[0];
  const file = raw === '/' ? 'rider.html' : raw.endsWith('.html') ? raw.slice(1) : raw.startsWith('/assets/') ? decodeURIComponent(raw.slice(1)) : null;
  try {
    const target = file && resolve(dist, file);
    if (!target?.startsWith(dist + sep)) throw new Error('not found');
    const body = await readFile(target);
    res.writeHead(200, { 'Content-Type': types[file.split('.').at(-1)] ?? 'application/octet-stream', 'Cache-Control': 'no-store' }).end(body);
  } catch { res.writeHead(404).end(); }
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const origin = `http://rider.localhost:${server.address().port}`;

const browser = await chromium.launch({
  executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
  headless: true,
  args: ['--no-proxy-server', '--host-resolver-rules=MAP rider.localhost 127.0.0.1'],
});
const context = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, timezoneId: 'Asia/Shanghai', locale: 'zh-CN' });
const page = await context.newPage();
const failures = [];
const writes = [];
const errors = [];
page.on('pageerror', error => errors.push(`pageerror: ${error.message}`));
page.on('console', message => { if (message.type() === 'error') errors.push(`console: ${message.text()}`); });

await page.route('**/api/v1/**', async route => {
  const request = route.request();
  const path = new URL(request.url()).pathname.replace('/api/v1', '');
  const respond = (body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  if (request.method() !== 'GET') writes.push({ path, body: request.postDataJSON() });
  if (path === '/me') return respond({ id: 'r-me', station_id: 'ST-01', roles: ['RIDER'], csrf_token: 'csrf-test', email: null });
  if (path === '/rider/availability') {
    if (request.method() === 'PUT') available = Boolean(request.postDataJSON().available);
    return respond({ available });
  }
  if (path === '/rider/capacity') return respond({ active: 2, limit: 8, remaining: 6, cod_active: tasks.some(t => t.payment_method === 'COD' && ['AWAITING_PICKUP', 'IN_DELIVERY'].includes(t.order_status)) });
  if (path === '/rider/tasks') return respond(pageOf(tasks.filter(t => t.order_status !== 'DELIVERED')));
  if (path === '/rider/history') return respond(pageOf(history));
  if (path === '/rider/earnings') return respond(pageOf(earnings));
  if (path === '/rider/messages') return respond(pageOf(messages));
  if (path === '/rider/leaderboard') return respond(leaderboard);
  if (path === '/rider/task-batches/claim') {
    const ids = writes.at(-1).body.ids;
    for (const task of tasks) if (ids.includes(task.id)) task.order_status = 'AWAITING_PICKUP';
    return respond({ count: ids.length });
  }
  if (path === '/rider/task-batches/deliver') {
    const ids = writes.at(-1).body.ids;
    for (const task of tasks) if (ids.includes(task.id)) task.order_status = 'DELIVERED';
    return respond({ count: ids.length });
  }
  if (path === '/rider/messages/read') {
    const ids = writes.at(-1).body.ids;
    messages = messages.map(m => ids.includes(m.id) ? { ...m, read_at: '2026-11-22T10:50:00+08:00' } : m);
    return respond({ accepted: true });
  }
  const task = tasks.find(item => path === `/rider/tasks/${item.id}` || path.startsWith(`/rider/tasks/${item.id}/`));
  if (task) {
    if (path.endsWith('/claim')) task.order_status = 'AWAITING_PICKUP';
    if (path.endsWith('/pickup')) task.order_status = 'IN_DELIVERY';
    if (path.endsWith('/deliver')) task.order_status = 'DELIVERED';
    return respond(taskView(task));
  }
  return respond(pageOf([]));
});

await page.goto(`${origin}/rider.html`, { waitUntil: 'networkidle' });
await page.getByTestId('nav-orders').waitFor({ timeout: 15000 });
await page.waitForTimeout(400);

const check = async (name, fn) => {
  try { await fn(); console.log('PASS', name); }
  catch (error) { failures.push(`${name}: ${error.message}`); console.log('FAIL', name, '-', error.message); }
};
const shot = async (name, width) => {
  if (width) await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 });
  await page.waitForTimeout(220);
  const box = await page.evaluate(() => Math.max(0, document.documentElement.scrollWidth - window.innerWidth));
  if (box > 1) failures.push(`overflow at ${name}: ${box}px`);
  await page.screenshot({ path: resolve(out, `${name}.png`) });
  console.log('shot', name);
};

/* ---------- 任务大厅 ---------- */
await check('任务大厅显示 3 个可接任务与容量', async () => {
  assert.equal(await page.getByTestId('rider-task-T1122001').count(), 1);
  assert.equal(await page.getByTestId('rider-task-T1122002').count(), 1);
  assert.match(await page.getByTestId('rider-capacity').innerText(), /手上 2\/8/);
});
await check('货到付款任务不提供批量勾选', async () => {
  assert.equal(await page.getByTestId('rider-task-T1122002').getByRole('checkbox').count(), 0);
});
await shot('01-orders-hall');

await page.getByTestId('rider-task-T1122001').getByRole('checkbox', { name: '选择接单' }).check();
await page.getByTestId('rider-task-T1122003').getByRole('checkbox', { name: '选择接单' }).check();
await check('勾选后批量抢单可用并显示已选数量', async () => {
  assert.equal(await page.getByTestId('rider-batch-claim').isDisabled(), false);
  assert.match(await page.getByText(/已选 2 单/).innerText(), /已选 2 单/);
});
await shot('02-orders-batch-selected');

await page.getByTestId('rider-batch-claim').click();
await page.getByText('已接单 2 单。').waitFor({ timeout: 8000 });
await check('批量抢单把两单交给待取货分段', async () => {
  assert.deepEqual(writes.filter(w => w.path === '/rider/task-batches/claim').at(-1).body.ids.sort(), ['T1122001', 'T1122003']);
  assert.equal(await page.getByTestId('seg-pickup').getAttribute('aria-pressed'), 'true');
  assert.equal(await page.getByTestId('rider-task-T1122001').count(), 1);
});
await shot('03-pickup');

/* ---------- 待取货 → 取货 ---------- */
await page.getByTestId('rider-task-T1122001').getByRole('button', { name: '去取货' }).click();
await page.getByTestId('current-task').waitFor();
await shot('04-pickup-detail');
await page.getByTestId('pickup').click();
await page.getByText('服务器已确认：取货。').waitFor({ timeout: 8000 });
await check('确认取货后进入配送中且详情仍在', async () => {
  assert.equal(await page.getByTestId('seg-delivering').getAttribute('aria-pressed'), 'true');
  assert.equal(await page.getByTestId('current-task').count(), 1);
  assert.match(await page.getByTestId('current-task').innerText(), /配送中/);
});

/* ---------- 配送中：货到付款逐单、线上已付款批量 ---------- */
await page.getByTestId('rider-task-T1122006').getByRole('button', { name: '去送达' }).click();
await page.getByTestId('current-task').waitFor();
await check('货到付款未确认收款时不能送达', async () => {
  assert.equal(await page.getByTestId('current-task').getByTestId('deliver').isDisabled(), true);
});
await shot('05-delivering-cod-detail');
await page.getByTestId('current-task').getByLabel('已全额收到货到付款').check();
await check('勾选收款后可以确认送达', async () => {
  assert.equal(await page.getByTestId('current-task').getByTestId('deliver').isDisabled(), false);
});
await page.getByTestId('current-task').getByTestId('deliver').click();
await page.getByText('服务器已确认：送达。').waitFor({ timeout: 8000 });
await check('送达写入 cod_received', async () => {
  assert.deepEqual(writes.filter(w => w.path.endsWith('/deliver')).at(-1).body, { cod_received: true });
  assert.equal(await page.getByTestId('rider-task-T1122006').count(), 0);
});

await page.getByTestId('rider-task-T1122005').getByRole('checkbox', { name: '选择送达' }).check();
await page.getByTestId('rider-batch-deliver').click();
await page.getByText('已送达 1 单。').waitFor({ timeout: 8000 });
await check('批量送达只提交线上已付款订单', async () => {
  assert.deepEqual(writes.filter(w => w.path === '/rider/task-batches/deliver').at(-1).body.ids, ['T1122005']);
});
await shot('06-delivering');

/* ---------- 筛选与搜索 ---------- */
await page.getByTestId('seg-hall').click();
await page.getByLabel('付款方式').selectOption('COD');
await check('付款方式筛选只保留货到付款', async () => {
  assert.equal(await page.getByTestId('rider-task-T1122002').count(), 1);
  assert.equal(await page.getByTestId('rider-task-T1122001').count(), 0);
});
await page.getByLabel('付款方式').selectOption('ALL');
await page.getByLabel('搜索楼宇或任务号').fill('图书馆');
await check('楼宇搜索命中图书馆任务', async () => {
  assert.equal(await page.getByTestId('rider-task-T1122002').count(), 1);
  assert.equal(await page.getByTestId('rider-task-T1122001').count(), 0);
});
await page.getByLabel('搜索楼宇或任务号').fill('');

/* ---------- 我的 ---------- */
await page.getByTestId('nav-me').click();
await page.getByTestId('me-work-status').waitFor();
await shot('07-me');
await check('我的展示今日单量与今日收入', async () => {
  const stats = await page.locator('.r-stats').innerText();
  assert.match(stats, /3/);
  assert.match(stats, /¥13\.5/);
});
await check('工作状态来自服务器', async () => {
  assert.match(await page.getByTestId('me-work-status').innerText(), /工作中/);
});
await page.getByTestId('me-work-status').click();
await shot('08-work-status');
await page.getByTestId('go-offline').click();
await page.getByText('服务器已确认下线').waitFor({ timeout: 8000 });
await check('下线后出现休息提示', async () => {
  assert.deepEqual(writes.filter(w => w.path === '/rider/availability').at(-1).body, { available: false });
  await page.getByText(/您已经休息啦/).waitFor({ timeout: 4000 });
});
await page.getByTestId('me-work-status').click();
await page.getByTestId('go-online').click();
await page.getByText('服务器已确认上线').waitFor({ timeout: 8000 });

await page.getByTestId('me-ranking').click();
await page.getByText('第 3 名').waitFor({ timeout: 6000 });
await check('排行榜显示我在第 3 名且高亮', async () => {
  assert.equal(await page.locator('.r-rank-row.me').count(), 1);
  assert.match(await page.locator('.r-rank-row.me').innerText(), /7\s*单/);
});
await shot('09-ranking');

await page.getByRole('button', { name: '返回' }).click();
await page.getByTestId('me-wallet').click();
await page.waitForTimeout(400);
await check('钱包列出收入明细并合计', async () => {
  assert.match(await page.locator('.r-wallet-value').innerText(), /¥13\.5/);
  assert.equal(await page.locator('.r-earn').count(), 3);
});
await shot('10-wallet');

await page.getByRole('button', { name: '返回' }).click();
await page.getByTestId('me-orders').click();
await page.waitForTimeout(300);
await check('我的订单展示历史配送', async () => {
  assert.equal(await page.locator('.r-task').count(), 2);
});
await shot('11-history');

/* ---------- 消息 ---------- */
await page.getByTestId('nav-messages').click();
await page.getByTestId('read-all').waitFor();
await shot('12-messages');
await page.getByTestId('read-all').click();
await page.getByText('消息已由服务器标记为已读。').waitFor({ timeout: 8000 });
await check('全部已读写入未读消息', async () => {
  assert.deepEqual(writes.filter(w => w.path === '/rider/messages/read').at(-1).body.ids, ['m-1']);
  assert.equal(await page.locator('.r-msg.unread').count(), 0);
});

/* ---------- 窄屏与桌面 ---------- */
await page.getByTestId('nav-orders').click();
await shot('13-orders-360', 360);
await page.getByTestId('nav-me').click();
await shot('14-me-360', 360);
await page.setViewportSize({ width: 1440, height: 1000 });
await page.getByTestId('nav-orders').click();
await shot('15-orders-desktop', 1440);

console.log('errors', JSON.stringify(errors));
assert.deepEqual(errors, []);
await browser.close();
await new Promise(r => server.close(r));
if (failures.length) {
  console.log('FAILURES', JSON.stringify(failures, null, 1));
  process.exitCode = 1;
} else {
  console.log('ALL_CHECKS_PASSED');
}
