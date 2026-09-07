import test from 'node:test';
import assert from 'node:assert/strict';
import { filterTopologyModel, planTopologyDOM } from './topology-model.js';
import { projectTopology } from './topology-visualizer.js';
import { presentTopology } from './topology-presentation.js';

function graph(paths = [['a', 'u1', 'u2', 'b', 'c']]) {
  const ids = [...new Set(paths.flat())];
  const pairs = new Map();
  for (const path of paths) path.slice(1).forEach((to, i) => pairs.set(`${path[i]}:${to}`, { from: path[i], to, status: 'unknown', observations: 1 }));
  return { nodes: ids.map((id, i) => ({ id, kind: id.startsWith('u') ? 'unknown' : 'ip', address: id.startsWith('u') ? '' : `192.0.2.${i+1}`, status: id.startsWith('u') ? 'unknown' : 'healthy', hop_min: i, hop_max: i, observations: 1 })), links: [...pairs.values()], routes: paths.map((node_ids, result_index) => ({ result_index, attempt: 1, node_ids, status: 'healthy', complete: true, reached: true })) };
}

test('separate routes, shared unknowns, revisits, leading/trailing and all-unknown spans are safe', () => {
  const paths = [['u0', 'a', 'u1', 'u2', 'b', 'u3'], ['c', 'u1', 'u2', 'd'], ['a', 'u1', 'a'], ['u1', 'u2']];
  const raw = graph(paths); const before = JSON.stringify(raw);
  for (const showUnresponsive of [true, false]) {
    const filtered = filterTopologyModel(raw, [0,1,2,3], { showUnresponsive });
    const plan = planTopologyDOM(filtered), p = plan.presentation;
    assert.deepEqual(plan, planTopologyDOM(filtered));
    if (showUnresponsive) {
      const groups = p.nodes.filter(n => n.kind === 'unknown-group');
      assert.equal(groups.length, 6);
      assert.equal(new Set(groups.map(n => n.id)).size, 6);
    } else {
      assert.deepEqual(p.connectors.map(c => [c.from, c.to]), [['a','b'], ['c','d'], ['a','a']]);
      assert.equal(p.nodes.some(n => n.kind.startsWith('unknown')), false);
      assert.deepEqual(p.routes[3].node_ids, []);
    }
    for (const mode of ['2d','3d']) assert.doesNotThrow(() => projectTopology(plan.topology, { mode, presentation: p }));
    const one = planTopologyDOM(filterTopologyModel(raw, [1], { showUnresponsive })).presentation;
    assert.equal(one.nodes.some(n => ['a','b'].includes(n.id)), false);
  }
  assert.equal(JSON.stringify(raw), before);
});

