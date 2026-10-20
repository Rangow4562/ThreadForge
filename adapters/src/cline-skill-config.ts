import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rmdir, unlink } from 'node:fs/promises';
import path from 'node:path';

const maxSkillBytes = 256 << 10;
const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

export type ProjectSkillHost = 'claude-code' | 'command-code' | 'cline' | 'cursor' | 'devin' | 'opencode' | 'codex-cli' | 'gemini-cli' | 'antigravity' | 'github-copilot' | 'openhands' | 'kimi-code' | 'zcode' | 'deepseek-harness' | 'qoder';

export interface ProjectSkillOptions {
  host: ProjectSkillHost;
  projectRoot: string;
  skillFile: string;
  sha256: string;
}

export interface ProjectSkillResult {
  schema_version: 'threadforge.project-skill.v1';
  host: ProjectSkillHost;
  action: 'installed' | 'unchanged' | 'removed' | 'absent';
  skill_name: string;
  path: string;
  sha256: string;
}

export type ClineProjectSkillResult = ProjectSkillResult;

interface PortableSkill {
  name: string;
  description: string;
  body: string;
  bytes: Buffer;
  sha256: string;
}

export interface ReviewedPortableSkillExport {
  projectRoot: string;
  name: string;
  description: string;
  body: string;
  sha256: string;
}

interface OwnershipMarker {
  schema_version: 'threadforge.project-skill-owner.v1';
  host: ProjectSkillHost | 'agents-skills';
  skill_name: string;
  path: string;
  sha256: string;
}

/**
 * Copy a Threadforge-generated, privacy-reviewed portable procedure into
 * Cline's project skill directory. The caller must pass the SHA-256 printed by
 * `threadforge procedure export-skill`; this detects edits between review and
 * installation. Cline's project MCP/user settings are never modified.
 */
