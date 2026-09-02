import test from 'node:test';
import assert from 'node:assert/strict';

import {
  REQUEST_PHASES, ERROR_KINDS, SCHEMA_LIMITS,
  createRequestLane, canonicalDiagnosticsInput, canonicalTopologyInput, inputSignature,
  transitionRequest, ownsRequest, clientTimeoutMS, parseRetryAfter,
  normalizeRequestError, parseResponse,
  normalizeReport, normalizeAnalysis, normalizeResult, normalizeTopology,
  normalizeCompactTopology
} from './state.js';

const started = '2026-09-01T12:00:00Z';
const result = (overrides = {}) => ({
  kind: 'dns', address: 'example.test', status: 'healthy', latency_ms: 4,
  started_at: started, details: { addresses: ['192.0.2.1'], answer_count: 1 }, ...overrides
});
const legacyReport = (overrides = {}) => ({
  id: 'report-1', status: 'healthy', started_at: started, duration_ms: 10,
  summary: { total: 1, passed: 1, failed: 0 }, results: [result()], ...overrides
});
const analysis = (overrides = {}) => ({
  verdict: 'attention',
  findings: [{ id: 'f-1', code: 'dns_resolution_failed', severity: 'critical', category: 'name_resolution', title: 'DNS failed', summary: 'No answer', confidence: 'direct', evidence_ids: ['e-1'], action_ids: ['a-1'] }],
  evidence: [{ id: 'e-1', result_index: 0, kind: 'dns', address: 'example.test', signal: 'error_code', observed: 'connection_failed', expected: 'successful DNS resolution', provenance: 'result' }],
  actions: [{ id: 'a-1', title: 'Check DNS', step: 'Resolve again', expected_result: 'An address', escalation_condition: 'Still no answer' }],
  coverage: { available: ['results[0].status'], missing: [], provider_failures: [], limitations: [] },
  ...overrides
});

const compactTopology = (overrides = {}) => ({
  schema: 'compact-v1',
  selection: 'fair-complete-prefix-v1',
  limits: { nodes: 500, links: 1000, max_response_bytes_exclusive: 1048576, max_geo_bundle_bytes: 4096 },
  nodes: [
    { id: 'n000001', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
    { id: 'n000002', kind: 'ip', address: '192.0.2.1', status: 'degraded', hop_min: 1, hop_max: 2, latency_ms_avg: 2.5, observations: 1, public_ip: true,
      geolocation: { city: 'Seoul', region: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 },
      asn: { number: 64500, organization: 'Example' } }
  ],
  links: [{ from: 'n000001', to: 'n000002', status: 'degraded', observations: 1 }],
  routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['n000001', 'n000002'] }],
  stats: {
    nodes: { total: 2, displayed: 2, omitted: 0 },
    links: { total: 1, displayed: 1, omitted: 0 },
    routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
    node_observations: { total: 2, displayed: 2, omitted: 0 },
    link_observations: { total: 1, displayed: 1, omitted: 0 }
  },
  result_stats: [{
    result_index: 0,
    routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
    node_observations: { total: 2, displayed: 2, omitted: 0 },
    link_observations: { total: 1, displayed: 1, omitted: 0 }
  }],
  geo: { eligible: 1, available: 1, included: 1, omitted: 0, unavailable: 0 },
  truncated: false,
  truncation_reasons: [],
  ...overrides
});

