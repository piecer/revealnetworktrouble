import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';
import { TopologyRenderCoordinator } from './topology-renderer.js';

const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');

test('actual unavailable HTTP report is not confused with deselected targets', async () => {
  const body = await readFile(new URL('../testdata/topology-unavailable-http-report.json', import.meta.url), 'utf8');
  const dom = new JSDOM(markup, { url: 'https://ui.example.test/#topology' });
  const document = dom.window.document;
  const app = createApp({ document, window: dom.window, fetchImpl: async () => ({ ok: true, status: 200, headers: { get: () => null }, text: async () => body }) });
  try {
    await app.start('topology');
    assert.equal(app.getState().topology.phase, 'ready');
    assert.equal(document.querySelectorAll('[data-target-index]:checked').length, 1);
    for (const selector of ['#topology-result', '#topology-render-status', '#topology-state-message']) {
      const text = document.querySelector(selector).textContent;
      assert.match(text, /Traceroute.*사용할 수 없/);
      assert.doesNotMatch(text, /선택.*0개/);
    }
    assert.equal(document.querySelector('#download-topology').disabled, false);
    document.querySelector('[data-filter-action="none"]').click();
    assert.match(document.querySelector('#topology-state-message').textContent, /선택.*0개/);
    document.querySelector('[data-filter-action="all"]').click();
    assert.match(document.querySelector('#topology-state-message').textContent, /Traceroute.*사용할 수 없/);
    assert.ok(document.querySelectorAll('*').length <= 1200);
  } finally { app.destroy(); dom.window.close(); }
});

test('empty renderer separates selection, missing observations, execution, and projection limits', () => {
  const cases = [
    [{ selectedCount: 0, errorCodes: ['traceroute_unavailable'] }, false, /선택.*0개/],
    [{ selectedCount: 1, errorCodes: ['traceroute_unavailable'] }, false, /Traceroute.*사용할 수 없/],
    [{ selectedCount: 1, errorCodes: ['traceroute_failed'] }, false, /실행.*완료되지 않/],
    [{ selectedCount: 1, errorCodes: ['timeout'] }, false, /시간.*초과/],
    [{ selectedCount: 1, errorCodes: ['cancelled'] }, false, /취소/],
    [{ selectedCount: 1, errorCodes: [] }, false, /관측.*경로.*없/],
    [{ selectedCount: 1, errorCodes: [] }, true, /제한.*경로/],
    [{ selectedCount: 1, errorCodes: ['<img src=x onerror=alert(1)>'] }, false, /관측.*경로.*없/],
    [undefined, false, /관측.*경로.*없/],
  ];
  for (const [observationState, truncated, expected] of cases) {
    const dom = new JSDOM('<main></main><p></p>');
    const document = dom.window.document, root = document.querySelector('main'), status = document.querySelector('p');
    const states = []; let scheduled = 0;
    const coordinator = new TopologyRenderCoordinator({ document, ownsRequest: () => true, schedule: () => { scheduled++; }, cancelScheduled() {}, onState: state => states.push(state) });
    const model = { nodes: [], links: [], routes: [], serverTruncation: { truncated, reasons: truncated ? ['response_size'] : [] }, adapterTruncation: { truncated: false, reasons: [] } };
    coordinator.start({ ownerId: 'owner', inputSignature: 'input', view: 'topology', model, root, status, observationState });
    assert.match(root.textContent, expected);
    assert.match(status.textContent, expected);
    assert.match(states.at(-1).emptyMessage, expected);
    assert.equal(root.querySelector('img'), null);
    assert.equal(scheduled, 0);
    coordinator.dispose(); dom.window.close();
  }
});
