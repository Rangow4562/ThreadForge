import { createHash, randomBytes } from 'node:crypto';
import { constants, type Stats } from 'node:fs';
import { lstat, mkdir, open, realpath, rename, unlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import type { DatabaseSync } from 'node:sqlite';
import { emitEvents, type ThreadforgeEvent } from './emit.ts';
import { isSameRealPathWithoutLinks } from './path-safety.ts';

const supportedSchemaVersion = 17;
const stateVersion = 1;
const stateFileName = 'goose-history.v1.json';
const maxStateBytes = 4 << 20;
const maxTrackedSessions = 100_000;
const maxSessions = 500;
const maxMessagesPerSession = 2_000;
const maxEvents = 20_000;
const maxPreviewEvents = 500;
const maxBatch = 100;
const maxContentBytes = 4 << 10;
const maxMessageSourceBytes = 1 << 20;
const maxSyncSourceBytes = 64 << 20;

type JsonObject = Record<string, unknown>;
type SessionCursor = { last_message_id: number; last_message_sha256: string };
type CursorState = {
  schema_version: 'threadforge.goose-history-cursor.v1';
  version: number;
  sessions: Record<string, SessionCursor>;
};
type GooseMessage = {
  id: number;
  role: 'user' | 'assistant' | string;
  contentJson: string;
  sourceBytes: number;
  timestamp: unknown;
  metadata: JsonObject;
};

export type GooseHistoryOptions = {
  dbPath?: string;
  statePath?: string;
  endpoint?: string;
  maxSessions?: number;
  maxMessages?: number;
  preview?: boolean;
  includeContent?: boolean;
  env?: NodeJS.ProcessEnv;
  homeDir?: string;
  platform?: NodeJS.Platform;
};

export type GooseHistoryStatus = {
  schema_version: 'threadforge.goose-history-status.v1';
  database_present: boolean;
  source_schema_version?: number;
  sessions: number;
  sessions_truncated: boolean;
  tracked_sessions: number;
  cursor_file_present: boolean;
  errors: Array<{ message: string }>;
};

export type GooseHistoryResult = {
  schema_version: 'threadforge.goose-history-sync.v1';
  source_schema_version?: number;
  sessions: number;
  sessions_changed: number;
  sessions_truncated: boolean;
  missing_tracked_sessions: number;
  messages_seen: number;
  events_emitted: number;
  events_previewed: number;
  history_truncated_sessions: number;
  content_items_truncated: number;
  cursor_conflicts: number;
  content_mode: 'metadata' | 'include-content';
  complete: boolean;
  preview_truncated: boolean;
  errors: Array<{ session_sha256?: string; message: string }>;
  events?: ThreadforgeEvent[];
};

/** Resolve Goose's documented data-root override or the platform data directory. */
export function resolveGooseHistoryDatabasePath(options: Pick<GooseHistoryOptions, 'env' | 'homeDir' | 'platform'> = {}): string {
  const env = options.env ?? process.env;
  const homeDir = options.homeDir ?? os.homedir();
  const platform = options.platform ?? process.platform;
  const pathApi = platform === 'win32' ? path.win32 : path.posix;
  const configuredRoot = env.GOOSE_PATH_ROOT?.trim();
  if (configuredRoot) {
    if (!pathApi.isAbsolute(configuredRoot)) throw new Error('GOOSE_PATH_ROOT must be absolute');
    return pathApi.join(configuredRoot, 'data', 'sessions', 'sessions.db');
  }

  if (platform === 'win32') {
    const localAppData = env.LOCALAPPDATA?.trim();
    const base = localAppData && pathApi.isAbsolute(localAppData)
      ? localAppData
      : pathApi.join(homeDir, 'AppData', 'Local');
    return pathApi.join(base, 'Block', 'goose', 'data', 'sessions', 'sessions.db');
  }
  if (platform === 'darwin') {
    return pathApi.join(homeDir, 'Library', 'Application Support', 'Block', 'goose', 'data', 'sessions', 'sessions.db');
  }
  if (platform === 'linux') {
    const xdgDataHome = env.XDG_DATA_HOME?.trim();
    const base = xdgDataHome && pathApi.isAbsolute(xdgDataHome)
      ? xdgDataHome
      : pathApi.join(homeDir, '.local', 'share');
    return pathApi.join(base, 'goose', 'sessions', 'sessions.db');
  }
  throw new Error('Goose history default path is unsupported on this platform; supply --db-path');
}

export async function getGooseHistoryStatus(
  options: Pick<GooseHistoryOptions, 'dbPath' | 'statePath' | 'maxSessions' | 'env' | 'homeDir' | 'platform'> = {},
): Promise<GooseHistoryStatus> {
  const dbPath = resolveDatabasePath(options);
  const statePath = resolveStatePath(options);
  const state = await readState(statePath);
  const cursorFilePresent = Boolean(await inspectOptionalRegularFile(statePath, maxStateBytes));
  const base: GooseHistoryStatus = {
    schema_version: 'threadforge.goose-history-status.v1', database_present: false,
    sessions: 0, sessions_truncated: false, tracked_sessions: Object.keys(state.sessions).length,
    cursor_file_present: cursorFilePresent, errors: [],
  };
  const before = await inspectDatabase(dbPath);
  if (!before) return base;
  let database: DatabaseSync | undefined;
  try {
    database = await openDatabase(dbPath, before);
    const count = database.prepare("SELECT COUNT(*) AS count FROM sessions WHERE session_type = 'user'").get() as { count?: unknown } | undefined;
    const total = nonNegativeInteger(count?.count);
    if (total === undefined) throw new Error('session count is invalid');
    const limit = boundedInteger(options.maxSessions ?? maxSessions, 1, maxSessions, '--max-sessions');
    return {
      ...base, database_present: true, source_schema_version: supportedSchemaVersion,
      sessions: Math.min(total, limit), sessions_truncated: total > limit,
    };
  } catch {
    return { ...base, errors: [{ message: 'Goose database could not be read or did not match the supported read-only schema' }] };
  } finally {
    database?.close();
  }
}

export async function syncGooseHistory(options: GooseHistoryOptions = {}): Promise<GooseHistoryResult> {
  validateOptions(options);
  const result = emptyResult(options.includeContent ?? false);
  const dbPath = resolveDatabasePath(options);
  const statePath = resolveStatePath(options);
  if (samePath(dbPath, statePath)) throw new Error('Goose database and Threadforge cursor paths must be different');
  const before = await inspectDatabase(dbPath);
  if (!before) {
    result.complete = true;
    return result;
  }

  const operation = async (state: CursorState, persist: boolean) => {
    let database: DatabaseSync | undefined;
    try {
      database = await openDatabase(dbPath, before);
      result.source_schema_version = supportedSchemaVersion;
      const listing = database.prepare(`
        SELECT substr(id, 1, 513) AS id, length(id) AS id_length, created_at FROM sessions
        WHERE session_type = 'user'
        ORDER BY updated_at DESC, id DESC LIMIT ?
      `).all((options.maxSessions ?? maxSessions) + 1) as Array<Record<string, unknown>>;
      result.sessions = Math.min(listing.length, options.maxSessions ?? maxSessions);
      result.sessions_truncated = listing.length > result.sessions;

      const messagesAfter = database.prepare(`
        SELECT id, substr(role, 1, 65) AS role, length(role) AS role_length,
               length(CAST(content_json AS BLOB)) AS content_bytes,
               created_timestamp,
               CASE WHEN metadata_json IS NULL THEN NULL
                    WHEN length(CAST(metadata_json AS BLOB)) <= 65536 THEN metadata_json
                    ELSE 'threadforge:oversized-metadata' END AS metadata_json
        FROM messages
        WHERE session_id = ? AND id > ?
        ORDER BY id LIMIT ?
      `);
      const messageById = database.prepare(`
        SELECT id, substr(role, 1, 65) AS role, length(role) AS role_length,
               length(CAST(content_json AS BLOB)) AS content_bytes, created_timestamp,
               CASE WHEN metadata_json IS NULL THEN NULL
                    WHEN length(CAST(metadata_json AS BLOB)) <= 65536 THEN metadata_json
                    ELSE 'threadforge:oversized-metadata' END AS metadata_json
        FROM messages WHERE session_id = ? AND id = ?
      `);
      const contentById = database.prepare(`
        SELECT CASE WHEN length(CAST(content_json AS BLOB)) <= ? THEN content_json ELSE NULL END AS content_json
        FROM messages WHERE session_id = ? AND id = ?
      `);
      const latestMessageId = database.prepare('SELECT MAX(id) AS id FROM messages WHERE session_id = ?');
      const seenSessions = new Set<string>();
      let totalSourceBytes = 0;

      for (const row of listing.slice(0, result.sessions)) {
        const rawSessionId = boundedString(row.id, 512);
        const sessionIdLength = nonNegativeInteger(row.id_length);
        if (!rawSessionId || sessionIdLength === undefined || sessionIdLength > 512 || rawSessionId.length !== sessionIdLength) {
          result.errors.push({ message: 'Goose returned a session without a valid identifier' });
          continue;
        }
        const sessionHash = sha256(rawSessionId);
        seenSessions.add(sessionHash);
        const previous = state.sessions[sessionHash];
        const latest = latestMessageId.get(rawSessionId) as { id?: unknown } | undefined;
        const latestId = latest?.id === null || latest?.id === undefined ? 0 : positiveInteger(latest.id);
        if (latestId === undefined) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose message cursor is invalid' });
          continue;
        }
        if (previous && previous.last_message_id > latestId) {
          cursorConflict(result, sessionHash, 'cursor is ahead of the available session; refusing to replay or skip records');
          continue;
        }
        if (previous) {
          const anchor = messageById.get(rawSessionId, previous.last_message_id) as Record<string, unknown> | undefined;
          const anchorContent = anchor ? contentById.get(maxMessageSourceBytes, rawSessionId, previous.last_message_id) as { content_json?: unknown } | undefined : undefined;
          const parsedAnchor = anchor && anchorContent ? parseMessage(anchor, anchorContent.content_json) : undefined;
          if (!parsedAnchor || messageDigest(parsedAnchor) !== previous.last_message_sha256) {
            cursorConflict(result, sessionHash, 'last captured Goose message changed or disappeared; refusing to replay history');
            continue;
          }
        }

        const rows = messagesAfter.all(
          rawSessionId, previous?.last_message_id ?? 0,
          (options.maxMessages ?? maxMessagesPerSession) + 1,
        ) as Array<Record<string, unknown>>;
        const limit = options.maxMessages ?? maxMessagesPerSession;
        const rowsSeen = Math.min(rows.length, limit);
        if (result.messages_seen + rowsSeen > maxEvents) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose sync exceeded the 20,000-message processing limit; this session cursor was not advanced' });
          break;
        }
        result.messages_seen += rowsSeen;
        if (rows.length > limit) {
          result.history_truncated_sessions++;
          result.errors.push({ session_sha256: sessionHash, message: 'Goose session exceeded the per-sync message limit; its cursor was not advanced' });
          continue;
        }
        const sourceSizes: number[] = [];
        let invalidSize = false;
        for (const row of rows) {
          const size = nonNegativeInteger(row.content_bytes);
          if (size === undefined || positiveInteger(row.id) === undefined) { invalidSize = true; break; }
          sourceSizes.push(size);
        }
        if (invalidSize) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose session contains an invalid message row; its cursor was not advanced' });
          continue;
        }
        if (sourceSizes.some((size) => size > maxMessageSourceBytes)) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose session contains a message exceeding the 1 MiB per-message read limit; its cursor was not advanced' });
          continue;
        }
        totalSourceBytes += sourceSizes.reduce((sum, size) => sum + size, 0);
        if (totalSourceBytes > maxSyncSourceBytes) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose sync exceeded the aggregate source-byte limit; this session cursor was not advanced' });
          break;
        }
        if (rowsSeen + result.events_emitted + result.events_previewed > maxEvents) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose sync exceeded the event processing limit; this session cursor was not advanced' });
          continue;
        }

        const messages: GooseMessage[] = [];
        let invalidRow = false;
        for (const row of rows) {
          const id = positiveInteger(row.id)!;
          const source = contentById.get(maxMessageSourceBytes, rawSessionId, id) as { content_json?: unknown } | undefined;
          const message = source ? parseMessage(row, source.content_json) : undefined;
          if (!message) { invalidRow = true; break; }
          messages.push(message);
        }
        if (invalidRow) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose session changed or contains invalid content; its cursor was not advanced' });
          continue;
        }

        const mapped: Array<{ message: GooseMessage; event: ThreadforgeEvent }> = [];
        let invalid = false;
        for (const message of messages) {
          const metadata = parseMetadata(message.metadata);
          if (!metadata) { invalid = true; break; }
          if (metadata.userVisible === false || metadata.userVisible === 0) continue;
          if (message.role !== 'user' && message.role !== 'assistant') continue;
          const event = mapMessage(message, sessionHash, options.includeContent ?? false);
          if (event.content && (event.content as JsonObject).truncated === true) result.content_items_truncated++;
          mapped.push({ message, event });
        }
        if (invalid) {
          result.errors.push({ session_sha256: sessionHash, message: 'Goose message metadata is invalid; its cursor was not advanced' });
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

        let failed = false;
        for (let offset = 0; offset < mapped.length; offset += maxBatch) {
          const batch = mapped.slice(offset, offset + maxBatch);
          try {
            await emitEvents(batch.map((item) => item.event), { endpoint: options.endpoint ?? process.env.THREADFORGE_EVENT_ENDPOINT });
          } catch {
            result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected Goose history events' });
            failed = true;
            break;
          }
          const lastAccepted = batch[batch.length - 1]!.message;
          state.sessions[sessionHash] = { last_message_id: lastAccepted.id, last_message_sha256: messageDigest(lastAccepted) };
          result.events_emitted += batch.length;
          if (persist) await writeState(statePath, state);
        }
        if (failed) continue;
        if (latestId > (previous?.last_message_id ?? 0)) {
          const lastRow = messages.at(-1);
          if (!lastRow || lastRow.id !== latestId) {
            result.errors.push({ session_sha256: sessionHash, message: 'Goose message set changed during the read; the cursor was not advanced' });
            continue;
          }
          state.sessions[sessionHash] = { last_message_id: latestId, last_message_sha256: messageDigest(lastRow) };
          if (persist) await writeState(statePath, state);
          result.sessions_changed++;
        }
      }

      result.missing_tracked_sessions = result.sessions_truncated ? 0 :
        Object.keys(state.sessions).filter((sessionHash) => !seenSessions.has(sessionHash)).length;
      if (result.missing_tracked_sessions > 0) {
        result.errors.push({ message: 'Previously tracked Goose sessions are no longer present; review the source database before resetting cursors' });
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
  result.complete = result.errors.length === 0 && !result.sessions_truncated &&
    result.history_truncated_sessions === 0 && result.content_items_truncated === 0 &&
    result.missing_tracked_sessions === 0;
  return result;
}

