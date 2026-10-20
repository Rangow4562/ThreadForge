import { constants, type Stats } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
import { devinHookEvents, devinManagedMarker, type DevinHookEvent } from './devin-hook.ts';

export type DevinHookScope = 'user' | 'project';
export type DevinHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export interface DevinHookConfigOptions {
  commandTemplate?: string;
  scope?: DevinHookScope;
  projectRoot?: string;
  homeDir?: string;
}

type JsonObject = Record<string, unknown>;
type Identity = { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
type Snapshot = { content: string; identity?: Identity };
type LocatedHooks = { root: JsonObject; hooks: JsonObject; wrapped: boolean };

const maxConfigBytes = 8 << 20;
const lockName = '.threadforge-devin-hooks.lock';
const eventTimeouts: Partial<Record<DevinHookEvent, number>> = {
  UserPromptSubmit: 30,
  PreToolUse: 10,
  PermissionRequest: 10,
  PostToolUse: 10,
  Stop: 45,
};

/** Install digest-only command hooks in Devin's user config or project hook file. */
export async function installDevinHooks(options: DevinHookConfigOptions = {}): Promise<DevinHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await resolveConfigPath(scope, options.projectRoot, options.homeDir, true);
  if (!filePath) throw new Error('unable to resolve Devin hook configuration path');
  return withLock(filePath, async () => {
    const snapshot = await readConfig(filePath);
    const located = locateHooks(snapshot.value, scope);
    const cleaned = removeManagedHooks(located.hooks);
    const nextHooks: JsonObject = { ...cleaned.value };

    for (const hookEvent of devinHookEvents) {
      const current = readEventGroups(nextHooks[hookEvent], hookEvent);
      const command = commandFor(hookEvent, options.commandTemplate);
      nextHooks[hookEvent] = [...current, {
        hooks: [{
          type: 'command',
          command,
          ...(eventTimeouts[hookEvent] === undefined ? {} : { timeout: eventTimeouts[hookEvent] }),
        }],
      }];
    }

    const nextRoot = applyHooks(located, nextHooks);
    if (deepEqual(snapshot.value, nextRoot)) return 'unchanged';
    await atomicWrite(filePath, snapshot.snapshot, nextRoot, scope === 'user' ? 0o600 : 0o644);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Remove only command references carrying Threadforge's explicit ownership marker. */
export async function removeDevinHooks(
  options: Omit<DevinHookConfigOptions, 'commandTemplate'> = {},
): Promise<DevinHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await resolveConfigPath(scope, options.projectRoot, options.homeDir, false);
  if (!filePath) return 'absent';
  const resolvedPath = filePath;
  return withLock(resolvedPath, async () => {
    const snapshot = await readConfig(resolvedPath);
    const located = locateHooks(snapshot.value, scope);
    const cleaned = removeManagedHooks(located.hooks);
    if (!cleaned.found) return 'absent';
    await atomicWrite(resolvedPath, snapshot.snapshot, applyHooks(located, cleaned.value), scope === 'user' ? 0o600 : 0o644);
    return 'removed';
  });
}

/** Resolve Devin CLI config paths; project hooks are a standalone map, user hooks are nested. */
export function devinHooksConfigPath(scope: DevinHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.devin', 'hooks.v1.json')
    : path.join(root, '.config', 'devin', 'config.json');
}

function locateHooks(root: JsonObject, scope: DevinHookScope): LocatedHooks {
  if (scope === 'project') return { root, hooks: root, wrapped: false };
  const rawHooks = root.hooks;
  if (rawHooks === undefined) return { root, hooks: {}, wrapped: true };
  if (!isRecord(rawHooks)) throw new Error('Devin config hooks must be an object; no changes were made');
  return { root, hooks: rawHooks, wrapped: true };
}

function applyHooks(located: LocatedHooks, hooks: JsonObject): JsonObject {
  if (!located.wrapped) return hooks;
  const next = { ...located.root };
  if (Object.keys(hooks).length === 0) delete next.hooks;
  else next.hooks = hooks;
  return next;
}

function readEventGroups(value: unknown, hookEvent: DevinHookEvent): unknown[] {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error(`Devin hooks.${hookEvent} must be an array; no changes were made`);
  for (const group of value) {
    if (!isRecord(group) || !Array.isArray(group.hooks) || group.hooks.some((ref) => !isRecord(ref))) {
      throw new Error(`Devin hooks.${hookEvent} contains an unsupported hook group; no changes were made`);
    }
  }
  return value;
}

function removeManagedHooks(hooks: JsonObject): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const hookEvent of devinHookEvents) {
    const groups = readEventGroups(hooks[hookEvent], hookEvent);
    if (groups.length === 0) continue;
    const retainedGroups: unknown[] = [];
    for (const groupValue of groups) {
      const group = groupValue as JsonObject;
      const refs = group.hooks as JsonObject[];
      const retained = refs.filter((ref) => {
        const managed = ref.type === 'command' && typeof ref.command === 'string' && ref.command.includes(devinManagedMarker);
        found ||= managed;
        return !managed;
      });
      if (retained.length > 0) retainedGroups.push(retained.length === refs.length ? group : { ...group, hooks: retained });
      else if (Object.keys(group).some((key) => key !== 'hooks')) retainedGroups.push({ ...group, hooks: [] });
    }
    if (retainedGroups.length === 0) delete value[hookEvent];
    else value[hookEvent] = retainedGroups;
  }
  return { value, found };
}

