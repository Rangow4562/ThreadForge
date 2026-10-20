import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const kimiCodeManagedMarker = '--threadforge-hook=kimi';
const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const actions: Record<string, string> = {
  SessionStart: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PostToolUse: 'tool.completed',
  PostToolUseFailure: 'tool.failed',
  PermissionRequest: 'permission.requested',
  Stop: 'assistant.turn_stopped',
  SessionEnd: 'session.ended',
  SubagentStart: 'subagent.started',
  SubagentStop: 'subagent.completed',
  PreCompact: 'session.compacting',
  PostCompact: 'session.compacted',
  StopFailure: 'assistant.turn_failed',
  Interrupt: 'assistant.turn_interrupted',
  TaskStarted: 'background_task.started',
  Notification: 'background_task.notification',
};

type JsonObject = Record<string, unknown>;
interface Digest { hash: string; bytes: number; }

/** Maps Kimi Code CLI hook JSON to privacy-minimized local Threadforge events. */
export function mapKimiCodeHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const hookEvent = boundedString(value.hook_event_name, 64);
  if (!hookEvent || !actions[hookEvent] && hookEvent !== 'PermissionResult') return undefined;
  const permissionDecision = hookEvent === 'PermissionResult'
    ? enumLabel(value.decision, ['approved', 'rejected', 'cancelled', 'error'])
    : undefined;
  // Do not turn an absent or future host result into an approval/denial signal.
  if (hookEvent === 'PermissionResult' && !permissionDecision) return undefined;
  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: actions[hookEvent] ?? 'approval.allowed', fidelity: 'observed' },
    severity: hookEvent === 'PostToolUseFailure' || hookEvent === 'StopFailure' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'kimi_code', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const sessionID = boundedString(value.session_id, 512);
  if (sessionID) event.session = { id: `kimi:${sha256(sessionID)}` };

  const clientType = safeLabel(value.client_type, 64);
  if (clientType) event.harness = { ...event.harness, version: clientType };

  if (hookEvent === 'SessionStart') {
    const source = enumLabel(value.source, ['startup', 'resume']);
    const model = safeLabel(value.model, 128);
    const profile = safeLabel(value.profile, 64);
    if (source || model || profile) event.raw = {
      ...(source ? { source } : {}), ...(model ? { model } : {}), ...(profile ? { profile } : {}),
    };
  }

  if (hookEvent === 'SessionEnd') {
    const reason = enumLabel(value.reason, ['exit', 'archive']);
    if (reason) event.raw = { reason };
  }

  if (hookEvent === 'UserPromptSubmit') {
    const prompt = digest(value.prompt);
    if (prompt) event.content = { retention: 'metadata', included: false, prompt_sha256: prompt.hash, prompt_bytes: prompt.bytes };
    if (typeof value.is_steer === 'boolean') event.raw = { is_steer: value.is_steer };
    markPromptHandoff(event, value.prompt);
  }

  if (['PreToolUse', 'PostToolUse', 'PostToolUseFailure'].includes(hookEvent)) {
    const toolName = safeLabel(value.tool_name, 128);
    const toolInput = isRecord(value.tool_input) ? value.tool_input : {};
    const toolCallID = boundedString(value.tool_call_id, 512);
    if (toolName) event.tool = { name: toolName };
    if (toolCallID) event.raw = { tool_call_id_sha256: sha256(toolCallID) };
    const input = digest(value.tool_input);
    const output = digest(value.tool_output);
    if (input || output) event.content = {
      retention: 'metadata', included: false,
      ...(input ? { input_sha256: input.hash, input_bytes: input.bytes } : {}),
      ...(output ? { output_sha256: output.hash, output_bytes: output.bytes } : {}),
    };

    if (hookEvent === 'PostToolUseFailure') event.event = { ...event.event, action: 'tool.failed' };
    else if (hookEvent === 'PostToolUse' && hasFailure(value.error)) {
      event.event = { ...event.event, action: 'tool.failed' };
      event.severity = 'medium';
    } else if (hookEvent === 'PostToolUse' && toolName) {
      const classified = classifyTool(toolName, toolInput);
      if (classified.action) event.event = { ...event.event, action: classified.action };
      if (classified.mcp) event.mcp = classified.mcp;
      if (classified.operation) event.file = { operation: classified.operation };
    }
  }

  if (hookEvent === 'PermissionRequest') {
    const toolName = safeLabel(value.tool_name, 128);
    if (toolName) event.tool = { name: toolName };
    event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
  }

  if (hookEvent === 'PermissionResult') {
    const decision = permissionDecision!;
    const action = decision === 'approved' ? 'approval.allowed'
      : decision === 'rejected' ? 'approval.denied'
        : decision === 'cancelled' ? 'permission.cancelled' : 'permission.result_error';
    event.event = { ...event.event, action };
    event.approval = {
      required: true,
      decision: decision === 'approved' ? 'approve' : decision === 'rejected' ? 'deny' : decision,
    };
  }

  if (hookEvent === 'SubagentStart' || hookEvent === 'SubagentStop') {
    const id = boundedString(value.agent_id ?? value.agentId, 512);
    const type = safeLabel(value.agent_type ?? value.agent_name, 128);
    if (id || type) event.raw = { ...(id ? { agent_id_sha256: sha256(id) } : {}), ...(type ? { agent_type: type } : {}) };
  }

  if (hookEvent === 'TaskStarted') {
    const id = boundedString(value.task_id, 512);
    const taskType = safeLabel(value.task_type, 64);
    const description = digest(value.description);
    const raw: JsonObject = {
      ...(id ? { task_id_sha256: sha256(id) } : {}),
      ...(taskType ? { task_type: taskType } : {}),
      ...(description ? { description_sha256: description.hash, description_bytes: description.bytes } : {}),
    };
    if (Object.keys(raw).length > 0) event.raw = raw;
  }

  if (hookEvent === 'Notification') {
    const type = safeLabel(value.notification_type, 64);
    if (type) event.raw = { notification_type: type };
  }

  return event;
}

