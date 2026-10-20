param(
    [Parameter()]
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._/-]{0,119}(:[A-Za-z0-9][A-Za-z0-9._-]{0,119})?$')]
    [string]$Tag = 'threadforge-engineering:local'
)

$ErrorActionPreference = 'Stop'
$baseImage = (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'base-image.txt') -Raw).Trim()
if ($baseImage -notmatch '^python:3\.12\.[0-9]+-slim-bookworm@sha256:[0-9a-f]{64}$') {
    throw 'Engineering base image must be the approved Python 3.12 Bookworm image pinned by sha256 digest.'
}

$runtimeLock = Join-Path $PSScriptRoot 'requirements-linux-x86_64-py312.lock'
$buildLock = Join-Path $PSScriptRoot 'build-requirements.lock'
foreach ($path in @($runtimeLock, $buildLock)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required locked dependency file is missing: $(Split-Path -Leaf $path)"
    }
}

$pythonRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..\python')).Path
$workspaceRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..\domains\workspace')).Path
$archipelagoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..\third_party\archipelago')).Path
$contextHost = (& docker --context default context inspect --format '{{.Endpoints.docker.Host}}' default 2>$null).Trim()
if ($LASTEXITCODE -ne 0 -or $contextHost -notmatch '^(unix|npipe)://') {
    throw 'Engineering image build requires Docker default context to use a local daemon.'
}
& docker buildx version *> $null
if ($LASTEXITCODE -ne 0) {
    throw 'Docker Buildx is required to assemble the isolated named source contexts.'
}

$runtimeHash = (Get-FileHash -LiteralPath $runtimeLock -Algorithm SHA256).Hash.ToLowerInvariant()
$buildHash = (Get-FileHash -LiteralPath $buildLock -Algorithm SHA256).Hash.ToLowerInvariant()
$dockerfile = Join-Path $PSScriptRoot 'Dockerfile'
& docker --context default buildx build `
    --platform linux/amd64 `
    --load `
    --file $dockerfile `
    --tag $Tag `
    --build-arg "THREADFORGE_PYTHON_BASE_IMAGE=$baseImage" `
    --build-arg "THREADFORGE_REQUIREMENTS_SHA256=$runtimeHash" `
    --build-arg "THREADFORGE_BUILD_REQUIREMENTS_SHA256=$buildHash" `
    --build-context "threadforge-python=$pythonRoot" `
    --build-context "threadforge-workspace=$workspaceRoot" `
    --build-context "threadforge-archipelago=$archipelagoRoot" `
    $PSScriptRoot
if ($LASTEXITCODE -ne 0) {
    throw 'Engineering runtime image build failed.'
}

$imageId = (& docker --context default image inspect --format '{{.Id}}' $Tag 2>$null).Trim()
if ($LASTEXITCODE -ne 0 -or $imageId -notmatch '^sha256:[0-9a-f]{64}$') {
    throw 'Docker did not return an immutable local image ID after the build.'
}
[ordered]@{
    schema_version = 'threadforge.engineering-image-build.v1'
    platform = 'linux/amd64'
    image_id = $imageId
    tag = $Tag
    base_image = $baseImage
    runtime_requirements_sha256 = $runtimeHash
    build_requirements_sha256 = $buildHash
    benchmark_data_included = $false
    evaluator_included = $false
} | ConvertTo-Json -Compress
