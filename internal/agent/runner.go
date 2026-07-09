// Package agent implements the Codex app-server client over stdio.
// It handles session startup, turn execution, continuation turns, and event streaming.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

	"github.com/kbsartain/simphony/internal/prompt"
	"github.com/kbsartain/simphony/pkg/api"
)

// Runner implements orchestrator.AgentRunner by speaking the Codex app-server
// protocol over stdio (NDJSON JSON-RPC).
type Runner struct {
	mu             sync.RWMutex
	promptRenderer *prompt.Renderer
	promptTemplate string
}

// NewRunner creates a new Runner with the given workflow prompt template.
func NewRunner(promptTemplate string) *Runner {
	return &Runner{
		promptRenderer: prompt.NewRenderer(),
		promptTemplate: promptTemplate,
	}
}

// jsonRPCError represents an error object in a JSON-RPC response.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonRPCMsg is a generic JSON-RPC message shape used for parsing off the wire.
type jsonRPCMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

// threadStartResult is a minimal shape to extract the thread id.
type threadStartResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// turnStartResult is a minimal shape to extract the turn id.
type turnStartResult struct {
	Turn struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

// turnCompletedParams is the params shape for turn/completed notification.
type turnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
}

// tokenUsageParams is the params shape for thread/tokenUsage/updated.
type tokenUsageParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Usage    *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	TokenUsage *struct {
		Total struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
			TotalTokens  int `json:"totalTokens"`
		} `json:"total"`
	} `json:"tokenUsage,omitempty"`
}

// dynamicToolCallParams is the params shape for item/tool/call requests.
type dynamicToolCallParams struct {
	ThreadID  string      `json:"threadId"`
	TurnID    string      `json:"turnId"`
	Tool      string      `json:"tool"`
	CallID    string      `json:"callId"`
	Arguments interface{} `json:"arguments"`
}

type skillsListResult struct {
	Data []struct {
		Skills []struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			Enabled bool   `json:"enabled"`
		} `json:"skills"`
	} `json:"data"`
}

var requestIDCounter int64

func nextRequestID() int {
	return int(atomic.AddInt64(&requestIDCounter, 1))
}

// SetPromptTemplate updates the workflow prompt template for future runs.
func (r *Runner) SetPromptTemplate(template string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.promptTemplate = template
}

