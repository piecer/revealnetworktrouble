import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';

const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const started = '2026-09-01T12:00:00Z';
const result = (overrides = {}) => ({ kind: 'dns', address: 'example.test', status: 'healthy', latency_ms: 4, started_at: started, details: {}, ...overrides });
const report = (id, overrides = {}) => ({ id, status: 'healthy', started_at: started, duration_ms: 10, summary: { total: 1, passed: 1, failed: 0 }, results: [result()], ...overrides });
const compactTopology = (overrides = {}) => ({
  schema: 'compact-v1', selection: 'fair-complete-prefix-v1',
  limits: { nodes: 500, links: 1000, max_response_bytes_exclusive: 1048576, max_geo_bundle_bytes: 4096 },
  nodes: [
    { id: 'n1', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
    { id: 'n2', kind: 'ip', address: '192.0.2.1', status: 'healthy', hop_min: 1, hop_max: 1, observations: 1, public_ip: true, geolocation: { city: 'Seoul', region: '', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 } }
  ],
  links: [{ from: 'n1', to: 'n2', status: 'healthy', observations: 1 }],
  routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['n1', 'n2'] }],
  stats: {
    nodes: { total: 2, displayed: 2, omitted: 0 }, links: { total: 1, displayed: 1, omitted: 0 },
    routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
    node_observations: { total: 2, displayed: 2, omitted: 0 }, link_observations: { total: 1, displayed: 1, omitted: 0 }
  },
  result_stats: [{ result_index: 0, routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 }, node_observations: { total: 2, displayed: 2, omitted: 0 }, link_observations: { total: 1, displayed: 1, omitted: 0 } }],
  geo: { eligible: 1, available: 1, included: 1, omitted: 0, unavailable: 0 },
  truncated: false, truncation_reasons: [], ...overrides
});
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
function labelRows(count, offset = 0) {
  return Array.from({ length: count }, (_, index) => {
    const value = index + offset;
    return { ip: `10.${Math.floor(value / 65536)}.${Math.floor(value / 256) % 256}.${value % 256}`, label: `node-${value}`, note: `note-${value}` };
  });
}
function drain(queue) { while (queue.length) queue.shift()(); }

test('concurrent apps send only their own diagnostics and topology inputs', async () => {
  const callsA = []; const callsB = [];
  const topology = address => report(`topology-${address}`, {
    results: [result({ kind: 'traceroute', address, details: {} })],
    compact_topology: compactTopology()
  });
  const a = setup(async (_url, init) => {
    const payload = JSON.parse(init.body); callsA.push(payload);
    return response(JSON.stringify(payload.topology_mode ? topology('a-route.example') : report('A')));
  }, { url: 'https://a.example.test/#topology', scheduler: { schedule: callback => { callback(); return 1; }, cancel() {} } });
  const b = setup(async (_url, init) => {
    const payload = JSON.parse(init.body); callsB.push(payload);
    return response(JSON.stringify(payload.topology_mode ? topology('b-route.example') : report('B')));
  }, { url: 'https://b.example.test/#topology', scheduler: { schedule: callback => { callback(); return 1; }, cancel() {} } });

  a.document.querySelector('#targets input').value = 'a-diagnostic.example';
  b.document.querySelector('#targets input').value = 'b-diagnostic.example';
  a.document.querySelector('#topology-targets').value = 'a-route.example';
  b.document.querySelector('#topology-targets').value = 'b-route.example';

  await a.app.start('diagnostics'); await b.app.start('diagnostics');
  await a.app.start('topology'); await b.app.start('topology');

  assert.equal(callsA[0].targets[0].address, 'a-diagnostic.example');
  assert.equal(callsB[0].targets[0].address, 'b-diagnostic.example');
  assert.deepEqual(callsA[1].targets.map(target => target.address), ['a-route.example']);
  assert.deepEqual(callsB[1].targets.map(target => target.address), ['b-route.example']);
});

test('destroy aborts owned work and gates every public method without affecting another app', async () => {
  const wait = deferred(); let abortsA = 0; let fetchesA = 0; let fetchesB = 0;
  const scheduledA = []; const cancelledA = [];
  const a = setup((_url, init) => {
    fetchesA++;
    init.signal.addEventListener('abort', () => abortsA++);
    return wait.promise;
  }, { scheduler: { schedule(callback) { scheduledA.push(callback); return callback; }, cancel(handle) { cancelledA.push(handle); } } });
  const b = setup(async () => { fetchesB++; return response(JSON.stringify(report('B'))); });

  const startA = a.app.start('diagnostics'); await flush();
  await b.app.start('diagnostics');
  const bBefore = b.document.body.textContent;
  assert.equal(a.document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'true');

  assert.equal(a.app.destroy(), true);
  assert.equal(a.app.destroy(), false);
  assert.equal(abortsA, 1);
  assert.ok(cancelledA.length <= scheduledA.length);
  const destroyedState = a.app.getState();
  await a.app.start('diagnostics');
  a.app.cancel('diagnostics');
  a.app.invalidate('diagnostics');
  a.document.querySelector('#check-form').dispatchEvent(new a.dom.window.Event('submit', { bubbles: true, cancelable: true }));
  a.dom.window.dispatchEvent(new a.dom.window.Event('hashchange'));
  await flush();

  assert.equal(fetchesA, 1);
  assert.deepEqual(a.app.getState(), destroyedState);
  assert.equal(fetchesB, 1);
  assert.equal(b.document.body.textContent, bBefore);
  wait.resolve(response(JSON.stringify(report('stale-A'))));
  await startA; await flush();
  assert.doesNotMatch(a.document.body.textContent, /stale-A/);
  assert.match(b.document.body.textContent, /B/);
});

