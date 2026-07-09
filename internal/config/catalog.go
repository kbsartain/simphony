package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kbsartain/simphony/pkg/api"
)

// BuiltinProviderCatalog returns the default, code-shipped provider catalog.
// simphony.yaml may override or extend it via a top-level `providers:` map.
//
// Model lists are curated starting points, not exhaustive; users add models by
// extending the catalog. An empty Models map disables model-name validation for
// that vendor. See docs/adr/0001-provider-model-catalog.md.
func BuiltinProviderCatalog() api.ProviderCatalog {
	anthropicThinking := []string{"none", "low", "medium", "high", "xhigh"}
	compatThinking := []string{"none", "low", "medium", "high"}
	return api.ProviderCatalog{
		"anthropic": {
			Transport: "claude",
			Auth:      api.ProviderAuth{Env: "ANTHROPIC_API_KEY", Style: "x-api-key"},
			Models: map[string]api.ProviderModel{
				"claude-opus-4-8":  {Thinking: anthropicThinking},
				"claude-sonnet-5":  {Thinking: anthropicThinking},
				"claude-haiku-4-5": {Thinking: anthropicThinking},
			},
		},
		"zai": {
			Transport: "claude",
			BaseURL:   "https://api.z.ai/api/anthropic",
			Auth:      api.ProviderAuth{Env: "ZAI_API_KEY", Style: "bearer"},
			Models: map[string]api.ProviderModel{
				"glm-5.2": {Thinking: compatThinking},
				"glm-4.6": {Thinking: compatThinking},
			},
		},
		"openai": {
			Transport: "codex",
			Auth:      api.ProviderAuth{Env: "OPENAI_API_KEY", Style: "bearer"},
			// Models intentionally empty for now; add ids to enable validation.
		},
		"kimi": {
			Transport: "claude",
			BaseURL:   "https://api.moonshot.ai/anthropic",
			Auth:      api.ProviderAuth{Env: "KIMI_API_KEY", Style: "bearer"},
			Models: map[string]api.ProviderModel{
				"kimi-k2": {Thinking: compatThinking},
			},
		},
	}
}

// resolveRegistryCatalog builds registry.Catalog from the builtin defaults plus
// an optional `providers:` override map from simphony.yaml.
func resolveRegistryCatalog(m map[string]interface{}, registry *ProjectRegistry) error {
	catalog := BuiltinProviderCatalog()
	if m != nil {
		override, err := parseProviderCatalog(m)
		if err != nil {
			return fmt.Errorf("%s: %w", api.ErrProjectRegistryParseError, err)
		}
		catalog = mergeProviderCatalog(catalog, override)
	}
	registry.Catalog = catalog
	return nil
}

// parseProviderCatalog reads a `providers:` map into a ProviderCatalog.
func parseProviderCatalog(m map[string]interface{}) (api.ProviderCatalog, error) {
	catalog := make(api.ProviderCatalog, len(m))
	for rawName, rawEntry := range m {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		entryMap, ok := rawEntry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("providers.%s must be a map", rawName)
		}
		entry := api.ProviderEntry{}
		if v, ok := getString(entryMap, "transport"); ok {
			entry.Transport = strings.ToLower(strings.TrimSpace(v))
		}
		switch entry.Transport {
		case "", "claude", "codex":
		default:
			return nil, fmt.Errorf("providers.%s.transport must be claude or codex, got %q", rawName, entry.Transport)
		}
		if v, ok := getString(entryMap, "base_url"); ok {
			entry.BaseURL = strings.TrimSpace(ResolveEnvVar(v))
		}
		if authMap := getSubMap(entryMap, "auth"); authMap != nil {
			if v, ok := getString(authMap, "env"); ok {
				entry.Auth.Env = strings.TrimSpace(v)
			}
			if v, ok := getString(authMap, "style"); ok {
				entry.Auth.Style = strings.ToLower(strings.TrimSpace(v))
			}
		}
		if modelsMap := getSubMap(entryMap, "models"); modelsMap != nil {
			entry.Models = make(map[string]api.ProviderModel, len(modelsMap))
			for rawModel, rawModelEntry := range modelsMap {
				modelID := strings.TrimSpace(rawModel)
				if modelID == "" {
					continue
				}
				model := api.ProviderModel{}
				if modelEntryMap, ok := rawModelEntry.(map[string]interface{}); ok {
					if thinking, ok := getStringSlice(modelEntryMap, "thinking"); ok {
						normalized, err := normalizeThinkingLevels(rawName, modelID, thinking)
						if err != nil {
							return nil, err
						}
						model.Thinking = normalized
					}
				}
				entry.Models[modelID] = model
			}
		}
		catalog[name] = entry
	}
	return catalog, nil
}