// Run launches a Codex app-server subprocess, initializes a thread, starts a
// turn, and blocks until the turn completes or an error occurs.
func (r *Runner) Run(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.AgentRuntimeConfig, stage api.PipelineStage, maxTurns int, shouldContinue func() (api.ContinueDecision, error), eventCallback func(api.AgentEvent)) error {
	if workspace == nil || workspace.Path == "" {
		return fmt.Errorf("%s: workspace path is empty", api.ErrInvalidWorkspaceCWD)
	}
	if cfg == nil {
		return fmt.Errorf("%s: agent runtime config is nil", api.ErrCodexNotFound)
	}
	effectiveCfg := effectiveCodexConfig(cfg, stage)
	cfg = &effectiveCfg
	if strings.EqualFold(cfg.Provider, "claude") {
		return r.runClaude(ctx, issue, workspace, attempt, cfg, stage, maxTurns, shouldContinue, eventCallback)
	}
	if maxTurns <= 0 {
		maxTurns = 1
	}

	// Build subprocess command.
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return fmt.Errorf("%s: command is empty", api.ErrCodexNotFound)
	}

	if !strings.Contains(command, "--listen") {
		command += " --listen stdio://"
	}

	cmd := shellCommandContext(ctx, command)
	cmd.Dir = workspace.Path
	cmd.Stderr = os.Stderr
	if err := configureSubprocessEnv(cmd, workspace.Path, cfg); err != nil {
		return err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%s: failed to create stdin pipe: %w", api.ErrCodexNotFound, err)
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: failed to create stdout pipe: %w", api.ErrCodexNotFound, err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: failed to start codex: %w", api.ErrCodexNotFound, err)
	}

	// Ensure the process is cleaned up when Run returns.
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	if cmd.Process != nil {
		pid := fmt.Sprintf("%d", cmd.Process.Pid)
		eventCallback(api.AgentEvent{
			Event:     "process_started",
			Timestamp: time.Now(),
			CodexPID:  &pid,
		})
	}

	// Start the NDJSON reader goroutine.
	msgCh := make(chan jsonRPCMsg, 64)
	readErrCh := make(chan error, 1)
	var readWg sync.WaitGroup
	readWg.Add(1)
	go func() {
		defer readWg.Done()
		defer close(msgCh)

		scanner := bufio.NewScanner(stdout)
		const maxCapacity = 10 * 1024 * 1024 // 10MB
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			line := scanner.Bytes()
			var m jsonRPCMsg
			if err := json.Unmarshal(line, &m); err != nil {
				// Log and continue on parse errors per robustness principle.
				log.Printf("action=agent_runner warning=json_parse error=%v", err)
				continue
			}
			select {
			case msgCh <- m:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case readErrCh <- err:
			case <-ctx.Done():
			}
		}
	}()

	// Helper to dispatch a notification to the event callback.
	dispatchNotification := func(m jsonRPCMsg) {
		if m.Method == "" {
			return
		}
		event := api.AgentEvent{
			Event:     m.Method,
			Timestamp: time.Now(),
			Payload:   map[string]interface{}{},
		}
		var paramsMap map[string]interface{}
		_ = json.Unmarshal(m.Params, &paramsMap)
		if paramsMap != nil {
			event.Payload = paramsMap
		}
		if m.Method == "thread/tokenUsage/updated" {
			var tu tokenUsageParams
			if err := json.Unmarshal(m.Params, &tu); err == nil {
				switch {
				case tu.TokenUsage != nil:
					event.Usage = map[string]interface{}{
						"input_tokens":  tu.TokenUsage.Total.InputTokens,
						"output_tokens": tu.TokenUsage.Total.OutputTokens,
						"total_tokens":  tu.TokenUsage.Total.TotalTokens,
					}
				case tu.Usage != nil:
					event.Usage = map[string]interface{}{
						"input_tokens":  tu.Usage.InputTokens,
						"output_tokens": tu.Usage.OutputTokens,
						"total_tokens":  tu.Usage.TotalTokens,
					}
				}
			}
		}
		eventCallback(event)
	}

	// sendRequest writes a JSON-RPC request and waits for the matching response.
	sendRequest := func(method string, params interface{}) (json.RawMessage, error) {
		id := nextRequestID()
		req := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		}
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		data = append(data, '\n')
		if _, err := stdin.Write(data); err != nil {
			return nil, fmt.Errorf("%s: failed to write %s request: %w", api.ErrResponseError, method, err)
		}

		readTimeout := time.Duration(cfg.ReadTimeoutMs) * time.Millisecond
		if readTimeout <= 0 {
			readTimeout = 5 * time.Second
		}
		timer := time.NewTimer(readTimeout)
		defer timer.Stop()

		for {
			select {
			case m, ok := <-msgCh:
				if !ok {
					return nil, fmt.Errorf("%s: connection closed while waiting for %s response", api.ErrPortExit, method)
				}
				// Match by ID. IDs are integers in our requests.
				if m.ID != nil {
					var mid float64
					switch v := m.ID.(type) {
					case float64:
						mid = v
					case int:
						mid = float64(v)
					case int64:
						mid = float64(v)
					}
					if int(mid) == id {
						if m.Error != nil {
							return nil, fmt.Errorf("%s: %s error: %s", api.ErrResponseError, method, m.Error.Message)
						}
						return m.Result, nil
					}
				}
				// Handle notifications and unmatched messages.
				if m.Method != "" {
					dispatchNotification(m)
				}
			case err := <-readErrCh:
				return nil, fmt.Errorf("%s: read error during %s: %w", api.ErrResponseTimeout, method, err)
			case <-timer.C:
				return nil, fmt.Errorf("%s: timeout waiting for %s response", api.ErrResponseTimeout, method)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	// sendResponse writes a JSON-RPC response (for server requests).
	sendResponse := func(id interface{}, result interface{}) error {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  result,
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		_, err = stdin.Write(data)
		return err
	}

	// sendErrorResponse writes a JSON-RPC error response.
	sendErrorResponse := func(id interface{}, code int, message string) error {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    code,
				"message": message,
			},
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		_, err = stdin.Write(data)
		return err
	}

	// 1. Initialize
	initParams := map[string]interface{}{
		"clientInfo": map[string]interface{}{
			"name":    "simphony",
			"version": "0.1.0",
		},
	}
	if _, err := sendRequest("initialize", initParams); err != nil {
		return err
	}

	skills, unresolvedSkills := resolveConfiguredSkills(workspace, cfg, sendRequest)

	// 2. Start thread
	threadParams := buildThreadStartParams(workspace, cfg, issue)
	threadResultRaw, err := sendRequest("thread/start", threadParams)
	if err != nil {
		return err
	}
	var threadResult threadStartResult
	if err := json.Unmarshal(threadResultRaw, &threadResult); err != nil {
		return fmt.Errorf("%s: failed to parse thread/start response: %w", api.ErrResponseError, err)
	}
	threadID := threadResult.Thread.ID
	if threadID == "" {
		return fmt.Errorf("%s: thread/start response missing thread id", api.ErrResponseError)
	}

	for turnCount := 1; turnCount <= maxTurns; turnCount++ {
		turnPrompt, err := r.turnPrompt(issue, attempt, stage, turnCount)
		if err != nil {
			return err
		}

		turnParams := buildTurnStartParams(threadID, workspace, cfg, turnPrompt, skills, unresolvedSkills)
		turnResultRaw, err := sendRequest("turn/start", turnParams)
		if err != nil {
			return err
		}
		var turnResult turnStartResult
		if err := json.Unmarshal(turnResultRaw, &turnResult); err != nil {
			return fmt.Errorf("%s: failed to parse turn/start response: %w", api.ErrResponseError, err)
		}
		turnID := turnResult.Turn.ID
		if turnID == "" {
			return fmt.Errorf("%s: turn/start response missing turn id", api.ErrResponseError)
		}

		sessionID := fmt.Sprintf("%s-%s", threadID, turnID)
		eventCallback(api.AgentEvent{
			Event:     "session_started",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"session_id": sessionID,
				"thread_id":  threadID,
				"turn_id":    turnID,
				"turn_count": turnCount,
			},
		})

		if err := waitForTurn(ctx, issue.ID, sessionID, turnID, cfg, msgCh, readErrCh, dispatchNotification, sendResponse, sendErrorResponse); err != nil {
			return err
		}

		if turnCount == maxTurns && shouldContinue != nil {
			eventCallback(api.AgentEvent{
				Event:     "max_turns_reached",
				Timestamp: time.Now(),
				Payload: map[string]interface{}{
					"turn_count": turnCount,
				},
			})
			return fmt.Errorf("%s: reached %d turns", api.ErrMaxTurnsReached, maxTurns)
		}
		if shouldContinue == nil {
			return nil
		}

		decision, err := shouldContinue()
		if err != nil {
			return fmt.Errorf("%s: continuation state check failed: %w", api.ErrResponseError, err)
		}
		if !decision.Continue {
			eventCallback(api.AgentEvent{
				Event:     "continuation_stopped",
				Timestamp: time.Now(),
				Payload: map[string]interface{}{
					"reason":      decision.Reason,
					"issue_state": decision.Issue.State,
				},
			})
			return nil
		}
		issue = decision.Issue
	}

	return nil
}

