import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { isMap, isScalar, isSeq, parseDocument, type YAMLMap, type YAMLSeq } from 'yaml';
import { verifyInstalledProjectSkill } from './cline-skill-config.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const maxAiderConfigBytes = 1 << 20;
const maxOwnershipBytes = 4096;

export type AiderAutoReadResult = {
  schema_version: 'threadforge.aider-auto-read.v1';
  action: 'installed' | 'unchanged' | 'already_configured' | 'removed' | 'absent';
  config_path: '.aider.conf.yml';
  skill_path: string;
  skill_sha256: string;
  automatic_read_enabled: boolean;
  managed: boolean;
  guidance_only: true;
  execution_authorized: false;
};

type Snapshot = { bytes?: Buffer; mode: number };
type AiderDocument = ReturnType<typeof parseDocument>;
type AiderMap = YAMLMap<unknown, unknown>;
type AiderSequence = YAMLSeq<unknown>;
type OwnershipRecord = {
  schema_version: 'threadforge.aider-auto-read-owner.v1';
  host: 'aider';
  skill_path: string;
  skill_sha256: string;
  read_key_created: boolean;
  config_created: boolean;
};
type OwnershipSnapshot = { record?: OwnershipRecord; bytes?: Buffer; mode: number };

/** Explicitly add one exact, Threadforge-owned procedure to the project's Aider read-only context list. */
export async function installAiderAutoRead(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<AiderAutoReadResult> {
  const root = await canonicalProjectRoot(options.projectRoot);
  return withProjectLock(root, async () => {
    const skill = await verifyInstalledProjectSkill({ ...options, host: 'aider' });
    const skillPath = validateSkillPath(skill.path);
    const configPath = path.join(root, '.aider.conf.yml');
    const snapshot = await readSnapshot(configPath);
    const parsed = parseAiderConfig(snapshot.bytes);
    const read = getReadSequence(parsed.root);
    const existingStatePath = await ownershipFilePath(root, skillPath, false);
    const existingOwnership = existingStatePath ? await readOwnership(existingStatePath) : undefined;
    const existingState = existingOwnership?.record;
    if (existingState && (existingState.skill_path !== skillPath || existingState.skill_sha256 !== options.sha256)) {
      throw new Error('Aider auto-read ownership record does not match this skill export');
    }
    const alreadyPresent = read.sequence?.items.some((item) => scalarString(item) === skillPath) ?? false;
    if (!existingState && alreadyPresent) return result('already_configured', skillPath, options.sha256, true, false);
    if (existingState && alreadyPresent) return result('unchanged', skillPath, options.sha256, true, true);

    const statePath = existingStatePath ?? await ownershipFilePath(root, skillPath, true);
    if (!statePath) throw new Error('unable to create Aider ownership directory');
    const ownershipSnapshot = existingOwnership ?? { mode: 0o600 };
    const readKeyCreated = Boolean(existingState?.read_key_created || !read.sequence);
    const sequence = read.sequence ?? parsed.document.createNode([]) as AiderSequence;
    if (!read.sequence) parsed.root.set('read', sequence);
    sequence.add(parsed.document.createNode(skillPath));
    const nextBytes = renderAiderConfig(parsed.document, snapshot.bytes, parsed.bom);
    await verifyInstalledProjectSkill({ ...options, host: 'aider' });
    const nextState: OwnershipRecord = {
      schema_version: 'threadforge.aider-auto-read-owner.v1',
      host: 'aider',
      skill_path: skillPath,
      skill_sha256: options.sha256,
      read_key_created: readKeyCreated,
      config_created: existingState?.config_created ?? snapshot.bytes === undefined,
    };
    if (existingState) await replaceOwnership(statePath, ownershipSnapshot, nextState);
    else await writeOwnershipExclusive(statePath, nextState);
    try {
      await atomicReplace(configPath, snapshot, nextBytes);
    } catch (error) {
      if (!existingState) await unlink(statePath).catch(() => {});
      throw error;
    }
    return result('installed', skillPath, options.sha256, true, true);
  });
}

/** Remove only the exact Threadforge-marked entry; leave user-owned Aider read paths untouched. */
export async function uninstallAiderAutoRead(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<AiderAutoReadResult> {
  const root = await canonicalProjectRoot(options.projectRoot);
  return withProjectLock(root, async () => {
    const skill = await verifyInstalledProjectSkill({ ...options, host: 'aider' });
    const skillPath = validateSkillPath(skill.path);
    const configPath = path.join(root, '.aider.conf.yml');
    const snapshot = await readSnapshot(configPath);
    const statePath = await ownershipFilePath(root, skillPath, false);
    const ownershipSnapshot = statePath ? await readOwnership(statePath) : undefined;
    const state = ownershipSnapshot?.record;
    if (!state) {
      const userConfigured = snapshot.bytes !== undefined &&
        (getReadSequence(parseAiderConfig(snapshot.bytes).root).sequence?.items.some((item) => scalarString(item) === skillPath) ?? false);
      return result(userConfigured ? 'already_configured' : 'absent', skillPath, options.sha256, userConfigured, false);
    }
    if (state.skill_path !== skillPath || state.skill_sha256 !== options.sha256) {
      throw new Error('Aider auto-read ownership record does not match this skill export');
    }
    if (!snapshot.bytes) {
      await unlink(statePath!);
      return result('absent', skillPath, options.sha256, false, false);
    }

    const parsed = parseAiderConfig(snapshot.bytes);
    const read = getReadSequence(parsed.root);
    if (!read.sequence) {
      await unlink(statePath!);
      return result('absent', skillPath, options.sha256, false, false);
    }
    let index = -1;
    for (let itemIndex = 0; itemIndex < read.sequence.items.length; itemIndex++) {
      if (scalarString(read.sequence.items[itemIndex]) === skillPath) index = itemIndex;
    }
    if (index < 0) {
      await unlink(statePath!);
      return result('absent', skillPath, options.sha256, false, false);
    }
    read.sequence.items.splice(index, 1);
    const stillEnabled = read.sequence.items.some((item) => scalarString(item) === skillPath);
    if (read.sequence.items.length === 0 && state.read_key_created) parsed.root.delete('read');
    const nextBytes = renderAiderConfig(parsed.document, snapshot.bytes, parsed.bom);
    await verifyInstalledProjectSkill({ ...options, host: 'aider' });
    if (state.config_created && parsed.root.items.length === 0 && !documentHasComments(parsed.document)) {
      await atomicRemove(configPath, snapshot);
    } else {
      await atomicReplace(configPath, snapshot, nextBytes);
    }
    await unlink(statePath!).catch(() => {});
    return result('removed', skillPath, options.sha256, stillEnabled, false);
  });
}

export async function hasAiderAutoReadState(options: {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}): Promise<boolean> {
  const root = await canonicalProjectRoot(options.projectRoot);
  const skill = await verifyInstalledProjectSkill({ ...options, host: 'aider' });
  const skillPath = validateSkillPath(skill.path);
  const statePath = await ownershipFilePath(root, skillPath, false);
  if (!statePath) return false;
  const state = await readOwnership(statePath);
  if (!state.record) return false;
  if (state.record.skill_path !== skillPath || state.record.skill_sha256 !== options.sha256) {
    throw new Error('Aider auto-read ownership record does not match this skill export');
  }
  return true;
}

function result(
  action: AiderAutoReadResult['action'],
  skillPath: string,
  sha256: string,
  enabled: boolean,
  managed: boolean,
): AiderAutoReadResult {
  return {
    schema_version: 'threadforge.aider-auto-read.v1',
    action,
    config_path: '.aider.conf.yml',
    skill_path: skillPath,
    skill_sha256: sha256,
    automatic_read_enabled: enabled,
    managed,
    guidance_only: true,
    execution_authorized: false,
  };
}

async function canonicalProjectRoot(input: string): Promise<string> {
  if (!path.isAbsolute(input)) throw new Error('project root must be an absolute path');
  const requested = path.resolve(input);
  const info = await lstat(requested).catch(() => null);
  if (!info?.isDirectory() || info.isSymbolicLink()) throw new Error('project root must be a real directory, not a symbolic link');
  const canonical = await realpath(requested).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, requested)) {
    throw new Error('project root must not traverse a symbolic link');
  }
  return canonical;
}

