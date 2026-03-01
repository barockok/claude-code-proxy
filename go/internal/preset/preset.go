package preset

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
)

type Preset struct {
	System   string `json:"system"`
	Suffix   string `json:"suffix"`
	SuffixEt string `json:"suffixEt"`
}

type Manager struct {
	fs     fs.FS
	prefix string
	cache  map[string]*Preset
	mu     sync.RWMutex
}

func NewManager(fsys fs.FS, prefix string) *Manager {
	return &Manager{
		fs:     fsys,
		prefix: prefix,
		cache:  make(map[string]*Preset),
	}
}

func NewManagerFromEmbed(fsys fs.FS) *Manager {
	return NewManager(fsys, "presets")
}

func (m *Manager) Load(name string) (*Preset, error) {
	m.mu.RLock()
	if p, ok := m.cache[name]; ok {
		m.mu.RUnlock()
		return p, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.cache[name]; ok {
		return p, nil
	}

	path := m.prefix + "/" + name + ".json"
	data, err := fs.ReadFile(m.fs, path)
	if err != nil {
		return nil, fmt.Errorf("preset %q not found: %w", name, err)
	}

	var p Preset
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse preset %q: %w", name, err)
	}

	m.cache[name] = &p
	slog.Debug("Loaded preset", "name", name)
	return &p, nil
}

func Apply(body map[string]interface{}, p *Preset) {
	if p.System != "" {
		systemPrompt := map[string]interface{}{
			"type": "text",
			"text": p.System,
		}
		if sys, ok := body["system"].([]interface{}); ok {
			body["system"] = append(sys, systemPrompt)
		}
	}

	hasThinking := false
	if thinking, ok := body["thinking"].(map[string]interface{}); ok {
		if thinkingType, ok := thinking["type"].(string); ok && thinkingType == "enabled" {
			hasThinking = true
		}
	}

	suffix := p.Suffix
	if hasThinking {
		suffix = p.SuffixEt
	}

	if suffix == "" {
		return
	}

	msgs, ok := body["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		return
	}

	lastUserIdx := -1
	for i, msg := range msgs {
		if m, ok := msg.(map[string]interface{}); ok {
			if m["role"] == "user" {
				lastUserIdx = i
			}
		}
	}

	if lastUserIdx == -1 {
		return
	}

	suffixMsg := map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": suffix,
			},
		},
	}

	newMsgs := make([]interface{}, 0, len(msgs)+1)
	newMsgs = append(newMsgs, msgs[:lastUserIdx+1]...)
	newMsgs = append(newMsgs, suffixMsg)
	newMsgs = append(newMsgs, msgs[lastUserIdx+1:]...)
	body["messages"] = newMsgs

	slog.Debug("Applied preset suffix")
}
