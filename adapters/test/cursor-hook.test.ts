import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapCursorHookPayload } from '../src/cursor-hook.ts';

const capturedAt = new Date('2026-09-24T13:00:00.000Z');

test('maps only the installed non-blocking Cursor hook subset', () => {
  const started = mapCursorHookPayload({ hook_event_name: 'sessionStart', conversation_id: 'conv-1', cwd: 'C:\\private\\repo' }, capturedAt);
  assert.equal(started?.event.action, 'session.started');
  assert.deepEqual(started?.session, { id: `cursor:${createHash('sha256').update('conv-1').digest('hex')}` });
  assert.equal(JSON.stringify(started).includes('C:\\private'), false);

  const toolInput = { command: 'echo private', path: 'private/file.txt' };
  const completed = mapCursorHookPayload({
    hook_event_name: 'postToolUse', conversation_id: 'conv-1', tool_name: 'Shell', tool_input: toolInput,
  }, capturedAt);
  assert.equal(completed?.event.action, 'tool.completed');
  assert.deepEqual(completed?.tool, { name: 'Shell' });
  assert.deepEqual(completed?.content, {
    retention: 'metadata', included: false,
    hash: createHash('sha256').update(JSON.stringify({ command: 'echo private', path: 'private/file.txt' })).digest('hex'),
    bytes: Buffer.byteLength(JSON.stringify(toolInput)),
  });
  assert.equal(JSON.stringify(completed).includes('echo private'), false);
  assert.equal(JSON.stringify(completed).includes('private/file.txt'), false);

  const responseText = 'assistant answer that must stay private';
  const response = mapCursorHookPayload({
    hook_event_name: 'afterAgentResponse', conversation_id: 'conv-1', text: responseText,
  }, capturedAt);
  const thoughtText = 'private reasoning that must not be retained';
  const thought = mapCursorHookPayload({
    hook_event_name: 'afterAgentThought', conversation_id: 'conv-1', text: thoughtText, duration_ms: 5000,
  }, capturedAt);
  assert.equal(response?.event.action, 'agent.message');
  assert.equal(thought?.event.action, 'agent.reasoning');
  assert.deepEqual(response?.content, {
    retention: 'metadata', included: false,
    hash: createHash('sha256').update(responseText).digest('hex'),
    bytes: Buffer.byteLength(responseText),
  });
  assert.deepEqual(thought?.content, {
    retention: 'metadata', included: false,
    hash: createHash('sha256').update(thoughtText).digest('hex'),
    bytes: Buffer.byteLength(thoughtText),
  });
  assert.equal(JSON.stringify([response, thought]).includes(responseText), false);
  assert.equal(JSON.stringify([response, thought]).includes(thoughtText), false);
});

test('maps file edits and failures without retaining paths, diffs, or error text', () => {
  const edit = mapCursorHookPayload({
    hook_event_name: 'afterFileEdit', file_path: '/private/repo/main.ts',
    edits: [{ old_string: 'secret old', new_string: 'secret new' }],
  }, capturedAt);
  const failure = mapCursorHookPayload({
    hook_event_name: 'postToolUseFailure', tool_name: 'Shell', failure_type: 'timeout',
    error_message: 'api_token=very-secret', tool_input: { command: 'private command' },
  }, capturedAt);
  assert.equal(edit?.event.action, 'file.modified');
  assert.equal(failure?.event.action, 'tool.failed');
  assert.equal(failure?.severity, 'medium');
  assert.deepEqual(failure?.raw, { failure_type: 'timeout' });
  assert.equal(JSON.stringify([edit, failure]).includes('/private/repo'), false);
  assert.equal(JSON.stringify([edit, failure]).includes('secret old'), false);
  assert.equal(JSON.stringify([edit, failure]).includes('very-secret'), false);
  assert.equal(mapCursorHookPayload({ hook_event_name: 'beforeShellExecution', command: 'rm -rf /' }, capturedAt), undefined);
  assert.equal(mapCursorHookPayload({ hook_event_name: 'beforeSubmitPrompt', prompt: 'private' }, capturedAt), undefined);
});

test('maps preCompact context occupancy as bounded metadata and drops compaction content', () => {
  const compact = mapCursorHookPayload({
    hook_event_name: 'preCompact', conversation_id: 'conv-compact', trigger: 'auto',
    context_usage_percent: 75, context_tokens: 75, context_window_size: 100,
    message_count: 20, messages_to_compact: [{ role: 'user', content: 'private history' }],
    user_message: 'private compaction summary',
  }, capturedAt);
  assert.equal(compact?.event.action, 'context.compaction');
  assert.deepEqual(compact?.gen_ai, { context: { used_tokens: 75, limit_tokens: 100 } });
  assert.equal(compact?.content, undefined);
  assert.equal(JSON.stringify(compact).includes('private history'), false);
  assert.equal(JSON.stringify(compact).includes('private compaction summary'), false);
  assert.equal(JSON.stringify(compact).includes('messages_to_compact'), false);

  for (const [used, limit] of [[-1, 100], [1.5, 100], [101, 100], [75, 0], [75, 1_000_000_001], ['75', 100]]) {
    const invalid = mapCursorHookPayload({
      hook_event_name: 'preCompact', context_tokens: used, context_window_size: limit,
    }, capturedAt);
    assert.equal(invalid?.event.action, 'context.compaction');
    assert.equal(invalid?.gen_ai, undefined);
  }
});

test('executable post-tool hook sends only canonical local event data and prints no permission response', async (t) => {
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
  const script = fileURLToPath(new URL('../src/cursor-hook.ts', import.meta.url));
  const child = spawn(process.execPath, ['--experimental-strip-types', script], {
    env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = '';
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stdin.end(JSON.stringify({ hook_event_name: 'postToolUse', conversation_id: 'conv-1', tool_name: 'Write', tool_input: { content: 'do not retain' } }));
  const code = await new Promise<number | null>((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  assert.equal(code, 0);
  assert.equal(stdout, '');
  assert.equal(JSON.parse(received).harness.name, 'cursor');
  assert.equal(received.includes('do not retain'), false);
});

test('executable reasoning hook emits only a digest and has no Cursor control response', async (t) => {
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
  const script = fileURLToPath(new URL('../src/cursor-hook.ts', import.meta.url));
  const child = spawn(process.execPath, ['--experimental-strip-types', script], {
    env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = '';
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stdin.end(JSON.stringify({ hook_event_name: 'afterAgentThought', conversation_id: 'conv-1', text: 'private thought' }));
  const code = await new Promise<number | null>((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  assert.equal(code, 0);
  assert.equal(stdout, '');
  assert.equal(JSON.parse(received).event.action, 'agent.reasoning');
  assert.equal(JSON.parse(received).content.included, false);
  assert.equal(received.includes('private thought'), false);
});

test('executable preCompact hook emits occupancy only and no Cursor control response', async (t) => {
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
  const script = fileURLToPath(new URL('../src/cursor-hook.ts', import.meta.url));
  const child = spawn(process.execPath, ['--experimental-strip-types', script], {
    env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = '';
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stdin.end(JSON.stringify({
    hook_event_name: 'preCompact', context_tokens: 75, context_window_size: 100,
    user_message: 'must not leave this process',
  }));
  const code = await new Promise<number | null>((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  assert.equal(code, 0);
  assert.equal(stdout, '');
  assert.deepEqual(JSON.parse(received).gen_ai.context, { used_tokens: 75, limit_tokens: 100 });
  assert.equal(received.includes('must not leave this process'), false);
});
