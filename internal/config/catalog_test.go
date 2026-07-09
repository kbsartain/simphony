package config

import (
	"testing"

	"github.com/kbsartain/simphony/pkg/api"
)

func warningCodes(warnings []RegistryWarning) map[string]RegistryWarning {
	out := make(map[string]RegistryWarning, len(warnings))
	for _, w := range warnings {
		out[w.Code] = w
	}
	return out
}

func TestBuiltinProviderCatalogHasCoreVendors(t *testing.T) {
	catalog := BuiltinProviderCatalog()
	for _, vendor := range []string{"anthropic", "zai", "openai", "kimi"} {
		if _, ok := catalog[vendor]; !ok {
			t.Errorf("builtin catalog missing vendor %q", vendor)
		}
	}
	if catalog["zai"].BaseURL != "https://api.z.ai/api/anthropic" {
		t.Errorf("zai base_url = %q, want z.ai anthropic endpoint", catalog["zai"].BaseURL)
	}
	if _, ok := catalog["zai"].Models["glm-5.2"]; !ok {
		t.Error("zai catalog missing glm-5.2")
	}
}

func TestValidateRuntimeAgainstCatalog_Clean(t *testing.T) {
	rt := api.AgentRuntimeConfig{
		ModelProvider:   "zai",
		EndpointURL:     "https://api.z.ai/api/anthropic",
		Model:           "glm-5.2",
		ReasoningEffort: "high",
	}
	warnings := validateRuntimeAgainstCatalog("geekli", rt, BuiltinProviderCatalog())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", warnings)
	}
}

func TestValidateRuntimeAgainstCatalog_EndpointMismatch(t *testing.T) {
	// The live-config drift: declares anthropic but points at z.ai serving glm-5.2.
	rt := api.AgentRuntimeConfig{
		ModelProvider:   "anthropic",
		EndpointURL:     "https://api.z.ai/api/anthropic",
		Model:           "glm-5.2",
		ReasoningEffort: "high",
	}
	got := warningCodes(validateRuntimeAgainstCatalog("geekli", rt, BuiltinProviderCatalog()))
	if _, ok := got["provider_endpoint_mismatch"]; !ok {
		t.Error("expected provider_endpoint_mismatch warning")
	}
	if _, ok := got["unknown_model"]; !ok {
		t.Error("expected unknown_model warning (glm-5.2 is not an anthropic model)")
	}
}

func TestValidateRuntimeAgainstCatalog_UnknownProvider(t *testing.T) {
	rt := api.AgentRuntimeConfig{ModelProvider: "acme", Model: "widget-1"}
	got := warningCodes(validateRuntimeAgainstCatalog("p", rt, BuiltinProviderCatalog()))
	if _, ok := got["unknown_provider"]; !ok {
		t.Error("expected unknown_provider warning")
	}
	// Once the vendor is unknown, we do not additionally warn about the model.
	if _, ok := got["unknown_model"]; ok {
		t.Error("did not expect unknown_model when the vendor itself is unknown")
	}
}

func TestValidateRuntimeAgainstCatalog_UnsupportedThinking(t *testing.T) {
	rt := api.AgentRuntimeConfig{
		ModelProvider:   "zai",
		EndpointURL:     "https://api.z.ai/api/anthropic",
		Model:           "glm-5.2",
		ReasoningEffort: "xhigh", // glm-5.2 supports up to high in the builtin catalog
	}
	got := warningCodes(validateRuntimeAgainstCatalog("geekli", rt, BuiltinProviderCatalog()))
	if _, ok := got["unsupported_thinking_level"]; !ok {
		t.Error("expected unsupported_thinking_level warning")
	}
}

func TestValidateRuntimeAgainstCatalog_EmptyVendorSkips(t *testing.T) {
	rt := api.AgentRuntimeConfig{Model: "glm-5.2", EndpointURL: "https://api.z.ai/api/anthropic"}
	if warnings := validateRuntimeAgainstCatalog("p", rt, BuiltinProviderCatalog()); len(warnings) != 0 {
		t.Fatalf("expected no warnings when model_provider is empty, got %+v", warnings)
	}
}

