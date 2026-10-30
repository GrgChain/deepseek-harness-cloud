// Executed by the worker in an isolated helper before the workspace starts.
// Input arrives over stdin; skill content never appears in command arguments.
import fs from 'node:fs';
import path from 'node:path';
import { createHash, randomUUID } from 'node:crypto';

export function installSkills(root, request) {
  const identity = JSON.parse(fs.readFileSync(path.join(root, '.hc-initialized'), 'utf8'));
  if (identity.schema_version !== 1 || ['instance_id', 'asset_id', 'org_id', 'owner_id'].some(key => identity[key] !== request[key])) throw Error('Storage identity mismatch');
  const digest = createHash('sha256').update(JSON.stringify(request.skills)).digest('hex');
  const marker = path.join(root, '.hc-skills-initialized');
  const exists = value => { try { return fs.lstatSync(value); } catch (error) { if (error.code === 'ENOENT') return null; throw error; } };
  const previous = exists(marker);
  if (previous) {
    if (!previous.isFile() || fs.readFileSync(marker, 'utf8') !== digest) throw Error('Skill initialization mismatch');
    return;
  }
  const directory = value => {
    const info = exists(value);
    if (info) { if (!info.isDirectory()) throw Error('Unsafe skill directory'); }
    else fs.mkdirSync(value, { mode: 0o700 });
  };
  const home = path.join(root, 'dsh');
  directory(home);
  const destination = path.join(home, 'skills');
  const staging = fs.mkdtempSync(path.join(home, '.hc-skills-'));
  const expected = new Map();
  try {
    for (const skill of request.skills) {
      if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(skill.name) || skill.name.length > 64) throw Error('Invalid skill name');
      const base = path.join(staging, skill.name);
      fs.mkdirSync(base, { mode: 0o700 });
      for (const [relative, encoded] of Object.entries(skill.bundle.files)) {
        if (relative.includes('\\') || relative.split('/').some(part => !part || part.startsWith('.') || part.includes(':'))) throw Error('Invalid skill path');
        const target = path.join(base, relative);
        fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
        const content = Buffer.from(encoded, 'base64');
        expected.set(`${skill.name}/${relative}`, content);
        const fd = fs.openSync(target, 'wx', 0o600);
        try { fs.writeFileSync(fd, content); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
      }
    }
    if (exists(destination)) {
      // A crash may have happened between the atomic rename and the marker.
      // Adopt only the exact copy, never replace existing workspace content.
      const verify = (folder, prefix = '') => {
        if (!fs.lstatSync(folder).isDirectory()) throw Error('Unsafe skill directory');
        for (const entry of fs.readdirSync(folder, { withFileTypes: true })) {
          const relative = prefix + entry.name;
          const target = path.join(folder, entry.name);
          if (entry.isDirectory()) verify(target, relative + '/');
          else if (!entry.isFile() || !expected.has(relative) || !fs.readFileSync(target).equals(expected.get(relative))) throw Error('Existing skills differ from snapshot');
          else expected.delete(relative);
        }
      };
      verify(destination);
      if (expected.size) throw Error('Incomplete existing skills');
    } else fs.renameSync(staging, destination);
    const temporaryMarker = path.join(home, `.hc-skills-marker-${randomUUID()}`);
    const fd = fs.openSync(temporaryMarker, 'wx', 0o600);
    try { fs.writeFileSync(fd, digest); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
    fs.renameSync(temporaryMarker, marker);
    const folder = fs.openSync(home, 'r');
    try { fs.fsyncSync(folder); } finally { fs.closeSync(folder); }
    const volume = fs.openSync(root, 'r');
    try { fs.fsyncSync(volume); } finally { fs.closeSync(volume); }
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] === '--install') {
  try {
    const input = fs.readFileSync(0);
    installSkills('/data', JSON.parse(input));
    process.stdout.write('{"installed":true}');
  } catch {
    console.error('Workspace skill initialization failed');
    process.exitCode = 1;
  }
}
