import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getCodexRolloutStatus, syncCodexRollouts } from '../src/codex-rollouts.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function createHome(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-rollout-'));
}

function fixtureRows(): Record<string, unknown>[] {
  return [
    { type: 'session_meta', timestamp: '2026-09-25T10:00:00Z', payload: { session_id: 'private-session-123', cwd: 'C:\\Users\\person\\private-repo', cli_version: '0.155.1' } },
    { type: 'turn_context', timestamp: '2026-09-25T10:00:01Z', payload: { turn_id: 'private-turn-456', model: 'gpt-6-astra' } },
    { type: 'response_item', timestamp: '2026-09-25T10:00:02Z', payload: { type: 'message', role: 'user', content: 'API_TOKEN=super-secret-value please fix the parser' } },
    { type: 'response_item', timestamp: '2026-09-25T10:00:03Z', payload: { type: 'message', role: 'assistant', content: 'I will inspect the parser.' } },
    { type: 'response_item', timestamp: '2026-09-25T10:00:04Z', payload: { type: 'function_call', call_id: 'private-call-789', name: 'exec', arguments: '{"command":"npm test","env":{"API_TOKEN":"super-secret-value"}}' } },
    { type: 'response_item', timestamp: '2026-09-25T10:00:05Z', payload: { type: 'function_call_output', call_id: 'private-call-789', status: 'completed', output: 'tests passed; API_TOKEN=super-secret-value' } },
    { type: 'token_usage_record', timestamp: '2026-09-25T10:00:06Z', payload: { thread_id: 'private-session-123', turn_id: 'private-turn-456', usage: { input_tokens: 120, cached_input_tokens: 90, cache_write_input_tokens: 10, output_tokens: 8, reasoning_output_tokens: 2 } } },
    { type: 'event_msg', timestamp: '2026-09-25T10:00:07Z', payload: { type: 'task_complete', turn_id: 'private-turn-456', duration_ms: 420, time_to_first_token_ms: 80 } },
  ];
}

