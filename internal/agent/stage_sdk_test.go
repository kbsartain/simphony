package agent

import (
	"strings"
	"testing"

	"github.com/kbsartain/simphony/pkg/api"
)

func TestEffectiveCodexConfig_StageSwitchesToCodex(t *testing.T) {
	base := &api.CodexConfig{
		Provider: "claude", Command: "", Model: "glm-5.2", ModelProvider: "zai", AuthStyle: "bearer",
		StageOverrides: map[string]api.AgentStageOverride{
			"review": {Provider: "codex", Command: "codex app-server", Model: "gpt-5.5", ModelProvider: "openai", AuthStyle: "bearer"},
		},
	}
	review := effectiveCodexConfig(base, api.PipelineStage{Kind: "review"})
	if review.Provider != "codex" {
		t.Errorf("review provider = %q, want codex", review.Provider)
	}
	if review.Command != "codex app-server" {
		t.Errorf("review command = %q, want codex app-server", review.Command)
	}
	if review.Model != "gpt-5.5" {
		t.Errorf("review model = %q, want gpt-5.5", review.Model)
	}
	// A non-overridden stage keeps the base SDK.
	coding := effectiveCodexConfig(base, api.PipelineStage{Kind: "coding"})
	if coding.Provider != "claude" {
		t.Errorf("coding provider = %q, want base claude", coding.Provider)
	}
}

func TestEffectiveCodexConfig_ClaudeStageClearsCodexCommand(t *testing.T) {
	base := &api.CodexConfig{
		Provider: "codex", Command: "codex app-server",
		StageOverrides: map[string]api.AgentStageOverride{
			"review": {Provider: "claude", ModelProvider: "zai", AuthStyle: "bearer"},
		},
	}
	review := effectiveCodexConfig(base, api.PipelineStage{Kind: "review"})
	if review.Provider != "claude" {
		t.Errorf("review provider = %q, want claude", review.Provider)
	}
	if strings.Contains(strings.ToLower(review.Command), "codex") {
		t.Errorf("codex command leaked into claude stage: %q", review.Command)
	}
	if review.PermissionMode == "" {
		t.Error("claude stage should get a default permission mode")
	}
}

func TestResolvedAuthIsBearer(t *testing.T) {
	cases := []struct {
		style, endpoint string
		want            bool
	}{
		{"bearer", "", true},
		{"x-api-key", "https://api.z.ai/api/anthropic", true}, // compatible endpoint always bearer, even if mislabeled
		{"", "https://api.z.ai/api/anthropic", true},          // non-native → bearer
		{"x-api-key", "", false},                              // native + explicit x-api-key
		{"", "", false},                                       // native anthropic default → x-api-key
		{"bearer", "https://api.anthropic.com", true},         // native + explicit bearer honored
	}
	for _, c := range cases {
		if got := resolvedAuthIsBearer(c.style, c.endpoint); got != c.want {
			t.Errorf("resolvedAuthIsBearer(%q,%q) = %v, want %v", c.style, c.endpoint, got, c.want)
		}
	}
}
