import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, sep } from 'node:path';
import test from 'node:test';
import { chromium } from 'playwright-core';

const distribution = resolve('dist');
const contentTypes = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css' };
const csrf = 'c'.repeat(64);
const me = (roles, email = null) => ({ id: 'synthetic-account', station_id: 's1', roles, csrf_token: csrf, email });
const error = (code, status) => ({ code, message: 'synthetic', request_id: 'synthetic-request', status });

async function serve(entry) {
  const server = createServer(async (req, res) => {
    const path = req.url.split('?')[0];
    const file = path === '/' ? entry + '.html' : path.endsWith('.html') || path.startsWith('/assets/') ? path.slice(1) : null;
    const target = file && resolve(distribution, file);
    try {
      if (!target?.startsWith(distribution + sep)) throw new Error('not found');
      const data = await readFile(target);
      const ext = file.slice(file.lastIndexOf('.'));
      res.writeHead(200, { 'Content-Type': contentTypes[ext] ?? 'application/octet-stream' }).end(data);
    } catch { res.writeHead(404).end(); }
  });
  await new Promise((done, fail) => server.once('error', fail).listen(0, '127.0.0.1', done));
  return { server, origin: 'http://127.0.0.1:' + server.address().port };
}

async function withPage(entry, session, run) {
  const { server, origin } = await serve(entry);
  const browser = await chromium.launch({ executablePath: process.env.BROWSER_EXECUTABLE || undefined });
  try {
    for (const width of [390, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      const seen = [];
      await page.route('**/api/v1/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const json = (status, body) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
        seen.push({ method: request.method(), path: url.pathname, body: request.postDataJSON?.() ?? null, csrf: request.headers()['x-csrf-token'] ?? null });
        if (url.pathname === '/api/v1/me') {
          if (session) return json(200, session);
          const verified = seen.some((call) => call.path.startsWith('/api/v1/auth/email/challenges/') && call.path.endsWith('/verify'));
          return verified ? json(200, me(['CUSTOMER'], 'synthetic.student+e2e@example.test')) : json(401, error('UNAUTHENTICATED', 401));
        }
        if (url.pathname.startsWith('/api/v1/catalog/products')) return json(200, { items: [], next_cursor: null });
        if (url.pathname.startsWith('/api/v1/admin/dashboard')) {
          if (!session?.roles.some((role) => role === 'OPERATOR' || role === 'FINANCE')) return json(403, error('FORBIDDEN', 403));
          return json(200, { local_date: '2026-11-19', order_count: 0, delivered_count: 0, net_collected_fen: 0, overdue_count: 0 });
        }
        if (url.pathname === '/api/v1/me/email/challenges/' + 'a'.repeat(32) + '/verify') return json(400, { ...error('BAD_REQUEST', 400), message: '验证码无效或已过期' });
        if (url.pathname.startsWith('/api/v1/admin/riders') && request.method() === 'GET') return json(200, { items: [], next_cursor: null });
        if (url.pathname.startsWith('/api/v1/admin/riders/provision')) return json(201, { id: 'r-synthetic', display_name: '合成骑手', enabled: false, available: false, version: 1 });
        if (url.pathname.startsWith('/api/v1/rider/')) return json(200, { items: [], next_cursor: null });
        return json(202, { challenge_id: 'a'.repeat(32), expires_at: new Date(Date.now() + 600_000).toISOString(), resend_after_seconds: 60 });
      });
      const response = await page.goto(origin + '/' + entry + '.html');
      assert.equal(response.status(), 200);
      await page.locator('main[data-entry="' + entry + '"]').waitFor();
      await run(page, seen, width);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal overflow at ' + width);
      assert.deepEqual(await page.evaluate(() => Object.keys(localStorage)), [], 'no credential may be persisted');
      await page.close();
    }
  } finally {
    await browser.close();
    await new Promise((done) => server.close(done));
  }
}

test('user entry offers customer sign-in without linking to the staff endpoint', async () => {
  await withPage('user', null, async (page) => {
    assert.equal(await page.locator('a[href="/api/v1/auth/staff/start"]').count(), 0);
    assert.equal(await page.getByRole('button', { name: '邮箱登录' }).count(), 1);
    assert.equal(await page.getByRole('button', { name: '手机号登录' }).count(), 1);
  });
});

