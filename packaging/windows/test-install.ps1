# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("threadforge-installer-test-" + [guid]::NewGuid().ToString('N'))
[void][System.IO.Directory]::CreateDirectory($temporaryRoot)
Import-Module (Join-Path $PSScriptRoot 'ThreadforgeInstaller.psm1') -Force

function Assert-TestCondition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-TestThrows {
    param([scriptblock]$Action, [string]$Message)
    $didThrow = $false
    try { & $Action } catch { $didThrow = $true }
    if (-not $didThrow) { throw $Message }
}

function New-TestBinary {
    param([string]$OutputPath, [string]$ReportedVersion, [int]$ExitCode = 0)

    $sourcePath = [System.IO.Path]::ChangeExtension($OutputPath, '.go')
    $source = @'
package main
import (
    "fmt"
    "os"
)
func main() {
    fmt.Println("threadforge __VERSION__")
    os.Exit(__EXIT_CODE__)
}
'@
    $source = $source.Replace('__VERSION__', $ReportedVersion).Replace('__EXIT_CODE__', [string]$ExitCode)
    [System.IO.File]::WriteAllText($sourcePath, $source, [System.Text.UTF8Encoding]::new($false))
    & go build -trimpath -o $OutputPath $sourcePath
    if ($LASTEXITCODE -ne 0) { throw 'Could not build a local installer-test CLI fixture.' }
    [System.IO.File]::Delete($sourcePath)
}

function New-TestArchive {
    param(
        [string]$ArchivePath,
        [string]$BinaryPath,
        [switch]$AddExtraEntry,
        [switch]$SymlinkEntry,
        [switch]$LegacyPublishedV050,
        [switch]$LegacyBinaryOnly
    )

    if ($LegacyPublishedV050 -and $LegacyBinaryOnly) { throw 'Test archive legacy layouts are mutually exclusive.' }

    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::Open($ArchivePath, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        $entry = $archive.CreateEntry('threadforge.exe', [System.IO.Compression.CompressionLevel]::Optimal)
        if ($SymlinkEntry) { $entry.ExternalAttributes = [int]0xA1FF0000 }
        $inputStream = [System.IO.File]::OpenRead($BinaryPath)
        $outputStream = $entry.Open()
        try { $inputStream.CopyTo($outputStream) } finally { $inputStream.Dispose(); $outputStream.Dispose() }
        if (-not $LegacyBinaryOnly) {
            $legalMembers = @('LICENSE', 'NOTICE')
            if (-not $LegacyPublishedV050) {
                $legalMembers += @('THIRD_PARTY.md', 'THIRD_PARTY_LICENSES.tar.gz')
            }
            foreach ($name in $legalMembers) {
                $legalEntry = $archive.CreateEntry($name, [System.IO.Compression.CompressionLevel]::Optimal)
                $legalStream = $legalEntry.Open()
                try {
                    $bytes = [System.Text.Encoding]::UTF8.GetBytes("synthetic $name legal payload`n")
                    $legalStream.Write($bytes, 0, $bytes.Length)
                }
                finally { $legalStream.Dispose() }
            }
        }
        if ($AddExtraEntry) {
            $extra = $archive.CreateEntry('unexpected.txt')
            $extraStream = $extra.Open()
            try {
                $bytes = [System.Text.Encoding]::UTF8.GetBytes('not part of the CLI release')
                $extraStream.Write($bytes, 0, $bytes.Length)
            }
            finally { $extraStream.Dispose() }
        }
    }
    finally { $archive.Dispose() }
}

