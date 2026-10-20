import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, unlink } from 'node:fs/promises';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
import { loadReviewedPortableSkillExport } from './cline-skill-config.ts';

const maxCommandBytes = 256 << 10;

export interface QoderProjectCommandOptions {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}

export interface QoderProjectCommandResult {
  schema_version: 'threadforge.qoder-project-command.v1';
  host: 'qoder-cli';
  action: 'installed' | 'unchanged' | 'removed' | 'absent';
  command_name: string;
  path: string;
  source_sha256: string;
  command_sha256: string;
}

interface OwnerMarker {
  schema_version: 'threadforge.qoder-command-owner.v1';
  command_name: string;
  path: string;
  source_sha256: string;
  command_sha256: string;
}

interface RenderedCommand {
  name: string;
  bytes: Buffer;
  sha256: string;
  sourceSha256: string;
}

/** Install a reviewed Threadforge procedure as an explicit Qoder project slash command. */
export async function installQoderProjectCommand(options: QoderProjectCommandOptions): Promise<QoderProjectCommandResult> {
  const { projectRoot, command } = await resolveCommand(options);
  const commandsRoot = await ensureCommandDirectory(projectRoot);
  await refuseSkillDirectoryMode(commandsRoot);
  const releaseLock = await acquireLock(commandsRoot, command.name);
  try {
    await refuseSkillDirectoryMode(commandsRoot);
    const target = path.join(commandsRoot, `${command.name}.md`);
    const markerPath = ownerPath(commandsRoot, command.name);
    const existing = await readOptionalRegularFile(target, maxCommandBytes);
    const marker = await readOwner(markerPath);
    if (existing || marker) {
      if (!existing || !marker) {
        throw new Error(`Qoder command '${command.name}' exists without a complete Threadforge ownership record; refusing to change it`);
      }
      assertOwned(marker, command, target, projectRoot);
      if (!existing.equals(command.bytes)) {
        throw new Error(`Qoder command '${command.name}' has different content; refusing to overwrite it`);
      }
      return result('unchanged', command, target, projectRoot);
    }

    let targetWritten = false;
    try {
      await writeExclusive(target, command.bytes, 0o644);
      targetWritten = true;
      await writeExclusive(markerPath, ownerBytes(command, target, projectRoot), 0o600);
    } catch {
      if (targetWritten) {
        const current = await readOptionalRegularFile(target, maxCommandBytes).catch(() => undefined);
        if (current?.equals(command.bytes)) await unlink(target).catch(() => {});
      }
      throw new Error('unable to install the Qoder project command; inspect the target and retry');
    }
    return result('installed', command, target, projectRoot);
  } finally {
    await releaseLock();
  }
}

/** Remove only the unchanged Threadforge command generated from the supplied reviewed export. */
export async function uninstallQoderProjectCommand(options: QoderProjectCommandOptions): Promise<QoderProjectCommandResult> {
  const { projectRoot, command } = await resolveCommand(options);
  const commandsRoot = await existingCommandDirectory(projectRoot);
  if (!commandsRoot) return result('absent', command, path.join(projectRoot, '.qoder', 'commands', `${command.name}.md`), projectRoot);
  const releaseLock = await acquireLock(commandsRoot, command.name);
  try {
    const target = path.join(commandsRoot, `${command.name}.md`);
    const markerPath = ownerPath(commandsRoot, command.name);
    const existing = await readOptionalRegularFile(target, maxCommandBytes);
    const marker = await readOwner(markerPath);
    if (!existing && !marker) return result('absent', command, target, projectRoot);
    if (!marker) throw new Error(`Qoder command '${command.name}' has no Threadforge ownership record; refusing to remove it`);
    assertOwned(marker, command, target, projectRoot);
    if (!existing) {
      await unlink(markerPath);
      return result('removed', command, target, projectRoot);
    }
    if (!existing.equals(command.bytes)) {
      throw new Error(`Qoder command '${command.name}' was modified; refusing to remove user-edited content`);
    }
    await unlink(target);
    await unlink(markerPath);
    return result('removed', command, target, projectRoot);
  } finally {
    await releaseLock();
  }
}

async function resolveCommand(options: QoderProjectCommandOptions): Promise<{ projectRoot: string; command: RenderedCommand }> {
  const skill = await loadReviewedPortableSkillExport(options);
  const name = skill.name;
  const bytes = Buffer.from(
    `---\nname: ${JSON.stringify(name)}\ndescription: ${JSON.stringify(skill.description)}\n---\n${skill.body}`,
    'utf8',
  );
  if (bytes.length > maxCommandBytes) throw new Error('generated Qoder command exceeds the 256 KiB limit');
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  return { projectRoot: skill.projectRoot, command: { name, bytes, sha256, sourceSha256: skill.sha256 } };
}

