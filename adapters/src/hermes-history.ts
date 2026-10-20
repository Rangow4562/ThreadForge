import { createHash, randomBytes } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import type { DatabaseSync } from 'node:sqlite';
import { emitEvents, type ThreadforgeEvent } from './emit.ts';

const stateVersion = 1;
const stateFileName = 'hermes-history.v1.json';
const maxStateBytes = 4 << 20;
const maxSessions = 500;
const maxMessagesPerSession = 2_000;
const maxEvents = 20_000;
const maxPreviewEvents = 500;
const maxBatch = 100;
const maxMessageChars = 4 << 10;
const maxContentBytes = 4 << 10;
const maxSyncSourceBytes = 64 << 20;

type JsonObject = Record<string, unknown>;
type HermesSession = { id: string; startedAt: unknown; endedAt: unknown };
type HermesMessage = {
  id: number;
  role: string;
  content: string;
  contentBytes: number;
  toolCallId: string;
  toolName: string;
  toolCalls: string;
  timestamp: unknown;
};
type SessionCursor = { last_message_id: number };
type CursorState = {
  schema_version: 'threadforge.hermes-history-cursor.v1';
  version: number;
  sessions: Record<string, SessionCursor>;
};

export type HermesHistoryOptions = {
  dbPath?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  maxMessages?: number;
  preview?: boolean;
  includeContent?: boolean;
};

