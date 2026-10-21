import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks, isWithinRealPaths } from './path-safety.ts';

const stateVersion = 1;
const stateFileName = 'copilot-sessions.v1.json';
const maxStateBytes = 8 << 20;
const maxSessions = 5_000;
const maxFileBytes = 64 << 20;
const maxTotalBytes = 512 << 20;
const maxLineBytes = 8 << 20;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxToolCorrelations = 10_000;
const maxModels = 128;
const batchSize = 100;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;

type JsonObject = Record<string, unknown>;
type SessionCursor = { line: number; size_bytes: number; mtime_ms: number; prefix_sha256: string; session_started: boolean };
type CursorState = { schema_version: 'threadforge.copilot-session-cursor.v1'; version: number; files: Record<string, SessionCursor> };
type CopilotFile = { key: string; path: string; sessionId: string; size: number; mtimeMs: number };
type ToolCall = { name: string };
type UsageTotals = { input: number; output: number; cacheRead: number; cacheWrite: number; reasoning: number };
type MapperState = {
  sessionId: string;
  started: boolean;
  version?: string;
  model?: string;
  tools: Map<string, ToolCall>;
  lastUsage: Map<string, UsageTotals>;
  outputSinceTotals: Map<string, number>;
};

export type CopilotHistoryOptions = {
  copilotDir?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type CopilotHistoryResult = {
  schema_version: 'threadforge.copilot-history-sync.v1';
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

export type CopilotHistoryStatus = {
  schema_version: 'threadforge.copilot-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  sessions_truncated: boolean;
};

export async function getCopilotHistoryStatus(
  options: Pick<CopilotHistoryOptions, 'copilotDir' | 'statePath' | 'maxSessions'> = {},
): Promise<CopilotHistoryStatus> {
  const maximum = boundedInteger(options.maxSessions ?? 1_000, 1, maxSessions, '--max-sessions');
  const copilotDir = await resolveCopilotDir(options.copilotDir);
  const listed = await listSessionFiles(copilotDir, maximum);
  const state = await readState(resolveStatePath(options.statePath));
  const checkpointed = listed.files.filter((file) => {
    const cursor = state.files[sha256(file.key)];
    return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
  }).length;
  return {
    schema_version: 'threadforge.copilot-history-status.v1', sessions: listed.files.length,
    checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    sessions_truncated: listed.truncated,
  };
}

export async function syncCopilotHistory(options: CopilotHistoryOptions = {}): Promise<CopilotHistoryResult> {
  validateOptions(options);
  const copilotDir = await resolveCopilotDir(options.copilotDir);
  const statePath = resolveStatePath(options.statePath);
  if (await isWithinRealPaths(copilotDir, statePath) !== false) throw new Error('Copilot history cursor must be stored outside the Copilot directory');
  const listed = await listSessionFiles(copilotDir, options.maxSessions ?? 1_000);
  const result = emptyResult(options.includeContent ?? false);
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.sessions_truncated = listed.truncated;
  if (options.preview) {
    await processFiles(listed.files, copilotDir, await readState(statePath), options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFiles(listed.files, copilotDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated;
  return result;
}

function emptyResult(includeContent: boolean): CopilotHistoryResult {
  return {
    schema_version: 'threadforge.copilot-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: CopilotHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? 1_000, 1, maxSessions, '--max-sessions');
  if (options.copilotDir !== undefined && !path.isAbsolute(options.copilotDir)) throw new Error('--copilot-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveCopilotDir(value?: string): Promise<string> {
  const root = value ?? path.join(os.homedir(), '.copilot');
  if (!path.isAbsolute(root)) throw new Error('--copilot-dir must be an absolute path');
  const info = await lstat(root).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Copilot directory');
  });
  if (!info) return path.resolve(root);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Copilot directory must be a real directory');
  const canonical = await realpath(root).catch(() => { throw new Error('Copilot directory could not be resolved'); });
  if (!await isSameRealPathWithoutLinks(canonical, root)) throw new Error('Copilot directory must not traverse symbolic links');
  return canonical;
}

async function listSessionFiles(
  copilotDir: string, maximum: number,
): Promise<{ files: CopilotFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const root = path.join(copilotDir, 'session-state');
  const rootInfo = await lstat(root).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Copilot session-state directory');
  });
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Copilot session-state path must be a real directory');
  const canonicalRoot = await realpath(root).catch(() => { throw new Error('Copilot session-state directory could not be resolved'); });
  if (!isWithin(copilotDir, canonicalRoot)) throw new Error('Copilot session-state path escaped the Copilot directory');
  const files: CopilotFile[] = [];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  const entries = await readdir(canonicalRoot, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Copilot sessions'); });
  for (const entry of entries) {
    const directory = path.join(canonicalRoot, entry.name);
    const dirInfo = await lstat(directory).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!dirInfo) continue;
    if (dirInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!dirInfo.isDirectory()) continue;
    const sessionDir = await realpath(directory).catch(() => '');
    if (!sessionDir || !isWithin(canonicalRoot, sessionDir)) { symlinksSkipped++; continue; }
    const sourcePath = path.join(sessionDir, 'events.jsonl');
    const fileInfo = await lstat(sourcePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!fileInfo) continue;
    if (fileInfo.isSymbolicLink()) { symlinksSkipped++; continue; }
    if (!fileInfo.isFile()) continue;
    if (fileInfo.size > maxFileBytes) { oversizedSkipped++; continue; }
    const canonicalFile = await realpath(sourcePath).catch(() => '');
    if (!canonicalFile || !isWithin(canonicalRoot, canonicalFile)) { symlinksSkipped++; continue; }
    const key = path.relative(canonicalRoot, canonicalFile).split(path.sep).join('/');
    if (!isSafeRelative(key)) { symlinksSkipped++; continue; }
    files.push({ key, path: canonicalFile, sessionId: entry.name, size: fileInfo.size, mtimeMs: fileInfo.mtimeMs });
    if (files.length > maximum) { truncated = true; break; }
  }
  files.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  if (truncated) files.length = maximum;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

function isSafeRelative(value: string): boolean {
  return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..');
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function readSessionFile(file: CopilotFile, root: string): Promise<Buffer> {
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes) throw new Error('Copilot session is not a bounded regular file');
  if (before.size !== file.size || before.mtimeMs !== file.mtimeMs) throw new Error('Copilot session changed after enumeration');
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, file.path) || !isWithin(root, canonical)) throw new Error('Copilot session path escaped the Copilot directory');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error('Copilot session changed before reading');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) throw new Error('Copilot session changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Copilot session')) throw error;
    throw new Error('Copilot session could not be opened safely');
  } finally { await handle?.close().catch(() => {}); }
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
  started: boolean;
};

