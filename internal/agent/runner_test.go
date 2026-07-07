package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kbsartain/simphony/internal/config"
	"github.com/kbsartain/simphony/pkg/api"
)

// mockMode controls the behavior of the mock Codex app-server.
type mockMode string

const (
	mockModeNormal           mockMode = "normal"
	mockModeExitAfterStart   mockMode = "exit_after_turn_start"
	mockModeHang             mockMode = "hang"
	mockModeRequestUserInput mockMode = "request_user_input"
)

func TestMain(m *testing.M) {
	if os.Getenv("SIMPHONY_MOCK_CODEX") == "1" {
		runMockCodexServer()
		os.Exit(0)
	}
	if os.Getenv("SIMPHONY_MOCK_CLAUDE") == "1" {
		runMockClaudeShim()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runMockClaudeShim() {
	var req claudeShimRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		log.Printf("mock claude decode error: %v", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	sessionID := req.ResumeSessionID
	if sessionID == "" {
		sessionID = "claude-session-123"
	}
	_ = enc.Encode(claudeShimEvent{
		Event: "session_started",
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"thread_id":  sessionID,
			"turn_id":    fmt.Sprintf("turn-%d", req.TurnCount),
			"turn_count": req.TurnCount,
		},
	})
	_ = enc.Encode(claudeShimEvent{
		Event: "thread/tokenUsage/updated",
		Usage: map[string]interface{}{
			"input_tokens":  12,
			"output_tokens": 8,
			"total_tokens":  20,
		},
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"turn_count": req.TurnCount,
		},
	})
	_ = enc.Encode(claudeShimEvent{
		Event: "item/completed",
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"item": map[string]interface{}{
				"type": "agentMessage",
				"text": "Claude completed the turn.",
			},
		},
	})
	_ = enc.Encode(claudeShimEvent{
		Event: "turn/completed",
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"status":     "completed",
			"turn_count": req.TurnCount,
		},
	})
}

func runMockCodexServer() {
	mode := mockMode(os.Getenv("SIMPHONY_MOCK_MODE"))
	if mode == "" {
		mode = mockModeNormal
	}
	capturePath := os.Getenv("SIMPHONY_MOCK_CAPTURE")

	stdin := os.Stdin
	stdout := os.Stdout
	enc := json.NewEncoder(stdout)
	dec := json.NewDecoder(stdin)

	var mu sync.Mutex
	var wg sync.WaitGroup

	for {
		var msg jsonRPCMsg
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("mock decode error: %v", err)
			break
		}

		if msg.Method == "" {
			continue
		}
		if capturePath != "" && (msg.Method == "thread/start" || msg.Method == "turn/start") {
			appendMockRequestCapture(capturePath, msg)
		}

		switch msg.Method {
		case "initialize":
			mu.Lock()
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
				},
			})
			mu.Unlock()

		case "thread/start":
			mu.Lock()
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]interface{}{
					"thread": map[string]interface{}{
						"id": "thread-mock-123",
					},
				},
			})
			mu.Unlock()

		case "turn/start":
			mu.Lock()
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]interface{}{
					"turn": map[string]interface{}{
						"id":     "turn-mock-456",
						"status": "inProgress",
					},
				},
			})
			mu.Unlock()

			if mode == mockModeExitAfterStart {
				return
			}

			if mode == mockModeHang {
				// Block forever reading stdin.
				_, _ = io.Copy(io.Discard, stdin)
				return
			}

			wg.Add(1)
			go func(reqID interface{}) {
				defer wg.Done()
				time.Sleep(50 * time.Millisecond)

				mu.Lock()
				_ = enc.Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "turn/started",
					"params": map[string]interface{}{
						"threadId": "thread-mock-123",
						"turn": map[string]interface{}{
							"id":     "turn-mock-456",
							"status": "inProgress",
						},
					},
				})
				mu.Unlock()

				time.Sleep(50 * time.Millisecond)

				mu.Lock()
				_ = enc.Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "thread/tokenUsage/updated",
					"params": map[string]interface{}{
						"threadId": "thread-mock-123",
						"turnId":   "turn-mock-456",
						"tokenUsage": map[string]interface{}{
							"total": map[string]interface{}{
								"inputTokens":  100,
								"outputTokens": 50,
								"totalTokens":  150,
							},
						},
					},
				})
				mu.Unlock()

				time.Sleep(50 * time.Millisecond)

				if mode == mockModeRequestUserInput {
					mu.Lock()
					_ = enc.Encode(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      "srv-req-1",
						"method":  "item/tool/requestUserInput",
						"params": map[string]interface{}{
							"threadId": "thread-mock-123",
							"turnId":   "turn-mock-456",
							"itemId":   "item-1",
							"questions": []map[string]interface{}{
								{"id": "q1", "question": "What is your name?", "header": "Input needed"},
							},
						},
					})
					mu.Unlock()
					return
				}

				mu.Lock()
				_ = enc.Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "turn/completed",
					"params": map[string]interface{}{
						"threadId": "thread-mock-123",
						"turn": map[string]interface{}{
							"id":     "turn-mock-456",
							"status": "completed",
						},
					},
				})
				mu.Unlock()
			}(msg.ID)

		case "item/tool/call":
			mu.Lock()
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]interface{}{
					"success":      false,
					"contentItems": []map[string]interface{}{},
				},
			})
			mu.Unlock()

		case "item/tool/requestUserInput":
			mu.Lock()
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]interface{}{
					"answers": map[string]interface{}{},
				},
			})
			mu.Unlock()
		}
	}

	wg.Wait()
}

