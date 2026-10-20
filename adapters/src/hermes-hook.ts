import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { emitEvents, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const hermesManagedMarker = '--threadforge-hermes-hook=v1';
export const hermesHookEvents = [
  'on_session_start', 'pre_llm_call', 'pre_tool_call', 'post_tool_call', 'pre_approval_request',
  'post_approval_response', 'subagent_stop', 'on_session_end', 'on_session_finalize',
] as const;
export type HermesHookEvent = typeof hermesHookEvents[number];
type JsonObject = Record<string, unknown>;
const maxInputBytes = 1 << 20;
const maxDigestBytes = 64 << 10;

/** Map one Beacon-observed Hermes shell-hook payload to privacy-minimized events. */
export function mapHermesHookPayload(eventName: string, value: unknown, capturedAt = new Date()): ThreadforgeEvent[] {
  if (!isSupportedEvent(eventName) || !isRecord(value)) return [];
  const extra = isRecord(value.extra) ? value.extra : {};
  const sessionId = safeOpaque(firstString(value, ['session_id', 'sessionId', 'session_key', 'task_id']) || firstString(extra, ['session_id', 'sessionId']), 512);
  const cwd = safeText(value.cwd ?? value.working_directory ?? extra.cwd ?? extra.working_directory, 4_096);
  const timestamp = capturedAt.toISOString();
  const makeEvent = (action: string, category: string, suffix: string, message: string, severity: ThreadforgeEvent['severity'] = 'info'): ThreadforgeEvent => {
    const event: ThreadforgeEvent = {
      timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
      event: { id: sha256(`${eventName}:${sessionId ?? ''}:${suffix}:${timestamp}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
      severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'hermes', collection_method: 'hook' },
      hook: { name: eventName },
      raw: { source: 'hermes_hook', event_type: eventName, message, ...(cwd ? { working_directory_sha256: sha256(cwd) } : {}) },
    };
    if (sessionId) event.session = { id: `hermes:${sha256(sessionId)}` };
    const model = safeLabel(value.model ?? extra.model, 160);
    if (model) event.model = model;
    return event;
  };

  if (eventName === 'on_session_start') return [makeEvent('session.started', 'session', 'session-start', 'Hermes session started')];
  if (eventName === 'on_session_end' || eventName === 'on_session_finalize') {
    return [makeEvent('session.ended', 'session', eventName, 'Hermes session ended')];
  }
  if (eventName === 'pre_llm_call') {
    const event = makeEvent('prompt.submitted', 'prompt', 'prompt', 'Hermes prompt submitted');
    const prompt = extra.user_message ?? value.user_message;
    const digest = contentDigest(prompt);
    if (digest) event.content = digest;
    markPromptHandoff(event, prompt);
    return [event];
  }
  if (eventName === 'pre_approval_request' || eventName === 'post_approval_response') {
    return [mapApproval(eventName, value, extra, makeEvent)];
  }
  if (eventName === 'subagent_stop') {
    const event = makeEvent('subagent.stopped', 'agent', 'subagent-stop', 'Hermes subagent stopped');
    const role = safeLabel(extra.child_role, 96);
    const status = safeLabel(extra.child_status, 96);
    const duration = nonNegativeInteger(extra.duration_ms);
    const childId = safeOpaque(extra.child_session_id ?? extra.session_id, 512);
    event.raw = { ...event.raw, subagent: {
      ...(role ? { role } : {}), ...(status ? { status } : {}), ...(duration !== undefined ? { duration_ms: duration } : {}),
      ...(childId ? { session_id_sha256: sha256(childId) } : {}),
    } };
    return [event];
  }

  const toolInput = firstRecord(value.tool_input, value.toolInput, extra.tool_input);
  const toolName = safeLabel(value.tool_name ?? value.toolName ?? extra.tool_name, 128) ?? 'unknown';
  const toolId = safeOpaque(value.tool_call_id ?? value.toolCallId ?? value.tool_use_id ?? extra.tool_call_id, 512);
  const command = boundedString(toolInput.command ?? extra.command, 1 << 20);
  if (eventName === 'pre_tool_call') {
    const event = makeEvent('tool.invoked', 'tool', `pre-tool:${toolName}`, 'Hermes tool invocation observed');
    event.tool = { name: toolName };
    const digest = contentDigest(toolInput);
    if (digest) event.content = digest;
    if (command) event.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
    if (toolId) event.gen_ai = { tool: { call: { id: `hermes-call:${sha256(toolId)}` } } };
    return [event];
  }

  const result = parseResult(extra.result ?? value.result);
  const resultError = boundedString(result.error, 4_096);
  const explicitFailure = result.is_error === true || resultError !== undefined;
  const output = resultText(result);
  const toolAction = classifyHermesTool(toolName, toolInput);
  const event = makeEvent(explicitFailure ? 'tool.failed' : toolAction.action, explicitFailure ? 'tool' : toolAction.category,
    `post-tool:${toolName}`, explicitFailure ? 'Hermes tool failed' : 'Hermes tool result observed', explicitFailure ? 'high' : 'info');
  event.tool = { name: toolName };
  const inputDigest = contentDigest(toolInput);
  if (inputDigest) event.content = inputDigest;
  if (toolId) event.gen_ai = { tool: { call: { id: `hermes-call:${sha256(toolId)}` } } };
  if (explicitFailure) event.error = { type: 'tool_error' };
  if (command) event.command = {
    command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8'),
    ...(isInteger(result.exit_code) ? { exit_code: result.exit_code } : {}),
    ...(output ? { output_sha256: sha256(output), output_bytes: Buffer.byteLength(output, 'utf8') } : {}),
  };
  if (output) event.content = contentDigest(output);
  const filePath = boundedString(toolInput.path ?? toolInput.file_path ?? toolInput.target_path ?? result.path, 4_096);
  if (filePath && toolAction.category === 'file') event.file = { path_sha256: sha256(filePath), operation: toolAction.operation ?? 'read' };
  return [event];
}

function mapApproval(
  eventName: string, value: JsonObject, extra: JsonObject,
  makeEvent: (action: string, category: string, suffix: string, message: string, severity?: ThreadforgeEvent['severity']) => ThreadforgeEvent,
): ThreadforgeEvent {
  const command = safeText(extra.command ?? value.command, 1 << 20);
  const description = safeText(extra.description ?? extra.reason ?? value.description, 4_096);
  if (eventName === 'pre_approval_request') {
    const event = makeEvent('approval.requested', 'approval', 'approval-request', 'Hermes approval requested');
    event.approval = { required: true, decision: 'requested', ...(description ? { reason_sha256: sha256(description) } : {}) };
    if (command) event.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
    const tool = safeLabel(extra.tool_name ?? value.tool_name, 128);
    if (tool) event.tool = { name: tool };
    return event;
  }
  const rawChoice = String(extra.choice ?? extra.decision ?? value.choice ?? value.decision ?? '').trim().toLowerCase();
  const decision = approvalDecision(rawChoice);
  const action = decision === 'approve' ? 'approval.allowed' : decision === 'deny' || decision === 'timeout' ? 'approval.denied' : 'approval.requested';
  const event = makeEvent(action, 'approval', 'approval-response', 'Hermes approval response observed');
  event.approval = { required: true, decision, ...(description ? { reason_sha256: sha256(description) } : {}) };
  if (command) event.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
  return event;
}

function approvalDecision(value: string): 'approve' | 'deny' | 'timeout' | 'unknown' {
  if (['approve', 'approved', 'allow', 'allowed', 'yes'].includes(value)) return 'approve';
  if (['deny', 'denied', 'reject', 'rejected', 'no'].includes(value)) return 'deny';
  if (['timeout', 'timed_out', 'expired'].includes(value)) return 'timeout';
  return 'unknown';
}

function classifyHermesTool(name: string, input: JsonObject): { action: string; category: string; operation?: string } {
  const lower = name.toLowerCase();
  if (lower === 'terminal' || boundedString(input.command, 1 << 20)) return { action: 'command.executed', category: 'command' };
  if (/read|view|list|search|grep/.test(lower)) return { action: 'file.read', category: 'file', operation: 'read' };
  if (/create|write/.test(lower)) return { action: 'file.created', category: 'file', operation: 'create' };
  if (/edit|patch/.test(lower)) return { action: 'file.modified', category: 'file', operation: 'modify' };
  if (lower.includes('mcp')) return { action: 'mcp.tool_invoked', category: 'mcp' };
  return { action: 'tool.completed', category: 'tool' };
}

function parseResult(value: unknown): JsonObject {
  if (isRecord(value)) return value;
  if (typeof value === 'string' && value.length <= maxInputBytes) {
    try { const parsed: unknown = JSON.parse(value); if (isRecord(parsed)) return parsed; } catch { /* opaque result */ }
    return { output: value };
  }
  return {};
}

function resultText(value: JsonObject): string {
  if (typeof value.output === 'string') return value.output;
  if (typeof value.result === 'string') return value.result;
  if (typeof value.content === 'string') return value.content;
  return '';
}

export function renderHermesHookConfig(commandTemplate?: string): string {
  const template = commandTemplate ?? defaultCommandTemplate();
  const eventSlots = typeof template === 'string' ? template.match(/\{event\}/g)?.length ?? 0 : 0;
  const markerSlots = typeof template === 'string' ? template.split(hermesManagedMarker).length - 1 : 0;
  if (typeof template !== 'string' || eventSlots !== 1 || markerSlots !== 1 || /[\r\n\0]/.test(template) || looksSecretLike(template)) {
    throw new Error(`command template must contain exactly one {event} and ${hermesManagedMarker}, with no credentials or line breaks`);
  }
  const timeouts: Partial<Record<HermesHookEvent, number>> = {
    pre_llm_call: 30, pre_tool_call: 10, post_tool_call: 10, pre_approval_request: 10,
    post_approval_response: 10, subagent_stop: 10,
  };
  const lines = ['hooks:'];
  for (const event of hermesHookEvents) {
    lines.push(`  ${event}:`, '    - command: ' + JSON.stringify(template.replace('{event}', event)));
    if (timeouts[event] !== undefined) lines.push(`      timeout: ${timeouts[event]}`);
    if (event === 'pre_tool_call' || event === 'post_tool_call') lines.push('      matcher: ".*"');
  }
  return `${lines.join('\n')}\n`;
}

function defaultCommandTemplate(): string {
  const cli = fileURLToPath(new URL('./cli.ts', import.meta.url));
  const quote = (value: string) => process.platform === 'win32' ? `"${value.replaceAll('"', '\\"')}"` : `'${value.replaceAll("'", "'\\''")}'`;
  return `${quote(process.execPath)} --experimental-strip-types ${quote(cli)} hermes hook {event} ${hermesManagedMarker}`;
}

/** Hook runner is fail-open and never writes a decision or context response to stdout. */
export async function runHermesHook(eventName: string, input: AsyncIterable<Uint8Array>, endpoint?: string): Promise<void> {
  try {
    const chunks: Buffer[] = [];
    let total = 0;
    for await (const chunk of input) {
      const bytes = Buffer.from(chunk);
      total += bytes.byteLength;
      if (total > maxInputBytes) return;
      chunks.push(bytes);
    }
    const payload: unknown = JSON.parse(Buffer.concat(chunks, total).toString('utf8'));
    const events = mapHermesHookPayload(eventName, payload);
    if (events.length) await emitEvents(events, { endpoint, timeoutMs: 250 });
  } catch {
    // Telemetry is observational; invalid payloads and collector failures never alter Hermes behavior.
  }
}

function isSupportedEvent(value: string): value is HermesHookEvent { return (hermesHookEvents as readonly string[]).includes(value); }
function firstString(source: JsonObject, keys: string[]): string { for (const key of keys) if (typeof source[key] === 'string' && source[key]) return source[key] as string; return ''; }
function firstRecord(...values: unknown[]): JsonObject { for (const value of values) if (isRecord(value)) return value; return {}; }
function boundedString(value: unknown, max: number): string | undefined { return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined; }
function safeText(value: unknown, max: number): string | undefined { return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined; }
function safeOpaque(value: unknown, max: number): string | undefined { const text = boundedString(value, max); return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined; }
function safeLabel(value: unknown, max: number): string | undefined {
  const text = typeof value === 'string' ? value.trim() : '';
  return text && text.length <= max && /^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) && !looksSecretLike(text) ? text : undefined;
}
function contentDigest(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)) ?? ''; } catch { return undefined; }
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return { retention: 'metadata', included: false, ...(bytes <= maxDigestBytes ? { hash: sha256(serialized) } : { omitted: true }), bytes };
}
function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}
function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function nonNegativeInteger(value: unknown): number | undefined { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined; }
function isInteger(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function normalizedOS(platform: string): string { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
