package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/air-mixer/i18n"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
	"github.com/yeixio/air-mixer/internal/protocols/bluetooth"
	"github.com/yeixio/air-mixer/internal/protocols/cast"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/local"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
	"github.com/yeixio/air-mixer/internal/protocols/pipe"
	"github.com/yeixio/air-mixer/internal/protocols/pulse"
	"github.com/yeixio/air-mixer/internal/protocols/radio"
	"github.com/yeixio/air-mixer/internal/protocols/snapcast"
	"github.com/yeixio/air-mixer/internal/protocols/upnp"
)

// Factory builds protocol adapters from stored configuration.
type Factory interface {
	Server(*db.Server) (protocols.Server, error)
	Client(*db.Client) (protocols.Client, error)
}

// Defaults are the settings used when a stored adapter leaves them out.
type Defaults struct {
	AirPlayPort int    // first RTSP port for AirPlay receivers
	// AirPlayLastPort ends the range receivers without an address take
	// ports from; 0 means AirPlayPort only.
	AirPlayLastPort int
	Interface   string // network interface to advertise on

	// DataDir is where adapters keep state, such as Spotify credentials.
	DataDir string

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
	// Address is the listen address, such as ":5000". For AirPlay it
	// defaults to the configured AirPlay port on every interface; for a
	// UPnP renderer, to a free port.
	Address string `json:"address,omitempty"`
	// Interface overrides the interface the receiver is advertised on.
	Interface string `json:"interface,omitempty"`
	// URL is what a radio input plays.
	URL string `json:"url,omitempty"`
	// Source is what a pulse input records: a source's name or
	// description, a phone's Bluetooth address, or "<sink>.monitor".
	// Empty means the default source.
	Source string `json:"source,omitempty"`
	// Server is the PulseAudio server for a pulse or bluetooth input;
	// empty means $PULSE_SERVER or the default.
	Server string `json:"server,omitempty"`
	// Adapter, Device and Hidden configure a bluetooth input: the adapter
	// (default hci0), the one phone to record by address (default: any),
	// and whether to stay out of scans.
	Adapter string `json:"adapter,omitempty"`
	Device  string `json:"device,omitempty"`
	Hidden  bool   `json:"hidden,omitempty"`
	// Bitrate is a Spotify input's quality: 96, 160 or 320 (default).
	Bitrate int `json:"bitrate,omitempty"`
}

var macAddress = regexp.MustCompile(`^(?i)[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// needProgram reports a receiver program that isn't installed.
func needProgram(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return invalidCode("program_missing", map[string]any{"program": name})
	}
	return nil
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
	// Format is what a UPnP output streams: "wav", "mp3" (needs ffmpeg),
	// or empty for MP3 on Sonos when ffmpeg is installed and WAV otherwise.
	Format string `json:"format,omitempty"`
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
	case "bluetooth":
		if cfg.Device != "" && !macAddress.MatchString(cfg.Device) {
			return nil, invalidCode("bluetooth_bad_device", nil)
		}
		return bluetooth.New(bluetooth.Options{
			ID: s.ID, Name: s.Name, Adapter: cfg.Adapter, Device: cfg.Device, Hidden: cfg.Hidden, Server: cfg.Server, Logger: f.log,
		}), nil
	case "spotify":
		if err := needProgram(pipe.Librespot); err != nil {
			return nil, err
		}
		if cfg.Bitrate != 0 && cfg.Bitrate != 96 && cfg.Bitrate != 160 && cfg.Bitrate != 320 {
			return nil, invalidCode("spotify_bitrate", nil)
		}
		var cache string
		if f.defaults.DataDir != "" {
			cache = filepath.Join(f.defaults.DataDir, "spotify", s.ID)
		}
		return pipe.Spotify(pipe.SpotifyOptions{ID: s.ID, Name: s.Name, Bitrate: cfg.Bitrate, CacheDir: cache, Logger: f.log}), nil
	case "airplay2":
		if err := needProgram(pipe.ShairportSync); err != nil {
			return nil, err
		}
		port := 0
		if cfg.Address != "" {
			_, p, err := net.SplitHostPort(cfg.Address)
			if err != nil {
				p = strings.TrimPrefix(cfg.Address, ":")
			}
			if port, err = strconv.Atoi(p); err != nil || port < 1 || port > 65535 {
				return nil, invalidCode("bad_port", nil)
			}
		}
		return pipe.AirPlay2(pipe.AirPlay2Options{ID: s.ID, Name: s.Name, Port: port, Logger: f.log}), nil
	case "upnp":
		return upnp.NewServer(upnp.ServerOptions{ID: s.ID, Name: s.Name, Address: cfg.Address, Logger: f.log}), nil
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
	lastPort := 0
	if cfg.Address == "" {
		// Without a port of its own, each receiver takes the first free
		// one in the AirPlay range; mDNS tells senders which.
		cfg.Address = ":" + strconv.Itoa(f.defaults.AirPlayPort)
		lastPort = f.defaults.AirPlayLastPort
	}
	if cfg.Interface == "" {
		cfg.Interface = f.defaults.Interface
	}
	return airplay.NewServer(airplay.ServerOptions{
		ID: s.ID, Name: s.Name, Address: cfg.Address, LastPort: lastPort, Interface: cfg.Interface, Logger: f.log,
	}), nil
}

func (f *factory) Client(c *db.Client) (protocols.Client, error) {
	var cfg ClientConfig
	if err := parseConfig(c.Config, &cfg); err != nil {
		return nil, err
	}
	switch c.Protocol {
	case "airplay", "airplay2":
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
			AirPlay2:          c.Protocol == "airplay2",
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
	case "cast":
		addr := c.Address
		if addr == "" {
			addr = cfg.Address
		}
		if addr == "" {
			return nil, invalidCode("cast_needs_address", nil)
		}
		return cast.New(cast.Options{
			ID: c.ID, Name: c.Name, Address: addr, Streams: f.defaults.Streams,
			PublicURL: f.defaults.PublicURL, WebPort: f.defaults.WebPort, Logger: f.log,
		}), nil
	case "upnp":
		addr := c.Address
		if addr == "" {
			addr = cfg.Address
		}
		if addr == "" {
			return nil, invalidCode("upnp_needs_address", nil)
		}
		if cfg.Format != "" && cfg.Format != "wav" && cfg.Format != "mp3" {
			return nil, invalidCode("upnp_bad_format", nil)
		}
		return upnp.NewClient(upnp.ClientOptions{
			ID: c.ID, Name: c.Name, Address: addr, Format: cfg.Format, Streams: f.defaults.Streams,
			PublicURL: f.defaults.PublicURL, WebPort: f.defaults.WebPort, Logger: f.log,
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
