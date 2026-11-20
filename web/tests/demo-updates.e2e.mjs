import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import test from 'node:test';
import { chromium } from 'playwright-core';

const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP4sAAAAASUVORK5CYII=', 'base64');

const entries = ['demo/index.html', '校零智选-手机演示.html']
  .map(file => [file, new URL('../../' + file, import.meta.url)])
  .filter(([, entry]) => existsSync(entry));

for (const [file, entry] of entries) {
  test(file + ' supports photos and the updated three-role order flow', { timeout: 30_000 }, async () => {
    const browser = await chromium.launch({ executablePath: process.env.BROWSER_EXECUTABLE || undefined });
    try {
      const mobile = file !== 'demo/index.html';
      const page = await browser.newPage({
        viewport: { width: mobile ? 390 : 1440, height: 844 },
        isMobile: mobile, hasTouch: mobile, reducedMotion: 'reduce'
      });
      page.setDefaultTimeout(3500);
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.clock.setFixedTime(new Date('2026-11-20T20:00:00'));
      await page.goto(entry.href);
      const device = await page.locator('.device').boundingBox();
      assert.equal(device.width, mobile ? 370 : 430);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      const act = (key, value) => {
        const selector = `[data-act="${key}"]`;
        return page.locator(value === undefined ? selector : `${selector}[data-val="${value}"]`).first();
      };
      const click = (key, value) => act(key, value).click();
      const role = key => page.locator(`#switcher [data-client="${key}"]`).click();
      const input = key => page.locator(`[data-input="${key}"]`);
      const text = selector => page.locator(selector).innerText();
      const order = () => text('.done p').then(value => value.match(/A\d+/)[0]);
      const screen = () => text('#screen');

      await role('admin');
      await click('tab', 'goods');
      await click('pNew');
      await page.locator('[data-input="pf"][data-field="name"]').fill('照片演示商品');
      const price = page.locator('[data-input="pf"][data-field="price"]');
      await price.fill('6.789');
      await click('pSave');
      assert.match(await text('#toast'), /最多两位小数/);
      await price.fill('6.78');
      const photo = page.locator('#product-photo');
      await photo.setInputFiles({ name: 'sample.txt', mimeType: 'text/plain', buffer: Buffer.from('image') });
      assert.match(await text('#photo-preview'), /JPEG 或 PNG/);
      await photo.setInputFiles({ name: 'sample.png', mimeType: 'image/png', buffer: png });
      await page.locator('#photo-preview img').waitFor({state:'visible'});
      assert.equal(await page.locator('#photo-preview img').evaluate(img => img.naturalWidth), 1);
      await click('pSave');
      const card = page.locator('.goods-row').filter({ hasText: '照片演示商品' });
      assert.ok(await card.locator('img').isVisible());
      const photoId = await card.locator('[data-act="pEdit"]').getAttribute('data-val');
      await role('user');
      const tile = page.locator(`.tile[data-val="${photoId}"]`);
      assert.ok(await tile.locator('img').isVisible());
      await tile.click();
      assert.ok(await page.locator('.pd-hero img').isVisible());
      assert.match(await text('.pd-price'), /6\.78 块/);
      await click('buyNow');
      await click('submit');
      assert.match(await screen(), /下单成功/);

      await role('admin');
      await click('tab', 'orders');
      await click('aSettings');
      await input('deliveryFee').fill('2.25');
      await input('merchantLimit').fill('2');
      await input('riderLimit').fill('3');
      await click('saveStation');

      await role('user');
      const makeOrder = async pay => {
        await click('tab', 'home');
        await click('openP', 'p1');
        await click('buyNow');
        await click('setPay', pay);
        assert.match(await screen(), /配送费\s*2\.25 块/);
        await click('submit');
        assert.match(await text('#toast'), /8\.75 块/);
        return order();
      };
      const cod = await makeOrder('货到付款');
      const prepaid = await makeOrder('微信支付');
      await role('admin');
      await click('tab', 'orders');
      await click('aSettings');
      await input('deliveryFee').fill('3.5');
      await click('saveStation');
      await click('aSelect', cod);
      await click('aSelect', prepaid);
      await click('aSelect', 'A2379');
      assert.ok(await act('aBatch', 'accept').isDisabled(), 'merchant batch cannot exceed its configured limit');
      await click('aSelect', 'A2379');
      await click('aBatch', 'accept');
      assert.match(await text('#toast'), /2 单/);
      await click('aSelect', cod);
      await click('aSelect', prepaid);
      await click('aSelect', 'A2379');
      assert.ok(await act('aBatch', 'ready').isDisabled(), 'selected orders must be in the same stage');
      await click('aSelect', 'A2379');
      await click('aBatch', 'ready');
      assert.match(await text('#toast'), /2 单/);

      await role('delivery');
      await input('riderPayFilter').selectOption('cod');
      assert.ok(await act('claimSelect', cod).isVisible());
      assert.equal(await act('claimSelect', prepaid).count(),0);
      await input('riderPayFilter').selectOption('all');
      await click('claimSelect', cod);
      await click('claimSelect', prepaid);
      await click('claimBatch');
      assert.match(await text('#toast'), /2 单/);
      await click('tab', 'hall');
      assert.ok(await act('accept', 'T1').isDisabled(), 'rider cannot claim past the active-task limit');
      await click('openTask', 'A2381');
      await click('openTask', cod);
      await click('dStep');
      assert.match(await screen(), /应收\s*8\.75 块/);
      assert.equal(await act('deliverSelect', cod).count(),0);
      assert.ok(await act('dStep').isDisabled());
      await input('riderPayFilter').selectOption('cod');
      await click('confirmCash');
      await click('dStep');
      assert.match(await text('#toast'), /2\.25 块/);
      assert.match(await text('#rider-task-detail'), new RegExp('#' + prepaid));
      assert.equal(await input('riderPayFilter').inputValue(), 'all');
      await click('dStep');
      await click('deliverSelect', prepaid);
      await click('deliverSelect', 'A2381');
      await click('deliverBatch');
      assert.match(await text('#toast'), /2 单/);
      await role('user');
      await click('tab', 'home');
      await click('addrSheet');
      await click('addrEdit', '0');
      await page.locator('[data-input="addr"][data-field="contact"]').fill('演示收货人');
      await click('addrSave');
      await click('addrSheet');
      assert.match(await text('#sheet'), /演示收货人/);
      await click('addrDelete', '0');
      await click('addrDeleteConfirm', '0');
      assert.doesNotMatch(await text('#sheet'), /演示收货人/);
      assert.deepEqual(errors, []);
    } finally {
      await browser.close();
    }
  });
}
