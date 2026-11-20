import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function loadDemo(entry) {
  const elements = new Map();
  const document = {
    querySelector(selector) {
      if (!elements.has(selector)) {
        elements.set(selector, {
          dataset: {}, style: {},
          classList: { add() {}, remove() {}, toggle() {} },
          addEventListener() {}, setAttribute() {}, querySelector() { return null; }
        });
      }
      return elements.get(selector);
    },
    querySelectorAll() { return []; },
    getElementById(id) { return elements.get('#' + id) || null; },
    addEventListener() {}
  };
  const app = vm.createContext({
    document, location: { hash: '' }, history: { replaceState() {} },
    addEventListener() {}, requestAnimationFrame() {}, setInterval() {}, setTimeout() {}, clearTimeout() {}
  });
  app.window = app;
  const html = readFileSync(entry, 'utf8');
  for (const [, src, inline] of html.matchAll(/<script(?: src="([^"]+)")?>([\s\S]*?)<\/script>/g)) {
    const code = src ? readFileSync(new URL(src, entry), 'utf8') : inline;
    vm.runInContext(code, app, { filename: src || entry.pathname });
  }
  return {
    app,
    screen: () => document.querySelector('#screen').innerHTML,
    toast: () => document.querySelector('#toast').textContent
  };
}

const demoEntry = new URL('index.html', import.meta.url);
const phoneEntry = new URL('../校零智选-手机演示.html', import.meta.url);
const entries = [['demo', demoEntry]];
if (existsSync(phoneEntry)) entries.push(['standalone phone demo', phoneEntry]);

for (const [label, entry] of entries) {
  test(label + ': second-item price follows quantity from product to paid order', () => {
    const { app, screen, toast } = loadDemo(entry);
    const actions = app.ACTIONS;
    assert.match(screen(), /第二件 3 块/);

    actions.openP('p1');
    assert.match(screen(), /<div class="pd-price"><b>6\.5 块<\/b>/);
    actions.pdQty('1');
    assert.match(screen(), /商品小计[^<]*<\/span><b>9\.5 块<\/b>/);
    actions.buyNow();
    assert.match(screen(), /应付<\/span><div[^>]*>9\.5 块<\/div>/);

    app.go('cart');
    actions.cartQty('0:-1');
    assert.match(screen(), /合计<b>6\.5 块<\/b>/);
    actions.cartQty('0:1');
    actions.cartQty('0:1');
    assert.match(screen(), /合计<b>16 块<\/b>/);
    actions.cartQty('0:1');
    assert.match(screen(), /合计<b>19 块<\/b>/);
    actions.checkout();
    assert.match(screen(), /应付<\/span><div[^>]*>19 块<\/div>/);
    actions.submit();
    assert.equal(toast(), '下单成功 · 19 块');

    app.byId('p1').price = 8;
    actions.openOrder(app.state.lastNo);
    assert.match(screen(), /<span class="p">19 块<\/span>/);
    assert.match(screen(), /实付<\/span><b>19 块<\/b>/);
  });

  test(label + ': existing tier discounts are reflected in the submission amount', () => {
    const { app, screen, toast } = loadDemo(entry);
    app.ACTIONS.openP('p2');
    app.ACTIONS.pdQty('1');
    app.ACTIONS.buyNow();
    assert.match(screen(), /应付<\/span><div[^>]*>6\.4 块<\/div>/);
    app.ACTIONS.submit();
    assert.equal(toast(), '下单成功 · 6.4 块');
    app.ACTIONS.openOrder(app.state.lastNo);
    assert.match(screen(), /<span class="p">6\.4 块<\/span>/);
  });
}

test('second-item half price keeps cents in checkout and the receipt', () => {
  const { app, screen, toast } = loadDemo(demoEntry);
  app.byId('p1').secondItemOffer = { rate: 0.5 };
  app.ACTIONS.openP('p1');
  assert.match(screen(), /第二件半价/);
  app.ACTIONS.pdQty('1');
  app.ACTIONS.buyNow();
  assert.match(screen(), /应付<\/span><div[^>]*>9\.75 块<\/div>/);
  app.ACTIONS.submit();
  assert.equal(toast(), '下单成功 · 9.75 块');
});

test('three-pack uses its own price and hides the single-pack offer', () => {
  const { app, screen, toast } = loadDemo(demoEntry);
  app.ACTIONS.openP('p1');
  app.ACTIONS.pdSpec('三件装');
  app.ACTIONS.pdQty('1');
  assert.doesNotMatch(screen(), /第二件 3 块/);
  app.ACTIONS.buyNow();
  assert.match(screen(), /应付<\/span><div[^>]*>38 块<\/div>/);
  app.ACTIONS.submit();
  assert.equal(toast(), '下单成功 · 38 块');
});
