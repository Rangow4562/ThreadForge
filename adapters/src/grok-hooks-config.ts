import { constants } from 'node:fs';
import { createHash, randomUUID } from 'node:crypto';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { grokHookActions, grokManagedMarker } from './grok-hook.ts';

export type GrokHookScope = 'user' | 'project';
export type GrokHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

export interface GrokHookConfigOptions {
  commandTemplate: string;
  scope?: GrokHookScope;
  projectRoot?: string;
  homeDir?: string;
}
export interface GrokHookPathOptions extends Omit<GrokHookConfigOptions, 'commandTemplate'> {}

const configFileName = 'threadforge.json';
const lockFileName = '.threadforge-grok-hooks.lock';
const maxConfigBytes = 1 << 20;
const ownerPrefix = 'Threadforge managed Grok Build hooks; threadforge-owner-v1:';
const hostEvents: Record<(typeof grokHookActions)[number], string> = {
  'session-start': 'SessionStart',
  'prompt-submit': 'UserPromptSubmit',
  'pre-tool': 'PreToolUse',
  'post-tool': 'PostToolUse',
  'post-tool-failure': 'PostToolUseFailure',
  stop: 'Stop',
  'session-end': 'SessionEnd',
};

type JsonObject = Record<string, unknown>;
interface Snapshot { dev: number; ino: number; mode: number; size: number; mtimeMs: number; hash: string }

/** Installs only Threadforge's dedicated Grok hook file; trust settings and neighboring files are untouched. */
export async function installGrokHooks(options: GrokHookConfigOptions): Promise<GrokHookChange> {
  validateCommandTemplate(options.commandTemplate);
  const scope = options.scope ?? 'user';
  const file = await resolveConfigPath({ ...options, scope }, true);
  if (!file) throw new Error('unable to resolve Grok hook config path');
  return withLock(file, async () => {
    const current = await readConfig(file);
    const desired = ownedConfig(options.commandTemplate);
    if (current.value && !isOwnedConfig(current.value)) {
      throw new Error('Grok Threadforge hook file is unowned or edited; no changes were made');
    }
    const bytes = serializeConfig(desired);
    if (current.snapshot && current.bytes?.equals(bytes)) return 'unchanged';
    await atomicWrite(file, current.snapshot, bytes, scope === 'user' ? 0o600 : 0o644);
    return current.snapshot ? 'updated' : 'installed';
  });
}

export async function removeGrokHooks(options: GrokHookPathOptions = {}): Promise<GrokHookChange> {
  const file = await resolveConfigPath({ ...options, scope: options.scope ?? 'user' }, false);
  if (!file) return 'absent';
  return withLock(file, async () => {
    const current = await readConfig(file);
    if (!current.snapshot) return 'absent';
    if (!current.value || !isOwnedConfig(current.value)) {
      throw new Error('Grok Threadforge hook file is unowned or edited; refusing removal');
    }
    await assertUnchanged(file, current.snapshot);
    await unlink(file);
    return 'removed';
  });
}

export async function grokHooksConfigPath(options: GrokHookPathOptions = {}): Promise<string | undefined> {
  return resolveConfigPath({ ...options, scope: options.scope ?? 'user' }, false);
}

export function grokHooksPath(scope: GrokHookScope, root: string): string {
  return path.join(root, '.grok', 'hooks', configFileName);
}

async function resolveConfigPath(options: GrokHookPathOptions & { scope: GrokHookScope }, create: boolean): Promise<string | undefined> {
  const { scope } = options;
  if (scope !== 'user' && scope !== 'project') throw new Error('Grok hook scope must be user or project');
  const inputRoot = scope === 'project' ? options.projectRoot : options.homeDir ?? os.homedir();
  if (!inputRoot || !path.isAbsolute(inputRoot)) throw new Error(`${scope} scope requires an absolute Grok root`);
  const root = await existingDirectory(inputRoot, 'Grok hook root');
  let directory = root;
  for (const part of ['.grok', 'hooks']) {
    const child = path.join(directory, part);
    const found = await lstat(child).catch(async (error: unknown) => {
      if (!isCode(error, 'ENOENT')) throw error;
      if (!create) return undefined;
      await mkdir(child, { mode: scope === 'user' ? 0o700 : 0o755 }).catch((mkdirError: unknown) => {
        if (!isCode(mkdirError, 'EEXIST')) throw new Error('unable to create Grok hook directory');
      });
      return lstat(child);
    });
    if (!found) return undefined;
    if (!found.isDirectory() || found.isSymbolicLink()) throw new Error('Grok hook path contains a symbolic link or non-directory');
    const resolved = await realpath(child).catch(() => { throw new Error('Grok hook directory is inaccessible'); });
    if (!isWithin(root, resolved)) throw new Error('Grok hook directory resolves outside the selected root');
    directory = resolved;
  }
  const file = path.join(directory, configFileName);
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (info && (!info.isFile() || info.isSymbolicLink())) throw new Error('Grok Threadforge hook config must be a regular non-symlink file');
  if (info && info.size > maxConfigBytes) throw new Error('Grok Threadforge hook config exceeds the size limit');
  return info || create ? file : undefined;
}

