export const defaultEventEndpoint = 'http://127.0.0.1:4318/v1/events';
const maxBatch = 100;
const maxRequestBytes = 8 << 20;
const maxPromptMarkerBytes = 1 << 20;
const handoffMarker = /\[beacon-handoff from=([a-z0-9_-]{1,64}) session=([A-Za-z0-9._:\-]{1,200})\]/;

type HandoffClaim = { sourceHarness: string; sourceSessionID: string };
const promptHandoffClaims = new WeakMap<ThreadforgeEvent, HandoffClaim>();

export interface ThreadforgeEvent {
  timestamp: string;
  vendor: 'beacon';
  product: 'endpoint-agent';
  schema_version: string;
  event: { id?: string; kind: string; action: string; category?: string; fidelity?: 'observed' | 'inferred' };
  severity: 'info' | 'low' | 'medium' | 'high' | 'critical';
  endpoint: { os: string };
  harness: { name: string; version?: string; collection_method?: 'hook' | 'otlp' | 'plugin' | 'poll' };
  gen_ai?: Record<string, unknown>;
  raw?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface EmitOptions {
  endpoint?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

export interface EmitReceipt {
  accepted: number;
  status: number;
}

/** Keeps only an explicit, bounded handoff claim outside the serialized prompt event. */
export function markPromptHandoff(event: ThreadforgeEvent, prompt: unknown): ThreadforgeEvent {
  if (event.event.action !== 'prompt.submitted' || typeof prompt !== 'string' ||
      new TextEncoder().encode(prompt).byteLength > maxPromptMarkerBytes) return event;
  const marker = handoffMarker.exec(prompt);
  if (marker) promptHandoffClaims.set(event, { sourceHarness: marker[1], sourceSessionID: marker[2] });
  return event;
}

export async function emitEvent(event: ThreadforgeEvent, options: EmitOptions = {}): Promise<EmitReceipt> {
  return emitEvents([event], options);
}

export async function emitEvents(events: ThreadforgeEvent[], options: EmitOptions = {}): Promise<EmitReceipt> {
  if (!Array.isArray(events) || events.length < 1 || events.length > maxBatch) {
    throw new Error(`event batch must contain 1 to ${maxBatch} events`);
  }
  const expandedEvents: ThreadforgeEvent[] = [];
  for (const event of events) {
    expandedEvents.push(event);
    const claim = promptHandoffClaims.get(event);
    const session = isRecord(event.session) ? event.session : undefined;
    if (!claim || event.event.action !== 'prompt.submitted' || !session || !nonEmpty(session.id, 256)) continue;
    expandedEvents.push({
      timestamp: event.timestamp,
      vendor: event.vendor,
      product: event.product,
      schema_version: event.schema_version,
      event: { kind: 'agent_runtime', action: 'session.handoff', category: 'session', fidelity: 'observed' },
      severity: 'info',
      endpoint: event.endpoint,
      harness: event.harness,
      session: { id: session.id },
      handoff: { source_harness: claim.sourceHarness, source_session_id: claim.sourceSessionID },
    });
  }
  if (expandedEvents.length > maxBatch * 2) throw new Error(`expanded event batch exceeds ${maxBatch * 2} events`);
  for (const [index, event] of expandedEvents.entries()) validateEvent(event, index);

  const endpoint = options.endpoint ?? defaultEventEndpoint;
  assertLoopbackEndpoint(endpoint);
  const timeoutMs = options.timeoutMs ?? 1500;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 10_000) {
    throw new Error('timeoutMs must be an integer between 100 and 10000');
  }
  const fetchImpl = options.fetchImpl ?? fetch;
  const body = JSON.stringify(expandedEvents.length === 1 ? expandedEvents[0] : expandedEvents);
  if (new TextEncoder().encode(body).byteLength > maxRequestBytes) {
    throw new Error(`event batch exceeds the ${maxRequestBytes}-byte local intake limit`);
  }
  const response = await fetchImpl(endpoint, {
    method: 'POST',
    headers: { 'content-type': 'application/json', accept: 'application/json' },
    body,
    signal: AbortSignal.timeout(timeoutMs),
    redirect: 'error',
  });
  if (response.status !== 202) {
    throw new Error(`Threadforge local intake rejected event batch (${response.status})`);
  }
  return { accepted: expandedEvents.length, status: response.status };
}

function validateEvent(value: unknown, index: number): asserts value is ThreadforgeEvent {
  if (!isRecord(value) || value.vendor !== 'beacon' || value.product !== 'endpoint-agent') {
    throw new Error(`events[${index}] must use the Beacon event contract`);
  }
  if (typeof value.timestamp !== 'string' || Number.isNaN(Date.parse(value.timestamp))) {
    throw new Error(`events[${index}].timestamp must be an ISO date-time`);
  }
  if (typeof value.schema_version !== 'string' || value.schema_version.length < 1 || value.schema_version.length > 32) {
    throw new Error(`events[${index}].schema_version is required`);
  }
  if (!isRecord(value.event) || !nonEmpty(value.event.kind, 128) || !nonEmpty(value.event.action, 256)) {
    throw new Error(`events[${index}] requires bounded event.kind and event.action`);
  }
  if (!['info', 'low', 'medium', 'high', 'critical'].includes(String(value.severity))) {
    throw new Error(`events[${index}].severity is invalid`);
  }
  if (!isRecord(value.endpoint) || !nonEmpty(value.endpoint.os, 64)) {
    throw new Error(`events[${index}].endpoint.os is required`);
  }
  if (!isRecord(value.harness) || !nonEmpty(value.harness.name, 128)) {
    throw new Error(`events[${index}].harness.name is required`);
  }
}

export function assertLoopbackEndpoint(value: string): void {
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
    throw new Error('event endpoint must be loopback HTTP at /v1/events; remote forwarding is not enabled by this adapter');
  }
}

function nonEmpty(value: unknown, max: number): value is string {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= max;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
