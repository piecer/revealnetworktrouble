import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

import { normalizeReport } from './state.js';
import {
  DATA_NODES, DATA_LINKS, DOCUMENT, CHUNK,
  LABEL_FILE, LABEL_RECORDS, LABEL_PAGE, LABEL, NOTE, DOM_COSTS,
  canonicalIP, topologyModelFromReport, filterTopologyModel, planTopologyDOM, commitDOMChunk
} from './topology-model.js';

const started = '2026-09-01T12:00:00Z';

function count(total, displayed = total) {
  return { total, displayed, omitted: total - displayed };
}

function routeCount(total, displayed = total, complete = displayed, partial = 0) {
  return { total, displayed, complete, partial, omitted: total - displayed };
}

function compactTopology(overrides = {}) {
  return {
    schema: 'compact-v1', selection: 'fair-complete-prefix-v1',
    limits: { nodes: 500, links: 1000, max_response_bytes_exclusive: 1048576, max_geo_bundle_bytes: 4096 },
    nodes: [
      { id: 'n1', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 1 },
      { id: 'n2', kind: 'ip', address: '192.0.2.1', status: 'healthy', hop_min: 1, hop_max: 1, latency_ms_avg: 2, observations: 1, public_ip: true,
        geolocation: { city: 'Seoul', region: '', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 } }
    ],
    links: [{ from: 'n1', to: 'n2', status: 'healthy', observations: 1 }],
    routes: [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: ['n1', 'n2'] }],
    stats: { nodes: count(2), links: count(1), routes: routeCount(1), node_observations: count(2), link_observations: count(1) },
    result_stats: [{ result_index: 0, routes: routeCount(1), node_observations: count(2), link_observations: count(1) }],
    geo: { eligible: 1, available: 1, included: 1, omitted: 0, unavailable: 0 },
    truncated: false, truncation_reasons: [],
    ...overrides
  };
}

function report(results, compact) {
  const passed = results.filter(item => item.status === 'healthy').length;
  const status = passed === results.length ? 'healthy' : (passed ? 'degraded' : 'unreachable');
  const value = {
    id: 'r1', status, started_at: started, duration_ms: 1,
    summary: { total: results.length, passed, failed: results.length - passed }, results
  };
  if (compact !== undefined) value.compact_topology = compact;
  return value;
}

function traceResult(address, attempts, topology) {
  return { kind: 'traceroute', address, status: 'healthy', latency_ms: 1, started_at: started, details: { attempts, ...(topology ? { topology } : {}) } };
}

function legacyTopology(resultIndex, attempt, hops = 30) {
  const nodes = [{ id: `r${resultIndex}-a${attempt}-local`, hop: 0, address: 'local', latency_ms: 0, status: 'healthy' }];
  for (let hop = 1; hop <= hops; hop++) {
    nodes.push({ id: `r${resultIndex}-a${attempt}-h${hop}`, hop, address: `198.${resultIndex}.${attempt}.${hop}`, latency_ms: hop, status: 'healthy', public_ip: true });
  }
  return {
    reached: true, nodes,
    links: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: 'healthy', latency_delta_ms: 1 }))
  };
}

test('exports the frontend data, document, chunk, and label ceilings', () => {
  assert.deepEqual(
    { DATA_NODES, DATA_LINKS, DOCUMENT, CHUNK, LABEL_FILE, LABEL_RECORDS, LABEL_PAGE, LABEL, NOTE },
    { DATA_NODES: 500, DATA_LINKS: 1000, DOCUMENT: 1200, CHUNK: 100, LABEL_FILE: 1048576, LABEL_RECORDS: 500, LABEL_PAGE: 100, LABEL: 256, NOTE: 1024 }
  );
});

test('canonicalIP matches Go netip compression, mapped unmapping, and zone semantics', () => {
  assert.equal(canonicalIP('2001:0DB8:0000:0000:0000:0000:0000:0001'), '2001:db8::1');
  assert.equal(canonicalIP('2001:db8::1'), '2001:db8::1');
  assert.equal(canonicalIP('::ffff:192.0.2.1'), '192.0.2.1');
  assert.equal(canonicalIP('0:0:0:0:0:ffff:c000:201'), '192.0.2.1');
  assert.equal(canonicalIP('FE80:0:0:0:0:0:0:1%eth%0'), 'fe80::1%eth%0');
  assert.equal(canonicalIP('fe80::1%'), null);
  assert.equal(canonicalIP('192.168.001.1'), null);
});

