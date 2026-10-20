// THREADFORGE_SENPI_EXTENSION_TEMPLATE:v1
// The installer replaces the first line with a SHA-256 ownership marker.

import { createHash } from 'node:crypto';

export const senpiExtensionTemplateMarker = '// THREADFORGE_SENPI_EXTENSION_TEMPLATE:v1';
export const senpiManagedMarker = '// threadforge-managed-senpi-extension:v1';
export const senpiExtensionEvents = [
  'session_start', 'session_shutdown', 'input', 'tool_call', 'tool_result', 'user_bash', 'message_end',
] as const;

const maxContentBytes = 64 << 10;
const endpoint = 'http://127.0.0.1:4318/v1/events';
const sendTimeoutMs = 250;

type JsonObject = Record<string, unknown>;
type Digest = { sha256?: string; bytes: number; omitted?: true };

/** Map only the seven documented Senpi agent-observation events. */
export function mapSenpiEvent(value: unknown, context?: unknown, capturedAt = new Date()): JsonObject[] {
  if (!isRecord(value) || typeof value.type !== 'string' || !isSenpiEvent(value.type)) return [];
  const ctx = isRecord(context) ? context : {};
  if (value.type === 'message_end') return mapAssistantMessageEnd(value, ctx, capturedAt);

  const event = baseEvent(value, ctx, capturedAt);
  if (value.type === 'session_start' || value.type === 'session_shutdown') {
    event.event = {
      kind: 'agent_runtime',
      action: value.type === 'session_start' ? 'session.started' : 'session.ended',
      fidelity: 'observed',
    };
    const reason = safeLabel(value.reason, 64);
    if (reason) event.raw = { ...(asRecord(event.raw) ?? {}), session_reason: reason };
    return [event];
  }
  if (value.type === 'input') {
    event.event = { kind: 'agent_runtime', action: 'prompt.submitted', fidelity: 'observed' };
    const promptValue = value.text ?? value.input;
    const prompt = digest(promptValue);
    if (prompt) event.content = contentMetadata(digestFields('prompt', prompt));
    const source = enumLabel(value.source, ['interactive', 'rpc', 'extension']);
    if (source) event.raw = { ...(asRecord(event.raw) ?? {}), input_source: source };
    const link = handoffLink(event, promptValue);
    return link ? [event, link] : [event];
  }
  if (value.type === 'user_bash') {
    event.event = { kind: 'agent_runtime', action: 'command.executed', fidelity: 'observed' };
    event.tool = { name: 'user_bash', action: 'operator_initiated' };
    event.raw = { ...(asRecord(event.raw) ?? {}), user_initiated: true };
    const command = digest(value.command);
    if (command) event.command = { ...contentMetadata(digestFields('command', command)) };
    return [event];
  }
  return mapToolEvent(value, event);
}

