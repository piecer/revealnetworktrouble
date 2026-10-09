import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {JSDOM} from 'jsdom';
import * as map from './geo-map.js';
import {routeColor, MAX_ROUTE_LANES} from './topology-visualizer.js';

const digests={BORDERS:'15c9d9272f6385d3e110de52c360c5ab0057e8adf2e8823a80c73b7b2bd25a96',COUNTRIES:'d1d0802d3eccbbee606af8ac88e8903e93dea064f95392b346475f0d543084b2',CITIES:'eaff8c7b0be60cb2bb3f68f694dc24688a9d847b0d571e5dc0442b0b68cd876b'};
export function harness({markers=[],segments=[],routes=[],nodes=markers.map(p=>({id:p.node_id,address:p.node_id})),slots=100,selectedNodeId,width=720,height=360}={}) {
 const dom=new JSDOM('<canvas></canvas><div id="list"></div>'+['detail','message','previous','next','pagePrevious','pageNext','pageStatus','fit','zoomIn','zoomOut'].map(id=>`<button id="${id}"></button>`).join(''));
 const win=dom.window,doc=win.document,canvas=doc.querySelector('canvas'),list=doc.querySelector('#list');
 for(let i=0;i<slots;i++)list.append(doc.createElement('button'));
 const controls=Object.fromEntries([...doc.querySelectorAll('[id]')].map(x=>[x.id,x]));
 const calls=[],ctx=new Proxy({measureText:s=>({width:[...s].length*8})},{get:(o,k)=>o[k]??((...args)=>calls.push({kind:k,args,stroke:o.strokeStyle,fill:o.fillStyle,alpha:o.globalAlpha??1,lineWidth:o.lineWidth,font:o.font})),set:(o,k,v)=>(o[k]=v,true)});
 canvas.getContext=()=>ctx;canvas.getBoundingClientRect=()=>({left:0,top:0,width,height});
 const jobs=new Map();let next=0;const scheduler={schedule(fn){jobs.set(++next,fn);return next;},cancel(id){jobs.delete(id);}};
 const controller=new AbortController(),geo={markers,segments},facts={nodes,routes};
 const before=JSON.stringify({geo,facts});
 const dispose=map.mountGeoMap({canvas,geo,facts,list,controls,selectedNodeId,signal:controller.signal,win,scheduler});
 return {canvas,controls,calls,ctx,jobs,win,doc,geo,facts,scheduler,controller,dispose,flush(){const batch=[...jobs.values()];jobs.clear();batch.forEach(f=>f());},close(){assert.equal(JSON.stringify({geo,facts}),before);dispose();win.close();}};
}
test('pinned offline borders and label data retain independently frozen source digests',()=>{
 const source=readFileSync(new URL('./geo-map.js',import.meta.url),'utf8');
 for(const [name,digest] of Object.entries(digests)){
  assert.ok(Array.isArray(map[name]),`${name} embedded from official source`);
  const raw=source.match(new RegExp(`export const ${name} = (.*);`))[1];
  assert.equal(createHash('sha256').update(raw).digest('hex'),digest,name);
 }
 assert.equal(map.BORDERS.length,333);assert.equal(map.BORDERS.flat().length,3108);
 assert.equal(map.COUNTRIES.length,177);assert.equal(map.CITIES.length,243);
 for(const [lon,lat,name,priority] of [...map.COUNTRIES,...map.CITIES]){assert.ok(Number.isFinite(lon)&&Number.isFinite(lat)&&typeof name==='string'&&name.length&&Number.isFinite(priority));}
});
test('empty map paints every border part independently and counts actual geometry work',()=>{
 const h=harness({slots:0});
 const paths=h.calls.filter(c=>c.kind==='stroke'&&c.stroke==='#91aaa0');
 assert.equal(paths.length,333*3,'no invented joins at multipart breaks');
 assert.equal(Number(h.canvas.dataset.geometryProjections),24753);
 assert.equal(h.jobs.size,0,'no polling or animation');h.close();
});

