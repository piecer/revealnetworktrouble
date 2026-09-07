import {
  createRequestLane, canonicalDiagnosticsInput, canonicalTopologyInput, inputSignature,
  transitionRequest, ownsRequest, clientTimeoutMS, normalizeRequestError, parseResponse, SCHEMA_LIMITS
} from './state.js';
import { canonicalIP, topologyModelFromReport, filterTopologyModel } from './topology-model.js';
import { mountGeoMap } from './geo-map.js';
import { TopologyRenderCoordinator } from './topology-renderer.js';
import { createViewTransform, resetViewTransform, updateViewTransform, normalizeViewport } from './topology-visualizer.js';

const IP_LABEL_STORAGE_KEY = 'checknetwork.ip-labels.v1';
export const LABEL_FILE = 1024 * 1024;
export const LABEL_RECORDS = 500;
export const LABEL_PAGE = 100;
export const LABEL = 256;
export const NOTE = 1024;
export const MAX_TARGETS = 20;

const placeholders = {
  dns: 'example.com', tcp: '1.1.1.1:443', http: 'http://example.com', https: 'https://example.com', traceroute: 'example.com',
  ssh: 'example.com', smtp: 'mail.example.com', submission: 'mail.example.com', smtps: 'mail.example.com',
  imap: 'mail.example.com', imaps: 'mail.example.com', pop3: 'mail.example.com', pop3s: 'mail.example.com'
};

function canonicalIPAddress(value) {
  const address = String(value ?? '').trim();
  return canonicalIP(address) ?? '';
}

function compareCanonicalIP(left, right) {
  left = canonicalIPAddress(left); right = canonicalIPAddress(right);
  const leftV4 = !left.includes(':'); const rightV4 = !right.includes(':');
  if (leftV4 !== rightV4) return leftV4 ? -1 : 1;
  if (leftV4) {
    const a = left.split('.').map(Number); const b = right.split('.').map(Number);
    for (let index = 0; index < 4; index++) if (a[index] !== b[index]) return a[index] - b[index];
    return 0;
  }
  const sortWords = value => {
    const [address, ...zone] = value.split('%');
    const halves = address.split('::');
    const before = halves[0] ? halves[0].split(':').map(word => Number.parseInt(word, 16)) : [];
    const after = halves.length > 1 && halves[1] ? halves[1].split(':').map(word => Number.parseInt(word, 16)) : [];
    return { words: [...before, ...Array(8 - before.length - after.length).fill(0), ...after], zone: zone.join('%') };
  };
  const a = sortWords(left); const b = sortWords(right);
  for (let index = 0; index < 8; index++) if (a.words[index] !== b.words[index]) return a.words[index] - b.words[index];
  return a.zone.localeCompare(b.zone);
}

function truncateCharacters(value, maximum) {
  return [...String(value ?? '').trim()].slice(0, maximum).join('');
}

function isIPAddress(value) {
  return canonicalIPAddress(value) !== '';
}

function parseCSVLine(line) {
  const fields = []; let value = ''; let quoted = false;
  for (let index = 0; index < line.length; index++) {
    const char = line[index];
    if (char === '"' && quoted && line[index + 1] === '"') { value += '"'; index++; }
    else if (char === '"') quoted = !quoted;
    else if (char === ',' && !quoted) { fields.push(value.trim()); value = ''; }
    else value += char;
  }
  fields.push(value.trim());
  return fields;
}

function parseIPLabelImport(text, filename = '') {
  if (filename.toLowerCase().endsWith('.json') || String(text).trim().startsWith('{') || String(text).trim().startsWith('[')) {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) return parsed.map(row => ({ ip: row.ip || row.address, label: row.label || row.name, note: row.note || row.description || '' }));
    return Object.entries(parsed).map(([ip, value]) => typeof value === 'string' ? { ip, label: value, note: '' } : { ip, label: value?.label || value?.name, note: value?.note || value?.description || '' });
  }
  const lines = String(text).split(/\r?\n/).filter(line => line.trim());
  if (!lines.length) return [];
  const first = parseCSVLine(lines[0]).map(value => value.toLowerCase());
  const hasHeader = first.includes('ip') || first.includes('address');
  const ipIndex = hasHeader ? Math.max(first.indexOf('ip'), first.indexOf('address')) : 0;
  const labelIndex = hasHeader ? Math.max(first.indexOf('label'), first.indexOf('name')) : 1;
  const noteIndex = hasHeader ? Math.max(first.indexOf('note'), first.indexOf('description')) : 2;
  return lines.slice(hasHeader ? 1 : 0).map(line => {
    const fields = parseCSVLine(line);
    return { ip: fields[ipIndex], label: fields[labelIndex], note: noteIndex >= 0 ? fields[noteIndex] || '' : '' };
  });
}


// Duplicate policy: the last valid plain record for a canonical IP wins.
export function normalizeIPLabelRows(rows, maximum = LABEL_RECORDS) {
  const accepted = new Map(); let invalid = 0; let duplicates = 0;
  if (!Array.isArray(rows)) return { labels: accepted, invalid: 1, omitted: 0 };
  for (const row of rows) {
    const prototype = row && typeof row === 'object' ? Object.getPrototypeOf(row) : undefined;
    if (!row || typeof row !== 'object' || Array.isArray(row) || (prototype !== Object.prototype && prototype !== null)) { invalid++; continue; }
    const ip = canonicalIPAddress(row.ip); const label = truncateCharacters(row.label, LABEL);
    if (!ip || !label) { invalid++; continue; }
    if (accepted.has(ip)) duplicates++;
    accepted.set(ip, { ip, label, note: truncateCharacters(row.note, NOTE) });
  }
  const sorted = [...accepted.entries()].sort(([left], [right]) => compareCanonicalIP(left, right));
  const kept = sorted.slice(0, maximum);
  return { labels: new Map(kept), invalid, omitted: duplicates + Math.max(0, sorted.length - kept.length) };
}

function escapeHTML(value) {
  return String(value ?? '').replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&#39;');
}

function sanitizeCredentialReflection(value, credential) {
  if (!credential) return value;
  const escaped = credential.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const pattern = new RegExp(escaped, 'gi');
  const credentialLower = credential.toLowerCase();
  const copy = current => {
    if (typeof current === 'string') return current.replace(pattern, '[REDACTED CREDENTIAL]');
    if (Array.isArray(current)) return current.map(copy);
    if (current && typeof current === 'object') {
      const result = {};
      for (const [key, entry] of Object.entries(current)) {
        if (key.toLowerCase().includes(credentialLower)) throw new TypeError('response reflected credential in a property name');
        result[key] = copy(entry);
      }
      return result;
    }
    return current;
  };
  return copy(value);
}

const PURPOSE_UI = {
  diagnostics: { workspace: '#diagnostics-workspace', message: '#diagnostics-state-message', form: '#check-form', run: '#run', cancel: '#cancel-diagnostics', report: '#report-section', error: '#error', download: '#download' },
  topology: { workspace: '#topology-workspace', message: '#topology-state-message', form: '#topology-form', run: '#run-topology', cancel: '#cancel-topology', report: '#topology-report-section', error: '#topology-error', download: '#download-topology' }
};
const AUTH_PREFIX = 'checknetwork.bearer.v1:';
const CREDENTIAL_FAILURE_MESSAGE = 'Credential 업데이트에 실패했습니다. 이전 credential은 변경되지 않았습니다.';
const CREDENTIAL_RECONCILIATION_MESSAGE = 'Credential 상태를 확인할 수 없습니다. 인증 요청 전에 credential을 다시 적용하거나 삭제해 주세요.';

export const PRESENTATION_SEMANTIC_KEYS = Object.freeze([
  'cause', 'supporting_evidence', 'expectation', 'evidence_directness', 'coverage_limitation', 'next_action'
]);
export const SERVER_GREETING_SCOPE = 'Expected server-first greeting observed; command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.';
export const MAX_DOCUMENT_ELEMENTS = 1200;
const FINDING_ACCORDIONS = new WeakMap();
const LABEL_ROOT_OWNERS = new WeakMap();

function findingPresentation(code, cause, expectation, limitation, action, signals = ['error_code']) {
  const key = `finding.${code}`;
  return Object.freeze({ key, cause, expectation, limitation, action, signals: Object.freeze(signals), actionRelationship: `action.${code}` });
}

export const FINDING_PRESENTATION_REGISTRY = Object.freeze(Object.fromEntries([
  findingPresentation('checker_capacity_unavailable', 'Checker execution capacity was unavailable, so no service observation was established.', 'A bounded checker execution is admitted and completes.', 'This finding establishes an execution-capacity condition, not endpoint health.', 'Retry after bounded checker capacity becomes available.'),
  findingPresentation('checker_panic', 'The checker stopped unexpectedly before service behavior was established.', 'The bounded checker completes and produces an observation.', 'This finding establishes a checker runtime failure, not endpoint health.', 'Repeat the bounded check and escalate recurring runtime failures.'),
  findingPresentation('dns_resolution_failed', 'Name resolution did not produce a successful observation.', 'The configured name resolves to at least one intended address.', 'This finding covers DNS resolution only; downstream connectivity was not established.', 'Compare the configured resolver with an approved known-good resolver.'),
  findingPresentation('endpoint_connect_failed', 'The endpoint connection did not complete.', 'The intended endpoint accepts a connection from this observation point.', 'This finding covers connection establishment only; application behavior was not tested.', 'Verify the listener and access policy, then repeat from the same observation point.'),
  findingPresentation('execution_cancelled', 'The check ended after cancellation.', 'The check remains active until a bounded observation completes.', 'Cancellation does not establish service health or failure.', 'Repeat the check when it can remain active through completion.', ['error_code', 'traceroute.attempts_cancelled']),
  findingPresentation('execution_timeout', 'The check did not complete before its bounded deadline.', 'The check completes before the configured deadline.', 'A timeout does not identify whether the endpoint, path, or checker caused the delay.', 'Repeat once with the same bounded timeout and verify endpoint availability independently.', ['error_code', 'traceroute.attempts_timed_out']),
  findingPresentation('http_unexpected_status', 'The observed HTTP status did not match the configured status expectation.', 'The endpoint returns the configured HTTP status.', 'Only the bounded HTTP status observation was compared; end-to-end application health was not established.', 'Verify the route, health endpoint, and configured status expectation.', ['http.status_code', 'error_code']),
  findingPresentation('invalid_target', 'The target was rejected before a network observation was possible.', 'The target uses accepted syntax, scheme, hostname, and port fields.', 'No network or service-health conclusion can be drawn from rejected input.', 'Correct the target fields and run the bounded check again.'),
  findingPresentation('service_greeting_unverified', 'The expected server-first greeting was not verified after transport connection.', 'The intended service returns its expected bounded server-first greeting without client input.', 'Command, authentication, STARTTLS, mailbox, and end-to-end service behavior were not tested.', 'Verify the intended listener and server-first greeting, then repeat the check.'),
  findingPresentation('target_policy_blocked', 'Deployment policy blocked the target before a connection was attempted.', 'The target is permitted by the selected deployment policy.', 'No endpoint-health conclusion can be drawn because policy prevented network observation.', 'Use an approved target or an explicitly authorized trusted-local deployment.'),
  findingPresentation('tls_certificate_expired', 'The observed TLS certificate was expired at verification time.', 'The endpoint presents a certificate valid at verification time.', 'Certificate validity does not by itself establish full application or service health.', 'Renew and deploy the intended certificate chain, then repeat verification.', ['error_code', 'tls.certificate_expires_at']),
  findingPresentation('tls_certificate_expiring', 'The observed TLS certificate expires within the bounded renewal window.', 'The endpoint presents a currently valid certificate with adequate renewal margin.', 'The renewal-window observation does not verify that renewal or deployment will succeed.', 'Confirm the certificate renewal schedule and deployed chain.', ['tls.certificate_expires_at']),
  findingPresentation('tls_certificate_not_yet_valid', 'The observed TLS certificate was not yet valid at verification time.', 'The endpoint presents a certificate valid at verification time.', 'The observation does not distinguish deployment error from clock error.', 'Verify certificate activation, deployment, and system clocks.'),
  findingPresentation('tls_downgrade', 'Verified TLS was not preserved for the HTTPS observation.', 'Every redirect and final response preserves verified TLS.', 'The observation is limited to the bounded redirect and response path.', 'Inspect redirects and the final endpoint for verified TLS.'),
  findingPresentation('tls_handshake_failed', 'The transport connected but the TLS handshake did not complete.', 'The TLS handshake completes with the intended endpoint.', 'Application behavior was not tested because TLS establishment failed.', 'Verify certificate chain, server name, protocol versions, and cipher compatibility.'),
  findingPresentation('tls_hostname_mismatch', 'The TLS certificate did not verify for the requested server name.', 'Certificate verification succeeds for the requested server name.', 'This identity failure does not establish other endpoint behavior.', 'Correct certificate identity or endpoint routing, then repeat verification.'),
  findingPresentation('tls_untrusted', 'The TLS certificate chain did not verify to an approved trust anchor.', 'The endpoint chain verifies to an approved trust anchor.', 'This trust-chain result does not establish full application behavior.', 'Deploy the intended complete chain and verify its trust anchor.'),
  findingPresentation('traceroute_execution_failed', 'One or more traceroute command attempts did not produce a completed route observation.', 'Every bounded command attempt completes with parseable route evidence.', 'Failed command attempts provide no route or destination-health evidence.', 'Verify traceroute availability and permissions, then repeat the bounded trace.', ['traceroute.attempts_execution_failed', 'traceroute.execution_failures']),
  findingPresentation('traceroute_partial_reachability', 'Destination reachability varied across completed traceroute attempts.', 'Completed attempts show consistent reachability or a reproducible divergence.', 'Traceroute reachability is path evidence, not end-to-end application health.', 'Compare reached and unreached routes for the first stable divergence.', ['traceroute.attempts_reached']),
  findingPresentation('traceroute_path_degraded', 'A completed route contained a producer-classified degraded segment.', 'Repeated completed routes establish whether the same segment remains degraded.', 'A degraded route segment does not by itself identify root cause or application impact.', 'Repeat from the same and another approved observation point.', ['traceroute.path_status']),
  findingPresentation('traceroute_path_unstable', 'Successful completed traceroute attempts contained more than one path sequence.', 'The path stabilizes or the same divergence is reproduced.', 'Path variation alone does not establish packet loss or application failure.', 'Compare the first divergent hop across repeated successful routes.', ['traceroute.path_signatures']),
  findingPresentation('traceroute_unavailable', 'Traceroute capability was unavailable, so no route observation was established.', 'A functional traceroute capability is available before diagnostics begin.', 'No route or destination-health conclusion can be drawn without a traceroute observation.', 'Restore the supported traceroute capability, then repeat the bounded check.'),
  findingPresentation('traceroute_unreachable', 'No completed traceroute attempt observed the destination as reached.', 'At least one completed trace reaches the destination or establishes a stable last responsive hop.', 'Traceroute non-reachability does not prove that every application protocol is unreachable.', 'Repeat from the same observation point and compare the last responsive hop.', ['traceroute.attempts_reached'])
].map(entry => [entry.key, entry])));

