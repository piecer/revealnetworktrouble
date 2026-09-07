import { readFileSync } from 'node:fs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { filterTopologyModel, planTopologyDOM, topologyModelFromReport } from './topology-model.js';
import { normalizeReport } from './state.js';

import { projectTopology } from './topology-visualizer.js';
import { presentTopology } from './topology-presentation.js';

function graph(paths = [['a', 'u1', 'u2', 'b', 'c']]) {
  const ids = [...new Set(paths.flat())];
  const pairs = new Map();
  for (const path of paths) path.slice(1).forEach((to, i) => pairs.set(`${path[i]}:${to}`, { from: path[i], to, status: 'unknown', observations: 1 }));
  return { nodes: ids.map((id, i) => ({ id, kind: id.startsWith('u') ? 'unknown' : 'ip', address: id.startsWith('u') ? '' : `192.0.2.${i+1}`, status: id.startsWith('u') ? 'unknown' : 'healthy', hop_min: i, hop_max: i, observations: 1 })), links: [...pairs.values()], routes: paths.map((node_ids, result_index) => ({ result_index, attempt: 1, node_ids, status: 'healthy', complete: true, reached: true })) };
}

function asnGraph(paths = [['a', 'p', 'q', 'b']]) {
  const raw = graph(paths);
  for (const node of raw.nodes) {
    node.address = ({ a: '8.8.8.8', b: '8.8.4.4', c: '1.1.1.1', d: '1.0.0.1', p: '10.1.2.3', q: '172.16.2.3' })[node.id] ?? node.address;
    if (['a','b','c','d'].includes(node.id)) {
      node.public_ip = true;
      node.asn = { number: ['a','b'].includes(node.id) ? 15169 : 13335, organization: node.id };
    }
  }
  for (const link of raw.links) link.status = 'healthy';
  return raw;
}

test('Go canonical prefix interior bits and boundary witnesses match private and public inference', () => {
  const rows = JSON.parse(readFileSync(new URL('../testdata/asn-classification.json', import.meta.url), 'utf8'));
  assert.ok(rows.length > 1000);
  for (const row of rows) {
    for (const role of ['private', 'public']) {
      const raw = asnGraph([['a','p','b']]);
      raw.nodes.find(n => n.id === (role === 'private' ? 'p' : 'a')).address = row.address;
      assert.equal(presentTopology(raw).stats.asn_contexts.displayed, Number(row[role]), `${role}: ${row.address}`);
    }
  }
});

test('exact Go compact report bytes normalize into contextual presentation without changing facts or Geo', () => {
  const bytes = readFileSync(new URL('../testdata/compact-asn-context-report.json', import.meta.url), 'utf8');
  const raw = JSON.parse(bytes), before = JSON.stringify(raw);
  const normalized = normalizeReport(raw), facts = JSON.stringify(normalized);
  const model = topologyModelFromReport(normalized), modelBefore = JSON.stringify(model);
  const plan = planTopologyDOM(model);
  assert.deepEqual(plan.presentation.stats.asn_contexts, { total: 2, displayed: 2, omitted: 0 });
  for (const node of plan.presentation.nodes.filter(n => n.asn_contexts)) {
    assert.equal(node.asn, undefined); assert.equal(node.geolocation, undefined); assert.notEqual(node.public_ip, true);
    assert.equal(node.asn_contexts[0].asn, 15169);
  }
  for (const mode of ['2d','3d']) {
    const projected = projectTopology(model, { mode, presentation: plan.presentation });
    assert.equal(projected.nodes.filter(n => n.asn_context && /AS15169.*추정/.test(n.label)).length, 2);
  }
  const geo = planTopologyDOM(model, { view: 'geo' });
  assert.deepEqual(geo.geo, {markers: [], segments: [], arrows: []});
  assert.equal(JSON.stringify(raw), before); assert.equal(JSON.stringify(normalized), facts); assert.equal(JSON.stringify(model), modelBefore);
  const forged = structuredClone(raw); forged.compact_topology.nodes.find(n => !n.public_ip).asn_contexts = [{asn:15169}];
  assert.throws(() => normalizeReport(forged), 'presentation fields are not accepted from the wire');
});

