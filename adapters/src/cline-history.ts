import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink, type FileHandle } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const stateSchema = 'threadforge.cline-history-cursor.v1';
const defaultMaxSessions = 1_000;
const maxSessionsLimit = 5_000;
const maxTaskDirs = 10_000;
const maxFiles = 20_000;
const maxFileBytes = 25 << 20;
const maxAggregateBytes = 512 << 20;
const maxRecords = 100_000;
const maxEventsPerSession = 10_000;
const maxEventsPerSync = 20_000;
const maxStateBytes = 8 << 20;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const batchSize = 100;

type JsonObject = Record<string, unknown>;
type SourceKind = 'history' | 'messages' | 'kanban';
type Entry = { index: number; value: JsonObject; digest: string };
type MutableRecord = { field: string; role: 'user' | 'assistant' | 'error'; text?: string; content_sha256: string; content_bytes: number; timestampMs: number; model?: string };
type ClineSource = {
  key: string;
  kind: SourceKind;
  id: string;
  root: string;
  filePath: string;
  size: number;
  mtimeMs: number;
  entries?: Entry[];
  mutableRecords?: MutableRecord[];
};
type Cursor = {
  record_count: number;
  prefix_sha256: string;
  session_started: boolean;
  mutable_hashes?: Record<string, string>;
};
type CursorState = { schema_version: typeof stateSchema; version: 1; sources: Record<string, Cursor> };
type EventGroup = { record: number; events: ThreadforgeEvent[]; startsSession?: boolean; field?: string };

