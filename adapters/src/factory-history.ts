import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks, isWithinRealPaths } from './path-safety.ts';

const stateVersion = 1;
const stateFileName = 'factory-sessions.v1.json';
const maxStateBytes = 8 << 20;
const maxSessions = 5_000;
const maxFileBytes = 64 << 20;
const maxSettingsBytes = 1 << 20;
const maxTotalBytes = 512 << 20;
const maxLineBytes = 4 << 20;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxToolCorrelations = 10_000;
const batchSize = 100;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;

type JsonObject = Record<string, unknown>;
type SessionCursor = {
  line: number;
  size_bytes: number;
  mtime_ms: number;
  prefix_sha256: string;
  session_started: boolean;
  settings_mtime_ms: number;
  settings_fingerprint: string;
};
type CursorState = { schema_version: 'threadforge.factory-session-cursor.v1'; version: number; files: Record<string, SessionCursor> };
type FactoryFile = { key: string; path: string; settingsPath: string; settingsMtimeMs: number; sessionId: string; size: number; mtimeMs: number };
type ToolCall = { name: string };
type Usage = { input?: number; output?: number; cacheRead?: number; cacheWrite?: number; reasoning?: number; credits?: number };
type FactorySettings = { model?: string; usage?: Usage; modifiedMs: number; sizeBytes: number; fingerprint: string };

