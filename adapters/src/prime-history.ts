import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const cursorVersion = 1;
const cursorName = 'prime-session-cursor.v1.json';
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
type RootKind = 'session' | 'artifact';
type PrimeRoot = { path: string; kind: RootKind };
type Cursor = { last_line: number; started: boolean; prefix_sha256: string; size_bytes: number; mtime_ms: number; incomplete: boolean };
type CursorState = { schema_version: 'threadforge.prime-session-cursor.v1'; version: number; files: Record<string, Cursor> };
type PrimeFile = { path: string; root: string; relative: string; kind: RootKind; sourceSha256: string; size: number; mtimeMs: number; name: string };
type PrimeEntry = { line: number; value: JsonObject };
type ParsedFile = { entries: PrimeEntry[]; completeLines: number; prefixHashes: Map<number, string>; partialTail: boolean; malformed: number };
type ToolCall = { id: string; name: string; args: unknown };
type MapperState = {
  sessionIdHash: string; cwd?: string; model?: string; repository?: string; branch?: string; parentHash?: string;
  calls: Map<string, ToolCall>; started: boolean;
};
type EventGroup = { line: number; prefix: string; started: boolean; events: ThreadforgeEvent[] };

export type PrimeHistoryOptions = {
  sessionsDir?: string; artifactsDir?: string; statePath?: string; endpoint?: string; maxFiles?: number;
  preview?: boolean; includeContent?: boolean;
};
export type PrimeHistoryStatus = {
  schema_version: 'threadforge.prime-history-status.v1'; files: number; session_files: number; artifact_files: number;
  checkpointed_files: number; pending_files: number; symlinks_skipped: number; oversized_files_skipped: number; files_truncated: boolean;
};
export type PrimeHistoryResult = {
  schema_version: 'threadforge.prime-history-sync.v1'; files: number; session_files: number; artifact_files: number;
  files_changed: number; events_emitted: number; events_previewed: number; malformed_lines: number; partial_files: number;
  symlinks_skipped: number; oversized_files_skipped: number; files_truncated: boolean; preview_truncated: boolean;
  content_mode: 'metadata' | 'include-content'; complete: boolean; errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

export async function getPrimeHistoryStatus(
  options: Pick<PrimeHistoryOptions, 'sessionsDir' | 'artifactsDir' | 'statePath' | 'maxFiles'> = {},
): Promise<PrimeHistoryStatus> {
  const maximum = boundedInteger(options.maxFiles ?? defaultMaxFiles, 1, maxFiles, '--max-files');
  const { roots, statePath } = await resolveOptions(options);
  const listed = await listFiles(roots, maximum);
  const state = await readState(statePath);
  const checkpointed = listed.files.filter((file) => {
    const cursor = state.files[file.sourceSha256];
    return cursor && !cursor.incomplete && (cursor.last_line > 0 || cursor.started) &&
      cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
  }).length;
  return {
    schema_version: 'threadforge.prime-history-status.v1', files: listed.files.length,
    session_files: listed.files.filter((file) => file.kind === 'session').length,
    artifact_files: listed.files.filter((file) => file.kind === 'artifact').length,
    checkpointed_files: checkpointed, pending_files: listed.files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped, files_truncated: listed.truncated,
  };
}

export async function syncPrimeHistory(options: PrimeHistoryOptions = {}): Promise<PrimeHistoryResult> {
  validateOptions(options);
  const { roots, statePath } = await resolveOptions(options);
  const listed = await listFiles(roots, options.maxFiles ?? defaultMaxFiles);
  const result = emptyResult(options.includeContent ?? false);
  result.files = listed.files.length;
  result.session_files = listed.files.filter((file) => file.kind === 'session').length;
  result.artifact_files = listed.files.filter((file) => file.kind === 'artifact').length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.files_truncated = listed.truncated;
  if (options.preview) {
    await processFiles(listed.files, roots, await readState(statePath), options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFiles(listed.files, roots, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_files === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
  return result;
}

function emptyResult(includeContent: boolean): PrimeHistoryResult {
  return { schema_version: 'threadforge.prime-history-sync.v1', files: 0, session_files: 0, artifact_files: 0,
    files_changed: 0, events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_files: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false, preview_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false, errors: [] };
}

function validateOptions(options: PrimeHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxFiles ?? defaultMaxFiles, 1, maxFiles, '--max-files');
  if (options.sessionsDir !== undefined && !path.isAbsolute(options.sessionsDir)) throw new Error('--sessions-dir must be an absolute path');
  if (options.artifactsDir !== undefined && !path.isAbsolute(options.artifactsDir)) throw new Error('--artifacts-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveOptions(options: Pick<PrimeHistoryOptions, 'sessionsDir' | 'artifactsDir' | 'statePath'>): Promise<{ roots: PrimeRoot[]; statePath: string }> {
  const home = os.homedir();
  const override = process.env.PRIME_AGENT_CODING_AGENT_DIR?.trim();
  const agentDir = override ? expandTilde(override, home) : path.join(home, '.prime', 'agent');
  if (!path.isAbsolute(agentDir)) throw new Error('PRIME_AGENT_CODING_AGENT_DIR must resolve to an absolute path');
  const roots: PrimeRoot[] = [];
  for (const [kind, value, fallback] of [
    ['session', options.sessionsDir, path.join(agentDir, 'sessions')],
    ['artifact', options.artifactsDir, path.join(agentDir, 'session-artifacts')],
  ] as const) roots.push({ path: await resolveRoot(value ?? fallback, `Prime ${kind} directory`), kind });
  if (isWithin(roots[0]!.path, roots[1]!.path) || isWithin(roots[1]!.path, roots[0]!.path)) {
    throw new Error('Prime sessions and artifacts directories must not overlap');
  }
  const statePath = resolveStatePath(options.statePath);
  for (const root of roots) if (isWithin(root.path, statePath)) throw new Error('Prime history cursor must be stored outside Prime data directories');
  return { roots, statePath };
}

function expandTilde(value: string, home: string): string {
  if (value === '~') return home;
  if (value.startsWith('~/') || value.startsWith('~\\')) return path.join(home, value.slice(2));
  return value;
}

async function resolveRoot(value: string, label: string): Promise<string> {
  if (!path.isAbsolute(value)) throw new Error(`${label} must be an absolute path`);
  const resolved = path.resolve(value);
  const info = await lstat(resolved).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return resolved;
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error(`${label} must be a real directory`);
  const canonical = await realpath(resolved).catch(() => { throw new Error(`${label} could not be resolved`); });
  if (!await isSameRealPathWithoutLinks(canonical, resolved)) throw new Error(`${label} must not traverse symbolic links`);
  return canonical;
}

async function listFiles(
  roots: PrimeRoot[], maximum: number,
): Promise<{ files: PrimeFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const files: PrimeFile[] = [];
  let entriesScanned = 0;
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  let scanLimitReached = false;
  for (const root of roots) {
    const pending = [root.path];
    let rootFileCount = 0;
    let rootLimitReached = false;
    while (pending.length > 0 && !scanLimitReached && !rootLimitReached) {
      const directory = pending.pop()!;
      const entries = await readdir(directory, { withFileTypes: true }).catch((error: unknown) => {
        if (isCode(error, 'ENOENT')) return [];
        throw new Error('unable to enumerate Prime session data');
      });
      entries.sort((left, right) => left.name.localeCompare(right.name));
      for (const entry of entries) {
        if (++entriesScanned > maxScanEntries) { truncated = true; scanLimitReached = true; break; }
        const fullPath = path.join(directory, entry.name);
        const info = await lstat(fullPath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
        if (!info) continue;
        if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
        if (info.isDirectory()) { pending.push(fullPath); continue; }
        if (!info.isFile() || path.extname(entry.name).toLowerCase() !== '.jsonl') continue;
        if (info.size > maxFileBytes) { oversizedSkipped++; continue; }
        const canonical = await realpath(fullPath).catch(() => '');
        if (!canonical || !await isSameRealPathWithoutLinks(canonical, fullPath) || !isWithin(root.path, canonical)) { symlinksSkipped++; continue; }
        const relative = path.relative(root.path, canonical).split(path.sep).join('/');
        if (!isSafeRelative(relative)) { symlinksSkipped++; continue; }
        if (rootFileCount >= maximum) { truncated = true; rootLimitReached = true; break; }
        files.push({ path: canonical, root: root.path, relative, kind: root.kind,
          sourceSha256: sha256(`${root.kind}:${relative}`), size: info.size, mtimeMs: info.mtimeMs, name: entry.name });
        rootFileCount++;
      }
    }
    if (scanLimitReached) break;
  }
  files.sort((left, right) => left.mtimeMs - right.mtimeMs || left.kind.localeCompare(right.kind) || left.relative.localeCompare(right.relative));
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

async function readSource(file: PrimeFile, roots: PrimeRoot[]): Promise<Buffer> {
  const root = roots.find((item) => item.kind === file.kind)?.path;
  if (!root || root !== file.root || !isWithin(root, file.path)) throw new Error('Prime transcript source escaped its configured root');
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink()) throw new Error('Prime transcript source is no longer a regular file');
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error('Prime transcript path escaped its source root');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || opened.size !== file.size || opened.mtimeMs !== file.mtimeMs) throw new Error('Prime transcript changed before read');
    const bytes = await handle.readFile();
    const after = await handle.stat();
    const current = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, after) || !sameFile(opened, current) ||
        after.size !== opened.size || after.mtimeMs !== opened.mtimeMs) throw new Error('Prime transcript changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Prime transcript')) throw error;
    throw new Error('Prime transcript could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
}

function sameFile(left: Pick<Stats, 'dev' | 'ino'>, right: Pick<Stats, 'dev' | 'ino'> | null): boolean {
  return right !== null && left.dev === right.dev && left.ino === right.ino;
}

function parsePrimeFile(bytes: Buffer): ParsedFile {
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const hasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const entries: PrimeEntry[] = [];
  let offset = 0; let line = 0; let completeLines = 0; let malformed = 0; let partialTail = false;
  while (offset < bytes.length) {
    const newline = bytes.indexOf(0x0a, offset);
    if (newline < 0) { partialTail = true; break; }
    line++;
    if (line > maxLinesPerFile) { malformed++; break; }
    const start = offset;
    const end = newline > offset && bytes[newline - 1] === 0x0d ? newline - 1 : newline;
    const raw = bytes.subarray(offset, end);
    offset = newline + 1;
    if (raw.byteLength > maxLineBytes) { malformed++; break; }
    let value: unknown;
    if (raw.toString('utf8').trim() !== '') {
      try { value = JSON.parse(decoder.decode(raw)) as unknown; }
      catch { malformed++; break; }
      if (!isRecord(value)) { malformed++; break; }
    }
    hasher.update(bytes.subarray(start, newline + 1));
    completeLines = line;
    prefixHashes.set(line, hasher.copy().digest('hex'));
    if (isRecord(value)) entries.push({ line, value });
  }
  return { entries, completeLines, prefixHashes, partialTail, malformed };
}

function mapPrimeEntries(
  file: PrimeFile, parsed: ParsedFile, minLine: number, started: boolean, includeContent: boolean,
): { groups: EventGroup[]; state: MapperState } {
  const header = parsed.entries.find((entry) => typeOf(entry.value) === 'session')?.value ?? {};
  const sessionId = boundedString(header.id, 512) ?? path.basename(file.name, path.extname(file.name));
  const git = isRecord(header.git) ? header.git : {};
  const state: MapperState = {
    sessionIdHash: sha256(sessionId),
    ...(boundedString(header.cwd, 4_096) ? { cwd: String(header.cwd) } : {}),
    ...(safeLabel(header.model ?? header.modelId, 160) ? { model: safeLabel(header.model ?? header.modelId, 160) } : {}),
    ...(boundedString(git.repoUrl, 2_048) ? { repository: String(git.repoUrl) } : {}),
    ...(safeLabel(git.branch, 256) ? { branch: safeLabel(git.branch, 256) } : {}),
    ...(boundedString(header.parentSession, 512) ? { parentHash: sha256(String(header.parentSession)) } : {}),
    calls: new Map(), started,
  };
  const groups: EventGroup[] = [];
  if (!started && parsed.entries.length > 0) {
    const event = primeEvent(file, 0, 'session.started', 'session.started', 'session', 'Prime Agent session start recovered from local transcript',
      state, timestampValue(header.timestamp, file.mtimeMs), { prime: { source_kind: file.kind } });
    groups.push({ line: 0, prefix: sha256(Buffer.alloc(0)), started: true, events: [event] });
    state.started = true;
  }
  let eventCount = groups[0]?.events.length ?? 0;
  for (const entry of parsed.entries) {
    const events: ThreadforgeEvent[] = [];
    mapPrimeEntry(events, file, entry, state, entry.line > minLine, includeContent);
    eventCount += events.length;
    if (eventCount > maxEventsPerFile) throw new Error(`Prime transcript exceeds the ${maxEventsPerFile}-event limit`);
    const prefix = parsed.prefixHashes.get(entry.line);
    if (prefix) groups.push({ line: entry.line, prefix, started: state.started, events });
  }
  return { groups, state };
}

function mapPrimeEntry(
  events: ThreadforgeEvent[], file: PrimeFile, entry: PrimeEntry, state: MapperState, emit: boolean, includeContent: boolean,
): void {
  const data = entry.value;
  const type = typeOf(data);
  const timestamp = timestampValue(data.timestamp, file.mtimeMs);
  if (type === 'session') {
    const cwd = boundedString(data.cwd, 4_096);
    const model = safeLabel(data.model ?? data.modelId, 160);
    if (cwd) state.cwd = cwd;
    if (model) state.model = model;
    return;
  }
  if (type === 'model_change') {
    const model = safeLabel(data.modelId ?? data.model, 160);
    if (model) {
      state.model = model;
      if (emit) pushEvent(events, primeEvent(file, entry.line, 'model_change', 'session.context', 'session', 'Prime Agent model changed',
        state, timestamp, { prime: { change: 'model_change', model } }));
    }
    return;
  }
  if (type === 'compaction' && emit) {
    const summary = data.summary ?? data.details ?? data;
    pushEvent(events, primeEvent(file, entry.line, 'compaction', 'session.compacting', 'session', 'Prime Agent compacted session history',
      state, timestamp, { content: contentDigest(summary), prime: { source_event_type: 'compaction' } }));
    return;
  }
  if (type === 'custom' && data.customType === 'prime-agent.refinement' && emit) {
    pushEvent(events, primeEvent(file, entry.line, 'refinement', 'session.context', 'session', 'Prime Agent refinement state updated',
      state, timestamp, { content: contentDigest(data.data), prime: { custom_type: 'prime-agent.refinement' } }));
    return;
  }
  if (type === 'custom_message' && emit) {
    pushEvent(events, primeEvent(file, entry.line, 'custom_message', 'agent.message', 'session', 'Prime Agent custom message observed',
      state, timestamp, { content: contentDigest(data) }));
    return;
  }
  if (type === 'message') {
    if (isRecord(data.message)) mapPrimeMessage(events, file, entry.line, data.message, timestamp, state, emit, includeContent);
    return;
  }
  if (type === 'tool_call') {
    const call = makePrimeCall(entry.line, 0, data);
    rememberPrimeCall(state.calls, call);
    if (emit) pushEvent(events, primeToolEvent(file, entry.line, 'tool_call', 'tool.invoked', 'tool',
      'Prime Agent tool invoked', state, timestamp, call));
  } else if (type === 'tool_result' && emit) {
    mapPrimeToolResult(events, file, entry.line, timestamp, state, data,
      safeOpaque(data.toolCallId ?? data.callId ?? data.id, 512) ?? `line-${entry.line}`);
  } else if (type === 'error' && emit) mapPrimeError(events, file, entry.line, timestamp, state, data);
}

function mapPrimeMessage(
  events: ThreadforgeEvent[], file: PrimeFile, line: number, message: JsonObject, fallback: string,
  state: MapperState, emit: boolean, includeContent: boolean,
): void {
  const role = String(message.role ?? '').toLowerCase();
  const timestamp = timestampValue(message.timestamp, fallback);
  if (role === 'user') {
    if (emit) textParts(message.content).forEach((text, index) => pushEvent(events,
      textEvent(file, line, `user.${index}`, 'prompt.submitted', 'prompt', 'Prime Agent prompt recovered from local transcript',
        state, timestamp, text, includeContent, 'user')));
    return;
  }
  if (role === 'assistant') {
    const model = safeLabel(message.model ?? message.modelId, 160);
    if (model) state.model = model;
    const parts = Array.isArray(message.content) ? message.content : typeof message.content === 'string' ? [{ type: 'text', text: message.content }] : [];
    for (let index = 0; index < parts.length; index++) {
      const part = isRecord(parts[index]) ? parts[index] as JsonObject : {};
      const type = String(part.type ?? '').toLowerCase();
      if (type === 'thinking') {
        const thinking = textValue(part.thinking ?? part.text);
        if (emit && thinking) pushEvent(events, primeEvent(file, line, `assistant.${index}.thinking`, 'agent.reasoning', 'reasoning',
          'Prime Agent reasoning observed', state, timestamp, { content: contentDigest(thinking) }));
      } else if (type === 'text') {
        const text = textValue(part.text);
        if (emit && text) pushEvent(events, textEvent(file, line, `assistant.${index}.text`, 'agent.message', 'agent',
          'Prime Agent assistant message recovered from local transcript', state, timestamp, text, includeContent, 'assistant'));
      } else if (type === 'toolcall' || type === 'tool_call') {
        const call = makePrimeCall(line, index, part);
        rememberPrimeCall(state.calls, call);
        if (emit) pushEvent(events, primeToolEvent(file, line, `assistant.${index}.tool_call`, 'tool.invoked', 'tool',
          'Prime Agent tool invoked', state, timestamp, call));
      }
    }
    if (emit && isRecord(message.usage)) mapPrimeUsage(events, file, line, timestamp, state, message.usage);
    return;
  }
  if (['toolresult', 'tool_result', 'tool'].includes(role) && emit) {
    mapPrimeToolResult(events, file, line, timestamp, state, message,
      safeOpaque(message.toolCallId ?? message.callId, 512) ?? `line-${line}`);
  } else if (role === 'error' && emit) mapPrimeError(events, file, line, timestamp, state, message);
}

function makePrimeCall(line: number, index: number, value: JsonObject): ToolCall {
  const id = safeOpaque(value.id ?? value.toolCallId ?? value.callId, 512) ?? `line-${line}-call-${index}`;
  const name = safeLabel(value.name ?? value.toolName, 128) ?? 'unknown';
  return { id, name, args: value.arguments ?? value.args ?? {} };
}

function rememberPrimeCall(calls: Map<string, ToolCall>, call: ToolCall): void {
  if (calls.size >= maxCallCorrelations && !calls.has(call.id)) calls.delete(calls.keys().next().value!);
  calls.set(call.id, call);
}

function mapPrimeToolResult(
  events: ThreadforgeEvent[], file: PrimeFile, line: number, timestamp: string, state: MapperState, data: JsonObject, callId: string,
): void {
  const remembered = state.calls.get(callId);
  const call = remembered ?? { id: callId, name: safeLabel(data.toolName ?? data.name, 128) ?? 'unknown', args: {} };
  const output = textParts(data.content ?? data.output ?? data.result).join('\n');
  const details = isRecord(data.details) ? data.details : {};
  const failed = data.isError === true || isErrorStatus(details.status) || isErrorStatus(data.status);
  const command = primeCommandFor(call.name, call.args);
  const action = failed ? 'tool.failed' : command ? 'command.executed' : 'tool.completed';
  const category = !failed && command ? 'command' : 'tool';
  const event = primeToolEvent(file, line, 'tool_result', action, category, 'Prime Agent tool result observed', state, timestamp, call,
    failed ? 'high' : 'info');
  if (failed) event.error = { type: 'tool_error' };
  if (output) event.content = contentDigest(output);
  if (command) event.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8'),
    ...(output ? { output_sha256: sha256(output), output_bytes: Buffer.byteLength(output, 'utf8') } : {}),
    ...(call.name.toLowerCase() === 'ipython' ? { language: 'python' } : {}) };
  const primeRaw: JsonObject = {
    ...(call.name.toLowerCase() === 'ipython' ? { python: true } : {}),
    ...(typeof data.status === 'string' ? { status: safeLabel(data.status, 32) ?? 'unknown' } : {}),
  };
  if (Object.keys(primeRaw).length) event.raw = { ...event.raw, prime: primeRaw };
  pushEvent(events, event);
  state.calls.delete(callId);
  const attachments = isRecord(details) && Array.isArray(details.attachments) ? details.attachments : [];
  for (let index = 0; index < attachments.length; index++) {
    const attachment = isRecord(attachments[index]) ? attachments[index] as JsonObject : {};
    const attachmentPath = boundedString(attachment.path, 4_096);
    if (!attachmentPath) continue;
    const fileEvent = primeEvent(file, line, `attachment.${index}`, 'file.read', 'file', 'Prime Agent context attachment observed',
      state, timestamp, { prime: { path_sha256: sha256(attachmentPath), ...(safeLabel(attachment.mimeType, 128) ? { mime_type: safeLabel(attachment.mimeType, 128) } : {}) } });
    fileEvent.file = { path_sha256: sha256(attachmentPath), operation: 'read' };
    pushEvent(events, fileEvent);
  }
}

function primeCommandFor(name: string, args: unknown): string | undefined {
  const object = isRecord(args) ? args : {};
  if (name.toLowerCase() === 'ipython') return boundedString(object.code, 1 << 20);
  if (name.toLowerCase() === 'bash') return boundedString(object.command, 1 << 20);
  return boundedString(object.command ?? object.cmd, 1 << 20);
}

function primeToolEvent(
  file: PrimeFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string, call: ToolCall, severity: ThreadforgeEvent['severity'] = 'info',
): ThreadforgeEvent {
  const name = safeLabel(call.name, 128) ?? 'unknown';
  const callHash = sha256(call.id);
  const args = digestUnknown(call.args);
  const event = primeEvent(file, line, suffix, action, category, message, state, timestamp,
    { severity, ...(args ? { content: args } : {}), prime: { tool_call_sha256: callHash, ...(args ? { arguments: args } : {}) } });
  event.tool = { name };
  event.gen_ai = { operation: { name: 'execute_tool' }, tool: { name, call: { id: `prime-call:${callHash}`, ...(args ? { arguments: args } : {}) } } };
  const command = primeCommandFor(name, call.args);
  if (command) event.tool = { name, command_sha256: sha256(command) };
  const object = isRecord(call.args) ? call.args : {};
  const pathValue = boundedString(object.path ?? object.file_path ?? object.target_path, 4_096);
  if (pathValue) event.tool = { ...(isRecord(event.tool) ? event.tool : { name }), path_sha256: sha256(pathValue) };
  return event;
}

function mapPrimeError(events: ThreadforgeEvent[], file: PrimeFile, line: number, timestamp: string, state: MapperState, data: JsonObject): void {
  const message = firstText(data.errorMessage, data.message, data.content) || textParts(data.content).join('\n') || 'Prime Agent runtime error observed';
  const event = primeEvent(file, line, 'error', 'agent.error', 'session', 'Prime Agent runtime error observed', state, timestamp,
    { severity: 'high', content: contentDigest(message), prime: { error_sha256: sha256(message), error_bytes: Buffer.byteLength(message, 'utf8') } });
  event.error = { type: 'runtime_error' };
  pushEvent(events, event);
}

function mapPrimeUsage(events: ThreadforgeEvent[], file: PrimeFile, line: number, timestamp: string, state: MapperState, value: JsonObject): void {
  const usage: JsonObject = {};
  for (const [source, target] of [['input', 'input_tokens'], ['output', 'output_tokens'], ['cacheRead', 'cache_read_input_tokens'],
    ['cacheWrite', 'cache_creation_input_tokens'], ['reasoning', 'reasoning_tokens']] as const) {
    const count = nonNegativeInteger(value[source]);
    if (count !== undefined) usage[target] = count;
  }
  const cost = isRecord(value.cost) ? finiteNonNegative(value.cost.total) : undefined;
  if (cost !== undefined) usage.cost_usd = cost;
  if (Object.keys(usage).length === 0) return;
  const event = primeEvent(file, line, 'usage', 'token.usage', 'metric', 'Prime Agent token usage recovered from local transcript', state, timestamp);
  event.gen_ai = { usage };
  pushEvent(events, event);
}

function primeEvent(
  file: PrimeFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string,
  extra: { content?: JsonObject; severity?: ThreadforgeEvent['severity']; prime?: JsonObject } = {},
): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${file.sourceSha256}:${state.sessionIdHash}:${line}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'prime_agent', collection_method: 'poll' },
    session: { id: `prime:${state.sessionIdHash}`, source_kind: file.kind },
    raw: { source: 'prime_session_jsonl', source_sha256: file.sourceSha256, source_kind: file.kind, line, message,
      ...(state.cwd ? { cwd_sha256: sha256(state.cwd) } : {}), ...(state.repository ? { repository_sha256: sha256(state.repository) } : {}),
      ...(state.branch ? { branch_sha256: sha256(state.branch) } : {}), ...(state.parentHash ? { parent_session_sha256: state.parentHash } : {}),
      ...(extra.prime ? { prime: extra.prime } : {}) },
  };
  if (state.model) event.model = state.model;
  if (extra.content) event.content = extra.content;
  return event;
}

