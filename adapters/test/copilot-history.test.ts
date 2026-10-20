import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getCopilotHistoryStatus, syncCopilotHistory } from '../src/copilot-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-history-')); }

async function session(root: string, id = 'private-session-id', rows: unknown[] = []): Promise<{ copilotDir: string; eventsPath: string }> {
  const copilotDir = path.join(root, '.copilot');
  const dir = path.join(copilotDir, 'session-state', id);
  await mkdir(dir, { recursive: true });
  const eventsPath = path.join(dir, 'events.jsonl');
  await writeFile(eventsPath, rows.map((row) => JSON.stringify(row)).join('\n') + (rows.length ? '\n' : ''), 'utf8');
  return { copilotDir, eventsPath };
}

function record(id: string, type: string, data: Record<string, unknown>, timestamp = 1_777_000_000_000) {
  return { id, timestamp, type, data };
}

function fixtureRows() {
  return [
    record('private-start-id', 'session.start', {
      sessionId: 'private-session-id', copilotVersion: '1.2.3', selectedModel: 'model-safe',
      context: { cwd: 'C:/private/repo', repository: 'private-owner/private-repo', branch: 'private-branch' },
    }),
    record('private-user-id', 'user.message', { content: 'private prompt body' }),
    record('private-bash-start', 'tool.execution_start', {
      toolCallId: 'private-call-bash', toolName: 'bash', arguments: { command: 'TOKEN=private', description: 'private command' },
    }),
    record('private-bash-result', 'tool.execution_complete', {
      toolCallId: 'private-call-bash', success: true, result: { content: 'private command output' },
    }),
    record('private-read-start', 'tool.execution_start', {
      toolCallId: 'private-call-read', toolName: 'view', arguments: { path: 'C:/private/repo/secret.txt' },
    }),
    record('private-read-result', 'tool.execution_complete', {
      toolCallId: 'private-call-read', success: true, result: { content: 'private file body' },
    }),
    record('private-assistant-id', 'assistant.message', {
      content: 'private assistant response', messageId: 'private-message-id', model: 'model-safe', outputTokens: 20,
      toolRequests: [{ toolCallId: 'unused-call', toolName: 'mcp__private_server__lookup', arguments: { query: 'private query' } }],
    }),
    record('private-shutdown-id', 'session.shutdown', {
      modelMetrics: { 'model-safe': { usage: { inputTokens: 100, outputTokens: 20, cacheReadTokens: 2, cacheWriteTokens: 3, reasoningTokens: 5 } } },
    }),
  ];
}

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

