# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

Set-StrictMode -Version Latest

function ConvertTo-ThreadforgeReleaseVersion {
    param([Parameter(Mandatory = $true)][string]$Value)

    $normalized = $Value.Trim()
    if ($normalized.StartsWith('v', [System.StringComparison]::OrdinalIgnoreCase)) {
        $normalized = $normalized.Substring(1)
    }
    if ($normalized -notmatch '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$') {
        throw 'Version must be a release version such as 0.5.0 or v0.5.0.'
    }
    return $normalized
}

function Invoke-ThreadforgeHttpsDownload {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][long]$MaximumBytes
    )

    $parsedUri = [Uri]$Uri
    if ($parsedUri.Scheme -ne 'https' -or $parsedUri.Host -notin @('api.github.com', 'github.com')) {
        throw 'Downloads must use the official GitHub HTTPS endpoints.'
    }
    $curl = Get-Command 'curl.exe' -ErrorAction SilentlyContinue
    if ($null -eq $curl) {
        throw 'curl.exe is required (included with supported Windows versions).'
    }

    $curlArguments = @(
        '--fail', '--silent', '--show-error', '--location',
        '--proto', '=https', '--proto-redir', '=https',
        '--max-filesize', [string]$MaximumBytes,
        '--output', $Destination, $Uri
    )
    & $curl.Source @curlArguments 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "Could not download the release resource from $($parsedUri.Host)."
    }
    if (-not [System.IO.File]::Exists($Destination) -or (Get-Item -LiteralPath $Destination).Length -gt $MaximumBytes) {
        throw 'The downloaded resource is missing or exceeds its permitted size.'
    }
}

function Assert-ThreadforgeSafeDirectoryPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not [System.IO.Path]::IsPathRooted($Path) -or $Path.StartsWith('\\')) {
        throw 'InstallDir must be an absolute local path; UNC paths are not supported.'
    }
    if (($Path -split '[\\/]') -contains '..') {
        throw 'InstallDir must not contain parent-directory components.'
    }

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetPathRoot($fullPath)
    if ($fullPath.TrimEnd([char[]]@('\', '/')) -eq $root.TrimEnd([char[]]@('\', '/'))) {
        throw 'InstallDir must be a directory below the filesystem root.'
    }

    $probe = $fullPath
    while (-not [string]::IsNullOrEmpty($probe)) {
        if ([System.IO.Directory]::Exists($probe)) {
            $item = Get-Item -LiteralPath $probe -Force
            if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "InstallDir cannot contain a symbolic link or reparse point: $probe"
            }
        }
        elseif ([System.IO.File]::Exists($probe)) {
            throw "An install directory component is a file: $probe"
        }

        $parent = [System.IO.Directory]::GetParent($probe)
        if ($null -eq $parent) { break }
        $probe = $parent.FullName
    }
    return $fullPath
}

function Assert-ThreadforgeRegularFile {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ([System.IO.Directory]::Exists($Path)) {
        throw "Expected a regular file but found a directory: $Path"
    }
    if ([System.IO.File]::Exists($Path)) {
        $item = Get-Item -LiteralPath $Path -Force
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Refusing to replace a symbolic link or reparse point: $Path"
        }
    }
}

function Get-ThreadforgeChecksum {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumPath,
        [Parameter(Mandatory = $true)][string]$AssetName
    )

    $text = [System.IO.File]::ReadAllText($ChecksumPath, [System.Text.Encoding]::UTF8)
    $checksumMatches = [System.Collections.Generic.List[string]]::new()
    foreach ($line in ($text -split "`r?`n")) {
        if ($line -match '^\s*([0-9a-fA-F]{64})\s+\*?([^\r\n]+?)\s*$' -and $Matches[2] -ceq $AssetName) {
            $checksumMatches.Add($Matches[1].ToLowerInvariant())
        }
    }
    if ($checksumMatches.Count -ne 1) {
        throw "checksums.txt must contain exactly one SHA-256 entry for $AssetName."
    }
    return $checksumMatches[0]
}

function Expand-ThreadforgeArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$StagedPath
    )

    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        if ($archive.Entries.Count -ne 1) {
            throw 'The release ZIP must contain exactly one entry.'
        }
        $entry = $archive.Entries[0]
        if ($entry.FullName -cne 'threadforge.exe' -or $entry.Length -le 0 -or $entry.Length -gt 134217728) {
            throw 'The release ZIP must contain one bounded, non-empty top-level threadforge.exe.'
        }
        $externalAttributes = [int]$entry.ExternalAttributes
        $unixType = ($externalAttributes -shr 16) -band 0xF000
        $dosAttributes = $externalAttributes -band 0xFFFF
        if ($unixType -eq 0xA000 -or ($dosAttributes -band 0x400) -ne 0) {
            throw 'The release ZIP entry must be a regular file, not a link or reparse point.'
        }

        $inputStream = $entry.Open()
        $outputStream = [System.IO.File]::Open($StagedPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
        try {
            $buffer = New-Object byte[] 65536
            [long]$total = 0
            while (($read = $inputStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                $total += $read
                if ($total -gt $entry.Length -or $total -gt 134217728) {
                    throw 'The decompressed CLI exceeds its declared or permitted size.'
                }
                $outputStream.Write($buffer, 0, $read)
            }
            if ($total -ne $entry.Length) {
                throw 'The decompressed CLI size does not match the ZIP metadata.'
            }
        }
        finally {
            $inputStream.Dispose()
            $outputStream.Dispose()
        }
    }
    finally {
        $archive.Dispose()
    }
}

