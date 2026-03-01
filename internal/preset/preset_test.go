package preset

import (
	"embed"
	"testing"
)

//go:embed testdata/*.json
var testFS embed.FS

func TestLoad(t *testing.T) {
	mgr := NewManager(testFS, "testdata")

	p, err := mgr.Load("test")
	if err != nil {
		t.Fatal(err)
	}

	if p.System == "" {
		t.Error("system should not be empty")
	}
}

func TestLoadMissing(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	_, err := mgr.Load("nonexistent")
	if err == nil {
		t.Error("should error on missing preset")
	}
}

func TestLoadCaches(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p1, _ := mgr.Load("test")
	p2, _ := mgr.Load("test")

	if p1 != p2 {
		t.Error("second load should return cached pointer")
	}
}

func TestApply_NoThinking(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p, _ := mgr.Load("test")

	body := map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{"type": "text", "text": "existing"},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "hello",
			},
		},
	}

	Apply(body, p)

	system := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system length = %d, want 2", len(system))
	}

	msgs := body["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("messages length = %d, want 2 (original + suffix)", len(msgs))
	}
}

func TestApply_WithThinking(t *testing.T) {
	mgr := NewManager(testFS, "testdata")
	p, _ := mgr.Load("test")

	body := map[string]interface{}{
		"system": []interface{}{},
		"thinking": map[string]interface{}{
			"type": "enabled",
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "hello",
			},
		},
	}

	Apply(body, p)

	msgs := body["messages"].([]interface{})
	if len(msgs) < 2 {
		t.Fatal("should have injected suffixEt message")
	}

	suffixMsg := msgs[1].(map[string]interface{})
	content := suffixMsg["content"].([]interface{})
	textBlock := content[0].(map[string]interface{})
	if textBlock["text"] != "thinking suffix" {
		t.Errorf("should use suffixEt when thinking enabled, got %q", textBlock["text"])
	}
}
