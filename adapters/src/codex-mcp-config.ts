import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, realpath, rename, unlink } from 'node:fs/promises';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { managedMcpArgument } from './mcp-config.ts';

const maxConfigBytes = 8 << 20;
const beginPrefix = '# >>> THREADFORGE CODEX MCP BEGIN v1';
const endMarker = '# <<< THREADFORGE CODEX MCP END v1';
const serverName = 'threadforge';

type FileIdentity = { dev: number; ino: number; mode: number; size: number; mtimeMs: number };
type ConfigSnapshot = { content: string; existed: boolean; identity?: FileIdentity };
type ManagedBlock = { start: number; end: number; body: string; eol: string; prefixAdded: boolean; createdConfig: boolean };
type Paths = { config: string; lock: string };
type TomlStringState = { multiline?: string };
export type CodexMcpChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Adds one hash-owned project-scoped stdio server to `.codex/config.toml`. */
export async function installCodexProjectMcpServer(projectRoot: string, command: string, args: string[]): Promise<CodexMcpChange> {
  const requestedBody = await buildManagedBody(projectRoot, command, args, '\n');
  const paths = await configPaths(projectRoot, true);
  return withLock(paths.lock, async () => {
    const snapshot = await readConfig(paths.config);
    const managed = findManagedBlock(snapshot.content);
    const outside = managed ? snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end) : snapshot.content;
    if (hasUnmanagedThreadforgeServer(outside)) throw new Error('Codex MCP server "threadforge" already exists outside the Threadforge-owned block');
    const eol = managed?.eol ?? detectLineEnding(snapshot.content);
    const body = requestedBody.replaceAll('\n', eol);
    if (managed) {
      if (managed.body === body) return 'unchanged';
      throw new Error('Threadforge Codex MCP config already exists with different settings; use update');
    }
    const prefixAdded = snapshot.content.length > 0 && !/(?:\r\n|\n)$/.test(snapshot.content);
    const block = renderManagedBlock(body, eol, prefixAdded, !snapshot.existed);
    const separator = prefixAdded ? eol : '';
    await atomicWrite(paths.config, `${snapshot.content}${separator}${block}${eol}`, snapshot);
    return 'installed';
  });
}

/** Updates only an unchanged Threadforge-owned block; unowned config is never overwritten. */
export async function updateCodexProjectMcpServer(projectRoot: string, command: string, args: string[]): Promise<CodexMcpChange> {
  const paths = await configPaths(projectRoot, false);
  if (!paths) throw new Error('project Codex config is absent; install it before updating');
  const requestedBody = await buildManagedBody(projectRoot, command, args, '\n');
  return withLock(paths.lock, async () => {
    const snapshot = await readConfig(paths.config);
    const managed = findManagedBlock(snapshot.content);
    if (!managed) throw new Error('Threadforge Codex MCP config is absent; refusing to create it during update');
    const outside = snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end);
    if (hasUnmanagedThreadforgeServer(outside)) throw new Error('an unmanaged Codex MCP server named threadforge conflicts with the managed block');
    const nextBody = requestedBody.replaceAll('\n', managed.eol);
    if (managed.body === nextBody) return 'unchanged';
    const replacement = renderManagedBlock(nextBody, managed.eol, managed.prefixAdded, managed.createdConfig);
    await atomicWrite(paths.config, snapshot.content.slice(0, managed.start) + replacement + snapshot.content.slice(managed.end), snapshot);
    return 'updated';
  });
}