const SAFE_ERROR_CODES = new Set([
  'cancelled', 'checker_capacity_unavailable', 'checker_panic', 'connection_failed', 'invalid_address', 'invalid_url',
  'network_policy_blocked', 'service_greeting_unverified', 'timeout', 'tls_certificate_expired', 'traceroute_unavailable',
  'tls_certificate_not_yet_valid', 'tls_downgrade', 'tls_handshake_failed', 'tls_hostname_mismatch', 'tls_untrusted',
  'unexpected_status'
]);
function evidenceSignal(label, category, observedShape, expectedShape) {
  return Object.freeze({ label, category, observedShape, expectedShape });
}
export const EVIDENCE_SIGNAL_PRESENTATION_REGISTRY = Object.freeze({
  error_code: evidenceSignal('Result error code', 'result', 'closed_error_code', 'none'),
  'http.status_code': evidenceSignal('HTTP status comparison', 'http', 'http_status', 'http_status'),
  'tls.certificate_expires_at': evidenceSignal('Certificate expiry timestamp', 'tls', 'utc_timestamp', 'none'),
  'traceroute.attempts_cancelled': evidenceSignal('Cancelled traceroute attempts', 'traceroute', 'count', 'count'),
  'traceroute.attempts_execution_failed': evidenceSignal('Failed traceroute command attempts', 'traceroute', 'count', 'count'),
  'traceroute.attempts_reached': evidenceSignal('Completed traceroute reachability', 'traceroute', 'completed_fraction', 'completed_fraction'),
  'traceroute.attempts_timed_out': evidenceSignal('Timed-out traceroute attempts', 'traceroute', 'count', 'count'),
  'traceroute.execution_failures': evidenceSignal('Traceroute execution failures', 'traceroute', 'execution_failure_counts', 'count'),
  'traceroute.path_signatures': evidenceSignal('Successful completed path signatures', 'traceroute', 'path_count', 'none'),
  'traceroute.path_status': evidenceSignal('Degraded completed path evidence', 'traceroute', 'path_count', 'none')
});
function coverageSignal(label, category, plural = false) {
  return Object.freeze({ label, category, plural });
}
export const COVERAGE_SIGNAL_PRESENTATION_REGISTRY = Object.freeze({
  certificate_expires_at: coverageSignal('Certificate expiry timestamp', 'tls'),
  details: coverageSignal('Checker details', 'result', true),
  dns_answers: coverageSignal('DNS answer facts', 'dns', true),
  endpoint: coverageSignal('Connected endpoint facts', 'connectivity', true),
  error_code: coverageSignal('Result error code', 'result'),
  geoip: coverageSignal('GeoIP result facts', 'geoip', true),
  geoip_enrichment: coverageSignal('GeoIP enrichment facts', 'geoip', true),
  http_status: coverageSignal('HTTP status facts', 'http', true),
  kind: coverageSignal('Result kind', 'result'),
  response_body: coverageSignal('Response body facts', 'http', true),
  result: coverageSignal('Result observation', 'result'),
  service_verification_details: coverageSignal('Service verification details', 'service', true),
  service_verification_scope: coverageSignal('Service verification scope', 'service'),
  status: coverageSignal('Result status', 'result'),
  tls: coverageSignal('TLS handshake facts', 'tls', true),
  tls_certificate: coverageSignal('TLS certificate facts', 'tls', true),
  tls_failure_details: coverageSignal('TLS failure details', 'tls', true),
  trace_attempts: coverageSignal('Traceroute attempt counters', 'traceroute', true),
  trace_error: coverageSignal('Traceroute error facts', 'traceroute', true),
  trace_paths: coverageSignal('Completed traceroute path facts', 'traceroute', true),
  trace_topology: coverageSignal('Traceroute topology facts', 'traceroute', true)
});
export const COVERAGE_CODE_PRESENTATION_REGISTRY = Object.freeze({
  missing_details: 'Required checker details were not observed.',
  malformed_details: 'Observed checker details did not satisfy the closed local contract.',
  unsupported_details: 'Observed details are outside the supported analysis scope.'
});
const DIRECTNESS_LABELS = Object.freeze({ direct: 'Direct result evidence.', corroborated: 'Corroborated evidence from multiple bounded facts.', limited: 'Limited evidence; independent verification is required.' });
const SERVICE_GREETING_KINDS = new Set(['imap', 'imaps', 'pop3', 'pop3s', 'smtp', 'smtps', 'ssh', 'submission']);
const PRESENTATION_FAILURE = Object.freeze({
  kind: 'invalid-response', code: 'unsupported_presentation_contract',
  message: 'This report cannot be presented safely because its presentation contract is unsupported.', retryable: false
});
function presentationFailure() { return { ...PRESENTATION_FAILURE }; }

function boundedEvidenceValue(value, shape, perspective) {
  const text = String(value ?? '');
  const prefix = perspective === 'expected' ? 'Expected' : 'Observed';
  if (shape === 'none') return '';
  if (shape === 'closed_error_code') return SAFE_ERROR_CODES.has(text) ? `${prefix} code: ${text}.` : '';
  if (shape === 'http_status') return /^(?:[1-5][0-9]{2})$/.test(text) ? `${prefix} HTTP status: ${text}.` : '';
  if (shape === 'count') return /^(?:0|[1-9]|10)$/.test(text) ? `${prefix} count: ${text}.` : '';
  if (shape === 'completed_fraction') {
    const match = /^(\d{1,2})\/(\d{1,2}) completed attempts reached$/.exec(text);
    return match && +match[1] <= +match[2] && +match[2] <= 10 ? `${prefix} completed reachability: ${match[1]} of ${match[2]} attempts.` : '';
  }
  if (shape === 'execution_failure_counts') {
    const match = /^(\d{1,2}) execution failures: (\d{1,2}) timed out, (\d{1,2}) cancelled, (\d{1,2}) command errors$/.exec(text);
    const values = match?.slice(1).map(Number);
    return values && values.every(number => number <= 10) && values[0] === values[1] + values[2] + values[3]
      ? `${prefix} execution failures: total ${values[0]}, timed out ${values[1]}, cancelled ${values[2]}, command errors ${values[3]}.` : '';
  }
  if (shape === 'path_count') {
    const degraded = /^degraded segment observed among (\d{1,2}) completed attempts?$/.exec(text);
    const signatures = /^multiple successful completed path signatures among (\d{1,2}) reached completed attempts$/.exec(text);
    const count = Number((degraded || signatures)?.[1]);
    return Number.isInteger(count) && count >= 0 && count <= 10 ? `${prefix} completed path evidence count: ${count}.` : '';
  }
  if (shape === 'utc_timestamp') {
    const parsed = new Date(text);
    return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(text) && !Number.isNaN(parsed.valueOf()) && parsed.toISOString() === text.replace('Z', '.000Z')
      ? `${prefix} UTC timestamp: ${text}.` : '';
  }
  return '';
}

function safeEvidenceFacts(finding, evidenceByID, presentation) {
  const facts = [];
  for (const id of finding.evidence_ids) {
    const item = evidenceByID.get(id); const signal = item && EVIDENCE_SIGNAL_PRESENTATION_REGISTRY[item.signal];
    if (!signal || !presentation.signals.includes(item.signal)) throw presentationFailure();
    const observed = boundedEvidenceValue(item.observed, signal.observedShape, 'observed');
    const expected = boundedEvidenceValue(item.expected, signal.expectedShape, 'expected');
    if (observed) facts.push(`${signal.label}. ${observed}${expected ? ` ${expected}` : ''}`);
    if (Number.isInteger(item.attempt) && item.attempt >= 1 && item.attempt <= 10) facts.push(`Bounded attempt: ${item.attempt}.`);
  }
  return facts.slice(0, 64);
}

function coveragePath(value) {
  const match = /^results\[(\d|1\d)\](?:\.(details)(?:\.([a-z_]+))?|\.([a-z_]+))?$/.exec(String(value));
  if (!match) throw presentationFailure();
  let signal = match[3] || match[4] || (match[2] ? 'details' : 'result');
  if (signal === 'verification_scope') signal = 'service_verification_scope';
  const presentation = COVERAGE_SIGNAL_PRESENTATION_REGISTRY[signal];
  if (!presentation) throw presentationFailure();
  return { index: Number(match[1]), signal, presentation };
}
function coveragePathFact(value, availability) {
  const { index, presentation } = coveragePath(value);
  return `${presentation.label} ${presentation.plural ? 'were' : 'was'} ${availability ? 'available' : 'not available'} for result ${index + 1}.`;
}
function hasGreetingScope(result) {
  return Boolean(result && SERVICE_GREETING_KINDS.has(result.kind) && result.status === 'healthy' && result.details?.verification_scope === 'server_greeting');
}
function coverageIssueFact(issue, results) {
  const presentation = COVERAGE_SIGNAL_PRESENTATION_REGISTRY[issue.signal];
  if (!presentation) throw presentationFailure();
  if (issue.signal === 'service_verification_scope' && issue.code === 'unsupported_details' && hasGreetingScope(results[issue.result_index]) && results[issue.result_index].kind === issue.kind) return SERVER_GREETING_SCOPE;
  const fact = COVERAGE_CODE_PRESENTATION_REGISTRY[issue.code];
  if (!fact) throw presentationFailure();
  return `${presentation.label} (${presentation.category}). ${fact}`;
}
function coverageFacts(coverage, results = []) {
  const facts = [];
  for (const value of coverage.available) facts.push(coveragePathFact(value, true));
  for (const value of coverage.missing) facts.push(coveragePathFact(value, false));
  for (const issue of [...coverage.provider_failures, ...coverage.limitations]) facts.push(coverageIssueFact(issue, results));
  for (const enrichment of coverage.enrichment || []) {
    if (!COVERAGE_SIGNAL_PRESENTATION_REGISTRY.geoip_enrichment) throw presentationFailure();
    facts.push(`GeoIP enrichment source category: ${enrichment.source}.`, `GeoIP cache hits: ${enrichment.cache_hits}.`, `GeoIP upstream fetches: ${enrichment.upstream_fetches}.`, `GeoIP maximum age milliseconds: ${enrichment.max_age_ms}.`);
    for (const failure of enrichment.failures) facts.push(`GeoIP ${failure.kind} failures: ${failure.count}; retry category: ${failure.retryable ? 'retryable' : 'not retryable'}.`);
  }
  return facts.slice(0, 512);
}
function validatePresentationContract(report) {
  const analysis = report.analysis;
  if (!analysis) return;
  for (const finding of analysis.findings) {
    const presentation = FINDING_PRESENTATION_REGISTRY[`finding.${finding.code}`];
    if (!presentation) throw presentationFailure();
    for (const id of finding.evidence_ids) {
      const item = analysis.evidence.find(evidence => evidence.id === id);
      if (!item || !EVIDENCE_SIGNAL_PRESENTATION_REGISTRY[item.signal] || !presentation.signals.includes(item.signal)) throw presentationFailure();
    }
  }
  for (const item of analysis.evidence) if (!EVIDENCE_SIGNAL_PRESENTATION_REGISTRY[item.signal]) throw presentationFailure();
  for (const value of [...analysis.coverage.available, ...analysis.coverage.missing]) coveragePath(value);
  for (const issue of [...analysis.coverage.provider_failures, ...analysis.coverage.limitations]) {
    if (!COVERAGE_SIGNAL_PRESENTATION_REGISTRY[issue.signal] || !COVERAGE_CODE_PRESENTATION_REGISTRY[issue.code]) throw presentationFailure();
  }
}

