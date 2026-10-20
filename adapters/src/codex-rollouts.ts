import { createHash } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { emitEvents, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const maxStateBytes = 8 << 20;
const maxRolloutBytes = 64 << 20;
const maxLineBytes = 8 << 20;
const maxFiles = 10_000;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const eventBatchSize = 100;
const maxPreviewEvents = 500;
const maxRetainedContentBytes = 4 << 10;
const genAIStoredStringLimit = 2 << 10;
const stateFileName = 'codex-rollouts.v1.json';
const supportedEntryTypes = new Set(['session_meta', 'turn_context', 'response_item', 'event_msg', 'token_usage_record']);

type JsonObject = Record<string, unknown>;
type RolloutRecord = { line: number; entry: JsonObject; payload: JsonObject; type: string; fingerprint: string };
type FileIdentity = { dev: number; ino: number; size: number; mtimeMs: number };
type RolloutFile = { key: string; absolutePath: string };
type RolloutCursor = {
  line: number;
  size_bytes: number;
  mtime_ms: number;
  prefix_sha256: string;
  session_started: boolean;
};
type RolloutState = { schema_version: 'threadforge.codex-rollout-cursor.v1'; version: number; files: Record<string, RolloutCursor> };

export type CodexRolloutSyncOptions = {
  codexHome?: string;
  statePath?: string;
  endpoint?: string;
  preview?: boolean;
  includeContent?: boolean;
};

export type CodexRolloutSyncResult = {
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_lines: number;
  partial_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  content_mode: 'metadata' | 'include-content';
  preview_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type CodexRolloutStatus = {
  schema_version: 'threadforge.codex-rollout-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files: Array<{
    source_sha256: string;
    size_bytes: number;
    cursor_line: number;
    checkpoint_matches_metadata: boolean;
  }>;
};

export async function getCodexRolloutStatus(options: Pick<CodexRolloutSyncOptions, 'codexHome' | 'statePath'> = {}): Promise<CodexRolloutStatus> {
  const codexHome = await resolveCodexHome(options.codexHome ?? process.env.CODEX_HOME ?? path.join(os.homedir(), '.codex'));
  const sessionsDir = path.join(codexHome, 'sessions');
  const dirInfo = await lstat(sessionsDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Codex sessions directory');
  });
  const empty: CodexRolloutStatus = {
    schema_version: 'threadforge.codex-rollout-status.v1', sessions: 0, checkpointed_sessions: 0,
    pending_sessions: 0, symlinks_skipped: 0, oversized_files_skipped: 0, files: [],
  };
  if (!dirInfo) return empty;
  if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink()) throw new Error('Codex sessions path must be a real directory');
  const canonicalSessionsDir = await realpath(sessionsDir).catch(() => { throw new Error('Codex sessions directory could not be resolved'); });
  if (!isWithin(codexHome, canonicalSessionsDir)) throw new Error('Codex sessions directory must remain inside CODEX_HOME');
  const listed = await listRolloutFiles(canonicalSessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(codexHome, await prospectiveRealPath(statePath))) {
    throw new Error('rollout cursor state must not be stored inside CODEX_HOME');
  }
  const state = await readState(statePath);
  const files: CodexRolloutStatus['files'] = [];
  for (const file of listed.files) {
    const info = await lstat(file.absolutePath).catch((error: unknown) => {
      if (isCode(error, 'ENOENT')) return null;
      throw new Error('unable to inspect Codex rollout metadata');
    });
    if (!info || !info.isFile() || info.isSymbolicLink()) continue;
    const cursor = state.files[file.key];
    const matches = cursor !== undefined && cursor.size_bytes === info.size && cursor.mtime_ms === info.mtimeMs;
    files.push({
      source_sha256: sha256(file.key),
      size_bytes: info.size,
      cursor_line: cursor?.line ?? 0,
      checkpoint_matches_metadata: matches,
    });
  }
  const checkpointed = files.filter((file) => file.checkpoint_matches_metadata).length;
  return {
    schema_version: empty.schema_version,
    sessions: files.length,
    checkpointed_sessions: checkpointed,
    pending_sessions: files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped,
    oversized_files_skipped: listed.oversizedSkipped,
    files,
  };
}