function response(status, headers = {}) {
  const normalized = new Map(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  return { status, ok: status >= 200 && status < 300, headers: { get: name => normalized.get(name.toLowerCase()) ?? null } };
}

test('exports fixed lifecycle, error, and schema contracts', () => {
  assert.deepEqual(REQUEST_PHASES, ['idle', 'loading', 'ready', 'error', 'cancelled']);
  assert.deepEqual(ERROR_KINDS, ['http', 'network', 'timeout', 'invalid-response', 'cancelled']);
  assert.equal(SCHEMA_LIMITS.results, 20);
  assert.equal(SCHEMA_LIMITS.string, 4096);
});

test('request lane replaces owners and rejects stale completion/finalize', () => {
  const idle = createRequestLane('diagnostics');
  assert.equal(idle.phase, 'idle');
  let lane = transitionRequest(idle, { type: 'REQUEST_STARTED', ownerId: 'a', inputSignature: 'sig-a', startedAt: 1 });
  lane = transitionRequest(lane, { type: 'REQUEST_STARTED', ownerId: 'b', inputSignature: 'sig-b', startedAt: 2 });
  const stale = transitionRequest(lane, { type: 'REQUEST_SUCCEEDED', ownerId: 'a', inputSignature: 'sig-a', report: legacyReport(), completedAt: 3 });
  assert.strictEqual(stale, lane);
  assert.equal(ownsRequest(lane, 'a', 'sig-a'), false);
  assert.equal(ownsRequest(lane, 'b', 'sig-b'), true);
  assert.strictEqual(transitionRequest(lane, { type: 'REQUEST_FINALIZED', ownerId: 'a' }), lane);
  lane = transitionRequest(lane, { type: 'REQUEST_SUCCEEDED', ownerId: 'b', inputSignature: 'sig-b', report: legacyReport({ id: 'b' }), completedAt: 4 });
  assert.equal(lane.phase, 'ready');
  assert.equal(lane.result.report.id, 'b');
  lane = transitionRequest(lane, { type: 'REQUEST_FINALIZED', ownerId: 'b' });
  assert.equal(lane.active, null);
  assert.equal(lane.phase, 'ready');
});

test('error/cancel transitions distinguish cancellation and input invalidation clears ownership', () => {
  let lane = transitionRequest(createRequestLane('topology'), { type: 'REQUEST_STARTED', ownerId: 'a', inputSignature: 'one', startedAt: 1 });
  lane = transitionRequest(lane, { type: 'REQUEST_CANCELLED', ownerId: 'a', inputSignature: 'one', reason: 'user' });
  assert.equal(lane.phase, 'cancelled');
  assert.deepEqual(lane.cancellation, { reason: 'user' });
  lane = transitionRequest(lane, { type: 'INPUT_CHANGED', inputSignature: 'two' });
  assert.equal(lane.phase, 'idle');
  assert.equal(lane.inputSignature, 'two');
  assert.equal(lane.active, null);
  assert.equal(lane.result, null);
  assert.equal(lane.cancellation, null);
});

test('canonical inputs validate API URLs, normalize values, and signatures never contain bearer text', () => {
  const rawBearer = 'secret-bearer-token';
  const diagnostics = canonicalDiagnosticsInput({
    apiBaseURL: ' https://api.example.test/proxy/ ', timeout_ms: '5000', bearer: rawBearer,
    targets: [{ kind: ' dns ', address: ' example.test ', expected_status: '0' }]
  });
  assert.deepEqual(diagnostics, { apiBaseURL: 'https://api.example.test/proxy', targets: [{ kind: 'dns', address: 'example.test', expected_status: 0 }], timeout_ms: 5000, authEnabled: true });
  const topology = canonicalTopologyInput({ apiBaseURL: 'http://localhost:8080/', addresses: [' a.test ', 'a.test', 'b.test'], attempts: '3', timeout_ms: '2000', authEnabled: false });
  assert.deepEqual(topology.addresses, ['a.test', 'b.test']);
  assert.throws(() => canonicalDiagnosticsInput({ apiBaseURL: 'ftp://x', targets: [] }));
  assert.throws(() => canonicalDiagnosticsInput({ apiBaseURL: 'https://u:p@x/a?query=1', targets: [] }));
  const sig1 = inputSignature('diagnostics', diagnostics, 7);
  const sig2 = inputSignature('diagnostics', diagnostics, 7);
  assert.equal(sig1, sig2);
  assert.doesNotMatch(sig1, /secret-bearer-token/);
  assert.notEqual(sig1, inputSignature('diagnostics', { ...diagnostics, timeout_ms: 5001 }, 7));
  assert.notEqual(sig1, inputSignature('topology', topology, 7));
  assert.notEqual(sig1, inputSignature('diagnostics', diagnostics, 8));
});

test('client timeout follows server budget and is capped at 307 seconds', () => {
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'dns' }], timeout_ms: 5000 }), 12000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute' }], timeout_ms: 5000 }), 32000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 3 }], timeout_ms: 2000 }), 13000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 10 }], timeout_ms: 30000 }), 307000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 999 }], timeout_ms: 999999 }), 307000);
});

