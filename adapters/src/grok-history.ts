import { createHash, randomUUID } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 2;
const stateFileName = 'grok-sessions.v2.json';
const maxStateBytes = 8 << 20;
const maxSessions = 5_000;
const defaultSessions = 1_000;
const maxChatBytes = 64 << 20;
const maxSummaryBytes = 1 << 20;
const maxTotalBytes = 512 << 20;
const maxTerminalFileBytes = 8 << 20;
const maxTerminalTotalBytes = 64 << 20;
const maxTerminalFiles = 5_000;
const maxLineBytes = 4 << 20;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxDirectoryEntries = 100_000;
const maxToolCalls = 10_000;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const batchSize = 100;

type JsonObject = Record<string, unknown>;
type SourceName = 'chat' | 'events';
type SourceCursor = { line: number; prefix_sha256: string };
type SessionCursor = {
  source_fingerprint: string;
  terminal_fingerprint: string;
  started: boolean;
  incomplete: boolean;
  chat: SourceCursor;
  events: SourceCursor;
};
type CursorState = {
  schema_version: 'threadforge.grok-history-cursor.v2';
  version: number;
  sessions: Record<string, SessionCursor>;
};
type FileMeta = { path: string; size: number; mtimeMs: number };
type GrokSession = {
  key: string;
  path: string;
  projectKey: string;
  sessionName: string;
  chat: FileMeta;
  events?: FileMeta;
  terminalFiles: FileMeta[];
  summary: FileMeta;
  fingerprint: string;
};
type ParsedRecord = { line: number; value: JsonObject };
type ParsedLog = {
  records: ParsedRecord[];
  completeLines: number;
  prefixHashes: Map<number, string>;
  partialTail: boolean;
  malformed: number;
  stoppedAtMalformed: boolean;
};
type EventGroup = { source: SourceName | 'session'; line: number; events: ThreadforgeEvent[] };