function classifyTool(name: string, input: JsonObject): { action?: string; operation?: string; mcp?: JsonObject } {
  if (name === 'Bash') return { action: 'command.executed' };
  if (['Read', 'ReadMediaFile', 'Glob', 'Grep'].includes(name)) return { action: 'file.read', operation: 'read' };
  if (name === 'Write') return { action: 'file.modified', operation: input.mode === 'append' ? 'modify' : 'create' };
  if (name === 'Edit') return { action: 'file.modified', operation: 'modify' };
  const mcpMatch = /^mcp__([A-Za-z0-9_.-]+)__([A-Za-z0-9_.-]+)$/.exec(name);
  if (mcpMatch) return { action: 'mcp.tool_invoked', mcp: { server: mcpMatch[1], tool: mcpMatch[2] } };
  return {};
}

function digest(value: unknown): Digest | undefined {
  if (value === undefined || value === null) return undefined;
  let text: string;
  try { text = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof text !== 'string') return undefined;
  const bytes = Buffer.byteLength(text, 'utf8');
  if (bytes > maxContentBytes) return undefined;
  return { hash: sha256(text), bytes };
}

function hasFailure(value: unknown): boolean {
  if (value === undefined || value === null || value === false || value === '') return false;
  return !isRecord(value) || Object.keys(value).length > 0;
}

function enumLabel(value: unknown, allowed: string[]): string | undefined {
  const normalized = boundedString(value, 64)?.toLowerCase();
  return normalized && allowed.includes(normalized) ? normalized : undefined;
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const normalized = boundedString(value, max);
  if (!normalized || !/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(normalized) || looksSecretLike(normalized)) return undefined;
  return normalized;
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function sha256(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

async function readBoundedStdin(): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of process.stdin) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    total += bytes.byteLength;
    if (total > maxInputBytes) throw new Error('Kimi Code hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const event = mapKimiCodeHookPayload(JSON.parse(input.toString('utf8')) as unknown);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
  } catch {
    process.stderr.write('threadforge Kimi Code hook capture skipped: invalid payload or local intake unavailable\n');
  }
  // In particular, UserPromptSubmit stdout becomes model context. Never write a host response.
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
