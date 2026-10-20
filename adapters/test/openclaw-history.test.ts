import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  buildOpenClawHistoryCommand,
  buildOpenClawSessionListCommand,
  getOpenClawHistoryStatus,
  syncOpenClawHistory,
  type OpenClawHistoryCommand,
} from '../src/openclaw-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-openclaw-history-')); }

async function createReceiver(): Promise<Receiver> {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => { body += chunk; });
    request.on('end', () => {
      const decoded = JSON.parse(body) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(decoded) ? decoded : [decoded]));
      response.writeHead(202, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ accepted: Array.isArray(decoded) ? decoded.length : 1 }));
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

function listing(rows = [{ key: 'agent:main:main', agentId: 'main' }], hasMore = false): string {
  return JSON.stringify({ count: rows.length, totalCount: rows.length, limitApplied: 50, hasMore, sessions: rows });
}

function history(messages: Record<string, unknown>[], hasMore = false): string {
  return JSON.stringify({ sessionKey: 'agent:main:main', messages, hasMore });
}

function runnerFor(state: { listing: string; history: string }, calls: OpenClawHistoryCommand[] = []) {
  return async (command: OpenClawHistoryCommand) => {
    calls.push(command);
    if (command.args[0] === 'sessions') return state.listing;
    return state.history;
  };
}

const originalMessages = [
  { id: 'private-message-1', timestamp: '2026-09-25T10:00:00Z', role: 'user', content: 'secret prompt body' },
  { id: 'private-message-2', timestamp: '2026-09-25T10:00:01Z', role: 'assistant', content: [{ type: 'text', text: 'secret answer body' }, { type: 'thinking', thinking: 'private reasoning' }] },
];

test('builds bounded OpenClaw sessions and documented Gateway history commands', () => {
  assert.deepEqual(buildOpenClawSessionListCommand({ openclawCli: 'openclaw-test', maxSessions: 25 }), {
    executable: 'openclaw-test', args: ['sessions', '--all-agents', '--limit', '25', '--json'],
  });
  const command = buildOpenClawHistoryCommand('agent:main:main', { maxMessages: 100 });
  assert.equal(command.args[0], 'gateway');
  assert.equal(command.args[2], 'chat.history');
  assert.deepEqual(JSON.parse(command.args[4]!), { sessionKey: 'agent:main:main', limit: 100 });
  const nextPage = buildOpenClawHistoryCommand('agent:main:main', { maxMessages: 100, offset: 200, agentId: 'main' });
  assert.deepEqual(JSON.parse(nextPage.args[4]!), { sessionKey: 'agent:main:main', limit: 100, offset: 200, agentId: 'main' });
  assert.throws(() => buildOpenClawHistoryCommand('bad', { maxMessages: 201 }), /between 1 and 200/);
  assert.throws(() => buildOpenClawSessionListCommand({ maxSessions: 201 }), /between 1 and 200/);
});

