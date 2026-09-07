import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';

const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const started = '2026-09-01T12:00:00Z';
const result = (overrides = {}) => ({ kind: 'dns', address: 'example.test', status: 'healthy', latency_ms: 4, started_at: started, details: {}, ...overrides });
const report = (id, overrides = {}) => ({ id, status: 'healthy', started_at: started, duration_ms: 10, summary: { total: 1, passed: 1, failed: 0 }, results: [result()], ...overrides });
const compactTraceResult = (address, {
  attemptsTotal = 1, attemptsReached = attemptsTotal, attemptsUnreached = 0,
  status = 'healthy', errorCode
} = {}) => result({
  kind: 'traceroute', address, status,
  ...(errorCode ? { error_code: errorCode } : {}),
  details: {
    attempts_total: attemptsTotal,
    attempts_reached: attemptsReached,
    attempts_failed: attemptsTotal - attemptsReached,
    attempts_unreached: attemptsUnreached,
    attempts_execution_failed: 0,
    attempts_timed_out: 0,
    attempts_cancelled: 0
  }
});
const compactTopology = (overrides = {}) => ({
  schema: 'compact-v1', selection: 'fair-complete-prefix-v1',
  limits: { nodes: 500, links: 1000, max_response_bytes_exclusive: 1048576, max_geo_bundle_bytes: 4096 },
  nodes: [
    { id: 'n1', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
    { id: 'n2', kind: 'ip', address: '192.0.2.1', status: 'healthy', hop_min: 1, hop_max: 1, observations: 1, public_ip: true, geolocation: { city: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 } }
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
  truncated: false, ...overrides
});
const fullAnalysis = (value = 'safe') => ({
  verdict: 'attention',
  findings: [{ id: 'f1', code: 'dns_resolution_failed', severity: 'critical', category: 'name_resolution', title: value, summary: 'No answer', confidence: 'direct', evidence_ids: ['e1'], action_ids: ['a1'] }],
  evidence: [{ id: 'e1', result_index: 0, kind: 'dns', address: 'example.test', signal: 'error_code', observed: value, expected: 'answer', provenance: 'result' }],
  actions: [{ id: 'a1', title: 'Check DNS', step: value, expected_result: 'answer', escalation_condition: 'still fails' }],
  coverage: { available: ['results[0].status'], missing: ['results[0].details'], provider_failures: [], limitations: [] }
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

const credentialBase = 'http://localhost:9090';
const credentialKey = base => `checknetwork.bearer.v1:${encodeURIComponent(base)}`;
function installCredentialStorageFaults(win, { initial = [], steps = [] } = {}) {
  const storage = win.sessionStorage;
  const prototype = win.Storage.prototype;
  const originals = Object.fromEntries(['getItem', 'setItem', 'removeItem'].map(name => [name, prototype[name]]));
  const values = new Map(initial);
  const calls = [];
  function invoke(method, args, normal) {
    if (this !== storage) return originals[method].apply(this, args);
    const step = steps[calls.length];
    calls.push({ method, key: args[0] });
    if (step) assert.equal(method, step.method, `storage operation ${calls.length}`);
    if (step?.throw) throw new Error('injected storage failure');
    if (step?.mismatch) return step.value;
    if (step?.noop) return undefined;
    return normal();
  };
  prototype.getItem = function (key) { return invoke.call(this, 'getItem', [key], () => values.get(key) ?? null); };
  prototype.setItem = function (key, value) { return invoke.call(this, 'setItem', [key, value], () => { values.set(key, String(value)); }); };
  prototype.removeItem = function (key) { return invoke.call(this, 'removeItem', [key], () => { values.delete(key); }); };
  return { values, calls };
}

test('credential transaction rejects blank Apply without storage mutation or lane invalidation', async () => {
  let aborts = 0; let faults;
  const { document, app } = setup((_url, init) => new Promise((_, reject) => {
    init.signal.addEventListener('abort', () => {
      aborts++;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    });
  }), { configureWindow(win) { faults = installCredentialStorageFaults(win); } });
  const pending = app.start('diagnostics'); await flush();
  const signature = app.getState().diagnostics.inputSignature;
  document.querySelector('#bearer-token').value = '   ';
  document.querySelector('#apply-bearer').click();
  assert.deepEqual(faults.calls, []);
  assert.equal(aborts, 0);
  assert.equal(app.getState().diagnostics.inputSignature, signature);
  assert.equal(app.getState().diagnostics.phase, 'loading');
  assert.equal(document.querySelector('#bearer-token').value, '   ');
  assert.match(document.querySelector('#credential-status').textContent, /required|입력|비어/i);
  app.cancel('diagnostics'); await pending;
});

test('credential transaction commits Apply and explicit Clear only after exact readback', async () => {
  let faults; const requests = [];
  const key = credentialKey(credentialBase);
  const { document, app } = setup(async (_url, init) => {
    requests.push(init);
    return response(JSON.stringify(report(`credential-${requests.length}`)));
  }, { configureWindow(win) {
    faults = installCredentialStorageFaults(win, { steps: [
      { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem' }, { method: 'getItem' },
      { method: 'getItem' }, { method: 'removeItem' }, { method: 'getItem' }, { method: 'getItem' }
    ] });
  } });
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = 'transaction-secret';
  document.querySelector('#apply-bearer').click();
  assert.equal(faults.values.get(key), 'transaction-secret');
  assert.equal(document.querySelector('#bearer-token').value, '');
  assert.match(document.querySelector('#credential-status').textContent, /설정|configured/i);
  await app.start('diagnostics');
  const appliedSignature = app.getState().diagnostics.inputSignature;
  assert.equal(requests[0].headers.Authorization, 'Bearer transaction-secret');

  document.querySelector('#bearer-token').value = 'must-not-be-written';
  document.querySelector('#clear-bearer').click();
  assert.equal(faults.values.has(key), false);
  assert.equal(document.querySelector('#bearer-token').value, '');
  assert.match(document.querySelector('#credential-status').textContent, /삭제|removed|cleared/i);
  await app.start('diagnostics');
  assert.notEqual(app.getState().diagnostics.inputSignature, appliedSignature);
  assert.equal(Object.hasOwn(requests[1].headers, 'Authorization'), false);
  assert.deepEqual(faults.calls.map(call => call.method), [
    'getItem', 'setItem', 'getItem', 'getItem',
    'getItem', 'removeItem', 'getItem', 'getItem'
  ]);
  assert.doesNotMatch(document.body.textContent, /transaction-secret|must-not-be-written/);
});

test('credential transaction storage fault matrix distinguishes preserved from indeterminate state', async t => {
  const previous = 'previous-secret';
  const requested = 'requested-secret';
  const key = credentialKey(credentialBase);
  const preservedMessage = 'Credential 업데이트에 실패했습니다. 이전 credential은 변경되지 않았습니다.';
  const reconciliationMessage = 'Credential 상태를 확인할 수 없습니다. 인증 요청 전에 credential을 다시 적용하거나 삭제해 주세요.';
  const cases = [
    { name: 'Apply initial read throws', action: 'apply', steps: [{ method: 'getItem', throw: true }], methods: ['getItem'], indeterminate: false },
    { name: 'Clear initial read throws', action: 'clear', steps: [{ method: 'getItem', throw: true }], methods: ['getItem'], indeterminate: false },
    { name: 'Apply set throws', action: 'apply', steps: [{ method: 'getItem' }, { method: 'setItem', throw: true }], methods: ['getItem', 'setItem'], indeterminate: false },
    { name: 'Clear remove throws', action: 'clear', steps: [{ method: 'getItem' }, { method: 'removeItem', throw: true }], methods: ['getItem', 'removeItem'], indeterminate: false },
    { name: 'Apply readback throws and rollback verifies', action: 'apply', steps: [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true }, { method: 'setItem' }, { method: 'getItem' }], indeterminate: false },
    { name: 'Clear readback mismatches and rollback verifies', action: 'clear', steps: [{ method: 'getItem' }, { method: 'removeItem' }, { method: 'getItem', mismatch: true, value: 'phantom' }, { method: 'setItem' }, { method: 'getItem' }], indeterminate: false },
    { name: 'Apply silent set mismatch rolls back previous value', action: 'apply', steps: [{ method: 'getItem' }, { method: 'setItem', noop: true }, { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem' }], indeterminate: false },
    { name: 'Clear silent remove mismatch rolls back previous value', action: 'clear', steps: [{ method: 'getItem' }, { method: 'removeItem', noop: true }, { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem' }], indeterminate: false },
    { name: 'Apply with no previous value rolls back by removing', action: 'apply', previousNull: true, steps: [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true }, { method: 'removeItem' }, { method: 'getItem' }], indeterminate: false },
    { name: 'Apply rollback set throws', action: 'apply', steps: [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true }, { method: 'setItem', throw: true }, { method: 'getItem' }], indeterminate: true },
    { name: 'Apply rollback remove throws with no previous value', action: 'apply', previousNull: true, steps: [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true }, { method: 'removeItem', throw: true }, { method: 'getItem' }], indeterminate: true },
    { name: 'Clear rollback set throws', action: 'clear', steps: [{ method: 'getItem' }, { method: 'removeItem' }, { method: 'getItem', throw: true }, { method: 'setItem', throw: true }, { method: 'getItem' }], indeterminate: true },
    { name: 'Apply rollback read throws', action: 'apply', steps: [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true }, { method: 'setItem' }, { method: 'getItem', throw: true }], indeterminate: true },
    { name: 'Clear rollback read mismatches', action: 'clear', steps: [{ method: 'getItem' }, { method: 'removeItem' }, { method: 'getItem', throw: true }, { method: 'setItem' }, { method: 'getItem', mismatch: true, value: 'phantom' }], indeterminate: true }
  ];
  for (const fixture of cases) await t.test(fixture.name, async () => {
    let faults; let fetches = 0;
    const { document, app } = setup(async () => {
      fetches++;
      return response(JSON.stringify(report(`fault-${fetches}`)));
    }, { configureWindow(win) {
      faults = installCredentialStorageFaults(win, { initial: fixture.previousNull ? [] : [[key, previous]], steps: fixture.steps });
    } });
    await app.start('diagnostics');
    const laneBefore = app.getState().diagnostics;
    document.querySelector('#public-auth-enabled').checked = true;
    document.querySelector('#bearer-token').value = requested;
    document.querySelector(fixture.action === 'clear' ? '#clear-bearer' : '#apply-bearer').click();

    assert.equal(document.querySelector('#bearer-token').value, requested, fixture.name);
    assert.deepEqual(faults.calls.map(call => call.method), fixture.methods ?? fixture.steps.map(step => step.method), fixture.name);
    assert.equal(document.querySelector('#credential-status').textContent, fixture.indeterminate ? reconciliationMessage : preservedMessage, fixture.name);
    assert.doesNotMatch(document.body.textContent, /previous-secret|requested-secret|phantom/, fixture.name);
    if (!fixture.indeterminate) {
      assert.strictEqual(app.getState().diagnostics, laneBefore, fixture.name);
      assert.equal(faults.values.get(key) ?? null, fixture.previousNull ? null : previous, fixture.name);
      return;
    }
    assert.equal(app.getState().diagnostics.phase, 'idle', fixture.name);
    assert.notStrictEqual(app.getState().diagnostics, laneBefore, fixture.name);
    const callsBeforeBlockedRequest = faults.calls.length;
    await app.start('diagnostics');
    assert.equal(fetches, 1, fixture.name);
    assert.equal(faults.calls.length, callsBeforeBlockedRequest, fixture.name);
    assert.equal(app.getState().diagnostics.phase, 'error', fixture.name);
    assert.equal(app.getState().diagnostics.error.code, 'credential_indeterminate', fixture.name);
    assert.equal(app.getState().diagnostics.error.message, reconciliationMessage, fixture.name);
    await app.start('topology');
    assert.equal(fetches, 1, `${fixture.name}: topology fetch`);
    assert.equal(faults.calls.length, callsBeforeBlockedRequest, `${fixture.name}: topology storage read`);
    assert.equal(app.getState().topology.phase, 'error', fixture.name);
    assert.equal(app.getState().topology.error.code, 'credential_indeterminate', fixture.name);
  });
});

test('credential indeterminate invalidation survives cleanup throws and rejects both stale lane owners', async () => {
  const waits = { diagnostics: deferred(), topology: deferred() }; const pending = []; const aborts = { diagnostics: 0, topology: 0 };
  let faults;
  const { document, app } = setup((_url, init) => {
    const purpose = pending.length === 0 ? 'diagnostics' : 'topology';
    pending.push(purpose);
    init.signal.addEventListener('abort', () => { aborts[purpose]++; });
    return waits[purpose].promise;
  }, { configureWindow(win) {
    faults = installCredentialStorageFaults(win, { initial: [[credentialKey(credentialBase), 'old']], steps: [
      { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true },
      { method: 'setItem', throw: true }, { method: 'getItem' }
    ] });
    const abort = win.AbortController.prototype.abort;
    win.AbortController.prototype.abort = function (reason) {
      const result = abort.call(this, reason);
      if (reason === 'input-change') throw new Error('injected abort cleanup failure');
      return result;
    };
  } });
  const diagnostics = app.start('diagnostics');
  const topology = app.start('topology');
  await flush();
  const signatures = {
    diagnostics: app.getState().diagnostics.inputSignature,
    topology: app.getState().topology.inputSignature
  };
  document.querySelector('#bearer-token').value = 'new-secret';
  document.querySelector('#apply-bearer').click();

  assert.deepEqual(aborts, { diagnostics: 1, topology: 1 });
  for (const purpose of ['diagnostics', 'topology']) {
    assert.equal(app.getState()[purpose].phase, 'idle', purpose);
    assert.equal(app.getState()[purpose].active, null, purpose);
    assert.notEqual(app.getState()[purpose].inputSignature, signatures[purpose], purpose);
  }
  waits.diagnostics.resolve(response(JSON.stringify(report('stale-diagnostics'))));
  waits.topology.resolve(response(JSON.stringify(report('stale-topology'))));
  await Promise.all([diagnostics, topology]); await flush();
  assert.doesNotMatch(document.body.textContent, /stale-diagnostics|stale-topology/);
  assert.equal(document.querySelector('#bearer-token').value, 'new-secret');
  assert.equal(faults.calls.length, 5);
});

test('explicit Apply and Clear each recover an indeterminate base only after a fresh verified transaction', async t => {
  for (const recovery of ['Apply', 'Clear']) await t.test(recovery, async () => {
    const key = credentialKey(credentialBase); let faults; const requests = [];
    const recoverySteps = recovery === 'Apply'
      ? [{ method: 'getItem' }, { method: 'setItem' }, { method: 'getItem' }, { method: 'getItem' }]
      : [{ method: 'getItem' }, { method: 'removeItem' }, { method: 'getItem' }, { method: 'getItem' }];
    const { document, app } = setup(async (_url, init) => {
      requests.push(init);
      return response(JSON.stringify(report(`recovered-${recovery}`)));
    }, { configureWindow(win) {
      faults = installCredentialStorageFaults(win, { initial: [[key, 'old-secret']], steps: [
        { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true },
        { method: 'setItem', throw: true }, { method: 'getItem' }, ...recoverySteps
      ] });
    } });
    document.querySelector('#public-auth-enabled').checked = true;
    document.querySelector('#bearer-token').value = 'failed-secret';
    document.querySelector('#apply-bearer').click();
    const indeterminateSignature = app.getState().diagnostics.inputSignature;
    assert.match(document.querySelector('#credential-status').textContent, /확인할 수 없습니다|reconcil/i);
    assert.doesNotMatch(document.querySelector('#credential-status').textContent, /이전.*변경되지|previous.*unchanged/i);
    await app.start('diagnostics');
    assert.equal(requests.length, 0, recovery);

    document.querySelector('#bearer-token').value = 'recovery-secret';
    document.querySelector(recovery === 'Apply' ? '#apply-bearer' : '#clear-bearer').click();
    assert.equal(document.querySelector('#bearer-token').value, '', recovery);
    assert.notEqual(app.getState().diagnostics.inputSignature, indeterminateSignature, recovery);
    await app.start('diagnostics');
    assert.equal(requests.length, 1, recovery);
    if (recovery === 'Apply') {
      assert.equal(requests[0].headers.Authorization, 'Bearer recovery-secret');
      assert.equal(faults.values.get(key), 'recovery-secret');
    } else {
      assert.equal(Object.hasOwn(requests[0].headers, 'Authorization'), false);
      assert.equal(faults.values.has(key), false);
    }
    assert.equal(faults.calls.length, 9, recovery);
  });
});

test('credential updates invalidate only lanes owned by the same canonical API base', async () => {
  const wait = deferred(); let aborts = 0; let faults;
  const otherBase = 'https://other.example.test/api';
  const { document, app } = setup((_url, init) => {
    init.signal.addEventListener('abort', () => { aborts++; });
    return wait.promise;
  }, { configureWindow(win) {
    faults = installCredentialStorageFaults(win, { steps: [
      { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem' }
    ] });
  } });
  const pending = app.start('diagnostics'); await flush();
  const laneBefore = app.getState().diagnostics;
  document.querySelector('#api-base-url').value = `${otherBase}/`;
  document.querySelector('#bearer-token').value = 'other-base-secret';
  document.querySelector('#apply-bearer').click();

  assert.equal(aborts, 0);
  assert.strictEqual(app.getState().diagnostics, laneBefore);
  assert.equal(app.getState().diagnostics.phase, 'loading');
  assert.equal(faults.values.get(credentialKey(otherBase)), 'other-base-secret');
  wait.resolve(response(JSON.stringify(report('original-base-owner'))));
  await pending;
  assert.equal(app.getState().diagnostics.phase, 'ready');
  assert.equal(app.getState().diagnostics.result.report.id, 'original-base-owner');
  assert.doesNotMatch(document.body.textContent, /original-base-owner/);
});

test('credential indeterminate state is isolated when the canonical API base changes', async () => {
  const otherBase = 'https://other.example.test/api'; let faults; const requests = [];
  const { dom, document, app } = setup(async (url, init) => {
    requests.push({ url, init });
    return response(JSON.stringify(report('other-base')));
  }, { configureWindow(win) {
    faults = installCredentialStorageFaults(win, { initial: [[credentialKey(credentialBase), 'old-secret']], steps: [
      { method: 'getItem' }, { method: 'setItem' }, { method: 'getItem', throw: true },
      { method: 'setItem', throw: true }, { method: 'getItem' }, { method: 'getItem' }
    ] });
  } });
  document.querySelector('#public-auth-enabled').checked = true;
  document.querySelector('#bearer-token').value = 'failed-secret';
  document.querySelector('#apply-bearer').click();

  document.querySelector('#api-base-url').value = `${otherBase}/`;
  document.querySelector('#api-base-url').dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  await app.start('diagnostics');
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, `${otherBase}/api/v1/reports`);
  assert.equal(Object.hasOwn(requests[0].init.headers, 'Authorization'), false);
  assert.equal(faults.calls.length, 6);

  document.querySelector('#api-base-url').value = `${credentialBase}/`;
  document.querySelector('#api-base-url').dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  await app.start('diagnostics');
  assert.equal(requests.length, 1);
  assert.equal(faults.calls.length, 6);
  assert.equal(app.getState().diagnostics.error.code, 'credential_indeterminate');
});

test('coverage renders fixed enrichment summaries after available and missing without reflecting prose', async () => {
  const serialized = await readFile(new URL('../testdata/enrichment-failures-report.json', import.meta.url), 'utf8');
  const parsed = JSON.parse(serialized);
  parsed.analysis.coverage.available = ['results[0].status'];
  parsed.analysis.coverage.missing = ['results[0].details'];
  // Isolate the enrichment renderer from the fixture's correctly derived
  // aggregate provider-failure coverage, which is rendered separately.
  parsed.analysis.coverage.provider_failures = [];
  const { app, document } = setup(async () => response(JSON.stringify(parsed)));
  await app.start('diagnostics');
  const coverage = document.querySelector('[data-analysis-section="coverage"]');
  assert.ok(coverage);
  const text = coverage.textContent;
  assert.doesNotMatch(text, /SAFE-AVAILABLE|SAFE-MISSING/);
  assert.match(text, /GeoIP enrichment source category: none/);
  assert.match(text, /GeoIP cache hits: 0/);
  assert.match(text, /GeoIP upstream fetches: 0/);
  assert.match(text, /GeoIP maximum age milliseconds: 0/);
  assert.match(text, /GeoIP busy failures: 1; retry category: retryable/);
  assert.match(text, /GeoIP cancelled failures: 2; retry category: not retryable/);
  assert.ok(coverage.compareDocumentPosition(document.querySelector('[data-analysis-section="raw"]')) & document.defaultView.Node.DOCUMENT_POSITION_FOLLOWING);
  assert.doesNotMatch(text, /provider/i);

  const hostile = JSON.parse(serialized);
  hostile.analysis.coverage.enrichment[0].provider = 'PROVIDER-CANARY';
  hostile.analysis.coverage.enrichment[0].url = 'URL-CANARY';
  hostile.analysis.coverage.enrichment[0].ip = 'IP-CANARY';
  hostile.analysis.coverage.enrichment[0].target = 'TARGET-CANARY';
  hostile.analysis.coverage.enrichment[0].error = 'ERROR-CANARY';
  const rejected = setup(async () => response(JSON.stringify(hostile)));
  await rejected.app.start('diagnostics');
  assert.doesNotMatch(rejected.document.body.textContent, /CANARY/);
  assert.match(rejected.document.body.textContent, /expected schema|response|응답/i);
});

test('concurrent apps send only their own diagnostics and topology inputs', async () => {
  const callsA = []; const callsB = [];
  const topology = address => report(`topology-${address}`, {
    results: [compactTraceResult(address)],
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
    results: [compactTraceResult('focus.example')],
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
    results: [compactTraceResult('focus.example')],
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
    results: [compactTraceResult('focus.example')],
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

test('destroy then recreate resets topology view controls and leaves stale mode owners inert', () => {
  const dom = new JSDOM(markup, { url: 'https://hmr-view.example/#topology' });
  const document = dom.window.document;
  const first = createApp({ document, window: dom.window, fetchImpl: async () => response('{}') });
  const threeD = document.querySelector('[name="topology-view-mode"][value="3d"]');
  threeD.checked = true;
  threeD.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  assert.equal(document.querySelector('#topology-view-status').textContent, '3D 그래프');
  first.destroy();

  const second = createApp({ document, window: dom.window, fetchImpl: async () => response('{}') });
  assert.equal(document.querySelector('[name="topology-view-mode"][value="2d"]').checked, true);
  assert.equal(document.querySelector('[name="topology-view-mode"][value="3d"]').checked, false);
  assert.equal(document.querySelector('#topology-view-status').textContent, '2D 그래프');
  second.destroy();
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
  assert.equal(newApp.getState().diagnostics.result.report.id, 'new');
  assert.doesNotMatch(dom.window.document.querySelector('#analysis-report').textContent, /\bnew\b/);
  assert.equal(newApp.destroy(), true);
});

test('target rows stop exactly at 20 and survive remove, re-add, and HMR without leaked ownership', t => {
  let audit;
  const { dom, document, app } = setup(undefined, { configureWindow(win) {
    const prototype = win.EventTarget.prototype;
    const add = prototype.addEventListener;
    const remove = prototype.removeEventListener;
    const registrations = [];
    const belongsToTargetRow = target => target instanceof win.Element && Boolean(target.closest('.target-row'));
    prototype.addEventListener = function (type, handler, options) {
      if (belongsToTargetRow(this)) registrations.push({ target: this, type, handler, options, active: true });
      return add.call(this, type, handler, options);
    };
    prototype.removeEventListener = function (type, handler, options) {
      const registration = registrations.findLast(item => item.active && item.target === this && item.type === type && item.handler === handler);
      if (registration) registration.active = false;
      return remove.call(this, type, handler, options);
    };
    audit = { active: () => registrations.filter(item => item.active).length };
  } });
  const add = document.querySelector('#add-target');
  const status = document.querySelector('#request-live');
  const statusCount = document.querySelectorAll('[role="status"]').length;
  let maximumObserved = document.querySelectorAll('*').length;
  const observe = () => { maximumObserved = Math.max(maximumObserved, document.querySelectorAll('*').length); };
  const rows = () => [...document.querySelectorAll('#targets .target-row')];
  const assertNames = () => rows().forEach((row, offset) => {
    const number = offset + 1;
    assert.equal(row.querySelector('select').getAttribute('aria-label'), `${number}번째 검사 종류`);
    assert.equal(row.querySelector('input:not(.expected)').getAttribute('aria-label'), `${number}번째 검사 주소`);
    assert.equal(row.querySelector('.expected').getAttribute('aria-label'), `${number}번째 기대 HTTP 상태`);
    assert.equal(row.querySelector('.icon-button').getAttribute('aria-label'), `${number}번째 검사 삭제`);
    assert.equal(row.querySelectorAll('[id]').length, 0);
  });

  assert.equal(rows().length, 4);
  assert.equal(audit.active(), 8);
  add.focus();
  for (let count = 4; count < 19; count++) { add.click(); observe(); }
  assert.equal(rows().length, 19, '4→19 accepts exactly fifteen additions');
  assert.equal(document.activeElement, add);
  const listenersAt19 = audit.active();

  add.click(); observe();
  assert.equal(rows().length, 20, '19→20 is accepted');
  assert.equal(audit.active(), listenersAt19 + 2);
  assert.equal(add.disabled, true);
  assert.ok(document.activeElement === add || document.activeElement === rows().at(-1).querySelector('.icon-button'));
  const elementsAt20 = document.querySelectorAll('*').length;
  const listenersAt20 = audit.active();
  for (let index = 0; index < 300; index++) {
    add.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, detail: index % 2 }));
    observe();
  }
  assert.equal(rows().length, 20, '20→304 attempted activations are rejected');
  assert.equal(document.querySelectorAll('*').length, elementsAt20, 'refusal creates no partial DOM');
  assert.equal(audit.active(), listenersAt20, 'refusal creates no listeners');
  assert.equal(document.querySelectorAll('[role="status"]').length, statusCount, 'announcement uses a fixed live region');
  assert.match(status.textContent, /최대 20|20.*maximum/i);
  assert.ok(status.textContent.length <= 80);
  assertNames();

  const removed = rows()[9].querySelector('.icon-button');
  removed.focus(); removed.click(); observe();
  assert.equal(rows().length, 19);
  assert.equal(add.disabled, false);
  assert.equal(audit.active(), listenersAt20 - 2, 'removed row listeners detach immediately');
  assert.equal(document.activeElement, rows()[9].querySelector('.icon-button'), 'focus moves to the relevant surviving remove control');
  assertNames();

  add.click(); observe();
  assert.equal(rows().length, 20);
  assert.equal(add.disabled, true);
  assert.equal(audit.active(), listenersAt20);
  assertNames();

  app.destroy();
  assert.equal(audit.active(), 0, 'destroy detaches every row listener');
  const recreated = createApp({ document, window: dom.window, fetchImpl: async () => response(JSON.stringify(report('hmr'))) });
  assert.equal(rows().length, 20, 'HMR adopts rather than duplicates retained rows');
  assert.equal(audit.active(), listenersAt20);
  const retainedSelect = rows()[0].querySelector('select');
  retainedSelect.value = 'https';
  retainedSelect.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  assert.equal(rows()[0].querySelector('.expected').hidden, false, 'successor owns retained row behavior');
  rows().at(-1).querySelector('.icon-button').click();
  assert.equal(rows().length, 19);
  add.click(); observe();
  assert.equal(rows().length, 20, 'successor owns Add exactly once');
  recreated.destroy();
  assert.equal(audit.active(), 0);
  const staleCount = rows().length;
  rows()[0].querySelector('.icon-button').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  add.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  assert.equal(rows().length, staleCount, 'destroyed app controls cannot mutate retained rows');
  assert.ok(maximumObserved <= 1200, `target row peak ${maximumObserved}`);
  t.diagnostic(`target row DOM peak=${maximumObserved}`);
});

test('target row admission counts hidden retained views and accepts exact DOM fit but rejects plus one atomically', t => {
  function boundary(extra) {
    let audit;
    const value = setup(undefined, { configureWindow(win) {
      const prototype = win.EventTarget.prototype;
      const add = prototype.addEventListener;
      const remove = prototype.removeEventListener;
      const registrations = [];
      const belongsToTargetRow = target => target instanceof win.Element && Boolean(target.closest('.target-row'));
      prototype.addEventListener = function (type, handler, options) {
        if (belongsToTargetRow(this)) registrations.push({ target: this, type, handler, active: true });
        return add.call(this, type, handler, options);
      };
      prototype.removeEventListener = function (type, handler, options) {
        const registration = registrations.findLast(item => item.active && item.target === this && item.type === type && item.handler === handler);
        if (registration) registration.active = false;
        return remove.call(this, type, handler, options);
      };
      audit = { active: () => registrations.filter(item => item.active).length };
    } });
    const { dom, document } = value;
    const add = document.querySelector('#add-target');
    for (let count = 4; count < 19; count++) add.click();
    assert.equal(document.querySelectorAll('#targets .target-row').length, 19);
    const candidate = document.querySelector('#target-template').content.firstElementChild;
    const candidateElements = candidate.querySelectorAll('*').length + 1;
    const ballastElements = 1200 - document.querySelectorAll('*').length - candidateElements + extra;
    assert.ok(ballastElements >= 1);
    const ballast = document.createElement('aside');
    ballast.dataset.retainedViewBallast = 'true';
    ballast.append(...Array.from({ length: ballastElements - 1 }, () => document.createElement('i')));
    const retainedView = document.querySelector('#ip-labels-view');
    assert.equal(retainedView.hidden, true);
    retainedView.append(ballast);
    const before = {
      elements: document.querySelectorAll('*').length,
      listeners: audit.active(),
      statusNodes: document.querySelectorAll('[role="status"]').length
    };
    let peak = before.elements;
    add.focus();
    for (let index = 0; index < (extra ? 100 : 1); index++) {
      add.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, detail: index % 2 }));
      peak = Math.max(peak, document.querySelectorAll('*').length);
    }
    return { ...value, add, audit, before, peak, candidateElements };
  }

  const exact = boundary(0);
  assert.equal(exact.document.querySelectorAll('#targets .target-row').length, 20);
  assert.equal(exact.document.querySelectorAll('*').length, 1200);
  assert.equal(exact.audit.active(), exact.before.listeners + 2);
  assert.equal(exact.add.disabled, true);
  assert.ok(exact.peak <= 1200);
  exact.app.destroy();

  const plusOne = boundary(1);
  assert.equal(plusOne.before.elements, 1200 - plusOne.candidateElements + 1);
  assert.equal(plusOne.document.querySelectorAll('#targets .target-row').length, 19);
  assert.equal(plusOne.document.querySelectorAll('*').length, plusOne.before.elements, 'DOM-budget refusal commits no nodes');
  assert.equal(plusOne.audit.active(), plusOne.before.listeners, 'DOM-budget refusal commits no listeners');
  assert.equal(plusOne.document.querySelectorAll('[role="status"]').length, plusOne.before.statusNodes);
  assert.equal(plusOne.add.disabled, false, 'a later retained-view removal can make Add admissible');
  assert.equal(plusOne.document.activeElement, plusOne.add);
  assert.match(plusOne.document.querySelector('#request-live').textContent, /문서.*요소.*한도|document.*element.*limit/i);
  assert.ok(plusOne.document.querySelector('#request-live').textContent.length <= 80);
  assert.ok(plusOne.peak <= 1200, `plus-one target peak ${plusOne.peak}`);
  plusOne.app.destroy();
  t.diagnostic(`target DOM exact=${exact.peak}, plus-one=${plusOne.peak}`);
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
  assert.equal(recreated.getState().diagnostics.result.report.id, 'recreated');
  assert.doesNotMatch(dom.window.document.querySelector('#analysis-report').textContent, /recreated/);
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

test('maximum report navigation and 100-row label import stay within the document-wide element budget', async t => {
  const maximum = JSON.parse(await readFile(new URL('../testdata/maximum-analysis-report.json', import.meta.url), 'utf8'));
  const queue = []; let peak = 0;
  const { dom, app, document } = setup(async () => response(JSON.stringify(maximum)), {
    scheduleLabelRender(callback) { queue.push(callback); return callback; }
  });
  const observe = () => { peak = Math.max(peak, document.querySelectorAll('*').length); };
  const drainObserved = () => { while (queue.length) { queue.shift()(); observe(); } };

  await app.start('diagnostics'); observe();
  assert.equal(document.querySelectorAll('*').length, 711, 'maximum valid report baseline including topology view controls');
  document.querySelector('[data-view-link="ip-labels"]').click();
  const imported = JSON.stringify(labelRows(500));
  const input = document.querySelector('#ip-label-import');
  Object.defineProperty(input, 'files', { configurable: true, value: [{ name: 'labels.json', size: imported.length, text: async () => imported }] });
  input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  await flush(); drainObserved();

  assert.equal(document.querySelectorAll('#ip-label-rows tr').length, 100);
  assert.ok(peak <= 1200, `document peak ${peak}`);
  assert.equal(document.querySelector('#analysis-report').childElementCount, 0, 'inactive report is unmounted, not retained hidden');

  document.querySelector('[data-view-link="diagnostics"]').click(); observe();
  assert.equal(document.querySelectorAll('#ip-label-rows tr').length, 0);
  assert.equal(document.querySelectorAll('.finding-toggle').length, 40, 'navigation reconstructs the owned report');
  assert.ok(document.querySelectorAll('*').length <= 711, 'reconstructed report remains no larger than the original mount');
  document.querySelector('[data-view-link="ip-labels"]').click(); drainObserved();
  assert.equal(document.querySelectorAll('#ip-label-rows tr').length, 100, 'navigation reconstructs the current label page');
  assert.ok(peak <= 1200, `document peak after repeated navigation ${peak}`);
  t.diagnostic(`maximum report/label navigation DOM peak=${peak}`);
});

test('label chunk admission accepts an exact 1200-element fit and atomically rejects plus one without partial rows', async t => {
  function addBallast(document, elements) {
    assert.ok(elements >= 1);
    const ballast = document.createElement('aside');
    ballast.append(...Array.from({ length: elements - 1 }, () => document.createElement('i')));
    document.body.append(ballast);
  }
  function boundary(extra) {
    const queue = []; let peak = 0;
    const value = setup(undefined, {
      url: 'https://labels.example/#ip-labels',
      scheduleLabelRender(callback) { queue.push(callback); return callback; },
      configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(20))); }
    });
    const observe = () => { peak = Math.max(peak, value.document.querySelectorAll('*').length); };
    assert.equal(queue.length, 1);
    queue.shift()(); observe();
    assert.equal(value.document.querySelectorAll('#ip-label-rows tr').length, 10);
    const secondChunkElements = 90;
    addBallast(value.document, 1200 - value.document.querySelectorAll('*').length - secondChunkElements + extra);
    observe();
    assert.ok(value.document.querySelectorAll('*').length <= 1200);
    queue.shift()(); observe();
    return { ...value, peak };
  }

  const exact = boundary(0);
  assert.equal(exact.document.querySelectorAll('*').length, 1200);
  assert.equal(exact.document.querySelectorAll('#ip-label-rows tr').length, 20);

  const plusOne = boundary(1);
  assert.ok(plusOne.peak <= 1200, `plus-one peak ${plusOne.peak}`);
  assert.equal(exact.document.querySelector('#ip-labels-view').dataset.state, 'ready');
  assert.equal(plusOne.document.querySelectorAll('#ip-label-rows tr').length, 0, 'the first committed chunk is rolled back');
  assert.equal(plusOne.document.querySelector('#ip-labels-view').dataset.state, 'render-limited');
  assert.equal(plusOne.document.querySelector('#ip-labels-view').getAttribute('aria-busy'), 'false');
  assert.match(plusOne.document.querySelector('#ip-label-message').textContent, /문서.*요소.*한도|render.*limit/i);
  assert.ok(plusOne.peak <= 1200, `plus-one peak ${plusOne.peak}`);
  t.diagnostic(`label chunk exact=${exact.peak}, plus-one=${plusOne.peak}`);
});

test('stale label owners cannot publish across navigation, HMR, or hidden topology and Geo disposal', async () => {
  const labelJobs = []; const topologyJobs = [];
  const options = {
    url: 'https://owners.example/#ip-labels',
    scheduleLabelRender(callback) { labelJobs.push(callback); return callback; },
    cancelLabelRender() { throw new Error('injected label cancellation failure'); },
    scheduler: { schedule(callback) { topologyJobs.push(callback); return callback; }, cancel() {} },
    configureWindow(win) {
      win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(30)));
      win.HTMLCanvasElement.prototype.getContext = () => null;
    }
  };
  const first = setup(async () => response(JSON.stringify(report('owner-topology', {
    results: [compactTraceResult('owner.example')], compact_topology: compactTopology()
  }))), options);
  const staleFirstChunk = labelJobs.shift();
  first.document.querySelector('[data-view-link="topology"]').click();
  await first.app.start('topology'); drain(topologyJobs);
  first.document.querySelector('[data-view-link="geo-map"]').click(); drain(topologyJobs);
  assert.ok(first.document.querySelector('#geo-map-result').childElementCount > 0);
  first.document.querySelector('[data-view-link="ip-labels"]').click();
  assert.equal(first.document.querySelector('#topology-result').childElementCount, 0);
  assert.equal(first.document.querySelector('#geo-map-result').childElementCount, 0);
  assert.ok(labelJobs.length > 0);
  labelJobs.shift()();
  const currentText = first.document.querySelector('#ip-label-rows').textContent;
  staleFirstChunk?.();
  assert.equal(first.document.querySelector('#ip-label-rows').textContent, currentText);
  assert.ok(first.document.querySelectorAll('*').length <= 1200);

  const staleRemaining = [...labelJobs];
  first.app.destroy();
  const second = createApp({
    document: first.document, window: first.dom.window, fetchImpl: async () => response('{}'),
    scheduleLabelRender(callback) { labelJobs.push(callback); return callback; }
  });
  const successor = labelJobs.at(-1); successor();
  const successorText = first.document.querySelector('#ip-label-rows').textContent;
  for (const callback of staleRemaining) callback();
  assert.equal(first.document.querySelector('#ip-label-rows').textContent, successorText);
  assert.notEqual(first.document.querySelector('#ip-labels-view').dataset.state, 'render-limited');
  assert.ok(first.document.querySelectorAll('*').length <= 1200);
  second.destroy();
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
  const { document, app } = setup(() => (++calls === 1 ? a.promise : b.promise));
  submit(document); await flush();
  document.querySelector('#timeout').value = '5001';
  submit(document); await flush();
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'true');
  assert.equal(document.querySelector('#analysis-report').hidden, true);
  a.resolve(response(JSON.stringify(report('A')))); await flush();
  assert.equal(document.querySelector('#analysis-report').textContent.includes('리포트 A'), false);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'true');
  b.resolve(response(JSON.stringify(report('B')))); await flush();
  assert.equal(app.getState().diagnostics.result.report.id, 'B');
  assert.doesNotMatch(document.querySelector('#analysis-report').textContent, /리포트 B/);
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

test('report deadline subtracts setup elapsed since client request start', async () => {
  const times = [1000, 1250];
  let delay;
  const { app } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => {
    reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
  })), {
    clock: () => {
      assert.ok(times.length, 'request setup must not read the clock more than twice');
      return times.shift();
    },
    setTimer: (_callback, milliseconds) => { delay = milliseconds; return 1; }
  });
  const pending = app.start('diagnostics');
  assert.equal(delay, 39750);
  assert.equal(times.length, 0);
  app.cancel('diagnostics');
  await pending;
});

