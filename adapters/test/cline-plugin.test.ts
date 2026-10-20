import assert from 'node:assert/strict';
import test from 'node:test';
import { createThreadforgeClinePlugin, mapClinePluginPayload } from '../src/cline-plugin.ts';
import type { ThreadforgeEvent } from '../src/emit.ts';

const at = new Date('2026-09-25T12:00:00.000Z');

test('maps Cline run start and prompt while hashing task and workspace identity', () => {
  const events = mapClinePluginPayload('beforeRun', {
    taskId: 'private-task-id', workspaceRoots: ['C:/private/workspace'], apiProvider: 'anthropic',
    modelId: 'claude-opus-5', input: { text: 'private prompt' },
  }, at);
  assert.deepEqual(events.map((event) => event.event.action), ['session.started', 'prompt.submitted']);
  assert.equal(events[0]?.harness.name, 'cline');
  assert.equal(events[0]?.harness.collection_method, 'plugin');
  assert.equal((events[1]?.content as { included?: boolean }).included, false);
  const serialized = JSON.stringify(events);
  for (const secret of ['private-task-id', 'C:/private/workspace', 'private prompt']) assert.equal(serialized.includes(secret), false);
  assert.equal(events[1]?.model, 'claude-opus-5');
  assert.equal((events[1]?.gen_ai as { provider: { name: string } }).provider.name, 'anthropic');
});

test('maps command tools with digests and preserves nonzero exit as an outcome, not a failure', () => {
  const before = mapClinePluginPayload('beforeTool', {
    taskId: 'task-1', toolCall: { id: 'call-1', name: 'execute_command', input: { command: 'npm test --private' } },
  }, at)[0];
  assert.equal(before?.event.action, 'tool.invoked');
  assert.equal(before?.approval, undefined);

  const after = mapClinePluginPayload('afterTool', {
    taskId: 'task-1', toolCall: {
      id: 'call-1', name: 'execute_command', input: { command: 'npm test --private' },
      result: { exit_code: 1, output: 'private output' },
    },
  }, at)[0];
  assert.equal(after?.event.action, 'command.executed');
  assert.equal(after?.severity, 'info');
  assert.equal((after?.command as { exit_code?: number }).exit_code, 1);
  const serialized = JSON.stringify([before, after]);
  for (const secret of ['npm test --private', 'private output']) assert.equal(serialized.includes(secret), false);
});

test('maps file changes and MCP calls without retaining paths or file contents', () => {
  const file = mapClinePluginPayload('afterTool', {
    toolCall: { name: 'replace_in_file', input: {
      path: 'src/private.ts', before: 'private before', after: 'private after',
    } },
  }, at)[0];
  assert.equal(file?.event.action, 'file.modified');
  assert.equal((file?.file as { operation: string }).operation, 'modify');
  assert.equal((file?.file as { path_sha256: string }).path_sha256.length, 64);
  assert.equal((file?.file as { before_content: { included: boolean } }).before_content.included, false);

  const mcp = mapClinePluginPayload('afterTool', {
    toolCall: { name: 'use_mcp_tool', input: { serverName: 'github', toolName: 'create_issue', body: 'private body' } },
  }, at)[0];
  assert.equal(mcp?.event.action, 'mcp.tool_invoked');
  assert.equal((mcp?.mcp as { server: string }).server, 'github');
  const serialized = JSON.stringify([file, mcp]);
  for (const secret of ['src/private.ts', 'private before', 'private after', 'private body']) assert.equal(serialized.includes(secret), false);
});

test('explicit tool errors and reported run outcomes are distinct; no approval is inferred', () => {
  const failed = mapClinePluginPayload('afterTool', {
    toolCall: { name: 'read_file', input: { path: '/private/file' }, result: { isError: true, error: 'private error' } },
  }, at)[0];
  assert.equal(failed?.event.action, 'tool.failed');
  assert.equal(failed?.file, undefined);
  assert.equal(failed?.approval, undefined);

  const cancelled = mapClinePluginPayload('afterRun', { taskId: 'task-1', status: 'aborted', usage: { inputTokens: 12, outputTokens: 4 } }, at)[0];
  assert.equal(cancelled?.event.action, 'session.ended');
  assert.equal((cancelled?.session as { cancel_reason: string }).cancel_reason, 'aborted');
  assert.equal((cancelled?.gen_ai as { usage: { input_tokens: number } }).usage.input_tokens, 12);

  const error = mapClinePluginPayload('afterRun', { status: 'failed', error: { code: 'PRIVATE_CODE', message: 'private message' } }, at)[0];
  assert.equal(error?.event.action, 'session.error');
  assert.equal(JSON.stringify(error).includes('private message'), false);
  assert.equal(JSON.stringify(error).includes('PRIVATE_CODE'), true);
});

test('ignores unknown stages and tolerates cyclic plugin contexts', () => {
  assert.deepEqual(mapClinePluginPayload('streamingToken', { text: 'do not ingest' }, at), []);
  const context: Record<string, unknown> = { taskId: 'cycle-task', toolCall: { name: 'custom_tool' } };
  context.self = context;
  const event = mapClinePluginPayload('afterTool', context, at)[0];
  assert.equal(event?.event.action, 'tool.completed');
  assert.doesNotThrow(() => JSON.stringify(event));
});

test('plugin callbacks only observe, send to the loopback contract, and always resolve undefined', async () => {
  const events: ThreadforgeEvent[] = [];
  const fetchImpl: typeof fetch = async (_input, init) => {
    const payload = JSON.parse(String(init?.body)) as ThreadforgeEvent | ThreadforgeEvent[];
    events.push(...(Array.isArray(payload) ? payload : [payload]));
    return new Response('{}', { status: 202 });
  };
  const plugin = createThreadforgeClinePlugin({ fetchImpl });
  assert.deepEqual(plugin.manifest.capabilities, ['hooks']);
  plugin.setup(undefined, { workspaceInfo: { rootPath: '/private/project' }, clineVersion: 'test-version' });
  assert.equal(await plugin.hooks.beforeRun({ taskId: 'private-task', input: { text: 'private prompt' } }), undefined);
  assert.equal(await plugin.hooks.beforeTool({ taskId: 'private-task', toolCall: { name: 'use_mcp_tool' } }), undefined);
  assert.equal(await plugin.hooks.afterRun({ taskId: 'private-task', status: 'completed' }), undefined);
  assert.deepEqual(events.map((event) => event.event.action), ['session.started', 'prompt.submitted', 'tool.invoked', 'session.ended']);
  assert.equal(JSON.stringify(events).includes('/private/project'), false);
  assert.equal(JSON.stringify(events).includes('private prompt'), false);
  assert.equal(events.every((event) => event.approval === undefined), true);
});

test('plugin ignores collector failure and constrains its send timeout', async () => {
  const plugin = createThreadforgeClinePlugin({ fetchImpl: (async () => { throw new Error('offline'); }) as typeof fetch });
  await assert.doesNotReject(plugin.hooks.beforeRun({ taskId: 'task-1' }));
  assert.throws(() => createThreadforgeClinePlugin({ timeoutMs: 99 }), /between 100 and 10000/);
  assert.throws(() => createThreadforgeClinePlugin({ timeoutMs: 10_001 }), /between 100 and 10000/);
});
