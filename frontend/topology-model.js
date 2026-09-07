'use strict';

import { normalizeCompactTopology } from './state.js';

const DATA_NODES = 500;
const DATA_LINKS = 1000;
const DOCUMENT = 1200;
const CHUNK = 100;
const LABEL_FILE = 1 << 20;
const LABEL_RECORDS = 500;
const LABEL_PAGE = 100;
const LABEL = 256;
const NOTE = 1024;

const DOM_COSTS = Object.freeze({
  documentLimit: DOCUMENT,
  maxChunkInsertion: CHUNK,
  topology: Object.freeze({ chromeReserve: 180, canvas: 1, node: 1, link: 1, route: 1 }),
  geo: Object.freeze({
    fixedReserve: 300,
    canvas: 1,
    accessibleList: 1,
    accessibleItem: 1,
    maxAccessibleItems: 100,
    marker: 0,
    segment: 0,
    arrow: 0
  }),
  labels: Object.freeze({ fixedReserve: 80, shell: 4, row: 1, cells: 3, control: 1 })
});

const STATUS_RANK = Object.freeze({ healthy: 0, degraded: 1, unknown: 2, failure: 3, unreachable: 3 });

function snapshotPlainData(value, path = 'input', seen = new WeakSet()) {
  if (value === null || typeof value !== 'object') return value;
  if (seen.has(value)) throw new TypeError(`${path} must not contain cycles or shared object identities`);
  const isArray = Array.isArray(value);
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== (isArray ? Array.prototype : Object.prototype)) throw new TypeError(`${path} must use a plain prototype`);
  const descriptors = Object.getOwnPropertyDescriptors(value);
  for (const key of Reflect.ownKeys(descriptors)) {
    if (isArray && key === 'length') continue;
    if (!Object.hasOwn(descriptors[key], 'value')) throw new TypeError(`${path} must not contain accessor properties`);
  }
  seen.add(value);
  const copy = isArray ? [] : {};
  for (const key of Object.keys(descriptors)) {
    if (isArray && key === 'length') continue;
    const descriptor = descriptors[key];
    if (descriptor.enumerable) copy[key] = snapshotPlainData(descriptor.value, `${path}.${key}`, seen);
  }
  seen.delete(value);
  return copy;
}

function clonePlain(value) {
  if (Array.isArray(value)) return value.map(clonePlain);
  if (value && typeof value === 'object') {
    const copy = {};
    for (const key of Object.keys(value)) copy[key] = clonePlain(value[key]);
    return copy;
  }
  return value;
}

function compactModel(compact, report) {
  const value = normalizeCompactTopology(compact);
  return {
    source: 'compact',
    nodes: clonePlain(value.nodes),
    links: clonePlain(value.links),
    routes: clonePlain(value.routes).map(route => ({ ...route, address: report.results?.[route.result_index]?.address })),
    limits: clonePlain(value.limits),
    serverStats: clonePlain(value.stats),
    serverResultStats: clonePlain(value.result_stats),
    serverGeo: clonePlain(value.geo),
    serverTruncation: {
      truncated: value.truncated,
      reasons: Object.hasOwn(value, 'truncation_reasons') ? [...value.truncation_reasons] : []
    },
    adapterTruncation: { truncated: false, reasons: [] }
  };
}

function worstStatus(left, right) {
  const normalized = right === 'unreachable' ? 'failure' : (Object.hasOwn(STATUS_RANK, right) ? right : 'unknown');
  if (!left || STATUS_RANK[normalized] > STATUS_RANK[left]) return normalized;
  return left;
}

function parseIPv4(value) {
  const parts = value.split('.');
  if (parts.length !== 4) return null;
  const bytes = [];
  for (const part of parts) {
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return null;
    const byte = Number(part);
    if (byte > 255) return null;
    bytes.push(byte);
  }
  return { text: bytes.join('.'), bytes };
}

