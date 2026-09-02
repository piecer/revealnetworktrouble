import test from 'node:test';
import assert from 'node:assert/strict';
import { cp, mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';

const productionAssets = ['app.js', 'index.html', 'state.js', 'styles.css', 'topology-model.js', 'topology-renderer.js'];

test('frontend image root materializes only production assets', async () => {
  const dockerfile = await readFile(new URL('./Dockerfile', import.meta.url), 'utf8');
  assert.match(dockerfile, /RUN\s+rm\s+-rf\s+\/usr\/share\/nginx\/html\/\*/i, 'base-image HTML must be removed before production assets are copied');
  const copies = dockerfile.split(/\r?\n/).filter(line => /^COPY\s+/i.test(line));
  const sources = copies.flatMap(line => line.trim().split(/\s+/).slice(1, -1));
  assert.deepEqual([...sources].sort(), productionAssets);
  assert.ok(copies.every(line => /\/usr\/share\/nginx\/html\/?$/.test(line)));

  const root = await mkdtemp(join(tmpdir(), 'checknetwork-frontend-image-root-'));
  try {
    for (const source of sources) await cp(new URL(`./${source}`, import.meta.url), join(root, basename(source)), { recursive: true });
    assert.deepEqual((await readdir(root)).sort(), productionAssets);
    assert.equal((await readdir(root)).some(name => name === 'node_modules' || name.endsWith('.test.js') || name === 'package.json' || name === 'package-lock.json'), false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
