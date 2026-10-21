import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isSameRealPathWithoutLinks, isWithinRealPaths } from './path-safety.ts';
import { constants as zstdConstants, createZstdDecompress } from 'node:zlib';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const stateFileName = 'dsh-sessions.v1.json';
const maxStateBytes = 8 << 20;
const defaultMaxSessions = 1_000;
const maxSessions = 5_000;
const maxDirectories = 20_000;
const maxFiles = 50_000;
const maxCompressedBytes = 64 << 20;
const maxUncompressedBytes = 256 << 20;
const maxTotalSourceBytes = 512 << 20;
const maxTotalDecodedBytes = 1 << 30;
const maxLineBytes = 8 << 20;
const maxLinesPerSession = 100_000;
const maxRecordsPerSession = 20_000;
const maxEventsPerSession = 10_000;
const maxEventsPerSync = 20_000;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const batchSize = 100;
const maxToolCalls = 10_000;

type JsonObject = Record<string, unknown>;
type Cursor = { last_line: number; prefix_sha256: string; started: boolean; incomplete: boolean; source_fingerprint: string };
type CursorState = { schema_version: 'threadforge.dsh-history-cursor.v1'; version: number; sessions: Record<string, Cursor> };
type SessionFile = { key: string; absolutePath: string; compressed: boolean; size: number; mtimeMs: number; fingerprint: string };
type SessionRecord = { line: number; kind: string; time?: unknown; createdAt?: unknown; data: JsonObject };
type ParsedSession = { records: SessionRecord[]; completeLines: number; prefixHashes: Map<number, string>; partialTail: boolean; malformed: number; uncompressedBytes: number };
type EventGroup = { line: number; events: ThreadforgeEvent[]; startsSession?: boolean };

export type DeepSeekHistoryOptions = {
  dshHome?: string;
  sessionsDir?: string;
  statePath?: string;
  endpoint?: string;
  fetchImpl?: typeof fetch;
  maxSessions?: number;
  workspace?: string;
  sessionId?: string;
  preview?: boolean;
  includeContent?: boolean;
};

export type DeepSeekHistoryResult = {
  schema_version: 'threadforge.dsh-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_lines: number;
  partial_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type DeepSeekHistoryStatus = {
  schema_version: 'threadforge.dsh-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  incomplete_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
};

export type DeepSeekHistoryWatchResult = { sweeps: number; complete: boolean };

/** Status exposes counts and hashes only, never native IDs, paths, or conversation text. */
export async function getDeepSeekHistoryStatus(
  options: Pick<DeepSeekHistoryOptions, 'dshHome' | 'sessionsDir' | 'statePath' | 'maxSessions' | 'workspace' | 'sessionId'> = {},
): Promise<DeepSeekHistoryStatus> {
  validateOptions(options);
  const root = await resolveSessionsDir(options);
  const statePath = resolveStatePath(options.statePath);
  await assertCursorOutsideSource(options, root, statePath);
  const listed = await listSessionFiles(root, options.maxSessions ?? defaultMaxSessions);
  const state = await readState(statePath);
  const errors: DeepSeekHistoryStatus['errors'] = [];
  let checkpointed = 0;
  let incomplete = 0;
  let sessions = 0;
  let totalSourceBytes = 0;
  let totalDecodedBytes = 0;
  let sourceLimitReached = false;
  for (const file of listed.files) {
    if (totalSourceBytes + file.size > maxTotalSourceBytes) {
      sourceLimitReached = true;
      errors.push({ source_sha256: sha256(file.key), message: 'aggregate source read limit reached; remaining sessions were not scanned' });
      break;
    }
    totalSourceBytes += file.size;
    try {
      const parsed = await readSession(file, root);
      totalDecodedBytes += parsed.uncompressedBytes;
      if (totalDecodedBytes > maxTotalDecodedBytes) {
        sourceLimitReached = true;
        errors.push({ source_sha256: sha256(file.key), message: 'aggregate expanded-size limit reached; remaining sessions were not scanned' });
        break;
      }
      const metadata = sessionMetadata(parsed.records, file);
      if (!matchesFilters(metadata, options)) continue;
      sessions++;
      const cursor = state.sessions[sha256(file.key)];
      const conflict = cursor !== undefined && cursor.last_line > 0 &&
        (parsed.completeLines < cursor.last_line || parsed.prefixHashes.get(cursor.last_line) !== cursor.prefix_sha256);
      if (conflict) errors.push({ source_sha256: sha256(file.key), message: 'session prefix changed; review before resuming' });
      else if (cursor && cursor.started && cursor.last_line >= parsed.completeLines && !cursor.incomplete && !parsed.partialTail && parsed.malformed === 0) checkpointed++;
      if (cursor?.incomplete || parsed.partialTail || parsed.malformed > 0 || conflict) incomplete++;
    } catch (error) {
      errors.push({ source_sha256: sha256(file.key), message: safeError(error) });
    }
  }
  return {
    schema_version: 'threadforge.dsh-history-status.v1', sessions,
    checkpointed_sessions: checkpointed, pending_sessions: Math.max(0, sessions - checkpointed),
    incomplete_sessions: incomplete, symlinks_skipped: listed.symlinksSkipped,
    oversized_files_skipped: listed.oversizedSkipped, sessions_truncated: listed.truncated || sourceLimitReached, errors,
  };
}

/** Reads committed DSH records, defaults to digest-only content, and sends only to loopback intake. */
export async function syncDeepSeekHistory(options: DeepSeekHistoryOptions = {}): Promise<DeepSeekHistoryResult> {
  validateOptions(options);
  const root = await resolveSessionsDir(options);
  const statePath = resolveStatePath(options.statePath);
  await assertCursorOutsideSource(options, root, statePath);
  const listed = await listSessionFiles(root, options.maxSessions ?? defaultMaxSessions);
  const result = emptyResult(options.includeContent ?? false);
  if (options.preview) result.events = [];
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.sessions_truncated = listed.truncated;
  const selected: SessionFile[] = [];
  let totalSourceBytes = 0;
  for (const file of listed.files) {
    if (totalSourceBytes + file.size > maxTotalSourceBytes) {
      result.sessions_truncated = true;
      result.errors.push({ source_sha256: sha256(file.key), message: 'source read limit reached; remaining sessions were not scanned' });
      break;
    }
    totalSourceBytes += file.size;
    selected.push(file);
  }
  result.sessions = 0;

  if (options.preview) {
    const state = await readState(statePath);
    await processSessions(selected, root, state, options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processSessions(selected, root, state, options, result, true, statePath);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated && !result.preview_truncated;
  return result;
}

/** Repeats bounded sync sweeps until aborted; each sweep reports its own incomplete/error state. */
export async function watchDeepSeekHistory(
  options: DeepSeekHistoryOptions, intervalMs: number, onResult: (result: DeepSeekHistoryResult) => void,
  signal: AbortSignal,
): Promise<DeepSeekHistoryWatchResult> {
  if (!Number.isInteger(intervalMs) || intervalMs < 5_000 || intervalMs > 7 * 24 * 60 * 60 * 1_000) {
    throw new Error('--interval must be between 5s and 168h');
  }
  if (options.preview) throw new Error('--watch cannot be combined with --print');
  let sweeps = 0;
  let complete = true;
  while (!signal.aborted) {
    const result = await syncDeepSeekHistory(options);
    sweeps++;
    complete &&= result.complete;
    onResult(result);
    if (signal.aborted) break;
    await waitForInterval(intervalMs, signal);
  }
  return { sweeps, complete };
}

function waitForInterval(intervalMs: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) { resolve(); return; }
    const done = () => { clearTimeout(timer); signal.removeEventListener('abort', done); resolve(); };
    const timer = setTimeout(done, intervalMs);
    signal.addEventListener('abort', done, { once: true });
  });
}

