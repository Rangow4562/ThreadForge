# Engineering runtime image

This image is the preloaded runtime for the opt-in Engineering adapter. It
contains the Threadforge Python package with the `workspace` dependencies, the
Apache-licensed Archipelago runner and `harness_eng`, and no benchmark tasks,
evaluator checkout, model weights, credentials, or result data.

The recipe targets `linux/amd64` and Python 3.12. It uses the exact base image in
`base-image.txt`, hash-locked runtime dependencies in
`requirements-linux-x86_64-py312.lock`, and hash-locked wheel-build tools in
`build-requirements.lock`. The runtime lock is generated from
`python/pyproject.toml`'s `workspace` extra with `constraints.txt`; both lock
files contain hashes and are installed with pip's `--require-hashes` mode.
Named Buildx contexts limit source inputs to `python/`, `domains/workspace/`,
and `third_party/archipelago/`; the Python context's `.dockerignore` excludes
build outputs, tests, caches, and generated runtime assets.

The image installs its runtime as root, sets `/opt/threadforge` to mode
`0555`, and runs the final image as UID/GID `65532`. It avoids recursively
rewriting permissions on every installed dependency. Engineering task
containers additionally use a read-only root filesystem, with only their
task-scoped output mount writable.

From the repository root, run:

```powershell
.\containers\engineering\build.ps1
```

The script requires a local Docker default context and Buildx, builds without
publishing, then prints a metadata-only
`threadforge.engineering-image-build.v1` receipt. `image_id` is the immutable
local `sha256:<image-id>` accepted by the runtime pin; `tag` is only a local
convenience label. Copy `image_id` into the private runtime-pin request's
`--image` value. The build itself needs network access to retrieve the pinned
base and hash-verified packages; the running benchmark container has no network
and never pulls an image.

The base digest and dependency locks make build inputs explicit, but a built
image is not benchmark acceptance evidence. Record the returned image ID in
the external runtime pin, perform the required license review, and validate
the image with the real local Docker engine before using it for any benchmark.
Do not publish the image or benchmark data as part of this build helper.

To refresh dependencies, update the constraints intentionally and regenerate
the lock files with `uv pip compile` for Python 3.12 and
`x86_64-unknown-linux-gnu`; review the package/version changes and rerun the
image validation before replacing a pin.
