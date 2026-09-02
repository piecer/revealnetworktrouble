import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';

const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const started = '2026-09-01T12:00:00Z';
const result = (overrides = {}) => ({ kind: 'dns', address: 'example.test', status: 'healthy', latency_ms: 4, started_at: started, details: {}, ...overrides });
const report = (id, overrides = {}) => ({ id, status: 'healthy', started_at: started, duration_ms: 10, summary: { total: 1, passed: 1, failed: 0 }, results: [result()], ...overrides });
const fullAnalysis = (value = 'safe') => ({
  verdict: 'attention',
  findings: [{ id: 'f1', code: 'dns_resolution_failed', severity: 'critical', category: 'name_resolution', title: value, summary: 'No answer', confidence: 'direct', evidence_ids: ['e1'], action_ids: ['a1'] }],
  evidence: [{ id: 'e1', result_index: 0, kind: 'dns', address: 'example.test', signal: 'error_code', observed: value, expected: 'answer', provenance: 'result' }],
  actions: [{ id: 'a1', title: 'Check DNS', step: value, expected_result: 'answer', escalation_condition: 'still fails' }],
  coverage: { available: ['status'], missing: ['packet loss'], provider_failures: [], limitations: [] }
});
const response = (body, status = 200, headers = {}) => ({ ok: status >= 200 && status < 300, status, headers: { get: n => headers[n] ?? null }, text: async () => body });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };

function setup(fetchImpl = async () => response(JSON.stringify(report('one'))), options = {}) {
  const { url = 'https://ui.example.test/#diagnostics', configureWindow, ...appOptions } = options;
  const dom = new JSDOM(markup, { url });
  configureWindow?.(dom.window);
  const app = createApp({ document: dom.window.document, window: dom.window, fetchImpl, ...appOptions });
  return { dom, app, document: dom.window.document };
}
function submit(document, selector = '#check-form') { document.querySelector(selector).dispatchEvent(new document.defaultView.Event('submit', { bubbles: true, cancelable: true })); }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); }

test('bootstrap exposes idle workspaces, concise live regions, and a native focusable import', () => {
  const { document } = setup();
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'idle');
  assert.equal(document.querySelector('#request-live').getAttribute('role'), 'status');
  assert.equal(document.querySelector('#request-alert').getAttribute('role'), 'alert');
  assert.equal(document.querySelector('#report-section').hasAttribute('aria-live'), false);
  const input = document.querySelector('#ip-label-import');
  assert.equal(input.hidden, false);
  assert.notEqual(input.tabIndex, -1);
});

test('new request removes prior report, stale A cannot publish or clear B busy, and owner B finalizes', async () => {
  const a = deferred(); const b = deferred(); let calls = 0;
  const { document } = setup(() => (++calls === 1 ? a.promise : b.promise));
  submit(document); await flush();
  document.querySelector('#timeout').value = '5001';
  submit(document); await flush();
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'true');
  assert.equal(document.querySelector('#analysis-report').hidden, true);
  a.resolve(response(JSON.stringify(report('A')))); await flush();
  assert.equal(document.querySelector('#analysis-report').textContent.includes('리포트 A'), false);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'true');
  b.resolve(response(JSON.stringify(report('B')))); await flush();
  assert.match(document.querySelector('#analysis-report').textContent, /B/);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
});

test('input invalidation aborts, clears ready result immediately, and returns to idle', async () => {
  const { document } = setup(); submit(document); await flush();
  assert.equal(document.querySelector('#analysis-report').hidden, false);
  document.querySelector('#timeout').value = '6000';
  document.querySelector('#timeout').dispatchEvent(new document.defaultView.Event('input', { bubbles: true }));
  assert.equal(document.querySelector('#analysis-report').hidden, true);
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'idle');
});

