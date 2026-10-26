// Package config loads Air-Mixer's settings from defaults, an optional YAML
// file and AIRMIXER_* environment variables, in increasing precedence.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the application configuration.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

// ServerConfig holds network and storage settings.
type ServerConfig struct {
	// Host is the address the web UI and API listen on.
	Host string `mapstructure:"host"`
	// WebPort is the web UI and API port.
	WebPort int `mapstructure:"web_port"`
	// AirPlayPort is the default RTSP port for AirPlay receivers.
	AirPlayPort int `mapstructure:"airplay_port"`
	// DataDir holds the database and other state.
	DataDir string `mapstructure:"data_dir"`
	// Interface is the network interface AirPlay receivers advertise on,
	// or "auto".
	Interface string `mapstructure:"interface"`
}

// DatabaseConfig holds database settings.
type DatabaseConfig struct {
	// Path to the SQLite file. Empty means airmixer.db in DataDir.
	Path string `mapstructure:"path"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // text, json
}

// Addr returns the host:port the HTTP server listens on.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.WebPort)
}

// Load reads the configuration. If file is empty, config.yaml is looked up
// in the working directory, $HOME/.airmixer and /etc/airmixer, and a missing
// file is not an error.
func Load(file string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("AIRMIXER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if file != "" {
		v.SetConfigFile(file)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.airmixer")
		v.AddConfigPath("/etc/airmixer")
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if file != "" || !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = filepath.Join(cfg.Server.DataDir, "airmixer.db")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.web_port", 9001)
	v.SetDefault("server.airplay_port", 49152)
	v.SetDefault("server.data_dir", "./data")
	v.SetDefault("server.interface", "auto")
	v.SetDefault("database.path", "")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "text")
}

// Validate reports the first invalid setting.
func (c *Config) Validate() error {
	validPort := func(p int) bool { return p > 0 && p < 65536 }
	switch {
	case !validPort(c.Server.WebPort):
		return fmt.Errorf("config: invalid server.web_port %d", c.Server.WebPort)
	case !validPort(c.Server.AirPlayPort):
		return fmt.Errorf("config: invalid server.airplay_port %d", c.Server.AirPlayPort)
	case c.Server.DataDir == "":
		return errors.New("config: server.data_dir is required")
	}
	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("config: invalid logging.level %q", c.Logging.Level)
	}
	switch strings.ToLower(c.Logging.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("config: invalid logging.format %q", c.Logging.Format)
	}
	return nil
}
