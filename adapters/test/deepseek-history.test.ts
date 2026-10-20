import assert from 'node:assert/strict';
import { createZstdCompress } from 'node:zlib';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getDeepSeekHistoryStatus, syncDeepSeekHistory, watchDeepSeekHistory } from '../src/deepseek-history.ts';

const sessionText = [
  { type: 'session', time: '2026-10-09T10:00:00Z', data: { id: 'private-session-001', cwd: 'C:\\private\\workspace' } },
  { type: 'request/header', time: '2026-10-09T10:00:01Z', data: { header: { config: { model: 'deepseek-chat' } } } },
  { type: 'user/message', time: '2026-10-09T10:00:02Z', data: { message: { content: 'private prompt sk-012345678901234567890123' } } },
  { type: 'assistant/message', time: '2026-10-09T10:00:03Z', data: { message: {
    source: { model: 'deepseek-chat' },
    content: [{ type: 'reasoning', text: 'private chain of thought' }, { type: 'text', text: 'private assistant answer' }],
    usage: { inputTokens: 12, outputTokens: 8, cacheReadTokens: 4, reasoningTokens: 2 },
  } } },
  { type: 'tool/call', time: '2026-10-09T10:00:04Z', data: { callId: 'private-call-1', name: 'bash', arguments: { command: 'echo API_KEY=private-value' } } },
  { type: 'tool/result', time: '2026-10-09T10:00:05Z', data: { callId: 'private-call-1', toolName: 'bash', content: 'private output [exit code: 0]' } },
  { type: 'tool/call', time: '2026-10-09T10:00:06Z', data: { callId: 'private-call-2', name: 'write', arguments: { file_path: 'C:\\private\\workspace\\.env', content: 'TOKEN=private-file-content' } } },
  { type: 'tool/result', time: '2026-10-09T10:00:07Z', data: { callId: 'private-call-2', toolName: 'write', content: 'written' } },
].map((record) => JSON.stringify(record)).join('\n') + '\n';

async function fixture(root: string, fileName = 'session.v3.jsonl.zstd', contents = sessionText): Promise<string> {
  const sessionDir = path.join(root, 'sessions', 'workspace-key', 'private-session-dir');
  await mkdir(sessionDir, { recursive: true });
  const filePath = path.join(sessionDir, fileName);
  if (fileName.endsWith('.zstd')) await writeFile(filePath, await compress(Buffer.from(contents)));
  else await writeFile(filePath, contents, 'utf8');
  return path.join(root, 'sessions');
}

async function compress(input: Buffer): Promise<Buffer> {
  const encoder = createZstdCompress();
  const chunks: Buffer[] = [];
  const collected = (async () => {
    for await (const chunk of encoder) chunks.push(Buffer.from(chunk as Buffer));
  })();
  encoder.end(input);
  await collected;
  return Buffer.concat(chunks);
}

function acceptedEvents(calls: string[]): typeof fetch {
  return async (_input, init) => {
    calls.push(String(init?.body ?? ''));
    return new Response(null, { status: 202 });
  };
}

