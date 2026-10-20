import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type OpenCodeProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install a reviewed Threadforge procedure into OpenCode's project Agent Skills directory. */
export function installOpenCodeProjectSkill(options: OpenCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'opencode' });
}

/** Remove only the unchanged Threadforge procedure installed for OpenCode. */
export function uninstallOpenCodeProjectSkill(options: OpenCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'opencode' });
}