function New-TestDownloader {
    param([string]$ArchivePath, [string]$ChecksumText, [string]$ReleaseVersion)

    $capturedArchive = $ArchivePath
    $capturedChecksums = $ChecksumText
    $capturedVersion = $ReleaseVersion
    return {
        param([string]$Uri, [string]$Destination, [long]$MaximumBytes)

        $parsed = [Uri]$Uri
        if ($parsed.Scheme -ne 'https') { throw 'Test observed a non-HTTPS download.' }
        if ($parsed.Host -eq 'api.github.com' -and $parsed.AbsolutePath -eq '/repos/Rangow4562/ThreadForge/releases/latest') {
            $json = @{ tag_name = "v$capturedVersion"; draft = $false; prerelease = $false } | ConvertTo-Json -Compress
            [System.IO.File]::WriteAllText($Destination, $json, [System.Text.Encoding]::UTF8)
        }
        elseif ($parsed.Host -eq 'github.com' -and $parsed.AbsolutePath.EndsWith('/checksums.txt', [System.StringComparison]::Ordinal)) {
            [System.IO.File]::WriteAllText($Destination, $capturedChecksums, [System.Text.Encoding]::UTF8)
        }
        elseif ($parsed.Host -eq 'github.com' -and $parsed.AbsolutePath.EndsWith("/threadforge_${capturedVersion}_windows_amd64.zip", [System.StringComparison]::Ordinal)) {
            [System.IO.File]::Copy($capturedArchive, $Destination, $true)
        }
        else {
            throw "Unexpected test download URI: $Uri"
        }
        if ((Get-Item -LiteralPath $Destination).Length -gt $MaximumBytes) { throw 'Test fixture exceeded the requested download limit.' }
    }.GetNewClosure()
}

