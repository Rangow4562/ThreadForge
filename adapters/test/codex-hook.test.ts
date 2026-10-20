import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapCodexHookPayload } from '../src/codex-hook.ts';

const capturedAt = new Date('2026-09-24T12:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value).digest('hex');

test('maps Codex session metadata without retaining user identity or local paths', () => {
  const event = mapCodexHookPayload({
    hook_event_name: 'SessionStart', session_id: 'codex-session-9',
    model: 'gpt-5-codex', cwd: 'C:\\Users\\alice\\private-project',
  }, capturedAt);
  assert.equal(event?.timestamp, capturedAt.toISOString());
  assert.equal(event?.harness.name, 'codex-cli');
  assert.equal(event?.event.action, 'session.started');
  assert.deepEqual(event?.session, { id: `codex:${sha256('codex-session-9')}` });
  assert.equal(event?.model, 'gpt-5-codex');
  assert.equal(JSON.stringify(event).includes('codex-session-9'), false);
  assert.equal(JSON.stringify(event).includes('alice'), false);
  assert.equal(JSON.stringify(event).includes('private-project'), false);
});

test('maps tool completion and failure with bounded digests and no raw arguments or results', () => {
  const args = { command: 'echo do-not-retain', nested: { path: 'private/file.txt' } };
  const event = mapCodexHookPayload({
    hook_event_name: 'PostToolUse', session_id: 'thread-1', tool_name: 'shell',
    tool_call_id: 'call-7', tool_input: args, tool_output: 'sensitive result text',
  }, capturedAt);
  assert.equal(event?.event.action, 'tool.completed');
  assert.deepEqual(event?.tool, { name: 'shell' });
  assert.deepEqual(event?.raw, { tool_call_id_sha256: sha256('call-7') });
  assert.deepEqual(event?.content, {
    retention: 'metadata', included: false,
    hash: sha256(JSON.stringify({ command: 'echo do-not-retain', nested: { path: 'private/file.txt' } })),
    bytes: Buffer.byteLength(JSON.stringify(args)),
  });
  const serialized = JSON.stringify(event);
  for (const secret of ['echo do-not-retain', 'private/file.txt', 'sensitive result text', 'thread-1', 'call-7']) {
    assert.equal(serialized.includes(secret), false);
  }

  const failed = mapCodexHookPayload({ hook_event_name: 'PostToolUseFailure', tool_name: 'shell' }, capturedAt);
  assert.equal(failed?.event.action, 'tool.failed');
  assert.equal(failed?.severity, 'medium');
  assert.equal(mapCodexHookPayload({ hook_event_name: 'PreToolUse', tool_name: 'shell' }, capturedAt), undefined);
});

test('oversized content is omitted and secret-like tool labels are suppressed', () => {
  const event = mapCodexHookPayload({
    hook_event_name: 'PostToolUse', tool_name: 'api_key=secret-value',
    tool_input: 'x'.repeat((64 << 10) + 1),
  }, capturedAt);
  assert.deepEqual(event?.content, { retention: 'metadata', included: false, bytes: (64 << 10) + 1, omitted: true });
  assert.equal(event?.tool, undefined);
  assert.equal(JSON.stringify(event).includes('secret-value'), false);
});

test('Codex command hook emits locally and writes only a no-op response for the host', async () => {
  const received: unknown[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    received.push(JSON.parse(Buffer.concat(chunks).toString('utf8')));
    response.writeHead(202, { 'content-type': 'application/json' }).end('{"accepted":1}');
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  assert.ok(address && typeof address === 'object');
  try {
    const hook = fileURLToPath(new URL('../src/codex-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook, '--threadforge-hook=codex'], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const output: Buffer[] = [];
    const errors: Buffer[] = [];
    child.stdout.on('data', (chunk) => output.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => errors.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ hook_event_name: 'SessionStart', session_id: 'session-test' }));
    const code = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(code, 0, Buffer.concat(errors).toString('utf8'));
    assert.equal(Buffer.concat(output).toString('utf8'), '{}\n');
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'codex-cli');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
