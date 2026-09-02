import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

import { TopologyRenderCoordinator } from './topology-renderer.js';

function harness({ owns = () => true, html = '' } = {}) {
  const dom = new JSDOM(`<!doctype html><html><body>${html}<main id="root"></main><p id="status"></p></body></html>`, { pretendToBeVisual: true });
  const queue = [];
  const cancelled = new Set();
  let nextID = 1;
  const schedule = callback => { const id = nextID++; queue.push({ id, callback }); return id; };
  const cancelScheduled = id => cancelled.add(id);
  const runNext = () => {
    const job = queue.shift();
    if (!job) return false;
    if (!cancelled.has(job.id)) job.callback();
    return true;
  };
  const runAll = () => { while (runNext()); };
  const states = [];
  const coordinator = new TopologyRenderCoordinator({ document: dom.window.document, schedule, cancelScheduled, ownsRequest: owns, onState: state => states.push(state) });
  return { dom, document: dom.window.document, root: dom.window.document.querySelector('#root'), status: dom.window.document.querySelector('#status'), queue, schedule, cancelScheduled, runNext, runAll, states, coordinator };
}

function topologyModel(size = 220) {
  const nodes = Array.from({ length: size }, (_, index) => ({ id: `n${index}`, kind: 'ip', address: `192.0.2.${index}`, status: 'healthy', hop_min: index, hop_max: index, observations: 1 }));
  return { nodes, links: [], routes: [], serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] } };
}

test('scheduler schedule and cancel failures leave no busy state or keyboard listener', () => {
  const scheduleDOM = new JSDOM('<main id="root"></main><p id="status"></p>');
  const scheduleStates = [];
  const scheduleCoordinator = new TopologyRenderCoordinator({
    document: scheduleDOM.window.document,
    schedule() { throw new Error('schedule failed'); }, cancelScheduled() {}, ownsRequest: () => true,
    onState: state => scheduleStates.push(state)
  });
  const scheduleRoot = scheduleDOM.window.document.querySelector('#root');
  const scheduleStatus = scheduleDOM.window.document.querySelector('#status');
  assert.doesNotThrow(() => scheduleCoordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(1), root: scheduleRoot, status: scheduleStatus }));
  assert.equal(scheduleRoot.getAttribute('aria-busy'), 'false');
  assert.equal(scheduleStatus.textContent, '표시 오류');
  assert.equal(scheduleStates.at(-1).phase, 'error');

  const cancelDOM = new JSDOM('<main id="root"></main><p id="status"></p>');
  const cancelRoot = cancelDOM.window.document.querySelector('#root');
  const cancelStatus = cancelDOM.window.document.querySelector('#status');
  const cancelCoordinator = new TopologyRenderCoordinator({
    document: cancelDOM.window.document, schedule: () => 17,
    cancelScheduled() { throw new Error('cancel failed'); }, ownsRequest: () => true
  });
  cancelCoordinator.start({ ownerId: 'B', inputSignature: 'b', view: 'topology', model: topologyModel(2), root: cancelRoot, status: cancelStatus });
  assert.doesNotThrow(() => cancelCoordinator.cancel('dispose'));
  assert.equal(cancelRoot.hasAttribute('aria-busy'), false);
  const key = new cancelDOM.window.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true });
  cancelRoot.dispatchEvent(key);
  assert.equal(key.defaultPrevented, false);
});

test('topology renders one bounded chunk per callback with deterministic progress', () => {
  const h = harness({ html: '<aside><i></i><i></i></aside>' });
  h.coordinator.start({ ownerId: 'A', inputSignature: 'sig-A', view: 'topology', model: topologyModel(), root: h.root, status: h.status });

  assert.equal(h.status.textContent, '표시 0/220');
  assert.equal(h.root.getAttribute('aria-busy'), 'true');
  assert.equal(h.root.hasAttribute('aria-live'), false);
  assert.equal(h.status.getAttribute('role'), 'status');
  assert.equal(h.status.getAttribute('aria-live'), 'polite');

  h.runNext();
  assert.equal(h.root.childElementCount, 100);
  assert.equal(h.status.textContent, '표시 100/220');
  h.runNext();
  assert.equal(h.root.childElementCount, 200);
  assert.equal(h.status.textContent, '표시 200/220');
  h.runNext();
  assert.equal(h.root.childElementCount, 220);
  assert.match(h.status.textContent, /표시 완료/);
  assert.equal(h.root.getAttribute('aria-busy'), 'false');
  assert.ok(h.document.getElementsByTagName('*').length <= 1200);
  assert.deepEqual(h.states.map(state => state.phase), ['rendering', 'ready']);
});