function emptyResult(includeContent: boolean): DeepSeekHistoryResult {
  return {
    schema_version: 'threadforge.dsh-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: DeepSeekHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  if (options.dshHome !== undefined && options.sessionsDir !== undefined) throw new Error('use either --dsh-home or --sessions-dir, not both');
  boundedInteger(options.maxSessions ?? defaultMaxSessions, 1, maxSessions, '--max-sessions');
  if (options.dshHome !== undefined && !path.isAbsolute(options.dshHome)) throw new Error('--dsh-home must be an absolute path');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveSessionsDir(options: Pick<DeepSeekHistoryOptions, 'dshHome' | 'sessionsDir'>): Promise<string> {
  const dshHome = options.dshHome ?? process.env.DSH_HOME ?? path.join(os.homedir(), '.dsh');
  if (!path.isAbsolute(dshHome)) throw new Error('DSH_HOME must be an absolute path');
  const root = path.resolve(options.sessionsDir ?? path.join(dshHome, 'sessions'));
  const info = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return root;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('DeepSeek sessions path must be a real directory');
  const canonical = await realpath(root).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, root)) throw new Error('DeepSeek sessions path must not traverse symbolic links');
  return canonical;
}

async function listSessionFiles(root: string, maximum: number): Promise<{ files: SessionFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('DeepSeek sessions path must be a real directory');
  const files: SessionFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let visitedDirs = 0;
  let visitedFiles = 0;
  let truncated = false;
  const walk = async (directory: string, depth: number): Promise<void> => {
    if (depth > 20) { truncated = true; return; }
    const entries = await readdir(directory, { withFileTypes: true }).catch(() => { throw new Error('DeepSeek session directory could not be enumerated'); });
    for (const entry of entries) {
      if (++visitedFiles > maxFiles) { truncated = true; return; }
      const candidatePath = path.join(directory, entry.name);
      const info = await lstat(candidatePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (info.isDirectory()) {
        if (++visitedDirs > maxDirectories) { truncated = true; return; }
        await walk(candidatePath, depth + 1);
        if (truncated && files.length >= maximum) return;
        continue;
      }
      if (!info.isFile() || !isSessionFileName(entry.name)) continue;
      if (info.size > maxCompressedBytes) { oversizedSkipped++; continue; }
      const canonical = await realpath(candidatePath).catch(() => '');
      if (!canonical || !isWithin(root, canonical) || !await isSameRealPathWithoutLinks(canonical, candidatePath)) { symlinksSkipped++; continue; }
      const key = path.relative(root, canonical).split(path.sep).join('/');
      files.push({ key, absolutePath: canonical, compressed: entry.name.endsWith('.zstd'), size: info.size, mtimeMs: info.mtimeMs,
        fingerprint: sha256(JSON.stringify([info.size, info.mtimeMs, info.ino, info.dev, entry.name.endsWith('.zstd')])) });
    }
  };
  await walk(root, 0);
  const bySession = new Map<string, SessionFile>();
  for (const file of files) {
    const directory = path.posix.dirname(file.key);
    const previous = bySession.get(directory);
    if (!previous || (file.compressed && !previous.compressed)) bySession.set(directory, file);
  }
  const sessions = [...bySession.values()].sort((left, right) => left.key.localeCompare(right.key));
  if (sessions.length > maximum) { sessions.length = maximum; truncated = true; }
  return { files: sessions, symlinksSkipped, oversizedSkipped, truncated };
}

function isSessionFileName(name: string): boolean {
  return /^session(?:\.v[0-9]+)?\.jsonl(?:\.zstd)?$/.test(name);
}

async function readSession(file: SessionFile, root: string): Promise<ParsedSession> {
  const before = await lstat(file.absolutePath).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxCompressedBytes || before.size !== file.size || before.mtimeMs !== file.mtimeMs) {
    throw new Error('DeepSeek session changed after enumeration');
  }
  const canonical = await realpath(file.absolutePath).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.absolutePath) || !isWithin(root, canonical)) throw new Error('DeepSeek session escaped its source directory');
  let handle;
  try {
    handle = await open(file.absolutePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened)) throw new Error('DeepSeek session changed before read');
    const bytes = await handle.readFile();
    const after = await handle.stat();
    const afterPath = await lstat(file.absolutePath).catch(() => null);
    const afterCanonical = await realpath(file.absolutePath).catch(() => '');
    if (bytes.byteLength > maxCompressedBytes || !sameFile(opened, after) || !sameFile(opened, afterPath) ||
        !afterCanonical || !await isSameRealPathWithoutLinks(afterCanonical, file.absolutePath) || !isWithin(root, afterCanonical)) throw new Error('DeepSeek session changed during read');
    const decoded = file.compressed ? await decompressZstd(bytes) : bytes;
    if (decoded.byteLength > maxUncompressedBytes) throw new Error('DeepSeek session exceeds the expanded-size limit');
    return parseSessionRecords(decoded);
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('DeepSeek')) throw error;
    throw new Error('DeepSeek session is malformed, truncated, or could not be decoded');
  } finally { await handle?.close().catch(() => {}); }
}

