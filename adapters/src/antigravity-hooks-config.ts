import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { antigravityManagedMarker } from './antigravity-hook.ts';

export type AntigravityHookScope = 'user' | 'project';
export type AntigravityHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

export const antigravityMemoryManagedMarker = '--threadforge-memory=antigravity-v1';
const antigravityMemoryRunnerMarker = '--threadforge-managed-antigravity-memory=v1';

export interface AntigravityHookConfigOptions {
  command: string;
  scope?: AntigravityHookScope;
  projectRoot?: string;
  homeDir?: string;
}

const configKey = 'threadforge-endpoint';
const memoryConfigKey = 'threadforge-approved-memory';
const lockName = '.threadforge-antigravity-hooks.lock';
const maxConfigBytes = 8 << 20;
const eventTimeouts: Record<string, number> = {
  PreInvocation: 30,
  PostToolUse: 10,
  PostInvocation: 30,
  Stop: 30,
};
const groupedEvents = new Set(['PostToolUse']);
const groupedHookEvents = new Set(['PreToolUse', 'PostToolUse']);
const managedEvents = new Set([...Object.keys(eventTimeouts), 'PreToolUse', 'UserPromptSubmit']);

type JsonObject = Record<string, unknown>;
interface FileSnapshot {
  content: string;
  identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
}

/** Installs observational hooks into Antigravity's user or project hook file. */
export async function installAntigravityHooks(options: AntigravityHookConfigOptions): Promise<AntigravityHookChange> {
  validateCommand(options.command);
  const scope = options.scope ?? 'user';
  const filePath = await configPath(scope, options.projectRoot, options.homeDir, true);
  if (!filePath) throw new Error('unable to resolve Antigravity hook configuration path');
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const snapshot = read.snapshot;
    const config = read.value;
    const oldBlock = config[configKey];
    if (oldBlock !== undefined && !isRecord(oldBlock)) throw new Error('Antigravity Threadforge hook block must be an object; no changes were made');
    const block = (oldBlock ?? {}) as JsonObject;
    const cleaned = removeManagedAntigravityHooks(block);
    if (oldBlock !== undefined && !cleaned.found && Object.keys(cleaned.value).length > 0) {
      throw new Error('Antigravity Threadforge hook block already exists without Threadforge ownership; no changes were made');
    }

    const nextBlock: JsonObject = { ...cleaned.value };
    for (const [event, timeout] of Object.entries(eventTimeouts)) {
      const command = `${options.command} --hook-event=${event}`;
      const ref = { type: 'command', command, timeout };
      if (groupedEvents.has(event)) {
        const current = readEventGroups(nextBlock, event);
        nextBlock[event] = [...current, { matcher: '*', hooks: [ref] }];
      } else {
        const current = readDirectEventHooks(nextBlock, event);
        nextBlock[event] = [...current, ref];
      }
    }
    const nextConfig: JsonObject = { ...config, [configKey]: nextBlock };
    if (deepEqual(config, nextConfig)) return 'unchanged';
    await atomicWrite(filePath, snapshot, nextConfig);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only marked Threadforge command refs and retains the host config file. */
export async function removeAntigravityHooks(options: Omit<AntigravityHookConfigOptions, 'command'> = {}): Promise<AntigravityHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await configPath(scope, options.projectRoot, options.homeDir, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const snapshot = read.snapshot;
    const config = read.value;
    const block = config[configKey];
    if (block === undefined) return 'absent';
    if (!isRecord(block)) throw new Error('Antigravity Threadforge hook block is malformed; no changes were made');
    const cleaned = removeManagedAntigravityHooks(block);
    if (!cleaned.found) {
      if (Object.keys(block).length > 0) throw new Error('Antigravity Threadforge hook block has no intact ownership marker; inspect it before removal');
      return 'absent';
    }
    const nextConfig: JsonObject = { ...config };
    if (Object.keys(cleaned.value).length === 0) delete nextConfig[configKey];
    else nextConfig[configKey] = cleaned.value;
    await atomicWrite(filePath, snapshot, nextConfig);
    return 'removed';
  });
}

