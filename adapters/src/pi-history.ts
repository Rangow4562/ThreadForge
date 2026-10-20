import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const cursorVersion = 1;
const cursorName = 'pi-session-cursor.v1.json';
const maxCursorBytes = 8 << 20;
const defaultMaxFiles = 1_000;
const maxFiles = 15_000;
const maxScanEntries = 100_000;
const maxFileBytes = 64 << 20;
const maxTotalBytes = 512 << 20;
const maxLineBytes = 10 << 20;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxCallCorrelations = 10_000;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const eventBatchSize = 100;

type JsonObject = Record<string, unknown>;
type PiCursor = {
  last_line: number;
  started: boolean;
  prefix_sha256: string;
  size_bytes: number;
  mtime_ms: number;
  incomplete: boolean;
};
type CursorState = {
  schema_version: 'threadforge.pi-session-cursor.v1';
  version: number;
  files: Record<string, PiCursor>;
};
type PiFile = { path: string; relative: string; sourceSha256: string; name: string; size: number; mtimeMs: number };
type PiEntry = { line: number; value: JsonObject };
type ParsedFile = {
  entries: PiEntry[];
  completeLines: number;
  prefixHashes: Map<number, string>;
  completePrefixHash: string;
  partialTail: boolean;
  malformed: number;
};
type ToolCallState = { id: string; name: string; args: unknown };
type MapperState = {
  sessionId: string;
  sessionIdHash: string;
  cwd?: string;
  model?: string;
  repository?: string;
  branch?: string;
  calls: Map<string, ToolCallState>;
  started: boolean;
};
type MappedGroup = { line: number; prefix: string; events: ThreadforgeEvent[]; started: boolean };

