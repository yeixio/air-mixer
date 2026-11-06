package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/yeixio/air-mixer/i18n"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/local"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
	"github.com/yeixio/air-mixer/internal/protocols/pulse"
	"github.com/yeixio/air-mixer/internal/protocols/radio"
	"github.com/yeixio/air-mixer/internal/protocols/snapcast"
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

	// For stream and Matinee adapters.
	Version   string
	WebPort   int                  // where the API (and so streams) is served
	PublicURL string               // how other machines reach the API; empty to work it out
	Streams   *httpstream.Registry // where stream outputs are served from
	Matinee   *matinee.Account     // Air-Mixer's sign-in to Matinee
	Settings  matinee.Settings     // remembers each Matinee output's station
}

// ServerConfig is the JSON stored in db.Server.Config.
type ServerConfig struct {
	// Address is the listen address, such as ":5000". Defaults to the
	// configured AirPlay port on every interface.
	Address string `json:"address,omitempty"`
	// Interface overrides the interface the receiver is advertised on.
	Interface string `json:"interface,omitempty"`
	// URL is what a radio input plays.
	URL string `json:"url,omitempty"`
	// Source is what a pulse input records: a source's name or
	// description, a phone's Bluetooth address, or "<sink>.monitor".
	// Empty means the default source.
	Source string `json:"source,omitempty"`
	// Server is the PulseAudio server for a pulse input; empty means
	// $PULSE_SERVER or the default.
	Server string `json:"server,omitempty"`
}

// ClientConfig is the JSON stored in db.Client.Config.
type ClientConfig struct {
	// Address is a fallback for db.Client.Address, for older records.
	Address string `json:"address,omitempty"`
	// LatencyMS is the AirPlay playback delay. Defaults to 2000.
	LatencyMS int `json:"latency_ms,omitempty"`
	// DisableEncryption sends AirPlay audio unencrypted.
	DisableEncryption bool `json:"disable_encryption,omitempty"`
	// DelayMS holds audio back on local and pulse outputs, to line up
	// speakers with different latencies, such as Bluetooth speakers in
	// one room.
	DelayMS int `json:"delay_ms,omitempty"`
	// Sink is the PulseAudio sink a pulse output plays to: its name, its
	// description, or a Bluetooth address such as "AA:BB:CC:DD:EE:FF".
	// Empty means the default sink.
	Sink string `json:"sink,omitempty"`
	// Server is the PulseAudio server for a pulse output, such as
	// "unix:/run/pulse/native". Empty means $PULSE_SERVER or the default.
	Server string `json:"server,omitempty"`
	// SampleRate is the rate a Snapcast output's source expects; 48000
	// if 0, snapserver's default.
	SampleRate int `json:"sample_rate,omitempty"`
	// LibraryID is the Matinee music library a Matinee output's station
	// goes in; 0 for the first one.
	LibraryID int64 `json:"library_id,omitempty"`
}

// radioSchemes are the URL schemes a radio input plays; rtsp and rtmp
// need ffmpeg.
var radioSchemes = []string{"http", "https", "rtsp", "rtmp"}

// maxDelayMS bounds delay_ms; more than this is a typo, not a calibration.
const maxDelayMS = 5000