test('clock regression cannot extend the report deadline beyond its total budget', async () => {
  const times = [1000, 750];
  let delay;
  const { app } = setup((_url, init) => new Promise((_, reject) => init.signal.addEventListener('abort', () => {
    reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
  })), {
    clock: () => {
      assert.ok(times.length, 'request setup must not read the clock more than twice');
      return times.shift();
    },
    setTimer: (_callback, milliseconds) => { delay = milliseconds; return 1; }
  });
  const pending = app.start('diagnostics');
  assert.equal(delay, 40000);
  assert.equal(times.length, 0);
  app.cancel('diagnostics');
  await pending;
});

test('deadline reached during setup aborts at the exact boundary without timer or fetch', async () => {
  const times = [1000, 41000]; let timerCalls = 0; let fetchCalls = 0;
  const { app, document } = setup(async () => { fetchCalls++; return response(JSON.stringify(report('stale'))); }, {
    clock: () => {
      assert.ok(times.length, 'expired request setup must read the clock exactly twice');
      return times.shift();
    },
    setTimer: () => { timerCalls++; return 1; }
  });
  await app.start('diagnostics');
  assert.equal(times.length, 0);
  assert.equal(timerCalls, 0);
  assert.equal(fetchCalls, 0);
  assert.equal(app.getState().diagnostics.phase, 'error');
  assert.equal(app.getState().diagnostics.active, null);
  assert.equal(document.querySelector('#diagnostics-workspace').getAttribute('aria-busy'), 'false');
  assert.match(document.querySelector('#request-alert').textContent, /시간|timed out/i);
});

