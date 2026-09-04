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

test('frontend release image is pinned, provenance-labelled, validated, and timestamp-normalized', async () => {
  const dockerfile = await readFile(new URL('./Dockerfile', import.meta.url), 'utf8');
  assert.match(dockerfile, /^FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10$/m);
  assert.doesNotMatch(dockerfile, /^FROM\s+[^\s@]+:[^\s]+$/m, 'mutable bases are forbidden');
  for (const name of ['VERSION', 'REVISION', 'SOURCE_DATE_EPOCH']) {
    assert.match(dockerfile, new RegExp(`^ARG ${name}$`, 'm'));
  }
  assert.match(dockerfile, /revision must be 40 lowercase hexadecimal characters/);
  assert.match(dockerfile, /SOURCE_DATE_EPOCH.*non-empty decimal/s);
  assert.match(dockerfile, /org\.opencontainers\.image\.version="\$VERSION"/);
  assert.match(dockerfile, /org\.opencontainers\.image\.revision="\$REVISION"/);
  assert.match(dockerfile, /sha256sum.*sort/s, 'asset identity must be deterministic and sorted');
  assert.match(dockerfile, /touch\s+-d\s+"@\$SOURCE_DATE_EPOCH"/, 'copied-file mtimes must be normalized');
});

test('compose passes the same release identity arguments to the frontend context', async () => {
  const compose = await readFile(new URL('../compose.yaml', import.meta.url), 'utf8');
  assert.match(compose, /web:\s*\n\s+build:\s*\n\s+context:\s*\.\/frontend/);
  for (const [name, value] of [
    ['VERSION', '${CHECKNETWORK_VERSION:-dev}'],
    ['REVISION', '${CHECKNETWORK_REVISION:-dev}'],
    ['SOURCE_DATE_EPOCH', '${SOURCE_DATE_EPOCH:-0}'],
  ]) {
    assert.ok(compose.includes(`${name}: ${value}`), `web build must pass ${name}`);
  }
});

test('release verifier exports two tagless Web archives and validates them offline', async () => {
  const script = await readFile(new URL('../scripts/verify-release.sh', import.meta.url), 'utf8');
  const ownership = await readFile(new URL('../scripts/release-resource-ownership.sh', import.meta.url), 'utf8');
  assert.match(script, /docker buildx build --no-cache --provenance=false/);
  assert.match(script, /docker buildx build --platform linux\/amd64 --no-cache --provenance=false/);
  assert.match(script, /SOURCE_DATE_EPOCH=0/);
  assert.match(script, /web-base-context/);
  assert.match(script, /--build-arg "SOURCE_DATE_EPOCH=\$source_date_epoch"/);
  assert.match(script, /--output=type=docker,dest="\$web_archive",rewrite-timestamp=true/);
  assert.match(script, /scripts\/verify_web_archive\.py/);
  assert.match(script, /nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10/);
  for (const forbidden of ['docker load', 'docker import', 'docker tag']) {
    assert.equal(script.includes(forbidden), false, `${forbidden} must never be used for a derived Web archive`);
  }
  assert.doesNotMatch(script, /docker image rm[^\n]*(web|nginx)/i);
  assert.match(ownership, /network_create_pending/);
  assert.match(ownership, /container_create_pending/);
  assert.match(ownership, /com\.checknetwork\.release\.nonce/);
  assert.match(ownership, /com\.checknetwork\.release\.owner/);
  assert.doesNotMatch(ownership, /docker (?:network|container) create[^\n]*\|\|\s*:/);
  assert.match(ownership, /web_error_is_conflict/);
  assert.match(ownership, /web_error_is_response_loss/);
});
