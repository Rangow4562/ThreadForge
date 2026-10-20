import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const stateFileName = 'openclaw-history.v1.json';
const maxStateBytes = 4 << 20;
const maxCliOutputBytes = 16 << 20;
const maxHistoryResponseBytes = 32 << 20;
const maxSessions = 200;
const gatewayPageSize = 200;
const maxHistoryMessages = 2_000;
const maxEvents = 20_000;
const batchSize = 100;
const maxPreviewEvents = 500;
const maxContentBytes = 4 << 10;
const maxDigestBytes = 1 << 20;
const cliTimeoutMs = 30_000;

type JsonObject = Record<string, unknown>;

export type OpenClawHistoryCommand = { executable: string; args: string[] };
export type OpenClawHistoryCommandRunner = (command: OpenClawHistoryCommand) => Promise<string>;

export type OpenClawHistoryOptions = {
  openclawCli?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  maxMessages?: number;
  preview?: boolean;
  includeContent?: boolean;
  commandRunner?: OpenClawHistoryCommandRunner;
};

export type OpenClawHistoryResult = {
  schema_version: 'threadforge.openclaw-history-sync.v1';
  sessions: number;
  sessions_changed: number;
  messages_seen: number;
  events_emitted: number;
  events_previewed: number;
  sessions_truncated: boolean;
  history_truncated_sessions: number;
  cursor_conflicts: number;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ session_sha256?: string; message: string }>;
  events?: ThreadforgeEvent[];
};

type SessionCursor = { latest_message_sha256: string };
type CursorState = {
  schema_version: 'threadforge.openclaw-history-cursor.v1';
  version: number;
  sessions: Record<string, SessionCursor>;
};
type NormalizedMessage = {
  identity: string;
  timestamp: string;
  role: 'user' | 'assistant';
  text: string;
};

export function buildOpenClawSessionListCommand(options: Pick<OpenClawHistoryOptions, 'openclawCli' | 'maxSessions'> = {}): OpenClawHistoryCommand {
  const executable = validateExecutable(options.openclawCli);
  const limit = boundedInteger(options.maxSessions ?? 50, 1, maxSessions, '--max-sessions');
  return { executable, args: ['sessions', '--all-agents', '--limit', String(limit), '--json'] };
}

export function buildOpenClawHistoryCommand(
  sessionKey: string,
  options: Pick<OpenClawHistoryOptions, 'openclawCli' | 'maxMessages'> & { agentId?: string; offset?: number } = {},
): OpenClawHistoryCommand {
  const executable = validateExecutable(options.openclawCli);
  if (!isBoundedString(sessionKey, 2048)) throw new Error('OpenClaw session key is invalid');
  const limit = boundedInteger(options.maxMessages ?? gatewayPageSize, 1, gatewayPageSize, '--max-messages');
  if (options.offset !== undefined) boundedInteger(options.offset, 0, Number.MAX_SAFE_INTEGER, 'history offset');
  const agentId = options.agentId === undefined ? undefined : boundedString(options.agentId, 128);
  if (options.agentId !== undefined && !agentId) throw new Error('OpenClaw agent id is invalid');
  return {
    executable,
    args: ['gateway', 'call', 'chat.history', '--params', JSON.stringify({
      sessionKey, limit, ...(options.offset === undefined ? {} : { offset: options.offset }), ...(agentId ? { agentId } : {}),
    }), '--json'],
  };
}

export async function getOpenClawHistoryStatus(options: Pick<OpenClawHistoryOptions, 'statePath'> = {}) {
  const statePath = resolveStatePath(options.statePath);
  const state = await readState(statePath);
  return {
    schema_version: 'threadforge.openclaw-history-status.v1' as const,
    tracked_sessions: Object.keys(state.sessions).length,
    cursor_file_present: await lstat(statePath).then(() => true, (error: unknown) => {
      if (isCode(error, 'ENOENT')) return false;
      throw new Error('unable to inspect OpenClaw history state');
    }),
  };
}

