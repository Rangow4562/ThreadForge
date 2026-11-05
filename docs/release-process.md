# Release-candidate build process

Threadforge does not publish a GitHub Release automatically. A stable `vMAJOR.MINOR.PATCH` tag starts the release-candidate jobs in `threadforge-tests.yml`, but only after the complete six-cell OS/Python test matrix and the Elasticsearch synthetic-ingestion job succeed. The tagged commit must be reachable from `main`. To exercise the release pipeline without creating a tag, manually run the workflow from `main` with `release_version` set to a stable `MAJOR.MINOR.PATCH`; this produces a rehearsal candidate tied to that tested `main` commit, not a tag-sourced production release.

The build creates the currently supported CLI artifacts and requires a separate six-cell Python evidence matrix (Linux, Windows, and macOS on Python 3.11 and 3.12) to succeed before candidate assembly:

- `threadforge_VERSION_linux_amd64.tar.gz`
- `threadforge_VERSION_darwin_amd64.tar.gz`
- `threadforge_VERSION_darwin_arm64.tar.gz`
- `threadforge_VERSION_windows_amd64.zip`

The version command is linked to the tag version. Archives normalize timestamps, ownership, and metadata where the format permits, and are checked to contain exactly one CLI binary; the Windows ZIP uses a stored entry to keep the archive byte-stable across compression-library versions. Each binary receives an SPDX JSON SBOM and a separate target-specific `threadforge_VERSION_GOOS_GOARCH.go-licenses.tar.gz` evidence bundle. The Go evidence uses pinned `go-licenses` v2.0.1 to save recognized license/notice files and a package/license-URL/license-expression report for `./cmd/threadforge` under that target's `GOOS`/`GOARCH`; unknown license expressions fail the candidate. The bundle records the source commit, scanner binary hash, notice-file hashes, unknown source-URL count, and native-code inspection warnings. These scanner classifications are unreviewed; the bundle sets `release_clearance=false` and is not legal approval.

For Python, the workflow syncs the locked `test`, `decisions`, and `workspace` extras on each matrix runner, then emits one `threadforge_VERSION_python_OS_pyPYTHON.license-evidence.tar.gz` archive per OS/Python pair. The archive binds the lockfile and project-file hashes, records installed package metadata and safe license fields, and retains bounded license/notice files with per-file hashes. The collector source SHA-256 normalizes CRLF to LF so it is identical across Windows and Unix checkouts. Installed packages not represented in `uv.lock`, unsafe paths, and oversized evidence fail the job. The archives are deterministic and individually provenance-attested. They remain unreviewed evidence (`release_clearance=false`), and do not cover the other Python extras, the Engineering container/runtime, or benchmark dataset/evaluator terms. See the [license audit](license-audit.md) for remaining scope and legal-review gaps.

If Syft installation or the first SBOM generation attempt fails, the pinned action is retried once; a second failure stops the candidate before attestation or upload. GitHub creates build-provenance attestations for each archive, SBOM, and Go/Python license-evidence bundle, a distinct SPDX SBOM attestation binding each binary archive to its SBOM, and a build-provenance attestation for the combined `checksums.txt`. Candidate assembly requires exactly 18 versioned payloads (4 CLI archives, 4 SPDX SBOMs, 4 Go evidence archives, and 6 Python evidence archives). All payloads are checksummed in the combined candidate bundle. The workflow uploads these files as a 14-day Actions artifact and has no `contents: write` permission, so it cannot publish or edit a public release.

Before attestation, `scripts/verify_release_sbom.py` reconciles each SPDX package set to that exact binary's `go version -m` output. It requires every embedded Go module, checks non-replaced module versions and the Go standard-library version, and rejects unexplained SBOM packages. Its JSON summary counts package license declarations/conclusions that remain unasserted and always sets `license_review_required=true` and `release_clearance=false`. This verifies Go-binary component coverage only; it does not classify licenses, review redistribution terms, cover Python/container runtime dependencies, or grant legal approval. Those release blockers remain tracked in [the dependency and license audit](license-audit.md).

After downloading the candidate artifact, verify the checksums and attestations before considering publication:

```bash
sha256sum --check checksums.txt
gh attestation verify threadforge_VERSION_linux_amd64.tar.gz --repo Rangow4562/ThreadForge
gh attestation verify threadforge_VERSION_linux_amd64.tar.gz --repo Rangow4562/ThreadForge --predicate-type https://spdx.dev/Document/v2.3
gh attestation verify threadforge_VERSION_linux_amd64.spdx.json --repo Rangow4562/ThreadForge
gh attestation verify threadforge_VERSION_linux_amd64.go-licenses.tar.gz --repo Rangow4562/ThreadForge
gh attestation verify checksums.txt --repo Rangow4562/ThreadForge
```

Repeat attestation verification for each platform archive, matching SBOM, and matching `.go-licenses.tar.gz` evidence bundle. A passing build workflow is artifact evidence only: it does not satisfy the cutover assessor, approve licenses/privacy/security/name review, or authorize publication. Release creation and asset attachment remain a separate human-approved operation after every documented parity and cutover gate passes. The existing `v0.4.6` assets predate this workflow and are not retroactively attested.
