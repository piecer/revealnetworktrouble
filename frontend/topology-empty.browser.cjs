// Isolated Chromium replay of real producer responses; never a deployed-service claim.
// PLAYWRIGHT_PATH=/path/to/playwright node frontend/topology-empty.browser.cjs OUTPUT
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const assert = require('node:assert/strict');
const output = process.argv[2];
if (!output) throw new Error('an evidence output directory is required');
fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
  const name = new URL(req.url, 'http://local').pathname;
  const file = path.resolve(__dirname, '.' + (name === '/' ? '/index.html' : name));
  if (!file.startsWith(__dirname + '/') || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404); res.end(); return; }
  res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(file));
});
(async () => {
  let browser;
  const results = [];
  try {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    browser = await chromium.launch();
    const base = `http://127.0.0.1:${server.address().port}`;
    for (const width of [375, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = []; let requests = 0;
      page.on('pageerror', error => errors.push(error.message));
      const body = fs.readFileSync(path.join(__dirname, '../testdata/topology-unavailable-http-report.json'));
      await page.route('**/api/v1/reports?geo_details=1', route => { requests++; return route.fulfill({ status: 200, contentType: 'application/json', body }); });
      await page.goto(base + '/#topology');
      await page.locator('#run-topology').click();
      await page.waitForFunction(() => document.querySelector('#topology-workspace').dataset.state === 'render-empty');
      assert.equal(await page.locator('[data-target-index]:checked').count(), 1);
      for (const selector of ['#topology-result', '#topology-render-status', '#topology-state-message']) {
        assert.match(await page.locator(selector).innerText(), /Traceroute.*사용할 수 없/);
      }
      assert.equal(await page.locator('#download-topology').isEnabled(), true);
      await page.screenshot({ path: path.join(output, `unavailable-${width}.png`), fullPage: true });
      await page.locator('[data-filter-action="none"]').click();
      assert.match(await page.locator('#topology-state-message').innerText(), /선택.*0개/);
      await page.locator('[data-filter-action="all"]').click();
      assert.match(await page.locator('#topology-state-message').innerText(), /Traceroute.*사용할 수 없/);
      const geometry = await page.evaluate(() => ({ elements: document.querySelectorAll('*').length, overflow: document.documentElement.scrollWidth > innerWidth }));
      assert.ok(geometry.elements <= 1200); assert.equal(geometry.overflow, false);
      assert.equal(requests, 1); assert.deepEqual(errors, []);
      results.push({ width, requests, ...geometry, errors, unavailable: true, deselection: true, reselection: true });
      await page.close();
    }
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(results, null, 2) + '\n');
    console.log(JSON.stringify(results));
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