func appendMockRequestCapture(path string, msg jsonRPCMsg) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("mock capture open error: %v", err)
		return
	}
	defer f.Close()

	var params map[string]interface{}
	_ = json.Unmarshal(msg.Params, &params)
	_ = json.NewEncoder(f).Encode(map[string]interface{}{
		"method": msg.Method,
		"params": params,
	})
}

func mockCommand(mode mockMode) string {
	return fmt.Sprintf("%s -test.run=TestMockCodexServer", os.Args[0])
}

func mockEnv(mode mockMode) []string {
	return []string{
		"SIMPHONY_MOCK_CODEX=1",
		fmt.Sprintf("SIMPHONY_MOCK_MODE=%s", mode),
	}
}

func mockClaudeEnv() []string {
	return []string{
		"SIMPHONY_MOCK_CLAUDE=1",
		"SIMPHONY_MOCK_CODEX=",
	}
}

func TestScrubInheritedAgentEnvRemovesProviderAndTrackerValues(t *testing.T) {
	env := []string{
		"OPENAI_API_KEY=parent-openai",
		"openai_base_url=https://parent-openai.example/v1",
		"ANTHROPIC_API_KEY=parent-anthropic",
		"LINEAR_API_KEY=parent-linear",
		"PATH=/usr/bin",
		"CUSTOM=value",
	}

	got := scrubInheritedAgentEnv(env)
	if getEnv(got, "OPENAI_API_KEY") != "" {
		t.Fatal("OPENAI_API_KEY was inherited")
	}
	if getEnv(got, "OPENAI_BASE_URL") != "" {
		t.Fatal("OPENAI_BASE_URL was inherited")
	}
	if getEnv(got, "ANTHROPIC_API_KEY") != "" {
		t.Fatal("ANTHROPIC_API_KEY was inherited")
	}
	if getEnv(got, "LINEAR_API_KEY") != "" {
		t.Fatal("LINEAR_API_KEY was inherited")
	}
	if getEnv(got, "PATH") != "/usr/bin" || getEnv(got, "CUSTOM") != "value" {
		t.Fatalf("non-secret env was not preserved: %+v", got)
	}
}

