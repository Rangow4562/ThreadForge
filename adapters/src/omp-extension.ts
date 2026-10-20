// THREADFORGE_OMP_EXTENSION_TEMPLATE:v1
// Threadforge observational extension template for Oh My Pi (omp).
// The installer replaces the first line with a SHA-256 ownership marker.

import { createHash } from 'node:crypto';

export const ompExtensionTemplateMarker = '// THREADFORGE_OMP_EXTENSION_TEMPLATE:v1';
export const ompManagedMarker = '// threadforge-managed-omp-extension:v1';
const maxContentBytes = 64 << 10;
const endpoint = 'http://127.0.0.1:4318/v1/events';
const subscribedEvents = [
  'session_start', 'session_shutdown', 'before_agent_start', 'tool_call', 'tool_result',
  'tool_approval_requested', 'tool_approval_resolved', 'user_bash', 'user_python', 'message_end',
];

type JsonObject = Record<string, unknown>;
type Digest = { sha256?: string; bytes: number; omitted?: true };

/** Pure mapper for host fixtures; the installed extension registers the same event set below. */
export function mapOmpEvent(value: unknown, context?: unknown, capturedAt = new Date()): JsonObject[] {
  if (!isRecord(value) || typeof value.type !== 'string') return [];
  const eventName = value.type;
  if (!(subscribedEvents as readonly string[]).includes(eventName)) return [];
  const ctx = isRecord(context) ? context : {};
  const eventFields = baseEvent(value, ctx, capturedAt);
  const action = actionFor(eventName);
  if (eventName === 'message_end') {
    const message = isRecord(value.message) ? value.message : undefined;
    if (message?.role !== 'assistant') return [];
    const usage = normalizedUsage(message.usage);
    if (Object.keys(usage).length === 0) return [];
    eventFields.event = { kind: 'metric', action: 'token.usage', fidelity: 'observed' };
    eventFields.gen_ai = { usage };
    const model = modelVersion(message.model);
    if (model) eventFields.harness = { ...(eventFields.harness as JsonObject), version: model };
    return [eventFields];
  }
  if (!action) return [];
  eventFields.event = { kind: 'agent_runtime', action, fidelity: 'observed' };
  if (eventName === 'before_agent_start') {
    const prompt = digest(value.prompt);
    if (prompt) eventFields.content = contentMetadata(digestFields('prompt', prompt));
  }
  if (eventName === 'tool_call' || eventName === 'tool_result') {
    const toolName = safeLabel(value.toolName, 128);
    if (toolName) eventFields.tool = { name: toolName };
    const callId = boundedString(value.toolCallId, 512);
    if (callId) eventFields.raw = { ...(isRecord(eventFields.raw) ? eventFields.raw : {}), tool_call_id_sha256: sha256(callId) };
    const input = digest(value.input);
    const hasOutput = Object.hasOwn(value, 'content') || Object.hasOwn(value, 'details');
    const output = eventName === 'tool_result' && hasOutput
      ? digest({ ...(Object.hasOwn(value, 'content') ? { content: value.content } : {}), ...(Object.hasOwn(value, 'details') ? { details: value.details } : {}) })
      : undefined;
    if (input || output) eventFields.content = contentMetadata({
      ...(input ? digestFields('input', input) : {}), ...(output ? digestFields('output', output) : {}),
    });
    if (eventName === 'tool_result') {
      if (value.isError === true) {
        eventFields.event = { kind: 'agent_runtime', action: 'tool.failed', fidelity: 'observed' };
        eventFields.severity = 'medium';
      } else {
        const classified = classifyTool(toolName ?? '');
        if (classified.action) eventFields.event = { kind: 'agent_runtime', action: classified.action, fidelity: 'observed' };
        if (classified.operation) eventFields.file = { operation: classified.operation };
        if (classified.mcp) eventFields.mcp = classified.mcp;
      }
    }
  }
  if (eventName === 'tool_approval_requested' || eventName === 'tool_approval_resolved') {
    const toolName = safeLabel(value.toolName, 128);
    if (toolName) eventFields.tool = { name: toolName };
    const callId = boundedString(value.toolCallId, 512);
    if (callId) eventFields.raw = { ...(isRecord(eventFields.raw) ? eventFields.raw : {}), tool_call_id_sha256: sha256(callId) };
    const mode = enumLabel(value.approvalMode, ['always-ask', 'write', 'yolo']);
    if (mode) eventFields.raw = { ...(isRecord(eventFields.raw) ? eventFields.raw : {}), approval_mode: mode };
    const reason = digest(value.reason);
    if (reason) eventFields.content = contentMetadata(digestFields('reason', reason));
    if (eventName === 'tool_approval_requested') {
      eventFields.approval = { required: true, reason: 'runtime approval request observed' };
    } else if (typeof value.approved === 'boolean') {
      eventFields.event = {
        kind: 'agent_runtime', action: value.approved ? 'approval.allowed' : 'approval.denied', fidelity: 'observed',
      };
      eventFields.approval = { required: true, decision: value.approved ? 'approve' : 'deny' };
    } else return [];
  }
  if (eventName === 'user_bash' || eventName === 'user_python') {
    const source = eventName === 'user_bash' ? value.command : value.code;
    const codeDigest = digest(source);
    if (codeDigest) eventFields.content = contentMetadata(digestFields('operator_input', codeDigest));
    eventFields.tool = { name: eventName };
    eventFields.raw = {
      ...(isRecord(eventFields.raw) ? eventFields.raw : {}),
      user_initiated: true,
      ...(eventName === 'user_python' ? { user_python: true } : {}),
      ...(typeof value.excludeFromContext === 'boolean' ? { excluded_from_model_context: value.excludeFromContext } : {}),
    };
  }
  if (eventName === 'before_agent_start') {
    const link = handoffLink(eventFields, value.prompt);
    if (link) return [eventFields, link];
  }
  return [eventFields];
}

