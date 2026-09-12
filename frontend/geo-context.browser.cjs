// Isolated source-backed synthetic renderer + actual application acceptance.
// PLAYWRIGHT_PATH=... node frontend/geo-context.browser.cjs OUTPUT
const {chromium}=require(process.env.PLAYWRIGHT_PATH||'playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const out=process.argv[2];assert.ok(out);fs.mkdirSync(out,{recursive:true});
const assets=['index.html','styles.css','app.js','common-prefix.js','state.js','topology-model.js','topology-presentation.js','topology-visualizer.js','topology-renderer.js','geo-map.js'];
let report=JSON.parse(fs.readFileSync(path.join(__dirname,'../testdata/geo-details-rich-compact-report.json')));
// In-memory synthetic coincident-coordinate variant; frozen witnesses untouched.
for(const n of report.compact_topology.nodes)if(n.geolocation){n.geolocation.latitude=0;n.geolocation.longitude=0;}
const ordinaryReport=report;
// Same bounded compact-v1 fixture shape as the retained Stage1 browser gate,
// but all 500 coordinates coincide. No producer fixture or source is modified.
function maximumGroup() {
 const stats=total=>({total,displayed:total,omitted:0}),routeStats=total=>({...stats(total),complete:total,partial:0});
 const started_at='2026-09-01T00:00:00Z';
 const nodes=Array.from({length:500},(_,i)=>({id:`n${i}`,kind:'ip',address:`8.1.${Math.floor(i/256)}.${i%256}`,status:'healthy',hop_min:i%25+1,hop_max:i%25+1,observations:1,public_ip:true,geolocation:{latitude:0,longitude:0,city:`Synthetic city ${i}`,region:'Synthetic region',country:'Synthetic country',country_code:'US'},latency_ms_avg:0,asn:{number:15169,organization:'Synthetic <organization>'}}));
 const routes=[],links=[],results=[],result_stats=[];
 for(let start=0;start<500;start+=25){const row=nodes.slice(start,start+25),result_index=results.length;
  results.push({kind:'traceroute',address:row.at(-1).address,status:'healthy',latency_ms:1,started_at,details:{attempts_total:1,attempts_reached:1,attempts_failed:0,attempts_unreached:0,attempts_execution_failed:0,attempts_timed_out:0,attempts_cancelled:0}});
  routes.push({result_index,attempt:1,status:'healthy',reached:true,complete:true,node_ids:row.map(n=>n.id)});
  for(let i=1;i<row.length;i++)links.push({from:row[i-1].id,to:row[i].id,status:'healthy',observations:1});
  result_stats.push({result_index,routes:routeStats(1),node_observations:stats(row.length),link_observations:stats(row.length-1)});
 }
 return {id:'synthetic-coincident-500',status:'healthy',started_at,duration_ms:1,summary:{total:results.length,passed:results.length,failed:0},results,compact_topology:{schema:'compact-v1',selection:'fair-complete-prefix-v1',limits:{nodes:500,links:1000,max_response_bytes_exclusive:1048576,max_geo_bundle_bytes:4096},nodes,links,routes,stats:{nodes:stats(nodes.length),links:stats(links.length),routes:routeStats(routes.length),node_observations:stats(nodes.length),link_observations:stats(links.length)},result_stats,geo:{eligible:500,available:500,included:500,omitted:0,unavailable:0},truncated:false}};
}
const disclosure=[];
const server=http.createServer((req,res)=>{const name=new URL(req.url,'http://local').pathname.slice(1)||'index.html';if(name==='api/v1/reports'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(report));return;}if(!assets.includes(name)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(path.join(__dirname,name)));});
(async()=>{let browser,base;const results=[],source={};try{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base=`http://127.0.0.1:${server.address().port}`;
 for(const name of assets){const b=fs.readFileSync(path.join(__dirname,name));assert.deepEqual(Buffer.from(await(await fetch(`${base}/${name}`)).arrayBuffer()),b);source[name]=crypto.createHash('sha256').update(b).digest('hex');}
 browser=await chromium.launch();
 for(const width of [1440,375]){
  const page=await browser.newPage({viewport:{width,height:900},hasTouch:true});const errors=[],external=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url());});
  if(process.env.GEO_CONTEXT_LONG_PATH_MUTANT)await page.route(`${base}/geo-map.js`,r=>{const original=fs.readFileSync(path.join(__dirname,'geo-map.js'),'utf8'),mutant=original.replace('from.longitude+wrapLongitude(to.longitude-from.longitude)','to.longitude');assert.notEqual(mutant,original);return r.fulfill({contentType:'text/javascript',body:mutant});});
  await page.goto(base);
  await page.evaluate(async width=>{
   const m=await import('./geo-map.js');window.M=m;window.V=await import('./topology-visualizer.js');
   document.body.replaceChildren();document.body.style.cssText='margin:0;padding:0;background:#0b1c2a';
   const c=document.createElement('canvas');c.id='probe';c.style.cssText=`display:block;width:${width}px;height:360px`;document.body.append(c);
   const controls={};for(const name of ['detail','previous','next','pagePrevious','pageNext','pageStatus']){const b=document.createElement(name==='detail'?'pre':'button');b.id=name;b.textContent=name;document.body.append(b);controls[name]=b;}
   const list=document.createElement('div');document.body.append(list);for(let i=0;i<100;i++)list.append(document.createElement('button'));
   window.C=c;window.controls=controls;window.list=list;window.ctx=c.getContext('2d');window.calls=[];window.omit='';
   for(const kind of ['stroke','fillText','strokeText']){const original=ctx[kind].bind(ctx);ctx[kind]=function(...args){const color=kind==='fillText'?this.fillStyle:this.strokeStyle;const isLabel=kind!=='stroke'&&(this.fillStyle==='#e3e9cd'||this.fillStyle==='#c9d4ee'||kind==='strokeText');if(kind==='fillText'&&isLabel){const metric=this.measureText(args[0]);calls.push({text:args[0],x:args[1],y:args[2],width:metric.width,ascent:metric.actualBoundingBoxAscent,descent:metric.actualBoundingBoxDescent,color,font:this.font});}if(window.omit==='borders'&&kind==='stroke'&&color==='#91aaa0'||window.omit==='labels'&&isLabel)return;return original(...args);};}
   window.mount=(geo,facts={nodes:geo.markers.map(p=>({id:p.node_id,address:p.node_id})),routes:[]})=>{window.dispose?.();window.abort=new AbortController();window.jobs=new Map();let id=0;window.sched={schedule(fn){jobs.set(++id,fn);return id;},cancel(i){jobs.delete(i);}};window.calls=[];window.geo=geo;window.facts=facts;window.dispose=m.mountGeoMap({canvas:c,geo,facts,controls,list,win:window,signal:abort.signal,scheduler:sched});};
   window.flush=()=>{const batch=[...jobs.values()];jobs.clear();batch.forEach(f=>f());};
   window.redraw=()=>{window.calls=[];dispatchEvent(new Event('resize'));flush();};
   window.pixels=()=>ctx.getImageData(0,0,c.width,c.height).data;
   window.ink=()=>{const original=pixels().slice();omit='borders';redraw();let border=0;const noBorder=pixels();for(let i=0;i<original.length;i+=4)if(original[i]!==noBorder[i]||original[i+1]!==noBorder[i+1]||original[i+2]!==noBorder[i+2])border++;omit='';redraw();const labels=calls.slice();omit='labels';redraw();const noLabel=pixels();const labelInk=labels.map(l=>{let count=0;const ratio=+C.dataset.dpr;for(let y=Math.max(0,Math.floor((l.y-l.ascent-2)*ratio));y<Math.min(C.height,(l.y+l.descent+2)*ratio);y++)for(let x=Math.max(0,Math.floor((l.x-l.width/2-2)*ratio));x<Math.min(C.width,(l.x+l.width/2+2)*ratio);x++){const i=(y*C.width+x)*4;if(original[i]!==noLabel[i]||original[i+1]!==noLabel[i+1]||original[i+2]!==noLabel[i+2])count++;}return {...l,ink:count};});omit='';redraw();return {border,labels:labelInk,data:{...C.dataset},overflow:document.documentElement.scrollWidth>innerWidth};};
   mount({markers:[],segments:[]});
  },width);
  const world=await page.evaluate(()=>ink());assert.ok(world.border>100);assert.ok(world.labels.length>5);assert.ok(world.labels.every(l=>l.ink>3));assert.equal(world.overflow,false);
  await page.locator('#probe').screenshot({path:path.join(out,`world-${width}.png`)});
  await page.evaluate(()=>mount({markers:[{node_id:'a',longitude:110,latitude:35},{node_id:'b',longitude:120,latitude:35}],segments:[]}));
  const zoom=await page.evaluate(()=>ink());assert.ok(zoom.labels.some(l=>l.color==='#c9d4ee'&&l.ink>3),'actual city ink');assert.ok(zoom.labels.some(l=>l.color==='#e3e9cd'&&l.ink>3),'actual country ink');assert.equal(+zoom.data.labelProjections,1260);
  for(const [i,l] of zoom.labels.entries()){assert.ok(l.x-l.width/2>=0&&l.x+l.width/2<=width);for(const p of zoom.labels.slice(0,i))assert.ok(l.x+l.width/2+4<=p.x-p.width/2||l.x-l.width/2>=p.x+p.width/2+4||l.y+l.descent+4<=p.y-p.ascent||l.y-l.ascent>=p.y+p.descent+4);}
  await page.locator('#probe').screenshot({path:path.join(out,`labels-${width}.png`)});
  await page.evaluate(()=>mount({markers:Array.from({length:500},(_,i)=>({node_id:`n${i}`,longitude:0,latitude:0})),segments:[]}));
  for(const id of ['n1','n2','n3']){if(width===375)await page.touchscreen.tap(width/2,180);else await page.mouse.click(width/2,180);await page.evaluate(()=>flush());assert.equal(await page.locator('#probe').getAttribute('data-selected-node-id'),id);}
  assert.match(await page.locator('#detail').textContent(),/동일 좌표 4 \/ 500/);
  const all=await page.evaluate(()=>{const ids=[];for(let p=0;p<5;p++){for(const b of list.children){b.click();ids.push(C.dataset.selectedNodeId);}controls.pageNext.click();}return {ids,count:document.querySelectorAll('*').length,slots:list.children.length,data:{...C.dataset}};});assert.equal(new Set(all.ids).size,500);assert.equal(all.slots,100);assert.ok(all.count<=1200);assert.equal(all.data.groups,'1');
  await page.locator('#probe').screenshot({path:path.join(out,`group-${width}.png`)});
  const route=await page.evaluate(()=>{
   const markers=[['a',-30,-10],['b',-10,-10],['c',10,10],['d',30,10]].map(([node_id,longitude,latitude])=>({node_id,longitude,latitude}));
   const routes=[2,5,7,28,42].map(result_index=>({result_index,attempt:99,node_ids:['a','b']}));routes.push({result_index:1,attempt:2,node_ids:['c','d']});
   mount({markers,segments:[{from:'a',to:'b'},{from:'c',to:'d'}]},{nodes:markers.map(m=>({id:m.node_id})),routes});
   const countColor=color=>{const rgb=color.match(/\w\w/g).map(x=>parseInt(x,16));const p=pixels();let n=0;for(let i=0;i<p.length;i+=4)if(rgb.every((v,k)=>p[i+k]===v))n++;return n;};
   const colors=[2,5,7,28].map(V.routeColor),before=colors.map(countColor),unrelatedBefore=countColor(V.routeColor(1));
   list.children[3].click();flush();const after=colors.map(countColor),unrelatedAfter=countColor(V.routeColor(1));
   return {colors,before,after,unrelatedBefore,unrelatedAfter,lanes:C.dataset.laneCopies};
  });assert.equal(route.lanes,'15');assert.ok(route.before.every(n=>n>5));assert.ok(route.after.every(n=>n===0));assert.equal(route.unrelatedBefore,0);assert.ok(route.unrelatedAfter>5);
  await page.locator('#probe').screenshot({path:path.join(out,`routes-${width}.png`)});
  const seams=[];for(const direction of [1,-1]){
   const result=await page.evaluate(direction=>{
    const geo={markers:[{node_id:'a',longitude:175*direction,latitude:0},{node_id:'b',longitude:-175*direction,latitude:0}],segments:[{from:'a',to:'b'}]};
    const f={nodes:[{id:'a'},{id:'b'}],routes:[{result_index:7,attempt:99,node_ids:['a','b']}]};mount(geo,f);
    const p=pixels(),s=+C.dataset.scale,half=C.width/2,ratio=+C.dataset.dpr,rgb=V.routeColor(7).match(/\w\w/g).map(x=>parseInt(x,16));let route=0,far=0,left=0,right=0;
    for(let y=0;y<C.height;y++)for(let x=0;x<C.width;x++){const i=(y*C.width+x)*4;if(rgb.every((v,k)=>p[i+k]===v)){route++;if(Math.abs((x+.5)/ratio-half/ratio)>5*s+3||Math.abs((y+.5)/ratio-180)>8)far++;if(Math.abs((x+.5-half)/ratio)<7&&Math.abs((y+.5)/ratio-180)>1.25){if(x+.5<half)left++;else right++;}}}
    return {route,far,left,right,data:{...C.dataset}};
   },direction);console.log('seam',width,direction,JSON.stringify(result));await page.locator('#probe').screenshot({path:path.join(out,`seam-${width}-${direction}.png`)});assert.ok(result.route>3);assert.equal(result.far,0);assert.ok(direction===1?result.left>result.right:result.right>result.left);seams.push({direction,...result});
  }
  const stale=await page.evaluate(()=>{dispatchEvent(new Event('resize'));const callbacks=[...jobs.values()];mount({markers:[],segments:[]});const text=controls.detail.textContent;callbacks.forEach(f=>f());abort.abort();dispose();const old=JSON.stringify({...C.dataset});dispatchEvent(new Event('resize'));flush();return {preserved:controls.detail.textContent!==text?controls.detail.textContent==='위치가 있는 경로를 불러오세요.':true,pending:jobs.size,unchanged:old===JSON.stringify({...C.dataset})};});assert.ok(stale.preserved&&stale.unchanged);assert.equal(stale.pending,0);
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);results.push({width,world,zoom,group:all,route,seams,stale,errors,external});await page.close();
  // R1: genuine clicks in the retained application layout, before any detail
  // scrolling. The original and compact caveats express the same requirement.
  for(const maximum of [false,true]){
   report=maximum?maximumGroup():ordinaryReport;
   const {normalizeReport}=await import(require('node:url').pathToFileURL(path.join(__dirname,'state.js')));
   const {topologyModelFromReport,planTopologyDOM}=await import(require('node:url').pathToFileURL(path.join(__dirname,'topology-model.js')));
   const plan=planTopologyDOM(topologyModelFromReport(normalizeReport(report)),{view:'geo'});
   const members=plan.geo.markers.map(p=>report.compact_topology.nodes.find(n=>n.id===p.node_id));
   const alias=maximum?'Edge <img onerror=alert(1)> '.padEnd(256,'긴'):'';
   const app=await browser.newPage({viewport:{width,height:900},hasTouch:true}),appErrors=[],appExternal=[];
   app.on('pageerror',e=>appErrors.push(e.message));app.on('request',r=>{if(!r.url().startsWith(base))appExternal.push(r.url());});
   await app.addInitScript(({alias,members})=>{
    localStorage.setItem('checknetwork.ip-labels.v1',JSON.stringify(members.map(n=>({ip:n.address,label:alias,note:''}))));
    window.__geoChunks=[];const append=Element.prototype.append;
    Element.prototype.append=function(...items){if(this.id==='geo-map-result')window.__geoChunks.push(items.reduce((n,item)=>n+(item.nodeType===1?1:0)+(item.querySelectorAll?.('*').length||0),0));return append.apply(this,items);};
   },{alias,members});
   await app.goto(base+'/#topology');await app.locator('#connection-settings summary').click();await app.locator('#api-base-url').fill(base);await app.locator('#connection-settings summary').click();await app.locator('#run-topology').click();
   await app.waitForFunction(()=>document.querySelector('#topology-result').getAttribute('aria-busy')==='false'&&document.querySelector('.topology-canvas'));
   await app.locator('[data-view-link="geo-map"]').click();await app.waitForFunction(()=>document.querySelector('#geo-map-result').getAttribute('aria-busy')==='false'&&document.querySelector('.topology-geo-canvas')?.dataset.drawState==='rendered');
   const canvas=app.locator('.topology-geo-canvas'),detail=app.locator('#geo-node-detail');
   for(const fullscreen of [false,true]){
    if(fullscreen){await app.locator('#geo-map-fullscreen').click();await app.waitForFunction(()=>document.fullscreenElement?.id==='geo-map-view');}
    for(const target of maximum?[members.at(-1)]:[members[1],members[0]]){
     // Select the predecessor through retained native keyboard controls. Only
     // the subsequent genuine marker tap is the observation under test.
     const ordinal=members.indexOf(target)+1,previous=members[(ordinal+members.length-2)%members.length];
     await canvas.scrollIntoViewIfNeeded();await canvas.focus();let canvasID=await canvas.getAttribute('data-selected-node-id');
     for(let i=0;canvasID!==previous.id;i++){assert.ok(i<members.length);const current=members.findIndex(n=>n.id===canvasID);await app.keyboard.press(current<members.indexOf(previous)?']':'[');canvasID=await canvas.getAttribute('data-selected-node-id');}
     await canvas.scrollIntoViewIfNeeded();const b=await canvas.boundingBox(),before=await canvas.getAttribute('data-selected-node-id');
     assert.equal(await canvas.evaluate(c=>{const b=c.getBoundingClientRect();return document.elementFromPoint(b.x+b.width/2,b.y+b.height/2)===c;}),true,'tap reaches the usable map');
     if(width===375)await app.touchscreen.tap(b.x+b.width/2,b.y+b.height/2);else await app.mouse.click(b.x+b.width/2,b.y+b.height/2);
     assert.notEqual(before,target.id);assert.equal(await canvas.getAttribute('data-selected-node-id'),target.id);
     const observation=await detail.evaluate((d,{ip,count})=>{
      const box=d.getBoundingClientRect(),text=d.textContent,rect=r=>r.toJSON();
      const check=needle=>{const offset=text.indexOf(needle);if(offset<0)return {text:needle,present:false,visible:false};const range=document.createRange();range.setStart(d.firstChild,offset);range.setEnd(d.firstChild,offset+needle.length);const rs=[...range.getClientRects()].map(rect);return {text:needle,present:true,offset,rects:rs,visible:rs.length>0&&rs.every(r=>r.top>=Math.max(0,box.top)&&r.bottom<=Math.min(innerHeight,box.bottom)&&r.left>=Math.max(0,box.left)&&r.right<=Math.min(innerWidth,box.right)&&document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)===d)};};
      const caveat=['같은 좌표라도 같은 장비 보장 아님.','같은 좌표는 같은 장비를 의미하지 않습니다.'].find(s=>text.includes(s))||'같은 좌표라도 같은 장비 보장 아님.';
      const map=document.querySelector('.topology-geo-canvas').getBoundingClientRect(),panel=document.querySelector('.geo-node-panel').getBoundingClientRect();
      return {identity:check(ip),count:check(count),caveat:check(caveat),detailText:text,detailBox:rect(box),panel:rect(panel),scrollTop:d.scrollTop,overflow:document.documentElement.scrollWidth>innerWidth,elements:document.querySelectorAll('*').length,maxChunk:Math.max(...window.__geoChunks),usableMapPixels:Math.min(innerHeight,map.bottom,innerWidth<=760?panel.top:innerHeight)-Math.max(0,map.top)};
     },{ip:target.address,count:`동일 좌표 ${ordinal} / ${members.length}`});
     const rich=!!report.geo_details?.entries.find(e=>e.address===target.address);
     const row={width,fullscreen,kind:maximum?'maximum-alias-500':rich?'rich':'short',ordinal,members:members.length,before,after:target.id,aliasLength:alias.length,...observation};disclosure.push(row);
     fs.writeFileSync(path.join(out,'disclosure.json'),JSON.stringify(disclosure,null,2)+'\n');
     await app.screenshot({path:path.join(out,`app-group-${width}-${fullscreen}-${row.kind}.png`)});
     assert.ok(observation.identity.visible,'full IP immediately visible');assert.equal(observation.scrollTop,0);assert.equal(observation.overflow,false);
     assert.ok(observation.elements<=1200);assert.ok(observation.maxChunk<=100);assert.ok(observation.usableMapPixels>=120);
     assert.ok(observation.panel.height<=(width<=760?216:884));
     assert.ok(observation.count.present&&observation.caveat.present);
     assert.ok(observation.detailText.includes(alias));assert.ok(observation.detailText.includes('반복 탭으로 구성원 선택. 같은 좌표는 같은 장비를 의미하지 않습니다.'));
     assert.equal(await detail.getAttribute('tabindex'),'0');assert.equal(await detail.getAttribute('role'),'region');
     if(rich)for(const value of Object.values(report.geo_details.entries.find(e=>e.address===target.address)).filter(v=>typeof v==='string'))assert.ok(observation.detailText.includes(value),`unabridged supplemental value ${value}`);
     // Native keyboard scrolling must expose every non-whitespace glyph of
     // every fact and the maximum alias, not just a sampled final screenshot.
     const ranges=[];for(let i=0;i<observation.detailText.length;i++)if(!/\s/.test(observation.detailText[i]))ranges.push([i,i+1]);
     const seen=new Set();await detail.focus();await app.keyboard.press('Home');
     for(let step=0;step<120;step++){
      await app.waitForTimeout(130);
      // Chromium Range advances can exceed a tight line by one 1/64px
      // layout unit (e.g. a 14.015625px Hangul advance at the right edge).
      // Only the full-fact glyph walk allows that rounding; immediate IP,
      // count and caveat ranges above remain strictly inside both clips.
      const visible=await detail.evaluate((d,ranges)=>{const b=d.getBoundingClientRect(),rounding=1/64;return ranges.map(([start,end],i)=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return [...r.getClientRects()].every(x=>x.top>=Math.max(0,b.top)&&x.bottom<=Math.min(innerHeight,b.bottom)&&x.left>=Math.max(0,b.left)-rounding&&x.right<=Math.min(innerWidth,b.right)+rounding)?i:-1;}).filter(i=>i>=0);},ranges);visible.forEach(i=>seen.add(i));
      if(seen.size===ranges.length)break;await app.keyboard.press('ArrowDown');
     }
     row.readableGlyphs=seen.size;row.requiredGlyphs=ranges.length;
     if(seen.size!==ranges.length){row.missingGlyphs=await detail.evaluate((d,ranges)=>({scrollTop:d.scrollTop,scrollHeight:d.scrollHeight,clientHeight:d.clientHeight,box:d.getBoundingClientRect().toJSON(),glyphs:ranges.map(([start,end])=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return {start,text:d.textContent.slice(start,end),rects:[...r.getClientRects()].map(r=>r.toJSON())};})}),ranges.filter((_,i)=>!seen.has(i)));fs.writeFileSync(path.join(out,'disclosure.json'),JSON.stringify(disclosure,null,2)+'\n');}
     assert.equal(seen.size,ranges.length,`all retained facts and full alias readable via native scroll: ${JSON.stringify(row.missingGlyphs)}`);
     // Leave the previous selection at its tail: the next tap must reset it.
     await app.keyboard.press('End');await app.waitForFunction(()=>{const d=document.querySelector('#geo-node-detail');return d.scrollTop+d.clientHeight>=d.scrollHeight-1;});
     fs.writeFileSync(path.join(out,'disclosure.json'),JSON.stringify(disclosure,null,2)+'\n');
    }
   }
   assert.deepEqual(appErrors,[]);assert.deepEqual(appExternal,[]);assert.equal(await app.locator('#geo-map-view img').count(),0);await app.close();
  }
 }
 fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({browser:browser.version(),source,results,disclosure},null,2)+'\n');
 assert.equal(disclosure.length,12);assert.ok(disclosure.every(r=>r.identity.visible&&r.count.visible&&r.caveat.visible),'selected group member/count and same-coordinate caveat must be immediately visible, not merely textContent-present');
 console.log('context browser PASS',results.length,'R1 disclosure',disclosure.length);
 }finally{await browser?.close();server.closeAllConnections();await new Promise(r=>server.close(r));fs.writeFileSync(path.join(out,'cleanup.json'),JSON.stringify({pid:process.pid,base,serverListening:server.listening,browserConnected:browser?.isConnected()??false})+'\n');}
})().catch(e=>{console.error(e);process.exitCode=1;});
