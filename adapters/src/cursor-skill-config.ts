import { installProjectSkill, uninstallProjectSkill, type ProjectSkillOptions, type ProjectSkillResult } from './project-skill-config.ts';

type CursorProjectSkillOptions = Omit<ProjectSkillOptions, 'host'>;

/** Install an approved portable procedure into Cursor's portable project Agent Skills directory. */
export function installCursorProjectSkill(options: CursorProjectSkillOptions): Promise<ProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'cursor' });
}

/** Remove only the unchanged Threadforge procedure installed for Cursor. */
export function uninstallCursorProjectSkill(options: CursorProjectSkillOptions): Promise<ProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'cursor' });
}
