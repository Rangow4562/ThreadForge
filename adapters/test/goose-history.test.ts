import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { DatabaseSync } from 'node:sqlite';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getGooseHistoryStatus,
  resolveGooseHistoryDatabasePath,
  syncGooseHistory,
} from '../src/goose-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-history-'));
}

function createDatabase(databasePath: string, version = 17): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.exec(`
      CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at TEXT DEFAULT CURRENT_TIMESTAMP);
      INSERT INTO schema_version(version) VALUES (${version});
      CREATE TABLE sessions (
        id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', session_type TEXT NOT NULL DEFAULT 'user',
        working_dir TEXT NOT NULL DEFAULT '', created_at TEXT DEFAULT CURRENT_TIMESTAMP,
        updated_at TEXT DEFAULT CURRENT_TIMESTAMP
      );
      CREATE TABLE messages (
        id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, session_id TEXT NOT NULL,
        role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL,
        metadata_json TEXT
      );
    `);
  } finally { database.close(); }
}

function addSession(databasePath: string, id = 'private-goose-session'): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.prepare('INSERT INTO sessions(id, working_dir) VALUES (?, ?)').run(id, 'C:/private/workspace');
  } finally { database.close(); }
}

function addMessage(databasePath: string, role: string, content: unknown, metadata: unknown = {}, sessionId = 'private-goose-session'): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.prepare(`
      INSERT INTO messages(session_id, role, content_json, created_timestamp, metadata_json)
      VALUES (?, ?, ?, ?, ?)
    `).run(sessionId, role, JSON.stringify(content), 1_760_000_000, JSON.stringify(metadata));
  } finally { database.close(); }
}

function replaceMessage(databasePath: string, id: number, content: unknown): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.prepare('UPDATE messages SET content_json = ? WHERE id = ?').run(JSON.stringify(content), id);
  } finally { database.close(); }
}

function hashFile(databasePath: string): string {
  return createHash('sha256').update(readFileSync(databasePath)).digest('hex');
}