test('global context cap keeps exact 500 memberships and truthfully counts cap-plus-one and later omissions', () => {
  for (const total of [499,500,501,600]) {
    const paths = Array.from({length:total}, (_, i) => ['a',`p${Math.floor(i/4)}`,'b']);
    const raw = asnGraph(paths);
    for (const node of raw.nodes) if (node.id.startsWith('p')) node.address = '10.1.2.3';
    const before = JSON.stringify(raw), result = presentTopology(raw, {elementBudget:1500});
    assert.deepEqual(result.stats.asn_contexts, {total, displayed:Math.min(total,500), omitted:Math.max(0,total-500)});
    assert.equal(result.nodes.reduce((n,v)=>n+(v.asn_contexts?.length??0),0),Math.min(total,500));
    assert.equal(result.nodes.reduce((n,v)=>n+(v.asn_contexts_omitted??0),0),Math.max(0,total-500));
    assert.ok(result.nodes.every(n => (n.asn_contexts?.length??0)<=4));
    assert.equal(JSON.stringify(raw),before);
  }
});

test('ASN legend and public denylist stay aligned with canonical producer policy', () => {
  const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
  assert.match(html, /점선 테두리: 사설 구간 ASN 문맥 추정 \(소속 확인 아님\)/);
  const source = readFileSync(new URL('../backend/diagnostic/network_policy.go', import.meta.url), 'utf8');
  const list = source.match(/var blockedDiagnosticNetworks = mustCIDRs\(([\s\S]*?)\n\)/)[1];
  for (const [, cidr] of list.matchAll(/"([^"]+)"/g)) {
    const raw = asnGraph(); raw.nodes.find(n => n.id === 'a').address = cidr.split('/')[0];
    assert.equal(presentTopology(raw).stats.asn_contexts.displayed, 0, cidr);
  }
});

test('private classifier accepts only RFC1918 and ULA, including canonical mapped IPv4', () => {
  const yes = ['10.0.0.0', '172.16.0.0', '172.31.255.255', '192.168.255.255', 'fc00::1', 'FDff:ffff::1', '::ffff:10.1.2.3'];
  const no = ['172.15.255.255', '172.32.0.0', '192.169.1.1', '100.64.0.1', '127.0.0.1', '169.254.0.1', '192.0.2.1', '198.18.0.1', '224.0.0.1', '0.0.0.0', '::', '::1', 'fe80::1', 'fec0::1', 'ff02::1', '2001:db8::1', 'fc00::zz', '010.1.2.3', '10.1.2.999', '10.0.0.1.example'];
  for (const [addresses, expected] of [[yes, 1], [no, 0]]) for (const address of addresses) {
    const raw = asnGraph([['a','p','b']]); raw.nodes.find(n => n.id === 'p').address = address;
    assert.equal(presentTopology(raw).stats.asn_contexts.displayed, expected, address);
  }
});

test('public context endpoints must pass diagnostic public classification, not just a claimed flag', () => {
  for (const address of ['127.1.2.3', '100.64.1.1', '169.254.1.1', '192.0.2.1', '198.18.0.1', '203.0.113.1', '240.0.0.1', 'localhost', '::1', '2001:db8::1', '2002::1', 'fc00::1', '64:ff9b::808:808', '3fff::1']) {
    const raw = asnGraph(); raw.nodes.find(n => n.id === 'a').address = address;
    assert.equal(presentTopology(raw).stats.asn_contexts.displayed, 0, address);
  }
  const raw = asnGraph();
  raw.nodes.find(n => n.id === 'a').address = '2001:4860:4860::8888';
  raw.nodes.find(n => n.id === 'b').address = '2001:4860:4860::8844';
  assert.equal(presentTopology(raw).stats.asn_contexts.displayed, 2);
});

