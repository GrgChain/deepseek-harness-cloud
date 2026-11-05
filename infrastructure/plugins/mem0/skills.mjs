import { fileURLToPath } from 'node:url';
import * as skillFilesystem from '@deepseek-ai/dsh-skill-filesystem';

export function mountMem0Skills(ctx) {
  return ctx.plugin(skillFilesystem, {
    providerName: 'mem0',
    includeDefaultRoots: false,
    bundledSkillDir: fileURLToPath(new URL('./skills', import.meta.url)),
    watch: false,
  });
}
