import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { getClineHistoryStatus, syncClineHistory } from '../src/cline-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

async function fixture() {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-history-'));
  const clineDir = path.join(root, '.cline');
  const statePath = path.join(root, '.threadforge', 'state.json');
  await mkdir(clineDir, { recursive: true });
  return { root, clineDir, statePath, vscodeStorageDir: path.join(root, 'vscode-global-storage') };
}

function capture(events: ThreadforgeEvent[]): typeof fetch {
  return async (_input, init) => {
    const payload = JSON.parse(String(init?.body)) as ThreadforgeEvent | ThreadforgeEvent[];
    events.push(...(Array.isArray(payload) ? payload : [payload]));
    return new Response('{}', { status: 202 });
  };
}

const history = [
  { role: 'user', ts: '2026-10-08T10:00:00Z', content: [{ type: 'text', text: 'private prompt with sk-test-secret' }] },
  { role: 'assistant', ts: '2026-10-08T10:00:01Z', modelId: 'claude-test', content: [
    { type: 'text', text: 'private answer' }, { type: 'thinking', thinking: 'private reasoning' },
    { type: 'tool_use', id: 'private-call-id', name: 'execute_command', input: { command: 'echo private-argument' } },
  ], metrics: { inputTokens: 10, outputTokens: 5, totalCost: 0.01 } },
  { role: 'user', ts: '2026-10-08T10:00:02Z', content: [{ type: 'tool_result', tool_use_id: 'private-call-id', content: 'private tool output' }] },
];

