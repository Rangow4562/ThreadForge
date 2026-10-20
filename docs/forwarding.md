# Local event forwarding

Threadforge remains a local JSONL producer by default. The S3, CloudWatch, GCS,
Splunk HEC, Falcon HEC, Datadog, Sumo Logic, and Rapid7 commands generate
optional customer-managed Vector packs. Elasticsearch also supports
Filebeat/standalone Elastic Agent packs with a local Vector sanitizer. Pack
generation never installs a shipper, starts a service, reads destination
credentials, contacts a provider, or uploads data.

Generating a config requires an explicit acknowledgement because an activated
forwarder sends timestamp and allowlisted severity metadata to a remote
destination.
The generated transform reconstructs a fresh object containing only the event
timestamp, severity (`info`, `low`, `medium`, `high`, `critical`, or `unknown`),
and a fixed schema/privacy marker. It omits raw event content, event/action
names, IDs, harness/model names, paths, and attributes:

```powershell
threadforge endpoint s3 print-config `
  --bucket <bucket-name> `
  --allow-remote-metadata-forwarding

threadforge endpoint s3 install-pack `
  --bucket <bucket-name> `
  --region us-east-1 `
  --prefix threadforge `
  --output .\threadforge-s3-pack `
  --allow-remote-metadata-forwarding
```

The acknowledgement is required for both commands. The printed config is only
text; `install-pack` writes `README.md`, `sample-event.jsonl`, and `vector.toml`
into a new directory (created with mode `0700` on POSIX; Windows inherits the
parent directory ACL). An existing output directory is refused rather than
merged or overwritten. The generated `vector.toml` includes the selected local
trace path, so keep the pack private and out of version control.

The file source starts at the end of the trace log to avoid historical
backfill. It forwards only metadata derived from newly written runtime events;
the original event object is not sent, and this initial pack does not include
inventory snapshots. Starting Vector is a separate operator action. Review
timestamp metadata and the destination bucket's access, encryption, and
retention controls before starting it. The Threadforge CLI does not put AWS
credentials in the pack; provide them to the customer-managed Vector process
through your approved external credential mechanism.

`sample-event.jsonl` contains one synthetic Beacon-compatible event and no
user, hostname, prompt, task, path, trace, or provider data. The README inside
the pack shows a manual S3 upload command for that synthetic event only; it
does not provide a command that copies the local trace log.

The generated TOML is parsed and contract-tested locally, but this does not
execute the Vector remap program or verify a particular Vector binary/version,
S3 bucket, credentials, IAM policy, remote delivery, or held-out cutover
evidence. Other Beacon destinations,
managed forwarding, inventory exports, packaging, and CI/cloud upload paths
remain unimplemented or unverified; see the [feature-parity ledger](feature-parity.md).

## AWS CloudWatch Logs

Threadforge can also generate an inert customer-managed Vector pack for a
pre-existing CloudWatch Logs group. It shares the same metadata-only projection
and explicit opt-in as S3; it does not forward Beacon-shaped raw events.

```powershell
threadforge endpoint cloudwatch print-config `
  --log-group /threadforge/runtime `
  --region us-east-1 `
  --allow-remote-metadata-forwarding

threadforge endpoint cloudwatch install-pack `
  --log-group /threadforge/runtime `
  --region us-east-1 `
  --output .\threadforge-cloudwatch-pack `
  --allow-remote-metadata-forwarding
```

The generated Vector config starts at end-of-file, uses the fixed stream name
`threadforge-metadata` (no host identifier), and emits only timestamp,
allowlisted severity, and the fixed Threadforge export marker defined by
[`metadata-export-v1.schema.json`](../schema/metadata-export-v1.schema.json). The CloudWatch
log group must already exist; the pack does not install Vector, create AWS
resources, access credentials, or make network requests. Supply AWS access to
Vector through its standard credential provider chain or host identity, and
configure retention, encryption, and access controls in AWS. Keep Vector's
data directory persistent so file positions survive restarts.

Tests parse the generated TOML and enforce the metadata allowlist, but do not
run Vector's VRL compiler or verify the CloudWatch sink against a particular
Vector release or AWS account. This remains partial Beacon parity until runtime
and destination verification can be performed.