function handoffLink(event: JsonObject, prompt: unknown): JsonObject | undefined {
  if (typeof prompt !== 'string' || Buffer.byteLength(prompt, 'utf8') > (1 << 20)) return undefined;
  const match = /\[beacon-handoff from=([a-z0-9_-]{1,64}) session=([A-Za-z0-9._:\-]{1,200})\]/.exec(prompt);
  const session = asRecord(event.session);
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

function mapToolEvent(value: JsonObject, event: JsonObject): JsonObject[] {
  const toolName = safeLabel(value.toolName, 128);
  const callId = boundedString(value.toolCallId, 512);
  if (toolName) event.tool = { name: toolName };
  if (callId) event.raw = { ...(asRecord(event.raw) ?? {}), tool_call_id_sha256: sha256(callId) };

  const input = digest(value.input);
  const hasDetails = Object.hasOwn(value, 'details') && value.details !== undefined && value.details !== null;
  const output = value.type === 'tool_result' && hasDetails ? digest(value.details) : undefined;
  if (input || output) {
    event.content = contentMetadata({
      ...(input ? digestFields('input', input) : {}),
      ...(output ? digestFields('output', output) : {}),
    });
  }

  if (value.type === 'tool_call') {
    event.event = { kind: 'agent_runtime', action: 'tool.invoked', fidelity: 'observed' };
    return [event];
  }

  if (value.isError === true) {
    event.event = { kind: 'agent_runtime', action: 'tool.failed', fidelity: 'observed' };
    event.severity = 'medium';
    return [event];
  }

  const classification = classifyTool(toolName ?? '');
  event.event = { kind: 'agent_runtime', action: classification.action, fidelity: 'observed' };
  if (classification.operation) {
    const toolInput = asRecord(value.input) ?? {};
    const toolDetails = asRecord(value.details) ?? {};
    const filePath = boundedString(toolInput.path ?? toolInput.filePath ?? toolDetails.path, 4_096);
    const file: JsonObject = {
      operation: classification.operation,
      included: false,
      retention: 'metadata',
      ...(filePath ? { path_sha256: sha256(filePath) } : {}),
    };
    const fileContent = digest(toolDetails.patch ?? toolDetails.content ?? toolInput.content);
    if (fileContent) Object.assign(file, digestFields('content', fileContent));
    event.file = file;
  }
  if (classification.command) {
    const toolInput = asRecord(value.input) ?? {};
    const toolDetails = asRecord(value.details) ?? {};
    const commandText = toolInput.command ?? toolInput.script ?? toolInput.text;
    const command = digest(commandText);
    const exitCode = safeInteger(toolDetails.exitCode);
    event.command = {
      ...contentMetadata(command ? digestFields('command', command) : {}),
      ...(exitCode === undefined ? {} : { exit_code: exitCode }),
    };
  }
  if (classification.mcp) event.mcp = classification.mcp;
  return [event];
}

function mapAssistantMessageEnd(value: JsonObject, context: JsonObject, capturedAt: Date): JsonObject[] {
  const message = asRecord(value.message);
  if (!message || message.role !== 'assistant') return [];
  const base = baseEvent(value, context, capturedAt);
  const model = modelVersion(message.model);
  if (model) base.harness = { ...(asRecord(base.harness) ?? {}), version: model };
  const events: JsonObject[] = [];
  const usage = normalizedUsage(message.usage);
  if (Object.keys(usage).length > 0) {
    events.push({
      ...base,
      event: { kind: 'metric', action: 'token.usage', fidelity: 'observed' },
      gen_ai: { usage },
    });
  }
  const content = Array.isArray(message.content) ? message.content : [];
  const reasoning = content.slice(0, 128).flatMap((part) => {
    const item = asRecord(part);
    if (item?.type !== 'thinking' || typeof item.thinking !== 'string') return [];
    const hashed = digest(item.thinking);
    return hashed ? [{ type: 'reasoning', ...digestFields('content', hashed) }] : [];
  });
  if (reasoning.length > 0) {
    events.push({
      ...base,
      event: { kind: 'agent_runtime', action: 'agent.reasoning', fidelity: 'observed' },
      content: contentMetadata({ reasoning_parts: reasoning.length }),
      gen_ai: { output: { messages: [{ role: 'assistant', parts: reasoning }] } },
    });
  }
  return events;
}

function baseEvent(value: JsonObject, context: JsonObject, capturedAt: Date): JsonObject {
  const sessionManager = asRecord(context.sessionManager) ?? {};
  let sessionId = boundedString(value.sessionId, 512);
  if (!sessionId && typeof sessionManager.getSessionId === 'function') {
    try { sessionId = boundedString((sessionManager.getSessionId as () => unknown)(), 512); } catch { /* omit unavailable host metadata */ }
  }
  const event: JsonObject = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: 'tool.invoked', fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'omo_senpi', collection_method: 'plugin' },
    hook: { name: value.type },
  };
  if (sessionId) event.session = { id: `omo_senpi:${sha256(sessionId)}` };
  const cwd = boundedString(context.cwd ?? value.cwd, 4_096);
  if (cwd) event.project = { id: sha256(cwd) };
  const mode = enumLabel(context.mode, ['tui', 'rpc', 'json', 'print']);
  if (mode) event.raw = { runtime_mode: mode };
  return event;
}