test('Retry-After parses delta seconds and HTTP dates', () => {
  const now = Date.parse('2026-09-01T12:00:00Z');
  assert.equal(parseRetryAfter('12', now), now + 12000);
  assert.equal(parseRetryAfter('Tue, 01 Sep 2026 12:01:00 GMT', now), now + 60000);
  assert.equal(parseRetryAfter('-1', now), null);
  assert.equal(parseRetryAfter('nonsense', now), null);
});

test('request errors distinguish network, timeout, and caller cancellation', () => {
  assert.equal(normalizeRequestError(new TypeError('fetch failed')).code, 'network_error');
  assert.equal(normalizeRequestError({ name: 'AbortError', reason: 'timeout' }).kind, 'timeout');
  assert.equal(normalizeRequestError({ name: 'AbortError', reason: 'user' }).kind, 'cancelled');
  assert.equal(normalizeRequestError({ name: 'TimeoutError' }).kind, 'timeout');
});

test('parseResponse handles empty/non-JSON errors and empty/malformed successes', () => {
  assert.equal(parseResponse(response(500), '<html>proxy secret</html>', 0).error.code, 'http_500');
  assert.doesNotMatch(parseResponse(response(500), '<html>proxy secret</html>', 0).error.message, /proxy secret/);
  assert.equal(parseResponse(response(500), '', 0).error.code, 'http_500');
  assert.equal(parseResponse(response(200), '', 0).error.code, 'invalid_response');
  assert.equal(parseResponse(response(200), '{oops', 0).error.code, 'invalid_response');
});

test('parseResponse normalizes 401/422/429/503 and Retry-After', () => {
  const now = Date.parse('2026-09-01T12:00:00Z');
  const cases = [
    [401, 'unauthorized', false], [422, 'network_policy_blocked', false],
    [429, 'rate_limited', true], [503, 'server_busy', true]
  ];
  for (const [status, code, retryable] of cases) {
    const parsed = parseResponse(response(status, { 'Retry-After': '30' }), JSON.stringify({ error: { code, message: `message ${status}` } }), now);
    assert.equal(parsed.ok, false);
    assert.equal(parsed.error.code, code);
    assert.equal(parsed.error.status, status);
    assert.equal(parsed.error.retryable, retryable);
    assert.equal(parsed.error.retryAt, status >= 429 ? now + 30000 : null);
  }
});

test('normalizers copy valid report/analysis/result/topology into inert plain data', () => {
  const xss = '<img src=x onerror=alert(1)>';
  const topology = normalizeTopology({ reached: true, nodes: [{ id: 'hop-1', hop: 1, address: xss, latency_ms: 1.5, status: 'healthy', public_ip: true, geolocation: { city: xss, country: 'KR', latitude: 37.5, longitude: 127 }, asn: { number: 64500, organization: xss } }], links: [] });
  assert.equal(topology.nodes[0].address, xss);
  assert.equal(Object.getPrototypeOf(topology), Object.prototype);
  const normalizedResult = normalizeResult(result({ message: xss, details: { topology } }), 0);
  assert.equal(normalizedResult.message, xss);
  const full = normalizeReport(legacyReport({ analysis: analysis({ findings: [{ ...analysis().findings[0], title: xss }] }) }));
  assert.equal(full.analysis.findings[0].title, xss);
  assert.equal(JSON.stringify(full).includes(xss), true);
});

