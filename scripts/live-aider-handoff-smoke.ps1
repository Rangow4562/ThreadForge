[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$RepositoryRoot = Split-Path -Parent $PSScriptRoot
$FixturePath = Join-Path $PSScriptRoot 'testdata\aider-handoff.jsonl'
$AiderCommand = Get-Command 'aider' -CommandType Application -ErrorAction SilentlyContinue
if (-not $AiderCommand) {
    [Console]::Error.WriteLine('Aider executable is not on PATH; install it separately, then rerun this smoke.')
    exit 2
}

$TempRoot = Join-Path ([IO.Path]::GetTempPath()) ('threadforge-aider-handoff-smoke-' + [guid]::NewGuid().ToString('N'))
$Workspace = Join-Path $TempRoot 'workspace with spaces'
$OutputDirectory = Join-Path $TempRoot 'handoffs with spaces'
$ThreadforgeHome = Join-Path $TempRoot 'threadforge home'
$IsolatedHome = Join-Path $TempRoot 'isolated home'
$EnvironmentNames = @(
    'HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME',
    'THREADFORGE_HOME', 'OPENAI_API_BASE', 'OPENAI_API_KEY', 'AIDER_MODEL',
    'AIDER_EXIT', 'AIDER_SHOW_RELEASE_NOTES'
)
$PreviousEnvironment = @{}
foreach ($Name in $EnvironmentNames) {
    $PreviousEnvironment[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process')
}

$AiderVersion = 'unknown'
$ThreadforgeExitCode = -1
$ReadOnlyLoadConfirmed = $false
$FixtureInBrief = $false
$FixtureEchoedToOutput = $false
$SourceUnchanged = $false
$BriefCount = 0
$SmokeError = $false

try {
    New-Item -ItemType Directory -Path @(
        $Workspace,
        $OutputDirectory,
        $ThreadforgeHome,
        $IsolatedHome,
        (Join-Path $IsolatedHome 'AppData\Roaming'),
        (Join-Path $IsolatedHome 'AppData\Local')
    ) -Force | Out-Null

    $env:HOME = $IsolatedHome
    $env:USERPROFILE = $IsolatedHome
    $env:APPDATA = Join-Path $IsolatedHome 'AppData\Roaming'
    $env:LOCALAPPDATA = Join-Path $IsolatedHome 'AppData\Local'
    $env:XDG_CONFIG_HOME = Join-Path $IsolatedHome '.config'
    $env:THREADFORGE_HOME = $ThreadforgeHome
    $env:OPENAI_API_BASE = 'http://127.0.0.1:9/v1'
    $env:OPENAI_API_KEY = 'threadforge-smoke-dummy-key'
    $env:AIDER_MODEL = 'openai/gpt-4o'
    $env:AIDER_EXIT = 'true'
    $env:AIDER_SHOW_RELEASE_NOTES = 'false'

    $AiderVersionOutput = & $AiderCommand.Source --version 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw 'aider_version_failed'
    }
    $AiderVersion = (($AiderVersionOutput | ForEach-Object { [string]$_ }) -join ' ').Trim()
    if ($AiderVersion -notmatch '^aider\s+\S+') {
        throw 'aider_version_unrecognized'
    }

    $SourceHashBefore = (Get-FileHash -LiteralPath $FixturePath -Algorithm SHA256).Hash
    Push-Location $RepositoryRoot
    try {
        $LaunchOutput = & go run ./cmd/threadforge handoff resume aider-e2e-session-123 `
            --source runtime-log `
            --harness codex `
            --agent aider `
            --traces $FixturePath `
            --cwd $Workspace `
            --output-dir $OutputDirectory `
            --yes 2>&1
        $ThreadforgeExitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }

    $OutputText = ($LaunchOutput | ForEach-Object { [string]$_ }) -join [Environment]::NewLine
    $ReadOnlyLoadConfirmed = [regex]::IsMatch($OutputText, '(?s)Added .*? to the chat\s*\(read-only\)\.')
    $FixtureEchoedToOutput = $OutputText.Contains('PRIVATE_HANDOFF_E2E_MARKER')
    $SourceHashAfter = (Get-FileHash -LiteralPath $FixturePath -Algorithm SHA256).Hash
    $SourceUnchanged = $SourceHashBefore -eq $SourceHashAfter

    $Briefs = @(Get-ChildItem -LiteralPath $OutputDirectory -File -Filter '*.md')
    $BriefCount = $Briefs.Count
    if ($BriefCount -eq 1) {
        $BriefText = [IO.File]::ReadAllText($Briefs[0].FullName)
        $FixtureInBrief = $BriefText.Contains('PRIVATE_HANDOFF_E2E_MARKER')
    }
}
catch {
    $SmokeError = $true
}
finally {
    foreach ($Name in $EnvironmentNames) {
        [Environment]::SetEnvironmentVariable($Name, $PreviousEnvironment[$Name], 'Process')
    }

    $TempParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $ResolvedTempRoot = [IO.Path]::GetFullPath($TempRoot)
    $ExpectedLeaf = (Split-Path -Leaf $ResolvedTempRoot) -match '^threadforge-aider-handoff-smoke-[0-9a-f]{32}$'
    $IsWithinTemp = $ResolvedTempRoot.StartsWith(
        $TempParent + [IO.Path]::DirectorySeparatorChar,
        [StringComparison]::OrdinalIgnoreCase
    )
    if ($ExpectedLeaf -and $IsWithinTemp -and (Test-Path -LiteralPath $ResolvedTempRoot)) {
        Remove-Item -LiteralPath $ResolvedTempRoot -Recurse -Force
    }
}

$Passed = -not $SmokeError -and
    $ThreadforgeExitCode -eq 0 -and
    $ReadOnlyLoadConfirmed -and
    $FixtureInBrief -and
    -not $FixtureEchoedToOutput -and
    $SourceUnchanged -and
    $BriefCount -eq 1

$Receipt = [ordered]@{
    schema_version = 'threadforge.aider-handoff-live-smoke.v1'
    status = if ($Passed) { 'passed' } else { 'failed' }
    aider_version = $AiderVersion
    threadforge_exit_code = $ThreadforgeExitCode
    read_only_load_confirmed = $ReadOnlyLoadConfirmed
    synthetic_brief_created = $BriefCount -eq 1
    source_trace_unchanged = $SourceUnchanged
    fixture_marker_echoed = $FixtureEchoedToOutput
    api_base_scope = 'loopback_fixture_only'
    model_prompt_submitted = $false
    user_configuration_isolated = $true
}
$Receipt | ConvertTo-Json -Compress

if (-not $Passed) {
    exit 1
}