function emptyResult(includeContent: boolean): GooseHistoryResult {
  return {
    schema_version: 'threadforge.goose-history-sync.v1', sessions: 0, sessions_changed: 0,
    sessions_truncated: false, missing_tracked_sessions: 0, messages_seen: 0,
    events_emitted: 0, events_previewed: 0, history_truncated_sessions: 0,
    content_items_truncated: 0, cursor_conflicts: 0,
    content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
    preview_truncated: false, errors: [],
  };
}

function validateOptions(options: GooseHistoryOptions): void {
  if (options.preview && options.includeContent) throw new Error('--print cannot be combined with --include-content');
  boundedInteger(options.maxSessions ?? maxSessions, 1, maxSessions, '--max-sessions');
  boundedInteger(options.maxMessages ?? maxMessagesPerSession, 1, maxMessagesPerSession, '--max-messages');
}

function parseMessage(row: Record<string, unknown>, contentJson: unknown): GooseMessage | undefined {
  const id = positiveInteger(row.id);
  const role = boundedString(row.role, 64);
  const roleLength = nonNegativeInteger(row.role_length);
  const rawMetadata = row.metadata_json;
  const sourceBytes = nonNegativeInteger(row.content_bytes);
  if (id === undefined || !role || roleLength === undefined || roleLength !== role.length || typeof contentJson !== 'string' || sourceBytes === undefined ||
      sourceBytes > maxMessageSourceBytes || Buffer.byteLength(contentJson, 'utf8') !== sourceBytes) return undefined;
  return { id, role, contentJson, sourceBytes, timestamp: row.created_timestamp, metadata: { raw: rawMetadata } };
}

