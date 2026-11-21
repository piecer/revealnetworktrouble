// Optional acceptance for the real embedded API binary; no production service is restarted.
// PLAYWRIGHT_PATH=/path/to/playwright make web-test-embedded-browser [BROWSER_OUTPUT_DIR=/path]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');
const readline = require('node:readline');
const root = path.resolve(__dirname, '..');
const binary = path.resolve(process.argv[2] || path.join(root, 'bin/checknetwork-api'));
const parent = process.argv[3] || os.tmpdir();
fs.mkdirSync(parent, { recursive: true });
const output = fs.mkdtempSync(path.join(parent, 'checknetwork-embedded-'));
const receipt = { passed: false, binary, cases: [], errors: [], output };
const children = new Set();

async function startAPI(mode = 'trusted-local') {
  // Never inherit deployment overrides or real credentials into a disposable process.
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('CHECKNETWORK_')));
  Object.assign(env, { CHECKNETWORK_ADDR: '127.0.0.1:0', CHECKNETWORK_WEB_UI: 'on', CHECKNETWORK_MODE: mode });
  if (mode === 'public') Object.assign(env, { CHECKNETWORK_API_KEY: 'test-only-embedded-key', CHECKNETWORK_RATE_LIMIT_PER_MINUTE: '100' });
  const child = spawn(binary, [], { env, cwd: output, stdio: ['ignore', 'pipe', 'pipe'] });
  children.add(child);
  child.done = new Promise(resolve => child.once('close', (code, signal) => resolve({ code, signal })));
  const log = fs.createWriteStream(path.join(output, `${mode}.log`), { flags: 'wx' });
  child.stdout.pipe(log, { end: false }); child.stderr.pipe(log, { end: false });
  child.once('close', () => log.end());
  const lines = readline.createInterface({ input: child.stdout });
  try {
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`${mode}: startup timeout`)), 15000);
      child.once('error', reject);
      child.once('exit', code => { clearTimeout(timer); reject(new Error(`${mode}: exited before startup (${code})`)); });
      lines.on('line', line => {
        try {
          const record = JSON.parse(line);
          if (record.msg === 'server started') { clearTimeout(timer); resolve(`http://${record.address}`); }
        } catch { /* Non-startup logs are retained as evidence. */ }
      });
    });
  } finally { lines.close(); }
}