test('user entry completes the email register flow through the frozen contract', async () => {
  await withPage('user', null, async (page, seen) => {
    for (const label of ['邮箱登录', '邮箱注册', '手机号登录']) {
      assert.equal(await page.getByRole('button', { name: label }).count(), 1, label + ' mode missing');
    }
    // The original phone login stays reachable next to the email modes.
    await page.getByTestId('auth-PHONE_LOGIN').click();
    await page.getByLabel('手机号').waitFor();
    await page.getByTestId('auth-EMAIL_REGISTER').click();
    await page.getByLabel('邮箱地址').fill('Synthetic.Student+e2e@Example.test');
    await page.getByTestId('request-email-code').click();
    const request = seen.find((call) => call.path === '/api/v1/auth/email/challenges');
    assert.ok(request, 'registration request missing');
    assert.deepEqual(request.body, { email: 'Synthetic.Student+e2e@Example.test', purpose: 'REGISTER' });
    await page.getByTestId('email-code').fill('123456');
    await page.getByTestId('verify-email-code').click();
    await page.getByTestId('session-ready').waitFor();
    assert.ok(seen.some((call) => call.path === '/api/v1/auth/email/challenges/' + 'a'.repeat(32) + '/verify' && call.body?.code === '123456'));
  });
});

test('admin entry offers rider provisioning only to a security admin', async () => {
  await withPage('admin', me(['OPERATOR']), async (page) => {
    await page.getByRole('button', { name: '骑手管理' }).click();
    await page.getByRole('button', { name: '启用骑手' }).count();
    assert.equal(await page.getByTestId('provision-rider').count(), 0, 'operator must not see provisioning');
  });
  await withPage('admin', me(['SECURITY_ADMIN']), async (page, seen) => {
    await page.getByTestId('provision-rider').waitFor({ timeout: 2500 });
    assert.equal(seen.some((call) => call.path === '/api/v1/admin/dashboard'), false, 'security-only staff cannot open the business dashboard');
    assert.equal(await page.getByText('暂无骑手记录').count(), 0, 'missing listing permission is not an empty rider list');
    await page.getByLabel('IdP issuer').fill('https://idp.example.test');
    await page.getByLabel('IdP subject').fill('synthetic-subject');
    await page.getByLabel('骑手显示名').fill('合成骑手');
    await page.getByLabel('开户原因码').fill('ONBOARDING');
    await page.getByTestId('provision-rider').click();
    const call = seen.find((row) => row.path === '/api/v1/admin/riders/provision');
    assert.ok(call, 'provision request missing');
    assert.deepEqual(call.body, { issuer: 'https://idp.example.test', subject: 'synthetic-subject', display_name: '合成骑手', reason_code: 'ONBOARDING' });
    assert.equal(call.csrf, csrf, 'provisioning must carry the CSRF token');
  });
});

test('email registration enforces one cooldown and discards the challenge when the address changes', async () => {
  await withPage('user', null, async (page, seen) => {
    await page.getByTestId('auth-EMAIL_REGISTER').click();
    const email = page.getByLabel('邮箱地址');
    await email.fill('first@example.test');
    const requestCode = page.getByTestId('request-email-code');
    await requestCode.click();
    await page.getByTestId('email-code').waitFor();
    assert.equal(await requestCode.isDisabled(), true, 'the initial request button must observe the same cooldown as resend');
    await email.fill('second@example.test');
    await page.getByTestId('email-code').waitFor({ state: 'detached', timeout: 2000 });
    assert.equal(await requestCode.isEnabled(), true, 'a different address can request its own challenge');
    await email.fill('first@example.test');
    assert.equal(await requestCode.isDisabled(), true, 'changing the address back must not erase its cooldown');
    assert.equal(seen.filter((call) => call.path === '/api/v1/auth/email/challenges').length, 1);
  });
});

test('rider entry exposes staff login only and no self-service registration', async () => {
  await withPage('rider', null, async (page) => {
    await page.getByRole('link', { name: '员工登录 / MFA' }).waitFor();
    for (const forbidden of ['注册', '开户', '申请', '邮箱', '手机号']) {
      assert.equal(await page.getByRole('button', { name: forbidden }).count(), 0, 'rider entry must not offer ' + forbidden);
    }
    assert.equal(await page.getByRole('textbox').count(), 0, 'rider entry must not render a sign-up form');
  });
});

test('an invalid binding code leaves the signed-in customer in their account', async () => {
  await withPage('user', me(['CUSTOMER']), async (page) => {
    await page.getByRole('button', { name: '我的账号' }).click();
    await page.getByLabel('要绑定的邮箱').fill('bound@example.test');
    await page.getByTestId('request-bind-code').click();
    await page.getByTestId('bind-code').fill('123456');
    await page.getByTestId('verify-bind-code').click();
    await page.getByRole('alert').filter({ hasText: '验证码无效' }).waitFor({ timeout: 2000 });
    assert.equal(await page.getByTestId('session-ready').count(), 1);
    assert.equal(await page.getByTestId('bind-code').count(), 1);
  });
});