test('legacy reports without analysis are accepted while malformed schema is rejected', () => {
  const normalized = normalizeReport(legacyReport());
  assert.equal(normalized.analysis, null);
  assert.equal(parseResponse(response(200), JSON.stringify(legacyReport()), 0).report.analysis, null);
  assert.throws(() => normalizeReport(legacyReport({ status: 'mystery' })));
  assert.throws(() => normalizeReport(legacyReport({ duration_ms: -1 })));
  assert.throws(() => normalizeReport(legacyReport({ results: Array.from({ length: SCHEMA_LIMITS.results + 1 }, () => result()) })));
  assert.throws(() => normalizeAnalysis(analysis({ verdict: 'maybe' })));
  assert.throws(() => normalizeAnalysis(analysis({ findings: [{ ...analysis().findings[0], severity: 'fatal' }] })));
  assert.throws(() => normalizeAnalysis(analysis({ evidence: [{ ...analysis().evidence[0], result_index: -1 }] })));
  assert.throws(() => normalizeAnalysis(analysis({ actions: [{ ...analysis().actions[0], title: 'x'.repeat(SCHEMA_LIMITS.string + 1) }] })));
  assert.throws(() => normalizeAnalysis(analysis({ coverage: { ...analysis().coverage, available: Array(SCHEMA_LIMITS.coverage + 1).fill('x') } })));
  assert.throws(() => normalizeTopology({ reached: true, nodes: [{ id: 'x', hop: 1, status: 'evil' }], links: [] }));
});

test('compact topology is optional and valid compact data is deeply copied into inert plain data', () => {
  const absent = normalizeReport(legacyReport());
  assert.equal(absent.compact_topology, null);

  const source = compactTopology();
  const normalized = normalizeReport(legacyReport({ compact_topology: source }));
  assert.notStrictEqual(normalized.compact_topology, source);
  assert.notStrictEqual(normalized.compact_topology.nodes, source.nodes);
  assert.notStrictEqual(normalized.compact_topology.nodes[1].geolocation, source.nodes[1].geolocation);
  assert.deepEqual(normalized.compact_topology, source);
  source.nodes[1].geolocation.city = 'mutated';
  source.routes[0].node_ids[0] = 'mutated';
  assert.equal(normalized.compact_topology.nodes[1].geolocation.city, 'Seoul');
  assert.equal(normalized.compact_topology.routes[0].node_ids[0], 'n000001');
  assert.equal(Object.getPrototypeOf(normalized.compact_topology), Object.prototype);
  assert.equal(Object.getPrototypeOf(normalized.compact_topology.nodes), Array.prototype);
});

test('present malformed compact topology rejects the report without legacy fallback', () => {
  const malformed = compactTopology({ schema: 'compact-v2' });
  const report = legacyReport({ compact_topology: malformed });
  assert.throws(() => normalizeReport(report), /compact_topology|schema/i);
  assert.throws(() => normalizeReport(legacyReport({ compact_topology: null })), /compact_topology|schema/i);
  const parsed = parseResponse(response(200), JSON.stringify(report), 0);
  assert.equal(parsed.ok, false);
  assert.equal(parsed.error.code, 'invalid_response');
});

test('compact per-result displayed route kinds must match the route array', () => {
  const value = compactTopology({
    nodes: [
      { id: 'n000001', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 2 },
      { id: 'n000002', kind: 'ip', address: '192.0.2.1', status: 'degraded', hop_min: 1, hop_max: 2, latency_ms_avg: 2.5, observations: 1, public_ip: true,
        geolocation: { city: 'Seoul', region: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 }, asn: { number: 64500, organization: 'Example' } }
    ],
    routes: [
      { result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['n000001', 'n000002'] },
      { result_index: 1, attempt: 1, status: 'degraded', reached: false, complete: false, node_ids: ['n000001'] }
    ],
    stats: {
      nodes: { total: 2, displayed: 2, omitted: 0 }, links: { total: 1, displayed: 1, omitted: 0 },
      routes: { total: 2, displayed: 2, complete: 1, partial: 1, omitted: 0 },
      node_observations: { total: 3, displayed: 3, omitted: 0 }, link_observations: { total: 1, displayed: 1, omitted: 0 }
    },
    result_stats: [
      { result_index: 0, routes: { total: 1, displayed: 1, complete: 0, partial: 1, omitted: 0 }, node_observations: { total: 3, displayed: 3, omitted: 0 }, link_observations: { total: 1, displayed: 1, omitted: 0 } },
      { result_index: 1, routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 }, node_observations: { total: 0, displayed: 0, omitted: 0 }, link_observations: { total: 0, displayed: 0, omitted: 0 } }
    ]
  });
  assert.throws(() => normalizeCompactTopology(value), /result_stats|route/i);
});

