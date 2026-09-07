import test from 'node:test';
import assert from 'node:assert/strict';

import {
  VISUAL_NODES,
  VISUAL_LINKS,
  VISUAL_ROUTES,
  VIEWPORT_LIMITS,
  TRANSFORM_LIMITS,
  STATUS_COLORS,
  DEFAULT_TRANSFORM,
  normalizeViewport,
  createViewTransform,
  updateViewTransform,
  resetViewTransform,
  projectTopology
} from './topology-visualizer.js';

const node = (id, hop = 0, overrides = {}) => ({
  id,
  kind: hop === 0 ? 'local' : 'ip',
  address: hop === 0 ? 'local' : `${id}.example`,
  status: 'healthy',
  hop_min: hop,
  hop_max: hop,
  observations: 1,
  ...overrides
});

const link = (from, to, status = 'healthy') => ({ from, to, status, observations: 1 });
const route = (node_ids, result_index = 0, attempt = 1, overrides = {}) => ({
  result_index, attempt, status: 'healthy', reached: true, complete: true, node_ids, ...overrides
});
const model = (nodes = [], links = [], routes = []) => ({ nodes, links, routes });
const viewport = { width: 800, height: 480, dpr: 2 };

function assertFiniteProjection(projection) {
  for (const item of projection.nodes) {
    for (const value of [item.world.x, item.world.y, item.world.z, item.screen.x, item.screen.y, item.screen.radius, item.depth]) {
      assert.ok(Number.isFinite(value), `${item.id} must contain only finite coordinates`);
    }
    assert.ok(item.screen.x >= 0 && item.screen.x <= projection.viewport.width);
    assert.ok(item.screen.y >= 0 && item.screen.y <= projection.viewport.height);
  }
  for (const item of projection.links) {
    for (const value of [item.from_screen.x, item.from_screen.y, item.to_screen.x, item.to_screen.y]) {
      assert.ok(Number.isFinite(value), `${item.from}->${item.to} must contain only finite coordinates`);
    }
  }
}

test('exports closed visualizer ceilings and immutable status presentation values', () => {
  assert.deepEqual({ VISUAL_NODES, VISUAL_LINKS, VISUAL_ROUTES }, { VISUAL_NODES: 500, VISUAL_LINKS: 1000, VISUAL_ROUTES: 1000 });
  assert.deepEqual(Object.keys(STATUS_COLORS), ['healthy', 'degraded', 'failure', 'unreachable', 'unknown']);
  assert.ok(Object.isFrozen(STATUS_COLORS));
  assert.ok(Object.isFrozen(DEFAULT_TRANSFORM));
  assert.ok(Object.isFrozen(VIEWPORT_LIMITS));
  assert.ok(Object.isFrozen(TRANSFORM_LIMITS));
});

test('empty and single-node topologies produce finite centered 2D projections', () => {
  const empty = projectTopology(model(), { viewport });
  assert.equal(empty.mode, '2d');
  assert.deepEqual(empty.nodes, []);
  assert.deepEqual(empty.links, []);

  const single = projectTopology(model([node('only')]), { viewport });
  assert.equal(single.nodes.length, 1);
  assert.equal(single.nodes[0].screen.x, viewport.width / 2);
  assert.equal(single.nodes[0].screen.y, viewport.height / 2);
  assert.equal(single.nodes[0].label, 'local');
  assert.equal(single.nodes[0].color, STATUS_COLORS.healthy);
  assertFiniteProjection(single);
});

test('initial fit uses projected node bounds in both modes at wide and narrow viewports', () => {
  const value = model(
    [node('root'), node('a', 1), node('b', 1), node('end', 8)],
    [link('root', 'a'), link('root', 'b'), link('a', 'end'), link('b', 'end')],
    [route(['root', 'a', 'end']), route(['root', 'b', 'end'], 1)]
  );
  for (const width of [1440, 375]) for (const mode of ['2d', '3d']) {
    const projected = projectTopology(value, { mode, viewport: { width, height: 480 }, transform: { yaw: 0.4, pitch: 0.2 } });
    const xs = projected.nodes.map(n => n.screen.x), ys = projected.nodes.map(n => n.screen.y);
    const xSpan = Math.max(...xs) - Math.min(...xs), ySpan = Math.max(...ys) - Math.min(...ys);
    assert.ok(xSpan >= (width - 64) * 0.99 || ySpan >= (480 - 64) * 0.99, `${mode}/${width} fills a padded viewport dimension`);
    assert.ok(Math.abs((Math.min(...xs) + Math.max(...xs)) / 2 - width / 2) < 1e-8);
    assert.ok(Math.abs((Math.min(...ys) + Math.max(...ys)) / 2 - 240) < 1e-8);
    assert.ok(projected.nodes.every(n => n.visible && n.screen.x >= 31.99 && n.screen.x <= width - 31.99 && n.screen.y >= 31.99 && n.screen.y <= 448.01));
    assert.equal(projected.links.length, value.links.length);
    const single = projectTopology(model([node('only')]), { mode, viewport: { width, height: 480 } });
    assert.deepEqual(single.nodes[0].screen, { x: width / 2, y: 240, radius: 8 });
  }
});

