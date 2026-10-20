import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, readdir, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { assertLoopbackEndpoint, emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const stateFileName = 'claude-sessions.v1.json';
const maxStateBytes = 8 << 20;
const maxRootFiles = 5_000;
const maxFileBytes = 64 << 20;
const maxTotalBytes = 512 << 20;
const maxLineBytes = 8 << 20;
const maxLinesPerFile = 100_000;
const maxEventsPerFile = 10_000;
const maxEventsPerSync = 20_000;
const maxToolCorrelations = 10_000;
const maxDepth = 12;
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
};
type CursorState = {
  schema_version: 'threadforge.claude-session-cursor.v1';
  version: number;
  files: Record<string, SessionCursor>;
};
type ClaudeFile = { key: string; path: string; size: number; mtimeMs: number };
type MappedItem = { line: number; event: ThreadforgeEvent };
type ToolCall = { name: string };

export type ClaudeHistoryOptions = {
  projectsDir?: string;
  statePath?: string;
  endpoint?: string;
  maxFiles?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type ClaudeHistoryResult = {
  schema_version: 'threadforge.claude-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  malformed_lines: number;
  partial_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
  events?: ThreadforgeEvent[];
};

type SessionStatus = {
  schema_version: 'threadforge.claude-history-status.v1';
  sessions: number;
  checkpointed_sessions: number;
  pending_sessions: number;
  symlinks_skipped: number;
  oversized_files_skipped: number;
  files_truncated: boolean;
};

export async function getClaudeHistoryStatus(options: Pick<ClaudeHistoryOptions, 'projectsDir' | 'statePath' | 'maxFiles'> = {}): Promise<SessionStatus> {
  const maxFiles = boundedInteger(options.maxFiles ?? 1_000, 1, maxRootFiles, '--max-files');
  const projectsDir = await resolveProjectsDir(options.projectsDir);
  const listed = await listSessionFiles(projectsDir, maxFiles);
  const state = await readState(resolveStatePath(options.statePath));
  const sessions = listed.files;
  const checkpointed = sessions.filter((file) => {
    const cursor = state.files[sha256(file.key)];
    return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
  }).length;
  return {
    schema_version: 'threadforge.claude-history-status.v1', sessions: sessions.length,
    checkpointed_sessions: checkpointed, pending_sessions: sessions.length - checkpointed,
    symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
    files_truncated: listed.truncated,
  };
}

export async function syncClaudeHistory(options: ClaudeHistoryOptions = {}): Promise<ClaudeHistoryResult> {
  validateOptions(options);
  const projectsDir = await resolveProjectsDir(options.projectsDir);
  const result = emptyResult(options.includeContent ?? false);
  const statePath = resolveStatePath(options.statePath);
  if (isWithin(projectsDir, statePath)) throw new Error('Claude history cursor must be stored outside Claude projects');
  const listed = await listSessionFiles(projectsDir, options.maxFiles ?? 1_000);
  result.sessions = listed.files.length;
  result.symlinks_skipped = listed.symlinksSkipped;
  result.oversized_files_skipped = listed.oversizedSkipped;
  result.files_truncated = listed.truncated;
  if (options.preview) {
    const state = await readState(statePath);
    await processFiles(listed.files, projectsDir, state, options, result, false);
  } else {
    await withStateLock(statePath, async () => {
      const state = await readState(statePath);
      await processFiles(listed.files, projectsDir, state, options, result, true, statePath);
      await writeState(statePath, state);
    });
  }
  result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
    result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
  return result;
}

function emptyResult(includeContent: boolean): ClaudeHistoryResult {
  return {
    schema_version: 'threadforge.claude-history-sync.v1', sessions: 0, sessions_changed: 0,
    events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
    symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: ClaudeHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxFiles ?? 1_000, 1, maxRootFiles, '--max-files');
  if (options.projectsDir !== undefined && !path.isAbsolute(options.projectsDir)) throw new Error('--projects-dir must be an absolute path');
  if (options.statePath !== undefined && !path.isAbsolute(options.statePath)) throw new Error('--state-path must be an absolute path');
  if (options.endpoint !== undefined) assertLoopbackEndpoint(options.endpoint);
}

async function resolveProjectsDir(value?: string): Promise<string> {
  let projectsDir = value;
  if (!projectsDir) {
    const configDir = process.env.CLAUDE_CONFIG_DIR?.trim();
    const home = configDir || path.join(os.homedir(), '.claude');
    if (!path.isAbsolute(home)) throw new Error('CLAUDE_CONFIG_DIR must be an absolute path');
    projectsDir = path.join(home, 'projects');
  }
  if (!path.isAbsolute(projectsDir)) throw new Error('--projects-dir must be an absolute path');
  const dirInfo = await lstat(projectsDir).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Claude projects directory');
  });
  if (!dirInfo) return path.resolve(projectsDir);
  if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink()) throw new Error('Claude projects path must be a real directory');
  const canonical = await realpath(projectsDir).catch(() => { throw new Error('Claude projects directory could not be resolved'); });
  if (canonical !== path.resolve(projectsDir)) throw new Error('Claude projects directory must not traverse symbolic links');
  return canonical;
}