export type HermesHistoryResult = {
  schema_version: 'threadforge.hermes-history-sync.v1';
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

export type HermesHistoryStatus = {
  schema_version: 'threadforge.hermes-history-status.v1';
  database_present: boolean;
  sessions: number;
  sessions_truncated: boolean;
  tracked_sessions: number;
  cursor_file_present: boolean;
  errors: Array<{ session_sha256?: string; message: string }>;
};

export async function getHermesHistoryStatus(options: Pick<HermesHistoryOptions, 'dbPath' | 'statePath' | 'maxSessions'> = {}): Promise<HermesHistoryStatus> {
  const dbPath = resolveDatabasePath(options.dbPath);
  const statePath = resolveStatePath(options.statePath);
  const state = await readState(statePath);
  const cursorFilePresent = await inspectOptionalRegularFile(statePath, maxStateBytes).then(Boolean);
  const missing: HermesHistoryStatus = {
    schema_version: 'threadforge.hermes-history-status.v1', database_present: false, sessions: 0,
    sessions_truncated: false, tracked_sessions: Object.keys(state.sessions).length,
    cursor_file_present: cursorFilePresent, errors: [],
  };
  const before = await inspectDatabase(dbPath);
  if (!before) return missing;
  let database: DatabaseSync | undefined;
  try {
    database = await openDatabase(dbPath, before);
    const count = database.prepare('SELECT COUNT(*) AS count FROM sessions WHERE archived = 0').get() as { count?: unknown } | undefined;
    const total = nonNegativeInteger(count?.count);
    if (total === undefined) throw new Error('Hermes state database session count is invalid');
    const limit = boundedInteger(options.maxSessions ?? 200, 1, maxSessions, '--max-sessions');
    return {
      ...missing, database_present: true, sessions: Math.min(total, limit), sessions_truncated: total > limit,
    };
  } catch {
    return { ...missing, errors: [{ message: 'Hermes state database could not be read or did not match the supported schema' }] };
  } finally {
    database?.close();
  }
}

export async function syncHermesHistory(options: HermesHistoryOptions = {}): Promise<HermesHistoryResult> {
  validateOptions(options);
  const result = emptyResult(options.includeContent ?? false);
  const dbPath = resolveDatabasePath(options.dbPath);
  const statePath = resolveStatePath(options.statePath);
  const before = await inspectDatabase(dbPath);
  if (!before) {
    result.complete = true;
    return result;
  }

  const operation = async (state: CursorState, persist: boolean) => {
    let database: DatabaseSync | undefined;
    try {
      database = await openDatabase(dbPath, before);
      const listing = database.prepare(`
        SELECT id, started_at, ended_at FROM sessions
        WHERE archived = 0 ORDER BY started_at, id LIMIT ?
      `).all((options.maxSessions ?? 50) + 1) as Array<Record<string, unknown>>;
      result.sessions = Math.min(listing.length, options.maxSessions ?? 50);
      result.sessions_truncated = listing.length > result.sessions;
      const readMessages = database.prepare(`
        SELECT id, role, substr(COALESCE(content, ''), 1, ?) AS content_prefix,
               length(CAST(COALESCE(content, '') AS BLOB)) AS content_bytes,
               tool_call_id, tool_name, substr(COALESCE(tool_calls, ''), 1, 16384) AS tool_calls, timestamp
        FROM messages
        WHERE session_id = ? AND active = 1 AND id > ?
        ORDER BY id LIMIT ?
      `);
      const lastMessage = database.prepare(
        'SELECT MAX(id) AS id FROM messages WHERE session_id = ? AND active = 1',
      );

      let totalSourceBytes = 0;
      for (const row of listing.slice(0, result.sessions)) {
        const rawId = boundedString(row.id, 512);
        if (!rawId) {
          result.errors.push({ message: 'Hermes returned a session without a valid identifier' });
          continue;
        }
        const sessionHash = sha256(rawId);
        const previous = state.sessions[sessionHash];
        const latest = lastMessage.get(rawId) as { id?: unknown } | undefined;
        const latestId = latest?.id === null || latest?.id === undefined ? 0 : nonNegativeInteger(latest.id);
        if (latestId === undefined) {
          result.errors.push({ session_sha256: sessionHash, message: 'Hermes session message cursor is invalid' });
          continue;
        }
        if (previous && previous.last_message_id > latestId) {
          result.cursor_conflicts++;
          result.errors.push({ session_sha256: sessionHash, message: 'history cursor is ahead of the available session; refusing to replay or skip records' });
          continue;
        }
        const rows = readMessages.all(maxMessageChars, rawId, previous?.last_message_id ?? 0, (options.maxMessages ?? maxMessagesPerSession) + 1) as Array<Record<string, unknown>>;
        result.messages_seen += Math.min(rows.length, options.maxMessages ?? maxMessagesPerSession);
        if (rows.length > (options.maxMessages ?? maxMessagesPerSession)) {
          result.history_truncated_sessions++;
          result.errors.push({ session_sha256: sessionHash, message: 'Hermes session history exceeded the per-session message limit; no cursor was advanced' });
          continue;
        }
        const messages = rows.map(parseMessage);
        if (messages.some((message) => !message)) {
          result.errors.push({ session_sha256: sessionHash, message: 'Hermes session contains an invalid message row; no cursor was advanced' });
          continue;
        }
        const parsedMessages = messages as HermesMessage[];
        totalSourceBytes += parsedMessages.reduce((sum, message) => sum + message.contentBytes, 0);
        if (totalSourceBytes > maxSyncSourceBytes) {
          result.errors.push({ session_sha256: sessionHash, message: 'Hermes history sync exceeded the total source-byte limit; no cursor was advanced for this session' });
          break;
        }
        if (parsedMessages.length + result.events_emitted + result.events_previewed > maxEvents) {
          result.errors.push({ session_sha256: sessionHash, message: 'Hermes history sync exceeded the event processing limit' });
          continue;
        }
        const session: HermesSession = { id: rawId, startedAt: row.started_at, endedAt: row.ended_at };
        const mapped = parsedMessages.flatMap((message) => {
          const event = mapMessage(message, session, sessionHash, options.includeContent ?? false);
          return event ? [{ messageId: message.id, event }] : [];
        });

        if (options.preview) {
          result.events_previewed += mapped.length;
          result.events ??= [];
          for (const item of mapped) {
            if (result.events.length < maxPreviewEvents) result.events.push(item.event);
            else result.preview_truncated = true;
          }
          continue;
        }

        let failed = false;
        for (let offset = 0; offset < mapped.length; offset += maxBatch) {
          const batch = mapped.slice(offset, offset + maxBatch);
          try {
            await emitEvents(batch.map((item) => item.event), { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
          } catch {
            result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected Hermes history events' });
            failed = true;
            break;
          }
          state.sessions[sessionHash] = { last_message_id: batch[batch.length - 1]!.messageId };
          result.events_emitted += batch.length;
          if (persist) await writeState(statePath, state);
        }
        if (failed) continue;
        if (latestId > (previous?.last_message_id ?? 0)) {
          state.sessions[sessionHash] = { last_message_id: latestId };
          if (persist) await writeState(statePath, state);
          result.sessions_changed++;
        }
      }
      if (persist) await writeState(statePath, state);
    } finally {
      if (database) {
        try { database.exec('ROLLBACK'); } catch { /* transaction may already be closed */ }
        database.close();
      }
    }
  };

  if (options.preview) await operation(await readState(statePath), false);
  else await withStateLock(statePath, async () => operation(await readState(statePath), true));
  result.complete = result.errors.length === 0 && !result.sessions_truncated && result.history_truncated_sessions === 0;
  return result;
}

function emptyResult(includeContent: boolean): HermesHistoryResult {
  return {
    schema_version: 'threadforge.hermes-history-sync.v1', sessions: 0, sessions_changed: 0,
    messages_seen: 0, events_emitted: 0, events_previewed: 0, sessions_truncated: false,
    history_truncated_sessions: 0, cursor_conflicts: 0,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: HermesHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? 50, 1, maxSessions, '--max-sessions');
  boundedInteger(options.maxMessages ?? maxMessagesPerSession, 1, maxMessagesPerSession, '--max-messages');
}

function parseMessage(row: Record<string, unknown>): HermesMessage | undefined {
  const id = nonNegativeInteger(row.id);
  const role = boundedString(row.role, 64);
  const content = typeof row.content_prefix === 'string' ? row.content_prefix : '';
  const contentBytes = nonNegativeInteger(row.content_bytes);
  if (id === undefined || id < 1 || !role || contentBytes === undefined) return undefined;
  return {
    id, role, content, contentBytes,
    toolCallId: boundedString(row.tool_call_id, 512) ?? '',
    toolName: safeLabel(row.tool_name, 128) ?? '',
    toolCalls: typeof row.tool_calls === 'string' ? row.tool_calls : '',
    timestamp: row.timestamp,
  };
}

function mapMessage(message: HermesMessage, session: HermesSession, sessionHash: string, includeContent: boolean): ThreadforgeEvent | undefined {
  const role = message.role.toLowerCase();
  const isUser = role === 'user';
  const isAssistant = role === 'assistant';
  const isTool = role === 'tool' || role === 'function';
  if (!isUser && !isAssistant && !isTool) return undefined;
  const toolCalls = isAssistant ? parseToolCalls(message.toolCalls) : [];
  const toolName = message.toolName || toolCalls.find((call) => call.name)?.name || '';
  const preview = Buffer.from(message.content, 'utf8');
  const retained = preview.byteLength <= maxContentBytes && message.contentBytes <= maxContentBytes ? message.content : undefined;
  const digestBytes = preview.subarray(0, maxContentBytes);
  const event: ThreadforgeEvent = {
    timestamp: toTimestamp(message.timestamp, session.startedAt), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: {
      id: sha256(`hermes:${sessionHash}:${message.id}`), kind: 'agent_runtime',
      action: isUser ? 'prompt.submitted' : isAssistant ? (message.toolCallId || toolName || toolCalls.length ? 'tool.invoked' : 'agent.message') : 'tool.completed',
      category: isUser ? 'prompt' : isAssistant && !message.toolCallId && !toolName && !toolCalls.length ? 'agent' : 'tool', fidelity: 'observed',
    },
    severity: 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'hermes', collection_method: 'poll' },
    session: { id: `hermes:${sessionHash}` },
    content: {
      included: includeContent && retained !== undefined,
      hash: sha256(digestBytes), bytes: message.contentBytes,
      ...(digestBytes.byteLength < message.contentBytes || preview.byteLength < message.contentBytes ? { digest_truncated: true } : {}),
      ...(includeContent && retained === undefined ? { truncated: true } : {}),
    },
    raw: {
      source: 'hermes_state_db', session_sha256: sessionHash,
      message_sha256: sha256(`${sessionHash}:${message.id}`),
    },
  };
  if (toolName) event.tool = { name: toolName };
  if (toolCalls.length > 0) event.tool = { ...(event.tool as JsonObject | undefined), call_count: toolCalls.length };
  if (includeContent && retained !== undefined) {
    if (isUser) {
      event.prompt = { text: retained };
      event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: retained }] }] } };
    } else if (isAssistant) {
      event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: retained }] }] } };
    }
  }
  return event;
}