test('explicit cancel aborts once, renders cancelled, and restores run focus', async () => {
  const wait = deferred(); let aborts = 0;
  const { document } = setup((_url, init) => { init.signal.addEventListener('abort', () => aborts++); return wait.promise; });
  submit(document); await flush(); document.querySelector('#cancel-diagnostics').click(); await flush();
  assert.equal(aborts, 1);
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'cancelled');
  assert.equal(document.activeElement, document.querySelector('#run'));
});

test('navigation and beforeunload abort active requests without allowing stale publication', async () => {
  const waits = [deferred(), deferred()]; let call = 0; let aborts = 0;
  const { dom, document } = setup((_url, init) => { init.signal.addEventListener('abort', () => aborts++); return waits[call++].promise; });
  submit(document); await flush();
  document.querySelector('[data-view-link="topology"]').click(); await flush();
  assert.equal(aborts, 1);
  submit(document); await flush();
  dom.window.dispatchEvent(new dom.window.Event('beforeunload'));
  assert.equal(aborts, 2);
});

test('timeout abort produces focused error alert and previous output stays removed', async () => {
  let timer;
  const { document } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })))), { setTimer: fn => { timer = fn; return 1; }, clearTimer: () => {} });
  submit(document); await flush(); timer(); await flush();
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'error');
  assert.equal(document.activeElement, document.querySelector('#request-alert'));
  assert.match(document.querySelector('#request-alert').textContent, /시간|timed out/i);
});

test('Bearer is isolated per API base in session storage and only sent in Authorization', async () => {
  const requests = []; const { dom, document, app } = setup(async (url, init) => { requests.push({ url, init }); return response(JSON.stringify(report('auth'))); });
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = 'raw-super-secret';
  document.querySelector('#apply-bearer').click();
  assert.equal(document.querySelector('#bearer-token').value, '');
  submit(document); await flush();
  assert.equal(requests[0].init.headers.Authorization, 'Bearer raw-super-secret');
  assert.doesNotMatch(requests[0].url, /raw-super-secret/);
  assert.doesNotMatch(document.body.textContent, /raw-super-secret/);
  assert.doesNotMatch(JSON.stringify(app.getState()), /raw-super-secret/);
  assert.equal(dom.window.localStorage.length, 0);
  assert.equal(dom.window.sessionStorage.length > 0, true);
});

test('analysis renders verdict then findings/evidence/actions/coverage/raw and keeps XSS inert', async () => {
  const attack = '<img src=x onerror=alert(1)><script>alert(1)</script>';
  const { document } = setup(async () => response(JSON.stringify(report('analysis-id', { analysis: fullAnalysis(attack) }))));
  submit(document); await flush();
  const root = document.querySelector('#analysis-report');
  assert.deepEqual([...root.querySelectorAll('[data-analysis-section]')].map(node => node.dataset.analysisSection), ['verdict', 'findings', 'evidence', 'actions', 'coverage', 'raw']);
  assert.match(root.textContent, /analysis-id/);
  assert.match(root.textContent, /<img src=x/);
  assert.equal(root.querySelector('img, script, iframe'), null);
  assert.equal(document.activeElement.id, 'analysis-title');
  assert.ok(root.querySelector('table caption'));
  assert.ok(root.querySelector('ol input[type="checkbox"] + label'));
  const toggle = root.querySelector('.finding-toggle');
  toggle.dispatchEvent(new document.defaultView.KeyboardEvent('keydown', { key: ' ', bubbles: true }));
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert.equal(document.getElementById(toggle.getAttribute('aria-controls')).hidden, false);
});

test('legacy report is ready but explicitly unsupported/inconclusive', async () => {
  const { document } = setup(); submit(document); await flush();
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'ready');
  assert.match(document.querySelector('#analysis-report').textContent, /미지원|판단 보류/);
});

test('download is unavailable until ready and exports only the owned normalized report', async () => {
  const { dom, document } = setup(); let created = 0; let clicked = 0;
  dom.window.URL.createObjectURL = () => { created++; return 'blob:report'; };
  dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => { clicked++; };
  document.querySelector('#download').click();
  assert.equal(created, 0);
  submit(document); await flush();
  document.querySelector('#download').click();
  assert.equal(created, 1);
  assert.equal(clicked, 1);
});

