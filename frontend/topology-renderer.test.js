import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

import { normalizeReport } from './state.js';
import { readFileSync } from 'node:fs';
import { topologyModelFromReport, planTopologyDOM } from './topology-model.js';
import { drawTopologyCanvas, TopologyRenderCoordinator } from './topology-renderer.js';

function harness({ owns = () => true, html = '', context = fakeContext() } = {}) {
  const dom = new JSDOM(`<!doctype html><html><body>${html}<main id="root"></main><p id="status"></p></body></html>`, { pretendToBeVisual: true });
  dom.window.CanvasRenderingContext2D = function CanvasRenderingContext2D() {};
  dom.window.HTMLCanvasElement.prototype.getContext = () => context;
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
  return { dom, document: dom.window.document, root: dom.window.document.querySelector('#root'), status: dom.window.document.querySelector('#status'), context, queue, schedule, cancelScheduled, runNext, runAll, states, coordinator };
}

function topologyModel(size = 220) {
  const nodes = Array.from({ length: size }, (_, index) => ({ id: `n${index}`, kind: 'ip', address: `192.0.2.${index}`, status: 'healthy', hop_min: index, hop_max: index, observations: 1 }));
  return { nodes, links: [], routes: [], serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] } };
}

function fakeContext() {
  const calls = [];
  const state = { textAlign: 'right', textBaseline: 'alphabetic' };
  const stack = [];
  const context = { calls, state };
  for (const name of ['setTransform', 'clearRect', 'fillRect', 'beginPath', 'moveTo', 'lineTo', 'stroke', 'closePath', 'fill', 'arc']) {
    context[name] = (...args) => calls.push([name, ...args]);
  }
  context.save = () => { stack.push({ ...state }); calls.push(['save']); };
  context.restore = () => { Object.assign(state, stack.pop()); calls.push(['restore']); };
  context.fillText = (...args) => calls.push(['fillText', ...args, { textAlign: state.textAlign, textBaseline: state.textBaseline }]);
  for (const name of ['fillStyle', 'strokeStyle', 'lineWidth', 'font', 'textAlign', 'textBaseline']) {
    Object.defineProperty(context, name, {
      set(value) { state[name] = value; calls.push([name, value]); },
      get() { return state[name]; }
    });
  }
  return context;
}

test('producer private contexts draw exactly two lavender dashed halos and reset dash state in both modes', () => {
  const model = topologyModelFromReport(normalizeReport(JSON.parse(readFileSync(new URL('../testdata/compact-asn-context-report.json', import.meta.url), 'utf8'))));
  const plan = planTopologyDOM(model);
  for (const mode of ['2d','3d']) {
    const context = fakeContext(); context.setLineDash = dash => context.calls.push(['dash', dash]);
    const projection = drawTopologyCanvas(context, model, {mode, presentation:plan.presentation});
    const halos = context.calls.flatMap((call,i) => call[0] === 'strokeStyle' && call[1] === '#c4b5fd' ? [context.calls.slice(i,i+7)] : []);
    assert.equal(halos.length,2);
    for (const halo of halos) {
      assert.deepEqual(halo.map(c=>c[0]), ['strokeStyle','lineWidth','dash','beginPath','arc','stroke','dash']);
      assert.deepEqual(halo[2],['dash',[3,3]]); assert.deepEqual(halo[6],['dash',[]]);
      assert.ok(projection.nodes.some(n=>n.asn_context && n.screen.x===halo[4][1] && n.screen.y===halo[4][2] && n.screen.radius+6===halo[4][3]));
    }
    assert.equal(context.calls.filter(c=>c[0]==='fillText' && /AS15169.*추정/.test(c[1])).length,2);
  }
});

