import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapAntigravityHookPayload } from '../src/antigravity-hook.ts';

const capturedAt = new Date('2026-09-25T12:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');

test('maps Antigravity invocation metadata without retaining prompt text, workspace paths, or conversation IDs', () => {
  const event = mapAntigravityHookPayload({
    conversationId: 'private-conversation',
    workspacePaths: ['C:\\Users\\Alice\\secret-project'],
    prompt: 'summarize token=do-not-retain',
  }, 'PreInvocation', capturedAt);
  assert.equal(event?.timestamp, capturedAt.toISOString());
  assert.equal(event?.harness.name, 'antigravity_cli');
  assert.equal(event?.event.action, 'agent.invocation_started');
  assert.deepEqual(event?.session, { id: `antigravity:${sha256('private-conversation')}` });
  assert.equal(event?.content, undefined);
  const serialized = JSON.stringify(event);
  for (const secret of ['private-conversation', 'Alice', 'secret-project', 'do-not-retain']) {
    assert.equal(serialized.includes(secret), false);
  }
});

test('maps invocation, observational tool, file, command, and failure events without returning decisions', () => {
  const input = { conversationId: 'session-9', toolCall: { name: 'write_file', args: { Path: '/repo/private.txt', content: 'secret text' } } };
  const started = mapAntigravityHookPayload({ conversationId: 'session-9' }, 'PreInvocation', capturedAt);
  assert.equal(started?.event.action, 'agent.invocation_started');

  const completed = mapAntigravityHookPayload(input, 'PostToolUse', capturedAt);
  assert.equal(completed?.event.action, 'file.modified');
  assert.deepEqual(completed?.tool, { name: 'write_file' });
  assert.equal(JSON.stringify(completed).includes('/repo/private.txt'), false);
  assert.equal(JSON.stringify(completed).includes('secret text'), false);

  const failed = mapAntigravityHookPayload({ toolCall: { name: 'run_command', args: { CommandLine: 'private command' } }, error: 'secret provider output' }, 'PostToolUse', capturedAt);
  assert.equal(failed?.event.action, 'tool.failed');
  assert.equal(failed?.severity, 'medium');
  assert.equal(JSON.stringify(failed).includes('secret provider output'), false);
  assert.equal(mapAntigravityHookPayload({}, 'PostInvocation', capturedAt)?.event.action, 'agent.invocation_completed');
  assert.equal(mapAntigravityHookPayload({}, 'Stop', capturedAt)?.event.action, 'assistant.turn_stopped');
  assert.equal(mapAntigravityHookPayload(input, 'PreToolUse', capturedAt), undefined);
  assert.equal(mapAntigravityHookPayload({}, 'UserPromptSubmit', capturedAt), undefined);
  assert.equal(mapAntigravityHookPayload({}, 'PermissionRequest', capturedAt), undefined);
});

test('bounds digests, suppresses secret-like labels, and ignores invalid payloads', () => {
  const event = mapAntigravityHookPayload({
    toolCall: { name: 'api_token=secret-value', args: 'x'.repeat((64 << 10) + 1) },
  }, 'PostToolUse', capturedAt);
  assert.equal(event?.tool, undefined);
  assert.deepEqual(event?.content, { retention: 'metadata', included: false, bytes: (64 << 10) + 1, omitted: true });
  assert.equal(mapAntigravityHookPayload([], 'PreToolUse', capturedAt), undefined);
});

test('Antigravity hook sends only loopback telemetry and emits no host response body', async () => {
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
    const hook = fileURLToPath(new URL('../src/antigravity-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook, '--hook-event=PostToolUse'], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const output: Buffer[] = [];
    const errors: Buffer[] = [];
    child.stdout.on('data', (chunk) => output.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => errors.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ conversationId: 'session-1', toolCall: { name: 'run_command', args: { CommandLine: 'do not retain' } } }));
    const code = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(code, 0, Buffer.concat(errors).toString('utf8'));
    assert.equal(Buffer.concat(output).toString('utf8'), '');
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'antigravity_cli');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
