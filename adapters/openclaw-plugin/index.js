import { createHash } from 'node:crypto';

export const openClawHooks = Object.freeze([
  'session_start', 'session_end', 'message_received', 'before_tool_call', 'after_tool_call', 'llm_output',
  'before_compaction', 'after_compaction', 'subagent_spawned', 'subagent_ended',
]);

const endpoint = 'http://127.0.0.1:4318/v1/events';
const maxContentBytes = 64 << 10;
const maxPendingCalls = 256;
const defaultQueueLimit = 128;

/** Maps only known OpenClaw hooks. Content and identities are fingerprinted in-process. */
export function mapOpenClawHook(hook, value, context = {}, capturedAt = new Date(), pending = new Map()) {
  if (!openClawHooks.includes(hook) || !isRecord(value)) return [];
  const ctx = isRecord(context) ? context : {};
  const event = baseEvent(hook, value, ctx, capturedAt);
  const toolName = safeLabel(value.toolName, 128);
  const callId = boundedString(value.toolCallId, 512);

  switch (hook) {
    case 'session_start':
      event.event.action = 'session.started';
      if (value.resumedFrom !== undefined && value.resumedFrom !== null) event.raw.resumed = true;
      return [event];
    case 'session_end':
      event.event.action = 'session.ended';
      addSafeLabel(event.raw, 'end_reason', value.reason, 64);
      return [event];
    case 'message_received': {
      event.event.action = 'prompt.submitted';
      const content = digest(messageContent(value));
      if (content) event.content = contentMetadata('prompt', content);
      const sender = boundedString(value.senderId ?? value.from, 512);
      if (sender) event.raw.sender_sha256 = sha256(sender);
      addSafeLabel(event.raw, 'channel', value.channel ?? ctx.channel, 64);
      return [event];
    }
    case 'before_tool_call': {
      event.event.action = 'tool.invoked';
      addToolMetadata(event, toolName, callId);
      const input = digest(value.params ?? value.input);
      if (input) event.content = contentMetadata('tool_input', input);
      if (callId && Array.isArray(value.derivedPaths)) {
        const paths = value.derivedPaths.slice(0, 100).flatMap((item) => {
          const path = boundedString(item, 4096);
          return path ? [sha256(path)] : [];
        });
        pending.delete(callId);
        if (paths.length) pending.set(callId, paths);
        while (pending.size > maxPendingCalls) pending.delete(pending.keys().next().value);
      }
      return [event];
    }
    case 'after_tool_call': {
      const classification = classifyTool(toolName ?? '');
      const failed = value.isError === true || value.error !== undefined && value.error !== null;
      const result = value.result;
      const running = isRecord(result) && result.status === 'running';
      event.event.action = failed ? 'tool.failed' : running ? 'command.started' : classification.action;
      addToolMetadata(event, toolName, callId);
      if (failed) event.severity = 'medium';
      if (classification.operation) event.file = { operation: classification.operation };
      if (classification.mcp) event.mcp = classification.mcp;
      const output = digest(result);
      if (output) event.content = contentMetadata('tool_output', output);
      const paths = callId ? pending.get(callId) : undefined;
      if (paths?.length) event.file = { ...(isRecord(event.file) ? event.file : {}), path_sha256: paths };
      if (callId) pending.delete(callId);
      return [event];
    }
    case 'llm_output': {
      const outputs = [];
      const usage = normalizeUsage(value.usage ?? value.tokens);
      if (Object.keys(usage).length) {
        event.event = { kind: 'metric', action: 'token.usage', fidelity: 'observed' };
        event.gen_ai = { usage };
        outputs.push(event);
      }
      const assistantOutput = digest(value.output ?? value.message);
      if (assistantOutput) {
        const messageEvent = baseEvent(hook, value, ctx, capturedAt);
        messageEvent.event.action = 'assistant.message.completed';
        messageEvent.content = contentMetadata('assistant_output', assistantOutput);
        outputs.push(messageEvent);
      }
      return outputs;
    }
    case 'before_compaction':
      event.event.action = 'session.compacting';
      return [event];
    case 'after_compaction':
      event.event.action = 'session.compacted';
      return [event];
    case 'subagent_spawned':
    case 'subagent_ended': {
      event.event.action = hook === 'subagent_spawned' ? 'subagent.started' : 'subagent.stopped';
      const child = boundedString(value.childSessionKey ?? value.childSessionId ?? value.sessionKey, 512);
      if (child) event.raw.child_session_sha256 = sha256(child);
      addSafeLabel(event.raw, 'subagent_id', value.agentId ?? value.childAgentId, 96);
      addSafeLabel(event.raw, 'subagent_model', value.model ?? value.modelId, 128);
      addSafeLabel(event.raw, 'subagent_outcome', value.outcome, 64);
      return [event];
    }
    default:
      return [];
  }
}

