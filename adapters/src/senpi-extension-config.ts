import { constants, type Stats } from 'node:fs';
import { createHash, randomUUID } from 'node:crypto';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { senpiExtensionTemplateMarker, senpiManagedMarker } from './senpi-extension.ts';

export type SenpiScope = 'user' | 'project';
export type SenpiExtensionChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export type SenpiExtensionState = 'installed' | 'missing' | 'modified_or_unowned';
export interface SenpiExtensionOptions {
  scope?: SenpiScope;
  projectRoot?: string;
  homeDir?: string;
  senpiAgentDir?: string;
}

type Identity = { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
type Snapshot = { content: string; identity?: Identity };

const extensionFileName = 'threadforge.ts';
const lockFileName = '.threadforge-senpi-extension.lock';
const maxExtensionBytes = 512 << 10;
const ownershipHeader = `${senpiManagedMarker} sha256=`;
const senpiAgentEnvironment = ['SENPI_CODING_AGENT_DIR', 'PI_CODING_AGENT_DIR'] as const;

/** Install a self-contained observer in Senpi's user or project extension directory. */
export async function installSenpiExtension(options: SenpiExtensionOptions = {}): Promise<SenpiExtensionChange> {
  const scope = options.scope ?? 'user';
  const file = await resolveExtensionPath({ ...options, scope }, true);
  if (!file) throw new Error('unable to resolve Senpi extension path');
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (current.snapshot.identity) verifyOwnedContent(current.content);
    const desired = await managedExtensionSource();
    if (current.content === desired) return 'unchanged';
    await atomicWrite(file, current.snapshot, desired, scope);
    return current.snapshot.identity ? 'updated' : 'installed';
  });
}

/** Refresh only a Threadforge-owned, unmodified Senpi extension. */
export async function updateSenpiExtension(options: SenpiExtensionOptions = {}): Promise<SenpiExtensionChange> {
  const scope = options.scope ?? 'user';
  const file = await resolveExtensionPath({ ...options, scope }, false);
  if (!file) throw new Error('no Threadforge Senpi extension exists to update');
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (!current.snapshot.identity) throw new Error('no Threadforge Senpi extension exists to update');
    verifyOwnedContent(current.content);
    const desired = await managedExtensionSource();
    if (current.content === desired) return 'unchanged';
    await atomicWrite(file, current.snapshot, desired, scope);
    return 'updated';
  });
}

/** Remove only a Threadforge-owned Senpi extension whose content still matches its digest. */
export async function removeSenpiExtension(options: SenpiExtensionOptions = {}): Promise<SenpiExtensionChange> {
  const scope = options.scope ?? 'user';
  const file = await resolveExtensionPath({ ...options, scope }, false);
  if (!file) return 'absent';
  return withLock(file, async () => {
    const current = await readExtension(file);
    if (!current.snapshot.identity) return 'absent';
    verifyOwnedContent(current.content);
    await verifySnapshot(file, current.snapshot);
    await unlink(file);
    return 'removed';
  });
}

/** Report extension ownership state without exposing its path or reading its contents to callers. */
export async function getSenpiExtensionState(options: SenpiExtensionOptions = {}): Promise<SenpiExtensionState> {
  const file = await resolveExtensionPath({ ...options, scope: options.scope ?? 'user' }, false);
  if (!file) return 'missing';
  const current = await readExtension(file);
  if (!current.snapshot.identity) return 'missing';
  try { verifyOwnedContent(current.content); return 'installed'; }
  catch { return 'modified_or_unowned'; }
}

/** `root` is Senpi's agent directory for user scope and workspace root for project scope. */
export function senpiExtensionPath(scope: SenpiScope, root: string): string {
  return scope === 'project'
    ? path.join(root, '.senpi', 'extensions', extensionFileName)
    : path.join(root, 'extensions', extensionFileName);
}

