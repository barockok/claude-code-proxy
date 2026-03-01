package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

const LevelTrace = slog.Level(-8)

func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func Init(level string, w io.Writer) {
	if w == nil {
		w = os.Stderr
	}

	lvl := ParseLevel(level)
	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if a.Value.Any().(slog.Level) == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
			}
			return a
		},
	}

	handler := slog.NewTextHandler(w, opts)
	slog.SetDefault(slog.New(handler))
}
