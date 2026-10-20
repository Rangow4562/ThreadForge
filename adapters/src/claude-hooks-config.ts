import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const claudeManagedMarker = '--threadforge-hook=claude';
export const claudeMemoryManagedMarker = '--threadforge-memory=claude-v1';
const claudeMemoryRunnerMarker = '--threadforge-managed-claude-memory=v1';
export const claudePlanGateManagedMarker = '--threadforge-plan-gate=claude-v1';
const claudePlanGateRunnerMarker = '--threadforge-managed-claude-plan-gate=v1';

export interface ClaudeHookInstallOptions {
  /** Complete shell command Claude should run. Include the managed marker argument. */
  command: string;
}

type RecordValue = Record<string, unknown>;
type Result = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
const maxSettingsBytes = 8 << 20;

const hookGroups: Record<string, Array<{ matcher?: string; hooks: Array<{ type: 'command'; command: string }> }>> = {
  SessionStart: [{ hooks: [{ type: 'command', command: '' }] }],
  UserPromptSubmit: [{ hooks: [{ type: 'command', command: '' }] }],
  PreToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
  PostToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
  PostToolUseFailure: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
  Stop: [{ hooks: [{ type: 'command', command: '' }] }],
  SubagentStart: [{ hooks: [{ type: 'command', command: '' }] }],
  SubagentStop: [{ hooks: [{ type: 'command', command: '' }] }],
  PermissionRequest: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
  SessionEnd: [{ hooks: [{ type: 'command', command: '' }] }],
};

/** Installs one non-blocking observational command in project-level Claude hooks. */
export async function installClaudeProjectHooks(projectRoot: string, options: ClaudeHookInstallOptions): Promise<Result> {
  validateCommand(options.command);
  return mutateClaudeSettings(projectRoot, (settings) => {
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudeManagedMarker);
    const next: RecordValue = { ...cleaned.value };
    for (const [event, groups] of Object.entries(hookGroups)) {
      const current = readEventGroups(next, event);
      const additions = groups.map((group) => ({
        ...group,
        hooks: group.hooks.map((hook) => ({ ...hook, command: options.command })),
      }));
      next[event] = [...current, ...additions];
    }
    settings.hooks = next;
    const result: Result = cleaned.found ? 'updated' : 'installed';
    return { result: deepEqual(cleaned.original, next) ? 'unchanged' : result, changed: !deepEqual(hooks, next) };
  });
}

/** Removes only command hook references carrying Threadforge's explicit ownership marker. */
export async function removeClaudeProjectHooks(projectRoot: string): Promise<Result> {
  return mutateClaudeSettings(projectRoot, (settings) => {
    if (settings.hooks === undefined || settings.hooks === null) return { result: 'absent', changed: false };
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudeManagedMarker);
    if (!cleaned.found) return { result: 'absent', changed: false };
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    return { result: 'removed', changed: true };
  });
}

/** Installs approved-memory retrieval as a separate, non-blocking prompt hook. */
export async function installClaudeProjectMemoryHook(projectRoot: string, command: string): Promise<Result> {
  validateCommand(command, claudeMemoryManagedMarker);
  if (!command.includes(claudeMemoryRunnerMarker)) {
    throw new Error(`Claude memory command must include the runtime marker ${claudeMemoryRunnerMarker}`);
  }
  return mutateClaudeSettings(projectRoot, (settings) => {
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudeMemoryManagedMarker);
    const next: RecordValue = { ...cleaned.value };
    const current = readEventGroups(next, 'UserPromptSubmit');
    next.UserPromptSubmit = [...current, { hooks: [{ type: 'command', command }] }];
    settings.hooks = next;
    const result: Result = cleaned.found ? 'updated' : 'installed';
    return { result: deepEqual(cleaned.original, next) ? 'unchanged' : result, changed: !deepEqual(hooks, next) };
  });
}

/** Removes only the Threadforge project-memory prompt hook, leaving telemetry and user hooks intact. */
export async function removeClaudeProjectMemoryHook(projectRoot: string): Promise<Result> {
  return mutateClaudeSettings(projectRoot, (settings) => {
    if (settings.hooks === undefined || settings.hooks === null) return { result: 'absent', changed: false };
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudeMemoryManagedMarker);
    if (!cleaned.found) return { result: 'absent', changed: false };
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    return { result: 'removed', changed: true };
  });
}

/** Installs an opt-in deny-only task-plan guard for Claude's direct Write/Edit tools. */
export async function installClaudeProjectPlanGateHook(projectRoot: string, command: string): Promise<Result> {
  validateCommand(command, claudePlanGateManagedMarker);
  if (!command.includes(claudePlanGateRunnerMarker)) {
    throw new Error('Claude plan-gate command must include the runtime marker ' + claudePlanGateRunnerMarker);
  }
  return mutateClaudeSettings(projectRoot, (settings) => {
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudePlanGateManagedMarker);
    const next: RecordValue = { ...cleaned.value };
    next.PreToolUse = [
      ...readEventGroups(next, 'PreToolUse'),
      { matcher: 'Write|Edit', hooks: [{ type: 'command', command }] },
    ];
    settings.hooks = next;
    const result: Result = cleaned.found ? 'updated' : 'installed';
    return { result: deepEqual(cleaned.original, next) ? 'unchanged' : result, changed: !deepEqual(hooks, next) };
  });
}

