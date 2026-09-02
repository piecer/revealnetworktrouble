'use strict';

const REQUEST_PHASES = Object.freeze(['idle', 'loading', 'ready', 'error', 'cancelled']);
const ERROR_KINDS = Object.freeze(['http', 'network', 'timeout', 'invalid-response', 'cancelled']);
const SCHEMA_LIMITS = Object.freeze({
  responseBytes: 8 * 1024 * 1024,
  string: 4096,
  errorCode: 128,
  results: 20,
  findings: 64,
  evidence: 64,
  actions: 64,
  coverage: 128,
  topologyNodes: 1024,
  topologyLinks: 2048,
  reportTopologyNodes: 8192,
  reportTopologyLinks: 16384,
  reportStringChars: 1000000,
  reportContainers: 32768,
  detailArray: 2048,
  detailKeys: 256,
  detailDepth: 8
});

const PURPOSES = new Set(['diagnostics', 'topology']);
const KINDS = new Set(['dns', 'tcp', 'http', 'https', 'traceroute', 'ssh', 'smtp', 'submission', 'smtps', 'imap', 'imaps', 'pop3', 'pop3s']);
const STATUSES = new Set(['healthy', 'degraded', 'unreachable']);
const VERDICTS = new Set(['healthy', 'attention', 'inconclusive']);
const SEVERITIES = new Set(['critical', 'warning', 'info']);
const CONFIDENCES = new Set(['direct', 'corroborated', 'limited']);
const CATEGORIES = new Set(['name_resolution', 'connectivity', 'application', 'security', 'routing', 'execution', 'input']);
const PROVENANCES = new Set(['result', 'details']);
const COVERAGE_CODES = new Set(['missing_details', 'malformed_details', 'unsupported_details']);
const ENRICHMENT_SOURCES = new Set(['upstream', 'cache', 'mixed', 'none']);
const ENRICHMENT_FAILURE_RETRYABLE = Object.freeze({
  busy: true,
  cancelled: false,
  malformed: false,
  not_found: false,
  policy: false,
  rate_limited: true,
  timeout: true,
  unavailable: true
});
const ENRICHMENT_FAILURE_KINDS = Object.freeze(Object.keys(ENRICHMENT_FAILURE_RETRYABLE));
const TOPOLOGY_STATUSES = new Set(['healthy', 'degraded', 'unknown', 'failure']);
const COMPACT_NODE_KINDS = new Set(['local', 'ip', 'hostname', 'unknown']);
const COMPACT_TRUNCATION_REASONS = Object.freeze(['node_limit', 'link_limit', 'response_size', 'geo_metadata_limit']);
const COMPACT_MAX_ROUTES = SCHEMA_LIMITS.results * 10;
const COMPACT_LIMITS = Object.freeze({ nodes: 500, links: 1000, maxResponseBytesExclusive: 1 << 20, maxGeoBundleBytes: 4096 });
const FINDING_CODES = new Set([
  'dns_resolution_failed', 'endpoint_connect_failed', 'http_unexpected_status', 'invalid_target',
  'execution_timeout', 'execution_cancelled', 'tls_downgrade', 'tls_certificate_expired',
  'tls_certificate_expiring', 'tls_handshake_failed', 'target_policy_blocked',
  'traceroute_unreachable', 'traceroute_partial_reachability', 'traceroute_path_degraded',
  'traceroute_path_unstable', 'traceroute_execution_failed', 'checker_panic',
  'checker_capacity_unavailable'
]);
const FINDING_PRESENTATIONS = Object.freeze({
  checker_panic: Object.freeze({
    title: 'Checker execution failed',
    summary: 'The checker stopped unexpectedly, so service health was not established.'
  }),
  checker_capacity_unavailable: Object.freeze({
    title: 'Checker capacity was unavailable',
    summary: 'The bounded checker supervisor had no execution slot, so service health was not established.'
  })
});
const API_ERROR_CODES = new Set([
  'invalid_json', 'invalid_request', 'server_busy', 'internal_error',
  'network_policy_blocked', 'unauthorized', 'rate_limited'
]);

function schemaError(path, message) {
  return new TypeError(`invalid response schema at ${path}: ${message}`);
}

function rejectAccessors(value, path, arrayPath = false) {
  const descriptors = Object.getOwnPropertyDescriptors(value);
  for (const key of Reflect.ownKeys(descriptors)) {
    if (arrayPath && key === 'length') continue;
    if (!Object.hasOwn(descriptors[key], 'value')) {
      const suffix = typeof key === 'symbol' ? `[${String(key)}]` : arrayPath ? `[${key}]` : `.${key}`;
      throw schemaError(`${path}${suffix}`, 'accessor properties are not supported');
    }
  }
}

function object(value, path) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw schemaError(path, 'expected object');
  if (Object.getPrototypeOf(value) !== Object.prototype) throw schemaError(path, 'expected a plain object');
  rejectAccessors(value, path);
  return value;
}

function array(value, path, max) {
  if (!Array.isArray(value)) throw schemaError(path, 'expected array');
  if (Object.getPrototypeOf(value) !== Array.prototype) throw schemaError(path, 'expected a plain array');
  rejectAccessors(value, path, true);
  if (value.length > max) throw schemaError(path, `exceeds limit ${max}`);
  return value;
}

function text(value, path, { required = true, max = SCHEMA_LIMITS.string } = {}) {
  if (typeof value !== 'string') throw schemaError(path, 'expected string');
  if (value.length > max) throw schemaError(path, `exceeds limit ${max}`);
  if (required && value.length === 0) throw schemaError(path, 'must not be empty');
  return value;
}

function optionalText(value, path, options) {
  return value === undefined || value === null || value === '' ? '' : text(value, path, { required: false, ...options });
}

function enumValue(value, allowed, path) {
  if (typeof value !== 'string' || !allowed.has(value)) throw schemaError(path, 'unsupported value');
  return value;
}

function integer(value, path, min = 0, max = Number.MAX_SAFE_INTEGER) {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw schemaError(path, 'expected bounded integer');
  return value;
}

function finite(value, path, min = 0, max = Number.MAX_VALUE) {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < min || value > max) throw schemaError(path, 'expected bounded finite number');
  return value;
}

function boolean(value, path) {
  if (typeof value !== 'boolean') throw schemaError(path, 'expected boolean');
  return value;
}

function timestamp(value, path) {
  const result = text(value, path);
  if (!Number.isFinite(Date.parse(result))) throw schemaError(path, 'expected timestamp');
  return result;
}

function createRequestLane(purpose) {
  if (!PURPOSES.has(purpose)) throw new TypeError('purpose must be diagnostics or topology');
  return { purpose, phase: 'idle', inputSignature: '', active: null, result: null, error: null, cancellation: null };
}

function eventType(value) {
  return String(value || '').trim().toUpperCase().replaceAll('-', '_');
}

function ownsRequest(state, ownerId, signature) {
  return Boolean(state && state.phase === 'loading' && state.active &&
    state.active.ownerId === ownerId && state.active.inputSignature === signature && state.inputSignature === signature);
}