function ownedConfig(commandTemplate: string): JsonObject {
  const hooks: JsonObject = {};
  for (const action of grokHookActions) {
    const event = hostEvents[action];
    const command = commandTemplate.replace('{event}', action);
    hooks[event] = [{ hooks: [{ type: 'command', command, timeout: 5 }] }];
  }
  const unsigned: JsonObject = { description: '', hooks };
  const digest = sha256(canonicalJson(unsigned));
  return { ...unsigned, description: `${ownerPrefix}${digest}` };
}

function isOwnedConfig(value: JsonObject): boolean {
  if (Object.keys(value).sort().join(',') !== 'description,hooks' || typeof value.description !== 'string' || !isRecord(value.hooks)) return false;
  if (!value.description.startsWith(ownerPrefix)) return false;
  const digest = value.description.slice(ownerPrefix.length);
  if (!/^[a-f0-9]{64}$/.test(digest)) return false;
  const withoutOwner: JsonObject = { description: '', hooks: value.hooks };
  if (sha256(canonicalJson(withoutOwner)) !== digest) return false;
  if (Object.keys(value.hooks).sort().join(',') !== Object.values(hostEvents).sort().join(',')) return false;
  for (const action of grokHookActions) {
    const groups = value.hooks[hostEvents[action]];
    if (!Array.isArray(groups) || groups.length !== 1 || !isRecord(groups[0]) || Object.keys(groups[0]).sort().join(',') !== 'hooks') return false;
    const refs = groups[0].hooks;
    if (!Array.isArray(refs) || refs.length !== 1 || !isRecord(refs[0])) return false;
    const ref = refs[0];
    if (Object.keys(ref).sort().join(',') !== 'command,timeout,type' || ref.type !== 'command' || ref.timeout !== 5 ||
        typeof ref.command !== 'string' || !ref.command.includes(grokManagedMarker) || !ref.command.includes(`grok hook ${action}`)) return false;
  }
  return true;
}

function validateCommandTemplate(value: string): void {
  if (typeof value !== 'string' || value.trim().length < 1 || value.length > 4096 || /[\r\n\0]/.test(value) ||
      value.split('{event}').length !== 2 || !value.includes(grokManagedMarker) || !value.includes('grok hook {event}') ||
      looksCredentialLike(value)) {
    throw new Error('Grok command template must contain one {event}, the Threadforge marker, and no credentials');
  }
}

function looksCredentialLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

async function existingDirectory(input: string, label: string): Promise<string> {
  const normalized = path.resolve(input);
  const parsed = path.parse(normalized);
  let current = parsed.root;
  for (const part of normalized.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    const info = await lstat(current).catch(() => { throw new Error(`${label} must exist and be accessible`); });
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error(`${label} contains a symbolic link or non-directory`);
  }
  return realpath(normalized).catch(() => { throw new Error(`${label} must exist and be accessible`); });
}

async function withLock<T>(file: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(file), lockFileName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Grok hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Grok hook config');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(file: string): Promise<{ value?: JsonObject; bytes?: Buffer; snapshot?: Snapshot }> {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Grok Threadforge hook config must be a regular non-symlink file');
    if (info.size > maxConfigBytes) throw new Error('Grok Threadforge hook config exceeds the size limit');
    const bytes = await readFile(file);
    if (bytes.byteLength > maxConfigBytes) throw new Error('Grok Threadforge hook config exceeds the size limit');
    const value: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    if (!isRecord(value)) throw new Error('Grok hook config root must be an object');
    return { value, bytes, snapshot: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs, hash: sha256(bytes.toString('base64')) } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith('Grok')) throw error;
    throw new Error('Grok Threadforge hook config is not valid UTF-8 JSON; no changes were made');
  }
}

async function atomicWrite(file: string, snapshot: Snapshot | undefined, bytes: Buffer, mode: number): Promise<void> {
  if (bytes.byteLength > maxConfigBytes) throw new Error('Grok Threadforge hook config exceeds the size limit');
  const temp = `${file}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    await assertUnchanged(file, snapshot);
    handle = await open(temp, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(file, snapshot);
    await rename(temp, file);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(temp).catch(() => {});
    throw new Error('unable to atomically update Grok Threadforge hook config');
  }
}

async function assertUnchanged(file: string, snapshot: Snapshot | undefined): Promise<void> {
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (!snapshot) {
    if (info) throw new Error('Grok hook config appeared during update');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.dev || info.ino !== snapshot.ino ||
      info.size !== snapshot.size || info.mtimeMs !== snapshot.mtimeMs || sha256((await readFile(file)).toString('base64')) !== snapshot.hash) {
    throw new Error('Grok hook config changed during update');
  }
}

function serializeConfig(value: JsonObject): Buffer { return Buffer.from(`${JSON.stringify(value, null, 2)}\n`, 'utf8'); }
function canonicalJson(value: unknown): string {
  const sorted = (item: unknown): unknown => Array.isArray(item) ? item.map(sorted) : isRecord(item)
    ? Object.fromEntries(Object.keys(item).sort().map((key) => [key, sorted(item[key])])) : item;
  return JSON.stringify(sorted(value));
}
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && (error as { code?: unknown }).code === code; }
function isWithin(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}