export type PiHistoryOptions = {
  sessionsDir?: string;
  statePath?: string;
  endpoint?: string;
  maxFiles?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type PiHistoryResult = {
  schema_version: 'threadforge.pi-history-sync.v1';
  files: number;
  files_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_lines: number;
  partial_files: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
  preview_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type PiHistoryStatus = {
  schema_version: 'threadforge.pi-history-status.v1';
  files: number;
  checkpointed_files: number;
  pending_files: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
};

const toolCallKinds = new Set(['tool_call', 'tool_use', 'function_call', 'toolcall']);
const toolResultKinds = new Set(['tool_result', 'tool_output', 'function_call_output', 'toolresult']);

export async function getPiHistoryStatus(
  options: Pick<PiHistoryOptions, 'sessionsDir' | 'statePath' | 'maxFiles'> = {},
): Promise<PiHistoryStatus> {
  const maximum = boundedInteger(options.maxFiles ?? defaultMaxFiles, 1, maxFiles, '--max-files');
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('Pi history cursor must be stored outside Pi sessions');
  const listed = await listFiles(sessionsDir, maximum);
  const state = await readState(statePath);
  const checkpointed = listed.files.filter((file) => {
    const cursor = state.files[file.sourceSha256];
    return cursor !== undefined && !cursor.incomplete && cursor.last_line > 0 &&
      cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
  }).length;
  return {
    schema_version: 'threadforge.pi-history-status.v1', files: listed.files.length,
    checkpointed_files: checkpointed, pending_files: listed.files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    files_truncated: listed.truncated,
  };
}

export async function syncPiHistory(options: PiHistoryOptions = {}): Promise<PiHistoryResult> {
  validateOptions(options);
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('Pi history cursor must be stored outside Pi sessions');
  const listed = await listFiles(sessionsDir, options.maxFiles ?? defaultMaxFiles);
  const result = emptyResult(options.includeContent ?? false);
  result.files = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.files_truncated = listed.truncated;
  if (options.preview) {
    await processFiles(listed.files, sessionsDir, await readState(statePath), options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFiles(listed.files, sessionsDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_files === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
  return result;
}

function emptyResult(includeContent: boolean): PiHistoryResult {
  return {
    schema_version: 'threadforge.pi-history-sync.v1', files: 0, files_changed: 0, events_emitted: 0,
    events_previewed: 0, malformed_lines: 0, partial_files: 0, symlinks_skipped: 0, oversized_files_skipped: 0,
    files_truncated: false, preview_truncated: false, content_mode: includeContent ? 'include-content' : 'metadata',
    complete: false, errors: [],
  };
}

function validateOptions(options: PiHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxFiles ?? defaultMaxFiles, 1, maxFiles, '--max-files');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveSessionsDir(value?: string): Promise<string> {
  let sessionsDir = value;
  if (!sessionsDir) {
    const home = os.homedir();
    const override = process.env.PI_CODING_AGENT_DIR?.trim();
    const agentDir = override ? expandTilde(override, home) : path.join(home, '.pi', 'agent');
    if (!path.isAbsolute(agentDir)) throw new Error('PI_CODING_AGENT_DIR must resolve to an absolute path');
    sessionsDir = path.join(agentDir, 'sessions');
  }
  if (!path.isAbsolute(sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  const info = await lstat(sessionsDir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return path.resolve(sessionsDir);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Pi sessions path must be a real directory');
  const canonical = await realpath(sessionsDir).catch(() => { throw new Error('Pi sessions directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, sessionsDir)) throw new Error('Pi sessions directory must not traverse symbolic links');
  return canonical;
}

function expandTilde(value: string, home: string): string {
  if (value === '~') return home;
  if (value.startsWith('~/') || value.startsWith('~\\')) return path.join(home, value.slice(2));
  return value;
}

async function listFiles(
  root: string, maximum: number,
): Promise<{ files: PiFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Pi sessions path must be a real directory');
  const files: PiFile[] = [];
  const pending = [root];
  let entriesScanned = 0;
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  while (pending.length > 0) {
    const directory = pending.pop()!;
    const entries = await readdir(directory, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Pi session files'); });
    for (const entry of entries) {
      entriesScanned++;
      if (entriesScanned > maxScanEntries) { truncated = true; break; }
      const fullPath = path.join(directory, entry.name);
      const info = await lstat(fullPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (info.isDirectory()) { pending.push(fullPath); continue; }
      if (!info.isFile() || path.extname(entry.name).toLowerCase() !== '.jsonl') continue;
      if (info.size > maxFileBytes) { oversizedSkipped++; continue; }
      const canonical = await realpath(fullPath).catch(() => '');
      if (!canonical || !await isSameRealPathWithoutLinks(canonical, fullPath) || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
      const relative = path.relative(root, canonical).split(path.sep).join('/');
      if (!isSafeRelative(relative)) { symlinksSkipped++; continue; }
      files.push({ path: canonical, relative, sourceSha256: sha256(relative), name: path.basename(fullPath), size: info.size, mtimeMs: info.mtimeMs });
      if (files.length > maximum) { truncated = true; break; }
    }
    if (truncated) break;
  }
  files.sort((left, right) => left.mtimeMs - right.mtimeMs || (left.relative < right.relative ? -1 : left.relative > right.relative ? 1 : 0));
  if (files.length > maximum) files.length = maximum;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

async function readPiFile(file: PiFile, root: string): Promise<Buffer> {
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size !== file.size || before.mtimeMs !== file.mtimeMs || before.size > maxFileBytes) {
    throw new Error('Pi transcript changed after enumeration');
  }
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error('Pi transcript path escaped sessions directory');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || opened.size !== file.size || opened.mtimeMs !== file.mtimeMs) throw new Error('Pi transcript changed before read');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) throw new Error('Pi transcript changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Pi transcript')) throw error;
    throw new Error('Pi transcript could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

function parsePiFile(bytes: Buffer): ParsedFile {
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const entries: PiEntry[] = [];
  let offset = 0;
  let line = 0;
  let malformed = 0;
  let partialTail = false;
  let completeLines = 0;
  while (offset < bytes.length) {
    const newline = bytes.indexOf(0x0a, offset);
    if (newline < 0) { partialTail = bytes.length > offset; break; }
    line++;
    if (line > maxLinesPerFile) { malformed++; break; }
    const start = offset;
    const end = newline > offset && bytes[newline - 1] === 0x0d ? newline - 1 : newline;
    const raw = bytes.subarray(offset, end);
    offset = newline + 1;
    if (raw.byteLength > maxLineBytes) { malformed++; break; }
    if (raw.toString('utf8').trim() === '') {
      prefixHasher.update(bytes.subarray(start, newline + 1));
      completeLines = line;
      prefixHashes.set(line, prefixHasher.copy().digest('hex'));
      continue;
    }
    let parsed: unknown;
    try { parsed = JSON.parse(decoder.decode(raw)); }
    catch { malformed++; break; }
    if (!isRecord(parsed)) { malformed++; break; }
    prefixHasher.update(bytes.subarray(start, newline + 1));
    completeLines = line;
    prefixHashes.set(line, prefixHasher.copy().digest('hex'));
    entries.push({ line, value: parsed });
  }
  return { entries, completeLines, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex'), partialTail, malformed };
}

function mapPiEntries(
  file: PiFile, parsed: ParsedFile, minLine: number, started: boolean, includeContent: boolean,
): { groups: MappedGroup[]; state: MapperState } {
  const header = parsed.entries.find((entry) => typeOf(entry.value) === 'session')?.value ?? {};
  const fallbackId = path.basename(file.name, path.extname(file.name));
  const state: MapperState = {
    sessionId: boundedString(header.id, 512) ?? fallbackId,
    sessionIdHash: sha256(boundedString(header.id, 512) ?? fallbackId),
    ...(boundedString(header.cwd, 4_096) ? { cwd: String(header.cwd) } : {}),
    ...(safeLabel(header.model ?? header.modelId, 160) ? { model: safeLabel(header.model ?? header.modelId, 160) } : {}),
    ...(isRecord(header.git) && boundedString(header.git.repoUrl, 2_048) ? { repository: String(header.git.repoUrl) } : {}),
    ...(isRecord(header.git) && safeLabel(header.git.branch, 256) ? { branch: safeLabel(header.git.branch, 256) } : {}),
    calls: new Map(), started,
  };
  const groups: MappedGroup[] = [];
  if (!started && parsed.entries.length > 0) {
    const event = makePiEvent(file, 0, 'session.started', 'session.started', 'session', 'Pi session start recovered from local transcript',
      state, timestampValue(header.timestamp, file.mtimeMs), { fx: {
        ...(state.cwd ? { cwd_sha256: sha256(state.cwd) } : {}),
        ...(state.repository ? { repository_sha256: sha256(state.repository) } : {}),
        ...(state.branch ? { branch_sha256: sha256(state.branch) } : {}),
      } });
    groups.push({ line: 0, prefix: sha256(Buffer.alloc(0)), events: [event], started: true });
    state.started = true;
  }
  let eventsInFile = groups[0]?.events.length ?? 0;
  for (const entry of parsed.entries) {
    const events: ThreadforgeEvent[] = [];
    const emit = entry.line > minLine;
    mapPiEntry(events, file, entry, state, emit, includeContent);
    eventsInFile += events.length;
    if (eventsInFile > maxEventsPerFile) throw new Error(`Pi transcript exceeds the ${maxEventsPerFile}-event processing limit`);
    const prefix = parsed.prefixHashes.get(entry.line);
    if (prefix) groups.push({ line: entry.line, prefix, events, started: state.started });
  }
  return { groups, state };
}

function mapPiEntry(
  events: ThreadforgeEvent[], file: PiFile, entry: PiEntry, state: MapperState, emit: boolean, includeContent: boolean,
): void {
  const data = entry.value;
  const type = typeOf(data);
  const timestamp = timestampValue(data.timestamp, file.mtimeMs);
  if (type === 'session') {
    const id = boundedString(data.id, 512);
    if (id) { state.sessionId = id; state.sessionIdHash = sha256(id); }
    const cwd = boundedString(data.cwd, 4_096);
    if (cwd) state.cwd = cwd;
    const model = safeLabel(data.model ?? data.modelId, 160);
    if (model) state.model = model;
    if (isRecord(data.git)) {
      const repository = boundedString(data.git.repoUrl, 2_048);
      const branch = safeLabel(data.git.branch, 256);
      if (repository) state.repository = repository;
      if (branch) state.branch = branch;
    }
    return;
  }
  if (type === 'model_change') {
    const model = safeLabel(data.modelId ?? data.model, 160);
    if (model) {
      state.model = model;
      if (emit) {
        const event = makePiEvent(file, entry.line, 'model_change', 'session.context', 'session', 'Pi model changed', state, timestamp,
          { fx: { change: 'model_change', model } });
        pushPiEvent(events, event);
      }
    }
    return;
  }
  if (type === 'compaction') {
    if (emit) {
      const summary = textValue(data.summary);
      const event = makePiEvent(file, entry.line, 'compaction', 'session.compacting', 'session', 'Pi compacted session history', state, timestamp,
        { fx: {
          ...(summary ? { summary: contentDigest(summary) } : {}),
          ...(safeOpaque(data.firstKeptEntryId, 512) ? { first_kept_entry_sha256: sha256(String(data.firstKeptEntryId)) } : {}),
          ...(nonNegativeInteger(data.tokensBefore) !== undefined ? { tokens_before: data.tokensBefore } : {}),
        } });
      pushPiEvent(events, event);
    }
    return;
  }
  if (type === 'thinking_level_change') return;
  if (type === 'custom') {
    if (data.customType !== 'context:skill_loaded' || !emit || !isRecord(data.data)) return;
    const name = safeLabel(data.data.name, 128);
    if (!name) return;
    const rawId = safeOpaque(data.id, 512) ?? `line-${entry.line}-skill`;
    const call: ToolCallState = { id: rawId, name: 'skill', args: { name, ...(boundedString(data.data.path, 4_096) ? { path: data.data.path } : {}) } };
    const invoke = makeToolEvent(file, entry.line, 'skill.loaded.call', 'tool.invoked', 'tool', 'Pi skill load observed', state, timestamp, call);
    const complete = makeToolEvent(file, entry.line, 'skill.loaded.result', 'tool.completed', 'tool', 'Pi skill load completed', state, timestamp, call);
    complete.raw = { ...complete.raw, fx: { custom_type: 'context:skill_loaded', skill_name: name,
      ...(boundedString(data.data.path, 4_096) ? { skill_path_sha256: sha256(String(data.data.path)) } : {}) } };
    pushPiEvent(events, invoke);
    pushPiEvent(events, complete);
    return;
  }
  if (type === 'message') {
    if (isRecord(data.message)) mapMessage(events, file, entry.line, data.message, timestamp, state, emit, includeContent);
    return;
  }
  if (toolCallKinds.has(type)) {
    const call = makeCallState(entry.line, 0, data);
    rememberCall(state.calls, call);
    if (emit) pushPiEvent(events, makeToolEvent(file, entry.line, 'tool_call', 'tool.invoked', 'tool', 'Pi tool invoked', state, timestamp, call));
    return;
  }
  if (toolResultKinds.has(type)) {
    if (emit) mapToolResult(events, file, entry.line, timestamp, state, data, safeOpaque(data.toolCallId ?? data.callId ?? data.id, 512) ?? `line-${entry.line}`);
    return;
  }
  if (type === 'bashexecution' || type === 'bash_execution') {
    if (emit) mapBashExecution(events, file, entry.line, timestamp, state, data);
    return;
  }
  if (type === 'error' && emit) mapPiError(events, file, entry.line, timestamp, state, data);
}

function pushPiEvent(events: ThreadforgeEvent[], event: ThreadforgeEvent): void {
  if (events.length >= eventBatchSize) throw new Error(`Pi transcript row exceeds the ${eventBatchSize}-event batch limit`);
  events.push(event);
}

function mapMessage(
  events: ThreadforgeEvent[], file: PiFile, line: number, message: JsonObject, fallback: string,
  state: MapperState, emit: boolean, includeContent: boolean,
): void {
  const role = String(message.role ?? '').toLowerCase();
  const timestamp = timestampValue(message.timestamp, fallback);
  if (role === 'user') {
    if (emit) {
      const prompts = textParts(message.content);
      for (let index = 0; index < prompts.length; index++) {
        pushPiEvent(events, makeTextEvent(file, line, `user.${index}`, 'prompt.submitted', 'prompt', 'Pi prompt recovered from local transcript',
          state, timestamp, prompts[index]!, includeContent, 'user'));
      }
    }
    return;
  }
  if (role === 'assistant') {
    const model = safeLabel(message.model ?? message.modelId, 160);
    if (model) state.model = model;
    const parts = Array.isArray(message.content) ? message.content : typeof message.content === 'string' ? [{ type: 'text', text: message.content }] : [];
    for (let index = 0; index < parts.length; index++) {
      const part = isRecord(parts[index]) ? parts[index] as JsonObject : {};
      const kind = String(part.type ?? '').toLowerCase();
      if (kind === 'thinking') {
        const text = textValue(part.thinking ?? part.text ?? part.content);
        if (emit && text) pushPiEvent(events, makePiEvent(file, line, `assistant.${index}.thinking`, 'agent.reasoning', 'agent', 'Pi reasoning observed',
          state, timestamp, { content: contentDigest(text) }));
      } else if (kind === 'text') {
        const text = textValue(part.text ?? part.content);
        if (emit && text) pushPiEvent(events, makeTextEvent(file, line, `assistant.${index}.text`, 'agent.message', 'agent',
          'Pi assistant message recovered from local transcript', state, timestamp, text, includeContent, 'assistant'));
      } else if (toolCallKinds.has(kind)) {
        const call = makeCallState(line, index, part);
        rememberCall(state.calls, call);
        if (emit) pushPiEvent(events, makeToolEvent(file, line, `assistant.${index}.tool_call`, 'tool.invoked', 'tool', 'Pi tool invoked', state, timestamp, call));
      } else if (toolResultKinds.has(kind) && emit) {
        const callId = safeOpaque(part.callId ?? part.toolCallId ?? part.id, 512) ?? `line-${line}-call-${index}`;
        mapToolResult(events, file, line, timestamp, state, part, callId, index);
      }
    }
    if (emit) mapPiUsage(events, file, line, timestamp, state, isRecord(message.usage) ? message.usage : undefined);
    return;
  }
  if (['toolresult', 'tool_result', 'tool'].includes(role) && emit) {
    const callId = safeOpaque(message.toolCallId ?? message.callId, 512) ?? `line-${line}`;
    mapToolResult(events, file, line, timestamp, state, message, callId);
  } else if ((role === 'bashexecution' || role === 'bash_execution') && emit) {
    mapBashExecution(events, file, line, timestamp, state, message);
  } else if (role === 'error' && emit) {
    mapPiError(events, file, line, timestamp, state, message);
  }
}

function makeCallState(line: number, index: number, value: JsonObject): ToolCallState {
  const nested = firstRecord(value.toolCall, value.functionCall);
  const id = safeOpaque(value.callId ?? value.toolCallId ?? nested.id ?? nested.callId ?? value.id, 512) ?? `line-${line}-call-${index}`;
  const name = safeLabel(value.name ?? value.toolName ?? nested.name ?? nested.toolName, 128) ?? 'unknown';
  const args = parseArgs(value.args ?? value.input ?? value.arguments ?? nested.args ?? nested.input ?? nested.arguments);
  return { id, name, args };
}

function rememberCall(calls: Map<string, ToolCallState>, call: ToolCallState): void {
  if (calls.size >= maxCallCorrelations && !calls.has(call.id)) calls.delete(calls.keys().next().value!);
  calls.set(call.id, call);
}

function mapToolResult(
  events: ThreadforgeEvent[], file: PiFile, line: number, timestamp: string, state: MapperState,
  data: JsonObject, callId: string, partIndex?: number,
): void {
  const call = state.calls.get(callId) ?? {
    id: callId, name: safeLabel(data.toolName ?? data.name, 128) ?? 'unknown',
    args: parseArgs(data.args ?? data.input ?? data.arguments),
  };
  const outputValue = data.output ?? data.result ?? data.content ?? (isRecord(data.toolResult) ? data.toolResult.output : undefined);
  const output = extractText(outputValue);
  const failed = data.isError === true || data.is_error === true || isErrorStatus(data.status) ||
    (isRecord(data.details) && isErrorStatus(data.details.status));
  const args = isRecord(call.args) ? call.args : {};
  const pathValue = boundedString(args.path ?? args.file_path ?? args.target_path, 4_096);
  const lower = call.name.toLowerCase();
  let action = 'tool.completed';
  let category = 'tool';
  let severity: ThreadforgeEvent['severity'] = 'info';
  let message = 'Pi tool result observed';
  if (lower === 'bash' || commandFor(call.name, args)) { action = 'command.executed'; category = 'command'; message = 'Pi command executed'; }
  else if (pathValue && lower === 'read') { action = 'file.read'; category = 'file'; message = 'Pi file read'; }
  else if (pathValue && lower === 'write') { action = 'file.created'; category = 'file'; message = 'Pi file created'; }
  else if (pathValue && lower === 'edit') { action = 'file.modified'; category = 'file'; message = 'Pi file modified'; }
  else if (isMcpTool(call.name)) { action = 'mcp.tool_invoked'; category = 'mcp'; message = 'Pi MCP tool invoked'; }
  else if (failed) { action = 'tool.failed'; category = 'tool'; severity = 'high'; message = 'Pi tool failed'; }
  const event = makeToolEvent(file, line, `tool_result${partIndex === undefined ? '' : `.${partIndex}`}`, action,
    category, message, state, timestamp, call, severity);
  if (failed) event.error = { type: 'tool_error' };
  if (output) event.content = contentDigest(output);
  if (category === 'command') {
    const command = commandFor(call.name, args);
    const commandInfo: JsonObject = {};
    if (command) { commandInfo.command_sha256 = sha256(command); commandInfo.command_bytes = Buffer.byteLength(command, 'utf8'); }
    if (output) { commandInfo.output_sha256 = sha256(output); commandInfo.output_bytes = Buffer.byteLength(output, 'utf8'); }
    event.command = commandInfo;
  }
  if (category === 'file') {
    const detail = isRecord(data.details) ? data.details : {};
    const diff = boundedString(detail.patch ?? detail.diff, maxLineBytes);
    const fileInfo: JsonObject = { ...(pathValue ? { path_sha256: sha256(pathValue) } : {}), operation: fileOperation(call.name) };
    if (diff) { fileInfo.diff_sha256 = sha256(diff); fileInfo.diff_bytes = Buffer.byteLength(diff, 'utf8'); event.content = contentDigest(diff); }
    event.file = fileInfo;
  }
  const raw: JsonObject = { ...(data.status === undefined ? {} : { status: safeLabel(data.status, 32) ?? 'unknown' }) };
  if (typeof data.output === 'string') raw.output_bytes = Buffer.byteLength(data.output, 'utf8');
  if (typeof data.truncated === 'boolean') raw.truncated = data.truncated;
  if (safeOpaque(callId, 512)) raw.tool_call_sha256 = sha256(callId);
  event.raw = { ...event.raw, fx: raw };
  if (category === 'mcp') event.mcp = piMcpFields(call.name);
  pushPiEvent(events, event);
  state.calls.delete(callId);
}

function mapBashExecution(
  events: ThreadforgeEvent[], file: PiFile, line: number, timestamp: string, state: MapperState, data: JsonObject,
): void {
  const command = boundedString(data.command, 1 << 20);
  if (!command) return;
  const output = extractText(data.output);
  const call: ToolCallState = { id: `line-${line}-bash`, name: 'bash', args: { command } };
  const event = makeToolEvent(file, line, 'bash_execution', 'command.executed', 'command', 'Pi bash command executed', state, timestamp, call);
  const commandInfo: JsonObject = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
  if (output) {
    commandInfo.output_sha256 = sha256(output);
    commandInfo.output_bytes = Buffer.byteLength(output, 'utf8');
    event.content = contentDigest(output);
  }
  if (nonNegativeInteger(data.exitCode) !== undefined) commandInfo.exit_code = data.exitCode;
  event.command = commandInfo;
  if (data.cancelled === true || (typeof data.exitCode === 'number' && data.exitCode !== 0)) event.error = { type: 'command_failed' };
  event.raw = { ...event.raw, fx: { ...(typeof data.cancelled === 'boolean' ? { cancelled: data.cancelled } : {}) } };
  pushPiEvent(events, event);
}

function mapPiError(events: ThreadforgeEvent[], file: PiFile, line: number, timestamp: string, state: MapperState, data: JsonObject): void {
  const text = firstText(data.errorMessage, data.message, data.content) || 'Pi runtime error observed';
  const event = makePiEvent(file, line, 'error', 'agent.error', 'session', 'Pi runtime error observed', state, timestamp, {
    severity: 'high', content: contentDigest(text), fx: { error_sha256: sha256(text), error_bytes: Buffer.byteLength(text, 'utf8') },
  });
  event.error = { type: 'runtime_error' };
  pushPiEvent(events, event);
}

function mapPiUsage(
  events: ThreadforgeEvent[], file: PiFile, line: number, timestamp: string, state: MapperState, value?: JsonObject,
): void {
  if (!value) return;
  const usage: JsonObject = {};
  const input = nonNegativeInteger(value.input);
  const output = nonNegativeInteger(value.output);
  const cacheRead = nonNegativeInteger(value.cacheRead);
  const cacheWrite = nonNegativeInteger(value.cacheWrite);
  const reasoning = nonNegativeInteger(value.reasoning);
  const cost = isRecord(value.cost) ? finiteNonNegative(value.cost.total) : undefined;
  if (input !== undefined) usage.input_tokens = input;
  if (output !== undefined) usage.output_tokens = output;
  if (cacheRead !== undefined) usage.cache_read_input_tokens = cacheRead;
  if (cacheWrite !== undefined) usage.cache_creation_input_tokens = cacheWrite;
  if (reasoning !== undefined) usage.reasoning_tokens = reasoning;
  if (cost !== undefined) usage.cost_usd = cost;
  if (Object.keys(usage).length === 0) return;
  const event = makePiEvent(file, line, 'usage', 'token.usage', 'metric', 'Pi token usage recovered from local transcript', state, timestamp);
  event.gen_ai = { usage };
  pushPiEvent(events, event);
}

function makeTextEvent(
  file: PiFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string, text: string, includeContent: boolean, role: 'user' | 'assistant',
): ThreadforgeEvent {
  const allowed = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
  const event = makePiEvent(file, line, suffix, action, category, message, state, timestamp,
    { content: contentDigest(text, allowed, includeContent) });
  if (allowed) {
    if (role === 'user') {
      event.prompt = { text };
      event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } };
    } else {
      event.gen_ai = { ...(isRecord(event.gen_ai) ? event.gen_ai : {}), output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } };
    }
  }
  return event;
}

function makeToolEvent(
  file: PiFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string, call: ToolCallState, severity: ThreadforgeEvent['severity'] = 'info',
): ThreadforgeEvent {
  const name = safeLabel(call.name, 128) ?? 'unknown';
  const callIdHash = sha256(call.id);
  const argsDigest = digestUnknown(call.args);
  const event = makePiEvent(file, line, suffix, action, category, message, state, timestamp,
    { severity, ...(argsDigest ? { content: argsDigest } : {}) });
  event.tool = { name };
  event.gen_ai = {
    ...(isRecord(event.gen_ai) ? event.gen_ai : {}), operation: { name: 'execute_tool' },
    tool: { name, call: { id: `pi-call:${callIdHash}`, ...(argsDigest ? { arguments: argsDigest } : {}) } },
  };
  const args = isRecord(call.args) ? call.args : {};
  const command = commandFor(name, args);
  const filePath = boundedString(args.path ?? args.file_path ?? args.target_path, 4_096);
  if (command) event.tool = { name, command_sha256: sha256(command) };
  if (filePath) event.tool = { ...(isRecord(event.tool) ? event.tool : { name }), path_sha256: sha256(filePath) };
  if (category === 'mcp') event.mcp = piMcpFields(name);
  event.raw = { ...event.raw, fx: { tool_call_sha256: callIdHash, ...(argsDigest ? { arguments: argsDigest } : {}) } };
  return event;
}

function makePiEvent(
  file: PiFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string,
  extra: { content?: JsonObject; severity?: ThreadforgeEvent['severity']; fx?: JsonObject } = {},
): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${file.sourceSha256}:${state.sessionIdHash}:${line}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'pi_cli', collection_method: 'poll' }, session: { id: `pi:${state.sessionIdHash}` },
    raw: {
      source: 'pi_session_jsonl', source_sha256: file.sourceSha256, line, message,
      ...(state.cwd ? { cwd_sha256: sha256(state.cwd) } : {}),
      ...(state.repository ? { repository_sha256: sha256(state.repository) } : {}),
      ...(state.branch ? { branch_sha256: sha256(state.branch) } : {}),
      ...(extra.fx ? { fx: extra.fx } : {}),
    },
  };
  if (state.model) event.model = state.model;
  if (extra.content) event.content = extra.content;
  return event;
}

function piMcpFields(name: string): JsonObject {
  const parsed = piMcpTool(name);
  return { ...(parsed.server ? { server: parsed.server } : {}), ...(parsed.tool ? { tool: parsed.tool } : {}) };
}

function piMcpTool(name: string): { server?: string; tool?: string } {
  const rest = name.trim().startsWith('mcp__') ? name.trim().slice('mcp__'.length) : '';
  if (!rest) return {};
  const parts = rest.includes('__') ? rest.split('__') : rest.split('_');
  const server = safeLabel(parts.shift(), 128);
  const tool = safeLabel(parts.join(rest.includes('__') ? '__' : '_'), 128);
  return { ...(server ? { server } : {}), ...(tool ? { tool } : {}) };
}

function isMcpTool(name: string): boolean { return name.trim().startsWith('mcp__'); }

function commandFor(name: string, args: JsonObject): string {
  if (name.toLowerCase() === 'bash') return boundedString(args.command, 1 << 20) ?? '';
  return boundedString(args.command ?? args.cmd, 1 << 20) ?? '';
}

function fileOperation(name: string): string | undefined {
  if (name.toLowerCase() === 'read') return 'read';
  if (name.toLowerCase() === 'write') return 'create';
  if (name.toLowerCase() === 'edit') return 'modify';
  return undefined;
}

function textParts(value: unknown): string[] {
  if (typeof value === 'string') return value.trim() ? [value] : [];
  if (!Array.isArray(value)) {
    if (!isRecord(value)) return [];
    const text = firstText(value.text, value.content, value.output);
    return text.trim() ? [text] : [];
  }
  const result: string[] = [];
  for (const item of value) {
    const texts = textParts(item);
    result.push(...texts);
  }
  return result;
}

function extractText(value: unknown): string {
  if (value === undefined || value === null) return '';
  const texts = textParts(value);
  if (texts.length) return texts.join('\n');
  try { return JSON.stringify(value) ?? ''; } catch { return ''; }
}

function parseArgs(value: unknown): unknown {
  if (isRecord(value)) return value;
  if (typeof value === 'string' && value.trim()) {
    try { return JSON.parse(value) as unknown; } catch { return value; }
  }
  if (value === undefined || value === null) return {};
  return value;
}

function contentDigest(value: unknown, included = false, requested = false): JsonObject {
  const digest = digestUnknown(value);
  return { retention: included ? 'full' : 'metadata', included, ...(digest ?? {}), ...(requested && !included ? { truncated: true } : {}) };
}

function digestUnknown(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); } catch { return undefined; }
  if (typeof text !== 'string') return undefined;
  return { hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

async function processFiles(
  files: PiFile[], root: string, state: CursorState, options: PiHistoryOptions, result: PiHistoryResult,
  persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const previous = state.files[file.sourceSha256];
    try {
      const bytes = await readPiFile(file, root);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi sync exceeded the aggregate source-byte limit' });
        break;
      }
      const parsed = parsePiFile(bytes);
      result.malformed_lines += parsed.malformed;
      if (parsed.partialTail) result.partial_files++;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript has a malformed row; scanning stops at the safe prefix' });
      if (parsed.partialTail) result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript ends in an incomplete row; waiting for the next sweep' });
      if (previous && (parsed.completeLines < previous.last_line ||
          parsed.prefixHashes.get(previous.last_line) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript prefix changed; refusing automatic replay' });
        continue;
      }
      const { groups, state: mapperState } = mapPiEntries(file, parsed, previous?.last_line ?? 0,
        previous?.started ?? false, options.includeContent ?? false);
      const pendingEventCount = groups.reduce((count, group) => count + group.events.length, 0);
      if (pendingEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi sync exceeded the event processing limit' });
        break;
      }
      if (options.preview) {
        result.events_previewed += pendingEventCount;
        result.events ??= [];
        for (const group of groups) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let pendingGroups: MappedGroup[] = [];
      let pendingEvents: ThreadforgeEvent[] = [];
      const flush = async () => {
        if (pendingGroups.length === 0) return;
        if (pendingEvents.length) {
          await emitEvents(pendingEvents, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
          result.events_emitted += pendingEvents.length;
        }
        const last = pendingGroups[pendingGroups.length - 1]!;
        const nextCursor = makeCursor(last.line, last.prefix, last.started, file, parsed.partialTail || parsed.malformed > 0);
        const current = state.files[file.sourceSha256];
        const changed = !current || current.last_line !== nextCursor.last_line || current.started !== nextCursor.started ||
          current.prefix_sha256 !== nextCursor.prefix_sha256 || current.size_bytes !== nextCursor.size_bytes ||
          current.mtime_ms !== nextCursor.mtime_ms || current.incomplete !== nextCursor.incomplete;
        state.files[file.sourceSha256] = nextCursor;
        if (changed) {
          result.files_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
        pendingGroups = [];
        pendingEvents = [];
      };
      try {
        for (const group of groups) {
          if (pendingEvents.length > 0 && pendingEvents.length + group.events.length > eventBatchSize) await flush();
          pendingGroups.push(group);
          pendingEvents.push(...group.events);
          if (pendingEvents.length >= eventBatchSize) await flush();
        }
        await flush();
      } catch {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected Pi history events' });
        continue;
      }

      const finalPrefix = parsed.prefixHashes.get(parsed.completeLines);
      if (finalPrefix) {
        const current = state.files[file.sourceSha256];
        const nextCursor = makeCursor(parsed.completeLines, finalPrefix, mapperState.started, file,
          parsed.partialTail || parsed.malformed > 0);
        const changed = !current || current.last_line !== nextCursor.last_line || current.started !== nextCursor.started ||
          current.prefix_sha256 !== nextCursor.prefix_sha256 || current.size_bytes !== nextCursor.size_bytes ||
          current.mtime_ms !== nextCursor.mtime_ms || current.incomplete !== nextCursor.incomplete;
        state.files[file.sourceSha256] = nextCursor;
        if (changed) {
          result.files_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
      }
    } catch {
      result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(line: number, prefix: string, started: boolean, file: PiFile, incomplete: boolean): PiCursor {
  return {
    last_line: line, started, prefix_sha256: prefix, size_bytes: file.size, mtime_ms: file.mtimeMs, incomplete,
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
    if (isCode(error, 'EEXIST')) throw new Error('Pi history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Pi history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.pi-session-cursor.v1', version: cursorVersion, files: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes) throw new Error('Pi history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('Pi cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxCursorBytes) throw new Error('Pi cursor exceeds local size limit');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.files)) {
      throw new Error('Pi history cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, PiCursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.last_line) ||
          typeof value.started !== 'boolean' || typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
          typeof value.incomplete !== 'boolean') throw new Error('Pi history cursor contains an invalid checkpoint');
      files[key] = { last_line: value.last_line, started: value.started, prefix_sha256: value.prefix_sha256,
        size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, incomplete: value.incomplete };
    }
    return { ...empty, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Pi history cursor')) throw error;
    throw new Error('Pi history cursor is malformed or unreadable');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Pi cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Pi cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxCursorBytes) throw new Error('Pi history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Pi history cursor');
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}
function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function finiteNonNegative(value: unknown): number | undefined { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined; }
function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value: unknown, maximum: number): string | undefined { return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined; }
function safeOpaque(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
}
function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = typeof value === 'string' ? value.trim() : '';
  if (!text || text.length > maximum || !/^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text) || /^(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[_-]/i.test(text) ||
      /^[A-Za-z0-9_-]{40,}$/.test(text) || /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(text)) return undefined;
  return text;
}
function typeOf(value: JsonObject): string { return typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function firstRecord(...values: unknown[]): JsonObject {
  for (const value of values) if (isRecord(value)) return value;
  return {};
}
function firstText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'string' && value.length) return value;
    if (isRecord(value)) {
      for (const key of ['text', 'content', 'output']) if (typeof value[key] === 'string') return value[key] as string;
    }
  }
  return '';
}
function textValue(value: unknown): string { return typeof value === 'string' ? value : ''; }
function isErrorStatus(value: unknown): boolean { return typeof value === 'string' && ['error', 'failed', 'failure'].includes(value.trim().toLowerCase()); }
function timestampValue(value: unknown, fallback: string | number): string {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    const date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof value === 'string') {
    const number = Number(value);
    if (value.trim() !== '' && Number.isFinite(number) && number >= 0) return timestampValue(number, fallback);
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  return timestampValue(fallback, 0);
}
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isSafeRelative(value: string): boolean { return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
