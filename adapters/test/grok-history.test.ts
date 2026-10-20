import assert from 'node:assert/strict';
import test from 'node:test';
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { getGrokHistoryStatus, syncGrokHistory } from '../src/grok-history.ts';

test('Grok local history previews digest-only session, chat, tool, approval, and turn events', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-history-preview-'));
  const source = await makeSession(root);
  const statePath = path.join(root, '..', `${path.basename(root)}-state`, 'cursor.json');
  try {
    const result = await syncGrokHistory({ sessionsDir: source.root, statePath, preview: true });
    assert.equal(result.complete, true, JSON.stringify(result.errors));
    assert.equal(result.sessions, 1);
    assert.equal(result.events_emitted, 0);
    assert.ok(result.events_previewed >= 8);
    const actions = result.events!.map((event) => event.event.action);
    assert.ok(actions.includes('session.started'));
    assert.ok(actions.includes('prompt.submitted'));
    assert.ok(actions.includes('agent.reasoning'));
    assert.ok(actions.includes('tool.invoked'));
    assert.ok(actions.includes('command.executed'));
    assert.ok(actions.includes('approval.requested'));
    assert.ok(actions.includes('approval.allowed'));
    assert.ok(actions.includes('session.context'));
    assert.equal(actions.includes('session.ended'), false, 'turn_ended is not a session boundary');
    const encoded = JSON.stringify(result.events);
    for (const privateValue of ['private prompt', 'private reasoning', 'private response', 'git status --private', 'private tool output', 'private terminal output', 'session-id-raw', source.projectName]) {
      assert.equal(encoded.includes(privateValue), false, `preview leaked ${privateValue}`);
    }
    assert.equal((await getGrokHistoryStatus({ sessionsDir: source.root, statePath })).checkpointed_sessions, 0);
    await assert.rejects(readFile(statePath), { code: 'ENOENT' });
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(path.dirname(statePath), { recursive: true, force: true });
  }
});

test('Grok history sync resumes independent logs, waits on partial tails, and detects rewritten prefixes', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-history-sync-'));
  const source = await makeSession(root);
  const statePath = path.join(root, 'threadforge-state', 'grok.json');
  const deliveries: Record<string, unknown>[] = [];
  const fetchImpl: typeof fetch = async (_input, init) => {
    const body = JSON.parse(String(init?.body)) as unknown;
    deliveries.push(...(Array.isArray(body) ? body as Record<string, unknown>[] : [body as Record<string, unknown>]));
    return new Response(null, { status: 202 });
  };
  try {
    const options = { sessionsDir: source.root, statePath, fetchImpl };
    const initial = await syncGrokHistory(options);
    assert.equal(initial.complete, true, JSON.stringify(initial.errors));
    assert.equal(initial.sessions_changed, 1);
    assert.ok(initial.events_emitted >= 8);
    const originalCount = deliveries.length;
    assert.ok(originalCount >= 8);
    const state = JSON.parse(await readFile(statePath, 'utf8')) as unknown;
    const stateJson = JSON.stringify(state);
    assert.equal(stateJson.includes('session-id-raw'), false);
    assert.equal(stateJson.includes(source.projectName), false);
    assert.equal(stateJson.includes('private prompt'), false);

    await appendFile(source.chatPath, `${JSON.stringify({ type: 'user', content: 'next private prompt' })}\n`, 'utf8');
    const appended = await syncGrokHistory(options);
    assert.equal(appended.complete, true, JSON.stringify(appended.errors));
    assert.equal(appended.events_emitted, 1);
    assert.equal(deliveries.length, originalCount + 1);
    assert.equal(JSON.stringify(deliveries).includes('next private prompt'), false);

    await appendFile(source.chatPath, JSON.stringify({ type: 'user', content: 'incomplete tail' }), 'utf8');
    const partial = await syncGrokHistory(options);
    assert.equal(partial.complete, false);
    assert.equal(partial.partial_sessions, 1);
    assert.equal(partial.events_emitted, 0);
    const pendingStatus = await getGrokHistoryStatus({ sessionsDir: source.root, statePath });
    assert.equal(pendingStatus.incomplete_sessions, 1);
    assert.equal(pendingStatus.pending_sessions, 1);

    await appendFile(source.chatPath, '\n', 'utf8');
    const repairedTail = await syncGrokHistory(options);
    assert.equal(repairedTail.complete, true, JSON.stringify(repairedTail.errors));
    assert.equal(repairedTail.events_emitted, 1);
    assert.equal(deliveries.length, originalCount + 2);
    const repairedStatus = await getGrokHistoryStatus({ sessionsDir: source.root, statePath });
    assert.equal(repairedStatus.incomplete_sessions, 0);

    const lines = (await readFile(source.chatPath, 'utf8')).split('\n');
    lines[0] = lines[0]!.replace('private prompt', 'rewritten prompt');
    await writeFile(source.chatPath, lines.join('\n'), 'utf8');
    const conflict = await syncGrokHistory(options);
    assert.equal(conflict.complete, false);
    assert.equal(conflict.events_emitted, 0);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Grok history reports terminal-log mutation after checkpoint instead of silently missing output', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-terminal-conflict-'));
  const source = await makeSession(root);
  const statePath = path.join(root, 'state', 'cursor.json');
  const terminalPath = path.join(path.dirname(source.chatPath), 'terminal', 'call-private-call-id-1.log');
  try {
    const fetchImpl: typeof fetch = async () => new Response(null, { status: 202 });
    const first = await syncGrokHistory({ sessionsDir: source.root, statePath, fetchImpl });
    assert.equal(first.complete, true, JSON.stringify(first.errors));
    await writeFile(terminalPath, 'changed terminal output', 'utf8');
    const changed = await syncGrokHistory({ sessionsDir: source.root, statePath, fetchImpl });
    assert.equal(changed.complete, false);
    assert.equal(changed.events_emitted, 0);
    assert.match(changed.errors[0]?.message ?? '', /prefix changed/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Grok history include-content is explicit, capped, and preview/remote paths fail closed', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-history-content-'));
  const source = await makeSession(root);
  try {
    const preview = await syncGrokHistory({ sessionsDir: source.root, statePath: path.join(root, 'state.json'), preview: true });
    assert.equal(JSON.stringify(preview.events).includes('private prompt'), false);
    await assert.rejects(syncGrokHistory({ sessionsDir: source.root, statePath: path.join(root, 'state.json'), preview: true, includeContent: true }), /cannot be combined/);
    await assert.rejects(syncGrokHistory({ sessionsDir: source.root, endpoint: 'https://collector.example/v1/events' }), /loopback/);

    const requested = await syncGrokHistory({
      sessionsDir: source.root, statePath: path.join(root, 'content-state.json'), includeContent: true,
      preview: false, fetchImpl: async (_input, init) => {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>[];
        assert.ok(body.some((event) => JSON.stringify(event).includes('private prompt')));
        return new Response(null, { status: 202 });
      },
    });
    assert.equal(requested.complete, true, JSON.stringify(requested.errors));
    assert.equal(requested.content_mode, 'include-content');
  } finally { await rm(root, { recursive: true, force: true }); }
});

