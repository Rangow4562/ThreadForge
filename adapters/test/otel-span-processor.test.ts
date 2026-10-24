import assert from 'node:assert/strict';
import test from 'node:test';
import { createThreadforgeSpanProcessor } from '../src/otel-span-processor.ts';

function span(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    name: 'chat user prompt SECRET_SPAN_NAME',
    traceId: 'SECRET_TRACE_ID',
    spanContext: { traceId: 'SECRET_CONTEXT_ID' },
    startTime: [1_800_000_000, 0],
    endTime: [1_800_000_001, 500_000_000],
    duration: [1, 500_000_000],
    status: { code: 2, message: 'SECRET_ERROR_DESCRIPTION' },
    attributes: {
      'gen_ai.operation.name': 'chat',
      'gen_ai.provider.name': 'anthropic',
      'gen_ai.request.model': 'claude-3-7-sonnet',
      'gen_ai.response.model': 'claude-3-7-sonnet',
      'gen_ai.usage.input_tokens': 17,
      'gen_ai.usage.output_tokens': 9,
      'gen_ai.prompt': 'SECRET_PROMPT',
      'gen_ai.completion': 'SECRET_OUTPUT',
      'user.email': 'SECRET_EMAIL',
    },
    events: [{ name: 'exception', attributes: { 'exception.message': 'SECRET_EXCEPTION' } }],
    links: [{ context: { traceId: 'SECRET_LINKED_TRACE' } }],
    ...overrides,
  };
}

function transport() {
  const bodies: unknown[] = [];
  const fetchImpl: typeof fetch = async (_input, init) => {
    bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 202 });
  };
  return { bodies, fetchImpl };
}

async function withTransport<T>(fetchImpl: typeof fetch, action: () => Promise<T>): Promise<T> {
  const previousFetch = globalThis.fetch;
  globalThis.fetch = fetchImpl;
  try {
    return await action();
  } finally {
    globalThis.fetch = previousFetch;
  }
}

test('projects only bounded GenAI metadata from a failed span', async () => {
  const { bodies, fetchImpl } = transport();
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    processor.onStart(span(), {});
    processor.onEnd(span());
    await processor.forceFlush();

    assert.equal(bodies.length, 1);
    const event = bodies[0] as Record<string, unknown>;
    assert.equal(event.timestamp, '2027-01-15T08:00:01.500Z');
    assert.equal((event.event as Record<string, unknown>).action, 'model.call.failed');
    assert.equal((event.harness as Record<string, unknown>).name, 'opentelemetry_sdk');
    assert.deepEqual(event.gen_ai, {
      system: 'anthropic',
      operation: 'chat',
      status: 'failed',
      content: { included: false, retention: 'metadata' },
      duration_ms: 1500,
      request_model: 'claude-3-7-sonnet',
      response_model: 'claude-3-7-sonnet',
      usage: { input_tokens: 17, output_tokens: 9 },
    });
    for (const secret of [
      'SECRET_SPAN_NAME', 'SECRET_TRACE_ID', 'SECRET_CONTEXT_ID', 'SECRET_ERROR_DESCRIPTION',
      'SECRET_PROMPT', 'SECRET_OUTPUT', 'SECRET_EMAIL', 'SECRET_EXCEPTION', 'SECRET_LINKED_TRACE',
    ]) assert.equal(JSON.stringify(event).includes(secret), false, `${secret} must not be emitted`);
    await processor.shutdown();
  });
});

test('ignores unsupported operations and spans without GenAI operation metadata', async () => {
  const { bodies, fetchImpl } = transport();
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    processor.onEnd(span({ attributes: { 'gen_ai.operation.name': 'SECRET_USER_VALUE' } }));
    processor.onEnd(span({ attributes: { 'user.prompt': 'SECRET_PROMPT' } }));
    await processor.forceFlush();
    assert.deepEqual(bodies, []);
    await processor.shutdown();
  });
});

test('omits credential-shaped provider and model identifiers', async () => {
  const { bodies, fetchImpl } = transport();
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    processor.onEnd(span({
      attributes: {
        'gen_ai.operation.name': 'chat',
        'gen_ai.provider.name': `ghp_${'A'.repeat(36)}`,
        'gen_ai.request.model': `sk-${'x'.repeat(32)}`,
        'gen_ai.response.model': 'claude-3-7-sonnet',
      },
    }));
    await processor.forceFlush();

    const event = bodies[0] as Record<string, unknown>;
    assert.deepEqual(event.gen_ai, {
      system: 'unknown',
      operation: 'chat',
      status: 'failed',
      content: { included: false, retention: 'metadata' },
      duration_ms: 1500,
      response_model: 'claude-3-7-sonnet',
    });
    assert.equal(JSON.stringify(event).includes(`ghp_${'A'.repeat(36)}`), false);
    assert.equal(JSON.stringify(event).includes(`sk-${'x'.repeat(32)}`), false);
    await processor.shutdown();
  });
});

test('does not invoke accessor attributes while projecting untrusted span objects', async () => {
  const { bodies, fetchImpl } = transport();
  let getterCalls = 0;
  const attributes = Object.defineProperty({}, 'gen_ai.operation.name', {
    enumerable: true,
    get() { getterCalls += 1; return 'chat'; },
  });
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    processor.onEnd({ attributes });
    await processor.forceFlush();
    assert.equal(getterCalls, 0);
    assert.deepEqual(bodies, []);
    await processor.shutdown();
  });
});

test('forceFlush waits for in-flight local intake and shutdown stops further capture', async () => {
  let resolveFetch!: (response: Response) => void;
  let calls = 0;
  const fetchImpl: typeof fetch = async () => {
    calls += 1;
    return new Promise<Response>((resolve) => { resolveFetch = resolve; });
  };
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    processor.onEnd(span());
    const flushing = processor.forceFlush();
    await Promise.resolve();
    assert.equal(calls, 1);
    resolveFetch(new Response(null, { status: 202 }));
    await flushing;
    await processor.shutdown();
    processor.onEnd(span());
    await processor.forceFlush();
    assert.equal(calls, 1);
  });
});

test('caps concurrent local exports and waits for admitted requests during shutdown', async () => {
  const releases: Array<(response: Response) => void> = [];
  let calls = 0;
  const fetchImpl: typeof fetch = async () => {
    calls += 1;
    return new Promise<Response>((resolve) => { releases.push(resolve); });
  };
  await withTransport(fetchImpl, async () => {
    const processor = createThreadforgeSpanProcessor();
    for (let index = 0; index < 65; index += 1) processor.onEnd(span());
    assert.equal(calls, 64, 'the 65th export must be dropped while the cap is full');

    const shutdown = processor.shutdown();
    for (const release of releases) release(new Response(null, { status: 202 }));
    await shutdown;
    assert.equal(calls, 64);
  });
});

test('rejects non-loopback endpoints and invalid timeouts at construction', () => {
  assert.throws(() => createThreadforgeSpanProcessor({ endpoint: 'https://collector.example/v1/events' }), /loopback HTTP/);
  assert.throws(() => createThreadforgeSpanProcessor({ timeoutMs: 99 }), /timeoutMs/);
});