function parseAndMap(bytes: Buffer, file: CopilotFile, cursor: SessionCursor | undefined, includeContent: boolean): ParsedFile {
  const groups: Array<{ line: number; events: ThreadforgeEvent[] }> = [];
  const fileHash = sha256(file.key);
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>([[0, sha256(Buffer.alloc(0))]]);
  const cursorLine = cursor?.line ?? 0;
  const state: MapperState = {
    sessionId: file.sessionId, started: cursor?.session_started ?? false,
    tools: new Map(), lastUsage: new Map(), outputSinceTotals: new Map(),
  };
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let line = 0;
  let safeLines = 0;
  let start = 0;
  let malformed = 0;
  let eventCount = 0;
  let stoppedOnMalformed = false;
  for (let offset = 0; offset < bytes.length; offset++) {
    if (bytes[offset] !== 0x0a) continue;
    line++;
    if (line > maxLinesPerFile) throw new Error(`Copilot session exceeds the ${maxLinesPerFile}-line processing limit`);
    const chunkStart = start;
    let end = offset;
    if (end > start && bytes[end - 1] === 0x0d) end--;
    const raw = bytes.subarray(start, end);
    start = offset + 1;
    let record: JsonObject | undefined;
    if (raw.length > maxLineBytes) {
      malformed++;
      stoppedOnMalformed = true;
      break;
    }
    if (raw.length > 0) {
      try {
        const decoded = JSON.parse(decoder.decode(raw)) as unknown;
        if (!isRecord(decoded) || !safeLabel(decoded.type, 96)) throw new Error('invalid event row');
        record = decoded;
      } catch {
        malformed++;
        stoppedOnMalformed = true;
        break;
      }
    }
    prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
    safeLines = line;
    prefixHashes.set(line, prefixHasher.copy().digest('hex'));
    if (!record) continue;
    const events = mapRecord(record, line, file, fileHash, state, cursorLine, includeContent);
    if (line <= cursorLine || events.length === 0) continue;
    if (events.length > batchSize) throw new Error(`Copilot session line ${line} maps to more than ${batchSize} events`);
    groups.push({ line, events });
    eventCount += events.length;
    if (eventCount > maxEventsPerFile) throw new Error(`Copilot session exceeds the ${maxEventsPerFile}-event processing limit`);
  }
  return {
    completeLines: safeLines, partialTail: !stoppedOnMalformed && start < bytes.length, malformed,
    prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex'), groups, started: state.started,
  };
}

