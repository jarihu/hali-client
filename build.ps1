$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

$gotmp   = Join-Path $root "gotmp"
$gocache = Join-Path $root "gocache"
$outDir  = Join-Path $root "bin\oss"

New-Item -ItemType Directory -Force $gotmp, $gocache, $outDir | Out-Null

$env:GOTMPDIR = $gotmp
$env:GOCACHE  = $gocache

$version = & git describe --tags --dirty --always 2>$null
if (-not $version) { $version = "dev" }
$commit  = & git rev-parse --short HEAD 2>$null
if (-not $commit)  { $commit  = "unknown" }
$mode    = if ($env:RELEASE) { "release" } else { "debug" }

$pkg     = "hali/internal/buildinfo"
$ldflags = "-X ${pkg}.Version=$version -X ${pkg}.Commit=$commit -X ${pkg}.BuildMode=$mode -X ${pkg}.Edition=oss"

function Invoke-GoBuild {
    param(
        [string]$Output,
        [string]$Package = "."
    )
    go build -tags oss -ldflags $ldflags -o $Output $Package
}

Write-Host "Building hali..."

Invoke-GoBuild -Output (Join-Path $outDir "hali.exe") -Package "."
Write-Host "Built: $(Join-Path $outDir 'hali.exe')"

if (Test-Path (Join-Path $root "cmd\service\main.go")) {
    Invoke-GoBuild -Output (Join-Path $outDir "halid.exe") -Package ".\cmd\service"
    Write-Host "Built: $(Join-Path $outDir 'halid.exe')"
}

if (Test-Path (Join-Path $root "cmd\tray\main.go")) {
    Invoke-GoBuild -Output (Join-Path $outDir "hali-tray.exe") -Package ".\cmd\tray"
    Write-Host "Built: $(Join-Path $outDir 'hali-tray.exe')"
}

$env:GOOS = "linux"
$env:GOARCH = "amd64"

Invoke-GoBuild -Output (Join-Path $outDir "hali-linux-amd64") -Package "."
Write-Host "Built: $(Join-Path $outDir 'hali-linux-amd64')"

if (Test-Path (Join-Path $root "cmd\service\main.go")) {
    Invoke-GoBuild -Output (Join-Path $outDir "halid-linux-amd64") -Package ".\cmd\service"
    Write-Host "Built: $(Join-Path $outDir 'halid-linux-amd64')"
}

if (Test-Path (Join-Path $root "cmd\tray\main.go")) {
    if ($IsWindows) {
        Write-Host "Skipped hali-tray-linux-amd64 build (linux tray cross-build is unsupported on Windows hosts)."
    }
    else {
        try {
            Invoke-GoBuild -Output (Join-Path $outDir "hali-tray-linux-amd64") -Package ".\cmd\tray"
            Write-Host "Built: $(Join-Path $outDir 'hali-tray-linux-amd64')"
        }
        catch {
            Write-Host "Skipped hali-tray-linux-amd64 build (linux tray dependencies unavailable in current environment)."
        }
    }
}

Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