function parseToolCalls(raw: string): Array<{ name?: string }> {
  if (!raw || Buffer.byteLength(raw, 'utf8') > 16_384) return [];
  try {
    const value = JSON.parse(raw) as unknown;
    if (!Array.isArray(value)) return [];
    return value.slice(0, 32).flatMap((entry) => {
      if (!isRecord(entry)) return [];
      const fn = isRecord(entry.function) ? entry.function : entry;
      const name = safeLabel(fn.name, 128);
      return [{ ...(name ? { name } : {}) }];
    });
  } catch { return []; }
}

function toTimestamp(value: unknown, fallback: unknown): string {
  for (const candidate of [value, fallback]) {
    const parsed = typeof candidate === 'number' && Number.isFinite(candidate)
      ? new Date(candidate * 1000)
      : typeof candidate === 'string' ? new Date(candidate) : undefined;
    if (parsed && !Number.isNaN(parsed.getTime())) return parsed.toISOString();
  }
  return '1970-01-01T00:00:00.000Z';
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function resolveDatabasePath(value?: string): string {
  if (value !== undefined) return absolutePath(value, '--db-path');
  return path.join(os.homedir(), '.hermes', 'state.db');
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

function absolutePath(value: string, name: string): string {
  if (!path.isAbsolute(value)) throw new Error(`${name} must be an absolute path`);
  return path.resolve(value);
}

async function inspectDatabase(databasePath: string) {
  const info = await lstat(databasePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Hermes state database');
  });
  if (!info) return null;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Hermes state database must be a regular non-symlink file');
  return info;
}

async function openDatabase(databasePath: string, before: Awaited<ReturnType<typeof lstat>>) {
  const canonical = await realpath(databasePath).catch(() => '');
  if (!canonical || canonical !== path.resolve(databasePath)) throw new Error('Hermes state database path cannot traverse symbolic links');
  const { DatabaseSync: SQLiteDatabase } = await import('node:sqlite');
  const database = new SQLiteDatabase(canonical, { readOnly: true });
  try {
    database.exec('PRAGMA busy_timeout=1000');
    database.exec('BEGIN DEFERRED TRANSACTION');
    const after = await lstat(databasePath).catch(() => null);
    if (!after || !after.isFile() || after.isSymbolicLink() || !sameFile(before, after)) {
      throw new Error('Hermes state database changed while it was being opened');
    }
    const tables = database.prepare("SELECT name FROM sqlite_master WHERE type='table'").all() as Array<{ name?: unknown }>;
    if (!tables.some((table) => table.name === 'sessions') || !tables.some((table) => table.name === 'messages')) {
      throw new Error('Hermes state database does not contain the supported sessions/messages tables');
    }
    return database;
  } catch (error) {
    database.close();
    throw error;
  }
}

function sameFile(a: Awaited<ReturnType<typeof lstat>>, b: Awaited<ReturnType<typeof lstat>>): boolean {
  return a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeMs === b.mtimeMs;
}

async function inspectOptionalRegularFile(filePath: string, maxBytes: number) {
  const info = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Hermes history cursor');
  });
  if (!info) return null;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxBytes) throw new Error('Hermes history cursor must be a bounded regular file');
  return info;
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.hermes-history-cursor.v1', version: stateVersion, sessions: {} };
  const info = await inspectOptionalRegularFile(statePath, maxStateBytes);
  if (!info) return empty;
  let handle;
  try {
    handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('cursor changed before read');
    const parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(await handle.readFile())) as unknown;
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
      throw new Error('unsupported cursor schema');
    }
    const sessions: Record<string, SessionCursor> = {};
    for (const [key, value] of Object.entries(parsed.sessions)) {
      if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || nonNegativeInteger(value.last_message_id) === undefined) {
        throw new Error('invalid cursor checkpoint');
      }
      sessions[key] = { last_message_id: value.last_message_id as number };
    }
    return { ...empty, sessions };
  } catch {
    throw new Error('Hermes history cursor is malformed or unreadable; inspect it before retrying');
  } finally { await handle?.close().catch(() => {}); }
}