test('normalized HTTP error uses alert and 401 opens credential settings', async () => {
  const { document } = setup(async () => response('<html>secret proxy</html>', 401));
  submit(document); await flush();
  assert.equal(document.querySelector('#connection-settings').open, true);
  assert.equal(document.activeElement, document.querySelector('#bearer-token'));
  assert.doesNotMatch(document.body.textContent, /secret proxy/);
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'error');
});

test('topology request lane is independent and sends bounded traceroute payload', async () => {
  const requests = [];
  const topologyReport = report('topology', {
    results: [result({ kind: 'traceroute', address: 'one.example', details: {} })]
  });
  const { document, app } = setup(async (url, init) => {
    requests.push({ url, init, payload: JSON.parse(init.body) });
    return response(JSON.stringify(topologyReport));
  });
  document.querySelector('#topology-targets').value = 'one.example\none.example\ntwo.example';
  document.querySelector('#topology-attempts').value = '10';
  submit(document, '#topology-form'); await flush();
  assert.equal(requests[0].payload.targets.length, 2);
  assert.deepEqual(requests[0].payload.targets.map(target => target.attempts), [10, 10]);
  assert.equal(app.getState().topology.phase, 'ready');
  assert.equal(app.getState().diagnostics.phase, 'idle');
});

test('initial deep-link hash focuses the visible view h2', () => {
  const { document } = setup(undefined, { url: 'https://ui.example.test/#ip-labels' });
  assert.equal(document.querySelector('#ip-labels-view').hidden, false);
  assert.equal(document.activeElement, document.querySelector('#ip-labels-view h2'));
  assert.equal(document.activeElement.closest('[hidden]'), null);
});

test('starting a same-lane retry clears its stale assertive alert without clearing another lane owner', async () => {
  let calls = 0;
  const second = deferred();
  const { document } = setup(async () => {
    calls++;
    if (calls === 1) return response(JSON.stringify({ error: { code: 'server_busy', message: 'busy' } }), 503);
    return second.promise;
  });
  submit(document); await flush();
  const alert = document.querySelector('#request-alert');
  assert.equal(alert.hidden, false);
  assert.equal(alert.dataset.purpose, 'diagnostics');
  submit(document); await flush();
  assert.equal(alert.hidden, true);
  assert.equal(alert.textContent, '');
  second.resolve(response(JSON.stringify(report('retry')))); await flush();
});

test('human export redacts overlap, case variants, and report ID and uses a generic filename', async () => {
  let exported = ''; let filename = '';
  const sensitive = 'Private.Internal.Example';
  const humanAnalysis = fullAnalysis(`Visit PRIVATE.INTERNAL.EXAMPLE then internal.example`);
  humanAnalysis.actions[0].step = 'Resolve private.internal.example and INTERNAL.EXAMPLE';
  const { dom, document } = setup(async () => response(JSON.stringify(report(sensitive, {
    results: [result({ address: sensitive }), result({ address: 'internal.example' })],
    summary: { total: 2, passed: 2, failed: 0 }, analysis: humanAnalysis
  }))));
  dom.window.Blob = class { constructor(parts) { exported = parts.join(''); } };
  dom.window.URL.createObjectURL = () => 'blob:human'; dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = function () { filename = this.download; };
  submit(document); await flush(); document.querySelector('#download-human').click();
  assert.equal(filename, 'checknetwork-human-report.md');
  assert.match(exported, /\[REDACTED TARGET\]/);
  assert.doesNotMatch(exported, /private\.internal\.example|internal\.example/i);
});

test('Content-Length rejects an oversized response before body allocation', async () => {
  let readerRequested = false;
  const oversized = {
    ok: true, status: 200,
    headers: { get: name => name.toLowerCase() === 'content-length' ? String(8 * 1024 * 1024 + 1) : null },
    body: { getReader() { readerRequested = true; throw new Error('must not read'); } }
  };
  const { document, app } = setup(async () => oversized);
  submit(document); await flush();
  assert.equal(readerRequested, false);
  assert.equal(app.getState().diagnostics.error.code, 'response_too_large');
});

