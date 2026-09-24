import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cp, mkdtemp, readFile, readdir, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { createApp } from './app.js';

const root = fileURLToPath(new URL('../', import.meta.url));
const assets = ['app.js', 'geo-map.js', 'index.html', 'state.js', 'styles.css', 'topology-model.js', 'topology-presentation.js', 'topology-renderer.js', 'topology-visualizer.js'];
const dockerfile = await readFile(new URL('./Dockerfile', import.meta.url), 'utf8');
const instructions = dockerfile.replace(/\\\r?\n/g, '').split(/\r?\n/);

function resolveCompose(port) {
  // Never discover a caller .env or inherit credentials/Compose overlays.
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(CHECKNETWORK_|COMPOSE_|SOURCE_DATE_EPOCH$)/.test(key)));
  if (port !== undefined) env.CHECKNETWORK_API_PORT = port;
  const result = spawnSync('docker', ['compose', '--env-file', '/dev/null', '-f', join(root, 'compose.yaml'), 'config', '--format', 'json'], { env, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout).services;
}

async function materialize(args = {}, prefix = 'checknetwork-deployment-') {
  const dir = await mkdtemp(join(tmpdir(), prefix));
  try {
    for (const name of assets) await cp(new URL(`./${name}`, import.meta.url), join(dir, name));
    await cp(new URL('./nginx.conf', import.meta.url), join(dir, 'nginx.conf'));
    const defaults = Object.fromEntries(instructions.filter(line => line.startsWith('ARG ') && line.includes('=')).map(line => {
      const at = line.indexOf('='); return [line.slice(4, at), line.slice(at + 1)];
    }));
    const env = { PATH: process.env.PATH, SOURCE_DATE_EPOCH: '0', ...defaults, ...args };
    const copied = instructions.findIndex(line => line.startsWith('COPY index.html '));
    assert.ok(copied >= 0, 'execute the actual post-COPY Dockerfile recipe');
    const commands = instructions.slice(copied + 1).filter(line => line.startsWith('RUN ')).map(line => line.slice(4)
      .replaceAll('/usr/share/nginx/html', '.').replaceAll('/etc/nginx/nginx.conf', './nginx.conf'));
    const result = spawnSync('sh', ['-eu', '-c', commands.join('\n')], { cwd: dir, env, encoding: 'utf8' });
    return { dir, result, html: await readFile(join(dir, 'index.html'), 'utf8') };
  } catch (error) {
    await rm(dir, { recursive: true, force: true });
    throw error;
  }
}

async function assertClientRequest(html, base) {
  const dom = new JSDOM(html, { url: 'http://localhost:3000/#diagnostics' });
  const calls = [];
  try {
    assert.equal(dom.window.document.querySelector('#api-base-url').value, base);
    const app = createApp({ document: dom.window.document, window: dom.window,
      fetchImpl: async (url, init) => {
        calls.push({ url, method: init.method });
        return { ok: false, status: 503, headers: { get: () => null }, text: async () => '{"error":"test transport"}' };
      } });
    await app.start('diagnostics');
    assert.equal(calls.length, 1, 'real app must reach its request path');
    assert.equal(calls[0].url, `${base}/api/v1/reports`);
    assert.equal(calls[0].method, 'POST');
  } finally {
    dom.window.close();
  }
}

test('API_PORT rejects malformed or injectable values before changing HTML', async () => {
  for (const port of ['', '0', '00', '08090', '65536', '99999999999999999999', '-1', '+8090', '1.5', ' 8090', '8090 ', '8090\n', '8090\n80', '８０９０', '80/evil', '80&evil', '80|evil', '80" autofocus="true', '$(touch injected)', '${PORT}', '80;exit 0']) {
    const output = await materialize({ API_PORT: port });
    try {
      assert.notEqual(output.result.status, 0, `must reject ${JSON.stringify(port)}`);
      assert.match(output.result.stderr, /API_PORT must be an integer from 1 to 65535/);
      assert.equal(output.html, await readFile(new URL('./index.html', import.meta.url), 'utf8'), 'invalid input must not rewrite HTML');
      assert.equal((await readdir(output.dir)).includes('.checknetwork-assets.sha256'), false, 'invalid build must stop before emitting the manifest');
    } finally {
      await rm(output.dir, { recursive: true, force: true });
    }
  }
});

