// Run through make web-test-cors-browser: real Go handlers, no intercepted API traffic.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const urls = JSON.parse(process.argv[2]);
const watchdog = setTimeout(() => { console.error('CORS browser gate timed out'); process.exit(1); }, 50000);
(async () => {
  let browser;
  try {
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    const errors = []; page.on('pageerror', error => errors.push(error.message));
    await page.goto(urls.ui);
    const results = await page.evaluate(async urls => {
      const { parseResponse } = await import('/state.js');
      const post = api => fetch(api + '/api/v1/reports', {
        method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer cors-test-key' }, body: '{}'
      });
      // Consume the public handler's single request allowance before checking 429.
      const warmup = await post(urls.rate); await warmup.text();
      const results = [];
      for (const name of ['draining', 'rate']) {
        const response = await post(urls[name]);
        results.push({ name, status: response.status, retryAfter: response.headers.get('Retry-After'),
          exposed: response.headers.get('Access-Control-Expose-Headers'),
          parsed: parseResponse(response, await response.text(), 1000) });
      }
      return results;
    }, urls);
    for (const result of results) {
      assert.equal(result.status, result.name === 'draining' ? 503 : 429);
      assert.match(result.retryAfter, /^[1-9][0-9]*$/, 'native fetch must expose Retry-After');
      assert.equal(result.parsed.error.code, result.name === 'draining' ? 'server_draining' : 'rate_limited');
      assert.equal(result.parsed.error.retryable, true);
      assert.equal(result.parsed.error.retryAt, 1000 + Number(result.retryAfter) * 1000);
      // Exposing Retry-After must not turn into a wildcard header policy.
      assert.equal(result.exposed, null);
    }
    assert.deepEqual(errors, []);
    await page.goto(urls.denied);
    const blocked = await page.evaluate(async api => {
      try { await fetch(api + '/api/v1/reports', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }); return false; }
      catch (error) { return error instanceof TypeError; }
    }, urls.draining);
    assert.equal(blocked, true, 'a disallowed second origin must remain blocked');
    console.log(JSON.stringify({ results, deniedOriginBlocked: blocked }));
  } finally {
    if (browser) await browser.close();
    clearTimeout(watchdog);
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
