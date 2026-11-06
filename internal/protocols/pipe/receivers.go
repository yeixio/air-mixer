package pipe

import (
	"context"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Librespot and ShairportSync are the programs the Spotify Connect and
// AirPlay 2 inputs run.
const (
	Librespot     = "librespot"
	ShairportSync = "shairport-sync"
)

// SpotifyOptions configures a Spotify Connect input.
type SpotifyOptions struct {
	ID, Name string
	// Bitrate is 96, 160 or 320 kbit/s; 320 if 0.
	Bitrate int
	// CacheDir keeps librespot's credentials, so the speaker stays signed
	// in to the account that last played to it.
	CacheDir string
	Logger   *slog.Logger
}

// Spotify returns an input that shows up in Spotify apps as a Spotify
// Connect speaker, through librespot.
func Spotify(o SpotifyOptions) *Server {
	if o.Bitrate == 0 {
		o.Bitrate = 320
	}
	args := []string{Librespot,
		"--name", o.Name,
		"--backend", "pipe", "--format", "S16",
		"--bitrate", strconv.Itoa(o.Bitrate),
		"--device-type", "speaker",
		"--initial-volume", "100",
		"--disable-audio-cache",
	}
	if o.CacheDir != "" {
		args = append(args, "--cache", o.CacheDir)
	}
	return New(Options{ID: o.ID, Name: o.Name, Protocol: "spotify", Command: args, Logger: o.Logger})
}

// AirPlay2Options configures an AirPlay 2 input.
type AirPlay2Options struct {
	ID, Name string
	// Port is the RTSP port; shairport-sync's default (7000) if 0.
	Port   int
	Logger *slog.Logger
}

// AirPlay2 returns an input that shows up on Apple devices as an AirPlay 2
// speaker, which they can add to multi-room groups, through
// shairport-sync built with AirPlay 2 support (and nqptp running).
func AirPlay2(o AirPlay2Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if v, ok := shairportAirPlay2(); !ok && v != "" {
		// Without AirPlay 2 support, shairport-sync is a plain AirPlay
		// receiver, which the built-in airplay input already is.
		o.Logger.Warn("shairport-sync was built without AirPlay 2 support; Apple devices will see an AirPlay 1 speaker", "version", v)
	}
	args := []string{ShairportSync, "--name", o.Name, "--output", "stdout"}
	if o.Port != 0 {
		args = append(args, "--port", strconv.Itoa(o.Port))
	}
	return New(Options{ID: o.ID, Name: o.Name, Protocol: "airplay2", Command: args, Logger: o.Logger})
}

// shairportAirPlay2 returns shairport-sync's version string and whether it
// lists AirPlay 2 support, as "4.3.7-AirPlay2-smi10-OpenSSL-…" does. The
// version is empty if the program didn't run.
func shairportAirPlay2() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ShairportSync, "-V").CombinedOutput()
	v := strings.TrimSpace(string(out))
	if err != nil && v == "" {
		return "", false
	}
	return v, strings.Contains(v, "AirPlay2")
}