function semanticBlock(doc, key, text, tag = 'div') {
  const block = makeNode(doc, tag); block.dataset.semanticKey = key;
  block.append(makeNode(doc, 'h5', key), makeNode(doc, 'p', text));
  return block;
}

function makeNode(doc, tag, text, className) {
  const element = doc.createElement(tag);
  if (text !== undefined) element.textContent = String(text);
  if (className) element.className = className;
  return element;
}
function analysisSection(doc, name, title) {
  const element = makeNode(doc, 'section');
  element.dataset.analysisSection = name;
  if (title) element.append(makeNode(doc, 'h3', title));
  return element;
}
const OVERALL_STATUS_TEXT = Object.freeze({
  healthy: 'All configured checks completed within their observed scope; no broader service-health claim is made.',
  degraded: 'At least one bounded observation was degraded; no broader health conclusion was made.',
  unreachable: 'At least one bounded observation was unreachable; no broader health conclusion was made.'
});
const OVERALL_VERDICT_TEXT = Object.freeze({
  healthy: 'No finding requiring attention was derived from the observed checks; this does not establish broader service health.',
  attention: 'One or more bounded observations require attention.',
  inconclusive: 'The bounded observations were insufficient for a conclusion.'
});
function findingPanel(doc, index, detail) {
  const { presentation, facts, directness } = detail;
  const panel = makeNode(doc, 'div', undefined, 'finding-panel'); panel.id = `finding-panel-${index}`;
  const evidenceRegion = analysisSection(doc, 'evidence', 'supporting_evidence');
  const evidenceTitle = evidenceRegion.querySelector('h3'); evidenceTitle.id = `finding-evidence-title-${index}`;
  const evidenceBlock = semanticBlock(doc, 'supporting_evidence', facts.length ? facts.join(' ') : 'No allowlisted bounded evidence value was available for display.');
  const scroll = makeNode(doc, 'div', undefined, 'evidence-scroll'); scroll.tabIndex = 0; scroll.setAttribute('aria-labelledby', evidenceTitle.id);
  const table = makeNode(doc, 'table'); table.append(makeNode(doc, 'caption', `${presentation.key} supporting_evidence`));
  const head = makeNode(doc, 'thead'); const headRow = makeNode(doc, 'tr'); const th = makeNode(doc, 'th', 'Safe bounded fact'); th.scope = 'col'; headRow.append(th); head.append(headRow); table.append(head);
  const body = makeNode(doc, 'tbody');
  for (const fact of facts) { const row = makeNode(doc, 'tr'); row.append(makeNode(doc, 'td', fact)); body.append(row); }
  table.append(body); scroll.append(table); evidenceBlock.append(scroll); evidenceRegion.append(evidenceBlock); panel.append(evidenceRegion);
  panel.append(semanticBlock(doc, 'expectation', presentation.expectation));
  panel.append(semanticBlock(doc, 'evidence_directness', directness));
  panel.append(semanticBlock(doc, 'coverage_limitation', presentation.limitation));
  const actionsRegion = analysisSection(doc, 'actions', 'next_action');
  const actionBlock = semanticBlock(doc, 'next_action', presentation.action);
  const list = makeNode(doc, 'ol'); const li = makeNode(doc, 'li'); const checkbox = makeNode(doc, 'input'); checkbox.type = 'checkbox'; checkbox.id = `action-${index}`;
  const label = makeNode(doc, 'label', presentation.action); label.htmlFor = checkbox.id; li.append(checkbox, label); list.append(li); actionBlock.append(list); actionsRegion.append(actionBlock); panel.append(actionsRegion);
  return panel;
}

function closeFindingPanel(state) {
  if (!state?.panel) return;
  state.openToggle?.setAttribute('aria-expanded', 'false');
  state.panel.remove();
  state.panel = null; state.openToggle = null;
}

function toggleFindingPanel(root, toggle) {
  const state = FINDING_ACCORDIONS.get(root);
  const index = Number(toggle.dataset.findingIndex);
  if (!state || !toggle.isConnected || !root.contains(toggle) || state.toggles[index] !== toggle) return;
  if (state.openToggle === toggle) { closeFindingPanel(state); return; }
  const panel = findingPanel(root.ownerDocument, index, state.details[index]);
  const oldElements = state.panel ? state.panel.querySelectorAll('*').length + 1 : 0;
  const candidateElements = panel.querySelectorAll('*').length + 1;
  if (root.ownerDocument.querySelectorAll('*').length - oldElements + candidateElements > MAX_DOCUMENT_ELEMENTS) throw presentationFailure();
  closeFindingPanel(state);
  toggle.closest('article').append(panel);
  toggle.setAttribute('aria-expanded', 'true');
  state.panel = panel; state.openToggle = toggle;
}

function renderAnalysisWorkspace(doc, report, root = doc.querySelector('#analysis-report')) {
  root.replaceChildren();
  root.hidden = false;
  const accordion = { details: [], toggles: [], panel: null, openToggle: null };
  const analysis = report.analysis;
  const verdict = analysisSection(doc, 'verdict');
  const verdictText = analysis ? OVERALL_VERDICT_TEXT[analysis.verdict] : OVERALL_VERDICT_TEXT.inconclusive;
  const header = makeNode(doc, 'div', undefined, `analysis-verdict verdict-${analysis?.verdict || 'inconclusive'}`);
  header.append(makeNode(doc, 'strong', verdictText), makeNode(doc, 'time', new Date(report.started_at).toISOString()), makeNode(doc, 'span', `${report.duration_ms} ms`));
  verdict.append(header);
  root.append(verdict);

  if (!analysis) {
    const unsupported = analysisSection(doc, 'findings', 'Automated analysis unavailable');
    unsupported.append(makeNode(doc, 'p', 'The bounded report did not include a supported analysis object.'));
    root.append(unsupported, analysisSection(doc, 'evidence', 'supporting_evidence'), analysisSection(doc, 'actions', 'next_action'), analysisSection(doc, 'coverage', 'coverage_limitation'));
  } else {
    const findings = analysisSection(doc, 'findings', 'Bounded findings');
    const evidenceByID = new Map(analysis.evidence.map(item => [item.id, item]));
    if (!analysis.findings.length) findings.append(makeNode(doc, 'p', 'No locally presentable finding requiring attention was derived from the observed checks.'));
    analysis.findings.forEach((finding, index) => {
      const presentation = FINDING_PRESENTATION_REGISTRY[`finding.${finding.code}`];
      if (!presentation) throw new TypeError('finding presentation registry is incomplete');
      const severity = ['critical', 'warning', 'info'].includes(finding.severity) ? finding.severity : 'info';
      const article = makeNode(doc, 'article', undefined, `finding finding-${severity}`);
      article.dataset.severity = severity;
      article.dataset.presentationKey = presentation.key;
      article.append(semanticBlock(doc, 'cause', presentation.cause));
      const toggle = makeNode(doc, 'button', 'Show bounded evidence and action', 'finding-toggle');
      toggle.type = 'button'; toggle.setAttribute('aria-expanded', 'false'); toggle.dataset.findingIndex = String(index);
      toggle.setAttribute('aria-controls', `finding-panel-${index}`);
      article.append(toggle); findings.append(article);
      const facts = safeEvidenceFacts(finding, evidenceByID, presentation);
      accordion.details.push(Object.freeze({ presentation, facts: Object.freeze([...facts]), directness: DIRECTNESS_LABELS[finding.confidence] }));
      accordion.toggles.push(toggle);
    });
    root.append(findings);
    const coverage = analysisSection(doc, 'coverage', 'coverage_limitation');
    const list = makeNode(doc, 'ul');
    for (const fact of coverageFacts(analysis.coverage, report.results)) list.append(makeNode(doc, 'li', fact));
    coverage.append(list); root.append(coverage);
  }
  const raw = analysisSection(doc, 'raw', '원시 결과');
  raw.append(makeNode(doc, 'p', 'Raw results can contain targets and diagnostic details. Review them before sharing.'));
  const details = makeNode(doc, 'details'); details.append(makeNode(doc, 'summary', '원시 측정 결과 보기'));
  const pre = makeNode(doc, 'pre'); pre.tabIndex = 0; pre.setAttribute('aria-label', '원시 측정 결과 JSON'); pre.textContent = JSON.stringify(report.results, null, 2); details.append(pre); raw.append(details); root.append(raw);
  return accordion;
}
function renderSafeResults(doc, report, targets = {}) {
  const summary = targets.summary || doc.querySelector('#summary'); summary.replaceChildren();
  const status = makeNode(doc, 'div', undefined, `status ${report.status}`); status.append(makeNode(doc, 'span'), doc.createTextNode(OVERALL_STATUS_TEXT[report.status])); summary.append(status);
  const coverage = report.analysis?.coverage;
  const coverageSummary = coverage ? `${coverageFacts(coverage, report.results).length} bounded coverage facts` : 'Automated analysis unavailable';
  const metrics = makeNode(doc, 'dl');
  for (const [label, value] of [['Checks', report.summary.total], ['Completed observations', report.summary.passed], ['Observations requiring attention', report.summary.failed], ['Duration', `${report.duration_ms} ms`], ['Observation point', 'API server execution environment'], ['Coverage', coverageSummary]]) {
    const item = makeNode(doc, 'div'); item.append(makeNode(doc, 'dt', label), makeNode(doc, 'dd', value)); metrics.append(item);
  }
  summary.append(metrics);
  const results = targets.results || doc.querySelector('#results'); results.replaceChildren();
  const resultStatusText = Object.freeze({
    healthy: 'This bounded check completed within its observed scope.',
    degraded: 'This bounded check produced an observation requiring attention.',
    unreachable: 'This bounded check did not establish endpoint reachability.'
  });
  report.results.forEach((item, index) => {
    const article = makeNode(doc, 'article', undefined, 'result');
    const content = makeNode(doc, 'div');
    const resultMeaning = hasGreetingScope(item) ? SERVER_GREETING_SCOPE : resultStatusText[item.status];
    content.append(makeNode(doc, 'h3', `Check ${index + 1}`), makeNode(doc, 'p', resultMeaning));
    const outcome = makeNode(doc, 'strong', item.status === 'healthy' ? 'Bounded observation completed' : item.status === 'degraded' ? 'Bounded observation degraded' : 'Bounded observation unreachable', item.status);
    article.append(makeNode(doc, 'span', item.kind.toUpperCase(), 'kind'), content, outcome, makeNode(doc, 'time', `${item.latency_ms} ms`));
    results.append(article);
  });
  return renderAnalysisWorkspace(doc, report, targets.analysis || doc.querySelector('#analysis-report'));
}
function humanReport(report) {
  const analysis = report.analysis;
  const verdict = analysis ? OVERALL_VERDICT_TEXT[analysis.verdict] : OVERALL_VERDICT_TEXT.inconclusive;
  const lines = ['# CheckNetwork bounded report', '', `Report status: ${OVERALL_STATUS_TEXT[report.status]}`, `Analysis verdict: ${verdict}`, `Started (UTC): ${report.started_at}`, `Duration (ms): ${report.duration_ms}`, `Checks: ${report.summary.total}`, ''];
  if (!analysis) return [...lines, 'Automated analysis: unavailable for this bounded report.'].join('\n');
  const evidenceByID = new Map(analysis.evidence.map(item => [item.id, item]));
  lines.push('## observed_check_scopes');
  for (const result of report.results) if (hasGreetingScope(result)) lines.push(`- ${SERVER_GREETING_SCOPE}`);
  lines.push('', '## findings');
  for (const finding of analysis.findings) {
    const presentation = FINDING_PRESENTATION_REGISTRY[`finding.${finding.code}`];
    if (!presentation) throw presentationFailure();
    const facts = safeEvidenceFacts(finding, evidenceByID, presentation);
    const values = {
      cause: presentation.cause,
      supporting_evidence: facts.length ? facts.join(' ') : 'No allowlisted bounded evidence value was available for export.',
      expectation: presentation.expectation,
      evidence_directness: DIRECTNESS_LABELS[finding.confidence],
      coverage_limitation: presentation.limitation,
      next_action: presentation.action
    };
    for (const key of PRESENTATION_SEMANTIC_KEYS) lines.push(`### ${key} [${presentation.key}]`, values[key], '');
  }
  if (!analysis.findings.length) lines.push('No locally presentable finding requiring attention was derived from the observed checks.', '');
  lines.push('## coverage_facts');
  const facts = coverageFacts(analysis.coverage, report.results);
  if (!facts.length) lines.push('- No allowlisted bounded coverage fact was available.');
  else for (const fact of facts) lines.push(`- ${fact}`);
  return lines.join('\n');
}
export function presentReport(document, report) {
  validatePresentationContract(report);
  const actual = {
    summary: document.querySelector('#summary'), results: document.querySelector('#results'),
    analysis: document.querySelector('#analysis-report')
  };
  const staged = {
    summary: document.createElement('div'), results: document.createElement('div'), analysis: document.createElement('div')
  };
  const accordion = renderSafeResults(document, report, staged);
  const markdown = humanReport(report);
  const replacedElements = Object.values(actual).reduce((count, root) => count + root.querySelectorAll('*').length, 0);
  const candidateElements = Object.values(staged).reduce((count, root) => count + root.querySelectorAll('*').length, 0);
  if (document.querySelectorAll('*').length - replacedElements + candidateElements > MAX_DOCUMENT_ELEMENTS) throw presentationFailure();
  for (const key of ['summary', 'results', 'analysis']) actual[key].replaceChildren(...staged[key].childNodes);
  actual.analysis.hidden = false;
  FINDING_ACCORDIONS.set(actual.analysis, accordion);
  document.querySelector('#report-section').hidden = false;
  document.querySelector('#analysis-title').focus();
  return markdown;
}

