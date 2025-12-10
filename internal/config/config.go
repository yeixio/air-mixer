package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Audio    AudioConfig    `mapstructure:"audio"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host         string `mapstructure:"host"`
	WebPort      int    `mapstructure:"web_port"`
	AirPlayPort  int    `mapstructure:"airplay_port"`
	DataDir      string `mapstructure:"data_dir"`
	PublishName  string `mapstructure:"publish_name"`
	Interface    string `mapstructure:"interface"` // "auto" to auto-detect
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

// AudioConfig holds audio engine configuration
type AudioConfig struct {
	SampleRate   int     `mapstructure:"sample_rate"`
	Channels     int     `mapstructure:"channels"`
	BufferSize   int     `mapstructure:"buffer_size"`
	DefaultVolume float64 `mapstructure:"default_volume"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"` // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
}

// Load loads configuration from file and environment variables
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	
	// Add config paths
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.airmixer")
	viper.AddConfigPath("/etc/airmixer")
	
	// Set defaults
	setDefaults()
	
	// Read environment variables (with AIRMIXER_ prefix)
	viper.SetEnvPrefix("AIRMIXER")
	viper.AutomaticEnv()
	
	// Bind environment variables
	bindEnvVars()
	
	// Read config file (optional, won't error if not found)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found is OK, we'll use defaults and env vars
	}
	
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	
	// Expand paths and set defaults
	if err := normalizeConfig(&config); err != nil {
		return nil, fmt.Errorf("failed to normalize config: %w", err)
	}
	
	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.web_port", 9001)
	viper.SetDefault("server.airplay_port", 49152)
	viper.SetDefault("server.data_dir", "./data")
	viper.SetDefault("server.publish_name", "airmixer")
	viper.SetDefault("server.interface", "auto")
	
	viper.SetDefault("database.path", "./data/airmixer.db")
	
	viper.SetDefault("audio.sample_rate", 44100)
	viper.SetDefault("audio.channels", 2)
	viper.SetDefault("audio.buffer_size", 4096)
	viper.SetDefault("audio.default_volume", 1.0)
	
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "text")
}

// bindEnvVars binds environment variables to config keys
func bindEnvVars() {
	viper.BindEnv("server.host", "AIRMIXER_SERVER_HOST")
	viper.BindEnv("server.web_port", "AIRMIXER_SERVER_WEB_PORT")
	viper.BindEnv("server.airplay_port", "AIRMIXER_SERVER_AIRPLAY_PORT")
	viper.BindEnv("server.data_dir", "AIRMIXER_SERVER_DATA_DIR")
	viper.BindEnv("server.publish_name", "AIRMIXER_SERVER_PUBLISH_NAME")
	viper.BindEnv("server.interface", "AIRMIXER_SERVER_INTERFACE")
	
	viper.BindEnv("database.path", "AIRMIXER_DATABASE_PATH")
	
	viper.BindEnv("audio.sample_rate", "AIRMIXER_AUDIO_SAMPLE_RATE")
	viper.BindEnv("audio.channels", "AIRMIXER_AUDIO_CHANNELS")
	viper.BindEnv("audio.buffer_size", "AIRMIXER_AUDIO_BUFFER_SIZE")
	viper.BindEnv("audio.default_volume", "AIRMIXER_AUDIO_DEFAULT_VOLUME")
	
	viper.BindEnv("logging.level", "AIRMIXER_LOGGING_LEVEL")
	viper.BindEnv("logging.format", "AIRMIXER_LOGGING_FORMAT")
}

// normalizeConfig normalizes and validates configuration
func normalizeConfig(config *Config) error {
	// Expand data directory path
	if !filepath.IsAbs(config.Server.DataDir) {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		config.Server.DataDir = filepath.Join(wd, config.Server.DataDir)
	}
	
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(config.Server.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	
	// Expand database path if relative
	if !filepath.IsAbs(config.Database.Path) {
		config.Database.Path = filepath.Join(config.Server.DataDir, filepath.Base(config.Database.Path))
	}
	
	// Validate ports
	if config.Server.WebPort < 1 || config.Server.WebPort > 65535 {
		return fmt.Errorf("invalid web port: %d", config.Server.WebPort)
	}
	if config.Server.AirPlayPort < 1 || config.Server.AirPlayPort > 65535 {
		return fmt.Errorf("invalid AirPlay port: %d", config.Server.AirPlayPort)
	}
	
	// Validate audio settings
	if config.Audio.SampleRate < 8000 || config.Audio.SampleRate > 192000 {
		return fmt.Errorf("invalid sample rate: %d", config.Audio.SampleRate)
	}
	if config.Audio.Channels < 1 || config.Audio.Channels > 8 {
		return fmt.Errorf("invalid channel count: %d", config.Audio.Channels)
	}
	if config.Audio.DefaultVolume < 0.0 || config.Audio.DefaultVolume > 1.0 {
		return fmt.Errorf("invalid default volume: %f", config.Audio.DefaultVolume)
	}
	
	return nil
}

// Save saves the current configuration to a file
func Save(config *Config, path string) error {
	viper.Set("server", config.Server)
	viper.Set("database", config.Database)
	viper.Set("audio", config.Audio)
	viper.Set("logging", config.Logging)
	
	return viper.WriteConfigAs(path)
}

// GetConfigFile returns the path to the config file being used
func GetConfigFile() string {
	return viper.ConfigFileUsed()
}

