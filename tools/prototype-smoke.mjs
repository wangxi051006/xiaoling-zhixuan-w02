import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

const [prototypeRoot, playwrightEntry, executablePath] = process.argv.slice(2);
assert(prototypeRoot && playwrightEntry && executablePath, 'usage: node prototype-smoke.mjs ROOT PLAYWRIGHT_ENTRY CHROME_EXE');
const { chromium } = await import(pathToFileURL(resolve(playwrightEntry)).href);
const browser = await chromium.launch({ executablePath });
try {
  for (const width of [390, 1440]) {
    for (const role of ['user', 'delivery', 'admin']) {
      const page = await browser.newPage({ viewport: { width, height: 900 } });
      try {
        const errors = [];
        page.on('pageerror', err => errors.push(err.message));
        const url = pathToFileURL(resolve(prototypeRoot, 'demo/index.html'));
        url.hash = role;
        await page.goto(url.href);
        const screen = page.locator(`#screen.app-${role}`);
        await screen.waitFor({ state: 'visible' });
        const text = (await screen.innerText()).trim();
        assert(text.length > 30 && !text.includes('页面不存在'), `${role}: missing initial view`);
        assert.deepEqual(errors, []);
        console.log(`PASS ${width}px ${role}`);
      } finally {
        await page.close();
      }
    }
  }
} finally {
  await browser.close();
}
