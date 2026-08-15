# dsh-launcher one-click build script (Windows PowerShell)
# Usage:
#   .\build.ps1                  # release build (windowsgui, version=dev)
#   .\build.ps1 -Version v0.0.1  # release build with version injected
#   .\build.ps1 -Debug           # debug build (console output)
param(
    [string]$Version = "dev",
    [switch]$Debug
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

# Embedded resources: rsrc.syso (icon + manifest). Generate if missing.
if (-not (Test-Path 'rsrc.syso')) {
    $rsrc = Join-Path (go env GOPATH) 'bin\rsrc.exe'
    if (-not (Test-Path $rsrc)) {
        Write-Host 'rsrc not found, installing github.com/akavel/rsrc@v0.10.2 ...'
        go install github.com/akavel/rsrc@v0.10.2
    }
    Write-Host 'Generating rsrc.syso ...'
    & $rsrc -ico icon.ico -manifest app.manifest -o rsrc.syso
}

if ($Debug) {
    Write-Host "Building debug build (version=$Version) ..."
    go build -ldflags "-X main.version=$Version" -o dsh-launcher-debug.exe .
    Write-Host 'OK: dsh-launcher-debug.exe'
} else {
    Write-Host "Building release build (version=$Version) ..."
    go build -ldflags "-s -w -H windowsgui -X main.guiBuild=1 -X main.version=$Version" -o dsh-launcher.exe .
    Write-Host 'OK: dsh-launcher.exe'
}