export async function syncCodexRollouts(options: CodexRolloutSyncOptions = {}): Promise<CodexRolloutSyncResult> {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  const codexHome = await resolveCodexHome(options.codexHome ?? process.env.CODEX_HOME ?? path.join(os.homedir(), '.codex'));
  const sessionsDir = path.join(codexHome, 'sessions');
  const dirInfo = await lstat(sessionsDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Codex sessions directory');
  });
  if (!dirInfo) return emptyResult();
  if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink()) throw new Error('Codex sessions path must be a real directory');
  const canonicalSessionsDir = await realpath(sessionsDir).catch(() => { throw new Error('Codex sessions directory could not be resolved'); });
  if (!isWithin(codexHome, canonicalSessionsDir)) throw new Error('Codex sessions directory must remain inside CODEX_HOME');

  const listed = await listRolloutFiles(canonicalSessionsDir);
  const result = emptyResult();
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.content_mode = options.includeContent ? 'include-content' : 'metadata';
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(codexHome, await prospectiveRealPath(statePath))) {
    throw new Error('rollout cursor state must not be stored inside CODEX_HOME');
  }
  if (options.preview) {
    const state = await readState(statePath);
    result.events = [];
    await processRolloutFiles(listed.files, canonicalSessionsDir, state, options, result, false);
    return result;
  }

  await withStateLock(statePath, async () => {
    const state = await readState(statePath);
    await processRolloutFiles(listed.files, canonicalSessionsDir, state, options, result, true, statePath);
    await writeState(statePath, state);
  });
  return result;
}

function emptyResult(): CodexRolloutSyncResult {
  return {
    sessions: 0, sessions_changed: 0, events_emitted: 0, events_previewed: 0,
    malformed_lines: 0, partial_sessions: 0, symlinks_skipped: 0, oversized_files_skipped: 0, content_mode: 'metadata',
    preview_truncated: false, errors: [],
  };
}

async function resolveCodexHome(value: string): Promise<string> {
  if (!path.isAbsolute(value)) throw new Error('CODEX_HOME must be an absolute path');
  const info = await lstat(value).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect CODEX_HOME');
  });
  if (!info) return path.resolve(value);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('CODEX_HOME must be a real directory, not a symbolic link');
  const canonical = await realpath(value).catch(() => { throw new Error('CODEX_HOME could not be resolved'); });
  const resolved = await lstat(canonical).catch(() => null);
  if (!resolved || !resolved.isDirectory() || resolved.isSymbolicLink()) throw new Error('CODEX_HOME must resolve to a real directory');
  return canonical;
}

async function listRolloutFiles(root: string): Promise<{ files: RolloutFile[]; symlinksSkipped: number; oversizedSkipped: number }> {
  const files: RolloutFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  const pending = [root];
  while (pending.length > 0) {
    const directory = pending.pop()!;
    const entries = await readdir(directory, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Codex rollout files'); });
    for (const entry of entries) {
      const absolutePath = path.join(directory, entry.name);
      const info = await lstat(absolutePath).catch((error: unknown) => {
        if (isCode(error, 'ENOENT')) return null;
        throw new Error('unable to inspect Codex rollout path');
      });
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (info.isDirectory()) {
        if (path.relative(root, absolutePath).split(path.sep).length > 10) throw new Error('Codex sessions exceed the directory-depth scan limit');
        pending.push(absolutePath);
        continue;
      }
      if (!info.isFile() || path.extname(entry.name).toLowerCase() !== '.jsonl') continue;
      if (info.size > maxRolloutBytes) { oversizedSkipped++; continue; }
      const relative = path.relative(root, absolutePath);
      if (!isSafeRelative(relative)) { symlinksSkipped++; continue; }
      const canonicalFile = await realpath(absolutePath).catch(() => '');
      if (!canonicalFile || !isWithin(root, canonicalFile)) { symlinksSkipped++; continue; }
      files.push({ key: relative.split(path.sep).join('/'), absolutePath: canonicalFile });
      if (files.length > maxFiles) throw new Error(`Codex sessions exceed the ${maxFiles}-file scan limit`);
    }
  }
  files.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  return { files, symlinksSkipped, oversizedSkipped };
}

function isSafeRelative(value: string): boolean {
  return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..');
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function readRolloutFile(filePath: string, root: string): Promise<{ bytes: Buffer; identity: FileIdentity }> {
  const before = await lstat(filePath).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink()) throw new Error('rollout is not a regular file');
  if (before.size > maxRolloutBytes) throw new Error('rollout exceeds the local file-size limit');
  const canonical = await realpath(filePath).catch(() => '');
  if (!canonical || !isWithin(root, canonical)) throw new Error('rollout path escaped the Codex sessions directory');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) {
      throw new Error('rollout changed before reading');
    }
    if (opened.size > maxRolloutBytes) throw new Error('rollout exceeds the local file-size limit');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(filePath).catch(() => null);
    if (bytes.byteLength > maxRolloutBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) {
      throw new Error('rollout changed while it was being read');
    }
    return { bytes, identity: identityOf(opened) };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('rollout ')) throw error;
    throw new Error('Codex rollout could not be opened safely');
  } finally {
    await handle?.close().catch(() => {});
  }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

