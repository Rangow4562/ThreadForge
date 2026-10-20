import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { managedMcpArgument } from './mcp-contract.ts';
import { qwenManagedMarker } from './qwen-hook.ts';

export const qwenMemoryManagedMarker = '--threadforge-memory=qwen-v1';
const qwenMemoryRunnerMarker = '--threadforge-managed-qwen-memory=v1';
export const qwenPlanGateManagedMarker = '--threadforge-plan-gate=qwen-v1';
const qwenPlanGateRunnerMarker = '--threadforge-managed-qwen-plan-gate=v1';

export type QwenHookScope = 'user' | 'project';
export type QwenHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

export interface QwenHookConfigOptions {
  command: string;
  scope?: QwenHookScope;
  projectRoot?: string;
  homeDir?: string;
}

export interface QwenMcpServerConfig {
  command: string;
  args: string[];
}

const maxSettingsBytes = 8 << 20;
const lockName = '.threadforge-qwen-hooks.lock';
const allToolsMatcher = '*';
const hookEvents: Record<string, { matcher?: string; timeout?: number }> = {
  SessionStart: {},
  UserPromptSubmit: { timeout: 30_000 },
  PreToolUse: { matcher: allToolsMatcher, timeout: 10_000 },
  PostToolUse: { matcher: allToolsMatcher, timeout: 10_000 },
  PostToolUseFailure: { matcher: allToolsMatcher, timeout: 10_000 },
  PermissionRequest: { matcher: allToolsMatcher, timeout: 10_000 },
  Stop: { timeout: 45_000 },
  SubagentStart: {},
  SubagentStop: {},
  SessionEnd: {},
};

/** Adds one local project MCP server while sharing the lock used by Qwen hooks and memory. */
export async function installQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
  server: QwenMcpServerConfig,
): Promise<QwenHookChange> {
  return mutateQwenProjectMcpServer(projectRoot, serverName, { action: 'install', server });
}

/** Compare-and-swap update for callers that already read the current Qwen MCP entry. */
export async function updateQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
  expectedCurrent: unknown,
  next: QwenMcpServerConfig,
): Promise<QwenHookChange> {
  return mutateQwenProjectMcpServer(projectRoot, serverName, {
    action: 'update', server: next, expectedCurrent, expectedMode: 'compare-and-swap',
  });
}

/** Updates only a Qwen MCP entry that still carries Threadforge's ownership marker. */
export async function updateManagedQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
  next: QwenMcpServerConfig,
): Promise<QwenHookChange> {
  return mutateQwenProjectMcpServer(projectRoot, serverName, { action: 'update', server: next, expectedMode: 'managed' });
}

/** Compare-and-swap removal for callers that already read the current Qwen MCP entry. */
export async function removeQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
  expectedCurrent: unknown,
): Promise<QwenHookChange> {
  return mutateQwenProjectMcpServer(projectRoot, serverName, {
    action: 'remove', expectedCurrent, expectedMode: 'compare-and-swap',
  });
}

/** Removes only a Qwen MCP entry that still carries Threadforge's ownership marker. */
export async function removeManagedQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
): Promise<QwenHookChange> {
  return mutateQwenProjectMcpServer(projectRoot, serverName, { action: 'remove', expectedMode: 'managed' });
}

type JsonObject = Record<string, unknown>;
interface FileSnapshot {
  identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
}

