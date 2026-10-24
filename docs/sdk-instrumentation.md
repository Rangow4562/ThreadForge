# Node SDK instrumentation

`@threadforge/adapter-contracts/genai-sdk` provides dependency-free wrappers for direct OpenAI and Anthropic clients. `@threadforge/adapter-contracts/claude-agent-sdk` observes one Claude Agent SDK `query()` result stream without importing the provider package. `@threadforge/adapter-contracts/vercel-ai-sdk` creates safe settings for Vercel AI SDK's native OpenTelemetry integration. These adapters do not require provider packages as runtime dependencies.

```ts
import OpenAI from "openai";
import { instrumentOpenAI } from "@threadforge/adapter-contracts/genai-sdk";

const openai = new OpenAI();
const telemetry = instrumentOpenAI(openai);

try {
  await openai.responses.create({ model: "gpt-4.1-mini", input: "..." });
} finally {
  telemetry.dispose();
}
```

For a Claude Agent SDK query, pass the query object directly to the observer. It does not wrap the SDK's `query()` function or inspect the prompt:

```ts
import { query } from "@anthropic-ai/claude-agent-sdk";
import { instrumentClaudeAgentQuery } from "@threadforge/adapter-contracts/claude-agent-sdk";

const observed = instrumentClaudeAgentQuery(query({ prompt: "...", options: {} }));
try {
  for await (const message of observed.query) {
    // Handle the original SDK message unchanged.
  }
} finally {
  observed.dispose();
}
```

The adapter-contracts package is currently private and unpublished. This export is available from a Threadforge source checkout/workspace; a public npm release remains behind the repository's release, dependency, and licensing gates.

`instrumentOpenAI(client, options)` wraps available `responses.create`, `chat.completions.create`, and `completions.create` methods. `instrumentAnthropic(client, options)` wraps `messages.create`. The return value contains the same client instance and an idempotent `dispose()` method. Disposal restores a method only if Threadforge still owns the installed wrapper; application changes made after installation are preserved.

Events are emitted asynchronously to the existing loopback-only `/v1/events` receiver (default `http://127.0.0.1:4318/v1/events`). Public options are limited to `endpoint` and `timeoutMs`; non-loopback endpoints are rejected and telemetry failures never change provider results or exceptions. Calls report provider, operation, allowlisted model identifiers, duration, completion/failure/interruption status, and validated token counts. Message, prompt, output, provider error text, credentials, and response IDs are never read or emitted. Streaming calls pass each yielded value unchanged and emit one terminal metadata event when drained, cancelled, or failed.

These are local metadata observers, not Beacon's OpenTelemetry auto-instrumentation. The direct-provider wrappers do not instrument Anthropic/OpenAI transports outside the documented methods. Host/provider live-version compatibility, deployment packaging, remote export, and cross-harness efficacy remain separate acceptance work.

`instrumentClaudeAgentQuery(query, options)` returns `{ query, dispose() }`. It preserves the query's async iteration and binds control methods to the original query object. It counts terminal `type: "result"` markers and uses only the result's boolean `is_error` to distinguish completed from failed calls. Early iterator return is reported as interrupted. It does not inspect message bodies, `result`, session IDs, UUIDs, model-usage snapshots, or control payloads. Token and cost attribution are intentionally omitted because SDK usage snapshots can be cumulative across streaming turns and resumed sessions. Disposing disables telemetry without interrupting the query. This remains a structural fixture-tested observer; no real Claude Agent SDK process or model run is part of the test suite.

For Vercel AI SDK, `createThreadforgeVercelTelemetryOptions(tracer)` returns the native `experimental_telemetry` shape with `isEnabled: true`, `recordInputs: false`, and `recordOutputs: false`. Pass the result to each `generateText` or `streamText` call that should emit spans:

```ts
import { generateText } from "ai";
import { createThreadforgeVercelTelemetryOptions } from "@threadforge/adapter-contracts/vercel-ai-sdk";

const telemetry = createThreadforgeVercelTelemetryOptions(localTracer);
await generateText({ model, prompt, experimental_telemetry: telemetry });
```

`localTracer` must be supplied and configured by the application (for example, with a local OTLP exporter targeting Threadforge's loopback receiver). The helper creates no exporter, process-global provider, environment setting, or network connection. The SDK/provider span attributes and live Vercel package compatibility have not been validated here; do not enable input/output recording when composing other telemetry settings.

For applications that own an OpenTelemetry provider, `@threadforge/adapter-contracts/otel-span-processor` exports `createThreadforgeSpanProcessor(options={})`, a dependency-free structural `SpanProcessor` implementation. It accepts only a fixed set of `gen_ai.operation.name` values and bounded provider/model/token metadata, and posts projected events to the loopback `/v1/events` intake. `endpoint` defaults to `http://127.0.0.1:4318/v1/events`; `timeoutMs` defaults to `1500` and must be from 100 to 10000. At most 64 intake requests are in flight; overflow is dropped, and `forceFlush()`/`shutdown()` wait for accepted in-flight requests. Span names, trace/span IDs, arbitrary attributes, events, links, exception descriptions, prompt/output text, and credential-shaped provider/model values are not copied. This is a local metadata bridge, not Beacon's OpenTelemetry auto-instrumentation or a live-version compatibility claim. The application remains responsible for provider/exporter setup and for ensuring its own other exporters do not forward sensitive data.

Focused fixtures run with `node --experimental-strip-types --test test/genai-sdk.test.ts` from `adapters/`; the repository-wide `test.ps1` includes the type-check, full adapter tests, and package export audit.