## Google Cloud Storage

The GCS pack uses the same versioned metadata-only projection and explicit
remote-forwarding consent:

```powershell
threadforge endpoint gcs print-config `
  --bucket <bucket-name> `
  --prefix threadforge `
  --allow-remote-metadata-forwarding

threadforge endpoint gcs install-pack `
  --bucket <bucket-name> `
  --prefix threadforge `
  --output .\threadforge-gcs-pack `
  --allow-remote-metadata-forwarding
```

Only new runtime-log events are read; inventory snapshots and historical log
content are excluded. Object keys are date-partitioned and include a generated
UUID suffix to avoid overwrites. The generated GCS sink startup healthcheck is
disabled so a write-only object-creator identity need not have object-read
permissions. Supply credentials only to the customer-managed Vector process
through its supported identity mechanism, and configure bucket IAM, lifecycle,
retention, and encryption in Google Cloud. The pack includes a manual upload
example for its synthetic event only; Threadforge does not execute it.

Tests validate TOML and the output schema but do not run Vector's VRL/sink
implementation or verify a GCS bucket, credentials, or remote delivery. This
remains partial Beacon parity until those runtime checks and the remaining
destination/exporter matrix are completed.

## Splunk HTTP Event Collector

The Splunk HEC pack requires an HTTPS origin (host and optional port only),
rejects non-root paths, queries, fragments, and embedded user information, and
requires explicit metadata-forwarding consent:

```powershell
threadforge endpoint splunk-hec print-config `
  --endpoint https://splunk.example:8088 `
  --index threadforge `
  --allow-remote-metadata-forwarding

threadforge endpoint splunk-hec install-pack `
  --endpoint https://splunk.example:8088 `
  --index threadforge `
  --output .\threadforge-splunk-hec-pack `
  --allow-remote-metadata-forwarding
```

The token stays outside the pack in the Vector directory-secrets backend as
the single-line file `splunkhectoken` under `--secrets-dir` (default:
`<user-config-dir>/threadforge/vector-secrets`). Restrict that file and
directory to the Vector service identity; Threadforge never reads or stores
the token. Keep the generated pack private because its Vector config contains
the HEC origin. Create the selected index before activation; index
names must start with a lowercase letter or digit and use only lowercase
letters, digits, underscores, and hyphens (the reserved `kvstore` name text is
rejected). Fixed source and sourcetype labels are non-host-identifying, and the HEC event body
uses the same strict metadata schema as the other packs. Historical events,
inventory, and raw event content are not sent.

Tests cover HTTPS validation, token omission, TOML structure, and the strict
metadata projection. Vector 0.58.0 validates config, directory-secret
reference, sink schema, and VRL with component/health checks disabled. A
Splunk HEC endpoint and remote delivery were not tested; this is partial
Beacon parity.

## CrowdStrike Falcon HEC

Falcon HEC accepts the two connector URL paths documented by Beacon:
`/services/collector` and `/api/v1/ingest/hec`. The generated Vector HTTP sink
wraps the strict metadata record in a HEC envelope. The event contract is
[`metadata-export-v1.schema.json`](../schema/metadata-export-v1.schema.json);
the envelope contract is
[`falcon-hec-envelope-v1.schema.json`](../schema/falcon-hec-envelope-v1.schema.json).

```powershell
threadforge endpoint falcon-hec print-config `
  --endpoint https://<tenant>.ingest.<region>.crowdstrike.com/services/collector `
  --repository <optional-repository> `
  --allow-remote-metadata-forwarding

threadforge endpoint falcon-hec install-pack `
  --endpoint https://<tenant>.ingest.<region>.crowdstrike.com/services/collector `
  --output .\threadforge-falcon-hec-pack `
  --allow-remote-metadata-forwarding
```

