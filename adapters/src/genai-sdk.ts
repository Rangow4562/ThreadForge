import { randomUUID } from 'node:crypto';
import { emitEvent, type ThreadforgeEvent } from './emit.ts';

/**
 * Options shared by the structural OpenAI and Anthropic SDK adapters.
 * Telemetry is sent through the existing loopback-only /v1/events contract.
 */
export interface GenAISdkOptions {
  endpoint?: string;
  timeoutMs?: number;
}

export interface SdkInstrumentation<T extends object> {
  /** The same client instance passed to the instrument function. */
  client: T;
  /** Restore methods only when they are still owned by this instrumentation. */
  dispose(): void;
}

type Provider = 'openai' | 'anthropic';
type Outcome = 'completed' | 'failed' | 'interrupted';
type MethodPath = string[];

const wrappedMethods = new WeakSet<Function>();

/**
 * Observe OpenAI Responses, Chat Completions, and legacy Completions calls.
 * The SDK is detected structurally, so applications do not need a hard runtime
 * dependency on the OpenAI package when they use another provider.
 */
export function instrumentOpenAI<T extends object>(
  client: T,
  options: GenAISdkOptions = {},
): SdkInstrumentation<T> {
  return instrument(client, 'openai', [
    ['responses', 'create'],
    ['chat', 'completions', 'create'],
    ['completions', 'create'],
  ], options);
}

/** Observe direct Anthropic Messages API calls without retaining message text. */
export function instrumentAnthropic<T extends object>(
  client: T,
  options: GenAISdkOptions = {},
): SdkInstrumentation<T> {
  return instrument(client, 'anthropic', [['messages', 'create']], options);
}

function instrument<T extends object>(
  client: T,
  provider: Provider,
  paths: MethodPath[],
  options: GenAISdkOptions,
): SdkInstrumentation<T> {
  const restore: Array<() => void> = [];
  let count = 0;
  try {
    for (const path of paths) {
      const owner = resolveOwner(client, path.slice(0, -1));
      if (!owner) continue;
      const methodName = path[path.length - 1];
      const original = safeGet(owner, methodName);
      if (typeof original !== 'function') continue;
      if (wrappedMethods.has(original)) {
        throw new Error(`SDK method ${path.join('.')} is already instrumented`);
      }

      const operation = path.join('.');
      const wrapped = function (...args: unknown[]): unknown {
        const observation = new CallObservation(provider, operation, args[0], options);
        let result: unknown;
        try {
          result = Reflect.apply(original, owner, args);
        } catch (error) {
          observation.finish('failed');
          throw error;
        }

        if (isPromiseLike(result)) {
          return Promise.resolve(result).then(
            (response) => {
              if (isAsyncIterable(response)) return wrapStream(response, observation);
              observation.finish('completed', response);
              return response;
            },
            (error: unknown) => {
              observation.finish('failed');
              throw error;
            },
          );
        }
        if (isAsyncIterable(result)) return wrapStream(result, observation);
        observation.finish('completed', result);
        return result;
      };

      restore.push(replaceMethod(owner, methodName, wrapped));
      wrappedMethods.add(wrapped);
      count += 1;
    }
  } catch (error) {
    for (const undo of restore.reverse()) undo();
    throw error;
  }

  if (count === 0) {
    throw new Error(`no supported ${provider} SDK create method was found`);
  }

  let disposed = false;
  return {
    client,
    dispose() {
      if (disposed) return;
      disposed = true;
      for (const undo of restore.reverse()) undo();
    },
  };
}

function resolveOwner(root: object, path: string[]): object | undefined {
  let current: unknown = root;
  for (const key of path) {
    current = safeGet(current, key);
    if (!isObject(current)) return undefined;
  }
  return isObject(current) ? current : undefined;
}

function replaceMethod(owner: object, key: string, wrapped: Function): () => void {
  const previous = Object.getOwnPropertyDescriptor(owner, key);
  if (previous && !previous.configurable &&
      (!('value' in previous) || previous.writable !== true)) {
    throw new Error(`SDK method ${key} cannot be instrumented without changing a locked property`);
  }
  if (!previous && !Object.isExtensible(owner)) {
    throw new Error(`SDK method ${key} cannot be instrumented on a non-extensible client`);
  }

  if (previous && !previous.configurable) {
    Object.defineProperty(owner, key, { value: wrapped });
  } else {
    Object.defineProperty(owner, key, {
      configurable: true,
      enumerable: previous?.enumerable ?? false,
      writable: true,
      value: wrapped,
    });
  }

  return () => {
    const current = Object.getOwnPropertyDescriptor(owner, key);
    if (!current || !('value' in current) || current.value !== wrapped) return;
    if (!previous) {
      Reflect.deleteProperty(owner, key);
    } else if (!previous.configurable) {
      Object.defineProperty(owner, key, { value: previous.value });
    } else {
      Object.defineProperty(owner, key, previous);
    }
  };
}

