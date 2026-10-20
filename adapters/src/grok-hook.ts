import { createHash } from 'node:crypto';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const grokManagedMarker = '--threadforge-grok-hook=v1';

export const grokHookActions = [
  'session-start', 'prompt-submit', 'pre-tool', 'post-tool', 'post-tool-failure', 'stop', 'session-end',
] as const;
export type GrokHookAction = typeof grokHookActions[number];

const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const eventNames: Record<GrokHookAction, string> = {
  'session-start': 'session_start',
  'prompt-submit': 'user_prompt_submit',
  'pre-tool': 'pre_tool_use',
  'post-tool': 'post_tool_use',
  'post-tool-failure': 'post_tool_use_failure',
  stop: 'stop',
  'session-end': 'session_end',
};
const eventActions: Record<GrokHookAction, string> = {
  'session-start': 'session.started',
  'prompt-submit': 'prompt.submitted',
  'pre-tool': 'tool.invoked',
  'post-tool': 'tool.completed',
  'post-tool-failure': 'tool.failed',
  stop: 'assistant.turn_stopped',
  'session-end': 'session.ended',
};

type JsonObject = Record<string, unknown>;
export interface GrokHookOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
  now?: () => Date;
}
export interface TextWriter { write(value: string): unknown }

/** Maps a documented Grok hook payload without retaining prompt, tool, path, or error text. */
export function mapGrokHookPayload(
  value: unknown,
  action: unknown,
  capturedAt = new Date(),
): ThreadforgeEvent | undefined {
  if (!isRecord(value) || !isGrokHookAction(action)) return undefined;
  const expectedEvent = eventNames[action];
  const observedEvent = value.hookEventName ?? value.hook_event_name;
  if (typeof observedEvent !== 'string' || observedEvent.toLowerCase() !== expectedEvent) return undefined;

  const failed = action === 'post-tool-failure';
  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: eventActions[action], fidelity: 'observed' },
    severity: failed ? 'high' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'grok_build', collection_method: 'hook' },
    hook: { name: expectedEvent },
  };

  const sessionId = boundedString(value.sessionId ?? value.session_id, 512);
  const workspace = boundedString(value.workspaceRoot ?? value.workspace_root ?? value.cwd, 4096);
  if (sessionId) event.session = { id: `grok:${sha256(sessionId)}` };
  if (workspace) event.project = { id: sha256(workspace) };

  if (action === 'prompt-submit') {
    const prompt = contentDigest(value.prompt);
    if (prompt) event.content = digestMetadata('prompt', prompt);
    markPromptHandoff(event, value.prompt);
  }

  if (action === 'pre-tool' || action === 'post-tool' || action === 'post-tool-failure') {
    const name = safeLabel(value.toolName ?? value.tool_name, 128);
    const callId = boundedString(value.toolCallId ?? value.tool_call_id ?? value.toolUseId ?? value.tool_use_id, 512);
    const input = contentDigest(value.toolInput ?? value.tool_input);
    const output = action === 'post-tool' || failed
      ? contentDigest(value.toolOutput ?? value.tool_output ?? value.toolResponse ?? value.tool_response)
      : undefined;
    const error = failed ? contentDigest(value.error) : undefined;
    if (name) event.tool = { name };
    const raw: JsonObject = {};
    if (callId) raw.tool_call_id_sha256 = sha256(callId);
    if (Object.keys(raw).length > 0) event.raw = raw;

    if (name && isCommandTool(name)) {
      const command = readRecord(value.toolInput ?? value.tool_input);
      const commandValue = contentDigest(command?.command);
      event.command = {
        ...(commandValue?.hash ? { sha256: commandValue.hash } : {}),
        ...(commandValue ? { bytes: commandValue.bytes } : {}),
        ...(commandValue?.omitted ? { omitted: true } : {}),
        retention: 'metadata',
      };
      if (action === 'post-tool') event.event = { ...event.event, action: 'command.executed' };
    } else if (action === 'post-tool') {
      event.event = { ...event.event, action: 'tool.completed' };
    }

    const fields: JsonObject = {};
    if (input) Object.assign(fields, digestMetadataFields('input', input));
    if (output) Object.assign(fields, digestMetadataFields('output', output));
    if (error) Object.assign(fields, digestMetadataFields('error', error));
    if (Object.keys(fields).length > 0) event.content = { retention: 'metadata', included: false, ...fields };
  }

  if (action === 'stop') {
    const response = contentDigest(value.lastAssistantMessage ?? value.last_assistant_message);
    if (response) event.content = digestMetadata('response', response);
  }
  return event;
}

/** Runs fail-open and emits no stdout decision; telemetry never authorizes or blocks an action. */
export async function runGrokHook(
  action: string,
  input: AsyncIterable<Uint8Array>,
  stdout: TextWriter,
  stderr: TextWriter,
  options: GrokHookOptions = {},
): Promise<void> {
  void stdout;
  try {
    if (!isGrokHookAction(action)) throw new Error('unsupported action');
    const bytes = await readBoundedInput(input);
    const payload: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    const event = mapGrokHookPayload(payload, action, options.now?.() ?? new Date());
    if (!event) throw new Error('invalid payload');
    await emitEvent(event, {
      endpoint: options.endpoint,
      timeoutMs: options.timeoutMs ?? 250,
      fetchImpl: options.fetchImpl,
    });
  } catch {
    stderr.write('Threadforge Grok hook capture skipped; no policy decision was made\n');
  }
}

function contentDigest(value: unknown): { hash?: string; bytes: number; omitted: boolean } | undefined {
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

function digestMetadata(prefix: string, digest: { hash?: string; bytes: number; omitted: boolean }): JsonObject {
  return { retention: 'metadata', included: false, ...digestMetadataFields(prefix, digest) };
}

function digestMetadataFields(prefix: string, digest: { hash?: string; bytes: number; omitted: boolean }): JsonObject {
  return {
    ...(digest.hash ? { [`${prefix}_sha256`]: digest.hash } : {}),
    [`${prefix}_bytes`]: digest.bytes,
    ...(digest.omitted ? { [`${prefix}_omitted`]: true } : {}),
  };
}

function canonicalize(value: unknown, depth = 0, budget = { nodes: 0 }): unknown {
  budget.nodes++;
  if (depth > 8 || budget.nodes > 2_000) throw new Error('content structure exceeds digest bounds');
  if (Array.isArray(value)) return value.map((child) => canonicalize(child, depth + 1, budget));
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key], depth + 1, budget)]));
  return value;
}

function readRecord(value: unknown): JsonObject | undefined { return isRecord(value) ? value : undefined; }
function isCommandTool(name: string): boolean { return ['run_terminal_command', 'terminal', 'shell', 'bash'].includes(name.toLowerCase()); }
function isGrokHookAction(value: unknown): value is GrokHookAction {
  return typeof value === 'string' && (grokHookActions as readonly string[]).includes(value);
}
function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}
function safeLabel(value: unknown, max: number): string | undefined {
  const label = boundedString(value, max);
  return label && /^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(label) && !looksSecretLike(label) ? label : undefined;
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
async function readBoundedInput(input: AsyncIterable<Uint8Array>): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of input) {
    const bytes = Buffer.from(chunk);
    total += bytes.byteLength;
    if (total > maxInputBytes) throw new Error('oversized payload');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}
