const targetsEl = document.querySelector('#targets');
const form = document.querySelector('#check-form');
const errorEl = document.querySelector('#error');
const reportSection = document.querySelector('#report-section');
let currentReport;
let currentTopologyReport;
let selectedTopologyTargets = new Set();
let showUnresponsiveTopologyNodes = true;
let geoRouteMap;
let geoRouteBounds;
const CARTO_BASE_MAP_STORAGE_KEY = 'checknetwork.carto-base-map.v1';
const DEFAULT_CARTO_TILE_URL = 'https://{s}.basemaps.cartocdn.com/rastertiles/dark_all/{z}/{x}/{y}{r}.png';
let cartoBaseMap = loadCartoBaseMap();
const IP_LABEL_STORAGE_KEY = 'checknetwork.ip-labels.v1';
const AGGREGATE_LABEL_STORAGE_KEY = 'checknetwork.aggregate-labels.v1';
let ipLabels = loadIPLabels();
let aggregateLabels = loadAggregateLabels();

const placeholders = {
  dns: 'example.com', tcp: '1.1.1.1:443', http: 'http://example.com', https: 'https://example.com', traceroute: 'example.com',
  ssh: 'example.com', smtp: 'mail.example.com', submission: 'mail.example.com', smtps: 'mail.example.com',
  imap: 'mail.example.com', imaps: 'mail.example.com', pop3: 'mail.example.com', pop3s: 'mail.example.com'
};

function addTarget(kind = 'dns', address = '') {
  const row = document.querySelector('#target-template').content.firstElementChild.cloneNode(true);
  const select = row.querySelector('select');
  const input = row.querySelector('input');
  const expected = row.querySelector('.expected');
  select.value = kind;
  input.value = address;
  const sync = () => {
    input.placeholder = placeholders[select.value];
    expected.hidden = !['http', 'https'].includes(select.value);
  };
  select.addEventListener('change', sync);
  row.querySelector('button').addEventListener('click', () => {
    if (targetsEl.children.length > 1) row.remove();
  });
  sync();
  targetsEl.append(row);
}

function collectTargets() {
  return [...targetsEl.children].map(row => {
    const kind = row.querySelector('select').value;
    const target = { kind, address: row.querySelector('input').value.trim() };
    if (['http', 'https'].includes(kind)) target.expected_status = Number(row.querySelector('.expected').value);
    return target;
  });
}

function render(report) {
  const statusText = { healthy: '정상', degraded: '일부 장애', unreachable: '연결 불가' };
  document.querySelector('#summary').innerHTML = `
    <div class="status ${report.status}"><span></span>${statusText[report.status]}</div>
    <dl><div><dt>전체 검사</dt><dd>${report.summary.total}</dd></div><div><dt>성공</dt><dd>${report.summary.passed}</dd></div><div><dt>실패</dt><dd>${report.summary.failed}</dd></div><div><dt>전체 소요</dt><dd>${report.duration_ms} ms</dd></div></dl>`;
  document.querySelector('#results').innerHTML = report.results.map(result => `
    <article class="result">
      <span class="kind">${result.kind.toUpperCase()}</span>
      <div><h3>${escapeHTML(result.address)}</h3><p>${escapeHTML(result.message || '연결과 응답이 정상입니다.')}</p></div>
      <strong class="${result.status}">${result.status === 'healthy' ? 'PASS' : 'FAIL'}</strong>
      <time>${result.latency_ms} ms</time>
      ${renderTopology(result.details?.topology)}
    </article>`).join('');
  reportSection.hidden = false;
}

function renderStandaloneTopology(report) {
  const allResults = report.results || [];
  const results = allResults.flatMap((result, index) => selectedTopologyTargets.has(index) ? [{
    ...result, _routeIndex: index
  }] : []);
  const attempts = results.flatMap(result => topologyAttempts(result));
  const reached = attempts.filter(attempt => attempt.topology?.reached).length;
  const failed = attempts.length - reached;
  const observedNodes = attempts.flatMap(attempt => attempt.topology?.nodes || []);
  const uniqueNodes = new Set(observedNodes.map(node => node.address).filter(address => address && address !== 'local')).size;
  const maxHop = Math.max(0, ...observedNodes.map(node => Number(node.hop) || 0));
  const selectedStatus = !attempts.length ? 'unknown' : failed === attempts.length ? 'unreachable' : failed ? 'degraded' : 'healthy';
  const statusText = { healthy: '모든 경로 정상', degraded: '일부 경로 확인 필요', unreachable: '목적지 미도달', unknown: '목적지를 선택하세요' };
  document.querySelector('#topology-result-summary').innerHTML = `
    <div class="status ${selectedStatus}"><span></span>${statusText[selectedStatus]}</div>
    <dl><div><dt>목적지</dt><dd>${results.length}</dd></div><div><dt>총 실행</dt><dd>${attempts.length}</dd></div><div><dt>도달</dt><dd>${reached}</dd></div><div><dt>미도달</dt><dd>${failed}</dd></div><div class="metric-hop"><dt>최대 홉 단계</dt><dd><small>HOP</small> ${maxHop}</dd></div><div class="metric-node"><dt>고유 응답 노드</dt><dd><small>NODE</small> ${uniqueNodes}</dd></div><div><dt>전체 소요</dt><dd>${report.duration_ms} ms</dd></div></dl>`;
  renderTopologyTargetFilter(allResults);
  document.querySelector('#topology-result').innerHTML = renderTopologyMap(results, ipLabels, aggregateLabels, showUnresponsiveTopologyNodes) || '<p class="empty-topology">표시할 목적지를 한 개 이상 선택해 주세요.</p>';
  enableTopologyDragging(document.querySelector('#topology-result'));
  renderGeoRouteMap(report);
  document.querySelector('#topology-report-section').hidden = false;
}

function geoPoint(node, groupIndex, attempt) {
  const geo = node?.geolocation;
  const latitude = Number(geo?.latitude); const longitude = Number(geo?.longitude);
  if (!node?.public_ip || !Number.isFinite(latitude) || !Number.isFinite(longitude) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180) return null;
  return { ...node, latitude, longitude, groupIndex, attempt };
}

function normalizeLongitude(longitude) {
  return ((longitude + 540) % 360) - 180;
}

function geoRouteCoordinates(points, anchorLongitude = points[0]?.longitude) {
  if (!points.length) return [];
  const coordinates = [[points[0].latitude, anchorLongitude + normalizeLongitude(points[0].longitude - anchorLongitude)]];
  points.slice(1).forEach((point, index) => {
    const previous = points[index];
    const previousLongitude = coordinates[index][1];
    coordinates.push([point.latitude, previousLongitude + normalizeLongitude(point.longitude - previous.longitude)]);
  });
  return coordinates;
}

function geoRouteSegment(map, from, to) {
  const longitudeDelta = normalizeLongitude(to.longitude - from.longitude);
  if (Math.abs(to.latitude - from.latitude) < 1e-9 && Math.abs(longitudeDelta) < 1e-9) return null;
  const fromPoint = map.latLngToLayerPoint([from.latitude, from.longitude]);
  const toPoint = map.latLngToLayerPoint([to.latitude, from.longitude + longitudeDelta]);
  const midpointPoint = { x: (fromPoint.x + toPoint.x) / 2, y: (fromPoint.y + toPoint.y) / 2 };
  const midpoint = map.layerPointToLatLng(midpointPoint);
  const distance = Math.hypot(toPoint.x - fromPoint.x, toPoint.y - fromPoint.y);
  const unit = { x: (toPoint.x - fromPoint.x) / distance, y: (toPoint.y - fromPoint.y) / distance };
  const perpendicular = { x: -unit.y, y: unit.x };
  const arrowPoint = (forward, sideways) => {
    const point = {
      x: midpointPoint.x + unit.x * forward + perpendicular.x * sideways,
      y: midpointPoint.y + unit.y * forward + perpendicular.y * sideways
    };
    const latLng = map.layerPointToLatLng(point);
    return [latLng.lat, latLng.lng];
  };
  return {
    midpoint: [midpoint.lat, midpoint.lng],
    cssRotation: Math.atan2(toPoint.y - fromPoint.y, toPoint.x - fromPoint.x) * 180 / Math.PI,
    arrow: [arrowPoint(11, 0), arrowPoint(2, 6), arrowPoint(2, 2), arrowPoint(-11, 2), arrowPoint(-11, -2), arrowPoint(2, -2), arrowPoint(2, -6)]
  };
}