test('hidden return to the same responsive node has a visible presentation curve', () => {
  const h = harness(); const model = topologyModel(2); model.showUnresponsive = false;
  Object.assign(model.nodes[1], { kind: 'unknown', address: '', status: 'unknown' });
  model.links = [{from:'n0',to:'n1',status:'unknown'},{from:'n1',to:'n0',status:'unknown'}];
  model.routes = [{result_index:0,attempt:1,status:'healthy',complete:true,node_ids:['n0','n1','n0']}];
  h.context.quadraticCurveTo = (...args) => h.context.calls.push(['curve',...args]);
  h.coordinator.start({ownerId:1,inputSignature:'return',view:'topology',model,root:h.root,status:h.status}); h.runAll();
  const curve = h.context.calls.find(c => c[0] === 'curve');
  assert.ok(Math.hypot(curve[1]-curve[3],curve[2]-curve[4]) >= 20);
});

test('folded and hidden spans mount separately, draw dashed and follow individual drag in both modes', () => {
  for (const mode of ['2d', '3d']) for (const showUnresponsive of [true, false]) {
    const context = fakeContext(); context.setLineDash = (...args) => context.calls.push(['setLineDash', ...args]);
    const h = harness({ context, html: '<div id="tip" hidden></div>' });
    const model = topologyModel(5); model.showUnresponsive = showUnresponsive;
    for (const i of [1, 2]) Object.assign(model.nodes[i], { kind: 'unknown', address: '', status: 'unknown' });
    model.links = model.nodes.slice(1).map((n, i) => ({ from: model.nodes[i].id, to: n.id, status: 'healthy' }));
    model.routes = [{ result_index: 0, attempt: 1, status: 'healthy', complete: true, node_ids: model.nodes.map(n => n.id) }];
    h.coordinator.start({ ownerId: 1, inputSignature: 'gap', view: 'topology', mode, model, root: h.root, status: h.status, workspace: { tooltip: h.document.querySelector('#tip') } }); h.runAll();
    assert.equal(h.root.querySelector('canvas').dataset.drawState, 'rendered');
    assert.equal(h.root.querySelectorAll('.topology-node').length, showUnresponsive ? 4 : 3);
    assert.equal(h.root.querySelectorAll('.topology-connector').length, showUnresponsive ? 2 : 1);
    assert.ok(context.calls.some(c => c[0] === 'setLineDash' && c[1].length));
    const before = structuredClone(h.coordinator.current.projection);
    const node = before.nodes.find(n => showUnresponsive ? n.kind === 'unknown-group' : n.id === 'n0');
    const canvas = h.root.querySelector('canvas');
    const event = (type, x, y) => canvas.dispatchEvent(new h.dom.window.MouseEvent(type, { clientX: x, clientY: y, button: 0 }));
    event('pointermove', node.screen.x, node.screen.y);
    assert.match(h.document.querySelector('#tip').textContent, showUnresponsive ? /무응답 2홉 접음/ : /192.0.2.0/);
    event('pointerdown', node.screen.x, node.screen.y); event('pointermove', node.screen.x + 20, node.screen.y + 20); event('pointerup', node.screen.x + 20, node.screen.y + 20);
    const after = h.coordinator.current.projection;
    const attached = before.connectors.find(c => c.from === node.id || c.to === node.id);
    const key = attached.from === node.id ? 'from_screen' : 'to_screen';
    assert.notDeepEqual(after.connectors.find(c => c.id === attached.id)[key], attached[key]);
    assert.equal(h.root.querySelectorAll('button[data-node-id^="presentation:"]').length, 0);
    assert.ok(h.document.getElementsByTagName('*').length <= 1200);
  }
});