function parseIPv6(value) {
  let address = value;
  let zone = '';
  const zoneAt = address.indexOf('%');
  if (zoneAt >= 0) {
    zone = address.slice(zoneAt + 1);
    address = address.slice(0, zoneAt);
    if (!zone) return null;
  }
  if (!address.includes(':') || address.match(/::/g)?.length > 1) return null;
  let halves = address.split('::');
  if (halves.length > 2) return null;
  const parseHalf = (half, allowIPv4) => {
    if (half === '') return [];
    const raw = half.split(':');
    const words = [];
    for (let index = 0; index < raw.length; index++) {
      const part = raw[index];
      if (part.includes('.')) {
        if (!allowIPv4 || index !== raw.length - 1) return null;
        const ipv4 = parseIPv4(part);
        if (!ipv4) return null;
        words.push((ipv4.bytes[0] << 8) | ipv4.bytes[1], (ipv4.bytes[2] << 8) | ipv4.bytes[3]);
      } else {
        if (!/^[0-9a-fA-F]{1,4}$/.test(part)) return null;
        words.push(Number.parseInt(part, 16));
      }
    }
    return words;
  };
  const left = parseHalf(halves[0], halves.length === 1);
  const right = halves.length === 2 ? parseHalf(halves[1], true) : [];
  if (!left || !right) return null;
  let words;
  if (halves.length === 2) {
    const missing = 8 - left.length - right.length;
    if (missing < 1) return null;
    words = [...left, ...Array(missing).fill(0), ...right];
  } else {
    if (left.length !== 8) return null;
    words = left;
  }
  if (words.length !== 8) return null;
  if (words.slice(0, 5).every(word => word === 0) && words[5] === 0xffff) {
    return parseIPv4(`${words[6] >> 8}.${words[6] & 255}.${words[7] >> 8}.${words[7] & 255}`)?.text ?? null;
  }
  let bestStart = -1;
  let bestLength = 0;
  for (let start = 0; start < words.length;) {
    if (words[start] !== 0) { start++; continue; }
    let end = start;
    while (end < words.length && words[end] === 0) end++;
    if (end - start > bestLength && end - start >= 2) [bestStart, bestLength] = [start, end - start];
    start = end;
  }
  let text;
  if (bestStart < 0) text = words.map(word => word.toString(16)).join(':');
  else {
    const before = words.slice(0, bestStart).map(word => word.toString(16)).join(':');
    const after = words.slice(bestStart + bestLength).map(word => word.toString(16)).join(':');
    text = `${before}::${after}`;
  }
  return text + (zone ? `%${zone}` : '');
}

function canonicalIP(value) {
  return parseIPv4(value)?.text ?? parseIPv6(value);
}

function legacyNodeKey(node, resultIndex, attempt, occurrence, nodeIndex) {
  if (nodeIndex === 0 && node.hop === 0) return { key: 'local', kind: 'local', address: node.address || 'local' };
  const address = String(node.address || '').trim();
  if (!address) return { key: `unknown:${resultIndex}:${attempt}:${occurrence}:${node.hop}:${nodeIndex}`, kind: 'unknown', address: '' };
  const ip = canonicalIP(address);
  if (ip !== null) return { key: `ip:${ip}`, kind: 'ip', address: ip };
  const hostname = address.toLowerCase().replace(/\.$/, '');
  return { key: `host:${hostname}`, kind: 'hostname', address: hostname };
}

function legacyRoutes(report) {
  const routes = [];
  for (let resultIndex = 0; resultIndex < (report.results || []).length; resultIndex++) {
    const result = report.results[resultIndex];
    if (!result || result.kind !== 'traceroute' || !result.details) continue;
    let attempts = Array.isArray(result.details.attempts) ? result.details.attempts.map((item, occurrence) => ({ item, occurrence })) : [];
    if (attempts.length === 0 && result.details.topology) {
      attempts = [{ item: { attempt: 1, status: result.status, topology: result.details.topology }, occurrence: 0 }];
    }
    attempts.sort((left, right) => (left.item.attempt - right.item.attempt) || (left.occurrence - right.occurrence));
    for (const { item, occurrence } of attempts) {
      if (!item.topology || !Array.isArray(item.topology.nodes)) continue;
      const ordered = item.topology.nodes.map((node, index) => ({ node, index }))
        .sort((left, right) => (left.node.hop - right.node.hop) || (left.index - right.index));
      if (ordered.length === 0) continue;
      const byID = new Map(ordered.map(entry => [entry.node.id, entry]));
      const outgoing = new Map();
      for (const link of Array.isArray(item.topology.links) ? item.topology.links : []) {
        if (!byID.has(link.from) || !byID.has(link.to) || link.from === link.to) continue;
        const key = `${link.from}\u0000${link.to}`;
        const list = outgoing.get(link.from) ?? [];
        if (!list.some(candidate => candidate.key === key)) list.push({ key, link, target: byID.get(link.to) });
        outgoing.set(link.from, list);
      }
      for (const list of outgoing.values()) {
        list.sort((left, right) => (left.target.node.hop - right.target.node.hop) || (left.target.index - right.target.index));
      }
      const path = [ordered[0]];
      const pathLinks = [];
      const visited = new Set([ordered[0].node.id]);
      for (;;) {
        const current = path.at(-1);
        const next = (outgoing.get(current.node.id) ?? []).find(candidate =>
          !visited.has(candidate.target.node.id) && candidate.target.node.hop > current.node.hop);
        if (!next) break;
        path.push(next.target);
        pathLinks.push(next.link);
        visited.add(next.target.node.id);
      }
      const rawObservations = path.map(({ node }, nodeIndex) => ({ ...legacyNodeKey(node, resultIndex, item.attempt, occurrence, nodeIndex), node }));
      const observations = [];
      const edgeStatuses = [];
      for (let index = 0; index < rawObservations.length; index++) {
        const observation = rawObservations[index];
        if (observations.length && observations.at(-1).key === observation.key) continue;
        observations.push(observation);
        if (observations.length > 1) edgeStatuses.push(pathLinks[index - 1]?.status ?? observation.node.status);
      }
      const maxHop = Math.max(...ordered.map(entry => entry.node.hop));
      const complete = path.at(-1).node.hop === maxHop;
      routes.push({
        result_index: resultIndex,
        address: result.address,
        attempt: item.attempt,
        occurrence,
        status: item.status,
        reached: Boolean(item.topology.reached),
        complete,
        observations,
        edgeStatuses
      });
    }
  }
  return routes;
}

