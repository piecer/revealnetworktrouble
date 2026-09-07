// Renderer-only synthetic seam coordinates: never submitted as observations or API data.
// PLAYWRIGHT_PATH=/tmp/canvasprobe/node_modules/playwright node frontend/geo-map-adversarial.browser.cjs URL OUTPUT
const {chromium}=require(process.env.PLAYWRIGHT_PATH||'playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
(async()=>{
 const [base,out]=process.argv.slice(2);fs.mkdirSync(out,{recursive:true,mode:0o700});
 assert.equal(await(await fetch(`${base}/geo-map.js`)).text(),fs.readFileSync(path.join(__dirname,'geo-map.js'),'utf8'));
 const browser=await chromium.launch();const results=[];
 try{for(const width of [375,1440])for(const direction of [1,-1]){
  const page=await browser.newPage({viewport:{width,height:600}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base);
  const result=await page.evaluate(async({width,direction})=>{
   const {mountGeoMap}=await import('./geo-map.js');
   document.body.replaceChildren();const canvas=document.createElement('canvas');canvas.style.cssText=`width:${width}px;height:360px`;document.body.append(canvas);
   const abort=new AbortController();let frames=0;const pending=new Set();
   const scheduler={schedule(fn){const id=requestAnimationFrame(()=>{pending.delete(id);frames++;fn();});pending.add(id);return id;},cancel(id){cancelAnimationFrame(id);pending.delete(id);}};
   const markers=[{node_id:'a',longitude:175*direction,latitude:0},{node_id:'b',longitude:-175*direction,latitude:0}];
   const dispose=mountGeoMap({canvas,geo:{markers,segments:[{from:'a',to:'b'}]},signal:abort.signal,win:window,scheduler});
   const pixels=canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data;
   const scale=Number(canvas.dataset.scale),half=canvas.width/2;let route=0,farRoute=0,arrowLeft=0,arrowRight=0;
   for(let y=0;y<canvas.height;y++)for(let x=0;x<canvas.width;x++){
    const i=(y*canvas.width+x)*4;
    if(pixels[i]===103&&pixels[i+1]===213&&pixels[i+2]===255){route++;if(Math.abs(x+.5-half)>5*scale+2||Math.abs(y+.5-180)>3)farRoute++;}
    if(pixels[i]===201&&pixels[i+1]===255&&pixels[i+2]===70&&Math.abs(x+.5-half)<Math.min(7,scale-5)){if(x+.5<half)arrowLeft++;else arrowRight++;}
   }
   const center=canvas.dataset.center;
   canvas.dispatchEvent(new WheelEvent('wheel',{deltaY:100,cancelable:true}));
   for(let i=0;i<50;i++)window.dispatchEvent(new Event('resize'));
   const queued=pending.size;abort.abort();dispose();const atAbort=frames;
   window.dispatchEvent(new Event('resize'));canvas.dispatchEvent(new KeyboardEvent('keydown',{key:'Home'}));
   await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
   return {route,farRoute,arrowLeft,arrowRight,center,queued,pending:pending.size,staleDraws:frames-atAbort};
  },{width,direction});
  assert.ok(result.route>3,'actual short cyan line pixels');assert.equal(result.farRoute,0,'no world-spanning or misplaced cyan stroke');assert.equal(result.center,'-180,0','seam fitted to center');
  assert.ok(direction===1?result.arrowLeft>result.arrowRight:result.arrowRight>result.arrowLeft,'actual triangle pixels point in traversal direction');
  assert.equal(result.queued,1);assert.equal(result.pending,0);assert.equal(result.staleDraws,0);assert.deepEqual(errors,[]);
  await page.locator('canvas').screenshot({path:path.join(out,`${width}-${direction}.png`)});results.push({width,direction,...result});await page.close();
 }}finally{await browser.close();fs.writeFileSync(path.join(out,'results.json'),JSON.stringify(results,null,2));console.log(JSON.stringify(results));}
})().catch(e=>{console.error(e);process.exitCode=1;});
