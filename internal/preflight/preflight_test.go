package preflight

import (
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