test('2D layout uses hop depth for x and stable shared-graph branch order for y', () => {
  const value = model(
    [node('root'), node('b', 1), node('a', 1), node('join', 2)],
    [link('root', 'b'), link('a', 'join', 'degraded'), link('root', 'a'), link('b', 'join')],
    [route(['root', 'b', 'join'], 1), route(['root', 'a', 'join'], 0)]
  );
  const projected = projectTopology(value, { viewport, mode: '2d' });
  const byID = new Map(projected.nodes.map(item => [item.id, item]));
  assert.ok(byID.get('root').world.x < byID.get('a').world.x);
  assert.equal(byID.get('a').world.x, byID.get('b').world.x);
  assert.ok(byID.get('a').world.y < byID.get('b').world.y);
  assert.ok(byID.get('a').world.x < byID.get('join').world.x);
  assert.equal(byID.get('join').world.y, 0);
  assert.deepEqual(projected.links.map(item => `${item.from}>${item.to}`), ['a>join', 'b>join', 'root>a', 'root>b']);
  assert.equal(projected.links.find(item => item.from === 'a').color, STATUS_COLORS.degraded);
  assert.equal(projected.links.find(item => item.from === 'a').label, 'a.example → join.example');
});

test('repeated routes and shared nodes remain one node/link with deterministic route memberships', () => {
  const value = model(
    [node('root'), node('shared', 1), node('end', 2)],
    [link('root', 'shared'), link('shared', 'end')],
    [route(['root', 'shared', 'end'], 2, 2), route(['root', 'shared', 'end'], 1, 1)]
  );
  const projected = projectTopology(value, { viewport, mode: '3d' });
  assert.equal(projected.nodes.length, 3);
  assert.equal(projected.links.length, 2);
  assert.deepEqual(projected.nodes.find(item => item.id === 'shared').route_keys, ['1:1:0', '2:2:1']);
  assertFiniteProjection(projected);
});

test('disconnected and unknown nodes get stable finite lanes and local labels', () => {
  const value = model(
    [node('z', 0, { kind: 'unknown', address: '', status: 'unknown' }), node('a', 0), node('m', 1)],
    [link('a', 'm', 'unknown')],
    [route(['a', 'm'])]
  );
  const projected = projectTopology(value, { viewport });
  const byID = new Map(projected.nodes.map(item => [item.id, item]));
  assert.notEqual(byID.get('a').world.y, byID.get('z').world.y);
  assert.equal(byID.get('z').label, 'Unknown hop');
  assert.equal(byID.get('z').color, STATUS_COLORS.unknown);
  assertFiniteProjection(projected);
});

test('3D projection separates result and branch lanes deterministically in world depth', () => {
  const value = model(
    [node('root'), node('left', 1), node('right', 1), node('end', 2)],
    [link('root', 'left'), link('left', 'end'), link('root', 'right'), link('right', 'end')],
    [route(['root', 'right', 'end'], 9, 1), route(['root', 'left', 'end'], 2, 1)]
  );
  const projected = projectTopology(value, { viewport, mode: '3d' });
  const byID = new Map(projected.nodes.map(item => [item.id, item]));
  assert.notEqual(byID.get('left').world.z, byID.get('right').world.z);
  assert.equal(byID.get('root').world.z, 0);
  assert.equal(byID.get('end').world.z, 0);
  assertFiniteProjection(projected);
});

