// Actual app, loopback fixture server; no provider, trace, tile or production service.
// PLAYWRIGHT_PATH=/installed/playwright node frontend/geo-target-selection.browser.cjs OUTPUT
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');
const assert = require('node:assert/strict'), crypto = require('node:crypto');
const { pathToFileURL } = require('node:url');
const out = process.argv[2]; assert.ok(out, 'owned evidence directory required'); fs.mkdirSync(out, { recursive: true });
const assets = ['index.html', 'styles.css', 'app.js', 'common-prefix.js', 'state.js', 'topology-model.js', 'topology-presentation.js', 'topology-visualizer.js', 'topology-renderer.js', 'geo-map.js'];
const stats = total => ({ total, displayed: total, omitted: 0 });
const routeStats = total => ({ ...stats(total), complete: total, partial: 0 });
function fixture(maximum = false, unlocated = false) {
  const started_at = '2026-10-09T00:00:00Z';
  const nodes = Array.from({ length: maximum ? 500 : 4 }, (_, i) => ({
    id: `n${i}`, kind: 'ip', address: `8.1.${Math.floor(i / 256)}.${i % 256}`, status: 'healthy',
    observations: 1, public_ip: true, hop_min: 1, hop_max: 25,
    ...(unlocated ? {} : { geolocation: { latitude: 0, longitude: maximum ? 0 : [-40, 0, 40, 70][i], country: 'Controlled country', country_code: 'US' } })
  }));
  const paths = maximum ? Array.from({ length: 20 }, (_, i) => nodes.slice(i * 25, (i + 1) * 25).map(n => n.id)) : [['n0', 'n1'], ['n2', 'n1'], ['n3', 'n1']];
  const results = [], routes = [], links = [], result_stats = [];
  paths.forEach((node_ids, result_index) => {
    results.push({ kind: 'traceroute', address: maximum ? `target-${result_index}-`.padEnd(4096, 'x') : result_index === 1 ? 'hostile-<img src=x onerror=alert(1)>-' + '긴이름'.repeat(40) : `target-${result_index}.example`, status: 'healthy', latency_ms: 1, started_at,
      details: { attempts_total: 1, attempts_reached: 1, attempts_failed: 0, attempts_unreached: 0, attempts_execution_failed: 0, attempts_timed_out: 0, attempts_cancelled: 0 } });
    routes.push({ result_index, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids });
    node_ids.slice(1).forEach((to, i) => links.push({ from: node_ids[i], to, status: 'healthy', observations: 1 }));
    result_stats.push({ result_index, routes: routeStats(1), node_observations: stats(node_ids.length), link_observations: stats(node_ids.length - 1) });
  });
  return { id: 'controlled-selection', status: 'healthy', started_at, duration_ms: 1, summary: { total: results.length, passed: results.length, failed: 0 }, results,
    compact_topology: { schema: 'compact-v1', selection: 'fair-complete-prefix-v1', limits: { nodes: 500, links: 1000, max_response_bytes_exclusive: 1048576, max_geo_bundle_bytes: 4096 },
      nodes, links, routes, stats: { nodes: stats(nodes.length), links: stats(links.length), routes: routeStats(routes.length), node_observations: stats(paths.reduce((n, p) => n + p.length, 0)), link_observations: stats(links.length) }, result_stats,
      geo: { eligible: nodes.length, available: unlocated ? 0 : nodes.length, included: unlocated ? 0 : nodes.length, omitted: 0, unavailable: unlocated ? nodes.length : 0 }, truncated: false } };
}
let report = fixture(), requests = 0;
const server = http.createServer((req, res) => {
  const name = new URL(req.url, 'http://local').pathname.slice(1) || 'index.html';
  if (name === 'api/v1/reports' && req.method === 'POST') { requests++; res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(report)); return; }
  if (!assets.includes(name)) { res.writeHead(404); res.end(); return; }
  res.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(path.join(__dirname, name)));
});
(async () => {
  let browser, base; const source = {}, runs = [];
  try {
    const { normalizeReport } = await import(pathToFileURL(path.join(__dirname, 'state.js')));
    const { routeColor } = await import(pathToFileURL(path.join(__dirname, 'topology-visualizer.js')));
    normalizeReport(report);
    await new Promise(r => server.listen(0, '127.0.0.1', r)); base = `http://127.0.0.1:${server.address().port}`;
    for (const name of assets) {
      const bytes = fs.readFileSync(path.join(__dirname, name)); assert.deepEqual(Buffer.from(await (await fetch(`${base}/${name}`)).arrayBuffer()), bytes);
      source[name] = crypto.createHash('sha256').update(bytes).digest('hex');
    }
    browser = await chromium.launch();
    for (const [width, height] of [[1440, 900], [375, 900], [667, 375], [760, 600]]) {
      report = fixture(); const before = requests, page = await browser.newPage({ viewport: { width, height }, hasTouch: true }), errors = [], external = [];
      page.on('pageerror', e => errors.push(e.message)); page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
      await page.route('**/*', r => { if (!r.request().url().startsWith(base + '/')) { external.push(r.request().url()); return r.abort(); } return r.continue(); });
      await page.addInitScript(() => {
        window.__census = { peak: 0, chunks: [] };
        for (const method of ['append', 'replaceChildren']) {
          const original = Element.prototype[method];
          Element.prototype[method] = function (...items) {
            if (this.id === 'geo-map-result') __census.chunks.push(items.reduce((n, e) => n + (e.nodeType === 1 ? 1 : 0) + (e.querySelectorAll?.('*').length || 0), 0));
            const result = original.apply(this, items); __census.peak = Math.max(__census.peak, document.querySelectorAll('*').length); return result;
          };
        }
        localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(Array.from({ length: 500 }, (_, i) => ({ ip: `10.0.${Math.floor(i / 256)}.${i % 256}`, label: `Stored label ${i}`, note: '' }))));
      });
      await page.goto(base + '/#topology');
      await page.locator('#connection-settings summary').click(); await page.locator('#api-base-url').fill(base); await page.locator('#connection-settings summary').click();
      const ready = () => page.waitForFunction(() => {
        const geo = !document.querySelector('#geo-map-view').hidden, root = document.querySelector(geo ? '#geo-map-result' : '#topology-result');
        return root.getAttribute('aria-busy') === 'false' && ['ready', 'render-empty', 'render-limited'].includes(document.querySelector('#topology-workspace').dataset.state);
      });
      const view = async name => { await page.locator(`[data-view-link="${name}"]`).click(); await ready(); };
      const download = async () => {
        const pending = page.waitForEvent('download'); await page.locator('#download-topology').click();
        return fs.readFileSync(await (await pending).path(), 'utf8');
      };
      await page.locator('#run-topology').click(); await ready(); const raw = await download(); await view('geo-map');
      const root = page.locator('#geo-target-filter'), inputs = root.locator('input[data-target-index]');
      assert.equal(await inputs.count(), 3, 'Geo must expose the complete native target catalog');
      assert.deepEqual(await inputs.evaluateAll(es => es.map(e => e.checked)), [true, true, true]);
      const checkedStyle = await inputs.first().evaluate(e => ({ color: getComputedStyle(e.nextElementSibling).backgroundColor, background: getComputedStyle(e.parentElement).backgroundColor }));
      assert.equal(checkedStyle.color, `rgb(${routeColor(0).match(/\w\w/g).map(n => parseInt(n, 16)).join(', ')})`, 'Geo selected swatch uses the original target color');
      assert.equal(await root.locator('.target-toggles label').evaluateAll(es => es.every(e => e.getBoundingClientRect().height >= 44 && e.scrollWidth <= e.clientWidth)), true, 'full labels wrap inside touch-sized targets');
      const canvas = page.locator('.topology-geo-canvas');
      assert.equal(await canvas.getAttribute('data-markers'), '4');
      const rasterHash = async () => crypto.createHash('sha256').update(await canvas.evaluate(c => c.toDataURL())).digest('hex');
      const allRaster = await rasterHash();
      assert.equal(await page.locator('#topology-target-filter > *').count(), 0);
      assert.equal(await root.locator('[data-toggle-unresponsive]').count(), 0);
      const ax = await page.context().newCDPSession(page), tree = await ax.send('Accessibility.getFullAXTree');
      for (let i = 0; i < 3; i++) assert.ok(tree.nodes.some(n => n.role?.value === 'checkbox' && n.name?.value === `R${i + 1} · ${report.results[i].address}`));
      await ax.detach();
      await root.locator('[data-filter-action="none"]').click(); await ready();
      assert.equal(await inputs.count(), 3, 'None must not erase the selection controls');
      assert.deepEqual(await inputs.evaluateAll(es => es.map(e => e.checked)), [false, false, false]);
      assert.equal(await canvas.getAttribute('data-markers'), '0');
      assert.match(await root.textContent(), /0\/3개 표시/);
      const noneRaster = await rasterHash(); assert.notEqual(noneRaster, allRaster);
      assert.equal(await page.evaluate(() => document.activeElement.dataset.filterAction), 'none');
      const uncheckedStyle = await inputs.first().evaluate(e => ({ color: getComputedStyle(e.nextElementSibling).backgroundColor, background: getComputedStyle(e.parentElement).backgroundColor }));
      assert.notEqual(uncheckedStyle.color, checkedStyle.color); assert.notEqual(uncheckedStyle.background, checkedStyle.background);
      await inputs.nth(1).focus(); await page.keyboard.press('Space'); await ready();
      assert.equal(await page.evaluate(() => document.activeElement.dataset.targetIndex), '1');
      assert.equal(await canvas.getAttribute('data-markers'), '2');
      const singleRaster = await rasterHash(); assert.notEqual(singleRaster, noneRaster); assert.notEqual(singleRaster, allRaster);
      assert.deepEqual(await page.locator('.topology-geo-list button').evaluateAll(es => es.map(e => e.dataset.nodeId)), ['n2', 'n1']);
      const colors = await page.locator('.geo-target-entry').evaluateAll(es => es.map(e => +e.dataset.resultIndex)); assert.deepEqual(colors, [1]);
      const pixelCount = await canvas.evaluate((c, color) => {
        const rgb = color.match(/\w\w/g).map(n => parseInt(n, 16)), bytes = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
        let count = 0; for (let i = 0; i < bytes.length; i += 4) if (rgb.every((v, k) => bytes[i + k] === v)) count++; return count;
      }, routeColor(1)); assert.ok(pixelCount > 10, 'selected original target color is actually painted');
      await inputs.nth(2).focus(); await page.keyboard.press('Space'); await ready(); assert.equal(await canvas.getAttribute('data-markers'), '3');
      await view('topology');
      assert.equal(await root.locator('*').count(), 0);
      assert.deepEqual(await page.locator('#topology-target-filter input[data-target-index]').evaluateAll(es => es.map(e => e.checked)), [false, true, true]);
      await page.locator('label:has([name="topology-view-mode"][value="3d"])').click();
      assert.equal(await page.locator('[name="topology-view-mode"][value="3d"]').isChecked(), true);
      await page.locator('#topology-target-filter [data-filter-action="none"]').click(); await ready();
      await page.locator('#topology-target-filter input[data-target-index="1"]').focus(); await page.keyboard.press('Space'); await ready();
      await view('geo-map'); assert.deepEqual(await inputs.evaluateAll(es => es.map(e => e.checked)), [false, true, false]);
      assert.equal(await canvas.getAttribute('data-markers'), '2');
      await root.locator('[data-filter-action="all"]').click(); await ready(); assert.equal(await canvas.getAttribute('data-markers'), '4');
      assert.equal(await page.evaluate(() => document.activeElement.dataset.filterAction), 'all');
      // Native label click/tap, not forced checked state, also retains focus.
      const label = root.locator('label:has([data-target-index="0"])'); await label.scrollIntoViewIfNeeded();
      const box = await label.boundingBox(), point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
      assert.equal(await label.evaluate((e, p) => e.contains(document.elementFromPoint(p.x, p.y)), point), true, 'selector itself is not occluded');
      if (width < 800) await page.touchscreen.tap(point.x, point.y); else await page.mouse.click(point.x, point.y);
      await ready(); assert.equal(await inputs.first().isChecked(), false); assert.equal(await page.evaluate(() => document.activeElement.dataset.targetIndex), '0');
      await page.screenshot({ path: path.join(out, `selection-${width}x${height}.png`) });
      await root.locator('label:has([data-target-index="1"])').scrollIntoViewIfNeeded();
      const fullName = await root.locator('label:has([data-target-index="1"]) span').evaluate(e => {
        const r = document.createRange(); r.selectNodeContents(e);
        return [...r.getClientRects()].every(b => b.left >= 0 && b.right <= innerWidth && b.top >= 0 && b.bottom <= innerHeight && e.contains(document.elementFromPoint(b.x + b.width / 2, b.y + b.height / 2)));
      });
      assert.equal(fullName, true, 'every line of the long native label is readable without occlusion');
      await page.screenshot({ path: path.join(out, `wrapped-${width}x${height}.png`) });
      if (width === 760) {
        await page.locator('#geo-map-fullscreen').click(); await page.waitForFunction(() => document.fullscreenElement?.id === 'geo-map-view');
        await root.locator('[data-filter-action="none"]').click(); await ready();
        await inputs.nth(2).focus(); await page.keyboard.press('Space'); await ready();
        assert.deepEqual(await inputs.evaluateAll(es => es.map(e => e.checked)), [false, false, true]);
        await page.screenshot({ path: path.join(out, 'fullscreen-selector-760x600.png') });
        await page.locator('#geo-map-fullscreen').click(); await page.waitForFunction(() => !document.fullscreenElement);
      }
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await view('topology'); assert.equal(await download(), raw, 'raw export unchanged by selection and navigation');
      assert.equal(requests - before, 1, 'selection must not fetch');

      // Retained maximum forms + stored labels + twenty maximum-schema labels + 500 nodes.
      await page.locator('[data-view-link="diagnostics"]').click();
      while (await page.locator('#targets .target-row').count() < 20) await page.locator('#add-target').click();
      await view('topology'); report = fixture(true); normalizeReport(report);
      await page.locator('#run-topology').click(); await ready(); await view('geo-map');
      assert.equal(await inputs.count(), 20); assert.equal(await canvas.getAttribute('data-markers'), '500');
      const labelGeometry = await root.locator('.target-toggles label').evaluateAll(es => es.map(e => {
        const s = e.querySelector('span'), r = document.createRange(); r.selectNodeContents(s); const b = e.getBoundingClientRect();
        return { text: s.textContent, height: b.height, wrap: getComputedStyle(s).overflowWrap, fits: [...r.getClientRects()].every(x => x.left >= b.left && x.right <= b.right && x.top >= b.top && x.bottom <= b.bottom), overflow: e.scrollWidth > e.clientWidth };
      }));
      labelGeometry.forEach((e, i) => { assert.equal(e.text, `R${i + 1} · ${report.results[i].address}`); assert.equal(e.fits, true); assert.equal(e.overflow, false); assert.equal(e.wrap, 'anywhere'); });
      await inputs.last().focus(); await page.keyboard.press('Space'); await ready();
      assert.equal(await inputs.last().isChecked(), false); assert.equal(await page.evaluate(() => document.activeElement.dataset.targetIndex), '19');
      await page.screenshot({ path: path.join(out, `maximum-label-${width}x${height}.png`) });
      await root.locator('[data-filter-action="all"]').click(); await ready();
      assert.equal(await canvas.getAttribute('data-markers'), '500');
      const seen = new Set(); for (let i = 0; i < 5; i++) {
        (await page.locator('.topology-geo-list button:not([hidden])').evaluateAll(es => es.map(e => e.dataset.nodeId))).forEach(id => seen.add(id));
        if (i < 4) await page.locator('#geo-page-next').click();
      }
      assert.equal(seen.size, 500);
      const census = await page.evaluate(() => ({ ...__census, elements: document.querySelectorAll('*').length, slots: document.querySelectorAll('.topology-geo-list button').length,
        hiddenFilter: document.querySelector('#topology-target-filter').childElementCount, hiddenLabels: document.querySelector('#ip-label-rows').childElementCount, overflow: document.documentElement.scrollWidth > innerWidth }));
      assert.ok(census.peak <= 1200, JSON.stringify(census)); assert.ok(Math.max(...census.chunks) <= 100); assert.equal(census.slots, 100); assert.equal(census.hiddenFilter, 0); assert.equal(census.hiddenLabels, 0); assert.equal(census.overflow, false);
      // Full inventory exists even when no result has coordinates or any observed route.
      for (const failed of [false, true]) {
        report = fixture(false, true);
        if (failed) { delete report.compact_topology; report.status = 'unreachable'; report.summary = { total: 3, passed: 0, failed: 3 };
          for (const result of report.results) { delete result.details; result.status = 'unreachable'; result.error_code = 'traceroute_unavailable'; }
        }
        normalizeReport(report); await view('topology'); await page.locator('#run-topology').click(); await ready(); await view('geo-map');
        assert.equal(await inputs.count(), 3); assert.equal(await canvas.getAttribute('data-markers'), '0');
        await root.locator('[data-filter-action="none"]').click(); await ready(); await inputs.nth(1).focus(); await page.keyboard.press('Space'); await ready();
        assert.equal(await inputs.count(), 3); assert.deepEqual(await inputs.evaluateAll(es => es.map(e => e.checked)), [false, true, false]);
      }
      assert.equal(requests - before, 4, 'only explicit Run actions fetch reports'); assert.deepEqual(errors, []); assert.deepEqual(external, []);
      runs.push({ width, height, pixelCount, allRaster, noneRaster, singleRaster, requests: requests - before, census, maximumLabelLengths: labelGeometry.map(e => e.text.length), errors, external }); await page.close();
    }
    const budgetCases = [];
    for (const view of ['geo-map', 'topology']) for (const capacity of ['one-slot-deficit', 'zero-remaining', 'exact-fit']) {
      report = fixture(false, true); delete report.compact_topology;
      report.results = report.results.slice(0, 1); report.status = 'unreachable'; report.summary = { total: 1, passed: 0, failed: 1 };
      delete report.results[0].details; report.results[0].status = 'unreachable'; report.results[0].error_code = 'traceroute_unavailable';
      normalizeReport(report);
      const beforeRequests = requests, page = await browser.newPage({ viewport: { width: 375, height: 900 }, hasTouch: true }), errors = [], external = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.route('**/*', r => { if (!r.request().url().startsWith(base + '/')) { external.push(r.request().url()); return r.abort(); } return r.continue(); });
      await page.goto(base + '/#topology');
      await page.locator('#connection-settings summary').click(); await page.locator('#api-base-url').fill(base); await page.locator('#connection-settings summary').click();
      const ready = () => page.waitForFunction(() => ['ready', 'render-empty', 'render-limited'].includes(document.querySelector('#topology-workspace').dataset.state) && document.querySelector('#topology-workspace').getAttribute('aria-busy') === 'false');
      const navigate = async target => { await page.locator(`[data-view-link="${target}"]`).click(); if (target !== 'diagnostics') await ready(); };
      const download = async () => { const pending = page.waitForEvent('download'); await page.locator('#download-topology').click(); return fs.readFileSync(await (await pending).path(), 'utf8'); };
      await page.locator('#run-topology').click(); await ready(); const raw = await download(); await navigate(view);
      const filter = page.locator(view === 'geo-map' ? '#geo-target-filter' : '#topology-target-filter');
      const catalogElements = await filter.locator('*').count(); assert.equal(catalogElements, view === 'geo-map' ? 11 : 14);
      await filter.locator('[data-filter-action="none"]').click(); await ready(); await navigate('diagnostics');
      const before = await page.evaluate(({ catalogElements, capacity }) => {
        const ballast = document.createElement('div'); ballast.id = 'selection-budget-ballast'; document.body.append(ballast);
        const desired = capacity === 'zero-remaining' ? 1200 : 1200 - catalogElements + (capacity === 'one-slot-deficit' ? 1 : 0);
        while (document.querySelectorAll('*').length < desired) ballast.append(document.createElement('i'));
        window.__budgetPeak = document.querySelectorAll('*').length;
        for (const method of ['append', 'replaceChildren']) {
          const original = Element.prototype[method];
          Element.prototype[method] = function (...items) { const result = original.apply(this, items); __budgetPeak = Math.max(__budgetPeak, document.querySelectorAll('*').length); return result; };
        }
        return document.querySelectorAll('*').length;
      }, { catalogElements, capacity });
      await navigate(view);
      const observation = await page.evaluate(view => {
        const geo = view === 'geo-map', filter = document.querySelector(geo ? '#geo-target-filter' : '#topology-target-filter');
        const root = document.querySelector(geo ? '#geo-map-result' : '#topology-result');
        return { elements: document.querySelectorAll('*').length, peak: __budgetPeak, catalogElements: filter.querySelectorAll('*').length, text: filter.textContent,
          state: document.querySelector('#topology-workspace').dataset.state, busy: root.getAttribute('aria-busy'), mapElements: root.childElementCount,
          status: document.querySelector(geo ? '#geo-render-status' : '#topology-render-status').textContent };
      }, view);
      budgetCases.push({ view, capacity, before, ...observation });
      fs.writeFileSync(path.join(out, 'budget-observations.json'), JSON.stringify(budgetCases, null, 2) + '\n');
      assert.ok(observation.peak <= 1200, JSON.stringify(observation));
      assert.equal(observation.state, 'render-limited'); assert.equal(observation.busy, 'false'); assert.equal(observation.mapElements, 0);
      if (capacity === 'exact-fit') {
        assert.equal(observation.elements, 1200); assert.equal(observation.catalogElements, catalogElements);
        for (const action of ['all', 'none']) { await filter.locator(`[data-filter-action="${action}"]`).click(); await ready(); assert.equal(await page.evaluate(() => document.querySelectorAll('*').length), 1200); }
      } else {
        assert.equal(observation.elements, before); assert.equal(observation.catalogElements, 0);
        assert.match(observation.text, /문서 요소 한도.*선택/); assert.match(observation.status, /선택.*유지.*다시/);
      }
      await page.screenshot({ path: path.join(out, `budget-${view}-${capacity}.png`) });
      // Release only test-owned ballast, then recover the retained all-off selection by navigation.
      await page.evaluate(() => document.querySelector('#selection-budget-ballast').remove());
      await navigate('diagnostics'); await navigate(view);
      assert.equal(await filter.locator('*').count(), catalogElements);
      assert.equal(await filter.locator('[data-target-index]').isChecked(), false);
      assert.doesNotMatch(await filter.textContent(), /문서 요소 한도/);
      await filter.locator('[data-filter-action="all"]').click(); await ready();
      assert.equal(await filter.locator('[data-target-index]').isChecked(), true);
      if (view === 'geo-map') assert.equal(await page.locator('#geo-map-result canvas').count(), 1);
      await navigate('topology'); assert.equal(await download(), raw);
      assert.equal(requests - beforeRequests, 1); assert.deepEqual(errors, []); assert.deepEqual(external, []);
      assert.ok(await page.evaluate(() => __budgetPeak <= 1200)); await page.close();
    }
    fs.writeFileSync(path.join(out, 'results.json'), JSON.stringify({ browser: browser.version(), root: __dirname, source, runs, budgetCases }, null, 2) + '\n');
    console.log('Geo target selection actual-app PASS', runs.length, 'viewports;', budgetCases.length, 'admission cases');
  } finally {
    await browser?.close(); server.closeAllConnections(); await new Promise(r => server.close(r));
    fs.writeFileSync(path.join(out, 'cleanup.json'), JSON.stringify({ pid: process.pid, base, serverListening: server.listening, browserConnected: browser?.isConnected() ?? false }) + '\n');
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
