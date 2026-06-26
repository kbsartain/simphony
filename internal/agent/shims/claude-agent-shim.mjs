import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import path from "node:path";
import process from "node:process";
import readline from "node:readline";

const rl = readline.createInterface({
  input: process.stdin,
  crlfDelay: Infinity,
});

let input = "";
for await (const line of rl) {
  input += line;
}

const request = JSON.parse(input || "{}");

function emit(event) {
  process.stdout.write(JSON.stringify({
    timestamp: new Date().toISOString(),
    ...event,
  }) + "\n");
}

async function loadSDK(cwd) {
  const candidates = [
    process.env.SIMPHONY_CLAUDE_SDK_PACKAGE,
    "@anthropic-ai/claude-agent-sdk",
    "@anthropic-ai/claude-code",
  ].filter(Boolean);
  const requireFromCwd = createRequire(path.join(cwd || process.cwd(), "package.json"));
  let lastError;
  for (const candidate of candidates) {
    try {
      const resolved = requireFromCwd.resolve(candidate);
      return await import(pathToFileURL(resolved).href);
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError || new Error("Claude Agent SDK package was not found");
}

function textFromContent(content) {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((part) => {
      if (typeof part === "string") {
        return part;
      }
      if (part && typeof part.text === "string") {
        return part.text;
      }
      return "";
    })
    .filter(Boolean)
    .join("");
}

function usageFrom(message) {
  const usage = message?.usage || message?.message?.usage || message?.total_usage;
  if (!usage || typeof usage !== "object") {
    return undefined;
  }
  const inputTokens = usage.input_tokens ?? usage.inputTokens;
  const outputTokens = usage.output_tokens ?? usage.outputTokens;
  const totalTokens = usage.total_tokens ?? usage.totalTokens ??
    ((Number.isFinite(inputTokens) && Number.isFinite(outputTokens)) ? inputTokens + outputTokens : undefined);
  const normalized = {};
  if (Number.isFinite(inputTokens)) {
    normalized.input_tokens = inputTokens;
  }
  if (Number.isFinite(outputTokens)) {
    normalized.output_tokens = outputTokens;
  }
  if (Number.isFinite(totalTokens)) {
    normalized.total_tokens = totalTokens;
  }
  return Object.keys(normalized).length > 0 ? normalized : undefined;
}

function sessionIDFrom(message) {
  return message?.session_id || message?.sessionId || message?.session?.id || "";
}

function messageText(message) {
  if (typeof message?.result === "string") {
    return message.result;
  }
  if (typeof message?.text === "string") {
    return message.text;
  }
  return textFromContent(message?.message?.content || message?.content);
}

async function main() {
  const cwd = request.cwd || process.cwd();
  const sdk = await loadSDK(cwd);
  const query = sdk.query || sdk.default?.query;
  if (typeof query !== "function") {
    throw new Error("Claude Agent SDK did not export query()");
  }

  const options = {
    cwd,
    model: request.model || undefined,
    resume: request.resume_session_id || undefined,
    permissionMode: request.permission_mode || undefined,
    allowedTools: request.allowed_tools,
    disallowedTools: request.disallowed_tools,
    settingSources: request.setting_sources,
  };
  Object.keys(options).forEach((key) => options[key] === undefined && delete options[key]);

  let sessionID = request.resume_session_id || "";
  let started = false;
  let lastText = "";
  const stream = query({ prompt: request.prompt || "", options });

  for await (const message of stream) {
    const nextSessionID = sessionIDFrom(message);
    if (nextSessionID) {
      sessionID = nextSessionID;
    }
    if (!started) {
      started = true;
      emit({
        event: "session_started",
        payload: {
          session_id: sessionID || `claude-${Date.now()}`,
          thread_id: sessionID || undefined,
          turn_id: `turn-${request.turn_count || 1}`,
          turn_count: request.turn_count || 1,
        },
      });
    }

    const usage = usageFrom(message);
    if (usage) {
      emit({
        event: "thread/tokenUsage/updated",
        usage,
        payload: {
          session_id: sessionID,
          turn_count: request.turn_count || 1,
        },
      });
    }

    const text = messageText(message).trim();
    if (text) {
      lastText = text;
      emit({
        event: "item/completed",
        payload: {
          session_id: sessionID,
          item: {
            type: "agentMessage",
            text,
          },
        },
      });
    }
  }

  if (!started) {
    sessionID = sessionID || `claude-${Date.now()}`;
    emit({
      event: "session_started",
      payload: {
        session_id: sessionID,
        thread_id: sessionID,
        turn_id: `turn-${request.turn_count || 1}`,
        turn_count: request.turn_count || 1,
      },
    });
  }

  emit({
    event: "turn/completed",
    payload: {
      session_id: sessionID,
      thread_id: sessionID,
      turn_id: `turn-${request.turn_count || 1}`,
      status: "completed",
      message: lastText,
      turn_count: request.turn_count || 1,
    },
  });
}

main().catch((error) => {
  emit({
    event: "error",
    error: error?.message || String(error),
  });
  process.exitCode = 1;
});