test('replacement removes A and stale callbacks cannot append, announce, or clear B busy', () => {
  const focused = [];
  const h = harness();
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(150), root: h.root, status: h.status, focusTarget: reason => focused.push(reason) });
  h.runNext();
  assert.equal(h.root.childElementCount, 100);
  const staleSecondChunk = h.queue[0].callback;

  h.coordinator.start({ ownerId: 'B', inputSignature: 'b', view: 'topology', model: topologyModel(1), root: h.root, status: h.status });
  assert.equal(h.root.childElementCount, 0, 'replacement clears A-owned nodes');
  assert.deepEqual(focused, ['replaced']);
  staleSecondChunk();
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.root.getAttribute('aria-busy'), 'true');
  assert.equal(h.status.textContent, '표시 0/1');
  h.runAll();
  assert.equal(h.root.childElementCount, 1);
  assert.equal(h.root.getAttribute('aria-busy'), 'false');
  assert.match(h.status.textContent, /표시 완료/);
});

test('nonrenderable plans announce a concise limitation without partial DOM', () => {
  const h = harness({ html: '<section id="crowd"></section>' });
  const crowd = h.document.querySelector('#crowd');
  crowd.append(...Array.from({ length: 1100 }, () => h.document.createElement('i')));
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(1), root: h.root, status: h.status });
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.queue.length, 0);
  assert.equal(h.root.getAttribute('aria-busy'), 'false');
  assert.equal(h.status.textContent, '표시할 수 없음: 문서 요소 한도');
  assert.equal(h.states.at(-1).phase, 'ready');
  assert.equal(h.states.at(-1).renderable, false);
});

test('hidden views are disposed before the existing document count is planned', () => {
  const h = harness({ html: '<section id="hidden"></section>' });
  const hidden = h.document.querySelector('#hidden');
  hidden.append(...Array.from({ length: 1100 }, () => h.document.createElement('i')));
  let disposed = 0;
  h.coordinator.start({
    ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(1), root: h.root, status: h.status,
    workspace: { disposeHiddenViews() { disposed++; hidden.remove(); } }
  });
  assert.equal(disposed, 1);
  assert.equal(h.status.textContent, '표시 0/1');
  h.runAll();
  assert.equal(h.root.childElementCount, 1);
});

test('owner changes prevent materialization and commit', () => {
  let owner = true;
  const h = harness({ owns: () => owner });
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(2), root: h.root, status: h.status });
  owner = false;
  h.runAll();
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.status.textContent, '표시 0/2');
  assert.equal(h.states.some(state => state.phase === 'ready'), false);
});

test('commit failure clears partial output and reports an error', () => {
  const h = harness();
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(1), root: h.root, status: h.status });
  const blocker = h.document.createElement('section');
  blocker.append(...Array.from({ length: 1200 }, () => h.document.createElement('i')));
  h.document.body.append(blocker);
  h.runAll();
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.root.getAttribute('aria-busy'), 'false');
  assert.equal(h.status.textContent, '표시 오류');
  assert.equal(h.states.at(-1).phase, 'error');
});

test('a stale coordinator cannot clear a successor root or busy state', () => {
  const h = harness();
  const successorStates = [];
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(120), root: h.root, status: h.status });
  h.runNext();
  const successor = new TopologyRenderCoordinator({
    document: h.document, schedule: h.schedule, cancelScheduled: h.cancelScheduled,
    ownsRequest: (ownerId, signature) => ownerId === 'B' && signature === 'b', onState: state => successorStates.push(state)
  });
  successor.start({ ownerId: 'B', inputSignature: 'b', view: 'topology', model: topologyModel(2), root: h.root, status: h.status });
  h.coordinator.cancel('user');
  assert.equal(h.states.at(-1).phase, 'rendering', 'stale coordinator does not announce cancellation');
  assert.equal(h.root.getAttribute('aria-busy'), 'true');
  assert.equal(h.status.textContent, '표시 0/2');
  h.runAll();
  assert.equal(h.root.childElementCount, 2);
  assert.equal(h.root.getAttribute('aria-busy'), 'false');
  assert.equal(successorStates.at(-1).phase, 'ready');
});