test('narrow rich graph labels do not overlap each other', () => {
  for (const count of [4, 500]) for (const mode of ['2d', '3d']) for (const width of [289, 1298]) {
    const context = fakeContext(), model = topologyModel(count);
    if (count === 500) model.nodes.at(-1).display_label = 'Priority alias';
    const facts = JSON.stringify(model), options = { mode, viewport: { width, height: 240 } };
    const projection = drawTopologyCanvas(context, model, options);
    const labels = context.calls.filter(call => call[0] === 'fillText');
    const boxes = labels.map(([, text, x, y, maxWidth, state]) => {
      const width = Math.min(text.length * 7, maxWidth);
      return { left: x - width / 2, right: x + width / 2, top: state.textBaseline === 'bottom' ? y - 14 : y, bottom: state.textBaseline === 'bottom' ? y : y + 14 };
    });
    if (count === 4) assert.equal(labels.length, projection.nodes.length, 'small graph retains every label');
    else {
      assert.ok(labels.length > 0 && labels.length < count, 'crowding omits only visual labels');
      assert.equal(labels[0][1], 'Priority alias', 'saved alias receives first placement');
    }
    for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i], b = boxes[j];
      assert.ok(a.right <= b.left || b.right <= a.left || a.bottom <= b.top || b.bottom <= a.top, 'label rectangles are disjoint');
    }
    const repeat = fakeContext();
    assert.deepEqual(drawTopologyCanvas(repeat, model, options), projection);
    assert.deepEqual(repeat.calls, context.calls, 'bounded placement is deterministic');
    assert.equal(JSON.stringify(model), facts, 'raw graph facts remain unchanged');
  }
});

test('dense graph hit testing chooses the nearest node rather than a later overlapping halo', () => {
  const h = harness({ html: '<div id="tip" hidden></div>' });
  const tooltip = h.document.querySelector('#tip');
  h.coordinator.start({ ownerId: 1, inputSignature: 'dense', view: 'topology', model: topologyModel(60), root: h.root, status: h.status, workspace: { tooltip } }); h.runAll();
  h.root.querySelector('canvas').dispatchEvent(new h.dom.window.MouseEvent('pointermove', { clientX: 32, clientY: 240 }));
  assert.ok(tooltip.textContent.startsWith('192.0.2.0 ·'), tooltip.textContent);
});

test('graph focus and bracket navigation expose inert details; click and Enter activate the existing annotation editor', () => {
  const h = harness({ html: '<div id="tip" role="tooltip" hidden></div>' });
  const model = topologyModel(2);
  model.nodes[0].display_note = '<img src=x onerror=alert(1)>';
  const tooltip = h.document.querySelector('#tip');
  h.coordinator.start({ ownerId: 1, inputSignature: 'edit', view: 'topology', model, root: h.root, status: h.status, workspace: { tooltip } }); h.runAll();
  const edited = [];
  h.root.addEventListener('click', event => { if (event.target.matches('button.topology-node')) edited.push(event.target.dataset.labelAddress); });
  const canvas = h.root.querySelector('canvas');
  canvas.focus();
  assert.equal(tooltip.hidden, false);
  assert.ok(tooltip.textContent.includes(model.nodes[0].display_note));
  assert.equal(tooltip.childElementCount, 0);
  canvas.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  canvas.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: ']', bubbles: true }));
  canvas.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  canvas.dispatchEvent(new h.dom.window.MouseEvent('click', { clientX: 32, clientY: 240, bubbles: true }));
  assert.deepEqual(edited, ['192.0.2.0', '192.0.2.1', '192.0.2.0']);
  canvas.dispatchEvent(new h.dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  assert.equal(tooltip.hidden, true);
});

