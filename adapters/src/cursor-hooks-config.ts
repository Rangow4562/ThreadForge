import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const cursorManagedMarker = '--threadforge-hook=cursor';
export const cursorMemoryManagedMarker = '--threadforge-memory=cursor-v1';
export const cursorPlanGateManagedMarker = '--threadforge-managed-cursor-plan-gate=v1';
const cursorMemoryRunnerMarker = '--threadforge-managed-cursor-memory=v1';
const cursorPlanGateOwnerMarker = '--threadforge-plan-gate=cursor-v1';
const maxSettingsBytes = 8 << 20;
const installedHooks: Record<string, Array<{ command: string; matcher?: string; timeout?: number }>> = {
  sessionStart: [{ command: '' }],
  postToolUse: [{ command: '' }],
  postToolUseFailure: [{ command: '' }],
  afterFileEdit: [{ command: '' }],
  afterAgentResponse: [{ command: '' }],
  afterAgentThought: [{ command: '' }],
  preCompact: [{ command: '' }],
  sessionEnd: [{ command: '' }],
};

type JsonObject = Record<string, unknown>;
export type CursorHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Adds non-blocking observation hooks; it never installs prompt/tool authorization hooks. */
export async function installCursorProjectHooks(projectRoot: string, command: string): Promise<CursorHookChange> {
  validateCommand(command);
  const filePath = await settingsPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Cursor project hooks path');
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks);
    const wasCurrentVersion = settings.version === 1;
    const next: JsonObject = { ...cleaned.value };
    for (const [hook, refs] of Object.entries(installedHooks)) {
      next[hook] = [...readHookRefs(next, hook), ...refs.map((ref) => ({ ...ref, command }))];
    }
    settings.version = 1;
    settings.hooks = next;
    if (wasCurrentVersion && deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

export async function removeCursorProjectHooks(projectRoot: string): Promise<CursorHookChange> {
  const filePath = await settingsPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    if (Object.keys(settings).length === 1 && settings.version === 1) {
      await unlink(filePath);
    } else {
      await atomicWrite(filePath, settings);
    }
    return 'removed';
  });
}

