// Package logging configures the standard library's structured logger.
package logging

import (
	"io"
	"log/slog"
	"strings"

	"github.com/yeixio/air-mixer/internal/config"
)

// New returns a logger writing to w at the configured level and format.
func New(w io.Writer, cfg config.LoggingConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: ParseLevel(cfg.Level)}
	if strings.EqualFold(cfg.Format, "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// ParseLevel maps a level name to a slog level, defaulting to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