async function decompressZstd(bytes: Buffer): Promise<Buffer> {
  const decoder = createZstdDecompress({ params: { [zstdConstants.ZSTD_d_windowLogMax]: 27 } });
  const chunks: Buffer[] = [];
  let total = 0;
  const collected = (async () => {
    for await (const chunk of decoder) {
      const output = Buffer.from(chunk as Buffer);
      total += output.byteLength;
      if (total > maxUncompressedBytes) {
        decoder.destroy(new Error('expanded-size limit exceeded'));
        throw new Error('DeepSeek session exceeds the expanded-size limit');
      }
      chunks.push(output);
    }
  })();
  decoder.end(bytes);
  await collected;
  return Buffer.concat(chunks, total);
}

function parseSessionRecords(bytes: Buffer): ParsedSession {
  let text: string;
  try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch { return { records: [], completeLines: 0, prefixHashes: new Map(), partialTail: false, malformed: 1, uncompressedBytes: bytes.byteLength }; }
  const records: SessionRecord[] = [];
  const prefixHashes = new Map<number, string>();
  const prefix = createHash('sha256');
  let start = 0;
  let line = 0;
  let malformed = 0;
  for (let offset = 0; offset < text.length; offset++) {
    if (text.charCodeAt(offset) !== 10) continue;
    line++;
    if (line > maxLinesPerSession || Buffer.byteLength(text.slice(start, offset), 'utf8') > maxLineBytes) {
      malformed++;
      break;
    }
    const bytesForLine = Buffer.from(text.slice(start, offset + 1), 'utf8');
    prefix.update(bytesForLine);
    prefixHashes.set(line, prefix.copy().digest('hex'));
    const raw = text.slice(start, offset).trim();
    if (raw) {
      try {
        const value: unknown = JSON.parse(raw);
        if (!isRecord(value) || typeof value.type !== 'string' || !value.type.trim() || (value.data !== undefined && value.data !== null && !isRecord(value.data))) {
          throw new Error('invalid record');
        }
        records.push({ line, kind: value.type.trim(), time: value.time, createdAt: value.createdAt, data: isRecord(value.data) ? value.data : {} });
        if (records.length > maxRecordsPerSession) throw new Error('record count limit');
      } catch {
        malformed++;
        prefixHashes.delete(line);
        break;
      }
    }
    start = offset + 1;
  }
  const stopped = malformed > 0;
  const completeLines = stopped ? Math.max(0, line - 1) : line;
  const partialTail = !stopped && start < text.length;
  return { records: records.filter((record) => record.line <= completeLines), completeLines, prefixHashes, partialTail, malformed, uncompressedBytes: bytes.byteLength };
}

