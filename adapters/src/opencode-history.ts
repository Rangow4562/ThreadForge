import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isWithinRealPaths } from './path-safety.ts';
import { TextDecoder } from 'node:util';
import { emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const stateFileName = 'opencode-history.v1.json';
const maxStateBytes = 8 << 20;
const maxCliOutputBytes = 16 << 20;
const maxSyncInputBytes = 128 << 20;
const maxSessions = 200;
const maxMessagesPerSession = 20_000;
const maxPartsPerSession = 50_000;
const maxEventsPerSync = 20_000;
const batchSize = 100;
const maxPreviewEvents = 500;
const maxRetainedContentBytes = 4 << 10;
const cliTimeoutMs = 45_000;

type JsonObject = Record<string, unknown>;
export type OpenCodeHistoryCommand = { executable: string; args: string[]; cwd: string };
export type OpenCodeHistoryCommandRunner = (command: OpenCodeHistoryCommand) => Promise<string>;

export type OpenCodeHistoryOptions = {
  projectRoot: string;
  opencodeCli?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  preview?: boolean;
  includeContent?: boolean;
  commandRunner?: OpenCodeHistoryCommandRunner;
};

export type OpenCodeHistoryResult = {
  schema_version: 'threadforge.opencode-history-sync.v1';
  project_sha256: string;
  sessions: number;
  sessions_changed: number;
  events_emitted: number;
  events_previewed: number;
  sessions_truncated: boolean;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ session_sha256?: string; message: string }>;
  events?: ThreadforgeEvent[];
};

type SessionCursor = { events: Record<string, string> };
type CursorState = {
  schema_version: 'threadforge.opencode-history-cursor.v1';
  version: number;
  sessions: Record<string, SessionCursor>;
};
type Candidate = { key: string; fingerprint: string; event: ThreadforgeEvent };
type MappedExport = { events: Candidate[]; liveKeys: Set<string> };

export function buildOpenCodeSessionListCommand(
  projectRoot: string,
  options: Pick<OpenCodeHistoryOptions, 'opencodeCli' | 'maxSessions'> = {},
): OpenCodeHistoryCommand {
  const root = absolutePath(projectRoot, '--project-root');
  const executable = validateExecutable(options.opencodeCli);
  const limit = boundedInteger(options.maxSessions ?? 50, 1, maxSessions, '--max-sessions');
  return { executable, cwd: root, args: ['session', 'list', '--format', 'json', '--max-count', String(limit + 1), '--pure'] };
}

export function buildOpenCodeExportCommand(
  projectRoot: string,
  sessionId: string,
  options: Pick<OpenCodeHistoryOptions, 'opencodeCli'> = {},
): OpenCodeHistoryCommand {
  const root = absolutePath(projectRoot, '--project-root');
  const executable = validateExecutable(options.opencodeCli);
  if (!validSessionId(sessionId)) throw new Error('OpenCode session id is invalid');
  return { executable, cwd: root, args: ['export', sessionId, '--sanitize', '--pure'] };
}

export async function getOpenCodeHistoryStatus(options: Pick<OpenCodeHistoryOptions, 'statePath'> = {}) {
  const statePath = resolveStatePath(options.statePath);
  const state = await readState(statePath);
  return {
    schema_version: 'threadforge.opencode-history-status.v1' as const,
    tracked_sessions: Object.keys(state.sessions).length,
    cursor_file_present: await lstat(statePath).then(() => true, (error: unknown) => {
      if (isCode(error, 'ENOENT')) return false;
      throw new Error('unable to inspect OpenCode history state');
    }),
  };
}

