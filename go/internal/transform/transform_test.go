package transform

import (
	"encoding/json"
	"testing"
)

func TestInjectSystemPrompt_NoExisting(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{},
	}

	InjectSystemPrompt(body)

	system, ok := body["system"].([]interface{})
	if !ok {
		t.Fatal("system should be an array")
	}
	if len(system) != 1 {
		t.Fatalf("system length = %d, want 1", len(system))
	}

	entry := system[0].(map[string]interface{})
	if entry["text"] != ClaudeCodeSystemPrompt {
		t.Errorf("system text = %q, want %q", entry["text"], ClaudeCodeSystemPrompt)
	}
}

func TestInjectSystemPrompt_ExistingArray(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "existing"},
		},
	}

	InjectSystemPrompt(body)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}

	first := system[0].(map[string]interface{})
	if first["text"] != ClaudeCodeSystemPrompt {
		t.Error("claude code prompt should be first")
	}
}

func TestInjectSystemPrompt_ExistingString(t *testing.T) {
	body := map[string]interface{}{
		"system": "existing prompt",
	}

	InjectSystemPrompt(body)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}
}

func TestStripTTL(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "hello",
				"cache_control": map[string]interface{}{
					"type": "ephemeral",
					"ttl":  float64(300),
				},
			},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "hi",
						"cache_control": map[string]interface{}{
							"type": "ephemeral",
							"ttl":  float64(600),
						},
					},
				},
			},
		},
	}

	StripTTL(body)

	sys := body["system"].([]interface{})
	cc := sys[0].(map[string]interface{})["cache_control"].(map[string]interface{})
	if _, hasTTL := cc["ttl"]; hasTTL {
		t.Error("ttl should be stripped from system cache_control")
	}
	if cc["type"] != "ephemeral" {
		t.Error("cache_control type should be preserved")
	}

	msgs := body["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	mcc := content[0].(map[string]interface{})["cache_control"].(map[string]interface{})
	if _, hasTTL := mcc["ttl"]; hasTTL {
		t.Error("ttl should be stripped from message cache_control")
	}
}

func TestStripTTL_NoCacheControl(t *testing.T) {
	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
		},
	}
	StripTTL(body)
}

func TestFilterSamplingParams_BothPresent_BothDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
		"top_p":       1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed when both are default")
	}
	if _, ok := body["temperature"]; !ok {
		t.Error("temperature should be kept when both are default")
	}
}

func TestFilterSamplingParams_BothPresent_OnlyTopPDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.7,
		"top_p":       1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed")
	}
	if body["temperature"] != 0.7 {
		t.Error("temperature should be preserved")
	}
}

func TestFilterSamplingParams_BothPresent_OnlyTempDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
		"top_p":       0.9,
	}
	FilterSamplingParams(body)

	if _, ok := body["temperature"]; ok {
		t.Error("temperature should be removed")
	}
	if body["top_p"] != 0.9 {
		t.Error("top_p should be preserved")
	}
}

func TestFilterSamplingParams_BothPresent_BothNonDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.5,
		"top_p":       0.8,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be removed (prefer temperature)")
	}
	if body["temperature"] != 0.5 {
		t.Error("temperature should be preserved")
	}
}

func TestFilterSamplingParams_OnlyTopP_Default(t *testing.T) {
	body := map[string]interface{}{
		"top_p": 1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["top_p"]; ok {
		t.Error("top_p=1.0 should be removed when alone")
	}
}

func TestFilterSamplingParams_OnlyTemperature_Default(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 1.0,
	}
	FilterSamplingParams(body)

	if _, ok := body["temperature"]; ok {
		t.Error("temperature=1.0 should be removed when alone")
	}
}

func TestFilterSamplingParams_OnlyNonDefault(t *testing.T) {
	body := map[string]interface{}{
		"temperature": 0.7,
	}
	FilterSamplingParams(body)

	if body["temperature"] != 0.7 {
		t.Error("non-default temperature should be preserved")
	}
}

func TestProcessRequestBody(t *testing.T) {
	raw := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello"}],"temperature":1.0,"top_p":1.0}`

	var body map[string]interface{}
	json.Unmarshal([]byte(raw), &body)

	ProcessRequestBody(body, true, true)

	sys, ok := body["system"].([]interface{})
	if !ok || len(sys) == 0 {
		t.Error("system prompt should be injected")
	}

	if _, ok := body["top_p"]; ok {
		t.Error("top_p should be filtered")
	}
}