function handoffLink(event: JsonObject, prompt: unknown): JsonObject | undefined {
  if (typeof prompt !== 'string' || Buffer.byteLength(prompt, 'utf8') > (1 << 20)) return undefined;
  const match = /\[beacon-handoff from=([a-z0-9_-]{1,64}) session=([A-Za-z0-9._:\-]{1,200})\]/.exec(prompt);
  const session = isRecord(event.session) ? event.session : undefined;
  const id = boundedString(session?.id, 256);
  if (!match || !id) return undefined;
  return {
    timestamp: event.timestamp,
    vendor: event.vendor,
    product: event.product,
    schema_version: event.schema_version,
    event: { kind: 'agent_runtime', action: 'session.handoff', category: 'session', fidelity: 'observed' },
    severity: 'info',
    endpoint: event.endpoint,
    harness: event.harness,
    session: { id },
    handoff: { source_harness: match[1], source_session_id: match[2] },
  };
}

function actionFor(eventName: string): string | undefined {
  const actions: Record<string, string> = {
    session_start: 'session.started',
    session_shutdown: 'session.ended',
    before_agent_start: 'prompt.submitted',
    tool_call: 'tool.invoked',
    tool_result: 'tool.completed',
    tool_approval_requested: 'approval.requested',
    tool_approval_resolved: 'approval.result_observed',
    user_bash: 'command.executed',
    user_python: 'command.executed',
  };
  return actions[eventName];
}

function baseEvent(value: JsonObject, context: JsonObject, capturedAt: Date): JsonObject {
  const manager = isRecord(context.sessionManager) ? context.sessionManager : {};
  let sessionId = boundedString(value.sessionId, 512);
  if (!sessionId && typeof manager.getSessionId === 'function') {
    try { sessionId = boundedString((manager.getSessionId as () => unknown)(), 512); } catch { /* omit */ }
  }
  const model = modelVersion(context.model);
  const event: JsonObject = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: 'tool.invoked', fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'omp', collection_method: 'plugin', ...(model ? { version: model } : {}) },
    hook: { name: String(value.type).slice(0, 64) },
  };
  if (sessionId) event.session = { id: `omp:${sha256(sessionId)}` };
  const mode = enumLabel(context.mode, ['tui', 'rpc', 'json', 'print']);
  if (mode) event.raw = { runtime_mode: mode };
  return event;
}

