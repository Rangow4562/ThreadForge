import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { resolveCursorGlobalDatabase, scanCursorComposers, type CursorComposerSnapshot } from './cursor-composer.ts';
import { areSameRealPathsWithoutLinks, isSameRealPathWithoutLinks, isWithinRealPaths } from './path-safety.ts';

const stateVersion = 1;
const stateFileName = 'cursor-transcripts.v1.json';
const maxStateBytes = 8 << 20;
const maxFiles = 5_000;
const maxFileBytes = 64 << 20;
const maxTotalBytes = 512 << 20;
const maxLineBytes = 8 << 20;
const maxRecordsPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxToolCorrelations = 10_000;
const batchSize = 100;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const maxDepth = 6;

type JsonObject = Record<string, unknown>;
type TranscriptCursor = { record_index: number; size_bytes: number; mtime_ms: number; prefix_sha256: string };
type ComposerCursor = { bubble_count: number; prefix_sha256: string; updated_at_ms: number; compaction_sha256?: string };
type CursorState = { schema_version: 'threadforge.cursor-transcript-cursor.v1'; version: number; files: Record<string, TranscriptCursor>; composers: Record<string, ComposerCursor> };
export type TranscriptFile = { key: string; path: string; size: number; mtimeMs: number };
type ToolCorrelation = { name: string; pathArgument: boolean; commandArgument: boolean };