// claudeStreamEvent is a single line from Claude Code's --output-format stream-json.
type claudeStreamEvent struct {
	Type           string         `json:"type"`
	SubType        string         `json:"subtype"`
	SessionID      string         `json:"session_id"`
	Message        *claudeMessage `json:"message"`
	Result         string         `json:"result"`
	Usage          *claudeUsage   `json:"usage"`
	Error          string         `json:"error"`
	IsError        bool           `json:"is_error"`
	APIErrorStatus int            `json:"api_error_status"`
}

type claudeMessage struct {
	ID      string               `json:"id"`
	Content []claudeContentBlock `json:"content"`
	Usage   *claudeUsage         `json:"usage"`
	Text    string               `json:"text"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type claudeResultEvent struct {
	SubType        string       `json:"subtype"`
	IsError        bool         `json:"is_error"`
	Result         string       `json:"result"`
	APIErrorStatus int          `json:"api_error_status"`
	Usage          *claudeUsage `json:"usage"`
	SessionID      string       `json:"session_id"`
}

func (r *Runner) runClaude(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.AgentRuntimeConfig, stage api.PipelineStage, maxTurns int, shouldContinue func() (api.ContinueDecision, error), eventCallback func(api.AgentEvent)) error {
	if maxTurns <= 0 {
		maxTurns = 1
	}
	sessionID := ""
	for turnCount := 1; turnCount <= maxTurns; turnCount++ {
		turnPrompt, err := r.turnPrompt(issue, attempt, stage, turnCount)
		if err != nil {
			return err
		}
		nextSessionID, err := r.runClaudeTurn(ctx, workspace, cfg, turnPrompt, sessionID, turnCount, eventCallback)
		if err != nil {
			return err
		}
		if nextSessionID != "" {
			sessionID = nextSessionID
		}

		if turnCount == maxTurns && shouldContinue != nil {
			eventCallback(api.AgentEvent{
				Event:     "max_turns_reached",
				Timestamp: time.Now(),
				Payload: map[string]interface{}{
					"turn_count": turnCount,
					"provider":   "claude",
				},
			})
			return fmt.Errorf("%s: reached %d turns", api.ErrMaxTurnsReached, maxTurns)
		}
		if shouldContinue == nil {
			return nil
		}

		decision, err := shouldContinue()
		if err != nil {
			return fmt.Errorf("%s: continuation state check failed: %w", api.ErrResponseError, err)
		}
		if !decision.Continue {
			eventCallback(api.AgentEvent{
				Event:     "continuation_stopped",
				Timestamp: time.Now(),
				Payload: map[string]interface{}{
					"reason":      decision.Reason,
					"issue_state": decision.Issue.State,
					"provider":    "claude",
				},
			})
			return nil
		}
		issue = decision.Issue
	}
	return nil
}

func (r *Runner) runClaudeTurn(ctx context.Context, workspace *api.Workspace, cfg *api.AgentRuntimeConfig, prompt string, resumeSessionID string, turnCount int, eventCallback func(api.AgentEvent)) (string, error) {
	claudePath := strings.TrimSpace(cfg.Command)
	var baseArgs []string
	if claudePath == "" {
		claudePath = findClaudeBinary()
	} else {
		// cfg.Command may include pre-existing arguments (e.g. test mock).
		parts := strings.Fields(claudePath)
		if len(parts) > 0 {
			claudePath = parts[0]
			baseArgs = parts[1:]
		}
	}
	if claudePath == "" {
		return "", fmt.Errorf("%s: claude binary not found on PATH", api.ErrCodexNotFound)
	}

	turnTimeout := time.Duration(cfg.TurnTimeoutMs) * time.Millisecond
	if turnTimeout <= 0 {
		turnTimeout = 3600 * time.Second
	}
	turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()

	args := append(baseArgs, []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--permission-mode", "bypassPermissions",
		"--bare",
	}...)
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowed-tools", strings.Join(cfg.AllowedTools, ","))
	}
	if len(cfg.DisallowedTools) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(cfg.DisallowedTools, ","))
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	// Terminate option parsing before the positional prompt. The Claude CLI's
	// --allowed-tools/--disallowed-tools flags are variadic (<tools...>) and will
	// otherwise consume the trailing prompt as additional tool values, leaving
	// claude with no input ("Input must be provided ... when using --print",
	// exit status 1).
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(turnCtx, claudePath, args...)
	cmd.Dir = workspace.Path
	cmd.Stderr = os.Stderr

	// Set environment. scrubInheritedAgentEnv has already stripped any inherited
	// ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN, so we start from a clean slate and
	// set exactly one auth header's worth of credential.
	env := scrubInheritedAgentEnv(os.Environ())
	if cfg.EndpointURL != "" {
		env = setEnv(env, "ANTHROPIC_BASE_URL", cfg.EndpointURL)
	}
	// Resolve the credential; an explicit auth_token wins over api_key.
	credential := cfg.AuthToken
	if credential == "" {
		credential = cfg.APIKey
	}
	if credential != "" {
		// The claude CLI emits BOTH the x-api-key and Authorization headers when
		// both env vars are set. Anthropic-compatible gateways (z.ai, Kimi)
		// reject that ambiguous dual-auth request, so send only the header the
		// endpoint expects: bearer (ANTHROPIC_AUTH_TOKEN) for compatible
		// endpoints, x-api-key (ANTHROPIC_API_KEY) for native Anthropic.
		if isNativeAnthropicEndpoint(cfg.EndpointURL) {
			env = setEnv(env, "ANTHROPIC_API_KEY", credential)
		} else {
			env = setEnv(env, "ANTHROPIC_AUTH_TOKEN", credential)
		}
	}
	for key, value := range cfg.Env {
		env = setEnv(env, key, value)
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("%s: failed to create stdout pipe: %w", api.ErrCodexNotFound, err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: failed to start claude: %w", api.ErrCodexNotFound, err)
	}

	waited := false
	defer func() {
		if !waited && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	if cmd.Process != nil {
		pid := fmt.Sprintf("%d", cmd.Process.Pid)
		eventCallback(api.AgentEvent{
			Event:     "process_started",
			Timestamp: time.Now(),
			CodexPID:  &pid,
			Payload: map[string]interface{}{
				"provider": "claude",
			},
		})
	}

	scanner := bufio.NewScanner(stdout)
	const maxCapacity = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	sessionID := resumeSessionID
	var resultEvent *claudeResultEvent
	completed := false
	started := false

	for scanner.Scan() {
		var event claudeStreamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			log.Printf("action=claude_runner warning=json_parse error=%v", err)
			continue
		}

		// Handle error events (not result-level errors)
		if event.Error != "" && event.Type != "result" {
			return sessionID, fmt.Errorf("%s: claude error: %s", api.ErrTurnFailed, event.Error)
		}

		switch event.Type {
		case "system":
			if event.SubType == "init" && event.SessionID != "" {
				sessionID = event.SessionID
				if !started {
					started = true
					eventCallback(api.AgentEvent{
						Event:     "session_started",
						Timestamp: time.Now(),
						Payload: map[string]interface{}{
							"session_id": sessionID,
							"turn_count": turnCount,
							"provider":   "claude",
						},
					})
				}
			}

		case "assistant":
			if event.Message != nil {
				if !started {
					started = true
					if sessionID == "" {
						sessionID = event.SessionID
					}
					eventCallback(api.AgentEvent{
						Event:     "session_started",
						Timestamp: time.Now(),
						Payload: map[string]interface{}{
							"session_id": sessionID,
							"turn_count": turnCount,
							"provider":   "claude",
						},
					})
				}
				text := extractTextFromMessage(event.Message)
				if text != "" {
					eventCallback(api.AgentEvent{
						Event:     "item/completed",
						Timestamp: time.Now(),
						Payload: map[string]interface{}{
							"session_id": sessionID,
							"item": map[string]interface{}{
								"type": "agentMessage",
								"text": text,
							},
							"turn_count": turnCount,
							"provider":   "claude",
						},
					})
				}
				if event.Message.Usage != nil {
					emitClaudeUsage(eventCallback, event.Message.Usage, sessionID, turnCount)
				}
			}

		case "result":
			resultEvent = &claudeResultEvent{
				SubType:        event.SubType,
				IsError:        event.IsError,
				Result:         event.Result,
				APIErrorStatus: event.APIErrorStatus,
				Usage:          event.Usage,
				SessionID:      event.SessionID,
			}
			if event.SessionID != "" {
				sessionID = event.SessionID
			}
			if !started {
				started = true
				eventCallback(api.AgentEvent{
					Event:     "session_started",
					Timestamp: time.Now(),
					Payload: map[string]interface{}{
						"session_id": sessionID,
						"turn_count": turnCount,
						"provider":   "claude",
					},
				})
			}
			eventCallback(api.AgentEvent{
				Event:     "turn/completed",
				Timestamp: time.Now(),
				Payload: map[string]interface{}{
					"session_id": sessionID,
					"status":     "completed",
					"turn_count": turnCount,
					"provider":   "claude",
				},
			})
			if event.Usage != nil {
				emitClaudeUsage(eventCallback, event.Usage, sessionID, turnCount)
			}
			completed = true
		}
	}

	if err := scanner.Err(); err != nil {
		return sessionID, fmt.Errorf("%s: read error during claude turn: %w", api.ErrResponseTimeout, err)
	}

	waitErr := cmd.Wait()
	waited = true
	if waitErr != nil {
		if turnCtx.Err() == context.DeadlineExceeded {
			return sessionID, fmt.Errorf("%s: claude turn exceeded %d ms", api.ErrTurnTimeout, cfg.TurnTimeoutMs)
		}
		if ctx.Err() != nil {
			return sessionID, ctx.Err()
		}
		if resultEvent != nil && resultEvent.IsError {
			return sessionID, fmt.Errorf("%s: %s", api.ErrTurnFailed, resultEvent.Result)
		}
		return sessionID, fmt.Errorf("%s: claude exited: %w", api.ErrPortExit, waitErr)
	}

	if resultEvent != nil && resultEvent.IsError {
		return sessionID, fmt.Errorf("%s: %s", api.ErrTurnFailed, resultEvent.Result)
	}

	if !completed {
		return sessionID, fmt.Errorf("%s: claude exited before turn completion", api.ErrPortExit)
	}

	return sessionID, nil
}

func extractTextFromMessage(msg *claudeMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if msg.Text != "" {
		return msg.Text
	}
	return ""
}

func emitClaudeUsage(cb func(api.AgentEvent), usage *claudeUsage, sessionID string, turnCount int) {
	if usage == nil {
		return
	}
	u := map[string]interface{}{}
	if usage.InputTokens > 0 {
		u["input_tokens"] = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		u["output_tokens"] = usage.OutputTokens
	}
	if usage.TotalTokens > 0 {
		u["total_tokens"] = usage.TotalTokens
	} else if usage.InputTokens > 0 && usage.OutputTokens > 0 {
		u["total_tokens"] = usage.InputTokens + usage.OutputTokens
	}
	if len(u) > 0 {
		cb(api.AgentEvent{
			Event:     "thread/tokenUsage/updated",
			Timestamp: time.Now(),
			Usage:     u,
			Payload: map[string]interface{}{
				"session_id": sessionID,
				"turn_count": turnCount,
				"provider":   "claude",
			},
		})
	}
}

func writeCodexWorkspaceConfig(codexHome string, cfg *api.AgentRuntimeConfig) error {
	configPath := filepath.Join(codexHome, "config.toml")
	var b strings.Builder
	fmt.Fprintln(&b, `forced_login_method = "api"`)
	fmt.Fprintln(&b, `cli_auth_credentials_store = "file"`)
	if cfg.Model != "" {
		fmt.Fprintf(&b, "model = %q\n", cfg.Model)
	}

	endpoint := cfg.EndpointURL
	if endpoint == "" && cfg.Provider != "" && cfg.Provider != "openai" {
		// Fallback: if no explicit endpoint but provider is non-OpenAI,
		// keep whatever base URL Codex already has configured.
		endpoint = cfg.EndpointURL
	}

	if endpoint != "" {
		// For non-OpenAI endpoints, wire_api must live inside a
		// [model_providers.*] table — a top-level wire_api is ignored.
		if !strings.Contains(endpoint, "openai.com") {
			providerName := strings.ToLower(strings.TrimSpace(cfg.ModelProvider))
			if providerName == "" {
				providerName = strings.ToLower(strings.TrimSpace(cfg.Provider))
			}
			if providerName == "" || providerName == "openai" {
				providerName = "custom"
			}
			fmt.Fprintf(&b, "model_provider = %q\n", providerName)
			fmt.Fprintf(&b, "\n[model_providers.%s]\n", providerName)
			fmt.Fprintf(&b, "name = %q\n", providerName)
			fmt.Fprintf(&b, "base_url = %q\n", endpoint)
			fmt.Fprintln(&b, `wire_api = "chat"`)
			fmt.Fprintln(&b, `requires_openai_auth = true`)
		} else {
			fmt.Fprintf(&b, "openai_base_url = %q\n", endpoint)
		}
	}
	if err := os.WriteFile(configPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	if cfg.APIKey != "" {
		authPath := filepath.Join(codexHome, "auth.json")
		auth := map[string]string{"api_key": cfg.APIKey}
		data, err := json.Marshal(auth)
		if err != nil {
			return err
		}
		if err := os.WriteFile(authPath, data, 0600); err != nil {
			return err
		}
		log.Printf("action=writeCodexWorkspaceConfig codex_home=%s provider=%s api_key_configured=true", codexHome, cfg.Provider)
	} else {
		log.Printf("action=writeCodexWorkspaceConfig codex_home=%s provider=%s api_key_configured=false warn=no_api_key", codexHome, cfg.Provider)
	}
	return nil
}

func buildThreadStartParams(workspace *api.Workspace, cfg *api.CodexConfig, issue api.Issue) map[string]interface{} {
	params := map[string]interface{}{
		"cwd": workspace.Path,
	}

	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if cfg.ModelProvider != "" {
		params["modelProvider"] = cfg.ModelProvider
	}

	// Map approval policy. The Codex protocol only accepts specific strings.
	policy := cfg.ApprovalPolicy
	if policy == "auto" {
		policy = "never"
	}
	if policy != "" {
		params["approvalPolicy"] = policy
	}

	// Sandbox mode for thread.
	if cfg.ThreadSandbox != "" && cfg.ThreadSandbox != "none" {
		params["sandbox"] = cfg.ThreadSandbox
	}

	return params
}

func (r *Runner) turnPrompt(issue api.Issue, attempt *int, stage api.PipelineStage, turnCount int) (string, error) {
	if turnCount == 1 {
		switch stage.Kind {
		case "merge":
			return mergePrompt(issue, stage.Instructions), nil
		case "review_resolution":
			return reviewResolutionPrompt(issue, stage.Instructions), nil
		case "review":
			return reviewPrompt(issue, stage.Instructions), nil
		}
		r.mu.RLock()
		template := r.promptTemplate
		r.mu.RUnlock()
		renderedPrompt, err := r.promptRenderer.Render(template, issue, attempt)
		if err != nil {
			return "", fmt.Errorf("%s: %w", api.ErrTemplateRenderError, err)
		}
		return renderedPrompt, nil
	}

	return fmt.Sprintf("Continue working on issue %s. Do not restart from the original task prompt; use the existing thread context, inspect the current workspace state, and proceed with the next useful step.", issue.Identifier), nil
}

func reviewPrompt(issue api.Issue, instructions string) string {
	var b strings.Builder
	if strings.TrimSpace(instructions) == "" {
		instructions = "Perform an internal review of the current workspace changes before human review. Inspect the implementation against the issue, security expectations, architecture guidance, and test coverage. Fix concrete problems you find, run appropriate checks, and leave a concise final summary of what changed and what passed."
	}
	fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(instructions))
	fmt.Fprintf(&b, "Issue: %s - %s\n", issue.Identifier, issue.Title)
	fmt.Fprintf(&b, "State: %s\n", issue.State)
	if issue.Description != nil && strings.TrimSpace(*issue.Description) != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", strings.TrimSpace(*issue.Description))
	}
	fmt.Fprint(&b, "\nWork from the current repository state in this workspace. Preserve the implementation intent, but correct defects, security gaps, missing tests, or drift from project conventions before the issue advances.")
	return b.String()
}

func reviewResolutionPrompt(issue api.Issue, instructions string) string {
	var b strings.Builder
	if strings.TrimSpace(instructions) == "" {
		instructions = "Resolve formal PR/code-review feedback for this issue. Inspect the pull request, unresolved review comments, review decision, and CI/check results. Fix actionable issues, push updates, reply to review comments when appropriate, and escalate only when policy requires it. End your final response with exactly one directive line: SIMPHONY_REVIEW_DECISION: approved, SIMPHONY_REVIEW_DECISION: retry, or SIMPHONY_REVIEW_DECISION: escalate."
	}
	fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(instructions))
	fmt.Fprintf(&b, "Issue: %s - %s\n", issue.Identifier, issue.Title)
	fmt.Fprintf(&b, "State: %s\n", issue.State)
	if issue.Description != nil && strings.TrimSpace(*issue.Description) != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", strings.TrimSpace(*issue.Description))
	}
	fmt.Fprint(&b, "\nWork from the current repository state in this workspace. Prefer the GitHub CLI or configured repository tooling to inspect the active PR and review comments. If all required review and CI conditions are satisfied, use the approved directive. If more review/checks are pending and another autonomous pass should be scheduled, use the retry directive. If policy requires human judgment, use the escalate directive and explain the blocker briefly.")
	fmt.Fprint(&b, "\n\nEnd your final response with exactly one directive line: SIMPHONY_REVIEW_DECISION: approved, SIMPHONY_REVIEW_DECISION: retry, or SIMPHONY_REVIEW_DECISION: escalate.")
	return b.String()
}

func mergePrompt(issue api.Issue, instructions string) string {
	var b strings.Builder
	if strings.TrimSpace(instructions) == "" {
		instructions = "Human review has been approved. Evaluate the existing workspace changes for merging, resolve merge-related issues, run appropriate checks, and commit the final changes to the repository."
	}
	fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(instructions))
	fmt.Fprintf(&b, "Issue: %s - %s\n", issue.Identifier, issue.Title)
	fmt.Fprintf(&b, "State: %s\n", issue.State)
	if issue.Description != nil && strings.TrimSpace(*issue.Description) != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", strings.TrimSpace(*issue.Description))
	}
	fmt.Fprint(&b, "\nWork from the current repository state in this workspace. Preserve the reviewed implementation unless a merge or verification problem requires a fix.")
	return b.String()
}

func waitForTurn(
	ctx context.Context,
	issueID string,
	sessionID string,
	turnID string,
	cfg *api.CodexConfig,
	msgCh <-chan jsonRPCMsg,
	readErrCh <-chan error,
	dispatchNotification func(jsonRPCMsg),
	sendResponse func(interface{}, interface{}) error,
	sendErrorResponse func(interface{}, int, string) error,
) error {
	turnTimeout := time.Duration(cfg.TurnTimeoutMs) * time.Millisecond
	if turnTimeout <= 0 {
		turnTimeout = 3600 * time.Second
	}
	turnTimer := time.NewTimer(turnTimeout)
	defer turnTimer.Stop()

	for {
		select {
		case m, ok := <-msgCh:
			if !ok {
				return fmt.Errorf("%s: subprocess exited during turn", api.ErrPortExit)
			}

			if m.ID != nil && m.Method != "" {
				switch m.Method {
				case "item/tool/call":
					var p dynamicToolCallParams
					_ = json.Unmarshal(m.Params, &p)
					log.Printf("issue_id=%s session_id=%s action=dynamic_tool_call tool=%s status=unsupported", issueID, sessionID, p.Tool)
					_ = sendResponse(m.ID, map[string]interface{}{
						"success":      false,
						"contentItems": []map[string]interface{}{{"type": "inputText", "text": fmt.Sprintf("unsupported tool: %s", p.Tool)}},
					})
				case "item/tool/requestUserInput":
					log.Printf("issue_id=%s session_id=%s action=request_user_input status=rejected", issueID, sessionID)
					_ = sendResponse(m.ID, map[string]interface{}{
						"answers": map[string]interface{}{},
					})
					return fmt.Errorf("%s: user input requested by agent", api.ErrTurnInputRequired)
				default:
					_ = sendErrorResponse(m.ID, -32601, "method not supported")
				}
				continue
			}

			if m.Method != "" {
				dispatchNotification(m)

				switch m.Method {
				case "turn/completed":
					var p turnCompletedParams
					if err := json.Unmarshal(m.Params, &p); err == nil {
						if p.Turn.ID != "" && p.Turn.ID != turnID {
							continue
						}
						switch p.Turn.Status {
						case "completed":
							return nil
						case "failed":
							if p.Turn.Error != nil {
								return fmt.Errorf("%s: turn failed: %s", api.ErrTurnFailed, p.Turn.Error.Message)
							}
							return fmt.Errorf("%s: turn failed", api.ErrTurnFailed)
						case "interrupted":
							return fmt.Errorf("%s: turn interrupted", api.ErrTurnCancelled)
						default:
						}
					}
				case "error":
					var p struct {
						Error struct {
							Message string `json:"message"`
						} `json:"error"`
						ThreadID  string `json:"threadId"`
						TurnID    string `json:"turnId"`
						WillRetry bool   `json:"willRetry"`
					}
					if err := json.Unmarshal(m.Params, &p); err == nil {
						if !p.WillRetry {
							return fmt.Errorf("%s: %s", api.ErrTurnFailed, p.Error.Message)
						}
						log.Printf("issue_id=%s session_id=%s action=error_notification message=%s will_retry=true", issueID, sessionID, p.Error.Message)
					}
				}
			}

		case err := <-readErrCh:
			return fmt.Errorf("%s: read error during turn: %w", api.ErrResponseTimeout, err)

		case <-turnTimer.C:
			return fmt.Errorf("%s: turn exceeded %d ms", api.ErrTurnTimeout, cfg.TurnTimeoutMs)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func shellCommandContext(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "bash", "-lc", command)
}

func quoteCommandArg(arg string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func configureSubprocessEnv(cmd *exec.Cmd, workspacePath string, cfg *api.AgentRuntimeConfig) error {
	env := scrubInheritedAgentEnv(os.Environ())

	gitConfigPath, err := writeGitSafeDirectoryConfig(workspacePath)
	if err != nil {
		return err
	}
	env = setEnv(env, "GIT_CONFIG_GLOBAL", gitConfigPath)

	if rgPath, err := exec.LookPath("rg"); err == nil {
		env = prependPath(env, filepath.Dir(rgPath))
	}
	env = prependWorkspaceToolPaths(env, workspacePath)

			// Isolate Codex config per workspace to prevent cached ChatGPT/OAuth auth
	// from overriding API-key mode. Only needed when using the Codex provider.
	if !strings.EqualFold(cfg.Provider, "claude") {
		codexHome := filepath.Join(workspacePath, ".simphony", "codex")
		if err := os.MkdirAll(codexHome, 0755); err != nil {
			return fmt.Errorf("%s: create codex home dir: %w", api.ErrInvalidWorkspaceCWD, err)
		}
		env = setEnv(env, "CODEX_HOME", codexHome)
		if err := writeCodexWorkspaceConfig(codexHome, cfg); err != nil {
			return err
		}
	}

	env = applyRuntimeEnv(env, cfg)

	cmd.Env = env
	return nil
}

var inheritedAgentEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":    {},
	"ANTHROPIC_AUTH_TOKEN": {},
	"ANTHROPIC_BASE_URL":   {},
	"OPENAI_API_KEY":       {},
	"OPENAI_AUTH_TOKEN":    {},
	"OPENAI_BASE_URL":      {},
	"LINEAR_API_KEY":       {},
}

// isNativeAnthropicEndpoint reports whether url targets Anthropic's own API
// (empty means the CLI default, which is native Anthropic). Native Anthropic
// authenticates via the x-api-key header; other Anthropic-compatible endpoints
// (z.ai, Kimi, ...) authenticate via the Authorization: Bearer header.
func isNativeAnthropicEndpoint(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return u == "" || strings.Contains(u, "api.anthropic.com")
}

func scrubInheritedAgentEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		if _, drop := inheritedAgentEnvKeys[strings.ToUpper(key)]; drop {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func applyRuntimeEnv(env []string, cfg *api.AgentRuntimeConfig) []string {
	if cfg == nil {
		return env
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
		case "claude":
		if cfg.APIKey != "" {
			env = setEnv(env, "ANTHROPIC_API_KEY", cfg.APIKey)
			// Z.AI and some other Anthropic-compatible endpoints expect the
			// key in ANT_AUTH_TOKEN rather than API_KEY.
			env = setEnv(env, "ANTHROPIC_AUTH_TOKEN", cfg.APIKey)
		}
		if cfg.EndpointURL != "" {
			env = setEnv(env, "ANTHROPIC_BASE_URL", cfg.EndpointURL)
		}
		if cfg.AuthToken != "" {
			env = setEnv(env, "ANTHROPIC_AUTH_TOKEN", cfg.AuthToken)
		}
	default:
		if cfg.APIKey != "" {
			env = setEnv(env, "OPENAI_API_KEY", cfg.APIKey)
		}
		if cfg.EndpointURL != "" {
			env = setEnv(env, "OPENAI_BASE_URL", cfg.EndpointURL)
		}
		if cfg.AuthToken != "" {
			env = setEnv(env, "OPENAI_AUTH_TOKEN", cfg.AuthToken)
		}
	}
	for key, value := range cfg.Env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		env = setEnv(env, key, value)
	}
	return env
}

func findClaudeBinary() string {
	// On Windows, LookPath finds .exe, .com, .bat, .cmd.
	// Try claude.exe first for direct execution.
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("claude.exe"); err == nil {
			return p
		}
		// Check known npm global locations for the direct binary.
		candidates := []string{
			filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming", "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
			`C:\Program Files\nodejs\node_modules\@anthropic-ai\claude-code\bin\claude.exe`,
		}
		for _, p := range candidates {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
		// Fall back to the npm wrapper (claude.cmd).
		if p, err := exec.LookPath("claude"); err == nil {
			return p
		}
	} else {
		if p, err := exec.LookPath("claude"); err == nil {
			return p
		}
		candidates := []string{
			"/usr/local/bin/claude",
			"/usr/bin/claude",
			filepath.Join(os.Getenv("HOME"), ".npm-global", "bin", "claude"),
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude"),
			filepath.Join(os.Getenv("HOME"), ".local", "share", "npm", "bin", "claude"),
		}
		for _, p := range candidates {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

func writeGitSafeDirectoryConfig(workspacePath string) (string, error) {
	configDir := filepath.Join(workspacePath, ".simphony")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("%s: create codex git config dir: %w", api.ErrInvalidWorkspaceCWD, err)
	}
	workspacePath = filepath.ToSlash(filepath.Clean(workspacePath))
	content := fmt.Sprintf("[safe]\n\tdirectory = %s\n", workspacePath)
	configPath := filepath.Join(configDir, "gitconfig")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("%s: write codex git config: %w", api.ErrInvalidWorkspaceCWD, err)
	}
	return configPath, nil
}

func setEnv(env []string, key string, value string) []string {
	prefix := strings.ToUpper(key) + "="
	for i, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

func prependPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	pathKey := "PATH"
	if runtime.GOOS == "windows" {
		pathKey = "Path"
	}
	current := getEnv(env, pathKey)
	if current == "" && pathKey != "PATH" {
		current = getEnv(env, "PATH")
	}
	return setEnv(env, pathKey, dir+string(os.PathListSeparator)+current)
}

func getEnv(env []string, key string) string {
	prefix := strings.ToUpper(key) + "="
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			return entry[len(key)+1:]
		}
	}
	return ""
}

func prependWorkspaceToolPaths(env []string, workspacePath string) []string {
	candidates := []string{
		filepath.Join(workspacePath, "node_modules", ".bin"),
		filepath.Join(workspacePath, "dashboard", "node_modules", ".bin"),
	}
	if runtime.GOOS == "windows" {
				candidates = append(candidates, `C:\Program Files\nodejs`)
	}
	for _, dir := range candidates {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			env = prependPath(env, dir)
		}
	}
	return env
}

func buildTurnStartParams(threadID string, workspace *api.Workspace, cfg *api.CodexConfig, prompt string, skills []api.CodexSkillRef, unresolvedSkills []string) map[string]interface{} {
	params := map[string]interface{}{
		"threadId": threadID,
		"cwd":      workspace.Path,
		"input":    turnInputItems(prompt, skills, unresolvedSkills),
	}

	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if cfg.ReasoningEffort != "" {
		params["effort"] = cfg.ReasoningEffort
	}

	policy := cfg.ApprovalPolicy
	if policy == "auto" {
		policy = "never"
	}
	if policy != "" {
		params["approvalPolicy"] = policy
	}

	// Turn sandbox policy uses the discriminated SandboxPolicy shape.
	if cfg.TurnSandboxPolicy != "" && cfg.TurnSandboxPolicy != "none" {
		params["sandboxPolicy"] = mapSandboxPolicy(cfg.TurnSandboxPolicy)
	}

	return params
}

func turnInputItems(prompt string, skills []api.CodexSkillRef, unresolvedSkills []string) []map[string]interface{} {
	input := make([]map[string]interface{}, 0, 1+len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		input = append(input, map[string]interface{}{
			"type": "skill",
			"name": skill.Name,
			"path": skill.Path,
		})
	}
	if len(unresolvedSkills) > 0 {
		prompt = fmt.Sprintf("%s\n\nConfigured skills not resolved by Codex app-server: %s. If an equivalent skill is available, use it; otherwise proceed using the best matching project conventions.", prompt, strings.Join(unresolvedSkills, ", "))
	}
	input = append(input, map[string]interface{}{
		"type": "text",
		"text": prompt,
	})
	return input
}

func resolveConfiguredSkills(workspace *api.Workspace, cfg *api.CodexConfig, sendRequest func(method string, params interface{}) (json.RawMessage, error)) ([]api.CodexSkillRef, []string) {
	if cfg == nil || len(cfg.Skills) == 0 {
		return nil, nil
	}
	resolved := make([]api.CodexSkillRef, 0, len(cfg.Skills))
	unresolved := make([]string, 0)
	needsLookup := false
	for _, skill := range cfg.Skills {
		if strings.TrimSpace(skill.Path) == "" {
			needsLookup = true
			break
		}
	}

	byName := map[string]api.CodexSkillRef{}
	if needsLookup {
		raw, err := sendRequest("skills/list", map[string]interface{}{
			"cwds": []string{workspace.Path},
		})
		if err == nil {
			var result skillsListResult
			if json.Unmarshal(raw, &result) == nil {
				for _, entry := range result.Data {
					for _, skill := range entry.Skills {
						if !skill.Enabled || strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Path) == "" {
							continue
						}
						byName[strings.ToLower(skill.Name)] = api.CodexSkillRef{Name: skill.Name, Path: skill.Path}
					}
				}
			}
		}
	}

	seen := make(map[string]struct{}, len(cfg.Skills))
	for _, skill := range cfg.Skills {
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Path = strings.TrimSpace(skill.Path)
		if skill.Name == "" && skill.Path == "" {
			continue
		}
		if skill.Name == "" {
			skill.Name = filepath.Base(skill.Path)
		}
		if skill.Path == "" {
			if found, ok := byName[strings.ToLower(skill.Name)]; ok {
				skill = found
			}
		}
		key := strings.ToLower(skill.Name) + "\x00" + strings.ToLower(skill.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if skill.Path == "" {
			unresolved = append(unresolved, skill.Name)
			continue
		}
		resolved = append(resolved, skill)
	}
	return resolved, unresolved
}

func effectiveCodexConfig(cfg *api.CodexConfig, stage api.PipelineStage) api.CodexConfig {
	if cfg == nil {
		return api.CodexConfig{}
	}
	effective := *cfg
	stageKey := strings.ToLower(strings.TrimSpace(stage.Kind))
	if stageKey == "" || cfg.StageOverrides == nil {
		return effective
	}
	override, ok := cfg.StageOverrides[stageKey]
	if !ok {
		return effective
	}
	if override.Model != "" {
		effective.Model = override.Model
	}
	if override.ModelProvider != "" {
		effective.ModelProvider = override.ModelProvider
	}
	if override.ReasoningEffort != "" {
		effective.ReasoningEffort = override.ReasoningEffort
	}
	if override.EndpointURL != "" {
		effective.EndpointURL = override.EndpointURL
	}
	if override.APIKeyConfigured {
		effective.APIKey = override.APIKey
		effective.APIKeyConfigured = true
	}
	if override.AuthTokenConfigured {
		effective.AuthToken = override.AuthToken
		effective.AuthTokenConfigured = true
	}
	if len(override.Env) > 0 {
		env := make(map[string]string, len(effective.Env)+len(override.Env))
		for key, value := range effective.Env {
			env[key] = value
		}
		for key, value := range override.Env {
			env[key] = value
		}
		effective.Env = env
	}
	if len(override.Skills) > 0 {
		effective.Skills = appendUniqueSkills(effective.Skills, override.Skills...)
	}
	return effective
}

func appendUniqueSkills(base []api.CodexSkillRef, extras ...api.CodexSkillRef) []api.CodexSkillRef {
	seen := make(map[string]struct{}, len(base)+len(extras))
	out := make([]api.CodexSkillRef, 0, len(base)+len(extras))
	for _, skill := range append(base, extras...) {
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Path = strings.TrimSpace(skill.Path)
		if skill.Name == "" && skill.Path == "" {
			continue
		}
		if skill.Name == "" {
			skill.Name = filepath.Base(skill.Path)
		}
		key := strings.ToLower(skill.Name) + "\x00" + strings.ToLower(skill.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, skill)
	}
	return out
}

func mapSandboxPolicy(policy string) interface{} {
	switch policy {
	case "read-only":
		return map[string]interface{}{
			"type":          "readOnly",
			"networkAccess": false,
		}
	case "workspace-write":
		return map[string]interface{}{
			"type":          "workspaceWrite",
			"networkAccess": false,
		}
	case "danger-full-access":
		return map[string]interface{}{
			"type": "dangerFullAccess",
		}
	default:
		return map[string]interface{}{
			"type":          "readOnly",
			"networkAccess": false,
		}
	}
}
