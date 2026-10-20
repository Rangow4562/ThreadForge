import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { museManagedMarker, type MuseHookEvent } from './muse-hook.ts';

export type MuseHookScope = 'user' | 'project';
export type MuseHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export interface MuseHookConfigOptions {
  command: string;
  scope: MuseHookScope;
  projectRoot?: string;
  xdgConfigHome?: string;
  homeDir?: string;
}

const lockName = '.threadforge-muse-hooks.lock';
const maxConfigBytes = 8 << 20;
const definitions: Array<{ event: MuseHookEvent; timeout: number }> = [
  { event: 'SessionStart', timeout: 10 },
  { event: 'UserPromptSubmit', timeout: 30 },
  { event: 'PreToolUse', timeout: 10 },
  { event: 'PermissionRequest', timeout: 10 },
  { event: 'PostToolUse', timeout: 10 },
  { event: 'PostToolUseFailure', timeout: 10 },
  { event: 'PreCompact', timeout: 10 },
  { event: 'PostCompact', timeout: 10 },
  { event: 'SubagentStart', timeout: 10 },
  { event: 'SubagentStop', timeout: 10 },
  { event: 'Notification', timeout: 10 },
  { event: 'Stop', timeout: 45 },
  { event: 'SessionEnd', timeout: 10 },
];

type JsonObject = Record<string, unknown>;
interface FileSnapshot { identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number }; }

/** Installs user or project Muse hooks. Managed hooks are intentionally not claimed or replaced. */
export async function installMuseHooks(options: MuseHookConfigOptions): Promise<MuseHookChange> {
  validateCommand(options.command);
  const file = await configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, true);
  if (!file) throw new Error('unable to resolve Muse hook configuration path');
  return withLock(file, async () => {
    const read = await readConfig(file, options.scope);
    const config = read.value;
    const before = JSON.stringify(canonicalize(config));
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks);
    const nextHooks = { ...cleaned.value };
    for (const { event, timeout } of definitions) {
      const groups = readEventGroups(nextHooks, event);
      const handler: JsonObject = {
        type: 'command', command: `${options.command} --hook-event=${event}`, timeout,
      };
      nextHooks[event] = [...groups, { matcher: '*', hooks: [handler] }];
    }
    config.hooks = nextHooks;
    if (before === JSON.stringify(canonicalize(config))) return 'unchanged';
    await atomicWrite(file, read.snapshot, config, options.scope);
    return cleaned.found ? 'updated' : 'installed';
  });
}

export async function updateMuseHooks(options: MuseHookConfigOptions): Promise<MuseHookChange> {
  validateCommand(options.command);
  const file = await configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, false);
  if (!file) throw new Error('no Muse hook configuration exists to update');
  return withLock(file, async () => {
    const read = await readConfig(file, options.scope);
    const hooks = readHookMap(read.value);
    const cleaned = removeManagedHooks(hooks);
    if (!cleaned.found) throw new Error('no Threadforge-owned Muse hooks exist to update');
    const nextHooks = { ...cleaned.value };
    for (const { event, timeout } of definitions) {
      nextHooks[event] = [...readEventGroups(nextHooks, event), {
        matcher: '*', hooks: [{ type: 'command', command: `${options.command} --hook-event=${event}`, timeout }],
      }];
    }
    const next = { ...read.value, hooks: nextHooks };
    if (JSON.stringify(canonicalize(read.value)) === JSON.stringify(canonicalize(next))) return 'unchanged';
    await atomicWrite(file, read.snapshot, next, options.scope);
    return 'updated';
  });
}

export async function removeMuseHooks(options: Omit<MuseHookConfigOptions, 'command'>): Promise<MuseHookChange> {
  const file = await configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, false);
  if (!file) return 'absent';
  return withLock(file, async () => {
    const read = await readConfig(file, options.scope);
    if (read.value.hooks === undefined) return 'absent';
    const cleaned = removeManagedHooks(readHookMap(read.value));
    if (!cleaned.found) return 'absent';
    const next = { ...read.value };
    if (Object.keys(cleaned.value).length === 0) delete next.hooks;
    else next.hooks = cleaned.value;
    await atomicWrite(file, read.snapshot, next, options.scope);
    return 'removed';
  });
}

export function museHooksConfigPath(scope: MuseHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.muse', 'hooks.json')
    : path.join(root, 'muse', 'settings.json');
}

async function configPath(
  scope: MuseHookScope,
  projectRoot: string | undefined,
  xdgConfigHome: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Muse hook scope must be user or project');
  let rootInput: string;
  let components: string[];
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    rootInput = projectRoot;
    components = ['.muse'];
  } else if (xdgConfigHome ?? process.env.XDG_CONFIG_HOME) {
    rootInput = xdgConfigHome ?? process.env.XDG_CONFIG_HOME ?? '';
    if (!path.isAbsolute(rootInput)) throw new Error('XDG_CONFIG_HOME must be an absolute directory');
    components = ['muse'];
  } else {
    rootInput = homeDir ?? os.homedir();
    components = ['.config', 'muse'];
  }
  if (!path.isAbsolute(rootInput)) throw new Error('Muse configuration root must be an absolute directory');
  const rootInfo = await lstat(rootInput).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) throw new Error('Muse configuration root must exist and be accessible');
    throw error;
  });
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Muse configuration root must be a real directory');
  const root = await realpath(rootInput).catch(() => { throw new Error('Muse configuration root must exist and be accessible'); });
  let directory = root;
  for (const component of components) {
    directory = path.join(directory, component);
    const info = await lstat(directory).catch(async (error: unknown) => {
      if (!isCode(error, 'ENOENT')) throw error;
      if (!create) return undefined;
      await mkdir(directory, { mode: scope === 'user' ? 0o700 : 0o755 }).catch((mkdirError: unknown) => {
        if (!isCode(mkdirError, 'EEXIST')) throw new Error('unable to create Muse hooks directory');
      });
      return lstat(directory);
    });
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Muse hooks path contains a symbolic link or non-directory');
    const resolved = await realpath(directory).catch(() => { throw new Error('Muse hooks directory is inaccessible'); });
    if (!isWithin(root, resolved)) throw new Error('Muse hooks directory resolves outside its selected root');
    directory = resolved;
  }
  const fileName = scope === 'project' ? 'hooks.json' : 'settings.json';
  const file = path.join(directory, fileName);
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (info && (!info.isFile() || info.isSymbolicLink())) throw new Error('Muse hook config must be a regular file and not a symbolic link');
  if (info && info.size > maxConfigBytes) throw new Error('Muse hook config exceeds the local size limit');
  return info || create ? file : undefined;
}

