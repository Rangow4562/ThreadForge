import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { addThreadforgeCopilotSDKTelemetry } from '../src/copilot-sdk-telemetry.ts';

async function collector() {
  const events: Array<Record<string, unknown>> = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const value: unknown = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if (Array.isArray(value)) events.push(...value as Array<Record<string, unknown>>);
    else events.push(value as Record<string, unknown>);
    response.writeHead(202, { 'content-type': 'application/json' });
    response.end('{"accepted":1}');
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test collector failed to bind');
  return {
    events,
    endpoint: `http://127.0.0.1:${address.port}/v1/events`,
    async waitFor(count: number) {
      const deadline = Date.now() + 3000;
      while (events.length < count && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 5));
      assert.ok(events.length >= count, `collector did not receive ${count} event(s)`);
    },
    close: () => new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
    }),
  };
}

function action(event: Record<string, unknown>): string {
  return String((event.event as Record<string, unknown>).action);
}

test('Copilot SDK wrappers preserve callback this, values, Promises, errors, and unrelated hooks', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const success = { privateResult: 'PRIVATE_RESULT' };
  const failure = new Error('PRIVATE_CALLBACK_ERROR');
  const originalPromise = Promise.resolve('ended');
  let sessionThis: unknown;
  const hooks = {
    marker: 'original-hooks-object',
    onSessionStart(this: unknown) {
      sessionThis = this;
      return success;
    },
    onSessionEnd: () => originalPromise,
    onPreToolUse() { throw failure; },
    onPostToolUse: () => success,
    unrelated: () => 'untouched',
  };
  const observed = addThreadforgeCopilotSDKTelemetry(hooks, { endpoint: capture.endpoint });
  assert.notStrictEqual(observed.hooks, hooks);
  assert.equal(observed.hooks.marker, hooks.marker);
  assert.equal(observed.hooks.unrelated, hooks.unrelated);
  assert.strictEqual(observed.hooks.onSessionStart?.(), success);
  assert.strictEqual(sessionThis, hooks);
  assert.strictEqual(observed.hooks.onSessionEnd?.(), originalPromise);
  assert.throws(() => observed.hooks.onPreToolUse?.({ toolName: 'bash' }), (error) => error === failure);
  assert.strictEqual(observed.hooks.onPostToolUse?.({ toolName: 'edit' }), success);
  await observed.flush();

  assert.deepEqual(capture.events.map(action), ['session.started', 'session.ended', 'tool.requested', 'tool.completed']);
  assert.equal(JSON.stringify(capture.events).includes('PRIVATE_RESULT'), false);
  assert.equal(JSON.stringify(capture.events).includes('PRIVATE_CALLBACK_ERROR'), false);
  observed.dispose();
});

test('tool events hash only the tool label and do not read arguments, results, paths, or SDK IDs', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  let forbiddenGetterReads = 0;
  const input = Object.defineProperties({ toolName: 'PRIVATE_CUSTOM_TOOL' }, {
    toolArgs: { get() { forbiddenGetterReads += 1; throw new Error('must not read tool args'); } },
    toolResult: { get() { forbiddenGetterReads += 1; throw new Error('must not read tool result'); } },
    error: { get() { forbiddenGetterReads += 1; throw new Error('must not read error'); } },
    cwd: { get() { forbiddenGetterReads += 1; throw new Error('must not read cwd'); } },
    sessionId: { get() { forbiddenGetterReads += 1; throw new Error('must not read session id'); } },
  });
  const hooks = {
    onPreToolUse: () => undefined,
    onPostToolUse: () => undefined,
    onPostToolUseFailure: () => undefined,
  };
  const observed = addThreadforgeCopilotSDKTelemetry(hooks, { endpoint: capture.endpoint });
  observed.hooks.onPreToolUse?.(input);
  observed.hooks.onPostToolUse?.(input);
  observed.hooks.onPostToolUseFailure?.(input);
  await observed.flush();
  await capture.waitFor(3);

  assert.equal(forbiddenGetterReads, 0);
  assert.deepEqual(capture.events.map(action), ['tool.requested', 'tool.completed', 'tool.failed']);
  for (const event of capture.events) {
    const tool = event.tool as Record<string, unknown>;
    assert.match(String(tool.name_sha256), /^[a-f0-9]{64}$/);
    assert.deepEqual(event.harness, { name: 'copilot_sdk', collection_method: 'plugin' });
  }
  const serialized = JSON.stringify(capture.events);
  for (const privateValue of ['PRIVATE_CUSTOM_TOOL', 'toolArgs', 'toolResult', 'sessionId', 'PRIVATE_RESULT']) {
    assert.equal(serialized.includes(privateValue), false, `telemetry leaked ${privateValue}`);
  }
  assert.deepEqual((capture.events[0].raw as Record<string, unknown>), { phase: 'pre_tool_use' });
  observed.dispose();
});

test('telemetry delivery failures do not alter SDK callback results or errors', async () => {
  const result = { decision: 'deny', modifiedArgs: { private: true } };
  const callbackError = new Error('private SDK callback failure');
  const hooks = {
    onPreToolUse: () => result,
    onPostToolUse: () => { throw callbackError; },
  };
  const observed = addThreadforgeCopilotSDKTelemetry(hooks, {
    fetchImpl: async () => { throw new Error('local intake unavailable'); },
  });
  assert.strictEqual(observed.hooks.onPreToolUse?.({ toolName: 'bash' }), result);
  assert.throws(() => observed.hooks.onPostToolUse?.({ toolName: 'bash' }), (error) => error === callbackError);
  await observed.flush();
  observed.dispose();
});

test('bounded queue drops excess telemetry without blocking hook results; dispose stops capture', async () => {
  const resolvers: Array<(response: Response) => void> = [];
  const hooks = { onPreToolUse: (input: { index: number }) => input.index };
  const observed = addThreadforgeCopilotSDKTelemetry(hooks, {
    fetchImpl: (() => new Promise<Response>((resolve) => resolvers.push(resolve))) as typeof fetch,
  });
  for (let index = 0; index < 65; index += 1) {
    assert.equal(observed.hooks.onPreToolUse?.({ index }), index);
  }
  assert.equal(observed.droppedEvents(), 1);
  const deadline = Date.now() + 3000;
  while (resolvers.length < 64 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(resolvers.length, 64);
  for (const resolve of resolvers) resolve(new Response(null, { status: 202 }));
  await observed.flush();
  observed.dispose();
  observed.hooks.onPreToolUse?.({ index: 66 });
  assert.equal(observed.droppedEvents(), 2);
  await observed.flush();
});

test('remote endpoints and invalid timeouts are rejected before hooks are wrapped', () => {
  assert.throws(() => addThreadforgeCopilotSDKTelemetry({}, { endpoint: 'https://collector.example/v1/events' }), /loopback/);
  assert.throws(() => addThreadforgeCopilotSDKTelemetry({}, { timeoutMs: 99 }), /timeoutMs/);
});
