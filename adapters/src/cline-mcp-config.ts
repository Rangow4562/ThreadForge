import { randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { looksCredentialLikeMcpValue, managedMcpArgument } from './mcp-contract.ts';

type JsonObject = Record<string, unknown>;
type Change = 'installed' | 'updated' | 'removed' | 'unchanged' | 'absent';
const maxSettingsBytes = 8 << 20;
const serverName = 'threadforge';
const managedArgs = ['mcp', 'serve', managedMcpArgument] as const;

export interface ClineMcpPathOptions {
  clineDir?: string;
  configPath?: string;
  home?: string;
  env?: NodeJS.ProcessEnv;
}

export interface ClineMcpStatus {
  result: 'installed' | 'modified' | 'absent';
  server_name: typeof serverName;
  config_path: string;
  expected_command?: string;
}

export function clineMcpSettingsPath(options: ClineMcpPathOptions = {}): string {
  const env = options.env ?? process.env;
  if (options.configPath !== undefined) return validateLocalAbsolutePath(options.configPath, 'Cline MCP settings path');

  const home = options.home ?? os.homedir();
  if (options.clineDir !== undefined) {
    const safeClineDir = validateLocalAbsolutePath(options.clineDir, 'Cline home');
    return path.join(safeClineDir, 'data', 'settings', 'cline_mcp_settings.json');
  }
  if (env.CLINE_MCP_SETTINGS_PATH !== undefined) {
    return validateLocalAbsolutePath(env.CLINE_MCP_SETTINGS_PATH, 'Cline MCP settings path');
  }
  const safeDataDir = validateLocalAbsolutePath(path.join(home, '.cline', 'data'), 'Cline data directory');
  return path.join(safeDataDir, 'settings', 'cline_mcp_settings.json');
}

export async function installClineUserMcpServer(
  command: string,
  options: ClineMcpPathOptions = {},
): Promise<Change> {
  const entry = buildEntry(command);
  return mutateSettings(options, (settings) => {
    const servers = readServers(settings);
    if (Object.hasOwn(servers, serverName)) {
      if (isOwnedEntry(servers[serverName]) && deepEqual(servers[serverName], entry)) {
        return { result: 'unchanged', changed: false };
      }
      throw new Error('Cline MCP server name is already in use; no settings were changed');
    }
    servers[serverName] = entry;
    settings.mcpServers = servers;
    return { result: 'installed', changed: true };
  });
}

export async function updateClineUserMcpServer(
  expectedCurrentCommand: string,
  command: string,
  options: ClineMcpPathOptions = {},
): Promise<Change> {
  const expected = buildEntry(expectedCurrentCommand);
  const entry = buildEntry(command);
  return mutateSettings(options, (settings) => {
    const servers = readServers(settings);
    if (!Object.hasOwn(servers, serverName)) {
      throw new Error('Cline Threadforge MCP server is absent; refusing to create it during update');
    }
    if (!isOwnedEntry(servers[serverName]) || !deepEqual(servers[serverName], expected)) {
      throw new Error('Cline MCP entry is not an unchanged Threadforge entry; refusing to replace it');
    }
    if (deepEqual(servers[serverName], entry)) return { result: 'unchanged', changed: false };
    servers[serverName] = entry;
    settings.mcpServers = servers;
    return { result: 'updated', changed: true };
  });
}

export async function uninstallClineUserMcpServer(
  expectedCurrentCommand: string,
  options: ClineMcpPathOptions = {},
): Promise<Change> {
  const expected = buildEntry(expectedCurrentCommand);
  return mutateSettings(options, (settings) => {
    const servers = readServers(settings);
    if (!Object.hasOwn(servers, serverName)) return { result: 'absent', changed: false };
    if (!isOwnedEntry(servers[serverName]) || !deepEqual(servers[serverName], expected)) {
      throw new Error('Cline MCP entry is not an unchanged Threadforge entry; refusing to remove it');
    }
    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete settings.mcpServers;
    else settings.mcpServers = servers;
    return { result: 'removed', changed: true };
  });
}

export async function getClineUserMcpServerStatus(options: ClineMcpPathOptions = {}): Promise<ClineMcpStatus> {
  const configPath = clineMcpSettingsPath(options);
  const settings = await readSettings(configPath);
  const servers = readServers(settings);
  const entry = servers[serverName];
  const owned = isOwnedEntry(entry);
  return {
    result: entry === undefined ? 'absent' : owned ? 'installed' : 'modified',
    server_name: serverName,
    config_path: configPath,
    ...(owned ? { expected_command: (entry as JsonObject).command as string } : {}),
  };
}

function buildEntry(command: string): JsonObject {
  if (typeof command !== 'string' || command.trim() === '' || command !== command.trim() || command.length > 2048 ||
      /[\r\n\0]/.test(command) || looksCredentialLikeMcpValue(command)) {
    throw new Error('Cline MCP executable must be a bounded command without control characters');
  }
  return {
    command,
    args: [...managedArgs],
    disabled: false,
    autoApprove: [],
  };
}

function isOwnedEntry(value: unknown): boolean {
  if (!isRecord(value) || Object.keys(value).some((key) => !['command', 'args', 'disabled', 'autoApprove'].includes(key))) return false;
  return typeof value.command === 'string' && value.command.trim() !== '' && value.command === value.command.trim() &&
    value.command.length <= 2048 && !/[\r\n\0]/.test(value.command) && !looksCredentialLikeMcpValue(value.command) &&
    Array.isArray(value.args) && deepEqual(value.args, managedArgs) && value.disabled === false &&
    Array.isArray(value.autoApprove) && value.autoApprove.length === 0;
}

async function mutateSettings(
  options: ClineMcpPathOptions,
  mutate: (settings: JsonObject) => { result: Change; changed: boolean },
): Promise<Change> {
  const configPath = clineMcpSettingsPath(options);
  const parent = path.dirname(configPath);
  await ensureSafeDirectory(parent);
  const lockPath = `${configPath}.threadforge.lock`;
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Cline MCP settings are locked or have a stale Threadforge lock; inspect before retrying');
    throw new Error('unable to lock Cline MCP settings');
  }
  try {
    const settings = await readSettings(configPath);
    const result = mutate(settings);
    if (!result.changed) return result.result;
    await atomicWriteJson(configPath, settings);
    return result.result;
  } finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function ensureSafeDirectory(target: string): Promise<void> {
  const absolute = validateLocalAbsolutePath(target, 'Cline MCP settings directory');
  const root = path.parse(absolute).root;
  const rootInfo = await lstat(root).catch(() => null);
  if (!rootInfo?.isDirectory() || isLinked(rootInfo)) throw new Error('Cline MCP settings path contains an unsafe root');
  let current = root;
  const relative = absolute.slice(root.length);
  for (const segment of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, segment);
    try {
      await mkdir(current);
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create Cline MCP settings directory');
    }
    const info = await lstat(current).catch(() => null);
    if (!info?.isDirectory() || isLinked(info)) throw new Error('Cline MCP settings path contains a non-directory or linked component');
  }
}