/** Installs a separate PreInvocation memory hook in Antigravity project scope. */
export async function installAntigravityProjectMemoryHook(options: { command: string; projectRoot: string }): Promise<AntigravityHookChange> {
  validateMemoryCommand(options.command);
  const filePath = await configPath('project', options.projectRoot, undefined, true);
  if (!filePath) throw new Error('unable to resolve Antigravity project hooks configuration path');
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const config = read.value;
    const previous = config[memoryConfigKey];
    if (previous !== undefined && !isRecord(previous)) throw new Error('Antigravity memory hook block must be an object; no changes were made');
    const block = (previous ?? {}) as JsonObject;
    const refs = readMemoryPreInvocationRefs(block);
    const cleaned = removeManagedMemoryRefs(refs);
    if (previous !== undefined && !cleaned.found) {
      throw new Error('Antigravity memory hook block exists without Threadforge ownership; no changes were made');
    }
    const nextBlock: JsonObject = {
      ...block,
      PreInvocation: [...cleaned.refs, { type: 'command', command: options.command, timeout: 10 }],
    };
    const nextConfig: JsonObject = { ...config, [memoryConfigKey]: nextBlock };
    if (deepEqual(config, nextConfig)) return 'unchanged';
    await atomicWrite(filePath, read.snapshot, nextConfig);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge memory PreInvocation handler. */
export async function removeAntigravityProjectMemoryHook(projectRoot: string): Promise<AntigravityHookChange> {
  const filePath = await configPath('project', projectRoot, undefined, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const config = read.value;
    const previous = config[memoryConfigKey];
    if (previous === undefined) return 'absent';
    if (!isRecord(previous)) throw new Error('Antigravity memory hook block is malformed; no changes were made');
    const refs = readMemoryPreInvocationRefs(previous);
    const cleaned = removeManagedMemoryRefs(refs);
    if (!cleaned.found) return 'absent';
    const nextConfig: JsonObject = { ...config };
    const nextBlock: JsonObject = { ...previous };
    if (cleaned.refs.length === 0) delete nextBlock.PreInvocation;
    else nextBlock.PreInvocation = cleaned.refs;
    if (Object.keys(nextBlock).length === 0) delete nextConfig[memoryConfigKey];
    else nextConfig[memoryConfigKey] = nextBlock;
    await atomicWrite(filePath, read.snapshot, nextConfig);
    return 'removed';
  });
}

export function antigravityHooksConfigPath(scope: AntigravityHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.agents', 'hooks.json')
    : path.join(root, '.gemini', 'config', 'hooks.json');
}

async function configPath(
  scope: AntigravityHookScope,
  projectRoot: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Antigravity scope must be user or project');
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
    const rootStat = await lstat(root);
    if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
    const agents = await ensureDirectory(root, path.join(root, '.agents'), create);
    if (!agents) return undefined;
    return await configFile(agents, 'hooks.json', create);
  }

  const home = homeDir ?? os.homedir();
  if (!path.isAbsolute(home)) throw new Error('Antigravity home directory must be absolute');
  const canonicalHome = await realpath(home).catch(() => { throw new Error('Antigravity home directory must exist and be accessible'); });
  const homeStat = await lstat(canonicalHome);
  if (!homeStat.isDirectory() || homeStat.isSymbolicLink()) throw new Error('Antigravity home must be a real directory');
  const gemini = await ensureDirectory(canonicalHome, path.join(canonicalHome, '.gemini'), create);
  if (!gemini) return undefined;
  const config = await ensureDirectory(canonicalHome, path.join(gemini, 'config'), create);
  if (!config) return undefined;
  return await configFile(config, 'hooks.json', create);
}