export async function syncOpenCodeHistory(options: OpenCodeHistoryOptions): Promise<OpenCodeHistoryResult> {
  validateOptions(options);
  const root = await resolveProjectRoot(options.projectRoot);
  const statePath = resolveStatePath(options.statePath);
  if (await isWithinRealPaths(root, statePath) !== false) throw new Error('OpenCode history cursor must be stored outside the selected project root');
  const projectHash = sha256(root);
  const result = emptyResult(projectHash, options.includeContent ?? false);
  const runner = options.commandRunner ?? runCommand;
  const maxSessionCount = options.maxSessions ?? 50;

  const operation = async (state: CursorState, persist: boolean) => {
    let listingText: string;
    let listed: JsonObject[];
    try {
      listingText = await runner(buildOpenCodeSessionListCommand(root, { opencodeCli: options.opencodeCli, maxSessions: maxSessionCount }));
      listed = parseSessionList(listingText, maxSessionCount + 1);
    } catch {
      result.errors.push({ message: 'OpenCode session listing could not be read or validated' });
      return;
    }
    let totalSourceBytes = Buffer.byteLength(listingText, 'utf8');
    result.sessions_truncated = listed.length > maxSessionCount;
    const sessions = listed.slice(0, maxSessionCount);
    result.sessions = sessions.length;
    let mappedEvents = 0;

    for (const row of sessions) {
      const sessionId = boundedString(row.id, 256);
      if (!sessionId) {
        result.errors.push({ message: 'OpenCode returned a session without a valid identifier' });
        continue;
      }
      const sessionHash = sha256(sessionId);
      let mapped: MappedExport;
      try {
        const output = await runner(buildOpenCodeExportCommand(root, sessionId, { opencodeCli: options.opencodeCli }));
        totalSourceBytes += Buffer.byteLength(output, 'utf8');
        if (totalSourceBytes > maxSyncInputBytes) throw new Error('OpenCode history sync exceeded its total source-byte budget');
        mapped = mapExport(parseExport(output, sessionId), sessionHash, options.includeContent ?? false);
      } catch {
        result.errors.push({ session_sha256: sessionHash, message: 'OpenCode session export could not be read or validated' });
        continue;
      }
      mappedEvents += mapped.events.length;
      if (mappedEvents > maxEventsPerSync) {
        result.errors.push({ session_sha256: sessionHash, message: 'OpenCode history sync exceeded the event processing limit' });
        break;
      }

      const previous = state.sessions[sessionHash]?.events ?? {};
      const changed = mapped.events.filter((candidate) => previous[candidate.key] !== candidate.fingerprint);
      if (changed.length + result.events_emitted + result.events_previewed > maxEventsPerSync) {
        result.errors.push({ session_sha256: sessionHash, message: 'OpenCode history sync exceeded the event processing limit' });
        continue;
      }

      if (options.preview) {
        result.events_previewed += changed.length;
        result.events ??= [];
        for (const candidate of changed) {
          if (result.events.length < maxPreviewEvents) result.events.push(candidate.event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let failed = false;
      for (let offset = 0; offset < changed.length; offset += batchSize) {
        const batch = changed.slice(offset, offset + batchSize);
        try {
          await emitEvents(batch.map((candidate) => candidate.event), { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        } catch {
          result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected OpenCode history events' });
          failed = true;
          break;
        }
        const cursorEvents = state.sessions[sessionHash]?.events ?? {};
        for (const candidate of batch) cursorEvents[candidate.key] = candidate.fingerprint;
        state.sessions[sessionHash] = { events: cursorEvents };
        result.events_emitted += batch.length;
        if (persist) await writeState(statePath, state);
      }
      if (failed) continue;

      const nextEvents: Record<string, string> = {};
      for (const candidate of mapped.events) nextEvents[candidate.key] = candidate.fingerprint;
      const before = JSON.stringify(previous);
      const after = JSON.stringify(nextEvents);
      if (before !== after) {
        state.sessions[sessionHash] = { events: nextEvents };
        result.sessions_changed++;
        if (persist) await writeState(statePath, state);
      }
    }
    if (persist) await writeState(statePath, state);
  };

  if (options.preview) await operation(await readState(statePath), false);
  else await withStateLock(statePath, async () => operation(await readState(statePath), true));
  result.complete = result.errors.length === 0 && !result.sessions_truncated;
  return result;
}

function emptyResult(projectHash: string, includeContent: boolean): OpenCodeHistoryResult {
  return {
    schema_version: 'threadforge.opencode-history-sync.v1', project_sha256: projectHash,
    sessions: 0, sessions_changed: 0, events_emitted: 0, events_previewed: 0, sessions_truncated: false,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false, preview_truncated: false, errors: [],
  };
}

function validateOptions(options: OpenCodeHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? 50, 1, maxSessions, '--max-sessions');
  absolutePath(options.projectRoot, '--project-root');
  validateExecutable(options.opencodeCli);
}

function validateExecutable(value?: string): string {
  const executable = value?.trim() || 'opencode';
  if (/[\0\r\n]/.test(executable)) throw new Error('OpenCode CLI path contains invalid characters');
  return executable;
}

function boundedInteger(value: number, min: number, max: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw new Error(`${label} must be an integer between ${min} and ${max}`);
  return value;
}

function absolutePath(value: string, label: string): string {
  if (!path.isAbsolute(value)) throw new Error(`${label} must be an absolute path`);
  return path.resolve(value);
}

async function resolveProjectRoot(value: string): Promise<string> {
  const root = absolutePath(value, '--project-root');
  const info = await lstat(root).catch(() => null);
  if (!info || !info.isDirectory()) throw new Error('--project-root must be an existing directory');
  return realpath(root).catch(() => { throw new Error('--project-root could not be resolved'); });
}

function parseSessionList(text: string, maxRows: number): JsonObject[] {
  if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes) throw new Error('OpenCode session listing exceeded the local output limit');
  if (!text.trim()) return [];
  const parsed = parseJson(text, 'OpenCode session listing');
  if (!Array.isArray(parsed) || parsed.length > maxRows) throw new Error('OpenCode session listing has an unsupported shape');
  return parsed.map((row) => {
    if (!isRecord(row) || !validSessionId(row.id)) throw new Error('OpenCode session listing contains an invalid row');
    return row;
  });
}

function parseExport(text: string, expectedSessionId: string): { info: JsonObject; messages: JsonObject[] } {
  if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes) throw new Error('OpenCode export exceeded the local size limit');
  const parsed = parseJson(text, 'OpenCode session export');
  if (!isRecord(parsed) || !isRecord(parsed.info) || parsed.info.id !== expectedSessionId || !Array.isArray(parsed.messages)) {
    throw new Error('OpenCode session export has an unsupported shape');
  }
  if (parsed.messages.length > maxMessagesPerSession) throw new Error('OpenCode session export exceeded the message limit');
  let parts = 0;
  const messages = parsed.messages.map((message) => {
    if (!isRecord(message) || !isRecord(message.info) || !Array.isArray(message.parts)) throw new Error('OpenCode export contains an invalid message');
    parts += message.parts.length;
    if (parts > maxPartsPerSession || !boundedString(message.info.id, 256)) throw new Error('OpenCode export exceeded the part limit or omitted a message id');
    for (const part of message.parts) if (!isRecord(part)) throw new Error('OpenCode export contains an invalid part');
    return message;
  });
  return { info: parsed.info, messages };
}

function mapExport(
  source: { info: JsonObject; messages: JsonObject[] },
  sessionHash: string,
  includeContent: boolean,
): MappedExport {
  const result: MappedExport = { events: [], liveKeys: new Set() };
  const add = (keyMaterial: string, fingerprintMaterial: string, event: ThreadforgeEvent) => {
    const key = sha256(keyMaterial);
    const fingerprint = sha256(fingerprintMaterial);
    if (result.liveKeys.has(key)) throw new Error('OpenCode export contains a duplicate event identity');
    result.liveKeys.add(key);
    event.event.id = sha256(`${sessionHash}:${key}:${fingerprint}`);
    result.events.push({ key, fingerprint, event });
  };
  for (const message of source.messages) {
    const info = message.info as JsonObject;
    const messageId = boundedString(info.id, 256);
    const role = boundedString(info.role, 32)?.toLowerCase();
    if (!messageId || (role !== 'user' && role !== 'assistant') || (role === 'assistant' && info.summary === true)) continue;
    const model = modelName(info);
    const time = isRecord(info.time) ? info.time : {};
    const createdAt = toTimestamp(time.created ?? info.timestamp);
    for (const item of message.parts as JsonObject[]) {
      const partId = boundedString(item.id, 256);
      const type = boundedString(item.type, 64);
      if (!type || !['text', 'reasoning', 'tool-invocation', 'tool', 'compaction'].includes(type)) continue;
      if (!partId) throw new Error('OpenCode export contains a supported part without a stable identifier');
      if (type === 'text' || type === 'reasoning') {
        if (type === 'text' && (item.ignored === true || item.synthetic === true)) continue;
        if (typeof item.text === 'string' && item.text.length > (4 << 20)) throw new Error('OpenCode text part exceeded the local processing limit');
        const text = boundedString(item.text, 4 << 20);
        if (!text) continue;
        const digest = sha256(text);
        const bytes = Buffer.byteLength(text, 'utf8');
        const included = type === 'text' && includeContent && bytes <= maxRetainedContentBytes;
        const event = createEvent({
          sessionHash, timestamp: partTimestamp(item, createdAt), model,
          action: type === 'reasoning' ? 'agent.reasoning' : role === 'user' ? 'prompt.submitted' : 'agent.message',
          category: type === 'reasoning' ? 'agent' : role === 'user' ? 'prompt' : 'agent',
          message: type === 'reasoning' ? 'OpenCode reasoning part observed' : role === 'user' ? 'OpenCode user message recovered from session export' : 'OpenCode assistant message recovered from session export',
          identityHash: sha256(`${messageId}:${partId}`),
          content: { retention: included ? 'full' : 'metadata', included, hash: digest, bytes, ...(includeContent && type === 'text' && bytes > maxRetainedContentBytes ? { truncated: true } : {}) },
          ...(included ? { text } : {}),
        });
        add(`${messageId}:${partId}:${type}`, `${type}:${role}:${digest}:${bytes}`, event);
        continue;
      }
      if (type === 'compaction') {
        const event = createEvent({
          sessionHash, timestamp: partTimestamp(item, createdAt), model, action: 'session.compacting', category: 'session',
          message: 'OpenCode compaction recovered from session export', identityHash: sha256(`${messageId}:${partId}`),
        });
        add(`${messageId}:${partId}:compaction`, 'compaction', event);
        continue;
      }
      const tool = isRecord(item.toolInvocation) ? item.toolInvocation : item;
      const toolState = isRecord(tool.state) ? tool.state : {};
      const toolName = safeLabel(tool.toolName ?? tool.tool ?? item.toolName, 128);
      const callId = boundedString(tool.toolCallId ?? tool.callID ?? item.callID, 512) ?? partId;
      const stage = boundedString(toolState.type ?? toolState.status, 64)?.toLowerCase();
      const input = toolState.input ?? toolState.args ?? toolState.partialCall;
      const output = toolState.output ?? toolState.result;
      const finished = stage === 'result' || stage === 'completed' || stage === 'error' || stage === 'failed';
      const supportedStage = finished || stage === 'pending' || stage === 'running' || stage === 'call' || stage === 'partial-call' || stage === 'invoked';
      if (!supportedStage) throw new Error('OpenCode export contains a tool state this adapter does not yet understand');
      const failed = toolState.isError === true || stage === 'error' || stage === 'failed';
      const commandLike = Boolean(toolName && /^(bash|shell|terminal|powershell|exec)$/i.test(toolName));
      const action = finished
        ? failed ? 'tool.failed' : commandLike ? 'command.executed' : 'tool.completed'
        : 'tool.invoked';
      const category = finished && commandLike ? 'command' : 'tool';
      const inputDigest = digestValue(input);
      const outputDigest = digestValue(output);
      const content = {
        retention: 'metadata', included: false,
        ...(inputDigest ? { input_hash: inputDigest.hash, input_bytes: inputDigest.bytes } : {}),
        ...(outputDigest ? { output_hash: outputDigest.hash, output_bytes: outputDigest.bytes } : {}),
      };
      const event = createEvent({
        sessionHash, timestamp: toolTimestamp(toolState, partTimestamp(item, createdAt)), model,
        action, category, message: `OpenCode ${finished ? (failed ? 'tool failure' : 'tool result') : 'tool invocation'} recovered from session export`,
        identityHash: sha256(`${messageId}:${partId}`), severity: failed ? 'medium' : 'info',
        toolName: toolName ?? undefined,
        toolCallHash: sha256(callId),
        content,
      });
      const phase = finished ? 'result' : 'invoke';
      const fingerprint = `${phase}:${stage}:${failed}:${inputDigest?.hash ?? ''}:${outputDigest?.hash ?? ''}`;
      add(`${messageId}:${partId}:tool:${phase}`, fingerprint, event);
    }

    if (role === 'assistant') {
      const usage = normalizedUsage(info.tokens, info.cost);
      if (usage) {
        const event = createEvent({
          sessionHash, timestamp: toTimestamp(time.completed ?? time.created), model, action: 'token.usage', category: 'metric',
          message: 'OpenCode token usage recovered from session export', identityHash: sha256(`${messageId}:usage`),
          genAi: { usage },
        });
        add(`${messageId}:usage`, JSON.stringify(usage), event);
      }
      if (isRecord(info.error)) {
        const errorType = safeLabel(info.error.name ?? info.error.type ?? info.error.code, 96);
        const event = createEvent({
          sessionHash, timestamp: toTimestamp(time.completed ?? time.created), model, action: 'session.error', category: 'session',
          message: 'OpenCode session error recovered from export', identityHash: sha256(`${messageId}:error`), severity: 'medium',
          ...(errorType ? { raw: { error_type: errorType } } : {}),
        });
        add(`${messageId}:error`, `error:${errorType ?? 'unknown'}`, event);
      }
    }
  }
  if (result.events.length > maxEventsPerSync) throw new Error('OpenCode session exceeded the event processing limit');
  return result;
}

function createEvent(input: {
  sessionHash: string; timestamp: string; model?: string; action: string; category: string; message: string;
  identityHash: string; severity?: ThreadforgeEvent['severity']; toolName?: string; toolCallHash?: string;
  content?: JsonObject; text?: string; genAi?: JsonObject; raw?: JsonObject;
}): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: input.timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { kind: 'agent_runtime', action: input.action, category: input.category, fidelity: 'observed' },
    severity: input.severity ?? 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'opencode', collection_method: 'poll' },
    session: { id: `opencode:${input.sessionHash}` },
    raw: { source: 'opencode_session_export', session_sha256: input.sessionHash, record_sha256: input.identityHash, message: input.message },
  };
  if (input.model) event.model = input.model;
  if (input.toolName) event.tool = { name: input.toolName };
  if (input.toolCallHash) event.raw = { ...event.raw, tool_call_sha256: input.toolCallHash };
  if (input.content && Object.keys(input.content).length > 0) event.content = input.content;
  if (input.text !== undefined) {
    if (input.action === 'prompt.submitted') {
      event.prompt = { text: input.text };
      event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: input.text }] }] } };
    } else {
      event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: input.text }] }] } };
    }
  }
  if (input.genAi) event.gen_ai = input.genAi;
  if (input.raw) event.raw = { ...event.raw, ...input.raw };
  return event;
}