test('planTopologyDOM preserves each fixed reserve and explicitly refuses an insufficient document budget', () => {
  const model = { nodes: [], links: [], routes: [] };
  const reserves = {
    topology: DOM_COSTS.topology.chromeReserve,
    geo: DOM_COSTS.geo.fixedReserve,
    labels: DOM_COSTS.labels.fixedReserve
  };

  for (const [view, reserve] of Object.entries(reserves)) {
    const existingDOMElements = DOCUMENT - reserve + 1;
    const refused = planTopologyDOM(model, { view, existingDOMElements });
    assert.equal(refused.renderable, false, view);
    assert.equal(refused.reserveElements, reserve, view);
    assert.deepEqual(refused.mountItems, [], view);
    assert.deepEqual(refused.chunks, [], view);
    assert.equal(refused.plannedElements, 0, view);
    assert.equal(refused.estimatedDOMElements, existingDOMElements, view);
    assert.equal(refused.viewTruncation.truncated, true, view);
    assert.ok(refused.viewTruncation.reasons.includes('dom_budget'), view);

    const exact = planTopologyDOM(model, { view, existingDOMElements: DOCUMENT - reserve });
    assert.equal(exact.renderable, true, view);
    assert.equal(exact.reserveElements, reserve, view);
    assert.equal(exact.estimatedDOMElements, DOCUMENT, view);
  }
});

test('topologyModelFromReport consumes compact data first without mutation and keeps server truncation distinct', () => {
  const compact = compactTopology({
    stats: { nodes: count(4, 2), links: count(3, 1), routes: routeCount(2, 1), node_observations: count(4, 2), link_observations: count(3, 1) },
    result_stats: [{ result_index: 0, routes: routeCount(2, 1), node_observations: count(4, 2), link_observations: count(3, 1) }],
    truncated: true, truncation_reasons: ['node_limit']
  });
  const normalized = normalizeReport(report([traceResult('target.test', [])], compact));
  const before = structuredClone(normalized);
  const model = topologyModelFromReport(normalized);
  assert.equal(model.source, 'compact');
  assert.deepEqual(model.nodes.map(node => node.id), ['n1', 'n2']);
  assert.deepEqual(model.serverTruncation, { truncated: true, reasons: ['node_limit'] });
  assert.deepEqual(model.adapterTruncation, { truncated: false, reasons: [] });
  assert.notStrictEqual(model.nodes, normalized.compact_topology.nodes);
  assert.deepEqual(normalized, before);
});

test('topologyModelFromReport rejects present malformed compact data instead of falling back to legacy', () => {
  const legacy = traceResult('target.test', [{ attempt: 1, status: 'healthy', error_code: '', message: '', topology: legacyTopology(0, 1, 1) }]);
  assert.throws(() => topologyModelFromReport(report([legacy], { schema: 'wrong' })), /compact|schema/i);
});

test('public model and planner reject accessors and custom prototypes without invoking getters', () => {
  let getterCount = 0;
  const hostileReport = {};
  Object.defineProperty(hostileReport, 'compact_topology', { enumerable: true, get() { getterCount++; return compactTopology(); } });
  assert.throws(() => topologyModelFromReport(hostileReport), /accessor|plain|prototype/i);
  assert.equal(getterCount, 0);

  class ReportLike {}
  assert.throws(() => topologyModelFromReport(Object.assign(new ReportLike(), report([]))), /plain|prototype/i);

  const hostileModel = { nodes: [], links: [], routes: [] };
  Object.defineProperty(hostileModel, 'nodes', { enumerable: true, get() { getterCount++; return []; } });
  assert.throws(() => planTopologyDOM(hostileModel), /accessor|plain|prototype/i);
  assert.equal(getterCount, 0);
  assert.throws(() => planTopologyDOM(Object.assign(Object.create(null), { nodes: [], links: [], routes: [] })), /plain|prototype/i);
});

