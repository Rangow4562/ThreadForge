import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink, type FileHandle } from 'node:fs/promises';
import { fileURLToPath, pathToFileURL } from 'node:url';
import os from 'node:os';
import path from 'node:path';

export type ClinePluginScope = 'user' | 'project';
export type ClinePluginChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export type ClinePluginState = 'installed' | 'absent' | 'unmanaged' | 'modified' | 'source-unavailable' | 'source-changed';

export interface ClinePluginOptions {
  scope?: ClinePluginScope;
  projectRoot?: string;
  clineDir?: string;
}

export interface ClinePluginResult {
  schema_version: 'threadforge.cline-plugin-result.v1';
  scope: ClinePluginScope;
  result: ClinePluginChange | ClinePluginState;
  plugin_path?: string;
  module_sha256?: string;
}

const managedMarker = '// threadforge-managed-cline-plugin:v1 sha256=';
const ownerComment = '// Threadforge observational Cline plugin wrapper; callbacks are non-authorizing.';
const moduleComment = '// threadforge-module-sha256:';
const pluginName = 'threadforge.ts';
const lockName = '.threadforge-cline-plugin.lock';
const maxPluginBytes = 64 << 10;
const maxModuleBytes = 512 << 10;

type Paths = { scope: ClinePluginScope; root: string; directory: string; plugin: string };
type Snapshot = { bytes: Buffer; stat: Stats };
type ManagedContent = { content: Buffer; moduleURL: string; moduleHash: string };

export async function installClinePlugin(options: ClinePluginOptions = {}): Promise<ClinePluginResult> {
  const paths = await resolvePaths(options, true);
  if (!paths) throw new Error('unable to create the selected Cline plugin directory');
  const desired = await renderManagedContent();
  return withLock(paths, async () => {
    const current = await readOptionalPlugin(paths.plugin);
    if (current) {
      verifyOwned(current.bytes);
      if (current.bytes.equals(desired.content)) return result(paths, 'unchanged', desired.moduleHash);
      await replaceAtomically(paths.plugin, current, desired.content, paths.scope);
      return result(paths, 'updated', desired.moduleHash);
    }
    await writeExclusive(paths.plugin, desired.content, paths.scope === 'user' ? 0o600 : 0o644);
    return result(paths, 'installed', desired.moduleHash);
  });
}

export async function updateClinePlugin(options: ClinePluginOptions = {}): Promise<ClinePluginResult> {
  const paths = await resolvePaths(options, false);
  if (!paths) throw new Error('no managed Threadforge Cline plugin exists in the selected scope');
  const desired = await renderManagedContent();
  return withLock(paths, async () => {
    const current = await readOptionalPlugin(paths.plugin);
    if (!current) throw new Error('no managed Threadforge Cline plugin exists in the selected scope');
    verifyOwned(current.bytes);
    if (current.bytes.equals(desired.content)) return result(paths, 'unchanged', desired.moduleHash);
    await replaceAtomically(paths.plugin, current, desired.content, paths.scope);
    return result(paths, 'updated', desired.moduleHash);
  });
}

export async function uninstallClinePlugin(options: ClinePluginOptions = {}): Promise<ClinePluginResult> {
  const paths = await resolvePaths(options, false);
  if (!paths) return { schema_version: 'threadforge.cline-plugin-result.v1', scope: validateScope(options.scope), result: 'absent' };
  return withLock(paths, async () => {
    const current = await readOptionalPlugin(paths.plugin);
    if (!current) return result(paths, 'absent');
    verifyOwned(current.bytes);
    await assertUnchanged(paths.plugin, current);
    await unlink(paths.plugin);
    return result(paths, 'removed');
  });
}

