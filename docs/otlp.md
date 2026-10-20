# Local OTLP intake

`threadforge serve` provides both OTLP transports on loopback by default:

- OTLP/HTTP on `127.0.0.1:4318`: logs, traces, and metrics at `POST /v1/logs`, `POST /v1/traces`, and `POST /v1/metrics`.
- OTLP/gRPC on `127.0.0.1:4317`: the standard collector `LogsService`, `TraceService`, and `MetricsService` export RPCs. Set `--grpc-listen ""` to disable this listener.

The HTTP endpoints accept OTLP/JSON (`application/json`) or OTLP/Protobuf (`application/x-protobuf`); gzip is also supported. Compressed and expanded HTTP bodies are each bounded to 8 MiB. Both transports bound each request to 100 log records, spans, or metric data points, and the gRPC server caps received messages at 8 MiB and concurrent streams at 32. Both listeners reject non-loopback IP addresses. They use plaintext only on loopback; do not expose either listener to a network.

The receiver converts log records, spans, and metric points through the same canonical decoders and into the same local Beacon-compatible event JSONL. Metric aggregation names, units, and point values remain in local event metadata; span IDs are normalized to hex and linked into trace metadata. OTLP key/value attributes are normalized before the trace store applies credential-key redaction. A malformed or over-limit batch is rejected as a whole before persistence; dashboard summaries omit event bodies and raw attributes.

For HTTP, successful requests return HTTP 200 with the matching OTLP response representation; invalid media types return 415, malformed or over-limit requests return 400, and local persistence errors return 500. Error bodies use `google.rpc.Status`. For gRPC, invalid data or an over-limit signal batch returns `InvalidArgument`, an over-limit encoded message returns `ResourceExhausted`, and local persistence failures return a generic `Internal` status without source values.

Normalized events remain in private local JSONL. Remote forwarding and signal-specific retention are not implemented. Host configuration remains separate; verify each host's exporter contract before enabling it.

## Local metric queries

`GET /v1/metrics/catalog?limit=100` lists bounded metric descriptors from the newest 64 MiB of the local event log. The limit is 1–250; results identify only safe metric name, harness, unit, aggregation, point count, and first/last timestamps. `older_events_omitted`, `metrics_truncated`, and `malformed_lines` make partial results visible.

`GET /v1/metrics/timeseries?name=<exact-name>&from=<RFC3339>&to=<RFC3339>&measure=value&limit=500` returns up to 1,000 numeric points for an exact metric name. `from` and `to` default together to the last 24 hours and may span at most 30 days. Optional exact filters are `harness`, `unit`, and `aggregation`; `measure` is `value` (`asDouble`/`asInt`), `count`, `sum`, `min`, or `max`. The response reports `matched_points`, `points_truncated`, `older_events_omitted`, and `malformed_lines`; when capped, it keeps the latest points by timestamp. Metric attributes and descriptions are never returned, because they may contain private dimensions or secrets. The dashboard's Metric time series card loads and queries metrics only after the user requests it; it renders a scatter plot so omitted dimensions are not implied to be one continuous series. Contracts are [`metric-catalog-v1`](../schema/metric-catalog-v1.schema.json) and [`metric-timeseries-v1`](../schema/metric-timeseries-v1.schema.json).

Tests cover logs, traces, and metrics over HTTP JSON/Protobuf and gRPC, gzip HTTP logs, response contracts, payload and record caps, trace correlation, credential redaction, invalid-batch atomicity, and severity mapping.
