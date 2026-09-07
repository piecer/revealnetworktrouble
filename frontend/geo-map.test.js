import test from 'node:test';
import assert from 'node:assert/strict';
import {JSDOM} from 'jsdom';
import {LAND,GEO_LIMITS,fitGeo,wrappedEdge,mountGeoMap} from './geo-map.js';
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
 const h=harness();assert.ok(h.calls.filter(c=>c[0]==='lineTo').length<16000);assert.equal(h.canvas.width<=GEO_LIMITS.width*GEO_LIMITS.dpr,true);
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
