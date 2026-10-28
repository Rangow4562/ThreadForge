import { randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import path from 'node:path';
import {
  installQwenProjectMcpServer,
  removeManagedQwenProjectMcpServer,
  removeQwenProjectMcpServer,
  updateManagedQwenProjectMcpServer,
  updateQwenProjectMcpServer,
} from './qwen-hooks-config.ts';
import { looksCredentialLikeMcpValue, managedMcpArgument } from './mcp-contract.ts';

export { managedMcpArgument } from './mcp-contract.ts';

export const projectMcpHosts = {
  'claude-code': '.mcp.json',
  'command-code': '.mcp.json',
  'github-copilot': '.mcp.json',
  qoder: '.mcp.json',
  'kimi-code': path.join('.kimi-code', 'mcp.json'),
  cursor: path.join('.cursor', 'mcp.json'),
  antigravity: path.join('.agents', 'mcp_config.json'),
  'gemini-cli': path.join('.gemini', 'settings.json'),
  'qwen-code': path.join('.qwen', 'settings.json'),
} as const;

export type ProjectMcpHost = keyof typeof projectMcpHosts;

export interface McpServerConfig {
  command: string;
  args: string[];
  cwd?: string;
  trust?: false;
}

type ConfigObject = Record<string, unknown>;
type ChangeResult = 'installed' | 'updated' | 'removed' | 'unchanged' | 'absent';
const maxConfigBytes = 8 << 20;

export function projectMcpConfigPath(projectRoot: string, host: ProjectMcpHost): string {
  if (!Object.hasOwn(projectMcpHosts, host)) throw new Error('unsupported project MCP host');
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  return path.join(path.resolve(projectRoot), projectMcpHosts[host]);
}

export async function installProjectMcpServer(
  projectRoot: string,
  host: ProjectMcpHost,
  serverName: string,
  server: McpServerConfig,
): Promise<ChangeResult> {
  validateServerName(serverName);
  validateServerForHost(host, server);
  if (!isManagedServer(server)) throw new Error('MCP server entry must include the Threadforge ownership argument');
  if (host === 'qwen-code') return installQwenProjectMcpServer(projectRoot, serverName, server);
  return mutateProjectConfig(projectRoot, host, (config) => {
    const servers = getServers(config);
    if (Object.hasOwn(servers, serverName)) {
      if (isManagedServer(servers[serverName]) && deepEqual(servers[serverName], server)) return { result: 'unchanged', changed: false };
      throw new Error(`MCP server '${serverName}' already exists with different settings; refusing to overwrite it`);
    }
    servers[serverName] = server;
    config.mcpServers = servers;
    return { result: 'installed', changed: true };
  });
}

export async function updateProjectMcpServer(
  projectRoot: string,
  host: ProjectMcpHost,
  serverName: string,
  expectedCurrent: unknown,
  next: McpServerConfig,
): Promise<ChangeResult> {
  validateServerName(serverName);
  validateServerForHost(host, next);
  if (!isManagedServer(next)) throw new Error('updated MCP server entry must include the Threadforge ownership argument');
  if (host === 'qwen-code') return updateQwenProjectMcpServer(projectRoot, serverName, expectedCurrent, next);
  return mutateProjectConfig(projectRoot, host, (config) => {
    const servers = getServers(config);
    if (!Object.hasOwn(servers, serverName)) {
      throw new Error(`MCP server '${serverName}' is absent; refusing to create it during update`);
    }
    if (!isManagedServer(servers[serverName]) || !isManagedServer(expectedCurrent) || !deepEqual(servers[serverName], expectedCurrent)) {
      throw new Error(`MCP server '${serverName}' changed since it was read; refusing to overwrite it`);
    }
    requireGeminiTrustOff(host, servers[serverName]);
    if (deepEqual(servers[serverName], next)) return { result: 'unchanged', changed: false };
    servers[serverName] = next;
    config.mcpServers = servers;
    return { result: 'updated', changed: true };
  });
}

export async function updateManagedProjectMcpServer(
  projectRoot: string,
  host: ProjectMcpHost,
  serverName: string,
  next: McpServerConfig,
): Promise<ChangeResult> {
  validateServerName(serverName);
  validateServerForHost(host, next);
  if (!isManagedServer(next)) throw new Error('updated MCP server entry must include the Threadforge ownership argument');
  if (host === 'qwen-code') return updateManagedQwenProjectMcpServer(projectRoot, serverName, next);
  return mutateProjectConfig(projectRoot, host, (config) => {
    const servers = getServers(config);
    if (!Object.hasOwn(servers, serverName)) throw new Error(`MCP server '${serverName}' is absent; refusing to update it`);
    if (!isManagedServer(servers[serverName])) throw new Error(`MCP server '${serverName}' is not marked as Threadforge-managed`);
    requireGeminiTrustOff(host, servers[serverName]);
    if (deepEqual(servers[serverName], next)) return { result: 'unchanged', changed: false };
    servers[serverName] = next;
    config.mcpServers = servers;
    return { result: 'updated', changed: true };
  });
}

export async function removeProjectMcpServer(
  projectRoot: string,
  host: ProjectMcpHost,
  serverName: string,
  expectedCurrent: unknown,
): Promise<ChangeResult> {
  validateServerName(serverName);
  if (host === 'qwen-code') return removeQwenProjectMcpServer(projectRoot, serverName, expectedCurrent);
  return mutateProjectConfig(projectRoot, host, (config) => {
    const servers = getServers(config);
    if (!Object.hasOwn(servers, serverName)) return { result: 'absent', changed: false };
    if (!isManagedServer(servers[serverName]) || !isManagedServer(expectedCurrent) || !deepEqual(servers[serverName], expectedCurrent)) {
      throw new Error(`MCP server '${serverName}' changed since it was read; refusing to remove it`);
    }
    requireGeminiTrustOff(host, servers[serverName]);
    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete config.mcpServers;
    else config.mcpServers = servers;
    return { result: 'removed', changed: true };
  });
}

export async function removeManagedProjectMcpServer(
  projectRoot: string,
  host: ProjectMcpHost,
  serverName: string,
): Promise<ChangeResult> {
  validateServerName(serverName);
  if (host === 'qwen-code') return removeManagedQwenProjectMcpServer(projectRoot, serverName);
  return mutateProjectConfig(projectRoot, host, (config) => {
    const servers = getServers(config);
    if (!Object.hasOwn(servers, serverName)) return { result: 'absent', changed: false };
    if (!isManagedServer(servers[serverName])) throw new Error(`MCP server '${serverName}' is not marked as Threadforge-managed`);
    requireGeminiTrustOff(host, servers[serverName]);
    delete servers[serverName];
    if (Object.keys(servers).length === 0) delete config.mcpServers;
    else config.mcpServers = servers;
    return { result: 'removed', changed: true };
  });
}

async function mutateProjectConfig(
  projectRoot: string,
  host: ProjectMcpHost,
  mutate: (config: ConfigObject) => { result: ChangeResult; changed: boolean },
): Promise<ChangeResult> {
  const requestedPath = projectMcpConfigPath(projectRoot, host);
  const root = await realpath(projectRoot).catch(() => {
    throw new Error('project root must exist and be accessible');
  });
  const configPath = path.join(root, path.relative(path.resolve(projectRoot), requestedPath));
  const parent = path.dirname(configPath);
  await ensureSafeDirectory(root, parent);

  const lockPath = path.join(parent, '.threadforge-mcp-config.lock');
  let lock;
  try {
    lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) {
      throw new Error('project MCP config is locked by another installer or a stale lock; inspect it before retrying');
    }
    throw new Error('unable to lock project MCP configuration');
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

async function ensureSafeDirectory(root: string, target: string): Promise<void> {
  const relative = path.relative(root, target);
  if (relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    throw new Error('project MCP config path escapes the project root');
  }
  let current = root;
  const rootStat = await lstat(root).catch(() => null);
  if (!rootStat?.isDirectory() || rootStat.isSymbolicLink()) {
    throw new Error('project root must be a real directory, not a symbolic link');
  }
  for (const segment of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, segment);
    try {
      await mkdir(current);
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create project MCP config directory');
    }
    const stat = await lstat(current).catch(() => null);
    if (!stat?.isDirectory() || stat.isSymbolicLink()) {
      throw new Error('project MCP config path contains a non-directory or symbolic link');
    }
  }
}

async function readConfig(filePath: string): Promise<ConfigObject> {
  let bytes: Buffer;
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) {
      throw new Error('project MCP config must be a regular file, not a symbolic link');
    }
    if (stat.size > maxConfigBytes) throw new Error('project MCP config exceeds the local size limit');
    bytes = await readFile(filePath);
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('project MCP config')) throw error;
    throw new Error('unable to read project MCP config');
  }
  try {
    const text = bytes.toString('utf8').replace(/^\uFEFF/, '');
    const value: unknown = JSON.parse(text);
    if (!isRecord(value)) throw new Error('root must be an object');
    return value;
  } catch {
    throw new Error('project MCP config is not valid strict JSON; no changes were made');
  }
}

