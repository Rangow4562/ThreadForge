import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';

const maxConfigBytes = 8 << 20;
const beginPrefix = '# >>> THREADFORGE CODEX OTEL BEGIN v1';
const endMarker = '# <<< THREADFORGE CODEX OTEL END v1';

type FileIdentity = {
  dev: number;
  ino: number;
  mode: number;
  size: number;
  mtimeMs: number;
};

type ConfigSnapshot = {
  content: string;
  existed: boolean;
  identity?: FileIdentity;
};

type ManagedBlock = {
  start: number;
  end: number;
  body: string;
  eol: string;
  prefixAdded: boolean;
  createdConfig: boolean;
};

export type CodexTelemetryChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

/** Installs only a hash-owned local OTLP block in the selected Codex user home. */
export async function installCodexTelemetry(options: { codexHome?: string; endpoint?: string } = {}): Promise<CodexTelemetryChange> {
  const endpoint = normalizeLoopbackOtlpEndpoint(options.endpoint);
  const paths = await configPaths(options.codexHome ?? process.env.CODEX_HOME ?? path.join(os.homedir(), '.codex'), true);
  return withLock(paths.lock, async () => {
    const snapshot = await readConfig(paths.config);
    const managed = findManagedBlock(snapshot.content);
    const outside = managed
      ? snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end)
      : snapshot.content;
    if (hasUnmanagedOtelConfig(outside)) {
      throw new Error('Codex config already contains an unmanaged otel section or key; merge it manually before installing Threadforge');
    }

    const eol = managed?.eol ?? detectLineEnding(snapshot.content);
    const body = managedConfigBody(eol, endpoint);
    if (managed?.body === body) return 'unchanged';
    if (managed) {
      const block = renderManagedBlock(body, eol, managed.prefixAdded, managed.createdConfig);
      const next = snapshot.content.slice(0, managed.start) + block + snapshot.content.slice(managed.end);
      await atomicWrite(paths.config, next, snapshot);
      return 'updated';
    }

    const prefixAdded = snapshot.content.length > 0 && !/(?:\r\n|\n)$/.test(snapshot.content);
    const separator = prefixAdded ? eol : '';
    const block = renderManagedBlock(body, eol, prefixAdded, !snapshot.existed);
    await atomicWrite(paths.config, `${snapshot.content}${separator}${block}${eol}`, snapshot);
    return 'installed';
  });
}

/** Removes only the unchanged Threadforge-owned TOML block and leaves other bytes untouched. */
export async function removeCodexTelemetry(options: { codexHome?: string } = {}): Promise<CodexTelemetryChange> {
  const paths = await configPaths(options.codexHome ?? process.env.CODEX_HOME ?? path.join(os.homedir(), '.codex'), false);
  if (!paths) return 'absent';
  return withLock(paths.lock, async () => {
    const snapshot = await readConfig(paths.config);
    const managed = findManagedBlock(snapshot.content);
    if (!managed) return 'absent';

    let start = managed.start;
    if (managed.prefixAdded && start >= managed.eol.length &&
        snapshot.content.slice(start - managed.eol.length, start) === managed.eol) {
      start -= managed.eol.length;
    }
    let end = managed.end;
    const suffix = snapshot.content.slice(end);
    const separator = suffix.startsWith('\r\n') ? '\r\n' : suffix.startsWith('\n') ? '\n' : '';
    const beforeBlock = snapshot.content.slice(0, start);
    if (separator && (suffix.length === separator.length || (!managed.prefixAdded && beforeBlock.endsWith(separator)))) {
      end += separator.length;
    }
    const next = snapshot.content.slice(0, start) + snapshot.content.slice(end);
    if (managed.createdConfig && next.length === 0) {
      await unlinkIfUnchanged(paths.config, snapshot);
      return 'removed';
    }
    await atomicWrite(paths.config, next, snapshot);
    return 'removed';
  });
}

