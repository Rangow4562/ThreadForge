import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';

import { SpanStatusCode } from '@opentelemetry/api';
import { TracerProvider } from '@opentelemetry/sdk-trace';

import { createThreadforgeSpanProcessor } from '../src/otel-span-processor.ts';

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
  });
}

test('real OpenTelemetry SDK lifecycle exports only allowlisted GenAI metadata', async () => {
  const events = [];
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    events.push(JSON.parse(Buffer.concat(chunks).toString('utf8')));
    response.writeHead(202);
    response.end();
  });
  await listen(server);
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const processor = createThreadforgeSpanProcessor({
    endpoint: `http://127.0.0.1:${address.port}/v1/events`,
    timeoutMs: 1000,
  });
  const provider = new TracerProvider({ spanProcessors: [processor] });
  try {
    const span = provider.getTracer('threadforge-sdk-compatibility', '3.1.1').startSpan(
      'SECRET_SPAN_NAME',
      {
        attributes: {
          'gen_ai.operation.name': 'chat',
          'gen_ai.provider.name': 'openai',
          'gen_ai.request.model': 'gpt-4.1-mini',
          'gen_ai.usage.input_tokens': 13,
          'gen_ai.usage.output_tokens': 5,
          'gen_ai.prompt': 'SECRET_PROMPT',
          'gen_ai.completion': 'SECRET_OUTPUT',
          'user.email': 'SECRET_EMAIL',
        },
      },
    );
    span.addEvent('exception', { 'exception.message': 'SECRET_EXCEPTION' });
    span.setStatus({ code: SpanStatusCode.ERROR, message: 'SECRET_ERROR_DESCRIPTION' });
    span.end();

    await provider.forceFlush();

    assert.equal(events.length, 1);
    const event = events[0];
    assert.equal(event.event.action, 'model.call.failed');
    assert.equal(event.harness.name, 'opentelemetry_sdk');
    assert.deepEqual(event.gen_ai, {
      system: 'openai',
      operation: 'chat',
      status: 'failed',
      content: { included: false, retention: 'metadata' },
      request_model: 'gpt-4.1-mini',
      usage: { input_tokens: 13, output_tokens: 5 },
    });
    const serialized = JSON.stringify(event);
    for (const secret of [
      'SECRET_SPAN_NAME', 'SECRET_PROMPT', 'SECRET_OUTPUT', 'SECRET_EMAIL',
      'SECRET_EXCEPTION', 'SECRET_ERROR_DESCRIPTION',
    ]) assert.equal(serialized.includes(secret), false, `${secret} must not be exported`);
  } finally {
    await provider.shutdown();
    await close(server);
  }
});
