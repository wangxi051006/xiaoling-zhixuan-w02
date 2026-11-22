import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright-core';

const port = process.env.LOCAL_TEST_WEB_PORT;
const roles = {user: ['CUSTOMER'], rider: ['RIDER'], admin: ['FINANCE', 'OPERATOR', 'SECURITY_ADMIN']};
const readMe = page => page.evaluate(async () => {
  const response = await fetch('/api/v1/me', {cache: 'no-store'});
  return {status: response.status, me: response.ok ? await response.json() : null};
});

// Exercise real credentials and secure cookies against the isolated local preview.
test('local password login works independently on all three entries', { skip: !port }, async () => {
  const browser = await chromium.launch({executablePath: process.env.BROWSER_EXECUTABLE || undefined});
  try {
    const context = await browser.newContext();
    // Match the older Chromium runtime in WeChat DevTools.
    await context.addInitScript(() => { delete AbortSignal.timeout; });
    const ids = new Set();
    const pages = [];
    for (const [entry, expected] of Object.entries(roles)) {
      const base = `http://${entry}.localhost:${port}`;
      const page = await context.newPage();
      pages.push({page, expected});
      assert.equal((await page.goto(`${base}/${entry}.html`)).status(), 200);
      const form = page.getByTestId('test-login');
      await form.waitFor({timeout: 5000});
      const password = form.getByLabel('密码', {exact: true});
      assert.equal(await password.getAttribute('type'), 'password');
      await form.getByLabel('账号', {exact: true}).fill('123456');
      await password.fill('123457');
      await form.getByRole('button', {name: '测试登录'}).click();
      await page.getByRole('alert').filter({hasText: '账号或密码不正确'}).waitFor();
      assert.equal(await password.inputValue(), '');
      await password.fill('123456');
      await form.getByRole('button', {name: '测试登录'}).click();
      await page.getByTestId('session-ready').waitFor();
      const {status, me} = await readMe(page);
      assert.equal(status, 200);
      assert.deepEqual(me.roles, expected);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      ids.add(me.id);
    }
    assert.equal(ids.size, 3);
    for (const {page, expected} of pages) {
      await page.reload();
      await page.getByTestId('session-ready').waitFor();
      assert.deepEqual((await readMe(page)).me?.roles, expected);
      await page.getByRole('button', {name: '退出登录'}).click();
      await page.getByTestId('test-login').waitFor();
      assert.equal((await readMe(page)).status, 401);
      assert.deepEqual(await page.evaluate(() => [localStorage.length, sessionStorage.length]), [0, 0]);
    }
  } finally {
    await browser.close();
  }
});