/** Removes only the unchanged Threadforge-owned project MCP block. */
export async function removeCodexProjectMcpServer(projectRoot: string): Promise<CodexMcpChange> {
  const paths = await configPaths(projectRoot, false);
  if (!paths) return 'absent';
  return withLock(paths.lock, async () => {
    const snapshot = await readConfig(paths.config);
    const managed = findManagedBlock(snapshot.content);
    if (!managed) return 'absent';
    const outside = snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end);
    if (hasUnmanagedThreadforgeServer(outside)) throw new Error('an unmanaged Codex MCP server named threadforge conflicts with the managed block');
    let start = managed.start;
    if (managed.prefixAdded && start >= managed.eol.length && snapshot.content.slice(start - managed.eol.length, start) === managed.eol) {
      start -= managed.eol.length;
    }
    let end = managed.end;
    const suffix = snapshot.content.slice(end);
    const separator = suffix.startsWith('\r\n') ? '\r\n' : suffix.startsWith('\n') ? '\n' : '';
    const beforeBlock = snapshot.content.slice(0, start);
    if (separator && (suffix.length === separator.length || (!managed.prefixAdded && beforeBlock.endsWith(separator)))) end += separator.length;
    const next = snapshot.content.slice(0, start) + snapshot.content.slice(end);
    if (managed.createdConfig && next.length === 0) {
      await unlinkIfUnchanged(paths.config, snapshot);
      return 'removed';
    }
    await atomicWrite(paths.config, next, snapshot);
    return 'removed';
  });
}

async function buildManagedBody(projectRoot: string, command: string, args: string[], eol: string): Promise<string> {
  const canonicalRoot = await validateProjectRoot(projectRoot);
  validateCommand(command);
  const normalizedArgs = await validateArgs(args, canonicalRoot);
  return buildManagedBodyText(command, normalizedArgs, eol);
}

function buildManagedBodyText(command: string, args: string[], eol: string): string {
  return [
    '[mcp_servers.threadforge]',
    `command = ${tomlString(command)}`,
    `args = [${args.map(tomlString).join(', ')}]`,
  ].join(eol);
}

async function validateProjectRoot(projectRoot: string): Promise<string> {
  if (!path.isAbsolute(projectRoot)) throw new Error('project root must be an absolute path');
  const absolute = path.resolve(projectRoot);
  const info = await lstat(absolute).catch(() => null);
  if (!info?.isDirectory() || info.isSymbolicLink()) throw new Error('project root must be a real directory, not a symbolic link');
  return realpath(absolute).catch(() => { throw new Error('project root could not be resolved'); });
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 2048 || /[\r\n\0]/.test(command) || looksSecretLike(command)) {
    throw new Error('Codex MCP command is invalid, too long, or appears to contain a credential');
  }
}

async function validateArgs(args: string[], canonicalRoot: string): Promise<string[]> {
  if (!Array.isArray(args) || args.length < 5 || args.length > 128 ||
      args.some((arg) => typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg) || looksSecretLike(arg))) {
    throw new Error('Codex MCP arguments must be bounded, credential-free strings');
  }
  if (args[0] !== 'mcp' || args[1] !== 'serve' || args.filter((arg) => arg === managedMcpArgument).length !== 1) {
    throw new Error(`Codex MCP command must begin with mcp serve and include ${managedMcpArgument}`);
  }
  const rootIndexes = args.flatMap((arg, index) => arg === '--project-root' ? [index] : []);
  if (rootIndexes.length !== 1 || args.some((arg) => arg.startsWith('--project-root='))) {
    throw new Error('Codex MCP command must contain exactly one --project-root <same-project-root>');
  }
  const root = args[rootIndexes[0] + 1];
  if (!root || root.startsWith('--') || !path.isAbsolute(root)) throw new Error('Codex MCP command requires an absolute --project-root');
  const normalized = await realpath(root).catch(() => { throw new Error('Codex MCP --project-root must exist and match the selected project root'); });
  const same = process.platform === 'win32'
    ? normalized.toLowerCase() === canonicalRoot.toLowerCase()
    : normalized === canonicalRoot;
  if (!same) throw new Error('Codex MCP --project-root must match the selected project root');
  const normalizedArgs = [...args];
  normalizedArgs[rootIndexes[0] + 1] = canonicalRoot;
  return normalizedArgs;
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|id[_ -]?token|token|password|secret|authorization|cookie|private[_ -]?key)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value);
}

function tomlString(value: string): string {
  return JSON.stringify(value);
}

