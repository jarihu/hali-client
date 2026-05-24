#Requires -Version 7
<#
.SYNOPSIS
    Builds the Hali MSI installer using WiX 7.

.DESCRIPTION
    Prerequisites:
      - WiX 7 toolset installed (winget install WiXToolset.WiXCLI) and 'wix' available in PATH
      - WiX UI and Util extensions installed: wix extension add -g WixToolset.UI.wixext WixToolset.Util.wixext
      - hali.exe, halid.exe, hali-tray.exe built in ..\bin\oss\  (run ..\build.ps1 first)

    Usage:
      cd installer
      .\build.ps1                        # builds Hali.msi from ..\bin\oss at the current git version
      .\build.ps1 -BinDir ..\bin\custom  # explicit bin dir override
      .\build.ps1 -Version 1.2.3         # explicit version override
#>
param(
    [string]$BinDir    = "",
    [string]$Version   = ""
)

$ErrorActionPreference = "Stop"

$resolvedBinDir = $BinDir
if (-not $resolvedBinDir) {
    $ossBinDir = Join-Path $PSScriptRoot "..\bin\oss"
    if (Test-Path $ossBinDir) {
        $resolvedBinDir = $ossBinDir
    }
    else {
        $resolvedBinDir = Join-Path $PSScriptRoot "..\bin"
    }
}

$binDir  = Resolve-Path $resolvedBinDir
$outFile = Join-Path $PSScriptRoot "Hali.msi"

# Derive product version from git tag (strip leading 'v'; fallback to 0.0.0).
# MSI requires X.Y.Z[.W] format with no pre-release labels.
if (-not $Version) {
    $rawTag = & git describe --tags --abbrev=0 2>$null
    if ($rawTag -match '^v?(\d+\.\d+\.\d+)') {
        $Version = $Matches[1]
    } else {
        $Version = "0.0.0"
    }
}
Write-Host "Product version: $Version"

foreach ($bin in @("hali.exe", "halid.exe", "hali-tray.exe")) {
    $p = Join-Path $binDir $bin
    if (-not (Test-Path $p)) {
        Write-Error "Missing binary: $p — run build.ps1 from repo root first."
    }
}

Write-Host "Building MSI..."
wix build `
    -arch x64 `
    -d "BinDir=$binDir" `
    -d "ProductVersion=$Version" `
    -ext WixToolset.UI.wixext `
    -ext WixToolset.Util.wixext `
    -out $outFile `
    (Join-Path $PSScriptRoot "hali.wxs")

Write-Host "Installer: $outFile"