function sessionMetadata(records: SessionRecord[], file: SessionFile): { id: string; cwd: string } {
  for (const record of records) {
    if (record.kind !== 'session') continue;
    return { id: firstString(record.data, 'id', 'sessionId', 'session_id') ?? path.posix.basename(path.posix.dirname(file.key)),
      cwd: firstString(record.data, 'cwd', 'workingDirectory', 'working_directory') ?? '' };
  }
  return { id: path.posix.basename(path.posix.dirname(file.key)), cwd: '' };
}

function matchesFilters(metadata: { id: string; cwd: string }, options: Pick<DeepSeekHistoryOptions, 'workspace' | 'sessionId'>): boolean {
  if (options.sessionId && metadata.id !== options.sessionId) return false;
  if (!options.workspace) return true;
  if (!metadata.cwd) return false;
  return samePath(path.resolve(metadata.cwd), path.resolve(options.workspace));
}

async function processSessions(
  files: SessionFile[], root: string, state: CursorState, options: DeepSeekHistoryOptions,
  result: DeepSeekHistoryResult, write: boolean, statePath?: string,
): Promise<void> {
  let totalEvents = 0;
  let totalDecodedBytes = 0;
  for (const file of files) {
    const sourceHash = sha256(file.key);
    try {
      const parsed = await readSession(file, root);
      totalDecodedBytes += parsed.uncompressedBytes;
      if (totalDecodedBytes > maxTotalDecodedBytes) {
        result.sessions_truncated = true;
        result.errors.push({ source_sha256: sourceHash, message: 'aggregate expanded-size limit reached; remaining sessions were not scanned' });
        break;
      }
      const metadata = sessionMetadata(parsed.records, file);
      if (!matchesFilters(metadata, options)) continue;
      result.sessions++;
      result.malformed_lines += parsed.malformed;
      if (parsed.partialTail || parsed.malformed > 0) result.partial_sessions++;
      const prior = state.sessions[sourceHash] ?? emptyCursor(file.fingerprint);
      if (prior.last_line > 0 && (parsed.completeLines < prior.last_line || parsed.prefixHashes.get(prior.last_line) !== prior.prefix_sha256)) {
        result.errors.push({ source_sha256: sourceHash, message: 'session prefix changed; cursor was not advanced' });
        continue;
      }
      const groups = mapSessionRecords(file, parsed.records, prior, options.includeContent ?? false);
      const count = groups.reduce((sum, group) => sum + group.events.length, 0);
      if (count > maxEventsPerSession || totalEvents + count > maxEventsPerSync) {
        result.errors.push({ source_sha256: sourceHash, message: 'event limit reached; cursor was not advanced' });
        continue;
      }
      totalEvents += count;
      if (count > 0 || prior.source_fingerprint !== file.fingerprint) result.sessions_changed++;
      if (options.preview) {
        for (const group of groups) for (const event of group.events) {
          if (result.events!.length >= maxPreviewEvents) { result.preview_truncated = true; break; }
          result.events!.push(event);
          result.events_previewed++;
        }
        continue;
      }
      const next: Cursor = { ...prior, source_fingerprint: file.fingerprint, incomplete: parsed.partialTail || parsed.malformed > 0 };
      await deliverGroups(groups, next, parsed, options, result, async () => {
        state.sessions[sourceHash] = next;
        if (write && statePath) await writeState(statePath, state);
      });
      if (!result.errors.some((item) => item.source_sha256 === sourceHash)) {
        const hash = parsed.prefixHashes.get(parsed.completeLines);
        if (hash && parsed.completeLines >= next.last_line) {
          next.last_line = parsed.completeLines;
          next.prefix_sha256 = hash;
        }
        state.sessions[sourceHash] = next;
        if (write && statePath) await writeState(statePath, state);
      }
    } catch (error) {
      result.errors.push({ source_sha256: sourceHash, message: safeError(error) });
    }
  }
}

function emptyCursor(fingerprint: string): Cursor {
  return { last_line: 0, prefix_sha256: '', started: false, incomplete: false, source_fingerprint: fingerprint };
}

function resolveStatePath(value?: string): string {
  if (value !== undefined && !path.isAbsolute(value)) throw new Error('--state-path must be an absolute path');
  return path.resolve(value ?? path.join(os.homedir(), '.threadforge', 'state', stateFileName));
}

