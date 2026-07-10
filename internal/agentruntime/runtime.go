// Package agentruntime resolves provider-neutral per-stage agent settings.
package agentruntime

import (
	"maps"
	"path/filepath"
	"strings"

	"github.com/kbsartain/simphony/pkg/api"
)

// EffectiveConfig resolves the complete runtime used for a pipeline stage.
// Switching execution providers starts from that provider's defaults so Codex
// commands, credentials, and sandbox settings cannot leak into Claude (or vice
// versa).
func EffectiveConfig(cfg *api.AgentRuntimeConfig, stage api.PipelineStage) api.AgentRuntimeConfig {
	if cfg == nil {
		return api.AgentRuntimeConfig{}
	}
	effective := cloneConfig(*cfg)
	stageKey := strings.ToLower(strings.TrimSpace(stage.Kind))
	if stageKey == "" || cfg.StageOverrides == nil {
		return effective
	}
	override, ok := cfg.StageOverrides[stageKey]
	if !ok {
		return effective
	}
	provider := strings.ToLower(strings.TrimSpace(override.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	}
	if provider != "" && !strings.EqualFold(provider, cfg.Provider) {
		effective = defaultsForProvider(provider)
		effective.TurnTimeoutMs = cfg.TurnTimeoutMs
		effective.ReadTimeoutMs = cfg.ReadTimeoutMs
		effective.StallTimeoutMs = cfg.StallTimeoutMs
		effective.Skills = appendUniqueSkills(nil, cfg.Skills...)
		effective.StageOverrides = cfg.StageOverrides
	}
	if override.Provider != "" {
		effective.Provider = provider
	}
	if override.Command != "" {
		effective.Command = override.Command
	}
	if override.Model != "" {
		effective.Model = override.Model
	}
	if override.ModelProvider != "" {
		effective.ModelProvider = override.ModelProvider
	}
	if override.ReasoningEffort != "" {
		effective.ReasoningEffort = override.ReasoningEffort
	}
	if override.EndpointURL != "" {
		effective.EndpointURL = override.EndpointURL
	}
	if override.APIKeyConfigured {
		effective.APIKey = override.APIKey
		effective.APIKeyConfigured = true
	}
	if override.AuthTokenConfigured {
		effective.AuthToken = override.AuthToken
		effective.AuthTokenConfigured = true
	}
	if len(override.Env) > 0 {
		env := make(map[string]string, len(effective.Env)+len(override.Env))
		for key, value := range effective.Env {
			env[key] = value
		}
		for key, value := range override.Env {
			env[key] = value
		}
		effective.Env = env
	}
	if len(override.Skills) > 0 {
		effective.Skills = appendUniqueSkills(effective.Skills, override.Skills...)
	}
	if override.AllowedTools != nil {
		effective.AllowedTools = append([]string(nil), override.AllowedTools...)
	}
	if override.DisallowedTools != nil {
		effective.DisallowedTools = append([]string(nil), override.DisallowedTools...)
	}
	if override.PermissionMode != "" {
		effective.PermissionMode = override.PermissionMode
	}
	if override.SettingSources != nil {
		effective.SettingSources = append([]string(nil), override.SettingSources...)
	}
	if override.ApprovalPolicy != "" {
		effective.ApprovalPolicy = override.ApprovalPolicy
	}
	if override.ThreadSandbox != "" {
		effective.ThreadSandbox = override.ThreadSandbox
	}
	if override.TurnSandboxPolicy != "" {
		effective.TurnSandboxPolicy = override.TurnSandboxPolicy
	}
	return effective
}

func defaultsForProvider(provider string) api.AgentRuntimeConfig {
	runtime := api.AgentRuntimeConfig{
		Provider:          provider,
		ApprovalPolicy:    "auto",
		ThreadSandbox:     "none",
		TurnSandboxPolicy: "none",
		TurnTimeoutMs:     3600000,
		ReadTimeoutMs:     5000,
		StallTimeoutMs:    300000,
	}
	if provider == "codex" {
		runtime.Command = "codex app-server"
	} else if provider == "claude" {
		runtime.PermissionMode = "acceptEdits"
	}
	return runtime
}

func cloneConfig(runtime api.AgentRuntimeConfig) api.AgentRuntimeConfig {
	if runtime.Env != nil {
		runtime.Env = maps.Clone(runtime.Env)
	}
	runtime.Skills = append([]api.AgentSkillRef(nil), runtime.Skills...)
	runtime.AllowedTools = append([]string(nil), runtime.AllowedTools...)
	runtime.DisallowedTools = append([]string(nil), runtime.DisallowedTools...)
	runtime.SettingSources = append([]string(nil), runtime.SettingSources...)
	return runtime
}

func appendUniqueSkills(base []api.AgentSkillRef, extras ...api.AgentSkillRef) []api.AgentSkillRef {
	seen := make(map[string]struct{}, len(base)+len(extras))
	out := make([]api.AgentSkillRef, 0, len(base)+len(extras))
	for _, skill := range append(base, extras...) {
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Path = strings.TrimSpace(skill.Path)
		if skill.Name == "" && skill.Path == "" {
			continue
		}
		if skill.Name == "" {
			skill.Name = filepath.Base(skill.Path)
		}
		key := strings.ToLower(skill.Name) + "\x00" + strings.ToLower(skill.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, skill)
	}
	return out
}
