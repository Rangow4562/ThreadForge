import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getPrimeHistoryStatus, syncPrimeHistory } from '../src/prime-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-prime-history-')); }

function sessionRecords() {
  return [
    { type: 'session', id: 'private-prime-session', timestamp: '2026-09-24T10:00:00.000Z', cwd: 'C:/private/project', model: 'provider/model-safe',
      git: { repoUrl: 'https://private.example/project', branch: 'private-branch' }, parentSession: 'private-parent-session' },
    { type: 'message', timestamp: '2026-09-24T10:00:01.000Z', message: { role: 'user', content: [{ text: 'private prompt' }] } },
    { type: 'message', timestamp: '2026-09-24T10:00:02.000Z', message: { role: 'assistant', content: [
      { type: 'thinking', thinking: 'private reasoning' }, { type: 'text', text: 'private answer' },
      { type: 'tool_call', id: 'private-ipython-call', name: 'ipython', arguments: { code: "bash('private command')" } },
    ], usage: { input: 100, output: 25, cacheRead: 10, cacheWrite: 2, reasoning: 8, cost: { total: 0.01 } } } },
    { type: 'message', timestamp: '2026-09-24T10:00:03.000Z', message: { role: 'tool_result', toolCallId: 'private-ipython-call',
      toolName: 'ipython', content: [{ text: 'private output' }], details: { status: 'ok', attachments: [
        { path: 'C:/private/project/README.md', mimeType: 'text/markdown' },
      ] } } },
    { type: 'compaction', timestamp: '2026-09-24T10:00:04.000Z', summary: 'private compacted context' },
    { type: 'custom', customType: 'prime-agent.refinement', timestamp: '2026-09-24T10:00:05.000Z', data: { summary: 'private refinement state' } },
  ];
}

async function writeTranscript(root: string, records: unknown[], name: string): Promise<string> {
  const file = path.join(root, 'nested', name);
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
  return { endpoint: `http://127.0.0.1:${address.port}/v1/events`, events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())) };
}