test('label tiers use official ranks and stable population priority with three copies at most',()=>{
 assert.equal(typeof map.layoutGeoLabels,'function');
 const run=zoom=>map.layoutGeoLabels({relativeZoom:zoom,width:720,height:360,project:(lon,lat)=>({x:360+lon*2,y:180-lat*2}),measure:()=>({width:0}),reserved:[]});
 const world=run(1),countries=run(2),cities=run(3);
 assert.equal(world.projections,map.COUNTRIES.filter(c=>c[3]<=3).length*3);
 assert.equal(countries.projections,177*3);assert.equal(cities.projections,420*3);
 assert.ok(world.labels.every(l=>l.kind==='country'&&map.COUNTRIES[l.index][3]<=3));
 const painted=cities.labels.filter(l=>l.kind==='city').map(l=>l.index);
 assert.deepEqual(painted,[...painted].sort((a,b)=>map.CITIES[b][3]-map.CITIES[a][3]||a-b));
});
test('labels fully cull text rectangles, reserve markers/message/attribution and reject collisions',()=>{
 assert.equal(typeof map.layoutGeoLabels,'function');
 const reserved=[{left:300,right:420,top:100,bottom:220},{left:0,right:720,top:0,bottom:44},{left:0,right:720,top:326,bottom:360}];
 const options={relativeZoom:3,width:720,height:360,project:(lon,lat)=>({x:360+lon*2,y:180-lat*2}),measure:()=>({width:50,actualBoundingBoxAscent:8,actualBoundingBoxDescent:4}),reserved};
 const result=map.layoutGeoLabels(options);assert.ok(result.labels.length>0&&result.labels.length<=64);
 assert.deepEqual(result,map.layoutGeoLabels(options),'deterministic suppression');
 const overlaps=(a,b)=>a.left<b.right+4&&a.right>b.left-4&&a.top<b.bottom+4&&a.bottom>b.top-4;
 for(const [i,l] of result.labels.entries()){
  assert.ok(l.left>=0&&l.right<=720&&l.top>=0&&l.bottom<=360);
  for(const r of [...reserved,...result.labels.slice(0,i)])assert.equal(overlaps(l,r),false);
 }
 assert.equal(map.layoutGeoLabels({...options,measure:()=>({width:10000})}).labels.length,0);
 const h=harness({slots:0});assert.ok(Number(h.canvas.dataset.paintedLabels)>0);assert.ok(Number(h.canvas.dataset.labelProjections)<=1260);assert.ok(h.calls.some(c=>c.kind==='fillText'&&c.fill==='#e3e9cd'));h.close();
});

const point=(id,longitude=0,latitude=0)=>({node_id:id,longitude,latitude});
test('exact groups canonicalize seam/signed zero only, retaining order and tiny coordinate differences',()=>{
 assert.equal(typeof map.groupGeoMarkers,'function');
 const markers=[point('a',180,-0),point('b',-180,0),point('c',540),point('d',0),point('e',1e-14),point('f',0,1e-14)];
 const before=JSON.stringify(markers),groups=map.groupGeoMarkers(markers);
 assert.deepEqual(groups.map(g=>g.members),[[0,1,2],[3],[4],[5]]);assert.equal(JSON.stringify(markers),before);
});
test('genuine group taps cycle original member order; cached text follows identity, count and ownership',()=>{
 const markers=[point('a'),point('b'),point('c')],h=harness({markers});
 assert.equal(h.canvas.dataset.groups,'1');assert.equal(h.canvas.dataset.groupCopies,'3');
 const tap=()=>{for(const type of ['pointerdown','pointerup']){const e=new h.win.MouseEvent(type,{clientX:360,clientY:180,button:0});Object.defineProperty(e,'pointerId',{value:1});h.canvas.dispatchEvent(e);}h.flush();};
 for(const [id,member] of [['b',2],['c',3],['a',1],['b',2]]){tap();assert.equal(h.canvas.dataset.selectedNodeId,id);assert.match(h.controls.detail.textContent,new RegExp(`동일 좌표 ${member} / 3`));assert.match(h.controls.detail.textContent,/같은 장비.*의미하지/);}
 assert.ok(h.calls.some(c=>c.kind==='fillText'&&c.args[0]==='3'));
 h.controls.next.click();const stale=[...h.jobs.values()];
 const replacement=map.mountGeoMap({canvas:h.canvas,geo:{markers:[markers[0]],segments:[]},facts:{nodes:[{id:'a'}],routes:[]},list:h.controls.list,controls:h.controls,signal:new AbortController().signal,win:h.win,scheduler:h.scheduler});
 const text=h.controls.detail.textContent;stale.forEach(f=>f());h.dispose();assert.equal(h.controls.detail.textContent,text);assert.doesNotMatch(text,/동일 좌표/);replacement();h.win.close();
});
test('500 coincident members remain reachable with fixed 100 and zero list slots',()=>{
 for(const slots of [100,0]){
  const h=harness({markers:Array.from({length:500},(_,i)=>point(`n${i}`)),slots});
  assert.equal(h.canvas.dataset.groups,'1');const count=h.doc.querySelectorAll('*').length,seen=new Set();
  if(slots){for(let p=0;p<5;p++){for(const b of h.controls.list.children){b.click();seen.add(h.canvas.dataset.selectedNodeId);}h.controls.pageNext.click();}}
  else{for(let i=0;i<500;i++){seen.add(h.canvas.dataset.selectedNodeId);h.controls.next.click();}}
  assert.equal(seen.size,500);assert.match(h.controls.detail.textContent,/동일 좌표 500 \/ 500/);assert.equal(h.doc.querySelectorAll('*').length,count);h.close();
 }
});