function identityOf(info: Stats): FileIdentity {
  return { dev: info.dev, ino: info.ino, size: info.size, mtimeMs: info.mtimeMs };
}

type ParsedRollout = { records: RolloutRecord[]; completeLines: number; partialTail: boolean; malformed: number; lineEnds: number[] };

function parseRollout(bytes: Buffer): ParsedRollout {
  const records: RolloutRecord[] = [];
  const lineEnds: number[] = [];
  let malformed = 0;
  let line = 0;
  let start = 0;
  const decoder = new TextDecoder('utf-8', { fatal: true });
  for (let offset = 0; offset < bytes.byteLength; offset++) {
    if (bytes[offset] !== 0x0a) continue;
    line++;
    if (line > maxLinesPerFile) throw new Error(`rollout exceeds the ${maxLinesPerFile}-line processing limit`);
    lineEnds.push(offset + 1);
    let end = offset;
    if (end > start && bytes[end - 1] === 0x0d) end--;
    const raw = bytes.subarray(start, end);
    start = offset + 1;
    if (raw.byteLength === 0) continue;
    if (raw.byteLength > maxLineBytes) { malformed++; continue; }
    try {
      const parsed = JSON.parse(decoder.decode(raw)) as unknown;
      if (!isRecord(parsed) || !boundedString(parsed.type, 128)) { malformed++; continue; }
      if (!supportedEntryTypes.has(parsed.type as string)) continue;
      const payload = isRecord(parsed.payload) ? parsed.payload : {};
      records.push({ line, entry: parsed, payload, type: parsed.type as string, fingerprint: sha256(raw) });
    } catch {
      malformed++;
    }
  }
  const partialTail = start < bytes.byteLength;
  return { records, completeLines: line, partialTail, malformed, lineEnds };
}

function prefixHash(bytes: Buffer, line: number, parsed: ParsedRollout): string {
  const end = line > 0 ? parsed.lineEnds[line - 1] : 0;
  return sha256(bytes.subarray(0, end));
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

async function prospectiveRealPath(value: string): Promise<string> {
  let parent = path.dirname(path.resolve(value));
  const suffix = [path.basename(value)];
  while (true) {
    const info = await lstat(parent).catch((error: unknown) => {
      if (isCode(error, 'ENOENT')) return null;
      throw new Error('unable to inspect rollout cursor state path');
    });
    if (info) {
      if (!info.isDirectory()) throw new Error('rollout cursor state parent must be a directory');
      const canonical = await realpath(parent).catch(() => { throw new Error('rollout cursor state parent could not be resolved'); });
      return path.join(canonical, ...suffix);
    }
    const next = path.dirname(parent);
    if (next === parent) throw new Error('rollout cursor state path has no existing parent');
    suffix.unshift(path.basename(parent));
    parent = next;
  }
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  const parent = path.dirname(statePath);
  await mkdir(parent, { recursive: true, mode: 0o700 }).catch(() => { throw new Error('unable to create private rollout state directory'); });
  const parentInfo = await lstat(parent).catch(() => null);
  if (!parentInfo || !parentInfo.isDirectory() || parentInfo.isSymbolicLink()) throw new Error('rollout state parent must be a real directory');
  const lockPath = `${statePath}.lock`;
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Codex rollout sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Codex rollout state');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readState(statePath: string): Promise<RolloutState> {
  const empty: RolloutState = { schema_version: 'threadforge.codex-rollout-cursor.v1', version: stateVersion, files: {} };
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect rollout cursor state');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('rollout cursor state must be a regular, non-symlink file');
  if (info.size > maxStateBytes) throw new Error('rollout cursor state exceeds the local size limit');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('rollout cursor state changed before reading');
    const bytes = await handle.readFile();
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
      throw new Error('rollout cursor state has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, RolloutCursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!isSafeRelative(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
          typeof value.session_started !== 'boolean') {
        throw new Error('rollout cursor state contains an invalid file checkpoint');
      }
      files[key] = {
        line: value.line,
        size_bytes: value.size_bytes,
        mtime_ms: value.mtime_ms,
        prefix_sha256: value.prefix_sha256,
        session_started: value.session_started,
      };
    }
    return { schema_version: empty.schema_version, version: stateVersion, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('rollout cursor state')) throw error;
    throw new Error('rollout cursor state is malformed or unreadable');
  } finally {
    await handle?.close().catch(() => {});
  }
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

