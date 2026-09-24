import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';

const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: { get: () => null }, text: async () => body });
function setup(fetchImpl = async () => response('{}', 400), options = {}) {
  const { url = 'https://ui.example.test/#diagnostics', configureWindow, ...appOptions } = options;
  const dom = new JSDOM(markup, { url });
  configureWindow?.(dom.window);
  const app = createApp({ document: dom.window.document, window: dom.window, fetchImpl, ...appOptions });
  return { dom, app, document: dom.window.document };
}
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); }
function labelRows(count, offset = 0) {
  return Array.from({ length: count }, (_, index) => {
    const value = index + offset;
    return { ip: `10.${Math.floor(value / 65536)}.${Math.floor(value / 256) % 256}.${value % 256}`, label: `node-${value}`, note: `note-${value}` };
  });
}
function drain(queue) { while (queue.length) queue.shift()(); }

test('label file imports preserve every existing key and account only for incoming omissions', async t => {
  const key = 'checknetwork.ip-labels.v1';
  const low = { ip: '1.1.1.1', label: 'new low', note: '' };
  const high = { ip: '192.0.2.1', label: 'new high', note: '' };
  const update = { ip: '10.0.1.243', label: 'updated existing', note: 'updated note' };
  const cases = [
    { name: 'low-sorting new key at capacity', count: 500, incoming: [low], accepted: [], omitted: 1 },
    { name: 'high-sorting new key at capacity', count: 500, incoming: [high], accepted: [], omitted: 1 },
    { name: 'existing key update at capacity', count: 500, incoming: [update], accepted: [update], omitted: 0 },
    { name: 'batch crossing capacity', count: 499, incoming: [high, low], accepted: [low], omitted: 1 },
    { name: 'duplicates and invalid rows', count: 500, incoming: [low, update, { ...update, label: 'last wins' }, { ip: 'not-an-ip', label: 'invalid' }], accepted: [{ ...update, label: 'last wins' }], omitted: 2, invalid: 1 },
    { name: 'existing update after 500 new keys', count: 500, incoming: [...labelRows(500).map(row => ({ ...row, ip: row.ip.replace(/^10\./, '1.') })), update], accepted: [update], omitted: 500 }
  ];
  for (const scenario of cases) await t.test(scenario.name, async () => {
    const existing = labelRows(scenario.count); const queue = [];
    const { dom, app, document } = setup(undefined, {
      url: 'https://labels.example/#ip-labels',
      scheduleLabelRender(callback) { queue.push(callback); return callback; },
      configureWindow(win) { win.localStorage.setItem(key, JSON.stringify(existing)); }
    });
    try {
      drain(queue);
      const text = JSON.stringify(scenario.incoming);
      const file = new dom.window.File([text], 'labels.json', { type: 'application/json' });
      // jsdom File lacks Blob.text; the native input/change and app parser remain real.
      file.text = async () => text;
      const input = document.querySelector('#ip-label-import');
      Object.defineProperty(input, 'files', { configurable: true, value: [file] });
      input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
      await flush(); drain(queue);
      const persisted = JSON.parse(dom.window.localStorage.getItem(key));
      const actual = new Map(persisted.map(row => [row.ip, row]));
      const expected = new Map(existing.map(row => [row.ip, row]));
      for (const row of scenario.accepted) expected.set(row.ip, row);
      assert.deepEqual(actual, expected, 'the entire persisted keyset and values must match, not just its size');
      assert.equal(document.querySelector('#ip-label-message').textContent,
        `${scenario.accepted.length}개 매핑을 가져왔습니다. ${scenario.invalid || 0}개 행은 유효하지 않아 제외했고 ${scenario.omitted}개 행은 한도를 초과하거나 중복되어 생략했습니다.`);
    } finally { app.destroy(); dom.window.close(); }
  });
});

test('label startup does not consume hidden DOM needed for 20 diagnostic targets', () => {
  const queue = [];
  const { dom, app, document } = setup(undefined, {
    scheduleLabelRender(callback) { queue.push(callback); return callback; },
    configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(500))); }
  });
  try {
    drain(queue); // Reproduce the real browser's completed startup microtasks.
    for (let index = 4; index < 20; index++) document.querySelector('#add-target').click();
    assert.equal(document.querySelector('#targets').children.length, 20, 'hidden labels must not block supported target admission');
    assert.equal(document.querySelector('#ip-label-rows').childElementCount, 0);
    document.querySelector('[data-view-link="ip-labels"]').click(); drain(queue);
    assert.equal(document.querySelector('#ip-labels-view').dataset.state, 'ready');
    assert.ok(document.querySelectorAll('#ip-label-rows tr[data-ip]').length > 0);
    assert.ok(document.querySelectorAll('*').length <= 1200);
  } finally { app.destroy(); dom.window.close(); }
});