function mapRecord(
  record: JsonObject, line: number, file: CopilotFile, fileHash: string,
  state: MapperState, cursorLine: number, includeContent: boolean,
): ThreadforgeEvent[] {
  const type = safeLabel(record.type, 96) ?? '';
  const data = isRecord(record.data) ? record.data : {};
  const events: ThreadforgeEvent[] = [];
  const id = boundedString(record.id, 512);
  if (type === 'session.start' || type === 'session.resume') {
    updateSessionContext(data, state);
    const wasStarted = state.started;
    state.started = true;
    if (line > cursorLine && !wasStarted) {
      events.push(makeEvent(record, data, file, fileHash, state, line, 'session.started', 'session',
        'GitHub Copilot CLI session start recovered from local transcript', `session.start:${id ?? line}`));
    } else if (line > cursorLine && type === 'session.resume') {
      events.push(makeEvent(record, data, file, fileHash, state, line, 'session.context', 'session',
        'GitHub Copilot CLI session resumed', `session.resume:${id ?? line}`));
    }
    return events;
  }
  if (type === 'session.model_change') {
    const model = safeLabel(data.newModel ?? data.selectedModel, 128);
    if (model) state.model = model;
    if (line > cursorLine) events.push(makeEvent(record, data, file, fileHash, state, line, 'session.context', 'session',
      'GitHub Copilot CLI model context changed', `session.model_change:${id ?? line}`));
    return events;
  }
  if (type === 'user.message') {
    const text = firstText(data.content, data.transformedContent);
    if (line > cursorLine) pushTextEvent(events, record, data, file, fileHash, state, line, 'prompt.submitted', 'prompt', text, includeContent, 'user');
    return events;
  }
  if (type === 'assistant.message') {
    const model = safeLabel(data.model, 128);
    if (model) state.model = model;
    for (const [toolId, call] of parseToolRequests(data)) rememberCall(state, toolId, call);
    const outputTokens = nonNegativeInteger(data.outputTokens);
    if (outputTokens !== undefined && outputTokens > 0) {
      const usageModel = state.model ?? '_unknown';
      state.outputSinceTotals.set(usageModel, (state.outputSinceTotals.get(usageModel) ?? 0) + outputTokens);
    }
    if (line > cursorLine) {
      const reasoning = boundedString(data.reasoningText, 1 << 20);
      if (reasoning) events.push(makeEvent(record, data, file, fileHash, state, line, 'agent.reasoning', 'agent',
        'GitHub Copilot CLI reasoning digest recovered from local transcript', `assistant.reasoning:${id ?? line}`,
        { content: contentDigest(reasoning, false) }));
      const text = firstText(data.content);
      pushTextEvent(events, record, data, file, fileHash, state, line, 'agent.message', 'agent', text, includeContent, 'assistant');
      if (outputTokens !== undefined && outputTokens > 0) events.push(makeEvent(record, data, file, fileHash, state, line,
        'token.usage', 'metric', 'GitHub Copilot CLI assistant output token usage', `assistant.usage:${id ?? line}`,
        { genAi: { usage: { output_tokens: outputTokens } } }));
    }
    return events;
  }
  if (type === 'tool.execution_start') {
    const call = parseToolCall(data);
    if (call.id) rememberCall(state, call.id, call);
    if (line > cursorLine && call.name) {
      const input = digestValue(call.input);
      events.push(makeEvent(record, data, file, fileHash, state, line, 'tool.invoked', 'tool',
        'GitHub Copilot CLI tool invocation recovered from local transcript', `tool.invoke:${id ?? line}`,
        { toolName: call.name, toolCallHash: call.id ? sha256(call.id) : undefined,
          content: { retention: 'metadata', included: false, ...(input ? { input_hash: input.hash, input_bytes: input.bytes } : {}) } }));
    }
    return events;
  }
  if (type === 'tool.execution_complete') {
    const callId = firstString(data.toolCallId, data.tool_call_id);
    const call = callId ? state.tools.get(callId) : undefined;
    if (callId) state.tools.delete(callId);
    const name = safeLabel(firstString(data.toolName, data.tool_name, call?.name), 128);
    if (line > cursorLine && name) {
      const success = data.success !== false;
      const category = toolCategory(name, success);
      const output = digestValue(data.result);
      events.push(makeEvent(record, data, file, fileHash, state, line, category.action, category.category,
        `GitHub Copilot CLI ${category.label} recovered from local transcript`, `tool.result:${id ?? line}`,
        { severity: success ? 'info' : 'medium', toolName: name, toolCallHash: callId ? sha256(callId) : undefined,
          content: { retention: 'metadata', included: false, ...(output ? { output_hash: output.hash, output_bytes: output.bytes } : {}) } }));
    }
    return events;
  }
  if (type === 'session.shutdown') {
    if (line > cursorLine) events.push(...mapShutdownUsage(record, data, file, fileHash, state, line, id));
    else rememberShutdownUsage(data, state);
    return events;
  }
  if (type === 'abort' && line > cursorLine) {
    events.push(makeEvent(record, data, file, fileHash, state, line, 'agent.error', 'session',
      'GitHub Copilot CLI request interrupted', `abort:${id ?? line}`, { severity: 'medium' }));
  }
  return events;
}