test('label CRUD and view events remain owned by their app instance', async () => {
  const a = setup(undefined, { configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify([{ ip: '192.0.2.1', label: 'A-only', note: '' }])); } });
  const b = setup(undefined, { configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify([{ ip: '198.51.100.1', label: 'B-only', note: '' }])); } });
  await flush();
  a.document.querySelector('#ip-label-address').value = '203.0.113.10';
  a.document.querySelector('#ip-label-name').value = 'A-new';
  a.document.querySelector('#ip-label-form').dispatchEvent(new a.dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await flush();

  assert.match(a.document.querySelector('#ip-label-rows').textContent, /192\.0\.2\.1|203\.0\.113\.10/);
  assert.doesNotMatch(b.document.body.textContent, /A-only|A-new|203\.0\.113\.10/);
  assert.match(b.document.querySelector('#ip-label-rows').textContent, /198\.51\.100\.1/);
  assert.equal(JSON.parse(a.dom.window.localStorage.getItem('checknetwork.ip-labels.v1')).length, 2);
  assert.deepEqual(JSON.parse(b.dom.window.localStorage.getItem('checknetwork.ip-labels.v1')).map(row => row.label), ['B-only']);

  const bView = b.document.querySelector('#diagnostics-view').hidden;
  a.document.querySelector('[data-view-link="ip-labels"]').click();
  a.dom.window.dispatchEvent(new a.dom.window.Event('hashchange'));
  assert.equal(b.document.querySelector('#diagnostics-view').hidden, bView);
});

test('topology checkbox keeps focus across its synchronous filter rebuild', async () => {
  const jobs = []; let id = 0;
  const scheduler = { schedule(callback) { jobs.push({ id: ++id, callback }); return id; }, cancel() {} };
  const topologyReport = report('focus-topology', {
    results: [result({ kind: 'traceroute', address: 'focus.example', details: {} })],
    compact_topology: compactTopology()
  });
  const { app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://focus.example/#topology', scheduler });
  await app.start('topology');
  while (jobs.length) jobs.shift().callback();
  const checkbox = document.querySelector('[data-target-index="0"]');
  checkbox.focus(); checkbox.checked = false;
  checkbox.dispatchEvent(new document.defaultView.Event('change', { bubbles: true }));
  assert.equal(document.activeElement, document.querySelector('[data-target-index="0"]'));
});

test('topology all and none actions restore focus across synchronous rebuilds', async () => {
  const jobs = []; let id = 0;
  const scheduler = { schedule(callback) { jobs.push({ id: ++id, callback }); return id; }, cancel() {} };
  const topologyReport = report('focus-actions', {
    results: [result({ kind: 'traceroute', address: 'focus.example', details: {} })],
    compact_topology: compactTopology()
  });
  const { app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://focus.example/#topology', scheduler });
  await app.start('topology');
  while (jobs.length) jobs.shift().callback();
  for (const action of ['none', 'all']) {
    const button = document.querySelector(`[data-filter-action="${action}"]`);
    button.focus(); button.click();
    assert.equal(document.activeElement, document.querySelector(`[data-filter-action="${action}"]`));
    while (jobs.length) jobs.shift().callback();
  }
});

test('zero selected targets renders a bounded explicit empty state without scheduling work', async () => {
  const jobs = []; let id = 0;
  const scheduler = { schedule(callback) { jobs.push({ id: ++id, callback }); return id; }, cancel() {} };
  const topologyReport = report('empty-selection', {
    results: [result({ kind: 'traceroute', address: 'focus.example', details: {} })],
    compact_topology: compactTopology()
  });
  const { app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://empty.example/#topology', scheduler });
  await app.start('topology');
  while (jobs.length) jobs.shift().callback();
  const none = document.querySelector('[data-filter-action="none"]');
  none.focus(); none.click();

  assert.equal(jobs.length, 0);
  assert.equal(document.activeElement, document.querySelector('[data-filter-action="none"]'));
  assert.equal(document.querySelectorAll('#topology-result .topology-empty-state').length, 1);
  assert.match(document.querySelector('#topology-result').textContent, /선택.*0|0.*선택/);
  assert.match(document.querySelector('#topology-render-status').textContent, /0/);
  assert.match(document.querySelector('#topology-result-summary').textContent, /노드 0\/0.*링크 0\/0.*경로 0\/0/);
  assert.equal(document.querySelector('#topology-result').getAttribute('aria-busy'), 'false');
  assert.ok(document.querySelectorAll('*').length <= 1200);
});

test('destroy then recreate on the same document behaves like HMR without duplicate owners', async () => {
  const dom = new JSDOM(markup, { url: 'https://hmr.example/#diagnostics' });
  let oldFetches = 0; let newFetches = 0;
  const oldApp = createApp({ document: dom.window.document, window: dom.window, fetchImpl: async () => { oldFetches++; return response(JSON.stringify(report('old'))); } });
  assert.equal(oldApp.destroy(), true);
  const newApp = createApp({ document: dom.window.document, window: dom.window, fetchImpl: async () => { newFetches++; return response(JSON.stringify(report('new'))); } });
  submit(dom.window.document); await flush();
  assert.equal(oldFetches, 0);
  assert.equal(newFetches, 1);
  assert.match(dom.window.document.querySelector('#analysis-report').textContent, /new/);
  assert.equal(newApp.destroy(), true);
});

test('initial stored-label render failure destroys partial app before rethrow and recreation has one submission', async () => {
  const dom = new JSDOM(markup, { url: 'https://bootstrap.example/#diagnostics' });
  dom.window.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify([{ ip: '192.0.2.4', label: 'stored', note: '' }]));
  let abandonedFetches = 0;
  assert.throws(() => createApp({
    document: dom.window.document,
    window: dom.window,
    fetchImpl: async () => { abandonedFetches++; return response(JSON.stringify(report('abandoned'))); },
    scheduleLabelRender: () => { throw new Error('label scheduler failed'); }
  }), /label scheduler failed/);

  let recreatedFetches = 0;
  const recreated = createApp({
    document: dom.window.document,
    window: dom.window,
    fetchImpl: async () => { recreatedFetches++; return response(JSON.stringify(report('recreated'))); }
  });
  submit(dom.window.document); await flush();
  assert.equal(abandonedFetches, 0);
  assert.equal(recreatedFetches, 1);
  assert.match(dom.window.document.querySelector('#analysis-report').textContent, /recreated/);
  recreated.destroy();
});

test('bootstrap exposes idle workspaces, concise live regions, and a native focusable import', () => {
  const { document } = setup();
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'idle');
  assert.equal(document.querySelector('#request-live').getAttribute('role'), 'status');
  assert.equal(document.querySelector('#request-alert').getAttribute('role'), 'alert');
  assert.equal(document.querySelector('#report-section').hasAttribute('aria-live'), false);
  for (const id of ['topology-render-status', 'geo-render-status']) {
    const status = document.getElementById(id);
    assert.equal(status.getAttribute('role'), 'status', id);
    assert.equal(status.getAttribute('aria-live'), 'polite', id);
    assert.equal(status.getAttribute('aria-atomic'), 'true', id);
  }
  assert.equal(document.querySelector('#geo-map-result').hasAttribute('aria-live'), false);
  const input = document.querySelector('#ip-label-import');
  assert.equal(input.hidden, false);
  assert.notEqual(input.tabIndex, -1);
});

test('stored labels are sanitized, bounded, rewritten, paginated, and progressively inserted', () => {
  const rows = [...labelRows(502), { ip: '999.1.1.1', label: 'bad' }, { ip: '10.0.0.1', label: 'last wins' }];
  const queue = []; const batches = [];
  const { dom, document } = setup(undefined, {
    scheduleLabelRender: callback => queue.push(callback),
    configureWindow(win) {
      win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(rows));
      const append = win.Element.prototype.append;
      win.Element.prototype.append = function (...nodes) {
        if (this.id === 'ip-label-rows') for (const node of nodes) if (node?.nodeType === 11) batches.push(node.querySelectorAll('*').length);
        return append.apply(this, nodes);
      };
    }
  });
  assert.equal(document.querySelector('#ip-label-rows').children.length, 0, 'scheduled rendering must not synchronously append every row');
  drain(queue);
  const stored = JSON.parse(dom.window.localStorage.getItem('checknetwork.ip-labels.v1'));
  assert.equal(stored.length, 500);
  assert.equal(stored.find(row => row.ip === '10.0.0.1').label, 'last wins');
  assert.match(document.querySelector('#ip-label-message').textContent, /제외|생략/);
  assert.equal(document.querySelectorAll('#ip-label-rows tr').length, 100);
  assert.equal(document.querySelector('#ip-label-page-status').textContent, '1 / 5');
  assert.equal(document.querySelector('#ip-label-prev').disabled, true);
  assert.equal(document.querySelector('#ip-label-next').disabled, false);
  assert.ok(batches.length > 1);
  assert.ok(batches.every(count => count <= 100), `oversized insertion batch: ${batches}`);
  assert.ok(document.querySelectorAll('*').length <= 1200);

  const next = document.querySelector('#ip-label-next'); next.focus(); next.click(); drain(queue);
  assert.equal(document.activeElement, next);
  assert.equal(document.querySelector('#ip-label-page-status').textContent, '2 / 5');
  assert.equal(document.querySelectorAll('#ip-label-rows tr').length, 100);

  const deleted = document.querySelector('#ip-label-rows .mapping-delete'); deleted.focus(); deleted.click(); drain(queue);
  assert.equal(document.activeElement?.classList.contains('mapping-delete'), true, 'delete must move focus to a surviving row action');
});

