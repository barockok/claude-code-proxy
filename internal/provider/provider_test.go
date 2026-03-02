package provider

import (
	"testing"
)

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		model string
	}{
		{"simple", `{"model":"claude-sonnet-4-20250514","messages":[]}`, "claude-sonnet-4-20250514"},
		{"nested", `{"messages":[],"model":"gpt-4o","stream":true}`, "gpt-4o"},
		{"no model", `{"messages":[]}`, ""},
		{"empty", `{}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractModel([]byte(tt.body))
			if got != tt.model {
				t.Errorf("ExtractModel = %q, want %q", got, tt.model)
			}
		})
	}
}

func TestRouterMatch(t *testing.T) {
	r := NewRouter()
	r.Add("anthropic", []string{"claude-*"})
	r.Add("openai", []string{"gpt-*", "o1-*", "o3-*"})
	r.Add("gemini", []string{"gemini-*"})

	tests := []struct {
		model    string
		provider string
		ok       bool
	}{
		{"claude-sonnet-4-20250514", "anthropic", true},
		{"claude-3-5-haiku-20241022", "anthropic", true},
		{"gpt-4o", "openai", true},
		{"o1-preview", "openai", true},
		{"gemini-2.0-flash", "gemini", true},
		{"unknown-model", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			name, ok := r.Match(tt.model)
			if ok != tt.ok {
				t.Errorf("Match ok = %v, want %v", ok, tt.ok)
			}
			if name != tt.provider {
				t.Errorf("Match provider = %q, want %q", name, tt.provider)
			}
		})
	}
}
