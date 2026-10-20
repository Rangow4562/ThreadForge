import { createHash } from 'node:crypto';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const devinManagedMarker = '--threadforge-devin-hook=v1';
export const devinHookEvents = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PermissionRequest', 'PostToolUse', 'Stop', 'SessionEnd',
] as const;
export type DevinHookEvent = typeof devinHookEvents[number];

const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const actionByEvent: Record<DevinHookEvent, string> = {
  SessionStart: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PermissionRequest: 'permission.requested',
  PostToolUse: 'tool.completed',
  Stop: 'assistant.turn_stopped',
  SessionEnd: 'session.ended',
};

type JsonObject = Record<string, unknown>;
type Digest = { hash?: string; bytes: number; omitted: boolean };

export interface DevinHookOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
  now?: () => Date;
}

export interface TextWriter { write(value: string): unknown }

/** Map a pinned Devin CLI hook event to privacy-minimized, non-authorizing telemetry. */
export function mapDevinHookPayload(
  value: unknown,
  hookEvent: unknown,
  capturedAt = new Date(),
): ThreadforgeEvent | undefined {
  if (!isRecord(value) || !isDevinHookEvent(hookEvent)) return undefined;
  const suppliedEvent = value.hook_event_name ?? value.hookEventName;
  if (suppliedEvent !== undefined && suppliedEvent !== hookEvent) return undefined;

  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: actionByEvent[hookEvent], fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'devin_cli', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const sessionId = boundedString(value.session_id ?? value.sessionId ?? value.conversation_id, 512);
  if (sessionId) event.session = { id: `devin:${sha256(sessionId)}` };

  const workspace = boundedString(value.cwd ?? value.working_dir ?? value.project_path, 4096);
  if (workspace) event.project = { id: sha256(workspace) };

  if (hookEvent === 'UserPromptSubmit') {
    const promptValue = value.prompt ?? value.user_prompt ?? value.message;
    const prompt = digestContent(promptValue);
    if (prompt) event.content = digestMetadata('prompt', prompt);
    markPromptHandoff(event, promptValue);
  }

  if (hookEvent === 'PreToolUse' || hookEvent === 'PermissionRequest' || hookEvent === 'PostToolUse') {
    const name = safeLabel(value.tool_name ?? value.toolName ?? value.tool, 128);
    const callId = boundedString(value.tool_use_id ?? value.tool_call_id ?? value.call_id, 512);
    const toolInput = digestContent(value.tool_input ?? value.toolInput ?? value.input ?? value.args);
    const toolOutput = hookEvent === 'PostToolUse'
      ? digestContent(value.tool_response ?? value.toolResponse ?? value.tool_output ?? value.result)
      : undefined;
    if (name) event.tool = { name };
    if (callId) event.raw = { tool_call_id_sha256: sha256(callId) };

    const content: JsonObject = { retention: 'metadata', included: false };
    if (toolInput) Object.assign(content, digestFields('input', toolInput));
    if (toolOutput) Object.assign(content, digestFields('output', toolOutput));
    if (Object.keys(content).length > 2) event.content = content;

    if (hookEvent === 'PermissionRequest') {
      // Record that the hook fired, never the host's eventual decision.
      event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
    }
  }
  return event;
}

/** Run fail-open with empty stdout: capture can never authorize or block Devin actions. */
export async function runDevinHook(
  hookEvent: string,
  input: AsyncIterable<Uint8Array>,
  stdout: TextWriter,
  stderr: TextWriter,
  options: DevinHookOptions = {},
): Promise<void> {
  void stdout;
  try {
    if (!isDevinHookEvent(hookEvent)) throw new Error('unsupported hook event');
    const chunks: Buffer[] = [];
    let bytesRead = 0;
    for await (const chunk of input) {
      const bytes = Buffer.from(chunk);
      bytesRead += bytes.byteLength;
      if (bytesRead > maxInputBytes) throw new Error('payload too large');
      chunks.push(bytes);
    }
    const json = new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks, bytesRead));
    const payload: unknown = JSON.parse(json);
    const event = mapDevinHookPayload(payload, hookEvent, options.now?.() ?? new Date());
    if (!event) throw new Error('event mismatch');
    await emitEvent(event, {
      endpoint: options.endpoint,
      timeoutMs: options.timeoutMs ?? 250,
      fetchImpl: options.fetchImpl,
    });
  } catch {
    stderr.write('Threadforge Devin hook capture skipped; no policy decision was made\n');
  }
}

function isDevinHookEvent(value: unknown): value is DevinHookEvent {
  return typeof value === 'string' && (devinHookEvents as readonly string[]).includes(value);
}

function digestContent(value: unknown): Digest | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try {
    const normalized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value));
    if (typeof normalized !== 'string') return undefined;
    serialized = normalized;
  } catch { return undefined; }
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return { ...(bytes <= maxContentBytes ? { hash: sha256(serialized) } : {}), bytes, omitted: bytes > maxContentBytes };
}

function digestMetadata(prefix: string, value: Digest): JsonObject {
  return { retention: 'metadata', included: false, ...digestFields(prefix, value) };
}

function digestFields(prefix: string, value: Digest): JsonObject {
  return {
    ...(value.hash ? { [`${prefix}_sha256`]: value.hash } : {}),
    [`${prefix}_bytes`]: value.bytes,
    ...(value.omitted ? { [`${prefix}_omitted`]: true } : {}),
  };
}

function canonicalize(value: unknown, depth = 0, budget = { nodes: 0 }): unknown {
  budget.nodes++;
  if (depth > 8 || budget.nodes > 2_000) throw new Error('content structure exceeds digest bounds');
  if (Array.isArray(value)) return value.map((child) => canonicalize(child, depth + 1, budget));
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key], depth + 1, budget)]));
  return value;
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const normalized = boundedString(value, max);
  if (!normalized || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(normalized) || looksSecretLike(normalized)) return undefined;
  return normalized;
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