async function ensureDirectory(root: string, target: string, create: boolean): Promise<string | undefined> {
  if (create) {
    try { await mkdir(target, { mode: 0o700 }); }
    catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create Antigravity hook configuration directory');
    }
  }
  const stat = await lstat(target).catch((error: unknown) => {
    if (!create && isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!stat) return undefined;
  if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error('Antigravity hook path contains a symbolic link or non-directory');
  const actual = await realpath(target).catch(() => { throw new Error('Antigravity hook directory is inaccessible'); });
  if (!isWithin(root, actual)) throw new Error('Antigravity hook directory resolves outside its selected root');
  return actual;
}

async function configFile(directory: string, name: string, create: boolean): Promise<string | undefined> {
  const filePath = path.join(directory, name);
  const stat = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!stat) return create ? filePath : undefined;
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Antigravity hooks config must be a regular file');
  if (stat.size > maxConfigBytes) throw new Error('Antigravity hooks config exceeds the local size limit');
  return filePath;
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Antigravity hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Antigravity hooks configuration');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<{ value: JsonObject; snapshot: FileSnapshot }> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Antigravity hooks config must be a regular file');
    if (stat.size > maxConfigBytes) throw new Error('Antigravity hooks config exceeds the local size limit');
    const bytes = await readFile(filePath);
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const parsed: unknown = JSON.parse(content);
    if (!isRecord(parsed)) throw new Error('Antigravity hooks config root must be an object');
    return { value: parsed, snapshot: { content, identity: identityOf(stat) } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: {}, snapshot: { content: '' } };
    if (error instanceof Error && error.message.startsWith('Antigravity hooks config')) throw error;
    throw new Error('Antigravity hooks config is invalid UTF-8 strict JSON; no changes were made');
  }
}

function readEventGroups(block: JsonObject, event: string): unknown[] {
  const current = block[event];
  if (current === undefined || current === null) return [];
  if (!Array.isArray(current) || current.some((item) => !isRecord(item) || !Array.isArray(item.hooks))) {
    throw new Error(`Antigravity ${event} must contain hook groups; no changes were made`);
  }
  return current;
}

function readDirectEventHooks(block: JsonObject, event: string): unknown[] {
  const current = block[event];
  if (current === undefined || current === null) return [];
  if (!Array.isArray(current)) throw new Error(`Antigravity ${event} must be a list; no changes were made`);
  return current;
}

function removeManagedAntigravityHooks(block: JsonObject): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...block };
  let found = false;
  if (block.hooks !== undefined && block.hooks !== null) {
    if (!isRecord(block.hooks)) throw new Error('Antigravity legacy hooks must be an object; no changes were made');
    const legacy = removeManagedLegacyHooks(block.hooks);
    found ||= legacy.found;
    if (Object.keys(legacy.value).length === 0) delete value.hooks;
    else value.hooks = legacy.value;
  }
  for (const event of managedEvents) {
    const raw = block[event];
    if (raw === undefined || raw === null) continue;
    if (!Array.isArray(raw)) throw new Error(`Antigravity ${event} must be an array; no changes were made`);
    if (!groupedHookEvents.has(event)) {
      const direct = removeManagedDirectHooks(raw);
      found ||= direct.found;
      if (direct.refs.length === 0) delete value[event];
      else value[event] = direct.refs;
      continue;
    }
    const retainedGroups: unknown[] = [];
    for (const rawGroup of raw) {
      if (!isRecord(rawGroup) || !Array.isArray(rawGroup.hooks)) {
        throw new Error(`Antigravity ${event} contains an unsupported hook group; no changes were made`);
      }
      let removedManagedRef = false;
      const retainedRefs = rawGroup.hooks.filter((rawRef) => {
        if (!isRecord(rawRef)) return true;
        const managed = isManagedTelemetryRef(rawRef);
        removedManagedRef ||= managed;
        found ||= managed;
        return !managed;
      });
      if (retainedRefs.length > 0) retainedGroups.push(retainedRefs.length === rawGroup.hooks.length ? rawGroup : { ...rawGroup, hooks: retainedRefs });
      else if (!removedManagedRef && Object.keys(rawGroup).some((key) => key !== 'hooks')) retainedGroups.push(rawGroup);
    }
    if (retainedGroups.length === 0) delete value[event];
    else value[event] = retainedGroups;
  }
  return { value, found };
}

