import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:http';
import test from 'node:test';
import {
  hermesHookEvents,
  hermesManagedMarker,
  mapHermesHookPayload,
  renderHermesHookConfig,
  runHermesHook,
} from '../src/hermes-hook.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

const at = new Date('2026-09-25T10:00:00.000Z');

test('maps lifecycle and prompt hooks while hashing private identifiers, paths, and prompt content', () => {
  const payload = {
    session_id: 'private-session-id', cwd: 'C:/private/project',
    extra: { user_message: 'private prompt text', model: 'local-model' },
  };
  const started = mapHermesHookPayload('on_session_start', payload, at)[0];
  const prompt = mapHermesHookPayload('pre_llm_call', payload, at)[0];
  const ended = mapHermesHookPayload('on_session_finalize', payload, at)[0];
  assert.deepEqual([started?.event.action, prompt?.event.action, ended?.event.action], [
    'session.started', 'prompt.submitted', 'session.ended',
  ]);
  assert.equal(prompt?.harness.name, 'hermes');
  assert.equal(prompt?.harness.collection_method, 'hook');
  assert.equal((prompt?.content as { included?: boolean }).included, false);
  const encoded = JSON.stringify([started, prompt, ended]);
  for (const secret of ['private-session-id', 'private prompt text', 'C:/private/project']) assert.equal(encoded.includes(secret), false);
});

test('records invocation and command outcomes without retaining input or output text', () => {
  const invoked = mapHermesHookPayload('pre_tool_call', {
    session_id: 'session-a', tool_call_id: 'call-a', tool_name: 'terminal',
    tool_input: { command: 'echo private-input' },
  }, at)[0];
  assert.equal(invoked?.event.action, 'tool.invoked');
  assert.equal(invoked?.approval, undefined);

  const completed = mapHermesHookPayload('post_tool_call', {
    tool_name: 'terminal', tool_call_id: 'call-a', tool_input: { command: 'echo private-input' },
    extra: { result: JSON.stringify({ exit_code: 1, output: 'private-output' }) },
  }, at)[0];
  assert.equal(completed?.event.action, 'command.executed');
  assert.equal(completed?.severity, 'info');
  assert.equal((completed?.command as { exit_code?: number }).exit_code, 1);
  const failed = mapHermesHookPayload('post_tool_call', {
    tool_name: 'terminal', tool_input: { command: 'private-command' },
    extra: { result: { is_error: true, error: 'password: private-value' } },
  }, at)[0];
  assert.equal(failed?.event.action, 'tool.failed');
  assert.deepEqual(failed?.error, { type: 'tool_error' });
  const encoded = JSON.stringify([invoked, completed, failed]);
  for (const secret of ['echo private-input', 'private-output', 'private-command', 'password: private-value']) assert.equal(encoded.includes(secret), false);
});

test('approval outcome is recorded only from explicit response fields', () => {
  const requested = mapHermesHookPayload('pre_approval_request', {
    extra: { command: 'private command', description: 'private reason' },
  }, at)[0];
  assert.equal(requested?.event.action, 'approval.requested');
  assert.deepEqual((requested?.approval as { decision: string }).decision, 'requested');

  const denied = mapHermesHookPayload('post_approval_response', { extra: { choice: 'deny' } }, at)[0];
  assert.equal(denied?.event.action, 'approval.denied');
  assert.equal((denied?.approval as { decision: string }).decision, 'deny');

  const ambiguous = mapHermesHookPayload('post_approval_response', { extra: { command: 'not evidence' } }, at)[0];
  assert.equal(ambiguous?.event.action, 'approval.requested');
  assert.equal((ambiguous?.approval as { decision: string }).decision, 'unknown');
  assert.equal(JSON.stringify([requested, denied, ambiguous]).includes('private command'), false);
});

test('maps subagent metadata and rejects unsupported or malformed hook payloads', () => {
  const [event] = mapHermesHookPayload('subagent_stop', {
    session_id: 'parent', extra: { child_session_id: 'child', child_role: 'researcher', child_status: 'done', duration_ms: 10 },
  }, at);
  assert.equal(event?.event.action, 'subagent.stopped');
  assert.equal((event?.raw as { subagent: { role: string; duration_ms: number } }).subagent.role, 'researcher');
  assert.equal(JSON.stringify(event).includes('child'), false);
  assert.deepEqual(mapHermesHookPayload('unknown_event', {}, at), []);
  assert.deepEqual(mapHermesHookPayload('pre_tool_call', null, at), []);
});

test('renders an opt-in YAML fragment and rejects ambiguous or credential-bearing commands', () => {
  const config = renderHermesHookConfig(`threadforge adapter --event {event} ${hermesManagedMarker}`);
  for (const event of hermesHookEvents) assert.match(config, new RegExp(`^  ${event}:$`, 'm'));
  assert.match(config, /timeout: 30/);
  assert.match(config, /matcher: "\.\*"/);
  assert.equal(config.includes('hooks_auto_accept'), false);
  assert.throws(() => renderHermesHookConfig(`one {event} two {event} ${hermesManagedMarker}`), /exactly one/);
  assert.throws(() => renderHermesHookConfig(`threadforge {event} ${hermesManagedMarker} api_key=private-value`), /credentials/);
});

test('hook runner emits only to a loopback receiver and fails open without stdout data', async () => {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => { body += chunk; });
    request.on('end', () => {
      const parsed = JSON.parse(body) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(parsed) ? parsed : [parsed]));
      response.writeHead(202);
      response.end('{}');
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test receiver has no TCP address');
  try {
    const input = async function* () { yield Buffer.from(JSON.stringify({ session_id: 'private-session' })); };
    await runHermesHook('on_session_start', input(), `http://127.0.0.1:${address.port}/v1/events`);
    assert.equal(events.length, 1);
    assert.equal(JSON.stringify(events).includes('private-session'), false);
    await runHermesHook('on_session_start', input(), 'https://collector.example/v1/events');
    assert.equal(events.length, 1);
    const oversized = async function* () { yield Buffer.alloc((1 << 20) + 1); };
    await runHermesHook('on_session_start', oversized(), `http://127.0.0.1:${address.port}/v1/events`);
    assert.equal(events.length, 1);
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
