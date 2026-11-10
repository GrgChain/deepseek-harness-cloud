// Uses the existing, immutable runtime image and the native profile format.
import fs from 'node:fs';
import path from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';

export function installExtensions(root, request) {
  const marker = JSON.parse(fs.readFileSync(path.join(root, '.hc-initialized'), 'utf8'));
  if (
    marker.schema_version !== 1 ||
    ['instance_id', 'asset_id', 'org_id', 'owner_id'].some((key) => marker[key] !== request[key])
  )
    throw Error('Storage identity mismatch');
  const digest = createHash('sha256').update(JSON.stringify(request.extensions)).digest('hex');
  const exists = (value) => {
    try {
      return fs.lstatSync(value);
    } catch (error) {
      if (error.code === 'ENOENT') return null;
      throw error;
    }
  };
  const initialized = path.join(root, '.hc-extensions-initialized');
  if (exists(initialized)) {
    if (!exists(initialized).isFile() || fs.readFileSync(initialized, 'utf8') !== digest)
      throw Error('Extension identity mismatch');
    return;
  }
  const directory = (value) => {
    const info = exists(value);
    if (info && !info.isDirectory()) throw Error('Unsafe profile directory');
    if (!info) fs.mkdirSync(value, { mode: 0o700 });
  };
  const home = path.join(root, 'dsh');
  directory(home);
  const profiles = path.join(home, 'profiles');
  directory(profiles);
  const staging = fs.mkdtempSync(path.join(profiles, '.hc-extensions-'));
  const target = path.join(profiles, 'web');
  try {
    const bundles = ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app'];
    for (const item of request.extensions.items) {
      if (item.kind === 'plugin') {
        if (
          !/^@deepseek-ai\/dsh-experimental-[a-z-]+$/.test(item.package) ||
          item.package_version !== '0.2.1-alpha.2'
        )
          throw Error('Unapproved plugin');
        const metadata = JSON.parse(
          fs.readFileSync(
            '/opt/harness-runtime/node_modules/' + item.package + '/package.json',
            'utf8',
          ),
        );
        if (metadata.version !== item.package_version || !metadata.dsh?.bundle)
          throw Error('Incompatible plugin');
        bundles.push(item.package);
      }
    }
    const memory = request.extensions.items.find(
      (item) => item.kind === 'memory' && item.session_memory,
    );
    if (memory?.backend && !['openviking', 'mem0', 'graphiti'].includes(memory.backend))
      throw Error('Unsupported session memory backend');
    let memoryBundle;
    if (memory) {
      const mem0 = memory.backend === 'mem0';
      memoryBundle = mem0
        ? {
            name: '@harness-cloud/dsh-mem0-memory',
            version: '0.2.0',
            sha256: '8aa1815c30544749b368faa23fa7cb8a090945ead14cf329ce5767cbe184b6fc',
          }
        : memory.backend === 'graphiti'
          ? {
              name: '@harness-cloud/dsh-graphiti-memory',
              version: '0.2.0',
              sha256: 'e13d2456c7c31193cdea162746af8ebfc2fec834ec6de7d12d79ddf280d99d0a',
            }
          : {
              name: '@openviking/dsh-memory-plugin',
              version: '0.5.31',
              sha256: '111c45ce690b5ab4b3ce1db04a9e847af1fe27ebc310ec97c90dcb76d55de46e',
            };
      const artifact = Buffer.from(request.session_plugin ?? '', 'base64');
      if (createHash('sha256').update(artifact).digest('hex') !== memoryBundle.sha256)
        throw Error('Invalid session memory artifact');
      const modules = path.join(staging, 'node_modules');
      fs.mkdirSync(modules, { mode: 0o700 });
      const [scopeName, packageName] = memoryBundle.name.split('/');
      const scope = path.join(modules, scopeName);
      fs.mkdirSync(scope, { mode: 0o700 });
      const plugin = path.join(scope, packageName);
      fs.mkdirSync(plugin, { mode: 0o700 });
      const unpack = spawnSync(
        'tar',
        [
          '-xzf',
          '-',
          '--strip-components=1',
          '--no-same-owner',
          '--no-same-permissions',
          '-C',
          plugin,
        ],
        { input: artifact, timeout: 10000, stdio: ['pipe', 'ignore', 'pipe'] },
      );
      if (unpack.status !== 0) throw Error('Session memory extraction failed');
      const metadata = JSON.parse(fs.readFileSync(path.join(plugin, 'package.json'), 'utf8'));
      if (
        metadata.name !== memoryBundle.name ||
        metadata.version !== memoryBundle.version ||
        !metadata.dsh?.bundle
      )
        throw Error('Incompatible session memory plugin');
      // Supply the exact host peers without running npm, scripts or a network call.
      fs.symlinkSync('/opt/harness-runtime/node_modules', path.join(plugin, 'node_modules'));
      const sync = (value) => {
        const info = fs.lstatSync(value);
        if (info.isSymbolicLink()) return;
        if (info.isDirectory())
          for (const entry of fs.readdirSync(value)) sync(path.join(value, entry));
        const fd = fs.openSync(value, 'r');
        try {
          fs.fsyncSync(fd);
        } finally {
          fs.closeSync(fd);
        }
      };
      sync(modules);
      bundles.push(memoryBundle.name);
    }
    const sessionMemory = bundles.includes('@openviking/dsh-memory-plugin');
    const patches = [
      {
        insert: [
          { id: 'harness-cloud-extensions', name: '/data/dsh/profiles/web/cloud-extensions.mjs' },
        ],
      },
    ];
    if (sessionMemory)
      patches.push({
        id: 'openviking-memory-runtime',
        inject: ['cloudSessionMemory'],
        config: {
          endpoint: { __jsExpr: 'ctx.cloudSessionMemory.endpoint' },
          apiKey: '',
          account: '',
          user: '',
          peerSource: 'none',
          syncTurns: true,
          captureToolResults: true,
          captureMaxLength: 100000,
          skipSubagentSessions: false,
          commitTokenThreshold: 20000,
        },
      });
    const localMemory = memoryBundle && memoryBundle.name !== '@openviking/dsh-memory-plugin';
    const localFilename = localMemory
      ? memoryBundle.name.slice(1).replace('/', '-') + '-' + memoryBundle.version + '.tgz'
      : undefined;
    if (localMemory)
      patches.push({
        id: memory.backend + '-memory-runtime',
        disabled: false,
        inject: ['cloudSessionMemory'],
        config: {
          ...(memory.backend === 'mem0' ? { managed: true } : {}),
          endpoint: { __jsExpr: 'ctx.cloudSessionMemory.endpoint' },
        },
      });
    const files = {
      'package.json': JSON.stringify({
        name: 'dsh-profile-web',
        private: true,
        dependencies: memoryBundle
          ? {
              [memoryBundle.name]: localMemory ? 'file:./' + localFilename : memoryBundle.version,
            }
          : {},
        dsh: { profile: { bundles } },
      }),
      'cordis.patch.yml': JSON.stringify(patches),
      'cloud-extensions.mjs': request.program,
      'cloud-mcp.mjs': request.mcp_program ?? '',
      'openviking-session.mjs': request.session_program ?? '',
      ...(localMemory
        ? {
            [localFilename]: Buffer.from(request.session_plugin, 'base64'),
          }
        : {}),
      'cloud-extensions.json': JSON.stringify(request.extensions),
      'pnpm-workspace.yaml': 'packages:\n  - .\n\nnodeLinker: hoisted\nautoInstallPeers: false\n',
    };
    for (const [name, content] of Object.entries(files)) {
      const fd = fs.openSync(path.join(staging, name), 'wx', 0o600);
      try {
        fs.writeFileSync(fd, content);
        fs.fsyncSync(fd);
      } finally {
        fs.closeSync(fd);
      }
    }
    if (exists(target)) {
      if (!exists(target).isDirectory()) throw Error('Unsafe existing profile');
      const compare = (expected, actual) => {
        const a = fs.lstatSync(expected),
          b = exists(actual);
        if (
          !b ||
          a.isFile() !== b.isFile() ||
          a.isDirectory() !== b.isDirectory() ||
          a.isSymbolicLink() !== b.isSymbolicLink()
        )
          throw Error('Existing profile differs');
        if (a.isSymbolicLink()) {
          if (fs.readlinkSync(expected) !== fs.readlinkSync(actual))
            throw Error('Existing profile differs');
        } else if (a.isFile()) {
          if (!fs.readFileSync(expected).equals(fs.readFileSync(actual)))
            throw Error('Existing profile differs');
        } else if (a.isDirectory()) {
          const names = fs.readdirSync(expected).sort();
          if (JSON.stringify(names) !== JSON.stringify(fs.readdirSync(actual).sort()))
            throw Error('Existing profile differs');
          for (const name of names) compare(path.join(expected, name), path.join(actual, name));
        } else throw Error('Unsafe profile entry');
      };
      compare(staging, target);
    } else fs.renameSync(staging, target);
    const temporary = path.join(root, '.hc-extensions-' + randomUUID());
    const fd = fs.openSync(temporary, 'wx', 0o600);
    try {
      fs.writeFileSync(fd, digest);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(temporary, initialized);
    for (const folder of [target, profiles, root]) {
      const fd = fs.openSync(folder, 'r');
      try {
        fs.fsyncSync(fd);
      } finally {
        fs.closeSync(fd);
      }
    }
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }
}
if (process.argv[1] === '--install') {
  try {
    const raw = fs.readFileSync(0);
    if (raw.length > 2 ** 20) throw Error('Oversized request');
    installExtensions('/data', JSON.parse(raw));
    process.stdout.write(JSON.stringify({ installed: true }));
  } catch {
    process.stderr.write('Workspace extension initialization failed\n');
    process.exitCode = 1;
  }
}
