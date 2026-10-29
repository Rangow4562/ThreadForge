import { createHash, randomUUID } from 'node:crypto';
import {
  assertLoopbackEndpoint,
  defaultEventEndpoint,
  emitEvent,
  type EmitOptions,
  type ThreadforgeEvent,
} from './emit.ts';

export type CopilotSDKTelemetryHandler = (...args: unknown[]) => unknown;

/** Structural callback surface; no Copilot SDK runtime dependency is required. */
export interface CopilotSDKTelemetryHooks {
  onSessionStart?: CopilotSDKTelemetryHandler;
  onSessionEnd?: CopilotSDKTelemetryHandler;
  onPreToolUse?: CopilotSDKTelemetryHandler;
  onPostToolUse?: CopilotSDKTelemetryHandler;
  onPostToolUseFailure?: CopilotSDKTelemetryHandler;
  [key: string]: unknown;
}

export interface CopilotSDKTelemetryOptions extends EmitOptions {}

export interface CopilotSDKTelemetryInstrumentation<T extends object> {
  /** A shallow copy with observational wrappers; the caller's hook object is not mutated. */
  hooks: T & CopilotSDKTelemetryHooks;
  /** Wait for currently accepted local intake requests to settle. */
  flush(): Promise<void>;
  /** Stop accepting new telemetry without changing SDK hook behavior. */
  dispose(): void;
  /** Number of events dropped because the queue was full or disposed. */
  droppedEvents(): number;
}

const maxInFlight = 64;
const maxTimeoutMs = 10_000;
const minTimeoutMs = 100;

const observedHooks = [
  ['onSessionStart', 'session.started', 'session'],
  ['onSessionEnd', 'session.ended', 'session'],
  ['onPreToolUse', 'tool.requested', 'tool'],
  ['onPostToolUse', 'tool.completed', 'tool'],
  ['onPostToolUseFailure', 'tool.failed', 'tool'],
] as const;

/**
 * Wrap documented Copilot SDK lifecycle/tool callbacks with bounded, local-only metadata capture.
 * Existing handlers are invoked with their original hook object as `this`; their synchronous
 * values, Promise objects, and thrown exceptions are returned unchanged.
 */
export function addThreadforgeCopilotSDKTelemetry<T extends object>(
  hooks: T | undefined,
  options: CopilotSDKTelemetryOptions = {},
): CopilotSDKTelemetryInstrumentation<T> {
  const endpoint = options.endpoint ?? defaultEventEndpoint;
  assertLoopbackEndpoint(endpoint);
  const timeoutMs = options.timeoutMs ?? 1500;
  if (!Number.isInteger(timeoutMs) || timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs) {
    throw new Error(`timeoutMs must be an integer between ${minTimeoutMs} and ${maxTimeoutMs}`);
  }

  const originalHooks = hooks ?? {} as T;
  const copied = { ...originalHooks } as T & CopilotSDKTelemetryHooks;
  const pending = new Set<Promise<void>>();
  let stopped = false;
  let dropped = 0;

  const flush = async (): Promise<void> => {
    while (pending.size > 0) await Promise.all([...pending]);
  };

  const dispose = (): void => {
    stopped = true;
  };

  const enqueue = (action: string, category: string, input?: unknown): void => {
    if (stopped || pending.size >= maxInFlight) {
      dropped += 1;
      return;
    }
    const event = makeEvent(action, category, input);
    const task = Promise.resolve()
      .then(() => emitEvent(event, { ...options, endpoint, timeoutMs }))
      .then(() => undefined, () => undefined);
    pending.add(task);
    void task.then(() => pending.delete(task));
  };

  for (const [name, action, category] of observedHooks) {
    const previous = copied[name];
    if (previous !== undefined && typeof previous !== 'function') continue;
    copied[name] = function (this: unknown, ...args: unknown[]): unknown {
      enqueue(action, category, category === 'tool' ? args[0] : undefined);
      if (typeof previous !== 'function') return undefined;
      return Reflect.apply(previous, originalHooks, args);
    };
  }

  return { hooks: copied, flush, dispose, droppedEvents: () => dropped };
}

function makeEvent(action: string, category: string, input?: unknown): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: new Date().toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { id: randomUUID(), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity: action === 'tool.failed' ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'copilot_sdk', collection_method: 'plugin' },
  };
  if (category === 'tool') {
    const digest = toolNameDigest(readOwn(input, 'toolName'));
    if (digest) event.tool = { name_sha256: digest };
    if (action === 'tool.requested') event.raw = { phase: 'pre_tool_use' };
  }
  return event;
}

function toolNameDigest(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length < 1 || value.length > 128 ||
      value.trim().length === 0 || /[\u0000-\u001f\u007f]/.test(value)) return undefined;
  return createHash('sha256').update('threadforge:copilot-sdk:tool-name:v1\0').update(value, 'utf8').digest('hex');
}

function readOwn(value: unknown, key: string): unknown {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined;
  try {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    return descriptor && 'value' in descriptor ? descriptor.value : undefined;
  } catch {
    return undefined;
  }
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