function parseMetadata(messageMetadata: JsonObject): JsonObject | undefined {
  const raw = messageMetadata.raw;
  if (raw === null || raw === undefined || raw === '') return {};
  if (typeof raw !== 'string' || Buffer.byteLength(raw, 'utf8') > 64 << 10) return undefined;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isRecord(parsed) ? parsed : undefined;
  } catch { return undefined; }
}

function mapMessage(message: GooseMessage, sessionHash: string, includeContent: boolean): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: toTimestamp(message.timestamp), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: {
      id: sha256(`goose:${sessionHash}:${message.id}`), kind: 'agent_runtime',
      action: message.role === 'user' ? 'prompt.submitted' : 'agent.message',
      category: message.role === 'user' ? 'prompt' : 'agent', fidelity: 'observed',
    },
    severity: 'info', endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'goose', collection_method: 'poll' },
    session: { id: `goose:${sessionHash}` },
    content: {
      included: false, hash: sha256(message.contentJson), bytes: message.sourceBytes,
    },
    raw: { source: 'goose_sessions_db', session_sha256: sessionHash, message_sha256: sha256(`${sessionHash}:${message.id}`) },
  };

  if (includeContent) {
    const parsed = parseVisibleText(message.contentJson);
    if (parsed !== undefined) {
      const byteLength = Buffer.byteLength(parsed, 'utf8');
      if (byteLength <= maxContentBytes) {
        (event.content as JsonObject).included = true;
        if (message.role === 'user') {
          event.prompt = { text: parsed };
          event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: parsed }] }] } };
        } else {
          event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: parsed }] }] } };
        }
      } else (event.content as JsonObject).truncated = true;
    } else (event.content as JsonObject).truncated = true;
  } else if (includeContent) (event.content as JsonObject).truncated = true;
  return event;
}

