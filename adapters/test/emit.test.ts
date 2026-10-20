import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { emitEvent, emitEvents, markPromptHandoff, type ThreadforgeEvent } from '../src/emit.ts';
import { mapClaudeHookPayload } from '../src/claude-hook.ts';

function event(action = 'tool.invoked'): ThreadforgeEvent {
  return {
    timestamp: new Date().toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action },
    severity: 'info',
    endpoint: { os: 'windows' },
    harness: { name: 'custom-sdk', collection_method: 'plugin' },
  };
}

async function withLocalServer(handler: (requestBody: string, contentType: string) => void | Promise<void>) {
  let receivedBody = '';
  let receivedType = '';
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    receivedBody = Buffer.concat(chunks).toString('utf8');
    receivedType = String(request.headers['content-type'] ?? '');
    await handler(receivedBody, receivedType);
    response.writeHead(202, { 'content-type': 'application/json' });
    response.end('{"accepted":1}');
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('test server did not bind a TCP port');
  return {
    endpoint: `http://127.0.0.1:${address.port}/v1/events`,
    get body() { return receivedBody; },
    get contentType() { return receivedType; },
    close: () => new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve())),
  };
}

test('emits one validated event to loopback intake', async (t) => {
  const server = await withLocalServer(() => {});
  t.after(() => server.close());
  const receipt = await emitEvent(event(), { endpoint: server.endpoint });
  assert.deepEqual(receipt, { accepted: 1, status: 202 });
  assert.equal(server.contentType, 'application/json');
  assert.equal(JSON.parse(server.body).harness.name, 'custom-sdk');
});

test('sends bounded batches in the endpoint contract shape', async (t) => {
  const server = await withLocalServer((body) => {
    assert.equal(JSON.parse(body).length, 2);
  });
  t.after(() => server.close());
  const receipt = await emitEvents([event('tool.invoked'), event('tool.completed')], { endpoint: server.endpoint });
  assert.deepEqual(receipt, { accepted: 2, status: 202 });
});

test('emits a metadata-only handoff link beside a marked prompt', async (t) => {
  let received: unknown;
  const fetchImpl: typeof fetch = async (_input, init) => {
    received = JSON.parse(String(init?.body));
    return new Response(null, { status: 202 });
  };
  const prompt = mapClaudeHookPayload({
    hook_event_name: 'UserPromptSubmit', session_id: 'target-session',
    prompt: 'Continue the task. [beacon-handoff from=codex_cli session=source-123]',
  });
  assert.ok(prompt);
  const receipt = await emitEvent(prompt, { fetchImpl });
  assert.equal(receipt.accepted, 2);
  assert.ok(Array.isArray(received));
  const [promptEvent, link] = received as Array<Record<string, unknown>>;
  assert.equal(promptEvent.event && (promptEvent.event as Record<string, unknown>).action, 'prompt.submitted');
  assert.equal(link.event && (link.event as Record<string, unknown>).action, 'session.handoff');
  assert.deepEqual(link.handoff, { source_harness: 'codex_cli', source_session_id: 'source-123' });
  assert.deepEqual(link.session, prompt.session);
  assert.equal(JSON.stringify(received).includes('Continue the task'), false);
  assert.equal(JSON.stringify(received).includes('beacon-handoff'), false);
  assert.deepEqual(Object.keys(link).sort(), ['endpoint', 'event', 'handoff', 'harness', 'product', 'schema_version', 'session', 'severity', 'timestamp', 'vendor']);
});

test('a full-size hook batch expands to the bounded 200-event intake ceiling', async () => {
  let accepted = 0;
  const fetchImpl: typeof fetch = async (_input, init) => {
    const body = JSON.parse(String(init?.body));
    accepted = Array.isArray(body) ? body.length : 1;
    return new Response(null, { status: 202 });
  };
  const events = Array.from({ length: 100 }, (_, index) => {
    const prompt = { ...event('prompt.submitted'), session: { id: `target-${index}` } };
    return markPromptHandoff(prompt, '[beacon-handoff from=codex_cli session=source-123]');
  });
  const receipt = await emitEvents(events, { fetchImpl });
  assert.equal(accepted, 200);
  assert.equal(receipt.accepted, 200);
});

test('emits no handoff link for invalid markers, non-prompt events, or missing target identity', async () => {
  const counts: number[] = [];
  const fetchImpl: typeof fetch = async (_input, init) => {
    const body = JSON.parse(String(init?.body));
    counts.push(Array.isArray(body) ? body.length : 1);
    return new Response(null, { status: 202 });
  };
  const invalid = { ...event('prompt.submitted'), session: { id: 'target' } };
  markPromptHandoff(invalid, '[beacon-handoff from=BAD session=source]');
  await emitEvent(invalid, { fetchImpl });
  const nonPrompt = { ...event('tool.invoked'), session: { id: 'target' } };
  markPromptHandoff(nonPrompt, '[beacon-handoff from=codex_cli session=source]');
  await emitEvent(nonPrompt, { fetchImpl });
  const noTarget = event('prompt.submitted');
  markPromptHandoff(noTarget, '[beacon-handoff from=codex_cli session=source]');
  await emitEvent(noTarget, { fetchImpl });
  assert.deepEqual(counts, [1, 1, 1]);
});

test('rejects remote endpoints and malformed events before network access', async () => {
  let called = false;
  const fetchImpl: typeof fetch = async () => {
    called = true;
    return new Response(null, { status: 202 });
  };
  await assert.rejects(emitEvent(event(), { endpoint: 'https://collector.example/v1/events', fetchImpl }), /loopback HTTP/);
  await assert.rejects(emitEvent({ ...event(), event: { kind: '', action: '' } }, { fetchImpl }), /event.kind/);
  await assert.rejects(emitEvents([], { fetchImpl }), /event batch/);
  await assert.rejects(emitEvent({ ...event(), message: 'x'.repeat(8 << 20) }, { fetchImpl }), /exceeds the/);
  assert.equal(called, false);
});

test('rejects redirects and non-accepted local responses', async () => {
  const fetchImpl: typeof fetch = async () => new Response(null, { status: 204 });
  await assert.rejects(emitEvent(event(), { fetchImpl }), /rejected event batch/);
});
