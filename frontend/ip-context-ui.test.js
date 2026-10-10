import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {JSDOM} from 'jsdom';
import * as appModule from './app.js';
import {parseResponse} from './state.js';
const markup=readFileSync(new URL('./index.html',import.meta.url),'utf8');
const reportBytes=readFileSync(new URL('../testdata/geo-details-rich-compact-report.json',import.meta.url),'utf8');
const corpus=JSON.parse(readFileSync(new URL('../testdata/ip-context-corpus.json',import.meta.url),'utf8')).cases;
const stamp=Date.parse(corpus[0].projection.fetched_at);
const tick=()=>new Promise(r=>setTimeout(r,0));
export async function harness({bytes=reportBytes,contextResponse,initial='topology'}={}){
 const dom=new JSDOM(markup,{url:`https://ui.test/#${initial}`}),win=dom.window,document=win.document,queue=[],requests=[];
 win.CanvasRenderingContext2D=function(){};
 win.HTMLCanvasElement.prototype.getContext=()=>new Proxy({},{get:(o,k)=>o[k]??(()=>{}),set:(o,k,v)=>(o[k]=v,true)});
 const app=appModule.createApp({document,window:win,clock:()=>stamp,fetchImpl:async(url,init)=>{
  requests.push({url,init});
  if(url.endsWith('/api/v1/ip-context'))return contextResponse?contextResponse(url,init):new Response(Buffer.from(corpus[0].wire_base64,'base64'));
  return {ok:true,status:200,headers:{get:()=>null},text:async()=>bytes};
 },scheduler:{schedule:fn=>(queue.push(fn),fn),cancel(){}}});
 const drain=()=>{while(queue.length)queue.shift()();};
 const view=async name=>{document.querySelector(`[data-view-link="${name}"]`).click();await tick();drain();};
 await tick();
 return {win,document,app,requests,drain,view,close:()=>{app.destroy();win.close();}};
}

test('native selected full-IP action is real in topology 2D/3D and Geo without implicit requests',async()=>{
 const h=await harness();try{
  await h.app.start('topology');h.drain();
  const before=JSON.stringify(h.app.getOwnedReport('topology'));
  const select=h.document.querySelector('[data-ip-context-select]');
  assert.ok(select,'native eligible IP selector missing');assert.equal(select.tagName,'SELECT');
  assert.ok(select.closest('.topology-view-toolbar'),'selector is co-located in graph toolbar');
  assert.ok([...select.options].some(o=>o.value==='1.1.1.1'),'unlocated public IP is eligible');
  select.value='1.1.1.1';select.dispatchEvent(new h.win.Event('change',{bubbles:true}));
  assert.equal(h.requests.length,1);
  const query=h.document.querySelector('[data-ip-context-query]');assert.equal(query.textContent,'IP 추가 정보 조회');
  assert.equal(h.document.querySelector('[data-ip-context-address]').textContent,'1.1.1.1');
  query.click();await tick();h.drain();
  assert.equal(h.requests.length,2);assert.deepEqual(JSON.parse(h.requests[1].init.body),{address:'1.1.1.1'});
  const panel=h.document.querySelector('[data-ip-context-panel]');assert.equal(panel.dataset.state,'ready');assert.match(panel.textContent,/one.example/);
  const three=h.document.querySelector('[name="topology-view-mode"][value="3d"]');three.checked=true;three.dispatchEvent(new h.win.Event('change',{bubbles:true}));h.drain();
  assert.equal(h.document.querySelectorAll('[data-ip-context-query]').length,1);assert.equal(h.requests.length,2);
  await h.view('geo-map');
  const geoPanel=h.document.querySelector('[data-ip-context-panel]');assert.ok(geoPanel.closest('#geo-map-view'));
  assert.ok(!geoPanel.closest('.geo-node-panel'),'supplement outside sticky immutable detail');
  assert.match(h.document.querySelector('#geo-node-detail').textContent,/1.1.1.1/);
  assert.equal(h.requests.length,2);assert.equal(JSON.stringify(h.app.getOwnedReport('topology')),before);
  assert.ok(h.document.querySelectorAll('*').length<=1200);
 }finally{h.close();}
});