test('R1 bounded disclosure precedes maximum alias while every singleton fact is retained',()=>{
 const alias='Edge <img onerror=alert(1)> '.padEnd(256,'긴');
 const nodes=Array.from({length:500},(_,i)=>({id:`n${i}`,address:`8.1.${Math.floor(i/256)}.${i%256}`,display_label:alias,hop_min:1,hop_max:25,latency_ms_avg:0,asn:{number:15169,organization:'Owner <>&'},geolocation:{city:'Legacy city',region:'Region',country:'Country'},geo_details:{city:'Supplemental '.padEnd(256,'길'),provider:'ipwho.is',source:'cache',fetched_at:'2026-10-09T01:02:03.456Z',expires_at:'2026-10-10T01:02:03.456Z'}}));
 const markers=nodes.map(n=>point(n.id)),routes=[{result_index:42,attempt:99,node_ids:nodes.map(n=>n.id)}];
 const h=harness({markers,nodes,routes,selectedNodeId:'n499'}),single=harness({markers:[markers[499]],nodes:[nodes[499]],routes});
 const text=h.controls.detail.textContent,lines=text.split('\n'),legacy=single.controls.detail.textContent;
 assert.deepEqual(lines.slice(0,3),[nodes[499].address,'동일 좌표 500 / 500','같은 좌표라도 같은 장비 보장 아님.']);
 assert.equal(lines[3],alias,'full 256-character alias follows the bounded disclosure');
 assert.ok(text.includes(legacy.slice(legacy.indexOf('\n')+1)),'every unabridged legacy and supplemental fact survives');
 assert.ok(text.includes('반복 탭으로 구성원 선택. 같은 좌표는 같은 장비를 의미하지 않습니다.'));
 assert.equal(h.controls.list.children[99].getAttribute('aria-label'),text);assert.equal(h.controls.detail.children.length,0,'plain text, not markup');
 assert.doesNotMatch(legacy,/동일 좌표|같은 장비/);assert.ok(legacy.startsWith(`${nodes[499].address} · ${alias}\n`));
 h.close();single.close();
});
test('R1 new group selection resets its scroll but stale owners cannot scroll the successor',()=>{
 const markers=[point('a'),point('b')],h=harness({markers,nodes:[{id:'a',address:'8.8.8.8'},{id:'b',address:'9.9.9.9'}],slots:0});
 h.controls.detail.scrollTop=300;h.controls.next.click();assert.equal(h.controls.detail.scrollTop,0);assert.match(h.controls.detail.textContent,/^9\.9\.9\.9\n동일 좌표 2 \/ 2\n/);
 const old=[...h.jobs.values()];const replacement=map.mountGeoMap({canvas:h.canvas,geo:{markers,segments:[]},facts:h.facts,list:h.controls.list,controls:h.controls,signal:new AbortController().signal,win:h.win,scheduler:h.scheduler});
 h.controls.detail.scrollTop=77;const text=h.controls.detail.textContent;old.forEach(f=>f());h.controller.abort();h.dispose();assert.equal(h.controls.detail.scrollTop,77);assert.equal(h.controls.detail.textContent,text);replacement();h.win.close();
});
test('R1 unlocated identity does not acquire a coincident group summary',()=>{
 const h=harness({markers:[point('a'),point('b')],nodes:[{id:'a'},{id:'b'},{id:'missing',address:'1.1.1.1',display_label:'Unlocated alias'}],selectedNodeId:'missing'});
 assert.ok(h.controls.detail.textContent.startsWith('1.1.1.1 · Unlocated alias\n'));assert.match(h.controls.detail.textContent,/좌표 미확인/);assert.doesNotMatch(h.controls.detail.textContent,/동일 좌표|같은 장비/);h.close();
});

