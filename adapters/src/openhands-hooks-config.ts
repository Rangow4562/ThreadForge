import { constants, type Stats } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

export const openHandsManagedMarker = '--threadforge-openhands-hook=v1';
export const openHandsHookEvents = ['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end'] as const;
export type OpenHandsHookEvent = typeof openHandsHookEvents[number];
type JsonObject = Record<string, unknown>;
type ChangeResult = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
type Outcome = { result: ChangeResult; changed: boolean; deleteFile?: boolean };
const maxConfigBytes = 8 << 20;

export type OpenHandsInstallOptions = { commandTemplate?: string };

/** Merge asynchronous, decision-free command hooks into a project-owned OpenHands hooks.json. */
export async function installOpenHandsProjectHooks(projectRoot: string, options: OpenHandsInstallOptions = {}): Promise<ChangeResult> {
  const commands = new Map<OpenHandsHookEvent, string>();
  for (const event of openHandsHookEvents) commands.set(event, commandFor(event, options.commandTemplate));
  return mutateOpenHandsConfig(projectRoot, true, (root) => {
    const located = locateHookMap(root);
    const cleaned = removeManagedReferences(located.map, located.keys);
    const nextMap: JsonObject = { ...cleaned.map };
    const nextKeys = new Map(located.keys);
    for (const event of openHandsHookEvents) {
      const key = nextKeys.get(event) ?? event;
      const current = readGroups(nextMap[key], key);
      nextMap[key] = [...current, { matcher: '*', hooks: [{ type: 'command', command: commands.get(event)!, async: true, timeout: 10 }] }];
      nextKeys.set(event, key);
    }
    const nextRoot = applyHookMap(root, nextMap, located.wrapped);
    const changed = !deepEqual(root, nextRoot);
    if (changed) replaceObject(root, nextRoot);
    return { result: !changed ? 'unchanged' : cleaned.found ? 'updated' : 'installed', changed };
  });
}

/** Remove only hook commands containing Threadforge's ownership marker. */
export async function removeOpenHandsProjectHooks(projectRoot: string): Promise<ChangeResult> {
  return mutateOpenHandsConfig(projectRoot, false, (root) => {
    const located = locateHookMap(root);
    const cleaned = removeManagedReferences(located.map, located.keys);
    if (!cleaned.found) return { result: 'absent', changed: false };
    const nextRoot = applyHookMap(root, cleaned.map, located.wrapped);
    const empty = Object.keys(nextRoot).length === 0;
    if (empty) return { result: 'removed', changed: true, deleteFile: true };
    replaceObject(root, nextRoot);
    return { result: 'removed', changed: true };
  });
}

function commandFor(event: OpenHandsHookEvent, template?: string): string {
  let command: string;
  if (template !== undefined) {
    validateCommandTemplate(template);
    command = template.replace('{event}', event);
  } else {
    const cliPath = fileURLToPath(new URL('./cli.ts', import.meta.url));
    command = `${quoteShellArgument(process.execPath)} --experimental-strip-types ${quoteShellArgument(cliPath)} openhands hook ${event} ${openHandsManagedMarker}`;
  }
  if (command.length > 8192 || /[\r\n\0]/.test(command) || !command.includes(openHandsManagedMarker) || looksCredentialLike(command)) {
    throw new Error('OpenHands hook command is invalid or too long');
  }
  return command;
}

function validateCommandTemplate(template: string): void {
  if (typeof template !== 'string' || template.trim() === '' || template.length > 8_000 || /[\r\n\0]/.test(template) ||
      template.split('{event}').length !== 2 || !template.includes(openHandsManagedMarker) || looksCredentialLike(template)) {
    throw new Error(`--command-template must contain exactly one {event} placeholder and ${openHandsManagedMarker}`);
  }
}

function quoteShellArgument(value: string): string {
  if (process.platform !== 'win32') return `'${value.replaceAll("'", "'\\''")}'`;
  let result = '"';
  let slashes = 0;
  for (const character of value) {
    if (character === '\\') { slashes++; continue; }
    if (character === '"') { result += '\\'.repeat(slashes * 2 + 1) + '"'; slashes = 0; continue; }
    result += '\\'.repeat(slashes) + character;
    slashes = 0;
  }
  return result + '\\'.repeat(slashes * 2) + '"';
}

function locateHookMap(root: JsonObject): { map: JsonObject; wrapped: boolean; keys: Map<OpenHandsHookEvent, string> } {
  const wrapped = Object.hasOwn(root, 'hooks');
  if (wrapped && Object.keys(root).some((key) => key !== 'hooks')) {
    throw new Error('OpenHands hooks wrapper contains unsupported top-level fields; no changes were made');
  }
  const map = wrapped ? root.hooks : root;
  if (!isRecord(map)) throw new Error('OpenHands hooks must be an object; no changes were made');
  const keys = new Map<OpenHandsHookEvent, string>();
  for (const key of Object.keys(map)) {
    const normalized = normalizeEvent(key);
    if (!normalized) throw new Error(`OpenHands hooks.json contains unsupported event ${key}; no changes were made`);
    if (keys.has(normalized)) throw new Error(`OpenHands hooks.json declares ${normalized} more than once; no changes were made`);
    keys.set(normalized, key);
    if (map[key] !== undefined && !Array.isArray(map[key])) throw new Error(`OpenHands hooks.${key} must be an array; no changes were made`);
  }
  return { map, wrapped, keys };
}