test('oversized label import rejects before reading file text', async () => {
  let textCalls = 0;
  const { dom, document } = setup();
  const file = { name: 'labels.json', size: 1024 * 1024 + 1, text: async () => { textCalls++; return '[]'; } };
  const input = document.querySelector('#ip-label-import');
  Object.defineProperty(input, 'files', { configurable: true, value: [file] });
  input.dispatchEvent(new dom.window.Event('change', { bubbles: true })); await flush();
  assert.equal(textCalls, 0);
  assert.match(document.querySelector('#ip-label-message').textContent, /1 MiB|크기/);
});

test('deferred label import from destroyed app cannot overwrite recreated app storage or DOM', async () => {
  const text = deferred();
  const dom = new JSDOM(markup, { url: 'https://labels.example/#ip-labels' });
  const document = dom.window.document;
  const oldApp = createApp({ document, window: dom.window, fetchImpl: async () => response('{}') });
  const input = document.querySelector('#ip-label-import');
  Object.defineProperty(input, 'files', { configurable: true, value: [{ name: 'old.json', size: 64, text: () => text.promise }] });
  input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  await flush();

  oldApp.destroy();
  const newApp = createApp({ document, window: dom.window, fetchImpl: async () => response('{}') });
  document.querySelector('#ip-label-address').value = '198.51.100.9';
  document.querySelector('#ip-label-name').value = 'fresh B';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await flush();
  const storageBefore = dom.window.localStorage.getItem('checknetwork.ip-labels.v1');
  const domBefore = document.querySelector('#ip-label-rows').textContent;
  const labelBefore = document.querySelector('#ip-label-rows [data-field="label"]').value;

  text.resolve('[{"ip":"192.0.2.9","label":"stale A"}]');
  await flush(); await flush();
  assert.equal(dom.window.localStorage.getItem('checknetwork.ip-labels.v1'), storageBefore);
  assert.equal(document.querySelector('#ip-label-rows').textContent, domBefore);
  assert.equal(document.querySelector('#ip-label-rows [data-field="label"]').value, labelBefore);
  assert.equal(labelBefore, 'fresh B');
  assert.doesNotMatch(document.body.textContent, /stale A/);
  newApp.destroy();
});

test('manual labels enforce character and record ceilings with explicit status', () => {
  const { dom, document } = setup(undefined, { configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(500))); } });
  document.querySelector('#ip-label-address').value = '192.0.2.1';
  document.querySelector('#ip-label-name').value = 'x'.repeat(257);
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  assert.match(document.querySelector('#ip-label-message').textContent, /256/);
  document.querySelector('#ip-label-name').value = 'new label';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  assert.match(document.querySelector('#ip-label-message').textContent, /500/);
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