function transitionRequest(state, event) {
  object(state, 'state');
  object(event, 'event');
  const type = eventType(event.type);
  if (type === 'INPUT_CHANGED' || type === 'INPUT_INVALIDATED') {
    return { ...state, phase: 'idle', inputSignature: String(event.inputSignature ?? event.signature ?? ''), active: null, result: null, error: null, cancellation: null };
  }
  if (type === 'REQUEST_STARTED' || type === 'START') {
    const signature = String(event.inputSignature ?? event.signature ?? '');
    const ownerId = String(event.ownerId ?? '');
    if (!ownerId || !signature) throw new TypeError('request start requires ownerId and inputSignature');
    return { ...state, phase: 'loading', inputSignature: signature, active: { ownerId, inputSignature: signature, startedAt: finite(event.startedAt ?? Date.now(), 'event.startedAt') }, result: null, error: null, cancellation: null };
  }
  if (type === 'REQUEST_FINALIZED' || type === 'FINALIZE') {
    if (!state.active || state.active.ownerId !== event.ownerId) return state;
    if (event.inputSignature !== undefined && state.active.inputSignature !== event.inputSignature) return state;
    return { ...state, active: null };
  }
  const signature = String(event.inputSignature ?? event.signature ?? '');
  if (!ownsRequest(state, event.ownerId, signature)) return state;
  if (type === 'REQUEST_SUCCEEDED' || type === 'SUCCESS') {
    return { ...state, phase: 'ready', result: { ownerId: event.ownerId, inputSignature: signature, report: event.report, completedAt: finite(event.completedAt ?? Date.now(), 'event.completedAt') }, error: null, cancellation: null };
  }
  if (type === 'REQUEST_FAILED' || type === 'FAILURE' || type === 'ERROR') {
    return { ...state, phase: 'error', result: null, error: normalizeRequestError(event.error), cancellation: null };
  }
  if (type === 'REQUEST_CANCELLED' || type === 'CANCEL') {
    const reason = event.reason ?? 'user';
    if (!['user', 'navigation', 'input-change'].includes(reason)) throw new TypeError('unsupported cancellation reason');
    return { ...state, phase: 'cancelled', result: null, error: null, cancellation: { reason } };
  }
  throw new TypeError(`unsupported request event: ${event.type}`);
}

function rawAPIBase(raw) {
  return raw.apiBaseURL ?? raw.apiBaseUrl ?? raw.apiURL ?? raw.api_url ?? raw.apiBase ?? '';
}

function canonicalAPIBase(value) {
  let url;
  try { url = new URL(String(value).trim()); } catch { throw new TypeError('API base must be a valid URL'); }
  if (!['http:', 'https:'].includes(url.protocol)) throw new TypeError('API base must use HTTP or HTTPS');
  if (url.username || url.password || url.search || url.hash) throw new TypeError('API base must not contain credentials, query, or fragment');
  url.pathname = url.pathname.replace(/\/+$/, '') || '/';
  return url.toString().replace(/\/$/, '');
}

function numericInteger(value, name, min, max, fallback) {
  if ((value === undefined || value === null || value === '') && fallback !== undefined) return fallback;
  const number = typeof value === 'number' ? value : Number(value);
  if (!Number.isInteger(number) || number < min || number > max) throw new TypeError(`${name} must be an integer from ${min} to ${max}`);
  return number;
}

function canonicalDiagnosticsInput(raw) {
  object(raw, 'input');
  const targets = array(raw.targets, 'input.targets', 20).map((target, index) => {
    object(target, `input.targets[${index}]`);
    const kind = String(target.kind ?? '').trim();
    const address = String(target.address ?? '').trim();
    if (!KINDS.has(kind) || !address || address.length > SCHEMA_LIMITS.string) throw new TypeError('invalid diagnostic target');
    const expected = numericInteger(target.expected_status ?? target.expectedStatus, 'expected_status', 0, 599, 0);
    const normalized = { kind, address, expected_status: expected };
    if (kind === 'traceroute') normalized.attempts = numericInteger(target.attempts, 'attempts', 1, 10, 5);
    return normalized;
  });
  const bearer = raw.bearer ?? raw.bearerToken ?? raw.token;
  return {
    apiBaseURL: canonicalAPIBase(rawAPIBase(raw)),
    targets,
    timeout_ms: numericInteger(raw.timeout_ms ?? raw.timeoutMS ?? raw.timeout, 'timeout_ms', 100, 30000, 5000),
    authEnabled: Boolean(raw.authEnabled ?? raw.auth_enabled ?? (typeof bearer === 'string' && bearer.length > 0))
  };
}

function canonicalTopologyInput(raw) {
  object(raw, 'input');
  const source = raw.addresses ?? raw.targets;
  const seen = new Set();
  const addresses = [];
  for (const item of array(source, 'input.addresses', 20)) {
    const address = String(typeof item === 'object' && item !== null ? item.address : item).trim();
    if (!address || address.length > SCHEMA_LIMITS.string) throw new TypeError('invalid topology address');
    if (!seen.has(address)) { seen.add(address); addresses.push(address); }
  }
  return {
    apiBaseURL: canonicalAPIBase(rawAPIBase(raw)),
    addresses,
    attempts: numericInteger(raw.attempts, 'attempts', 1, 10, 5),
    timeout_ms: numericInteger(raw.timeout_ms ?? raw.timeoutMS ?? raw.timeout, 'timeout_ms', 100, 30000, 5000),
    authEnabled: Boolean(raw.authEnabled ?? raw.auth_enabled)
  };
}

function inputSignature(purpose, canonicalInput, authRevision = 0) {
  if (!PURPOSES.has(purpose)) throw new TypeError('invalid purpose');
  const input = purpose === 'diagnostics'
    ? canonicalDiagnosticsInput(canonicalInput)
    : canonicalTopologyInput(canonicalInput);
  return JSON.stringify({ purpose, ...input, authRevision: numericInteger(authRevision, 'authRevision', 0, Number.MAX_SAFE_INTEGER, 0) });
}

function clientTimeoutMS(payload) {
  const timeout = Math.min(30000, Math.max(100, numericInteger(payload?.timeout_ms ?? payload?.timeoutMS, 'timeout_ms', 0, Number.MAX_SAFE_INTEGER, 5000)));
  let longest = timeout;
  for (const target of Array.isArray(payload?.targets) ? payload.targets.slice(0, 20) : []) {
    if (target?.kind !== 'traceroute') continue;
    let attempts = Number(target.attempts ?? 5);
    if (!Number.isInteger(attempts)) attempts = 5;
    attempts = Math.min(10, Math.max(1, attempts));
    longest = Math.max(longest, timeout * attempts);
  }
  return Math.min(315000, longest + 15000);
}

function parseRetryAfter(value, nowMS = Date.now()) {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) {
    const seconds = Number(trimmed);
    return Number.isSafeInteger(seconds) ? nowMS + seconds * 1000 : null;
  }
  const parsed = Date.parse(trimmed);
  return Number.isFinite(parsed) && parsed >= nowMS ? parsed : null;
}

