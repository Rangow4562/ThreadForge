import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getClaudeHistoryStatus, syncClaudeHistory } from '../src/claude-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-history-')); }

async function fixture(root: string, rows: unknown[]): Promise<{ projectsDir: string; transcript: string }> {
  const projectsDir = path.join(root, 'claude', 'projects');
  const project = path.join(projectsDir, 'private-project');
  await mkdir(project, { recursive: true });
  const transcript = path.join(project, 'private-session.jsonl');
  await writeFile(transcript, rows.map((row) => JSON.stringify(row)).join('\n') + (rows.length ? '\n' : ''), 'utf8');
  return { projectsDir, transcript };
}

function userRow(text = 'private prompt body', extra: Record<string, unknown> = {}) {
  return {
    type: 'user', uuid: 'private-user-uuid', timestamp: '2026-09-24T10:00:00.000Z', sessionId: 'private-session-id',
    message: { role: 'user', content: text }, ...extra,
  };
}

function assistantRow() {
  return {
    type: 'assistant', uuid: 'private-assistant-uuid', timestamp: '2026-09-24T10:00:01.000Z', sessionId: 'private-session-id',
    message: { id: 'private-message-id', model: 'model-safe', content: [
      { type: 'text', text: 'private assistant answer' },
      { type: 'thinking', thinking: 'private reasoning content' },
      { type: 'tool_use', id: 'private-tool-id', name: 'bash', input: { command: 'PRIVATE=secret' } },
    ], usage: { input_tokens: 23, output_tokens: 11 } },
  };
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

test('preview maps Claude transcript events to privacy-minimized Beacon events', async () => {
  const root = await tempRoot();
  const { projectsDir } = await fixture(root, [
    userRow(),
    { ...userRow('private tool result'), message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'private-tool-id', content: 'private tool output' }] } },
    assistantRow(),
    { type: 'summary', uuid: 'private-summary-id', timestamp: '2026-09-24T10:00:02.000Z', sessionId: 'private-session-id', summary: 'private session summary' },
    { ...userRow('private command text'), isMeta: true },
  ]);
  const statePath = path.join(root, 'threadforge', 'state.json');
  try {
    const result = await syncClaudeHistory({ projectsDir, statePath, preview: true });
    assert.equal(result.complete, true);
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'tool.completed', 'tool.invoked', 'agent.message', 'agent.reasoning', 'token.usage', 'session.summary',
    ]);
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      'private-project', 'private-session.jsonl', 'private-session-id', 'private-user-uuid', 'private-message-id',
      'private-tool-id', 'private prompt body', 'private assistant answer', 'private reasoning content',
      'PRIVATE=secret', 'private tool output', 'private session summary', 'private command text',
    ]) assert.equal(encoded.includes(secret), false, `leaked ${secret}`);
    assert.equal((result.events?.find((event) => event.event.action === 'prompt.submitted')?.content as { included: boolean }).included, false);
    assert.equal((result.events?.find((event) => event.event.action === 'agent.reasoning')?.content as { included: boolean }).included, false);
    const usage = result.events?.find((event) => event.event.action === 'token.usage');
    assert.deepEqual((usage?.gen_ai as { usage: unknown }).usage, { input_tokens: 23, output_tokens: 11 });
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('sync resumes partial rows, emits only new events, and stores hashed cursors', async () => {
  const root = await tempRoot();
  const { projectsDir, transcript } = await fixture(root, [userRow()]);
  const statePath = path.join(root, 'threadforge', 'state.json');
  const receiver = await createReceiver();
  try {
    let result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 2);
    result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const appended = JSON.stringify(assistantRow());
    await appendFile(transcript, appended.slice(0, 70), 'utf8');
    result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.partial_sessions, 1);
    assert.equal(result.events_emitted, 0);
    await appendFile(transcript, `${appended.slice(70)}\n`, 'utf8');
    result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 4);
    assert.equal(receiver.events.length, 6);
    assert.equal(JSON.stringify(receiver.events).includes('private assistant answer'), false);
    const cursor = await readFile(statePath, 'utf8');
    for (const secret of ['private-session-id', 'private-user-uuid', 'private-assistant-uuid', 'private-tool-id', 'private-project']) {
      assert.equal(cursor.includes(secret), false);
    }
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('malformed rows stop scanning without checkpointing past them and can be repaired', async () => {
  const root = await tempRoot();
  const { projectsDir, transcript } = await fixture(root, [userRow()]);
  const statePath = path.join(root, 'threadforge', 'state.json');
  const receiver = await createReceiver();
  try {
    await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    await appendFile(transcript, '{broken json}\n', 'utf8');
    await appendFile(transcript, `${JSON.stringify(userRow('later private prompt', { uuid: 'later-user' }))}\n`, 'utf8');
    let result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.equal(result.malformed_lines, 1);
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 2);

    const original = await readFile(transcript, 'utf8');
    await writeFile(transcript, original.replace('{broken json}', JSON.stringify({ type: 'system', sessionId: 'private-session-id' })), 'utf8');
    result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events.at(-1)?.event.action, 'prompt.submitted');
    assert.equal(JSON.stringify(receiver.events).includes('later private prompt'), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('changed transcript prefixes and remote endpoints fail closed', async () => {
  const root = await tempRoot();
  const { projectsDir, transcript } = await fixture(root, [userRow()]);
  const statePath = path.join(root, 'state', 'history.json');
  const receiver = await createReceiver();
  try {
    await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    await writeFile(transcript, `${JSON.stringify(userRow('replacement prompt'))}\n`, 'utf8');
    const result = await syncClaudeHistory({ projectsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.match(result.errors[0]?.message ?? '', /prefix changed/);
    await assert.rejects(syncClaudeHistory({ projectsDir, statePath, endpoint: 'https://example.com/v1/events' }), /loopback HTTP/);
    assert.equal(receiver.events.length, 2);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('opt-in content is capped and oversized requested text is marked truncated', async () => {
  const root = await tempRoot();
  const { projectsDir } = await fixture(root, [userRow('x'.repeat(4_097))]);
  const receiver = await createReceiver();
  try {
    const result = await syncClaudeHistory({ projectsDir, statePath: path.join(root, 'state.json'), endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.complete, true);
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    assert.equal((prompt?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((prompt?.content as { truncated: boolean }).truncated, true);
    assert.equal(prompt?.prompt, undefined);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('status uses only cursor metadata and reports pending sessions', async () => {
  const root = await tempRoot();
  const { projectsDir } = await fixture(root, [userRow()]);
  try {
    const status = await getClaudeHistoryStatus({ projectsDir, statePath: path.join(root, 'missing-state.json') });
    assert.deepEqual(status, {
      schema_version: 'threadforge.claude-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
    });
  } finally { await rm(root, { recursive: true, force: true }); }
});
