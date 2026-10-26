package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.WebPort != 9001 || cfg.Server.AirPlayPort != 49152 || cfg.Server.Interface != "auto" {
		t.Errorf("server defaults = %+v", cfg.Server)
	}
	if cfg.Database.Path != filepath.Join("data", "airmixer.db") {
		t.Errorf("database path = %q", cfg.Database.Path)
	}
	if cfg.Addr() != "0.0.0.0:9001" {
		t.Errorf("Addr() = %q", cfg.Addr())
	}
}

func TestFileAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "airmixer.yaml")
	os.WriteFile(file, []byte("server:\n  web_port: 8080\n  data_dir: /var/lib/airmixer\nlogging:\n  level: debug\n"), 0o600)
	t.Setenv("AIRMIXER_SERVER_WEB_PORT", "9090")
	t.Setenv("AIRMIXER_LOGGING_FORMAT", "json")

	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.WebPort != 9090 {
		t.Errorf("env should override file: web_port = %d", cfg.Server.WebPort)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" {
		t.Errorf("logging = %+v", cfg.Logging)
	}
	if cfg.Database.Path != "/var/lib/airmixer/airmixer.db" {
		t.Errorf("database path = %q", cfg.Database.Path)
	}
}

func TestMissingExplicitFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("expected error for a missing explicit config file")
	}
}

func TestValidation(t *testing.T) {
	for env, val := range map[string]string{
		"AIRMIXER_SERVER_WEB_PORT":     "0",
		"AIRMIXER_SERVER_AIRPLAY_PORT": "70000",
		"AIRMIXER_LOGGING_LEVEL":       "loud",
		"AIRMIXER_LOGGING_FORMAT":      "xml",
	} {
		t.Run(env, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(env, val)
			_, err := Load("")
			if err == nil || !strings.HasPrefix(err.Error(), "config:") {
				t.Errorf("%s=%q: err = %v", env, val, err)
			}
		})
	}
}

func TestValidateRequiresDataDir(t *testing.T) {
	cfg := Config{Server: ServerConfig{WebPort: 1, AirPlayPort: 2}, Logging: LoggingConfig{Level: "info", Format: "text"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected empty data_dir to be rejected")
	}
	cfg.Server.DataDir = "d"
	if err := cfg.Validate(); err != nil {
		t.Error(err)
	}
}