async function resolveExtensionPath(options: SenpiExtensionOptions & { scope: SenpiScope }, create: boolean): Promise<string | undefined> {
  if (options.scope !== 'user' && options.scope !== 'project') throw new Error('Senpi scope must be user or project');
  let agentRoot: string | undefined;
  if (options.scope === 'project') {
    if (!options.projectRoot || !path.isAbsolute(options.projectRoot)) throw new Error('project scope requires an absolute project root');
    agentRoot = await safeDirectory(options.projectRoot, false, 'Senpi project root');
    if (!agentRoot) throw new Error('Senpi project root must exist and be accessible');
    agentRoot = await childDirectory(agentRoot, '.senpi', create, 'Senpi project configuration directory', 0o755);
    if (agentRoot) {
      const extensionDir = await childDirectory(agentRoot, 'extensions', create, 'Senpi project extension directory', 0o755);
      if (!extensionDir) return undefined;
      return checkedExtensionFile(extensionDir, create);
    }
  } else {
    const homeInput = options.homeDir ?? os.homedir();
    if (!path.isAbsolute(homeInput)) throw new Error('Senpi home directory must be absolute');
    const home = await safeDirectory(homeInput, false, 'Senpi home directory');
    if (!home) throw new Error('Senpi home directory must exist and be accessible');
    if (options.senpiAgentDir !== undefined) {
      if (!options.senpiAgentDir.trim()) throw new Error('--senpi-agent-dir must not be empty');
      const expanded = expandHomePrefix(options.senpiAgentDir, home);
      if (!path.isAbsolute(expanded)) throw new Error('--senpi-agent-dir must be absolute');
      agentRoot = await safeDirectory(expanded, create, 'Senpi agent directory');
    } else {
      const configured = configuredAgentDirectory();
      if (configured !== undefined) {
        if (!configured.trim()) throw new Error('Senpi coding-agent directory must not be empty');
        const resolved = resolveRuntimeAgentDirectory(configured, home);
        agentRoot = await safeDirectory(resolved, create, 'Senpi coding-agent directory');
      } else {
        agentRoot = await childDirectory(home, '.senpi', create, 'Senpi configuration directory');
        if (agentRoot) agentRoot = await childDirectory(agentRoot, 'agent', create, 'Senpi agent directory');
      }
    }
  }
  if (!agentRoot) return undefined;
  const extensionDir = await childDirectory(agentRoot, 'extensions', create, 'Senpi extension directory');
  if (!extensionDir) return undefined;
  return checkedExtensionFile(extensionDir, create);
}

async function checkedExtensionFile(extensionDir: string, create: boolean): Promise<string | undefined> {
  const file = path.join(extensionDir, extensionFileName);
  const info = await lstat(file).catch((error: unknown) => isCode(error, 'ENOENT') ? undefined : Promise.reject(error));
  if (info && (!info.isFile() || info.isSymbolicLink())) throw new Error('Senpi extension must be a regular file and not a symbolic link');
  if (info && info.size > maxExtensionBytes) throw new Error('Senpi extension exceeds the local size limit');
  return info || create ? file : undefined;
}

function configuredAgentDirectory(): string | undefined {
  for (const name of senpiAgentEnvironment) {
    if (Object.hasOwn(process.env, name)) {
      const value = process.env[name];
      // Senpi stops at the first defined variable; an empty primary override selects its default path.
      return value || undefined;
    }
  }
  return undefined;
}

function expandHomePrefix(value: string, home: string): string {
  if (value === '~') return home;
  if (value.startsWith('~/') || value.startsWith('~\\')) return path.join(home, value.slice(2));
  return value;
}

function resolveRuntimeAgentDirectory(value: string, home: string): string {
  const expanded = expandHomePrefix(value, home);
  if (/^file:\/\//i.test(expanded)) {
    try { return fileURLToPath(new URL(expanded)); }
    catch { throw new Error('Senpi coding-agent file URL is invalid'); }
  }
  return path.isAbsolute(expanded) ? expanded : path.resolve(expanded);
}

async function safeDirectory(input: string, create: boolean, label: string): Promise<string | undefined> {
  if (!path.isAbsolute(input)) throw new Error(`${label} must be an absolute directory`);
  const normalized = path.resolve(input);
  const parsed = path.parse(normalized);
  let current = parsed.root;
  for (const segment of normalized.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, segment);
    let info = await lstat(current).catch((error: unknown) => {
      if (isCode(error, 'ENOENT')) return undefined;
      throw error;
    });
    if (!info && !create) return undefined;
    if (!info && create) {
      try { await mkdir(current, { mode: 0o700 }); }
      catch (error) { if (!isCode(error, 'EEXIST')) throw new Error(`unable to create ${label.toLowerCase()}`); }
      info = await lstat(current);
    }
    if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} path contains a symbolic link or non-directory`);
    const actual = await realpath(current).catch(() => { throw new Error(`${label} is inaccessible`); });
    if (!samePath(actual, current)) throw new Error(`${label} path traverses a symbolic link`);
    current = actual;
  }
  return current;
}

async function childDirectory(parent: string, child: string, create: boolean, label: string, mode = 0o700): Promise<string | undefined> {
  const target = path.join(parent, child);
  let info = await lstat(target).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (!info && !create) return undefined;
  if (!info && create) {
    try { await mkdir(target, { mode }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error(`unable to create ${label.toLowerCase()}`); }
    info = await lstat(target);
  }
  if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} contains a symbolic link or non-directory`);
  const actual = await realpath(target).catch(() => { throw new Error(`${label} is inaccessible`); });
  if (!isWithin(parent, actual)) throw new Error(`${label} resolves outside the selected root`);
  return actual;
}