/** Removes only the Threadforge plan-gate reference and preserves other PreToolUse hooks. */
export async function removeClaudeProjectPlanGateHook(projectRoot: string): Promise<Result> {
  return mutateClaudeSettings(projectRoot, (settings) => {
    if (settings.hooks === undefined || settings.hooks === null) return { result: 'absent', changed: false };
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, claudePlanGateManagedMarker);
    if (!cleaned.found) return { result: 'absent', changed: false };
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    return { result: 'removed', changed: true };
  });
}

function removeManagedHooks(hooks: RecordValue, marker: string): { value: RecordValue; found: boolean; original: RecordValue } {
  const result: RecordValue = { ...hooks };
  let found = false;
  for (const [event, rawGroups] of Object.entries(hooks)) {
    if (rawGroups === null) continue;
    if (!Array.isArray(rawGroups)) throw new Error(`Claude hooks.${event} must be an array; no changes were made`);
    const retainedGroups: unknown[] = [];
    for (const rawGroup of rawGroups) {
      if (!isRecord(rawGroup)) throw new Error(`Claude hooks.${event} contains an unsupported group; no changes were made`);
      if (!Array.isArray(rawGroup.hooks)) throw new Error(`Claude hooks.${event} group has no hooks array; no changes were made`);
      const retainedRefs = rawGroup.hooks.filter((rawRef) => {
        if (!isRecord(rawRef)) throw new Error(`Claude hooks.${event} contains an unsupported hook reference; no changes were made`);
        const managed = rawRef.type === 'command' && typeof rawRef.command === 'string' && rawRef.command.includes(marker);
        if (managed) found = true;
        return !managed;
      });
      if (retainedRefs.length === rawGroup.hooks.length || retainedRefs.length > 0) {
        retainedGroups.push(retainedRefs.length === rawGroup.hooks.length ? rawGroup : { ...rawGroup, hooks: retainedRefs });
      }
    }
    if (retainedGroups.length === rawGroups.length && retainedGroups.every((group, index) => group === rawGroups[index])) continue;
    if (retainedGroups.length === 0) delete result[event];
    else result[event] = retainedGroups;
  }
  return { value: result, found, original: hooks };
}

function readHookMap(settings: RecordValue): RecordValue {
  if (settings.hooks === undefined || settings.hooks === null) return {};
  if (!isRecord(settings.hooks)) throw new Error('Claude settings hooks must be an object; no changes were made');
  return settings.hooks;
}

function readEventGroups(hooks: RecordValue, event: string): unknown[] {
  const value = hooks[event];
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`Claude hooks.${event} must be an array; no changes were made`);
  return value;
}

async function mutateClaudeSettings(projectRoot: string, mutate: (settings: RecordValue) => { result: Result; changed: boolean }): Promise<Result> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const canonicalRoot = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(canonicalRoot);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
  const settingsDir = path.join(canonicalRoot, '.claude');
  try {
    await mkdir(settingsDir);
  } catch (error) {
    if (!isCode(error, 'EEXIST')) throw new Error('unable to create Claude project settings directory');
  }
  const dirStat = await lstat(settingsDir).catch(() => null);
  if (!dirStat?.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Claude project settings path contains a symbolic link or non-directory');
  const filePath = path.join(settingsDir, 'settings.json');
  const lockPath = path.join(settingsDir, '.threadforge-settings.lock');
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Claude project settings are locked by another writer or a stale lock');
    throw new Error('unable to lock Claude project settings');
  }
  try {
    const settings = await readSettings(filePath);
    const outcome = mutate(settings);
    if (!outcome.changed) return outcome.result;
    await atomicWrite(filePath, settings);
    return outcome.result;
  } finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readSettings(filePath: string): Promise<RecordValue> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Claude settings must be a regular file');
    if (stat.size > maxSettingsBytes) throw new Error('Claude settings exceed the local size limit');
    const text = (await readFile(filePath, 'utf8')).replace(/^\uFEFF/, '');
    const value: unknown = JSON.parse(text);
    if (!isRecord(value)) throw new Error('settings root must be an object');
    return value;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('Claude settings')) throw error;
    throw new Error('Claude project settings are invalid strict JSON; no changes were made');
  }
}

async function atomicWrite(filePath: string, value: RecordValue): Promise<void> {
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
    throw new Error('unable to atomically update Claude project settings');
  }
}

function validateCommand(command: string, marker = claudeManagedMarker): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Claude hook command is invalid or too long');
  }
  if (!command.includes(marker)) {
    throw new Error(`Claude hook command must include the ownership marker ${marker}`);
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

function isRecord(value: unknown): value is RecordValue {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
