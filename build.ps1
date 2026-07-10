<#
.SYNOPSIS
    Builds and verifies the Simphony executable that operators actually run.

.DESCRIPTION
    The main package lives in .\cmd\simphony, so a generic `go build ./...`
    does not update the root-level simphony.exe. This script verifies the
    repository, builds that exact executable, and confirms its timestamp moved.
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
    throw "Build reported success but $exePath does not exist."
}

$afterTime = (Get-Item -LiteralPath $exePath).LastWriteTimeUtc
if ($null -ne $beforeTime -and $afterTime -eq $beforeTime) {
    throw "$exePath timestamp did not change ($afterTime)."
}

Write-Host ""
Write-Host "BUILD OK: $exePath" -ForegroundColor Green
Write-Host "  Previous: $(if ($beforeTime) { $beforeTime } else { '(none)' })"
Write-Host "  Current:  $afterTime"
