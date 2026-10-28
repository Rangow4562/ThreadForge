import { randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { looksCredentialLikeMcpValue, managedMcpArgument } from './mcp-contract.ts';

type JsonObject = Record<string, unknown>;
type Change = 'installed' | 'updated' | 'removed' | 'unchanged' | 'absent';
const maxConfigBytes = 8 << 20;
const serverName = 'threadforge';
const managedArgs = ['mcp', 'serve', managedMcpArgument] as const;

export interface OpenHandsMcpPathOptions {
  configPath?: string;
  home?: string;
}

export interface OpenHandsMcpStatus {
  result: 'installed' | 'modified' | 'absent';
  server_name: typeof serverName;
  config_path: string;
  expected_command?: string;
}

export function openHandsMcpConfigPath(options: OpenHandsMcpPathOptions = {}): string {
  if (options.configPath !== undefined) return validateLocalAbsolutePath(options.configPath, 'OpenHands MCP config path');
  const home = validateLocalAbsolutePath(options.home ?? os.homedir(), 'OpenHands home');
  return path.join(home, '.openhands', 'mcp.json');
}

export async function installOpenHandsUserMcpServer(
  command: string,
  options: OpenHandsMcpPathOptions = {},
): Promise<Change> {
  const entry = buildEntry(command);
  return mutateConfig(options, (config) => {
    const servers = readServers(config);
    if (Object.hasOwn(servers, serverName)) {
      if (isOwnedEntry(servers[serverName]) && deepEqual(servers[serverName], entry)) {
        return { result: 'unchanged', changed: false };
      }
      throw new Error('OpenHands MCP server name is already in use; no config was changed');
    }
    servers[serverName] = entry;
    config.mcpServers = servers;
    return { result: 'installed', changed: true };
  });
}

export async function updateOpenHandsUserMcpServer(
  expectedCurrentCommand: string,
  command: string,
  options: OpenHandsMcpPathOptions = {},
): Promise<Change> {
  const expected = buildEntry(expectedCurrentCommand);
  const entry = buildEntry(command);
  return mutateConfig(options, (config) => {
    const servers = readServers(config);
    if (!Object.hasOwn(servers, serverName)) {
      throw new Error('OpenHands Threadforge MCP server is absent; refusing to create it during update');
    }
    if (!isOwnedEntry(servers[serverName]) || !deepEqual(servers[serverName], expected)) {
      throw new Error('OpenHands MCP entry changed or is unowned; refusing to replace it');
    }
    if (deepEqual(servers[serverName], entry)) return { result: 'unchanged', changed: false };
    servers[serverName] = entry;
    config.mcpServers = servers;
    return { result: 'updated', changed: true };
  });
}

export async function uninstallOpenHandsUserMcpServer(
  expectedCurrentCommand: string,
  options: OpenHandsMcpPathOptions = {},
): Promise<Change> {
  const expected = buildEntry(expectedCurrentCommand);
  return mutateConfig(options, (config) => {
    const servers = readServers(config);
    if (!Object.hasOwn(servers, serverName)) return { result: 'absent', changed: false };
    if (!isOwnedEntry(servers[serverName]) || !deepEqual(servers[serverName], expected)) {
      throw new Error('OpenHands MCP entry changed or is unowned; refusing to remove it');
    }
    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete config.mcpServers;
    else config.mcpServers = servers;
    return { result: 'removed', changed: true };
  });
}

export async function getOpenHandsUserMcpServerStatus(
  options: OpenHandsMcpPathOptions = {},
): Promise<OpenHandsMcpStatus> {
  const configPath = openHandsMcpConfigPath(options);
  const config = await readConfig(configPath);
  const entry = readServers(config)[serverName];
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
    throw new Error('OpenHands MCP executable must be a bounded command without credentials');
  }
  return { command, args: [...managedArgs] };
}

function isOwnedEntry(value: unknown): boolean {
  if (!isRecord(value) || Object.keys(value).some((key) => key !== 'command' && key !== 'args')) return false;
  return typeof value.command === 'string' && value.command.trim() !== '' && value.command === value.command.trim() &&
    value.command.length <= 2048 && !/[\r\n\0]/.test(value.command) && !looksCredentialLikeMcpValue(value.command) &&
    Array.isArray(value.args) && deepEqual(value.args, managedArgs);
}

