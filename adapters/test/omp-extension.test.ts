import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';
import { createThreadforgeOmpExtension, mapOmpEvent } from '../src/omp-extension.ts';

const capturedAt = new Date('2026-09-25T20:00:00.000Z');
const sha256 = (value: string) => createHash('sha256').update(value, 'utf8').digest('hex');
const context = {
  mode: 'tui',
  model: { id: 'model-safe', provider: 'provider-safe' },
  sessionManager: { getSessionId: () => 'private-session-id' },
  cwd: 'C:/private/project',
};

test('maps OMP session and prompt events with hashed identity and digest-only prompts', () => {
  const start = mapOmpEvent({ type: 'session_start' }, context, capturedAt)[0];
  assert.equal((start.event as { action: string }).action, 'session.started');
  assert.deepEqual(start.session, { id: `omp:${sha256('private-session-id')}` });
  assert.deepEqual(start.harness, { name: 'omp', collection_method: 'plugin', version: 'provider-safe_model-safe' });
  assert.deepEqual(start.raw, { runtime_mode: 'tui' });
  assert.equal(JSON.stringify(start).includes('C:/private/project'), false);
  assert.equal(JSON.stringify(start).includes('private-session-id'), false);

  const prompt = mapOmpEvent({ type: 'before_agent_start', prompt: 'private prompt', images: [{ data: 'not retained' }] }, context, capturedAt)[0];
  assert.equal((prompt.event as { action: string }).action, 'prompt.submitted');
  assert.deepEqual(prompt.content, {
    retention: 'metadata', included: false, prompt_bytes: 14, prompt_sha256: sha256('private prompt'),
  });
  assert.equal(JSON.stringify(prompt).includes('private prompt'), false);
  assert.equal(JSON.stringify(prompt).includes('not retained'), false);
  assert.deepEqual(mapOmpEvent({ type: 'input', text: 'duplicate prompt path' }, context), []);
});

test('maps OMP handoff marker to a separate source-claim event without prompt text', () => {
  const events = mapOmpEvent({
    type: 'before_agent_start',
    prompt: 'Continue now. [beacon-handoff from=claude_code session=source-123]',
  }, context, capturedAt);
  assert.equal(events.length, 2);
  assert.equal((events[1]!.event as { action: string }).action, 'session.handoff');
  assert.deepEqual(events[1]!.handoff, { source_harness: 'claude_code', source_session_id: 'source-123' });
  assert.equal(JSON.stringify(events).includes('Continue now'), false);
  assert.equal(JSON.stringify(events).includes('beacon-handoff'), false);
});

test('maps OMP tool, MCP, and approval events without altering permission decisions', () => {
  const toolInput = { command: 'rm -rf /private/data' };
  const invoke = mapOmpEvent({ type: 'tool_call', toolCallId: 'call-private', toolName: 'bash', input: toolInput }, context, capturedAt)[0];
  assert.equal((invoke.event as { action: string }).action, 'tool.invoked');
  assert.equal((invoke.raw as { tool_call_id_sha256: string }).tool_call_id_sha256, sha256('call-private'));
  assert.equal(JSON.stringify(invoke).includes('rm -rf'), false);

  const completed = mapOmpEvent({
    type: 'tool_result', toolCallId: 'call-private', toolName: 'mcp__docs_search', input: {},
    content: [{ type: 'text', text: 'private result' }], isError: false,
  }, context, capturedAt)[0];
  assert.equal((completed.event as { action: string }).action, 'mcp.tool_invoked');
  assert.deepEqual(completed.mcp, { server: 'docs', tool: 'search' });
  assert.equal(JSON.stringify(completed).includes('private result'), false);
  assert.equal((mapOmpEvent({ type: 'tool_result', toolName: 'bash', isError: true }, context)[0].event as { action: string }).action, 'tool.failed');

  const request = mapOmpEvent({ type: 'tool_approval_requested', toolCallId: 'call-1', toolName: 'bash', approvalMode: 'always-ask' }, context, capturedAt)[0];
  assert.equal((request.event as { action: string }).action, 'approval.requested');
  assert.deepEqual(request.approval, { required: true, reason: 'runtime approval request observed' });
  const allowed = mapOmpEvent({ type: 'tool_approval_resolved', toolCallId: 'call-1', toolName: 'bash', approved: true }, context, capturedAt)[0];
  assert.equal((allowed.event as { action: string }).action, 'approval.allowed');
  assert.deepEqual(allowed.approval, { required: true, decision: 'approve' });
  assert.equal(mapOmpEvent({ type: 'tool_approval_resolved', approved: 'true' }, context).length, 0);
  assert.equal(mapOmpEvent({ type: 'tool_call', toolName: 'bash' }, context)[0].approval, undefined);
});