export async function syncOpenClawHistory(options: OpenClawHistoryOptions = {}): Promise<OpenClawHistoryResult> {
  validateOptions(options);
  const result = emptyResult(options.includeContent ?? false);
  const runner = options.commandRunner ?? runCommand;
  const statePath = resolveStatePath(options.statePath);
  const maxSessionCount = options.maxSessions ?? 50;
  const maxMessageCount = options.maxMessages ?? maxHistoryMessages;
  const operation = async (state: CursorState, persist: boolean) => {
    const listingText = await runner(buildOpenClawSessionListCommand({ openclawCli: options.openclawCli, maxSessions: maxSessionCount }));
    const listing = parseSessionListing(listingText);
    result.sessions = listing.sessions.length;
    result.sessions_truncated = listing.hasMore !== false;

    for (const row of listing.sessions) {
      const key = boundedString(row.key, 2048);
      if (!key) {
        result.errors.push({ message: 'OpenClaw returned a session row without a valid key' });
        continue;
      }
      const sessionHash = sha256(key);
      const previous = state.sessions[sessionHash];
      let history: { messages: NormalizedMessage[]; hasMore: boolean };
      try {
        history = await readBoundedHistory(runner, key, boundedString(row.agentId, 128), options.openclawCli, maxMessageCount);
      } catch {
        result.errors.push({ session_sha256: sessionHash, message: 'OpenClaw session history could not be read or validated' });
        continue;
      }

      result.messages_seen += history.messages.length;
      if (history.hasMore) result.history_truncated_sessions++;
      if (history.messages.length === 0) {
        if (previous) {
          result.cursor_conflicts++;
          result.errors.push({ session_sha256: sessionHash, message: 'history cursor is no longer present; review session reset or retention changes' });
        }
        continue;
      }

      const anchorIndex = previous
        ? history.messages.findIndex((message) => message.identity === previous.latest_message_sha256)
        : -1;
      if (previous && anchorIndex < 0) {
        result.cursor_conflicts++;
        result.errors.push({ session_sha256: sessionHash, message: 'history cursor is no longer present; refusing to skip or replay messages' });
        continue;
      }

      const startIndex = previous ? anchorIndex + 1 : 0;
      const fresh = history.messages.slice(startIndex);
      const mapped = fresh.flatMap((message) => {
        const event = mapMessage(message, sessionHash, options.includeContent ?? false);
        return event ? [{ identity: message.identity, event }] : [];
      });
      if (mapped.length + result.events_emitted + result.events_previewed > maxEvents) {
        result.errors.push({ session_sha256: sessionHash, message: 'OpenClaw history sync exceeded the event processing limit' });
        continue;
      }

      if (options.preview) {
        result.events_previewed += mapped.length;
        result.events ??= [];
        for (const item of mapped) {
          if (result.events.length < maxPreviewEvents) result.events.push(item.event);
          else result.preview_truncated = true;
        }
        continue;
      }

      let acknowledgedIdentity = previous?.latest_message_sha256;
      for (let offset = 0; offset < mapped.length; offset += batchSize) {
        const batch = mapped.slice(offset, offset + batchSize);
        try {
          await emitEvents(batch.map((item) => item.event), { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
        } catch {
          result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected OpenClaw history events' });
          break;
        }
        acknowledgedIdentity = batch[batch.length - 1]!.identity;
        state.sessions[sessionHash] = { latest_message_sha256: acknowledgedIdentity };
        result.events_emitted += batch.length;
        if (persist) await writeState(statePath, state);
      }
      if (result.errors.some((error) => error.session_sha256 === sessionHash && error.message.startsWith('Threadforge'))) continue;

      const newestIdentity = history.messages[history.messages.length - 1]!.identity;
      if (acknowledgedIdentity !== newestIdentity || !previous) {
        state.sessions[sessionHash] = { latest_message_sha256: newestIdentity };
        if (persist) await writeState(statePath, state);
      }
      if (newestIdentity !== previous?.latest_message_sha256) result.sessions_changed++;
    }

    if (persist) await writeState(statePath, state);
  };

  if (options.preview) {
    await operation(await readState(statePath), false);
  } else {
    await withStateLock(statePath, async () => operation(await readState(statePath), true));
  }
  result.complete = result.errors.length === 0 && !result.sessions_truncated && result.history_truncated_sessions === 0;
  return result;
}

function emptyResult(includeContent: boolean): OpenClawHistoryResult {
  return {
    schema_version: 'threadforge.openclaw-history-sync.v1', sessions: 0, sessions_changed: 0,
    messages_seen: 0, events_emitted: 0, events_previewed: 0, sessions_truncated: false,
    history_truncated_sessions: 0, cursor_conflicts: 0,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: OpenClawHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? 50, 1, maxSessions, '--max-sessions');
  boundedInteger(options.maxMessages ?? maxHistoryMessages, 1, maxHistoryMessages, '--max-messages');
  validateExecutable(options.openclawCli);
}

function validateExecutable(value?: string): string {
  const executable = value?.trim() || 'openclaw';
  if (/[\0\r\n]/.test(executable)) throw new Error('OpenClaw CLI path contains invalid characters');
  return executable;
}

function boundedInteger(value: number, min: number, max: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw new Error(`${label} must be an integer between ${min} and ${max}`);
  return value;
}

function parseSessionListing(text: string): { sessions: JsonObject[]; hasMore: boolean | undefined } {
  const parsed = parseJsonOutput(text, 'OpenClaw sessions listing');
  if (!isRecord(parsed) || !Array.isArray(parsed.sessions)) throw new Error('OpenClaw sessions listing has an unsupported shape');
  if (parsed.sessions.length > maxSessions) throw new Error('OpenClaw sessions listing exceeded the local processing limit');
  const sessions: JsonObject[] = [];
  for (const row of parsed.sessions) {
    if (!isRecord(row)) throw new Error('OpenClaw sessions listing contains an invalid row');
    sessions.push(row);
  }
  return { sessions, hasMore: typeof parsed.hasMore === 'boolean' ? parsed.hasMore : undefined };
}

async function readBoundedHistory(
  runner: OpenClawHistoryCommandRunner,
  sessionKey: string,
  agentId: string | undefined,
  openclawCli: string | undefined,
  maxMessages: number,
): Promise<{ messages: NormalizedMessage[]; hasMore: boolean }> {
  const pages: NormalizedMessage[][] = [];
  const maxPages = Math.ceil(maxMessages / gatewayPageSize);
  let rawMessages = 0;
  let responseBytes = 0;
  let offset: number | undefined;
  let hasMore = false;
  for (let pageIndex = 0; pageIndex < maxPages; pageIndex++) {
    const limit = Math.min(gatewayPageSize, maxMessages - rawMessages);
    if (limit < 1) break;
    const responseText = await runner(buildOpenClawHistoryCommand(sessionKey, {
      openclawCli, maxMessages: limit, ...(agentId ? { agentId } : {}), ...(offset === undefined ? {} : { offset }),
    }));
    responseBytes += Buffer.byteLength(responseText, 'utf8');
    if (responseBytes > maxHistoryResponseBytes) throw new Error('OpenClaw history exceeded the per-session response budget');
    const page = parseHistory(responseText, limit);
    pages.push(page.messages);
    rawMessages += page.rawCount;
    hasMore = page.hasMore;
    if (!hasMore) break;
    if (page.nextOffset === undefined || page.nextOffset === offset) break;
    offset = page.nextOffset;
    if (pageIndex === maxPages - 1 || rawMessages >= maxMessages) break;
  }
  const unique = new Map<string, NormalizedMessage>();
  for (const page of pages.reverse()) for (const message of page) unique.set(message.identity, message);
  return { messages: [...unique.values()], hasMore };
}

function parseHistory(text: string, maxMessages: number): { messages: NormalizedMessage[]; rawCount: number; hasMore: boolean; nextOffset?: number } {
  const parsed = parseJsonOutput(text, 'OpenClaw chat history');
  if (!isRecord(parsed) || !Array.isArray(parsed.messages)) throw new Error('OpenClaw chat history has an unsupported shape');
  if (parsed.messages.length > maxMessages) throw new Error('OpenClaw chat history exceeded the requested message limit');
  const messages: NormalizedMessage[] = [];
  for (const row of parsed.messages) {
    if (!isRecord(row)) continue;
    const value = isRecord(row.message) ? row.message : row;
    const role = boundedString(value.role, 32)?.toLowerCase();
    if (role !== 'user' && role !== 'assistant') continue;
    const textValue = visibleText(value.content);
    if (!textValue.trim()) continue;
    const rawTimestamp = value.timestamp ?? value.createdAt ?? row.timestamp;
    const time = timestamp(rawTimestamp);
    const rawId = boundedString(value.id, 512) ?? boundedString(value.messageId, 512) ?? boundedString(row.id, 512);
    if (!rawId) throw new Error('OpenClaw history contains a visible message without a stable identifier');
    const contentHash = sha256(textValue.slice(0, maxDigestBytes));
    const identity = sha256(`id:${rawId}`);
    messages.push({ identity, timestamp: time, role, text: textValue });
  }
  const unique = new Map<string, NormalizedMessage>();
  for (const message of messages) unique.set(message.identity, message);
  const nextOffset = Number.isSafeInteger(parsed.nextOffset) && (parsed.nextOffset as number) >= 0 ? parsed.nextOffset as number : undefined;
  return { messages: [...unique.values()], rawCount: parsed.messages.length, hasMore: parsed.hasMore === true, ...(nextOffset === undefined ? {} : { nextOffset }) };
}

function mapMessage(message: NormalizedMessage, sessionHash: string, includeContent: boolean): ThreadforgeEvent | undefined {
  const utf8 = Buffer.from(message.text, 'utf8');
  const digestText = utf8.subarray(0, maxDigestBytes);
  const retained = includeContent ? utf8.subarray(0, maxContentBytes).toString('utf8') : undefined;
  const truncated = utf8.byteLength > maxContentBytes;
  const content = {
    retention: includeContent && !truncated ? 'full' : 'metadata',
    included: includeContent && !truncated,
    hash: sha256(digestText),
    bytes: utf8.byteLength,
    ...(utf8.byteLength > maxDigestBytes ? { digest_truncated: true } : {}),
    ...(includeContent && truncated ? { truncated: true } : {}),
  };
  const user = message.role === 'user';
  const extra: JsonObject = { content };
  if (retained && !truncated) {
    if (user) {
      extra.prompt = { text: retained };
      extra.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: retained }] }] } };
    } else {
      extra.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: retained }] }] } };
    }
  }
  return {
    timestamp: message.timestamp,
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: {
      id: sha256(`${sessionHash}:${message.identity}`),
      kind: 'agent_runtime',
      action: user ? 'prompt.submitted' : 'agent.message',
      category: user ? 'prompt' : 'agent',
      fidelity: 'observed',
    },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'openclaw-gateway', collection_method: 'poll' },
    session: { id: `openclaw:${sessionHash}` },
    raw: { source: 'openclaw_chat_history', source_sha256: sessionHash, message_sha256: message.identity },
    ...extra,
  };
}

