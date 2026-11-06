# Dependency and license audit status

Status: **open; not legal clearance for redistribution.** This workpaper records the repository inventory checked on **November 6, 2026**. It separates machine-readable lock metadata from actual license review; a package's declared license field or a successful build does not by itself establish compatibility.

The fresh `scripts/generate_dependency_inventory.py --repo-root .` inventory on November 6, 2026 contains 931 components across 10 manifests. Its 477 `missing_declared_license_metadata` entries mean **license metadata was not collected**, not that those components are unlicensed. Inventory v3 breaks the missing declarations down as 77 Go module records and 400 Python package records; it does not infer licenses. It reports 0 `unresolved_versions`: the 277 external `python/uv.lock` package records have resolved versions and artifact hashes, while the 32 direct source requirements are retained on their locked records with pyproject manifest hashes. All 454 npm lock entries have a nonempty declared license field, including the new pinned `yaml` parser dependency used by Aider's explicit project-config adapter. Every inventory entry still has `license_review_status=unreviewed`, and `release_clearance=false`.

Uncollected license metadata by source manifest: `containers/engineering/build-requirements.lock` (pypi): 3, `containers/engineering/requirements-linux-x86_64-py312.lock` (pypi): 120, `go.mod` (golang): 77, `python/uv.lock` (pypi): 277.

## Inventory observed

| Area | Current evidence | Remaining review |
|---|---|---|
| Threadforge-authored code | Top-level `LICENSE` and `NOTICE` declare the Apache-2.0 project boundary. | Confirm every distributed file is either covered by this license or has its upstream notice retained. |
| Go modules | The latest `go list -m all` reports 78 module records including the Threadforge main module (77 external), including `github.com/klauspost/compress` v1.20.3; versions and checksums are in `go.mod`/`go.sum`. Inventory v3 reports 77 module-graph components with license metadata not collected. | `THIRD_PARTY.md` describes selected modules, not a reviewed license disposition for every transitive module. Produce a complete module-to-SPDX/license-text inventory for each release graph. |
| Adapter npm dependencies | `adapters/package-lock.json` contains 4 package entries and the browser-extension lock contains 431; all 435 have nonempty license metadata in the lockfiles. | Verify license expressions, bundled notices, and actual production dependency inclusion; metadata presence is not legal review. |
| Engineering container Python runtime | `containers/engineering/requirements-linux-x86_64-py312.lock` pins 120 runtime distributions with hashes; `build-requirements.lock` pins 3 build distributions. Inventory v3 separately counts these as 120 and 3 license-metadata gaps; it does not infer package licenses from names or hashes. | The container/base-image and transitive package licenses have not been reviewed. The local image was built and passed a network-disabled, read-only, non-root import/permission smoke on October 9; that is runtime evidence, not license clearance. |
| Python package extras | `python/pyproject.toml` defines `decisions`, `hf`, `serving`, `typed-data`, `evolution`, `workspace`, and `test` extras. Inventory v3 expands `python/uv.lock` into 277 exact external package/version records with SHA-256 artifacts and supported resolution markers; all 277 lack collected license metadata. Each of the 32 direct requirement entries retains its group, sanitized requirement string, and `pyproject.toml` path/SHA-256 binding. It reports 0 unresolved component versions (`unresolved_versions`). Required tests exercise `test`, `decisions`, and `workspace`; the release-candidate evidence matrix installs all seven declared extras on Ubuntu, Windows, and macOS for Python 3.11 and 3.12. This proves installation for those locked target environments only, not that every extra's behavior works. The `serving` extra selects `vllm` only on Linux/Python 3.11 and selects `transformers` elsewhere. | Review dependency licenses and compatibility separately for every shipped extra. Installed-file evidence and a green test matrix are not license review or behavioral validation. Run dedicated runtime/behavior checks for each shipped extra. |
| Vendored source | Notices currently exist for Beacon, AnyJev, RRSI, Archipelago, and the Harbor Terminus-2 fork; file-level scope and modifications are described in `THIRD_PARTY.md` and `python/UPSTREAM_MODIFICATIONS.md`. | Reconcile every vendored file and generated distribution against required copyright, license, and modification notices. Review excluded RRSI `third_party/` and external benchmark/evaluator components before adding them. |
| Mancode-derived contracts | No Mancode implementation source or documentation prose is intentionally copied; the contract-derived importer is identified in `THIRD_PARTY.md`. | Obtain a clean-room/license review before distribution. The source project's AGPL-3.0-only terms make this a release blocker even if tests pass. |
| Benchmark datasets and external runtimes | Threadforge stores source commitments and metadata for selected datasets; task rows and benchmark datasets are not bundled. | Review each dataset/evaluator's execution and redistribution terms before enabling real benchmark runs or shipping data. |

