import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { normalizeReport, parseResponse } from './state.js';
import { topologyModelFromReport } from './topology-model.js';

const response = {status: 200, ok: true};
const fixture = name => readFileSync(new URL(`../testdata/${name}`, import.meta.url), 'utf8');
const richBytes = fixture('geo-details-rich-compact-report.json');
const rich = () => JSON.parse(richBytes);
const corpus = JSON.parse(fixture('geo-details-corpus.json'));
function corpusBytes(entry) {
  let sidecar = corpus.bases[entry.base];
  assert.equal(typeof sidecar, 'string');
  for (const edit of entry.edits) {
    assert.ok(sidecar.includes(edit.old), `missing exact edit in ${entry.name}`);
    sidecar = sidecar.replace(edit.old, () => edit.new);
  }
  return corpus.report_prefix + sidecar + (entry.duplicate_root ? ',"geo_details":' + sidecar : '') + corpus.report_suffix;
}

test('frozen corpus collection is exactly 165 cases, 27 accept and 138 reject', () => {
  assert.equal(corpus.cases.length, 165);
  assert.equal(new Set(corpus.cases.map(c => c.name)).size, 165);
  assert.equal(corpus.cases.filter(c => c.accept).length, 27);
  assert.equal(corpus.cases.filter(c => !c.accept).length, 138);
});
for (const entry of corpus.cases) test(`raw corpus: ${entry.name} => ${entry.accept ? 'accept' : 'reject'}`, t => {
  // Preserve raw spellings and duplicates: no intermediate JSON.parse/re-encode.
  const bytes = corpusBytes(entry), parsed = parseResponse(response, bytes);
  t.diagnostic(JSON.stringify({case: entry.name, expected: entry.accept, actual: parsed.ok, bytes: Buffer.byteLength(bytes)}));
  assert.equal(parsed.ok, entry.accept, entry.name);
  if (!parsed.ok) assert.equal(parsed.error.code, 'invalid_response');
});
for (const name of ['rich-full', 'rich-compact', 'empty-full', 'empty-compact', 'truncated-compact']) {
  test(`complete real producer witness: ${name}`, () => {
    const bytes = fixture(`geo-details-${name}-report.json`), parsed = parseResponse(response, bytes);
    assert.equal(parsed.ok, true);
    assert.deepEqual(parsed.report.geo_details, JSON.parse(bytes).geo_details);
  });
}

test('sidecar numbers never round fractional or underflowing raw decimals into valid integers', () => {
  for (const [old, replacement] of [
    ['"schema_version":1', '"schema_version":1.00000000000000000000001'],
    ['"total":1', '"total":0.9999999999999999999999'],
    ['"omitted":0', '"omitted":1e-9999'],
    ['"omitted":0', '"omitted":-1e-9999']
  ]) {
    const sidecar = corpus.bases.minimal.replace(old, replacement);
    assert.notEqual(sidecar, corpus.bases.minimal);
    assert.equal(parseResponse(response, corpus.report_prefix + sidecar + corpus.report_suffix).ok, false, replacement);
  }
});

test('producer-review companion precision and huge equivalent-integral spellings use exact raw decimals', () => {
  const cases = [
    ['1.0000000000000000000001', '1', '0', false],
    ['1', '6200.0000000000000001', '6199', false],
    ['1', '1', '1e-9999', false],
    ['1', '1', '-1e-9999', false],
    ['1', '1', '0e999999999999999999999999999999999', true],
    ['1', '1', '-0e-99999999999999999999999999999999', true],
    ['1e+' + '0'.repeat(2000), '1', '0', true],
    ['1' + '0'.repeat(2000) + 'e-2000', '1', '0', true],
    ['1', '6200' + '0'.repeat(2000) + 'e-2000', '6199', true],
    ['1', '6201' + '0'.repeat(2000) + 'e-2000', '6200', false],
    ['1e9999999999999999999999', '1', '0', false],
    ['1e-9999999999999999999999', '1', '0', false]
  ];
  for (const [version, total, omitted, accept] of cases) {
    const sidecar = corpus.bases.minimal.replace('"schema_version":1', `"schema_version":${version}`)
      .replace('"total":1', `"total":${total}`).replace('"omitted":0', `"omitted":${omitted}`);
    assert.equal(parseResponse(response, corpus.report_prefix + sidecar + corpus.report_suffix).ok, accept,
      JSON.stringify({version: version.slice(0, 60), total: total.slice(0, 60), omitted}));
  }
});

test('legacy-only duplicate-key lexical behavior remains unchanged', () => {
  const input = rich(); delete input.geo_details;
  const bytes = JSON.stringify(input).replace('"id":', '"id":"predecessor", "id":');
  assert.equal(parseResponse(response, bytes).ok, true);
});