test('eligible compact/full positive-hop facts include missing Geo and exclude execution failures, private and synthetic nodes',()=>{
 for(const form of ['full','compact']){
  const raw=readFileSync(new URL(`../testdata/geo-details-rich-${form}-report.json`,import.meta.url),'utf8');
  const report=parseResponse({ok:true,status:200},raw).report;
  const before=JSON.stringify(report),c=appModule.eligibleIPContextAddresses(report);
  assert.deepEqual(c.addresses,['1.1.1.1','208.67.222.222','2606:4700:4700::1111','8.8.8.8','9.9.9.9']);
  assert.equal(c.omitted,0);assert.equal(JSON.stringify(report),before);
 }
 // Validated compact healthy zero/missing-RTT facts must not depend on GeoIP.
 const r=parseResponse({ok:true,status:200},reportBytes).report;
 delete r.compact_topology.nodes.find(n=>n.address==='1.1.1.1').latency_ms_avg;
 assert.ok(appModule.eligibleIPContextAddresses(r).addresses.includes('1.1.1.1'));
});

test('one atomic bounded fragment, exact fit / one deficit / zero capacity, full 500-address paging',()=>{
 const addresses=Array.from({length:500},(_,i)=>`8.2.${Math.floor(i/256)}.${i%256}`);
 const catalog={addresses,total:503,omitted:3};
 const dom=new JSDOM('<!doctype html><html><head><script></script></head><body><main></main><p role="status"></p></body></html>');
 const d=dom.window.document,parent=d.querySelector('main');let calls=0,limited=0,peak=0;
 const mount=()=>appModule.mountIPContextView({document:d,parent,catalog,selectedAddress:addresses[0],onQuery:()=>calls++,onLimited:()=>limited++});
 let first=mount();const cost=parent.querySelectorAll('*').length;assert.ok(cost<=100);first.destroy();
 const foreign=d.createElement('div');parent.append(foreign);
 const fill=n=>{while(d.querySelectorAll('*').length<n)foreign.append(d.createElement('i'));};
 fill(1200-cost);
 const original=dom.window.Element.prototype.append;
 dom.window.Element.prototype.append=function(...args){const result=original.apply(this,args);if(this.isConnected)peak=Math.max(peak,d.querySelectorAll('*').length);return result;};
 first=mount();assert.ok(first);assert.equal(d.querySelectorAll('*').length,1200);assert.equal(peak,1200);
 const slots=[...d.querySelectorAll('[data-ip-context-select] option')], seen=new Set();
 do{for(const option of slots)if(!option.hidden)seen.add(option.value);
  if(d.querySelector('[data-ip-context-next]').disabled)break;d.querySelector('[data-ip-context-next]').click();
 }while(true);
 assert.deepEqual([...seen],addresses);assert.deepEqual([...d.querySelectorAll('[data-ip-context-select] option')],slots);
 assert.match(d.querySelector('[data-ip-context-page]').textContent,/500\/503.*생략 3/);assert.equal(calls,0);
 first.update({phase:'ready',value:corpus[0].projection,appCache:false});assert.equal(d.querySelectorAll('*').length,1200);
 const oldQuery=d.querySelector('[data-ip-context-query]');oldQuery.focus();
 first.destroy();fill(1200-cost+1);peak=d.querySelectorAll('*').length;
 const before=parent.querySelectorAll('*').length;assert.equal(mount(),null);assert.equal(parent.querySelectorAll('*').length,before);assert.equal(limited,1);
 fill(1200);assert.equal(mount(),null);assert.equal(d.querySelectorAll('*').length,1200);assert.equal(limited,2);
 oldQuery.click();assert.equal(calls,0);assert.ok(foreign.isConnected);dom.window.close();
});

