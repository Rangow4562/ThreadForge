import { constants, type Stats } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { devinDesktopHookEvents, devinDesktopManagedMarker, type DevinDesktopHookEvent } from './devin-desktop-hook.ts';

export type DevinDesktopHookScope = 'user' | 'project';
export type DevinDesktopHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export interface DevinDesktopHookConfigOptions {
  commandTemplate?: string;
  scope?: DevinDesktopHookScope;
  projectRoot?: string;
  homeDir?: string;
}

type JsonObject = Record<string, unknown>;
type Identity = { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
type Snapshot = { content: string; identity?: Identity };
type LocatedHooks = { root: JsonObject; hooks: JsonObject; wrapped: boolean };

const maxConfigBytes = 8 << 20;
const lockName = '.threadforge-devin-desktop-hooks.lock';

/** Merge observation-only Cascade/Windsurf hooks into Devin Desktop's hook config. */
export async function installDevinDesktopHooks(options: DevinDesktopHookConfigOptions = {}): Promise<DevinDesktopHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await resolveConfigPath(scope, options.projectRoot, options.homeDir, true);
  if (!filePath) throw new Error('unable to resolve Devin Desktop hook configuration path');
  return withLock(filePath, async () => {
    const snapshot = await readConfig(filePath);
    const located = locateHooks(snapshot.value);
    const cleaned = removeManagedHooks(located.hooks);
    const nextHooks: JsonObject = { ...cleaned.value };
    for (const hookEvent of devinDesktopHookEvents) {
      const existing = readEventHooks(nextHooks[hookEvent], hookEvent);
      const command = commandFor(hookEvent, options.commandTemplate);
      nextHooks[hookEvent] = [...existing, { command, show_output: false }];
    }
    const nextRoot = applyHooks(located, nextHooks);
    if (deepEqual(snapshot.value, nextRoot)) return 'unchanged';
    await atomicWrite(filePath, snapshot.snapshot, nextRoot, scope === 'user' ? 0o600 : 0o644);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Remove only command references carrying Threadforge's Devin Desktop ownership marker. */
export async function removeDevinDesktopHooks(
  options: Omit<DevinDesktopHookConfigOptions, 'commandTemplate'> = {},
): Promise<DevinDesktopHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await resolveConfigPath(scope, options.projectRoot, options.homeDir, false);
  if (!filePath) return 'absent';
  const resolvedPath = filePath;
  return withLock(resolvedPath, async () => {
    const snapshot = await readConfig(resolvedPath);
    const located = locateHooks(snapshot.value);
    const cleaned = removeManagedHooks(located.hooks);
    if (!cleaned.found) return 'absent';
    await atomicWrite(resolvedPath, snapshot.snapshot, applyHooks(located, cleaned.value), scope === 'user' ? 0o600 : 0o644);
    return 'removed';
  });
}

/** Cascade/Windsurf-compatible paths used by Devin Desktop. */
export function devinDesktopHooksConfigPath(scope: DevinDesktopHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.windsurf', 'hooks.json')
    : path.join(root, '.codeium', 'windsurf', 'hooks.json');
}

function locateHooks(root: JsonObject): LocatedHooks {
  if (root.hooks === undefined) return { root, hooks: {}, wrapped: true };
  if (!isRecord(root.hooks)) throw new Error('Devin Desktop hooks config must contain an object at `hooks`; no changes were made');
  return { root, hooks: root.hooks, wrapped: true };
}

function applyHooks(located: LocatedHooks, hooks: JsonObject): JsonObject {
  const next = { ...located.root };
  if (Object.keys(hooks).length === 0) delete next.hooks;
  else next.hooks = hooks;
  return next;
}

function readEventHooks(value: unknown, hookEvent: DevinDesktopHookEvent): unknown[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.some((hook) => !isRecord(hook) || typeof hook.command !== 'string')) {
    throw new Error(`Devin Desktop hooks.${hookEvent} must be an array of command hooks; no changes were made`);
  }
  return value;
}

function removeManagedHooks(hooks: JsonObject): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const hookEvent of devinDesktopHookEvents) {
    const current = readEventHooks(hooks[hookEvent], hookEvent);
    const kept = current.filter((rawHook) => {
      const hook = rawHook as JsonObject;
      const managed = typeof hook.command === 'string' && hook.command.includes(devinDesktopManagedMarker);
      found ||= managed;
      return !managed;
    });
    if (kept.length === current.length) continue;
    if (kept.length === 0) delete value[hookEvent];
    else value[hookEvent] = kept;
  }
  return { value, found };
}

function commandFor(hookEvent: DevinDesktopHookEvent, template?: string): string {
  const commandTemplate = template ?? defaultCommandTemplate();
  validateCommandTemplate(commandTemplate);
  const command = commandTemplate.replace('{event}', hookEvent);
  if (command.length > 8_192 || /[\r\n\0]/.test(command) || looksCredentialLike(command)) {
    throw new Error('Devin Desktop hook command is invalid or too long');
  }
  return command;
}

function defaultCommandTemplate(): string {
  const cliPath = fileURLToPath(new URL('./cli.ts', import.meta.url));
  return `${quoteShellArgument(process.execPath)} --experimental-strip-types ${quoteShellArgument(cliPath)} devin-desktop hook {event} ${devinDesktopManagedMarker}`;
}

