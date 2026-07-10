package preflight

import (
	"strings"
	"testing"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestCheckBlocksMissingConfig(t *testing.T) {
	health := Check(nil)
	if health.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", health.Status, StatusBlocked)
	}
	if len(health.Issues) != 1 || health.Issues[0].Code != "config_missing" {
		t.Fatalf("issues = %+v, want config_missing", health.Issues)
	}
}

func TestCheckReportsStageSpecificSDKCommandFailure(t *testing.T) {
	health := Check(&api.WorkflowConfig{
		AgentRuntime: api.AgentRuntimeConfig{
			Provider: "codex",
			Command:  "go version",
			StageOverrides: map[string]api.AgentStageOverride{
				"review": {
					Provider: "claude",
					Command:  "definitely-missing-simphony-agent-command",
				},
			},
		},
		Workspace: api.WorkspaceConfig{Root: t.TempDir(), Mode: "directory"},
	})

	if health.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", health.Status, StatusBlocked)
	}
	for _, issue := range health.Issues {
		if issue.Code == "agent_command_not_found" && strings.Contains(issue.Message, "Stage review") && strings.Contains(issue.Detail, "stage=review") {
			return
		}
	}
	t.Fatalf("issues = %+v, want stage-specific command failure", health.Issues)
}

func TestCheckBlocksUnresolvedStageCredentialReference(t *testing.T) {
	health := Check(&api.WorkflowConfig{
		AgentRuntime: api.AgentRuntimeConfig{
			Provider: "codex",
			Command:  "go version",
			StageOverrides: map[string]api.AgentStageOverride{
				"review": {
					Provider:         "claude",
					Command:          "go version",
					APIKeyConfigured: true,
				},
			},
		},
		Workspace: api.WorkspaceConfig{Root: t.TempDir(), Mode: "directory"},
	})

	if health.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", health.Status, StatusBlocked)
	}
	for _, issue := range health.Issues {
		if issue.Code == "agent_api_key_unresolved" && strings.Contains(issue.Message, "Stage review") && strings.Contains(issue.Detail, "stage=review") {
			return
		}
	}
	t.Fatalf("issues = %+v, want stage-specific unresolved credential", health.Issues)
}

func TestCheckAllowsUnconfiguredCredentialsForLocalSDKAuth(t *testing.T) {
	health := Check(&api.WorkflowConfig{
		AgentRuntime: api.AgentRuntimeConfig{Provider: "codex", Command: "go version"},
		Workspace:    api.WorkspaceConfig{Root: t.TempDir(), Mode: "directory"},
	})

	for _, issue := range health.Issues {
		if strings.Contains(issue.Code, "api_key") || strings.Contains(issue.Code, "auth_token") {
			t.Fatalf("unexpected credential issue for local SDK auth: %+v", issue)
		}
	}
}

func TestCheckBlocksMissingAgentCommand(t *testing.T) {
	health := Check(&api.WorkflowConfig{
		Workspace: api.WorkspaceConfig{Root: t.TempDir(), Mode: "directory"},
	})
	if health.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", health.Status, StatusBlocked)
	}
	if health.Issues[0].Code != "agent_command_missing" {
		t.Fatalf("first issue = %+v, want agent_command_missing", health.Issues[0])
	}
}

func TestWindowsPathIssue(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "valid", path: "frontend/src/App.tsx", want: false},
		{name: "question marks", path: "frontend/railway_log_review_20250828_124346.md??", want: true},
		{name: "control characters", path: "frontend/railway_log_review_20250828_124346.md\r\r", want: true},
		{name: "reserved device", path: "docs/CON.txt", want: true},
		{name: "trailing period", path: "docs/release-notes.", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windowsPathIssue(tt.path) != ""
			if got != tt.want {
				t.Fatalf("windowsPathIssue(%q) blocked = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
