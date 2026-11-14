import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import vm from 'node:vm';

const sourceURL = new URL('./browser-gates.cjs', import.meta.url);
const source = await readFile(sourceURL, 'utf8');
const realRequire = createRequire(sourceURL);

// Execute the complete unchanged wrapper, including its real owned HTTP server
// and final failure handler. Control only children and time, not gate decisions.
async function runWrapper(outcome) {
  const directory = await mkdtemp(join(tmpdir(), 'checknetwork-wrapper-test-'));
  const logs = [], errors = [], children = [], timers = [];
  const process = { argv: ['node', 'browser-gates.cjs', directory], execPath: globalThis.process.execPath, exitCode: undefined };
  function spawn() {
    const child = new EventEmitter();
    child.signals = [];
    child.kill = signal => {
      child.signals.push(signal);
      if (signal === 'SIGTERM' && outcome === 'timeout-graceful') queueMicrotask(() => child.emit('close', 0, null));
      if (signal === 'SIGKILL') queueMicrotask(() => child.emit('close', null, 'SIGKILL'));
      return true;
    };
    children.push(child);
    if (outcome === 'success') queueMicrotask(() => child.emit('close', 0, null));
    if (outcome === 'failure') queueMicrotask(() => child.emit('close', 7, null));
    if (outcome === 'spawn-error') queueMicrotask(() => child.emit('error', new Error('spawn failed')));
    return child;
  }
  try {
    await vm.runInNewContext(source, {
      __dirname: directory, process,
      require: name => name === 'node:child_process' ? { spawn } : realRequire(name),
      console: { log: (...args) => logs.push(args.join(' ')), error: error => errors.push(String(error)) },
      setTimeout: (callback, delay) => {
        const timer = { delay, cleared: false, fired: false }; timers.push(timer);
        if ((delay === 120000 && outcome.startsWith('timeout-')) || (delay === 2000 && outcome === 'timeout-forced')) {
          queueMicrotask(() => { if (!timer.cleared) { timer.fired = true; callback(); } });
        }
        return timer;
      },
      clearTimeout: timer => { if (timer) timer.cleared = true; }
    }, { filename: sourceURL.pathname });
    assert.ok(timers.every(timer => timer.cleared), 'every deadline/escalation timer must be cleared');
    return { logs, errors, children, timers, code: process.exitCode ?? 0 };
  } finally { await rm(directory, { recursive: true, force: true }); }
}

test('IP context gate delegates default executable selection to its matching Playwright package', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'checknetwork-launch-test-'));
  const optionsFile = join(directory, 'launch-options.json');
  const probe = join(directory, 'playwright-probe.cjs');
  await writeFile(probe, `// Stop after recording the real harness launch boundary; no UI verdict is simulated.
const fs = require('node:fs');
module.exports.chromium = { executablePath: () => '/matching-package/browser', launch: async options => {
  fs.writeFileSync(${JSON.stringify(optionsFile)}, JSON.stringify(options ?? {}));
  throw new Error('launch-boundary-probe');
} };
`);
  try {
    for (const override of [undefined, '/explicit-browser-path']) {
      const env = { ...process.env, PLAYWRIGHT_PATH: probe };
      delete env.IP_CONTEXT_CHROMIUM;
      if (override) env.IP_CONTEXT_CHROMIUM = override;
      const output = join(directory, override ? 'override' : 'default');
      const child = spawnSync(process.execPath, [new URL('./ip-context.browser.cjs', import.meta.url).pathname, output], { env, encoding: 'utf8', timeout: 10000 });
      assert.equal(child.status, 1, child.stderr);
      assert.match(JSON.parse(await readFile(join(output, 'failure.json'), 'utf8')).message, /launch-boundary-probe/);
      const options = JSON.parse(await readFile(optionsFile, 'utf8'));
      assert.deepEqual(options, { executablePath: override ?? '/matching-package/browser' });
      assert.deepEqual(JSON.parse(await readFile(join(output, 'cleanup.json'), 'utf8')), { browserConnected: false, serverListening: false });
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('browser wrapper rejects timeout even when SIGTERM cleanup exits zero', async () => {
  const result = await runWrapper('timeout-graceful');
  assert.equal(result.code, 1);
  assert.equal(result.children.length, 1, 'stop before subsequent acceptance gates');
  assert.deepEqual(result.children[0].signals, ['SIGTERM']);
  assert.equal(result.logs.some(line => line.includes(': PASS;')), false);
  assert.match(result.errors.join('\n'), /timed out/);
});

test('browser wrapper accepts normal zero exits before deadline', async () => {
  const result = await runWrapper('success');
  assert.equal(result.code, 0);
  assert.equal(result.children.length, 5, 'required context two-surface gate must run');
  assert.equal(result.logs.filter(line => line.includes(': PASS;')).length, 5);
  assert.ok(result.children.every(child => child.signals.length === 0));
  assert.deepEqual(result.errors, []);
});

test('browser wrapper propagates nonzero child exit', async () => {
  const result = await runWrapper('failure');
  assert.equal(result.code, 1);
  assert.equal(result.children.length, 1);
  assert.equal(result.logs.some(line => line.includes(': PASS;')), false);
  assert.match(result.errors.join('\n'), /failed \(7\)/);
});

test('browser wrapper escalates an uncooperative timed-out child and fails', async () => {
  const result = await runWrapper('timeout-forced');
  assert.equal(result.code, 1);
  assert.equal(result.children.length, 1);
  assert.deepEqual(result.children[0].signals, ['SIGTERM', 'SIGKILL']);
  assert.equal(result.logs.some(line => line.includes(': PASS;')), false);
  assert.match(result.errors.join('\n'), /timed out/);
});

test('browser wrapper propagates child spawn failure', async () => {
  const result = await runWrapper('spawn-error');
  assert.equal(result.code, 1);
  assert.equal(result.children.length, 1);
  assert.equal(result.logs.some(line => line.includes(': PASS;')), false);
  assert.match(result.errors.join('\n'), /spawn failed/);
});
