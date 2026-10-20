import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getPiHistoryStatus, syncPiHistory } from '../src/pi-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-pi-history-')); }

function piRecords(): unknown[] {
  return [
    { type: 'session', id: 'private-session-id', timestamp: '2026-09-24T10:00:00.000Z', cwd: 'C:/private/repo', model: 'provider/model-safe', git: { repoUrl: 'https://private.example/repo', branch: 'private-branch' } },
    { type: 'message', timestamp: '2026-09-24T10:00:01.000Z', message: { role: 'user', content: [{ type: 'text', text: 'private prompt text' }] } },
    { type: 'message', timestamp: '2026-09-24T10:00:02.000Z', message: { role: 'assistant', model: 'provider/model-safe', content: [
      { type: 'thinking', thinking: 'private reasoning text' },
      { type: 'text', text: 'private assistant response' },
      { type: 'toolCall', id: 'private-call-id', name: 'read', arguments: { path: 'C:/private/repo/secret.txt' } },
    ], usage: { input: 120, output: 36, cacheRead: 12, cacheWrite: 4, reasoning: 10, cost: { total: 0.012 } } } },
    { type: 'tool_result', toolCallId: 'private-call-id', output: 'private file contents', details: { patch: 'private patch contents' } },
    { type: 'bash_execution', command: 'npm test --secret=private', output: 'private command output', exitCode: 0 },
  ];
}

async function writeTranscript(sessionsDir: string, records = piRecords(), name = 'session-private.jsonl') {
  const file = path.join(sessionsDir, 'nested', name);
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${records.map((record) => JSON.stringify(record)).join('\n')}\n`, 'utf8');
  return file;
}

async function createReceiver(): Promise<Receiver> {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => { body += chunk; });
    request.on('end', () => {
      const parsed = JSON.parse(body) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(parsed) ? parsed : [parsed]));
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

test('Pi preview recursively maps sessions while retaining only digest metadata by default', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'pi', 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  await writeTranscript(sessionsDir);
  try {
    const result = await syncPiHistory({ sessionsDir, statePath, preview: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'agent.reasoning', 'agent.message', 'tool.invoked', 'token.usage', 'file.read', 'command.executed',
    ]);
    assert.equal(result.content_mode, 'metadata');
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      'private-session-id', 'session-private', 'private prompt text', 'private reasoning text', 'private assistant response',
      'private-call-id', 'C:/private/repo', 'private.example/repo', 'private-branch', 'secret.txt', 'private file contents',
      'private patch contents', 'npm test --secret=private', 'private command output',
    ]) assert.equal(encoded.includes(secret), false, `preview leaked ${secret}`);
    assert.equal(result.events?.[0]?.harness.name, 'pi_cli');
    assert.equal((result.events?.[0]?.session as { id?: string } | undefined)?.id?.startsWith('pi:'), true);
    assert.equal((result.events?.find((event) => event.event.action === 'file.read')?.file as { path_sha256?: string }).path_sha256 !== undefined, true);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Pi sync checkpoints a recursive transcript, resumes appended complete lines, and is idempotent', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  const file = await writeTranscript(sessionsDir, piRecords().slice(0, 2));
  const receiver = await createReceiver();
  try {
    let result = await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 2);
    assert.equal((await getPiHistoryStatus({ sessionsDir, statePath })).pending_files, 0);

    const appended = { type: 'message', timestamp: '2026-09-24T10:00:03.000Z', message: { role: 'assistant', content: [{ type: 'text', text: 'later private response' }] } };
    await appendFile(file, `${JSON.stringify(appended)}\n`, 'utf8');
    result = await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events.filter((event) => event.event.action === 'session.started').length, 1);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('later private response')), false);

    result = await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);
    assert.equal((await getPiHistoryStatus({ sessionsDir, statePath })).pending_files, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Pi waits for an incomplete final row without advancing state, then ingests after repair', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  const file = await writeTranscript(sessionsDir, piRecords().slice(0, 2));
  const complete = (await readFile(file, 'utf8')).trimEnd();
  await writeFile(file, `${complete}\n{"type":"message","message":`, 'utf8');
  const receiver = await createReceiver();
  try {
    let result = await syncPiHistory({ sessionsDir, statePath, preview: true });
    assert.equal(result.complete, false);
    assert.equal(result.partial_files, 1);
    assert.equal(result.events_previewed, 2);
    await assert.rejects(readFile(statePath), /ENOENT/);

    await writeFile(file, `${complete}\n${JSON.stringify({ type: 'message', timestamp: '2026-09-24T10:00:03.000Z', message: { role: 'assistant', content: [{ type: 'text', text: 'repaired' }] } })}\n`, 'utf8');
    result = await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 3);
    assert.equal((await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint })).events_emitted, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Pi refuses changed transcript prefixes and non-loopback destinations without cursor advancement', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  const records = piRecords().slice(0, 2) as Array<Record<string, unknown>>;
  const file = await writeTranscript(sessionsDir, records);
  const receiver = await createReceiver();
  try {
    await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    const changed = `${JSON.stringify(records[0])}\n${JSON.stringify({ ...records[1], message: { role: 'user', content: 'rewritten private prompt' } })}\n`;
    await writeFile(file, changed, 'utf8');
    const conflict = await syncPiHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(conflict.complete, false);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('rewritten private prompt')), false);
    await assert.rejects(syncPiHistory({ sessionsDir, statePath, endpoint: 'https://collector.example/v1/events' }), /loopback/);
    assert.equal((await getPiHistoryStatus({ sessionsDir, statePath })).pending_files, 1);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Pi content opt-in includes only bounded prompt and assistant text', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  await writeTranscript(sessionsDir, [
    { type: 'session', id: 'id', timestamp: '2026-09-24T10:00:00Z' },
    { type: 'message', message: { role: 'user', content: 'allowed prompt' } },
    { type: 'message', message: { role: 'assistant', content: [{ type: 'text', text: `large-${'x'.repeat(4_100)}` }] } },
  ]);
  const receiver = await createReceiver();
  try {
    await assert.rejects(syncPiHistory({ sessionsDir, statePath, preview: true, includeContent: true }), /cannot be combined/);
    const optIn = await syncPiHistory({ sessionsDir, statePath, includeContent: true, endpoint: receiver.endpoint });
    assert.equal(optIn.complete, true, JSON.stringify(optIn.errors));
    assert.equal(optIn.content_mode, 'include-content');
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    assert.equal((prompt?.prompt as { text?: string }).text, 'allowed prompt');
    const assistant = receiver.events.find((event) => event.event.action === 'agent.message');
    assert.equal(JSON.stringify(assistant).includes('large-'), false);
    assert.equal(JSON.stringify(assistant).includes('x'.repeat(100)), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Pi file and line limits fail visibly rather than scanning unbounded transcripts', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  await writeTranscript(sessionsDir, piRecords().slice(0, 1));
  try {
    await assert.rejects(getPiHistoryStatus({ sessionsDir, maxFiles: 15_001 }), /between 1 and 15000/);
    const status = await getPiHistoryStatus({ sessionsDir, maxFiles: 1 });
    assert.equal(status.files, 1);
  } finally { await rm(root, { recursive: true, force: true }); }
});