/** Observer callbacks return synchronously with undefined; telemetry cannot gate gateway work. */
export function createThreadforgeOpenClawPlugin(send = sendLocal, queueLimit = defaultQueueLimit) {
  if (!Number.isInteger(queueLimit) || queueLimit < 1 || queueLimit > 4096) {
    throw new Error('OpenClaw queue limit must be between 1 and 4096');
  }
  const pendingCalls = new Map();
  let queued = 0;
  let dropped = 0;
  let queue = Promise.resolve();
  const enqueue = (event) => {
    if (!event) return;
    if (queued >= queueLimit) {
      dropped++;
      return;
    }
    queued++;
    queue = queue.then(() => send(event)).catch(() => undefined).finally(() => { queued--; });
  };
  const handlers = Object.fromEntries(openClawHooks.map((hook) => [hook, (value, context) => {
    try {
      for (const event of mapOpenClawHook(hook, value, context, new Date(), pendingCalls)) enqueue(event);
    } catch { /* observers never throw into a gateway hook */ }
    return undefined;
  }]));

  return {
    hooks: [...openClawHooks],
    register(api) {
      for (const hook of openClawHooks) api.on(hook, handlers[hook]);
    },
    flush: () => queue,
    droppedEvents: () => dropped,
  };
}

export default {
  id: 'threadforge-observability',
  name: 'Threadforge Observational Telemetry',
  description: 'Sends privacy-minimized OpenClaw agent activity to a local Threadforge intake.',
  register(api) {
    createThreadforgeOpenClawPlugin().register(api);
  },
};

function baseEvent(hook, value, context, capturedAt) {
  const event = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: hook, fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'openclaw_gateway', collection_method: 'plugin' },
    hook: { name: hook },
    raw: {},
  };
  const sessionId = boundedString(value.sessionId ?? context.sessionId, 512);
  if (sessionId) event.session = { id: `openclaw:${sha256(sessionId)}` };
  const sessionKey = boundedString(value.sessionKey ?? context.sessionKey, 512);
  if (sessionKey) event.raw.conversation_key_sha256 = sha256(sessionKey);
  const runId = boundedString(value.runId ?? context.runId, 512);
  if (runId) event.raw.run_id_sha256 = sha256(runId);
  const provider = safeLabel(context.modelProviderId ?? value.provider, 64);
  const model = safeLabel(context.modelId ?? value.model, 96);
  if (model) event.model = provider ? `${provider}/${model}` : model;
  addSafeLabel(event.raw, 'agent_id', context.agentId ?? value.agentId, 96);
  return event;
}

function messageContent(value) {
  if (typeof value.content === 'string' || Array.isArray(value.content)) return value.content;
  if (typeof value.text === 'string') return value.text;
  if (typeof value.message === 'string') return value.message;
  if (isRecord(value.message)) return value.message.content ?? value.message.text;
  return undefined;
}

function addToolMetadata(event, name, callId) {
  if (name) event.tool = { name };
  if (callId) event.raw.tool_call_id_sha256 = sha256(callId);
}

function classifyTool(name) {
  const normalized = name.toLowerCase();
  if (normalized === 'exec' || normalized === 'bash' || normalized === 'shell') return { action: 'command.executed' };
  if (normalized === 'read' || normalized === 'grep' || normalized === 'find' || normalized === 'ls') {
    return { action: 'file.read', operation: 'read' };
  }
  if (normalized === 'write') return { action: 'file.created', operation: 'create' };
  if (normalized === 'edit' || normalized === 'apply_patch') return { action: 'file.modified', operation: 'modify' };
  const withoutPrefix = normalized.startsWith('mcp__') ? normalized.slice(5) : normalized;
  const separator = withoutPrefix.indexOf('__');
  if (separator > 0 && separator + 2 < withoutPrefix.length) {
    const server = safeLabel(withoutPrefix.slice(0, separator), 96);
    const tool = safeLabel(withoutPrefix.slice(separator + 2), 128);
    if (server && tool) return { action: 'mcp.tool_invoked', mcp: { server, tool } };
  }
  return { action: 'tool.completed' };
}