function loadCartoBaseMap() {
  const runtimeValue = String(window.CHECKNETWORK_CONFIG?.CARTO_BASE_MAP || '').trim();
  if (runtimeValue) return { value: runtimeValue, source: 'environment' };
  try {
    const sessionValue = String(sessionStorage.getItem(CARTO_BASE_MAP_STORAGE_KEY) || '').trim();
    if (sessionValue) return { value: sessionValue, source: 'session' };
  } catch (_) { /* Storage can be disabled by browser policy. */ }
  return { value: '', source: 'default' };
}

function cartoTileURL(value = cartoBaseMap.value) {
  const configured = String(value || '').trim();
  if (/^https:\/\//i.test(configured)) return configured;
  if (!configured) return DEFAULT_CARTO_TILE_URL;
  const separator = DEFAULT_CARTO_TILE_URL.includes('?') ? '&' : '?';
  return `${DEFAULT_CARTO_TILE_URL}${separator}key=${encodeURIComponent(configured)}`;
}

function renderCartoBaseMapStatus(message = '') {
  const status = document.querySelector('#carto-base-map-status');
  if (!status) return;
  const sourceText = { environment: '배포 환경의 CARTO 베이스맵 키 적용됨', session: '현재 탭의 CARTO 베이스맵 키 적용됨', default: 'CARTO 키 미설정 · 워터마크가 표시될 수 있음' };
  status.textContent = message || sourceText[cartoBaseMap.source];
  status.className = `carto-base-map-status ${cartoBaseMap.source === 'default' ? 'degraded' : 'healthy'}`;
}

function renderGeoRouteMap(report = currentTopologyReport) {
  const root = document.querySelector('#geo-map-result');
  if (!root) return;
  if (geoRouteMap) { geoRouteMap.remove(); geoRouteMap = undefined; geoRouteBounds = undefined; }
  const groups = (report?.results || []).map((result, groupIndex) => ({
    address: result.address, groupIndex, color: routeColor(groupIndex),
    routes: topologyAttempts(result).map(attempt => (attempt.topology?.nodes || []).map(node => geoPoint(node, groupIndex, attempt.attempt)).filter(Boolean)).filter(route => route.length)
  }));
  const routes = groups.flatMap(group => group.routes.map(points => ({ ...group, points })));
  const points = routes.flatMap(route => route.points);
  if (!points.length) {
    root.innerHTML = `<p class="empty-topology">${report ? '위치가 식별된 공인 IP 홉이 없습니다.' : '먼저 경로 토폴로지 메뉴에서 분석을 실행해 주세요.'}</p>`;
    return;
  }
  const unique = new Map();
  points.forEach(point => {
    const key = String(point.address).toLowerCase();
    const observed = unique.get(key) || { ...point, groups: new Set(), observations: 0 };
    observed.groups.add(point.groupIndex); observed.observations++;
    unique.set(key, observed);
  });
  const legend = groups.filter(group => group.routes.length).map(group => `<li><label style="--route:${group.color}"><input type="checkbox" data-geo-route-index="${group.groupIndex}" checked><i></i><span>${escapeHTML(group.address)}</span></label></li>`).join('');
  const locations = [...unique.values()].map(point => `<li><span class="geo-location-dot" style="--dot:${routeColor([...point.groups][0])}"></span><div><strong>${escapeHTML(labelForAddress(point.address))}</strong><span>${escapeHTML(locationLabel(point.geolocation) || '지역명 없음')}</span></div><code>${point.latitude.toFixed(4)}, ${point.longitude.toFixed(4)}</code></li>`).join('');
  root.innerHTML = `<section class="geo-route-map"><div class="geo-map-toolbar"><div class="geo-map-summary"><strong>${unique.size}</strong><span>개 공인 IP 위치</span><strong>${routes.length}</strong><span>개 관측 경로</span></div><div class="geo-map-actions"><span>스크롤 확대 · 드래그 이동</span><button class="secondary geo-map-fullscreen" type="button">전체 화면</button></div></div><div id="geo-leaflet-map" class="geo-map-stage" role="application" aria-label="확대와 이동이 가능한 공인 IP traceroute 지도"></div><div class="geo-map-detail"><div><p>TRACE ROUTES</p><ul class="geo-route-legend">${legend}</ul></div><div><p>LOCATED HOPS</p><ol class="geo-location-list">${locations}</ol></div></div><p class="geo-map-notice">GeoIP 좌표는 네트워크 사업자 등록 정보 기반의 추정치입니다. 정확한 장비 소재지나 실제 패킷 이동 경로를 보장하지 않습니다.</p></section>`;

  if (!window.L) {
    document.querySelector('#geo-leaflet-map').innerHTML = '<p class="geo-map-load-error">상세 지도 리소스를 불러오지 못했습니다. 아래 위치 목록에서 식별 결과를 확인할 수 있습니다.</p>';
    return;
  }
  const mapElement = document.querySelector('#geo-leaflet-map');
  geoRouteMap = window.L.map(mapElement, { zoomControl: false, minZoom: 2, maxZoom: 18 });
  geoRouteMap.createPane('geoRouteArrows');
  geoRouteMap.getPane('geoRouteArrows').style.zIndex = 425;
  geoRouteMap.getPane('geoRouteArrows').style.pointerEvents = 'none';
  window.L.control.zoom({ position: 'bottomright' }).addTo(geoRouteMap);
  window.L.tileLayer(cartoTileURL(), {
    attribution: '&copy; OpenStreetMap contributors &copy; CARTO', subdomains: 'abcd', maxZoom: 20
  }).addTo(geoRouteMap);
  // Leaflet cannot project route arrows until the map has an initial center and zoom.
  const anchorLongitude = points[0].longitude;
  routes.forEach(route => { route.coordinates = geoRouteCoordinates(route.points, anchorLongitude); });
  geoRouteBounds = window.L.latLngBounds(routes.flatMap(route => route.coordinates));
  if (unique.size === 1) geoRouteMap.setView(geoRouteBounds.getCenter(), 8);
  else geoRouteMap.fitBounds(geoRouteBounds, { padding: [70, 70], maxZoom: 8 });
  const routeLayers = new Map();
  const routeArrows = [];
  groups.filter(group => group.routes.length).forEach(group => routeLayers.set(group.groupIndex, window.L.layerGroup().addTo(geoRouteMap)));
  routes.forEach(route => {
    const layer = routeLayers.get(route.groupIndex);
    const coordinates = route.coordinates;
    if (coordinates.length > 1) window.L.polyline(coordinates, { color: route.color, weight: 3, opacity: .78, dashArray: '2 8', lineCap: 'round' }).addTo(layer);
    route.points.slice(0, -1).forEach((point, index) => {
      const [latitude, longitude] = coordinates[index];
      const [nextLatitude, nextLongitude] = coordinates[index + 1];
      const displayPoint = { latitude, longitude };
      const nextPoint = { latitude: nextLatitude, longitude: nextLongitude };
      const segment = geoRouteSegment(geoRouteMap, displayPoint, nextPoint);
      if (!segment) return;
      const arrowLayer = window.L.polygon(segment.arrow, {
        pane: 'geoRouteArrows', interactive: false, color: route.color, fillColor: route.color, fillOpacity: .92, opacity: .92, weight: 1
      }).addTo(layer);
      routeArrows.push({ layer: arrowLayer, point: displayPoint, nextPoint });
    });
  });
  const updateRouteArrows = () => routeArrows.forEach(arrow => {
    const segment = geoRouteSegment(geoRouteMap, arrow.point, arrow.nextPoint);
    if (!segment) return;
    arrow.layer.setLatLngs(segment.arrow);
  });
  geoRouteMap.on('zoomend moveend', updateRouteArrows);
  root.querySelectorAll('[data-geo-route-index]').forEach(toggle => toggle.addEventListener('change', () => {
    const layer = routeLayers.get(Number(toggle.dataset.geoRouteIndex));
    if (!layer) return;
    if (toggle.checked) layer.addTo(geoRouteMap); else layer.remove();
  }));
  [...unique.values()].forEach(point => {
    const firstHop = Math.max(1, Number(point.hop) || 1);
    const color = routeColor([...point.groups][0]);
    const location = locationLabel(point.geolocation) || '지역명 없음';
    const asn = asnLabel(point.asn) || 'ASN 정보 없음';
    const icon = window.L.divIcon({ className: 'geo-marker-wrap', html: `<span class="geo-marker-pin" style="--marker:${color}"><b>${firstHop}</b></span>`, iconSize: [34, 42], iconAnchor: [17, 38], popupAnchor: [0, -34] });
    const popup = `<article class="geo-popup"><span>HOP ${firstHop} · ${point.observations}회 관측</span><h3>${escapeHTML(labelForAddress(point.address))}</h3><p>${escapeHTML(location)}</p><code>${point.latitude.toFixed(5)}, ${point.longitude.toFixed(5)}</code><small>${escapeHTML(asn)}</small></article>`;
    const displayLongitude = anchorLongitude + normalizeLongitude(point.longitude - anchorLongitude);
    window.L.marker([point.latitude, displayLongitude], { icon, title: `${point.address} · ${location}` }).addTo(geoRouteMap).bindPopup(popup, { maxWidth: 300 });
  });
  root.querySelector('.geo-map-fullscreen')?.addEventListener('click', async () => {
    await root.querySelector('.geo-route-map')?.requestFullscreen?.();
    setTimeout(() => geoRouteMap?.invalidateSize(), 80);
  });
}

function renderTopologyTargetFilter(results) {
  const filter = document.querySelector('#topology-target-filter');
  filter.innerHTML = `<div><strong>TRACE 경로 선택</strong><span>${selectedTopologyTargets.size}/${results.length}개 표시</span></div>
    <div class="target-filter-actions"><button type="button" data-filter-action="all">전체 선택</button><button type="button" data-filter-action="none">전체 해제</button><label class="unknown-node-toggle"><input type="checkbox" data-toggle-unresponsive ${showUnresponsiveTopologyNodes ? 'checked' : ''}><span>응답없음 노드</span></label></div>
    <div class="target-toggles">${results.map((result, index) => `<label style="--route:${routeColor(index)}"><input type="checkbox" data-target-index="${index}" ${selectedTopologyTargets.has(index) ? 'checked' : ''}><i></i><span>${escapeHTML(result.address)}</span></label>`).join('')}</div>`;
}

function topologyAttempts(result) {
  const attempts = result.details?.attempts;
  if (Array.isArray(attempts) && attempts.length) return attempts;
  return [{ attempt: 1, status: result.status, topology: result.details?.topology }];
}

function renderTopologyMap(results, labels = new Map(), groupLabels = new Map(), showUnresponsive = true) {
  const groups = results.map((result, index) => ({
    name: result.address,
    color: routeColor(Number.isInteger(result._routeIndex) ? result._routeIndex : index),
    attempts: topologyAttempts(result)
  }));
  const routes = groups.flatMap((group, groupIndex) => group.attempts.map((attempt, attemptIndex) => ({
    groupIndex,
    attempt: Number(attempt.attempt || attemptIndex + 1),
    reached: Boolean(attempt.topology?.reached),
    nodes: foldUnresponsiveNodes(attempt.topology?.nodes?.length ? attempt.topology.nodes : [
      { hop: 0, address: 'local', status: 'healthy' },
      { hop: 1, address: group.name, status: 'failure' }
    ]).filter(node => showUnresponsive || (node.status !== 'unknown' && node.address))
  })));
  if (!routes.length) return '';

  const root = { key: 'local', address: 'LOCAL', hops: [0], status: 'healthy', latencies: [], groups: new Set(), observations: [], destination: false };
  const observedNodes = new Map([[root.key, root]]);
  const observedEdges = new Map();
  routes.forEach((route, routeIndex) => {
    let previous = root;
    root.groups.add(route.groupIndex);
    root.observations.push({ groupIndex: route.groupIndex, attempt: route.attempt, hop: 0, reached: route.reached });
    route.nodes.slice(1).forEach((node, nodeIndex) => {
      const nodeHops = node.hops?.length ? node.hops : [Number(node.hop)];
      const foldedCount = Number(node.foldedCount || 1);
      const address = node.address || `응답 없음 · ${rangeLabel(nodeHops, 'HOP')}`;
      // An address is a single physical node even when routes split and later rejoin.
      // Non-responsive hops stay route-scoped because they cannot safely be identified.
      const key = node.address ? `address:${node.address.toLowerCase()}` : `unknown:${routeIndex}:${nodeHops.join('-')}`;
      let observed = observedNodes.get(key);
      if (!observed) {
        observed = { key, address, hops: [], status: node.status, latencies: [], groups: new Set(), observations: [], destination: false, foldedCount, publicIP: Boolean(node.public_ip), geolocation: node.geolocation, asn: node.asn };
        observedNodes.set(key, observed);
      }
      observed.hops.push(...nodeHops);
      observed.foldedCount = Math.max(observed.foldedCount || 1, foldedCount);
      observed.groups.add(route.groupIndex);
      observed.observations.push({ groupIndex: route.groupIndex, attempt: route.attempt, hop: nodeHops[0], reached: route.reached });
      if (!observed.geolocation && node.geolocation) observed.geolocation = node.geolocation;
      if (!observed.asn && node.asn) observed.asn = node.asn;
      if (Number(node.latency_ms) > 0) observed.latencies.push(Number(node.latency_ms));
      if (statusRank(node.status) > statusRank(observed.status)) observed.status = node.status;
      if (nodeIndex === route.nodes.length - 2) observed.destination = true;
      if (previous.key !== observed.key) {
        const edgeKey = `${previous.key}\u0000${observed.key}`;
        const edge = observedEdges.get(edgeKey) || { from: previous.key, to: observed.key, observations: 0, groups: new Set() };
        edge.observations++;
        edge.groups.add(route.groupIndex);
        observedEdges.set(edgeKey, edge);
      }
      previous = observed;
    });
  });

  const physicalNodes = [...observedNodes.values()];
  physicalNodes.forEach(node => { node.depth = node.key === 'local' ? 0 : Math.max(1, Math.round(average(node.hops))); });

  // Collapse sibling nodes into an explicit network hierarchy when a shared
  // IPv4 /24, IPv6 /48, or hostname domain exists at the same hop level.
  const buckets = new Map();
  physicalNodes.filter(node => node.key !== 'local' && !node.destination).forEach(node => {
    const hierarchy = networkHierarchy(node.address);
    if (!hierarchy) return;
    const bucketKey = `${node.depth}:${hierarchy.key}`;
    if (!buckets.has(bucketKey)) buckets.set(bucketKey, { ...hierarchy, depth: node.depth, members: [] });
    buckets.get(bucketKey).members.push(node);
  });
  const aggregates = [...buckets.values()].filter(bucket => bucket.members.length >= 2);
  const aggregateByMember = new Map();
  aggregates.forEach((aggregate, index) => {
    aggregate.hierarchyKey = aggregate.key;
    aggregate.key = `aggregate:${aggregate.depth}:${aggregate.key}:${index}`;
    aggregate.address = aggregate.label;
    aggregate.aggregate = true;
    aggregate.hops = aggregate.members.flatMap(member => member.hops);
    aggregate.status = aggregate.members.reduce((status, member) => statusRank(member.status) > statusRank(status) ? member.status : status, 'healthy');
    aggregate.latencies = aggregate.members.flatMap(member => member.latencies);
    aggregate.groups = new Set(aggregate.members.flatMap(member => [...member.groups]));
    aggregate.observations = aggregate.members.flatMap(member => member.observations);
    aggregate.destination = false;
    aggregate.members.forEach(member => aggregateByMember.set(member.key, aggregate));
  });
  const visibleNodes = physicalNodes.filter(node => !aggregateByMember.has(node.key)).concat(aggregates);
  const visibleByKey = new Map(visibleNodes.map(node => [node.key, node]));
  const visibleEdges = new Map();
  observedEdges.forEach(edge => {
    const from = aggregateByMember.get(edge.from)?.key || edge.from;
    const to = aggregateByMember.get(edge.to)?.key || edge.to;
    if (from === to) return;
    const key = `${from}\u0000${to}`;
    const visible = visibleEdges.get(key) || { from, to, observations: 0, groups: new Set() };
    visible.observations += edge.observations;
    edge.groups.forEach(group => visible.groups.add(group));
    visibleEdges.set(key, visible);
  });

  const layers = new Map();
  visibleNodes.forEach(node => {
    if (!layers.has(node.depth)) layers.set(node.depth, []);
    layers.get(node.depth).push(node);
  });
  const laneSpacing = 150;
  const nodeSpacing = 112;
  const laneOffset = index => (index - (groups.length - 1) / 2) * laneSpacing;
  layers.forEach(layer => {
    layer.forEach(node => {
      const routeGroups = [...node.groups];
      node.preferredY = node.key === 'local' || !routeGroups.length
        ? 0
        : average(routeGroups.map(laneOffset));
    });
    layer.sort((a, b) => a.preferredY - b.preferredY || a.address.localeCompare(b.address));
    layer.forEach((node, index) => {
      node.y = index ? Math.max(node.preferredY, layer[index - 1].y + nodeSpacing) : node.preferredY;
    });
    for (let index = layer.length - 2; index >= 0; index--) {
      layer[index].y = Math.min(layer[index].y, layer[index + 1].y - nodeSpacing);
    }
  });
  const maxDepth = Math.max(...layers.keys());
  const allY = visibleNodes.map(node => node.y || 0);
  const minY = Math.min(...allY, ...groups.map((_, index) => laneOffset(index)));
  const maxY = Math.max(...allY, ...groups.map((_, index) => laneOffset(index)));
  const width = Math.max(820, (maxDepth + 1) * 190 + 100);
  const verticalPadding = 115;
  const height = Math.max(340, maxY - minY + verticalPadding * 2);
  const centerY = verticalPadding - minY;
  const centerPoint = node => ({ x: 70 + node.depth * 190, y: centerY + node.y });
  const laneGuides = groups.length > 1 ? groups.map((group, index) => {
    const y = centerY + laneOffset(index);
    return `<g class="route-lane" style="--route:${group.color}"><line x1="38" y1="${y}" x2="${width - 28}" y2="${y}"/><text x="94" y="${y - 10}">${escapeHTML(group.name)}</text></g>`;
  }).join('') : '';
  const links = [...visibleEdges.values()].map(edge => {
    const parent = visibleByKey.get(edge.from); const child = visibleByKey.get(edge.to);
    const from = centerPoint(parent); const to = centerPoint(child);
    const colors = [...edge.groups].map(index => groups[index].color);
    const stroke = colors.length === 1 ? colors[0] : 'url(#shared-route)';
    const strokeWidth = Math.min(5, 1.5 + Math.log2(edge.observations + 1));
    return `<path class="map-link" data-from="${escapeHTML(edge.from)}" data-to="${escapeHTML(edge.to)}" d="${topologyLinkPath(from, to)}" stroke="${stroke}" stroke-width="${strokeWidth.toFixed(1)}"/>`;
  }).join('');
  const nodes = visibleNodes.map(node => {
    const { x, y } = centerPoint(node);
    const latencyValue = node.latencies?.length ? `${(node.latencies.reduce((sum, value) => sum + value, 0) / node.latencies.length).toFixed(1)} ms` : (node.key === 'local' ? 'THIS DEVICE' : node.status === 'unknown' ? 'NO REPLY' : '—');
    const observationCount = node.observations.length;
    const latency = node.key !== 'local' ? `${latencyValue} · ${observationCount}회 관측` : latencyValue;
    const routeDots = [...node.groups].map(index => `<i style="--dot:${groups[index].color}"></i>`).join('');
    const hopRange = rangeLabel(node.hops, 'HOP');
    const targetNames = [...node.groups].map(index => groups[index].name);
    const folded = !node.aggregate && node.foldedCount > 1;
    const mapping = node.aggregate ? groupLabels.get(node.hierarchyKey) : labels.get(String(node.address).toLowerCase());
    const displayName = mapping?.label || node.address;
    const memberLines = node.aggregate ? node.members.map(member => escapeHTML(labelForAddress(member.address, labels))).join('<br>') : escapeHTML(node.address);
    const detail = node.aggregate ? `<em>${memberLines}</em>` : folded ? `<em>연속 무응답 ${node.foldedCount}개 홉을 한 구간으로 접음</em>` : '';
    const mappingDetail = mapping ? `<span>${node.aggregate ? '집계 기준' : 'IP'}: ${escapeHTML(node.address)}${mapping.note ? ` · ${escapeHTML(mapping.note)}` : ''}</span>` : '';
    const geoDetail = !node.aggregate && node.geolocation ? `<span>위치: ${escapeHTML(locationLabel(node.geolocation))}</span>` : '';
    const asnDetail = !node.aggregate && node.asn ? `<span>${escapeHTML(asnLabel(node.asn))}</span>` : '';
    const tooltip = `<strong>${node.aggregate ? `${escapeHTML(displayName)} · ${node.members.length}개 노드` : escapeHTML(displayName)}</strong>${mappingDetail}<span>${hopRange} · ${observationCount}회 관측 · 평균 ${latencyValue}</span>${geoDetail}${asnDetail}<span>대상: ${escapeHTML(targetNames.join(', ') || '로컬')}</span>${detail}`;
    const accessible = `${displayName}${mapping ? `, ${node.aggregate ? '집계 기준' : 'IP'} ${node.address}` : ''}, ${hopRange}, ${observationCount}회 관측${node.aggregate ? `, ${node.members.length}개 노드 집계` : folded ? `, 연속 무응답 ${node.foldedCount}개 홉 접음` : ''}`;
    const editableAddress = !node.aggregate && isIPAddress(node.address) ? ` data-label-address="${escapeHTML(node.address)}"` : '';
    const editableGroup = node.aggregate ? ` data-label-group="${escapeHTML(node.hierarchyKey)}" data-label-group-name="${escapeHTML(node.address)}"` : '';
    return `<g class="map-node ${safeStatus(node.status)}${node.aggregate ? ' aggregate' : ''}${folded ? ' folded' : ''}" data-node-key="${escapeHTML(node.key)}"${editableAddress}${editableGroup} data-x="${x}" data-y="${y}" transform="translate(${x} ${y})" tabindex="0" role="group" aria-label="${escapeHTML(accessible)}">
      <title>${escapeHTML(accessible)}</title><circle class="node-halo" r="${node.aggregate ? 29 : 25}"/><circle class="node-core" r="${node.aggregate ? 20 : 17}"/>
      <text class="node-icon" text-anchor="middle" y="4">${node.key === 'local' ? '◉' : node.aggregate ? 'N' : folded ? '…' : node.destination ? '◆' : node.status === 'unknown' ? '?' : '●'}</text>
      <foreignObject x="-68" y="28" width="136" height="72"><div class="map-label"><strong>${escapeHTML(displayName)}</strong>${mapping ? `<small>${escapeHTML(node.address)}</small>` : ''}<span>${latency}</span><em>${routeDots}</em></div></foreignObject>
      <foreignObject class="map-tooltip-object" x="-108" y="-138" width="216" height="112"><div class="map-tooltip">${tooltip}</div></foreignObject>
    </g>`;
  }).join('');
  const legend = groups.map(group => {
    const reached = group.attempts.filter(attempt => attempt.topology?.reached).length;
    const status = reached === group.attempts.length ? 'healthy' : reached ? 'degraded' : 'failure';
    return `<li><i style="--route:${group.color}"></i><span>${escapeHTML(group.name)}</span><strong class="${status}">${reached}/${group.attempts.length} 도달</strong></li>`;
  }).join('');

  return `<section class="topology-map" aria-label="반복 실행을 합친 네트워크 토폴로지">
    <div class="map-head"><div><span class="topology-kicker">MULTI-RUN ROUTE INTELLIGENCE</span><h3>Observed route topology</h3><p>${routes.length}회 경로를 통합하고 연속 무응답 구간을 접었습니다. 목적지별 레인은 공통 노드에서 합류하고 나머지 구간은 분기됩니다. HOP은 경로 단계, NODE는 식별된 장비 수입니다. 노드를 드래그해 배치를 조정할 수 있습니다.</p></div><div class="map-head-actions"><div class="map-pulse"><i></i> ANALYSIS COMPLETE</div><button class="secondary map-fullscreen" type="button" aria-label="토폴로지 전체 화면으로 보기">전체 화면</button></div></div>
    <div class="map-stage" tabindex="0" aria-label="스크롤하거나 노드를 드래그하여 전체 토폴로지 확인"><svg viewBox="0 0 ${width} ${height}" style="min-width:${width}px;height:${height}px" role="img">
      <defs><linearGradient id="shared-route" x1="0" x2="1"><stop stop-color="#c9ff46"/><stop offset=".5" stop-color="#67d5ff"/><stop offset="1" stop-color="#b58cff"/></linearGradient><filter id="route-glow"><feGaussianBlur stdDeviation="2" result="blur"/><feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>
      ${laneGuides}<g filter="url(#route-glow)">${links}</g>${nodes}</svg></div>
    <div class="map-footer"><ul class="route-legend">${legend}</ul><div class="symbol-legend"><span><b class="branch-symbol">●</b> 식별 노드</span><span><b class="aggregate-symbol">N</b> 복수 노드 그룹</span><span><b class="destination-symbol">◆</b> 목적지</span><span><b class="unknown-symbol">…</b> 연속 무응답 접음</span><span>선 굵기 = 관측 빈도</span></div></div>
  </section>`;
}

function routeColor(index) { return ['#c9ff46', '#67d5ff', '#b58cff', '#ff9f68', '#52e0b1', '#ff72a5'][index % 6]; }
function labelForAddress(address, labels = ipLabels) { return labels.get(String(address).toLowerCase())?.label || address; }
function locationLabel(geolocation) {
  if (!geolocation) return '';
  return [geolocation.city, geolocation.region, geolocation.country].filter(Boolean).filter((value, index, rows) => rows.indexOf(value) === index).join(', ');
}
function asnLabel(asn) {
  if (!asn) return '';
  return [asn.number ? `AS${asn.number}` : '', asn.organization].filter(Boolean).join(' · ');
}

function topologyLinkPath(from, to) {
  return `M ${from.x + 22} ${from.y} C ${from.x + 88} ${from.y}, ${to.x - 88} ${to.y}, ${to.x - 22} ${to.y}`;
}

function enableTopologyDragging(root) {
  const stage = root?.querySelector('.map-stage');
  const svg = stage?.querySelector('svg');
  if (!stage || !svg) return;
  root.querySelector('.map-fullscreen')?.addEventListener('click', async () => {
    const map = root.querySelector('.topology-map');
    if (!document.fullscreenElement) await map?.requestFullscreen?.();
    else await document.exitFullscreen?.();
  });
  const positions = new Map([...svg.querySelectorAll('.map-node')].map(node => [node.dataset.nodeKey, {
    x: Number(node.dataset.x), y: Number(node.dataset.y), element: node
  }]));
  const redrawLinks = key => {
    [...svg.querySelectorAll('.map-link')].filter(link => link.dataset.from === key || link.dataset.to === key).forEach(link => {
      const from = positions.get(link.dataset.from); const to = positions.get(link.dataset.to);
      if (from && to) link.setAttribute('d', topologyLinkPath(from, to));
    });
  };
  let drag;
  svg.addEventListener('pointerdown', event => {
    const node = event.target.closest('.map-node');
    if (!node || event.button !== 0) return;
    const point = positions.get(node.dataset.nodeKey);
    drag = { key: node.dataset.nodeKey, node, pointerId: event.pointerId, clientX: event.clientX, clientY: event.clientY, x: point.x, y: point.y };
    node.classList.add('dragging');
    node.setPointerCapture(event.pointerId);
    event.preventDefault();
  });
  svg.addEventListener('pointermove', event => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const scaleX = svg.viewBox.baseVal.width / svg.getBoundingClientRect().width;
    const scaleY = svg.viewBox.baseVal.height / svg.getBoundingClientRect().height;
    const point = positions.get(drag.key);
    point.x = Math.max(34, Math.min(svg.viewBox.baseVal.width - 34, drag.x + (event.clientX - drag.clientX) * scaleX));
    point.y = Math.max(34, Math.min(svg.viewBox.baseVal.height - 72, drag.y + (event.clientY - drag.clientY) * scaleY));
    drag.node.setAttribute('transform', `translate(${point.x} ${point.y})`);
    redrawLinks(drag.key);
  });
  const finishDrag = event => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    drag.node.classList.remove('dragging');
    drag = null;
  };
  svg.addEventListener('pointerup', finishDrag);
  svg.addEventListener('pointercancel', finishDrag);
}

