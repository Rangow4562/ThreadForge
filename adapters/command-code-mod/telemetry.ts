import { randomUUID } from 'node:crypto';

export const defaultCommandCodeEventEndpoint = 'http://127.0.0.1:4318/v1/events';

export const commandCodeObservedEventTypes = [
  'session_start',
  'session_shutdown',
  'run_start',
  'run_end',
  'turn_start',
  'turn_end',
  'model_request_start',
  'model_request_end',
  'tool_running',
  'tool_completed',
  'tool_errored',
  'subagent_start',
  'subagent_stop',
  'subagent_progress',
  'compaction_start',
  'compaction_done',
] as const;

type EventType = typeof commandCodeObservedEventTypes[number];
type EventBinding = { action: string; category: string; severity?: 'info' | 'medium' };
type Disposable = { dispose(): void };
export type CommandCodeModApi = { on(event: EventType, handler: (event: unknown) => void): Disposable };
type ThreadforgeEvent = {
  timestamp: string;
  vendor: 'beacon';
  product: 'endpoint-agent';
  schema_version: '1.0';
  event: { id: string; kind: 'agent_runtime'; action: string; category: string; fidelity: 'observed' };
  severity: 'info' | 'medium';
  endpoint: { os: string };
  harness: { name: 'command_code'; collection_method: 'plugin' };
  session: { id: string };
};

const maxInFlight = 64;
const minTimeoutMs = 100;
const maxTimeoutMs = 10_000;

const eventBindings: Record<EventType, EventBinding> = {
  session_start: { action: 'session.started', category: 'session' },
  session_shutdown: { action: 'session.ended', category: 'session' },
  run_start: { action: 'run.started', category: 'run' },
  run_end: { action: 'run.completed', category: 'run' },
  turn_start: { action: 'turn.started', category: 'turn' },
  turn_end: { action: 'turn.completed', category: 'turn' },
  model_request_start: { action: 'model.requested', category: 'model' },
  model_request_end: { action: 'model.completed', category: 'model' },
  tool_running: { action: 'tool.started', category: 'tool' },
  tool_completed: { action: 'tool.completed', category: 'tool' },
  tool_errored: { action: 'tool.failed', category: 'tool', severity: 'medium' },
  subagent_start: { action: 'subagent.started', category: 'subagent' },
  subagent_stop: { action: 'subagent.completed', category: 'subagent' },
  subagent_progress: { action: 'subagent.progressed', category: 'subagent' },
  compaction_start: { action: 'compaction.started', category: 'compaction' },
  compaction_done: { action: 'compaction.completed', category: 'compaction' },
};

export interface CommandCodeObserverOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

export interface CommandCodeObserver {
  observe(eventType: EventType, event: unknown): void;
  flush(): Promise<void>;
  dispose(): void;
  droppedEvents(): number;
}

export function createCommandCodeEventRecorder(
  options: CommandCodeObserverOptions = {},
): CommandCodeObserver {
  const endpoint = options.endpoint ?? defaultCommandCodeEventEndpoint;
  assertLoopbackEndpoint(endpoint);
  const timeoutMs = options.timeoutMs ?? 1500;
  if (!Number.isInteger(timeoutMs) || timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs) {
    throw new Error(`timeoutMs must be an integer between ${minTimeoutMs} and ${maxTimeoutMs}`);
  }
  const fetchImpl = options.fetchImpl ?? fetch;
  const pending = new Set<Promise<void>>();
  let sessionID = randomUUID();
  let stopped = false;
  let dropped = 0;

  const flush = async (): Promise<void> => {
    while (pending.size > 0) await Promise.all([...pending]);
  };

  const dispose = (): void => {
    stopped = true;
  };

  const enqueue = (eventType: EventType): void => {
    if (stopped || pending.size >= maxInFlight) {
      dropped += 1;
      return;
    }
    const event = makeEvent(eventType, sessionID);
    const task = Promise.resolve()
      .then(() => sendEvent(event, { endpoint, timeoutMs, fetchImpl }))
      .then(() => undefined, () => undefined);
    pending.add(task);
    void task.then(() => pending.delete(task));
  };

  const observe = (eventType: EventType, event: unknown): void => {
    try {
      if (stopped || readOwn(event, 'type') !== eventType) return;
      if (eventType === 'session_start') sessionID = randomUUID();
      enqueue(eventType);
      if (eventType === 'session_shutdown') void flush();
    } catch {
      // Observability is best-effort and must not alter Command Code behavior.
    }
  };

  return { observe, flush, dispose, droppedEvents: () => dropped };
}

