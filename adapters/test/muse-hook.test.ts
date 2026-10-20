import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { mapMuseHookPayload } from '../src/muse-hook.ts';

const capturedAt = new Date('2026-09-25T18:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');

test('maps Muse lifecycle, model and prompt metadata without retaining raw text or paths', () => {
  const start = mapMuseHookPayload({
    session_id: 'private-session', cwd: '/private/project', model: 'muse-spark-1.3', source: 'startup',
    transcript_path: '/private/transcripts/session.jsonl',
  }, 'SessionStart', capturedAt);
  assert.equal(start?.event.action, 'session.started');
  assert.deepEqual(start?.session, { id: `muse:${sha256('private-session')}` });
  assert.deepEqual(start?.raw, { source: 'startup' });
  assert.equal(start?.harness.name, 'muse_code');
  assert.equal(start?.harness.version, 'muse-spark-1.3');
  for (const privateValue of ['private-session', '/private/project', '/private/transcripts/session.jsonl']) {
    assert.equal(JSON.stringify(start).includes(privateValue), false);
  }

  const prompt = mapMuseHookPayload({ prompt: 'private prompt', turn_id: 'private-turn' }, 'UserPromptSubmit', capturedAt);
  assert.equal(prompt?.event.action, 'prompt.submitted');
  assert.deepEqual(prompt?.content, { retention: 'metadata', included: false, prompt_sha256: sha256('private prompt'), prompt_bytes: 14 });
  assert.deepEqual(prompt?.raw, { turn_id_sha256: sha256('private-turn') });
  assert.equal(JSON.stringify(prompt).includes('private prompt'), false);
});

test('observes permission requests and successful tool classes without guessing subagent file actions', () => {
  const request = mapMuseHookPayload({ tool_name: 'bash', tool_use_id: 'call', permission_mode: 'default' }, 'PermissionRequest', capturedAt);
  assert.equal(request?.event.action, 'permission.requested');
  assert.deepEqual(request?.approval, { required: true, reason: 'permission request observed; no decision inferred' });
  assert.equal(JSON.stringify(request).includes('permissionDecision'), false);

  const edit = mapMuseHookPayload({
    tool_name: 'file_write', tool_use_id: 'write-id', tool_input: { file_path: '/private/file.txt', content: 'secret text' },
    tool_response: { success: true },
  }, 'PostToolUse', capturedAt);
  assert.equal(edit?.event.action, 'file.modified');
  assert.deepEqual(edit?.file, { operation: 'modify' });
  assert.equal(JSON.stringify(edit).includes('/private/file.txt'), false);
  assert.equal(JSON.stringify(edit).includes('secret text'), false);
  assert.equal(JSON.stringify(edit).includes('write-id'), false);

  const command = mapMuseHookPayload({ tool_name: 'bash' }, 'PostToolUse', capturedAt);
  assert.equal(command?.event.action, 'command.executed');
  const subagentRead = mapMuseHookPayload({ tool_name: 'subagent_read_result', tool_input: { path: '/private' } }, 'PostToolUse', capturedAt);
  assert.equal(subagentRead?.event.action, 'tool.completed');
  assert.equal(subagentRead?.file, undefined);
  assert.equal(mapMuseHookPayload({ tool_name: 'bash' }, 'PostToolUseFailure')?.event.action, 'tool.failed');
});

test('handles stop, compaction and subagent signals; omits full-message model calls', () => {
  const stop = mapMuseHookPayload({ last_assistant_message: 'private answer', session_id: 'child-session' }, 'Stop', capturedAt);
  assert.equal(stop?.event.action, 'assistant.turn_stopped');
  assert.equal(JSON.stringify(stop).includes('private answer'), false);
  assert.equal((stop?.content as { response_sha256: string }).response_sha256, sha256('private answer'));
  assert.equal(mapMuseHookPayload({ session_id: 'child-session' }, 'SubagentStart')?.event.action, 'subagent.started');
  assert.equal(mapMuseHookPayload({}, 'PreCompact')?.event.action, 'session.compacting');
  assert.equal(mapMuseHookPayload({}, 'PostCompact')?.event.action, 'session.compacted');
  assert.equal(mapMuseHookPayload({ messages: [{ content: 'full transcript' }] }, 'PreLLMCall'), undefined);
  assert.equal(mapMuseHookPayload({ messages: [{ content: 'full transcript' }] }, 'PostLLMCall'), undefined);
  const oversizedPrompt = mapMuseHookPayload({ prompt: 'x'.repeat((64 << 10) + 1) }, 'UserPromptSubmit')?.content as { prompt_sha256?: string } | undefined;
  assert.equal(oversizedPrompt?.prompt_sha256, undefined);
});

test('Muse command hook delivers locally and emits an empty no-decision response', async () => {
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
    const hook = fileURLToPath(new URL('../src/muse-hook.ts', import.meta.url));
    const child = spawn(process.execPath, ['--experimental-strip-types', hook, '--threadforge-hook=muse', '--hook-event=UserPromptSubmit'], {
      env: { ...process.env, THREADFORGE_EVENT_ENDPOINT: `http://127.0.0.1:${address.port}/v1/events` },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on('data', (chunk) => stdout.push(Buffer.from(chunk)));
    child.stderr.on('data', (chunk) => stderr.push(Buffer.from(chunk)));
    child.stdin.end(JSON.stringify({ hook_event_name: 'UserPromptSubmit', session_id: 'private-session', prompt: 'do not output me' }));
    const exitCode = await new Promise<number | null>((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    assert.equal(exitCode, 0, Buffer.concat(stderr).toString('utf8'));
    assert.equal(Buffer.concat(stdout).toString('utf8'), '{}\n');
    assert.equal(received.length, 1);
    assert.equal((received[0] as { harness: { name: string } }).harness.name, 'muse_code');
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
});
