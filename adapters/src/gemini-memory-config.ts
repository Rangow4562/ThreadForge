import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const geminiMemoryManagedMarker = '--threadforge-memory=gemini-v1';
const geminiMemoryRunnerMarker = '--threadforge-managed-gemini-memory=v1';
export const geminiPlanGateManagedMarker = '--threadforge-plan-gate=gemini-v1';
const geminiPlanGateRunnerMarker = '--threadforge-managed-gemini-plan-gate=v1';
const maxSettingsBytes = 8 << 20;
const hookTimeoutMs = 10_000;
const hookName = 'threadforge-approved-memory';
const planGateHookName = 'threadforge-task-plan-gate';
const planGateMatcher = '^(write_file|replace)$';

type JsonObject = Record<string, unknown>;
export type GeminiMemoryHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Installs a project-local BeforeAgent hook for approved memory retrieval. */
export async function installGeminiProjectMemoryHook(projectRoot: string, command: string): Promise<GeminiMemoryHookChange> {
  validateCommand(command);
  return mutateGeminiSettings(projectRoot, (settings) => {
    const hooks = readHookMap(settings);
    const beforeAgent = readBeforeAgent(hooks);
    const cleaned = removeManagedMemoryHooks(beforeAgent);
    const nextGroup = {
      hooks: [{ name: hookName, type: 'command', command, timeout: hookTimeoutMs }],
    };
    const nextHooks: JsonObject = { ...hooks, BeforeAgent: [...cleaned.groups, nextGroup] };
    settings.hooks = nextHooks;
    if (deepEqual(hooks, nextHooks)) return { result: 'unchanged', changed: false };
    return { result: cleaned.found ? 'updated' : 'installed', changed: true };
  });
}

/** Removes only Threadforge's owned BeforeAgent memory command. */
export async function removeGeminiProjectMemoryHook(projectRoot: string): Promise<GeminiMemoryHookChange> {
  return mutateGeminiSettings(projectRoot, (settings) => {
    if (settings.hooks === undefined || settings.hooks === null) return { result: 'absent', changed: false };
    const hooks = readHookMap(settings);
    const beforeAgent = readBeforeAgent(hooks);
    const cleaned = removeManagedMemoryHooks(beforeAgent);
    if (!cleaned.found) return { result: 'absent', changed: false };
    const nextHooks: JsonObject = { ...hooks };
    if (cleaned.groups.length === 0) delete nextHooks.BeforeAgent;
    else nextHooks.BeforeAgent = cleaned.groups;
    if (Object.keys(nextHooks).length === 0) delete settings.hooks;
    else settings.hooks = nextHooks;
    return { result: 'removed', changed: true };
  }, false);
}

/** Installs a project-local, deny-only BeforeTool gate for direct file tools. */
export async function installGeminiProjectPlanGateHook(projectRoot: string, command: string): Promise<GeminiMemoryHookChange> {
  validatePlanGateCommand(command);
  return mutateGeminiSettings(projectRoot, (settings) => {
    const hooks = readHookMap(settings);
    const beforeTool = readBeforeTool(hooks);
    const cleaned = removeManagedPlanGateHooks(beforeTool);
    const nextGroup = {
      matcher: planGateMatcher,
      hooks: [{ name: planGateHookName, type: 'command', command, timeout: hookTimeoutMs }],
    };
    const nextHooks: JsonObject = { ...hooks, BeforeTool: [...cleaned.groups, nextGroup] };
    settings.hooks = nextHooks;
    if (deepEqual(hooks, nextHooks)) return { result: 'unchanged', changed: false };
    return { result: cleaned.found ? 'updated' : 'installed', changed: true };
  });
}

/** Removes only Threadforge's owned BeforeTool plan-gate command. */
export async function removeGeminiProjectPlanGateHook(projectRoot: string): Promise<GeminiMemoryHookChange> {
  return mutateGeminiSettings(projectRoot, (settings) => {
    if (settings.hooks === undefined || settings.hooks === null) return { result: 'absent', changed: false };
    const hooks = readHookMap(settings);
    const beforeTool = readBeforeTool(hooks);
    const cleaned = removeManagedPlanGateHooks(beforeTool);
    if (!cleaned.found) return { result: 'absent', changed: false };
    const nextHooks: JsonObject = { ...hooks };
    if (cleaned.groups.length === 0) delete nextHooks.BeforeTool;
    else nextHooks.BeforeTool = cleaned.groups;
    if (Object.keys(nextHooks).length === 0) delete settings.hooks;
    else settings.hooks = nextHooks;
    return { result: 'removed', changed: true };
  }, false);
}