func TestRuntimeEnvAppliesProjectScopedValuesAfterScrub(t *testing.T) {
	env := scrubInheritedAgentEnv([]string{
		"OPENAI_API_KEY=parent-openai",
		"ANTHROPIC_API_KEY=parent-anthropic",
		"LINEAR_API_KEY=parent-linear",
		"PATH=/usr/bin",
	})
	cfg := &api.AgentRuntimeConfig{
		Provider:    "codex",
		APIKey:      "project-openai",
		EndpointURL: "https://project-openai.example/v1",
		AuthToken:   "project-auth",
		Env: map[string]string{
			"LINEAR_API_KEY": "project-linear",
		},
	}

	got := applyRuntimeEnv(env, cfg)
	if getEnv(got, "OPENAI_API_KEY") != "project-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want project-openai", getEnv(got, "OPENAI_API_KEY"))
	}
	if getEnv(got, "OPENAI_BASE_URL") != "https://project-openai.example/v1" {
		t.Fatalf("OPENAI_BASE_URL = %q, want project endpoint", getEnv(got, "OPENAI_BASE_URL"))
	}
	if getEnv(got, "OPENAI_AUTH_TOKEN") != "project-auth" {
		t.Fatalf("OPENAI_AUTH_TOKEN = %q, want project-auth", getEnv(got, "OPENAI_AUTH_TOKEN"))
	}
	if getEnv(got, "ANTHROPIC_API_KEY") != "" {
		t.Fatal("ANTHROPIC_API_KEY was inherited into codex runtime")
	}
	if getEnv(got, "LINEAR_API_KEY") != "project-linear" {
		t.Fatalf("LINEAR_API_KEY = %q, want explicit project env", getEnv(got, "LINEAR_API_KEY"))
	}
}

func TestRunnerSuccessfulSession(t *testing.T) {
	// Set up environment so that the mock server runs.
	for _, e := range mockEnv(mockModeNormal) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-1",
		Identifier: "TEST-1",
		Title:      "Test Issue",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeNormal),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	var events []api.AgentEvent
	mu := sync.Mutex{}
	eventCallback := func(e api.AgentEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, eventCallback)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	var hasSessionStarted, hasTurnCompleted bool
	for _, e := range events {
		if e.Event == "session_started" {
			hasSessionStarted = true
			if e.Payload["session_id"] != "thread-mock-123-turn-mock-456" {
				t.Errorf("unexpected session_id: %v", e.Payload["session_id"])
			}
		}
		if e.Event == "turn/completed" {
			hasTurnCompleted = true
		}
		if e.Event == "thread/tokenUsage/updated" {
			if e.Usage == nil {
				t.Errorf("expected usage in tokenUsage event")
			}
		}
	}
	if !hasSessionStarted {
		t.Errorf("expected session_started event")
	}
	if !hasTurnCompleted {
		t.Errorf("expected turn/completed event")
	}
}

func TestRunnerContinuationUsesSameThread(t *testing.T) {
	for _, e := range mockEnv(mockModeNormal) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-continue",
		Identifier: "TEST-99",
		Title:      "Continue Issue",
		State:      "In Progress",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeNormal),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	var events []api.AgentEvent
	mu := sync.Mutex{}
	eventCallback := func(e api.AgentEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	continuations := 0
	shouldContinue := func() (api.ContinueDecision, error) {
		continuations++
		return api.ContinueDecision{Issue: issue, Continue: continuations == 1}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 3, shouldContinue, eventCallback)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	var sessionStarts int
	for _, e := range events {
		if e.Event != "session_started" {
			continue
		}
		sessionStarts++
		if e.Payload["thread_id"] != "thread-mock-123" {
			t.Fatalf("expected same thread id, got %v", e.Payload["thread_id"])
		}
	}
	if sessionStarts != 2 {
		t.Fatalf("expected 2 turns on same thread, got %d", sessionStarts)
	}
}

