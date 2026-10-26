// Package local plays audio on the machine Air-Mixer runs on, through the
// system's default output device.
package local

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"

	"github.com/ebitengine/oto/v3"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// maxBuffered bounds the audio queued for the device, and so the latency
// added when the source runs ahead of it.
const maxBuffered = audio.SampleRate * audio.Channels / 2 // 500 ms

// player is the part of an oto.Player the client uses.
type player interface {
	Play()
	Close() error
}

// newPlayer creates a player pulling float32 little-endian audio in the
// internal format from r. Tests replace it.
var newPlayer = otoPlayer

var (
	otoOnce sync.Once
	otoCtx  *oto.Context
	otoErr  error
)

// otoPlayer opens the process-wide oto context on first use; oto allows
// only one per process, so every local output shares it.
func otoPlayer(r io.Reader) (player, error) {
	otoOnce.Do(func() {
		var ready chan struct{}
		otoCtx, ready, otoErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   audio.SampleRate,
			ChannelCount: audio.Channels,
			Format:       oto.FormatFloat32LE,
		})
		if otoErr == nil {
			<-ready
		}
	})
	if otoErr != nil {
		return nil, fmt.Errorf("local: open audio device: %w", otoErr)
	}
	return otoCtx.NewPlayer(r), nil
}

// Client plays audio on the local default output device.
type Client struct {
	id, name string
	log      *slog.Logger

	mu        sync.Mutex
	player    player
	connected bool
	queue     []float32
}

// NewClient returns a local output. Call Connect to open the device.
func NewClient(id, name string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{id: id, name: name, log: logger.With("component", "local-client", "client", id)}
}

func (c *Client) ID() string       { return c.id }
func (c *Client) Name() string     { return c.name }
func (c *Client) Protocol() string { return "local" }

// Connected reports whether the device is open.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Connect opens the output device and starts playback; until audio arrives
// it plays silence.
func (c *Client) Connect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	p, err := newPlayer(reader{c})
	if err != nil {
		return err
	}
	p.Play()
	c.player, c.connected, c.queue = p, true, nil
	c.log.Info("local output opened")
	return nil
}

// Disconnect stops playback and releases the device.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	p := c.player
	c.player, c.connected, c.queue = nil, false, nil
	c.mu.Unlock()
	if p == nil {
		return nil
	}
	c.log.Info("local output closed")
	return p.Close()
}

// Send queues audio in the internal format.
func (c *Client) Send(p protocols.Packet) error {
	if p.Format != audio.Internal {
		return fmt.Errorf("local: expected %+v audio, got %+v", audio.Internal, p.Format)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return protocols.ErrNotConnected
	}
	if len(c.queue)+len(p.Samples) > maxBuffered {
		return protocols.ErrBufferFull
	}
	c.queue = append(c.queue, p.Samples...)
	return nil
}

// reader feeds queued audio to the device, padding with silence.
type reader struct{ c *Client }

func (r reader) Read(b []byte) (int, error) {
	n := len(b) / 4
	if n == 0 {
		return 0, nil
	}
	r.c.mu.Lock()
	k := min(n, len(r.c.queue))
	for i, s := range r.c.queue[:k] {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(s))
	}
	r.c.queue = r.c.queue[k:]
	r.c.mu.Unlock()
	clear(b[4*k : 4*n])
	return 4 * n, nil
}
