// Owned ephemeral static server; exact producer fixture and optional captured REPORT replay.
// PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/route-visual.browser.cjs OUTPUT [REPORT_LOG]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http'), crypto = require('node:crypto'), assert = require('node:assert/strict');
const root = path.resolve(__dirname, '..'), out = process.argv[2];
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const reports = [['producer-route-RTT', fs.readFileSync(path.join(root, 'testdata/compact-route-visual-report.json'))]];
if (process.argv[3]) { const line = fs.readFileSync(process.argv[3], 'utf8').split('\n').find(l => l.startsWith('REPORT ')); assert.ok(line); reports.push(['captured-existing-network-replay', Buffer.from(line.slice(7))]); }
fs.mkdirSync(out, { recursive: true });
const server = http.createServer((req, res) => {
 const name = new URL(req.url, 'http://local').pathname, file = path.resolve(__dirname, '.' + (name === '/' ? '/index.html' : name));
 if (!file.startsWith(__dirname + '/') || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404);res.end();return; }
 res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html'); res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const base = `http://127.0.0.1:${server.address().port}`, browser = await chromium.launch(), results = [], assets = {};
 try {
  for (const asset of ['index.html','styles.css','app.js','state.js','topology-model.js','topology-presentation.js','topology-renderer.js','topology-visualizer.js','geo-map.js']) {
   const bytes = Buffer.from(await (await fetch(base + '/' + asset)).arrayBuffer()); assert.deepEqual(bytes, fs.readFileSync(path.join(__dirname, asset))); assets[asset] = hash(bytes);
  }
  for (const [name, body] of reports) for (const width of [375,1440]) {
   const page = await browser.newPage({viewport:{width,height:1000}}); const errors=[]; let requests=0;
   page.on('pageerror',e=>errors.push(e.message));
   await page.addInitScript(() => {
    const proto=CanvasRenderingContext2D.prototype;
    for (const method of ['clearRect','arc','moveTo','quadraticCurveTo','fillText']) {
     const old=proto[method]; proto[method]=function(...args){
      if(method==='clearRect')this.canvas.probe={nodes:[],curves:[],labels:[]};const p=this.canvas.probe;
      if(p&&method==='fillText'&&!['◉','…','?','!','◆'].includes(args[0])) {
       const m=this.measureText(args[0]), scale=Math.min(1,(args[3]??Infinity)/m.width);
       p.labels.push({text:args[0],left:args[1]-m.actualBoundingBoxLeft*scale,right:args[1]+m.actualBoundingBoxRight*scale,top:args[2]-m.actualBoundingBoxAscent,bottom:args[2]+m.actualBoundingBoxDescent});
      }
      if(method==='moveTo')this.probeFrom=args;
      if(p&&method==='arc'&&/^#[0-9a-f]{6}$/i.test(this.fillStyle))p.nodes.push({xy:args.slice(0,2),radius:args[2],color:this.fillStyle});
      if(p&&method==='quadraticCurveTo')p.curves.push({from:this.probeFrom,control:args.slice(0,2),to:args.slice(2),color:this.strokeStyle,dash:this.getLineDash()});
      return old.apply(this,args);
     };
    }
   });
   await page.route('**/api/v1/reports',r=>{requests++;return r.fulfill({status:200,contentType:'application/json',body});});
   await page.goto(base+'/#topology'); await page.locator('#run-topology').click();
   const ready=()=>page.waitForFunction(()=>document.querySelector('canvas.topology-canvas')?.dataset.drawState==='rendered'&&document.querySelector('#topology-result').getAttribute('aria-busy')==='false');
   await ready(); await page.locator('[data-filter-action="all"]').click(); await ready(); const canvas=page.locator('canvas.topology-canvas');
   for(const mode of ['2d','3d']) {
    await page.locator(`[name="topology-view-mode"][value="${mode}"]`).check();await ready();await canvas.scrollIntoViewIfNeeded();
    const actual=await canvas.evaluate(c=>({...c.probe,box:c.getBoundingClientRect().toJSON()}));
    assert.ok(actual.nodes.length>1&&actual.curves.length>0);
    let circleOverlaps=0,glyphOverlaps=0,glyphCircleOverlaps=0;
    actual.nodes.forEach((a,i)=>actual.nodes.slice(i+1).forEach(b=>{if(Math.hypot(a.xy[0]-b.xy[0],a.xy[1]-b.xy[1])<a.radius+b.radius)circleOverlaps++;}));
    actual.labels.forEach((a,i)=>{
     actual.labels.slice(i+1).forEach(b=>{if(a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top)glyphOverlaps++;});
     actual.nodes.forEach(n=>{if(Math.hypot(n.xy[0]-Math.max(a.left,Math.min(a.right,n.xy[0])),n.xy[1]-Math.max(a.top,Math.min(a.bottom,n.xy[1])))<n.radius+2)glyphCircleOverlaps++;});
    });
    const density={name,width,mode,ordinaryLabels:actual.labels.length,circleOverlaps,glyphOverlaps,glyphCircleOverlaps};
    fs.appendFileSync(path.join(out,'density.jsonl'),JSON.stringify(density)+'\n');
    assert.equal(glyphOverlaps,0,JSON.stringify(density));assert.equal(glyphCircleOverlaps,0,JSON.stringify(density));
    if(name==='captured-existing-network-replay') {
     assert.equal(hash(body),'bf730a41e105628abb96e1aef06563e3566f69e202c481dd2adc863be93f95bd','baseline thresholds require the exact reviewed capture');
     assert.equal(actual.nodes.length,129);
     // Frozen independent baseline 2aff022, same captured bytes/default/all targets.
     const baseline=width===375?{labels:11,overlaps:mode==='2d'?379:358}:{labels:mode==='2d'?86:75,overlaps:mode==='2d'?0:1};
     assert.ok(actual.labels.length>=baseline.labels,JSON.stringify(density));
     assert.ok(circleOverlaps<=baseline.overlaps,JSON.stringify(density));
    }
    if(name==='producer-route-RTT') {
     assert.ok(actual.nodes.every(n=>n.radius>=10),'small fixture keeps large circles');
     assert.equal(circleOverlaps,0);assert.ok(actual.labels.length>=(width===375?10:11));
     assert.deepEqual([...new Set(actual.curves.filter(c=>!c.dash.length).map(c=>c.color))].sort(),['#c9ff46','#67d5ff','#ff72a5'].sort());
     assert.deepEqual([...new Set(actual.nodes.map(n=>n.color))].sort(),['#94a3b8','#52e0b1','#c9ff46','#ffb84d','#ff7185'].sort());
     assert.ok(actual.curves.some((c,i,a)=>a.some((d,j)=>i!==j&&c.from.join()===d.from.join()&&c.to.join()===d.to.join()&&c.color!==d.color)), 'shared edges visibly contain distinct membership lanes');
     assert.match(await page.locator('.topology-link').first().getAttribute('title'), /공유 경로 3/);
     const swatches=await page.locator('.target-toggles label').evaluateAll(ns=>ns.map(n=>n.style.getPropertyValue('--route-color')));
     assert.deepEqual(swatches,['#c9ff46','#67d5ff','#ff72a5']);
    }
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    const count=await page.locator('*').count();assert.ok(count<=1200);assert.equal(requests,1);
    await page.screenshot({path:path.join(out,`${name}-${width}-${mode}.png`)});
    await canvas.screenshot({path:path.join(out,`${name}-${width}-${mode}-canvas.png`)});
    results.push({name,width,mode,count,requests,nodes:actual.nodes.length,curves:actual.curves.length,routeColors:[...new Set(actual.curves.filter(c=>!c.dash.length).map(c=>c.color))],nodeColors:[...new Set(actual.nodes.map(n=>n.color))],bodySHA256:hash(body),errors});
   }
   if(name==='producer-route-RTT') {
    await page.locator('label:has([data-target-index="0"])').click();await page.locator('label:has([data-target-index="2"])').click();await ready();
    const filtered=await canvas.evaluate(c=>c.probe.curves.filter(l=>!l.dash.length).map(l=>l.color));assert.ok(filtered.length&&filtered.every(c=>c==='#67d5ff'));
    await page.locator('#topology-color-legend').screenshot({path:path.join(out,`legend-${width}.png`)});
   }
   assert.deepEqual(errors,[]);await page.close();
  }
  // Existing executable interaction regression: drag, attached edges, annotations,
  // keyboard edit, persistence, stale cleanup, and 1200 DOM bound on both replays.
  const {spawn}=require('node:child_process');
  const interaction=await new Promise((resolve,reject)=>{
   const child=spawn(process.execPath,[path.join(__dirname,'rich-topology.browser.cjs'),base,path.join(out,'interaction'),...(process.argv[3]?[process.argv[3]]:[])],{env:process.env});
   let stdout='',stderr='';child.stdout.on('data',b=>stdout+=b);child.stderr.on('data',b=>stderr+=b);
   child.on('error',reject);child.on('close',status=>resolve({status,stdout,stderr}));
  });
  fs.writeFileSync(path.join(out,'interaction.log'),interaction.stdout+interaction.stderr);assert.equal(interaction.status,0,interaction.stderr);
 } finally {await browser.close();await new Promise(resolve=>server.close(resolve));fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({assets,results},null,2));}
 console.log(JSON.stringify({cases:results.length,results},null,2));
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
