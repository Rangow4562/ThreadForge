import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { createThreadforgeOpenClawPlugin, mapOpenClawHook, openClawHooks } from '../openclaw-plugin/index.js';

const capturedAt = new Date('2026-09-25T12:00:00.000Z');
const context = {
  sessionId: 'private-session', sessionKey: 'discord:private-channel', runId: 'private-run', agentId: 'main',
  channel: 'discord', modelProviderId: 'openai', modelId: 'gpt-5', workspaceDir: 'C:\\private\\repo',
};
const sha256 = (value: string) => createHash('sha256').update(value).digest('hex');

test('maps gateway lifecycle and chat with hashed identities and digest-only content', () => {
  const [started] = mapOpenClawHook('session_start', { sessionId: 'private-session' }, context, capturedAt);
  assert.equal(started?.event.action, 'session.started');
  assert.deepEqual(started?.session, { id: `openclaw:${sha256('private-session')}` });
  assert.equal(started?.model, 'openai/gpt-5');
  assert.equal(JSON.stringify(started).includes('private-channel'), false);
  assert.equal(JSON.stringify(started).includes('C:\\private'), false);

  const [prompt] = mapOpenClawHook('message_received', {
    sessionId: 'private-session', senderId: 'private-user-id', channel: 'discord', text: 'private chat text',
  }, context, capturedAt);
  assert.equal(prompt?.event.action, 'prompt.submitted');
  assert.equal((prompt?.content as { retention: string }).retention, 'metadata');
  assert.equal((prompt?.content as { sha256: string }).sha256, sha256('private chat text'));
  assert.equal((prompt?.raw as { sender_sha256: string }).sender_sha256, sha256('private-user-id'));
  assert.equal(JSON.stringify(prompt).includes('private chat text'), false);
  assert.equal(JSON.stringify(prompt).includes('private-user-id'), false);
  assert.equal(mapOpenClawHook('session_end', { reason: 'completed' }, context, capturedAt)[0]?.event.action, 'session.ended');
});

test('joins tool call paths by call id without keeping paths or arguments', () => {
  const pending = new Map();
  const before = mapOpenClawHook('before_tool_call', {
    toolName: 'apply_patch', toolCallId: 'private-call', params: { patch: 'secret patch' }, derivedPaths: ['C:\\private\\repo\\a.go'],
  }, context, capturedAt, pending)[0];
  assert.equal(before?.event.action, 'tool.invoked');
  assert.equal(JSON.stringify(before).includes('secret patch'), false);
  assert.equal(JSON.stringify(before).includes('C:\\private'), false);

  const after = mapOpenClawHook('after_tool_call', {
    toolName: 'apply_patch', toolCallId: 'private-call', result: { ok: true },
  }, context, capturedAt, pending)[0];
  assert.equal(after?.event.action, 'file.modified');
  assert.deepEqual((after?.file as { path_sha256: string[] }).path_sha256, [sha256('C:\\private\\repo\\a.go')]);
  assert.equal(JSON.stringify(after).includes('C:\\private'), false);
  assert.equal(pending.size, 0);

  const mcp = mapOpenClawHook('after_tool_call', {
    toolName: 'github__create_issue', toolCallId: 'mcp-call', result: { status: 'ok' },
  }, context, capturedAt)[0];
  assert.equal(mcp?.event.action, 'mcp.tool_invoked');
  assert.deepEqual(mcp?.mcp, { server: 'github', tool: 'create_issue' });
  const prefixed = mapOpenClawHook('after_tool_call', { toolName: 'mcp__docs__search', result: {} }, context, capturedAt)[0];
  assert.deepEqual(prefixed?.mcp, { server: 'docs', tool: 'search' });
});

test('records explicit tool failure and running command without retaining errors or outputs', () => {
  const failed = mapOpenClawHook('after_tool_call', {
    toolName: 'exec', toolCallId: 'failed-call', error: new Error('private error text'), result: 'secret output',
  }, context, capturedAt)[0];
  assert.equal(failed?.event.action, 'tool.failed');
  assert.equal(JSON.stringify(failed).includes('private error text'), false);
  assert.equal(JSON.stringify(failed).includes('secret output'), false);
  assert.equal(failed?.severity, 'medium');

  const running = mapOpenClawHook('after_tool_call', {
    toolName: 'exec', result: { status: 'running', partialOutput: 'private tail' },
  }, context, capturedAt)[0];
  assert.equal(running?.event.action, 'command.started');
  assert.equal(JSON.stringify(running).includes('private tail'), false);
});

test('maps token usage and assistant-output digests but excludes message and reasoning text', () => {
  const events = mapOpenClawHook('llm_output', {
    usage: { input: 10, output: 4, cacheRead: 2, cacheWrite: 1, reasoningTokens: 3 },
    output: 'private assistant response', reasoning: 'private chain of thought',
  }, context, capturedAt);
  assert.equal(events.length, 2);
  assert.equal(events[0]?.event.action, 'token.usage');
  assert.deepEqual(events[0]?.gen_ai, { usage: {
    input_tokens: 10, output_tokens: 4, cache_read: { input_tokens: 2 }, cache_creation: { input_tokens: 1 },
    reasoning: { output_tokens: 3 },
  } });
  assert.equal(events[1]?.event.action, 'assistant.message.completed');
  assert.equal(JSON.stringify(events).includes('private assistant response'), false);
  assert.equal(JSON.stringify(events).includes('private chain of thought'), false);
  assert.equal(mapOpenClawHook('llm_output', { usage: { input: -1, output: 1.4 } }, context, capturedAt).length, 0);
});

