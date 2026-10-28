import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const opencodeManagedMarker = 'threadforge-managed-opencode-plugin:v1';
const maxPluginBytes = 256 << 10;

export interface OpenCodeMemoryPluginSettings {
  projectId: string;
  maxItems?: number;
  maxTokens?: number;
  taskId?: string;
  sessionId?: string;
  discoverActiveTask?: boolean;
  maxTaskEntries?: number;
}

export type OpenCodePluginChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Installs Threadforge as a separate project plugin file, leaving other plugins untouched. */
export async function installOpenCodeProjectPlugin(
  projectRoot: string,
  moduleSpecifier: string,
  memory?: OpenCodeMemoryPluginSettings,
): Promise<OpenCodePluginChange> {
  validateModuleSpecifier(moduleSpecifier);
  if (memory) validateMemorySettings(memory);
  const filePath = await pluginPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve OpenCode project plugin path');
  return withLock(filePath, async () => {
    let existing: string | undefined;
    try {
      const stat = await lstat(filePath);
      if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('OpenCode Threadforge plugin target must be a regular file');
      if (stat.size > maxPluginBytes) throw new Error('OpenCode plugin target exceeds the local size limit');
      existing = await readFile(filePath, 'utf8');
    } catch (error) {
      if (!isCode(error, 'ENOENT')) throw error;
    }
    if (existing !== undefined && !existing.includes(opencodeManagedMarker)) {
      throw new Error('refusing to overwrite unmanaged OpenCode plugin file');
    }
    const next = renderOpenCodePlugin(moduleSpecifier, memory);
    if (existing === next) return 'unchanged';
    await atomicWrite(filePath, next);
    return existing === undefined ? 'installed' : 'updated';
  });
}

/** Removes only the isolated plugin file that contains Threadforge's ownership marker. */
export async function removeOpenCodeProjectPlugin(projectRoot: string): Promise<OpenCodePluginChange> {
  const filePath = await pluginPath(projectRoot, false);
  if (filePath === undefined) return 'absent';
  return withLock(filePath, async () => {
    let text: string;
    try {
      const stat = await lstat(filePath);
      if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('OpenCode Threadforge plugin target must be a regular file');
      if (stat.size > maxPluginBytes) throw new Error('OpenCode plugin target exceeds the local size limit');
      text = await readFile(filePath, 'utf8');
    } catch (error) {
      if (isCode(error, 'ENOENT')) return 'absent';
      throw error;
    }
    if (!text.includes(opencodeManagedMarker)) return 'absent';
    await unlink(filePath);
    return 'removed';
  });
}

export function renderOpenCodePlugin(moduleSpecifier: string, memory?: OpenCodeMemoryPluginSettings): string {
  validateModuleSpecifier(moduleSpecifier);
  if (memory) validateMemorySettings(memory);
  const quoted = JSON.stringify(moduleSpecifier);
  if (memory) {
    const options = JSON.stringify({ memory: {
      projectId: memory.projectId,
      maxItems: memory.maxItems ?? 8,
      maxTokens: memory.maxTokens ?? 1600,
      ...(memory.taskId ? { taskId: memory.taskId } : {}),
      ...(memory.sessionId ? { sessionId: memory.sessionId } : {}),
      ...(memory.discoverActiveTask ? { discoverActiveTask: true } : {}),
      ...((memory.taskId || memory.discoverActiveTask) ? { maxTaskEntries: memory.maxTaskEntries ?? 6 } : {}),
    } });
    return [
      `// ${opencodeManagedMarker}`,
      'import { ThreadforgeOpenCodePlugin } from ' + quoted + ';',
      'export const ThreadforgeOpenCode = (context) => ThreadforgeOpenCodePlugin(context, ' + options + ');',
      'export default ThreadforgeOpenCode;',
      '',
    ].join('\n');
  }
  return [
    `// ${opencodeManagedMarker}`,
    'import { ThreadforgeOpenCodePlugin } from ' + quoted + ';',
    'export const ThreadforgeOpenCode = ThreadforgeOpenCodePlugin;',
    'export default ThreadforgeOpenCodePlugin;',
    '',
  ].join('\n');
}

