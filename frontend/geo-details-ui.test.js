import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';
import { parseResponse } from './state.js';
import { topologyModelFromReport, planTopologyDOM } from './topology-model.js';

const fixture = name => readFileSync(new URL(`../testdata/${name}`, import.meta.url), 'utf8');
const markup = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const richBytes = fixture('geo-details-rich-compact-report.json');
const flush = () => new Promise(resolve => setTimeout(resolve, 0));
async function harness(bytes = richBytes, initial = 'topology') {
  const dom = new JSDOM(markup, {url: `https://ui.example.test/#${initial}`});
  const win = dom.window, document = win.document, queue = [], requests = [];
  win.CanvasRenderingContext2D = function () {};
  win.HTMLCanvasElement.prototype.getContext = () => new Proxy({}, {get: (o,k) => o[k] ?? (() => {}), set: (o,k,v) => (o[k]=v,true)});
  const app = createApp({document, window: win, fetchImpl: async (url, init) => {
    requests.push({url, method: init.method, headers: init.headers, body: JSON.parse(init.body)});
    return {status: 200, ok: true, headers: {get: () => null}, text: async () => bytes};
  }, scheduler: {schedule(fn) {queue.push(fn); return fn;}, cancel() {}}});
  const drain = () => {while (queue.length) queue.shift()();};
  const view = async name => {document.querySelector(`[data-view-link="${name}"]`).click(); await flush(); drain();};
  const close = () => {app.destroy(); win.close();};
  await flush();
  return {dom, win, document, app, requests, drain, view, close};
}

test('producer report to model attaches only canonical matching public nodes and never changes Geo geometry', () => {
  for (const name of ['rich-full', 'rich-compact', 'truncated-compact']) {
    const bytes = fixture(`geo-details-${name}-report.json`), parsed = parseResponse({ok: true, status: 200}, bytes);
    assert.equal(parsed.ok, true);
    const snapshot = JSON.stringify(parsed.report), model = topologyModelFromReport(parsed.report);
    const legacy = structuredClone(parsed.report); delete legacy.geo_details;
    const baseline = topologyModelFromReport(legacy);
    assert.equal(model.nodes.length, baseline.nodes.length);
    assert.deepEqual(planTopologyDOM(model, {view: 'geo'}).geo, planTopologyDOM(baseline, {view: 'geo'}).geo);
    assert.deepEqual(model.geo_details_summary, {total: parsed.report.geo_details.total, omitted: parsed.report.geo_details.omitted});
    const eligible = new Map(parsed.report.geo_details.entries.map(e => [e.address, e]));
    for (const node of model.nodes) {
      assert.deepEqual(node.geo_details, node.public_ip === true ? eligible.get(node.address) : undefined);
      if (node.geo_details) assert.notEqual(node.geo_details, eligible.get(node.address));
    }
    assert.equal(JSON.stringify(parsed.report), snapshot);
    if (name === 'rich-compact') {
      const noPoint = model.nodes.find(n => n.address === '1.1.1.1');
      assert.equal(noPoint.geolocation, undefined);
      assert.equal(noPoint.geo_details.city, 'Text without coordinates');
      assert.equal(planTopologyDOM(model, {view: 'geo'}).geo.markers.some(p => p.node_id === noPoint.id), false);
    }
  }
});

test('real producer selected detail separates supplemental provenance, ISP and missing coordinates from legacy facts', async () => {
  const h = await harness();
  try {
    await h.app.start('topology'); h.drain();
    const input = JSON.parse(richBytes), before = JSON.stringify(h.app.getState().topology.result);
    await h.view('geo-map');
    const model = topologyModelFromReport(parseResponse({ok: true, status: 200}, richBytes).report);
    const located = model.nodes.find(n => n.address === '8.8.8.8');
    h.document.querySelector(`.topology-geo-list button[data-node-id="${located.id}"]`).click();
    const detail = h.document.querySelector('#geo-node-detail');
    const expected = input.geo_details.entries.find(e => e.address === '8.8.8.8');
    for (const value of Object.values(expected)) assert.ok(detail.textContent.includes(value), value);
    for (const label of ['ISP', '시간대', '우편번호', '대륙', '지역 코드', '네트워크 도메인', '로컬 조회 완료', '캐시 만료']) assert.ok(detail.textContent.includes(label), label);
    assert.match(detail.textContent, /보충.*정보.*좌표.*ASN.*별도/);
    assert.match(detail.textContent, /데이터베이스 갱신.*아님/);
    assert.match(detail.textContent, /위도 0, 경도 0/);
    assert.ok(detail.textContent.includes(located.asn.organization));
    assert.equal(detail.children.length, 0, 'untrusted facts remain textContent, not HTML');
    assert.equal(JSON.stringify(h.app.getState().topology.result), before);
    await h.view('topology');
    const unlocated = model.nodes.find(n => n.address === '1.1.1.1');
    h.document.querySelector(`.topology-node[data-node-id="${unlocated.id}"]`).click(); h.drain();
    await h.view('geo-map');
    assert.equal(h.document.querySelector('.topology-geo-canvas').dataset.selectedNodeId, unlocated.id);
    assert.match(detail.textContent, /Text without coordinates/);
    assert.match(detail.textContent, /Australia\/Sydney/);
    assert.match(detail.textContent, /좌표 미확인/);
    assert.match(detail.textContent, /ISP 미확인/);
    assert.doesNotMatch(detail.textContent, /위도 0|Different access ISP/);
    assert.ok(h.document.querySelectorAll('*').length <= 1200);
  } finally {h.close();}
});

