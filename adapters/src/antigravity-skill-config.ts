import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type AntigravityProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved Threadforge procedure into Antigravity's project Agent Skills directory. */
export function installAntigravityProjectSkill(options: AntigravityProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'antigravity' });
}

/** Remove only the unchanged Threadforge procedure installed for Antigravity. */
export function uninstallAntigravityProjectSkill(options: AntigravityProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'antigravity' });
}
