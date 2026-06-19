# Build the Go engine sidecar for Tauri (Windows).
# Output: src-tauri\binaries\engine-<target-triple>.exe  (name Tauri expects)
$ErrorActionPreference = "Stop"

$triple = "x86_64-pc-windows-msvc"
$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root "src-tauri\binaries\engine-$triple.exe"
New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null

Push-Location (Join-Path $root "engine")
try {
    go build -o $out ./cmd/engine
} finally {
    Pop-Location
}
Write-Host "Built $out"