function average(values) { return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : 0; }
function rangeLabel(values, prefix = '') {
  const numbers = values.filter(Number.isFinite);
  if (!numbers.length) return prefix || '—';
  const min = Math.min(...numbers); const max = Math.max(...numbers);
  return `${prefix} ${min}${min === max ? '' : `–${max}`}`.trim();
}
function foldUnresponsiveNodes(nodes) {
  const folded = [];
  for (let index = 0; index < nodes.length;) {
    const node = nodes[index];
    const unresponsive = node?.status === 'unknown' || !node?.address;
    if (!unresponsive) {
      folded.push({ ...node, hops: [Number(node.hop)], firstIndex: index, lastIndex: index, foldedCount: 1 });
      index++;
      continue;
    }
    let end = index + 1;
    while (end < nodes.length && (nodes[end]?.status === 'unknown' || !nodes[end]?.address)) end++;
    const segment = nodes.slice(index, end);
    const hops = segment.map(item => Number(item.hop)).filter(Number.isFinite);
    folded.push({ ...node, address: '', status: 'unknown', hop: hops[0], hops, firstIndex: index, lastIndex: end - 1, foldedCount: segment.length });
    index = end;
  }
  return folded;
}
function networkHierarchy(address) {
  const ipv4 = String(address).match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.\d{1,3}$/);
  if (ipv4 && ipv4.slice(1).every(part => Number(part) <= 255)) {
    const prefix = ipv4.slice(1, 4).join('.');
    return { key: `ipv4:${prefix}`, label: `${prefix}.0/24` };
  }
  if (String(address).includes(':')) {
    const prefix = String(address).split(':').filter(Boolean).slice(0, 3).join(':');
    if (prefix) return { key: `ipv6:${prefix}`, label: `${prefix}::/48` };
  }
  const labels = String(address).toLowerCase().split('.');
  if (labels.length >= 3) {
    const domain = labels.slice(-2).join('.');
    return { key: `domain:${domain}`, label: `*.${domain}` };
  }
  return null;
}