test('replacement and destroy own request-start clocks, timers, and aborts independently', async () => {
  const times = [1000, 1250, 2000, 2500]; const delays = []; const cleared = []; let aborts = 0; let fetches = 0;
  const { app } = setup((_url, init) => {
    fetches++;
    return new Promise((_, reject) => init.signal.addEventListener('abort', () => {
      aborts++;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    }));
  }, {
    clock: () => {
      assert.ok(times.length, 'each request setup must read the clock exactly twice');
      return times.shift();
    },
    setTimer: (_callback, milliseconds) => { delays.push(milliseconds); return delays.length; },
    clearTimer: handle => { cleared.push(handle); }
  });
  const first = app.start('diagnostics');
  const second = app.start('diagnostics');
  assert.deepEqual(delays, [39750, 39500]);
  assert.equal(times.length, 0);
  assert.equal(fetches, 2);
  assert.equal(aborts, 1);
  assert.equal(app.destroy(), true);
  assert.equal(aborts, 2);
  await assert.doesNotReject(Promise.all([first, second]));
  assert.ok(cleared.includes(1));
  assert.ok(cleared.includes(2));
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
  assert.deepEqual([...root.querySelectorAll('[data-analysis-section]')].map(node => node.dataset.analysisSection), ['verdict', 'findings', 'coverage', 'raw']);
  assert.doesNotMatch(root.textContent, /analysis-id|<img src=x|alert\(1\)/);
  assert.deepEqual([...root.querySelectorAll('article [data-semantic-key]')].map(node => node.dataset.semanticKey), ['cause']);
  assert.equal(root.querySelector('img, script, iframe'), null);
  assert.equal(document.activeElement.id, 'analysis-title');
  const toggle = root.querySelector('.finding-toggle');
  toggle.dispatchEvent(new document.defaultView.KeyboardEvent('keydown', { key: ' ', bubbles: true }));
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert.ok(document.getElementById(toggle.getAttribute('aria-controls'))?.isConnected);
  assert.deepEqual([...root.querySelectorAll('article [data-semantic-key]')].map(node => node.dataset.semanticKey), ['cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action']);
  assert.ok(root.querySelector('table caption'));
  assert.ok(root.querySelector('ol input[type="checkbox"] + label'));
});

