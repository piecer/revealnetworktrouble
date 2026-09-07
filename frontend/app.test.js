import assert from 'node:assert/strict';
import fs from 'node:fs';
import {
  parseIPLabelImport, isIPAddress, viewFromHash, activateView, normalizeIPLabelRows,
  LABEL_FILE, LABEL_RECORDS, LABEL_PAGE, LABEL, NOTE
} from './app.js';

const source = fs.readFileSync(`${import.meta.dirname}/app.js`, 'utf8');
const markup = fs.readFileSync(`${import.meta.dirname}/index.html`, 'utf8');

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
assert.deepEqual([LABEL_FILE, LABEL_RECORDS, LABEL_PAGE, LABEL, NOTE], [1024 * 1024, 500, 100, 256, 1024]);

const normalizedLabels = normalizeIPLabelRows([
  { ip: '10.0.0.10', label: ' first ', note: 'old' },
  Object.assign(Object.create(null), { ip: '10.0.0.2', label: 'second', note: 'n'.repeat(NOTE + 2) }),
  { ip: '10.0.0.10', label: 'last', note: '' },
  { ip: '999.0.0.1', label: 'invalid' },
  ['10.0.0.3', 'array is not a record'],
  { ip: '192.0.2.1', label: '🙂'.repeat(LABEL + 1) }
]);
assert.deepEqual([...normalizedLabels.labels.keys()], ['10.0.0.2', '10.0.0.10', '192.0.2.1']);
assert.equal(normalizedLabels.labels.get('10.0.0.10').label, 'last');
assert.equal([...normalizedLabels.labels.get('192.0.2.1').label].length, LABEL);
assert.equal([...normalizedLabels.labels.get('10.0.0.2').note].length, NOTE);
assert.equal(normalizedLabels.invalid, 2);
assert.equal(normalizedLabels.omitted, 1);

const canonicalAliases = normalizeIPLabelRows([
  { ip: '2001:0DB8:0:0:0:0:0:1', label: 'expanded', note: 'old' },
  { ip: '2001:db8::1', label: 'compact wins', note: 'new' },
  { ip: '::ffff:192.0.2.1', label: 'mapped' },
  { ip: '192.0.2.1', label: 'IPv4 wins' },
  { ip: '2001:db8::10', label: 'ten' },
  { ip: '2001:db8::2', label: 'two' },
  { ip: 'FE80:0:0:0:0:0:0:1%eth%0', label: 'zoned' }
]);
assert.deepEqual([...canonicalAliases.labels.keys()], [
  '192.0.2.1', '2001:db8::1', '2001:db8::2', '2001:db8::10', 'fe80::1%eth%0'
]);
assert.equal(canonicalAliases.labels.get('2001:db8::1').label, 'compact wins');
assert.equal(canonicalAliases.labels.get('192.0.2.1').label, 'IPv4 wins');
assert.equal(canonicalAliases.omitted, 2);

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
  dataset: { viewLink: view }, attributes: {},
  classList: { toggle(name, enabled) { this[name] = enabled; } },
  setAttribute(name, value) { this.attributes[name] = value; },
  removeAttribute(name) { delete this.attributes[name]; }
}));
const fakeDocument = { querySelectorAll(selector) { return selector === '[data-view]' ? views : links; } };
assert.equal(activateView(fakeDocument, 'ip-labels'), 'ip-labels');
assert.deepEqual(views.map(view => view.hidden), [true, true, false]);
assert.equal(links[2].attributes['aria-current'], 'page');
assert.equal(activateView(fakeDocument, 'invalid'), 'diagnostics');
assert.deepEqual(views.map(view => view.hidden), [false, true, true]);

assert.match(source, /TopologyRenderCoordinator/);
assert.match(source, /function drawGeo/);
assert.match(source, /topology_mode:\s*'compact'/);
assert.match(source, /devicePixelRatio/);
assert.match(source, /geo\.segments/);
assert.match(source, /geo\.markers/);
assert.match(source, /function destroy/);
assert.doesNotMatch(source, /function renderTopologyMap/);
assert.doesNotMatch(source, /function renderGeoRouteMap/);
assert.doesNotMatch(source, /enableTopologyDragging/);
assert.doesNotMatch(source, /window\.L\./);
assert.doesNotMatch(source, /CARTO_BASE_MAP/);
assert.doesNotMatch(markup, /leaflet@1\.9\.4/);
assert.doesNotMatch(markup, /id="carto-base-map-form"/);
assert.match(markup, /id="topology-fullscreen"/);
assert.match(markup, /id="geo-map-fullscreen"/);
assert.match(markup, /id="geo-render-status"[^>]*aria-live="polite"/);
assert.match(markup, /id="topology-label-form"/);
assert.match(markup, /option value="traceroute"/);
assert.match(markup, /<fieldset[^>]*id="topology-view-controls"/);
assert.match(markup, /<input[^>]*type="radio"[^>]*name="topology-view-mode"[^>]*value="2d"[^>]*checked/);
assert.match(markup, /2D 그래프/);
assert.match(markup, /<input[^>]*type="radio"[^>]*name="topology-view-mode"[^>]*value="3d"/);
assert.match(markup, /3D 그래프/);
assert.match(markup, /<button[^>]*id="topology-view-reset"[^>]*type="button"/);
assert.match(markup, /id="topology-view-help"/);
assert.match(markup, /id="topology-view-status"[^>]*role="status"/);

console.log('application contract tests passed');
