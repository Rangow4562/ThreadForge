$ErrorActionPreference = 'Stop'

$RepositoryRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$CheckResults = [System.Collections.Generic.List[object]]::new()

function Invoke-Check {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory
    )

    if (-not (Get-Command $Executable -ErrorAction SilentlyContinue)) {
        $message = "Required test command '$Executable' is not available."
        Write-Host "BLOCKED: $Label — $message" -ForegroundColor Yellow
        $script:CheckResults.Add([pscustomobject]@{ Label = $Label; Status = 'blocked'; Detail = $message })
        return
    }

    Write-Host "== $Label =="
    Push-Location $WorkingDirectory
    try {
        & $Executable @Arguments
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            $message = "Exited with code $exitCode."
            Write-Host "FAILED: $Label — $message" -ForegroundColor Red
            $script:CheckResults.Add([pscustomobject]@{ Label = $Label; Status = 'failed'; Detail = $message })
        }
        else {
            Write-Host "PASSED: $Label" -ForegroundColor Green
            $script:CheckResults.Add([pscustomobject]@{ Label = $Label; Status = 'passed'; Detail = '' })
        }
    }
    catch {
        $message = $_.Exception.Message
        Write-Host "FAILED: $Label — $message" -ForegroundColor Red
        $script:CheckResults.Add([pscustomobject]@{ Label = $Label; Status = 'failed'; Detail = $message })
    }
    finally {
        Pop-Location
    }
}