async function writeState(statePath: string, state: RolloutState): Promise<void> {
  const payload = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('rollout cursor state would exceed the local size limit');
  const tempPath = `${statePath}.${createHash('sha256').update(String(Date.now()) + String(Math.random())).digest('hex')}.tmp`;
  let handle;
  try {
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.chmod(0o600);
    await handle.writeFile(payload);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, statePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically write Codex rollout cursor state');
  }
}

function sha256(value: string | Buffer): string {
  return createHash('sha256').update(value).digest('hex');
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function boundedString(value: unknown, max: number): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined;
}

type TokenUsage = {
  input_tokens: number;
  cached_input_tokens: number;
  cache_write_input_tokens: number;
  output_tokens: number;
  reasoning_output_tokens: number;
  total_tokens: number;
};
type ToolCall = { id: string; name: string; input: unknown };
type MappedRolloutEvent = { sourceLine: number; event: ThreadforgeEvent };
type MapperState = {
  sessionID: string;
  model: string;
  started: boolean;
  tools: Map<string, ToolCall>;
  lastSessionTotal?: TokenUsage;
};

function mapRolloutRecords(
  records: RolloutRecord[],
  sourceHash: string,
  startLine: number,
  started: boolean,
  fallbackTimestamp: string,
  includeContent: boolean,
): { events: MappedRolloutEvent[] } {
  const tokenRecordTurns = new Set<string>();
  for (const record of records) {
    if (record.type !== 'token_usage_record') continue;
    const turnID = boundedString(record.payload.turn_id, 256);
    if (turnID) tokenRecordTurns.add(turnID);
  }
  const state: MapperState = { sessionID: sourceHash, model: '', started, tools: new Map() };
  const events: MappedRolloutEvent[] = [];
  const emit = (record: RolloutRecord, action: string, category: string, message: string, suffix: string, extra: JsonObject = {}, severity: 'info' | 'medium' = 'info', handoffPrompt?: string) => {
    if (record.line <= startLine) return;
    if (events.length >= maxEventsPerFile) throw new Error(`rollout exceeds the ${maxEventsPerFile}-event processing limit`);
    const timestamp = recordTimestamp(record, fallbackTimestamp);
    const eventID = sha256(`${sourceHash}:${record.line}:${suffix}:${record.fingerprint}`);
    const sessionHash = `codex:${sha256(state.sessionID || sourceHash)}`;
    const event: ThreadforgeEvent = {
      timestamp,
      vendor: 'beacon',
      product: 'endpoint-agent',
      schema_version: '1.0',
      event: { id: eventID, kind: 'agent_runtime', action, category, fidelity: 'observed' },
      severity,
      endpoint: { os: normalizedOS(process.platform) },
      harness: { name: 'codex-cli', collection_method: 'poll' },
      session: { id: sessionHash },
      raw: {
        source: 'codex_rollout_backfill',
        source_sha256: sourceHash,
        line: record.line,
        entry_type: record.type,
        ...(safeLabel(record.payload.type, 128) ? { payload_type: safeLabel(record.payload.type, 128) } : {}),
      },
      ...extra,
    };
    if (state.model) event.model = state.model;
    if (handoffPrompt !== undefined) markPromptHandoff(event, handoffPrompt);
    events.push({ sourceLine: record.line, event });
  };

  for (const record of records) {
    const { payload } = record;
    if (record.type === 'session_meta') {
      state.sessionID = firstString(payload, ['id', 'session_id']) ?? state.sessionID;
      if (record.line > startLine && !state.started) {
        emit(record, 'session.started', 'session', 'Codex session start recovered from local rollout', 'session.started');
        state.started = true;
      }
      continue;
    }

    if (record.type === 'turn_context') {
      const model = safeLabel(payload.model, 128);
      if (model) state.model = model;
      continue;
    }

    if (record.type === 'response_item') {
      const itemType = boundedString(payload.type, 128) ?? '';
      if (itemType === 'message') {
        const text = textFromContent(payload.content);
        if (!text) continue;
        const role = boundedString(payload.role, 32)?.toLowerCase();
        if (role !== 'user' && role !== 'assistant') continue;
        const content = contentMetadata(text, includeContent);
        if (role === 'user') {
          const extra: JsonObject = { content };
          if (includeContent) {
            const retained = retainText(text);
            extra.prompt = { text: retained };
            extra.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: retained }] }] } };
          }
          emit(record, 'prompt.submitted', 'prompt', 'Codex prompt recovered from local rollout', 'prompt', extra, 'info', text);
        } else {
          const extra: JsonObject = { content };
          if (includeContent) {
            const retained = retainText(text);
            extra.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: retained }] }] } };
          }
          emit(record, 'agent.message', 'agent', 'Codex assistant message recovered from local rollout', 'agent.message', extra);
        }
        continue;
      }

      if (['custom_tool_call', 'function_call'].includes(itemType)) {
        const callID = boundedString(payload.call_id, 512) ?? '';
        const name = safeLabel(payload.name, 128) ?? '';
        const input = itemType === 'function_call' ? parseArguments(payload.arguments, payload.input) : payload.input;
        const call = { id: callID, name, input };
        if (callID) state.tools.set(callID, call);
        if (!name) continue;
        const serialized = stableText(input);
        const content = input === undefined ? undefined : contentMetadata(serialized, includeContent);
        const retainArguments = includeContent && Buffer.byteLength(serialized, 'utf8') <= maxRetainedContentBytes;
        if (content && includeContent && !retainArguments) {
          content.included = false;
          content.retention = 'metadata';
          content.truncated = true;
        }
        const extra: JsonObject = { tool: { name }, ...(content ? { content } : {}) };
        if (callID) extra.raw = { source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type, tool_call_id_sha256: sha256(callID) };
        if (retainArguments) {
          extra.gen_ai = { tool: { name, call: { ...(callID ? { id: sha256(callID) } : {}), ...(input === undefined ? {} : { arguments: input }) } } };
          if (isCommandTool(name)) extra.tool = { name, command: retainText(commandFromTool(input)) };
        }
        emit(record, 'tool.invoked', 'tool', 'Codex tool invocation recovered from local rollout', `tool.invoked.${callID || record.line}`, extra);
        continue;
      }

      if (['custom_tool_call_output', 'function_call_output'].includes(itemType)) {
        const callID = boundedString(payload.call_id, 512) ?? '';
        const call = state.tools.get(callID) ?? { id: callID, name: '', input: undefined };
        const output = toolOutputText(payload.output);
        const failed = ['failed', 'failure', 'error'].includes((boundedString(payload.status, 32) ?? '').toLowerCase());
        const content = output ? contentMetadata(output, includeContent) : undefined;
        const extra: JsonObject = content ? { content } : {};
        if (call.name) extra.tool = { name: call.name, ...(includeContent && isCommandTool(call.name) ? { command: retainText(commandFromTool(call.input)) } : {}) };
        if (callID) extra.raw = { source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type, tool_call_id_sha256: sha256(callID) };
        const outputRetained = includeContent && Buffer.byteLength(output, 'utf8') <= maxRetainedContentBytes;
        if (includeContent && !outputRetained) {
          if (content) {
            content.included = false;
            content.retention = 'metadata';
            content.truncated = true;
          }
        }
        if (includeContent && outputRetained) {
          const inputSerialized = stableText(call.input);
          const retainInput = call.input !== undefined && Buffer.byteLength(inputSerialized, 'utf8') <= maxRetainedContentBytes;
          extra.gen_ai = { tool: { ...(call.name ? { name: call.name } : {}), call: { ...(callID ? { id: sha256(callID) } : {}), ...(retainInput ? { arguments: call.input } : {}), ...(output ? { result: retainText(output) } : {}) } } };
        }
        if (isCommandTool(call.name)) {
          if (outputRetained) extra.command = { command: retainText(commandFromTool(call.input)), output: retainText(output) };
          emit(record, 'command.executed', 'command', 'Codex command result recovered from local rollout', `tool.result.${callID || record.line}`, extra, failed ? 'medium' : 'info');
        } else {
          emit(record, failed ? 'tool.failed' : 'tool.completed', 'tool', 'Codex tool result recovered from local rollout', `tool.result.${callID || record.line}`, extra, failed ? 'medium' : 'info');
        }
        continue;
      }
    }

    if (record.type === 'event_msg') {
      const messageType = boundedString(payload.type, 128) ?? '';
      if (messageType === 'task_complete') {
        const extra: JsonObject = {};
        const duration = nonNegativeNumber(payload.duration_ms);
        const firstToken = nonNegativeNumber(payload.time_to_first_token_ms);
        const turnID = boundedString(payload.turn_id, 256);
        extra.raw = {
          source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type,
          payload_type: messageType, ...(turnID ? { turn_id_sha256: sha256(turnID) } : {}),
          ...(duration === undefined ? {} : { duration_ms: duration }),
          ...(firstToken === undefined ? {} : { time_to_first_token_ms: firstToken }),
        };
        emit(record, 'session.status', 'session', 'Codex task completion recovered from local rollout', 'task.complete', extra);
      } else if (messageType === 'token_count') {
        consumeTokenCount(record, sourceHash, payload, state, tokenRecordTurns, startLine, emit);
      }
      continue;
    }

    if (record.type === 'token_usage_record') {
      const turnID = boundedString(payload.turn_id, 256) ?? '';
      const sessionID = boundedString(payload.session_id, 256);
      if (sessionID) state.sessionID = sessionID;
      const usage = tokenUsage(payload.turn_token_usage) ?? tokenUsage(payload.usage);
      if (!usage || record.line <= startLine) continue;
      emitTokenUsage(record, sourceHash, usage, state, emit, turnID, boundedString(payload.response_id, 256));
    }
  }
  return { events };
}

