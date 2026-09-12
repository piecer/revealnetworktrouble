// Owned loopback source server + explicitly synthetic compact reports, not live GeoIP evidence.
// PLAYWRIGHT_PATH=/path/to/playwright-core node frontend/geo-details.browser.cjs OUTPUT
const {chromium}=require(process.env.PLAYWRIGHT_PATH || 'playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http'),crypto=require('node:crypto');
const {pathToFileURL}=require('node:url');
const output=process.argv[2];
if(!output)throw Error('an evidence output directory is required');
fs.mkdirSync(output,{recursive:true,mode:0o700});
const assets=['index.html','styles.css','app.js','common-prefix.js','state.js','topology-model.js','topology-presentation.js','topology-visualizer.js','topology-renderer.js','geo-map.js'];
const stats=total=>({total,displayed:total,omitted:0});
const routeStats=total=>({...stats(total),complete:total,partial:0});
function fixture(size) {
 const started_at='2026-09-01T00:00:00Z';
 const nodes=Array.from({length:size},(_,i)=>({id:`n${i}`,kind:'ip',address:`8.1.${Math.floor(i/256)}.${i%256}`,status:'healthy',hop_min:i%25+1,hop_max:i%25+1,observations:1,public_ip:true,geolocation:{latitude:-65+Math.floor(i/25)*6.5,longitude:-170+(i%25)*14,city:`Synthetic city ${i}`,region:'Synthetic region',country:'Synthetic country',country_code:'US'},...(i%2?{}:{latency_ms_avg:0,asn:{number:15169,organization:'Synthetic <organization>'}})}));
 const routes=[],links=[],results=[],result_stats=[];
 for(let start=0;start<size;start+=25){const row=nodes.slice(start,start+25),result_index=results.length;
  results.push({kind:'traceroute',address:row.at(-1).address,status:'healthy',latency_ms:1,started_at,details:{attempts_total:1,attempts_reached:1,attempts_failed:0,attempts_unreached:0,attempts_execution_failed:0,attempts_timed_out:0,attempts_cancelled:0}});
  routes.push({result_index,attempt:1,status:'healthy',reached:true,complete:true,node_ids:row.map(n=>n.id)});
  for(let i=1;i<row.length;i++)links.push({from:row[i-1].id,to:row[i].id,status:'healthy',observations:1});
  result_stats.push({result_index,routes:routeStats(1),node_observations:stats(row.length),link_observations:stats(row.length-1)});
 }
 return {id:`synthetic-geo-${size}`,status:'healthy',started_at,duration_ms:1,summary:{total:results.length,passed:results.length,failed:0},results,compact_topology:{schema:'compact-v1',selection:'fair-complete-prefix-v1',limits:{nodes:500,links:1000,max_response_bytes_exclusive:1048576,max_geo_bundle_bytes:4096},nodes,links,routes,stats:{nodes:stats(nodes.length),links:stats(links.length),routes:routeStats(routes.length),node_observations:stats(nodes.length),link_observations:stats(links.length)},result_stats,geo:{eligible:size,available:size,included:size,omitted:0,unavailable:0},truncated:false}};
}
let report=fixture(500),requests=0;
const server=http.createServer((req,res)=>{
 const name=new URL(req.url,'http://local').pathname;
 if(name==='/api/v1/reports'&&req.method==='POST'){requests++;res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(report));return;}
 const asset=name==='/'?'index.html':name.slice(1);
 if(!assets.includes(asset)){res.writeHead(404);res.end();return;}
 res.setHeader('Content-Type',asset.endsWith('.js')?'text/javascript':asset.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(path.join(__dirname,asset)));
});
(async()=>{
 let browser,base;const results=[],source={};
 try {
  const {normalizeReport}=await import(pathToFileURL(path.join(__dirname,'state.js')));
  normalizeReport(report); // Fixture must satisfy the real closed consumer before a browser run.
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  base=`http://127.0.0.1:${server.address().port}`;
  for(const asset of assets){const bytes=fs.readFileSync(path.join(__dirname,asset));assert.deepEqual(Buffer.from(await(await fetch(`${base}/${asset}`)).arrayBuffer()),bytes);source[asset]=crypto.createHash('sha256').update(bytes).digest('hex');}
  browser=await chromium.launch();
  // R1: measure real marker selections BEFORE any detail scrolling. Ordinary
  // aliases reproduce this too; maximum aliases must remain completely readable.
  const layoutResults=[];
  for(const [width,height] of [[1440,900],[1440,720],[375,812],[320,640]]) for(const long of [false,true]) {
   report=fixture(25);
   report.compact_topology.nodes.forEach((n,i)=>{n.geolocation.latitude=0;n.geolocation.longitude=-20+40*i/24;});
   normalizeReport(report);
   const alias=long?'Edge <img onerror=alert(1)> '.padEnd(256,'긴'):'Edge A';
   assert.ok(alias.length<=256);
   const page=await browser.newPage({viewport:{width,height},hasTouch:true}),errors=[],external=[];
   page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url());});
   await page.addInitScript(({alias})=>localStorage.setItem('checknetwork.ip-labels.v1',JSON.stringify([{ip:'8.1.0.0',label:alias,note:''},{ip:'8.1.0.1',label:'Edge B',note:''}])),{alias});
   await page.goto(base+'/#topology');await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(base);await page.locator('#connection-settings summary').click();
   await page.locator('#run-topology').click();await page.waitForFunction(()=>document.querySelector('#topology-result').getAttribute('aria-busy')==='false'&&document.querySelector('.topology-canvas'));
   await page.locator('[data-view-link="geo-map"]').click();await page.waitForFunction(()=>document.querySelector('#geo-map-result').getAttribute('aria-busy')==='false'&&document.querySelector('.topology-geo-canvas')?.dataset.drawState==='rendered');
   const canvas=page.locator('.topology-geo-canvas'),detail=page.locator('#geo-node-detail');
   const frame=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const clickMarker=async (id,touch=false)=>{const p=await canvas.evaluate((c,n)=>{const r=c.getBoundingClientRect(),[lon,lat]=c.dataset.center.split(',').map(Number),s=+c.dataset.scale,w=c.width/+c.dataset.dpr,h=c.height/+c.dataset.dpr,delta=((n.geolocation.longitude-lon+180)%360+360)%360-180;return{x:r.left+(w/2+delta*s)*r.width/w,y:r.top+(h/2+(lat-n.geolocation.latitude)*s)*r.height/h};},report.compact_topology.nodes.find(n=>n.id===id));assert.ok(p.x>=0&&p.x<width&&p.y>=0&&p.y<height,JSON.stringify(p));if(touch)await page.touchscreen.tap(p.x,p.y);else await page.mouse.click(p.x,p.y);await frame();const hit=await page.evaluate(p=>({target:document.elementFromPoint(p.x,p.y)?.outerHTML.slice(0,250),panel:document.querySelector('.geo-node-panel').getBoundingClientRect().toJSON(),canvas:document.querySelector('.topology-geo-canvas').getBoundingClientRect().toJSON(),scrollY}),p);await page.screenshot({path:path.join(output,`hit-${width}x${height}-${long}.png`)});assert.equal(await canvas.getAttribute('data-selected-node-id'),id,JSON.stringify({width,height,long,p,hit}));};
   const visibleIdentity=async phase=>{
    const g=await detail.evaluate(d=>{const r=document.createRange();r.setStart(d.firstChild,0);r.setEnd(d.firstChild,d.textContent.indexOf(' · '));const identity=r.getBoundingClientRect(),box=d.getBoundingClientRect(),canvas=document.querySelector('.topology-geo-canvas').getBoundingClientRect();return {identity:identity.toJSON(),detail:box.toJSON(),canvas:canvas.toJSON(),scrollTop:d.scrollTop,elements:document.querySelectorAll('*').length,overflow:document.documentElement.scrollWidth>innerWidth};});
    layoutResults.push({width,height,long,phase,...g});fs.writeFileSync(path.join(output,'layout-results.json'),JSON.stringify(layoutResults,null,2)+'\n');
    await page.screenshot({path:path.join(output,`layout-${width}x${height}-${long?'max':'normal'}-${phase}.png`)});
    assert.ok(g.identity.top>=Math.max(0,g.detail.top)&&g.identity.bottom<=Math.min(height,g.detail.bottom),`R1 selected identity must be visibly readable after ${phase}: ${JSON.stringify(g)}`);
    assert.ok(g.canvas.top<height&&g.canvas.bottom>0,'map remains visible');assert.ok(g.elements<=1200);assert.equal(g.overflow,false);
    assert.equal(await detail.evaluate(d=>{const r=document.createRange();r.setStart(d.firstChild,0);r.setEnd(d.firstChild,1);const b=r.getBoundingClientRect();return document.elementFromPoint(b.x+b.width/2,b.y+b.height/2)===d;}),true,'selected identity is not obscured');
   };
   const readFacts=async phase=>{
    assert.equal(await detail.getAttribute('tabindex'),'0');assert.equal(await detail.getAttribute('role'),'region');
    const heading=page.locator('#geo-node-title');assert.match(await heading.textContent(),/스크롤/);
    const box=await detail.boundingBox(),panel=await page.locator('.geo-node-panel').boundingBox();
    assert.ok(box.y>=0&&box.y+box.height<=height,`${phase} scroll region must fit viewport: ${JSON.stringify(box)}`);
    assert.ok(panel.height<=(width<=760?216:height-16),'bounded panel retains space for the map');
    const text=await detail.textContent();assert.ok(text.includes(alias),'full alias is retained');
    const tokens=['8.1.0.0','국가','지역','도시','AS15169','Synthetic <organization>','관측 평균 RTT','링크 지연 아님','HOP 1','경로 1','시도 1','GeoIP','보장하지 않습니다.','위도 0','경도 -20'];
    tokens.forEach(token=>assert.ok(text.includes(token),token));
    // Measure ink, not collapsed trailing spaces whose Range can exceed a line.
    // Cover every non-whitespace glyph, including the entire unabridged alias.
    const ranges=[];for(let i=0;i<text.length;i++)if(!/\s/.test(text[i]))ranges.push([i,i+1]);
    const seen=new Set();await detail.focus();await page.keyboard.press('Home');
    for(let step=0;step<30;step++) {
     await page.waitForTimeout(130);
     const visible=await detail.evaluate((d,ranges)=>{const b=d.getBoundingClientRect();return ranges.map(([start,end],i)=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return [...r.getClientRects()].every(x=>x.top>=Math.max(0,b.top)&&x.bottom<=Math.min(innerHeight,b.bottom)&&x.left>=b.left&&x.right<=b.right)?i:-1;}).filter(i=>i>=0);},ranges);visible.forEach(i=>seen.add(i));
     if(seen.size===ranges.length)break;await page.keyboard.press('ArrowDown');
    }
    assert.equal(seen.size,ranges.length,`${phase}: ${width}x${height} long=${long}: every fact and alias tail is readable through native keyboard scrolling; missing ${JSON.stringify(await detail.evaluate((d,ranges)=>{const b=d.getBoundingClientRect();return {box:b.toJSON(),scrollTop:d.scrollTop,ranges:ranges.map(([start,end])=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return [...r.getClientRects()].map(x=>x.toJSON());})};},ranges.filter((_,i)=>!seen.has(i))))}`);
    await page.keyboard.press('End');await page.waitForFunction(()=>{const d=document.querySelector('#geo-node-detail');return d.scrollTop+d.clientHeight>=d.scrollHeight-1;});
    // Touch dragging scrolls the same bounded region; no synthetic DOM scroll.
    if(width<=760){await page.keyboard.press('Home');await page.waitForFunction(()=>document.querySelector('#geo-node-detail').scrollTop===0);const cdp=await page.context().newCDPSession(page);const b=await detail.boundingBox(),x=b.x+b.width/2;
     await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y:b.y+b.height-8}]});
     for(let i=1;i<=6;i++){await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x,y:b.y+b.height-8-(b.height-16)*i/6}]});await page.waitForTimeout(20);}
     await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await page.waitForFunction(()=>document.querySelector('#geo-node-detail').scrollTop>0);await cdp.detach();
    }
    const usableMap=await canvas.evaluate(c=>{const b=c.getBoundingClientRect(),p=document.querySelector('.geo-node-panel').getBoundingClientRect();const bottom=innerWidth<=760?Math.min(innerHeight,p.top,b.bottom):Math.min(innerHeight,b.bottom);return bottom-Math.max(0,b.top);});
    assert.ok(usableMap>=120,`${phase}: a usable map area remains alongside the detail`);
    layoutResults.push({width,height,long,phase,readableGlyphs:seen.size,requiredGlyphs:ranges.length,fullAliasLength:alias.length,keyboardScroll:true,touchScroll:width<=760,usableMapPixels:usableMap});fs.writeFileSync(path.join(output,'layout-results.json'),JSON.stringify(layoutResults,null,2)+'\n');
   };
   await canvas.scrollIntoViewIfNeeded();await clickMarker('n1');await visibleIdentity('map-click');
   await page.mouse.move(1,Math.max(1,(await canvas.boundingBox()).y-20));await page.mouse.wheel(0,260);await page.waitForTimeout(180);
   await clickMarker('n0',width<=760);await visibleIdentity('page-scroll');assert.ok((await detail.textContent()).includes(alias));
   await readFacts('page-detail-access');
   await canvas.focus();await page.keyboard.press('Home');await page.keyboard.press('-');await page.keyboard.press('ArrowRight');await frame();await clickMarker('n1');await visibleIdentity('pan-zoom-after-detail-scroll');
   await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>document.fullscreenElement?.id==='geo-map-view');await canvas.scrollIntoViewIfNeeded();await canvas.focus();await page.keyboard.press('Home');await frame();await clickMarker('n0');await visibleIdentity('fullscreen');
   assert.ok((await detail.textContent()).includes(alias));assert.match(await detail.textContent(),/AS15169.*Synthetic <organization>/);assert.match(await detail.textContent(),/0 ms \(링크 지연 아님\)/);assert.match(await detail.textContent(),/HOP 1/);assert.match(await detail.textContent(),/경로 1 · 시도 1/);assert.match(await detail.textContent(),/GeoIP.*추정/);assert.match(await detail.textContent(),/좌표:/);assert.equal(await page.locator('#geo-map-view img').count(),0);
   await readFacts('fullscreen-detail-access');
   assert.deepEqual(errors,[]);assert.deepEqual(external,[]);await page.close();
  }
  for(const width of [1440,375]) {
   report=fixture(500);
   const page=await browser.newPage({viewport:{width,height:1000},hasTouch:true});
   const errors=[],external=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url());});
   await page.addInitScript(()=>{
    localStorage.setItem('checknetwork.ip-labels.v1',JSON.stringify([{ip:'8.1.0.0',label:'Edge <img onerror=alert(1)> '+ '긴별칭'.repeat(70),note:''}]));
    window.__geoChunks=[];
    const append=Element.prototype.append;
    Element.prototype.append=function(...items){if(this.id==='geo-map-result')window.__geoChunks.push(items.reduce((n,item)=>n+(item.nodeType===1?1:0)+(item.querySelectorAll?.('*').length||0),0));return append.apply(this,items);};
   });
   await page.goto(base+'/#topology');
   await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(base);await page.locator('#connection-settings summary').click();
   const ready=()=>page.waitForFunction(()=>{const root=document.querySelector(document.querySelector('#geo-map-view').hidden?'#topology-result':'#geo-map-result');return ['ready','render-empty'].includes(document.querySelector('#topology-workspace').dataset.state)&&root.getAttribute('aria-busy')==='false';});
   const view=async name=>{await page.locator(`[data-view-link="${name}"]`).click();if(['topology','geo-map'].includes(name))await ready();};
   await page.locator('#run-topology').click();await ready();await view('geo-map');
   const canvas=page.locator('.topology-geo-canvas'),detail=page.locator('#geo-node-detail');
   await page.waitForFunction(()=>document.querySelector('.topology-geo-canvas')?.dataset.drawState==='rendered');
   assert.equal(await canvas.getAttribute('data-markers'),'500');
   const seen=new Set(),slots=await page.locator('.topology-geo-list button').count();assert.equal(slots,100);
   for(let p=0;p<5;p++) {
    const ids=await page.locator('.topology-geo-list button:not([hidden])').evaluateAll(nodes=>nodes.map(n=>n.dataset.nodeId));ids.forEach(id=>seen.add(id));
    const last=page.locator('.topology-geo-list button:not([hidden])').last();await last.click();assert.equal(await canvas.getAttribute('data-selected-node-id'),ids.at(-1));
    const before=await page.locator('*').count();assert.ok(before<=1200);assert.equal(await page.locator('.topology-geo-list button').count(),slots);
    if(p<4)await page.locator('#geo-page-next').click();
   }
   assert.equal(seen.size,500);assert.equal(await page.locator('#geo-page-next').isDisabled(),true);
   // Selecting by a real pointer after fit/pan/zoom must select the exact marker.
   await canvas.scrollIntoViewIfNeeded();await canvas.focus();await page.keyboard.press('Home');await page.keyboard.press('-');await page.keyboard.press('ArrowRight');
   await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const point=async id=>canvas.evaluate((c,node)=>{const r=c.getBoundingClientRect(),[lon,lat]=c.dataset.center.split(',').map(Number),s=Number(c.dataset.scale),w=c.width/Number(c.dataset.dpr),h=c.height/Number(c.dataset.dpr),delta=((node.geolocation.longitude-lon+180)%360+360)%360-180;return{x:r.left+(w/2+delta*s)*r.width/w,y:r.top+(h/2+(lat-node.geolocation.latitude)*s)*r.height/h};},report.compact_topology.nodes.find(n=>n.id===id));
   let p=await point('n262');await page.mouse.click(p.x,p.y);assert.equal(await canvas.getAttribute('data-selected-node-id'),'n262');
   p=await point('n263');await page.touchscreen.tap(p.x,p.y);assert.equal(await canvas.getAttribute('data-selected-node-id'),'n263');assert.match(await detail.textContent(),/RTT 미측정/);
   const beforeDrag=await canvas.getAttribute('data-selected-node-id');p=await point('n262');await page.mouse.move(p.x,p.y);await page.mouse.down();await page.mouse.move(p.x+35,p.y+15,{steps:4});await page.mouse.up();assert.equal(await canvas.getAttribute('data-selected-node-id'),beforeDrag);
   await canvas.focus();await page.keyboard.press(']');assert.notEqual(await canvas.getAttribute('data-selected-node-id'),beforeDrag);
   // Page navigation remains native-keyboard operable and identity is complete.
   while(await page.locator('#geo-page-previous').isEnabled())await page.locator('#geo-page-previous').click();
   await page.locator('.topology-geo-list button[data-node-id="n0"]').focus();await page.keyboard.press('Enter');
   assert.match(await detail.textContent(),/Edge <img onerror=alert\(1\)>/);assert.match(await detail.textContent(),/AS15169/);assert.match(await detail.textContent(),/0 ms \(링크 지연 아님\)/);assert.match(await detail.textContent(),/GeoIP.*추정/);assert.equal(await page.locator('#geo-map-view img').count(),0);
   const geometry=await page.evaluate(()=>({elements:document.querySelectorAll('*').length,overflow:document.documentElement.scrollWidth>innerWidth,maxChunk:Math.max(...window.__geoChunks),detail:document.querySelector('#geo-node-detail').getBoundingClientRect().toJSON()}));assert.ok(geometry.elements<=1200);assert.equal(geometry.overflow,false);assert.ok(geometry.maxChunk<=100);
   await detail.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,`detail-${width}.png`)});
   const ax=await page.context().newCDPSession(page);const tree=await ax.send('Accessibility.getFullAXTree');assert.ok(tree.nodes.some(n=>n.role?.value==='button'&&n.name?.value.includes('8.1.0.0')&&n.name.value.includes('Synthetic city 0')));await ax.detach();
   // Use a small report for complete topology parity under its independent DOM budget.
   report=fixture(2);await view('topology');await page.locator('#run-topology').click();await ready();
   const newGraphState=await page.locator('.topology-canvas').evaluate(c=>({data:{...c.dataset},status:document.querySelector('#topology-render-status').textContent,state:document.querySelector('#topology-workspace').dataset.state}));
   assert.equal(await page.locator('.topology-canvas').getAttribute('data-selected-node-id'),'',JSON.stringify(newGraphState));
   await view('geo-map');await page.locator('#geo-node-next').click();assert.equal(await canvas.getAttribute('data-selected-node-id'),'n1');await view('topology');
   const graph=page.locator('.topology-canvas');assert.equal(await graph.getAttribute('data-selected-node-id'),'n1');await graph.focus();await page.keyboard.press('Enter');assert.equal(await page.locator('#topology-label-address').inputValue(),'8.1.0.1');
   await page.locator('#topology-label-name').fill('Edited alias');await page.locator('#topology-label-form button[type="submit"]').click();await ready();
   await view('geo-map');assert.match(await detail.textContent(),/Edited alias/);assert.equal(await canvas.getAttribute('data-selected-node-id'),'n1');
   for(let i=0;i<3;i++){await view('topology');assert.equal(await page.locator('.topology-geo-canvas').count(),0);await view('geo-map');assert.equal(await canvas.getAttribute('data-selected-node-id'),'n1');}
   await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>document.fullscreenElement?.id==='geo-map-view');
   assert.equal(await page.locator('#geo-node-previous').isVisible(),true);await page.locator('#geo-node-previous').click();assert.equal(await canvas.getAttribute('data-selected-node-id'),'n0');
   assert.match(await detail.textContent(),/8\.1\.0\.0/);await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>!document.fullscreenElement);
   await view('topology');await page.locator('[data-filter-action="none"]').click();await ready();await view('geo-map');assert.equal(await canvas.getAttribute('data-markers'),'0');assert.doesNotMatch(await detail.textContent(),/Edited alias|8\.1\.0\.1/);
   assert.equal(await page.locator('#geo-node-next').isDisabled(),true);
   // Canvas failure still offers full accessible node selection.
   await page.evaluate(()=>{HTMLCanvasElement.prototype.getContext=()=>null;});await view('topology');await page.locator('[data-filter-action="all"]').click();await ready();await view('geo-map');
   assert.equal(await canvas.getAttribute('data-draw-state'),'unavailable');await page.locator('#geo-node-next').click();assert.equal(await canvas.getAttribute('data-selected-node-id'),'n1');
   assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
   results.push({width,fixture:'synthetic compact-v1; no upstream lookup',markersReached:seen.size,slots,...geometry,keyboard:true,mouse:true,touch:true,dragPreserved:true,sharedIdentity:true,aliasEditor:true,filterReset:true,fullscreenDetail:true,canvasFallback:true,axButtonNames:true,errors,externalRequests:external.length});await page.close();
  }
  fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({browser:browser.version(),source,requests,layoutResults,results},null,2)+'\n');console.log(JSON.stringify({requests,layoutObservations:layoutResults.length,results}));
 } finally {if(browser)await browser.close();await new Promise(resolve=>server.close(resolve));fs.writeFileSync(path.join(output,'cleanup.json'),JSON.stringify({pid:process.pid,base,serverListening:server.listening,browserConnected:browser?.isConnected()??false})+'\n');console.log('owned browser and loopback server closed');}
})().catch(error=>{console.error(error);process.exitCode=1;});