function statusRank(status) { return ({ healthy: 0, unknown: 1, degraded: 2, failure: 3 })[status] ?? 1; }
function safeStatus(status) { return ['healthy', 'degraded', 'unknown', 'failure'].includes(status) ? status : 'unknown'; }

function loadIPLabels() {
  try {
    const rows = JSON.parse(localStorage.getItem(IP_LABEL_STORAGE_KEY) || '[]');
    return new Map(Array.isArray(rows) ? rows.filter(row => row?.ip && row?.label).map(row => [String(row.ip).toLowerCase(), { ip: String(row.ip), label: String(row.label), note: String(row.note || '') }]) : []);
  } catch (_) {
    return new Map();
  }
}

function saveIPLabels() {
  try { localStorage.setItem(IP_LABEL_STORAGE_KEY, JSON.stringify([...ipLabels.values()])); } catch (_) { /* Keep the in-memory table usable when browser storage is unavailable. */ }
}

function loadAggregateLabels() {
  try {
    const rows = JSON.parse(localStorage.getItem(AGGREGATE_LABEL_STORAGE_KEY) || '[]');
    return new Map(Array.isArray(rows) ? rows.filter(row => row?.key && row?.label).map(row => [String(row.key), { key: String(row.key), label: String(row.label), note: String(row.note || '') }]) : []);
  } catch (_) {
    return new Map();
  }
}

