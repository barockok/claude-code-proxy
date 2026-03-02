package provider

import (
	"encoding/json"
	"path/filepath"
)

// ExtractModel does a lightweight extraction of the "model" field from JSON bytes.
func ExtractModel(body []byte) string {
	var peek struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return ""
	}
	return peek.Model
}

type routeEntry struct {
	name     string
	patterns []string
}

// Router matches model names to provider names using glob patterns.
type Router struct {
	entries []routeEntry
}

func NewRouter() *Router {
	return &Router{}
}

func (r *Router) Add(name string, patterns []string) {
	r.entries = append(r.entries, routeEntry{name: name, patterns: patterns})
}

// Match returns the first provider name whose glob pattern matches the model.
func (r *Router) Match(model string) (string, bool) {
	for _, e := range r.entries {
		for _, pat := range e.patterns {
			if matched, _ := filepath.Match(pat, model); matched {
				return e.name, true
			}
		}
	}
	return "", false
}