Invoke-Check -Label 'Go tests' -Executable 'go' -Arguments @('test', './...') -WorkingDirectory $RepositoryRoot
Invoke-Check -Label 'Go vet' -Executable 'go' -Arguments @('vet', './...') -WorkingDirectory $RepositoryRoot
Invoke-Check -Label 'Python tests' -Executable 'python' -Arguments @('-m', 'pytest', '-q') -WorkingDirectory (Join-Path $RepositoryRoot 'python')
Invoke-Check -Label 'Python compile check' -Executable 'python' -Arguments @('-m', 'compileall', '-q', '.', '..\third_party\harbor_terminus2', '..\third_party\archipelago', '..\domains\coding', '..\domains\workspace') -WorkingDirectory (Join-Path $RepositoryRoot 'python')
Invoke-Check -Label 'Python sdist/wheel build, content audit, and installed protocol smoke' -Executable 'python' -Arguments @('scripts/verify_distribution.py') -WorkingDirectory (Join-Path $RepositoryRoot 'python')
$DependencyInventoryOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("threadforge-dependency-inventory-" + [guid]::NewGuid().ToString('N') + '.json')
Invoke-Check -Label 'Dependency manifest inventory (not legal clearance)' -Executable 'python' -Arguments @('scripts/generate_dependency_inventory.py', '--repo-root', $RepositoryRoot, '--output', $DependencyInventoryOutput) -WorkingDirectory $RepositoryRoot
if (Get-Command 'bash' -ErrorAction SilentlyContinue) {
    Invoke-Check -Label 'Coding runner shell syntax' -Executable 'bash' `
        -Arguments @('-n', 'domains/coding/scripts/run_eval.sh') -WorkingDirectory $RepositoryRoot
    Invoke-Check -Label 'Workspace runner shell syntax' -Executable 'bash' `
        -Arguments @('-n', 'domains/workspace/produce.sh') -WorkingDirectory $RepositoryRoot
    Invoke-Check -Label 'Elasticsearch stack integration shell syntax' -Executable 'bash' `
        -Arguments @('-n', 'scripts/test-elasticsearch-stack.sh') -WorkingDirectory $RepositoryRoot
} else {
    Write-Host 'SKIPPED: Coding runner shell syntax (Bash is unavailable)'
}
Invoke-Check -Label 'TypeScript adapter type check' -Executable 'npm' -Arguments @('run', 'check') -WorkingDirectory (Join-Path $RepositoryRoot 'adapters')
Invoke-Check -Label 'TypeScript adapter tests' -Executable 'npm' -Arguments @('test') -WorkingDirectory (Join-Path $RepositoryRoot 'adapters')
Invoke-Check -Label 'TypeScript adapter package contents' -Executable 'npm' -Arguments @('run', 'test:package') -WorkingDirectory (Join-Path $RepositoryRoot 'adapters')
$BrowserExtensionRoot = Join-Path $RepositoryRoot 'adapters/browser-extension'
$BrowserValidationRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("threadforge-browser-validation-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $BrowserValidationRoot | Out-Null
Invoke-Check -Label 'Browser extension type check' -Executable 'npm' -Arguments @('run', 'check') -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Browser extension unit tests' -Executable 'npm' -Arguments @('run', 'test:unit') -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Chromium browser extension build' -Executable 'npm' -Arguments @('run', 'build') -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Chromium browser extension replay tests' -Executable 'node' `
    -Arguments @('.\node_modules\@playwright\test\cli.js', 'test', '--reporter=line', '--output', (Join-Path $BrowserValidationRoot 'playwright')) `
    -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Chromium browser extension package smoke' -Executable 'node' `
    -Arguments @('.\node_modules\web-ext\bin\web-ext.js', 'build', '--source-dir', 'dist', '--artifacts-dir', (Join-Path $BrowserValidationRoot 'chromium'), '--filename', 'threadforge-browser-chromium.zip', '--no-input', '--no-config-discovery', '--overwrite-dest') `
    -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Firefox browser extension build and lint' -Executable 'npm' -Arguments @('run', 'lint:firefox') -WorkingDirectory $BrowserExtensionRoot
Invoke-Check -Label 'Firefox browser extension package smoke' -Executable 'node' `
    -Arguments @('.\node_modules\web-ext\bin\web-ext.js', 'build', '--source-dir', 'dist-firefox', '--artifacts-dir', (Join-Path $BrowserValidationRoot 'firefox'), '--filename', 'threadforge-browser-firefox.zip', '--no-input', '--no-config-discovery', '--overwrite-dest') `
    -WorkingDirectory $BrowserExtensionRoot
$hasOpenCode = [bool](Get-Command 'opencode' -ErrorAction SilentlyContinue)
$hasGemini = [bool](Get-Command 'gemini' -ErrorAction SilentlyContinue)
$hasCodex = [bool](Get-Command 'codex' -ErrorAction SilentlyContinue)
$hasClaude = [bool](Get-Command 'claude' -ErrorAction SilentlyContinue)
if ($hasOpenCode -and $hasGemini -and $hasCodex -and $hasClaude) {
    Invoke-Check -Label 'Installed host smoke (OpenCode/Gemini/Codex/Claude/Antigravity/Command Code, deterministic local fixtures)' -Executable 'npm' `
        -Arguments @('run', 'test:installed-skills') -WorkingDirectory (Join-Path $RepositoryRoot 'adapters')
}
else {
    $message = 'Install OpenCode, Gemini CLI, Codex CLI, and Claude Code to run the complete no-model host smoke.'
    Write-Host "SKIPPED: Installed skill discovery — $message" -ForegroundColor DarkYellow
    $script:CheckResults.Add([pscustomobject]@{ Label = 'Installed host smoke (OpenCode/Gemini/Codex/Claude/Antigravity/Command Code)'; Status = 'skipped'; Detail = $message })
}
Invoke-Check -Label 'Vendored Beacon Go tests' -Executable 'go' -Arguments @('test', './...') -WorkingDirectory (Join-Path $RepositoryRoot 'third_party/agent-beacon/pkg/asymptoteobserve')
Invoke-Check -Label 'Beacon native session-reader tests' -Executable 'go' -Arguments @('test', './...') -WorkingDirectory (Join-Path $RepositoryRoot 'third_party/beacon-sessionreaders')

$passed = @($CheckResults | Where-Object Status -eq 'passed').Count
$failed = @($CheckResults | Where-Object Status -eq 'failed').Count
$blocked = @($CheckResults | Where-Object Status -eq 'blocked').Count
 $skipped = @($CheckResults | Where-Object Status -eq 'skipped').Count
Write-Host "`nCheck summary: $passed passed, $failed failed, $blocked blocked, $skipped skipped."
foreach ($result in $CheckResults | Where-Object Status -ne 'passed') {
    Write-Host "- $($result.Status): $($result.Label) — $($result.Detail)"
}

if ($failed -gt 0 -or $blocked -gt 0) {
    Write-Host 'Threadforge validation is incomplete; all required checks must pass.' -ForegroundColor Red
    exit 1
}

Write-Host 'All configured Threadforge checks passed.' -ForegroundColor Green
