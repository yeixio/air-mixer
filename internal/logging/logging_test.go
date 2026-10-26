package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/yeixio/air-mixer/internal/config"
)

func TestNew(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, config.LoggingConfig{Level: "warn", Format: "json"})
	log.Info("hidden")
	log.Warn("shown", "k", 1)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not one JSON record: %q", buf.String())
	}
	if rec["msg"] != "shown" {
		t.Errorf("record = %v", rec)
	}

	buf.Reset()
	New(&buf, config.LoggingConfig{Level: "debug", Format: "text"}).Debug("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Errorf("text output = %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warning": slog.LevelWarn,
		"warn": slog.LevelWarn, "error": slog.LevelError, "bogus": slog.LevelInfo,
	} {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