func TestParseAndMergeProviderCatalogOverride(t *testing.T) {
	override := map[string]interface{}{
		"zai": map[string]interface{}{
			"transport": "claude",
			"base_url":  "https://api.z.ai/api/anthropic",
			"auth":      map[string]interface{}{"env": "ZAI_API_KEY", "style": "bearer"},
			"models": map[string]interface{}{
				"glm-5.2": map[string]interface{}{"thinking": []interface{}{"low", "medium", "high", "xhigh"}},
			},
		},
		"local": map[string]interface{}{
			"transport": "codex",
			"base_url":  "http://127.0.0.1:1234/v1",
			"auth":      map[string]interface{}{"env": "LOCAL_KEY"},
		},
	}
	parsed, err := parseProviderCatalog(override)
	if err != nil {
		t.Fatalf("parseProviderCatalog: %v", err)
	}
	merged := mergeProviderCatalog(BuiltinProviderCatalog(), parsed)

	// Override replaces the vendor entry: glm-5.2 now permits xhigh.
	if got := merged["zai"].Models["glm-5.2"].Thinking; !containsString(got, "xhigh") {
		t.Errorf("override did not take effect, glm-5.2 thinking = %v", got)
	}
	// New vendor is added alongside builtins.
	if _, ok := merged["local"]; !ok {
		t.Error("merged catalog missing added vendor 'local'")
	}
	if _, ok := merged["anthropic"]; !ok {
		t.Error("merge dropped builtin vendor 'anthropic'")
	}
}

func TestApplyProviderCatalog_DerivesBaseAuthAndEndpoint(t *testing.T) {
	cfg := &api.WorkflowConfig{}
	cfg.AgentRuntime = api.AgentRuntimeConfig{Provider: "claude", ModelProvider: "zai", Model: "glm-5.2"}
	ApplyProviderCatalog(cfg, BuiltinProviderCatalog())
	if cfg.AgentRuntime.AuthStyle != "bearer" {
		t.Errorf("AuthStyle = %q, want bearer", cfg.AgentRuntime.AuthStyle)
	}
	if cfg.AgentRuntime.EndpointURL != "https://api.z.ai/api/anthropic" {
		t.Errorf("EndpointURL = %q, want derived z.ai endpoint", cfg.AgentRuntime.EndpointURL)
	}
}

func TestApplyProviderCatalog_AutoWiresKeyFromEnv(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "secret-zai")
	cfg := &api.WorkflowConfig{}
	cfg.AgentRuntime = api.AgentRuntimeConfig{Provider: "claude", ModelProvider: "zai", Model: "glm-5.2"}
	ApplyProviderCatalog(cfg, BuiltinProviderCatalog())
	if cfg.AgentRuntime.APIKey != "secret-zai" || !cfg.AgentRuntime.APIKeyConfigured {
		t.Errorf("key not auto-wired: %q configured=%v", cfg.AgentRuntime.APIKey, cfg.AgentRuntime.APIKeyConfigured)
	}
}

func TestApplyProviderCatalog_RespectsExplicitValues(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "env-key")
	cfg := &api.WorkflowConfig{}
	cfg.AgentRuntime = api.AgentRuntimeConfig{
		Provider: "claude", ModelProvider: "zai",
		EndpointURL: "https://custom.example", APIKey: "explicit", APIKeyConfigured: true,
	}
	ApplyProviderCatalog(cfg, BuiltinProviderCatalog())
	if cfg.AgentRuntime.EndpointURL != "https://custom.example" {
		t.Errorf("explicit endpoint overridden: %q", cfg.AgentRuntime.EndpointURL)
	}
	if cfg.AgentRuntime.APIKey != "explicit" {
		t.Errorf("explicit key overridden: %q", cfg.AgentRuntime.APIKey)
	}
}

func TestApplyProviderCatalog_StageDerivesTransport(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	cfg := &api.WorkflowConfig{}
	cfg.AgentRuntime = api.AgentRuntimeConfig{
		Provider: "claude", ModelProvider: "zai", Model: "glm-5.2",
		StageOverrides: map[string]api.AgentStageOverride{
			"review": {ModelProvider: "openai", Model: "gpt-5.5"},
		},
	}
	ApplyProviderCatalog(cfg, BuiltinProviderCatalog())
	ov := cfg.AgentRuntime.StageOverrides["review"]
	if ov.Provider != "codex" {
		t.Errorf("stage transport = %q, want codex (derived from openai vendor)", ov.Provider)
	}
	if ov.Command != "codex app-server" {
		t.Errorf("stage command = %q, want codex app-server", ov.Command)
	}
	if ov.APIKey != "openai-key" || !ov.APIKeyConfigured {
		t.Errorf("stage key not auto-wired from OPENAI_API_KEY: %q", ov.APIKey)
	}
}

func TestParseProviderCatalog_RejectsBadTransport(t *testing.T) {
	_, err := parseProviderCatalog(map[string]interface{}{
		"bad": map[string]interface{}{"transport": "grpc"},
	})
	if err == nil {
		t.Fatal("expected error for invalid transport")
	}
}