/** Inspect only the Threadforge-owned entry; unrelated Cline plugins are never enumerated or changed. */
export async function getClinePluginStatus(options: ClinePluginOptions = {}): Promise<ClinePluginResult> {
  const paths = await resolvePaths(options, false);
  if (!paths) return { schema_version: 'threadforge.cline-plugin-result.v1', scope: validateScope(options.scope), result: 'absent' };
  const current = await readOptionalPlugin(paths.plugin);
  if (!current) return result(paths, 'absent');
  let managed: ManagedContent;
  try { managed = verifyOwned(current.bytes); }
  catch (error) {
    return result(paths, error instanceof Error && error.message.includes('unmanaged') ? 'unmanaged' : 'modified');
  }
  const module = await readOptionalModule(managed.moduleURL);
  if (!module) return result(paths, 'source-unavailable', managed.moduleHash);
  return result(paths, sha256(module) === managed.moduleHash ? 'installed' : 'source-changed', managed.moduleHash);
}

export function clinePluginPath(scope: ClinePluginScope, root: string): string {
  if (scope === 'project') return path.join(root, '.cline', 'plugins', pluginName);
  if (scope === 'user') return path.join(root, 'plugins', pluginName);
  throw new Error('Cline plugin scope must be user or project');
}

async function renderManagedContent(): Promise<ManagedContent> {
  const sourcePath = fileURLToPath(new URL('./cline-plugin.ts', import.meta.url));
  const sourceReal = await realpath(sourcePath).catch(() => '');
  if (!sourceReal) throw new Error('Threadforge Cline adapter module is unavailable');
  const sourceBytes = await readRequiredModule(sourceReal);
  const moduleHash = sha256(sourceBytes);
  const moduleURL = pathToFileURL(sourceReal).href;
  const body = `${ownerComment}\n${moduleComment}${moduleHash}\nimport plugin from ${JSON.stringify(moduleURL)};\nexport default plugin;\n`;
  const bodyBytes = Buffer.from(body, 'utf8');
  const content = Buffer.concat([Buffer.from(`${managedMarker}${sha256(bodyBytes)}\n`, 'utf8'), bodyBytes]);
  if (content.byteLength > maxPluginBytes) throw new Error('generated Cline plugin exceeds the local size limit');
  return { content, moduleURL, moduleHash };
}