test('compact routes accept backend-collapsed consecutive canonical nodes', () => {
  const value = structuredClone(compactTopology());
  value.routes[0].node_ids = ['n000001', 'n000002', 'n000002'];
  value.nodes[1].observations = 2;
  value.stats.node_observations = { total: 3, displayed: 3, omitted: 0 };
  value.result_stats[0].node_observations = { total: 3, displayed: 3, omitted: 0 };

  const normalized = normalizeCompactTopology(value);
  assert.deepEqual(normalized.routes[0].node_ids, ['n000001', 'n000002']);
  assert.deepEqual(normalized.stats.node_observations, value.stats.node_observations);
});

test('compact route observation stats count emitted route entries independently of raw node observations', () => {
  const value = structuredClone(compactTopology());
  value.nodes[1].observations = 2;
  value.stats.node_observations = { total: 2, displayed: 2, omitted: 0 };
  value.result_stats[0].node_observations = { total: 2, displayed: 2, omitted: 0 };

  const normalized = normalizeCompactTopology(value);
  assert.equal(normalized.routes[0].node_ids.length, 2);
  assert.equal(normalized.nodes.reduce((sum, node) => sum + node.observations, 0), 3);
  assert.equal(normalized.stats.node_observations.displayed, 2);
});

test('compact schema accepts the exact backend 30-hop unreached route ceiling', () => {
  const value = structuredClone(compactTopology());
  value.nodes = Array.from({ length: 32 }, (_, index) => ({
    id: `n${String(index + 1).padStart(6, '0')}`,
    kind: index === 0 ? 'local' : index === 31 ? 'hostname' : 'unknown',
    address: index === 0 ? 'local' : index === 31 ? 'target.example' : '',
    status: index === 0 ? 'healthy' : index === 31 ? 'failure' : 'unknown',
    hop_min: index === 31 ? 30 : index,
    hop_max: index === 31 ? 30 : index,
    observations: 1
  }));
  value.links = value.nodes.slice(1).map((node, index) => ({
    from: value.nodes[index].id, to: node.id, status: node.status, observations: 1
  }));
  value.routes = [{ result_index: 0, attempt: 1, status: 'unreachable', reached: false, complete: true, node_ids: value.nodes.map(node => node.id) }];
  value.stats = {
    nodes: { total: 32, displayed: 32, omitted: 0 }, links: { total: 31, displayed: 31, omitted: 0 },
    routes: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 },
    node_observations: { total: 32, displayed: 32, omitted: 0 }, link_observations: { total: 31, displayed: 31, omitted: 0 }
  };
  value.result_stats = [{ result_index: 0, routes: value.stats.routes, node_observations: value.stats.node_observations, link_observations: value.stats.link_observations }];
  value.geo = { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 };

  assert.equal(normalizeCompactTopology(value).routes[0].node_ids.length, 32);
});

test('compact links are unique directed non-self edges and routes use real edges', () => {
  const self = structuredClone(compactTopology());
  self.links[0].to = self.links[0].from;
  assert.throws(() => normalizeCompactTopology(self), /self|link/i);

  const duplicate = structuredClone(compactTopology());
  duplicate.links.push(structuredClone(duplicate.links[0]));
  duplicate.stats.links = { total: 2, displayed: 2, omitted: 0 };
  duplicate.stats.link_observations = { total: 2, displayed: 2, omitted: 0 };
  duplicate.result_stats[0].link_observations = { total: 2, displayed: 2, omitted: 0 };
  assert.throws(() => normalizeCompactTopology(duplicate), /duplicate|unique|link/i);

  const missing = structuredClone(compactTopology());
  missing.links = [];
  missing.stats.links = { total: 0, displayed: 0, omitted: 0 };
  missing.stats.link_observations = { total: 1, displayed: 0, omitted: 1 };
  assert.throws(() => normalizeCompactTopology(missing), /route|link|edge/i);

});

