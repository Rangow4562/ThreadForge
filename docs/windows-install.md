# Windows CLI installation

> [!WARNING]
> The latest downloadable CLI is `v0.5.0` and is not production/cutover-approved. Review the [public release status](release-status.md) before installing; this binary does not authorize replacing or retiring legacy systems.

Threadforge publishes a Windows `amd64` CLI ZIP. From a Threadforge checkout, run the user-local installer in PowerShell 7:

```powershell
# Defaults to the latest stable GitHub release.
pwsh -NoProfile -File .\packaging\windows\install.ps1

# Pin a reviewed release when reproducibility matters.
pwsh -NoProfile -File .\packaging\windows\install.ps1 -Version 0.5.0

# Optional absolute destination instead of %LOCALAPPDATA%\Programs\Threadforge.
pwsh -NoProfile -File .\packaging\windows\install.ps1 -Version 0.5.0 -InstallDir 'D:\Tools\Threadforge'
```

The installer requires `curl.exe`, supports Windows `amd64`, and makes no administrator, account-login, PATH, credential, or host-configuration changes. New candidate ZIPs contain `threadforge.exe`, `LICENSE`, `NOTICE`, `THIRD_PARTY.md`, and `THIRD_PARTY_LICENSES.tar.gz`; the installer also accepts the historical binary-only and published-v0.5.0 layouts. It downloads only from GitHub over HTTPS, verifies the archive against the release's `checksums.txt`, rejects unknown, oversized, link, and reparse-point ZIP entries, runs the staged CLI's exact version command, and only then atomically replaces `threadforge.exe`. If verification fails, an existing installation is left in place. Add the chosen install directory to PATH yourself if desired.

The checksum is downloaded from the same GitHub release as the archive. It detects transfer/corruption errors, but is not a publisher signature or independent proof of release provenance. Review and pin a release version when that distinction matters.

This installer installs the Go CLI only. Python decision/evolution operations require the Python package and any selected optional runtime extras; harness adapters and browser extensions are packaged separately. See [Python modules](../python/README.md), the [TypeScript adapter package](../adapters/package.json), and the [release parity ledger](feature-parity.md).
