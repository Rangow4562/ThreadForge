import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { DatabaseSync } from 'node:sqlite';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getHermesHistoryStatus, syncHermesHistory } from '../src/hermes-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-hermes-history-')); }

function createDatabase(databasePath: string): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.exec(`
      CREATE TABLE sessions (id TEXT PRIMARY KEY, archived INTEGER NOT NULL DEFAULT 0, started_at REAL, ended_at REAL);
      CREATE TABLE messages (
        id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT,
        tool_call_id TEXT, tool_name TEXT, tool_calls TEXT, timestamp REAL, active INTEGER NOT NULL DEFAULT 1
      );
    `);
    database.prepare('INSERT INTO sessions(id, archived, started_at, ended_at) VALUES (?, 0, ?, NULL)')
      .run('private-hermes-session', 1791536400);
    const insert = database.prepare(`
      INSERT INTO messages(id, session_id, role, content, tool_call_id, tool_name, tool_calls, timestamp, active)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
    `);
    insert.run(1, 'private-hermes-session', 'user', 'private prompt body', null, null, null, 1791536401);
    insert.run(2, 'private-hermes-session', 'assistant', 'private assistant answer', null, null, null, 1791536402);
    insert.run(3, 'private-hermes-session', 'assistant', '', null, null,
      JSON.stringify([{ id: 'private-tool-call', function: { name: 'read_file', arguments: { path: 'private/path' } } }]), 1791536403);
  } finally { database.close(); }
}

async function createReceiver(): Promise<Receiver> {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => { body += chunk; });
    request.on('end', () => {
      const value = JSON.parse(body) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(value) ? value : [value]));
      response.writeHead(202, { 'content-type': 'application/json' });
      response.end('{}');
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test receiver has no TCP address');
  return {
    endpoint: `http://127.0.0.1:${address.port}/v1/events`, events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

function fileHash(data: Buffer): string { return createHash('sha256').update(data).digest('hex'); }

test('Hermes preview is digest-first and read-only; opt-in sync checkpoints hashed session identity idempotently', async () => {
  const root = await tempRoot();
  const databasePath = path.join(root, 'state.db');
  const statePath = path.join(root, 'threadforge', 'hermes-state.json');
  createDatabase(databasePath);
  const sourceHash = fileHash(await readFile(databasePath));
  const receiver = await createReceiver();
  try {
    const preview = await syncHermesHistory({ dbPath: databasePath, statePath, preview: true });
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 3);
    assert.equal(preview.events?.every((event) => event.harness.collection_method === 'poll'), true);
    assert.equal(preview.events?.some((event) => event.event.action === 'prompt.submitted'), true);
    const encoded = JSON.stringify(preview.events);
    for (const secret of ['private-hermes-session', 'private-tool-call', 'private prompt body', 'private assistant answer', 'private/path']) {
      assert.equal(encoded.includes(secret), false, `leaked ${secret}`);
    }
    await assert.rejects(readFile(statePath), { code: 'ENOENT' });

    const synced = await syncHermesHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint, includeContent: true });
    assert.equal(synced.complete, true);
    assert.equal(synced.events_emitted, 3);
    assert.equal(receiver.events.length, 3);
    assert.equal(receiver.events[0]?.prompt && (receiver.events[0].prompt as { text?: string }).text, 'private prompt body');
    const cursor = await readFile(statePath, 'utf8');
    assert.equal(cursor.includes('private-hermes-session'), false);
    assert.equal(cursor.includes('private prompt body'), false);

    const repeated = await syncHermesHistory({ dbPath: databasePath, statePath, endpoint: receiver.endpoint });
    assert.equal(repeated.complete, true);
    assert.equal(repeated.events_emitted, 0);
    assert.equal(fileHash(await readFile(databasePath)), sourceHash);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Hermes history rejects preview with content inclusion and safely reports a missing local store', async () => {
  const root = await tempRoot();
  try {
    await assert.rejects(syncHermesHistory({ preview: true, includeContent: true }), /--print cannot be combined/);
    const status = await getHermesHistoryStatus({
      dbPath: path.join(root, 'missing.db'), statePath: path.join(root, 'private', 'state.json'),
    });
    assert.equal(status.database_present, false);
    assert.equal(status.sessions, 0);
    assert.equal(status.cursor_file_present, false);
    assert.deepEqual(status.errors, []);
  } finally { await rm(root, { recursive: true, force: true }); }
});
