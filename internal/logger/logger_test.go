package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestInit(t *testing.T) {
	var buf bytes.Buffer
	Init("debug", &buf)

	slog.Info("test message", "key", "value")
	output := buf.String()

	if !strings.Contains(output, "test message") {
		t.Errorf("expected log output to contain 'test message', got: %s", output)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	Init("warn", &buf)

	slog.Info("should not appear")
	slog.Warn("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("info message should be filtered at warn level")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("warn message should appear at warn level")
	}
}
