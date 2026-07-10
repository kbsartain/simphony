#!/usr/bin/env bash
# Simphony worktree helper — per-issue git worktree isolation.
# Mirrors internal/workspace/manager.go (mode: git_worktree).
#
# Usage:
#   worktree.sh prepare <identifier> [base_branch] [branch_prefix] [root] [repo]
#   worktree.sh path    <identifier> [root]
#   worktree.sh merge   <identifier> [base_branch] [branch_prefix] [root] [repo]  # merge issue branch into base
#   worktree.sh remove  <identifier> [root] [repo]
#
# Branch name: set WT_BRANCH to the tracker's suggested branch (e.g. Linear's
# `gitBranchName`) to use it verbatim; otherwise it's `<branch_prefix><slug>`.
# Base: `prepare` fetches and branches off `origin/<base_branch>` (the CURRENT
# remote tip), not a stale local ref — Simphony does its work in worktrees, so
# the local base branch drifts. Falls back to the local base when offline.
#
# Defaults: base_branch=main  branch_prefix=simphony/  root=./simphony_workspaces  repo=.
set -euo pipefail

sanitize() { echo "$1" | tr '[:upper:]' '[:lower:]' | sed 's#[^a-z0-9._-]#-#g'; }

cmd="${1:-}"; ident="${2:-}"
[ -z "$cmd" ] && { echo "usage: worktree.sh <prepare|path|merge|remove> <identifier> ..."; exit 2; }
slug="$(sanitize "$ident")"

case "$cmd" in
  prepare)
    base="${3:-main}"; prefix="${4:-simphony/}"; root="${5:-./simphony_workspaces}"; repo="${6:-.}"
    branch="${WT_BRANCH:-${prefix}${slug}}"
    wt="${root}/${slug}"
    mkdir -p "$root"
    if git -C "$repo" worktree list --porcelain | grep -q "worktree .*/${slug}$"; then
      echo "$wt"; exit 0
    fi
    # Branch off the current remote tip. Fetch first; fall back to the local
    # ref when there's no remote / no network.
    git -C "$repo" fetch --quiet origin "$base" 2>/dev/null || true
    baseref="origin/${base}"
    git -C "$repo" rev-parse --verify --quiet "${baseref}^{commit}" >/dev/null 2>&1 || baseref="$base"
    if git -C "$repo" show-ref --verify --quiet "refs/heads/${branch}"; then
      git -C "$repo" worktree add "$wt" "$branch" >&2
    else
      git -C "$repo" worktree add -b "$branch" "$wt" "$baseref" >&2
    fi
    echo "$wt"
    ;;
  path)
    root="${3:-./simphony_workspaces}"; echo "${root}/${slug}" ;;
  merge)
    base="${3:-main}"; prefix="${4:-simphony/}"; root="${5:-./simphony_workspaces}"; repo="${6:-.}"
    branch="${WT_BRANCH:-${prefix}${slug}}"
    git -C "$repo" checkout "$base" >&2
    git -C "$repo" merge --no-ff "$branch" -m "Simphony merge ${ident}" >&2
    echo "merged ${branch} -> ${base}"
    ;;
  remove)
    root="${3:-./simphony_workspaces}"; repo="${4:-.}"
    wt="${root}/${slug}"
    git -C "$repo" worktree remove "$wt" --force >&2 || rm -rf "$wt"
    echo "removed ${wt}"
    ;;
  *) echo "unknown command: $cmd"; exit 2 ;;
esac