test('ASN context never crosses missing/failed evidence or unrelated route occurrences', () => {
  const mutations = [
    g => { g.nodes.find(n => n.id === 'b').asn.number++; },
    g => { delete g.nodes.find(n => n.id === 'b').asn; },
    g => { g.nodes.find(n => n.id === 'b').asn = { organization: 'same' }; },
    ...[0, -1, 1.5, 4294967296, '15169', null].map(number => g => { g.nodes.find(n => n.id === 'b').asn.number = number; }),
    ...[false, undefined, 1].map(flag => g => { g.nodes.find(n => n.id === 'b').public_ip = flag; }),
    g => { g.routes[0].complete = false; },
    g => { g.routes[0].status = 'failure'; },
    g => { g.nodes.find(n => n.id === 'p').status = 'failure'; },
    g => { g.nodes.find(n => n.id === 'a').status = 'unknown'; },
    g => { g.links.splice(1, 1); },
    g => { g.links[1].status = 'failure'; },
    g => { g.links[1].observations = 0; },
    g => { g.routes = [{ ...g.routes[0], node_ids: ['a','p','q'] }, { ...g.routes[0], node_ids: ['q','b'] }]; }
  ];
  for (const [i, mutate] of mutations.entries()) {
    const raw = asnGraph(); mutate(raw); const before = JSON.stringify(raw);
    assert.equal(presentTopology(raw).stats.asn_contexts.displayed, 0, `mutation ${i}`);
    assert.equal(JSON.stringify(raw), before);
  }
  for (const showUnresponsive of [true, false]) {
    const raw = asnGraph([['a','p','u1','q','b']]);
    assert.equal(presentTopology(raw, { showUnresponsive }).stats.asn_contexts.displayed, 0);
  }
});

test('shared conflicting and repeated private contexts remain occurrence-scoped and bounded', () => {
  const raw = asnGraph([['a','p','b','p','a'], ['c','p','d']]);
  const p = presentTopology(raw), node = p.nodes.find(n => n.id === 'p');
  assert.deepEqual(node.asn_contexts.map(c => [c.asn, c.route_occurrence, c.start]), [[15169,0,1], [15169,0,3], [13335,1,1]]);
  const many = asnGraph(Array.from({length: 200}, () => ['a','p','q','b']));
  const bounded = presentTopology(many);
  assert.deepEqual(bounded.stats.asn_contexts, { total: 400, displayed: 8, omitted: 392 });
  assert.equal(bounded.nodes.find(n => n.id === 'p').asn_contexts_omitted, 196);
  for (let elementBudget = 0; elementBudget < 9; elementBudget++) {
    const result = presentTopology(asnGraph(), { elementBudget });
    assert.equal(result.stats.asn_contexts.displayed, elementBudget < 8 ? 0 : 2);
  }
});

test('private context is visibly inferred in both projections, separate from aliases and facts', () => {
  const raw = asnGraph(); raw.nodes.find(n => n.id === 'p').display_label = 'My router';
  const plan = planTopologyDOM(raw);
  for (const mode of ['2d','3d']) {
    const node = projectTopology(plan.topology, { mode, presentation: plan.presentation }).nodes.find(n => n.id === 'p');
    assert.match(node.label, /My router.*AS15169 문맥·추정/);
    assert.ok(node.label.length <= 28, 'keep contextual Canvas text concise instead of squeezing a full disclaimer into maxWidth');
    assert.equal(node.asn_context, true);
  }
  const shared = planTopologyDOM(asnGraph([['a','p','b'], ['c','p','d']]));
  assert.match(projectTopology(shared.topology, { presentation: shared.presentation }).nodes.find(n => n.id === 'p').label, /경로별 ASN 문맥 · 추정/);
  assert.equal(plan.mountItems.filter(i => i.kind === 'topology-node').length, 4);
});

test('same numeric observed ASN bounds a route-scoped private run without assigning private ASN', () => {
  const raw = asnGraph(), before = JSON.stringify(raw);
  const p = presentTopology(raw);
  for (const id of ['p','q']) {
    const node = p.nodes.find(n => n.id === id);
    assert.deepEqual(node.asn_contexts, [{ asn: 15169, route_occurrence: 0, result_index: 0, attempt: 1, start: 1, end: 2 }]);
    assert.equal(node.asn, undefined);
    assert.equal(node.public_ip, undefined);
    assert.equal(node.geolocation, undefined);
  }
  assert.deepEqual(p.stats.asn_contexts, { total: 2, displayed: 2, omitted: 0 });
  assert.equal(JSON.stringify(raw), before);
});

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
