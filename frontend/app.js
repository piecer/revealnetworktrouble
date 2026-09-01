const targetsEl = document.querySelector('#targets');
const form = document.querySelector('#check-form');
const errorEl = document.querySelector('#error');
const reportSection = document.querySelector('#report-section');
let currentReport;

const placeholders = { dns: 'example.com', tcp: '1.1.1.1:443', http: 'https://example.com' };

function addTarget(kind = 'dns', address = '') {
  const row = document.querySelector('#target-template').content.firstElementChild.cloneNode(true);
  const select = row.querySelector('select');
  const input = row.querySelector('input');
  const expected = row.querySelector('.expected');
  select.value = kind;
  input.value = address;
  const sync = () => {
    input.placeholder = placeholders[select.value];
    expected.hidden = select.value !== 'http';
  };
  select.addEventListener('change', sync);
  row.querySelector('button').addEventListener('click', () => {
    if (targetsEl.children.length > 1) row.remove();
  });
  sync();
  targetsEl.append(row);
}

function collectTargets() {
  return [...targetsEl.children].map(row => {
    const kind = row.querySelector('select').value;
    const target = { kind, address: row.querySelector('input').value.trim() };
    if (kind === 'http') target.expected_status = Number(row.querySelector('.expected').value);
    return target;
  });
}

function render(report) {
  const statusText = { healthy: '정상', degraded: '일부 장애', unreachable: '연결 불가' };
  document.querySelector('#summary').innerHTML = `
    <div class="status ${report.status}"><span></span>${statusText[report.status]}</div>
    <dl><div><dt>전체 검사</dt><dd>${report.summary.total}</dd></div><div><dt>성공</dt><dd>${report.summary.passed}</dd></div><div><dt>실패</dt><dd>${report.summary.failed}</dd></div><div><dt>전체 소요</dt><dd>${report.duration_ms} ms</dd></div></dl>`;
  document.querySelector('#results').innerHTML = report.results.map(result => `
    <article class="result">
      <span class="kind">${result.kind.toUpperCase()}</span>
      <div><h3>${escapeHTML(result.address)}</h3><p>${escapeHTML(result.message || '연결과 응답이 정상입니다.')}</p></div>
      <strong class="${result.status}">${result.status === 'healthy' ? 'PASS' : 'FAIL'}</strong>
      <time>${result.latency_ms} ms</time>
    </article>`).join('');
  reportSection.hidden = false;
}

function escapeHTML(value) {
  const el = document.createElement('span');
  el.textContent = value;
  return el.innerHTML;
}

document.querySelector('#add-target').addEventListener('click', () => addTarget());
form.addEventListener('submit', async event => {
  event.preventDefault();
  errorEl.hidden = true;
  const button = document.querySelector('#run');
  button.disabled = true;
  button.firstChild.textContent = '진단 중 ';
  try {
    const apiURL = document.querySelector('#api-url').value.replace(/\/$/, '');
    const response = await fetch(`${apiURL}/api/v1/reports`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ targets: collectTargets(), timeout_ms: Number(document.querySelector('#timeout').value) })
    });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message || `API 오류 (${response.status})`);
    currentReport = data;
    render(data);
  } catch (error) {
    errorEl.textContent = `진단을 완료하지 못했습니다: ${error.message}`;
    errorEl.hidden = false;
  } finally {
    button.disabled = false;
    button.firstChild.textContent = '진단 시작 ';
  }
});

document.querySelector('#download').addEventListener('click', () => {
  const blob = new Blob([JSON.stringify(currentReport, null, 2)], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `checknetwork-${currentReport.id}.json`;
  link.click();
  URL.revokeObjectURL(link.href);
});

addTarget('dns', 'example.com');
addTarget('tcp', '1.1.1.1:443');
addTarget('http', 'https://example.com');
