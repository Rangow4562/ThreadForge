import { createHash } from 'node:crypto';
import type { Stats } from 'node:fs';
import { lstat, realpath } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
import type { DatabaseSync } from 'node:sqlite';

type JsonObject = Record<string, unknown>;

const composerPrefix = 'composerData:';
const composerEnd = 'composerData;';
const bubblePrefix = 'bubbleId:';
const maxComposers = 5_000;
const maxComposerBytes = 4 << 20;
const maxBubbleBytes = 8 << 20;
const maxTotalBytes = 512 << 20;
const maxBubblesPerComposer = 100_000;
const maxBubblesPerSync = 200_000;

export type CursorComposerBubble = { index: number; records: JsonObject[] };

export type CursorComposerSnapshot = {
  id_sha256: string;
  updated_at_ms: number;
  header_count: number;
  prefix_sha256: string[];
  bubbles: CursorComposerBubble[];
  compaction?: JsonObject;
  compaction_sha256?: string;
};

export type CursorComposerScan = {
  present: boolean;
  composers: CursorComposerSnapshot[];
  malformed: number;
  truncated: boolean;
  errors: Array<{ source_sha256: string; message: string }>;
};

export type CursorComposerOptions = { globalDbPath?: string; maxComposers?: number };

export async function scanCursorComposers(options: CursorComposerOptions = {}): Promise<CursorComposerScan> {
  const databasePath = resolveCursorGlobalDatabase(options.globalDbPath);
  const maximum = boundedInteger(options.maxComposers ?? 1_000, 1, maxComposers, '--max-composers');
  const missing: CursorComposerScan = { present: false, composers: [], malformed: 0, truncated: false, errors: [] };
  const before = await lstat(databasePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!before) return missing;
  if (!before.isFile() || before.isSymbolicLink()) {
    throw new Error('Cursor global database must be a regular non-symlink file');
  }
  const canonical = await realpath(databasePath).catch(() => '');
  if (!canonical || !await isSameRealPathWithoutLinks(canonical, databasePath)) throw new Error('Cursor global database path cannot traverse symbolic links');

  let database: DatabaseSync | undefined;
  try {
    // Node 22.12+ is required so opening Cursor's database is enforced read-only.
    const { DatabaseSync: SQLiteDatabase } = await import('node:sqlite');
    database = new SQLiteDatabase(canonical, { readOnly: true });
    database.exec('PRAGMA busy_timeout=1000');
    database.exec('BEGIN DEFERRED TRANSACTION');
    const after = await lstat(databasePath).catch(() => null);
    if (!after || !after.isFile() || after.isSymbolicLink() || !sameFile(before, after)) {
      throw new Error('Cursor global database changed while it was being opened');
    }
    const table = database.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='cursorDiskKV'").get();
    if (!table) throw new Error('Cursor global database does not contain the documented cursorDiskKV store');
    const composers = database.prepare(
      'SELECT key, value FROM cursorDiskKV WHERE key >= ? AND key < ? ORDER BY key LIMIT ?',
    );
    const bubble = database.prepare('SELECT value FROM cursorDiskKV WHERE key = ?');
    const scan: CursorComposerScan = { ...missing, present: true };
    let totalBytes = 0;
    let totalBubbles = 0;
    const rows = composers.iterate(composerPrefix, composerEnd, maximum + 1);
    for (const row of rows) {
      const key = typeof row.key === 'string' ? row.key : '';
      const id = key.startsWith(composerPrefix) ? key.slice(composerPrefix.length) : '';
      const idHash = sha256(id);
      if (scan.composers.length >= maximum) { scan.truncated = true; break; }
      const composerBytes = bytesOf(row.value);
      totalBytes += composerBytes.byteLength;
      if (totalBytes > maxTotalBytes) { scan.truncated = true; break; }
      if (!id || Buffer.byteLength(id, 'utf8') > 512 || composerBytes.byteLength === 0 || composerBytes.byteLength > maxComposerBytes) {
        scan.malformed++;
        scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer metadata is invalid or exceeds the local size limit' });
        continue;
      }
      let parsed: unknown;
      try { parsed = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(composerBytes)) as unknown; }
      catch {
        scan.malformed++;
        scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer metadata is malformed' });
        continue;
      }
      if (!isRecord(parsed) || !Array.isArray(parsed.fullConversationHeadersOnly) ||
          parsed.fullConversationHeadersOnly.length > maxBubblesPerComposer) {
        scan.malformed++;
        scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer conversation headers are invalid or exceed the local limit' });
        continue;
      }
      const headers: string[] = [];
      let invalidHeader = false;
      for (const item of parsed.fullConversationHeadersOnly) {
        if (!isRecord(item) || typeof item.bubbleId !== 'string' || item.bubbleId.length < 1 ||
            Buffer.byteLength(item.bubbleId, 'utf8') > 512) { invalidHeader = true; break; }
        headers.push(item.bubbleId);
      }
      if (invalidHeader) {
        scan.malformed++;
        scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer contains an invalid conversation reference' });
        continue;
      }
      if (totalBubbles + headers.length > maxBubblesPerSync) { scan.truncated = true; break; }
      const updatedAt = timeMs(parsed.lastUpdatedAt) || timeMs(parsed.createdAt) || before.mtimeMs;
      const model = isRecord(parsed.modelConfig) ? safeModel(parsed.modelConfig.modelName) : undefined;
      const snapshot: CursorComposerSnapshot = {
        id_sha256: idHash, updated_at_ms: updatedAt, header_count: headers.length,
        prefix_sha256: [sha256('')], bubbles: [],
      };
      const prefix = createHash('sha256');
      let sourceIncomplete = false;
      for (const [index, bubbleId] of headers.entries()) {
        const bubbleRow = bubble.get(`${bubblePrefix}${id}:${bubbleId}`);
        if (!bubbleRow) {
          scan.malformed++;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer references an unavailable bubble; later records were withheld' });
          sourceIncomplete = true;
          break;
        }
        const bubbleBytes = bytesOf(bubbleRow.value);
        if (totalBytes + bubbleBytes.byteLength > maxTotalBytes) {
          scan.truncated = true;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer source data exceeded the local sync byte limit; later records were withheld' });
          sourceIncomplete = true;
          break;
        }
        totalBytes += bubbleBytes.byteLength;
        if (bubbleBytes.byteLength === 0 || bubbleBytes.byteLength > maxBubbleBytes) {
          scan.malformed++;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer bubble is invalid or exceeds the local size limit' });
          sourceIncomplete = true;
          break;
        }
        let decoded: unknown;
        try { decoded = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bubbleBytes)) as unknown; }
        catch {
          scan.malformed++;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer bubble is malformed; later records were withheld' });
          sourceIncomplete = true;
          break;
        }
        if (!isRecord(decoded)) {
          scan.malformed++;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer bubble has an unsupported shape; later records were withheld' });
          sourceIncomplete = true;
          break;
        }
        if (Array.isArray(decoded.allThinkingBlocks) && decoded.allThinkingBlocks.length > 64) {
          scan.malformed++;
          scan.errors.push({ source_sha256: idHash, message: 'Cursor Composer bubble exceeds the bounded reasoning-block limit; later records were withheld' });
          sourceIncomplete = true;
          break;
        }
        prefix.update(Buffer.from(`${Buffer.byteLength(bubbleId, 'utf8')}:${bubbleId}:`, 'utf8'));
        prefix.update(sha256(bubbleBytes));
        snapshot.prefix_sha256.push(prefix.copy().digest('hex'));
        snapshot.bubbles.push({ index: index + 1, records: normalizeBubble(decoded, bubbleId, model) });
        totalBubbles++;
      }
      const summary = isRecord(parsed.latestConversationSummary) && isRecord(parsed.latestConversationSummary.summary)
        ? parsed.latestConversationSummary.summary : undefined;
      if (!sourceIncomplete && summary && typeof summary.truncationLastBubbleIdInclusive === 'string' &&
          summary.truncationLastBubbleIdInclusive.trim()) {
        snapshot.compaction = {
          type: 'system_event', id: 'compaction', timestamp: updatedAt,
          ...(model ? { model } : {}),
          ...(typeof parsed.context_tokens === 'number' ? { contextTokens: parsed.context_tokens } : {}),
          ...(typeof parsed.context_window_size === 'number' ? { contextWindowSize: parsed.context_window_size } : {}),
        };
        snapshot.compaction_sha256 = sha256(JSON.stringify({
          truncationLastBubbleIdInclusive: summary.truncationLastBubbleIdInclusive,
          previousConversationSummaryBubbleID: summary.previousConversationSummaryBubbleID,
          clientShouldStartSendingFromInclusiveBubbleId: summary.clientShouldStartSendingFromInclusiveBubbleId,
        }));
      }
      scan.composers.push(snapshot);
    }
    database.exec('COMMIT');
    return scan;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Cursor global database')) throw error;
    throw new Error('Cursor global database is unreadable or has an unsupported local schema');
  } finally { database?.close(); }
}

