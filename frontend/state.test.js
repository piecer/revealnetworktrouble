import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

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
  ...overrides
});

const zeroResultStats = result_index => ({
  result_index,
  routes: { total: 0, displayed: 0, complete: 0, partial: 0, omitted: 0 },
  node_observations: { total: 0, displayed: 0, omitted: 0 },
  link_observations: { total: 0, displayed: 0, omitted: 0 }
});

const emptyCompactTopology = result_stats => compactTopology({
  nodes: [], links: [], routes: [], result_stats,
  stats: {
    nodes: { total: 0, displayed: 0, omitted: 0 },
    links: { total: 0, displayed: 0, omitted: 0 },
    routes: { total: 0, displayed: 0, complete: 0, partial: 0, omitted: 0 },
    node_observations: { total: 0, displayed: 0, omitted: 0 },
    link_observations: { total: 0, displayed: 0, omitted: 0 }
  },
  geo: { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 }
});

async function findingContractFixture() {
  return JSON.parse(await readFile(new URL('../testdata/finding-contract.json', import.meta.url), 'utf8'));
}

async function apiErrorContractFixture() {
  return JSON.parse(await readFile(new URL('../testdata/api-error-contract.json', import.meta.url), 'utf8'));
}

function resultDetailValue(detail) {
  if (detail.const !== undefined) return detail.const;
  switch (detail.type) {
    case 'string': return 'fixture';
    case 'nonempty_string': return 'TLS 1.3';
    case 'canonical_utc_rfc3339':
    case 'utc_timestamp': return started;
    case 'nonnegative_integer': return 0;
    case 'string_array': return ['192.0.2.1'];
    case 'trace_attempts': return [];
    case 'topology': return { reached: true, nodes: [], links: [] };
    case 'enrichment': return {
      provider: 'geoip', source: 'none', cache_hits: 0, upstream_fetches: 0, max_age_ms: 0, failures: []
    };
    default: throw new TypeError(`unsupported fixture detail type: ${detail.type}`);
  }
}

function resultDetailsForShape(shape) {
  const fields = [...shape.required_details, ...shape.optional_details];
  if (fields.length === 0) return undefined;
  const details = Object.fromEntries(fields.map(detail => [detail.key, resultDetailValue(detail)]));
  if (Object.hasOwn(details, 'certificate_not_before')) details.certificate_not_before = '2025-09-01T12:00:00Z';
  if (Object.hasOwn(details, 'certificate_not_after')) details.certificate_not_after = '2027-09-01T12:00:00Z';
  return details;
}

function resultShapeReport(shape, kind = shape.kinds[0]) {
  const details = resultDetailsForShape(shape);
  const value = result({
    kind,
    status: shape.status,
    ...(shape.error_code === undefined ? {} : { error_code: shape.error_code }),
    ...(details === undefined ? { details: undefined } : { details })
  });
  if (details === undefined) delete value.details;
  return legacyReport({
    status: shape.status,
    summary: { total: 1, passed: shape.status === 'healthy' ? 1 : 0, failed: shape.status === 'healthy' ? 0 : 1 },
    results: [value]
  });
}

function fixtureAllowsResult(contract, value) {
  const code = Object.hasOwn(value, 'error_code') ? value.error_code : undefined;
  return contract.result_shapes.some(shape => {
    if (!shape.kinds.includes(value.kind) || shape.status !== value.status || shape.error_code !== code) return false;
    const required = shape.required_details.map(detail => detail.key);
    const allowed = new Set([...required, ...shape.optional_details.map(detail => detail.key)]);
    if (!Object.hasOwn(value, 'details')) return required.length === 0;
    if (value.details === null || typeof value.details !== 'object' || Array.isArray(value.details)) return false;
    const keys = Object.keys(value.details);
    return required.every(key => Object.hasOwn(value.details, key)) && keys.every(key => allowed.has(key));
  });
}

function semanticCompatibilityAllowsResult(contract, value) {
  if (fixtureAllowsResult(contract, value)) return true;
  const code = Object.hasOwn(value, 'error_code') ? value.error_code : undefined;
  if ((value.kind === 'dns' || value.kind === 'tcp') && value.status === 'unreachable' &&
      ['checker_capacity_unavailable', 'checker_panic', 'connection_failed', 'network_policy_blocked', 'timeout'].includes(code) &&
      Object.hasOwn(value, 'details') && value.details !== null && typeof value.details === 'object' && !Array.isArray(value.details)) {
    return true;
  }
  if (value.kind === 'dns' && value.status === 'healthy' && code === undefined &&
      Object.hasOwn(value, 'details') && value.details !== null && typeof value.details === 'object' && !Array.isArray(value.details)) {
    return true;
  }
  if (value.kind !== 'traceroute' || !Object.hasOwn(value, 'details') || value.details === null ||
      typeof value.details !== 'object' || Array.isArray(value.details)) return false;
  const tupleAllowed = ((value.status === 'healthy' || value.status === 'degraded') && code === undefined) ||
    (value.status === 'unreachable' && ['destination_unreached', 'timeout', 'traceroute_execution_incomplete', 'traceroute_failed'].includes(code));
  const allowed = new Set([
    'attempts_cancelled', 'attempts_execution_failed', 'attempts_failed', 'attempts_reached',
    'attempts_timed_out', 'attempts_total', 'attempts_unreached', 'attempts',
    'geoip_enrichment', 'geoip_provider_failures', 'topology'
  ]);
  return tupleAllowed && Object.keys(value.details).every(key => allowed.has(key));
}

function fixtureDerivedResultMutationCorpus(contract) {
  const rows = contract.result_shapes.flatMap(shape => shape.kinds.map(kind => ({ shape, kind })));
  const knownKinds = [...new Set(rows.map(({ kind }) => kind))];
  const knownStatuses = [...new Set(contract.result_shapes.map(shape => shape.status))];
  const knownCodes = [undefined, ...new Set(contract.result_shapes.map(shape => shape.error_code).filter(code => code !== undefined))];
  const knownDetails = contract.result_shapes.map(shape => resultDetailsForShape(shape));
  const mutations = [];

  const addFirstIncompatible = (shape, kind, dimension, candidates, apply, describe) => {
    const original = resultShapeReport(shape, kind);
    for (const candidate of candidates) {
      const report = structuredClone(original);
      apply(report, candidate);
      if (!semanticCompatibilityAllowsResult(contract, report.results[0])) {
        mutations.push({ name: `${shape.name}/${kind}: incompatible ${dimension} ${describe(candidate)}`, report, dimension });
        return;
      }
    }
  };

  for (const { shape, kind } of rows) {
    addFirstIncompatible(shape, kind, 'status', knownStatuses.filter(value => value !== shape.status), (report, status) => {
      report.results[0].status = status;
      report.status = status;
      report.summary = { total: 1, passed: status === 'healthy' ? 1 : 0, failed: status === 'healthy' ? 0 : 1 };
    }, String);
    // Runner-owned terminal codes are valid for every kind. Only emit a kind
    // mutation when the fixture proves that the selected known kind is incompatible.
    addFirstIncompatible(shape, kind, 'kind', knownKinds.filter(value => value !== kind),
      (report, candidate) => { report.results[0].kind = candidate; }, String);
    addFirstIncompatible(shape, kind, 'error_code', knownCodes.filter(value => value !== shape.error_code), (report, candidate) => {
      if (candidate === undefined) delete report.results[0].error_code;
      else report.results[0].error_code = candidate;
    }, value => value ?? '<absent>');
    const semanticallyRelatedDetails = [
      ...contract.result_shapes.filter(candidateShape => candidateShape.kinds.includes(kind)).map(resultDetailsForShape),
      ...knownDetails
    ];
    addFirstIncompatible(shape, kind, 'details', semanticallyRelatedDetails, (report, candidate) => {
      if (candidate === undefined) delete report.results[0].details;
      else report.results[0].details = structuredClone(candidate);
    }, value => value === undefined ? '<absent>' : Object.keys(value).join(','));
  }
  return mutations;
}

function validResultForKind(kind) {
  if (['ssh', 'smtp', 'submission', 'smtps', 'imap', 'imaps', 'pop3', 'pop3s'].includes(kind)) {
    const details = { verification_scope: 'server_greeting' };
    if (['smtps', 'imaps', 'pop3s'].includes(kind)) Object.assign(details, {
      certificate_expires_at: started,
      certificate_subject: 'CN=fixture',
      cipher_suite: 'TLS_AES_128_GCM_SHA256',
      tls_version: 'TLS 1.3'
    });
    return result({ kind, details });
  }
  if (kind === 'tcp') return result({ kind, details: { local_address: '192.0.2.1:1', remote_address: '192.0.2.2:2' } });
  if (kind === 'http' || kind === 'https') {
    const value = result({ kind });
    delete value.details;
    return value;
  }
  return result({ kind });
}