async function assertCursorOutsideSource(options: Pick<DeepSeekHistoryOptions, 'dshHome' | 'sessionsDir'>, sessionsDir: string, statePath: string): Promise<void> {
  if (await isWithinRealPaths(sessionsDir, statePath) !== false) throw new Error('DeepSeek history cursor must be stored outside the native sessions directory');
  if (options.sessionsDir === undefined) {
    const dshHome = path.resolve(options.dshHome ?? process.env.DSH_HOME ?? path.join(os.homedir(), '.dsh'));
    if (await isWithinRealPaths(dshHome, statePath) !== false) throw new Error('DeepSeek history cursor must be stored outside the DSH_HOME source tree');
  }
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await ensurePrivateDirectory(path.dirname(statePath));
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('DeepSeek history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock DeepSeek history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.dsh-history-cursor.v1', version: stateVersion, sessions: {} };
  await assertSafeParentDirectories(path.dirname(statePath));
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('DeepSeek history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('DeepSeek history cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxStateBytes) throw new Error('DeepSeek history cursor exceeds the size limit');
    const value: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    if (!isRecord(value) || value.schema_version !== empty.schema_version || value.version !== stateVersion || !isRecord(value.sessions)) {
      throw new Error('DeepSeek history cursor has an unsupported schema');
    }
    const sessions: Record<string, Cursor> = {};
    for (const [key, entry] of Object.entries(value.sessions)) {
      if (!/^[a-f0-9]{64}$/.test(key) || !isRecord(entry) || !Number.isSafeInteger(entry.last_line) || (entry.last_line as number) < 0 ||
          typeof entry.prefix_sha256 !== 'string' || (entry.prefix_sha256 !== '' && !/^[a-f0-9]{64}$/.test(entry.prefix_sha256)) ||
          typeof entry.started !== 'boolean' || typeof entry.incomplete !== 'boolean' ||
          typeof entry.source_fingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(entry.source_fingerprint)) {
        throw new Error('DeepSeek history cursor contains invalid checkpoints');
      }
      sessions[key] = { last_line: entry.last_line as number, prefix_sha256: entry.prefix_sha256, started: entry.started,
        incomplete: entry.incomplete, source_fingerprint: entry.source_fingerprint };
    }
    return { schema_version: empty.schema_version, version: stateVersion, sessions };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('DeepSeek history cursor')) throw error;
    throw new Error('DeepSeek history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  await ensurePrivateDirectory(path.dirname(statePath));
  const bytes = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxStateBytes) throw new Error('DeepSeek history cursor exceeds the size limit');
  const temp = `${statePath}.${randomUUID()}.tmp`;
  let handle;
  try {
    handle = await open(temp, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.chmod(0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temp, statePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(temp).catch(() => {});
    throw new Error('unable to atomically write DeepSeek history cursor');
  }
}

async function ensurePrivateDirectory(directory: string): Promise<void> {
  const absolute = path.resolve(directory);
  const parsed = path.parse(absolute);
  let current = parsed.root;
  for (const part of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    let info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) {
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private DeepSeek cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error('DeepSeek cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function assertSafeParentDirectories(directory: string): Promise<void> {
  const absolute = path.resolve(directory);
  const parsed = path.parse(absolute);
  let current = parsed.root;
  for (const part of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    const info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('DeepSeek cursor parent path contains a symbolic link or non-directory');
  }
}

function withDeterministicId(event: ThreadforgeEvent, sourceHash: string, line: number, index: number): ThreadforgeEvent {
  event.event.id = sha256(`${sourceHash}:poll:${line}:${index}`);
  return event;
}

function digestText(value: string): JsonObject {
  const bytes = Buffer.byteLength(value, 'utf8');
  return { retention: 'metadata', included: false, bytes, ...(bytes <= 64 * 1024 ? { hash: sha256(value) } : { omitted: true }) };
}

function digestValue(value: string): JsonObject { return digestText(value); }

function dshExitCode(value: string): number | undefined {
  const match = /\[exit code:\s*(-?\d+)\]/i.exec(value);
  if (!match) return undefined;
  const number = Number(match[1]);
  return Number.isSafeInteger(number) ? number : undefined;
}

function messageText(data: JsonObject): string { return textValue(firstPresent(unwrap(data, 'message'), 'text', 'content', 'message')); }

function textValue(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map((item) => isRecord(item) ? textValue(firstPresent(item, 'text', 'content', 'value')) : '').filter(Boolean).join('\n');
  if (isRecord(value)) return firstString(value, 'text', 'content', 'value') ?? '';
  return '';
}

function parseTimestamp(value: unknown, fallbackMs: number): string {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
    const date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof value === 'string' && value.trim()) {
    const numeric = Number(value);
    const date = new Date(Number.isFinite(numeric) ? numeric < 1_000_000_000_000 ? numeric * 1_000 : numeric : value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  const fallback = new Date(fallbackMs);
  return Number.isNaN(fallback.getTime()) ? new Date(0).toISOString() : fallback.toISOString();
}

function unwrap(value: JsonObject, ...keys: string[]): JsonObject {
  for (const key of keys) if (isRecord(value[key])) return value[key] as JsonObject;
  return value;
}

function childMap(value: JsonObject, key: string): JsonObject { return isRecord(value[key]) ? value[key] as JsonObject : {}; }
function firstPresent(value: JsonObject, ...keys: string[]): unknown { for (const key of keys) if (value[key] !== undefined && value[key] !== null) return value[key]; return undefined; }
function firstString(value: JsonObject, ...keys: string[]): string | undefined {
  for (const key of keys) { const item = value[key]; if (typeof item === 'string' && item.trim()) return item.trim(); }
  return undefined;
}
function safeLabel(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const label = value.trim();
  if (!label || label.length > max || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(label) || looksSecretLike(label)) return undefined;
  return label;
}
function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function safeJSONStringify(value: unknown): string { try { return JSON.stringify(value) ?? ''; } catch { return ''; } }
function boundedInteger(value: number, min: number, max: number, label: string): number {
  if (!Number.isInteger(value) || value < min || value > max) throw new Error(`${label} must be between ${min} and ${max}`);
  return value;
}
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && (error as { code?: unknown }).code === code; }
function sameFile(a: Stats, b: Stats | null): boolean { return b !== null && a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeMs === b.mtimeMs; }
function samePath(left: string, right: string): boolean {
  const a = path.resolve(left).replace(/[\\/]+$/, ''); const b = path.resolve(right).replace(/[\\/]+$/, '');
  return process.platform === 'win32' ? a.toLowerCase() === b.toLowerCase() : a === b;
}
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
function safeError(_error: unknown): string { return 'DeepSeek session could not be read or delivered safely; inspect local files and retry'; }
async function deliverGroups(
  groups: EventGroup[], cursor: Cursor, parsed: ParsedSession, options: DeepSeekHistoryOptions,
  result: DeepSeekHistoryResult, checkpoint: () => Promise<void>,
): Promise<void> {
  let batch: ThreadforgeEvent[] = [];
  let accepted: EventGroup[] = [];
  const flush = async () => {
    if (!batch.length) return;
    try { await emitEvents(batch, { endpoint: options.endpoint, timeoutMs: 1_500, fetchImpl: options.fetchImpl }); }
    catch { throw new Error('DeepSeek batch rejected'); }
    result.events_emitted += batch.length;
    for (const group of accepted) {
      if (group.startsSession) cursor.started = true;
      if (group.line > 0) {
        const hash = parsed.prefixHashes.get(group.line);
        if (hash) { cursor.last_line = group.line; cursor.prefix_sha256 = hash; }
      }
    }
    batch = [];
    accepted = [];
    await checkpoint();
  };
  for (const group of groups) {
    if (group.events.length > batchSize) throw new Error('one DeepSeek record exceeded the event batch bound');
    if (batch.length + group.events.length > batchSize) await flush();
    if (group.events.length) { batch.push(...group.events); accepted.push(group); }
  }
  await flush();
}

function mapSessionRecords(file: SessionFile, records: SessionRecord[], cursor: Cursor, includeContent: boolean): EventGroup[] {
  const sourceHash = sha256(file.key);
  const metadata = sessionMetadata(records, file);
  const sessionHash = sha256(metadata.id);
  const projectHash = metadata.cwd ? sha256(metadata.cwd) : undefined;
  let model: string | undefined;
  const calls = new Map<string, { id: string; name: string; args: JsonObject }>();
  const groups: EventGroup[] = [];
  if (!cursor.started && records.length) {
    const sessionRecord = records.find((record) => record.kind === 'session') ?? records[0]!;
    const started = baseEvent(file, sourceHash, sessionHash, projectHash, sessionRecord, 'session.started', 'session', { model });
    groups.push({ line: 0, startsSession: true, events: [withDeterministicId(started, sourceHash, 0, 0)] });
  }
  for (const record of records) {
    if (record.kind === 'request/header') model = safeLabel(childMap(childMap(record.data, 'header'), 'config').model, 128) ?? model;
    if (record.kind === 'assistant/message') model = safeLabel(childMap(unwrap(record.data, 'message'), 'source').model, 128) ?? safeLabel(unwrap(record.data, 'message').model, 128) ?? model;
    if (record.kind === 'tool/call' || record.kind === 'tool/code-dispatch-start') {
      const call = parseToolCall(record);
      if (call.id) {
        if (calls.size >= maxToolCalls && !calls.has(call.id)) calls.delete(calls.keys().next().value!);
        calls.set(call.id, call);
      }
    }
    if (record.line <= cursor.last_line) continue;
    const events: ThreadforgeEvent[] = [];
    if (record.kind === 'user/message') {
      const text = messageText(record.data);
      if (text) events.push(textEvent(file, sourceHash, sessionHash, projectHash, record, 'prompt.submitted', 'prompt', text, includeContent, model));
    } else if (record.kind === 'assistant/message') {
      const message = unwrap(record.data, 'message');
      const reasoning = firstString(message, 'reasoning', 'reasoningText', 'reasoning_content');
      if (reasoning) events.push(textEvent(file, sourceHash, sessionHash, projectHash, record, 'agent.reasoning', 'agent', reasoning, false, model));
      for (const part of assistantParts(message)) {
        events.push(textEvent(file, sourceHash, sessionHash, projectHash, record, part.kind === 'reasoning' ? 'agent.reasoning' : 'agent.message', 'agent', part.text,
          part.kind === 'text' && includeContent, model));
      }
      const usage = usageFromAssistant(record.data);
      if (usage) {
        const event = baseEvent(file, sourceHash, sessionHash, projectHash, record, 'token.usage', 'metric', { model });
        event.gen_ai = { usage };
        events.push(event);
      }
    } else if (record.kind === 'tool/call' || record.kind === 'tool/code-dispatch-start') {
      const call = parseToolCall(record);
      if (call.name) events.push(toolEvent(file, sourceHash, sessionHash, projectHash, record, call, undefined, model));
    } else if (record.kind === 'tool/result' || record.kind === 'tool/code-dispatch') {
      const result = parseToolResult(record);
      const call = calls.get(result.callId) ?? { id: result.callId, name: result.name, args: {} };
      if (result.name) call.name = result.name;
      if (call.name) events.push(toolEvent(file, sourceHash, sessionHash, projectHash, record, call, result, model));
    } else if (record.kind === 'turn/end') {
      const reason = childMap(record.data, 'reason');
      const kind = firstString(reason, 'kind', 'type');
      if (kind === 'error' || kind === 'interrupted') {
        const event = baseEvent(file, sourceHash, sessionHash, projectHash, record, 'agent.error', 'session', { severity: 'high', model });
        event.error = { type: safeLabel(kind, 32) ?? 'unknown' };
        events.push(event);
      }
    }
    if (events.length) {
      const indexed = events.map((event, index) => withDeterministicId(event, sourceHash, record.line, index));
      groups.push({ line: record.line, events: indexed });
    } else if (record.line > cursor.last_line) groups.push({ line: record.line, events: [] });
  }
  return groups;
}

function baseEvent(
  file: SessionFile, sourceHash: string, sessionHash: string, projectHash: string | undefined, record: SessionRecord,
  action: string, category: string, options: { model?: string; severity?: ThreadforgeEvent['severity'] } = {},
): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: parseTimestamp(record.time ?? record.createdAt, file.mtimeMs),
    vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: options.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'deepseek_harness', collection_method: 'poll' },
    session: { id: `dsh:${sessionHash}` },
    raw: { source: 'dsh_native_session', source_sha256: sourceHash, line: record.line, record_type: safeLabel(record.kind, 64) ?? 'unknown' },
  };
  if (projectHash) event.project = { id: projectHash };
  if (options.model) event.model = options.model;
  return event;
}

