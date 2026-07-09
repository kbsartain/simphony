package api

// ProviderCatalog is the set of known model vendors, keyed by lowercase vendor
// name (e.g. "anthropic", "zai", "openai", "kimi"). It is the single source of
// truth for the facts that were previously implicit or duplicated across
// WORKFLOW.md files: which local transport drives a vendor, its canonical base
// URL, how it authenticates, and which models (and thinking levels) it serves.
//
// See docs/adr/0001-provider-model-catalog.md.
type ProviderCatalog map[string]ProviderEntry

// ProviderEntry describes one upstream model vendor.
type ProviderEntry struct {
	// Transport is the local agent transport that drives this vendor:
	// "claude" (the Claude CLI) or "codex" (the Codex app-server).
	Transport string `json:"transport"`
	// BaseURL is the canonical endpoint for the vendor. Empty means the
	// transport's built-in default (e.g. Anthropic-native for "claude").
	BaseURL string `json:"base_url,omitempty"`
	// Auth describes how the vendor authenticates.
	Auth ProviderAuth `json:"auth"`
	// Models maps model id -> model capabilities. An empty map means model
	// names are not validated for this vendor.
	Models map[string]ProviderModel `json:"models,omitempty"`
}

// ProviderAuth describes how a vendor authenticates.
type ProviderAuth struct {
	// Env is the environment variable that holds the API key/token.
	Env string `json:"env,omitempty"`
	// Style is the wire auth style: "x-api-key" (Anthropic-native) or
	// "bearer" (Anthropic-compatible endpoints such as z.ai/Kimi, which read
	// the token from ANTHROPIC_AUTH_TOKEN).
	Style string `json:"style,omitempty"`
}

// ProviderModel describes a single model and the thinking levels it accepts.
type ProviderModel struct {
	// Thinking lists the reasoning-effort levels the model supports, drawn
	// from: none, minimal, low, medium, high, xhigh. Empty means unconstrained
	// (thinking level is not validated for this model).
	Thinking []string `json:"thinking,omitempty"`
}