function normalizedError(kind, code, { status = null, message, retryAt = null, retryable = false } = {}) {
  return { kind, code, status, message, retryAt, retryable };
}

function normalizeRequestError(input) {
  const wrapper = input && typeof input === 'object' && input.error ? input : null;
  const error = wrapper ? wrapper.error : input;
  const reason = wrapper?.reason ?? wrapper?.abortReason ?? error?.reason ?? error?.cause;
  if (reason === 'timeout' || error?.name === 'TimeoutError') return normalizedError('timeout', 'timeout', { message: 'The request timed out.', retryable: true });
  if (error?.name === 'AbortError') {
    if (reason === 'timeout') return normalizedError('timeout', 'timeout', { message: 'The request timed out.', retryable: true });
    return normalizedError('cancelled', 'cancelled', { message: 'The request was cancelled.' });
  }
  if (error instanceof TypeError || error?.name === 'TypeError') return normalizedError('network', 'network_error', { message: 'The network request could not be completed.', retryable: true });
  if (error && ERROR_KINDS.includes(error.kind) && typeof error.code === 'string') {
    return normalizedError(error.kind, error.code, {
      status: Number.isInteger(error.status) ? error.status : null,
      message: typeof error.message === 'string' ? error.message.slice(0, SCHEMA_LIMITS.string) : 'The request failed.',
      retryAt: Number.isFinite(error.retryAt) ? error.retryAt : null,
      retryable: Boolean(error.retryable)
    });
  }
  return normalizedError('network', 'network_error', { message: 'The network request could not be completed.', retryable: true });
}

function header(response, name) {
  try { return response?.headers?.get?.(name) ?? null; } catch { return null; }
}

function parseResponse(response, bodyText, nowMS = Date.now()) {
  const status = Number.isInteger(response?.status) ? response.status : 0;
  const ok = typeof response?.ok === 'boolean' ? response.ok : status >= 200 && status < 300;
  const body = typeof bodyText === 'string' ? bodyText : '';
  let parsed;
  if (body.trim()) {
    try { parsed = JSON.parse(body); } catch {
      if (ok) return { ok: false, error: normalizedError('invalid-response', 'invalid_response', { status, message: 'The server returned malformed JSON.' }) };
    }
  }
  if (ok) {
    if (parsed === undefined) return { ok: false, error: normalizedError('invalid-response', 'invalid_response', { status, message: 'The server returned an empty response.' }) };
    try { return { ok: true, report: normalizeReport(parsed), error: null }; }
    catch { return { ok: false, error: normalizedError('invalid-response', 'invalid_response', { status, message: 'The server response did not match the expected schema.' }) }; }
  }
  const defaults = {
    401: ['unauthorized', false], 422: ['unprocessable_entity', false],
    429: ['rate_limited', true], 503: ['server_busy', true]
  };
  let [code, retryable] = defaults[status] ?? [`http_${status || 'error'}`, status >= 500];
  const stableMessages = {
    401: 'Authorization credential was rejected (HTTP 401).',
    422: 'The request could not be processed (HTTP 422).',
    429: 'The request was rate limited (HTTP 429).',
    503: 'The server is busy (HTTP 503).'
  };
  const message = stableMessages[status] ?? (body.trim()
    ? `The server returned a non-JSON error response (HTTP ${status}).`
    : `The server returned an error (HTTP ${status}).`);
  const structured = parsed && typeof parsed === 'object' && !Array.isArray(parsed) && parsed.error && typeof parsed.error === 'object' && !Array.isArray(parsed.error) ? parsed.error : null;
  if (structured && typeof structured.code === 'string' && API_ERROR_CODES.has(structured.code)) code = structured.code;
  if (code === 'network_policy_blocked') retryable = false;
  if (code === 'rate_limited' || code === 'server_busy') retryable = true;
  const retryAt = retryable ? parseRetryAfter(header(response, 'Retry-After'), nowMS) : null;
  return { ok: false, error: normalizedError('http', code, { status, message, retryAt, retryable }) };
}

function normalizeStringArray(value, path, max = SCHEMA_LIMITS.coverage) {
  return array(value, path, max).map((item, index) => text(item, `${path}[${index}]`));
}

function normalizeCoverageIssue(value, path) {
  const source = object(value, path);
  return {
    code: enumValue(source.code, COVERAGE_CODES, `${path}.code`),
    result_index: integer(source.result_index, `${path}.result_index`, 0, SCHEMA_LIMITS.results - 1),
    kind: enumValue(source.kind, KINDS, `${path}.kind`),
    signal: optionalText(source.signal, `${path}.signal`),
    reason: text(source.reason, `${path}.reason`)
  };
}

function exactFields(value, expected, path) {
  const keys = Reflect.ownKeys(value);
  if (keys.length !== expected.length || expected.some(key => !Object.hasOwn(value, key)) || keys.some(key => typeof key !== 'string' || !expected.includes(key))) {
    throw schemaError(path, 'expected exact fields');
  }
}

function normalizeEnrichment(value, path = 'analysis.coverage.enrichment') {
  const entries = array(value, path, 1);
  const normalized = entries.map((entry, index) => {
    const itemPath = `${path}[${index}]`;
    const source = object(entry, itemPath);
    exactFields(source, ['provider', 'source', 'cache_hits', 'upstream_fetches', 'max_age_ms', 'failures'], itemPath);
    if (source.provider !== 'geoip') throw schemaError(`${itemPath}.provider`, 'unsupported value');
    const cacheHits = integer(source.cache_hits, `${itemPath}.cache_hits`, 0, 6200);
    const upstreamFetches = integer(source.upstream_fetches, `${itemPath}.upstream_fetches`, 0, 6200);
    const failuresSource = array(source.failures, `${itemPath}.failures`, ENRICHMENT_FAILURE_KINDS.length);
    let total = cacheHits + upstreamFetches;
    if (total > 6200) throw schemaError(itemPath, 'lookup count exceeds limit 6200');
    let previous = '';
    const failures = failuresSource.map((entryValue, failureIndex) => {
      const failurePath = `${itemPath}.failures[${failureIndex}]`;
      const failure = object(entryValue, failurePath);
      exactFields(failure, ['kind', 'count', 'retryable'], failurePath);
      if (typeof failure.kind !== 'string' || !Object.hasOwn(ENRICHMENT_FAILURE_RETRYABLE, failure.kind)) throw schemaError(`${failurePath}.kind`, 'unsupported value');
      if (failure.kind <= previous) throw schemaError(`${itemPath}.failures`, 'must contain unique kinds in canonical order');
      previous = failure.kind;
      const count = integer(failure.count, `${failurePath}.count`, 1, 6200);
      const retryable = boolean(failure.retryable, `${failurePath}.retryable`);
      if (retryable !== ENRICHMENT_FAILURE_RETRYABLE[failure.kind]) throw schemaError(`${failurePath}.retryable`, 'contradicts failure kind');
      total += count;
      if (total > 6200) throw schemaError(itemPath, 'lookup count exceeds limit 6200');
      return Object.freeze({ kind: failure.kind, count, retryable });
    });
    const expectedSource = cacheHits > 0
      ? (upstreamFetches > 0 ? 'mixed' : 'cache')
      : (upstreamFetches > 0 ? 'upstream' : 'none');
    const enrichmentSource = enumValue(source.source, ENRICHMENT_SOURCES, `${itemPath}.source`);
    if (enrichmentSource !== expectedSource) throw schemaError(`${itemPath}.source`, 'contradicts success counts');
    return Object.freeze({
      provider: 'geoip', source: enrichmentSource, cache_hits: cacheHits, upstream_fetches: upstreamFetches,
      max_age_ms: integer(source.max_age_ms, `${itemPath}.max_age_ms`, 0, 86400000), failures: Object.freeze(failures)
    });
  });
  return Object.freeze(normalized);
}

