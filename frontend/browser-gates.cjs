// Optional Chromium acceptance. Every HTTP listener and child belongs to this run.
// API fixture replay here is distinct from the real-handler CORS browser gate.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { spawn } = require('node:child_process');
const assert = require('node:assert/strict');
const root = __dirname;
const assets = new Set(['index.html', 'styles.css', 'app.js', 'common-prefix.js', 'state.js', 'topology-model.js', 'topology-renderer.js', 'topology-presentation.js', 'topology-visualizer.js', 'geo-map.js']);
const parent = process.argv[2] || os.tmpdir();
fs.mkdirSync(parent, { recursive: true });
const output = fs.mkdtempSync(path.join(parent, 'checknetwork-browser-'));
const server = http.createServer((req, res) => {
  const name = new URL(req.url, 'http://local').pathname.slice(1) || 'index.html';
  if (!assets.has(name)) { res.writeHead(404); res.end(); return; }
  res.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(path.join(root, name)));
});
async function run(name, args) {
  const log = path.join(output, name + '.log');
  const fd = fs.openSync(log, 'wx');
  const child = spawn(process.execPath, [path.join(root, name + '.browser.cjs'), ...args], { stdio: ['ignore', fd, fd] });
  fs.closeSync(fd);
  let kill, timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    child.kill('SIGTERM');
    kill = setTimeout(() => child.kill('SIGKILL'), 2000);
  }, 120000);
  try {
    const result = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', (code, signal) => resolve({ code, signal })); });
    assert.equal(timedOut, false, `${name} timed out; evidence: ${log}`);
    assert.equal(result.code, 0, `${name} failed (${result.signal || result.code}); evidence: ${log}`);
    console.log(`${name}: PASS; ${log}`);
  } finally { clearTimeout(timer); clearTimeout(kill); }
}
(async () => {
  try {
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    const base = `http://127.0.0.1:${server.address().port}`;
    await run('graph-first', [base, path.join(output, 'graph-first')]);
    await run('route-visual', [path.join(output, 'route-visual')]);
    await run('label-integrity', [path.join(output, 'label-integrity')]);
    await run('topology-empty', [path.join(output, 'topology-empty')]);
    await run('ip-context', [path.join(output, 'ip-context')]);
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    console.log(`Browser evidence: ${output}`);
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
