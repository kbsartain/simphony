---
agent:
    max_concurrent_agents: 10
    max_retry_backoff_ms: 300000
    max_turns: 20
codex:
    approval_policy: never
    command: C:\Users\kbsar\AppData\Local\Microsoft\WinGet\Packages\OpenAI.Codex_Microsoft.Winget.Source_8wekyb3d8bbwe\codex-x86_64-pc-windows-msvc.exe app-server
    read_timeout_ms: 5000
    reasoning_effort: high
    stall_timeout_ms: 300000
    stage_overrides:
        coding:
            reasoning_effort: high
        review:
            reasoning_effort: xhigh
        merge:
            reasoning_effort: high
    thread_sandbox: danger-full-access
    turn_sandbox_policy: danger-full-access
    turn_timeout_ms: 3.6e+06
hooks:
    before_run: |
        powershell -NoProfile -ExecutionPolicy Bypass -File C:\Users\kbsar\simphony\scripts\setup-workspace.ps1 -WorkspacePath .
    timeout_ms: 300000
pipeline:
    done_state: Done
    merge_state: Approved
    review_state: In Review
polling:
    interval_ms: 30000
server:
    port: 8080
tracker:
    active_states:
        - Backlog
        - Todo
        - In Progress
        - Approved
    api_key: $LINEAR_API_KEY
    completion_states:
        - In Review
        - Review
        - Done
        - Completed
    kind: linear
    project_slug: simphony-2172572a4807
    working_state: In Progress
workspace:
    base_branch: main
    branch_prefix: simphony/
    cleanup_worktrees: false
    mode: git_worktree
    repo: .
    root: ./simphony_workspaces
---

You are working on issue {{ issue.identifier }}: {{ issue.title }}.

Priority: {{ issue.priority }}
State: {{ issue.state }}
Labels: {{ issue.labels | join: ", " }}

Description:
{{ issue.description }}

Please implement the necessary changes to resolve this issue.
Follow the project's coding standards and write tests where appropriate.
