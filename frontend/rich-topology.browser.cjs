// Real Chromium, no production dependency. Private static server + intercepted report replay.
// PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/rich-topology.browser.cjs URL OUTPUT [REPORT_LOG]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
(async () => {
  const [base, output, log] = process.argv.slice(2);
  const reports = [['fixture', fs.readFileSync(path.join(__dirname, '../testdata/traceroute-command-failure-compact-report.json'), 'utf8')]];
  if (log) {
    const line = fs.readFileSync(log, 'utf8').split('\n').find(line => line.startsWith('REPORT '));
    assert.ok(line); reports.push(['captured', line.slice(7)]);
  }
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch();
  const results = [];
  try {
    for (const [name, body] of reports) for (const width of [1440, 375]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = []; let requests = 0;
      page.on('pageerror', error => errors.push(error.message));
      await page.addInitScript(() => {
        const proto = CanvasRenderingContext2D.prototype;
        for (const method of ['clearRect', 'arc', 'moveTo', 'quadraticCurveTo', 'fillText']) {
          const original = proto[method];
          proto[method] = function (...args) {
            if (method === 'clearRect') this.canvas.probe = { nodes: [], curves: [], labels: [], labelBoxes: [] };
            const p = this.canvas.probe;
            if (p && method === 'arc' && /^#[0-9a-f]{6}$/i.test(this.fillStyle)) p.nodes.push(args.slice(0, 3));
            if (method === 'moveTo') this.probeFrom = args;
            if (p && method === 'quadraticCurveTo') p.curves.push({ from: this.probeFrom, control: args.slice(0, 2), to: args.slice(2), color: this.strokeStyle });
            if (p && method === 'fillText') {
              p.labels.push(args[0]);
              const [text, x, y, maxWidth] = args, metrics = this.measureText(text);
              const scale = Math.min(1, maxWidth / metrics.width);
              p.labelBoxes.push({ left: x - metrics.actualBoundingBoxLeft * scale,
                right: x + metrics.actualBoundingBoxRight * scale,
                top: y - metrics.actualBoundingBoxAscent, bottom: y + metrics.actualBoundingBoxDescent });
            }
            return original.apply(this, args);
          };
        }
      });
      await page.route('**/api/v1/reports', route => { requests++; return route.fulfill({ status: 200, contentType: 'application/json', body }); });
      await page.goto(base + '/#topology');
      await page.locator('#run-topology').click();
      const ready = () => page.waitForFunction(() => document.querySelector('canvas.topology-canvas')?.dataset.drawState === 'rendered' && document.querySelector('#topology-result').getAttribute('aria-busy') === 'false');
      await ready();
      const canvas = page.locator('canvas.topology-canvas');
      await canvas.scrollIntoViewIfNeeded(); await page.waitForTimeout(150);
      const get = async () => {
        const result = await canvas.evaluate(c => ({ ...c.probe, box: c.getBoundingClientRect().toJSON() }));
        for (let i = 0; i < result.labelBoxes.length; i++) for (let j = i + 1; j < result.labelBoxes.length; j++) {
          const a = result.labelBoxes[i], b = result.labelBoxes[j];
          assert.ok(a.right <= b.left || b.right <= a.left || a.bottom <= b.top || b.bottom <= a.top, `${name}/${width}: actual glyph bounds do not overlap`);
        }
        return result;
      };
      let before = await get();
      assert.ok(before.nodes.length > 1 && before.curves.length > 0);
      // Choose an IP (not the uneditable local origin), preferably a linked node.
      const items = await page.locator('.topology-node').evaluateAll(nodes => nodes.map(n => ({ id: n.dataset.nodeId, ip: n.dataset.labelAddress })).sort((a, b) => a.id < b.id ? -1 : 1));
      const index = items.findIndex(n => n.ip);
      assert.ok(index >= 0);
      const [x, y] = before.nodes[index];
      await page.mouse.move(before.box.x + x, before.box.y + y);
      const tip = page.locator('#topology-node-tooltip');
      assert.equal(await tip.isVisible(), true, 'graph hover exposes bounded tooltip');
      assert.ok((await tip.textContent()).includes(items[index].ip));
      assert.match(await tip.textContent(), /HOP|observations/);
      await page.mouse.down();
      const destination = [[35, 30], [-35, -30], [0, -60], [0, 60]].find(([dx, dy]) =>
        x + dx > 16 && x + dx < before.box.width - 16 && y + dy > 16 && y + dy < before.box.height - 16 &&
        before.nodes.every((node, i) => i === index || Math.hypot(node[0] - x - dx, node[1] - y - dy) > 20));
      assert.ok(destination, 'choose a free drop point rather than cover another node');
      const [dx, dy] = destination;
      await page.mouse.move(before.box.x + x + dx, before.box.y + y + dy, { steps: 4 });
      await page.mouse.up();
      const after = await get();
      for (let i = 0; i < before.nodes.length; i++) {
        if (i === index) {
          assert.ok(Math.abs(after.nodes[i][0] - before.nodes[i][0] - dx) < 1);
          assert.ok(Math.abs(after.nodes[i][1] - before.nodes[i][1] - dy) < 1);
        } else assert.deepEqual(after.nodes[i], before.nodes[i], 'other nodes do not pan');
      }
      const attached = before.curves.map((c, i) => ({ c, i })).filter(({ c }) => (c.from[0] === x && c.from[1] === y) || (c.to[0] === x && c.to[1] === y));
      assert.ok(attached.length);
      for (const { c, i } of attached) {
        const endpoint = c.from[0] === x && c.from[1] === y ? 'from' : 'to';
        assert.ok(Math.abs(after.curves[i][endpoint][0] - c[endpoint][0] - dx) < 1, 'attached curve follows dragged node');
      }
      await page.mouse.click(after.box.x + after.nodes[index][0], after.box.y + after.nodes[index][1]);
      assert.equal(await page.locator('#topology-label-address').inputValue(), items[index].ip, `${name}/${width} dragged node editor`);
      assert.equal(await page.locator('#topology-label-name').evaluate(n => n === document.activeElement), true);
      const alias = '<img src=x onerror=alert(1)>', note = 'Local note <svg onload=alert(1)>';
      await page.locator('#topology-label-name').fill(alias);
      await page.locator('#topology-label-note').fill(note);
      await page.locator('#topology-label-form button[type=submit]').click();
      await ready();
      for (const mode of ['2d', '3d']) {
        await page.locator(`[name=topology-view-mode][value="${mode}"]`).check();
        await canvas.scrollIntoViewIfNeeded(); await page.waitForTimeout(100);
        const current = await get();
        assert.ok(current.labels.includes(alias), 'saved alias is drawn in both modes');
        assert.equal(await page.locator('#topology-result img, #topology-node-tooltip svg').count(), 0);
        const annotated = page.locator(`.topology-node[data-node-id="${items[index].id}"]`);
        assert.ok((await annotated.textContent()).includes(note));
        assert.ok((await annotated.getAttribute('aria-label')).includes(items[index].ip));
        assert.equal(requests, 1, 'editing and switching modes reuse cached report');
        await page.screenshot({ path: path.join(output, `${name}-${width}-${mode}.png`) });
      }
      await canvas.focus();
      await page.keyboard.press(']'); await page.keyboard.press('Enter');
      assert.equal(await page.locator('#topology-label-name').evaluate(n => n === document.activeElement), true, 'keyboard opens editor');
      const count = await page.locator('*').count(); assert.ok(count <= 1200);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      // Annotation persistence across a real reload; analysis facts are replayed unchanged.
      await page.reload(); await page.locator('#run-topology').click(); await ready();
      assert.ok((await get()).labels.includes(alias));
      const stale = await canvas.elementHandle();
      await page.evaluate(() => { location.hash = '#ip-labels'; });
      await page.waitForTimeout(100);
      assert.equal(await page.locator('canvas.topology-canvas').count(), 0);
      await stale.evaluate(c => c.dispatchEvent(new MouseEvent('pointermove', { clientX: 32, clientY: 240, bubbles: true })));
      assert.equal(await tip.isVisible(), false, 'stale detached canvas cannot reopen tooltip');
      assert.deepEqual(errors, []);
      results.push({ name, width, nodes: before.nodes.length, curves: before.curves.length, count, requests, errors });
      await page.close();
    }
  } finally { await browser.close(); fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(results, null, 2)); }
  console.log(JSON.stringify(results, null, 2));
})().catch(error => { console.error(error); process.exitCode = 1; });
