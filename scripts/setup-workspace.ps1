param(
    [string]$WorkspacePath = "."
)

$ErrorActionPreference = "Stop"

$workspace = Resolve-Path -LiteralPath $WorkspacePath
Set-Location -LiteralPath $workspace

$nodeDir = "C:\Program Files\nodejs"
if (Test-Path -LiteralPath $nodeDir) {
    $env:PATH = "$nodeDir;$env:PATH"
}

$dashboard = Join-Path $workspace "dashboard"
if (-not (Test-Path -LiteralPath (Join-Path $dashboard "package.json"))) {
    Write-Host "setup-workspace: dashboard package.json not found; skipping npm setup"
    exit 0
}

$npm = Join-Path $nodeDir "npm.cmd"
if (-not (Test-Path -LiteralPath $npm)) {
    $npm = "npm"
}

Push-Location -LiteralPath $dashboard
try {
    $nodeModules = Join-Path $dashboard "node_modules"
    $npmCache = Join-Path $dashboard ".npm-cache"
    $requiredBins = @("tsc.cmd", "vite.cmd")
    $hasRequiredBins = $true
    foreach ($bin in $requiredBins) {
        $binPath = Join-Path $dashboard "node_modules\.bin\$bin"
        if (-not (Test-Path -LiteralPath $binPath)) {
            $hasRequiredBins = $false
        }
    }

    if ($hasRequiredBins) {
        Write-Host "setup-workspace: dashboard dependencies already ready"
        return
    }

    if (Test-Path -LiteralPath $nodeModules) {
        Remove-Item -LiteralPath $nodeModules -Recurse -Force
    }
    if (Test-Path -LiteralPath $npmCache) {
        Remove-Item -LiteralPath $npmCache -Recurse -Force
    }

    if (Test-Path -LiteralPath (Join-Path $dashboard "package-lock.json")) {
        & $npm ci --cache .npm-cache
    } else {
        & $npm install --cache .npm-cache
    }
    if ($LASTEXITCODE -ne 0) {
        throw "npm dependency install failed with exit code $LASTEXITCODE"
    }

    foreach ($bin in $requiredBins) {
        $binPath = Join-Path $dashboard "node_modules\.bin\$bin"
        if (-not (Test-Path -LiteralPath $binPath)) {
            throw "expected dashboard tool missing after npm install: $binPath"
        }
    }
}
finally {
    Pop-Location
}

Write-Host "setup-workspace: dashboard dependencies ready"
