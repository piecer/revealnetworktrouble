import test from 'node:test';
import assert from 'node:assert/strict';
import {createIPContextController} from './state.js';

test('total deadline fires even when the wall clock stalls or moves backwards',async()=>{
  let timer;const controller=createIPContextController({clock:()=>stamp,setTimer:f=>(timer=f,1),clearTimer:()=>{},fetchImpl:()=>new Promise(()=>{})});
  controller.setOwner({report:{},address:'1.1.1.1',apiBaseURL:'http://localhost',authRevision:0});
  const pending=controller.query();timer();
  assert.equal(controller.getState().phase,'timeout');
  assert.equal(await pending,false);controller.destroy();
});

test('a deadline scheduling failure cannot issue an unbounded fetch or retain a lease',async()=>{
  let calls=0;const controller=createIPContextController({setTimer:()=>{throw new Error('timer broken');},fetchImpl:()=>{calls++;return new Promise(()=>{});}});
  controller.setOwner({report:{},address:'1.1.1.1',apiBaseURL:'http://localhost',authRevision:0});
  assert.equal(await controller.query(),false);assert.equal(controller.getState().phase,'unavailable');
  assert.equal(await controller.query(),false);assert.equal(calls,0);controller.destroy();
});

test('late timer from a completed same-owner request cannot replace ready cache state',async()=>{
  let timer;const controller=createIPContextController({clock:()=>stamp,setTimer:f=>(timer=f,1),clearTimer:()=>{},fetchImpl:async()=>new Response(JSON.stringify(snapshot()))});
  controller.setOwner({report:{},address:'1.1.1.1',apiBaseURL:'http://localhost',authRevision:0});
  assert.equal(await controller.query(),true);assert.equal(await controller.query(),true);
  const ready=controller.getState();timer();assert.equal(controller.getState(),ready);controller.destroy();
});

const stamp = Date.parse('2026-01-02T03:04:05.006Z');
const snapshot = (address = '1.1.1.1', time = stamp) => ({schema_version:1,address,source:'upstream',fetched_at:new Date(time).toISOString(),expires_at:new Date(time+300000).toISOString(),reverse_dns:{status:'not_found',source:'system_resolver',fetched_at:new Date(time).toISOString(),names:[],omitted:0},registration:{status:'not_found',source:'rdap',fetched_at:new Date(time).toISOString()},routing:{status:'not_found',source:'ripe_ris',fetched_at:new Date(time).toISOString(),origins:[],omitted:0}});
const response = (address, time) => new Response(JSON.stringify(snapshot(address,time)));
const owner = (report = {}, address = '1.1.1.1') => ({report,address,apiBaseURL:'https://api.test',authRevision:0,token:''});
const deferred = () => {let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};
const tick = () => new Promise(r => setImmediate(r));

for (const late of ['success','error']) test(`selection revocation fences stale ${late}/finally; one actual flight stays charged`, async () => {
  const gate = deferred(), requests = [], changes = [];
  const c = createIPContextController({fetchImpl:(url,init)=>{requests.push(init);return gate.promise;},clock:()=>stamp,onChange:s=>changes.push(s)});
  const first = owner(); c.setOwner(first); const pending=c.query();
  c.setOwner({...first,address:'8.8.8.8'});
  assert.equal(requests[0].signal?.aborted,true,'selection must abort transport');
  await c.query(); assert.equal(requests.length,1,'noncooperative old request retains lease');
  const before=JSON.stringify(changes);
  if(late==='success')gate.resolve(response());else gate.reject(new Error('secret raw error'));
  await pending;await tick();
  assert.equal(JSON.stringify(changes),before,'stale callbacks cannot even clear busy/finally');
  assert.notEqual(c.getState().phase,'ready'); c.destroy();
});