test('legacy adapter canonicalizes IPs like Go netip and never invents missing edges', () => {
  const topology = {
    reached: true,
    nodes: [
      { id: 'local', hop: 0, address: 'local', latency_ms: 0, status: 'healthy' },
      { id: 'v6', hop: 1, address: '2001:0DB8:0:0:0:0:0:1', latency_ms: 1, status: 'healthy' },
      { id: 'mapped', hop: 2, address: '::ffff:192.0.2.1', latency_ms: 2, status: 'healthy' },
      { id: 'bad', hop: 3, address: '999.1.2.3', latency_ms: 3, status: 'healthy' },
      { id: 'tail', hop: 4, address: 'tail.example.', latency_ms: 4, status: 'healthy' }
    ],
    links: [
      { from: 'local', to: 'v6', status: 'healthy', latency_delta_ms: 1 },
      { from: 'v6', to: 'mapped', status: 'healthy', latency_delta_ms: 1 },
      { from: 'bad', to: 'tail', status: 'healthy', latency_delta_ms: 1 }
    ]
  };
  const normalized = normalizeReport(report([traceResult('target.test', [{ attempt: 1, status: 'healthy', error_code: '', message: '', topology }])]));
  const model = topologyModelFromReport(normalized);
  assert.deepEqual(model.nodes.map(node => node.address), ['local', '2001:db8::1', '192.0.2.1']);
  assert.deepEqual(model.nodes.map(node => node.kind), ['local', 'ip', 'ip']);
  assert.equal(model.routes[0].complete, false);
  assert.equal(model.routes[0].node_ids.length, 3);
  assert.equal(model.links.length, 2);
  const pairs = new Set(model.links.map(link => `${link.from}>${link.to}`));
  assert.ok(model.routes[0].node_ids.slice(1).every((id, index) => pairs.has(`${model.routes[0].node_ids[index]}>${id}`)));
});

test('legacy adapter matches Go netip zones containing additional percent characters', () => {
  const topology = {
    reached: true,
    nodes: [
      { id: 'local', hop: 0, address: 'local', latency_ms: 0, status: 'healthy' },
      { id: 'zoned', hop: 1, address: 'FE80:0:0:0:0:0:0:1%eth%0', latency_ms: 1, status: 'healthy' }
    ],
    links: [{ from: 'local', to: 'zoned', status: 'healthy', latency_delta_ms: 1 }]
  };
  const value = report([traceResult('target.test', [{ attempt: 1, status: 'healthy', topology }])]);
  const model = topologyModelFromReport(value);
  assert.equal(model.nodes[1].kind, 'ip');
  assert.equal(model.nodes[1].address, 'fe80::1%eth%0');
});

test('legacy adapter chooses same-hop parallel paths and rejoins deterministically from real directed links', () => {
  const topology = {
    reached: true,
    nodes: [
      { id: 'l', hop: 0, address: 'local', latency_ms: 0, status: 'healthy' },
      { id: 'a', hop: 1, address: '192.0.2.1', latency_ms: 1, status: 'healthy' },
      { id: 'b', hop: 1, address: '192.0.2.2', latency_ms: 1, status: 'healthy' },
      { id: 'r', hop: 2, address: '192.0.2.3', latency_ms: 2, status: 'healthy' }
    ],
    links: [
      { from: 'l', to: 'b', status: 'healthy', latency_delta_ms: 1 },
      { from: 'b', to: 'r', status: 'healthy', latency_delta_ms: 1 },
      { from: 'l', to: 'a', status: 'healthy', latency_delta_ms: 1 },
      { from: 'a', to: 'r', status: 'healthy', latency_delta_ms: 1 }
    ]
  };
  const value = normalizeReport(report([traceResult('target.test', [{ attempt: 1, status: 'healthy', error_code: '', message: '', topology }])]));
  const first = topologyModelFromReport(value);
  for (let repetition = 0; repetition < 100; repetition++) assert.deepEqual(topologyModelFromReport(value), first);
  assert.deepEqual(first.routes[0].node_ids.map(id => first.nodes.find(node => node.id === id).address), ['local', '192.0.2.1', '192.0.2.3']);
});

test('legacy adapter is deterministic in result, attempt, and hop order and stays under data caps', () => {
  const results = [];
  for (let resultIndex = 0; resultIndex < 20; resultIndex++) {
    const attempts = [];
    for (let attempt = 10; attempt >= 1; attempt--) {
      attempts.push({ attempt, status: 'healthy', error_code: '', message: '', topology: legacyTopology(resultIndex, attempt) });
    }
    results.push(traceResult(`target-${resultIndex}.test`, attempts, structuredClone(attempts[0].topology)));
  }
  const normalized = normalizeReport(report(results));
  const before = structuredClone(normalized);
  const first = topologyModelFromReport(normalized);
  const second = topologyModelFromReport(normalized);
  assert.equal(first.source, 'legacy');
  assert.ok(first.nodes.length <= DATA_NODES);
  assert.ok(first.links.length <= DATA_LINKS);
  assert.deepEqual(first, second);
  assert.equal(first.routes[0].result_index, 0);
  assert.equal(first.routes[0].attempt, 1);
  assert.deepEqual(first.routes[0].node_ids.slice(0, 2).map(id => first.nodes.find(node => node.id === id).hop_min), [0, 1]);
  assert.equal(first.adapterTruncation.truncated, true);
  assert.equal(first.serverTruncation.truncated, false);
  assert.deepEqual(normalized, before);
});

