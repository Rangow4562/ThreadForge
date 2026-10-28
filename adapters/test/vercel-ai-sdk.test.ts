import assert from 'node:assert/strict';
import test from 'node:test';
import { createThreadforgeVercelTelemetryOptions } from '../src/vercel-ai-sdk.ts';

test('creates Vercel AI SDK v7 per-call telemetry settings with content and context capture disabled', () => {
  const settings = createThreadforgeVercelTelemetryOptions();

  assert.deepEqual(settings, {
    recordInputs: false,
    recordOutputs: false,
    includeRuntimeContext: false,
    includeToolsContext: false,
  });
  assert.deepEqual(Object.keys(settings).sort(), [
    'includeRuntimeContext', 'includeToolsContext', 'recordInputs', 'recordOutputs',
  ]);
});

test('does not initialize global telemetry, exporters, or network state', () => {
  const first = createThreadforgeVercelTelemetryOptions();
  const second = createThreadforgeVercelTelemetryOptions();
  assert.notEqual(first, second);
  assert.equal(Object.hasOwn(first, 'tracer'), false);
  assert.equal(Object.hasOwn(first, 'isEnabled'), false);
  assert.equal(Object.hasOwn(first, 'endpoint'), false);
});