class CallObservation {
  private readonly provider: Provider;
  private readonly operation: string;
  private readonly options: GenAISdkOptions;
  private readonly started = performance.now();
  private readonly requestModel: string | undefined;
  private streaming: boolean;
  private responseModel: string | undefined;
  private inputTokens: number | undefined;
  private outputTokens: number | undefined;
  private finished = false;

  constructor(
    provider: Provider,
    operation: string,
    request: unknown,
    options: GenAISdkOptions,
  ) {
    this.provider = provider;
    this.operation = operation;
    this.options = options;
    this.requestModel = safeModel(readOwn(request, 'model'));
    this.streaming = readOwn(request, 'stream') === true;
  }

  get isFinished(): boolean {
    return this.finished;
  }

  markStreaming(): void {
    this.streaming = true;
  }

  observe(value: unknown): void {
    this.observeRecord(value);
    const response = readOwn(value, 'response');
    const message = readOwn(value, 'message');
    if (response !== undefined) this.observeRecord(response);
    if (message !== undefined) this.observeRecord(message);
  }

  finish(outcome: Outcome, response?: unknown): void {
    if (this.finished) return;
    if (response !== undefined) this.observe(response);
    this.finished = true;

    const duration = Math.max(0, Math.min(2_147_483_647, Math.round(performance.now() - this.started)));
    const genAI: Record<string, unknown> = {
      system: this.provider,
      operation: this.operation,
      status: outcome,
      streaming: this.streaming,
      duration_ms: duration,
      content: { included: false, retention: 'metadata' },
      ...(this.requestModel ? { request_model: this.requestModel } : {}),
      ...(this.responseModel ? { response_model: this.responseModel } : {}),
      ...(this.inputTokens !== undefined || this.outputTokens !== undefined ? {
        usage: {
          ...(this.inputTokens !== undefined ? { input_tokens: this.inputTokens } : {}),
          ...(this.outputTokens !== undefined ? { output_tokens: this.outputTokens } : {}),
        },
      } : {}),
    };
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
      harness: { name: `${this.provider}_sdk` },
      gen_ai: genAI,
    };
    // Telemetry is observational: a missing local receiver never changes the
    // provider call's value, timing contract, or error behavior.
    void emitEvent(event, this.options).catch(() => undefined);
  }

  private observeRecord(value: unknown): void {
    const model = safeModel(readOwn(value, 'model'));
    if (model) this.responseModel = model;
    const usage = readOwn(value, 'usage');
    if (!isObject(usage)) return;
    const input = safeTokenCount(readOwn(usage, 'input_tokens')) ??
      safeTokenCount(readOwn(usage, 'prompt_tokens'));
    const output = safeTokenCount(readOwn(usage, 'output_tokens')) ??
      safeTokenCount(readOwn(usage, 'completion_tokens'));
    if (input !== undefined) this.inputTokens = input;
    if (output !== undefined) this.outputTokens = output;
  }
}

function wrapStream<T extends AsyncIterable<unknown>>(source: T, observation: CallObservation): T {
  observation.markStreaming();
  const proxy = new Proxy(source, {
    get(target, property) {
      if (property === Symbol.asyncIterator) {
        return () => {
          const iterator = target[Symbol.asyncIterator]();
          return (async function* () {
            try {
              while (true) {
                const item = await iterator.next();
                if (item.done) {
                  observation.finish('completed', item.value);
                  return item.value;
                }
                observation.observe(item.value);
                yield item.value;
              }
            } catch (error) {
              observation.finish('failed');
              throw error;
            } finally {
              if (!observation.isFinished) {
                try {
                  await iterator.return?.();
                } catch (error) {
                  observation.finish('failed');
                  throw error;
                }
                observation.finish('interrupted');
              }
            }
          })();
        };
      }
      const value = Reflect.get(target, property, target);
      return typeof value === 'function' ? value.bind(target) : value;
    },
  });
  return proxy;
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

function safeModel(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$/.test(value)
    ? value
    : undefined;
}

function safeTokenCount(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 2_147_483_647
    ? value
    : undefined;
}

function isPromiseLike(value: unknown): value is PromiseLike<unknown> {
  if (!isObject(value) && typeof value !== 'function') return false;
  try {
    return typeof Reflect.get(value, 'then', value) === 'function';
  } catch {
    return false;
  }
}

function isAsyncIterable(value: unknown): value is AsyncIterable<unknown> {
  if (!isObject(value)) return false;
  try {
    return typeof Reflect.get(value, Symbol.asyncIterator, value) === 'function';
  } catch {
    return false;
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