/** Installs a separate, bounded approved-memory bootstrap on Cursor sessionStart. */
export async function installCursorProjectMemoryHook(projectRoot: string, command: string): Promise<CursorHookChange> {
  validateCursorMemoryCommand(command);
  const filePath = await settingsPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Cursor project hooks path');
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const wasCurrentVersion = settings.version === 1;
    const cleaned = removeHooksWithMarker(hooks, cursorMemoryManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    next.sessionStart = [...readHookRefs(next, 'sessionStart'), { command, timeout: 10 }];
    settings.version = 1;
    settings.hooks = next;
    if (wasCurrentVersion && deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge approved-memory sessionStart command. */
export async function removeCursorProjectMemoryHook(projectRoot: string): Promise<CursorHookChange> {
  const filePath = await settingsPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const cleaned = removeHooksWithMarker(hooks, cursorMemoryManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    if (Object.keys(settings).length === 1 && settings.version === 1) await unlink(filePath);
    else await atomicWrite(filePath, settings);
    return 'removed';
  });
}

/** Installs an opt-in, fail-closed Cursor preToolUse guard for direct Write tools only. */
export async function installCursorProjectPlanGateHook(projectRoot: string, command: string): Promise<CursorHookChange> {
  validateCursorPlanGateCommand(command);
  const filePath = await settingsPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Cursor project hooks path');
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const wasCurrentVersion = settings.version === 1;
    const cleaned = removeHooksWithMarker(hooks, cursorPlanGateManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    next.preToolUse = [
      ...readHookRefs(next, 'preToolUse'),
      { command, matcher: 'Write', timeout: 10, failClosed: true },
    ];
    settings.version = 1;
    settings.hooks = next;
    if (wasCurrentVersion && deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge Cursor preToolUse plan gate. */
export async function removeCursorProjectPlanGateHook(projectRoot: string): Promise<CursorHookChange> {
  const filePath = await settingsPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const settings = await readSettings(filePath);
    const hooks = readHookMap(settings);
    const cleaned = removeHooksWithMarker(hooks, cursorPlanGateManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    if (Object.keys(settings).length === 1 && settings.version === 1) await unlink(filePath);
    else await atomicWrite(filePath, settings);
    return 'removed';
  });
}

async function settingsPath(projectRoot: string, create: boolean): Promise<string | undefined> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
  const cursorDir = path.join(root, '.cursor');
  if (create) {
    try { await mkdir(cursorDir); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Cursor project directory'); }
  } else {
    const dirStat = await lstat(cursorDir).catch(() => null);
    if (!dirStat) return undefined;
    if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Cursor project path contains a symbolic link or non-directory');
  }
  const dirStat = await lstat(cursorDir).catch(() => null);
  if (!dirStat?.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Cursor project path contains a symbolic link or non-directory');
  return path.join(cursorDir, 'hooks.json');
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), '.threadforge-cursor-hooks.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Cursor hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Cursor hooks configuration');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readSettings(filePath: string): Promise<JsonObject> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Cursor hooks config must be a regular file');
    if (stat.size > maxSettingsBytes) throw new Error('Cursor hooks config exceeds the local size limit');
    const value: unknown = JSON.parse((await readFile(filePath, 'utf8')).replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error('Cursor hooks config root must be an object');
    if (value.version !== undefined && value.version !== 1) throw new Error('unsupported Cursor hooks config version');
    return value;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { version: 1, hooks: {} };
    if (error instanceof Error && error.message.startsWith('Cursor hooks config')) throw error;
    if (error instanceof Error && error.message.startsWith('unsupported Cursor')) throw error;
    throw new Error('Cursor hooks config is invalid strict JSON; no changes were made');
  }
}

function readHookMap(settings: JsonObject): JsonObject {
  if (settings.hooks === undefined || settings.hooks === null) return {};
  if (!isRecord(settings.hooks)) throw new Error('Cursor hooks config hooks must be an object; no changes were made');
  return settings.hooks;
}

function readHookRefs(hooks: JsonObject, hookName: string): JsonObject[] {
  const raw = hooks[hookName];
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw) || raw.some((value) => !isRecord(value) || typeof value.command !== 'string')) {
    throw new Error(`Cursor hooks.${hookName} must contain command objects; no changes were made`);
  }
  return raw;
}

function removeManagedHooks(hooks: JsonObject): { value: JsonObject; found: boolean } {
  return removeHooksWithMarker(hooks, cursorManagedMarker);
}

function removeHooksWithMarker(hooks: JsonObject, marker: string): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const [hookName, raw] of Object.entries(hooks)) {
    if (raw === null) continue;
    if (!Array.isArray(raw) || raw.some((item) => !isRecord(item) || typeof item.command !== 'string')) {
      throw new Error(`Cursor hooks.${hookName} must contain command objects; no changes were made`);
    }
    const kept = raw.filter((item) => {
      const managed = item.command.includes(marker);
      found ||= managed;
      return !managed;
    });
    if (kept.length === raw.length) continue;
    if (kept.length === 0) delete value[hookName];
    else value[hookName] = kept;
  }
  return { value, found };
}

async function atomicWrite(filePath: string, value: JsonObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try { mode = (await lstat(filePath)).mode & 0o777; }
    catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Cursor hooks config');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Cursor hook command is invalid or too long');
  }
  if (!command.includes(cursorManagedMarker)) throw new Error(`Cursor hook command must include ${cursorManagedMarker}`);
}

function validateCursorMemoryCommand(command: string): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Cursor memory hook command is invalid or too long');
  }
  if (!command.includes(cursorMemoryRunnerMarker) || !command.includes(cursorMemoryManagedMarker)) {
    throw new Error(`Cursor memory command must include ${cursorMemoryRunnerMarker} and ${cursorMemoryManagedMarker}`);
  }
}

function validateCursorPlanGateCommand(command: string): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Cursor plan-gate command is invalid or too long');
  }
  if (!command.includes(cursorPlanGateManagedMarker) || !command.includes(cursorPlanGateOwnerMarker)) {
    throw new Error(`Cursor plan-gate command must include ${cursorPlanGateManagedMarker} and ${cursorPlanGateOwnerMarker}`);
  }
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