async function configPaths(codexHome: string, create: true): Promise<{ config: string; lock: string }>;
async function configPaths(codexHome: string, create: false): Promise<{ config: string; lock: string } | undefined>;
async function configPaths(codexHome: string, create: boolean): Promise<{ config: string; lock: string } | undefined> {
  if (!path.isAbsolute(codexHome)) throw new Error('CODEX_HOME must be an absolute path');
  const absolute = path.resolve(codexHome);
  let info = await lstat(absolute).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect CODEX_HOME');
  });
  if (!info) {
    if (!create) return undefined;
    const parent = await realpath(path.dirname(absolute)).catch(() => { throw new Error('CODEX_HOME parent must exist and be accessible'); });
    if (!path.isAbsolute(parent)) throw new Error('CODEX_HOME parent is invalid');
    try { await mkdir(absolute, { mode: 0o700 }); }
    catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create CODEX_HOME directory');
    }
    info = await lstat(absolute).catch(() => null);
  }
  if (!info || !info.isDirectory() || info.isSymbolicLink()) {
    throw new Error('CODEX_HOME must be a real directory, not a symbolic link');
  }
  const canonical = await realpath(absolute).catch(() => { throw new Error('CODEX_HOME could not be resolved'); });
  const canonicalInfo = await lstat(canonical).catch(() => null);
  if (!canonicalInfo || !canonicalInfo.isDirectory() || canonicalInfo.isSymbolicLink()) {
    throw new Error('CODEX_HOME must resolve to a real directory');
  }
  return {
    config: path.join(canonical, 'config.toml'),
    lock: path.join(canonical, '.threadforge-codex-telemetry.lock'),
  };
}

async function withLock<T>(lockPath: string, action: () => Promise<T>): Promise<T> {
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Codex telemetry config is locked by another writer or a stale lock');
    throw new Error('unable to lock Codex telemetry config');
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
    throw new Error('unable to inspect Codex config.toml');
  });
  if (!initial) return { content: '', existed: false };
  if (!initial.isFile() || initial.isSymbolicLink()) throw new Error('Codex config.toml must be a regular, non-symlink file');
  if (initial.size > maxConfigBytes) throw new Error('Codex config.toml exceeds the local size limit');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(initial, opened)) throw new Error('Codex config.toml changed before reading');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxConfigBytes) throw new Error('Codex config.toml exceeds the local size limit');
    let content: string;
    try { content = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
    catch { throw new Error('Codex config.toml is not valid UTF-8; no changes were made'); }
    return { content, existed: true, identity: identityOf(initial) };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Codex config.toml')) throw error;
    throw new Error('Codex config.toml could not be opened safely');
  } finally {
    await handle?.close().catch(() => {});
  }
}

async function atomicWrite(filePath: string, content: string, snapshot: ConfigSnapshot): Promise<void> {
  const bytes = Buffer.from(content, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Codex config.toml would exceed the local size limit');
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
    throw new Error('unable to atomically update Codex config.toml');
  }
}

async function assertUnchanged(filePath: string, snapshot: ConfigSnapshot): Promise<void> {
  const current = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (current) throw new Error('Codex config.toml appeared during update');
    return;
  }
  if (!current || !current.isFile() || current.isSymbolicLink() || !sameFile(snapshot.identity, current) ||
      current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Codex config.toml changed during update');
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

function managedConfigBody(eol: string, endpoint: string): string {
  return [
    '[otel]',
    'environment = "local"',
    'metrics_exporter = "none"',
    'log_user_prompt = false',
    '',
    '[otel.exporter."otlp-http"]',
    `endpoint = "${endpoint}/v1/logs"`,
    'protocol = "json"',
    '',
    '[otel.trace_exporter."otlp-http"]',
    `endpoint = "${endpoint}/v1/traces"`,
    'protocol = "json"',
  ].join(eol);
}

function normalizeLoopbackOtlpEndpoint(value: string | undefined): string {
  const candidate = value ?? 'http://127.0.0.1:4318';
  let parsed: URL;
  try { parsed = new URL(candidate); }
  catch { throw new Error('Codex OTLP endpoint must be a loopback HTTP origin'); }
  const hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '');
  const ipv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(hostname) &&
    hostname.split('.').every((part) => Number(part) <= 255);
  const loopback = hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1' || ipv4Loopback;
  if (parsed.protocol !== 'http:' || !loopback || parsed.username || parsed.password ||
      (parsed.pathname !== '' && parsed.pathname !== '/') || parsed.search || parsed.hash) {
    throw new Error('Codex OTLP endpoint must be a loopback HTTP origin without credentials, path, query, or fragment');
  }
  return parsed.origin;
}

function renderManagedBlock(body: string, eol: string, prefixAdded: boolean, createdConfig: boolean): string {
  const digest = createHash('sha256').update(body, 'utf8').digest('hex');
  const marker = `${beginPrefix} sha256=${digest} prefix=${prefixAdded ? 1 : 0} created=${createdConfig ? 1 : 0}`;
  return `${marker}${eol}${body}${eol}${endMarker}`;
}

