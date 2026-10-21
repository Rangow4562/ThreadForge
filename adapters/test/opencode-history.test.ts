import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, realpath, rm } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  buildOpenCodeExportCommand,
  buildOpenCodeSessionListCommand,
  getOpenCodeHistoryStatus,
  syncOpenCodeHistory,
  type OpenCodeHistoryCommand,
} from '../src/opencode-history.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };

async function tempRoot(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-history-')); }
async function projectUnder(root: string): Promise<string> {
  const project = path.join(root, 'project');
  await mkdir(project);
  return project;
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
    endpoint: `http://127.0.0.1:${address.port}/v1/events`, events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

function textSession(id = 'ses_private-id') {
  return {
    info: { id, title: 'private project title', directory: 'C:/private/repo', projectID: 'private-project', time: { created: 1790330400000 } },
    messages: [
      { info: { id: 'msg_user', role: 'user', time: { created: 1790330400000 } }, parts: [{ id: 'part_user', type: 'text', text: 'private prompt body' }] },
      { info: {
        id: 'msg_assistant', role: 'assistant', modelID: 'model-x', providerID: 'provider-y',
        time: { created: 1790330401000, completed: 1790330403000 },
        tokens: { input: 12, output: 8, reasoning: 2, cache: { read: 3, write: 1 } }, cost: 0.0004,
      }, parts: [
        { id: 'part_text', type: 'text', text: 'private assistant answer' },
        { id: 'part_reasoning', type: 'reasoning', text: 'private chain of thought' },
        { id: 'part_tool', type: 'tool-invocation', toolInvocation: {
      toolCallId: 'private-call-id', toolName: 'bash', state: { type: 'partial-call', input: { command: 'API_TOKEN=private' } },
        } },
      ] },
    ],
  };
}

function fixtureRunner(state: { sessions: Array<Record<string, unknown>>; exports: Map<string, unknown> }, commands: OpenCodeHistoryCommand[] = []) {
  return async (command: OpenCodeHistoryCommand) => {
    commands.push(command);
    if (command.args[0] === 'session') return JSON.stringify(state.sessions);
    const value = state.exports.get(command.args[1] ?? '');
    if (value === undefined) throw new Error('not found');
    return JSON.stringify(value);
  };
}

test('builds project-scoped OpenCode list and sanitized export commands', async () => {
  const root = await tempRoot();
  try {
    assert.deepEqual(buildOpenCodeSessionListCommand(root, { maxSessions: 20, opencodeCli: 'opencode-test' }), {
      executable: 'opencode-test', cwd: root, args: ['session', 'list', '--format', 'json', '--max-count', '21', '--pure'],
    });
    assert.deepEqual(buildOpenCodeExportCommand(root, 'ses_fixture'), {
      executable: 'opencode', cwd: root, args: ['export', 'ses_fixture', '--sanitize', '--pure'],
    });
    assert.throws(() => buildOpenCodeSessionListCommand(root, { maxSessions: 201 }), /between 1 and 200/);
    assert.throws(() => buildOpenCodeExportCommand(root, 'bad\nid'), /invalid characters|invalid/i);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('preview maps visible history to digests and never persists raw identifiers, reasoning, paths, or prompts', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  const statePath = path.join(root, 'threadforge', 'state.json');
  const exported = textSession();
  const state = { sessions: [{ id: 'ses_private-id', title: 'private title', directory: root }], exports: new Map([['ses_private-id', exported]]) };
  const commands: OpenCodeHistoryCommand[] = [];
  try {
    const result = await syncOpenCodeHistory({ projectRoot, statePath, preview: true, commandRunner: fixtureRunner(state, commands) });
    assert.equal(result.complete, true);
    assert.equal(result.events_previewed, 5);
    assert.deepEqual(result.events?.map((event) => event.event.action), [
      'prompt.submitted', 'agent.message', 'agent.reasoning', 'tool.invoked', 'token.usage',
    ]);
    assert.equal(commands[0]?.args[0], 'session');
    assert.equal(commands[1]?.args[0], 'export');
    assert.equal(commands[1]?.args[2], '--sanitize');
    assert.equal(commands[0]?.args.at(-1), '--pure');
    assert.equal(commands[1]?.args.at(-1), '--pure');
    assert.equal(commands[0]?.cwd, await realpath(projectRoot));
    const encoded = JSON.stringify(result.events);
    for (const value of ['ses_private-id', 'msg_user', 'part_user', 'private-call-id', 'private prompt body', 'private assistant answer', 'private chain of thought', 'API_TOKEN=private', 'private/repo', 'private title']) {
      assert.equal(encoded.includes(value), false);
    }
    assert.equal((result.events?.[0]?.content as { included: boolean }).included, false);
    assert.equal(result.events?.[0]?.prompt, undefined);
    assert.equal((result.events?.[2]?.content as { included: boolean }).included, false);
    const usage = result.events?.find((event) => event.event.action === 'token.usage');
    assert.equal((usage?.gen_ai as { usage: { input_tokens: number } }).usage.input_tokens, 12);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('repeated exports are idempotent and a mutated tool part produces a result event', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  const statePath = path.join(root, 'state', 'open-code.json');
  const receiver = await createReceiver();
  type ToolExportFixture = {
    info: { id: string };
    messages: Array<{
      info: { id: string; role: string; time: { created: number } };
      parts: Array<{ id: string; type: string; toolInvocation: { toolCallId: string; toolName: string; state: Record<string, unknown> } }>;
    }>;
  };
  const pending: ToolExportFixture = {
    info: { id: 'ses_tool' },
    messages: [{ info: { id: 'msg_tool', role: 'assistant', time: { created: 1790330400000 } }, parts: [{
      id: 'part_tool', type: 'tool-invocation', toolInvocation: {
        toolCallId: 'call-id', toolName: 'bash', state: { status: 'pending', input: { command: 'run tests' }, raw: 'private raw args' },
      },
    }] }],
  };
  const state = { sessions: [{ id: 'ses_tool' }], exports: new Map<string, unknown>([['ses_tool', pending]]) };
  const runner = fixtureRunner(state);
  try {
    let result = await syncOpenCodeHistory({ projectRoot, statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events[0]?.event.action, 'tool.invoked');
    result = await syncOpenCodeHistory({ projectRoot, statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 1);

    const completed = structuredClone(pending);
    completed.messages[0]!.parts[0]!.toolInvocation.state = {
      status: 'completed', output: 'private tool output', time: { end: 1790330402000 }, isError: false,
    };
    state.exports.set('ses_tool', completed);
    result = await syncOpenCodeHistory({ projectRoot, statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 1);
    assert.equal(receiver.events[1]?.event.action, 'command.executed');
    assert.equal(JSON.stringify(receiver.events).includes('private tool output'), false);
    result = await syncOpenCodeHistory({ projectRoot, statePath, endpoint: receiver.endpoint, commandRunner: runner });
    assert.equal(result.events_emitted, 0);
    assert.equal(receiver.events.length, 2);
    const cursor = await readFile(statePath, 'utf8');
    for (const privateValue of ['ses_tool', 'msg_tool', 'part_tool', 'call-id', 'private tool output']) {
      assert.equal(cursor.includes(privateValue), false);
    }
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('refuses to place hash cursors in the selected project tree', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  try {
    await assert.rejects(syncOpenCodeHistory({
      projectRoot,
      statePath: path.join(projectRoot, '.threadforge', 'history.json'),
      preview: true,
      commandRunner: fixtureRunner({ sessions: [], exports: new Map() }),
    }), /outside the selected project root/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('session overflow is reported and only the bounded newest set is previewed', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  const statePath = path.join(root, 'state', 'overflow.json');
  const state = {
    sessions: [{ id: 'ses_one' }, { id: 'ses_two' }],
    exports: new Map<string, unknown>([['ses_one', { info: { id: 'ses_one' }, messages: [] }]]),
  };
  const commands: OpenCodeHistoryCommand[] = [];
  try {
    const result = await syncOpenCodeHistory({
      projectRoot, statePath, maxSessions: 1, preview: true, commandRunner: fixtureRunner(state, commands),
    });
    assert.equal(result.complete, false);
    assert.equal(result.sessions_truncated, true);
    assert.equal(result.sessions, 1);
    assert.equal(commands.length, 2);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('explicit message capture is capped and does not include reasoning or tool payloads', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  const receiver = await createReceiver();
  const statePath = path.join(root, 'state', 'content.json');
  const exported = {
    info: { id: 'ses_content' },
    messages: [{ info: { id: 'msg_content', role: 'user' }, parts: [
      { id: 'part_short', type: 'text', text: 'opt-in prompt text' },
      { id: 'part_long', type: 'text', text: 'x'.repeat(5000) },
      { id: 'part_reasoning', type: 'reasoning', text: 'reasoning is never retained' },
    ] }],
  };
  const state = { sessions: [{ id: 'ses_content' }], exports: new Map([['ses_content', exported]]) };
  try {
    await assert.rejects(syncOpenCodeHistory({ projectRoot, statePath, preview: true, includeContent: true, commandRunner: fixtureRunner(state) }), /cannot be combined/);
    const result = await syncOpenCodeHistory({ projectRoot, statePath, endpoint: receiver.endpoint, includeContent: true, commandRunner: fixtureRunner(state) });
    assert.equal(result.events_emitted, 3);
    assert.equal((receiver.events[0]?.prompt as { text: string }).text, 'opt-in prompt text');
    assert.equal((receiver.events[1]?.content as { included: boolean; truncated: boolean }).included, false);
    assert.equal((receiver.events[1]?.content as { truncated: boolean }).truncated, true);
    assert.equal(JSON.stringify(receiver.events).includes('reasoning is never retained'), false);
    assert.equal(JSON.stringify(receiver.events).includes('x'.repeat(5000)), false);
  } finally {
    await receiver.close();
    await rm(root, { recursive: true, force: true });
  }
});

test('remote event intake is rejected and leaves OpenCode history uncheckpointed', async () => {
  const root = await tempRoot();
  const projectRoot = await projectUnder(root);
  const statePath = path.join(root, 'state', 'remote.json');
  const state = { sessions: [{ id: 'ses_remote' }], exports: new Map([['ses_remote', textSession('ses_remote')]]) };
  try {
    const result = await syncOpenCodeHistory({
      projectRoot, statePath, endpoint: 'https://collector.invalid/v1/events', commandRunner: fixtureRunner(state),
    });
    assert.equal(result.complete, false);
    assert.equal(result.events_emitted, 0);
    assert.equal((await getOpenCodeHistoryStatus({ statePath })).tracked_sessions, 0);
  } finally { await rm(root, { recursive: true, force: true }); }
});