test('input ordering does not change the deterministic projection', () => {
  const nodes = [node('root'), node('a', 1), node('b', 1), node('join', 2)];
  const links = [link('root', 'a'), link('a', 'join'), link('root', 'b'), link('b', 'join')];
  const routes = [route(['root', 'a', 'join'], 0), route(['root', 'b', 'join'], 1)];
  const forward = projectTopology(model(nodes, links, routes), { viewport, mode: '3d' });
  const reverse = projectTopology(model([...nodes].reverse(), [...links].reverse(), [...routes].reverse()), { viewport, mode: '3d' });
  assert.deepEqual(reverse, forward);
});

test('projection snapshots plain input without mutation and rejects accessors or custom prototypes', () => {
  const value = model([node('a'), node('b', 1)], [link('a', 'b')], [route(['a', 'b'])]);
  const before = structuredClone(value);
  projectTopology(value, { viewport });
  assert.deepEqual(value, before);

  let getterCalls = 0;
  const hostile = {};
  Object.defineProperty(hostile, 'nodes', { enumerable: true, get() { getterCalls++; return []; } });
  assert.throws(() => projectTopology(hostile, { viewport }), /plain|accessor|prototype/i);
  assert.equal(getterCalls, 0);
  assert.throws(() => projectTopology(Object.assign(Object.create(null), model())), /plain|prototype/i);
});

test('validation rejects malformed collections, duplicate ids, invalid references, duplicate and self links', () => {
  assert.throws(() => projectTopology({ nodes: {}, links: [], routes: [] }, { viewport }), /nodes.*array/i);
  assert.throws(() => projectTopology(model([node('a'), node('a')]), { viewport }), /duplicate.*node/i);
  assert.throws(() => projectTopology(model([node('a')], [link('a', 'missing')]), { viewport }), /reference|missing/i);
  assert.throws(() => projectTopology(model([node('a')], [link('a', 'a')]), { viewport }), /self/i);
  assert.throws(() => projectTopology(model([node('a'), node('b', 1)], [link('a', 'b'), link('a', 'b')]), { viewport }), /duplicate.*link/i);
  assert.throws(() => projectTopology(model([node('a')], [], [route(['missing'])]), { viewport }), /route.*reference/i);
  assert.throws(() => projectTopology(model([node('a'), node('b', 1)], [], [route(['a', 'b'])]), { viewport }), /directed link/i);
});

test('validation rejects non-finite data, bad scalar types, invalid mode, and over-limit arrays', () => {
  assert.throws(() => projectTopology(model([node('a', Number.NaN)]), { viewport }), /finite/i);
  assert.throws(() => projectTopology(model([node('', 0)]), { viewport }), /id/i);
  assert.throws(() => projectTopology(model([node('a', 0, { status: 3 })]), { viewport }), /status/i);
  assert.throws(() => projectTopology(model(), { viewport, mode: 'wireframe' }), /mode/i);
  assert.throws(() => projectTopology(model(Array.from({ length: VISUAL_NODES + 1 }, (_, index) => node(`n${index}`))), { viewport }), /500/);
  const capNodes = Array.from({ length: VISUAL_NODES }, (_, index) => node(`n${index}`, index));
  const tooManyLinks = Array.from({ length: VISUAL_LINKS + 1 }, (_, index) => link(`n${index % VISUAL_NODES}`, `n${(index + 1) % VISUAL_NODES}`));
  assert.throws(() => projectTopology(model(capNodes, tooManyLinks), { viewport }), /1000/);
  assert.throws(() => projectTopology(model([node('a')], [], Array.from({ length: VISUAL_ROUTES + 1 }, (_, index) => route(['a'], index))), { viewport }), /1000/);
});

test('exact node and link caps project within finite 2D and 3D bounds', () => {
  const nodes = Array.from({ length: VISUAL_NODES }, (_, index) => node(`n${index}`, index));
  const links = [];
  for (let index = 0; index < VISUAL_LINKS; index++) {
    const from = index % VISUAL_NODES;
    const span = 1 + Math.floor(index / VISUAL_NODES);
    links.push(link(`n${from}`, `n${(from + span) % VISUAL_NODES}`));
  }
  const value = model(nodes, links, []);
  for (const mode of ['2d', '3d']) {
    const projected = projectTopology(value, { viewport, mode });
    assert.equal(projected.nodes.length, VISUAL_NODES);
    assert.equal(projected.links.length, VISUAL_LINKS);
    assertFiniteProjection(projected);
  }
});