function findManagedBlock(content: string): ManagedBlock | undefined {
  const begins = [...content.matchAll(/^# >>> THREADFORGE CODEX OTEL BEGIN v1 sha256=([0-9a-f]{64}) prefix=([01]) created=([01])(?=\r?$)/gm)];
  const ends = [...content.matchAll(/^# <<< THREADFORGE CODEX OTEL END v1(?=\r?$)/gm)];
  const containsMarkerText = content.includes('THREADFORGE CODEX OTEL BEGIN') || content.includes('THREADFORGE CODEX OTEL END');
  if (begins.length === 0 && ends.length === 0 && !containsMarkerText) return undefined;
  if (begins.length !== 1 || ends.length !== 1) throw new Error('Threadforge Codex telemetry markers are malformed or duplicated');
  const begin = begins[0];
  const end = ends[0];
  const start = begin.index ?? -1;
  const markerEnd = start + begin[0].length;
  const eol = lineEndingAt(content, markerEnd);
  if (start < 0 || !eol || (end.index ?? -1) <= markerEnd) throw new Error('Threadforge Codex telemetry block is incomplete');
  const bodyStart = markerEnd + eol.length;
  const bodyEnd = end.index ?? -1;
  const rawBody = content.slice(bodyStart, bodyEnd);
  if (!rawBody.endsWith(eol)) throw new Error('Threadforge Codex telemetry block has invalid separators');
  const body = rawBody.slice(0, -eol.length);
  const digest = createHash('sha256').update(body, 'utf8').digest('hex');
  if (digest !== begin[1]) throw new Error('Threadforge Codex telemetry block was edited; inspect it before retrying');
  return {
    start,
    end: bodyEnd + end[0].length,
    body,
    eol,
    prefixAdded: begin[2] === '1',
    createdConfig: begin[3] === '1',
  };
}

function hasUnmanagedOtelConfig(content: string): boolean {
  let inRoot = true;
  const strings: { multiline?: string } = {};
  for (const line of content.split(/\r?\n/)) {
    const code = stripTomlComment(line, strings).trim();
    if (!code) continue;
    if (code.startsWith('[')) {
      const double = code.startsWith('[[');
      const openLength = double ? 2 : 1;
      const close = double ? ']]' : ']';
      if (!code.endsWith(close)) throw new Error('Codex config has an unsupported TOML table header; no changes were made');
      const tablePath = code.slice(openLength, -openLength).trim();
      if (!tablePath) throw new Error('Codex config has an empty TOML table header; no changes were made');
      inRoot = false;
      if (firstKeyIsOtel(tablePath)) return true;
      continue;
    }
    if (inRoot) {
      const equals = findUnquotedEquals(code);
      if (equals >= 0 && firstKeyIsOtel(code.slice(0, equals).trim())) return true;
    }
  }
  if (strings.multiline) throw new Error('Codex config has an unterminated TOML multiline string; no changes were made');
  return false;
}

function stripTomlComment(line: string, state: { multiline?: string }): string {
  if (state.multiline) {
    const close = line.indexOf(state.multiline);
    if (close < 0) return '';
    state.multiline = undefined;
    return '';
  }
  let result = '';
  for (let index = 0; index < line.length;) {
    const char = line[index];
    if (char === '#') return result;
    if ((char === '"' || char === "'") && line.startsWith(char.repeat(3), index)) {
      const marker = char.repeat(3);
      const close = line.indexOf(marker, index + 3);
      if (close < 0) {
        state.multiline = marker;
        return result;
      }
      result += line.slice(index, close + 3);
      index = close + 3;
      continue;
    }
    if (char === '"' || char === "'") {
      let end = index + 1;
      let escaped = false;
      for (; end < line.length; end++) {
        const current = line[end];
        if (char === '"' && current === '\\' && !escaped) { escaped = true; continue; }
        if (current === char && !escaped) break;
        escaped = false;
      }
      if (end >= line.length) throw new Error('Codex config has an unterminated TOML string; no changes were made');
      result += line.slice(index, end + 1);
      index = end + 1;
      continue;
    }
    result += char;
    index++;
  }
  return result;
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

function firstKeyIsOtel(value: string): boolean {
  return /^(?:otel|"otel"|'otel')(?=\s*(?:\.|$))/.test(value.trim());
}

function detectLineEnding(content: string): string {
  return content.includes('\r\n') ? '\r\n' : '\n';
}

function lineEndingAt(content: string, index: number): string | undefined {
  if (content.startsWith('\r\n', index)) return '\r\n';
  if (content.startsWith('\n', index)) return '\n';
  return undefined;
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