try {
    $goodBinary = Join-Path $temporaryRoot 'good.exe'
    $wrongBinary = Join-Path $temporaryRoot 'wrong.exe'
    $failingBinary = Join-Path $temporaryRoot 'failing.exe'
    $legacyBinary = Join-Path $temporaryRoot 'legacy.exe'
    New-TestBinary -OutputPath $goodBinary -ReportedVersion '0.5.0'
    New-TestBinary -OutputPath $wrongBinary -ReportedVersion '9.9.9'
    New-TestBinary -OutputPath $failingBinary -ReportedVersion '0.5.0' -ExitCode 9
    New-TestBinary -OutputPath $legacyBinary -ReportedVersion '0.4.6'

    $goodArchive = Join-Path $temporaryRoot 'good.zip'
    New-TestArchive -ArchivePath $goodArchive -BinaryPath $goodBinary
    $goodHash = (Get-FileHash -LiteralPath $goodArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $assetName = 'threadforge_0.5.0_windows_amd64.zip'
    $goodChecksums = "$goodHash  $assetName`r`n$('{0}' -f ('0' * 64))  unrelated.txt`r`n"

    $pinnedDir = Join-Path $temporaryRoot 'pinned'
    $pinnedDownloader = New-TestDownloader -ArchivePath $goodArchive -ChecksumText $goodChecksums -ReleaseVersion '0.5.0'
    $pinnedOutput = @(Install-Threadforge -Version 'v0.5.0' -InstallDir $pinnedDir -Downloader $pinnedDownloader) -join "`n"
    Assert-TestCondition ([System.IO.File]::Exists((Join-Path $pinnedDir 'threadforge.exe'))) 'Pinned install did not create threadforge.exe.'
    Assert-TestCondition ($pinnedOutput.Contains('threadforge 0.5.0')) 'Pinned install output did not report the verified version.'

    $latestDir = Join-Path $temporaryRoot 'latest'
    $latestDownloader = New-TestDownloader -ArchivePath $goodArchive -ChecksumText $goodChecksums -ReleaseVersion '0.5.0'
    $null = Install-Threadforge -InstallDir $latestDir -Downloader $latestDownloader
    Assert-TestCondition ([System.IO.File]::Exists((Join-Path $latestDir 'threadforge.exe'))) 'Latest install did not create threadforge.exe.'

    $publishedLegacyArchive = Join-Path $temporaryRoot 'published-v050.zip'
    New-TestArchive -ArchivePath $publishedLegacyArchive -BinaryPath $goodBinary -LegacyPublishedV050
    $publishedLegacyHash = (Get-FileHash -LiteralPath $publishedLegacyArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $publishedLegacyDownloader = New-TestDownloader -ArchivePath $publishedLegacyArchive -ChecksumText "$publishedLegacyHash  $assetName`n" -ReleaseVersion '0.5.0'
    $publishedLegacyDir = Join-Path $temporaryRoot 'published-v050'
    $publishedLegacyOutput = @(Install-Threadforge -Version '0.5.0' -InstallDir $publishedLegacyDir -Downloader $publishedLegacyDownloader) -join "`n"
    Assert-TestCondition ($publishedLegacyOutput.Contains('threadforge 0.5.0')) 'Published v0.5.0 ZIP layout was not accepted.'

    $legacyArchive = Join-Path $temporaryRoot 'legacy-binary-only.zip'
    New-TestArchive -ArchivePath $legacyArchive -BinaryPath $legacyBinary -LegacyBinaryOnly
    $legacyHash = (Get-FileHash -LiteralPath $legacyArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $legacyAssetName = 'threadforge_0.4.6_windows_amd64.zip'
    $legacyDownloader = New-TestDownloader -ArchivePath $legacyArchive -ChecksumText "$legacyHash  $legacyAssetName`n" -ReleaseVersion '0.4.6'
    $legacyDir = Join-Path $temporaryRoot 'legacy-binary-only'
    $legacyOutput = @(Install-Threadforge -Version '0.4.6' -InstallDir $legacyDir -Downloader $legacyDownloader) -join "`n"
    Assert-TestCondition ($legacyOutput.Contains('threadforge 0.4.6')) 'Historical binary-only ZIP layout was not accepted.'

    $replacementDir = Join-Path $temporaryRoot 'replacement'
    [void][System.IO.Directory]::CreateDirectory($replacementDir)
    [System.IO.File]::Copy($wrongBinary, (Join-Path $replacementDir 'threadforge.exe'))
    $replacementDownloader = New-TestDownloader -ArchivePath $goodArchive -ChecksumText $goodChecksums -ReleaseVersion '0.5.0'
    $null = Install-Threadforge -Version '0.5.0' -InstallDir $replacementDir -Downloader $replacementDownloader
    Assert-TestCondition ((Get-FileHash -LiteralPath (Join-Path $replacementDir 'threadforge.exe') -Algorithm SHA256).Hash -ceq (Get-FileHash -LiteralPath $goodBinary -Algorithm SHA256).Hash) 'Verified installation did not atomically replace the prior binary.'

    $invalidDir = Join-Path $temporaryRoot 'invalid-version'
    Assert-TestThrows { Install-Threadforge -Version '../0.5.0' -InstallDir $invalidDir -Downloader $pinnedDownloader } 'Installer accepted a path-like version.'
    Assert-TestCondition (-not [System.IO.Directory]::Exists($invalidDir)) 'Invalid version created an install directory.'

    $badChecksumDir = Join-Path $temporaryRoot 'bad-checksum'
    $badChecksumDownloader = New-TestDownloader -ArchivePath $goodArchive -ChecksumText ("$('0' * 64)  $assetName`n") -ReleaseVersion '0.5.0'
    Assert-TestThrows { Install-Threadforge -Version '0.5.0' -InstallDir $badChecksumDir -Downloader $badChecksumDownloader } 'Installer accepted a checksum mismatch.'
    Assert-TestCondition (-not [System.IO.Directory]::Exists($badChecksumDir)) 'Checksum mismatch created an install directory.'

    $extraArchive = Join-Path $temporaryRoot 'extra.zip'
    New-TestArchive -ArchivePath $extraArchive -BinaryPath $goodBinary -AddExtraEntry
    $extraHash = (Get-FileHash -LiteralPath $extraArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $extraDownloader = New-TestDownloader -ArchivePath $extraArchive -ChecksumText "$extraHash  $assetName`n" -ReleaseVersion '0.5.0'
    $extraDir = Join-Path $temporaryRoot 'extra-entry'
    Assert-TestThrows { Install-Threadforge -Version '0.5.0' -InstallDir $extraDir -Downloader $extraDownloader } 'Installer accepted an unexpected ZIP member.'
    Assert-TestCondition (-not [System.IO.Directory]::Exists($extraDir)) 'Unexpected archive member created an install directory.'

    $symlinkArchive = Join-Path $temporaryRoot 'symlink.zip'
    New-TestArchive -ArchivePath $symlinkArchive -BinaryPath $goodBinary -SymlinkEntry
    $symlinkHash = (Get-FileHash -LiteralPath $symlinkArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $symlinkDownloader = New-TestDownloader -ArchivePath $symlinkArchive -ChecksumText "$symlinkHash  $assetName`n" -ReleaseVersion '0.5.0'
    $symlinkDir = Join-Path $temporaryRoot 'symlink-entry'
    Assert-TestThrows { Install-Threadforge -Version '0.5.0' -InstallDir $symlinkDir -Downloader $symlinkDownloader } 'Installer accepted a ZIP link entry.'
    Assert-TestCondition (-not [System.IO.Directory]::Exists($symlinkDir)) 'ZIP link entry created an install directory.'

    $rollbackDir = Join-Path $temporaryRoot 'rollback'
    [void][System.IO.Directory]::CreateDirectory($rollbackDir)
    $existingPath = Join-Path $rollbackDir 'threadforge.exe'
    [System.IO.File]::Copy($goodBinary, $existingPath)
    $existingHash = (Get-FileHash -LiteralPath $existingPath -Algorithm SHA256).Hash
    $wrongArchive = Join-Path $temporaryRoot 'wrong.zip'
    New-TestArchive -ArchivePath $wrongArchive -BinaryPath $wrongBinary
    $wrongHash = (Get-FileHash -LiteralPath $wrongArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $wrongDownloader = New-TestDownloader -ArchivePath $wrongArchive -ChecksumText "$wrongHash  $assetName`n" -ReleaseVersion '0.5.0'
    Assert-TestThrows { Install-Threadforge -Version '0.5.0' -InstallDir $rollbackDir -Downloader $wrongDownloader } 'Installer accepted a binary reporting the wrong version.'
    Assert-TestCondition ((Get-FileHash -LiteralPath $existingPath -Algorithm SHA256).Hash -ceq $existingHash) 'Failed version check changed the prior installation.'

    $failedSmokeArchive = Join-Path $temporaryRoot 'failed-smoke.zip'
    New-TestArchive -ArchivePath $failedSmokeArchive -BinaryPath $failingBinary
    $failedSmokeHash = (Get-FileHash -LiteralPath $failedSmokeArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $failedSmokeDownloader = New-TestDownloader -ArchivePath $failedSmokeArchive -ChecksumText "$failedSmokeHash  $assetName`n" -ReleaseVersion '0.5.0'
    Assert-TestThrows { Install-Threadforge -Version '0.5.0' -InstallDir $rollbackDir -Downloader $failedSmokeDownloader } 'Installer accepted a non-zero CLI version check.'
    Assert-TestCondition ((Get-FileHash -LiteralPath $existingPath -Algorithm SHA256).Hash -ceq $existingHash) 'Failed process check changed the prior installation.'

    Write-Output 'Windows installer fixture tests passed (pinned/latest install, HTTPS selection, checksum, archive shape/link rejection, version validation, and rollback).'
}
finally {
    Remove-Module ThreadforgeInstaller -Force -ErrorAction SilentlyContinue
    if ([System.IO.Directory]::Exists($temporaryRoot)) {
        [System.IO.Directory]::Delete($temporaryRoot, $true)
    }
}
