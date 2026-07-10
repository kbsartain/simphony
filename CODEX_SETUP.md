# Codex App-Server Setup

Simphony runs Codex in app-server mode and communicates with it over stdio using newline-delimited JSON-RPC.

## Install Codex

Install Codex for your platform using the official OpenAI distribution. The configured command must support:

```bash
codex app-server --listen stdio://
```

If `codex` is not on `PATH`, set `codex.command` in `WORKFLOW.md` to the full executable path followed by `app-server`.

On Windows, the combined ChatGPT/Codex Store application may place its packaged `codex.exe` on `PATH` even though background processes cannot execute that package path directly. Older workflows may also pin the retired WinGet package executable and remain stuck on an incompatible CLI version. Simphony detects both cases and resolves them to the newest user-accessible CLI copy under `%LOCALAPPDATA%\OpenAI\Codex\bin` or `%USERPROFILE%\.codex`. Open the ChatGPT/Codex app once after an update so it can stage the user-accessible copy. Preflight reports `agent_command_not_executable` when no usable copy is available.

```yaml
codex:
  command: codex app-server
```

On Windows, a full path can be used:

```yaml
codex:
  command: C:\Path\To\codex.exe app-server
```

Simphony appends `--listen stdio://` automatically when the command does not already include `--listen`.

## Authentication

Configure Codex authentication using the mechanism supported by your Codex installation, or set the required environment variables before starting Simphony.

For example, in PowerShell:

```powershell
$env:OPENAI_API_KEY = "replace-with-your-openai-api-key"
```

## Runtime Behavior

For each issue, the agent runner:

1. Starts the Codex app-server subprocess.
2. Sets the subprocess working directory to the issue workspace.
3. Sends `initialize`.
4. Sends `thread/start`.
5. Sends `turn/start` with the rendered issue prompt.
6. Streams notifications back to the orchestrator.
7. Starts continuation turns while the tracker says the issue is still active.

The runner rejects interactive user-input requests because Simphony is intended to run unattended.

## JSON Protocol Schemas

Codex protocol schemas are checked into `codex-schema/` for reference. Important files include:

- `codex_app_server_protocol.v2.schemas.json`
- `JSONRPCMessage.json`
- `ClientRequest.json`
- `ServerNotification.json`
- `v2/ThreadStartParams.json`
- `v2/TurnStartParams.json`

Regenerate schemas with your local Codex binary when needed:

```bash
codex app-server generate-json-schema --out ./codex-schema
```

On Windows with a full executable path:

```powershell
& "C:\Path\To\codex.exe" app-server generate-json-schema --out ".\codex-schema"
```