function parseVisibleText(raw: string): string | undefined {
  try {
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value)) return undefined;
    const parts = value.flatMap((part) =>
      isRecord(part) && part.type === 'text' && typeof part.text === 'string' ? [part.text] : [],
    );
    return parts.join('');
  } catch { return undefined; }
}

function messageDigest(message: GooseMessage): string {
  return sha256(`${message.id}\0${message.role}\0${message.sourceBytes}\0${message.timestamp ?? ''}\0${String(message.metadata.raw ?? '')}\0${message.contentJson}`);
}

async function openDatabase(databasePath: string, before: Stats) {
  const canonical = await realpath(databasePath).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, databasePath)) {
    throw new Error('Goose database path cannot traverse symbolic links');
  }
  const { DatabaseSync: SQLiteDatabase } = await import('node:sqlite');
  const database = new SQLiteDatabase(canonical, { readOnly: true });
  try {
    database.exec('PRAGMA busy_timeout=1000');
    database.exec('BEGIN DEFERRED TRANSACTION');
    const after = await lstat(databasePath, { bigint: false }).catch(() => null);
    if (!after || !after.isFile() || after.isSymbolicLink() || !sameFile(before, after)) {
      throw new Error('Goose database changed while it was being opened');
    }
    const schemaTable = database.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'").get();
    if (!schemaTable) throw new Error('Goose schema-version table is missing');
    const versionRow = database.prepare('SELECT MAX(version) AS version FROM schema_version').get() as { version?: unknown } | undefined;
    if (versionRow?.version !== supportedSchemaVersion) throw new Error('unsupported Goose schema version');
    requireColumns(database, 'sessions', ['id', 'session_type', 'created_at', 'updated_at']);
    requireColumns(database, 'messages', ['id', 'session_id', 'role', 'content_json', 'created_timestamp', 'metadata_json']);
    return database;
  } catch (error) {
    database.close();
    throw error;
  }
}

