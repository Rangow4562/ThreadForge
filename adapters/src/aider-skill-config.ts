import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';
import { hasAiderAutoReadState, installAiderAutoRead, uninstallAiderAutoRead } from './aider-auto-read-config.ts';
export { installAiderAutoRead, uninstallAiderAutoRead };

type AiderProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Stage an approved procedure for explicit, read-only Aider --read use. */
export function installAiderProjectSkill(options: AiderProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'aider' });
}

/** Remove only the exact unchanged Threadforge-owned Aider skill export. */
export async function uninstallAiderProjectSkill(options: AiderProjectSkillOptions): Promise<ProjectSkillResult> {
  if (await hasAiderAutoReadState(options)) await uninstallAiderAutoRead(options);
  return uninstallProjectSkill({ ...options, host: 'aider' });
}
