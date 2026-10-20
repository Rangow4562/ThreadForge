import { constants } from 'node:fs';
import { lstat, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { managedMcpArgument } from './mcp-config.ts';

type JsonObject = Record<string, unknown>;
type ChangeResult = 'installed' | 'updated' | 'removed' | 'unchanged' | 'absent';
const maxConfigBytes = 8 << 20;

export function openCodeMcpConfigPath(projectRoot: string): string {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  return path.join(path.resolve(projectRoot), 'opencode.json');
}

export async function installOpenCodeProjectMcpServer(
  projectRoot: string,
  serverName: string,
  command: string,
  args: string[],
): Promise<ChangeResult> {
  validateName(serverName);
  const entry = await buildEntry(projectRoot, command, args);
  return mutateOpenCodeConfig(projectRoot, (config) => {
    const servers = readMcpServers(config);
    if (Object.hasOwn(servers, serverName)) {
      if (isOwnedEntry(servers[serverName]) && deepEqual(servers[serverName], entry)) return { result: 'unchanged', changed: false };
      throw new Error(`OpenCode MCP server '${serverName}' already exists with different settings; refusing to overwrite it`);
    }
    servers[serverName] = entry;
    config.mcp = servers;
    return { result: 'installed', changed: true };
  });
}

export async function updateManagedOpenCodeProjectMcpServer(
  projectRoot: string,
  serverName: string,
  command: string,
  args: string[],
): Promise<ChangeResult> {
  validateName(serverName);
  const entry = await buildEntry(projectRoot, command, args);
  return mutateOpenCodeConfig(projectRoot, (config) => {
    const servers = readMcpServers(config);
    const current = servers[serverName];
    if (current === undefined) throw new Error(`OpenCode MCP server '${serverName}' is absent; refusing to update it`);
    if (!isOwnedEntry(current)) throw new Error(`OpenCode MCP server '${serverName}' is not an unchanged Threadforge entry; refusing to replace it`);
    if (deepEqual(current, entry)) return { result: 'unchanged', changed: false };
    servers[serverName] = entry;
    config.mcp = servers;
    return { result: 'updated', changed: true };
  });
}

export async function removeManagedOpenCodeProjectMcpServer(projectRoot: string, serverName: string): Promise<ChangeResult> {
  validateName(serverName);
  return mutateOpenCodeConfig(projectRoot, (config) => {
    const servers = readMcpServers(config);
    if (!Object.hasOwn(servers, serverName)) return { result: 'absent', changed: false };
    if (!isOwnedEntry(servers[serverName])) throw new Error(`OpenCode MCP server '${serverName}' is not an unchanged Threadforge entry; refusing to remove it`);
    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete config.mcp;
    else config.mcp = servers;
    return { result: 'removed', changed: true };
  });
}

async function buildEntry(projectRoot: string, command: string, args: string[]): Promise<JsonObject> {
  if (typeof command !== 'string' || command.trim() === '' || command.length > 2048 || /[\r\n\0]/.test(command)) {
    throw new Error('OpenCode MCP executable is invalid or too long');
  }
  if (!Array.isArray(args) || args.length > 128 || args.some((arg) => typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg))) {
    throw new Error('OpenCode MCP arguments must be bounded strings without control characters');
  }
  if (args[0] !== 'mcp' || args[1] !== 'serve' || !args.includes(managedMcpArgument)) {
    throw new Error(`OpenCode MCP command must begin with mcp serve and include ${managedMcpArgument}`);
  }
  const rootIndexes = args.flatMap((value, index) => value === '--project-root' ? [index] : []);
  if (rootIndexes.length !== 1 || args.some((value) => value.startsWith('--project-root='))) {
    throw new Error('OpenCode MCP command must contain exactly one --project-root <same-root> argument');
  }
  const configuredRoot = args[rootIndexes[0] + 1];
  if (!configuredRoot || configuredRoot.startsWith('--')) throw new Error('OpenCode MCP command is missing --project-root <same-root>');
  const expectedRoot = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const actualRoot = await realpath(configuredRoot).catch(() => { throw new Error('MCP --project-root must exist and match the selected project root'); });
  const normalize = (value: string) => path.resolve(value).replace(/[\\/]+$/, '');
  const same = process.platform === 'win32'
    ? normalize(expectedRoot).toLowerCase() === normalize(actualRoot).toLowerCase()
    : normalize(expectedRoot) === normalize(actualRoot);
  if (!same) throw new Error('MCP --project-root must match the selected project root');
  return { type: 'local', command: [command, ...args], enabled: true };
}

async function mutateOpenCodeConfig(
  projectRoot: string,
  mutate: (config: JsonObject) => { result: ChangeResult; changed: boolean },
): Promise<ChangeResult> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory, not a symbolic link');
  const configPath = path.join(root, 'opencode.json');
  const jsoncPath = path.join(root, 'opencode.jsonc');
  if (await pathExists(jsoncPath)) throw new Error('OpenCode opencode.jsonc is present; no changes made because comments will not be rewritten; merge the Threadforge MCP entry manually');

  const lockPath = path.join(root, '.threadforge-opencode-mcp.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenCode MCP config is locked by another writer or a stale lock');
    throw new Error('unable to lock OpenCode MCP configuration');
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
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('OpenCode config must be a regular file, not a symbolic link');
    if (stat.size > maxConfigBytes) throw new Error('OpenCode config exceeds the local size limit');
    const value: unknown = JSON.parse((await readFile(filePath, 'utf8')).replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error('OpenCode config root must be an object');
    return value;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('OpenCode config')) throw error;
    throw new Error('OpenCode config is not valid strict JSON; no changes were made');
  }
}

function readMcpServers(config: JsonObject): JsonObject {
  if (config.mcp === undefined || config.mcp === null) return {};
  if (!isRecord(config.mcp)) throw new Error('OpenCode config mcp must be an object; no changes were made');
  return config.mcp;
}

function isOwnedEntry(value: unknown): boolean {
  if (!isRecord(value) || value.type !== 'local' || value.enabled !== true || !Array.isArray(value.command)) return false;
  if (Object.keys(value).some((key) => !['type', 'enabled', 'command'].includes(key))) return false;
  return value.command.every((part) => typeof part === 'string') && value.command.includes(managedMcpArgument);
}

async function atomicWriteJson(filePath: string, value: JsonObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      const existing = await lstat(filePath);
      if (!existing.isFile() || existing.isSymbolicLink()) throw new Error('OpenCode config target must be a regular file');
      mode = existing.mode & 0o777;
    } catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update OpenCode config');
  }
}

async function pathExists(filePath: string): Promise<boolean> {
  try { await lstat(filePath); return true; }
  catch (error) { if (isCode(error, 'ENOENT')) return false; throw new Error('unable to inspect OpenCode JSONC config'); }
}

function validateName(value: string): void {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value)) throw new Error('MCP server name is invalid');
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
