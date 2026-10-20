import assert from 'node:assert/strict';
import { appendFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getFxHistoryStatus, syncFxHistory } from '../src/fx-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };
const generationA = '3b4c72027563ff60190f0299461ee01c';
const generationB = '9c4a72027563ff60190f0299461ee02d';

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-fx-history-')); }

function envelope(seq: number, kind: string, payload: unknown, generation = generationA) {
  return {
    schema_version: 1, log_generation: generation, seq, event_id: `private-envelope-${seq}`,
    timestamp_ms: 1_780_000_000_000 + seq * 1_000, kind, payload,
  };
}

function started(generation = generationA, workspace = 'C:/private/repo') {
  return envelope(1, 'session_started', {
    id: 'private-session-id', created_at_ms: 1_780_000_000_000,
    origin_workspace_root: workspace, workspace_root: workspace, conversation_language: 'en',
    preferences: { provider: 'gateway', model: 'provider/model-safe', effort: 'medium', fast_mode: false }, usage: null,
  }, generation);
}

function turn(seq = 2, prompt = 'private prompt text', generation = generationA) {
  return envelope(seq, 'history_turn_committed', {
    conversation_language: 'en', total_input_tokens: 4_200, total_output_tokens: 880, work_id: 'private-work-id',
    turn: {
      kind: 'assistant', user: { text: prompt, images: [{ id: 1, path: 'C:/private/image.png', media_type: 'image/png' }] },
      assistant: 'private assistant response',
      execution: {
        schema_version: 5,
        tool_steps: [{
          assistant: 'private tool preamble',
          tool_calls: [
            { id: 'private-read-call', name: 'read_file', arguments_json: JSON.stringify({ path: 'C:/private/repo/secret.txt' }), provider_result: null },
            { id: 'private-edit-call', name: 'edit_file', arguments_json: JSON.stringify({ path: 'C:/private/repo/secret.txt', content: 'private diff content' }), provider_result: null },
            { id: 'private-command-call', name: 'run_command', arguments_json: JSON.stringify({ command: 'npm test --secret=private' }), provider_result: null },
          ],
          tool_results: [
            {
              tool_call_id: 'private-read-call', tool_name: 'read_file', status: 'success', output: 'private file contents',
              output_bytes: 21, stored_output_bytes: 21, truncated: false, provider_native: false, created_at_ms: 1_780_000_001_000,
              permission_feedback: [], committed_file_presentation: null, command_output_replay: null,
              command_process_presentation: null, terminal_action_presentation: null,
            },
            {
              tool_call_id: 'private-edit-call', tool_name: 'edit_file', status: 'success', output: 'private edit result',
              output_bytes: 18, stored_output_bytes: 18, truncated: false, provider_native: false, created_at_ms: 1_780_000_002_000,
              permission_feedback: ['private approval feedback'],
              committed_file_presentation: { path: 'C:/private/repo/secret.txt', kind: 'edited', lines: [
                { kind: 'deletion', text: 'private old line' }, { kind: 'addition', text: 'private new line' },
              ], additions: 1, deletions: 1, truncated: false },
              command_output_replay: null, command_process_presentation: null, terminal_action_presentation: null,
            },
            {
              tool_call_id: 'private-command-call', tool_name: 'run_command', status: 'failure', output: 'private command output',
              output_bytes: 4096, stored_output_bytes: 22, truncated: true, provider_native: false, created_at_ms: 1_780_000_003_000,
              permission_feedback: [], committed_file_presentation: null, command_output_replay: { kind: 'available', handle: 'private-handle', framed_bytes: 4096 },
              command_process_presentation: { kind: 'exit_code', value: 1 }, terminal_action_presentation: null,
            },
          ],
        }],
        files: [
          { path: 'C:/private/repo/secret.txt', new_path: null, tool_call_id: 'private-read-call', tool_name: 'read_file', action: 'read', status: 'success', model_view_covers_full_file: true, stale: false },
          { path: 'C:/private/repo/secret.txt', new_path: null, tool_call_id: 'private-edit-call', tool_name: 'edit_file', action: 'edit', status: 'success', model_view_covers_full_file: true, stale: false },
        ],
        turn_summary: { started_at_ms: 1_780_000_000_500, completed_at_ms: 1_780_000_004_000, thinking_duration_ms: 900, turn_duration_ms: 3_500,
          token_progress: { input_tokens: 1_200, output_tokens: 340, input_exact: true, output_exact: false } },
      },
    },
  }, generation);
}