test('legacy adapter round-robins results and attempts before spending later edges', () => {
  const results = Array.from({ length: 20 }, (_, resultIndex) => {
    const attempts = Array.from({ length: 10 }, (_, offset) => ({
      attempt: offset + 1, status: 'healthy', error_code: '', message: '',
      topology: legacyTopology(resultIndex, offset + 1, 30)
    }));
    return traceResult(`target-${resultIndex}.test`, attempts);
  });
  const model = topologyModelFromReport(normalizeReport(report(results)));
  assert.equal(new Set(model.routes.map(route => route.result_index)).size, 20);
  for (let resultIndex = 0; resultIndex < 20; resultIndex++) {
    assert.equal(new Set(model.routes.filter(route => route.result_index === resultIndex).map(route => route.attempt)).size, 10);
  }
  assert.ok(model.routes.every(route => route.node_ids.length >= 2));
  assert.equal(model.nodes.length, DATA_NODES);
});

test('legacy adapter retains a 500-node partial prefix for oversized single paths', () => {
  for (const size of [501, 1024]) {
    const nodes = Array.from({ length: size }, (_, index) => ({
      id: `raw-${index}`, hop: index, address: index === 0 ? 'local' : `hop-${index}.example`,
      latency_ms: index, status: 'healthy'
    }));
    const topology = {
      reached: true,
      nodes,
      links: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: 'healthy', latency_delta_ms: 1 }))
    };
    const model = topologyModelFromReport(report([traceResult('target.test', [{ attempt: 1, status: 'healthy', topology }])]));
    assert.equal(model.nodes.length, DATA_NODES);
    assert.equal(model.routes.length, 1);
    assert.equal(model.routes[0].node_ids.length, DATA_NODES);
    assert.equal(model.routes[0].complete, false);
  }
});

test('Geo markers require literal public_ip true and finite coordinates', () => {
  const geo = (latitude = 37.5, longitude = 127) => ({ latitude, longitude });
  const nodes = [
    { id: 'public', public_ip: true, geolocation: geo() },
    { id: 'private', public_ip: false, geolocation: geo() },
    { id: 'missing', geolocation: geo() },
    { id: 'truthy', public_ip: 1, geolocation: geo() },
    { id: 'bad-latitude', public_ip: true, geolocation: geo(Number.POSITIVE_INFINITY, 127) },
    { id: 'bad-longitude', public_ip: true, geolocation: geo(37.5, Number.NaN) }
  ];
  const plan = planTopologyDOM({ nodes, links: [], routes: [] }, { view: 'geo' });
  assert.deepEqual(plan.geo.markers.map(marker => marker.node_id), ['public']);
});

test('planner truncates routes at the first missing directed link and never invents Geo edges', () => {
  const nodes = ['n1', 'n2', 'n3', 'n4'].map((id, index) => ({
    id, public_ip: true,
    geolocation: { latitude: 37 + index, longitude: 127 + index }
  }));
  const links = [
    { from: 'n1', to: 'n2' },
    { from: 'n3', to: 'n4' }
  ];
  const routes = [
    { result_index: 0, attempt: 1, complete: true, node_ids: ['n1', 'n2', 'n3', 'n4'] },
    { result_index: 1, attempt: 1, complete: true, node_ids: ['n4', 'n3'] }
  ];
  const plan = planTopologyDOM({ nodes, links, routes }, { view: 'geo' });
  const selectedLinks = new Set(plan.topology.links.map(link => `${link.from}>${link.to}`));

  assert.deepEqual(plan.topology.routes.map(route => route.node_ids), [['n1', 'n2'], ['n4']]);
  assert.ok(plan.topology.routes.every(route => route.complete === false));
  for (const route of plan.topology.routes) {
    for (let index = 1; index < route.node_ids.length; index++) {
      assert.ok(selectedLinks.has(`${route.node_ids[index - 1]}>${route.node_ids[index]}`));
    }
  }
  for (const edge of [...plan.geo.segments, ...plan.geo.arrows]) {
    assert.ok(selectedLinks.has(`${edge.from}>${edge.to}`));
  }
  assert.deepEqual(plan.geo.segments, [{ from: 'n1', to: 'n2' }]);
  assert.deepEqual(plan.geo.arrows, [{ from: 'n1', to: 'n2' }]);
  assert.equal(plan.viewTruncation.truncated, true);
});