test('topology uses safe one-element items and roving arrow-key focus', () => {
  const h = harness();
  const attack = '<img src=x onerror="globalThis.pwned=1">';
  const model = {
    nodes: [
      { id: 'local', kind: 'local', address: 'local', status: 'healthy' },
      { id: 'editable', kind: 'ip', address: attack, status: 'degraded' },
      { id: 'second', kind: 'ip', address: '192.0.2.2', status: 'healthy' },
      { id: 'plain', kind: 'unknown', address: '', status: 'unknown' }
    ],
    links: [{ from: 'local', to: 'editable', status: 'healthy' }, { from: 'editable', to: 'second', status: 'degraded' }],
    routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['local', 'editable', 'second'] }],
    serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] }
  };
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model, root: h.root, status: h.status });
  h.runAll();

  assert.equal(h.root.querySelectorAll('.topology-node').length, 3);
  assert.equal(h.root.querySelectorAll('.topology-link').length, 2);
  assert.equal(h.root.querySelectorAll('.topology-route').length, 1);
  assert.equal(h.root.querySelector('.topology-link').dataset.from, 'local');
  assert.equal(h.root.querySelector('.topology-link').dataset.to, 'editable');
  assert.equal(h.root.querySelector('.topology-route').dataset.complete, 'true');
  assert.equal(h.root.querySelector('.topology-route').dataset.nodeIds, 'local editable second');
  assert.equal(h.root.querySelectorAll('img,script').length, 0);
  assert.match(h.root.textContent, /<img src=x/);
  assert.equal(globalThis.pwned, undefined);
  assert.equal(h.root.querySelector('[data-node-id="local"]').tagName, 'DIV');
  const buttons = [...h.root.querySelectorAll('button.topology-node')];
  assert.equal(buttons.length, 2);
  assert.deepEqual(buttons.map(button => button.tabIndex), [0, -1]);
  buttons[0].focus();
  buttons[0].dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
  assert.strictEqual(h.document.activeElement, buttons[1]);
  assert.deepEqual(buttons.map(button => button.tabIndex), [-1, 0]);
  buttons[1].dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true }));
  assert.strictEqual(h.document.activeElement, buttons[0]);
});

test('bounded topology elements expose rich semantics and reorder without adding DOM', () => {
  const h = harness();
  const model = {
    nodes: [
      { id: 'n1', kind: 'ip', address: '203.0.113.8', status: 'degraded', hop_min: 2, hop_max: 4, observations: 7,
        display_label: 'Seoul edge', display_note: 'Primary transit',
        latency_ms_avg: 12.5, public_ip: true,
        geolocation: { city: 'Seoul', region: 'Seoul', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 },
        asn: { number: 64500, organization: 'Example Transit' } },
      { id: 'n2', kind: 'ip', address: '198.51.100.9', status: 'healthy', hop_min: 5, hop_max: 5, observations: 2, latency_ms_avg: Number.POSITIVE_INFINITY }
    ],
    links: [{ from: 'n1', to: 'n2', status: 'degraded', observations: 1_000_000 }],
    routes: [{ result_index: 7, attempt: 2, address: 'target.example', status: 'degraded', reached: false, complete: false, node_ids: ['n1', 'n2'] }],
    serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] }
  };
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model, root: h.root, status: h.status });
  h.runAll();

  const first = h.root.querySelector('[data-node-id="n1"]');
  const second = h.root.querySelector('[data-node-id="n2"]');
  assert.equal(first.childElementCount, 0);
  for (const value of ['Seoul edge', '203.0.113.8', 'Primary transit', 'degraded', 'HOP 2–4', '7 observations', '12.5 ms', 'public IP', 'Seoul', 'KR', 'AS64500', 'Example Transit']) {
    assert.match(first.dataset.detail, new RegExp(value));
    assert.match(first.getAttribute('aria-label'), new RegExp(value));
    assert.match(first.title, new RegExp(value));
    assert.match(first.textContent, new RegExp(value));
  }
  assert.doesNotMatch(second.dataset.detail, /Infinity|latency/i);
  const link = h.root.querySelector('.topology-link');
  assert.match(link.dataset.detail, /1000000 observations/);
  assert.equal(link.style.getPropertyValue('--observation-width'), '8px');
  const route = h.root.querySelector('.topology-route');
  assert.match(route.dataset.detail, /target\.example/);
  assert.match(route.dataset.detail, /degraded/);
  assert.match(route.dataset.detail, /unreached/);
  assert.match(route.dataset.detail, /incomplete/);
  assert.equal(route.style.getPropertyValue('--route-color').length > 0, true);

  const beforeCount = h.document.getElementsByTagName('*').length;
  assert.equal(first.draggable, true, 'native dragging is enabled only in ready output');
  first.focus();
  first.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'ArrowRight', altKey: true, bubbles: true, cancelable: true }));
  assert.deepEqual([...h.root.querySelectorAll('.topology-node')].map(node => node.dataset.nodeId), ['n2', 'n1']);
  assert.strictEqual(h.document.activeElement, first);
  assert.equal(h.document.getElementsByTagName('*').length, beforeCount);

  first.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true }));
  assert.strictEqual(h.document.activeElement, second, 'regular arrows retain roving navigation');
});