function textEvent(
  file: SessionFile, sourceHash: string, sessionHash: string, projectHash: string | undefined, record: SessionRecord,
  action: string, category: string, text: string, includeContent: boolean, model?: string,
): ThreadforgeEvent {
  const event = baseEvent(file, sourceHash, sessionHash, projectHash, record, action, category, { model });
  const bytes = Buffer.byteLength(text, 'utf8');
  const include = includeContent && bytes <= maxContentBytes;
  event.content = { retention: include ? 'full' : 'metadata', included: include, bytes,
    ...(bytes <= 64 * 1024 ? { hash: sha256(text) } : { omitted: true }), ...(includeContent && !include ? { truncated: true } : {}) };
  if (include) {
    if (action === 'prompt.submitted') event.prompt = { text };
    const part = { role: action === 'prompt.submitted' ? 'user' : 'assistant', parts: [{ type: action === 'agent.reasoning' ? 'reasoning' : 'text', content: text }] };
    event.gen_ai = action === 'prompt.submitted' ? { input: { messages: [part] } } : { output: { messages: [part] } };
  }
  return event;
}

function toolEvent(
  file: SessionFile, sourceHash: string, sessionHash: string, projectHash: string | undefined, record: SessionRecord,
  call: { id: string; name: string; args: JsonObject }, result?: { text: string; isError: boolean }, model?: string,
): ThreadforgeEvent {
  const toolName = safeLabel(call.name, 128) ?? 'unknown';
  const isResult = result !== undefined;
  const lowered = toolName.toLowerCase();
  let action = isResult ? 'tool.completed' : 'tool.invoked';
  let category = 'tool';
  let severity: ThreadforgeEvent['severity'] = 'info';
  if (isResult && result.isError) { action = 'tool.failed'; severity = 'high'; }
  else if (isResult && lowered.startsWith('mcp__')) { action = 'mcp.tool_invoked'; category = 'mcp'; }
  else if (isResult) {
    const type = toolType(toolName, call.args);
    if (type === 'command') { action = 'command.executed'; category = 'command'; }
    else if (type === 'read') { action = 'file.read'; category = 'file'; }
    else if (type === 'write') { action = 'file.modified'; category = 'file'; }
  }
  const event = baseEvent(file, sourceHash, sessionHash, projectHash, record, action, category, { model, severity });
  event.tool = { name: toolName };
  event.raw = { ...event.raw, ...(call.id ? { tool_call_sha256: sha256(call.id) } : {}) };
  const command = firstString(call.args, 'command', 'cmd', 'text');
  if (command) event.command = digestValue(command);
  const filePath = firstString(call.args, 'file_path', 'path', 'target_path');
  if (filePath) event.file = { path_sha256: sha256(filePath), operation: fileOperation(toolName, call.args) };
  const argumentsJson = safeJSONStringify(call.args);
  event.content = digestText(argumentsJson);
  if (result) {
    event.raw = { ...event.raw, result_content: digestText(result.text), is_error: result.isError };
    if (action === 'command.executed' && result.text) {
      const exitCode = dshExitCode(result.text);
      event.command = { ...(isRecord(event.command) ? event.command : {}), ...(exitCode === undefined ? {} : { exit_code: exitCode }),
        output: digestText(result.text) };
    }
    if (result.isError) event.error = { type: 'tool_execution_failed' };
  }
  return event;
}