test('Prime preview recursively maps sessions and subagent artifacts without retaining private values', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'session-artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  await writeTranscript(sessionsDir, sessionRecords(), 'session-private.jsonl');
  await writeTranscript(artifactsDir, [
    { type: 'session', id: 'private-artifact-session', timestamp: '2026-09-24T11:00:00Z', parentSession: 'private-prime-session' },
    { type: 'message', message: { role: 'user', content: 'private artifact prompt' } },
  ], 'artifact-private.jsonl');
  try {
    const result = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, preview: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.session_files, 1);
    assert.equal(result.artifact_files, 1);
    const actions = result.events?.map((event) => event.event.action) ?? [];
    for (const action of ['session.started', 'prompt.submitted', 'agent.reasoning', 'agent.message', 'tool.invoked', 'token.usage',
      'command.executed', 'file.read', 'session.compacting', 'session.context']) assert.ok(actions.includes(action), `${action} missing`);
    const encoded = JSON.stringify(result.events);
    for (const secret of ['private-prime-session', 'private-artifact-session', 'private-parent-session', 'private-ipython-call',
      'private prompt', 'private reasoning', 'private answer', "bash('private command')", 'private output', 'private compacted context',
      'private refinement state', 'C:/private/project', 'private.example/project', 'private-branch', 'README.md']) {
      assert.equal(encoded.includes(secret), false, `preview leaked ${secret}`);
    }
    assert.ok(result.events?.some((event) => (event.session as { source_kind?: string }).source_kind === 'artifact'));
    assert.ok(result.events?.some((event) => (event.raw as { parent_session_sha256?: string }).parent_session_sha256));
    assert.equal(result.events?.every((event) => event.harness.name === 'prime_agent' && event.harness.collection_method === 'poll'), true);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Prime sync checkpoints both roots, resumes appends once, and reports per-root status', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  const sessionFile = await writeTranscript(sessionsDir, sessionRecords().slice(0, 2), 'session.jsonl');
  await writeTranscript(artifactsDir, [{ type: 'session', id: 'artifact-id', timestamp: '2026-09-24T11:00:00Z' }], 'subagent.jsonl');
  const receiver = await createReceiver();
  try {
    let result = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 3);
    assert.equal((await getPrimeHistoryStatus({ sessionsDir, artifactsDir, statePath })).pending_files, 0);

    await appendFile(sessionFile, `${JSON.stringify({ type: 'message', message: { role: 'assistant', content: [{ type: 'text', text: 'later private answer' }] } })}\n`, 'utf8');
    result = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 1);
    result = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.filter((event) => event.event.action === 'session.started').length, 2);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('later private answer')), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Prime incomplete tails wait for repair and rewritten prefixes report conflicts', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  const file = await writeTranscript(sessionsDir, sessionRecords().slice(0, 2), 'session.jsonl');
  const complete = (await readFile(file, 'utf8')).trimEnd();
  await writeFile(file, `${complete}\n{"type":"message","message":`, 'utf8');
  const receiver = await createReceiver();
  try {
    const partial = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, preview: true });
    assert.equal(partial.complete, false);
    assert.equal(partial.partial_files, 1);
    await assert.rejects(readFile(statePath), /ENOENT/);

    await writeFile(file, `${complete}\n`, 'utf8');
    await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    const changed = `${JSON.stringify(sessionRecords()[0])}\n${JSON.stringify({ type: 'message', message: { role: 'user', content: 'rewritten prompt' } })}\n`;
    await writeFile(file, changed, 'utf8');
    const conflict = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(conflict.complete, false);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('rewritten prompt')), false);
    await assert.rejects(syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: 'https://remote.example/v1/events' }), /loopback/);
    assert.equal((await getPrimeHistoryStatus({ sessionsDir, artifactsDir, statePath })).pending_files, 1);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Prime malformed rows checkpoint only the safe prefix and resume after repair', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  const file = path.join(sessionsDir, 'session.jsonl');
  await mkdir(sessionsDir, { recursive: true });
  const header = JSON.stringify({ type: 'session', id: 'private-id', timestamp: '2026-09-24T10:00:00Z' });
  const prompt = JSON.stringify({ type: 'message', message: { role: 'user', content: 'private prompt' } });
  await writeFile(file, `${header}\n{malformed\n${prompt}\n`, 'utf8');
  const receiver = await createReceiver();
  try {
    const malformed = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(malformed.complete, false);
    assert.equal(malformed.malformed_lines, 1);
    assert.equal(malformed.events_emitted, 1);
    assert.equal((await getPrimeHistoryStatus({ sessionsDir, artifactsDir, statePath })).pending_files, 1);

    await writeFile(file, `${header}\n${prompt}\n`, 'utf8');
    const repaired = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(repaired.complete, true, JSON.stringify(repaired.errors));
    assert.equal(repaired.events_emitted, 1);
    assert.equal(receiver.events.filter((event) => event.event.action === 'session.started').length, 1);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('private prompt')), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Prime rejects overlapping roots and bounds files independently across sessions and artifacts', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  await writeTranscript(sessionsDir, [{ type: 'session', id: 's1' }], 'one.jsonl');
  await writeTranscript(sessionsDir, [{ type: 'session', id: 's2' }], 'two.jsonl');
  await writeTranscript(artifactsDir, [{ type: 'session', id: 'a1' }], 'one.jsonl');
  await writeTranscript(artifactsDir, [{ type: 'session', id: 'a2' }], 'two.jsonl');
  try {
    await assert.rejects(getPrimeHistoryStatus({ sessionsDir, artifactsDir: path.join(sessionsDir, 'nested') }), /must not overlap/);
    const status = await getPrimeHistoryStatus({ sessionsDir, artifactsDir, statePath, maxFiles: 1 });
    assert.equal(status.files, 2);
    assert.equal(status.files_truncated, true);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Prime caps explicit user and assistant transcript text at 4 KiB', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const artifactsDir = path.join(root, 'artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  await writeTranscript(sessionsDir, [
    { type: 'session', id: 'session' },
    { type: 'message', message: { role: 'user', content: 'allowed prompt' } },
    { type: 'message', message: { role: 'assistant', content: [{ type: 'text', text: `oversized-${'x'.repeat(4_100)}` }] } },
  ], 'session.jsonl');
  const receiver = await createReceiver();
  try {
    await assert.rejects(syncPrimeHistory({ sessionsDir, artifactsDir, statePath, preview: true, includeContent: true }), /cannot be combined/);
    const result = await syncPrimeHistory({ sessionsDir, artifactsDir, statePath, includeContent: true, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.content_mode, 'include-content');
    assert.equal((receiver.events.find((event) => event.event.action === 'prompt.submitted')?.prompt as { text?: string }).text, 'allowed prompt');
    const assistant = receiver.events.find((event) => event.event.action === 'agent.message');
    assert.equal(JSON.stringify(assistant).includes('oversized-'), false);
    assert.equal(JSON.stringify(assistant).includes('x'.repeat(100)), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('Prime defaults use PRIME_AGENT_CODING_AGENT_DIR and ignore the Pi override', async () => {
  const root = await tempRoot();
  const primeAgentDir = path.join(root, 'prime-agent');
  const piAgentDir = path.join(root, 'pi-agent');
  const oldPrime = process.env.PRIME_AGENT_CODING_AGENT_DIR;
  const oldPi = process.env.PI_CODING_AGENT_DIR;
  const oldThreadforge = process.env.THREADFORGE_HOME;
  await writeTranscript(path.join(primeAgentDir, 'sessions'), [{ type: 'session', id: 'prime' }], 'prime.jsonl');
  await writeTranscript(path.join(piAgentDir, 'sessions'), [{ type: 'session', id: 'pi' }], 'pi.jsonl');
  try {
    process.env.PRIME_AGENT_CODING_AGENT_DIR = primeAgentDir;
    process.env.PI_CODING_AGENT_DIR = piAgentDir;
    process.env.THREADFORGE_HOME = path.join(root, 'threadforge');
    const status = await getPrimeHistoryStatus();
    assert.equal(status.session_files, 1);
    assert.equal(status.artifact_files, 0);
  } finally {
    if (oldPrime === undefined) delete process.env.PRIME_AGENT_CODING_AGENT_DIR; else process.env.PRIME_AGENT_CODING_AGENT_DIR = oldPrime;
    if (oldPi === undefined) delete process.env.PI_CODING_AGENT_DIR; else process.env.PI_CODING_AGENT_DIR = oldPi;
    if (oldThreadforge === undefined) delete process.env.THREADFORGE_HOME; else process.env.THREADFORGE_HOME = oldThreadforge;
    await rm(root, { recursive: true, force: true });
  }
});