test('checker execution findings render generic text instead of backend prose', async () => {
  const serialized = await readFile(new URL('../testdata/checker-execution-report.json', import.meta.url), 'utf8');
  const backend = JSON.parse(serialized);
  backend.analysis.findings[0].title = 'private target and panic prose';
  backend.analysis.findings[0].summary = 'Bearer secret-token';
  const { document } = setup(async () => response(JSON.stringify(backend)));
  submit(document); await flush();
  const findings = document.querySelector('[data-analysis-section="findings"]');
  assert.match(findings.textContent, /checker stopped unexpectedly/i);
  assert.match(findings.textContent, /execution capacity was unavailable/i);
  assert.doesNotMatch(findings.textContent, /private target|panic prose|secret-token|Bearer/i);
});

test('Web presentation registry exhaustively matches all producer finding keys with fixed relationships', async () => {
  const contract = JSON.parse(await readFile(new URL('../testdata/finding-contract.json', import.meta.url), 'utf8'));
  const { FINDING_PRESENTATION_REGISTRY, EVIDENCE_SIGNAL_PRESENTATION_REGISTRY, COVERAGE_CODE_PRESENTATION_REGISTRY } = await import('./app.js');
  const expected = contract.findings.map(finding => finding.presentation_key).sort();
  assert.equal(expected.length, 23);
  assert.deepEqual(Object.keys(FINDING_PRESENTATION_REGISTRY).sort(), expected);
  assert.deepEqual(Object.keys(EVIDENCE_SIGNAL_PRESENTATION_REGISTRY).sort(), [
    'error_code', 'http.status_code', 'tls.certificate_expires_at', 'traceroute.attempts_cancelled',
    'traceroute.attempts_execution_failed', 'traceroute.attempts_reached', 'traceroute.attempts_timed_out',
    'traceroute.execution_failures', 'traceroute.path_signatures', 'traceroute.path_status'
  ]);
  assert.deepEqual(Object.keys(COVERAGE_CODE_PRESENTATION_REGISTRY).sort(), ['malformed_details', 'missing_details', 'unsupported_details']);
  for (const key of expected) {
    const entry = FINDING_PRESENTATION_REGISTRY[key];
    assert.equal(entry.key, key);
    assert.equal(entry.actionRelationship, `action.${key.slice('finding.'.length)}`);
    assert.deepEqual(Object.keys(entry).sort(), ['action', 'actionRelationship', 'cause', 'expectation', 'key', 'limitation', 'signals'].sort());
    assert.ok(entry.signals.length > 0, key);
  }
});