function updateSessionContext(data: JsonObject, state: MapperState): void {
  const sessionId = safeIdentifier(data.sessionId ?? data.session_id, 512);
  if (sessionId) state.sessionId = sessionId;
  const version = safeLabel(data.copilotVersion ?? data.version, 96);
  if (version) state.version = version;
  const model = safeLabel(data.selectedModel ?? data.currentModel, 128);
  if (model) state.model = model;
}

function parseToolCall(data: JsonObject): { id: string; name: string; input: unknown } {
  const id = safeIdentifier(data.toolCallId ?? data.tool_call_id ?? data.id, 512) ?? '';
  const name = safeLabel(data.toolName ?? data.tool_name ?? data.name, 128) ?? '';
  return { id, name, input: parseArguments(data.arguments) };
}

function parseToolRequests(data: JsonObject): Array<[string, ToolCall]> {
  if (!Array.isArray(data.toolRequests)) return [];
  const calls: Array<[string, ToolCall]> = [];
  for (const item of data.toolRequests.slice(0, 1_000)) {
    if (!isRecord(item)) continue;
    const id = safeIdentifier(item.toolCallId ?? item.tool_call_id ?? item.id, 512);
    const name = safeLabel(item.toolName ?? item.tool_name ?? item.name, 128);
    if (id && name) calls.push([id, { name }]);
  }
  return calls;
}