function requireColumns(database: DatabaseSync, table: 'sessions' | 'messages', required: string[]): void {
  const columns = database.prepare(`PRAGMA table_info(${table})`).all() as Array<{ name?: unknown }>;
  const names = new Set(columns.flatMap((column) => typeof column.name === 'string' ? [column.name] : []));
  if (required.some((column) => !names.has(column))) throw new Error(`Goose ${table} schema is incomplete`);
}

async function inspectDatabase(databasePath: string) {
  const info = await lstat(databasePath, { bigint: false }).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Goose database');
  });
  if (!info) return null;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Goose database must be a regular non-symlink file');
  return info;
}

async function inspectOptionalRegularFile(filePath: string, maxBytes: number) {
  const info = await lstat(filePath, { bigint: false }).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Goose history cursor');
  });
  if (!info) return null;
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxBytes) throw new Error('Goose history cursor must be a bounded regular file');
  return info;
}

async function readState(statePath: string): Promise<CursorState> {
  const empty: CursorState = { schema_version: 'threadforge.goose-history-cursor.v1', version: stateVersion, sessions: {} };
  const info = await inspectOptionalRegularFile(statePath, maxStateBytes);
  if (!info) return empty;
  const handle = await open(statePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const openedInfo = await handle.stat({ bigint: false });
    const latestInfo = await lstat(statePath, { bigint: false });
    if (!latestInfo.isFile() || latestInfo.isSymbolicLink() || !sameFile(info, openedInfo) || !sameFile(info, latestInfo)) {
      throw new Error('Goose history cursor changed while it was being read');
    }
    const bytes = Buffer.alloc(openedInfo.size);
    const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
    const parsed: unknown = JSON.parse(bytes.subarray(0, bytesRead).toString('utf8'));
    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
      throw new Error('Goose history cursor has an unsupported structure');
    }
    const sessions: Record<string, SessionCursor> = {};
    const entries = Object.entries(parsed.sessions);
    if (entries.length > maxTrackedSessions) throw new Error('Goose history cursor exceeds its session limit');
    for (const [key, value] of entries) {
      if (!/^[a-f0-9]{64}$/.test(key) || !isRecord(value) ||
          !Number.isSafeInteger(value.last_message_id) || Number(value.last_message_id) < 1 ||
          typeof value.last_message_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(value.last_message_sha256)) {
        throw new Error('Goose history cursor contains an invalid session entry');
      }
      sessions[key] = { last_message_id: Number(value.last_message_id), last_message_sha256: value.last_message_sha256 };
    }
    return { schema_version: empty.schema_version, version: stateVersion, sessions };
  } finally { await handle.close(); }
}

