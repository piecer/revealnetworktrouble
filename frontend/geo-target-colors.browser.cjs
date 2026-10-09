// Actual application + controlled compact reports. No external traces/providers.
// PLAYWRIGHT_PATH=... node frontend/geo-target-colors.browser.cjs OUTPUT
const {chromium}=require(process.env.PLAYWRIGHT_PATH||'playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const {pathToFileURL}=require('node:url');
const out=process.argv[2];assert.ok(out,'an owned evidence directory is required');fs.mkdirSync(out,{recursive:true});
const assets=['index.html','styles.css','app.js','common-prefix.js','state.js','topology-model.js','topology-presentation.js','topology-visualizer.js','topology-renderer.js','geo-map.js'];
const started_at='2026-10-09T00:00:00Z',stats=total=>({total,displayed:total,omitted:0}),routeStats=total=>({...stats(total),complete:total,partial:0});
function fixture(maximum=false,unlocated=false){
 const nodes=Array.from({length:maximum?500:6},(_,i)=>({id:`n${i}`,kind:'ip',address:`8.1.${Math.floor(i/256)}.${i%256}`,status:'healthy',observations:1,public_ip:true,hop_min:1,hop_max:25,...(unlocated?{}:{geolocation:{latitude:0,longitude:maximum?0:[-40,10,40,-10,-10,-25][i],city:'Controlled city',region:'Controlled region',country:'Controlled country',country_code:'US'}})}));
 const paths=maximum?Array.from({length:20},(_,i)=>nodes.slice(i*25,(i+1)*25).map(n=>n.id)):[['n0','n1','n4'],['n2','n1'],['n5','n1','n3']];
 const routes=[],results=[],result_stats=[],linkMap=new Map();
 paths.forEach((node_ids,result_index)=>{
  const address=result_index===1?'hostile-<img src=x onerror=alert(1)>-'+ '긴이름'.repeat(40):`target-${result_index}.example`;
  results.push({kind:'traceroute',address,status:'healthy',latency_ms:1,started_at,details:{attempts_total:2,attempts_reached:2,attempts_failed:0,attempts_unreached:0,attempts_execution_failed:0,attempts_timed_out:0,attempts_cancelled:0}});
  for(const attempt of [1,2])routes.push({result_index,attempt,status:'healthy',reached:true,complete:true,node_ids:[...node_ids]});
  node_ids.slice(1).forEach((to,i)=>{const from=node_ids[i];linkMap.set(`${from}\0${to}`,{from,to,status:'healthy',observations:2});});
  result_stats.push({result_index,routes:routeStats(2),node_observations:stats(node_ids.length*2),link_observations:stats((node_ids.length-1)*2)});
 });
 const links=[...linkMap.values()],observations=paths.reduce((n,p)=>n+p.length*2,0),linkObservations=paths.reduce((n,p)=>n+(p.length-1)*2,0);
 return {id:'controlled-target-colors',status:'healthy',started_at,duration_ms:1,summary:{total:results.length,passed:results.length,failed:0},results,compact_topology:{schema:'compact-v1',selection:'fair-complete-prefix-v1',limits:{nodes:500,links:1000,max_response_bytes_exclusive:1048576,max_geo_bundle_bytes:4096},nodes,links,routes,stats:{nodes:stats(nodes.length),links:stats(links.length),routes:routeStats(routes.length),node_observations:stats(observations),link_observations:stats(linkObservations)},result_stats,geo:{eligible:nodes.length,available:unlocated?0:nodes.length,included:unlocated?0:nodes.length,omitted:0,unavailable:unlocated?nodes.length:0},truncated:false}};
}
let report=fixture(),requests=0;
const server=http.createServer((req,res)=>{const name=new URL(req.url,'http://local').pathname.slice(1)||'index.html';if(name==='api/v1/reports'&&req.method==='POST'){requests++;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(report));return;}if(!assets.includes(name)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(path.join(__dirname,name)));});
(async()=>{let browser,base;const source={},results=[];try{
 const {normalizeReport}=await import(pathToFileURL(path.join(__dirname,'state.js')));
 const {routeColor}=await import(pathToFileURL(path.join(__dirname,'topology-visualizer.js')));
 for(const maximum of [false,true])normalizeReport(fixture(maximum));normalizeReport(fixture(false,true));
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base=`http://127.0.0.1:${server.address().port}`;
 for(const name of assets){const bytes=fs.readFileSync(path.join(__dirname,name));assert.deepEqual(Buffer.from(await(await fetch(`${base}/${name}`)).arrayBuffer()),bytes);source[name]=crypto.createHash('sha256').update(bytes).digest('hex');}
 browser=await chromium.launch();
 for(const width of [1440,375]){
  report=fixture();const page=await browser.newPage({viewport:{width,height:900},hasTouch:true}),errors=[],external=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text());});
  await page.route('**/*',r=>{if(!r.request().url().startsWith(base+'/')){external.push(r.request().url());return r.abort();}return r.continue();});
  await page.addInitScript(()=>{window.__chunks=[];const append=Element.prototype.append;Element.prototype.append=function(...items){if(this.id==='geo-map-result')window.__chunks.push(items.reduce((n,x)=>n+(x.nodeType===1?1:0)+(x.querySelectorAll?.('*').length||0),0));return append.apply(this,items);};});
  await page.goto(base+'/#topology');await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(base);await page.locator('#connection-settings summary').click();
  const ready=()=>page.waitForFunction(()=>{const geo=!document.querySelector('#geo-map-view').hidden,root=document.querySelector(geo?'#geo-map-result':'#topology-result');return root.getAttribute('aria-busy')==='false'&&['ready','render-empty'].includes(document.querySelector('#topology-workspace').dataset.state);});
  const view=async name=>{await page.locator(`[data-view-link="${name}"]`).click();await ready();};
  const load=async()=>{await page.locator('#run-topology').click();await ready();await view('geo-map');};
  await load();
  const canvas=page.locator('.topology-geo-canvas'),detail=page.locator('#geo-node-detail');
  const frame=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
  // The existing mobile sticky detail dock overlays the bottom of the page.
  // Use real page scrolling to bring the legend above it before claiming ink
  // visibility; a locator screenshot alone can capture only the opaque dock.
  const revealLegend=async()=>{const r=page.locator('.geo-target-legend');await r.scrollIntoViewIfNeeded();const b=await r.boundingBox();await page.mouse.move(1,200);await page.mouse.wheel(0,b.y-40);await page.waitForTimeout(180);assert.equal(await r.evaluate(e=>{const b=e.getBoundingClientRect();return [b.top+14,(b.top+b.bottom)/2,b.bottom-14].every(y=>e.contains(document.elementFromPoint(b.left+b.width/2,y)));}),true,'legend is actually visible, not covered by the existing dock');return r;};
  const readLegend=async()=>{
   const region=await revealLegend();await region.focus();await page.keyboard.press('Home');
   const required=await page.locator('.geo-target-entry').evaluateAll(es=>es.flatMap((e,entry)=>[...e.textContent].flatMap((glyph,i)=>/\s/.test(glyph)?[]:[`${entry}:${i}`]))),seen=new Set();
   for(let step=0;step<100&&seen.size<required.length;step++){
    await page.waitForTimeout(80);
    const visible=await region.evaluate(e=>{const b=e.getBoundingClientRect();return [...e.querySelectorAll('.geo-target-entry')].flatMap((entry,index)=>[...entry.textContent].flatMap((glyph,i)=>{
     if(/\s/.test(glyph))return[];const r=document.createRange();r.setStart(entry.firstChild,i);r.setEnd(entry.firstChild,i+1);
     return [...r.getClientRects()].every(x=>x.top>=Math.max(0,b.top)&&x.bottom<=Math.min(innerHeight,b.bottom)&&x.left>=b.left&&x.right<=b.right&&entry.contains(document.elementFromPoint(x.x+x.width/2,x.y+x.height/2)))?[`${index}:${i}`]:[];
    }));});visible.forEach(id=>seen.add(id));if(seen.size<required.length)await page.keyboard.press('ArrowDown');
   }
   assert.equal(seen.size,required.length,'every full target-number/name glyph readable by native scrolling, with occlusion checks');return {required:required.length,readable:seen.size};
  };
  // Check the actual color swatch, not only a style variable or text association.
  const legend=async()=>{
   const entries=await page.locator('.geo-target-entry').evaluateAll(es=>es.map(e=>({id:+e.dataset.resultIndex,text:e.textContent,color:getComputedStyle(e,'::before').backgroundColor,content:getComputedStyle(e,'::before').content,wrap:getComputedStyle(e).overflowWrap,children:e.children.length,scroll:e.scrollWidth,width:e.clientWidth})));
   for(const e of entries){assert.equal(e.text,`대상 ${e.id+1} · ${report.results[e.id].address}`);const rgb=routeColor(e.id).match(/\w\w/g).map(x=>parseInt(x,16));assert.equal(e.color,`rgb(${rgb.join(', ')})`);assert.equal(e.content,'""');assert.equal(e.wrap,'anywhere');assert.equal(e.children,0);assert.ok(e.scroll<=e.width);}
   assert.equal(await page.locator('.geo-target-legend').getAttribute('tabindex'),'0');return entries;
  };
  const smallLegend=await legend();assert.equal(smallLegend.length,3);
  const pixelProof=async(id,expected,radius)=>canvas.evaluate((c,{node,expected,radius})=>{
   const ratio=+c.dataset.dpr,w=c.width/ratio,h=c.height/ratio,[lon,lat]=c.dataset.center.split(',').map(Number),s=+c.dataset.scale,delta=((node.geolocation.longitude-lon+180)%360+360)%360-180,x=w/2+delta*s,y=h/2+(lat-node.geolocation.latitude)*s;
   const bytes=c.getContext('2d').getImageData(0,0,c.width,c.height).data,counts=Object.fromEntries(expected.map(color=>[color,0]));
   for(let py=Math.floor((y-radius)*ratio);py<Math.ceil((y+radius)*ratio);py++)for(let px=Math.floor((x-radius)*ratio);px<Math.ceil((x+radius)*ratio);px++){
    const d=Math.hypot((px+.5)/ratio-x,(py+.5)/ratio-y);if(d>radius||d<(radius>8?7:0)||px<0||py<0||px>=c.width||py>=c.height)continue;
    const offset=(py*c.width+px)*4;for(const color of expected)if(color.match(/\w\w/g).map(z=>parseInt(z,16)).every((v,k)=>bytes[offset+k]===v))counts[color]++;
   }
   const b=c.getBoundingClientRect();return{counts,x:b.x+x*b.width/w,y:b.y+y*b.height/h,data:{...c.dataset}};
  },{node:report.compact_topology.nodes.find(n=>n.id===id),expected,radius});
  const click=async(id,p)=>{assert.equal(await canvas.evaluate((c,p)=>document.elementFromPoint(p.x,p.y)===c,p),true,'real marker is not occluded');if(width===375)await page.touchscreen.tap(p.x,p.y);else await page.mouse.click(p.x,p.y);await frame();assert.equal(await canvas.getAttribute('data-selected-node-id'),id);};
  await canvas.scrollIntoViewIfNeeded();const pixels=[];
  for(const [id,target] of [['n0',0],['n2',1],['n5',2]]){const p=await pixelProof(id,[routeColor(target)],3);assert.ok(p.counts[routeColor(target)]>=12,JSON.stringify(p));await click(id,p);assert.ok((await detail.textContent()).includes(`대상 ${target+1} · ${report.results[target].address}`));pixels.push({id,target,...p});}
  const shared=await pixelProof('n1',[0,1,2].map(routeColor),6);assert.ok(Object.values(shared.counts).every(n=>n>=5));await click('n1',shared);
  for(let i=0;i<3;i++)assert.ok((await detail.textContent()).includes(`대상 ${i+1} · ${report.results[i].address}`));
  const group=await pixelProof('n4',[0,2].map(routeColor),11);assert.ok(Object.values(group.counts).every(n=>n>8));await click('n4',group);assert.match(await detail.textContent(),/동일 좌표 1 \/ 2/);await click('n3',group);assert.match(await detail.textContent(),/동일 좌표 2 \/ 2/);assert.match(await detail.textContent(),/동일 좌표 전체 대상/);
  await canvas.focus();await page.keyboard.press('[');assert.notEqual(await canvas.getAttribute('data-selected-node-id'),'n3');
  await canvas.screenshot({path:path.join(out,`markers-${width}.png`)});
  await revealLegend();await page.locator('.geo-target-legend').screenshot({path:path.join(out,`legend-${width}.png`)});
  const smallReadable=await readLegend();await page.locator('.geo-target-legend').screenshot({path:path.join(out,`legend-end-${width}.png`)});
  const ax=await page.context().newCDPSession(page),tree=await ax.send('Accessibility.getFullAXTree');assert.ok(tree.nodes.some(n=>n.role?.value==='button'&&n.name?.value.includes('대상 2 · '+report.results[1].address)));await ax.detach();
  // Original numbers/colors survive selection filtering rather than reindexing.
  await view('topology');await page.locator('[data-filter-action="none"]').click();await ready();
  const filters=page.locator('#topology-target-filter input[data-target-index]');assert.equal(await filters.count(),3);await filters.nth(1).focus();await page.keyboard.press('Space');await ready();await view('geo-map');
  const filtered=await legend();assert.deepEqual(filtered.map(e=>e.id),[1]);await canvas.scrollIntoViewIfNeeded();const retained=await pixelProof('n2',[routeColor(1)],3);assert.ok(retained.counts[routeColor(1)]>=12);
  // Replacement report: twenty identities, 500 coincident original nodes.
  report=fixture(true);await view('topology');await load();assert.equal(await canvas.getAttribute('data-markers'),'500');
  const maximumLegend=await legend();assert.equal(maximumLegend.length,20);await canvas.scrollIntoViewIfNeeded();
  const capped=await pixelProof('n0',[0,1,2,3].map(routeColor).concat('#94a3b8'),11);assert.ok(Object.values(capped.counts).every(n=>n>5));
  await click('n25',capped);const full=await detail.textContent();assert.match(full,/동일 좌표 2 \/ 500/);assert.match(full,/16개 색 생략/);for(let i=0;i<20;i++)assert.ok(full.includes(`대상 ${i+1} · ${report.results[i].address}`));
  const seen=new Set();for(let p=0;p<5;p++){const ids=await page.locator('.topology-geo-list button:not([hidden])').evaluateAll(bs=>bs.map(b=>b.dataset.nodeId));ids.forEach(id=>seen.add(id));if(p<4)await page.locator('#geo-page-next').click();}assert.equal(seen.size,500);
  const geometry=await page.evaluate(()=>({elements:document.querySelectorAll('*').length,maxChunk:Math.max(...__chunks),slots:document.querySelectorAll('.topology-geo-list button').length,overflow:document.documentElement.scrollWidth>innerWidth}));assert.ok(geometry.elements<=1200);assert.ok(geometry.maxChunk<=100);assert.equal(geometry.slots,100);assert.equal(geometry.overflow,false);
  await canvas.screenshot({path:path.join(out,`maximum-${width}.png`)});
  // Every full target name is reachable by native scrolling in the legend.
  const maximumReadable=await readLegend();
  const region=await revealLegend();await region.focus();await page.keyboard.press('End');await page.waitForFunction(()=>{const e=document.querySelector('.geo-target-legend');return e.scrollTop+e.clientHeight>=e.scrollHeight-1;});
  assert.equal(await page.locator('.geo-target-entry').last().evaluate(e=>{const r=e.getBoundingClientRect(),p=e.parentElement.getBoundingClientRect();return r.top>=p.top&&r.bottom<=p.bottom;}),true);
  await region.screenshot({path:path.join(out,`maximum-legend-end-${width}.png`)});
  report=fixture(false,true);await view('topology');await load();assert.equal(await canvas.getAttribute('data-markers'),'0');assert.equal((await legend()).length,3);assert.match(await page.locator('#geo-map-message').textContent(),/위치가 식별된.*없습니다/);
  await view('topology');await page.locator('[data-filter-action="none"]').click();await ready();await view('geo-map');assert.equal(await page.locator('.geo-target-entry').count(),0);assert.doesNotMatch(await detail.textContent(),/target-19/);
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);results.push({width,smallLegend,smallReadable,pixels,shared,group,filtered,maximumLegend,maximumReadable,capped,geometry,locationsReached:seen.size,errors,external});
  fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({browser:browser.version(),source,requests,results},null,2)+'\n');await page.close();
 }
 console.log('target-color app browser PASS',results.length,'desktop/mobile');
}finally{await browser?.close();server.closeAllConnections();await new Promise(r=>server.close(r));fs.writeFileSync(path.join(out,'cleanup.json'),JSON.stringify({pid:process.pid,base,serverListening:server.listening,browserConnected:browser?.isConnected()??false})+'\n');}
})().catch(e=>{console.error(e);process.exitCode=1;});