function validateCommandTemplate(template: string): void {
  if (typeof template !== 'string' || template.trim() === '' || template.length > 8_000 || /[\r\n\0]/.test(template) ||
      template.split('{event}').length !== 2 || !template.includes(devinDesktopManagedMarker)) {
    throw new Error(`--command-template must contain exactly one {event} placeholder and ${devinDesktopManagedMarker}`);
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
  scope: DevinDesktopHookScope,
  projectRoot: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Devin Desktop hook scope must be user or project');
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    const requestedRoot = path.resolve(projectRoot);
    const rootInfo = await lstat(requestedRoot).catch(() => null);
    if (!rootInfo || !rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('project root must be a real directory');
    const root = await realpath(requestedRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
    if (!samePath(root, requestedRoot)) throw new Error('project root must not traverse symbolic links');
    const configDir = await ensureDirectory(root, path.join(root, '.windsurf'), create);
    return configDir ? checkConfigFile(path.join(configDir, 'hooks.json')) : undefined;
  }

  const homeCandidate = homeDir ?? os.homedir();
  if (!path.isAbsolute(homeCandidate)) throw new Error('Devin Desktop home directory must be absolute');
  const requestedHome = path.resolve(homeCandidate);
  const homeInfo = await lstat(requestedHome).catch(() => null);
  if (!homeInfo || !homeInfo.isDirectory() || homeInfo.isSymbolicLink()) throw new Error('Devin Desktop home must be a real directory');
  const home = await realpath(requestedHome).catch(() => { throw new Error('Devin Desktop home directory must exist and be accessible'); });
  if (!samePath(home, requestedHome)) throw new Error('Devin Desktop home must not traverse symbolic links');
  const codeium = await ensureDirectory(home, path.join(home, '.codeium'), create);
  if (!codeium) return undefined;
  const windsurf = await ensureDirectory(home, path.join(codeium, 'windsurf'), create);
  return windsurf ? checkConfigFile(path.join(windsurf, 'hooks.json')) : undefined;
}

async function ensureDirectory(root: string, target: string, create: boolean): Promise<string | undefined> {
  if (create) {
    try { await mkdir(target, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Devin Desktop hook configuration directory'); }
  }
  const info = await lstat(target).catch((error: unknown) => {
    if (!create && isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!info) return undefined;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Devin Desktop hook path contains a symbolic link or non-directory');
  const actual = await realpath(target).catch(() => { throw new Error('Devin Desktop hook configuration directory is inaccessible'); });
  if (!isWithin(root, actual)) throw new Error('Devin Desktop hook directory resolves outside its selected root');
  return actual;
}

async function checkConfigFile(filePath: string): Promise<string> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return filePath;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Devin Desktop hooks config must be a regular file');
  if (info.size > maxConfigBytes) throw new Error('Devin Desktop hooks config exceeds the local size limit');
  return filePath;
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Devin Desktop hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Devin Desktop hooks configuration');
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
    if (!before.isFile() || before.isSymbolicLink()) throw new Error('Devin Desktop hooks config must be a regular file');
    if (before.size > maxConfigBytes) throw new Error('Devin Desktop hooks config exceeds the local size limit');
    const bytes = await readFile(filePath);
    const after = await lstat(filePath);
    if (!sameIdentity(identityOf(before), identityOf(after)) || bytes.byteLength !== after.size) {
      throw new Error('Devin Desktop hooks config changed while being read; no changes were made');
    }
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const parsed: unknown = JSON.parse(content);
    if (!isRecord(parsed)) throw new Error('Devin Desktop hooks config root must be an object');
    return { value: parsed, snapshot: { content, identity: identityOf(after) } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: {}, snapshot: { content: '' } };
    if (error instanceof Error && error.message.startsWith('Devin Desktop hooks config')) throw error;
    throw new Error('Devin Desktop hooks config is invalid UTF-8 strict JSON; no changes were made');
  }
}

async function atomicWrite(filePath: string, snapshot: Snapshot, value: JsonObject, defaultMode: number): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Devin Desktop hooks config exceeds the local size limit');
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : defaultMode;
  const tempPath = path.join(path.dirname(filePath), `.threadforge-devin-desktop-${randomUUID()}.tmp`);
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
      : new Error('unable to atomically update Devin Desktop hooks config; no changes were made');
  } finally {
    await temp?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
  }
}

async function verifySnapshot(filePath: string, snapshot: Snapshot): Promise<void> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!snapshot.identity) {
    if (info) throw new Error('Devin Desktop hooks config changed while being updated; no changes were made');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || !sameIdentity(snapshot.identity, identityOf(info))) {
    throw new Error('Devin Desktop hooks config changed while being updated; no changes were made');
  }
  const content = await readFile(filePath);
  if (content.byteLength !== Buffer.byteLength(snapshot.content, 'utf8') || content.toString('utf8') !== snapshot.content) {
    throw new Error('Devin Desktop hooks config changed while being updated; no changes were made');
  }
}

function identityOf(stat: Stats): Identity {
  return { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs };
}
function sameIdentity(a: Identity, b: Identity): boolean {
  return a.dev === b.dev && a.ino === b.ino && a.mode === b.mode && a.size === b.size && a.mtimeMs === b.mtimeMs;
}
function samePath(a: string, b: string): boolean {
  const normalize = (value: string) => path.resolve(value).replace(/[\\/]+$/, '');
  return process.platform === 'win32' ? normalize(a).toLowerCase() === normalize(b).toLowerCase() : normalize(a) === normalize(b);
}
function isWithin(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}
function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
function deepEqual(a: unknown, b: unknown): boolean { return JSON.stringify(a) === JSON.stringify(b); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