// normalizeThinkingLevels validates and normalizes a model's thinking list.
func normalizeThinkingLevels(providerName, modelID string, levels []string) ([]string, error) {
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		normalized, err := normalizeReasoningEffort(level)
		if err != nil {
			return nil, fmt.Errorf("providers.%s.models.%s.thinking %w", providerName, modelID, err)
		}
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out, nil
}

// mergeProviderCatalog overlays override entries on top of base. A provider
// present in override fully replaces the base entry for that provider.
func mergeProviderCatalog(base, override api.ProviderCatalog) api.ProviderCatalog {
	merged := make(api.ProviderCatalog, len(base)+len(override))
	for name, entry := range base {
		merged[name] = entry
	}
	for name, entry := range override {
		merged[name] = entry
	}
	return merged
}

// ApplyProviderCatalog enriches a resolved workflow config with values derived
// from the provider catalog: auth style and (when unset) endpoint for the base
// runtime, and transport/endpoint/auth/command for any stage override that names
// a vendor. Explicitly configured values are never overridden. This lets a
// minimal config (or stage) select a vendor and inherit the rest — and lets a
// stage switch the coding SDK entirely (e.g. code on Claude, review on Codex).
func ApplyProviderCatalog(cfg *api.WorkflowConfig, catalog api.ProviderCatalog) {
	if cfg == nil {
		return
	}
	if catalog == nil {
		catalog = BuiltinProviderCatalog()
	}
	deriveRuntimeFromCatalog(&cfg.AgentRuntime, catalog)
	for key, override := range cfg.AgentRuntime.StageOverrides {
		deriveStageOverrideFromCatalog(&override, catalog)
		cfg.AgentRuntime.StageOverrides[key] = override
	}
}

// deriveRuntimeFromCatalog fills auth style and endpoint for the base runtime
// from its ModelProvider vendor. The base Provider (transport) is left as the
// user configured it; per-stage transport switching is handled separately.
func deriveRuntimeFromCatalog(rt *api.AgentRuntimeConfig, catalog api.ProviderCatalog) {
	entry, ok := catalog[strings.ToLower(strings.TrimSpace(rt.ModelProvider))]
	if !ok {
		return
	}
	if rt.AuthStyle == "" {
		rt.AuthStyle = entry.Auth.Style
	}
	if strings.TrimSpace(rt.EndpointURL) == "" && entry.BaseURL != "" {
		rt.EndpointURL = entry.BaseURL
	}
	// Auto-wire the key from the vendor's env var when none was configured.
	if !rt.APIKeyConfigured && !rt.AuthTokenConfigured && entry.Auth.Env != "" {
		if v := strings.TrimSpace(os.Getenv(entry.Auth.Env)); v != "" {
			rt.APIKey = v
			rt.APIKeyConfigured = true
		}
	}
}

// deriveStageOverrideFromCatalog resolves a stage override's transport, endpoint,
// auth style, and command from its ModelProvider vendor when not set explicitly.
func deriveStageOverrideFromCatalog(override *api.AgentStageOverride, catalog api.ProviderCatalog) {
	entry, ok := catalog[strings.ToLower(strings.TrimSpace(override.ModelProvider))]
	if !ok {
		return
	}
	if strings.TrimSpace(override.Provider) == "" && entry.Transport != "" {
		override.Provider = entry.Transport
	}
	if strings.TrimSpace(override.EndpointURL) == "" && entry.BaseURL != "" {
		override.EndpointURL = entry.BaseURL
	}
	if strings.TrimSpace(override.AuthStyle) == "" && entry.Auth.Style != "" {
		override.AuthStyle = entry.Auth.Style
	}
	if strings.TrimSpace(override.Command) == "" && strings.EqualFold(override.Provider, "codex") {
		override.Command = "codex app-server"
	}
	// Auto-wire the stage's key from the vendor's env var when none was
	// configured, so a cross-vendor stage does not inherit the base vendor's key.
	if !override.APIKeyConfigured && !override.AuthTokenConfigured && entry.Auth.Env != "" {
		if v := strings.TrimSpace(os.Getenv(entry.Auth.Env)); v != "" {
			override.APIKey = v
			override.APIKeyConfigured = true
		}
	}
}

