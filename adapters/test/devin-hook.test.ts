import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { PassThrough } from 'node:stream';
import test from 'node:test';
import { devinHookEvents, mapDevinHookPayload, runDevinHook } from '../src/devin-hook.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

const at = new Date('2026-09-25T12:00:00.000Z');

test('maps the pinned Devin lifecycle, prompt, tool, and permission events without retaining private values', () => {
  const payloads = [
    { hook_event_name: 'SessionStart', session_id: 'private-session', cwd: 'C:/private/repo' },
    { hook_event_name: 'UserPromptSubmit', conversation_id: 'private-session', prompt: 'private prompt', working_dir: 'C:/private/repo' },
    { hook_event_name: 'PreToolUse', sessionId: 'private-session', tool_name: 'edit', tool_use_id: 'private-call', tool_input: { path: 'C:/private/file', content: 'private input' } },
    { hook_event_name: 'PermissionRequest', session_id: 'private-session', tool_name: 'shell', tool_input: { command: 'private command' } },
    { hook_event_name: 'PostToolUse', session_id: 'private-session', tool_name: 'shell', tool_response: 'private output' },
    { hook_event_name: 'Stop', session_id: 'private-session' },
    { hook_event_name: 'SessionEnd', session_id: 'private-session' },
  ];
  const events = payloads.map((payload, index) => mapDevinHookPayload(payload, devinHookEvents[index], at));
  assert.deepEqual(events.map((event) => event?.event.action), [
    'session.started', 'prompt.submitted', 'tool.invoked', 'permission.requested', 'tool.completed',
    'assistant.turn_stopped', 'session.ended',
  ]);
  assert.deepEqual(events.map((event) => event?.harness.name), Array(7).fill('devin_cli'));
  assert.deepEqual(events.map((event) => event?.harness.collection_method), Array(7).fill('hook'));
  assert.equal((events[0]?.session as { id: string }).id,
    `devin:${createHash('sha256').update('private-session').digest('hex')}`);
  assert.equal(events[3]?.approval && (events[3]?.approval as { required: boolean }).required, true);
  assert.equal(JSON.stringify(events[3]).includes('decision'), true);
  assert.equal(JSON.stringify(events[3]).includes('approve'), false);
  const encoded = JSON.stringify(events);
  for (const privateValue of [
    'private-session', 'private prompt', 'private-call', 'C:/private', 'private input', 'private command', 'private output',
  ]) assert.equal(encoded.includes(privateValue), false);
  assert.equal((events[1]?.content as { included: boolean }).included, false);
});

test('rejects unsupported and mismatched hook events instead of trusting payload event names', () => {
  assert.equal(mapDevinHookPayload({ hook_event_name: 'PreToolUse' }, 'PermissionRequest', at), undefined);
  assert.equal(mapDevinHookPayload({ hook_event_name: 'SessionStart' }, 'PostCompaction', at), undefined);
  assert.equal(mapDevinHookPayload({ hookEventName: 'Stop' }, 'Stop', at)?.event.action, 'assistant.turn_stopped');
});

test('hook runner emits only to the local intake, writes no stdout, and fails open', async () => {
  const input = new PassThrough();
  const stdout: string[] = [];
  const stderr: string[] = [];
  const events: ThreadforgeEvent[] = [];
  input.end(JSON.stringify({ hook_event_name: 'PreToolUse', session_id: 'private-session', tool_name: 'shell', tool_input: 'private command' }));
  await runDevinHook('PreToolUse', input, { write: (value) => stdout.push(value) }, { write: (value) => stderr.push(value) }, {
    endpoint: 'http://127.0.0.1:4318/v1/events',
    now: () => at,
    fetchImpl: (async (url, init) => {
      assert.equal(String(url), 'http://127.0.0.1:4318/v1/events');
      events.push(JSON.parse(String(init?.body)) as ThreadforgeEvent);
      return new Response('{}', { status: 202 });
    }) as typeof fetch,
  });
  assert.deepEqual(stdout, []);
  assert.deepEqual(stderr, []);
  assert.equal(events[0]?.harness.name, 'devin_cli');
  assert.equal(JSON.stringify(events).includes('private command'), false);

  const broken = new PassThrough();
  const brokenOut: string[] = [];
  const brokenErr: string[] = [];
  broken.end(JSON.stringify({ hook_event_name: 'SessionEnd', secret: 'not-retained' }));
  await runDevinHook('SessionStart', broken, { write: (value) => brokenOut.push(value) }, { write: (value) => brokenErr.push(value) });
  assert.deepEqual(brokenOut, []);
  assert.equal(brokenErr.join('').includes('not-retained'), false);
});

test('oversized payloads and unavailable local intake never block the host or leak payload text', async () => {
  for (const inputText of ['x'.repeat((1 << 20) + 1), JSON.stringify({ hook_event_name: 'Stop', session_id: 'private' })]) {
    const input = new PassThrough();
    const stdout: string[] = [];
    const stderr: string[] = [];
    input.end(inputText);
    await runDevinHook('Stop', input, { write: (value) => stdout.push(value) }, { write: (value) => stderr.push(value) }, {
      endpoint: 'http://127.0.0.1:4318/v1/events',
      fetchImpl: (async () => { throw new Error('offline'); }) as typeof fetch,
    });
    assert.deepEqual(stdout, []);
    assert.equal(stderr.join('').includes('private'), false);
  }
});
