import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import {
  createThreadforgeSenpiExtension,
  mapSenpiEvent,
  senpiExtensionEvents,
} from '../src/senpi-extension.ts';

const at = new Date('2026-09-25T12:00:00.000Z');

test('maps all seven Senpi event types under a distinct plugin identity without retaining private content', () => {
  const context = {
    cwd: 'C:/private/repository',
    mode: 'tui',
    model: { provider: 'anthropic', id: 'claude-opus' },
    sessionManager: { getSessionId: () => 'private-session' },
  };
  const cases = [
    [{ type: 'session_start', reason: 'startup' }, ['session.started']],
    [{ type: 'session_shutdown', reason: 'quit' }, ['session.ended']],
    [{ type: 'input', text: 'private prompt', source: 'interactive' }, ['prompt.submitted']],
    [{ type: 'tool_call', toolName: 'bash', toolCallId: 'private-call', input: { command: 'private command' } }, ['tool.invoked']],
    [{ type: 'tool_result', toolName: 'bash', toolCallId: 'private-call', input: { command: 'private command' }, details: { exitCode: 2, output: 'private output' } }, ['command.executed']],
    [{ type: 'user_bash', command: 'operator private command', cwd: 'C:/private/repository' }, ['command.executed']],
    [{ type: 'message_end', message: {
      role: 'assistant', model: { provider: 'anthropic', id: 'claude-opus' },
      content: [{ type: 'thinking', thinking: 'private reasoning' }, { type: 'text', text: 'visible answer' }],
      usage: { input: 120, output: 30, cacheRead: 8, reasoning: 12, cost: { total: 0.0042 } },
    } }, ['token.usage', 'agent.reasoning']],
  ] as const;
  const all = cases.flatMap(([payload, actions]) => {
    const events = mapSenpiEvent(payload, context, at);
    assert.deepEqual(events.map((event) => (event.event as { action: string }).action), actions);
    return events;
  });
  assert.ok(all.every((event) => (event.harness as { name: string }).name === 'omo_senpi'));
  assert.ok(all.every((event) => (event.harness as { collection_method: string }).collection_method === 'plugin'));
  assert.equal((all[0]?.session as { id: string }).id,
    `omo_senpi:${createHash('sha256').update('private-session').digest('hex')}`);
  assert.equal((all[2]?.raw as { input_source: string }).input_source, 'interactive');
  assert.equal((all[4]?.command as { exit_code: number }).exit_code, 2);
  const usage = (all.find((event) => (event.event as { action: string }).action === 'token.usage')?.gen_ai as { usage: Record<string, unknown> }).usage;
  assert.deepEqual(usage, {
    input_tokens: 120,
    output_tokens: 30,
    cache_read: { input_tokens: 8 },
    reasoning: { output_tokens: 12 },
    cost_usd: 0.0042,
  });
  const reasoning = all.find((event) => (event.event as { action: string }).action === 'agent.reasoning');
  const encoded = JSON.stringify(all);
  for (const privateValue of [
    'private-session', 'private-call', 'private prompt', 'private command', 'operator private command',
    'private output', 'private reasoning', 'visible answer', 'C:/private',
  ]) assert.equal(encoded.includes(privateValue), false);
  assert.equal((reasoning?.content as { included: boolean }).included, false);
});

test('maps only marker metadata into a separate Senpi session handoff event', () => {
  const source = 'Continue here. [beacon-handoff from=codex_cli session=source-123]';
  const events = mapSenpiEvent({ type: 'input', text: source }, {
    sessionManager: { getSessionId: () => 'target-session' },
  }, at);
  assert.equal(events.length, 2);
  assert.equal((events[1]!.event as { action: string }).action, 'session.handoff');
  assert.deepEqual(events[1]!.handoff, { source_harness: 'codex_cli', source_session_id: 'source-123' });
  assert.equal(JSON.stringify(events).includes('Continue here'), false);
  assert.equal(JSON.stringify(events).includes('beacon-handoff'), false);
});

test('subscribes only to documented events and never invents approvals', () => {
  const handlers = new Map<string, (event: unknown, context?: unknown) => unknown>();
  const extension = createThreadforgeSenpiExtension();
  extension.register({ on: (name, handler) => { handlers.set(name, handler); } });
  assert.deepEqual(extension.subscribedEvents, [...senpiExtensionEvents]);
  assert.deepEqual([...handlers.keys()], [...senpiExtensionEvents]);
  for (const unknown of ['tool_approval_requested', 'tool_approval_resolved', 'user_python', 'agent_start', 'message_update']) {
    assert.deepEqual(mapSenpiEvent({ type: unknown }), []);
  }
});

test('classifies file tools and explicit failures while hashing file paths and content', () => {
  const cases = [
    [{ type: 'tool_result', toolName: 'read', input: { path: '/private/a.txt' }, details: { content: 'private file' } }, 'file.read', 'read'],
    [{ type: 'tool_result', toolName: 'write', input: { path: '/private/a.txt', content: 'private file' } }, 'file.modified', 'create'],
    [{ type: 'tool_result', toolName: 'edit', input: { path: '/private/a.txt' }, details: { patch: 'private patch' } }, 'file.modified', 'modify'],
    [{ type: 'tool_result', toolName: 'bash', isError: true, details: { output: 'private error' } }, 'tool.failed', undefined],
  ] as const;
  for (const [payload, action, operation] of cases) {
    const event = mapSenpiEvent(payload, undefined, at)[0]!;
    assert.equal((event.event as { action: string }).action, action);
    if (operation) assert.equal((event.file as { operation: string }).operation, operation);
    assert.doesNotMatch(JSON.stringify(event), /private/);
  }
});

test('bounds digests, rejects malformed payloads, and emits loopback-only observations without callback results', async () => {
  const oversized = mapSenpiEvent({ type: 'input', text: 'x'.repeat((64 << 10) + 1) }, undefined, at)[0]!;
  const content = oversized.content as { prompt_omitted?: boolean; prompt_sha256?: string };
  assert.equal(content.prompt_omitted, true);
  assert.equal(content.prompt_sha256, undefined);
  assert.deepEqual(mapSenpiEvent({ type: 'unknown' }, undefined, at), []);
  assert.deepEqual(mapSenpiEvent({ type: 'message_end', message: { role: 'user', content: [] } }, undefined, at), []);

  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; body: string }> = [];
  globalThis.fetch = (async (url, init) => {
    requests.push({ url: String(url), body: String(init?.body) });
    return new Response('{}', { status: 202 });
  }) as typeof fetch;
  try {
    const handlers = new Map<string, (event: unknown, context?: unknown) => unknown>();
    createThreadforgeSenpiExtension().register({ on: (name, handler) => { handlers.set(name, handler); } });
    const result = handlers.get('input')?.({ type: 'input', text: 'private prompt' });
    assert.equal(result, undefined);
    await new Promise<void>((resolve) => setImmediate(resolve));
    assert.equal(requests.length, 1);
    assert.equal(requests[0]?.url, 'http://127.0.0.1:4318/v1/events');
    assert.equal(JSON.parse(requests[0]!.body).harness.name, 'omo_senpi');
    assert.doesNotMatch(requests[0]!.body, /private prompt/);
  } finally { globalThis.fetch = originalFetch; }
});
