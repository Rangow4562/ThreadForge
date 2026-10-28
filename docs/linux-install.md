# Linux CLI installation

Threadforge publishes a Linux `amd64` CLI archive. The user-local installer downloads the selected GitHub release, verifies the archive against that release's `checksums.txt`, rejects archives that contain anything except the single top-level `threadforge` binary, and installs it under `$HOME/.local/bin` without `sudo` or account login.

From a Threadforge checkout:

```bash
# Defaults to the latest release. Pin a reviewed version when reproducibility matters.
bash packaging/linux/install.sh
THREADFORGE_VERSION=0.4.5 bash packaging/linux/install.sh

# Optional absolute destination instead of $HOME/.local/bin.
THREADFORGE_INSTALL_DIR="$HOME/bin" THREADFORGE_VERSION=0.4.5 bash packaging/linux/install.sh
```

The installer currently supports Linux `x86_64`/`amd64`, matching the published release asset. It refuses other operating systems and architectures rather than guessing an asset. The install directory must be absolute; the existing `threadforge` executable is replaced atomically after the downloaded binary passes its version check.

The checksum is downloaded from the same GitHub release as the archive. It detects transfer/corruption errors, but is not a publisher signature or independent proof of release provenance. Review and pin a release version when that distinction matters.

This installer installs the Go CLI only. Python decision/evolution operations require the Python package and any selected optional runtime extras; harness adapters and browser extensions are packaged separately. See [Python modules](../python/README.md), the [TypeScript adapter package](../adapters/package.json), and the [release parity ledger](feature-parity.md).
