import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';

const maxInputBytes = 1 << 20;
const maxHashedContentBytes = 64 << 10;
type JsonObject = Record<string, unknown>;

const actionByHook: Record<string, string> = {
  SessionStart: 'session.started',
  PostToolUse: 'tool.completed',
  PostToolUseFailure: 'tool.failed',
  SessionEnd: 'session.ended',
};

/** Maps Codex command-hook payloads to local, metadata-only Threadforge events. */
export function mapCodexHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const hookEvent = boundedString(value.hook_event_name, 64);
  const action = hookEvent ? actionByHook[hookEvent] : undefined;
  if (!hookEvent || !action) return undefined;

  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action, fidelity: 'observed' },
    severity: hookEvent === 'PostToolUseFailure' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'codex-cli', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const sessionID = firstString(value, ['session_id', 'thread_id', 'sessionId']);
  if (sessionID) event.session = { id: `codex:${sha256(sessionID)}` };

  if (hookEvent === 'SessionStart') {
    const model = safeLabel(value.model, 128);
    if (model) event.model = model;
  }

  if (hookEvent === 'PostToolUse' || hookEvent === 'PostToolUseFailure') {
    const tool = isRecord(value.tool) ? value.tool : {};
    const toolName = safeLabel(value.tool_name ?? value.toolName ?? tool.name, 128);
    if (toolName) event.tool = { name: toolName };
    const callID = firstString(value, ['tool_call_id', 'tool_use_id', 'call_id']);
    if (callID) event.raw = { tool_call_id_sha256: sha256(callID) };
    const input = value.tool_input ?? value.tool_args ?? tool.input;
    const digest = contentDigest(input);
    if (digest) event.content = { retention: 'metadata', included: false, ...digest };
  }

  return event;
}

function contentDigest(value: unknown): { hash?: string; bytes: number; omitted?: true } | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return bytes > maxHashedContentBytes
    ? { bytes, omitted: true }
    : { hash: sha256(serialized), bytes };
}

function firstString(value: JsonObject, keys: string[]): string | undefined {
  for (const key of keys) {
    const candidate = boundedString(value[key], 256);
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
  if (!normalized || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(normalized) || looksSecretLike(normalized)) return undefined;
  return normalized;
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
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
    if (total > maxInputBytes) throw new Error('Codex hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const event = mapCodexHookPayload(JSON.parse(input.toString('utf8')) as unknown);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
    process.stdout.write('{}\n');
  } catch {
    process.stderr.write('threadforge Codex hook capture skipped: invalid payload or local intake unavailable\n');
    process.stdout.write('{}\n');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
