const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync(`${__dirname}/app.js`, 'utf8');
const markup = fs.readFileSync(`${__dirname}/index.html`, 'utf8');
const rendererSource = source.slice(
  source.indexOf('function renderTopologyMap'),
  source.indexOf('function renderTopology(topology)')
);
const navigationSource = source.slice(
  source.indexOf('const APP_VIEWS'),
  source.indexOf("document.querySelector('.main-nav').addEventListener")
);

function topologyAttempts(result) {
  return result.details.attempts;
}

function escapeHTML(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
}

eval(rendererSource);
eval(navigationSource);

const topology = nodes => ({ reached: true, nodes });
const results = [
  {
    address: 'alpha.example',
    details: { attempts: [{ attempt: 1, topology: topology([
      { hop: 0, address: 'local', status: 'healthy' },
      { hop: 1, address: '10.0.0.1', status: 'healthy', latency_ms: 1 },
      { hop: 2, address: '192.0.2.1', status: 'healthy', latency_ms: 5 },
      { hop: 3, address: '203.0.113.50', status: 'healthy', latency_ms: 10, public_ip: true, geolocation: { city: 'Seoul', country: 'South Korea', latitude: 37.56, longitude: 126.97 }, asn: { number: 64500, organization: 'Example Transit' } }
    ]) }] }
  },
  {
    address: 'beta.example',
    details: { attempts: [{ attempt: 1, topology: topology([
      { hop: 0, address: 'local', status: 'healthy' },
      { hop: 1, address: '10.0.0.2', status: 'healthy', latency_ms: 2 },
      { hop: 2, address: '192.0.2.2', status: 'degraded', latency_ms: 7 },
      { hop: 3, address: '203.0.113.50', status: 'healthy', latency_ms: 11 }
    ]) }] }
  }
];

const rendered = renderTopologyMap(results);