export type GrokHistoryOptions = {
  sessionsDir?: string;
  statePath?: string;
  endpoint?: string;
  fetchImpl?: typeof fetch;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
};
export type GrokHistoryResult = {
  schema_version: 'threadforge.grok-history-sync.v1';
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
export type GrokHistoryStatus = {
  schema_version: 'threadforge.grok-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  incomplete_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
};

/** Status reports counts only; it never returns Grok IDs, workspace paths, or session content. */
export async function getGrokHistoryStatus(
  options: Pick<GrokHistoryOptions, 'sessionsDir' | 'statePath' | 'maxSessions'> = {},
): Promise<GrokHistoryStatus> {
  const maximum = boundedInteger(options.maxSessions ?? defaultSessions, 1, maxSessions, '--max-sessions');
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('Grok history cursor must be stored outside Grok sessions');
  const listed = await listSessions(sessionsDir, maximum);
  const state = await readState(statePath);
  const checkpointed = listed.sessions.filter((session) => {
    const cursor = state.sessions[sha256(session.key)];
    return cursor !== undefined && cursor.source_fingerprint === session.fingerprint && cursor.started &&
      cursor.chat.line > 0 && !cursor.incomplete;
  }).length;
  const incomplete = listed.sessions.filter((session) => state.sessions[sha256(session.key)]?.incomplete).length;
  return {
    schema_version: 'threadforge.grok-history-status.v1', sessions: listed.sessions.length,
    checkpointed_sessions: checkpointed, pending_sessions: listed.sessions.length - checkpointed,
    incomplete_sessions: incomplete,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    sessions_truncated: listed.truncated,
  };
}

export async function syncGrokHistory(options: GrokHistoryOptions = {}): Promise<GrokHistoryResult> {
  validateOptions(options);
  const sessionsDir = await resolveSessionsDir(options.sessionsDir);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(sessionsDir, statePath)) throw new Error('Grok history cursor must be stored outside Grok sessions');
  const listed = await listSessions(sessionsDir, options.maxSessions ?? defaultSessions);
  const result = emptyResult(options.includeContent ?? false);
  if (options.preview) result.events = [];
  result.sessions = listed.sessions.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.sessions_truncated = listed.truncated;

  if (options.preview) {
    const state = await readState(statePath);
    await processSessions(listed.sessions, sessionsDir, state, options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processSessions(listed.sessions, sessionsDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated && !result.preview_truncated;
  return result;
}

function emptyResult(includeContent: boolean): GrokHistoryResult {
  return {
    schema_version: 'threadforge.grok-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: GrokHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? defaultSessions, 1, maxSessions, '--max-sessions');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveSessionsDir(value?: string): Promise<string> {
  const sessionsDir = path.resolve(value ?? path.join(os.homedir(), '.grok', 'sessions'));
  const info = await lstat(sessionsDir).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return sessionsDir;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Grok sessions path must be a real directory');
  const canonical = await realpath(sessionsDir).catch(() => { throw new Error('Grok sessions directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, sessionsDir)) throw new Error('Grok sessions directory must not traverse symbolic links');
  return canonical;
}

async function listSessions(
  root: string, maximum: number,
): Promise<{ sessions: GrokSession[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!rootInfo) return { sessions: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Grok sessions path must be a real directory');
  const sessions: GrokSession[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let totalBytes = 0;
  let truncated = false;
  const projects = await readdir(root, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Grok project session folders'); });
  if (projects.length > maxDirectoryEntries) truncated = true;
  for (const project of projects.slice(0, maxDirectoryEntries)) {
    const projectPath = path.join(root, project.name);
    const projectInfo = await lstat(projectPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!projectInfo) continue;
    if (projectInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!projectInfo.isDirectory()) continue;
    const children = await readdir(projectPath, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Grok session folders'); });
    if (children.length > maxDirectoryEntries) truncated = true;
    for (const child of children.slice(0, maxDirectoryEntries)) {
      const sessionPath = path.join(projectPath, child.name);
      const sessionInfo = await lstat(sessionPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
      if (!sessionInfo) continue;
      if (sessionInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (!sessionInfo.isDirectory()) continue;
      const chat = await fileMeta(path.join(sessionPath, 'chat_history.jsonl'), maxChatBytes, false);
      const summary = await fileMeta(path.join(sessionPath, 'summary.json'), maxSummaryBytes, false);
      const events = await fileMeta(path.join(sessionPath, 'events.jsonl'), maxChatBytes, true);
      const terminal = await listTerminalFiles(sessionPath);
      symlinksSkipped += terminal.symlinksSkipped;
      oversizedSkipped += terminal.oversizedSkipped;
      if (terminal.truncated) truncated = true;
      for (const found of [chat, summary, events]) {
        if (found === 'oversized') oversizedSkipped++;
        else if (found === 'symlink') symlinksSkipped++;
      }
      if (!chat || chat === 'oversized' || chat === 'symlink' || !summary || summary === 'oversized' || summary === 'symlink' ||
          events === 'oversized' || events === 'symlink') continue;
      const canonical = await realpath(sessionPath).catch(() => '');
      if (!canonical || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
      const terminalBytes = terminal.files.reduce((sum, item) => sum + item.size, 0);
      if (terminalBytes > maxTerminalTotalBytes) { oversizedSkipped++; continue; }
      const sourceTotal = chat.size + summary.size + (events ? events.size : 0) + terminalBytes;
      totalBytes += sourceTotal;
      if (totalBytes > maxTotalBytes) { truncated = true; break; }
      const key = path.relative(root, canonical).split(path.sep).join('/');
      const fingerprint = sha256(JSON.stringify([
        chat.size, chat.mtimeMs, summary.size, summary.mtimeMs,
        events ? [events.size, events.mtimeMs] : null,
        terminal.files.map((item) => [sha256(path.basename(item.path)), item.size, item.mtimeMs]),
      ]));
      sessions.push({ key, path: canonical, projectKey: project.name, sessionName: child.name, chat, summary,
        ...(events ? { events } : {}), terminalFiles: terminal.files, fingerprint });
      if (sessions.length > maximum) { truncated = true; break; }
    }
    if (truncated) break;
  }
  sessions.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  if (sessions.length > maximum) sessions.length = maximum;
  return { sessions, symlinksSkipped, oversizedSkipped, truncated };
}

async function listTerminalFiles(sessionPath: string): Promise<{ files: FileMeta[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const directory = path.join(sessionPath, 'terminal');
  const info = await lstat(directory).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (info.isSymbolicLink() || !info.isDirectory()) return { files: [], symlinksSkipped: 1, oversizedSkipped: 0, truncated: false };
  const entries = await readdir(directory, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Grok terminal logs'); });
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  const truncated = entries.length > maxTerminalFiles;
  const files: FileMeta[] = [];
  for (const entry of entries.slice(0, maxTerminalFiles)) {
    if (!entry.name.toLowerCase().endsWith('.log')) continue;
    const file = path.join(directory, entry.name);
    const fileInfo = await lstat(file).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!fileInfo) continue;
    if (fileInfo.isSymbolicLink() || !fileInfo.isFile()) { symlinksSkipped++; continue; }
    if (fileInfo.size > maxTerminalFileBytes) { oversizedSkipped++; continue; }
    files.push({ path: file, size: fileInfo.size, mtimeMs: fileInfo.mtimeMs });
  }
  files.sort((left, right) => left.path.localeCompare(right.path));
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

async function fileMeta(file: string, limit: number, optional: boolean): Promise<FileMeta | 'oversized' | 'symlink' | undefined> {
  const info = await lstat(file).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (!info) {
    if (optional) return undefined;
    return undefined;
  }
  if (info.isSymbolicLink()) return 'symlink';
  if (!info.isFile()) return 'symlink';
  if (info.size > limit) return 'oversized';
  return { path: file, size: info.size, mtimeMs: info.mtimeMs };
}

async function readStableFile(file: FileMeta | undefined, root: string, label: string, limit: number): Promise<Buffer> {
  if (!file) return Buffer.alloc(0);
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > limit || before.size !== file.size || before.mtimeMs !== file.mtimeMs) {
    throw new Error(`${label} changed after enumeration`);
  }
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error(`${label} escaped the Grok sessions directory`);
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened)) throw new Error(`${label} changed before read`);
    const bytes = await handle.readFile();
    const after = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > limit || !sameFile(opened, after) || !sameFile(opened, afterPath)) throw new Error(`${label} changed during read`);
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith(label)) throw error;
    throw new Error(`${label} could not be opened safely`);
  } finally { await handle?.close().catch(() => {}); }
}

async function readLog(file: FileMeta | undefined, root: string, label: string): Promise<ParsedLog> {
  const bytes = await readStableFile(file, root, label, maxChatBytes);
  let text: string;
  try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch { return { records: [], completeLines: 0, prefixHashes: new Map(), partialTail: false, malformed: 1, stoppedAtMalformed: true }; }
  const records: ParsedRecord[] = [];
  const prefixHashes = new Map<number, string>();
  const hasher = createHash('sha256');
  let start = 0;
  let line = 0;
  let malformed = 0;
  let stoppedAtMalformed = false;
  for (let offset = 0; offset < text.length; offset++) {
    if (text.charCodeAt(offset) !== 10) continue;
    line++;
    const lineBytes = Buffer.byteLength(text.slice(start, offset), 'utf8');
    if (lineBytes > maxLineBytes || line > maxLinesPerFile) { malformed++; stoppedAtMalformed = true; break; }
    const raw = text.slice(start, offset).trim();
    hasher.update(Buffer.from(text.slice(start, offset + 1), 'utf8'));
    prefixHashes.set(line, hasher.copy().digest('hex'));
    if (raw) {
      try {
        const value: unknown = JSON.parse(raw);
        if (!isRecord(value)) throw new Error('not an object');
        records.push({ line, value });
      } catch { malformed++; stoppedAtMalformed = true; break; }
    }
    start = offset + 1;
  }
  const completeLines = stoppedAtMalformed ? Math.max(0, line - 1) : line;
  if (stoppedAtMalformed) prefixHashes.delete(line);
  return {
    records: records.filter((record) => record.line <= completeLines), completeLines, prefixHashes,
    partialTail: !stoppedAtMalformed && start < text.length, malformed, stoppedAtMalformed,
  };
}

async function readSummary(file: FileMeta, root: string): Promise<JsonObject> {
  const bytes = await readStableFile(file, root, 'Grok summary', maxSummaryBytes);
  try {
    const value: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    if (!isRecord(value)) throw new Error('invalid summary');
    return value;
  } catch { throw new Error('Grok summary is malformed or unreadable'); }
}

async function readTerminalLogs(files: FileMeta[], root: string): Promise<Map<string, string>> {
  const logs = new Map<string, string>();
  let total = 0;
  for (const file of files) {
    total += file.size;
    if (total > maxTerminalTotalBytes) throw new Error('Grok terminal logs exceed the session limit');
    const bytes = await readStableFile(file, root, 'Grok terminal log', maxTerminalFileBytes);
    let text: string;
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
    catch { throw new Error('Grok terminal log is malformed or unreadable'); }
    logs.set(terminalCallId(file), text);
  }
  return logs;
}

function terminalCallId(file: FileMeta): string {
  const base = path.basename(file.path).slice(0, -'.log'.length);
  const separator = base.lastIndexOf('-');
  return separator > 'call-'.length ? base.slice(0, separator) : base;
}

function fingerprintTerminalLogs(files: FileMeta[], logs: Map<string, string>): string {
  return sha256(JSON.stringify(files.map((file) => [
    sha256(path.basename(file.path)), file.size, file.mtimeMs, sha256(logs.get(terminalCallId(file)) ?? ''),
  ])));
}

async function processSessions(
  files: GrokSession[], root: string, state: CursorState, options: GrokHistoryOptions,
  result: GrokHistoryResult, write: boolean, statePath?: string,
): Promise<void> {
  let totalEvents = 0;
  for (const file of files) {
    const sourceHash = sha256(file.key);
    const previous = state.sessions[sourceHash] ?? emptySessionCursor(file.fingerprint);
    try {
      const [chat, events, summary, terminalLogs] = await Promise.all([
        readLog(file.chat, root, 'Grok chat history'),
        readLog(file.events, root, 'Grok event history'),
        readSummary(file.summary, root),
        readTerminalLogs(file.terminalFiles, root),
      ]);
      result.malformed_lines += chat.malformed + events.malformed;
      if (chat.partialTail || events.partialTail || chat.stoppedAtMalformed || events.stoppedAtMalformed) result.partial_sessions++;
      const chatConflict = cursorConflict(previous.chat, chat);
      const eventsConflict = cursorConflict(previous.events, events);
      const terminalFingerprint = fingerprintTerminalLogs(file.terminalFiles, terminalLogs);
      const terminalConflict = previous.chat.line > 0 && previous.terminal_fingerprint !== terminalFingerprint;
      if (chatConflict || eventsConflict || terminalConflict) {
        result.errors.push({ source_sha256: sourceHash, message: 'session log prefix changed; cursor was not advanced' });
        continue;
      }
      const model = safeLabel(summary.current_model_id, 128);
      const groups = mapSession(file, summary, chat, events, terminalLogs, previous, options.includeContent ?? false, model);
      const sessionStart = sessionStartEvent(file, summary, events, sourceHash, model, previous.started);
      if (sessionStart) groups.unshift(sessionStart);
      const eventCount = groups.reduce((sum, group) => sum + group.events.length, 0);
      if (eventCount > maxEventsPerFile || totalEvents + eventCount > maxEventsPerSync) {
        result.errors.push({ source_sha256: sourceHash, message: 'session event limit exceeded; no cursor advancement' });
        continue;
      }
      totalEvents += eventCount;
      const changed = eventCount > 0 || previous.source_fingerprint !== file.fingerprint;
      if (changed) result.sessions_changed++;
      if (options.preview) {
        for (const group of groups) {
          for (const event of group.events) {
            if (result.events!.length >= maxPreviewEvents) { result.preview_truncated = true; break; }
            result.events!.push(event);
            result.events_previewed++;
          }
          if (result.preview_truncated) break;
        }
        continue;
      }

      const next = { ...previous, source_fingerprint: file.fingerprint, terminal_fingerprint: terminalFingerprint };
      next.incomplete = chat.partialTail || events.partialTail || chat.stoppedAtMalformed || events.stoppedAtMalformed;
      await deliverGroups(groups, next, chat, events, options, result, async () => {
        state.sessions[sourceHash] = next;
        if (statePath) await writeState(statePath, state);
      });
      // A complete no-event scan still advances physical-line checkpoints (e.g. ignored record kinds).
      if (!result.errors.some((item) => item.source_sha256 === sourceHash)) {
        advanceEmptyLines(next, chat, events);
        state.sessions[sourceHash] = next;
        if (statePath) await writeState(statePath, state);
      }
    } catch (error) {
      result.errors.push({ source_sha256: sourceHash, message: safeError(error) });
    }
  }
}

function mapSession(
  file: GrokSession, summary: JsonObject, chat: ParsedLog, lifecycle: ParsedLog, terminalLogs: Map<string, string>, cursor: SessionCursor,
  includeContent: boolean, model?: string,
): EventGroup[] {
  const groups: EventGroup[] = [];
  const sourceHash = sha256(file.key);
  const toolCalls = new Map<string, { name: string; args: unknown }>();
  for (const record of chat.records) {
    if (safeLabel(record.value.type, 64)?.toLowerCase() !== 'assistant' || !Array.isArray(record.value.tool_calls)) continue;
    for (const candidate of record.value.tool_calls) {
      if (!isRecord(candidate)) continue;
      const id = boundedString(candidate.id, 512);
      const name = safeLabel(candidate.name, 128);
      if (id && name) {
        if (toolCalls.size >= maxToolCalls && !toolCalls.has(id)) toolCalls.delete(toolCalls.keys().next().value!);
        toolCalls.set(id, { name, args: parseJsonValue(candidate.arguments) });
      }
    }
  }
  for (const record of chat.records) {
    if (record.line <= cursor.chat.line) continue;
    const type = safeLabel(record.value.type, 64)?.toLowerCase();
    const output: ThreadforgeEvent[] = [];
    if (type === 'user' && !boundedString(record.value.synthetic_reason, 256)) {
      const prompt = textValue(record.value.content);
      if (prompt) {
        const content = digestTextContent(prompt, includeContent);
        output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', 'prompt.submitted', 'prompt', record.value, model,
          { content, ...(content.included === true ? { text: prompt } : {}) }));
      }
    } else if (type === 'assistant') {
      const reasoning = isRecord(record.value.reasoning) ? textValue(record.value.reasoning.text) : '';
      if (reasoning) output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', 'agent.reasoning', 'agent', record.value, model,
        { content: digestContent(reasoning) }));
      const assistant = textValue(record.value.content);
      if (assistant) {
        const content = digestTextContent(assistant, includeContent);
        output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', 'agent.message', 'agent', record.value, safeLabel(record.value.model_id, 128) ?? model,
          { content, ...(content.included === true ? { text: assistant } : {}) }));
      }
      if (Array.isArray(record.value.tool_calls)) {
        for (let index = 0; index < record.value.tool_calls.length; index++) {
          const call = record.value.tool_calls[index];
          if (!isRecord(call)) continue;
          const id = boundedString(call.id, 512);
          const name = safeLabel(call.name, 128);
          if (!name) continue;
          const args = parseJsonValue(call.arguments);
          output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', toolAction(name, false), 'tool', record.value, model,
            { toolName: name, toolCallHash: id ? sha256(id) : undefined, content: digestContent(args),
              toolInput: args, eventSuffix: `call:${index}` }));
        }
      }
    } else if (type === 'tool_result') {
      const callId = boundedString(record.value.tool_call_id, 512);
      const call = callId ? toolCalls.get(callId) : undefined;
      const content = callId && terminalLogs.has(callId) ? terminalLogs.get(callId) : record.value.content;
      if (call) output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', toolAction(call.name, true), 'tool', record.value, model,
        { toolName: call.name, toolCallHash: callId ? sha256(callId) : undefined, content: digestContent(content),
          toolInput: call.args, eventSuffix: 'result' }));
      else if (content !== undefined) output.push(makeHistoryEvent(file, sourceHash, record.line, 'chat', 'tool.completed', 'tool', record.value, model,
        { content: digestContent(content), eventSuffix: 'unmatched-result' }));
    }
    if (output.length > 0) groups.push({ source: 'chat', line: record.line, events: output.map((event, index) => addDeterministicId(event, sourceHash, record.line, index)) });
  }
  for (const record of lifecycle.records) {
    if (record.line <= cursor.events.line) continue;
    const type = safeLabel(record.value.type, 64)?.toLowerCase();
    let action: string | undefined;
    let severity: ThreadforgeEvent['severity'] = 'info';
    let raw: JsonObject | undefined;
    let toolName = safeLabel(record.value.tool_name, 128);
    if (type === 'permission_requested') action = 'approval.requested';
    else if (type === 'permission_resolved') {
      const decision = normalizeDecision(record.value.decision);
      action = `approval.${decision}`;
      raw = { decision };
    } else if (type === 'tool_completed' && !isSuccess(record.value.outcome)) {
      action = 'tool.failed';
      severity = 'high';
      raw = { outcome: safeLabel(record.value.outcome, 48) ?? 'unknown' };
    } else if (type === 'turn_ended') {
      action = 'session.context';
      raw = {
        lifecycle: 'turn_ended',
        ...(typeof record.value.turn_number === 'number' && Number.isInteger(record.value.turn_number) && record.value.turn_number >= 0 ? { turn_number: record.value.turn_number } : {}),
        ...(safeLabel(record.value.outcome, 48) ? { outcome: safeLabel(record.value.outcome, 48) } : {}),
        ...(safeLabel(record.value.cancellation_category, 64) ? { cancellation_category: safeLabel(record.value.cancellation_category, 64) } : {}),
      };
    } else if (type === 'yolo_toggled') {
      action = 'session.context';
      raw = { lifecycle: 'yolo_toggled', ...(typeof record.value.enabled === 'boolean' ? { enabled: record.value.enabled } : {}) };
    }
    if (!action || type === 'turn_started') continue;
    const event = makeHistoryEvent(file, sourceHash, record.line, 'events', action, (type ?? '').startsWith('permission_') ? 'approval' : 'session', record.value, model,
      { ...(toolName ? { toolName } : {}), severity, ...(raw ? { raw } : {}) });
    if (type === 'permission_requested' || type === 'permission_resolved') event.approval = { required: true, ...(type === 'permission_resolved' ? { decision: normalizeDecision(record.value.decision) } : {}) };
    groups.push({ source: 'events', line: record.line, events: [addDeterministicId(event, sourceHash, record.line, 0)] });
  }
  // Each source retains its own monotonic physical-line order. Session start remains first.
  return groups.sort((left, right) => left.source === right.source ? left.line - right.line : left.source === 'chat' ? -1 : 1);
}

