// Owned Chromium storage/DOM/request-admission acceptance; API responses are intercepted.
// PLAYWRIGHT_PATH=/path/to/playwright node frontend/label-integrity.browser.cjs OUTPUT
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const root = __dirname;
const outputDir = process.argv[2];
if (!outputDir) throw new Error('an evidence output directory is required');
fs.mkdirSync(outputDir, { recursive: true });
const output = path.join(outputDir, 'results.json');
const storageKey = 'checknetwork.ip-labels.v1';
const labels = count => Array.from({ length: count }, (_, i) => ({ ip: `10.0.${Math.floor(i / 256)}.${i % 256}`, label: `node-${i}`, note: `note-${i}` }));
const server = http.createServer((req, res) => {
  const name = new URL(req.url, 'http://local').pathname;
  const file = path.resolve(root, '.' + (name === '/' ? '/index.html' : name));
  if (!file.startsWith(root + '/') || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404); res.end(); return; }
  res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(file));
});
const results = { assets: {}, pagination: [], imports: [], topology: [] };
(async () => {
  let browser;
  try {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const base = `http://127.0.0.1:${server.address().port}`;
    browser = await chromium.launch({ headless: true });
    for (const file of ['index.html', 'app.js', 'common-prefix.js', 'state.js']) {
      const served = Buffer.from(await (await fetch(base + '/' + file)).arrayBuffer());
      assert.deepEqual(served, fs.readFileSync(path.join(root, file)));
      results.assets[file] = crypto.createHash('sha256').update(served).digest('hex');
    }
    async function newPage(count, width = 1440) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = []; page.on('pageerror', error => errors.push(error.message));
      await page.addInitScript(({ seeded, key }) => {
        localStorage.setItem(key, JSON.stringify(seeded));
        window.labelProbe = { peak: 0, chunks: [] };
        const append = Element.prototype.append;
        Element.prototype.append = function (...nodes) {
          const inserted = nodes.reduce((n, node) => n + (node.nodeType === 1 ? 1 : 0) + (node.querySelectorAll?.('*').length || 0), 0);
          if (this.id === 'ip-label-rows') window.labelProbe.chunks.push(inserted);
          const result = append.apply(this, nodes);
          window.labelProbe.peak = Math.max(window.labelProbe.peak, document.querySelectorAll('*').length);
          return result;
        };
      }, { seeded: labels(count), key: storageKey });
      await page.goto(base + '/#diagnostics');
      await page.waitForFunction(() => document.querySelector('#targets').children.length === 4);
      return { page, errors };
    }
    const ready = page => page.waitForFunction(() => document.querySelector('#ip-labels-view').dataset.state === 'ready');
    const ips = page => page.locator('#ip-label-rows tr[data-ip]').evaluateAll(rows => rows.map(row => row.dataset.ip));
    const stored = page => page.evaluate(key => JSON.parse(localStorage.getItem(key)), storageKey);
    for (const targets of [1, 4, 20]) for (const count of [0, 100, 500]) {
      const { page, errors } = await newPage(count);
      try {
        // Direct startup-to-target admission: no navigation workaround.
        assert.equal(await page.locator('#ip-label-rows > *').count(), 0);
        while (await page.locator('#targets > *').count() > targets) await page.locator('#targets .icon-button').first().click();
        while (await page.locator('#targets > *').count() < targets) {
          const before = await page.locator('#targets > *').count();
          await page.locator('#add-target').click();
          assert.equal(await page.locator('#targets > *').count(), before + 1, 'add target must make progress');
        }
        await page.locator('[data-view-link="ip-labels"]').click(); await ready(page);
        const pages = [];
        for (let guard = 0; ; guard++) {
          assert.ok(guard < 500);
          const current = await ips(page); if (count) assert.ok(current.length > 0);
          assert.ok(current.length <= 100); pages.push(current);
          if (await page.locator('#ip-label-next').isDisabled()) break;
          await page.locator('#ip-label-next').click(); await ready(page);
        }
        assert.deepEqual(pages.flat(), labels(count).map(row => row.ip));
        for (let i = pages.length - 1; i >= 0; i--) {
          assert.deepEqual(await ips(page), pages[i]);
          if (i) { await page.locator('#ip-label-prev').click(); await ready(page); }
        }
        while (!(await page.locator('#ip-label-next').isDisabled())) { await page.locator('#ip-label-next').click(); await ready(page); }
        if (count) {
          const row = page.locator('#ip-label-rows tr[data-ip]').last(); const ip = await row.getAttribute('data-ip');
          await row.locator('[data-field="label"]').fill('browser edited');
          await row.locator('[data-field="note"]').fill('browser note');
          await row.locator('[data-field="note"]').press('Tab');
          assert.deepEqual((await stored(page)).find(row => row.ip === ip), { ip, label: 'browser edited', note: 'browser note' });
          await row.locator('.mapping-delete').click(); await ready(page);
          assert.deepEqual(await stored(page), labels(count).filter(row => row.ip !== ip));
        }
        const probe = await page.evaluate(() => window.labelProbe);
        assert.ok(probe.peak <= 1200); assert.ok(probe.chunks.every(n => n <= 100)); assert.deepEqual(errors, []);
        results.pagination.push({ targets, count, pages: pages.length, peak: probe.peak, maxChunk: Math.max(...probe.chunks), errors });
        if (targets === 20 && count === 500) {
          await page.locator('#ip-label-prev').click(); await ready(page);
          await page.screenshot({ path: path.join(outputDir, 'labels-desktop.png') });
          await page.setViewportSize({ width: 375, height: 1000 });
          await page.screenshot({ path: path.join(outputDir, 'labels-mobile.png') });
        }
      } finally { await page.close(); }
    }
    const low = { ip: '1.1.1.1', label: 'new', note: '' }, high = { ip: '192.0.2.1', label: 'new high', note: '' };
    const update = { ip: '10.0.1.243', label: 'updated', note: 'updated note' };
    for (const scenario of [
      { name: 'low new at capacity', count: 500, incoming: [low], accepted: [], omitted: 1 },
      { name: 'high new at capacity', count: 500, incoming: [high], accepted: [], omitted: 1 },
      { name: 'CSV existing update at capacity', count: 500, incoming: [update], accepted: [update], omitted: 0, csv: true },
      { name: 'batch crossing capacity', count: 499, incoming: [high, low], accepted: [low], omitted: 1 },
      { name: 'update after 500 new keys', count: 500, incoming: [...labels(500).map(row => ({ ...row, ip: row.ip.replace(/^10\./, '1.') })), update], accepted: [update], omitted: 500 }
    ]) {
      const { page, errors } = await newPage(scenario.count);
      try {
        await page.locator('[data-view-link="ip-labels"]').click(); await ready(page);
        const text = scenario.csv ? 'ip,label,note\n' + scenario.incoming.map(row => `${row.ip},${row.label},${row.note}`).join('\n') : JSON.stringify(scenario.incoming);
        await page.locator('#ip-label-import').setInputFiles({ name: scenario.csv ? 'labels.csv' : 'labels.json', mimeType: scenario.csv ? 'text/csv' : 'application/json', buffer: Buffer.from(text) });
        await page.waitForFunction(() => document.querySelector('#ip-label-message').textContent.includes('가져왔습니다'));
        await ready(page);
        const expected = new Map(labels(scenario.count).map(row => [row.ip, row]));
        for (const row of scenario.accepted) expected.set(row.ip, row);
        assert.deepEqual(new Map((await stored(page)).map(row => [row.ip, row])), expected);
        const message = await page.locator('#ip-label-message').textContent();
        assert.equal(message, `${scenario.accepted.length}개 매핑을 가져왔습니다. 0개 행은 유효하지 않아 제외했고 ${scenario.omitted}개 행은 한도를 초과하거나 중복되어 생략했습니다.`);
        assert.deepEqual(errors, []); results.imports.push({ name: scenario.name, persisted: expected.size, message, errors });
      } finally { await page.close(); }
    }
    const twenty = Array.from({ length: 20 }, (_, i) => `target-${i}.example`);
    const { page, errors } = await newPage(0);
    try {
      const posts = [];
      await page.route('**/api/v1/reports?geo_details=1', route => { posts.push(route.request().postDataJSON()); return route.fulfill({ status: 400, contentType: 'application/json', body: '{}' }); });
      await page.locator('[data-view-link="topology"]').click();
      for (const [name, text, addresses] of [
        ['LF', 'example.com\n', ['example.com']], ['CRLF', 'example.com\r\n', ['example.com']],
        ['blank mixed', ',\n example.com,\r\n, other.example,,\n', ['example.com', 'other.example']],
        ['20 real', twenty.join(',\r\n, \n') + '\n', twenty],
        ['all blank', ',\r\n, \t\n,', null], ['21 real', [...twenty, 'extra.example'].join('\n') + '\n', null]
      ]) {
        const before = posts.length;
        await page.locator('#topology-targets').fill(text); await page.locator('#run-topology').click();
        if (addresses) {
          await page.waitForFunction(() => document.querySelector('#topology-workspace').getAttribute('aria-busy') === 'false');
          assert.equal(posts.length, before + 1); assert.deepEqual(posts.at(-1).targets.map(row => row.address), addresses);
        } else {
          assert.match(await page.locator('#topology-error').textContent(), /invalid topology address|exceeds limit 20/);
          assert.equal(posts.length, before);
        }
        results.topology.push({ name, posts: posts.length - before, addresses });
      }
      assert.deepEqual(errors, []);
    } finally { await page.close(); }
    results.pass = true;
  } finally {
    await browser?.close(); await new Promise(resolve => server.close(resolve));
    fs.writeFileSync(output, JSON.stringify(results, null, 2));
  }
  console.log(JSON.stringify(results, null, 2));
})().catch(error => { console.error(error); process.exitCode = 1; });