function classifyTool(name: string): { action?: string; operation?: string; mcp?: JsonObject } {
  switch (name.toLowerCase()) {
    case 'bash': return { action: 'command.executed' };
    case 'read': return { action: 'file.read', operation: 'read' };
    case 'write': return { action: 'file.modified', operation: 'create' };
    case 'edit': return { action: 'file.modified', operation: 'modify' };
  }
  const mcp = /^mcp__([A-Za-z0-9.-]+)_([A-Za-z0-9_.-]+)$/.exec(name);
  if (mcp) return { action: 'mcp.tool_invoked', mcp: { server: mcp[1], tool: mcp[2] } };
  return {};
}

function normalizedUsage(value: unknown): JsonObject {
  if (!isRecord(value)) return {};
  const usage: JsonObject = {};
  for (const [source, target] of [['input', 'input_tokens'], ['output', 'output_tokens']] as const) {
    const count = boundedCount(value[source]);
    if (count !== undefined) usage[target] = count;
  }
  const cacheRead = boundedCount(value.cacheRead);
  if (cacheRead !== undefined) usage.cache_read = { input_tokens: cacheRead };
  const cacheWrite = boundedCount(value.cacheWrite);
  if (cacheWrite !== undefined) usage.cache_creation = { input_tokens: cacheWrite };
  const reasoning = boundedCount(value.reasoning);
  if (reasoning !== undefined) usage.reasoning = { output_tokens: reasoning };
  const cost = isRecord(value.cost) ? boundedMoney(value.cost.total) : undefined;
  if (cost !== undefined) usage.cost_usd = cost;
  return usage;
}

function modelVersion(value: unknown): string | undefined {
  if (!isRecord(value)) return safeLabel(value, 128);
  const id = safeLabel(value.id ?? value.name, 96);
  const provider = safeLabel(value.provider, 64);
  return id ? `${provider ? `${provider}_` : ''}${id}`.slice(0, 128) : undefined;
}

function digest(value: unknown): Digest | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value === 'string') {
    const bytes = Buffer.byteLength(value, 'utf8');
    return bytes > maxContentBytes ? { bytes, omitted: true } : { bytes, sha256: sha256(value) };
  }
  const canonical = boundedCanonicalJSON(value);
  if (canonical === undefined) return undefined;
  const bytes = Buffer.byteLength(canonical, 'utf8');
  return { bytes, sha256: sha256(canonical) };
}

function boundedCanonicalJSON(value: unknown): string | undefined {
  const pieces: string[] = [];
  let bytes = 0;
  let failed = false;
  const active = new WeakSet<object>();
  const append = (part: string): void => {
    bytes += Buffer.byteLength(part, 'utf8');
    if (bytes > maxContentBytes) failed = true;
    else pieces.push(part);
  };
  const visit = (item: unknown, depth: number): void => {
    if (failed) return;
    if (depth > 8) { failed = true; return; }
    if (item === null) { append('null'); return; }
    if (typeof item === 'string') { append(JSON.stringify(item)); return; }
    if (typeof item === 'boolean' || typeof item === 'number') { append(Number.isFinite(item as number) || typeof item === 'boolean' ? JSON.stringify(item) : 'null'); return; }
    if (Array.isArray(item)) {
      if (item.length > 4096) { failed = true; return; }
      const object = item as object;
      if (active.has(object)) { failed = true; return; }
      active.add(object); append('[');
      item.forEach((child, index) => { if (index) append(','); visit(child, depth + 1); });
      append(']'); active.delete(object); return;
    }
    if (isRecord(item)) {
      const keys = Object.keys(item).sort();
      if (keys.length > 4096 || active.has(item)) { failed = true; return; }
      active.add(item); append('{');
      keys.forEach((key, index) => { if (index) append(','); append(JSON.stringify(key)); append(':'); visit(item[key], depth + 1); });
      append('}'); active.delete(item); return;
    }
    append('null');
  };
  try { visit(value, 0); } catch { return undefined; }
  return failed ? undefined : pieces.join('');
}