async function listSessionFiles(root: string, maxFiles: number): Promise<{ files: ClaudeFile[]; symlinksSkipped: number; oversizedSkipped: number; truncated: boolean }> {
  const rootInfo = await lstat(root).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Claude projects directory');
  });
  if (!rootInfo) return { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false };
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) throw new Error('Claude projects path must be a real directory');
  const files: ClaudeFile[] = [];
  const pending = [root];
  let symlinksSkipped = 0;
  let oversizedSkipped = 0;
  let truncated = false;
  while (pending.length > 0) {
    const directory = pending.pop()!;
    const entries = await readdir(directory, { withFileTypes: true }).catch(() => { throw new Error('unable to enumerate Claude session paths'); });
    for (const entry of entries) {
      const absolute = path.join(directory, entry.name);
      const info = await lstat(absolute).catch((error: unknown) => {
        if (isCode(error, 'ENOENT')) return null;
        throw new Error('unable to inspect Claude session path');
      });
      if (!info) continue;
      if (info.isSymbolicLink()) { symlinksSkipped++; continue; }
      if (info.isDirectory()) {
        const depth = path.relative(root, absolute).split(path.sep).length;
        if (depth > maxDepth) throw new Error(`Claude projects exceed the directory-depth scan limit (${maxDepth})`);
        pending.push(absolute);
        continue;
      }
      if (!info.isFile() || path.extname(entry.name).toLowerCase() !== '.jsonl') continue;
      if (info.size > maxFileBytes) { oversizedSkipped++; continue; }
      const relative = path.relative(root, absolute);
      if (!isSafeRelative(relative)) { symlinksSkipped++; continue; }
      const canonical = await realpath(absolute).catch(() => '');
      if (!canonical || !isWithin(root, canonical)) { symlinksSkipped++; continue; }
      files.push({ key: relative.split(path.sep).join('/'), path: canonical, size: info.size, mtimeMs: info.mtimeMs });
      if (files.length > maxFiles) { truncated = true; break; }
    }
    if (truncated) break;
  }
  files.sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  if (truncated) files.length = maxFiles;
  return { files, symlinksSkipped, oversizedSkipped, truncated };
}