function parseArguments(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}

function rememberCall(state: MapperState, id: string, call: ToolCall): void {
  if (state.tools.size >= maxToolCorrelations && !state.tools.has(id)) state.tools.delete(state.tools.keys().next().value!);
  state.tools.set(id, call);
}

function toolCategory(name: string, success: boolean): { action: string; category: string; label: string } {
  const tool = name.toLowerCase();
  if (!success) return { action: 'tool.failed', category: 'tool', label: 'tool failure' };
  if (tool.startsWith('mcp__')) return { action: 'mcp.tool_invoked', category: 'mcp', label: 'MCP tool result' };
  if (tool === 'bash') return { action: 'command.executed', category: 'command', label: 'command result' };
  if (['view', 'read', 'read_file', 'glob', 'rg', 'grep', 'search'].includes(tool)) return { action: 'file.read', category: 'file', label: 'file activity' };
  if (['create', 'write'].includes(tool)) return { action: 'file.created', category: 'file', label: 'file activity' };
  if (['edit', 'str_replace_editor', 'replace', 'patch'].includes(tool)) return { action: 'file.modified', category: 'file', label: 'file activity' };
  return { action: 'tool.completed', category: 'tool', label: 'tool result' };
}

function mapShutdownUsage(
  record: JsonObject, data: JsonObject, file: CopilotFile, fileHash: string, state: MapperState, line: number, id: string | undefined,
): ThreadforgeEvent[] {
  if (isRecord(data.modelMetrics)) {
    const events: ThreadforgeEvent[] = [];
    for (const [modelName, value] of Object.entries(data.modelMetrics).slice(0, maxModels)) {
      if (!isRecord(value) || !isRecord(value.usage)) continue;
      const model = safeLabel(modelName, 128);
      if (!model) continue;
      const current = usageTotalsFrom(value.usage);
      const previous = state.lastUsage.get(model) ?? zeroUsage();
      const outputReported = state.outputSinceTotals.get(model) ?? 0;
      const usage: JsonObject = {};
      assignPositive(usage, 'input_tokens', current.input - previous.input);
      assignPositive(usage, 'output_tokens', current.output - previous.output - outputReported);
      if (current.cacheRead > previous.cacheRead) usage.cache_read = { input_tokens: current.cacheRead - previous.cacheRead };
      if (current.cacheWrite > previous.cacheWrite) usage.cache_creation = { input_tokens: current.cacheWrite - previous.cacheWrite };
      if (current.reasoning > previous.reasoning) usage.reasoning = { output_tokens: current.reasoning - previous.reasoning };
      state.lastUsage.set(model, current);
      state.outputSinceTotals.set(model, 0);
      if (Object.keys(usage).length > 0) events.push(makeEvent(record, data, file, fileHash, { ...state, model }, line,
        'token.usage', 'metric', 'GitHub Copilot CLI shutdown token usage', `shutdown.usage:${model}:${id ?? line}`,
        { genAi: { usage } }));
    }
    return events;
  }
  return [];
}

function rememberShutdownUsage(data: JsonObject, state: MapperState): void {
  if (!isRecord(data.modelMetrics)) return;
  for (const [modelName, value] of Object.entries(data.modelMetrics).slice(0, maxModels)) {
    if (isRecord(value) && isRecord(value.usage)) {
      const model = safeLabel(modelName, 128);
      if (model) {
        state.lastUsage.set(model, usageTotalsFrom(value.usage));
        state.outputSinceTotals.set(model, 0);
      }
    }
  }
}

