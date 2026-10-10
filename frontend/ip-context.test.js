import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import crypto from 'node:crypto';

const raw = fs.readFileSync(new URL('../testdata/ip-context-corpus.json', import.meta.url));
assert.equal(crypto.createHash('sha256').update(raw).digest('hex'), '5976737708e238833b2491f707c7f2365ca73193397407b7b5cda89b6f3a3843');
export const corpus = JSON.parse(raw).cases;
const moduleURL = new URL('./state.js', import.meta.url);
const client = fs.existsSync(moduleURL) ? await import(moduleURL) : {};

test('IP context parser accepts the actual producer IPv4 bytes bound to the request', () => {
  assert.equal(typeof client.parseIPContext, 'function', 'real IP-context parser is missing');
  const c = corpus.find(c => c.id === 'ipv4');
  assert.deepEqual(client.parseIPContext(Buffer.from(c.wire_base64, 'base64'), c.requested_address), c.projection);
  assert.throws(() => client.parseIPContext(Buffer.from(c.wire_base64, 'base64'), '8.8.8.8'));
});

test('same unchanged 192 wire_base64 cases: exact acceptance and projection', async t => {
  assert.equal(corpus.length, 192);
  assert.equal(corpus.filter(c => c.accept).length, 60);
  for (const c of corpus) await t.test(c.id, () => {
    const bytes = Buffer.from(c.wire_base64, 'base64');
    if (c.accept) assert.deepEqual(client.parseIPContext(bytes, c.requested_address), c.projection);
    else assert.throws(() => client.parseIPContext(bytes, c.requested_address), c.id);
  });
});

test('original object descriptors and dense arrays are checked before any hook', () => {
  let hooks = 0;
  const hook = () => { hooks++; return 1; };
  const base = corpus[0].projection;
  const paths = [[], ['reverse_dns'], ['reverse_dns','names'], ['reverse_dns','names',0], ['registration'], ['routing'], ['routing','origins'], ['routing','origins',0], ['routing','origins',0,'rpki']];
  for (const path of paths) for (const mutate of [
    x => Object.defineProperty(x, 'hidden', {value: null}),
    x => Object.defineProperty(x, 'toJSON', {get: hook}),
    x => Object.defineProperty(x, Symbol('extra'), {get: hook}),
    x => Object.setPrototypeOf(x, Object.create(Object.getPrototypeOf(x))),
    x => { const key = Object.keys(x)[0]; Object.defineProperty(x, key, {get: hook}); },
    x => { const key = Object.keys(x)[0]; Object.defineProperty(x, key, {enumerable: false}); }
  ]) {
    const value = structuredClone(base); let target = value;
    for (const part of path) target = target[part]; mutate(target);
    assert.throws(() => client.normalizeIPContext(value, base.address));
    assert.equal(hooks, 0);
  }
  for (const mutation of [a => delete a[0], a => a.length++, a => Object.defineProperty(a, '0', {get: hook})]) {
    const value = structuredClone(base); mutation(value.reverse_dns.names);
    assert.throws(() => client.normalizeIPContext(value, base.address)); assert.equal(hooks, 0);
  }
  assert.deepEqual(client.normalizeIPContext(base, base.address), base);
});

test('all numeric fields retain exact decimal semantics including huge signed zero exponents', () => {
  const wire = Buffer.from(corpus[0].wire_base64, 'base64').toString();
  for (const number of ['0e'+'9'.repeat(9000), '-0.00e-'+'9'.repeat(9000), '0e+'+'0'.repeat(9000)]) {
    assert.deepEqual(client.parseIPContext(Buffer.from(wire.replace('"omitted":0', '"omitted":'+number)), '1.1.1.1'), corpus[0].projection);
  }
  for (const number of ['1.00000000000000000000001', '1e-9000', '4294967295.00000000000001']) {
    assert.throws(() => client.parseIPContext(Buffer.from(wire.replace('"schema_version":1', '"schema_version":'+number)), '1.1.1.1'));
  }
});

test('separate explicit controller sends one bounded POST, not reports or checks', async () => {
  assert.equal(typeof client.createIPContextController, 'function', 'context controller missing');
  const c = corpus[0], requests = [], changes = [];
  const controller = client.createIPContextController({fetchImpl: async (url, init) => {
    requests.push({url, init}); return new Response(Buffer.from(c.wire_base64, 'base64'));
  }, clock: () => Date.parse(c.projection.fetched_at), onChange: s => changes.push(s)});
  controller.setOwner({report: {}, address: c.requested_address, apiBaseURL:'https://api.test', authRevision:0, token:'secret'});
  assert.equal(requests.length, 0);
  await controller.query();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, 'https://api.test/api/v1/ip-context');
  assert.deepEqual(JSON.parse(requests[0].init.body), {address:'1.1.1.1'});
  assert.equal(requests[0].init.redirect, 'error');
  assert.equal(requests[0].init.credentials, 'omit');
  assert.equal(requests[0].init.headers.Authorization, 'Bearer secret');
  assert.equal(controller.getState().phase, 'ready');
  assert.deepEqual(controller.getState().value, c.projection);
  controller.destroy();
});