function isSafeRelative(value: string): boolean {
  return value !== '' && !path.isAbsolute(value) && !value.split(/[\\/]+/).includes('..');
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function readSessionFile(file: ClaudeFile, root: string): Promise<Buffer> {
  const before = await lstat(file.path).catch(() => null);
  if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes) throw new Error('Claude session is not a bounded regular file');
  if (before.size !== file.size || before.mtimeMs !== file.mtimeMs) throw new Error('Claude session changed after enumeration');
  const canonical = await realpath(file.path).catch(() => '');
  if (!canonical || !isWithin(root, canonical)) throw new Error('Claude session path escaped the projects directory');
  let handle;
  try {
    handle = await open(file.path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) throw new Error('Claude session changed before reading');
    const bytes = await handle.readFile();
    const afterHandle = await handle.stat();
    const afterPath = await lstat(file.path).catch(() => null);
    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) throw new Error('Claude session changed during read');
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Claude session')) throw error;
    throw new Error('Claude session could not be opened safely');
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

function parseAndMap(
  bytes: Buffer,
  file: ClaudeFile,
  cursor: SessionCursor | undefined,
  includeContent: boolean,
): ParsedFile {
  const groups: Array<{ line: number; events: ThreadforgeEvent[] }> = [];
  const tools = new Map<string, ToolCall>();
  const fileHash = sha256(file.key);
  const prefixHasher = createHash('sha256');
  const prefixHashes = new Map<number, string>();
  prefixHashes.set(0, sha256(Buffer.alloc(0)));
  let line = 0;
  let start = 0;
  let safeLines = 0;
  let malformed = 0;
  let eventCount = 0;
  let started = cursor?.session_started ?? false;
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let stoppedOnMalformed = false;
  for (let offset = 0; offset < bytes.length; offset++) {
    if (bytes[offset] !== 0x0a) continue;
    line++;
    if (line > maxLinesPerFile) throw new Error(`Claude session exceeds the ${maxLinesPerFile}-line processing limit`);
    const chunkStart = start;
    let end = offset;
    if (end > start && bytes[end - 1] === 0x0d) end--;
    const raw = bytes.subarray(start, end);
    start = offset + 1;
    let entry: JsonObject | undefined;
    if (raw.length > maxLineBytes) {
      malformed++;
      stoppedOnMalformed = true;
      break;
    }
    if (raw.length > 0) {
      try {
        const parsed = JSON.parse(decoder.decode(raw)) as unknown;
        if (!isRecord(parsed) || !boundedString(parsed.type, 96)) throw new Error('invalid row');
        entry = parsed;
      } catch {
        malformed++;
        stoppedOnMalformed = true;
        break;
      }
    }
    prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
    safeLines = line;
    prefixHashes.set(line, prefixHasher.copy().digest('hex'));
    if (!entry) continue;
    const events: ThreadforgeEvent[] = [];
    const sessionId = boundedString(entry.sessionId, 256);
    if (line > (cursor?.line ?? 0) && !started && sessionId) {
      events.push(makeEvent(entry, fileHash, sessionId, line, 'session.started', 'session', 'Claude Code session start recovered from local transcript', 'session.started'));
      started = true;
    }
    mapEntry(entry, line, fileHash, sessionId, tools, includeContent, events);
    if (line > (cursor?.line ?? 0) && events.length > 0) {
      if (events.length > batchSize) throw new Error(`Claude session line ${line} maps to more than ${batchSize} events`);
      groups.push({ line, events });
      eventCount += events.length;
      if (eventCount > maxEventsPerFile) throw new Error(`Claude session exceeds the ${maxEventsPerFile}-event processing limit`);
      prefixHashes.set(line, prefixHasher.copy().digest('hex'));
    }
  }
  const partialTail = !stoppedOnMalformed && start < bytes.length;
  return {
    completeLines: safeLines, partialTail, malformed, prefixHashes,
    completePrefixHash: prefixHasher.copy().digest('hex'), groups, started,
  };
}

function mapEntry(
  entry: JsonObject,
  line: number,
  fileHash: string,
  sessionId: string | undefined,
  tools: Map<string, ToolCall>,
  includeContent: boolean,
  events: ThreadforgeEvent[],
): void {
  const type = boundedString(entry.type, 96) ?? '';
  const message = isRecord(entry.message) ? entry.message : {};
  const content = message.content;
  const model = safeLabel(message.model, 128);
  if (type === 'user' && entry.isMeta !== true) {
    const blocks = Array.isArray(content) ? content : undefined;
    if (typeof content === 'string') pushTextEvent(events, entry, fileHash, sessionId, line, 'prompt.submitted', 'prompt', cleanUserText(content), includeContent, model, 'user');
    if (blocks) {
      const prompts: string[] = [];
      for (let index = 0; index < blocks.length; index++) {
        const block = blocks[index];
        if (!isRecord(block)) continue;
        if (block.type === 'text' && typeof block.text === 'string') {
          const clean = cleanUserText(block.text);
          if (clean) prompts.push(clean);
        } else if (block.type === 'tool_result') {
          pushToolResult(events, entry, fileHash, sessionId, line, block, tools, model, index);
        }
      }
      if (prompts.length > 0) pushTextEvent(events, entry, fileHash, sessionId, line, 'prompt.submitted', 'prompt', prompts.join('\n'), includeContent, model, 'user');
    }
    return;
  }
  if (type === 'assistant') {
    if (typeof content === 'string') {
      pushTextEvent(events, entry, fileHash, sessionId, line, 'agent.message', 'agent', content, includeContent, model, 'assistant');
    }
    const blocks = Array.isArray(content) ? content : [];
    const text: string[] = [];
    const reasoning: string[] = [];
    for (let index = 0; index < blocks.length; index++) {
      const block = blocks[index];
      if (!isRecord(block)) continue;
      if (block.type === 'text' && typeof block.text === 'string') text.push(block.text.trim());
      else if (block.type === 'thinking') {
        const thinking = boundedString(block.thinking ?? block.text, 1 << 20);
        if (thinking) reasoning.push(thinking);
      } else if (block.type === 'tool_use') {
        const id = boundedString(block.id, 512) ?? '';
        const name = safeLabel(block.name, 128) ?? '';
        const input = block.input;
        if (id) {
          if (tools.size >= maxToolCorrelations && !tools.has(id)) tools.delete(tools.keys().next().value!);
          tools.set(id, { name });
        }
        if (id && name) pushToolInvoke(events, entry, fileHash, sessionId, line, model, index, { id, name, input });
      }
    }
    if (text.some((part) => part.length > 0)) pushTextEvent(events, entry, fileHash, sessionId, line, 'agent.message', 'agent', text.filter(Boolean).join('\n'), includeContent, model, 'assistant');
    if (reasoning.length > 0) pushReasoningEvent(events, entry, fileHash, sessionId, line, reasoning.join('\n'), model);
    const usage = normalizedUsage(message.usage);
    if (usage) events.push(makeEvent(entry, fileHash, sessionId, line, 'token.usage', 'metric', 'Claude Code token usage recovered from local transcript', `usage:${boundedString(message.id, 256) ?? line}`, { model, genAi: { usage } }));
    return;
  }
  if (type === 'summary') {
    const summary = boundedString(entry.summary ?? entry.content, 1 << 20);
    if (summary) pushReasoningEvent(events, entry, fileHash, sessionId, line, summary, model, 'session.summary');
  }
}

function pushTextEvent(
  events: ThreadforgeEvent[], entry: JsonObject, fileHash: string, sessionId: string | undefined, line: number,
  action: string, category: string, text: string, includeContent: boolean, model: string | undefined, role: 'user' | 'assistant',
): void {
  const value = text.trim();
  if (!value) return;
  const included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
  const event = makeEvent(entry, fileHash, sessionId, line, action, category,
    role === 'user' ? 'Claude Code prompt recovered from local transcript' : 'Claude Code assistant message recovered from local transcript',
    `${role}:${boundedString(entry.uuid, 256) ?? line}`, {
      model,
      content: contentDigest(value, included, includeContent),
      ...(included ? { text: value } : {}),
    });
  events.push(event);
}

function pushReasoningEvent(
  events: ThreadforgeEvent[], entry: JsonObject, fileHash: string, sessionId: string | undefined, line: number,
  text: string, model: string | undefined, action = 'agent.reasoning',
): void {
  const value = text.trim();
  if (!value) return;
  events.push(makeEvent(entry, fileHash, sessionId, line, action, 'agent',
    action === 'session.summary' ? 'Claude Code session summary metadata observed' : 'Claude Code reasoning digest recovered from local transcript',
    `${action}:${boundedString(entry.uuid, 256) ?? line}`, { model, content: contentDigest(value, false) }));
}

function pushToolInvoke(
  events: ThreadforgeEvent[], entry: JsonObject, fileHash: string, sessionId: string | undefined, line: number,
  model: string | undefined, index: number, call: { id: string; name: string; input: unknown },
): void {
  const input = digestValue(call.input);
  events.push(makeEvent(entry, fileHash, sessionId, line, 'tool.invoked', 'tool', 'Claude Code tool invocation recovered from local transcript',
    `tool.invoke:${boundedString(entry.uuid, 256) ?? line}:${index}`, {
      model, toolName: call.name, toolCallHash: sha256(call.id),
      content: { retention: 'metadata', included: false, ...(input ? { input_hash: input.hash, input_bytes: input.bytes } : {}) },
    }));
}

function pushToolResult(
  events: ThreadforgeEvent[], entry: JsonObject, fileHash: string, sessionId: string | undefined, line: number,
  block: JsonObject, tools: Map<string, ToolCall>, model: string | undefined, index: number,
): void {
  const callId = boundedString(block.tool_use_id, 512);
  const call = callId ? tools.get(callId) : undefined;
  if (callId) tools.delete(callId);
  const value = toolResultText(block.content);
  const failed = block.is_error === true;
  const name = call?.name;
  const command = Boolean(name && ['bash', 'shell', 'terminal'].includes(name.toLowerCase()));
  const action = command && !failed ? 'command.executed' : failed ? 'tool.failed' : 'tool.completed';
  const output = digestValue(value);
  events.push(makeEvent(entry, fileHash, sessionId, line, action, command && !failed ? 'command' : 'tool',
    `Claude Code tool result recovered from local transcript`,
    `tool.result:${boundedString(entry.uuid, 256) ?? line}:${index}`, {
      model, severity: failed ? 'medium' : 'info', ...(name ? { toolName: name } : {}),
      ...(callId ? { toolCallHash: sha256(callId) } : {}),
      content: { retention: 'metadata', included: false, ...(output ? { output_hash: output.hash, output_bytes: output.bytes } : {}) },
    }));
}

function makeEvent(
  entry: JsonObject, fileHash: string, sessionId: string | undefined, line: number,
  action: string, category: string, message: string, suffix: string,
  extra: { model?: string; content?: JsonObject; text?: string; toolName?: string; toolCallHash?: string; severity?: ThreadforgeEvent['severity']; genAi?: JsonObject } = {},
): ThreadforgeEvent {
  const uuid = boundedString(entry.uuid, 256);
  const sourceSession = sessionId ?? `file:${fileHash}`;
  const event: ThreadforgeEvent = {
    timestamp: parseTimestamp(entry.timestamp),
    vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${fileHash}:${line}:${uuid ?? ''}:${suffix}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: extra.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'claude-code', collection_method: 'poll' },
    session: { id: `claude-code:${sha256(sourceSession)}` },
    raw: {
      source: 'claude_session_jsonl', source_sha256: fileHash, line,
      entry_type: boundedString(entry.type, 96) ?? 'unknown', ...(uuid ? { uuid_sha256: sha256(uuid) } : {}), message,
    },
  };
  if (extra.model) event.model = extra.model;
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
    hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8'),
    ...(requested && !included ? { truncated: true } : {}),
  };
}