async function ensureCommandDirectory(root: string): Promise<string> {
  let current = root;
  for (const segment of ['.qoder', 'commands']) {
    current = path.join(current, segment);
    try { await mkdir(current, { mode: 0o755 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create the Qoder project commands directory'); }
    const info = await lstat(current).catch(() => null);
    const canonical = await realpath(current).catch(() => '');
    if (!info?.isDirectory() || info.isSymbolicLink() || !canonical || !await isSameRealPathWithoutLinks(canonical, current)) {
      throw new Error('Qoder project commands path contains a non-directory or symbolic link');
    }
  }
  return current;
}

async function existingCommandDirectory(root: string): Promise<string | undefined> {
  let current = root;
  for (const segment of ['.qoder', 'commands']) {
    current = path.join(current, segment);
    const info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return undefined;
    const canonical = await realpath(current).catch(() => '');
    if (!info.isDirectory() || info.isSymbolicLink() || !canonical || !await isSameRealPathWithoutLinks(canonical, current)) {
      throw new Error('Qoder project commands path contains a non-directory or symbolic link');
    }
  }
  return current;
}

async function refuseSkillDirectoryMode(commandsRoot: string): Promise<void> {
  const skillPath = path.join(commandsRoot, 'SKILL.md');
  const info = await lstat(skillPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (info) throw new Error('Qoder treats a commands directory containing SKILL.md as a single Skill; refusing to add an ignored slash command');
}

async function readOptionalRegularFile(filePath: string, byteLimit: number): Promise<Buffer | undefined> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return undefined;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Qoder command target must be a regular file, not a symbolic link');
  if (info.size > byteLimit) throw new Error('existing Qoder command exceeds the local size limit');
  const bytes = await readFile(filePath);
  const afterRead = await lstat(filePath).catch(() => null);
  if (!afterRead?.isFile() || afterRead.isSymbolicLink() || afterRead.size !== info.size || afterRead.mtimeMs !== info.mtimeMs) {
    throw new Error('Qoder command target changed while it was being read');
  }
  return bytes;
}

async function readOwner(filePath: string): Promise<OwnerMarker | undefined> {
  const bytes = await readOptionalRegularFile(filePath, 4096);
  if (!bytes) return undefined;
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new Error('Threadforge Qoder command ownership record is malformed'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Threadforge Qoder command ownership record is malformed');
  const marker = value as Record<string, unknown>;
  if (Object.keys(marker).sort().join(',') !== 'command_name,command_sha256,path,schema_version,source_sha256' ||
      marker.schema_version !== 'threadforge.qoder-command-owner.v1' || typeof marker.command_name !== 'string' ||
      typeof marker.path !== 'string' || typeof marker.source_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.source_sha256) ||
      typeof marker.command_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.command_sha256)) {
    throw new Error('Threadforge Qoder command ownership record is malformed');
  }
  return marker as unknown as OwnerMarker;
}

function ownerPath(commandsRoot: string, name: string): string {
  return path.join(commandsRoot, `.threadforge-${name}.owner.json`);
}

function ownerBytes(command: RenderedCommand, target: string, root: string): Buffer {
  const marker: OwnerMarker = {
    schema_version: 'threadforge.qoder-command-owner.v1',
    command_name: command.name,
    path: relativePath(root, target),
    source_sha256: command.sourceSha256,
    command_sha256: command.sha256,
  };
  return Buffer.from(`${JSON.stringify(marker)}\n`, 'utf8');
}

function assertOwned(marker: OwnerMarker, command: RenderedCommand, target: string, root: string): void {
  const expected = {
    command_name: command.name,
    path: relativePath(root, target),
    source_sha256: command.sourceSha256,
    command_sha256: command.sha256,
  };
  if (marker.schema_version !== 'threadforge.qoder-command-owner.v1' || marker.command_name !== expected.command_name ||
      marker.path !== expected.path || marker.source_sha256 !== expected.source_sha256 || marker.command_sha256 !== expected.command_sha256) {
    throw new Error(`Qoder command '${command.name}' ownership record does not match the reviewed export`);
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

async function acquireLock(commandsRoot: string, name: string): Promise<() => Promise<void>> {
  const lockPath = path.join(commandsRoot, `.threadforge-${name}.lock`);
  let handle;
  try { handle = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Qoder command is locked by another installer or a stale lock; inspect before retrying');
    throw new Error('unable to lock the Qoder project command');
  }
  return async () => {
    await handle!.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  };
}

function result(action: QoderProjectCommandResult['action'], command: RenderedCommand, target: string, root: string): QoderProjectCommandResult {
  return {
    schema_version: 'threadforge.qoder-project-command.v1',
    host: 'qoder-cli',
    action,
    command_name: command.name,
    path: relativePath(root, target),
    source_sha256: command.sourceSha256,
    command_sha256: command.sha256,
  };
}

function relativePath(root: string, target: string): string {
  return path.relative(root, target).split(path.sep).join('/');
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
