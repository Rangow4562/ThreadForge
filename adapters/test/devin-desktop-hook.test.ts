import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { PassThrough } from 'node:stream';
import test from 'node:test';
import { devinDesktopHookEvents, mapDevinDesktopHookPayload, runDevinDesktopHook } from '../src/devin-desktop-hook.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

const at = new Date('2026-09-25T12:00:00.000Z');

test('maps the five Cascade events to digest-first observational telemetry', () => {
  const payloads = [
    { trajectory_id: 'private-session', agent_action_name: 'pre_user_prompt', user_prompt: 'private prompt', cwd: 'C:/private/repo' },
    { trajectory_id: 'private-session', agent_action_name: 'post_write_code', execution_id: 'private-call', tool_info: { file_path: 'C:/private/file', edits: [{ text: 'private edit' }] } },
    { trajectory_id: 'private-session', agent_action_name: 'post_run_command', execution_id: 'private-call', tool_info: { command: 'private command', exit_code: 7 } },
    { trajectory_id: 'private-session', agent_action_name: 'post_mcp_tool_use', tool_info: { server_name: 'local-server', tool_name: 'read_item', arguments: { key: 'private input' } } },
    { trajectory_id: 'private-session', agent_action_name: 'post_read_code', tool_info: { file_path: 'C:/private/file', content: 'private file content' } },
  ];
  const events = payloads.map((payload, index) => mapDevinDesktopHookPayload(payload, devinDesktopHookEvents[index], at));
  assert.deepEqual(events.map((event) => event?.event.action), [
    'prompt.submitted', 'file.modified', 'command.executed', 'mcp.tool_invoked', 'file.read',
  ]);
  assert.deepEqual(events.map((event) => event?.harness.name), Array(5).fill('devin_desktop'));
  assert.deepEqual(events.map((event) => event?.harness.collection_method), Array(5).fill('hook'));
  assert.equal((events[0]?.session as { id: string }).id,
    `devin-desktop:${createHash('sha256').update('private-session').digest('hex')}`);
  assert.equal((events[2]?.command as { exit_code: number }).exit_code, 7);
  assert.equal((events[3]?.mcp as { tool: string }).tool, 'read_item');
  const encoded = JSON.stringify(events);
  for (const privateValue of [
    'private-session', 'private prompt', 'private-call', 'C:/private', 'private edit', 'private command', 'private input', 'private file content',
  ]) assert.equal(encoded.includes(privateValue), false);
  assert.equal((events[0]?.content as { included: boolean }).included, false);
});

test('rejects unsupported or mismatched Cascade hook events', () => {
  assert.equal(mapDevinDesktopHookPayload({ hook_event_name: 'post_run_command' }, 'post_read_code', at), undefined);
  assert.equal(mapDevinDesktopHookPayload({ agent_action_name: 'pre_user_prompt' }, 'unknown', at), undefined);
  assert.equal(mapDevinDesktopHookPayload({ hookEventName: 'post_write_code' }, 'post_write_code', at)?.event.action, 'file.modified');
});

test('hook runner emits only through local intake, leaves stdout empty, and fails open', async () => {
  const input = new PassThrough();
  const stdout: string[] = [];
  const stderr: string[] = [];
  const events: ThreadforgeEvent[] = [];
  input.end(JSON.stringify({ agent_action_name: 'post_run_command', tool_info: { command: 'private command' } }));
  await runDevinDesktopHook('post_run_command', input, { write: (value) => stdout.push(value) }, { write: (value) => stderr.push(value) }, {
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
  assert.equal(events[0]?.harness.name, 'devin_desktop');
  assert.equal(JSON.stringify(events).includes('private command'), false);

  const malformed = new PassThrough();
  const failedOut: string[] = [];
  const failedErr: string[] = [];
  malformed.end('{bad-json private-value');
  await runDevinDesktopHook('post_read_code', malformed, { write: (value) => failedOut.push(value) }, { write: (value) => failedErr.push(value) });
  assert.deepEqual(failedOut, []);
  assert.equal(failedErr.join('').includes('private-value'), false);
});

test('oversized events and unavailable local intake remain non-blocking', async () => {
  const input = new PassThrough();
  const stdout: string[] = [];
  const stderr: string[] = [];
  input.end('x'.repeat((1 << 20) + 1));
  await runDevinDesktopHook('post_read_code', input, { write: (value) => stdout.push(value) }, { write: (value) => stderr.push(value) });
  assert.deepEqual(stdout, []);
  assert.match(stderr.join(''), /capture skipped/);

  const offline = new PassThrough();
  const offlineErr: string[] = [];
  offline.end(JSON.stringify({ hook_event_name: 'post_read_code', tool_info: { content: 'private' } }));
  await runDevinDesktopHook('post_read_code', offline, { write: () => {} }, { write: (value) => offlineErr.push(value) }, {
    endpoint: 'http://127.0.0.1:4318/v1/events',
    fetchImpl: (async () => { throw new Error('offline'); }) as typeof fetch,
  });
  assert.doesNotMatch(offlineErr.join(''), /private|offline/);
});
