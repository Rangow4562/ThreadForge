# Browser extension release candidate

The main-based release rehearsal builds the browser extension after all required platform suites and the source-policy job pass. The separate Actions artifact is named `threadforge-browser-extension-VERSION` and contains:

- `threadforge_browser_extension_VERSION_chromium.zip`
- `threadforge_browser_extension_VERSION_firefox.zip`
- Chromium and Firefox SPDX inventories generated from their respective build directories
- An SPDX inventory from the locked npm build dependency graph
- `checksums.txt`

Each archive is attested with its matching SPDX inventory; the dependency SPDX and checksum file are also attested. The workflow checks out `${{ github.sha }}` with persisted credentials disabled and verifies the checkout SHA before building. Candidate assembly depends on this job, so a failed or missing browser package blocks the candidate. The browser ZIPs are deliberately not added to the exact 27-payload CLI/Python bundle.

Download the artifact from the same successful `threadforge-tests.yml` candidate run. Verify its listed hashes and the GitHub attestations before inspection:

```bash
sha256sum --check checksums.txt
gh attestation verify threadforge_browser_extension_VERSION_chromium.zip --repo Rangow4562/ThreadForge
gh attestation verify threadforge_browser_extension_VERSION_firefox.zip --repo Rangow4562/ThreadForge
gh attestation verify threadforge_browser_extension_VERSION_build_dependencies.spdx.json --repo Rangow4562/ThreadForge
gh attestation verify checksums.txt --repo Rangow4562/ThreadForge
```

This is a 14-day Actions artifact in a public repository, not an access-controlled channel. The ZIPs are unsigned review packages, are not uploaded to Chrome Web Store or Mozilla Add-ons, and do not establish live browser compatibility or legal clearance. SBOMs retain `NOASSERTION` where evidence does not support a license conclusion; the existing dependency/legal review gates still control distribution.
