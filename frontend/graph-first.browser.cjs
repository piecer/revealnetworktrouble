// Optional real-Chromium gate; Playwright stays outside production dependencies.
// PLAYWRIGHT_PATH=/path/to/playwright node frontend/graph-first.browser.cjs URL OUTPUT_DIR [REPORT_LOG]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');

(async () => {
  const [base, output, log] = process.argv.slice(2);
  assert.ok(base && output, 'URL and output directory required');
  const reports = [['fixture', fs.readFileSync(path.join(__dirname, '../android/app/src/test/resources/core/compact-traceroute-report.json'), 'utf8')]];
  reports.push(['linked-fixture', fs.readFileSync(path.join(__dirname, '../testdata/traceroute-command-failure-compact-report.json'), 'utf8')]);
  if (log) {
    const line = fs.readFileSync(log, 'utf8').split('\n').find(line => line.startsWith('REPORT '));
    assert.ok(line, 'captured REPORT line required');
    reports.push(['captured', line.slice(7)]);
  }
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch();
  const results = [];
  const failures = [];
  try {
    for (const [name, body] of reports) for (const width of [1440, 375]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      let requests = 0;
      const errors = [];
      await page.addInitScript(() => {
        const proto = CanvasRenderingContext2D.prototype;
        for (const name of ['clearRect', 'beginPath', 'moveTo', 'lineTo', 'stroke']) {
          const original = proto[name];
          proto[name] = function (...args) {
            if (name === 'clearRect') this.canvas.graphStrokes = [];
            if (name === 'beginPath') this.graphPath = [];
            if (name === 'moveTo' || name === 'lineTo') this.graphPath?.push(args);
            if (name === 'stroke' && this.graphPath?.length === 2 && this.strokeStyle !== '#17243a') {
              this.canvas.graphStrokes?.push({ points: this.graphPath, color: this.strokeStyle });
            }
            return original.apply(this, args);
          };
        }
      });
      page.on('pageerror', error => errors.push(error.message));
      await page.route('**/api/v1/reports', route => {
        requests++;
        return route.fulfill({ status: 200, contentType: 'application/json', body });
      });
      await page.goto(`${base}/#topology`);
      await page.locator('#run-topology').click();
      await page.waitForFunction(() => document.querySelector('canvas.topology-canvas')?.dataset.drawState === 'rendered' && document.querySelector('#topology-result')?.getAttribute('aria-busy') === 'false');
      for (const mode of ['2d', '3d']) {
        await page.locator(`[name="topology-view-mode"][value="${mode}"]`).check();
        await page.locator('#topology-view-reset').click();
        await page.locator('canvas.topology-canvas').scrollIntoViewIfNeeded();
        await page.waitForTimeout(150); // settle scheduled resize; no network probes
        const result = await page.evaluate(() => {
          const root = document.querySelector('#topology-result');
          const canvas = root.querySelector('canvas');
          const inspector = root.querySelector('details');
          const rect = canvas.getBoundingClientRect();
          const ctx = canvas.getContext('2d');
          const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
          let colored = 0;
          for (let i = 0; i < pixels.length; i += 4) {
            if ((pixels[i] === 34 && pixels[i+1] === 197 && pixels[i+2] === 94) ||
                (pixels[i] === 245 && pixels[i+1] === 158 && pixels[i+2] === 11) ||
                (pixels[i] === 239 && pixels[i+1] === 68 && pixels[i+2] === 68)) colored++;
          }
          const dpr = Number(canvas.dataset.dpr);
          const paintedLinks = (canvas.graphStrokes || []).filter(({ points: [from, to], color }) => {
            if (Math.hypot(to[0] - from[0], to[1] - from[1]) < 40) return false;
            const x = Math.round((from[0] + to[0]) / 2 * dpr), y = Math.round((from[1] + to[1]) / 2 * dpr);
            const rgb = color.slice(1).match(/../g).map(v => parseInt(v, 16));
            for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) {
              const offset = ((y + dy) * canvas.width + x + dx) * 4;
              if (rgb.every((v, i) => pixels[offset + i] === v)) return true;
            }
            return false;
          }).length;
          return {
            paintedLinks,
            mode: canvas.dataset.mode, colored, canvas: rect.toJSON(),
            visible: canvas.checkVisibility() && rect.bottom > 0 && rect.top < innerHeight,
            closed: inspector?.open === false,
            visibleCards: [...root.querySelectorAll('.topology-node,.topology-link,.topology-route')].filter(n => n.checkVisibility()).length,
            count: document.querySelectorAll('*').length,
            overflow: document.documentElement.scrollWidth > innerWidth,
            first: root.firstElementChild === canvas
          };
        });
        results.push({ name, width, requests, errors, ...result });
        await page.screenshot({ path: path.join(output, `${name}-${width}-${mode}.png`) });
        try {
          assert.equal(result.mode, mode);
          assert.ok(result.first && result.closed && result.visible);
          assert.equal(result.visibleCards, 0);
          assert.ok(result.colored > 100, 'status-colored graph pixels');
          if (name !== 'fixture') assert.ok(result.paintedLinks > 0, 'actual colored pixels at directed-link midpoints');
          assert.ok(result.canvas.width > 200 && result.canvas.height >= 220);
          assert.equal(result.overflow, false, 'no horizontal overflow');
          assert.ok(result.count <= 1200);
          assert.equal(requests, 1, 'mode/reset never request new report');
          assert.deepEqual(errors, []);
        } catch (error) { failures.push(`${name}/${width}/${mode}: ${error.message}`); }
      }
      const summary = page.locator('#topology-result details > summary');
      await summary.focus();
      await page.keyboard.press('Enter');
      assert.ok(await page.locator('#topology-result details').evaluate(n => n.open), 'native keyboard expands inspector');
      assert.ok(await page.locator('#topology-result .topology-node').first().isVisible());
      assert.equal(requests, 1);
      await page.close();
    }
  } finally {
    await browser.close();
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ results, failures }, null, 2));
  }
  console.log(JSON.stringify({ results, failures }, null, 2));
  assert.deepEqual(failures, []);
})().catch(error => { console.error(error); process.exitCode = 1; });