function Test-ThreadforgeBinaryVersion {
    param(
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [Parameter(Mandatory = $true)][string]$ExpectedVersion
    )

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $ExecutablePath
    $startInfo.Arguments = 'version'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) { throw 'The staged CLI could not be started.' }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(10000)) {
            $process.Kill()
            throw 'The staged CLI version check timed out.'
        }
        $stdout = $stdoutTask.GetAwaiter().GetResult().Trim()
        $null = $stderrTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0 -or $stdout -cne "threadforge $ExpectedVersion") {
            throw 'The staged CLI failed its exact version check.'
        }
    }
    finally {
        $process.Dispose()
    }
}

function Install-Threadforge {
    [CmdletBinding()]
    param(
        [string]$Version,
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [scriptblock]$Downloader
    )

    if ($null -eq $Downloader) { $Downloader = ${function:Invoke-ThreadforgeHttpsDownload} }
    $installPath = Assert-ThreadforgeSafeDirectoryPath -Path $InstallDir
    $temporaryPath = Join-Path ([System.IO.Path]::GetTempPath()) ("threadforge-install-" + [guid]::NewGuid().ToString('N'))
    [void][System.IO.Directory]::CreateDirectory($temporaryPath)
    $stagedPath = $null

    try {
        $tempItem = Get-Item -LiteralPath $temporaryPath -Force
        if (($tempItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw 'The installer temporary directory cannot be a reparse point.'
        }

        if ([string]::IsNullOrWhiteSpace($Version)) {
            $releaseInfoPath = Join-Path $temporaryPath 'latest-release.json'
            & $Downloader 'https://api.github.com/repos/Rangow4562/ThreadForge/releases/latest' $releaseInfoPath 1048576
            $releaseInfo = [System.IO.File]::ReadAllText($releaseInfoPath, [System.Text.Encoding]::UTF8) | ConvertFrom-Json
            if ($releaseInfo.draft -eq $true -or $releaseInfo.prerelease -eq $true -or [string]::IsNullOrWhiteSpace([string]$releaseInfo.tag_name)) {
                throw 'GitHub did not return a stable latest Threadforge release.'
            }
            $normalizedVersion = ConvertTo-ThreadforgeReleaseVersion -Value ([string]$releaseInfo.tag_name)
        }
        else {
            $normalizedVersion = ConvertTo-ThreadforgeReleaseVersion -Value $Version
        }

        $assetName = "threadforge_${normalizedVersion}_windows_amd64.zip"
        $releaseBase = "https://github.com/Rangow4562/ThreadForge/releases/download/v$normalizedVersion"
        $archivePath = Join-Path $temporaryPath $assetName
        $checksumPath = Join-Path $temporaryPath 'checksums.txt'
        & $Downloader "$releaseBase/$assetName" $archivePath 268435456
        & $Downloader "$releaseBase/checksums.txt" $checksumPath 1048576

        $expectedHash = Get-ThreadforgeChecksum -ChecksumPath $checksumPath -AssetName $assetName
        $actualHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -cne $expectedHash) { throw 'Release archive SHA-256 verification failed.' }

        $verifiedBinaryPath = Join-Path $temporaryPath 'threadforge.exe'
        Expand-ThreadforgeArchive -ArchivePath $archivePath -StagedPath $verifiedBinaryPath
        Test-ThreadforgeBinaryVersion -ExecutablePath $verifiedBinaryPath -ExpectedVersion $normalizedVersion

        [void][System.IO.Directory]::CreateDirectory($installPath)
        $installPath = Assert-ThreadforgeSafeDirectoryPath -Path $installPath
        $destinationPath = Join-Path $installPath 'threadforge.exe'
        Assert-ThreadforgeRegularFile -Path $destinationPath
        $stagedPath = Join-Path $installPath ('.threadforge-' + [guid]::NewGuid().ToString('N') + '.exe')
        [System.IO.File]::Copy($verifiedBinaryPath, $stagedPath)
        $verifiedHash = (Get-FileHash -LiteralPath $verifiedBinaryPath -Algorithm SHA256).Hash
        $stagedHash = (Get-FileHash -LiteralPath $stagedPath -Algorithm SHA256).Hash
        if ($verifiedHash -cne $stagedHash) { throw 'Staged CLI bytes changed during installation.' }
        Test-ThreadforgeBinaryVersion -ExecutablePath $stagedPath -ExpectedVersion $normalizedVersion
        Assert-ThreadforgeRegularFile -Path $destinationPath

        if ([System.IO.File]::Exists($destinationPath)) {
            $backupPath = Join-Path $installPath ('.threadforge-backup-' + [guid]::NewGuid().ToString('N'))
            [System.IO.File]::Replace($stagedPath, $destinationPath, $backupPath, $true)
            try { [System.IO.File]::Delete($backupPath) }
            catch { Write-Warning "The old CLI backup remains at $backupPath because cleanup failed." }
        }
        else {
            [System.IO.File]::Move($stagedPath, $destinationPath)
        }
        $stagedPath = $null
        Write-Output "Installed Threadforge CLI to $destinationPath"
        Write-Output "threadforge $normalizedVersion"
        Write-Output 'Add the install directory to PATH manually if you want to invoke threadforge.exe by name.'
    }
    finally {
        if ($null -ne $stagedPath -and [System.IO.File]::Exists($stagedPath)) {
            [System.IO.File]::Delete($stagedPath)
        }
        if ([System.IO.Directory]::Exists($temporaryPath)) {
            [System.IO.Directory]::Delete($temporaryPath, $true)
        }
    }
}

Export-ModuleMember -Function Install-Threadforge