test('node drag changes only the hit node and attached edge, keeps offsets on redraw, and disposes stale handlers', () => {
  const h = harness({ html: '<div id="tip" role="tooltip" hidden></div>' });
  const model = topologyModel(3);
  model.links = [{ from: 'n0', to: 'n1', status: 'healthy' }];
  const tooltip = h.document.querySelector('#tip');
  const args = { ownerId: 1, inputSignature: 'rich', view: 'topology', model, root: h.root, status: h.status, workspace: { tooltip } };
  h.coordinator.start(args); h.runAll();
  const canvas = h.root.querySelector('canvas');
  const send = (type, x, y) => canvas.dispatchEvent(new h.dom.window.MouseEvent(type, { bubbles: true, clientX: x, clientY: y, button: 0 }));
  send('pointermove', 32, 240);
  assert.equal(tooltip.hidden, false, 'hit testing opens tooltip');
  assert.match(tooltip.textContent, /192.0.2.0.*healthy.*HOP 0.*1 observations/);
  const before = drawTopologyCanvas(fakeContext(), model);
  send('pointerdown', 32, 240); send('pointermove', 82, 280); send('pointerup', 82, 280);
  const after = h.coordinator.current.projection;
  assert.equal(after.nodes[0].screen.x, before.nodes[0].screen.x + 50);
  assert.equal(after.nodes[0].screen.y, before.nodes[0].screen.y + 40);
  assert.deepEqual(after.nodes.slice(1), before.nodes.slice(1));
  assert.deepEqual(after.links[0].from_screen, { x: 82, y: 280 });
  h.coordinator.updateTopologyView({ ownerId: 1, inputSignature: 'rich', generation: h.coordinator.generation, mode: '3d' });
  assert.equal(h.coordinator.current.projection.nodes.find(n => n.id === 'n0').screen.x, 82);
  h.coordinator.cancel();
  tooltip.textContent = 'successor';
  send('pointermove', 82, 280);
  assert.equal(tooltip.textContent, 'successor');
  assert.equal(tooltip.hidden, true);
});

test('rich graph draws curved connections and halos using local aliases without changing facts', () => {
  const context = fakeContext();
  context.quadraticCurveTo = (...args) => context.calls.push(['quadraticCurveTo', ...args]);
  const topology = topologyModel(2);
  topology.nodes[0].display_label = 'Boundary router';
  topology.nodes[0].display_note = 'Local note';
  topology.links = [{ from: 'n0', to: 'n1', status: 'healthy' }];
  const before = JSON.stringify(topology);
  const projection = drawTopologyCanvas(context, topology);
  assert.equal(projection.nodes[0].label, 'Boundary router');
  assert.ok(context.calls.some(call => call[0] === 'quadraticCurveTo'));
  assert.equal(context.calls.filter(call => call[0] === 'arc').length, 4, 'core and halo per node');
  assert.equal(JSON.stringify(topology), before);
});

test('drawTopologyCanvas clears, paints background/grid, then directed links and arrowheads before status-colored nodes and bounded labels', () => {
  const context = fakeContext();
  const attack = '<svg onload="globalThis.canvasPwned=1">' + 'x'.repeat(200);
  const topology = {
    nodes: [
      { id: 'a', address: attack, status: 'healthy', hop_min: 0 },
      { id: 'b', address: 'branch', status: 'degraded', hop_min: 1 },
      { id: 'c', address: 'other', status: 'failure', hop_min: 1 },
      { id: 'd', address: 'rejoin', status: 'unknown', hop_min: 2 }
    ],
    links: [
      { from: 'a', to: 'b', status: 'healthy' }, { from: 'a', to: 'c', status: 'degraded' },
      { from: 'b', to: 'd', status: 'failure' }, { from: 'c', to: 'd', status: 'unknown' }
    ],
    routes: [
      { result_index: 0, attempt: 1, status: 'healthy', node_ids: ['a', 'b', 'd'] },
      { result_index: 1, attempt: 1, status: 'degraded', node_ids: ['a', 'c', 'd'] }
    ]
  };
  const projection = drawTopologyCanvas(context, topology, { viewport: { width: 640, height: 360, dpr: 2 } });

  assert.equal(projection.links.length, 4, 'branch/rejoin keeps every directed link');
  assert.deepEqual(context.calls.slice(0, 3).map(call => call[0]), ['setTransform', 'clearRect', 'fillStyle']);
  const firstLink = context.calls.findIndex(call => call[0] === 'moveTo');
  const firstArrow = context.calls.findIndex(call => call[0] === 'closePath');
  const firstNode = context.calls.findIndex(call => call[0] === 'arc');
  assert.ok(firstLink >= 0 && firstArrow > firstLink && firstNode > firstArrow, 'links and arrowheads draw before nodes');
  for (const color of ['#22c55e', '#f59e0b', '#ef4444', '#94a3b8']) {
    assert.ok(context.calls.some(call => call[0] === 'fillStyle' && call[1] === color));
  }
  const labels = context.calls.filter(call => call[0] === 'fillText');
  assert.equal(labels.length, 4);
  assert.ok(labels.every(call => call[1].length <= 43 && Number.isFinite(call[2]) && Number.isFinite(call[3])));
  assert.equal(globalThis.canvasPwned, undefined);
});