function getServers(config: ConfigObject): ConfigObject {
  if (config.mcpServers === undefined) return {};
  if (!isRecord(config.mcpServers)) throw new Error('project MCP config mcpServers must be an object; no changes were made');
  return config.mcpServers;
}

async function atomicWriteJson(filePath: string, value: ConfigObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      mode = (await lstat(filePath)).mode & 0o777;
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
    throw new Error('unable to atomically update project MCP config');
  }
}

function validateServerName(value: string): void {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value)) throw new Error('MCP server name is invalid');
}

function validateServer(value: McpServerConfig): void {
  if (!isRecord(value) || typeof value.command !== 'string' || value.command.trim() === '' || value.command.length > 2048) {
    throw new Error('MCP server command is required and must be bounded');
  }
  if (!Array.isArray(value.args) || value.args.length > 128 || value.args.some((arg) => typeof arg !== 'string' || arg.length > 4096)) {
    throw new Error('MCP server args must be a bounded string array');
  }
  if (value.cwd !== undefined && (typeof value.cwd !== 'string' || value.cwd.trim() === '' || value.cwd.length > 4096)) {
    throw new Error('MCP server cwd must be a bounded non-empty path');
  }
  if (value.trust !== undefined && value.trust !== false) {
    throw new Error('MCP server trust must remain false; Threadforge never disables host confirmations');
  }
  const extraKeys = Object.keys(value).filter((key) => key !== 'command' && key !== 'args' && key !== 'cwd' && key !== 'trust');
  if (extraKeys.length > 0) throw new Error('MCP server config only accepts command, args, and cwd; environment secrets are not accepted');
}