test('does not invent a tool-result digest when the host supplies no result payload', () => {
  const [event] = mapOmpEvent({ type: 'tool_result', toolCallId: 'call-empty', toolName: 'bash', isError: false }, context);
  assert.equal(event?.content, undefined);
});

test('records operator-run Bash/Python and bounded usage without retaining code or reasoning text', () => {
  const shell = mapOmpEvent({ type: 'user_bash', command: 'rm -rf private', excludeFromContext: true }, context, capturedAt)[0];
  assert.equal((shell.event as { action: string }).action, 'command.executed');
  assert.deepEqual(shell.raw, { runtime_mode: 'tui', user_initiated: true, excluded_from_model_context: true });
  assert.equal(JSON.stringify(shell).includes('rm -rf'), false);

  const python = mapOmpEvent({ type: 'user_python', code: 'secret = "value"' }, context, capturedAt)[0];
  assert.equal((python.raw as { user_python: boolean }).user_python, true);
  assert.equal(JSON.stringify(python).includes('secret ='), false);

  const message = mapOmpEvent({ type: 'message_end', message: {
    role: 'assistant', content: [{ type: 'thinking', thinking: 'private reasoning' }],
    usage: { input: 12, output: 8, cacheRead: 3, cacheWrite: 2, reasoning: 5, totalTokens: 20, cost: { total: 0.004 },
      input_tokens: 999999 },
  } }, context, capturedAt)[0];
  assert.equal((message.event as { action: string }).action, 'token.usage');
  assert.deepEqual(message.gen_ai, { usage: {
    input_tokens: 12, output_tokens: 8, cache_read: { input_tokens: 3 }, cache_creation: { input_tokens: 2 },
    reasoning: { output_tokens: 5 }, cost_usd: 0.004,
  } });
  assert.equal(JSON.stringify(message).includes('private reasoning'), false);
  assert.equal(mapOmpEvent({ type: 'message_end', message: { role: 'user' } }, context).length, 0);
});

test('extension registers observational callbacks that return no behavior-changing result', async () => {
  const registered = new Map<string, (event: unknown, context?: unknown) => unknown>();
  const previousFetch = globalThis.fetch;
  const sent: Array<{ url: string; event: Record<string, unknown> }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const body = typeof init?.body === 'string' ? init.body : '';
    sent.push({ url: String(input), event: JSON.parse(body) as Record<string, unknown> });
    return { status: 202 } as Response;
  }) as typeof fetch;
  try {
    const extension = createThreadforgeOmpExtension();
    extension.register({ on: (event, handler) => { registered.set(event, handler); } });
    assert.deepEqual(extension.subscribedEvents, [
      'session_start', 'session_shutdown', 'before_agent_start', 'tool_call', 'tool_result',
      'tool_approval_requested', 'tool_approval_resolved', 'user_bash', 'user_python', 'message_end',
    ]);
    const tool = registered.get('tool_call');
    assert.ok(tool);
    assert.equal(tool({ type: 'tool_call', toolCallId: 'joined-call', toolName: 'bash', input: { command: 'private command' } }, context), undefined);
    const approval = registered.get('tool_approval_resolved');
    assert.ok(approval);
    assert.equal(approval({ type: 'tool_approval_resolved', toolCallId: 'joined-call', toolName: 'bash', approved: false }, context), undefined);
    await new Promise<void>((resolve) => setImmediate(resolve));
    assert.equal(sent.length, 2);
    assert.equal(sent.every((item) => item.url === 'http://127.0.0.1:4318/v1/events'), true);
    const decision = sent[1]?.event;
    assert.equal((decision?.event as { action: string }).action, 'approval.denied');
    assert.equal(((decision?.content as { decided_tool_input_sha256: string }).decided_tool_input_sha256).length, 64);
    assert.equal(JSON.stringify(sent).includes('private command'), false);
  } finally {
    globalThis.fetch = previousFetch;
  }
});