export function resolveCursorGlobalDatabase(value?: string): string {
  if (value !== undefined && !path.isAbsolute(value)) throw new Error('--global-db-path must be an absolute path');
  if (value) return path.resolve(value);
  const home = os.homedir();
  if (process.platform === 'win32') {
    const appData = process.env.APPDATA?.trim() || path.join(home, 'AppData', 'Roaming');
    return path.join(appData, 'Cursor', 'User', 'globalStorage', 'state.vscdb');
  }
  if (process.platform === 'darwin') {
    return path.join(home, 'Library', 'Application Support', 'Cursor', 'User', 'globalStorage', 'state.vscdb');
  }
  const configHome = process.env.XDG_CONFIG_HOME?.trim() || path.join(home, '.config');
  if (!path.isAbsolute(configHome)) throw new Error('XDG_CONFIG_HOME must be an absolute path');
  return path.join(configHome, 'Cursor', 'User', 'globalStorage', 'state.vscdb');
}

function normalizeBubble(bubble: JsonObject, bubbleId: string, model?: string): JsonObject[] {
  const context = {
    ...(typeof bubble.context_tokens === 'number' ? { contextTokens: bubble.context_tokens } : {}),
    ...(typeof bubble.contextTokens === 'number' ? { contextTokens: bubble.contextTokens } : {}),
    ...(typeof bubble.context_window_size === 'number' ? { contextWindowSize: bubble.context_window_size } : {}),
    ...(typeof bubble.contextWindowSize === 'number' ? { contextWindowSize: bubble.contextWindowSize } : {}),
  };
  const common = {
    createdAt: bubble.createdAt,
    ...(model ? { model } : {}),
    ...context,
  };
  const type = bubble.type;
  if (type === 1) return typeof bubble.text === 'string'
    ? [{ type: 'user_message', id: bubbleId, text: bubble.text, ...common }] : [];
  if (type !== 2) return [];

  const out: JsonObject[] = [];
  const tool = isRecord(bubble.toolFormerData) ? bubble.toolFormerData : undefined;
  if (tool) {
    const callId = `${bubbleId}:tool`;
    out.push({ type: 'tool_call', id: `${bubbleId}:call`,
      toolCall: { id: callId, name: safeToolName(tool.name), args: tool.params }, ...common });
    const result = toolResultText(tool);
    if (result !== '') out.push({ type: 'tool_result', id: `${bubbleId}:result`,
      toolResult: { callId, toolName: safeToolName(tool.name), result,
        status: tool.status === 'error' ? 'error' : 'success' }, ...common });
  } else {
    const content: JsonObject[] = [];
    if (Array.isArray(bubble.allThinkingBlocks)) {
      for (const block of bubble.allThinkingBlocks) if (isRecord(block) && typeof block.thinking === 'string') {
        content.push({ type: 'reasoning', text: block.thinking });
      }
    }
    if (isRecord(bubble.thinking) && typeof bubble.thinking.text === 'string') {
      content.push({ type: 'reasoning', text: bubble.thinking.text });
    }
    if (typeof bubble.text === 'string') content.push({ type: 'text', text: bubble.text });
    if (content.length > 0) out.push({ type: 'message', id: bubbleId,
      message: { role: 'assistant', model, content }, ...common });
  }
  if (isRecord(bubble.errorDetails) && typeof bubble.errorDetails.message === 'string' && bubble.errorDetails.message.trim()) {
    out.push({ type: 'error', id: `${bubbleId}:error`, ...common });
  }
  return out;
}

