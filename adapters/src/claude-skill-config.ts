import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type ClaudeCodeProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into a local Claude Code project skills directory. */
export function installClaudeCodeProjectSkill(options: ClaudeCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'claude-code' });
}

/** Remove only the unchanged Threadforge procedure installed for Claude Code. */
export function uninstallClaudeCodeProjectSkill(options: ClaudeCodeProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'claude-code' });
}