async function withProjectLock<T>(root: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(root, '.threadforge-aider-auto-read.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Aider project config is locked by another writer or a stale lock');
    throw new Error('unable to lock Aider project config');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readSnapshot(filePath: string): Promise<Snapshot> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return { mode: 0o600 };
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Aider project config must be a regular file, not a symbolic link');
  if (info.size > maxAiderConfigBytes) throw new Error('Aider project config exceeds the 1 MiB local limit');
  const bytes = await readFile(filePath);
  const after = await lstat(filePath).catch(() => null);
  if (!after?.isFile() || after.isSymbolicLink() || after.size !== info.size || after.mtimeMs !== info.mtimeMs) {
    throw new Error('Aider project config changed while it was being read');
  }
  return { bytes, mode: info.mode & 0o777 };
}

function parseAiderConfig(bytes?: Buffer): { document: AiderDocument; root: AiderMap; bom: boolean } {
  let text = '';
  let bom = false;
  if (bytes) {
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
    catch { throw new Error('Aider project config is not valid UTF-8; no changes were made'); }
    bom = text.startsWith('\ufeff');
    if (bom) text = text.slice(1);
    if (text.includes('\r\n') && /(^|[^\r])\n/.test(text)) {
      throw new Error('Aider project config has mixed line endings; normalize it before Threadforge edits');
    }
  }
  const document = parseDocument(text, { version: '1.2', uniqueKeys: true });
  if (document.errors.length > 0 || document.warnings.length > 0) {
    throw new Error('Aider project config is ambiguous or invalid YAML; no changes were made');
  }
  if (document.contents === null) {
    document.contents = document.createNode({}) as NonNullable<typeof document.contents>;
  }
  if (!isMap(document.contents)) throw new Error('Aider project config root must be a YAML mapping; no changes were made');
  return { document, root: document.contents as AiderMap, bom };
}

