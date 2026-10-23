import assert from 'node:assert/strict';
import test from 'node:test';
import { createThreadforgeVercelTelemetryOptions } from '../src/vercel-ai-sdk.ts';

test('creates opt-in Vercel telemetry settings with input and output recording disabled', () => {
  let spansStarted = 0;
  const tracer = { startSpan() { spansStarted += 1; return {}; } };
  const settings = createThreadforgeVercelTelemetryOptions(tracer);

  assert.deepEqual(settings, {
    isEnabled: true,
    tracer,
    recordInputs: false,
    recordOutputs: false,
  });
  assert.equal(settings.tracer, tracer);
  assert.equal(spansStarted, 0, 'creating settings must not start a span or make a network request');
  assert.equal(Object.hasOwn(settings, 'endpoint'), false, 'exporter endpoints remain application-owned');
});

test('rejects missing or malformed tracers without initializing global telemetry', () => {
  assert.throws(() => createThreadforgeVercelTelemetryOptions(null as never), /tracer is required/);
  assert.throws(() => createThreadforgeVercelTelemetryOptions({} as never), /tracer is required/);
  assert.throws(() => createThreadforgeVercelTelemetryOptions({ startSpan: 1 } as never), /tracer is required/);
});

test('does not mutate tracer state or expose unrelated configuration fields', () => {
  const tracer = Object.freeze({ startSpan() { return {}; }, privateLabel: 'PRIVATE_TRACER_LABEL' });
  const originalKeys = Object.keys(tracer);
  const settings = createThreadforgeVercelTelemetryOptions(tracer);
  assert.deepEqual(Object.keys(tracer), originalKeys);
  assert.deepEqual(Object.keys(settings).sort(), ['isEnabled', 'recordInputs', 'recordOutputs', 'tracer'].sort());
});