function adaptLegacyTopology(report) {
  const rawRoutes = legacyRoutes(report);
  const allNodeKeys = new Set();
  const allLinkKeys = new Set();
  for (const route of rawRoutes) {
    for (const observation of route.observations) allNodeKeys.add(observation.key);
    for (let index = 1; index < route.observations.length; index++) {
      const from = route.observations[index - 1].key;
      const to = route.observations[index].key;
      if (from !== to) allLinkKeys.add(`${from}\u0000${to}`);
    }
  }

  const selectedKeys = new Set();
  const selectedLinks = new Set();
  const selectedOrder = [];
  const addNode = key => {
    if (selectedKeys.has(key)) return;
    selectedKeys.add(key);
    selectedOrder.push(key);
  };
  const local = rawRoutes.find(route => route.observations[0]?.kind === 'local');
  if (local) addNode(local.observations[0].key);

  const groups = new Map();
  for (const route of rawRoutes) {
    route.accepted = 0;
    route.blocked = false;
    const group = groups.get(route.result_index) ?? [];
    group.push(route);
    groups.set(route.result_index, group);
  }
  const cursors = new Map([...groups.keys()].map(key => [key, 0]));
  let nodeLimited = false;
  let linkLimited = false;
  for (;;) {
    let active = false;
    for (const resultIndex of [...groups.keys()].sort((left, right) => left - right)) {
      const group = groups.get(resultIndex);
      let route = null;
      for (let scanned = 0; scanned < group.length; scanned++) {
        const position = (cursors.get(resultIndex) + scanned) % group.length;
        const candidate = group[position];
        if (!candidate.blocked && candidate.accepted < candidate.observations.length - 1) {
          route = candidate;
          cursors.set(resultIndex, (position + 1) % group.length);
          break;
        }
      }
      if (!route) continue;
      active = true;
      const from = route.observations[route.accepted].key;
      const to = route.observations[route.accepted + 1].key;
      const edgeKey = `${from}\u0000${to}`;
      const nodeCost = Number(!selectedKeys.has(from)) + Number(!selectedKeys.has(to));
      const linkCost = Number(!selectedLinks.has(edgeKey));
      if (selectedKeys.size + nodeCost > DATA_NODES) {
        route.blocked = true;
        nodeLimited = true;
        continue;
      }
      if (selectedLinks.size + linkCost > DATA_LINKS) {
        route.blocked = true;
        linkLimited = true;
        continue;
      }
      addNode(from);
      addNode(to);
      selectedLinks.add(edgeKey);
      route.accepted++;
    }
    if (!active) break;
  }
  const selectedRoutes = rawRoutes.filter(route => route.accepted > 0 ||
    (route.observations.length === 1 && selectedKeys.has(route.observations[0].key)));

  const aggregates = new Map();
  const linkAggregates = new Map();
  for (const route of selectedRoutes) {
    const nodeCount = route.accepted > 0 ? route.accepted + 1 : 1;
    for (const observation of route.observations.slice(0, nodeCount)) {
      let aggregate = aggregates.get(observation.key);
      if (!aggregate) {
        aggregate = {
          kind: observation.kind, address: observation.address, status: '',
          hop_min: observation.node.hop, hop_max: observation.node.hop,
          observations: 0, latencyTotal: 0, latencyCount: 0,
          public_ip: false, geolocation: undefined, asn: undefined
        };
        aggregates.set(observation.key, aggregate);
      }
      aggregate.status = worstStatus(aggregate.status, observation.node.status);
      aggregate.hop_min = Math.min(aggregate.hop_min, observation.node.hop);
      aggregate.hop_max = Math.max(aggregate.hop_max, observation.node.hop);
      aggregate.observations++;
      if (observation.node.hop > 0 && Number.isFinite(observation.node.latency_ms) && observation.node.latency_ms >= 0) {
        aggregate.latencyTotal += observation.node.latency_ms;
        aggregate.latencyCount++;
      }
      aggregate.public_ip ||= Boolean(observation.node.public_ip);
      if (!aggregate.geolocation && observation.node.geolocation) aggregate.geolocation = clonePlain(observation.node.geolocation);
      if (!aggregate.asn && observation.node.asn) aggregate.asn = clonePlain(observation.node.asn);
    }
    for (let index = 1; index <= route.accepted; index++) {
      const from = route.observations[index - 1];
      const to = route.observations[index];
      if (from.key === to.key) continue;
      const key = `${from.key}\u0000${to.key}`;
      let aggregate = linkAggregates.get(key);
      if (!aggregate) {
        aggregate = { from: from.key, to: to.key, status: '', observations: 0 };
        linkAggregates.set(key, aggregate);
      }
      aggregate.status = worstStatus(aggregate.status, route.edgeStatuses[index - 1]);
      aggregate.observations++;
    }
  }

  const ids = new Map();
  const nodeKeys = selectedOrder.filter(key => aggregates.has(key));
  const nodes = nodeKeys.map((key, index) => {
    ids.set(key, `l${String(index + 1).padStart(6, '0')}`);
    const aggregate = aggregates.get(key);
    const node = {
      id: `l${String(index + 1).padStart(6, '0')}`, kind: aggregate.kind, address: aggregate.address,
      status: aggregate.status || 'unknown', hop_min: aggregate.hop_min, hop_max: aggregate.hop_max,
      observations: aggregate.observations
    };
    if (aggregate.latencyCount) node.latency_ms_avg = aggregate.latencyTotal / aggregate.latencyCount;
    if (aggregate.public_ip) node.public_ip = true;
    if (aggregate.geolocation) node.geolocation = aggregate.geolocation;
    if (aggregate.asn) node.asn = aggregate.asn;
    return node;
  });
  const links = [...linkAggregates.values()].map(link => ({ from: ids.get(link.from), to: ids.get(link.to), status: link.status || 'unknown', observations: link.observations }));
  const routes = selectedRoutes.map(route => {
    const nodeCount = route.accepted > 0 ? route.accepted + 1 : 1;
    return {
      result_index: route.result_index, attempt: route.attempt, address: route.address, status: route.status,
      reached: route.reached, complete: Boolean(route.complete && route.accepted === route.observations.length - 1),
      node_ids: route.observations.slice(0, nodeCount).map(item => ids.get(item.key))
    };
  });
  const reasons = [];
  if (nodeLimited) reasons.push('node_limit');
  if (linkLimited) reasons.push('link_limit');
  return {
    source: 'legacy', nodes, links, routes,
    limits: { nodes: DATA_NODES, links: DATA_LINKS },
    serverStats: null, serverResultStats: [], serverGeo: null,
    serverTruncation: { truncated: false, reasons: [] },
    adapterTruncation: { truncated: reasons.length > 0, reasons },
    adapterStats: {
      nodes: { total: allNodeKeys.size, displayed: nodes.length, omitted: allNodeKeys.size - nodes.length },
      links: { total: allLinkKeys.size, displayed: links.length, omitted: allLinkKeys.size - links.length },
      routes: { total: rawRoutes.length, displayed: routes.length, omitted: rawRoutes.length - routes.length }
    }
  };
}