function validateServerForHost(host: ProjectMcpHost, value: McpServerConfig): void {
  validateServer(value);
  if (host === 'gemini-cli' && value.trust !== false) {
    throw new Error('Gemini MCP config must explicitly set trust to false');
  }
  if ((host === 'qwen-code' || host === 'qoder' || host === 'kimi-code') &&
      Object.keys(value).some((key) => key !== 'command' && key !== 'args')) {
    throw new Error(`${host} project MCP config only accepts command and args; Threadforge does not set trust, cwd, or environment`);
  }
  if ((host === 'qoder' || host === 'kimi-code') && [value.command, ...value.args].some(looksCredentialLikeMcpValue)) {
    throw new Error(`${host} project MCP values must not contain credential-like text`);
  }
}

function requireGeminiTrustOff(host: ProjectMcpHost, value: unknown): void {
  if (host === 'gemini-cli' && (!isRecord(value) || value.trust !== false)) {
    throw new Error('Gemini MCP trust changed; refusing to replace or remove the user-edited entry');
  }
}

function isManagedServer(value: unknown): value is McpServerConfig {
  return isRecord(value) && Array.isArray(value.args) && value.args.includes(managedMcpArgument);
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  }
  return value;
}

function isRecord(value: unknown): value is ConfigObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