test('destroy releases complete and partial label DOM before cross-view recreation', async t => {
  for (const view of ['diagnostics', 'topology', 'geo-map']) for (const complete of [false, true]) {
    await t.test(`${view} after ${complete ? 'complete' : 'partial'} render`, () => {
      const queue = [];
      const { dom, app: first, document } = setup(undefined, {
        url: 'https://labels.example/#ip-labels',
        scheduleLabelRender(callback) { queue.push(callback); return callback; },
        configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(500))); }
      });
      let second;
      try {
        if (complete) drain(queue);
        else while (!document.querySelector('#ip-label-rows').childElementCount) queue.shift()();
        assert.ok(document.querySelector('#ip-label-rows').childElementCount > 0);
        first.destroy();
        assert.equal(document.querySelector('#ip-label-rows').childElementCount, 0);
        assert.equal(document.querySelector('#ip-labels-view').getAttribute('aria-busy'), 'false');
        dom.window.history.replaceState(null, '', `#${view}`);
        second = createApp({ document, window: dom.window, scheduleLabelRender(callback) { queue.push(callback); return callback; } });
        drain(queue); // Includes stale callbacks from the predecessor.
        assert.equal(document.querySelector('#ip-label-rows').childElementCount, 0);
        document.querySelector('[data-view-link="diagnostics"]').click();
        for (let index = 4; index < 20; index++) document.querySelector('#add-target').click();
        assert.equal(document.querySelector('#targets').children.length, 20);
        assert.ok(document.querySelectorAll('*').length <= 1200);
      } finally { first.destroy(); second?.destroy(); dom.window.close(); }
    });
  }
});

test('destroy never removes label DOM claimed by a successor', () => {
  const queue = [];
  const { dom, app: first, document } = setup(undefined, {
    url: 'https://labels.example/#ip-labels',
    scheduleLabelRender(callback) { queue.push(callback); return callback; },
    configureWindow(win) { win.localStorage.setItem('checknetwork.ip-labels.v1', JSON.stringify(labelRows(500))); }
  });
  let second;
  try {
    drain(queue);
    second = createApp({ document, window: dom.window, scheduleLabelRender(callback) { queue.push(callback); return callback; } });
    drain(queue);
    const rows = [...document.querySelector('#ip-label-rows').children];
    assert.equal(rows.length, 100);
    first.destroy(); drain(queue);
    assert.deepEqual([...document.querySelector('#ip-label-rows').children], rows);
    second.destroy();
    assert.equal(document.querySelector('#ip-label-rows').childElementCount, 0);
  } finally { first.destroy(); second?.destroy(); dom.window.close(); }
});