function response(status, headers = {}) {
  const normalized = new Map(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  return { status, ok: status >= 200 && status < 300, headers: { get: name => normalized.get(name.toLowerCase()) ?? null } };
}

test('exports fixed lifecycle, error, and schema contracts', () => {
  assert.deepEqual(REQUEST_PHASES, ['idle', 'loading', 'ready', 'error', 'cancelled']);
  assert.deepEqual(ERROR_KINDS, ['http', 'network', 'timeout', 'invalid-response', 'cancelled']);
  assert.equal(SCHEMA_LIMITS.results, 20);
  assert.equal(SCHEMA_LIMITS.string, 4096);
  assert.equal(SCHEMA_LIMITS.findings, 64);
  assert.equal(SCHEMA_LIMITS.evidence, 64);
  assert.equal(SCHEMA_LIMITS.actions, 64);
  assert.equal(SCHEMA_LIMITS.coverage, 128);
});

test('normalizes the serialized maximum Go analysis and rejects every cap plus one', async () => {
  const serialized = await readFile(new URL('../testdata/maximum-analysis-report.json', import.meta.url), 'utf8');
  const maximum = normalizeReport(JSON.parse(serialized));
  assert.deepEqual([
    maximum.analysis.findings.length,
    maximum.analysis.evidence.length,
    maximum.analysis.actions.length,
    maximum.analysis.coverage.available.length
  ], [40, 40, 40, 80]);

  const padded = (name, limit) => {
    const report = JSON.parse(serialized);
    const values = report.analysis[name];
    while (values.length <= limit) values.push({ ...structuredClone(values[0]), id: `${name}-${values.length}` });
    return report;
  };
  for (const [name, limit] of [['findings', SCHEMA_LIMITS.findings], ['evidence', SCHEMA_LIMITS.evidence], ['actions', SCHEMA_LIMITS.actions]]) {
    assert.throws(() => normalizeReport(padded(name, limit)), new RegExp(`analysis\\.${name}.*limit`, 'i'));
  }

  const issue = { code: 'missing_details', result_index: 0, kind: 'https', signal: 'fixture', reason: 'fixture coverage issue' };
  for (const name of ['available', 'missing', 'provider_failures', 'limitations']) {
    const report = JSON.parse(serialized);
    const values = report.analysis.coverage[name];
    while (values.length <= SCHEMA_LIMITS.coverage) values.push(name === 'available' || name === 'missing' ? `fixture-${values.length}` : structuredClone(issue));
    assert.throws(() => normalizeReport(report), new RegExp(`coverage\\.${name}.*limit`, 'i'));
  }
});

test('accepts backend-shaped enrichment fixtures and builds immutable deep copies', async () => {
  const expected = {
    upstream: ['upstream', 0, 2, 12, 0],
    cache: ['cache', 3, 0, 60000, 0],
    mixed: ['mixed', 2, 1, 42000, 0],
    failures: ['none', 0, 0, 0, 8]
  };
  for (const [name, values] of Object.entries(expected)) {
    const serialized = await readFile(new URL(`../testdata/enrichment-${name}-report.json`, import.meta.url), 'utf8');
    const source = JSON.parse(serialized);
    const normalized = normalizeReport(source).analysis.coverage.enrichment;
    assert.equal(normalized.length, 1);
    assert.deepEqual([
      normalized[0].source, normalized[0].cache_hits, normalized[0].upstream_fetches,
      normalized[0].max_age_ms, normalized[0].failures.length
    ], values);
    assert.ok(Object.isFrozen(normalized));
    assert.ok(Object.isFrozen(normalized[0]));
    assert.ok(Object.isFrozen(normalized[0].failures));
    for (const failure of normalized[0].failures) assert.ok(Object.isFrozen(failure));
    source.analysis.coverage.enrichment[0].source = 'none';
    source.analysis.coverage.enrichment[0].failures.splice(0);
    assert.equal(normalized[0].source, values[0]);
    assert.equal(normalized[0].failures.length, values[4]);
    assert.throws(() => { normalized[0].source = 'none'; }, TypeError);
    assert.throws(() => { normalized[0].failures.push({}); }, TypeError);
  }
});

test('accepts legacy coverage without enrichment and rejects every malformed enrichment shape', () => {
  const baseCoverage = () => structuredClone(analysis().coverage);
  assert.deepEqual(normalizeAnalysis(analysis()).coverage.enrichment, []);
  const entry = (overrides = {}) => ({ provider: 'geoip', source: 'mixed', cache_hits: 1, upstream_fetches: 1, max_age_ms: 1, failures: [], ...overrides });
  const reject = enrichment => assert.throws(
    () => normalizeAnalysis(analysis({ coverage: { ...baseCoverage(), enrichment } })),
    /analysis\.coverage\.enrichment|schema/i
  );

  for (const invalid of [null, {}, 'none', [entry(), entry()]]) reject(invalid);
  for (const key of ['provider', 'source', 'cache_hits', 'upstream_fetches', 'max_age_ms', 'failures']) {
    const missing = entry(); delete missing[key]; reject([missing]);
    reject([{ ...entry(), [key]: null }]);
  }
  reject([{ ...entry(), extra: 'SECRET-URL-IP-TARGET-ERROR' }]);
  reject([entry({ provider: 'ipwhois', target: undefined })]);
  reject([entry({ source: 'future' })]);
  for (const key of ['cache_hits', 'upstream_fetches', 'max_age_ms']) {
    reject([entry({ [key]: -1 })]); reject([entry({ [key]: 1.5 })]); reject([entry({ [key]: '1' })]);
  }
  reject([entry({ max_age_ms: 86400001 })]);
  reject([entry({ cache_hits: 6201, upstream_fetches: 0, source: 'cache' })]);
  reject([entry({ cache_hits: 6200, upstream_fetches: 0, source: 'cache', failures: [{ kind: 'timeout', count: 1, retryable: true }] })]);
  for (const contradictory of [
    entry({ source: 'none' }),
    entry({ source: 'cache' }),
    entry({ source: 'upstream' }),
    entry({ source: 'mixed', upstream_fetches: 0 })
  ]) reject([contradictory]);

  const failure = (kind, count = 1, retryable = ({ busy: true, cancelled: false, malformed: false, not_found: false, policy: false, rate_limited: true, timeout: true, unavailable: true })[kind]) => ({ kind, count, retryable });
  reject([entry({ failures: Array.from({ length: 9 }, () => failure('timeout')) })]);
  reject([entry({ failures: [failure('timeout'), failure('timeout')] })]);
  reject([entry({ failures: [failure('timeout'), failure('rate_limited')] })]);
  reject([entry({ failures: [failure('future')] })]);
  reject([entry({ failures: [failure('timeout', 0)] })]);
  reject([entry({ failures: [failure('timeout', 1, false)] })]);
  reject([entry({ failures: [{ ...failure('timeout'), error: 'SECRET' }] })]);
  reject([entry({ failures: [{ kind: 'timeout', count: 1 }] })]);
  reject([entry({ failures: [{ kind: 'timeout', count: 1, retryable: null }] })]);
});

test('normalizes serialized Go checker execution findings into privacy-safe generic text', async () => {
  const serialized = await readFile(new URL('../testdata/checker-execution-report.json', import.meta.url), 'utf8');
  const report = normalizeReport(JSON.parse(serialized));
  assert.deepEqual(report.analysis.findings.map(finding => [finding.code, finding.title, finding.summary]), [
    ['checker_panic', 'Checker execution failed', 'The checker stopped unexpectedly, so service health was not established.'],
    ['checker_capacity_unavailable', 'Checker capacity was unavailable', 'The bounded checker supervisor had no execution slot, so service health was not established.']
  ]);

  const hostile = JSON.parse(serialized);
  hostile.analysis.findings[0].title = 'private target and panic prose';
  hostile.analysis.findings[0].summary = 'Bearer secret-token';
  const safe = normalizeReport(hostile).analysis.findings[0];
  assert.equal(safe.title, 'Checker execution failed');
  assert.equal(safe.summary, 'The checker stopped unexpectedly, so service health was not established.');
  hostile.analysis.findings[0].code = 'checker_future_unknown';
  assert.throws(() => normalizeReport(hostile), /findings.*code|unsupported/i);
});

test('Web finding allowlist accepts every producer-fixture code and rejects closed-version mutations', async () => {
  const contract = await findingContractFixture();
  assert.equal(contract.schema, 'finding-contract-v1');
  assert.equal(contract.findings.length, 23);
  assert.deepEqual(contract.findings.map(item => item.code), [...contract.findings.map(item => item.code)].sort());

  for (const { code } of contract.findings) {
    const source = analysis({ findings: [{ ...analysis().findings[0], code }] });
    const normalized = normalizeAnalysis(source);
    assert.equal(normalized.findings[0].code, code);
    assert.deepEqual(normalized.findings[0].evidence_ids, ['e-1']);
    assert.deepEqual(normalized.findings[0].action_ids, ['a-1']);
  }

  const mutations = [
    'future_finding', '', null, 7,
    'x'.repeat(SCHEMA_LIMITS.string + 1),
    '<img src=x onerror=alert("HOSTILE_FINDING_CANARY")>'
  ];
  for (const code of mutations) {
    const report = legacyReport({ analysis: analysis({ findings: [{ ...analysis().findings[0], code }] }) });
    assert.throws(() => normalizeReport(report), /findings.*code|unsupported|schema/i);
    const parsed = parseResponse(response(200), JSON.stringify(report), 0);
    assert.equal(parsed.ok, false);
    assert.equal(parsed.error.code, 'invalid_response');
    assert.doesNotMatch(JSON.stringify(parsed), /future_finding|HOSTILE_FINDING_CANARY/);
  }
});

test('healthy DNS with a known connection failure rejects atomically without retaining hostile prose', () => {
  const report = legacyReport({ results: [result({
    status: 'healthy',
    error_code: 'connection_failed',
    message: 'HOSTILE_RESULT_PROSE_CANARY'
  })] });
  assert.throws(() => normalizeReport(report), /result|status|error|schema/i);

  const parsed = parseResponse(response(200), JSON.stringify(report), 0);
  assert.equal(parsed.ok, false);
  assert.equal(parsed.error.code, 'invalid_response');
  assert.doesNotMatch(JSON.stringify(parsed), /HOSTILE_RESULT_PROSE_CANARY/);
});

test('Web result parser consumes all 136 producer-fixture matrix rows repeatedly', async () => {
  const contract = await findingContractFixture();
  const rows = contract.result_shapes.flatMap(shape => shape.kinds.map(kind => ({ shape, kind })));
  assert.equal(contract.result_shapes.length, 31);
  assert.equal(contract.result_matrix_rows, 136);
  assert.equal(rows.length, contract.result_matrix_rows);
  assert.deepEqual(contract.verification_scopes, ['server_greeting']);
  for (let repetition = 0; repetition < 100; repetition++) {
    for (const { shape, kind } of rows) {
      const source = resultShapeReport(shape, kind);
      assert.equal(fixtureAllowsResult(contract, source.results[0]), true, `${shape.name}/${kind}`);
      const normalized = normalizeReport(source);
      assert.equal(normalized.results[0].kind, kind);
      assert.equal(normalized.results[0].status, shape.status);
      assert.equal(normalized.results[0].error_code, shape.error_code ?? '');
      const expectedDetails = resultDetailsForShape(shape);
      if (expectedDetails === undefined) {
        assert.equal(Object.hasOwn(normalized.results[0], 'details'), false, `${shape.name}/${kind}`);
      } else {
        assert.deepEqual(Object.keys(normalized.results[0].details).sort(), Object.keys(expectedDetails).sort(), `${shape.name}/${kind}`);
      }
    }
  }
});

test('all eight producer traceroute witnesses normalize with only completed attempts projected', async () => {
  const fixtureNames = [
    'traceroute-timeout-full-report.json',
    'traceroute-timeout-compact-report.json',
    'traceroute-command-failure-full-report.json',
    'traceroute-command-failure-compact-report.json',
    'traceroute-failed-reached-parse-full-report.json',
    'traceroute-failed-reached-parse-compact-report.json',
    'traceroute-mixed-completed-failed-full-report.json',
    'traceroute-mixed-completed-failed-compact-report.json'
  ];
  const normalized = new Map();
  for (const fixtureName of fixtureNames) {
    const serialized = await readFile(new URL(`../testdata/${fixtureName}`, import.meta.url), 'utf8');
    assert.equal(serialized.endsWith('\n'), true, fixtureName);
    normalized.set(fixtureName, normalizeReport(JSON.parse(serialized)));
  }
  assert.equal(normalized.size, 8);

  for (const fixtureName of ['traceroute-timeout-full-report.json', 'traceroute-command-failure-full-report.json']) {
    const trace = normalized.get(fixtureName).results[0];
    assert.equal(Object.hasOwn(trace.details, 'topology'), true, fixtureName);
    assert.equal(trace.details.topology.reached, false, fixtureName);
    assert.deepEqual(trace.details.attempts.map(attempt => attempt.attempt), [1, 2], fixtureName);
  }

  const failedReachedFull = normalized.get('traceroute-failed-reached-parse-full-report.json');
  const failedReachedCompact = normalized.get('traceroute-failed-reached-parse-compact-report.json');
  assert.equal(failedReachedFull.results[0].details.attempts[0].topology.reached, true);
  assert.deepEqual(failedReachedCompact.compact_topology.routes, []);
  assert.deepEqual(failedReachedCompact.compact_topology.stats.routes, {
    total: 0, displayed: 0, complete: 0, partial: 0, omitted: 0
  });

  for (const stem of ['traceroute-timeout', 'traceroute-command-failure']) {
    const compact = normalized.get(`${stem}-compact-report.json`);
    assert.deepEqual(compact.compact_topology.routes.map(route => route.attempt), [1], stem);
    assert.equal(compact.results[0].details.attempts_execution_failed, 1, stem);
  }

  const mixedFull = normalized.get('traceroute-mixed-completed-failed-full-report.json');
  const mixedCompact = normalized.get('traceroute-mixed-completed-failed-compact-report.json');
  const completedAttempts = mixedFull.results[0].details.attempts
    .filter(attempt => !attempt.error_code)
    .map(attempt => attempt.attempt);
  assert.deepEqual(completedAttempts, [1]);
  assert.deepEqual(mixedCompact.compact_topology.routes.map(route => route.attempt), completedAttempts);
  assert.equal(mixedCompact.compact_topology.stats.routes.total, completedAttempts.length);
  assert.equal(mixedCompact.compact_topology.result_stats[0].routes.total, completedAttempts.length);
});

test('fixture-derived known-shape mutation corpus rejects every incompatible semantic tuple without reflection', async () => {
  const contract = await findingContractFixture();
  const mutations = fixtureDerivedResultMutationCorpus(contract);
  const mutationNames = mutations.map(({ name }) => name);
  assert.equal(new Set(mutationNames).size, mutations.length, 'mutation names must uniquely identify fixture-derived coverage');
  assert.equal(mutations.length, 456);
  assert.deepEqual(Object.fromEntries(['status', 'kind', 'error_code', 'details'].map(dimension => [
    dimension, mutations.filter(mutation => mutation.dimension === dimension).length
  ])), { status: 136, kind: 58, error_code: 136, details: 126 });
  assert.deepEqual([...new Set(mutations.map(({ dimension }) => dimension))].sort(), ['details', 'error_code', 'kind', 'status']);

  const rejected = [];
  for (const { name, report } of mutations) {
    assert.equal(semanticCompatibilityAllowsResult(contract, report.results[0]), false, `${name}: compatibility precondition`);
    report.results[0].message = 'HOSTILE_RESULT_PROSE_CANARY';
    assert.throws(() => normalizeReport(report), /result|details|status|kind|error|schema|timestamp/i, name);
    const parsed = parseResponse(response(200), JSON.stringify(report), 0);
    assert.equal(parsed.ok, false, name);
    assert.equal(parsed.error.code, 'invalid_response', name);
    assert.doesNotMatch(JSON.stringify(parsed), /HOSTILE_RESULT_PROSE_CANARY/, name);
    rejected.push(name);
  }
  assert.deepEqual(rejected, mutationNames, 'every generated mutation must be asserted rejected');

  const cancelledDetailMutations = mutations.filter(mutation =>
    mutation.dimension === 'details' && mutation.name.startsWith('cancelled/'));
  assert.equal(cancelledDetailMutations.length, 13, 'cancelled details must reject for every fixture kind');

  for (const code of ['connection_failed', 'network_policy_blocked', 'checker_panic', 'checker_capacity_unavailable']) {
    for (const kind of ['dns', 'tcp']) {
      const shape = contract.result_shapes.find(candidate => candidate.name === code);
      const report = resultShapeReport(shape, kind);
      report.results[0].details = { witness: `${code}/${kind}` };
      const normalized = normalizeReport(report);
      assert.deepEqual(normalized.results[0].details, { witness: `${code}/${kind}` });
    }
  }
});

test('HTTP and service no-detail outcomes follow the producer fixture instead of a Cartesian allowlist', async () => {
  const contract = await findingContractFixture();
  const rows = contract.result_shapes.flatMap(shape => shape.kinds
    .filter(kind => kind === 'http' || kind === 'https' || ['imap', 'imaps', 'pop3', 'pop3s', 'smtp', 'smtps', 'ssh', 'submission'].includes(kind))
    .filter(() => shape.required_details.length === 0 && shape.optional_details.length === 0)
    .map(kind => ({ shape, kind })));
  assert.ok(rows.length > 0);
  for (const { shape, kind } of rows) {
    const report = resultShapeReport(shape, kind);
    assert.equal(fixtureAllowsResult(contract, report.results[0]), true, `${shape.name}/${kind}`);
    assert.equal(normalizeReport(report).results[0].error_code, shape.error_code, `${shape.name}/${kind}`);
  }
});

test('unknown TLS error codes reject the whole report without reflecting the hostile canary', async () => {
  const contract = await findingContractFixture();
  const shapes = contract.result_shapes.filter(shape => shape.name.startsWith('tls_'));
  assert.equal(shapes.length, 6);
  for (const shape of shapes) {
    const report = resultShapeReport(shape);
    report.results[0].error_code = 'HOSTILE_ERROR_CODE_CANARY';
    assert.throws(() => normalizeReport(report), /error|result|schema/i, shape.name);
    const parsed = parseResponse(response(200), JSON.stringify(report), 0);
    assert.equal(parsed.ok, false, shape.name);
    assert.doesNotMatch(JSON.stringify(parsed), /HOSTILE_ERROR_CODE_CANARY/, shape.name);
  }
});

test('compact-v1 rejects unknown fields at every compact object boundary', () => {
  const boundaries = [
    ['root', value => value],
    ['limits', value => value.limits],
    ['node', value => value.nodes[0]],
    ['link', value => value.links[0]],
    ['route', value => value.routes[0]],
    ['stats', value => value.stats],
    ['count stats', value => value.stats.nodes],
    ['route stats', value => value.stats.routes],
    ['result stats', value => value.result_stats[0]],
    ['geo stats', value => value.geo],
    ['Geo', value => value.nodes[1].geolocation],
    ['ASN', value => value.nodes[1].asn]
  ];
  for (const [name, select] of boundaries) {
    const value = structuredClone(compactTopology());
    select(value).unknown_canary = 'HOSTILE_COMPACT_CANARY';
    assert.throws(() => normalizeCompactTopology(value), /exact|field|schema|compact/i, name);
  }
});

test('compact-v1 required-field mutation matrix rejects missing and null at every structural object', () => {
  const objects = [
    ['root', value => value, ['schema', 'selection', 'limits', 'nodes', 'links', 'routes', 'stats', 'result_stats', 'geo', 'truncated']],
    ['limits', value => value.limits, ['nodes', 'links', 'max_response_bytes_exclusive', 'max_geo_bundle_bytes']],
    ['node', value => value.nodes[0], ['id', 'kind', 'status', 'hop_min', 'hop_max', 'observations']],
    ['link', value => value.links[0], ['from', 'to', 'status', 'observations']],
    ['route', value => value.routes[0], ['result_index', 'attempt', 'status', 'reached', 'complete', 'node_ids']],
    ['stats', value => value.stats, ['nodes', 'links', 'routes', 'node_observations', 'link_observations']],
    ['count stats', value => value.stats.nodes, ['total', 'displayed', 'omitted']],
    ['route stats', value => value.stats.routes, ['total', 'displayed', 'complete', 'partial', 'omitted']],
    ['result stats', value => value.result_stats[0], ['result_index', 'routes', 'node_observations', 'link_observations']],
    ['result count stats', value => value.result_stats[0].node_observations, ['total', 'displayed', 'omitted']],
    ['result route stats', value => value.result_stats[0].routes, ['total', 'displayed', 'complete', 'partial', 'omitted']],
    ['geo stats', value => value.geo, ['eligible', 'available', 'included', 'omitted', 'unavailable']],
    ['geolocation', value => value.nodes[1].geolocation, ['latitude', 'longitude']]
  ];
  let mutations = 0;
  for (const [objectName, select, required] of objects) {
    for (const key of required) {
      for (const replacement of ['missing', null]) {
        const value = structuredClone(compactTopology());
        const target = select(value);
        if (replacement === 'missing') delete target[key];
        else target[key] = null;
        assert.throws(
          () => normalizeCompactTopology(value),
          /schema|field|object|array|string|boolean|integer|number|bounded|compact/i,
          `${objectName}.${key}: ${replacement}`
        );
        mutations++;
      }
    }
  }
  assert.equal(mutations, objects.reduce((total, [, , fields]) => total + fields.length * 2, 0));
});

test('compact-v1 requires own enumerable data fields without invoking inherited mandatory getters', () => {
  const value = structuredClone(compactTopology());
  delete value.schema;
  const saved = Object.getOwnPropertyDescriptor(Object.prototype, 'schema');
  let invoked = 0;
  try {
    Object.defineProperty(Object.prototype, 'schema', {
      configurable: true,
      get() { invoked++; return 'compact-v1'; }
    });
    assert.throws(() => normalizeCompactTopology(value), /schema|own|field/i);
    assert.equal(invoked, 0);
  } finally {
    if (saved) Object.defineProperty(Object.prototype, 'schema', saved);
    else delete Object.prototype.schema;
  }

  const nonEnumerable = structuredClone(compactTopology());
  Object.defineProperty(nonEnumerable.limits, 'nodes', { value: 500, enumerable: false });
  assert.throws(() => normalizeCompactTopology(nonEnumerable), /nodes|field|schema/i);
});

test('accepts the three exact compact producer fixtures, rejects empty ASN, and requires array presence', async () => {
  for (const name of ['compact-empty-asn-report.json', 'compact-zero-node-attempt-report.json', 'compact-zero-route-report.json']) {
    const serialized = await readFile(new URL(`../testdata/${name}`, import.meta.url), 'utf8');
    assert.doesNotThrow(() => normalizeReport(JSON.parse(serialized)), name);
  }

  const emptyASN = structuredClone(compactTopology());
  emptyASN.nodes[1].asn = {};
  delete emptyASN.nodes[1].geolocation;
  emptyASN.geo = { eligible: 1, available: 1, included: 1, omitted: 0, unavailable: 0 };
  assert.throws(() => normalizeCompactTopology(emptyASN), /asn|canonical|schema/i);

  for (const key of ['nodes', 'links', 'routes', 'result_stats']) {
    for (const replacement of [null, undefined]) {
      const missing = structuredClone(compactTopology());
      if (replacement === undefined) delete missing[key];
      else missing[key] = replacement;
      assert.throws(() => normalizeCompactTopology(missing), new RegExp(key, 'i'));
    }
  }
  for (const replacement of [null, undefined]) {
    const invalidReasons = structuredClone(compactTopology());
    invalidReasons.truncation_reasons = replacement;
    assert.throws(() => normalizeCompactTopology(invalidReasons), /truncation_reasons|array|schema/i);
  }
});

test('compact optional fields accept absence but reject present null and undefined', () => {
  const cases = [
    ['truncation_reasons', value => value, 'truncation_reasons'],
    ['node address', value => value.nodes[0], 'address'],
    ['node latency', value => value.nodes[1], 'latency_ms_avg'],
    ['node public IP', value => value.nodes[0], 'public_ip'],
    ['node geolocation', value => value.nodes[1], 'geolocation'],
    ['node ASN', value => value.nodes[1], 'asn'],
    ['Geo city', value => value.nodes[1].geolocation, 'city'],
    ['Geo region', value => value.nodes[1].geolocation, 'region'],
    ['Geo country', value => value.nodes[1].geolocation, 'country'],
    ['Geo country code', value => value.nodes[1].geolocation, 'country_code'],
    ['ASN number', value => value.nodes[1].asn, 'number'],
    ['ASN organization', value => value.nodes[1].asn, 'organization']
  ];
  for (const [name, target, key] of cases) {
    const absent = structuredClone(compactTopology());
    delete target(absent)[key];
    assert.doesNotThrow(() => normalizeCompactTopology(absent), `${name}: absent`);
    for (const malformed of [null, undefined]) {
      const present = structuredClone(compactTopology());
      target(present)[key] = malformed;
      assert.throws(() => normalizeCompactTopology(present), /schema|compact|array|string|number|boolean|object/i, `${name}: ${malformed}`);
    }
  }

  const absentTopology = legacyReport();
  assert.doesNotThrow(() => normalizeReport(absentTopology));
  for (const malformed of [null, undefined]) {
    const presentTopology = legacyReport();
    presentTopology.compact_topology = malformed;
    assert.throws(() => normalizeReport(presentTopology), /compact_topology|schema/i);
  }
});

test('compact omitempty fields accept absence and reject every present noncanonical zero value', () => {
  const cases = [
    ['node address', value => value.nodes[0], 'address', ''],
    ['node public IP', value => value.nodes[0], 'public_ip', false],
    ['Geo city', value => value.nodes[1].geolocation, 'city', ''],
    ['Geo region', value => value.nodes[1].geolocation, 'region', ''],
    ['Geo country', value => value.nodes[1].geolocation, 'country', ''],
    ['Geo country code', value => value.nodes[1].geolocation, 'country_code', ''],
    ['ASN number', value => value.nodes[1].asn, 'number', 0],
    ['ASN organization', value => value.nodes[1].asn, 'organization', '']
  ];
  for (const [name, target, key, zero] of cases) {
    const absent = structuredClone(compactTopology());
    delete target(absent)[key];
    assert.doesNotThrow(() => normalizeCompactTopology(absent), `${name}: absent`);
    const present = structuredClone(compactTopology());
    target(present)[key] = zero;
    assert.throws(() => normalizeCompactTopology(present), /canonical|empty|zero|public_ip|schema/i, `${name}: present zero`);
  }

  const absentReasons = structuredClone(compactTopology());
  delete absentReasons.truncation_reasons;
  assert.doesNotThrow(() => normalizeCompactTopology(absentReasons));
  const emptyReasons = structuredClone(compactTopology());
  emptyReasons.truncation_reasons = [];
  assert.throws(() => normalizeCompactTopology(emptyReasons), /truncation_reasons|canonical|empty|schema/i);
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

test('client timeout is 15 seconds beyond the longest target and capped at 315 seconds', () => {
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'dns' }], timeout_ms: 5000 }), 20000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute' }], timeout_ms: 5000 }), 40000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 3 }], timeout_ms: 2000 }), 21000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 10 }], timeout_ms: 29999 }), 314990);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 10 }], timeout_ms: 30000 }), 315000);
  assert.equal(clientTimeoutMS({ targets: [{ kind: 'traceroute', attempts: 999 }], timeout_ms: Number.MAX_SAFE_INTEGER }), 315000);
});