func TestRunnerClaudeShimSession(t *testing.T) {
	for _, e := range mockClaudeEnv() {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-claude",
		Identifier: "TEST-CLAUDE",
		Title:      "Claude Issue",
		State:      "In Progress",
	}
	cfg := api.AgentRuntimeConfig{
		Provider:       "claude",
		Command:        mockCommand(mockModeNormal),
		Model:          "claude-sonnet",
		PermissionMode: "acceptEdits",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	var events []api.AgentEvent
	mu := sync.Mutex{}
	eventCallback := func(e api.AgentEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, eventCallback)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var hasSessionStarted, hasMessage, hasUsage bool
	for _, e := range events {
		switch e.Event {
		case "session_started":
			hasSessionStarted = true
			if e.Payload["session_id"] != "claude-session-123" {
				t.Fatalf("session_id = %v, want claude-session-123", e.Payload["session_id"])
			}
			if e.Payload["provider"] != "claude" {
				t.Fatalf("provider = %v, want claude", e.Payload["provider"])
			}
		case "item/completed":
			hasMessage = true
		case "thread/tokenUsage/updated":
			hasUsage = e.Usage != nil
		}
	}
	if !hasSessionStarted || !hasMessage || !hasUsage {
		t.Fatalf("events missing session/message/usage: session=%t message=%t usage=%t events=%+v", hasSessionStarted, hasMessage, hasUsage, events)
	}
}

func TestRunnerMaxTurnsReached(t *testing.T) {
	for _, e := range mockEnv(mockModeNormal) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-max-turns",
		Identifier: "TEST-100",
		Title:      "Max Turns Issue",
		State:      "In Progress",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeNormal),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, func() (api.ContinueDecision, error) {
		return api.ContinueDecision{Issue: issue, Continue: true}, nil
	}, func(api.AgentEvent) {})
	if err == nil {
		t.Fatal("expected max turns error")
	}
	if !strings.Contains(err.Error(), api.ErrMaxTurnsReached) {
		t.Fatalf("expected %s error, got %v", api.ErrMaxTurnsReached, err)
	}
}

func TestRunnerContextCancellation(t *testing.T) {
	for _, e := range mockEnv(mockModeHang) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-2",
		Identifier: "TEST-2",
		Title:      "Test Issue",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeHang),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel shortly after the turn starts.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRunnerSubprocessExit(t *testing.T) {
	for _, e := range mockEnv(mockModeExitAfterStart) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-3",
		Identifier: "TEST-3",
		Title:      "Test Issue",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeExitAfterStart),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {})
	if err == nil {
		t.Fatalf("expected error for subprocess exit")
	}
	if !strings.Contains(err.Error(), api.ErrPortExit) {
		t.Fatalf("expected %s error, got %v", api.ErrPortExit, err)
	}
}

func TestRunnerTurnTimeout(t *testing.T) {
	for _, e := range mockEnv(mockModeHang) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-4",
		Identifier: "TEST-4",
		Title:      "Test Issue",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeHang),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  100, // 100ms timeout
		ReadTimeoutMs:  5000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {})
	if err == nil {
		t.Fatalf("expected error for turn timeout")
	}
	if !strings.Contains(err.Error(), api.ErrTurnTimeout) {
		t.Fatalf("expected %s error, got %v", api.ErrTurnTimeout, err)
	}
}

func TestRunnerRequestUserInput(t *testing.T) {
	for _, e := range mockEnv(mockModeRequestUserInput) {
		os.Setenv(strings.SplitN(e, "=", 2)[0], strings.SplitN(e, "=", 2)[1])
	}

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-5",
		Identifier: "TEST-5",
		Title:      "Test Issue",
	}
	cfg := api.CodexConfig{
		Command:        mockCommand(mockModeRequestUserInput),
		ApprovalPolicy: "never",
		TurnTimeoutMs:  30000,
		ReadTimeoutMs:  5000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {})
	if err == nil {
		t.Fatalf("expected error for user input request")
	}
	if !strings.Contains(err.Error(), api.ErrTurnInputRequired) {
		t.Fatalf("expected %s error, got %v", api.ErrTurnInputRequired, err)
	}
}

