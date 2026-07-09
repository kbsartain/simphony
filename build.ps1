<#
.SYNOPSIS
    Builds and verifies the simphony executable.

    Exists specifically because "go build ./..." (or any build run without an
    explicit -o pointed at the real binary) silently succeeds without ever
    touching the root-level simphony.exe that gets launched -- the module's
    main package lives in .\cmd\simphony, not the repo root. That mismatch
    once cost hours to diagnose (silent "successful" builds that never
    updated the running binary). This script makes that failure mode
    impossible: it always builds the exact file you run, and it refuses to
    report success unless that specific file's timestamp actually moved.
#>

$ErrorActionPreference = "Stop"
Set-Location -LiteralPath $PSScriptRoot

$exePath = Join-Path $PSScriptRoot "simphony.exe"

Write-Host "=== go vet ./... ===" -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed (exit $LASTEXITCODE) - not building." }

Write-Host "=== go test ./... ===" -ForegroundColor Cyan
go test ./...
if ($LASTEXITCODE -ne 0) { throw "go test failed (exit $LASTEXITCODE) - not building." }

$beforeTime = $null
if (Test-Path -LiteralPath $exePath) {
    $beforeTime = (Get-Item -LiteralPath $exePath).LastWriteTimeUtc
}

Write-Host "=== go build -o simphony.exe .\cmd\simphony ===" -ForegroundColor Cyan
go build -o $exePath ./cmd/simphony
if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)." }

if (-not (Test-Path -LiteralPath $exePath)) {
    throw "Build reported success but $exePath does not exist. Something is wrong with the build target."
}

$afterTime = (Get-Item -LiteralPath $exePath).LastWriteTimeUtc
if ($null -ne $beforeTime -and $afterTime -eq $beforeTime) {
    throw "$exePath timestamp did NOT change ($afterTime). The build did not actually update the executable you run. Stop and investigate before restarting simphony."
}

Write-Host ""
Write-Host "BUILD OK: $exePath" -ForegroundColor Green
Write-Host "  Previous: $(if ($beforeTime) { $beforeTime } else { '(none)' })"
Write-Host "  Current:  $afterTime"
Write-Host ""
Write-Host "Safe to stop and restart simphony.exe now." -ForegroundColor Green