function getReadSequence(root: AiderMap): { pair?: AiderMap['items'][number]; sequence?: AiderSequence } {
  const pair = root.items.find((candidate) => scalarString(candidate.key) === 'read');
  if (!pair) return {};
  if (!isSeq(pair.value)) throw new Error('Aider project config read setting must be a YAML list; no changes were made');
  for (const item of pair.value.items) {
    if (!isScalar(item) || typeof item.value !== 'string') {
      throw new Error('Aider project config read list must contain only string paths; no changes were made');
    }
  }
  return { pair, sequence: pair.value as AiderSequence };
}

function scalarString(value: unknown): string | undefined {
  return isScalar(value) && typeof value.value === 'string' ? value.value : undefined;
}

function validateSkillPath(value: string): string {
  const normalized = value.replaceAll('\\', '/');
  if (!/^\.agents\/skills\/[a-z0-9]+(?:-[a-z0-9]+)*\/SKILL\.md$/.test(normalized)) {
    throw new Error('Aider read target must be the exact project-relative Threadforge skill path');
  }
  return normalized;
}

function renderAiderConfig(document: AiderDocument, original: Buffer | undefined, bom: boolean): Buffer {
  let output = document.toString({ lineWidth: 0, indent: 2 });
  const hadFinalNewline = original === undefined || /\r?\n$/.test(original.toString('utf8'));
  if (hadFinalNewline && !output.endsWith('\n')) output += '\n';
  if (!hadFinalNewline && output.endsWith('\n')) output = output.slice(0, -1);
  if (original?.toString('utf8').includes('\r\n')) output = output.replaceAll('\n', '\r\n');
  if (bom) output = `\ufeff${output}`;
  const bytes = Buffer.from(output, 'utf8');
  if (bytes.length > maxAiderConfigBytes) throw new Error('updated Aider project config exceeds the 1 MiB local limit');
  return bytes;
}

async function ownershipFilePath(root: string, skillPath: string, create: boolean): Promise<string | undefined> {
  const skillName = skillPath.split('/')[2];
  if (!skillName || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(skillName)) {
    throw new Error('Aider skill identity is invalid for a project ownership record');
  }
  let current = root;
  for (const segment of ['.threadforge', 'aider-auto-read']) {
    current = path.join(current, segment);
    let info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info && create) {
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => {
        if (!isCode(error, 'EEXIST')) throw new Error('unable to create Aider project ownership directory');
      });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Aider ownership path contains a symbolic link or non-directory');
    const canonical = await realpath(current).catch(() => '');
    if (!canonical || !await isSameRealPathWithoutLinks(canonical, current)) {
      throw new Error('Aider ownership path changed during validation');
    }
  }
  return path.join(current, `${skillName}.json`);
}

