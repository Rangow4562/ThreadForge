import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, rmdir, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';

export const opencodeManagedMarker = 'threadforge-managed-opencode-plugin:v1';
export const opencodeV2ManagedMarker = 'threadforge-managed-opencode-v2-plugin:v1';
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

export interface OpenCodePlanGatePluginSettings {
  taskId: string;
  projectId: string;
}

export type OpenCodePluginChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Installs the V1 file and V2 directory adapters without replacing unrelated OpenCode plugins. */
export async function installOpenCodeProjectPlugin(
  projectRoot: string,
  moduleSpecifier: string,
  memory?: OpenCodeMemoryPluginSettings,
  planGate?: OpenCodePlanGatePluginSettings,
): Promise<OpenCodePluginChange> {
  validateModuleSpecifier(moduleSpecifier);
  if (memory) validateMemorySettings(memory);
  if (planGate) validatePlanGateSettings(planGate);
  const filePath = await pluginPath(projectRoot, true);
  if (!filePath) throw new Error('unable to resolve OpenCode project plugin path');
  const v2FilePath = await openCodeV2PluginPath(projectRoot, true);
  if (!v2FilePath) throw new Error('unable to resolve OpenCode V2 project plugin path');
  return withLock(filePath, async () => {
    const existing = await readPluginFile(filePath);
    const existingV2 = await readPluginFile(v2FilePath);
    if (existing !== undefined && !existing.includes(opencodeManagedMarker)) {
      throw new Error('refusing to overwrite unmanaged OpenCode plugin file');
    }
    if (existingV2 !== undefined && !existingV2.includes(opencodeV2ManagedMarker)) {
      throw new Error('refusing to overwrite unmanaged OpenCode V2 plugin file');
    }
    const next = renderOpenCodePlugin(moduleSpecifier, memory, planGate);
    const nextV2 = renderOpenCodeV2Plugin(moduleSpecifier, memory, planGate);
    if (existing === next && existingV2 === nextV2) return 'unchanged';
    if (existing !== next) await atomicWrite(filePath, next);
    try {
      if (existingV2 !== nextV2) await atomicWrite(v2FilePath, nextV2);
    } catch (error) {
      await restorePluginFileIfUnchanged(filePath, next, existing).catch(() => {});
      throw error;
    }
    return existing === undefined || existingV2 === undefined ? 'installed' : 'updated';
  });
}

/** Removes only the two isolated plugin entrypoints that carry Threadforge ownership markers. */
export async function removeOpenCodeProjectPlugin(projectRoot: string): Promise<OpenCodePluginChange> {
  const filePath = await pluginPath(projectRoot, false);
  if (filePath === undefined) return 'absent';
  const v2FilePath = await openCodeV2PluginPath(projectRoot, false);
  return withLock(filePath, async () => {
    const existing = await readPluginFile(filePath);
    const existingV2 = v2FilePath ? await readPluginFile(v2FilePath) : undefined;
    let removed = false;
    if (existing?.includes(opencodeManagedMarker)) {
      await unlink(filePath);
      removed = true;
    }
    if (v2FilePath && existingV2?.includes(opencodeV2ManagedMarker)) {
      await unlink(v2FilePath);
      removed = true;
      await rmdir(path.dirname(v2FilePath)).catch((error) => {
        if (!isCode(error, 'ENOENT') && !isCode(error, 'ENOTEMPTY') && !isCode(error, 'EEXIST')) throw error;
      });
    }
    return removed ? 'removed' : 'absent';
  });
}

export function renderOpenCodePlugin(
  moduleSpecifier: string,
  memory?: OpenCodeMemoryPluginSettings,
  planGate?: OpenCodePlanGatePluginSettings,
): string {
  validateModuleSpecifier(moduleSpecifier);
  if (memory) validateMemorySettings(memory);
  if (planGate) validatePlanGateSettings(planGate);
  const quoted = JSON.stringify(moduleSpecifier);
  if (memory || planGate) {
    const options = JSON.stringify(buildOpenCodePluginOptions(memory, planGate));
    return [
      `// ${opencodeManagedMarker}`,
      'import { createThreadforgeOpenCodeV1Plugin } from ' + quoted + ';',
      'const threadforgePlugin = createThreadforgeOpenCodeV1Plugin(' + options + ');',
      'export const ThreadforgeOpenCode = threadforgePlugin;',
      'export default threadforgePlugin;',
      '',
    ].join('\n');
  }
  return [
    `// ${opencodeManagedMarker}`,
    'import { createThreadforgeOpenCodeV1Plugin } from ' + quoted + ';',
    'const threadforgePlugin = createThreadforgeOpenCodeV1Plugin();',
    'export const ThreadforgeOpenCode = threadforgePlugin;',
    'export default threadforgePlugin;',
    '',
  ].join('\n');
}