test('route lanes use original result identity, adjacency and four-lane cap, not attempts or filtered positions',()=>{
 const markers=[point('a',-10),point('b',10)],segments=[{from:'a',to:'b'}];
 const routes=[28,7,42,5,2,28].map((result_index,i)=>({result_index,attempt:i+10,node_ids:['a','b']}));
 const h=harness({markers,segments,routes});
 const strokes=h.calls.filter(c=>c.kind==='stroke'&&c.lineWidth===2.5&&c.stroke!=='#ffffff');
 assert.deepEqual([...new Set(strokes.map(c=>c.stroke))],[2,5,7,28].map(routeColor));
 assert.equal(strokes.length,MAX_ROUTE_LANES*3);assert.equal(h.canvas.dataset.laneCopies,'12');
 assert.match(h.controls.detail.textContent,/경로 43/,'undrawn fifth membership stays available');h.close();
});
test('selection emphasizes matching segments, subdues unrelated ones and never creates missing-coordinate links',()=>{
 const markers=[point('a',-30),point('b',-10),point('c',10),point('d',30)];
 const routes=[{result_index:7,attempt:1,node_ids:['a','b','missing','c']},{result_index:2,attempt:99,node_ids:['c','d']}];
 const h=harness({markers,segments:[{from:'a',to:'b'},{from:'c',to:'d'}],routes});
 const lines=()=>h.calls.filter(c=>c.kind==='stroke'&&c.lineWidth===2.5&&c.stroke!=='#ffffff');
 assert.deepEqual(lines().map(c=>[c.stroke,c.alpha]),[...Array(3).fill([routeColor(7),1]),...Array(3).fill([routeColor(2),.35])]);
 h.calls.length=0;h.controls.list.children[3].click();h.flush();
 assert.deepEqual(lines().map(c=>[c.stroke,c.alpha]),[...Array(3).fill([routeColor(7),.35]),...Array(3).fill([routeColor(2),1])]);
 assert.equal(h.canvas.dataset.segments,'2');assert.equal(h.canvas.dataset.laneCopies,'6');h.close();
});
test('actual scheduled maximum paint work stays within geometry/label/group/lane/backing bounds',()=>{
 const markers=Array.from({length:501},(_,i)=>point(`n${i}`,i/10,i/20));
 const h=harness({markers,segments:Array.from({length:1001},()=>({from:'n0',to:'n1'})),routes:Array.from({length:5},(_,i)=>({result_index:i,attempt:1,node_ids:['n0','n1']})),width:10000,height:10000});
 Object.defineProperty(h.win,'devicePixelRatio',{value:100});h.calls.length=0;h.controls.fit.click();h.flush();
 assert.equal(h.canvas.dataset.geometryProjections,'24753');assert.ok(Number(h.canvas.dataset.labelProjections)<=1260);assert.ok(Number(h.canvas.dataset.paintedLabels)<=64);
 assert.equal(h.canvas.dataset.groups,'500');assert.equal(h.canvas.dataset.groupCopies,'1500');assert.equal(h.canvas.dataset.laneCopies,'12000');
 assert.equal(h.canvas.width*h.canvas.height,8388608);assert.equal(h.canvas.dataset.backingPixels,'8388608');
 assert.equal(h.calls.filter(c=>c.kind==='stroke'&&c.lineWidth===2.5&&c.stroke!=='#ffffff').length,12000);
 assert.equal(h.jobs.size,0);h.close();
});
