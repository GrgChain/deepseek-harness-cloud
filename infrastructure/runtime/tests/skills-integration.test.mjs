import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';

const exec = promisify(execFile);
test('installed workspace skills reach the native user catalog and model loader', { skip: !process.env.HC_RUNTIME_IMAGE, timeout: 45000 }, async () => {
  const installer = await readFile(new URL('../../../services/internal/worker/install_skills.mjs', import.meta.url), 'utf8');
  const script = `
    import assert from 'node:assert/strict';
    import fs from 'node:fs/promises';
    import { Context } from '@deepseek-ai/cordis';
    import SkillRegistry, { isUserInvocable } from '@deepseek-ai/dsh-skill';
    import * as filesystem from '@deepseek-ai/dsh-skill-filesystem';
    import { apply as mountTool } from '@deepseek-ai/dsh-tool-skill';
    const { installSkills } = await import('data:text/javascript;base64,${Buffer.from(installer).toString('base64')}');
    const root = '/tmp/workspace-skills';
    await fs.mkdir(root);
    const identity = { schema_version: 1, instance_id: 'instance', asset_id: 'asset', org_id: 'org', owner_id: 'owner' };
    await fs.writeFile(root + '/.hc-initialized', JSON.stringify(identity));
    const bundle = enabled => ({ files: {
      'SKILL.md': Buffer.from('---\\nname: ' + (enabled ? 'selected' : 'disabled') + '\\ndescription: Workspace instructions\\nwhenToUse: During verification\\ndisable-model-invocation: ' + !enabled + '\\nuser-invocable: ' + enabled + '\\n---\\nRead references/guide.txt before acting.').toString('base64'),
      'references/guide.txt': Buffer.from('Associated resource').toString('base64'),
    } });
    installSkills(root, { ...identity, skills: [
      { name: 'selected', bundle: bundle(true) },
      { name: 'disabled', bundle: bundle(false) },
    ] });
    const ctx = new Context();
    const mounted = [];
    try {
      mounted.push(ctx.plugin(SkillRegistry));
      await mounted.at(-1);
      mounted.push(ctx.plugin(filesystem, { dshHome: root + '/dsh', agentsHome: '/tmp/agents', watch: false }));
      await mounted.at(-1);
      const listed = await ctx.skills.list({ cwd: '/tmp' });
      assert.deepEqual(listed.filter(isUserInvocable).map(item => item.name), ['selected']);
      assert.equal(listed.find(item => item.name === 'selected').invocation.modelInvocable, true);
      let tool;
      mountTool({ skills: ctx.skills, tools: { register(value) { tool = value; } }, on() {} });
      const loaded = await tool.execute({ name: 'selected' }, {});
      assert.equal(loaded.content, 'Read references/guide.txt before acting.');
      assert.equal(loaded.resourceBase.path, root + '/dsh/skills/selected');
      assert.equal(await fs.readFile(loaded.resourceBase.path + '/references/guide.txt', 'utf8'), 'Associated resource');
      await assert.rejects(tool.execute({ name: 'disabled' }, {}), /not available for model invocation/);
      console.log(JSON.stringify({ user_catalog: true, model_loader: true, resources: true }));
    } finally { for (const fiber of mounted.reverse()) await fiber.dispose(); }
  `;
  const name = 'hc-skill-native-test-' + randomUUID();
  try {
    const { stdout } = await exec('docker', ['run', '--rm', '--name', name, '--read-only', '--network', 'none', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--pids-limit', '64', '--memory', '512m', '--cpus', '1', '--tmpfs', '/tmp:rw,noexec,nosuid,nodev,size=64m,uid=1000,gid=1000', '--entrypoint', 'node', process.env.HC_RUNTIME_IMAGE, '--input-type=module', '-e', script], { timeout: 40000, maxBuffer: 8192 });
    assert.deepEqual(JSON.parse(stdout.trim()), { user_catalog: true, model_loader: true, resources: true });
  } finally {
    await exec('docker', ['rm', '-f', name], { timeout: 5000 }).catch(() => {});
  }
});