test('throwing timer cleanup cannot block request replacement ownership', async () => {
  let calls = 0; let firstAbort = 0;
  const { document, app } = setup((_url, init) => {
    calls++;
    if (calls === 2) return response(JSON.stringify(report('replacement')));
    return new Promise((_, reject) => init.signal.addEventListener('abort', () => {
      firstAbort++;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    }));
  }, { setTimer: () => 1, clearTimer: () => { throw new Error('clear failed'); } });
  const first = app.start('diagnostics'); await flush();
  document.querySelector('#timeout').value = '5001';
  await assert.doesNotReject(app.start('diagnostics'));
  await assert.doesNotReject(first);
  assert.equal(firstAbort, 1);
  assert.equal(calls, 2);
  assert.equal(app.getState().diagnostics.phase, 'ready');
  assert.equal(app.getState().diagnostics.active, null);
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

test('throwing timer cleanup cannot block input invalidation ownership cleanup', async () => {
  let aborts = 0;
  const { document, app } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => {
    aborts++;
    reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
  })), { setTimer: () => 1, clearTimer: () => { throw new Error('clear failed'); } });
  const pending = app.start('diagnostics'); await flush();
  assert.doesNotThrow(() => app.invalidate('diagnostics'));
  await assert.doesNotReject(pending);
  assert.equal(aborts, 1);
  assert.equal(app.getState().diagnostics.phase, 'idle');
  assert.equal(app.getState().diagnostics.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
});

test('explicit cancel aborts once, renders cancelled, and restores run focus', async () => {
  const wait = deferred(); let aborts = 0;
  const { document } = setup((_url, init) => { init.signal.addEventListener('abort', () => aborts++); return wait.promise; });
  submit(document); await flush(); document.querySelector('#cancel-diagnostics').click(); await flush();
  assert.equal(aborts, 1);
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'cancelled');
  assert.equal(document.activeElement, document.querySelector('#run'));
});

test('throwing timer cleanup cannot block explicit cancellation ownership cleanup', async () => {
  let aborts = 0;
  const { document, app } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => {
    aborts++;
    reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
  })), { setTimer: () => 1, clearTimer: () => { throw new Error('clear failed'); } });
  const pending = app.start('diagnostics'); await flush();
  assert.doesNotThrow(() => app.cancel('diagnostics'));
  await assert.doesNotReject(pending);
  assert.equal(aborts, 1);
  assert.equal(app.getState().diagnostics.phase, 'cancelled');
  assert.equal(app.getState().diagnostics.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
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

test('throwing timer cleanup cannot stop beforeunload from releasing every request owner', async () => {
  let aborts = 0;
  const { dom, app } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => {
    aborts++;
    reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
  })), { setTimer: () => 1, clearTimer: () => { throw new Error('clear failed'); } });
  const diagnostics = app.start('diagnostics');
  const topology = app.start('topology');
  await flush();
  dom.window.dispatchEvent(new dom.window.Event('beforeunload'));
  await assert.doesNotReject(Promise.all([diagnostics, topology]));
  assert.equal(aborts, 2);
  app.destroy();
  assert.equal(aborts, 2, 'beforeunload must remove owners so destroy cannot abort them again');
});

