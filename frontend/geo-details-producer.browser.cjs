// Exact-source browser acceptance: frozen Go provider/serializer fixture REPLAY,
// not a live provider request. One additional in-memory stress case is synthetic.
// PLAYWRIGHT_PATH=/path/to/playwright-core node frontend/geo-details-producer.browser.cjs OUTPUT
const {chromium} = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
const http = require('node:http'), crypto = require('node:crypto');
const {pathToFileURL} = require('node:url');
const output = process.argv[2];
if (!output) throw Error('an owned evidence directory is required');
fs.mkdirSync(output, {recursive:true, mode:0o700});
const assets = ['index.html','styles.css','app.js','state.js','topology-model.js','topology-presentation.js','topology-visualizer.js','topology-renderer.js','geo-map.js'];
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const fixtureNames = ['rich-full','rich-compact','empty-full','empty-compact','truncated-compact'];
const fixtures = Object.fromEntries(fixtureNames.map(name => [name, fs.readFileSync(path.join(__dirname,`../testdata/geo-details-${name}-report.json`),'utf8')]));
let bytes = fixtures['rich-compact'];
const requests = [], results = [], source = {}, fixtureHashes = {};
const server = http.createServer((req,res) => {
  const url = new URL(req.url,'http://local');
  if (url.pathname === '/api/v1/reports' && req.method === 'POST') {
    requests.push({url:req.url, method:req.method, responseSHA256:hash(bytes)});
    res.writeHead(200,{'Content-Type':'application/json'});res.end(bytes);return;
  }
  const name = url.pathname.slice(1) || 'index.html';
  if (!assets.includes(name)) {res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html');
  res.end(fs.readFileSync(path.join(__dirname,name)));
});
(async () => {
  let browser, base;
  try {
    const {parseResponse} = await import(pathToFileURL(path.join(__dirname,'state.js')));
    const {topologyModelFromReport,planTopologyDOM} = await import(pathToFileURL(path.join(__dirname,'topology-model.js')));
    for (const [name,body] of Object.entries(fixtures)) {
      assert.equal(parseResponse({ok:true,status:200},body).ok,true,name);fixtureHashes[name]=hash(body);
    }
    await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
    base=`http://127.0.0.1:${server.address().port}`;
    for (const name of assets) {
      const local=fs.readFileSync(path.join(__dirname,name));
      assert.deepEqual(Buffer.from(await (await fetch(`${base}/${name}`)).arrayBuffer()),local);
      source[name]=hash(local);
    }
    browser=await chromium.launch();
    for (const width of [1440,375]) for (const name of [...fixtureNames,'legacy-no-sidecar','synthetic-long-text']) {
      if (process.env.GEO_CASE && process.env.GEO_CASE !== `${width}/${name}`) continue;
      let raw;
      if (name === 'legacy-no-sidecar') {raw=JSON.parse(fixtures['rich-compact']);delete raw.geo_details;bytes=JSON.stringify(raw);}
      else if (name === 'synthetic-long-text') {
        raw=JSON.parse(fixtures['rich-compact']);
        const e=raw.geo_details.entries.find(e=>e.address==='8.8.8.8');
        for (const key of ['city','region','country','country_code','continent','continent_code','region_code','postal','timezone','isp','network_domain']) delete e[key];
        for (const key of ['city','region','postal','timezone','isp','network_domain']) e[key]=`<img onerror=alert(1)> ${key} `.padEnd(256,'x');
        bytes=JSON.stringify(raw);
      } else {bytes=fixtures[name];raw=JSON.parse(bytes);}
      const normalized=parseResponse({ok:true,status:200},bytes);assert.equal(normalized.ok,true);
      const model=topologyModelFromReport(normalized.report),plan=planTopologyDOM(model,{view:'geo'});
      const page=await browser.newPage({viewport:{width,height:900},hasTouch:true});
      page.setDefaultTimeout(15000);
      const errors=[],external=[],posts=[];
      page.on('pageerror',e=>errors.push(e.message));
      page.on('request',r=>{if(!r.url().startsWith(base))external.push(r.url());if(r.method()==='POST')posts.push(r.url());});
      const alias=name==='synthetic-long-text'?'Edge <img onerror=alert(1)> '.padEnd(256,'긴'):'Provider replay';
      await page.addInitScript(({alias})=>{
        localStorage.setItem('checknetwork.ip-labels.v1',JSON.stringify([{ip:'8.8.8.8',label:alias,note:''}]));
        window.__geoChunks=[];
        const append=Element.prototype.append;
        Element.prototype.append=function(...items){if(this.id==='geo-map-result')window.__geoChunks.push(items.reduce((n,item)=>n+(item.nodeType===1?1:0)+(item.querySelectorAll?.('*').length||0),0));return append.apply(this,items);};
      },{alias});
      await page.goto(base+'/#topology');
      await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(base);await page.locator('#connection-settings summary').click();
      await page.locator('#run-topology').click();
      const ready=()=>page.waitForFunction(()=>{const root=document.querySelector(document.querySelector('#geo-map-view').hidden?'#topology-result':'#geo-map-result');return ['ready','render-empty'].includes(document.querySelector('#topology-workspace').dataset.state)&&root.getAttribute('aria-busy')==='false';});
      await ready();
      await page.locator('[data-view-link="geo-map"]').click();await ready();
      await page.waitForFunction(()=>document.querySelector('.topology-geo-canvas')?.dataset.drawState==='rendered');
      const canvas=page.locator('.topology-geo-canvas'),detail=page.locator('#geo-node-detail');
      assert.equal(+await canvas.getAttribute('data-markers'),plan.geo.markers.length);
      assert.ok(await page.locator('.topology-geo-list button').count()<=100);
      const expected=raw.geo_details?.entries.find(e=>e.address==='8.8.8.8');
      let readableGlyphs=0,unlocated=false;
      if (expected) {
        const node=model.nodes.find(n=>n.address==='8.8.8.8');
        await canvas.scrollIntoViewIfNeeded();await canvas.focus();await page.keyboard.press('Home');
        await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
        const p=await canvas.evaluate((c,n)=>{const r=c.getBoundingClientRect(),[lon,lat]=c.dataset.center.split(',').map(Number),s=+c.dataset.scale,w=c.width/+c.dataset.dpr,h=c.height/+c.dataset.dpr,delta=((n.geolocation.longitude-lon+180)%360+360)%360-180;return{x:r.left+(w/2+delta*s)*r.width/w,y:r.top+(h/2+(lat-n.geolocation.latitude)*s)*r.height/h};},node);
        if(width<=760)await page.touchscreen.tap(p.x,p.y);else await page.mouse.click(p.x,p.y);
        assert.equal(await canvas.getAttribute('data-selected-node-id'),node.id);
        const visibleIdentity=await detail.evaluate(d=>{const r=document.createRange();r.setStart(d.firstChild,0);r.setEnd(d.firstChild,'8.8.8.8'.length);const i=r.getBoundingClientRect(),b=d.getBoundingClientRect();return i.top>=Math.max(0,b.top)&&i.bottom<=Math.min(innerHeight,b.bottom)&&d.scrollTop===0;});
        assert.equal(visibleIdentity,true,'identity visible at real marker selection, before scrolling');
        const text=await detail.textContent();
        for(const value of Object.values(expected))assert.ok(text.includes(value),`${name}: ${value}`);
        assert.ok(text.includes(alias));assert.match(text,/보충.*정보.*좌표.*ASN.*별도/);assert.match(text,/데이터베이스 갱신.*아님/);
        assert.match(text,/위도 0, 경도 0/);assert.ok(text.includes(node.asn.organization));
        assert.equal(await detail.locator('*').count(),0);
        const readAll=async phase=>{
          await detail.focus();await page.keyboard.press('Home');await page.waitForFunction(()=>document.querySelector('#geo-node-detail').scrollTop===0);
          const ranges=[];for(let i=0;i<text.length;i++){if(/\s/.test(text[i]))continue;const count=text.codePointAt(i)>65535?2:1;ranges.push([i,i+count]);i+=count-1;}
          const seen=new Set();
          for(let step=0;step<250;step++) {
            await page.waitForTimeout(100);
            // Chromium's measured Range advance can exceed its wrapped line by
            // 1/64 CSS px (36 terminal x glyphs in the retained 375px diagnostic).
            // Allow that measured subpixel only; keep vertical/full-glyph checks.
            const visible=await detail.evaluate((d,ranges)=>{const b=d.getBoundingClientRect();return ranges.map(([start,end],i)=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return [...r.getClientRects()].every(x=>x.top>=Math.max(0,b.top)&&x.bottom<=Math.min(innerHeight,b.bottom)&&x.left>=b.left&&x.right<=b.right+1/64)?i:-1;}).filter(i=>i>=0);},ranges);
            visible.forEach(i=>seen.add(i));if(seen.size===ranges.length)break;await page.keyboard.press('ArrowDown');
          }
          if(seen.size!==ranges.length) {
            const missing=await detail.evaluate((d,ranges)=>({box:d.getBoundingClientRect().toJSON(),scrollTop:d.scrollTop,scrollHeight:d.scrollHeight,clientHeight:d.clientHeight,clientWidth:d.clientWidth,scrollWidth:d.scrollWidth,missing:ranges.map(([start,end])=>{const r=document.createRange();r.setStart(d.firstChild,start);r.setEnd(d.firstChild,end);return {start,end,text:d.textContent.slice(start,end),rects:[...r.getClientRects()].map(x=>x.toJSON())};})}),ranges.filter((_,i)=>!seen.has(i)));
            fs.writeFileSync(path.join(output,`${width}-${name}-${phase}-missing.json`),JSON.stringify(missing,null,2)+'\n');
            await page.screenshot({path:path.join(output,`${width}-${name}-${phase}-failure.png`)});
          }
          assert.equal(seen.size,ranges.length,`${width}/${name}/${phase}: every unabridged glyph reachable by native keyboard scroll`);
          readableGlyphs=seen.size;
          await page.keyboard.press('End');await page.waitForFunction(()=>{const d=document.querySelector('#geo-node-detail');return d.scrollTop+d.clientHeight>=d.scrollHeight-1;});
          const mapPixels=await canvas.evaluate(c=>{const b=c.getBoundingClientRect(),p=document.querySelector('.geo-node-panel').getBoundingClientRect();return (innerWidth<=760?Math.min(innerHeight,p.top,b.bottom):Math.min(innerHeight,b.bottom))-Math.max(0,b.top);});
          assert.ok(mapPixels>=120,`${width}/${name}/${phase}: usable map remains visible while reading, got ${mapPixels}px`);
          await page.screenshot({path:path.join(output,`${width}-${name}-${phase}.png`)});
          if(width<=760){
            await page.keyboard.press('Home');await page.waitForFunction(()=>document.querySelector('#geo-node-detail').scrollTop===0);
            const session=await page.context().newCDPSession(page),b=await detail.boundingBox(),x=b.x+b.width/2;
            await session.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y:b.y+b.height-8}]});
            for(let i=1;i<=6;i++){await session.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x,y:b.y+b.height-8-(b.height-16)*i/6}]});await page.waitForTimeout(20);}
            await session.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await page.waitForFunction(()=>document.querySelector('#geo-node-detail').scrollTop>0);await session.detach();
          }
        };
        await page.screenshot({path:path.join(output,`${width}-${name}-selection.png`)});
        await readAll('native-scroll');
        await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>document.fullscreenElement?.id==='geo-map-view');
        // Match the accepted Stage1 gate: entering the fullscreen workspace can
        // reset its page scroll to the heading. Bring its real Canvas into view
        // before testing simultaneous map/detail access (not detail scroll).
        await canvas.scrollIntoViewIfNeeded();
        await readAll('fullscreen');
        await page.locator('#geo-map-fullscreen').click();await page.waitForFunction(()=>!document.fullscreenElement);
        await page.locator('[data-view-link="topology"]').click();await ready();
        const graph=page.locator('.topology-canvas'),target=model.nodes.find(n=>n.address==='1.1.1.1');
        await graph.focus();for(let i=0;i<=model.nodes.length;i++){if(await graph.getAttribute('data-selected-node-id')===target.id)break;await page.keyboard.press(']');}
        assert.equal(await graph.getAttribute('data-selected-node-id'),target.id);
        await page.locator('[data-view-link="geo-map"]').click();await ready();
        assert.equal(await canvas.getAttribute('data-selected-node-id'),target.id);
        assert.match(await detail.textContent(),/Text without coordinates/);assert.match(await detail.textContent(),/좌표 미확인/);assert.doesNotMatch(await detail.textContent(),/위도 0/);
        assert.equal(await detail.evaluate(d=>d.scrollTop),0);unlocated=true;
      }
      if(name==='truncated-compact')assert.match(await page.locator('#geo-map-message').textContent(),/보충.*501.*생략.*1/);
      if(name==='legacy-no-sidecar')assert.doesNotMatch(await detail.textContent(),/로컬 조회 완료|캐시 만료/);
      const metrics=await page.evaluate(()=>({elements:document.querySelectorAll('*').length,overflow:document.documentElement.scrollWidth>innerWidth,maxChunk:Math.max(0,...window.__geoChunks)}));
      assert.ok(metrics.elements<=1200);assert.equal(metrics.overflow,false);assert.ok(metrics.maxChunk<=100);
      assert.deepEqual(posts,[base+'/api/v1/reports?geo_details=1']);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
      const ax=await page.context().newCDPSession(page);const tree=await ax.send('Accessibility.getFullAXTree');
      assert.ok(tree.nodes.some(n=>n.role?.value==='region'&&n.name?.value.includes('스크롤')));await ax.detach();
      results.push({width,name,provenance:name.startsWith('synthetic')?'synthetic bounded text stress':name==='legacy-no-sidecar'?'derived old-server compatibility':'frozen producer fixture replay',responseSHA256:hash(bytes),...metrics,markers:plan.geo.markers.length,readableGlyphs,unlocated,posts:posts.length,errors,external});
      fs.writeFileSync(path.join(output,'progress.json'),JSON.stringify(results,null,2)+'\n');await page.close();
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({browser:browser.version(),source,fixtureHashes,requests,results},null,2)+'\n');
    console.log(JSON.stringify({cases:results.length,requests:requests.length,source,results}));
  } finally {
    if(browser)await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));
    fs.writeFileSync(path.join(output,'cleanup.json'),JSON.stringify({pid:process.pid,base,serverListening:server.listening,browserConnected:browser?.isConnected()??false})+'\n');
    console.log('owned browser and loopback replay server closed');
  }
})().catch(error=>{console.error(error);process.exitCode=1;});