test('projected node, connector and document costs are bounded including shared-run expansion', () => {
  const repeated = graph([['a', 'b'], ['a', 'u1', 'b', 'u2', 'a', 'u3', 'b']]);
  for (let elementBudget = 2; elementBudget <= 18; elementBudget++) {
    const p = presentTopology(repeated, { showUnresponsive: false, elementBudget });
    const route = p.routes[1];
    assert.deepEqual(route.node_ids, ['a', 'b', 'a', 'b'].slice(0, route.node_ids.length));
    assert.equal(route.complete, route.node_ids.length === 4);
    assert.equal(p.connectors.length, Math.max(0, route.node_ids.length - 1), 'another route observed pair or earlier repeated bypass cannot stand in for this occurrence');
  }
  const raw = graph(Array.from({ length: 200 }, () => ['a', 'u1', 'b', 'u2', 'c', 'u3', 'd']));
  for (const showUnresponsive of [true, false]) for (const existingDOMElements of [0, 700, 1150, 1199]) {
    const plan = planTopologyDOM(filterTopologyModel(raw, raw.routes.map(r => r.result_index), { showUnresponsive }), { existingDOMElements });
    const p = plan.presentation;
    assert.ok(p.nodes.length <= 500); assert.ok(p.links.length + p.connectors.length <= 1000);
    assert.ok(plan.estimatedDOMElements <= 1200);
    for (const stat of Object.values(p.stats)) assert.equal(stat.total, stat.displayed + stat.omitted);
    const mounted = plan.mountItems.filter(i => ['topology-node','topology-link','topology-connector','topology-route'].includes(i.kind));
    if (plan.renderable) assert.equal(mounted.length, p.nodes.length + p.links.length + p.connectors.length + p.routes.length);
    const ids = new Set(p.nodes.map(n => n.id));
    for (const edge of [...p.links, ...p.connectors]) assert.ok(ids.has(edge.from) && ids.has(edge.to));
    p.routes.forEach((route, occurrence) => {
      const expected = showUnresponsive
        ? ['a', `presentation:unknown:${route.result_index}:1:${occurrence}:1`, 'b', `presentation:unknown:${route.result_index}:1:${occurrence}:3`, 'c', `presentation:unknown:${route.result_index}:1:${occurrence}:5`, 'd']
        : ['a', 'b', 'c', 'd'];
      assert.deepEqual(route.node_ids, expected.slice(0, route.node_ids.length), 'budget keeps a presentation prefix, never joins across a missing group');
      if (route.node_ids.length < expected.length) assert.equal(route.complete, false);
      route.node_ids.slice(1).forEach((to, i) => {
        const from = route.node_ids[i];
        assert.ok(p.links.some(e => e.from === from && e.to === to) ||
          p.connectors.some(e => e.route_occurrence === occurrence && e.from === from && e.to === to), 'each route adjacency retains its own connector or observed link');
      });
    });
  }
});

test('visualizer rejects oversized separate presentation data', () => {
  const raw = graph(); const plan = planTopologyDOM(raw);
  const presentation = { ...plan.presentation, connectors: Array(1001).fill(plan.presentation.connectors[0]) };
  assert.throws(() => projectTopology(plan.topology, { presentation }), /at most 1000/);
});

test('hidden unknown span keeps responsive tail and draws only a distinct presentation bypass', () => {
  const raw = graph(); const before = JSON.stringify(raw);
  const filtered = filterTopologyModel(raw, [0], { showUnresponsive: false });
  assert.deepEqual(filtered.routes, raw.routes);
  const plan = planTopologyDOM(filtered);
  assert.deepEqual(plan.presentation.nodes.map(n => n.id), ['a', 'b', 'c']);
  assert.equal(plan.presentation.connectors.length, 1);
  assert.equal(plan.presentation.connectors[0].label, '무응답 2홉 생략 · 직접 연결 관측 아님');
  assert.equal(plan.presentation.connectors[0].from, 'a');
  assert.equal(plan.presentation.connectors[0].to, 'b');
  assert.equal(plan.presentation.links.length, 1);
  assert.equal(JSON.stringify(raw), before);
  for (const mode of ['2d', '3d']) {
    const projected = projectTopology(plan.topology, { mode, presentation: plan.presentation });
    assert.equal(projected.nodes.length, 3);
    assert.equal(projected.links.length, 1);
    assert.equal(projected.connectors[0].kind, 'bypass');
  }
});

test('consecutive unknown hops fold per route occurrence, without changing observed topology', () => {
  const raw = graph(); const before = JSON.stringify(raw);
  const plan = planTopologyDOM(filterTopologyModel(raw, [0]));
  assert.equal(plan.presentation?.nodes.length, 4);
  const folded = plan.presentation.nodes.find(n => n.kind === 'unknown-group');
  assert.equal(folded.folded_count, 2);
  assert.deepEqual(folded.member_ids, ['u1', 'u2']);
  assert.equal(folded.editable, false);
  assert.equal(plan.presentation.connectors.length, 2);
  assert.deepEqual(plan.topology.links, raw.links);
  assert.equal(JSON.stringify(raw), before);
  for (const mode of ['2d', '3d']) {
    const projected = projectTopology(plan.topology, { mode, presentation: plan.presentation });
    assert.equal(projected.nodes.length, 4);
    assert.equal(projected.connectors.length, 2);
  }
});
