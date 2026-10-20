import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { DatabaseSync } from 'node:sqlite';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getCursorHistoryStatus, syncCursorHistory } from '../src/cursor-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-history-')); }

async function transcript(root: string, name = 'session-1.jsonl', records: unknown[] = []) {
  const projectsDir = path.join(root, 'projects');
  const transcriptDir = path.join(projectsDir, '-private-project', 'agent-transcripts');
  await mkdir(transcriptDir, { recursive: true });
  const filePath = path.join(transcriptDir, name);
  await writeFile(filePath, records.map((record) => JSON.stringify(record)).join('\n') + (records.length ? '\n' : ''), 'utf8');
  return { projectsDir, filePath };
}

function user(id = 'private-user-id', text = 'private prompt body') {
  return { type: 'user_message', id, text, timestamp: '2026-09-24T10:00:00.000Z' };
}

function assistant() {
  return {
    type: 'message', id: 'private-assistant-id', timestamp: '2026-09-24T10:00:01.000Z',
    contextTokens: 42, contextWindowSize: 100,
    message: { role: 'assistant', model: 'model-safe', content: [
      { type: 'reasoning', text: 'private reasoning content' }, { type: 'text', text: 'private assistant answer' },
    ] },
  };
}

function toolRows() {
  return [
    { type: 'tool_call', id: 'private-call-row', toolCall: { id: 'private-call-id', name: 'read_file', args: { path: 'C:/private/repo/secret.txt' } } },
    { type: 'tool_result', id: 'private-result-row', toolResult: { callId: 'private-call-id', result: 'private tool output', status: 'success' } },
  ];
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

function createComposerDatabase(databasePath: string, entries: Array<[string, string]>): void {
  const database = new DatabaseSync(databasePath);
  try {
    database.exec('CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value BLOB)');
    const insert = database.prepare('INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)');
    for (const [key, value] of entries) insert.run(key, Buffer.from(value, 'utf8'));
  } finally { database.close(); }
}

function composerEntries() {
  return [
    ['composerData:private-composer-id', JSON.stringify({
      composerId: 'private-composer-id', name: 'private title', text: 'private metadata text',
      createdAt: '2026-10-07T10:00:00.000Z', lastUpdatedAt: '2026-10-07T10:00:05.000Z',
      modelConfig: { modelName: 'gpt-5.5' }, context_tokens: 75, context_window_size: 100,
      fullConversationHeadersOnly: [
        { bubbleId: 'private-user-bubble', type: 1 }, { bubbleId: 'private-agent-bubble', type: 2 },
        { bubbleId: 'private-tool-bubble', type: 2 }, { bubbleId: 'private-error-bubble', type: 2 },
      ],
      latestConversationSummary: { summary: { truncationLastBubbleIdInclusive: 'private-agent-bubble', summary: 'private compaction text' } },
    })],
    ['bubbleId:private-composer-id:private-user-bubble', JSON.stringify({ type: 1, text: 'private prompt text', createdAt: '2026-10-07T10:00:01.000Z' })],
    ['bubbleId:private-composer-id:private-agent-bubble', JSON.stringify({
      type: 2, text: 'private assistant text', createdAt: '2026-10-07T10:00:02.000Z',
      allThinkingBlocks: [{ thinking: 'private reasoning text' }],
    })],
    ['bubbleId:private-composer-id:private-tool-bubble', JSON.stringify({
      type: 2, createdAt: '2026-10-07T10:00:03.000Z',
      toolFormerData: { name: 'run_terminal_cmd', params: '{"command":"private command"}', result: 'private tool output', status: 'success' },
    })],
    ['bubbleId:private-composer-id:private-error-bubble', JSON.stringify({
      type: 2, createdAt: '2026-10-07T10:00:04.000Z', errorDetails: { title: 'private error title', message: 'private error message' },
    })],
  ] as Array<[string, string]>;
}

test('preview maps Cursor agent transcripts without retaining prompts, reasoning, ids, paths, or tool payloads', async () => {
  const root = await tempRoot();
  const { projectsDir } = await transcript(root, 'private-session.jsonl', [user(), assistant(), ...toolRows()]);
  const statePath = path.join(root, 'threadforge', 'cursor-state.json');
  try {
    const result = await syncCursorHistory({ projectsDir, statePath, preview: true });
    assert.equal(result.complete, true);
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'prompt.submitted', 'agent.reasoning', 'agent.message', 'tool.invoked', 'file.read',
    ]);
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      '-private-project', 'private-session.jsonl', 'private-user-id', 'private-assistant-id', 'private-call-row',
      'private-result-row', 'private-call-id', 'private prompt body', 'private reasoning content',
      'private assistant answer', 'C:/private/repo/secret.txt', 'private tool output',
    ]) assert.equal(encoded.includes(secret), false, `leaked ${secret}`);
    const reasoning = result.events?.find((event) => event.event.action === 'agent.reasoning');
    assert.equal((reasoning?.content as { included: boolean }).included, false);
    const assistantEvent = result.events?.find((event) => event.event.action === 'agent.message');
    assert.deepEqual((assistantEvent?.gen_ai as { context: unknown }).context, { used_tokens: 42, limit_tokens: 100 });
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('read-only Composer polling maps committed bubbles, hashes cursors, and withholds private text by default', async () => {
  const root = await tempRoot();
  const globalDbPath = path.join(root, 'state.vscdb');
  const projectsDir = path.join(root, 'missing-projects');
  const statePath = path.join(root, 'threadforge', 'cursor-state.json');
  createComposerDatabase(globalDbPath, composerEntries());
  const originalDatabase = await readFile(globalDbPath);
  try {
    const preview = await syncCursorHistory({ globalDbPath, projectsDir, statePath, preview: true });
    assert.equal(preview.complete, true);
    assert.equal(preview.global_db_present, true);
    assert.equal(preview.composer_sessions, 1);
    assert.deepEqual(preview.events?.map((event) => event.event.action), [
      'prompt.submitted', 'agent.reasoning', 'agent.message', 'tool.invoked', 'command.executed', 'tool.failed', 'session.compacting',
    ]);
    const encoded = JSON.stringify(preview.events);
    for (const secret of [
      'private-composer-id', 'private title', 'private metadata text', 'private-user-bubble', 'private-agent-bubble',
      'private-tool-bubble', 'private-error-bubble', 'private prompt text', 'private assistant text',
      'private reasoning text', 'private command', 'private tool output', 'private error title',
      'private error message', 'private compaction text', globalDbPath,
    ]) assert.equal(encoded.includes(secret), false, `leaked ${secret}`);
    await assert.rejects(readFile(statePath), /ENOENT/);
    assert.deepEqual(await readFile(globalDbPath), originalDatabase, 'Composer database must remain unchanged');
    assert.deepEqual(await readdir(root), ['state.vscdb'], 'read-only Composer polling must not create source sidecars');

    const receiver = await createReceiver();
    try {
      const result = await syncCursorHistory({ globalDbPath, projectsDir, statePath, endpoint: receiver.endpoint });
      assert.equal(result.complete, true);
      assert.equal(result.events_emitted, 7);
      const status = await getCursorHistoryStatus({ globalDbPath, projectsDir, statePath });
      assert.equal(status.composer_sessions, 1);
      assert.equal(status.composer_checkpointed_sessions, 1);
      assert.equal(status.composer_pending_sessions, 0);
      const repeated = await syncCursorHistory({ globalDbPath, projectsDir, statePath, endpoint: receiver.endpoint });
      assert.equal(repeated.events_emitted, 0);
      assert.equal(receiver.events.length, 7);
      const cursor = await readFile(statePath, 'utf8');
      for (const secret of ['private-composer-id', 'private-user-bubble', 'private prompt text', globalDbPath]) {
        assert.equal(cursor.includes(secret), false, `cursor leaked ${secret}`);
      }
      const mutateDatabase = new DatabaseSync(globalDbPath);
      try { mutateDatabase.exec("DELETE FROM cursorDiskKV WHERE key >= 'composerData:' AND key < 'composerData;'"); }
      finally { mutateDatabase.close(); }
      const missing = await syncCursorHistory({ globalDbPath, projectsDir, statePath, endpoint: receiver.endpoint });
      assert.equal(missing.complete, false);
      assert.equal(missing.composer_missing_sessions, 1);
      assert.match(missing.errors[0]?.message ?? '', /no longer present/);
    } finally { await receiver.close(); }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Composer content needs bounded opt-in and previously processed bubble edits fail closed', async () => {
  const root = await tempRoot();
  const globalDbPath = path.join(root, 'state.vscdb');
  const projectsDir = path.join(root, 'missing-projects');
  const statePath = path.join(root, 'threadforge', 'cursor-state.json');
  createComposerDatabase(globalDbPath, composerEntries());
  const receiver = await createReceiver();
  try {
    const updatedDatabase = new DatabaseSync(globalDbPath);
    try {
      updatedDatabase.prepare('UPDATE cursorDiskKV SET value = ? WHERE key = ?').run(
        Buffer.from(JSON.stringify({ type: 1, text: 'p'.repeat(4_097), createdAt: '2026-10-07T10:00:01.000Z' }), 'utf8'),
        'bubbleId:private-composer-id:private-user-bubble',
      );
    } finally { updatedDatabase.close(); }
    const first = await syncCursorHistory({ globalDbPath, projectsDir, statePath, endpoint: receiver.endpoint, includeContent: true });
    assert.equal(first.complete, true);
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    assert.equal((prompt?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((prompt?.content as { truncated: boolean }).truncated, true);
    assert.equal(JSON.stringify(receiver.events.filter((event) => event.event.action === 'agent.reasoning')).includes('private reasoning text'), false);
    assert.equal(JSON.stringify(receiver.events).includes('private tool output'), false);

    const mutateDatabase = new DatabaseSync(globalDbPath);
    try {
      mutateDatabase.prepare('UPDATE cursorDiskKV SET value = ? WHERE key = ?').run(
        Buffer.from(JSON.stringify({ type: 1, text: 'rewritten historical prompt' }), 'utf8'),
        'bubbleId:private-composer-id:private-user-bubble',
      );
      mutateDatabase.prepare('UPDATE cursorDiskKV SET value = ? WHERE key = ?').run(
        Buffer.from(JSON.stringify({ composerId: 'private-composer-id', lastUpdatedAt: '2026-10-07T10:00:06.000Z',
          fullConversationHeadersOnly: [...JSON.parse(composerEntries()[0]![1]).fullConversationHeadersOnly, { bubbleId: 'new-bubble', type: 1 }] }), 'utf8'),
        'composerData:private-composer-id',
      );
      mutateDatabase.prepare('INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)').run(
        'bubbleId:private-composer-id:new-bubble', Buffer.from(JSON.stringify({ type: 1, text: 'new prompt' }), 'utf8'),
      );
    } finally { mutateDatabase.close(); }
    const result = await syncCursorHistory({ globalDbPath, projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.equal(result.events_emitted, 0);
    assert.match(result.errors[0]?.message ?? '', /prefix changed/);
    const status = await getCursorHistoryStatus({ globalDbPath, projectsDir, statePath });
    assert.equal(status.composer_conflicts, 1);
    assert.equal(JSON.stringify(receiver.events).includes('rewritten historical prompt'), false);
    assert.equal(JSON.stringify(receiver.events).includes('new prompt'), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('sync is resumable, idempotent, and waits for a newline-terminated JSONL tail', async () => {
  const root = await tempRoot();
  const { projectsDir, filePath } = await transcript(root, 'session.jsonl', [user()]);
  const statePath = path.join(root, 'threadforge', 'cursor.json');
  const receiver = await createReceiver();
  try {
    let result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 1);
    result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const row = JSON.stringify(assistant());
    await appendFile(filePath, row, 'utf8');
    result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.partial_sessions, 1);
    assert.equal(result.events_emitted, 0);
    await appendFile(filePath, '\n', 'utf8');
    result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 2);
    assert.equal(receiver.events.length, 3);
    assert.equal(JSON.stringify(receiver.events).includes('private assistant answer'), false);
    const cursor = await readFile(statePath, 'utf8');
    for (const secret of ['private-project', 'private-user-id', 'private-assistant-id']) assert.equal(cursor.includes(secret), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('malformed rows stop before bad data and rewritten processed records report a conflict', async () => {
  const root = await tempRoot();
  const { projectsDir, filePath } = await transcript(root, 'session.jsonl', [user()]);
  const statePath = path.join(root, 'state', 'cursor.json');
  const receiver = await createReceiver();
  try {
    await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    await appendFile(filePath, `{bad}\n${JSON.stringify(user('later-user', 'later prompt'))}\n`, 'utf8');
    let result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.equal(result.malformed_records, 1);
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 1);

    const text = await readFile(filePath, 'utf8');
    await writeFile(filePath, text.replace('{bad}', JSON.stringify({ type: 'system_event', id: 'fixed' })), 'utf8');
    result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 2);
    assert.deepEqual(receiver.events.slice(-2).map((event) => event.event.action), ['session.compacting', 'prompt.submitted']);
    assert.equal(JSON.stringify(receiver.events).includes('later prompt'), false);

    await writeFile(filePath, `${JSON.stringify(user('replacement-user', 'replacement prompt'))}\n`, 'utf8');
    result = await syncCursorHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.match(result.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(receiver.events.length, 3);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('invalid UTF-8 is reported as a malformed transcript instead of crashing the parser', async () => {
  const root = await tempRoot();
  const { projectsDir, filePath } = await transcript(root, 'invalid-utf8.jsonl');
  await writeFile(filePath, Buffer.from([0xff, 0xfe]));
  try {
    const result = await syncCursorHistory({ projectsDir, statePath: path.join(root, 'state.json'), preview: true });
    assert.equal(result.complete, false);
    assert.equal(result.malformed_records, 1);
    assert.equal(result.events_previewed, 0);
    assert.match(result.errors[0]?.message ?? '', /malformed record/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('JSON wrapper transcripts are read-only previewable and content requires bounded opt-in', async () => {
  const root = await tempRoot();
  const { projectsDir } = await transcript(root, 'session.json', []);
  const filePath = path.join(projectsDir, '-private-project', 'agent-transcripts', 'session.json');
  await writeFile(filePath, JSON.stringify({ messages: [
    { role: 'user', text: 'visible opt-in prompt' },
    { role: 'assistant', content: [{ type: 'thinking', text: 'never reveal reasoning' }, { type: 'text', text: 'visible assistant' }] },
  ] }), 'utf8');
  const receiver = await createReceiver();
  try {
    let result = await syncCursorHistory({ projectsDir, statePath: path.join(root, 'state.json'), endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.complete, true);
    assert.equal(JSON.stringify(receiver.events).includes('visible opt-in prompt'), true);
    assert.equal(JSON.stringify(receiver.events).includes('visible assistant'), true);
    assert.equal(JSON.stringify(receiver.events).includes('never reveal reasoning'), false);
    const oversized = 'x'.repeat(4_097);
    await writeFile(filePath, JSON.stringify([{ type: 'user_message', text: oversized }]), 'utf8');
    result = await syncCursorHistory({ projectsDir, statePath: path.join(root, 'state2.json'), endpoint: receiver.endpoint, includeContent: true });
    const prompt = receiver.events.at(-1);
    assert.equal((prompt?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((prompt?.content as { truncated: boolean }).truncated, true);
    assert.equal(result.complete, true);
    await assert.rejects(syncCursorHistory({ projectsDir, preview: true, includeContent: true }), /cannot be combined/);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('status uses aggregate metadata and file limits; remote endpoints and project-local cursors are rejected', async () => {
  const root = await tempRoot();
  const { projectsDir } = await transcript(root, 'one.jsonl', [user()]);
  await transcript(root, 'two.jsonl', [user('other')]);
  const receiver = await createReceiver();
  try {
    const status = await getCursorHistoryStatus({ projectsDir, statePath: path.join(root, 'missing.json'), maxFiles: 1 });
    assert.deepEqual(status, {
      schema_version: 'threadforge.cursor-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: true,
      global_db_present: false, composer_sessions: 0, composer_checkpointed_sessions: 0,
      composer_pending_sessions: 0, composer_conflicts: 0, composer_missing_sessions: 0, composers_truncated: false,
    });
    await assert.rejects(syncCursorHistory({ projectsDir, statePath: path.join(projectsDir, 'state.json'), preview: true }), /outside Cursor projects/);
    await assert.rejects(syncCursorHistory({ projectsDir, globalDbPath: path.join(root, 'missing.vscdb'), statePath: path.join(root, 'missing.vscdb') }), /cannot overwrite/);
    await assert.rejects(syncCursorHistory({ projectsDir, endpoint: 'https://example.org/v1/events', preview: true }), /loopback HTTP/);
    await assert.rejects(syncCursorHistory({ projectsDir, maxFiles: 5_001, preview: true }), /between 1 and 5000/);
    await assert.rejects(syncCursorHistory({ projectsDir, maxComposers: 5_001, preview: true }), /between 1 and 5000/);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('symlinked Cursor transcript paths are skipped', async (t) => {
  const root = await tempRoot();
  const outside = await tempRoot();
  const { projectsDir } = await transcript(root, 'normal.jsonl', [user()]);
  const outsideFile = path.join(outside, 'outside.jsonl');
  await writeFile(outsideFile, `${JSON.stringify(user('outside', 'private'))}\n`, 'utf8');
  try {
    const linkedPath = path.join(projectsDir, '-private-project', 'agent-transcripts', 'linked.jsonl');
    try { await symlink(outsideFile, linkedPath); }
    catch { t.skip('symlink creation is unavailable in this environment'); return; }
    const status = await getCursorHistoryStatus({ projectsDir, statePath: path.join(root, 'cursor.json') });
    assert.equal(status.sessions, 1);
    assert.equal(status.symlinks_skipped, 1);
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});