function sessionStartEvent(
  file: GrokSession, summary: JsonObject, lifecycle: ParsedLog, sourceHash: string, model: string | undefined, alreadyStarted: boolean,
): EventGroup | undefined {
  if (alreadyStarted) return undefined;
  const firstTurn = lifecycle.records.find((record) => safeLabel(record.value.type, 64)?.toLowerCase() === 'turn_started');
  const timestamp = boundedString(firstTurn?.value.ts, 128) ?? boundedString(summary.created_at, 128);
  if (!timestamp) return undefined;
  const event = makeHistoryEvent(file, sourceHash, 0, 'session', 'session.started', 'session', summary, model, { timestamp });
  return { source: 'session', line: 0, events: [addDeterministicId(event, sourceHash, 0, 0)] };
}

async function deliverGroups(
  groups: EventGroup[], cursor: SessionCursor, chat: ParsedLog, lifecycle: ParsedLog,
  options: GrokHistoryOptions, result: GrokHistoryResult, checkpoint: () => Promise<void>,
): Promise<void> {
  let batch: ThreadforgeEvent[] = [];
  let acceptedGroups: EventGroup[] = [];
  const flush = async () => {
    if (batch.length === 0) return;
    await emitEvents(batch, { endpoint: options.endpoint, timeoutMs: 1_500, fetchImpl: options.fetchImpl });
    result.events_emitted += batch.length;
    for (const group of acceptedGroups) advanceCursorForGroup(cursor, group, chat, lifecycle);
    batch = [];
    acceptedGroups = [];
    await checkpoint();
  };
  for (const group of groups) {
    if (group.events.length > batchSize) throw new Error('one Grok source line maps to more than 100 events');
    if (batch.length + group.events.length > batchSize) await flush();
    if (group.events.length > 0) {
      batch.push(...group.events);
      acceptedGroups.push(group);
    }
    if (group.source === 'session') cursor.started = true;
    if (group.events.length === 0) advanceCursorForGroup(cursor, group, chat, lifecycle);
  }
  await flush();
}