async function withLock<T>(file: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(file), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Muse hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Muse hook config');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(file: string, scope: MuseHookScope): Promise<{ value: JsonObject; snapshot: FileSnapshot }> {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Muse hook config must be a regular file and not a symbolic link');
    if (info.size > maxConfigBytes) throw new Error('Muse hook config exceeds the local size limit');
    const bytes = await readFile(file);
    if (bytes.byteLength > maxConfigBytes) throw new Error('Muse hook config exceeds the local size limit');
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const value: unknown = JSON.parse(text);
    if (!isRecord(value)) throw new Error('Muse hook config root must be an object');
    if (scope === 'user' && value.schema_version !== 1) throw new Error('Muse settings must declare schema_version: 1; no changes were made');
    return { value, snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: scope === 'user' ? { schema_version: 1 } : {}, snapshot: {} };
    if (error instanceof Error && error.message.startsWith('Muse ')) throw error;
    throw new Error('Muse hook config is invalid UTF-8 strict JSON; no changes were made');
  }
}

function readHookMap(config: JsonObject): JsonObject {
  if (config.hooks === undefined || config.hooks === null) return {};
  if (!isRecord(config.hooks)) throw new Error('Muse hooks must be an object; no changes were made');
  return config.hooks;
}

function readEventGroups(hooks: JsonObject, event: string): unknown[] {
  const groups = hooks[event];
  if (groups === undefined || groups === null) return [];
  if (!Array.isArray(groups) || groups.some((group) => !isRecord(group) || !Array.isArray(group.hooks))) {
    throw new Error(`Muse hooks.${event} must contain matcher groups with hooks arrays; no changes were made`);
  }
  return groups;
}

function removeManagedHooks(hooks: JsonObject): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const [event, rawGroups] of Object.entries(hooks)) {
    if (rawGroups === null) continue;
    if (!Array.isArray(rawGroups)) throw new Error(`Muse hooks.${event} must be an array; no changes were made`);
    const retainedGroups: unknown[] = [];
    for (const group of rawGroups) {
      if (!isRecord(group) || !Array.isArray(group.hooks)) throw new Error(`Muse hooks.${event} has a malformed matcher group; no changes were made`);
      const retainedRefs = group.hooks.filter((hook) => {
        if (!isRecord(hook)) throw new Error(`Muse hooks.${event} has a malformed handler; no changes were made`);
        const markerInCommand = typeof hook.command === 'string' && hook.command.includes(museManagedMarker);
        if (markerInCommand) validateManagedHandler(event, group, hook);
        found ||= markerInCommand;
        return !markerInCommand;
      });
      if (retainedRefs.length > 0) retainedGroups.push(retainedRefs.length === group.hooks.length ? group : { ...group, hooks: retainedRefs });
      else if (Object.keys(group).some((key) => key !== 'hooks' && key !== 'matcher')) retainedGroups.push({ ...group, hooks: [] });
    }
    if (retainedGroups.length === 0) delete value[event];
    else value[event] = retainedGroups;
  }
  return { value, found };
}

function validateManagedHandler(event: string, group: JsonObject, hook: JsonObject): void {
  const definition = definitions.find((item) => item.event === event);
  const command = typeof hook.command === 'string' ? hook.command : '';
  if (!definition || group.matcher !== '*' || Object.keys(group).some((key) => key !== 'matcher' && key !== 'hooks') ||
      hook.type !== 'command' || hook.timeout !== definition.timeout || !command.endsWith(`--hook-event=${event}`) ||
      Object.keys(hook).some((key) => key !== 'type' && key !== 'command' && key !== 'timeout')) {
    throw new Error('Threadforge Muse hook was modified or is unsupported; inspect it before editing');
  }
}

async function atomicWrite(file: string, snapshot: FileSnapshot, config: JsonObject, scope: MuseHookScope): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(config, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Muse hook config would exceed the local size limit');
  const tempFile = `${file}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : scope === 'user' ? 0o600 : 0o644;
  let handle;
  try {
    await assertUnchanged(file, snapshot);
    handle = await open(tempFile, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(file, snapshot);
    await rename(tempFile, file);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempFile).catch(() => {});
    throw new Error('unable to atomically update Muse hook config');
  }
}

async function assertUnchanged(file: string, snapshot: FileSnapshot): Promise<void> {
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (info) throw new Error('Muse hook config appeared during update');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
      info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Muse hook config changed during update');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command) || /--hook-event=/.test(command)) {
    throw new Error('Muse hook command is invalid, too long, or already contains a managed event selector');
  }
  if (!command.includes(museManagedMarker)) throw new Error(`Muse hook command must include ${museManagedMarker}`);
  if (looksSecretLike(command)) throw new Error('Muse hook command must not contain credential-like values');
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
