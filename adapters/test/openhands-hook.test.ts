import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:http';
import test from 'node:test';
import { mapOpenHandsHookPayload, runOpenHandsHook } from '../src/openhands-hook.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

type Receiver = { endpoint: string; events: ThreadforgeEvent[]; close: () => Promise<void> };
const at = new Date('2026-09-24T10:00:00.000Z');

async function createReceiver(): Promise<Receiver> {
  const events: ThreadforgeEvent[] = [];
  const server: Server = createServer((request, response) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => { body += chunk; });
    request.on('end', () => {
      const value = JSON.parse(body) as ThreadforgeEvent | ThreadforgeEvent[];
      events.push(...(Array.isArray(value) ? value : [value]));
      response.writeHead(202);
      response.end('{}');
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test receiver has no TCP address');
  return { endpoint: `http://127.0.0.1:${address.port}/v1/events`, events,
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())) };
}

test('maps OpenHands session, prompt, and stop hooks without retaining identifiers or prompt text', () => {
  const payload = { session_id: 'private-session', working_dir: 'C:/private/project', message: 'private prompt' };
  const start = mapOpenHandsHookPayload('session_start', payload, at);
  const prompt = mapOpenHandsHookPayload('user_prompt_submit', payload, at);
  const stop = mapOpenHandsHookPayload('stop', payload, at);
  assert.deepEqual([start[0]?.event.action, prompt[0]?.event.action, stop[0]?.event.action], [
    'session.started', 'prompt.submitted', 'assistant.turn_stopped',
  ]);
  const encoded = JSON.stringify([start, prompt, stop]);
  for (const secret of ['private-session', 'private prompt', 'C:/private/project']) assert.equal(encoded.includes(secret), false);
  assert.equal(prompt[0]?.harness.name, 'openhands');
  assert.equal(prompt[0]?.harness.collection_method, 'hook');
  assert.equal((prompt[0]?.content as { included?: boolean }).included, false);
});

test('pre-tool observation records only invocation metadata and never infers permission approval', () => {
  const [event] = mapOpenHandsHookPayload('pre_tool_use', {
    session_id: 'private-session', tool_name: 'terminal', tool_input: { command: 'private command' },
  }, at);
  assert.equal(event?.event.action, 'tool.invoked');
  assert.equal(event?.approval, undefined);
  assert.equal(JSON.stringify(event).includes('private command'), false);
});

test('terminal result preserves nonzero exit status as a command outcome rather than tool failure', () => {
  const [event] = mapOpenHandsHookPayload('post_tool_use', {
    tool_name: 'terminal', tool_input: { command: 'npm test --private' },
    tool_response: { kind: 'TerminalToolObservation', exit_code: 1, content: [{ type: 'text', text: 'private test output' }] },
  }, at);
  assert.equal(event?.event.action, 'command.executed');
  assert.equal(event?.severity, 'info');
  assert.equal((event?.command as { exit_code?: number }).exit_code, 1);
  assert.equal(JSON.stringify(event).includes('npm test --private'), false);
  assert.equal(JSON.stringify(event).includes('private test output'), false);
});

test('classifies OpenHands editor operations and apply_patch per changed file using hashed paths', () => {
  const viewed = mapOpenHandsHookPayload('post_tool_use', {
    tool_name: 'file_editor', tool_input: { command: 'view', path: 'C:/private/readme.md' }, tool_response: { kind: 'FileEditorObservation' },
  }, at)[0];
  assert.equal(viewed?.event.action, 'file.read');
  assert.equal(JSON.stringify(viewed).includes('readme.md'), false);

  const changed = mapOpenHandsHookPayload('post_tool_use', {
    tool_name: 'apply_patch', tool_input: {}, tool_response: { kind: 'ApplyPatchObservation', commit: { changes: {
      'C:/private/a.ts': { old_content: 'private old', new_content: 'private new' },
      'C:/private/b.ts': { old_content: '', new_content: 'private added', move_path: 'C:/private/c.ts' },
    } } },
  }, at);
  assert.equal(changed.length, 2);
  assert.deepEqual(changed.map((event) => event.event.action), ['file.modified', 'file.modified']);
  const encoded = JSON.stringify(changed);
  for (const secret of ['C:/private', 'private old', 'private new', 'private added']) assert.equal(encoded.includes(secret), false);
  assert.equal((changed[0]?.file as { after_sha256?: string }).after_sha256 !== undefined, true);
});

test('uses explicit MCP observation kind and reports tool failures without creating file edits', () => {
  const mcp = mapOpenHandsHookPayload('post_tool_use', {
    tool_name: 'search', tool_input: { query: 'private query' }, tool_response: { kind: 'MCPToolObservation', content: [] },
  }, at)[0];
  assert.equal(mcp?.event.action, 'mcp.tool_invoked');

  const failed = mapOpenHandsHookPayload('post_tool_use', {
    tool_name: 'file_editor', tool_input: { command: 'str_replace', path: '/private/file' },
    tool_response: { is_error: true, old_content: 'not changed', new_content: 'wrong' },
  }, at)[0];
  assert.equal(failed?.event.action, 'tool.failed');
  assert.equal(failed?.file, undefined);
  assert.equal(failed?.error !== undefined, true);
  assert.equal(JSON.stringify(failed).includes('not changed'), false);
});

test('ignores unsupported hooks and drops invalid or remote telemetry input safely', async () => {
  assert.deepEqual(mapOpenHandsHookPayload('permission_request', { tool_name: 'x' }, at), []);
  const receiver = await createReceiver();
  try {
    const chunks = async function* () { yield Buffer.from(JSON.stringify({ session_id: 'private', working_dir: '/private' })); };
    await runOpenHandsHook('session_start', chunks(), receiver.endpoint);
    assert.equal(receiver.events.length, 1);
    const invalid = async function* () { yield Buffer.from('{not-json'); };
    await runOpenHandsHook('session_start', invalid(), receiver.endpoint);
    const remote = async function* () { yield Buffer.from(JSON.stringify({ session_id: 'private' })); };
    await runOpenHandsHook('session_start', remote(), 'https://collector.example/v1/events');
    assert.equal(receiver.events.length, 1);
    assert.equal(await runOpenHandsHook('session_start', invalid(), receiver.endpoint), undefined);
  } finally { await receiver.close(); }
});