function saveAggregateLabels() {
  try { localStorage.setItem(AGGREGATE_LABEL_STORAGE_KEY, JSON.stringify([...aggregateLabels.values()])); } catch (_) { /* Keep in-memory labels usable when storage is unavailable. */ }
}

function isIPAddress(value) {
  const address = String(value).trim();
  const ipv4 = address.split('.');
  if (ipv4.length === 4 && ipv4.every(part => /^\d{1,3}$/.test(part) && Number(part) <= 255)) return true;
  if (!address.includes(':') || !/^[0-9a-f:]+$/i.test(address) || (address.match(/::/g) || []).length > 1) return false;
  const parts = address.split(':');
  const specified = parts.filter(Boolean);
  if (!specified.every(part => part.length <= 4)) return false;
  return address.includes('::') ? specified.length < 8 : parts.length === 8;
}

function parseCSVLine(line) {
  const fields = []; let value = ''; let quoted = false;
  for (let index = 0; index < line.length; index++) {
    const char = line[index];
    if (char === '"' && quoted && line[index + 1] === '"') { value += '"'; index++; }
    else if (char === '"') quoted = !quoted;
    else if (char === ',' && !quoted) { fields.push(value.trim()); value = ''; }
    else value += char;
  }
  fields.push(value.trim());
  return fields;
}