function normalizeAnalysis(value) {
  const source = object(value, 'analysis');
  const findings = array(source.findings, 'analysis.findings', SCHEMA_LIMITS.findings).map((entry, index) => {
    const path = `analysis.findings[${index}]`; const item = object(entry, path);
    const code = enumValue(item.code, FINDING_CODES, `${path}.code`);
    const presentation = FINDING_PRESENTATIONS[code];
    return {
      id: text(item.id, `${path}.id`), code,
      severity: enumValue(item.severity, SEVERITIES, `${path}.severity`), category: enumValue(item.category, CATEGORIES, `${path}.category`),
      title: presentation?.title ?? text(item.title, `${path}.title`),
      summary: presentation?.summary ?? text(item.summary, `${path}.summary`),
      confidence: enumValue(item.confidence, CONFIDENCES, `${path}.confidence`),
      evidence_ids: normalizeStringArray(item.evidence_ids, `${path}.evidence_ids`, SCHEMA_LIMITS.evidence),
      action_ids: normalizeStringArray(item.action_ids, `${path}.action_ids`, SCHEMA_LIMITS.actions)
    };
  });
  const evidence = array(source.evidence, 'analysis.evidence', SCHEMA_LIMITS.evidence).map((entry, index) => {
    const path = `analysis.evidence[${index}]`; const item = object(entry, path);
    const normalized = {
      id: text(item.id, `${path}.id`), result_index: integer(item.result_index, `${path}.result_index`, 0, SCHEMA_LIMITS.results - 1),
      kind: enumValue(item.kind, KINDS, `${path}.kind`), address: text(item.address, `${path}.address`, { required: false }),
      signal: text(item.signal, `${path}.signal`), observed: text(item.observed, `${path}.observed`, { required: false }),
      expected: optionalText(item.expected, `${path}.expected`), provenance: enumValue(item.provenance, PROVENANCES, `${path}.provenance`)
    };
    if (item.attempt !== undefined) normalized.attempt = integer(item.attempt, `${path}.attempt`, 1, 10);
    return normalized;
  });
  const actions = array(source.actions, 'analysis.actions', SCHEMA_LIMITS.actions).map((entry, index) => {
    const path = `analysis.actions[${index}]`; const item = object(entry, path);
    return { id: text(item.id, `${path}.id`), title: text(item.title, `${path}.title`), step: text(item.step, `${path}.step`), expected_result: text(item.expected_result, `${path}.expected_result`), escalation_condition: text(item.escalation_condition, `${path}.escalation_condition`) };
  });
  const coverageSource = object(source.coverage, 'analysis.coverage');
  const normalizeIssues = (name) => array(coverageSource[name], `analysis.coverage.${name}`, SCHEMA_LIMITS.coverage).map((entry, index) => normalizeCoverageIssue(entry, `analysis.coverage.${name}[${index}]`));
  const normalized = {
    verdict: enumValue(source.verdict, VERDICTS, 'analysis.verdict'), findings, evidence, actions,
    coverage: {
      available: normalizeStringArray(coverageSource.available, 'analysis.coverage.available'),
      missing: normalizeStringArray(coverageSource.missing, 'analysis.coverage.missing'),
      enrichment: coverageSource.enrichment === undefined
        ? Object.freeze([])
        : normalizeEnrichment(coverageSource.enrichment),
      provider_failures: normalizeIssues('provider_failures'), limitations: normalizeIssues('limitations')
    }
  };
  if (normalized.verdict === 'attention' && normalized.findings.length === 0) throw schemaError('analysis.findings', 'attention verdict requires a finding');
  for (const [items, path] of [[findings, 'analysis.findings'], [evidence, 'analysis.evidence'], [actions, 'analysis.actions']]) {
    if (new Set(items.map(item => item.id)).size !== items.length) throw schemaError(path, 'ids must be unique');
  }
  const evidenceIDs = new Set(evidence.map(item => item.id));
  const actionIDs = new Set(actions.map(item => item.id));
  for (const finding of findings) {
    if (finding.evidence_ids.some(id => !evidenceIDs.has(id)) || finding.action_ids.some(id => !actionIDs.has(id))) throw schemaError('analysis.findings', 'references unknown evidence or action');
  }
  return normalized;
}

function normalizeTopology(value, path = 'topology') {
  const source = object(value, path);
  const nodes = array(source.nodes, `${path}.nodes`, SCHEMA_LIMITS.topologyNodes).map((entry, index) => {
    const itemPath = `${path}.nodes[${index}]`; const item = object(entry, itemPath);
    const normalized = {
      id: text(item.id, `${itemPath}.id`), hop: integer(item.hop, `${itemPath}.hop`, 0, 255),
      address: optionalText(item.address, `${itemPath}.address`), latency_ms: finite(item.latency_ms ?? 0, `${itemPath}.latency_ms`),
      status: enumValue(item.status, TOPOLOGY_STATUSES, `${itemPath}.status`), public_ip: item.public_ip === undefined ? false : boolean(item.public_ip, `${itemPath}.public_ip`)
    };
    if (item.geolocation !== undefined && item.geolocation !== null) {
      const geo = object(item.geolocation, `${itemPath}.geolocation`);
      normalized.geolocation = {
        city: optionalText(geo.city, `${itemPath}.geolocation.city`), region: optionalText(geo.region, `${itemPath}.geolocation.region`),
        country: optionalText(geo.country, `${itemPath}.geolocation.country`), country_code: optionalText(geo.country_code, `${itemPath}.geolocation.country_code`),
        latitude: finite(geo.latitude, `${itemPath}.geolocation.latitude`, -90, 90), longitude: finite(geo.longitude, `${itemPath}.geolocation.longitude`, -180, 180)
      };
    }
    if (item.asn !== undefined && item.asn !== null) {
      const asn = object(item.asn, `${itemPath}.asn`);
      normalized.asn = { number: integer(asn.number ?? 0, `${itemPath}.asn.number`, 0, 4294967295), organization: optionalText(asn.organization, `${itemPath}.asn.organization`) };
    }
    return normalized;
  });
  const nodeIDs = new Set();
  for (const node of nodes) {
    if (nodeIDs.has(node.id)) throw schemaError(`${path}.nodes`, 'node ids must be unique');
    nodeIDs.add(node.id);
  }
  const links = array(source.links, `${path}.links`, SCHEMA_LIMITS.topologyLinks).map((entry, index) => {
    const itemPath = `${path}.links[${index}]`; const item = object(entry, itemPath);
    const normalized = { from: text(item.from, `${itemPath}.from`), to: text(item.to, `${itemPath}.to`), status: enumValue(item.status, TOPOLOGY_STATUSES, `${itemPath}.status`), latency_delta_ms: finite(item.latency_delta_ms ?? 0, `${itemPath}.latency_delta_ms`, -Number.MAX_VALUE) };
    if (!nodeIDs.has(normalized.from) || !nodeIDs.has(normalized.to)) throw schemaError(itemPath, 'link references an unknown node');
    return normalized;
  });
  return { reached: boolean(source.reached, `${path}.reached`), nodes, links };
}