function normalizeUsage(value) {
  if (!isRecord(value)) return {};
  const usage = {};
  for (const [source, target] of [['input', 'input_tokens'], ['output', 'output_tokens']]) {
    const count = boundedCount(value[source]);
    if (count !== undefined) usage[target] = count;
  }
  for (const [source, target, nested] of [['cacheRead', 'cache_read', 'input_tokens'], ['cacheWrite', 'cache_creation', 'input_tokens']]) {
    const count = boundedCount(value[source]);
    if (count !== undefined) usage[target] = { [nested]: count };
  }
  const reasoning = boundedCount(value.reasoning ?? value.reasoningTokens);
  if (reasoning !== undefined) usage.reasoning = { output_tokens: reasoning };
  return usage;
}

function digest(value) {
  if (value === undefined || value === null) return undefined;
  if (typeof value === 'string') {
    const bytes = Buffer.byteLength(value, 'utf8');
    return bytes > maxContentBytes ? { bytes, omitted: true } : { bytes, sha256: sha256(value) };
  }
  const canonical = stableJSON(value);
  if (canonical === undefined) return undefined;
  const bytes = Buffer.byteLength(canonical, 'utf8');
  return { bytes, sha256: sha256(canonical) };
}

function stableJSON(value) {
  const parts = [];
  const active = new WeakSet();
  let bytes = 0;
  let nodes = 0;
  let failed = false;
  const append = (part) => {
    bytes += Buffer.byteLength(part, 'utf8');
    if (bytes > maxContentBytes) failed = true;
    else parts.push(part);
  };
  const visit = (item, depth) => {
    if (failed || ++nodes > 4096 || depth > 8) { failed = true; return; }
    if (item === null) { append('null'); return; }
    if (typeof item === 'string') {
      if (Buffer.byteLength(item, 'utf8') > maxContentBytes) { failed = true; return; }
      append(JSON.stringify(item)); return;
    }
    if (typeof item === 'boolean') { append(item ? 'true' : 'false'); return; }
    if (typeof item === 'number') { append(Number.isFinite(item) ? JSON.stringify(item) : 'null'); return; }
    if (Array.isArray(item)) {
      if (item.length > 4096 || active.has(item)) { failed = true; return; }
      active.add(item); append('[');
      item.forEach((child, index) => { if (index) append(','); visit(child, depth + 1); });
      append(']'); active.delete(item); return;
    }
    if (isRecord(item)) {
      const keys = Object.keys(item).sort();
      if (keys.length > 4096 || active.has(item)) { failed = true; return; }
      active.add(item); append('{');
      keys.forEach((key, index) => { if (index) append(','); append(JSON.stringify(key)); append(':'); visit(item[key], depth + 1); });
      append('}'); active.delete(item); return;
    }
    failed = true;
  };
  try { visit(value, 0); } catch { return undefined; }
  return failed ? undefined : parts.join('');
}

function contentMetadata(kind, content) {
  return {
    retention: 'metadata', included: false, kind, bytes: content.bytes,
    ...(content.sha256 ? { sha256: content.sha256 } : {}), ...(content.omitted ? { omitted: true } : {}),
  };
}

function safeLabel(value, max) {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) || looksSecretLike(text)) return undefined;
  return text;
}

function addSafeLabel(target, key, value, max) {
  const label = safeLabel(value, max);
  if (label) target[key] = label;
}

function boundedString(value, max) {
  return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined;
}

function boundedCount(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 1_000_000_000_000 ? value : undefined;
}

function looksSecretLike(value) {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function sha256(value) {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function normalizedOS(platform) {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function isRecord(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

async function sendLocal(event) {
  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(250),
      redirect: 'error',
    });
    if (response.status !== 202) return;
  } catch { /* gateway actions never depend on local telemetry */ }
}
