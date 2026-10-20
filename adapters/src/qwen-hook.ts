import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const qwenManagedMarker = '--threadforge-hook=qwen';
const maxInputBytes = 1 << 20;
const maxHashedContentBytes = 64 << 10;
const qwenSessionSources = new Set(['startup', 'resume', 'clear', 'compact']);
const actionByHook: Record<string, string> = {
  SessionStart: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PostToolUse: 'tool.completed',
  PostToolUseFailure: 'tool.failed',
  PermissionRequest: 'permission.requested',
  Stop: 'assistant.turn_stopped',
  SubagentStart: 'subagent.started',
  SubagentStop: 'subagent.completed',
  SessionEnd: 'session.ended',
};
const qwenFileReadTools = new Set(['read_file', 'read_many_files', 'list_directory', 'glob', 'grep_search', 'search_file_content']);
const qwenFileWriteTools = new Set(['write_file', 'edit', 'replace', 'notebook_edit']);

type JsonObject = Record<string, unknown>;

/** Maps Qwen Code command-hook JSON to metadata-only Threadforge events. */
export function mapQwenHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
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
    harness: { name: 'qwen_code', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const sessionID = boundedString(value.session_id, 512);
  if (sessionID) event.session = { id: `qwen:${sha256(sessionID)}` };

  if (hookEvent === 'UserPromptSubmit') {
    const digest = contentDigest(value.prompt);
    if (digest) event.content = contentMetadata(digest);
    markPromptHandoff(event, value.prompt);
  }

  if (hookEvent === 'PreToolUse' || hookEvent === 'PostToolUse' || hookEvent === 'PostToolUseFailure') {
    const toolName = safeLabel(value.tool_name, 128);
    const toolUseID = boundedString(value.tool_use_id ?? value.tool_call_id, 512);
    if (toolName) event.tool = { name: toolName };
    if (toolUseID) event.raw = { tool_call_id_sha256: sha256(toolUseID) };
    const digest = contentDigest(value.tool_input);
    if (digest) event.content = contentMetadata(digest);

    const failed = hookEvent === 'PostToolUseFailure' || hasFailure(value.error) || value.is_interrupt === true;
    if (failed && hookEvent !== 'PreToolUse') {
      event.event = { ...event.event, action: 'tool.failed' };
      event.severity = 'medium';
    } else if (hookEvent === 'PostToolUse' && toolName) {
      const completion = classifyCompletedTool(toolName);
      if (completion) event.event = { ...event.event, action: completion };
    }
    const permissionMode = safeLabel(value.permission_mode, 64);
    if (permissionMode) event.raw = { ...(isRecord(event.raw) ? event.raw : {}), permission_mode: permissionMode };
  }

  if (hookEvent === 'PermissionRequest') {
    const toolName = safeLabel(value.tool_name, 128);
    if (toolName) event.tool = { name: toolName };
    event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
  }

  if (hookEvent === 'SessionStart') {
    const source = boundedString(value.source, 64)?.toLowerCase();
    if (source && qwenSessionSources.has(source)) event.raw = { source };
  }

  if (hookEvent === 'SubagentStart' || hookEvent === 'SubagentStop') {
    const subagentID = boundedString(value.agent_id ?? value.agentId, 512);
    const subagentType = safeLabel(value.agent_type ?? value.agentType, 128);
    event.raw = {
      ...(subagentID ? { agent_id_sha256: sha256(subagentID) } : {}),
      ...(subagentType ? { agent_type: subagentType } : {}),
    };
    if (Object.keys(event.raw as JsonObject).length === 0) delete event.raw;
  }

  if (hookEvent === 'Stop') {
    const used = boundedCount(value.input_tokens);
    const limit = boundedCount(value.context_limit);
    if (used !== undefined || limit !== undefined) {
      event.context = {
        ...(used === undefined ? {} : { used_tokens: used }),
        ...(limit === undefined ? {} : { limit_tokens: limit }),
      };
    }
    const contextUsage = boundedRatio(value.context_usage);
    if (contextUsage !== undefined) event.context = { ...(isRecord(event.context) ? event.context : {}), utilization: contextUsage };
    if (typeof value.stop_hook_active === 'boolean') {
      event.raw = { ...(isRecord(event.raw) ? event.raw : {}), stop_hook_active: value.stop_hook_active };
    }
  }

  return event;
}

function classifyCompletedTool(name: string): string | undefined {
  const normalized = name.toLowerCase();
  if (normalized === 'run_shell_command') return 'command.executed';
  if (qwenFileReadTools.has(normalized)) return 'file.read';
  if (qwenFileWriteTools.has(normalized)) return 'file.modified';
  return undefined;
}

function hasFailure(value: unknown): boolean {
  if (value === undefined || value === null || value === false || value === '') return false;
  if (isRecord(value)) return Object.keys(value).length > 0;
  return true;
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
    retention: 'metadata', included: false,
    ...(digest.sha256 ? { hash: digest.sha256 } : {}),
    bytes: digest.bytes,
    ...(digest.omitted ? { omitted: true } : {}),
  };
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

function boundedCount(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 1_000_000_000_000 ? value : undefined;
}

function boundedRatio(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1 ? value : undefined;
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
    if (total > maxInputBytes) throw new Error('Qwen hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const event = mapQwenHookPayload(JSON.parse(input.toString('utf8')) as unknown);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
  } catch {
    process.stderr.write('threadforge Qwen hook capture skipped: invalid payload or local intake unavailable\n');
  }
  // Empty output carries no permissionDecision, so Qwen's regular authorization remains active.
  process.stdout.write('{}\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
