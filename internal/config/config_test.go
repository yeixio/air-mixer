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

func TestSetup(t *testing.T) {
	file := filepath.Join(t.TempDir(), "airmixer.yaml")
	os.WriteFile(file, []byte(`
setup:
  servers:
    - name: Living Room
      protocol: airplay
  clients:
    - name: Left
      protocol: pulse
      enabled: false
      config:
        sink: "AA:BB:CC:DD:EE:FF"
        delay_ms: 40
  routes:
    - name: Living Room
      source: Living Room
      destinations: [Left]
      volume: 0.8
`), 0o600)
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Setup
	if len(s.Servers) != 1 || s.Servers[0].Name != "Living Room" || s.Servers[0].Enabled != nil {
		t.Errorf("servers = %+v", s.Servers)
	}
	if len(s.Clients) != 1 || s.Clients[0].Config["sink"] != "AA:BB:CC:DD:EE:FF" || *s.Clients[0].Enabled {
		t.Errorf("clients = %+v", s.Clients)
	}
	if len(s.Routes) != 1 || *s.Routes[0].Volume != 0.8 || s.Routes[0].Destinations[0] != "Left" {
		t.Errorf("routes = %+v", s.Routes)
	}
}

func TestAirPlayPorts(t *testing.T) {
	for _, tc := range []struct {
		port        int
		rng         string
		first, last int
	}{
		{49152, "", 49152, 49199},
		{49200, "", 49200, 49247},
		{49152, "50000-50009", 50000, 50009},
		{65530, "", 65530, 65535},
	} {
		f, l := ServerConfig{AirPlayPort: tc.port, AirPlayPortRange: tc.rng}.AirPlayPorts()
		if f != tc.first || l != tc.last {
			t.Errorf("%d %q = %d-%d, want %d-%d", tc.port, tc.rng, f, l, tc.first, tc.last)
		}
	}
	for _, bad := range []string{"50000", "50009-50000", "0-10", "a-b", "1-70000"} {
		cfg := Config{Server: ServerConfig{WebPort: 1, AirPlayPort: 2, AirPlayPortRange: bad, DataDir: "x"}, Logging: LoggingConfig{Level: "info", Format: "text"}}
		if err := cfg.Validate(); err == nil {
			t.Errorf("range %q accepted", bad)
		}
	}
}
