import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getFactoryHistoryStatus, syncFactoryHistory } from '../src/factory-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-factory-history-')); }

async function fixture(root: string) {
  const sessionsDir = path.join(root, 'sessions');
  const projectDir = path.join(sessionsDir, '-private-repo');
  const filePath = path.join(projectDir, 'private-session.jsonl');
  const statePath = path.join(root, 'threadforge', 'factory-state.json');
  await mkdir(projectDir, { recursive: true });
  const records = [
    { type: 'session_start', id: 'private-start-id', sessionId: 'private-session', timestamp: '2026-09-25T10:00:00Z', model: 'model-safe' },
    { type: 'message', id: 'private-user-id', sessionId: 'private-session', timestamp: '2026-09-25T10:00:01Z', message: { role: 'user', content: [{ type: 'text', text: 'private prompt text' }] } },
    { type: 'message', id: 'private-assistant-id', sessionId: 'private-session', timestamp: '2026-09-25T10:00:02Z', message: { role: 'assistant', model: 'model-safe', content: [
      { type: 'thinking', thinking: 'private reasoning text' }, { type: 'text', text: 'private assistant answer' },
      { type: 'tool_use', id: 'private-tool-id', name: 'read_file', input: { path: 'C:/private/repo/secret.txt' } },
    ] } },
    { type: 'message', id: 'private-tool-result-row', sessionId: 'private-session', timestamp: '2026-09-25T10:00:03Z', message: { role: 'assistant', content: [
      { type: 'tool_result', tool_use_id: 'private-tool-id', content: 'private tool output' },
    ] } },
    { type: 'todo_state', id: 'private-todo-id', todos: [{ content: 'do not retain' }] },
  ];
  await writeFile(filePath, `${records.map((record) => JSON.stringify(record)).join('\n')}\n`, 'utf8');
  await writeFile(path.join(projectDir, 'private-session.settings.json'), JSON.stringify({
    model: 'model-safe', providerLock: 'private-provider-secret',
    tokenUsage: { inputTokens: 11, outputTokens: 7, cacheCreationTokens: 3, cacheReadTokens: 5, thinkingTokens: 2, factoryCredits: 0.125 },
    childInclusiveTokenUsageBySessionId: { 'private-child-id': { accessToken: 'private-child-token' } },
    unknownSetting: 'private-settings-secret',
  }), 'utf8');
  return { sessionsDir, projectDir, filePath, statePath, records };
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

test('Factory history preview maps session activity and settings without retaining private content', async () => {
  const root = await tempRoot();
  const { sessionsDir, statePath } = await fixture(root);
  try {
    const result = await syncFactoryHistory({ sessionsDir, statePath, preview: true });
    assert.equal(result.complete, true);
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'agent.reasoning', 'agent.message', 'tool.invoked', 'tool.completed', 'token.usage',
    ]);
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      '-private-repo', 'private-session.jsonl', 'private-start-id', 'private-user-id', 'private-assistant-id',
      'private-tool-id', 'private-tool-result-row', 'private prompt text', 'private reasoning text',
      'private assistant answer', 'C:/private/repo/secret.txt', 'private tool output', 'private-provider-secret',
      'private-child-id', 'private-child-token', 'private-settings-secret',
    ]) assert.equal(encoded.includes(secret), false, `preview leaked ${secret}`);
    assert.deepEqual((result.events?.at(-1)?.gen_ai as { usage: Record<string, unknown> }).usage, {
      input_tokens: 11, output_tokens: 7, cache_read_input_tokens: 5, cache_creation_input_tokens: 3,
      reasoning_tokens: 2, factory_credits: 0.125,
    });
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Factory history checkpoints only after loopback acceptance, resumes appends, and does not replay settings', async () => {
  const root = await tempRoot();
  const { sessionsDir, statePath, filePath, projectDir } = await fixture(root);
  const receiver = await createReceiver();
  try {
    let result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 7);
    assert.equal(receiver.events.length, 7);
    assert.equal(receiver.events.find((event) => event.event.action === 'prompt.submitted')?.prompt !== undefined, false);
    const cursor = JSON.parse(await readFile(statePath, 'utf8')) as { files: Record<string, Record<string, unknown>> };
    const cursorJson = JSON.stringify(cursor);
    assert.equal(cursorJson.includes('private-session'), false);
    assert.equal(cursorJson.includes('private-provider-secret'), false);
    assert.equal(cursorJson.includes('private-settings-secret'), false);
    assert.match(JSON.stringify(cursor.files), /settings_fingerprint/);

    result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 7);

    const tail = JSON.stringify({ type: 'message', id: 'private-appended-id', sessionId: 'private-session', message: { role: 'assistant', content: [{ type: 'text', text: 'appended assistant response' }] } });
    await appendFile(filePath, tail.slice(0, 20), 'utf8');
    result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, false);
    assert.equal(result.partial_sessions, 1);
    assert.equal(receiver.events.length, 7);
    await appendFile(filePath, `${tail.slice(20)}\n`, 'utf8');
    result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 1);

    await writeFile(path.join(projectDir, 'private-session.settings.json'), JSON.stringify({
      model: 'model-safe', providerLock: 'rotated-provider-secret', tokenUsage: { inputTokens: 21, outputTokens: 8 },
    }), 'utf8');
    const pendingSettings = await getFactoryHistoryStatus({ sessionsDir, statePath });
    assert.equal(pendingSettings.pending_sessions, 1);
    result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events.at(-1)?.event.action, 'token.usage');
    assert.equal(JSON.stringify(receiver.events.at(-1)).includes('rotated-provider-secret'), false);
    result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const status = await getFactoryHistoryStatus({ sessionsDir, statePath });
    assert.equal(status.sessions, 1);
    assert.equal(status.checkpointed_sessions, 1);
    assert.equal(status.pending_sessions, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Factory history opt-in captures only bounded user and assistant text', async () => {
  const root = await tempRoot();
  const { sessionsDir, statePath, filePath } = await fixture(root);
  const receiver = await createReceiver();
  try {
    const oversizedText = 'x'.repeat(4_097);
    await appendFile(filePath, `${JSON.stringify({ type: 'message', id: 'private-large-prompt', message: { role: 'user', content: [{ type: 'text', text: oversizedText }] } })}\n`, 'utf8');
    const result = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(receiver.events.find((event) => event.event.action === 'prompt.submitted')?.prompt !== undefined, true);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('private reasoning text')), false);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('private tool output')), false);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('C:/private/repo/secret.txt')), false);
    const largePrompt = receiver.events.find((event) => (event.content as { bytes?: number } | undefined)?.bytes === 4_097);
    assert.ok(largePrompt);
    assert.equal((largePrompt.content as { truncated?: boolean }).truncated, true);
    assert.equal(largePrompt.prompt, undefined);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Factory history refuses rewritten prefixes and reports bounded session truncation', async () => {
  const root = await tempRoot();
  const { sessionsDir, statePath, filePath } = await fixture(root);
  const receiver = await createReceiver();
  try {
    await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    const original = (await readFile(filePath, 'utf8')).split('\n');
    original[0] = JSON.stringify({ type: 'session_start', id: 'changed-id', sessionId: 'private-session' });
    await writeFile(filePath, original.join('\n'), 'utf8');
    const conflict = await syncFactoryHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(conflict.complete, false);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(receiver.events.length, 7);

    const otherProject = path.join(sessionsDir, '-other-project');
    await mkdir(otherProject, { recursive: true });
    await writeFile(path.join(otherProject, 'other-session.jsonl'), `${JSON.stringify({ type: 'session_start', sessionId: 'other' })}\n`, 'utf8');
    const limited = await syncFactoryHistory({ sessionsDir, statePath: path.join(root, 'other-state.json'), preview: true, maxSessions: 1 });
    assert.equal(limited.sessions, 1);
    assert.equal(limited.sessions_truncated, true);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Factory history skips symlinked sessions and rejects non-loopback intake endpoints', async (t) => {
  const root = await tempRoot();
  const { sessionsDir, projectDir, filePath } = await fixture(root);
  try {
    try { await symlink(filePath, path.join(projectDir, 'linked-session.jsonl')); }
    catch { t.skip('symlink creation is unavailable in this environment'); return; }
    const status = await getFactoryHistoryStatus({ sessionsDir });
    assert.equal(status.sessions, 1);
    assert.equal(status.symlinks_skipped, 1);
    await assert.rejects(syncFactoryHistory({ sessionsDir, endpoint: 'https://example.com/v1/events' }), /loopback/);
    await assert.rejects(syncFactoryHistory({ sessionsDir, statePath: path.join(sessionsDir, 'cursor.json'), preview: true }), /outside Factory sessions/);
    await assert.rejects(getFactoryHistoryStatus({ sessionsDir, statePath: path.join(sessionsDir, 'cursor.json') }), /outside Factory sessions/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