async function withLock<T>(file: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(file), lockFileName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Senpi extension is locked by another writer or a stale lock');
    throw new Error('unable to lock Senpi extension directory');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readExtension(file: string): Promise<{ content: string; snapshot: Snapshot }> {
  try {
    const before = await lstat(file);
    if (!before.isFile() || before.isSymbolicLink()) throw new Error('Senpi extension must be a regular file and not a symbolic link');
    if (before.size > maxExtensionBytes) throw new Error('Senpi extension exceeds the local size limit');
    const bytes = await readFile(file);
    const after = await lstat(file);
    if (!sameIdentity(identityOf(before), identityOf(after)) || bytes.byteLength !== after.size) {
      throw new Error('Senpi extension changed while being read; no changes were made');
    }
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    return { content, snapshot: { content, identity: identityOf(after) } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { content: '', snapshot: { content: '' } };
    if (error instanceof Error && error.message.startsWith('Senpi extension')) throw error;
    throw new Error('Senpi extension is invalid UTF-8; no changes were made');
  }
}

async function managedExtensionSource(): Promise<string> {
  let source: string;
  try { source = new TextDecoder('utf-8', { fatal: true }).decode(await readFile(new URL('./senpi-extension.ts', import.meta.url))); }
  catch { throw new Error('Threadforge Senpi extension source is unavailable'); }
  const newline = source.indexOf('\n');
  const firstLine = (newline < 0 ? source : source.slice(0, newline)).replace(/\r$/, '');
  if (firstLine !== senpiExtensionTemplateMarker || newline < 0) throw new Error('Threadforge Senpi extension template marker is invalid');
  const body = source.slice(newline + 1);
  const hash = createHash('sha256').update(body, 'utf8').digest('hex');
  const managed = `${ownershipHeader}${hash}\n${body}`;
  if (Buffer.byteLength(managed, 'utf8') > maxExtensionBytes) throw new Error('Threadforge Senpi extension exceeds the local size limit');
  return managed;
}

function verifyOwnedContent(content: string): void {
  const newline = content.indexOf('\n');
  const header = newline < 0 ? content : content.slice(0, newline).replace(/\r$/, '');
  const expected = header.startsWith(ownershipHeader) ? header.slice(ownershipHeader.length) : '';
  const body = newline < 0 ? '' : content.slice(newline + 1);
  const actual = createHash('sha256').update(body, 'utf8').digest('hex');
  if (!/^[a-f0-9]{64}$/.test(expected) || actual !== expected) {
    throw new Error('Senpi extension is unowned or was modified; inspect it before updating or removing');
  }
}

async function atomicWrite(file: string, snapshot: Snapshot, content: string, scope: SenpiScope): Promise<void> {
  const bytes = Buffer.from(content, 'utf8');
  if (bytes.byteLength > maxExtensionBytes) throw new Error('Senpi extension would exceed the local size limit');
  const temp = path.join(path.dirname(file), `.threadforge-senpi-${randomUUID()}.tmp`);
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : scope === 'project' ? 0o644 : 0o600;
  let handle;
  try {
    await verifySnapshot(file, snapshot);
    handle = await open(temp, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await verifySnapshot(file, snapshot);
    await rename(temp, file);
  } catch (error) {
    await handle?.close().catch(() => {});
    await unlink(temp).catch(() => {});
    if (error instanceof Error && error.message.includes('changed')) throw error;
    throw new Error('unable to atomically update Senpi extension');
  }
}

async function verifySnapshot(file: string, snapshot: Snapshot): Promise<void> {
  const info = await lstat(file).catch((error: unknown) => isCode(error, 'ENOENT') ? undefined : Promise.reject(error));
  if (!snapshot.identity) {
    if (info) throw new Error('Senpi extension appeared during update; no changes were made');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || !sameIdentity(snapshot.identity, identityOf(info))) {
    throw new Error('Senpi extension changed during update; no changes were made');
  }
  const content = await readFile(file);
  if (content.byteLength !== Buffer.byteLength(snapshot.content, 'utf8') || content.toString('utf8') !== snapshot.content) {
    throw new Error('Senpi extension changed during update; no changes were made');
  }
}

function identityOf(stat: Stats): Identity { return { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs }; }
function sameIdentity(a: Identity, b: Identity): boolean {
  return a.dev === b.dev && a.ino === b.ino && a.mode === b.mode && a.size === b.size && a.mtimeMs === b.mtimeMs;
}
function samePath(a: string, b: string): boolean {
  const normalize = (value: string) => path.resolve(value).replace(/[\\/]+$/, '');
  return process.platform === 'win32' ? normalize(a).toLowerCase() === normalize(b).toLowerCase() : normalize(a) === normalize(b);
}
function isWithin(root: string, target: string): boolean {
  const relative = path.relative(root, target);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}
function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