function advanceCursorForGroup(cursor: SessionCursor, group: EventGroup, chat: ParsedLog, lifecycle: ParsedLog): void {
  if (group.source === 'session') { cursor.started = true; return; }
  const parsed = group.source === 'chat' ? chat : lifecycle;
  const target = group.source === 'chat' ? cursor.chat : cursor.events;
  const hash = parsed.prefixHashes.get(group.line);
  if (hash) { target.line = group.line; target.prefix_sha256 = hash; }
}

function advanceEmptyLines(cursor: SessionCursor, chat: ParsedLog, lifecycle: ParsedLog): void {
  const chatHash = chat.prefixHashes.get(chat.completeLines);
  if (chatHash && chat.completeLines >= cursor.chat.line) { cursor.chat = { line: chat.completeLines, prefix_sha256: chatHash }; }
  const eventHash = lifecycle.prefixHashes.get(lifecycle.completeLines);
  if (eventHash && lifecycle.completeLines >= cursor.events.line) { cursor.events = { line: lifecycle.completeLines, prefix_sha256: eventHash }; }
}

function cursorConflict(cursor: SourceCursor, log: ParsedLog): boolean {
  if (cursor.line === 0) return false;
  return log.completeLines < cursor.line || log.prefixHashes.get(cursor.line) !== cursor.prefix_sha256;
}

