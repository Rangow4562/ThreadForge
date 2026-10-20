# Reuse inventory

## Source repositories

The initial source trees were clean when inspected. Work in this repository must not modify them.

| Source | Useful capability | License boundary | Initial action |
|---|---|---|---|
| `agent-beacon` | Normalized event/trace contracts, capture adapters, MCP, memory review, exporters, threat rules | MIT; preserve copyright and license | Event contracts and CEL threat-rule engine/rule corpus are vendored; bounded local scan and an independently authored S3 Vector pack are integrated. The S3 pack follows documented local-JSONL handoff behavior but copies no Beacon pack source/prose. Other host adapters, exporter destinations, managed forwarding, managed rule lifecycle, and broad security views remain to be ported |
| `AnyJev` | Typed decision levels, calibration, routing, model backends | Apache-2.0; preserve license and notices | Vendor package source with provenance; keep heavyweight model dependencies optional |
| `rrsi` | Candidate proposal/critique, history, calibrated selection, domain evaluation | Apache-2.0; preserve license and notices | Vendor engine source; audit domain and benchmark dependencies separately |
| `mancode` | Project/task governance, privacy, versioned context, team workflow | AGPL-3.0-only | Do not copy source; implement from this product contract and new tests |
| Procedural Graphs paper | Local procedural guidance and validation-gated evolution | No paper text/code reuse | Cite as a design reference in project documentation |

## Additional adapter implementation evidence

Claude Code, Cursor, OpenCode, Oh My Pi, and OpenClaw's documented hook/event surfaces and Beacon's published mappings were used to define test cases for Threadforge's new TypeScript adapters. No Beacon host-adapter source was copied into these modules. The adapters retain bounded metadata and content digests; live-host compatibility is still an explicit release gate.

## Required audit before cutover

- Reconcile all copied paths against `THIRD_PARTY.md` and upstream notices.
- Produce an SBOM and scan dependency licenses, including platform plugins and benchmark runners.
- Do not copy model weights, task data, benchmark datasets, user credentials, or private project records as source assets.
- Retain original source repositories and user data until migration acceptance and rollback sign-off.
