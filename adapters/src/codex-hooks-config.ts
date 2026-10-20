import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const codexManagedMarker = '--threadforge-hook=codex';
export const codexMemoryManagedMarker = '--threadforge-memory=codex-v1';
export const codexPlanGateManagedMarker = '--threadforge-managed-codex-plan-gate=v1';
const codexMemoryRunnerMarker = '--threadforge-managed-codex-memory=v1';
const codexPlanGateRunnerMarker = '--threadforge-plan-gate=codex-v1';
const maxSettingsBytes = 8 << 20;
const installedHooks: Record<string, Array<{ hooks: Array<{ type: 'command'; command: string; timeout: number }> }>> = {
  SessionStart: [{ hooks: [{ type: 'command', command: '', timeout: 5 }] }],
  PostToolUse: [{ hooks: [{ type: 'command', command: '', timeout: 5 }] }],
};

type JsonObject = Record<string, unknown>;
export type CodexHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Installs observational hooks only; PreToolUse and permission hooks are deliberately excluded. */
export async function installCodexProjectHooks(projectRoot: string, command: string): Promise<CodexHookChange> {
  validateCommand(command);
  const filePath = await hooksPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Codex project hooks path');
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    for (const [event, refs] of Object.entries(installedHooks)) {
      const additions = refs.map((group) => ({
        ...group,
        hooks: group.hooks.map((hook) => ({ ...hook, command })),
      }));
      next[event] = [...readEventRefs(next, event), ...additions];
    }
    config.hooks = next;
    if (deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, config);
    return cleaned.found ? 'updated' : 'installed';
  });
}

export async function removeCodexProjectHooks(projectRoot: string): Promise<CodexHookChange> {
  const filePath = await hooksPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete config.hooks;
    else config.hooks = cleaned.value;
    if (Object.keys(config).length === 0) await unlink(filePath);
    else await atomicWrite(filePath, config);
    return 'removed';
  });
}

/** Installs approved-memory retrieval as a separate, non-blocking prompt hook. */
export async function installCodexProjectMemoryHook(projectRoot: string, command: string): Promise<CodexHookChange> {
  validateCommand(command, codexMemoryManagedMarker);
  if (!command.includes(codexMemoryRunnerMarker)) {
    throw new Error(`Codex memory command must include the runtime marker ${codexMemoryRunnerMarker}`);
  }
  const filePath = await hooksPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Codex project hooks path');
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexMemoryManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    next.UserPromptSubmit = [...readEventRefs(next, 'UserPromptSubmit'), {
      hooks: [{ type: 'command', command, timeout: 5 }],
    }];
    config.hooks = next;
    if (deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, config);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge approved-memory prompt hook. */
export async function removeCodexProjectMemoryHook(projectRoot: string): Promise<CodexHookChange> {
  const filePath = await hooksPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexMemoryManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete config.hooks;
    else config.hooks = cleaned.value;
    if (Object.keys(config).length === 0) await unlink(filePath);
    else await atomicWrite(filePath, config);
    return 'removed';
  });
}

/** Installs an opt-in deny-only guard for Codex CLI direct patch and shell tools. */
export async function installCodexProjectPlanGateHook(projectRoot: string, command: string): Promise<CodexHookChange> {
  validateCommand(command, codexPlanGateManagedMarker);
  if (!command.includes(codexPlanGateRunnerMarker)) {
    throw new Error(`Codex plan-gate command must include the runtime marker ${codexPlanGateRunnerMarker}`);
  }
  const filePath = await hooksPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Codex project hooks path');
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexPlanGateManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    next.PreToolUse = [...readEventRefs(next, 'PreToolUse'), {
      matcher: '^(apply_patch|exec_command)$',
      hooks: [{ type: 'command', command, timeout: 5 }],
    }];
    config.hooks = next;
    if (deepEqual(hooks, next)) return 'unchanged';
    await atomicWrite(filePath, config);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge-owned Codex task-plan gate. */
export async function removeCodexProjectPlanGateHook(projectRoot: string): Promise<CodexHookChange> {
  const filePath = await hooksPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const config = await readConfig(filePath);
    const hooks = readHookMap(config);
    const cleaned = removeManagedHooks(hooks, codexPlanGateManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete config.hooks;
    else config.hooks = cleaned.value;
    if (Object.keys(config).length === 0) await unlink(filePath);
    else await atomicWrite(filePath, config);
    return 'removed';
  });
}

async function hooksPath(projectRoot: string, create: boolean): Promise<string | undefined> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
  const codexDir = path.join(root, '.codex');
  if (create) {
    try { await mkdir(codexDir); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Codex project directory'); }
  }
  const dirStat = await lstat(codexDir).catch(() => null);
  if (!dirStat) return undefined;
  if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Codex project path contains a symbolic link or non-directory');
  return path.join(codexDir, 'hooks.json');
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), '.threadforge-codex-hooks.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Codex hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Codex hooks configuration');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<JsonObject> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Codex hooks config must be a regular file');
    if (stat.size > maxSettingsBytes) throw new Error('Codex hooks config exceeds the local size limit');
    const value: unknown = JSON.parse((await readFile(filePath, 'utf8')).replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error('Codex hooks config root must be an object');
    return value;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('Codex hooks config')) throw error;
    throw new Error('Codex hooks config is invalid strict JSON; no changes were made');
  }
}

function readHookMap(config: JsonObject): JsonObject {
  if (config.hooks === undefined || config.hooks === null) return {};
  if (!isRecord(config.hooks)) throw new Error('Codex hooks config hooks must be an object; no changes were made');
  return config.hooks;
}

function readEventRefs(hooks: JsonObject, event: string): JsonObject[] {
  const raw = hooks[event];
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw) || raw.some((group) => !isRecord(group) || !Array.isArray(group.hooks))) {
    throw new Error(`Codex hooks.${event} must contain hook groups; no changes were made`);
  }
  return raw;
}

function removeManagedHooks(hooks: JsonObject, marker: string): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const [event, rawGroups] of Object.entries(hooks)) {
    if (rawGroups === null) continue;
    if (!Array.isArray(rawGroups)) throw new Error(`Codex hooks.${event} must be an array; no changes were made`);
    const keptGroups: unknown[] = [];
    for (const group of rawGroups) {
      if (!isRecord(group) || !Array.isArray(group.hooks)) throw new Error(`Codex hooks.${event} contains an unsupported group; no changes were made`);
      const keptRefs = group.hooks.filter((ref) => {
        if (!isRecord(ref)) throw new Error(`Codex hooks.${event} contains an unsupported hook reference; no changes were made`);
        const managed = ref.type === 'command' && typeof ref.command === 'string' && ref.command.includes(marker);
        found ||= managed;
        return !managed;
      });
      if (keptRefs.length === group.hooks.length) keptGroups.push(group);
      else if (keptRefs.length > 0) keptGroups.push({ ...group, hooks: keptRefs });
    }
    if (keptGroups.length === 0) delete value[event];
    else value[event] = keptGroups;
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
    throw new Error('unable to atomically update Codex hooks config');
  }
}

function validateCommand(command: string, marker = codexManagedMarker): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Codex hook command is invalid or too long');
  }
  if (!command.includes(marker)) throw new Error(`Codex hook command must include ${marker}`);
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