function makeHistoryEvent(
  file: GrokSession, sourceHash: string, line: number, source: string, action: string, category: string,
  record: JsonObject, model?: string,
  extra: { timestamp?: string; content?: JsonObject; text?: string; toolName?: string; toolCallHash?: string; toolInput?: unknown; severity?: ThreadforgeEvent['severity']; raw?: JsonObject; eventSuffix?: string } = {},
): ThreadforgeEvent {
  const sessionHash = sha256(`${file.projectKey}/${file.sessionName}`);
  const event: ThreadforgeEvent = {
    timestamp: parseTimestamp(extra.timestamp ?? record.ts ?? record.timestamp,
      source === 'events' ? file.events?.mtimeMs ?? file.chat.mtimeMs : file.chat.mtimeMs),
    vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'grok_build', collection_method: 'poll' },
    session: { id: `grok:${sessionHash}` },
    project: { id: sha256(file.projectKey) },
    raw: { source: 'grok_local_session', source_sha256: sourceHash, source_kind: source, ...(line > 0 ? { line } : {}), record_type: safeLabel(record.type, 64) ?? 'session' },
  };
  if (model) event.model = model;
  if (extra.toolName) event.tool = { name: extra.toolName };
  if (extra.toolCallHash) event.raw = { ...event.raw, tool_call_sha256: extra.toolCallHash };
  if (extra.content) event.content = extra.content;
  if (extra.raw) event.raw = { ...event.raw, ...extra.raw };
  if (extra.toolInput !== undefined) {
    const args = isRecord(extra.toolInput) ? extra.toolInput : {};
    const command = typeof args.command === 'string' ? args.command : undefined;
    if (command) event.command = digestContent(command);
    const filePath = boundedString(args.target_file ?? args.target_directory ?? args.path, 4_096);
    if (filePath) event.file = { path_sha256: sha256(filePath), operation: fileOperation(extra.toolName) };
  }
  if (extra.text !== undefined) {
    if (action === 'prompt.submitted') event.prompt = { text: extra.text };
    event.gen_ai = action === 'prompt.submitted'
      ? { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: extra.text }] }] } }
      : { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: extra.text }] }] } };
  }
  if (extra.eventSuffix) event.raw = { ...event.raw, event_suffix: extra.eventSuffix };
  return event;
}

