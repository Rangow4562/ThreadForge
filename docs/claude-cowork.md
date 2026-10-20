# Claude Cowork OTLP guidance

Claude Cowork telemetry is configured by an organization administrator and exported from Anthropic's service. It cannot reach Threadforge's loopback-only OTLP intake directly. This integration therefore prints manual setup guidance only; it does not install Cowork, receive or relay its events, or validate delivery.

To print the values to review and enter manually:

```powershell
npm.cmd run adapter -- claude-cowork otel print-config --endpoint https://otel.company.com
```

The endpoint is required and must be an HTTPS URL with a DNS hostname. Threadforge rejects IP literals, common local-only hostnames, embedded credentials, query strings, and fragments. This is a syntax/safety check only: it does not resolve DNS, verify public reachability, or make a network request. Use a separately operated, durable collector that Anthropic services can reach.

The printed protocol is `HTTP/protobuf`; the resource attributes are `deployment.environment=prod,service.name=claude-cowork`. If the collector requires authentication, configure its headers directly in the Claude organization console. Threadforge accepts no header option, does not print or persist secrets, and does not modify the console or collector.

Cowork telemetry may include prompts, tool parameters and results, file paths, user identifiers, model usage, and errors. Review collection, retention, and access policy before enabling remote export. Do not use a laptop, loopback endpoint, or temporary tunnel for production.

This slice has no `setup`, `status`, or `validate` operation. No Cowork host, organization export, public collector, or end-to-end event delivery was tested. It is partial source-matrix coverage, not cutover evidence. The implementation is newly written against the pinned Beacon docs as a behavior reference; no Beacon source was copied.
