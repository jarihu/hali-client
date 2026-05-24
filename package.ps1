$ErrorActionPreference = "Stop"
$root    = Split-Path -Parent $MyInvocation.MyCommand.Path
$outDir  = Join-Path $root "bin\oss"
$distDir = Join-Path $root "dist"
New-Item -ItemType Directory -Force $distDir | Out-Null

if (!(Test-Path $outDir)) {
    throw "Missing build output: $outDir — run build.ps1 first."
}
if ((Get-ChildItem $outDir).Count -eq 0) {
    throw "Empty build output: $outDir"
}

$zip = Join-Path $distDir "hali-oss.zip"
Compress-Archive -Path (Join-Path $outDir "*") -DestinationPath $zip -Force
Write-Host "Packaged: $zip"