The endpoint must be HTTPS, non-loopback, and contain no userinfo, query, or
fragment. The HEC token stays outside the pack in the Vector directory-secrets
backend as the single-line file `falconhectoken` under `--secrets-dir`
(default: `<user-config-dir>/threadforge/vector-secrets`). Restrict that file
and directory to the Vector service identity; Threadforge never reads or
stores the token. The pack uses fixed non-host-identifying source/sourcetype
labels, optionally includes a validated repository name, and never includes
host, user, action, harness, inventory, or raw event fields. Its manual `curl`
example sends only the synthetic sample file.

The generated config, HEC envelope schema, and VRL are validated with Vector
0.58.0 with component/health checks disabled. Falcon endpoint behavior, token
authentication, and delivery remain unverified; this is partial Beacon
parity.

## Elasticsearch

The Elasticsearch paths require an HTTPS origin (host and optional port only)
and explicit metadata-forwarding consent. Vector remains the default path:

```powershell
threadforge endpoint elasticsearch print-config `
  --endpoint https://elasticsearch.example:9200 `
  --index threadforge-metadata `
  --allow-remote-metadata-forwarding

threadforge endpoint elasticsearch install-pack `
  --endpoint https://elasticsearch.example:9200 `
  --index threadforge-metadata `
  --output .\threadforge-elasticsearch-pack `
  --allow-remote-metadata-forwarding

threadforge endpoint elasticsearch install-pack `
  --path filebeat `
  --endpoint https://elasticsearch.example:9200 `
  --log-path C:\\Users\\you\\.threadforge\\traces.jsonl `
  --metadata-log-path C:\\Users\\you\\.threadforge\\elasticsearch-metadata.jsonl `
  --output .\\threadforge-elasticsearch-filebeat-pack `
  --allow-remote-metadata-forwarding

threadforge endpoint elasticsearch install-pack `
  --path elastic-agent `
  --endpoint https://elasticsearch.example:9200 `
  --log-path C:\\Users\\you\\.threadforge\\traces.jsonl `
  --metadata-log-path C:\\Users\\you\\.threadforge\\elasticsearch-metadata.jsonl `
  --output .\\threadforge-elasticsearch-agent-pack `
  --allow-remote-metadata-forwarding
```

The endpoint rejects userinfo, non-root paths, queries, fragments, and
loopback destinations. The Vector sink writes a newly constructed
metadata-only document to the selected index. Its API key stays outside the
pack in the Vector directory-secrets backend as the single-line file
`elasticsearchapikey` under `--secrets-dir` (default:
`<user-config-dir>/threadforge/vector-secrets`). Restrict that file and
directory to the Vector service identity; the Threadforge CLI never reads or
stores the key. The pack starts at end-of-file,
does not backfill existing events, and includes only a synthetic
metadata-only sample. Create the index and configure its privileges,
retention, encryption, and access controls outside Threadforge.

The `filebeat` and `elastic-agent` paths write a local Vector sanitizer and a
credential-free native shipper config (`filebeat.yml` or
`elastic-agent-standalone.yml`). The shipper reads only the separate
`--metadata-log-path`; Vector reads the raw runtime log locally, starts at
end-of-file, reconstructs the strict metadata allowlist, and writes that
separate file. The metadata destination must be absolute, absent or empty,
non-symlinked, distinct from, and not hard-linked to the raw trace. Configure
shipper credentials through its secure external deployment mechanism;
generated configs do not contain username, password, or API-key values.
`--secrets-dir` and `--index` apply only to the direct Vector sink. Filebeat
and standalone Elastic Agent use the `logs-threadforge.endpoint-default` data
stream.

The vector, Filebeat, and Elastic Agent packs also include inert
`elasticsearch-upload-smoke-test.sh` and `.ps1` scripts. After an administrator
installs the generated data-stream template and `threadforge-metadata` ingest
pipeline, you can explicitly send one synthetic document:

```powershell
./elasticsearch-upload-smoke-test.ps1 -SendSyntheticEvent
```

Or, in a POSIX shell:

```sh
sh ./elasticsearch-upload-smoke-test.sh --send-synthetic-event
```