test('successor ownership fences detached events, cleanup, focus and fact publication',()=>{
 const dom=new JSDOM('<main><i id="foreign"></i></main>'),d=dom.window.document,parent=d.querySelector('main');let calls=0;
 const options={document:d,parent,catalog:{addresses:['1.1.1.1'],total:1,omitted:0},selectedAddress:'1.1.1.1',onQuery:()=>calls++};
 const first=appModule.mountIPContextView(options),old=d.querySelector('[data-ip-context-query]');
 const next=appModule.mountIPContextView(options),button=d.querySelector('[data-ip-context-query]');button.focus();
 old.click();first.update({phase:'ready',value:corpus[0].projection});first.destroy();
 assert.equal(calls,0);assert.equal(d.activeElement,button);assert.ok(button.isConnected);assert.ok(d.querySelector('#foreign'));
 assert.equal(d.querySelectorAll('[data-ip-context-panel]').length,1);assert.equal(d.querySelector('[data-ip-context-panel]').dataset.state,'idle');
 button.click();assert.equal(calls,1);next.destroy();dom.window.close();
});

test('old backend 404 preserves ready report, complete target catalog / All / None, and export bytes',async()=>{
 const h=await harness({contextResponse:()=>new Response('untrusted <script>credential</script>',{status:404})});try{
  await h.app.start('topology');h.drain();const report=JSON.stringify(h.app.getOwnedReport('topology'));let exported;
  const nativeClick=h.win.HTMLAnchorElement.prototype.click;
  h.win.Blob=class{constructor(parts){exported=parts.join('');}};h.win.URL.createObjectURL=()=> 'blob:test';h.win.URL.revokeObjectURL=()=>{};h.win.HTMLAnchorElement.prototype.click=()=>{};
  h.document.querySelector('[data-ip-context-query]').click();await tick();
  assert.equal(h.document.querySelector('[data-ip-context-panel]').dataset.state,'unsupported');
  assert.ok(!h.document.body.textContent.includes('untrusted'));
  h.document.querySelector('#download-topology').click();assert.deepEqual(JSON.parse(exported),JSON.parse(report));
  h.win.HTMLAnchorElement.prototype.click=nativeClick;
  await h.view('geo-map');const root=h.document.querySelector('#geo-target-filter');
  const count=root.querySelectorAll('[data-target-index]').length;
  root.querySelector('[data-filter-action="none"]').click();h.drain();
  assert.equal(root.querySelectorAll('[data-target-index]').length,count);assert.equal(h.document.querySelector('[data-ip-context-query]').disabled,true);
  root.querySelector('[data-filter-action="all"]').click();h.drain();assert.equal(h.document.querySelector('[data-ip-context-query]').disabled,false);
  assert.equal(h.requests.length,2);assert.equal(h.app.getState().topology.phase,'ready');
 }finally{h.close();}
});

for(const replacement of ['select','navigation','base','auth','report','destroy','cancel'])test(`actual app ${replacement} revokes delayed context and never changes old report or successor focus`,async()=>{
 let resolve;const h=await harness({contextResponse:()=>new Promise(r=>{resolve=r;})});try{
  await h.app.start('topology');h.drain();const report=h.app.getOwnedReport('topology'),before=JSON.stringify(report);
  h.document.querySelector('[data-ip-context-query]').click();await tick();const request=h.requests[1].init;
  if(replacement==='select'){const s=h.document.querySelector('[data-ip-context-select]');s.value='9.9.9.9';s.dispatchEvent(new h.win.Event('change',{bubbles:true}));}
  if(replacement==='navigation')await h.view('ip-labels');
  if(replacement==='base'){const e=h.document.querySelector('#api-base-url');e.value='https://other.test';e.dispatchEvent(new h.win.Event('input',{bubbles:true}));}
  if(replacement==='auth'){h.document.querySelector('#bearer-token').value='new-secret';h.document.querySelector('#apply-bearer').click();}
  if(replacement==='report'){await h.app.start('topology');h.drain();}
  if(replacement==='destroy')h.app.destroy();
  if(replacement==='cancel')h.document.querySelector('[data-ip-context-cancel]').click();
  assert.equal(request.signal.aborted,true);const active=h.document.activeElement,body=h.document.body.textContent;
  resolve(new Response(Buffer.from(corpus[0].wire_base64,'base64')));await tick();h.drain();
  assert.equal(JSON.stringify(report),before);assert.equal(h.document.activeElement,active);assert.equal(h.document.body.textContent,body);
 }finally{h.close();}
});

