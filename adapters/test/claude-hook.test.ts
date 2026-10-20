import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapClaudeHookPayload } from '../src/claude-hook.ts';

const capturedAt = new Date('2026-09-24T12:00:00.000Z');

test('maps documented Claude lifecycle hooks without copying local paths or content', () => {
  const event = mapClaudeHookPayload({
    hook_event_name: 'SessionStart',
    session_id: 'session-123',
    cwd: 'C:\\private\\project',
    transcript_path: 'C:\\private\\transcript.jsonl',
  }, capturedAt);
  assert.equal(event?.timestamp, capturedAt.toISOString());
  assert.equal(event?.harness.name, 'claude-code');
  assert.equal(event?.harness.collection_method, 'hook');
  assert.deepEqual(event?.event, { kind: 'agent_runtime', action: 'session.started', fidelity: 'observed' });
  assert.deepEqual(event?.session, { id: `claude:${createHash('sha256').update('session-123').digest('hex')}` });
  assert.equal(JSON.stringify(event).includes('session-123'), false);
  assert.equal(JSON.stringify(event).includes('C:\\private'), false);
  assert.equal(JSON.stringify(event).includes('transcript_path'), false);
});

test('records prompt and tool content only as bounded digests', () => {
  const prompt = 'Do not persist this prompt';
  const promptEvent = mapClaudeHookPayload({ hook_event_name: 'UserPromptSubmit', prompt }, capturedAt);
  assert.equal(promptEvent?.event.action, 'prompt.submitted');
  assert.deepEqual(promptEvent?.content, {
    retention: 'metadata', included: false,
    hash: 'c7f3ed825c4e3c2cdc2dead131c670d4bbf9fff01ec90975d7227a20e6be513e',
    bytes: Buffer.byteLength(prompt),
  });
  assert.equal(JSON.stringify(promptEvent).includes(prompt), false);

  const input = { command: 'echo secret-value', nested: { path: 'private/file.txt' } };
  const toolEvent = mapClaudeHookPayload({
    hook_event_name: 'PreToolUse',
    tool_name: 'Bash',
    tool_use_id: 'tool-9',
    tool_input: input,
  }, capturedAt);
  assert.equal(toolEvent?.event.action, 'tool.invoked');
  assert.deepEqual(toolEvent?.tool, { name: 'Bash' });
  assert.deepEqual(toolEvent?.raw, { tool_call_id_sha256: createHash('sha256').update('tool-9').digest('hex') });
  assert.deepEqual(toolEvent?.content, {
    retention: 'metadata', included: false,
    hash: '3ee6679ccd10d22c49317f2046804ee3f020f31e1a97081d3ed9097feaddf75f',
    bytes: Buffer.byteLength(JSON.stringify(input)),
  });
  assert.equal(JSON.stringify(toolEvent).includes('echo secret-value'), false);
  assert.equal(JSON.stringify(toolEvent).includes('private/file.txt'), false);
  const reordered = mapClaudeHookPayload({
    hook_event_name: 'PreToolUse',
    tool_name: 'Bash',
    tool_input: { nested: { path: 'private/file.txt' }, command: 'echo secret-value' },
    tool_use_id: 'tool-9',
  }, capturedAt);
  assert.deepEqual(reordered?.content, toolEvent?.content);
});

test('does not invent tool outcomes, approval decisions, or process exit codes', () => {
  const completed = mapClaudeHookPayload({ hook_event_name: 'PostToolUse', tool_name: 'Bash' }, capturedAt);
  const failed = mapClaudeHookPayload({ hook_event_name: 'PostToolUseFailure', tool_name: 'Bash' }, capturedAt);
  const permission = mapClaudeHookPayload({ hook_event_name: 'PermissionRequest', tool_name: 'Write' }, capturedAt);
  assert.deepEqual(completed?.tool, { name: 'Bash' });
  assert.deepEqual(failed?.tool, { name: 'Bash' });
  assert.equal(completed?.command, undefined);
  assert.equal(failed?.command, undefined);
  assert.deepEqual(permission?.approval, { required: true, reason: 'permission request observed; no decision inferred' });
});

test('maps subagent and stop hooks and ignores unknown or malformed payloads', () => {
  const subagent = mapClaudeHookPayload({ hook_event_name: 'SubagentStart', agent_id: 'agent-1', agent_type: 'Explore' }, capturedAt);
  const stop = mapClaudeHookPayload({ hook_event_name: 'Stop', stop_hook_active: true }, capturedAt);
  assert.deepEqual(subagent?.raw, {
    agent_id_sha256: '6ff3b3bd11c44cac620c43d5b65377bd2ba7e8951c1e835ae40c96733730982b',
    agent_type: 'Explore',
  });
  assert.equal(stop?.event.action, 'assistant.turn_stopped');
  assert.equal(mapClaudeHookPayload({ hook_event_name: 'FutureHook' }, capturedAt), undefined);
  assert.equal(mapClaudeHookPayload(null, capturedAt), undefined);
});

test('omits digests for content beyond the digest threshold', () => {
  const oversized = 'x'.repeat((64 << 10) + 1);
  const event = mapClaudeHookPayload({ hook_event_name: 'UserPromptSubmit', prompt: oversized }, capturedAt);
  assert.deepEqual(event?.content, { retention: 'metadata', included: false, bytes: Buffer.byteLength(oversized) });
  assert.equal(JSON.stringify(event).includes(oversized), false);
});

test('does not retain credential-like tool and agent labels', () => {
  const event = mapClaudeHookPayload({
    hook_event_name: 'PreToolUse',
    tool_name: 'api_token=secret-value',
    tool_input: { safe: true },
  }, capturedAt);
  assert.equal(event?.tool, undefined);
  assert.equal(JSON.stringify(event).includes('secret-value'), false);
});

test('executable hook sends metadata to loopback and exits without affecting the harness', async (t) => {
  let received = '';
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    received = Buffer.concat(chunks).toString('utf8');
    response.writeHead(202);
    response.end();
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  t.after(() => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test listener did not bind');
  const script = fileURLToPath(new URL('../src/claude-hook.ts', import.meta.url));
  const child = spawn(process.execPath, ['--experimental-strip-types', script], {
    env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
    stdio: ['pipe', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  child.stdin.end(JSON.stringify({ hook_event_name: 'UserPromptSubmit', session_id: 'spawned-session', prompt: 'private' }));
  const exitCode = await new Promise<number | null>((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  assert.equal(exitCode, 0);
  assert.equal(stderr, '');
  assert.equal(JSON.parse(received).harness.name, 'claude-code');
  assert.equal(received.includes('private'), false);
});