export type CursorHistoryOptions = {
  projectsDir?: string;
  statePath?: string;
  endpoint?: string;
  maxFiles?: number;
  globalDbPath?: string;
  maxComposers?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type CursorHistoryResult = {
  schema_version: 'threadforge.cursor-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_records: number;
  partial_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
  global_db_present: boolean;
  composer_sessions: number;
  composer_sessions_changed: number;
  composer_missing_sessions: number;
  composers_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export type CursorHistoryStatus = {
  schema_version: 'threadforge.cursor-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
  global_db_present: boolean;
  composer_sessions: number;
  composer_checkpointed_sessions: number;
  composer_pending_sessions: number;
  composer_conflicts: number;
  composer_missing_sessions: number;
  composers_truncated: boolean;
};

export async function getCursorHistoryStatus(
  options: Pick<CursorHistoryOptions, 'projectsDir' | 'statePath' | 'maxFiles' | 'globalDbPath' | 'maxComposers'> = {},
): Promise<CursorHistoryStatus> {
  const maximum = boundedInteger(options.maxFiles ?? 1_000, 1, maxFiles, '--max-files');
  const projectsDir = await resolveProjectsDir(options.projectsDir);
  const listed = await listTranscriptFiles(projectsDir, maximum);
  const composerScan = await scanCursorComposers({ globalDbPath: options.globalDbPath, maxComposers: options.maxComposers });
  const state = await readState(resolveStatePath(options.statePath));
  const checkpointed = listed.files.filter((file) => {
    const cursor = state.files[sha256(file.key)];
    return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
  }).length;
  let composerCheckpointed = 0;
  let composerConflicts = 0;
  for (const composer of composerScan.composers) {
    const cursor = state.composers[composer.id_sha256];
    if (!cursor) continue;
    if (composer.prefix_sha256[cursor.bubble_count] !== cursor.prefix_sha256) composerConflicts++;
    else if (cursor.bubble_count === composer.header_count && cursor.compaction_sha256 === composer.compaction_sha256) composerCheckpointed++;
  }
  const composerMissing = missingComposerKeys(state, composerScan);
  return {
    schema_version: 'threadforge.cursor-history-status.v1', sessions: listed.files.length,
    checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    files_truncated: listed.truncated, global_db_present: composerScan.present,
    composer_sessions: composerScan.composers.length, composer_checkpointed_sessions: composerCheckpointed,
    composer_pending_sessions: composerScan.composers.length - composerCheckpointed + composerMissing.length,
    composer_conflicts: composerConflicts, composer_missing_sessions: composerMissing.length,
    composers_truncated: composerScan.truncated,
  };
}

export async function syncCursorHistory(options: CursorHistoryOptions = {}): Promise<CursorHistoryResult> {
  validateOptions(options);
  const projectsDir = await resolveProjectsDir(options.projectsDir);
  const statePath = resolveStatePath(options.statePath);
  if (await isWithinRealPaths(projectsDir, statePath) !== false) throw new Error('Cursor transcript cursor must be stored outside Cursor projects');
  const databasePath = resolveCursorGlobalDatabase(options.globalDbPath);
  if (await areSameRealPathsWithoutLinks(statePath, databasePath) !== false) {
    throw new Error('Cursor history cursor cannot overwrite the global Composer database');
  }
  const listed = await listTranscriptFiles(projectsDir, options.maxFiles ?? 1_000);
  const composerScan = await scanCursorComposers({ globalDbPath: options.globalDbPath, maxComposers: options.maxComposers });
  const result = emptyResult(options.includeContent ?? false);
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.files_truncated = listed.truncated;
  result.global_db_present = composerScan.present;
  result.composer_sessions = composerScan.composers.length;
  result.composers_truncated = composerScan.truncated;
  result.malformed_records += composerScan.malformed;
  result.errors.push(...composerScan.errors);
  if (options.preview) {
    const state = await readState(statePath);
    reportMissingComposers(state, composerScan, result);
    await processFiles(listed.files, projectsDir, state, options, result, false);
    await processComposers(composerScan.composers, state, options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      reportMissingComposers(state, composerScan, result);
      await processFiles(listed.files, projectsDir, state, options, result, true, statePath);
      await processComposers(composerScan.composers, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_records === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated && !result.composers_truncated;
  return result;
}

function emptyResult(includeContent: boolean): CursorHistoryResult {
  return {
    schema_version: 'threadforge.cursor-history-sync.v1', sessions: 0, sessions_changed: 0,
    global_db_present: false, composer_sessions: 0, composer_sessions_changed: 0, composer_missing_sessions: 0, composers_truncated: false,
    events_emitted: 0, events_previewed: 0, malformed_records: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: CursorHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxFiles ?? 1_000, 1, maxFiles, '--max-files');
  boundedInteger(options.maxComposers ?? 1_000, 1, 5_000, '--max-composers');
  if (options.projectsDir !== undefined && !path.isAbsolute(options.projectsDir)) throw new Error('--projects-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.globalDbPath !== undefined && !path.isAbsolute(options.globalDbPath)) throw new Error('--global-db-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveProjectsDir(value?: string): Promise<string> {
  const projectsDir = value ?? path.join(os.homedir(), '.cursor', 'projects');
  if (!path.isAbsolute(projectsDir)) throw new Error('--projects-dir must be an absolute path');
  const info = await lstat(projectsDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Cursor projects directory');
  });
  if (!info) return path.resolve(projectsDir);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Cursor projects path must be a real directory');
  const canonical = await realpath(projectsDir).catch(() => { throw new Error('Cursor projects directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, projectsDir)) throw new Error('Cursor projects directory must not traverse symbolic links');
  return canonical;
}

async function listTranscriptFiles(
  root: string, maximum: number,
): Promise<{ files: TranscriptFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Cursor projects path must be a real directory');
  const files: TranscriptFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  const projectEntries = await readdir(root, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Cursor projects'); });
  const pending: Array<{ path: string; depth: number }> = [];
  for (const project of projectEntries) {
    const projectPath = path.join(root, project.name);
    const projectInfo = await lstat(projectPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!projectInfo) continue;
    if (projectInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!projectInfo.isDirectory()) continue;
    const transcriptDir = path.join(projectPath, 'agent-transcripts');
    const transcriptInfo = await lstat(transcriptDir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!transcriptInfo) continue;
    if (transcriptInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (transcriptInfo.isDirectory()) pending.push({ path: transcriptDir, depth: 0 });
  }
  while (pending.length > 0) {
    const current = pending.pop()!;
    if (current.depth > maxDepth) throw new Error(`Cursor transcript directories exceed depth ${maxDepth}`);
    const entries = await readdir(current.path, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Cursor transcript files'); });
    for (const entry of entries) {
      const absolute = path.join(current.path, entry.name);
      const info = await lstat(absolute).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (info.isDirectory()) {
        pending.push({ path: absolute, depth: current.depth + 1 });
        continue;
      }
      if (!info.isFile() || entry.name.startsWith('.') || !isTranscriptFilename(entry.name)) continue;
      if (info.size > maxFileBytes) { oversizedSkipped++; continue; }
      const canonical = await realpath(absolute).catch(() => '');
      if (!canonical || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
      const relative = path.relative(root, canonical);
      if (!isSafeRelative(relative)) { symlinksSkipped++; continue; }
      files.push({ key: relative.split(path.sep).join('/'), path: canonical, size: info.size, mtimeMs: info.mtimeMs });
      if (files.length > maximum) { truncated = true; break; }
    }
    if (truncated) break;
  }
  files.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  if (truncated) files.length = maximum;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

function isTranscriptFilename(name: string): boolean {
  const lower = name.toLowerCase();
  return lower.endsWith('.json') || lower.endsWith('.jsonl') || path.extname(name) === '';
}

function isSafeRelative(value: string): boolean { return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function readTranscriptFile(file: TranscriptFile, root: string): Promise<Buffer> {
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes) throw new Error('Cursor transcript is not a bounded regular file');
  if (before.size !== file.size || before.mtimeMs !== file.mtimeMs) throw new Error('Cursor transcript changed after enumeration');
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error('Cursor transcript path escaped Cursor projects');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error('Cursor transcript changed before reading');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) throw new Error('Cursor transcript changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Cursor transcript')) throw error;
    throw new Error('Cursor transcript could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

type ParsedTranscript = {
  records: JsonObject[];
  partialTail: boolean;
  malformed: number;
  prefixHashes: Map<number, string>;
  completePrefixHash: string;
};

function parseTranscript(bytes: Buffer, sourceKey: string): ParsedTranscript {
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const records: JsonObject[] = [];
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let trimmed: string;
  try { trimmed = decoder.decode(bytes).trim(); }
  catch { return { records, partialTail: false, malformed: 1, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') }; }
  let partialTail = false;
  let malformed = 0;
  if (trimmed.length === 0) return { records, partialTail, malformed, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
  let whole: unknown;
  if (/\.jsonl$/i.test(sourceKey)) whole = undefined;
  else {
    try { whole = JSON.parse(trimmed) as unknown; }
    catch { whole = undefined; }
  }
  if (whole !== undefined) {
    const candidates = transcriptCandidates(whole);
    if (!candidates) return { records, partialTail, malformed: 1, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
    for (const candidate of candidates) {
      if (!isRecord(candidate)) { malformed++; break; }
      if (records.length >= maxRecordsPerFile) { malformed++; break; }
      const encoded = Buffer.from(JSON.stringify(candidate), 'utf8');
      if (encoded.byteLength > maxLineBytes) { malformed++; break; }
      records.push(candidate);
      prefixHasher.update(encoded);
      prefixHasher.update(Buffer.from('\n'));
      prefixHashes.set(records.length, prefixHasher.copy().digest('hex'));
    }
    return { records, partialTail, malformed, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
  }
  let start = 0;
  let stoppedOnMalformed = false;
  for (let offset = 0; offset < bytes.length; offset++) {
    if (bytes[offset] !== 0x0a) continue;
    const end = offset > start && bytes[offset - 1] === 0x0d ? offset - 1 : offset;
    const raw = bytes.subarray(start, end);
    start = offset + 1;
    if (raw.byteLength > maxLineBytes) { malformed++; stoppedOnMalformed = true; break; }
    if (raw.toString('utf8').trim() === '') continue;
    if (records.length >= maxRecordsPerFile) { malformed++; stoppedOnMalformed = true; break; }
    let decoded: unknown;
    try { decoded = JSON.parse(decoder.decode(raw)) as unknown; }
    catch { malformed++; stoppedOnMalformed = true; break; }
    const candidates = transcriptCandidates(decoded);
    if (!candidates || candidates.length !== 1 || !isRecord(candidates[0])) { malformed++; stoppedOnMalformed = true; break; }
    records.push(candidates[0]);
    prefixHasher.update(Buffer.from(`${JSON.stringify(candidates[0])}\n`));
    prefixHashes.set(records.length, prefixHasher.copy().digest('hex'));
  }
  partialTail = !stoppedOnMalformed && start < bytes.length;
  return { records, partialTail, malformed, prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
}

function transcriptCandidates(value: unknown): unknown[] | undefined {
  if (Array.isArray(value)) return value;
  if (!isRecord(value)) return undefined;
  for (const key of ['messages', 'events', 'entries']) if (Array.isArray(value[key])) return value[key] as unknown[];
  return [value];
}

export function mapTranscriptRecord(
  record: JsonObject, recordIndex: number, file: TranscriptFile, fileHash: string,
  correlations: Map<string, ToolCorrelation>, includeContent: boolean, shouldEmit: boolean,
): ThreadforgeEvent[] {
  const type = safeLabel(record.type, 96)?.toLowerCase() ?? '';
  const id = safeIdentifier(record.id, 512);
  const timestamp = timestampMs(record.timestamp ?? record.createdAt ?? record.time, file.mtimeMs);
  const model = safeLabel(record.modelId ?? record.model ?? record.newModel ?? nestedString(record, 'data', 'model') ??
    nestedString(record, 'message', 'model') ?? nestedString(record, 'message', 'modelId'), 128);
  const context = contextFrom(record, record.data, record.message);
  const events: ThreadforgeEvent[] = [];
  if (type === 'message') {
    const message = isRecord(record.message) ? record.message : {
      role: record.role,
      text: record.text,
      content: record.content,
      model,
    };
    mapMessage(message, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
    return events;
  }
  if (type === 'user_message') {
    const message = { role: 'user', text: record.text ?? record.message, content: record.content };
    mapMessage(message, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
    return events;
  }
  if (type === 'tool_call' || type === 'tool_use' || type === 'function_call') {
    const call = isRecord(record.toolCall) ? record.toolCall : isRecord(record.functionCall) ? record.functionCall :
      isRecord(record.data) ? record.data : record;
    const parsed = parseToolCall(call, id ?? `record-${recordIndex}`);
    if (parsed.id) rememberTool(correlations, parsed.id, parsed.name, parsed.args);
    if (shouldEmit && parsed.name) events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context,
      'tool.invoked', 'tool', 'Cursor tool invocation recovered from local history', `tool.call:${id ?? recordIndex}`,
      { toolName: parsed.name, toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.args) }));
    return events;
  }
  if (type === 'tool_result' || type === 'tool_output' || type === 'function_call_output') {
    const result = isRecord(record.toolResult) ? record.toolResult : isRecord(record.functionResult) ? record.functionResult :
      isRecord(record.data) ? record.data : record;
    const parsed = parseToolResult(result, id ?? `record-${recordIndex}`, correlations);
    if (shouldEmit && parsed.name) {
      const classified = classifyTool(parsed.name, parsed.args, parsed.failed);
      events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context,
        classified.action, classified.category, `Cursor ${classified.label} recovered from local history`, `tool.result:${id ?? recordIndex}`,
        { severity: parsed.failed ? 'medium' : 'info', toolName: parsed.name,
          toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.output) }));
    }
    return events;
  }
  if (type === 'error' && shouldEmit) {
    events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context,
      'tool.failed', 'tool', 'Cursor transcript records an agent error', `error:${id ?? recordIndex}`, { severity: 'medium' }));
    return events;
  }
  if (type === 'system_event' && shouldEmit) {
    events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context,
      'session.compacting', 'session', 'Cursor transcript records session compaction', `system:${id ?? recordIndex}`));
    return events;
  }
  const role = normalizeRole(firstString(record.role, nestedString(record, 'message', 'role')));
  if (role) {
    mapMessage(record, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
  }
  return events;
}

function mapMessage(
  message: JsonObject, recordId: string | undefined, timestamp: string, context: { used?: number; limit?: number },
  model: string | undefined, source: JsonObject, recordIndex: number, file: TranscriptFile, fileHash: string,
  correlations: Map<string, ToolCorrelation>, includeContent: boolean, shouldEmit: boolean, events: ThreadforgeEvent[],
): void {
  const role = normalizeRole(firstString(message.role));
  if (role === 'user') {
    const text = cleanUserText(messageText(message));
    if (shouldEmit) pushTextEvent(events, source, fileHash, recordIndex, recordId, timestamp, file, model, context,
      'prompt.submitted', 'prompt', text, includeContent, 'user');
    return;
  }
  if (role !== 'assistant') return;
  if (shouldEmit) {
    for (const [index, reasoning] of reasoningTexts(message).entries()) {
      if (!reasoning.trim()) continue;
      events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context,
      'agent.reasoning', 'agent', 'Cursor reasoning digest recovered from local history', `reasoning:${recordId ?? recordIndex}:${index}`,
        { content: contentDigest(reasoning, false) }));
    }
    const text = messageText(message);
    pushTextEvent(events, source, fileHash, recordIndex, recordId, timestamp, file, model, context,
      'agent.message', 'agent', text, includeContent, 'assistant');
  }
  for (const [callIndex, value] of toolCalls(message).entries()) {
    const parsed = parseToolCall(value, `${recordId ?? recordIndex}:tool:${callIndex}`);
    if (parsed.id) rememberTool(correlations, parsed.id, parsed.name, parsed.args);
    if (shouldEmit && parsed.name) events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context,
      'tool.invoked', 'tool', 'Cursor tool invocation recovered from local history', `tool.call:${recordId ?? recordIndex}:${callIndex}`,
      { toolName: parsed.name, toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.args) }));
  }
}

function parseToolCall(value: JsonObject, fallbackId: string): { id: string; name: string; args: unknown } {
  const id = safeIdentifier(value.id ?? value.toolCallId ?? value.callId, 512) ?? fallbackId;
  const name = safeLabel(value.name ?? value.toolName ?? value.tool, 128) ?? '';
  return { id, name, args: parseArguments(value.args ?? value.arguments ?? value.params ?? value.input) };
}

function parseToolResult(
  value: JsonObject, fallbackId: string, correlations: Map<string, ToolCorrelation>,
): { id: string; name: string; args: unknown; output: unknown; failed: boolean } {
  const id = safeIdentifier(value.toolCallId ?? value.callId ?? value.id, 512) ?? fallbackId;
  const call = correlations.get(id);
  const name = safeLabel(value.toolName ?? value.name ?? value.tool ?? call?.name, 128) ?? '';
  const status = boundedString(value.status, 48)?.toLowerCase();
  const failed = value.success === false || ['error', 'failed', 'failure'].includes(status ?? '');
  const output = value.output ?? value.result ?? value.content ?? value.text ?? value.data;
  if (call) correlations.delete(id);
  return { id, name, args: call ? { path: call.pathArgument, command: call.commandArgument } : {}, output, failed };
}

function rememberTool(correlations: Map<string, ToolCorrelation>, id: string, name: string, args: unknown): void {
  if (correlations.size >= maxToolCorrelations && !correlations.has(id)) correlations.delete(correlations.keys().next().value!);
  const value = isRecord(args) ? args : {};
  const pathArgument = ['path', 'file_path', 'filePath', 'targetFile', 'target_file', 'target_path', 'relativeWorkspacePath']
    .some((key) => typeof value[key] === 'string' && (value[key] as string).trim() !== '');
  const commandArgument = ['command', 'cmd', 'commandLine'].some((key) => typeof value[key] === 'string' && (value[key] as string).trim() !== '');
  correlations.set(id, { name, pathArgument, commandArgument });
}

function classifyTool(name: string, args: unknown, failed: boolean): { action: string; category: string; label: string } {
  const lower = name.toLowerCase();
  const shape = isRecord(args) ? args : {};
  const pathPresent = shape.path === true || ['path', 'file_path', 'filePath', 'targetFile', 'target_file', 'target_path', 'relativeWorkspacePath']
    .some((key) => typeof shape[key] === 'string' && (shape[key] as string).trim() !== '');
  const commandPresent = shape.command === true || ['command', 'cmd', 'commandLine']
    .some((key) => typeof shape[key] === 'string' && (shape[key] as string).trim() !== '');
  if (failed) return { action: 'tool.failed', category: 'tool', label: 'tool failure' };
  if (lower.startsWith('mcp_') || lower.includes('__')) return { action: 'mcp.tool_invoked', category: 'mcp', label: 'MCP tool result' };
  if (commandPresent || ['terminal', 'command', 'bash', 'shell'].some((part) => lower.includes(part))) return { action: 'command.executed', category: 'command', label: 'command result' };
  if (pathPresent) {
    if (['read', 'grep', 'search', 'list'].some((part) => lower.includes(part))) return { action: 'file.read', category: 'file', label: 'file activity' };
    if (['write', 'create'].some((part) => lower.includes(part))) return { action: 'file.created', category: 'file', label: 'file activity' };
    if (['edit', 'delete', 'apply'].some((part) => lower.includes(part))) return { action: 'file.modified', category: 'file', label: 'file activity' };
  }
  return { action: 'tool.completed', category: 'tool', label: 'tool result' };
}

function toolCalls(message: JsonObject): JsonObject[] {
  const out: JsonObject[] = [];
  for (const key of ['toolCalls', 'tool_calls']) {
    if (!Array.isArray(message[key])) continue;
    for (const item of (message[key] as unknown[]).slice(0, 500)) if (isRecord(item)) out.push(item);
  }
  return out;
}

function messageText(message: JsonObject): string {
  for (const value of [message.text, message.content, message.message]) if (typeof value === 'string' && value.trim()) return value;
  const parts: string[] = [];
  for (const block of normalizeBlocks(message.content)) {
    const type = (safeLabel(block.type, 64) ?? 'text').toLowerCase();
    if (type !== 'text') continue;
    const text = firstText(block.text, block.content);
    if (text.trim()) parts.push(text);
  }
  return parts.join('\n').trim();
}

function reasoningTexts(message: JsonObject): string[] {
  const out: string[] = [];
  for (const value of [message.thinking, message.reasoning]) if (typeof value === 'string' && value.trim()) out.push(value);
  for (const block of normalizeBlocks(message.content)) {
    const type = (safeLabel(block.type, 64) ?? 'text').toLowerCase();
    if (type === 'thinking' || type === 'reasoning') {
      const text = firstText(block.thinking, block.text, block.content);
      if (text.trim()) out.push(text);
    }
  }
  return out;
}

function normalizeBlocks(value: unknown): JsonObject[] {
  if (Array.isArray(value)) return value.filter(isRecord);
  if (isRecord(value)) return [value];
  return [];
}

function normalizeRole(value: string): 'user' | 'assistant' | '' {
  const role = value.toLowerCase();
  if (role === 'user' || role === 'human') return 'user';
  if (role === 'assistant' || role === 'agent' || role === 'model') return 'assistant';
  return '';
}

function cleanUserText(text: string): string {
  return text.replace(/<user_query>\s*([\s\S]*?)\s*<\/user_query>/g, '$1').trim();
}

function contextFrom(...values: unknown[]): { used?: number; limit?: number } {
  const usedKeys = ['context_tokens', 'contextTokens', 'contextTokenCount', 'context_usage_tokens', 'contextUsageTokens', 'context_used_tokens', 'contextUsedTokens', 'used_context_tokens', 'usedContextTokens'];
  const limitKeys = ['context_window_size', 'contextWindowSize', 'context_window_tokens', 'contextWindowTokens', 'context_token_budget', 'contextTokenBudget', 'max_context_tokens', 'maxContextTokens', 'context_limit', 'contextLimit', 'context_limit_tokens', 'contextLimitTokens'];
  let used: number | undefined;
  let limit: number | undefined;
  const visit = (value: unknown, depth: number) => {
    if (depth > 4 || value === null || typeof value !== 'object') return;
    if (Array.isArray(value)) { for (const child of value) visit(child, depth + 1); return; }
    const object = value as JsonObject;
    if (used === undefined) used = firstInteger(object, usedKeys);
    if (limit === undefined) limit = firstInteger(object, limitKeys);
    if (used !== undefined && limit !== undefined) return;
    for (const [key, child] of Object.entries(object)) {
      if (key.toLowerCase().includes('context') || key === 'data' || key === 'message') visit(child, depth + 1);
    }
  };
  for (const value of values) visit(value, 0);
  return { ...(used !== undefined ? { used } : {}), ...(limit !== undefined ? { limit } : {}) };
}

function firstInteger(object: JsonObject, keys: string[]): number | undefined {
  for (const key of keys) {
    const value = object[key];
    const number = typeof value === 'number' ? value : typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : NaN;
    if (Number.isSafeInteger(number) && number > 0) return number;
  }
  return undefined;
}

function pushTextEvent(
  events: ThreadforgeEvent[], source: JsonObject, fileHash: string, recordIndex: number, recordId: string | undefined,
  timestamp: string, file: TranscriptFile, model: string | undefined, context: { used?: number; limit?: number },
  action: string, category: string, text: string, includeContent: boolean, role: 'user' | 'assistant',
): void {
  const value = text.trim();
  if (!value) return;
  const included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
  events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context,
    action, category, role === 'user' ? 'Cursor prompt recovered from local history' : 'Cursor assistant message recovered from local history',
    `${role}:${recordId ?? recordIndex}`, { content: contentDigest(value, included, includeContent), ...(included ? { text: value } : {}) }));
}

function makeEvent(
  source: JsonObject, fileHash: string, recordIndex: number, recordId: string | undefined, timestamp: string,
  file: TranscriptFile, model: string | undefined, context: { used?: number; limit?: number },
  action: string, category: string, message: string, suffix: string,
  extra: { content?: JsonObject; text?: string; toolName?: string; toolCallHash?: string; severity?: ThreadforgeEvent['severity'] } = {},
): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${fileHash}:${recordIndex}:${recordId ? sha256(recordId) : ''}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'cursor', collection_method: 'poll' },
    session: { id: `cursor:${fileHash}` },
    raw: {
      source: file.key.startsWith('composer/') ? 'cursor_composer' : 'cursor_agent_transcript', source_sha256: fileHash, record_index: recordIndex,
      record_type: safeLabel(source.type, 96) ?? 'unknown', ...(recordId ? { record_id_sha256: sha256(recordId) } : {}), message,
    },
  };
  if (model) event.model = model;
  if (context.used !== undefined || context.limit !== undefined) {
    event.gen_ai = { context: { ...(context.used !== undefined ? { used_tokens: context.used } : {}), ...(context.limit !== undefined ? { limit_tokens: context.limit } : {}) } };
  }
  if (extra.toolName) event.tool = { name: extra.toolName };
  if (extra.toolCallHash) event.raw = { ...event.raw, tool_call_sha256: extra.toolCallHash };
  if (extra.content) event.content = extra.content;
  if (extra.text !== undefined) {
    if (action === 'prompt.submitted') {
      event.prompt = { text: extra.text };
      event.gen_ai = { ...(event.gen_ai as JsonObject | undefined), input: { messages: [{ role: 'user', parts: [{ type: 'text', content: extra.text }] }] } };
    } else {
      event.gen_ai = { ...(event.gen_ai as JsonObject | undefined), output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: extra.text }] }] } };
    }
  }
  return event;
}