function verifyOwned(bytes: Buffer): ManagedContent {
  if (bytes.byteLength > maxPluginBytes) throw new Error('Cline plugin file exceeds the local size limit');
  let source: string;
  try { source = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch { throw new Error('Cline plugin file is not valid UTF-8; refusing to change it'); }
  const newline = source.indexOf('\n');
  if (newline < 0) throw new Error('Cline plugin file is not marked as Threadforge-owned');
  const markerLine = source.slice(0, newline).replace(/\r$/, '');
  const match = new RegExp(`^${escapeRegExp(managedMarker)}([a-f0-9]{64})$`).exec(markerLine);
  if (!match) throw new Error('refusing to overwrite or remove an unmanaged Cline plugin');
  const body = source.slice(newline + 1);
  if (sha256(Buffer.from(body, 'utf8')) !== match[1]) throw new Error('Threadforge Cline plugin was edited; refusing to change it');
  const lines = body.split(/\r?\n/);
  if (lines.length !== 5 || lines[0] !== ownerComment || !lines[1]?.startsWith(moduleComment) ||
      !/^[a-f0-9]{64}$/.test(lines[1].slice(moduleComment.length)) || !lines[2]?.startsWith('import plugin from ') ||
      !lines[2]?.endsWith(';') || lines[3] !== 'export default plugin;' || lines[4] !== '') {
    throw new Error('Threadforge Cline plugin wrapper has an unsupported shape');
  }
  let moduleURL: URL;
  try {
    const literal = lines[2].slice('import plugin from '.length, -1);
    const specifier: unknown = JSON.parse(literal);
    if (typeof specifier !== 'string') throw new Error('invalid import');
    moduleURL = new URL(specifier);
  } catch { throw new Error('Threadforge Cline plugin module reference is invalid'); }
  if (moduleURL.protocol !== 'file:' || moduleURL.search || moduleURL.hash || moduleURL.username || moduleURL.password) {
    throw new Error('Threadforge Cline plugin module reference is not a local file URL');
  }
  const moduleHash = lines[1].slice(moduleComment.length);
  return { content: bytes, moduleURL: moduleURL.href, moduleHash };
}

async function resolvePaths(options: ClinePluginOptions, create: boolean): Promise<Paths | undefined> {
  const scope = validateScope(options.scope);
  let rootInput: string;
  let components: string[];
  if (scope === 'project') {
    if (options.clineDir !== undefined) throw new Error('--cline-dir is only valid with --scope user');
    if (!options.projectRoot || !path.isAbsolute(options.projectRoot)) throw new Error('--scope project requires an absolute --project-root');
    rootInput = path.resolve(options.projectRoot);
    components = ['.cline', 'plugins'];
  } else {
    if (options.projectRoot !== undefined) throw new Error('--project-root is only valid with --scope project');
    rootInput = options.clineDir ?? (process.env.CLINE_HOME?.trim() || path.join(os.homedir(), '.cline'));
    if (!path.isAbsolute(rootInput)) throw new Error('CLINE_HOME or --cline-dir must be an absolute path');
    rootInput = path.resolve(rootInput);
    components = ['plugins'];
  }
  const root = await walkDirectory(rootInput, create, scope === 'user' ? 0o700 : 0o755);
  if (!root) return undefined;
  let directory = root;
  for (const component of components) {
    directory = path.join(directory, component);
    const next = await walkDirectory(directory, create, scope === 'user' ? 0o700 : 0o755);
    if (!next) return undefined;
    directory = next;
  }
  const plugin = path.join(directory, pluginName);
  const existing = await lstat(plugin).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (existing && (!existing.isFile() || existing.isSymbolicLink())) throw new Error('Cline Threadforge plugin target must be a regular file, not a symlink');
  if (existing && existing.size > maxPluginBytes) throw new Error('Cline plugin file exceeds the local size limit');
  return { scope, root, directory, plugin };
}

async function walkDirectory(input: string, create: boolean, mode: number): Promise<string | undefined> {
  if (!path.isAbsolute(input)) throw new Error('Cline plugin directories must be absolute');
  const absolute = path.resolve(input);
  const parsed = path.parse(absolute);
  let current = parsed.root;
  for (const component of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    let info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info && create) {
      await mkdir(current, { mode }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw error; });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Cline plugin path must contain only real directories');
  }
  const canonical = await realpath(absolute).catch(() => '');
  if (!canonical || canonical !== absolute) throw new Error('Cline plugin path must not traverse symbolic links');
  return canonical;
}