test('drawTopologyCanvas supports bounded 2d/3d edge labels and rejects visual limits plus one', () => {
  const limitContext = fakeContext();
  const limitTopology = {
    nodes: [{ id: 'a', status: 'healthy' }, { id: 'b', status: 'healthy' }],
    links: [{ from: 'a', to: 'b', status: 'healthy' }],
    routes: [{ result_index: 0, attempt: 1, status: 'healthy', node_ids: ['a', 'b'] }]
  };
  const limitProjection = drawTopologyCanvas(limitContext, limitTopology, { mode: '3d', transform: { yaw: 0.4, pitch: 0.2 } });
  assert.equal(limitProjection.mode, '3d');
  assert.ok(limitProjection.nodes.every(node => Number.isFinite(node.depth)));
  assert.throws(() => drawTopologyCanvas(limitContext, { nodes: Array.from({ length: 501 }, (_, id) => ({ id: String(id), status: 'healthy' })), links: [], routes: [] }), /at most 500/i);

  const labels = ['192.0.2.255', '2001:db8:85a3::8a2e:370:7334', 'x'.repeat(40), '서울망경계'.repeat(10)];
  const topology = {
    nodes: labels.map((address, index) => ({ id: `n${index}`, address, status: 'healthy', hop_min: 0 })),
    links: [], routes: []
  };
  const inset = 4;
  const labelHeight = 14;

  for (const width of [800, 400, 375, 320, 100]) {
    const states = [
      ['initial', {}],
      ['pan-right', { panX: width / 2 }],
      ['pan-zoom-left', { panX: -width / 2, zoom: 1.1 }],
      ['reset', {}]
    ];
    for (const mode of ['2d', '3d']) {
      for (const [stateName, transform] of states) {
        const context = fakeContext();
        const viewport = { width, height: 480, dpr: 2 };
        const projection = drawTopologyCanvas(context, topology, { mode, transform, viewport });
        const drawn = context.calls.filter(call => call[0] === 'fillText');
        assert.ok(drawn.length > 0, `${mode}/${width}/${stateName} draws visible labels`);
        assert.ok(projection.nodes.every(node => Number.isFinite(node.screen.x) && Number.isFinite(node.screen.y)));
        if (stateName === 'pan-right') assert.ok(projection.nodes.some(node => node.screen.x === width && node.visible), `${mode}/${width} reaches exact right edge`);
        if (stateName === 'pan-zoom-left') assert.ok(projection.nodes.some(node => node.screen.x === 0 && node.visible), `${mode}/${width} reaches exact left edge`);

        for (const call of drawn) {
          const [, text, x, y, maxWidth, textState] = call;
          assert.ok(Number.isFinite(x) && Number.isFinite(y) && Number.isFinite(maxWidth) && maxWidth > 0, `${mode}/${width}/${stateName} uses finite label geometry`);
          assert.ok(maxWidth <= Math.min(160, width - inset * 2), `${mode}/${width}/${stateName} bounds maxWidth`);
          assert.equal(textState.textAlign, 'center', `${mode}/${width}/${stateName} resets horizontal text state`);
          assert.ok(textState.textBaseline === 'top' || textState.textBaseline === 'bottom', `${mode}/${width}/${stateName} resets vertical text state`);
          assert.ok(x - maxWidth / 2 >= inset - 1e-9, `${mode}/${width}/${stateName} keeps ${text} inside left inset`);
          assert.ok(x + maxWidth / 2 <= width - inset + 1e-9, `${mode}/${width}/${stateName} keeps ${text} inside right inset`);
          const top = textState.textBaseline === 'bottom' ? y - labelHeight : y;
          const bottom = textState.textBaseline === 'bottom' ? y : y + labelHeight;
          assert.ok(top >= inset - 1e-9, `${mode}/${width}/${stateName} keeps ${text} inside top inset`);
          assert.ok(bottom <= viewport.height - inset + 1e-9, `${mode}/${width}/${stateName} keeps ${text} inside bottom inset`);
        }

        const arcs = context.calls.filter(call => call[0] === 'arc').filter((_, index) => index % 2 === 1); // core, excluding the new halo
        assert.deepEqual(arcs.map(call => call.slice(1, 4)), projection.nodes.filter(node => node.visible).map(node => [node.screen.x, node.screen.y, node.screen.radius]), `${mode}/${width}/${stateName} does not alter node geometry`);
        assert.deepEqual({ textAlign: context.textAlign, textBaseline: context.textBaseline }, { textAlign: 'right', textBaseline: 'alphabetic' }, `${mode}/${width}/${stateName} does not leak text state`);
        if (stateName === 'initial') {
          assert.ok(drawn.some(call => call[1] === labels[0]), 'IPv4 label remains exact');
          assert.ok(drawn.some(call => call[1] === labels[1]), 'IPv6 label remains exact');
          assert.ok(drawn.some(call => call[1] === labels[2]), 'exact 40-character label does not gain ellipsis');
          assert.ok(drawn.some(call => call[1] === `${labels[3].slice(0, 40)}…`), 'long Korean label is bounded with ellipsis');
        }
      }
    }
  }
});