func TestMapSandboxPolicy(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"read-only", "readOnly"},
		{"workspace-write", "workspaceWrite"},
		{"danger-full-access", "dangerFullAccess"},
		{"unknown", "readOnly"},
	}
	for _, c := range cases {
		result := mapSandboxPolicy(c.input)
		m, ok := result.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map for %s", c.input)
		}
		if m["type"] != c.expected {
			t.Errorf("mapSandboxPolicy(%s) type=%v, want %v", c.input, m["type"], c.expected)
		}
	}
}

func TestBuildParamsIncludeModelSelection(t *testing.T) {
	workspace := &api.Workspace{Path: t.TempDir()}
	cfg := &api.CodexConfig{
		Model:           "gpt-5.4",
		ModelProvider:   "openai",
		ReasoningEffort: "high",
		EndpointURL:     "https://openai.example/v1",
		APIKey:          "openai-key",
		AuthToken:       "openai-token",
		Env:             map[string]string{"ROUTER_MODE": "default"},
		StageOverrides: map[string]api.CodexStageOverride{
			"review": {
				Model:               "claude-opus-4.1",
				ModelProvider:       "anthropic",
				ReasoningEffort:     "xhigh",
				EndpointURL:         "https://anthropic.example/v1",
				APIKey:              "anthropic-key",
				APIKeyConfigured:    true,
				AuthToken:           "anthropic-token",
				AuthTokenConfigured: true,
				Env:                 map[string]string{"ROUTER_MODE": "review", "STAGE_ONLY": "1"},
			},
		},
	}

	threadParams := buildThreadStartParams(workspace, cfg, api.Issue{ID: "1"})
	if threadParams["model"] != "gpt-5.4" {
		t.Fatalf("thread model = %v, want gpt-5.4", threadParams["model"])
	}
	if threadParams["modelProvider"] != "openai" {
		t.Fatalf("thread modelProvider = %v, want openai", threadParams["modelProvider"])
	}

	turnParams := buildTurnStartParams("thread-1", workspace, cfg, "hello", nil, nil)
	if turnParams["model"] != "gpt-5.4" {
		t.Fatalf("turn model = %v, want gpt-5.4", turnParams["model"])
	}
	if turnParams["effort"] != "high" {
		t.Fatalf("turn effort = %v, want high", turnParams["effort"])
	}

	reviewCfg := effectiveCodexConfig(cfg, api.PipelineStage{Kind: "review"})
	if reviewCfg.Model != "claude-opus-4.1" {
		t.Fatalf("review model = %q, want claude-opus-4.1", reviewCfg.Model)
	}
	if reviewCfg.ModelProvider != "anthropic" {
		t.Fatalf("review model provider = %q, want anthropic", reviewCfg.ModelProvider)
	}
	if reviewCfg.ReasoningEffort != "xhigh" {
		t.Fatalf("review reasoning = %q, want xhigh", reviewCfg.ReasoningEffort)
	}
	if reviewCfg.EndpointURL != "https://anthropic.example/v1" || reviewCfg.APIKey != "anthropic-key" || reviewCfg.AuthToken != "anthropic-token" {
		t.Fatalf("review routing = endpoint %q api %q auth %q, want stage routing", reviewCfg.EndpointURL, reviewCfg.APIKey, reviewCfg.AuthToken)
	}
	if reviewCfg.Env["ROUTER_MODE"] != "review" || reviewCfg.Env["STAGE_ONLY"] != "1" {
		t.Fatalf("review env = %+v, want merged stage env", reviewCfg.Env)
	}
	reviewTurnParams := buildTurnStartParams("thread-1", workspace, &reviewCfg, "review", nil, nil)
	if reviewTurnParams["model"] != "claude-opus-4.1" || reviewTurnParams["effort"] != "xhigh" {
		t.Fatalf("review turn params = %v, want review model and xhigh effort", reviewTurnParams)
	}
}