test('context provider strings are inert and use existing credential reflection policy only at display',async()=>{
 const value=structuredClone(corpus[0].projection);value.registration.organization='<img src=x onerror=alert(1)> secret';
 const h=await harness({contextResponse:()=>new Response(JSON.stringify(value))});try{
  h.document.querySelector('#public-auth-enabled').checked=true;h.document.querySelector('#bearer-token').value='secret';h.document.querySelector('#apply-bearer').click();
  await h.app.start('topology');h.drain();h.document.querySelector('[data-ip-context-query]').click();await tick();
  const panel=h.document.querySelector('[data-ip-context-panel]');assert.match(panel.textContent,/<img/);assert.match(panel.textContent,/\[REDACTED CREDENTIAL\]/);
  assert.ok(!panel.textContent.includes('secret'));assert.equal(panel.querySelectorAll('a,img,script').length,0);
  assert.ok(!JSON.stringify(h.app.getOwnedReport('topology')).includes('registration'));
 }finally{h.close();}
});

test('inactive attempted mount cannot evict successor, append controls or announce a limit',()=>{
 const dom=new JSDOM('<main></main>'),d=dom.window.document,parent=d.querySelector('main');let limited=0;
 const options={document:d,parent,catalog:{addresses:['1.1.1.1'],total:1,omitted:0},selectedAddress:'1.1.1.1'};
 const current=appModule.mountIPContextView(options),root=parent.firstElementChild;root.querySelector('button').focus();const focus=d.activeElement;
 assert.equal(appModule.mountIPContextView({...options,isActive:()=>false,onLimited:()=>limited++}),null);
 assert.equal(parent.firstElementChild,root);assert.equal(d.activeElement,focus);assert.equal(limited,0);assert.ok(current.isActive());
 current.destroy();dom.window.close();
 });

 test('validated full-report 603 eligible observations cap at 500 with truthful omission and unchanged bytes',async()=>{
 const raw=JSON.parse(readFileSync(new URL('../testdata/geo-details-rich-full-report.json',import.meta.url),'utf8'));
 delete raw.analysis;delete raw.geo_details;raw.status='healthy';raw.summary={total:1,passed:1,failed:0};
 const result=raw.results[0];result.status='healthy';delete result.error_code;
 const attempts=Array.from({length:3},(_,a)=>{
 const nodes=Array.from({length:201},(_,i)=>({id:`n${i}`,hop:i+1,address:`8.3.${Math.floor((a*201+i)/256)}.${(a*201+i)%256}`,status:'healthy',latency_ms:0,public_ip:true}));
 return {attempt:a+1,status:'healthy',error_code:'',message:'',topology:{reached:true,nodes,links:nodes.slice(1).map((n,i)=>({from:nodes[i].id,to:n.id,status:'healthy'}))}};
 });
 result.details={attempts,topology:attempts[0].topology,attempts_total:3,attempts_reached:3,attempts_failed:0,attempts_unreached:0,attempts_execution_failed:0,attempts_timed_out:0,attempts_cancelled:0};
 const bytes=JSON.stringify(raw),parsed=parseResponse({ok:true,status:200},bytes);assert.equal(parsed.ok,true,JSON.stringify(parsed));
 const c=appModule.eligibleIPContextAddresses(parsed.report);assert.equal(c.total,603);assert.equal(c.addresses.length,500);assert.equal(c.omitted,103);
 const h=await harness({bytes});try{
 await h.app.start('topology');h.drain();const seen=new Set(),slots=[...h.document.querySelectorAll('[data-ip-context-select] option')];
 for(;;){slots.filter(o=>!o.hidden).forEach(o=>seen.add(o.value));const next=h.document.querySelector('[data-ip-context-next]');if(next.disabled)break;next.click();}
 assert.deepEqual([...seen],c.addresses);assert.match(h.document.querySelector('[data-ip-context-page]').textContent,/500\/603.*생략 103/);
 assert.equal(h.requests.length,1);assert.ok(h.document.querySelectorAll('*').length<=1200);assert.equal(JSON.stringify(raw),bytes);
 }finally{h.close();}
 });
