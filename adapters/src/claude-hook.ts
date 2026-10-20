import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

const maxInputBytes = 1 << 20;
const maxHashedContentBytes = 64 << 10;

type JsonObject = Record<string, unknown>;
type ContentDigest = { sha256?: string; byte_count: number; omitted?: true };

const actionByHook: Record<string, string> = {
  SessionStart: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PostToolUse: 'tool.completed',
  PostToolUseFailure: 'tool.failed',
  Stop: 'assistant.turn_stopped',
  SubagentStart: 'subagent.started',
  SubagentStop: 'subagent.completed',
  PermissionRequest: 'permission.requested',
  SessionEnd: 'session.ended',
};

/** Maps Claude Code's command-hook JSON to metadata-only Threadforge telemetry. */
export function mapClaudeHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const hookEvent = boundedString(value.hook_event_name, 64);
  if (!hookEvent) return undefined;
  const action = actionByHook[hookEvent];
  if (!action) return undefined;

  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action, fidelity: 'observed' },
    severity: hookEvent === 'PostToolUseFailure' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'claude-code', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const sessionID = boundedString(value.session_id, 256);
  if (sessionID) event.session = { id: `claude:${sha256(sessionID)}` };

  if (hookEvent === 'UserPromptSubmit') {
    const digest = contentDigest(value.prompt);
    if (digest) event.content = contentMetadata(digest);
    markPromptHandoff(event, value.prompt);
  }

  if (hookEvent === 'PreToolUse' || hookEvent === 'PostToolUse' || hookEvent === 'PostToolUseFailure') {
    const tool: JsonObject = {};
    const toolName = safeLabel(value.tool_name, 128);
    const toolCallID = boundedString(value.tool_use_id, 256);
    if (toolName) tool.name = toolName;
    const digest = contentDigest(value.tool_input);
    if (digest) event.content = contentMetadata(digest);
    if (Object.keys(tool).length > 0) event.tool = tool;
    if (toolCallID) event.raw = { tool_call_id_sha256: sha256(toolCallID) };
  }

  if (hookEvent === 'SubagentStart' || hookEvent === 'SubagentStop') {
    const agent: JsonObject = {};
    const agentID = boundedString(value.agent_id, 256);
    const agentType = safeLabel(value.agent_type, 128);
    if (agentID) agent.agent_id_sha256 = sha256(agentID);
    if (agentType) agent.agent_type = agentType;
    if (Object.keys(agent).length > 0) event.raw = agent;
  }

  if (hookEvent === 'PermissionRequest') {
    const toolName = safeLabel(value.tool_name, 128);
    if (toolName) event.tool = { name: toolName };
    event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
  }

  return event;
}

function contentDigest(value: unknown): ContentDigest | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try {
    serialized = typeof value === 'string' ? value : JSON.stringify(canonicalizeContent(value));
  } catch {
    return undefined;
  }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  if (bytes > maxHashedContentBytes) return { byte_count: bytes, omitted: true };
  return { sha256: sha256(serialized), byte_count: bytes };
}

function sha256(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function contentMetadata(digest: ContentDigest): JsonObject {
  return {
    retention: 'metadata',
    included: false,
    ...(digest.sha256 ? { hash: digest.sha256 } : {}),
    bytes: digest.byte_count,
  };
}

async function readBoundedStdin(): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of process.stdin) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    total += bytes.byteLength;
    if (total > maxInputBytes) throw new Error('Claude hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const payload: unknown = JSON.parse(input.toString('utf8'));
    const event = mapClaudeHookPayload(payload);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT });
  } catch (error) {
    // Observability is fail-open: collector or parsing failures must not authorize or block agent actions.
    const reason = error instanceof Error && error.message === 'Claude hook payload exceeds the local size limit'
      ? 'payload exceeded the local size limit'
      : 'invalid payload or local intake unavailable';
    process.stderr.write(`threadforge Claude hook capture skipped: ${reason}\n`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
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

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function canonicalizeContent(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalizeContent);
  if (isRecord(value)) {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalizeContent(value[key])]));
  }
  return value;
}