function contentMetadata(fields: JsonObject): JsonObject {
  return { retention: 'metadata', included: false, ...fields };
}

function digestFields(prefix: string, value: Digest): JsonObject {
  return {
    [`${prefix}_bytes`]: value.bytes,
    ...(value.sha256 ? { [`${prefix}_sha256`]: value.sha256 } : {}),
    ...(value.omitted ? { [`${prefix}_omitted`]: true } : {}),
  };
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  return text.length > 0 && text.length <= max ? text : undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  return text && /^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(text) && !looksSecretLike(text) ? text : undefined;
}

function enumLabel(value: unknown, allowed: string[]): string | undefined {
  const text = boundedString(value, 64);
  return text && allowed.includes(text) ? text : undefined;
}

function boundedCount(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 1_000_000_000_000 ? value : undefined;
}

function boundedMoney(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1_000_000 ? value : undefined;
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function sha256(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function mapOmpEventWithPending(value: unknown, context: unknown, pending: Map<string, JsonObject>, capturedAt = new Date()): JsonObject[] {
  if (!isRecord(value) || typeof value.type !== 'string') return [];
  const id = boundedString(value.toolCallId, 512);
  if (value.type === 'tool_call' && id) {
    const name = safeLabel(value.toolName, 128);
    const input = digest(value.input);
    pending.delete(id);
    pending.set(id, { ...(name ? { tool_name: name } : {}), ...(input ? { input: input } : {}) });
    while (pending.size > 64) pending.delete(pending.keys().next().value as string);
  }
  let payload = value;
  if ((value.type === 'tool_approval_requested' || value.type === 'tool_approval_resolved') && id) {
    const memo = pending.get(id);
    if (memo) payload = { ...value, ...(memo.tool_name && !value.toolName ? { toolName: memo.tool_name } : {}) };
  }
  const result = mapOmpEvent(payload, context, capturedAt);
  const event = result[0];
  if (event && isRecord(value)) {
    const memo = id ? pending.get(id) : undefined;
    if (memo?.input && (value.type === 'tool_approval_requested' || value.type === 'tool_approval_resolved')) {
      event.content = contentMetadata({ ...(isRecord(event.content) ? event.content : {}), ...digestFields('decided_tool_input', memo.input as Digest) });
    }
  }
  if ((value.type === 'tool_result' || value.type === 'tool_approval_resolved') && id) pending.delete(id);
  if (value.type === 'session_start' || value.type === 'session_shutdown') pending.clear();
  return result;
}

function sendLocal(event: JsonObject): void {
  try {
    void fetch(endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(250),
      redirect: 'error',
    }).then((response) => {
      if (response.status !== 202) return;
    }).catch(() => {});
  } catch { /* telemetry never changes an OMP callback result */ }
}

export function createThreadforgeOmpExtension() {
  const pending = new Map<string, JsonObject>();
  const handler = (event: unknown, context?: unknown): void => {
    try {
      for (const payload of mapOmpEventWithPending(event, context, pending)) sendLocal(payload);
    } catch { /* observer callbacks stay fail-open and non-blocking */ }
    // Never return a result: OMP interprets values from several extension events as behavior changes.
  };
  return {
    register(pi: { on: (event: string, handler: (event: unknown, context?: unknown) => unknown) => void }): void {
      for (const name of subscribedEvents) pi.on(name, handler);
    },
    subscribedEvents: [...subscribedEvents],
  };
}

export default function threadforgeOmpExtension(pi: { on: (event: string, handler: (event: unknown, context?: unknown) => unknown) => void }): void {
  createThreadforgeOmpExtension().register(pi);
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
