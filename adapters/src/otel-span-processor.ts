import { randomUUID } from 'node:crypto';
import {
  assertLoopbackEndpoint,
  defaultEventEndpoint,
  emitEvent,
  type ThreadforgeEvent,
} from './emit.ts';

/** Options for a metadata-only processor attached to an application-owned OTel provider. */
export interface ThreadforgeSpanProcessorOptions {
  endpoint?: string;
  timeoutMs?: number;
}

/** Structural subset of the OpenTelemetry SpanProcessor lifecycle. */
export interface ThreadforgeSpanProcessor {
  onStart(span: unknown, parentContext: unknown): void;
  onEnd(span: unknown): void;
  forceFlush(): Promise<void>;
  shutdown(): Promise<void>;
}

const maxInFlight = 64;
const maxDurationMs = 2_147_483_647;
const modelPattern = /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$/;
const providerPattern = /^[A-Za-z][A-Za-z0-9_.-]{0,63}$/;
const credentialLikePattern = /\b(?:(?:api|access|refresh|auth)[_ -]?)?(?:key|token|password|secret|credential|authorization)\b\s*[:=]\s*\S+/i;
const bearerPattern = /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i;
const tokenPattern = /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/;
const safeOperations = new Set([
  'chat', 'completion', 'text_completion', 'embeddings', 'create_agent',
  'invoke_agent', 'execute_tool', 'generate_content', 'rerank', 'transcription',
  'speech', 'image_generation', 'unknown',
]);

/**
 * Observe completed GenAI spans and project only bounded metadata to Threadforge's
 * loopback event intake. Span names, IDs, arbitrary attributes, events, links,
 * exception descriptions, prompts, and outputs are never copied.
 */
export function createThreadforgeSpanProcessor(
  options: ThreadforgeSpanProcessorOptions = {},
): ThreadforgeSpanProcessor {
  const endpoint = options.endpoint ?? defaultEventEndpoint;
  assertLoopbackEndpoint(endpoint);
  const timeoutMs = options.timeoutMs ?? 1500;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 10_000) {
    throw new Error('timeoutMs must be an integer between 100 and 10000');
  }

  const pending = new Set<Promise<void>>();
  let stopped = false;
  let shutdownPromise: Promise<void> | undefined;

  async function forceFlush(): Promise<void> {
    while (pending.size > 0) await Promise.all([...pending]);
  }

  function shutdown(): Promise<void> {
    if (!shutdownPromise) {
      stopped = true;
      shutdownPromise = forceFlush();
    }
    return shutdownPromise;
  }

  return {
    onStart() {
      // No span state is retained between callbacks.
    },
    onEnd(span: unknown) {
      if (stopped || pending.size >= maxInFlight) return;
      let event: ThreadforgeEvent | undefined;
      try {
        event = projectSpan(span);
      } catch {
        return;
      }
      if (!event) return;

      const task = emitEvent(event, { endpoint, timeoutMs })
        .then(() => undefined, () => undefined);
      pending.add(task);
      void task.then(() => pending.delete(task));
    },
    forceFlush,
    shutdown,
  };
}

function projectSpan(span: unknown): ThreadforgeEvent | undefined {
  const attributes = readOwn(span, 'attributes');
  if (!isRecord(attributes)) return undefined;

  const operationValue = readOwn(attributes, 'gen_ai.operation.name');
  if (typeof operationValue !== 'string' || !safeOperations.has(operationValue)) return undefined;

  const providerValue = readOwn(attributes, 'gen_ai.provider.name') ?? readOwn(attributes, 'gen_ai.system');
  const provider = typeof providerValue === 'string' && providerPattern.test(providerValue)
    ? (looksCredentialLike(providerValue) ? 'unknown' : providerValue)
    : 'unknown';
  const requestModel = safeModel(readOwn(attributes, 'gen_ai.request.model'));
  const responseModel = safeModel(readOwn(attributes, 'gen_ai.response.model'));
  const inputTokens = safeTokenCount(
    readOwn(attributes, 'gen_ai.usage.input_tokens') ?? readOwn(attributes, 'gen_ai.usage.prompt_tokens'),
  );
  const outputTokens = safeTokenCount(
    readOwn(attributes, 'gen_ai.usage.output_tokens') ?? readOwn(attributes, 'gen_ai.usage.completion_tokens'),
  );

  const status = readOwn(span, 'status');
  const failed = isRecord(status) && readOwn(status, 'code') === 2;
  const outcome = failed ? 'failed' : 'completed';
  const duration = durationMilliseconds(readOwn(span, 'duration'));
  const endTime = timestampFromHrTime(readOwn(span, 'endTime'));
  const genAI: Record<string, unknown> = {
    system: provider,
    operation: operationValue,
    status: outcome,
    content: { included: false, retention: 'metadata' },
    ...(duration !== undefined ? { duration_ms: duration } : {}),
    ...(requestModel ? { request_model: requestModel } : {}),
    ...(responseModel ? { response_model: responseModel } : {}),
    ...(inputTokens !== undefined || outputTokens !== undefined ? {
      usage: {
        ...(inputTokens !== undefined ? { input_tokens: inputTokens } : {}),
        ...(outputTokens !== undefined ? { output_tokens: outputTokens } : {}),
      },
    } : {}),
  };

  return {
    timestamp: endTime ?? new Date().toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: {
      id: randomUUID(),
      kind: 'gen_ai',
      action: `model.call.${outcome}`,
      category: 'llm',
      fidelity: 'observed',
    },
    severity: failed ? 'medium' : 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'opentelemetry_sdk', collection_method: 'plugin' },
    gen_ai: genAI,
  };
}

function durationMilliseconds(value: unknown): number | undefined {
  if (!isHrTime(value)) return undefined;
  const milliseconds = value[0] * 1000 + value[1] / 1_000_000;
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return undefined;
  return Math.min(maxDurationMs, Math.round(milliseconds));
}

function timestampFromHrTime(value: unknown): string | undefined {
  if (!isHrTime(value)) return undefined;
  const milliseconds = value[0] * 1000 + value[1] / 1_000_000;
  if (!Number.isFinite(milliseconds) || milliseconds < 0 || milliseconds > 8.64e15) return undefined;
  try {
    return new Date(milliseconds).toISOString();
  } catch {
    return undefined;
  }
}

function isHrTime(value: unknown): value is [number, number] {
  return Array.isArray(value) && value.length === 2 &&
    Number.isSafeInteger(value[0]) && value[0] >= 0 &&
    Number.isSafeInteger(value[1]) && value[1] >= 0 && value[1] < 1_000_000_000;
}

function safeModel(value: unknown): string | undefined {
  return typeof value === 'string' && modelPattern.test(value) && !looksCredentialLike(value) ? value : undefined;
}

function looksCredentialLike(value: string): boolean {
  return credentialLikePattern.test(value) || bearerPattern.test(value) || tokenPattern.test(value);
}

function safeTokenCount(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= maxDurationMs
    ? value
    : undefined;
}

function readOwn(value: unknown, key: PropertyKey): unknown {
  if (!isRecord(value)) return undefined;
  try {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    return descriptor && 'value' in descriptor ? descriptor.value : undefined;
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<PropertyKey, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