export type ClineHistoryOptions = {
  clineDir?: string;
  vscodeStorageDir?: string;
  statePath?: string;
  endpoint?: string;
  fetchImpl?: typeof fetch;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type ClineHistoryResult = {
  schema_version: 'threadforge.cline-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_sessions: number;
  incomplete_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type ClineHistoryStatus = {
  schema_version: 'threadforge.cline-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  incomplete_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
};

export type ClineHistoryWatchResult = { sweeps: number; complete: boolean };

/** Status contains counts and source hashes only; native paths, task IDs and message text are omitted. */
export async function getClineHistoryStatus(
  options: Pick<ClineHistoryOptions, 'clineDir' | 'vscodeStorageDir' | 'statePath' | 'maxSessions'> = {},
): Promise<ClineHistoryStatus> {
  validateOptions(options);
  const roots = await resolveRoots(options);
  const statePath = resolveStatePath(options.statePath);
  assertStateOutsideSources(statePath, roots);
  const listed = await listSources(roots, options.maxSessions ?? defaultMaxSessions, false);
  const state = await readState(statePath);
  const errors = [...listed.errors];
  let checkpointed = 0;
  let incomplete = 0;
  let scannedBytes = listed.scannedBytes;
  for (const source of listed.sources) {
    const cursor = state.sources[sha256(source.key)];
    try {
      const parsed = await readSource(source);
      scannedBytes += parsed.bytesRead;
      if (scannedBytes > maxAggregateBytes) {
        listed.truncated = true;
        errors.push({ source_sha256: sha256(source.key), message: 'aggregate source read limit reached' });
        incomplete++;
        break;
      }
      if (source.kind === 'kanban') {
        const snapshotHash = mutableSnapshotHash(source.mutableRecords ?? []);
        if (cursor?.session_started && cursor.prefix_sha256 === snapshotHash) checkpointed++;
        else if (cursor) incomplete++;
        continue;
      }
      const records = parsed.entries ?? [];
      const conflict = Boolean(cursor && cursor.record_count > 0 &&
        (records.length < cursor.record_count || prefixDigest(records, cursor.record_count) !== cursor.prefix_sha256));
      if (conflict) {
        errors.push({ source_sha256: sha256(source.key), message: 'committed history prefix changed; review before resuming' });
        incomplete++;
      } else if (cursor?.session_started && cursor.record_count >= records.length) checkpointed++;
      else if (cursor) incomplete++;
    } catch (error) {
      errors.push({ source_sha256: sha256(source.key), message: safeError(error) });
      incomplete++;
    }
  }
  return {
    schema_version: 'threadforge.cline-history-status.v1', sessions: listed.sources.length,
    checkpointed_sessions: checkpointed, pending_sessions: Math.max(0, listed.sources.length - checkpointed),
    incomplete_sessions: incomplete, symlinks_skipped: listed.symlinksSkipped,
    oversized_files_skipped: listed.oversizedSkipped, sessions_truncated: listed.truncated, errors,
  };
}

/** Polls Cline's committed local history stores. Content is digest-only unless explicitly opted in. */
export async function syncClineHistory(options: ClineHistoryOptions = {}): Promise<ClineHistoryResult> {
  validateOptions(options);
  const roots = await resolveRoots(options);
  const statePath = resolveStatePath(options.statePath);
  assertStateOutsideSources(statePath, roots);
  const listed = await listSources(roots, options.maxSessions ?? defaultMaxSessions, options.includeContent ?? false);
  const result = emptyResult(options.includeContent ?? false);
  if (options.preview) result.events = [];
  result.sessions = listed.sources.length;
  result.sessions_truncated = listed.truncated;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.errors.push(...listed.errors);
  if (options.preview) {
    const state = await readState(statePath);
    await processSources(listed.sources, state, options, result, false, undefined, listed.scannedBytes);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processSources(listed.sources, state, options, result, true, statePath, listed.scannedBytes);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_sessions === 0 && result.incomplete_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated && !result.preview_truncated;
  return result;
}

export async function watchClineHistory(
  options: ClineHistoryOptions, intervalMs: number,
  onResult: (result: ClineHistoryResult) => void, signal: AbortSignal,
): Promise<ClineHistoryWatchResult> {
  if (!Number.isInteger(intervalMs) || intervalMs < 5_000 || intervalMs > 7 * 24 * 60 * 60 * 1_000) {
    throw new Error('--interval must be between 5s and 168h');
  }
  if (options.preview) throw new Error('--watch cannot be combined with --print');
  let sweeps = 0;
  let complete = true;
  while (!signal.aborted) {
    const result = await syncClineHistory(options);
    sweeps++;
    complete &&= result.complete;
    onResult(result);
    if (!signal.aborted) await waitForInterval(intervalMs, signal);
  }
  return { sweeps, complete };
}

function emptyResult(includeContent: boolean): ClineHistoryResult {
  return {
    schema_version: 'threadforge.cline-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_sessions: 0, incomplete_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: ClineHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? defaultMaxSessions, 1, maxSessionsLimit, '--max-sessions');
  if (options.clineDir !== undefined && !path.isAbsolute(options.clineDir)) throw new Error('--cline-dir must be an absolute path');
  if (options.vscodeStorageDir !== undefined && !path.isAbsolute(options.vscodeStorageDir)) throw new Error('--vscode-storage-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveRoots(options: Pick<ClineHistoryOptions, 'clineDir' | 'vscodeStorageDir'>): Promise<{ cline?: string; vscode?: string }> {
  const home = options.clineDir ?? (process.env.CLINE_HOME?.trim() || path.join(os.homedir(), '.cline'));
  if (!path.isAbsolute(home)) throw new Error('CLINE_HOME must be an absolute path');
  const cline = await canonicalOptionalDirectory(home, 'Cline directory');
  const vscodePath = options.vscodeStorageDir ?? defaultVSCodeStorageDir();
  if (vscodePath !== undefined && !path.isAbsolute(vscodePath)) throw new Error('--vscode-storage-dir must be an absolute path');
  const vscode = vscodePath ? await canonicalOptionalDirectory(vscodePath, 'Cline VS Code storage directory') : undefined;
  return { ...(cline ? { cline } : {}), ...(vscode ? { vscode } : {}) };
}

function defaultVSCodeStorageDir(): string | undefined {
  const home = os.homedir();
  if (process.platform === 'win32') {
    const base = process.env.APPDATA?.trim() || path.join(home, 'AppData', 'Roaming');
    return path.join(base, 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev');
  }
  if (process.platform === 'darwin') return path.join(home, 'Library', 'Application Support', 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev');
  return path.join(process.env.XDG_CONFIG_HOME?.trim() || path.join(home, '.config'), 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev');
}

async function canonicalOptionalDirectory(value: string, label: string): Promise<string | undefined> {
  const absolute = path.resolve(value);
  const info = await lstat(absolute).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return undefined;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} must be a real directory`);
  const canonical = await realpath(absolute).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, absolute)) throw new Error(`${label} must not traverse symbolic links`);
  return canonical;
}

type ListedSources = { sources: ClineSource[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean; scannedBytes: number; errors: ClineHistoryResult['errors'] };

async function listSources(roots: { cline?: string; vscode?: string }, maximum: number, retainKanbanContent: boolean): Promise<ListedSources> {
  const result: ListedSources = { sources: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false, scannedBytes: 0, errors: [] };
  const byID = new Map<string, ClineSource>();
  const addFile = async (source: ClineSource) => {
    const inspected = await inspectFile(source.filePath, source.root);
    if (inspected === 'symlink') { result.symlinksSkipped++; return; }
    if (inspected === 'oversized') { result.oversizedSkipped++; return; }
    if (!inspected) return;
    source.size = inspected.size;
    source.mtimeMs = inspected.mtimeMs;
    const key = `${source.kind}:${source.id}`;
    const previous = byID.get(key);
    if (!previous && byID.size >= maximum) { result.truncated = true; return; }
    if (!previous || source.mtimeMs > previous.mtimeMs) byID.set(key, source);
  };
  const scanTaskRoot = async (root: string, tasksPath: string, sourcePrefix: string) => {
    const entries = await safeReadDir(tasksPath, root, result);
    if (!entries) return;
    let scanned = 0;
    for (const entry of entries) {
      if (scanned >= maxTaskDirs) { result.truncated = true; break; }
      const taskPath = path.join(tasksPath, entry.name);
      const kind = await inspectPath(taskPath, root);
      if (kind === 'symlink') { result.symlinksSkipped++; continue; }
      if (kind !== 'directory' || !safeSegment(entry.name)) continue;
      scanned++;
      await addFile({ kind: 'history', id: entry.name, key: `${sourcePrefix}/${entry.name}`, root,
        filePath: path.join(taskPath, 'api_conversation_history.json'), size: 0, mtimeMs: 0 });
    }
  };
  if (roots.cline) {
    await scanTaskRoot(roots.cline, path.join(roots.cline, 'data', 'tasks'), 'data/tasks');
    await scanTaskRoot(roots.cline, path.join(roots.cline, 'tasks'), 'tasks');
    await scanMessagesRoot(roots.cline, path.join(roots.cline, 'data', 'sessions'), result, addFile);
    await scanKanbanRoot(roots.cline, path.join(roots.cline, 'kanban', 'workspaces'), result, byID, maximum, retainKanbanContent);
  }
  if (roots.vscode) await scanTaskRoot(roots.vscode, path.join(roots.vscode, 'tasks'), 'vscode/tasks');
  const sources = [...byID.values()].sort((a, b) => a.key.localeCompare(b.key));
  if (sources.length > maximum) { sources.length = maximum; result.truncated = true; }
  if (sources.length > maxFiles) { sources.length = maxFiles; result.truncated = true; }
  return { ...result, sources };
}

async function scanMessagesRoot(root: string, sessionsPath: string, result: ListedSources, addFile: (source: ClineSource) => Promise<void>): Promise<void> {
  const leads = await safeReadDir(sessionsPath, root, result);
  if (!leads) return;
  let scanned = 0;
  for (const lead of leads) {
    if (scanned >= maxTaskDirs) { result.truncated = true; break; }
    const leadPath = path.join(sessionsPath, lead.name);
    const leadKind = await inspectPath(leadPath, root);
    if (leadKind === 'symlink') { result.symlinksSkipped++; continue; }
    if (leadKind !== 'directory' || !safeSegment(lead.name)) continue;
    scanned++;
    const files = await safeReadDir(leadPath, root, result);
    if (!files) continue;
    for (const file of files) {
      if (!file.name.endsWith('.messages.json')) continue;
      const messageId = file.name.slice(0, -'.messages.json'.length);
      if (!safeSegment(messageId)) continue;
      const id = messageId === lead.name ? lead.name : `subagent:${lead.name}:${messageId}`;
      await addFile({ kind: 'messages', id, key: `data/sessions/${lead.name}/${file.name}`, root,
        filePath: path.join(leadPath, file.name), size: 0, mtimeMs: 0 });
    }
  }
}

async function scanKanbanRoot(
  root: string, workspacesPath: string, result: ListedSources, byID: Map<string, ClineSource>, maximum: number, retainContent: boolean,
): Promise<void> {
  const workspaces = await safeReadDir(workspacesPath, root, result);
  if (!workspaces) return;
  let scanned = 0;
  for (const workspace of workspaces) {
    if (scanned >= maxTaskDirs) { result.truncated = true; break; }
    const workspacePath = path.join(workspacesPath, workspace.name);
    const kind = await inspectPath(workspacePath, root);
    if (kind === 'symlink') { result.symlinksSkipped++; continue; }
    if (kind !== 'directory' || !safeSegment(workspace.name)) continue;
    scanned++;
    const sessionsPath = path.join(workspacePath, 'sessions.json');
    const boardPath = path.join(workspacePath, 'board.json');
    const sessionsInfo = await inspectFile(sessionsPath, root);
    if (sessionsInfo === 'symlink') { result.symlinksSkipped++; continue; }
    if (sessionsInfo === 'oversized') { result.oversizedSkipped++; continue; }
    if (!sessionsInfo) continue;
    const boardInfo = await inspectFile(boardPath, root);
    if (boardInfo === 'symlink') { result.symlinksSkipped++; continue; }
    if (boardInfo === 'oversized') { result.oversizedSkipped++; continue; }
    try {
      const bytes = sessionsInfo.size + (boardInfo?.size ?? 0);
      if (result.scannedBytes + bytes > maxAggregateBytes) {
        result.truncated = true;
        result.errors.push({ source_sha256: sha256(`kanban/${workspace.name}`), message: 'aggregate source read limit reached' });
        break;
      }
      result.scannedBytes += bytes;
      const sessions = asRecord(await readJsonFile(sessionsPath, root));
      const board = boardInfo ? asRecord(await readJsonFile(boardPath, root)) : {};
      let taskCount = 0;
      for (const taskId in sessions) {
        if (!Object.hasOwn(sessions, taskId)) continue;
        if (taskCount >= maxTaskDirs) { result.truncated = true; break; }
        const raw = sessions[taskId];
        if (!safeSegment(taskId) || taskId.startsWith('__home_agent__:')) continue;
        const session = asRecord(raw);
        if (!Object.keys(session).length) continue;
        taskCount++;
        const card = findKanbanCard(board, taskId);
        const modified = Math.max(sessionsInfo.mtimeMs, boardInfo?.mtimeMs ?? 0);
        const mutableRecords: MutableRecord[] = [];
        const prompt = typeof card.prompt === 'string' ? card.prompt : '';
        const activity = asRecord(session.latestHookActivity);
        const summary = firstText(activity.finalMessage, activity.activityText, kanbanStateSummary(session));
        const warning = firstText(session.warningMessage, kanbanFailureSummary(session));
        const model = safeLabel(firstText(session.modelId, session.model));
        const created = timestampMs(session.startedAt ?? card.createdAt, modified);
        const updated = timestampMs(session.updatedAt ?? session.lastOutputAt ?? card.updatedAt ?? card.createdAt, modified);
        if (prompt.trim()) mutableRecords.push(makeMutableRecord('prompt', 'user', prompt, created, model, retainContent));
        if (summary.trim()) mutableRecords.push(makeMutableRecord('assistant', 'assistant', summary, updated, model, retainContent));
        if (warning.trim()) mutableRecords.push(makeMutableRecord('error', 'error', warning, updated, model, retainContent));
        const sourceKey = `kanban/${workspace.name}/${taskId}`;
        const source: ClineSource = { kind: 'kanban', id: `${workspace.name}:${taskId}`, key: sourceKey, root,
          filePath: sessionsPath, size: 0, mtimeMs: modified, mutableRecords };
        const mapKey = `kanban:${source.id}`;
        const previous = byID.get(mapKey);
        if (!previous && byID.size >= maximum) { result.truncated = true; break; }
        if (!previous || source.mtimeMs > previous.mtimeMs) byID.set(mapKey, source);
      }
    } catch {
      result.errors.push({ source_sha256: sha256(`kanban/${workspace.name}`), message: 'kanban state could not be parsed safely' });
    }
  }
}

async function safeReadDir(directory: string, root: string, result: ListedSources) {
  const kind = await inspectPath(directory, root);
  if (!kind || kind === 'missing') return undefined;
  if (kind === 'symlink') { result.symlinksSkipped++; return undefined; }
  if (kind !== 'directory') return undefined;
  return readdir(directory, { withFileTypes: true }).catch(() => {
    result.errors.push({ source_sha256: sha256(path.relative(root, directory)), message: 'source directory could not be enumerated' });
    return undefined;
  });
}

async function inspectPath(filePath: string, root: string): Promise<'file' | 'directory' | 'symlink' | 'missing' | undefined> {
  const relative = path.relative(root, filePath);
  if (path.isAbsolute(relative) || relative === '..' || relative.startsWith(`..${path.sep}`)) return 'symlink';
  const components = relative ? relative.split(path.sep) : [];
  let current = root;
  let info: Awaited<ReturnType<typeof lstat>> | null = null;
  for (const component of components) {
    current = path.join(current, component);
    info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return 'missing';
    if (info.isSymbolicLink()) return 'symlink';
  }
  if (!info) {
    info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return 'missing';
    if (info.isSymbolicLink()) return 'symlink';
  }
  const canonical = await realpath(filePath).catch(() => '');
  if (!canonical || !isWithin(root, canonical)) return 'symlink';
  if (info.isDirectory()) return 'directory';
  if (info.isFile()) return 'file';
  return undefined;
}

async function inspectFile(filePath: string, root: string): Promise<{ size: number; mtimeMs: number } | 'symlink' | 'oversized' | undefined> {
  const kind = await inspectPath(filePath, root);
  if (kind === 'missing' || kind === undefined || kind === 'directory') return undefined;
  if (kind === 'symlink') return 'symlink';
  const info = await lstat(filePath).catch(() => null);
  if (!info || !info.isFile() || info.isSymbolicLink()) return 'symlink';
  if (info.size > maxFileBytes) return 'oversized';
  return { size: info.size, mtimeMs: info.mtimeMs };
}

async function readSource(source: ClineSource): Promise<{ entries?: Entry[]; bytesRead: number }> {
  if (source.kind === 'kanban') return { bytesRead: source.size };
  const data = await readFileSafely(source.filePath, source.root, source.size, source.mtimeMs);
  let parsed: unknown;
  try { parsed = parseBoundedJson(new TextDecoder('utf-8', { fatal: true }).decode(data)); }
  catch { throw new Error('history JSON is incomplete or malformed'); }
  const values = historyEntries(parsed);
  if (!values) throw new Error('unsupported Cline history schema');
  if (values.length > maxRecords) throw new Error('history exceeds the per-session record limit');
  if (values.some((value) => !isRecord(value))) throw new Error('history contains an unsupported record');
  const entries = values.map((value, index) => ({ index: index + 1, value: value as JsonObject, digest: sha256(stableJson(value)) }));
  return { entries, bytesRead: data.byteLength };
}

async function readFileSafely(filePath: string, root: string, expectedSize: number, expectedMtime: number): Promise<Buffer> {
  const before = await lstat(filePath).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes) throw new Error('history is not a bounded regular file');
  if (before.size !== expectedSize || before.mtimeMs !== expectedMtime) throw new Error('history changed after enumeration');
  const canonical = await realpath(filePath).catch(() => '');
  if (!canonical || !isWithin(root, canonical)) throw new Error('history path escaped its source directory');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || opened.size !== before.size || opened.mtimeMs !== before.mtimeMs) throw new Error('history changed before reading');
    const bytes = await readHandleBounded(handle, maxFileBytes);
    const after = await handle.stat();
    const afterPath = await lstat(filePath).catch(() => null);
    if (bytes.byteLength !== opened.size || bytes.byteLength > maxFileBytes || !sameFile(opened, after) || !sameFile(opened, afterPath) ||
        after.size !== opened.size || after.mtimeMs !== opened.mtimeMs) throw new Error('history changed during reading');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('history')) throw error;
    throw new Error('history could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

function historyEntries(parsed: unknown): unknown[] | undefined {
  if (Array.isArray(parsed)) return parsed;
  const object = asRecord(parsed);
  for (const key of ['messages', 'history', 'entries', 'apiConversationHistory', 'conversationHistory', 'conversation_history']) {
    if (Array.isArray(object[key])) return object[key] as unknown[];
  }
  return undefined;
}

async function processSources(
  sources: ClineSource[], state: CursorState, options: ClineHistoryOptions,
  result: ClineHistoryResult, persist: boolean, statePath?: string, initialBytes = 0,
): Promise<void> {
  let totalBytes = initialBytes;
  let totalEvents = 0;
  for (const source of sources) {
    if (totalEvents >= maxEventsPerSync) {
      result.sessions_truncated = true;
      result.errors.push({ source_sha256: sha256(source.key), message: 'per-sync event limit reached; remaining sessions were not read' });
      break;
    }
    const cursorKey = sha256(source.key);
    const cursor = state.sources[cursorKey] ?? { record_count: 0, prefix_sha256: emptyPrefixDigest(), session_started: false };
    try {
      const parsed = await readSource(source);
      totalBytes += parsed.bytesRead;
      if (totalBytes > maxAggregateBytes) {
        result.sessions_truncated = true;
        result.errors.push({ source_sha256: cursorKey, message: 'aggregate source read limit reached' });
        break;
      }
      const beforeCount = result.events_emitted + result.events_previewed;
      if (source.kind === 'kanban') {
        const groups = mapMutableSource(source, cursor, options.includeContent ?? false);
        const mappedEvents = groups.reduce((count, group) => count + group.events.length, 0);
        if (mappedEvents > maxEventsPerSession) throw new Error('mapped history exceeds the per-session event limit');
        if (totalEvents + mappedEvents > maxEventsPerSync) throw new Error('per-sync event limit reached before this session');
        await deliverGroups(source, groups, cursor, state, cursorKey, options, result, persist, statePath);
        const latestHash = mutableSnapshotHash(source.mutableRecords ?? []);
        if (cursor.prefix_sha256 !== latestHash) {
          cursor.prefix_sha256 = latestHash;
          cursor.mutable_hashes = Object.fromEntries((source.mutableRecords ?? []).map((item) => [item.field, hashMutable(item)]));
          if (persist && statePath) await writeState(statePath, state);
        }
      } else {
        const entries = parsed.entries ?? [];
        const conflict = cursor.record_count > 0 && (entries.length < cursor.record_count ||
          prefixDigest(entries, cursor.record_count) !== cursor.prefix_sha256);
        if (conflict) {
          result.incomplete_sessions++;
          result.errors.push({ source_sha256: cursorKey, message: 'committed history prefix changed; review before resuming' });
          continue;
        }
        const groups = mapHistorySource(source, entries, cursor, options.includeContent ?? false);
        const mappedEvents = groups.reduce((count, group) => count + group.events.length, 0);
        if (mappedEvents > maxEventsPerSession) throw new Error('mapped history exceeds the per-session event limit');
        if (totalEvents + mappedEvents > maxEventsPerSync) throw new Error('per-sync event limit reached before this session');
        await deliverGroups(source, groups, cursor, state, cursorKey, options, result, persist, statePath,
          (recordCount) => prefixDigest(entries, recordCount));
        if (cursor.record_count < entries.length) {
          cursor.record_count = entries.length;
          cursor.prefix_sha256 = prefixDigest(entries, entries.length);
          state.sources[cursorKey] = cursor;
          if (persist && statePath) await writeState(statePath, state);
        }
      }
      const emitted = result.events_emitted + result.events_previewed - beforeCount;
      if (emitted > 0) result.sessions_changed++;
    } catch (error) {
      const message = safeError(error);
      if (/malformed|unsupported Cline|unsupported record/i.test(message)) result.malformed_sessions++;
      result.incomplete_sessions++;
      result.errors.push({ source_sha256: cursorKey, message });
    }
    totalEvents = result.events_emitted + result.events_previewed;
  }
}

async function deliverGroups(
  source: ClineSource, groups: EventGroup[], cursor: Cursor, state: CursorState, cursorKey: string,
  options: ClineHistoryOptions, result: ClineHistoryResult, persist: boolean, statePath?: string,
  prefixAt?: (recordCount: number) => string,
): Promise<void> {
  const candidates = groups.filter((group) => group.record === 0 ? !cursor.session_started :
    source.kind === 'kanban' ? true : group.record > cursor.record_count);
  if (options.preview) {
    for (const group of candidates) for (const event of group.events) {
      if ((result.events?.length ?? 0) >= maxPreviewEvents) { result.preview_truncated = true; continue; }
      result.events?.push(event);
      result.events_previewed++;
    }
    return;
  }
  let batch: ThreadforgeEvent[] = [];
  let checkpointGroup: EventGroup | undefined;
  let batchGroups: EventGroup[] = [];
  const flush = async () => {
    if (batch.length > 0) {
      await emitEvents(batch, { endpoint: options.endpoint, fetchImpl: options.fetchImpl, timeoutMs: 1500 });
      result.events_emitted += batch.length;
    }
    if (checkpointGroup) {
      for (const completed of batchGroups) {
        if (completed.startsSession || completed.record === 0) cursor.session_started = true;
        if (source.kind === 'kanban' && completed.field) {
          const item = (source.mutableRecords ?? []).find((record) => record.field === completed.field);
          if (item) {
            cursor.mutable_hashes ??= {};
            cursor.mutable_hashes[item.field] = hashMutable(item);
          }
        } else if (source.kind !== 'kanban' && completed.record > cursor.record_count) {
          cursor.record_count = completed.record;
          if (prefixAt) cursor.prefix_sha256 = prefixAt(cursor.record_count);
        }
      }
      state.sources[cursorKey] = cursor;
      if (persist && statePath) await writeState(statePath, state);
      batch = [];
      batchGroups = [];
      checkpointGroup = undefined;
    }
  };
  for (const group of candidates) {
    if (group.events.length === 0) {
      checkpointGroup = group;
      batchGroups.push(group);
      continue;
    }
    if (batch.length + group.events.length > batchSize && batch.length > 0) await flush();
    if (group.events.length > batchSize) throw new Error('mapped record exceeds the intake batch limit');
    batch.push(...group.events);
    checkpointGroup = group;
    batchGroups.push(group);
    if (batch.length >= batchSize) await flush();
  }
  if (batch.length > 0 || checkpointGroup) await flush();
}

function mapHistorySource(source: ClineSource, entries: Entry[], cursor: Cursor, includeContent: boolean): EventGroup[] {
  const groups: EventGroup[] = [];
  if (!cursor.session_started) groups.push({ record: 0, startsSession: true, events: [sessionStarted(source, source.mtimeMs)] });
  const calls = new Map<string, { name: string; args: JsonObject }>();
  for (const entry of entries) {
    const events: ThreadforgeEvent[] = [];
    const role = String(entry.value.role ?? '').toLowerCase().trim();
    if (role !== 'user' && role !== 'assistant') continue;
    const model = safeLabel(firstText(entry.value.modelId, entry.value.model_id, entry.value.model,
      asRecord(entry.value.modelInfo).modelId, asRecord(entry.value.model_info).modelId));
    const ts = timestampMs(entry.value.ts ?? entry.value.timestamp ?? entry.value.time, source.mtimeMs);
    const blocks = normalizeBlocks(entry.value.content ?? entry.value.text ?? entry.value.message);
    for (let partIndex = 0; partIndex < blocks.length; partIndex++) {
      const block = blocks[partIndex];
      if (typeof block === 'string') {
        const text = visibleText(role, block);
        if (text) events.push(textEvent(source, entry, partIndex, role === 'user' ? 'prompt.submitted' : 'agent.message', text, ts, model, includeContent));
        continue;
      }
      if (!isRecord(block)) continue;
      const kind = String(block.type ?? 'text').toLowerCase();
      if (kind === 'text' || !block.type) {
        const text = visibleText(role, firstText(block.text, block.content, block.value, block.message));
        if (text) events.push(textEvent(source, entry, partIndex, role === 'user' ? 'prompt.submitted' : 'agent.message', text, ts, model, includeContent));
      } else if (kind === 'thinking' || kind === 'reasoning' || kind === 'redacted_thinking') {
        const text = firstText(block.thinking, block.text, block.content);
        if (role === 'assistant' && text.trim()) events.push(textEvent(source, entry, partIndex, 'agent.reasoning', text, ts, model, false));
      } else if (['tool_use', 'tool-call', 'tool_call'].includes(kind)) {
        const callId = firstText(block.id, block.tool_use_id, block.callId, `entry-${entry.index}-part-${partIndex}`);
        const name = safeLabel(firstText(block.name, block.toolName, 'unknown')) ?? 'unknown';
        const args = asRecord(block.input ?? block.args ?? block.arguments);
        calls.set(callId, { name, args });
        events.push(toolEvent(source, entry, partIndex, 'tool.invoked', name, callId, args, undefined, false, ts, model));
      } else if (['tool_result', 'tool-result', 'tool_response'].includes(kind)) {
        const callId = firstText(block.tool_use_id, block.callId, block.id, `entry-${entry.index}-part-${partIndex}`);
        const prior = calls.get(callId);
        const name = safeLabel(firstText(prior?.name, block.name, block.toolName, 'unknown')) ?? 'unknown';
        const args = prior?.args ?? {};
        const output = block.content ?? block.output ?? block.result;
        const failed = block.is_error === true || block.isError === true || String(block.status ?? '').toLowerCase() === 'error';
        events.push(toolEvent(source, entry, partIndex, failed ? 'tool.failed' : classifyTool(name), name, callId, args, output, failed, ts, model));
        calls.delete(callId);
      }
    }
    const usage = usageFields(entry.value.metrics ?? entry.value.usage);
    if (Object.keys(usage).length) {
      const target = [...events].reverse().find((event) => event.event.action === 'agent.message' || event.event.action === 'agent.reasoning' || event.event.action === 'tool.invoked');
      if (target) target.gen_ai = { ...asRecord(target.gen_ai), usage };
    }
    if (events.length) groups.push({ record: entry.index, events });
  }
  return groups;
}

function mapMutableSource(source: ClineSource, cursor: Cursor, includeContent: boolean): EventGroup[] {
  const groups: EventGroup[] = [];
  if (!cursor.session_started) groups.push({ record: 0, startsSession: true, events: [sessionStarted(source, source.mtimeMs)] });
  const known = cursor.mutable_hashes ?? {};
  for (let index = 0; index < (source.mutableRecords ?? []).length; index++) {
    const item = source.mutableRecords![index]!;
    const digest = hashMutable(item);
    if (known[item.field] === digest) continue;
    const event = item.role === 'error'
      ? baseEvent(source, 'session.error', 'error', item.timestampMs, index, `kanban:${item.field}:${digest}`, item.model, 'Cline session reported an error', 'high')
      : textEventFromDigest(source, index + 1, index, item.role === 'user' ? 'prompt.submitted' : 'agent.message',
        item.content_sha256, item.content_bytes, item.text, item.timestampMs, item.model, includeContent);
    if (item.role === 'error') event.content = digestValue(item.content_sha256, item.content_bytes, false);
    event.event.id = sha256(`${source.key}\0kanban\0${item.field}\0${digest}`);
    groups.push({ record: index + 1, field: item.field, events: [event] });
  }
  return groups;
}

function sessionStarted(source: ClineSource, timestamp: number): ThreadforgeEvent {
  return baseEvent(source, 'session.started', 'session', timestamp, 0, 'session.started', undefined, 'Cline session discovered');
}

function textEvent(
  source: ClineSource, entry: Entry, part: number, action: 'prompt.submitted' | 'agent.message' | 'agent.reasoning',
  text: string, timestamp: number, model: string | undefined, includeContent: boolean,
): ThreadforgeEvent {
  return textEventFromDigest(source, entry.index, part, action, sha256(text), Buffer.byteLength(text, 'utf8'), text, timestamp, model, includeContent);
}

function textEventFromDigest(
  source: ClineSource, record: number, part: number, action: 'prompt.submitted' | 'agent.message' | 'agent.reasoning',
  contentHash: string, contentBytes: number, text: string | undefined, timestamp: number, model: string | undefined,
  includeContent: boolean,
): ThreadforgeEvent {
  const event = baseEvent(source, action, action === 'prompt.submitted' ? 'prompt' : action === 'agent.reasoning' ? 'reasoning' : 'session',
    timestamp, record, `${part}:${action}`, model, action === 'prompt.submitted' ? 'Cline prompt observed' : action === 'agent.reasoning' ? 'Cline reasoning observed' : 'Cline agent message observed');
  const retain = includeContent && action !== 'agent.reasoning' && text !== undefined && contentBytes <= maxContentBytes;
  event.content = digestValue(contentHash, contentBytes, retain, includeContent && !retain, retain ? text : undefined);
  if (action === 'prompt.submitted' && retain) event.prompt = { text };
  if (retain) {
    const message = { role: action === 'prompt.submitted' ? 'user' : 'assistant', parts: [{ type: 'text', content: text }] };
    event.gen_ai = action === 'prompt.submitted' ? { input: { messages: [message] } } : { output: { messages: [message] } };
  }
  return event;
}

function toolEvent(
  source: ClineSource, entry: Entry, part: number, action: string, name: string, callId: string,
  args: JsonObject, output: unknown, failed: boolean, timestamp: number, model?: string,
): ThreadforgeEvent {
  const event = baseEvent(source, action, action === 'mcp.tool_invoked' ? 'mcp' : action.startsWith('file.') ? 'file' : action === 'command.executed' ? 'command' : 'tool',
    timestamp, entry.index, `${part}:${action}:${name}:${callId}`, model, failed ? 'Cline tool failed' : 'Cline tool activity observed', failed ? 'high' : 'info');
  event.tool = { name };
  const callHash = sha256(callId);
  event.gen_ai = { tool: { call: { id: `cline-call:${callHash}` } } };
  const inputDigest = digestContent(args, false);
  if (Object.keys(args).length) event.content = inputDigest;
  if (action === 'command.executed') {
    const command = firstText(args.command, args.cmd);
    const response = asRecord(output);
    const outputText = firstText(response.output, response.stdout, response.text, typeof output === 'string' ? output : '');
    const exitCode = response.exit_code ?? response.exitCode ?? args.exit_code ?? args.exitCode;
    event.command = {
      ...(command ? { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command) } : {}),
      ...(Number.isSafeInteger(exitCode) ? { exit_code: exitCode } : {}),
      ...(outputText ? { output_sha256: sha256(outputText), output_bytes: Buffer.byteLength(outputText) } : {}),
    };
  }
  if (action === 'file.read' || action === 'file.modified') {
    const filePath = firstText(args.path, args.file_path, args.filePath, args.target_file, args.targetFile);
    if (filePath) event.file = { path_sha256: sha256(filePath), operation: action === 'file.read' ? 'read' : /write|create/i.test(name) ? 'create' : 'modify' };
  }
  if (action === 'mcp.tool_invoked') {
    const server = safeLabel(firstText(args.serverName, args.server_name, args.server));
    const tool = safeLabel(firstText(args.toolName, args.tool_name, args.tool, args.resource));
    event.mcp = { ...(server ? { server } : {}), ...(tool ? { tool } : {}) };
  }
  if (output !== undefined) event.raw = { ...asRecord(event.raw), result_content: digestContent(output, false) };
  if (failed) event.error = { type: 'tool_error' };
  return event;
}

function classifyTool(name: string): string {
  const lower = name.toLowerCase();
  if (['list_files', 'search_files', 'list_code_definition_names', 'read_file'].includes(lower) || lower.includes('read')) return 'file.read';
  if (lower === 'use_mcp_tool' || lower === 'access_mcp_resource' || lower.includes('mcp')) return 'mcp.tool_invoked';
  if (/command|shell|terminal|bash/.test(lower)) return 'command.executed';
  if (/write|edit|replace|patch|create/.test(lower)) return 'file.modified';
  return 'tool.completed';
}

function baseEvent(
  source: ClineSource, action: string, category: string, timestamp: number, record: number, suffix: string,
  model: string | undefined, message: string, severity: ThreadforgeEvent['severity'] = 'info',
): ThreadforgeEvent {
  return {
    timestamp: new Date(timestamp > 0 ? timestamp : Date.now()).toISOString(), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${source.key}\0${record}\0${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'cline', collection_method: 'poll' },
    session: { id: `cline:${sha256(source.key)}` },
    ...(model ? { model } : {}),
    raw: { source_sha256: sha256(source.key), source_kind: source.kind, record_index: record, message },
  };
}

function usageFields(value: unknown): JsonObject {
  const usage = asRecord(value);
  const result: JsonObject = {};
  const addInt = (target: string, ...keys: string[]) => {
    const num = firstNumber(usage, keys);
    if (num !== undefined && Number.isSafeInteger(num) && num >= 0) result[target] = num;
  };
  addInt('input_tokens', 'inputTokens', 'input_tokens', 'tokensIn', 'promptTokens', 'prompt_tokens');
  addInt('output_tokens', 'outputTokens', 'output_tokens', 'tokensOut', 'completionTokens', 'completion_tokens');
  addInt('reasoning_tokens', 'thoughtsTokenCount', 'reasoningTokenCount', 'reasoningTokens');
  const cacheRead = firstNumber(usage, ['cacheReadTokens', 'cache_read_tokens', 'cachedTokens']);
  const cacheWrite = firstNumber(usage, ['cacheWriteTokens', 'cache_write_tokens', 'cacheCreationTokens']);
  if (cacheRead !== undefined && Number.isSafeInteger(cacheRead) && cacheRead >= 0) result.cache_read = { input_tokens: cacheRead };
  if (cacheWrite !== undefined && Number.isSafeInteger(cacheWrite) && cacheWrite >= 0) result.cache_creation = { input_tokens: cacheWrite };
  const cost = firstNumber(usage, ['totalCost', 'total_cost', 'costUsd', 'cost_usd', 'cost']);
  if (cost !== undefined && Number.isFinite(cost) && cost >= 0 && cost <= 1_000_000_000) result.cost_usd = cost;
  return result;
}

function visibleText(role: string, value: string): string {
  const text = value.trim();
  if (!text) return '';
  if (role === 'assistant') return text;
  for (const tag of ['user_message', 'user_input', 'feedback', 'task']) {
    const escaped = tag.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const match = text.match(new RegExp(`<${escaped}[^>]*>([\\s\\S]*?)</${escaped}>`, 'i'));
    if (match?.[1]?.trim()) return match[1].trim();
  }
  if (text.includes('<environment_details>') || text.startsWith('# task_progress') || text.startsWith('[ERROR] You did not use a tool') ||
      text.startsWith('New instructions for task continuation:') || text.startsWith('Command executed.\nOutput:') ||
      text.startsWith('The user has provided feedback on the results.')) return '';
  return text;
}

function normalizeBlocks(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (value === undefined || value === null || value === '') return [];
  return [value];
}

function normalizeOutput(value: unknown): string {
  if (typeof value === 'string') return value;
  try { return stableJson(value); } catch { return ''; }
}

function digestContent(value: unknown, include: boolean): JsonObject {
  const text = typeof value === 'string' ? value : normalizeOutput(value);
  const bytes = Buffer.byteLength(text, 'utf8');
  const retain = include && bytes <= maxContentBytes;
  return digestValue(sha256(text), bytes, retain, include && !retain, retain ? text : undefined);
}

function mutableSnapshotHash(records: MutableRecord[]): string { return sha256(stableJson(records.map((item) => ({ field: item.field, digest: hashMutable(item) })))); }
function hashMutable(value: MutableRecord): string { return sha256(stableJson({ field: value.field, role: value.role, content_sha256: value.content_sha256, content_bytes: value.content_bytes, model: value.model })); }
function makeMutableRecord(field: string, role: MutableRecord['role'], text: string, timestampMs: number, model: string | undefined, retain: boolean): MutableRecord {
  const content_bytes = Buffer.byteLength(text, 'utf8');
  return { field, role, content_sha256: sha256(text), content_bytes, timestampMs, model, ...(retain && content_bytes <= maxContentBytes ? { text } : {}) };
}
function digestValue(hash: string, bytes: number, included: boolean, truncated = false, text?: string): JsonObject {
  return { retention: 'metadata', included, hash, bytes, ...(truncated ? { truncated: true } : {}), ...(included && text !== undefined ? { text } : {}) };
}

function prefixDigest(entries: Entry[], count: number): string {
  return sha256(entries.slice(0, count).map((entry) => entry.digest).join('\n'));
}
function emptyPrefixDigest(): string { return sha256(''); }

async function readJsonFile(filePath: string, root: string): Promise<unknown> {
  const info = await inspectFile(filePath, root);
  if (!info || info === 'symlink' || info === 'oversized') throw new Error('kanban JSON is unavailable or unsafe');
  const bytes = await readFileSafely(filePath, root, info.size, info.mtimeMs);
  try { return parseBoundedJson(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new Error('kanban JSON is malformed'); }
}

function findKanbanCard(board: JsonObject, taskId: string): JsonObject {
  const columns = Array.isArray(board.columns) ? board.columns : [];
  for (const columnValue of columns) {
    const column = asRecord(columnValue);
    const cards = Array.isArray(column.cards) ? column.cards : [];
    for (const cardValue of cards) {
      const card = asRecord(cardValue);
      if (card.id === taskId) return card;
    }
  }
  return {};
}

function kanbanStateSummary(session: JsonObject): string {
  const rawState = firstText(session.state).replaceAll('_', ' ');
  if (!rawState) return '';
  const state = rawState[0]!.toUpperCase() + rawState.slice(1);
  const reason = firstText(session.reviewReason);
  const agent = firstText(session.agentId);
  return [state, reason ? `(${reason})` : '', agent ? `via ${agent}` : ''].filter(Boolean).join(' ');
}
function kanbanFailureSummary(session: JsonObject): string {
  const state = firstText(session.state).toLowerCase();
  const reason = firstText(session.reviewReason).toLowerCase();
  if (state === 'failed' || reason === 'error') return 'Cline session failed';
  if (state === 'interrupted' || reason === 'interrupted') return 'Cline session interrupted';
  return '';
}

function timestampMs(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) return value < 1e12 ? Math.trunc(value * 1_000) : Math.trunc(value);
  if (typeof value === 'string') {
    const numeric = Number(value);
    if (Number.isFinite(numeric) && numeric > 0) return numeric < 1e12 ? Math.trunc(numeric * 1_000) : Math.trunc(numeric);
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function stableJson(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return `[${value.map(stableJson).join(',')}]`;
  const object = value as JsonObject;
  return `{${Object.keys(object).sort().map((key) => `${JSON.stringify(key)}:${stableJson(object[key])}`).join(',')}}`;
}

function parseBoundedJson(text: string, maxDepth = 64): unknown {
  let depth = 0;
  let quoted = false;
  let escaped = false;
  for (const char of text) {
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') quoted = false;
      continue;
    }
    if (char === '"') quoted = true;
    else if (char === '{' || char === '[') {
      depth++;
      if (depth > maxDepth) throw new Error('JSON nesting depth limit exceeded');
    } else if (char === '}' || char === ']') depth--;
  }
  return JSON.parse(text);
}

function resolveStatePath(value?: string): string {
  const home = process.env.THREADFORGE_HOME?.trim() || path.join(os.homedir(), '.threadforge');
  const candidate = value ?? path.join(home, 'state', 'cline-sessions.v1.json');
  if (!path.isAbsolute(candidate)) throw new Error('--state-path must be an absolute path');
  return path.resolve(candidate);
}

function assertStateOutsideSources(statePath: string, roots: { cline?: string; vscode?: string }): void {
  for (const root of [roots.cline, roots.vscode]) if (root && isWithin(root, statePath)) {
    throw new Error('Cline session cursor must be outside native Cline and VS Code source roots');
  }
}

async function readState(statePath: string): Promise<CursorState> {
  const initial: CursorState = { schema_version: stateSchema, version: 1, sources: {} };
  await rejectSymlinkParents(path.dirname(statePath));
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return initial;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Cline cursor must be a bounded regular file');
  let handle: FileHandle | undefined;
  let data: Buffer;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened) || opened.size > maxStateBytes || opened.size !== info.size) throw new Error('Cline cursor changed before reading');
    data = await readHandleBounded(handle, maxStateBytes);
    const after = await handle.stat();
    const afterPath = await lstat(statePath).catch(() => null);
    if (data.byteLength !== opened.size || !sameFile(opened, after) || !sameFile(opened, afterPath) ||
        after.size !== opened.size || after.mtimeMs !== opened.mtimeMs) throw new Error('Cline cursor changed during reading');
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Cline cursor')) throw error;
    throw new Error('Cline cursor could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
  let value: unknown;
  try { value = parseBoundedJson(new TextDecoder('utf-8', { fatal: true }).decode(data)); } catch { throw new Error('Cline cursor state is malformed'); }
  const state = asRecord(value);
  if (!hasExactKeys(state, ['schema_version', 'version', 'sources']) || state.schema_version !== stateSchema || state.version !== 1 ||
      !isRecord(state.sources) || Object.keys(state.sources).length > maxSessionsLimit) throw new Error('Cline cursor state has an unsupported schema');
  for (const [sourceHash, cursorValue] of Object.entries(state.sources)) {
    if (!/^[a-f0-9]{64}$/.test(sourceHash)) throw new Error('Cline cursor state contains an invalid source key');
    const cursor = asRecord(cursorValue);
    if (!hasExactKeys(cursor, ['record_count', 'prefix_sha256', 'session_started'], ['mutable_hashes']) ||
        !Number.isSafeInteger(cursor.record_count) || Number(cursor.record_count) < 0 || Number(cursor.record_count) > maxRecords || typeof cursor.prefix_sha256 !== 'string' ||
        !/^[a-f0-9]{64}$/.test(cursor.prefix_sha256) || typeof cursor.session_started !== 'boolean' ||
        (cursor.mutable_hashes !== undefined && (!isRecord(cursor.mutable_hashes) ||
          Object.entries(cursor.mutable_hashes).some(([key, hash]) => !['prompt', 'assistant', 'error'].includes(key) || typeof hash !== 'string' || !/^[a-f0-9]{64}$/.test(hash))))) {
      throw new Error('Cline cursor state contains an invalid checkpoint');
    }
  }
  return value as CursorState;
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const parent = path.dirname(statePath);
  await mkdir(parent, { recursive: true, mode: 0o700 });
  await rejectSymlinkParents(parent);
  const current = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (current && (!current.isFile() || current.isSymbolicLink())) throw new Error('Cline cursor target must be a regular file');
  const temp = `${statePath}.${randomUUID()}.tmp`;
  const payload = Buffer.from(JSON.stringify(state), 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('Cline cursor state exceeds its size limit');
  let handle: FileHandle | undefined;
  try {
    handle = await open(temp, 'wx', 0o600);
    await handle.writeFile(payload);
    await handle.sync();
  } catch (error) {
    await unlink(temp).catch(() => {});
    throw error;
  } finally { await handle?.close().catch(() => {}); }
  try { await rename(temp, statePath); } catch (error) { await unlink(temp).catch(() => {}); throw error; }
}

async function readHandleBounded(handle: FileHandle, maxBytes: number): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  let position = 0;
  while (true) {
    const remaining = maxBytes + 1 - total;
    if (remaining <= 0) throw new Error('bounded file size limit exceeded');
    const buffer = Buffer.allocUnsafe(Math.min(64 << 10, remaining));
    const { bytesRead } = await handle.read(buffer, 0, buffer.byteLength, position);
    if (bytesRead === 0) break;
    total += bytesRead;
    if (total > maxBytes) throw new Error('bounded file size limit exceeded');
    chunks.push(buffer.subarray(0, bytesRead));
    position += bytesRead;
  }
  return Buffer.concat(chunks, total);
}

function hasExactKeys(value: JsonObject, required: string[], optional: string[] = []): boolean {
  const keys = Object.keys(value);
  return required.every((key) => Object.hasOwn(value, key)) && keys.every((key) => required.includes(key) || optional.includes(key));
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  const parent = path.dirname(statePath);
  await mkdir(parent, { recursive: true, mode: 0o700 });
  await rejectSymlinkParents(parent);
  const lockPath = `${statePath}.lock`;
  let lock;
  for (let attempt = 0; attempt < 100; attempt++) {
    try { lock = await open(lockPath, 'wx', 0o600); break; }
    catch (error) { if (!isCode(error, 'EEXIST')) throw error; await new Promise((resolve) => setTimeout(resolve, 50)); }
  }
  if (!lock) throw new Error('Cline history sync could not acquire its state lock');
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(lockPath).catch(() => {}); }
}

async function rejectSymlinkParents(directory: string): Promise<void> {
  const absolute = path.resolve(directory);
  const root = path.parse(absolute).root;
  let current = root;
  for (const component of absolute.slice(root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    const info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return;
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error('Cline cursor path must not traverse symlinks or non-directory parents');
  }
}

function waitForInterval(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) { resolve(); return; }
    const done = () => { clearTimeout(timer); signal.removeEventListener('abort', done); resolve(); };
    const timer = setTimeout(done, ms);
    signal.addEventListener('abort', done, { once: true });
  });
}

function safeError(error: unknown): string {
  const message = error instanceof Error ? error.message : '';
  if (/changed|prefix|malformed|schema|limit|bounded|regular|unsafe|escaped|could not|unsupported/i.test(message)) return message.slice(0, 180);
  return 'source read failed';
}
function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean { return right !== null && left.dev === right.dev && left.ino === right.ino; }
function isWithin(root: string, value: string): boolean { const relative = path.relative(root, value); return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`)); }
function safeSegment(value: string): boolean { return value !== '' && value !== '.' && value !== '..' && !value.includes('/') && !value.includes('\\'); }
function boundedInteger(value: number, min: number, max: number, name: string): number { if (!Number.isInteger(value) || value < min || value > max) throw new Error(`${name} must be between ${min} and ${max}`); return value; }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isCode(error: unknown, code: string): boolean { return isRecord(error) && error.code === code; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function asRecord(value: unknown): JsonObject { return isRecord(value) ? value : {}; }
function firstText(...values: unknown[]): string { for (const value of values) if (typeof value === 'string' && value.trim()) return value; return ''; }
function firstNumber(record: JsonObject, keys: string[]): number | undefined { for (const key of keys) if (typeof record[key] === 'number') return record[key] as number; return undefined; }
function safeLabel(value: string): string | undefined { const label = value.trim().replace(/[\r\n\0]/g, '').slice(0, 128); return label && !/(?:api[_-]?key|token|secret|password|bearer)\s*[:=]/i.test(label) ? label : undefined; }
function normalizedOS(value: string): string { return value === 'win32' ? 'windows' : value === 'darwin' ? 'macos' : value === 'linux' ? 'linux' : 'other'; }