function topologyModelFromReport(input) {
  const report = snapshotPlainData(input, 'report');
  if (!report || typeof report !== 'object' || Array.isArray(report)) throw new TypeError('report must be an object');
  if (report.compact_topology !== undefined && report.compact_topology !== null) return compactModel(report.compact_topology, report);
  return adaptLegacyTopology(report);
}

function filterTopologyModel(inputModel, selectedResultIndexes, { showUnresponsive = true } = {}) {
  const model = snapshotPlainData(inputModel, 'model');
  const selected = new Set(selectedResultIndexes ?? []);
  const nodeByID = new Map((Array.isArray(model.nodes) ? model.nodes : []).map(node => [node.id, node]));
  const selectedRoutes = (Array.isArray(model.routes) ? model.routes : [])
    .filter(route => selected.has(route.result_index));
  const totalNodeIDs = new Set(selectedRoutes.flatMap(route => route.node_ids));
  const totalLinkKeys = new Set(selectedRoutes.flatMap(route => route.node_ids.slice(1)
    .map((id, index) => `${route.node_ids[index]}\u0000${id}`)));
  const routes = [];
  for (const route of selectedRoutes) {
    let node_ids = [...route.node_ids];
    if (!showUnresponsive) {
      const unknownAt = node_ids.findIndex(id => nodeByID.get(id)?.kind === 'unknown');
      if (unknownAt >= 0) node_ids = node_ids.slice(0, unknownAt);
    }
    if (node_ids.length === 0) continue;
    routes.push({ ...route, complete: Boolean(route.complete && node_ids.length === route.node_ids.length), node_ids });
  }
  const nodeIDs = new Set(routes.flatMap(route => route.node_ids));
  const linkKeys = new Set(routes.flatMap(route => route.node_ids.slice(1)
    .map((id, index) => `${route.node_ids[index]}\u0000${id}`)));
  return {
    ...model,
    nodes: (model.nodes ?? []).filter(node => nodeIDs.has(node.id)),
    links: (model.links ?? []).filter(link => linkKeys.has(`${link.from}\u0000${link.to}`)),
    routes,
    viewTotals: {
      nodes: totalNodeIDs.size,
      links: (model.links ?? []).filter(link => totalLinkKeys.has(`${link.from}\u0000${link.to}`)).length,
      routes: selectedRoutes.length
    }
  };
}