function removeManagedLegacyHooks(hooks: JsonObject): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const [event, rawGroups] of Object.entries(hooks)) {
    if (rawGroups === null) continue;
    if (!Array.isArray(rawGroups)) throw new Error(`Antigravity legacy hooks.${event} must be an array; no changes were made`);
    const retainedGroups: unknown[] = [];
    for (const rawGroup of rawGroups) {
      if (!isRecord(rawGroup) || !Array.isArray(rawGroup.hooks)) {
        throw new Error(`Antigravity legacy hooks.${event} contains an unsupported hook group; no changes were made`);
      }
      let removedManagedRef = false;
      const retainedRefs = rawGroup.hooks.filter((rawRef) => {
        if (!isRecord(rawRef)) return true;
        const managed = isManagedTelemetryRef(rawRef);
        removedManagedRef ||= managed;
        found ||= managed;
        return !managed;
      });
      if (retainedRefs.length > 0) retainedGroups.push(retainedRefs.length === rawGroup.hooks.length ? rawGroup : { ...rawGroup, hooks: retainedRefs });
      else if (!removedManagedRef && Object.keys(rawGroup).some((key) => key !== 'hooks')) retainedGroups.push(rawGroup);
    }
    if (retainedGroups.length === 0) delete value[event];
    else value[event] = retainedGroups;
  }
  return { value, found };
}

function removeManagedDirectHooks(refs: unknown[]): { refs: unknown[]; found: boolean } {
  let found = false;
  const retained = refs.filter((ref) => {
    if (!isRecord(ref)) return true;
    const managed = isManagedTelemetryRef(ref);
    found ||= managed;
    return !managed;
  });
  return { refs: retained, found };
}

function isManagedTelemetryRef(ref: JsonObject): boolean {
  return ref.type === 'command' && typeof ref.command === 'string' && ref.command.includes(antigravityManagedMarker);
}

function readMemoryPreInvocationRefs(block: JsonObject): unknown[] {
  const raw = block.PreInvocation;
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) {
    throw new Error('Antigravity memory PreInvocation must be a list; no changes were made');
  }
  return raw;
}

function removeManagedMemoryRefs(refs: unknown[]): { refs: unknown[]; found: boolean } {
  let found = false;
  const retained = refs.filter((ref) => {
    const managed = isRecord(ref) && ref.type === 'command' && typeof ref.command === 'string' &&
      ref.command.includes(antigravityMemoryManagedMarker) && ref.command.includes(antigravityMemoryRunnerMarker);
    found ||= managed;
    return !managed;
  });
  return { refs: retained, found };
}

async function atomicWrite(filePath: string, snapshot: FileSnapshot, value: JsonObject): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Antigravity hooks config would exceed the local size limit');
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : 0o600;
  let handle;
  try {
    await assertUnchanged(filePath, snapshot);
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(filePath, snapshot);
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Antigravity hooks config');
  }
}

async function assertUnchanged(filePath: string, snapshot: FileSnapshot): Promise<void> {
  const current = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (current) throw new Error('Antigravity hooks config appeared during update');
    return;
  }
  if (!current || !current.isFile() || current.isSymbolicLink() || current.dev !== snapshot.identity.dev ||
      current.ino !== snapshot.identity.ino || current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Antigravity hooks config changed during update');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Antigravity hook command is invalid or too long');
  }
  if (!command.includes(antigravityManagedMarker)) throw new Error(`Antigravity hook command must include ${antigravityManagedMarker}`);
  if (looksSecretLike(command)) throw new Error('Antigravity hook command must not contain credential-like values');
}

function validateMemoryCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Antigravity memory hook command is invalid or too long');
  }
  if (!command.includes(antigravityMemoryRunnerMarker) || !command.includes(antigravityMemoryManagedMarker)) {
    throw new Error(`Antigravity memory command must include ${antigravityMemoryRunnerMarker} and ${antigravityMemoryManagedMarker}`);
  }
  if (looksSecretLike(command)) throw new Error('Antigravity memory hook command must not contain credential-like values');
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

function identityOf(stat: Stats): NonNullable<FileSnapshot['identity']> {
  return { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs };
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