function classifyTool(name: string): { action: string; operation?: 'read' | 'create' | 'modify'; command?: true; mcp?: JsonObject } {
  switch (name.toLowerCase()) {
    case 'bash': return { action: 'command.executed', command: true };
    case 'read': return { action: 'file.read', operation: 'read' };
    case 'write': return { action: 'file.modified', operation: 'create' };
    case 'edit': return { action: 'file.modified', operation: 'modify' };
  }
  const mcp = /^mcp__([A-Za-z0-9.-]+)_([A-Za-z0-9_.-]+)$/.exec(name);
  if (mcp) return { action: 'mcp.tool_invoked', mcp: { server: mcp[1], tool: mcp[2] } };
  return { action: 'tool.completed' };
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
  const cost = asRecord(value.cost);
  const costTotal = boundedMoney(cost?.total);
  if (costTotal !== undefined) usage.cost_usd = costTotal;
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
  const serialized = boundedCanonicalJSON(value);
  if (!serialized) return undefined;
  if (serialized.omitted) return { bytes: serialized.bytes, omitted: true };
  return { bytes: serialized.bytes, sha256: sha256(serialized.value) };
}

function boundedCanonicalJSON(value: unknown): { value: string; bytes: number; omitted?: false } | { value: ''; bytes: number; omitted: true } | undefined {
  const parts: string[] = [];
  let byteCount = 0;
  let exceeded = false;
  let nodes = 0;
  const active = new WeakSet<object>();
  const append = (part: string) => {
    byteCount += Buffer.byteLength(part, 'utf8');
    if (byteCount > maxContentBytes) exceeded = true;
    else parts.push(part);
  };
  const visit = (item: unknown, depth: number): void => {
    nodes++;
    if (exceeded || depth > 8 || nodes > 2_000) { exceeded = true; return; }
    if (item === null) { append('null'); return; }
    if (typeof item === 'string') { append(JSON.stringify(item)); return; }
    if (typeof item === 'number') { append(Number.isFinite(item) ? String(item) : 'null'); return; }
    if (typeof item === 'boolean') { append(item ? 'true' : 'false'); return; }
    if (Array.isArray(item)) {
      if (active.has(item) || item.length > 2_000) { exceeded = true; return; }
      active.add(item); append('[');
      item.forEach((child, index) => { if (index > 0) append(','); visit(child, depth + 1); });
      append(']'); active.delete(item); return;
    }
    if (isRecord(item)) {
      const keys = Object.keys(item).sort();
      if (active.has(item) || keys.length > 2_000) { exceeded = true; return; }
      active.add(item); append('{');
      keys.forEach((key, index) => { if (index > 0) append(','); append(JSON.stringify(key)); append(':'); visit(item[key], depth + 1); });
      append('}'); active.delete(item); return;
    }
    append('null');
  };
  try { visit(value, 0); } catch { return undefined; }
  if (exceeded) return { value: '', bytes: Math.max(byteCount, maxContentBytes + 1), omitted: true };
  return { value: parts.join(''), bytes: byteCount };
}

function sendLocal(event: JsonObject): void {
  try {
    void fetch(endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(sendTimeoutMs),
      redirect: 'error',
    }).then(() => {}).catch(() => {});
  } catch { /* observation never changes a Senpi callback result */ }
}

/** Create a self-contained extension; callbacks never block or return behavior-changing values. */
export function createThreadforgeSenpiExtension() {
  const handler = (event: unknown, context?: unknown): void => {
    try {
      for (const payload of mapSenpiEvent(event, context)) sendLocal(payload);
    } catch { /* observer callbacks remain fail-open */ }
  };
  return {
    register(pi: { on: (event: string, handler: (event: unknown, context?: unknown) => unknown) => void }): void {
      for (const name of senpiExtensionEvents) pi.on(name, handler);
    },
    subscribedEvents: [...senpiExtensionEvents],
  };
}

export default function threadforgeSenpiExtension(pi: { on: (event: string, handler: (event: unknown, context?: unknown) => unknown) => void }): void {
  createThreadforgeSenpiExtension().register(pi);
}

function digestFields(prefix: string, value: Digest): JsonObject {
  return {
    [`${prefix}_bytes`]: value.bytes,
    ...(value.sha256 ? { [`${prefix}_sha256`]: value.sha256 } : {}),
    ...(value.omitted ? { [`${prefix}_omitted`]: true } : {}),
  };
}
function contentMetadata(fields: JsonObject): JsonObject { return { retention: 'metadata', included: false, ...fields }; }
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
function safeInteger(value: unknown): number | undefined { return typeof value === 'number' && Number.isSafeInteger(value) ? value : undefined; }
function isSenpiEvent(value: string): value is typeof senpiExtensionEvents[number] {
  return (senpiExtensionEvents as readonly string[]).includes(value);
}
function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function asRecord(value: unknown): JsonObject | undefined { return isRecord(value) ? value : undefined; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
