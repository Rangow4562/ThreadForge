import { createHash } from 'node:crypto';
import { emitEvents, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

const maxInputBytes = 1 << 20;
const maxDigestBytes = 64 << 10;
const maxHookEvents = 100;
const supportedEvents = new Set(['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end']);
type JsonObject = Record<string, unknown>;

/** Convert one documented OpenHands hook payload into privacy-minimized observed events. */
export function mapOpenHandsHookPayload(
  eventType: string, value: unknown, capturedAt = new Date(), workingDirectory = process.env.OPENHANDS_PROJECT_DIR,
): ThreadforgeEvent[] {
  if (!supportedEvents.has(eventType) || !isRecord(value)) return [];
  const timestamp = capturedAt.toISOString();
  const sessionId = boundedString(value.session_id, 512);
  const cwd = boundedString(value.working_dir ?? value.workingDir ?? workingDirectory, 4_096);
  const base = (action: string, category: string, suffix: string, message: string, severity: ThreadforgeEvent['severity'] = 'info'): ThreadforgeEvent => ({
    timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${eventType}:${sessionId ?? ''}:${suffix}:${timestamp}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'openhands', collection_method: 'hook' },
    hook: { name: eventType },
    ...(sessionId ? { session: { id: `openhands:${sha256(sessionId)}` } } : {}),
    raw: { source: 'openhands_hook', event_type: eventType, ...(cwd ? { working_directory_sha256: sha256(cwd) } : {}), message },
  });

  if (eventType === 'session_start') return [base('session.started', 'session', 'start', 'OpenHands session started')];
  if (eventType === 'session_end') return [base('session.ended', 'session', 'end', 'OpenHands session ended')];
  if (eventType === 'user_prompt_submit') {
    const event = base('prompt.submitted', 'prompt', 'prompt', 'OpenHands prompt submitted');
    const digest = contentDigest(value.message);
    if (digest) event.content = digest;
    markPromptHandoff(event, value.message);
    return [event];
  }
  if (eventType === 'stop') return [base('assistant.turn_stopped', 'agent', 'stop', 'OpenHands agent turn stopped')];

  const toolName = safeLabel(value.tool_name, 128) ?? 'unknown';
  const input = isRecord(value.tool_input) ? value.tool_input : {};
  const response = isRecord(value.tool_response) ? value.tool_response : {};
  const toolDigest = contentDigest(value.tool_input);
  if (eventType === 'pre_tool_use') {
    const event = base('tool.invoked', 'tool', `pre:${toolName}`, 'OpenHands tool invocation observed');
    event.tool = { name: toolName };
    if (toolDigest) event.content = toolDigest;
    return [event];
  }

  const failed = response.is_error === true || (typeof response.is_error === 'string' && response.is_error.toLowerCase() === 'true');
  const outputText = observationText(response);
  const command = toolName.toLowerCase() === 'terminal' ? boundedString(input.command, 1 << 20) : undefined;
  const eventFor = (action: string, category: string, suffix: string, message: string): ThreadforgeEvent => {
    const event = base(action, category, `post:${suffix}`, message, failed ? 'high' : 'info');
    event.tool = { name: toolName };
    event.raw = { ...event.raw, openhands: { ...(typeof response.kind === 'string' ? { observation_kind: safeLabel(response.kind, 96) ?? 'unknown' } : {}) } };
    if (failed) event.error = { type: 'tool_error' };
    if (toolDigest) event.content = toolDigest;
    return event;
  };

  if (response.kind === 'MCPToolObservation') {
    const event = eventFor(failed ? 'tool.failed' : 'mcp.tool_invoked', failed ? 'tool' : 'mcp', 'mcp', 'OpenHands MCP tool result observed');
    event.mcp = { tool: toolName };
    return [event];
  }
  if (command) {
    const event = eventFor(failed ? 'tool.failed' : 'command.executed', failed ? 'tool' : 'command', 'command', 'OpenHands terminal command observed');
    event.command = {
      command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8'),
      ...(isInteger(response.exit_code) ? { exit_code: response.exit_code } : {}),
      ...(outputText ? { output_sha256: sha256(outputText), output_bytes: Buffer.byteLength(outputText, 'utf8') } : {}),
    };
    if (outputText) event.content = contentDigest(outputText);
    return [event];
  }

  const lower = toolName.toLowerCase();
  const editorCommand = safeLabel(input.command, 64)?.toLowerCase();
  const editorOperation = lower === 'file_editor' || lower === 'planning_file_editor'
    ? editorCommand === 'view' ? 'read' : ['create', 'str_replace', 'insert', 'undo_edit'].includes(editorCommand ?? '') ? 'modify' : undefined
    : undefined;
  const readTool = ['read_file', 'list_directory', 'glob', 'grep'].includes(lower);
  const editTool = ['apply_patch', 'write_file', 'edit'].includes(lower);
  if (!failed && (editorOperation || readTool || editTool)) {
    const filePath = boundedString(input.path ?? input.file_path ?? response.path ?? response.file_path, 4_096);
    const operation = editorOperation ?? (readTool ? 'read' : lower === 'write_file' ? 'create' : 'modify');
    if (lower === 'apply_patch') {
      const changes = isRecord(response.commit) && isRecord((response.commit as JsonObject).changes)
        ? (response.commit as JsonObject).changes as JsonObject : {};
      const entries = Object.entries(changes);
      if (entries.length > 0) {
        if (entries.length > maxHookEvents) {
          const event = eventFor('tool.completed', 'tool', 'patch-overflow', 'OpenHands patch exceeded per-hook file event limit');
          event.raw = { ...event.raw, openhands: { mapping_truncated: true, changed_file_count: entries.length } };
          return [event];
        }
        return entries.map(([sourcePath, change], index) => {
          const record = isRecord(change) ? change : {};
          const target = boundedString(record.move_path, 4_096) ?? sourcePath;
          return makeFileEvent(eventFor, target, index, 'modify', record);
        });
      }
    }
    return [makeFileEvent(eventFor, filePath, 0, operation, response)];
  }

  const event = eventFor(failed ? 'tool.failed' : 'tool.completed', 'tool', 'result', 'OpenHands tool result observed');
  if (outputText) event.content = contentDigest(outputText);
  return [event];
}

