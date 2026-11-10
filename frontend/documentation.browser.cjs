// Optional native-API acceptance; diagnostics are real and topology/Geo are labelled fixture replay.
// PLAYWRIGHT_PATH=/path/to/playwright node frontend/documentation.browser.cjs WEB_ORIGIN API_ORIGIN OUTPUT_DIR [SCREENSHOT_DIR]
const {chromium}=require(process.env.PLAYWRIGHT_PATH || 'playwright');
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const root=path.resolve(__dirname,'..');
const [base,api,out,screenshotOutput]=process.argv.slice(2);
assert.ok(base&&api&&out,'WEB_ORIGIN API_ORIGIN OUTPUT_DIR are required');
for(const value of [base,api]){
 const u=new URL(value);
 assert.ok(u.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(u.hostname)&&u.origin===value,'only exact owned loopback HTTP origins are accepted');
}
const images=screenshotOutput||path.join(out,'screenshots');
fs.mkdirSync(out,{recursive:true});fs.mkdirSync(images,{recursive:true});
const fixturePath='testdata/geo-details-rich-compact-report.json';
const fixture=fs.readFileSync(path.join(root,fixturePath),'utf8');
const results=[],requests=[],widths=[1440,320,375,400];let passed=false;
async function metric(page,width,view,errors){
 const value=await page.evaluate(()=>({elements:document.querySelectorAll('*').length,width:innerWidth,scrollWidth:document.documentElement.scrollWidth,activeElement:document.activeElement.tagName}));
 results.push({width,view,...value,pageErrors:[...errors]});
 assert.ok(value.elements<=1200,`${width}/${view}: document budget`);
 assert.ok(value.scrollWidth<=width,`${width}/${view}: horizontal overflow ${value.scrollWidth}`);
 assert.deepEqual(errors,[],`${width}/${view}: script errors`);
}
(async()=>{
 for(const name of ['app.js','state.js','index.html','styles.css'])assert.equal(await(await fetch(`${base}/${name}`)).text(),fs.readFileSync(path.join(root,'frontend',name),'utf8'));
 const browser=await chromium.launch();let version;
 try{
  version=browser.version();
  for(const width of widths){
   const page=await browser.newPage({viewport:{width,height:1000}}),errors=[];
   page.on('pageerror',e=>errors.push(e.message));
   page.on('request',r=>requests.push({width,url:r.url()}));
   await page.goto(base);await page.keyboard.press('Tab');
   assert.notEqual(await page.evaluate(()=>document.activeElement.tagName),'BODY','keyboard focus reaches a control');
   await page.locator('#connection-settings summary').click();await page.locator('#api-base-url').fill(api);await page.locator('#connection-settings summary').click();
   while(await page.locator('#targets .target-row').count()>2)await page.locator('#targets .target-row').last().locator('button').click();
   for(const [i,kind,address] of [[0,'dns','example.com'],[1,'https','https://example.com']]){
    const row=page.locator('#targets .target-row').nth(i);await row.locator('select').selectOption(kind);await row.locator('input').first().fill(address);
   }
   await page.locator('#run').click();await page.waitForFunction(()=>document.querySelector('#diagnostics-workspace').dataset.state==='ready',null,{timeout:20000});
   await metric(page,width,'diagnostics-actual-api',errors);
   if(width===1440){await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:path.join(images,'diagnostics.png'),fullPage:true});}
   // Only topology is intercepted. Diagnostics above used the actual Go HTTP boundary.
   await page.route('**/api/v1/reports?geo_details=1',r=>r.fulfill({status:200,contentType:'application/json',headers:{'access-control-allow-origin':base},body:fixture}));
   await page.locator('[data-view-link="topology"]').click();await page.locator('#topology-targets').fill('2606:4700:4700::1111');await page.locator('#topology-attempts').fill('2');await page.locator('#run-topology').click();
   await page.waitForFunction(()=>document.querySelector('#topology-workspace').dataset.state==='ready'&&document.querySelector('canvas.topology-canvas')?.dataset.drawState==='rendered');
   for(const mode of ['2d','3d']){
    await page.locator(`[name="topology-view-mode"][value="${mode}"]`).check();await page.locator('#topology-view-reset').click();await metric(page,width,`topology-${mode}-fixture`,errors);
    if(width===1440&&mode==='2d'){await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:path.join(images,'topology.png'),fullPage:true});}
   }
   await page.locator('[href="#geo-map"]').click();await page.waitForFunction(()=>Number(document.querySelector('.topology-geo-canvas')?.dataset.markers)>0);
   await metric(page,width,'geo-map-fixture',errors);
   if(width===1440){await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:path.join(images,'geo-map.png'),fullPage:true});}
   await page.locator('[href="#ip-labels"]').click();await metric(page,width,'ip-labels',errors);
   await page.locator('[href="#common-prefix"]').click();await page.locator('#common-prefix-input').fill('192.0.2.1\n192.0.2.129\n2001:db8::1\n2001:db8::2');await page.locator('#run-common-prefix').click();
   await page.locator('#common-prefix-report-section').waitFor({state:'visible'});
   const text=await page.locator('#common-prefix-result').textContent();assert.ok(text.includes('IPv4')&&text.includes('IPv6'));
   await metric(page,width,'common-prefix-local',errors);
   if(width===1440){await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:path.join(images,'common-prefix.png'),fullPage:true});}
   await page.close();
  }
  assert.equal(requests.filter(r=>![base,api].includes(new URL(r.url).origin)).length,0,'no third-party browser requests');
  assert.equal(results.length,widths.length*6,'every requested viewport/view case must execute');passed=true;
 }finally{
  await browser.close();
  const data={passed,expectedCases:widths.length*6,browser:version,fixture:fixturePath,fixture_sha256:crypto.createHash('sha256').update(fixture).digest('hex'),diagnostics:'actual Go API on owned loopback port; real example.com DNS/HTTPS',topology_and_geo:'explicit fixed producer fixture replay, not live external paths',results,thirdPartyBrowserRequests:requests.filter(r=>![base,api].includes(new URL(r.url).origin)).length};
  fs.writeFileSync(path.join(out,'responsive-ui.json'),JSON.stringify(data,null,2));console.log(JSON.stringify(data,null,2));
 }
})().catch(e=>{console.error(e);process.exitCode=1});
