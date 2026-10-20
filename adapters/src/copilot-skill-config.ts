import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type CopilotProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install a reviewed Threadforge procedure into GitHub Copilot's project skills directory. */
export function installCopilotProjectSkill(options: CopilotProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'github-copilot' });
}

/** Remove only the unchanged Threadforge procedure installed for GitHub Copilot. */
export function uninstallCopilotProjectSkill(options: CopilotProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'github-copilot' });
}