function commandFor(hookEvent: DevinHookEvent, template?: string): string {
  const commandTemplate = template ?? defaultCommandTemplate();
  validateCommandTemplate(commandTemplate);
  const command = commandTemplate.replace('{event}', hookEvent);
  if (command.length > 8_192 || /[\r\n\0]/.test(command) || looksCredentialLike(command)) {
    throw new Error('Devin hook command is invalid or too long');
  }
  return command;
}

function defaultCommandTemplate(): string {
  const cliPath = fileURLToPath(new URL('./cli.ts', import.meta.url));
  return `${quoteShellArgument(process.execPath)} --experimental-strip-types ${quoteShellArgument(cliPath)} devin hook {event} ${devinManagedMarker}`;
}

function validateCommandTemplate(template: string): void {
  if (typeof template !== 'string' || template.trim() === '' || template.length > 8_000 || /[\r\n\0]/.test(template) ||
      template.split('{event}').length !== 2 || !template.includes(devinManagedMarker)) {
    throw new Error(`--command-template must contain exactly one {event} placeholder and ${devinManagedMarker}`);
  }
  if (looksCredentialLike(template)) throw new Error('--command-template contains a credential-like value');
}

function quoteShellArgument(value: string): string {
  if (process.platform !== 'win32') return `'${value.replaceAll("'", "'\\''")}'`;
  let result = '"';
  let slashes = 0;
  for (const character of value) {
    if (character === '\\') { slashes++; continue; }
    if (character === '"') { result += '\\'.repeat(slashes * 2 + 1) + '"'; slashes = 0; continue; }
    result += '\\'.repeat(slashes) + character;
    slashes = 0;
  }
  return result + '\\'.repeat(slashes * 2) + '"';
}