function makeFileEvent(
  eventFor: (action: string, category: string, suffix: string, message: string) => ThreadforgeEvent,
  filePath: string | undefined, index: number, operation: string, details: JsonObject,
): ThreadforgeEvent {
  const event = eventFor(operation === 'read' ? 'file.read' : 'file.modified', 'file', `file:${index}`, 'OpenHands file activity observed');
  const pathHash = filePath ? sha256(filePath) : undefined;
  event.file = { ...(pathHash ? { path_sha256: pathHash } : {}), operation };
  const oldContent = boundedString(details.old_content, maxInputBytes);
  const newContent = boundedString(details.new_content, maxInputBytes);
  if (oldContent !== undefined || newContent !== undefined) {
    const before = oldContent ?? '';
    const after = newContent ?? '';
    event.file = { ...(pathHash ? { path_sha256: pathHash } : {}), operation,
      before_sha256: sha256(before), after_sha256: sha256(after),
      before_bytes: Buffer.byteLength(before, 'utf8'), after_bytes: Buffer.byteLength(after, 'utf8') };
    event.content = contentDigest({ before, after });
  }
  return event;
}

/** Read, map, and send a host hook event. Errors are swallowed so telemetry cannot affect the run. */
export async function runOpenHandsHook(eventType: string, input: AsyncIterable<Uint8Array>, endpoint?: string): Promise<void> {
  try {
    const chunks: Buffer[] = [];
    let total = 0;
    for await (const chunk of input) {
      const bytes = Buffer.from(chunk);
      total += bytes.byteLength;
      if (total > maxInputBytes) return;
      chunks.push(bytes);
    }
    const payload: unknown = JSON.parse(Buffer.concat(chunks, total).toString('utf8'));
    const events = mapOpenHandsHookPayload(eventType, payload);
    if (events.length) await emitEvents(events, { endpoint });
  } catch {
    // OpenHands telemetry is observational and fail-open. Never emit a decision response.
  }
}

function observationText(response: JsonObject): string {
  if (!Array.isArray(response.content)) return '';
  return response.content.flatMap((part) => isRecord(part) && (!part.type || part.type === 'text') && typeof part.text === 'string' ? [part.text] : []).join('\n');
}

function contentDigest(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)) ?? ''; } catch { return undefined; }
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return { retention: 'metadata', included: false, ...(bytes <= maxDigestBytes ? { hash: sha256(serialized) } : { omitted: true }), bytes };
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined;
}
function safeLabel(value: unknown, maximum: number): string | undefined {
  const text = typeof value === 'string' ? value.trim() : '';
  if (!text || text.length > maximum || !/^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text) || /^(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[_-]/i.test(text) ||
      /^[A-Za-z0-9_-]{40,}$/.test(text) || /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(text)) return undefined;
  return text;
}
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isInteger(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value); }
function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function normalizedOS(platform: string): string { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
