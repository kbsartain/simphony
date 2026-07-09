package agent

import "testing"

func TestIsNativeAnthropicEndpoint(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"", true},                                    // empty = CLI default = native Anthropic
		{"https://api.anthropic.com", true},           // native
		{"https://api.anthropic.com/v1", true},        // native with path
		{"  https://API.Anthropic.com  ", true},       // whitespace + case insensitive
		{"https://api.z.ai/api/anthropic", false},     // z.ai (bearer)
		{"https://api.moonshot.ai/anthropic", false},  // Kimi (bearer)
		{"https://openai-compatible.example/v1", false},
	}
	for _, c := range cases {
		if got := isNativeAnthropicEndpoint(c.url); got != c.want {
			t.Errorf("isNativeAnthropicEndpoint(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