function usageTotalsFrom(value: JsonObject): UsageTotals {
  return {
    input: nonNegativeInteger(value.inputTokens ?? value.input_tokens) ?? 0,
    output: nonNegativeInteger(value.outputTokens ?? value.output_tokens) ?? 0,
    cacheRead: nonNegativeInteger(value.cacheReadTokens ?? value.cache_read_tokens) ?? 0,
    cacheWrite: nonNegativeInteger(value.cacheWriteTokens ?? value.cache_write_tokens) ?? 0,
    reasoning: nonNegativeInteger(value.reasoningTokens ?? value.reasoning_tokens) ?? 0,
  };
}

function zeroUsage(): UsageTotals { return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 }; }

function assignPositive(target: JsonObject, key: string, value: number): void {
  if (Number.isSafeInteger(value) && value > 0) target[key] = value;
}

function pushTextEvent(
  events: ThreadforgeEvent[], record: JsonObject, data: JsonObject, file: CopilotFile, fileHash: string,
  state: MapperState, line: number, action: string, category: string, text: string,
  includeContent: boolean, role: 'user' | 'assistant',
): void {
  const value = text.trim();
  if (!value) return;
  const included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
  events.push(makeEvent(record, data, file, fileHash, state, line, action, category,
    role === 'user' ? 'GitHub Copilot CLI prompt recovered from local transcript' : 'GitHub Copilot CLI assistant message recovered from local transcript',
    `${role}:${safeIdentifier(record.id, 512) ?? line}`, {
      content: contentDigest(value, included, includeContent), ...(included ? { text: value } : {}),
    }));
}

function makeEvent(
  record: JsonObject, data: JsonObject, file: CopilotFile, fileHash: string, state: MapperState, line: number,
  action: string, category: string, message: string, suffix: string,
  extra: { content?: JsonObject; text?: string; toolName?: string; toolCallHash?: string; severity?: ThreadforgeEvent['severity']; genAi?: JsonObject } = {},
): ThreadforgeEvent {
  const recordId = safeIdentifier(record.id, 512);
  const sessionId = safeIdentifier(data.sessionId ?? data.session_id, 512) ?? state.sessionId;
  const event: ThreadforgeEvent = {
    timestamp: parseTimestamp(record.timestamp, file.mtimeMs),
    vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${fileHash}:${line}:${recordId ? sha256(recordId) : ''}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'copilot-cli', ...(state.version ? { version: state.version } : {}), collection_method: 'poll' },
    session: { id: `copilot-cli:${sha256(sessionId)}` },
    raw: {
      source: 'copilot_cli_session_jsonl', source_sha256: fileHash, line,
      record_type: safeLabel(record.type, 96) ?? 'unknown', ...(recordId ? { record_id_sha256: sha256(recordId) } : {}), message,
    },
  };
  const model = state.model;
  if (model) event.model = model;
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
  return {
    retention: included ? 'full' : 'metadata', included,
    hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8'), ...(requested && !included ? { truncated: true } : {}),
  };
}

function digestValue(value: unknown): { hash: string; bytes: number } | undefined {
  if (value === undefined || value === null) return undefined;
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); } catch { return undefined; }
  if (typeof text !== 'string') return undefined;
  return { hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

function firstText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'string') return value;
    if (isRecord(value)) {
      for (const key of ['text', 'content', 'value']) if (typeof value[key] === 'string') return value[key] as string;
    }
  }
  return '';
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    const text = boundedString(value, 2_048);
    if (text) return text;
  }
  return '';
}

function safeIdentifier(value: unknown, maximum: number): string | undefined {
  return boundedString(value, maximum);
}

