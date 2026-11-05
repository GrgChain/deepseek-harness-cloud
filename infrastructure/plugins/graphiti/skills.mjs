import { fileURLToPath } from 'node:url';
import * as skillFilesystem from '@deepseek-ai/dsh-skill-filesystem';

export function mountGraphitiSkills(ctx) {
  return ctx.plugin(skillFilesystem, {
    providerName: 'graphiti',
    includeDefaultRoots: false,
    bundledSkillDir: fileURLToPath(new URL('./skills', import.meta.url)),
    watch: false,
  });
}
