import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type GeminiProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved Threadforge procedure into Gemini CLI's project Agent Skills directory. */
export function installGeminiProjectSkill(options: GeminiProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'gemini-cli' });
}

/** Remove only the unchanged Threadforge procedure installed for Gemini CLI. */
export function uninstallGeminiProjectSkill(options: GeminiProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'gemini-cli' });
}