function normalizeCompactCount(value, path, max = SCHEMA_LIMITS.reportTopologyLinks) {
  const source = object(value, path);
  const normalized = {
    total: integer(source.total, `${path}.total`, 0, max),
    displayed: integer(source.displayed, `${path}.displayed`, 0, max),
    omitted: integer(source.omitted, `${path}.omitted`, 0, max)
  };
  if (normalized.total !== normalized.displayed + normalized.omitted) throw schemaError(path, 'total must equal displayed plus omitted');
  return normalized;
}

function normalizeCompactRouteCount(value, path) {
  const source = object(value, path);
  const normalized = {
    total: integer(source.total, `${path}.total`, 0, COMPACT_MAX_ROUTES),
    displayed: integer(source.displayed, `${path}.displayed`, 0, COMPACT_MAX_ROUTES),
    complete: integer(source.complete, `${path}.complete`, 0, COMPACT_MAX_ROUTES),
    partial: integer(source.partial, `${path}.partial`, 0, COMPACT_MAX_ROUTES),
    omitted: integer(source.omitted, `${path}.omitted`, 0, COMPACT_MAX_ROUTES)
  };
  if (normalized.total !== normalized.displayed + normalized.omitted || normalized.displayed !== normalized.complete + normalized.partial) {
    throw schemaError(path, 'route counts do not agree');
  }
  return normalized;
}

function ownValue(value, key) {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

function goJSONStringBytes(value, path) {
  let encoded = 2;
  let utf8 = 0;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code >= 0xd800 && code <= 0xdbff) {
      const low = index + 1 < value.length ? value.charCodeAt(index + 1) : 0;
      if (low < 0xdc00 || low > 0xdfff) throw schemaError(path, 'malformed UTF-16 surrogate');
      encoded += 4;
      utf8 += 4;
      index++;
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      throw schemaError(path, 'malformed UTF-16 surrogate');
    } else {
      utf8 += code <= 0x7f ? 1 : code <= 0x7ff ? 2 : 3;
      if (code === 0x22 || code === 0x5c || code === 0x08 || code === 0x09 || code === 0x0a || code === 0x0c || code === 0x0d) encoded += 2;
      else if (code < 0x20 || code === 0x3c || code === 0x3e || code === 0x26 || code === 0x2028 || code === 0x2029) encoded += 6;
      else encoded += code <= 0x7f ? 1 : code <= 0x7ff ? 2 : 3;
    }
    if (utf8 > COMPACT_LIMITS.maxGeoBundleBytes) throw schemaError(path, 'Geo string exceeds byte limit');
  }
  return encoded;
}

function goJSONNumberBytes(value) {
  return (Object.is(value, -0) ? '-0' : String(value)).length;
}

function compactGeoBundleBytes(hasGeo, city, region, country, countryCode, latitude, longitude, hasASN, asnNumber, organization, path) {
  let bytes = 0;
  if (hasGeo) {
    bytes += ',"geolocation":{'.length;
    let first = true;
    for (const [key, value, fieldPath] of [
      ['city', city, `${path}.geolocation.city`],
      ['region', region, `${path}.geolocation.region`],
      ['country', country, `${path}.geolocation.country`],
      ['country_code', countryCode, `${path}.geolocation.country_code`]
    ]) {
      if (value === '') continue;
      bytes += (first ? 0 : 1) + key.length + 3 + goJSONStringBytes(value, fieldPath);
      first = false;
    }
    bytes += (first ? 0 : 1) + 'latitude'.length + 3 + goJSONNumberBytes(latitude);
    bytes += 1 + 'longitude'.length + 3 + goJSONNumberBytes(longitude) + 1;
  }
  if (hasASN) {
    bytes += ',"asn":{'.length;
    let first = true;
    if (asnNumber !== 0) {
      bytes += 'number'.length + 3 + goJSONNumberBytes(asnNumber);
      first = false;
    }
    if (organization !== '') {
      bytes += (first ? 0 : 1) + 'organization'.length + 3 + goJSONStringBytes(organization, `${path}.asn.organization`);
    }
    bytes++;
  }
  return bytes;
}

