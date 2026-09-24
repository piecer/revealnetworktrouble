#!/usr/bin/env python3
"""Opt-in live nginx regression; borrowed cached base, no builds/Compose up.

Run: python3 scripts/test_deployment_live.py --receipts /absolute/new/directory
Every container create/cleanup is journaled; only this run's exact ID is removed.
"""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ('app.js', 'index.html', 'state.js', 'styles.css', 'topology-model.js',
          'topology-renderer.js', 'geo-map.js', 'topology-presentation.js', 'topology-visualizer.js')


def docker(*args, check=True):
    result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=60)
    if check and result.returncode:
        raise RuntimeError(f'docker {args[0]}: {result.stderr}')
    return result


class OwnedContainer:
    def __init__(self, receipts, image, mounts):
        self.path = receipts / ('container-' + uuid.uuid4().hex + '.json')
        self.state = {'name': 'checknetwork-deployment-' + uuid.uuid4().hex,
                      'owner': uuid.uuid4().hex, 'id': None, 'phase': 'preflight'}
        self.image, self.mounts = image, mounts

    def save(self):
        self.path.write_text(json.dumps(self.state, indent=2) + '\n')

    def inspect(self):
        data = json.loads(docker('inspect', self.state['id']).stdout)[0]
        assert data['Id'] == self.state['id']
        assert data['Name'] == '/' + self.state['name']
        assert data['Config']['Labels']['checknetwork.deployment.owner'] == self.state['owner']
        assert not any(m['Type'] == 'volume' for m in data['Mounts'])
        return data

    def __enter__(self):
        name = self.state['name']
        existing = docker('container', 'inspect', name, check=False)
        assert existing.returncode != 0 and 'No such container' in existing.stderr, existing.stderr
        image = json.loads(docker('image', 'inspect', self.image).stdout)[0]
        assert not image['Config'].get('Volumes'), 'borrowed base must not create anonymous volumes'
        self.state.update(phase='create_pending', image_id=image['Id'])
        self.save()
        args = ['create', '--pull=never', '--name', name, '--label',
                'checknetwork.deployment.owner=' + self.state['owner'],
                '--publish', '127.0.0.1::80', '--memory', '64m', '--cpus', '0.25',
                '--pids-limit', '64', '--entrypoint', 'nginx']
        for source, target in self.mounts:
            args.extend(['--mount', f'type=bind,src={source},dst={target},readonly'])
        # Block termination through the create receipt: never lose a returned ID.
        blocked = {signal.SIGTERM, signal.SIGINT, signal.SIGHUP}
        old = signal.pthread_sigmask(signal.SIG_BLOCK, blocked)
        try:
            try:
                result = docker(*args, self.image, '-g', 'daemon off;', check=False)
                self.state.update(create_exit=result.returncode, create_stdout=result.stdout,
                                  create_stderr=result.stderr)
                if result.returncode == 0:
                    self.state['id'] = result.stdout.strip()
                    self.state['phase'] = 'created'
                self.save()
            finally:
                # Cleanup scope already covers both pending delivery here and
                # signals arriving after unmask, before initialization finishes.
                signal.pthread_sigmask(signal.SIG_SETMASK, old)
            if result.returncode:
                raise RuntimeError('create failed; journal retained, no name-based adoption')
            assert re.fullmatch(r'[0-9a-f]{64}', self.state['id'])
            self.inspect()
            docker('start', self.state['id'])
            info = self.inspect()
            binding = info['NetworkSettings']['Ports']['80/tcp'][0]
            assert binding['HostIp'] == '127.0.0.1'
            self.port = int(binding['HostPort'])
            self.state.update(phase='started', port=self.port)
            self.save()
            for _ in range(100):
                try:
                    self.get('/')
                    return self
                except (OSError, http.client.HTTPException):
                    time.sleep(0.05)
            raise RuntimeError('nginx never became ready')
        except BaseException:
            self.close()
            raise

    def get(self, path, headers=None):
        conn = http.client.HTTPConnection('127.0.0.1', self.port, timeout=5)
        try:
            conn.request('GET', path, headers=headers or {})
            response = conn.getresponse()
            return response.status, dict((k.lower(), v) for k, v in response.getheaders()), response.read()
        finally:
            conn.close()

    def close(self):
        if not self.state['id'] or self.state['phase'] == 'removed':
            return
        assert re.fullmatch(r'[0-9a-f]{64}', self.state['id']), 'unusable create receipt: preserve for manual reconciliation'
        self.inspect()  # Exact immutable ID + name + independent owner label.
        docker('rm', '-f', self.state['id'])
        for target in (self.state['id'], self.state['name']):
            result = docker('container', 'inspect', target, check=False)
            assert result.returncode != 0 and 'No such container' in result.stderr, result.stderr
        self.state.update(phase='removed', absence_verified=True)
        self.save()

    def __exit__(self, *_):
        self.close()