test('timer registration failure finalizes the owning request and restores controls', async () => {
  let fetchCalls = 0;
  const { document, app } = setup(async () => { fetchCalls++; return response(JSON.stringify(report('never'))); }, { setTimer: () => { throw new Error('timer unavailable'); } });
  submit(document); await flush();
  const lane = app.getState().diagnostics;
  assert.equal(fetchCalls, 0);
  assert.equal(lane.phase, 'error');
  assert.equal(lane.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
  assert.equal(document.querySelector('#run').disabled, false);
  assert.match(document.querySelector('#request-alert').textContent, /실패/);
});

test('throwing timer cleanup cannot strand a successful request and a retry remains ready', async () => {
  let fetchCalls = 0;
  const { document, app } = setup(async () => response(JSON.stringify(report(`ready-${++fetchCalls}`))), {
    setTimer: () => 17,
    clearTimer: () => { throw new Error('clear failed'); }
  });
  await assert.doesNotReject(app.start('diagnostics'));
  assert.equal(app.getState().diagnostics.phase, 'ready');
  assert.equal(app.getState().diagnostics.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
  assert.equal(document.querySelector('#run').disabled, false);
  await assert.doesNotReject(app.start('diagnostics'));
  assert.equal(fetchCalls, 2);
  assert.equal(app.getState().diagnostics.phase, 'ready');
  assert.equal(app.getState().diagnostics.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
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
  assert.equal(dom.window.localStorage.getItem('checknetwork.ip-labels.v1'), '[]');
  assert.equal(dom.window.localStorage.length, 1);
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

test('topology request asks for the compact topology representation', async () => {
  let payload;
  const { document } = setup(async (_url, init) => {
    payload = JSON.parse(init.body);
    return response(JSON.stringify(report('compact-request', { results: [] })));
  });
  submit(document, '#topology-form');
  await flush();
  assert.equal(payload.topology_mode, 'compact');
});

test('valid compact response progressively publishes through the model renderer without legacy SVG or Leaflet', async () => {
  const queue = []; const cancelled = new Set(); let nextID = 1; let leafletCalls = 0;
  const scheduler = {
    schedule(callback) { const id = nextID++; queue.push({ id, callback }); return id; },
    cancel(id) { cancelled.add(id); }
  };
  const compactReport = report('compact-render', {
    results: [result({ kind: 'traceroute', address: 'one.example', details: {} })], compact_topology: compactTopology()
  });
  const { dom, document } = setup(async () => response(JSON.stringify(compactReport)), {
    url: 'https://ui.example.test/#topology', scheduler,
    configureWindow(win) { win.L = { map() { leafletCalls++; throw new Error('Leaflet must not run'); } }; }
  });
  const root = document.querySelector('#topology-result');
  Object.defineProperty(root, 'innerHTML', { configurable: true, set() { throw new Error('legacy innerHTML topology path'); } });
  submit(document, '#topology-form'); await flush();
  assert.match(document.querySelector('#topology-render-status').textContent, /표시 0\//);
  while (queue.length) { const job = queue.shift(); if (!cancelled.has(job.id)) job.callback(); }
  assert.equal(root.querySelectorAll('svg').length, 0);
  assert.equal(root.querySelectorAll('.topology-node').length, 2);
  assert.equal(root.querySelectorAll('.topology-link').length, 1);
  assert.equal(root.querySelectorAll('.topology-route').length, 1);
  assert.equal(leafletCalls, 0);
  assert.match(document.querySelector('#topology-render-status').textContent, /표시 완료/);
  dom.window.close();
});

test('report without compact topology uses the bounded legacy adapter', async () => {
  const queue = [];
  const legacy = report('legacy-topology', { results: [result({
    kind: 'traceroute', address: 'legacy.example',
    details: { attempts: [{ attempt: 1, status: 'healthy', topology: {
      reached: true,
      nodes: [{ id: 'local', hop: 0, address: 'local', status: 'healthy' }, { id: 'hop', hop: 1, address: '192.0.2.8', status: 'healthy' }],
      links: [{ from: 'local', to: 'hop', status: 'healthy' }]
    } }] }
  })] });
  const { document } = setup(async () => response(JSON.stringify(legacy)), {
    url: 'https://ui.example.test/#topology',
    scheduler: { schedule(callback) { queue.push(callback); return callback; }, cancel() {} }
  });
  submit(document, '#topology-form'); await flush();
  while (queue.length) queue.shift()();
  assert.equal(document.querySelectorAll('#topology-result .topology-node').length, 2);
  assert.equal(document.querySelectorAll('#topology-result svg').length, 0);
  assert.match(document.querySelector('#topology-result-summary').textContent, /레거시 변환/);
  assert.ok(document.querySelectorAll('*').length <= 1200);
});

test('active topology and Geo views render lazily, unmount each other, and filters rerender only the active view', async () => {
  const jobs = []; const cancelled = new Set(); let id = 0;
  const scheduler = { schedule(callback) { const handle = ++id; jobs.push({ handle, callback }); return handle; }, cancel(handle) { cancelled.add(handle); } };
  const runAll = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.handle)) job.callback(); } };
  const nodes = [
    { id: 'local', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 2 },
    { id: 'a', kind: 'ip', address: '192.0.2.10', status: 'healthy', hop_min: 1, hop_max: 1, observations: 1, public_ip: true, geolocation: { latitude: 37.5, longitude: 127 } },
    { id: 'b', kind: 'ip', address: '198.51.100.20', status: 'healthy', hop_min: 1, hop_max: 1, observations: 1, public_ip: true, geolocation: { latitude: 35.6, longitude: 139.7 } }
  ];
  const links = [{ from: 'local', to: 'a', status: 'healthy', observations: 1 }, { from: 'local', to: 'b', status: 'healthy', observations: 1 }];
  const routes = [
    { result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['local', 'a'] },
    { result_index: 1, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['local', 'b'] }
  ];
  const value = report('views', {
    summary: { total: 2, passed: 2, failed: 0 },
    results: [result({ kind: 'traceroute', address: 'a.example' }), result({ kind: 'traceroute', address: 'b.example' })],
    compact_topology: compactTopology({
      nodes, links, routes,
      stats: { nodes: { total: 3, displayed: 3, omitted: 0 }, links: { total: 2, displayed: 2, omitted: 0 }, routes: { total: 2, displayed: 2, complete: 2, partial: 0, omitted: 0 }, node_observations: { total: 4, displayed: 4, omitted: 0 }, link_observations: { total: 2, displayed: 2, omitted: 0 } },
      result_stats: [0, 1].map(result_index => ({ result_index, routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 }, node_observations: { total: 2, displayed: 2, omitted: 0 }, link_observations: { total: 1, displayed: 1, omitted: 0 } })),
      geo: { eligible: 2, available: 2, included: 2, omitted: 0, unavailable: 0 }
    })
  });
  const { dom, document } = setup(async () => response(JSON.stringify(value)), {
    url: 'https://ui.example.test/#topology', scheduler,
    configureWindow(win) { win.HTMLCanvasElement.prototype.getContext = () => null; }
  });
  submit(document, '#topology-form'); await flush();
  assert.equal(document.querySelector('#topology-workspace').getAttribute('aria-busy'), 'true');
  assert.equal(document.querySelector('#geo-map-result').childElementCount, 0);
  runAll();
  assert.equal(document.querySelector('#topology-workspace').getAttribute('aria-busy'), 'false');
  assert.equal(document.querySelectorAll('#topology-result .topology-route').length, 2);

  document.querySelector('[data-view-link="geo-map"]').click(); await flush(); runAll();
  assert.equal(document.querySelector('#topology-result').childElementCount, 0);
  assert.equal(document.querySelectorAll('#geo-map-result canvas').length, 1);
  const firstCanvas = document.querySelector('#geo-map-result canvas');
  assert.equal(firstCanvas.dataset.markers, '2');

  const target = document.querySelector('#topology-target-filter [data-target-index="1"]');
  target.checked = false;
  target.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  assert.equal(document.querySelector('#topology-result').childElementCount, 0);
  runAll();
  assert.equal(document.querySelector('#geo-map-result canvas').dataset.markers, '1');

  const queuedBeforeLabel = jobs.length;
  document.querySelector('#ip-label-address').value = '192.0.2.10';
  document.querySelector('#ip-label-name').value = 'Seoul edge';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  assert.equal(jobs.length, queuedBeforeLabel);
  assert.equal(document.querySelector('#topology-result').childElementCount, 0);
  document.querySelector('[data-view-link="topology"]').click(); await flush(); runAll();
  assert.equal(document.querySelector('#geo-map-result').childElementCount, 0);
  assert.match(document.querySelector('#topology-result [data-label-address="192.0.2.10"]').textContent, /^Seoul edge\b/);
});

test('expanded and mapped label aliases rerender active topology with notes and rich semantics intact', async () => {
  const jobs = []; const cancelled = new Set(); let id = 0;
  const scheduler = { schedule(callback) { const handle = ++id; jobs.push({ handle, callback }); return handle; }, cancel(handle) { cancelled.add(handle); } };
  const runAll = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.handle)) job.callback(); } };
  const topologyReport = report('label-aliases', {
    results: [result({ kind: 'traceroute', address: 'v6.example', details: {} })],
    compact_topology: compactTopology({
      nodes: [
        { id: 'n1', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
        { id: 'n2', kind: 'ip', address: '2001:db8::1', status: 'degraded', hop_min: 1, hop_max: 2, observations: 7, latency_ms_avg: 12.5, public_ip: true,
          geolocation: { city: 'Seoul', region: '', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 }, asn: { number: 64500, organization: 'Example Transit' } },
        { id: 'n3', kind: 'ip', address: '192.0.2.1', status: 'healthy', hop_min: 3, hop_max: 3, observations: 1 }
      ],
      links: [{ from: 'n1', to: 'n2', status: 'degraded', observations: 1 }, { from: 'n2', to: 'n3', status: 'healthy', observations: 1 }],
      routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['n1', 'n2', 'n3'] }],
      stats: {
        nodes: { total: 3, displayed: 3, omitted: 0 }, links: { total: 2, displayed: 2, omitted: 0 },
        routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
        node_observations: { total: 3, displayed: 3, omitted: 0 }, link_observations: { total: 2, displayed: 2, omitted: 0 }
      },
      result_stats: [{ result_index: 0, routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 }, node_observations: { total: 3, displayed: 3, omitted: 0 }, link_observations: { total: 2, displayed: 2, omitted: 0 } }]
    })
  });
  const { dom, app, document } = setup(async () => response(JSON.stringify(topologyReport)), {
    url: 'https://labels.example/#topology', scheduler,
    configureWindow(win) {
      win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify([
        { ip: '2001:0DB8:0:0:0:0:0:1', label: 'old expanded', note: '' },
        { ip: '2001:db8::1', label: 'old compact wins', note: '' },
        { ip: '::ffff:192.0.2.1', label: 'old mapped', note: '' },
        { ip: '192.0.2.1', label: 'old IPv4 wins', note: '' }
      ]));
    }
  });
  assert.deepEqual(JSON.parse(dom.window.localStorage.getItem('checknetwork.ip-labels.v1')).map(row => row.ip), ['192.0.2.1', '2001:db8::1']);
  await app.start('topology'); runAll();
  assert.equal(app.getState().topology.phase, 'ready', document.querySelector('#request-alert').textContent);
  document.querySelector('#topology-label-address').value = '2001:0DB8:0:0:0:0:0:1';
  document.querySelector('#topology-label-name').value = 'Core v6';
  document.querySelector('#topology-label-note').value = 'Primary path';
  document.querySelector('#topology-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  runAll();

  const node = document.querySelector('#topology-result [data-node-id="n2"]');
  for (const surface of [node.textContent, node.title, node.getAttribute('aria-label'), node.dataset.detail]) {
    for (const value of ['Core v6', '2001:db8::1', 'Primary path', 'degraded', 'HOP 1–2', '7 observations', '12.5 ms', 'Seoul', 'KR', 'AS64500', 'Example Transit']) assert.match(surface, new RegExp(value));
  }

  document.querySelector('#ip-label-address').value = '::ffff:192.0.2.1';
  document.querySelector('#ip-label-name').value = 'Mapped alias';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  runAll();
  assert.match(document.querySelector('#topology-result [data-label-address="192.0.2.1"]').textContent, /Mapped alias/);
});