test('label pagination remains reachable and editable across supported target and label counts', async t => {
  for (const targets of [1, 4, 20]) for (const count of [0, 100, 500]) {
    await t.test(`${targets} targets / ${count} labels`, () => {
      const queue = []; const chunks = []; let peak = 0;
      const key = 'checknetwork.ip-labels.v1';
      const seeded = labelRows(count);
      const { dom, app, document } = setup(undefined, {
        scheduleLabelRender(callback) { queue.push(callback); return callback; },
        configureWindow(win) {
          win.localStorage.setItem(key, JSON.stringify(seeded));
          const append = win.Element.prototype.append;
          win.Element.prototype.append = function (...nodes) {
            if (this.id === 'ip-label-rows') {
              const cost = nodes.reduce((sum, node) => sum + (node.nodeType === 1 ? 1 : 0) + (node.querySelectorAll?.('*').length || 0), 0);
              chunks.push(cost);
              assert.ok(cost <= 100, `chunk ${cost} exceeds 100 elements`);
              assert.ok(win.document.querySelectorAll('*').length + cost <= 1200);
            }
            return append.apply(this, nodes);
          };
        }
      });
      const observe = () => {
        peak = Math.max(peak, document.querySelectorAll('*').length);
        assert.ok(peak <= 1200, `document peak ${peak}`);
      };
      const drainObserved = () => { while (queue.length) { queue.shift()(); observe(); } };
      const pageRows = () => [...document.querySelectorAll('#ip-label-rows tr[data-ip]')];
      const ready = () => {
        drainObserved(); observe();
        assert.equal(document.querySelector('#ip-labels-view').dataset.state, 'ready');
        assert.equal(document.querySelector('#ip-labels-view').getAttribute('aria-busy'), 'false');
        assert.ok(pageRows().length <= 100);
        if (count) assert.ok(pageRows().length > 0, 'a supported nonempty table must remain usable');
      };
      try {
        drainObserved(); // Complete initial microtasks before configuring the form.
        while (document.querySelector('#targets').children.length > targets) document.querySelector('#targets .icon-button').click();
        while (document.querySelector('#targets').children.length < targets) document.querySelector('#add-target').click();
        assert.equal(document.querySelector('#targets').children.length, targets);
        document.querySelector('[data-view-link="ip-labels"]').click(); ready();
        const pages = []; const statuses = [];
        for (let guard = 0; ; guard++) {
          assert.ok(guard < 500, 'pagination must terminate');
          pages.push(pageRows().map(row => row.dataset.ip));
          statuses.push(document.querySelector('#ip-label-page-status').textContent);
          if (document.querySelector('#ip-label-next').disabled) break;
          const next = document.querySelector('#ip-label-next'); next.focus(); next.click(); ready();
          assert.equal(document.activeElement, next);
        }
        assert.deepEqual(pages.flat(), seeded.map(row => row.ip), 'all labels reached exactly once in deterministic order');
        for (let index = pages.length - 1; index >= 0; index--) {
          assert.deepEqual(pageRows().map(row => row.dataset.ip), pages[index]);
          assert.equal(document.querySelector('#ip-label-page-status').textContent, statuses[index]);
          if (index) { document.querySelector('#ip-label-prev').click(); ready(); }
        }
        assert.equal(document.querySelector('#ip-label-prev').disabled, true);
        // Edit and delete on the last page, including a possibly short page.
        while (!document.querySelector('#ip-label-next').disabled) { document.querySelector('#ip-label-next').click(); ready(); }
        if (count) {
          const row = pageRows().at(-1); const ip = row.dataset.ip;
          for (const field of ['label', 'note']) {
            const input = row.querySelector(`[data-field="${field}"]`); input.value = `edited ${field}`;
            input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
          }
          const edited = JSON.parse(dom.window.localStorage.getItem(key)).find(value => value.ip === ip);
          assert.deepEqual(edited, { ip, label: 'edited label', note: 'edited note' });
          row.querySelector('.mapping-delete').click(); ready();
          assert.deepEqual(JSON.parse(dom.window.localStorage.getItem(key)), seeded.filter(value => value.ip !== ip));
          assert.equal(document.activeElement?.classList.contains('mapping-delete'), true);
        } else {
          assert.match(document.querySelector('#ip-label-rows').textContent, /등록된 IP 라벨이 없습니다/);
          assert.equal(document.querySelector('#ip-label-next').disabled, true);
        }
        t.diagnostic(`${targets} targets / ${count} labels: pages=${pages.length}, peak=${peak}, maxChunk=${Math.max(...chunks)}`);
      } finally { app.destroy(); dom.window.close(); }
    });
  }
});

test('topology textarea ignores blank separators before real-target admission', async t => {
  const twenty = Array.from({ length: 20 }, (_, index) => `target-${index}.example`);
  const cases = [
    { name: 'terminal LF', text: 'example.com\n', addresses: ['example.com'] },
    { name: 'terminal CRLF', text: 'example.com\r\n', addresses: ['example.com'] },
    { name: 'whitespace blank lines', text: '\n  \t\n example.com \n \t\n', addresses: ['example.com'] },
    { name: 'commas and newlines', text: ', example.com,\r\n, other.example,,\n', addresses: ['example.com', 'other.example'] },
    { name: 'deduplicate real addresses', text: 'example.com,\nexample.com,\n', addresses: ['example.com'] },
    { name: '20 real targets with many blanks', text: `${twenty.join(',\r\n, \n')}\n`, addresses: twenty },
    { name: '21 real targets', text: `${[...twenty, 'extra.example'].join('\n')}\n`, addresses: null },
    { name: '21 duplicate real targets still exceed admission', text: `${Array(21).fill('example.com').join(',\n')},`, addresses: null },
    { name: 'only blank separators', text: ',\r\n, \t\n,', addresses: null },
    { name: 'empty input', text: '', addresses: null },
    { name: 'oversized address is not weakened', text: `${'x'.repeat(4097)}\n`, addresses: null }
  ];
  for (const scenario of cases) await t.test(scenario.name, async () => {
    const posts = [];
    const { dom, app, document } = setup(async (_url, init) => {
      posts.push(JSON.parse(init.body));
      return response('{}', 400);
    }, { url: 'https://topology.example/#topology' });
    try {
      const textarea = document.querySelector('#topology-targets'); textarea.value = scenario.text;
      textarea.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
      // Native submit button exercises the real form handler and request builder.
      document.querySelector('#run-topology').click(); await flush();
      assert.equal(posts.length, scenario.addresses ? 1 : 0, 'only admitted real targets may cause a POST');
      if (scenario.addresses) {
        assert.deepEqual(posts[0].targets.map(target => target.address), scenario.addresses);
        assert.ok(posts[0].targets.every(target => target.kind === 'traceroute'));
        assert.equal(posts[0].topology_mode, 'compact');
      } else if (scenario.text) {
        assert.match(document.querySelector('#topology-error').textContent, /invalid topology address|exceeds limit 20/);
      }
    } finally { app.destroy(); dom.window.close(); }
  });
});
