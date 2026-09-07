// Private exact-source Chromium replay; no producer mutation or live diagnostics.
// PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/unknown-presentation.browser.cjs URL OUTPUT REPORT_LOG
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
(async () => {
  const [base, output, log] = process.argv.slice(2);
  const reports = [['fixture', fs.readFileSync(path.join(__dirname, '../testdata/traceroute-command-failure-compact-report.json'), 'utf8')]];
  if (log) reports.push(['captured', fs.readFileSync(log, 'utf8').split('\n').find(line => line.startsWith('REPORT ')).slice(7)]);
  fs.mkdirSync(output, { recursive: true });
  // Verify every served production module against this exact dirty worktree.
  for (const name of ['app.js','state.js','topology-model.js','topology-presentation.js','topology-renderer.js','topology-visualizer.js','styles.css','index.html']) {
    assert.equal(await (await fetch(`${base}/${name}`)).text(), fs.readFileSync(path.join(__dirname, name), 'utf8'), `exact source ${name}`);
  }
  const browser = await chromium.launch(); const results = [];
  try {
    for (const [name, body] of reports) for (const width of [1440, 375]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      const errors = []; page.on('pageerror', e => errors.push(e.message));
      await page.addInitScript(() => {
        for (const name of ['clearRect','arc','moveTo','quadraticCurveTo']) {
          const original = CanvasRenderingContext2D.prototype[name];
          CanvasRenderingContext2D.prototype[name] = function(...args) {
            if (name === 'clearRect') this.canvas.probe = { nodes: [], curves: [] };
            const p = this.canvas.probe;
            if (p && name === 'arc' && /^#[0-9a-f]{6}$/i.test(this.fillStyle)) p.nodes.push(args.slice(0,2));
            if (name === 'moveTo') this.probeFrom = args;
            if (p && name === 'quadraticCurveTo') p.curves.push({ from: this.probeFrom, to: args.slice(2), control: args.slice(0,2), dash: this.getLineDash() });
            return original.apply(this,args);
          };
        }
      });
      await page.route('**/api/v1/reports', r => r.fulfill({ status: 200, contentType: 'application/json', body }));
      await page.goto(base + '/#topology'); await page.locator('#run-topology').click();
      const ready = () => page.waitForFunction(() => document.querySelector('canvas.topology-canvas')?.dataset.drawState === 'rendered' && document.querySelector('#topology-result')?.getAttribute('aria-busy') === 'false');
      await ready();
      const report = JSON.parse(body), graph = report.compact_topology;
      const ids = new Map(graph.nodes.map(n => [n.id,n]));
      const responsive = [...new Set(graph.routes.flatMap(r => r.node_ids).filter(id => ids.get(id).kind !== 'unknown'))].sort();
      const runs = [], bypasses = [];
      for (const route of graph.routes) for (let i=0; i<route.node_ids.length;) {
        const p = route.node_ids;
        if (ids.get(p[i]).kind !== 'unknown') { i++; continue; }
        const start = i; while (i<p.length && ids.get(p[i]).kind === 'unknown') i++;
        runs.push(i-start);
        if (start > 0 && i < p.length) bypasses.push([p[start-1],p[i],i-start]);
      }
      for (const show of [true,false]) {
        await page.locator('[data-toggle-unresponsive]').setChecked(show); await ready();
        const nodeItems = await page.locator('.topology-node').evaluateAll(ns => ns.map(n => ({id:n.dataset.nodeId, editable:n.tagName==='BUTTON', text:n.textContent})));
        assert.deepEqual(nodeItems.filter(n => !n.id.startsWith('presentation:')).map(n=>n.id).sort(), responsive, 'responsive tail inventory exact');
        const groups = nodeItems.filter(n => n.id.startsWith('presentation:'));
        assert.equal(groups.length, show ? runs.length : 0);
        assert.ok(groups.every(n => !n.editable));
        const connectors = await page.locator('.topology-connector').evaluateAll(ns => ns.map(n => ({from:n.dataset.from,to:n.dataset.to,text:n.textContent})));
        if (!show) assert.deepEqual(connectors.map(n => [n.from,n.to,Number(n.text.match(/무응답 (\d+)홉/)[1])]).sort(), [...bypasses].sort());
        for (const mode of ['2d','3d']) {
          await page.locator(`input[name="topology-view-mode"][value="${mode}"]`).check(); await ready();
          const canvas = page.locator('canvas.topology-canvas'); await canvas.scrollIntoViewIfNeeded();
          const probe = await canvas.evaluate(c => c.probe);
          assert.equal(probe.nodes.length, nodeItems.length);
          assert.equal(probe.curves.filter(c => c.dash.length).length, connectors.length);
          if (show && groups.length) {
            // Use the pure exact-source projection for mode-specific node order.
            const order = await page.evaluate(async ({body,mode}) => {
              const {topologyModelFromReport,filterTopologyModel,planTopologyDOM}=await import('./topology-model.js');
              const {projectTopology}=await import('./topology-visualizer.js');
              const m=topologyModelFromReport(JSON.parse(body)); const plan=planTopologyDOM(filterTopologyModel(m,m.routes.map(r=>r.result_index)));
              return projectTopology(plan.topology,{mode,presentation:plan.presentation}).nodes.map(n=>n.id);
            },{body,mode});
            const index = order.indexOf(groups[0].id), point=probe.nodes[index];
            const box=await canvas.boundingBox();
            await page.mouse.move(box.x+point[0],box.y+point[1]);
            assert.match(await page.locator('#topology-node-tooltip').textContent(),/무응답 \d+홉 접음/);
          }
          assert.ok(await page.evaluate(()=>document.getElementsByTagName('*').length)<=1200);
          assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
          results.push({name,width,mode,show,nodes:probe.nodes.length,connectors:connectors.length,sha256:crypto.createHash('sha256').update(body).digest('hex')});
          await page.screenshot({path:path.join(output,`${name}-${width}-${mode}-${show}.png`),fullPage:true});
        }
      }
      assert.deepEqual(errors,[]); await page.close();
    }
  } finally { await browser.close(); }
  fs.writeFileSync(path.join(output,'results.json'),JSON.stringify(results,null,2));
  console.log(JSON.stringify(results,null,2));
})().catch(e=>{console.error(e);process.exitCode=1;});
