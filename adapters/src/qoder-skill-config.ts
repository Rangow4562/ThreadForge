import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type QoderProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into Qoder CLI's project Skills directory. */
export function installQoderProjectSkill(options: QoderProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'qoder' });
}

/** Remove only the unchanged Threadforge procedure installed for Qoder. */
export function uninstallQoderProjectSkill(options: QoderProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'qoder' });
}