function textEvent(
  file: PrimeFile, line: number, suffix: string, action: string, category: string, message: string,
  state: MapperState, timestamp: string, text: string, includeContent: boolean, role: 'user' | 'assistant',
): ThreadforgeEvent {
  const included = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
  const event = primeEvent(file, line, suffix, action, category, message, state, timestamp, { content: contentDigest(text, included, includeContent) });
  if (included && role === 'user') {
    event.prompt = { text };
    event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } };
  } else if (included) event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } };
  return event;
}

function pushEvent(events: ThreadforgeEvent[], event: ThreadforgeEvent): void {
  if (events.length >= eventBatchSize) throw new Error(`Prime transcript row exceeds the ${eventBatchSize}-event batch limit`);
  events.push(event);
}

function textParts(value: unknown): string[] {
  if (typeof value === 'string') return value.trim() ? [value] : [];
  if (Array.isArray(value)) return value.flatMap((item) => textParts(item));
  if (!isRecord(value)) return [];
  if (typeof value.text === 'string') return value.text.trim() ? [value.text] : [];
  return [];
}

function digestUnknown(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(value) ?? ''; } catch { return undefined; }
  return { hash: sha256(serialized), bytes: Buffer.byteLength(serialized, 'utf8') };
}

function contentDigest(value: unknown, included = false, requested = false): JsonObject {
  const digest = digestUnknown(value);
  return { retention: included ? 'full' : 'metadata', included, ...(digest ?? {}), ...(requested && !included ? { truncated: true } : {}) };
}

