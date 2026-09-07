// PLAYWRIGHT_PATH=/path/to/playwright node frontend/geo-map.browser.cjs URL OUTPUT [SOURCE_DIR]
// GEO_LIVE=1 uses the real API8090. Otherwise GEO_REPORT is explicitly captured replay.
const {chromium}=require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs=require('node:fs'), path=require('node:path'), assert=require('node:assert/strict');
(async()=>{
 const [base,out,source]=process.argv.slice(2); fs.mkdirSync(out,{recursive:true,mode:0o700});
 if(!source) for(const name of ['app.js','state.js','topology-model.js','geo-map.js','topology-presentation.js','topology-renderer.js','topology-visualizer.js','styles.css','index.html']) assert.equal(await(await fetch(`${base}/${name}`)).text(),fs.readFileSync(path.join(__dirname,name),'utf8'),`exact source ${name}`);
 const browser=await chromium.launch(); const results=[];
 try { for(const width of [1440,375]) {
 const page=await browser.newPage({viewport:{width,height:1000}}); const errors=[];const requests=[];
 page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>requests.push(r.url()));
 // Private source origin is not in the live API CORS allowlist. Forward the
 // unchanged browser POST to the actual API; do not alter the live deployment.
 if(process.env.GEO_LIVE) await page.route('http://localhost:8090/api/v1/reports',async r=>{const response=await r.fetch({timeout:90000});assert.equal(response.status(),200);await r.fulfill({response});});
 if(source) await page.route(`${base}/**`,async route=>{const name=new URL(route.request().url()).pathname.slice(1)||'index.html'; if(!/^[a-z-]+\.(js|css|html)$/.test(name)) return route.continue();const file=path.join(source,name);if(fs.existsSync(file)) return route.fulfill({path:file,contentType:name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html'});return route.continue();});
 if(!process.env.GEO_LIVE) await page.route('**/api/v1/reports',r=>r.fulfill({contentType:'application/json',body:fs.readFileSync(process.env.GEO_EMPTY ? path.join(__dirname,'../testdata/traceroute-command-failure-compact-report.json') : process.env.GEO_REPORT || '/tmp/geo-live-report.json','utf8')}));
 let body;page.on('response',async r=>{if(r.url().endsWith('/api/v1/reports')) body=await r.json();});
 await page.goto(`${base}/#topology`);await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill('http://localhost:8090');await page.locator('#connection-settings summary').click();await page.locator('#topology-targets').fill('1.1.1.1');await page.locator('#topology-attempts').fill('1');await page.locator('#run-topology').click();
 await page.waitForFunction(()=>document.querySelector('#topology-workspace').dataset.state==='ready',{},{timeout:60000});
 await page.locator('[href="#geo-map"]').click(); const canvas=page.locator('.topology-geo-canvas');await canvas.waitFor();await canvas.scrollIntoViewIfNeeded();await page.waitForTimeout(300);
 const probe=()=>canvas.evaluate(c=>{const ctx=c.getContext('2d'),p=ctx.getImageData(0,0,c.width,c.height).data;let land=0,ocean=0,marker=0,route=0;for(let i=0;i<p.length;i+=4){if(p[i]===46&&p[i+1]===72&&p[i+2]===72)land++;if(p[i]===11&&p[i+1]===28&&p[i+2]===42)ocean++;if(p[i]===201&&p[i+1]===255&&p[i+2]===70)marker++;if(p[i]===103&&p[i+1]===213&&p[i+2]===255)route++;}return{land,ocean,marker,route,data:{...c.dataset},dom:document.querySelectorAll('*').length,overflow:document.documentElement.scrollWidth>innerWidth};});
 const before=await probe(); await canvas.screenshot({path:path.join(out,`${width}-map.png`)});
 const trace=await page.evaluate(async body=>{
   const {normalizeReport}=await import('./state.js');const {topologyModelFromReport,planTopologyDOM}=await import('./topology-model.js');
   const snapshot=JSON.stringify(body),normalized=normalizeReport(body),model=topologyModelFromReport(normalized),plan=planTopologyDOM(model,{view:'geo'});
   return {rawResults:body.results.length,rawCompactNodes:body.compact_topology?.nodes.length??null,normalizedResults:normalized.results.length,modelNodes:model.nodes.length,publicCoordinateNodes:model.nodes.filter(n=>n.public_ip&&n.geolocation).length,plannedMarkers:plan.geo.markers.length,plannedSegments:plan.geo.segments.length,rawUnchanged:snapshot===JSON.stringify(body)};
 },body);
 assert.ok(trace.rawUnchanged);assert.equal(Number(before.data.markers),trace.plannedMarkers);assert.equal(Number(before.data.segments),trace.plannedSegments);
 if(process.env.GEO_EMPTY){assert.equal(before.data.markers,'0');assert.equal(before.data.segments,'0');assert.equal(before.marker,0);assert.equal(before.route,0);assert.match(await page.locator('#geo-map-message').textContent(),/공인 IP 홉이 없습니다/);}
 // Only aggregate trace is persisted: no report, target, request URL or coordinates.
 const safeData={...before.data};delete safeData.center;
 results.push({width,source:process.env.GEO_LIVE?'actual API8090 (unchanged POST via Playwright CORS bridge)':process.env.GEO_EMPTY?'permanent no-Geo fixture':'captured replay',...before,data:safeData,trace,errors,thirdPartyRequests:requests.filter(u=>!u.startsWith(base)&&!u.startsWith('http://localhost:8090')).length});fs.writeFileSync(path.join(out,'results.json'),JSON.stringify(results,null,2));
 assert.ok(before.land>1000 && before.ocean>1000,'recognizable offline geographic land/ocean pixels, not a blank coordinate canvas');assert.ok(before.dom<=1200&&!before.overflow);assert.deepEqual(errors,[]);
 if(Number(before.data.markers))assert.ok(before.marker>10,'coordinate markers painted');if(Number(before.data.segments))assert.ok(before.route>10,'coordinate route strokes painted');
 await page.locator('#geo-zoom-in').click();await page.waitForTimeout(80);assert.notEqual((await probe()).data.scale,before.data.scale);await page.locator('#geo-fit').click();await page.waitForTimeout(80);assert.equal((await probe()).data.scale,before.data.scale);
 await canvas.focus();await page.keyboard.press('ArrowRight');await page.waitForTimeout(80);assert.notEqual((await probe()).data.center,before.data.center);await page.keyboard.press('Home');await page.waitForTimeout(80);assert.equal((await probe()).data.center,before.data.center);
 const box=await canvas.boundingBox();await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width/2+25,box.y+box.height/2+10);await page.mouse.up();await page.waitForTimeout(80);assert.notEqual((await probe()).data.center,before.data.center);await page.locator('#geo-fit').click();
 for(let i=0;i<3;i++){await page.locator('[href="#topology"]').first().click();assert.equal(await page.locator('.topology-geo-canvas').count(),0);await page.locator('[href="#geo-map"]').click();await canvas.waitFor();await page.waitForTimeout(80);assert.equal((await probe()).dom,before.dom);}
 assert.deepEqual(errors,[]);assert.equal(requests.filter(u=>!u.startsWith(base)&&!u.startsWith('http://localhost:8090')).length,0);
 await page.locator('[href="#topology"]').first().click();assert.equal(await page.locator('.topology-geo-canvas').count(),0); await page.close();
 }}finally{await browser.close();console.log(JSON.stringify(results,null,2));}
})().catch(e=>{console.error(e);process.exitCode=1;});
