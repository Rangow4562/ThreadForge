# Release-candidate build process

Threadforge does not publish a GitHub Release automatically. A stable `vMAJOR.MINOR.PATCH` tag starts the release-candidate jobs in `threadforge-tests.yml`, but only after the complete six-cell OS/Python test matrix and the Elasticsearch synthetic-ingestion job succeed. The tagged commit must be reachable from `main`. To exercise the release pipeline without creating a tag, manually run the workflow from `main` with `release_version` set to a stable `MAJOR.MINOR.PATCH`; this produces a rehearsal candidate tied to that tested `main` commit, not a tag-sourced production release.

The build creates the currently supported CLI artifacts:

- `threadforge_VERSION_linux_amd64.tar.gz`
- `threadforge_VERSION_darwin_amd64.tar.gz`
- `threadforge_VERSION_darwin_arm64.tar.gz`
- `threadforge_VERSION_windows_amd64.zip`

The version command is linked to the tag version. Archives normalize timestamps, ownership, and metadata where the format permits, and are checked to contain exactly one CLI binary; the Windows ZIP uses a stored entry to keep the archive byte-stable across compression-library versions. Each binary receives an SPDX JSON SBOM. GitHub creates a build-provenance attestation for each archive, a distinct SPDX SBOM attestation binding each archive to its SBOM, and separate build-provenance attestations for each SBOM file and the combined `checksums.txt`. The workflow uploads these files as a 14-day Actions artifact and has no `contents: write` permission, so it cannot publish or edit a public release.

After downloading the candidate artifact, verify the checksums and attestations before considering publication:

```bash
sha256sum --check checksums.txt
gh attestation verify threadforge_VERSION_linux_amd64.tar.gz --repo Rangow4562/ThreadForge
gh attestation verify threadforge_VERSION_linux_amd64.tar.gz --repo Rangow4562/ThreadForge --predicate-type https://spdx.dev/Document/v2.3
gh attestation verify threadforge_VERSION_linux_amd64.spdx.json --repo Rangow4562/ThreadForge
gh attestation verify checksums.txt --repo Rangow4562/ThreadForge
```

Repeat attestation verification for each platform archive and its matching SBOM. A passing build workflow is artifact evidence only: it does not satisfy the cutover assessor, approve licenses/privacy/security/name review, or authorize publication. Release creation and asset attachment remain a separate human-approved operation after every documented parity and cutover gate passes. The existing `v0.4.6` assets predate this workflow and are not retroactively attested.