const APP_VIEW_NAMES = new Set(['diagnostics', 'topology', 'geo-map', 'ip-labels']);

function oversizedResponseError() {
  return { kind: 'invalid-response', code: 'response_too_large', message: '서버 응답이 허용된 크기를 초과했습니다.', retryable: false };
}

async function readResponseText(response, maxBytes = SCHEMA_LIMITS.responseBytes) {
  const declared = Number(response?.headers?.get?.('Content-Length'));
  if (Number.isFinite(declared) && declared > maxBytes) throw oversizedResponseError();
  if (response?.body?.getReader) {
    const reader = response.body.getReader(); const decoder = new TextDecoder(); let total = 0; let text = '';
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > maxBytes) { try { await reader.cancel(); } catch { /* size error remains authoritative */ } throw oversizedResponseError(); }
      text += decoder.decode(value, { stream: true });
    }
    return text + decoder.decode();
  }
  const text = await response.text();
  if (new TextEncoder().encode(text).byteLength > maxBytes) throw oversizedResponseError();
  return text;
}
function viewFromHash(hash) {
  const requested = String(hash || '').replace(/^#/, '');
  return APP_VIEW_NAMES.has(requested) ? requested : 'diagnostics';
}
function activateView(doc, requestedView) {
  const view = APP_VIEW_NAMES.has(requestedView) ? requestedView : 'diagnostics';
  doc.querySelectorAll('[data-view]').forEach(element => { element.hidden = element.dataset.view !== view; });
  doc.querySelectorAll('[data-view-link]').forEach(link => {
    const selected = link.dataset.viewLink === view; link.classList.toggle('active', selected);
    if (selected) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current');
  });
  return view;
}

export function createApp({ document: doc, window: win, fetchImpl = win.fetch?.bind(win), clock = () => Date.now(), setTimer = win.setTimeout.bind(win), clearTimer = win.clearTimeout.bind(win), scheduler, scheduleLabelRender = callback => win.queueMicrotask(callback), cancelLabelRender = () => {} } = {}) {
  let activeInstance = true;
  const labelInstance = {};
  const lifecycle = new win.AbortController();
  const listeners = new Set();
  const listen = (target, type, handler, options) => {
    target.addEventListener(type, handler, options);
    const remove = () => {
      if (!listeners.delete(remove)) return;
      target.removeEventListener(type, handler, options);
    };
    listeners.add(remove);
    return remove;
  };
  const targetsEl = doc.querySelector('#targets');
  const addTargetButton = doc.querySelector('#add-target');
  const targetRowDisposers = new WeakMap();
  let currentReport;
  let currentTopologyReport;
  let selectedTopologyTargets = new Set();
  let showUnresponsiveTopologyNodes = true;
  let ipLabelPage = 0;
  let ipLabelRenderGeneration = 0;
  let ipLabelImportGeneration = 0;
  let ipLabelRenderHandle;
  let ipLabelRenderLimited = false;
  let ipLabels = new Map();
  let ipLabelLoadStatus = { invalid: 0, omitted: 0 };

  const scheduleIPLabelRender = scheduleLabelRender;
  function announceTargets(message) {
    doc.querySelector('#request-live').textContent = message;
  }

  function refreshTargetRows() {
    [...targetsEl.children].forEach((row, offset) => {
      const number = offset + 1;
      row.querySelector('select').setAttribute('aria-label', `${number}번째 검사 종류`);
      row.querySelector('input:not(.expected)').setAttribute('aria-label', `${number}번째 검사 주소`);
      row.querySelector('.expected').setAttribute('aria-label', `${number}번째 기대 HTTP 상태`);
      row.querySelector('.icon-button').setAttribute('aria-label', `${number}번째 검사 삭제`);
    });
    addTargetButton.disabled = targetsEl.children.length >= MAX_TARGETS;
  }

  function detachTargetRow(row) {
    targetRowDisposers.get(row)?.();
    targetRowDisposers.delete(row);
  }

  function bindTargetRow(row) {
    if (targetRowDisposers.has(row)) return;
    const select = row.querySelector('select');
    const input = row.querySelector('input:not(.expected)');
    const expected = row.querySelector('.expected');
    const removeButton = row.querySelector('.icon-button');
    const sync = () => {
      input.placeholder = placeholders[select.value];
      expected.hidden = !['http', 'https'].includes(select.value);
    };
    const disposers = [];
    try {
      disposers.push(listen(select, 'change', sync));
      disposers.push(listen(removeButton, 'click', () => {
        if (targetsEl.children.length <= 1 || !row.isConnected) return;
        const index = [...targetsEl.children].indexOf(row);
        detachTargetRow(row);
        row.remove();
        refreshTargetRows();
        announceTargets(`검사 ${targetsEl.children.length}개.`);
        const survivors = [...targetsEl.children];
        survivors[Math.min(index, survivors.length - 1)]?.querySelector('.icon-button')?.focus();
      }));
    } catch (error) {
      for (const dispose of disposers.reverse()) dispose();
      throw error;
    }
    targetRowDisposers.set(row, () => { for (const dispose of disposers.reverse()) dispose(); });
    sync();
  }

  function addTarget(kind = 'dns', address = '', { announce = true } = {}) {
    if (targetsEl.children.length >= MAX_TARGETS) {
      refreshTargetRows();
      if (announce) announceTargets(`검사는 최대 ${MAX_TARGETS}개까지 추가할 수 있습니다.`);
      return false;
    }
    const row = doc.querySelector('#target-template').content.firstElementChild.cloneNode(true);
    const select = row.querySelector('select');
    const input = row.querySelector('input:not(.expected)');
    select.value = kind;
    input.value = address;
    const candidateElements = row.querySelectorAll('*').length + 1;
    if (doc.querySelectorAll('*').length + candidateElements > MAX_DOCUMENT_ELEMENTS) {
      if (announce) announceTargets('문서 요소 한도로 검사를 추가할 수 없습니다.');
      return false;
    }
    bindTargetRow(row);
    try { targetsEl.append(row); }
    catch (error) { detachTargetRow(row); throw error; }
    refreshTargetRows();
    if (announce) announceTargets(`검사 ${targetsEl.children.length}개. 최대 ${MAX_TARGETS}개입니다.`);
    if (addTargetButton.disabled && doc.activeElement === addTargetButton) row.querySelector('.icon-button').focus();
    return true;
  }

  function collectTargets() {
    return [...targetsEl.children].map(row => {
      const kind = row.querySelector('select').value;
      const target = { kind, address: row.querySelector('input').value.trim() };
      if (['http', 'https'].includes(kind)) target.expected_status = Number(row.querySelector('.expected').value);
      return target;
    });
  }

  function loadIPLabels() {
    try {
      const rows = JSON.parse(win.localStorage.getItem(IP_LABEL_STORAGE_KEY) || '[]');
      const normalized = normalizeIPLabelRows(rows);
      win.localStorage.setItem(IP_LABEL_STORAGE_KEY, JSON.stringify([...normalized.labels.values()]));
      return normalized;
    } catch (_) {
      try { win.localStorage.setItem(IP_LABEL_STORAGE_KEY, '[]'); } catch { /* Storage can be disabled. */ }
      return { labels: new Map(), invalid: 1, omitted: 0 };
    }
  }

  function saveIPLabels() {
    try { win.localStorage.setItem(IP_LABEL_STORAGE_KEY, JSON.stringify([...ipLabels.values()])); } catch (_) { /* Keep the in-memory table usable when browser storage is unavailable. */ }
  }


  function upsertIPLabels(rows, { manual = false } = {}) {
    if (manual) {
      const row = rows[0] || {}; const ip = canonicalIPAddress(row.ip);
      const rawLabel = String(row.label ?? '').trim(); const rawNote = String(row.note ?? '').trim();
      if ([...rawLabel].length > LABEL || [...rawNote].length > NOTE) return { imported: 0, invalid: [], omitted: 0, reason: 'characters' };
      if (ip && !ipLabels.has(ip) && ipLabels.size >= LABEL_RECORDS) return { imported: 0, invalid: [], omitted: 1, reason: 'capacity' };
    }
    const incoming = normalizeIPLabelRows(rows);
    const combined = normalizeIPLabelRows([...ipLabels.values(), ...incoming.labels.values()]);
    let imported = 0;
    incoming.labels.forEach((mapping, key) => { if (combined.labels.get(key)?.label === mapping.label && combined.labels.get(key)?.note === mapping.note) imported++; });
    const capacityOmitted = incoming.labels.size - imported;
    ipLabels = combined.labels;
    saveIPLabels();
    renderIPLabelTable();
    refreshRenderedLabels();
    return { imported, invalid: Array.from({ length: incoming.invalid }, () => 'invalid'), omitted: incoming.omitted + capacityOmitted };
  }

  function makeIPLabelRow(ownerDocument, row) {
    const tr = ownerDocument.createElement('tr'); tr.dataset.ip = row.ip;
    const addressCell = ownerDocument.createElement('td'); const code = ownerDocument.createElement('code'); code.textContent = row.ip; addressCell.append(code);
    const labelCell = ownerDocument.createElement('td'); const label = ownerDocument.createElement('input'); label.dataset.field = 'label'; label.value = row.label; label.maxLength = LABEL; label.setAttribute('aria-label', `${row.ip} 표시 라벨`); labelCell.append(label);
    const noteCell = ownerDocument.createElement('td'); const note = ownerDocument.createElement('input'); note.dataset.field = 'note'; note.value = row.note; note.maxLength = NOTE; note.setAttribute('aria-label', `${row.ip} 설명`); noteCell.append(note);
    const actionCell = ownerDocument.createElement('td'); const remove = ownerDocument.createElement('button'); remove.type = 'button'; remove.className = 'mapping-delete'; remove.textContent = '삭제'; actionCell.append(remove);
    tr.append(addressCell, labelCell, noteCell, actionCell);
    return tr;
  }

  function cancelIPLabelRender() {
    ipLabelRenderGeneration++;
    if (ipLabelRenderHandle !== undefined) {
      try { cancelLabelRender(ipLabelRenderHandle); } catch { /* generation ownership still cancels publication */ }
    }
    ipLabelRenderHandle = undefined;
  }

  function unmountIPLabelTable() {
    const root = doc.querySelector('#ip-label-rows');
    cancelIPLabelRender();
    const owner = LABEL_ROOT_OWNERS.get(root);
    if (owner?.instance !== labelInstance) return;
    root.replaceChildren();
    doc.querySelector('#ip-labels-view').setAttribute('aria-busy', 'false');
    LABEL_ROOT_OWNERS.delete(root);
  }

  function renderIPLabelTable({ focusIndex } = {}) {
    if (!activeInstance) return;
    const root = doc.querySelector('#ip-label-rows'); if (!root) return;
    const rows = [...ipLabels.values()]; const pages = Math.max(1, Math.ceil(rows.length / LABEL_PAGE));
    ipLabelPage = Math.max(0, Math.min(ipLabelPage, pages - 1));
    const visible = rows.slice(ipLabelPage * LABEL_PAGE, (ipLabelPage + 1) * LABEL_PAGE);
    const previous = doc.querySelector('#ip-label-prev'); const next = doc.querySelector('#ip-label-next'); const status = doc.querySelector('#ip-label-page-status');
    previous.disabled = ipLabelPage === 0; next.disabled = ipLabelPage >= pages - 1; status.textContent = `${ipLabelPage + 1} / ${pages}`;
    cancelIPLabelRender();
    const generation = ipLabelRenderGeneration;
    const token = { instance: labelInstance, generation };
    LABEL_ROOT_OWNERS.set(root, token);
    const ownsRender = () => activeInstance && !lifecycle.signal.aborted && generation === ipLabelRenderGeneration && LABEL_ROOT_OWNERS.get(root) === token;
    const view = doc.querySelector('#ip-labels-view');
    if (ipLabelRenderLimited) doc.querySelector('#ip-label-message').textContent = '';
    ipLabelRenderLimited = false;
    view.dataset.state = 'rendering'; view.setAttribute('aria-busy', 'true');
    root.replaceChildren(); let offset = 0;
    const finish = () => {
      if (!ownsRender()) return;
      view.dataset.state = 'ready'; view.setAttribute('aria-busy', 'false');
      if (Number.isInteger(focusIndex)) root.querySelectorAll('.mapping-delete')[Math.min(focusIndex, visible.length - 1)]?.focus();
    };
    const limit = () => {
      if (!ownsRender()) return;
      root.replaceChildren();
      previous.disabled = true; next.disabled = true; status.textContent = '표시 제한';
      ipLabelRenderLimited = true;
      view.dataset.state = 'render-limited'; view.setAttribute('aria-busy', 'false');
      doc.querySelector('#ip-label-message').textContent = '문서 요소 한도로 IP 라벨을 표시할 수 없습니다. 저장된 매핑은 유지됩니다.';
    };
    if (!visible.length) {
      const row = doc.createElement('tr'); const cell = doc.createElement('td'); cell.colSpan = 4; cell.className = 'empty-mapping'; cell.textContent = '등록된 IP 라벨이 없습니다.'; row.append(cell);
      if (doc.querySelectorAll('*').length + 2 > MAX_DOCUMENT_ELEMENTS) { limit(); return; }
      root.append(row); finish(); return;
    }
    const appendChunk = () => {
      ipLabelRenderHandle = undefined;
      if (!ownsRender()) return;
      const detachedDocument = doc.implementation.createHTMLDocument('');
      const staged = detachedDocument.createDocumentFragment();
      visible.slice(offset, offset + 10).forEach(row => staged.append(makeIPLabelRow(detachedDocument, row)));
      const insertionElements = staged.querySelectorAll('*').length;
      const fragment = doc.createDocumentFragment();
      for (const node of [...staged.childNodes]) fragment.append(doc.importNode(node, true));
      if (!ownsRender()) return;
      if (fragment.querySelectorAll('*').length !== insertionElements || doc.querySelectorAll('*').length + insertionElements > MAX_DOCUMENT_ELEMENTS) { limit(); return; }
      root.append(fragment); offset += 10;
      if (offset < visible.length) { ipLabelRenderHandle = scheduleIPLabelRender(appendChunk); return; }
      finish();
    };
    ipLabelRenderHandle = scheduleIPLabelRender(appendChunk);
  }

  function refreshRenderedLabels() {
    if (activeView === 'topology' && currentTopologyReport && currentTopologyModel && renderSource && lanes.topology.phase === 'ready') {
      startActiveTopologyRender();
    }
  }

  function populateTopologyLabelEditor(address) {
    const ip = canonicalIPAddress(address);
    if (!ip) return false;
    resetTopologyLabelEditorToIP();
    const mapping = ipLabels.get(ip);
    doc.querySelector('#topology-label-address').value = ip;
    doc.querySelector('#topology-label-name').value = mapping?.label || '';
    doc.querySelector('#topology-label-note').value = mapping?.note || '';
    doc.querySelector('#topology-label-delete').hidden = !mapping;
    doc.querySelector('#topology-label-name').focus();
    const message = doc.querySelector('#topology-label-message');
    message.textContent = mapping ? `${ip}의 기존 라벨을 불러왔습니다.` : `${ip}에 적용할 라벨을 입력해 주세요.`;
    message.className = 'mapping-message';
    return true;
  }

  function resetTopologyLabelEditorToIP() {
    doc.querySelector('#topology-label-subject-label').textContent = 'IP 주소';
    doc.querySelector('#topology-label-address').readOnly = false;
  }

  ipLabelRenderGeneration++;
  const loadedLabels = loadIPLabels(); ipLabels = loadedLabels.labels; ipLabelLoadStatus = loadedLabels;
  if (!targetsEl.children.length) {
    addTarget('dns', 'example.com', { announce: false }); addTarget('tcp', '1.1.1.1:443', { announce: false });
    addTarget('https', 'https://example.com', { announce: false }); addTarget('traceroute', 'example.com', { announce: false });
  } else {
    [...targetsEl.children].forEach(bindTargetRow);
    refreshTargetRows();
  }
  const lanes = { diagnostics: createRequestLane('diagnostics'), topology: createRequestLane('topology') };
  const active = new Map(); const laneBases = new Map(); const revisions = new Map(); const credentialIndeterminate = new Set(); let ownerSequence = 0;
  let activeView = viewFromHash(win.location.hash);
  let currentTopologyModel;
  let renderSource;
  let renderPhase = 'idle';
  let finalTopologyPlan;
  const topologyViewState = { mode: '2d', transform: resetViewTransform() };
  for (const input of doc.querySelectorAll('input[name="topology-view-mode"]')) input.checked = input.value === topologyViewState.mode;
  doc.querySelector('#topology-view-status').textContent = '2D 그래프';
  let topologyResizeObserver = null;
  let topologyResizeObserved = false;
  let topologyResizeHandle = null;
  let pointerInteraction = null;
  const selectedScheduler = scheduler || (typeof win.requestAnimationFrame === 'function' && typeof win.cancelAnimationFrame === 'function'
    ? { schedule: callback => win.requestAnimationFrame(callback), cancel: handle => win.cancelAnimationFrame(handle) }
    : { schedule: callback => setTimer(callback, 0), cancel: handle => safeClearTimer(handle) });
  let renderCoordinator;
  renderCoordinator = new TopologyRenderCoordinator({
    document: doc,
    schedule: selectedScheduler.schedule.bind(selectedScheduler),
    cancelScheduled: selectedScheduler.cancel.bind(selectedScheduler),
    ownsRequest: (renderOwnerId, signature) => {
      const source = renderSource;
      if (!source || source.renderOwnerId !== renderOwnerId || source.signature !== signature) return false;
      const requestStillActive = ownsRequest(lanes.topology, source.requestOwnerId, signature);
      const readySourceStillCurrent = lanes.topology.phase === 'ready' && lanes.topology.result?.report === source.report && currentTopologyReport === source.report;
      return requestStillActive || readySourceStillCurrent;
    },
    onState: state => {
      if (!renderSource || state.ownerId !== renderSource.renderOwnerId || state.inputSignature !== renderSource.signature) return;
      const observedGeneration = renderSource.renderGeneration ?? 0;
      if (state.generation < observedGeneration) return;
      if (state.phase === 'rendering' || state.generation > observedGeneration) {
        renderSource.renderGeneration = state.generation;
        finalTopologyPlan = undefined;
      } else if (state.generation !== renderSource.renderGeneration) return;
      if (state.phase === 'ready' && state.plan) {
        finalTopologyPlan = { ownerId: state.ownerId, inputSignature: state.inputSignature, generation: state.generation, view: state.view, plan: state.plan };
        if (state.view === 'topology' && state.empty !== true && state.renderable !== false) scheduleTopologyResize();
      }
      renderPhase = state.phase === 'error' ? 'render-error'
        : state.renderable === false ? 'render-limited'
          : state.empty === true ? 'render-empty' : state.phase;
      if (activeView === 'topology') renderTopologySummary();
      renderState('topology');
    }
  });
  const authKey = base => AUTH_PREFIX + encodeURIComponent(base);
  const authRevision = base => revisions.get(base) || 0;
  const tokenFor = base => { try { return win.sessionStorage.getItem(authKey(base)) || ''; } catch { return ''; } };
  function safeClearTimer(handle) {
    try { clearTimer(handle); return null; } catch (error) { return error; }
  }
  const ui = purpose => Object.fromEntries(Object.entries(PURPOSE_UI[purpose]).map(([key, selector]) => [key, doc.querySelector(selector)]));
  function filteredTopologyModel(model = currentTopologyModel) {
    if (!model) return null;
    const filtered = filterTopologyModel(model, selectedTopologyTargets, { showUnresponsive: showUnresponsiveTopologyNodes });
    return {
      ...filtered,
      nodes: filtered.nodes.map(node => {
        const address = typeof node.address === 'string' ? canonicalIPAddress(node.address) : '';
        const mapping = address ? ipLabels.get(address) : undefined;
        return mapping ? { ...node, display_label: mapping.label, display_note: mapping.note } : node;
      })
    };
  }
  function renderTopologySummary() {
    const root = doc.querySelector('#topology-result-summary');
    root.replaceChildren();
    if (!currentTopologyModel) return;
    const server = currentTopologyModel.serverStats;
    const line = makeNode(doc, 'p', undefined, 'compact-render-stats');
    const serverText = server
      ? `서버 ${server.nodes.displayed}/${server.nodes.total} 노드 · ${server.links.displayed}/${server.links.total} 링크 · ${server.routes.displayed}/${server.routes.total} 경로`
      : `레거시 변환 ${currentTopologyModel.nodes.length} 노드 · ${currentTopologyModel.links.length} 링크 · ${currentTopologyModel.routes.length} 경로`;
    const limited = currentTopologyModel.serverTruncation?.truncated || currentTopologyModel.adapterTruncation?.truncated ? ' · 제한 적용' : '';
    const owned = finalTopologyPlan && renderSource && finalTopologyPlan.ownerId === renderSource.renderOwnerId &&
      finalTopologyPlan.inputSignature === renderSource.signature && finalTopologyPlan.generation === renderSource.renderGeneration &&
      finalTopologyPlan.view === 'topology';
    const viewText = owned
      ? ['nodes', 'links', 'routes'].map((key, index) => {
        const label = ['노드', '링크', '경로'][index]; const stats = finalTopologyPlan.plan.viewStats[key];
        return `${label} ${stats.displayed}/${stats.total} (생략 ${stats.omitted})`;
      }).join(' · ')
      : '계획 중';
    const p = owned ? finalTopologyPlan.plan.presentation : null;
    const screen = p ? ` · 화면 노드 ${p.nodes.length} · 관측 링크 ${p.links.length} · 점선 구간 ${p.connectors.length}` : '';
    line.textContent = `${serverText} · 검증 원본 ${viewText}${screen}${limited}`;
    root.append(line);
  }
  function renderTopologyTargetFilter(results, restore = null) {
    const filter = doc.querySelector('#topology-target-filter');
    const active = restore || (() => {
      const element = doc.activeElement;
      if (!filter.contains(element)) return null;
      if (element.dataset.filterAction) return { selector: `[data-filter-action="${element.dataset.filterAction}"]` };
      if (element.matches('[data-toggle-unresponsive]')) return { selector: '[data-toggle-unresponsive]' };
      if (element.dataset.targetIndex !== undefined) return { selector: `[data-target-index="${element.dataset.targetIndex}"]` };
      return null;
    })();
    filter.replaceChildren();
    const heading = makeNode(doc, 'div'); heading.append(makeNode(doc, 'strong', 'TRACE 경로 선택'), makeNode(doc, 'span', `${selectedTopologyTargets.size}/${results.length}개 표시`));
    const actions = makeNode(doc, 'div', undefined, 'target-filter-actions');
    for (const [action, text] of [['all', '전체 선택'], ['none', '전체 해제']]) { const button = makeNode(doc, 'button', text); button.type = 'button'; button.dataset.filterAction = action; actions.append(button); }
    const unknown = makeNode(doc, 'label', undefined, 'unknown-node-toggle'); const unknownInput = makeNode(doc, 'input'); unknownInput.type = 'checkbox'; unknownInput.dataset.toggleUnresponsive = ''; unknownInput.checked = showUnresponsiveTopologyNodes; unknown.append(unknownInput, makeNode(doc, 'span', '응답없음 노드')); actions.append(unknown);
    const toggles = makeNode(doc, 'div', undefined, 'target-toggles');
    results.forEach((result, index) => { const label = makeNode(doc, 'label'); const input = makeNode(doc, 'input'); input.type = 'checkbox'; input.dataset.targetIndex = String(index); input.checked = selectedTopologyTargets.has(index); label.append(input, makeNode(doc, 'i'), makeNode(doc, 'span', result.address)); toggles.append(label); });
    filter.append(heading, actions, toggles);
    if (active?.selector) filter.querySelector(active.selector)?.focus();
  }
  function drawGeo({ canvas, geo, signal }) {
    if (!canvas) return undefined;
    return mountGeoMap({ canvas, geo, signal, win, scheduler: selectedScheduler,
      controls: { message: doc.querySelector('#geo-map-message'), zoomIn: doc.querySelector('#geo-zoom-in'), zoomOut: doc.querySelector('#geo-zoom-out'), fit: doc.querySelector('#geo-fit') } });
  }
  function startActiveTopologyRender() {
    unmountDiagnosticsPresentation();
    unmountIPLabelTable();
    renderCoordinator.cancel('view-change');
    doc.querySelector(activeView === 'geo-map' ? '#topology-result' : '#geo-map-result').replaceChildren();
    doc.querySelector(activeView === 'geo-map' ? '#topology-render-status' : '#geo-render-status').textContent = '';
    if (!currentTopologyReport || !currentTopologyModel || !renderSource || !['topology', 'geo-map'].includes(activeView)) {
      renderPhase = 'idle';
      renderState('topology');
      return;
    }
    const geo = activeView === 'geo-map';
    if (geo) stopTopologyResizeObservation(); else ensureTopologyResizeObservation();
    const root = doc.querySelector(geo ? '#geo-map-result' : '#topology-result');
    const status = doc.querySelector(geo ? '#geo-render-status' : '#topology-render-status');
    renderPhase = 'rendering';
    finalTopologyPlan = undefined;
    if (!geo) { renderTopologySummary(); renderTopologyTargetFilter(currentTopologyReport.results || []); }
    renderCoordinator.start({
      ownerId: renderSource.renderOwnerId, inputSignature: renderSource.signature,
      view: geo ? 'geo' : 'topology', model: filteredTopologyModel(), root, status,
      mode: topologyViewState.mode, transform: topologyViewState.transform,
      workspace: {
        tooltip: doc.querySelector('#topology-node-tooltip'),
        disposeHiddenViews() { doc.querySelector(geo ? '#topology-result' : '#geo-map-result').replaceChildren(); },
        drawGeo
      }
    });
    renderState('topology');
  }

  function updateTopologyView({ mode = topologyViewState.mode, transform = topologyViewState.transform, viewport, message } = {}) {
    if (!activeInstance || activeView !== 'topology' || !renderSource || !Number.isInteger(renderSource.renderGeneration)) return false;
    const changed = renderCoordinator.updateTopologyView({
      ownerId: renderSource.renderOwnerId,
      inputSignature: renderSource.signature,
      generation: renderSource.renderGeneration,
      mode, transform, ...(viewport ? { viewport } : {})
    });
    if (!changed) return false;
    topologyViewState.mode = mode;
    topologyViewState.transform = createViewTransform(transform);
    const status = doc.querySelector('#topology-view-status');
    status.textContent = message || `${mode === '3d' ? '3D' : '2D'} 그래프`;
    return true;
  }

  function measuredTopologyViewport() {
    const canvas = doc.querySelector('#topology-result canvas.topology-canvas');
    if (!canvas?.isConnected) return null;
    const rect = canvas.getBoundingClientRect();
    const rootRect = doc.querySelector('#topology-result').getBoundingClientRect();
    return normalizeViewport({
      width: rect.width || rootRect.width || canvas.clientWidth || 800,
      height: rect.height || canvas.clientHeight || Math.max(240, Math.round((rootRect.width || 800) * 0.55)),
      dpr: Number(win.devicePixelRatio) || 1
    });
  }

  function scheduleTopologyResize() {
    if (!activeInstance || activeView !== 'topology' || topologyResizeHandle !== null) return;
    topologyResizeHandle = selectedScheduler.schedule(() => {
      topologyResizeHandle = null;
      const viewport = measuredTopologyViewport();
      if (viewport) updateTopologyView({ viewport });
    });
  }
  function ensureTopologyResizeObservation() {
    if (!topologyResizeObserver || topologyResizeObserved) return;
    topologyResizeObserver.observe(doc.querySelector('#topology-result'));
    topologyResizeObserved = true;
  }
  function stopTopologyResizeObservation() {
    pointerInteraction = null;
    if (topologyResizeHandle !== null) {
      try { selectedScheduler.cancel(topologyResizeHandle); } catch { /* cleanup must continue */ }
      topologyResizeHandle = null;
    }
    if (topologyResizeObserver && topologyResizeObserved) {
      try { topologyResizeObserver.disconnect(); } catch { /* cleanup must continue */ }
      topologyResizeObserved = false;
    }
  }
  function disposeTopologyRender(reason = 'dispose', clearSource = false) {
    stopTopologyResizeObservation();
    renderCoordinator.cancel(reason);
    renderPhase = 'idle';
    doc.querySelector('#topology-result').replaceChildren(); doc.querySelector('#geo-map-result').replaceChildren();
    doc.querySelector('#topology-render-status').textContent = ''; doc.querySelector('#geo-render-status').textContent = '';
    if (clearSource) renderSource = undefined;
    if (clearSource) finalTopologyPlan = undefined;
  }
  function unmountDiagnosticsPresentation() {
    const analysis = doc.querySelector('#analysis-report');
    closeFindingPanel(FINDING_ACCORDIONS.get(analysis));
    FINDING_ACCORDIONS.delete(analysis);
    doc.querySelector('#summary').replaceChildren();
    doc.querySelector('#results').replaceChildren();
    analysis.replaceChildren(); analysis.hidden = true;
    doc.querySelector('#report-section').hidden = true;
  }
  function mountOwnedDiagnosticsPresentation() {
    if (!currentReport || lanes.diagnostics.phase !== 'ready') return;
    try { presentReport(doc, currentReport); }
    catch {
      unmountDiagnosticsPresentation();
      const workspace = doc.querySelector('#diagnostics-workspace');
      workspace.dataset.state = 'render-limited'; workspace.setAttribute('aria-busy', 'false');
      doc.querySelector('#diagnostics-state-message').textContent = '문서 요소 한도로 진단 화면을 표시할 수 없습니다. 원본 JSON 다운로드는 사용할 수 있습니다.';
    }
  }
  function unmountInactiveTopologyPresentation() {
    disposeTopologyRender('view-change');
    doc.querySelector('#topology-result-summary').replaceChildren();
    doc.querySelector('#topology-target-filter').replaceChildren();
  }
  function readInput(purpose) {
    const common = { apiBaseURL: doc.querySelector('#api-base-url').value, authEnabled: doc.querySelector('#public-auth-enabled').checked };
    return purpose === 'diagnostics'
      ? canonicalDiagnosticsInput({ ...common, targets: collectTargets(), timeout_ms: doc.querySelector('#timeout').value })
      : canonicalTopologyInput({ ...common, addresses: doc.querySelector('#topology-targets').value.split(/\n|,/), attempts: doc.querySelector('#topology-attempts').value, timeout_ms: doc.querySelector('#topology-timeout').value });
  }
  function clearRequestAlert(purpose) {
    const alert = doc.querySelector('#request-alert');
    if (alert.dataset.purpose !== purpose) return;
    alert.hidden = true; alert.textContent = ''; delete alert.dataset.purpose;
  }
  function clearPurpose(purpose) {
    clearRequestAlert(purpose);
    const elements = ui(purpose); elements.report.hidden = true; elements.download.disabled = true; elements.error.hidden = true;
    if (purpose === 'diagnostics') {
      doc.querySelector('#analysis-report').replaceChildren(); doc.querySelector('#analysis-report').hidden = true; doc.querySelector('#summary').replaceChildren(); doc.querySelector('#results').replaceChildren(); currentReport = undefined;
    } else {
      disposeTopologyRender('clear', true);
      doc.querySelector('#topology-result-summary').replaceChildren(); doc.querySelector('#topology-target-filter').replaceChildren();
      currentTopologyReport = undefined; currentTopologyModel = undefined; selectedTopologyTargets = new Set();
    }
  }
  function renderState(purpose) {
    const lane = lanes[purpose]; const elements = ui(purpose); const loading = lane.phase === 'loading';
    const rendering = purpose === 'topology' && renderPhase === 'rendering' && ['topology', 'geo-map'].includes(activeView);
    const renderStateName = purpose === 'topology' && ['render-error', 'render-limited', 'render-empty'].includes(renderPhase) ? renderPhase : lane.phase;
    elements.workspace.dataset.state = renderStateName; elements.workspace.setAttribute('aria-busy', String(loading || rendering)); elements.cancel.hidden = !loading; elements.run.disabled = loading; elements.download.disabled = lane.phase !== 'ready';
    if (purpose === 'diagnostics') doc.querySelector('#download-human').disabled = lane.phase !== 'ready';
    elements.message.textContent = rendering
      ? '네트워크 응답 완료 · 화면을 표시하는 중입니다.'
      : renderStateName === 'render-error' ? '네트워크 응답은 완료했지만 화면 표시 중 오류가 발생했습니다. 원시 JSON 다운로드는 사용할 수 있습니다.'
        : renderStateName === 'render-limited' ? '문서 요소 한도로 토폴로지를 표시할 수 없습니다. 원시 JSON 다운로드는 사용할 수 있습니다.'
          : renderStateName === 'render-empty' ? '선택한 TRACE 경로가 0개입니다.'
            : { idle: '입력을 확인하고 실행해 주세요.', loading: purpose === 'diagnostics' ? '진단 중입니다.' : '경로 분석 중입니다.', ready: purpose === 'topology' ? '네트워크 응답과 화면 표시가 완료되었습니다.' : '분석이 완료되었습니다.', error: '요청을 완료하지 못했습니다.', cancelled: '요청이 취소되었습니다.' }[lane.phase];
  }
  function publishError(purpose, error) {
    const normalized = normalizeRequestError(error); const alert = doc.querySelector('#request-alert');
    ui(purpose).error.textContent = normalized.message; ui(purpose).error.hidden = false; alert.dataset.purpose = purpose; alert.textContent = `${purpose === 'diagnostics' ? '진단' : '경로 분석'} 실패: ${normalized.message}`; alert.hidden = false; doc.querySelector('#request-live').textContent = '';
    if (normalized.code === 'unauthorized') { doc.querySelector('#connection-settings').open = true; doc.querySelector('#bearer-token').focus(); } else alert.focus();
  }
  function invalidate(purpose) {
    if (!activeInstance) return false;
    const request = active.get(purpose);
    if (request) {
      request.reason = 'input-change'; safeClearTimer(request.timer);
      try { request.controller.abort('input-change'); }
      catch { /* Ownership invalidation remains authoritative. */ }
      finally { if (active.get(purpose) === request) active.delete(purpose); }
    }
    try { clearPurpose(purpose); } catch { /* Lane ownership must still be invalidated. */ }
    let signature = ''; try { const input = readInput(purpose); signature = inputSignature(purpose, input, authRevision(input.apiBaseURL)); } catch { /* invalid input remains idle */ }
    lanes[purpose] = transitionRequest(lanes[purpose], { type: 'INPUT_CHANGED', inputSignature: signature }); laneBases.delete(purpose); renderState(purpose);
  }
  function invalidateCredentialBase(apiBaseURL) {
    for (const purpose of ['diagnostics', 'topology']) {
      const request = active.get(purpose);
      let ownsBase = request ? request.apiBaseURL === apiBaseURL : laneBases.get(purpose) === apiBaseURL;
      if (!request && !laneBases.has(purpose)) {
        try { ownsBase = readInput(purpose).apiBaseURL === apiBaseURL; } catch { ownsBase = false; }
      }
      if (ownsBase) invalidate(purpose);
    }
  }
  function cancel(purpose, reason = 'user') {
    if (!activeInstance) return false;
    const request = active.get(purpose); if (!request) return;
    request.reason = reason; safeClearTimer(request.timer);
    try { request.controller.abort(reason); }
    finally {
      if (ownsRequest(lanes[purpose], request.ownerId, request.signature)) {
        try {
          lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_CANCELLED', ownerId: request.ownerId, inputSignature: request.signature, reason: reason === 'navigation' ? 'navigation' : 'user' });
        } finally {
          active.delete(purpose);
          try { lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_FINALIZED', ownerId: request.ownerId, inputSignature: request.signature }); }
          finally { clearPurpose(purpose); renderState(purpose); doc.querySelector('#request-live').textContent = '요청이 취소되었습니다.'; if (reason === 'user') ui(purpose).run.focus(); }
        }
      }
    }
  }
  async function start(purpose) {
    if (!activeInstance) return undefined;
    const previous = active.get(purpose); if (previous) { previous.reason = 'replaced'; safeClearTimer(previous.timer); previous.controller.abort('replaced'); }
    clearPurpose(purpose); let input;
    try { input = readInput(purpose); } catch (error) { lanes[purpose] = transitionRequest(lanes[purpose], { type: 'INPUT_CHANGED', inputSignature: '' }); renderState(purpose); publishError(purpose, { kind: 'http', code: 'invalid_input', message: error.message }); return; }
    const signature = inputSignature(purpose, input, authRevision(input.apiBaseURL)); const ownerId = `${purpose}:${++ownerSequence}`; const controller = new win.AbortController();
    const payload = purpose === 'diagnostics'
      ? { targets: input.targets.map(({ kind, address, expected_status }) => ({ kind, address, ...(expected_status ? { expected_status } : {}) })), timeout_ms: input.timeout_ms }
      : { targets: input.addresses.map(address => ({ kind: 'traceroute', address, attempts: input.attempts })), timeout_ms: input.timeout_ms, topology_mode: 'compact' };
    const startedAt = clock(); const total = clientTimeoutMS(payload);
    const request = { ownerId, signature, apiBaseURL: input.apiBaseURL, controller, reason: null, timer: null }; active.set(purpose, request); laneBases.set(purpose, input.apiBaseURL);
    lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_STARTED', ownerId, inputSignature: signature, startedAt }); renderState(purpose); doc.querySelector('#request-live').textContent = purpose === 'diagnostics' ? '진단을 시작했습니다.' : '경로 분석을 시작했습니다.';
    try {
      if (input.authEnabled && credentialIndeterminate.has(input.apiBaseURL)) {
        throw { kind: 'http', code: 'credential_indeterminate', message: CREDENTIAL_RECONCILIATION_MESSAGE, retryable: false };
      }
      const remaining = Math.max(0, total - Math.max(0, clock() - startedAt));
      if (remaining === 0) {
        request.reason = 'timeout'; controller.abort('timeout');
        throw { name: 'AbortError', reason: 'timeout' };
      }
      request.timer = setTimer(() => { if (active.get(purpose) === request) { request.reason = 'timeout'; controller.abort('timeout'); } }, remaining);
      const headers = { 'Content-Type': 'application/json' }; const token = input.authEnabled ? tokenFor(input.apiBaseURL) : '';
      if (token) { const url = new URL(input.apiBaseURL); if (url.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]', '::1'].includes(url.hostname)) throw { kind: 'http', code: 'insecure_auth', message: 'Bearer credential은 HTTPS API에만 전송할 수 있습니다.', retryable: false }; headers.Authorization = `Bearer ${token}`; }
      const response = await fetchImpl(`${input.apiBaseURL}/api/v1/reports`, { method: 'POST', headers, body: JSON.stringify(payload), signal: controller.signal });
      const body = await readResponseText(response); const parsed = parseResponse(response, body, clock()); if (!parsed.ok) throw parsed.error;
      let report;
      try { report = sanitizeCredentialReflection(parsed.report, token); }
      catch { throw { kind: 'invalid-response', code: 'credential_reflection', message: 'The server response reflected an authorization credential.', retryable: false }; }
      if (!activeInstance || !ownsRequest(lanes[purpose], ownerId, signature)) return;
      let topologyModel;
      if (purpose === 'diagnostics') validatePresentationContract(report);
      if (purpose === 'topology') topologyModel = topologyModelFromReport(report);
      if (purpose === 'diagnostics') {
        if (activeView === 'diagnostics' && doc.querySelector('#ip-label-rows tr[data-ip]')) unmountIPLabelTable();
        presentReport(doc, report);
        lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_SUCCEEDED', ownerId, inputSignature: signature, report, completedAt: clock() });
        currentReport = report;
      } else {
        lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_SUCCEEDED', ownerId, inputSignature: signature, report, completedAt: clock() });
        currentTopologyReport = report; currentTopologyModel = topologyModel;
        selectedTopologyTargets = new Set(report.results.map((_, index) => index)); showUnresponsiveTopologyNodes = true;
        renderSource = { requestOwnerId: ownerId, renderOwnerId: `topology-render:${report.id}:${signature}`, signature, report };
        ui(purpose).report.hidden = false;
        startActiveTopologyRender();
      }
      clearRequestAlert(purpose); doc.querySelector('#request-live').textContent = purpose === 'topology' ? '네트워크 응답을 받았습니다.' : '분석이 완료되었습니다.'; renderState(purpose);
    } catch (error) {
      if (!activeInstance || !ownsRequest(lanes[purpose], ownerId, signature)) return;
      if (request.reason === 'timeout') error = { name: 'AbortError', reason: 'timeout' };
      if (error?.name === 'AbortError' && request.reason !== 'timeout' && ['user', 'navigation', 'replaced', 'input-change'].includes(request.reason)) return;
      lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_FAILED', ownerId, inputSignature: signature, error }); clearPurpose(purpose); renderState(purpose); publishError(purpose, lanes[purpose].error);
    } finally {
      if (activeInstance && active.get(purpose)?.ownerId === ownerId) {
        try { safeClearTimer(request.timer); }
        finally {
          active.delete(purpose);
          try { lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_FINALIZED', ownerId, inputSignature: signature }); }
          finally { renderState(purpose); }
        }
      }
    }
  }
  function applyCredential(clear = false) {
    let apiBaseURL;
    try {
      apiBaseURL = canonicalDiagnosticsInput({ apiBaseURL: doc.querySelector('#api-base-url').value, targets: [{ kind: 'dns', address: 'credential.invalid' }], timeout_ms: 5000 }).apiBaseURL;
    } catch (error) { publishError('diagnostics', { kind: 'http', code: 'invalid_input', message: error.message }); return; }
    const tokenInput = doc.querySelector('#bearer-token');
    const token = tokenInput.value.trim();
    if (!clear && !token) {
      doc.querySelector('#credential-status').textContent = 'Credential을 입력하거나 삭제 버튼을 사용해 주세요.';
      tokenInput.focus();
      return;
    }
    let previous;
    try { previous = win.sessionStorage.getItem(authKey(apiBaseURL)); }
    catch {
      doc.querySelector('#credential-status').textContent = CREDENTIAL_FAILURE_MESSAGE;
      tokenInput.focus();
      return;
    }
    try {
      if (clear) win.sessionStorage.removeItem(authKey(apiBaseURL));
      else win.sessionStorage.setItem(authKey(apiBaseURL), token);
    } catch {
      doc.querySelector('#credential-status').textContent = CREDENTIAL_FAILURE_MESSAGE;
      tokenInput.focus();
      return;
    }
    let committed; let committedRead = false;
    try { committed = win.sessionStorage.getItem(authKey(apiBaseURL)); committedRead = true; }
    catch { /* Roll back below. */ }
    if (!committedRead || committed !== (clear ? null : token)) {
      let rollbackMutationFailed = false;
      try {
        if (previous === null) win.sessionStorage.removeItem(authKey(apiBaseURL));
        else win.sessionStorage.setItem(authKey(apiBaseURL), previous);
      } catch { rollbackMutationFailed = true; }
      let rollback; let rollbackRead = false;
      try { rollback = win.sessionStorage.getItem(authKey(apiBaseURL)); rollbackRead = true; }
      catch { /* The final state cannot be established. */ }
      if (!rollbackMutationFailed && rollbackRead && rollback === previous) {
        doc.querySelector('#credential-status').textContent = CREDENTIAL_FAILURE_MESSAGE;
        tokenInput.focus();
        return;
      }
      credentialIndeterminate.add(apiBaseURL);
      revisions.set(apiBaseURL, authRevision(apiBaseURL) + 1);
      doc.querySelector('#credential-status').textContent = CREDENTIAL_RECONCILIATION_MESSAGE;
      tokenInput.focus();
      invalidateCredentialBase(apiBaseURL);
      return;
    }
    credentialIndeterminate.delete(apiBaseURL);
    tokenInput.value = ''; revisions.set(apiBaseURL, authRevision(apiBaseURL) + 1); doc.querySelector('#credential-status').textContent = clear ? '현재 API credential을 삭제했습니다.' : '이 탭에 credential이 설정되었습니다.'; invalidateCredentialBase(apiBaseURL);
  }
  try {
    for (const purpose of ['diagnostics', 'topology']) { listen(ui(purpose).form, 'submit', event => { event.preventDefault(); start(purpose); }); listen(ui(purpose).cancel, 'click', () => cancel(purpose)); renderState(purpose); }
  listen(addTargetButton, 'click', () => { if (addTarget()) invalidate('diagnostics'); });
  listen(targetsEl, 'click', event => { if (event.target.closest('.icon-button')) invalidate('diagnostics'); });
  listen(doc.querySelector('#diagnostics-view'), 'input', event => { if (event.target.closest('#check-form')) invalidate('diagnostics'); });
  listen(doc.querySelector('#diagnostics-view'), 'change', event => { if (event.target.closest('#check-form')) invalidate('diagnostics'); });
  listen(doc.querySelector('#topology-form'), 'input', () => invalidate('topology'));
  listen(doc.querySelector('#api-base-url'), 'input', () => { invalidate('diagnostics'); invalidate('topology'); });
  listen(doc.querySelector('#public-auth-enabled'), 'change', () => { invalidate('diagnostics'); invalidate('topology'); });
  listen(doc.querySelector('#apply-bearer'), 'click', () => applyCredential(false)); listen(doc.querySelector('#clear-bearer'), 'click', () => applyCredential(true));
  const fullscreenStatus = doc.querySelector('#fullscreen-status');
  const fullscreenEntries = [
    { button: doc.querySelector('#topology-fullscreen'), target: doc.querySelector('#topology-result') },
    { button: doc.querySelector('#geo-map-fullscreen'), target: doc.querySelector('#geo-map-result') }
  ];
  let fullscreenInvoker = null; let fullscreenTarget = null; let fullscreenGeneration = 0;
  const ownsFullscreen = generation => activeInstance && !lifecycle.signal.aborted && generation === fullscreenGeneration;
  const announceFullscreen = (message, generation = fullscreenGeneration) => { if (ownsFullscreen(generation)) fullscreenStatus.textContent = message; };
  fullscreenEntries.forEach(entry => listen(entry.button, 'click', async () => {
    const generation = ++fullscreenGeneration;
    if (doc.fullscreenElement === entry.target) {
      if (typeof doc.exitFullscreen !== 'function') { announceFullscreen('전체 화면 종료를 지원하지 않습니다.', generation); return; }
      try {
        await doc.exitFullscreen();
        if (!ownsFullscreen(generation)) return;
      } catch { if (ownsFullscreen(generation)) announceFullscreen('전체 화면을 종료하지 못했습니다.', generation); }
      return;
    }
    if (typeof entry.target.requestFullscreen !== 'function') { announceFullscreen('이 브라우저는 전체 화면을 지원하지 않습니다.', generation); return; }
    fullscreenInvoker = entry.button; fullscreenTarget = entry.target;
    try {
      await entry.target.requestFullscreen();
      if (!ownsFullscreen(generation)) return;
    } catch {
      if (!ownsFullscreen(generation)) return;
      fullscreenTarget = null; entry.button.setAttribute('aria-pressed', 'false'); announceFullscreen('전체 화면을 시작하지 못했습니다.', generation);
    }
  }));
  listen(doc, 'fullscreenchange', () => {
    fullscreenEntries.forEach(entry => entry.button.setAttribute('aria-pressed', String(doc.fullscreenElement === entry.target)));
    if (doc.fullscreenElement) { fullscreenTarget = doc.fullscreenElement; announceFullscreen('전체 화면을 시작했습니다.'); }
    else if (fullscreenTarget) { const restore = fullscreenInvoker; fullscreenTarget = null; fullscreenInvoker = null; announceFullscreen('전체 화면을 종료했습니다.'); restore?.focus(); }
    scheduleTopologyResize();
  });
  function downloadOwned(purpose) {
    const lane = lanes[purpose]; if (lane.phase !== 'ready' || !lane.result?.report) return;
    const report = lane.result.report; const blob = new win.Blob([JSON.stringify(report, null, 2)], { type: 'application/json' });
    const link = doc.createElement('a'); link.href = win.URL.createObjectURL(blob); link.download = `${purpose === 'diagnostics' ? 'checknetwork' : 'checknetwork-topology'}-${report.id}.json`; link.click(); win.URL.revokeObjectURL(link.href);
  }
  listen(ui('diagnostics').download, 'click', () => downloadOwned('diagnostics'));
  listen(ui('topology').download, 'click', () => downloadOwned('topology'));
  listen(doc.querySelector('#download-human'), 'click', () => {
    const report = lanes.diagnostics.phase === 'ready' ? lanes.diagnostics.result?.report : null; if (!report) return;
    const blob = new win.Blob([humanReport(report)], { type: 'text/markdown;charset=utf-8' }); const link = doc.createElement('a');
    link.href = win.URL.createObjectURL(blob); link.download = 'checknetwork-human-report.md'; link.click(); win.URL.revokeObjectURL(link.href);
  });
  const analysisRoot = doc.querySelector('#analysis-report');
  listen(analysisRoot, 'click', event => {
    const toggle = event.target.closest('.finding-toggle'); if (!toggle) return;
    toggleFindingPanel(analysisRoot, toggle);
  });
  listen(analysisRoot, 'keydown', event => {
    const toggle = event.target.closest('.finding-toggle'); if (!toggle) return;
    if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggle.click(); return; }
    const toggles = [...analysisRoot.querySelectorAll('.finding-toggle')]; const index = toggles.indexOf(toggle);
    const target = event.key === 'Home' ? toggles[0] : event.key === 'End' ? toggles.at(-1) : event.key === 'ArrowDown' ? toggles[(index + 1) % toggles.length] : event.key === 'ArrowUp' ? toggles[(index - 1 + toggles.length) % toggles.length] : null;
    if (target) { event.preventDefault(); target.focus(); }
  });
  const topologyFilter = doc.querySelector('#topology-target-filter');
  listen(topologyFilter, 'change', event => {
    if (!currentTopologyReport || lanes.topology.phase !== 'ready') return;
    if (event.target.matches('[data-toggle-unresponsive]')) showUnresponsiveTopologyNodes = event.target.checked;
    else { const index = Number(event.target.dataset.targetIndex); if (!Number.isInteger(index)) return; if (event.target.checked) selectedTopologyTargets.add(index); else selectedTopologyTargets.delete(index); }
    startActiveTopologyRender();
  });
  listen(topologyFilter, 'click', event => {
    if (!currentTopologyReport || lanes.topology.phase !== 'ready' || !event.target.dataset.filterAction) return;
    selectedTopologyTargets = event.target.dataset.filterAction === 'all' ? new Set(currentTopologyReport.results.map((_, index) => index)) : new Set(); startActiveTopologyRender();
  });
  const topologyRoot = doc.querySelector('#topology-result');
  const topologyViewControls = doc.querySelector('#topology-view-controls');
  listen(topologyViewControls, 'change', event => {
    const mode = event.target.matches('input[name="topology-view-mode"]') ? event.target.value : '';
    if (mode !== '2d' && mode !== '3d') return;
    topologyViewState.mode = mode;
    doc.querySelector('#topology-view-status').textContent = `${mode === '3d' ? '3D' : '2D'} 그래프`;
    topologyViewState.transform = resetViewTransform();
    updateTopologyView({ mode, transform: topologyViewState.transform });
  });
  listen(doc.querySelector('#topology-view-reset'), 'click', () => {
    topologyViewState.transform = resetViewTransform();
    updateTopologyView({ transform: topologyViewState.transform, message: `${topologyViewState.mode === '3d' ? '3D' : '2D'} 그래프 보기를 초기화했습니다.` });
  });
  listen(topologyRoot, 'pointerdown', event => {
    const canvas = event.target.closest?.('canvas.topology-canvas');
    if (!canvas || event.button !== 0 || activeView !== 'topology') return;
    pointerInteraction = { canvas, x: event.clientX, y: event.clientY };
    try { canvas.setPointerCapture?.(event.pointerId); } catch { /* capture is an enhancement */ }
  });
  listen(topologyRoot, 'pointermove', event => {
    if (!pointerInteraction || event.target !== pointerInteraction.canvas || activeView !== 'topology') return;
    const dx = event.clientX - pointerInteraction.x; const dy = event.clientY - pointerInteraction.y;
    if (!Number.isFinite(dx) || !Number.isFinite(dy) || (dx === 0 && dy === 0)) return;
    const delta = topologyViewState.mode === '3d' ? { yaw: dx * 0.01, pitch: dy * 0.01 } : { panX: dx, panY: dy };
    const transform = updateViewTransform(topologyViewState.transform, delta);
    if (updateTopologyView({ transform })) {
      pointerInteraction.x = event.clientX; pointerInteraction.y = event.clientY;
      event.preventDefault();
    }
  });
  for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) listen(topologyRoot, type, event => {
    if (pointerInteraction?.canvas === event.target) pointerInteraction = null;
  });
  listen(topologyRoot, 'wheel', event => {
    if (!event.target.closest?.('canvas.topology-canvas') || activeView !== 'topology' || !Number.isFinite(event.deltaY) || event.deltaY === 0) return;
    const direction = event.deltaY < 0 ? 1 : -1;
    const transform = updateViewTransform(topologyViewState.transform, { zoom: direction * Math.max(0.05, topologyViewState.transform.zoom * 0.12) });
    if (updateTopologyView({ transform })) event.preventDefault();
  }, { passive: false });
  listen(topologyRoot, 'keydown', event => {
    if (!event.target.matches?.('canvas.topology-canvas') || activeView !== 'topology') return;
    let transform;
    if (event.key === 'Home') transform = resetViewTransform();
    else if (event.key === '+' || event.key === '=') transform = updateViewTransform(topologyViewState.transform, { zoom: Math.max(0.05, topologyViewState.transform.zoom * 0.12) });
    else if (event.key === '-') transform = updateViewTransform(topologyViewState.transform, { zoom: -Math.max(0.05, topologyViewState.transform.zoom * 0.12) });
    else if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
      const x = event.key === 'ArrowLeft' ? -1 : event.key === 'ArrowRight' ? 1 : 0;
      const y = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : 0;
      transform = topologyViewState.mode === '3d'
        ? updateViewTransform(topologyViewState.transform, { yaw: x * 0.12, pitch: y * 0.12 })
        : updateViewTransform(topologyViewState.transform, { panX: x * 24, panY: y * 24 });
    } else return;
    const message = event.key === 'Home' ? `${topologyViewState.mode === '3d' ? '3D' : '2D'} 그래프 보기를 초기화했습니다.` : undefined;
    if (updateTopologyView({ transform, message })) event.preventDefault();
  });
  listen(win, 'resize', scheduleTopologyResize);
  if (typeof win.ResizeObserver === 'function') {
    topologyResizeObserver = new win.ResizeObserver(() => scheduleTopologyResize());
  }
  const editTopologyNode = target => {
    const item = target.closest('.topology-node[data-label-address]');
    if (!item) return;
    populateTopologyLabelEditor(item.dataset.labelAddress);
  };
  listen(topologyRoot, 'click', event => editTopologyNode(event.target));
  listen(doc.querySelector('#ip-label-form'), 'submit', event => {
    event.preventDefault(); const ip = doc.querySelector('#ip-label-address').value.trim(); const result = upsertIPLabels([{ ip, label: doc.querySelector('#ip-label-name').value.trim(), note: doc.querySelector('#ip-label-note').value }], { manual: true });
    const message = doc.querySelector('#ip-label-message');
    message.textContent = result.reason === 'characters' ? `표시 라벨은 ${LABEL}자, 설명은 ${NOTE}자 이하여야 합니다.` : result.reason === 'capacity' ? `IP 라벨은 최대 ${LABEL_RECORDS}개까지 저장할 수 있습니다.` : result.imported ? `${ip} 매핑을 저장했습니다.` : '올바른 IPv4/IPv6 주소와 라벨을 입력해 주세요.';
    if (result.imported) event.target.reset();
  });
  listen(doc.querySelector('#ip-label-import'), 'change', async event => {
    const file = event.target.files?.[0]; if (!file) return; const message = doc.querySelector('#ip-label-message');
    const generation = ++ipLabelImportGeneration;
    const ownsImport = () => activeInstance && !lifecycle.signal.aborted && generation === ipLabelImportGeneration;
    try {
      if (Number(file.size) > LABEL_FILE) { if (ownsImport()) message.textContent = '파일 크기는 1 MiB 이하여야 합니다.'; return; }
      const text = await file.text();
      if (!ownsImport()) return;
      const result = upsertIPLabels(parseIPLabelImport(text, file.name));
      if (!ownsImport()) return;
      message.textContent = `${result.imported}개 매핑을 가져왔습니다. ${result.invalid.length}개 행은 유효하지 않아 제외했고 ${result.omitted}개 행은 한도를 초과하거나 중복되어 생략했습니다.`;
    } catch { if (ownsImport()) message.textContent = '파일을 가져오지 못했습니다.'; }
    finally { if (ownsImport()) event.target.value = ''; }
  });
  listen(doc.querySelector('#ip-label-rows'), 'change', event => {
    const row = event.target.closest('tr[data-ip]'); const mapping = row && ipLabels.get(row.dataset.ip); if (!mapping || !event.target.dataset.field) return;
    const maximum = event.target.dataset.field === 'label' ? LABEL : NOTE; mapping[event.target.dataset.field] = truncateCharacters(event.target.value, maximum); event.target.value = mapping[event.target.dataset.field];
    if (!mapping.label) { const index = [...row.parentElement.children].indexOf(row); ipLabels.delete(row.dataset.ip); saveIPLabels(); renderIPLabelTable({ focusIndex: index }); return; } saveIPLabels(); refreshRenderedLabels();
  });
  listen(doc.querySelector('#ip-label-rows'), 'click', event => {
    if (!event.target.classList.contains('mapping-delete')) return; const row = event.target.closest('tr[data-ip]');
    if (row) { const index = [...row.parentElement.children].indexOf(row); ipLabels.delete(row.dataset.ip); saveIPLabels(); renderIPLabelTable({ focusIndex: index }); refreshRenderedLabels(); }
  });
  listen(doc.querySelector('#ip-label-prev'), 'click', () => { if (ipLabelPage > 0) { ipLabelPage--; renderIPLabelTable(); } });
  listen(doc.querySelector('#ip-label-next'), 'click', () => { if ((ipLabelPage + 1) * LABEL_PAGE < ipLabels.size) { ipLabelPage++; renderIPLabelTable(); } });
  listen(doc.querySelector('#topology-label-address'), 'change', event => {
    resetTopologyLabelEditorToIP();
    const ip = event.target.value.trim();
    if (isIPAddress(ip)) populateTopologyLabelEditor(ip);
  });
  listen(doc.querySelector('#topology-label-form'), 'submit', event => {
    event.preventDefault();
    const label = doc.querySelector('#topology-label-name').value.trim();
    const note = doc.querySelector('#topology-label-note').value.trim();
    const message = doc.querySelector('#topology-label-message');
    const ip = doc.querySelector('#topology-label-address').value.trim();
    const result = upsertIPLabels([{ ip, label, note }], { manual: true });
    if (!result.imported) { message.textContent = result.reason === 'characters' ? `표시 라벨은 ${LABEL}자, 설명은 ${NOTE}자 이하여야 합니다.` : result.reason === 'capacity' ? `IP 라벨은 최대 ${LABEL_RECORDS}개까지 저장할 수 있습니다.` : '올바른 IPv4/IPv6 주소와 라벨을 입력해 주세요.'; message.className = 'mapping-message failure'; return; }
    message.textContent = `${ip} 라벨을 저장하고 토폴로지에 반영했습니다.`; message.className = 'mapping-message healthy'; doc.querySelector('#topology-label-delete').hidden = false;
  });
  listen(doc.querySelector('#topology-label-delete'), 'click', () => {
    const subject = doc.querySelector('#topology-label-address').value.trim();
    const removed = ipLabels.delete(canonicalIPAddress(subject));
    if (!removed) return;
    saveIPLabels(); renderIPLabelTable();
    refreshRenderedLabels(); doc.querySelector('#topology-label-name').value = ''; doc.querySelector('#topology-label-note').value = ''; doc.querySelector('#topology-label-delete').hidden = true;
    const message = doc.querySelector('#topology-label-message'); message.textContent = `${subject} 라벨을 삭제했습니다.`; message.className = 'mapping-message healthy';
  });

  const switchView = (target, { focus = false } = {}) => {
    const previousView = activeView;
    if (previousView === 'diagnostics' && target !== 'diagnostics') cancel('diagnostics', 'navigation');
    if (previousView === 'topology' && target !== 'topology') cancel('topology', 'navigation');
    activeView = activateView(doc, target);
    if (previousView !== activeView) {
      if (activeView !== 'diagnostics') unmountDiagnosticsPresentation();
      if (activeView !== 'ip-labels') unmountIPLabelTable();
      if (!['topology', 'geo-map'].includes(activeView)) unmountInactiveTopologyPresentation();
      if (['topology', 'geo-map'].includes(activeView) && currentTopologyReport && lanes.topology.phase === 'ready') startActiveTopologyRender();
      else if (activeView === 'ip-labels') renderIPLabelTable();
      else if (activeView === 'diagnostics') mountOwnedDiagnosticsPresentation();
    }
    if (focus) {
      const heading = doc.querySelector(`#${activeView}-view h2`);
      if (heading) { heading.tabIndex = -1; heading.focus(); }
    }
  };
  doc.querySelectorAll('[data-view-link]').forEach(link => listen(link, 'click', event => {
    event.preventDefault(); const target = link.dataset.viewLink; switchView(target, { focus: true }); win.location.hash = `#${target}`;
  }));
  listen(win, 'hashchange', () => switchView(viewFromHash(win.location.hash), { focus: true }));
  listen(win, 'beforeunload', () => {
    for (const [purpose, request] of [...active.entries()]) {
      request.reason = 'beforeunload'; safeClearTimer(request.timer);
      try { request.controller.abort('beforeunload'); } finally { if (active.get(purpose) === request) active.delete(purpose); }
    }
    disposeTopologyRender('beforeunload', true);
  });
  const labelMessage = doc.querySelector('#ip-label-message');
  if (ipLabelLoadStatus.invalid || ipLabelLoadStatus.omitted) labelMessage.textContent = `저장된 라벨을 정리했습니다. ${ipLabelLoadStatus.invalid}개는 유효하지 않아 제외했고 ${ipLabelLoadStatus.omitted}개는 중복 또는 한도 초과로 생략했습니다.`;
  renderIPLabelTable(); switchView(viewFromHash(win.location.hash), { focus: true });
  } catch (error) {
    destroy();
    throw error;
  }
  const getState = () => ({ diagnostics: lanes.diagnostics, topology: lanes.topology });
  function destroy() {
    if (!activeInstance) return false;
    activeInstance = false;
    ipLabelImportGeneration++;
    lifecycle.abort('destroy');
    stopTopologyResizeObservation();
    topologyResizeObserver = null;
    for (const remove of [...listeners].reverse()) { try { remove(); } catch { /* best-effort listener cleanup */ } }
    for (const request of active.values()) {
      request.reason = 'destroy';
      safeClearTimer(request.timer);
      try { request.controller.abort('destroy'); } catch { /* best-effort request cleanup */ }
    }
    active.clear();
    cancelIPLabelRender();
    const labelRoot = doc.querySelector('#ip-label-rows');
    if (LABEL_ROOT_OWNERS.get(labelRoot)?.instance === labelInstance) LABEL_ROOT_OWNERS.delete(labelRoot);
    try { renderCoordinator?.dispose(); } catch { /* scheduler cleanup must not break destruction */ }
    renderSource = undefined; currentReport = undefined; currentTopologyReport = undefined; currentTopologyModel = undefined;
    return true;
  }
  return { start, cancel, invalidate, destroy, getState, getOwnedReport: purpose => activeInstance && lanes[purpose]?.phase === 'ready' ? lanes[purpose].result?.report : null };
}

export function bootstrap(dependencies) { return createApp(dependencies); }
export { parseIPLabelImport, isIPAddress, viewFromHash, activateView };

if (globalThis.document?.querySelector('#check-form') && globalThis.window) bootstrap({ document: globalThis.document, window: globalThis.window });
