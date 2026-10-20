import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type DeepSeekHarnessProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into DeepSeek Harness' project-local skill directory. */
export function installDeepSeekHarnessProjectSkill(options: DeepSeekHarnessProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'deepseek-harness' });
}

/** Remove only the unchanged Threadforge procedure installed for DeepSeek Harness. */
export function uninstallDeepSeekHarnessProjectSkill(options: DeepSeekHarnessProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'deepseek-harness' });
}
