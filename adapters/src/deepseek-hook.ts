import { mapClaudeHookPayload } from './claude-hook.ts';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';

export const deepseekHookEvents = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop', 'SubagentStart', 'SubagentStop',
] as const;
export const deepseekManagedMarker = '--threadforge-dsh-hook=v1';

const maxInputBytes = 1 << 20;

export interface DeepSeekHookOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
  now?: () => Date;
}

interface TextWriter {
  write(value: string): unknown;
}

/** Maps the seven supported DeepSeek Harness bridge events without retaining payload content. */
export function mapDeepSeekHookPayload(value: unknown, capturedAt = new Date()): ThreadforgeEvent | undefined {
  if (!isRecord(value)) return undefined;
  const eventName = value.hook_event_name;
  if (!isDeepSeekHookEvent(eventName)) return undefined;

  const event = mapClaudeHookPayload(value, capturedAt);
  if (!event) return undefined;
  const harness = event.harness;
  if (isRecord(harness)) harness.name = 'deepseek_harness';
  const session = event.session;
  if (isRecord(session) && typeof session.id === 'string' && session.id.startsWith('claude:')) {
    session.id = `dsh:${session.id.slice('claude:'.length)}`;
  }
  if (eventName === 'SubagentStop' && isRecord(event.event)) event.event.action = 'subagent.stopped';
  return event;
}

/**
 * Emits a digest-first event to the loopback receiver and always answers the Claude-shaped bridge
 * with an empty no-opinion object. Collector and parse failures cannot affect tools or permissions.
 */
export async function runDeepSeekHook(
  eventName: string,
  input: AsyncIterable<Uint8Array>,
  stdout: TextWriter,
  stderr: TextWriter,
  options: DeepSeekHookOptions = {},
): Promise<void> {
  try {
    if (!isDeepSeekHookEvent(eventName)) throw new Error('unsupported event');
    const bytes = await readBoundedInput(input);
    const payload: unknown = JSON.parse(bytes.toString('utf8'));
    if (!isRecord(payload) || payload.hook_event_name !== eventName) throw new Error('event mismatch');
    const event = mapDeepSeekHookPayload(payload, options.now?.() ?? new Date());
    if (event) {
      await emitEvent(event, {
        endpoint: options.endpoint,
        timeoutMs: options.timeoutMs ?? 250,
        fetchImpl: options.fetchImpl,
      });
    }
  } catch {
    stderr.write('threadforge DeepSeek Harness hook capture skipped; no policy decision was made\n');
  }
  stdout.write('{}\n');
}

async function readBoundedInput(input: AsyncIterable<Uint8Array>): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of input) {
    const bytes = Buffer.from(chunk);
    total += bytes.byteLength;
    if (total > maxInputBytes) throw new Error('oversized input');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks, total);
}

function isDeepSeekHookEvent(value: unknown): value is typeof deepseekHookEvents[number] {
  return typeof value === 'string' && (deepseekHookEvents as readonly string[]).includes(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
