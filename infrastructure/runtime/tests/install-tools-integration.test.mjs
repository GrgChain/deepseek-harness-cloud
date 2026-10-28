import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';

const exec = promisify(execFile);
const docker = async (...args) => (await exec('docker', args, { timeout: 45000, maxBuffer: 1024 * 1024 })).stdout.trim();

test('Ubuntu runtime provides Node, Python and working install tools as a non-root user', { skip: !process.env.HC_RUNTIME_IMAGE, timeout: 60000 }, async t => {
  const name = 'hc-install-tools-' + randomUUID();
  t.after(async () => { await docker('rm', '-f', name).catch(() => {}); });
  // Serve a local fixture inside the container so this contract needs no internet.
  await docker('run', '-d', '--name', name, '--network', 'none', '--read-only', '--cap-drop', 'ALL',
    '--security-opt', 'no-new-privileges', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000',
    '--entrypoint', 'python3', process.env.HC_RUNTIME_IMAGE, '-u', '-c', `
import http.server, io, pathlib, tarfile, zipfile
root = pathlib.Path('/tmp')
payload = b'installer-download-proof\\n'
with zipfile.ZipFile(root / 'package.zip', 'w') as archive:
    archive.writestr('proof.txt', payload)
with tarfile.open(root / 'package.tar.xz', 'w:xz') as archive:
    info = tarfile.TarInfo('proof.txt')
    info.size = len(payload)
    archive.addfile(info, io.BytesIO(payload))
class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(root), **kwargs)
server = http.server.HTTPServer(('127.0.0.1', 8000), Handler)
(root / 'ready').touch()
server.serve_forever()
`);
  let ready = false;
  for (let attempt = 0; attempt < 40; attempt++) {
    if (await docker('exec', name, 'sh', '-c', 'test -f /tmp/ready && echo ready').catch(() => '') === 'ready') { ready = true; break; }
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.ok(ready, 'Local download fixture must start');
  const output = await docker('exec', name, 'bash', '-euc', `
    test "$(id -u)" = 1000
    source /etc/os-release
    test "$ID" = ubuntu
    test "$VERSION_ID" = 24.04
    node -e 'require("node:assert/strict").equal(process.versions.node, "24.21.0")'
    npm --version >/dev/null
    npx --version >/dev/null
    python -c 'import sys; assert sys.version_info[:2] == (3, 12)'
    pip --version >/dev/null
    pip3 --version >/dev/null
    python3 -m pip --version >/dev/null
    python3 -m venv /tmp/test-venv
    /tmp/test-venv/bin/python -m pip --version >/dev/null
    cd /tmp
    curl --fail --silent --show-error --location http://127.0.0.1:8000/package.zip -o downloaded.zip
    wget --quiet http://127.0.0.1:8000/package.tar.xz -O downloaded.tar.xz
    unzip -q downloaded.zip -d zip-output
    mkdir tar-output
    tar -xJf downloaded.tar.xz -C tar-output
    cmp zip-output/proof.txt tar-output/proof.txt
    cat zip-output/proof.txt
  `);
  assert.equal(output, 'installer-download-proof');
});