function usage(seq: number, input: number, output: number, cost: number, generation = generationA) {
  return envelope(seq, 'usage_checkpointed', { usage: {
    billing: 'complete', api_duration_ms: 5_100, wall_duration_ms: 7_200, total_cost: cost,
    input_tokens: input, output_tokens: output, cache_read_tokens: input / 2, cache_write_tokens: output / 2,
    billable_web_search_calls: 2, lines_added: 12, lines_removed: 3,
    models: [{ model: 'provider/model-safe', total_cost: cost, input_tokens: input, output_tokens: output, cache_read_tokens: input / 2, cache_write_tokens: output / 2 }],
  } }, generation);
}

async function writeSession(
  root: string, id: string, records: unknown[], options: { generation?: string; watermarkRecords?: number; lastSeq?: number; manifest?: boolean } = {},
) {
  const generation = options.generation ?? generationA;
  const dir = path.join(root, id);
  await mkdir(dir, { recursive: true });
  const lines = records.map((record) => JSON.stringify(record));
  const body = `${lines.join('\n')}\n`;
  const logPath = path.join(dir, 'events.jsonl');
  await writeFile(logPath, body, 'utf8');
  if (options.manifest !== false) {
    const watermarkRecords = options.watermarkRecords ?? records.length;
    const eventLogBytes = lines.slice(0, watermarkRecords).reduce((size, line) => size + Buffer.byteLength(line, 'utf8') + 1, 0);
    await writeFile(path.join(dir, 'session.json'), JSON.stringify({
      schema_version: 3, storage_format: 'event_log_v1', id, authority_id: 'private-authority', log_generation: generation,
      created_at_ms: 1_780_000_000_000, updated_at_ms: 1_780_000_005_000,
      origin_workspace_root: 'C:/private/repo', workspace_root: 'C:/private/repo', conversation_language: 'en',
      history_len: 1, total_input_tokens: 4_200, total_output_tokens: 880,
      last_event_seq: options.lastSeq ?? watermarkRecords, event_log_bytes: eventLogBytes,
      preferences: { provider: 'gateway', model: 'provider/model-safe', effort: 'medium', fast_mode: false },
    }), 'utf8');
  }
  return { dir, logPath, body, lines };
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

test('fx preview maps committed session, prompts, files, commands, tools, and token usage without private values', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  await writeSession(sessionsDir, '1788051418424-1788051418424884928-a177316124162631', [started(), turn()]);
  try {
    const result = await syncFxHistory({ sessionsDir, statePath, preview: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'file.read', 'file.modified', 'command.executed', 'agent.message', 'token.usage',
    ]);
    const encoded = JSON.stringify(result.events);
    for (const secret of [
      '1788051418424-1788051418424884928-a177316124162631', 'private-session-id', 'private-envelope-', 'private-work-id',
      'private prompt text', 'private assistant response', 'private tool preamble', 'private-read-call', 'private-edit-call',
      'C:/private/repo', 'secret.txt', 'private file contents', 'private diff content', 'private old line', 'private new line',
      'npm test --secret=private', 'private command output', 'private-handle', 'private approval feedback',
    ]) assert.equal(encoded.includes(secret), false, `preview leaked ${secret}`);
    assert.equal(result.events?.[0]?.harness.name, 'vercel_fx');
    assert.equal(result.events?.[0]?.harness.collection_method, 'poll');
    assert.equal((result.events?.[2]?.file as { path_sha256?: string }).path_sha256 !== undefined, true);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('fx checkpoints an empty log when its valid manifest has no committed events', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  await writeSession(sessionsDir, '1788051418424-1788051418424884928-a177316124162631', []);
  try {
    const result = await syncFxHistory({ sessionsDir, statePath });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 0);
    const status = await getFxHistoryStatus({ sessionsDir, statePath });
    assert.equal(status.checkpointed_sessions, 1);
    assert.equal(status.pending_sessions, 0);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('fx committed watermark excludes in-flight records and status tracks the manifest cursor', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const sessionId = '1788051418424-1788051418424884928-a177316124162631';
  const inFlight = envelope(3, 'history_turn_committed', { turn: { kind: 'assistant', user: { text: 'never committed secret', images: [] } } });
  await writeSession(sessionsDir, sessionId, [started(), turn(), inFlight], { watermarkRecords: 2, lastSeq: 2 });
  const receiver = await createReceiver();
  try {
    const pending = await getFxHistoryStatus({ sessionsDir, statePath });
    assert.equal(pending.pending_sessions, 1);
    let result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('never committed secret')), false);
    const status = await getFxHistoryStatus({ sessionsDir, statePath });
    assert.equal(status.checkpointed_sessions, 1);
    assert.equal(status.pending_sessions, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('fx waits on an unterminated frame when the manifest is absent, then collects it after repair', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const sessionDir = path.join(sessionsDir, 'session-partial');
  const logPath = path.join(sessionDir, 'events.jsonl');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const firstLine = JSON.stringify(started());
  const secondLine = JSON.stringify(turn());
  await mkdir(sessionDir, { recursive: true });
  await writeFile(logPath, `${firstLine}\n${secondLine.slice(0, 48)}`, 'utf8');
  const receiver = await createReceiver();
  try {
    let result = await syncFxHistory({ sessionsDir, statePath, preview: true });
    assert.equal(result.complete, false);
    assert.equal(result.partial_sessions, 1);
    assert.equal(result.events_previewed, 1);
    await assert.rejects(readFile(statePath), /ENOENT/);

    await writeFile(logPath, `${firstLine}\n${secondLine}\n`, 'utf8');
    result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 7);
    const repeated = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(repeated.events_emitted, 0);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('fx sync is resumable, idempotent, handles log generations, and emits cumulative usage deltas', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const sessionId = '1788051418424-1788051418424884928-a177316124162631';
  const { logPath, dir } = await writeSession(sessionsDir, sessionId, [started(), turn(), usage(3, 4_500, 900, 0.02)]);
  const receiver = await createReceiver();
  try {
    let result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.events_emitted, 8);
    result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 0);

    const next = usage(4, 5_000, 1_000, 0.04);
    const oldManifest = JSON.parse(await readFile(path.join(dir, 'session.json'), 'utf8')) as Record<string, unknown>;
    const appendedLine = JSON.stringify(next);
    await appendFile(logPath, `${appendedLine}\n`, 'utf8');
    oldManifest.event_log_bytes = (await readFile(logPath)).byteLength;
    oldManifest.last_event_seq = 4;
    oldManifest.updated_at_ms = 1_780_000_010_000;
    await writeFile(path.join(dir, 'session.json'), JSON.stringify(oldManifest), 'utf8');
    result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.events_emitted, 1);
    const usageEvent = receiver.events.at(-1);
    assert.deepEqual((usageEvent?.gen_ai as { usage: Record<string, unknown> }).usage, {
      cache_read_input_tokens: 250, cache_creation_input_tokens: 50, cost_usd: 0.02,
    });

    const compactedRecords = [started(generationB), turn(2, 'private prompt after compaction', generationB)];
    const compactedBody = `${compactedRecords.map((record) => JSON.stringify(record)).join('\n')}\n`;
    await writeFile(logPath, compactedBody, 'utf8');
    const compactedManifest = JSON.parse(await readFile(path.join(dir, 'session.json'), 'utf8')) as Record<string, unknown>;
    compactedManifest.log_generation = generationB;
    compactedManifest.event_log_bytes = Buffer.byteLength(compactedBody, 'utf8');
    compactedManifest.last_event_seq = 2;
    await writeFile(path.join(dir, 'session.json'), JSON.stringify(compactedManifest), 'utf8');
    result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(receiver.events.filter((event) => event.event.action === 'session.started').length, 1);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('private prompt after compaction')), false);
    const cursorText = await readFile(statePath, 'utf8');
    assert.equal(cursorText.includes(sessionId), false);
    assert.equal(cursorText.includes(generationA), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('fx content opt-in is capped and tool, path, command, and reasoning payloads remain digests', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const longText = 'x'.repeat(4_097);
  await writeSession(sessionsDir, 'session-opt-in', [started(), turn(2, longText), turn(3, 'short opt-in prompt')]);
  const receiver = await createReceiver();
  try {
    const result = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint, includeContent: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    const prompt = receiver.events.find((event) => event.event.action === 'prompt.submitted');
    assert.ok(prompt);
    assert.equal(prompt.prompt, undefined);
    assert.equal((prompt.content as { truncated?: boolean }).truncated, true);
    const smallPrompt = receiver.events.find((event) => event.event.action === 'prompt.submitted' && (event.content as { bytes?: number }).bytes !== 4_097);
    assert.ok(smallPrompt?.prompt);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('private file contents')), false);
    assert.equal(receiver.events.some((event) => JSON.stringify(event).includes('npm test --secret=private')), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('fx rejects same-generation rewrites, unsafe endpoints, source-local cursors, and invalid manifests', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const sessionId = 'session-conflict';
  const { logPath, dir } = await writeSession(sessionsDir, sessionId, [started(), turn()]);
  const receiver = await createReceiver();
  try {
    await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    const rewritten = `${JSON.stringify(started())}\n${JSON.stringify(turn(2, 'rewritten prompt'))}\n`;
    await writeFile(logPath, rewritten, 'utf8');
    const manifestPath = path.join(dir, 'session.json');
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as Record<string, unknown>;
    manifest.event_log_bytes = Buffer.byteLength(rewritten, 'utf8');
    manifest.last_event_seq = 2;
    await writeFile(manifestPath, JSON.stringify(manifest), 'utf8');
    const conflict = await syncFxHistory({ sessionsDir, statePath, endpoint: receiver.endpoint });
    assert.equal(conflict.complete, false);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(receiver.events.length, 7);
    await assert.rejects(syncFxHistory({ sessionsDir, endpoint: 'https://example.com/v1/events' }), /loopback/);
    await assert.rejects(syncFxHistory({ sessionsDir, statePath: path.join(sessionsDir, 'cursor.json'), preview: true }), /outside fx sessions/);
    await assert.rejects(getFxHistoryStatus({ sessionsDir, statePath: path.join(sessionsDir, 'cursor.json') }), /outside fx sessions/);

    await writeFile(path.join(dir, 'session.json'), JSON.stringify({ schema_version: 4, storage_format: 'event_log_v2' }), 'utf8');
    const invalid = await syncFxHistory({ sessionsDir, statePath: path.join(root, 'other-state.json'), preview: true });
    assert.equal(invalid.complete, false);
    assert.match(invalid.errors[0]?.message ?? '', /manifest/);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('fx does not checkpoint rejected loopback batches and suppresses credential-like provider labels', async () => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  const poisonedStart = started();
  const payload = poisonedStart.payload as Record<string, unknown>;
  payload.preferences = { provider: 'sk-proj-private-provider-token', model: 'ghp_private-model-token' };
  const poisoned = await writeSession(sessionsDir, 'session-poisoned-labels', [poisonedStart]);
  const manifestPath = path.join(poisoned.dir, 'session.json');
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as Record<string, unknown>;
  manifest.preferences = { provider: 'sk-proj-private-provider-token', model: 'ghp_private-model-token' };
  await writeFile(manifestPath, JSON.stringify(manifest), 'utf8');
  const rejectingServer: Server = createServer((_request, response) => { response.writeHead(500); response.end('{}'); });
  await new Promise<void>((resolve) => rejectingServer.listen(0, '127.0.0.1', resolve));
  const address = rejectingServer.address();
  assert.ok(address && typeof address !== 'string');
  try {
    const rejected = await syncFxHistory({ sessionsDir, statePath, endpoint: `http://127.0.0.1:${address.port}/v1/events` });
    assert.equal(rejected.complete, false);
    assert.equal(rejected.events_emitted, 0);
    const status = await getFxHistoryStatus({ sessionsDir, statePath });
    assert.equal(status.pending_sessions, 1);

    const preview = await syncFxHistory({ sessionsDir, statePath, preview: true });
    const encoded = JSON.stringify(preview.events);
    assert.equal(encoded.includes('sk-proj-private-provider-token'), false);
    assert.equal(encoded.includes('ghp_private-model-token'), false);
    assert.equal(preview.events?.[0]?.model, undefined);
    assert.equal((preview.events?.[0]?.gen_ai as { provider?: unknown } | undefined)?.provider, undefined);
  } finally {
    await new Promise<void>((resolve, reject) => rejectingServer.close((error) => error ? reject(error) : resolve()));
    await rm(root, { recursive: true, force: true });
  }
});

test('fx bounded listing reports truncated sessions and skips symlinked session trees', async (t) => {
  const root = await tempRoot();
  const sessionsDir = path.join(root, 'sessions');
  await writeSession(sessionsDir, 'session-one', [started()]);
  await writeSession(sessionsDir, 'session-two', [started()]);
  try {
    const limited = await syncFxHistory({ sessionsDir, statePath: path.join(root, 'state.json'), preview: true, maxSessions: 1 });
    assert.equal(limited.sessions, 1);
    assert.equal(limited.sessions_truncated, true);
    const outside = path.join(root, 'outside');
    await mkdir(outside);
    try { await symlink(outside, path.join(sessionsDir, 'session-link'), 'junction'); }
    catch { t.skip('symlink creation is unavailable in this environment'); return; }
    const status = await getFxHistoryStatus({ sessionsDir, maxSessions: 10 });
    assert.equal(status.symlinks_skipped, 1);
  } finally { await rm(root, { recursive: true, force: true }); }
});
