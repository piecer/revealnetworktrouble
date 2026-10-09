import test from 'node:test';
import assert from 'node:assert/strict';
import {JSDOM} from 'jsdom';
import {LAND,GEO_LIMITS,fitGeo,wrappedEdge,mountGeoMap} from './geo-map.js';

function selectionHarness({count=2, selectedNodeId, available=true, isActive=()=>true, extraNodes=[]}={}) {
 const dom=new JSDOM('<canvas></canvas><div id="list"></div><p id="detail"></p><p id="message"></p><button id="previous"></button><button id="next"></button><button id="pagePrevious"></button><button id="pageNext"></button><p id="pageStatus"></p>');
 const win=dom.window,doc=win.document,canvas=doc.querySelector('canvas'),list=doc.querySelector('#list');
 const nodes=Array.from({length:count},(_,i)=>({id:`n${i}`,address:`2001:4860:1234:5678:90ab:cdef:0000:${i}`,public_ip:true,hop_min:1,hop_max:3,geolocation:{city:'Seoul',region:'Region',country:'Korea',latitude:0,longitude:i ? 179 : -179},...(i===0?{display_label:'<img src=x onerror=alert(1)> — '+ '별칭'.repeat(80),asn:{number:15169,organization:'Example <ISP>'},latency_ms_avg:0}:{})}));
 const geo={markers:nodes.map(n=>({node_id:n.id,latitude:n.geolocation.latitude,longitude:n.geolocation.longitude})),segments:[]};
 for(let i=0;i<Math.min(100,count);i++){const button=doc.createElement('button');button.disabled=true;list.append(button);}
 const controls=Object.fromEntries(['detail','message','previous','next','pagePrevious','pageNext','pageStatus'].map(id=>[id,doc.getElementById(id)]));
 const jobs=new Map();let id=0;const calls=[];const context=new Proxy({}, {get:(o,k)=>o[k]??((...args)=>calls.push([k,...args])),set:(o,k,v)=>(o[k]=v,true)});
 canvas.getContext=()=>available?context:null;canvas.getBoundingClientRect=()=>({left:50,top:20,width:720,height:360});
 const signal=new AbortController(),selections=[],before=JSON.stringify({nodes,geo});
 const dispose=mountGeoMap({canvas,geo,facts:{nodes:[...nodes,...extraNodes],routes:[{result_index:2,attempt:4,node_ids:nodes.map(n=>n.id)}]},list,controls,selectedNodeId,onSelect:id=>selections.push(id),isActive,signal:signal.signal,win,scheduler:{schedule(fn){jobs.set(++id,fn);return id;},cancel(id){jobs.delete(id);}}});
 return {win,doc,canvas,list,controls,nodes,geo,before,calls,selections,signal,dispose,jobs,flush(){while(jobs.size){const batch=[...jobs.values()];jobs.clear();batch.forEach(fn=>fn());}}};
}