def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


def run(receipts):
    receipts.mkdir(parents=True, exist_ok=False)
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, interrupted)
    base = re.search(r'^FROM (\S+)', (ROOT / 'frontend/Dockerfile').read_text())
    assert base, 'Dockerfile must declare its pinned base'
    image = base.group(1)
    html = receipts / 'html'
    html.mkdir(mode=0o755)
    for name in ASSETS:
        shutil.copyfile(ROOT / 'frontend' / name, html / name)
    paths = {'/': 'index.html', '/index.html': 'index.html', '/app.js': 'app.js',
             '/styles.css': 'styles.css', '/state.js': 'state.js', '/spa/route': 'index.html'}
    before = {}
    for name in set(paths.values()):
        path = html / name
        marker = b'\n<!-- generation A -->\n' if name.endswith('.html') else b'\n/* generation A */\n'
        path.write_bytes(path.read_bytes() + marker)
        before[name] = path.read_bytes()
    for name in ASSETS:
        os.utime(html / name, (0, 0))
    # Copy config so the receipt freezes the actual tested configuration.
    config = receipts / 'nginx.conf'
    shutil.copyfile(ROOT / 'frontend/nginx.conf', config)
    result = {'config_sha256': hashlib.sha256(config.read_bytes()).hexdigest(), 'responses': []}
    with OwnedContainer(receipts, image, [(html, '/usr/share/nginx/html'), (config, '/etc/nginx/nginx.conf')]) as nginx:
        initial = {path: nginx.get(path) for path in paths}
        for path, name in paths.items():
            assert initial[path][0] == 200 and initial[path][2] == before[name]
        for name, content in before.items():
            updated = content.replace(b'generation A', b'generation B')
            assert len(updated) == len(content) and updated != content
            (html / name).write_bytes(updated)
            os.utime(html / name, (0, 0))
        for path, name in paths.items():
            old_headers = initial[path][1]
            # Include pre-fix validators even when current config no longer emits ETag.
            etag = old_headers.get('etag', f'"0-{len(before[name]):x}"')
            modified = old_headers['last-modified']
            for condition in ({'If-None-Match': etag}, {'If-Modified-Since': modified},
                              {'If-None-Match': etag, 'If-Modified-Since': modified}):
                status, headers, body = nginx.get(path, condition)
                expected = (html / name).read_bytes()
                result['responses'].append({'path': path, 'condition': condition, 'status': status,
                    'cache_control': headers.get('cache-control'), 'etag': headers.get('etag'),
                    'old_sha256': hashlib.sha256(before[name]).hexdigest(),
                    'expected_sha256': hashlib.sha256(expected).hexdigest(),
                    'response_sha256': hashlib.sha256(body).hexdigest(),
                    'fresh': status == 200 and body == expected,
                    'policy': headers.get('cache-control') == 'no-store' and 'etag' not in headers})
    (receipts / 'results.json').write_text(json.dumps(result, indent=2) + '\n')
    for row in result['responses']:
        print(json.dumps(row))
    assert all(row['fresh'] for row in result['responses']), 'stale conditional response: changed bytes must return 200'
    assert all(row['policy'] for row in result['responses']), 'stable URLs must disable caching and ETags'
    print('PASS: actual isolated nginx, equal-length epoch=0 generations, 18 conditional requests; cleanup verified')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--receipts', type=Path, required=True)
    run(parser.parse_args().receipts.resolve())