function parseIPLabelImport(text, filename = '') {
  if (filename.toLowerCase().endsWith('.json') || String(text).trim().startsWith('{') || String(text).trim().startsWith('[')) {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) return parsed.map(row => ({ ip: row.ip || row.address, label: row.label || row.name, note: row.note || row.description || '' }));
    return Object.entries(parsed).map(([ip, value]) => typeof value === 'string' ? { ip, label: value, note: '' } : { ip, label: value?.label || value?.name, note: value?.note || value?.description || '' });
  }
  const lines = String(text).split(/\r?\n/).filter(line => line.trim());
  if (!lines.length) return [];
  const first = parseCSVLine(lines[0]).map(value => value.toLowerCase());
  const hasHeader = first.includes('ip') || first.includes('address');
  const ipIndex = hasHeader ? Math.max(first.indexOf('ip'), first.indexOf('address')) : 0;
  const labelIndex = hasHeader ? Math.max(first.indexOf('label'), first.indexOf('name')) : 1;
  const noteIndex = hasHeader ? Math.max(first.indexOf('note'), first.indexOf('description')) : 2;
  return lines.slice(hasHeader ? 1 : 0).map(line => {
    const fields = parseCSVLine(line);
    return { ip: fields[ipIndex], label: fields[labelIndex], note: noteIndex >= 0 ? fields[noteIndex] || '' : '' };
  });
}

function upsertIPLabels(rows) {
  let imported = 0; const invalid = [];
  rows.forEach(row => {
    const ip = String(row.ip || '').trim(); const label = String(row.label || '').trim();
    if (!isIPAddress(ip) || !label) { invalid.push(ip || '(빈 IP)'); return; }
    ipLabels.set(ip.toLowerCase(), { ip, label, note: String(row.note || '').trim() });
    imported++;
  });
  saveIPLabels();
  renderIPLabelTable();
  refreshRenderedLabels();
  return { imported, invalid };
}

function renderIPLabelTable() {
  const rows = [...ipLabels.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true }));
  document.querySelector('#ip-label-rows').innerHTML = rows.length ? rows.map(row => `<tr data-ip="${escapeHTML(row.ip.toLowerCase())}">
    <td><code>${escapeHTML(row.ip)}</code></td>
    <td><input data-field="label" value="${escapeHTML(row.label)}" aria-label="${escapeHTML(row.ip)} 표시 라벨"></td>
    <td><input data-field="note" value="${escapeHTML(row.note)}" aria-label="${escapeHTML(row.ip)} 설명"></td>
    <td><button type="button" class="mapping-delete">삭제</button></td>
  </tr>`).join('') : '<tr><td colspan="4" class="empty-mapping">등록된 IP 라벨이 없습니다.</td></tr>';
}

function refreshRenderedLabels() {
  if (currentReport) render(currentReport);
  if (currentTopologyReport) renderStandaloneTopology(currentTopologyReport);
}

function populateTopologyLabelEditor(address) {
  const ip = String(address || '').trim();
  if (!isIPAddress(ip)) return false;
  resetTopologyLabelEditorToIP();
  const mapping = ipLabels.get(ip.toLowerCase());
  document.querySelector('#topology-label-address').value = ip;
  document.querySelector('#topology-label-name').value = mapping?.label || '';
  document.querySelector('#topology-label-note').value = mapping?.note || '';
  document.querySelector('#topology-label-delete').hidden = !mapping;
  document.querySelector('#topology-label-name').focus();
  const message = document.querySelector('#topology-label-message');
  message.textContent = mapping ? `${ip}의 기존 라벨을 불러왔습니다.` : `${ip}에 적용할 라벨을 입력해 주세요.`;
  message.className = 'mapping-message';
  return true;
}

function populateAggregateLabelEditor(key, name) {
  const groupKey = String(key || '').trim();
  const groupName = String(name || '').trim();
  if (!groupKey || !groupName) return false;
  const mapping = aggregateLabels.get(groupKey);
  const address = document.querySelector('#topology-label-address');
  document.querySelector('#topology-label-kind').value = 'aggregate';
  document.querySelector('#topology-label-key').value = groupKey;
  document.querySelector('#topology-label-subject-label').textContent = '집계 기준';
  address.value = groupName;
  address.readOnly = true;
  document.querySelector('#topology-label-name').value = mapping?.label || '';
  document.querySelector('#topology-label-note').value = mapping?.note || '';
  document.querySelector('#topology-label-delete').hidden = !mapping;
  document.querySelector('#topology-label-name').focus();
  const message = document.querySelector('#topology-label-message');
  message.textContent = mapping ? `${groupName} 집계의 기존 라벨을 불러왔습니다.` : `${groupName} 집계에 적용할 라벨을 입력해 주세요.`;
  message.className = 'mapping-message';
  return true;
}

function resetTopologyLabelEditorToIP() {
  document.querySelector('#topology-label-kind').value = 'ip';
  document.querySelector('#topology-label-key').value = '';
  document.querySelector('#topology-label-subject-label').textContent = 'IP 주소';
  document.querySelector('#topology-label-address').readOnly = false;
}