test('unresponsive filtering stops each route before its first unknown node without inventing edges', () => {
  const model = {
    nodes: [
      { id: 'local', kind: 'local', status: 'healthy' },
      { id: 'known', kind: 'ip', status: 'healthy' },
      { id: 'unknown', kind: 'unknown', status: 'unknown' },
      { id: 'tail', kind: 'ip', status: 'healthy' },
      { id: 'other', kind: 'ip', status: 'healthy' }
    ],
    links: [
      { from: 'local', to: 'known' }, { from: 'known', to: 'unknown' },
      { from: 'unknown', to: 'tail' }, { from: 'local', to: 'other' }
    ],
    routes: [
      { result_index: 0, attempt: 1, reached: false, complete: true, node_ids: ['local', 'known', 'unknown', 'tail'] },
      { result_index: 1, attempt: 1, reached: true, complete: true, node_ids: ['local', 'other'] }
    ]
  };

  const filtered = filterTopologyModel(model, new Set([0]), { showUnresponsive: false });
  assert.deepEqual(filtered.nodes.map(node => node.id), ['local', 'known']);
  assert.deepEqual(filtered.links.map(link => `${link.from}>${link.to}`), ['local>known']);
  assert.deepEqual(filtered.routes[0].node_ids, ['local', 'known']);
  assert.equal(filtered.routes[0].complete, false);
  assert.deepEqual(filtered.viewTotals, { nodes: 4, links: 3, routes: 1 });

  const plan = planTopologyDOM(filtered);
  assert.deepEqual(plan.viewStats.nodes, { total: 4, displayed: 2, omitted: 2 });
  assert.deepEqual(plan.viewStats.links, { total: 3, displayed: 1, omitted: 2 });
  assert.deepEqual(plan.viewStats.routes, { total: 1, displayed: 1, omitted: 0 });
});

test('planTopologyDOM is deterministic, fair, referentially closed, chunked, and document bounded', () => {
  const nodes = [{ id: 'n0', kind: 'local', address: 'local', status: 'healthy', hop_min: 0, hop_max: 0, observations: 20 }];
  const links = [];
  const routes = [];
  for (let resultIndex = 0; resultIndex < 20; resultIndex++) {
    const node_ids = ['n0'];
    for (let hop = 1; hop <= 24; hop++) {
      const id = `n-${resultIndex}-${hop}`;
      nodes.push({ id, kind: 'ip', address: `198.18.${resultIndex}.${hop}`, status: 'healthy', hop_min: hop, hop_max: hop, observations: 1, public_ip: true,
        geolocation: { city: `city-${resultIndex}-${hop}`, region: '', country: 'KR', country_code: 'KR', latitude: 30 + resultIndex / 10, longitude: 120 + hop / 10 } });
      links.push({ from: node_ids.at(-1), to: id, status: 'healthy', observations: 1 });
      node_ids.push(id);
    }
    routes.push({ result_index: resultIndex, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids });
  }
  const model = {
    source: 'compact', nodes, links, routes,
    serverTruncation: { truncated: true, reasons: ['response_size'] },
    adapterTruncation: { truncated: false, reasons: [] }
  };
  const before = structuredClone(model);
  const options = { availableElements: DOCUMENT - 173, maxDOMPerChunk: 37 };
  const first = planTopologyDOM(model, options);
  for (let repetition = 0; repetition < 100; repetition++) assert.deepEqual(planTopologyDOM(model, options), first);

  assert.ok(first.plannedElements <= options.availableElements);
  assert.ok(first.plannedElements + 173 <= DOCUMENT);
  assert.ok(first.topology.nodes.length <= DATA_NODES);
  assert.ok(first.topology.links.length <= DATA_LINKS);
  assert.ok(first.geo.markers.length <= DATA_NODES);
  assert.ok(first.geo.segments.length <= DATA_LINKS);
  assert.ok(first.geo.arrows.length <= DATA_LINKS);
  assert.ok(first.chunks.every(chunk => chunk.reduce((sum, item) => sum + item.domCost, 0) <= 37));
  assert.ok(new Set(first.topology.routes.map(route => route.result_index)).size > 1, 'selection should be fair across results');

  const nodeIDs = new Set(first.topology.nodes.map(node => node.id));
  assert.ok(first.topology.links.every(link => nodeIDs.has(link.from) && nodeIDs.has(link.to)));
  assert.ok(first.topology.routes.every(route => route.node_ids.length > 0 && route.node_ids.every(id => nodeIDs.has(id))));
  assert.ok(first.geo.markers.every(marker => nodeIDs.has(marker.node_id)));
  assert.ok(first.geo.segments.every(segment => nodeIDs.has(segment.from) && nodeIDs.has(segment.to)));
  assert.deepEqual(first.serverTruncation, model.serverTruncation);
  assert.equal(first.viewStats.nodes.total, Math.min(nodes.length, DATA_NODES));
  assert.equal(first.viewStats.nodes.total, first.viewStats.nodes.displayed + first.viewStats.nodes.omitted);
  assert.equal(first.viewStats.links.total, first.viewStats.links.displayed + first.viewStats.links.omitted);
  assert.equal(first.viewStats.routes.total, first.viewStats.routes.displayed + first.viewStats.routes.omitted);
  assert.equal(first.labels.maxFileBytes, LABEL_FILE);
  assert.equal(first.labels.maxRecords, LABEL_RECORDS);
  assert.equal(first.labels.pageSize, LABEL_PAGE);
  assert.deepEqual(model, before);
});