test('Retry-After accepts only one canonical bounded ASCII delta-seconds value', async () => {
  const now = Date.parse('2026-09-01T12:00:00Z');
  const contract = await apiErrorContractFixture();
  assert.equal(contract.retry_after_cases.length, 13);
  for (const fixture of contract.retry_after_cases) {
    assert.equal(
      parseRetryAfter(fixture.header, now),
      fixture.valid ? now + fixture.seconds * 1000 : null,
      fixture.name
    );
  }
  for (const malformed of ['', '12x', '１２', '1\t', '1\n', '1,2', '1, 1']) {
    assert.equal(parseRetryAfter(malformed, now), null, JSON.stringify(malformed));
  }
});

test('request errors distinguish network, timeout, and caller cancellation', () => {
  assert.equal(normalizeRequestError(new TypeError('fetch failed')).code, 'network_error');
  assert.equal(normalizeRequestError({ name: 'AbortError', reason: 'timeout' }).kind, 'timeout');
  assert.equal(normalizeRequestError({ name: 'AbortError', reason: 'user' }).kind, 'cancelled');
  assert.equal(normalizeRequestError({ name: 'TimeoutError' }).kind, 'timeout');
});

test('parseResponse classifies every untyped HTTP failure as one fixed invalid server response', () => {
  const expected = {
    kind: 'invalid-response', code: 'invalid_server_response', status: 500,
    message: 'The server returned an invalid error response.', retryable: false
  };
  for (const body of ['<html>proxy secret</html>', '', '{oops', JSON.stringify({ error: null })]) {
    const parsed = parseResponse(response(500, { 'Retry-After': '30' }), body, 0);
    assert.deepEqual(parsed.error, expected);
    assert.equal(Object.hasOwn(parsed.error, 'retryAt'), false);
    assert.doesNotMatch(JSON.stringify(parsed), /proxy secret|Retry-After/i);
  }
  assert.equal(parseResponse(response(200), '', 0).error.code, 'invalid_response');
  assert.equal(parseResponse(response(200), '{oops', 0).error.code, 'invalid_response');
});