test('compact result observation displays are derived from each route prefix', () => {
  const wrongNodes = structuredClone(compactTopology());
  wrongNodes.result_stats[0].node_observations = { total: 2, displayed: 1, omitted: 1 };
  assert.throws(() => normalizeCompactTopology(wrongNodes), /result_stats|observation/i);

  const wrongLinks = structuredClone(compactTopology());
  wrongLinks.result_stats[0].link_observations = { total: 1, displayed: 0, omitted: 1 };
  assert.throws(() => normalizeCompactTopology(wrongLinks), /result_stats|observation/i);
});

test('compact duplicate attempt numbers retain backend occurrence order', () => {
  const value = structuredClone(compactTopology());
  value.routes.push({ ...structuredClone(value.routes[0]), complete: false, node_ids: ['n000001'] });
  value.nodes[0].observations = 2;
  value.stats.routes = { total: 2, displayed: 2, complete: 1, partial: 1, omitted: 0 };
  value.stats.node_observations = { total: 3, displayed: 3, omitted: 0 };
  value.result_stats[0].routes = { total: 2, displayed: 2, complete: 1, partial: 1, omitted: 0 };
  value.result_stats[0].node_observations = { total: 3, displayed: 3, omitted: 0 };
  const normalized = normalizeCompactTopology(value);
  assert.equal(normalized.routes.length, 2);
  assert.deepEqual(normalized.routes.map(route => [route.result_index, route.attempt]), [[0, 1], [0, 1]]);
});

test('compact truncation reasons accept backend rollback response order', () => {
  const value = compactTopology({ truncated: true, truncation_reasons: ['response_size', 'geo_metadata_limit'] });
  assert.deepEqual(normalizeCompactTopology(value).truncation_reasons, ['response_size', 'geo_metadata_limit']);
});

test('compact schema rejects caps, identity, references, numeric, enum, reason, count, and Geo violations', () => {
  const invalid = [];
  const mutate = callback => { const value = structuredClone(compactTopology()); callback(value); invalid.push(value); };
  mutate(value => { value.selection = 'first-come'; });
  mutate(value => { value.nodes = Array.from({ length: 501 }, (_, index) => ({ id: `n${index}`, kind: 'ip', address: `${index}`, status: 'healthy', hop_min: 1, hop_max: 1, observations: 1 })); });
  mutate(value => { value.links = Array.from({ length: 1001 }, () => ({ from: 'n000001', to: 'n000002', status: 'healthy', observations: 1 })); });
  mutate(value => { value.nodes[1].id = value.nodes[0].id; });
  mutate(value => { value.nodes[1].latency_ms_avg = Number.NaN; });
  mutate(value => { value.nodes[1].kind = 'router'; });
  mutate(value => { value.links[0].status = 'unreachable'; });
  mutate(value => { value.links[0].to = 'missing'; });
  mutate(value => { value.routes[0].node_ids = ['missing']; });
  mutate(value => { value.stats.nodes.total = 3; });
  mutate(value => { value.stats.nodes.displayed = 1; value.stats.nodes.omitted = 1; });
  mutate(value => { value.geo.included = 0; value.geo.omitted = 1; });
  mutate(value => { value.truncated = true; value.truncation_reasons = ['response_size', 'node_limit']; });
  mutate(value => { value.truncated = true; value.truncation_reasons = ['node_limit', 'node_limit']; });
  for (const value of invalid) assert.throws(() => normalizeCompactTopology(value), /schema|compact|limit|count|reference|unique|Geo|reason/i);
});

