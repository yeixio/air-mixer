package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
	"github.com/yeixio/air-mixer/internal/protocols/local"
)

// Factory builds protocol adapters from stored configuration.
type Factory interface {
	Server(*db.Server) (protocols.Server, error)
	Client(*db.Client) (protocols.Client, error)
}

// Defaults are the settings used when a stored adapter leaves them out.
type Defaults struct {
	AirPlayPort int    // RTSP port for AirPlay receivers
	Interface   string // network interface to advertise on
}

// ServerConfig is the JSON stored in db.Server.Config.
type ServerConfig struct {
	// Address is the listen address, such as ":5000". Defaults to the
	// configured AirPlay port on every interface.
	Address string `json:"address,omitempty"`
	// Interface overrides the interface the receiver is advertised on.
	Interface string `json:"interface,omitempty"`
}

// ClientConfig is the JSON stored in db.Client.Config.
type ClientConfig struct {
	// Address is a fallback for db.Client.Address, for older records.
	Address string `json:"address,omitempty"`
	// LatencyMS is the AirPlay playback delay. Defaults to 2000.
	LatencyMS int `json:"latency_ms,omitempty"`
	// DisableEncryption sends AirPlay audio unencrypted.
	DisableEncryption bool `json:"disable_encryption,omitempty"`
}

// parseConfig decodes an adapter's JSON config. Unknown fields are kept
// by the database and ignored here.
func parseConfig(s string, v any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return invalid("config is not valid JSON: %v", err)
	}
	return nil
}

// NewFactory returns the Factory for the built-in protocols.
func NewFactory(d Defaults, logger *slog.Logger) Factory {
	if logger == nil {
		logger = slog.Default()
	}
	return &factory{defaults: d, log: logger}
}

type factory struct {
	defaults Defaults
	log      *slog.Logger
}

func (f *factory) Server(s *db.Server) (protocols.Server, error) {
	var cfg ServerConfig
	if err := parseConfig(s.Config, &cfg); err != nil {
		return nil, err
	}
	if s.Protocol != "airplay" {
		return nil, invalid("unsupported server protocol %q", s.Protocol)
	}
	if cfg.Address == "" {
		cfg.Address = ":" + strconv.Itoa(f.defaults.AirPlayPort)
	}
	if cfg.Interface == "" {
		cfg.Interface = f.defaults.Interface
	}
	return airplay.NewServer(airplay.ServerOptions{
		ID: s.ID, Name: s.Name, Address: cfg.Address, Interface: cfg.Interface, Logger: f.log,
	}), nil
}

func (f *factory) Client(c *db.Client) (protocols.Client, error) {
	var cfg ClientConfig
	if err := parseConfig(c.Config, &cfg); err != nil {
		return nil, err
	}
	switch c.Protocol {
	case "airplay":
		addr := c.Address
		if addr == "" {
			addr = cfg.Address
		}
		if addr == "" {
			return nil, invalid("an AirPlay client needs an address")
		}
		if cfg.LatencyMS < 0 {
			return nil, invalid("latency_ms must not be negative")
		}
		return airplay.NewClient(airplay.ClientOptions{
			ID: c.ID, Name: c.Name, Address: addr,
			Latency:           time.Duration(cfg.LatencyMS) * time.Millisecond,
			DisableEncryption: cfg.DisableEncryption,
			Logger:            f.log,
		}), nil
	case "local":
		return local.NewClient(c.ID, c.Name, f.log), nil
	}
	return nil, invalid("unsupported client protocol %q", c.Protocol)
}

// InvalidError reports a request that cannot be carried out as given.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}