test('render commit failure after network success exposes render-error and keeps explained raw download', async () => {
  const jobs = []; let id = 0; let downloaded = 0;
  const scheduler = { schedule(callback) { jobs.push({ id: ++id, callback }); return id; }, cancel() {} };
  const topologyReport = report('render-fault', {
    results: [result({ kind: 'traceroute', address: 'fault.example', details: {} })], compact_topology: compactTopology()
  });
  const { dom, app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://fault.example/#topology', scheduler });
  dom.window.URL.createObjectURL = () => 'blob:raw'; dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => { downloaded++; };
  await app.start('topology');
  const blocker = document.createElement('section');
  blocker.append(...Array.from({ length: 1200 }, () => document.createElement('i')));
  document.body.append(blocker);
  while (jobs.length) jobs.shift().callback();

  assert.equal(app.getState().topology.phase, 'ready', 'network result remains downloadable');
  assert.equal(document.querySelector('#topology-workspace').dataset.state, 'render-error');
  assert.equal(document.querySelector('#topology-workspace').getAttribute('aria-busy'), 'false');
  assert.match(document.querySelector('#topology-state-message').textContent, /표시.*오류|오류.*표시/);
  assert.match(document.querySelector('#topology-state-message').textContent, /원시.*JSON|JSON.*원시/);
  assert.doesNotMatch(document.querySelector('#topology-state-message').textContent, /화면 표시가 완료/);
  assert.equal(document.querySelector('#download-topology').disabled, false);
  document.querySelector('#download-topology').click();
  assert.equal(downloaded, 1);
});

test('nonrenderable topology exposes render-limited and explains the retained raw download', async () => {
  const topologyReport = report('render-limited', {
    results: [result({ kind: 'traceroute', address: 'limited.example', details: {} })], compact_topology: compactTopology()
  });
  const { app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://limited.example/#topology' });
  const blocker = document.createElement('section');
  blocker.append(...Array.from({ length: 1200 }, () => document.createElement('i')));
  document.body.append(blocker);
  await app.start('topology');

  assert.equal(document.querySelector('#topology-workspace').dataset.state, 'render-limited');
  assert.equal(document.querySelector('#topology-workspace').getAttribute('aria-busy'), 'false');
  assert.match(document.querySelector('#topology-state-message').textContent, /한도|제한/);
  assert.match(document.querySelector('#topology-state-message').textContent, /원시.*JSON|JSON.*원시/);
  assert.doesNotMatch(document.querySelector('#topology-state-message').textContent, /화면 표시가 완료/);
  assert.equal(document.querySelector('#download-topology').disabled, false);
});

test('unresponsive toggle removes unknown tails and reports actual planned omissions', async () => {
  const jobs = []; const cancelled = new Set(); let id = 0;
  const scheduler = {
    schedule(callback) { const handle = ++id; jobs.push({ handle, callback }); return handle; },
    cancel(handle) { cancelled.add(handle); }
  };
  const runAll = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.handle)) job.callback(); } };
  const nodes = [
    { id: 'local', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
    { id: 'known', kind: 'ip', address: '192.0.2.1', status: 'healthy', hop_min: 1, hop_max: 1, observations: 1 },
    { id: 'unknown', kind: 'unknown', address: '', status: 'unknown', hop_min: 2, hop_max: 2, observations: 1 },
    { id: 'tail', kind: 'ip', address: '192.0.2.3', status: 'healthy', hop_min: 3, hop_max: 3, observations: 1 }
  ];
  const links = nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: node.status, observations: 1 }));
  const routes = [{ result_index: 0, attempt: 1, status: 'unreachable', reached: false, complete: true, node_ids: nodes.map(node => node.id) }];
  const stats = {
    nodes: { total: 4, displayed: 4, omitted: 0 }, links: { total: 3, displayed: 3, omitted: 0 },
    routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
    node_observations: { total: 4, displayed: 4, omitted: 0 }, link_observations: { total: 3, displayed: 3, omitted: 0 }
  };
  const topologyReport = report('unknown-filter', {
    results: [result({ kind: 'traceroute', address: 'unknown.example', details: {} })],
    compact_topology: compactTopology({
      nodes, links, routes, stats,
      result_stats: [{ result_index: 0, routes: stats.routes, node_observations: stats.node_observations, link_observations: stats.link_observations }],
      geo: { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 }
    })
  });
  const { dom, app, document } = setup(async () => response(JSON.stringify(topologyReport)), { url: 'https://filter.example/#topology', scheduler });
  await app.start('topology');
  assert.match(document.querySelector('#topology-result-summary').textContent, /계획 중|pending/i);
  runAll();

  const toggle = document.querySelector('[data-toggle-unresponsive]');
  toggle.checked = false;
  toggle.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  assert.match(document.querySelector('#topology-result-summary').textContent, /계획 중|pending/i);
  runAll();

  assert.deepEqual([...document.querySelectorAll('#topology-result .topology-node')].map(node => node.dataset.nodeId), ['local', 'known']);
  assert.deepEqual([...document.querySelectorAll('#topology-result .topology-link')].map(link => `${link.dataset.from}>${link.dataset.to}`), ['local>known']);
  assert.equal(document.querySelector('#topology-result .topology-route').dataset.complete, 'false');
  const summary = document.querySelector('#topology-result-summary').textContent;
  assert.match(summary, /노드 2\/4[^·]*생략 2/);
  assert.match(summary, /링크 1\/3[^·]*생략 2/);
});

