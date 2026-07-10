package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestRunnerLiveCodexCanUseRipgrep(t *testing.T) {
	if os.Getenv("SIMPHONY_LIVE_CODEX_TURN") == "" {
		t.Skip("set SIMPHONY_LIVE_CODEX_TURN=1 to run a real Codex turn")
	}

	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "probe.txt"), []byte("simphony_rg_probe\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner("Use the shell tool to run `rg -n simphony_rg_probe .`. If and only if that command succeeds, write the word success to rg-proof.txt. Do not perform any other work.")
	issue := api.Issue{ID: "live-rg", Identifier: "LIVE-RG", Title: "Verify Ripgrep"}
	workspace := &api.Workspace{Path: workspacePath}
	cfg := api.AgentRuntimeConfig{
		Provider:          "codex",
		Command:           "codex app-server",
		Model:             "gpt-5.6-sol",
		ModelProvider:     "openai",
		EndpointURL:       "https://api.openai.com/v1",
		ApprovalPolicy:    "never",
		ReasoningEffort:   "low",
		ReadTimeoutMs:     60000,
		StallTimeoutMs:    60000,
		TurnTimeoutMs:     180000,
		ThreadSandbox:     "workspace-write",
		TurnSandboxPolicy: "workspace-write",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := runner.Run(ctx, issue, workspace, nil, &cfg, api.PipelineStage{Kind: "coding"}, 1, nil, func(api.AgentEvent) {}); err != nil {
		t.Fatal(err)
	}
	proof, err := os.ReadFile(filepath.Join(workspacePath, "rg-proof.txt"))
	if err != nil {
		t.Fatalf("Codex did not produce rg proof: %v", err)
	}
	if string(proof) != "success" && string(proof) != "success\n" {
		t.Fatalf("unexpected rg proof %q", proof)
	}
}