function visibleText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (!Array.isArray(value)) return '';
  const chunks: string[] = [];
  for (const item of value) {
    if (!isRecord(item) || item.type !== 'text' || typeof item.text !== 'string') continue;
    chunks.push(item.text);
  }
  return chunks.join('\n');
}

function timestamp(value: unknown): string {
  const date = typeof value === 'number' ? new Date(value) : typeof value === 'string' ? new Date(value) : new Date();
  return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function parseJsonOutput(text: string, label: string): unknown {
  if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes) throw new Error(`${label} exceeded the local output limit`);
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(text, 'utf8'))) as unknown; }
  catch { throw new Error(`${label} returned invalid JSON`); }
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
  const parent = path.dirname(statePath);
  await mkdir(parent, { recursive: true, mode: 0o700 }).catch(() => { throw new Error('unable to create private OpenClaw cursor directory'); });
  const info = await lstat(parent).catch(() => null);
  if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error('OpenClaw cursor parent must be a real directory');
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('OpenClaw history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock OpenClaw history cursor');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(`${statePath}.lock`).catch(() => {});
  }
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.openclaw-history-cursor.v1', version: stateVersion, sessions: {} };
  const info = await lstat(statePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect OpenClaw history cursor');
  });
  if (!info) return empty;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes) throw new Error('OpenClaw history cursor must be a bounded regular file');
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (opened.dev !== info.dev || opened.ino !== info.ino) throw new Error('OpenClaw cursor changed before read');
    const bytes = await handle.readFile();
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
      throw new Error('OpenClaw history cursor has an unsupported schema; inspect it before retrying');
    }
    const sessions: Record<string, SessionCursor> = {};
    for (const [key, value] of Object.entries(parsed.sessions)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || typeof value.latest_message_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.latest_message_sha256)) {
        throw new Error('OpenClaw history cursor contains an invalid checkpoint');
      }
      sessions[key] = { latest_message_sha256: value.latest_message_sha256 };
    }
    return { ...empty, sessions };
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('OpenClaw history cursor')) throw error;
    throw new Error('OpenClaw history cursor is malformed or unreadable');
  } finally { await handle?.close().catch(() => {}); }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const data = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (data.byteLength > maxStateBytes) throw new Error('OpenClaw history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write OpenClaw history cursor');
  }
}