test('max 500/499/20 summary reports the actual bounded plan and dynamic omissions', async () => {
  const jobs = []; const cancelled = new Set(); let handle = 0;
  const scheduler = { schedule(callback) { jobs.push({ handle: ++handle, callback }); return handle; }, cancel(id) { cancelled.add(id); } };
  const runAll = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.handle)) job.callback(); } };
  const nodes = Array.from({ length: 500 }, (_, index) => ({
    id: `n${index}`, kind: index === 0 ? 'local' : 'ip', address: index === 0 ? 'local' : `198.18.${Math.floor(index / 256)}.${index % 256}`,
    status: 'healthy', hop_min: index % 30, hop_max: index % 30,
    observations: index > 0 && index % 25 === 0 && index < 500 ? 2 : 1
  }));
  const links = nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: 'healthy', observations: 1 }));
  const routes = Array.from({ length: 20 }, (_, result_index) => {
    const start = result_index * 25; const end = result_index === 19 ? 500 : start + 26;
    return { result_index, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: nodes.slice(start, end).map(node => node.id) };
  });
  const nodeObservations = routes.reduce((sum, route) => sum + route.node_ids.length, 0);
  const stats = {
    nodes: { total: 500, displayed: 500, omitted: 0 }, links: { total: 499, displayed: 499, omitted: 0 },
    routes: { total: 20, displayed: 20, complete: 20, partial: 0, omitted: 0 },
    node_observations: { total: nodeObservations, displayed: nodeObservations, omitted: 0 },
    link_observations: { total: 499, displayed: 499, omitted: 0 }
  };
  const results = routes.map((route, index) => result({ kind: 'traceroute', address: `target-${index}.example`, details: {} }));
  const value = report('max-plan', {
    summary: { total: 20, passed: 20, failed: 0 }, results,
    compact_topology: compactTopology({
      nodes, links, routes, stats,
      result_stats: routes.map(route => ({
        result_index: route.result_index,
        routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
        node_observations: { total: route.node_ids.length, displayed: route.node_ids.length, omitted: 0 },
        link_observations: { total: route.node_ids.length - 1, displayed: route.node_ids.length - 1, omitted: 0 }
      })),
      geo: { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 }
    })
  });
  const { app, document } = setup(async () => response(JSON.stringify(value)), { url: 'https://max.example/#topology', scheduler });
  await app.start('topology'); runAll();

  const displayed = {
    nodes: document.querySelectorAll('#topology-result .topology-node').length,
    links: document.querySelectorAll('#topology-result .topology-link').length,
    routes: document.querySelectorAll('#topology-result .topology-route').length
  };
  const summary = document.querySelector('#topology-result-summary').textContent;
  for (const [key, label, total] of [['nodes', '노드', 500], ['links', '링크', 499], ['routes', '경로', 20]]) {
    assert.match(summary, new RegExp(`${label} ${displayed[key]}/${total} \\(생략 ${total - displayed[key]}\\)`));
  }
  assert.ok(document.querySelectorAll('*').length <= 1200);
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
  assert.equal(dom.window.localStorage.getItem('checknetwork.ip-labels.v1'), '[]');
  assert.equal(dom.window.localStorage.length, 1);
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