test('Cline history status and preview cover task JSON without exposing source identity or content', async () => {
  const f = await fixture();
  const file = path.join(f.clineDir, 'data', 'tasks', 'private-task', 'api_conversation_history.json');
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(history));
    const status = await getClineHistoryStatus({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath });
    assert.equal(status.sessions, 1);
    assert.equal(status.pending_sessions, 1);
    assert.equal(JSON.stringify(status).includes('private-task'), false);
    assert.equal(JSON.stringify(status).includes(file), false);

    const preview = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, preview: true });
    assert.equal(preview.complete, true);
    assert.deepEqual(preview.events?.map((event) => event.event.action), [
      'session.started', 'prompt.submitted', 'agent.message', 'agent.reasoning', 'tool.invoked', 'command.executed',
    ]);
    assert.ok(preview.events?.some((event) => event.event.action === 'command.executed'));
    const serialized = JSON.stringify(preview.events);
    for (const privateValue of ['private-task', 'private-call-id', 'sk-test-secret', 'private prompt', 'private answer', 'private reasoning', 'private-argument', 'private tool output', file]) {
      assert.equal(serialized.includes(privateValue), false, `preview leaked ${privateValue}`);
    }
    await assert.rejects(readFile(f.statePath), /ENOENT/);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline task history resumes appended rows, emits once, and withholds rewritten prefixes', async () => {
  const f = await fixture();
  const file = path.join(f.clineDir, 'tasks', 'task-a', 'api_conversation_history.json');
  const delivered: ThreadforgeEvent[] = [];
  const fetchImpl = capture(delivered);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(history.slice(0, 1)));
    const first = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl });
    assert.equal(first.complete, true);
    assert.equal(first.events_emitted, 2);
    assert.equal(first.content_mode, 'metadata');
    assert.equal(JSON.stringify(delivered).includes('private prompt'), false);

    const second = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl });
    assert.equal(second.events_emitted, 0);
    assert.equal(second.complete, true);

    await writeFile(file, JSON.stringify([...history.slice(0, 1), { role: 'assistant', content: 'new answer', ts: '2026-10-08T10:00:03Z' }]));
    const appended = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl });
    assert.equal(appended.events_emitted, 1);
    assert.equal(delivered.at(-1)?.event.action, 'agent.message');

    await writeFile(file, JSON.stringify([{ role: 'user', content: 'changed old prompt' }, ...history.slice(1, 2)]));
    const conflict = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl });
    assert.equal(conflict.complete, false);
    assert.match(conflict.errors[0]?.message ?? '', /prefix changed/);
    assert.equal(delivered.length, 3);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline session-message stores and VS Code task stores are discovered', async () => {
  const f = await fixture();
  const messageFile = path.join(f.clineDir, 'data', 'sessions', 'lead-task', 'subagent-1.messages.json');
  const vscodeFile = path.join(f.vscodeStorageDir, 'tasks', 'vscode-task', 'api_conversation_history.json');
  try {
    await mkdir(path.dirname(messageFile), { recursive: true });
    await mkdir(path.dirname(vscodeFile), { recursive: true });
    await writeFile(messageFile, JSON.stringify({ messages: [{ role: 'assistant', content: 'subagent answer' }] }));
    await writeFile(vscodeFile, JSON.stringify([{ role: 'user', content: 'VS Code prompt' }]));
    const status = await getClineHistoryStatus({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath });
    assert.equal(status.sessions, 2);
    assert.equal(status.pending_sessions, 2);
    const preview = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, preview: true });
    assert.equal(preview.events_previewed, 4);
    assert.equal(JSON.stringify(preview.events).includes('subagent-1'), false);
    assert.equal(JSON.stringify(preview.events).includes('VS Code prompt'), false);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline Kanban polling reports mutable activity revisions without replaying unchanged fields', async () => {
  const f = await fixture();
  const workspace = path.join(f.clineDir, 'kanban', 'workspaces', 'workspace-1');
  const sessionsPath = path.join(workspace, 'sessions.json');
  const boardPath = path.join(workspace, 'board.json');
  const delivered: ThreadforgeEvent[] = [];
  const options = { clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl: capture(delivered) };
  try {
    await mkdir(workspace, { recursive: true });
    await writeFile(sessionsPath, JSON.stringify({ task1: {
      state: 'running', startedAt: 1_791_456_000_000, latestHookActivity: { activityText: 'private initial activity' },
    } }));
    await writeFile(boardPath, JSON.stringify({ columns: [{ cards: [{ id: 'task1', prompt: 'private kanban prompt' }] }] }));
    const first = await syncClineHistory(options);
    assert.equal(first.events_emitted, 3);
    assert.equal(JSON.stringify(delivered).includes('private kanban prompt'), false);
    const next = await syncClineHistory(options);
    assert.equal(next.events_emitted, 0);

    await writeFile(sessionsPath, JSON.stringify({ task1: {
      state: 'completed', startedAt: 1_791_456_000_000, updatedAt: 1_791_456_001_000,
      latestHookActivity: { finalMessage: 'private final activity' },
    } }));
    const updated = await syncClineHistory(options);
    assert.equal(updated.events_emitted, 1);
    assert.equal(delivered.at(-1)?.event.action, 'agent.message');
    assert.equal(JSON.stringify(delivered).includes('private final activity'), false);
    assert.equal(delivered.filter((event) => event.event.action === 'prompt.submitted').length, 1);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline content capture is explicit, bounded, and rejected for preview', async () => {
  const f = await fixture();
  const file = path.join(f.clineDir, 'data', 'tasks', 'task-b', 'api_conversation_history.json');
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify([{ role: 'user', content: 'opt-in prompt' }]));
    const events: ThreadforgeEvent[] = [];
    const result = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir,
      statePath: f.statePath, includeContent: true, fetchImpl: capture(events) });
    assert.equal(result.content_mode, 'include-content');
    assert.equal(JSON.stringify(events).includes('opt-in prompt'), true);
    await assert.rejects(syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, preview: true, includeContent: true }), /cannot be combined/);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline bounds, loopback-only forwarding, cursor placement, and invalid formats fail closed', async () => {
  const f = await fixture();
  try {
    await assert.rejects(getClineHistoryStatus({ clineDir: f.clineDir, maxSessions: 5_001 }), /between 1 and 5000/);
    await assert.rejects(syncClineHistory({ clineDir: f.clineDir, endpoint: 'https://collector.example/v1/events' }), /loopback/);
    await assert.rejects(syncClineHistory({ clineDir: f.clineDir, statePath: path.join(f.clineDir, 'state.json') }), /outside native/);
    const file = path.join(f.clineDir, 'data', 'tasks', 'broken', 'api_conversation_history.json');
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, '{"messages":[{"role":"user"}]');
    const malformed = await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, preview: true });
    assert.equal(malformed.complete, false);
    assert.equal(malformed.malformed_sessions, 1);
    assert.match(malformed.errors[0]?.message ?? '', /malformed/);
    assert.equal(JSON.stringify(malformed).includes(file), false);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});

test('Cline cursor stores only hashes and rejects unknown edited state fields', async () => {
  const f = await fixture();
  const file = path.join(f.clineDir, 'data', 'tasks', 'state-check', 'api_conversation_history.json');
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify([{ role: 'user', content: 'private cursor source text' }]));
    const events: ThreadforgeEvent[] = [];
    await syncClineHistory({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath, fetchImpl: capture(events) });
    const stateText = await readFile(f.statePath, 'utf8');
    assert.equal(stateText.includes('private cursor source text'), false);
    const state = JSON.parse(stateText) as Record<string, unknown>;
    state.untrusted_text = 'must not be copied back into state';
    await writeFile(f.statePath, JSON.stringify(state));
    await assert.rejects(getClineHistoryStatus({ clineDir: f.clineDir, vscodeStorageDir: f.vscodeStorageDir, statePath: f.statePath }), /unsupported schema/);
  } finally { await rm(f.root, { recursive: true, force: true }); }
});