test('topology commits exactly one finite accessible canvas before drawing and owner-safe updates redraw without DOM or plan work', () => {
  const h = harness();
  const generation = h.coordinator.start({
    ownerId: 'A', inputSignature: 'a', view: 'topology', mode: '3d', transform: { yaw: 0.3 },
    model: topologyModel(2), root: h.root, status: h.status
  });
  assert.equal(h.context.calls.length, 0);
  h.runNext();
  const canvas = h.root.querySelector('canvas.topology-canvas');
  assert.ok(canvas);
  assert.equal(h.root.firstElementChild, canvas);
  assert.equal(h.root.querySelectorAll('canvas').length, 1);
  assert.equal(h.root.querySelectorAll('svg').length, 0);
  assert.equal(canvas.dataset.mode, '2d', 'detached materialization has a safe 2d default');
  assert.equal(canvas.getAttribute('aria-hidden'), null);
  assert.equal(canvas.tabIndex, 0);
  assert.equal(canvas.getAttribute('role'), 'img');
  assert.match(canvas.getAttribute('aria-label'), /토폴로지.*그래프/);
  assert.equal(canvas.getAttribute('aria-describedby'), 'topology-view-help topology-render-status');
  assert.ok(Number.isFinite(canvas.width) && canvas.width > 0 && Number.isFinite(canvas.height) && canvas.height > 0);
  assert.ok(Number.isFinite(Number(canvas.dataset.dpr)) && Number(canvas.dataset.dpr) > 0);
  assert.equal(h.context.calls.length, 0, 'draw is deferred until after the canvas commit');
  h.runNext();
  assert.equal(canvas.dataset.mode, '3d');
  assert.ok(h.context.calls.length > 0);
  h.runAll();

  const node = h.root.querySelector('.topology-node');
  const elementCount = h.document.getElementsByTagName('*').length;
  const callCount = h.context.calls.length;
  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'A', inputSignature: 'a', generation, mode: '2d', transform: { panX: 12, zoom: 1.5 } }), true);
  assert.equal(canvas.dataset.mode, '2d');
  assert.ok(h.context.calls.length > callCount);
  assert.strictEqual(h.root.querySelector('canvas.topology-canvas'), canvas);
  assert.strictEqual(h.root.querySelector('.topology-node'), node);
  assert.equal(h.document.getElementsByTagName('*').length, elementCount);

  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'A', inputSignature: 'a', generation, viewport: { width: 375, height: 240, dpr: 2 } }), true);
  assert.equal(canvas.width, 750);
  assert.equal(canvas.height, 480);
  assert.equal(canvas.dataset.dpr, '2');

  const afterUpdate = h.context.calls.length;
  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'stale', inputSignature: 'a', generation, mode: '3d' }), false);
  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'A', inputSignature: 'old', generation, mode: '3d' }), false);
  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'A', inputSignature: 'a', generation: generation - 1, mode: '3d' }), false);
  assert.equal(h.context.calls.length, afterUpdate);
  h.coordinator.dispose();
  assert.equal(h.coordinator.updateTopologyView({ ownerId: 'A', inputSignature: 'a', generation, mode: '3d' }), false);
});