async function configPaths(projectRoot: string, create: true): Promise<Paths>;
async function configPaths(projectRoot: string, create: false): Promise<Paths | undefined>;
async function configPaths(projectRoot: string, create: boolean): Promise<Paths | undefined> {
  const root = await validateProjectRoot(projectRoot);
  const codexDir = path.join(root, '.codex');
  let dir = await lstat(codexDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect project .codex directory');
  });
  if (!dir) {
    if (!create) return undefined;
    try { await mkdir(codexDir, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create project .codex directory'); }
    dir = await lstat(codexDir).catch(() => null);
  }
  if (!dir?.isDirectory() || dir.isSymbolicLink()) throw new Error('project .codex path must be a real directory, not a symbolic link');
  const canonicalDir = await realpath(codexDir).catch(() => { throw new Error('project .codex directory could not be resolved'); });
  if (!isWithin(root, canonicalDir)) throw new Error('project .codex directory must remain within the project root');
  return {
    config: path.join(canonicalDir, 'config.toml'),
    lock: path.join(canonicalDir, '.threadforge-mcp-config.lock'),
  };
}

async function withLock<T>(lockPath: string, action: () => Promise<T>): Promise<T> {
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('project Codex MCP config is locked by another writer or a stale lock');
    throw new Error('unable to lock project Codex MCP config');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(filePath: string): Promise<ConfigSnapshot> {
  const initial = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect project Codex config.toml');
  });
  if (!initial) return { content: '', existed: false };
  if (!initial.isFile() || initial.isSymbolicLink()) throw new Error('project Codex config.toml must be a regular, non-symlink file');
  if (initial.size > maxConfigBytes) throw new Error('project Codex config.toml exceeds the local size limit');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(initial, opened) || initial.size !== opened.size || initial.mtimeMs !== opened.mtimeMs) {
      throw new Error('project Codex config.toml changed before reading');
    }
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxConfigBytes) throw new Error('project Codex config.toml exceeds the local size limit');
    let content: string;
    try { content = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
    catch { throw new Error('project Codex config.toml is not valid UTF-8; no changes were made'); }
    return { content, existed: true, identity: identityOf(initial) };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('project Codex config.toml')) throw error;
    throw new Error('project Codex config.toml could not be opened safely');
  } finally {
    await handle?.close().catch(() => {});
  }
}

async function atomicWrite(filePath: string, content: string, snapshot: ConfigSnapshot): Promise<void> {
  const bytes = Buffer.from(content, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('project Codex config.toml would exceed the local size limit');
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : 0o600;
  let handle;
  try {
    await assertUnchanged(filePath, snapshot);
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(filePath, snapshot);
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update project Codex config.toml');
  }
}

async function assertUnchanged(filePath: string, snapshot: ConfigSnapshot): Promise<void> {
  const current = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (current) throw new Error('project Codex config.toml appeared during update');
    return;
  }
  if (!current || !current.isFile() || current.isSymbolicLink() || !sameFile(snapshot.identity, current) ||
      current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('project Codex config.toml changed during update');
  }
}

async function unlinkIfUnchanged(filePath: string, snapshot: ConfigSnapshot): Promise<void> {
  await assertUnchanged(filePath, snapshot);
  await unlink(filePath);
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'>): boolean {
  return left.dev === right.dev && left.ino === right.ino;
}

function identityOf(info: Stats): FileIdentity {
  return { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs };
}

function managedBodyHash(body: string): string {
  return createHash('sha256').update(body, 'utf8').digest('hex');
}

function renderManagedBlock(body: string, eol: string, prefixAdded: boolean, createdConfig: boolean): string {
  const marker = `${beginPrefix} sha256=${managedBodyHash(body)} prefix=${prefixAdded ? 1 : 0} created=${createdConfig ? 1 : 0}`;
  return `${marker}${eol}${body}${eol}${endMarker}`;
}