test('observes compaction and subagent lifecycle, while ignoring unknown or behavior-changing hooks', () => {
  assert.equal(mapOpenClawHook('before_compaction', {}, context, capturedAt)[0]?.event.action, 'session.compacting');
  const [child] = mapOpenClawHook('subagent_spawned', {
    childSessionKey: 'private-child-session', agentId: 'researcher', model: 'gpt-5',
  }, context, capturedAt);
  assert.equal(child?.event.action, 'subagent.started');
  assert.equal(JSON.stringify(child).includes('private-child-session'), false);
  assert.equal(mapOpenClawHook('after_compaction', {}, context, capturedAt)[0]?.event.action, 'session.compacted');
  for (const forbidden of ['before_prompt_build', 'inbound_claim', 'reply_dispatch', 'skill_proposal_evaluate', 'before_install']) {
    assert.deepEqual(mapOpenClawHook(forbidden, {}, context, capturedAt), []);
  }
  assert.deepEqual(mapOpenClawHook('message_received', null, context, capturedAt), []);
});

test('bounds content and never returns a host decision or waits for telemetry', async () => {
  const [oversized] = mapOpenClawHook('message_received', { text: 'x'.repeat(70 << 10) }, context, capturedAt);
  assert.equal((oversized?.content as { omitted: boolean }).omitted, true);

  const handlers = new Map<string, (event: unknown, context?: unknown) => unknown>();
  const sent: Array<Record<string, unknown>> = [];
  const plugin = createThreadforgeOpenClawPlugin(async (event) => { sent.push(event); }, 4);
  plugin.register({ on: (hook: string, handler: (event: unknown, context?: unknown) => unknown) => handlers.set(hook, handler) });
  assert.deepEqual([...handlers.keys()], [...openClawHooks]);
  for (const hook of ['before_prompt_build', 'inbound_claim', 'skill_proposal_evaluate', 'tool_result_persist']) {
    assert.equal(handlers.has(hook), false);
  }
  assert.equal(handlers.get('before_tool_call')?.({ toolName: 'exec', params: { command: 'private' } }, context), undefined);
  assert.equal(handlers.get('message_received')?.({ text: 'private prompt' }, context), undefined);
  await plugin.flush();
  assert.equal(sent.length, 2);
  assert.equal(JSON.stringify(sent).includes('private'), false);
  assert.throws(() => createThreadforgeOpenClawPlugin(async () => {}, 0), /queue limit/);
});

test('drops events after its bounded queue fills without delaying hook callbacks', async () => {
  let release: (() => void) | undefined;
  let started!: () => void;
  const sendStarted = new Promise<void>((resolve) => { started = resolve; });
  const plugin = createThreadforgeOpenClawPlugin(() => new Promise<void>((resolve) => { release = resolve; started(); }), 1);
  const handlers = new Map<string, (event: unknown, context?: unknown) => unknown>();
  plugin.register({ on: (hook: string, handler: (event: unknown, context?: unknown) => unknown) => handlers.set(hook, handler) });
  const handler = handlers.get('session_start');
  assert.ok(handler);
  assert.equal(handler({ sessionId: 'one' }, context), undefined);
  assert.equal(handler({ sessionId: 'two' }, context), undefined);
  assert.equal(plugin.droppedEvents(), 1);
  await sendStarted;
  release?.();
  await plugin.flush();
});

test('default delivery posts only to the loopback event endpoint', async () => {
  const previousFetch = globalThis.fetch;
  const requests: Array<{ url: string; body: string }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(input), body: String(init?.body ?? '') });
    return { status: 202 } as Response;
  }) as typeof fetch;
  try {
    const plugin = createThreadforgeOpenClawPlugin();
    plugin.register({ on: (_hook: string, handler: (event: unknown, context?: unknown) => unknown) => {
      if (_hook === 'session_start') assert.equal(handler({ sessionId: 'private-id' }, context), undefined);
    } });
    await plugin.flush();
    assert.equal(requests.length, 1);
    assert.equal(requests[0]?.url, 'http://127.0.0.1:4318/v1/events');
    assert.equal(requests[0]?.body.includes('private-id'), false);
  } finally {
    globalThis.fetch = previousFetch;
  }
});

test('OpenClaw package and plugin manifest are valid and use the same managed id', async () => {
  const root = fileURLToPath(new URL('../openclaw-plugin/', import.meta.url));
  const packageInfo = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8')) as {
    openclaw: { extensions: string[]; runtimeExtensions: string[] };
  };
  const manifest = JSON.parse(await readFile(path.join(root, 'openclaw.plugin.json'), 'utf8')) as { id: string };
  assert.equal(manifest.id, 'threadforge-observability');
  assert.deepEqual(packageInfo.openclaw.extensions, ['./index.js']);
  assert.deepEqual(packageInfo.openclaw.runtimeExtensions, ['./index.js']);
});
