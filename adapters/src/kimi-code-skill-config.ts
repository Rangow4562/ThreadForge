import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type KimiCodeProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into Kimi Code's project skills directory. */
export function installKimiCodeProjectSkill(options: KimiCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'kimi-code' });
}

/** Remove only the unchanged Threadforge procedure installed for Kimi Code. */
export function uninstallKimiCodeProjectSkill(options: KimiCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'kimi-code' });
}
