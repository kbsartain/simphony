// Package pricing provides estimated per-model token pricing used for
// spend statistics. Rates are list-price estimates in USD per 1M tokens;
// subscription/quota plans (for example Z.ai coding plans) do not map
// one-to-one to token prices, so reported costs are approximations
// suitable for relative comparisons, not invoices. Unknown models simply
// have no rates and contribute no cost.
package pricing

import "strings"

// Rates holds estimated USD pricing per 1M tokens.
type Rates struct {
	Input  float64
	Output float64
}

var exactRates = map[string]Rates{}

type prefixRate struct {
	prefix string
	rates  Rates
}

// prefixRates is evaluated in order; the first matching prefix wins.
var prefixRates = []prefixRate{
	// Anthropic Claude
	{"claude-opus", Rates{15.00, 75.00}},
	{"claude-sonnet", Rates{3.00, 15.00}},
	{"claude-haiku", Rates{1.00, 5.00}},
	// OpenAI GPT-5 family
	{"gpt-5.4-mini", Rates{0.25, 2.00}},
	{"gpt-5", Rates{1.25, 10.00}},
	// Google Gemini
	{"gemini-3-pro", Rates{2.00, 12.00}},
	{"gemini-2.5-pro", Rates{1.25, 10.00}},
	{"gemini", Rates{0.30, 2.50}},
	// Z.ai GLM (coding-plan estimate)
	{"glm-", Rates{0.60, 2.00}},
	// Moonshot Kimi
	{"kimi-", Rates{0.60, 2.50}},
	// DeepSeek
	{"deepseek-reasoner", Rates{0.55, 2.19}},
	{"deepseek", Rates{0.27, 1.10}},
}

// Lookup returns the estimated rates for a model. Matching is
// case-insensitive on trimmed names: exact IDs first, then prefixes.
// ok is false when the model is unknown.
func Lookup(model string) (rates Rates, ok bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return Rates{}, false
	}
	if rates, ok := exactRates[key]; ok {
		return rates, true
	}
	for _, entry := range prefixRates {
		if strings.HasPrefix(key, entry.prefix) {
			return entry.rates, true
		}
	}
	return Rates{}, false
}

// Cost returns the estimated USD cost for token counts at the given rates.
func (r Rates) Cost(inputTokens, outputTokens int) float64 {
	return r.Input*float64(inputTokens)/1e6 + r.Output*float64(outputTokens)/1e6
}