function extractText(value: unknown): string {
  if (value === undefined || value === null) return '';
  const parts = textParts(value);
  if (parts.length) return parts.join('\n');
  try { return JSON.stringify(value) ?? ''; } catch { return ''; }
}

function firstText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'string' && value.length) return value;
    if (isRecord(value) && typeof value.text === 'string') return value.text;
  }
  return '';
}

function typeOf(value: JsonObject): string { return typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''; }

async function processFiles(
  files: PrimeFile[], roots: PrimeRoot[], state: CursorState, options: PrimeHistoryOptions, result: PrimeHistoryResult,
  persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    try {
      const bytes = await readSource(file, roots);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime sync exceeded the aggregate source-byte limit' });
        break;
      }
      const parsed = parsePrimeFile(bytes);
      result.malformed_lines += parsed.malformed;
      if (parsed.partialTail) result.partial_files++;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript has a malformed row; scanning stops at the safe prefix' });
      if (parsed.partialTail) result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript ends in an incomplete row; waiting for the next sweep' });
      const previous = state.files[file.sourceSha256];
      if (previous && (parsed.completeLines < previous.last_line || parsed.prefixHashes.get(previous.last_line) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript prefix changed; refusing automatic replay' });
        continue;
      }
      const { groups, state: mapperState } = mapPrimeEntries(file, parsed, previous?.last_line ?? 0,
        previous?.started ?? false, options.includeContent ?? false);
      const pendingCount = groups.reduce((count, group) => count + group.events.length, 0);
      if (pendingCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime sync exceeded the event processing limit' });
        break;
      }
      if (options.preview) {
        result.events_previewed += pendingCount;
        result.events ??= [];
        for (const group of groups) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let pendingGroups: EventGroup[] = [];
      let pendingEvents: ThreadforgeEvent[] = [];
      const flush = async () => {
        if (pendingGroups.length === 0) return;
        if (pendingEvents.length) {
          await emitEvents(pendingEvents, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
          result.events_emitted += pendingEvents.length;
        }
        const last = pendingGroups[pendingGroups.length - 1]!;
        const next = makeCursor(last.line, last.prefix, last.started, file, parsed.partialTail || parsed.malformed > 0);
        if (cursorChanged(state.files[file.sourceSha256], next)) {
          state.files[file.sourceSha256] = next;
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
        result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected Prime history events' });
        continue;
      }

      const finalPrefix = parsed.prefixHashes.get(parsed.completeLines);
      if (finalPrefix) {
        const next = makeCursor(parsed.completeLines, finalPrefix, mapperState.started, file, parsed.partialTail || parsed.malformed > 0);
        if (cursorChanged(state.files[file.sourceSha256], next)) {
          state.files[file.sourceSha256] = next;
          result.files_changed++;
          if (persist && statePath) await writeState(statePath, state);
        }
      }
    } catch {
      result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(line: number, prefix: string, started: boolean, file: PrimeFile, incomplete: boolean): Cursor {
  return { last_line: line, started, prefix_sha256: prefix, size_bytes: file.size, mtime_ms: file.mtimeMs, incomplete };
}

function cursorChanged(previous: Cursor | undefined, next: Cursor): boolean {
  return !previous || previous.last_line !== next.last_line || previous.started !== next.started ||
    previous.prefix_sha256 !== next.prefix_sha256 || previous.size_bytes !== next.size_bytes ||
    previous.mtime_ms !== next.mtime_ms || previous.incomplete !== next.incomplete;
}

function resolveStatePath(value?: string): string {
  if (value !== undefined) {
    if (!path.isAbsolute(value)) throw new Error('--state-path must be an absolute path');
    return path.resolve(value);
  }
  let root = process.env.THREADFORGE_HOME?.trim();
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
    if (isCode(error, 'EEXIST')) throw new Error('Prime history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Prime history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.prime-session-cursor.v1', version: cursorVersion, files: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes) throw new Error('Prime history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('Prime cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxCursorBytes) throw new Error('Prime cursor exceeds local size limit');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.files)) {
      throw new Error('Prime history cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, Cursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.last_line) ||
          typeof value.started !== 'boolean' || typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
          typeof value.incomplete !== 'boolean') throw new Error('Prime history cursor contains an invalid checkpoint');
      files[key] = { last_line: value.last_line, started: value.started, prefix_sha256: value.prefix_sha256,
        size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, incomplete: value.incomplete };
    }
    return { ...empty, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Prime history cursor')) throw error;
    throw new Error('Prime history cursor is malformed or unreadable');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Prime cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Prime cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const bytes = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (bytes.byteLength > maxCursorBytes) throw new Error('Prime history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Prime history cursor');
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}
function nonNegativeInteger(value: unknown): number | undefined { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined; }
function finiteNonNegative(value: unknown): number | undefined { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined; }
function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value: unknown, maximum: number): string | undefined { return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined; }
function textValue(value: unknown): string { return typeof value === 'string' ? value : ''; }
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
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function firstRecord(...values: unknown[]): JsonObject { for (const value of values) if (isRecord(value)) return value; return {}; }
function isErrorStatus(value: unknown): boolean { return typeof value === 'string' && ['error', 'failed', 'failure'].includes(value.trim().toLowerCase()); }
function timestampValue(value: unknown, fallback: string | number): string {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    const date = new Date(value < 1_000_000_000_000 ? value * 1_000 : value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof value === 'string') {
    const numeric = Number(value);
    if (value.trim() && Number.isFinite(numeric) && numeric >= 0) return timestampValue(numeric, fallback);
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof fallback === 'number') {
    const date = new Date(fallback);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof fallback === 'string') {
    const date = new Date(fallback);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  return new Date(0).toISOString();
}
function normalizedOS(platform: string): string { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function isSafeRelative(value: string): boolean { return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