async function mutateConfig(
  options: OpenHandsMcpPathOptions,
  mutate: (config: JsonObject) => { result: Change; changed: boolean },
): Promise<Change> {
  const configPath = openHandsMcpConfigPath(options);
  const parent = path.dirname(configPath);
  await ensureSafeDirectory(parent);
  const lockPath = `${configPath}.threadforge.lock`;
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenHands MCP config is locked or has a stale Threadforge lock; inspect before retrying');
    throw new Error('unable to lock OpenHands MCP config');
  }
  try {
    const config = await readConfig(configPath);
    const outcome = mutate(config);
    if (!outcome.changed) return outcome.result;
    await atomicWriteJson(configPath, config);
    return outcome.result;
  } finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<JsonObject> {
  let info;
  try {
    info = await lstat(filePath);
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    throw new Error('unable to inspect OpenHands MCP config');
  }
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('OpenHands MCP config must be a regular non-linked file');
  if (info.size > maxConfigBytes) throw new Error('OpenHands MCP config exceeds the local size limit');

  let raw: Buffer;
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!opened.isFile() || fileIdentity(opened) !== fileIdentity(info)) throw new Error('changed');
    const buffer = Buffer.alloc(maxConfigBytes + 1);
    const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
    const after = await handle.stat();
    const pathAfter = await lstat(filePath);
    if (bytesRead !== info.size || bytesRead > maxConfigBytes || fileIdentity(after) !== fileIdentity(info) ||
        fileIdentity(pathAfter) !== fileIdentity(info) || pathAfter.isSymbolicLink()) throw new Error('changed');
    raw = buffer.subarray(0, bytesRead);
  } catch {
    throw new Error('OpenHands MCP config could not be read consistently');
  } finally {
    await handle?.close().catch(() => {});
  }

  try {
    const parsed: unknown = JSON.parse(raw.toString('utf8').replace(/^\uFEFF/, ''));
    if (!isRecord(parsed)) throw new Error('root must be an object');
    return parsed;
  } catch {
    throw new Error('OpenHands MCP config is not valid strict JSON; no changes were made');
  }
}

function readServers(config: JsonObject): JsonObject {
  if (config.mcpServers === undefined) return {};
  if (!isRecord(config.mcpServers)) throw new Error('OpenHands MCP config mcpServers must be an object; no changes were made');
  return config.mcpServers;
}

async function ensureSafeDirectory(target: string): Promise<void> {
  const absolute = validateLocalAbsolutePath(target, 'OpenHands MCP config directory');
  const root = path.parse(absolute).root;
  const rootStat = await lstat(root).catch(() => null);
  if (!rootStat?.isDirectory() || rootStat.isSymbolicLink()) throw new Error('OpenHands MCP config path has an unsafe filesystem root');
  let current = root;
  const relative = absolute.slice(root.length);
  for (const segment of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, segment);
    try {
      await mkdir(current);
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create OpenHands MCP config directory');
    }
    const info = await lstat(current).catch(() => null);
    if (!info?.isDirectory() || info.isSymbolicLink()) throw new Error('OpenHands MCP config path contains a non-directory or linked component');
  }
}

async function atomicWriteJson(filePath: string, value: JsonObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      const existing = await lstat(filePath);
      if (!existing.isFile() || existing.isSymbolicLink()) throw new Error('unsafe target');
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
    throw new Error('unable to atomically update OpenHands MCP config');
  }
}

function validateLocalAbsolutePath(value: string, label: string): string {
  if (typeof value !== 'string' || value.trim() === '' || value.length > 4096 || !path.isAbsolute(value) ||
      /^(?:\\\\|\/\/)/.test(value) || value.split(/[\\/]/).some((part) => part === '..' || part === '.')) {
    throw new Error(`${label} must be an absolute local path without traversal`);
  }
  return path.resolve(value);
}

function fileIdentity(stat: { dev: number | bigint; ino: number | bigint; size: number; mtimeMs: number; ctimeMs: number }): string {
  return `${stat.dev}:${stat.ino}:${stat.size}:${stat.mtimeMs}:${stat.ctimeMs}`;
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
