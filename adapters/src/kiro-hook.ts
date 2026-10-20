import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const kiroManagedMarker = '--threadforge-hook=kiro';
export const kiroHookEvents = ['SessionStart', 'AgentSpawn', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop'] as const;
export type KiroHookEvent = typeof kiroHookEvents[number];

const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const actions: Record<KiroHookEvent, string> = {
  SessionStart: 'session.started',
  AgentSpawn: 'session.started',
  UserPromptSubmit: 'prompt.submitted',
  PreToolUse: 'tool.invoked',
  PostToolUse: 'tool.completed',
  Stop: 'assistant.turn_stopped',
};

type JsonObject = Record<string, unknown>;
interface Digest { hash: string; bytes: number; }

/** Maps Kiro v1 hook payloads to privacy-minimized local Threadforge events. */
export function mapKiroHookPayload(value: unknown, hookEvent: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const eventName = normalizeHookEvent(hookEvent);
  if (!eventName) return undefined;
  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: actions[eventName], fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'kiro', collection_method: 'hook' },
    hook: { name: eventName },
  };

  const sessionId = boundedString(value.session_id, 512);
  if (sessionId) event.session = { id: `kiro:${sha256(sessionId)}` };

  if (eventName === 'UserPromptSubmit') {
    const prompt = digest(value.prompt);
    if (prompt) event.content = contentMetadata({ prompt_sha256: prompt.hash, prompt_bytes: prompt.bytes });
    markPromptHandoff(event, value.prompt);
  }

  if (eventName === 'PreToolUse' || eventName === 'PostToolUse') {
    const toolName = boundedString(value.tool_name, 256);
    const toolInput = isRecord(value.tool_input) ? value.tool_input : {};
    const toolResponse = isRecord(value.tool_response) ? value.tool_response : {};
    if (toolName) {
      const safeName = safeLabel(toolName, 128);
      if (safeName) event.tool = { name: safeName };
      const callId = boundedString(value.tool_call_id, 512);
      if (callId) event.raw = { tool_call_id_sha256: sha256(callId) };
      const input = digest(value.tool_input);
      const output = eventName === 'PostToolUse' ? digest(value.tool_response) : undefined;
      if (input || output) event.content = contentMetadata({
        ...(input ? { input_sha256: input.hash, input_bytes: input.bytes } : {}),
        ...(output ? { output_sha256: output.hash, output_bytes: output.bytes } : {}),
      });

      if (eventName === 'PostToolUse') {
        if (toolResponse.success === false) {
          event.event = { ...event.event, action: 'tool.failed' };
          event.severity = 'medium';
        } else if (toolResponse.success === true) {
          const classification = classifyTool(toolName);
          if (classification.action) event.event = { ...event.event, action: classification.action };
          if (classification.mcp) event.mcp = classification.mcp;
          if (classification.operation) event.file = { operation: classification.operation };
        }
      }
    }
  }

  if (eventName === 'Stop') {
    const response = digest(value.assistant_response);
    if (response) event.content = contentMetadata({ response_sha256: response.hash, response_bytes: response.bytes });
  }
  return event;
}

function normalizeHookEvent(value: unknown): KiroHookEvent | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return (kiroHookEvents as readonly string[]).includes(normalized) ? normalized as KiroHookEvent : undefined;
}

function classifyTool(name: string): { action?: string; operation?: string; mcp?: JsonObject } {
  const normalized = name.trim().toLowerCase();
  if (['read', 'fs_read', 'fsread', 'read_file', 'read_files', 'list_directory', 'file_search', 'glob', 'grep_search', 'grep', 'code', 'read_code'].includes(normalized)) {
    return { action: 'file.read', operation: 'read' };
  }
  if (['write', 'fs_write', 'fswrite'].includes(normalized)) return { action: 'file.modified', operation: 'create' };
  if (['fs_append', 'str_replace'].includes(normalized)) return { action: 'file.modified', operation: 'modify' };
  if (normalized === 'delete_file') return { action: 'file.modified', operation: 'delete' };
  if (['shell', 'execute_bash', 'execute_cmd', 'control_bash_process'].includes(normalized)) return { action: 'command.executed' };
  const mcp = /^@([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)$/.exec(name.trim());
  if (mcp) return { action: 'mcp.tool_invoked', mcp: { server: mcp[1], tool: mcp[2] } };
  return {};
}

function digest(value: unknown): Digest | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return bytes > maxContentBytes ? undefined : { hash: sha256(serialized), bytes };
}

function contentMetadata(fields: JsonObject): JsonObject {
  return { retention: 'metadata', included: false, ...fields };
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const normalized = value.trim();
  return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}

function safeLabel(value: string, max: number): string | undefined {
  if (value.length > max || !/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(value) || looksSecretLike(value)) return undefined;
  return value;
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
    if (total > maxInputBytes) throw new Error('Kiro hook payload exceeds the local size limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

function hookEventFromArgs(args: string[]): KiroHookEvent | undefined {
  const value = args.find((arg) => arg.startsWith('--hook-event='))?.slice('--hook-event='.length);
  return normalizeHookEvent(value);
}

async function main(): Promise<void> {
  try {
    const input = await readBoundedStdin();
    const payload = JSON.parse(input.toString('utf8')) as unknown;
    const eventName = hookEventFromArgs(process.argv.slice(2));
    const event = mapKiroHookPayload(payload, eventName);
    if (event) await emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 });
  } catch {
    process.stderr.write('threadforge Kiro hook capture skipped: invalid payload or local intake unavailable\n');
  }
  // Kiro can append command-hook stdout to model context; an observational hook stays silent.
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  await main();
}
