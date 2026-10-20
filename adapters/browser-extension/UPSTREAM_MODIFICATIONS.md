# Agent Beacon browser-extension reuse and modifications

This directory contains source imported from Agent Beacon commit `5303224c63ddb73b7c2a125278f4233d807348d9`, licensed under MIT. The exact upstream license is retained in `LICENSE.agent-beacon`. This file records Threadforge's modifications to the imported component. Threadforge-authored changes are covered by the repository's Apache-2.0 license where separable from upstream material.

## Imported scope

Imported: `src/**`, `test/unit/**`, `e2e/**`, `fixtures/**`, `tools/**`, package/build/test configuration, and lockfile. The upstream README and generated build outputs were excluded. The package is independently named `@threadforge/browser-extension`.

## Modified upstream paths

- `src/manifest.json`: Threadforge product labels; HTTPS-only chat-site matches and web-accessible-resource origins; loopback receiver permissions.
- `src/background/assembler.ts`: at most 12 in-flight turns total and 4 per tab; idle streams finalize after 120 seconds.
- `src/background/delivery.ts`: local-endpoint validation, 100-item / 4 MiB durable queue, 512 KiB per-envelope limit, six delivery attempts, delivery issue codes, and a 10-second request timeout.
- `src/background/settings.ts`, `src/shared/types.ts`: capture disabled, both sites disabled, and metadata-only retention by default.
- `src/background/sw.ts`, `src/content/content.ts`, `src/interceptor/interceptor.ts`: explicit opt-in propagation, strict message/host checks, main-world capture gating, request-body cap of 256 Ki UTF-16 code units, response cap of 4 MiB, stream chunks capped at 32 Ki UTF-16 code units, and service-worker completion handling.
- `src/shared/normalize.ts`, `src/shared/vocab.ts`: metadata-only privacy behavior and Threadforge-compatible OTLP event/attribute mapping.
- `src/options/options.html`, `src/options/options.ts`, `src/popup/popup.html`, `src/popup/popup.ts`: Threadforge labels, opt-in/site controls, local delivery status, and explicit queued-data clearing.
- `tools/targets.mjs`: Threadforge Gecko add-on ID and Threadforge product/data-disclosure labels.
- `package.json`, `package-lock.json`: package identity and the pinned dependency lock for the independent workspace.
- `esbuild.config.mjs`: copies the exact Beacon MIT license plus Threadforge's Apache license and NOTICE into each browser target so generated packages preserve attribution.
- `test/unit/normalize.test.ts`, `test/unit/permissions.test.ts`, `test/unit/targets.test.ts`, `e2e/permissions.spec.ts`: updated expectations for narrowed permissions, new identity, and privacy defaults.
- `e2e/helpers/cert.ts`, `e2e/helpers/mock-collector.ts`, `e2e/helpers/sse-replay-server.ts`: isolated replay TLS setup, Threadforge-local collector behavior, and deterministic opt-in timing.

## Threadforge-authored paths

- `src/shared/endpoint.ts`, `src/shared/messages.ts`, and `src/shared/settings-schema.ts`: local receiver validation, strict relay-message checks, and fail-closed settings normalization.
- `test/unit/assembler.test.ts`, `test/unit/endpoint.test.ts`, `test/unit/messages.test.ts`, and `test/unit/settings-schema.test.ts`: added boundedness, privacy, and validation coverage.
- `e2e/privacy-defaults.spec.ts`: verifies installation captures nothing and metadata mode does not transmit prompt or response text.

The integration tests use synthetic local fixtures only. No live vendor API or account data is included. Refer to [the browser extension guide](../../docs/browser-extension.md) for supported local workflows and unverified compatibility boundaries.
