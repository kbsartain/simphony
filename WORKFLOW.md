---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: simphony-2172572a4807
  active_states:
    - Backlog
    - Todo
    - In Progress
    - Merge and Commit
  working_state: In Progress
  completion_states:
    - In Review
    - Review
    - Done
    - Completed
pipeline:
  review_state: In Review
  merge_state: Merge and Commit
  done_state: Done
polling:
  interval_ms: 30000
workspace:
  root: ./simphony_workspaces
  mode: git_worktree
  repo: .
  base_branch: main
  branch_prefix: simphony/
  cleanup_worktrees: false
hooks:
  before_run: >
    powershell -NoProfile -ExecutionPolicy Bypass -File C:\Users\kbsar\simphony\scripts\setup-workspace.ps1 -WorkspacePath .
  timeout_ms: 300000
agent:
  max_concurrent_agents: 10
  max_turns: 20
  max_retry_backoff_ms: 300000
codex:
  command: C:\Users\kbsar\AppData\Local\Microsoft\WinGet\Packages\OpenAI.Codex_Microsoft.Winget.Source_8wekyb3d8bbwe\codex-x86_64-pc-windows-msvc.exe app-server
  approval_policy: never
  thread_sandbox: danger-full-access
  turn_sandbox_policy: danger-full-access
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
server:
  port: 8080
---

You are working on issue {{ issue.identifier }}: {{ issue.title }}.

Priority: {{ issue.priority }}
State: {{ issue.state }}
Labels: {{ issue.labels | join: ", " }}

Description:
{{ issue.description }}

Please implement the necessary changes to resolve this issue.
Follow the project's coding standards and write tests where appropriate.