async function writeState(statePath: string, state: CursorState): Promise<void> {
  const body = JSON.stringify(state);
  if (Buffer.byteLength(body, 'utf8') > maxStateBytes) throw new Error('Goose history cursor exceeds its storage limit');
  const parent = path.dirname(statePath);
  await mkdir(parent, { recursive: true, mode: 0o700 });
  if (process.platform !== 'win32') await (await import('node:fs/promises')).chmod(parent, 0o700).catch(() => undefined);
  const existing = await inspectOptionalRegularFile(statePath, maxStateBytes);
  if (existing) {
    const canonical = await realpath(statePath).catch(() => '');
    if (!canonical || !await isSameRealPathWithoutLinks(canonical, statePath)) throw new Error('Goose history cursor path cannot traverse symbolic links');
  }
  const tempPath = `${statePath}.${process.pid}.${randomBytes(8).toString('hex')}.tmp`;
  const handle = await open(tempPath, 'wx', 0o600);
  try {
    await handle.writeFile(body, 'utf8');
    await handle.sync();
  } finally { await handle.close(); }
  try { await rename(tempPath, statePath); }
  catch (error) { await unlink(tempPath).catch(() => undefined); throw error; }
}

async function withStateLock<T>(statePath: string, action: () => Promise<T>): Promise<T> {
  await mkdir(path.dirname(statePath), { recursive: true, mode: 0o700 });
  const lockPath = `${statePath}.lock`;
  let lock;
  try { lock = await open(lockPath, 'wx', 0o600); }
  catch { throw new Error('Goose history cursor is locked by another sync or has a stale lock'); }
  try { await lock.writeFile(String(process.pid), 'utf8'); await lock.sync(); return await action(); }
  finally { await lock.close(); await unlink(lockPath).catch(() => undefined); }
}