function normalizedUsage(value: unknown, costValue: unknown): JsonObject | undefined {
  if (!isRecord(value)) return undefined;
  const cache = isRecord(value.cache) ? value.cache : {};
  const numbers: JsonObject = {};
  const input = nonNegativeInteger(value.input);
  const output = nonNegativeInteger(value.output);
  const reasoning = nonNegativeInteger(value.reasoning);
  const cacheRead = nonNegativeInteger(cache.read);
  const cacheWrite = nonNegativeInteger(cache.write);
  if (input !== undefined) numbers.input_tokens = input;
  if (output !== undefined) numbers.output_tokens = output;
  if (reasoning !== undefined) numbers.reasoning = { output_tokens: reasoning };
  if (cacheRead !== undefined) numbers.cache_read = { input_tokens: cacheRead };
  if (cacheWrite !== undefined) numbers.cache_creation = { input_tokens: cacheWrite };
  const cost = typeof costValue === 'number' && Number.isFinite(costValue) && costValue >= 0 ? costValue : undefined;
  if (cost !== undefined) numbers.cost_usd = cost;
  return Object.keys(numbers).length > 0 ? numbers : undefined;
}

function digestValue(value: unknown): { hash: string; bytes: number } | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(value); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  return { hash: sha256(serialized), bytes: Buffer.byteLength(serialized, 'utf8') };
}

