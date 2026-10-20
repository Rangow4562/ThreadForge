import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const museManagedMarker = '--threadforge-hook=muse';
export const museHookEvents = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PermissionRequest', 'PostToolUse',
  'PostToolUseFailure', 'PreCompact', 'PostCompact', 'SubagentStart', 'SubagentStop',
  'Notification', 'Stop', 'SessionEnd',
] as const;
export type MuseHookEvent = typeof museHookEvents[number];

const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const actions: Record<MuseHookEvent, string> = {
  SessionStart: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PermissionRequest: 'permission.requested',
  PostToolUse: 'tool.completed',
  PostToolUseFailure: 'tool.failed',
  PreCompact: 'session.compacting',
  PostCompact: 'session.compacted',
  SubagentStart: 'subagent.started',
  SubagentStop: 'subagent.completed',
  Notification: 'agent.notification',
  Stop: 'assistant.turn_stopped',
  SessionEnd: 'session.ended',
};

type JsonObject = Record<string, unknown>;

/** Maps the documented Muse event names and Beacon-observed payload envelope to local events. */
export function mapMuseHookPayload(value: unknown, hookEvent: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value) || typeof hookEvent !== 'string' || !(museHookEvents as readonly string[]).includes(hookEvent)) return undefined;
  const eventName = hookEvent as MuseHookEvent;
  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: actions[eventName], fidelity: 'observed' },
    severity: eventName === 'PostToolUseFailure' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'muse_code', collection_method: 'hook' },
    hook: { name: eventName },
  };

  const sessionID = boundedString(value.session_id, 512);
  if (sessionID) event.session = { id: `muse:${sha256(sessionID)}` };
  const model = safeLabel(value.model, 128);
  if (model && model !== 'unknown') event.harness = { ...event.harness, version: model };
  const turnID = boundedString(value.turn_id, 512);
  if (turnID) event.raw = { turn_id_sha256: sha256(turnID) };

  if (eventName === 'SessionStart') {
    const source = enumLabel(value.source, ['startup', 'resume', 'clear', 'compact']);
    if (source) event.raw = { ...(isRecord(event.raw) ? event.raw : {}), source };
  }
  if (eventName === 'UserPromptSubmit') {
    const prompt = contentDigest(value.prompt);
    if (prompt) event.content = contentMetadata({
      ...(prompt.hash ? { prompt_sha256: prompt.hash } : {}), prompt_bytes: prompt.bytes,
      ...(prompt.omitted ? { prompt_omitted: true } : {}),
    });
    markPromptHandoff(event, value.prompt);
  }
  if (['PreToolUse', 'PostToolUse', 'PostToolUseFailure', 'PermissionRequest'].includes(eventName)) {
    const toolName = safeLabel(value.tool_name, 128);
    if (toolName) event.tool = { name: toolName };
    const callID = boundedString(value.tool_use_id ?? value.tool_call_id, 512);
    if (callID) event.raw = { ...(isRecord(event.raw) ? event.raw : {}), tool_call_id_sha256: sha256(callID) };
    const permissionMode = enumLabel(value.permission_mode, ['default', 'acceptEdits', 'plan', 'bypassPermissions', 'dontAsk']);
    if (permissionMode) event.raw = { ...(isRecord(event.raw) ? event.raw : {}), permission_mode: permissionMode };

    const input = contentDigest(value.tool_input);
    const output = eventName === 'PostToolUse' || eventName === 'PostToolUseFailure' ? contentDigest(value.tool_response) : undefined;
    if (input || output) event.content = contentMetadata({
      ...(input ? { ...(input.hash ? { input_sha256: input.hash } : {}), input_bytes: input.bytes, ...(input.omitted ? { input_omitted: true } : {}) } : {}),
      ...(output ? { ...(output.hash ? { output_sha256: output.hash } : {}), output_bytes: output.bytes, ...(output.omitted ? { output_omitted: true } : {}) } : {}),
    });
    if (eventName === 'PostToolUse' && toolName) {
      const classification = classifyTool(toolName, value.tool_input);
      if (classification.action) event.event = { ...event.event, action: classification.action };
      if (classification.operation) event.file = { operation: classification.operation };
    }
    if (eventName === 'PermissionRequest') {
      event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
    }
  }
  if (eventName === 'Notification') {
    const notificationType = safeLabel(value.notification_type, 96);
    if (notificationType) event.raw = { ...(isRecord(event.raw) ? event.raw : {}), notification_type: notificationType };
  }
  if (eventName === 'SubagentStart' || eventName === 'SubagentStop') {
    const agentID = boundedString(value.subagent_id ?? value.agent_id, 512);
    const childSessionID = boundedString(value.child_session_id, 512);
    const agentType = safeLabel(value.agent_type, 96);
    event.raw = {
      ...(isRecord(event.raw) ? event.raw : {}),
      ...(agentID ? { subagent_id_sha256: sha256(agentID) } : {}),
      ...(childSessionID ? { child_session_id_sha256: sha256(childSessionID) } : {}),
      ...(agentType ? { agent_type: agentType } : {}),
    };
  }
  if (eventName === 'Stop') {
    const response = contentDigest(value.last_assistant_message);
    if (response) event.content = contentMetadata({
      ...(response.hash ? { response_sha256: response.hash } : {}), response_bytes: response.bytes,
      ...(response.omitted ? { response_omitted: true } : {}),
    });
  }
  return event;
}

function classifyTool(toolName: string, input: unknown): { action?: string; operation?: string } {
  const normalized = toolName.toLowerCase();
  const objectInput = isRecord(input) ? input : {};
  if (['bash', 'shell', 'execute_command'].includes(normalized)) return { action: 'command.executed' };
  if (normalized.startsWith('subagent_')) return {};
  const hasPath = ['path', 'file_path', 'target_file'].some((key) => typeof objectInput[key] === 'string' && (objectInput[key] as string).length > 0);
  if (normalized === 'read_skill') return { action: 'file.read', operation: 'read' };
  if (hasPath && /(?:read|search|list)/.test(normalized)) return { action: 'file.read', operation: 'read' };
  if (hasPath && /(?:edit|write|create|patch)/.test(normalized)) {
    return { action: 'file.modified', operation: /create/.test(normalized) ? 'create' : 'modify' };
  }
  return {};
}

function contentDigest(value: unknown): { hash?: string; bytes: number; omitted: boolean } | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return { ...(bytes > maxContentBytes ? {} : { hash: sha256(serialized) }), bytes, omitted: bytes > maxContentBytes };
}

function contentMetadata(fields: JsonObject): JsonObject {
  return { retention: 'metadata', included: false, ...fields };
}

function enumLabel(value: unknown, allowed: string[]): string | undefined {
  const normalized = boundedString(value, 64);
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

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function sha256(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
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
    if (total > maxInputBytes) throw new Error('Muse hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const payload = JSON.parse(input.toString('utf8')) as unknown;
    const eventName = process.argv.slice(2).find((arg) => arg.startsWith('--hook-event='))?.slice('--hook-event='.length);
    const event = mapMuseHookPayload(payload, eventName);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
  } catch {
    process.stderr.write('threadforge Muse hook capture skipped: invalid payload or local intake unavailable\n');
  }
  // Muse's command-hook protocol uses an empty no-decision object; never print memory or raw data.
  process.stdout.write('{}\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