function consumeTokenCount(
  record: RolloutRecord,
  sourceHash: string,
  payload: JsonObject,
  state: MapperState,
  authoritativeTurns: Set<string>,
  startLine: number,
  emit: (record: RolloutRecord, action: string, category: string, message: string, suffix: string, extra?: JsonObject, severity?: 'info' | 'medium') => void,
): void {
  const info = isRecord(payload.info) ? payload.info : {};
  const turnID = boundedString(payload.turn_id, 256) ?? '';
  const total = tokenUsage(info.total_token_usage);
  const previous = state.lastSessionTotal;
  if (total) {
    if (previous && sameUsage(previous, total)) return;
    state.lastSessionTotal = total;
  }
  if (turnID && authoritativeTurns.has(turnID)) return;
  if (record.line <= startLine) return;
  const hasLastSnapshot = isRecord(info.last_token_usage);
  const usage = hasLastSnapshot
    ? tokenUsage(info.last_token_usage)
    : total ? usageDelta(total, previous) : undefined;
  if (usage) emitTokenUsage(record, sourceHash, usage, state, emit, turnID);
}

function emitTokenUsage(
  record: RolloutRecord,
  sourceHash: string,
  usage: TokenUsage,
  state: MapperState,
  emit: (record: RolloutRecord, action: string, category: string, message: string, suffix: string, extra?: JsonObject, severity?: 'info' | 'medium') => void,
  turnID = '',
  responseID?: string,
): void {
  const cached = Math.max(usage.cached_input_tokens, 0);
  const cacheWrite = Math.max(usage.cache_write_input_tokens, 0);
  const extra: JsonObject = {
    gen_ai: {
      conversation: { id: `codex:${sha256(state.sessionID || sourceHash)}` },
      usage: {
        input_tokens: Math.max(usage.input_tokens - cached - cacheWrite, 0),
        output_tokens: Math.max(usage.output_tokens, 0),
        cache_read: { input_tokens: cached },
        cache_creation: { input_tokens: cacheWrite },
        reasoning: { output_tokens: Math.max(usage.reasoning_output_tokens, 0) },
      },
      ...(responseID ? { response: { id: sha256(responseID), ...(state.model ? { model: state.model } : {}) } } : {}),
    },
    raw: {
      source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type,
      ...(turnID ? { turn_id_sha256: sha256(turnID) } : {}),
    },
  };
  emit(record, 'token.usage', 'metric', 'Codex token usage recovered from local rollout', `usage.${turnID ? sha256(turnID) : record.line}`, extra);
}