function toolResultText(tool: JsonObject): string {
  if (typeof tool.result === 'string') return tool.result;
  if (isRecord(tool.additionalData)) return JSON.stringify({
    ...(typeof tool.additionalData.totalMatches === 'number' ? { totalMatches: tool.additionalData.totalMatches } : {}),
    ...(typeof tool.additionalData.totalFiles === 'number' ? { totalFiles: tool.additionalData.totalFiles } : {}),
  });
  return '';
}

function timeMs(value: unknown): number {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) return value < 1_000_000_000_000 ? value * 1_000 : value;
  if (typeof value === 'string' && value.trim()) {
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed) && parsed > 0) return parsed;
    const numeric = Number(value);
    if (Number.isSafeInteger(numeric) && numeric > 0) return numeric < 1_000_000_000_000 ? numeric * 1_000 : numeric;
  }
  return 0;
}

function safeModel(value: unknown): string | undefined {
  return typeof value === 'string' && /^[a-zA-Z0-9._:/+-]{1,128}$/.test(value) ? value : undefined;
}
function safeToolName(value: unknown): string { return typeof value === 'string' && value.length <= 128 ? value : 'unknown_tool'; }
function bytesOf(value: unknown): Buffer {
  if (typeof value === 'string') return Buffer.from(value, 'utf8');
  if (value instanceof Uint8Array) return Buffer.from(value);
  return Buffer.alloc(0);
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedInteger(value: number, min: number, max: number, name: string): number {
  if (!Number.isInteger(value) || value < min || value > max) throw new Error(`${name} must be between ${min} and ${max}`);
  return value;
}
function sha256(value: string | Buffer | Uint8Array): string { return createHash('sha256').update(value).digest('hex'); }
function sameFile(left: Stats, right: Stats): boolean {
  return left.dev === right.dev && left.ino === right.ino && left.size === right.size && left.mtimeMs === right.mtimeMs;
}