func TestRunnerRunAppliesStageSpecificModelSelection(t *testing.T) {
	for _, e := range mockEnv(mockModeNormal) {
		parts := strings.SplitN(e, "=", 2)
		t.Setenv(parts[0], parts[1])
	}
	capturePath := t.TempDir() + string(os.PathSeparator) + "requests.jsonl"
	t.Setenv("SIMPHONY_MOCK_CAPTURE", capturePath)

	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-stage-models",
		Identifier: "TEST-STAGE-MODELS",
		Title:      "Verify stage-specific models",
	}
	cfg := api.AgentRuntimeConfig{
		Command:         mockCommand(mockModeNormal),
		Model:           "gpt-coding-test",
		ModelProvider:   "openai",
		ReasoningEffort: "high",
		ApprovalPolicy:  "never",
		TurnTimeoutMs:   30000,
		ReadTimeoutMs:   5000,
		StageOverrides: map[string]api.AgentStageOverride{
			"review": {
				Model:           "claude-review-test",
				ModelProvider:   "anthropic",
				ReasoningEffort: "xhigh",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {}); err != nil {
		t.Fatalf("coding run failed: %v", err)
	}
	if err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "review"}, 1, nil, func(api.AgentEvent) {}); err != nil {
		t.Fatalf("review run failed: %v", err)
	}

	records := readCapturedMockRequests(t, capturePath)
	if len(records) != 4 {
		t.Fatalf("captured %d requests, want thread/start and turn/start for coding and review: %+v", len(records), records)
	}
	assertCapturedModelRequest(t, records[0], "thread/start", "gpt-coding-test", "openai", "")
	assertCapturedModelRequest(t, records[1], "turn/start", "gpt-coding-test", "", "high")
	assertCapturedModelRequest(t, records[2], "thread/start", "claude-review-test", "anthropic", "")
	assertCapturedModelRequest(t, records[3], "turn/start", "claude-review-test", "", "xhigh")
}