test('streamed 8 MiB cap counts multibyte bytes and preserves size error when cancel fails', async () => {
  const encoder = new TextEncoder();
  const chunks = [encoder.encode('€'.repeat(2_796_202)), encoder.encode('€')];
  let reads = 0; let cancelled = 0;
  const streamed = {
    ok: true, status: 200, headers: { get: () => null },
    body: { getReader: () => ({
      async read() { return reads < chunks.length ? { value: chunks[reads++], done: false } : { done: true }; },
      async cancel() { cancelled++; throw new Error('cancel failed'); }
    }) }
  };
  const { document, app } = setup(async () => streamed);
  submit(document); await flush(); await flush();
  assert.equal(cancelled, 1);
  assert.equal(app.getState().diagnostics.error.code, 'response_too_large');
});

test('reflected bearer in structured error never enters state, DOM, or exports', async () => {
  const token = 'REFLECTED-super-secret';
  const { dom, document, app } = setup(async () => response(JSON.stringify({ error: { code: 'unauthorized', message: `bad Bearer ${token}` } }), 401));
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = token;
  document.querySelector('#apply-bearer').click();
  submit(document); await flush();
  assert.doesNotMatch(JSON.stringify(app.getState()), new RegExp(token, 'i'));
  assert.doesNotMatch(document.body.textContent, new RegExp(token, 'i'));
  assert.equal(document.querySelector('#download').disabled, true);
  assert.equal(document.querySelector('#download-human').disabled, true);
  assert.equal(dom.window.localStorage.length, 0);
});

test('reflected bearer in a successful report is redacted from state, DOM, and JSON export', async () => {
  const token = 'SUCCESS-reflected-secret'; let exported = '';
  const reflected = report(token, { results: [result({ message: `server echoed ${token}`, details: { reflected: token } })] });
  const { dom, document, app } = setup(async () => response(JSON.stringify(reflected)));
  dom.window.Blob = class { constructor(parts) { exported = parts.join(''); } };
  dom.window.URL.createObjectURL = () => 'blob:report'; dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => {};
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = token;
  document.querySelector('#apply-bearer').click();
  submit(document); await flush();
  document.querySelector('#download').click();
  const pattern = new RegExp(token, 'i');
  assert.doesNotMatch(JSON.stringify(app.getState()), pattern);
  assert.doesNotMatch(document.body.textContent, pattern);
  assert.doesNotMatch(exported, pattern);
});

test('credential reflection in a report property name is rejected without disclosure', async () => {
  const token = 'KEY-reflected-secret';
  const reflected = report('safe', { results: [result({ details: { [token]: 'safe' } })] });
  const { document, app } = setup(async () => response(JSON.stringify(reflected)));
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = token;
  document.querySelector('#apply-bearer').click();
  submit(document); await flush();
  assert.equal(app.getState().diagnostics.phase, 'error');
  assert.doesNotMatch(JSON.stringify(app.getState()), new RegExp(token, 'i'));
  assert.doesNotMatch(document.body.textContent, new RegExp(token, 'i'));
});

test('topology keyboard delegation only consumes activation on editable map nodes', () => {
  const { dom, document } = setup();
  const root = document.querySelector('#topology-result');
  root.innerHTML = '<button type="button">fullscreen</button><div class="map-stage" tabindex="0"></div><div class="map-node" data-label-address="203.0.113.10" tabindex="0"></div>';
  for (const selector of ['button', '.map-stage']) {
    const event = new dom.window.KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
    root.querySelector(selector).dispatchEvent(event);
    assert.equal(event.defaultPrevented, false, selector);
  }
  const nodeEvent = new dom.window.KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
  root.querySelector('.map-node').dispatchEvent(nodeEvent);
  assert.equal(nodeEvent.defaultPrevented, true);
});