function tokenUsage(value: unknown): TokenUsage | undefined {
  if (!isRecord(value)) return undefined;
  const usage: TokenUsage = {
    input_tokens: integerOrZero(value.input_tokens),
    cached_input_tokens: integerOrZero(value.cached_input_tokens),
    cache_write_input_tokens: integerOrZero(value.cache_write_input_tokens),
    output_tokens: integerOrZero(value.output_tokens),
    reasoning_output_tokens: integerOrZero(value.reasoning_output_tokens),
    total_tokens: integerOrZero(value.total_tokens),
  };
  return Object.values(usage).some((number) => number > 0) ? usage : undefined;
}

function integerOrZero(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : 0;
}

function nonNegativeNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function sameUsage(left: TokenUsage, right: TokenUsage): boolean {
  return Object.keys(left).every((key) => left[key as keyof TokenUsage] === right[key as keyof TokenUsage]);
}

function usageDelta(current: TokenUsage, previous?: TokenUsage): TokenUsage {
  if (!previous) return current;
  return {
    input_tokens: Math.max(current.input_tokens - previous.input_tokens, 0),
    cached_input_tokens: Math.max(current.cached_input_tokens - previous.cached_input_tokens, 0),
    cache_write_input_tokens: Math.max(current.cache_write_input_tokens - previous.cache_write_input_tokens, 0),
    output_tokens: Math.max(current.output_tokens - previous.output_tokens, 0),
    reasoning_output_tokens: Math.max(current.reasoning_output_tokens - previous.reasoning_output_tokens, 0),
    total_tokens: Math.max(current.total_tokens - previous.total_tokens, 0),
  };
}