async function verifyStateParent(statePath: string, create: boolean): Promise<void> {
  const parent = path.dirname(statePath);
  if (create) await mkdir(parent, { recursive: true, mode: 0o700 }).catch(() => { throw new Error('unable to create private Hermes cursor directory'); });
  const info = await lstat(parent).catch(() => null);
  if (!info || !info.isDirectory() || info.isSymbolicLink()) throw new Error('Hermes cursor parent must be a real directory');
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await verifyStateParent(statePath, true);
  let lock;
  try { lock = await open(`${statePath}.lock`, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Hermes history sync is locked by another writer or a stale lock');
    throw new Error('unable to lock Hermes history cursor');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(`${statePath}.lock`).catch(() => {});
  }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  await verifyStateParent(statePath, true);
  const data = Buffer.from(`${JSON.stringify(state, null, 2)}\n`, 'utf8');
  if (data.byteLength > maxStateBytes) throw new Error('Hermes history cursor exceeds the local size limit');
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
    throw new Error('unable to atomically write Hermes history cursor');
  }
}

function boundedInteger(value: number, min: number, max: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw new Error(`${label} must be an integer between ${min} and ${max}`);
  return value;
}

function nonNegativeInteger(value: unknown): number | undefined {
  const number = typeof value === 'bigint' ? Number(value) : value;
  return typeof number === 'number' && Number.isSafeInteger(number) && number >= 0 ? number : undefined;
}

function boundedString(value: unknown, max: number): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined;
}

function sha256(value: string | Uint8Array): string { return createHash('sha256').update(value).digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return isRecord(error) && error.code === code; }
