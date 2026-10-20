# Browser extension: Claude.ai and ChatGPT Web

Threadforge includes a partial Manifest V3 browser extension in `adapters/browser-extension`. It observes Claude.ai and ChatGPT Web streaming traffic in the page and maps completed or interrupted turns into Threadforge's local OTLP/HTTP logs contract.

## Build and test

Use Node.js 22 or newer:

```powershell
cd adapters/browser-extension
npm ci
npm run check
npm run test:unit
npm test
```

`npm test` builds the Chromium extension into `dist/` before running Playwright's local HTTPS replay suite. For local development, load `dist/` as an unpacked extension in a Chromium browser. This is a developer workflow, not signed/store distribution. `npm run build:firefox` emits `dist-firefox/`; `npm run lint:firefox` also runs Mozilla's manifest linter. Firefox has not been exercised in a real browser here. The Safari build target is not packaged or validated as a Safari extension.

The repository-wide `test.ps1` gate now includes this subproject's type check, unit tests, Chromium build and replay tests, Firefox build/lint, and unsigned Chromium/Firefox package smoke checks. Replay results and package archives are written under a unique temporary directory, not into the source tree's `test-results/` or `artifacts/` folders.

Create unsigned review archives with `npm run package:chromium` or `npm run package:firefox`. They are written to `artifacts/chromium/threadforge-browser-chromium.zip` and `artifacts/firefox/threadforge-browser-firefox.zip`. Both rebuild from their respective target first and include the Agent Beacon MIT license plus Threadforge's Apache license and NOTICE. These archives are not signed and do not establish store approval or installed-browser compatibility.

## Privacy and controls

- A fresh install has global capture disabled, Claude.ai and ChatGPT site toggles disabled, and `metadata` retention selected.
- Enable capture in the toolbar popup and explicitly enable each intended site in the options page. Browser site permissions are separately controlled by the browser.
- Retention can be changed to `redacted` or `full`; these modes can include conversation content. Choose them only if that content should be stored in the local Threadforge trace store.
- The receiver defaults to `http://127.0.0.1:4318/v1/logs`. Endpoint validation accepts only HTTP loopback hosts, an explicit port, and exactly the `/v1/logs` path; credentials, non-loopback hosts, and other paths are rejected. There is no endpoint editor in the options UI.
- The extension uses a bounded local retry queue. The popup reports queue depth, dropped records, active streams, and delivery issue codes, and offers **Clear queued data**.
- Captured conversation text is telemetry, not authorization to run tools or change policy.

The implementation caps the in-flight assembler at 12 turns total and 4 per tab, finalizes idle partial streams after 120 seconds, limits an intercepted request body to 256 Ki UTF-16 code units, limits a response to 4 MiB and individual stream chunks to 32 Ki UTF-16 code units, and caps the durable queue at 100 items / 4 MiB with a 512 KiB per-envelope limit and six delivery attempts. A local POST has a 10-second deadline; a full or repeatedly failing queue drops records and increments the visible drop counter.

## Compatibility and release limits

The site adapters follow Beacon's pinned replay fixtures and rely on private, undocumented vendor endpoints and stream formats. Tests use local HTTPS replay servers; they do not log into Anthropic/OpenAI, send real conversations, or prove compatibility with current production web clients. Vendor changes may stop capture or alter event interpretation. Chromium replay tests pass, and Firefox manifest lint passes, but no stable Chrome/Edge store installation, Firefox runtime, Safari packaging, cloud-agent, or cross-harness acceptance test is complete. Treat this as a development integration, not evidence that the browser-source parity or legacy cutover gate has passed.