func TestRunnerRunAppliesWorkflowProviderOnlyStageOverrides(t *testing.T) {
	for _, e := range mockEnv(mockModeNormal) {
		parts := strings.SplitN(e, "=", 2)
		t.Setenv(parts[0], parts[1])
	}
	t.Setenv("LINEAR_API_KEY", "test-linear-key")
	capturePath := t.TempDir() + string(os.PathSeparator) + "requests.jsonl"
	t.Setenv("SIMPHONY_MOCK_CAPTURE", capturePath)

	var frontMatter map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"agent": {
			"max_concurrent_agents": 10,
			"max_retry_backoff_ms": 300000,
			"max_turns": 20
		},
		"codex": {
			"approval_policy": "never",
			"command": "codex app-server",
			"model_provider": "zai",
			"read_timeout_ms": 5000,
			"reasoning_effort": "high",
			"stage_overrides": {
				"coding": {
					"reasoning_effort": "high"
				},
				"merge": {
					"reasoning_effort": "high"
				},
				"review": {
					"model_provider": "openai",
					"reasoning_effort": "xhigh"
				}
			},
			"stall_timeout_ms": 300000,
			"thread_sandbox": "danger-full-access",
			"turn_sandbox_policy": "danger-full-access",
			"turn_timeout_ms": 3600000
		},
		"hooks": {
			"before_run": "powershell -NoProfile -ExecutionPolicy Bypass -File C:\\Users\\kbsar\\simphony\\scripts\\setup-workspace.ps1 -WorkspacePath .\n",
			"timeout_ms": 300000
		},
		"pipeline": {
			"done_state": "Done",
			"merge_state": "Approved",
			"review_state": "In Review"
		},
		"polling": {
			"interval_ms": 30000
		},
		"server": {
			"port": 8080
		},
		"tracker": {
			"active_states": [
				"Backlog",
				"Todo",
				"In Progress",
				"Approved"
			],
			"api_key": "$LINEAR_API_KEY",
			"completion_states": [
				"In Review",
				"Review",
				"Done",
				"Completed"
			],
			"kind": "linear",
			"project_slug": "simphony-2172572a4807",
			"working_state": "In Progress"
		},
		"workspace": {
			"base_branch": "main",
			"branch_prefix": "simphony/",
			"cleanup_worktrees": false,
			"mode": "git_worktree",
			"repo": ".",
			"root": "./simphony_workspaces"
		}
	}`), &frontMatter); err != nil {
		t.Fatalf("decode front matter JSON: %v", err)
	}

	workflowCfg, err := config.ResolveConfig(&api.WorkflowDefinition{
		Config:         frontMatter,
		PromptTemplate: "You are working on issue {{ issue.identifier }}: {{ issue.title }}",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("resolve front matter config: %v", err)
	}
	if workflowCfg.AgentRuntime.ModelProvider != "zai" || workflowCfg.AgentRuntime.Model != "" {
		t.Fatalf("resolved base runtime model/provider = %q/%q, want empty model with zai provider", workflowCfg.AgentRuntime.Model, workflowCfg.AgentRuntime.ModelProvider)
	}
	if review := workflowCfg.AgentRuntime.StageOverrides["review"]; review.ModelProvider != "openai" || review.Model != "" {
		t.Fatalf("resolved review override model/provider = %q/%q, want empty model with openai provider", review.Model, review.ModelProvider)
	}

	workflowCfg.AgentRuntime.Command = mockCommand(mockModeNormal)
	runner := NewRunner("You are working on issue {{ issue.identifier }}: {{ issue.title }}")
	workspace := &api.Workspace{Path: t.TempDir()}
	issue := api.Issue{
		ID:         "issue-provider-only",
		Identifier: "TEST-PROVIDER-ONLY",
		Title:      "Verify provider-only stage overrides",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Run(ctx, issue, workspace, nil, &workflowCfg.AgentRuntime, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {}); err != nil {
		t.Fatalf("coding run failed: %v", err)
	}
	if err := runner.Run(ctx, issue, workspace, nil, &workflowCfg.AgentRuntime, api.PipelineStage{Kind: "review"}, 1, nil, func(api.AgentEvent) {}); err != nil {
		t.Fatalf("review run failed: %v", err)
	}

	records := readCapturedMockRequests(t, capturePath)
	if len(records) != 4 {
		t.Fatalf("captured %d requests, want thread/start and turn/start for coding and review: %+v", len(records), records)
	}
	assertCapturedProviderOnlyRequest(t, records[0], "thread/start", "zai", "")
	assertCapturedProviderOnlyRequest(t, records[1], "turn/start", "", "high")
	assertCapturedProviderOnlyRequest(t, records[2], "thread/start", "openai", "")
	assertCapturedProviderOnlyRequest(t, records[3], "turn/start", "", "xhigh")
}

type capturedMockRequest struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

func readCapturedMockRequests(t *testing.T, path string) []capturedMockRequest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mock capture: %v", err)
	}
	var records []capturedMockRequest
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record capturedMockRequest
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode mock capture line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func assertCapturedModelRequest(t *testing.T, record capturedMockRequest, method string, model string, modelProvider string, effort string) {
	t.Helper()
	if record.Method != method {
		t.Fatalf("method = %q, want %q for record %+v", record.Method, method, record)
	}
	if record.Params["model"] != model {
		t.Fatalf("%s model = %v, want %s", method, record.Params["model"], model)
	}
	if modelProvider != "" && record.Params["modelProvider"] != modelProvider {
		t.Fatalf("%s modelProvider = %v, want %s", method, record.Params["modelProvider"], modelProvider)
	}
	if effort != "" && record.Params["effort"] != effort {
		t.Fatalf("%s effort = %v, want %s", method, record.Params["effort"], effort)
	}
}

func assertCapturedProviderOnlyRequest(t *testing.T, record capturedMockRequest, method string, modelProvider string, effort string) {
	t.Helper()
	if record.Method != method {
		t.Fatalf("method = %q, want %q for record %+v", record.Method, method, record)
	}
	if _, ok := record.Params["model"]; ok {
		t.Fatalf("%s model = %v, want model omitted", method, record.Params["model"])
	}
	if modelProvider != "" && record.Params["modelProvider"] != modelProvider {
		t.Fatalf("%s modelProvider = %v, want %s", method, record.Params["modelProvider"], modelProvider)
	}
	if modelProvider == "" {
		if _, ok := record.Params["modelProvider"]; ok {
			t.Fatalf("%s modelProvider = %v, want modelProvider omitted", method, record.Params["modelProvider"])
		}
	}
	if effort != "" && record.Params["effort"] != effort {
		t.Fatalf("%s effort = %v, want %s", method, record.Params["effort"], effort)
	}
}

func TestBuildTurnStartParamsIncludesSkills(t *testing.T) {
	workspace := &api.Workspace{Path: t.TempDir()}
	cfg := &api.CodexConfig{}
	skills := []api.CodexSkillRef{{Name: "conjit-product-ui", Path: `C:\skills\conjit-product-ui\SKILL.md`}}
	params := buildTurnStartParams("thread-1", workspace, cfg, "hello", skills, []string{"missing-skill"})
	input, ok := params["input"].([]map[string]interface{})
	if !ok {
		t.Fatalf("input = %T, want []map[string]interface{}", params["input"])
	}
	if len(input) != 2 {
		t.Fatalf("input length = %d, want skill plus text", len(input))
	}
	if input[0]["type"] != "skill" || input[0]["name"] != "conjit-product-ui" {
		t.Fatalf("skill item = %v, want conjit skill", input[0])
	}
	text, _ := input[1]["text"].(string)
	if !strings.Contains(text, "missing-skill") {
		t.Fatalf("text item = %q, want unresolved skill note", text)
	}
}

func TestMergePromptUsesStageInstructions(t *testing.T) {
	runner := NewRunner("coding prompt")
	issue := api.Issue{Identifier: "A-1", Title: "Reviewed change", State: "Approved"}
	prompt, err := runner.turnPrompt(issue, nil, api.PipelineStage{Kind: "merge", Instructions: "Merge this reviewed change."}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Merge this reviewed change.") || !strings.Contains(prompt, "A-1 - Reviewed change") {
		t.Fatalf("merge prompt = %q, want stage instructions and issue context", prompt)
	}
}

func TestReviewPromptUsesStageInstructions(t *testing.T) {
	runner := NewRunner("coding prompt")
	issue := api.Issue{Identifier: "A-2", Title: "Change awaiting review", State: "In Review"}
	prompt, err := runner.turnPrompt(issue, nil, api.PipelineStage{Kind: "review", Instructions: "Review this implementation at high confidence."}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Review this implementation at high confidence.") || !strings.Contains(prompt, "A-2 - Change awaiting review") {
		t.Fatalf("review prompt = %q, want stage instructions and issue context", prompt)
	}
}

func TestReviewResolutionPromptRequiresDecisionDirective(t *testing.T) {
	runner := NewRunner("coding prompt")
	issue := api.Issue{Identifier: "A-3", Title: "PR awaiting code review", State: "Review Resolution"}
	prompt, err := runner.turnPrompt(issue, nil, api.PipelineStage{Kind: "review_resolution", Instructions: "Resolve PR review comments."}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Resolve PR review comments.") || !strings.Contains(prompt, "SIMPHONY_REVIEW_DECISION") || !strings.Contains(prompt, "A-3 - PR awaiting code review") {
		t.Fatalf("review-resolution prompt = %q, want instructions, decision directive, and issue context", prompt)
	}
}

// TestMockCodexServer is a dummy test that exists so that `go test` compiles
// a binary containing the mock server code. It is never executed as a real test.
func TestMockCodexServer(t *testing.T) {
	// This test is only used as an entry point for the mock subprocess.
	// The real mock logic is in runMockCodexServer, triggered by SIMPHONY_MOCK_CODEX=1.
}