function parseToolCall(record: SessionRecord): { id: string; name: string; args: JsonObject } {
  const data = unwrap(record.data, 'call', 'toolCall', 'tool_call');
  const argsValue = firstPresent(data, 'arguments', 'args', 'input', 'tool_input');
  let args: JsonObject = isRecord(argsValue) ? argsValue : {};
  if (!Object.keys(args).length) {
    const text = firstString(data, 'argumentsJson', 'arguments_json');
    if (text) { try { const parsed: unknown = JSON.parse(text); if (isRecord(parsed)) args = parsed; } catch { /* invalid argument JSON stays omitted */ } }
  }
  return { id: firstString(data, 'callId', 'callID', 'id', 'tool_call_id', 'tool_use_id') ?? '',
    name: firstString(data, 'name', 'toolName', 'tool_name') ?? '', args };
}

function parseToolResult(record: SessionRecord): { callId: string; name: string; text: string; isError: boolean } {
  const data = unwrap(record.data, 'result', 'toolResult', 'tool_result');
  return { callId: firstString(data, 'callId', 'callID', 'id', 'tool_call_id', 'toolUseId', 'tool_use_id') ?? '',
    name: firstString(data, 'name', 'toolName', 'tool_name') ?? '', text: textValue(firstPresent(data, 'text', 'output', 'content', 'result')),
    isError: isTrueish(data.isError) || isTrueish(data.is_error) || isTrueish(data.error) };
}

