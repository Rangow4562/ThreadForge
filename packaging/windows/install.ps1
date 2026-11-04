# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

[CmdletBinding()]
param(
    [string]$Version,
    [string]$InstallDir
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        throw 'LOCALAPPDATA is unset; pass an absolute -InstallDir.'
    }
    $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\Threadforge'
}

Import-Module (Join-Path $PSScriptRoot 'ThreadforgeInstaller.psm1') -Force
Install-Threadforge -Version $Version -InstallDir $InstallDir