test('parseResponse consumes every exact producer API error body with local policy only', async () => {
  const now = Date.parse('2026-09-01T12:00:00Z');
  const contract = await apiErrorContractFixture();
  assert.equal(contract.errors.length, 16);
  const exercised = [];
  for (const fixture of contract.errors) {
    const parsed = parseResponse(
      response(fixture.status, { 'Retry-After': '30' }),
      fixture.body,
      now
    );
    assert.equal(parsed.ok, false);
    assert.equal(parsed.error.kind, 'http', fixture.key);
    assert.equal(parsed.error.code, fixture.code, fixture.key);
    assert.equal(parsed.error.status, fixture.status, fixture.key);
    assert.equal(parsed.error.message, fixture.message, fixture.key);
    assert.equal(parsed.error.retryable, fixture.retryable, fixture.key);
    assert.equal(Object.hasOwn(parsed.error, 'retryAt'), fixture.retry_after, fixture.key);
    if (fixture.retry_after) assert.equal(parsed.error.retryAt, now + 30000, fixture.key);
    assert.equal(fixture.body.endsWith('\n'), true, fixture.key);
    exercised.push(fixture.key);
  }
  assert.deepEqual(exercised, contract.errors.map(fixture => fixture.key));
});

test('API error mutation corpus has one identical invalid-server-response outcome', async () => {
  const contract = await apiErrorContractFixture();
  const expectedFor = status => ({
    kind: 'invalid-response', code: 'invalid_server_response', status,
    message: 'The server returned an invalid error response.', retryable: false
  });
  assert.equal(contract.schema, 'api-error-wire-v1');
  assert.deepEqual(contract.limits, {
    body_utf16_units: 65536, depth: 2, tokens: 10, properties: 3, string_utf16_units: 128
  });
  assert.equal(contract.structural_mutations.length, 35 + contract.errors.length * 3 + 2);
  assert.equal(contract.structural_mutations.length, 85);
  assert.equal(new Set(contract.structural_mutations.map(mutation => mutation.name)).size, contract.structural_mutations.length);
  for (const mutation of contract.structural_mutations) {
    assert.equal(mutation.valid, false, mutation.name);
    const parsed = parseResponse(
      response(mutation.status, { 'Retry-After': '30' }),
      mutation.body,
      0
    );
    assert.deepEqual(parsed.error, expectedFor(mutation.status), mutation.name);
    assert.equal(Object.hasOwn(parsed.error, 'retryAt'), false, mutation.name);
    assert.doesNotMatch(JSON.stringify(parsed), /HOSTILE|server_busy|rate_limited|valid API credentials/, mutation.name);
  }
});