// catalogWarnings validates each resolved project runtime against the catalog
// and returns non-fatal warnings (unknown vendor/model, unsupported thinking
// level, vendor/endpoint mismatch). Phase 1 is warn-only.
func catalogWarnings(projects []registryResolvedProject, catalog api.ProviderCatalog) []RegistryWarning {
	if catalog == nil {
		catalog = BuiltinProviderCatalog()
	}
	var warnings []RegistryWarning
	for _, item := range projects {
		if item.cfg == nil {
			continue
		}
		warnings = append(warnings, validateRuntimeAgainstCatalog(item.project.ID, item.cfg.AgentRuntime, catalog)...)
	}
	return warnings
}

func validateRuntimeAgainstCatalog(projectID string, rt api.AgentRuntimeConfig, catalog api.ProviderCatalog) []RegistryWarning {
	var warnings []RegistryWarning
	vendor := strings.ToLower(strings.TrimSpace(rt.ModelProvider))
	endpoint := normalizeBaseURL(rt.EndpointURL)
	model := strings.TrimSpace(rt.Model)
	effort := strings.TrimSpace(rt.ReasoningEffort)

	// Vendor/endpoint mismatch: the endpoint matches a known vendor that differs
	// from the declared model_provider (e.g. model_provider: anthropic while the
	// endpoint is z.ai).
	if endpoint != "" && vendor != "" {
		if matched, ok := vendorForBaseURL(catalog, endpoint); ok && matched != vendor {
			warnings = append(warnings, RegistryWarning{
				Code:       "provider_endpoint_mismatch",
				Message:    fmt.Sprintf("project %q: model_provider is %q but endpoint_url matches provider %q", projectID, vendor, matched),
				ProjectIDs: []string{projectID},
			})
		}
	}

	if vendor == "" {
		return warnings
	}
	entry, known := catalog[vendor]
	if !known {
		warnings = append(warnings, RegistryWarning{
			Code:       "unknown_provider",
			Message:    fmt.Sprintf("project %q: model_provider %q is not in the provider catalog (%s)", projectID, vendor, knownProviders(catalog)),
			ProjectIDs: []string{projectID},
		})
		return warnings
	}

	if model != "" && len(entry.Models) > 0 {
		if _, ok := entry.Models[model]; !ok {
			warnings = append(warnings, RegistryWarning{
				Code:       "unknown_model",
				Message:    fmt.Sprintf("project %q: model %q is not listed for provider %q", projectID, model, vendor),
				ProjectIDs: []string{projectID},
			})
		}
	}

	if effort != "" && model != "" {
		if m, ok := entry.Models[model]; ok && len(m.Thinking) > 0 && !containsString(m.Thinking, effort) {
			warnings = append(warnings, RegistryWarning{
				Code:       "unsupported_thinking_level",
				Message:    fmt.Sprintf("project %q: reasoning_effort %q is not supported by model %q (%s)", projectID, effort, model, strings.Join(m.Thinking, ", ")),
				ProjectIDs: []string{projectID},
			})
		}
	}

	return warnings
}

// vendorForBaseURL returns the catalog vendor whose base URL matches url.
func vendorForBaseURL(catalog api.ProviderCatalog, url string) (string, bool) {
	url = normalizeBaseURL(url)
	if url == "" {
		return "", false
	}
	for name, entry := range catalog {
		if entry.BaseURL != "" && normalizeBaseURL(entry.BaseURL) == url {
			return name, true
		}
	}
	return "", false
}

func normalizeBaseURL(url string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(url), "/"))
}

func knownProviders(catalog api.ProviderCatalog) string {
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