function addDeterministicId(event: ThreadforgeEvent, sourceHash: string, line: number, index: number): ThreadforgeEvent {
  const suffix = typeof event.raw?.event_suffix === 'string' ? event.raw.event_suffix : '';
  event.event.id = sha256(`${sourceHash}:${event.harness.collection_method}:${line}:${index}:${suffix}`);
  return event;
}

function toolAction(name: string, completed: boolean): string {
  switch (name.toLowerCase()) {
    case 'run_terminal_command': return completed ? 'command.executed' : 'tool.invoked';
    case 'read_file': case 'list_dir': case 'grep': return completed ? 'file.read' : 'tool.invoked';
    case 'search_replace': case 'write_file': return completed ? 'file.modified' : 'tool.invoked';
    default: return completed ? 'tool.completed' : 'tool.invoked';
  }
}
function fileOperation(name?: string): string {
  switch (name?.toLowerCase()) {
    case 'read_file': case 'list_dir': case 'grep': return 'read';
    case 'write_file': return 'create';
    case 'search_replace': return 'modify';
    default: return 'unknown';
  }
}
function normalizeDecision(value: unknown): string {
  const decision = boundedString(value, 64)?.toLowerCase();
  if (['allow', 'allowed', 'approve', 'approved'].includes(decision ?? '')) return 'allowed';
  if (['deny', 'denied', 'reject', 'rejected'].includes(decision ?? '')) return 'denied';
  return 'unknown';
}
function isSuccess(value: unknown): boolean { return boundedString(value, 48)?.toLowerCase() === 'success'; }
function contentText(value: unknown): string {
  if (typeof value === 'string') return value.trim();
  if (Array.isArray(value)) return value.flatMap((part) => isRecord(part) ? [contentText(part.text)] : []).filter(Boolean).join('\n');
  return '';
}
function textValue(value: unknown): string { return contentText(value); }
function parseJsonValue(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}
function digestContent(value: unknown): JsonObject {
  if (value === undefined || value === null) return { retention: 'metadata', included: false };
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); } catch { return { retention: 'metadata', included: false }; }
  if (typeof text !== 'string') return { retention: 'metadata', included: false };
  const bytes = byteSize(text);
  return { retention: 'metadata', included: false, bytes, ...(bytes <= (64 << 10) ? { hash: sha256(text) } : { omitted: true }) };
}
function digestTextContent(value: string, includeContent: boolean): JsonObject {
  const bytes = byteSize(value);
  const included = includeContent && bytes <= maxContentBytes;
  return {
    retention: included ? 'full' : 'metadata', included,
    bytes,
    ...(bytes <= (64 << 10) ? { hash: sha256(value) } : { omitted: true }),
    ...(includeContent && !included ? { truncated: true } : {}),
  };
}
function parseTimestamp(value: unknown, fallbackMs: number): string {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    const date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof value === 'string') {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  const fallback = new Date(fallbackMs);
  return Number.isNaN(fallback.getTime()) ? new Date(0).toISOString() : fallback.toISOString();
}