function digestContent(value: unknown): JsonObject {
  if (value === null || value === undefined) return { retention: 'metadata', included: false };
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); } catch { return { retention: 'metadata', included: false }; }
  if (typeof text !== 'string') return { retention: 'metadata', included: false };
  return { retention: 'metadata', included: false, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

function contentDigest(text: string, included: boolean, requested = false): JsonObject {
  return {
    retention: included ? 'full' : 'metadata', included, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8'),
    ...(requested && !included ? { truncated: true } : {}),
  };
}

async function processFiles(
  files: TranscriptFile[], root: string, state: CursorState, options: CursorHistoryOptions,
  result: CursorHistoryResult, persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const key = sha256(file.key);
    const previous = state.files[key];
    try {
      const bytes = await readTranscriptFile(file, root);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.files_truncated = true;
        result.errors.push({ source_sha256: key, message: 'Cursor history sync exceeded the total source-byte limit' });
        break;
      }
      const parsed = parseTranscript(bytes, file.key);
      result.malformed_records += parsed.malformed;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: key, message: 'Cursor transcript contains a malformed record; scanning stops before it' });
      if (parsed.partialTail) result.partial_sessions++;
      const cursorIndex = previous?.record_index ?? 0;
      if (previous && (parsed.records.length < cursorIndex || parsed.prefixHashes.get(cursorIndex) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: key, message: 'Cursor transcript prefix changed; refusing automatic replay' });
        continue;
      }
      const correlations = new Map<string, ToolCorrelation>();
      const groups: Array<{ recordIndex: number; events: ThreadforgeEvent[] }> = [];
      let fileEventCount = 0;
      for (let index = 0; index < parsed.records.length; index++) {
        const recordIndex = index + 1;
        const shouldEmit = recordIndex > cursorIndex;
        const events = mapTranscriptRecord(parsed.records[index]!, recordIndex, file, key, correlations, options.includeContent ?? false, shouldEmit);
        if (!shouldEmit || events.length === 0) continue;
        if (events.length > batchSize) throw new Error(`Cursor transcript record ${recordIndex} maps to too many events`);
        groups.push({ recordIndex, events });
        fileEventCount += events.length;
        if (fileEventCount > maxEventsPerFile) throw new Error(`Cursor transcript exceeds ${maxEventsPerFile} events`);
      }
      if (fileEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: key, message: 'Cursor sync exceeded the event processing limit' });
        break;
      }
      if (options.preview) {
        result.events_previewed += fileEventCount;
        result.events ??= [];
        for (const group of groups) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        continue;
      }
      let batch: Array<{ recordIndex: number; events: ThreadforgeEvent[] }> = [];
      let batchEvents = 0;
      let failed = false;
      const flush = async () => {
        if (batch.length === 0) return;
        const send = batch.flatMap((group) => group.events);
        await emitEvents(send, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        const lastIndex = batch[batch.length - 1]!.recordIndex;
        const prefix = parsed.prefixHashes.get(lastIndex);
        if (!prefix) throw new Error('Cursor transcript checkpoint prefix is unavailable');
        state.files[key] = makeCursor(lastIndex, file, prefix);
        result.events_emitted += send.length;
        if (persist && statePath) await writeState(statePath, state);
        batch = [];
        batchEvents = 0;
      };
      try {
        for (const group of groups) {
          if (batchEvents > 0 && batchEvents + group.events.length > batchSize) await flush();
          batch.push(group);
          batchEvents += group.events.length;
        }
        await flush();
      } catch {
        failed = true;
        result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Cursor history events' });
      }
      if (failed) continue;
      if (parsed.records.length > cursorIndex) {
        state.files[key] = makeCursor(parsed.records.length, file, parsed.completePrefixHash);
        result.sessions_changed++;
        if (persist && statePath) await writeState(statePath, state);
      }
    } catch {
      result.errors.push({ source_sha256: key, message: 'Cursor transcript could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(recordIndex: number, file: TranscriptFile, prefix: string): TranscriptCursor {
  return { record_index: recordIndex, size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix };
}

function missingComposerKeys(state: CursorState, scan: Awaited<ReturnType<typeof scanCursorComposers>>): string[] {
  if (scan.truncated) return [];
  const current = new Set(scan.composers.map((composer) => composer.id_sha256));
  return Object.keys(state.composers).filter((key) => !current.has(key));
}

function reportMissingComposers(state: CursorState, scan: Awaited<ReturnType<typeof scanCursorComposers>>, result: CursorHistoryResult): void {
  const missing = missingComposerKeys(state, scan);
  result.composer_missing_sessions = missing.length;
  for (const sourceHash of missing) {
    result.errors.push({ source_sha256: sourceHash, message: 'Previously observed Cursor Composer is no longer present in the local store' });
  }
}

async function processComposers(
  composers: CursorComposerSnapshot[], state: CursorState, options: CursorHistoryOptions,
  result: CursorHistoryResult, persist: boolean, statePath?: string,
): Promise<void> {
  for (const composer of composers) {
    const key = composer.id_sha256;
    const previous = state.composers[key];
    const start = previous?.bubble_count ?? 0;
    if (previous && composer.prefix_sha256[start] !== previous.prefix_sha256) {
      result.errors.push({ source_sha256: key, message: 'Cursor Composer history prefix changed; refusing automatic replay' });
      continue;
    }
    const file: TranscriptFile = { key: `composer/${key}`, path: '', size: 0, mtimeMs: composer.updated_at_ms };
    const fileHash = sha256(file.key);
    const correlations = new Map<string, ToolCorrelation>();
    const groups: Array<{ index: number; events: ThreadforgeEvent[]; compaction?: string }> = [];
    let composerEventCount = 0;
    for (const bubble of composer.bubbles) {
      if (bubble.index <= start) continue;
      const events: ThreadforgeEvent[] = [];
      for (const record of bubble.records) {
        events.push(...mapTranscriptRecord(record, bubble.index, file, fileHash, correlations, options.includeContent ?? false, true));
      }
      if (events.length > batchSize) {
        result.errors.push({ source_sha256: key, message: 'Cursor Composer bubble maps to too many events; later records were withheld' });
        break;
      }
      composerEventCount += events.length;
      if (composerEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: key, message: 'Cursor history sync exceeded the event processing limit' });
        break;
      }
      groups.push({ index: bubble.index, events });
    }
    const compactionChanged = composer.compaction !== undefined && composer.compaction_sha256 !== previous?.compaction_sha256;
    if (compactionChanged && composer.compaction) {
      const record = { ...composer.compaction, id: `compaction-${composer.compaction_sha256?.slice(0, 16) ?? 'current'}` };
      const events = mapTranscriptRecord(record, Math.max(1, composer.header_count), file, fileHash, correlations, false, true);
      groups.push({ index: composer.header_count, events, compaction: composer.compaction_sha256 });
      composerEventCount += events.length;
    }
    if (options.preview) {
      result.events_previewed += composerEventCount;
      result.events ??= [];
      for (const group of groups) for (const event of group.events) {
        if (result.events.length < maxPreviewEvents) result.events.push(event);
        else result.preview_truncated = true;
      }
      continue;
    }

    let completedCount = start;
    let completedCompaction = previous?.compaction_sha256;
    let changed = false;
    for (const group of groups) {
      try {
        if (group.events.length > 0) await emitEvents(group.events, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
      } catch {
        result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Cursor Composer events' });
        break;
      }
      if (group.index <= composer.header_count) completedCount = Math.max(completedCount, group.index);
      if (group.compaction !== undefined) completedCompaction = group.compaction;
      const prefix = composer.prefix_sha256[completedCount];
      if (!prefix) {
        result.errors.push({ source_sha256: key, message: 'Cursor Composer checkpoint prefix is unavailable' });
        break;
      }
      state.composers[key] = {
        bubble_count: completedCount, prefix_sha256: prefix, updated_at_ms: composer.updated_at_ms,
        ...(completedCompaction ? { compaction_sha256: completedCompaction } : {}),
      };
      result.events_emitted += group.events.length;
      changed ||= group.events.length > 0;
      if (persist && statePath) await writeState(statePath, state);
    }
    const scanHasCompletePrefix = composer.bubbles.length === composer.header_count;
    if (scanHasCompletePrefix && result.errors.every((entry) => entry.source_sha256 !== key)) {
      const prefix = composer.prefix_sha256[composer.header_count];
      if (prefix) {
        state.composers[key] = {
          bubble_count: composer.header_count, prefix_sha256: prefix, updated_at_ms: composer.updated_at_ms,
          ...(completedCompaction ? { compaction_sha256: completedCompaction } : {}),
        };
        if (persist && statePath) await writeState(statePath, state);
      }
    } else if (composer.bubbles.length < composer.header_count) {
      completedCount = composer.bubbles.length;
      const prefix = composer.prefix_sha256[completedCount];
      if (prefix && completedCount >= start && result.errors.every((entry) => entry.source_sha256 !== key)) {
        state.composers[key] = { bubble_count: completedCount, prefix_sha256: prefix, updated_at_ms: composer.updated_at_ms,
          ...(completedCompaction ? { compaction_sha256: completedCompaction } : {}) };
        if (persist && statePath) await writeState(statePath, state);
      }
    }
    if (changed) result.composer_sessions_changed++;
  }
}

function timestampMs(value: unknown, fallbackMs: number): string {
  let date: Date | undefined;
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
  } else if (typeof value === 'string') {
    const numeric = Number(value);
    date = value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0
      ? new Date(numeric < 1_000_000_000_000 ? numeric * 1_000 : numeric)
      : new Date(value);
  }
  return date && !Number.isNaN(date.getTime()) ? date.toISOString() : new Date(fallbackMs).toISOString();
}

