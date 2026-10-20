import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const cursorVersion = 1;
const cursorName = 'fx-session-cursor.v1.json';
const maxCursorBytes = 8 << 20;
const defaultMaxSessions = 1_000;
const maxSessions = 5_000;
const maxManifestBytes = 1 << 20;
const maxLogBytes = 64 << 20;
const maxTotalBytes = 512 << 20;
const maxFrameBytes = 8 << 20;
const maxLinesPerLog = 100_000;
const maxEventsPerLog = 10_000;
const maxEventsPerSync = 20_000;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const eventBatchSize = 100;

type JsonObject = Record<string, unknown>;
type FxCursor = {
  generation_sha256: string;
  last_seq: number;
  started: boolean;
  prefix_sha256: string;
  size_bytes: number;
  mtime_ms: number;
  updated_at_ms: number;
  incomplete: boolean;
};
type CursorState = {
  schema_version: 'threadforge.fx-session-cursor.v1';
  version: number;
  sessions: Record<string, FxCursor>;
};
type FxFile = {
  id: string;
  dir: string;
  logPath: string;
  manifestPath: string;
  sourceSha256: string;
  size: number;
  mtimeMs: number;
};
type FxManifest = {
  schema_version: number;
  storage_format: string;
  id: string;
  log_generation: string;
  updated_at_ms: number;
  workspace_root?: string;
  last_event_seq: number;
  event_log_bytes: number;
  preferences?: JsonObject;
};
type FxEnvelope = {
  schema_version: number;
  log_generation: string;
  seq: number;
  timestamp_ms?: number;
  kind: string;
  payload?: unknown;
};
type ParsedLog = {
  groups: Array<{ seq: number; record: FxEnvelope; prefix: string }>;
  maxSeq: number;
  prefixHashes: Map<number, string>;
  partialTail: boolean;
  malformed: number;
  skipped: number;
  generation?: string;
};
type MapperState = {
  workspace?: string;
  model?: string;
  provider?: string;
  lastUsage?: JsonObject;
  lastInput: number;
  lastOutput: number;
  started: boolean;
};