function modelName(info: JsonObject): string | undefined {
  const provider = safeLabel(info.providerID ?? (isRecord(info.model) ? info.model.providerID : undefined), 64);
  const model = safeLabel(info.modelID ?? (isRecord(info.model) ? info.model.id : undefined), 96);
  return provider && model ? `${provider}/${model}` : undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function partTimestamp(part: JsonObject, fallback: string): string {
  const time = isRecord(part.time) ? part.time : {};
  return toTimestamp(time.created ?? time.start ?? time.end ?? fallback);
}

function toolTimestamp(state: JsonObject, fallback: string): string {
  const time = isRecord(state.time) ? state.time : {};
  return toTimestamp(time.end ?? time.start ?? fallback);
}

function toTimestamp(value: unknown): string {
  const date = typeof value === 'number' ? new Date(value) : typeof value === 'string' ? new Date(value) : new Date();
  return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}

function nonNegativeInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function parseJson(text: string, label: string): unknown {
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(text, 'utf8'))) as unknown; }
  catch { throw new Error(`${label} returned invalid JSON`); }
}

function resolveStatePath(value?: string): string {
  if (value !== undefined) return absolutePath(value, '--state-path');
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
  const parent = path.dirname(statePath);
  await verifyStateParent(statePath, true);
  const info = await lstat(parent).catch(() => null);
  if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error('OpenCode cursor parent must be a real directory');
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenCode history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock OpenCode history cursor');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(`${statePath}.lock`).catch(() => {});
  }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.opencode-history-cursor.v1', version: stateVersion, sessions: {} };
  await verifyStateParent(statePath, false);
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect OpenCode history cursor');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('OpenCode history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (opened.dev !== info.dev || opened.ino !== info.ino) throw new Error('OpenCode cursor changed before read');
    const bytes = await handle.readFile();
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
      throw new Error('OpenCode history cursor has an unsupported schema; inspect it before retrying');
    }
    const sessions: Record<string, SessionCursor> = {};
    for (const [sessionHash, value] of Object.entries(parsed.sessions)) {
      if (!/^[0-9a-f]{64}$/.test(sessionHash) || !isRecord(value) || !isRecord(value.events)) throw new Error('OpenCode cursor has an invalid session entry');
      const events: Record<string, string> = {};
      for (const [key, fingerprint] of Object.entries(value.events)) {
        if (!/^[0-9a-f]{64}$/.test(key) || typeof fingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(fingerprint)) {
          throw new Error('OpenCode cursor contains an invalid event checkpoint');
        }
        events[key] = fingerprint;
      }
      sessions[sessionHash] = { events };
    }
    return { ...empty, sessions };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('OpenCode history cursor')) throw error;
    throw new Error('OpenCode history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

async function verifyStateParent(statePath: string, create: boolean): Promise<void> {
  const absoluteParent = path.dirname(path.resolve(statePath));
  const parsed = path.parse(absoluteParent);
  const segments = absoluteParent.slice(parsed.root.length).split(path.sep).filter(Boolean);
  let current = parsed.root;
  for (const segment of segments) {
    current = path.join(current, segment);
    let info = await lstat(current).catch((error: unknown) => {
      if (isCode(error, 'ENOENT')) return null;
      throw new Error('unable to inspect OpenCode cursor directory');
    });
    if (!info && create) {
      await mkdir(current, { mode: 0o700 }).catch((error: unknown) => {
        if (!isCode(error, 'EEXIST')) throw new Error('unable to create private OpenCode cursor directory');
      });
      info = await lstat(current).catch(() => null);
    }
    if (!info) return;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('OpenCode cursor path cannot contain symbolic links or non-directory parents');
  }
}

function isWithin(root: string, value: string): boolean {
  const relative = path.relative(root, value);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const data = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (data.byteLength > maxStateBytes) throw new Error('OpenCode history cursor exceeds the local size limit');
  const tempPath = `${statePath}.${randomBytes(12).toString('hex')}.tmp`;
  let handle;
  try {
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    await handle.chmod(0o600);
    await handle.writeFile(data);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, statePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically write OpenCode history cursor');
  }
}

function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function boundedString(value: unknown, max: number): string | undefined { return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined; }
function isBoundedString(value: unknown, max: number): value is string { return boundedString(value, max) !== undefined; }
function validSessionId(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 256 && /^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(value);
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

async function runCommand(command: OpenCodeHistoryCommand): Promise<string> {
  if (process.platform === 'win32') return runPowerShell(command);
  return spawnCaptured(command.executable, command.args, command.cwd);
}

async function runPowerShell(command: OpenCodeHistoryCommand): Promise<string> {
  const quote = (value: string) => {
    if (/[\0\r\n]/.test(value)) throw new Error('OpenCode CLI argument contains invalid characters');
    return `'${value.replace(/'/g, "''")}'`;
  };
  const invocation = [command.executable, ...command.args].map(quote).join(' ');
  const script = `$ErrorActionPreference = 'Stop'\nSet-Location -LiteralPath ${quote(command.cwd)}\n& ${invocation}\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit`;
  return spawnCaptured('powershell.exe', ['-NoProfile', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')], command.cwd);
}

async function spawnCaptured(executable: string, args: string[], cwd: string): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const child = spawn(executable, args, { cwd, stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true });
    const chunks: Buffer[] = [];
    let size = 0;
    let settled = false;
    const timeout = setTimeout(() => {
      child.kill();
      finish(new Error('OpenCode CLI timed out'));
    }, cliTimeoutMs);
    const finish = (error?: Error, output?: string) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      if (error) reject(error);
      else resolve(output ?? '');
    };
    child.stdout.on('data', (chunk: Buffer) => {
      size += chunk.byteLength;
      if (size > maxCliOutputBytes) {
        child.kill();
        finish(new Error('OpenCode CLI output exceeded the local limit'));
      } else chunks.push(Buffer.from(chunk));
    });
    child.once('error', () => finish(new Error('OpenCode CLI could not be started')));
    child.once('close', (code) => {
      if (code !== 0) finish(new Error(`OpenCode CLI exited with status ${code}`));
      else finish(undefined, Buffer.concat(chunks).toString('utf8'));
    });
  });
}