function normalizeEvent(value: string): OpenHandsHookEvent | undefined {
  const normalized = value.replace(/[^A-Za-z0-9]/g, '').toLowerCase();
  return openHandsHookEvents.find((event) => event.replaceAll('_', '') === normalized);
}

function readGroups(value: unknown, key: string): unknown[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`OpenHands hooks.${key} must be an array; no changes were made`);
  return value;
}

function removeManagedReferences(map: JsonObject, keys: Map<OpenHandsHookEvent, string>): { map: JsonObject; found: boolean } {
  const result: JsonObject = { ...map };
  let found = false;
  for (const [event, key] of keys) {
    const groups = readGroups(map[key], key);
    const retainedGroups: unknown[] = [];
    for (const rawGroup of groups) {
      if (!isRecord(rawGroup) || !Array.isArray(rawGroup.hooks)) {
        throw new Error(`OpenHands hooks.${key} contains an unsupported hook group; no changes were made`);
      }
      const refs = rawGroup.hooks.filter((rawRef) => {
        if (!isRecord(rawRef)) throw new Error(`OpenHands hooks.${key} contains an unsupported hook; no changes were made`);
        const managed = typeof rawRef.command === 'string' && rawRef.command.includes(openHandsManagedMarker);
        if (managed) found = true;
        return !managed;
      });
      if (refs.length === rawGroup.hooks.length) retainedGroups.push(rawGroup);
      else if (refs.length > 0) retainedGroups.push({ ...rawGroup, hooks: refs });
    }
    if (retainedGroups.length === 0) delete result[key];
    else if (retainedGroups.length !== groups.length || retainedGroups.some((group, index) => group !== groups[index])) result[key] = retainedGroups;
  }
  return { map: result, found };
}

function applyHookMap(root: JsonObject, map: JsonObject, wrapped: boolean): JsonObject {
  return wrapped ? { ...root, hooks: map } : { ...map };
}

function replaceObject(target: JsonObject, source: JsonObject): void {
  for (const key of Object.keys(target)) delete target[key];
  Object.assign(target, source);
}

async function mutateOpenHandsConfig(projectRoot: string, create: boolean, mutate: (root: JsonObject) => Outcome): Promise<ChangeResult> {
  if (!path.isAbsolute(projectRoot)) throw new Error('--project-root must be an absolute path');
  const resolved = path.resolve(projectRoot);
  const rootInfo = await lstat(resolved).catch(() => null);
  if (!rootInfo || !rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('project root must exist and be a real directory');
  const canonical = await realpath(resolved).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, resolved)) throw new Error('project root must not traverse symbolic links');
  const configDir = path.join(canonical, '.openhands');
  let dirInfo = await lstat(configDir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!dirInfo && create) {
    await mkdir(configDir).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create OpenHands project config directory'); });
    dirInfo = await lstat(configDir).catch(() => null);
  }
  if (!dirInfo) return 'absent';
  if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink()) throw new Error('OpenHands project config path contains a symbolic link or non-directory');
  const filePath = path.join(configDir, 'hooks.json');
  const lockPath = path.join(configDir, '.threadforge-hooks.lock');
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenHands hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock OpenHands hooks');
  }
  try {
    const root = await readConfig(filePath);
    const before = JSON.stringify(root);
    const outcome = mutate(root);
    if (!outcome.changed) return outcome.result;
    if (outcome.deleteFile) {
      await unlink(filePath).catch((error: unknown) => { if (!isCode(error, 'ENOENT')) throw new Error('unable to remove empty OpenHands hooks file'); });
    } else if (before !== JSON.stringify(root)) await writeConfig(filePath, root);
    return outcome.result;
  } finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<JsonObject> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return {};
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('OpenHands hooks.json must be a regular file');
  if (info.size > maxConfigBytes) throw new Error('OpenHands hooks.json exceeds the local size limit');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('OpenHands hooks.json changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxConfigBytes) throw new Error('OpenHands hooks.json exceeds the local size limit');
    const value: unknown = JSON.parse(bytes.toString('utf8').replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error('OpenHands hooks.json root must be an object');
    return value;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('OpenHands hooks.json')) throw error;
    throw new Error('OpenHands hooks.json is invalid strict JSON; no changes were made');
  } finally { await handle?.close().catch(() => {}); }
}

async function writeConfig(filePath: string, value: JsonObject): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  const serialized = `${JSON.stringify(value, null, 2)}\n`;
  if (Buffer.byteLength(serialized, 'utf8') > maxConfigBytes) throw new Error('OpenHands hooks.json exceeds the local size limit after update');
  let handle;
  try {
    let mode = 0o600;
    try { mode = (await lstat(filePath)).mode & 0o777; }
    catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(serialized, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update OpenHands hooks.json');
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
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function sameFile(left: Stats, right: Stats): boolean { return left.dev === right.dev && left.ino === right.ino; }
function looksCredentialLike(value: string): boolean {
  return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
