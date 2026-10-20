import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { kiroHookEvents, kiroManagedMarker, type KiroHookEvent } from './kiro-hook.ts';

export type KiroHookScope = 'user' | 'project';
export type KiroHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

export interface KiroHookConfigOptions {
  command: string;
  scope?: KiroHookScope;
  projectRoot?: string;
  kiroHome?: string;
  homeDir?: string;
}

const configFileName = 'threadforge-observational.json';
const lockName = '.threadforge-kiro-hooks.lock';
const maxConfigBytes = 1 << 20;
const definitions: Array<{ event: KiroHookEvent; timeout: number }> = [
  { event: 'SessionStart', timeout: 10 },
  { event: 'AgentSpawn', timeout: 10 },
  { event: 'UserPromptSubmit', timeout: 30 },
  { event: 'PreToolUse', timeout: 10 },
  { event: 'PostToolUse', timeout: 10 },
  { event: 'Stop', timeout: 45 },
];
const description = 'Threadforge local observational telemetry; no prompt or policy output.';

type JsonObject = Record<string, unknown>;
interface FileSnapshot {
  identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
}
interface HooksFile { version: 'v1'; hooks: JsonObject[]; }

/** Installs Kiro v1 command hooks in a dedicated file without changing sibling hook files. */
export async function installKiroHooks(options: KiroHookConfigOptions): Promise<KiroHookChange> {
  validateCommand(options.command);
  const scope = options.scope ?? 'user';
  const file = await configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, true);
  if (!file) throw new Error('unable to resolve Kiro hook configuration path');
  return withLock(file, async () => {
    const read = await readHooksFile(file);
    const prior = read.value;
    const owned = splitOwnedHooks(prior.hooks);
    const nextHooks = [...owned.unowned, ...createManagedHooks(options.command)];
    assertNoNameCollisions(owned.unowned, nextHooks);
    const next: HooksFile = { version: 'v1', hooks: nextHooks };
    if (JSON.stringify(canonicalize(prior)) === JSON.stringify(canonicalize(next))) return 'unchanged';
    await atomicWrite(file, read.snapshot, next);
    return owned.managed.length > 0 ? 'updated' : 'installed';
  });
}

/** Updates only after a Threadforge-owned hook set has been installed. */
export async function updateKiroHooks(options: KiroHookConfigOptions): Promise<KiroHookChange> {
  validateCommand(options.command);
  const scope = options.scope ?? 'user';
  const file = await configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, false);
  if (!file) throw new Error('no Kiro hook configuration exists to update');
  return withLock(file, async () => {
    const read = await readHooksFile(file);
    const owned = splitOwnedHooks(read.value.hooks);
    if (owned.managed.length === 0) throw new Error('no Threadforge-owned Kiro hooks exist to update');
    const next: HooksFile = { version: 'v1', hooks: [...owned.unowned, ...createManagedHooks(options.command)] };
    assertNoNameCollisions(owned.unowned, next.hooks);
    if (JSON.stringify(canonicalize(read.value)) === JSON.stringify(canonicalize(next))) return 'unchanged';
    await atomicWrite(file, read.snapshot, next);
    return 'updated';
  });
}

/** Removes Threadforge hook objects only; unrelated hook objects and files remain intact. */
export async function removeKiroHooks(options: Omit<KiroHookConfigOptions, 'command'> = {}): Promise<KiroHookChange> {
  const scope = options.scope ?? 'user';
  const file = await configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, false);
  if (!file) return 'absent';
  return withLock(file, async () => {
    const read = await readHooksFile(file);
    const owned = splitOwnedHooks(read.value.hooks);
    if (owned.managed.length === 0) return 'absent';
    await atomicWrite(file, read.snapshot, { version: 'v1', hooks: owned.unowned });
    return 'removed';
  });
}

export function kiroHooksConfigPath(scope: KiroHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.kiro', 'hooks', configFileName)
    : path.join(root, 'hooks', configFileName);
}

async function configPath(
  scope: KiroHookScope,
  projectRoot: string | undefined,
  kiroHome: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Kiro scope must be user or project');
  let rootInput: string;
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    rootInput = projectRoot;
  } else {
    rootInput = kiroHome ?? process.env.KIRO_HOME ?? homeDir ?? os.homedir();
    if (!path.isAbsolute(rootInput)) throw new Error('Kiro home must be an absolute directory');
  }
  const rootInfo = await lstat(rootInput).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) throw new Error('Kiro scope root must exist and be accessible');
    throw error;
  });
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Kiro scope root must be a real directory');
  const root = await realpath(rootInput).catch(() => { throw new Error('Kiro scope root must exist and be accessible'); });
  const directories = scope === 'project' ? [path.join(root, '.kiro'), path.join(root, '.kiro', 'hooks')] : [path.join(root, 'hooks')];
  let current: string | undefined;
  for (const directory of directories) {
    const info = await lstat(directory).catch(async (error: unknown) => {
      if (!isCode(error, 'ENOENT')) throw error;
      if (!create) return undefined;
      await mkdir(directory, { mode: 0o755 }).catch((mkdirError: unknown) => {
        if (!isCode(mkdirError, 'EEXIST')) throw new Error('unable to create Kiro hooks directory');
      });
      return lstat(directory);
    });
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Kiro hooks path contains a symbolic link or non-directory');
    const resolved = await realpath(directory).catch(() => { throw new Error('Kiro hooks directory is inaccessible'); });
    if (!isWithin(root, resolved)) throw new Error('Kiro hooks directory resolves outside its selected root');
    current = resolved;
  }
  if (!current) return undefined;
  const file = path.join(current, configFileName);
  const fileInfo = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (fileInfo && (!fileInfo.isFile() || fileInfo.isSymbolicLink())) throw new Error('Kiro Threadforge hook file must be regular and not a symbolic link');
  if (fileInfo && fileInfo.size > maxConfigBytes) throw new Error('Kiro hook file exceeds the local size limit');
  return fileInfo || create ? file : undefined;
}

