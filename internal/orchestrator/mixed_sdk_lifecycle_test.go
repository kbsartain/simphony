package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kbsartain/simphony/internal/agent"
	"github.com/kbsartain/simphony/internal/workspace"
	"github.com/kbsartain/simphony/pkg/api"
)

func TestMain(m *testing.M) {
	if os.Getenv("SIMPHONY_MIXED_SDK_HELPER") == "1" {
		runMixedSDKHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runMixedSDKHelper() {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("SIMPHONY_TEST_EXECUTION_PROVIDER")))
	stage := strings.ToLower(strings.TrimSpace(os.Getenv("SIMPHONY_TEST_STAGE")))
	if capturePath := strings.TrimSpace(os.Getenv("SIMPHONY_MIXED_SDK_CAPTURE")); capturePath != "" {
		file, err := os.OpenFile(capturePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%s:%s\n", provider, stage)
			_ = file.Close()
		}
	}

	if provider == "claude" {
		runMixedClaudeHelper()
		return
	}
	runMixedCodexHelper()
}

func runMixedClaudeHelper() {
	var request struct {
		ResumeSessionID string `json:"resume_session_id"`
		TurnCount       int    `json:"turn_count"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		return
	}
	sessionID := request.ResumeSessionID
	if sessionID == "" {
		sessionID = "mixed-claude-session"
	}
	encoder := json.NewEncoder(os.Stdout)
	_ = encoder.Encode(map[string]interface{}{
		"event": "session_started",
		"payload": map[string]interface{}{
			"session_id": sessionID,
			"thread_id":  sessionID,
			"turn_id":    fmt.Sprintf("claude-turn-%d", request.TurnCount),
			"turn_count": request.TurnCount,
		},
	})
	_ = encoder.Encode(map[string]interface{}{
		"event": "item/completed",
		"payload": map[string]interface{}{
			"session_id": sessionID,
			"item": map[string]interface{}{
				"type": "agentMessage",
				"text": "Claude adversarial review completed.",
			},
		},
	})
	_ = encoder.Encode(map[string]interface{}{
		"event": "turn/completed",
		"payload": map[string]interface{}{
			"session_id": sessionID,
			"status":     "completed",
			"turn_count": request.TurnCount,
		},
	})
}

func runMixedCodexHelper() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var message struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		switch message.Method {
		case "initialize":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      message.ID,
				"result":  map[string]interface{}{"protocolVersion": "2024-11-05"},
			})
		case "thread/start":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      message.ID,
				"result":  map[string]interface{}{"thread": map[string]interface{}{"id": "mixed-codex-thread"}},
			})
		case "turn/start":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      message.ID,
				"result":  map[string]interface{}{"turn": map[string]interface{}{"id": "mixed-codex-turn", "status": "inProgress"}},
			})
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "turn/completed",
				"params": map[string]interface{}{
					"threadId": "mixed-codex-thread",
					"turn":     map[string]interface{}{"id": "mixed-codex-turn", "status": "completed"},
				},
			})
		}
	}
}

func TestOrchestratorMixedSDKLifecycleCodexClaudeCodex(t *testing.T) {
	issue := api.Issue{ID: "1", Identifier: "A-1", Title: "Mixed SDK lifecycle", State: "Todo"}
	tracker := &mockTracker{
		candidates: []api.Issue{issue},
		byIDs:      map[string]api.Issue{"1": issue},
	}
	wsMgr, err := workspace.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("workspace manager: %v", err)
	}
	capturePath := t.TempDir() + string(os.PathSeparator) + "mixed-sdk-runs.txt"
	helperCommand := os.Args[0]
	cfg := defaultConfig()
	cfg.Polling.IntervalMs = 100
	cfg.Tracker.ActiveStates = []string{"Todo", "In Progress", "In Review", "Approved"}
	cfg.Agent.MaxConcurrentAgents = 1
	cfg.Agent.MaxTurns = 2
	cfg.AgentRuntime.Provider = "codex"
	cfg.AgentRuntime.Command = helperCommand
	cfg.AgentRuntime.Model = "gpt-coding"
	cfg.AgentRuntime.ModelProvider = "openai"
	cfg.AgentRuntime.TurnTimeoutMs = 5000
	cfg.AgentRuntime.ReadTimeoutMs = 5000
	cfg.AgentRuntime.Env = map[string]string{
		"SIMPHONY_MIXED_SDK_HELPER":        "1",
		"SIMPHONY_MIXED_SDK_CAPTURE":       capturePath,
		"SIMPHONY_TEST_EXECUTION_PROVIDER": "codex",
		"SIMPHONY_TEST_STAGE":              "coding",
	}
	cfg.AgentRuntime.StageOverrides = map[string]api.AgentStageOverride{
		"review": {
			Provider:      "claude",
			Command:       helperCommand,
			Model:         "claude-opus-review",
			ModelProvider: "anthropic",
			Env: map[string]string{
				"SIMPHONY_MIXED_SDK_HELPER":        "1",
				"SIMPHONY_MIXED_SDK_CAPTURE":       capturePath,
				"SIMPHONY_TEST_EXECUTION_PROVIDER": "claude",
				"SIMPHONY_TEST_STAGE":              "review",
			},
		},
		"merge": {
			Model: "gpt-merge",
			Env:   map[string]string{"SIMPHONY_TEST_STAGE": "merge"},
		},
	}

	orch := New(cfg, tracker, wsMgr, agent.NewRunner("Work on {{ issue.identifier }}: {{ issue.title }}"))
	orch.Start()
	defer orch.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tracker.mu.Lock()
		state := tracker.byIDs[issue.ID].State
		tracker.mu.Unlock()
		if state == "Done" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	tracker.mu.Lock()
	finalState := tracker.byIDs[issue.ID].State
	tracker.mu.Unlock()
	if finalState != "Done" {
		t.Fatalf("final issue state = %q, want Done; snapshot=%+v", finalState, orch.Snapshot())
	}

	content, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read SDK capture: %v", err)
	}
	runs := strings.Fields(string(content))
	want := []string{"codex:coding", "claude:review", "codex:merge"}
	if len(runs) != len(want) {
		t.Fatalf("SDK runs = %v, want %v", runs, want)
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Fatalf("SDK runs = %v, want %v", runs, want)
		}
	}
	if _, ok := orch.IssueDetail(issue.Identifier); ok {
		t.Fatal("terminal issue should not remain as a running or retrying detail")
	}
}