test('preview maps Copilot CLI history with digest-only content and no raw ids, paths, args, or text', async () => {
  const root = await tempRoot();
  const { copilotDir } = await session(root, 'private-directory-id', fixtureRows());
  const statePath = path.join(root, 'threadforge', 'copilot-state.json');
  try {
    const result = await syncCopilotHistory({ copilotDir, statePath, preview: true });
    assert.equal(result.complete, true);
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'tool.invoked', 'command.executed', 'tool.invoked', 'file.read',
      'agent.message', 'token.usage', 'token.usage',
    ]);
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      'private-directory-id', 'private-session-id', 'private-start-id', 'private-user-id', 'private-call-bash',
      'private-call-read', 'private-message-id', 'C:/private/repo', 'private-owner/private-repo', 'private-branch',
      'private prompt body', 'private assistant response', 'TOKEN=private', 'private command',
      'private command output', 'private file body', 'private query',
    ]) assert.equal(encoded.includes(secret), false, `leaked ${secret}`);
    assert.equal((result.events?.find((event) => event.event.action === 'prompt.submitted')?.content as { included: boolean }).included, false);
    const usageEvents = result.events?.filter((event) => event.event.action === 'token.usage') ?? [];
    assert.equal((usageEvents[0]?.gen_ai as { usage: { output_tokens: number } }).usage.output_tokens, 20);
    const shutdownUsage = (usageEvents[1]?.gen_ai as { usage: Record<string, unknown> }).usage;
    assert.equal(shutdownUsage.output_tokens, undefined);
    assert.equal(shutdownUsage.input_tokens, 100);
    assert.deepEqual(shutdownUsage.cache_read, { input_tokens: 2 });
    assert.deepEqual(shutdownUsage.cache_creation, { input_tokens: 3 });
    assert.deepEqual(shutdownUsage.reasoning, { output_tokens: 5 });
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('sync resumes incomplete trailing JSONL rows and emits each accepted record once', async () => {
  const root = await tempRoot();
  const { copilotDir, eventsPath } = await session(root, 'session-a', fixtureRows().slice(0, 2));
  const statePath = path.join(root, 'threadforge', 'state.json');
  const receiver = await createReceiver();
  try {
    let result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 2);
    result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const appended = JSON.stringify(fixtureRows()[2]);
    await appendFile(eventsPath, appended.slice(0, 30), 'utf8');
    result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.partial_sessions, 1);
    assert.equal(result.events_emitted, 0);
    await appendFile(eventsPath, `${appended.slice(30)}\n`, 'utf8');
    result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events.length, 3);
    const cursor = await readFile(statePath, 'utf8');
    for (const secret of ['session-a', 'private-session-id', 'private-user-id', 'private-call-bash']) assert.equal(cursor.includes(secret), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('malformed complete rows block later records until repaired without losing the good prefix', async () => {
  const root = await tempRoot();
  const { copilotDir, eventsPath } = await session(root, 'session-b', fixtureRows().slice(0, 2));
  const statePath = path.join(root, 'threadforge', 'state.json');
  const receiver = await createReceiver();
  try {
    await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    await appendFile(eventsPath, `{bad}\n${JSON.stringify(record('later-id', 'user.message', { content: 'later prompt' }))}\n`, 'utf8');
    let result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.equal(result.malformed_lines, 1);
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 2);

    const text = await readFile(eventsPath, 'utf8');
    await writeFile(eventsPath, text.replace('{bad}', JSON.stringify(record('repair-row', 'session.model_change', { newModel: 'model-repaired' }))), 'utf8');
    result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true);
    assert.equal(result.events_emitted, 2);
    assert.deepEqual(receiver.events.slice(-2).map((event) => event.event.action), ['session.context', 'prompt.submitted']);
    assert.equal(JSON.stringify(receiver.events).includes('later prompt'), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('prefix rewrites, remote endpoints, and in-source cursor paths fail closed', async () => {
  const root = await tempRoot();
  const { copilotDir, eventsPath } = await session(root, 'session-c', fixtureRows().slice(0, 2));
  const statePath = path.join(root, 'state', 'cursor.json');
  const receiver = await createReceiver();
  try {
    await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    await writeFile(eventsPath, `${JSON.stringify(record('replacement', 'user.message', { content: 'replacement' }))}\n`, 'utf8');
    const result = await syncCopilotHistory({ copilotDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.match(result.errors[0]?.message ?? '', /prefix changed/);
    await assert.rejects(syncCopilotHistory({ copilotDir, statePath, endpoint: 'https://example.org/v1/events' }), /loopback HTTP/);
    await assert.rejects(syncCopilotHistory({ copilotDir, statePath: path.join(copilotDir, 'cursor.json'), preview: true }), /outside the Copilot directory/);
    assert.equal(receiver.events.length, 2);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('opt-in text capture is capped; tool and reasoning content always remain digest-only', async () => {
  const root = await tempRoot();
  const rows = [record('start', 'session.start', { sessionId: 'session-content' }), record('user', 'user.message', { content: 'x'.repeat(4_097) }),
    record('assistant', 'assistant.message', { content: 'visible only when opted in', reasoningText: 'never include reasoning', outputTokens: 4 })];
  const { copilotDir } = await session(root, 'session-content', rows);
  const receiver = await createReceiver();
  try {
    const result = await syncCopilotHistory({ copilotDir, statePath: path.join(root, 'state.json'), endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.complete, true);
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    const assistant = receiver.events.find((event) => event.event.action === 'agent.message');
    const reasoning = receiver.events.find((event) => event.event.action === 'agent.reasoning');
    assert.equal((prompt?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((prompt?.content as { truncated: boolean }).truncated, true);
    assert.equal(assistant?.gen_ai !== undefined, true);
    assert.equal(JSON.stringify(assistant).includes('visible only when opted in'), true);
    assert.equal(JSON.stringify(reasoning).includes('never include reasoning'), false);
    await assert.rejects(syncCopilotHistory({ copilotDir, preview: true, includeContent: true }), /cannot be combined/);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('status counts sessions without exposing their identifiers and reports scan overflow', async () => {
  const root = await tempRoot();
  const first = await session(root, 'session-one', [record('one', 'user.message', { content: 'secret' })]);
  await session(root, 'session-two', [record('two', 'user.message', { content: 'secret' })]);
  try {
    const status = await getCopilotHistoryStatus({ copilotDir: first.copilotDir, statePath: path.join(root, 'missing-state.json'), maxSessions: 1 });
    assert.deepEqual(status, {
      schema_version: 'threadforge.copilot-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: true,
    });
    await assert.rejects(syncCopilotHistory({ copilotDir: first.copilotDir, maxSessions: 5_001, preview: true }), /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('symlinked Copilot session directories and transcript files are skipped', async (t) => {
  const root = await tempRoot();
  const outside = await tempRoot();
  const { copilotDir } = await session(root, 'real-session', [record('id', 'user.message', { content: 'secret' })]);
  const sessionRoot = path.join(copilotDir, 'session-state');
  await mkdir(path.join(outside, 'foreign-session'), { recursive: true });
  await writeFile(path.join(outside, 'foreign-session', 'events.jsonl'), `${JSON.stringify(record('foreign', 'user.message', { content: 'private' }))}\n`, 'utf8');
  try {
    try {
      await symlink(path.join(outside, 'foreign-session'), path.join(sessionRoot, 'linked-session'), 'junction');
      await symlink(path.join(outside, 'foreign-session', 'events.jsonl'), path.join(sessionRoot, 'real-session', 'linked.jsonl'));
    } catch {
      t.skip('symlink creation is unavailable in this environment');
      return;
    }
    const status = await getCopilotHistoryStatus({ copilotDir, statePath: path.join(root, 'cursor.json') });
    assert.equal(status.sessions, 1);
    assert.equal(status.symlinks_skipped, 2);
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});
