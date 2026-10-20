import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import { PassThrough } from 'node:stream';
import { deepseekHookEvents, mapDeepSeekHookPayload, runDeepSeekHook } from '../src/deepseek-hook.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

const at = new Date('2026-09-25T12:00:00.000Z');

test('maps only the seven documented Claude-bridge events to DeepSeek Harness identity', () => {
  const payloads = [
    { hook_event_name: 'SessionStart', session_id: 'private-session', source: 'startup' },
    { hook_event_name: 'UserPromptSubmit', session_id: 'private-session', prompt: 'private prompt' },
    { hook_event_name: 'PreToolUse', session_id: 'private-session', tool_name: 'bash', tool_use_id: 'call-private', tool_input: { command: 'private command' } },
    { hook_event_name: 'PostToolUse', session_id: 'private-session', tool_name: 'bash', tool_use_id: 'call-private', tool_response: 'private output' },
    { hook_event_name: 'Stop', session_id: 'private-session' },
    { hook_event_name: 'SubagentStart', session_id: 'private-session', agent_id: 'private-agent', agent_type: 'general-purpose' },
    { hook_event_name: 'SubagentStop', session_id: 'private-session', agent_id: 'private-agent', agent_type: 'general-purpose' },
  ];
  const events = payloads.map((payload) => mapDeepSeekHookPayload(payload, at));
  assert.equal(events.length, deepseekHookEvents.length);
  assert.deepEqual(events.map((event) => event?.harness.name), Array(7).fill('deepseek_harness'));
  assert.deepEqual(events.map((event) => event?.harness.collection_method), Array(7).fill('hook'));
  const sessionHash = createHash('sha256').update('private-session', 'utf8').digest('hex');
  assert.equal((events[0]?.session as { id: string }).id, `dsh:${sessionHash}`);
  assert.equal(events[6]?.event.action, 'subagent.stopped');
  const serialized = JSON.stringify(events);
  for (const privateValue of ['private-session', 'private prompt', 'private command', 'private output', 'private-agent', 'call-private']) {
    assert.equal(serialized.includes(privateValue), false);
  }
  for (const unsupported of ['PermissionRequest', 'SessionEnd', 'PreCompact']) {
    assert.equal(mapDeepSeekHookPayload({ hook_event_name: unsupported, prompt: 'must not pass' }, at), undefined);
  }
});

test('hook runner emits locally and always returns an empty no-opinion response', async () => {
  const input = new PassThrough();
  const output: string[] = [];
  const errors: string[] = [];
  const events: ThreadforgeEvent[] = [];
  input.end(JSON.stringify({ hook_event_name: 'PreToolUse', session_id: 'private-session', tool_name: 'bash', tool_input: { command: 'secret command' } }));
  await runDeepSeekHook('PreToolUse', input, { write: (value) => output.push(value) }, { write: (value) => errors.push(value) }, {
    now: () => at,
    fetchImpl: (async (url, init) => {
      assert.equal(String(url), 'http://127.0.0.1:4318/v1/events');
      const body = JSON.parse(String(init?.body)) as ThreadforgeEvent;
      events.push(body);
      return new Response('{}', { status: 202 });
    }) as typeof fetch,
  });
  assert.deepEqual(output, ['{}\n']);
  assert.deepEqual(errors, []);
  assert.equal(events[0]?.harness.name, 'deepseek_harness');
  assert.equal(JSON.stringify(events).includes('secret command'), false);
});

test('collector failures and mismatched or oversized payloads fail open without exposing input', async () => {
  for (const [eventName, body] of [
    ['PreToolUse', JSON.stringify({ hook_event_name: 'SessionEnd', secret: 'private-value' })],
    ['PreToolUse', 'x'.repeat((1 << 20) + 1)],
  ] as const) {
    const input = new PassThrough();
    const output: string[] = [];
    const errors: string[] = [];
    input.end(body);
    await runDeepSeekHook(eventName, input, { write: (value) => output.push(value) }, { write: (value) => errors.push(value) });
    assert.deepEqual(output, ['{}\n']);
    assert.equal(errors.join('').includes('private-value'), false);
  }

  const input = new PassThrough();
  const output: string[] = [];
  const errors: string[] = [];
  input.end(JSON.stringify({ hook_event_name: 'PreToolUse', session_id: 'private-session' }));
  await runDeepSeekHook('PreToolUse', input, { write: (value) => output.push(value) }, { write: (value) => errors.push(value) }, {
    fetchImpl: (async () => { throw new Error('offline'); }) as typeof fetch,
  });
  assert.deepEqual(output, ['{}\n']);
  assert.equal(errors.join('').includes('offline'), false);
});