async function readOptionalPlugin(file: string): Promise<Snapshot | undefined> {
  const info = await lstat(file).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return undefined;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Cline plugin must be a regular file, not a symlink');
  if (info.size > maxPluginBytes) throw new Error('Cline plugin file exceeds the local size limit');
  let handle: FileHandle | undefined;
  try {
    handle = await open(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened) || opened.size !== info.size || opened.mtimeMs !== info.mtimeMs) throw new Error('Cline plugin changed before reading');
    const bytes = await readBounded(handle, maxPluginBytes);
    const after = await handle.stat();
    const afterPath = await lstat(file).catch(() => null);
    if (bytes.byteLength !== opened.size || !sameFile(opened, after) || !sameFile(opened, afterPath) || after.size !== opened.size || after.mtimeMs !== opened.mtimeMs) {
      throw new Error('Cline plugin changed during reading');
    }
    return { bytes, stat: info };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Cline plugin')) throw error;
    throw new Error('Cline plugin could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

async function readRequiredModule(file: string): Promise<Buffer> {
  const info = await lstat(file).catch(() => null);
  if (!info?.isFile() || info.isSymbolicLink() || info.size > maxModuleBytes) throw new Error('Threadforge Cline module must be a bounded regular file');
  const bytes = await readFile(file);
  const after = await lstat(file).catch(() => null);
  if (!after?.isFile() || after.isSymbolicLink() || after.size !== info.size || after.mtimeMs !== info.mtimeMs || bytes.byteLength !== info.size) {
    throw new Error('Threadforge Cline module changed while preparing installation');
  }
  return bytes;
}

async function readOptionalModule(moduleURL: string): Promise<Buffer | undefined> {
  try { return await readRequiredModule(fileURLToPath(moduleURL)); }
  catch { return undefined; }
}

async function writeExclusive(file: string, bytes: Buffer, mode: number): Promise<void> {
  let handle: FileHandle | undefined;
  try {
    handle = await open(file, 'wx', mode);
    await handle.writeFile(bytes);
    await handle.sync();
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Cline plugin appeared during installation; inspect it before retrying');
    throw new Error('unable to write the Threadforge Cline plugin');
  } finally { await handle?.close().catch(() => {}); }
}

async function replaceAtomically(file: string, snapshot: Snapshot, desired: Buffer, scope: ClinePluginScope): Promise<void> {
  const temp = `${file}.${randomUUID()}.tmp`;
  let handle: FileHandle | undefined;
  try {
    handle = await open(temp, 'wx', scope === 'user' ? 0o600 : 0o644);
    await handle.writeFile(desired);
    await handle.sync();
  } catch {
    await unlink(temp).catch(() => {});
    throw new Error('unable to stage the Threadforge Cline plugin update');
  } finally { await handle?.close().catch(() => {}); }
  try {
    await assertUnchanged(file, snapshot);
    await rename(temp, file);
  } catch (error) {
    await unlink(temp).catch(() => {});
    throw error;
  }
}

async function assertUnchanged(file: string, snapshot: Snapshot): Promise<void> {
  const current = await lstat(file).catch(() => null);
  if (!current?.isFile() || current.isSymbolicLink() || !sameFile(snapshot.stat, current) ||
      current.size !== snapshot.stat.size || current.mtimeMs !== snapshot.stat.mtimeMs) throw new Error('Cline plugin changed before the requested operation');
}

async function withLock<T>(paths: Paths, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(paths.directory, lockName);
  let lock: FileHandle | undefined;
  try { lock = await open(lockPath, 'wx', 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Cline plugin is locked by another writer or a stale lock');
    throw new Error('unable to lock the Cline plugin directory');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(lockPath).catch(() => {}); }
}

async function readBounded(handle: FileHandle, maxBytes: number): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  let position = 0;
  while (true) {
    const remaining = maxBytes + 1 - total;
    if (remaining <= 0) throw new Error('Cline plugin size limit exceeded');
    const buffer = Buffer.allocUnsafe(Math.min(64 << 10, remaining));
    const { bytesRead } = await handle.read(buffer, 0, buffer.byteLength, position);
    if (bytesRead === 0) break;
    total += bytesRead;
    if (total > maxBytes) throw new Error('Cline plugin size limit exceeded');
    chunks.push(buffer.subarray(0, bytesRead));
    position += bytesRead;
  }
  return Buffer.concat(chunks, total);
}

function validateScope(scope: ClinePluginScope | undefined): ClinePluginScope {
  const value = scope ?? 'user';
  if (value !== 'user' && value !== 'project') throw new Error('Cline plugin scope must be user or project');
  return value;
}
function result(paths: Paths, change: ClinePluginChange | ClinePluginState, moduleHash?: string): ClinePluginResult {
  return { schema_version: 'threadforge.cline-plugin-result.v1', scope: paths.scope, result: change, plugin_path: paths.plugin, ...(moduleHash ? { module_sha256: moduleHash } : {}) };
}
function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean { return right !== null && left.dev === right.dev && left.ino === right.ino; }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && (error as { code?: unknown }).code === code; }
function escapeRegExp(value: string): string { return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); }