function pointer(h,type,x,y,id=1) {
 const event=new h.win.MouseEvent(type,{clientX:x,clientY:y,button:0,bubbles:true});Object.defineProperty(event,'pointerId',{value:id});h.canvas.dispatchEvent(event);
}
function markerPoint(h,index) {
 const r=h.canvas.getBoundingClientRect(),[lon,lat]=h.canvas.dataset.center.split(',').map(Number),scale=Number(h.canvas.dataset.scale),marker=h.geo.markers[index];
 const width=Math.min(GEO_LIMITS.width,r.width),height=Math.min(GEO_LIMITS.height,r.height);
 const delta=((marker.longitude-lon+180)%360+360)%360-180;
 return {x:r.left+(width/2+delta*scale)*r.width/width,y:r.top+(height/2+(lat-marker.latitude)*scale)*r.height/height};
}
test('Geo pointer hit testing selects wrapped transformed markers but never pans or cancelled gestures',()=>{
 const h=selectionHarness();h.flush();
 const tap=index=>{const p=markerPoint(h,index);pointer(h,'pointerdown',p.x,p.y);pointer(h,'pointerup',p.x,p.y);h.flush();};
 tap(1);assert.equal(h.canvas.dataset.selectedNodeId,'n1','tap selects the -360 world copy');
 assert.ok(h.calls.some(c=>c[0]==='arc'&&c[3]===9),'persistent selected marker ring');
 h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:'-'}));h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:'ArrowLeft'}));h.flush();tap(0);assert.equal(h.canvas.dataset.selectedNodeId,'n0');
 // CSS-to-bounded-logical scaling when the canvas exceeds its backing cap.
 h.canvas.getBoundingClientRect=()=>({left:50,top:20,width:4096,height:2048});h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:'Home'}));h.flush();tap(1);assert.equal(h.canvas.dataset.selectedNodeId,'n1');
 const p=markerPoint(h,0),before=h.selections.length;
 pointer(h,'pointerdown',p.x,p.y);pointer(h,'pointermove',p.x+30,p.y+10);pointer(h,'pointermove',p.x,p.y);pointer(h,'pointerup',p.x,p.y);h.flush();assert.equal(h.selections.length,before,'out-and-back drag is not a tap');
 for(const type of ['pointercancel','lostpointercapture']){const p=markerPoint(h,0);pointer(h,'pointerdown',p.x,p.y);pointer(h,type,p.x,p.y);pointer(h,'pointerup',p.x,p.y);assert.equal(h.selections.length,before);}
 assert.equal(JSON.stringify({nodes:h.nodes,geo:h.geo}),h.before);h.dispose();h.win.close();
});

test('Geo retains a selected non-geolocated graph identity without inventing coordinates',()=>{
 const h=selectionHarness({selectedNodeId:'private',extraNodes:[{id:'private',address:'10.0.0.1',public_ip:false}]});
 assert.equal(h.canvas.dataset.selectedNodeId,'private');assert.match(h.controls.detail.textContent,/10\.0\.0\.1/);
 assert.match(h.controls.detail.textContent,/국가 미확인.*지역 미확인.*도시 미확인/);assert.match(h.controls.detail.textContent,/좌표 미확인/);assert.equal(h.canvas.dataset.markers,'2');
 assert.equal(h.list.querySelector('[aria-pressed="true"]'),null);assert.equal(h.controls.next.disabled,false);
 h.controls.next.click();assert.equal(h.canvas.dataset.selectedNodeId,'n0');h.dispose();h.win.close();
});

test('Geo selection owners cannot mutate successor controls and dispose cancels every initial frame',()=>{
 let live=true;const h=selectionHarness({isActive:()=>live});
 const pending=[...h.jobs.values()];const before=h.selections.length;live=false;
 h.controls.detail.textContent='successor';h.controls.detail.scrollTop=123;h.controls.pageStatus.textContent='successor';
 h.controls.next.click();h.controls.pageNext.click();h.list.lastElementChild.click();h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:']'}));pending.forEach(fn=>fn());
 assert.equal(h.controls.detail.textContent,'successor');assert.equal(h.controls.detail.scrollTop,123);assert.equal(h.controls.pageStatus.textContent,'successor');assert.equal(h.selections.length,before);
 h.signal.abort();assert.equal(h.jobs.size,0,'all frames are tracked, including initial selection');h.dispose();h.win.close();
});

test('Geo disposal clears only owned detail and never a successor sharing static controls',()=>{
 const h=selectionHarness();h.dispose();assert.doesNotMatch(h.controls.detail.textContent,/2001:4860/);assert.equal(h.controls.next.disabled,true);h.win.close();
 const old=selectionHarness();
 const replacement=mountGeoMap({canvas:old.canvas,geo:old.geo,facts:{nodes:old.nodes.map(n=>({...n,display_label:'successor alias'})),routes:[]},list:old.list,controls:old.controls,selectedNodeId:'n1',signal:new AbortController().signal,win:old.win,scheduler:{schedule(){return 1;},cancel(){}}});
 const before=old.controls.detail.textContent;old.dispose();assert.equal(old.controls.detail.textContent,before);assert.match(before,/successor alias/);replacement();old.win.close();
});