async function readOwnership(filePath: string): Promise<OwnershipSnapshot> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return { mode: 0o600 };
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Aider ownership record must be a regular file, not a symbolic link');
  if (info.size > maxOwnershipBytes) throw new Error('Aider ownership record exceeds the local size limit');
  const bytes = await readFile(filePath);
  const after = await lstat(filePath).catch(() => null);
  if (!after?.isFile() || after.isSymbolicLink() || after.size !== info.size || after.mtimeMs !== info.mtimeMs) {
    throw new Error('Aider ownership record changed while it was being read');
  }
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new Error('Aider ownership record is malformed'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Aider ownership record is malformed');
  const record = value as Record<string, unknown>;
  if (Object.keys(record).sort().join(',') !== 'config_created,host,read_key_created,schema_version,skill_path,skill_sha256' ||
      record.schema_version !== 'threadforge.aider-auto-read-owner.v1' || record.host !== 'aider' ||
      typeof record.skill_path !== 'string' || !/^\.agents\/skills\/[a-z0-9]+(?:-[a-z0-9]+)*\/SKILL\.md$/.test(record.skill_path) ||
      typeof record.skill_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(record.skill_sha256) ||
      typeof record.read_key_created !== 'boolean' || typeof record.config_created !== 'boolean') {
    throw new Error('Aider ownership record is malformed');
  }
  return { record: record as OwnershipRecord, bytes, mode: info.mode & 0o777 };
}

async function writeOwnershipExclusive(filePath: string, record: OwnershipRecord): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(record)}\n`, 'utf8');
  if (bytes.length > maxOwnershipBytes) throw new Error('Aider ownership record exceeds the local size limit');
  let handle;
  try {
    handle = await open(filePath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
  } catch (error) {
    await handle?.close().catch(() => {});
    await unlink(filePath).catch(() => {});
    if (isCode(error, 'EEXIST')) throw new Error('Aider ownership record appeared during installation; inspect it before retrying');
    throw new Error('unable to write Aider ownership record');
  }
  await handle?.close();
}

async function replaceOwnership(filePath: string, expected: OwnershipSnapshot, record: OwnershipRecord): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(record)}\n`, 'utf8');
  if (bytes.length > maxOwnershipBytes) throw new Error('Aider ownership record exceeds the local size limit');
  await atomicReplace(filePath, expected, bytes);
}

function documentHasComments(document: AiderDocument): boolean {
  const value = document as unknown as { comment?: string | null; commentBefore?: string | null };
  return Boolean(value.comment?.trim() || value.commentBefore?.trim());
}

async function atomicRemove(filePath: string, expected: Snapshot): Promise<void> {
  const current = await readSnapshot(filePath);
  if (!sameSnapshot(expected, current)) throw new Error('Aider project config changed before cleanup; no changes were made');
  try { await unlink(filePath); }
  catch { throw new Error('unable to remove the empty Threadforge-created Aider config'); }
}

async function atomicReplace(filePath: string, expected: Snapshot, bytes: Buffer): Promise<void> {
  const current = await readSnapshot(filePath);
  if (!sameSnapshot(expected, current)) throw new Error('Aider project config changed before the update; no changes were made');
  const temporary = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    handle = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, expected.mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    const beforeRename = await readSnapshot(filePath);
    if (!sameSnapshot(expected, beforeRename)) throw new Error('Aider project config changed during the update; no changes were made');
    await rename(temporary, filePath);
  } catch (error) {
    await handle?.close().catch(() => {});
    await unlink(temporary).catch(() => {});
    if (error instanceof Error && error.message.startsWith('Aider project config changed')) throw error;
    throw new Error('unable to atomically update Aider project config');
  }
}

function sameSnapshot(left: Snapshot, right: Snapshot): boolean {
  if (left.bytes === undefined || right.bytes === undefined) return left.bytes === right.bytes;
  return left.bytes.equals(right.bytes);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