test('future renderer plans charge only the active view and runtime commits stay under 1200 DOM elements', () => {
  assert.equal(DOM_COSTS.documentLimit, DOCUMENT);
  assert.equal(DOM_COSTS.maxChunkInsertion, CHUNK);
  assert.deepEqual(DOM_COSTS.topology, { chromeReserve: 180, node: 1, link: 1, route: 1 });
  assert.equal(DOM_COSTS.geo.canvas, 1);
  assert.equal(DOM_COSTS.geo.segment, 0);
  assert.equal(DOM_COSTS.geo.arrow, 0);
  assert.equal(DOM_COSTS.labels.row + DOM_COSTS.labels.cells + DOM_COSTS.labels.control, 5);

  const nodes = Array.from({ length: DATA_NODES }, (_, index) => ({
    id: `n${index}`, kind: 'ip', address: `node-${index}.example`, status: 'healthy', hop_min: index, hop_max: index,
    observations: 1, public_ip: true,
    geolocation: { city: 'Seoul', region: '', country: 'KR', country_code: 'KR', latitude: 37.5, longitude: 127 }
  }));
  const links = nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, status: 'healthy', observations: 1 }));
  const routes = [{ result_index: 0, attempt: 1, status: 'healthy', reached: true, complete: true, node_ids: nodes.map(node => node.id) }];
  const model = { nodes, links, routes, serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] } };
  const labels = Array.from({ length: LABEL_RECORDS }, (_, index) => ({ node_id: `n${index}`, label: `label-${index}`, note: '' }));

  const materialize = (item, document) => {
    if (item.kind === 'label-shell') {
      const table = document.createElement('table');
      table.append(document.createElement('thead'), document.createElement('tbody'), document.createElement('caption'));
      return table;
    }
    if (item.kind === 'label-row') {
      const row = document.createElement('tr');
      row.append(document.createElement('td'), document.createElement('td'), document.createElement('td'), document.createElement('button'));
      return row;
    }
    return document.createElement(item.kind === 'geo-canvas' ? 'canvas' : item.kind === 'geo-accessible-list' ? 'ul' : item.kind === 'geo-accessible-item' ? 'li' : 'div');
  };

  for (const [view, extra] of [['topology', {}], ['geo', {}], ['labels', { labelRecords: labels }]]) {
    const dom = new JSDOM(`<!doctype html><html><body><main id="root">${'<i></i>'.repeat(120)}</main></body></html>`);
    const document = dom.window.document;
    const existingDOMElements = document.getElementsByTagName('*').length;
    const plan = planTopologyDOM(model, { view, existingDOMElements, ...extra });
    assert.equal(plan.view, view);
    assert.ok(plan.chunks.every(chunk => chunk.reduce((sum, item) => sum + item.domCost, 0) <= CHUNK));
    assert.ok(plan.estimatedDOMElements <= DOCUMENT);
    for (const chunk of plan.chunks) commitDOMChunk(document, document.querySelector('#root'), chunk, materialize);
    assert.ok(document.getElementsByTagName('*').length <= DOCUMENT);
    assert.ok(plan.estimatedDOMElements >= document.getElementsByTagName('*').length);
    if (view === 'geo') {
      assert.equal(plan.semanticCounts.geo.segments, links.length);
      assert.equal(plan.mountItems.some(item => item.kind === 'geo-segment' || item.kind === 'geo-arrow'), false);
    }
    if (view !== 'topology') assert.equal(plan.mountItems.some(item => item.kind.startsWith('topology-')), false);
  }
});