// parseConfig decodes an adapter's JSON config. Unknown fields are kept
// by the database and ignored here.
func parseConfig(s string, v any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return invalidCode("invalid_config", map[string]any{"detail": err.Error()})
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
	if s.Protocol == "matinee" {
		if f.defaults.Matinee == nil {
			return nil, invalidCode("matinee_unavailable", nil)
		}
		return matinee.NewSource(matinee.SourceOptions{
			ID: s.ID, Name: s.Name, Account: f.defaults.Matinee, Version: f.defaults.Version, Logger: f.log,
		}), nil
	}
	switch s.Protocol {
	case "airplay":
	case "pulse":
		return pulse.NewSource(pulse.SourceOptions{
			ID: s.ID, Name: s.Name, Server: cfg.Server, Source: cfg.Source, Logger: f.log,
		}), nil
	case "radio":
		if cfg.URL == "" {
			return nil, invalidCode("radio_needs_url", nil)
		}
		if u, err := url.Parse(cfg.URL); err != nil || u.Host == "" || !slices.Contains(radioSchemes, u.Scheme) {
			return nil, invalidCode("radio_bad_url", nil)
		}
		return radio.New(radio.Options{ID: s.ID, Name: s.Name, URL: cfg.URL, Logger: f.log}), nil
	default:
		return nil, invalidCode("unsupported_server_protocol", map[string]any{"protocol": s.Protocol})
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
			return nil, invalidCode("airplay_needs_address", nil)
		}
		if cfg.LatencyMS < 0 {
			return nil, invalidCode("negative_latency", nil)
		}
		return airplay.NewClient(airplay.ClientOptions{
			ID: c.ID, Name: c.Name, Address: addr,
			Latency:           time.Duration(cfg.LatencyMS) * time.Millisecond,
			DisableEncryption: cfg.DisableEncryption,
			Logger:            f.log,
		}), nil
	case "local", "pulse":
		if cfg.DelayMS < 0 || cfg.DelayMS > maxDelayMS {
			return nil, invalidCode("delay_range", map[string]any{"max": maxDelayMS})
		}
		delay := time.Duration(cfg.DelayMS) * time.Millisecond
		if c.Protocol == "local" {
			return local.NewClient(local.Options{ID: c.ID, Name: c.Name, Delay: delay, Logger: f.log}), nil
		}
		return pulse.NewClient(pulse.Options{
			ID: c.ID, Name: c.Name, Server: cfg.Server, Sink: cfg.Sink, Delay: delay, Logger: f.log,
		}), nil
	case "snapcast":
		addr := c.Address
		if addr == "" {
			addr = cfg.Address
		}
		if addr == "" {
			return nil, invalidCode("snapcast_needs_address", nil)
		}
		if cfg.SampleRate != 0 && (cfg.SampleRate < 8000 || cfg.SampleRate > 192000) {
			return nil, invalidCode("sample_rate_range", nil)
		}
		sc, err := snapcast.New(snapcast.Options{ID: c.ID, Name: c.Name, Address: addr, SampleRate: cfg.SampleRate, Logger: f.log})
		if err != nil {
			return nil, err
		}
		return sc, nil
	case "stream":
		return httpstream.New(c.ID, c.Name, f.defaults.Streams, f.log), nil
	case "matinee":
		addr := c.Address
		if addr == "" {
			addr = cfg.Address
		}
		if addr == "" {
			return nil, invalidCode("matinee_needs_player", nil)
		}
		if f.defaults.Matinee == nil {
			return nil, invalidCode("matinee_unavailable", nil)
		}
		return matinee.NewSpeaker(matinee.SpeakerOptions{
			ID: c.ID, Name: c.Name, Player: addr, LibraryID: cfg.LibraryID,
			Account: f.defaults.Matinee, Streams: f.defaults.Streams, Settings: f.defaults.Settings,
			PublicURL: f.defaults.PublicURL, WebPort: f.defaults.WebPort, Logger: f.log,
		}), nil
	}
	return nil, invalidCode("unsupported_client_protocol", map[string]any{"protocol": c.Protocol})
}

// InvalidError reports a request that cannot be carried out as given. Code
// and Params, when set, name the message in the i18n error catalog, so
// clients can show it in their reader's language.
type InvalidError struct {
	Reason string
	Code   string
	Params map[string]any
}

func (e *InvalidError) Error() string { return e.Reason }

// invalid is an InvalidError with an English reason only.
func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}

// invalidCode is an InvalidError from the i18n error catalog.
func invalidCode(code string, params map[string]any) error {
	return &InvalidError{Reason: i18n.Message(code, params), Code: code, Params: params}
}