test('all 21 coverage issues combine their fixed private label and category with fixed code text', async () => {
  const contract = JSON.parse(await readFile(new URL('../testdata/presentation-contract.json', import.meta.url), 'utf8'));
  const base = contract.scenarios.find(scenario => scenario.name === 'finding.dns_resolution_failed').report;
  const { COVERAGE_SIGNAL_PRESENTATION_REGISTRY, COVERAGE_CODE_PRESENTATION_REGISTRY, presentReport } = await import('./app.js');
  const expectedSignals = {
    certificate_expires_at: ['Certificate expiry timestamp', 'tls'], details: ['Checker details', 'result'],
    dns_answers: ['DNS answer facts', 'dns'], endpoint: ['Connected endpoint facts', 'connectivity'],
    error_code: ['Result error code', 'result'], geoip: ['GeoIP result facts', 'geoip'],
    geoip_enrichment: ['GeoIP enrichment facts', 'geoip'], http_status: ['HTTP status facts', 'http'],
    kind: ['Result kind', 'result'], response_body: ['Response body facts', 'http'],
    result: ['Result observation', 'result'], service_verification_details: ['Service verification details', 'service'],
    service_verification_scope: ['Service verification scope', 'service'], status: ['Result status', 'result'],
    tls: ['TLS handshake facts', 'tls'], tls_certificate: ['TLS certificate facts', 'tls'],
    tls_failure_details: ['TLS failure details', 'tls'], trace_attempts: ['Traceroute attempt counters', 'traceroute'],
    trace_error: ['Traceroute error facts', 'traceroute'], trace_paths: ['Completed traceroute path facts', 'traceroute'],
    trace_topology: ['Traceroute topology facts', 'traceroute']
  };
  assert.deepEqual(Object.keys(expectedSignals).sort(), [...contract.coverage_signals].sort());
  for (const [signal, [label, category]] of Object.entries(expectedSignals)) {
    const value = structuredClone(base);
    const reason = `PRIVATE-REASON-${signal}`;
    value.analysis.coverage.available = [];
    value.analysis.coverage.missing = [];
    value.analysis.coverage.provider_failures = [];
    value.analysis.coverage.limitations = Object.keys(COVERAGE_CODE_PRESENTATION_REGISTRY).map(code => ({ code, result_index: 0, kind: 'dns', signal, reason }));
    const { dom, document } = setup();
    const markdown = presentReport(document, value);
    assert.equal(COVERAGE_SIGNAL_PRESENTATION_REGISTRY[signal].label, label, signal);
    assert.equal(COVERAGE_SIGNAL_PRESENTATION_REGISTRY[signal].category, category, signal);
    for (const codeText of Object.values(COVERAGE_CODE_PRESENTATION_REGISTRY)) {
      const fact = `${label} (${category}). ${codeText}`;
      assert.match(document.querySelector('[data-analysis-section="coverage"]').textContent, new RegExp(fact.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `${signal}: ${codeText}`);
      assert.match(markdown, new RegExp(fact.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `${signal}: ${codeText}`);
    }
    assert.doesNotMatch(document.body.textContent, /PRIVATE-REASON/, signal);
    assert.doesNotMatch(markdown, /PRIVATE-REASON/, signal);
    dom.window.close();
  }
});

test('maximum producer report lazily owns one bounded finding panel and rejects an oversized atomic commit', async t => {
  const maximum = JSON.parse(await readFile(new URL('../testdata/maximum-analysis-report.json', import.meta.url), 'utf8'));
  const poison = 'MAXIMUM_PRIVATE_PROSE_CANARY';
  for (const resultValue of maximum.results) resultValue.message = poison;
  for (const finding of maximum.analysis.findings) { finding.title = poison; finding.summary = poison; }
  for (const evidence of maximum.analysis.evidence) evidence.address = poison;
  for (const action of maximum.analysis.actions) {
    action.title = poison; action.step = poison; action.expected_result = poison; action.escalation_condition = poison;
  }
  for (const issue of [...maximum.analysis.coverage.provider_failures, ...maximum.analysis.coverage.limitations]) issue.reason = poison;
  const { dom, document, app } = setup(async () => response(JSON.stringify(maximum)));
  await app.start('diagnostics');
  const root = document.querySelector('#analysis-report');
  const toggles = [...root.querySelectorAll('.finding-toggle')];
  assert.equal(toggles.length, 40);
  assert.equal(root.querySelectorAll('.finding-panel').length, 0);
  assert.equal(root.querySelectorAll('[data-semantic-key="cause"]').length, 40);
  assert.equal(root.querySelectorAll('[data-semantic-key]:not([data-semantic-key="cause"])').length, 0);
  const initialElements = document.querySelectorAll('*').length;
  let peakElements = initialElements;
  assert.ok(initialElements <= 1200, `initial document elements: ${initialElements}`);
  let previousPanel;
  for (const [index, toggle] of toggles.entries()) {
    toggle.focus();
    toggle.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: index % 2 ? 'Enter' : ' ', bubbles: true }));
    const panel = document.getElementById(toggle.getAttribute('aria-controls'));
    assert.ok(panel?.isConnected, `panel ${index}`);
    assert.equal(document.activeElement, toggle, `focus ${index}`);
    assert.equal(root.querySelectorAll('.finding-panel').length, 1, `owned panel ${index}`);
    assert.deepEqual([...toggle.closest('article').querySelectorAll('[data-semantic-key]')].map(node => node.dataset.semanticKey),
      ['cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action'], `keys ${index}`);
    if (previousPanel) assert.equal(previousPanel.isConnected, false, `prior panel ${index}`);
    const elementCount = document.querySelectorAll('*').length;
    peakElements = Math.max(peakElements, elementCount);
    assert.ok(elementCount <= 1200, `document elements ${index}: ${elementCount}`);
    const visible = document.querySelector('#diagnostics-view').cloneNode(true);
    visible.querySelector('[data-analysis-section="raw"]')?.remove();
    visible.querySelector('.secondary-results')?.remove();
    assert.doesNotMatch(visible.textContent, new RegExp(poison), `producer prose ${index}`);
    for (const item of maximum.results) assert.doesNotMatch(visible.textContent, new RegExp(item.address.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i'), `raw target ${index}`);
    previousPanel = panel;
  }
  toggles.at(-1).click();
  assert.equal(root.querySelectorAll('.finding-panel').length, 0);

  toggles[1].focus();
  for (const [key, expected] of [['ArrowDown', toggles[2]], ['ArrowUp', toggles[1]], ['End', toggles.at(-1)], ['Home', toggles[0]]]) {
    document.activeElement.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key, bubbles: true }));
    assert.equal(document.activeElement, expected, key);
  }

  let markdown = '';
  dom.window.Blob = class { constructor(parts) { markdown = parts.join(''); } };
  dom.window.URL.createObjectURL = () => 'blob:human'; dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => {};
  document.querySelector('#download-human').click();
  for (const key of ['cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action']) {
    assert.equal(markdown.match(new RegExp(`^### ${key} \\[finding\\.`, 'gm'))?.length, 40, key);
  }
  for (const item of maximum.results) assert.doesNotMatch(markdown, new RegExp(item.address.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i'));
  assert.doesNotMatch(markdown, new RegExp(poison));
  assert.equal([...root.querySelectorAll('[data-analysis-section]')].at(-1).dataset.analysisSection, 'raw');
  assert.equal(root.querySelector('[data-analysis-section="raw"] details').open, false);

  const stale = toggles[0];
  await app.start('diagnostics');
  stale.click();
  assert.equal(root.querySelectorAll('.finding-panel').length, 0);

  const { document: guarded } = setup();
  guarded.querySelector('#summary').append(Object.assign(guarded.createElement('strong'), { textContent: 'old summary' }));
  guarded.querySelector('#results').append(Object.assign(guarded.createElement('strong'), { textContent: 'old results' }));
  guarded.querySelector('#analysis-report').append(Object.assign(guarded.createElement('strong'), { textContent: 'old analysis' }));
  const ballast = guarded.createElement('div');
  for (let index = 0; index < 1100; index++) ballast.append(guarded.createElement('i'));
  guarded.body.append(ballast);
  const { presentReport } = await import('./app.js');
  assert.throws(() => presentReport(guarded, maximum), error => error.code === 'unsupported_presentation_contract');
  assert.equal(guarded.querySelector('#summary').textContent, 'old summary');
  assert.equal(guarded.querySelector('#results').textContent, 'old results');
  assert.equal(guarded.querySelector('#analysis-report').textContent, 'old analysis');

  const guardedRuntime = setup(async () => response(JSON.stringify(maximum)), {
    configureWindow(win) {
      const extra = win.document.createElement('div');
      for (let index = 0; index < 1200; index++) extra.append(win.document.createElement('i'));
      win.document.body.append(extra);
    }
  });
  await guardedRuntime.app.start('diagnostics');
  assert.equal(guardedRuntime.app.getState().diagnostics.phase, 'error');
  assert.equal(guardedRuntime.document.querySelector('#analysis-report').hidden, true);
  assert.match(guardedRuntime.document.querySelector('#request-alert').textContent, /cannot be presented safely/i);
  assert.doesNotMatch(guardedRuntime.document.body.textContent, new RegExp(poison));
  t.diagnostic(`maximum report DOM elements: initial=${initialElements}, peak=${peakElements}, findings=${toggles.length}`);
});