function contentMetadata(value: string, includeContent: boolean): JsonObject {
  const bytes = Buffer.byteLength(value, 'utf8');
  const included = includeContent;
  return {
    retention: included ? 'full' : 'metadata',
    included,
    hash: sha256(value),
    bytes,
    ...(includeContent && bytes > genAIStoredStringLimit ? { truncated: true } : {}),
  };
}

function firstString(value: JsonObject, keys: string[]): string | undefined {
  for (const key of keys) {
    const candidate = boundedString(value[key], 256);
    if (candidate) return candidate;
  }
  return undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const label = boundedString(value, max);
  if (!label || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(label) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(label) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(label)) return undefined;
  return label;
}

function stableText(value: unknown): string {
  if (value === undefined) return '';
  try { return JSON.stringify(canonicalize(value)) ?? ''; }
  catch { return ''; }
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function parseArguments(value: unknown, fallback: unknown): unknown {
  if (typeof value !== 'string' || !value.trim()) return fallback;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}

function textFromContent(value: unknown): string {
  if (typeof value === 'string') return value.trim();
  if (!Array.isArray(value)) return '';
  const parts: string[] = [];
  for (const raw of value) {
    if (!isRecord(raw)) continue;
    const type = boundedString(raw.type, 64) ?? '';
    if (!['input_text', 'output_text', 'text', 'summary_text', 'reasoning', 'reasoning_text'].includes(type)) continue;
    const text = firstText(raw, ['text', 'content', 'summary']);
    if (text) parts.push(text);
  }
  return parts.join('\n').trim();
}

function toolOutputText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) {
    return value.map((part) => {
      if (typeof part === 'string') return part;
      return isRecord(part) ? firstText(part, ['text', 'content', 'output']) ?? '' : '';
    }).filter(Boolean).join('\n');
  }
  return stableText(value);
}

function commandFromTool(value: unknown): string {
  if (typeof value === 'string') return value.trim();
  if (!isRecord(value)) return stableText(value);
  for (const key of ['command', 'cmd', 'shell_command']) {
    if (typeof value[key] === 'string' && value[key].trim()) return value[key].trim();
    if (Array.isArray(value[key])) return value[key].filter((part): part is string => typeof part === 'string').join(' ').trim();
  }
  return stableText(value);
}