test('raw-scope sidecar entries absent from compact nodes stay valid and only aggregate omissions enter bounded status', async () => {
  const bytes = fixture('geo-details-truncated-compact-report.json');
  const h = await harness(bytes);
  try {
    await h.app.start('topology'); h.drain(); await h.view('geo-map');
    assert.equal(h.app.getState().topology.phase, 'ready');
    assert.match(h.document.querySelector('#geo-map-message').textContent, /보충.*501.*생략.*1/);
    assert.ok(h.document.querySelectorAll('.topology-geo-list button').length <= 100);
    assert.ok(h.document.querySelectorAll('*').length <= 1200);
    h.win.HTMLCanvasElement.prototype.getContext = () => null;
    await h.view('topology'); await h.view('geo-map');
    assert.match(h.document.querySelector('#geo-map-message').textContent, /보충.*501.*생략.*1/);
  } finally {h.close();}
});

for (const reflected of [false, true]) test(`sidecar raw JSON export retained, human export private, credential redaction=${reflected}`, async () => {
  const input = JSON.parse(fixture('geo-details-rich-full-report.json'));
  const secret = 'sidecar-secret';
  if (reflected) input.geo_details.entries[2].isp = `ISP ${secret}`;
  const h = await harness(JSON.stringify(input), 'diagnostics');
  let exported = '';
  try {
    h.win.Blob = class {constructor(parts) {exported = parts.join('');}};
    h.win.URL.createObjectURL = () => 'blob:report'; h.win.URL.revokeObjectURL = () => {};
    h.win.HTMLAnchorElement.prototype.click = () => {};
    if (reflected) {
      h.document.querySelector('#public-auth-enabled').checked = true;
      h.document.querySelector('#bearer-token').value = secret;
      h.document.querySelector('#apply-bearer').click();
    }
    await h.app.start('diagnostics'); h.drain();
    assert.equal(h.app.getState().diagnostics.phase, 'ready');
    assert.equal(h.requests.length, 1);
    assert.match(h.requests[0].url, /\?geo_details=1$/);
    h.document.querySelector('#download').click();
    const raw = JSON.parse(exported);
    if (!reflected) assert.deepEqual(raw.geo_details, input.geo_details);
    else {
      assert.equal(raw.geo_details.entries[2].isp, 'ISP [REDACTED CREDENTIAL]');
      for (const text of [exported, JSON.stringify(h.app.getState()), h.document.body.textContent]) assert.ok(!text.includes(secret));
      assert.equal(h.requests[0].headers.Authorization, `Bearer ${secret}`);
    }
    h.document.querySelector('#download-human').click();
    for (const value of ['Different access ISP', 'Asia/Seoul', 'example.net', '04527', '8.8.8.8', secret]) assert.ok(!exported.includes(value), value);
  } finally {h.close();}
});

test('opt-in is exactly one unchanged POST and old-server no-sidecar success needs no probe or retry', async () => {
  const legacy = JSON.parse(richBytes); delete legacy.geo_details;
  const h = await harness(JSON.stringify(legacy));
  try {
    h.document.querySelector('#topology-targets').value = '8.8.8.8';
    await h.app.start('topology'); h.drain();
    assert.equal(h.app.getState().topology.phase, 'ready');
    assert.equal(h.requests.length, 1);
    assert.equal(h.requests[0].url, 'http://localhost:9090/api/v1/reports?geo_details=1');
    assert.equal(h.requests[0].method, 'POST');
    assert.deepEqual(h.requests[0].headers, {'Content-Type': 'application/json'});
    assert.deepEqual(Object.keys(h.requests[0].body).sort(), ['targets', 'timeout_ms', 'topology_mode']);
    assert.equal(h.requests[0].body.topology_mode, 'compact');
    assert.deepEqual(Object.keys(h.requests[0].body.targets[0]).sort(), ['address', 'attempts', 'kind']);
    assert.equal(Object.hasOwn(h.app.getState().topology.result, 'geo_details'), false);
  } finally {h.close();}
});