The npm counts are a snapshot of lockfile fields, not a claim that all licenses have been classified. Likewise, Go module checksums and Python artifact hashes prove byte identity, not license compliance. The universal Python lock and the Engineering container's platform-specific requirements locks cover separate installation surfaces.

## Repeatable dependency inventory

Run `python scripts/generate_dependency_inventory.py --repo-root .` to print the inventory, or add `--output <new-file>` to write a JSON file. The versioned `threadforge.dependency-inventory.v3` contract records hashes for its input manifests and components from the resolved Go module graph, both npm lockfiles, the Engineering runtime/build Python locks, and every external package/version record in `python/uv.lock`. Python lock resolution markers and package artifact hashes are retained; direct pyproject requirements are attached to their resolved package records. Registry URLs and machine-local paths are omitted. v3 adds deterministic missing-license-metadata counts by ecosystem and source manifest without inferring licenses. The v1 and v2 schemas remain available for earlier inventory payloads.

The report marks every component `license_review_status=unreviewed`; missing license metadata stays missing, automatic license inference is false, and `release_clearance` is always false. The direct pyproject ranges remain flexible for unlocked installs, while inventory v3 records the exact locked versions used by CI. This is still not a complete release-artifact SBOM, license classification, or legal approval. `test.ps1` generates it only under a unique temporary path.

