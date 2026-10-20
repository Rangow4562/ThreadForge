import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';

export const antigravityManagedMarker = '--threadforge-hook=antigravity';
const maxInputBytes = 1 << 20;
const maxHashedContentBytes = 64 << 10;
const eventActions: Record<string, string> = {
  PreInvocation: 'agent.invocation_started',
  PostToolUse: 'tool.completed',
  PostInvocation: 'agent.invocation_completed',
  Stop: 'assistant.turn_stopped',
};

type JsonObject = Record<string, unknown>;

/** Maps Antigravity command-hook input to privacy-minimized, observational events. */
export function mapAntigravityHookPayload(
  value: unknown,
  hookEvent: string,
  capturedAt = new Date(),
): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const action = eventActions[hookEvent];
  if (!action) return undefined;

  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action, fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'antigravity_cli', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const conversation = firstString(value, ['conversationId', 'conversation_id', 'session_id']);
  if (conversation) event.session = { id: `antigravity:${sha256(conversation)}` };

  if (hookEvent === 'PostToolUse') {
    const call = isRecord(value.toolCall) ? value.toolCall : {};
    const toolName = safeLabel(call.name ?? value.tool_name ?? value.toolName, 128);
    if (toolName) event.tool = { name: toolName };
    const digest = contentDigest(call.args ?? value.tool_input);
    if (digest) event.content = contentMetadata(digest);
    const callID = firstString(value, ['toolCallId', 'tool_call_id', 'callId']);
    if (callID) event.raw = { tool_call_id_sha256: sha256(callID) };

    if (hookEvent === 'PostToolUse' && value.error !== undefined && value.error !== null && value.error !== '') {
      event.event = { ...event.event, action: 'tool.failed' };
      event.severity = 'medium';
    } else if (hookEvent === 'PostToolUse' && toolName) {
      const completionAction = classifyCompletedTool(toolName);
      if (completionAction) event.event = { ...event.event, action: completionAction };
    }
  }

  return event;
}

function classifyCompletedTool(name: string): string | undefined {
  switch (name.toLowerCase()) {
    case 'run_command': return 'command.executed';
    case 'view_file':
    case 'list_dir':
    case 'grep_search':
    case 'find_by_name': return 'file.read';
    case 'edit_file':
    case 'write_file':
    case 'apply_patch': return 'file.modified';
    default: return undefined;
  }
}

function contentDigest(value: unknown): { sha256?: string; bytes: number; omitted?: true } | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return bytes > maxHashedContentBytes ? { bytes, omitted: true } : { sha256: sha256(serialized), bytes };
}

function contentMetadata(digest: { sha256?: string; bytes: number; omitted?: true }): JsonObject {
  return {
    retention: 'metadata',
    included: false,
    ...(digest.sha256 ? { hash: digest.sha256 } : {}),
    bytes: digest.bytes,
    ...(digest.omitted ? { omitted: true } : {}),
  };
}

function firstString(value: JsonObject, keys: string[]): string | undefined {
  for (const key of keys) {
    const candidate = boundedString(value[key], 512);
    if (candidate) return candidate;
  }
  return undefined;
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
    if (total > maxInputBytes) throw new Error('Antigravity hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  const hookEvent = process.argv.slice(2).find((argument) => argument.startsWith('--hook-event='))?.slice('--hook-event='.length);
  try {
    if (!hookEvent || !eventActions[hookEvent]) throw new Error('unsupported hook event');
    const input = await readBoundedStdin();
    const event = mapAntigravityHookPayload(JSON.parse(input.toString('utf8')) as unknown, hookEvent);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
  } catch {
    // Capture is observational: failures never write a host decision or block the harness.
    process.stderr.write('threadforge Antigravity hook capture skipped: invalid payload or local intake unavailable\n');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
