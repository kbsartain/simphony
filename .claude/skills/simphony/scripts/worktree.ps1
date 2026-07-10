# Simphony worktree helper (PowerShell) — per-issue git worktree isolation.
# Mirrors internal/workspace/manager.go (mode: git_worktree).
#
# Usage:
#   worktree.ps1 -Cmd prepare -Identifier ENG-123 [-Base main] [-Prefix simphony/] [-Root ./simphony_workspaces] [-Repo .]
#   worktree.ps1 -Cmd path    -Identifier ENG-123 [-Root ./simphony_workspaces]
#   worktree.ps1 -Cmd merge   -Identifier ENG-123 [-Base main] [-Prefix simphony/] [-Root ./simphony_workspaces] [-Repo .]
#   worktree.ps1 -Cmd remove  -Identifier ENG-123 [-Root ./simphony_workspaces] [-Repo .]
param(
  [Parameter(Mandatory=$true)][string]$Cmd,
  [Parameter(Mandatory=$true)][string]$Identifier,
  [string]$Base = "main",
  [string]$Prefix = "simphony/",
  [string]$Root = "./simphony_workspaces",
  [string]$Repo = "."
)
$ErrorActionPreference = "Stop"
function Get-Slug([string]$s) { ($s.ToLower() -replace '[^a-z0-9._-]', '-') }
$slug   = Get-Slug $Identifier
$branch = "$Prefix$slug"
$wt     = Join-Path $Root $slug

switch ($Cmd) {
  "prepare" {
    New-Item -ItemType Directory -Force -Path $Root | Out-Null
    $exists = (git -C $Repo worktree list --porcelain) -match "/$slug$"
    if ($exists) { Write-Output $wt; break }
    $hasBranch = (git -C $Repo show-ref --verify --quiet "refs/heads/$branch"; $LASTEXITCODE -eq 0)
    if ($hasBranch) { git -C $Repo worktree add $wt $branch | Out-Host }
    else            { git -C $Repo worktree add -b $branch $wt $Base | Out-Host }
    Write-Output $wt
  }
  "path"   { Write-Output $wt }
  "merge"  {
    git -C $Repo checkout $Base | Out-Host
    git -C $Repo merge --no-ff $branch -m "Simphony merge $Identifier" | Out-Host
    Write-Output "merged $branch -> $Base"
  }
  "remove" {
    git -C $Repo worktree remove $wt --force
    Write-Output "removed $wt"
  }
  default  { throw "unknown command: $Cmd" }
}