function emptySessionCursor(fingerprint: string): SessionCursor {
  return { source_fingerprint: fingerprint, terminal_fingerprint: '', started: false, incomplete: false,
    chat: { line: 0, prefix_sha256: '' }, events: { line: 0, prefix_sha256: '' } };
}
function resolveStatePath(value?: string): string {
  if (value !== undefined && !path.isAbsolute(value)) throw new Error('--state-path must be an absolute path');
  return path.resolve(value ?? path.join(os.homedir(), '.threadforge', 'state', stateFileName));
}
async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await ensurePrivateDirectory(path.dirname(statePath));
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Grok history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Grok history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}
async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.grok-history-cursor.v2', version: stateVersion, sessions: {} };
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Grok history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('Grok history cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxStateBytes) throw new Error('Grok history cursor exceeds the size limit');
    const value: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    if (!isRecord(value) || value.schema_version !== empty.schema_version || value.version !== stateVersion || !isRecord(value.sessions)) {
      throw new Error('Grok history cursor has an unsupported schema');
    }
    const sessions: Record<string, SessionCursor> = {};
    for (const [key, entry] of Object.entries(value.sessions)) {
      if (!/^[a-f0-9]{64}$/.test(key) || !isRecord(entry) || typeof entry.source_fingerprint !== 'string' || typeof entry.terminal_fingerprint !== 'string' ||
          typeof entry.started !== 'boolean' || typeof entry.incomplete !== 'boolean' || !isSourceCursor(entry.chat) || !isSourceCursor(entry.events)) throw new Error('Grok history cursor contains invalid checkpoints');
      sessions[key] = { source_fingerprint: entry.source_fingerprint, terminal_fingerprint: entry.terminal_fingerprint,
        started: entry.started, incomplete: entry.incomplete, chat: entry.chat, events: entry.events };
    }
    return { schema_version: empty.schema_version, version: stateVersion, sessions };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Grok history cursor')) throw error;
    throw new Error('Grok history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}
async function writeState(statePath: string, state: CursorState): Promise<void> {
  await ensurePrivateDirectory(path.dirname(statePath));
  const bytes = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxStateBytes) throw new Error('Grok history cursor exceeds the size limit');
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
    throw new Error('unable to atomically write Grok history cursor');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Grok cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error('Grok cursor path cannot contain symbolic links or non-directory parents');
  }
}
function isSourceCursor(value: unknown): value is SourceCursor {
  return isRecord(value) && Number.isInteger(value.line) && (value.line as number) >= 0 &&
    typeof value.prefix_sha256 === 'string' && (value.prefix_sha256 === '' || /^[a-f0-9]{64}$/.test(value.prefix_sha256));
}
function sameFile(a: Stats, b: Stats | null): boolean { return b !== null && a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeMs === b.mtimeMs; }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
function safeLabel(value: unknown, max: number): string | undefined {
  const label = boundedString(value, max);
  return label && /^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(label) && !looksSecretLike(label) ? label : undefined;
}
function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}
function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function boundedInteger(value: number, min: number, max: number, label: string): number {
  if (!Number.isInteger(value) || value < min || value > max) throw new Error(`${label} must be between ${min} and ${max}`);
  return value;
}
function byteSize(value: string): number { return Buffer.byteLength(value, 'utf8'); }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && (error as { code?: unknown }).code === code; }
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
function safeError(_error: unknown): string { return 'Grok session could not be read safely; inspect local files and retry'; }