function firstText(...values: unknown[]): string {
  for (const value of values) if (typeof value === 'string' && value.length > 0) return value;
  return '';
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    const text = boundedString(value, 2_048);
    if (text) return text;
  }
  return '';
}

function nestedString(value: unknown, outer: string, inner: string): string | undefined {
  return isRecord(value) && isRecord(value[outer]) ? boundedString((value[outer] as JsonObject)[inner], 128) : undefined;
}

function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function safeIdentifier(value: unknown, maximum: number): string | undefined { return boundedString(value, maximum); }

function parseArguments(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}

function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
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
    if (isCode(error, 'EEXIST')) throw new Error('Cursor history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Cursor transcript cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.cursor-transcript-cursor.v1', version: stateVersion, files: {}, composers: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Cursor transcript cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (opened.dev !== info.dev || opened.ino !== info.ino) throw new Error('Cursor transcript cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxStateBytes) throw new Error('Cursor transcript cursor exceeds the local size limit');
    const decoded = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(decoded) || decoded.schema_version !== empty.schema_version || decoded.version !== stateVersion || !isRecord(decoded.files) ||
        (decoded.composers !== undefined && !isRecord(decoded.composers))) {
      throw new Error('Cursor transcript cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, TranscriptCursor> = {};
    for (const [key, value] of Object.entries(decoded.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.record_index) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256)) {
        throw new Error('Cursor transcript cursor contains an invalid checkpoint');
      }
      files[key] = { record_index: value.record_index, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, prefix_sha256: value.prefix_sha256 };
    }
    const composers: Record<string, ComposerCursor> = {};
    for (const [key, value] of Object.entries(decoded.composers ?? {})) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.bubble_count) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
          typeof value.updated_at_ms !== 'number' || !Number.isFinite(value.updated_at_ms) ||
          (value.compaction_sha256 !== undefined && (typeof value.compaction_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.compaction_sha256)))) {
        throw new Error('Cursor transcript cursor contains an invalid Composer checkpoint');
      }
      composers[key] = {
        bubble_count: value.bubble_count, prefix_sha256: value.prefix_sha256, updated_at_ms: value.updated_at_ms,
        ...(typeof value.compaction_sha256 === 'string' ? { compaction_sha256: value.compaction_sha256 } : {}),
      };
    }
    return { ...empty, files, composers };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Cursor transcript cursor')) throw error;
    throw new Error('Cursor transcript cursor is malformed or unreadable');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Cursor transcript cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Cursor transcript cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const payload = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('Cursor transcript cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Cursor transcript cursor');
  }
}