test('all 23 finding families render and export only six stable local semantic sections with safe facts', async () => {
  const contract = JSON.parse(await readFile(new URL('../testdata/finding-contract.json', import.meta.url), 'utf8'));
  const { FINDING_PRESENTATION_REGISTRY } = await import('./app.js');
  const poison = {
    report: 'POISON_REPORT_ID', findingID: 'POISON_FINDING_ID', title: 'POISON_TITLE', summary: 'POISON_SUMMARY',
    evidenceID: 'POISON_EVIDENCE_ID', observed: 'POISON_ERROR_PANIC_DETAILS', expected: 'POISON_EXPECTATION',
    actionID: 'POISON_ACTION_ID', action: 'POISON_ACTION_PROSE', target: 'POISON_TARGET_URL_HOST_IP_TOKEN',
    reason: 'POISON_PROVIDER_REASON'
  };
  const findings = contract.findings.map((entry, index) => ({
    id: `${poison.findingID}_${index}`, code: entry.code, severity: 'warning', category: 'execution',
    title: `${poison.title}_${index}`, summary: `${poison.summary}_${index}`, confidence: 'direct',
    evidence_ids: [`${poison.evidenceID}_${index}`], action_ids: [`${poison.actionID}_${index}`]
  }));
  const safeEvidence = signal => ({
    error_code: ['connection_failed', ''],
    'http.status_code': ['503', '200'],
    'tls.certificate_expires_at': ['2026-09-09T00:00:00Z', ''],
    'traceroute.attempts_cancelled': ['1', '0'],
    'traceroute.attempts_execution_failed': ['1', '0'],
    'traceroute.attempts_reached': ['1/2 completed attempts reached', '2/2 completed attempts reached'],
    'traceroute.attempts_timed_out': ['1', '0'],
    'traceroute.execution_failures': ['2 execution failures: 1 timed out, 1 cancelled, 0 command errors', '0'],
    'traceroute.path_signatures': ['multiple successful completed path signatures among 2 reached completed attempts', ''],
    'traceroute.path_status': ['degraded segment observed among 1 completed attempt', '']
  })[signal];
  const evidence = contract.findings.map((entry, index) => {
    const signal = FINDING_PRESENTATION_REGISTRY[entry.presentation_key].signals[0];
    const [observed, expected] = safeEvidence(signal);
    return {
      id: `${poison.evidenceID}_${index}`, result_index: 0, kind: 'dns', address: poison.target,
      signal, observed, expected, provenance: 'result'
    };
  });
  const actions = contract.findings.map((entry, index) => ({
    id: `${poison.actionID}_${index}`, title: poison.action, step: poison.action,
    expected_result: poison.action, escalation_condition: poison.action
  }));
  const analysis = {
    verdict: 'attention', findings, evidence, actions,
    coverage: {
      available: ['results[0].status'], missing: ['results[0].details'], enrichment: [], provider_failures: [],
      limitations: [{ code: 'missing_details', result_index: 0, kind: 'dns', signal: 'details', reason: poison.reason }]
    }
  };
  let exported = '';
  const { dom, document } = setup(async () => response(JSON.stringify(report(poison.report, {
    status: 'unreachable', summary: { total: 1, passed: 0, failed: 1 },
    results: [result({ address: poison.target, status: 'unreachable', error_code: 'connection_failed', message: poison.observed, details: { poison: poison.observed } })], analysis
  }))));
  dom.window.Blob = class { constructor(parts) { exported = parts.join(''); } };
  dom.window.URL.createObjectURL = () => 'blob:human'; dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => {};
  submit(document); await flush();

  const semanticKeys = ['cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action'];
  const articles = [...document.querySelectorAll('[data-analysis-section="findings"] article')];
  assert.equal(articles.length, 23);
  for (const article of articles) {
    article.querySelector('.finding-toggle').click();
    assert.deepEqual([...article.querySelectorAll('[data-semantic-key]')].map(node => node.dataset.semanticKey), semanticKeys);
  }
  const visible = document.querySelector('#diagnostics-view').cloneNode(true);
  visible.querySelector('[data-analysis-section="raw"]')?.remove();
  visible.querySelector('.secondary-results')?.remove();
  const visibleText = visible.textContent;
  for (const value of Object.values(poison)) assert.doesNotMatch(visibleText, new RegExp(value, 'i'), value);

  document.querySelector('#download-human').click();
  for (const key of semanticKeys) assert.equal(exported.match(new RegExp(`^### ${key} \\[finding\\.`, 'gm'))?.length, 23, key);
  assert.match(exported, /Observed code: connection_failed/);
  assert.match(exported, /Result status was available/);
  assert.match(exported, /Checker details were not available/);
  assert.match(exported, /Required checker details were not observed/);
  for (const value of Object.values(poison)) assert.doesNotMatch(exported, new RegExp(value, 'i'), value);
});

test('all eight valid greeting then close service kinds retain exact bounded meaning on screen and in Markdown', async () => {
  const serviceKinds = ['imap', 'imaps', 'pop3', 'pop3s', 'smtp', 'smtps', 'ssh', 'submission'];
  const meaning = 'Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.';
  for (const kind of serviceKinds) {
    let exported = '';
    const serviceResult = result({ kind, address: `POISON_${kind}_ENDPOINT`, details: { verification_scope: 'server_greeting' } });
    const analysis = {
      verdict: 'healthy', findings: [], evidence: [], actions: [],
      coverage: {
        available: ['results[0].status', 'results[0].details.verification_scope'], missing: [], enrichment: [], provider_failures: [],
        limitations: [{ code: 'unsupported_details', result_index: 0, kind, signal: 'service_verification_scope', reason: 'POISON_SERVICE_REASON' }]
      }
    };
    const { dom, document } = setup(async () => response(JSON.stringify(report(`POISON_${kind}_ID`, { results: [serviceResult], analysis }))));
    dom.window.Blob = class { constructor(parts) { exported = parts.join(''); } };
    dom.window.URL.createObjectURL = () => 'blob:human'; dom.window.URL.revokeObjectURL = () => {};
    dom.window.HTMLAnchorElement.prototype.click = () => {};
    submit(document); await flush();
    const visible = document.querySelector('#diagnostics-view').cloneNode(true);
    visible.querySelector('[data-analysis-section="raw"]')?.remove(); visible.querySelector('.secondary-results')?.remove();
    assert.match(visible.textContent, new RegExp(meaning.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), kind);
    assert.doesNotMatch(visible.textContent, /\bPASS\b|\bHealthy\b|response is normal|응답이 정상|POISON_/i, kind);
    document.querySelector('#download-human').click();
    assert.match(exported, new RegExp(meaning.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), kind);
    assert.doesNotMatch(exported, /\bPASS\b|\bHealthy\b|response is normal|응답이 정상|POISON_/i, kind);
    dom.window.close();
  }
});