assert.equal((rendered.match(/<g class="map-node/g) || []).length, 4, 'shared destination must render once');
assert.match(rendered, /10\.0\.0\.0\/24 · 2개 노드/);
assert.match(rendered, /192\.0\.2\.0\/24 · 2개 노드/);
assert.match(rendered, /class="map-tooltip-object"/);
assert.match(rendered, /tabindex="0" role="group"/);
assert.equal((rendered.match(/<g class="route-lane"/g) || []).length, 2, 'each destination must receive a stable visual lane');
assert.match(rendered, /목적지별 레인은 공통 노드에서 합류하고 나머지 구간은 분기됩니다/);
assert.match(rendered, /위치: Seoul, South Korea/);
assert.match(rendered, /AS64500 · Example Transit/);

const disjointRendered = renderTopologyMap([
  {
    address: 'east.example',
    details: { attempts: [{ attempt: 1, topology: topology([
      { hop: 0, address: 'local', status: 'healthy' },
      { hop: 1, address: '10.1.0.1', status: 'healthy' },
      { hop: 2, address: '198.51.100.10', status: 'healthy' }
    ]) }] }
  },
  {
    address: 'west.example',
    details: { attempts: [{ attempt: 1, topology: topology([
      { hop: 0, address: 'local', status: 'healthy' },
      { hop: 1, address: '172.16.0.1', status: 'healthy' },
      { hop: 2, address: '203.0.113.20', status: 'healthy' }
    ]) }] }
  }
]);
const disjointHopOneY = [...disjointRendered.matchAll(/data-node-key="address:(?:10\.1\.0\.1|172\.16\.0\.1)"[^>]*data-y="([^"]+)"/g)].map(match => Number(match[1]));
assert.equal(disjointHopOneY.length, 2);
assert.notEqual(disjointHopOneY[0], disjointHopOneY[1], 'routes without a shared node must split into separate destination lanes');

const foldedRendered = renderTopologyMap([{
  address: 'folded.example',
  details: { attempts: [{ attempt: 1, topology: topology([
    { hop: 0, address: 'local', status: 'healthy' },
    { hop: 1, address: '10.0.0.1', status: 'healthy', latency_ms: 1 },
    { hop: 2, address: '', status: 'unknown' },
    { hop: 3, address: '', status: 'unknown' },
    { hop: 4, address: '', status: 'unknown' },
    { hop: 5, address: '203.0.113.90', status: 'healthy', latency_ms: 12 }
  ]) }] }
}]);

assert.equal((foldedRendered.match(/<g class="map-node/g) || []).length, 4, 'three consecutive unknown hops must render as one node');
assert.match(foldedRendered, /응답 없음 · HOP 2–4/);
assert.match(foldedRendered, /class="map-node unknown folded"/);
assert.match(foldedRendered, /연속 무응답 3개 홉을 한 구간으로 접음/);
const hiddenUnknownRendered = renderTopologyMap([{
  address: 'folded.example',
  details: { attempts: [{ attempt: 1, topology: topology([
    { hop: 0, address: 'local', status: 'healthy' },
    { hop: 1, address: '10.0.0.1', status: 'healthy' },
    { hop: 2, address: '', status: 'unknown' },
    { hop: 3, address: '', status: 'unknown' },
    { hop: 4, address: '203.0.113.90', status: 'healthy' }
  ]) }] }
}], new Map(), new Map(), false);
assert.equal((hiddenUnknownRendered.match(/<g class="map-node/g) || []).length, 3, 'unknown-node toggle must remove folded no-reply nodes');
assert.doesNotMatch(hiddenUnknownRendered, /응답 없음 · HOP/);
assert.match(hiddenUnknownRendered, /data-from="address:10\.0\.0\.1" data-to="address:203\.0\.113\.90"/, 'removing unknown nodes must reconnect adjacent responsive nodes');
assert.match(rendered, /data-node-key=/);
assert.match(rendered, /data-from=.*data-to=/);
assert.match(rendered, />N<\/text>/, 'aggregate node must use N instead of a count that resembles a hop number');
assert.match(rendered, /data-label-group="ipv4:10\.0\.0"/, 'aggregate nodes must expose a stable editable hierarchy key');
assert.match(rendered, /HOP은 경로 단계, NODE는 식별된 장비 수/);
assert.match(rendered, /class="secondary map-fullscreen"/);

const mappedRendered = renderTopologyMap(results, new Map([
  ['203.0.113.50', { ip: '203.0.113.50', label: '공용 서비스 경계', note: '서울 IDC' }]
]));
assert.match(mappedRendered, /공용 서비스 경계/);
assert.match(mappedRendered, /IP: 203\.0\.113\.50 · 서울 IDC/);
assert.match(mappedRendered, /<small>203\.0\.113\.50<\/small>/);
assert.match(mappedRendered, /data-label-address="203\.0\.113\.50"/, 'individual IP nodes must be selectable for inline label editing');
assert.doesNotMatch(mappedRendered, /class="map-node[^\"]*aggregate[^>]*data-label-address=/, 'aggregate nodes must not masquerade as one editable IP');

const aggregateMappedRendered = renderTopologyMap(results, new Map(), new Map([
  ['ipv4:10.0.0', { key: 'ipv4:10.0.0', label: '본사 WAN 이중화 구간', note: '통신사 A/B' }]
]));
assert.match(aggregateMappedRendered, /본사 WAN 이중화 구간/);
assert.match(aggregateMappedRendered, /집계 기준: 10\.0\.0\.0\/24 · 통신사 A\/B/);
assert.match(aggregateMappedRendered, /<small>10\.0\.0\.0\/24<\/small>/);

const filteredRendered = renderTopologyMap([{ ...results[1], _routeIndex: 2 }]);
assert.match(filteredRendered, /--route:#b58cff/, 'filtered route must retain its original color');

assert.deepEqual(parseIPLabelImport('ip,label,note\n10.0.0.1,"서울, 게이트웨이",본사', 'labels.csv'), [
  { ip: '10.0.0.1', label: '서울, 게이트웨이', note: '본사' }
]);
assert.deepEqual(parseIPLabelImport('{"2001:db8::1":"IPv6 경계"}', 'labels.json'), [
  { ip: '2001:db8::1', label: 'IPv6 경계', note: '' }
]);
assert.deepEqual(parseIPLabelImport('address,name,description\n192.0.2.1,경계 라우터,서울', 'labels.csv'), [
  { ip: '192.0.2.1', label: '경계 라우터', note: '서울' }
]);
assert.equal(isIPAddress('192.0.2.10'), true);
assert.equal(isIPAddress('999.0.0.1'), false);
assert.equal(isIPAddress('2001:db8::1'), true);
assert.equal(isIPAddress('2001:db8::1::2'), false);
assert.equal(viewFromHash('#ip-labels'), 'ip-labels');
assert.equal(viewFromHash('#topology'), 'topology');
assert.equal(viewFromHash('#geo-map'), 'geo-map');
assert.equal(viewFromHash('#unknown'), 'diagnostics');
const views = [
  { dataset: { view: 'diagnostics' }, hidden: false },
  { dataset: { view: 'topology' }, hidden: true },
  { dataset: { view: 'ip-labels' }, hidden: true }
];
const links = ['diagnostics', 'topology', 'ip-labels'].map(view => ({
  dataset: { viewLink: view },
  attributes: {},
  classList: { toggle(name, enabled) { this[name] = enabled; } },
  setAttribute(name, value) { this.attributes[name] = value; },
  removeAttribute(name) { delete this.attributes[name]; }
}));
global.document = {
  querySelectorAll(selector) { return selector === '[data-view]' ? views : links; }
};
assert.equal(activateView('ip-labels'), 'ip-labels');
assert.deepEqual(views.map(view => view.hidden), [true, true, false], 'IP label menu must reveal its view');
assert.deepEqual(links.map(link => Boolean(link.classList.active)), [false, false, true]);
assert.equal(links[2].attributes['aria-current'], 'page');
assert.equal(activateView('invalid'), 'diagnostics');
assert.deepEqual(views.map(view => view.hidden), [false, true, true]);
assert.match(source, /최대 홉 단계/);
assert.match(source, /고유 응답 노드/);
assert.match(source, /selectedTopologyTargets/);
assert.match(source, /data-toggle-unresponsive/);
assert.match(source, /checknetwork\.ip-labels\.v1/);
assert.match(source, /checknetwork\.aggregate-labels\.v1/);
assert.match(markup, /data-view-link="ip-labels"/);
assert.match(markup, /data-view-link="geo-map"/);
assert.match(markup, /id="geo-map-result"/);
assert.match(source, /function renderGeoRouteMap/);
assert.match(source, /node\.geolocation/);
assert.match(source, /node\.asn/);
assert.match(markup, /leaflet@1\.9\.4/, 'geo map must load the interactive map renderer');
assert.match(source, /basemaps\.cartocdn\.com/, 'geo map must use a detailed geographic basemap');
assert.match(source, /CHECKNETWORK_CONFIG\?\.CARTO_BASE_MAP/, 'geo map must accept CARTO_BASE_MAP runtime configuration');
assert.match(source, /sessionStorage\.setItem\(CARTO_BASE_MAP_STORAGE_KEY/, 'geo map must accept a per-tab CARTO setting');
assert.match(source, /separator}key=\$\{encodeURIComponent\(configured\)}/, 'CARTO basemap key must use the key query parameter');
assert.doesNotMatch(source, /access_token=\$\{encodeURIComponent\(configured\)}/, 'CARTO basemap key must not be sent as a CARTO API access token');
assert.match(markup, /id="carto-base-map-form"/, 'geo map must expose CARTO configuration input');
assert.match(markup, /type="password"/, 'CARTO configuration must not be displayed as plain text');
assert.match(source, /geo-map-fullscreen/, 'geo map must provide a large fullscreen view');
assert.match(source, /window\.L\.marker/, 'geo hops must render as interactive map markers');
assert.match(source, /fitBounds/, 'geo map must frame all identified hop locations');
assert.match(markup, /id="topology-label-form"/, 'topology view must expose inline IP label editing');
assert.match(source, /function populateTopologyLabelEditor/);
assert.match(source, /\.map-node\[data-label-address\]/);
assert.match(source, /\.map-node\[data-label-group\]/);
assert.match(markup, /option value="traceroute"/, 'default traceroute target must have a selectable option');
assert.match(source, /function enableTopologyDragging/);
assert.doesNotMatch(source, /CSS\.escape/, 'dragging must not depend on CSS.escape browser support');
assert.match(source, /Math\.max\(34, Math\.min/, 'dragged nodes must remain inside the topology canvas');
console.log('topology renderer tests passed');