/** Installs Qwen hooks without changing its global hook-disable or project-trust controls. */
export async function installQwenHooks(options: QwenHookConfigOptions): Promise<QwenHookChange> {
  validateCommand(options.command);
  const scope = options.scope ?? 'user';
  const filePath = await settingsPath(scope, options.projectRoot, options.homeDir, true);
  if (!filePath) throw new Error('unable to resolve Qwen settings path');
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    const before = JSON.stringify(canonicalize(settings));
    if (settings.disableAllHooks === true) throw new Error('Qwen disableAllHooks is true; enable hooks in Qwen settings before installing Threadforge');
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks);
    const next: JsonObject = { ...cleaned.value };
    for (const [event, eventOptions] of Object.entries(hookEvents)) {
      const groups = readEventGroups(next, event);
      // Use a separate object for each event and include Qwen's millisecond timeout only where
      // the host contract explicitly overrides its 60000 ms default.
      const hookRef: JsonObject = {
        type: 'command', shell: 'bash', command: options.command,
        ...(eventOptions.timeout === undefined ? {} : { timeout: eventOptions.timeout }),
      };
      const group: JsonObject = {
        ...(eventOptions.matcher === undefined ? {} : { matcher: eventOptions.matcher }),
        hooks: [hookRef],
      };
      next[event] = appendToEmptyGroup(groups, group, hookRef, eventOptions.matcher);
    }
    settings.hooks = next;
    if (before === JSON.stringify(canonicalize(settings))) return 'unchanged';
    await atomicWrite(filePath, read.snapshot, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

export async function removeQwenHooks(options: Omit<QwenHookConfigOptions, 'command'> = {}): Promise<QwenHookChange> {
  const scope = options.scope ?? 'user';
  const filePath = await settingsPath(scope, options.projectRoot, options.homeDir, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    if (settings.hooks === undefined || settings.hooks === null) return 'absent';
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    await atomicWrite(filePath, read.snapshot, settings);
    return 'removed';
  });
}

/** Installs a separate project-only UserPromptExpansion memory hook. */
export async function installQwenProjectMemoryHook(options: { command: string; projectRoot: string }): Promise<QwenHookChange> {
  validateQwenMemoryCommand(options.command);
  const filePath = await settingsPath('project', options.projectRoot, undefined, true);
  if (!filePath) throw new Error('unable to resolve Qwen project settings path');
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    if (settings.disableAllHooks === true) throw new Error('Qwen disableAllHooks is true; enable hooks in Qwen settings before installing Threadforge memory');
    const before = JSON.stringify(canonicalize(settings));
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, qwenMemoryManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    const groups = readEventGroups(next, 'UserPromptExpansion');
    const hookRef: JsonObject = {
      type: 'command', shell: 'bash', command: options.command, timeout: 30_000,
    };
    next.UserPromptExpansion = [...groups, { hooks: [hookRef] }];
    settings.hooks = next;
    if (before === JSON.stringify(canonicalize(settings))) return 'unchanged';
    await atomicWrite(filePath, read.snapshot, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only Threadforge's project-scoped prompt-expansion memory hook. */
export async function removeQwenProjectMemoryHook(projectRoot: string): Promise<QwenHookChange> {
  const filePath = await settingsPath('project', projectRoot, undefined, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    if (settings.hooks === undefined || settings.hooks === null) return 'absent';
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, qwenMemoryManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    await atomicWrite(filePath, read.snapshot, settings);
    return 'removed';
  });
}

/** Installs a project-only deny-only guard for Qwen's direct write_file/edit tools. */
export async function installQwenProjectPlanGateHook(options: { command: string; projectRoot: string }): Promise<QwenHookChange> {
  validateQwenPlanGateCommand(options.command);
  const filePath = await settingsPath('project', options.projectRoot, undefined, true);
  if (!filePath) throw new Error('unable to resolve Qwen project settings path');
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    if (settings.disableAllHooks === true) throw new Error('Qwen disableAllHooks is true; enable hooks in Qwen settings before installing Threadforge plan gate');
    const before = JSON.stringify(canonicalize(settings));
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, qwenPlanGateManagedMarker);
    const next: JsonObject = { ...cleaned.value };
    const groups = readEventGroups(next, 'PreToolUse');
    const hookRef: JsonObject = {
      type: 'command', shell: 'bash', command: options.command, timeout: 10,
    };
    next.PreToolUse = [...groups, { matcher: '^(write_file|edit)$', hooks: [hookRef] }];
    settings.hooks = next;
    if (before === JSON.stringify(canonicalize(settings))) return 'unchanged';
    await atomicWrite(filePath, read.snapshot, settings);
    return cleaned.found ? 'updated' : 'installed';
  });
}