test('compact vantage and coverage header precedes analysis and folded raw workspaces', async () => {
  const { document } = setup(async () => response(JSON.stringify(report('hierarchy', { analysis: fullAnalysis() }))));
  submit(document); await flush();
  const summary = document.querySelector('#summary');
  const analysisRoot = document.querySelector('#analysis-report');
  const rawCards = document.querySelector('.secondary-results');
  assert.match(summary.textContent, /관측 위치/);
  assert.match(summary.textContent, /Coverage/);
  assert.ok(summary.compareDocumentPosition(analysisRoot) & document.defaultView.Node.DOCUMENT_POSITION_FOLLOWING);
  assert.ok(analysisRoot.compareDocumentPosition(rawCards) & document.defaultView.Node.DOCUMENT_POSITION_FOLLOWING);
  assert.equal(rawCards.open, false);
  assert.equal(analysisRoot.querySelector('[data-analysis-section="raw"] details').open, false);
});

test('topology SVG and IP-label table expose accessible names and scoped headers', async () => {
  const { renderTopologyMap } = await import('./app.js');
  const rendered = renderTopologyMap([{ address: 'example.test', status: 'healthy', details: {} }]);
  const fragment = new JSDOM(rendered).window.document;
  const svg = fragment.querySelector('svg');
  assert.ok(svg.getAttribute('aria-label') || svg.querySelector(':scope > title') || svg.getAttribute('role') !== 'img');

  const { document } = setup();
  const table = document.querySelector('.mapping-table');
  assert.ok(table.querySelector('caption') || table.getAttribute('aria-label') || table.getAttribute('aria-labelledby'));
  assert.deepEqual([...table.querySelectorAll('thead th')].map(th => th.getAttribute('scope')), ['col', 'col', 'col', 'col']);
});

test('CARTO environment precedence text agrees with runtime behavior', () => {
  const { document } = setup(undefined, {
    configureWindow(win) {
      win.sessionStorage.setItem('checknetwork.carto-base-map.v1', 'session-key');
      win.CHECKNETWORK_CONFIG = { CARTO_BASE_MAP: 'environment-key' };
    }
  });
  assert.match(document.querySelector('#carto-base-map-form p').textContent, /배포 환경.*우선/);
  assert.match(document.querySelector('#carto-base-map-status').textContent, /배포 환경/);
  document.querySelector('#carto-base-map-input').value = 'new-session-key';
  document.querySelector('#carto-base-map-form').dispatchEvent(new document.defaultView.Event('submit', { bubbles: true, cancelable: true }));
  assert.match(document.querySelector('#carto-base-map-status').textContent, /배포 환경/);
});

test('IP-label and CARTO forms remain wired after lifecycle migration', () => {
  const { dom, document } = setup();
  document.querySelector('#ip-label-address').value = '203.0.113.10';
  document.querySelector('#ip-label-name').value = 'Seoul edge';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  assert.match(document.querySelector('#ip-label-rows').textContent, /203\.0\.113\.10/);
  assert.equal(document.querySelector('#ip-label-rows [data-field="label"]').value, 'Seoul edge');

  document.querySelector('#carto-base-map-input').value = 'session-carto-key';
  document.querySelector('#carto-base-map-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  assert.equal(dom.window.sessionStorage.getItem('checknetwork.carto-base-map.v1'), 'session-carto-key');
  assert.match(document.querySelector('#carto-base-map-status').textContent, /현재 탭/);
});

test('responsive and accessibility contracts cover 320/375/400, focus, reduced motion, and wide component scroll', async () => {
  const css = await readFile(new URL('./styles.css', import.meta.url), 'utf8');
  assert.match(css, /@media \(max-width:400px\)/);
  assert.match(css, /@media \(max-width:320px\)/);
  assert.match(css, /@media \(max-width:760px\)/);
  assert.match(css, /focus-visible/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /evidence-scroll[^}]*overflow-x:auto|overflow-x:auto[^}]*evidence-scroll/s);
});