function renderTopology(topology) {
  if (!topology?.nodes?.length) return '';
  const statusText = { healthy: '정상', degraded: '지연 증가', unknown: '응답 없음', failure: '도달 실패' };
  const sourceNodes = topology.nodes;
  const nodes = foldUnresponsiveNodes(sourceNodes);
  const unknownCount = sourceNodes.filter(node => node.status === 'unknown' || !node.address).length;
  const degradedCount = topology.links?.filter(link => link.status === 'degraded').length || 0;
  const routeStatus = topology.reached ? (degradedCount ? 'degraded' : 'healthy') : 'failure';
  return `<section class="topology" aria-label="네트워크 경로 상태도">
    <div class="topology-head">
      <div>
        <span class="topology-kicker">TRACEROUTE MAP</span>
        <h4>네트워크 경로</h4>
      </div>
      <strong class="topology-badge ${routeStatus}"><i aria-hidden="true"></i>${topology.reached ? '대상 도달' : '대상 미도달'}</strong>
    </div>
    <div class="topology-stats" aria-label="경로 요약">
      <div><strong>${Math.max(nodes.length - 1, 0)}</strong><span>관측된 홉</span></div>
      <div><strong>${unknownCount}</strong><span>응답 없음</span></div>
      <div><strong>${degradedCount}</strong><span>지연 증가 구간</span></div>
    </div>
    <div class="topology-viewport" tabindex="0" aria-label="가로로 스크롤하여 전체 경로 확인">
      <div class="topology-flow">${nodes.map((node, index) => {
      const link = index > 0 ? topology.links?.[node.firstIndex - 1] : null;
      const status = ['healthy', 'degraded', 'unknown', 'failure'].includes(node.status) ? node.status : 'unknown';
      const linkStatus = ['healthy', 'degraded', 'unknown', 'failure'].includes(link?.status) ? link.status : 'unknown';
      const latencyDelta = Number(link?.latency_delta_ms || 0);
      const latency = Number(node.latency_ms);
      const folded = node.foldedCount > 1;
      const hopLabel = node.hop === 0 ? '출발지' : (index === nodes.length - 1 && topology.reached ? '대상지' : rangeLabel(node.hops, 'HOP'));
      return `${link ? `<div class="topology-link ${linkStatus}" title="지연 변화 ${latencyDelta.toFixed(1)} ms">
          <span></span>${latencyDelta > 0 ? `<small>+${latencyDelta.toFixed(1)} ms</small>` : ''}
        </div>` : ''}
        <div class="topology-node ${status}${folded ? ' folded' : ''}" title="${escapeHTML(folded ? `연속 무응답 ${node.foldedCount}개 홉 접음` : statusText[status])}">
          <div class="node-marker"><span aria-hidden="true">${node.hop === 0 ? '◉' : folded ? '…' : status === 'unknown' ? '?' : status === 'failure' ? '!' : node.hop}</span></div>
          <div class="node-card">
            <span class="node-hop">${hopLabel}</span>
            <strong>${escapeHTML(node.address ? labelForAddress(node.address) : (folded ? `응답 없음 · ${node.foldedCount}개 홉` : '응답 없음'))}</strong>
            ${node.address && labelForAddress(node.address) !== node.address ? `<span class="node-address">${escapeHTML(node.address)}</span>` : ''}
            ${node.geolocation ? `<span class="node-address">${escapeHTML(locationLabel(node.geolocation))}</span>` : ''}
            ${node.asn ? `<span class="node-address">${escapeHTML(asnLabel(node.asn))}</span>` : ''}
            <small><i aria-hidden="true"></i>${folded ? '연속 무응답 접음' : Number.isFinite(latency) && latency > 0 ? `${latency.toFixed(1)} ms` : statusText[status]}</small>
          </div>
        </div>`;
    }).join('')}</div></div>
    <div class="topology-foot">
      <div class="topology-legend" aria-label="상태 범례"><span class="healthy">정상</span><span class="degraded">지연 증가</span><span class="unknown">응답 없음</span><span class="failure">도달 실패</span></div>
      <p>※ 중간 홉은 정책에 따라 응답하지 않을 수 있어 단독으로 장애를 의미하지 않습니다.</p>
    </div>
  </section>`;
}

function escapeHTML(value) {
  const el = document.createElement('span');
  el.textContent = value;
  return el.innerHTML;
}

document.querySelector('#add-target').addEventListener('click', () => addTarget());
form.addEventListener('submit', async event => {
  event.preventDefault();
  errorEl.hidden = true;
  const button = document.querySelector('#run');
  button.disabled = true;
  button.firstChild.textContent = '진단 중 ';
  try {
    const apiURL = document.querySelector('#api-url').value.replace(/\/$/, '');
    const response = await fetch(`${apiURL}/api/v1/reports`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ targets: collectTargets(), timeout_ms: Number(document.querySelector('#timeout').value) })
    });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message || `API 오류 (${response.status})`);
    currentReport = data;
    render(data);
  } catch (error) {
    errorEl.textContent = `진단을 완료하지 못했습니다: ${error.message}`;
    errorEl.hidden = false;
  } finally {
    button.disabled = false;
    button.firstChild.textContent = '진단 시작 ';
  }
});

document.querySelector('#download').addEventListener('click', () => {
  const blob = new Blob([JSON.stringify(currentReport, null, 2)], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `checknetwork-${currentReport.id}.json`;
  link.click();
  URL.revokeObjectURL(link.href);
});

document.querySelector('#topology-form').addEventListener('submit', async event => {
  event.preventDefault();
  const error = document.querySelector('#topology-error');
  const button = document.querySelector('#run-topology');
  error.hidden = true;
  button.disabled = true;
  button.firstChild.textContent = '토폴로지 분석 중 ';
  try {
    const apiURL = document.querySelector('#topology-api-url').value.replace(/\/$/, '');
    const addresses = [...new Set(document.querySelector('#topology-targets').value.split(/\n|,/).map(value => value.trim()).filter(Boolean))];
    const attempts = Number(document.querySelector('#topology-attempts').value);
    if (!addresses.length) throw new Error('점검할 목적지를 한 개 이상 입력해 주세요.');
    if (addresses.length > 20) throw new Error('목적지는 최대 20개까지 입력할 수 있습니다.');
    if (!Number.isInteger(attempts) || attempts < 1 || attempts > 10) throw new Error('반복 횟수는 1~10 사이의 정수여야 합니다.');
    const response = await fetch(`${apiURL}/api/v1/reports`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        targets: addresses.map(address => ({ kind: 'traceroute', address, attempts })),
        timeout_ms: Number(document.querySelector('#topology-timeout').value)
      })
    });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message || `API 오류 (${response.status})`);
    currentTopologyReport = data;
    selectedTopologyTargets = new Set((data.results || []).map((_, index) => index));
    showUnresponsiveTopologyNodes = true;
    renderStandaloneTopology(data);
  } catch (caught) {
    error.textContent = `경로 점검을 완료하지 못했습니다: ${caught.message}`;
    error.hidden = false;
  } finally {
    button.disabled = false;
    button.firstChild.textContent = '토폴로지 분석 ';
  }
});