Supply either `ELASTIC_API_KEY` or both `ELASTIC_USERNAME` and
`ELASTIC_PASSWORD` from an external secret manager. The script checks that the
expected data stream already exists before posting the SHA-256-pinned
`sample-event.jsonl` through the metadata pipeline; it never reads runtime
traces, follows redirects, or displays response bodies. The PowerShell script
uses an in-process HTTP client; the POSIX script sends credentials to curl
through configuration stdin. Neither script writes credentials to disk. This
is a customer-triggered external write, so Threadforge's tests exercise it only
against a loopback fixture; no customer endpoint is contacted.

`print-config --path filebeat|elastic-agent` prints only the selected native
shipper YAML; `install-pack` also writes the required local `vector.toml`, a
synthetic sample, privacy instructions, and inert Elasticsearch/Kibana assets:
`ilm-policy.json`, component settings/mappings, a Vector index template, a
native data-stream template, an ingest pipeline, and `kibana-assets.ndjson`.
The ingest pipeline parses the timestamp, clamps severity to the export
allowlist, discards every other input field, and rebuilds the fixed
`threadforge.metadata-export.v1` / `raw_content=false` marker. The strict index
mapping rejects unexpected output fields.

The generated local test stack can be started with:

```powershell
threadforge endpoint elasticsearch up `
  --pack-dir .\threadforge-elasticsearch-pack `
  --allow-image-pulls

threadforge endpoint elasticsearch status --pack-dir .\threadforge-elasticsearch-pack
threadforge endpoint elasticsearch down --pack-dir .\threadforge-elasticsearch-pack
```

It uses pinned Elasticsearch, Kibana, Filebeat, and curl images; security is
disabled only inside this test stack, both published ports bind to loopback,
container networking is internal, and Filebeat reads only a synthetic fixture.
The CLI verifies the generated Compose/assets, rejects non-local Docker
contexts, and requires explicit acknowledgement before Docker may pull images.
Custom `--pack-dir` values receive a stable Compose project name derived from
the absolute pack path, so separate packs cannot stop or reuse one another's
containers/volumes; the documented default path keeps its existing project
name. `down` preserves the named data volume. `up` requires exact current
assets, while `status` and `down` also accept an older intact stack pack so a
Threadforge upgrade can inspect or stop it; these control operations still
require an unchanged Compose file and empty environment file.

On 2026-10-09, the local Docker Desktop stack completed synthetic Filebeat
ingestion and Kibana saved-object setup. Elasticsearch contained one document
with exactly `@timestamp`, `severity`, and `threadforge_export`, including
`raw_content=false`; Kibana returned the `threadforge-metadata-stream` data
view. The integration script now runs its assertions from disposable curl
containers attached only to the stack's internal Docker network, so it does
not depend on Windows/WSL host-loopback access. The full script passed from
WSL against Docker Desktop. The hosted Ubuntu workflow has not yet run, and
customer-cluster authentication/delivery remains unverified.

An administrator must review and explicitly install the assets on the cluster
and import the Kibana saved objects. Pack generation remains local and does
not edit host configuration, install/start services, resolve credentials, or
contact Elasticsearch. The data-stream ILM policy rolls over at 30 days or
50 GB per primary shard and deletes data after 90 days; review these defaults
against organizational retention policy before installation.

Tests cover URL validation, TOML/YAML/JSON/NDJSON structure, credential
omission, sample shape, strict field mappings, ingest allowlisting, and target
consistency. The optional Vector runtime-validation matrix includes the local
sanitizer config; Vector and standalone Elastic Agent executables were not
available for this run. Docker Compose 5.1.4 accepted the generated stack, and
the live local test above verified the synthetic Filebeat-to-Elasticsearch
path plus the Kibana data view. This validates only the isolated synthetic
stack; hosted CI execution, customer destination authentication/delivery,
and complete Beacon Elastic field parity remain open.

## Datadog Logs

The Datadog pack accepts the HTTPS Logs intake origin for your Datadog site and
requires explicit metadata-forwarding consent:

```powershell
threadforge endpoint datadog print-config `
  --endpoint <https-datadog-logs-intake-origin> `
  --allow-remote-metadata-forwarding

threadforge endpoint datadog install-pack `
  --endpoint <https-datadog-logs-intake-origin> `
  --output .\threadforge-datadog-pack `
  --allow-remote-metadata-forwarding
