import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createThreadforgeVercelTelemetryOptions } from '../src/vercel-ai-sdk.ts';

const npmPrefix = parseNpmPrefix(process.argv.slice(2));
const localRequire = createRequire(import.meta.url);
const resolveOptions = npmPrefix ? { paths: [npmPrefix] } : undefined;

function parseNpmPrefix(args) {
  if (args.length === 0) return undefined;
  if (args.length !== 2 || args[0] !== '--npm-prefix' || !isAbsolute(args[1])) {
    throw new Error('usage: node --experimental-strip-types scripts/vercel-ai-sdk-live-smoke.mjs [--npm-prefix <absolute-path>]');
  }
  return resolve(args[1]);
}

function resolvePackage(specifier) {
  try {
    return localRequire.resolve(specifier, resolveOptions);
  } catch {
    throw new Error(`optional package ${specifier} is unavailable; install the documented smoke dependencies first`);
  }
}

async function loadPackage(specifier) {
  return import(pathToFileURL(resolvePackage(specifier)).href);
}

async function readPackageManifest(name, entrySpecifier = name) {
  let directory = dirname(resolvePackage(entrySpecifier));
  while (true) {
    try {
      const candidate = join(directory, 'package.json');
      const manifest = JSON.parse(await readFile(candidate, 'utf8'));
      if (manifest.name === name) return manifest;
    } catch {
      // Keep walking toward node_modules; package entry directories need not
      // contain manifests themselves.
    }
    const parent = dirname(directory);
    if (parent === directory) break;
    directory = parent;
  }
  throw new Error(`could not read installed package metadata for ${name}`);
}

function assertSupportedAISDKVersion(version) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!match) throw new Error('AI SDK smoke requires a stable semantic version');
  const [, majorText, minorText, patchText] = match;
  const major = Number(majorText);
  const minor = Number(minorText);
  const patch = Number(patchText);
  if (major !== 7 || (minor === 0 && patch < 114)) {
    throw new Error('AI SDK smoke requires Vercel AI SDK v7.0.114 or later within v7');
  }
}

const [ai, aiTest, otel, otelSDK] = await Promise.all([
  loadPackage('ai'),
  loadPackage('ai/test'),
  loadPackage('@ai-sdk/otel'),
  loadPackage('@opentelemetry/sdk-trace'),
]);
const [aiManifest, otelManifest, otelSDKManifest] = await Promise.all([
  readPackageManifest('ai'),
  readPackageManifest('@ai-sdk/otel'),
  readPackageManifest('@opentelemetry/sdk-trace'),
]);
assertSupportedAISDKVersion(aiManifest.version);
assert.equal(typeof ai.generateText, 'function');
assert.equal(typeof ai.registerTelemetry, 'function');
assert.equal(typeof aiTest.MockLanguageModelV3, 'function');
assert.equal(typeof otel.OpenTelemetry, 'function');
assert.equal(typeof otelSDK.TracerProvider, 'function');

const exporter = new otelSDK.InMemorySpanExporter();
const tracerProvider = new otelSDK.TracerProvider({
  spanProcessors: [new otelSDK.SimpleSpanProcessor({ exporter })],
});
const runtimeContextMarker = 'THREADFORGE_PRIVATE_RUNTIME_CONTEXT_MARKER';
const toolContextMarker = 'THREADFORGE_PRIVATE_TOOL_CONTEXT_MARKER';
const usage = {
  inputTokens: { total: 3, noCache: 3, cacheRead: 0, cacheWrite: 0 },
  outputTokens: { total: 2, text: 2, reasoning: 0 },
};
let toolContextSeen = false;
const toolContextSchema = ai.jsonSchema({
  type: 'object',
  properties: { privateMarker: { type: 'string' } },
  required: ['privateMarker'],
  additionalProperties: false,
});
const tools = {
  lookup: ai.tool({
    inputSchema: ai.jsonSchema({ type: 'object', properties: {}, additionalProperties: false }),
    contextSchema: toolContextSchema,
    execute: async (_input, { context }) => {
      assert.ok(context.privateMarker === toolContextMarker, 'the mock tool must receive its private context');
      toolContextSeen = true;
      return 'safe local tool result';
    },
  }),
};
const mockModel = new aiTest.MockLanguageModelV3({
  provider: 'threadforge-smoke',
  modelId: 'mock-model',
  doGenerate: [
    {
      content: [{ type: 'tool-call', toolCallId: 'threadforge-smoke-call', toolName: 'lookup', input: '{}' }],
      finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
      usage,
      warnings: [],
    },
    {
      content: [{ type: 'text', text: 'THREADFORGE_PRIVATE_OUTPUT_MARKER' }],
      finishReason: { unified: 'stop', raw: 'stop' },
      usage,
      warnings: [],
    },
  ],
});

try {
  ai.registerTelemetry(new otel.OpenTelemetry({
    tracer: tracerProvider.getTracer('threadforge-vercel-ai-sdk-smoke'),
  }));
  let result;
  try {
    result = await ai.generateText({
      model: mockModel,
      prompt: 'THREADFORGE_PRIVATE_PROMPT_MARKER',
      tools,
      stopWhen: ai.stepCountIs(2),
      runtimeContext: { privateMarker: runtimeContextMarker },
      toolsContext: { lookup: { privateMarker: toolContextMarker } },
      telemetry: createThreadforgeVercelTelemetryOptions(),
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : 'unknown error';
    const safeMessage = [
      'THREADFORGE_PRIVATE_PROMPT_MARKER',
      'THREADFORGE_PRIVATE_OUTPUT_MARKER',
      runtimeContextMarker,
      toolContextMarker,
    ].reduce((current, marker) => current.replaceAll(marker, '[redacted]'), message);
    throw new Error(`Vercel AI SDK mock generation or tool execution failed: ${safeMessage}`);
  }
  await tracerProvider.forceFlush();

  const finishedSpans = exporter.getFinishedSpans();
  const serializedSpans = JSON.stringify(finishedSpans.map((span) => ({
    name: span.name,
    attributes: span.attributes,
    events: span.events,
  })));
  assert.ok(result.text === 'THREADFORGE_PRIVATE_OUTPUT_MARKER', 'mock model output was not returned');
  assert.ok(finishedSpans.length > 0, 'the registered telemetry integration must receive spans');
  assert.equal(toolContextSeen, true, 'the smoke must execute the tool with its private context');
  assert.equal(serializedSpans.includes('THREADFORGE_PRIVATE_PROMPT_MARKER'), false);
  assert.equal(serializedSpans.includes('THREADFORGE_PRIVATE_OUTPUT_MARKER'), false);
  assert.equal(serializedSpans.includes(runtimeContextMarker), false);
  assert.equal(serializedSpans.includes(toolContextMarker), false);
  assert.equal(mockModel.doGenerateCalls.length, 2);

  process.stdout.write(`${JSON.stringify({
    schema_version: 'threadforge.vercel-ai-sdk-live-smoke.v1',
    ai_sdk_version: aiManifest.version,
    ai_sdk_otel_version: otelManifest.version,
    opentelemetry_sdk_version: otelSDKManifest.version,
    mock_model_calls: mockModel.doGenerateCalls.length,
    mock_tool_executions: toolContextSeen ? 1 : 0,
    captured_span_count: finishedSpans.length,
    prompt_marker_captured: false,
    output_marker_captured: false,
    runtime_context_marker_captured: false,
    tool_context_marker_captured: false,
    real_model_calls: 0,
    real_provider_calls: 0,
  })}\n`);
} finally {
  await tracerProvider.shutdown();
}