test('follows bounded nextOffset pages and emits them in transcript order', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const receiver = await createReceiver();
  const calls: OpenClawHistoryCommand[] = [];
  const runner = async (command: OpenClawHistoryCommand) => {
    calls.push(command);
    if (command.args[0] === 'sessions') return listing();
    const params = JSON.parse(command.args[4]!) as { offset?: number; agentId?: string };
    assert.equal(params.agentId, 'main');
    if (params.offset === undefined) {
      return JSON.stringify({
        messages: [{ id: 'newest', timestamp: '2026-09-25T10:00:01Z', role: 'assistant', content: 'newer' }],
        hasMore: true, nextOffset: 1,
      });
    }
    assert.equal(params.offset, 1);
    return JSON.stringify({
      messages: [{ id: 'oldest', timestamp: '2026-09-25T10:00:00Z', role: 'user', content: 'older' }],
      hasMore: false,
    });
  };
  try {
    const result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, maxMessages: 300, commandRunner: runner });
    assert.equal(result.complete, true);
    assert.equal(calls.length, 3);
    assert.deepEqual(receiver.events.map((event) => event.event.action), ['prompt.submitted', 'agent.message']);
    assert.deepEqual(receiver.events.map((event) => event.timestamp), ['2026-09-25T10:00:00.000Z', '2026-09-25T10:00:01.000Z']);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('preview uses metadata-only history and does not create a cursor file', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const state = { listing: listing(), history: history(originalMessages) };
  const calls: OpenClawHistoryCommand[] = [];
  try {
    const result = await syncOpenClawHistory({ statePath, preview: true, commandRunner: runnerFor(state, calls) });
    assert.equal(result.complete, true);
    assert.equal(result.events_previewed, 2);
    assert.deepEqual(result.events?.map((event) => event.event.action), ['prompt.submitted', 'agent.message']);
    assert.equal(calls[0]?.args[0], 'sessions');
    assert.equal(calls[1]?.args[1], 'call');
    const encoded = JSON.stringify(result.events);
    for (const secret of ['agent:main:main', 'private-message-1', 'secret prompt body', 'private reasoning']) {
      assert.equal(encoded.includes(secret), false);
    }
    const prompt = result.events?.[0];
    assert.equal((prompt?.content as { included: boolean }).included, false);
    assert.equal(prompt?.prompt, undefined);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('sync checkpoints only delivered messages and resumes without replaying old history', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const receiver = await createReceiver();
  const state = { listing: listing(), history: history(originalMessages) };
  const runner = runnerFor(state);
  try {
    let result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 2);
    assert.equal(receiver.events.length, 2);
    assert.equal((await getOpenClawHistoryStatus({ statePath })).tracked_sessions, 1);

    result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 2);

    state.history = history([...originalMessages, { id: 'private-message-3', timestamp: '2026-09-25T10:00:02Z', role: 'user', content: 'next private prompt' }]);
    result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events.length, 3);
    assert.equal(receiver.events[2]?.event.action, 'prompt.submitted');

    const cursor = await readFile(statePath, 'utf8');
    for (const secret of ['agent:main:main', 'private-message-3', 'next private prompt']) assert.equal(cursor.includes(secret), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('history truncation and missing cursor anchors are surfaced without advancing', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const receiver = await createReceiver();
  const state = { listing: listing([{ key: 'agent:main:main', agentId: 'main' }], true), history: history(originalMessages, true) };
  try {
    const first = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runnerFor(state) });
    assert.equal(first.complete, false);
    assert.equal(first.sessions_truncated, true);
    assert.equal(first.history_truncated_sessions, 1);
    assert.equal(first.events_emitted, 2);
    const before = await readFile(statePath, 'utf8');

    state.listing = listing();
    state.history = history([{ id: 'replacement-message', timestamp: '2026-09-25T10:01:00Z', role: 'assistant', content: 'do not replay after reset' }]);
    const conflict = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runnerFor(state) });
    assert.equal(conflict.cursor_conflicts, 1);
    assert.equal(conflict.events_emitted, 0);
    assert.equal((await readFile(statePath, 'utf8')).includes('replacement-message'), false);
    assert.notEqual(before, '');
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('content capture is explicit and capped before loopback delivery', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const receiver = await createReceiver();
  const state = { listing: listing(), history: history([
    { id: 'short-message', timestamp: '2026-09-25T10:00:00Z', role: 'user', content: 'explicitly included prompt' },
    { id: 'long-message', timestamp: '2026-09-25T10:00:01Z', role: 'assistant', content: 'x'.repeat(5000) },
  ]) };
  try {
    await assert.rejects(syncOpenClawHistory({ statePath, preview: true, includeContent: true, commandRunner: runnerFor(state) }), /cannot be combined/);
    const result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, includeContent: true, commandRunner: runnerFor(state) });
    assert.equal(result.events_emitted, 2);
    assert.equal((receiver.events[0]?.content as { included: boolean }).included, true);
    assert.equal((receiver.events[0]?.prompt as { text: string }).text, 'explicitly included prompt');
    assert.equal((receiver.events[1]?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((receiver.events[1]?.content as { truncated: boolean }).truncated, true);
    assert.equal(receiver.events[1]?.gen_ai, undefined);
    assert.equal(JSON.stringify(receiver.events).includes('x'.repeat(5000)), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('remote intake is rejected and leaves the cursor unadvanced', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const state = { listing: listing(), history: history([{ id: 'long-message', timestamp: '2026-09-25T10:00:00Z', role: 'user', content: 'x'.repeat(5000) }]) };
  try {
    const failed = await syncOpenClawHistory({ statePath, endpoint: 'https://collector.example/v1/events', includeContent: true, commandRunner: runnerFor(state) });
    assert.equal(failed.errors.length, 1);
    assert.equal(failed.events_emitted, 0);
    const status = await getOpenClawHistoryStatus({ statePath });
    assert.equal(status.tracked_sessions, 0);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('visible history without stable message identifiers fails closed', async () => {
  const root = await tempRoot();
  const statePath = path.join(root, 'state', 'openclaw-history.json');
  const receiver = await createReceiver();
  const state = { listing: listing(), history: history([{ timestamp: '2026-09-25T10:00:00Z', role: 'user', content: 'cannot checkpoint safely' }]) };
  try {
    const result = await syncOpenClawHistory({ statePath, endpoint: receiver.endpoint, commandRunner: runnerFor(state) });
    assert.equal(result.complete, false);
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 0);
    assert.equal((await getOpenClawHistoryStatus({ statePath })).tracked_sessions, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});
