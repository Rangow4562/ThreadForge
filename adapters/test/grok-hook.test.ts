import assert from 'node:assert/strict';
import test from 'node:test';
import { grokHookActions, mapGrokHookPayload, runGrokHook } from '../src/grok-hook.ts';

const time = new Date('2026-09-25T10:00:00.000Z');

test('Grok prompt mapping hashes session, workspace, and prompt without retaining content', () => {
  const event = mapGrokHookPayload({
    hookEventName: 'user_prompt_submit', sessionId: 'local-session-id', workspaceRoot: 'C:\\private\\repo',
    prompt: 'token=private-prompt-value',
  }, 'prompt-submit', time);
  assert.ok(event);
  assert.equal(event.harness.name, 'grok_build');
  assert.equal(event.event.action, 'prompt.submitted');
  assert.equal((event.session as { id: string }).id.length, 'grok:'.length + 64);
  assert.equal((event.project as { id: string }).id.length, 64);
  assert.match((event.content as Record<string, unknown>).prompt_sha256 as string, /^[a-f0-9]{64}$/);
  assert.deepEqual(event.content, {
    retention: 'metadata', included: false,
    prompt_sha256: (event.content as Record<string, unknown>).prompt_sha256,
    prompt_bytes: Buffer.byteLength('token=private-prompt-value'),
  });
  const encoded = JSON.stringify(event);
  assert.equal(encoded.includes('private-prompt-value'), false);
  assert.equal(encoded.includes('C:\\private\\repo'), false);
});

test('Grok tool mapping is observational, digest-first, and classifies command completion/failure', () => {
  const pre = mapGrokHookPayload({
    hookEventName: 'pre_tool_use', sessionId: 's', toolName: 'run_terminal_command',
    toolInput: { command: 'npm test --token=secret-value' },
  }, 'pre-tool', time);
  assert.ok(pre);
  assert.equal(pre.event.action, 'tool.invoked');
  assert.equal(pre.approval, undefined, 'PreToolUse observation must not infer allow/deny');
  assert.equal(JSON.stringify(pre).includes('npm test'), false);

  const post = mapGrokHookPayload({
    hookEventName: 'post_tool_use', toolName: 'run_terminal_command', toolInput: { command: 'npm test' },
  }, 'post-tool', time);
  assert.ok(post);
  assert.equal(post.event.action, 'command.executed');

  const failed = mapGrokHookPayload({
    hookEventName: 'post_tool_use_failure', toolName: 'write_file', error: 'private file contents',
  }, 'post-tool-failure', time);
  assert.ok(failed);
  assert.equal(failed.event.action, 'tool.failed');
  assert.equal(failed.severity, 'high');
  assert.equal(JSON.stringify(failed).includes('private file contents'), false);
});

test('Grok mapper rejects event mismatch and digest size/depth limits fail closed for content', () => {
  assert.equal(mapGrokHookPayload({ hookEventName: 'post_tool_use' }, 'pre-tool', time), undefined);
  assert.equal(mapGrokHookPayload({}, 'not-an-action', time), undefined);
  const deep: Record<string, unknown> = {};
  let node = deep;
  for (let i = 0; i < 12; i++) { node.next = {}; node = node.next as Record<string, unknown>; }
  const event = mapGrokHookPayload({ hookEventName: 'user_prompt_submit', prompt: deep }, 'prompt-submit', time);
  assert.ok(event);
  assert.equal(event.content, undefined, 'bounded digest failure should omit content metadata rather than throw');
  assert.equal(grokHookActions.length, 7);
});

test('Grok hook emits locally and never writes a hook decision to stdout', async () => {
  const sent: string[] = [];
  let stdout = '';
  let stderr = '';
  const fetchImpl: typeof fetch = async (_input, init) => {
    sent.push(String(init?.body));
    return new Response(null, { status: 202 });
  };
  await runGrokHook('pre-tool', bytes(JSON.stringify({
    hookEventName: 'pre_tool_use', sessionId: 'private-session', toolName: 'run_terminal_command',
    toolInput: { command: 'private command' },
  })), { write: (value) => { stdout += value; } }, { write: (value) => { stderr += value; } }, { fetchImpl, now: () => time });
  assert.equal(stdout, '');
  assert.equal(stderr, '');
  assert.equal(sent.length, 1);
  assert.equal(sent[0].includes('private command'), false);

  await runGrokHook('pre-tool', bytes('{}'), { write: (value) => { stdout += value; } }, { write: (value) => { stderr += value; } }, {
    fetchImpl: async () => { throw new Error('connection refused'); }, now: () => time,
  });
  assert.equal(stdout, '');
  assert.match(stderr, /no policy decision was made/);
  assert.equal(stderr.includes('private-session'), false);
});

test('Grok hook bounds stdin and suppresses parse or oversized-payload details', async () => {
  let stdout = '';
  let stderr = '';
  await runGrokHook('prompt-submit', singleChunk(Buffer.alloc((1 << 20) + 1, 0x61)),
    { write: (value) => { stdout += value; } }, { write: (value) => { stderr += value; } });
  assert.equal(stdout, '');
  assert.match(stderr, /capture skipped; no policy decision was made/);
  assert.equal(stderr.includes('aaaa'), false);
});

async function* bytes(value: string): AsyncIterable<Uint8Array> { yield Buffer.from(value, 'utf8'); }
async function* singleChunk(value: Uint8Array): AsyncIterable<Uint8Array> { yield value; }