function normalizeCompactTopology(value, path = 'compact_topology', reportResults = null) {
  const source = object(value, path);
  if (source.schema !== 'compact-v1') throw schemaError(`${path}.schema`, 'unsupported value');
  if (source.selection !== 'fair-complete-prefix-v1') throw schemaError(`${path}.selection`, 'unsupported value');

  const limitSource = object(source.limits, `${path}.limits`);
  const limits = {
    nodes: integer(limitSource.nodes, `${path}.limits.nodes`, 0, COMPACT_LIMITS.nodes),
    links: integer(limitSource.links, `${path}.limits.links`, 0, COMPACT_LIMITS.links),
    max_response_bytes_exclusive: integer(limitSource.max_response_bytes_exclusive, `${path}.limits.max_response_bytes_exclusive`, 1, COMPACT_LIMITS.maxResponseBytesExclusive),
    max_geo_bundle_bytes: integer(limitSource.max_geo_bundle_bytes, `${path}.limits.max_geo_bundle_bytes`, 1, COMPACT_LIMITS.maxGeoBundleBytes)
  };
  if (limits.nodes !== COMPACT_LIMITS.nodes || limits.links !== COMPACT_LIMITS.links ||
      limits.max_response_bytes_exclusive !== COMPACT_LIMITS.maxResponseBytesExclusive || limits.max_geo_bundle_bytes !== COMPACT_LIMITS.maxGeoBundleBytes) {
    throw schemaError(`${path}.limits`, 'unsupported compact-v1 limits');
  }

  const nodes = array(source.nodes, `${path}.nodes`, COMPACT_LIMITS.nodes).map((entry, index) => {
    const itemPath = `${path}.nodes[${index}]`;
    const item = object(entry, itemPath);
    const normalized = {
      id: text(item.id, `${itemPath}.id`),
      kind: enumValue(item.kind, COMPACT_NODE_KINDS, `${itemPath}.kind`),
      address: optionalText(item.address, `${itemPath}.address`),
      status: enumValue(item.status, TOPOLOGY_STATUSES, `${itemPath}.status`),
      hop_min: integer(item.hop_min, `${itemPath}.hop_min`, 0, 255),
      hop_max: integer(item.hop_max, `${itemPath}.hop_max`, 0, 255),
      observations: integer(item.observations, `${itemPath}.observations`, 1, SCHEMA_LIMITS.reportTopologyNodes)
    };
    const publicIP = ownValue(item, 'public_ip');
    if (publicIP !== undefined) normalized.public_ip = boolean(publicIP, `${itemPath}.public_ip`);
    if (normalized.hop_min > normalized.hop_max) throw schemaError(itemPath, 'hop_min exceeds hop_max');
    const latency = ownValue(item, 'latency_ms_avg');
    if (latency !== undefined) normalized.latency_ms_avg = finite(latency, `${itemPath}.latency_ms_avg`, 0, Number.MAX_SAFE_INTEGER);
    const geolocationValue = ownValue(item, 'geolocation');
    const asnValue = ownValue(item, 'asn');
    const hasGeo = geolocationValue !== undefined && geolocationValue !== null;
    const hasASN = asnValue !== undefined && asnValue !== null;
    if ((hasGeo || hasASN) && normalized.public_ip !== true) throw schemaError(itemPath, 'Geo bundle requires public_ip true');
    let city = '';
    let region = '';
    let country = '';
    let countryCode = '';
    let latitude = 0;
    let longitude = 0;
    let asnNumber = 0;
    let organization = '';
    if (hasGeo) {
      const geo = object(geolocationValue, `${itemPath}.geolocation`);
      city = optionalText(ownValue(geo, 'city'), `${itemPath}.geolocation.city`);
      region = optionalText(ownValue(geo, 'region'), `${itemPath}.geolocation.region`);
      country = optionalText(ownValue(geo, 'country'), `${itemPath}.geolocation.country`);
      countryCode = optionalText(ownValue(geo, 'country_code'), `${itemPath}.geolocation.country_code`);
      latitude = finite(ownValue(geo, 'latitude'), `${itemPath}.geolocation.latitude`, -90, 90);
      longitude = finite(ownValue(geo, 'longitude'), `${itemPath}.geolocation.longitude`, -180, 180);
      normalized.geolocation = { city, region, country, country_code: countryCode, latitude, longitude };
    }
    if (hasASN) {
      const asn = object(asnValue, `${itemPath}.asn`);
      const rawASNNumber = ownValue(asn, 'number');
      asnNumber = integer(rawASNNumber === undefined ? 0 : rawASNNumber, `${itemPath}.asn.number`, 0, 4294967295);
      organization = optionalText(ownValue(asn, 'organization'), `${itemPath}.asn.organization`);
      normalized.asn = { number: asnNumber, organization };
    }
    if (compactGeoBundleBytes(
      hasGeo, city, region, country, countryCode, latitude, longitude,
      hasASN, asnNumber, organization, itemPath
    ) > limits.max_geo_bundle_bytes) {
      throw schemaError(itemPath, 'Geo bundle exceeds byte limit');
    }
    return normalized;
  });
  const nodeIDs = new Set(nodes.map(node => node.id));
  if (nodeIDs.size !== nodes.length) throw schemaError(`${path}.nodes`, 'node ids must be unique');

  const links = array(source.links, `${path}.links`, COMPACT_LIMITS.links).map((entry, index) => {
    const itemPath = `${path}.links[${index}]`;
    const item = object(entry, itemPath);
    const normalized = {
      from: text(item.from, `${itemPath}.from`), to: text(item.to, `${itemPath}.to`),
      status: enumValue(item.status, TOPOLOGY_STATUSES, `${itemPath}.status`),
      observations: integer(item.observations, `${itemPath}.observations`, 1, SCHEMA_LIMITS.reportTopologyLinks)
    };
    if (!nodeIDs.has(normalized.from) || !nodeIDs.has(normalized.to)) throw schemaError(itemPath, 'link references an unknown node');
    if (normalized.from === normalized.to) throw schemaError(itemPath, 'self links are not supported');
    return normalized;
  });
  const linkKeys = new Set();
  for (const link of links) {
    const key = `${link.from}\u0000${link.to}`;
    if (linkKeys.has(key)) throw schemaError(`${path}.links`, 'directed links must be unique');
    linkKeys.add(key);
  }

  const routeObservationCounts = [];
  const routes = array(source.routes, `${path}.routes`, COMPACT_MAX_ROUTES).map((entry, index) => {
    const itemPath = `${path}.routes[${index}]`;
    const item = object(entry, itemPath);
    const rawNodeIDs = array(item.node_ids, `${itemPath}.node_ids`, 32).map((id, nodeIndex) => text(id, `${itemPath}.node_ids[${nodeIndex}]`));
    if (rawNodeIDs.length === 0 || rawNodeIDs.some(id => !nodeIDs.has(id))) throw schemaError(itemPath, 'route references an unknown node');
    if (rawNodeIDs.some((id, nodeIndex) => nodeIndex > 0 && id === rawNodeIDs[nodeIndex - 1])) {
      throw schemaError(`${itemPath}.node_ids`, 'consecutive node IDs must differ');
    }
    for (let nodeIndex = 1; nodeIndex < rawNodeIDs.length; nodeIndex++) {
      if (!linkKeys.has(`${rawNodeIDs[nodeIndex - 1]}\u0000${rawNodeIDs[nodeIndex]}`)) throw schemaError(itemPath, 'route edge has no matching directed link');
    }
    const result_index = integer(item.result_index, `${itemPath}.result_index`, 0, SCHEMA_LIMITS.results - 1);
    if (reportResults && (!reportResults[result_index] || reportResults[result_index].kind !== 'traceroute')) {
      throw schemaError(`${itemPath}.result_index`, 'route requires a traceroute result');
    }
    const status = enumValue(item.status, STATUSES, `${itemPath}.status`);
    const reached = boolean(item.reached, `${itemPath}.reached`);
    if (reached !== (status !== 'unreachable')) throw schemaError(itemPath, 'reached contradicts status');
    routeObservationCounts.push({ result_index, nodes: rawNodeIDs.length, links: Math.max(0, rawNodeIDs.length - 1) });
    return {
      result_index,
      attempt: integer(item.attempt, `${itemPath}.attempt`, 1, 10),
      status,
      reached, complete: boolean(item.complete, `${itemPath}.complete`), node_ids: rawNodeIDs
    };
  });

  const statsSource = object(source.stats, `${path}.stats`);
  const stats = {
    nodes: normalizeCompactCount(statsSource.nodes, `${path}.stats.nodes`, SCHEMA_LIMITS.reportTopologyNodes),
    links: normalizeCompactCount(statsSource.links, `${path}.stats.links`, SCHEMA_LIMITS.reportTopologyLinks),
    routes: normalizeCompactRouteCount(statsSource.routes, `${path}.stats.routes`),
    node_observations: normalizeCompactCount(statsSource.node_observations, `${path}.stats.node_observations`, SCHEMA_LIMITS.reportTopologyNodes),
    link_observations: normalizeCompactCount(statsSource.link_observations, `${path}.stats.link_observations`, SCHEMA_LIMITS.reportTopologyLinks)
  };
  if (stats.nodes.displayed !== nodes.length || stats.links.displayed !== links.length || stats.routes.displayed !== routes.length ||
      stats.routes.complete !== routes.filter(route => route.complete).length || stats.routes.partial !== routes.filter(route => !route.complete).length ||
      stats.link_observations.displayed !== links.reduce((sum, link) => sum + link.observations, 0)) {
    throw schemaError(`${path}.stats`, 'displayed counts do not match compact arrays');
  }

  const result_stats = array(source.result_stats ?? [], `${path}.result_stats`, SCHEMA_LIMITS.results).map((entry, index) => {
    const itemPath = `${path}.result_stats[${index}]`; const item = object(entry, itemPath);
    return {
      result_index: integer(item.result_index, `${itemPath}.result_index`, 0, SCHEMA_LIMITS.results - 1),
      routes: normalizeCompactRouteCount(item.routes, `${itemPath}.routes`),
      node_observations: normalizeCompactCount(item.node_observations, `${itemPath}.node_observations`, SCHEMA_LIMITS.reportTopologyNodes),
      link_observations: normalizeCompactCount(item.link_observations, `${itemPath}.link_observations`, SCHEMA_LIMITS.reportTopologyLinks)
    };
  });
  if (new Set(result_stats.map(item => item.result_index)).size !== result_stats.length) throw schemaError(`${path}.result_stats`, 'result indexes must be unique');
  if (reportResults) {
    for (const item of result_stats) {
      const result = reportResults[item.result_index];
      if (!result) throw schemaError(`${path}.result_stats`, 'references unknown result');
      if (result.kind !== 'traceroute' && [
        ...Object.values(item.routes),
        ...Object.values(item.node_observations),
        ...Object.values(item.link_observations)
      ].some(count => count !== 0)) {
        throw schemaError(`${path}.result_stats`, 'non-traceroute counts must be zero');
      }
    }
  }
  const sum = (field, subfield) => result_stats.reduce((total, item) => total + item[field][subfield], 0);
  for (const field of ['routes', 'node_observations', 'link_observations']) {
    for (const subfield of field === 'routes' ? ['total', 'displayed', 'complete', 'partial', 'omitted'] : ['total', 'displayed', 'omitted']) {
      if (sum(field, subfield) !== stats[field][subfield]) throw schemaError(`${path}.result_stats`, 'per-result counts do not match global stats');
    }
  }
  const routeCounts = new Map();
  for (const route of routes) {
    const counts = routeCounts.get(route.result_index) ?? { displayed: 0, complete: 0, partial: 0 };
    counts.displayed++;
    counts[route.complete ? 'complete' : 'partial']++;
    routeCounts.set(route.result_index, counts);
  }
  for (const item of result_stats) {
    const counts = routeCounts.get(item.result_index) ?? { displayed: 0, complete: 0, partial: 0 };
    if (item.routes.displayed !== counts.displayed || item.routes.complete !== counts.complete || item.routes.partial !== counts.partial) {
      throw schemaError(`${path}.result_stats`, 'displayed routes do not match route array');
    }
    const observations = routeObservationCounts.filter(route => route.result_index === item.result_index);
    const displayedNodes = observations.reduce((total, route) => total + route.nodes, 0);
    const displayedLinks = observations.reduce((total, route) => total + route.links, 0);
    if (item.node_observations.displayed !== displayedNodes || item.link_observations.displayed !== displayedLinks) {
      throw schemaError(`${path}.result_stats`, 'displayed observations do not match route prefixes');
    }
  }

  const geoSource = object(source.geo, `${path}.geo`);
  const geo = {
    eligible: integer(geoSource.eligible, `${path}.geo.eligible`, 0, COMPACT_LIMITS.nodes),
    available: integer(geoSource.available, `${path}.geo.available`, 0, COMPACT_LIMITS.nodes),
    included: integer(geoSource.included, `${path}.geo.included`, 0, COMPACT_LIMITS.nodes),
    omitted: integer(geoSource.omitted, `${path}.geo.omitted`, 0, COMPACT_LIMITS.nodes),
    unavailable: integer(geoSource.unavailable, `${path}.geo.unavailable`, 0, COMPACT_LIMITS.nodes)
  };
  const eligible = nodes.filter(node => node.public_ip).length;
  const included = nodes.filter(node => node.public_ip && (node.geolocation || node.asn)).length;
  if (geo.eligible !== eligible || geo.eligible !== geo.available + geo.unavailable || geo.available !== geo.included + geo.omitted || geo.included !== included) {
    throw schemaError(`${path}.geo`, 'Geo counts do not match nodes');
  }

  const reasons = array(source.truncation_reasons ?? [], `${path}.truncation_reasons`, COMPACT_TRUNCATION_REASONS.length)
    .map((reason, index) => enumValue(reason, new Set(COMPACT_TRUNCATION_REASONS), `${path}.truncation_reasons[${index}]`));
  if (new Set(reasons).size !== reasons.length || reasons.some((reason, index) => index > 0 && COMPACT_TRUNCATION_REASONS.indexOf(reason) <= COMPACT_TRUNCATION_REASONS.indexOf(reasons[index - 1]))) {
    throw schemaError(`${path}.truncation_reasons`, 'reasons must be unique and in fixed order');
  }
  const truncated = boolean(source.truncated, `${path}.truncated`);
  if (truncated !== (reasons.length > 0)) throw schemaError(`${path}.truncated`, 'does not match truncation reasons');
  return { schema: 'compact-v1', selection: 'fair-complete-prefix-v1', limits, nodes, links, routes, stats, result_stats, geo, truncated, truncation_reasons: reasons };
}

