'use strict';

// Parse literals only. URL's IPv6 parser supplies validation/canonicalization;
// strict dotted-decimal parsing avoids URL's legacy integer/octal IPv4 forms.
function addressBits(address) {
  if (typeof address !== 'string' || address.length > 45) return null;
  if (!address.includes(':')) {
    const parts = address.split('.');
    if (parts.length !== 4 || parts.some(p => !/^(0|[1-9]\d{0,2})$/.test(p) || +p > 255)) return null;
    return { width: 32, value: parts.reduce((n, p) => (n << 8n) | BigInt(p), 0n) };
  }
  if (!/^[a-fA-F0-9:.]+$/.test(address)) return null;
  let text;
  try { text = new URL(`http://[${address}]/`).hostname.slice(1, -1); } catch { return null; }
  const halves = text.split('::'), left = halves[0] ? halves[0].split(':') : [], right = halves[1] ? halves[1].split(':') : [];
  const words = halves.length === 2 ? [...left, ...Array(8 - left.length - right.length).fill('0'), ...right] : left;
  const value = words.reduce((n, p) => (n << 16n) | BigInt(`0x${p}`), 0n);
  return value >> 32n === 65535n ? { width: 32, value: value & 0xffffffffn } : { width: 128, value };
}

function networks(cidrs) {
  return cidrs.map(cidr => {
    const [address, prefix] = cidr.split('/'), bits = addressBits(address);
    return { ...bits, shift: BigInt(bits.width - Number(prefix)) };
  });
}
const PRIVATE_NETWORKS = networks(['10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7']);
// Mirrors backend/diagnostic/network_policy.go; an independent parity test reads
// the canonical Go ranges so policy changes cannot silently widen this inference.
const NONPUBLIC_NETWORKS = networks([
  '0.0.0.0/8', '10.0.0.0/8', '100.64.0.0/10', '127.0.0.0/8', '169.254.0.0/16', '172.16.0.0/12',
  '192.0.0.0/24', '192.0.2.0/24', '192.31.196.0/24', '192.52.193.0/24', '192.88.99.0/24', '192.168.0.0/16',
  '192.175.48.0/24', '198.18.0.0/15', '198.51.100.0/24', '203.0.113.0/24', '224.0.0.0/4', '240.0.0.0/4',
  '100.100.100.200/32', '::/128', '::1/128', '64:ff9b::/96', '64:ff9b:1::/48', '100::/64', '100:0:0:1::/64',
  '2001::/23', 'fc00::/7', 'fe80::/10', 'ff00::/8', '2001:db8::/32', '2002::/16', '2620:4f:8000::/48', '3fff::/20', '5f00::/16'
]);
function inNetworks(bits, ranges) {
  return ranges.some(n => n.width === bits.width && bits.value >> n.shift === n.value >> n.shift);
}
function addressClass(address) {
  const bits = addressBits(address);
  if (!bits) return 'other';
  if (inNetworks(bits, PRIVATE_NETWORKS)) return 'private';
  if (inNetworks(bits, NONPUBLIC_NETWORKS)) return 'other';
  return 'public';
}

function annotateASN(topology, retained, keptRoutes, keptPairs) {
  const byID = new Map(topology.nodes.map(n => [n.id, n]));
  const copies = new Map(retained.map(n => [n.id, { ...n }]));
  const evidencePairs = new Set(topology.links.filter(l => l.observations > 0 && ['healthy', 'degraded'].includes(l.status)).map(l => `${l.from}\0${l.to}`));
  const stats = { total: 0, displayed: 0, omitted: 0 };
  const classes = new Map(topology.nodes.map(n => [n.id, addressClass(n.address)]));
  const privateHop = n => n?.kind === 'ip' && classes.get(n.id) === 'private' && ['healthy', 'degraded'].includes(n.status);
  const endpoint = n => n?.kind === 'ip' && n.public_ip === true && classes.get(n.id) === 'public' &&
    ['healthy', 'degraded'].includes(n.status) && Number.isInteger(n.asn?.number) && n.asn.number > 0 && n.asn.number <= 4294967295;
  topology.routes.forEach((route, occurrence) => {
    if (route.complete !== true || keptRoutes[occurrence]?.complete !== true || !['healthy', 'degraded'].includes(route.status)) return;
    const ids = route.node_ids;
    for (let i = 1; i < ids.length - 1;) {
      if (!privateHop(byID.get(ids[i]))) { i++; continue; }
      const start = i;
      while (i < ids.length && privateHop(byID.get(ids[i]))) i++;
      const left = byID.get(ids[start - 1]), right = byID.get(ids[i]);
      if (!endpoint(left) || !endpoint(right) || left.asn.number !== right.asn.number) continue;
      if (ids.slice(start, i + 1).some((id, j) => {
        const pair = `${ids[start + j - 1]}\0${id}`;
        return !keptPairs.has(pair) || !evidencePairs.has(pair);
      })) continue;
      const context = { asn: left.asn.number, route_occurrence: occurrence, result_index: route.result_index, attempt: route.attempt, start, end: i - 1 };
      for (const id of new Set(ids.slice(start, i))) {
        const node = copies.get(id);
        if (!node) continue;
        stats.total++;
        if ((node.asn_contexts?.length ?? 0) < 4 && stats.displayed < 500) {
          (node.asn_contexts ??= []).push(context); stats.displayed++;
        } else { node.asn_contexts_omitted = (node.asn_contexts_omitted ?? 0) + 1; stats.omitted++; }
      }
    }
  });
  return { nodes: [...copies.values()], stats };
}

export const ASN_CONTEXT_DISCLAIMER = '양끝 공인 IP의 ASN이 같아 표시한 경로 문맥이며, 사설 IP의 ASN 소속을 확인한 것은 아닙니다';

export function asnContextLabel(node) {
  const count = node.asn_contexts?.length ?? 0;
  if (!count && !node.asn_contexts_omitted) return '';
  return count === 1 && !node.asn_contexts_omitted
    ? `AS${node.asn_contexts[0].asn} 사이 사설 구간 · 추정` : '경로별 ASN 문맥 · 추정';
}

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
  const annotated = annotateASN(topology, retained, keptRoutes, keptPairs);
  return { nodes: annotated.nodes.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0), links, connectors: keptConnectors,
    routes: keptRoutes,
    stats: { asn_contexts: annotated.stats, nodes: { total: totalNodes, displayed: retained.length, omitted: totalNodes - retained.length },
      links: { total: totalLinks, displayed: links.length + keptConnectors.length, omitted: totalLinks - links.length - keptConnectors.length } } };
}