test('Geo mounts canvas plus an accessible list, draws semantic data separately, and disposes resources', () => {
  const h = harness();
  const nodes = Array.from({ length: 130 }, (_, index) => ({
    id: `g${index}`, kind: 'ip', address: `198.51.100.${index}`, status: 'healthy', public_ip: true,
    geolocation: { latitude: 30 + index / 1000, longitude: 120 + index / 1000 }
  }));
  const links = nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: 'healthy' }));
  const model = {
    nodes, links,
    routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: nodes.map(node => node.id) }],
    serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] }
  };
  let drawCalls = 0;
  let cleaned = 0;
  let semantic;
  h.coordinator.start({
    ownerId: 'A', inputSignature: 'a', view: 'geo', model, root: h.root, status: h.status,
    workspace: { drawGeo(value) { drawCalls++; semantic = value.geo; assert.ok(value.canvas.isConnected); return () => cleaned++; } }
  });
  h.runNext();
  assert.equal(drawCalls, 0, 'drawing has its own callback after mount');
  h.runNext();
  assert.equal(drawCalls, 1);
  h.runAll();
  assert.equal(h.root.querySelectorAll('canvas').length, 1);
  assert.equal(h.root.querySelectorAll('ul').length, 1);
  assert.equal(h.root.querySelectorAll('ul > li').length, 100);
  assert.equal(h.root.querySelectorAll('[data-segment], [data-arrow]').length, 0);
  assert.ok(semantic.markers.length <= 500);
  assert.ok(semantic.segments.length <= 1000);
  assert.equal(semantic.segments.length, semantic.arrows.length);
  h.coordinator.dispose();
  assert.equal(cleaned, 1);
  assert.equal(h.root.childElementCount, 0);
});

test('labels materialize trusted shell and exact-cost rows with inert text', () => {
  const h = harness();
  const attack = '<svg onload="globalThis.labelsPwned=1">';
  const labelRecords = Array.from({ length: 25 }, (_, index) => ({ node_id: `n${index}`, label: index ? `label-${index}` : attack, note: attack }));
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'labels', model: topologyModel(30), root: h.root, status: h.status, labelRecords });
  h.runAll();
  assert.equal(h.root.querySelectorAll('table.topology-labels').length, 1);
  assert.equal(h.root.querySelectorAll('tbody > tr').length, 25);
  assert.equal(h.root.querySelectorAll('tbody > tr > td').length, 75);
  assert.equal(h.root.querySelectorAll('tbody > tr > button').length, 25);
  assert.equal(h.root.querySelectorAll('svg,script').length, 0);
  assert.equal(globalThis.labelsPwned, undefined);
  assert.match(h.root.textContent, /<svg onload/);
  assert.ok(h.document.getElementsByTagName('*').length <= 1200);
});

test('finish reports server and view truncation concisely', () => {
  const h = harness();
  const model = topologyModel(1);
  model.serverTruncation = { truncated: true, reasons: ['response_size'] };
  model.adapterTruncation = { truncated: true, reasons: ['node_limit'] };
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model, root: h.root, status: h.status });
  h.runAll();
  assert.equal(h.status.textContent, '표시 완료 · 서버 제한 · 변환 제한');
});
