import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type ZCodeProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into ZCode's project Agent Skills directory. */
export function installZCodeProjectSkill(options: ZCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'zcode' });
}

/** Remove only the unchanged Threadforge procedure installed for ZCode. */
export function uninstallZCodeProjectSkill(options: ZCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'zcode' });
}
