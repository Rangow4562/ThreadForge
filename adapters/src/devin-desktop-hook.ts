import { createHash } from 'node:crypto';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

export const devinDesktopManagedMarker = '--threadforge-devin-desktop-hook=v1';
export const devinDesktopHookEvents = [
  'pre_user_prompt', 'post_write_code', 'post_run_command', 'post_mcp_tool_use', 'post_read_code',
] as const;
export type DevinDesktopHookEvent = typeof devinDesktopHookEvents[number];

const maxInputBytes = 1 << 20;
const maxContentBytes = 64 << 10;
const actionByEvent: Record<DevinDesktopHookEvent, string> = {
  pre_user_prompt: 'prompt.submitted',
  post_write_code: 'file.modified',
  post_run_command: 'command.executed',
  post_mcp_tool_use: 'mcp.tool_invoked',
  post_read_code: 'file.read',
};

type JsonObject = Record<string, unknown>;
type Digest = { hash?: string; bytes: number; omitted: boolean };

export interface DevinDesktopHookOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
  now?: () => Date;
}

export interface TextWriter { write(value: string): unknown }

/** Map a pinned Cascade/Windsurf event to digest-first, observation-only telemetry. */
export function mapDevinDesktopHookPayload(
  value: unknown,
  hookEvent: unknown,
  capturedAt = new Date(),
): ThreadforgeEvent | undefined {
  if (!isRecord(value) || !isDevinDesktopHookEvent(hookEvent)) return undefined;
  const suppliedEvent = value.agent_action_name ?? value.hook_event_name ?? value.hookEventName;
  if (suppliedEvent !== undefined && suppliedEvent !== hookEvent) return undefined;

  const toolInfo = asRecord(value.tool_info ?? value.toolInfo) ?? value;
  const event: ThreadforgeEvent = {
    timestamp: capturedAt.toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action: actionByEvent[hookEvent], fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'devin_desktop', collection_method: 'hook' },
    hook: { name: hookEvent },
  };

  const trajectory = boundedString(value.trajectory_id ?? value.session_id ?? value.sessionId ?? value.conversation_id, 512);
  if (trajectory) event.session = { id: `devin-desktop:${sha256(trajectory)}` };
  const workspace = boundedString(
    value.cwd ?? value.workspace_path ?? value.project_path ??
      toolInfo.cwd ?? toolInfo.workspace_path ?? toolInfo.project_path ?? toolInfo.directory ?? toolInfo.working_directory,
    4096,
  );
  if (workspace) event.project = { id: sha256(workspace) };

  const executionId = boundedString(value.execution_id ?? value.executionId, 512);
  if (executionId) event.raw = { execution_id_sha256: sha256(executionId) };
  const toolName = safeLabel(toolInfo.tool_name ?? toolInfo.toolName ?? toolInfo.name ?? toolInfo.command_name, 128) ??
    defaultToolName(hookEvent);
  event.tool = { name: toolName, action: hookEvent };

  if (hookEvent === 'pre_user_prompt') {
    const promptValue = toolInfo.user_prompt ?? toolInfo.userPrompt ?? toolInfo.prompt ?? toolInfo.text;
    const prompt = digestContent(promptValue);
    if (prompt) event.content = digestMetadata('prompt', prompt);
    markPromptHandoff(event, promptValue);
  } else if (hookEvent === 'post_run_command') {
    const command = digestContent(toolInfo.command ?? toolInfo.command_line ?? toolInfo.text ?? toolInfo.input);
    if (command) event.command = { ...digestFields('command', command), retention: 'metadata' };
    const exitCode = finiteInteger(toolInfo.exit_code ?? toolInfo.exitCode);
    if (exitCode !== undefined) event.command = { ...(asRecord(event.command) ?? {}), exit_code: exitCode };
  } else if (hookEvent === 'post_mcp_tool_use') {
    const server = safeLabel(toolInfo.server_name ?? toolInfo.serverName ?? toolInfo.mcp_server, 128);
    const name = safeLabel(toolInfo.tool_name ?? toolInfo.toolName ?? toolInfo.name, 128);
    event.mcp = {
      ...(server ? { server } : {}),
      ...(name ? { tool: name } : {}),
    };
    const input = digestContent(toolInfo.arguments ?? toolInfo.input ?? toolInfo.parameters);
    if (input) event.content = digestMetadata('input', input);
  } else if (hookEvent === 'post_read_code' || hookEvent === 'post_write_code') {
    const filePath = boundedString(toolInfo.file_path ?? toolInfo.filePath ?? toolInfo.path, 4096);
    const file: JsonObject = { retention: 'metadata', included: false };
    if (filePath) file.path_sha256 = sha256(filePath);
    if (hookEvent === 'post_write_code') {
      const edits = digestContent(toolInfo.edits ?? toolInfo.content ?? toolInfo.text);
      if (edits) Object.assign(file, digestFields('change', edits));
    } else {
      const contents = digestContent(toolInfo.content ?? toolInfo.text ?? toolInfo.output);
      if (contents) Object.assign(file, digestFields('content', contents));
    }
    event.file = file;
  }
  return event;
}

/** Run fail-open with empty stdout; capture never blocks or authorizes Cascade actions. */
export async function runDevinDesktopHook(
  hookEvent: string,
  input: AsyncIterable<Uint8Array>,
  stdout: TextWriter,
  stderr: TextWriter,
  options: DevinDesktopHookOptions = {},
): Promise<void> {
  void stdout;
  try {
    if (!isDevinDesktopHookEvent(hookEvent)) throw new Error('unsupported hook event');
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
    const event = mapDevinDesktopHookPayload(payload, hookEvent, options.now?.() ?? new Date());
    if (!event) throw new Error('event mismatch');
    await emitEvent(event, {
      endpoint: options.endpoint,
      timeoutMs: options.timeoutMs ?? 250,
      fetchImpl: options.fetchImpl,
    });
  } catch {
    stderr.write('Threadforge Devin Desktop hook capture skipped; no policy decision was made\n');
  }
}

function defaultToolName(event: DevinDesktopHookEvent): string {
  if (event === 'post_run_command') return 'run_command';
  if (event === 'post_mcp_tool_use') return 'mcp_tool';
  if (event === 'post_read_code') return 'read_code';
  if (event === 'post_write_code') return 'write_code';
  return 'user_prompt';
}

function isDevinDesktopHookEvent(value: unknown): value is DevinDesktopHookEvent {
  return typeof value === 'string' && (devinDesktopHookEvents as readonly string[]).includes(value);
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
  return /\b(?:(?:api|access|refresh|auth)[_ -]?)?(?:key|token|password|secret|credential|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function finiteInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : undefined;
}

function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function asRecord(value: unknown): JsonObject | undefined { return isRecord(value) ? value : undefined; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