test('runtime DOM guard rejects an over-budget chunk before commit', () => {
  const dom = new JSDOM(`<!doctype html><html><body><main id="root">${'<i></i>'.repeat(DOCUMENT - 4)}</main></body></html>`);
  const document = dom.window.document;
  const root = document.querySelector('#root');
  const before = root.childElementCount;
  assert.throws(() => commitDOMChunk(document, root, [{ kind: 'topology-node', value: {}, domCost: 1 }],
    (_item, owner) => owner.createElement('div')), /DOM|budget/i);
  assert.equal(root.childElementCount, before);
});

test('maxDOMPerChunk is a DOM-cost ceiling, legacy maxNodesPerChunk remains an alias, and explicit limits below five are rejected', () => {
  const labelRecords = Array.from({ length: 25 }, (_, index) => ({
    node_id: `n${index}`, label: `label-${index}`, note: ''
  }));
  const model = { nodes: [], links: [], routes: [] };
  const chunkCost = chunk => chunk.reduce((sum, item) => sum + item.domCost, 0);

  for (const options of [{ maxDOMPerChunk: 7 }, { maxNodesPerChunk: 7 }, { maxDOMPerChunk: 1000 }]) {
    const plan = planTopologyDOM(model, { view: 'labels', labelRecords, ...options });
    const requested = options.maxDOMPerChunk ?? options.maxNodesPerChunk;
    assert.ok(plan.chunks.length > 1);
    assert.ok(plan.chunks.every(chunk => chunkCost(chunk) <= requested && chunkCost(chunk) <= CHUNK));
  }

  for (const options of [{ maxDOMPerChunk: 4 }, { maxNodesPerChunk: 4 }]) {
    assert.throws(
      () => planTopologyDOM(model, { view: 'labels', labelRecords, ...options }),
      error => error instanceof TypeError && /maxDOMPerChunk.*at least 5/i.test(error.message)
    );
  }
});

test('planTopologyDOM handles zero budget and hostile over-cap arrays without dangling references', () => {
  const nodes = Array.from({ length: DATA_NODES + 5 }, (_, index) => ({ id: `n${index}`, kind: 'ip', address: `node-${index}`, status: 'healthy', hop_min: index % 256, hop_max: index % 256, observations: 1 }));
  const links = Array.from({ length: DATA_LINKS + 5 }, (_, index) => ({ from: `n${index % DATA_NODES}`, to: `n${(index + 1) % DATA_NODES}`, status: 'healthy', observations: 1 }));
  const model = { source: 'legacy', nodes, links, routes: [], serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: true, reasons: ['node_limit'] } };
  const plan = planTopologyDOM(model, { availableElements: 0, maxDOMPerChunk: 1000 });
  assert.equal(plan.plannedElements, 0);
  assert.deepEqual(plan.topology, { nodes: [], links: [], routes: [] });
  assert.deepEqual(plan.geo, { markers: [], segments: [], arrows: [] });
  assert.ok(plan.chunks.every(chunk => chunk.length <= CHUNK));
  assert.equal(plan.viewTruncation.truncated, true);
});

