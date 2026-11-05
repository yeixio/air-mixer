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
	"time"

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

// Options configures a local output.
type Options struct {
	ID, Name string
	// Delay holds audio back before playing it, to line this output up
	// with slower ones.
	Delay  time.Duration
	Logger *slog.Logger
}

// Client plays audio on the local default output device.
type Client struct {
	id, name string
	log      *slog.Logger
	buf      *audio.Playout

	mu        sync.Mutex
	player    player
	connected bool
}

// NewClient returns a local output. Call Connect to open the device.
func NewClient(o Options) *Client {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Client{
		id: o.ID, name: o.Name,
		log: o.Logger.With("component", "local-client", "client", o.ID),
		buf: audio.NewPlayout(maxBuffered, audio.Samples(o.Delay)),
	}
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
	p, err := newPlayer(&reader{c: c})
	if err != nil {
		return err
	}
	c.buf.Reset()
	p.Play()
	c.player, c.connected = p, true
	c.log.Info("local output opened")
	return nil
}

// Disconnect stops playback and releases the device.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	p := c.player
	c.player, c.connected = nil, false
	c.mu.Unlock()
	c.buf.Reset()
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
	connected := c.connected
	c.mu.Unlock()
	if !connected {
		return protocols.ErrNotConnected
	}
	if !c.buf.Push(p.Samples) {
		return protocols.ErrBufferFull
	}
	return nil
}

// reader feeds queued audio to the device, padding with silence.
type reader struct {
	c       *Client
	scratch []float32
}

func (r *reader) Read(b []byte) (int, error) {
	n := len(b) / 4
	if n == 0 {
		return 0, nil
	}
	if cap(r.scratch) < n {
		r.scratch = make([]float32, n)
	}
	samples := r.scratch[:n]
	r.c.buf.Fill(samples)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(s))
	}
	return 4 * n, nil
}
