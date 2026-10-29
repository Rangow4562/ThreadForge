import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const copilotPlanGateManagedMarker = '--threadforge-plan-gate=copilot-v1';
const copilotPlanGateRunnerMarker = '--threadforge-managed-copilot-plan-gate=v1';
const configName = 'threadforge-plan-gate.json';
export const copilotMemoryManagedMarker = '--threadforge-memory=copilot-v1';
const memoryConfigName = 'threadforge-memory.json';
const lockName = '.threadforge-plan-gate.lock';
const memoryLockName = '.threadforge-memory.lock';
const maxConfigBytes = 2 << 20;
const planGateMatcher = '^(create|edit|apply_patch|str_replace_editor)$';

type JsonObject = Record<string, unknown>;
export type CopilotPlanGateChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

interface FileSnapshot {
  bytes: Buffer | null;
  identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
}

interface ReadConfig {
  value: JsonObject;
  snapshot: FileSnapshot;
}

/** Installs a project-only Copilot CLI preToolUse guard in its own hooks file. */
export async function installCopilotProjectPlanGateHook(options: {
  command?: string;
  bashCommand?: string;
  powershellCommand?: string;
  projectRoot: string;
}): Promise<CopilotPlanGateChange> {
  const bashCommand = options.bashCommand ?? options.command;
  const powershellCommand = options.powershellCommand ?? options.command;
  if (!bashCommand || !powershellCommand) {
    throw new Error('Copilot plan-gate requires --command or both bashCommand and powershellCommand');
  }
  validateCommand(bashCommand);
  validateCommand(powershellCommand);
  const filePath = await hooksConfigPath(options.projectRoot, true);
  if (!filePath) throw new Error('unable to resolve Copilot plan-gate hooks path');
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const settings = read.value;
    if (settings.version !== undefined && settings.version !== 1) {
      throw new Error('Copilot hooks file has an unsupported version; no changes were made');
    }
    const before = JSON.stringify(canonicalize(settings));
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks);
    const nextHooks: JsonObject = { ...cleaned.value };
    const current = readEventHooks(nextHooks, 'preToolUse');
    nextHooks.preToolUse = [...current, {
      type: 'command',
      bash: bashCommand,
      powershell: powershellCommand,
      matcher: planGateMatcher,
      timeoutSec: 10,
    }];
    settings.version = 1;
    settings.hooks = nextHooks;
    if (before === JSON.stringify(canonicalize(settings))) return 'unchanged';
    await atomicWrite(filePath, read.snapshot, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only Threadforge-owned references from the dedicated project hooks file. */
export async function removeCopilotProjectPlanGateHook(projectRoot: string): Promise<CopilotPlanGateChange> {
  const filePath = await hooksConfigPath(projectRoot, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const hooks = readHookMap(read.value);
    const cleaned = removeManagedHooks(hooks);
    if (!cleaned.found) return 'absent';
    const next = { ...read.value };
    if (Object.keys(cleaned.value).length === 0) delete next.hooks;
    else next.hooks = cleaned.value;
    await atomicWrite(filePath, read.snapshot, next);
    return 'removed';
  });
}

/** Removes only Threadforge-owned Copilot prompt-memory hooks. */
export async function removeCopilotProjectMemoryHook(projectRoot: string): Promise<CopilotPlanGateChange> {
  const filePath = await hooksConfigPath(projectRoot, false, memoryConfigName);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readConfig(filePath);
    const hooks = readHookMap(read.value);
    const cleaned = removeManagedHooks(hooks, copilotMemoryManagedMarker);
    if (!cleaned.found) return 'absent';
    const next = { ...read.value };
    if (Object.keys(cleaned.value).length === 0) delete next.hooks;
    else next.hooks = cleaned.value;
    await atomicWrite(filePath, read.snapshot, next);
    return 'removed';
  }, memoryLockName);
}

export function copilotProjectHooksConfigPath(projectRoot: string): string {
  return path.join(projectRoot, '.github', 'hooks', configName);
}

export function copilotProjectMemoryHooksConfigPath(projectRoot: string): string {
  return path.join(projectRoot, '.github', 'hooks', memoryConfigName);
}