function validateMemorySettings(memory: OpenCodeMemoryPluginSettings): void {
  if (typeof memory.projectId !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(memory.projectId)) {
    throw new Error('OpenCode memory project ID must match [a-z][a-z0-9-]{0,63}');
  }
  const maxItems = memory.maxItems ?? 8;
  const maxTokens = memory.maxTokens ?? 1600;
  if (!Number.isInteger(maxItems) || maxItems < 1 || maxItems > 32 ||
      !Number.isInteger(maxTokens) || maxTokens < 1 || maxTokens > 16000) {
    throw new Error('OpenCode memory limits must be maxItems 1-32 and maxTokens 1-16000');
  }
  const hasTaskId = memory.taskId !== undefined;
  const hasSessionId = memory.sessionId !== undefined;
  if (hasTaskId && (typeof memory.taskId !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(memory.taskId))) {
    throw new Error('OpenCode task ID must match [a-z][a-z0-9-]{0,63}');
  }
  if (hasSessionId && (typeof memory.sessionId !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(memory.sessionId))) {
    throw new Error('OpenCode session ID must match [a-z][a-z0-9-]{0,63}');
  }
  if (hasSessionId && !hasTaskId) throw new Error('OpenCode session ID requires an explicit task ID');
  if (memory.discoverActiveTask !== undefined && typeof memory.discoverActiveTask !== 'boolean') {
    throw new Error('OpenCode active-task discovery must be a boolean');
  }
  if (memory.discoverActiveTask && (hasTaskId || hasSessionId)) {
    throw new Error('OpenCode task ID and active-task discovery are mutually exclusive');
  }
  const hasTaskSelection = hasTaskId || memory.discoverActiveTask === true;
  if (memory.maxTaskEntries !== undefined && !hasTaskSelection) {
    throw new Error('OpenCode task-entry limit requires task resume selection');
  }
  const maxTaskEntries = memory.maxTaskEntries ?? 6;
  if (!Number.isInteger(maxTaskEntries) || maxTaskEntries < 1 || maxTaskEntries > 20) {
    throw new Error('OpenCode task-entry limit must be between 1 and 20');
  }
}

async function pluginPath(projectRoot: string, createDirectory: boolean): Promise<string | undefined> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be absolute');
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('project root must be a real directory');
  const opencodeDir = path.join(root, '.opencode');
  const pluginsDir = path.join(opencodeDir, 'plugins');
  if (createDirectory) {
    await ensureDirectory(root, opencodeDir);
    await ensureDirectory(root, pluginsDir);
  } else {
    const opencodeStat = await lstat(opencodeDir).catch(() => null);
    if (!opencodeStat) return undefined;
    if (!opencodeStat.isDirectory() || opencodeStat.isSymbolicLink()) throw new Error('OpenCode project config path contains a symbolic link or non-directory');
    const pluginsStat = await lstat(pluginsDir).catch(() => null);
    if (!pluginsStat) return undefined;
    if (!pluginsStat.isDirectory() || pluginsStat.isSymbolicLink()) throw new Error('OpenCode plugin path contains a symbolic link or non-directory');
  }
  return path.join(pluginsDir, 'threadforge.ts');
}

async function ensureDirectory(root: string, target: string): Promise<void> {
  const relative = path.relative(root, target);
  if (relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    throw new Error('OpenCode plugin path escapes the project root');
  }
  let current = root;
  for (const part of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    try { await mkdir(current); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create OpenCode plugin directory'); }
    const stat = await lstat(current).catch(() => null);
    if (!stat?.isDirectory() || stat.isSymbolicLink()) throw new Error('OpenCode plugin path contains a symbolic link or non-directory');
  }
}

async function withLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(filePath), '.threadforge-opencode-plugin.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenCode plugin is locked by another installer or a stale lock');
    throw new Error('unable to lock OpenCode plugin directory');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function atomicWrite(filePath: string, text: string): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try { mode = (await lstat(filePath)).mode & 0o777; }
    catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(text, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update OpenCode plugin file');
  }
}

function validateModuleSpecifier(value: string): void {
  if (typeof value !== 'string' || value.trim() !== value || value.length < 1 || value.length > 4096 || /[\r\n\0]/.test(value)) {
    throw new Error('OpenCode adapter module specifier is invalid');
  }
  if (value === '@threadforge/adapter-contracts/opencode-plugin') return;
  let url: URL;
  try { url = new URL(value); }
  catch { throw new Error('module specifier must be the Threadforge package export or an absolute file URL'); }
  if (url.protocol !== 'file:' || url.username || url.password || url.search || url.hash) {
    throw new Error('module specifier must be a local file URL without credentials or query parameters');
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