async function mutateGeminiSettings(
  projectRoot: string,
  mutate: (settings: JsonObject) => { result: GeminiMemoryHookChange; changed: boolean },
  create = true,
): Promise<GeminiMemoryHookChange> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
  const settingsDir = path.join(root, '.gemini');
  let dirStat = await lstat(settingsDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Gemini project settings directory');
  });
  if (!dirStat && !create) return 'absent';
  if (!dirStat) {
    try {
      await mkdir(settingsDir);
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create Gemini project settings directory');
    }
    dirStat = await lstat(settingsDir).catch(() => null);
  }
  if (!dirStat?.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Gemini project settings path contains a symbolic link or non-directory');
  const filePath = path.join(settingsDir, 'settings.json');
  // Share the project config lock with the Gemini MCP writer, since both edit settings.json.
  const lockPath = path.join(settingsDir, '.threadforge-mcp-config.lock');
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Gemini project settings are locked by another writer or a stale lock');
    throw new Error('unable to lock Gemini project settings');
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

async function readSettings(filePath: string): Promise<JsonObject> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Gemini settings must be a regular file');
    if (stat.size > maxSettingsBytes) throw new Error('Gemini settings exceed the local size limit');
    const parsed: unknown = JSON.parse((await readFile(filePath, 'utf8')).replace(/^\uFEFF/, ''));
    if (!isRecord(parsed)) throw new Error('Gemini settings root must be an object');
    return parsed;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('Gemini settings')) throw error;
    throw new Error('Gemini project settings are invalid strict JSON; no changes were made');
  }
}

function readHookMap(settings: JsonObject): JsonObject {
  if (settings.hooks === undefined || settings.hooks === null) return {};
  if (!isRecord(settings.hooks)) throw new Error('Gemini settings hooks must be an object; no changes were made');
  return settings.hooks;
}

function readBeforeAgent(hooks: JsonObject): JsonObject[] {
  const raw = hooks.BeforeAgent;
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) throw new Error('Gemini hooks.BeforeAgent must be an array; no changes were made');
  return raw.map((group) => {
    if (!isRecord(group) || !Array.isArray(group.hooks) || group.hooks.some((hook) => !isRecord(hook))) {
      throw new Error('Gemini hooks.BeforeAgent contains an unsupported hook group; no changes were made');
    }
    return group;
  });
}

function readBeforeTool(hooks: JsonObject): JsonObject[] {
  const raw = hooks.BeforeTool;
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) throw new Error('Gemini hooks.BeforeTool must be an array; no changes were made');
  return raw.map((group) => {
    if (!isRecord(group) || !Array.isArray(group.hooks) || group.hooks.some((hook) => !isRecord(hook))) {
      throw new Error('Gemini hooks.BeforeTool contains an unsupported hook group; no changes were made');
    }
    return group;
  });
}

function removeManagedMemoryHooks(groups: JsonObject[]): { groups: JsonObject[]; found: boolean } {
  let found = false;
  const retained: JsonObject[] = [];
  for (const group of groups) {
    const hooks = group.hooks as JsonObject[];
    const kept = hooks.filter((hook) => {
      const managed = hook.type === 'command' && typeof hook.command === 'string' &&
        hook.command.includes(geminiMemoryRunnerMarker) && hook.command.includes(geminiMemoryManagedMarker);
      if (managed) found = true;
      return !managed;
    });
    if (kept.length === hooks.length) retained.push(group);
    else if (kept.length > 0) retained.push({ ...group, hooks: kept });
  }
  return { groups: retained, found };
}

function removeManagedPlanGateHooks(groups: JsonObject[]): { groups: JsonObject[]; found: boolean } {
  let found = false;
  const retained: JsonObject[] = [];
  for (const group of groups) {
    const hooks = group.hooks as JsonObject[];
    const kept = hooks.filter((hook) => {
      const managed = hook.type === 'command' && typeof hook.command === 'string' &&
        hook.command.includes(geminiPlanGateRunnerMarker) && hook.command.includes(geminiPlanGateManagedMarker);
      if (managed) found = true;
      return !managed;
    });
    if (kept.length === hooks.length) retained.push(group);
    else if (kept.length > 0) retained.push({ ...group, hooks: kept });
  }
  return { groups: retained, found };
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
    throw new Error('unable to atomically update Gemini project settings');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Gemini memory command is invalid or too long');
  }
  if (!command.includes(geminiMemoryRunnerMarker) || !command.includes(geminiMemoryManagedMarker)) {
    throw new Error(`Gemini memory command must include ${geminiMemoryRunnerMarker} and ${geminiMemoryManagedMarker}`);
  }
}

function validatePlanGateCommand(command: string): void {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Gemini plan-gate command is invalid or too long');
  }
  if (!command.includes(geminiPlanGateRunnerMarker) || !command.includes(geminiPlanGateManagedMarker)) {
    throw new Error(`Gemini plan-gate command must include ${geminiPlanGateRunnerMarker} and ${geminiPlanGateManagedMarker}`);
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
