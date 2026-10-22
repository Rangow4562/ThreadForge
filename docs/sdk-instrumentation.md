# Node SDK instrumentation

`@threadforge/adapter-contracts/genai-sdk` provides dependency-free wrappers for direct OpenAI and Anthropic clients. The wrapper detects supported SDK methods structurally, so an application does not need both provider packages installed.

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

The adapter-contracts package is currently private and unpublished. This export is available from a Threadforge source checkout/workspace; a public npm release remains behind the repository's release, dependency, and licensing gates.

`instrumentOpenAI(client, options)` wraps available `responses.create`, `chat.completions.create`, and `completions.create` methods. `instrumentAnthropic(client, options)` wraps `messages.create`. The return value contains the same client instance and an idempotent `dispose()` method. Disposal restores a method only if Threadforge still owns the installed wrapper; application changes made after installation are preserved.

Events are emitted asynchronously to the existing loopback-only `/v1/events` receiver (default `http://127.0.0.1:4318/v1/events`). Public options are limited to `endpoint` and `timeoutMs`; non-loopback endpoints are rejected and telemetry failures never change provider results or exceptions. Calls report provider, operation, allowlisted model identifiers, duration, completion/failure/interruption status, and validated token counts. Message, prompt, output, provider error text, credentials, and response IDs are never read or emitted. Streaming calls pass each yielded value unchanged and emit one terminal metadata event when drained, cancelled, or failed.

This is a local metadata observer, not Beacon's OpenTelemetry auto-instrumentation. It does not instrument Anthropic/OpenAI transports outside the documented methods, the Claude Agent SDK, or Vercel AI SDK. Host/provider live-version compatibility, deployment packaging, remote export, and cross-harness efficacy remain separate acceptance work.

Focused fixtures run with `node --experimental-strip-types --test test/genai-sdk.test.ts` from `adapters/`; the repository-wide `test.ps1` includes the type-check, full adapter tests, and package export audit.