Release-candidate builds add separate per-target Go license-evidence archives using pinned `go-licenses` v2.0.1 against `./cmd/threadforge` with the candidate's `GOOS`/`GOARCH`. The collector saves the scanner's report and notice files with source-commit, scanner-binary, and per-file hashes; it fails on an unknown license expression, records missing source URLs and native-code inspection warnings, and always sets `license_review_status=unreviewed` and `release_clearance=false`. The [scanner project's README](https://github.com/google/go-licenses) states it is not an officially supported Google product; its result is machine-collected evidence, not Google endorsement or legal review.

The release-candidate workflow collects Python license evidence from six locked environments: Linux, Windows, and macOS on Python 3.11 and 3.12, each installing all seven extras declared in `python/pyproject.toml`. New candidates emit `threadforge.python-license-evidence.v2`; the v1 schema remains unchanged for older raw-byte input hashes. `scripts/build_python_license_evidence.py` records the source commit, candidate version, platform/runtime, selected extras, `uv.lock`/`pyproject.toml` hashes, installed package metadata hashes, safe license-expression/classifier fields, and hashes plus copies of discovered license/notice files. The collector-source and v2 input-manifest hashes normalize CRLF to LF so their identities are consistent across Windows and Unix checkouts; the source commit still binds them to the committed files. It rejects installed distributions absent from the lock and unsafe/oversized paths or payloads. Missing metadata/files stay explicitly missing; it does not infer a license. Each deterministic archive is provenance-attested and included in the combined candidate checksums. The collector remains `license_review_status=unreviewed`, `license_review_required=true`, and `release_clearance=false`.

Verified November 6, 2026: non-publishing version `0.5.2` rehearsal run `42189214107` completed successfully from source commit `94c178b5a986f6e9fc44c613f9b312c24a718c2c`. All six Python evidence bundles include all seven extras and report zero unlocked distributions and zero malformed license metadata. The per-environment readback still found distributions without captured license metadata or without a discovered license file:

| Target | Installed distributions | Without captured license metadata | Without discovered license files |
|---|---:|---:|---:|
| Linux, Python 3.11 | 253 | 6 | 14 |
| Linux, Python 3.12 | 161 | 3 | 7 |
| Windows, Python 3.11 | 146 | 2 | 7 |
| Windows, Python 3.12 | 146 | 2 | 7 |
| macOS, Python 3.11 | 142 | 2 | 6 |
| macOS, Python 3.12 | 142 | 2 | 6 |

“Without captured license metadata” means the installed distribution has no license expression, license classifier, or legacy license field; “without discovered license files” means the collector found no matching license/notice file in that distribution. Neither count proves that a package is unlicensed. Independent verification confirmed all 18 payload hashes in `checksums.txt`, all 19 SLSA provenance attestations (18 payloads plus the checksum file), and all four archive-to-SPDX attestations against the expected repository, workflow, branch, and source commit. The workflow created no public release. This candidate evidence does not populate the source inventory's missing metadata, classify license compatibility, or change `release_clearance=false`.

The six candidate manifests identify these 19 distinct installed distributions for targeted review. `L3.11`/`L3.12`, `W3.11`/`W3.12`, and `M3.11`/`M3.12` mean Linux, Windows, and macOS with the indicated Python version. A dash means that dimension had no reported gap for that distribution in the six profiles:

| Distribution/version | No captured license metadata | No discovered license/notice file |
|---|---|---|
| `cuda-tile` 1.7.0 | — | L3.11 |
| `cuda-toolkit` 13.0.3.0 | L3.11 | L3.11 |
| `cuda-toolkit` 13.2.2 | L3.12 | L3.12 |
| `fastmcp-slim` 4.1.4 | — | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `flatbuffers` 25.12.19 | — | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `loguru` 0.7.3 | — | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `model-hosting-container-standards` 0.1.16 | — | L3.11 |
| `nvidia-ml-py` 13.615.71 | — | L3.11 |
| `opentelemetry-semantic-conventions-ai` 0.5.1 | — | L3.11 |
| `oss-harmony` 0.0.11 | L3.11 | — |
| `pymupdf-layout` 2.0 | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `pymupdf4llm` 2.0 | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `quack-kernels` 0.6.5 | L3.11 | — |
| `ruff` 0.15.1 | — | W3.11, W3.12 |
| `sentencepiece` 0.2.2 | — | L3.11 |
| `supervisor` 4.3.0 | — | L3.11 |
| `tokenizers` 0.23.3 | — | L3.11, L3.12, W3.11, W3.12, M3.11, M3.12 |
| `torchcodec` 0.17.0 | L3.11 | — |
| `z3-solver` 4.15.4.0 | — | L3.11 |

This is a reviewer work queue derived from installed wheel metadata and `RECORD` file listings, not an inferred license list. Review each distribution against its authoritative source and release artifact; do not treat metadata fields alone as proof of license terms.

This Python evidence is a readback of only those six installed environments; it does **not** populate the lockfile inventory's missing Python metadata entries, establish the license of a package from its notice text, or cover the Engineering container/runtime or benchmark datasets/evaluators. Successful installation of the `serving` extra on Linux/Python 3.11 is not evidence that vLLM inference or GPU/runtime behavior works. Neither the Go nor Python collectors review compatibility or grant legal approval.

## Release gate

Before distribution, generate a complete transitive SBOM/license inventory for the exact release artifacts and supported platform/runtime combinations; reconcile it against retained notices; review the Mancode-derived contract boundary and every dataset/evaluator; and obtain independent legal approval. Keep the all-or-nothing cutover closed until those checks, full feature parity, migration reconciliation, and held-out efficacy gates pass.

See [`THIRD_PARTY.md`](../THIRD_PARTY.md) for pinned upstream revisions, reuse scope, and retained notice locations.
