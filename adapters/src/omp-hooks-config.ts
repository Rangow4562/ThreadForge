import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { ompExtensionTemplateMarker, ompManagedMarker } from './omp-extension.ts';

export type OmpHookScope = 'user' | 'project';
export type OmpHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export interface OmpHookConfigOptions {
  scope: OmpHookScope;
  projectRoot?: string;
  ompAgentDir?: string;
  homeDir?: string;
}

const extensionFileName = 'threadforge.ts';
const lockName = '.threadforge-omp-extension.lock';
const maxExtensionBytes = 512 << 10;
const ownedHeader = `${ompManagedMarker} sha256=`;

interface FileSnapshot {
  content: string;
  identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
}

/** Installs the self-contained observer in an Oh My Pi extension directory. */
export async function installOmpExtension(options: OmpHookConfigOptions): Promise<OmpHookChange> {
  const file = await resolveExtensionPath(options, true);
  if (!file) throw new Error('unable to resolve Oh My Pi extension path');
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (current.snapshot.identity) verifyOwnedContent(current.content);
    const desired = await managedExtensionSource();
    if (current.content === desired) return 'unchanged';
    await atomicWrite(file, current.snapshot, desired, options.scope);
    return current.snapshot.identity ? 'updated' : 'installed';
  });
}

export async function updateOmpExtension(options: OmpHookConfigOptions): Promise<OmpHookChange> {
  const file = await resolveExtensionPath(options, false);
  if (!file) throw new Error('no Threadforge Oh My Pi extension exists to update');
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (!current.snapshot.identity) throw new Error('no Threadforge Oh My Pi extension exists to update');
    verifyOwnedContent(current.content);
    const desired = await managedExtensionSource();
    if (current.content === desired) return 'unchanged';
    await atomicWrite(file, current.snapshot, desired, options.scope);
    return 'updated';
  });
}

export async function removeOmpExtension(options: OmpHookConfigOptions): Promise<OmpHookChange> {
  const file = await resolveExtensionPath(options, false);
  if (!file) return 'absent';
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (!current.snapshot.identity) return 'absent';
    verifyOwnedContent(current.content);
    await assertUnchanged(file, current.snapshot);
    await unlink(file);
    return 'removed';
  });
}

/** `root` is an OMP agent directory for user scope, or the repository root for project scope. */
export function ompExtensionPath(scope: OmpHookScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.omp', 'extensions', extensionFileName)
    : path.join(root, 'extensions', extensionFileName);
}

async function resolveExtensionPath(options: OmpHookConfigOptions, create: boolean): Promise<string | undefined> {
  if (options.scope !== 'user' && options.scope !== 'project') throw new Error('Oh My Pi scope must be user or project');
  let root: string;
  let directoryParts: string[] = ['extensions'];
  if (options.scope === 'project') {
    if (!options.projectRoot || !path.isAbsolute(options.projectRoot)) throw new Error('project scope requires an absolute project root');
    root = await existingDirectory(options.projectRoot, 'Oh My Pi project root');
    directoryParts = ['.omp', 'extensions'];
  } else if (options.ompAgentDir) {
    if (!path.isAbsolute(options.ompAgentDir)) throw new Error('--omp-agent-dir must be absolute');
    root = await existingDirectory(options.ompAgentDir, 'Oh My Pi agent directory');
  } else {
    const home = await existingDirectory(options.homeDir ?? os.homedir(), 'home directory');
    const profileValue = Object.hasOwn(process.env, 'OMP_PROFILE') ? process.env.OMP_PROFILE : process.env.PI_PROFILE;
    const profile = normalizeProfile(profileValue);
    const configName = normalizeConfigDirectory(process.env.PI_CONFIG_DIR);
    const agentOverride = process.env.PI_CODING_AGENT_DIR;
    if (profile) {
      directoryParts = [configName, 'profiles', profile, 'agent', 'extensions'];
    } else if (agentOverride) {
      if (!path.isAbsolute(agentOverride)) throw new Error('PI_CODING_AGENT_DIR must be an absolute directory');
      root = await existingDirectory(agentOverride, 'PI_CODING_AGENT_DIR');
      directoryParts = ['extensions'];
      return extensionFile(root, directoryParts, create, options.scope);
    } else {
      directoryParts = [configName, 'agent', 'extensions'];
    }
    return extensionFile(home, directoryParts, create, options.scope);
  }
  return extensionFile(root, directoryParts, create, options.scope);
}

async function extensionFile(root: string, parts: string[], create: boolean, scope: OmpHookScope): Promise<string | undefined> {
  const canonicalRoot = await realpath(root);
  let directory = canonicalRoot;
  for (const part of parts) {
    directory = path.join(directory, part);
    const info = await lstat(directory).catch(async (error: unknown) => {
      if (!isCode(error, 'ENOENT')) throw error;
      if (!create) return undefined;
      await mkdir(directory, { mode: scope === 'project' ? 0o755 : 0o700 }).catch((mkdirError: unknown) => {
        if (!isCode(mkdirError, 'EEXIST')) throw new Error('unable to create Oh My Pi extension directory');
      });
      return lstat(directory);
    });
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Oh My Pi extension path contains a symbolic link or non-directory');
    const resolved = await realpath(directory).catch(() => { throw new Error('Oh My Pi extension directory is inaccessible'); });
    if (!isWithin(canonicalRoot, resolved)) throw new Error('Oh My Pi extension directory resolves outside its selected root');
    directory = resolved;
  }
  const file = path.join(directory, extensionFileName);
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (info && (!info.isFile() || info.isSymbolicLink())) throw new Error('Oh My Pi extension must be a regular file and not a symbolic link');
  if (info && info.size > maxExtensionBytes) throw new Error('Oh My Pi extension exceeds the local size limit');
  return info || create ? file : undefined;
}