function normalizedUsage(value: unknown): JsonObject | undefined {
  if (!isRecord(value)) return undefined;
  const usage: JsonObject = {};
  const input = nonNegativeInteger(value.input_tokens);
  const output = nonNegativeInteger(value.output_tokens);
  const cacheWrite = nonNegativeInteger(value.cache_creation_input_tokens ?? value.cache_creation_tokens);
  const cacheRead = nonNegativeInteger(value.cache_read_input_tokens ?? value.cache_read_tokens);
  if (input !== undefined) usage.input_tokens = input;
  if (output !== undefined) usage.output_tokens = output;
  if (cacheWrite !== undefined) usage.cache_creation = { input_tokens: cacheWrite };
  if (cacheRead !== undefined) usage.cache_read = { input_tokens: cacheRead };
  return Object.keys(usage).length > 0 ? usage : undefined;
}

function toolResultText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (!Array.isArray(value)) return '';
  return value.flatMap((item) => isRecord(item) && typeof (item.text ?? item.content) === 'string' ? [String(item.text ?? item.content)] : []).join('\n');
}

function cleanUserText(text: string): string {
  const value = text.trim();
  return value.includes('<local-command-caveat>') || value.includes('<command-name>') ? '' : value;
}

function digestValue(value: unknown): { hash: string; bytes: number } | undefined {
  if (value === undefined || value === null) return undefined;
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(value); }
  catch { return undefined; }
  if (typeof text !== 'string') return undefined;
  return { hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}

