import {
  createRequestLane, canonicalDiagnosticsInput, canonicalTopologyInput, inputSignature,
  transitionRequest, ownsRequest, clientTimeoutMS, normalizeRequestError, parseResponse, SCHEMA_LIMITS
} from './state.js';
import { canonicalIP, topologyModelFromReport, filterTopologyModel } from './topology-model.js';
import { TopologyRenderCoordinator } from './topology-renderer.js';

const IP_LABEL_STORAGE_KEY = 'checknetwork.ip-labels.v1';
export const LABEL_FILE = 1024 * 1024;
export const LABEL_RECORDS = 500;
export const LABEL_PAGE = 100;
export const LABEL = 256;
export const NOTE = 1024;

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
function renderAnalysisWorkspace(doc, report) {
  const root = doc.querySelector('#analysis-report');
  root.replaceChildren();
  root.hidden = false;
  const analysis = report.analysis;
  const verdict = analysisSection(doc, 'verdict');
  const verdictText = { healthy: '관측 범위에서 이상 징후 없음', attention: '확인 필요', inconclusive: '판단 보류' };
  const header = makeNode(doc, 'div', undefined, `analysis-verdict verdict-${analysis?.verdict || 'inconclusive'}`);
  header.append(makeNode(doc, 'strong', analysis ? verdictText[analysis.verdict] : '판단 보류'));
  header.append(makeNode(doc, 'span', `리포트 ${report.id}`));
  header.append(makeNode(doc, 'time', new Date(report.started_at).toISOString()));
  header.append(makeNode(doc, 'span', `${report.duration_ms} ms`));
  verdict.append(header);
  root.append(verdict);

  if (!analysis) {
    const unsupported = analysisSection(doc, 'findings', '자동 분석 미지원');
    unsupported.append(makeNode(doc, 'p', '이 서버는 자동 분석을 제공하지 않아 판단 보류입니다. 원시 측정 결과를 검토해 주세요.'));
    root.append(unsupported, analysisSection(doc, 'evidence', '근거'), analysisSection(doc, 'actions', '다음 조치'), analysisSection(doc, 'coverage', 'Coverage'));
  } else {
    const findings = analysisSection(doc, 'findings', '원인 후보');
    const evidenceByID = new Map(analysis.evidence.map(item => [item.id, item]));
    const actionByID = new Map(analysis.actions.map(item => [item.id, item]));
    if (!analysis.findings.length) findings.append(makeNode(doc, 'p', analysis.verdict === 'healthy' ? '원인 후보 없음' : '판단에 필요한 원인 후보가 제공되지 않았습니다.'));
    analysis.findings.forEach((finding, index) => {
      const severity = ['critical', 'warning', 'info'].includes(finding.severity) ? finding.severity : 'info';
      const article = makeNode(doc, 'article', undefined, `finding finding-${severity}`);
      article.dataset.severity = severity;
      article.append(makeNode(doc, 'h4', finding.title), makeNode(doc, 'p', finding.summary), makeNode(doc, 'p', `근거 신뢰도: ${{ direct: '높음', corroborated: '중간', limited: '낮음' }[finding.confidence]}`));
      const toggle = makeNode(doc, 'button', '근거와 조치 보기', 'finding-toggle');
      toggle.type = 'button'; toggle.setAttribute('aria-expanded', 'false'); toggle.dataset.findingIndex = String(index);
      const panel = makeNode(doc, 'div'); panel.id = `finding-panel-${index}`; panel.hidden = true; toggle.setAttribute('aria-controls', panel.id);
      article.append(toggle, panel); findings.append(article);

      const scroll = makeNode(doc, 'div', undefined, 'evidence-scroll');
      const table = makeNode(doc, 'table'); table.append(makeNode(doc, 'caption', `${finding.title} 근거`));
      const head = makeNode(doc, 'thead'); const headRow = makeNode(doc, 'tr');
      ['결과', '종류', '주소', '신호', '관측값', '출처'].forEach(label => { const th = makeNode(doc, 'th', label); th.scope = 'col'; headRow.append(th); });
      head.append(headRow); table.append(head);
      const body = makeNode(doc, 'tbody');
      finding.evidence_ids.map(id => evidenceByID.get(id)).filter(Boolean).forEach(item => {
        const row = makeNode(doc, 'tr');
        [item.result_index, item.kind, item.address, item.signal, item.observed, item.provenance].forEach(value => row.append(makeNode(doc, 'td', value)));
        body.append(row);
      });
      table.append(body); scroll.append(table);
      const evidenceRegion = analysisSection(doc, 'evidence', '근거');
      const evidenceTitle = evidenceRegion.querySelector('h3'); evidenceTitle.id = `finding-evidence-title-${index}`; scroll.tabIndex = 0; scroll.setAttribute('aria-labelledby', evidenceTitle.id);
      evidenceRegion.append(scroll); panel.append(evidenceRegion);

      const list = makeNode(doc, 'ol');
      finding.action_ids.map(id => actionByID.get(id)).filter(Boolean).forEach((item, actionIndex) => {
        const li = makeNode(doc, 'li'); const checkbox = makeNode(doc, 'input'); checkbox.type = 'checkbox'; checkbox.id = `action-${index}-${actionIndex}`;
        const label = makeNode(doc, 'label', item.title); label.htmlFor = checkbox.id;
        li.append(checkbox, label, makeNode(doc, 'p', `확인: ${item.step}`), makeNode(doc, 'p', `예상: ${item.expected_result}`), makeNode(doc, 'p', `에스컬레이션: ${item.escalation_condition}`)); list.append(li);
      });
      const actionsRegion = analysisSection(doc, 'actions', '다음 조치'); actionsRegion.append(list); panel.append(actionsRegion);
    });
    root.append(findings);
    const coverage = analysisSection(doc, 'coverage', 'Coverage와 한계');
    [['사용 가능', analysis.coverage.available], ['누락', analysis.coverage.missing]].forEach(([label, values]) => {
      coverage.append(makeNode(doc, 'h4', label)); const list = makeNode(doc, 'ul'); values.forEach(value => list.append(makeNode(doc, 'li', value))); coverage.append(list);
    });
    for (const enrichment of analysis.coverage.enrichment) {
      coverage.append(makeNode(doc, 'h4', 'Enrichment'));
      const summary = makeNode(doc, 'ul');
      for (const value of [
        `Source: ${enrichment.source}`,
        `Cache hits: ${enrichment.cache_hits}`,
        `Upstream fetches: ${enrichment.upstream_fetches}`,
        `Maximum age: ${enrichment.max_age_ms} ms`
      ]) summary.append(makeNode(doc, 'li', value));
      for (const failure of enrichment.failures) {
        summary.append(makeNode(doc, 'li', `${failure.kind}: ${failure.count} (${failure.retryable ? 'retryable' : 'not retryable'})`));
      }
      coverage.append(summary);
    }
    [...analysis.coverage.provider_failures, ...analysis.coverage.limitations].forEach(issue => coverage.append(makeNode(doc, 'p', issue.reason)));
    root.append(coverage);
  }
  const raw = analysisSection(doc, 'raw', '원시 결과'); const details = makeNode(doc, 'details'); details.append(makeNode(doc, 'summary', '원시 측정 결과 보기'));
  const pre = makeNode(doc, 'pre'); pre.tabIndex = 0; pre.setAttribute('aria-label', '원시 측정 결과 JSON'); pre.textContent = JSON.stringify(report.results, null, 2); details.append(pre); raw.append(details); root.append(raw);
}
function renderSafeResults(doc, report) {
  const summary = doc.querySelector('#summary'); summary.replaceChildren();
  const statusText = { healthy: '정상', degraded: '일부 장애', unreachable: '연결 불가' };
  const status = makeNode(doc, 'div', undefined, `status ${report.status}`); status.append(makeNode(doc, 'span'), doc.createTextNode(statusText[report.status])); summary.append(status);
  const coverage = report.analysis?.coverage;
  const coverageSummary = coverage ? `${coverage.available.length} 사용 가능 · ${coverage.missing.length + coverage.provider_failures.length + coverage.limitations.length} 한계` : '분석 미지원';
  const metrics = makeNode(doc, 'dl');
  for (const [label, value] of [['전체 검사', report.summary.total], ['성공', report.summary.passed], ['실패', report.summary.failed], ['전체 소요', `${report.duration_ms} ms`], ['관측 위치', 'API 서버 실행 환경'], ['Coverage', coverageSummary]]) {
    const item = makeNode(doc, 'div'); item.append(makeNode(doc, 'dt', label), makeNode(doc, 'dd', value)); metrics.append(item);
  }
  summary.append(metrics);
  const results = doc.querySelector('#results'); results.replaceChildren();
  report.results.forEach(item => {
    const article = makeNode(doc, 'article', undefined, 'result');
    const content = makeNode(doc, 'div');
    content.append(makeNode(doc, 'h3', item.address), makeNode(doc, 'p', item.message || '연결과 응답이 정상입니다.'));
    const outcome = makeNode(doc, 'strong', item.status === 'healthy' ? 'PASS' : 'FAIL', item.status);
    article.append(makeNode(doc, 'span', item.kind.toUpperCase(), 'kind'), content, outcome, makeNode(doc, 'time', `${item.latency_ms} ms`));
    results.append(article);
  });
  renderAnalysisWorkspace(doc, report);
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
  const lifecycle = new win.AbortController();
  const listeners = [];
  const listen = (target, type, handler, options) => {
    target.addEventListener(type, handler, options);
    listeners.push(() => target.removeEventListener(type, handler, options));
  };
  const targetsEl = doc.querySelector('#targets');
  let currentReport;
  let currentTopologyReport;
  let selectedTopologyTargets = new Set();
  let showUnresponsiveTopologyNodes = true;
  let ipLabelPage = 0;
  let ipLabelRenderGeneration = 0;
  let ipLabelImportGeneration = 0;
  let ipLabelRenderHandle;
  let ipLabels = new Map();
  let ipLabelLoadStatus = { invalid: 0, omitted: 0 };

  const scheduleIPLabelRender = scheduleLabelRender;
  function addTarget(kind = 'dns', address = '') {
    const row = doc.querySelector('#target-template').content.firstElementChild.cloneNode(true);
    const select = row.querySelector('select');
    const input = row.querySelector('input');
    const expected = row.querySelector('.expected');
    select.value = kind;
    input.value = address;
    const sync = () => {
      input.placeholder = placeholders[select.value];
      expected.hidden = !['http', 'https'].includes(select.value);
    };
    listen(select, 'change', sync);
    listen(row.querySelector('button'), 'click', () => {
      if (targetsEl.children.length > 1) row.remove();
    });
    sync();
    targetsEl.append(row);
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

  function makeIPLabelRow(row) {
    const tr = doc.createElement('tr'); tr.dataset.ip = row.ip;
    const addressCell = doc.createElement('td'); const code = doc.createElement('code'); code.textContent = row.ip; addressCell.append(code);
    const labelCell = doc.createElement('td'); const label = doc.createElement('input'); label.dataset.field = 'label'; label.value = row.label; label.maxLength = LABEL; label.setAttribute('aria-label', `${row.ip} 표시 라벨`); labelCell.append(label);
    const noteCell = doc.createElement('td'); const note = doc.createElement('input'); note.dataset.field = 'note'; note.value = row.note; note.maxLength = NOTE; note.setAttribute('aria-label', `${row.ip} 설명`); noteCell.append(note);
    const actionCell = doc.createElement('td'); const remove = doc.createElement('button'); remove.type = 'button'; remove.className = 'mapping-delete'; remove.textContent = '삭제'; actionCell.append(remove);
    tr.append(addressCell, labelCell, noteCell, actionCell);
    return tr;
  }

  function renderIPLabelTable({ focusIndex } = {}) {
    if (!activeInstance) return;
    const root = doc.querySelector('#ip-label-rows'); if (!root) return;
    const rows = [...ipLabels.values()]; const pages = Math.max(1, Math.ceil(rows.length / LABEL_PAGE));
    ipLabelPage = Math.max(0, Math.min(ipLabelPage, pages - 1));
    const visible = rows.slice(ipLabelPage * LABEL_PAGE, (ipLabelPage + 1) * LABEL_PAGE);
    const previous = doc.querySelector('#ip-label-prev'); const next = doc.querySelector('#ip-label-next'); const status = doc.querySelector('#ip-label-page-status');
    previous.disabled = ipLabelPage === 0; next.disabled = ipLabelPage >= pages - 1; status.textContent = `${ipLabelPage + 1} / ${pages}`;
    const generation = ++ipLabelRenderGeneration; root.replaceChildren(); let offset = 0;
    if (!visible.length) {
      const row = doc.createElement('tr'); const cell = doc.createElement('td'); cell.colSpan = 4; cell.className = 'empty-mapping'; cell.textContent = '등록된 IP 라벨이 없습니다.'; row.append(cell); root.append(row); return;
    }
    const appendChunk = () => {
      ipLabelRenderHandle = undefined;
      if (!activeInstance || generation !== ipLabelRenderGeneration) return;
      const fragment = doc.createDocumentFragment();
      visible.slice(offset, offset + 10).forEach(row => fragment.append(makeIPLabelRow(row)));
      root.append(fragment); offset += 10;
      if (offset < visible.length) { ipLabelRenderHandle = scheduleIPLabelRender(appendChunk); return; }
      if (Number.isInteger(focusIndex)) root.querySelectorAll('.mapping-delete')[Math.min(focusIndex, visible.length - 1)]?.focus();
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
  if (!targetsEl.children.length) { addTarget('dns', 'example.com'); addTarget('tcp', '1.1.1.1:443'); addTarget('https', 'https://example.com'); addTarget('traceroute', 'example.com'); }
  const lanes = { diagnostics: createRequestLane('diagnostics'), topology: createRequestLane('topology') };
  const active = new Map(); const revisions = new Map(); let ownerSequence = 0;
  let activeView = viewFromHash(win.location.hash);
  let currentTopologyModel;
  let renderSource;
  let renderPhase = 'idle';
  let finalTopologyPlan;
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
    line.textContent = `${serverText} · 현재 보기 ${viewText}${limited}`;
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
    canvas.dataset.markers = String(geo.markers.length);
    canvas.dataset.segments = String(geo.segments.length);
    canvas.dataset.arrows = String(geo.arrows.length);
    const markerByID = new Map(geo.markers.map(marker => [marker.node_id, marker]));
    let pending = null;
    const draw = () => {
      pending = null;
      if (signal.aborted || !canvas.isConnected) return;
      let context;
      try { context = canvas.getContext?.('2d'); } catch { return; }
      if (!context) return;
      const rect = canvas.getBoundingClientRect();
      const cssWidth = Math.max(1, Math.round(rect.width || canvas.clientWidth || 960));
      const cssHeight = Math.max(1, Math.round(rect.height || canvas.clientHeight || 540));
      const ratio = Math.max(1, Math.min(3, Number(win.devicePixelRatio) || 1));
      canvas.width = Math.round(cssWidth * ratio); canvas.height = Math.round(cssHeight * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      context.clearRect(0, 0, cssWidth, cssHeight);
      const project = marker => ({ x: (marker.longitude + 180) / 360 * cssWidth, y: (90 - marker.latitude) / 180 * cssHeight });
      context.lineWidth = 2; context.strokeStyle = '#67d5ff'; context.fillStyle = '#c9ff46';
      for (const segment of geo.segments) {
        const from = markerByID.get(segment.from); const to = markerByID.get(segment.to); if (!from || !to) continue;
        const a = project(from); const b = project(to); context.beginPath(); context.moveTo(a.x, a.y); context.lineTo(b.x, b.y); context.stroke();
        const angle = Math.atan2(b.y - a.y, b.x - a.x); const mx = (a.x + b.x) / 2; const my = (a.y + b.y) / 2;
        context.beginPath(); context.moveTo(mx, my); context.lineTo(mx - 8 * Math.cos(angle - .45), my - 8 * Math.sin(angle - .45)); context.lineTo(mx - 8 * Math.cos(angle + .45), my - 8 * Math.sin(angle + .45)); context.closePath(); context.fill();
      }
      for (const marker of geo.markers) { const point = project(marker); context.beginPath(); context.arc(point.x, point.y, 4, 0, Math.PI * 2); context.fill(); }
    };
    const scheduleDraw = () => { if (pending === null) pending = selectedScheduler.schedule(draw); };
    draw();
    listen(win, 'resize', scheduleDraw);
    return () => { win.removeEventListener('resize', scheduleDraw); if (pending !== null) selectedScheduler.cancel(pending); pending = null; };
  }
  function startActiveTopologyRender() {
    renderCoordinator.cancel('view-change');
    doc.querySelector(activeView === 'geo-map' ? '#topology-result' : '#geo-map-result').replaceChildren();
    doc.querySelector(activeView === 'geo-map' ? '#topology-render-status' : '#geo-render-status').textContent = '';
    if (!currentTopologyReport || !currentTopologyModel || !renderSource || !['topology', 'geo-map'].includes(activeView)) {
      renderPhase = 'idle';
      renderState('topology');
      return;
    }
    const geo = activeView === 'geo-map';
    const root = doc.querySelector(geo ? '#geo-map-result' : '#topology-result');
    const status = doc.querySelector(geo ? '#geo-render-status' : '#topology-render-status');
    renderPhase = 'rendering';
    finalTopologyPlan = undefined;
    if (!geo) { renderTopologySummary(); renderTopologyTargetFilter(currentTopologyReport.results || []); }
    renderCoordinator.start({
      ownerId: renderSource.renderOwnerId, inputSignature: renderSource.signature,
      view: geo ? 'geo' : 'topology', model: filteredTopologyModel(), root, status,
      workspace: {
        disposeHiddenViews() { doc.querySelector(geo ? '#topology-result' : '#geo-map-result').replaceChildren(); },
        drawGeo
      }
    });
    renderState('topology');
  }
  function disposeTopologyRender(reason = 'dispose', clearSource = false) {
    renderCoordinator.cancel(reason);
    renderPhase = 'idle';
    doc.querySelector('#topology-result').replaceChildren(); doc.querySelector('#geo-map-result').replaceChildren();
    doc.querySelector('#topology-render-status').textContent = ''; doc.querySelector('#geo-render-status').textContent = '';
    if (clearSource) renderSource = undefined;
    if (clearSource) finalTopologyPlan = undefined;
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
      try { request.controller.abort('input-change'); } finally { active.delete(purpose); }
    }
    clearPurpose(purpose); let signature = ''; try { const input = readInput(purpose); signature = inputSignature(purpose, input, authRevision(input.apiBaseURL)); } catch { /* invalid input remains idle */ }
    lanes[purpose] = transitionRequest(lanes[purpose], { type: 'INPUT_CHANGED', inputSignature: signature }); renderState(purpose);
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
    const request = { ownerId, signature, controller, reason: null, timer: null }; active.set(purpose, request);
    lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_STARTED', ownerId, inputSignature: signature, startedAt }); renderState(purpose); doc.querySelector('#request-live').textContent = purpose === 'diagnostics' ? '진단을 시작했습니다.' : '경로 분석을 시작했습니다.';
    try {
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
      if (purpose === 'topology') topologyModel = topologyModelFromReport(report);
      lanes[purpose] = transitionRequest(lanes[purpose], { type: 'REQUEST_SUCCEEDED', ownerId, inputSignature: signature, report, completedAt: clock() });
      if (purpose === 'diagnostics') { currentReport = report; renderSafeResults(doc, report); ui(purpose).report.hidden = false; doc.querySelector('#analysis-title').focus(); }
      else {
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
    const token = doc.querySelector('#bearer-token').value.trim(); try { if (clear || !token) win.sessionStorage.removeItem(authKey(apiBaseURL)); else win.sessionStorage.setItem(authKey(apiBaseURL), token); } catch { /* memory-only status below */ }
    doc.querySelector('#bearer-token').value = ''; revisions.set(apiBaseURL, authRevision(apiBaseURL) + 1); doc.querySelector('#credential-status').textContent = clear ? '현재 API credential을 삭제했습니다.' : '이 탭에 credential이 설정되었습니다.'; invalidate('diagnostics'); invalidate('topology');
  }
  try {
    for (const purpose of ['diagnostics', 'topology']) { listen(ui(purpose).form, 'submit', event => { event.preventDefault(); start(purpose); }); listen(ui(purpose).cancel, 'click', () => cancel(purpose)); renderState(purpose); }
  listen(doc.querySelector('#add-target'), 'click', () => { addTarget(); invalidate('diagnostics'); });
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
  });
  function humanReport(report) {
    const targets = report.results.map(result => result.address).filter(Boolean).sort((left, right) => right.length - left.length);
    const targetPatterns = targets.map(target => new RegExp(target.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi'));
    const redact = value => targetPatterns.reduce((text, pattern) => text.replace(pattern, '[REDACTED TARGET]'), String(value || ''));
    const lines = [`# CheckNetwork 보고서 ${redact(report.id)}`, '', `판정: ${report.analysis?.verdict || 'inconclusive'}`, `실행 시각: ${report.started_at}`, `소요 시간: ${report.duration_ms} ms`, ''];
    if (!report.analysis) return [...lines, '자동 분석: 이 서버에서 제공하지 않음'].join('\n');
    lines.push('## 원인 후보');
    for (const finding of report.analysis.findings) lines.push(`- [${finding.severity}] ${redact(finding.title)} — ${redact(finding.summary)} (근거 신뢰도: ${finding.confidence})`);
    if (!report.analysis.findings.length) lines.push('- 관측 범위에서 원인 후보 없음');
    lines.push('', '## 다음 조치');
    for (const action of report.analysis.actions) lines.push(`- ${redact(action.title)}: ${redact(action.step)}\n  - 예상: ${redact(action.expected_result)}\n  - 에스컬레이션: ${redact(action.escalation_condition)}`);
    lines.push('', '## Coverage', `- 사용 가능 신호: ${report.analysis.coverage.available.length}`, `- 누락 신호: ${report.analysis.coverage.missing.length}`, `- Provider 실패: ${report.analysis.coverage.provider_failures.length}`, `- 한계: ${report.analysis.coverage.limitations.length}`);
    return lines.join('\n');
  }
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
    const panel = doc.getElementById(toggle.getAttribute('aria-controls')); const expanded = toggle.getAttribute('aria-expanded') === 'true';
    toggle.setAttribute('aria-expanded', String(!expanded)); if (panel) panel.hidden = expanded;
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
      if (['topology', 'geo-map'].includes(activeView) && currentTopologyReport && lanes.topology.phase === 'ready') startActiveTopologyRender();
      else disposeTopologyRender('view-change');
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
    for (const remove of listeners.splice(0).reverse()) { try { remove(); } catch { /* best-effort listener cleanup */ } }
    for (const request of active.values()) {
      request.reason = 'destroy';
      safeClearTimer(request.timer);
      try { request.controller.abort('destroy'); } catch { /* best-effort request cleanup */ }
    }
    active.clear();
    ipLabelRenderGeneration++;
    if (ipLabelRenderHandle !== undefined) { try { cancelLabelRender(ipLabelRenderHandle); } catch { /* generation still invalidates the callback */ } }
    ipLabelRenderHandle = undefined;
    try { renderCoordinator?.dispose(); } catch { /* scheduler cleanup must not break destruction */ }
    renderSource = undefined; currentReport = undefined; currentTopologyReport = undefined; currentTopologyModel = undefined;
    return true;
  }
  return { start, cancel, invalidate, destroy, getState, getOwnedReport: purpose => activeInstance && lanes[purpose]?.phase === 'ready' ? lanes[purpose].result?.report : null };
}

export function bootstrap(dependencies) { return createApp(dependencies); }
export { parseIPLabelImport, isIPAddress, viewFromHash, activateView };

if (globalThis.document?.querySelector('#check-form') && globalThis.window) bootstrap({ document: globalThis.document, window: globalThis.window });
