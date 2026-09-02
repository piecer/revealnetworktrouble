import test from 'node:test';
import assert from 'node:assert/strict';
import { cp, mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';

const productionAssets = ['app.js', 'index.html', 'state.js', 'styles.css', 'topology-model.js', 'topology-renderer.js'];

test('frontend image root materializes only production assets and keeps nginx config separate', async () => {
  const dockerfile = await readFile(new URL('./Dockerfile', import.meta.url), 'utf8');
  assert.match(dockerfile, /RUN\s+rm\s+-rf\s+\/usr\/share\/nginx\/html\/\*/i, 'base-image HTML must be removed before production assets are copied');
  const copies = dockerfile.split(/\r?\n/).filter(line => /^COPY\s+/i.test(line));
  const assetCopies = copies.filter(line => /\/usr\/share\/nginx\/html\/?$/.test(line));
  const assetSources = assetCopies.flatMap(line => line.trim().split(/\s+/).slice(1, -1));
  assert.deepEqual([...assetSources].sort(), productionAssets);
  assert.equal(assetCopies.length, 1, 'the six-file HTML root must use one explicit COPY');

  const configCopies = copies.filter(line => /\/etc\/nginx\/nginx\.conf$/.test(line));
  assert.deepEqual(configCopies, ['COPY nginx.conf /etc/nginx/nginx.conf']);
  assert.equal(copies.length, assetCopies.length + configCopies.length, 'only assets and nginx config may be copied');

  const root = await mkdtemp(join(tmpdir(), 'checknetwork-frontend-image-root-'));
  try {
    for (const source of assetSources) await cp(new URL(`./${source}`, import.meta.url), join(root, basename(source)), { recursive: true });
    assert.deepEqual((await readdir(root)).sort(), productionAssets);
    assert.equal((await readdir(root)).some(name => name === 'node_modules' || name.includes('.test.') || name === 'package.json' || name === 'package-lock.json'), false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('nginx uses a fixed small worker pool and serves the unchanged HTML root', async () => {
  const config = await readFile(new URL('./nginx.conf', import.meta.url), 'utf8');
  assert.match(config, /^worker_processes\s+2;/m);
  assert.match(config, /root\s+\/usr\/share\/nginx\/html;/);
  assert.match(config, /location\s+\/\s*\{/);
  assert.doesNotMatch(config, /proxy_pass/);
});