test('viewport dimensions and DPR clamp to documented finite integer bounds', () => {
  assert.deepEqual(normalizeViewport({ width: -5, height: 1e9, dpr: 100 }), {
    width: VIEWPORT_LIMITS.minWidth,
    height: VIEWPORT_LIMITS.maxHeight,
    dpr: VIEWPORT_LIMITS.maxDPR,
    pixelWidth: VIEWPORT_LIMITS.minWidth * VIEWPORT_LIMITS.maxDPR,
    pixelHeight: VIEWPORT_LIMITS.maxHeight * VIEWPORT_LIMITS.maxDPR
  });
  assert.deepEqual(normalizeViewport({}), {
    width: VIEWPORT_LIMITS.defaultWidth,
    height: VIEWPORT_LIMITS.defaultHeight,
    dpr: VIEWPORT_LIMITS.defaultDPR,
    pixelWidth: VIEWPORT_LIMITS.defaultWidth,
    pixelHeight: VIEWPORT_LIMITS.defaultHeight
  });
  for (const field of ['width', 'height', 'dpr']) assert.throws(() => normalizeViewport({ [field]: Number.POSITIVE_INFINITY }), /finite/i);
});

test('transform creation, controls, and reset keep pan zoom yaw and pitch finite and bounded', () => {
  assert.deepEqual(createViewTransform(), DEFAULT_TRANSFORM);
  const bounded = createViewTransform({ panX: 1e9, panY: -1e9, zoom: 1e9, yaw: 99, pitch: -99 });
  assert.deepEqual(bounded, {
    panX: TRANSFORM_LIMITS.maxPan,
    panY: -TRANSFORM_LIMITS.maxPan,
    zoom: TRANSFORM_LIMITS.maxZoom,
    yaw: TRANSFORM_LIMITS.maxYaw,
    pitch: -TRANSFORM_LIMITS.maxPitch
  });
  const updated = updateViewTransform(DEFAULT_TRANSFORM, { panX: 25, panY: -10, zoom: 0.5, yaw: 0.25, pitch: -0.2 });
  assert.deepEqual(updated, { panX: 25, panY: -10, zoom: 1.5, yaw: 0.25, pitch: -0.2 });
  assert.deepEqual(resetViewTransform(updated), DEFAULT_TRANSFORM);
  assert.notStrictEqual(resetViewTransform(updated), DEFAULT_TRANSFORM);
  assert.throws(() => updateViewTransform(DEFAULT_TRANSFORM, { zoom: Number.NaN }), /finite/i);
});

test('bounded transforms keep both modes finite and reset restores the original projection', () => {
  const value = model([node('a'), node('b', 1)], [link('a', 'b')], [route(['a', 'b'])]);
  const original = projectTopology(value, { viewport, mode: '3d' });
  const moved = projectTopology(value, {
    viewport,
    mode: '3d',
    transform: createViewTransform({ panX: 99999, panY: -99999, zoom: 100, yaw: 100, pitch: -100 })
  });
  assertFiniteProjection(moved);
  assert.notDeepEqual(moved.nodes.map(item => item.screen), original.nodes.map(item => item.screen));
  assert.deepEqual(projectTopology(value, { viewport, mode: '3d', transform: resetViewTransform(moved.transform) }), original);
});

test('node/link status, labels, direction, and visibility are projected without DOM or Canvas globals', () => {
  const previousDocument = globalThis.document;
  const previousCanvas = globalThis.HTMLCanvasElement;
  try {
    delete globalThis.document;
    delete globalThis.HTMLCanvasElement;
    const projected = projectTopology(model(
      [node('a', 0, { address: 'Start', status: 'failure' }), node('b', 1, { address: '', kind: 'unknown', status: 'unreachable' })],
      [link('a', 'b', 'unreachable')],
      [route(['a', 'b'])]
    ), { viewport });
    assert.deepEqual(projected.nodes.map(item => [item.label, item.status, item.color, item.visible]), [
      ['Start', 'failure', STATUS_COLORS.failure, true],
      ['Unknown hop', 'unreachable', STATUS_COLORS.unreachable, true]
    ]);
    assert.deepEqual(projected.links.map(item => [item.directed, item.status, item.color, item.visible]), [
      [true, 'unreachable', STATUS_COLORS.unreachable, true]
    ]);
  } finally {
    if (previousDocument !== undefined) globalThis.document = previousDocument;
    if (previousCanvas !== undefined) globalThis.HTMLCanvasElement = previousCanvas;
  }
});