function normalizedTopologyStatus(topology) {
  if (!topology.reached) return 'unreachable';
  return topology.nodes.some(node => node.status === 'unknown' || node.status === 'degraded') ? 'degraded' : 'healthy';
}

function normalizeTraceAttempt(value, path) {
  const source = object(value, path);
  const normalized = {
    attempt: integer(source.attempt, `${path}.attempt`, 1, 10),
    status: enumValue(source.status, STATUSES, `${path}.status`),
    error_code: optionalText(source.error_code, `${path}.error_code`, { max: SCHEMA_LIMITS.errorCode }),
    message: optionalText(source.message, `${path}.message`)
  };
  if (normalized.error_code && !['timeout', 'cancelled', 'traceroute_failed'].includes(normalized.error_code)) throw schemaError(`${path}.error_code`, 'unsupported trace error');
  if (source.topology !== undefined && source.topology !== null) normalized.topology = normalizeTopology(source.topology, `${path}.topology`);
  if (normalized.error_code && normalized.status !== 'unreachable') throw schemaError(path, 'trace error contradicts status');
  if (!normalized.error_code && (!normalized.topology || normalized.status !== normalizedTopologyStatus(normalized.topology))) throw schemaError(path, 'trace status contradicts topology');
  return normalized;
}

function normalizeDetails(value, path) {
  const source = object(value, path);
  const keys = Object.keys(source);
  if (keys.length > SCHEMA_LIMITS.detailKeys) throw schemaError(path, 'too many keys');
  const result = {};
  for (const key of keys) {
    if (key === '__proto__' || key === 'prototype' || key === 'constructor') continue;
    if (key.length > 128) throw schemaError(path, 'key too long');
    if (key === 'topology') result[key] = normalizeTopology(source[key], `${path}.topology`);
    else if (key === 'attempts') result[key] = array(source[key], `${path}.attempts`, 10).map((entry, index) => normalizeTraceAttempt(entry, `${path}.attempts[${index}]`));
    else result[key] = normalizeJSON(source[key], `${path}.${key}`, 1);
  }
  return result;
}