test('direct object sidecar boundaries reject hidden hooks, sparse arrays and exotic shapes without invoking getters', () => {
  let calls = 0;
  const getter = () => { calls++; throw new Error('getter must never run'); };
  const mutations = [
    r => Object.defineProperty(r, 'geo_details', {value: r.geo_details, enumerable: false}),
    r => Object.defineProperty(r, 'geo_details', {get: getter}),
    r => { r.geo_details = undefined; },
    r => { r.geo_details = Object.create(r.geo_details); },
    r => { r.geo_details[Symbol('extra')] = 'no'; },
    r => Object.defineProperty(r.geo_details, 'toJSON', {value: getter}),
    r => Object.defineProperty(r.geo_details, 'total', {get: getter}),
    r => { r.geo_details.entries[0] = Object.create(r.geo_details.entries[0]); },
    r => { r.geo_details.entries[0].city = undefined; },
    r => { r.geo_details.entries[0][Symbol('extra')] = 'no'; },
    r => Object.defineProperty(r.geo_details.entries[0], 'city', {get: getter}),
    r => Object.defineProperty(r.geo_details.entries[0], 'address', {value: '1.1.1.1', enumerable: false}),
    r => Object.defineProperty(r.geo_details.entries[0], 'toJSON', {value: getter}),
    r => Object.defineProperty(r.geo_details.entries, '0', {get: getter}),
    r => Object.defineProperty(r.geo_details.entries, '0', {value: r.geo_details.entries[0], enumerable: false}),
    r => Object.defineProperty(r.geo_details.entries, 'toJSON', {value: getter}),
    r => { r.geo_details.entries[Symbol('extra')] = 'no'; },
    r => { r.geo_details.entries.extra = 1; },
    r => { delete r.geo_details.entries[1]; },
    r => { Object.setPrototypeOf(r.geo_details.entries, Object.create(Array.prototype)); }
  ];
  const accepted = [];
  mutations.forEach((mutate, index) => {
    const input = rich(); mutate(input);
    try { normalizeReport(input); accepted.push(index); } catch { /* expected rejection */ }
    assert.equal(calls, 0, `mutation ${index} invoked a hook`);
  });
  assert.deepEqual(accepted, [], 'no malformed object shape may be silently accepted');
});

// Exercise the original object at both public boundaries, before any lossy copy.
const originalSidecarMutations = [
  ['hidden root sidecar', r => Object.defineProperty(r, 'geo_details', {value: r.geo_details, enumerable: false})],
  ['hidden envelope extra', r => Object.defineProperty(r.geo_details, 'extra', {value: 7})],
  ['symbol envelope extra', r => { r.geo_details[Symbol('extra')] = 7; }],
  ['hidden entry toJSON', (r, hook) => Object.defineProperty(r.geo_details.entries[0], 'toJSON', {value: hook})],
  ['hidden optional city', r => Object.defineProperty(r.geo_details.entries[0], 'city', {value: 'hidden', enumerable: false})],
  ['symbol entry extra', r => { r.geo_details.entries[0][Symbol('extra')] = 7; }],
  ['symbol array extra', r => { r.geo_details.entries[Symbol('extra')] = 7; }],
  ['trailing array hole with unchanged counts', r => { r.geo_details.entries.length++; }],
  ['own enumerable __proto__ envelope key', r => Object.defineProperty(r.geo_details, '__proto__', {value: 7, enumerable: true})],
  ['hidden null root sidecar', r => Object.defineProperty(r, 'geo_details', {value: null, enumerable: false})],
  ['root accessor', (r, hook) => Object.defineProperty(r, 'geo_details', {get: hook})],
  ['envelope accessor', (r, hook) => Object.defineProperty(r.geo_details, 'total', {get: hook})],
  ['entry accessor', (r, hook) => Object.defineProperty(r.geo_details.entries[0], 'city', {get: hook})],
  ['array accessor', (r, hook) => Object.defineProperty(r.geo_details.entries, '0', {get: hook})],
  ['enumerable entry toJSON', (r, hook) => { r.geo_details.entries[0].toJSON = hook; }]
];
for (const [name, mutate] of originalSidecarMutations) test(`original sidecar rejection: ${name}`, () => {
  let calls = 0;
  const hook = () => { calls++; throw new Error('getter/toJSON must never run'); };
  const input = rich(); mutate(input, hook);
  for (const boundary of [normalizeReport, topologyModelFromReport]) {
    try {
      assert.throws(() => boundary(input), TypeError, `${boundary.name} must reject, not sanitize or downgrade`);
    } finally {
      assert.equal(calls, 0, `${boundary.name} invoked a getter/toJSON`);
    }
  }
});

