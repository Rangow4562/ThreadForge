import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapKiroHookPayload } from '../src/kiro-hook.ts';

const capturedAt = new Date('2026-09-25T17:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');

test('maps Kiro lifecycle and prompt events without retaining IDs, prompt text, or paths', () => {
  const started = mapKiroHookPayload({ session_id: 'private-session', cwd: 'C:/private/workspace' }, 'SessionStart', capturedAt);
  assert.equal(started?.event.action, 'session.started');
  assert.deepEqual(started?.session, { id: `kiro:${sha256('private-session')}` });
  assert.equal(started?.harness.name, 'kiro');
  assert.equal(JSON.stringify(started).includes('private-session'), false);
  assert.equal(JSON.stringify(started).includes('private/workspace'), false);
  assert.equal(mapKiroHookPayload({ session_id: 'cli-session' }, 'AgentSpawn', capturedAt)?.event.action, 'session.started');

  const prompt = mapKiroHookPayload({ session_id: 'private-session', prompt: 'private prompt body', cwd: '/private/workspace' }, 'UserPromptSubmit', capturedAt);
  assert.equal(prompt?.event.action, 'prompt.submitted');
  assert.deepEqual(prompt?.content, {
    retention: 'metadata', included: false,
    prompt_sha256: sha256('private prompt body'), prompt_bytes: Buffer.byteLength('private prompt body'),
  });
  assert.equal(JSON.stringify(prompt).includes('private prompt body'), false);
  assert.equal(JSON.stringify(prompt).includes('/private/workspace'), false);
});

test('classifies Kiro tools by exact names and suppresses raw tool inputs, paths, and results', () => {
  const input = { operations: [{ mode: 'Line', path: '/private/main.go' }] };
  const result = { success: true, result: [{ content: 'private file content' }] };
  const read = mapKiroHookPayload({ tool_name: 'read', tool_input: input, tool_response: result, tool_call_id: 'call-id' }, 'PostToolUse', capturedAt);
  assert.equal(read?.event.action, 'file.read');
  assert.deepEqual(read?.file, { operation: 'read' });
  assert.deepEqual(read?.raw, { tool_call_id_sha256: sha256('call-id') });
  assert.deepEqual(read?.content, {
    retention: 'metadata', included: false,
    input_sha256: sha256(JSON.stringify({ operations: [{ mode: 'Line', path: '/private/main.go' }] })),
    input_bytes: Buffer.byteLength(JSON.stringify(input)),
    output_sha256: sha256('{"result":[{"content":"private file content"}],"success":true}'), output_bytes: Buffer.byteLength(JSON.stringify(result)),
  });
  for (const privateValue of ['/private/main.go', 'private file content', 'call-id']) {
    assert.equal(JSON.stringify(read).includes(privateValue), false);
  }

  assert.deepEqual(mapKiroHookPayload({ tool_name: '@postgres/query', tool_response: { success: true } }, 'PostToolUse')?.mcp, { server: 'postgres', tool: 'query' });
  assert.equal(mapKiroHookPayload({ tool_name: 'execute_bash', tool_response: { success: true } }, 'PostToolUse')?.event.action, 'command.executed');
  const appended = mapKiroHookPayload({ tool_name: 'fs_append', tool_response: { success: true } }, 'PostToolUse')?.file as { operation?: string } | undefined;
  const deleted = mapKiroHookPayload({ tool_name: 'delete_file', tool_response: { success: true } }, 'PostToolUse')?.file as { operation?: string } | undefined;
  assert.equal(appended?.operation, 'modify');
  assert.equal(deleted?.operation, 'delete');
  assert.equal(mapKiroHookPayload({ tool_name: 'delete_file', tool_response: {} }, 'PostToolUse')?.event.action, 'tool.completed');

  const failure = mapKiroHookPayload({ tool_name: 'write', tool_input: { file_path: '/private/file' }, tool_response: { success: false } }, 'PostToolUse');
  assert.equal(failure?.event.action, 'tool.failed');
  assert.equal(failure?.severity, 'medium');
  assert.equal(failure?.file, undefined);
  assert.equal(mapKiroHookPayload({ hook_event_name: 'postToolUse', tool_name: 'read' }, 'FutureTrigger'), undefined);
});

test('hashes bounded Kiro final response and omits content beyond the local digest limit', () => {
  const stop = mapKiroHookPayload({ assistant_response: 'private final answer', session_id: 'session' }, 'Stop', capturedAt);
  assert.equal(stop?.event.action, 'assistant.turn_stopped');
  assert.deepEqual(stop?.content, {
    retention: 'metadata', included: false,
    response_sha256: sha256('private final answer'), response_bytes: Buffer.byteLength('private final answer'),
  });
  assert.equal(JSON.stringify(stop).includes('private final answer'), false);
  assert.equal(mapKiroHookPayload({ assistant_response: 'x'.repeat((64 << 10) + 1) }, 'Stop')?.content, undefined);
});

test('Kiro command hook sends to loopback and produces no stdout model context', async () => {
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
    const hook = fileURLToPath(new URL('../src/kiro-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook, '--threadforge-hook=kiro', '--hook-event=UserPromptSubmit'], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on('data', (chunk) => stdout.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => stderr.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ hook_event_name: 'userPromptSubmit', session_id: 'session-1', prompt: 'must not enter model context' }));
    const exitCode = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(exitCode, 0, Buffer.concat(stderr).toString('utf8'));
    assert.equal(Buffer.concat(stdout).toString('utf8'), '');
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'kiro');
    assert.equal((received[0] as { hook: { name: string } }).hook.name, 'UserPromptSubmit');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