async function makeSession(parent: string): Promise<{ root: string; chatPath: string; projectName: string }> {
  const root = path.join(parent, 'sessions');
  const projectName = encodeURIComponent('/private/workspace');
  const session = path.join(root, projectName, 'session-id-raw');
  await mkdir(path.join(session, 'terminal'), { recursive: true });
  await writeFile(path.join(session, 'summary.json'), JSON.stringify({
    created_at: '2026-09-25T09:00:00.000Z', current_model_id: 'grok-build', generated_title: 'private title',
  }), 'utf8');
  const chatPath = path.join(session, 'chat_history.jsonl');
  const chat = [
    { type: 'user', content: 'private prompt with token=do-not-leak' },
    {
      type: 'assistant', content: 'private response', reasoning: { text: 'private reasoning' }, model_id: 'grok-build',
      tool_calls: [{ id: 'call-private-call-id', name: 'run_terminal_command', arguments: '{"command":"git status --private"}' }],
    },
    { type: 'tool_result', tool_call_id: 'call-private-call-id', content: 'private tool output' },
  ];
  await writeFile(chatPath, `${chat.map((record) => JSON.stringify(record)).join('\n')}\n`, 'utf8');
  await writeFile(path.join(session, 'events.jsonl'), [
    { type: 'turn_started', ts: '2026-09-25T09:00:00.000Z', turn_number: 0, model_id: 'grok-build' },
    { type: 'permission_requested', ts: '2026-09-25T09:00:01.000Z', tool_name: 'run_terminal_command' },
    { type: 'permission_resolved', ts: '2026-09-25T09:00:02.000Z', tool_name: 'run_terminal_command', decision: 'allow' },
    { type: 'tool_completed', ts: '2026-09-25T09:00:03.000Z', outcome: 'success', tool_name: 'run_terminal_command' },
    { type: 'turn_ended', ts: '2026-09-25T09:00:04.000Z', turn_number: 0, outcome: 'success' },
  ].map((record) => JSON.stringify(record)).join('\n') + '\n', 'utf8');
  await writeFile(path.join(session, 'terminal', 'call-private-call-id-1.log'), 'private terminal output', 'utf8');
  return { root, chatPath, projectName };
}