```

The endpoint must be HTTPS, non-loopback, and contain no userinfo, path,
query, or fragment. The API key stays outside the pack in the Vector
directory-secrets backend as the single-line file `datadogapikey` under
`--secrets-dir` (default: `<user-config-dir>/threadforge/vector-secrets`).
Restrict that file and directory to the Vector service identity; Threadforge
never reads or stores the key. The generated transform exports only timestamp,
allowlisted severity, and the fixed Threadforge privacy marker from new runtime
events. It does not export raw event content or Beacon's investigation fields.

Tests cover endpoint validation, TOML structure, external API-key reference,
synthetic sample shape, and metadata projection. Vector 0.58.0 validates config,
secret reference, sink schema, and VRL with component/health checks disabled.
The Vector pack also contains inert `datadog-upload-smoke-test.sh` and
`datadog-upload-smoke-test.ps1` scripts. They require an explicit
`--send-synthetic-event` or `-SendSyntheticEvent` switch and externally supplied
`DD_API_KEY`, verify the exact sibling sample SHA-256, and submit only that
sample to the configured origin's `/api/v2/logs` endpoint. They do not read
runtime JSONL or the Vector secrets file, do not follow redirects, and suppress
response bodies; success requires HTTP 202. A 202 confirms API acceptance for
processing, not indexing or retention. Regenerate the pack if its sample
timestamp is more than 18 hours old. The smoke scripts use the API-key
environment value only in their own process and never write it to disk.
PowerShell tests use a local HTTP fixture and POSIX tests use a mocked `curl`;
no customer endpoint was contacted, so real endpoint authentication and
delivery remain unverified.
A separate Datadog Agent path is generated as an inert metadata-only bridge:

```powershell
threadforge endpoint datadog install-pack `
  --path agent `
  --log-path C:\ProgramData\Threadforge\traces\events.jsonl `
  --metadata-log-path C:\ProgramData\Threadforge\datadog\metadata.jsonl `
  --output .\threadforge-datadog-agent-pack `
  --allow-remote-metadata-forwarding
```

The pack writes `vector.toml`, `datadog-agent-conf.yaml`, a synthetic sample,
and setup instructions. Vector reads new Threadforge runtime lines, sanitizes
them into a separate local JSONL file, and the Agent snippet tails only that
file. The metadata path must be absolute, have an existing non-symlinked parent
directory, differ from the raw log path, and be absent or empty when the pack
is generated. Do not reuse it for raw logs. The generated files do not enable
Agent log collection; use your existing Datadog Agent deployment to configure
global credentials/log collection, install the snippet under that Agent's
`conf.d/threadforge.d/` directory, and grant only the required Vector-write and
Agent-read access to the metadata file. Site, credentials, and host tags are
not read or written by Threadforge. Review the Agent's standard host metadata
and remote-delivery policy before starting it. After applying the config,
inspect Agent status for the `threadforge` log source and confirm only the
synthetic/metadata-only shape is present.

Tests validate generated YAML/TOML, raw-vs-metadata path separation, an
end-of-file-only source, strict metadata projection, no secret material, and
pack overwrite refusal. The Datadog Agent binary, installed-host permissions,
site/API-key resolution, and live delivery were not tested; Beacon's full event
field parity remains intentionally incomplete under Threadforge's
metadata-only forwarding boundary.

## Sumo Logic Hosted Collector

The Sumo pack uses a customer-managed Vector HTTP sink for a Sumo Logic Hosted
Collector HTTP Logs & Metrics Source. It requires the same explicit metadata
forwarding consent as other remote destinations:

```powershell
threadforge endpoint sumo print-config `
  --endpoint https://collectors.sumologic.com/receiver/v1/http `
  --auth-header `
  --allow-remote-metadata-forwarding

threadforge endpoint sumo install-pack `
  --endpoint https://collectors.sumologic.com/receiver/v1/http `
  --auth-header `
  --output .\threadforge-sumo-pack `
  --allow-remote-metadata-forwarding