test('one total 10s deadline covers headers, stream and post-parse publication', async () => {
  let now=stamp, sequence=0;const timers=new Map(), gate=deferred();let init;
  const c=createIPContextController({fetchImpl:(url,i)=>{init=i;return gate.promise;},clock:()=>now,monotonicClock:()=>now,
    setTimer:(fn,ms)=>{assert.equal(ms,10000);timers.set(++sequence,fn);return sequence;},clearTimer:id=>timers.delete(id)});
  c.setOwner(owner());const pending=c.query();now+=10000;[...timers.values()][0]();
  await pending;assert.equal(init.signal.aborted,true);assert.equal(c.getState().phase,'timeout');
  gate.resolve(response());await tick();assert.equal(c.getState().phase,'timeout');assert.equal(timers.size,0);c.destroy();
});

test('delayed reader is revoked and cancelled without waiting for a cooperative body', async () => {
  let cancel=0,release=0;const gate=deferred();
  const c=createIPContextController({clock:()=>stamp,fetchImpl:async()=>({status:200,headers:{get:()=>null},body:{getReader:()=>({read:()=>gate.promise,cancel:()=>{cancel++;return Promise.resolve();},releaseLock:()=>release++})}})});
  const first=owner();c.setOwner(first);const pending=c.query();await tick();c.cancel();await pending;
  assert.equal(cancel,1);assert.equal(c.getState().phase,'cancelled');
  gate.resolve({done:false,value:new TextEncoder().encode(JSON.stringify(snapshot()))});await tick();
  assert.equal(c.getState().phase,'cancelled');assert.equal(release,1);c.destroy();
});

for(const mode of ['declared','stream','utf8','duplicate','redirect','foreign','404','429','503'])test(`bounded transport ${mode} fails locally with safe status, no retry`,async()=>{
  let calls=0,reads=0,cancels=0;
  const c=createIPContextController({clock:()=>stamp,fetchImpl:async()=>{calls++;
    if(mode==='404'||mode==='429'||mode==='503')return new Response('secret provider message',{status:+mode});
    if(mode==='redirect')return {status:200,redirected:true,body:{cancel:()=>{cancels++;}}};
    const bytes=mode==='utf8'?Uint8Array.of(0xff):mode==='duplicate'?new TextEncoder().encode('{"x":1,"x":2}'):new TextEncoder().encode(JSON.stringify(snapshot('8.8.8.8')));
    if(mode==='declared'||mode==='stream')return {status:200,headers:{get:()=>mode==='declared'?'16385':null},body:{getReader:()=>({read:async()=>{reads++;return {value:new Uint8Array(16385),done:false};},cancel:()=>{cancels++;},releaseLock(){}}),cancel:()=>{cancels++;}}};
    return new Response(bytes);
  }});
  c.setOwner(owner());await c.query();assert.equal(calls,1);assert.notEqual(c.getState().phase,'ready');
  assert.ok(!JSON.stringify(c.getState()).includes('secret'));
  if(mode==='404')assert.equal(c.getState().phase,'unsupported');
  if(mode==='declared')assert.equal(reads,0);
  if(mode==='stream'){assert.equal(reads,1);assert.equal(cancels,1);}
  c.destroy();
});

test('memory cache uses server expiry, ≤64 LRU entries and report/base/auth isolation without rewriting facts',async()=>{
  let now=stamp,calls=0;
  const c=createIPContextController({clock:()=>now,fetchImpl:async(url,init)=>{calls++;return response(JSON.parse(init.body).address,now);}});
  let o=owner();c.setOwner(o);await c.query();const original=structuredClone(c.getState().value);
  await c.query();assert.equal(calls,1);assert.equal(c.getState().appCache,true);assert.deepEqual(c.getState().value,original);
  now+=300000;await c.query();assert.equal(calls,2);assert.equal(c.getState().appCache,false);
  for(let i=1;i<=65;i++){c.setOwner({...o,address:`8.8.0.${i}`});await c.query();}
  const after=calls;c.setOwner(o);await c.query();assert.equal(calls,after+1,'evicted oldest address');
  for(const change of [{report:{}},{apiBaseURL:'https://other.test'},{authRevision:1}]){o={...o,...change};c.setOwner(o);const before=calls;await c.query();assert.equal(calls,before+1);}
  c.destroy();
});

