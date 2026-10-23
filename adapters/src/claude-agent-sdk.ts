import { randomUUID } from 'node:crypto';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';
import type { GenAISdkOptions } from './genai-sdk.ts';

export interface ClaudeAgentQueryInstrumentation<T extends object> {
  /** The query with its async-iterator and control methods preserved. */
  query: T;
  /** Stop emitting telemetry without interrupting or changing the query. */
  dispose(): void;
}

type Outcome = 'completed' | 'failed' | 'interrupted';
type IteratorResultLike = { done?: unknown; value?: unknown };
const iteratorMethods = new Set<PropertyKey>(['next', 'return', 'throw']);

/**
 * Observe one Claude Agent SDK query without importing or depending on its package.
 * Only yielded `type: "result"` / `is_error` markers are inspected. Message text,
 * session identifiers, model usage snapshots, and control payloads are untouched.
 */
export function instrumentClaudeAgentQuery<T extends object>(
  query: T,
  options: GenAISdkOptions = {},
): ClaudeAgentQueryInstrumentation<T> {
  if (!isObject(query)) throw new Error('Claude Agent SDK query must be an object');
  const asyncIterator = safeGet(query, Symbol.asyncIterator);
  if (typeof asyncIterator !== 'function' || typeof safeGet(query, 'next') !== 'function') {
    throw new Error('Claude Agent SDK query must be an async generator');
  }

  for (const key of [Symbol.asyncIterator, ...iteratorMethods]) {
    const descriptor = Object.getOwnPropertyDescriptor(query, key);
    if (descriptor && descriptor.configurable === false &&
        'value' in descriptor && descriptor.writable === false && typeof descriptor.value === 'function') {
      throw new Error(`Claude Agent SDK query has a locked ${String(key)} method`);
    }
  }

  const observation = new AgentQueryObservation(options);
  const iteratorCache = new WeakMap<object, object>();
  const instrumented = new Proxy(query, {
    get(target, property) {
      const method = safeGet(target, property);
      if (property === Symbol.asyncIterator && typeof method === 'function') {
        return () => wrapIterator(Reflect.apply(method, target, []), observation, iteratorCache);
      }
      if (iteratorMethods.has(property) && typeof method === 'function') {
        return (...args: unknown[]) => invokeIteratorMethod(target, method, property, args, observation);
      }
      return typeof method === 'function' ? method.bind(target) : method;
    },
  });

  let disposed = false;
  return {
    query: instrumented,
    dispose() {
      if (disposed) return;
      disposed = true;
      observation.disable();
    },
  };
}

function wrapIterator<T extends object>(
  iterator: T,
  observation: AgentQueryObservation,
  cache: WeakMap<object, object>,
): T {
  if (!isObject(iterator)) throw new Error('Claude Agent SDK returned an invalid async iterator');
  const existing = cache.get(iterator);
  if (existing) return existing as T;
  const wrapped = new Proxy(iterator, {
    get(target, property) {
      const method = safeGet(target, property);
      if (iteratorMethods.has(property) && typeof method === 'function') {
        return (...args: unknown[]) => invokeIteratorMethod(target, method, property, args, observation);
      }
      return typeof method === 'function' ? method.bind(target) : method;
    },
  });
  cache.set(iterator, wrapped);
  return wrapped;
}

function invokeIteratorMethod(
  target: object,
  method: Function,
  name: PropertyKey,
  args: unknown[],
  observation: AgentQueryObservation,
): Promise<unknown> {
  let result: unknown;
  try {
    result = Reflect.apply(method, target, args);
  } catch (error) {
    observation.finish('failed');
    throw error;
  }
  return Promise.resolve(result).then(
    (value) => {
      if (name === 'return') observation.finish('interrupted');
      else observation.observeIteratorResult(value);
      return value;
    },
    (error: unknown) => {
      observation.finish('failed');
      throw error;
    },
  );
}

class AgentQueryObservation {
  private readonly started = performance.now();
  private readonly options: GenAISdkOptions;
  private resultCount = 0;
  private lastResultWasError = false;
  private finished = false;
  private enabled = true;

  constructor(options: GenAISdkOptions) {
    this.options = options;
  }

  disable(): void {
    this.enabled = false;
  }

  observeIteratorResult(value: unknown): void {
    if (!isObject(value)) return;
    if (readOwn(value, 'done') === true) {
      this.finish(this.lastResultWasError ? 'failed' : 'completed');
      return;
    }
    const message = readOwn(value, 'value');
    if (readOwn(message, 'type') !== 'result') return;
    this.resultCount = Math.min(1_000_000, this.resultCount + 1);
    this.lastResultWasError = readOwn(message, 'is_error') === true;
  }

  finish(outcome: Outcome): void {
    if (this.finished) return;
    this.finished = true;
    if (!this.enabled) return;

    const duration = Math.max(0, Math.min(2_147_483_647, Math.round(performance.now() - this.started)));
    const event: ThreadforgeEvent = {
      timestamp: new Date().toISOString(),
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
      severity: outcome === 'completed' ? 'info' : 'medium',
      endpoint: { os: normalizedOS(process.platform) },
      harness: { name: 'claude_agent_sdk' },
      gen_ai: {
        system: 'anthropic',
        operation: 'claude-agent.query',
        status: outcome,
        streaming: true,
        duration_ms: duration,
        result_count: this.resultCount,
        content: { included: false, retention: 'metadata' },
      },
    };
    // Result usage can include prior turns on resumed or streaming sessions, so
    // this adapter deliberately records no token or cost attribution.
    void emitEvent(event, this.options).catch(() => undefined);
  }
}

function readOwn(value: unknown, key: string): unknown {
  if (!isObject(value)) return undefined;
  try {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    return descriptor && 'value' in descriptor ? descriptor.value : undefined;
  } catch {
    return undefined;
  }
}

function safeGet(value: unknown, key: PropertyKey): unknown {
  if (!isObject(value)) return undefined;
  try {
    return Reflect.get(value, key, value);
  } catch {
    return undefined;
  }
}

function isObject(value: unknown): value is object {
  return typeof value === 'object' && value !== null;
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}
