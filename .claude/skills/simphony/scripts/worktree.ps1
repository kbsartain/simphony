# Simphony worktree helper (PowerShell) — per-issue git worktree isolation.
# Mirrors internal/workspace/manager.go (mode: git_worktree).
#
# Usage:
#   worktree.ps1 -Cmd prepare -Identifier ENG-123 [-Base main] [-Prefix simphony/] [-Root ./simphony_workspaces] [-Repo .] [-Branch <tracker branch>]
#   worktree.ps1 -Cmd path    -Identifier ENG-123 [-Root ./simphony_workspaces]
#   worktree.ps1 -Cmd merge   -Identifier ENG-123 [-Base main] [-Prefix simphony/] [-Root ./simphony_workspaces] [-Repo .] [-Branch <tracker branch>]
#   worktree.ps1 -Cmd remove  -Identifier ENG-123 [-Root ./simphony_workspaces] [-Repo .]
#
# -Branch: the tracker's suggested branch (e.g. Linear's gitBranchName), used
# verbatim; otherwise the branch is "<Prefix><slug>". `prepare` fetches and
# branches off origin/<Base> (the current remote tip), not a stale local ref.
param(
  [Parameter(Mandatory=$true)][string]$Cmd,
  [Parameter(Mandatory=$true)][string]$Identifier,
  [string]$Base = "main",
  [string]$Prefix = "simphony/",
  [string]$Root = "./simphony_workspaces",
  [string]$Repo = ".",
  [string]$Branch = ""
)
$ErrorActionPreference = "Stop"
function Get-Slug([string]$s) { ($s.ToLower() -replace '[^a-z0-9._-]', '-') }
$slug   = Get-Slug $Identifier
if ([string]::IsNullOrWhiteSpace($Branch)) { $Branch = "$Prefix$slug" }
$wt     = Join-Path $Root $slug

switch ($Cmd) {
  "prepare" {
    New-Item -ItemType Directory -Force -Path $Root | Out-Null
    $exists = (git -C $Repo worktree list --porcelain) -match "/$slug$"
    if ($exists) { Write-Output $wt; break }
    # Branch off the current remote tip; fall back to local base when offline.
    git -C $Repo fetch --quiet origin $Base 2>$null
    $baseRef = "origin/$Base"
    git -C $Repo rev-parse --verify --quiet "$baseRef^{commit}" *> $null
    if ($LASTEXITCODE -ne 0) { $baseRef = $Base }
    $hasBranch = (git -C $Repo show-ref --verify --quiet "refs/heads/$Branch"; $LASTEXITCODE -eq 0)
    if ($hasBranch) { git -C $Repo worktree add $wt $Branch | Out-Host }
    else            { git -C $Repo worktree add -b $Branch $wt $baseRef | Out-Host }
    Write-Output $wt
  }
  "path"   { Write-Output $wt }
  "status" {
    # Evidence sweep for reconciliation (key=value).
    git -C $Repo fetch --quiet origin $Base 2>$null
    $baseRef = "origin/$Base"
    git -C $Repo rev-parse --verify --quiet "$baseRef^{commit}" *> $null
    if ($LASTEXITCODE -ne 0) { $baseRef = $Base }
    Write-Output "branch=$Branch"
    if ((git -C $Repo worktree list --porcelain) -match "/$slug$") { Write-Output "worktree=yes" } else { Write-Output "worktree=no" }
    git -C $Repo show-ref --verify --quiet "refs/heads/$Branch"; if ($LASTEXITCODE -eq 0) { Write-Output "branch_local=yes" } else { Write-Output "branch_local=no" }
    git -C $Repo ls-remote --exit-code --heads origin $Branch *> $null; if ($LASTEXITCODE -eq 0) { Write-Output "branch_remote=yes" } else { Write-Output "branch_remote=no" }
    git -C $Repo rev-parse --verify --quiet "refs/heads/$Branch^{commit}" *> $null
    if ($LASTEXITCODE -eq 0) {
      $ahead = (git -C $Repo rev-list --count "$baseRef..$Branch"); Write-Output "commits_ahead=$ahead"
      # changes_vs_base: two-dot tree compare (heuristic; not squash-reliable — prefer PR state, §7)
      git -C $Repo diff --quiet "$baseRef" "$Branch"; if ($LASTEXITCODE -eq 0) { Write-Output "changes_vs_base=no" } else { Write-Output "changes_vs_base=yes" }
    } else { Write-Output "commits_ahead=0"; Write-Output "changes_vs_base=no" }
    if (Test-Path $wt) { $d = ((git -C $wt status --porcelain) | Measure-Object -Line).Lines; Write-Output "dirty=$d" } else { Write-Output "dirty=0" }
  }
  "merge"  {
    git -C $Repo checkout $Base | Out-Host
    git -C $Repo merge --no-ff $Branch -m "Simphony merge $Identifier" | Out-Host
    Write-Output "merged $Branch -> $Base"
  }
  "remove" {
    git -C $Repo worktree remove $wt --force
    Write-Output "removed $wt"
  }
  default  { throw "unknown command: $Cmd" }
}