async function createReceiver(): Promise<Receiver> {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on('data', (chunk: Buffer) => chunks.push(chunk));
    request.on('end', () => {
      const value = JSON.parse(Buffer.concat(chunks).toString('utf8')) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(value) ? value : [value]));
      response.writeHead(202).end();
    });
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  return {
    endpoint: `http://127.0.0.1:${address.port}/v1/events`, events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

test('Goose data-root and platform defaults resolve without exposing host paths', () => {
  assert.equal(
    resolveGooseHistoryDatabasePath({ env: { GOOSE_PATH_ROOT: 'D:/GooseData' }, platform: 'win32', homeDir: 'C:/Users/test' }),
    path.join('D:/GooseData', 'data', 'sessions', 'sessions.db'),
  );
  assert.equal(
    resolveGooseHistoryDatabasePath({ env: { XDG_DATA_HOME: '/tmp/data' }, platform: 'linux', homeDir: '/home/test' }),
    path.join('/tmp/data', 'goose', 'sessions', 'sessions.db'),
  );
  assert.equal(
    resolveGooseHistoryDatabasePath({ env: {}, platform: 'darwin', homeDir: '/Users/test' }),
    path.join('/Users/test', 'Library', 'Application Support', 'Block', 'goose', 'data', 'sessions', 'sessions.db'),
  );
  assert.throws(() => resolveGooseHistoryDatabasePath({ env: { GOOSE_PATH_ROOT: 'relative' }, platform: 'linux' }), /absolute/);
});

test('Goose history status handles an absent host database without host access', async () => {
  const root = await tempRoot();
  try {
    const status = await getGooseHistoryStatus({
      dbPath: path.join(root, 'missing.db'), statePath: path.join(root, 'threadforge', 'state.json'),
    });
    assert.equal(status.database_present, false);
    assert.equal(status.errors.length, 0);
    assert.equal(JSON.stringify(status).includes(root), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Goose sync is digest-first, filters hidden/tool messages, resumes idempotently and does not modify source', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  const statePath = path.join(root, 'threadforge', 'state.json');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'private goose prompt' }]);
  addMessage(databasePath, 'assistant', [{ type: 'text', text: 'private goose answer' }]);
  addMessage(databasePath, 'tool', [{ type: 'text', text: 'private tool output' }]);
  addMessage(databasePath, 'assistant', [{ type: 'text', text: 'private hidden content' }], { userVisible: false });
  const before = hashFile(databasePath);
  const receiver = await createReceiver();
  try {
    const first = await syncGooseHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(first.complete, true);
    assert.equal(first.events_emitted, 2);
    assert.equal(hashFile(databasePath), before, 'read-only sync must not modify Goose source');
    assert.equal(receiver.events.length, 2);
    assert.deepEqual(receiver.events.map((event) => event.event.action), ['prompt.submitted', 'agent.message']);
    assert.equal(receiver.events.every((event) => event.harness.name === 'goose'), true);
    assert.equal(JSON.stringify(receiver.events).includes('private goose prompt'), false);
    assert.equal(JSON.stringify(receiver.events).includes('private goose answer'), false);
    assert.equal(JSON.stringify(receiver.events).includes('private tool output'), false);
    assert.equal(JSON.stringify(receiver.events).includes('private hidden content'), false);
    assert.equal(JSON.stringify(receiver.events).includes(databasePath), false);
    const state = await readFile(statePath, 'utf8');
    for (const secret of ['private-goose-session', 'private prompt', databasePath, 'C:/private/workspace']) {
      assert.equal(state.includes(secret), false);
    }

    const second = await syncGooseHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(second.complete, true);
    assert.equal(second.events_emitted, 0);
    addMessage(databasePath, 'user', [{ type: 'text', text: 'a later prompt' }]);
    const third = await syncGooseHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(third.complete, true);
    assert.equal(third.events_emitted, 1);
    assert.equal(receiver.events.length, 3);
    assert.equal(hashFile(databasePath) !== before, true, 'later fixture mutation should change its hash');
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Goose history includes bounded text only with explicit opt-in', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'explicit private prompt' }]);
  const receiver = await createReceiver();
  try {
    const result = await syncGooseHistory({
      dbPath: databasePath, statePath: path.join(root, 'threadforge', 'state.json'),
      endpoint: receiver.endpoint, includeContent: true,
    });
    assert.equal(result.complete, true);
    assert.equal((receiver.events[0]?.content as { included?: boolean }).included, true);
    assert.deepEqual(receiver.events[0]?.prompt, { text: 'explicit private prompt' });
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Goose history preview emits no events and creates no cursor', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  const statePath = path.join(root, 'threadforge', 'state.json');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'preview only' }]);
  try {
    const result = await syncGooseHistory({ dbPath: databasePath, statePath, preview: true });
    assert.equal(result.complete, true);
    assert.equal(result.events_previewed, 1);
    assert.equal(result.events_emitted, 0);
    await assert.rejects(readFile(statePath));
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Goose message anchor changes fail closed without replaying prior history', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  const statePath = path.join(root, 'threadforge', 'state.json');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'original prompt' }]);
  const receiver = await createReceiver();
  try {
    const first = await syncGooseHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(first.events_emitted, 1);
    replaceMessage(databasePath, 1, [{ type: 'text', text: 'rewritten historical prompt' }]);
    const next = await syncGooseHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(next.complete, false);
    assert.equal(next.cursor_conflicts, 1);
    assert.equal(next.events_emitted, 0);
    assert.equal(receiver.events.length, 1);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Goose sessions schema versions outside the audited contract are rejected', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  createDatabase(databasePath, 18);
  try {
    const status = await getGooseHistoryStatus({ dbPath: databasePath, statePath: path.join(root, 'state.json') });
    assert.equal(status.database_present, false);
    assert.equal(status.errors.length, 1);
    assert.equal(JSON.stringify(status).includes(databasePath), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Goose sessions with too many new rows do not advance their cursor', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  const statePath = path.join(root, 'threadforge', 'state.json');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'first' }]);
  addMessage(databasePath, 'assistant', [{ type: 'text', text: 'second' }]);
  const receiver = await createReceiver();
  try {
    const result = await syncGooseHistory({
      dbPath: databasePath, statePath, endpoint: receiver.endpoint, maxMessages: 1,
    });
    assert.equal(result.complete, false);
    assert.equal(result.history_truncated_sessions, 1);
    assert.equal(result.events_emitted, 0);
    const cursor = JSON.parse(await readFile(statePath, 'utf8')) as { sessions: Record<string, unknown> };
    assert.deepEqual(cursor.sessions, {});
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Goose history refuses oversized source messages without advancing the cursor', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'sessions.db');
  const statePath = path.join(root, 'threadforge', 'state.json');
  createDatabase(databasePath);
  addSession(databasePath);
  addMessage(databasePath, 'user', [{ type: 'text', text: 'x'.repeat((1 << 20) + 32) }]);
  try {
    const result = await syncGooseHistory({ dbPath: databasePath, statePath });
    assert.equal(result.complete, false);
    assert.equal(result.events_emitted, 0);
    assert.match(result.errors[0]?.message ?? '', /1 MiB/);
    const cursor = JSON.parse(await readFile(statePath, 'utf8')) as { sessions: Record<string, unknown> };
    assert.deepEqual(cursor.sessions, {});
  } finally { await rm(root, { recursive: true, force: true }); }
});