function findManagedBlock(content: string): ManagedBlock | undefined {
  const begins = [...content.matchAll(/^# >>> THREADFORGE CODEX MCP BEGIN v1 sha256=([0-9a-f]{64}) prefix=([01]) created=([01])(?=\r?$)/gm)];
  const ends = [...content.matchAll(/^# <<< THREADFORGE CODEX MCP END v1(?=\r?$)/gm)];
  const hasMarker = content.includes('THREADFORGE CODEX MCP BEGIN') || content.includes('THREADFORGE CODEX MCP END');
  if (begins.length === 0 && ends.length === 0 && !hasMarker) return undefined;
  if (begins.length !== 1 || ends.length !== 1) throw new Error('Threadforge Codex MCP ownership markers are malformed or duplicated');
  const begin = begins[0];
  const end = ends[0];
  const start = begin.index ?? -1;
  const markerEnd = start + begin[0].length;
  const eol = lineEndingAt(content, markerEnd);
  const bodyEnd = end.index ?? -1;
  if (start < 0 || !eol || bodyEnd <= markerEnd || bodyEnd <= 0) throw new Error('Threadforge Codex MCP block is incomplete');
  const rawBody = content.slice(markerEnd + eol.length, bodyEnd);
  if (!rawBody.endsWith(eol)) throw new Error('Threadforge Codex MCP block has invalid separators');
  const body = rawBody.slice(0, -eol.length);
  if (managedBodyHash(body) !== begin[1]) throw new Error('Threadforge Codex MCP block was edited; inspect it before retrying');
  return {
    start,
    end: bodyEnd + end[0].length,
    body,
    eol,
    prefixAdded: begin[2] === '1',
    createdConfig: begin[3] === '1',
  };
}

function lineEndingAt(content: string, index: number): string | undefined {
  if (content.startsWith('\r\n', index)) return '\r\n';
  if (content.startsWith('\n', index)) return '\n';
  return undefined;
}

function detectLineEnding(content: string): string {
  return content.includes('\r\n') ? '\r\n' : '\n';
}

function hasUnmanagedThreadforgeServer(content: string): boolean {
  let currentTable: string[] = [];
  let valueDepth = 0;
  const stringState: TomlStringState = {};
  for (const line of content.split(/\r?\n/)) {
    const code = stripTomlComment(line, stringState).trim();
    if (!code) continue;
    if (valueDepth > 0) {
      valueDepth = updateTomlValueDepth(code, valueDepth);
      continue;
    }
    if (code.startsWith('[')) {
      currentTable = parseTomlTableHeader(code);
      if (currentTable[0] === 'mcp_servers' && currentTable[1] === serverName) return true;
      continue;
    }
    const equals = findUnquotedEquals(code);
    if (equals < 0) throw new Error('Codex config contains unsupported TOML syntax; no changes were made');
    const keys = parseTomlKeyPath(code.slice(0, equals).trim());
    if (keys.length === 0) throw new Error('Codex config contains an empty TOML key; no changes were made');
    const fullPath = [...currentTable, ...keys];
    if (fullPath[0] === 'mcp_servers' && fullPath[1] === serverName) return true;
    if (currentTable.length === 0 && keys[0] === 'mcp_servers' && keys.length === 1) {
      throw new Error('Codex config uses an inline mcp_servers value; merge Threadforge manually');
    }
    valueDepth = updateTomlValueDepth(code.slice(equals + 1), 0);
  }
  if (stringState.multiline) throw new Error('Codex config contains an unterminated TOML multiline string; no changes were made');
  if (valueDepth !== 0) throw new Error('Codex config contains an unterminated TOML array or inline table; no changes were made');
  return false;
}

function parseTomlTableHeader(line: string): string[] {
  const arrayTable = line.startsWith('[[');
  const opening = arrayTable ? 2 : 1;
  const closing = arrayTable ? ']]' : ']';
  if (!line.endsWith(closing)) throw new Error('Codex config has an unsupported TOML table header; no changes were made');
  const keys = parseTomlKeyPath(line.slice(opening, -closing.length).trim());
  if (keys.length === 0) throw new Error('Codex config has an empty TOML table header; no changes were made');
  return keys;
}

function parseTomlKeyPath(value: string): string[] {
  const keys: string[] = [];
  let index = 0;
  while (index < value.length) {
    while (/\s/.test(value[index] ?? '')) index++;
    if (index >= value.length) break;
    const quote = value[index];
    let key = '';
    if (quote === '"' || quote === "'") {
      const start = index++;
      let escaped = false;
      for (; index < value.length; index++) {
        const char = value[index];
        if (quote === '"' && char === '\\' && !escaped) { escaped = true; continue; }
        if (char === quote && !escaped) break;
        escaped = false;
      }
      if (index >= value.length) throw new Error('Codex config has an unterminated quoted TOML key; no changes were made');
      const raw = value.slice(start, index + 1);
      try { key = quote === '"' ? JSON.parse(raw) as string : raw.slice(1, -1); }
      catch { throw new Error('Codex config has an unsupported quoted TOML key; no changes were made'); }
      index++;
    } else {
      const start = index;
      while (index < value.length && !/[.\s]/.test(value[index])) index++;
      key = value.slice(start, index);
      if (!/^[A-Za-z0-9_-]+$/.test(key)) throw new Error('Codex config has an unsupported bare TOML key; no changes were made');
    }
    keys.push(key);
    while (/\s/.test(value[index] ?? '')) index++;
    if (index === value.length) break;
    if (value[index] !== '.') throw new Error('Codex config has an unsupported dotted TOML key; no changes were made');
    index++;
  }
  return keys;
}

function findUnquotedEquals(value: string): number {
  let quote = '';
  let escaped = false;
  for (let index = 0; index < value.length; index++) {
    const char = value[index];
    if (quote) {
      if (quote === '"' && char === '\\' && !escaped) { escaped = true; continue; }
      if (char === quote && !escaped) quote = '';
      escaped = false;
      continue;
    }
    if (char === '"' || char === "'") { quote = char; continue; }
    if (char === '=') return index;
  }
  return -1;
}

function stripTomlComment(line: string, state: TomlStringState): string {
  let output = '';
  let index = 0;
  if (state.multiline) {
    const close = findMultilineEnd(line, state.multiline, 0);
    if (close < 0) return '';
    state.multiline = undefined;
    index = close + 3;
  }
  for (; index < line.length;) {
    const char = line[index];
    if (char === '#') break;
    if ((char === '"' || char === "'") && line.startsWith(char.repeat(3), index)) {
      const marker = char.repeat(3);
      const close = findMultilineEnd(line, marker, index + 3);
      if (close < 0) {
        state.multiline = marker;
        break;
      }
      output += '""';
      index = close + 3;
      continue;
    }
    if (char === '"' || char === "'") {
      const start = index++;
      let escaped = false;
      for (; index < line.length; index++) {
        const current = line[index];
        if (char === '"' && current === '\\' && !escaped) { escaped = true; continue; }
        if (current === char && !escaped) break;
        escaped = false;
      }
      if (index >= line.length) throw new Error('Codex config has an unterminated TOML string; no changes were made');
      output += line.slice(start, index + 1);
      index++;
      continue;
    }
    output += char;
    index++;
  }
  return output;
}

function findMultilineEnd(line: string, marker: string, from: number): number {
  for (let index = line.indexOf(marker, from); index >= 0; index = line.indexOf(marker, index + marker.length)) {
    if (marker === "'''" || !isEscaped(line, index)) return index;
  }
  return -1;
}

function isEscaped(value: string, index: number): boolean {
  let slashes = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor--) slashes++;
  return slashes % 2 === 1;
}

function updateTomlValueDepth(value: string, initial: number): number {
  let depth = initial;
  let quote = '';
  let escaped = false;
  for (let index = 0; index < value.length; index++) {
    const char = value[index];
    if (quote) {
      if (quote === '"' && char === '\\' && !escaped) { escaped = true; continue; }
      if (char === quote && !escaped) quote = '';
      escaped = false;
      continue;
    }
    if ((char === '"' || char === "'") && value.startsWith(char.repeat(3), index)) {
      const marker = char.repeat(3);
      const close = findMultilineEnd(value, marker, index + 3);
      if (close < 0) return depth;
      index = close + 2;
      continue;
    }
    if (char === '"' || char === "'") { quote = char; continue; }
    if (char === '[' || char === '{') depth++;
    else if (char === ']' || char === '}') depth--;
    if (depth < 0) throw new Error('Codex config has an invalid TOML array or inline table; no changes were made');
  }
  return depth;
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
