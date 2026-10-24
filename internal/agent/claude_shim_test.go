package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fakeClaudeSDK = `
import fs from "node:fs";

export async function* query({ options }) {
  if (process.env.SIMPHONY_TEST_SDK_CAPTURE) {
    fs.writeFileSync(process.env.SIMPHONY_TEST_SDK_CAPTURE, JSON.stringify(options));
  }
  const mode = process.env.SIMPHONY_TEST_SDK_MODE;
  if (mode === "no_result") {
    yield { type: "assistant", session_id: "sdk-session", message: { content: [{ type: "text", text: "unfinished" }] } };
    return;
  }
  if (mode === "error") {
    yield { type: "result", subtype: "error_during_execution", is_error: true, errors: ["authentication failed", "check provider"], session_id: "sdk-session" };
    return;
  }
  yield { type: "result", subtype: "success", is_error: false, result: "done", session_id: "sdk-session" };
}
`

func TestClaudeShimForwardsEffortAndCompletesOnSuccess(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "options.json")
	events, err := runClaudeShimWithFakeSDK(t, "success", capturePath)
	if err != nil {
		t.Fatalf("shim failed: %v", err)
	}
	if !hasClaudeShimEvent(events, "turn/completed") {
		t.Fatalf("events = %+v, want turn/completed", events)
	}

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var options map[string]interface{}
	if err := json.Unmarshal(data, &options); err != nil {
		t.Fatal(err)
	}
	if options["effort"] != "high" {
		t.Fatalf("SDK effort = %v, want high", options["effort"])
	}
	if options["model"] != "glm-test" {
		t.Fatalf("SDK model = %v, want glm-test", options["model"])
	}
}

func TestClaudeShimDefaultsSettingSourcesWithoutUserSettings(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "options.json")
	request := claudeShimRequest{
		Prompt:    "test prompt",
		Model:     "glm-test",
		TurnCount: 1,
	}
	if _, err := runClaudeShimRequestWithFakeSDK(t, request, "success", capturePath); err != nil {
		t.Fatalf("shim failed: %v", err)
	}

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var options map[string]interface{}
	if err := json.Unmarshal(data, &options); err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"project", "local"}
	if !reflect.DeepEqual(options["settingSources"], want) {
		t.Fatalf("SDK settingSources = %v, want %v (user settings must not override gateway credentials)", options["settingSources"], want)
	}
}

func TestClaudeShimHonorsExplicitSettingSources(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "options.json")
	request := claudeShimRequest{
		Prompt:         "test prompt",
		Model:          "glm-test",
		SettingSources: []string{"user", "project", "local"},
		TurnCount:      1,
	}
	if _, err := runClaudeShimRequestWithFakeSDK(t, request, "success", capturePath); err != nil {
		t.Fatalf("shim failed: %v", err)
	}

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var options map[string]interface{}
	if err := json.Unmarshal(data, &options); err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"user", "project", "local"}
	if !reflect.DeepEqual(options["settingSources"], want) {
		t.Fatalf("SDK settingSources = %v, want %v", options["settingSources"], want)
	}
}

func TestClaudeShimRejectsSDKErrorResult(t *testing.T) {
	events, err := runClaudeShimWithFakeSDK(t, "error", "")
	if err == nil {
		t.Fatal("expected SDK error result to fail")
	}
	if hasClaudeShimEvent(events, "turn/completed") {
		t.Fatalf("error result emitted turn/completed: %+v", events)
	}
	if !strings.Contains(err.Error(), "error_during_execution") ||
		!strings.Contains(err.Error(), "authentication failed; check provider") {
		t.Fatalf("shim error = %v, want subtype and SDK errors", err)
	}
}

func TestClaudeShimRejectsStreamWithoutResult(t *testing.T) {
	events, err := runClaudeShimWithFakeSDK(t, "no_result", "")
	if err == nil {
		t.Fatal("expected result-less stream to fail")
	}
	if hasClaudeShimEvent(events, "turn/completed") {
		t.Fatalf("result-less stream emitted turn/completed: %+v", events)
	}
	if !strings.Contains(err.Error(), "without a successful result") {
		t.Fatalf("shim error = %v, want missing-result failure", err)
	}
}

func runClaudeShimWithFakeSDK(t *testing.T, mode string, capturePath string) ([]claudeShimEvent, error) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "external-workspace")
	request := claudeShimRequest{
		CWD:             workspace,
		Prompt:          "test prompt",
		Model:           "glm-test",
		ReasoningEffort: "high",
		TurnCount:       1,
	}
	return runClaudeShimRequestWithFakeSDK(t, request, mode, capturePath)
}

func runClaudeShimRequestWithFakeSDK(t *testing.T, request claudeShimRequest, mode string, capturePath string) ([]claudeShimEvent, error) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}

	dir := t.TempDir()
	shimPath := filepath.Join(dir, "claude-agent-shim.mjs")
	sdkPath := filepath.Join(dir, "sdk.mjs")
	if err := os.WriteFile(shimPath, claudeShimSource, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sdkPath, []byte(fakeClaudeSDK), 0644); err != nil {
		t.Fatal(err)
	}
	if request.CWD == "" {
		request.CWD = filepath.Join(dir, "external-workspace")
	}
	if err := os.MkdirAll(request.CWD, 0755); err != nil {
		t.Fatal(err)
	}

	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, shimPath)
	cmd.Dir = request.CWD
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	cmd.Env = append(os.Environ(),
		"SIMPHONY_CLAUDE_SDK_PACKAGE="+sdkPath,
		"SIMPHONY_TEST_SDK_MODE="+mode,
		"SIMPHONY_TEST_SDK_CAPTURE="+capturePath,
	)
	output, runErr := cmd.CombinedOutput()

	var events []claudeShimEvent
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event claudeShimEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid shim output %q: %v", line, err)
		}
		events = append(events, event)
	}
	if runErr != nil {
		for _, event := range events {
			if event.Event == "error" && event.Error != "" {
				return events, &claudeShimTestError{message: event.Error}
			}
		}
	}
	return events, runErr
}

type claudeShimTestError struct {
	message string
}

func (e *claudeShimTestError) Error() string {
	return e.message
}

func hasClaudeShimEvent(events []claudeShimEvent, name string) bool {
	for _, event := range events {
		if event.Event == name {
			return true
		}
	}
	return false
}