test('shared fullscreen controller reports failures, tracks exit, and restores invoker focus', async () => {
  const { dom, document } = setup();
  const topologyButton = document.querySelector('#topology-fullscreen');
  const topologyTarget = document.querySelector('#topology-result');
  assert.equal(topologyButton.getAttribute('aria-pressed'), 'false');
  topologyTarget.requestFullscreen = async () => { throw new Error('denied'); };
  topologyButton.click(); await flush();
  assert.match(document.querySelector('#fullscreen-status').textContent, /전체 화면.*못|실패/);
  assert.equal(topologyButton.getAttribute('aria-pressed'), 'false');

  topologyTarget.requestFullscreen = async () => {
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: topologyTarget });
    document.dispatchEvent(new dom.window.Event('fullscreenchange'));
  };
  topologyButton.focus(); topologyButton.click(); await flush();
  assert.equal(topologyButton.getAttribute('aria-pressed'), 'true');
  Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: null });
  document.dispatchEvent(new dom.window.Event('fullscreenchange'));
  assert.equal(topologyButton.getAttribute('aria-pressed'), 'false');
  assert.equal(document.activeElement, topologyButton);

  const space = new dom.window.KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
  topologyButton.dispatchEvent(space);
  assert.equal(space.defaultPrevented, false, 'native button Space behavior must remain browser-owned');
});

test('fullscreen controller announces unsupported API without throwing', async () => {
  const { document } = setup();
  document.querySelector('#geo-map-result').requestFullscreen = undefined;
  document.querySelector('#geo-map-fullscreen').click(); await flush();
  assert.match(document.querySelector('#fullscreen-status').textContent, /지원하지/);
});

test('deferred fullscreen rejection from destroyed app cannot alter recreated app status or focus', async () => {
  const dom = new JSDOM(markup, { url: 'https://fullscreen.example/#topology' });
  const document = dom.window.document;
  let rejectFullscreen;
  const oldApp = createApp({ document, window: dom.window, fetchImpl: async () => response(JSON.stringify(report('old'))) });
  document.querySelector('#topology-result').requestFullscreen = () => new Promise((_, reject) => { rejectFullscreen = reject; });
  document.querySelector('#topology-fullscreen').click();
  await flush();
  oldApp.destroy();

  const newApp = createApp({ document, window: dom.window, fetchImpl: async () => response(JSON.stringify(report('new'))) });
  document.querySelector('#topology-result').requestFullscreen = undefined;
  const newButton = document.querySelector('#topology-fullscreen');
  newButton.click(); await flush();
  newButton.focus();
  const statusBefore = document.querySelector('#fullscreen-status').textContent;
  const focusBefore = document.activeElement;

  rejectFullscreen(new Error('late denial'));
  await flush(); await flush();
  assert.equal(document.querySelector('#fullscreen-status').textContent, statusBefore);
  assert.equal(document.activeElement, focusBefore);
  assert.match(statusBefore, /지원하지/);
  newApp.destroy();
});

test('topology app delegation preserves native activation keys', () => {
  const { dom, document } = setup();
  const root = document.querySelector('#topology-result');
  const button = document.createElement('button'); button.type = 'button'; button.className = 'topology-node'; button.dataset.labelAddress = '203.0.113.10';
  root.append(button);
  for (const key of ['Enter', ' ']) {
    const event = new dom.window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true });
    button.dispatchEvent(event);
    assert.equal(event.defaultPrevented, false, key);
  }
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

test('IP-label table exposes an accessible caption and scoped headers', () => {
  const { document } = setup();
  const table = document.querySelector('.mapping-table');
  assert.ok(table.querySelector('caption') || table.getAttribute('aria-label') || table.getAttribute('aria-labelledby'));
  assert.deepEqual([...table.querySelectorAll('thead th')].map(th => th.getAttribute('scope')), ['col', 'col', 'col', 'col']);
});

test('Canvas Geo view does not request or store unused tile credentials', () => {
  const { dom, document } = setup();
  assert.equal(document.querySelector('#carto-base-map-form'), null);
  assert.match(document.querySelector('.canvas-map-notice').textContent, /외부 타일.*credential/);
  assert.equal(dom.window.sessionStorage.getItem('checknetwork.carto-base-map.v1'), null);
});

test('Geo copy describes the bounded Canvas overview without promising interactive tiles', () => {
  const { document } = setup();
  const description = document.querySelector('#geo-map-view .section-description').textContent;
  assert.match(description, /Canvas|캔버스/);
  assert.match(description, /제한|bounded/i);
  assert.doesNotMatch(description, /확대.*이동|타일|interactive/i);
});

test('IP-label form remains wired with progressive pagination', async () => {
  const { dom, document } = setup();
  document.querySelector('#ip-label-address').value = '203.0.113.10';
  document.querySelector('#ip-label-name').value = 'Seoul edge';
  document.querySelector('#ip-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await flush();
  assert.match(document.querySelector('#ip-label-rows').textContent, /203\.0\.113\.10/);
  assert.equal(document.querySelector('#ip-label-rows [data-field="label"]').value, 'Seoul edge');
});

test('responsive and accessibility contracts cover 320/375/400, focus, reduced motion, and wide component scroll', async () => {
  const css = await readFile(new URL('./styles.css', import.meta.url), 'utf8');
  assert.match(css, /@media \(max-width:400px\)/);
  assert.match(css, /@media \(max-width:320px\)/);
  assert.match(css, /@media \(max-width:760px\)/);
  assert.match(css, /focus-visible/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /evidence-scroll[^}]*overflow-x:auto|overflow-x:auto[^}]*evidence-scroll/s);
  assert.match(css, /\.standalone-topology\s*\{[^}]*display:grid/s);
  assert.match(css, /\.standalone-topology\s+\.topology-node\s*\{/s);
  assert.match(css, /\.topology-geo-canvas\s*\{/s);
  assert.match(css, /\.standalone-topology\s+\[data-detail\]:(?:hover|focus)[^}]*::after/s);
  assert.match(css, /--observation-width/);
  assert.match(css, /--route-color/);
});