export function renderOpenCodeV2Plugin(
  moduleSpecifier: string,
  memory?: OpenCodeMemoryPluginSettings,
  planGate?: OpenCodePlanGatePluginSettings,
): string {
  validateModuleSpecifier(moduleSpecifier);
  if (memory) validateMemorySettings(memory);
  if (planGate) validatePlanGateSettings(planGate);
  const quoted = JSON.stringify(moduleSpecifier);
  const options = memory || planGate ? JSON.stringify(buildOpenCodePluginOptions(memory, planGate)) : undefined;
  return [
    `// ${opencodeV2ManagedMarker}`,
    'import { createThreadforgeOpenCodeV2Plugin } from ' + quoted + ';',
    'const threadforgePlugin = createThreadforgeOpenCodeV2Plugin(' + (options ?? '') + ');',
    'export const ThreadforgeOpenCodeV2 = threadforgePlugin;',
    'export default threadforgePlugin;',
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

function validatePlanGateSettings(settings: OpenCodePlanGatePluginSettings): void {
  for (const [name, value] of [['task ID', settings.taskId], ['project ID', settings.projectId]] as const) {
    if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)) {
      throw new Error(`OpenCode task-plan gate ${name} is invalid`);
    }
  }
}

function buildOpenCodePluginOptions(
  memory?: OpenCodeMemoryPluginSettings,
  planGate?: OpenCodePlanGatePluginSettings,
): Record<string, unknown> {
  return {
    ...(memory ? { memory: {
      projectId: memory.projectId,
      maxItems: memory.maxItems ?? 8,
      maxTokens: memory.maxTokens ?? 1600,
      ...(memory.taskId ? { taskId: memory.taskId } : {}),
      ...(memory.sessionId ? { sessionId: memory.sessionId } : {}),
      ...(memory.discoverActiveTask ? { discoverActiveTask: true } : {}),
      ...((memory.taskId || memory.discoverActiveTask) ? { maxTaskEntries: memory.maxTaskEntries ?? 6 } : {}),
    } } : {}),
    ...(planGate ? { planGate: { taskId: planGate.taskId, projectId: planGate.projectId } } : {}),
  };
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

async function openCodeV2PluginPath(projectRoot: string, createDirectory: boolean): Promise<string | undefined> {
  const legacyPath = await pluginPath(projectRoot, createDirectory);
  if (!legacyPath) return undefined;
  const root = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const pluginDirectory = path.join(path.dirname(legacyPath), 'threadforge-v2');
  if (createDirectory) {
    await ensureDirectory(root, pluginDirectory);
  } else {
    const stat = await lstat(pluginDirectory).catch(() => null);
    if (!stat) return undefined;
    if (!stat.isDirectory() || stat.isSymbolicLink()) {
      throw new Error('OpenCode V2 plugin path contains a symbolic link or non-directory');
    }
  }
  return path.join(pluginDirectory, 'index.ts');
}

async function readPluginFile(filePath: string): Promise<string | undefined> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('OpenCode Threadforge plugin target must be a regular file');
    if (stat.size > maxPluginBytes) throw new Error('OpenCode plugin target exceeds the local size limit');
    return await readFile(filePath, 'utf8');
  } catch (error) {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  }
}

async function restorePluginFileIfUnchanged(filePath: string, expected: string, previous: string | undefined): Promise<void> {
  const current = await readPluginFile(filePath);
  if (current !== expected) return;
  if (previous === undefined) await unlink(filePath);
  else await atomicWrite(filePath, previous);
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