test('canonical release materialization preserves all nine source assets and deterministic manifest', async () => {
  const outputs = [];
  try {
    for (let run = 0; run < 2; run++) {
      const output = await materialize(); outputs.push(output);
      assert.equal(output.result.status, 0, output.result.stderr);
      await assertClientRequest(output.html, 'http://localhost:9090');
      assert.deepEqual((await readdir(output.dir)).filter(name => name !== 'nginx.conf').sort(), ['.checknetwork-assets.sha256', ...assets].sort());
      const manifest = await readFile(join(output.dir, '.checknetwork-assets.sha256'), 'utf8');
      assert.deepEqual(manifest.trim().split('\n').map(line => line.split(/\s+/)[1]), assets);
      const checked = spawnSync('sha256sum', ['-c', '.checknetwork-assets.sha256'], { cwd: output.dir, encoding: 'utf8' });
      assert.equal(checked.status, 0, checked.stdout + checked.stderr);
      for (const asset of assets) {
        assert.deepEqual(await readFile(join(output.dir, asset)), await readFile(new URL(`./${asset}`, import.meta.url)));
        assert.equal((await stat(join(output.dir, asset))).mtimeMs, 0);
      }
      assert.equal((await stat(join(output.dir, '.checknetwork-assets.sha256'))).mtimeMs, 0);
    }
    assert.deepEqual(await readFile(join(outputs[0].dir, '.checknetwork-assets.sha256')), await readFile(join(outputs[1].dir, '.checknetwork-assets.sha256')));
  } finally {
    for (const output of outputs) await rm(output.dir, { recursive: true, force: true });
  }
});

test('valid port boundaries and explicit canonical port reach the actual request path', async () => {
  for (const port of ['1', '65535', '9090']) {
    const output = await materialize({ API_PORT: port });
    try {
      assert.equal(output.result.status, 0, output.result.stderr);
      await assertClientRequest(output.html, `http://localhost:${port}`);
    } finally {
      await rm(output.dir, { recursive: true, force: true });
    }
  }
});

test('materialization preserves paths containing spaces and shell quotes', async () => {
  const output = await materialize({ API_PORT: '18090' }, "checknetwork deployment 'quoted-");
  try {
    assert.equal(output.result.status, 0, output.result.stderr);
    await assertClientRequest(output.html, 'http://localhost:18090');
  } finally {
    await rm(output.dir, { recursive: true, force: true });
  }
});

test('stable nginx URLs ignore both validators and forbid storage', async () => {
  const config = await readFile(new URL('./nginx.conf', import.meta.url), 'utf8');
  assert.match(config, /\betag\s+off\s*;/);
  assert.match(config, /\bif_modified_since\s+off\s*;/);
  assert.match(config, /\badd_header\s+Cache-Control\s+"no-store"\s+always\s*;/);
});

const composeAvailable = spawnSync('docker', ['compose', 'version'], { encoding: 'utf8', timeout: 10000 }).status === 0;
test('Compose default and override flow through the actual HTML recipe to the app request', {
  skip: !composeAvailable && process.env.CHECKNETWORK_RUN_COMPOSE_TESTS !== '1'
}, async () => {
  for (const [override, expected] of [[undefined, '8090'], ['', '8090'], ['18090', '18090']]) {
    const services = resolveCompose(override);
    const published = services.api.ports.find(port => port.target === 8080);
    assert.equal(published.host_ip, '127.0.0.1');
    assert.equal(published.published, expected);
    const output = await materialize(services.web.build.args);
    try {
      assert.equal(output.result.status, 0, output.result.stderr);
      await assertClientRequest(output.html, `http://localhost:${published.published}`);
    } finally {
      await rm(output.dir, { recursive: true, force: true });
    }
  }
});