function isCommandTool(name: string): boolean {
  return ['exec', 'exec_command', 'shell', 'bash'].includes(name.toLowerCase());
}

function recordTimestamp(record: RolloutRecord, fallback: string): string {
  const candidates = [record.entry.timestamp, record.payload.timestamp];
  if (record.type === 'session_meta') candidates.push(record.payload.timestamp);
  for (const candidate of candidates) {
    if (typeof candidate !== 'string') continue;
    const date = new Date(candidate);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  return fallback;
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function firstText(value: JsonObject, keys: string[]): string | undefined {
  for (const key of keys) {
    if (typeof value[key] === 'string') return value[key] as string;
  }
  return undefined;
}

function retainText(value: string): string {
  if (Buffer.byteLength(value, 'utf8') <= maxRetainedContentBytes) return value;
  let output = '';
  for (const char of value) {
    if (Buffer.byteLength(output + char, 'utf8') > maxRetainedContentBytes) break;
    output += char;
  }
  return output;
}

async function processRolloutFiles(
  files: RolloutFile[],
  root: string,
  state: RolloutState,
  options: CodexRolloutSyncOptions,
  result: CodexRolloutSyncResult,
  persist: boolean,
  statePath?: string,
): Promise<void> {
  for (const file of files) {
    const sourceHash = sha256(file.key);
    try {
      const { bytes, identity } = await readRolloutFile(file.absolutePath, root);
      const parsed = parseRollout(bytes);
      result.malformed_lines += parsed.malformed;
      if (parsed.partialTail) result.partial_sessions++;

      const stored = state.files[file.key];
      let cursorLine = stored?.line ?? 0;
      let sessionStarted = stored?.session_started ?? false;
      if (cursorLine > parsed.completeLines || prefixHash(bytes, cursorLine, parsed) !== (stored?.prefix_sha256 ?? sha256(Buffer.alloc(0)))) {
        cursorLine = 0;
        sessionStarted = false;
      }

      const fallbackTimestamp = new Date(identity.mtimeMs).toISOString();
      const mapped = mapRolloutRecords(parsed.records, sourceHash, cursorLine, sessionStarted, fallbackTimestamp, options.includeContent ?? false);
      if (mapped.events.length > maxEventsPerFile) throw new Error(`rollout exceeds the ${maxEventsPerFile}-event processing limit`);

      if (options.preview) {
        result.events_previewed += mapped.events.length;
        for (const item of mapped.events) {
          if (result.events!.length < maxPreviewEvents) result.events!.push(item.event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let acknowledgedLine = cursorLine;
      for (let offset = 0; offset < mapped.events.length; offset += eventBatchSize) {
        const batch = mapped.events.slice(offset, offset + eventBatchSize);
        await emitEvents(batch.map((item) => item.event), { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        acknowledgedLine = Math.max(acknowledgedLine, ...batch.map((item) => item.sourceLine));
        if (batch.some((item) => item.event.event.action === 'session.started')) sessionStarted = true;
        state.files[file.key] = makeCursor(acknowledgedLine, identity, prefixHash(bytes, acknowledgedLine, parsed), sessionStarted);
        result.events_emitted += batch.length;
        if (statePath) await writeState(statePath, state);
      }

      const previousLine = stored?.line ?? 0;
      if (parsed.completeLines > acknowledgedLine) acknowledgedLine = parsed.completeLines;
      if (mapped.events.length > 0 || acknowledgedLine > previousLine || !stored) {
        state.files[file.key] = makeCursor(acknowledgedLine, identity, prefixHash(bytes, acknowledgedLine, parsed), sessionStarted);
        result.sessions_changed++;
        if (persist && statePath) await writeState(statePath, state);
      }
    } catch (error) {
      result.errors.push({
        source_sha256: sourceHash,
        message: error instanceof Error ? error.message.slice(0, 256) : 'Codex rollout could not be processed',
      });
    }
  }
}

function makeCursor(line: number, identity: FileIdentity, prefix: string, sessionStarted: boolean): RolloutCursor {
  return {
    line,
    size_bytes: identity.size,
    mtime_ms: identity.mtimeMs,
    prefix_sha256: prefix,
    session_started: sessionStarted,
  };
}