async function readSettings(filePath: string): Promise<JsonObject> {
  let info;
  try {
    info = await lstat(filePath);
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    throw new Error('unable to inspect Cline MCP settings');
  }
  if (!info.isFile() || isLinked(info)) throw new Error('Cline MCP settings must be a regular non-linked file');
  if (info.size > maxSettingsBytes) throw new Error('Cline MCP settings exceed the local size limit');

  const identity = fileIdentity(info);
  const flags = constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0);
  let handle;
  let raw: Buffer;
  try {
    handle = await open(filePath, flags);
    const opened = await handle.stat();
    if (!opened.isFile() || fileIdentity(opened) !== identity) throw new Error('changed');
    const buffer = Buffer.alloc(maxSettingsBytes + 1);
    const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
    const after = await handle.stat();
    const pathAfter = await lstat(filePath);
    if (bytesRead !== info.size || bytesRead > maxSettingsBytes ||
        fileIdentity(after) !== identity || fileIdentity(pathAfter) !== identity || isLinked(pathAfter)) {
      throw new Error('changed');
    }
    raw = buffer.subarray(0, bytesRead);
  } catch {
    throw new Error('Cline MCP settings could not be read consistently');
  } finally {
    await handle?.close().catch(() => {});
  }

  try {
    const value: unknown = JSON.parse(raw.toString('utf8').replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error('root is not an object');
    return value;
  } catch {
    throw new Error('Cline MCP settings are not valid strict JSON; no changes were made');
  }
}

function readServers(settings: JsonObject): JsonObject {
  if (settings.mcpServers === undefined) return {};
  if (!isRecord(settings.mcpServers)) throw new Error('Cline MCP settings mcpServers must be an object; no changes were made');
  return settings.mcpServers;
}

async function atomicWriteJson(filePath: string, value: JsonObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      const existing = await lstat(filePath);
      if (!existing.isFile() || isLinked(existing)) throw new Error('unsafe Cline MCP settings target');
      mode = existing.mode & 0o777;
    } catch (error) {
      if (!isCode(error, 'ENOENT')) throw error;
    }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Cline MCP settings');
  }
}

function validateLocalAbsolutePath(value: string, label: string): string {
  if (typeof value !== 'string' || value.trim() === '' || value.length > 4096 || !path.isAbsolute(value) ||
      /^(?:\\\\|\/\/)/.test(value) || value.split(/[\\/]/).some((part) => part === '..' || part === '.')) {
    throw new Error(`${label} must be an absolute local path without traversal components`);
  }
  return path.resolve(value);
}

function isLinked(info: { isSymbolicLink(): boolean; st_file_attributes?: number }): boolean {
  return info.isSymbolicLink() || Boolean((info.st_file_attributes ?? 0) & 0x400);
}

function fileIdentity(info: { dev: number; ino: number; size: number; mtimeMs: number }): string {
  return `${info.dev}:${info.ino}:${info.size}:${info.mtimeMs}`;
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
