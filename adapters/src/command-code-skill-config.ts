import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type CommandCodeProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into Command Code's project skill directory. */
export function installCommandCodeProjectSkill(options: CommandCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'command-code' });
}

/** Remove only the unchanged Threadforge procedure installed for Command Code. */
export function uninstallCommandCodeProjectSkill(options: CommandCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'command-code' });
}