test('DeepSeek history reads versioned Zstandard sessions with digest-first event mapping', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-history-'));
  try {
    const sessionsDir = await fixture(root);
    const statePath = path.join(root, 'threadforge-state', 'cursor.json');
    const preview = await syncDeepSeekHistory({ sessionsDir, statePath, preview: true });
    assert.equal(preview.complete, true, JSON.stringify(preview.errors));
    assert.equal(preview.sessions, 1);
    assert.equal(preview.events_emitted, 0);
    assert.ok((preview.events?.length ?? 0) >= 7);
    const serialized = JSON.stringify(preview.events);
    for (const privateValue of [
      'private-session-001', 'private-session-dir', 'C:\\private\\workspace', 'private prompt',
      'private chain of thought', 'private assistant answer', 'private-call-1', 'private-value', 'private output',
      'C:\\private\\workspace\\.env', 'private-file-content', 'private-call-2',
    ]) assert.equal(serialized.includes(privateValue), false, `preview leaked ${privateValue}`);
    assert.match(serialized, /deepseek_harness/);
    assert.match(serialized, /token\.usage/);
    assert.match(serialized, /command\.executed/);
    assert.match(serialized, /"operation":"create"/);
    assert.match(serialized, /file\.modified/);
    assert.match(serialized, /"input_tokens":12/);
    await assert.rejects(readFile(statePath));

    const requests: string[] = [];
    const synced = await syncDeepSeekHistory({ sessionsDir, statePath, fetchImpl: acceptedEvents(requests) });
    assert.equal(synced.complete, true, JSON.stringify(synced.errors));
    assert.equal(synced.events_emitted, preview.events?.length);
    assert.ok(requests.length > 0);
    const requestBody = requests.join('\n');
    for (const privateValue of ['private-session-001', 'private prompt', 'private assistant answer', 'private-value', 'private output']) {
      assert.equal(requestBody.includes(privateValue), false, `default delivery leaked ${privateValue}`);
    }
    const status = await getDeepSeekHistoryStatus({ sessionsDir, statePath });
    assert.equal(status.checkpointed_sessions, 1);
    assert.equal(status.pending_sessions, 0);
    const repeated = await syncDeepSeekHistory({ sessionsDir, statePath, fetchImpl: acceptedEvents([]) });
    assert.equal(repeated.complete, true);
    assert.equal(repeated.events_emitted, 0);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek history includes only opted-in bounded user and assistant text', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-content-'));
  try {
    const sessionsDir = await fixture(root, 'session.v3.jsonl');
    const result = await syncDeepSeekHistory({ sessionsDir, statePath: path.join(root, 'state.json'), includeContent: true, preview: false,
      fetchImpl: async (_input, init) => {
        const events = JSON.parse(String(init?.body)) as Array<Record<string, unknown>>;
        const included = events.filter((event) => (event.content as Record<string, unknown> | undefined)?.included === true);
        assert.ok(included.some((event) => JSON.stringify(event).includes('private prompt')));
        assert.ok(included.some((event) => JSON.stringify(event).includes('private assistant answer')));
        assert.equal(events.some((event) => JSON.stringify(event).includes('private chain of thought')), false);
        assert.equal(events.some((event) => JSON.stringify(event).includes('private output')), false);
        return new Response(null, { status: 202 });
      } });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek history keeps partial JSONL tails pending and reports changed prefixes', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-partial-'));
  try {
    const sessionsDir = await fixture(root, 'session.v3.jsonl', sessionText.slice(0, -1));
    const statePath = path.join(root, 'state.json');
    const first = await syncDeepSeekHistory({ sessionsDir, statePath, fetchImpl: acceptedEvents([]) });
    assert.equal(first.complete, false);
    assert.equal(first.partial_sessions, 1);
    const partialStatus = await getDeepSeekHistoryStatus({ sessionsDir, statePath });
    assert.equal(partialStatus.incomplete_sessions, 1);
    const sessionFile = path.join(sessionsDir, 'workspace-key', 'private-session-dir', 'session.v3.jsonl');
    await writeFile(sessionFile, sessionText.replace('private-session-001', 'private-session-002'), 'utf8');
    const changed = await syncDeepSeekHistory({ sessionsDir, statePath, fetchImpl: acceptedEvents([]) });
    assert.equal(changed.complete, false);
    assert.equal(changed.errors.some((error) => error.message.includes('prefix changed')), true);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek history fails visibly on invalid compressed sessions instead of reporting an empty store', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-corrupt-'));
  try {
    const sessionsDir = await fixture(root);
    const filePath = path.join(sessionsDir, 'workspace-key', 'private-session-dir', 'session.v3.jsonl.zstd');
    await writeFile(filePath, Buffer.from('not a zstd stream'));
    const result = await syncDeepSeekHistory({ sessionsDir, statePath: path.join(root, 'state.json'), preview: true });
    assert.equal(result.sessions, 0);
    assert.equal(result.complete, false);
    assert.equal(result.errors.length, 1);
    assert.equal(result.errors[0]?.source_sha256.length, 64);
    assert.equal(JSON.stringify(result).includes(filePath), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek history rejects remote intake and unsafe state locations', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-bounds-'));
  try {
    const sessionsDir = await fixture(root);
    await assert.rejects(syncDeepSeekHistory({ sessionsDir, statePath: path.join(root, 'state.json'), endpoint: 'https://example.test/v1/events' }), /loopback/);
    await assert.rejects(syncDeepSeekHistory({ sessionsDir, statePath: path.join(sessionsDir, 'cursor.json') }), /outside the native sessions/);
    await assert.rejects(syncDeepSeekHistory({ dshHome: root, statePath: path.join(root, 'cursor.json') }), /outside the DSH_HOME source tree/);
    await assert.rejects(syncDeepSeekHistory({ sessionsDir, statePath: path.join(root, 'state.json'), preview: true, includeContent: true }), /cannot be combined/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek history watch performs an immediate sweep and stops through AbortSignal', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-watch-'));
  try {
    const sessionsDir = path.join(root, 'sessions');
    await mkdir(sessionsDir, { recursive: true });
    const controller = new AbortController();
    const sweeps: boolean[] = [];
    const result = await watchDeepSeekHistory({ sessionsDir, statePath: path.join(root, 'state.json') }, 5_000, (sweep) => {
      sweeps.push(sweep.complete);
      controller.abort();
    }, controller.signal);
    assert.deepEqual(sweeps, [true]);
    assert.deepEqual(result, { sweeps: 1, complete: true });
  } finally { await rm(root, { recursive: true, force: true }); }
});
