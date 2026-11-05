// Build the reviewed offline artifact without running scripts or publishing.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const source = new URL('./mem0/', import.meta.url);
const output = new URL('../../apps/public/plugin-packages/', import.meta.url);
const manifest = JSON.parse(await readFile(new URL('package.json', source)));
if (manifest.name !== '@harness-cloud/dsh-mem0-memory' || manifest.version !== '0.2.0')
  throw Error('Update the catalog distribution when changing the package identity');
await mkdir(output, { recursive: true });
const [packed] = JSON.parse(
  execFileSync(
    'npm',
    [
      'pack',
      '--ignore-scripts',
      '--offline',
      '--json',
      '--pack-destination',
      fileURLToPath(output),
    ],
    { cwd: source, encoding: 'utf8' },
  ),
);
const allowed = new Set([
  'package.json',
  'index.mjs',
  'runtime.mjs',
  'managed.mjs',
  'session.mjs',
  'mcp.mjs',
  'skills.mjs',
  'skills/mem0-memory/SKILL.md',
  'servers/mcp-server.mjs',
  'cordis.patch.yml',
  'README.md',
  'LICENSE',
  'icon.svg',
  'locale/en.json',
  'locale/zh.json',
]);
if (packed.files.length !== allowed.size || packed.files.some((file) => !allowed.has(file.path)))
  throw Error('Unexpected contents in Mem0 package');
const archive = await readFile(new URL(packed.filename, output));
const checksum = createHash('sha256').update(archive).digest('hex');
const embedded = new URL('./mem0-dist/', import.meta.url);
await mkdir(embedded, { recursive: true });
await writeFile(new URL(packed.filename, embedded), archive);
await writeFile(
  new URL(packed.filename + '.sha256', output),
  checksum + '  ' + packed.filename + '\n',
);
console.log(
  JSON.stringify({
    filename: packed.filename,
    version: manifest.version,
    files: packed.files.length,
    bytes: archive.length,
    sha256: checksum,
  }),
);
