package pricing

import "testing"

func TestLookupExactAndPrefix(t *testing.T) {
	tests := []struct {
		model   string
		wantIn  float64
		wantOut float64
		wantOK  bool
	}{
		{model: "claude-opus-4.7", wantIn: 15.00, wantOut: 75.00, wantOK: true},
		{model: "claude-sonnet-4.6", wantIn: 3.00, wantOut: 15.00, wantOK: true},
		{model: "Claude-Sonnet-4.6", wantIn: 3.00, wantOut: 15.00, wantOK: true},
		{model: " gpt-5.4-mini ", wantIn: 0.25, wantOut: 2.00, wantOK: true},
		{model: "gpt-5.6", wantIn: 1.25, wantOut: 10.00, wantOK: true},
		{model: "glm-5.2", wantIn: 0.60, wantOut: 2.00, wantOK: true},
		{model: "kimi-k3.1-pro", wantIn: 0.60, wantOut: 2.50, wantOK: true},
		{model: "deepseek-v4-pro", wantIn: 0.27, wantOut: 1.10, wantOK: true},
		{model: "deepseek-reasoner", wantIn: 0.55, wantOut: 2.19, wantOK: true},
		{model: "gemini-2.5-flash", wantIn: 0.30, wantOut: 2.50, wantOK: true},
		{model: "some-private-model", wantOK: false},
		{model: "", wantOK: false},
	}
	for _, tt := range tests {
		rates, ok := Lookup(tt.model)
		if ok != tt.wantOK {
			t.Fatalf("Lookup(%q) ok = %v, want %v", tt.model, ok, tt.wantOK)
		}
		if !tt.wantOK {
			continue
		}
		if rates.Input != tt.wantIn || rates.Output != tt.wantOut {
			t.Fatalf("Lookup(%q) = %+v, want in=%v out=%v", tt.model, rates, tt.wantIn, tt.wantOut)
		}
	}
}

func TestCostMath(t *testing.T) {
	rates := Rates{Input: 3.00, Output: 15.00}
	got := rates.Cost(1_000_000, 500_000)
	want := 3.00 + 7.50
	if got != want {
		t.Fatalf("Cost = %v, want %v", got, want)
	}
	if zero := (Rates{}).Cost(100, 100); zero != 0 {
		t.Fatalf("zero rates Cost = %v, want 0", zero)
	}
}