test('Retry-After fixture corpus applies only to registry rows that permit it', async () => {
  const now = Date.parse('2026-09-01T12:00:00Z');
  const contract = await apiErrorContractFixture();
  for (const fixture of contract.errors) {
    for (const retryCase of contract.retry_after_cases) {
      const headers = retryCase.header === null ? {} : { 'Retry-After': retryCase.header };
      const parsed = parseResponse(
        response(fixture.status, headers),
        fixture.body,
        now
      );
      const applicable = fixture.retry_after && retryCase.valid;
      assert.equal(Object.hasOwn(parsed.error, 'retryAt'), applicable, `${fixture.key}/${retryCase.name}`);
      if (applicable) assert.equal(parsed.error.retryAt, now + retryCase.seconds * 1000, `${fixture.key}/${retryCase.name}`);
      assert.equal(parsed.error.code, fixture.code, `${fixture.key}/${retryCase.name}`);
      assert.equal(parsed.error.retryable, fixture.retryable, `${fixture.key}/${retryCase.name}`);
    }
  }
});

test('Retry-After handles canonicalized and combined values exposed by real Web Headers', () => {
  for (const [raw, exposed, retryAt] of [[' 1 ', '1', 2000], ['3600', '3600', 3601000], ['0', '0', undefined], ['3601', '3601', undefined]]) {
    const headers = new Headers({ 'Retry-After': raw });
    assert.equal(headers.get('Retry-After'), exposed);
    const parsed = parseResponse(
      { status: 429, ok: false, headers },
      '{"error":{"code":"rate_limited","message":"per-client request limit exceeded"}}\n',
      1000
    );
    assert.equal(parsed.error.retryAt, retryAt, raw);
  }

  const headers = new Headers();
  headers.append('Retry-After', '1');
  headers.append('Retry-After', '2');
  assert.match(headers.get('Retry-After'), /,/);
  const parsed = parseResponse(
    { status: 429, ok: false, headers },
    '{"error":{"code":"rate_limited","message":"per-client request limit exceeded"}}\n',
    1000
  );
  assert.equal(parsed.error.code, 'rate_limited');
  assert.equal(Object.hasOwn(parsed.error, 'retryAt'), false);
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
  const normalized = normalizeReport(legacyReport({
    results: [result({ kind: 'traceroute', details: { attempts_total: 1 } })], compact_topology: source
  }));
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

  const optionalSource = compactTopology();
  delete optionalSource.nodes[0].address;
  delete optionalSource.nodes[1].geolocation.city;
  delete optionalSource.nodes[1].geolocation.region;
  delete optionalSource.nodes[1].asn.organization;

  const once = normalizeCompactTopology(optionalSource);
  assert.equal(Object.hasOwn(once, 'truncation_reasons'), false);
  assert.equal(Object.hasOwn(once.nodes[0], 'address'), false);
  assert.equal(Object.hasOwn(once.nodes[1].geolocation, 'city'), false);
  assert.equal(Object.hasOwn(once.nodes[1].geolocation, 'region'), false);
  assert.equal(Object.hasOwn(once.nodes[1].asn, 'organization'), false);
  assert.deepEqual(normalizeCompactTopology(once), once);
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

test('compact Geo metadata requires literal public_ip true for Geo and ASN', () => {
  for (const publicIP of [undefined, false]) {
    for (const metadata of [
      { geolocation: { city: 'Seoul', region: '', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 } },
      { asn: { number: 64500, organization: 'Example' } }
    ]) {
      const value = structuredClone(compactTopology());
      const node = { ...value.nodes[1], ...metadata };
      delete node.geolocation;
      delete node.asn;
      Object.assign(node, metadata);
      if (publicIP === undefined) delete node.public_ip;
      else node.public_ip = publicIP;
      value.nodes[1] = node;
      value.geo = { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 };
      assert.throws(() => normalizeCompactTopology(value), /public_ip/i, `${publicIP}/${Object.keys(metadata)[0]}`);
    }
  }
});

test('compact Geo bundle accepts exactly 4096 ASCII bytes and rejects 4097', () => {
  for (const [organizationLength, accepted] of [[4059, true], [4060, false]]) {
    const value = structuredClone(compactTopology());
    delete value.nodes[1].geolocation;
    value.nodes[1].asn = { number: 1, organization: 'x'.repeat(organizationLength) };
    if (accepted) assert.equal(normalizeCompactTopology(value).nodes[1].asn.organization.length, organizationLength);
    else assert.throws(() => normalizeCompactTopology(value), /Geo bundle|byte limit/i);
  }
});

test('compact Geo bundle counts multibyte Korean UTF-8 bytes at the exact boundary', () => {
  for (const [organization, accepted] of [
    ['한'.repeat(1353), true],
    [`${'한'.repeat(1353)}x`, false]
  ]) {
    const value = structuredClone(compactTopology());
    delete value.nodes[1].geolocation;
    value.nodes[1].asn = { number: 1, organization };
    if (accepted) assert.equal(normalizeCompactTopology(value).nodes[1].asn.organization, organization);
    else assert.throws(() => normalizeCompactTopology(value), /Geo bundle|byte limit/i);
  }
});

test('compact Geo bundle aggregates mixed geolocation and ASN fields', () => {
  const exact = structuredClone(compactTopology());
  exact.nodes[1].asn.organization = 'x'.repeat(3940);
  assert.doesNotThrow(() => normalizeCompactTopology(exact));

  const over = structuredClone(exact);
  over.nodes[1].asn.organization += 'x';
  assert.throws(() => normalizeCompactTopology(over), /Geo bundle|byte limit/i);
});

test('compact Geo budget is per included node and Geo counts remain aggregate', () => {
  const value = structuredClone(compactTopology());
  const extra = Array.from({ length: 40 }, (_, index) => ({
    id: `geo${index}`, kind: 'ip', address: `198.51.100.${index + 1}`, status: 'healthy',
    hop_min: 1, hop_max: 1, observations: 1, public_ip: true,
    asn: { number: index + 1, organization: 'x'.repeat(4000) }
  }));
  value.nodes.push(...extra);
  value.stats.nodes = { total: 42, displayed: 42, omitted: 0 };
  value.geo = { eligible: 41, available: 41, included: 41, omitted: 0, unavailable: 0 };
  const normalized = normalizeCompactTopology(value);
  assert.equal(normalized.nodes.length, 42);
  assert.deepEqual(normalized.geo, value.geo);
});

test('compact Geo byte validation rejects accessors without invoking canaries', () => {
  for (const field of ['geolocation', 'asn']) {
    const value = structuredClone(compactTopology());
    let invoked = false;
    Object.defineProperty(value.nodes[1][field], 'toJSON', {
      get() { invoked = true; throw new Error('canary invoked'); }
    });
    assert.throws(() => normalizeCompactTopology(value), /accessor|schema/i);
    assert.equal(invoked, false, field);
  }
});

test('compact Geo bundle applies Go HTML escaping at the exact 4096 byte boundary', () => {
  for (const html of ['<', '>', '&']) {
    for (const [suffix, accepted] of [['xxx', true], ['xxxx', false]]) {
      const value = structuredClone(compactTopology());
      delete value.nodes[1].geolocation;
      value.nodes[1].asn = { number: 1, organization: html.repeat(676) + suffix };
      if (accepted) assert.doesNotThrow(() => normalizeCompactTopology(value), `${html} exact 4096`);
      else assert.throws(() => normalizeCompactTopology(value), /Geo bundle|byte limit/i, `${html} 4097`);
    }
  }
});

test('compact Geo bundle applies Go U+2028 and U+2029 escaping at the exact boundary', () => {
  for (const separator of ['\u2028', '\u2029']) {
    for (const [suffix, accepted] of [['xxx', true], ['xxxx', false]]) {
      const value = structuredClone(compactTopology());
      delete value.nodes[1].geolocation;
      value.nodes[1].asn = { number: 1, organization: separator.repeat(676) + suffix };
      if (accepted) assert.doesNotThrow(() => normalizeCompactTopology(value));
      else assert.throws(() => normalizeCompactTopology(value), /Geo bundle|byte limit/i);
    }
  }
});

test('producer exact Geo boundary fixture is accepted byte-for-byte and named invalid-wire 4097 rejects', async () => {
  const serialized = await readFile(new URL('../testdata/compact-geo-exact-boundary-report.json', import.meta.url), 'utf8');
  assert.equal(serialized.endsWith('\n'), true);
  for (const escaped of ['\\u003c', '\\u003e', '\\u0026', '\\u2028', '\\u2029']) {
    assert.equal(serialized.split(escaped).length - 1, 135, escaped);
  }
  const exact = JSON.parse(serialized);
  const organization = exact.compact_topology.nodes[1].asn.organization;
  assert.equal(organization, '<>&\u2028\u2029'.repeat(135) + 'boundary!');
  assert.equal(normalizeReport(exact).compact_topology.nodes[1].asn.organization, organization);

  const invalidWire4097 = structuredClone(exact);
  invalidWire4097.compact_topology.nodes[1].asn.organization += 'x';
  assert.throws(() => normalizeReport(invalidWire4097), /Geo bundle|byte limit/i, 'invalid-wire-geo-bundle-4097');
});

test('compact Geo strings reject lone UTF-16 surrogates but accept valid scalar pairs', () => {
  for (const malformed of ['\ud800', '\udfff', `ok\ud800x`, `ok\udfffx`]) {
    const value = structuredClone(compactTopology());
    value.nodes[1].asn.organization = malformed;
    assert.throws(() => normalizeCompactTopology(value), /Geo bundle|surrogate|UTF-8|schema/i);
  }
  const valid = structuredClone(compactTopology());
  valid.nodes[1].asn.organization = '😀';
  assert.equal(normalizeCompactTopology(valid).nodes[1].asn.organization, '😀');
});

test('compact Geo bundle counts Go quote, backslash, and control escapes exactly', () => {
  for (const escaped of ['"', '\\', '\b', '\t', '\n', '\f', '\r']) {
    const exact = structuredClone(compactTopology());
    delete exact.nodes[1].geolocation;
    exact.nodes[1].asn = { number: 1, organization: escaped.repeat(2000) + 'x'.repeat(59) };
    assert.doesNotThrow(() => normalizeCompactTopology(exact));
    exact.nodes[1].asn.organization += 'x';
    assert.throws(() => normalizeCompactTopology(exact), /Geo bundle|byte limit/i);
  }
  const genericControl = structuredClone(compactTopology());
  delete genericControl.nodes[1].geolocation;
  genericControl.nodes[1].asn = { number: 1, organization: '\u0001'.repeat(600) + 'x'.repeat(459) };
  assert.doesNotThrow(() => normalizeCompactTopology(genericControl));
  genericControl.nodes[1].asn.organization += 'x';
  assert.throws(() => normalizeCompactTopology(genericControl), /Geo bundle|byte limit/i);
});

test('compact Geo strings enforce the Go 4096 raw UTF-8 byte ceiling', () => {
  const atRawLimit = structuredClone(compactTopology());
  delete atRawLimit.nodes[1].geolocation;
  atRawLimit.nodes[1].asn = { number: 1, organization: 'é'.repeat(2048) };
  assert.throws(() => normalizeCompactTopology(atRawLimit), /Geo bundle|byte limit/i);

  const overRawLimit = structuredClone(atRawLimit);
  overRawLimit.nodes[1].asn.organization += 'é';
  assert.throws(() => normalizeCompactTopology(overRawLimit), /Geo string exceeds byte limit/i);
});

test('compact Geo counting invokes no inherited Object conversion hook', () => {
  const names = ['toJSON', 'toString', 'valueOf', Symbol.toPrimitive];
  const saved = names.map(name => [name, Object.getOwnPropertyDescriptor(Object.prototype, name)]);
  let invoked = 0;
  try {
    for (const name of names) {
      Object.defineProperty(Object.prototype, name, {
        configurable: true,
        writable: true,
        value() { invoked++; throw new Error('inherited conversion canary invoked'); }
      });
    }
    assert.doesNotThrow(() => normalizeCompactTopology(compactTopology()));
    assert.equal(invoked, 0);
  } finally {
    for (const [name, descriptor] of saved) {
      if (descriptor) Object.defineProperty(Object.prototype, name, descriptor);
      else delete Object.prototype[name];
    }
  }
});

test('compact Geo closure rejects own conversion hooks without invoking them', () => {
  const value = structuredClone(compactTopology());
  let invoked = 0;
  const canary = () => { invoked++; throw new Error('own conversion canary invoked'); };
  for (const bundle of [value.nodes[1].geolocation, value.nodes[1].asn]) {
    for (const name of ['toJSON', 'toString', 'valueOf', Symbol.toPrimitive]) {
      Object.defineProperty(bundle, name, { configurable: true, enumerable: true, value: canary });
    }
  }
  assert.throws(() => normalizeCompactTopology(value), /exact|field|schema/i);
  assert.equal(invoked, 0);
});

test('compact Geo normalization performs no inherited optional-field accessor lookup', () => {
  const cases = [
    ['truncation_reasons', value => delete value.truncation_reasons],
    ['address', value => delete value.nodes[0].address],
    ['latency_ms_avg', value => delete value.nodes[1].latency_ms_avg],
    ['public_ip', () => {}],
    ['geolocation', value => delete value.nodes[1].geolocation],
    ['asn', value => delete value.nodes[1].asn],
    ['city', value => delete value.nodes[1].geolocation.city],
    ['region', value => delete value.nodes[1].geolocation.region],
    ['country', value => delete value.nodes[1].geolocation.country],
    ['country_code', value => delete value.nodes[1].geolocation.country_code],
    ['number', value => delete value.nodes[1].asn.number],
    ['organization', value => delete value.nodes[1].asn.organization]
  ];
  const saved = cases.map(([name]) => [name, Object.getOwnPropertyDescriptor(Object.prototype, name)]);
  let invoked = 0;
  try {
    for (const [name, removeOwnField] of cases) {
      const value = structuredClone(compactTopology());
      removeOwnField(value);
      Object.defineProperty(Object.prototype, name, {
        configurable: true,
        get() { invoked++; throw new Error('inherited accessor canary invoked'); }
      });
      assert.doesNotThrow(() => normalizeCompactTopology(value), name);
      delete Object.prototype[name];
    }
    assert.equal(invoked, 0);
  } finally {
    for (const [name, descriptor] of saved) {
      if (descriptor) Object.defineProperty(Object.prototype, name, descriptor);
      else delete Object.prototype[name];
    }
  }
});

test('compact metadata validation does not invoke inherited public_ip accessors', () => {
  const value = structuredClone(compactTopology());
  delete value.nodes[1].public_ip;
  const saved = Object.getOwnPropertyDescriptor(Object.prototype, 'public_ip');
  let invoked = 0;
  try {
    Object.defineProperty(Object.prototype, 'public_ip', {
      configurable: true,
      get() { invoked++; throw new Error('inherited public_ip accessor invoked'); }
    });
    assert.throws(() => normalizeCompactTopology(value), /public_ip|schema/i);
    assert.equal(invoked, 0);
  } finally {
    if (saved) Object.defineProperty(Object.prototype, 'public_ip', saved);
    else delete Object.prototype.public_ip;
  }
});

test('compact Geo counting matches Go omitempty and number rendering fixtures', () => {
  const cases = [
    { geolocation: { latitude: -0, longitude: 1e-7 }, target: 'city', expectedPadding: 4039 },
    { geolocation: { latitude: 1e-7, longitude: 1e-6 }, target: 'city', expectedPadding: 4033 },
    { geolocation: { latitude: 37.5, longitude: 127 }, target: 'city', expectedPadding: 4038 },
    { geolocation: { latitude: -90, longitude: 180 }, target: 'city', expectedPadding: 4039 },
    { asn: {}, target: 'organization', expectedPadding: 4070 },
    { asn: { number: 4294967295 }, target: 'organization', expectedPadding: 4050 }
  ];
  for (const fixture of cases) {
    const exact = structuredClone(compactTopology());
    delete exact.nodes[1].geolocation;
    delete exact.nodes[1].asn;
    Object.assign(exact.nodes[1], structuredClone(fixture));
    delete exact.nodes[1].expectedPadding;
    delete exact.nodes[1].target;
    const target = exact.nodes[1].geolocation ?? exact.nodes[1].asn;
    target[fixture.target] = 'x'.repeat(fixture.expectedPadding);
    assert.doesNotThrow(() => normalizeCompactTopology(exact));
    target[fixture.target] += 'x';
    assert.throws(() => normalizeCompactTopology(exact), /Geo bundle|byte limit/i);
  }
});

test('reviewer seven-case result-indexed semantic-linkage corpus rejects atomically without reflection', () => {
  const hostile = /HOSTILE_(?:REASON|OBSERVED)_CANARY|tcp/;
  const linkedAnalysis = (kind, evidenceOverrides = {}, coverageOverrides = {}) => analysis({
    evidence: [{ ...analysis().evidence[0], kind, ...evidenceOverrides }],
    coverage: { ...analysis().coverage, ...coverageOverrides }
  });
  const reportFor = (resultValue, analysisValue, compact_topology) => legacyReport({
    results: [resultValue], analysis: analysisValue,
    ...(compact_topology === undefined ? {} : { compact_topology })
  });
  const traceResult = details => result({ kind: 'traceroute', details });
  const issue = {
    code: 'missing_details', result_index: 0, kind: 'tcp', signal: 'fixture',
    reason: 'HOSTILE_REASON_CANARY'
  };
  const cases = [
    ['evidence kind mismatch', reportFor(result(), linkedAnalysis('tcp', { observed: 'HOSTILE_OBSERVED_CANARY' }))],
    ['coverage kind mismatch', reportFor(result(), linkedAnalysis('dns', {}, { limitations: [issue] }))],
    ['attempt on non-traceroute', reportFor(result(), linkedAnalysis('dns', { attempt: 1, observed: 'HOSTILE_OBSERVED_CANARY' }))],
    ['attempt absent from inventory despite larger attempts_total', reportFor(
      traceResult({ attempts: [{ attempt: 2, status: 'unreachable', error_code: 'timeout' }], attempts_total: 10 }),
      linkedAnalysis('traceroute', { attempt: 1, observed: 'HOSTILE_OBSERVED_CANARY' })
    )],
    ['attempt without inventory or attempts_total', reportFor(
      traceResult({}), linkedAnalysis('traceroute', { attempt: 1, observed: 'HOSTILE_OBSERVED_CANARY' })
    )],
    ['compact route attempt absent from inventory despite larger attempts_total', reportFor(
      traceResult({ attempts: [{ attempt: 2, status: 'unreachable', error_code: 'timeout' }], attempts_total: 10 }),
      linkedAnalysis('traceroute'), compactTopology()
    )],
    ['compact route attempt without inventory or attempts_total', reportFor(
      traceResult({}), linkedAnalysis('traceroute'), compactTopology()
    )]
  ];

  assert.equal(cases.length, 7);
  const accepted = [];
  const parsedAsValid = [];
  for (const [name, report] of cases) {
    try {
      normalizeReport(report);
      accepted.push(name);
    } catch (error) {
      assert.match(error.message, /analysis|coverage|evidence|attempt|route|result|schema/i, name);
      assert.doesNotMatch(error.message, hostile, `${name}: thrown error`);
    }
    const parsed = parseResponse(response(200), JSON.stringify(report), 0);
    if (parsed.ok) {
      parsedAsValid.push(name);
    } else {
      assert.deepEqual(parsed.error, {
        kind: 'invalid-response', code: 'invalid_response', status: 200,
        message: 'The server response did not match the expected schema.', retryable: false
      }, name);
      assert.doesNotMatch(JSON.stringify(parsed), hostile, `${name}: normalized response`);
    }
  }
  assert.deepEqual(accepted, [], 'every reviewer mutation must reject directly');
  assert.deepEqual(parsedAsValid, [], 'every reviewer mutation must reject through parseResponse');
});

test('producer-valid semantic links preserve duplicate attempt inventory and producer ordering', () => {
  const attempts = [2, 2, 1].map(attempt => ({ attempt, status: 'unreachable', error_code: 'timeout' }));
  const trace = result({ kind: 'traceroute', details: { attempts, attempts_total: 1 } });
  const validIssue = {
    code: 'missing_details', result_index: 0, kind: 'traceroute', signal: 'fixture', reason: 'producer reason'
  };
  const validAnalysis = analysis({
    evidence: [{ ...analysis().evidence[0], result_index: 0, kind: 'traceroute', attempt: 2 }],
    coverage: {
      ...analysis().coverage,
      provider_failures: [validIssue],
      limitations: [{ ...validIssue, code: 'unsupported_details' }]
    }
  });
  const linked = normalizeReport(legacyReport({ results: [trace], analysis: validAnalysis }));
  assert.deepEqual(linked.results[0].details.attempts.map(item => item.attempt), [2, 2, 1]);
  assert.equal(linked.analysis.evidence[0].attempt, 2, 'inventory takes precedence over attempts_total');
  assert.deepEqual([
    linked.analysis.coverage.provider_failures[0].kind,
    linked.analysis.coverage.limitations[0].kind
  ], ['traceroute', 'traceroute']);

  const totalFallback = normalizeReport(legacyReport({
    results: [result({ kind: 'traceroute', details: { attempts_total: 3 } })],
    analysis: analysis({ evidence: [{ ...analysis().evidence[0], kind: 'traceroute', attempt: 3 }] })
  }));
  assert.equal(totalFallback.analysis.evidence[0].attempt, 3);

  const topology = structuredClone(compactTopology());
  topology.routes = [
    { ...topology.routes[0], attempt: 2 },
    { ...structuredClone(topology.routes[0]), attempt: 2, complete: false, node_ids: ['n000001'] },
    { ...structuredClone(topology.routes[0]), attempt: 1, complete: false, node_ids: ['n000001'] }
  ];
  topology.nodes[0].observations = 3;
  topology.stats.routes = { total: 3, displayed: 3, complete: 1, partial: 2, omitted: 0 };
  topology.stats.node_observations = { total: 4, displayed: 4, omitted: 0 };
  topology.result_stats[0].routes = structuredClone(topology.stats.routes);
  topology.result_stats[0].node_observations = structuredClone(topology.stats.node_observations);
  const compactLinked = normalizeReport(legacyReport({
    results: [trace], analysis: validAnalysis, compact_topology: topology
  }));
  assert.deepEqual(compactLinked.compact_topology.routes.map(route => route.attempt), [2, 2, 1]);
});

test('compact routes must reference traceroute results', () => {
  assert.throws(() => normalizeReport(legacyReport({ compact_topology: compactTopology() })), /route|traceroute|result_index/i);
});

test('compact route result references reject every non-traceroute result kind', () => {
  for (const kind of ['dns', 'tcp', 'http', 'https', 'ssh', 'smtp', 'submission', 'smtps', 'imap', 'imaps', 'pop3', 'pop3s']) {
    const report = legacyReport({ results: [validResultForKind(kind)], compact_topology: compactTopology() });
    assert.throws(() => normalizeReport(report), /route|traceroute|result_index/i, kind);
  }
  const report = legacyReport({
    results: [result({ kind: 'traceroute', details: { attempts_total: 1 } })],
    compact_topology: compactTopology()
  });
  assert.equal(normalizeReport(report).compact_topology.routes[0].result_index, 0);
});

test('linked non-traceroute result stats must be entirely zero', () => {
  const value = structuredClone(compactTopology());
  value.links = [];
  value.routes = [];
  value.stats.links = { total: 0, displayed: 0, omitted: 0 };
  value.stats.routes = { total: 1, displayed: 0, complete: 0, partial: 0, omitted: 1 };
  value.stats.node_observations = { total: 1, displayed: 0, omitted: 1 };
  value.stats.link_observations = { total: 0, displayed: 0, omitted: 0 };
  value.result_stats[0] = {
    result_index: 0,
    routes: { total: 1, displayed: 0, complete: 0, partial: 0, omitted: 1 },
    node_observations: { total: 1, displayed: 0, omitted: 1 },
    link_observations: { total: 0, displayed: 0, omitted: 0 }
  };
  assert.equal(normalizeCompactTopology(value).result_stats[0].routes.omitted, 1);
  assert.throws(
    () => normalizeReport(legacyReport({ compact_topology: value })),
    /result_stats|non-traceroute|zero/i
  );
});

test('linked result stats reject every nonzero counter field for non-traceroute results', () => {
  const scenarios = {
    'routes.total': { field: 'routes', value: { total: 1, displayed: 0, complete: 0, partial: 0, omitted: 1 } },
    'routes.displayed': { field: 'routes', value: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 } },
    'routes.complete': { field: 'routes', value: { total: 1, displayed: 1, complete: 1, partial: 0, omitted: 0 } },
    'routes.partial': { field: 'routes', value: { total: 1, displayed: 1, complete: 0, partial: 1, omitted: 0 } },
    'routes.omitted': { field: 'routes', value: { total: 1, displayed: 0, complete: 0, partial: 0, omitted: 1 } },
    'node_observations.total': { field: 'node_observations', value: { total: 1, displayed: 0, omitted: 1 } },
    'node_observations.displayed': { field: 'node_observations', value: { total: 1, displayed: 1, omitted: 0 } },
    'node_observations.omitted': { field: 'node_observations', value: { total: 1, displayed: 0, omitted: 1 } },
    'link_observations.total': { field: 'link_observations', value: { total: 1, displayed: 0, omitted: 1 } },
    'link_observations.displayed': { field: 'link_observations', value: { total: 1, displayed: 1, omitted: 0 } },
    'link_observations.omitted': { field: 'link_observations', value: { total: 1, displayed: 0, omitted: 1 } }
  };
  for (const [counter, { field, value: nonzero }] of Object.entries(scenarios)) {
    const topology = emptyCompactTopology([zeroResultStats(0)]);
    topology.result_stats[0][field] = nonzero;
    topology.stats[field] = structuredClone(nonzero);
    assert.throws(
      () => normalizeReport(legacyReport({ compact_topology: topology })),
      /result_stats|non-traceroute|zero|displayed counts|route/i,
      counter
    );
  }
});

test('linked zero result stats accept all 12 non-traceroute kinds as immutable copies', () => {
  const kinds = ['dns', 'tcp', 'http', 'https', 'ssh', 'smtp', 'submission', 'smtps', 'imap', 'imaps', 'pop3', 'pop3s'];
  for (const kind of kinds) {
    const nonzero = emptyCompactTopology([zeroResultStats(0)]);
    nonzero.stats.routes = { total: 1, displayed: 0, complete: 0, partial: 0, omitted: 1 };
    nonzero.result_stats[0].routes = structuredClone(nonzero.stats.routes);
    assert.throws(
      () => normalizeReport(legacyReport({ results: [validResultForKind(kind)], compact_topology: nonzero })),
      /result_stats|non-traceroute|zero/i,
      kind
    );
  }
  const results = kinds.map(validResultForKind);
  const topology = emptyCompactTopology(kinds.map((_, index) => zeroResultStats(index)));
  const normalized = normalizeReport(legacyReport({
    results,
    summary: { total: kinds.length, passed: kinds.length, failed: 0 },
    compact_topology: topology
  }));
  assert.deepEqual(normalized.compact_topology.result_stats, topology.result_stats);
  topology.result_stats[0].routes.total = 99;
  topology.result_stats.splice(1);
  assert.equal(normalized.compact_topology.result_stats.length, kinds.length);
  assert.equal(normalized.compact_topology.result_stats[0].routes.total, 0);
});

test('linked result stats require existing indexes while standalone parsing retains no-linkage behavior', () => {
  const topology = emptyCompactTopology([zeroResultStats(1)]);
  assert.equal(normalizeCompactTopology(topology).result_stats[0].result_index, 1);
  assert.throws(
    () => normalizeCompactTopology(topology, 'report.compact_topology', [result()]),
    /result_stats|unknown result|result_index/i
  );
  assert.throws(() => normalizeCompactTopology(emptyCompactTopology(Array.from({ length: 21 }, (_, index) => zeroResultStats(index))), 'compact_topology'), /result_stats|limit/i);
});

test('compact route reached must agree with status', () => {
  const value = structuredClone(compactTopology());
  value.routes[0].reached = false;
  assert.throws(() => normalizeCompactTopology(value), /route|reached|status/i);
});

test('compact route reached equals status not unreachable for every pair', () => {
  for (const [status, reached, accepted] of [
    ['healthy', true, true], ['healthy', false, false],
    ['degraded', true, true], ['degraded', false, false],
    ['unreachable', true, false], ['unreachable', false, true]
  ]) {
    const value = structuredClone(compactTopology());
    Object.assign(value.routes[0], { status, reached });
    if (accepted) assert.equal(normalizeCompactTopology(value).routes[0].reached, reached, `${status}/${reached}`);
    else assert.throws(() => normalizeCompactTopology(value), /route|reached|status/i, `${status}/${reached}`);
  }
});

test('compact routes reject consecutive duplicate node IDs', () => {
  const value = structuredClone(compactTopology());
  value.routes[0].node_ids = ['n000001', 'n000002', 'n000002'];
  value.stats.node_observations = { total: 3, displayed: 3, omitted: 0 };
  value.result_stats[0].node_observations = { total: 3, displayed: 3, omitted: 0 };
  assert.throws(() => normalizeCompactTopology(value), /route|node_ids|consecutive|duplicate/i);
});

test('compact routes reject consecutive duplicates at start, middle, and end', () => {
  for (const node_ids of [
    ['n000001', 'n000001', 'n000002'],
    ['n000001', 'n000002', 'n000002', 'n000001'],
    ['n000001', 'n000002', 'n000001', 'n000001']
  ]) {
    const value = structuredClone(compactTopology());
    value.links.push({ from: 'n000002', to: 'n000001', status: 'healthy', observations: 1 });
    value.routes[0].node_ids = node_ids;
    assert.throws(() => normalizeCompactTopology(value), /route|node_ids|consecutive|duplicate/i, node_ids.join(','));
  }
});

test('compact routes preserve a producer-valid non-consecutive revisit', () => {
  const value = structuredClone(compactTopology());
  value.nodes[0].observations = 2;
  value.links.push({ from: 'n000002', to: 'n000001', status: 'healthy', observations: 1 });
  value.routes[0].node_ids = ['n000001', 'n000002', 'n000001'];
  value.stats.links = { total: 2, displayed: 2, omitted: 0 };
  value.stats.node_observations = { total: 3, displayed: 3, omitted: 0 };
  value.stats.link_observations = { total: 2, displayed: 2, omitted: 0 };
  value.result_stats[0].node_observations = { total: 3, displayed: 3, omitted: 0 };
  value.result_stats[0].link_observations = { total: 2, displayed: 2, omitted: 0 };

  const normalized = normalizeCompactTopology(value);
  assert.deepEqual(normalized.routes[0].node_ids, ['n000001', 'n000002', 'n000001']);
  value.routes[0].node_ids[0] = 'mutated';
  assert.deepEqual(normalized.routes[0].node_ids, ['n000001', 'n000002', 'n000001']);
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

test('compact routes preserve backend-collapsed canonical node observations', () => {
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
    ...(index === 0 ? { address: 'local' } : index === 31 ? { address: 'target.example' } : {}),
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

test('compact schema preserves required empty arrays, omits empty optional arrays, and rejects accessors without invoking them', () => {
  const empty = compactTopology({
    nodes: [], links: [], routes: [],
    stats: { nodes: { total: 0, displayed: 0, omitted: 0 }, links: { total: 0, displayed: 0, omitted: 0 }, routes: { total: 0, displayed: 0, complete: 0, partial: 0, omitted: 0 }, node_observations: { total: 0, displayed: 0, omitted: 0 }, link_observations: { total: 0, displayed: 0, omitted: 0 } },
    result_stats: [], geo: { eligible: 0, available: 0, included: 0, omitted: 0, unavailable: 0 }
  });
  const normalized = normalizeCompactTopology(empty);
  assert.deepEqual([normalized.nodes, normalized.links, normalized.routes, normalized.result_stats], [[], [], [], []]);
  assert.equal(Object.hasOwn(normalized, 'truncation_reasons'), false);
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

test('full topology accepts only optional bounded latency_delta_ms and never normalizes omission to zero', () => {
  const topology = delta => ({
    reached: true,
    nodes: [{ id: 'one', hop: 1, status: 'healthy' }, { id: 'two', hop: 2, status: 'healthy' }],
    links: [{ from: 'one', to: 'two', status: 'healthy', ...(delta === undefined ? {} : { latency_delta_ms: delta }) }]
  });
  const absent = normalizeTopology(topology(undefined));
  assert.equal(Object.hasOwn(absent.links[0], 'latency_delta_ms'), false);
  for (const delta of [0, 0.25, 30000]) {
    const normalized = normalizeTopology(topology(delta));
    assert.equal(normalized.links[0].latency_delta_ms, delta);
  }
  for (const delta of [-1, 30000.0001, 30001, Number.NaN, Number.POSITIVE_INFINITY, null, '1']) {
    assert.throws(() => normalizeTopology(topology(delta)), /latency_delta_ms|bounded|finite|schema/i, String(delta));
  }
  const obsolete = topology(undefined);
  obsolete.links[0].latency_ms = 1;
  assert.throws(() => normalizeTopology(obsolete), /latency_ms|field|schema/i);
});

test('noncanonical API error messages fail closed without retaining a reflected bearer credential', () => {
  const bearer = 'Bearer SUPER-secret-Token-123';
  const parsed = parseResponse(
    response(401),
    JSON.stringify({ error: { code: 'unauthorized', message: `invalid ${bearer}` } }),
    0
  );
  assert.equal(parsed.error.code, 'invalid_server_response');
  assert.doesNotMatch(JSON.stringify(parsed), /SUPER-secret-Token-123/i);
  assert.equal(parsed.error.message, 'The server returned an invalid error response.');
});

test('unknown structured HTTP error codes use the fixed invalid response without reflecting a bearer credential', () => {
  const bearer = 'reflected-secret-token-123';
  const parsed = parseResponse(response(401), JSON.stringify({ error: { code: bearer, message: 'rejected' } }), 0);
  assert.equal(parsed.error.code, 'invalid_server_response');
  assert.equal(parsed.error.retryable, false);
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
