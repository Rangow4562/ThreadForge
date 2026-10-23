import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { instrumentClaudeAgentQuery } from '../src/claude-agent-sdk.ts';

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

function genAI(event: Record<string, unknown>): Record<string, unknown> {
  return event.gen_ai as Record<string, unknown>;
}

test('Claude Agent SDK query preserves messages and control methods while emitting content-free result metadata', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const progress = { type: 'assistant', message: { content: [{ text: 'PRIVATE_ASSISTANT_TEXT' }] } };
  let usageRead = false;
  const result = Object.defineProperties({
    type: 'result',
    subtype: 'success',
    is_error: false,
    result: 'PRIVATE_FINAL_RESULT',
    session_id: 'private-session-id',
  }, {
    modelUsage: { get() { usageRead = true; throw new Error('modelUsage must not be read'); } },
    uuid: { get() { throw new Error('message identity must not be read'); } },
  });
  const query = (async function* () { yield progress; yield result; })() as AsyncGenerator<unknown> & {
    controlCalls: number;
    interrupt(): Promise<undefined>;
  };
  query.controlCalls = 0;
  query.interrupt = function (this: typeof query) {
    this.controlCalls += 1;
    return Promise.resolve(undefined);
  };
  const observation = instrumentClaudeAgentQuery(query, { endpoint: capture.endpoint });
  assert.equal(await observation.query.interrupt(), undefined);
  assert.equal(query.controlCalls, 1, 'control method must keep the original query as `this`');
  const messages: unknown[] = [];
  for await (const message of observation.query as AsyncIterable<unknown>) messages.push(message);
  await capture.waitFor(1);

  assert.deepEqual(messages, [progress, result]);
  assert.equal(messages[1], result, 'result messages must retain object identity');
  assert.equal(usageRead, false);
  assert.equal(capture.events.length, 1);
  const event = capture.events[0];
  assert.equal((event.harness as Record<string, unknown>).name, 'claude_agent_sdk');
  assert.deepEqual(genAI(event), {
    system: 'anthropic', operation: 'claude-agent.query', status: 'completed', streaming: true,
    duration_ms: genAI(event).duration_ms, result_count: 1,
    content: { included: false, retention: 'metadata' },
  });
  const serialized = JSON.stringify(capture.events);
  for (const privateValue of ['PRIVATE_ASSISTANT_TEXT', 'PRIVATE_FINAL_RESULT', 'private-session-id', 'modelUsage']) {
    assert.equal(serialized.includes(privateValue), false, `telemetry leaked ${privateValue}`);
  }
  observation.dispose();
});

test('SDK result error markers produce failed metadata without reading result text', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const query = (async function* () {
    yield { type: 'result', subtype: 'error_during_execution', is_error: true, result: 'PRIVATE_ERROR_TEXT' };
  })();
  const observation = instrumentClaudeAgentQuery(query, { endpoint: capture.endpoint });
  for await (const _message of observation.query as AsyncIterable<unknown>) { /* drain */ }
  await capture.waitFor(1);
  assert.equal(genAI(capture.events[0]).status, 'failed');
  assert.equal(JSON.stringify(capture.events).includes('PRIVATE_ERROR_TEXT'), false);
  observation.dispose();
});

test('query errors preserve error identity and record a failed outcome', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const failure = new Error('private prompt-bearing SDK failure');
  const query = (async function* () { throw failure; })();
  const observation = instrumentClaudeAgentQuery(query, { endpoint: capture.endpoint });
  await assert.rejects(async () => {
    for await (const _message of observation.query as AsyncIterable<unknown>) { /* drain */ }
  }, (error) => error === failure);
  await capture.waitFor(1);
  assert.equal(genAI(capture.events[0]).status, 'failed');
  assert.equal(JSON.stringify(capture.events).includes(failure.message), false);
  observation.dispose();
});

test('early query cancellation records interrupted and still runs the provider iterator return', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  let providerClosed = false;
  const query = (async function* () {
    try {
      while (true) yield { type: 'assistant', text: 'PRIVATE' };
    } finally {
      providerClosed = true;
    }
  })();
  const observation = instrumentClaudeAgentQuery(query, { endpoint: capture.endpoint });
  for await (const _message of observation.query as AsyncIterable<unknown>) break;
  await capture.waitFor(1);
  assert.equal(providerClosed, true);
  assert.equal(genAI(capture.events[0]).status, 'interrupted');
  assert.equal(JSON.stringify(capture.events).includes('PRIVATE'), false);
  observation.dispose();
});

test('dispose disables observation without changing query execution; unsupported shapes fail closed', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const query = (async function* () { yield { type: 'result', is_error: false }; })();
  const observation = instrumentClaudeAgentQuery(query, { endpoint: capture.endpoint });
  observation.dispose();
  for await (const _message of observation.query as AsyncIterable<unknown>) { /* drain */ }
  await new Promise((resolve) => setTimeout(resolve, 30));
  assert.equal(capture.events.length, 0);
  assert.throws(() => instrumentClaudeAgentQuery({ next() {} }), /async generator/);
});