/**
 * Observe only documented Command Code AgentEvent lifecycle types. Event payloads are deliberately
 * ignored: prompts, tool arguments/results, paths, model labels, IDs, and errors never leave the
 * host. Delivery is local-only, bounded, and fail-open.
 */
export function registerCommandCodeObserver(
  value: unknown,
  options: CommandCodeObserverOptions = {},
): CommandCodeObserver {
  if (typeof value !== 'object' || value === null || typeof (value as { on?: unknown }).on !== 'function') {
    throw new Error('Command Code ModApi must provide on(event, handler)');
  }
  const api = value as CommandCodeModApi;
  const recorder = createCommandCodeEventRecorder(options);
  const registrations: Disposable[] = [];

  try {
    for (const eventType of commandCodeObservedEventTypes) {
      const registration = api.on(eventType, (event) => recorder.observe(eventType, event));
      if (!registration || typeof registration.dispose !== 'function') {
        throw new Error('Command Code ModApi on() must return a disposable registration');
      }
      registrations.push(registration);
    }
  } catch {
    recorder.dispose();
    for (const registration of registrations.splice(0)) {
      try {
        registration.dispose();
      } catch {
        // Failed setup must not leave partial observer subscriptions.
      }
    }
    throw new Error('Could not register the Command Code telemetry observer');
  }

  return {
    observe: recorder.observe,
    flush: recorder.flush,
    droppedEvents: recorder.droppedEvents,
    dispose() {
      recorder.dispose();
      for (const registration of registrations.splice(0)) {
        try {
          registration.dispose();
        } catch {
          // Removing telemetry must not interfere with the host's mod lifecycle.
        }
      }
    },
  };
}

function makeEvent(eventType: EventType, sessionID: string): ThreadforgeEvent {
  const binding = eventBindings[eventType];
  return {
    timestamp: new Date().toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: {
      id: randomUUID(),
      kind: 'agent_runtime',
      action: binding.action,
      category: binding.category,
      fidelity: 'observed',
    },
    severity: binding.severity ?? 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'command_code', collection_method: 'plugin' },
    session: { id: sessionID },
  };
}

async function sendEvent(
  event: ThreadforgeEvent,
  options: Required<CommandCodeObserverOptions>,
): Promise<void> {
  const response = await options.fetchImpl(options.endpoint, {
    method: 'POST',
    headers: { 'content-type': 'application/json', accept: 'application/json' },
    body: JSON.stringify(event),
    signal: AbortSignal.timeout(options.timeoutMs),
    redirect: 'error',
  });
  if (response.status !== 202) throw new Error(`Threadforge local intake rejected event (${response.status})`);
}

function assertLoopbackEndpoint(value: string): void {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error('event endpoint must be a valid loopback HTTP URL');
  }
  const hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '');
  const isIPv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(hostname) &&
    hostname.split('.').every((segment) => Number(segment) <= 255);
  if (parsed.protocol !== 'http:' || parsed.username || parsed.password || parsed.search || parsed.hash ||
      !(hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1' || isIPv4Loopback) ||
      parsed.pathname !== '/v1/events') {
    throw new Error('event endpoint must be loopback HTTP at /v1/events; remote forwarding is not enabled');
  }
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
