import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';

const maxInputBytes = 1 << 20;
const maxDigestBytes = 64 << 10;
type JsonObject = Record<string, unknown>;

const actionByHook: Record<string, string> = {
  sessionStart: 'session.started',
  postToolUse: 'tool.completed',
  postToolUseFailure: 'tool.failed',
  afterFileEdit: 'file.modified',
  afterAgentResponse: 'agent.message',
  afterAgentThought: 'agent.reasoning',
  preCompact: 'context.compaction',
  sessionEnd: 'session.ended',
};

/** Maps the installed non-blocking Cursor hook subset to metadata-only telemetry. */
export function mapCursorHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const hookName = boundedString(value.hook_event_name ?? value.hookEventName, 64);
  const action = hookName ? actionByHook[hookName] : undefined;
  if (!action) return undefined;

  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action, fidelity: 'observed' },
    severity: hookName === 'postToolUseFailure' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'cursor', collection_method: 'hook' },
  };

  const conversationID = boundedString(value.conversation_id ?? value.parent_conversation_id, 256);
  if (conversationID) event.session = { id: `cursor:${hash(conversationID)}` };

  const toolName = safeLabel(value.tool_name, 128);
  if (toolName) event.tool = { name: toolName };
  const model = safeLabel(value.model, 128);
  if (model) event.model = model;

  let content: unknown;
  if (hookName === 'postToolUse' || hookName === 'postToolUseFailure') {
    content = value.tool_input;
    const failureType = safeLabel(value.failure_type, 64);
    if (failureType) event.raw = { failure_type: failureType };
  } else if (hookName === 'afterFileEdit') {
    content = value.edits ?? value.diff;
  } else if (hookName === 'afterAgentResponse' || hookName === 'afterAgentThought') {
    content = value.text;
  } else if (hookName === 'preCompact') {
    const usedTokens = boundedInteger(value.context_tokens, 0, 1_000_000_000);
    const limitTokens = boundedInteger(value.context_window_size, 1, 1_000_000_000);
    if (usedTokens !== undefined && limitTokens !== undefined && usedTokens <= limitTokens) {
      event.gen_ai = { context: { used_tokens: usedTokens, limit_tokens: limitTokens } };
    }
  }
  const digest = contentDigest(content);
  if (digest) event.content = digest;
  return event;
}

async function readStdinBounded(): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of process.stdin) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    total += bytes.byteLength;
    if (total > maxInputBytes) throw new Error('Cursor hook input exceeded its local limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const payload: unknown = JSON.parse((await readStdinBounded()).toString('utf8'));
    const event = mapCursorHookPayload(payload);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT });
  } catch (error) {
    const reason = error instanceof Error && error.message === 'Cursor hook input exceeded its local limit'
      ? 'payload exceeded the local size limit'
      : 'invalid payload or local intake unavailable';
    process.stderr.write(`threadforge Cursor hook capture skipped: ${reason}\n`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) await main();

function contentDigest(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  if (bytes > maxDigestBytes) return undefined;
  return { retention: 'metadata', included: false, hash: hash(serialized), bytes };
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  return text && text.length <= max ? text : undefined;
}

function boundedInteger(value: unknown, min: number, max: number): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max ? value : undefined;
}

function hash(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
