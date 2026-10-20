import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapKimiCodeHookPayload } from '../src/kimi-code-hook.ts';

const capturedAt = new Date('2026-09-25T16:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');

test('maps Kimi Code prompt/session metadata without retaining text, paths, or session IDs', () => {
  const start = mapKimiCodeHookPayload({
    hook_event_name: 'SessionStart', session_id: 'private-session', source: 'startup',
    model: 'kimi-k2', profile: 'default', client_type: 'kimi_code_cli', cwd: 'C:/private/workspace',
  }, capturedAt);
  assert.equal(start?.event.action, 'session.started');
  assert.deepEqual(start?.session, { id: `kimi:${sha256('private-session')}` });
  assert.deepEqual(start?.raw, { source: 'startup', model: 'kimi-k2', profile: 'default' });
  assert.equal(start?.harness.version, 'kimi_code_cli');
  assert.equal(JSON.stringify(start).includes('private-session'), false);
  assert.equal(JSON.stringify(start).includes('private/workspace'), false);

  const prompt = mapKimiCodeHookPayload({
    hook_event_name: 'UserPromptSubmit', session_id: 'private-session', prompt: 'prompt with secret text',
    cwd: '/private/workspace', is_steer: true,
  }, capturedAt);
  assert.equal(prompt?.event.action, 'prompt.submitted');
  assert.equal(JSON.stringify(prompt).includes('prompt with secret text'), false);
  assert.equal(JSON.stringify(prompt).includes('/private/workspace'), false);
  assert.equal((prompt?.content as { prompt_sha256?: string }).prompt_sha256, sha256('prompt with secret text'));
  assert.deepEqual(prompt?.raw, { is_steer: true });
});

test('classifies Kimi tools with closed names and hashes both tool payloads', () => {
  const args = { file_path: '/private/main.ts', old_string: 'old', new_string: 'new secret' };
  const result = { tool_output: 'sensitive output' };
  const edit = mapKimiCodeHookPayload({
    hook_event_name: 'PostToolUse', tool_name: 'Edit', tool_input: args,
    tool_output: result, tool_call_id: 'call-private',
  }, capturedAt);
  assert.equal(edit?.event.action, 'file.modified');
  assert.deepEqual(edit?.file, { operation: 'modify' });
  assert.deepEqual(edit?.raw, { tool_call_id_sha256: sha256('call-private') });
  assert.deepEqual(edit?.content, {
    retention: 'metadata', included: false,
    input_sha256: sha256(JSON.stringify({ file_path: '/private/main.ts', new_string: 'new secret', old_string: 'old' })),
    input_bytes: Buffer.byteLength(JSON.stringify({ file_path: '/private/main.ts', new_string: 'new secret', old_string: 'old' })),
    output_sha256: sha256(JSON.stringify(result)), output_bytes: Buffer.byteLength(JSON.stringify(result)),
  });
  for (const privateValue of ['/private/main.ts', 'new secret', 'sensitive output', 'call-private']) {
    assert.equal(JSON.stringify(edit).includes(privateValue), false);
  }

  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'Bash' }, capturedAt)?.event.action, 'command.executed');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'Grep' }, capturedAt)?.event.action, 'file.read');
  const append = mapKimiCodeHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'Write', tool_input: { mode: 'append' } }, capturedAt);
  assert.deepEqual(append?.file, { operation: 'modify' });
  const mcp = mapKimiCodeHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'mcp__docs__search' }, capturedAt);
  assert.equal(mcp?.event.action, 'mcp.tool_invoked');
  assert.deepEqual(mcp?.mcp, { server: 'docs', tool: 'search' });
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PostToolUseFailure', tool_name: 'Write' }, capturedAt)?.event.action, 'tool.failed');
});

test('records approval outcomes only after PermissionResult and maps lifecycle failures without raw errors', () => {
  const request = mapKimiCodeHookPayload({ hook_event_name: 'PermissionRequest', tool_name: 'Bash' }, capturedAt);
  assert.equal(request?.event.action, 'permission.requested');
  assert.deepEqual(request?.approval, { required: true, reason: 'permission request observed; no decision inferred' });

  const denied = mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult', decision: 'rejected', feedback: 'private reason' }, capturedAt);
  assert.equal(denied?.event.action, 'approval.denied');
  assert.deepEqual(denied?.approval, { required: true, decision: 'deny' });
  assert.equal(JSON.stringify(denied).includes('private reason'), false);
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult', decision: 'approved' }, capturedAt)?.event.action, 'approval.allowed');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult', decision: 'cancelled' }, capturedAt)?.event.action, 'permission.cancelled');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult', decision: 'error' }, capturedAt)?.event.action, 'permission.result_error');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult', decision: 'future-state' }, capturedAt), undefined);
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PermissionResult' }, capturedAt), undefined);
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'StopFailure', error: 'private failure' }, capturedAt)?.event.action, 'assistant.turn_failed');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'Interrupt', reason: 'cancelled by user' }, capturedAt)?.event.action, 'assistant.turn_interrupted');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PreCompact' }, capturedAt)?.event.action, 'session.compacting');
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'PostCompact' }, capturedAt)?.event.action, 'session.compacted');
});

test('hashes background task metadata and omits high-frequency/duplicate events', () => {
  const task = mapKimiCodeHookPayload({
    hook_event_name: 'TaskStarted', task_id: 'private-task', task_type: 'agent', description: 'do private work', cwd: '/private',
  }, capturedAt);
  assert.deepEqual(task?.raw, {
    task_id_sha256: sha256('private-task'), task_type: 'agent',
    description_sha256: sha256('do private work'), description_bytes: Buffer.byteLength('do private work'),
  });
  assert.equal(JSON.stringify(task).includes('private-task'), false);
  assert.equal(mapKimiCodeHookPayload({ hook_event_name: 'Notification', notification_type: 'task.completed' }, capturedAt)?.event.action, 'background_task.notification');
  for (const ignored of ['SessionHeartbeat', 'UserPromptQueued', 'TurnStarted']) {
    assert.equal(mapKimiCodeHookPayload({ hook_event_name: ignored }, capturedAt), undefined);
  }
  const long = mapKimiCodeHookPayload({ hook_event_name: 'UserPromptSubmit', prompt: 'x'.repeat((64 << 10) + 1) }, capturedAt);
  assert.equal(long?.content, undefined);
});

test('Kimi command hook sends loopback events and writes no stdout context', async () => {
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
    const hook = fileURLToPath(new URL('../src/kimi-code-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on('data', (chunk) => stdout.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => stderr.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ hook_event_name: 'UserPromptSubmit', session_id: 'session-1', prompt: 'must not enter context output' }));
    const exitCode = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(exitCode, 0, Buffer.concat(stderr).toString('utf8'));
    assert.equal(Buffer.concat(stdout).toString('utf8'), '');
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'kimi_code');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