function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = boundedString(value, maximum);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function parseTimestamp(value: unknown, fallbackMs: number): string {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    const milliseconds = value < 1_000_000_000_000 ? value * 1_000 : value;
    const date = new Date(milliseconds);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  if (typeof value === 'string') {
    const numeric = Number(value);
    if (value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0) return parseTimestamp(numeric, fallbackMs);
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  return new Date(fallbackMs).toISOString();
}

async function processFiles(
  files: CopilotFile[], root: string, state: CursorState, options: CopilotHistoryOptions,
  result: CopilotHistoryResult, persist: boolean, statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const key = sha256(file.key);
    const previous = state.files[key];
    try {
      const bytes = await readSessionFile(file, root);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.sessions_truncated = true;
        result.errors.push({ source_sha256: key, message: 'Copilot sync exceeded the total source-byte limit' });
        break;
      }
      const parsed = parseAndMap(bytes, file, previous, options.includeContent ?? false);
      result.malformed_lines += parsed.malformed;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: key, message: 'Copilot transcript contains a malformed row; scanning stops before it' });
      if (parsed.partialTail) result.partial_sessions++;
      const cursorLine = previous?.line ?? 0;
      if (previous && (parsed.completeLines < cursorLine || parsed.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: key, message: 'Copilot transcript prefix changed; refusing automatic replay' });
        continue;
      }
      const fileEvents = parsed.groups.reduce((count, group) => count + group.events.length, 0);
      if (fileEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: key, message: 'Copilot sync exceeded the event processing limit' });
        break;
      }
      if (options.preview) {
        result.events_previewed += fileEvents;
        result.events ??= [];
        for (const group of parsed.groups) for (const event of group.events) {
          if (result.events.length < maxPreviewEvents) result.events.push(event);
          else result.preview_truncated = true;
        }
        continue;
      }
      let batch: Array<{ line: number; events: ThreadforgeEvent[] }> = [];
      let batchEvents = 0;
      let failed = false;
      const flush = async () => {
        if (batch.length === 0) return;
        const send = batch.flatMap((group) => group.events);
        await emitEvents(send, { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        const lastLine = batch[batch.length - 1]!.line;
        const started = previous?.session_started === true || batch.some((group) => group.events.some((event) => event.event.action === 'session.started'));
        const prefix = parsed.prefixHashes.get(lastLine);
        if (!prefix) throw new Error('Copilot session checkpoint prefix is unavailable');
        state.files[key] = makeCursor(lastLine, file, prefix, started);
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
        result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Copilot history events' });
      }
      if (failed) continue;
      if (parsed.completeLines > cursorLine || (previous !== undefined && previous.session_started !== parsed.started)) {
        state.files[key] = makeCursor(parsed.completeLines, file, parsed.completePrefixHash, parsed.started);
        result.sessions_changed++;
        if (persist && statePath) await writeState(statePath, state);
      }
    } catch {
      result.errors.push({ source_sha256: key, message: 'Copilot session could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(line: number, file: CopilotFile, prefix: string, started: boolean): SessionCursor {
  return { line, size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix, session_started: started };
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
    if (isCode(error, 'EEXIST')) throw new Error('Copilot history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Copilot history cursor');
  }
  try { return await action(); }
  finally { await lock.close().catch(() => {}); await unlink(`${statePath}.lock`).catch(() => {}); }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.copilot-session-cursor.v1', version: stateVersion, files: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Copilot history cursor');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Copilot history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (opened.dev !== info.dev || opened.ino !== info.ino) throw new Error('Copilot cursor changed before read');
    const bytes = await handle.readFile();
    if (bytes.byteLength > maxStateBytes) throw new Error('Copilot cursor exceeds local size limit');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
      throw new Error('Copilot history cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, SessionCursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean') {
        throw new Error('Copilot history cursor contains an invalid checkpoint');
      }
      files[key] = { line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
        prefix_sha256: value.prefix_sha256, session_started: value.session_started };
    }
    return { ...empty, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Copilot history cursor')) throw error;
    throw new Error('Copilot history cursor is malformed or unreadable');
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
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => { if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Copilot cursor directory'); });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Copilot cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const payload = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('Copilot history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Copilot history cursor');
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}

function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function isNonNegativeInteger(value: unknown): value is number { return nonNegativeInteger(value) !== undefined; }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