test('producer presentation fixture is consumed exactly across all 33 DOM and Markdown scenarios', async t => {
  const contract = JSON.parse(await readFile(new URL('../testdata/presentation-contract.json', import.meta.url), 'utf8'));
  const {
    PRESENTATION_SEMANTIC_KEYS, FINDING_PRESENTATION_REGISTRY,
    EVIDENCE_SIGNAL_PRESENTATION_REGISTRY, COVERAGE_SIGNAL_PRESENTATION_REGISTRY, presentReport
  } = await import('./app.js');
  const greetingScope = 'Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.';
  const forbiddenOverclaim = /\bPASS\b|\bHealthy\b|response is normal|응답이 정상/i;

  assert.equal(contract.schema, 'presentation-contract-v1');
  assert.deepEqual(contract.semantic_keys, [...PRESENTATION_SEMANTIC_KEYS]);
  assert.deepEqual(Object.keys(contract).sort(), ['action_relationships', 'coverage_signals', 'evidence_signals', 'scenarios', 'schema', 'semantic_keys']);
  assert.equal(contract.semantic_keys.length, 6);
  assert.equal(contract.action_relationships.length, 23);
  assert.equal(contract.evidence_signals.length, 10);
  assert.equal(contract.coverage_signals.length, 21);
  assert.equal(contract.scenarios.length, 33);
  assert.deepEqual(Object.keys(FINDING_PRESENTATION_REGISTRY).sort(), contract.action_relationships.map(item => item.finding_presentation_key).sort());
  assert.deepEqual(Object.keys(EVIDENCE_SIGNAL_PRESENTATION_REGISTRY).sort(), contract.evidence_signals.map(item => item.signal).sort());
  assert.deepEqual(Object.keys(COVERAGE_SIGNAL_PRESENTATION_REGISTRY).sort(), [...contract.coverage_signals].sort());
  for (const fixture of contract.evidence_signals) {
    const local = EVIDENCE_SIGNAL_PRESENTATION_REGISTRY[fixture.signal];
    assert.deepEqual(
      { observed_shape: local.observedShape, expected_shape: local.expectedShape },
      { observed_shape: fixture.observed_shape, expected_shape: fixture.expected_shape },
      fixture.signal
    );
    assert.equal(typeof local.label, 'string', fixture.signal);
    assert.equal(typeof local.category, 'string', fixture.signal);
  }
  for (const signal of contract.coverage_signals) {
    assert.equal(typeof COVERAGE_SIGNAL_PRESENTATION_REGISTRY[signal].label, 'string', signal);
    assert.equal(typeof COVERAGE_SIGNAL_PRESENTATION_REGISTRY[signal].category, 'string', signal);
  }
  for (const relationship of contract.action_relationships) {
    assert.equal(FINDING_PRESENTATION_REGISTRY[relationship.finding_presentation_key].actionRelationship, relationship.key);
  }

  for (const scenario of contract.scenarios) await t.test(scenario.name, async () => {
    const { dom, document } = setup();
    const markdown = presentReport(document, structuredClone(scenario.report));

    const root = document.querySelector('#analysis-report');
    const articles = [...root.querySelectorAll('[data-analysis-section="findings"] article')];
    assert.equal(articles.length, scenario.findings.length, scenario.name);
    for (const article of articles) {
      assert.deepEqual([...article.querySelectorAll('[data-semantic-key]')].map(node => node.dataset.semanticKey), ['cause'], scenario.name);
      article.querySelector('.finding-toggle').click();
    }
    const visible = document.querySelector('#diagnostics-view').cloneNode(true);
    visible.querySelector('[data-analysis-section="raw"] pre')?.remove();
    visible.querySelector('.secondary-results')?.remove();
    const visibleText = visible.textContent;
    const status = document.querySelector('#summary .status');
    const verdict = root.querySelector('.analysis-verdict');
    assert.ok(status.classList.contains(scenario.report.status), scenario.name);
    assert.ok(verdict.classList.contains(`verdict-${scenario.report.analysis.verdict}`), scenario.name);
    assert.match(markdown, /^Report status: /m, scenario.name);
    assert.match(markdown, /^Analysis verdict: /m, scenario.name);
    assert.doesNotMatch(visibleText, forbiddenOverclaim, scenario.name);
    assert.doesNotMatch(markdown, forbiddenOverclaim, scenario.name);
    assert.ok(document.querySelectorAll('*').length <= 1200, scenario.name);
    assert.equal(document.activeElement, document.querySelector('#analysis-title'), scenario.name);
    assert.match(root.querySelector('[data-analysis-section="raw"]').textContent, /raw.*contain.*target|원시.*대상/i, scenario.name);

    assert.equal(scenario.report.analysis.findings.length, scenario.findings.length, scenario.name);
    for (let index = 0; index < scenario.findings.length; index++) {
      const fixture = scenario.findings[index];
      const finding = scenario.report.analysis.findings[index];
      assert.equal(finding.code, fixture.code, scenario.name);
      assert.equal(articles[index].dataset.presentationKey, fixture.presentation_key, scenario.name);
      if (articles[index].querySelectorAll('[data-semantic-key]').length === 1) {
        articles[index].querySelector('.finding-toggle').click();
      }
      assert.deepEqual([...articles[index].querySelectorAll('[data-semantic-key]')].map(node => node.dataset.semanticKey), contract.semantic_keys, scenario.name);
      assert.equal(FINDING_PRESENTATION_REGISTRY[fixture.presentation_key].actionRelationship, fixture.action_relationship, scenario.name);
      const linked = finding.evidence_ids.map(id => scenario.report.analysis.evidence.find(item => item.id === id));
      assert.deepEqual(linked.map(item => item.signal), fixture.evidence.map(item => item.signal), scenario.name);
      for (let evidenceIndex = 0; evidenceIndex < fixture.evidence.length; evidenceIndex++) {
        const expected = fixture.evidence[evidenceIndex];
        const registry = EVIDENCE_SIGNAL_PRESENTATION_REGISTRY[expected.signal];
        assert.equal(registry.observedShape, expected.observed_shape, scenario.name);
        assert.equal(registry.expectedShape, expected.expected_shape, scenario.name);
      }
    }

    if (scenario.name === 'control.dns_only_healthy') {
      assert.doesNotMatch(visibleText, /greeting/i);
      assert.doesNotMatch(markdown, /greeting/i);
      assert.match(visibleText, /observed checks/i);
    }
    if (scenario.name.startsWith('control.greeting_healthy.')) {
      assert.equal(visibleText.match(new RegExp(greetingScope, 'g'))?.length, 1, scenario.name);
      assert.equal(document.body.textContent.match(new RegExp(greetingScope, 'g'))?.length, 2, scenario.name);
      assert.equal(markdown.match(new RegExp(greetingScope, 'g'))?.length, 2, scenario.name);
    }
    if (scenario.name === 'control.mixed_greeting_healthy_dns_failed') {
      assert.match(status.textContent, /degraded|requires attention/i);
      assert.match(verdict.textContent, /require attention/i);
      assert.equal(visibleText.match(new RegExp(greetingScope, 'g'))?.length, 1);
      assert.equal(document.body.textContent.match(new RegExp(greetingScope, 'g'))?.length, 2);
      assert.equal(markdown.match(new RegExp(greetingScope, 'g'))?.length, 2);
      assert.doesNotMatch(status.textContent, /greeting/i);
      assert.doesNotMatch(verdict.textContent, /greeting/i);
    }
    if (scenario.name === 'finding.http_unexpected_status') {
      assert.match(visibleText, /Expected HTTP status: 200\./);
      assert.match(visibleText, /Observed HTTP status: 503\./);
      assert.match(markdown, /Expected HTTP status: 200\./);
      assert.match(markdown, /Observed HTTP status: 503\./);
    }
    if (scenario.name === 'finding.traceroute_unavailable') {
      for (const rendered of [visibleText, markdown]) {
        assert.match(rendered, /The bounded observations were insufficient for a conclusion\./);
        assert.match(rendered, /Traceroute capability was unavailable, so no route observation was established\./);
        assert.match(rendered, /Observed code: traceroute_unavailable\./);
        assert.match(rendered, /Restore the supported traceroute capability, then repeat the bounded check\./);
        assert.doesNotMatch(rendered, /unavailable\.example\.test|traceroute is unavailable|Install or repair the supported traceroute executable|startup capability probe/i);
      }
    }
    dom.window.close();
  });
});

test('unknown presentation signals fail closed without fallback or reflection', async t => {
  const contract = JSON.parse(await readFile(new URL('../testdata/presentation-contract.json', import.meta.url), 'utf8'));
  const base = contract.scenarios.find(scenario => scenario.name === 'finding.http_unexpected_status').report;
  for (const mutation of ['evidence', 'coverage-path', 'coverage-issue']) await t.test(mutation, async () => {
    const hostile = structuredClone(base);
    const canary = `HOSTILE_${mutation.toUpperCase().replace('-', '_')}_SIGNAL`;
    if (mutation === 'evidence') hostile.analysis.evidence[0].signal = canary;
    if (mutation === 'coverage-path') hostile.analysis.coverage.available[0] = `results[0].details.${canary}`;
    if (mutation === 'coverage-issue') hostile.analysis.coverage.limitations.push({ code: 'unsupported_details', result_index: 0, kind: 'http', signal: canary, reason: canary });
    const { app, document } = setup(async () => response(JSON.stringify(hostile)));
    await app.start('diagnostics');
    assert.equal(app.getState().diagnostics.phase, 'error', mutation);
    assert.equal(document.querySelector('#analysis-report').hidden, true, mutation);
    assert.equal(document.querySelector('#download-human').disabled, true, mutation);
    assert.equal(document.activeElement, document.querySelector('#request-alert'), mutation);
    assert.match(document.querySelector('#request-alert').textContent, /cannot be presented safely|표시할 수 없/i, mutation);
    assert.doesNotMatch(document.body.textContent, new RegExp(canary, 'i'), mutation);
  });
});

