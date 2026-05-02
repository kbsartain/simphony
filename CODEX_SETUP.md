# Codex Setup Reference

## Installation
Codex CLI v0.128.0 was installed via winget:
```
winget install --id OpenAI.Codex
```

## Windows Executable Path
The actual binary on this system is:
```
C:\Users\kbsar\AppData\Local\Microsoft\WinGet\Packages\OpenAI.Codex_Microsoft.Winget.Source_8wekyb3d8bbwe\codex-x86_64-pc-windows-msvc.exe
```

**Note:** The `codex` alias is not directly available on Windows because the executable includes the target triple in its name. The WORKFLOW.md has been updated with the full path.

## JSON Protocol Schemas
Generated schemas are available at:
```
C:\Users\kbsar\simphony\codex-schema\
```

Key files for agent runner implementation:
- `codex_app_server_protocol.v2.schemas.json` — Master schema bundle
- `v2/ThreadStartParams.json` — Thread initialization parameters
- `v2/TurnStartParams.json` — Turn start parameters
- `JSONRPCMessage.json` — Transport framing
- `ClientRequest.json` / `ServerNotification.json` — Request/notification shapes

Generate updated schemas anytime with:
```powershell
$codex = "C:\Users\kbsar\AppData\Local\Microsoft\WinGet\Packages\OpenAI.Codex_Microsoft.Winget.Source_8wekyb3d8bbwe\codex-x86_64-pc-windows-msvc.exe"
& $codex app-server generate-json-schema --out "$env:USERPROFILE\simphony\codex-schema"
```

## Required Environment Variable
Before running simphony, set your OpenAI API key:
```powershell
$env:OPENAI_API_KEY = "sk-..."
```

Or configure it in `~/.codex/config.toml` (see Codex docs).

## App-Server Mode
The Symphony agent runner launches Codex in app-server mode over stdio:
```
codex-x86_64-pc-windows-msvc.exe app-server --listen stdio://
```

Protocol transport is JSON-RPC over stdio. The Go agent runner must:
1. Start the subprocess with cwd = workspace path
2. Read/write JSON-RPC messages on stdin/stdout
3. Keep stderr separate for diagnostics
4. Handle approval requests, tool calls, and turn lifecycle events

## Windows-Specific Notes
- Subprocess spawning should use `cmd /C` or direct `.exe` execution rather than `bash -lc`
- The spec mentions `bash -lc` as the POSIX default; on Windows we invoke the executable directly
- Sandbox and approval policies are passed as Codex config values (`-c` flags or config.toml)