async function createRollout(home: string, rows = fixtureRows()): Promise<{ codexHome: string; rolloutPath: string; bytes: string }> {
  const codexHome = path.join(home, '.codex');
  const rolloutPath = path.join(codexHome, 'sessions', '2026', '09', '25', 'rollout-2026-09-25T10-00-00-session.jsonl');
  await mkdir(path.dirname(rolloutPath), { recursive: true });
  const bytes = `${rows.map((row) => JSON.stringify(row)).join('\n')}\n`;
  await writeFile(rolloutPath, bytes, 'utf8');
  return { codexHome, rolloutPath, bytes };
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
      response.end(JSON.stringify({ accepted: Array.isArray(decoded) ? decoded.length : 1 }));
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test receiver has no TCP address');
  return {
    endpoint: `http://127.0.0.1:${address.port}/v1/events`,
    events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

test('previews Codex rollout events with metadata-only content and no cursor writes', async () => {
  const parent = await createHome();
  const { codexHome } = await createRollout(parent);
  const statePath = path.join(parent, 'threadforge', 'state', 'codex-rollouts.v1.json');
  try {
    await writeFile(path.join(codexHome, 'auth.json'), '{"access_token":"must-never-be-read"}', 'utf8');
    const preview = await syncCodexRollouts({ codexHome, statePath, preview: true });
    assert.equal(preview.sessions, 1);
    assert.equal(preview.events_previewed, 7);
    assert.equal(preview.errors.length, 0);
    assert.deepEqual(preview.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'agent.message', 'tool.invoked', 'command.executed', 'token.usage', 'session.status',
    ]);
    const serialized = JSON.stringify(preview.events);
    assert.ok(!serialized.includes('super-secret-value'));
    assert.ok(!serialized.includes('private-session-123'));
    assert.ok(!serialized.includes('private-turn-456'));
    assert.ok(!serialized.includes('private-repo'));
    assert.ok(!serialized.includes('must-never-be-read'));
    const prompt = preview.events?.find((event) => event.event.action === 'prompt.submitted');
    assert.deepEqual(prompt?.content && { retention: (prompt.content as { retention: string }).retention, included: (prompt.content as { included: boolean }).included }, { retention: 'metadata', included: false });
    assert.equal(prompt?.prompt, undefined);
    assert.equal(prompt?.harness.collection_method, 'poll');
    assert.match(prompt?.event.id ?? '', /^[0-9a-f]{64}$/);
    const usage = preview.events?.find((event) => event.event.action === 'token.usage');
    assert.equal((usage?.gen_ai as { usage: { input_tokens: number } }).usage.input_tokens, 20);
    assert.equal((usage?.gen_ai as { usage: { cache_read: { input_tokens: number } } }).usage.cache_read.input_tokens, 90);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('maps token_count usage snapshots without relying on token_usage_record rows', async () => {
  const parent = await createHome();
  const { codexHome } = await createRollout(parent, [
    { type: 'session_meta', timestamp: '2026-09-25T10:00:00Z', payload: { session_id: 'private-session' } },
    {
      type: 'event_msg', timestamp: '2026-09-25T10:00:01Z',
      payload: { type: 'token_count', turn_id: 'private-turn', info: { last_token_usage: { input_tokens: 11, output_tokens: 4, total_tokens: 15 } } },
    },
  ]);
  try {
    const result = await syncCodexRollouts({ codexHome, preview: true });
    assert.equal(result.errors.length, 0, JSON.stringify(result.errors));
    const usage = result.events?.find((event) => event.event.action === 'token.usage');
    assert.ok(usage);
    const totals = (usage.gen_ai as { usage: { input_tokens: number; output_tokens: number } }).usage;
    assert.equal(totals.input_tokens, 11);
    assert.equal(totals.output_tokens, 4);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('sync is resumable, ignores partial tails, and emits only appended complete records', async () => {
  const parent = await createHome();
  const { codexHome, rolloutPath, bytes } = await createRollout(parent);
  const receiver = await createReceiver();
  const statePath = path.join(parent, 'threadforge', 'state', 'codex-rollouts.v1.json');
  try {
    let status = await getCodexRolloutStatus({ codexHome, statePath });
    assert.equal(status.sessions, 1);
    assert.equal(status.pending_sessions, 1);
    assert.equal(status.files[0]?.cursor_line, 0);
    let result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 7);
    assert.equal(result.errors.length, 0);
    status = await getCodexRolloutStatus({ codexHome, statePath });
    assert.equal(status.checkpointed_sessions, 1);
    assert.equal(status.pending_sessions, 0);
    result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const nextRow = JSON.stringify({ type: 'response_item', timestamp: '2026-09-25T10:00:08Z', payload: { type: 'message', role: 'user', content: 'second prompt' } });
    await writeFile(rolloutPath, `${bytes}${nextRow}`, 'utf8');
    result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);
    assert.equal(result.partial_sessions, 1);
    status = await getCodexRolloutStatus({ codexHome, statePath });
    assert.equal(status.pending_sessions, 1);

    await appendFile(rolloutPath, '\n', 'utf8');
    result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 1);
    status = await getCodexRolloutStatus({ codexHome, statePath });
    assert.equal(status.pending_sessions, 0);
    assert.equal(receiver.events.length, 8);
    result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);
  } finally {
    await receiver.close();
    await rm(parent, { recursive: true, force: true });
  }
});

test('content collection requires an explicit flag and is capped before loopback delivery', async () => {
  const parent = await createHome();
  const rows = fixtureRows();
  rows[2] = { type: 'response_item', timestamp: '2026-09-25T10:00:02Z', payload: { type: 'message', role: 'user', content: 'x'.repeat(5000) } };
  const { codexHome } = await createRollout(parent, rows);
  const receiver = await createReceiver();
  const statePath = path.join(parent, 'threadforge', 'state', 'codex-rollouts.v1.json');
  try {
    await assert.rejects(syncCodexRollouts({ codexHome, statePath, preview: true, includeContent: true }), /cannot be combined/);
    const result = await syncCodexRollouts({ codexHome, statePath, endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.errors.length, 0);
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    assert.equal((prompt?.prompt as { text: string }).text.length, 4096);
    assert.equal((prompt?.content as { included: boolean }).included, true);
    assert.equal((prompt?.content as { truncated: boolean }).truncated, true);
  } finally {
    await receiver.close();
    await rm(parent, { recursive: true, force: true });
  }
});

test('fails closed on remote endpoints, malformed cursor state, and stale state locks', async () => {
  const parent = await createHome();
  const { codexHome } = await createRollout(parent);
  const statePath = path.join(parent, 'threadforge', 'state', 'codex-rollouts.v1.json');
  try {
    let result = await syncCodexRollouts({ codexHome, statePath, endpoint: 'https://collector.example/v1/events' });
    assert.equal(result.errors.length, 1);
    assert.equal(result.events_emitted, 0);

    await mkdir(path.dirname(statePath), { recursive: true });
    await writeFile(statePath, '{"schema_version":"wrong"}', 'utf8');
    await assert.rejects(syncCodexRollouts({ codexHome, statePath }), /unsupported schema/);
    await rm(statePath);
    await writeFile(`${statePath}.lock`, 'stale', 'utf8');
    await assert.rejects(syncCodexRollouts({ codexHome, statePath }), /locked/);
    await rm(`${statePath}.lock`);
    await assert.rejects(syncCodexRollouts({ codexHome, statePath: path.join(codexHome, 'state.json') }), /must not be stored inside CODEX_HOME/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});