function boundedInteger(value, fallback, minimum, maximum) {
  if (value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new TypeError('plan limits must be finite numbers');
  return Math.min(maximum, Math.max(minimum, Math.floor(value)));
}

function fairRouteOrder(routes) {
  const groups = new Map();
  for (const route of routes) {
    if (!groups.has(route.result_index)) groups.set(route.result_index, []);
    groups.get(route.result_index).push(route);
  }
  const resultIndexes = [...groups.keys()].sort((left, right) => left - right);
  for (const group of groups.values()) group.sort((left, right) => (left.attempt - right.attempt) || (left.order - right.order));
  const ordered = [];
  for (let offset = 0; ; offset++) {
    let added = false;
    for (const resultIndex of resultIndexes) {
      const route = groups.get(resultIndex)[offset];
      if (route) { ordered.push(route); added = true; }
    }
    if (!added) return ordered;
  }
}

function planTopologyDOM(inputModel, inputOptions = {}) {
  const model = snapshotPlainData(inputModel, 'model');
  const options = snapshotPlainData(inputOptions, 'options');
  if (!model || typeof model !== 'object' || Array.isArray(model)) throw new TypeError('model must be an object');
  const view = options.view === undefined ? 'topology' : options.view;
  if (!['topology', 'geo', 'labels'].includes(view)) throw new TypeError('view must be topology, geo, or labels');
  const existingDOMElements = boundedInteger(options.existingDOMElements, 0, 0, DOCUMENT);
  const documentRemaining = Math.max(0, DOCUMENT - existingDOMElements);
  const explicitAvailable = options.availableElements === undefined
    ? documentRemaining
    : boundedInteger(options.availableElements, documentRemaining, 0, DOCUMENT);
  const availableElements = Math.min(explicitAvailable, documentRemaining);
  const explicitChunkLimit = options.maxDOMPerChunk === undefined
    ? options.maxNodesPerChunk
    : options.maxDOMPerChunk;
  if (explicitChunkLimit !== undefined &&
      (typeof explicitChunkLimit !== 'number' || !Number.isFinite(explicitChunkLimit))) {
    throw new TypeError('maxDOMPerChunk must be a finite number');
  }
  if (explicitChunkLimit !== undefined && Math.floor(explicitChunkLimit) < DOM_COSTS.labels.row + DOM_COSTS.labels.cells + DOM_COSTS.labels.control) {
    throw new TypeError('maxDOMPerChunk must be at least 5 DOM elements');
  }
  const chunkSize = explicitChunkLimit === undefined ? CHUNK : Math.min(CHUNK, Math.floor(explicitChunkLimit));
  const requestedReserve = view === 'topology'
    ? DOM_COSTS.topology.chromeReserve
    : DOM_COSTS[view].fixedReserve;
  const reserve = requestedReserve;
  const renderable = documentRemaining >= requestedReserve;
  const dynamicBudget = Math.max(0, availableElements - requestedReserve);

  const rawNodes = Array.isArray(model.nodes) ? model.nodes : [];
  const rawLinks = Array.isArray(model.links) ? model.links : [];
  const rawRoutes = Array.isArray(model.routes) ? model.routes : [];
  const dataNodes = [];
  const dataNodeIDs = new Set();
  for (const node of rawNodes) {
    if (dataNodes.length >= DATA_NODES) break;
    if (!node || typeof node.id !== 'string' || dataNodeIDs.has(node.id)) continue;
    dataNodeIDs.add(node.id);
    dataNodes.push(node);
  }
  const dataLinks = [];
  for (const link of rawLinks) {
    if (dataLinks.length >= DATA_LINKS) break;
    if (!link || !dataNodeIDs.has(link.from) || !dataNodeIDs.has(link.to)) continue;
    dataLinks.push(link);
  }
  const dataRoutes = rawRoutes.slice(0, 200).map((route, order) => ({ ...route, order }))
    .filter(route => Array.isArray(route.node_ids) && route.node_ids.length > 0 && route.node_ids.every(id => dataNodeIDs.has(id)));

  const nodeByID = new Map(dataNodes.map(node => [node.id, node]));
  const linkByPair = new Map();
  for (const link of dataLinks) {
    const key = `${link.from}\u0000${link.to}`;
    if (!linkByPair.has(key)) linkByPair.set(key, link);
  }
  const hasGeo = node => node?.public_ip === true && Boolean(node.geolocation &&
    Number.isFinite(node.geolocation.latitude) && Number.isFinite(node.geolocation.longitude));

  const selectedNodeIDs = new Set();
  const selectedLinkKeys = new Set();
  const selectedNodes = [];
  const selectedLinks = [];
  const selectedRoutes = [];
  const markers = [];
  const segments = [];
  const arrows = [];
  let topologyCost = 0;
  const topologyBudget = view === 'topology'
    ? Math.max(0, dynamicBudget - DOM_COSTS.topology.canvas)
    : Number.POSITIVE_INFINITY;
  const canAdd = cost => topologyCost + cost <= topologyBudget;
  const addNode = id => {
    if (selectedNodeIDs.has(id)) return;
    const node = nodeByID.get(id);
    selectedNodeIDs.add(id);
    selectedNodes.push(clonePlain(node));
    topologyCost += DOM_COSTS.topology.node;
    if (hasGeo(node) && markers.length < DATA_NODES) {
      markers.push({ node_id: id, latitude: node.geolocation.latitude, longitude: node.geolocation.longitude });
    }
  };
  const addLink = (from, to) => {
    const key = `${from}\u0000${to}`;
    if (selectedLinkKeys.has(key)) return;
    const link = linkByPair.get(key);
    if (!link) return;
    selectedLinkKeys.add(key);
    selectedLinks.push(clonePlain(link));
    topologyCost += DOM_COSTS.topology.link;
  };

  const orderedRoutes = fairRouteOrder(dataRoutes);
  const routeStates = [];
  let missingRouteLink = false;
  for (const route of orderedRoutes) {
    const firstID = route.node_ids[0];
    const cost = DOM_COSTS.topology.route + (selectedNodeIDs.has(firstID) ? 0 : DOM_COSTS.topology.node);
    if (!canAdd(cost)) continue;
    addNode(firstID);
    const plannedRoute = {
      result_index: route.result_index, attempt: route.attempt, address: route.address, status: route.status,
      reached: Boolean(route.reached), complete: Boolean(route.complete && route.node_ids.length === 1), node_ids: [firstID]
    };
    selectedRoutes.push(plannedRoute);
    topologyCost += DOM_COSTS.topology.route;
    routeStates.push({ route, plannedRoute, cursor: 1, blocked: false });
  }

  for (;;) {
    let progressed = false;
    for (const state of routeStates) {
      if (state.blocked || state.cursor >= state.route.node_ids.length) continue;
      const from = state.route.node_ids[state.cursor - 1];
      const to = state.route.node_ids[state.cursor];
      const node = nodeByID.get(to);
      const linkKey = `${from}\u0000${to}`;
      if (!linkByPair.has(linkKey)) {
        state.blocked = true;
        missingRouteLink = true;
        continue;
      }
      const newNode = !selectedNodeIDs.has(to);
      const newLink = !selectedLinkKeys.has(linkKey);
      const geoEdge = hasGeo(nodeByID.get(from)) && hasGeo(node) && segments.length < DATA_LINKS;
      const cost = (newNode ? DOM_COSTS.topology.node : 0) + (newLink ? DOM_COSTS.topology.link : 0);
      if (!canAdd(cost)) { state.blocked = true; continue; }
      if (newNode) addNode(to);
      if (newLink) addLink(from, to);
      if (geoEdge && selectedLinkKeys.has(linkKey)) {
        segments.push({ from, to });
        arrows.push({ from, to });
      }
      state.plannedRoute.node_ids.push(to);
      state.cursor++;
      state.plannedRoute.complete = Boolean(state.route.complete && state.cursor === state.route.node_ids.length);
      progressed = true;
    }
    if (!progressed) break;
  }

  if (dataRoutes.length === 0) {
    for (const node of dataNodes) {
      const cost = DOM_COSTS.topology.node;
      if (!canAdd(cost)) break;
      addNode(node.id);
    }
    for (const link of dataLinks) {
      if (!selectedNodeIDs.has(link.from) || !selectedNodeIDs.has(link.to) || !canAdd(DOM_COSTS.topology.link)) continue;
      addLink(link.from, link.to);
    }
  }

  const viewTotals = model.viewTotals && typeof model.viewTotals === 'object' ? model.viewTotals : {};
  const nodeTotal = Math.max(dataNodes.length, boundedInteger(viewTotals.nodes, dataNodes.length, 0, DATA_NODES));
  const linkTotal = Math.max(dataLinks.length, boundedInteger(viewTotals.links, dataLinks.length, 0, DATA_LINKS));
  const routeTotal = Math.max(dataRoutes.length, boundedInteger(viewTotals.routes, dataRoutes.length, 0, 200));
  const viewStats = {
    nodes: { total: nodeTotal, displayed: selectedNodes.length, omitted: nodeTotal - selectedNodes.length },
    links: { total: linkTotal, displayed: selectedLinks.length, omitted: linkTotal - selectedLinks.length },
    routes: { total: routeTotal, displayed: selectedRoutes.length, omitted: routeTotal - selectedRoutes.length }
  };
  const partialRoute = selectedRoutes.some(route => !route.complete);
  const reasons = [];
  if (rawNodes.length > DATA_NODES) reasons.push('data_node_limit');
  if (rawLinks.length > DATA_LINKS) reasons.push('data_link_limit');
  if (Object.values(viewStats).some(stats => stats.omitted > 0) || partialRoute) reasons.push('element_budget');
  if (missingRouteLink) reasons.push('missing_link');
  if (!renderable) reasons.push('dom_budget');

  const labelInput = Array.isArray(options.labelRecords) ? options.labelRecords : [];
  const normalizedLabels = labelInput.map((record, order) => {
    if (!record || typeof record !== 'object' || Array.isArray(record)) return null;
    const node_id = typeof record.node_id === 'string' ? record.node_id : '';
    const label = typeof record.label === 'string' ? record.label.slice(0, LABEL) : '';
    const note = typeof record.note === 'string' ? record.note.slice(0, NOTE) : '';
    if (!node_id || (!label && !note)) return null;
    return { node_id, label, note, order };
  }).filter(Boolean).sort((left, right) => left.node_id.localeCompare(right.node_id) ||
    left.label.localeCompare(right.label) || left.note.localeCompare(right.note) || left.order - right.order);
  const labelsTruncated = normalizedLabels.length > LABEL_RECORDS;
  const labelRecords = normalizedLabels.slice(0, LABEL_RECORDS).map(({ order: _order, ...record }) => record);

  const mountItems = [];
  let mountCost = 0;
  const mount = (kind, value, domCost) => {
    if (!renderable) return false;
    if (mountCost + domCost > dynamicBudget) return false;
    mountItems.push({ kind, value, domCost });
    mountCost += domCost;
    return true;
  };
  if (view === 'topology') {
    mount('topology-canvas', null, DOM_COSTS.topology.canvas);
    for (const value of selectedNodes) mount('topology-node', value, DOM_COSTS.topology.node);
    for (const value of selectedLinks) mount('topology-link', value, DOM_COSTS.topology.link);
    for (const value of selectedRoutes) mount('topology-route', value, DOM_COSTS.topology.route);
  } else if (view === 'geo') {
    mount('geo-canvas', null, DOM_COSTS.geo.canvas);
    if (mount('geo-accessible-list', null, DOM_COSTS.geo.accessibleList)) {
      for (const value of markers.slice(0, DOM_COSTS.geo.maxAccessibleItems)) {
        if (!mount('geo-accessible-item', value, DOM_COSTS.geo.accessibleItem)) break;
      }
    }
  } else if (mount('label-shell', null, DOM_COSTS.labels.shell)) {
    const rowCost = DOM_COSTS.labels.row + DOM_COSTS.labels.cells + DOM_COSTS.labels.control;
    for (const value of labelRecords.slice(0, LABEL_PAGE)) {
      if (!mount('label-row', value, rowCost)) break;
    }
  }

  const chunks = [];
  let chunk = [];
  let chunkCost = 0;
  for (const item of mountItems) {
    if (chunk.length && chunkCost + item.domCost > chunkSize) {
      chunks.push(chunk);
      chunk = [];
      chunkCost = 0;
    }
    chunk.push(item);
    chunkCost += item.domCost;
  }
  if (chunk.length) chunks.push(chunk);
  const plannedElements = mountCost;
  const semanticCounts = {
    topology: { nodes: selectedNodes.length, links: selectedLinks.length, routes: selectedRoutes.length },
    geo: { markers: markers.length, segments: segments.length, arrows: arrows.length },
    labels: { records: labelRecords.length }
  };
  const estimatedDOMElements = renderable ? existingDOMElements + reserve + mountCost : existingDOMElements;
  return {
    view, renderable,
    topology: { nodes: selectedNodes, links: selectedLinks, routes: selectedRoutes },
    geo: { markers, segments, arrows },
    labels: {
      maxFileBytes: LABEL_FILE, maxRecords: LABEL_RECORDS, pageSize: LABEL_PAGE,
      maxLabelChars: LABEL, maxNoteChars: NOTE, records: labelRecords
    },
    mountItems, semanticCounts, chunks, plannedElements, availableElements,
    existingDOMElements, reserveElements: reserve, estimatedDOMElements,
    serverTruncation: clonePlain(model.serverTruncation || { truncated: false, reasons: [] }),
    adapterTruncation: clonePlain(model.adapterTruncation || { truncated: false, reasons: [] }),
    viewStats,
    viewTruncation: { truncated: reasons.length > 0, reasons },
    labelTruncation: {
      truncated: labelsTruncated || labelRecords.length > mountItems.filter(item => item.kind === 'label-row').length,
      total: normalizedLabels.length,
      retained: labelRecords.length,
      mounted: mountItems.filter(item => item.kind === 'label-row').length,
      reasons: [...(labelsTruncated ? ['record_limit'] : []),
        ...(view === 'labels' && labelRecords.length > mountItems.filter(item => item.kind === 'label-row').length ? ['page_or_element_budget'] : [])]
    }
  };
}

/**
 * Validate and commit one planned DOM chunk atomically.
 *
 * `materialize(item, detachedDocument)` is a trusted internal callback contract:
 * it must be side-effect-free and return a newly created, detached Element owned
 * by the supplied detached HTMLDocument. Closure side effects cannot be
 * contained, so this callback boundary is not a security boundary.
 *
 * @param {Document} document live destination document
 * @param {Element} root live destination element
 * @param {Array<{domCost: number}>} inputChunk planned items
 * @param {(item: object, detachedDocument: Document) => Element} materialize trusted materializer
 * @returns {number} number of inserted DOM elements
 */
function commitDOMChunk(document, root, inputChunk, materialize) {
  if (!document || typeof document.createDocumentFragment !== 'function' ||
      typeof document.implementation?.createHTMLDocument !== 'function' || typeof document.importNode !== 'function') {
    throw new TypeError('invalid document');
  }
  if (!root || root.ownerDocument !== document || typeof root.append !== 'function') throw new TypeError('invalid DOM root');
  if (!Array.isArray(inputChunk)) throw new TypeError('DOM chunk must be an array');
  if (typeof materialize !== 'function') throw new TypeError('materialize must be a function');

  const detachedDocument = document.implementation.createHTMLDocument('');
  const stagingFragment = detachedDocument.createDocumentFragment();
  let declaredCost = 0;
  for (const item of inputChunk) {
    if (!item || !Number.isInteger(item.domCost) || item.domCost < 1) throw new TypeError('invalid declared DOM cost');
    const node = materialize(item, detachedDocument);
    if (!node || node.nodeType !== 1 || node.ownerDocument !== detachedDocument || node.parentNode !== null) {
      throw new TypeError('materialize must return a detached HTMLDocument Element');
    }
    const actualCost = 1 + node.getElementsByTagName('*').length;
    if (actualCost !== item.domCost) throw new Error(`declared DOM cost ${item.domCost} does not match actual subtree cost ${actualCost}`);
    declaredCost += item.domCost;
    stagingFragment.append(node);
  }
  const actualInsertion = stagingFragment.querySelectorAll('*').length;
  if (actualInsertion !== declaredCost) throw new Error('DOM chunk declared/actual cost mismatch');
  if (actualInsertion > DOM_COSTS.maxChunkInsertion) throw new Error('DOM chunk insertion exceeds budget');
  const currentElements = document.getElementsByTagName('*').length;
  if (currentElements + actualInsertion > DOM_COSTS.documentLimit) throw new Error('document DOM budget exceeded');

  const liveFragment = document.createDocumentFragment();
  for (const node of [...stagingFragment.childNodes]) liveFragment.append(document.importNode(node, true));
  if (liveFragment.querySelectorAll('*').length !== actualInsertion) throw new Error('imported DOM chunk cost mismatch');
  root.append(liveFragment);
  return actualInsertion;
}

export {
  DATA_NODES, DATA_LINKS, DOCUMENT, CHUNK,
  LABEL_FILE, LABEL_RECORDS, LABEL_PAGE, LABEL, NOTE, DOM_COSTS,
  canonicalIP, topologyModelFromReport, filterTopologyModel, planTopologyDOM, commitDOMChunk
};
