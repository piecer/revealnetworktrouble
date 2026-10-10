// Actual application browser gate. Standalone mode replays frozen Go report and
// context fixtures through an owned HTTP server; it is NOT a live provider run.
// Parent mode accepts {api,ui,frontend,output}, uses the actual Go API unchanged,
// and never intercepts API delivery. External provider datasets remain controlled.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),crypto=require('node:crypto'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_PATH||'playwright');
const arg=process.argv[2];assert.ok(arg,'owned output directory or parent config required');
const parentMode=arg.startsWith('{'),config=parentMode?JSON.parse(arg):{frontend:__dirname,output:arg};
fs.mkdirSync(config.output,{recursive:true});
const assets=['index.html','styles.css','app.js','common-prefix.js','state.js','topology-model.js','topology-presentation.js','topology-renderer.js','topology-visualizer.js','geo-map.js'];
const executable=process.env.IP_CONTEXT_CHROMIUM||'/home/piecer/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome';
const hash=b=>crypto.createHash('sha256').update(b).digest('hex');
const save=(name,value)=>fs.writeFileSync(path.join(config.output,name),JSON.stringify(value,null,2)+'\n');
let server,browser,currentPage;
const source={},runs=[];
const corpus=JSON.parse(fs.readFileSync(path.join(config.frontend,'../testdata/ip-context-corpus.json'))).cases;
function controlledContext(address){
 const value=structuredClone(corpus[0].projection),now=new Date().toISOString();
 value.address=address;value.fetched_at=now;value.expires_at=new Date(Date.parse(now)+300000).toISOString();
 value.reverse_dns.fetched_at=now;value.registration.fetched_at=now;value.routing.fetched_at=now;
 value.registration.start_address=address.slice(0,address.lastIndexOf('.')+1)+'0';value.registration.end_address=address.slice(0,address.lastIndexOf('.')+1)+'255';
 value.registration.organization='Context <fixture> organization';value.routing.prefix=value.registration.start_address+'/24';
 value.routing.origins.forEach(o=>o.rpki.checked_at=now);return value;
}
(async()=>{try{
 if(!parentMode){
  const report=fs.readFileSync(path.join(config.frontend,'../testdata/geo-details-rich-compact-report.json'));
  server=http.createServer((req,res)=>{
   const name=new URL(req.url,'http://local').pathname.slice(1)||'index.html';
   if(name==='api/v1/reports'&&req.method==='POST'){req.resume();res.setHeader('Content-Type','application/json');res.end(report);return;}
   if(name==='api/v1/ip-context'&&req.method==='POST'){let body='';req.on('data',b=>body+=b);req.on('end',()=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(controlledContext(JSON.parse(body).address)));});return;}
   if(!assets.includes(name)){res.writeHead(404);res.end();return;}
   res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(path.join(config.frontend,name)));
  });
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
  config.api=config.ui=`http://127.0.0.1:${server.address().port}`;
 }
 for(const name of assets){const bytes=fs.readFileSync(path.join(config.frontend,name));assert.deepEqual(Buffer.from(await(await fetch(config.ui+'/'+name)).arrayBuffer()),bytes);source[name]=hash(bytes);}
 browser=await chromium.launch({executablePath:executable});
 for(const [width,height] of [[375,812],[667,375],[760,600],[1440,900]]){
  const page=currentPage=await browser.newPage({viewport:{width,height},hasTouch:true});page.setDefaultTimeout(10000);
  const errors=[],posts=[],external=[];page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{const u=new URL(r.url());if(![config.ui,config.api].includes(u.origin))external.push(r.url());if(r.method()==='POST')posts.push({path:u.pathname,body:JSON.parse(r.postData())});});
  await page.addInitScript(()=>{
   window.__contextCensus={peak:0,chunks:[]};
   for(const method of ['append','replaceChildren']){const original=Element.prototype[method];Element.prototype[method]=function(...items){
    const cost=items.reduce((n,x)=>n+(x.nodeType===1?1:0)+(x.querySelectorAll?.('*').length||0),0),result=original.apply(this,items);
    if(this.isConnected){__contextCensus.peak=Math.max(__contextCensus.peak,document.querySelectorAll('*').length);if(items.some(x=>x.matches?.('[data-ip-context-panel]'))) __contextCensus.chunks.push(cost);}
    return result;
   };}
  });
  await page.goto(config.ui+'/#topology');await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(config.api);await page.locator('#connection-settings summary').click();
  await page.locator('#topology-targets').fill('8.8.8.8\n9.9.9.9\n208.67.222.222');await page.locator('#topology-attempts').fill('2');
  const received=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/v1/reports');await page.locator('#run-topology').click();const reportResponse=await received;assert.equal(reportResponse.status(),200);const report=await reportResponse.json();
  const ready=()=>page.waitForFunction(()=>{const geo=!document.querySelector('#geo-map-view').hidden;const r=document.querySelector(geo?'#geo-map-result':'#topology-result');return r.getAttribute('aria-busy')==='false'&&!!r.querySelector('canvas');});
  await ready();assert.equal(posts.length,1);
  const query=()=>page.getByRole('button',{name:'IP 추가 정보 조회',exact:true});
  assert.equal(await query().count(),1);assert.equal(await page.locator('[data-ip-context-select]').evaluate(e=>e.tagName),'SELECT');
  await page.locator('[data-ip-context-select]').selectOption('1.1.1.1');assert.equal(posts.length,1);
  const contextReceived=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/ip-context');await query().focus();await page.keyboard.press('Enter');const wire=await(await contextReceived).json();
  await page.waitForFunction(()=>document.querySelector('[data-ip-context-panel]')?.dataset.state==='ready');
  assert.equal(posts.filter(p=>p.path==='/api/v1/ip-context').length,1);assert.deepEqual(posts[1].body,{address:'1.1.1.1'});
  const panel=page.locator('[data-ip-context-panel]'),facts=page.locator('[data-ip-context-facts]');
  for(const text of ['PTR','RDAP','BGP','RPKI','RIS 8시간','신원','운영자','보안','system_resolver','ripe_ris','ripe_rpki',wire.fetched_at])assert.ok((await panel.textContent()).includes(text),text);
  assert.equal(await panel.locator('a,img,script').count(),0);
  await query().scrollIntoViewIfNeeded();await page.screenshot({path:path.join(config.output,`topology-controls-${width}.png`)});
  const geometry=await panel.evaluate(p=>{const b=p.getBoundingClientRect(),a=p.querySelector('[data-ip-context-address]').getBoundingClientRect(),q=p.querySelector('[data-ip-context-query]').getBoundingClientRect();return {position:getComputedStyle(p).position,overflow:document.documentElement.scrollWidth>innerWidth,panelWidth:b.width,identityVisible:a.top>=0&&a.bottom<=innerHeight,actionVisible:q.top>=0&&q.bottom<=innerHeight,touchHeight:q.height};});
  assert.equal(geometry.overflow,false,'new context must reflow without horizontal overflow');assert.equal(geometry.identityVisible,true,'full selected IP co-visible with action');assert.ok(geometry.touchHeight>=40);
  await facts.focus();await page.keyboard.press('End');
  await page.waitForFunction(()=>{const e=document.querySelector('[data-ip-context-facts]');return e.scrollHeight<=e.clientHeight+1||e.scrollTop>0;});
  await page.screenshot({path:path.join(config.output,`topology-facts-${width}.png`)});
  const scroll=await facts.evaluate(e=>({top:e.scrollTop,max:e.scrollHeight-e.clientHeight,height:e.clientHeight}));assert.ok(scroll.height<=Math.max(160,height*0.5),'supplemental facts need their own bounded scroll region');assert.ok(scroll.max<=1||scroll.top>0,'facts reachable by native keyboard scrolling');
  await page.locator('[name="topology-view-mode"][value="3d"]').check();await ready();assert.equal(await query().count(),1);
  await query().click();assert.equal(posts.filter(p=>p.path==='/api/v1/ip-context').length,1,'fresh app cache adds zero network');assert.match(await panel.textContent(),/앱 메모리 캐시/);
  await page.locator('#topology-fullscreen').click();await page.waitForFunction(()=>!!document.fullscreenElement);
  await page.screenshot({path:path.join(config.output,`topology-fullscreen-${width}.png`)});
  assert.equal(await query().evaluate(e=>document.fullscreenElement.contains(e)),true,'fullscreen includes explicit context action');
  await page.evaluate(()=>document.exitFullscreen());
  await page.locator('[data-view-link="geo-map"]').click();await ready();await page.waitForFunction(()=>document.querySelector('.topology-geo-canvas')?.dataset.drawState==='rendered');
  assert.equal(await query().count(),1);assert.equal(posts.length,2);
  const node=report.compact_topology.nodes.find(n=>n.address===(parentMode?'1.1.1.1':'8.8.8.8'));
  await page.locator(`.topology-geo-list button[data-node-id="${node.id}"]`).click();
  assert.ok((await page.locator('#geo-node-detail').textContent()).includes(node.address));assert.equal(posts.length,2);
  const noOverlay=await panel.evaluate(p=>{const a=p.getBoundingClientRect(),b=document.querySelector('.topology-geo-canvas').getBoundingClientRect();return {position:getComputedStyle(p).position,separate:!p.closest('.geo-node-panel'),outside:a.top>=b.bottom||a.left>=b.right||a.right<=b.left};});
  assert.equal(noOverlay.separate,true);assert.equal(noOverlay.outside,true,'new region never overlays markers');assert.ok(!['fixed','sticky','absolute'].includes(noOverlay.position));
  await page.locator('[data-ip-context-select]').selectOption('9.9.9.9');await ready();assert.match(await page.locator('#geo-node-detail').textContent(),/9.9.9.9/);
  const second=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/ip-context');await query().tap();await second;await page.waitForFunction(()=>document.querySelector('[data-ip-context-panel]').dataset.state==='ready');
  if(parentMode){assert.match(await panel.textContent(),/요청 제한/);assert.match(await panel.textContent(),/적용 가능한 ROA 없음/);assert.match(await page.locator('#geo-node-detail').textContent(),/좌표 미확인/);}
  await query().scrollIntoViewIfNeeded();await page.screenshot({path:path.join(config.output,`geo-controls-${width}.png`)});
  await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>!!document.fullscreenElement);assert.equal(await query().evaluate(e=>document.fullscreenElement.contains(e)),true);await query().scrollIntoViewIfNeeded();await page.screenshot({path:path.join(config.output,`geo-fullscreen-${width}.png`)});await page.evaluate(()=>document.exitFullscreen());
  const before=posts.length,catalog=page.locator('#geo-target-filter [data-target-index]'),count=await catalog.count();
  await page.locator('#geo-target-filter [data-filter-action="none"]').click();await ready();assert.equal(await catalog.count(),count);assert.equal(await query().isDisabled(),true);
  await page.locator('#geo-target-filter [data-filter-action="all"]').click();await ready();assert.equal(await catalog.count(),count);assert.equal(posts.length,before);
  const ax=await page.context().newCDPSession(page),tree=await ax.send('Accessibility.getFullAXTree');assert.ok(tree.nodes.some(n=>n.role?.value==='button'&&n.name?.value==='IP 추가 정보 조회'));await ax.detach();
  const census=await page.evaluate(()=>__contextCensus);assert.ok(census.peak<=1200);assert.ok(census.chunks.every(n=>n<=100));assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
  runs.push({width,height,geometry,noOverlay,census,posts,accessibilityTreeAction:true});save('progress.json',{source,runs});await page.close();currentPage=null;
 }
 save('result.json',{verdict:'PASS',provenance:parentMode?'real Go API/CORS/verified local TLS providers; no API interception':'controlled fixture replay via owned HTTP; not live Go API',browser:browser.version(),executable,executableSHA256:hash(fs.readFileSync(executable)),source,runs});
 console.log(JSON.stringify({verdict:'PASS',viewports:runs.length,parentMode}));
} catch(error){if(currentPage)await currentPage.screenshot({path:path.join(config.output,'failure.png')}).catch(()=>{});save('failure.json',{message:error.message,stack:error.stack,source,runs});throw error;
} finally{await browser?.close();if(server){server.closeAllConnections();await new Promise(r=>server.close(r));}save('cleanup.json',{browserConnected:browser?.isConnected()??false,serverListening:server?.listening??false});}})().catch(error=>{console.error(error);process.exitCode=1;});
