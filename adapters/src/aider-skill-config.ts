import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type AiderProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Stage an approved procedure for explicit, read-only Aider --read use. */
export function installAiderProjectSkill(options: AiderProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'aider' });
}

/** Remove only the exact unchanged Threadforge-owned Aider skill export. */
export function uninstallAiderProjectSkill(options: AiderProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'aider' });
}