function resolveDatabasePath(options: Pick<GooseHistoryOptions, 'dbPath' | 'env' | 'homeDir' | 'platform'>): string {
  if (options.dbPath !== undefined) return absolutePath(options.dbPath, '--db-path');
  return resolveGooseHistoryDatabasePath(options);
}

function resolveStatePath(options: Pick<GooseHistoryOptions, 'statePath' | 'env' | 'homeDir' | 'platform'>): string {
  if (options.statePath !== undefined) return absolutePath(options.statePath, '--state-path');
  const env = options.env ?? process.env;
  const homeDir = options.homeDir ?? os.homedir();
  const platform = options.platform ?? process.platform;
  const configuredHome = env.THREADFORGE_HOME?.trim();
  let cacheRoot: string;
  if (configuredHome) {
    if (!path.isAbsolute(configuredHome)) throw new Error('THREADFORGE_HOME must be absolute');
    cacheRoot = path.join(configuredHome, 'state');
  } else if (platform === 'win32') {
    const localAppData = env.LOCALAPPDATA?.trim();
    cacheRoot = path.join(localAppData && path.isAbsolute(localAppData) ? localAppData : path.join(homeDir, 'AppData', 'Local'), 'threadforge', 'state');
  } else if (platform === 'darwin') cacheRoot = path.join(homeDir, 'Library', 'Caches', 'threadforge', 'state');
  else {
    const xdg = env.XDG_CACHE_HOME?.trim();
    cacheRoot = path.join(xdg && path.isAbsolute(xdg) ? xdg : path.join(homeDir, '.cache'), 'threadforge', 'state');
  }
  return path.join(cacheRoot, stateFileName);
}

function absolutePath(value: string, option: string): string {
  if (!path.isAbsolute(value)) throw new Error(`${option} must be absolute`);
  return path.resolve(value);
}

function boundedInteger(value: number, min: number, max: number, name: string): number {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw new Error(`${name} must be an integer between ${min} and ${max}`);
  return value;
}

function positiveInteger(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && Number(value) > 0 ? Number(value) : undefined;
}

function nonNegativeInteger(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && Number(value) >= 0 ? Number(value) : undefined;
}

function boundedString(value: unknown, max: number): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined;
}

function sha256(value: string): string { return createHash('sha256').update(value).digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function samePath(a: string, b: string): boolean { return process.platform === 'win32' ? a.toLowerCase() === b.toLowerCase() : a === b; }
function sameFile(a: Stats, b: Stats): boolean {
  return a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeMs === b.mtimeMs;
}

function cursorConflict(result: GooseHistoryResult, sessionHash: string, message: string): void {
  result.cursor_conflicts++;
  result.errors.push({ session_sha256: sessionHash, message });
}

function toTimestamp(value: unknown): string {
  let date: Date | undefined;
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) {
    date = new Date(value > 10_000_000_000 ? value : value * 1000);
  } else if (typeof value === 'string') date = new Date(value);
  return date && Number.isFinite(date.getTime()) ? date.toISOString() : '1970-01-01T00:00:00.000Z';
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
