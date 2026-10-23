/**
 * Create content-minimizing Vercel AI SDK telemetry settings.
 * The caller owns and configures the tracer/exporter; this helper performs no
 * network or global OpenTelemetry setup of its own.
 */
export function createThreadforgeVercelTelemetryOptions<TTracer extends object>(
  tracer: TTracer,
): {
  isEnabled: true;
  tracer: TTracer;
  recordInputs: false;
  recordOutputs: false;
} {
  if (!isObject(tracer) || typeof safeGet(tracer, 'startSpan') !== 'function') {
    throw new Error('a valid OpenTelemetry tracer is required');
  }

  return {
    isEnabled: true,
    tracer,
    recordInputs: false,
    recordOutputs: false,
  };
}

function safeGet(value: object, key: PropertyKey): unknown {
  try {
    return Reflect.get(value, key, value);
  } catch {
    return undefined;
  }
}

function isObject(value: unknown): value is object {
  return typeof value === 'object' && value !== null;
}