/** Removes only the Threadforge project-scoped Qwen plan-gate hook. */
export async function removeQwenProjectPlanGateHook(projectRoot: string): Promise<QwenHookChange> {
  const filePath = await settingsPath('project', projectRoot, undefined, false);
  if (!filePath) return 'absent';
  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    if (settings.hooks === undefined || settings.hooks === null) return 'absent';
    const hooks = readHookMap(settings);
    const cleaned = removeManagedHooks(hooks, qwenPlanGateManagedMarker);
    if (!cleaned.found) return 'absent';
    if (Object.keys(cleaned.value).length === 0) delete settings.hooks;
    else settings.hooks = cleaned.value;
    await atomicWrite(filePath, read.snapshot, settings);
    return 'removed';
  });
}

export function qwenSettingsPath(scope: QwenHookScope, root: string): string {
  return path.join(root, '.qwen', 'settings.json');
}

async function settingsPath(
  scope: QwenHookScope,
  projectRoot: string | undefined,
  homeDir: string | undefined,
  create: boolean,
): Promise<string | undefined> {
  if (scope !== 'user' && scope !== 'project') throw new Error('Qwen hook scope must be user or project');
  let root: string;
  if (scope === 'project') {
    if (!projectRoot || !path.isAbsolute(projectRoot)) throw new Error('project scope requires an absolute project root');
    root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  } else {
    const home = homeDir ?? os.homedir();
    if (!path.isAbsolute(home)) throw new Error('Qwen home directory must be absolute');
    root = await realpath(home).catch(() => { throw new Error('Qwen home directory must exist and be accessible'); });
  }
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('Qwen settings root must be a real directory');
  const qwenDir = path.join(root, '.qwen');
  if (create) {
    try { await mkdir(qwenDir, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Qwen settings directory'); }
  }
  const dirStat = await lstat(qwenDir).catch((error: unknown) => {
    if (!create && isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!dirStat) return undefined;
  if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Qwen settings path contains a symbolic link or non-directory');
  const actualDir = await realpath(qwenDir).catch(() => { throw new Error('Qwen settings directory is inaccessible'); });
  if (!isWithin(root, actualDir)) throw new Error('Qwen settings directory resolves outside its selected root');
  const filePath = path.join(actualDir, 'settings.json');
  const fileStat = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (fileStat && (!fileStat.isFile() || fileStat.isSymbolicLink())) throw new Error('Qwen settings file must be regular and not a symbolic link');
  if (fileStat && fileStat.size > maxSettingsBytes) throw new Error('Qwen settings exceed the local size limit');
  return fileStat || create ? filePath : undefined;
}

type QwenMcpMutation =
  | { action: 'install'; server: QwenMcpServerConfig }
  | { action: 'update'; server: QwenMcpServerConfig; expectedMode: 'managed' | 'compare-and-swap'; expectedCurrent?: unknown }
  | { action: 'remove'; expectedMode: 'managed' | 'compare-and-swap'; expectedCurrent?: unknown };

async function mutateQwenProjectMcpServer(
  projectRoot: string,
  serverName: string,
  mutation: QwenMcpMutation,
): Promise<QwenHookChange> {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(serverName)) throw new Error('Qwen MCP server name is invalid');
  if (mutation.action !== 'remove') await validateQwenMcpServer(projectRoot, mutation.server);

  const filePath = await settingsPath('project', projectRoot, undefined, mutation.action === 'install');
  if (!filePath) {
    if (mutation.action === 'remove') return 'absent';
    throw new Error('Qwen project MCP settings are absent; refusing to create them during update');
  }

  return withLock(filePath, async () => {
    const read = await readSettings(filePath);
    const settings = read.value;
    const servers = readQwenMcpServerMap(settings);
    const exists = Object.hasOwn(servers, serverName);
    const current = servers[serverName];

    if (mutation.action === 'install') {
      if (exists) {
        if (isManagedQwenMcpServer(current) && deepEqual(current, mutation.server)) return 'unchanged';
        throw new Error(`Qwen MCP server '${serverName}' already exists with different settings; refusing to overwrite it`);
      }
      servers[serverName] = mutation.server;
      settings.mcpServers = servers;
      await atomicWrite(filePath, read.snapshot, settings);
      return 'installed';
    }

    if (!exists) {
      if (mutation.action === 'remove') return 'absent';
      throw new Error(`Qwen MCP server '${serverName}' is absent; refusing to update it`);
    }
    if (!isManagedQwenMcpServer(current)) {
      throw new Error(`Qwen MCP server '${serverName}' is not marked as Threadforge-managed`);
    }
    if (mutation.expectedMode === 'compare-and-swap' &&
        (!isManagedQwenMcpServer(mutation.expectedCurrent) || !deepEqual(current, mutation.expectedCurrent))) {
      throw new Error(`Qwen MCP server '${serverName}' changed since it was read; refusing to overwrite it`);
    }

    if (mutation.action === 'update') {
      if (deepEqual(current, mutation.server)) return 'unchanged';
      servers[serverName] = mutation.server;
      settings.mcpServers = servers;
      await atomicWrite(filePath, read.snapshot, settings);
      return 'updated';
    }

    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete settings.mcpServers;
    else settings.mcpServers = servers;
    await atomicWrite(filePath, read.snapshot, settings);
    return 'removed';
  });
}

function readQwenMcpServerMap(settings: JsonObject): JsonObject {
  if (settings.mcpServers === undefined) return {};
  if (!isRecord(settings.mcpServers)) throw new Error('Qwen settings mcpServers must be an object; no changes were made');
  return settings.mcpServers;
}

function isManagedQwenMcpServer(value: unknown): value is QwenMcpServerConfig {
  return isRecord(value) && Array.isArray(value.args) && value.args.includes(managedMcpArgument);
}

async function validateQwenMcpServer(projectRoot: string, value: QwenMcpServerConfig): Promise<void> {
  if (!isRecord(value) || Object.keys(value).some((key) => key !== 'command' && key !== 'args')) {
    throw new Error('Qwen project MCP entries only accept command and args; trust, cwd, and environment are not managed');
  }
  if (typeof value.command !== 'string' || !value.command.trim() || value.command.length > 2048 || /[\r\n\0]/.test(value.command)) {
    throw new Error('Qwen MCP command is invalid or too long');
  }
  if (looksSecretLike(value.command)) throw new Error('Qwen MCP command must not contain credential-like values');
  if (!Array.isArray(value.args) || value.args.length < 5 || value.args.length > 128 ||
      value.args.some((arg) => typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg) || looksSecretLike(arg))) {
    throw new Error('Qwen MCP arguments must be bounded, credential-free strings');
  }
  if (value.args[0] !== 'mcp' || value.args[1] !== 'serve' || value.args[2] !== '--project-root') {
    throw new Error('Qwen MCP arguments must begin with mcp serve --project-root <same-project-root>');
  }
  const rootArgument = value.args[3];
  if (!rootArgument || !path.isAbsolute(rootArgument) || value.args[value.args.length - 1] !== managedMcpArgument ||
      value.args.filter((arg) => arg === managedMcpArgument).length !== 1) {
    throw new Error('Qwen MCP arguments must bind the project root and end with the single Threadforge ownership marker');
  }
  const selectedRoot = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const configuredRoot = await realpath(rootArgument).catch(() => { throw new Error('Qwen MCP --project-root must match the selected project root'); });
  const normalize = (value: string) => path.resolve(value).replace(/[\\/]+$/, '');
  const same = process.platform === 'win32'
    ? normalize(selectedRoot).toLowerCase() === normalize(configuredRoot).toLowerCase()
    : normalize(selectedRoot) === normalize(configuredRoot);
  if (!same) throw new Error('Qwen MCP --project-root must match the selected project root');
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Qwen settings are locked by another writer or a stale lock');
    throw new Error('unable to lock Qwen settings');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readSettings(filePath: string): Promise<{ value: JsonObject; snapshot: FileSnapshot }> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Qwen settings file must be regular and not a symbolic link');
    if (stat.size > maxSettingsBytes) throw new Error('Qwen settings exceed the local size limit');
    const bytes = await readFile(filePath);
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
    const value: unknown = JSON.parse(text);
    if (!isRecord(value)) throw new Error('Qwen settings root must be an object');
    return { value, snapshot: { identity: { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs } } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { value: {}, snapshot: {} };
    if (error instanceof Error && error.message.startsWith('Qwen settings')) throw error;
    throw new Error('Qwen settings are invalid UTF-8 strict JSON; no changes were made');
  }
}

function readHookMap(settings: JsonObject): JsonObject {
  if (settings.hooks === undefined || settings.hooks === null) return {};
  if (!isRecord(settings.hooks)) throw new Error('Qwen settings hooks must be an object; no changes were made');
  return settings.hooks;
}

function readEventGroups(hooks: JsonObject, event: string): unknown[] {
  const current = hooks[event];
  if (current === undefined || current === null) return [];
  if (!Array.isArray(current) || current.some((group) => !isRecord(group) || !Array.isArray(group.hooks))) {
    throw new Error(`Qwen hooks.${event} must contain hook groups; no changes were made`);
  }
  return current;
}

function appendToEmptyGroup(groups: unknown[], desired: JsonObject, hook: JsonObject, matcher?: string): unknown[] {
  const index = groups.findIndex((group) => isRecord(group) && Array.isArray(group.hooks) && group.hooks.length === 0 &&
    (matcher === undefined ? !Object.hasOwn(group, 'matcher') : group.matcher === matcher));
  if (index < 0) return [...groups, desired];
  return groups.map((group, current) => current === index && isRecord(group) ? { ...group, hooks: [hook] } : group);
}

function removeManagedHooks(hooks: JsonObject, marker = qwenManagedMarker): { value: JsonObject; found: boolean } {
  const value: JsonObject = { ...hooks };
  let found = false;
  for (const [event, rawGroups] of Object.entries(hooks)) {
    if (rawGroups === null) continue;
    if (!Array.isArray(rawGroups)) throw new Error(`Qwen hooks.${event} must be an array; no changes were made`);
    const keptGroups: unknown[] = [];
    for (const group of rawGroups) {
      if (!isRecord(group) || !Array.isArray(group.hooks)) throw new Error(`Qwen hooks.${event} contains an unsupported group; no changes were made`);
      const keptRefs = group.hooks.filter((hook) => {
        if (!isRecord(hook)) throw new Error(`Qwen hooks.${event} contains an unsupported hook ref; no changes were made`);
        const managed = hook.type === 'command' && typeof hook.command === 'string' && hook.command.includes(marker);
        found ||= managed;
        return !managed;
      });
      if (keptRefs.length > 0) keptGroups.push(keptRefs.length === group.hooks.length ? group : { ...group, hooks: keptRefs });
      else if (Object.keys(group).some((key) => key !== 'hooks' && key !== 'matcher')) keptGroups.push({ ...group, hooks: [] });
    }
    if (keptGroups.length === 0) delete value[event];
    else value[event] = keptGroups;
  }
  return { value, found };
}

async function atomicWrite(filePath: string, snapshot: FileSnapshot, value: JsonObject): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxSettingsBytes) throw new Error('Qwen settings would exceed the local size limit');
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
    throw new Error('unable to atomically update Qwen settings');
  }
}

