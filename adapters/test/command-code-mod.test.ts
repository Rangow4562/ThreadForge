import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import threadforgeCommandCodeTelemetry from '../command-code-mod/index.ts';
import { commandCodeObservedEventTypes, registerCommandCodeObserver } from '../command-code-mod/telemetry.ts';

async function collector() {
  const events: Array<Record<string, unknown>> = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const parsed: unknown = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    events.push(parsed as Record<string, unknown>);
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
    close: () => new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
    }),
  };
}

function fakeModApi() {
  const handlers = new Map<string, (event: unknown) => void>();
  const disposals: string[] = [];
  const cmd = {
    hooks() { throw new Error('telemetry must not register behavior-changing hooks'); },
    on(event: string, handler: (event: unknown) => void) {
      assert.equal(handlers.has(event), false, `duplicate registration for ${event}`);
      handlers.set(event, handler);
      return { dispose: () => { disposals.push(event); handlers.delete(event); } };
    },
  };
  return { cmd, handlers, disposals };
}

function action(event: Record<string, unknown>): string {
  return String((event.event as Record<string, unknown>).action);
}

test('observer registers only the documented lifecycle event allowlist and disposes every subscription', () => {
  const host = fakeModApi();
  const observer = registerCommandCodeObserver(host.cmd, { fetchImpl: async () => new Response(null, { status: 202 }) });
  assert.deepEqual([...host.handlers.keys()], commandCodeObservedEventTypes);
  observer.dispose();
  assert.deepEqual(host.handlers.size, 0);
  assert.deepEqual(host.disposals, [...commandCodeObservedEventTypes]);
});

test('loadable mod entrypoint exposes direct observer subscriptions and never registers hooks', () => {
  const host = fakeModApi();
  assert.equal(threadforgeCommandCodeTelemetry(host.cmd), undefined);
  assert.deepEqual([...host.handlers.keys()], commandCodeObservedEventTypes);
  assert.equal(host.disposals.length, 0);
});

test('AgentEvent records lifecycle outcomes only and excludes host payloads', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const host = fakeModApi();
  const observer = registerCommandCodeObserver(host.cmd, { endpoint: capture.endpoint });
  const privateValues = ['PRIVATE_PROMPT', 'PRIVATE_TOOL_INPUT', 'PRIVATE_TOOL_OUTPUT', 'PRIVATE_PATH', 'PRIVATE_ERROR'];
  for (const eventType of commandCodeObservedEventTypes) {
    const result = host.handlers.get(eventType)?.({
      type: eventType,
      prompt: privateValues[0],
      toolInput: privateValues[1],
      toolResult: privateValues[2],
      cwd: privateValues[3],
      error: privateValues[4],
    });
    assert.equal(result, undefined, 'telemetry callback must not return a host decision');
  }
  await observer.flush();

  assert.deepEqual(capture.events.map(action).sort(), [
    'session.started', 'session.ended', 'run.started', 'run.completed', 'turn.started', 'turn.completed',
    'model.requested', 'model.completed', 'tool.started', 'tool.completed', 'tool.failed',
    'subagent.started', 'subagent.completed', 'subagent.progressed', 'compaction.started', 'compaction.completed',
  ].sort());
  const serialized = JSON.stringify(capture.events);
  for (const privateValue of privateValues) assert.equal(serialized.includes(privateValue), false);
  for (const event of capture.events) {
    assert.deepEqual(event.harness, { name: 'command_code', collection_method: 'plugin' });
    assert.equal(event.raw, undefined);
    assert.equal(event.prompt, undefined);
    assert.match(String((event.session as Record<string, unknown>).id), /^[0-9a-f-]{36}$/);
  }
  assert.equal(
    (capture.events.find((event) => action(event) === 'session.started')?.session as Record<string, unknown>).id,
    (capture.events.find((event) => action(event) === 'run.started')?.session as Record<string, unknown>).id,
    'the random correlation ID should group events from the same Command Code session',
  );
  assert.equal(capture.events.find((event) => action(event) === 'tool.failed')?.severity, 'medium');
  observer.dispose();
});

test('event sends never exceed the bounded queue and retain host progress when full', async () => {
  const host = fakeModApi();
  const resolvers: Array<(response: Response) => void> = [];
  const observer = registerCommandCodeObserver(host.cmd, {
    fetchImpl: (() => new Promise<Response>((resolve) => resolvers.push(resolve))) as typeof fetch,
  });
  const handler = host.handlers.get('tool_running');
  assert.ok(handler);
  for (let index = 0; index < 65; index += 1) {
    assert.equal(handler({ type: 'tool_running', index, toolInput: 'PRIVATE_TOOL_INPUT' }), undefined);
  }
  assert.equal(observer.droppedEvents(), 1);
  const deadline = Date.now() + 3000;
  while (resolvers.length < 64 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(resolvers.length, 64);
  for (const resolve of resolvers) resolve(new Response(null, { status: 202 }));
  await observer.flush();
  observer.dispose();
});

test('accessor-backed event types are ignored without reading getters and delivery errors stay fail-open', async () => {
  const host = fakeModApi();
  const observer = registerCommandCodeObserver(host.cmd, {
    fetchImpl: async () => { throw new Error('LOCAL_INTAKE_UNAVAILABLE'); },
  });
  let getterReads = 0;
  const malicious = Object.defineProperty({}, 'type', {
    get() { getterReads += 1; throw new Error('PRIVATE_GETTER_ERROR'); },
  });
  host.handlers.get('tool_errored')?.(malicious);
  host.handlers.get('tool_errored')?.({ type: 'tool_errored', error: 'PRIVATE_TOOL_ERROR' });
  await observer.flush();
  assert.equal(getterReads, 0);
  observer.dispose();
});

test('registration failures unwind earlier event subscriptions', () => {
  const host = fakeModApi();
  let calls = 0;
  const broken = {
    on(event: string, handler: (event: unknown) => void) {
      calls += 1;
      if (calls === 4) throw new Error('private host detail');
      return host.cmd.on(event, handler);
    },
  };
  assert.throws(() => registerCommandCodeObserver(broken), /Could not register/);
  assert.equal(host.handlers.size, 0);
  assert.equal(host.disposals.length, 3);
});

test('remote endpoints and out-of-range timeouts are rejected before registration', () => {
  const host = fakeModApi();
  assert.throws(() => registerCommandCodeObserver(host.cmd, { endpoint: 'https://collector.example/v1/events' }), /loopback/);
  assert.throws(() => registerCommandCodeObserver(host.cmd, { timeoutMs: 99 }), /timeoutMs/);
  assert.equal(host.handlers.size, 0);
});
