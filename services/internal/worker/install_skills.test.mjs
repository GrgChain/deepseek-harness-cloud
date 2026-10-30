import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { installSkills } from './install_skills.mjs';

const identity = { schema_version: 1, instance_id: 'instance', asset_id: 'asset', org_id: 'org', owner_id: 'owner' };
const request = { ...identity, skills: [{ id: 'skill', version: 2, name: 'notes', bundle: { files: {
  'SKILL.md': Buffer.from('---\nname: notes\ndescription: Notes\n---\nPrivate instructions').toString('base64'),
  'scripts/run.py': Buffer.from('print("hello")').toString('base64'),
  'references/image.bin': Buffer.from([0, 255, 7]).toString('base64'),
} } }] };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'hc-skills-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.writeFileSync(path.join(root, '.hc-initialized'), JSON.stringify(identity));
  return root;
}
test('copies complete bundles and preserves workspace changes on recovery', t => {
  const root = fixture(t);
  installSkills(root, request);
  const base = path.join(root, 'dsh/skills/notes');
  assert.equal(fs.readFileSync(path.join(base, 'scripts/run.py'), 'utf8'), 'print("hello")');
  assert.deepEqual(fs.readFileSync(path.join(base, 'references/image.bin')), Buffer.from([0, 255, 7]));
  assert.equal(fs.statSync(path.join(base, 'SKILL.md')).mode & 0o777, 0o600);
  fs.writeFileSync(path.join(base, 'SKILL.md'), 'User edit');
  installSkills(root, request);
  assert.equal(fs.readFileSync(path.join(base, 'SKILL.md'), 'utf8'), 'User edit');
  assert.throws(() => installSkills(root, { ...request, skills: [{ ...request.skills[0], version: 3 }] }));
});
test('recovers a rename before the marker, but refuses divergent existing files', t => {
  const root = fixture(t);
  installSkills(root, request);
  const marker = path.join(root, '.hc-skills-initialized');
  fs.unlinkSync(marker);
  installSkills(root, request);
  fs.unlinkSync(marker);
  fs.writeFileSync(path.join(root, 'dsh/skills/notes/SKILL.md'), 'Unrelated contents');
  assert.throws(() => installSkills(root, request));
  assert.equal(fs.readFileSync(path.join(root, 'dsh/skills/notes/SKILL.md'), 'utf8'), 'Unrelated contents');
});
test('rejects a foreign asset, traversal and directory symlinks', t => {
  const root = fixture(t);
  assert.throws(() => installSkills(root, { ...request, owner_id: 'other' }));
  const bad = structuredClone(request);
  bad.skills[0].bundle.files['../outside'] = Buffer.from('bad').toString('base64');
  assert.throws(() => installSkills(root, bad));
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), 'hc-skills-outside-'));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));
  fs.rmSync(path.join(root, 'dsh'), { recursive: true, force: true });
  fs.symlinkSync(outside, path.join(root, 'dsh'));
  assert.throws(() => installSkills(root, request));
  assert.deepEqual(fs.readdirSync(outside), []);
});

test('installs a large selection through the stdin helper and recovers it intact', t => {
  const root = fixture(t);
  const skills = Array.from({ length: 33 }, (_, n) => ({
    id: `skill-${n}`, version: 1, name: `skill-${n}`, bundle: { files: {
      'SKILL.md': Buffer.from(`---\nname: skill-${n}\ndescription: Resource\n---\nInstructions`).toString('base64'),
      'references/data.bin': Buffer.alloc(512 * 1024, n).toString('base64'),
    } },
  }));
  const input = JSON.stringify({ ...identity, skills });
  assert.ok(Buffer.byteLength(input) > 18 * 1024 * 1024);
  // Use the actual stdin entry point, with its volume path mapped to our fixture.
  const source = fs.readFileSync(fileURLToPath(new URL('./install_skills.mjs', import.meta.url)), 'utf8')
    .replace("installSkills('/data',", `installSkills(${JSON.stringify(root)},`);
  const run = () => spawnSync(process.execPath, ['--input-type=module', '-e', source, '--', '--install'], { input, encoding: 'utf8' });
  const result = run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { installed: true });
  for (let n = 0; n < skills.length; n++) {
    assert.deepEqual(fs.readFileSync(path.join(root, `dsh/skills/skill-${n}/references/data.bin`)), Buffer.alloc(512 * 1024, n));
  }
  fs.unlinkSync(path.join(root, '.hc-skills-initialized'));
  assert.equal(run().status, 0);
});