test('destroy revokes completed and in-flight owners, and invalid addresses admit zero network',async()=>{
  let calls=0;const c=createIPContextController({clock:()=>stamp,fetchImpl:async()=>{calls++;return response();}});
  for(const address of ['10.0.0.1','example.com','1.01.1.1','::ffff:1.1.1.1']){c.setOwner(owner({},address));await c.query();}
  assert.equal(calls,0);c.setOwner(owner());c.destroy();await c.query();assert.equal(calls,0);
});

test('native elapsed deadline rejects a late response before timer dispatch with frozen wall time', async () => {
  let calls = 0; const changes = [], started = performance.now();
  const c = createIPContextController({clock:()=>stamp, onChange:s=>changes.push(s.phase), fetchImpl:async()=>{
    calls++; if (calls === 1) Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 10030);
    return response();
  }});
  c.setOwner(owner());
  try {
    assert.equal(await c.query(), false); assert.ok(performance.now() - started >= 10000);
    assert.equal(c.getState().phase, 'timeout'); assert.ok(!changes.includes('ready'));
    await tick(); assert.equal(await c.query(), true); assert.equal(calls, 2, 'late result must not enter app cache');
  } finally { c.destroy(); }
});

for (const stage of ['loading', 'headers', 'read-entry', 'read-return', 'validation', 'expiry', 'http-error', 'fetch-error']) test(`monotonic admission rejects overdue ${stage} independently of wall time`, async () => {
  let elapsed = 0, calls = 0, reads = 0, complete = false; const phases = [], timers = [];
  const decode = TextDecoder.prototype.decode;
  if (stage === 'validation') TextDecoder.prototype.decode = function(...args) { const value = decode.apply(this, args); elapsed = 10000; return value; };
  const c = createIPContextController({monotonicClock:()=>elapsed,
    clock:()=>{ if (stage === 'expiry' && complete) elapsed = 10000; return stamp; },
    setTimer:(fn,ms)=>{ assert.equal(ms,10000); timers.push(fn); return timers.length; }, clearTimer:()=>{},
    onChange:s=>{ phases.push(s.phase); if (s.phase === 'loading' && stage === 'loading') elapsed = 10000; },
    fetchImpl:async()=>{
      calls++;
      if (stage === 'headers') elapsed = 10000;
      if (stage === 'fetch-error') { elapsed = 10000; throw new Error('private provider failure'); }
      if (stage === 'http-error') return {status:503,body:{cancel() { elapsed = 10000; }}};
      if (['read-entry','read-return','expiry'].includes(stage)) return {status:200,headers:{get:()=>null},body:{getReader() {
        if (stage === 'read-entry') elapsed = 10000;
        return {async read() { reads++; if (stage === 'read-return') elapsed = 10000; complete = reads > 1; return complete ? {done:true} : {done:false,value:new TextEncoder().encode(JSON.stringify(snapshot()))}; },cancel() {},releaseLock() {}};
      }}};
      return response();
    }});
  c.setOwner(owner());
  try {
    assert.equal(await c.query(), false); assert.equal(c.getState().phase, 'timeout'); assert.ok(!phases.includes('ready'));
    if (stage === 'loading') assert.equal(calls, 0, 'no fetch after pre-admission expiry');
    if (stage === 'read-entry') assert.equal(reads, 0, 'no read after reader acquisition consumed deadline');
  } finally { TextDecoder.prototype.decode = decode; c.destroy(); }
});