export async function installClineProjectSkill(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<ClineProjectSkillResult> {
  return installProjectSkill({ ...options, host: 'cline' });
}

/** Install the reviewed procedure into the portable project Agent Skills directory for Cursor. */
export async function installProjectSkill(options: ProjectSkillOptions): Promise<ProjectSkillResult> {
  const host = validateHost(options.host);
  const { root, skill } = await resolvePortableSkill(options);
  const skillSegments = skillsDirectorySegments(host);
  const skillsRoot = await ensureDirectories(root, skillSegments);
  const releaseLock = await acquireSkillLock(skillsRoot, skill.name, host);
  try {
    const targetDirectory = await ensureDirectories(root, [...skillSegments, skill.name]);
    const target = path.join(targetDirectory, 'SKILL.md');
    const markerPath = path.join(targetDirectory, '.threadforge-owner.json');
    const existing = await readOptionalRegularFile(target, maxSkillBytes);
    const marker = await readOwnershipMarker(markerPath);
    if (existing || marker) {
      if (!existing || !marker) {
        throw new Error(`project skill '${skill.name}' already exists without a complete Threadforge ownership record; refusing to change it`);
      }
      assertOwned(marker, skill, target, root, host);
      if (!existing.equals(skill.bytes)) throw new Error(`project skill '${skill.name}' already exists with different content; refusing to overwrite it`);
      return result('unchanged', skill, target, root, host);
    }

    let targetWritten = false;
    try {
      await writeExclusive(target, skill.bytes, 0o644);
      targetWritten = true;
      await writeExclusive(markerPath, Buffer.from(`${JSON.stringify(ownerFor(skill, target, root, host))}\n`, 'utf8'), 0o600);
    } catch (error) {
      if (targetWritten) await unlink(target).catch(() => {});
      if (isCode(error, 'EEXIST')) {
        throw new Error(`project skill '${skill.name}' appeared during installation; inspect it before retrying`);
      }
      throw new Error('unable to write the project skill');
    }
    return result('installed', skill, target, root, host);
  } finally {
    await releaseLock();
  }
}

/** Remove only the exact exported bytes named by the user's expected digest. */
export async function uninstallClineProjectSkill(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<ClineProjectSkillResult> {
  return uninstallProjectSkill({ ...options, host: 'cline' });
}

/** Remove only the exact reviewed export installed for its selected host. */
export async function uninstallProjectSkill(options: ProjectSkillOptions): Promise<ProjectSkillResult> {
  const host = validateHost(options.host);
  const { root, skill } = await resolvePortableSkill(options);
  const skillSegments = skillsDirectorySegments(host);
  const targetDirectoryPath = path.join(root, ...skillSegments, skill.name);
  const targetPath = path.join(targetDirectoryPath, 'SKILL.md');
  const skillsRoot = await existingDirectories(root, skillSegments);
  if (!skillsRoot) return result('absent', skill, targetPath, root, host);

  const releaseLock = await acquireSkillLock(skillsRoot, skill.name, host);
  try {
    const skillDirectory = path.join(skillsRoot, skill.name);
    const directoryStat = await lstat(skillDirectory).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!directoryStat) return result('absent', skill, path.join(skillDirectory, 'SKILL.md'), root, host);
    if (!directoryStat.isDirectory() || directoryStat.isSymbolicLink()) {
      throw new Error(`project skill path '${skill.name}' is not a safe directory`);
    }

    const target = path.join(skillDirectory, 'SKILL.md');
    const markerPath = path.join(skillDirectory, '.threadforge-owner.json');
    const existing = await readOptionalRegularFile(target, maxSkillBytes);
    const marker = await readOwnershipMarker(markerPath);
    if (!existing && !marker) return result('absent', skill, target, root, host);
    if (!marker) throw new Error(`project skill '${skill.name}' has no Threadforge ownership record; refusing to remove it`);
    assertOwned(marker, skill, target, root, host);
    if (!existing) {
      await unlink(markerPath);
      return result('removed', skill, target, root, host);
    }
    if (!existing.equals(skill.bytes)) {
      throw new Error(`project skill '${skill.name}' was modified; refusing to remove user-edited content`);
    }
    await unlink(target);
    await unlink(markerPath);
    // Preserve any user-added supporting files and directories.
    await rmdir(skillDirectory).catch(() => {});
    return result('removed', skill, target, root, host);
  } finally {
    await releaseLock();
  }
}

async function resolvePortableSkill(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<{ root: string; skill: PortableSkill }> {
  if (!path.isAbsolute(options.projectRoot)) throw new Error('project root must be an absolute path');
  if (!options.skillFile.trim()) throw new Error('portable skill file path is required');
  if (!/^[a-f0-9]{64}$/.test(options.sha256)) throw new Error('--sha256 must be a lowercase SHA-256 digest');

  const rootInput = path.resolve(options.projectRoot);
  const rootInfo = await lstat(rootInput).catch(() => null);
  if (!rootInfo?.isDirectory() || rootInfo.isSymbolicLink()) {
    throw new Error('project root must be a real directory, not a symbolic link');
  }
  const root = await realpath(rootInput).catch(() => { throw new Error('project root could not be resolved'); });
  const sourceInput = path.isAbsolute(options.skillFile)
    ? path.resolve(options.skillFile)
    : path.resolve(root, options.skillFile);
  const relativeSourcePath = path.relative(root, sourceInput);
  const parts = relativeSourcePath.split(path.sep);
  if (!isWithin(root, sourceInput) || parts.length !== 5 || parts[0] !== '.threadforge' ||
      parts[1] !== 'skills' || !parts[2] || !/^rev-\d{6}$/.test(parts[3] ?? '') || parts[4] !== 'SKILL.md') {
    throw new Error('skill file must be an exported .threadforge/skills/<id>/rev-NNNNNN/SKILL.md artifact in this project');
  }
  await assertNoSymlinkPath(root, sourceInput);
  const fileInfo = await lstat(sourceInput).catch(() => null);
  if (!fileInfo?.isFile() || fileInfo.isSymbolicLink()) throw new Error('portable skill must be a regular file, not a symbolic link');
  if (fileInfo.size > maxSkillBytes) throw new Error('portable skill exceeds the 256 KiB local limit');
  const sourceRealPath = await realpath(sourceInput).catch(() => '');
  if (sourceRealPath !== sourceInput) throw new Error('portable skill path changed during validation');
  const bytes = await readFile(sourceInput);
  const afterRead = await lstat(sourceInput).catch(() => null);
  if (!afterRead?.isFile() || afterRead.isSymbolicLink() || afterRead.size !== fileInfo.size || afterRead.mtimeMs !== fileInfo.mtimeMs) {
    throw new Error('portable skill changed while it was being read');
  }
  const digest = createHash('sha256').update(bytes).digest('hex');
  if (digest !== options.sha256) throw new Error('portable skill SHA-256 does not match the reviewed export digest');

  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  const generated = validateGeneratedSkill(text, parts[2]!, Number(parts[3]!.slice(4)));
  return { root, skill: { ...generated, bytes, sha256: digest } };
}

/** Load the exact, digest-verified portable procedure export for another host-specific renderer. */
export async function loadReviewedPortableSkillExport(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<ReviewedPortableSkillExport> {
  const { root, skill } = await resolvePortableSkill(options);
  return {
    projectRoot: root,
    name: skill.name,
    description: skill.description,
    body: skill.body,
    sha256: skill.sha256,
  };
}

function validateGeneratedSkill(text: string, graphID: string, revision: number): Pick<PortableSkill, 'name' | 'description' | 'body'> {
  const frontmatter = /^---\r?\nname: ("(?:\\.|[^"\\])*")\r?\ndescription: ("(?:\\.|[^"\\])*")\r?\n---\r?\n/.exec(text);
  if (!frontmatter) throw new Error('file is not a generated Threadforge portable SKILL.md');
  let name: unknown;
  let description: unknown;
  try {
    name = JSON.parse(frontmatter[1]!);
    description = JSON.parse(frontmatter[2]!);
  } catch {
    throw new Error('portable skill frontmatter is malformed');
  }
  if (typeof name !== 'string' || name.length > 64 || !/^threadforge-[a-z0-9]+(?:-[a-z0-9]+)*-r\d+$/.test(name)) {
    throw new Error('portable skill name is invalid for project Agent Skills');
  }
  if (typeof description !== 'string' || description.length === 0 || description.length > 1024) {
    throw new Error('portable skill description is invalid for project Agent Skills');
  }
  const identity = /^> Source procedure: (\S+) revision (\d+)$/m.exec(text);
  if (!identity || identity[1] !== graphID || Number(identity[2]) !== revision || !name.endsWith(`-r${revision}`)) {
    throw new Error('portable skill identity does not match its exported project path');
  }
  if (!text.includes(approvalNotice) || !text.includes(omissionNotice)) {
    throw new Error('portable skill is missing the Threadforge advisory/privacy notices');
  }
  return { name, description, body: text.slice(frontmatter[0].length) };
}

async function ensureDirectories(root: string, segments: string[]): Promise<string> {
  let current = root;
  for (const segment of segments) {
    current = path.join(current, segment);
    try {
      await mkdir(current, { mode: 0o755 });
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create the project skills directory');
    }
    const info = await lstat(current).catch(() => null);
    if (!info?.isDirectory() || info.isSymbolicLink()) {
      throw new Error('project skill path contains a non-directory or symbolic link');
    }
    if (await realpath(current).catch(() => '') !== current) {
      throw new Error('project skill path changed during validation');
    }
  }
  return current;
}

async function existingDirectories(root: string, segments: string[]): Promise<string | undefined> {
  let current = root;
  for (const segment of segments) {
    current = path.join(current, segment);
    const info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('project skill path contains a non-directory or symbolic link');
    if (await realpath(current).catch(() => '') !== current) throw new Error('project skill path changed during validation');
  }
  return current;
}

function validateHost(host: ProjectSkillHost): ProjectSkillHost {
  if (host !== 'claude-code' && host !== 'command-code' && host !== 'cline' && host !== 'cursor' && host !== 'devin' && host !== 'opencode' && host !== 'codex-cli' && host !== 'gemini-cli' && host !== 'antigravity' && host !== 'github-copilot' && host !== 'openhands' && host !== 'kimi-code' && host !== 'zcode' && host !== 'deepseek-harness' && host !== 'qoder') {
    throw new Error('unsupported project skill host');
  }
  return host;
}

function skillsDirectorySegments(host: ProjectSkillHost): string[] {
  if (host === 'claude-code') return ['.claude', 'skills'];
  if (host === 'command-code') return ['.commandcode', 'skills'];
  if (host === 'cline') return ['.cline', 'skills'];
  if (host === 'github-copilot') return ['.github', 'skills'];
  if (host === 'kimi-code') return ['.kimi-code', 'skills'];
  if (host === 'deepseek-harness') return ['.dsh', 'skills'];
  if (host === 'qoder') return ['.qoder', 'skills'];
  return ['.agents', 'skills'];
}

async function assertNoSymlinkPath(root: string, target: string): Promise<void> {
  const relative = path.relative(root, target);
  if (!isWithin(root, target)) throw new Error('portable skill path escapes the project root');
  let current = root;
  for (const segment of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, segment);
    const info = await lstat(current).catch(() => null);
    if (!info || info.isSymbolicLink()) throw new Error('portable skill path contains a missing or symbolic-link component');
  }
}

async function readOptionalRegularFile(filePath: string, byteLimit: number): Promise<Buffer | undefined> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return undefined;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('project skill target must be a regular file, not a symbolic link');
  if (info.size > byteLimit) throw new Error('existing project skill exceeds the local size limit');
  const bytes = await readFile(filePath);
  const afterRead = await lstat(filePath).catch(() => null);
  if (!afterRead?.isFile() || afterRead.isSymbolicLink() || afterRead.size !== info.size || afterRead.mtimeMs !== info.mtimeMs) {
    throw new Error('project skill target changed while it was being read');
  }
  return bytes;
}

async function readOwnershipMarker(filePath: string): Promise<OwnershipMarker | undefined> {
  const bytes = await readOptionalRegularFile(filePath, 4096);
  if (!bytes) return undefined;
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new Error('Threadforge project skill ownership record is malformed'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Threadforge project skill ownership record is malformed');
  const marker = value as Record<string, unknown>;
  if (Object.keys(marker).sort().join(',') !== 'host,path,schema_version,sha256,skill_name' ||
      marker.schema_version !== 'threadforge.project-skill-owner.v1' ||
      (marker.host !== 'claude-code' && marker.host !== 'command-code' && marker.host !== 'cline' && marker.host !== 'cursor' && marker.host !== 'devin' && marker.host !== 'opencode' && marker.host !== 'codex-cli' && marker.host !== 'gemini-cli' && marker.host !== 'antigravity' && marker.host !== 'github-copilot' && marker.host !== 'openhands' && marker.host !== 'kimi-code' && marker.host !== 'zcode' && marker.host !== 'deepseek-harness' && marker.host !== 'qoder' && marker.host !== 'agents-skills') || typeof marker.skill_name !== 'string' ||
      typeof marker.path !== 'string' || typeof marker.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.sha256)) {
    throw new Error('Threadforge project skill ownership record is malformed');
  }
  return marker as unknown as OwnershipMarker;
}

function ownerFor(skill: PortableSkill, target: string, root: string, host: ProjectSkillHost): OwnershipMarker {
  return {
    schema_version: 'threadforge.project-skill-owner.v1',
    host: ownershipHost(host),
    skill_name: skill.name,
    path: path.relative(root, target).split(path.sep).join('/'),
    sha256: skill.sha256,
  };
}

function assertOwned(marker: OwnershipMarker, skill: PortableSkill, target: string, root: string, host: ProjectSkillHost): void {
  const expected = ownerFor(skill, target, root, host);
  if (ownershipHost(marker.host) !== expected.host || marker.skill_name !== expected.skill_name || marker.path !== expected.path || marker.sha256 !== expected.sha256) {
    throw new Error(`project skill '${skill.name}' ownership record does not match the reviewed export`);
  }
}

async function writeExclusive(filePath: string, bytes: Buffer, mode: number): Promise<void> {
  let handle;
  let created = false;
  try {
    handle = await open(filePath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    created = true;
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
  } catch (error) {
    await handle?.close().catch(() => {});
    if (created) await unlink(filePath).catch(() => {});
    throw error;
  }
}

async function acquireSkillLock(skillsRoot: string, name: string, host: ProjectSkillHost): Promise<() => Promise<void>> {
  const lockPath = path.join(skillsRoot, `.threadforge-${ownershipHost(host)}-${name}.lock`);
  let handle;
  try {
    handle = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('project skill is locked by another installer or a stale lock; inspect it before retrying');
    throw new Error('unable to lock the project skill');
  }
  return async () => {
    await handle!.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  };
}

function ownershipHost(host: ProjectSkillHost | OwnershipMarker['host']): OwnershipMarker['host'] {
  return host === 'claude-code' || host === 'command-code' || host === 'cline' || host === 'github-copilot' || host === 'kimi-code' || host === 'deepseek-harness' || host === 'qoder' ? host : 'agents-skills';
}

function result(action: ProjectSkillResult['action'], skill: PortableSkill, target: string, root: string, host: ProjectSkillHost): ProjectSkillResult {
  return {
    schema_version: 'threadforge.project-skill.v1',
    host,
    action,
    skill_name: skill.name,
    path: path.relative(root, target).split(path.sep).join('/'),
    sha256: skill.sha256,
  };
}

function isWithin(parent: string, child: string): boolean {
  const relative = path.relative(parent, child);
  return relative !== '' && relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