function normalizeJSON(value, path, depth = 0) {
  if (depth > SCHEMA_LIMITS.detailDepth) throw schemaError(path, 'nested too deeply');
  if (value === null || typeof value === 'boolean') return value;
  if (typeof value === 'string') return text(value, path, { required: false });
  if (typeof value === 'number') return finite(value, path, -Number.MAX_VALUE);
  if (Array.isArray(value)) return array(value, path, SCHEMA_LIMITS.detailArray).map((entry, index) => normalizeJSON(entry, `${path}[${index}]`, depth + 1));
  const source = object(value, path);
  const keys = Object.keys(source);
  if (keys.length > SCHEMA_LIMITS.detailKeys) throw schemaError(path, 'too many keys');
  const result = {};
  for (const key of keys) {
    if (key === '__proto__' || key === 'prototype' || key === 'constructor') continue;
    if (key.length > 128) throw schemaError(path, 'key too long');
    result[key] = key === 'topology' ? normalizeTopology(source[key], `${path}.topology`) : normalizeJSON(source[key], `${path}.${key}`, depth + 1);
  }
  return result;
}

function normalizeResult(value, index = 0) {
  const path = `results[${index}]`; const source = object(value, path);
  const normalized = {
    kind: enumValue(source.kind, KINDS, `${path}.kind`), address: text(source.address, `${path}.address`, { required: false }),
    status: enumValue(source.status, STATUSES, `${path}.status`), latency_ms: integer(source.latency_ms, `${path}.latency_ms`),
    started_at: timestamp(source.started_at, `${path}.started_at`), error_code: optionalText(source.error_code, `${path}.error_code`, { max: SCHEMA_LIMITS.errorCode }),
    message: optionalText(source.message, `${path}.message`)
  };
  if (source.details !== undefined && source.details !== null) normalized.details = normalizeDetails(source.details, `${path}.details`);
  return normalized;
}

function enforceReportBudget(value) {
  const stack = [{ value, key: '' }]; const seen = new WeakSet();
  let containers = 0; let stringChars = 0; let topologyNodes = 0; let topologyLinks = 0;
  while (stack.length) {
    const entry = stack.pop(); const current = entry.value;
    if (typeof current === 'string') {
      stringChars += current.length;
      if (stringChars > SCHEMA_LIMITS.reportStringChars) throw schemaError('report', 'aggregate string budget exceeded');
      continue;
    }
    if (current === null || typeof current !== 'object') continue;
    if (seen.has(current)) throw schemaError('report', 'duplicate or cyclic object exceeds aggregate budget');
    seen.add(current); containers++;
    if (containers > SCHEMA_LIMITS.reportContainers) throw schemaError('report', 'aggregate container budget exceeded');
    if (Array.isArray(current) && entry.key === 'nodes') topologyNodes += current.length;
    if (Array.isArray(current) && entry.key === 'links') topologyLinks += current.length;
    if (topologyNodes > SCHEMA_LIMITS.reportTopologyNodes || topologyLinks > SCHEMA_LIMITS.reportTopologyLinks) throw schemaError('report', 'aggregate topology budget exceeded');
    for (const key of Object.keys(current)) {
      const descriptor = Object.getOwnPropertyDescriptor(current, key);
      if (!descriptor || !Object.hasOwn(descriptor, 'value')) throw schemaError('report', 'accessor properties are not supported');
      stack.push({ value: descriptor.value, key });
    }
  }
}

function normalizeReport(value) {
  enforceReportBudget(value);
  const source = object(value, 'report');
  const results = array(source.results, 'report.results', SCHEMA_LIMITS.results).map(normalizeResult);
  const summarySource = object(source.summary, 'report.summary');
  const summary = { total: integer(summarySource.total, 'report.summary.total', 0, SCHEMA_LIMITS.results), passed: integer(summarySource.passed, 'report.summary.passed', 0, SCHEMA_LIMITS.results), failed: integer(summarySource.failed, 'report.summary.failed', 0, SCHEMA_LIMITS.results) };
  if (summary.total !== results.length || summary.passed + summary.failed !== summary.total) throw schemaError('report.summary', 'counts do not match results');
  const passed = results.filter(result => result.status === 'healthy').length;
  if (summary.passed !== passed || summary.failed !== results.length - passed) throw schemaError('report.summary', 'counts contradict result statuses');
  const status = enumValue(source.status, STATUSES, 'report.status');
  const hasDegraded = results.some(result => result.status === 'degraded');
  const expectedStatus = summary.failed === 0 ? 'healthy' : (hasDegraded || summary.passed > 0 ? 'degraded' : 'unreachable');
  if (status !== expectedStatus) throw schemaError('report.status', 'contradicts result statuses');
  const normalizedAnalysis = source.analysis === undefined || source.analysis === null ? null : normalizeAnalysis(source.analysis);
  const compactTopology = source.compact_topology === undefined ? null : normalizeCompactTopology(source.compact_topology, 'report.compact_topology', results);
  if (compactTopology) {
    if (compactTopology.result_stats.length !== results.length || compactTopology.result_stats.some((item, index) => item.result_index !== index)) {
      throw schemaError('report.compact_topology.result_stats', 'must contain one ordered entry per result');
    }
    if (compactTopology.routes.some(route => route.result_index >= results.length)) throw schemaError('report.compact_topology.routes', 'references unknown result');
  }
  if (normalizedAnalysis) {
    for (const evidence of normalizedAnalysis.evidence) {
      if (evidence.result_index >= results.length) throw schemaError('report.analysis.evidence', 'references unknown result');
    }
    for (const issue of [...normalizedAnalysis.coverage.provider_failures, ...normalizedAnalysis.coverage.limitations]) {
      if (issue.result_index >= results.length) throw schemaError('report.analysis.coverage', 'references unknown result');
    }
  }
  return {
    id: text(source.id, 'report.id'), status,
    started_at: timestamp(source.started_at, 'report.started_at'), duration_ms: integer(source.duration_ms, 'report.duration_ms'),
    summary, results, analysis: normalizedAnalysis, compact_topology: compactTopology
  };
}

export {
  REQUEST_PHASES, ERROR_KINDS, SCHEMA_LIMITS,
  createRequestLane, canonicalDiagnosticsInput, canonicalTopologyInput, inputSignature,
  transitionRequest, ownsRequest, clientTimeoutMS, parseRetryAfter, normalizeRequestError, parseResponse,
  normalizeReport, normalizeAnalysis, normalizeResult, normalizeTopology, normalizeCompactTopology
};