```

The Auth Header URL is non-secret and ends exactly in `/receiver/v1/http`;
presigned URLs are rejected because their path is a bearer credential. The
token stays outside the pack in the Vector directory-secrets backend as the
single-line file `sumotoken` under the configured `--secrets-dir` (default:
`<user-config-dir>/threadforge/vector-secrets`). Restrict that file and
directory to the Vector service identity. Threadforge never opens or stores
the Vector token file. The generated sink sends it as `x-sumo-token`, preserves
newline-delimited JSON framing, tails only new runtime events, and applies the
strict metadata projection. The included sample event is synthetic and
contains no runtime-derived values. The pack also contains opt-in
`sumo-upload-smoke-test.sh` and `sumo-upload-smoke-test.ps1` scripts. They are
inert unless invoked with `--send-synthetic-event` or `-SendSyntheticEvent`;
they require `SUMO_TOKEN` to be supplied by an external secret manager, verify
the exact sample-file SHA-256, and post only that single synthetic record. They
never read the runtime log or Vector token file, do not follow redirects, and
suppress the response body. The shell script sends the token to curl over its
configuration stdin rather than as a process argument or temporary file; the
PowerShell script reads `SUMO_TOKEN` into its own process memory and sends it
through an in-process HTTP client. Neither script writes the token to disk,
and the `threadforge` CLI is not involved in the upload.

Pack generation is local-only and does not install or start Vector, read
environment variables, connect to Sumo, or upload a sample. To deliberately
send the synthetic event, first make `SUMO_TOKEN` available from your approved
secret manager, then run the generated script yourself:

```powershell
./threadforge-sumo-pack/sumo-upload-smoke-test.ps1 -SendSyntheticEvent
```

Or, in a POSIX shell:

```sh
sh ./threadforge-sumo-pack/sumo-upload-smoke-test.sh --send-synthetic-event
```

This sends one synthetic record and reports only the HTTP status. It is not run
by Threadforge's tests. Generated Vector configs were validated with Vector
0.58.0 using `validate --no-environment`; this checks config, secret
references, sink schema, and VRL but not destination health or delivery. No
live Sumo authentication or delivery has been verified. The generated Vector
config includes the selected local trace path, so keep the pack private.

## Rapid7 InsightIDR Custom Logs

The Rapid7 pack uses a customer-managed Vector HTTP sink for a Custom Logs
event source configured for Webhook collection:

```powershell
threadforge endpoint rapid7 print-config `
  --allow-remote-metadata-forwarding `
  --allow-vector-env-interpolation

threadforge endpoint rapid7 install-pack `
  --output .\threadforge-rapid7-pack `
  --allow-remote-metadata-forwarding `
  --allow-vector-env-interpolation
```

The generated pack contains no webhook URL. Provide the bearer URL from the
Rapid7 source only to the Vector process as
`THREADFORGE_RAPID7_WEBHOOK_URL` through an approved external secret or
service-environment manager. Threadforge never reads or stores the URL. Since
Vector 0.57+ disables environment interpolation by default, start Vector with
`--dangerously-allow-env-var-interpolation` (or set
`VECTOR_DANGEROUSLY_ALLOW_ENV_VAR_INTERPOLATION=true`) after reviewing the
broader interpolation behavior. This is a separate acknowledgement because
the webhook URL is a bearer credential. Restrict who can inspect the Vector
process environment. The sink sends newline-delimited JSON with
`Content-Type: application/x-ndjson`, tails only new runtime events, and
applies the strict metadata-only projection. The sample event is synthetic.

Pack generation is local-only and does not install or start Vector, read
environment variables, connect to Rapid7, or upload data. Generated config
was validated with Vector 0.58.0 with only the documented env-interpolation
flag and no component/health checks, using a synthetic URL; this does not
verify Rapid7 authentication or delivery. NDJSON framing, EOF reading, and the
metadata allowlist are tested. Beacon's one-shot raw-log upload smoke-test flow
is intentionally not ported because it would cross Threadforge's local-raw-trace
boundary.

## Wazuh local metadata ingestion

The Wazuh pack creates a local two-stage handoff: customer-managed Vector tails
Threadforge's raw local runtime log, transforms each event into the shared
metadata-only export, and writes a separate JSONL file; Wazuh tails only that
sanitized output. It does not configure Wazuh to read the raw trace.

