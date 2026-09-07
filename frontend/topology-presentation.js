'use strict';

// Presentation only. Never feed these connectors into the observed-link validator
// or producer schema. IDs use route occurrence + run position, not IP identity.
export function presentTopology(topology, { showUnresponsive = true, elementBudget = 1500 } = {}) {
  const byID = new Map(topology.nodes.map(n => [n.id, n]));
  const nodes = new Map(), observed = new Map(), connectors = [], routes = [];
  const spanPositions = new Map();
  const pairs = new Map(topology.links.map(l => [`${l.from}\0${l.to}`, l]));
  const usedIDs = new Set(byID.keys());
  const unique = base => { let id = base; while (usedIDs.has(id)) id += ':'; usedIDs.add(id); return id; };
  topology.routes.forEach((route, occurrence) => {
    const tokens = [];
    for (let i = 0; i < route.node_ids.length;) {
      const id = route.node_ids[i], node = byID.get(id);
      if (node.kind !== 'unknown') { nodes.set(id, node); tokens.push({ id, start: i, end: i }); i++; continue; }
      const start = i;
      while (i < route.node_ids.length && byID.get(route.node_ids[i]).kind === 'unknown') i++;
      const member_ids = route.node_ids.slice(start, i);
      if (!showUnresponsive) continue;
      const groupID = unique(`presentation:unknown:${route.result_index}:${route.attempt}:${occurrence}:${start}`);
      const members = member_ids.map(key => byID.get(key));
      const group = { id: groupID, kind: 'unknown-group', address: '', status: 'unknown', editable: false,
        folded_count: member_ids.length, member_ids, display_label: `무응답 ${member_ids.length}홉 접음`,
        hop_min: Math.min(...members.map(n => n.hop_min ?? start)), hop_max: Math.max(...members.map(n => n.hop_max ?? i - 1)) };
      nodes.set(groupID, group); tokens.push({ id: groupID, start, end: i - 1, group: true });
    }
    for (let i = 1; i < tokens.length; i++) {
      const from = tokens[i-1], to = tokens[i];
      const skipped = to.start - from.end - 1;
      if (skipped > 0) {
        connectors.push({ id: unique(`presentation:bypass:${occurrence}:${i}`), from: from.id, to: to.id,
          kind: 'bypass', status: 'unknown', skipped_hops: skipped,
          label: `무응답 ${skipped}홉 생략 · 직접 연결 관측 아님`, route_occurrence: occurrence });
      } else if (!from.group && !to.group) {
        const key = `${from.id}\0${to.id}`, link = pairs.get(key);
        if (link) observed.set(key, link);
      } else connectors.push({ id: unique(`presentation:fold:${occurrence}:${i}`), from: from.id, to: to.id,
        kind: 'fold', status: 'unknown', label: '연속 무응답 접음 · 구간 표시 (직접 연결 관측 아님)', route_occurrence: occurrence });
      if (skipped > 0 || from.group || to.group) spanPositions.set(`${occurrence}\0${i}`, connectors[connectors.length - 1].id);
    }
    routes.push({ ...route, node_ids: tokens.map(t => t.id) });
  });
  if (!topology.routes.length) {
    for (const node of topology.nodes) nodes.set(node.id, node);
    for (const link of topology.links) observed.set(`${link.from}\0${link.to}`, link);
  }
  // A shared/revisited unknown may expand into multiple occurrence-scoped groups.
  // Bound those as real presentation elements, without manufacturing adjacency.
  const totalNodes = nodes.size, totalLinks = observed.size + connectors.length;
  const retained = [...nodes.values()].slice(0, Math.min(500, Math.max(0, elementBudget - routes.length)));
  const retainedIDs = new Set(retained.map(n => n.id));
  let remaining = Math.min(1000, Math.max(0, elementBudget - routes.length - retained.length));
  const links = [...observed.values()].filter(l => retainedIDs.has(l.from) && retainedIDs.has(l.to)).slice(0, remaining);
  remaining -= links.length;
  const keptConnectors = connectors.filter(l => retainedIDs.has(l.from) && retainedIDs.has(l.to)).slice(0, remaining);
  const keptPairs = new Set(links.map(l => `${l.from}\0${l.to}`));
  const keptSpans = new Set(keptConnectors.map(l => l.id));
  const keptRoutes = routes.map((route, occurrence) => {
    const node_ids = [];
    for (const id of route.node_ids) {
      if (!retainedIDs.has(id)) break;
      if (node_ids.length) {
        const pair = `${node_ids[node_ids.length - 1]}\0${id}`;
        const span = spanPositions.get(`${occurrence}\0${node_ids.length}`);
        if (span ? !keptSpans.has(span) : !keptPairs.has(pair)) break;
      }
      node_ids.push(id);
    }
    // Never filter out a middle group and silently join its neighbours, or
    // advertise a complete route after its occurrence-specific span was cut.
    return { ...route, node_ids, complete: Boolean(route.complete && node_ids.length === route.node_ids.length) };
  });
  return { nodes: retained.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0), links, connectors: keptConnectors,
    routes: keptRoutes,
    stats: { nodes: { total: totalNodes, displayed: retained.length, omitted: totalNodes - retained.length },
      links: { total: totalLinks, displayed: links.length + keptConnectors.length, omitted: totalLinks - links.length - keptConnectors.length } } };
}