test('DOM planner fuzzes 1000 deterministic plain max and sparse models without mutation', () => {
  let seed = 0x5eed1234;
  const random = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
    return seed / 0x100000000;
  };
  for (let iteration = 0; iteration < 1000; iteration++) {
    const nodeCount = iteration === 0 ? DATA_NODES + 7 : Math.floor(random() * 36);
    const nodes = Array.from({ length: nodeCount }, (_, index) => ({
      id: `i${iteration}n${index}`, kind: index ? 'ip' : 'local', address: `host-${index}`,
      status: index % 7 ? 'healthy' : 'degraded', hop_min: index, hop_max: index, observations: 1,
      ...(index % 3 === 0 ? { geolocation: { latitude: 37 + index / 1000, longitude: 127 } } : {})
    }));
    const links = nodes.slice(1).map((node, index) => ({
      from: nodes[index].id, to: node.id, status: 'healthy', observations: 1
    }));
    if (iteration === 0) {
      for (let index = links.length; index < DATA_LINKS + 7; index++) {
        links.push({ from: nodes[index % DATA_NODES].id, to: nodes[(index + 11) % DATA_NODES].id, status: 'unknown', observations: 1 });
      }
    }
    const routes = nodes.length ? [{
      result_index: iteration % 4, attempt: 1, status: 'healthy', reached: true, complete: true,
      node_ids: nodes.slice(0, Math.min(nodes.length, 30)).map(node => node.id)
    }] : [];
    const model = { nodes, links, routes, serverTruncation: { truncated: false, reasons: [] }, adapterTruncation: { truncated: false, reasons: [] } };
    const labels = nodes.slice(0, 12).reverse().map(node => ({ node_id: node.id, label: node.address, note: '' }));
    const options = {
      view: ['topology', 'geo', 'labels'][iteration % 3], existingDOMElements: Math.floor(random() * 1100),
      availableElements: Math.floor(random() * 1201), maxDOMPerChunk: 5 + Math.floor(random() * (CHUNK - 4)), labelRecords: labels
    };
    const modelBefore = structuredClone(model);
    const optionsBefore = structuredClone(options);
    const plan = planTopologyDOM(model, options);
    assert.deepEqual(model, modelBefore);
    assert.deepEqual(options, optionsBefore);
    assert.ok(plan.estimatedDOMElements <= DOCUMENT);
    assert.equal(plan.plannedElements, plan.mountItems.reduce((sum, item) => sum + item.domCost, 0));
    assert.ok(plan.chunks.every(chunk => {
      const cost = chunk.reduce((sum, item) => sum + item.domCost, 0);
      return cost <= options.maxDOMPerChunk && cost <= CHUNK;
    }));
    const selected = new Set(plan.topology.nodes.map(node => node.id));
    assert.ok(plan.topology.links.every(link => selected.has(link.from) && selected.has(link.to)));
    assert.ok(plan.topology.routes.every(route => route.node_ids.every(id => selected.has(id))));
    if (plan.view !== 'topology') assert.equal(plan.mountItems.some(item => item.kind.startsWith('topology-')), false);
  }
});

test('commitDOMChunk materializes in a detached HTMLDocument and rejects connected live-document nodes', () => {
  const dom = new JSDOM('<!doctype html><html><body><main id="root"></main></body></html>');
  const document = dom.window.document;
  const root = document.querySelector('#root');
  let materializerDocument;

  assert.equal(commitDOMChunk(document, root, [{ kind: 'test', value: null, domCost: 1 }], (_item, owner) => {
    materializerDocument = owner;
    return owner.createElement('div');
  }), 1);
  assert.notStrictEqual(materializerDocument, document);
  assert.equal(materializerDocument.constructor.name, 'Document');
  assert.strictEqual(root.firstElementChild.ownerDocument, document);

  const connectedLiveNode = root.firstElementChild;
  assert.equal(connectedLiveNode.isConnected, true);
  assert.throws(
    () => commitDOMChunk(document, root, [{ kind: 'test', value: null, domCost: 1 }], () => connectedLiveNode),
    /detached HTMLDocument/i
  );
  assert.strictEqual(root.firstElementChild, connectedLiveNode);
  assert.equal(root.childElementCount, 1);
});

test('commitDOMChunk rejects invalid nodes and subtree cost mismatches atomically', () => {
  const dom = new JSDOM('<!doctype html><html><body><main id="root"></main></body></html>');
  const document = dom.window.document;
  const root = document.querySelector('#root');
  for (const materialize of [
    (_item, owner) => owner.createTextNode('not an element'),
    (_item, owner) => { const node = owner.createElement('div'); node.append(owner.createElement('span')); return node; },
    () => root
  ]) {
    assert.throws(() => commitDOMChunk(document, root, [{ kind: 'test', value: null, domCost: 1 }], materialize), /element|cost|detached|DOM/i);
    assert.equal(root.childElementCount, 0);
    assert.equal(document.querySelector('#root'), root);
  }
});
