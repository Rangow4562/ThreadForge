# Public release status

Status checked **November 5, 2026**: GitHub's latest public stable release is `v0.4.6`, published October 30 from tag commit `a5b9e73436876038fb7d3e40268eece298af4c7b`. The current `main` commit is `79a39c927791b1b18b0ddf231d5210a2550a525d`, which is newer than that tag; installers that select the latest release install the `v0.4.6` binary, not current `main`.

GitHub marks `v0.4.6` as a stable release, but Threadforge has **not** passed the all-or-nothing production/cutover acceptance gate. Its publication is not evidence that Beacon, Mancode, AnyJev, or RRSI can be retired or replaced. Do not use this release as authorization to cut over or delete legacy data.

The release includes `checksums.txt`, but the checksum is published alongside the archives and is not a publisher signature or independent build-provenance attestation. The repository currently has no dedicated automated release-build workflow; the successful tag test workflow verifies code, not that it produced the uploaded release assets.

The remaining release gates are tracked in the [cutover assessment](cutover.md), [feature-parity matrix](feature-parity.md), and [license audit](license-audit.md). They include complete documented feature and host parity; authoritative populated-source migration, reconciliation, and rollback; independent trusted held-out efficacy results; release-artifact SBOM and license/privacy/security/name approvals; and a rollback rehearsal with explicit cutover approval. The dependency inventory still reports `release_clearance=false`.

Do not publish a replacement release or retire legacy systems until the cutover assessor accepts the full evidence set and release, security, and license approvals are recorded.