document.querySelector('#download-topology').addEventListener('click', () => {
  const blob = new Blob([JSON.stringify(currentTopologyReport, null, 2)], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `checknetwork-topology-${currentTopologyReport.id}.json`;
  link.click();
  URL.revokeObjectURL(link.href);
});

document.querySelector('#topology-target-filter').addEventListener('change', event => {
  if (event.target.matches('[data-toggle-unresponsive]')) {
    showUnresponsiveTopologyNodes = event.target.checked;
    renderStandaloneTopology(currentTopologyReport);
    return;
  }
  const index = Number(event.target.dataset.targetIndex);
  if (!Number.isInteger(index)) return;
  if (event.target.checked) selectedTopologyTargets.add(index);
  else selectedTopologyTargets.delete(index);
  renderStandaloneTopology(currentTopologyReport);
});

document.querySelector('#topology-target-filter').addEventListener('click', event => {
  const action = event.target.dataset.filterAction;
  if (!action || !currentTopologyReport) return;
  selectedTopologyTargets = action === 'all' ? new Set((currentTopologyReport.results || []).map((_, index) => index)) : new Set();
  renderStandaloneTopology(currentTopologyReport);
});

document.querySelector('#topology-result').addEventListener('click', event => {
  const node = event.target.closest('.map-node[data-label-address], .map-node[data-label-group]');
  if (node?.dataset.labelGroup) populateAggregateLabelEditor(node.dataset.labelGroup, node.dataset.labelGroupName);
  else if (node) populateTopologyLabelEditor(node.dataset.labelAddress);
});

document.querySelector('#topology-result').addEventListener('keydown', event => {
  if (!['Enter', ' '].includes(event.key)) return;
  const node = event.target.closest('.map-node[data-label-address], .map-node[data-label-group]');
  if (!node) return;
  event.preventDefault();
  if (node.dataset.labelGroup) populateAggregateLabelEditor(node.dataset.labelGroup, node.dataset.labelGroupName);
  else populateTopologyLabelEditor(node.dataset.labelAddress);
});

document.querySelector('#topology-label-address').addEventListener('change', event => {
  resetTopologyLabelEditorToIP();
  const ip = event.target.value.trim();
  if (isIPAddress(ip)) populateTopologyLabelEditor(ip);
});

document.querySelector('#topology-label-form').addEventListener('submit', event => {
  event.preventDefault();
  const kind = document.querySelector('#topology-label-kind').value;
  const label = document.querySelector('#topology-label-name').value.trim();
  const note = document.querySelector('#topology-label-note').value.trim();
  const message = document.querySelector('#topology-label-message');
  if (kind === 'aggregate') {
    const key = document.querySelector('#topology-label-key').value;
    const name = document.querySelector('#topology-label-address').value.trim();
    if (!key || !label) {
      message.textContent = '집계 노드의 표시 라벨을 입력해 주세요.';
      message.className = 'mapping-message failure';
      return;
    }
    aggregateLabels.set(key, { key, label, note });
    saveAggregateLabels(); refreshRenderedLabels();
    message.textContent = `${name} 집계 라벨을 저장하고 토폴로지에 반영했습니다.`;
    message.className = 'mapping-message healthy';
    document.querySelector('#topology-label-delete').hidden = false;
    return;
  }
  const ip = document.querySelector('#topology-label-address').value.trim();
  const result = upsertIPLabels([{
    ip,
    label,
    note
  }]);
  if (!result.imported) {
    message.textContent = '올바른 IPv4/IPv6 주소와 라벨을 입력해 주세요.';
    message.className = 'mapping-message failure';
    return;
  }
  message.textContent = `${ip} 라벨을 저장하고 토폴로지에 반영했습니다.`;
  message.className = 'mapping-message healthy';
  document.querySelector('#topology-label-delete').hidden = false;
});

document.querySelector('#topology-label-delete').addEventListener('click', () => {
  const address = document.querySelector('#topology-label-address');
  const kind = document.querySelector('#topology-label-kind').value;
  const subject = address.value.trim();
  if (kind === 'aggregate') {
    const key = document.querySelector('#topology-label-key').value;
    if (!aggregateLabels.delete(key)) return;
    saveAggregateLabels(); refreshRenderedLabels();
  } else {
    if (!ipLabels.delete(subject.toLowerCase())) return;
    saveIPLabels(); renderIPLabelTable(); refreshRenderedLabels();
  }
  document.querySelector('#topology-label-name').value = '';
  document.querySelector('#topology-label-note').value = '';
  document.querySelector('#topology-label-delete').hidden = true;
  const message = document.querySelector('#topology-label-message');
  message.textContent = `${subject} ${kind === 'aggregate' ? '집계 ' : ''}라벨을 삭제했습니다.`;
  message.className = 'mapping-message healthy';
});

document.querySelector('#ip-label-form').addEventListener('submit', event => {
  event.preventDefault();
  const ip = document.querySelector('#ip-label-address').value.trim();
  const label = document.querySelector('#ip-label-name').value.trim();
  const result = upsertIPLabels([{ ip, label, note: document.querySelector('#ip-label-note').value }]);
  const message = document.querySelector('#ip-label-message');
  if (!result.imported) { message.textContent = '올바른 IPv4/IPv6 주소와 라벨을 입력해 주세요.'; message.className = 'mapping-message failure'; return; }
  message.textContent = `${ip} 매핑을 저장했습니다.`; message.className = 'mapping-message healthy';
  event.target.reset();
});

document.querySelector('#ip-label-import').addEventListener('change', async event => {
  const file = event.target.files?.[0]; if (!file) return;
  const message = document.querySelector('#ip-label-message');
  try {
    const result = upsertIPLabels(parseIPLabelImport(await file.text(), file.name));
    message.textContent = `${result.imported}개 매핑을 가져왔습니다.${result.invalid.length ? ` ${result.invalid.length}개 행은 형식 오류로 제외했습니다.` : ''}`;
    message.className = `mapping-message ${result.invalid.length ? 'degraded' : 'healthy'}`;
  } catch (error) {
    message.textContent = `가져오지 못했습니다: ${error.message}`; message.className = 'mapping-message failure';
  } finally { event.target.value = ''; }
});

document.querySelector('#ip-label-rows').addEventListener('change', event => {
  const row = event.target.closest('tr[data-ip]'); if (!row || !event.target.dataset.field) return;
  const mapping = ipLabels.get(row.dataset.ip); if (!mapping) return;
  mapping[event.target.dataset.field] = event.target.value.trim();
  if (!mapping.label) { renderIPLabelTable(); return; }
  saveIPLabels(); refreshRenderedLabels();
});

document.querySelector('#ip-label-rows').addEventListener('click', event => {
  if (!event.target.classList.contains('mapping-delete')) return;
  const row = event.target.closest('tr[data-ip]'); if (!row) return;
  ipLabels.delete(row.dataset.ip); saveIPLabels(); renderIPLabelTable(); refreshRenderedLabels();
});

document.querySelector('#carto-base-map-form').addEventListener('submit', event => {
  event.preventDefault();
  const input = document.querySelector('#carto-base-map-input');
  const value = input.value.trim();
  try {
    if (value) sessionStorage.setItem(CARTO_BASE_MAP_STORAGE_KEY, value);
    else sessionStorage.removeItem(CARTO_BASE_MAP_STORAGE_KEY);
  } catch (_) {
    renderCartoBaseMapStatus('브라우저 저장소를 사용할 수 없어 설정을 적용하지 못했습니다.');
    return;
  }
  cartoBaseMap = value ? { value, source: 'session' } : loadCartoBaseMap();
  input.value = '';
  renderCartoBaseMapStatus(value ? 'CARTO 설정을 현재 탭에 적용했습니다.' : '입력 설정을 지우고 배포/기본 설정으로 전환했습니다.');
  if (currentTopologyReport) renderGeoRouteMap(currentTopologyReport);
});

const APP_VIEWS = new Set(['diagnostics', 'topology', 'geo-map', 'ip-labels']);

function viewFromHash(hash) {
  const requested = String(hash || '').replace(/^#/, '');
  return APP_VIEWS.has(requested) ? requested : 'diagnostics';
}

function activateView(requestedView) {
  const view = APP_VIEWS.has(requestedView) ? requestedView : 'diagnostics';
  document.querySelectorAll('[data-view]').forEach(element => { element.hidden = element.dataset.view !== view; });
  document.querySelectorAll('[data-view-link]').forEach(link => {
    const active = link.dataset.viewLink === view;
    link.classList.toggle('active', active);
    if (active) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  if (view === 'geo-map' && geoRouteMap) setTimeout(() => {
    geoRouteMap.invalidateSize();
    if (geoRouteBounds?.isValid()) geoRouteMap.fitBounds(geoRouteBounds, { padding: [70, 70], maxZoom: 8 });
  }, 0);
  return view;
}

function syncView() {
  activateView(viewFromHash(location.hash));
}

document.querySelector('.main-nav').addEventListener('click', event => {
  const link = event.target.closest('[data-view-link]');
  if (!link) return;
  event.preventDefault();
  const view = activateView(link.dataset.viewLink);
  const hash = `#${view}`;
  if (location.hash !== hash) location.hash = hash;
});
window.addEventListener('hashchange', syncView);
syncView();
renderIPLabelTable();
renderCartoBaseMapStatus();

addTarget('dns', 'example.com');
addTarget('tcp', '1.1.1.1:443');
addTarget('https', 'https://example.com');
addTarget('traceroute', 'example.com');
