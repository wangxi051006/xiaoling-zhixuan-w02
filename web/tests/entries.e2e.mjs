import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, sep } from 'node:path';
import test from 'node:test';
import { chromium } from 'playwright-core';

const distribution = resolve('dist');
const contentTypes = { '.html':'text/html; charset=utf-8', '.js':'text/javascript', '.css':'text/css' };
for (const role of ['user', 'rider', 'admin']) {
  test(`${role} serves only its own compiled entry`, async () => {
    const server = createServer(async (req, res) => {
      const path = req.url.split('?')[0];
      const file = path === '/' ? `${role}.html` : path.startsWith('/assets/') ? path.slice(1) : null;
      const target = file && resolve(distribution, file);
      try {
        if (!target?.startsWith(distribution + sep)) throw new Error('not found');
        const ext = file.slice(file.lastIndexOf('.'));
        const data = await readFile(target);
        res.writeHead(200, { 'Content-Type': contentTypes[ext] ?? 'application/octet-stream' }).end(data);
      } catch { res.writeHead(404).end(); }
    });
    await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
    let browser;
    try {
      const url = `http://127.0.0.1:${server.address().port}`;
      browser = await chromium.launch({ executablePath: process.env.BROWSER_EXECUTABLE || undefined });
      for (const width of [390, 1440]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } });
        const errors = [];
        page.on('pageerror', err => errors.push(err.message));
        const response = await page.goto(url);
        assert.equal(response.status(), 200);
        await page.locator(`main[data-entry="${role}"]`).waitFor();
        assert.equal(await page.getByRole('heading', { level: 1 }).count(), 1);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
        assert.deepEqual(errors, []);
        assert.equal((await page.request.get(`${url}/unknown`)).status(), 404);
        for (const other of ['user','rider','admin'].filter(x => x !== role)) assert.equal((await page.request.get(`${url}/${other}.html`)).status(), 404);
        await page.close();
      }
    } finally {
      await browser?.close();
      await new Promise(resolve => server.close(resolve));
    }
  });
}