test('compact schema preserves required empty arrays and rejects accessors without invoking them', () => {
  const empty = compactTopology({
    nodes: [], links: [], routes: [],
    stats: { nodes: { total: 0, displayed: 0, omitted: 0 }, links: { total: 0, displayed: 0, omitted: 0 }, routes: { total: 0, displayed: 0, complete: 0, partial: 0, omitted: 0 }, node_observations: { total: 0, displayed: 0, omitted: 0 }, link_observations: { total: 0, displayed: 0, omitted: 0 } },
    result_stats: [], geo: { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 }
  });
  const normalized = normalizeCompactTopology(empty);
  assert.deepEqual([normalized.nodes, normalized.links, normalized.routes, normalized.result_stats, normalized.truncation_reasons], [[], [], [], [], []]);
  let invoked = false;
  Object.defineProperty(empty, 'nodes', { get() { invoked = true; return []; } });
  assert.throws(() => normalizeCompactTopology(empty), /accessor|schema/i);
  assert.equal(invoked, false);
});

test('accessor-like and prototype keys are not preserved by normalization', () => {
  const input = JSON.parse('{"id":"safe","status":"healthy","started_at":"2026-09-01T12:00:00Z","duration_ms":1,"summary":{"total":0,"passed":0,"failed":0},"results":[],"__proto__":{"polluted":true},"constructor":"bad"}');
  const normalized = normalizeReport(input);
  assert.equal(Object.hasOwn(normalized, '__proto__'), false);
  assert.equal(Object.hasOwn(normalized, 'constructor'), false);
  assert.equal({}.polluted, undefined);
});

test('strict result details reject malformed trace attempts and contradictory report aggregates', () => {
  const badAttempt = result({
    kind: 'traceroute',
    details: { attempts: [{ attempt: 1, status: 'invented', error_code: 'timeout' }] }
  });
  assert.throws(() => normalizeResult(badAttempt, 0));
  assert.throws(() => normalizeReport(legacyReport({ summary: { total: 1, passed: 0, failed: 1 } })));
  assert.throws(() => normalizeReport(legacyReport({
    analysis: analysis({ evidence: [{ ...analysis().evidence[0], result_index: 19 }] })
  })));
});

test('topology normalization requires unique nodes and links that reference them', () => {
  assert.throws(() => normalizeTopology({
    reached: true,
    nodes: [{ id: 'same', hop: 1, status: 'healthy' }, { id: 'same', hop: 2, status: 'healthy' }],
    links: []
  }));
  assert.throws(() => normalizeTopology({
    reached: true,
    nodes: [{ id: 'one', hop: 1, status: 'healthy' }],
    links: [{ from: 'one', to: 'missing', status: 'healthy' }]
  }));
});

test('structured HTTP errors never retain a reflected bearer credential', () => {
  const bearer = 'Bearer SUPER-secret-Token-123';
  const parsed = parseResponse(
    response(401),
    JSON.stringify({ error: { code: 'unauthorized', message: `invalid ${bearer}` } }),
    0
  );
  assert.equal(parsed.error.code, 'unauthorized');
  assert.doesNotMatch(JSON.stringify(parsed), /SUPER-secret-Token-123/i);
  assert.match(parsed.error.message, /HTTP 401|authorization|credential|unauthorized/i);
});

test('structured HTTP error codes are allowlisted and cannot reflect a bearer credential', () => {
  const bearer = 'reflected-secret-token-123';
  const parsed = parseResponse(response(401), JSON.stringify({ error: { code: bearer, message: 'rejected' } }), 0);
  assert.equal(parsed.error.code, 'unauthorized');
  assert.doesNotMatch(JSON.stringify(parsed), new RegExp(bearer, 'i'));
});

test('every exported normalizer rejects non-plain objects and accessors without invoking them', () => {
  class ReportLike {}
  const inherited = Object.assign(new ReportLike(), legacyReport());
  assert.throws(() => normalizeReport(inherited), /plain|prototype|schema/i);
  assert.throws(() => normalizeAnalysis(Object.assign(Object.create(null), analysis())), /plain|prototype|schema/i);
  assert.throws(() => normalizeResult(Object.assign(Object.create({ inherited: true }), result())), /plain|prototype|schema/i);
  assert.throws(() => normalizeTopology(Object.assign(Object.create(null), { reached: true, nodes: [], links: [] })), /plain|prototype|schema/i);

  let invoked = false;
  const topologyWithGetter = { reached: true, links: [] };
  Object.defineProperty(topologyWithGetter, 'nodes', {
    enumerable: true,
    get() { invoked = true; throw new Error('getter executed'); }
  });
  assert.throws(() => normalizeTopology(topologyWithGetter), /accessor|schema/i);
  assert.equal(invoked, false);
});