function sha256(value: string | Buffer): string { return createHash('sha256').update(value).digest('hex'); }
function boundedString(value: unknown, max: number): string | undefined { return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined; }
function isBoundedString(value: unknown, max: number): value is string { return boundedString(value, max) !== undefined; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }

async function runCommand(command: OpenClawHistoryCommand): Promise<string> {
  if (process.platform === 'win32') return runPowerShell(command);
  return spawnCaptured(command.executable, command.args);
}

async function runPowerShell(command: OpenClawHistoryCommand): Promise<string> {
  const quote = (value: string) => {
    if (/[\0\r\n]/.test(value)) throw new Error('OpenClaw CLI argument contains invalid characters');
    return `'${value.replace(/'/g, "''")}'`;
  };
  const invocation = [command.executable, ...command.args].map(quote).join(' ');
  const script = `$ErrorActionPreference = 'Stop'\n& ${invocation}\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit`;
  return spawnCaptured('powershell.exe', ['-NoProfile', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')]);
}

async function spawnCaptured(executable: string, args: string[]): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const child = spawn(executable, args, { stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true });
    const chunks: Buffer[] = [];
    let size = 0;
    let settled = false;
    const timeout = setTimeout(() => {
      child.kill();
      finish(new Error('OpenClaw CLI timed out'));
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
        finish(new Error('OpenClaw CLI output exceeded the local limit'));
      } else chunks.push(Buffer.from(chunk));
    });
    child.once('error', () => finish(new Error('OpenClaw CLI could not be started')));
    child.once('close', (code) => {
      if (code !== 0) finish(new Error(`OpenClaw CLI exited with status ${code}`));
      else finish(undefined, Buffer.concat(chunks).toString('utf8'));
    });
  });
}