Choose a new absolute metadata output path whose parent directory already
exists, then generate the pack:

```powershell
threadforge endpoint wazuh install-pack `
  --metadata-log-path C:\ProgramData\Threadforge\wazuh\metadata.jsonl `
  --allow-remote-metadata-forwarding `
  --output .\threadforge-wazuh-pack
```

The pack contains `vector.toml`, `ossec-localfile.xml`,
`threadforge-rules.xml`, a synthetic `sample-event.jsonl`, and a README. Run
Vector yourself and add the Wazuh localfile snippet on the same machine. Review
the generated rule IDs `119500`-`119560` for conflicts. They classify only the
exported severity label; they do not detect threats or authorize actions.
Restrict the source trace to Vector and grant Wazuh access only to the
sanitized metadata file. Although this file remains local at first, Wazuh may
forward it to a manager, so the command requires explicit metadata-export
consent.

Vector 0.58.0 validates the generated file-sink, source, and VRL configuration
with component/health checks disabled. XML and rule-contract tests pass; no
Wazuh manager/logtest or downstream forwarding was available for runtime
validation. The pack intentionally omits Beacon's full event fields and
dashboard-column helper to preserve Threadforge's raw-trace privacy boundary.

## Microsoft Sentinel

Sentinel supports two explicit paths, both restricted to the same four-field
metadata contract. Neither path sends the raw runtime event or creates Azure
resources. The default `--path vector` uses the Logs Ingestion API:

```powershell
threadforge endpoint sentinel install-pack `
  --path vector `
  --endpoint https://threadforge-dce.eastus.ingest.monitor.azure.com `
  --dcr-immutable-id dcr-0123456789abcdef0123456789abcdef `
  --tenant-id 01234567-89ab-cdef-0123-456789abcdef `
  --client-id abcdef01-2345-6789-abcd-ef0123456789 `
  --allow-remote-metadata-forwarding `
  --output .\threadforge-sentinel-pack
```

The optional `--path ama` mode first sanitizes the local runtime log to a
separate metadata JSONL file, then configures Azure Monitor Agent (AMA) to read
only that file:

```powershell
threadforge endpoint sentinel install-pack `
  --path ama `
  --metadata-log-path C:\ProgramData\Threadforge\sentinel\metadata.jsonl `
  --allow-remote-metadata-forwarding `
  --output .\threadforge-sentinel-ama-pack
```

For `ama`, the metadata path must be absolute, its parent must already exist,
and it must differ from the raw source path. Run the generated
`vector-local.toml`, restrict the output file so Vector can write and AMA can
read it, deploy `dcr-template.json`, and associate the DCR with the machine.
Vector writes flat newline-delimited JSON with exactly `TimeGenerated`,
allowlisted `Severity`, `SchemaVersion`, and `RawContent=false`; the DCR uses
the Custom JSON Logs format and declares those four columns. Its file pattern
points to the sanitized metadata path, never the raw runtime log. `--path both`
creates both independent Vector configs and DCR templates; do not activate both
for the same source events unless duplicate ingestion is intended.
`print-config --path both` is refused because it has two configs, so use
`install-pack` and inspect each file.

Both paths write the `ThreadforgeAgentMetadata_CL` four-column contract:
`TimeGenerated`, allowlisted `Severity`, fixed `SchemaVersion`, and
`RawContent=false`. Only the direct Vector path requires the app registration
IDs and `azureclientsecret` file in Vector's directory-secrets backend
(`--secrets-dir`, default `<user-config-dir>/threadforge/vector-secrets`).
Threadforge never accepts or reads the client-secret value. For that path,
deploy `dcr-logs-ingestion-template.json`, grant the app Monitoring Metrics
Publisher on the DCR, and configure persistent Vector state.

Vector 0.58.0 validated both the direct Azure sink and local metadata file
sanitizer with `validate --no-environment`; these checks made no Azure request.
The AMA DCR, Azure resource deployment, role assignment, authentication, and
live ingestion remain unverified. The pack's strict schema is intentionally
narrower than Beacon's rich event table.
