import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type CodexProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install a reviewed Threadforge procedure into Codex CLI's project Agent Skills directory. */
export function installCodexProjectSkill(options: CodexProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'codex-cli' });
}

/** Remove only the unchanged Threadforge procedure installed for Codex CLI. */
export function uninstallCodexProjectSkill(options: CodexProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'codex-cli' });
}