async function existingDirectory(input: string, label: string): Promise<string> {
  if (!path.isAbsolute(input)) throw new Error(`${label} must be an absolute directory`);
  const normalized = path.resolve(input);
  const parsed = path.parse(normalized);
  let current = parsed.root;
  for (const part of normalized.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    const info = await lstat(current).catch(() => { throw new Error(`${label} must exist and be accessible`); });
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error(`${label} path contains a symbolic link or non-directory`);
  }
  return realpath(normalized).catch(() => { throw new Error(`${label} must exist and be accessible`); });
}

async function withLock<T>(file: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(file), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Oh My Pi extension is locked by another writer or a stale lock');
    throw new Error('unable to lock Oh My Pi extension directory');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readExtension(file: string): Promise<{ content: string; snapshot: FileSnapshot }> {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Oh My Pi extension must be a regular file and not a symbolic link');
    if (info.size > maxExtensionBytes) throw new Error('Oh My Pi extension exceeds the local size limit');
    const bytes = await readFile(file);
    if (bytes.byteLength > maxExtensionBytes) throw new Error('Oh My Pi extension exceeds the local size limit');
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    return {
      content,
      snapshot: { content, identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } },
    };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { content: '', snapshot: { content: '' } };
    if (error instanceof Error && error.message.startsWith('Oh My Pi ')) throw error;
    throw new Error('Oh My Pi extension is invalid UTF-8; no changes were made');
  }
}

async function managedExtensionSource(): Promise<string> {
  let source: string;
  try { source = new TextDecoder('utf-8', { fatal: true }).decode(await readFile(new URL('./omp-extension.ts', import.meta.url))); }
  catch { throw new Error('Threadforge Oh My Pi extension source is unavailable'); }
  const newline = source.indexOf('\n');
  const firstLine = (newline < 0 ? source : source.slice(0, newline)).replace(/\r$/, '');
  if (firstLine !== ompExtensionTemplateMarker || newline < 0) throw new Error('Threadforge Oh My Pi extension template marker is invalid');
  const body = source.slice(newline + 1);
  const hash = createHash('sha256').update(body, 'utf8').digest('hex');
  const result = `${ownedHeader}${hash}\n${body}`;
  if (Buffer.byteLength(result, 'utf8') > maxExtensionBytes) throw new Error('Threadforge Oh My Pi extension exceeds the local size limit');
  return result;
}

function verifyOwnedContent(content: string): void {
  const newline = content.indexOf('\n');
  const header = newline < 0 ? content : content.slice(0, newline).replace(/\r$/, '');
  const expected = header.startsWith(ownedHeader) ? header.slice(ownedHeader.length) : '';
  const body = newline < 0 ? '' : content.slice(newline + 1);
  const actual = createHash('sha256').update(body, 'utf8').digest('hex');
  if (!/^[a-f0-9]{64}$/.test(expected) || actual !== expected) {
    throw new Error('Oh My Pi extension is unowned or was modified; inspect it before updating or removing');
  }
}

async function atomicWrite(file: string, snapshot: FileSnapshot, content: string, scope: OmpHookScope): Promise<void> {
  const bytes = Buffer.from(content, 'utf8');
  if (bytes.byteLength > maxExtensionBytes) throw new Error('Oh My Pi extension would exceed the local size limit');
  const temp = `${file}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : scope === 'project' ? 0o644 : 0o600;
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
    throw new Error('unable to atomically update Oh My Pi extension');
  }
}

async function assertUnchanged(file: string, snapshot: FileSnapshot): Promise<void> {
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (info) throw new Error('Oh My Pi extension appeared during update');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
      info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Oh My Pi extension changed during update');
  }
}

function normalizeProfile(value: string | undefined): string | undefined {
  const name = value?.trim();
  if (!name || name === 'default') return undefined;
  if (name === '.' || name === '..' || name.endsWith('.') || !/^[a-z0-9][a-z0-9._-]{0,63}$/.test(name) ||
      /^(?:CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(?:\..*)?$/i.test(name)) {
    throw new Error('OMP profile name is invalid; pass --omp-agent-dir for a non-default profile');
  }
  return name;
}

function normalizeConfigDirectory(value: string | undefined): string {
  const name = value?.trim() || '.omp';
  if (name === '.' || name === '..' || path.isAbsolute(name) || /[\\/]/.test(name) || /[\0]/.test(name)) {
    throw new Error('PI_CONFIG_DIR must be one relative directory name');
  }
  return name;
}

function isWithin(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