async function assertUnchanged(filePath: string, snapshot: FileSnapshot): Promise<void> {
  const current = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (current) throw new Error('Qwen settings appeared during update');
    return;
  }
  if (!current || !current.isFile() || current.isSymbolicLink() || current.dev !== snapshot.identity.dev ||
      current.ino !== snapshot.identity.ino || current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Qwen settings changed during update');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Qwen hook command is invalid or too long');
  }
  if (!command.includes(qwenManagedMarker)) throw new Error(`Qwen hook command must include ${qwenManagedMarker}`);
  if (looksSecretLike(command)) throw new Error('Qwen hook command must not contain credential-like values');
}

function validateQwenMemoryCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Qwen memory hook command is invalid or too long');
  }
  if (!command.includes(qwenMemoryRunnerMarker) || !command.includes(qwenMemoryManagedMarker)) {
    throw new Error(`Qwen memory hook command must include ${qwenMemoryRunnerMarker} and ${qwenMemoryManagedMarker}`);
  }
  if (looksSecretLike(command)) throw new Error('Qwen memory hook command must not contain credential-like values');
}

function validateQwenPlanGateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Qwen plan-gate hook command is invalid or too long');
  }
  if (!command.includes(qwenPlanGateRunnerMarker) || !command.includes(qwenPlanGateManagedMarker)) {
    throw new Error(`Qwen plan-gate command must include ${qwenPlanGateRunnerMarker} and ${qwenPlanGateManagedMarker}`);
  }
  if (looksSecretLike(command)) throw new Error('Qwen plan-gate hook command must not contain credential-like values');
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

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
