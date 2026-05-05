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
	os.Exit(m.Run())
}

func runMockCodexServer() {
	mode := mockMode(os.Getenv("SIMPHONY_MOCK_MODE"))
	if mode == "" {
		mode = mockModeNormal
	}

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

func mockCommand(mode mockMode) string {
	return fmt.Sprintf("%s -test.run=TestMockCodexServer", os.Args[0])
}

func mockEnv(mode mockMode) []string {
	return []string{
		"SIMPHONY_MOCK_CODEX=1",
		fmt.Sprintf("SIMPHONY_MOCK_MODE=%s", mode),
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
		Model:         "gpt-5.4",
		ModelProvider: "openai",
	}

	threadParams := buildThreadStartParams(workspace, cfg, api.Issue{ID: "1"})
	if threadParams["model"] != "gpt-5.4" {
		t.Fatalf("thread model = %v, want gpt-5.4", threadParams["model"])
	}
	if threadParams["modelProvider"] != "openai" {
		t.Fatalf("thread modelProvider = %v, want openai", threadParams["modelProvider"])
	}

	turnParams := buildTurnStartParams("thread-1", workspace, cfg, "hello")
	if turnParams["model"] != "gpt-5.4" {
		t.Fatalf("turn model = %v, want gpt-5.4", turnParams["model"])
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

// TestMockCodexServer is a dummy test that exists so that `go test` compiles
// a binary containing the mock server code. It is never executed as a real test.
func TestMockCodexServer(t *testing.T) {
	// This test is only used as an entry point for the mock subprocess.
	// The real mock logic is in runMockCodexServer, triggered by SIMPHONY_MOCK_CODEX=1.
}
