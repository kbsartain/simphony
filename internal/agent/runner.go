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
func (r *Runner) Run(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.CodexConfig, stage api.PipelineStage, maxTurns int, shouldContinue func() (api.ContinueDecision, error), eventCallback func(api.AgentEvent)) error {
	if workspace == nil || workspace.Path == "" {
		return fmt.Errorf("%s: workspace path is empty", api.ErrInvalidWorkspaceCWD)
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
	if err := configureSubprocessEnv(cmd, workspace.Path); err != nil {
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

		turnParams := buildTurnStartParams(threadID, workspace, cfg, turnPrompt)
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
		if stage.Kind == "merge" {
			return mergePrompt(issue, stage.Instructions), nil
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

func configureSubprocessEnv(cmd *exec.Cmd, workspacePath string) error {
	env := os.Environ()

	gitConfigPath, err := writeGitSafeDirectoryConfig(workspacePath)
	if err != nil {
		return err
	}
	env = setEnv(env, "GIT_CONFIG_GLOBAL", gitConfigPath)

	if rgPath, err := exec.LookPath("rg"); err == nil {
		env = prependPath(env, filepath.Dir(rgPath))
	}
	env = prependWorkspaceToolPaths(env, workspacePath)

	cmd.Env = env
	return nil
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

func buildTurnStartParams(threadID string, workspace *api.Workspace, cfg *api.CodexConfig, prompt string) map[string]interface{} {
	params := map[string]interface{}{
		"threadId": threadID,
		"cwd":      workspace.Path,
		"input": []map[string]interface{}{
			{
				"type": "text",
				"text": prompt,
			},
		},
	}

	if cfg.Model != "" {
		params["model"] = cfg.Model
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
