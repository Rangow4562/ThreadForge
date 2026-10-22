import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { assertLoopbackEndpoint } from '../src/emit.ts';
import { instrumentAnthropic, instrumentOpenAI } from '../src/genai-sdk.ts';

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
      while (events.length < count && Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 5));
      }
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

test('OpenAI calls emit bounded metadata and preserve result, arguments, and disposal', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const request = { model: 'gpt-4.1-mini', input: 'private prompt body' };
  const response = { id: 'response-id', model: 'gpt-4.1-mini', usage: { input_tokens: 11, output_tokens: 4 } };
  const client = {
    responses: {
      create: async (value: typeof request) => {
        assert.equal(value, request);
        return response;
      },
    },
    chat: { completions: { create: async () => ({}) } },
  };
  const original = client.responses.create;
  const handle = instrumentOpenAI(client, { endpoint: capture.endpoint });
  assert.equal(handle.client, client);
  assert.equal(await client.responses.create(request), response);
  await capture.waitFor(1);
  assert.equal(capture.events.length, 1);
  const event = capture.events[0];
  assert.equal(event.event && (event.event as Record<string, unknown>).action, 'model.call.completed');
  assert.deepEqual(genAI(event), {
    system: 'openai', operation: 'responses.create', status: 'completed', streaming: false,
    duration_ms: (genAI(event).duration_ms), content: { included: false, retention: 'metadata' },
    request_model: 'gpt-4.1-mini', response_model: 'gpt-4.1-mini',
    usage: { input_tokens: 11, output_tokens: 4 },
  });
  assert.equal(JSON.stringify(capture.events).includes('private prompt body'), false);
  assert.equal(JSON.stringify(capture.events).includes('response-id'), false);
  handle.dispose();
  assert.equal(client.responses.create, original);
  handle.dispose();
});

test('content accessors are not evaluated and SDK error identity is preserved', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  let contentRead = false;
  const request = Object.defineProperty({ model: 'gpt-4o' }, 'input', {
    enumerable: true,
    get() { contentRead = true; throw new Error('content getter must not run'); },
  });
  const failure = new Error('private provider error with prompt details');
  const client = { completions: { create: async (_request: unknown) => { throw failure; } } };
  const handle = instrumentOpenAI(client, { endpoint: capture.endpoint });
  await assert.rejects(client.completions.create(request), (error) => error === failure);
  await capture.waitFor(1);
  assert.equal(contentRead, false);
  assert.equal(capture.events.length, 1);
  assert.equal(genAI(capture.events[0]).status, 'failed');
  assert.equal(JSON.stringify(capture.events).includes(failure.message), false);
  handle.dispose();
});

test('Anthropic streaming calls pass each chunk through and report terminal usage only', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  const first = { type: 'message_start', message: { model: 'claude-sonnet-4-5', usage: { input_tokens: 8 } } };
  const second = { type: 'message_delta', usage: { output_tokens: 6 } };
  const client = {
    messages: {
      create: async (_request: unknown) => (async function* () {
        yield first;
        yield second;
      })(),
    },
  };
  const handle = instrumentAnthropic(client, { endpoint: capture.endpoint });
  const stream = await client.messages.create({ model: 'claude-sonnet-4-5', stream: true });
  const chunks: unknown[] = [];
  for await (const chunk of stream) chunks.push(chunk);
  await capture.waitFor(1);
  assert.deepEqual(chunks, [first, second]);
  assert.equal(capture.events.length, 1);
  const metadata = genAI(capture.events[0]);
  assert.equal(metadata.status, 'completed');
  assert.equal(metadata.streaming, true);
  assert.equal(metadata.response_model, 'claude-sonnet-4-5');
  assert.deepEqual(metadata.usage, { input_tokens: 8, output_tokens: 6 });
  handle.dispose();
});

test('stream cancellation is recorded as interrupted and unsupported clients remain unchanged', async (t) => {
  const capture = await collector();
  t.after(() => capture.close());
  let returned = false;
  const stream: AsyncIterable<string> = {
    [Symbol.asyncIterator]() {
      let index = 0;
      return {
        async next() { return index++ === 0 ? { done: false as const, value: 'chunk' } : { done: false as const, value: 'next' }; },
        async return() { returned = true; return { done: true as const, value: undefined }; },
      };
    },
  };
  const client = { responses: { create: (_request: unknown) => Promise.resolve(stream) } };
  const handle = instrumentOpenAI(client, { endpoint: capture.endpoint });
  const wrapped = await client.responses.create({ model: 'gpt-4.1', stream: true });
  for await (const _chunk of wrapped) break;
  await capture.waitFor(1);
  assert.equal(returned, true);
  assert.equal(capture.events.length, 1);
  assert.equal(genAI(capture.events[0]).status, 'interrupted');
  handle.dispose();

  const unsupported = Object.freeze({ messages: Object.freeze({}) });
  assert.throws(() => instrumentAnthropic(unsupported), /no supported anthropic SDK create method/);
  assert.deepEqual(unsupported, { messages: {} });
});

test('a remote endpoint is rejected without affecting provider calls', async () => {
  const client = { messages: { create: async (_request: unknown) => ({ model: 'claude-sonnet-4-5' }) } };
  assert.throws(() => assertLoopbackEndpoint('https://collector.example/v1/events'), /loopback HTTP/);
  const handle = instrumentAnthropic(client, {
    endpoint: 'https://collector.example/v1/events',
  });
  const response = await client.messages.create({ model: 'claude-sonnet-4-5' });
  assert.equal(response.model, 'claude-sonnet-4-5');
  await new Promise((resolve) => setImmediate(resolve));
  handle.dispose();
});