test('normalizers reject non-enumerable object and array accessors without invoking them', () => {
  let invoked = 0;
  const topology = { reached: true, links: [] };
  Object.defineProperty(topology, 'nodes', { enumerable: false, get() { invoked++; return []; } });
  assert.throws(() => normalizeTopology(topology), /accessor|schema/i);

  const nodes = [];
  Object.defineProperty(nodes, '0', { enumerable: false, configurable: true, get() { invoked++; return { id: 'x', hop: 1, status: 'healthy' }; } });
  nodes.length = 1;
  assert.throws(() => normalizeTopology({ reached: true, nodes, links: [] }), /accessor|schema/i);
  assert.equal(invoked, 0);
});

test('normalizers reject symbol accessors without invoking them', () => {
  let invoked = 0;
  const topology = { reached: true, nodes: [], links: [] };
  Object.defineProperty(topology, Symbol('hostile'), { get() { invoked++; return 'secret'; } });
  assert.throws(() => normalizeTopology(topology), /accessor|schema/i);
  assert.equal(invoked, 0);
});

function maximumTracerouteReport() {
  const results = [];
  for (let target = 0; target < 20; target++) {
    const attempts = [];
    for (let attempt = 1; attempt <= 10; attempt++) {
      const nodes = Array.from({ length: 30 }, (_, hop) => ({
        id: `t${target}-a${attempt}-h${hop}`,
        hop,
        address: `198.18.${target}.${hop}`,
        latency_ms: hop + 0.25,
        status: 'healthy',
        public_ip: true,
        geolocation: { city: 'Seoul', region: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 },
        asn: { number: 64500 + target, organization: 'Example Network' }
      }));
      const links = nodes.slice(1).map((node, hop) => ({
        from: nodes[hop].id, to: node.id, status: 'healthy', latency_delta_ms: 1
      }));
      attempts.push({ attempt, status: 'healthy', error_code: '', message: '', topology: { reached: true, nodes, links } });
    }
    results.push({
      kind: 'traceroute', address: `target-${target}.example`, status: 'healthy', latency_ms: 1,
      started_at: started,
      details: {
        attempts,
        attempts_total: 10,
        attempts_reached: 10,
        attempts_failed: 0,
        attempts_unreached: 0,
        attempts_execution_failed: 0,
        attempts_timed_out: 0,
        attempts_cancelled: 0,
        topology: JSON.parse(JSON.stringify(attempts[0].topology))
      }
    });
  }
  return {
    id: 'maximum-valid', status: 'healthy', started_at: started, duration_ms: 300000,
    summary: { total: 20, passed: 20, failed: 0 }, results
  };
}

test('aggregate budgets accept the valid 20×10×30 traceroute maximum with GeoIP and ASN', () => {
  const normalized = normalizeReport(maximumTracerouteReport());
  assert.equal(normalized.results.length, 20);
  assert.equal(normalized.results[19].details.attempts.length, 10);
  assert.equal(normalized.results[19].details.attempts[9].topology.nodes.length, 30);
});

test('aggregate budgets reject multiplicative hostile details without charging repeated key names', () => {
  const hostile = legacyReport({
    results: Array.from({ length: 20 }, (_, index) => result({
      address: `hostile-${index}`,
      details: Object.fromEntries(Array.from({ length: 200 }, (_, key) => [
        `field${key}`,
        Array.from({ length: 200 }, () => ({ value: 'x' }))
      ]))
    })),
    summary: { total: 20, passed: 20, failed: 0 }
  });
  assert.throws(() => normalizeReport(hostile), /aggregate|budget|limit/i);

  const repeatedKeys = legacyReport({
    results: [result({ details: { rows: Array.from({ length: 1500 }, () => ({ repeated_property_name: 'ok' })) } })]
  });
  assert.doesNotThrow(() => normalizeReport(repeatedKeys));
});
