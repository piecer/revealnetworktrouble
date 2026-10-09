// Optional real-Chromium gate; Playwright stays outside production dependencies.
// PLAYWRIGHT_PATH=/path/to/playwright node frontend/graph-first.browser.cjs URL OUTPUT_DIR [REPORT_LOG]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');

function assertPaintedGraph(result, needsLinks) {
  assert.ok(result.colored > 100, 'latency-colored graph pixels');
  assert.ok(result.paintedNodes > 0, 'actual pixels inside recorded circular nodes');
  if (needsLinks) assert.ok(result.paintedLinks > 0, 'actual colored pixels at directed-link midpoints');
}

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
        for (const name of ['clearRect', 'beginPath', 'moveTo', 'lineTo', 'quadraticCurveTo', 'stroke', 'arc']) {
          const original = proto[name];
          proto[name] = function (...args) {
            if (name === 'clearRect') { this.canvas.graphStrokes = []; this.canvas.graphNodes = []; }
            if (name === 'arc' && /^#[0-9a-f]{6}$/i.test(this.fillStyle)) {
              this.canvas.graphNodes?.push({ x: args[0], y: args[1], radius: args[2], color: this.fillStyle });
            }
            if (name === 'beginPath') this.graphPath = [];
            if (name === 'moveTo' || name === 'lineTo') this.graphPath?.push(args);
            if (name === 'quadraticCurveTo') this.graphPath?.push(args.slice(0, 2), args.slice(2));
            if (name === 'stroke' && [2, 3].includes(this.graphPath?.length) && this.strokeStyle !== '#17243a') {
              this.canvas.graphStrokes?.push({ points: this.graphPath, color: this.strokeStyle });
            }
            return original.apply(this, args);
          };
        }
      });
      page.on('pageerror', error => errors.push(error.message));
      await page.route('**/api/v1/reports?geo_details=1', route => {
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
        const inspectGraph = ({ blank = false } = {}) => {
          const root = document.querySelector('#topology-result');
          const canvas = root.querySelector('canvas');
          const inspector = root.querySelector('details');
          const rect = canvas.getBoundingClientRect();
          const ctx = canvas.getContext('2d');
          if (blank) {
            // Keep draw-state, semantic DOM and recorded commands intact: the
            // oracle must reject missing pixels even when those all look ready.
            const strokes = canvas.graphStrokes, nodes = canvas.graphNodes;
            ctx.save(); ctx.setTransform(1, 0, 0, 1, 0, 0);
            ctx.clearRect(0, 0, canvas.width, canvas.height); ctx.restore();
            canvas.graphStrokes = strokes; canvas.graphNodes = nodes;
          }
          const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
          const nodeColors = new Set(['82,224,177', '201,255,70', '255,184,77', '255,113,133', '148,163,184']);
          let colored = 0;
          for (let i = 0; i < pixels.length; i += 4) {
            if (pixels[i+3] && nodeColors.has(`${pixels[i]},${pixels[i+1]},${pixels[i+2]}`)) colored++;
          }
          const dpr = Number(canvas.dataset.dpr);
          const paintedNodes = (canvas.graphNodes || []).filter(node => {
            const rgb = node.color.slice(1).match(/../g).map(v => parseInt(v, 16));
            const x = Math.round((node.x + node.radius / 2) * dpr), y = Math.round((node.y + node.radius / 3) * dpr);
            for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) {
              if (x + dx < 0 || x + dx >= canvas.width || y + dy < 0 || y + dy >= canvas.height) continue;
              const offset = ((y + dy) * canvas.width + x + dx) * 4;
              if (pixels[offset+3] && rgb.every((v, i) => pixels[offset+i] === v)) return true;
            }
            return false;
          }).length;
          const paintedLinks = (canvas.graphStrokes || []).filter(({ points, color }) => {
            const from = points[0], to = points.at(-1);
            if (Math.hypot(to[0] - from[0], to[1] - from[1]) < 40) return false;
            const midpoint = axis => points.length === 3 ? (from[axis] + 2 * points[1][axis] + to[axis]) / 4 : (from[axis] + to[axis]) / 2;
            const x = Math.round(midpoint(0) * dpr), y = Math.round(midpoint(1) * dpr);
            const rgb = color.slice(1).match(/../g).map(v => parseInt(v, 16));
            for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) {
              if (x + dx < 0 || x + dx >= canvas.width || y + dy < 0 || y + dy >= canvas.height) continue;
              const offset = ((y + dy) * canvas.width + x + dx) * 4;
              if (rgb.every((v, i) => pixels[offset + i] === v)) return true;
            }
            return false;
          }).length;
          return {
            paintedLinks, paintedNodes, recordedNodes: canvas.graphNodes?.length,
            drawState: canvas.dataset.drawState,
            semanticNodes: root.querySelectorAll('.topology-node').length,
            mode: canvas.dataset.mode, colored, canvas: rect.toJSON(),
            visible: canvas.checkVisibility() && rect.bottom > 0 && rect.top < innerHeight,
            closed: inspector?.open === false,
            visibleCards: [...root.querySelectorAll('.topology-node,.topology-link,.topology-route')].filter(n => n.checkVisibility()).length,
            count: document.querySelectorAll('*').length,
            overflow: document.documentElement.scrollWidth > innerWidth,
            first: root.firstElementChild === canvas
          };
        };
        const result = await page.evaluate(inspectGraph);
        results.push({ name, width, requests, errors, ...result });
        await page.screenshot({ path: path.join(output, `${name}-${width}-${mode}.png`) });
        try {
          assert.equal(result.mode, mode);
          assert.ok(result.first && result.closed && result.visible);
          assert.equal(result.visibleCards, 0);
          assertPaintedGraph(result, name !== 'fixture');
          assert.ok(result.canvas.width > 200 && result.canvas.height >= 220);
          assert.equal(result.overflow, false, 'no horizontal overflow');
          assert.ok(result.count <= 1200);
          assert.equal(requests, 1, 'mode/reset never request new report');
          assert.deepEqual(errors, []);
          const blank = await page.evaluate(inspectGraph, { blank: true });
          assert.equal(blank.drawState, 'rendered');
          assert.equal(blank.recordedNodes, result.recordedNodes);
          assert.equal(blank.semanticNodes, result.semanticNodes);
          assert.equal(blank.colored, 0);
          assert.throws(() => assertPaintedGraph(blank, name !== 'fixture'), /latency-colored graph pixels/);
          results.at(-1).blankCanaryRejected = true;
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