for (const name of ['rich-full', 'rich-compact', 'empty-full', 'empty-compact', 'truncated-compact']) {
  test(`original sidecar producer and mutation isolation: ${name}`, () => {
    const input = JSON.parse(fixture(`geo-details-${name}-report.json`));
    const before = JSON.stringify(input), normalized = normalizeReport(input);
    const model = topologyModelFromReport(input), independent = topologyModelFromReport(structuredClone(input));
    const normalizedModel = topologyModelFromReport(normalized);
    assert.deepEqual(model, independent);
    assert.deepEqual(model.nodes.map(node => node.geo_details), normalizedModel.nodes.map(node => node.geo_details));
    assert.equal(JSON.stringify(input), before);
    assert.deepEqual(model.geo_details_summary, {total: input.geo_details.total, omitted: input.geo_details.omitted});
    assert.ok(Object.isFrozen(model.geo_details_summary));
    const attached = model.nodes.filter(node => node.geo_details);
    if (input.geo_details.entries.length) assert.ok(attached.length > 0);
    for (const node of attached) {
      const original = input.geo_details.entries.find(entry => entry.address === node.address);
      assert.deepEqual(node.geo_details, original);
      assert.notEqual(node.geo_details, original);
      assert.ok(Object.isFrozen(node.geo_details));
      assert.throws(() => { node.geo_details.city = 'model field mutation'; }, TypeError);
    }
    input.geo_details.entries.forEach(entry => { entry.city = 'raw mutation'; });
    assert.deepEqual(model, independent, 'input mutation cannot affect the model snapshot');
    attached.forEach(node => { node.geo_details = {city: 'model replacement'}; });
    model.geo_details_summary = {total: -1, omitted: -1};
    assert.equal(JSON.stringify(normalized.geo_details), JSON.stringify(JSON.parse(before).geo_details));
    assert.deepEqual(topologyModelFromReport(normalized), normalizedModel, 'model mutation cannot affect normalized input');
  });
}

test('original sidecar repair leaves legacy-only snapshot policy unchanged', () => {
  for (const name of ['rich-full', 'rich-compact']) {
    const input = JSON.parse(fixture(`geo-details-${name}-report.json`));
    delete input.geo_details;
    const baseline = topologyModelFromReport(input);
    Object.defineProperty(input, 'unrelated', {value: 7});
    input[Symbol('unrelated')] = 7;
    if (input.compact_topology) {
      Object.defineProperty(input.compact_topology, 'unrelated', {value: 7});
      input.compact_topology[Symbol('unrelated')] = 7;
    }
    const model = topologyModelFromReport(input);
    assert.deepEqual(model, baseline);
    assert.equal(Object.hasOwn(model, 'geo_details_summary'), false);
  }
  assert.equal(topologyModelFromReport({results: []}).source, 'legacy', 'model-only legacy input need not satisfy the full report schema');
});

test('exact Unicode per-field and aggregate UTF-8 limits are independent of JS code-unit length', () => {
  const texts = ['a'.repeat(256), 'é'.repeat(128), '한'.repeat(85) + 'x', '😀'.repeat(64), '<>&"\\\n\u2028\u2029'];
  const keys = ['city', 'region', 'country', 'country_code', 'continent', 'continent_code'];
  for (const text of texts) {
    const r = rich(), entry = {address: '8.8.8.8', provider: 'ipwho.is', source: 'upstream', city: text};
    r.geo_details = {schema_version: 1, total: 1, omitted: 0, entries: [entry]};
    assert.equal(normalizeReport(r).geo_details.entries[0].city, text);
    if (Buffer.byteLength(text) === 256) {
      entry.city += 'x'; assert.throws(() => normalizeReport(r)); entry.city = text;
      keys.forEach(key => entry[key] = text);
      assert.equal(Buffer.byteLength(keys.map(key => entry[key]).join('')), 1536);
      assert.doesNotThrow(() => normalizeReport(r));
      entry.postal = 'x'; assert.throws(() => normalizeReport(r));
    }
  }
  for (const timestamp of ['0001-01-01T00:00:00.000Z', '9999-12-31T23:59:59.999Z', '2000-02-29T00:00:00.000Z']) {
    const r = rich(); r.geo_details.entries[0].fetched_at = timestamp; r.geo_details.entries[0].expires_at = timestamp;
    assert.doesNotThrow(() => normalizeReport(r));
  }
  for (const timestamp of ['0000-01-01T00:00:00.000Z', '1900-02-29T00:00:00.000Z']) {
    const r = rich(); r.geo_details.entries[0].fetched_at = timestamp; r.geo_details.entries[0].expires_at = timestamp;
    assert.throws(() => normalizeReport(r));
  }
});

test('real producer supplemental snapshot survives the response boundary as an independent immutable copy', () => {
  const input = rich(), expected = structuredClone(input.geo_details);
  const parsed = parseResponse(response, richBytes);
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.report.geo_details, expected);
  const normalized = normalizeReport(input);
  assert.notEqual(normalized.geo_details, input.geo_details);
  input.geo_details.entries[0].city = 'mutated after admission';
  assert.deepEqual(normalized.geo_details, expected);
  assert.ok(Object.isFrozen(normalized.geo_details));
  assert.ok(Object.isFrozen(normalized.geo_details.entries));
  assert.ok(Object.isFrozen(normalized.geo_details.entries[0]));
});

test('malformed-present supplemental snapshot atomically rejects instead of silently disappearing', () => {
  const input = rich(); input.geo_details = null;
  assert.equal(parseResponse(response, JSON.stringify(input)).ok, false);
  assert.throws(() => normalizeReport(input), /geo_details/);
});