function toolType(name: string, args: JsonObject): string {
  const normalized = name.toLowerCase();
  if (normalized.startsWith('mcp__')) return 'mcp';
  if (['bash', 'pwsh', 'terminal_send'].includes(normalized)) return 'command';
  if (['read', 'read_image', 'glob', 'grep'].includes(normalized)) return 'read';
  if (normalized === 'write') return 'write';
  if (normalized === 'edit') return 'modify';
  if (normalized === 'str_replace_editor') {
    const command = firstString(args, 'command')?.toLowerCase();
    if (command === 'view') return 'read';
    if (command === 'create') return 'write';
    if (command === 'str_replace' || command === 'insert') return 'modify';
  }
  return 'tool';
}

function fileOperation(name: string, args: JsonObject): string {
  switch (toolType(name, args)) {
    case 'read': return 'read';
    case 'write': return name.toLowerCase() === 'write' || firstString(args, 'command')?.toLowerCase() === 'create' ? 'create' : 'modify';
    case 'modify': return 'modify';
    default: return 'unknown';
  }
}

function isTrueish(value: unknown): boolean {
  return value === true || (typeof value === 'string' && ['true', 'error'].includes(value.trim().toLowerCase()));
}

function assistantParts(message: JsonObject): Array<{ kind: 'text' | 'reasoning'; text: string }> {
  const value = firstPresent(message, 'content', 'parts');
  const parts: Array<{ kind: 'text' | 'reasoning'; text: string }> = [];
  if (Array.isArray(value)) {
    for (const part of value) {
      if (!isRecord(part)) continue;
      const kind = safeLabel(part.type, 64)?.toLowerCase();
      if (kind === 'tool-call' || kind === 'tool_call') continue;
      const text = textValue(firstPresent(part, 'text', 'content'));
      if (text) parts.push({ kind: kind === 'reasoning' || kind === 'thinking' ? 'reasoning' : 'text', text });
    }
  } else {
    const text = textValue(value);
    if (text) parts.push({ kind: 'text', text });
  }
  return parts;
}

function usageFromAssistant(data: JsonObject): JsonObject | undefined {
  const message = unwrap(data, 'message');
  let usage = childMap(message, 'usage');
  if (isRecord(usage.usage)) usage = usage.usage;
  if (!Object.keys(usage).length) usage = childMap(data, 'usage');
  const fields: JsonObject = {};
  assignUsage(fields, 'input_tokens', firstPresent(usage, 'inputTokens', 'input_tokens', 'promptTokens'));
  assignUsage(fields, 'output_tokens', firstPresent(usage, 'outputTokens', 'output_tokens', 'completionTokens'));
  assignUsage(fields, 'cache_read_input_tokens', firstPresent(usage, 'cacheReadTokens', 'cache_read_tokens'));
  assignUsage(fields, 'cache_creation_input_tokens', firstPresent(usage, 'cacheWriteTokens', 'cache_write_tokens'));
  assignUsage(fields, 'reasoning_output_tokens', firstPresent(usage, 'reasoningTokens', 'reasoning_tokens'));
  return Object.keys(fields).length ? fields : undefined;
}

function assignUsage(target: JsonObject, key: string, value: unknown): void {
  const number = typeof value === 'number' ? value : typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : NaN;
  if (Number.isSafeInteger(number) && number >= 0) target[key] = number;
}
