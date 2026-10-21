import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type WindsurfProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into Windsurf/Cascade's shared project Agent Skills directory. */
export function installWindsurfProjectSkill(options: WindsurfProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'windsurf' });
}

/** Remove only the unchanged Threadforge procedure installed for Windsurf/Cascade. */
export function uninstallWindsurfProjectSkill(options: WindsurfProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'windsurf' });
}