export type FactoryHistoryOptions = {
  sessionsDir?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type FactoryHistoryResult = {
  schema_version: 'threadforge.factory-history-sync.v1';
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

export type FactoryHistoryStatus = {
  schema_version: 'threadforge.factory-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
};

export async function getFactoryHistoryStatus(
  options: Pick<FactoryHistoryOptions, 'sessionsDir' | 'statePath' | 'maxSessions'> = {},
): Promise<FactoryHistoryStatus> {
  const maximum = boundedInteger(options.maxSessions ?? 1_000, 1, maxSessions, '--max-sessions');
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (await isWithinRealPaths(sessionsDir, statePath) !== false) throw new Error('Factory history cursor must be stored outside Factory sessions');
  const listed = await listSessions(sessionsDir, maximum);
  const state = await readState(statePath);
  const checkpointed = listed.files.filter((file) => {
    const cursor = state.files[sha256(file.key)];
    return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs &&
      cursor.settings_mtime_ms === file.settingsMtimeMs;
  }).length;
  return {
    schema_version: 'threadforge.factory-history-status.v1', sessions: listed.files.length,
    checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    sessions_truncated: listed.truncated,
  };
}

export async function syncFactoryHistory(options: FactoryHistoryOptions = {}): Promise<FactoryHistoryResult> {
  validateOptions(options);
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (await isWithinRealPaths(sessionsDir, statePath) !== false) throw new Error('Factory history cursor must be stored outside Factory sessions');
  const listed = await listSessions(sessionsDir, options.maxSessions ?? 1_000);
  const result = emptyResult(options.includeContent ?? false);
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.sessions_truncated = listed.truncated;
  if (options.preview) {
    await processFiles(listed.files, sessionsDir, await readState(statePath), options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFiles(listed.files, sessionsDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated;
  return result;
}

function emptyResult(includeContent: boolean): FactoryHistoryResult {
  return {
    schema_version: 'threadforge.factory-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: FactoryHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? 1_000, 1, maxSessions, '--max-sessions');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveSessionsDir(value?: string): Promise<string> {
  const sessionsDir = value ?? path.join(os.homedir(), '.factory', 'sessions');
  if (!path.isAbsolute(sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  const info = await lstat(sessionsDir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return path.resolve(sessionsDir);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Factory sessions path must be a real directory');
  const canonical = await realpath(sessionsDir).catch(() => { throw new Error('Factory sessions directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, sessionsDir)) throw new Error('Factory sessions directory must not traverse symbolic links');
  return canonical;
}

async function listSessions(
  root: string, maximum: number,
): Promise<{ files: FactoryFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Factory sessions path must be a real directory');
  const files: FactoryFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  const projects = await readdir(root, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Factory project folders'); });
  for (const project of projects) {
    const projectPath = path.join(root, project.name);
    const projectInfo = await lstat(projectPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!projectInfo) continue;
    if (projectInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!projectInfo.isDirectory()) continue;
    const entries = await readdir(projectPath, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Factory session files'); });
    for (const entry of entries) {
      if (!entry.name.toLowerCase().endsWith('.jsonl')) continue;
      const sessionId = entry.name.slice(0, -'.jsonl'.length);
      if (!/^[A-Za-z0-9_-]{1,255}$/.test(sessionId)) continue;
      const sourcePath = path.join(projectPath, entry.name);
      const info = await lstat(sourcePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (!info.isFile()) continue;
      if (info.size > maxFileBytes) { oversizedSkipped++; continue; }
      const canonical = await realpath(sourcePath).catch(() => '');
      if (!canonical || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
      const key = path.relative(root, canonical).split(path.sep).join('/');
      if (!isSafeRelative(key)) { symlinksSkipped++; continue; }
      const settingsPath = path.join(projectPath, `${sessionId}.settings.json`);
      const settingsInfo = await lstat(settingsPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      const settingsMtimeMs = !settingsInfo ? 0 : settingsInfo.isFile() && !settingsInfo.isSymbolicLink() ? settingsInfo.mtimeMs : -1;
      files.push({ key, path: canonical, settingsPath, settingsMtimeMs, sessionId, size: info.size, mtimeMs: info.mtimeMs });
      if (files.length > maximum) { truncated = true; break; }
    }
    if (truncated) break;
  }
  files.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  if (truncated) files.length = maximum;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

function isSafeRelative(value: string): boolean { return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function readSessionFile(file: FactoryFile, root: string): Promise<Buffer> {
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes) throw new Error('Factory session is not a bounded regular file');
  if (before.size !== file.size || before.mtimeMs !== file.mtimeMs) throw new Error('Factory session changed after enumeration');
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error('Factory session path escaped the sessions directory');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error('Factory session changed before reading');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) throw new Error('Factory session changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Factory session')) throw error;
    throw new Error('Factory session could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

async function readSettingsFile(file: FactoryFile, root: string): Promise<FactorySettings | undefined> {
  const before = await lstat(file.settingsPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!before) return undefined;
  if (!before.isFile() || before.isSymbolicLink() || before.size > maxSettingsBytes) throw new Error('Factory settings must be a bounded regular file');
  const canonical = await realpath(file.settingsPath).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.settingsPath) || !isWithin(root, canonical) || path.dirname(canonical) !== path.dirname(file.path)) {
    throw new Error('Factory settings path escaped the sessions directory');
  }
  let handle;
  try {
    handle = await open(file.settingsPath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error('Factory settings changed before reading');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.settingsPath).catch(() => null);
    if (bytes.byteLength > maxSettingsBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        opened.size !== afterHandle.size || opened.mtimeMs !== afterHandle.mtimeMs) throw new Error('Factory settings changed during read');
    const decoded = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(decoded)) throw new Error('Factory settings are malformed');
    const model = safeLabel(decoded.model, 128);
    const usageValue = isRecord(decoded.tokenUsage) ? decoded.tokenUsage : isRecord(decoded.inclusiveTokenUsage) ? decoded.inclusiveTokenUsage : undefined;
    const usage = usageValue ? parseSettingsUsage(usageValue, usageValue.factoryCredits ?? decoded.factoryCredits) : undefined;
    // Only allowlisted values are fingerprinted; providerLock, child-session maps,
    // and unknown settings are never emitted or copied into the local cursor.
    const allowed = { ...(model ? { model } : {}), ...(usage ? { usage } : {}) };
    return { ...(model ? { model } : {}), ...(usage ? { usage } : {}), modifiedMs: opened.mtimeMs, sizeBytes: bytes.byteLength, fingerprint: sha256(JSON.stringify(allowed)) };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Factory settings')) throw error;
    throw new Error('Factory settings are malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

function parseSettingsUsage(value: JsonObject, creditsValue: unknown): Usage | undefined {
  const usage: Usage = {};
  const input = nonNegativeInteger(value.inputTokens);
  const output = nonNegativeInteger(value.outputTokens);
  const cacheWrite = nonNegativeInteger(value.cacheCreationTokens);
  const cacheRead = nonNegativeInteger(value.cacheReadTokens);
  const reasoning = nonNegativeInteger(value.thinkingTokens);
  const credits = typeof creditsValue === 'number' && Number.isFinite(creditsValue) && creditsValue >= 0 && creditsValue <= 1_000_000_000_000
    ? creditsValue : undefined;
  if (input !== undefined) usage.input = input;
  if (output !== undefined) usage.output = output;
  if (cacheWrite !== undefined) usage.cacheWrite = cacheWrite;
  if (cacheRead !== undefined) usage.cacheRead = cacheRead;
  if (reasoning !== undefined) usage.reasoning = reasoning;
  if (credits !== undefined) usage.credits = credits;
  return Object.keys(usage).length > 0 ? usage : undefined;
}

function makeSettingsUsageEvent(file: FactoryFile, settings: FactorySettings): ThreadforgeEvent | undefined {
  if (!settings.usage) return undefined;
  const usage: JsonObject = {};
  if (settings.usage.input !== undefined) usage.input_tokens = settings.usage.input;
  if (settings.usage.output !== undefined) usage.output_tokens = settings.usage.output;
  if (settings.usage.cacheRead !== undefined) usage.cache_read_input_tokens = settings.usage.cacheRead;
  if (settings.usage.cacheWrite !== undefined) usage.cache_creation_input_tokens = settings.usage.cacheWrite;
  if (settings.usage.reasoning !== undefined) usage.reasoning_tokens = settings.usage.reasoning;
  if (settings.usage.credits !== undefined) usage.factory_credits = settings.usage.credits;
  const fileHash = sha256(file.key);
  const event: ThreadforgeEvent = {
    timestamp: new Date(settings.modifiedMs).toISOString(), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${fileHash}:settings:${settings.fingerprint}`), kind: 'agent_runtime', action: 'token.usage', category: 'metric', fidelity: 'observed' },
    severity: 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'factory', collection_method: 'poll' }, session: { id: `factory:${sha256(file.sessionId)}` },
    raw: { source: 'factory_session_settings_json', source_sha256: fileHash, message: 'Factory Droid token usage recovered from allowlisted local settings' },
    gen_ai: { usage },
  };
  if (settings.model) event.model = settings.model;
  return event;
}

async function processFiles(
  files: FactoryFile[], root: string, state: CursorState, options: FactoryHistoryOptions, result: FactoryHistoryResult,
  persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const key = sha256(file.key);
    const previous = state.files[key];
    try {
      const bytes = await readSessionFile(file, root);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the total source-byte limit' });
        break;
      }
      let settings: FactorySettings | undefined;
      try {
        settings = await readSettingsFile(file, root);
        if (settings) totalBytes += settings.sizeBytes;
        if (totalBytes > maxTotalBytes) {
          result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the total source-byte limit' });
          break;
        }
      } catch {
        result.errors.push({ source_sha256: key, message: 'Factory session settings could not be read safely' });
        continue;
      }

      const parsed = parseSessionFile(bytes, file, previous, settings?.model, options.includeContent ?? false);
      result.malformed_lines += parsed.malformed;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: key, message: 'Factory transcript contains a malformed row; scanning stops before it' });
      if (parsed.partialTail) result.partial_sessions++;
      const cursorLine = previous?.line ?? 0;
      if (previous && (parsed.completeLines < cursorLine || parsed.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: key, message: 'Factory transcript prefix changed; refusing automatic replay' });
        continue;
      }

      const fileEvents = parsed.groups.reduce((count, group) => count + group.events.length, 0);
      const settingsFingerprint = settings?.fingerprint ?? '';
      const settingsChanged = settingsFingerprint !== (previous?.settings_fingerprint ?? '');
      const settingsCheckpointChanged = settingsChanged || (settings?.modifiedMs ?? 0) !== (previous?.settings_mtime_ms ?? 0);
      const settingsEvent = settings && settingsChanged ? makeSettingsUsageEvent(file, settings) : undefined;
      const pendingEvents = fileEvents + (settingsEvent ? 1 : 0);
      if (pendingEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the event processing limit' });
        break;
      }

      if (options.preview) {
        result.events_previewed += pendingEvents;
        result.events ??= [];
        for (const group of parsed.groups) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        if (settingsEvent) {
          if (result.events.length < maxPreviewEvents) result.events.push(settingsEvent);
          else result.preview_truncated = true;
        }
        continue;
      }

      let failed = false;
      let batch: Array<{ line: number; events: ThreadforgeEvent[] }> = [];
      let batchEvents = 0;
      const flush = async () => {
        if (batch.length === 0) return;
        const send = batch.flatMap((group) => group.events);
        await emitEvents(send, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        const lastLine = batch[batch.length - 1]!.line;
        const started = previous?.session_started === true || batch.some((group) => group.events.some((event) => event.event.action === 'session.started'));
        const prefix = parsed.prefixHashes.get(lastLine);
        if (!prefix) throw new Error('Factory session checkpoint prefix is unavailable');
        state.files[key] = makeCursor(lastLine, file, prefix, started, previous);
        result.events_emitted += send.length;
        if (persist && statePath) await writeState(statePath, state);
        batch = [];
        batchEvents = 0;
      };
      try {
        for (const group of parsed.groups) {
          if (batchEvents > 0 && batchEvents + group.events.length > batchSize) await flush();
          batch.push(group);
          batchEvents += group.events.length;
        }
        await flush();
      } catch {
        failed = true;
        result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Factory history events' });
      }
      if (failed) continue;

      const completePrefix = parsed.completePrefixHash;
      if (!completePrefix) {
        result.errors.push({ source_sha256: key, message: 'Factory session checkpoint prefix is unavailable' });
        continue;
      }
      const transcriptAdvanced = parsed.completeLines > cursorLine || previous === undefined ||
        (previous !== undefined && previous.session_started !== parsed.sessionStarted);
      if (transcriptAdvanced) {
        state.files[key] = makeCursor(parsed.completeLines, file, completePrefix, parsed.sessionStarted, previous);
        if (persist && statePath) await writeState(statePath, state);
      }

      if (settingsCheckpointChanged) {
        if (settingsChanged && settingsEvent) {
          try {
            await emitEvents([settingsEvent], { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
            result.events_emitted++;
          } catch {
            result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Factory settings usage' });
            continue;
          }
        }
        const current = state.files[key] ?? makeCursor(parsed.completeLines, file, completePrefix, parsed.sessionStarted, previous);
        state.files[key] = { ...current, settings_mtime_ms: settings?.modifiedMs ?? 0, settings_fingerprint: settingsFingerprint };
        if (persist && statePath) await writeState(statePath, state);
      }
      if (transcriptAdvanced || settingsCheckpointChanged) result.sessions_changed++;
    } catch {
      result.errors.push({ source_sha256: key, message: 'Factory session could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(line: number, file: FactoryFile, prefix: string, started: boolean, previous?: SessionCursor): SessionCursor {
  return {
    line, size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix, session_started: started,
    settings_mtime_ms: previous?.settings_mtime_ms ?? 0, settings_fingerprint: previous?.settings_fingerprint ?? '',
  };
}

function resolveStatePath(value?: string): string {
  if (value !== undefined) {
    if (!path.isAbsolute(value)) throw new Error('--state-path must be an absolute path');
    return path.resolve(value);
  }
  const explicitRoot = process.env.THREADFORGE_HOME?.trim();
  let root = explicitRoot;
  if (!root) {
    if (process.platform === 'win32' && process.env.LOCALAPPDATA) root = path.join(process.env.LOCALAPPDATA, 'threadforge');
    else if (process.platform === 'darwin') root = path.join(os.homedir(), 'Library', 'Caches', 'threadforge');
    else root = path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'threadforge');
  }
  if (!path.isAbsolute(root)) throw new Error('THREADFORGE_HOME/cache root must be an absolute path');
  return path.join(root, 'state', stateFileName);
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await verifyStateParent(statePath, true);
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Factory history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Factory history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.factory-session-cursor.v1', version: stateVersion, files: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Factory history cursor');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Factory history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('Factory cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxStateBytes) throw new Error('Factory cursor exceeds local size limit');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
      throw new Error('Factory history cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, SessionCursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean' ||
          typeof value.settings_mtime_ms !== 'number' || !Number.isFinite(value.settings_mtime_ms) || value.settings_mtime_ms < 0 ||
          typeof value.settings_fingerprint !== 'string' ||
          (value.settings_fingerprint !== '' && !/^[0-9a-f]{64}$/.test(value.settings_fingerprint))) {
        throw new Error('Factory history cursor contains an invalid checkpoint');
      }
      files[key] = {
        line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, prefix_sha256: value.prefix_sha256,
        session_started: value.session_started, settings_mtime_ms: value.settings_mtime_ms, settings_fingerprint: value.settings_fingerprint,
      };
    }
    return { ...empty, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Factory history cursor')) throw error;
    throw new Error('Factory history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

async function verifyStateParent(statePath: string, create: boolean): Promise<void> {
  const parent = path.dirname(path.resolve(statePath));
  const parsed = path.parse(parent);
  const segments = parent.slice(parsed.root.length).split(path.sep).filter(Boolean);
  let current = parsed.root;
  for (const segment of segments) {
    current = path.join(current, segment);
    let info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info && create) {
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Factory cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Factory cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const payload = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('Factory history cursor exceeds the local size limit');
  const temporary = `${statePath}.${randomBytes(12).toString('hex')}.tmp`;
  let handle;
  try {
    handle = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.chmod(0o600);
    await handle.writeFile(payload);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, statePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(temporary).catch(() => {});
    throw new Error('unable to atomically write Factory history cursor');
  }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

type ParsedFile = {
  completeLines: number;
  partialTail: boolean;
  malformed: number;
  prefixHashes: Map<number, string>;
  completePrefixHash: string;
  groups: Array<{ line: number; events: ThreadforgeEvent[] }>;
  sessionStarted: boolean;
};

type MapperState = { sessionStarted: boolean; model?: string; tools: Map<string, ToolCall> };

function parseSessionFile(
  bytes: Buffer, file: FactoryFile, previous: SessionCursor | undefined, settingsModel?: string, includeContent = false,
): ParsedFile {
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const groups: Array<{ line: number; events: ThreadforgeEvent[] }> = [];
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const mapper: MapperState = { sessionStarted: previous?.session_started ?? false, ...(settingsModel ? { model: settingsModel } : {}), tools: new Map() };
  let line = 0;
  let safeLines = 0;
  let start = 0;
  let malformed = 0;
  let eventCount = 0;
  let stoppedOnMalformed = false;
  for (let offset = 0; offset < bytes.length; offset++) {
    if (bytes[offset] !== 0x0a) continue;
    line++;
    if (line > maxLinesPerFile) throw new Error(`Factory session exceeds ${maxLinesPerFile} lines`);
    const chunkStart = start;
    const end = offset > start && bytes[offset - 1] === 0x0d ? offset - 1 : offset;
    const raw = bytes.subarray(start, end);
    start = offset + 1;
    if (raw.byteLength > maxLineBytes) { malformed++; stoppedOnMalformed = true; break; }
    if (raw.toString('utf8').trim() === '') {
      prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
      safeLines = line;
      prefixHashes.set(line, prefixHasher.copy().digest('hex'));
      continue;
    }
    let record: JsonObject;
    try {
      const decoded = JSON.parse(decoder.decode(raw)) as unknown;
      if (!isRecord(decoded) || !safeLabel(decoded.type, 96)) throw new Error('invalid record');
      record = decoded;
    } catch {
      malformed++;
      stoppedOnMalformed = true;
      break;
    }
    prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
    safeLines = line;
    prefixHashes.set(line, prefixHasher.copy().digest('hex'));
    const events = mapSessionRecord(record, line, file, mapper, line > (previous?.line ?? 0), includeContent);
    if (line <= (previous?.line ?? 0) || events.length === 0) continue;
    if (events.length > batchSize) throw new Error(`Factory session line ${line} maps to more than ${batchSize} events`);
    groups.push({ line, events });
    eventCount += events.length;
    if (eventCount > maxEventsPerFile) throw new Error(`Factory session exceeds ${maxEventsPerFile} events`);
  }
  return {
    completeLines: safeLines, partialTail: !stoppedOnMalformed && start < bytes.length, malformed,
    prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex'), groups, sessionStarted: mapper.sessionStarted,
  };
}

function mapSessionRecord(
  record: JsonObject, line: number, file: FactoryFile, state: MapperState, shouldEmit: boolean, includeContent: boolean,
): ThreadforgeEvent[] {
  const type = safeLabel(record.type, 96)?.toLowerCase() ?? '';
  const id = safeIdentifier(record.id, 512);
  const events: ThreadforgeEvent[] = [];
  if (type === 'session_start') {
    const previousStart = state.sessionStarted;
    state.sessionStarted = true;
    const model = safeLabel(record.model ?? record.selectedModel, 128);
    if (model) state.model = model;
    if (shouldEmit && !previousStart) events.push(makeEvent(record, file, line, 'session.started', 'session',
      'Factory Droid session start recovered from local transcript', `session.start:${id ?? line}`, state));
    return events;
  }
  if (type === 'todo_state') return events;
  if (type === 'compaction_state') {
    if (shouldEmit) {
      const summary = boundedString(record.summaryText, 1 << 20);
      events.push(makeEvent(record, file, line, 'session.compacted', 'session', 'Factory Droid compaction observed',
        `session.compaction:${id ?? line}`, state, { content: summary ? digestContent(summary) : undefined }));
    }
    return events;
  }
  if (type !== 'message' || !isRecord(record.message)) return events;
  const message = record.message;
  if (message.visibility === 'user_only' || message.visibility === 'llm_only') return events;
  const model = safeLabel(message.model, 128);
  if (model) state.model = model;
  const role = boundedString(message.role, 32)?.toLowerCase() ?? '';
  const blocks = messageBlocks(message.content);
  for (let index = 0; index < blocks.length; index++) {
    const block = blocks[index]!;
    const blockType = safeLabel(block.type, 64)?.toLowerCase() ?? 'text';
    if (role === 'user' && blockType === 'text') {
      if (shouldEmit) pushTextEvent(events, record, file, line, 'prompt.submitted', 'prompt', blockText(block), includeContent, state, `prompt:${id ?? line}:${index}`);
      continue;
    }
    if (role !== 'assistant' && role !== 'tool') continue;
    if (blockType === 'thinking' && shouldEmit) {
      const text = blockText(block);
      if (text) events.push(makeEvent(record, file, line, 'agent.reasoning', 'agent', 'Factory Droid reasoning digest observed',
        `reasoning:${id ?? line}:${index}`, state, { content: digestContent(text) }));
    } else if (blockType === 'tool_use') {
      const toolId = safeIdentifier(block.id, 512) ?? `${id ?? line}:tool:${index}`;
      const name = safeLabel(block.name, 128) ?? '';
      const input = parseJsonValue(block.input);
      if (name) {
        rememberTool(state.tools, toolId, name);
        if (shouldEmit) {
          const digest = digestContent(input);
          events.push(makeEvent(record, file, line, 'tool.invoked', 'tool', 'Factory Droid tool invocation observed',
            `tool.invoke:${id ?? line}:${index}`, state, { toolName: name, toolCallHash: sha256(toolId), content: digest }));
        }
      }
    } else if (blockType === 'tool_result') {
      const toolId = safeIdentifier(block.tool_use_id ?? block.id, 512) ?? `${id ?? line}:tool:${index}`;
      const name = safeLabel(state.tools.get(toolId)?.name ?? block.name, 128) ?? '';
      state.tools.delete(toolId);
      if (name && shouldEmit) {
        const isError = block.is_error === true;
        events.push(makeEvent(record, file, line, isError ? 'tool.failed' : 'tool.completed', 'tool',
          isError ? 'Factory Droid tool failure observed' : 'Factory Droid tool result observed', `tool.result:${id ?? line}:${index}`,
          state, { severity: isError ? 'medium' : 'info', toolName: name, toolCallHash: sha256(toolId), content: digestContent(block.content) }));
      }
    } else if (role === 'tool' && blockType === 'text' && shouldEmit) {
      const text = blockText(block);
      const name = safeLabel(message.name, 128);
      if (text) events.push(makeEvent(record, file, line, 'tool.completed', 'tool', 'Factory Droid tool output observed',
        `tool.message:${id ?? line}:${index}`, state, { ...(name ? { toolName: name } : {}), content: digestContent(text) }));
    } else if (role === 'assistant' && blockType === 'text' && shouldEmit) {
      const text = blockText(block);
      if (text) pushTextEvent(events, record, file, line, 'agent.message', 'agent', text, includeContent, state, `assistant:${id ?? line}:${index}`);
    }
  }
  return events;
}

function messageBlocks(value: unknown): JsonObject[] {
  if (Array.isArray(value)) return value.filter(isRecord);
  if (typeof value === 'string' && value.trim()) return [{ type: 'text', text: value }];
  if (isRecord(value)) return [value];
  return [];
}

function blockText(block: JsonObject): string {
  const value = block.text ?? block.thinking ?? block.content;
  if (typeof value === 'string') return value.trim();
  if (Array.isArray(value)) return value.flatMap((item) => isRecord(item) ? [blockText(item)] : []).filter(Boolean).join('\n');
  return '';
}

function parseJsonValue(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}

function rememberTool(tools: Map<string, ToolCall>, id: string, name: string): void {
  if (tools.size >= maxToolCorrelations && !tools.has(id)) tools.delete(tools.keys().next().value!);
  tools.set(id, { name });
}

function digestContent(value: unknown): JsonObject {
  if (value === undefined || value === null) return { retention: 'metadata', included: false };
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); } catch { return { retention: 'metadata', included: false }; }
  if (typeof text !== 'string') return { retention: 'metadata', included: false };
  return { retention: 'metadata', included: false, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

function pushTextEvent(
  events: ThreadforgeEvent[], record: JsonObject, file: FactoryFile, line: number, action: string,
  category: string, text: string, includeContent: boolean, state: MapperState, suffix: string,
): void {
  const value = text.trim();
  if (!value) return;
  const included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
  events.push(makeEvent(record, file, line, action, category,
    action === 'prompt.submitted' ? 'Factory Droid prompt recovered from local transcript' : 'Factory Droid assistant message recovered from local transcript',
    suffix, state, { content: contentDigest(value, included, includeContent), ...(included ? { text: value } : {}) }));
}

function makeEvent(
  record: JsonObject, file: FactoryFile, line: number, action: string, category: string, message: string,
  suffix: string, state: MapperState,
  extra: { content?: JsonObject; text?: string; toolName?: string; toolCallHash?: string; severity?: ThreadforgeEvent['severity']; genAi?: JsonObject } = {},
): ThreadforgeEvent {
  const recordId = safeIdentifier(record.id, 512);
  const rawSession = safeIdentifier(record.sessionId, 512) ?? file.sessionId;
  const event: ThreadforgeEvent = {
    timestamp: parseTimestamp(record.timestamp, file.mtimeMs), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${sha256(file.key)}:${line}:${recordId ? sha256(recordId) : ''}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'factory', collection_method: 'poll' },
    session: { id: `factory:${sha256(rawSession)}` },
    raw: { source: 'factory_session_jsonl', source_sha256: sha256(file.key), line,
      record_type: safeLabel(record.type, 96) ?? 'unknown', ...(recordId ? { record_id_sha256: sha256(recordId) } : {}), message },
  };
  if (state.model) event.model = state.model;
  if (extra.toolName) event.tool = { name: extra.toolName };
  if (extra.toolCallHash) event.raw = { ...event.raw, tool_call_sha256: extra.toolCallHash };
  if (extra.content) event.content = extra.content;
  if (extra.text !== undefined) {
    if (action === 'prompt.submitted') {
      event.prompt = { text: extra.text };
      event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: extra.text }] }] } };
    } else {
      event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: extra.text }] }] } };
    }
  }
  if (extra.genAi) event.gen_ai = extra.genAi;
  return event;
}

function contentDigest(text: string, included: boolean, requested = false): JsonObject {
  return { retention: included ? 'full' : 'metadata', included, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8'),
    ...(requested && !included ? { truncated: true } : {}) };
}

function parseTimestamp(value: unknown, fallbackMs: number): string {
  let date: Date | undefined;
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
  else if (typeof value === 'string') {
    const numeric = Number(value);
    date = value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0
      ? new Date(numeric < 1_000_000_000_000 ? numeric * 1_000 : numeric) : new Date(value);
  }
  return date && !Number.isNaN(date.getTime()) ? date.toISOString() : new Date(fallbackMs).toISOString();
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}
function safeIdentifier(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
}
function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