test('Geo pagination reaches every marker with fixed buttons and honest empty/fallback states',()=>{
 for(const count of [0,1,101,500,501]) {
  const h=selectionHarness({count,available:false}),slots=[...h.list.children],total=Math.min(count,500),seen=new Set(),elementCount=h.doc.querySelectorAll('*').length;
  for(let page=0;page<5;page++) {
   for(const button of slots.filter(b=>!b.hidden)) {assert.equal(button.disabled,false);seen.add(button.dataset.nodeId);button.click();assert.ok(h.controls.detail.textContent.includes(h.nodes[Number(button.dataset.nodeId.slice(1))].address));}
   assert.deepEqual([...h.list.children],slots,'pagination reuses planned elements');assert.equal(h.doc.querySelectorAll('*').length,elementCount);
   if(h.controls.pageNext.disabled)break;
   h.controls.pageNext.click();
  }
  assert.equal(seen.size,total);assert.equal(h.controls.pageNext.disabled,true);assert.equal(h.controls.next.disabled,true);
  if(total>1){h.controls.previous.click();assert.equal(h.canvas.dataset.selectedNodeId,`n${total-2}`);h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:']'}));assert.equal(h.canvas.dataset.selectedNodeId,`n${total-1}`);}
  if(total===0){assert.equal(h.controls.previous.disabled,true);assert.match(h.controls.detail.textContent,/선택할 공인 IP 위치가 없습니다/);}
  assert.match(h.controls.message.textContent,/Canvas.*사용할 수 없습니다/);h.dispose();h.win.close();
 }
 const h=selectionHarness({count:500,selectedNodeId:'n499'});assert.equal(h.list.firstElementChild.dataset.nodeId,'n400');assert.equal(h.list.lastElementChild.getAttribute('aria-pressed'),'true');h.controls.pagePrevious.click();assert.equal(h.list.firstElementChild.dataset.nodeId,'n300');assert.equal(h.canvas.dataset.selectedNodeId,'n499');h.dispose();h.win.close();
});