test('monotonic expiry settles timeout while late body disposal stays charged', async () => {
  const cleanup = deferred(); let elapsed = 0, calls = 0, cancels = 0;
  const c = createIPContextController({clock:()=>stamp,monotonicClock:()=>elapsed,fetchImpl:async()=>{
    calls++; if (calls > 1) return response(); elapsed = 10000;
    return new Response(new ReadableStream({cancel() { cancels++; return cleanup.promise; }}));
  }});
  c.setOwner(owner());
  try {
    const pending = c.query(); await tick(); assert.equal(c.getState().phase, 'timeout');
    assert.equal(await pending, false); assert.equal(cancels, 1);
    assert.equal(await c.query(), false); assert.equal(calls, 1);
    cleanup.resolve(); await tick(); assert.equal(await c.query(), true); assert.equal(calls, 2);
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

test('wall expiry and rollback do not change an on-time monotonic request deadline', async () => {
  let wall = stamp, elapsed = 500, calls = 0;
  const c = createIPContextController({clock:()=>wall,monotonicClock:()=>elapsed,fetchImpl:async()=>{ calls++; elapsed += 9999; wall -= 100000; return response(); }});
  c.setOwner(owner()); assert.equal(await c.query(), true); assert.equal(await c.query(), true); assert.equal(calls, 1);
  wall = stamp + 300000; assert.equal(await c.query(), true); assert.equal(calls, 2, 'server expiry uses wall time, not elapsed time'); c.destroy();
});

// A real stream settles a pending read before asynchronous source cancellation.
for (const action of ['cancel', 'replace', 'destroy']) test(`physical stream cleanup retains its lease after ${action}`, async () => {
  const cleanup = deferred(); let calls = 0, cancels = 0, finished = false;
  const stream = new ReadableStream({pull() {}, async cancel() { cancels++; await cleanup.promise; finished = true; }});
  const c = createIPContextController({clock:()=>stamp, fetchImpl:async()=>{ calls++; return calls === 1 ? new Response(stream) : response(); }});
  const first = owner(); c.setOwner(first);
  const pending = c.query(); await tick();
  if (action === 'replace') c.setOwner({...first, address:'8.8.8.8'});
  else if (action === 'destroy') c.destroy();
  else { c.cancel(); c.cancel(); }
  try {
    assert.equal(await pending, false, 'user cancellation must not wait for physical cleanup');
    await tick(); assert.equal(cancels, 1); assert.equal(finished, false);
    const state = c.getState();
    assert.equal(await c.query(), false); assert.equal(calls, 1, 'pending source cancel must retain physical lease');
    cleanup.resolve(); await tick(); assert.equal(finished, true); assert.equal(c.getState(), state, 'stale cleanup cannot publish');
    if (action !== 'destroy') { c.setOwner(first); assert.equal(await c.query(), true); assert.equal(calls, 2); }
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

for (const disposition of ['late-headers', 'http-error', 'declared-limit', 'stream-limit']) test(`physical cancellation owns held ${disposition} disposal`, async () => {
  const headers = deferred(), cleanup = deferred(); let calls = 0, cancels = 0;
  const stream = new ReadableStream({
    start(controller) { if (disposition === 'stream-limit') controller.enqueue(new Uint8Array(16385)); },
    cancel() { cancels++; return cleanup.promise; }
  });
  const firstResponse = new Response(stream, {status:disposition === 'http-error' ? 503 : 200, headers:disposition === 'declared-limit' ? {'Content-Length':'16385'} : {}});
  const c = createIPContextController({clock:()=>stamp, fetchImpl:()=>{ calls++; return calls === 1 ? headers.promise : Promise.resolve(response()); }});
  c.setOwner(owner()); const pending = c.query();
  if (disposition === 'late-headers') { c.cancel(); assert.equal(await pending, false); }
  headers.resolve(firstResponse);
  try {
    assert.equal(await pending, false, 'failure outcome must not wait for disposal'); await tick();
    assert.equal(cancels, 1); assert.equal(await c.query(), false); assert.equal(calls, 1);
    cleanup.resolve(); await tick(); assert.equal(await c.query(), true); assert.equal(calls, 2);
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

for (const mode of ['reject', 'throw']) test(`physical cleanup ${mode} retires once without stale publication`, async () => {
  const cleanup = deferred(); let calls = 0, cancels = 0;
  const c = createIPContextController({clock:()=>stamp, fetchImpl:async()=>{
    calls++; if (calls > 1) return response();
    return {status:503,body:{cancel() { cancels++; if (mode === 'throw') throw new Error('private cleanup'); return cleanup.promise; }}};
  }});
  c.setOwner(owner());
  try {
    assert.equal(await c.query(), false); assert.equal(c.getState().phase, 'busy');
    c.cancel(); c.cancel(); assert.equal(cancels, 1);
    if (mode === 'reject') { assert.equal(await c.query(), false); assert.equal(calls, 1); cleanup.reject(new Error('private cleanup')); }
    await tick(); const cancelled = c.getState(); await tick(); assert.equal(c.getState(), cancelled);
    assert.equal(await c.query(), true); assert.equal(calls, 2);
    assert.ok(!JSON.stringify(c.getState()).includes('private cleanup'));
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

test('settled HTTP failure cannot be replaced by a late timer during held cleanup', async () => {
  const cleanup = deferred(); let timer;
  const c = createIPContextController({clock:()=>stamp,setTimer:fn=>(timer=fn,1),clearTimer:()=>{},
    fetchImpl:async()=>new Response(new ReadableStream({cancel:()=>cleanup.promise}),{status:503})});
  c.setOwner(owner());
  try {
    assert.equal(await c.query(),false); const failed = c.getState(); assert.equal(failed.phase,'busy');
    timer(); assert.equal(c.getState(),failed); assert.equal(await c.query(),false);
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

test('external owner revocation cancels the unfinished body and owns its cleanup', async () => {
  const cleanup = deferred(); let current = true, source, cancels = 0, calls = 0;
  const stream = new ReadableStream({start(controller) { source=controller; },cancel() { cancels++; return cleanup.promise; }});
  const c = createIPContextController({clock:()=>stamp,isCurrentOwner:()=>current,
    fetchImpl:async()=>{ calls++; return calls === 1 ? new Response(stream) : response(); }});
  c.setOwner(owner()); const pending = c.query(); await tick(); current = false;
  source.enqueue(new TextEncoder().encode('{}'));
  try {
    assert.equal(await pending,false); assert.equal(cancels,1);
    current = true; assert.equal(await c.query(),false); assert.equal(calls,1);
    cleanup.resolve(); await tick(); assert.equal(await c.query(),true); assert.equal(calls,2);
  } finally { cleanup.resolve(); await tick(); c.destroy(); }
});

// Cleanup finishes while a successor's headers remain held: old finally/timer has no authority.
test('retired stream cleanup cannot release a successor physical flight', async () => {
  const cleanup = deferred(), successor = deferred(); const timers = []; let calls = 0;
  const c = createIPContextController({clock:()=>stamp, setTimer:fn=>(timers.push(fn),timers.length), clearTimer:()=>{},
    fetchImpl:async()=>{ calls++; return calls === 1 ? new Response(new ReadableStream({cancel:()=>cleanup.promise})) : successor.promise; }});
  c.setOwner(owner()); const first = c.query(); await tick(); c.cancel(); assert.equal(await first, false);
  try {
    await tick(); const blocked = c.query(); assert.equal(calls, 1); assert.equal(await blocked, false);
    cleanup.resolve(); await tick(); const second = c.query(); timers[0](); await tick();
    assert.equal(c.getState().phase, 'loading'); assert.equal(await c.query(), false); assert.equal(calls, 2);
    successor.resolve(response()); assert.equal(await second, true);
  } finally { cleanup.resolve(); successor.resolve(response()); await tick(); c.destroy(); }
});

test('credential transport keeps HTTPS/loopback restriction and never consumes cookies',async()=>{
  let calls=0;const c=createIPContextController({clock:()=>stamp,fetchImpl:async()=>{calls++;return response();}});
  c.setOwner({...owner(),apiBaseURL:'http://remote.test',token:'secret'});await c.query();assert.equal(calls,0);assert.equal(c.getState().phase,'insecure-auth');c.destroy();
});