async function hooksConfigPath(projectRoot: string, create: boolean, fileName = configName): Promise<string | undefined> {
  if (!path.isAbsolute(projectRoot)) throw new Error('Copilot project root must be absolute');
  const suppliedRootStat = await lstat(projectRoot).catch(() => null);
  if (!suppliedRootStat?.isDirectory() || suppliedRootStat.isSymbolicLink()) {
    throw new Error('Copilot project root must be a real directory');
  }
  const root = await realpath(projectRoot).catch(() => { throw new Error('Copilot project root must exist'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('Copilot project root must be a real directory');

  let parent = root;
  for (const segment of ['.github', 'hooks']) {
    const directory = path.join(parent, segment);
    if (create) {
      try { await mkdir(directory, { mode: 0o700 }); }
      catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Copilot hooks directory'); }
    }
    const stat = await lstat(directory).catch((error: unknown) => {
      if (!create && isCode(error, 'ENOENT')) return null;
      throw error;
    });
    if (!stat) return undefined;
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error('Copilot hooks path contains a symbolic link or non-directory');
    const resolved = await realpath(directory).catch(() => { throw new Error('Copilot hooks directory is inaccessible'); });
    if (!isWithin(root, resolved)) throw new Error('Copilot hooks directory resolves outside the project');
    parent = resolved;
  }

  const filePath = path.join(parent, fileName);
  const stat = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (stat && (!stat.isFile() || stat.isSymbolicLink())) throw new Error('Copilot hooks file must be regular and not a symbolic link');
  if (stat && stat.size > maxConfigBytes) throw new Error('Copilot hooks file exceeds the size limit');
  return filePath;
}

async function withLock<T>(filePath: string, action: () => Promise<T>, lockFileName = lockName): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), lockFileName);
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Copilot hooks file is locked by another writer or a stale lock');
    throw new Error('unable to lock Copilot hooks file');
  }
  try {
    return await action();
  } finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<ReadConfig> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Copilot hooks file must be a regular file');
    if (stat.size > maxConfigBytes) throw new Error('Copilot hooks file exceeds the size limit');
    const bytes = await readFile(filePath);
    let value: unknown;
    try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
    catch { throw new Error('Copilot hooks file is not valid UTF-8 JSON; no changes were made'); }
    if (!isRecord(value)) throw new Error('Copilot hooks file root must be an object; no changes were made');
    return {
      value,
      snapshot: {
        bytes,
        identity: { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs },
      },
    };
  } catch (error) {
    if (isCode(error, 'ENOENT')) {
      return { value: {}, snapshot: { bytes: null } };
    }
    throw error;
  }
}

function readHookMap(settings: JsonObject): JsonObject {
  if (settings.hooks === undefined) return {};
  if (!isRecord(settings.hooks)) throw new Error('Copilot hooks must be an object; no changes were made');
  for (const [event, value] of Object.entries(settings.hooks)) {
    if (!Array.isArray(value) || value.some((entry) => !isRecord(entry))) {
      throw new Error(`Copilot hooks.${event} must be an array of hook objects; no changes were made`);
    }
  }
  return settings.hooks;
}

function readEventHooks(hooks: JsonObject, event: string): JsonObject[] {
  const value = hooks[event];
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.some((entry) => !isRecord(entry))) {
    throw new Error(`Copilot hooks.${event} must be an array of hook objects; no changes were made`);
  }
  return value;
}

function removeManagedHooks(hooks: JsonObject, marker = copilotPlanGateManagedMarker): { value: JsonObject; found: boolean } {
  const result: JsonObject = { ...hooks };
  let found = false;
  for (const [event, entries] of Object.entries(hooks)) {
    if (!Array.isArray(entries)) throw new Error(`Copilot hooks.${event} must be an array; no changes were made`);
    let eventFound = false;
    const retained = entries.filter((entry) => {
      if (!isRecord(entry)) throw new Error(`Copilot hooks.${event} contains an unsupported hook; no changes were made`);
      const managed = entry.type === 'command' &&
        ['command', 'bash', 'powershell', 'exec'].some((key) =>
          typeof entry[key] === 'string' && (entry[key] as string).includes(marker));
      found ||= managed;
      eventFound ||= managed;
      return !managed;
    });
    if (eventFound && retained.length === 0) delete result[event];
    else if (eventFound) result[event] = retained;
  }
  return { value: result, found };
}

async function atomicWrite(filePath: string, snapshot: FileSnapshot, value: JsonObject): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Copilot hooks file would exceed the size limit');
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
    throw new Error('unable to atomically update Copilot hooks file');
  }
}

async function assertUnchanged(filePath: string, snapshot: FileSnapshot): Promise<void> {
  const stat = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (stat) throw new Error('Copilot hooks file appeared during update');
    return;
  }
  if (!stat || !stat.isFile() || stat.isSymbolicLink() || stat.dev !== snapshot.identity.dev ||
      stat.ino !== snapshot.identity.ino || stat.size !== snapshot.identity.size || stat.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Copilot hooks file changed during update');
  }
  const current = await readFile(filePath);
  if (!snapshot.bytes?.equals(current)) throw new Error('Copilot hooks file changed during update');
}

function validateCommand(command: string, runnerMarker = copilotPlanGateRunnerMarker, managedMarker = copilotPlanGateManagedMarker): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Copilot hook command is invalid or too long');
  }
  if (!command.includes(runnerMarker) || !command.includes(managedMarker)) {
    throw new Error(`Copilot hook command must include ${runnerMarker} and ${managedMarker}`);
  }
  if (looksSecretLike(command)) throw new Error('Copilot hook command must not contain credential-like values');
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

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
