import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type DevinProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install a reviewed Threadforge procedure where Devin Cloud discovers project Agent Skills. */
export function installDevinProjectSkill(options: DevinProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'devin' });
}

/** Remove only the unchanged Threadforge procedure installed for Devin. */
export function uninstallDevinProjectSkill(options: DevinProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'devin' });
}