test('graph comes first with a collapsed native semantic inspector costed in every chunk', () => {
  const h = harness();
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(500), root: h.root, status: h.status });
  let previous = h.document.querySelectorAll('*').length;
  while (h.runNext()) {
    const count = h.document.querySelectorAll('*').length;
    assert.ok(count - previous <= 100);
    assert.ok(count <= 1200);
    previous = count;
  }
  const inspector = h.root.querySelector('details.topology-inspector');
  assert.ok(inspector, 'native details inspector exists');
  assert.equal(inspector.open, false);
  assert.equal(inspector.firstElementChild.tagName, 'SUMMARY');
  assert.match(inspector.firstElementChild.textContent, /노드.*링크.*경로/);
  assert.equal(h.root.firstElementChild.tagName, 'CANVAS');
  assert.equal(inspector.querySelectorAll('.topology-node').length, 500);
  assert.equal(h.root.querySelectorAll(':scope > .topology-node').length, 0);
  const plan = h.states.at(-1).plan;
  assert.equal(plan.plannedElements, h.root.querySelectorAll('*').length);
  inspector.open = true;
  assert.equal(h.root.querySelectorAll('*').length, plan.plannedElements);
});

test('Canvas context failure keeps the bounded semantic inspector and reports only a local visual limitation', () => {
  const h = harness({ html: '<section id="report">report stays</section><pre id="raw">raw stays</pre>' });
  h.dom.window.HTMLCanvasElement.prototype.getContext = () => { throw new Error('context denied'); };
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(3), root: h.root, status: h.status });
  h.runAll();

  assert.equal(h.root.querySelectorAll('canvas.topology-canvas').length, 1);
  assert.equal(h.root.querySelectorAll('.topology-node').length, 3);
  assert.equal(h.root.querySelector('canvas').dataset.drawState, 'unavailable');
  assert.equal(h.document.querySelector('#report').textContent, 'report stays');
  assert.equal(h.document.querySelector('#raw').textContent, 'raw stays');
  assert.equal(h.status.textContent, '표시 완료 · 그래프를 표시할 수 없습니다. 노드 · 링크 · 경로 상세 보기를 펼쳐 확인하세요.');
  assert.doesNotMatch(h.status.textContent, /context denied/);
  assert.equal(h.states.at(-1).phase, 'ready');
  assert.equal(h.states.at(-1).localLimitation, 'canvas_context_unavailable');
  assert.equal(h.states.some(state => state.phase === 'error'), false);
});

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

  assert.equal(h.status.textContent, '표시 0/223');
  assert.equal(h.root.getAttribute('aria-busy'), 'true');
  assert.equal(h.root.hasAttribute('aria-live'), false);
  assert.equal(h.status.getAttribute('role'), 'status');
  assert.equal(h.status.getAttribute('aria-live'), 'polite');

  h.runNext();
  assert.equal(h.root.querySelectorAll("*").length, 100);
  assert.equal(h.status.textContent, '표시 100/223');
  h.runNext();
  assert.equal(h.root.querySelectorAll("*").length, 100, 'canvas draw has its own bounded callback');
  assert.ok(h.context.calls.length > 0);
  h.runNext();
  assert.equal(h.root.querySelectorAll("*").length, 200);
  assert.equal(h.status.textContent, '표시 200/223');
  h.runNext();
  assert.equal(h.root.querySelectorAll("*").length, 223);
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
  assert.equal(h.root.querySelectorAll("*").length, 100);
  assert.equal(h.context.calls.length, 0);
  const staleSecondChunk = h.queue[0].callback;

  h.coordinator.start({ ownerId: 'B', inputSignature: 'b', view: 'topology', model: topologyModel(1), root: h.root, status: h.status });
  assert.equal(h.root.childElementCount, 0, 'replacement clears A-owned nodes');
  assert.deepEqual(focused, ['replaced']);
  staleSecondChunk();
  assert.equal(h.context.calls.length, 0, 'stale scheduled canvas draw is owner-gated');
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.root.getAttribute('aria-busy'), 'true');
  assert.equal(h.status.textContent, '표시 0/4');
  h.runAll();
  assert.equal(h.root.childElementCount, 2);
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
  assert.equal(h.status.textContent, '표시 0/4');
  h.runAll();
  assert.equal(h.root.childElementCount, 2);
});