function looksCredentialLike(value: string): boolean {
  return /\b(?:(?:api|access|refresh|auth)[_ -]?)?(?:key|token|password|secret|credential|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

async function resolveConfigPath(
  scope: DevinHookScope,
  projectRoot: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Devin hook scope must be user or project');
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    const requestedRoot = path.resolve(projectRoot);
    const rootInfo = await lstat(requestedRoot).catch(() => null);
    if (!rootInfo || !rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('project root must be a real directory');
    const root = await realpath(requestedRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
    if (!await isSameRealPathWithoutLinks(root, requestedRoot)) throw new Error('project root must not traverse symbolic links');
    const canonicalRootInfo = await lstat(root);
    if (!canonicalRootInfo.isDirectory() || canonicalRootInfo.isSymbolicLink()) throw new Error('project root must be a real directory');
    const devinDir = await ensureDirectory(root, path.join(root, '.devin'), create);
    return devinDir ? checkConfigFile(path.join(devinDir, 'hooks.v1.json')) : undefined;
  }

  const requestedHome = homeDir ?? os.homedir();
  if (!path.isAbsolute(requestedHome)) throw new Error('Devin home directory must be absolute');
  const resolvedHome = path.resolve(requestedHome);
  const requestedHomeInfo = await lstat(resolvedHome).catch(() => null);
  if (!requestedHomeInfo || !requestedHomeInfo.isDirectory() || requestedHomeInfo.isSymbolicLink()) {
    throw new Error('Devin home must be a real directory');
  }
  const home = await realpath(resolvedHome).catch(() => { throw new Error('Devin home directory must exist and be accessible'); });
  if (!await isSameRealPathWithoutLinks(home, resolvedHome)) throw new Error('Devin home must not traverse symbolic links');
  const homeInfo = await lstat(home);
  if (!homeInfo.isDirectory() || homeInfo.isSymbolicLink()) throw new Error('Devin home must be a real directory');
  const configBase = await ensureDirectory(home, path.join(home, '.config'), create);
  if (!configBase) return undefined;
  const devinDir = await ensureDirectory(home, path.join(configBase, 'devin'), create);
  return devinDir ? checkConfigFile(path.join(devinDir, 'config.json')) : undefined;
}

async function ensureDirectory(root: string, target: string, create: boolean): Promise<string | undefined> {
  if (create) {
    try { await mkdir(target, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Devin hook configuration directory'); }
  }
  const info = await lstat(target).catch((error: unknown) => {
    if (!create && isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!info) return undefined;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Devin hook path contains a symbolic link or non-directory');
  const actual = await realpath(target).catch(() => { throw new Error('Devin hook configuration directory is inaccessible'); });
  if (!isWithin(root, actual)) throw new Error('Devin hook directory resolves outside its selected root');
  return actual;
}

async function checkConfigFile(filePath: string): Promise<string | undefined> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return filePath;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Devin hooks config must be a regular file');
  if (info.size > maxConfigBytes) throw new Error('Devin hooks config exceeds the local size limit');
  return filePath;
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Devin hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Devin hooks configuration');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<{ value: JsonObject; snapshot: Snapshot }> {
  try {
    const before = await lstat(filePath);
    if (!before.isFile() || before.isSymbolicLink()) throw new Error('Devin hooks config must be a regular file');
    if (before.size > maxConfigBytes) throw new Error('Devin hooks config exceeds the local size limit');
    const bytes = await readFile(filePath);
    const after = await lstat(filePath);
    if (!sameIdentity(identityOf(before), identityOf(after)) || bytes.byteLength !== after.size) {
      throw new Error('Devin hooks config changed while being read; no changes were made');
    }
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const parsed: unknown = JSON.parse(content);
    if (!isRecord(parsed)) throw new Error('Devin hooks config root must be an object');
    return { value: parsed, snapshot: { content, identity: identityOf(after) } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: {}, snapshot: { content: '' } };
    if (error instanceof Error && error.message.startsWith('Devin hooks config')) throw error;
    throw new Error('Devin hooks config is invalid UTF-8 strict JSON; no changes were made');
  }
}

async function atomicWrite(filePath: string, snapshot: Snapshot, value: JsonObject, defaultMode: number): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Devin hooks config exceeds the local size limit');
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : defaultMode;
  const tempPath = path.join(path.dirname(filePath), `.threadforge-devin-${randomUUID()}.tmp`);
  let temp;
  try {
    temp = await open(tempPath, 'wx', mode);
    await temp.writeFile(bytes);
    await temp.sync();
    await temp.close();
    temp = undefined;
    await verifySnapshot(filePath, snapshot);
    await rename(tempPath, filePath);
  } catch (error) {
    throw error instanceof Error && error.message.includes('changed')
      ? error
      : new Error('unable to atomically update Devin hooks config; no changes were made');
  } finally {
    await temp?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
  }
}

async function verifySnapshot(filePath: string, snapshot: Snapshot): Promise<void> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!snapshot.identity) {
    if (info) throw new Error('Devin hooks config changed while being updated; no changes were made');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || !sameIdentity(snapshot.identity, identityOf(info))) {
    throw new Error('Devin hooks config changed while being updated; no changes were made');
  }
  const content = await readFile(filePath);
  if (content.byteLength !== Buffer.byteLength(snapshot.content, 'utf8') || content.toString('utf8') !== snapshot.content) {
    throw new Error('Devin hooks config changed while being updated; no changes were made');
  }
}

function identityOf(stat: Stats): Identity {
  return { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs };
}
function sameIdentity(a: Identity, b: Identity): boolean {
  return a.dev === b.dev && a.ino === b.ino && a.mode === b.mode && a.size === b.size && a.mtimeMs === b.mtimeMs;
}
function deepEqual(a: unknown, b: unknown): boolean { return JSON.stringify(a) === JSON.stringify(b); }
function isWithin(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}
function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