test('legacy report is ready but explicitly unsupported/inconclusive', async () => {
  const { document } = setup(); submit(document); await flush();
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'ready');
  assert.match(document.querySelector('#analysis-report').textContent, /Automated analysis unavailable|insufficient for a conclusion/);
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

test('untyped HTTP 401 uses the invalid-response alert without opening credential settings', async () => {
  const { document } = setup(async () => response('<html>secret proxy</html>', 401));
  submit(document); await flush();
  assert.equal(document.querySelector('#connection-settings').open, false);
  assert.equal(document.activeElement, document.querySelector('#request-alert'));
  assert.doesNotMatch(document.body.textContent, /secret proxy/);
  assert.equal(document.querySelector('#diagnostics-workspace').dataset.state, 'error');
});

test('every exact producer API error fixture renders only local text with classification-owned focus', async () => {
  const contract = JSON.parse(await readFile(new URL('../testdata/api-error-contract.json', import.meta.url), 'utf8'));
  assert.equal(contract.errors.length, 16);
  for (const fixture of contract.errors) {
    const { dom, app, document } = setup(async () => response(
      fixture.body, fixture.status,
      { 'Retry-After': '30' }
    ));
    submit(document); await flush();
    const error = app.getState().diagnostics.error;
    assert.equal(error.code, fixture.code, fixture.key);
    assert.equal(error.message, fixture.message, fixture.key);
    assert.equal(error.retryable, fixture.retryable, fixture.key);
    assert.doesNotMatch(document.body.textContent, /HOSTILE-SERVER-PROSE/, fixture.key);
    assert.match(document.querySelector('#request-alert').textContent, new RegExp(fixture.message.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    assert.equal(document.querySelector('#request-alert').hidden, false, fixture.key);
    assert.equal(
      document.activeElement,
      fixture.code === 'unauthorized' ? document.querySelector('#bearer-token') : document.querySelector('#request-alert'),
      fixture.key
    );
    dom.window.close();
  }
});

test('unknown 401 code cannot trigger credential focus or reflect code, prose, or Retry-After', async () => {
  const canary = 'HOSTILE-UNKNOWN-CODE-AND-PROSE';
  const { app, document } = setup(async () => response(
    JSON.stringify({ error: { code: canary, message: canary } }), 401, { 'Retry-After': '30' }
  ));
  submit(document); await flush();
  const error = app.getState().diagnostics.error;
  assert.deepEqual(error, {
    kind: 'invalid-response', code: 'invalid_server_response', status: 401,
    message: 'The server returned an invalid error response.', retryable: false
  });
  assert.equal(document.activeElement, document.querySelector('#request-alert'));
  assert.equal(document.querySelector('#connection-settings').open, false);
  assert.doesNotMatch(document.body.textContent, /HOSTILE|Retry-After/);
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
    results: [compactTraceResult('one.example')], compact_topology: compactTopology()
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

test('topology native view controls default to a visible accessible 2d graph and switch to 3d without fetching or rebuilding', async () => {
  const jobs = []; const cancelled = new Set(); let handle = 0; let fetches = 0;
  const context = { calls: [] };
  for (const name of ['setTransform', 'clearRect', 'fillRect', 'beginPath', 'moveTo', 'lineTo', 'stroke', 'closePath', 'fill', 'arc', 'fillText']) context[name] = (...args) => context.calls.push([name, ...args]);
  const value = report('interactive-topology', { results: [compactTraceResult('one.example')], compact_topology: compactTopology() });
  const { dom, app, document } = setup(async () => { fetches++; return response(JSON.stringify(value)); }, {
    url: 'https://views.example/#topology',
    scheduler: { schedule(callback) { const id = ++handle; jobs.push({ id, callback }); return id; }, cancel(id) { cancelled.add(id); } },
    configureWindow(win) {
      Object.defineProperty(win, 'devicePixelRatio', { configurable: true, value: 2 });
      win.CanvasRenderingContext2D = function CanvasRenderingContext2D() {};
      win.HTMLCanvasElement.prototype.getContext = () => context;
    }
  });
  const drainDraws = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.id)) job.callback(); } };

  const controls = document.querySelector('#topology-view-controls');
  assert.equal(controls.tagName, 'FIELDSET');
  assert.equal(document.querySelector('[name="topology-view-mode"][value="2d"]').checked, true);
  assert.equal(document.querySelector('[name="topology-view-mode"][value="3d"]').checked, false);
  assert.equal(document.querySelector('#topology-view-reset').tagName, 'BUTTON');
  assert.ok(document.querySelector('#topology-view-help').textContent.length <= 140);

  await app.start('topology'); drainDraws();
  const canvas = document.querySelector('#topology-result canvas.topology-canvas');
  assert.ok(canvas, 'default result contains a visual graph');
  assert.equal(canvas.dataset.mode, '2d');
  assert.equal(canvas.dataset.drawState, 'rendered');
  assert.ok(context.calls.some(call => call[0] === 'arc'), 'nodes are visibly drawn');
  assert.ok(context.calls.some(call => call[0] === 'lineTo'), 'directed links are visibly drawn');
  assert.equal(canvas.tabIndex, 0);
  assert.equal(canvas.getAttribute('role'), 'img');
  assert.equal(canvas.hasAttribute('aria-hidden'), false);
  assert.equal(canvas.getAttribute('aria-describedby'), 'topology-view-help topology-render-status');
  const canvasBefore = canvas; const nodesBefore = [...document.querySelectorAll('#topology-result .topology-node')]; const countBefore = document.querySelectorAll('*').length;

  canvas.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
  const threeD = document.querySelector('[name="topology-view-mode"][value="3d"]');
  context.calls.length = 0;
  threeD.checked = true;
  threeD.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const modeArcs = context.calls.filter(call => call[0] === 'arc');
  context.calls.length = 0;
  document.querySelector('#topology-view-reset').click();
  assert.deepEqual(modeArcs, context.calls.filter(call => call[0] === 'arc'), 'mode switch restores fit instead of inheriting pan');
  assert.equal(fetches, 1);
  assert.strictEqual(document.querySelector('#topology-result canvas'), canvasBefore);
  assert.deepEqual([...document.querySelectorAll('#topology-result .topology-node')], nodesBefore);
  assert.equal(document.querySelectorAll('*').length, countBefore);
  assert.equal(canvas.dataset.mode, '3d');
  assert.match(document.querySelector('#topology-view-status').textContent, /3D/);

  let filter = document.querySelector('[data-target-index="0"]');
  filter.checked = false;
  filter.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  drainDraws();
  assert.equal(fetches, 1);
  filter = document.querySelector('[data-target-index="0"]');
  filter.checked = true;
  filter.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  drainDraws();
  assert.equal(document.querySelector('#topology-result canvas').dataset.mode, '3d', 'filter redraw preserves mode');
  document.querySelector('#topology-label-address').value = '192.0.2.1';
  document.querySelector('#topology-label-name').value = 'Edge';
  document.querySelector('#topology-label-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  drainDraws();
  assert.equal(document.querySelector('#topology-result canvas').dataset.mode, '3d', 'label redraw preserves mode');
  assert.equal(fetches, 1);
  assert.ok(document.querySelectorAll('*').length <= 1200);
  app.destroy();
});

test('topology canvas pointer wheel keyboard reset resize and fullscreen redraw are bounded and stale owners become inert', async () => {
  const jobs = []; const cancelled = new Set(); let handle = 0; let resizeCallback; let disconnects = 0;
  const context = { calls: [] };
  for (const name of ['setTransform', 'clearRect', 'fillRect', 'beginPath', 'moveTo', 'lineTo', 'stroke', 'closePath', 'fill', 'arc', 'fillText']) context[name] = (...args) => context.calls.push([name, ...args]);
  const scheduler = { schedule(callback) { const id = ++handle; jobs.push({ id, callback }); return id; }, cancel(id) { cancelled.add(id); } };
  const value = report('interactions', { results: [compactTraceResult('one.example')], compact_topology: compactTopology() });
  const { dom, app, document } = setup(async () => response(JSON.stringify(value)), {
    url: 'https://interactions.example/#topology', scheduler,
    configureWindow(win) {
      Object.defineProperty(win, 'devicePixelRatio', { configurable: true, value: 2 });
      win.CanvasRenderingContext2D = function CanvasRenderingContext2D() {};
      win.HTMLCanvasElement.prototype.getContext = () => context;
      win.ResizeObserver = class { constructor(callback) { resizeCallback = callback; } observe() {} disconnect() { disconnects++; } };
    }
  });
  const runAll = () => { while (jobs.length) { const job = jobs.shift(); if (!cancelled.has(job.id)) job.callback(); } };
  const root = document.querySelector('#topology-result');
  root.getBoundingClientRect = () => ({ width: 375, height: 250, top: 0, left: 0, right: 375, bottom: 250 });
  await app.start('topology'); runAll();
  const canvas = root.querySelector('canvas');
  canvas.getBoundingClientRect = () => ({ width: 375, height: 240, top: 0, left: 0, right: 375, bottom: 240 });
  canvas.setPointerCapture = () => {};
  resizeCallback?.([{ target: root }]); runAll();
  assert.equal(canvas.width, 750); assert.equal(canvas.height, 480); assert.equal(canvas.dataset.dpr, '2');

  const calls = () => context.calls.length;
  let before = calls();
  canvas.dispatchEvent(new dom.window.MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
  const move = new dom.window.MouseEvent('pointermove', { bubbles: true, clientX: 35, clientY: 20, cancelable: true }); canvas.dispatchEvent(move);
  canvas.dispatchEvent(new dom.window.MouseEvent('pointerup', { bubbles: true, button: 0, clientX: 35, clientY: 20 }));
  assert.equal(move.defaultPrevented, true); assert.ok(calls() > before); assert.equal(canvas.dataset.mode, '2d');

  before = calls();
  const wheel = new dom.window.WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -100 }); canvas.dispatchEvent(wheel);
  assert.equal(wheel.defaultPrevented, true); assert.ok(calls() > before);
  for (const key of ['ArrowRight', '+', 'Home']) {
    before = calls(); const event = new dom.window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }); canvas.dispatchEvent(event);
    assert.equal(event.defaultPrevented, true, key); assert.ok(calls() > before, key);
  }
  const unhandled = new dom.window.KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }); canvas.dispatchEvent(unhandled);
  assert.equal(unhandled.defaultPrevented, false);

  document.querySelector('[name="topology-view-mode"][value="3d"]').checked = true;
  document.querySelector('[name="topology-view-mode"][value="3d"]').dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  before = calls();
  canvas.dispatchEvent(new dom.window.MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
  canvas.dispatchEvent(new dom.window.MouseEvent('pointermove', { bubbles: true, clientX: 30, clientY: 30, cancelable: true }));
  assert.ok(calls() > before); assert.equal(canvas.dataset.mode, '3d');
  document.querySelector('#topology-view-reset').click();
  assert.match(document.querySelector('#topology-view-status').textContent, /초기화|3D/);

  before = calls();
  Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: root });
  document.dispatchEvent(new dom.window.Event('fullscreenchange')); runAll();
  assert.ok(calls() > before, 'fullscreen schedules a measured redraw');
  const staleResize = resizeCallback;
  document.querySelector('[data-view-link="diagnostics"]').click();
  runAll();
  const afterNavigation = calls();
  staleResize?.([{ target: root }]); dom.window.dispatchEvent(new dom.window.Event('resize')); runAll();
  assert.equal(calls(), afterNavigation);
  assert.equal(disconnects, 1, 'navigation disconnects the topology ResizeObserver');
  app.destroy();
  const afterDestroy = calls(); staleResize?.([{ target: root }]); dom.window.dispatchEvent(new dom.window.Event('resize')); runAll();
  assert.equal(calls(), afterDestroy);
  assert.equal(disconnects, 1, 'destroy does not duplicate completed observer cleanup');
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
    results: [compactTraceResult('a.example'), compactTraceResult('b.example')],
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
    results: [compactTraceResult('v6.example')],
    compact_topology: compactTopology({
      nodes: [
        { id: 'n1', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
        { id: 'n2', kind: 'ip', address: '2001:db8::1', status: 'degraded', hop_min: 1, hop_max: 2, observations: 7, latency_ms_avg: 12.5, public_ip: true,
          geolocation: { city: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 }, asn: { number: 64500, organization: 'Example Transit' } },
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
    results: [compactTraceResult('fault.example')], compact_topology: compactTopology()
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
    results: [compactTraceResult('limited.example')], compact_topology: compactTopology()
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
    { id: 'unknown', kind: 'unknown', status: 'unknown', hop_min: 2, hop_max: 2, observations: 1 },
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
    status: 'unreachable', summary: { total: 1, passed: 0, failed: 1 },
    results: [compactTraceResult('unknown.example', {
      status: 'unreachable', errorCode: 'destination_unreached',
      attemptsReached: 0, attemptsUnreached: 1
    })],
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
  const results = routes.map((_route, index) => compactTraceResult(`target-${index}.example`));
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
    if (calls === 1) return response(JSON.stringify({ error: { code: 'server_busy', message: 'report capacity is temporarily unavailable' } }), 503);
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

test('human export omits targets, producer prose, and report ID and uses a generic filename', async () => {
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
  assert.doesNotMatch(exported, /private\.internal\.example|internal\.example/i);
  assert.doesNotMatch(exported, /Visit|Resolve/);
  assert.match(exported, /### cause \[finding\.dns_resolution_failed\]/);
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
  assert.match(summary.textContent, /Observation point/);
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
  assert.match(css, /@media \(max-width:375px\)/);
  assert.match(css, /@media \(max-width:320px\)/);
  assert.match(css, /@media \(max-width:760px\)/);
  assert.match(css, /focus-visible/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /prefers-reduced-motion:reduce[^}]*\{[^}]*transition:none/s);
  assert.match(css, /\.topology-view-toolbar\s*\{[^}]*flex-wrap:wrap/s);
  assert.match(css, /\.topology-canvas\s*\{[^}]*width:100%[^}]*min-height:[^;}]+[^}]*aspect-ratio:/s);
  assert.match(css, /evidence-scroll[^}]*overflow-x:auto|overflow-x:auto[^}]*evidence-scroll/s);
  assert.match(css, /\.standalone-topology\s*\{[^}]*display:grid/s);
  assert.match(css, /\.standalone-topology\s+\.topology-node\s*\{/s);
  assert.match(css, /\.topology-geo-canvas\s*\{/s);
  assert.match(css, /\.standalone-topology\s+\[data-detail\]:(?:hover|focus)[^}]*::after/s);
  assert.match(css, /--observation-width/);
  assert.match(css, /--route-color/);
});