function parseTimestamp(value: unknown): string {
  if (typeof value !== 'string') return new Date().toISOString();
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

async function processFiles(
  files: ClaudeFile[],
  root: string,
  state: CursorState,
  options: ClaudeHistoryOptions,
  result: ClaudeHistoryResult,
  persist: boolean,
  statePath?: string,
): Promise<void> {
  let totalBytes = 0;
  for (const file of files) {
    const key = sha256(file.key);
    const previous = state.files[key];
    try {
      const bytes = await readSessionFile(file, root);
      totalBytes += bytes.byteLength;
      if (totalBytes > maxTotalBytes) {
        result.files_truncated = true;
        result.errors.push({ source_sha256: key, message: 'Claude sync exceeded the total source-byte limit' });
        break;
      }
      const parsed = parseAndMap(bytes, file, previous, options.includeContent ?? false);
      result.malformed_lines += parsed.malformed;
      if (parsed.malformed > 0) result.errors.push({ source_sha256: key, message: 'Claude transcript contains a malformed row; scanning stops before it' });
      if (parsed.partialTail) result.partial_sessions++;

      const cursorLine = previous?.line ?? 0;
      if (previous && (parsed.completeLines < cursorLine || parsed.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
        result.errors.push({ source_sha256: key, message: 'Claude transcript prefix changed; refusing automatic replay' });
        continue;
      }
      const fileEvents = parsed.groups.reduce((count, group) => count + group.events.length, 0);
      if (fileEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ source_sha256: key, message: 'Claude sync exceeded the event processing limit' });
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
        if (!prefix) throw new Error('Claude session checkpoint prefix is unavailable');
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
        result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Claude history events' });
      }
      if (failed) continue;

      const completePrefix = parsed.completePrefixHash;
      if (!completePrefix) {
        result.errors.push({ source_sha256: key, message: 'Claude session checkpoint prefix is unavailable' });
        continue;
      }
      if (parsed.completeLines > cursorLine || (previous !== undefined && previous.session_started !== parsed.started)) {
        state.files[key] = makeCursor(parsed.completeLines, file, completePrefix, parsed.started);
        result.sessions_changed++;
        if (persist && statePath) await writeState(statePath, state);
      }
    } catch {
      result.errors.push({ source_sha256: key, message: 'Claude session could not be read or checkpointed safely' });
    }
  }
}

