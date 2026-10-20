import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapQwenHookPayload } from '../src/qwen-hook.ts';

const capturedAt = new Date('2026-09-25T15:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');

test('maps Qwen session and prompt metadata without retaining identifiers or prompt text', () => {
  const started = mapQwenHookPayload({ hook_event_name: 'SessionStart', session_id: 'local-session', source: 'startup' }, capturedAt);
  assert.equal(started?.event.action, 'session.started');
  assert.deepEqual(started?.session, { id: `qwen:${sha256('local-session')}` });
  assert.deepEqual(started?.raw, { source: 'startup' });
  assert.equal(started?.harness.name, 'qwen_code');
  const prompt = mapQwenHookPayload({ hook_event_name: 'UserPromptSubmit', session_id: 'local-session', prompt: 'private prompt text' }, capturedAt);
  assert.equal(prompt?.event.action, 'prompt.submitted');
  assert.deepEqual(prompt?.content, {
    retention: 'metadata', included: false, hash: sha256('private prompt text'), bytes: Buffer.byteLength('private prompt text'),
  });
  for (const secret of ['local-session', 'private prompt text']) assert.equal(JSON.stringify(prompt).includes(secret), false);
  const pathSource = mapQwenHookPayload({ hook_event_name: 'SessionStart', source: 'C:/Users/private/project' }, capturedAt);
  assert.equal(pathSource?.raw, undefined);
});

test('classifies Qwen tools and gives failure signals precedence over successful file actions', () => {
  const cases = [
    ['run_shell_command', 'command.executed'], ['read_many_files', 'file.read'], ['glob', 'file.read'],
    ['write_file', 'file.modified'], ['replace', 'file.modified'], ['mcp_edit_tool', 'tool.completed'],
  ];
  for (const [toolName, expected] of cases) {
    const event = mapQwenHookPayload({ hook_event_name: 'PostToolUse', tool_name: toolName, tool_input: { path: '/private/path', command: 'private command' } }, capturedAt);
    assert.equal(event?.event.action, expected, toolName);
    assert.equal(JSON.stringify(event).includes('/private/path'), false);
    assert.equal(JSON.stringify(event).includes('private command'), false);
  }
  for (const payload of [
    { hook_event_name: 'PostToolUseFailure', tool_name: 'write_file' },
    { hook_event_name: 'PostToolUse', tool_name: 'write_file', error: 'private error text' },
    { hook_event_name: 'PostToolUse', tool_name: 'write_file', is_interrupt: true },
  ]) {
    const failed = mapQwenHookPayload(payload, capturedAt);
    assert.equal(failed?.event.action, 'tool.failed');
    assert.equal(failed?.severity, 'medium');
  }
});

test('observes permission requests without granting them and hashes subagent identity', () => {
  const permission = mapQwenHookPayload({ hook_event_name: 'PermissionRequest', tool_name: 'run_shell_command', permission_mode: 'ask' }, capturedAt);
  assert.equal(permission?.event.action, 'permission.requested');
  assert.deepEqual(permission?.approval, { required: true, reason: 'permission request observed; no decision inferred' });
  assert.equal(JSON.stringify(permission).includes('permissionDecision'), false);
  const subagent = mapQwenHookPayload({ hook_event_name: 'SubagentStart', agent_id: 'private-agent-id', agent_type: 'explorer' }, capturedAt);
  assert.deepEqual(subagent?.raw, { agent_id_sha256: sha256('private-agent-id'), agent_type: 'explorer' });
  assert.equal(JSON.stringify(subagent).includes('private-agent-id'), false);
});

test('maps bounded context utilization rather than token spend and suppresses oversized content', () => {
  const stop = mapQwenHookPayload({ hook_event_name: 'Stop', input_tokens: 32_000, context_limit: 64_000, context_usage: 0.5, stop_hook_active: true }, capturedAt);
  assert.deepEqual(stop?.context, { used_tokens: 32_000, limit_tokens: 64_000, utilization: 0.5 });
  assert.equal(stop?.usage, undefined);
  const oversized = mapQwenHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'api_token=secret-value', tool_input: 'x'.repeat((64 << 10) + 1) }, capturedAt);
  assert.equal(oversized?.tool, undefined);
  assert.deepEqual(oversized?.content, { retention: 'metadata', included: false, bytes: (64 << 10) + 1, omitted: true });
  assert.equal(mapQwenHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'C:/Users/private/project' }, capturedAt)?.tool, undefined);
  assert.equal(mapQwenHookPayload({ hook_event_name: 'MessageDisplay' }, capturedAt), undefined);
});

test('Qwen command hook sends local telemetry and only an empty no-decision response', async () => {
  const received: unknown[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    received.push(JSON.parse(Buffer.concat(chunks).toString('utf8')));
    response.writeHead(202).end();
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  assert.ok(address && typeof address === 'object');
  try {
    const hook = fileURLToPath(new URL('../src/qwen-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const output: Buffer[] = [];
    const errors: Buffer[] = [];
    child.stdout.on('data', (chunk) => output.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => errors.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ hook_event_name: 'PreToolUse', session_id: 'qwen-session', tool_name: 'run_shell_command', tool_input: { command: 'not retained' } }));
    const code = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(code, 0, Buffer.concat(errors).toString('utf8'));
    assert.deepEqual(JSON.parse(Buffer.concat(output).toString('utf8')), {});
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'qwen_code');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