test('Geo selected list item exposes persistent complete inert observed facts',()=>{
 const h=selectionHarness(),buttons=h.list.querySelectorAll('button');
 assert.equal(buttons[0].disabled,false,'planned location buttons become interactive');
 assert.equal(buttons[0].getAttribute('aria-pressed'),'true');
 assert.ok(buttons[0].textContent.includes(h.nodes[0].address));
 assert.ok(buttons[0].textContent.includes('Seoul'));
 const detail=h.controls.detail.textContent;
 for(const value of [h.nodes[0].address,h.nodes[0].display_label,'Korea','Region','Seoul','AS15169','Example <ISP>','0 ms','HOP 1–3','경로 3','시도 4','위도 0','경도 -179']) assert.ok(detail.includes(value),value);
 assert.match(detail,/GeoIP.*추정/);assert.match(detail,/링크 지연 아님/);
 assert.equal(h.doc.querySelector('img'),null);
 h.controls.detail.scrollTop=200;
 buttons[1].click();assert.equal(buttons[1].getAttribute('aria-pressed'),'true');
 assert.equal(h.controls.detail.scrollTop,0,'new selection reveals its identity after reading the previous detail tail');
 assert.match(h.controls.detail.textContent,/RTT 미측정/);assert.match(h.controls.detail.textContent,/ASN.*미확인/);
 assert.equal(h.selections.at(-1),'n1');
 h.canvas.dispatchEvent(new h.win.Event('pointerleave'));assert.ok(h.controls.detail.textContent.includes(h.nodes[1].address));
 assert.equal(JSON.stringify({nodes:h.nodes,geo:h.geo}),h.before);h.dispose();h.win.close();
});
const inside=(x,y,ring)=>{let value=false;for(let i=0,j=ring.length-1;i<ring.length;j=i++){const [a,b]=ring[i],[c,d]=ring[j];if((b>y)!==(d>y)&&x<(c-a)*(y-b)/(d-b)+a)value=!value;}return value;};
const land=(lon,lat)=>LAND.some(p=>p.reduce((v,r)=>v!==inside(lon,lat,r),false));
test('vendored Natural Earth has bounded real continent geometry, not a sketch',()=>{
 assert.equal(LAND.length,127);assert.equal(LAND.flat().reduce((sum,r)=>sum+r.length,0),GEO_LIMITS.vertices);
 for(const [lon,lat] of [[10,50],[-100,40],[135,-25],[-60,-10],[25,5]])assert.ok(land(lon,lat),`${lon},${lat} land`);
 for(const [lon,lat] of [[-140,0],[-30,0],[80,-30]])assert.ok(!land(lon,lat),`${lon},${lat} ocean`);
});
test('fit uses the smallest longitude arc and preserves geographic aspect at both viewport sizes',()=>{
 for(const width of [375,1440]){const points=[{latitude:20,longitude:179},{latitude:-20,longitude:-179}];const fit=fitGeo(points,width,540);assert.equal(fit.longitude,-180);assert.equal(fit.latitude,0);assert.ok(fit.scale>0&&fit.scale<=Math.min(width/360,3)*8);const end=wrappedEdge(points[0],points[1]);assert.equal(end.longitude,181);assert.equal(end.latitude,-20);assert.equal(wrappedEdge(points[1],points[0]).longitude,-181);}
 assert.deepEqual(fitGeo([],360,180),{longitude:0,latitude:0,scale:1});
});
function harness(geo={markers:[],segments:[],arrows:[]}){
 const dom=new JSDOM('<canvas></canvas><button></button>');const win=dom.window,canvas=win.document.querySelector('canvas'),fit=win.document.querySelector('button');
 const calls=[];const context=new Proxy({}, {get:(o,k)=>o[k]??((...args)=>calls.push([k,...args])),set:(o,k,v)=>(o[k]=v,true)});canvas.getContext=()=>context;
 const jobs=new Map();let id=0;const scheduler={schedule(fn){jobs.set(++id,fn);return id;},cancel(id){jobs.delete(id);}};const signal=new AbortController();
 const dispose=mountGeoMap({canvas,geo,signal:signal.signal,win,scheduler,controls:{fit}});
 return {dom,win,canvas,fit,calls,jobs,signal,dispose,flush(){const next=[...jobs.values()];jobs.clear();next.forEach(fn=>fn());}};
}
test('empty Geo explicitly explains missing coordinates while painting the offline world',()=>{
 const h=harness();assert.equal(h.canvas.dataset.markers,'0');assert.equal(h.canvas.dataset.drawState,'rendered');assert.ok(h.calls.some(c=>c[0]==='fillText'&&c[1].includes('공인 IP 홉이 없습니다')));h.dispose();h.dom.window.close();
});
test('map draw is bounded and event-driven; abort cancels frames and detaches all controls',()=>{
 const h=harness();assert.ok(h.calls.filter(c=>c[0]==='lineTo').length<24753+50);assert.equal(Number(h.canvas.dataset.geometryProjections),24753);assert.equal(h.canvas.width<=GEO_LIMITS.width*GEO_LIMITS.dpr,true);
 h.flush();assert.equal(h.jobs.size,0);h.fit.click();h.fit.click();assert.equal(h.jobs.size,1);h.signal.abort();assert.equal(h.jobs.size,0);const n=h.calls.length;h.fit.click();h.win.dispatchEvent(new h.win.Event('resize'));h.canvas.dispatchEvent(new h.win.KeyboardEvent('keydown',{key:'+'}));assert.equal(h.jobs.size,0);assert.equal(h.calls.length,n);h.dispose();h.dom.window.close();
});
test('Canvas context failure announces rendering failure, distinct from missing coordinates',()=>{
 const dom=new JSDOM('<canvas></canvas><p></p>');const canvas=dom.window.document.querySelector('canvas'),message=dom.window.document.querySelector('p');canvas.getContext=()=>null;
 const dispose=mountGeoMap({canvas,geo:{markers:[],segments:[]},signal:new AbortController().signal,win:dom.window,scheduler:{schedule(){return 1;},cancel(){}},controls:{message}});
 assert.equal(canvas.dataset.drawState,'unavailable');assert.match(message.textContent,/Canvas.*사용할 수 없습니다/);dispose();dom.window.close();
});
test('pre-aborted mount acquires no observer or listeners',()=>{
 const dom=new JSDOM('<canvas></canvas>'), win=dom.window, canvas=win.document.querySelector('canvas');
 let acquired=0;win.ResizeObserver=class{observe(){acquired++;}disconnect(){acquired--;}};
 const abort=new AbortController();abort.abort();
 const dispose=mountGeoMap({canvas,geo:{markers:[],segments:[]},signal:abort.signal,win,scheduler:{schedule(){throw Error('stale schedule');},cancel(){}}});
 assert.equal(acquired,0);dispose();win.close();
});
test('unavailable Canvas remains safe under keyboard pan',()=>{
 const dom=new JSDOM('<canvas></canvas>'),win=dom.window,canvas=win.document.querySelector('canvas');canvas.getContext=()=>null;
 const errors=[];win.addEventListener('error',e=>{errors.push(e.error);e.preventDefault();});
 const dispose=mountGeoMap({canvas,geo:{markers:[],segments:[]},signal:new AbortController().signal,win,scheduler:{schedule(){return 1;},cancel(){}}});
 canvas.dispatchEvent(new win.KeyboardEvent('keydown',{key:'ArrowRight'}));assert.deepEqual(errors,[]);dispose();win.close();
});
test('detached stale frame cannot paint, replacement remains live',()=>{
 const h=harness();h.fit.click();const stale=[...h.jobs.values()][0];h.canvas.remove();const count=h.calls.length;stale();assert.equal(h.calls.length,count);h.dispose();stale();assert.equal(h.calls.length,count);h.win.close();
 const replacement=harness();assert.equal(replacement.canvas.dataset.drawState,'rendered');replacement.dispose();replacement.win.close();
});
test('hostile viewport DPR and maximum model stay within backing and draw budgets',()=>{
 const markers=Array.from({length:501},(_,i)=>({node_id:String(i),longitude:10,latitude:20}));
 const segments=Array.from({length:1001},()=>({from:'0',to:'1'}));const h=harness({markers,segments});
 h.canvas.getBoundingClientRect=()=>({width:1e9,height:1e9});Object.defineProperty(h.win,'devicePixelRatio',{value:100});h.fit.click();h.calls.length=0;h.flush();
 assert.equal(h.canvas.width,4096);assert.equal(h.canvas.height,2048);assert.equal(h.canvas.dataset.markers,'500');assert.equal(h.canvas.dataset.segments,'1000');assert.ok(h.calls.length<60000);assert.equal(h.jobs.size,0);h.dispose();h.win.close();
});
test('wrapped line and arrow share short seam geometry, including reverse direction',()=>{
 for(const [a,b] of [[179,-179],[-179,179]]){const h=harness({markers:[{node_id:'a',longitude:a,latitude:0},{node_id:'b',longitude:b,latitude:10}],segments:[{from:'a',to:'b'}],arrows:[{from:'a',to:'b'}]});assert.equal(h.canvas.dataset.segments,'1');const moves=h.calls.filter(c=>c[0]==='moveTo');assert.ok(moves.every(c=>c.slice(1).every(Number.isFinite)));h.dispose();h.dom.window.close();}
});