test('owner changes prevent materialization and commit', () => {
  let owner = true;
  const h = harness({ owns: () => owner });
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model: topologyModel(2), root: h.root, status: h.status });
  owner = false;
  h.runAll();
  assert.equal(h.root.childElementCount, 0);
  assert.equal(h.status.textContent, '표시 0/5');
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
  assert.equal(h.status.textContent, '표시 0/5');
  h.runAll();
  assert.equal(h.root.querySelectorAll("*").length, 5);
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

test('private context semantics disclose route-specific inference without adding DOM or breaking editing', () => {
  const h = harness();
  const model = { nodes: ['8.8.8.8','10.1.2.3','8.8.4.4'].map((address, i) => ({ id: `n${i}`, kind: 'ip', address, status: 'healthy', observations: 1,
    ...(i !== 1 ? { public_ip: true, asn: { number: 15169, organization: 'Observed only' } } : { display_label: 'Router', display_note: 'x'.repeat(1024) }) })),
    links: [{ from: 'n0', to: 'n1', status: 'healthy', observations: 1 }, { from: 'n1', to: 'n2', status: 'healthy', observations: 1 }],
    routes: [{ result_index: 0, attempt: 1, node_ids: ['n0','n1','n2'], status: 'healthy', complete: true, reached: true }] };
  h.coordinator.start({ ownerId: 'A', inputSignature: 'a', view: 'topology', model, root: h.root, status: h.status });
  h.runAll();
  assert.match(h.status.textContent, /ASN 문맥 1개 표시 · 0개 생략 \(추정\)/);
  const node = h.root.querySelector('[data-node-id="n1"]');
  for (const text of [node.textContent, node.title, node.getAttribute('aria-label'), node.dataset.detail.slice(0, 2048)]) {
    assert.match(text, /AS15169 사이 사설 구간 · 추정/);
    assert.match(text, /경로 1 · 시도 1 · 구간 1–1/);
    assert.match(text, /양끝 공인 IP의 ASN이 같아 표시한 경로 문맥이며, 사설 IP의 ASN 소속을 확인한 것은 아닙니다/);
    assert.doesNotMatch(text, /Observed only|public IP/);
  }
  assert.equal(node.tagName, 'BUTTON'); assert.equal(node.draggable, true); assert.equal(node.childElementCount, 0);
  assert.equal(node.dataset.labelAddress, '10.1.2.3');
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