function makeCursor(line: number, file: ClaudeFile, prefix: string, started: boolean): SessionCursor {
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
  const parent = path.dirname(statePath);
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Claude history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Claude history cursor');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(`${statePath}.lock`).catch(() => {});
  }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.claude-session-cursor.v1', version: stateVersion, files: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Claude history cursor');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('Claude history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (opened.dev !== info.dev || opened.ino !== info.ino) throw new Error('Claude cursor changed before read');
    const bytes = await handle.readFile();
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
      throw new Error('Claude history cursor has an unsupported schema; inspect it before retrying');
    }
    const files: Record<string, SessionCursor> = {};
    for (const [key, value] of Object.entries(parsed.files)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
          !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
          typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean') {
        throw new Error('Claude history cursor contains an invalid checkpoint');
      }
      files[key] = {
        line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
        prefix_sha256: value.prefix_sha256, session_started: value.session_started,
      };
    }
    return { ...empty, files };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Claude history cursor')) throw error;
    throw new Error('Claude history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

async function verifyStateParent(statePath: string, create: boolean): Promise<void> {
  const parent = path.dirname(path.resolve(statePath));
  const parsed = path.parse(parent);
  const segments = parent.slice(parsed.root.length).split(path.sep).filter(Boolean);
  let current = parsed.root;
  for (const segment of segments) {
    current = path.join(current, segment);
    let info = await lstat(current).catch((error: unknown) => {
      if (isCode(error, 'ENOENT')) return null;
      throw new Error('unable to inspect Claude cursor directory');
    });
    if (!info && create) {
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => {
        if (!isCode(error, 'EEXIST')) throw new Error('unable to create private Claude cursor directory');
      });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Claude cursor path cannot contain symbolic links or non-directory parents');
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const payload = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (payload.byteLength > maxStateBytes) throw new Error('Claude history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Claude history cursor');
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`${label} must be an integer between ${minimum} and ${maximum}`);
  return value;
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}

function boundedString(value: unknown, max: number): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function sha256(value: string | Buffer): string {
  return createHash('sha256').update(value).digest('hex');
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