export type FxHistoryOptions = {
  sessionsDir?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type FxHistoryResult = {
  schema_version: 'threadforge.fx-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_lines: number;
  partial_sessions: number;
  skipped_records: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
  preview_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type FxHistoryStatus = {
  schema_version: 'threadforge.fx-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  unreadable_manifests: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
};

export async function getFxHistoryStatus(
  options: Pick<FxHistoryOptions, 'sessionsDir' | 'statePath' | 'maxSessions'> = {},
): Promise<FxHistoryStatus> {
  const maximum = boundedInteger(options.maxSessions ?? defaultMaxSessions, 1, maxSessions, '--max-sessions');
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('fx history cursor must be stored outside fx sessions');
  const listed = await listSessions(sessionsDir, maximum);
  const state = await readState(statePath);
  let checkpointed = 0;
  let unreadableManifests = 0;
  for (const file of listed.files) {
    const cursor = state.sessions[file.sourceSha256];
    let manifest: FxManifest | undefined;
    try { manifest = await readManifest(file); }
    catch { unreadableManifests++; continue; }
    if (!cursor || cursor.incomplete || cursor.size_bytes !== file.size || cursor.mtime_ms !== file.mtimeMs) continue;
    if (manifest) {
      if (cursor.generation_sha256 === sha256(manifest.log_generation) && cursor.last_seq >= manifest.last_event_seq &&
          cursor.updated_at_ms === manifest.updated_at_ms) checkpointed++;
    } else if (cursor.generation_sha256 && cursor.last_seq > 0) checkpointed++;
  }
  return {
    schema_version: 'threadforge.fx-history-status.v1', sessions: listed.files.length,
    checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
    unreadable_manifests: unreadableManifests, symlinks_skipped: listed.symlinksSkipped,
    oversized_files_skipped: listed.oversizedSkipped, sessions_truncated: listed.truncated,
  };
}

export async function syncFxHistory(options: FxHistoryOptions = {}): Promise<FxHistoryResult> {
  validateOptions(options);
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('fx history cursor must be stored outside fx sessions');
  const listed = await listSessions(sessionsDir, options.maxSessions ?? defaultMaxSessions);
  const result = emptyResult(options.includeContent ?? false);
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.sessions_truncated = listed.truncated;
  if (options.preview) {
    await processFxFiles(listed.files, sessionsDir, await readState(statePath), options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFxFiles(listed.files, sessionsDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated;
  return result;
}

function emptyResult(includeContent: boolean): FxHistoryResult {
  return {
    schema_version: 'threadforge.fx-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0, skipped_records: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false, preview_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false, errors: [],
  };
}

function validateOptions(options: FxHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? defaultMaxSessions, 1, maxSessions, '--max-sessions');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveSessionsDir(value?: string): Promise<string> {
  const directory = value ?? path.join(os.homedir(), '.fx', 'sessions');
  if (!path.isAbsolute(directory)) throw new Error('--sessions-dir must be an absolute path');
  const info = await lstat(directory).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return path.resolve(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('fx sessions path must be a real directory');
  const canonical = await realpath(directory).catch(() => { throw new Error('fx sessions directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, directory)) throw new Error('fx sessions directory must not traverse symbolic links');
  return canonical;
}

async function listSessions(
  root: string, maximum: number,
): Promise<{ files: FxFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const info = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('fx sessions path must be a real directory');
  const files: FxFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  const entries = await readdir(root, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate fx session directories'); });
  for (const entry of entries) {
    if (!entry.isDirectory() && !entry.isSymbolicLink()) continue;
    if (!validSessionId(entry.name)) continue;
    const dir = path.join(root, entry.name);
    const dirInfo = await lstat(dir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!dirInfo) continue;
    if (dirInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!dirInfo.isDirectory()) continue;
    const logPath = path.join(dir, 'events.jsonl');
    const logInfo = await lstat(logPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!logInfo) continue;
    if (logInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!logInfo.isFile()) continue;
    if (logInfo.size > maxLogBytes) { oversizedSkipped++; continue; }
    const canonical = await realpath(logPath).catch(() => '');
    if (!canonical || !await isSameRealPathWithoutLinks(canonical, logPath) || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
    files.push({
      id: entry.name, dir, logPath: canonical, manifestPath: path.join(dir, 'session.json'),
      sourceSha256: sha256(entry.name), size: logInfo.size, mtimeMs: logInfo.mtimeMs,
    });
    if (files.length > maximum) { truncated = true; break; }
  }
  files.sort((left, right) => left.mtimeMs - right.mtimeMs || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0));
  if (truncated) files.length = maximum;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

async function readManifest(file: FxFile): Promise<FxManifest | undefined> {
  const bytes = await readBoundedRegularFile(file.manifestPath, file.dir, maxManifestBytes, 'fx manifest', true);
  if (!bytes) return undefined;
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new Error('fx manifest is malformed'); }
  if (!isRecord(value) || value.schema_version !== 3 || value.storage_format !== 'event_log_v1' ||
      !validSessionId(value.id) || value.id !== file.id || !safeGeneration(value.log_generation) ||
      !isNonNegativeInteger(value.last_event_seq) || !isNonNegativeInteger(value.event_log_bytes) ||
      !isNonNegativeInteger(value.updated_at_ms) || (value.preferences !== undefined && !isRecord(value.preferences))) {
    throw new Error('fx manifest has an unsupported or invalid schema');
  }
  return value as FxManifest;
}

async function readBoundedRegularFile(
  filePath: string, root: string, maximum: number, label: string, allowMissing = false,
): Promise<Buffer | undefined> {
  const before = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') && allowMissing ? null : Promise.reject(error));
  if (!before) return undefined;
  if (!before.isFile() || before.isSymbolicLink() || before.size > maximum) throw new Error(`${label} must be a bounded regular file`);
  const canonical = await realpath(filePath).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, filePath) || !isWithin(root, canonical)) throw new Error(`${label} escaped its source directory`);
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error(`${label} changed before reading`);
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(filePath).catch(() => null);
    if (bytes.byteLength > maximum || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        opened.size !== afterHandle.size || opened.mtimeMs !== afterHandle.mtimeMs) throw new Error(`${label} changed during read`);
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith(label)) throw error;
    throw new Error(`${label} could not be opened safely`);
  } finally { await handle?.close().catch(() => {}); }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

function validSessionId(value: unknown): value is string {
  return typeof value === 'string' && value.length >= 1 && value.length <= 255 && value !== '.' && value !== '..' && /^[A-Za-z0-9_.-]+$/.test(value);
}

function safeGeneration(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 128 && /^[A-Za-z0-9_.-]+$/.test(value);
}

function isSafeRelative(value: string): boolean { return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

function parseFxLog(bytes: Buffer, committedBytes: number | undefined, expectedGeneration?: string): ParsedLog {
  const limit = committedBytes === undefined ? bytes.length : Math.min(committedBytes, bytes.length);
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const groups: ParsedLog['groups'] = [];
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let offset = 0;
  let malformed = 0;
  let skipped = 0;
  let partialTail = false;
  let previousSeq = 0;
  let generation = expectedGeneration;
  let lineCount = 0;
  while (offset < limit) {
    const newline = bytes.indexOf(0x0a, offset);
    if (newline < 0 || newline >= limit) {
      partialTail = limit > offset;
      break;
    }
    lineCount++;
    if (lineCount > maxLinesPerLog) { malformed++; break; }
    const lineStart = offset;
    const end = newline > offset && bytes[newline - 1] === 0x0d ? newline - 1 : newline;
    const raw = bytes.subarray(offset, end);
    offset = newline + 1;
    if (raw.byteLength > maxFrameBytes) { malformed++; break; }
    if (raw.toString('utf8').trim() === '') {
      prefixHasher.update(bytes.subarray(lineStart, newline + 1));
      continue;
    }
    let decoded: unknown;
    try { decoded = JSON.parse(decoder.decode(raw)) as unknown; }
    catch { malformed++; break; }
    if (!isRecord(decoded) || decoded.schema_version !== 1 || !safeGeneration(decoded.log_generation) ||
        !isNonNegativeInteger(decoded.seq) || decoded.seq < 1 || !safeRecordKind(decoded.kind)) {
      malformed++;
      break;
    }
    const recordGeneration = decoded.log_generation;
    if (generation === undefined) generation = recordGeneration;
    if (recordGeneration !== generation || (expectedGeneration !== undefined && recordGeneration !== expectedGeneration) || decoded.seq <= previousSeq) {
      malformed++;
      break;
    }
    const kind = decoded.kind;
    if (knownMappedKinds.has(kind) && !isRecord(decoded.payload)) { malformed++; break; }
    prefixHasher.update(bytes.subarray(lineStart, newline + 1));
    const prefix = prefixHasher.copy().digest('hex');
    const record: FxEnvelope = {
      schema_version: 1, log_generation: recordGeneration, seq: decoded.seq,
      ...(isNonNegativeInteger(decoded.timestamp_ms) ? { timestamp_ms: decoded.timestamp_ms } : {}),
      kind, ...(decoded.payload === undefined ? {} : { payload: decoded.payload }),
    };
    groups.push({ seq: record.seq, record, prefix });
    prefixHashes.set(record.seq, prefix);
    previousSeq = record.seq;
    if (!knownMappedKinds.has(kind)) skipped++;
  }
  return { groups, maxSeq: previousSeq, prefixHashes, partialTail, malformed, skipped, ...(generation ? { generation } : {}) };
}

const knownMappedKinds = new Set([
  'session_started', 'preferences_changed', 'workspace_rebound', 'history_turn_committed', 'usage_checkpointed',
]);
const mcpControlTools = new Set(['mcp_search_tools', 'mcp_select_tool', 'mcp_features']);

function safeRecordKind(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 96 && /^[A-Za-z0-9_]+$/.test(value);
}

type MappedFxGroup = { seq: number; prefix: string; events: ThreadforgeEvent[]; started: boolean };

function mapFxGroups(
  file: FxFile, generation: string, parsed: ParsedLog, minSeq: number, state: MapperState, includeContent: boolean,
): MappedFxGroup[] {
  const generationHash = sha256(generation);
  const mapped: MappedFxGroup[] = [];
  let totalEvents = 0;
  for (const group of parsed.groups) {
    const record = group.record;
    const payload = isRecord(record.payload) ? record.payload : {};
    const emit = record.seq > minSeq;
    const events: ThreadforgeEvent[] = [];
    switch (record.kind) {
      case 'session_started': {
        const workspace = boundedString(payload.workspace_root, 4_096);
        if (workspace) state.workspace = workspace;
        applyPreferences(state, isRecord(payload.preferences) ? payload.preferences : undefined);
        const usage = isRecord(payload.usage) ? payload.usage : undefined;
        if (usage) {
          state.lastUsage = usage;
          state.lastInput = nonNegativeNumber(usage.input_tokens) ?? state.lastInput;
          state.lastOutput = nonNegativeNumber(usage.output_tokens) ?? state.lastOutput;
        }
        if (emit && !state.started) {
          const event = makeFxEvent(file, generationHash, record, 'session.started', 'session', 'fx session start recovered from local session log', 'session.started', state);
          const createdAt = nonNegativeNumber(payload.created_at_ms);
          if (createdAt !== undefined) event.timestamp = timestampFromMs(createdAt, file.mtimeMs);
          event.raw = { ...event.raw, fx: {
            created_at_ms: createdAt,
            workspace_sha256: workspace ? sha256(workspace) : undefined,
            origin_workspace_sha256: boundedString(payload.origin_workspace_root, 4_096)
              ? sha256(String(payload.origin_workspace_root)) : undefined,
            conversation_language: safeLabel(payload.conversation_language, 32),
            ...(safePreferences(payload.preferences) ?? {}),
          } };
          pushFxEvent(events, event);
        }
        state.started = true;
        break;
      }
      case 'preferences_changed': {
        applyPreferences(state, payload);
        if (emit) {
          const event = makeFxEvent(file, generationHash, record, 'session.context', 'session', 'fx session preferences changed', 'preferences_changed', state);
          event.raw = { ...event.raw, fx: { change: 'preferences_changed', ...safePreferences(payload) } };
          pushFxEvent(events, event);
        }
        break;
      }
      case 'workspace_rebound': {
        const previousWorkspace = boundedString(payload.previous_workspace_root, 4_096);
        const nextWorkspace = boundedString(payload.workspace_root, 4_096);
        if (nextWorkspace) state.workspace = nextWorkspace;
        if (emit) {
          const event = makeFxEvent(file, generationHash, record, 'session.context', 'session', 'fx session rebound to a new workspace', 'workspace_rebound', state);
          event.raw = { ...event.raw, fx: {
            change: 'workspace_rebound',
            ...(previousWorkspace ? { previous_workspace_sha256: sha256(previousWorkspace) } : {}),
            ...(nextWorkspace ? { workspace_sha256: sha256(nextWorkspace) } : {}),
          } };
          pushFxEvent(events, event);
        }
        break;
      }
      case 'history_turn_committed': {
        const turn = isRecord(payload.turn) ? payload.turn : {};
        if (emit) mapCommittedTurn(events, file, generationHash, record, payload, turn, state, includeContent);
        updateTurnTotals(payload, state);
        break;
      }
      case 'usage_checkpointed': {
        const current = isRecord(payload.usage) ? payload.usage : undefined;
        if (current && emit) {
          const event = makeUsageCheckpoint(file, generationHash, record, current, state);
          if (event) pushFxEvent(events, event);
        }
        if (current) state.lastUsage = current;
        break;
      }
      default:
        break;
    }
    totalEvents += events.length;
    if (totalEvents > maxEventsPerLog) throw new Error(`fx session exceeds the ${maxEventsPerLog}-event processing limit`);
    mapped.push({ seq: group.seq, prefix: group.prefix, events, started: state.started });
  }
  return mapped;
}

function applyPreferences(state: MapperState, preferences?: JsonObject): void {
  if (!preferences) return;
  const model = safeLabel(preferences.model, 160);
  const provider = safeLabel(preferences.provider, 96);
  if (model) state.model = model;
  if (provider) state.provider = provider;
}

function pushFxEvent(events: ThreadforgeEvent[], event: ThreadforgeEvent): void {
  if (events.length >= eventBatchSize) throw new Error(`fx record exceeds the ${eventBatchSize}-event batch limit`);
  events.push(event);
}

function safePreferences(value: unknown): JsonObject | undefined {
  if (!isRecord(value)) return undefined;
  const result: JsonObject = {};
  for (const key of ['provider', 'model', 'effort']) {
    const label = safeLabel(value[key], key === 'model' ? 160 : 96);
    if (label) result[key] = label;
  }
  if (typeof value.fast_mode === 'boolean') result.fast_mode = value.fast_mode;
  return Object.keys(result).length ? result : undefined;
}

function mapCommittedTurn(
  events: ThreadforgeEvent[], file: FxFile, generationHash: string, record: FxEnvelope,
  payload: JsonObject, turn: JsonObject, state: MapperState, includeContent: boolean,
): void {
  const user = isRecord(turn.user) ? turn.user : undefined;
  if (user) {
    const text = durableText(user.text);
    if (text) {
      const summary = isRecord(turn.execution) && isRecord(turn.execution.turn_summary) ? turn.execution.turn_summary : undefined;
      const startedAt = summary ? nonNegativeNumber(summary.started_at_ms) : undefined;
      const prompt = makeTextEvent(file, generationHash, record, 'prompt.submitted', 'prompt', 'fx prompt recovered from committed turn',
        `turn.prompt`, state, text, includeContent, 'user');
      if (startedAt !== undefined) prompt.timestamp = timestampFromMs(startedAt, file.mtimeMs);
      const images = Array.isArray(user.images) ? user.images.length : 0;
      if (images > 0) prompt.raw = { ...prompt.raw, fx: { image_count: Math.min(images, 100) } };
      pushFxEvent(events, prompt);
    }
  }

  const execution = isRecord(turn.execution) ? turn.execution : undefined;
  const steps = execution && Array.isArray(execution.tool_steps) ? execution.tool_steps : [];
  for (let stepIndex = 0; stepIndex < Math.min(steps.length, 500); stepIndex++) {
    const step = isRecord(steps[stepIndex]) ? steps[stepIndex] as JsonObject : {};
    const calls = Array.isArray(step.tool_calls) ? step.tool_calls : [];
    const results = Array.isArray(step.tool_results) ? step.tool_results : [];
    const resultById = new Map<string, JsonObject>();
    for (const item of results) {
      if (!isRecord(item)) continue;
      const callId = opaqueString(item.tool_call_id, 512);
      if (callId) resultById.set(callId, item);
    }
    for (let callIndex = 0; callIndex < Math.min(calls.length, 500); callIndex++) {
      const call = isRecord(calls[callIndex]) ? calls[callIndex] as JsonObject : {};
      const callId = opaqueString(call.id, 512) ?? `seq-${record.seq}-step-${stepIndex}-call-${callIndex}`;
      const outcome = resultById.get(callId);
      if (outcome) mapToolOutcome(events, file, generationHash, record, turn, call, outcome, state, stepIndex, callIndex);
      else mapToolWithoutResult(events, file, generationHash, record, call, state, stepIndex, callIndex,
        turn.kind === 'interrupted' ? 'interrupted' : 'no_result');
    }
  }

  if (turn.kind === 'interrupted' && isRecord(turn.tool_call)) {
    mapToolWithoutResult(events, file, generationHash, record, turn.tool_call, state, -1, 0, 'interrupted');
  }

  const assistant = durableText(turn.assistant);
  if (assistant?.trim()) {
    pushFxEvent(events, makeTextEvent(file, generationHash, record, 'agent.message', 'agent', 'fx assistant response recovered from committed turn',
      'turn.assistant', state, assistant, includeContent, 'assistant'));
  }
  if (turn.kind === 'compacted_summary') {
    const summary = durableText(turn.summary);
    const event = makeFxEvent(file, generationHash, record, 'session.compacting', 'session', 'fx compacted session history',
      'turn.compaction', state);
    event.raw = { ...event.raw, fx: {
      removed_turn_count: nonNegativeInteger(turn.removed_turn_count) ?? 0,
      compaction_count: nonNegativeInteger(turn.compaction_count) ?? 0,
      ...(summary ? { summary: contentDigest(summary) } : {}),
    } };
    pushFxEvent(events, event);
  }
  const usageEvent = makeTurnUsage(file, generationHash, record, payload, execution, state);
  if (usageEvent) pushFxEvent(events, usageEvent);
}

function makeTextEvent(
  file: FxFile, generationHash: string, record: FxEnvelope, action: string, category: string, message: string,
  suffix: string, state: MapperState, text: string, includeContent: boolean, role: 'user' | 'assistant',
): ThreadforgeEvent {
  const allowed = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
  const event = makeFxEvent(file, generationHash, record, action, category, message, suffix, state, {
    content: contentDigest(text, allowed, includeContent),
  });
  if (allowed) {
    if (role === 'user') {
      event.prompt = { text };
      event.gen_ai = { ...(isRecord(event.gen_ai) ? event.gen_ai : {}), input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } };
    } else {
      event.gen_ai = { ...(isRecord(event.gen_ai) ? event.gen_ai : {}), output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } };
    }
  }
  return event;
}

function mapToolWithoutResult(
  events: ThreadforgeEvent[], file: FxFile, generationHash: string, record: FxEnvelope,
  call: JsonObject, state: MapperState, stepIndex: number, callIndex: number, reason: string,
): void {
  const name = safeLabel(call.name, 128) ?? 'unknown';
  const callId = opaqueString(call.id, 512);
  const args = parseToolArguments(call.arguments_json);
  const argsDigest = digestUnknown(args);
  const event = makeFxEvent(file, generationHash, record, 'tool.invoked', 'tool', 'fx tool call has no recorded result',
    `turn.${stepIndex}.${callIndex}.invoked`, state, { content: argsDigest });
  event.tool = { name };
  event.gen_ai = {
    ...(isRecord(event.gen_ai) ? event.gen_ai : {}), operation: { name: 'execute_tool' },
    tool: { name, call: { id: callId ? `fxcall:${sha256(callId)}` : undefined, arguments: argsDigest } },
  };
  event.raw = { ...event.raw, fx: { result_missing: reason, ...(callId ? { tool_call_sha256: sha256(callId) } : {}) } };
  pushFxEvent(events, event);
}

function mapToolOutcome(
  events: ThreadforgeEvent[], file: FxFile, generationHash: string, record: FxEnvelope, turn: JsonObject,
  call: JsonObject, result: JsonObject, state: MapperState, stepIndex: number, callIndex: number,
): void {
  const name = safeLabel(result.tool_name, 128) ?? safeLabel(call.name, 128) ?? 'unknown';
  const callId = opaqueString(result.tool_call_id, 512) ?? opaqueString(call.id, 512);
  const status = result.status === 'failure' ? 'failure' : result.status === 'success' ? 'success' : 'unknown';
  const toolLower = name.toLowerCase();
  const args = parseToolArguments(call.arguments_json);
  const argsDigest = digestUnknown(args);
  const evidence = findFileEvidence(turn, callId);
  const committed = isRecord(result.committed_file_presentation) ? result.committed_file_presentation : undefined;
  let action = 'tool.completed';
  let category = 'tool';
  let severity: ThreadforgeEvent['severity'] = 'info';
  let message = 'fx tool completed';
  if (toolLower.startsWith('mcp_') && !mcpControlTools.has(toolLower)) {
    action = 'mcp.tool_invoked'; category = 'mcp'; message = 'fx MCP tool invoked';
  } else if (toolLower === 'run_command' || toolLower === 'terminal') {
    action = 'command.executed'; category = 'command'; message = 'fx command executed';
  } else if (committed) {
    action = committed.kind === 'added' ? 'file.created' : 'file.modified';
    category = 'file'; message = committed.kind === 'added' ? 'fx file created' : 'fx file modified';
  } else if (evidence) {
    const fileAction = String(evidence.action ?? '');
    if (['read', 'search', 'list'].includes(fileAction)) { action = 'file.read'; category = 'file'; message = 'fx file read'; }
    else if (fileAction === 'write') { action = 'file.created'; category = 'file'; message = 'fx file created'; }
    else if (['edit', 'delete', 'rename', 'copy'].includes(fileAction)) { action = 'file.modified'; category = 'file'; message = 'fx file modified'; }
  } else if (status === 'failure') {
    action = 'tool.failed'; category = 'tool'; severity = 'high'; message = 'fx tool failed';
  }
  const event = makeFxEvent(file, generationHash, record, action, category, message,
    `turn.${stepIndex}.${callIndex}.result`, state, { severity });
  const createdAt = nonNegativeNumber(result.created_at_ms);
  if (createdAt !== undefined) event.timestamp = timestampFromMs(createdAt, file.mtimeMs);
  event.tool = { name };
  event.gen_ai = {
    ...(isRecord(event.gen_ai) ? event.gen_ai : {}), operation: { name: 'execute_tool' },
    tool: { name, call: { ...(callId ? { id: `fxcall:${sha256(callId)}` } : {}), arguments: argsDigest } },
  };
  if (action === 'mcp.tool_invoked') event.mcp = { tool: name };
  if (action === 'command.executed') mapCommandFields(event, call, result, args);
  if (action === 'file.read' || action === 'file.created' || action === 'file.modified') mapFileFields(event, result, evidence, committed, args);
  const output = durableText(result.output);
  if (output) event.content = contentDigest(output);
  const statusRaw: JsonObject = {
    status, output_bytes: nonNegativeInteger(result.output_bytes), stored_output_bytes: nonNegativeInteger(result.stored_output_bytes),
    truncated: result.truncated === true, provider_native: result.provider_native === true,
    ...(callId ? { tool_call_sha256: sha256(callId) } : {}),
  };
  const feedback = Array.isArray(result.permission_feedback) ? result.permission_feedback.map(durableText).filter(Boolean) : [];
  if (feedback.length) statusRaw.permission_feedback = { count: feedback.length, sha256: sha256(feedback.join('\n')) };
  const process = isRecord(result.command_process_presentation) ? result.command_process_presentation : undefined;
  if (process && safeLabel(process.kind, 64)) {
    statusRaw.process_outcome = process.kind;
    const value = typeof process.value === 'number' && Number.isSafeInteger(process.value) ? process.value : undefined;
    if (value !== undefined) statusRaw.process_value = value;
  }
  const terminal = isRecord(result.terminal_action_presentation) ? result.terminal_action_presentation : undefined;
  if (terminal && safeLabel(terminal.kind, 64)) statusRaw.terminal_outcome = terminal.kind;
  if (committed) {
    statusRaw.diff_additions = nonNegativeInteger(committed.additions) ?? 0;
    statusRaw.diff_deletions = nonNegativeInteger(committed.deletions) ?? 0;
    statusRaw.diff_truncated = committed.truncated === true;
  }
  event.raw = { ...event.raw, fx: { ...(isRecord((event.raw as JsonObject).fx) ? (event.raw as JsonObject).fx as JsonObject : {}), ...statusRaw } };
  if (status === 'failure') event.error = { type: 'tool_execution_failed' };
  pushFxEvent(events, event);
}

function mapCommandFields(event: ThreadforgeEvent, call: JsonObject, result: JsonObject, args: unknown): void {
  const command = isRecord(args) ? boundedString(args.command ?? args.cmd, 1 << 20) : undefined;
  const output = durableText(result.output);
  const process = isRecord(result.command_process_presentation) ? result.command_process_presentation : undefined;
  const commandInfo: JsonObject = {};
  if (command) {
    commandInfo.command_sha256 = sha256(command);
    commandInfo.command_bytes = Buffer.byteLength(command, 'utf8');
  }
  if (output) {
    commandInfo.output_sha256 = sha256(output);
    commandInfo.output_bytes = Buffer.byteLength(output, 'utf8');
  }
  if (process?.kind === 'exit_code' && typeof process.value === 'number' && Number.isSafeInteger(process.value)) commandInfo.exit_code = process.value;
  event.command = commandInfo;
  if (command) event.tool = { name: safeLabel(call.name, 128) ?? 'command', command_sha256: sha256(command) };
}

function mapFileFields(
  event: ThreadforgeEvent, result: JsonObject, evidence: JsonObject | undefined, committed: JsonObject | undefined, args: unknown,
): void {
  const committedPath = committed ? durableText(committed.path) : '';
  const evidencePath = evidence ? durableText(evidence.path) : '';
  const argsPath = isRecord(args) ? boundedString(args.path ?? args.file_path ?? args.target_path, 4_096) ?? '' : '';
  const selectedPath = committedPath || evidencePath || argsPath;
  const operation = evidence ? fileOperation(String(evidence.action ?? '')) : committed?.kind === 'added' ? 'create' : committed ? 'modify' : undefined;
  const fileInfo: JsonObject = {};
  if (selectedPath) fileInfo.path_sha256 = sha256(selectedPath);
  if (operation) fileInfo.operation = operation;
  if (committed) {
    const diff = committedDiff(committed);
    if (diff) { fileInfo.diff_sha256 = sha256(diff); fileInfo.diff_bytes = Buffer.byteLength(diff, 'utf8'); event.content = contentDigest(diff); }
  }
  if (committed && nonNegativeInteger(committed.additions) !== undefined) fileInfo.additions = committed.additions;
  if (committed && nonNegativeInteger(committed.deletions) !== undefined) fileInfo.deletions = committed.deletions;
  event.file = fileInfo;
  void result;
}

function findFileEvidence(turn: JsonObject, callId?: string): JsonObject | undefined {
  if (!callId || !isRecord(turn.execution) || !Array.isArray(turn.execution.files)) return undefined;
  return turn.execution.files.find((value) => isRecord(value) && opaqueString(value.tool_call_id, 512) === callId) as JsonObject | undefined;
}

function fileOperation(action: string): string | undefined {
  if (['read', 'search', 'list'].includes(action)) return 'read';
  if (action === 'write') return 'create';
  if (action === 'edit') return 'modify';
  if (['delete', 'rename', 'copy'].includes(action)) return action;
  return undefined;
}

function committedDiff(value: JsonObject): string {
  if (!Array.isArray(value.lines)) return '';
  let output = '';
  for (const row of value.lines.slice(0, 20_000)) {
    if (!isRecord(row)) continue;
    const kind = row.kind;
    if (kind === 'elision') { output += '...\n'; continue; }
    const prefix = kind === 'addition' ? '+' : kind === 'deletion' ? '-' : kind === 'context' ? ' ' : '# ';
    output += prefix + durableText(row.text) + '\n';
    if (Buffer.byteLength(output, 'utf8') > maxFrameBytes) return output.slice(0, maxFrameBytes);
  }
  return output;
}

function parseToolArguments(value: unknown): unknown {
  const text = durableText(value);
  if (!text) return undefined;
  try { return JSON.parse(text) as unknown; } catch { return text; }
}

function makeTurnUsage(
  file: FxFile, generationHash: string, record: FxEnvelope, payload: JsonObject,
  execution: JsonObject | undefined, state: MapperState,
): ThreadforgeEvent | undefined {
  const summary = execution && isRecord(execution.turn_summary) ? execution.turn_summary : undefined;
  const progress = summary && isRecord(summary.token_progress) ? summary.token_progress : undefined;
  const inputSummary = progress ? nonNegativeInteger(progress.input_tokens) ?? 0 : 0;
  const outputSummary = progress ? nonNegativeInteger(progress.output_tokens) ?? 0 : 0;
  let input = 0;
  let output = 0;
  const raw: JsonObject = {};
  if ((inputSummary > 0 || outputSummary > 0) && progress) {
    input = inputSummary; output = outputSummary;
    raw.token_source = 'turn_summary';
    raw.input_exact = progress.input_exact === true;
    raw.output_exact = progress.output_exact === true;
    raw.turn_duration_ms = nonNegativeInteger(summary?.turn_duration_ms);
    raw.thinking_duration_ms = nonNegativeInteger(summary?.thinking_duration_ms);
  } else {
    input = positiveDelta(nonNegativeNumber(payload.total_input_tokens) ?? 0, state.lastInput);
    output = positiveDelta(nonNegativeNumber(payload.total_output_tokens) ?? 0, state.lastOutput);
    raw.token_source = 'session_totals_delta';
  }
  if (input <= 0 && output <= 0) return undefined;
  const usage: JsonObject = {};
  if (input > 0) usage.input_tokens = input;
  if (output > 0) usage.output_tokens = output;
  const event = makeFxEvent(file, generationHash, record, 'token.usage', 'metric', 'fx token usage recovered from committed turn', 'turn.usage', state);
  event.gen_ai = { ...(isRecord(event.gen_ai) ? event.gen_ai : {}), usage };
  event.raw = { ...event.raw, fx: raw };
  return event;
}

function updateTurnTotals(payload: JsonObject, state: MapperState): void {
  const input = nonNegativeNumber(payload.total_input_tokens);
  const output = nonNegativeNumber(payload.total_output_tokens);
  if (input !== undefined) state.lastInput = input;
  if (output !== undefined) state.lastOutput = output;
}

function makeUsageCheckpoint(
  file: FxFile, generationHash: string, record: FxEnvelope, current: JsonObject, state: MapperState,
): ThreadforgeEvent | undefined {
  const previous = state.lastUsage;
  const cacheRead = positiveDelta(nonNegativeNumber(current.cache_read_tokens) ?? 0, nonNegativeNumber(previous?.cache_read_tokens) ?? 0);
  const cacheWrite = positiveDelta(nonNegativeNumber(current.cache_write_tokens) ?? 0, nonNegativeNumber(previous?.cache_write_tokens) ?? 0);
  const costNow = finiteNonNegative(current.total_cost) ?? 0;
  const costBefore = finiteNonNegative(previous?.total_cost) ?? 0;
  const cost = costNow > costBefore ? costNow - costBefore : 0;
  if (cacheRead <= 0 && cacheWrite <= 0 && cost <= 0) return undefined;
  const usage: JsonObject = {};
  if (cacheRead > 0) usage.cache_read_input_tokens = cacheRead;
  if (cacheWrite > 0) usage.cache_creation_input_tokens = cacheWrite;
  if (cost > 0) usage.cost_usd = cost;
  const models = Array.isArray(current.models) ? current.models.filter(isRecord).slice(0, 20).map((model) => ({
    ...(safeLabel(model.model, 160) ? { model: safeLabel(model.model, 160) } : {}),
    ...(nonNegativeInteger(model.input_tokens) !== undefined ? { input_tokens: model.input_tokens } : {}),
    ...(nonNegativeInteger(model.output_tokens) !== undefined ? { output_tokens: model.output_tokens } : {}),
    ...(nonNegativeInteger(model.cache_read_tokens) !== undefined ? { cache_read_tokens: model.cache_read_tokens } : {}),
    ...(nonNegativeInteger(model.cache_write_tokens) !== undefined ? { cache_write_tokens: model.cache_write_tokens } : {}),
    ...(nonNegativeInteger(model.reasoning_tokens) !== undefined ? { reasoning_tokens: model.reasoning_tokens } : {}),
    ...(finiteNonNegative(model.total_cost) !== undefined ? { cost_usd: model.total_cost } : {}),
  })) : [];
  const event = makeFxEvent(file, generationHash, record, 'token.usage', 'metric', 'fx usage checkpoint recovered from local session log', 'usage.checkpoint', state);
  event.gen_ai = { ...(isRecord(event.gen_ai) ? event.gen_ai : {}), usage };
  event.raw = { ...event.raw, fx: {
    ...(safeLabel(current.billing, 32) ? { billing: current.billing } : {}),
    cumulative_input_tokens: nonNegativeInteger(current.input_tokens),
    cumulative_output_tokens: nonNegativeInteger(current.output_tokens),
    cumulative_cache_read_tokens: nonNegativeInteger(current.cache_read_tokens),
    cumulative_cache_write_tokens: nonNegativeInteger(current.cache_write_tokens),
    cumulative_cost_usd: costNow,
    api_duration_ms: nonNegativeInteger(current.api_duration_ms),
    wall_duration_ms: nonNegativeInteger(current.wall_duration_ms),
    api_duration_complete: current.api_duration_complete === true,
    wall_duration_complete: current.wall_duration_complete === true,
    code_complete: current.code_complete === true,
    billable_web_search_calls: nonNegativeInteger(current.billable_web_search_calls),
    cumulative_lines_added: nonNegativeInteger(current.lines_added),
    cumulative_lines_removed: nonNegativeInteger(current.lines_removed),
    reported_as_delta_from_prior: previous !== undefined,
    ...(models.length ? { cumulative_models: models } : {}),
  } };
  return event;
}

function makeFxEvent(
  file: FxFile, generationHash: string, record: FxEnvelope, action: string, category: string,
  message: string, suffix: string, state: MapperState,
  extra: { content?: JsonObject; severity?: ThreadforgeEvent['severity'] } = {},
): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: timestampFromMs(nonNegativeNumber(record.timestamp_ms) ?? file.mtimeMs, file.mtimeMs),
    vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: {
      id: sha256(`${file.sourceSha256}:${generationHash}:${record.seq}:${suffix}`),
      kind: 'agent_runtime', action, category, fidelity: 'observed',
    },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'vercel_fx', collection_method: 'poll' },
    session: { id: `vercel-fx:${file.sourceSha256}` },
    raw: {
      source: 'fx_session_events_jsonl', source_sha256: file.sourceSha256, seq: record.seq,
      log_generation_sha256: generationHash, record_kind: record.kind, message,
      ...(state.workspace ? { workspace_sha256: sha256(state.workspace) } : {}),
    },
  };
  if (state.model) event.model = state.model;
  if (state.provider) event.gen_ai = { provider: { name: state.provider } };
  if (extra.content) event.content = extra.content;
  return event;
}

function contentDigest(value: unknown, include = false, requested = false): JsonObject {
  const digest = digestUnknown(value);
  return {
    retention: include ? 'full' : 'metadata', included: include,
    ...(digest ? digest : {}), ...(requested && !include ? { truncated: true } : {}),
  };
}

function digestUnknown(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); }
  catch { return undefined; }
  if (typeof text !== 'string') return undefined;
  return { hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

async function processFxFiles(
  files: FxFile[], root: string, state: CursorState, options: FxHistoryOptions, result: FxHistoryResult,
  persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const previous = state.sessions[file.sourceSha256];
    try {
      let manifest: FxManifest | undefined;
      try { manifest = await readManifest(file); }
      catch {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx session manifest is unreadable or unsupported' });
        continue;
      }
      const manifestInfo = await lstat(file.manifestPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      const sourceBytes = file.size + (manifestInfo?.size ?? 0);
      totalBytes += sourceBytes;
      if (totalBytes > maxTotalBytes) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx sync exceeded the aggregate source-byte limit' });
        break;
      }
      const before = await lstat(file.logPath).catch(() => null);
      if (!before || before.isSymbolicLink() || !before.isFile() || before.size !== file.size || before.mtimeMs !== file.mtimeMs) {
        throw new Error('fx event log changed after enumeration');
      }
      const bytes = await readBoundedRegularFile(file.logPath, root, maxLogBytes, 'fx event log');
      if (!bytes) throw new Error('fx event log disappeared');
      const after = await lstat(file.logPath).catch(() => null);
      if (!after || after.size !== file.size || after.mtimeMs !== file.mtimeMs || !sameFile(before, after)) {
        throw new Error('fx event log changed while being read');
      }
      const expectedGeneration = manifest?.log_generation;
      const watermark = manifest?.event_log_bytes;
      const parsed = parseFxLog(bytes, watermark, expectedGeneration);
      result.malformed_lines += parsed.malformed;
      result.skipped_records += parsed.skipped;
      if (parsed.partialTail) result.partial_sessions++;
      const parseIncomplete = parsed.partialTail || parsed.malformed > 0 || (watermark !== undefined && watermark > bytes.byteLength);
      if (parsed.malformed > 0) result.errors.push({ source_sha256: file.sourceSha256, message: 'fx event log contains a malformed or unsupported committed frame' });
      if (watermark !== undefined && watermark > bytes.byteLength) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest committed watermark extends beyond the event log' });
      }
      const cursorIncomplete = parseIncomplete || (manifest !== undefined && manifest.last_event_seq !== parsed.maxSeq);
      const generation = manifest?.log_generation ?? parsed.generation;
      if (!generation) {
        if (manifest && manifest.last_event_seq === 0 && manifest.event_log_bytes === 0) {
          state.sessions[file.sourceSha256] = makeFxCursor('', 0, previous?.started ?? false,
            parsed.prefixHashes.get(0)!, file, manifest.updated_at_ms, cursorIncomplete);
          result.sessions_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
        continue;
      }
      const generationHash = sha256(generation);
      if (manifest && manifest.last_event_seq > parsed.maxSeq && !(manifest.last_event_seq === 0 && parsed.maxSeq === 0)) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest event sequence is ahead of committed records' });
      }
      if (manifest && parsed.maxSeq > manifest.last_event_seq) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest event sequence is behind committed records' });
      }
      const generationChanged = previous !== undefined && previous.generation_sha256 !== generationHash;
      const minSeq = previous && !generationChanged ? previous.last_seq : 0;
      if (previous && !generationChanged && (parsed.maxSeq < previous.last_seq ||
          parsed.prefixHashes.get(previous.last_seq) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx event-log prefix changed within the same generation; refusing replay' });
        continue;
      }

      const mapper: MapperState = {
        ...(manifest?.workspace_root ? { workspace: manifest.workspace_root } : {}),
        lastInput: 0, lastOutput: 0, started: previous?.started ?? false,
      };
      if (manifest?.preferences) applyPreferences(mapper, manifest.preferences);
      const mapped = mapFxGroups(file, generation, parsed, minSeq, mapper, options.includeContent ?? false);
      const pendingEventCount = mapped.reduce((count, group) => count + group.events.length, 0);
      if (pendingEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx sync exceeded the event processing limit' });
        break;
      }
      if (options.preview) {
        result.events_previewed += pendingEventCount;
        result.events ??= [];
        for (const group of mapped) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let pendingGroups: MappedFxGroup[] = [];
      let pendingEvents: ThreadforgeEvent[] = [];
      const flush = async () => {
        if (pendingGroups.length === 0) return;
        if (pendingEvents.length) {
          await emitEvents(pendingEvents, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
          result.events_emitted += pendingEvents.length;
        }
        const last = pendingGroups[pendingGroups.length - 1]!;
        const nextCursor = makeFxCursor(generationHash, last.seq, last.started, last.prefix, file,
          manifest?.updated_at_ms ?? file.mtimeMs, cursorIncomplete);
        const current = state.sessions[file.sourceSha256];
        const changed = !current || current.generation_sha256 !== nextCursor.generation_sha256 || current.last_seq !== nextCursor.last_seq ||
          current.started !== nextCursor.started || current.prefix_sha256 !== nextCursor.prefix_sha256 ||
          current.size_bytes !== nextCursor.size_bytes || current.mtime_ms !== nextCursor.mtime_ms ||
          current.updated_at_ms !== nextCursor.updated_at_ms || current.incomplete !== nextCursor.incomplete;
        state.sessions[file.sourceSha256] = nextCursor;
        if (changed) {
          result.sessions_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
        pendingGroups = [];
        pendingEvents = [];
      };
      try {
        for (const group of mapped) {
          if (pendingEvents.length > 0 && pendingEvents.length + group.events.length > eventBatchSize) await flush();
          pendingGroups.push(group);
          pendingEvents.push(...group.events);
          if (pendingEvents.length >= eventBatchSize) await flush();
        }
        await flush();
      } catch {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected fx history events' });
        continue;
      }

      if (mapped.length === 0 && parsed.maxSeq === 0 && manifest?.last_event_seq === 0) {
        state.sessions[file.sourceSha256] = makeFxCursor(generationHash, 0, previous?.started ?? false,
          parsed.prefixHashes.get(0)!, file, manifest.updated_at_ms, cursorIncomplete);
        if (persist && statePath) await writeState(statePath, state);
      } else if (mapped.length > 0 && mapped[mapped.length - 1]!.seq < parsed.maxSeq) {
        const lastPrefix = parsed.prefixHashes.get(parsed.maxSeq);
        if (lastPrefix) {
          const current = state.sessions[file.sourceSha256] ?? previous;
          state.sessions[file.sourceSha256] = makeFxCursor(generationHash, parsed.maxSeq, mapper.started, lastPrefix, file,
            manifest?.updated_at_ms ?? file.mtimeMs, cursorIncomplete);
          if (current?.last_seq !== parsed.maxSeq || generationChanged) result.sessions_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
      }
    } catch {
      result.errors.push({ source_sha256: file.sourceSha256, message: 'fx session could not be read or checkpointed safely' });
    }
  }
}

function makeFxCursor(
  generationHash: string, lastSeq: number, started: boolean, prefix: string, file: FxFile,
  updatedAtMs: number, incomplete: boolean,
): FxCursor {
  return {
    generation_sha256: generationHash, last_seq: lastSeq, started, prefix_sha256: prefix,
    size_bytes: file.size, mtime_ms: file.mtimeMs, updated_at_ms: updatedAtMs, incomplete,
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
  return path.join(root, 'state', cursorName);
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await verifyStateParent(statePath, true);
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('fx history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock fx history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.fx-session-cursor.v1', version: cursorVersion, sessions: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes) throw new Error('fx history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('fx cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxCursorBytes) throw new Error('fx cursor exceeds local size limit');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.sessions)) {
      throw new Error('fx history cursor has an unsupported schema; inspect it before retrying');
    }
    const sessions: Record<string, FxCursor> = {};
    for (const [key, value] of Object.entries(parsed.sessions)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) ||
          (value.generation_sha256 !== '' && (typeof value.generation_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.generation_sha256))) ||
          !isNonNegativeInteger(value.last_seq) || typeof value.started !== 'boolean' ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
          typeof value.updated_at_ms !== 'number' || !Number.isFinite(value.updated_at_ms) || value.updated_at_ms < 0 ||
          typeof value.incomplete !== 'boolean') throw new Error('fx history cursor contains an invalid checkpoint');
      sessions[key] = {
        generation_sha256: value.generation_sha256, last_seq: value.last_seq, started: value.started,
        prefix_sha256: value.prefix_sha256, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
        updated_at_ms: value.updated_at_ms, incomplete: value.incomplete,
      };
    }
    return { ...empty, sessions };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('fx history cursor')) throw error;
    throw new Error('fx history cursor is malformed or unreadable');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private fx cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('fx cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxCursorBytes) throw new Error('fx history cursor exceeds the local size limit');
  const temporary = `${statePath}.${randomBytes(12).toString('hex')}.tmp`;
  let handle;
  try {
    handle = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.chmod(0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, statePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(temporary).catch(() => {});
    throw new Error('unable to atomically write fx history cursor');
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function nonNegativeNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function finiteNonNegative(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined;
}
function positiveDelta(current: number, previous: number): number { return current > previous ? current - previous : 0; }
function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined;
}
function opaqueString(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
}
function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = typeof value === 'string' ? value.trim() : '';
  if (!text || text.length > maximum || !/^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text) ||
      /^(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[_-]/i.test(text) ||
      /^[A-Za-z0-9_-]{40,}$/.test(text) ||
      /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(text)) return undefined;
  return text;
}
function durableText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (isRecord(value) && value.encoding === 'base64' && typeof value.data === 'string' && value.data.length <= (maxFrameBytes * 4 / 3 + 8) &&
      /^[A-Za-z0-9+/]*={0,2}$/.test(value.data)) {
    return Buffer.from(value.data, 'base64').toString('utf8');
  }
  return '';
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function timestampFromMs(value: number, fallbackMs: number): string {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.getTime()) ? new Date(fallbackMs).toISOString() : timestamp.toISOString();
}
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
