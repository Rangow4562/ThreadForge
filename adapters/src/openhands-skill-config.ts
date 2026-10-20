import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type OpenHandsProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into OpenHands' project Agent Skills directory. */
export function installOpenHandsProjectSkill(options: OpenHandsProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'openhands' });
}

/** Remove only the unchanged Threadforge procedure installed for OpenHands. */
export function uninstallOpenHandsProjectSkill(options: OpenHandsProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'openhands' });
}
