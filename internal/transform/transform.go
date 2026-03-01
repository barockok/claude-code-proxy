package transform

import "log/slog"

const ClaudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

func InjectSystemPrompt(body map[string]interface{}) {
	prompt := map[string]interface{}{
		"type": "text",
		"text": ClaudeCodeSystemPrompt,
	}

	existing, exists := body["system"]
	if !exists {
		body["system"] = []interface{}{prompt}
		return
	}

	switch v := existing.(type) {
	case []interface{}:
		body["system"] = append([]interface{}{prompt}, v...)
	case string:
		body["system"] = []interface{}{prompt, map[string]interface{}{
			"type": "text",
			"text": v,
		}}
	default:
		body["system"] = []interface{}{prompt, v}
	}
}

func StripTTL(body map[string]interface{}) {
	if sys, ok := body["system"].([]interface{}); ok {
		stripTTLFromContentArray(sys)
	}

	if msgs, ok := body["messages"].([]interface{}); ok {
		for _, msg := range msgs {
			if m, ok := msg.(map[string]interface{}); ok {
				if content, ok := m["content"].([]interface{}); ok {
					stripTTLFromContentArray(content)
				}
			}
		}
	}
}

func stripTTLFromContentArray(items []interface{}) {
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		cc, ok := m["cache_control"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasTTL := cc["ttl"]; hasTTL {
			delete(cc, "ttl")
			slog.Debug("Removed ttl from cache_control")
		}
	}
}

func FilterSamplingParams(body map[string]interface{}) {
	tempVal, hasTemp := body["temperature"]
	topPVal, hasTopP := body["top_p"]

	tempF, tempIsFloat := toFloat64(tempVal)
	topPF, topPIsFloat := toFloat64(topPVal)

	if hasTemp && hasTopP {
		tempIsDefault := tempIsFloat && tempF == 1.0
		topPIsDefault := topPIsFloat && topPF == 1.0

		switch {
		case tempIsDefault && topPIsDefault:
			delete(body, "top_p")
			slog.Debug("Removed top_p=1.0 (both default, keeping temperature)")
		case topPIsDefault:
			delete(body, "top_p")
			slog.Debug("Removed default top_p, keeping temperature")
		case tempIsDefault:
			delete(body, "temperature")
			slog.Debug("Removed default temperature, keeping top_p")
		default:
			delete(body, "top_p")
			slog.Debug("Removed top_p (prefer temperature when both non-default)")
		}
	} else if hasTopP && topPIsFloat && topPF == 1.0 {
		delete(body, "top_p")
		slog.Debug("Removed default top_p=1.0")
	} else if hasTemp && tempIsFloat && tempF == 1.0 {
		delete(body, "temperature")
		slog.Debug("Removed default temperature=1.0")
	}
}

func ProcessRequestBody(body map[string]interface{}, stripTTL, filterSampling bool) {
	InjectSystemPrompt(body)
	if stripTTL {
		StripTTL(body)
	}
	if filterSampling {
		FilterSamplingParams(body)
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