async function withLock<T>(file: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(file), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Kiro hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Kiro hooks');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readHooksFile(file: string): Promise<{ value: HooksFile; snapshot: FileSnapshot }> {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Kiro Threadforge hook file must be regular and not a symbolic link');
    if (info.size > maxConfigBytes) throw new Error('Kiro hook file exceeds the local size limit');
    const bytes = await readFile(file);
    if (bytes.byteLength > maxConfigBytes) throw new Error('Kiro hook file exceeds the local size limit');
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const value: unknown = JSON.parse(text);
    if (!isRecord(value) || value.version !== 'v1' || !Array.isArray(value.hooks) || Object.keys(value).some((key) => key !== 'version' && key !== 'hooks')) {
      throw new Error('Kiro hook file must use the documented v1 {version,hooks} structure; no changes were made');
    }
    const hooks: JsonObject[] = value.hooks.map((hook) => {
      if (!isRecord(hook)) throw new Error('Kiro hook file contains a malformed hook object; no changes were made');
      return hook;
    });
    return {
      value: { version: 'v1', hooks },
      snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } },
    };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: { version: 'v1', hooks: [] }, snapshot: {} };
    if (error instanceof Error && error.message.startsWith('Kiro ')) throw error;
    throw new Error('Kiro hook file is invalid UTF-8 strict JSON; no changes were made');
  }
}

function splitOwnedHooks(hooks: JsonObject[]): { managed: JsonObject[]; unowned: JsonObject[] } {
  const managed: JsonObject[] = [];
  const unowned: JsonObject[] = [];
  for (const hook of hooks) {
    const action = isRecord(hook.action) ? hook.action : undefined;
    const command = typeof action?.command === 'string' ? action.command : '';
    const markedByName = typeof hook.name === 'string' && hook.name.startsWith('threadforge-kiro-');
    const markedByCommand = command.includes(kiroManagedMarker);
    if (markedByName !== markedByCommand) throw new Error('Kiro hook has an incomplete Threadforge ownership marker; inspect it before editing');
    if (!markedByName) { unowned.push(hook); continue; }
    const definition = definitions.find((item) => hook.trigger === item.event);
    const allowed = ['name', 'description', 'trigger', 'action', 'timeout', 'enabled'];
    if (!definition || hook.name !== hookName(definition.event) || hook.description !== description ||
        action?.type !== 'command' || hook.timeout !== definition.timeout || hook.enabled !== true ||
        Object.keys(hook).some((key) => !allowed.includes(key)) || Object.keys(action ?? {}).some((key) => key !== 'type' && key !== 'command')) {
      throw new Error('Threadforge Kiro hook was modified or is unsupported; inspect it before editing');
    }
    managed.push(hook);
  }
  return { managed, unowned };
}

function createManagedHooks(command: string): JsonObject[] {
  return definitions.map(({ event, timeout }) => ({
    name: hookName(event),
    description,
    trigger: event,
    action: { type: 'command', command: `${command} --hook-event=${event}` },
    timeout,
    enabled: true,
  }));
}

function hookName(event: KiroHookEvent): string {
  return `threadforge-kiro-${event.replace(/[A-Z]/g, (character, index) => `${index ? '-' : ''}${character.toLowerCase()}`)}`;
}

function assertNoNameCollisions(unowned: JsonObject[], next: JsonObject[]): void {
  const managedNames = new Set(next.filter((hook) => typeof hook.name === 'string' && hook.name.startsWith('threadforge-kiro-')).map((hook) => hook.name));
  if (unowned.some((hook) => typeof hook.name === 'string' && managedNames.has(hook.name))) {
    throw new Error('an unrelated Kiro hook uses a Threadforge hook name; no changes were made');
  }
}

async function atomicWrite(file: string, snapshot: FileSnapshot, value: HooksFile): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Kiro hook file would exceed the local size limit');
  const tempPath = `${file}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : 0o644;
  let handle;
  try {
    await assertUnchanged(file, snapshot);
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(file, snapshot);
    await rename(tempPath, file);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Kiro hook file');
  }
}

async function assertUnchanged(file: string, snapshot: FileSnapshot): Promise<void> {
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (info) throw new Error('Kiro hook file appeared during update');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
      info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Kiro hook file changed during update');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command) || /--hook-event=/.test(command)) {
    throw new Error('Kiro hook command is invalid, too long, or already contains a managed event selector');
  }
  if (!command.includes(kiroManagedMarker)) throw new Error(`Kiro hook command must include ${kiroManagedMarker}`);
  if (looksSecretLike(command)) throw new Error('Kiro hook command must not contain credential-like values');
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function isWithin(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