(async () => {
  // Preflight the matching package/browser before provisioning any API resources.
  const browser = await chromium.launch();
  receipt.browser = browser.version();
  const fixture = http.createServer((_request, response) => { response.end('owned local TCP/HTTP fixture'); });
  let fixtureOpen = false;
  try {
    await new Promise(resolve => fixture.listen(0, '127.0.0.1', resolve)); fixtureOpen = true;
    const target = `127.0.0.1:${fixture.address().port}`;
    const base = await startAPI(); receipt.origin = base;
    const topology = fs.readFileSync(path.join(root, 'testdata/geo-details-rich-compact-report.json'), 'utf8');
    for (const width of [1440, 375]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = [], requests = [];
      page.on('pageerror', error => errors.push(error.message));
      page.on('request', request => requests.push(request.url()));
      await page.addInitScript(() => {
        window.cspViolations = [];
        document.addEventListener('securitypolicyviolation', event => window.cspViolations.push({ directive: event.effectiveDirective, blockedURI: event.blockedURI }));
      });
      const response = await page.goto(base);
      assert.equal(response.status(), 200);
      assert.match(response.headers()['content-security-policy'], /connect-src 'self'/);
      assert.equal(await page.locator('#api-base-url').inputValue(), base);
      assert.equal(await page.locator('#api-base-url').getAttribute('readonly'), '');
      while (await page.locator('#targets .target-row').count() > 1) await page.locator('#targets .target-row').last().locator('button').click();
      const row = page.locator('#targets .target-row').first();
      await row.locator('select').selectOption('tcp');
      await row.locator('input').first().fill(target);
      const reportResponse = page.waitForResponse(r => r.url() === `${base}/api/v1/reports?geo_details=1` && r.request().method() === 'POST');
      await page.locator('#run').click();
      const actual = await reportResponse;
      assert.equal(actual.status(), 200);
      const report = await actual.json();
      assert.equal(report.results[0].status, 'healthy');
      assert.equal(report.results[0].kind, 'tcp');
      await page.waitForFunction(() => document.querySelector('#diagnostics-workspace').dataset.state === 'ready');
      receipt.cases.push({ width, view: 'diagnostics', source: 'real Go API + owned local TCP endpoint', status: report.results[0].status });
      await page.screenshot({ path: path.join(output, `diagnostics-${width}.png`), fullPage: true });
      // Topology/Geo below intentionally replay a producer fixture, never claim live path measurements.
      await page.route(`${base}/api/v1/reports?geo_details=1`, route => route.fulfill({ status: 200, contentType: 'application/json', body: topology }));
      await page.locator('[data-view-link="topology"]').click();
      await page.locator('#topology-targets').fill('2606:4700:4700::1111');
      await page.locator('#topology-attempts').fill('2');
      await page.locator('#run-topology').click();
      await page.waitForFunction(() => document.querySelector('#topology-workspace').dataset.state === 'ready' && document.querySelector('canvas.topology-canvas')?.dataset.drawState === 'rendered');
      for (const mode of ['2d', '3d']) {
        await page.locator(`[name="topology-view-mode"][value="${mode}"]`).check();
        receipt.cases.push({ width, view: `topology-${mode}`, source: 'producer fixture replay' });
      }
      await page.screenshot({ path: path.join(output, `topology-${width}.png`), fullPage: true });
      await page.locator('[data-view-link="geo-map"]').click();
      await page.waitForFunction(() => Number(document.querySelector('.topology-geo-canvas')?.dataset.markers) > 0);
      receipt.cases.push({ width, view: 'geo-map', source: 'producer fixture replay' });
      await page.locator('[data-view-link="ip-labels"]').click();
      receipt.cases.push({ width, view: 'ip-labels', source: 'local browser UI' });
      await page.locator('[data-view-link="common-prefix"]').click();
      await page.locator('#common-prefix-input').fill('192.0.2.1\n192.0.2.129');
      await page.locator('#run-common-prefix').click();
      await page.locator('#common-prefix-report-section').waitFor({ state: 'visible' });
      receipt.cases.push({ width, view: 'common-prefix', source: 'local browser calculation' });
      const metric = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, width: innerWidth, elements: document.querySelectorAll('*').length, violations: window.cspViolations }));
      assert.ok(metric.scrollWidth <= width, `horizontal overflow at ${width}`);
      assert.ok(metric.elements <= 1200, `document budget at ${width}`);
      assert.deepEqual(metric.violations, [], 'normal operation must not violate CSP');
      assert.deepEqual(errors, [], 'no script errors');
      assert.ok(requests.every(url => new URL(url).origin === base), 'no third-party browser requests');
      // Deliberate probes verify the policy, separately from the normal-operation census.
      const blocked = await page.evaluate(async other => {
        window.inlineScriptRan = false;
        const script = document.createElement('script'); script.textContent = 'window.inlineScriptRan = true'; document.head.append(script);
        try { await fetch(other); return false; } catch { return !window.inlineScriptRan; }
      }, `http://${target}/blocked-by-csp`);
      assert.equal(blocked, true, 'CSP blocks inline scripts and cross-origin fetch');
      await page.waitForFunction(() => window.cspViolations.some(v => v.directive === 'connect-src') && window.cspViolations.some(v => v.directive === 'script-src-elem'));
      await page.close();
    }
    const publicBase = await startAPI('public');
    const page = await browser.newPage();
    await page.goto(publicBase);
    assert.equal(await page.locator('#api-base-url').inputValue(), publicBase);
    const auth = await page.evaluate(async () => {
      const denied = await fetch('/api/v1/health');
      const allowed = await fetch('/api/v1/health', { headers: { Authorization: 'Bearer test-only-embedded-key' } });
      return { denied: denied.status, allowed: allowed.status };
    });
    assert.deepEqual(auth, { denied: 401, allowed: 200 });
    receipt.cases.push({ view: 'public-mode', source: 'real API, public UI and preserved API Bearer auth', ...auth });
    await page.close();
    assert.equal(receipt.cases.length, 13, 'all viewports/views plus public auth must execute');
    receipt.passed = true;
  } finally {
    await browser.close();
    if (fixtureOpen) { fixture.closeAllConnections(); await new Promise(resolve => fixture.close(resolve)); }
    receipt.children = [];
    for (const child of children) {
      child.kill('SIGTERM');
      let timer;
      try {
        const result = await Promise.race([child.done, new Promise(resolve => { timer = setTimeout(() => { child.kill('SIGKILL'); resolve({ cleanupTimedOut: true }); }, 5000); })]);
        receipt.children.push(result);
        if (result.code !== 0) receipt.passed = false;
      } finally { clearTimeout(timer); }
    }
    fs.writeFileSync(path.join(output, 'receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
    console.log(JSON.stringify(receipt, null, 2));
    assert.ok(receipt.children.every(result => result.code === 0), 'all owned API processes must stop normally');
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
